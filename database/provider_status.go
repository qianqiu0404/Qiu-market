package database

import (
	"encoding/json"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ProviderStatusDetails struct {
	ReceivedCount        int64 `json:"received_count,omitempty"`
	MatchedAssetCount    int64 `json:"matched_asset_count,omitempty"`
	PriceAvailableCount  int64 `json:"price_available_count,omitempty"`
	ChangeAvailableCount int64 `json:"change_available_count,omitempty"`
	WrittenCount         int64 `json:"written_count,omitempty"`
	ProbeObservedAt      int64 `json:"probe_observed_at,omitempty"`
}

type ProviderStatus struct {
	Provider             string         `gorm:"primaryKey;column:provider"`
	SourceKey            string         `gorm:"primaryKey;column:source_key"`
	LastAttemptAt        *time.Time     `gorm:"column:last_attempt_at"`
	LastSuccessAt        *time.Time     `gorm:"column:last_success_at"`
	NextRetryAt          *time.Time     `gorm:"column:next_retry_at"`
	WindowStartedAt      time.Time      `gorm:"column:window_started_at"`
	ObservationStartedAt *time.Time     `gorm:"column:observation_started_at"`
	AttemptCount         int64          `gorm:"column:attempt_count"`
	SuccessCount         int64          `gorm:"column:success_count"`
	ConsecutiveFailures  int            `gorm:"column:consecutive_failures"`
	LastSourceTime       *time.Time     `gorm:"column:last_source_time"`
	LastErrorClass       *string        `gorm:"column:last_error_class"`
	LastErrorSummary     *string        `gorm:"column:last_error_summary"`
	Details              datatypes.JSON `gorm:"column:details"`
	UpdatedAt            time.Time      `gorm:"column:updated_at"`
}

func (ProviderStatus) TableName() string {
	return "market_provider_status"
}

type ProviderStatusDB interface {
	RecordAttempt(provider, sourceKey string, attemptedAt time.Time) error
	RecordSuccess(provider, sourceKey string, succeededAt time.Time, sourceTime *time.Time) error
	RecordSuccessWithDetails(provider, sourceKey string, succeededAt time.Time, sourceTime *time.Time, details ProviderStatusDetails) error
	RecordFailure(provider, sourceKey string, failedAt time.Time, errorClass, summary string) error
	SetNextRetry(provider, sourceKey string, nextRetryAt *time.Time) error
	QueryProviderStatus(provider, sourceKey string) (*ProviderStatus, error)
	QueryProviderStatuses() ([]ProviderStatus, error)
}

type providerStatusDB struct {
	gorm *gorm.DB
}

func NewProviderStatusDB(db *gorm.DB) ProviderStatusDB {
	return &providerStatusDB{gorm: db}
}

func (p *providerStatusDB) RecordAttempt(provider, sourceKey string, attemptedAt time.Time) error {
	row := ProviderStatus{
		Provider:             normalizedProvider(provider),
		SourceKey:            strings.TrimSpace(sourceKey),
		LastAttemptAt:        cloneTime(&attemptedAt),
		WindowStartedAt:      attemptedAt,
		ObservationStartedAt: cloneTime(&attemptedAt),
		AttemptCount:         1,
		Details:              datatypes.JSON([]byte(`{}`)),
		UpdatedAt:            attemptedAt,
	}
	return p.gorm.Table(row.TableName()).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "provider"}, {Name: "source_key"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"last_attempt_at": attemptedAt,
				"observation_started_at": gorm.Expr(
					"CASE WHEN market_provider_status.attempt_count = 0 OR market_provider_status.observation_started_at IS NULL THEN ? ELSE market_provider_status.observation_started_at END",
					attemptedAt,
				),
				"attempt_count": gorm.Expr("market_provider_status.attempt_count + 1"),
				"updated_at":    attemptedAt,
			}),
		}).
		Create(&row).Error
}

func (p *providerStatusDB) RecordSuccess(
	provider, sourceKey string,
	succeededAt time.Time,
	sourceTime *time.Time,
) error {
	return p.recordSuccess(provider, sourceKey, succeededAt, sourceTime, nil)
}

func (p *providerStatusDB) RecordSuccessWithDetails(
	provider, sourceKey string,
	succeededAt time.Time,
	sourceTime *time.Time,
	details ProviderStatusDetails,
) error {
	return p.recordSuccess(provider, sourceKey, succeededAt, sourceTime, &details)
}

func (p *providerStatusDB) recordSuccess(
	provider, sourceKey string,
	succeededAt time.Time,
	sourceTime *time.Time,
	details *ProviderStatusDetails,
) error {
	rawDetails := datatypes.JSON([]byte(`{}`))
	if details != nil {
		encoded, err := json.Marshal(details)
		if err != nil {
			return err
		}
		rawDetails = datatypes.JSON(encoded)
	}
	row := ProviderStatus{
		Provider:             normalizedProvider(provider),
		SourceKey:            strings.TrimSpace(sourceKey),
		LastSuccessAt:        cloneTime(&succeededAt),
		LastSourceTime:       cloneTime(sourceTime),
		WindowStartedAt:      succeededAt,
		ObservationStartedAt: cloneTime(&succeededAt),
		AttemptCount:         1,
		SuccessCount:         1,
		Details:              rawDetails,
		UpdatedAt:            succeededAt,
	}
	assignments := map[string]interface{}{
		"last_success_at": succeededAt,
		"next_retry_at":   nil,
		"observation_started_at": gorm.Expr(
			"COALESCE(market_provider_status.observation_started_at, ?)",
			succeededAt,
		),
		"attempt_count": gorm.Expr(
			"GREATEST(market_provider_status.attempt_count, market_provider_status.success_count + 1)",
		),
		"success_count":        gorm.Expr("market_provider_status.success_count + 1"),
		"consecutive_failures": 0,
		"last_source_time":     sourceTime,
		"last_error_class":     nil,
		"last_error_summary":   nil,
		"updated_at":           succeededAt,
	}
	if details != nil {
		assignments["details"] = rawDetails
	}
	return p.gorm.Table(row.TableName()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider"}, {Name: "source_key"}},
			DoUpdates: clause.Assignments(assignments),
		}).
		Create(&row).Error
}

func (p ProviderStatus) ParsedDetails() ProviderStatusDetails {
	var details ProviderStatusDetails
	if len(p.Details) > 0 {
		_ = json.Unmarshal(p.Details, &details)
	}
	return details
}

func (p *providerStatusDB) RecordFailure(
	provider, sourceKey string,
	failedAt time.Time,
	errorClass, summary string,
) error {
	errorClass = truncateStatusValue(errorClass, 80)
	summary = truncateStatusValue(summary, 300)
	row := ProviderStatus{
		Provider:             normalizedProvider(provider),
		SourceKey:            strings.TrimSpace(sourceKey),
		LastAttemptAt:        cloneTime(&failedAt),
		WindowStartedAt:      failedAt,
		ObservationStartedAt: cloneTime(&failedAt),
		ConsecutiveFailures:  1,
		LastErrorClass:       cloneString(&errorClass),
		LastErrorSummary:     cloneString(&summary),
		Details:              datatypes.JSON([]byte(`{}`)),
		UpdatedAt:            failedAt,
	}
	return p.gorm.Table(row.TableName()).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "provider"}, {Name: "source_key"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"last_attempt_at": failedAt,
				"observation_started_at": gorm.Expr(
					"COALESCE(market_provider_status.observation_started_at, ?)",
					failedAt,
				),
				"consecutive_failures": gorm.Expr("market_provider_status.consecutive_failures + 1"),
				"last_error_class":     errorClass,
				"last_error_summary":   summary,
				"updated_at":           failedAt,
			}),
		}).
		Create(&row).Error
}

func (p *providerStatusDB) SetNextRetry(
	provider, sourceKey string,
	nextRetryAt *time.Time,
) error {
	return p.gorm.Table("market_provider_status").
		Where("provider = ? AND source_key = ?",
			normalizedProvider(provider), strings.TrimSpace(sourceKey)).
		Updates(map[string]interface{}{
			"next_retry_at": cloneTime(nextRetryAt),
			"updated_at":    time.Now().UTC(),
		}).Error
}

func (p *providerStatusDB) QueryProviderStatus(
	provider, sourceKey string,
) (*ProviderStatus, error) {
	var row ProviderStatus
	result := p.gorm.Table("market_provider_status").
		Where("provider = ? AND source_key = ?",
			normalizedProvider(provider), strings.TrimSpace(sourceKey)).
		First(&row)
	if result.Error == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &row, result.Error
}

func (p *providerStatusDB) QueryProviderStatuses() ([]ProviderStatus, error) {
	var rows []ProviderStatus
	if err := p.gorm.Table("market_provider_status").
		Order("provider ASC, source_key ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func normalizedProvider(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func truncateStatusValue(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
