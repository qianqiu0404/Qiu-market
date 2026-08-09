package providercontract

import (
	"errors"
	"testing"
	"time"
)

func TestProviderErrorTaxonomyAndFallbackEligibility(t *testing.T) {
	cause := errors.New("provider busy")
	rateLimit := NewRetryError(ErrorRateLimit, "binance", "ticker", 3*time.Second, cause)
	if !rateLimit.Retryable() || !FallbackEligible(rateLimit) {
		t.Fatal("rate limit must be retryable and fallback eligible")
	}
	if rateLimit.RetryAfter != 3*time.Second || !errors.Is(rateLimit, cause) {
		t.Fatalf("rate-limit details were not preserved: %+v", rateLimit)
	}
	if !errors.Is(rateLimit, &ProviderError{Kind: ErrorRateLimit}) {
		t.Fatal("typed errors must support errors.Is by kind")
	}
	if FallbackEligible(NewError(ErrorBadPayload, "binance", "ticker", cause)) {
		t.Fatal("bad payload must fail closed")
	}
	if FallbackEligible(NewError(ErrorStale, "binance", "ticker", cause)) {
		t.Fatal("stale data must not be a default fallback/cache success")
	}
	if !FallbackEligible(NewError(ErrorUnsupported, "binance", "ticker", nil)) {
		t.Fatal("unsupported capability must allow deterministic fallback")
	}
}

func TestMetadataFreshnessUsesCallerClock(t *testing.T) {
	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	meta := Metadata{ObservedAt: now.Add(-time.Second), TTL: 2 * time.Second}
	if got := meta.Freshness(now); got != FreshnessFresh {
		t.Fatalf("freshness = %q", got)
	}
	if got := meta.Freshness(now.Add(2 * time.Second)); got != FreshnessStale {
		t.Fatalf("freshness = %q", got)
	}
	meta.ObservedAt = now.Add(time.Nanosecond)
	if got := meta.Freshness(now); got != FreshnessFuture {
		t.Fatalf("freshness = %q", got)
	}
}
