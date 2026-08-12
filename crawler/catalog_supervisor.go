package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"github.com/the-web3/s78-market-services/catalog"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/marketdata"
)

const (
	catalogRefreshInterval   = 6 * time.Hour
	metricRefreshInterval    = 5 * time.Minute
	rolloutReconcileInterval = 30 * time.Second
)

type DiscoveredMarket struct {
	SourceSymbol   string
	BaseAlias      string
	QuoteAlias     string
	MarketType     string
	UpstreamStatus string
	Tradable       bool
	RawMetadata    json.RawMessage
}

type CatalogAdapter interface {
	Provider() string
	Discover(context.Context) ([]DiscoveredMarket, error)
}

type CatalogSupervisor struct {
	db       *database.DB
	reporter *marketdata.ProviderReporter
	client   *http.Client
	adapters []CatalogAdapter
	cancel   context.CancelFunc
	stopped  atomic.Bool
}

func NewCatalogSupervisor(db *database.DB, _ bool) *CatalogSupervisor {
	client := &http.Client{Timeout: 15 * time.Second}
	return &CatalogSupervisor{
		db:       db,
		reporter: marketdata.NewProviderReporter(db.ProviderStatus),
		client:   client,
		adapters: []CatalogAdapter{
			NewBinanceSpotAdapter(client),
			NewCoinbaseSpotAdapter(client),
			NewBybitSpotAdapter(client),
			NewOKXSpotAdapter(client),
		},
	}
}

func (s *CatalogSupervisor) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	go s.run(ctx)
	return nil
}

func (s *CatalogSupervisor) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.stopped.Store(true)
}

func (s *CatalogSupervisor) Stopped() bool { return s.stopped.Load() }

func (s *CatalogSupervisor) run(ctx context.Context) {
	s.refreshMetricsAndManifest(ctx)
	for _, adapter := range s.adapters {
		adapter := adapter
		go s.superviseProviderCatalog(ctx, adapter)
	}
	metricTicker := time.NewTicker(metricRefreshInterval)
	defer metricTicker.Stop()
	reconcileTicker := time.NewTicker(rolloutReconcileInterval)
	defer reconcileTicker.Stop()
	for {
		select {
		case <-metricTicker.C:
			s.refreshMetricsAndManifest(ctx)
		case <-reconcileTicker.C:
			s.reconcileRollouts()
		case <-ctx.Done():
			return
		}
	}
}

func (s *CatalogSupervisor) reconcileRollouts() {
	for _, provider := range []string{"binance", "coinbase", "bybit", "okx"} {
		if _, err := s.db.MarketAggregation.ReconcileResolvedSpotMarkets(provider); err != nil {
			log.Warn("provider rollout reconcile failed", "provider", provider, "error", err)
		}
	}
}

func (s *CatalogSupervisor) refreshMetricsAndManifest(ctx context.Context) {
	if err := s.refreshCoinGeckoUniverse(ctx); err != nil {
		log.Warn("CoinGecko Top 200 refresh failed", "error", err)
	}
	result, err := catalog.ApplyEmbedded(s.db)
	if err != nil {
		log.Warn("reviewed provider asset manifest apply failed", "error", err)
		return
	}
	log.Info("reviewed provider asset manifest applied",
		"aliases", result.AliasCount,
		"representations", result.RepresentationCount,
		"missing_assets", len(result.MissingAssetIDs))
}

func (s *CatalogSupervisor) superviseProviderCatalog(ctx context.Context, adapter CatalogAdapter) {
	backoff := 30 * time.Second
	for ctx.Err() == nil {
		err := s.refreshProviderCatalog(ctx, adapter)
		wait := catalogRefreshInterval
		if err != nil {
			log.Warn("provider catalog refresh failed",
				"provider", adapter.Provider(), "error", err, "retry_in", backoff)
			wait = backoff
			s.reporter.NextRetry(adapter.Provider(), "catalog", time.Now().UTC().Add(wait))
			backoff *= 2
			if backoff > 10*time.Minute {
				backoff = 10 * time.Minute
			}
		} else {
			backoff = 30 * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (s *CatalogSupervisor) refreshProviderCatalog(ctx context.Context, adapter CatalogAdapter) error {
	provider := adapter.Provider()
	sourceKey := "catalog"
	attemptedAt := time.Now().UTC()
	s.reporter.Attempt(provider, sourceKey, attemptedAt)
	discovered, err := adapter.Discover(ctx)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, httpStatusFromError(err))
		return err
	}
	approved, err := s.db.MarketAggregation.QueryApprovedAliases(provider)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	if err := s.db.MarketAggregation.UpsertAssetExternalMappings(
		approvedAliasMappings(provider, approved),
	); err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	uniqueTopSymbols, err := s.db.MarketAggregation.QueryUniqueTopAssetSymbols(200)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	topAssets, err := s.db.MarketAggregation.QueryTopAssetIDs(200)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	now := time.Now().UTC()
	candidates, suggestions := resolveDiscoveredMarkets(
		provider, discovered, approved, uniqueTopSymbols, topAssets, now,
	)
	if err := s.db.MarketAggregation.UpsertAssetAliases(suggestions); err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	if err := s.db.MarketAggregation.UpsertProviderMarketCandidates(candidates); err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	rollout, err := s.db.MarketAggregation.QueryProviderRollout(provider)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	if rollout != nil && rollout.LocalPreviewEnabled {
		if _, err := s.db.MarketAggregation.EnsureProviderAssetSelection(
			provider, 50, "successful-catalog-refresh",
		); err != nil {
			s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
			return fmt.Errorf("refresh %s provider selection: %w", provider, err)
		}
	}
	allowedAssets, rollout, err := s.db.MarketAggregation.QueryPublishedAssetIDs(provider)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	enabled, err := s.db.MarketAggregation.EnableResolvedSpotMarkets(provider, now, allowedAssets)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		return fmt.Errorf("enable resolved %s markets: %w", provider, err)
	}
	s.reporter.Success(provider, sourceKey, now, nil)
	rolloutMode := "shadow"
	if rollout != nil {
		rolloutMode = rollout.Mode
	}
	log.Info("provider catalog refreshed",
		"provider", provider, "discovered", len(discovered),
		"pending_alias_suggestions", len(suggestions), "enabled", enabled,
		"rollout_mode", rolloutMode,
		"local_preview", rollout != nil && rollout.LocalPreviewEnabled)
	return nil
}

func approvedAliasMappings(provider string, aliases map[string]string) []database.AssetExternalMapping {
	aliasNames := make([]string, 0, len(aliases))
	for alias := range aliases {
		aliasNames = append(aliasNames, alias)
	}
	sort.Strings(aliasNames)
	seenAssets := make(map[string]struct{}, len(aliasNames))
	result := make([]database.AssetExternalMapping, 0, len(aliasNames))
	for _, alias := range aliasNames {
		assetID := aliases[alias]
		if _, exists := seenAssets[assetID]; exists {
			continue
		}
		seenAssets[assetID] = struct{}{}
		result = append(result, database.AssetExternalMapping{
			Provider: provider, AssetGuid: assetID, ExternalID: alias,
		})
	}
	return result
}

func resolveDiscoveredMarkets(
	provider string,
	discovered []DiscoveredMarket,
	approvedAliases map[string]string,
	uniqueTopSymbols map[string]string,
	topAssets map[string]struct{},
	now time.Time,
) ([]database.ProviderMarketCandidate, []database.AssetAlias) {
	candidates := make([]database.ProviderMarketCandidate, 0, len(discovered))
	suggestionsByAlias := make(map[string]database.AssetAlias)
	for _, market := range discovered {
		base := strings.ToUpper(strings.TrimSpace(market.BaseAlias))
		quote := strings.ToUpper(strings.TrimSpace(market.QuoteAlias))
		marketType := strings.ToLower(strings.TrimSpace(market.MarketType))
		if marketType == "" {
			marketType = "spot"
		}
		status := strings.TrimSpace(market.UpstreamStatus)
		rawMetadata := market.RawMetadata
		if len(rawMetadata) == 0 {
			rawMetadata = json.RawMessage(`{}`)
		}
		candidate := database.ProviderMarketCandidate{
			Provider: provider, SourceSymbol: strings.TrimSpace(market.SourceSymbol),
			MarketType: marketType, BaseAlias: base, QuoteAlias: quote,
			UpstreamStatus: optionalText(status), ResolutionStatus: "discovered",
			FirstSeenAt: now, LastSeenAt: now, RawMetadata: datatypes.JSON(rawMetadata),
		}
		switch {
		case marketType != "spot":
			candidate.ResolutionStatus = "rejected"
			candidate.RejectionReason = optionalText("unsupported_market_type")
		case !market.Tradable:
			candidate.ResolutionStatus = "rejected"
			candidate.RejectionReason = optionalText("upstream_not_tradable")
		case !isUSDQuote(quote):
			candidate.ResolutionStatus = "rejected"
			candidate.RejectionReason = optionalText("unsupported_quote_asset")
		default:
			baseID, baseApproved := approvedAliases[base]
			quoteID, quoteApproved := approvedAliases[quote]
			if !baseApproved {
				if suggestedID, unique := uniqueTopSymbols[base]; unique {
					suggestionsByAlias[base] = database.AssetAlias{
						Provider: provider, Alias: base, AssetGuid: suggestedID,
						ReviewStatus: "pending", CreatedAt: now, UpdatedAt: now,
					}
					candidate.RejectionReason = optionalText("base_alias_review_required")
				} else {
					candidate.ResolutionStatus = "ambiguous"
					candidate.RejectionReason = optionalText("base_alias_ambiguous_or_outside_top200")
				}
				break
			}
			if !quoteApproved {
				if suggestedID, unique := uniqueTopSymbols[quote]; unique {
					suggestionsByAlias[quote] = database.AssetAlias{
						Provider: provider, Alias: quote, AssetGuid: suggestedID,
						ReviewStatus: "pending", CreatedAt: now, UpdatedAt: now,
					}
					candidate.RejectionReason = optionalText("quote_alias_review_required")
				} else {
					candidate.ResolutionStatus = "ambiguous"
					candidate.RejectionReason = optionalText("quote_alias_ambiguous")
				}
				break
			}
			if _, inside := topAssets[baseID]; !inside {
				candidate.ResolutionStatus = "rejected"
				candidate.RejectionReason = optionalText("base_asset_outside_top200")
				break
			}
			candidate.BaseAssetGuid = &baseID
			candidate.QuoteAssetGuid = &quoteID
			candidate.ResolutionStatus = "resolved"
			candidate.ResolvedAt = &now
		}
		candidates = append(candidates, candidate)
	}
	suggestions := make([]database.AssetAlias, 0, len(suggestionsByAlias))
	for _, suggestion := range suggestionsByAlias {
		suggestions = append(suggestions, suggestion)
	}
	return candidates, suggestions
}

func isUSDQuote(value string) bool {
	switch strings.ToUpper(value) {
	case "USD", "USDT", "USDC":
		return true
	default:
		return false
	}
}

func optionalText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	copy := value
	return &copy
}

type coinGeckoMarket struct {
	ID                string   `json:"id"`
	Symbol            string   `json:"symbol"`
	Name              string   `json:"name"`
	Image             string   `json:"image"`
	CurrentPrice      *float64 `json:"current_price"`
	MarketCap         *float64 `json:"market_cap"`
	MarketCapRank     *int     `json:"market_cap_rank"`
	TotalVolume       *float64 `json:"total_volume"`
	CirculatingSupply *float64 `json:"circulating_supply"`
	TotalSupply       *float64 `json:"total_supply"`
	MaxSupply         *float64 `json:"max_supply"`
	LastUpdated       string   `json:"last_updated"`
}

type coinGeckoGlobalResponse struct {
	Data struct {
		TotalMarketCap      map[string]float64 `json:"total_market_cap"`
		TotalVolume         map[string]float64 `json:"total_volume"`
		MarketCapPercentage map[string]float64 `json:"market_cap_percentage"`
		UpdatedAt           int64              `json:"updated_at"`
	} `json:"data"`
}

func (s *CatalogSupervisor) refreshCoinGeckoUniverse(ctx context.Context) error {
	sourceKey := "top200"
	attemptedAt := time.Now().UTC()
	s.reporter.Attempt("coingecko", sourceKey, attemptedAt)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=200&page=1&sparkline=false",
		nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		s.reporter.Failure("coingecko", sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err := fmt.Errorf("CoinGecko markets HTTP %d", response.StatusCode)
		s.reporter.Failure("coingecko", sourceKey, time.Now().UTC(), err, response.StatusCode)
		return err
	}
	var markets []coinGeckoMarket
	if err := json.NewDecoder(response.Body).Decode(&markets); err != nil {
		s.reporter.Failure("coingecko", sourceKey, time.Now().UTC(), err, response.StatusCode)
		return err
	}

	existing, err := s.db.ExchangeSymbol.QueryAssetExternalMappings("coingecko")
	if err != nil {
		return err
	}
	assetByExternalID := make(map[string]string, len(existing))
	for _, mapping := range existing {
		assetByExternalID[mapping.ExternalID] = mapping.AssetGuid
	}
	reviewedManifest, err := catalog.LoadEmbedded()
	if err != nil {
		return err
	}
	existingAssets, err := s.db.Asset.QueryAssets()
	if err != nil {
		return err
	}
	existingAssetIDs := make(map[string]struct{}, len(existingAssets))
	for _, item := range existingAssets {
		existingAssetIDs[item.Guid] = struct{}{}
	}
	preferredAssetByExternalID := make(map[string]string)
	for _, item := range reviewedManifest.Assets {
		preferredID := strings.TrimSpace(item.CanonicalAssetID)
		if preferredID == "" {
			continue
		}
		if _, exists := existingAssetIDs[preferredID]; exists {
			preferredAssetByExternalID[item.CoinGeckoID] = preferredID
		}
	}
	now := time.Now().UTC()
	assets := make([]database.Asset, 0, len(markets))
	mappings := make([]database.AssetExternalMapping, 0, len(markets))
	metrics := make([]database.AssetMetricCurrent, 0, len(markets))
	var latestProviderTime *time.Time
	for _, item := range markets {
		assetID := assetByExternalID[item.ID]
		if preferredID := preferredAssetByExternalID[item.ID]; preferredID != "" {
			assetID = preferredID
		}
		if assetID == "" {
			assetID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("coingecko:"+item.ID)).String()
		}
		providerTime := parseProviderTime(item.LastUpdated)
		if providerTime != nil && (latestProviderTime == nil || providerTime.After(*latestProviderTime)) {
			latestProviderTime = providerTime
		}
		assets = append(assets, database.Asset{
			Guid: assetID, AssetName: truncateText(item.Name, 50),
			AssetSymbol: truncateText(strings.ToUpper(item.Symbol), 20),
			AssetLogo:   "", IsActive: true, CreatedAt: now, UpdatedAt: now,
		})
		mappings = append(mappings, database.AssetExternalMapping{
			Provider: "coingecko", AssetGuid: assetID, ExternalID: item.ID,
		})
		metrics = append(metrics, database.AssetMetricCurrent{
			AssetGuid: assetID, Provider: "coingecko", ProviderAssetID: item.ID,
			MarketCapRank: item.MarketCapRank, ReferencePriceUSD: floatString(item.CurrentPrice),
			MarketCapUSD:      floatString(item.MarketCap),
			CirculatingSupply: floatString(item.CirculatingSupply),
			TotalSupply:       floatString(item.TotalSupply), MaxSupply: floatString(item.MaxSupply),
			ImageURL: optionalText(item.Image), ProviderUpdatedAt: providerTime,
			ObservedAt: now, UpdatedAt: now,
		})
	}
	if err := s.db.Asset.UpsertAssets(assets); err != nil {
		return err
	}
	if err := s.db.MarketAggregation.UpsertAssetExternalMappings(mappings); err != nil {
		return err
	}
	if err := s.db.MarketAggregation.UpsertAssetMetrics(metrics); err != nil {
		return err
	}
	if err := s.refreshCoinGeckoGlobal(ctx); err != nil {
		log.Warn("CoinGecko global metric refresh failed", "error", err)
	}
	s.reporter.Success("coingecko", sourceKey, now, latestProviderTime)
	log.Info("CoinGecko Top 200 universe refreshed", "assets", len(metrics))
	return nil
}

func (s *CatalogSupervisor) refreshCoinGeckoGlobal(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.coingecko.com/api/v3/global", nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("CoinGecko global HTTP %d", response.StatusCode)
	}
	var payload coinGeckoGlobalResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	now := time.Now().UTC()
	var providerTime *time.Time
	if payload.Data.UpdatedAt > 0 {
		value := time.Unix(payload.Data.UpdatedAt, 0).UTC()
		providerTime = &value
	}
	return s.db.MarketAggregation.UpsertGlobalMetric(database.MarketGlobalMetric{
		Provider:          "coingecko",
		TotalMarketCapUSD: floatStringValue(payload.Data.TotalMarketCap["usd"]),
		TotalVolume24hUSD: floatStringValue(payload.Data.TotalVolume["usd"]),
		BTCDominancePct:   floatStringValue(payload.Data.MarketCapPercentage["btc"]),
		ProviderUpdatedAt: providerTime, ObservedAt: now, UpdatedAt: now,
	})
}

func floatString(value *float64) *string {
	if value == nil {
		return nil
	}
	return floatStringValue(*value)
}

func floatStringValue(value float64) *string {
	text := fmt.Sprintf("%.18g", value)
	return &text
}

func parseProviderTime(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func truncateText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
