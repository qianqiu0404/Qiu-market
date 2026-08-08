package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const klineRetentionAdvisoryLock int64 = 725810781

type KlineRetentionPolicy struct {
	Interval string
	Keep     time.Duration
}

// PersonalServerKlineRetentionPolicies uses the external-SSD capacity without
// turning the single-user staging host into an unbounded archive. Daily bars
// remain permanent; shorter intervals keep enough history for strategy and
// market-microstructure study while leaving generous headroom for models,
// backups, and container data.
func PersonalServerKlineRetentionPolicies() []KlineRetentionPolicy {
	return []KlineRetentionPolicy{
		{Interval: "1m", Keep: 30 * 24 * time.Hour},
		{Interval: "15m", Keep: 180 * 24 * time.Hour},
		{Interval: "1h", Keep: 2 * 365 * 24 * time.Hour},
	}
}

func KlineRetentionCutoff(interval string, now time.Time) (time.Time, bool) {
	for _, policy := range PersonalServerKlineRetentionPolicies() {
		if policy.Interval == interval {
			return now.UTC().Add(-policy.Keep), true
		}
	}
	return time.Time{}, false
}

type KlineRetentionResult struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Deleted    map[string]int64
	Skipped    bool
}

type KlineRetentionStatus struct {
	LastStartedAt *time.Time
	LastSuccessAt *time.Time
	LastError     string
	Deleted       map[string]int64
}

type KlineIntervalStorage struct {
	Interval string
	Rows     int64
	Oldest   *time.Time
	Newest   *time.Time
}

type KlineStorageStats struct {
	DatabaseBytes int64
	TableBytes    int64
	HeapBytes     int64
	IndexBytes    int64
	Rows          int64
	Intervals     []KlineIntervalStorage
	Retention     KlineRetentionStatus
}

type KlineRetentionDB interface {
	Run(context.Context, time.Time, []KlineRetentionPolicy, int) (KlineRetentionResult, error)
	QueryStatus(context.Context) (KlineRetentionStatus, error)
	QueryStorageStats(context.Context) (KlineStorageStats, error)
}

type klineRetentionDB struct {
	gorm *gorm.DB
}

func NewKlineRetentionDB(db *gorm.DB) KlineRetentionDB {
	return &klineRetentionDB{gorm: db}
}

func (k *klineRetentionDB) Run(
	ctx context.Context,
	now time.Time,
	policies []KlineRetentionPolicy,
	batchSize int,
) (KlineRetentionResult, error) {
	result := KlineRetentionResult{
		StartedAt: now.UTC(),
		Deleted:   make(map[string]int64, len(policies)),
	}
	if batchSize <= 0 || batchSize > 10_000 {
		return result, fmt.Errorf("K-line retention batch size must be in [1,10000]")
	}
	if err := validateKlineRetentionPolicies(policies); err != nil {
		return result, err
	}

	sqlDB, err := k.gorm.DB()
	if err != nil {
		return result, fmt.Errorf("open K-line retention SQL pool: %w", err)
	}
	lockConnection, err := sqlDB.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("reserve K-line retention lock connection: %w", err)
	}
	defer func() {
		_ = lockConnection.Close()
	}()

	var acquired bool
	if err := lockConnection.QueryRowContext(
		ctx,
		"SELECT pg_try_advisory_lock($1)",
		klineRetentionAdvisoryLock,
	).Scan(&acquired); err != nil {
		return result, fmt.Errorf("acquire K-line retention lock: %w", err)
	}
	if !acquired {
		result.Skipped = true
		return result, nil
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = lockConnection.ExecContext(
			unlockContext,
			"SELECT pg_advisory_unlock($1)",
			klineRetentionAdvisoryLock,
		)
	}()

	if err := k.recordStarted(ctx, result.StartedAt); err != nil {
		return result, err
	}
	for _, policy := range policies {
		cutoff := result.StartedAt.Add(-policy.Keep)
		for {
			deleted, err := k.deleteBatch(ctx, policy.Interval, cutoff, batchSize)
			if err != nil {
				_ = k.recordFailure(context.Background(), err)
				return result, err
			}
			result.Deleted[policy.Interval] += deleted
			if deleted < int64(batchSize) {
				break
			}
			select {
			case <-ctx.Done():
				err := ctx.Err()
				_ = k.recordFailure(context.Background(), err)
				return result, err
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	result.FinishedAt = time.Now().UTC()
	if err := k.recordSuccess(ctx, result); err != nil {
		return result, err
	}
	return result, nil
}

func validateKlineRetentionPolicies(policies []KlineRetentionPolicy) error {
	seen := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		switch policy.Interval {
		case "1m", "15m", "1h":
		default:
			return fmt.Errorf("unsupported retained K-line interval %q", policy.Interval)
		}
		if policy.Keep <= 0 {
			return fmt.Errorf("K-line retention for %s must be positive", policy.Interval)
		}
		if _, exists := seen[policy.Interval]; exists {
			return fmt.Errorf("duplicate K-line retention interval %q", policy.Interval)
		}
		seen[policy.Interval] = struct{}{}
	}
	return nil
}

func (k *klineRetentionDB) deleteBatch(
	ctx context.Context,
	interval string,
	cutoff time.Time,
	batchSize int,
) (int64, error) {
	tx := k.gorm.WithContext(ctx).Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	defer func() {
		if tx.Error != nil {
			_ = tx.Rollback().Error
		}
	}()
	if err := tx.Exec("SET LOCAL statement_timeout = '5s'").Error; err != nil {
		_ = tx.Rollback().Error
		return 0, err
	}
	deleted := tx.Exec(`
WITH victims AS (
	SELECT ctid
	FROM symbol_kline
	WHERE "interval" = ? AND open_time < ?
	ORDER BY open_time ASC
	LIMIT ?
)
DELETE FROM symbol_kline AS target
USING victims
WHERE target.ctid = victims.ctid
`, interval, cutoff.UTC(), batchSize)
	if deleted.Error != nil {
		_ = tx.Rollback().Error
		return 0, fmt.Errorf("delete expired %s K-lines: %w", interval, deleted.Error)
	}
	if err := tx.Commit().Error; err != nil {
		return 0, fmt.Errorf("commit expired %s K-lines: %w", interval, err)
	}
	return deleted.RowsAffected, nil
}

func (k *klineRetentionDB) recordStarted(ctx context.Context, started time.Time) error {
	return k.gorm.WithContext(ctx).Exec(`
INSERT INTO kline_retention_status(singleton, last_started_at, last_error, updated_at)
VALUES (TRUE, ?, '', clock_timestamp())
ON CONFLICT (singleton) DO UPDATE SET
	last_started_at = EXCLUDED.last_started_at,
	last_error = '',
	updated_at = clock_timestamp()
`, started.UTC()).Error
}

func (k *klineRetentionDB) recordSuccess(ctx context.Context, result KlineRetentionResult) error {
	payload, err := json.Marshal(result.Deleted)
	if err != nil {
		return err
	}
	return k.gorm.WithContext(ctx).Exec(`
UPDATE kline_retention_status
SET last_success_at = ?, last_error = '', deleted_rows = ?::jsonb,
	updated_at = clock_timestamp()
WHERE singleton = TRUE
`, result.FinishedAt.UTC(), string(payload)).Error
}

func (k *klineRetentionDB) recordFailure(ctx context.Context, cause error) error {
	return k.gorm.WithContext(ctx).Exec(`
UPDATE kline_retention_status
SET last_error = ?, updated_at = clock_timestamp()
WHERE singleton = TRUE
`, truncateRetentionError(cause.Error())).Error
}

func truncateRetentionError(value string) string {
	if len(value) <= 500 {
		return value
	}
	return value[:500]
}

func (k *klineRetentionDB) QueryStatus(ctx context.Context) (KlineRetentionStatus, error) {
	var row struct {
		LastStartedAt *time.Time `gorm:"column:last_started_at"`
		LastSuccessAt *time.Time `gorm:"column:last_success_at"`
		LastError     string     `gorm:"column:last_error"`
		DeletedJSON   string     `gorm:"column:deleted_json"`
	}
	result := k.gorm.WithContext(ctx).
		Table("kline_retention_status").
		Select("last_started_at, last_success_at, last_error, deleted_rows::text AS deleted_json").
		Where("singleton = TRUE").
		Limit(1).
		Scan(&row)
	if result.Error != nil {
		return KlineRetentionStatus{}, result.Error
	}
	if result.RowsAffected == 0 {
		return KlineRetentionStatus{Deleted: map[string]int64{}}, nil
	}
	deleted := make(map[string]int64)
	if row.DeletedJSON != "" {
		if err := json.Unmarshal([]byte(row.DeletedJSON), &deleted); err != nil {
			return KlineRetentionStatus{}, fmt.Errorf("decode K-line retention deleted rows: %w", err)
		}
	}
	return KlineRetentionStatus{
		LastStartedAt: row.LastStartedAt,
		LastSuccessAt: row.LastSuccessAt,
		LastError:     row.LastError,
		Deleted:       deleted,
	}, nil
}

func (k *klineRetentionDB) QueryStorageStats(ctx context.Context) (KlineStorageStats, error) {
	var stats KlineStorageStats
	var relation struct {
		DatabaseBytes int64 `gorm:"column:database_bytes"`
		TableBytes    int64 `gorm:"column:table_bytes"`
		HeapBytes     int64 `gorm:"column:heap_bytes"`
		IndexBytes    int64 `gorm:"column:index_bytes"`
		Rows          int64 `gorm:"column:rows"`
	}
	if err := k.gorm.WithContext(ctx).Raw(`
SELECT
	pg_database_size(current_database()) AS database_bytes,
	pg_total_relation_size('symbol_kline') AS table_bytes,
	pg_relation_size('symbol_kline') AS heap_bytes,
	pg_indexes_size('symbol_kline') AS index_bytes,
	(SELECT GREATEST(reltuples, 0)::bigint FROM pg_class WHERE oid = 'symbol_kline'::regclass) AS rows
`).Scan(&relation).Error; err != nil {
		return stats, err
	}
	stats.DatabaseBytes = relation.DatabaseBytes
	stats.TableBytes = relation.TableBytes
	stats.HeapBytes = relation.HeapBytes
	stats.IndexBytes = relation.IndexBytes
	stats.Rows = relation.Rows
	if err := k.gorm.WithContext(ctx).Raw(`
SELECT requested."interval",
	(SELECT min(open_time) FROM symbol_kline WHERE "interval" = requested."interval") AS oldest,
	(SELECT max(open_time) FROM symbol_kline WHERE "interval" = requested."interval") AS newest
FROM (VALUES ('1m'), ('15m'), ('1h'), ('1d')) AS requested("interval")
`).Scan(&stats.Intervals).Error; err != nil {
		return stats, err
	}
	status, err := k.QueryStatus(ctx)
	if err != nil && err != gorm.ErrRecordNotFound {
		return stats, err
	}
	stats.Retention = status
	return stats, nil
}
