package database

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const KlineV2AcceptanceStream = "kline-v2-reconciliation"

type DWAcceptanceState struct {
	StreamName                 string     `gorm:"primaryKey;column:stream_name"`
	ContinuousSuccessStartedAt *time.Time `gorm:"column:continuous_success_started_at"`
	LastAttemptAt              *time.Time `gorm:"column:last_attempt_at"`
	LastSuccessAt              *time.Time `gorm:"column:last_success_at"`
	ConsecutiveFailures        int        `gorm:"column:consecutive_failures"`
	SuccessfulCycles           int64      `gorm:"column:successful_cycles"`
	LastErrorSummary           *string    `gorm:"column:last_error_summary"`
	UpdatedAt                  time.Time  `gorm:"column:updated_at"`
}

func (DWAcceptanceState) TableName() string { return "dw_acceptance_state" }

type DWAcceptanceDB interface {
	RecordAttempt(streamName string, at time.Time) error
	RecordSuccess(streamName string, at time.Time) error
	RecordFailure(streamName string, at time.Time, summary string) error
	Query(streamName string) (*DWAcceptanceState, error)
}

type dwAcceptanceDB struct {
	gorm *gorm.DB
}

func NewDWAcceptanceDB(db *gorm.DB) DWAcceptanceDB {
	return &dwAcceptanceDB{gorm: db}
}

func (d *dwAcceptanceDB) RecordAttempt(streamName string, at time.Time) error {
	streamName = strings.TrimSpace(streamName)
	row := DWAcceptanceState{StreamName: streamName, LastAttemptAt: cloneTime(&at), UpdatedAt: at}
	return d.gorm.Table(row.TableName()).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "stream_name"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"last_attempt_at": at,
				"updated_at":      at,
			}),
		}).
		Create(&row).Error
}

func (d *dwAcceptanceDB) RecordSuccess(streamName string, at time.Time) error {
	streamName = strings.TrimSpace(streamName)
	row := DWAcceptanceState{
		StreamName: streamName, ContinuousSuccessStartedAt: cloneTime(&at),
		LastAttemptAt: cloneTime(&at), LastSuccessAt: cloneTime(&at),
		SuccessfulCycles: 1, UpdatedAt: at,
	}
	return d.gorm.Table(row.TableName()).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "stream_name"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"continuous_success_started_at": gorm.Expr(
					"COALESCE(dw_acceptance_state.continuous_success_started_at, ?)", at,
				),
				"last_attempt_at":      at,
				"last_success_at":      at,
				"consecutive_failures": 0,
				"successful_cycles":    gorm.Expr("dw_acceptance_state.successful_cycles + 1"),
				"last_error_summary":   nil,
				"updated_at":           at,
			}),
		}).
		Create(&row).Error
}

func (d *dwAcceptanceDB) RecordFailure(streamName string, at time.Time, summary string) error {
	streamName = strings.TrimSpace(streamName)
	summary = truncateStatusValue(summary, 300)
	row := DWAcceptanceState{
		StreamName: streamName, LastAttemptAt: cloneTime(&at),
		ConsecutiveFailures: 1, LastErrorSummary: cloneString(&summary), UpdatedAt: at,
	}
	return d.gorm.Table(row.TableName()).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "stream_name"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"continuous_success_started_at": nil,
				"last_attempt_at":               at,
				"consecutive_failures":          gorm.Expr("dw_acceptance_state.consecutive_failures + 1"),
				"last_error_summary":            summary,
				"updated_at":                    at,
			}),
		}).
		Create(&row).Error
}

func (d *dwAcceptanceDB) Query(streamName string) (*DWAcceptanceState, error) {
	var row DWAcceptanceState
	result := d.gorm.Table(row.TableName()).
		Where("stream_name = ?", strings.TrimSpace(streamName)).
		Take(&row)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &row, result.Error
}
