package systemstatus

import (
	"context"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/services/http/model"
)

const (
	liveMarketAge          = 30 * time.Second
	cachedMarketAge        = 5 * time.Minute
	liveRouteAge           = time.Minute
	cachedRouteAge         = 5 * time.Minute
	retentionFreshAge      = 36 * time.Hour
	diskWarningBelowBytes  = int64(25 << 30)
	diskCriticalBelowBytes = int64(15 << 30)
)

type MarketSource interface {
	GetSystemOverview(*model.CommonRequest) (*model.SystemOverviewResponse, error)
	GetMarketOverview(*model.MarketOverviewRequest) (*model.MarketOverviewResponse, error)
}

type Service struct {
	market  MarketSource
	trading TradingProbe
	now     func() time.Time
}

func NewService(market MarketSource, trading TradingProbe) *Service {
	return &Service{market: market, trading: trading, now: time.Now}
}

func (s *Service) Snapshot(ctx context.Context) Snapshot {
	now := s.now().UTC()
	snapshot := Snapshot{
		SchemaVersion:    SchemaVersion,
		FormulaVersion:   FormulaVersion,
		GeneratedAt:      now.UnixMilli(),
		Processes:        []ProcessStatus{},
		ProviderStatuses: []model.ProviderStatusItem{},
	}

	var overview *model.SystemOverviewResponse
	var overviewErr error
	if s.market == nil {
		overviewErr = context.Canceled
	} else {
		overview, overviewErr = s.market.GetSystemOverview(&model.CommonRequest{})
	}
	if overviewErr == nil && overview != nil {
		snapshot.Processes = processStatuses(overview.Result)
		snapshot.ProviderStatuses = overview.Result.ProviderStatuses
		snapshot.Storage = storageStatus(overview.Result.Storage)
		snapshot.Components.Database = databaseStatus(overview.Result.DatabaseStatus)
		snapshot.Components.Disk = diskStatus(overview.Result.Storage)
		snapshot.Components.Retention = retentionStatus(overview.Result.Storage, now)
	} else {
		snapshot.Processes = unavailableProcesses()
		snapshot.Storage = unavailableStorage("system overview is unavailable")
		snapshot.Components.Database = Evidence{
			State: StateOffline, Reason: "system overview is unavailable",
			Source: "PostgreSQL status probe",
		}
		snapshot.Components.Disk = Evidence{
			State: StateUnknown, Reason: "disk probe result is unavailable",
			Source: "filesystem statfs",
		}
		snapshot.Components.Retention = Evidence{
			State: StateUnknown, Reason: "retention status is unavailable",
			Source: "kline_retention_status",
		}
	}

	referenceOverview, referenceErr := s.marketOverview("all")
	snapshot.Components.MarketData = referenceStatus(referenceOverview, referenceErr, now)
	routeStatus := s.routeStatus(now)
	snapshot.PriceSources = []PriceSource{
		{
			Key: "route_price", Label: "Route price", Status: routeStatus,
			Source:   "Uniswap and PancakeSwap venue route summaries",
			Meaning:  "Venue-specific indicative route quotes at the reported notional.",
			Boundary: "Never substituted for the CEX Spot reference display price.",
		},
		{
			Key: "reference_display_price", Label: "Reference display price",
			Status:   snapshot.Components.MarketData,
			Source:   "asset_price_index built from fresh CEX Spot contributors",
			Meaning:  "Read-only composite reference used for display and the virtual demo-maker.",
			Boundary: "Not an executable route price and never filled from DEX or mock data.",
		},
	}

	trading := TradingProbeResult{ProbedAt: now}
	if s.trading == nil {
		trading.StatusError = context.Canceled
		trading.OrderBookError = context.Canceled
	} else {
		trading = s.trading.Probe(ctx)
	}
	snapshot.Components.Transport = transportStatus(trading)
	snapshot.Components.Matching = matchingStatus(trading)
	snapshot.Components.Liquidity = liquidityStatus(trading)
	snapshot.Components.Outbox = outboxStatus(trading, now)
	snapshot.Overall = overallStatus(snapshot.Components, overviewErr != nil)
	return snapshot
}

func (s *Service) marketOverview(venue string) (*model.MarketOverviewResponse, error) {
	if s.market == nil {
		return nil, context.Canceled
	}
	return s.market.GetMarketOverview(&model.MarketOverviewRequest{
		Venue: venue,
	})
}

func (s *Service) routeStatus(now time.Time) Evidence {
	uniswap, uniswapErr := s.marketOverview("uniswap")
	pancake, pancakeErr := s.marketOverview("pancakeswap")
	if uniswapErr != nil && pancakeErr != nil {
		return Evidence{
			State: StateDegraded, Reason: "DEX route summaries are unavailable",
			Source: "Uniswap and PancakeSwap route summaries",
		}
	}
	var latest int64
	var routable int64
	for _, response := range []*model.MarketOverviewResponse{uniswap, pancake} {
		if response == nil {
			continue
		}
		routable += response.Result.RoutableAssetCount
		if response.Result.IndexUpdatedAt > latest {
			latest = response.Result.IndexUpdatedAt
		}
	}
	if routable <= 0 {
		return Evidence{
			State: StateDegraded, Reason: "no current DEX route prices are available",
			Source: "Uniswap and PancakeSwap route summaries",
		}
	}
	return timedEvidence(
		latest, now, liveRouteAge, cachedRouteAge,
		"DEX route summaries are current",
		"DEX route summaries are cached",
		"DEX route summaries are stale",
		"Uniswap and PancakeSwap route summaries",
	)
}

func referenceStatus(
	response *model.MarketOverviewResponse,
	err error,
	now time.Time,
) Evidence {
	if err != nil {
		return Evidence{
			State: StateDegraded, Reason: "CEX Spot reference overview is unavailable",
			Source: "asset_price_index",
		}
	}
	if response == nil {
		return Evidence{
			State: StateUnknown, Reason: "CEX Spot reference overview was not reported",
			Source: "asset_price_index",
		}
	}
	if response.Result.PricedAssetCount <= 0 {
		return Evidence{
			State: StateDegraded, Reason: "no CEX Spot reference prices are available",
			Source: "asset_price_index",
		}
	}
	return timedEvidence(
		response.Result.IndexUpdatedAt, now, liveMarketAge, cachedMarketAge,
		"CEX Spot reference data is current",
		"CEX Spot reference data is served from the retained last success",
		"CEX Spot reference data is stale",
		"asset_price_index",
	)
}

func timedEvidence(
	lastSuccessMillis int64,
	now time.Time,
	liveFor time.Duration,
	cachedFor time.Duration,
	liveReason string,
	cachedReason string,
	staleReason string,
	source string,
) Evidence {
	if lastSuccessMillis <= 0 {
		return Evidence{
			State: StateUnknown, Reason: "last successful observation was not reported",
			Source: source,
		}
	}
	lastSuccess := time.UnixMilli(lastSuccessMillis).UTC()
	age := now.Sub(lastSuccess)
	if age < 0 {
		age = 0
	}
	last := lastSuccessMillis
	seconds := int64(age / time.Second)
	evidence := Evidence{
		LastSuccessAt: &last, AgeSeconds: &seconds, Source: source,
	}
	switch {
	case age <= liveFor:
		evidence.State = StateLive
		evidence.Reason = liveReason
	case age <= cachedFor:
		evidence.State = StateCached
		evidence.Reason = cachedReason
	default:
		evidence.State = StateDegraded
		evidence.Reason = staleReason
	}
	return evidence
}

func transportStatus(result TradingProbeResult) Evidence {
	successes := 0
	if result.StatusError == nil && result.Status != nil {
		successes++
	}
	if result.OrderBookError == nil && result.OrderBook != nil {
		successes++
	}
	switch successes {
	case 2:
		return probeEvidence(
			StateLive, result.ProbedAt,
			"trading status and order book reads both succeeded",
			"loopback trading REST over gRPC",
		)
	case 1:
		return probeEvidence(
			StateDegraded, result.ProbedAt,
			"only one trading read succeeded",
			"loopback trading REST over gRPC",
		)
	default:
		return Evidence{
			State: StateOffline, Reason: "trading status and order book are unreachable",
			Source: "loopback trading REST over gRPC",
		}
	}
}

func matchingStatus(result TradingProbeResult) Evidence {
	if result.StatusError != nil || result.Status == nil {
		return Evidence{
			State: StateOffline, Reason: "matching status is unreachable",
			Source: "trading GetStatus",
		}
	}
	if result.Status.State == nil || strings.TrimSpace(*result.Status.State) == "" {
		return probeEvidence(
			StateUnknown, result.ProbedAt,
			"matching state was not reported",
			"trading GetStatus",
		)
	}
	state := strings.ToLower(strings.TrimSpace(*result.Status.State))
	if state == "ready" {
		return probeEvidence(
			StateLive, result.ProbedAt,
			"matching engine explicitly reports ready",
			"trading GetStatus",
		)
	}
	return probeEvidence(
		StateDegraded, result.ProbedAt,
		"matching engine reports "+state,
		"trading GetStatus",
	)
}

func liquidityStatus(result TradingProbeResult) Evidence {
	if result.OrderBookError != nil || result.OrderBook == nil {
		return Evidence{
			State: StateOffline, Reason: "order book is unreachable",
			Source: "BTC-USDT public order book",
		}
	}
	if result.OrderBook.Bids == nil || result.OrderBook.Asks == nil {
		return probeEvidence(
			StateUnknown, result.ProbedAt,
			"order book sides were not reported",
			"BTC-USDT public order book",
		)
	}
	if len(*result.OrderBook.Bids) > 0 && len(*result.OrderBook.Asks) > 0 {
		return probeEvidence(
			StateLive, result.ProbedAt,
			"two-sided BTC-USDT liquidity is visible",
			"BTC-USDT public order book",
		)
	}
	return probeEvidence(
		StateDegraded, result.ProbedAt,
		"two-sided BTC-USDT liquidity is not visible",
		"BTC-USDT public order book",
	)
}

func outboxStatus(result TradingProbeResult, now time.Time) Evidence {
	if result.StatusError != nil || result.Status == nil {
		return Evidence{
			State: StateOffline, Reason: "outbox status is unreachable",
			Source: "trading GetStatus outbox fields",
		}
	}
	if result.Status.OutboxState == nil ||
		strings.TrimSpace(*result.Status.OutboxState) == "" {
		return probeEvidence(
			StateUnknown, result.ProbedAt,
			"backend does not expose outbox state",
			"trading GetStatus outbox fields",
		)
	}
	state := strings.ToLower(strings.TrimSpace(*result.Status.OutboxState))
	hasError := result.Status.OutboxLastError != nil &&
		strings.TrimSpace(*result.Status.OutboxLastError) != ""
	var evidence Evidence
	if state == "ready" && !hasError {
		evidence = probeEvidence(
			StateLive, result.ProbedAt,
			"outbox publisher explicitly reports ready",
			"trading GetStatus outbox fields",
		)
	} else {
		evidence = probeEvidence(
			StateDegraded, result.ProbedAt,
			"outbox publisher reports "+state,
			"trading GetStatus outbox fields",
		)
	}
	if result.Status.OutboxLastPublishedAt != nil {
		if parsed, err := time.Parse(time.RFC3339Nano, *result.Status.OutboxLastPublishedAt); err == nil {
			last := parsed.UTC().UnixMilli()
			age := now.Sub(parsed.UTC())
			if age < 0 {
				age = 0
			}
			seconds := int64(age / time.Second)
			evidence.LastSuccessAt = &last
			evidence.AgeSeconds = &seconds
		}
	}
	return evidence
}

func probeEvidence(
	state State,
	probedAt time.Time,
	reason string,
	source string,
) Evidence {
	at := probedAt.UTC().UnixMilli()
	age := int64(0)
	return Evidence{
		State: state, LastSuccessAt: &at, AgeSeconds: &age,
		Reason: reason, Source: source,
	}
}

func databaseStatus(raw string) Evidence {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "connected", "healthy", "ready", "running":
		return Evidence{
			State: StateLive, Reason: "PostgreSQL read probe succeeded",
			Source: "system overview database_status",
		}
	case "disconnected", "offline", "stopped", "failed":
		return Evidence{
			State: StateOffline, Reason: "PostgreSQL read probe failed",
			Source: "system overview database_status",
		}
	default:
		return Evidence{
			State: StateUnknown, Reason: "database status was not reported",
			Source: "system overview database_status",
		}
	}
}

func diskStatus(storage model.StorageStatus) Evidence {
	if storage.DiskFreeBytes <= 0 ||
		strings.TrimSpace(storage.DiskState) == "" ||
		strings.EqualFold(storage.DiskState, "unknown") {
		return Evidence{
			State: StateUnknown, Reason: "free disk bytes were not measured",
			Source: "filesystem statfs",
		}
	}
	switch strings.ToLower(strings.TrimSpace(storage.DiskState)) {
	case "healthy":
		return Evidence{
			State: StateLive, Reason: "free disk is above the warning threshold",
			Source: "filesystem statfs",
		}
	case "warning":
		return Evidence{
			State: StateDegraded, Reason: "free disk is below 25 GB",
			Source: "filesystem statfs",
		}
	case "critical":
		return Evidence{
			State: StateDegraded, Reason: "free disk is below 15 GB",
			Source: "filesystem statfs",
		}
	default:
		return Evidence{
			State: StateUnknown, Reason: "disk threshold state is not recognized",
			Source: "filesystem statfs",
		}
	}
}

func retentionStatus(storage model.StorageStatus, now time.Time) Evidence {
	if strings.TrimSpace(storage.RetentionLastError) != "" {
		return timedEvidence(
			storage.RetentionLastSuccessAt, now, retentionFreshAge, retentionFreshAge,
			"retention last succeeded but the latest run failed",
			"retention last succeeded but the latest run failed",
			"retention last succeeded but the latest run failed",
			"kline_retention_status",
		).withState(StateDegraded)
	}
	if storage.RetentionLastSuccessAt <= 0 {
		return Evidence{
			State: StateUnknown, Reason: "retention has no recorded successful run",
			Source: "kline_retention_status",
		}
	}
	evidence := timedEvidence(
		storage.RetentionLastSuccessAt, now,
		retentionFreshAge, retentionFreshAge,
		"retention succeeded within the expected daily window",
		"retention succeeded within the expected daily window",
		"retention success is older than 36 hours",
		"kline_retention_status",
	)
	if evidence.State == StateCached {
		evidence.State = StateLive
	}
	return evidence
}

func (e Evidence) withState(state State) Evidence {
	e.State = state
	return e
}

func processStatuses(overview model.SystemOverview) []ProcessStatus {
	return []ProcessStatus{
		processStatus("crawler", "Spot ingest supervisor", overview.CrawlerStatus),
		processStatus("dex", "DEX ingest supervisor", overview.DexStatus),
		processStatus("worker", "Repair worker", overview.WorkerStatus),
		processStatus("dw", "DW sync", overview.DwStatus),
		processStatus("rpc", "gRPC", overview.RpcStatus),
		processStatus("redis", "Redis", overview.RedisStatus),
		processStatus("database", "PostgreSQL", overview.DatabaseStatus),
		processStatus("api", "API", overview.ApiStatus),
	}
}

func unavailableProcesses() []ProcessStatus {
	keys := []struct {
		key   string
		label string
	}{
		{"crawler", "Spot ingest supervisor"},
		{"dex", "DEX ingest supervisor"},
		{"worker", "Repair worker"},
		{"dw", "DW sync"},
		{"rpc", "gRPC"},
		{"redis", "Redis"},
		{"database", "PostgreSQL"},
		{"api", "API"},
	}
	result := make([]ProcessStatus, 0, len(keys))
	for _, item := range keys {
		result = append(result, ProcessStatus{
			Key: item.key, Label: item.label, RawStatus: "unknown",
			Status: Evidence{
				State: StateUnknown, Reason: "process heartbeat was not reported",
				Source: "Redis heartbeat existence",
			},
		})
	}
	return result
}

func processStatus(key, label, raw string) ProcessStatus {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	evidence := Evidence{
		State: StateUnknown, Reason: "process heartbeat was not reported",
		Source: "Redis heartbeat existence",
	}
	switch normalized {
	case "running", "connected", "healthy", "ready":
		evidence.State = StateLive
		evidence.Reason = "the latest heartbeat exists"
	case "stopped", "disconnected", "offline", "failed":
		evidence.State = StateOffline
		evidence.Reason = "the heartbeat is absent or the dependency probe failed"
	}
	if strings.TrimSpace(raw) == "" {
		raw = "unknown"
	}
	return ProcessStatus{Key: key, Label: label, RawStatus: raw, Status: evidence}
}

func storageStatus(raw model.StorageStatus) Storage {
	metricsAvailable := raw.DatabaseBytes > 0
	metricReason := "storage metrics were not reported"
	result := Storage{
		DatabaseBytes:      optionalMetric(raw.DatabaseBytes, metricsAvailable, metricReason),
		KlineTableBytes:    optionalMetric(raw.KlineTableBytes, metricsAvailable, metricReason),
		KlineHeapBytes:     optionalMetric(raw.KlineHeapBytes, metricsAvailable, metricReason),
		KlineIndexBytes:    optionalMetric(raw.KlineIndexBytes, metricsAvailable, metricReason),
		KlineEstimatedRows: optionalMetric(raw.KlineEstimatedRows, metricsAvailable, metricReason),
		DiskFreeBytes:      optionalMetric(raw.DiskFreeBytes, raw.DiskFreeBytes > 0, "free disk bytes were not measured"),
		DiskState:          strings.ToLower(strings.TrimSpace(raw.DiskState)),
		WarningBelowBytes:  diskWarningBelowBytes,
		CriticalBelowBytes: diskCriticalBelowBytes,
		RetentionStartedAt: optionalMetric(raw.RetentionLastStartedAt, raw.RetentionLastStartedAt > 0, "retention has not recorded a start"),
		RetentionSuccessAt: optionalMetric(raw.RetentionLastSuccessAt, raw.RetentionLastSuccessAt > 0, "retention has not recorded a success"),
		RetentionError:     raw.RetentionLastError,
		RetentionDeleted:   make(map[string]OptionalInt64, 3),
		KlineIntervals:     make([]KlineIntervalStorage, 0, 4),
	}
	if result.DiskState == "" {
		result.DiskState = "unknown"
	}
	for _, interval := range []string{"1m", "15m", "1h"} {
		value, ok := raw.RetentionDeletedRows[interval]
		result.RetentionDeleted[interval] = optionalMetric(
			value, ok, "retention did not report a deleted-row count",
		)
	}
	byInterval := make(map[string]model.KlineIntervalStorage, len(raw.KlineIntervals))
	for _, interval := range raw.KlineIntervals {
		byInterval[interval.Interval] = interval
	}
	for _, interval := range []string{"1m", "15m", "1h", "1d"} {
		item, exists := byInterval[interval]
		result.KlineIntervals = append(result.KlineIntervals, KlineIntervalStorage{
			Interval: interval,
			OldestAt: optionalMetric(
				item.OldestAt, exists && item.OldestAt > 0,
				"no oldest candle was reported for this interval",
			),
			NewestAt: optionalMetric(
				item.NewestAt, exists && item.NewestAt > 0,
				"no newest candle was reported for this interval",
			),
		})
	}
	return result
}

func unavailableStorage(reason string) Storage {
	missing := unavailableMetric(reason)
	result := Storage{
		DatabaseBytes: missing, KlineTableBytes: missing,
		KlineHeapBytes: missing, KlineIndexBytes: missing,
		KlineEstimatedRows: missing, DiskFreeBytes: missing,
		DiskState: "unknown", WarningBelowBytes: diskWarningBelowBytes,
		CriticalBelowBytes: diskCriticalBelowBytes,
		RetentionStartedAt: missing, RetentionSuccessAt: missing,
		RetentionDeleted: map[string]OptionalInt64{
			"1m": missing, "15m": missing, "1h": missing,
		},
		KlineIntervals: make([]KlineIntervalStorage, 0, 4),
	}
	for _, interval := range []string{"1m", "15m", "1h", "1d"} {
		result.KlineIntervals = append(result.KlineIntervals, KlineIntervalStorage{
			Interval: interval, OldestAt: missing, NewestAt: missing,
		})
	}
	return result
}

func optionalMetric(value int64, available bool, reason string) OptionalInt64 {
	if !available {
		return unavailableMetric(reason)
	}
	copy := value
	return OptionalInt64{Available: true, Value: &copy}
}

func unavailableMetric(reason string) OptionalInt64 {
	return OptionalInt64{Available: false, Reason: reason}
}

func overallStatus(components Components, overviewFailed bool) Evidence {
	if overviewFailed &&
		components.Transport.State == StateOffline &&
		components.MarketData.State != StateLive &&
		components.MarketData.State != StateCached {
		return Evidence{
			State:  StateOffline,
			Reason: "system overview and trading reads are both unavailable",
			Source: FormulaVersion,
		}
	}
	required := []Evidence{
		components.Matching,
		components.Liquidity,
		components.Transport,
		components.MarketData,
		components.Outbox,
		components.Database,
		components.Disk,
		components.Retention,
	}
	allLive := true
	for _, component := range required {
		if component.State != StateLive {
			allLive = false
			break
		}
	}
	if allLive {
		return Evidence{
			State:  StateLive,
			Reason: "all required read-only probes have explicit current success evidence",
			Source: FormulaVersion,
		}
	}
	if components.MarketData.State == StateCached {
		othersLive := true
		for _, component := range []Evidence{
			components.Matching,
			components.Liquidity,
			components.Transport,
			components.Outbox,
			components.Database,
			components.Disk,
			components.Retention,
		} {
			if component.State != StateLive {
				othersLive = false
				break
			}
		}
		if othersLive {
			return Evidence{
				State:  StateCached,
				Reason: "only market data is using a retained last success within five minutes",
				Source: FormulaVersion,
			}
		}
	}
	return Evidence{
		State:  StateDegraded,
		Reason: "one or more required probes are stale, failed, or missing explicit evidence",
		Source: FormulaVersion,
	}
}
