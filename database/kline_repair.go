package database

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type KlineRepairTask struct {
	TaskKey       string     `gorm:"primaryKey;column:task_key"`
	Provider      string     `gorm:"column:provider"`
	MarketID      string     `gorm:"column:market_id"`
	SourceSymbol  string     `gorm:"column:source_symbol"`
	Interval      string     `gorm:"column:interval"`
	GapStart      time.Time  `gorm:"column:gap_start"`
	GapEnd        time.Time  `gorm:"column:gap_end"`
	Status        string     `gorm:"column:status"`
	AttemptCount  int        `gorm:"column:attempt_count"`
	NextAttemptAt time.Time  `gorm:"column:next_attempt_at"`
	LockedAt      *time.Time `gorm:"column:locked_at"`
	LastError     *string    `gorm:"column:last_error"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at"`
}

func (KlineRepairTask) TableName() string {
	return "kline_repair_task"
}

type KlineRepairDB interface {
	UpsertRepairTasks(tasks []KlineRepairTask) (int64, error)
	ClaimRepairTasks(provider string, limit int, now time.Time) ([]KlineRepairTask, error)
	CompleteRepairTask(taskKey string, completedAt time.Time) error
	RetryRepairTask(taskKey, summary string, retryAt time.Time, permanent bool) error
	QueryKlineOpenTimes(marketID, interval string, start, end time.Time) ([]time.Time, error)
}

type klineRepairDB struct {
	gorm *gorm.DB
}

func NewKlineRepairDB(db *gorm.DB) KlineRepairDB {
	return &klineRepairDB{gorm: db}
}

func RepairTaskKey(provider, marketID, interval string, start, end time.Time) string {
	canonical := strings.Join([]string{
		normalizedProvider(provider),
		strings.TrimSpace(marketID),
		strings.TrimSpace(interval),
		start.UTC().Format(time.RFC3339Nano),
		end.UTC().Format(time.RFC3339Nano),
	}, "|")
	digest := sha256.Sum256([]byte(canonical))
	return fmt.Sprintf("repair-%x", digest[:16])
}

func (k *klineRepairDB) UpsertRepairTasks(tasks []KlineRepairTask) (int64, error) {
	if len(tasks) == 0 {
		return 0, nil
	}
	now := time.Now().UTC()
	for index := range tasks {
		tasks[index].Provider = normalizedProvider(tasks[index].Provider)
		if tasks[index].TaskKey == "" {
			tasks[index].TaskKey = RepairTaskKey(
				tasks[index].Provider,
				tasks[index].MarketID,
				tasks[index].Interval,
				tasks[index].GapStart,
				tasks[index].GapEnd,
			)
		}
		if tasks[index].Status == "" {
			tasks[index].Status = "pending"
		}
		if tasks[index].NextAttemptAt.IsZero() {
			tasks[index].NextAttemptAt = now
		}
	}
	result := k.gorm.Table("kline_repair_task").
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "task_key"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"source_symbol":   gorm.Expr("EXCLUDED.source_symbol"),
				"status":          "pending",
				"attempt_count":   0,
				"next_attempt_at": gorm.Expr("EXCLUDED.next_attempt_at"),
				"locked_at":       nil,
				"last_error":      nil,
				"updated_at":      now,
			}),
			// A completed task is reopened only when the scanner still observes
			// the same deterministic gap. Permanent failures remain explicit.
			Where: clause.Where{Exprs: []clause.Expression{
				clause.Eq{
					Column: clause.Column{Table: "kline_repair_task", Name: "status"},
					Value:  "completed",
				},
			}},
		}).
		Create(&tasks)
	return result.RowsAffected, result.Error
}

func (k *klineRepairDB) ClaimRepairTasks(provider string, limit int, now time.Time) ([]KlineRepairTask, error) {
	if limit <= 0 {
		limit = 5
	}
	var tasks []KlineRepairTask
	err := k.gorm.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("kline_repair_task").
			Where("provider = ? AND status = ? AND locked_at < ?",
				normalizedProvider(provider), "running", now.Add(-10*time.Minute)).
			Updates(map[string]interface{}{
				"status":          "pending",
				"locked_at":       nil,
				"next_attempt_at": now,
				"last_error":      "reclaimed after stale worker lock",
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Table("kline_repair_task").
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("provider = ? AND status = ? AND next_attempt_at <= ?",
				normalizedProvider(provider), "pending", now).
			Order("created_at ASC").
			Limit(limit).
			Find(&tasks).Error; err != nil {
			return err
		}
		if len(tasks) == 0 {
			return nil
		}
		keys := make([]string, 0, len(tasks))
		for _, task := range tasks {
			keys = append(keys, task.TaskKey)
		}
		return tx.Table("kline_repair_task").
			Where("task_key IN ?", keys).
			Updates(map[string]interface{}{
				"status":        "running",
				"locked_at":     now,
				"attempt_count": gorm.Expr("attempt_count + 1"),
				"updated_at":    now,
			}).Error
	})
	if err != nil {
		return nil, err
	}
	for index := range tasks {
		tasks[index].Status = "running"
		tasks[index].AttemptCount++
		tasks[index].LockedAt = cloneTime(&now)
	}
	return tasks, nil
}

func (k *klineRepairDB) CompleteRepairTask(taskKey string, completedAt time.Time) error {
	return k.gorm.Table("kline_repair_task").
		Where("task_key = ?", taskKey).
		Updates(map[string]interface{}{
			"status":     "completed",
			"locked_at":  nil,
			"last_error": nil,
			"updated_at": completedAt,
		}).Error
}

func (k *klineRepairDB) RetryRepairTask(taskKey, summary string, retryAt time.Time, permanent bool) error {
	status := "pending"
	if permanent {
		status = "failed"
	}
	summary = truncateStatusValue(summary, 300)
	return k.gorm.Table("kline_repair_task").
		Where("task_key = ?", taskKey).
		Updates(map[string]interface{}{
			"status":          status,
			"locked_at":       nil,
			"last_error":      summary,
			"next_attempt_at": retryAt,
			"updated_at":      time.Now().UTC(),
		}).Error
}

func (k *klineRepairDB) QueryKlineOpenTimes(
	marketID, interval string,
	start, end time.Time,
) ([]time.Time, error) {
	var values []time.Time
	if err := k.gorm.Table("symbol_kline").
		Where("market_id = ? AND \"interval\" = ? AND open_time >= ? AND open_time < ? AND is_active = ?",
			marketID, interval, start, end, true).
		Order("open_time ASC").
		Pluck("open_time", &values).Error; err != nil {
		return nil, err
	}
	return values, nil
}
