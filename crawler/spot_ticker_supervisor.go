package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/marketdata"
	"github.com/the-web3/s78-market-services/redis"
)

type normalizedSpotTicker struct {
	SourceSymbol   string
	Last           string
	Bid            string
	Ask            string
	Open24h        string
	Change24hPct   string
	QuoteTurnover  string
	SourceTime     *time.Time
	SourceTimeKind string
}

type spotTickerBatchAdapter interface {
	Provider() string
	Fetch(context.Context) (map[string]normalizedSpotTicker, error)
}

type spotTickerStreamAdapter interface {
	Provider() string
	Stream(context.Context, []string, chan<- normalizedSpotTicker) error
}

type SpotTickerSupervisor struct {
	db       *database.DB
	writer   *marketdata.SnapshotWriter
	reporter *marketdata.ProviderReporter
	client   *http.Client
	adapters []spotTickerBatchAdapter
	streams  []spotTickerStreamAdapter
	indexer  *marketdata.CompositeIndexer
	cancel   context.CancelFunc
	stopped  atomic.Bool
}

func NewSpotTickerSupervisor(db *database.DB, redisClient *redis.Client, _ bool) *SpotTickerSupervisor {
	client := &http.Client{Timeout: 10 * time.Second}
	adapters := []spotTickerBatchAdapter{
		&binanceBatchTickerAdapter{client: client, baseURL: binanceMarketDataRESTBaseURL},
		&bybitBatchTickerAdapter{client: client, baseURL: bybitV5RESTBaseURL},
		&okxBatchTickerAdapter{client: client, baseURL: "https://www.okx.com"},
	}
	streams := []spotTickerStreamAdapter{
		&binanceTickerStreamAdapter{},
		&bybitTickerStreamAdapter{},
		&okxTickerStreamAdapter{},
	}
	return &SpotTickerSupervisor{
		db: db, writer: marketdata.NewSnapshotWriter(db, redisClient),
		reporter: marketdata.NewProviderReporter(db.ProviderStatus),
		client:   client,
		adapters: adapters,
		streams:  streams,
		indexer:  marketdata.NewCompositeIndexer(db.MarketAggregation),
	}
}

func (s *SpotTickerSupervisor) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	for _, adapter := range s.adapters {
		adapter := adapter
		go s.superviseBatchAdapter(ctx, adapter)
	}
	for _, adapter := range s.streams {
		adapter := adapter
		go s.superviseStreamAdapter(ctx, adapter)
	}
	go s.superviseCoinbase(ctx)
	go s.superviseCoinbaseRESTFallback(ctx)
	go s.runCompositeIndexer(ctx)
	return nil
}

func (s *SpotTickerSupervisor) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.stopped.Store(true)
}

func (s *SpotTickerSupervisor) Stopped() bool { return s.stopped.Load() }

func (s *SpotTickerSupervisor) superviseBatchAdapter(ctx context.Context, adapter spotTickerBatchAdapter) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error("spot adapter panic isolated",
				"provider", adapter.Provider(), "panic", recovered, "stack", string(debug.Stack()))
			if ctx.Err() == nil {
				go s.superviseBatchAdapter(ctx, adapter)
			}
		}
	}()
	backoff := 5 * time.Second
	for {
		if s.fetchAndWriteBatch(ctx, adapter) {
			backoff = s.providerPollInterval(adapter.Provider())
		} else {
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
			sourceKey := "spot-tickers"
			if rollout, rolloutErr := s.db.MarketAggregation.QueryProviderRollout(
				adapter.Provider(),
			); rolloutErr == nil && rollout != nil && rollout.LocalPreviewEnabled {
				sourceKey = "spot-tickers-preview"
			} else if rolloutErr == nil && rollout != nil &&
				(rollout.Mode == "shadow" || rollout.Mode == "paused") {
				sourceKey = "spot-tickers-shadow"
			}
			s.reporter.NextRetry(
				adapter.Provider(), sourceKey, time.Now().UTC().Add(backoff),
			)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (s *SpotTickerSupervisor) providerPollInterval(provider string) time.Duration {
	rollout, err := s.db.MarketAggregation.QueryProviderRollout(provider)
	if err == nil && rollout != nil &&
		(rollout.Mode == "shadow" || rollout.Mode == "paused") {
		return time.Minute
	}
	// WebSocket is the primary realtime feed. REST is deliberately slower and
	// only reconciles quiet/missed symbols or keeps the product useful while a
	// stream is reconnecting.
	return 30 * time.Second
}

func (s *SpotTickerSupervisor) fetchAndWriteBatch(ctx context.Context, adapter spotTickerBatchAdapter) bool {
	provider := adapter.Provider()
	rollout, err := s.db.MarketAggregation.QueryProviderRollout(provider)
	if err != nil {
		log.Warn("spot ticker rollout state unavailable", "provider", provider, "error", err)
		return false
	}
	if rollout == nil {
		return true
	}
	if !rollout.LocalPreviewEnabled &&
		(rollout.Mode == "shadow" || rollout.Mode == "paused") {
		return s.probeBatchAdapter(ctx, adapter, rollout.RankLimit)
	}
	sourceKey := "spot-tickers-rest-reconcile"
	if !rollout.LocalPreviewEnabled &&
		(rollout.Mode == "shadow" || rollout.Mode == "paused") {
		sourceKey = "spot-tickers-rest-shadow"
	}
	allowed, _, err := s.db.MarketAggregation.QueryPublishedAssetIDs(provider)
	if err != nil {
		log.Warn("spot ticker rollout asset query failed", "provider", provider, "error", err)
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	markets, err := s.db.ExchangeSymbol.QueryProviderMarkets(provider)
	if err != nil {
		log.Warn("spot ticker enabled market query failed", "provider", provider, "error", err)
		return false
	}
	if len(markets) == 0 {
		// A rollout command may precede the next successful catalog refresh.
		// Do not start the promotion observation clock until the fixed canary
		// has actual enabled markets to validate and write.
		log.Warn("spot ticker rollout has no enabled markets yet", "provider", provider)
		return false
	}
	markets = filterProviderMarketsByAssetIDs(markets, allowed)
	if len(markets) == 0 {
		log.Warn("spot ticker publication has no markets in the effective asset boundary",
			"provider", provider, "local_preview", rollout.LocalPreviewEnabled)
		return false
	}
	attemptedAt := time.Now().UTC()
	s.reporter.Attempt(provider, sourceKey, attemptedAt)
	tickers, err := adapter.Fetch(ctx)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, httpStatusFromError(err))
		log.Warn("spot ticker batch failed", "provider", provider, "error", err)
		return false
	}
	latestSource, writeCount := s.writeProviderTickers(ctx, markets, tickers)
	if writeCount == 0 {
		err := fmt.Errorf("no enabled market ticker matched the provider batch")
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, 0)
		log.Warn("spot ticker batch produced no formal writes",
			"provider", provider, "received", len(tickers), "enabled_markets", len(markets))
		return false
	}
	details := tickerEvidenceForProviderMarkets(markets, tickers, writeCount, time.Now().UTC())
	s.reporter.SuccessWithDetails(
		provider, sourceKey, time.Now().UTC(), latestSource, details,
	)
	log.Debug("spot ticker batch observed",
		"provider", provider, "received", len(tickers), "written", writeCount)
	return true
}

func (s *SpotTickerSupervisor) probeBatchAdapter(
	ctx context.Context,
	adapter spotTickerBatchAdapter,
	rankLimit int,
) bool {
	provider := adapter.Provider()
	sourceKey := "spot-tickers-shadow"
	candidates, err := s.db.MarketAggregation.QueryEligibleProviderFeedMarkets(provider, rankLimit)
	if err != nil {
		log.Warn("shadow ticker candidate query failed", "provider", provider, "error", err)
		return false
	}
	if len(candidates) == 0 {
		return true
	}
	s.reporter.Attempt(provider, sourceKey, time.Now().UTC())
	tickers, err := adapter.Fetch(ctx)
	if err != nil {
		s.reporter.Failure(provider, sourceKey, time.Now().UTC(), err, httpStatusFromError(err))
		log.Warn("shadow spot ticker batch failed", "provider", provider, "error", err)
		return false
	}
	details, latestSource := tickerEvidenceForFeedMarkets(candidates, tickers, time.Now().UTC())
	s.reporter.SuccessWithDetails(
		provider, sourceKey, time.Now().UTC(), latestSource, details,
	)
	log.Debug("shadow spot ticker batch observed",
		"provider", provider, "received", len(tickers),
		"matched_assets", details.MatchedAssetCount,
		"prices", details.PriceAvailableCount,
		"changes", details.ChangeAvailableCount)
	return true
}

func (s *SpotTickerSupervisor) writeProviderTickers(
	ctx context.Context,
	markets []database.ProviderMarket,
	tickers map[string]normalizedSpotTicker,
) (*time.Time, int) {
	var latestSource *time.Time
	written := 0
	for _, market := range markets {
		if !strings.EqualFold(market.MarketType, "spot") {
			continue
		}
		ticker, ok := tickers[market.SourceSymbol]
		if !ok {
			continue
		}
		snapshot, err := normalizedTickerSnapshot(market, ticker, time.Now().UTC())
		if err != nil {
			log.Warn("spot ticker normalization rejected",
				"provider", market.Provider, "market_id", market.MarketID, "error", err)
			continue
		}
		if _, err := s.writer.Write(ctx, snapshot); err != nil {
			log.Warn("spot ticker write failed",
				"provider", market.Provider, "market_id", market.MarketID, "error", err)
			continue
		}
		written++
		if ticker.SourceTime != nil && (latestSource == nil || ticker.SourceTime.After(*latestSource)) {
			value := ticker.SourceTime.UTC()
			latestSource = &value
		}
	}
	return latestSource, written
}

func normalizedTickerSnapshot(
	market database.ProviderMarket,
	ticker normalizedSpotTicker,
	observedAt time.Time,
) (marketdata.Snapshot, error) {
	price, err := decimalStringToUint256String(ticker.Last, 8)
	if err != nil {
		return marketdata.Snapshot{}, err
	}
	bid, err := decimalStringToUint256String(fallbackString(ticker.Bid, ticker.Last), 8)
	if err != nil {
		return marketdata.Snapshot{}, err
	}
	ask, err := decimalStringToUint256String(fallbackString(ticker.Ask, ticker.Last), 8)
	if err != nil {
		return marketdata.Snapshot{}, err
	}
	turnover, err := decimalStringToUint256String(fallbackString(ticker.QuoteTurnover, "0"), 8)
	if err != nil {
		return marketdata.Snapshot{}, err
	}
	var openScaled *string
	var change *string
	if strings.TrimSpace(ticker.Change24hPct) != "" {
		changeDecimal, parseErr := decimal.NewFromString(strings.TrimSpace(ticker.Change24hPct))
		if parseErr != nil {
			return marketdata.Snapshot{}, fmt.Errorf("invalid 24h change: %w", parseErr)
		}
		value := changeDecimal.String()
		change = &value
	}
	open24h := strings.TrimSpace(ticker.Open24h)
	if open24h == "" && change != nil {
		open24h = inferOpen24h(ticker.Last, *change)
	}
	if open24h != "" {
		open, parseErr := decimalStringToUint256String(open24h, 8)
		if parseErr != nil {
			return marketdata.Snapshot{}, parseErr
		}
		openScaled = &open
		lastDecimal, lastErr := decimal.NewFromString(ticker.Last)
		openDecimal, openErr := decimal.NewFromString(open24h)
		if change == nil && lastErr == nil && openErr == nil && openDecimal.GreaterThan(decimal.Zero) {
			value := lastDecimal.Sub(openDecimal).Div(openDecimal).Mul(decimal.NewFromInt(100)).String()
			change = &value
		}
	}
	var kind *string
	if ticker.SourceTime != nil && strings.TrimSpace(ticker.SourceTimeKind) != "" {
		value := ticker.SourceTimeKind
		kind = &value
	}
	return marketdata.Snapshot{
		MarketSnapshotInput: database.MarketSnapshotInput{
			Guid: "m-" + market.MarketID, MarketID: market.MarketID, SymbolGuid: market.SymbolGuid,
			Price: price, BidPrice: bid, AskPrice: ask, Volume: turnover,
			Open24h: openScaled, QuoteTurnover24h: &turnover,
			Change24hPct: change, IsActive: true, ObservedAt: observedAt,
			SourceTime: ticker.SourceTime, SourceTimeKind: kind,
		},
		ExchangeGuid: market.ExchangeGuid, ExchangeName: market.ExchangeName,
		SymbolName: market.SymbolName,
	}, nil
}

func (s *SpotTickerSupervisor) runCompositeIndexer(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.indexer.RunOnce(time.Now().UTC()); err != nil {
			log.Warn("asset composite index refresh failed", "error", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (s *SpotTickerSupervisor) superviseCoinbase(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := s.runCoinbaseSession(ctx); err != nil && ctx.Err() == nil {
			sourceKey := s.coinbaseSourceKey()
			s.reporter.Failure("coinbase", sourceKey, time.Now().UTC(), err, 0)
			s.reporter.NextRetry("coinbase", sourceKey, time.Now().UTC().Add(backoff))
			log.Warn("Coinbase ticker_batch disconnected", "error", err, "retry_in", backoff)
		} else {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (s *SpotTickerSupervisor) coinbaseSourceKey() string {
	rollout, err := s.db.MarketAggregation.QueryProviderRollout("coinbase")
	if err == nil && rollout != nil && rollout.LocalPreviewEnabled {
		return "spot-tickers-preview"
	}
	if err == nil && rollout != nil &&
		(rollout.Mode == "shadow" || rollout.Mode == "paused") {
		return "spot-tickers-shadow"
	}
	return "spot-tickers"
}

func (s *SpotTickerSupervisor) runCoinbaseSession(ctx context.Context) error {
	rollout, err := s.db.MarketAggregation.QueryProviderRollout("coinbase")
	if err != nil {
		return err
	}
	if rollout == nil {
		select {
		case <-time.After(30 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	published := rollout.LocalPreviewEnabled ||
		rollout.Mode == "canary" || rollout.Mode == "enabled"
	sourceKey := "spot-tickers-shadow"
	flushInterval := 30 * time.Second
	var markets []database.ProviderMarket
	var candidates []database.ProviderFeedMarket
	var productIDs []string
	if published {
		sourceKey = "spot-tickers"
		if rollout.LocalPreviewEnabled {
			sourceKey = "spot-tickers-preview"
		}
		flushInterval = 5 * time.Second
		allowed, _, queryErr := s.db.MarketAggregation.QueryPublishedAssetIDs("coinbase")
		if queryErr != nil {
			return queryErr
		}
		if len(allowed) == 0 {
			return fmt.Errorf("coinbase formal rollout exposes no assets")
		}
		markets, err = s.db.ExchangeSymbol.QueryProviderMarkets("coinbase")
		if err != nil {
			return err
		}
		markets = filterProviderMarketsByAssetIDs(markets, allowed)
		for _, market := range markets {
			if strings.EqualFold(market.MarketType, "spot") {
				productIDs = append(productIDs, market.SourceSymbol)
			}
		}
	} else {
		candidates, err = s.db.MarketAggregation.QueryEligibleProviderFeedMarkets(
			"coinbase", rollout.RankLimit,
		)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			productIDs = append(productIDs, candidate.SourceSymbol)
		}
	}
	productIDs = uniqueStrings(productIDs)
	if len(productIDs) == 0 {
		select {
		case <-time.After(30 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	sort.Strings(productIDs)
	s.reporter.Attempt("coinbase", sourceKey, time.Now().UTC())
	connection, _, err := websocket.DefaultDialer.DialContext(ctx, "wss://ws-feed.exchange.coinbase.com", nil)
	if err != nil {
		return err
	}
	defer connection.Close()
	if err := connection.WriteJSON(map[string]any{
		"type": "subscribe", "product_ids": productIDs, "channels": []string{"ticker_batch"},
	}); err != nil {
		return err
	}
	tickers := make(map[string]normalizedSpotTicker)
	lastFlush := time.Now()
	for {
		_ = connection.SetReadDeadline(time.Now().Add(20 * time.Second))
		var message map[string]any
		if err := connection.ReadJSON(&message); err != nil {
			return err
		}
		parseCoinbaseTickerMessage(message, tickers)
		if time.Since(lastFlush) >= flushInterval && len(tickers) > 0 {
			s.reporter.Attempt("coinbase", sourceKey, time.Now().UTC())
			var latest *time.Time
			var details database.ProviderStatusDetails
			if published {
				var written int
				latest, written = s.writeProviderTickers(ctx, markets, tickers)
				if written == 0 {
					return fmt.Errorf("Coinbase ticker_batch matched no enabled markets")
				}
				details = tickerEvidenceForProviderMarkets(
					markets, tickers, written, time.Now().UTC(),
				)
			} else {
				details, latest = tickerEvidenceForFeedMarkets(
					candidates, tickers, time.Now().UTC(),
				)
			}
			s.reporter.SuccessWithDetails(
				"coinbase", sourceKey, time.Now().UTC(), latest, details,
			)
			log.Debug("Coinbase ticker_batch observed",
				"mode", rollout.Mode, "local_preview", rollout.LocalPreviewEnabled,
				"received", len(tickers), "written", details.WrittenCount,
				"matched_assets", details.MatchedAssetCount)
			tickers = make(map[string]normalizedSpotTicker)
			lastFlush = time.Now()
			current, queryErr := s.db.MarketAggregation.QueryProviderRollout("coinbase")
			if queryErr != nil {
				return queryErr
			}
			if current == nil || current.Mode != rollout.Mode ||
				current.LocalPreviewEnabled != rollout.LocalPreviewEnabled {
				return nil
			}
		}
	}
}

// superviseCoinbaseRESTFallback complements ticker_batch for quiet products.
// Coinbase documents ticker_batch as emitting every five seconds only when a
// price changes. The REST ticker/stats snapshots therefore refresh selected
// assets that have not produced a WebSocket event recently, without replacing
// the WebSocket as the primary low-latency feed.
func (s *SpotTickerSupervisor) superviseCoinbaseRESTFallback(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.refreshCoinbaseRESTFallback(ctx, time.Now().UTC()); err != nil &&
			ctx.Err() == nil {
			log.Warn("Coinbase REST fallback refresh failed", "error", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (s *SpotTickerSupervisor) refreshCoinbaseRESTFallback(
	ctx context.Context,
	now time.Time,
) error {
	rollout, err := s.db.MarketAggregation.QueryProviderRollout("coinbase")
	if err != nil {
		return err
	}
	if rollout == nil || (!rollout.LocalPreviewEnabled &&
		rollout.Mode != "canary" && rollout.Mode != "enabled") {
		return nil
	}
	allowed, _, err := s.db.MarketAggregation.QueryPublishedAssetIDs("coinbase")
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		return nil
	}
	freshIDs, err := s.db.MarketAggregation.QueryFreshVenueAssetIDs(
		"coinbase", "venue_spot", now.Add(-20*time.Second),
	)
	if err != nil {
		return err
	}
	fresh := make(map[string]struct{}, len(freshIDs))
	for _, assetID := range freshIDs {
		fresh[assetID] = struct{}{}
	}
	markets, err := s.db.ExchangeSymbol.QueryProviderMarkets("coinbase")
	if err != nil {
		return err
	}
	markets = selectCoinbaseFallbackMarkets(markets, allowed, fresh)
	if len(markets) == 0 {
		return nil
	}
	sourceKey := "spot-tickers-rest-fallback"
	s.reporter.Attempt("coinbase", sourceKey, now)
	tickers, latest, err := fetchCoinbaseRESTFallback(
		ctx, s.client, "https://api.exchange.coinbase.com", markets,
	)
	if err != nil {
		s.reporter.Failure("coinbase", sourceKey, time.Now().UTC(), err, httpStatusFromError(err))
		return err
	}
	latestSource, written := s.writeProviderTickers(ctx, markets, tickers)
	if written == 0 {
		err := fmt.Errorf("Coinbase REST fallback produced no writes")
		s.reporter.Failure("coinbase", sourceKey, time.Now().UTC(), err, 0)
		return err
	}
	if latestSource != nil && (latest == nil || latestSource.After(*latest)) {
		latest = latestSource
	}
	details := tickerEvidenceForProviderMarkets(markets, tickers, written, time.Now().UTC())
	s.reporter.SuccessWithDetails("coinbase", sourceKey, time.Now().UTC(), latest, details)
	log.Debug("Coinbase REST fallback observed",
		"requested_assets", len(markets), "received", len(tickers), "written", written)
	return nil
}

func selectCoinbaseFallbackMarkets(
	markets []database.ProviderMarket,
	allowed map[string]struct{},
	fresh map[string]struct{},
) []database.ProviderMarket {
	selected := make([]database.ProviderMarket, 0)
	seen := make(map[string]struct{})
	for _, market := range markets {
		if !strings.EqualFold(market.MarketType, "spot") {
			continue
		}
		if _, ok := allowed[market.BaseAssetID]; !ok {
			continue
		}
		if _, ok := fresh[market.BaseAssetID]; ok {
			continue
		}
		if _, ok := seen[market.BaseAssetID]; ok {
			continue
		}
		seen[market.BaseAssetID] = struct{}{}
		selected = append(selected, market)
	}
	return selected
}

type coinbaseRESTTicker struct {
	Price  string `json:"price"`
	Bid    string `json:"bid"`
	Ask    string `json:"ask"`
	Volume string `json:"volume"`
	Time   string `json:"time"`
}

type coinbaseRESTStats struct {
	Open   string `json:"open"`
	Volume string `json:"volume"`
	Last   string `json:"last"`
}

type coinbaseRESTResult struct {
	sourceSymbol string
	ticker       normalizedSpotTicker
	err          error
}

func fetchCoinbaseRESTFallback(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	markets []database.ProviderMarket,
) (map[string]normalizedSpotTicker, *time.Time, error) {
	const workers = 2
	jobs := make(chan database.ProviderMarket)
	results := make(chan coinbaseRESTResult, len(markets))
	worker := func() {
		for market := range jobs {
			ticker, err := fetchCoinbaseRESTProduct(ctx, client, baseURL, market.SourceSymbol)
			results <- coinbaseRESTResult{
				sourceSymbol: market.SourceSymbol,
				ticker:       ticker,
				err:          err,
			}
		}
	}
	for i := 0; i < workers; i++ {
		go worker()
	}
	go func() {
		defer close(jobs)
		for _, market := range markets {
			select {
			case jobs <- market:
			case <-ctx.Done():
				return
			}
		}
	}()
	tickers := make(map[string]normalizedSpotTicker, len(markets))
	var latest *time.Time
	var failures []string
	for range markets {
		select {
		case result := <-results:
			if result.err != nil {
				failures = append(failures, result.sourceSymbol)
				continue
			}
			tickers[result.sourceSymbol] = result.ticker
			if result.ticker.SourceTime != nil &&
				(latest == nil || result.ticker.SourceTime.After(*latest)) {
				value := result.ticker.SourceTime.UTC()
				latest = &value
			}
		case <-ctx.Done():
			return tickers, latest, ctx.Err()
		}
	}
	if len(tickers) == 0 && len(failures) > 0 {
		return nil, nil, fmt.Errorf(
			"Coinbase REST fallback failed for %d products", len(failures),
		)
	}
	if len(failures) > 0 {
		log.Debug("Coinbase REST fallback partial",
			"failed_products", len(failures), "successful_products", len(tickers))
	}
	return tickers, latest, nil
}

func fetchCoinbaseRESTProduct(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	productID string,
) (normalizedSpotTicker, error) {
	var ticker coinbaseRESTTicker
	if err := getProviderJSON(
		ctx, client, strings.TrimRight(baseURL, "/")+"/products/"+
			productID+"/ticker", &ticker,
	); err != nil {
		return normalizedSpotTicker{}, err
	}
	var stats coinbaseRESTStats
	if err := getProviderJSON(
		ctx, client, strings.TrimRight(baseURL, "/")+"/products/"+
			productID+"/stats", &stats,
	); err != nil {
		return normalizedSpotTicker{}, err
	}
	last := firstNonEmpty(ticker.Price, stats.Last)
	if !positiveDecimal(last) {
		return normalizedSpotTicker{}, fmt.Errorf(
			"Coinbase REST fallback product %s has no positive price", productID,
		)
	}
	volume := firstNonEmpty(stats.Volume, ticker.Volume)
	return normalizedSpotTicker{
		SourceSymbol:   productID,
		Last:           last,
		Bid:            ticker.Bid,
		Ask:            ticker.Ask,
		Open24h:        stats.Open,
		QuoteTurnover:  multiplyDecimalStrings(volume, last),
		SourceTime:     parseTickerTime(ticker.Time),
		SourceTimeKind: "ticker_event",
	}, nil
}

func filterProviderMarketsByAssetIDs(
	markets []database.ProviderMarket,
	allowed map[string]struct{},
) []database.ProviderMarket {
	result := make([]database.ProviderMarket, 0, len(markets))
	for _, market := range markets {
		if _, ok := allowed[market.BaseAssetID]; ok {
			result = append(result, market)
		}
	}
	return result
}

func tickerEvidenceForProviderMarkets(
	markets []database.ProviderMarket,
	tickers map[string]normalizedSpotTicker,
	written int,
	at time.Time,
) database.ProviderStatusDetails {
	feedMarkets := make([]database.ProviderFeedMarket, 0, len(markets))
	for _, market := range markets {
		if strings.EqualFold(market.MarketType, "spot") {
			feedMarkets = append(feedMarkets, database.ProviderFeedMarket{
				SourceSymbol: market.SourceSymbol,
				AssetID:      market.BaseAssetID,
			})
		}
	}
	details, _ := tickerEvidenceForFeedMarkets(feedMarkets, tickers, at)
	details.WrittenCount = int64(written)
	return details
}

func tickerEvidenceForFeedMarkets(
	markets []database.ProviderFeedMarket,
	tickers map[string]normalizedSpotTicker,
	at time.Time,
) (database.ProviderStatusDetails, *time.Time) {
	matched := make(map[string]struct{})
	priced := make(map[string]struct{})
	changed := make(map[string]struct{})
	var latestSource *time.Time
	for _, market := range markets {
		ticker, ok := tickers[market.SourceSymbol]
		if !ok {
			continue
		}
		matched[market.AssetID] = struct{}{}
		if positiveDecimal(ticker.Last) {
			priced[market.AssetID] = struct{}{}
		}
		if strings.TrimSpace(ticker.Change24hPct) != "" ||
			positiveDecimal(ticker.Open24h) {
			changed[market.AssetID] = struct{}{}
		}
		if ticker.SourceTime != nil &&
			(latestSource == nil || ticker.SourceTime.After(*latestSource)) {
			value := ticker.SourceTime.UTC()
			latestSource = &value
		}
	}
	return database.ProviderStatusDetails{
		ReceivedCount:        int64(len(tickers)),
		MatchedAssetCount:    int64(len(matched)),
		PriceAvailableCount:  int64(len(priced)),
		ChangeAvailableCount: int64(len(changed)),
		ProbeObservedAt:      at.UTC().UnixMilli(),
	}, latestSource
}

func positiveDecimal(value string) bool {
	parsed, err := decimal.NewFromString(strings.TrimSpace(value))
	return err == nil && parsed.GreaterThan(decimal.Zero)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func parseCoinbaseTickerMessage(message map[string]any, target map[string]normalizedSpotTicker) {
	// Exchange ticker_batch uses the ticker message shape. Advanced channel
	// envelopes are accepted as well so the adapter can migrate endpoints
	// without changing the normalized writer.
	if product, ok := message["product_id"].(string); ok {
		ticker := normalizedSpotTicker{
			SourceSymbol:   product,
			Last:           stringValue(message["price"]),
			Bid:            stringValue(message["best_bid"]),
			Ask:            stringValue(message["best_ask"]),
			SourceTime:     parseTickerTime(stringValue(message["time"])),
			SourceTimeKind: "ticker_event",
		}
		ticker.Change24hPct = stringValue(message["price_percent_chg_24_h"])
		ticker.QuoteTurnover = multiplyDecimalStrings(stringValue(message["volume_24h"]), ticker.Last)
		ticker.Open24h = firstNonEmpty(
			stringValue(message["open_24h"]),
			inferOpen24h(ticker.Last, stringValue(message["price_percent_chg_24_h"])),
		)
		target[product] = ticker
	}
	events, _ := message["events"].([]any)
	for _, rawEvent := range events {
		event, _ := rawEvent.(map[string]any)
		rows, _ := event["tickers"].([]any)
		for _, rawTicker := range rows {
			row, _ := rawTicker.(map[string]any)
			product := stringValue(row["product_id"])
			if product == "" {
				continue
			}
			ticker := normalizedSpotTicker{
				SourceSymbol: product, Last: stringValue(row["price"]),
				Bid: stringValue(row["best_bid"]), Ask: stringValue(row["best_ask"]),
				SourceTime:     parseTickerTime(stringValue(event["timestamp"])),
				SourceTimeKind: "ticker_event",
			}
			ticker.Change24hPct = stringValue(row["price_percent_chg_24_h"])
			ticker.QuoteTurnover = multiplyDecimalStrings(stringValue(row["volume_24_h"]), ticker.Last)
			ticker.Open24h = firstNonEmpty(
				stringValue(row["open_24_h"]),
				inferOpen24h(ticker.Last, stringValue(row["price_percent_chg_24_h"])),
			)
			target[product] = ticker
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type binanceBatchTickerAdapter struct {
	client  *http.Client
	baseURL string
}

func (*binanceBatchTickerAdapter) Provider() string { return "binance" }

func (a *binanceBatchTickerAdapter) Fetch(ctx context.Context) (map[string]normalizedSpotTicker, error) {
	var payload []binanceTicker
	if err := getProviderJSON(ctx, a.client, a.baseURL+"/api/v3/ticker/24hr", &payload); err != nil {
		return nil, err
	}
	result := make(map[string]normalizedSpotTicker, len(payload))
	for _, item := range payload {
		source := time.UnixMilli(item.CloseTime).UTC()
		open := inferOpen24h(item.LastPrice, item.PriceChangePercent)
		result[item.Symbol] = normalizedSpotTicker{
			SourceSymbol: item.Symbol, Last: item.LastPrice, Bid: item.BidPrice, Ask: item.AskPrice,
			Open24h: open, Change24hPct: item.PriceChangePercent,
			QuoteTurnover: item.QuoteVolume, SourceTime: &source,
			SourceTimeKind: "ticker_window_close",
		}
	}
	return result, nil
}

type bybitBatchTickerAdapter struct {
	client  *http.Client
	baseURL string
}

func (*bybitBatchTickerAdapter) Provider() string { return "bybit" }

func (a *bybitBatchTickerAdapter) Fetch(ctx context.Context) (map[string]normalizedSpotTicker, error) {
	var payload struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Time    int64  `json:"time"`
		Result  struct {
			List []struct {
				Symbol       string `json:"symbol"`
				LastPrice    string `json:"lastPrice"`
				Bid1Price    string `json:"bid1Price"`
				Ask1Price    string `json:"ask1Price"`
				PrevPrice24h string `json:"prevPrice24h"`
				Turnover24h  string `json:"turnover24h"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := getProviderJSON(ctx, a.client, a.baseURL+"/v5/market/tickers?category=spot", &payload); err != nil {
		return nil, err
	}
	if payload.RetCode != 0 {
		return nil, fmt.Errorf("Bybit tickers retCode=%d retMsg=%s", payload.RetCode, payload.RetMsg)
	}
	source := time.UnixMilli(payload.Time).UTC()
	result := make(map[string]normalizedSpotTicker, len(payload.Result.List))
	for _, item := range payload.Result.List {
		result[item.Symbol] = normalizedSpotTicker{
			SourceSymbol: item.Symbol, Last: item.LastPrice, Bid: item.Bid1Price, Ask: item.Ask1Price,
			Open24h: item.PrevPrice24h, QuoteTurnover: item.Turnover24h, SourceTime: &source,
			SourceTimeKind: "provider_response_time",
		}
	}
	return result, nil
}

type okxBatchTickerAdapter struct {
	client  *http.Client
	baseURL string
}

func (*okxBatchTickerAdapter) Provider() string { return "okx" }

func (a *okxBatchTickerAdapter) Fetch(ctx context.Context) (map[string]normalizedSpotTicker, error) {
	var payload struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			InstID    string `json:"instId"`
			Last      string `json:"last"`
			BidPx     string `json:"bidPx"`
			AskPx     string `json:"askPx"`
			Open24h   string `json:"open24h"`
			VolCcy24h string `json:"volCcy24h"`
			Timestamp string `json:"ts"`
		} `json:"data"`
	}
	if err := getProviderJSON(ctx, a.client, a.baseURL+"/api/v5/market/tickers?instType=SPOT", &payload); err != nil {
		return nil, err
	}
	if payload.Code != "0" {
		return nil, fmt.Errorf("OKX tickers code=%s msg=%s", payload.Code, payload.Msg)
	}
	result := make(map[string]normalizedSpotTicker, len(payload.Data))
	for _, item := range payload.Data {
		result[item.InstID] = normalizedSpotTicker{
			SourceSymbol: item.InstID, Last: item.Last, Bid: item.BidPx, Ask: item.AskPx,
			Open24h: item.Open24h, QuoteTurnover: item.VolCcy24h,
			SourceTime:     parseTickerMilliseconds(item.Timestamp),
			SourceTimeKind: "ticker_event",
		}
	}
	return result, nil
}

func inferOpen24h(last, changePct string) string {
	lastValue, lastErr := decimal.NewFromString(strings.TrimSpace(last))
	change, changeErr := decimal.NewFromString(strings.TrimSpace(changePct))
	if lastErr != nil || changeErr != nil {
		return ""
	}
	denominator := decimal.NewFromInt(1).Add(change.Div(decimal.NewFromInt(100)))
	if denominator.LessThanOrEqual(decimal.Zero) {
		return ""
	}
	return lastValue.Div(denominator).String()
}

func multiplyDecimalStrings(left, right string) string {
	leftValue, leftErr := decimal.NewFromString(strings.TrimSpace(left))
	rightValue, rightErr := decimal.NewFromString(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil {
		return ""
	}
	return leftValue.Mul(rightValue).String()
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return ""
		}
		return fmt.Sprintf("%.18g", typed)
	default:
		return ""
	}
}

func parseTickerTime(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func parseTickerMilliseconds(value string) *time.Time {
	milliseconds, err := decimal.NewFromString(value)
	if err != nil {
		return nil
	}
	result := time.UnixMilli(milliseconds.IntPart()).UTC()
	return &result
}

func httpStatusFromError(err error) int {
	var httpErr *providerHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.status
	}
	return 0
}
