package binancepublic

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

const (
	ProviderID           providercontract.ProviderID = "binance-public"
	RequestKey                                       = "BTC-USDT"
	DefaultCacheCapacity                             = 16
	MaximumCacheCapacity                             = 128
	DefaultCacheTTL                                  = 5 * time.Second
	MaximumCacheTTL                                  = time.Minute
	spotTickerTTL                                    = 5 * time.Second
	ohlcvTTL                                         = 65 * time.Second
)

// Config is deliberately unable to inject a transport or origin. NewReader
// always constructs the allowlisted Binance public client when Enabled is
// true. The zero value is disabled and cannot perform network I/O.
type Config struct {
	Enabled         bool
	Clock           providercontract.Clock
	CacheCapacity   int
	CacheTTL        time.Duration
	ObservationSink ObservationSink
}

type Reader struct {
	router *providercontract.Router
}

type jsonTransport interface {
	DoJSON(
		context.Context,
		string,
		string,
		url.Values,
		any,
	) (time.Time, error)
}

type adapter struct {
	enabled   bool
	clock     providercontract.Clock
	transport jsonTransport
}

type adapterClock struct{}

func (adapterClock) Now() time.Time { return time.Now().UTC() }

// NewReader is the only production construction path. When disabled it does
// not even construct an HTTP client; Fetch returns a typed unconfigured error.
func NewReader(config Config) (*Reader, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	var transport jsonTransport
	if config.Enabled {
		client, clientErr := newProductionClient(clientOptions{
			Clock: config.Clock,
			Sink:  config.ObservationSink,
		})
		if clientErr != nil {
			return nil, clientErr
		}
		transport = client
	}
	return newReaderWithTransport(config, transport)
}

// newReaderWithTransport is intentionally package-private. Tests can replace
// the network boundary without creating a public arbitrary-origin seam.
func newReaderWithTransport(config Config, transport jsonTransport) (*Reader, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	provider := &adapter{
		enabled: config.Enabled, clock: config.Clock, transport: transport,
	}
	cache := providercontract.NewCache(config.CacheCapacity, config.Clock)
	router, err := providercontract.NewRouter(
		[]providercontract.Provider{provider},
		providercontract.RouterOptions{
			Clock: config.Clock, Cache: cache, CacheTTL: config.CacheTTL,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("construct Binance public router: %w", err)
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
			providercontract.ErrorUnconfigured,
			ProviderID,
			"fetch",
			errors.New("nil reader"),
		)
	}
	return r.router.Dispatch(ctx, request)
}

func (r *Reader) SpotTicker(ctx context.Context) (providercontract.DispatchResult, error) {
	return r.Fetch(ctx, providercontract.Request{
		Capability: providercontract.CapabilitySpotTicker,
		Key:        RequestKey,
	})
}

func (r *Reader) OHLCV(ctx context.Context) (providercontract.DispatchResult, error) {
	return r.Fetch(ctx, providercontract.Request{
		Capability: providercontract.CapabilityOHLCV,
		Key:        RequestKey,
	})
}

func (*adapter) Identity() providercontract.ProviderIdentity {
	return providercontract.ProviderIdentity{
		ID:          ProviderID,
		DisplayName: "Binance Public Spot",
		Capabilities: []providercontract.Capability{
			providercontract.CapabilitySpotTicker,
			providercontract.CapabilityOHLCV,
		},
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
	if a == nil || !a.enabled || a.transport == nil || a.clock == nil {
		return providercontract.Response{}, providercontract.NewError(
			providercontract.ErrorUnconfigured,
			ProviderID,
			"fetch",
			errors.New("Binance public provider is disabled or incomplete"),
		)
	}
	normalizedRequest, err := providercontract.NormalizeRequest(request)
	if err != nil {
		return providercontract.Response{}, err
	}
	if normalizedRequest.Key != RequestKey || len(normalizedRequest.Parameters) != 0 {
		return providercontract.Response{}, providercontract.NewError(
			providercontract.ErrorBadRequest,
			ProviderID,
			"fetch",
			errors.New("only the fixed BTC-USDT request without parameters is supported"),
		)
	}

	var response providercontract.Response
	switch normalizedRequest.Capability {
	case providercontract.CapabilitySpotTicker:
		response, err = a.fetchSpotTicker(ctx)
	case providercontract.CapabilityOHLCV:
		response, err = a.fetchOHLCV(ctx)
	default:
		return providercontract.Response{}, providercontract.NewError(
			providercontract.ErrorUnsupported,
			ProviderID,
			"fetch",
			fmt.Errorf("capability %q is not supported", normalizedRequest.Capability),
		)
	}
	if err != nil {
		return providercontract.Response{}, normalizeTransportError(err)
	}
	return providercontract.NormalizeResponse(
		response,
		a.clock.Now().UTC(),
		providercontract.DefaultMaxFutureSkew,
	)
}

func (a *adapter) fetchSpotTicker(ctx context.Context) (providercontract.Response, error) {
	query := url.Values{
		"symbol":       {providerSymbol},
		"type":         {"FULL"},
		"symbolStatus": {"TRADING"},
	}
	var payload ticker24hPayload
	receivedAt, err := a.transport.DoJSON(
		ctx, OperationTicker24h, tickerPath, query, &payload,
	)
	if err != nil {
		return providercontract.Response{}, err
	}
	return mapTicker(payload, receivedAt)
}

func (a *adapter) fetchOHLCV(ctx context.Context) (providercontract.Response, error) {
	query := url.Values{
		"symbol":   {providerSymbol},
		"interval": {"1m"},
		"limit":    {"10"},
		"timeZone": {"0"},
	}
	var payload []klinePayload
	receivedAt, err := a.transport.DoJSON(
		ctx, OperationKlines, klinesPath, query, &payload,
	)
	if err != nil {
		return providercontract.Response{}, err
	}
	return mapOHLCV(payload, receivedAt)
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
