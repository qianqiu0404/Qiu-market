package systemstatus

import "github.com/the-web3/s78-market-services/services/http/model"

const (
	SchemaVersion  = "system-status.v1"
	FormulaVersion = "system-display.v1"
)

type State string

const (
	StateLive         State = "live"
	StateCached       State = "cached"
	StateDemoSnapshot State = "demo_snapshot"
	StateDegraded     State = "degraded"
	StateOffline      State = "offline"
	StateUnknown      State = "unknown"
)

type Evidence struct {
	State         State  `json:"state"`
	LastSuccessAt *int64 `json:"last_success_at"`
	AgeSeconds    *int64 `json:"age_seconds"`
	Reason        string `json:"reason"`
	Source        string `json:"source"`
}

type Components struct {
	Matching   Evidence `json:"matching"`
	Liquidity  Evidence `json:"liquidity"`
	Transport  Evidence `json:"transport"`
	MarketData Evidence `json:"market_data"`
	Outbox     Evidence `json:"outbox"`
	Database   Evidence `json:"database"`
	Disk       Evidence `json:"disk"`
	Retention  Evidence `json:"retention"`
}

type ProcessStatus struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	RawStatus string   `json:"raw_status"`
	Status    Evidence `json:"status"`
}

type OptionalInt64 struct {
	Available bool   `json:"available"`
	Value     *int64 `json:"value"`
	Reason    string `json:"reason"`
}

type KlineIntervalStorage struct {
	Interval string        `json:"interval"`
	OldestAt OptionalInt64 `json:"oldest_at"`
	NewestAt OptionalInt64 `json:"newest_at"`
}

type Storage struct {
	DatabaseBytes      OptionalInt64            `json:"database_bytes"`
	KlineTableBytes    OptionalInt64            `json:"kline_table_bytes"`
	KlineHeapBytes     OptionalInt64            `json:"kline_heap_bytes"`
	KlineIndexBytes    OptionalInt64            `json:"kline_index_bytes"`
	KlineEstimatedRows OptionalInt64            `json:"kline_estimated_rows"`
	DiskFreeBytes      OptionalInt64            `json:"disk_free_bytes"`
	DiskState          string                   `json:"disk_state"`
	WarningBelowBytes  int64                    `json:"warning_below_bytes"`
	CriticalBelowBytes int64                    `json:"critical_below_bytes"`
	RetentionStartedAt OptionalInt64            `json:"retention_last_started_at"`
	RetentionSuccessAt OptionalInt64            `json:"retention_last_success_at"`
	RetentionError     string                   `json:"retention_last_error"`
	RetentionDeleted   map[string]OptionalInt64 `json:"retention_deleted_rows"`
	KlineIntervals     []KlineIntervalStorage   `json:"kline_intervals"`
}

type PriceSource struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Status   Evidence `json:"status"`
	Source   string   `json:"source"`
	Meaning  string   `json:"meaning"`
	Boundary string   `json:"boundary"`
}

type Snapshot struct {
	SchemaVersion    string                     `json:"schema_version"`
	FormulaVersion   string                     `json:"formula_version"`
	GeneratedAt      int64                      `json:"generated_at"`
	Overall          Evidence                   `json:"overall"`
	Components       Components                 `json:"components"`
	Processes        []ProcessStatus            `json:"processes"`
	Storage          Storage                    `json:"storage"`
	PriceSources     []PriceSource              `json:"price_sources"`
	ProviderStatuses []model.ProviderStatusItem `json:"provider_statuses"`
}

type Response struct {
	Code    uint64   `json:"code"`
	Message string   `json:"message"`
	Result  Snapshot `json:"result"`
}
