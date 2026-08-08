package database

import (
	"testing"
	"time"
)

func TestPersonalServerKlineRetentionPolicies(t *testing.T) {
	t.Parallel()
	policies := PersonalServerKlineRetentionPolicies()
	if err := validateKlineRetentionPolicies(policies); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]time.Duration, len(policies))
	for _, policy := range policies {
		got[policy.Interval] = policy.Keep
	}
	if got["1m"] != 30*24*time.Hour {
		t.Fatalf("1m retention = %s", got["1m"])
	}
	if got["15m"] != 180*24*time.Hour {
		t.Fatalf("15m retention = %s", got["15m"])
	}
	if got["1h"] != 2*365*24*time.Hour {
		t.Fatalf("1h retention = %s", got["1h"])
	}
	if _, exists := got["1d"]; exists {
		t.Fatal("1d K-lines must be retained indefinitely")
	}
}

func TestValidateKlineRetentionPoliciesRejectsUnsafeInput(t *testing.T) {
	t.Parallel()
	for _, policies := range [][]KlineRetentionPolicy{
		{{Interval: "1d", Keep: time.Hour}},
		{{Interval: "1m", Keep: 0}},
		{{Interval: "1m", Keep: time.Hour}, {Interval: "1m", Keep: 2 * time.Hour}},
	} {
		if err := validateKlineRetentionPolicies(policies); err == nil {
			t.Fatalf("policies unexpectedly accepted: %+v", policies)
		}
	}
}

func TestRetainedKlinesPreventsExpiredRowsFromReturning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	rows := []SymbolKline{
		{Guid: "old-1m", Interval: "1m", OpenTime: now.Add(-30*24*time.Hour - time.Minute)},
		{Guid: "new-1m", Interval: "1m", OpenTime: now.Add(-30 * 24 * time.Hour)},
		{Guid: "old-15m", Interval: "15m", OpenTime: now.Add(-181 * 24 * time.Hour)},
		{Guid: "old-1h", Interval: "1h", OpenTime: now.Add(-(2*365*24*time.Hour + time.Hour))},
		{Guid: "old-1d", Interval: "1d", OpenTime: now.Add(-10 * 365 * 24 * time.Hour)},
	}
	retained := RetainedKlines(rows, now)
	if len(retained) != 2 || retained[0].Guid != "new-1m" || retained[1].Guid != "old-1d" {
		t.Fatalf("retained = %+v", retained)
	}
}
