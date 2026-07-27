package service

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"syscall"
	"time"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h HandleSvc) GetSystemOverview(request *model.CommonRequest) (*model.SystemOverviewResponse, error) {
	// 状态全部是真实探测，不硬编码：
	//   - crawler / worker：读 Redis 心跳 key（进程每 5s 刷新，TTL 15s，
	//     进程死后 key 自然过期）——进程无需暴露端口即可被观测；
	//   - redis：靠心跳 key 的读取结果判定（一次真实往返）；
	//   - postgres：用下方资产查询的真实 err 判定；
	//   - api：能响应本请求即自证健康。
	// 状态词收敛到前端 isHealthyStatus 词表内（Running/Connected/Healthy/Stopped/
	// Disconnected），词表外的词会被前端一律判红。
	overview := model.SystemOverview{
		CrawlerStatus:  h.processStatus("crawler"),
		DexStatus:      h.processStatus("dex"),
		DwStatus:       h.processStatus("dw"),
		RpcStatus:      h.processStatus("rpc"),
		WorkerStatus:   h.processStatus("worker"),
		RedisStatus:    "Connected",
		DatabaseStatus: "Connected",
		ApiStatus:      "Healthy",
	}
	if overview.CrawlerStatus == "Unknown" || overview.WorkerStatus == "Unknown" {
		overview.RedisStatus = "Disconnected"
	}
	if h.providerStatusView != nil {
		if statuses, err := h.providerStatusView.QueryProviderStatuses(); err == nil {
			rollouts, _ := h.marketAggregationView.QueryProviderRolloutStates()
			overview.ProviderStatuses = aggregateProviderStatuses(statuses, rollouts, time.Now().UTC())
			if h.db != nil {
				for i := range overview.ProviderStatuses {
					item := &overview.ProviderStatuses[i]
					if selection, selectionErr := h.marketAggregationView.
						QueryProviderAssetSelectionState(item.Provider); selectionErr == nil && selection != nil {
						item.SelectionVersion = selection.ActiveVersion
						item.SelectionTargetCount = selection.TargetCount
						item.SelectionCount = selection.SelectedCount
						item.SelectionCandidateCount = selection.CandidateCount
						item.SelectionGeneratedAt = selection.GeneratedAt.UnixMilli()
					}
					if item.RolloutMode == "" {
						continue
					}
					readiness, readinessErr := database.EvaluateProviderRolloutReadiness(
						h.db, item.Provider, item.RankLimit, time.Now().UTC(),
					)
					if readinessErr != nil {
						item.RolloutReady = false
						item.RolloutBlockers = []string{"readiness evaluation failed"}
						continue
					}
					item.PrimarySourceKey = readiness.PrimarySourceKey
					item.RolloutReady = readiness.Ready
					item.RolloutBlockers = readiness.Blockers
					item.ReceivedCount = readiness.ReceivedCount
					item.MatchedAssetCount = readiness.MatchedAssetCount
					item.PriceAvailableCount = readiness.PriceAvailableCount
					item.ChangeAvailableCount = readiness.ChangeAvailableCount
					if item.LocalPreviewEnabled {
						if summary, summaryErr := h.marketAggregationView.QueryAssetIndexSummary(item.Provider); summaryErr == nil {
							item.PreviewCoveredCount = summary.PricedAssetCount
						}
					}
					if readiness.ObservationStartedAt != nil {
						item.ObservationStartedAt = readiness.ObservationStartedAt.UnixMilli()
					}
					if readiness.ReadinessNotBefore != nil {
						item.ReadinessNotBefore = readiness.ReadinessNotBefore.UnixMilli()
					}
				}
			}
		}
	}
	overview.Storage = h.storageStatus()

	assets, err := h.assetView.QueryAssets()
	if err != nil {
		overview.DatabaseStatus = "Disconnected"
	}
	overview.AssetCount = int64(len(assets))

	symbols, _ := h.symbolView.QuerySymbols()
	overview.SymbolCount = int64(len(symbols))

	exchanges, _ := h.exchangeView.QueryExchanges()
	overview.ExchangeCount = int64(len(exchanges))

	// Fetch all symbol_market records for real stats
	markets, _, _ := h.symbolMarketView.QuerySymbolMarketList(database.SymbolMarketListQuery{Page: 1, PageSize: 1000})
	overview.MarketCount = int64(len(markets))

	// Compute total market_cap and volume (1e8 scaled)
	totalMC := new(big.Int)
	totalVol := new(big.Int)
	var latestMarketUpdatedAt time.Time
	for _, m := range markets {
		if m.UpdatedAt.After(latestMarketUpdatedAt) {
			latestMarketUpdatedAt = m.UpdatedAt
		}

		// Parse market_cap (numeric(65,18) → big.Int)
		mcStr := m.MarketCap
		if idx := strings.Index(mcStr, "."); idx >= 0 {
			mcStr = mcStr[:idx]
		}
		if mc, ok := new(big.Int).SetString(mcStr, 10); ok && mc.Sign() > 0 {
			totalMC.Add(totalMC, mc)
		}

		// Parse volume
		volStr := m.Volume
		if idx := strings.Index(volStr, "."); idx >= 0 {
			volStr = volStr[:idx]
		}
		if vol, ok := new(big.Int).SetString(volStr, 10); ok && vol.Sign() > 0 {
			totalVol.Add(totalVol, vol)
		}
	}

	overview.TotalMarketCap = unscaleString(totalMC.String(), 8)
	overview.TotalVolume = unscaleString(totalVol.String(), 8)
	overview.UpdatedAt = latestMarketUpdatedAt.UnixMilli()
	overview.DataDelaySeconds = marketDataDelaySeconds(latestMarketUpdatedAt)
	if latestMarketUpdatedAt.IsZero() {
		overview.UpdatedAt = 0
		overview.DataDelaySeconds = -1
	}

	return &model.SystemOverviewResponse{
		Code:    2000,
		Message: "success",
		Result:  overview,
	}, nil
}

func (h HandleSvc) storageStatus() model.StorageStatus {
	result := model.StorageStatus{
		DiskState:            "unknown",
		RetentionDeletedRows: map[string]int64{},
		KlineIntervals:       []model.KlineIntervalStorage{},
	}
	var fileSystem syscall.Statfs_t
	if err := syscall.Statfs("/", &fileSystem); err == nil {
		result.DiskFreeBytes = int64(fileSystem.Bavail) * int64(fileSystem.Bsize)
		switch {
		case result.DiskFreeBytes < 15<<30:
			result.DiskState = "critical"
		case result.DiskFreeBytes < 25<<30:
			result.DiskState = "warning"
		default:
			result.DiskState = "healthy"
		}
	}
	if h.db == nil || h.db.KlineRetention == nil {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stats, err := h.db.KlineRetention.QueryStorageStats(ctx)
	if err != nil {
		result.RetentionLastError = "storage metrics unavailable"
		return result
	}
	result.DatabaseBytes = stats.DatabaseBytes
	result.KlineTableBytes = stats.TableBytes
	result.KlineHeapBytes = stats.HeapBytes
	result.KlineIndexBytes = stats.IndexBytes
	result.KlineEstimatedRows = stats.Rows
	result.RetentionLastError = stats.Retention.LastError
	result.RetentionDeletedRows = stats.Retention.Deleted
	if stats.Retention.LastStartedAt != nil {
		result.RetentionLastStartedAt = stats.Retention.LastStartedAt.UnixMilli()
	}
	if stats.Retention.LastSuccessAt != nil {
		result.RetentionLastSuccessAt = stats.Retention.LastSuccessAt.UnixMilli()
	}
	for _, interval := range stats.Intervals {
		item := model.KlineIntervalStorage{Interval: interval.Interval}
		if interval.Oldest != nil {
			item.OldestAt = interval.Oldest.UnixMilli()
		}
		if interval.Newest != nil {
			item.NewestAt = interval.Newest.UnixMilli()
		}
		result.KlineIntervals = append(result.KlineIntervals, item)
	}
	return result
}

func aggregateProviderStatuses(
	rows []database.ProviderStatus,
	rollouts []database.ProviderRolloutState,
	now time.Time,
) []model.ProviderStatusItem {
	type aggregate struct {
		item    model.ProviderStatusItem
		rows    map[string]database.ProviderStatus
		rollout *database.ProviderRolloutState
	}
	order := make([]string, 0)
	byProvider := make(map[string]*aggregate)
	for _, rollout := range rollouts {
		rolloutCopy := rollout
		group := &aggregate{
			item: model.ProviderStatusItem{
				Provider: rollout.Provider, RolloutMode: rollout.Mode, RankLimit: rollout.RankLimit,
				LocalPreviewEnabled: rollout.LocalPreviewEnabled,
				PreviewSourceKey: func() string {
					if rollout.LocalPreviewEnabled {
						return previewProviderSourceKey(rollout.Provider)
					}
					return ""
				}(),
			},
			rows:    make(map[string]database.ProviderStatus),
			rollout: &rolloutCopy,
		}
		if rollout.MinSoakUntil != nil {
			group.item.MinSoakUntil = rollout.MinSoakUntil.UnixMilli()
		}
		byProvider[rollout.Provider] = group
		order = append(order, rollout.Provider)
	}
	for _, row := range rows {
		group, ok := byProvider[row.Provider]
		if !ok {
			group = &aggregate{
				item: model.ProviderStatusItem{Provider: row.Provider},
				rows: make(map[string]database.ProviderStatus),
			}
			byProvider[row.Provider] = group
			order = append(order, row.Provider)
		}
		group.item.SourceCount++
		source := providerSourceStatus(row, now)
		group.item.Sources = append(group.item.Sources, source)
		group.rows[row.SourceKey] = row
		failing := row.ConsecutiveFailures > 0 &&
			(row.LastSuccessAt == nil ||
				(row.LastAttemptAt != nil && row.LastAttemptAt.After(*row.LastSuccessAt)))
		if failing {
			group.item.FailingSourceCount++
		}
	}

	result := make([]model.ProviderStatusItem, 0, len(order))
	for _, provider := range order {
		group := byProvider[provider]
		primaryKey := primaryProviderSourceKey(
			provider, group.item.RolloutMode, group.item.LocalPreviewEnabled,
		)
		group.item.PrimarySourceKey = primaryKey
		primary, hasPrimary := group.rows[primaryKey]
		if hasPrimary {
			group.item.AttemptCount = primary.AttemptCount
			group.item.SuccessCount = primary.SuccessCount
			group.item.ConsecutiveFailures = int64(primary.ConsecutiveFailures)
			group.item.SuccessRatePct = successRateText(primary.AttemptCount, primary.SuccessCount)
			if primary.LastAttemptAt != nil {
				group.item.LastAttemptAt = primary.LastAttemptAt.UnixMilli()
			}
			if primary.LastSuccessAt != nil {
				group.item.LastSuccessAt = primary.LastSuccessAt.UnixMilli()
			}
			if primary.LastSourceTime != nil {
				group.item.LastSourceTime = primary.LastSourceTime.UnixMilli()
			}
			if primary.NextRetryAt != nil {
				group.item.NextRetryAt = primary.NextRetryAt.UnixMilli()
			}
			if primary.LastErrorClass != nil {
				group.item.LastErrorClass = *primary.LastErrorClass
			}
			details := primary.ParsedDetails()
			group.item.ReceivedCount = details.ReceivedCount
			group.item.MatchedAssetCount = details.MatchedAssetCount
			group.item.PriceAvailableCount = details.PriceAvailableCount
			group.item.ChangeAvailableCount = details.ChangeAvailableCount
			if group.item.LocalPreviewEnabled {
				group.item.PreviewCoveredCount = details.PriceAvailableCount
			}
		}
		switch {
		case group.item.LocalPreviewEnabled:
			group.item.Status = "Local Preview"
			if hasPrimary {
				group.item.OperationalStatus = providerSourceStatus(primary, now).Status
			} else {
				group.item.OperationalStatus = "Unavailable"
			}
		case group.item.RolloutMode == "paused":
			group.item.Status = "Paused"
		case isUnconfiguredProvider(group.rows, primaryKey):
			group.item.Status = "Unconfigured"
		case group.item.RolloutMode == "shadow" && !hasPrimary:
			group.item.Status = "Observing"
		case group.item.RolloutMode == "shadow" && hasPrimary:
			group.item.Status = shadowOperationalStatus(primary, now)
		case !hasPrimary:
			group.item.Status = "Unavailable"
		default:
			group.item.Status = providerSourceStatus(primary, now).Status
		}
		if group.item.OperationalStatus == "" {
			group.item.OperationalStatus = group.item.Status
		}
		group.item.FeedMode = providerFeedMode(
			provider, primaryKey, group.rows,
		)
		if kline, ok := group.rows["klines"]; ok {
			klineStatus := providerSourceStatus(kline, now)
			klineDetails := kline.ParsedDetails()
			group.item.KlineStatus = klineStatus.Status
			group.item.KlineMarketCount = klineDetails.MatchedAssetCount
			group.item.KlineCandleCount = klineDetails.WrittenCount
			group.item.KlineLastSuccessAt = klineStatus.LastSuccessAt
		}
		result = append(result, group.item)
	}
	return result
}

func providerFeedMode(
	provider, primaryKey string,
	rows map[string]database.ProviderStatus,
) string {
	switch provider {
	case "binance", "coinbase", "bybit", "okx":
		_, hasPrimary := rows[primaryKey]
		_, hasReconcile := rows["spot-tickers-rest-reconcile"]
		if provider == "coinbase" {
			_, hasReconcile = rows["spot-tickers-rest-fallback"]
		}
		switch {
		case hasPrimary && hasReconcile:
			return "websocket_primary_rest_reconcile"
		case hasPrimary:
			return "websocket_primary"
		case hasReconcile:
			return "rest_reconcile_only"
		default:
			return "unobserved"
		}
	case "hyperliquid":
		return "http_polling"
	case "uniswap", "pancakeswap":
		return "native_rpc_routes"
	case "coingecko":
		return "http_catalog"
	default:
		return "provider_specific"
	}
}

func primaryProviderSourceKey(provider, rolloutMode string, localPreview bool) string {
	switch provider {
	case "binance", "coinbase", "bybit", "okx":
		if localPreview {
			return "spot-tickers-preview"
		}
		if rolloutMode == "shadow" || rolloutMode == "paused" {
			return "spot-tickers-shadow"
		}
		return "spot-tickers"
	case "hyperliquid":
		if localPreview {
			return "metaAndAssetCtxs-preview"
		}
		return "metaAndAssetCtxs"
	case "uniswap", "pancakeswap":
		if localPreview {
			return "route-quotes-preview"
		}
		return "route-quotes"
	case "coingecko":
		return "top200"
	case "exchange-rate-api", "open-er":
		return "fiat-rates"
	default:
		return "ticker-24h"
	}
}

func previewProviderSourceKey(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "hyperliquid":
		return "metaAndAssetCtxs-preview"
	case "uniswap", "pancakeswap":
		return "route-quotes-preview"
	default:
		return "spot-tickers-preview"
	}
}

func shadowOperationalStatus(row database.ProviderStatus, now time.Time) string {
	status := providerSourceStatus(row, now).Status
	if status == "Healthy" {
		return "Observing"
	}
	return status
}

func isUnconfiguredProvider(rows map[string]database.ProviderStatus, primaryKey string) bool {
	if row, ok := rows[primaryKey]; ok && row.LastErrorClass != nil {
		return strings.EqualFold(strings.TrimSpace(*row.LastErrorClass), "unconfigured")
	}
	for _, row := range rows {
		if row.LastErrorClass != nil &&
			strings.EqualFold(strings.TrimSpace(*row.LastErrorClass), "unconfigured") {
			return true
		}
	}
	return false
}

func providerSourceFreshnessLimit(provider, sourceKey string) time.Duration {
	switch sourceKey {
	case "catalog", "pool-catalog":
		return 7 * time.Hour
	case "spot-tickers", "spot-tickers-shadow", "spot-tickers-preview":
		return 2 * time.Minute
	case "metaAndAssetCtxs", "metaAndAssetCtxs-preview",
		"route-quotes", "route-quotes-preview", "rpc-session":
		return time.Minute
	case "top200", "global":
		return 10 * time.Minute
	case "ticker-24h":
		return 2 * time.Minute
	case "klines":
		return 20 * time.Minute
	}
	switch provider {
	case "binance", "coinbase", "bybit", "okx":
		return 2 * time.Minute
	case "hyperliquid", "uniswap", "pancakeswap":
		return time.Minute
	case "coingecko":
		return 10 * time.Minute
	case "exchange-rate-api", "open-er":
		return 26 * time.Hour
	default:
		return 15 * time.Minute
	}
}

func providerSourceStatus(row database.ProviderStatus, now time.Time) model.ProviderSourceStatusItem {
	details := row.ParsedDetails()
	item := model.ProviderSourceStatusItem{
		SourceKey:           row.SourceKey,
		Capability:          providerSourceCapability(row.SourceKey),
		ConsecutiveFailures: int64(row.ConsecutiveFailures),
		AttemptCount:        row.AttemptCount,
		SuccessCount:        row.SuccessCount,
		SuccessRatePct:      successRateText(row.AttemptCount, row.SuccessCount),
		ReceivedCount:       details.ReceivedCount,
		MatchedAssetCount:   details.MatchedAssetCount,
		WrittenCount:        details.WrittenCount,
	}
	if row.LastAttemptAt != nil {
		item.LastAttemptAt = row.LastAttemptAt.UnixMilli()
	}
	if row.LastSuccessAt != nil {
		item.LastSuccessAt = row.LastSuccessAt.UnixMilli()
	}
	if row.LastSourceTime != nil {
		item.LastSourceTime = row.LastSourceTime.UnixMilli()
	}
	if row.NextRetryAt != nil {
		item.NextRetryAt = row.NextRetryAt.UnixMilli()
	}
	if row.LastErrorClass != nil {
		item.LastErrorClass = *row.LastErrorClass
	}
	switch {
	case row.LastSuccessAt == nil:
		item.Status = "Unavailable"
	case now.Sub(row.LastSuccessAt.UTC()) >
		providerSourceFreshnessLimit(row.Provider, row.SourceKey):
		item.Status = "Stale"
	case row.ConsecutiveFailures > 0:
		item.Status = "Stale"
	default:
		item.Status = "Healthy"
	}
	return item
}

func providerSourceCapability(sourceKey string) string {
	switch sourceKey {
	case "catalog", "pool-catalog", "top200":
		return "catalog"
	case "spot-tickers", "spot-tickers-shadow", "spot-tickers-preview",
		"spot-tickers-rest-fallback", "spot-tickers-rest-reconcile",
		"spot-tickers-rest-shadow", "ticker-24h",
		"metaAndAssetCtxs", "metaAndAssetCtxs-preview",
		"route-quotes", "route-quotes-preview":
		return "realtime"
	case "klines":
		return "kline"
	case "simple-price":
		return "legacy-reference"
	case "global":
		return "global-metric"
	case "fiat-rates":
		return "fiat"
	default:
		switch {
		case strings.HasPrefix(sourceKey, "kline:"):
			return "kline"
		case strings.HasPrefix(sourceKey, "ticker:"):
			return "legacy-realtime"
		default:
			return "other"
		}
	}
}

func worstSourceStatus(sources []model.ProviderSourceStatusItem) string {
	status := "Healthy"
	for _, source := range sources {
		if source.Status == "Unavailable" {
			return "Unavailable"
		}
		if source.Status == "Stale" {
			status = "Stale"
		}
	}
	return status
}

func successRateText(attempts, successes int64) string {
	if attempts <= 0 {
		return ""
	}
	rate := float64(successes) / float64(attempts) * 100
	if rate > 100 {
		rate = 100
	}
	return fmt.Sprintf("%.2f", rate)
}

func unixMilliOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UnixMilli()
}

// processStatus 读进程心跳判定存活：key 存在 → Running，不存在 → Stopped；
// Redis 本身不可用时返回 Unknown（调用方据此把 RedisStatus 标为 Disconnected，
// 避免把「观测手段坏了」误报成「业务进程挂了」）。
func (h HandleSvc) processStatus(role string) string {
	if h.redisCli == nil {
		return "Unknown"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := h.redisCli.Exists(ctx, redis.HeartbeatKey(role))
	if err != nil {
		return "Unknown"
	}
	if ok {
		return "Running"
	}
	return "Stopped"
}
