package providercontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRouterStableRetryableAndUnsupportedFallback(t *testing.T) {
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	first := fakeWithCapabilities("first", CapabilitySpotTicker)
	second := fakeWithCapabilities("second", CapabilitySpotTicker)
	third := fakeWithCapabilities("third", CapabilitySpotTicker)
	require.NoError(t, first.Script(request, FakeStep{Err: NewError(ErrorUnsupported, "first", "fetch", nil)}))
	require.NoError(t, second.Script(request, FakeStep{Err: NewError(ErrorNetwork, "second", "fetch", nil)}))
	expected := fixtureResponse("third", CapabilitySpotTicker, "third-result")
	require.NoError(t, third.Script(request, FakeStep{Response: expected}))

	router, err := NewRouter([]Provider{first, second, third}, RouterOptions{})
	require.NoError(t, err)
	result, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, expected, result.Response)
	require.Equal(t, ProviderID("third"), result.Trace.ActualProvider)
	require.Equal(t, expected.Meta.Source, result.Trace.Source)
	require.Equal(t, []AttemptTrace{
		{Provider: "first", Capability: CapabilitySpotTicker, ErrorKind: ErrorUnsupported},
		{Provider: "second", Capability: CapabilitySpotTicker, ErrorKind: ErrorNetwork},
		{Provider: "third", Capability: CapabilitySpotTicker},
	}, result.Trace.Attempts)
	require.Len(t, first.Calls(), 1)
	require.Len(t, second.Calls(), 1)
	require.Len(t, third.Calls(), 1)
}

func TestRouterTimeoutFallsBackToNextProvider(t *testing.T) {
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	primary := fakeWithCapabilities("primary", CapabilitySpotTicker)
	fallback := fakeWithCapabilities("fallback", CapabilitySpotTicker)
	require.NoError(t, primary.Script(request, FakeStep{
		Err: NewRetryError(ErrorTimeout, "primary", "fetch", 0, context.DeadlineExceeded),
	}))
	expected := fixtureResponse("fallback", CapabilitySpotTicker, "timeout-fallback")
	require.NoError(t, fallback.Script(request, FakeStep{Response: expected}))
	router, err := NewRouter([]Provider{primary, fallback}, RouterOptions{})
	require.NoError(t, err)

	result, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, ProviderID("fallback"), result.Trace.ActualProvider)
	require.Equal(t, []AttemptTrace{
		{Provider: "primary", Capability: CapabilitySpotTicker, ErrorKind: ErrorTimeout},
		{Provider: "fallback", Capability: CapabilitySpotTicker},
	}, result.Trace.Attempts)
}

func TestRouterFailClosedErrorsNeverFallback(t *testing.T) {
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	for _, kind := range []ErrorKind{
		ErrorAuth, ErrorPermission, ErrorUnconfigured, ErrorBadRequest,
		ErrorBadPayload, ErrorInvalidSchema, ErrorInvalidIdentity,
	} {
		t.Run(string(kind), func(t *testing.T) {
			first := fakeWithCapabilities("first", CapabilitySpotTicker)
			second := fakeWithCapabilities("second", CapabilitySpotTicker)
			require.NoError(t, first.Script(request, FakeStep{Err: NewError(kind, "first", "fetch", nil)}))
			require.NoError(t, second.Script(request, FakeStep{Response: fixtureResponse("second", CapabilitySpotTicker, "must-not-run")}))
			router, err := NewRouter([]Provider{first, second}, RouterOptions{})
			require.NoError(t, err)

			result, err := router.Dispatch(context.Background(), request)
			require.ErrorIs(t, err, &ProviderError{Kind: kind, Provider: "first"})
			require.Equal(t, []AttemptTrace{{Provider: "first", Capability: CapabilitySpotTicker, ErrorKind: kind}}, result.Trace.Attempts)
			require.Empty(t, second.Calls())
		})
	}
}

func TestRouterBindsOrRejectsProviderErrorIdentity(t *testing.T) {
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	t.Run("empty identity is bound to actual provider", func(t *testing.T) {
		first := fakeWithCapabilities("first", CapabilitySpotTicker)
		second := fakeWithCapabilities("second", CapabilitySpotTicker)
		require.NoError(t, first.Script(request, FakeStep{Err: NewError(ErrorNetwork, "", "fetch", nil)}))
		require.NoError(t, second.Script(request, FakeStep{Response: fixtureResponse("second", CapabilitySpotTicker, "ok")}))
		router, err := NewRouter([]Provider{first, second}, RouterOptions{})
		require.NoError(t, err)

		result, err := router.Dispatch(context.Background(), request)
		require.NoError(t, err)
		require.Equal(t, ErrorNetwork, result.Trace.Attempts[0].ErrorKind)
		require.Equal(t, ProviderID("second"), result.Trace.ActualProvider)
	})

	t.Run("conflicting identity fails closed", func(t *testing.T) {
		first := fakeWithCapabilities("first", CapabilitySpotTicker)
		second := fakeWithCapabilities("second", CapabilitySpotTicker)
		require.NoError(t, first.Script(request, FakeStep{Err: NewError(ErrorNetwork, "impostor", "fetch", nil)}))
		require.NoError(t, second.Script(request, FakeStep{Response: fixtureResponse("second", CapabilitySpotTicker, "must-not-run")}))
		router, err := NewRouter([]Provider{first, second}, RouterOptions{})
		require.NoError(t, err)

		result, err := router.Dispatch(context.Background(), request)
		require.ErrorIs(t, err, &ProviderError{Kind: ErrorInvalidIdentity, Provider: "first"})
		require.Equal(t, ErrorInvalidIdentity, result.Trace.Attempts[0].ErrorKind)
		require.Empty(t, second.Calls())
	})
}

func TestRouterUntypedProviderErrorFailsClosed(t *testing.T) {
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	primary := fakeWithCapabilities("primary", CapabilitySpotTicker)
	fallback := fakeWithCapabilities("fallback", CapabilitySpotTicker)
	require.NoError(t, primary.Script(request, FakeStep{Err: errors.New("opaque SDK failure")}))
	require.NoError(t, fallback.Script(request, FakeStep{Response: fixtureResponse("fallback", CapabilitySpotTicker, "must-not-run")}))
	router, err := NewRouter([]Provider{primary, fallback}, RouterOptions{})
	require.NoError(t, err)

	result, err := router.Dispatch(context.Background(), request)
	require.ErrorIs(t, err, &ProviderError{Kind: ErrorBadPayload, Provider: "primary"})
	require.Equal(t, []AttemptTrace{{
		Provider: "primary", Capability: CapabilitySpotTicker, ErrorKind: ErrorBadPayload,
	}}, result.Trace.Attempts)
	require.Empty(t, fallback.Calls())
}

func TestRouterRateLimitBlocksUntilRetryAfter(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	primary := fakeWithCapabilities("primary", CapabilitySpotTicker)
	fallback := fakeWithCapabilities("fallback", CapabilitySpotTicker)
	retryAfter := 30 * time.Second
	require.NoError(t, primary.Script(request,
		FakeStep{Err: NewRetryError(ErrorRateLimit, "primary", "fetch", retryAfter, nil)},
		FakeStep{Response: fixtureResponse("primary", CapabilitySpotTicker, "recovered")},
	))
	require.NoError(t, fallback.Script(request,
		FakeStep{Response: fixtureResponse("fallback", CapabilitySpotTicker, "fallback-1")},
		FakeStep{Response: fixtureResponse("fallback", CapabilitySpotTicker, "fallback-2")},
	))
	router, err := NewRouter([]Provider{primary, fallback}, RouterOptions{Clock: clock})
	require.NoError(t, err)

	first, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, ProviderID("fallback"), first.Trace.ActualProvider)
	require.Equal(t, retryAfter, first.Trace.Attempts[0].RetryAfter)

	clock.Advance(10 * time.Second)
	second, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, ProviderID("fallback"), second.Trace.ActualProvider)
	require.Equal(t, AttemptTrace{
		Provider: "primary", Capability: CapabilitySpotTicker,
		ErrorKind: ErrorRateLimit, RetryAfter: 20 * time.Second,
	}, second.Trace.Attempts[0])
	require.Len(t, primary.Calls(), 1, "blocked provider is not invoked")

	clock.Advance(20 * time.Second)
	third, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, ProviderID("primary"), third.Trace.ActualProvider)
	require.Len(t, primary.Calls(), 2)
	require.Len(t, fallback.Calls(), 2)
}

func TestRouterCachePreservesActualSourceAndExpiresDeterministically(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	cache := NewCache(1, clock)
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	provider := fakeWithCapabilities("actual", CapabilitySpotTicker)
	firstResponse := fixtureResponse("actual", CapabilitySpotTicker, "one")
	secondResponse := fixtureResponse("actual", CapabilitySpotTicker, "two")
	require.NoError(t, provider.Script(request, FakeStep{Response: firstResponse}, FakeStep{Response: secondResponse}))
	router, err := NewRouter([]Provider{provider}, RouterOptions{Clock: clock, Cache: cache, CacheTTL: time.Minute})
	require.NoError(t, err)

	_, err = router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	cached, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.True(t, cached.Trace.CacheHit)
	require.Equal(t, ProviderID("actual"), cached.Trace.ActualProvider)
	require.Equal(t, firstResponse.Meta.Source, cached.Trace.Source)
	require.Equal(t, []AttemptTrace{{Provider: "actual", Capability: CapabilitySpotTicker, CacheHit: true}}, cached.Trace.Attempts)
	require.Len(t, provider.Calls(), 1)

	clock.Advance(time.Minute)
	fresh, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.False(t, fresh.Trace.CacheHit)
	require.Equal(t, secondResponse, fresh.Response)
	require.Len(t, provider.Calls(), 2)
}

func TestRouterCacheTTLNeverExceedsResponseFreshness(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	cache := NewCache(1, clock)
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	provider := fakeWithCapabilities("actual", CapabilitySpotTicker)
	first := fixtureResponse("actual", CapabilitySpotTicker, "one")
	first.Meta.ObservedAt = clock.Now()
	first.Meta.ReceivedAt = clock.Now()
	first.Meta.TTL = 20 * time.Second
	firstValue := first.Value.(SpotTickerEnvelope)
	firstValue.Meta.ObservedAt = clock.Now()
	firstValue.Meta.ReceivedAt = clock.Now()
	firstValue.Meta.TTL = 20 * time.Second
	first.Value = firstValue
	second := fixtureResponse("actual", CapabilitySpotTicker, "two")
	require.NoError(t, provider.Script(request, FakeStep{Response: first}, FakeStep{Response: second}))
	router, err := NewRouter([]Provider{provider}, RouterOptions{
		Clock: clock, Cache: cache, CacheTTL: time.Hour,
	})
	require.NoError(t, err)

	_, err = router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	clock.Advance(20 * time.Second)
	result, err := router.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, second, result.Response)
	require.False(t, result.Trace.CacheHit)
	require.Len(t, provider.Calls(), 2)
}

func TestRouterSharedCacheIsIsolatedByProviderOrderAndPolicy(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}

	t.Run("provider fingerprint", func(t *testing.T) {
		cache := NewCache(4, clock)
		firstProvider := fakeWithCapabilities("first", CapabilitySpotTicker)
		secondProvider := fakeWithCapabilities("second", CapabilitySpotTicker)
		require.NoError(t, firstProvider.Script(request, FakeStep{Response: fixtureResponse("first", CapabilitySpotTicker, "one")}))
		require.NoError(t, secondProvider.Script(request, FakeStep{Response: fixtureResponse("second", CapabilitySpotTicker, "two")}))
		firstRouter, err := NewRouter([]Provider{firstProvider}, RouterOptions{Clock: clock, Cache: cache, CacheTTL: time.Minute})
		require.NoError(t, err)
		secondRouter, err := NewRouter([]Provider{secondProvider}, RouterOptions{Clock: clock, Cache: cache, CacheTTL: time.Minute})
		require.NoError(t, err)

		_, err = firstRouter.Dispatch(context.Background(), request)
		require.NoError(t, err)
		second, err := secondRouter.Dispatch(context.Background(), request)
		require.NoError(t, err)
		require.False(t, second.Trace.CacheHit)
		require.Equal(t, ProviderID("second"), second.Trace.ActualProvider)
		require.Len(t, secondProvider.Calls(), 1)
	})

	t.Run("ttl policy fingerprint", func(t *testing.T) {
		cache := NewCache(4, clock)
		provider := fakeWithCapabilities("actual", CapabilitySpotTicker)
		require.NoError(t, provider.Script(request,
			FakeStep{Response: fixtureResponse("actual", CapabilitySpotTicker, "long-policy")},
			FakeStep{Response: fixtureResponse("actual", CapabilitySpotTicker, "short-policy")},
		))
		longRouter, err := NewRouter([]Provider{provider}, RouterOptions{Clock: clock, Cache: cache, CacheTTL: time.Hour})
		require.NoError(t, err)
		shortRouter, err := NewRouter([]Provider{provider}, RouterOptions{Clock: clock, Cache: cache, CacheTTL: time.Minute})
		require.NoError(t, err)

		_, err = longRouter.Dispatch(context.Background(), request)
		require.NoError(t, err)
		short, err := shortRouter.Dispatch(context.Background(), request)
		require.NoError(t, err)
		require.False(t, short.Trace.CacheHit)
		require.Equal(t, "short-policy", short.Response.Meta.Source.SourceID)
		require.Len(t, provider.Calls(), 2)
	})
}

func TestRouterCorruptCacheFailsClosedAndEvicts(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	provider := fakeWithCapabilities("actual", CapabilitySpotTicker)
	routerCache := NewCache(2, clock)
	router, err := NewRouter([]Provider{provider}, RouterOptions{
		Clock: clock, Cache: routerCache, CacheTTL: time.Minute,
	})
	require.NoError(t, err)
	normalizedRequest, err := NormalizeRequest(request)
	require.NoError(t, err)
	key := router.cacheNS + requestCacheKey(normalizedRequest)

	t.Run("unregistered source", func(t *testing.T) {
		corrupt := fixtureResponse("unknown", CapabilitySpotTicker, "corrupt-source")
		routerCache.putUntil(key, corrupt, clock.Now().Add(time.Minute))
		result, dispatchErr := router.Dispatch(context.Background(), request)
		require.ErrorIs(t, dispatchErr, &ProviderError{Kind: ErrorInvalidIdentity})
		require.True(t, result.Trace.CacheHit)
		require.Equal(t, []AttemptTrace{{
			Provider: "unknown", Capability: CapabilitySpotTicker,
			ErrorKind: ErrorInvalidIdentity, CacheHit: true,
		}}, result.Trace.Attempts)
		require.Zero(t, routerCache.Len())
		require.Empty(t, provider.Calls())
	})

	t.Run("invalid payload", func(t *testing.T) {
		corrupt := fixtureResponse("actual", CapabilitySpotTicker, "corrupt-payload")
		inner := corrupt.Value.(SpotTickerEnvelope)
		inner.Data.LastPrice.Value = "not-a-decimal"
		corrupt.Value = inner
		routerCache.putUntil(key, corrupt, clock.Now().Add(time.Minute))
		result, dispatchErr := router.Dispatch(context.Background(), request)
		require.ErrorIs(t, dispatchErr, &ProviderError{Kind: ErrorBadPayload})
		require.True(t, result.Trace.CacheHit)
		require.Equal(t, ErrorBadPayload, result.Trace.Attempts[0].ErrorKind)
		require.True(t, result.Trace.Attempts[0].CacheHit)
		require.Zero(t, routerCache.Len())
		require.Empty(t, provider.Calls())
	})
}

func TestRouterStaleResponseFailsClosedAndIsNotCached(t *testing.T) {
	clock := NewManualClock(time.Date(2026, 8, 10, 0, 1, 0, 0, time.UTC))
	cache := NewCache(1, clock)
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	stale := fakeWithCapabilities("stale", CapabilitySpotTicker)
	fallback := fakeWithCapabilities("fallback", CapabilitySpotTicker)
	response := fixtureResponse("stale", CapabilitySpotTicker, "stale")
	response.Meta.TTL = 30 * time.Second
	inner := response.Value.(SpotTickerEnvelope)
	inner.Meta.TTL = 30 * time.Second
	response.Value = inner
	require.NoError(t, stale.Script(request, FakeStep{Response: response}))
	require.NoError(t, fallback.Script(request, FakeStep{Response: fixtureResponse("fallback", CapabilitySpotTicker, "must-not-run")}))
	router, err := NewRouter([]Provider{stale, fallback}, RouterOptions{
		Clock: clock, Cache: cache, CacheTTL: time.Hour,
	})
	require.NoError(t, err)

	result, err := router.Dispatch(context.Background(), request)
	require.ErrorIs(t, err, &ProviderError{Kind: ErrorStale, Provider: "stale"})
	require.Equal(t, ErrorStale, result.Trace.Attempts[0].ErrorKind)
	require.Empty(t, fallback.Calls())
	require.Zero(t, cache.Len())
}

func TestRouterRejectsBadResponseWithoutMaskingIt(t *testing.T) {
	request := Request{Capability: CapabilitySpotTicker, Key: "btc-usdt"}
	bad := fakeWithCapabilities("bad", CapabilitySpotTicker)
	fallback := fakeWithCapabilities("fallback", CapabilitySpotTicker)
	wrongSource := fixtureResponse("somebody-else", CapabilitySpotTicker, "wrong")
	require.NoError(t, bad.Script(request, FakeStep{Response: wrongSource}))
	require.NoError(t, fallback.Script(request, FakeStep{Response: fixtureResponse("fallback", CapabilitySpotTicker, "must-not-run")}))
	router, err := NewRouter([]Provider{bad, fallback}, RouterOptions{})
	require.NoError(t, err)

	result, err := router.Dispatch(context.Background(), request)
	require.ErrorIs(t, err, &ProviderError{Kind: ErrorInvalidIdentity, Provider: "bad"})
	require.Equal(t, ErrorInvalidIdentity, result.Trace.Attempts[0].ErrorKind)
	require.Empty(t, fallback.Calls())
}

func TestRouterDiscoveryAndUnsupportedAreStable(t *testing.T) {
	first := fakeWithCapabilities("first", CapabilitySignals, CapabilitySpotTicker)
	second := fakeWithCapabilities("second", CapabilityOHLCV)
	router, err := NewRouter([]Provider{first, second}, RouterOptions{})
	require.NoError(t, err)
	require.Equal(t, []ProviderID{"first", "second"}, []ProviderID{
		router.Providers()[0].ID, router.Providers()[1].ID,
	})

	result, err := router.Dispatch(context.Background(), Request{Capability: CapabilityDerivatives, Key: "btc"})
	require.ErrorIs(t, err, &ProviderError{Kind: ErrorUnsupported})
	require.Empty(t, result.Trace.Attempts)
	require.Empty(t, first.Calls())
	require.Empty(t, second.Calls())
}

func TestRouterDoesNotDispatchAfterCallerCancellation(t *testing.T) {
	provider := fakeWithCapabilities("only", CapabilitySpotTicker)
	router, err := NewRouter([]Provider{provider}, RouterOptions{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = router.Dispatch(ctx, Request{Capability: CapabilitySpotTicker, Key: "btc"})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, provider.Calls())
}

func fakeWithCapabilities(id ProviderID, capabilities ...Capability) *FakeProvider {
	return NewFakeProvider(ProviderIdentity{ID: id, DisplayName: string(id), Capabilities: capabilities})
}
