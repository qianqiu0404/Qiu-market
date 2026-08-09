package coinglass

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

const (
	ProviderID providercontract.ProviderID = "coinglass"

	RequestKeyOpenInterest = "BTC-USD-PERP:open-interest"
	RequestKeyLiquidation  = "BTC-USD-PERP:liquidation"
	RequestKeyFundingRate  = "BTC-USD-PERP:funding-rate"

	DefaultCacheCapacity = 8
	MaximumCacheCapacity = 32
	DefaultCacheTTL      = 20 * time.Second
	MaximumCacheTTL      = time.Minute

	historyInterval    = "4h"
	historyLimit       = "2"
	derivativeTTL      = time.Minute
	maximumSourceAge   = 5 * time.Hour
	liquidationWindow  = int64(4 * time.Hour / time.Second)
	fundingUnsupported = "CoinGlass documents a funding_rate value but does not declare whether this endpoint returns a ratio or percent"
)

// Metric names the independently routable parts of the coarse Q-M3
// derivatives capability.
type Metric string

const (
	MetricOpenInterest Metric = "open_interest"
	MetricFundingRate  Metric = "funding_rate"
	MetricLiquidation  Metric = "liquidation"
)

// ContractIdentity is the fixed upstream authority for this adapter. Quote
// and settlement are deliberately distinct: BTCUSD_PERP is never relabelled
// as BTC/USDT merely because it settles in USDT.
type ContractIdentity struct {
	Exchange           string                      `json:"exchange"`
	InstrumentID       string                      `json:"instrument_id"`
	BaseAsset          string                      `json:"base_asset"`
	QuoteAsset         string                      `json:"quote_asset"`
	SettlementCurrency string                      `json:"settlement_currency"`
	MarketType         providercontract.MarketType `json:"market_type"`
}

// MetricSupport makes partial provider capability explicit. Unsupported
// metrics are never represented by a successful empty snapshot.
type MetricSupport struct {
	Metric       Metric                `json:"metric"`
	Supported    bool                  `json:"supported"`
	Unit         providercontract.Unit `json:"unit,omitempty"`
	WindowSec    int64                 `json:"window_seconds,omitempty"`
	EndpointPath string                `json:"endpoint_path,omitempty"`
	Reason       string                `json:"reason,omitempty"`
}

// Discovery binds coarse Q-M3 discovery to the exact contract and per-metric
// support known from the current official CoinGlass documentation.
type Discovery struct {
	Provider providercontract.ProviderIdentity `json:"provider"`
	Contract ContractIdentity                  `json:"contract"`
	Metrics  []MetricSupport                   `json:"metrics"`
}

// Config has no origin or transport override. The key is obtained only from
// a process-injected SecretProvider and is excluded from serialization.
type Config struct {
	Enabled         bool
	SecretProvider  SecretProvider         `json:"-"`
	Clock           providercontract.Clock `json:"-"`
	CacheCapacity   int
	CacheTTL        time.Duration
	ObservationSink ObservationSink `json:"-"`
}

type Reader struct {
	router *providercontract.Router
}

type jsonTransport interface {
	DoJSON(context.Context, string, string, url.Values, any) (time.Time, error)
}

type adapter struct {
	enabled   bool
	clock     providercontract.Clock
	transport jsonTransport
}

type adapterClock struct{}

func (adapterClock) Now() time.Time { return time.Now().UTC() }

// NewReader is the only production construction path. Its zero value is
// disabled and constructs no HTTP client. Enabling without a secret channel
// fails before any network boundary exists.
func NewReader(config Config) (*Reader, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	var transport jsonTransport
	if config.Enabled {
		if config.SecretProvider == nil {
			return nil, providercontract.NewError(
				providercontract.ErrorUnconfigured,
				ProviderID,
				"configure",
				errors.New("enabled CoinGlass provider requires a secret provider"),
			)
		}
		client, clientErr := newProductionClient(clientOptions{
			Clock:          config.Clock,
			Sink:           config.ObservationSink,
			SecretProvider: config.SecretProvider,
		})
		if clientErr != nil {
			return nil, clientErr
		}
		transport = client
	}
	return newReaderWithTransport(config, transport)
}

// newReaderWithTransport is package-private so tests can replace the network
// seam without exposing arbitrary origins or transports to production callers.
func newReaderWithTransport(config Config, transport jsonTransport) (*Reader, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	provider := &adapter{enabled: config.Enabled, clock: config.Clock, transport: transport}
	router, err := providercontract.NewRouter(
		[]providercontract.Provider{provider},
		providercontract.RouterOptions{
			Clock:    config.Clock,
			Cache:    providercontract.NewCache(config.CacheCapacity, config.Clock),
			CacheTTL: config.CacheTTL,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("construct CoinGlass router: %w", err)
	}
	return &Reader{router: router}, nil
}

func normalizeConfig(config Config) (Config, error) {
	if config.Clock == nil {
		config.Clock = adapterClock{}
	}
	if config.CacheCapacity == 0 {
		config.CacheCapacity = DefaultCacheCapacity
	}
	if config.CacheCapacity < 0 || config.CacheCapacity > MaximumCacheCapacity {
		return Config{}, providercontract.NewError(
			providercontract.ErrorBadRequest,
			ProviderID,
			"configure",
			fmt.Errorf("cache capacity must be within [1,%d]", MaximumCacheCapacity),
		)
	}
	if config.CacheTTL == 0 {
		config.CacheTTL = DefaultCacheTTL
	}
	if config.CacheTTL < 0 || config.CacheTTL > MaximumCacheTTL {
		return Config{}, providercontract.NewError(
			providercontract.ErrorBadRequest,
			ProviderID,
			"configure",
			fmt.Errorf("cache TTL must be within (0,%s]", MaximumCacheTTL),
		)
	}
	return config, nil
}

func (r *Reader) Fetch(
	ctx context.Context,
	request providercontract.Request,
) (providercontract.DispatchResult, error) {
	if r == nil || r.router == nil {
		return providercontract.DispatchResult{}, providercontract.NewError(
			providercontract.ErrorUnconfigured, ProviderID, "fetch", errors.New("nil reader"),
		)
	}
	return r.router.Dispatch(ctx, request)
}

func (r *Reader) OpenInterest(ctx context.Context) (providercontract.DispatchResult, error) {
	return r.Fetch(ctx, derivativesRequest(RequestKeyOpenInterest))
}

func (r *Reader) Liquidation(ctx context.Context) (providercontract.DispatchResult, error) {
	return r.Fetch(ctx, derivativesRequest(RequestKeyLiquidation))
}

func (r *Reader) FundingRate(ctx context.Context) (providercontract.DispatchResult, error) {
	return r.Fetch(ctx, derivativesRequest(RequestKeyFundingRate))
}

func derivativesRequest(key string) providercontract.Request {
	return providercontract.Request{Capability: providercontract.CapabilityDerivatives, Key: key}
}

func (*Reader) Discovery() Discovery { return discovery() }

func discovery() Discovery {
	provider := (&adapter{}).Identity()
	return Discovery{
		Provider: provider,
		Contract: contractIdentity,
		Metrics: []MetricSupport{
			{
				Metric: MetricOpenInterest, Supported: true, Unit: providercontract.UnitUSD,
				EndpointPath: openInterestHistoryPath,
			},
			{
				Metric: MetricFundingRate, Supported: false, Reason: fundingUnsupported,
			},
			{
				Metric: MetricLiquidation, Supported: true, Unit: providercontract.UnitUSD,
				WindowSec: liquidationWindow, EndpointPath: liquidationHistoryPath,
			},
		},
	}
}

func (*adapter) Identity() providercontract.ProviderIdentity {
	return providercontract.ProviderIdentity{
		ID: ProviderID, DisplayName: "CoinGlass Derivatives (disabled by default)",
		Capabilities: []providercontract.Capability{providercontract.CapabilityDerivatives},
	}
}

func (a *adapter) Capabilities() []providercontract.Capability {
	return append([]providercontract.Capability(nil), a.Identity().Capabilities...)
}

func (a *adapter) Fetch(
	ctx context.Context,
	request providercontract.Request,
) (providercontract.Response, error) {
	if err := ctx.Err(); err != nil {
		return providercontract.Response{}, err
	}
	request, err := providercontract.NormalizeRequest(request)
	if err != nil {
		return providercontract.Response{}, err
	}
	if request.Capability != providercontract.CapabilityDerivatives {
		return providercontract.Response{}, providercontract.NewError(
			providercontract.ErrorUnsupported, ProviderID, "fetch",
			fmt.Errorf("capability %q is not supported", request.Capability),
		)
	}
	if len(request.Parameters) != 0 {
		return providercontract.Response{}, providercontract.NewError(
			providercontract.ErrorBadRequest, ProviderID, "fetch",
			errors.New("the fixed CoinGlass requests do not accept parameters"),
		)
	}
	// Funding fails before the enabled/transport checks by design: the current
	// official response does not identify its unit, so no configuration can
	// make it safe to normalize or send over the network.
	if request.Key == RequestKeyFundingRate {
		return providercontract.Response{}, providercontract.NewError(
			providercontract.ErrorUnsupported, ProviderID, "funding_rate",
			errors.New(fundingUnsupported),
		)
	}
	if request.Key != RequestKeyOpenInterest && request.Key != RequestKeyLiquidation {
		return providercontract.Response{}, providercontract.NewError(
			providercontract.ErrorBadRequest, ProviderID, "fetch",
			errors.New("only the frozen BTCUSD_PERP derivative metric keys are supported"),
		)
	}
	if a == nil || !a.enabled || a.transport == nil || a.clock == nil {
		return providercontract.Response{}, providercontract.NewError(
			providercontract.ErrorUnconfigured, ProviderID, "fetch",
			errors.New("CoinGlass provider is disabled or incomplete"),
		)
	}

	var response providercontract.Response
	switch request.Key {
	case RequestKeyOpenInterest:
		response, err = a.fetchOpenInterest(ctx)
	case RequestKeyLiquidation:
		response, err = a.fetchLiquidation(ctx)
	}
	if err != nil {
		return providercontract.Response{}, normalizeTransportError(err)
	}
	return providercontract.NormalizeResponse(
		response, a.clock.Now().UTC(), providercontract.DefaultMaxFutureSkew,
	)
}

func (a *adapter) fetchOpenInterest(ctx context.Context) (providercontract.Response, error) {
	query := historyQuery()
	query.Set("unit", "usd")
	var payload openInterestHistoryPayload
	receivedAt, err := a.transport.DoJSON(
		ctx, OperationOpenInterestHistory, openInterestHistoryPath, query, &payload,
	)
	if err != nil {
		return providercontract.Response{}, err
	}
	return mapOpenInterest(payload, receivedAt)
}

func (a *adapter) fetchLiquidation(ctx context.Context) (providercontract.Response, error) {
	var payload liquidationHistoryPayload
	receivedAt, err := a.transport.DoJSON(
		ctx, OperationLiquidationHistory, liquidationHistoryPath, historyQuery(), &payload,
	)
	if err != nil {
		return providercontract.Response{}, err
	}
	return mapLiquidation(payload, receivedAt)
}

func historyQuery() url.Values {
	return url.Values{
		"exchange": {providerExchange},
		"symbol":   {providerInstrument},
		"interval": {historyInterval},
		"limit":    {historyLimit},
	}
}

func normalizeTransportError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var providerError *providercontract.ProviderError
	if errors.As(err, &providerError) {
		return err
	}
	return providercontract.NewError(
		providercontract.ErrorBadPayload,
		ProviderID,
		"fetch",
		fmt.Errorf("transport returned an untyped error: %w", err),
	)
}
