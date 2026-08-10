package fullstackgolden

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
	"github.com/the-web3/s78-market-services/marketdata/providercontract/binancepublic"
	"github.com/the-web3/s78-market-services/marketdata/providercontract/coinglass"
	"github.com/the-web3/s78-market-services/marketdata/quality"
	"github.com/the-web3/s78-market-services/marketdata/qualityadapters"
	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
)

type mutableClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newMutableClock(now time.Time) *mutableClock { return &mutableClock{now: now.UTC()} }
func (c *mutableClock) Now() time.Time            { c.mu.RLock(); defer c.mu.RUnlock(); return c.now }
func (c *mutableClock) advance(duration time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
	return c.now
}

type binanceSink struct {
	mu    sync.Mutex
	items []binancepublic.Observation
}

func (s *binanceSink) Observe(value binancepublic.Observation) {
	s.mu.Lock()
	s.items = append(s.items, value)
	s.mu.Unlock()
}
func (s *binanceSink) take() (binancepublic.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 {
		return binancepublic.Observation{}, fmt.Errorf("missing Binance observation")
	}
	value := s.items[len(s.items)-1]
	s.items = s.items[:0]
	return value, nil
}

type coinGlassSink struct {
	mu    sync.Mutex
	items []coinglass.Observation
}

func (s *coinGlassSink) Observe(value coinglass.Observation) {
	s.mu.Lock()
	s.items = append(s.items, value)
	s.mu.Unlock()
}
func (s *coinGlassSink) take() (coinglass.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 {
		return coinglass.Observation{}, fmt.Errorf("missing CoinGlass observation")
	}
	value := s.items[len(s.items)-1]
	s.items = s.items[:0]
	return value, nil
}

type qualityHarness struct {
	clock         *mutableClock
	monitor       *quality.Monitor
	binance       *binancepublic.Reader
	coinGlass     *coinglass.Reader
	research      researchsignal.Reader
	binanceSink   *binanceSink
	coinGlassSink *coinGlassSink
	sequence      uint64
	mu            sync.Mutex
	history       []QualityWindowEvidence
}

type QualitySourceEvidence struct {
	Source                  quality.SourceKind    `json:"source"`
	Status                  quality.Status        `json:"status"`
	ScoreBPS                *uint32               `json:"score_bps"`
	Reasons                 []string              `json:"reasons"`
	HealthyWindowStreak     uint32                `json:"healthy_window_streak"`
	OriginalLicense         quality.LicenseStatus `json:"original_license"`
	GoldenLicenseAssumption bool                  `json:"golden_license_assumption"`
}

type QualityWindowEvidence struct {
	Scenario string                  `json:"scenario"`
	End      time.Time               `json:"end"`
	Sources  []QualitySourceEvidence `json:"sources"`
	Faults   []QualityFaultEvidence  `json:"faults"`
}

type QualityFaultEvidence struct {
	Source              quality.SourceKind         `json:"source"`
	Operation           string                     `json:"operation"`
	HTTPOutcome         string                     `json:"http_outcome"`
	HTTPErrorKind       providercontract.ErrorKind `json:"http_error_kind,omitempty"`
	NormalizedErrorKind providercontract.ErrorKind `json:"normalized_error_kind,omitempty"`
	QualityOutcome      quality.Outcome            `json:"quality_outcome"`
	HardFaults          []quality.HardFault        `json:"hard_faults"`
	HTTPStatus          int                        `json:"http_status,omitempty"`
	RetryAfterSeconds   int64                      `json:"retry_after_seconds,omitempty"`
}

func newQualityHarness(origin string, caPEM []byte, clock *mutableClock, research researchsignal.Reader) (*qualityHarness, error) {
	monitor, err := quality.NewMonitor(quality.DefaultPolicies(), clock)
	if err != nil {
		return nil, err
	}
	binanceObservations, coinGlassObservations := &binanceSink{}, &coinGlassSink{}
	binance, err := binancepublic.NewLoopbackGoldenReader(origin, caPEM, clock, binanceObservations)
	if err != nil {
		return nil, err
	}
	coinGlass, err := coinglass.NewLoopbackGoldenReader(origin, caPEM, clock, coinGlassObservations)
	if err != nil {
		return nil, err
	}
	return &qualityHarness{clock: clock, monitor: monitor, binance: binance, coinGlass: coinGlass, research: research,
		binanceSink: binanceObservations, coinGlassSink: coinGlassObservations}, nil
}

func (h *qualityHarness) recordWindow(ctx context.Context, scenario string, setScenario func(context.Context, string, string, time.Time) error) (QualityWindowEvidence, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Six minutes closes the Binance five-minute source window without
	// fabricating months-old research timestamps in the browser contract.
	now := h.clock.advance(6 * time.Minute)
	providerScenario, researchScenario := scenario, "fresh"
	if scenario == "healthy" || scenario == "recover" || scenario == "cache_hit" || scenario == "no_data" {
		providerScenario = "healthy"
	}
	if scenario == "research_legacy" {
		providerScenario, researchScenario = "healthy", "legacy"
	}
	if scenario == "no_data" {
		researchScenario = "empty"
	}
	if err := setScenario(ctx, "provider", providerScenario, now); err != nil {
		return QualityWindowEvidence{}, err
	}
	if err := setScenario(ctx, "research", researchScenario, now); err != nil {
		return QualityWindowEvidence{}, err
	}
	var faults []QualityFaultEvidence
	if scenario == "cache_hit" {
		if err := h.recordBinanceCache(ctx, now); err != nil {
			return QualityWindowEvidence{}, err
		}
	} else if scenario != "no_data" {
		var err error
		faults, err = h.recordProviders(ctx, scenario)
		if err != nil {
			return QualityWindowEvidence{}, err
		}
	}
	if err := h.recordResearch(ctx); err != nil {
		return QualityWindowEvidence{}, err
	}
	end := h.clock.Now().Add(time.Nanosecond)
	reports, err := h.monitor.Advance(end)
	if err != nil {
		return QualityWindowEvidence{}, err
	}
	evidence := QualityWindowEvidence{Scenario: scenario, End: end, Faults: faults}
	for _, report := range reports.Reports {
		evidence.Sources = append(evidence.Sources, QualitySourceEvidence{Source: report.Source, Status: report.Status,
			ScoreBPS: report.TechnicalScoreBPS, Reasons: append([]string(nil), report.Reasons...), HealthyWindowStreak: report.Gate.HealthyWindowStreak,
			OriginalLicense: originalLicense(report.Source), GoldenLicenseAssumption: report.Source == quality.SourceBinanceSpot})
	}
	h.history = append(h.history, evidence)
	return evidence, nil
}

func (h *qualityHarness) recordProviders(ctx context.Context, scenario string) ([]QualityFaultEvidence, error) {
	if scenario == "coinglass_5xx" {
		return h.recordCoinGlass(ctx, true)
	}
	if scenario == "binance_429" || scenario == "timeout" || scenario == "bad_payload" || scenario == "stale" || scenario == "future" || scenario == "conflict" {
		capability := providercontract.CapabilitySpotTicker
		if scenario == "conflict" {
			capability = providercontract.CapabilityOHLCV
		}
		return h.recordBinance(ctx, capability, 1, scenario == "timeout")
	}
	faults, err := h.recordBinance(ctx, providercontract.CapabilitySpotTicker, 5, false)
	if err != nil {
		return nil, err
	}
	more, err := h.recordBinance(ctx, providercontract.CapabilityOHLCV, 2, false)
	if err != nil {
		return nil, err
	}
	faults = append(faults, more...)
	more, err = h.recordCoinGlass(ctx, false)
	return append(faults, more...), err
}

func (h *qualityHarness) recordBinance(ctx context.Context, capability providercontract.Capability, count int, forceTimeout bool) ([]QualityFaultEvidence, error) {
	faults := make([]QualityFaultEvidence, 0, count)
	for index := 0; index < count; index++ {
		now := h.clock.advance(2 * time.Nanosecond)
		h.sequence++
		var result providercontract.DispatchResult
		var fetchErr error
		fetchContext := ctx
		_ = forceTimeout // the golden reader's verified HTTP client owns the 250ms timeout.
		if capability == providercontract.CapabilitySpotTicker {
			result, fetchErr = h.binance.SpotTicker(fetchContext)
		} else {
			result, fetchErr = h.binance.OHLCV(fetchContext)
		}
		observation, err := h.binanceSink.take()
		if err != nil {
			return nil, err
		}
		evidence, err := qualityadapters.BinanceEvidence(fmt.Sprintf("fullstack-binance-%06d", h.sequence), now, observation, result, fetchErr)
		if err != nil {
			return nil, fmt.Errorf("adapt Binance observation http_kind=%s dispatch_kind=%s: %w", observation.ErrorKind, providerErrorKind(fetchErr), err)
		}
		// This explicit golden-only assumption drives the technical recovery
		// state machine without changing the production adapter's Unknown
		// redistribution license. It is surfaced in every quality window.
		evidence.License = quality.LicenseApproved
		if err := h.monitor.Record(evidence); err != nil {
			return nil, err
		}
		faults = append(faults, binanceFault(observation, evidence, fetchErr))
	}
	return faults, nil
}

func (h *qualityHarness) recordBinanceCache(ctx context.Context, now time.Time) error {
	h.sequence++
	if _, err := h.binance.SpotTicker(ctx); err != nil {
		return err
	}
	if _, err := h.binanceSink.take(); err != nil {
		return err
	}
	result, err := h.binance.SpotTicker(ctx)
	if err != nil {
		return err
	}
	return qualityadapters.RecordBinanceCache(h.monitor, fmt.Sprintf("fullstack-binance-cache-%06d", h.sequence), now, providercontract.CapabilitySpotTicker, result)
}

func (h *qualityHarness) recordCoinGlass(ctx context.Context, failOnly bool) ([]QualityFaultEvidence, error) {
	operations := []struct {
		name  string
		fetch func(context.Context) (providercontract.DispatchResult, error)
	}{
		{coinglass.OperationOpenInterestHistory, h.coinGlass.OpenInterest},
		{coinglass.OperationLiquidationHistory, h.coinGlass.Liquidation},
	}
	if failOnly {
		operations = operations[:1]
	}
	faults := make([]QualityFaultEvidence, 0, len(operations))
	for _, operation := range operations {
		now := h.clock.advance(2 * time.Nanosecond)
		h.sequence++
		result, fetchErr := operation.fetch(ctx)
		observation, err := h.coinGlassSink.take()
		if err != nil {
			return nil, err
		}
		evidence, err := qualityadapters.CoinGlassEvidence(fmt.Sprintf("fullstack-coinglass-%06d", h.sequence), now, observation, result, fetchErr)
		if err != nil {
			return nil, err
		}
		if err := h.monitor.Record(evidence); err != nil {
			return nil, err
		}
		faults = append(faults, coinGlassFault(observation, evidence, fetchErr))
	}
	return faults, nil
}

func (h *qualityHarness) recordResearch(ctx context.Context) error {
	h.sequence++
	now := h.clock.advance(2 * time.Nanosecond)
	started := time.Now()
	summary, summaryErr := h.research.Summary(ctx)
	if err := qualityadapters.RecordResearchSummary(h.monitor, fmt.Sprintf("fullstack-research-summary-%06d", h.sequence), now, time.Since(started), summary, summaryErr, true); err != nil {
		return err
	}
	h.sequence++
	now = h.clock.advance(2 * time.Nanosecond)
	started = time.Now()
	events, eventsErr := h.research.Events(ctx, researchsignal.EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 50})
	return qualityadapters.RecordResearchEvents(h.monitor, fmt.Sprintf("fullstack-research-events-%06d", h.sequence), now, time.Since(started), events, eventsErr, true)
}

func originalLicense(source quality.SourceKind) quality.LicenseStatus {
	switch source {
	case quality.SourceCoinGlassDerivative:
		return quality.LicenseRestricted
	default:
		return quality.LicenseUnknown
	}
}

func binanceFault(observation binancepublic.Observation, evidence quality.Evidence, fetchErr error) QualityFaultEvidence {
	return QualityFaultEvidence{Source: quality.SourceBinanceSpot, Operation: observation.Operation, HTTPOutcome: observation.Outcome,
		HTTPErrorKind: observation.ErrorKind, NormalizedErrorKind: providerErrorKind(fetchErr), QualityOutcome: evidence.Outcome,
		HardFaults: append([]quality.HardFault(nil), evidence.HardFaults...), HTTPStatus: observation.StatusCode,
		RetryAfterSeconds: int64(observation.RetryAfter / time.Second)}
}

func coinGlassFault(observation coinglass.Observation, evidence quality.Evidence, fetchErr error) QualityFaultEvidence {
	return QualityFaultEvidence{Source: quality.SourceCoinGlassDerivative, Operation: observation.Operation, HTTPOutcome: observation.Outcome,
		HTTPErrorKind: observation.ErrorKind, NormalizedErrorKind: providerErrorKind(fetchErr), QualityOutcome: evidence.Outcome,
		HardFaults: append([]quality.HardFault(nil), evidence.HardFaults...), HTTPStatus: observation.StatusCode,
		RetryAfterSeconds: int64(observation.RetryAfter / time.Second)}
}

func providerErrorKind(err error) providercontract.ErrorKind {
	kind, _ := providercontract.ErrorKindOf(err)
	return kind
}

func (h *qualityHarness) evidence() []QualityWindowEvidence {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]QualityWindowEvidence(nil), h.history...)
}
