package providercontract

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AttemptTrace struct {
	Provider   ProviderID    `json:"provider"`
	Capability Capability    `json:"capability"`
	ErrorKind  ErrorKind     `json:"error_kind,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	CacheHit   bool          `json:"cache_hit"`
}

type DispatchTrace struct {
	Attempts       []AttemptTrace `json:"attempts"`
	ActualProvider ProviderID     `json:"actual_provider,omitempty"`
	Source         SourceRef      `json:"source,omitempty"`
	CacheHit       bool           `json:"cache_hit"`
}

type DispatchResult struct {
	Response Response      `json:"response"`
	Trace    DispatchTrace `json:"trace"`
}

type RouterOptions struct {
	Clock    Clock
	Cache    *Cache
	CacheTTL time.Duration
}

type routedProvider struct {
	provider     Provider
	identity     ProviderIdentity
	capabilities map[Capability]struct{}
}

// Router preserves provider registration order. A provider map is never used
// for dispatch, so identical requests and scripts produce identical traces.
type Router struct {
	providers []routedProvider
	clock     Clock
	cache     *Cache
	cacheTTL  time.Duration
	cacheNS   string

	mu           sync.Mutex
	blockedUntil map[ProviderID]time.Time
}

func NewRouter(providers []Provider, options RouterOptions) (*Router, error) {
	clock := options.Clock
	if clock == nil {
		clock = wallClock{}
	}
	router := &Router{
		providers:    make([]routedProvider, 0, len(providers)),
		clock:        clock,
		cache:        options.Cache,
		cacheTTL:     options.CacheTTL,
		blockedUntil: make(map[ProviderID]time.Time),
	}
	seen := make(map[ProviderID]struct{}, len(providers))
	for index, provider := range providers {
		if provider == nil {
			return nil, fmt.Errorf("provider %d is nil", index)
		}
		identity, err := NormalizeProviderIdentity(provider.Identity())
		if err != nil {
			return nil, fmt.Errorf("provider %d identity: %w", index, err)
		}
		if _, exists := seen[identity.ID]; exists {
			return nil, fmt.Errorf("duplicate provider id %q", identity.ID)
		}
		seen[identity.ID] = struct{}{}
		capabilities, err := NormalizeCapabilities(provider.Capabilities())
		if err != nil {
			return nil, fmt.Errorf("provider %q capabilities: %w", identity.ID, err)
		}
		if !reflect.DeepEqual(identity.Capabilities, capabilities) {
			return nil, fmt.Errorf("provider %q identity and discovery capabilities conflict", identity.ID)
		}
		capabilitySet := make(map[Capability]struct{}, len(capabilities))
		for _, capability := range capabilities {
			if capability != "" {
				capabilitySet[capability] = struct{}{}
			}
		}
		router.providers = append(router.providers, routedProvider{
			provider: provider, identity: identity, capabilities: capabilitySet,
		})
	}
	router.cacheNS = routerCacheNamespace(router.providers, router.cacheTTL)
	return router, nil
}

// Providers returns discovery metadata in stable registration order.
func (r *Router) Providers() []ProviderIdentity {
	if r == nil {
		return []ProviderIdentity{}
	}
	result := make([]ProviderIdentity, 0, len(r.providers))
	for _, provider := range r.providers {
		identity := provider.identity
		identity.Capabilities = append([]Capability(nil), identity.Capabilities...)
		result = append(result, identity)
	}
	return result
}

func (r *Router) Dispatch(ctx context.Context, request Request) (DispatchResult, error) {
	result := DispatchResult{Trace: DispatchTrace{Attempts: []AttemptTrace{}}}
	if r == nil {
		return result, NewError(ErrorUnavailable, "", "dispatch", errors.New("nil router"))
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	request, err := NormalizeRequest(request)
	if err != nil {
		return result, err
	}
	key := r.cacheNS + requestCacheKey(request)
	if r.cache != nil {
		if response, ok := r.cache.Get(key); ok {
			provider := response.Meta.Source.Provider
			if !r.supports(provider, request.Capability) {
				err := NewError(
					ErrorInvalidIdentity, provider, "cache_hit",
					errors.New("cached source is not registered for the requested capability"),
				)
				result.Trace.CacheHit = true
				result.Trace.Attempts = append(result.Trace.Attempts, traceFor(provider, request.Capability, err, true))
				r.cache.Delete(key)
				return result, err
			}
			normalized, _, err := r.normalizeRoutedResponse(provider, request, response, r.clock.Now().UTC())
			if err != nil {
				result.Trace.CacheHit = true
				result.Trace.Attempts = append(result.Trace.Attempts, traceFor(provider, request.Capability, err, true))
				r.cache.Delete(key)
				return result, err
			}
			result.Response = normalized
			result.Trace.CacheHit = true
			result.Trace.ActualProvider = normalized.Meta.Source.Provider
			result.Trace.Source = normalized.Meta.Source
			result.Trace.Attempts = append(result.Trace.Attempts, AttemptTrace{
				Provider: normalized.Meta.Source.Provider, Capability: request.Capability, CacheHit: true,
			})
			return result, nil
		}
	}

	capable := 0
	var lastEligible error
	for _, candidate := range r.providers {
		if _, ok := candidate.capabilities[request.Capability]; !ok {
			continue
		}
		capable++
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if remaining, blocked := r.rateLimitRemaining(candidate.identity.ID); blocked {
			err := NewRetryError(ErrorRateLimit, candidate.identity.ID, "dispatch", remaining, errors.New("provider rate-limit window is active"))
			result.Trace.Attempts = append(result.Trace.Attempts, traceFor(candidate.identity.ID, request.Capability, err, false))
			lastEligible = err
			continue
		}

		response, err := candidate.provider.Fetch(ctx, request)
		if err == nil {
			now := r.clock.Now().UTC()
			response, remainingTTL, validationErr := r.normalizeRoutedResponse(candidate.identity.ID, request, response, now)
			if validationErr != nil {
				result.Trace.Attempts = append(result.Trace.Attempts, traceFor(candidate.identity.ID, request.Capability, validationErr, false))
				return result, validationErr
			}
			result.Response = response
			result.Trace.Attempts = append(result.Trace.Attempts, traceFor(candidate.identity.ID, request.Capability, nil, false))
			result.Trace.ActualProvider = candidate.identity.ID
			result.Trace.Source = response.Meta.Source
			if r.cache != nil && r.cacheTTL > 0 && remainingTTL > 0 {
				cacheTTL := min(r.cacheTTL, remainingTTL)
				r.cache.putUntil(key, response, now.Add(cacheTTL))
			}
			return result, nil
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		err = bindProviderError(candidate.identity.ID, err)
		result.Trace.Attempts = append(result.Trace.Attempts, traceFor(candidate.identity.ID, request.Capability, err, false))
		if !FallbackEligible(err) {
			return result, err
		}
		var providerError *ProviderError
		if errors.As(err, &providerError) && providerError.Kind == ErrorRateLimit && providerError.RetryAfter > 0 {
			r.block(candidate.identity.ID, providerError.RetryAfter)
		}
		lastEligible = err
	}
	if capable == 0 {
		return result, NewError(ErrorUnsupported, "", "dispatch", fmt.Errorf("capability %q has no provider", request.Capability))
	}
	if lastEligible != nil {
		return result, lastEligible
	}
	return result, NewError(ErrorUnavailable, "", "dispatch", errors.New("no provider returned a response"))
}

func (r *Router) supports(provider ProviderID, capability Capability) bool {
	for _, candidate := range r.providers {
		if candidate.identity.ID != provider {
			continue
		}
		_, ok := candidate.capabilities[capability]
		return ok
	}
	return false
}

func (r *Router) normalizeRoutedResponse(
	provider ProviderID,
	request Request,
	response Response,
	now time.Time,
) (Response, time.Duration, error) {
	if response.Capability != request.Capability {
		return Response{}, 0, NewError(ErrorBadPayload, provider, "dispatch", fmt.Errorf("response capability %q does not match request %q", response.Capability, request.Capability))
	}
	if response.Meta.Source.Provider == "" || response.Meta.Source.Provider != provider {
		return Response{}, 0, NewError(ErrorInvalidIdentity, provider, "dispatch", fmt.Errorf("response source provider %q does not match actual provider", response.Meta.Source.Provider))
	}
	normalized, err := NormalizeResponse(response, now, DefaultMaxFutureSkew)
	if err != nil {
		return Response{}, 0, err
	}
	freshness, remainingTTL, err := ResponseFreshness(normalized, now, DefaultMaxFutureSkew)
	if err != nil {
		return Response{}, 0, err
	}
	if freshness != FreshnessFresh {
		kind := ErrorStale
		if freshness == FreshnessFuture {
			kind = ErrorFuture
		}
		return Response{}, 0, NewError(kind, provider, "dispatch", errors.New("normalized response is not usable"))
	}
	for _, quality := range normalized.Meta.Quality {
		var kind ErrorKind
		switch quality {
		case QualityMissing:
			kind = ErrorBadPayload
		case QualityStale:
			kind = ErrorStale
		default:
			continue
		}
		return Response{}, 0, NewError(kind, provider, "dispatch", fmt.Errorf("unsafe quality flag %q", quality))
	}
	return normalized, remainingTTL, nil
}

func bindProviderError(provider ProviderID, err error) error {
	var providerError *ProviderError
	if !errors.As(err, &providerError) {
		return NewError(
			ErrorBadPayload, provider, "dispatch",
			fmt.Errorf("provider returned an untyped contract error: %w", err),
		)
	}
	if providerError.Provider != "" && providerError.Provider != provider {
		return NewError(
			ErrorInvalidIdentity, provider, "dispatch",
			fmt.Errorf("provider error claims source %q", providerError.Provider),
		)
	}
	copy := *providerError
	copy.Provider = provider
	return &copy
}

func traceFor(provider ProviderID, capability Capability, err error, cacheHit bool) AttemptTrace {
	trace := AttemptTrace{Provider: provider, Capability: capability, CacheHit: cacheHit}
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		trace.ErrorKind = providerError.Kind
		trace.RetryAfter = providerError.RetryAfter
	}
	return trace
}

func (r *Router) block(provider ProviderID, retryAfter time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	until := r.clock.Now().UTC().Add(retryAfter)
	if until.After(r.blockedUntil[provider]) {
		r.blockedUntil[provider] = until
	}
}

func (r *Router) rateLimitRemaining(provider ProviderID) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	until, ok := r.blockedUntil[provider]
	if !ok {
		return 0, false
	}
	now := r.clock.Now().UTC()
	if !now.Before(until) {
		delete(r.blockedUntil, provider)
		return 0, false
	}
	return until.Sub(now), true
}

func requestCacheKey(request Request) string {
	var builder strings.Builder
	appendKeyPart := func(value string) {
		builder.WriteString(strconv.Itoa(len(value)))
		builder.WriteByte(':')
		builder.WriteString(value)
		builder.WriteByte('|')
	}
	appendKeyPart(string(request.Capability))
	appendKeyPart(request.Key)
	for _, parameter := range request.Parameters {
		appendKeyPart(parameter.Key)
		appendKeyPart(parameter.Value)
	}
	return builder.String()
}

func routerCacheNamespace(providers []routedProvider, cacheTTL time.Duration) string {
	var builder strings.Builder
	builder.WriteString("router/v1|")
	appendKeyPart := func(value string) {
		builder.WriteString(strconv.Itoa(len(value)))
		builder.WriteByte(':')
		builder.WriteString(value)
		builder.WriteByte('|')
	}
	appendKeyPart(strconv.FormatInt(int64(cacheTTL), 10))
	for _, provider := range providers {
		appendKeyPart(string(provider.identity.ID))
		for _, capability := range provider.identity.Capabilities {
			appendKeyPart(string(capability))
		}
		builder.WriteByte(';')
	}
	return builder.String()
}
