// Package fullstackgolden composes the production virtual-trading transports
// with deterministic, loopback-only research and quality fixtures. It is a
// verification harness, not a production server.
package fullstackgolden

import "time"

const (
	SchemaReady    = "qiu.full-stack.ready.v1"
	SchemaControl  = "qiu.full-stack.control.v1"
	SchemaState    = "qiu.full-stack.state.v1"
	SchemaEvidence = "qiu.full-stack.evidence.v1"
	SchemaManifest = "qiu.full-stack.manifest.v1"

	MarketID        = "BTC-USDT"
	BuyerAccount    = "github:qianqiu0404"
	SellerAccount   = "full-stack:seller"
	Price           = "60000.00"
	FullQuantity    = "0.01000000"
	PartialQuantity = "0.02000000"
	FillQuantity    = "0.01000000"

	FullClientOrderID    = "full-stack-full-v1"
	PartialClientOrderID = "full-stack-partial-v1"
	CancelRequestID      = "full-stack-cancel-v1"
)

type Generation string

const (
	GenerationA Generation = "A"
	GenerationB Generation = "B"
)

type Phase string

const (
	PhaseReadyA     Phase = "ready_a"
	PhaseFullFilled Phase = "full_filled"
	PhasePartial    Phase = "partial_filled"
	PhaseRestoredB  Phase = "restored_b"
	PhaseCanceled   Phase = "canceled"
)

type ProcessEvidence struct {
	Generation Generation `json:"generation,omitempty"`
	PID        int        `json:"pid"`
	Exited     bool       `json:"exited,omitempty"`
	Sequence   uint64     `json:"sequence,omitempty"`
	StateHash  string     `json:"state_hash,omitempty"`
}

type PostgresEvidence struct {
	PID              int    `json:"pid"`
	Version          string `json:"version"`
	Authority        string `json:"authority"`
	SnapshotSequence uint64 `json:"snapshot_sequence,omitempty"`
	HeadSequence     uint64 `json:"head_sequence,omitempty"`
	// False is meaningful snapshot-tail evidence and must remain present on
	// the audit wire rather than being mistaken for an unavailable check.
	SnapshotMatchesHead    bool `json:"snapshot_matches_head"`
	SnapshotMatchesRuntime bool `json:"snapshot_matches_runtime"`
}

type FixtureState struct {
	Research string `json:"research"`
	Provider string `json:"provider"`
}

type SpyEvidence struct {
	ResearchReads                  uint64 `json:"research_reads"`
	ProviderReads                  uint64 `json:"provider_reads"`
	QualityReads                   uint64 `json:"quality_reads"`
	LegacyReadRequests             uint64 `json:"legacy_read_requests"`
	FixtureControlWrites           uint64 `json:"fixture_control_writes"`
	AllowedBrowserTradingMutations uint64 `json:"allowed_browser_trading_mutations"`
	AllowedBootstrapFundWrites     uint64 `json:"allowed_bootstrap_fund_writes"`
	DeterministicFillWrites        uint64 `json:"deterministic_fill_writes"`
	ReadDomainTradingMutations     uint64 `json:"read_domain_trading_mutations"`
	ReadDomainReferenceWrites      uint64 `json:"read_domain_reference_writes"`
	ReadDomainFundWrites           uint64 `json:"read_domain_fund_writes"`
	ForbiddenWrites                uint64 `json:"forbidden_writes"`
	PublicNetworkRequests          uint64 `json:"public_network_requests"`
	FixtureNonGETRequests          uint64 `json:"fixture_non_get_requests"`
}

type Ready struct {
	SchemaVersion  string           `json:"schema_version"`
	Ready          bool             `json:"ready"`
	Phase          Phase            `json:"phase"`
	Generation     Generation       `json:"generation"`
	APIOrigin      string           `json:"api_origin"`
	CoordinatorPID int              `json:"coordinator_pid"`
	FixturePID     int              `json:"fixture_pid"`
	VuePID         int              `json:"vue_pid"`
	Postgres       PostgresEvidence `json:"postgres"`
	Backend        ProcessEvidence  `json:"backend"`
	Fixtures       FixtureState     `json:"fixtures"`
	Spy            SpyEvidence      `json:"spy"`
}

type Manifest struct {
	SchemaVersion  string           `json:"schema_version"`
	APIOrigin      string           `json:"api_origin"`
	ReadyURL       string           `json:"ready_url"`
	ControlURL     string           `json:"control_url"`
	StateURL       string           `json:"state_url"`
	EvidenceURL    string           `json:"evidence_url"`
	CoordinatorPID int              `json:"coordinator_pid"`
	FixturePID     int              `json:"fixture_pid"`
	VuePID         int              `json:"vue_pid"`
	Postgres       PostgresEvidence `json:"postgres"`
	Backend        ProcessEvidence  `json:"backend"`
}

type ControlRequest struct {
	Action        string `json:"action"`
	ClientOrderID string `json:"client_order_id,omitempty"`
	Scenario      string `json:"scenario,omitempty"`
}

type OrderEvidence struct {
	ClientOrderID     string `json:"client_order_id"`
	OrderID           string `json:"order_id"`
	Status            string `json:"status"`
	Side              string `json:"side"`
	Type              string `json:"type"`
	TimeInForce       string `json:"time_in_force"`
	PostOnly          bool   `json:"post_only"`
	Price             string `json:"price"`
	OriginalQuantity  string `json:"original_quantity"`
	FilledQuantity    string `json:"filled_quantity"`
	RemainingQuantity string `json:"remaining_quantity"`
	HeldAsset         string `json:"held_asset"`
	HeldAmount        string `json:"held_amount"`
}

type ControlResponse struct {
	SchemaVersion    string                 `json:"schema_version"`
	Action           string                 `json:"action"`
	Phase            Phase                  `json:"phase"`
	Generation       Generation             `json:"generation"`
	BackendPID       int                    `json:"backend_pid"`
	Sequence         uint64                 `json:"sequence"`
	StateHash        string                 `json:"state_hash"`
	Order            *OrderEvidence         `json:"order,omitempty"`
	PreviousBackend  *ProcessEvidence       `json:"previous_backend,omitempty"`
	CurrentBackend   *ProcessEvidence       `json:"current_backend,omitempty"`
	Restored         bool                   `json:"restored,omitempty"`
	Scenario         string                 `json:"scenario,omitempty"`
	WaitMilliseconds int                    `json:"wait_milliseconds,omitempty"`
	Quality          *QualityWindowEvidence `json:"quality,omitempty"`
}

type BalanceEvidence struct {
	Available string `json:"available"`
	Held      string `json:"held"`
}

type DatabaseCounts struct {
	Facts              uint64 `json:"facts"`
	Trades             uint64 `json:"trades"`
	LedgerTransactions uint64 `json:"ledger_transactions"`
	LedgerEntries      uint64 `json:"ledger_entries"`
	Orders             uint64 `json:"orders"`
}

type TradeEvidence struct {
	TradeID          string `json:"trade_id"`
	Sequence         uint64 `json:"sequence"`
	Price            string `json:"price"`
	Quantity         string `json:"quantity"`
	QuoteAmount      string `json:"quote_amount"`
	BuyerOrderID     string `json:"buyer_order_id"`
	SellerOrderID    string `json:"seller_order_id"`
	MakerOrderID     string `json:"maker_order_id"`
	TakerOrderID     string `json:"taker_order_id"`
	BuyerFeeAsset    string `json:"buyer_fee_asset"`
	BuyerFeeAmount   string `json:"buyer_fee_amount"`
	BuyerFeeRateBPS  int64  `json:"buyer_fee_rate_bps"`
	BuyerFeeRole     string `json:"buyer_fee_role"`
	SellerFeeAsset   string `json:"seller_fee_asset"`
	SellerFeeAmount  string `json:"seller_fee_amount"`
	SellerFeeRateBPS int64  `json:"seller_fee_rate_bps"`
	SellerFeeRole    string `json:"seller_fee_role"`
}

type LedgerEntryEvidence struct {
	Index   uint32 `json:"index"`
	Account string `json:"account"`
	Asset   string `json:"asset"`
	Amount  string `json:"amount"`
}

type LedgerTransactionEvidence struct {
	TransactionID string                `json:"transaction_id"`
	Sequence      uint64                `json:"sequence"`
	Reference     string                `json:"reference"`
	Entries       []LedgerEntryEvidence `json:"entries"`
}

type ReferenceFact struct {
	Source     string    `json:"source"`
	MarketID   string    `json:"market_id"`
	Price      string    `json:"price"`
	ObservedAt time.Time `json:"observed_at"`
	Hash       string    `json:"hash"`
}

type ReferenceEvidence struct {
	Before    ReferenceFact `json:"before"`
	After     ReferenceFact `json:"after"`
	Unchanged bool          `json:"unchanged"`
}

type DatabaseState struct {
	Digest                string                      `json:"digest"`
	Sequence              uint64                      `json:"sequence"`
	SnapshotSequence      uint64                      `json:"snapshot_sequence"`
	EventHash             string                      `json:"event_hash"`
	SnapshotHash          string                      `json:"snapshot_hash"`
	SnapshotEventHash     string                      `json:"snapshot_event_hash"`
	Counts                DatabaseCounts              `json:"counts"`
	BuyerBalances         map[string]BalanceEvidence  `json:"buyer_balances"`
	SellerBalances        map[string]BalanceEvidence  `json:"seller_balances"`
	PlatformFees          map[string]string           `json:"platform_fees"`
	Orders                map[string]OrderEvidence    `json:"orders"`
	Trades                []TradeEvidence             `json:"trades"`
	LedgerTransactions    []LedgerTransactionEvidence `json:"ledger_transactions"`
	JournalSums           map[string]string           `json:"journal_sums"`
	DuplicateTransactions bool                        `json:"duplicate_transactions"`
	ReferenceMismatch     bool                        `json:"reference_mismatch"`
}

type State struct {
	SchemaVersion string        `json:"schema_version"`
	ObservedAt    time.Time     `json:"observed_at"`
	Phase         Phase         `json:"phase"`
	Generation    Generation    `json:"generation"`
	BackendPID    int           `json:"backend_pid"`
	Database      DatabaseState `json:"database"`
}

type RestoreEvidence struct {
	Before        ProcessEvidence `json:"before"`
	After         ProcessEvidence `json:"after"`
	SameSequence  bool            `json:"same_sequence"`
	SameStateHash bool            `json:"same_state_hash"`
}

type ReplayEvidence struct {
	CancelRequestID  string         `json:"cancel_request_id"`
	CancelRequests   uint64         `json:"cancel_requests"`
	OriginalSequence uint64         `json:"original_sequence"`
	ReplaySequence   uint64         `json:"replay_sequence"`
	OriginalStatus   string         `json:"original_status"`
	ReplayStatus     string         `json:"replay_status"`
	BeforeCounts     DatabaseCounts `json:"before_counts"`
	AfterCounts      DatabaseCounts `json:"after_counts"`
	BeforeDigest     string         `json:"before_digest"`
	AfterDigest      string         `json:"after_digest"`
	BeforeEventHash  string         `json:"before_event_hash"`
	AfterEventHash   string         `json:"after_event_hash"`
	NoDelta          bool           `json:"no_delta"`
}

type Evidence struct {
	SchemaVersion  string                  `json:"schema_version"`
	ObservedAt     time.Time               `json:"observed_at"`
	Postgres       PostgresEvidence        `json:"postgres"`
	CoordinatorPID int                     `json:"coordinator_pid"`
	FixturePID     int                     `json:"fixture_pid"`
	VuePID         int                     `json:"vue_pid"`
	BackendA       ProcessEvidence         `json:"backend_a"`
	BackendB       ProcessEvidence         `json:"backend_b"`
	Restore        RestoreEvidence         `json:"restore"`
	Replay         ReplayEvidence          `json:"replay"`
	Reference      ReferenceEvidence       `json:"reference"`
	Fixture        FixtureEvidence         `json:"fixture"`
	Quality        []QualityWindowEvidence `json:"quality"`
	Partial        *DatabaseState          `json:"partial,omitempty"`
	Final          *DatabaseState          `json:"final,omitempty"`
	Spy            SpyEvidence             `json:"spy"`
	CleanupArmed   bool                    `json:"cleanup_armed"`
}
