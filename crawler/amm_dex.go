package crawler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/shopspring/decimal"

	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/marketdata"
	"github.com/the-web3/s78-market-services/redis"
)

const (
	dexDiscoveryInterval = 6 * time.Hour
	dexQuoteInterval     = 15 * time.Second
	// Historical observations are produced by a bounded, multi-route sweep.
	// Keep its continuity SLA separate from the 30-second public quote
	// freshness rule: the current successful quote is the window endpoint,
	// while prior same-route/same-notional samples may be at most ten minutes
	// apart.
	dexHistoricalObservationMaxGap = 10 * time.Minute
	// Keep enough same-route/same-notional history to prove the 72-hour
	// production gate with margin and to support a later seven-day review.
	dexObservationRetention = 8 * 24 * time.Hour
	dexMinimumTVLUSD        = int64(100_000)
	dexMinimumVolumeUSD     = int64(5_000)
	dexMaxRoutesPerAsset    = 12
	publicEthereumRPCURL    = "https://ethereum-rpc.publicnode.com"
	publicBSCRPCURL         = "https://bsc-rpc.publicnode.com"
	dexScreenerAPIURL       = "https://api.dexscreener.com/token-pairs/v1"
)

var (
	dexQuoteNotionalLadderUSD = []int64{10_000, 1_000, 100}
	uniswapAMMConfig          = ammProviderConfig{
		Provider: "uniswap", ChainID: 1,
		V2FactoryAddress: "0x5c69bee701ef814a2b6a3edd4b1652cb9cc5aa6f",
		V2RouterAddress:  "0x7a250d5630b4cf539739df2c5dacab4c659f2488d",
		V3FactoryAddress: "0x1f98431c8ad98523631ae4a59f267346ea31f984",
		V3QuoterAddress:  "0x61ffe014ba17989e743c5f6cb21bf9697530b21e",
		StableAddress:    "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
		BridgeAddress:    "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2",
	}
	pancakeAMMConfig = ammProviderConfig{
		Provider: "pancakeswap", ChainID: 56,
		V2FactoryAddress: "0xca143ce32fe78f1f7019d7d551a6402fc5350c73",
		V2RouterAddress:  "0x10ed43c718714eb63d5aa57b78b54704e256024e",
		V3FactoryAddress: "0x0bfbcf9fa4f9c56b0f40a671ad40e0805a091865",
		V3QuoterAddress:  "0xb048bbc1ee6b733fffcfb9e9cef7375518e25997",
		StableAddress:    "0x55d398326f99059ff775485246999027b3197955",
		BridgeAddress:    "0xbb4cdb9cbd36b01bd1cbaebf2de08d9173bc095c",
	}
)

type ammProviderConfig struct {
	Provider         string
	ChainID          int64
	V2FactoryAddress string
	V2RouterAddress  string
	V3FactoryAddress string
	V3QuoterAddress  string
	StableAddress    string
	BridgeAddress    string
	RPCURL           string
	SubgraphURL      string
	PublicDiscovery  bool
	DiscoveryURL     string
}

type dexIdentityError struct {
	message string
}

func (e *dexIdentityError) Error() string { return e.message }

func isDexIdentityError(err error) bool {
	var identityErr *dexIdentityError
	return errors.As(err, &identityErr)
}

// DexSupervisor keeps the historical Hyperliquid process name while isolating
// three independent data-source loops inside the same local runtime role.
type DexSupervisor struct {
	hyperliquid *DexCrawler
	adapters    []*AMMAdapter
	stopped     atomic.Bool
}

func NewDexSupervisor(db *database.DB, redisClient *redis.Client, cfg *config.Config) *DexSupervisor {
	adapters := buildAMMProviderConfigs(cfg)
	return &DexSupervisor{
		hyperliquid: NewDexCrawler(db, redisClient),
		adapters: []*AMMAdapter{
			NewAMMAdapter(db, adapters[0]),
			NewAMMAdapter(db, adapters[1]),
		},
	}
}

func buildAMMProviderConfigs(cfg *config.Config) [2]ammProviderConfig {
	uniswap := uniswapAMMConfig
	uniswap.RPCURL = strings.TrimSpace(cfg.EthereumRPCURL)
	uniswap.SubgraphURL = strings.TrimSpace(cfg.UniswapV3SubgraphURL)
	pancake := pancakeAMMConfig
	pancake.RPCURL = strings.TrimSpace(cfg.BSCRPCURL)
	pancake.SubgraphURL = strings.TrimSpace(cfg.PancakeV3SubgraphURL)
	if cfg.DexPublicFallback {
		if uniswap.RPCURL == "" {
			uniswap.RPCURL = publicEthereumRPCURL
		}
		if uniswap.SubgraphURL == "" {
			uniswap.PublicDiscovery = true
			uniswap.DiscoveryURL = dexScreenerAPIURL
		}
		if pancake.RPCURL == "" {
			pancake.RPCURL = publicBSCRPCURL
		}
		if pancake.SubgraphURL == "" {
			pancake.PublicDiscovery = true
			pancake.DiscoveryURL = dexScreenerAPIURL
		}
	}
	return [2]ammProviderConfig{uniswap, pancake}
}

func (s *DexSupervisor) Start(ctx context.Context) error {
	if err := s.hyperliquid.Start(ctx); err != nil {
		return err
	}
	for _, adapter := range s.adapters {
		adapter.Start(ctx)
	}
	return nil
}

func (s *DexSupervisor) Stop(ctx context.Context) error {
	for _, adapter := range s.adapters {
		adapter.Stop()
	}
	err := s.hyperliquid.Stop(ctx)
	s.stopped.Store(true)
	return err
}

func (s *DexSupervisor) Stopped() bool { return s.stopped.Load() }

type graphToken struct {
	ID       string `json:"id"`
	Symbol   string `json:"symbol"`
	Decimals string `json:"decimals"`
}

type graphPool struct {
	ID                  string     `json:"id"`
	FeeTier             string     `json:"feeTier"`
	TotalValueLockedUSD string     `json:"totalValueLockedUSD"`
	Token0Price         string     `json:"token0Price"`
	Token1Price         string     `json:"token1Price"`
	Token0              graphToken `json:"token0"`
	Token1              graphToken `json:"token1"`
	DayData             []graphDay `json:"poolDayData"`
}

type graphDay struct {
	VolumeUSD string `json:"volumeUSD"`
}

type graphEnvelope struct {
	Data struct {
		Pools0 []graphPool `json:"pools0"`
		Pools1 []graphPool `json:"pools1"`
		Meta   struct {
			Block struct {
				Number int64 `json:"number"`
			} `json:"block"`
		} `json:"_meta"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type dexScreenerPair struct {
	ChainID     string   `json:"chainId"`
	DexID       string   `json:"dexId"`
	PairAddress string   `json:"pairAddress"`
	Labels      []string `json:"labels"`
	BaseToken   struct {
		Address string `json:"address"`
		Symbol  string `json:"symbol"`
	} `json:"baseToken"`
	QuoteToken struct {
		Address string `json:"address"`
		Symbol  string `json:"symbol"`
	} `json:"quoteToken"`
	PriceNative string `json:"priceNative"`
	Volume      struct {
		H24 float64 `json:"h24"`
	} `json:"volume"`
	Liquidity struct {
		USD float64 `json:"usd"`
	} `json:"liquidity"`
}

type coinGeckoPlatformCoin struct {
	ID        string            `json:"id"`
	Symbol    string            `json:"symbol"`
	Platforms map[string]string `json:"platforms"`
}

type discoveredPool struct {
	Address         string
	ProtocolVersion string
	Token0          string
	Token1          string
	Token0Symbol    string
	Token1Symbol    string
	Token0Price     decimal.Decimal
	Token1Price     decimal.Decimal
	Fee             int
	TVLUSD          decimal.Decimal
	Volume24hUSD    decimal.Decimal
	BlockNumber     int64
	BlockTimestamp  time.Time
}

type ammRoute struct {
	Asset    database.AssetRepresentation
	Tokens   []database.AssetRepresentation
	Pools    []discoveredPool
	RouteKey string
}

type AMMAdapter struct {
	db              *database.DB
	reporter        *marketdata.ProviderReporter
	config          ammProviderConfig
	httpClient      *http.Client
	rpcClient       *ethclient.Client
	cancel          context.CancelFunc
	stopped         atomic.Bool
	mu              sync.RWMutex
	routes          []ammRoute
	rpcRetryMin     time.Duration
	rpcRetryMax     time.Duration
	connectionReady func(context.Context)
}

func NewAMMAdapter(db *database.DB, cfg ammProviderConfig) *AMMAdapter {
	return &AMMAdapter{
		db: db, reporter: marketdata.NewProviderReporter(db.ProviderStatus),
		config: cfg, httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *AMMAdapter) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel
	if a.config.RPCURL == "" ||
		(a.config.SubgraphURL == "" && !a.config.PublicDiscovery) {
		now := time.Now().UTC()
		err := fmt.Errorf("DEX provider is unconfigured")
		a.reporter.Attempt(a.config.Provider, "route-quotes", now)
		a.reporter.Failure(a.config.Provider, "route-quotes", now, err, 0)
		log.Warn("AMM adapter disabled because read-only endpoints are not configured",
			"provider", a.config.Provider)
		return
	}
	go a.superviseConnection(ctx)
}

func (a *AMMAdapter) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Lock()
	if a.rpcClient != nil {
		a.rpcClient.Close()
		a.rpcClient = nil
	}
	a.mu.Unlock()
	a.stopped.Store(true)
}

func (a *AMMAdapter) superviseConnection(ctx context.Context) {
	backoff := a.rpcRetryMin
	if backoff <= 0 {
		backoff = 30 * time.Second
	}
	maxBackoff := a.rpcRetryMax
	if maxBackoff <= 0 {
		maxBackoff = 10 * time.Minute
	}
	for ctx.Err() == nil {
		now := time.Now().UTC()
		a.reporter.Attempt(a.config.Provider, "rpc-session", now)
		client, err := ethclient.DialContext(ctx, a.config.RPCURL)
		if err == nil {
			var chainID *big.Int
			chainID, err = client.ChainID(ctx)
			if err == nil && chainID.Int64() != a.config.ChainID {
				err = fmt.Errorf("RPC chain identity mismatch")
			}
		}
		if err == nil {
			a.mu.Lock()
			a.rpcClient = client
			a.mu.Unlock()
			a.reporter.Success(a.config.Provider, "rpc-session", time.Now().UTC(), nil)
			if a.connectionReady != nil {
				a.connectionReady(ctx)
			} else {
				go a.superviseDiscovery(ctx)
				go a.runQuoteLoop(ctx)
			}
			return
		}
		if client != nil {
			client.Close()
		}
		safeErr := fmt.Errorf("RPC connection or chain validation failed")
		a.reporter.Failure(a.config.Provider, "rpc-session", time.Now().UTC(), safeErr, 0)
		a.reporter.NextRetry(a.config.Provider, "rpc-session", time.Now().UTC().Add(backoff))
		log.Warn("AMM RPC session unavailable",
			"provider", a.config.Provider, "retry_in", backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (a *AMMAdapter) currentRPC() *ethclient.Client {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.rpcClient
}

func (a *AMMAdapter) superviseDiscovery(ctx context.Context) {
	backoff := 30 * time.Second
	for ctx.Err() == nil {
		err := a.discover(ctx)
		wait := dexDiscoveryInterval
		if err != nil {
			a.reporter.Failure(a.config.Provider, "pool-catalog", time.Now().UTC(), err, 0)
			log.Warn("AMM pool discovery failed",
				"provider", a.config.Provider, "retry_in", backoff)
			wait = backoff
			a.reporter.NextRetry(
				a.config.Provider, "pool-catalog", time.Now().UTC().Add(wait),
			)
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

func (a *AMMAdapter) discover(ctx context.Context) error {
	now := time.Now().UTC()
	a.reporter.Attempt(a.config.Provider, "pool-catalog", now)
	if err := a.refreshProviderMappedRepresentations(ctx); err != nil {
		return fmt.Errorf("refresh CoinGecko chain representations failed")
	}
	representations, err := a.db.MarketAggregation.QueryApprovedAssetRepresentations(a.config.ChainID)
	if err != nil {
		return fmt.Errorf("query approved representations failed")
	}
	byAddress := make(map[string]database.AssetRepresentation, len(representations))
	verifiedRepresentations := make([]database.AssetRepresentation, 0, len(representations))
	verificationResults := make(chan database.AssetRepresentation, len(representations))
	verificationSemaphore := make(chan struct{}, 8)
	var verificationWait sync.WaitGroup
	for _, representation := range representations {
		representation := representation
		verificationWait.Add(1)
		go func() {
			defer verificationWait.Done()
			select {
			case verificationSemaphore <- struct{}{}:
				defer func() { <-verificationSemaphore }()
			case <-ctx.Done():
				return
			}
			if err := a.verifyRepresentation(ctx, representation); err != nil {
				if isDexIdentityError(err) {
					log.Warn("reviewed token representation skipped after deterministic identity mismatch",
						"provider", a.config.Provider,
						"asset_id", representation.AssetGuid)
					return
				}
				// A reviewed representation is durable identity data. A public
				// RPC timeout must degrade this refresh, not erase the token
				// and every route that depends on it.
				log.Warn("reviewed token representation retained after transient validation failure",
					"provider", a.config.Provider,
					"asset_id", representation.AssetGuid)
			}
			verificationResults <- representation
		}()
	}
	verificationWait.Wait()
	close(verificationResults)
	for representation := range verificationResults {
		byAddress[strings.ToLower(representation.ContractAddress)] = representation
		verifiedRepresentations = append(verifiedRepresentations, representation)
	}
	sort.Slice(verifiedRepresentations, func(i, j int) bool {
		if verifiedRepresentations[i].AssetGuid != verifiedRepresentations[j].AssetGuid {
			return verifiedRepresentations[i].AssetGuid < verifiedRepresentations[j].AssetGuid
		}
		return verifiedRepresentations[i].ContractAddress < verifiedRepresentations[j].ContractAddress
	})
	if _, ok := byAddress[a.config.StableAddress]; !ok {
		return fmt.Errorf("reviewed stable-token representation is missing")
	}
	log.Info("AMM representation verification completed",
		"provider", a.config.Provider,
		"verified_assets", len(verifiedRepresentations))
	poolsByAddress := make(map[string]discoveredPool)
	type tokenDiscoveryResult struct {
		token string
		pools []discoveredPool
		err   error
	}
	discoveryResults := make(chan tokenDiscoveryResult, len(byAddress))
	discoverySemaphore := make(chan struct{}, 4)
	var discoveryWait sync.WaitGroup
	for address := range byAddress {
		// Public discovery only needs asset/stable and asset/bridge edges. Every
		// asset is queried directly, while the bridge query supplies the final
		// bridge/stable edge; querying the stable token would inspect hundreds
		// of unrelated pools and exhaust free RPC capacity.
		if a.config.PublicDiscovery && address == a.config.StableAddress {
			continue
		}
		address := address
		discoveryWait.Add(1)
		go func() {
			defer discoveryWait.Done()
			select {
			case discoverySemaphore <- struct{}{}:
				defer func() { <-discoverySemaphore }()
			case <-ctx.Done():
				return
			}
			pools, err := a.discoverPoolsForToken(ctx, address)
			discoveryResults <- tokenDiscoveryResult{token: address, pools: pools, err: err}
		}()
	}
	discoveryWait.Wait()
	close(discoveryResults)
	discoveryFailures := 0
	deferredTokens := make(map[string]struct{})
	for result := range discoveryResults {
		if result.err != nil {
			discoveryFailures++
			deferredTokens[result.token] = struct{}{}
			log.Warn("AMM token pool discovery skipped",
				"provider", a.config.Provider, "error", result.err)
			continue
		}
		for _, pool := range result.pools {
			poolsByAddress[pool.Address] = pool
		}
	}
	pools := make([]discoveredPool, 0, len(poolsByAddress))
	for _, pool := range poolsByAddress {
		pools = append(pools, pool)
	}
	if len(pools) == 0 && discoveryFailures > 0 {
		return fmt.Errorf("all AMM token pool discovery requests failed")
	}
	log.Info("AMM token pool discovery completed",
		"provider", a.config.Provider,
		"verified_pools", len(pools),
		"failed_tokens", discoveryFailures)
	sort.Slice(pools, func(i, j int) bool { return pools[i].TVLUSD.GreaterThan(pools[j].TVLUSD) })

	candidates := make([]database.DexPoolCandidate, 0, len(pools))
	resolved := make([]discoveredPool, 0, len(pools))
	for _, pool := range pools {
		status := "resolved"
		var reason *string
		if err := a.verifyPool(ctx, pool); err != nil {
			if isDexIdentityError(err) {
				status = "rejected"
				reason = textPtr("onchain_pool_identity_mismatch")
			} else {
				status = "discovered"
				reason = textPtr("onchain_validation_temporarily_unavailable")
				deferredTokens[pool.Token0] = struct{}{}
				deferredTokens[pool.Token1] = struct{}{}
			}
		}
		_, token0Reviewed := byAddress[pool.Token0]
		_, token1Reviewed := byAddress[pool.Token1]
		quoteEligible := status == "resolved" &&
			token0Reviewed && token1Reviewed &&
			!pool.TVLUSD.LessThan(decimal.NewFromInt(dexMinimumTVLUSD)) &&
			!pool.Volume24hUSD.LessThan(decimal.NewFromInt(dexMinimumVolumeUSD))
		if quoteEligible {
			resolved = append(resolved, pool)
		}
		raw, _ := json.Marshal(pool)
		blockNumber := pool.BlockNumber
		blockTimestamp := pool.BlockTimestamp
		tvl := pool.TVLUSD.String()
		volume := pool.Volume24hUSD.String()
		candidates = append(candidates, database.DexPoolCandidate{
			Provider: a.config.Provider, ChainID: a.config.ChainID,
			ProtocolVersion: pool.ProtocolVersion, PoolAddress: pool.Address,
			Token0Address: pool.Token0, Token1Address: pool.Token1, FeeTier: pool.Fee,
			ResolutionStatus: status, RejectionReason: reason,
			QuoteEligible: quoteEligible,
			TVLUSD:        &tvl, Volume24hUSD: &volume,
			BlockNumber: &blockNumber, BlockTimestamp: &blockTimestamp,
			FirstSeenAt: now, LastSeenAt: now, RawMetadata: raw,
		})
	}
	if err := a.db.MarketAggregation.UpsertDexPoolCandidates(candidates); err != nil {
		return fmt.Errorf("store DEX pool candidates failed")
	}
	if _, err := a.db.MarketAggregation.EnsureProviderAssetSelection(
		a.config.Provider, 50, "dex-listed-asset-refresh",
	); err != nil {
		return fmt.Errorf("refresh DEX provider selection failed: %w", err)
	}
	routes := buildAMMRoutes(a.config, verifiedRepresentations, resolved)
	a.mu.Lock()
	routes = mergeDeferredAMMRoutes(routes, a.routes, deferredTokens)
	a.routes = routes
	a.mu.Unlock()
	sourceTime := newestPoolTime(resolved)
	a.reporter.Success(a.config.Provider, "pool-catalog", time.Now().UTC(), sourceTime)
	log.Info("AMM pool catalog refreshed",
		"provider", a.config.Provider, "reviewed_pools", len(pools),
		"resolved_pools", len(resolved), "routes", len(routes))
	return nil
}

func (a *AMMAdapter) refreshProviderMappedRepresentations(ctx context.Context) error {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://api.coingecko.com/api/v3/coins/list?include_platform=true",
		nil,
	)
	if err != nil {
		return err
	}
	response, err := a.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &providerHTTPError{host: "coingecko-platforms", status: response.StatusCode}
	}
	var coins []coinGeckoPlatformCoin
	if err := json.NewDecoder(response.Body).Decode(&coins); err != nil {
		return err
	}
	platform := "ethereum"
	if a.config.ChainID == 56 {
		platform = "binance-smart-chain"
	}
	coinByID := make(map[string]coinGeckoPlatformCoin, len(coins))
	for _, coin := range coins {
		coinByID[coin.ID] = coin
	}
	topAssets, err := a.db.MarketAggregation.QueryTopAssetIDs(200)
	if err != nil {
		return err
	}
	mappings, err := a.db.ExchangeSymbol.QueryAssetExternalMappings("coingecko")
	if err != nil {
		return err
	}
	assets, err := a.db.Asset.QueryAssets()
	if err != nil {
		return err
	}
	symbolByAsset := make(map[string]string, len(assets))
	for _, asset := range assets {
		symbolByAsset[asset.Guid] = strings.ToUpper(strings.TrimSpace(asset.AssetSymbol))
	}
	existing, err := a.db.MarketAggregation.QueryApprovedAssetRepresentations(a.config.ChainID)
	if err != nil {
		return err
	}
	ownerByAddress := make(map[string]string, len(existing))
	for _, representation := range existing {
		ownerByAddress[strings.ToLower(representation.ContractAddress)] = representation.AssetGuid
	}
	sort.Slice(mappings, func(i, j int) bool {
		return mappings[i].ExternalID < mappings[j].ExternalID
	})
	now := time.Now().UTC()
	rows := make([]database.AssetRepresentation, 0, len(mappings))
	for _, mapping := range mappings {
		if _, inside := topAssets[mapping.AssetGuid]; !inside {
			continue
		}
		coin, exists := coinByID[mapping.ExternalID]
		if !exists {
			continue
		}
		address := strings.ToLower(strings.TrimSpace(coin.Platforms[platform]))
		if !common.IsHexAddress(address) {
			continue
		}
		if owner, occupied := ownerByAddress[address]; occupied {
			if owner != mapping.AssetGuid {
				log.Warn("CoinGecko chain representation identity conflict skipped",
					"provider", a.config.Provider, "asset_id", mapping.AssetGuid)
			}
			continue
		}
		decimals, decimalsErr := a.readTokenDecimals(ctx, address)
		if decimalsErr != nil || decimals < 0 || decimals > 36 {
			continue
		}
		tokenSymbol := symbolByAsset[mapping.AssetGuid]
		if tokenSymbol == "" {
			tokenSymbol = strings.ToUpper(strings.TrimSpace(coin.Symbol))
		}
		source := "https://api.coingecko.com/api/v3/coins/list?include_platform=true"
		note := "CoinGecko asset-id platform mapping, admitted after on-chain decimals verification; not a claim that this is the canonical issuance contract."
		rows = append(rows, database.AssetRepresentation{
			AssetGuid: mapping.AssetGuid, ChainID: a.config.ChainID,
			ContractAddress: address, RepresentationKind: "provider_mapped",
			TokenSymbol: tokenSymbol, Decimals: decimals,
			ReviewStatus: "approved", ReviewSource: &source, ReviewNote: &note,
			ReviewedAt: &now, CreatedAt: now, UpdatedAt: now,
		})
		ownerByAddress[address] = mapping.AssetGuid
	}
	if err := a.db.MarketAggregation.UpsertAssetRepresentations(rows); err != nil {
		return err
	}
	if len(rows) > 0 {
		log.Info("CoinGecko chain representations verified",
			"provider", a.config.Provider, "verified", len(rows))
	}
	return nil
}

func (a *AMMAdapter) readTokenDecimals(ctx context.Context, address string) (int, error) {
	values, err := a.callView(ctx, address, erc20MetadataABI, "decimals")
	if err != nil {
		return 0, fmt.Errorf("token decimals call failed: %w", err)
	}
	if len(values) != 1 {
		return 0, &dexIdentityError{message: "token decimals returned an invalid response"}
	}
	switch value := values[0].(type) {
	case uint8:
		return int(value), nil
	case *big.Int:
		return int(value.Int64()), nil
	default:
		return 0, &dexIdentityError{message: "token decimals returned unexpected type"}
	}
}

func (a *AMMAdapter) verifyRepresentation(
	ctx context.Context,
	representation database.AssetRepresentation,
) error {
	decimals, err := a.readTokenDecimals(ctx, representation.ContractAddress)
	if err != nil {
		return err
	}
	if decimals != representation.Decimals {
		return &dexIdentityError{message: "token decimals mismatch"}
	}
	return nil
}

func (a *AMMAdapter) discoverPoolsForToken(ctx context.Context, token string) ([]discoveredPool, error) {
	if a.config.PublicDiscovery {
		return a.discoverPublicPoolsForToken(ctx, token)
	}
	const query = `query Pools($token: String!) {
	  pools0: pools(first: 12, orderBy: totalValueLockedUSD, orderDirection: desc, where: {token0: $token}) {
	    id feeTier totalValueLockedUSD token0Price token1Price
	    token0 { id symbol decimals } token1 { id symbol decimals }
	    poolDayData(first: 1, orderBy: date, orderDirection: desc) { volumeUSD }
	  }
	  pools1: pools(first: 12, orderBy: totalValueLockedUSD, orderDirection: desc, where: {token1: $token}) {
	    id feeTier totalValueLockedUSD token0Price token1Price
	    token0 { id symbol decimals } token1 { id symbol decimals }
	    poolDayData(first: 1, orderBy: date, orderDirection: desc) { volumeUSD }
	  }
	  _meta { block { number } }
	}`
	body, _ := json.Marshal(map[string]interface{}{
		"query": query, "variables": map[string]string{"token": strings.ToLower(token)},
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config.SubgraphURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build subgraph request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("subgraph request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &providerHTTPError{host: "subgraph", status: response.StatusCode}
	}
	var envelope graphEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode subgraph response failed")
	}
	if len(envelope.Errors) > 0 {
		return nil, fmt.Errorf("subgraph returned GraphQL errors")
	}
	client := a.currentRPC()
	if client == nil {
		return nil, fmt.Errorf("RPC session unavailable")
	}
	header, err := client.HeaderByNumber(ctx, big.NewInt(envelope.Data.Meta.Block.Number))
	if err != nil {
		return nil, fmt.Errorf("load subgraph block header failed")
	}
	blockTime := time.Unix(int64(header.Time), 0).UTC()
	result := make([]discoveredPool, 0, len(envelope.Data.Pools0)+len(envelope.Data.Pools1))
	for _, row := range append(envelope.Data.Pools0, envelope.Data.Pools1...) {
		pool, ok := normalizeGraphPool(row, envelope.Data.Meta.Block.Number, blockTime)
		if ok {
			result = append(result, pool)
		}
	}
	return result, nil
}

func (a *AMMAdapter) discoverPublicPoolsForToken(
	ctx context.Context,
	token string,
) ([]discoveredPool, error) {
	chain := "ethereum"
	if a.config.ChainID == 56 {
		chain = "bsc"
	}
	endpoint := fmt.Sprintf(
		"%s/%s/%s", publicDiscoveryBaseURL(a.config), chain,
		strings.ToLower(token),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build public discovery request failed")
	}
	response, err := a.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("public discovery request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &providerHTTPError{
			host: "public-discovery", status: response.StatusCode,
		}
	}
	var pairs []dexScreenerPair
	if err := json.NewDecoder(response.Body).Decode(&pairs); err != nil {
		return nil, fmt.Errorf("decode public discovery response failed")
	}
	client := a.currentRPC()
	if client == nil {
		return nil, fmt.Errorf("RPC session unavailable")
	}
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil || header == nil {
		return nil, fmt.Errorf("load public discovery block header failed")
	}
	blockNumber := header.Number.Int64()
	blockTime := time.Unix(int64(header.Time), 0).UTC()
	requestedToken := strings.ToLower(token)
	candidates := make([]dexScreenerPair, 0, len(pairs))
	for _, pair := range pairs {
		protocol := dexPairProtocol(pair.Labels)
		if !strings.EqualFold(pair.ChainID, chain) ||
			!strings.EqualFold(pair.DexID, a.config.Provider) ||
			protocol == "" ||
			!common.IsHexAddress(pair.PairAddress) ||
			!common.IsHexAddress(pair.BaseToken.Address) ||
			!common.IsHexAddress(pair.QuoteToken.Address) {
			continue
		}
		baseAddress := strings.ToLower(pair.BaseToken.Address)
		quoteAddress := strings.ToLower(pair.QuoteToken.Address)
		if baseAddress != requestedToken && quoteAddress != requestedToken {
			continue
		}
		candidates = append(candidates, pair)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Liquidity.USD > candidates[j].Liquidity.USD
	})
	prioritized := make([]dexScreenerPair, 0, dexMaxRoutesPerAsset)
	seenPairs := make(map[string]struct{}, dexMaxRoutesPerAsset)
	addFirst := func(predicate func(dexScreenerPair) bool) {
		for _, pair := range candidates {
			address := strings.ToLower(pair.PairAddress)
			if _, exists := seenPairs[address]; exists || !predicate(pair) {
				continue
			}
			seenPairs[address] = struct{}{}
			prioritized = append(prioritized, pair)
			return
		}
	}
	counterpartIs := func(expected string) func(dexScreenerPair) bool {
		return func(pair dexScreenerPair) bool {
			base := strings.ToLower(pair.BaseToken.Address)
			quote := strings.ToLower(pair.QuoteToken.Address)
			counterpart := base
			if counterpart == requestedToken {
				counterpart = quote
			}
			return counterpart == expected
		}
	}
	for _, counterpart := range []string{
		a.config.StableAddress, a.config.BridgeAddress,
	} {
		for _, protocol := range []string{"v3", "v2"} {
			counterpart := counterpart
			protocol := protocol
			addFirst(func(pair dexScreenerPair) bool {
				return counterpartIs(counterpart)(pair) &&
					dexPairProtocol(pair.Labels) == protocol
			})
		}
	}
	for _, pair := range candidates {
		if len(prioritized) >= dexMaxRoutesPerAsset {
			break
		}
		address := strings.ToLower(pair.PairAddress)
		if _, exists := seenPairs[address]; exists {
			continue
		}
		seenPairs[address] = struct{}{}
		prioritized = append(prioritized, pair)
	}

	result := make([]discoveredPool, 0, len(prioritized))
	for _, pair := range prioritized {
		baseAddress := strings.ToLower(pair.BaseToken.Address)
		quoteAddress := strings.ToLower(pair.QuoteToken.Address)
		poolAddress := strings.ToLower(pair.PairAddress)
		protocol := dexPairProtocol(pair.Labels)
		fee := v2FeeTier(a.config.Provider)
		if protocol == "v3" {
			feeValue, feeErr := a.callUint(ctx, poolAddress, "fee")
			if feeErr != nil {
				continue
			}
			fee = int(feeValue.Int64())
		}
		nativePrice, parseErr := decimal.NewFromString(pair.PriceNative)
		if parseErr != nil || nativePrice.LessThanOrEqual(decimal.Zero) {
			continue
		}
		token0Address := baseAddress
		token1Address := quoteAddress
		if token0Address > token1Address {
			token0Address, token1Address = token1Address, token0Address
		}
		token0Price := decimal.Zero
		token1Price := decimal.Zero
		switch {
		case baseAddress == token0Address && quoteAddress == token1Address:
			token0Price = nativePrice
			token1Price = decimal.NewFromInt(1).Div(nativePrice)
		case baseAddress == token1Address && quoteAddress == token0Address:
			token0Price = decimal.NewFromInt(1).Div(nativePrice)
			token1Price = nativePrice
		default:
			continue
		}
		token0Symbol := pair.BaseToken.Symbol
		token1Symbol := pair.QuoteToken.Symbol
		if baseAddress == token1Address {
			token0Symbol, token1Symbol = token1Symbol, token0Symbol
		}
		result = append(result, discoveredPool{
			Address: poolAddress, ProtocolVersion: protocol,
			Token0: token0Address, Token1: token1Address,
			Token0Symbol: token0Symbol, Token1Symbol: token1Symbol,
			Token0Price: token0Price, Token1Price: token1Price,
			Fee:          fee,
			TVLUSD:       decimal.NewFromFloat(pair.Liquidity.USD),
			Volume24hUSD: decimal.NewFromFloat(pair.Volume.H24),
			BlockNumber:  blockNumber, BlockTimestamp: blockTime,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TVLUSD.GreaterThan(result[j].TVLUSD)
	})
	return result, nil
}

func publicDiscoveryBaseURL(config ammProviderConfig) string {
	if value := strings.TrimRight(strings.TrimSpace(config.DiscoveryURL), "/"); value != "" {
		return value
	}
	return dexScreenerAPIURL
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func dexPairProtocol(labels []string) string {
	switch {
	case containsFold(labels, "v3"):
		return "v3"
	case containsFold(labels, "v2"):
		return "v2"
	default:
		return ""
	}
}

func v2FeeTier(provider string) int {
	if strings.EqualFold(provider, "pancakeswap") {
		// Informational parts-per-million equivalent. The V2 router remains
		// authoritative for actual fee math.
		return 2500
	}
	return 3000
}

func normalizeGraphPool(row graphPool, blockNumber int64, blockTime time.Time) (discoveredPool, bool) {
	if !common.IsHexAddress(row.ID) || !common.IsHexAddress(row.Token0.ID) ||
		!common.IsHexAddress(row.Token1.ID) {
		return discoveredPool{}, false
	}
	fee, err := strconv.Atoi(row.FeeTier)
	if err != nil || fee <= 0 {
		return discoveredPool{}, false
	}
	tvl, err := decimal.NewFromString(row.TotalValueLockedUSD)
	if err != nil {
		return discoveredPool{}, false
	}
	token0Price, err := decimal.NewFromString(row.Token0Price)
	if err != nil || token0Price.LessThanOrEqual(decimal.Zero) {
		return discoveredPool{}, false
	}
	token1Price, err := decimal.NewFromString(row.Token1Price)
	if err != nil || token1Price.LessThanOrEqual(decimal.Zero) {
		return discoveredPool{}, false
	}
	volume := decimal.Zero
	if len(row.DayData) > 0 {
		if value, parseErr := decimal.NewFromString(row.DayData[0].VolumeUSD); parseErr == nil {
			volume = value
		}
	}
	return discoveredPool{
		Address: strings.ToLower(row.ID), ProtocolVersion: "v3",
		Token0: strings.ToLower(row.Token0.ID), Token1: strings.ToLower(row.Token1.ID),
		Token0Symbol: row.Token0.Symbol, Token1Symbol: row.Token1.Symbol,
		Token0Price: token0Price, Token1Price: token1Price, Fee: fee,
		TVLUSD: tvl, Volume24hUSD: volume,
		BlockNumber: blockNumber, BlockTimestamp: blockTime,
	}, true
}

func (a *AMMAdapter) verifyPool(ctx context.Context, pool discoveredPool) error {
	if pool.ProtocolVersion == "v2" {
		values, err := a.callView(
			ctx,
			a.config.V2FactoryAddress,
			v2FactoryABI,
			"getPair",
			common.HexToAddress(pool.Token0),
			common.HexToAddress(pool.Token1),
		)
		if err != nil || len(values) != 1 {
			if err != nil {
				return fmt.Errorf("V2 factory getPair failed: %w", err)
			}
			return &dexIdentityError{message: "V2 factory getPair returned an invalid response"}
		}
		address, ok := values[0].(common.Address)
		if !ok || !strings.EqualFold(address.Hex(), pool.Address) {
			return &dexIdentityError{message: "V2 factory pair identity mismatch"}
		}
		return nil
	}
	if pool.ProtocolVersion != "v3" {
		return &dexIdentityError{message: "unsupported AMM protocol"}
	}
	values, err := a.callView(
		ctx,
		a.config.V3FactoryAddress,
		v3FactoryABI,
		"getPool",
		common.HexToAddress(pool.Token0),
		common.HexToAddress(pool.Token1),
		big.NewInt(int64(pool.Fee)),
	)
	if err != nil || len(values) != 1 {
		if err != nil {
			return fmt.Errorf("factory getPool failed: %w", err)
		}
		return &dexIdentityError{message: "factory getPool returned an invalid response"}
	}
	address, ok := values[0].(common.Address)
	if !ok || !strings.EqualFold(address.Hex(), pool.Address) {
		return &dexIdentityError{message: "factory pool identity mismatch"}
	}
	return nil
}

func (a *AMMAdapter) callAddress(ctx context.Context, address, method string) (common.Address, error) {
	values, err := a.callView(ctx, address, poolIdentityABI, method)
	if err != nil || len(values) != 1 {
		return common.Address{}, fmt.Errorf("%s call failed", method)
	}
	value, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("%s returned unexpected type", method)
	}
	return value, nil
}

func (a *AMMAdapter) callUint(ctx context.Context, address, method string) (*big.Int, error) {
	values, err := a.callView(ctx, address, poolIdentityABI, method)
	if err != nil || len(values) != 1 {
		return nil, fmt.Errorf("%s call failed", method)
	}
	switch value := values[0].(type) {
	case *big.Int:
		return value, nil
	case uint32:
		return new(big.Int).SetUint64(uint64(value)), nil
	default:
		return nil, fmt.Errorf("%s returned unexpected type", method)
	}
}

func (a *AMMAdapter) callView(
	ctx context.Context,
	address string,
	contractABI abi.ABI,
	method string,
	args ...interface{},
) ([]interface{}, error) {
	input, err := contractABI.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	target := common.HexToAddress(address)
	client := a.currentRPC()
	if client == nil {
		return nil, fmt.Errorf("RPC session unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	output, err := client.CallContract(callCtx, ethereum.CallMsg{To: &target, Data: input}, nil)
	if err != nil {
		return nil, err
	}
	return contractABI.Unpack(method, output)
}

func buildAMMRoutes(
	cfg ammProviderConfig,
	representations []database.AssetRepresentation,
	pools []discoveredPool,
) []ammRoute {
	byAddress := make(map[string]database.AssetRepresentation, len(representations))
	for _, representation := range representations {
		byAddress[strings.ToLower(representation.ContractAddress)] = representation
	}
	stable, stableOK := byAddress[cfg.StableAddress]
	if !stableOK {
		return nil
	}
	poolsByPair := make(map[string][]discoveredPool)
	for _, pool := range pools {
		key := poolPairKey(pool.Token0, pool.Token1)
		poolsByPair[key] = append(poolsByPair[key], pool)
	}
	for key := range poolsByPair {
		sort.Slice(poolsByPair[key], func(i, j int) bool {
			return betterDiscoveredPool(poolsByPair[key][i], poolsByPair[key][j])
		})
	}
	sortedRepresentations := append([]database.AssetRepresentation(nil), representations...)
	sort.Slice(sortedRepresentations, func(i, j int) bool {
		if sortedRepresentations[i].AssetGuid != sortedRepresentations[j].AssetGuid {
			return sortedRepresentations[i].AssetGuid < sortedRepresentations[j].AssetGuid
		}
		return sortedRepresentations[i].ContractAddress <
			sortedRepresentations[j].ContractAddress
	})
	bridge, bridgeOK := byAddress[cfg.BridgeAddress]
	routes := make([]ammRoute, 0, len(representations)*2)
	for _, asset := range sortedRepresentations {
		address := strings.ToLower(asset.ContractAddress)
		if address == cfg.StableAddress {
			continue
		}
		assetRoutes := make([]ammRoute, 0, dexMaxRoutesPerAsset)
		seenRoutes := make(map[string]struct{})
		appendRoute := func(tokens []database.AssetRepresentation, routePools []discoveredPool) {
			if len(assetRoutes) >= dexMaxRoutesPerAsset {
				return
			}
			route := newAMMRoute(cfg.Provider, tokens, routePools)
			if _, exists := seenRoutes[route.RouteKey]; exists {
				return
			}
			seenRoutes[route.RouteKey] = struct{}{}
			assetRoutes = append(assetRoutes, route)
		}
		for _, direct := range poolsByPair[poolPairKey(address, cfg.StableAddress)] {
			appendRoute(
				[]database.AssetRepresentation{asset, stable},
				[]discoveredPool{direct},
			)
		}
		if address != cfg.BridgeAddress && bridgeOK {
			firstPools := poolsByPair[poolPairKey(address, cfg.BridgeAddress)]
			secondPools := poolsByPair[poolPairKey(
				cfg.BridgeAddress, cfg.StableAddress,
			)]
			for _, first := range firstPools {
				for _, second := range secondPools {
					appendRoute(
						[]database.AssetRepresentation{asset, bridge, stable},
						[]discoveredPool{first, second},
					)
				}
			}
		}
		routes = append(routes, assetRoutes...)
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Asset.AssetGuid != routes[j].Asset.AssetGuid {
			return routes[i].Asset.AssetGuid < routes[j].Asset.AssetGuid
		}
		return routes[i].RouteKey < routes[j].RouteKey
	})
	return routes
}

func mergeDeferredAMMRoutes(
	current []ammRoute,
	previous []ammRoute,
	deferredTokens map[string]struct{},
) []ammRoute {
	if len(previous) == 0 || len(deferredTokens) == 0 {
		return current
	}
	seen := make(map[string]struct{}, len(current)+len(previous))
	for _, route := range current {
		seen[route.RouteKey] = struct{}{}
	}
	for _, route := range previous {
		if _, exists := seen[route.RouteKey]; exists {
			continue
		}
		deferred := false
		for _, token := range route.Tokens {
			if _, affected := deferredTokens[strings.ToLower(token.ContractAddress)]; affected {
				deferred = true
				break
			}
		}
		if deferred {
			current = append(current, route)
			seen[route.RouteKey] = struct{}{}
		}
	}
	sort.Slice(current, func(i, j int) bool {
		if current[i].Asset.AssetGuid != current[j].Asset.AssetGuid {
			return current[i].Asset.AssetGuid < current[j].Asset.AssetGuid
		}
		return current[i].RouteKey < current[j].RouteKey
	})
	return current
}

func newAMMRoute(
	provider string,
	tokens []database.AssetRepresentation,
	pools []discoveredPool,
) ammRoute {
	hash := sha256.New()
	hash.Write([]byte(provider))
	for index, token := range tokens {
		hash.Write([]byte(strings.ToLower(token.ContractAddress)))
		if index < len(pools) {
			hash.Write([]byte(
				":" + pools[index].ProtocolVersion +
					":" + strings.ToLower(pools[index].Address) +
					fmt.Sprintf(":%d", pools[index].Fee),
			))
		}
	}
	return ammRoute{
		Asset: tokens[0], Tokens: tokens, Pools: pools,
		RouteKey: hex.EncodeToString(hash.Sum(nil))[:20],
	}
}

func betterDiscoveredPool(left, right discoveredPool) bool {
	if !left.TVLUSD.Equal(right.TVLUSD) {
		return left.TVLUSD.GreaterThan(right.TVLUSD)
	}
	if !left.Volume24hUSD.Equal(right.Volume24hUSD) {
		return left.Volume24hUSD.GreaterThan(right.Volume24hUSD)
	}
	if left.ProtocolVersion != right.ProtocolVersion {
		return left.ProtocolVersion < right.ProtocolVersion
	}
	if left.Fee != right.Fee {
		return left.Fee < right.Fee
	}
	return left.Address < right.Address
}

func (a *AMMAdapter) runQuoteLoop(ctx context.Context) {
	ticker := time.NewTicker(dexQuoteInterval)
	defer ticker.Stop()
	for {
		a.quoteAll(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (a *AMMAdapter) quoteAll(ctx context.Context) {
	a.mu.RLock()
	routes := append([]ammRoute(nil), a.routes...)
	a.mu.RUnlock()
	if len(routes) == 0 || a.currentRPC() == nil {
		return
	}
	now := time.Now().UTC()
	sourceKey := "route-quotes"
	if rollout, err := a.db.MarketAggregation.QueryProviderRollout(
		a.config.Provider,
	); err == nil && rollout != nil && rollout.LocalPreviewEnabled {
		sourceKey = "route-quotes-preview"
	}
	a.reporter.Attempt(a.config.Provider, sourceKey, now)
	semaphore := make(chan struct{}, 12)
	results := make(chan database.DexRouteCurrent, len(routes))
	var wait sync.WaitGroup
	for _, route := range routes {
		route := route
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			results <- a.quoteRoute(ctx, route, time.Now().UTC())
		}()
	}
	wait.Wait()
	close(results)
	completedAt := time.Now().UTC()

	rows := make([]database.DexRouteCurrent, 0, len(routes))
	observations := make([]database.DexQuoteObservation, 0, len(routes))
	latestSource := time.Time{}
	technicalQuoteCount := 0
	for row := range results {
		rows = append(rows, row)
		if row.BlockTimestamp != nil && row.BlockTimestamp.After(latestSource) {
			latestSource = *row.BlockTimestamp
		}
		if row.Available && row.PriceUSD != nil {
			observations = append(observations, database.DexQuoteObservation{
				Provider: row.Provider, AssetGuid: row.AssetGuid,
				RouteKey: row.RouteKey, ObservedAt: row.ObservedAt,
				PriceUSD: *row.PriceUSD, QuoteNotionalUSD: row.QuoteNotionalUSD,
				BlockNumber: row.BlockNumber,
			})
		}
		if row.PriceUSD != nil {
			technicalQuoteCount++
		}
	}
	if err := a.db.MarketAggregation.UpsertDexRoutes(rows); err != nil {
		a.reporter.Failure(a.config.Provider, sourceKey, now, fmt.Errorf("store DEX routes failed"), 0)
		a.reporter.NextRetry(a.config.Provider, sourceKey, now.Add(dexQuoteInterval))
		return
	}
	routeKeys := make([]string, 0, len(rows))
	for _, row := range rows {
		routeKeys = append(routeKeys, row.RouteKey)
	}
	if err := a.db.MarketAggregation.MarkUnselectedDexRoutesUnavailable(
		a.config.Provider, routeKeys, completedAt,
	); err != nil {
		a.reporter.Failure(
			a.config.Provider, sourceKey, completedAt,
			fmt.Errorf("deactivate unselected DEX routes failed"), 0,
		)
		a.reporter.NextRetry(
			a.config.Provider, sourceKey, completedAt.Add(dexQuoteInterval),
		)
		return
	}
	if _, err := a.db.MarketAggregation.EnsureProviderAssetSelection(
		a.config.Provider, 50, "dex-route-refresh",
	); err != nil {
		a.reporter.Failure(
			a.config.Provider, sourceKey, completedAt,
			fmt.Errorf("refresh DEX provider selection failed"), 0,
		)
		a.reporter.NextRetry(a.config.Provider, sourceKey, completedAt.Add(dexQuoteInterval))
		return
	}
	if err := a.db.MarketAggregation.InsertDexQuoteObservations(observations); err != nil {
		a.reporter.Failure(a.config.Provider, sourceKey, completedAt, fmt.Errorf("store DEX quote history failed"), 0)
		a.reporter.NextRetry(a.config.Provider, sourceKey, completedAt.Add(dexQuoteInterval))
		return
	}
	_ = a.db.MarketAggregation.PruneDexQuoteObservations(
		completedAt.Add(-dexObservationRetention),
	)
	if err := a.publishVenueSnapshots(rows, completedAt); err != nil {
		a.reporter.Failure(a.config.Provider, sourceKey, completedAt, fmt.Errorf("publish DEX venue snapshots failed"), 0)
		a.reporter.NextRetry(a.config.Provider, sourceKey, completedAt.Add(dexQuoteInterval))
		return
	}
	if technicalQuoteCount == 0 {
		err := fmt.Errorf("no DEX route produced a technical quote")
		a.reporter.Failure(a.config.Provider, sourceKey, completedAt, err, 0)
		a.reporter.NextRetry(a.config.Provider, sourceKey, completedAt.Add(dexQuoteInterval))
		return
	}
	var sourceTime *time.Time
	if !latestSource.IsZero() {
		sourceTime = &latestSource
	}
	a.reporter.Success(a.config.Provider, sourceKey, time.Now().UTC(), sourceTime)
}

func (a *AMMAdapter) quoteRoute(ctx context.Context, route ammRoute, now time.Time) database.DexRouteCurrent {
	pathSymbols := make([]string, len(route.Tokens))
	poolAddresses := make([]string, len(route.Pools))
	protocolVersions := make([]string, len(route.Pools))
	for index := range route.Tokens {
		pathSymbols[index] = route.Tokens[index].TokenSymbol
	}
	for index := range route.Pools {
		poolAddresses[index] = route.Pools[index].Address
		protocolVersions[index] = route.Pools[index].ProtocolVersion
	}
	pathJSON, _ := json.Marshal(pathSymbols)
	poolJSON, _ := json.Marshal(poolAddresses)
	protocolJSON, _ := json.Marshal(protocolVersions)
	row := database.DexRouteCurrent{
		Provider: a.config.Provider, AssetGuid: route.Asset.AssetGuid,
		RouteKey: route.RouteKey, ChainID: a.config.ChainID,
		QuoteNotionalUSD:   strconv.FormatInt(dexQuoteNotionalLadderUSD[0], 10),
		QuoteReferenceKind: "none",
		Quality:            "unknown", Available: false, Path: pathJSON,
		PoolAddresses: poolJSON, ProtocolVersions: protocolJSON,
		ObservedAt: now,
	}
	client := a.currentRPC()
	if client == nil {
		row.UnavailableReason = textPtr("rpc_session_unavailable")
		return row
	}
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		row.UnavailableReason = textPtr("rpc_header_unavailable")
		return row
	}
	blockTime := time.Unix(int64(header.Time), 0).UTC()
	blockNumber := header.Number.Int64()
	row.BlockTimestamp = &blockTime
	row.BlockNumber = &blockNumber
	if now.Sub(blockTime) > time.Minute || blockTime.After(now.Add(5*time.Second)) {
		row.UnavailableReason = textPtr("block_stale")
		return row
	}

	reference := a.freshCompositeReference(route.Asset.AssetGuid, now)
	for _, quoteNotionalUSD := range dexQuoteNotionalLadderUSD {
		row = a.quoteRouteAtNotional(
			ctx, route, row, quoteNotionalUSD, reference,
		)
		if row.Available || !retryableDexQuoteFailure(row.UnavailableReason) {
			break
		}
	}
	if !row.Available {
		return row
	}

	windowStart := now.Add(-24 * time.Hour)
	coverage, err := a.db.MarketAggregation.QueryDexWindowCoverage(
		a.config.Provider, route.Asset.AssetGuid, route.RouteKey,
		row.QuoteNotionalUSD, windowStart, now,
	)
	if err == nil && dexCoverageSufficient(coverage, windowStart, now) {
		openPrice, parseErr := decimal.NewFromString(coverage.OpenPriceUSD)
		currentPrice, currentErr := decimal.NewFromString(*row.PriceUSD)
		if parseErr == nil && currentErr == nil && openPrice.GreaterThan(decimal.Zero) {
			row.Change24hPct = decimalText(
				currentPrice.Sub(openPrice).Div(openPrice).Mul(decimal.NewFromInt(100)),
			)
		}
	}
	return row
}

func (a *AMMAdapter) quoteRouteAtNotional(
	ctx context.Context,
	route ammRoute,
	row database.DexRouteCurrent,
	quoteNotionalUSD int64,
	reference *decimal.Decimal,
) database.DexRouteCurrent {
	row.QuoteNotionalUSD = strconv.FormatInt(quoteNotionalUSD, 10)
	row.QuoteReferenceKind = "none"
	row.Quality = "unknown"
	row.Available = false
	row.UnavailableReason = nil
	row.PriceUSD = nil
	row.BuyPriceUSD = nil
	row.SellPriceUSD = nil
	row.PriceImpactPct = nil
	row.RoundTripSpreadPct = nil
	stable := route.Tokens[len(route.Tokens)-1]
	amountStable := decimal.NewFromInt(quoteNotionalUSD).Shift(int32(stable.Decimals)).BigInt()
	reverseTokens := reverseRepresentations(route.Tokens)
	reversePools := reversePools(route.Pools)
	assetAmountRaw, err := a.quoteExactInput(
		ctx, reverseTokens, reversePools, amountStable,
	)
	if err != nil || assetAmountRaw.Sign() <= 0 {
		row.UnavailableReason = textPtr("buy_quote_failed")
		return row
	}
	stableBackRaw, err := a.quoteExactInput(
		ctx, route.Tokens, route.Pools, assetAmountRaw,
	)
	if err != nil || stableBackRaw.Sign() <= 0 {
		row.UnavailableReason = textPtr("sell_quote_failed")
		return row
	}
	assetAmount := decimal.NewFromBigInt(assetAmountRaw, int32(-route.Asset.Decimals))
	stableBack := decimal.NewFromBigInt(stableBackRaw, int32(-stable.Decimals))
	if assetAmount.LessThanOrEqual(decimal.Zero) {
		row.UnavailableReason = textPtr("non_positive_asset_quote")
		return row
	}
	notional := decimal.NewFromInt(quoteNotionalUSD)
	buyPrice := notional.Div(assetAmount)
	sellPrice := stableBack.Div(assetAmount)
	mid, spread, impact := dexQuotePriceMetrics(buyPrice, sellPrice)
	tvl, volume := routeLiquidity(route.Pools)
	row.PriceUSD = decimalText(mid)
	row.BuyPriceUSD = decimalText(buyPrice)
	row.SellPriceUSD = decimalText(sellPrice)
	row.TVLUSD = decimalText(tvl)
	row.Turnover24hUSD = decimalText(volume)
	row.PriceImpactPct = decimalText(impact)
	row.RoundTripSpreadPct = decimalText(spread)
	row.QuoteReferenceKind = "onchain_only"

	var divergence *decimal.Decimal
	if reference != nil {
		value := mid.Sub(*reference).Abs().Div(*reference).Mul(decimal.NewFromInt(100))
		divergence = &value
		row.QuoteReferenceKind = "cex_correlated"
	}
	row.Available, row.Quality, row.UnavailableReason = assessDexQuote(
		impact, spread, divergence, quoteNotionalUSD,
		len(route.Pools), tvl, volume,
	)
	if row.Available {
		row.UnavailableReason = nil
	}
	return row
}

func dexQuotePriceMetrics(
	buyPrice, sellPrice decimal.Decimal,
) (decimal.Decimal, decimal.Decimal, decimal.Decimal) {
	mid := buyPrice.Add(sellPrice).Div(decimal.NewFromInt(2))
	spread := buyPrice.Sub(sellPrice).Abs().Div(mid).Mul(decimal.NewFromInt(100))
	buyImpact := buyPrice.Sub(mid).Abs().Div(mid)
	sellImpact := sellPrice.Sub(mid).Abs().Div(mid)
	impact := decimal.Max(buyImpact, sellImpact).Mul(decimal.NewFromInt(100))
	return mid, spread, impact
}

func (a *AMMAdapter) freshCompositeReference(assetID string, now time.Time) *decimal.Decimal {
	index, err := a.db.MarketAggregation.QueryAssetPriceIndex(assetID)
	if err != nil || index == nil || !index.Available || index.PriceUSD == nil ||
		now.Sub(index.ObservedAt) > marketdata.CompositeFreshnessLimit {
		return nil
	}
	reference, err := decimal.NewFromString(*index.PriceUSD)
	if err != nil || reference.LessThanOrEqual(decimal.Zero) {
		return nil
	}
	return &reference
}

func retryableDexQuoteFailure(reason *string) bool {
	if reason == nil {
		return false
	}
	switch *reason {
	case "buy_quote_failed", "sell_quote_failed",
		"price_impact_gt_1pct", "round_trip_spread_gt_2pct",
		"cex_divergence_gt_3pct":
		return true
	default:
		return false
	}
}

func dexCoverageSufficient(
	coverage *database.DexWindowCoverage,
	windowStart, now time.Time,
) bool {
	if coverage == nil || coverage.ObservationCount == 0 {
		return false
	}
	// quoteRoute calls this only after obtaining a fresh current quote. That
	// quote is inserted after the route batch completes, so it is not yet part
	// of the database window. Treat it as the endpoint and verify that the gap
	// from the latest persisted sample stays inside the historical sampling
	// SLA. Public route freshness remains independently capped at 30 seconds.
	endpointGap := now.Sub(coverage.LastObservedAt)
	if endpointGap < 0 {
		return false
	}
	return !coverage.FirstObservedAt.After(windowStart.Add(30*time.Minute)) &&
		endpointGap <= dexHistoricalObservationMaxGap &&
		coverage.MaxGap <= dexHistoricalObservationMaxGap
}

func assessDexQuote(
	impact, spread decimal.Decimal,
	divergence *decimal.Decimal,
	quoteNotionalUSD int64,
	poolCount int,
	tvl, volume decimal.Decimal,
) (bool, string, *string) {
	switch {
	case impact.GreaterThan(decimal.NewFromInt(1)):
		return false, "unknown", textPtr("price_impact_gt_1pct")
	case spread.GreaterThan(decimal.NewFromInt(2)):
		return false, "unknown", textPtr("round_trip_spread_gt_2pct")
	case divergence != nil && divergence.GreaterThan(decimal.NewFromInt(3)):
		return false, "unknown", textPtr("cex_divergence_gt_3pct")
	}
	quality := "low"
	if quoteNotionalUSD >= 10_000 {
		quality = "medium"
	}
	if divergence != nil && quoteNotionalUSD >= 10_000 && poolCount == 1 &&
		tvl.GreaterThanOrEqual(decimal.NewFromInt(10_000_000)) &&
		volume.GreaterThanOrEqual(decimal.NewFromInt(1_000_000)) &&
		impact.LessThanOrEqual(decimal.NewFromFloat(0.5)) &&
		spread.LessThanOrEqual(decimal.NewFromInt(1)) {
		quality = "high"
	}
	return true, quality, nil
}

func (a *AMMAdapter) quoteExactInput(
	ctx context.Context,
	tokens []database.AssetRepresentation,
	pools []discoveredPool,
	amountIn *big.Int,
) (*big.Int, error) {
	if len(tokens) < 2 || len(pools) != len(tokens)-1 {
		return nil, fmt.Errorf("invalid AMM route shape")
	}
	amount := new(big.Int).Set(amountIn)
	for index, pool := range pools {
		var err error
		amount, err = a.quotePoolExactInput(
			ctx, pool, tokens[index], tokens[index+1], amount,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%s hop %d quote failed: %w",
				pool.ProtocolVersion, index+1, err,
			)
		}
	}
	return amount, nil
}

func (a *AMMAdapter) quotePoolExactInput(
	ctx context.Context,
	pool discoveredPool,
	tokenIn, tokenOut database.AssetRepresentation,
	amountIn *big.Int,
) (*big.Int, error) {
	if pool.ProtocolVersion == "v2" {
		values, err := a.callView(
			ctx, a.config.V2RouterAddress, v2RouterABI, "getAmountsOut",
			amountIn,
			[]common.Address{
				common.HexToAddress(tokenIn.ContractAddress),
				common.HexToAddress(tokenOut.ContractAddress),
			},
		)
		if err != nil || len(values) != 1 {
			return nil, fmt.Errorf("V2 router call failed")
		}
		amounts, ok := values[0].([]*big.Int)
		if !ok || len(amounts) != 2 || amounts[1] == nil {
			return nil, fmt.Errorf("V2 router returned unexpected amounts")
		}
		return amounts[1], nil
	}
	if pool.ProtocolVersion != "v3" {
		return nil, fmt.Errorf("unsupported AMM protocol")
	}
	path, err := encodeV3Path(
		[]database.AssetRepresentation{tokenIn, tokenOut},
		[]int{pool.Fee},
	)
	if err != nil {
		return nil, err
	}
	values, err := a.callView(
		ctx, a.config.V3QuoterAddress, quoterV2ABI,
		"quoteExactInput", path, amountIn,
	)
	if err != nil || len(values) == 0 {
		return nil, fmt.Errorf("quoter call failed")
	}
	value, ok := values[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("quoter returned unexpected amount type")
	}
	return value, nil
}

func (a *AMMAdapter) publishVenueSnapshots(rows []database.DexRouteCurrent, now time.Time) error {
	allowed, rollout, err := a.db.MarketAggregation.QueryPublishedAssetIDs(a.config.Provider)
	if err != nil {
		return err
	}
	mode := "shadow"
	if rollout != nil {
		mode = rollout.Mode
	}
	best := make(map[string]database.DexRouteCurrent)
	routeCounts := make(map[string]int)
	for _, row := range rows {
		routeCounts[row.AssetGuid]++
		current, exists := best[row.AssetGuid]
		if !exists || betterDexRoute(row, current) {
			best[row.AssetGuid] = row
		}
	}
	snapshots := make([]database.AssetVenueSnapshot, 0, len(allowed))
	for assetID := range allowed {
		row, hasRoute := best[assetID]
		available := hasRoute && row.Available
		unavailableReason := "no_current_route"
		if hasRoute && row.UnavailableReason != nil &&
			strings.TrimSpace(*row.UnavailableReason) != "" {
			unavailableReason = strings.TrimSpace(*row.UnavailableReason)
		}
		poolAddresses := json.RawMessage([]byte(`[]`))
		protocolVersions := json.RawMessage([]byte(`[]`))
		routeKey := ""
		quoteNotional := strconv.FormatInt(dexQuoteNotionalLadderUSD[0], 10)
		quoteReferenceKind := "none"
		quality := "unknown"
		var sourceTime *time.Time
		if hasRoute {
			poolAddresses = json.RawMessage(row.PoolAddresses)
			protocolVersions = json.RawMessage(row.ProtocolVersions)
			routeKey = row.RouteKey
			quoteNotional = row.QuoteNotionalUSD
			quoteReferenceKind = row.QuoteReferenceKind
			quality = row.Quality
			sourceTime = row.BlockTimestamp
		}
		metadata, _ := json.Marshal(map[string]interface{}{
			"route_key": routeKey, "rollout_mode": mode,
			"technical_available":  available,
			"pool_addresses":       poolAddresses,
			"protocol_versions":    protocolVersions,
			"quote_notional_usd":   quoteNotional,
			"quote_reference_kind": quoteReferenceKind,
			"local_preview":        rollout != nil && rollout.LocalPreviewEnabled,
			"exclusions": []map[string]string{{
				"reason": unavailableReason,
			}},
		})
		if available {
			metadata, _ = json.Marshal(map[string]interface{}{
				"route_key": routeKey, "rollout_mode": mode,
				"technical_available":  true,
				"pool_addresses":       poolAddresses,
				"protocol_versions":    protocolVersions,
				"quote_notional_usd":   quoteNotional,
				"quote_reference_kind": quoteReferenceKind,
				"local_preview":        rollout != nil && rollout.LocalPreviewEnabled,
			})
		}
		snapshot := database.AssetVenueSnapshot{
			AssetGuid: assetID, Provider: a.config.Provider, PriceKind: "dex_route",
			ContributorCount: 1, MarketCount: routeCounts[assetID], Confidence: quality,
			Quality: quality, Available: available, SourceTime: sourceTime,
			ObservedAt: now, Metadata: metadata,
		}
		if hasRoute && !row.ObservedAt.IsZero() {
			snapshot.ObservedAt = row.ObservedAt
		}
		if available {
			snapshot.PriceUSD = row.PriceUSD
			snapshot.Change24hPct = row.Change24hPct
			snapshot.Turnover24hUSD = row.Turnover24hUSD
		}
		snapshots = append(snapshots, snapshot)
	}
	return a.db.MarketAggregation.ReplaceDexVenueSnapshots(snapshots)
}

func betterDexRoute(candidate, current database.DexRouteCurrent) bool {
	if candidate.Available != current.Available {
		return candidate.Available
	}
	qualityRank := map[string]int{"unknown": 0, "low": 1, "medium": 2, "high": 3}
	if qualityRank[candidate.Quality] != qualityRank[current.Quality] {
		return qualityRank[candidate.Quality] > qualityRank[current.Quality]
	}
	candidateTVL := decimal.Zero
	currentTVL := decimal.Zero
	if candidate.TVLUSD != nil {
		candidateTVL, _ = decimal.NewFromString(*candidate.TVLUSD)
	}
	if current.TVLUSD != nil {
		currentTVL, _ = decimal.NewFromString(*current.TVLUSD)
	}
	return candidateTVL.GreaterThan(currentTVL)
}

func encodeV3Path(tokens []database.AssetRepresentation, fees []int) ([]byte, error) {
	if len(tokens) < 2 || len(fees) != len(tokens)-1 {
		return nil, fmt.Errorf("invalid V3 route shape")
	}
	path := make([]byte, 0, 20*len(tokens)+3*len(fees))
	for index, token := range tokens {
		if !common.IsHexAddress(token.ContractAddress) {
			return nil, fmt.Errorf("invalid route token address")
		}
		path = append(path, common.HexToAddress(token.ContractAddress).Bytes()...)
		if index < len(fees) {
			fee := fees[index]
			if fee <= 0 || fee > 0xffffff {
				return nil, fmt.Errorf("invalid V3 fee")
			}
			path = append(path, byte(fee>>16), byte(fee>>8), byte(fee))
		}
	}
	return path, nil
}

func reverseRepresentations(values []database.AssetRepresentation) []database.AssetRepresentation {
	result := append([]database.AssetRepresentation(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func reversePools(values []discoveredPool) []discoveredPool {
	result := append([]discoveredPool(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func routeLiquidity(pools []discoveredPool) (decimal.Decimal, decimal.Decimal) {
	if len(pools) == 0 {
		return decimal.Zero, decimal.Zero
	}
	tvl := pools[0].TVLUSD
	volume := decimal.Zero
	seen := make(map[string]struct{})
	for _, pool := range pools {
		if pool.TVLUSD.LessThan(tvl) {
			tvl = pool.TVLUSD
		}
		if _, exists := seen[pool.Address]; !exists {
			volume = volume.Add(pool.Volume24hUSD)
			seen[pool.Address] = struct{}{}
		}
	}
	return tvl, volume
}

func newestPoolTime(pools []discoveredPool) *time.Time {
	var latest time.Time
	for _, pool := range pools {
		if pool.BlockTimestamp.After(latest) {
			latest = pool.BlockTimestamp
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}

func poolPairKey(left, right string) string {
	left = strings.ToLower(left)
	right = strings.ToLower(right)
	if left > right {
		left, right = right, left
	}
	return left + ":" + right
}

func decimalText(value decimal.Decimal) *string {
	text := value.String()
	return &text
}

func textPtr(value string) *string { return &value }

func mustABI(raw string) abi.ABI {
	value, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return value
}

var poolIdentityABI = mustABI(`[
  {"inputs":[],"name":"token0","outputs":[{"type":"address"}],"stateMutability":"view","type":"function"},
  {"inputs":[],"name":"token1","outputs":[{"type":"address"}],"stateMutability":"view","type":"function"},
  {"inputs":[],"name":"factory","outputs":[{"type":"address"}],"stateMutability":"view","type":"function"},
  {"inputs":[],"name":"fee","outputs":[{"type":"uint24"}],"stateMutability":"view","type":"function"}
]`)

var erc20MetadataABI = mustABI(`[
  {"inputs":[],"name":"decimals","outputs":[{"type":"uint8"}],"stateMutability":"view","type":"function"}
]`)

var v3FactoryABI = mustABI(`[
  {"inputs":[
    {"name":"tokenA","type":"address"},
    {"name":"tokenB","type":"address"},
    {"name":"fee","type":"uint24"}
  ],"name":"getPool","outputs":[{"name":"pool","type":"address"}],
  "stateMutability":"view","type":"function"}
]`)

var v2FactoryABI = mustABI(`[
  {"inputs":[
    {"name":"tokenA","type":"address"},
    {"name":"tokenB","type":"address"}
  ],"name":"getPair","outputs":[{"name":"pair","type":"address"}],
  "stateMutability":"view","type":"function"}
]`)

var v2RouterABI = mustABI(`[
  {"inputs":[
    {"name":"amountIn","type":"uint256"},
    {"name":"path","type":"address[]"}
  ],"name":"getAmountsOut","outputs":[{"name":"amounts","type":"uint256[]"}],
  "stateMutability":"view","type":"function"}
]`)

var quoterV2ABI = mustABI(`[
  {"inputs":[{"name":"path","type":"bytes"},{"name":"amountIn","type":"uint256"}],
   "name":"quoteExactInput",
   "outputs":[
     {"name":"amountOut","type":"uint256"},
     {"name":"sqrtPriceX96AfterList","type":"uint160[]"},
     {"name":"initializedTicksCrossedList","type":"uint32[]"},
     {"name":"gasEstimate","type":"uint256"}
   ],
   "stateMutability":"nonpayable","type":"function"}
]`)
