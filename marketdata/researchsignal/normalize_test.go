package researchsignal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixtureEvent(now time.Time) upstreamEvent {
	watch := "Confirm the next official release."
	return upstreamEvent{
		ID: "btc-event-1", Slug: "btc-event-1", Market: "crypto", Priority: "P1",
		Score: json.Number("72"), TitleZH: "BTC research event", SummaryZH: "A reviewed, non-executable research summary.",
		WhyItMattersZH: "This may change market attention.", WatchFor: &watch,
		EventType: "release", NewsDirection: "neutral", SystemJudgment: "Monitor official confirmation.", Horizon: "days",
		OccurredAt: now.Add(-time.Hour).Format(time.RFC3339), PublishedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
		SourceCount: 2,
		Sources:     []upstreamSource{{Name: "GitHub", URL: "https://github.com/example/release"}, {Name: "Federal Reserve", URL: "https://www.federalreserve.gov/newsevents.htm"}},
		Assets:      []upstreamAsset{{Namespace: "crypto", Symbol: "BTC", Relevance: json.Number("1")}},
	}
}

func TestNormalizeEventCanonicalAuditBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	first, err := normalizeEvent(fixtureEvent(now), now, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeEvent(fixtureEvent(now), now.Add(time.Second), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if first.ObservedAt != nil || first.Executable || first.Provider != Provider {
		t.Fatalf("invalid canonical boundary: %+v", first)
	}
	if first.Source != "xiuqiu-site Market Radar" || first.SourceURL != productionOrigin+"/market-radar/events/btc-event-1" {
		t.Fatalf("unexpected publisher audit source: %+v", first)
	}
	if first.ContentHash != second.ContentHash || first.ReceivedAt == second.ReceivedAt {
		t.Fatalf("receipt time must not change content identity: first=%+v second=%+v", first, second)
	}
	if !strings.Contains(strings.Join(first.QualityFlags, ","), "observed_time_missing") {
		t.Fatalf("missing observation boundary: %+v", first.QualityFlags)
	}
}

func TestNormalizeEventsDeduplicatesAndSuppressesConflict(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	duplicate := fixtureEvent(now)
	items, partial, err := normalizeEvents([]upstreamEvent{duplicate, duplicate}, now, now)
	if err != nil || !partial || len(items) != 1 || !strings.Contains(strings.Join(items[0].QualityFlags, ","), "duplicate") {
		t.Fatalf("duplicate result items=%+v partial=%v err=%v", items, partial, err)
	}
	conflict := fixtureEvent(now)
	conflict.TitleZH = "Conflicting version"
	items, partial, err = normalizeEvents([]upstreamEvent{duplicate, conflict}, now, now)
	if err != nil || !partial || len(items) != 0 {
		t.Fatalf("conflict must suppress every version: items=%+v partial=%v err=%v", items, partial, err)
	}
}

func TestNormalizeEventValidatesIgnoredUpstreamSchema(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	tests := map[string]func(*upstreamEvent){
		"missing BTC identity": func(event *upstreamEvent) { event.Assets[0].Symbol = "ETH" },
		"wrong market":         func(event *upstreamEvent) { event.Market = "macro" },
		"invalid horizon":      func(event *upstreamEvent) { event.Horizon = "forever" },
		"source count drift":   func(event *upstreamEvent) { event.SourceCount = 1 },
		"future event":         func(event *upstreamEvent) { event.OccurredAt = now.Add(time.Minute).Format(time.RFC3339) },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			event := fixtureEvent(now)
			mutate(&event)
			if _, err := normalizeEvent(event, now, now); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestNormalizeEventMarksMissingVerificationFieldsLegacy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	event := fixtureEvent(now)
	event.WatchFor = nil
	event.Invalidation = nil
	item, err := normalizeEvent(event, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(item.QualityFlags, ","), "legacy_fields_missing") {
		t.Fatalf("quality flags = %v", item.QualityFlags)
	}
}
