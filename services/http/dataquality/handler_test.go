package dataquality

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/quality"
)

type fixedClock time.Time

func (f fixedClock) Now() time.Time { return time.Time(f) }

type fixedReporter struct {
	reports quality.ReportSet
	err     error
}

func (f fixedReporter) Reports() (quality.ReportSet, error) { return f.reports, f.err }

func TestUnconfiguredKeepsThreeSourcesAndAllSixCapabilitiesExplicit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	handler := NewHandler(nil)
	handler.clock = fixedClock(now)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", Path, nil))
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	var body Response
	raw := append([]byte(nil), response.Body.Bytes()...)
	if bytes.Contains(raw, []byte(`"reasons":null`)) || bytes.Contains(raw, []byte(`"dimensions":null`)) || bytes.Contains(raw, []byte(`"errorCounts":null`)) {
		t.Fatalf("nullable collection escaped: %s", raw)
	}
	decodeResponse(t, response, &body)
	if body.Status != "unconfigured" || len(body.Items) != 3 || body.Error != nil {
		t.Fatalf("body=%+v", body)
	}
	capabilityCount := 0
	for _, item := range body.Items {
		if item.Status != quality.StatusInsufficient || item.TradeEligible || item.TechnicalScoreBPS != nil || item.Grade != nil || item.Gate.RecoveryRequired != 3 {
			t.Fatalf("unsafe empty item: %+v", item)
		}
		capabilityCount += len(item.Capabilities)
		for _, capability := range item.Capabilities {
			if capability.Status != quality.StatusInsufficient || capability.SampleCount != 0 || capability.SuccessCount != 0 || capability.MinSamples == 0 || capability.MaxAgeSeconds == 0 {
				t.Fatalf("unsafe empty capability: %+v", capability)
			}
		}
	}
	if capabilityCount != 6 {
		t.Fatalf("capability count=%d", capabilityCount)
	}
}

func TestReportIsSplitByCapabilityWithoutInventingSuccess(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	start := now.Add(-5 * time.Minute)
	window, err := quality.NewEvidenceWindow(start, now, 2, []quality.Evidence{
		{ID: "ticker-ok", Source: quality.SourceBinanceSpot, Capability: quality.CapabilitySpotTicker, At: now.Add(-2 * time.Second), Outcome: quality.OutcomeSuccess},
		{ID: "ticker-timeout", Source: quality.SourceBinanceSpot, Capability: quality.CapabilitySpotTicker, At: now.Add(-time.Second), Outcome: quality.OutcomeTimeout},
	})
	if err != nil {
		t.Fatal(err)
	}
	coverage := uint32(5000)
	score := uint32(7500)
	grade := quality.GradeC
	report := quality.Report{
		Source: quality.SourceBinanceSpot, Window: window,
		Dimensions: []quality.Dimension{{Metric: quality.MetricCoverage, Numerator: 1, Denominator: 2, BPS: &coverage}},
		Capabilities: []quality.CapabilityReport{
			{
				Capability: quality.CapabilitySpotTicker, MinSamples: 2, MaxAge: 5 * time.Second,
				SampleCount: 2, SuccessCount: 1, ValidSampleCount: 1, LastAttemptAt: timePointer(now.Add(-time.Second)),
				LastSuccessAt: timePointer(now.Add(-2 * time.Second)), Age: durationPointer(2 * time.Second),
				Status: quality.StatusDegraded, Reasons: []string{"availability_below_slo"},
			},
			{
				Capability: quality.CapabilityOHLCV, MinSamples: 2, MaxAge: 65 * time.Second,
				Status: quality.StatusInsufficient, Reasons: []string{"capability_min_samples"},
			},
		},
		TechnicalScoreBPS: &score, Grade: &grade, Status: quality.StatusDegraded,
		License: quality.LicenseApproved, Reasons: []string{"availability_below_slo"},
		AttemptCount: 2, SuccessCount: 1, LastAttemptAt: timePointer(now.Add(-time.Second)),
		LastSuccessAt: timePointer(now.Add(-2 * time.Second)), Age: durationPointer(2 * time.Second),
		PriorityCounts: quality.PriorityCounts{P0: 1, P1: 2, P2: 3},
		Gate:           quality.GateState{Source: quality.SourceBinanceSpot, Status: quality.StatusRecovering, HealthyWindowStreak: 1, RecoveryRequired: 3},
	}
	handler := NewHandler(fixedReporter{reports: quality.ReportSet{Reports: []quality.Report{report}}})
	handler.clock = fixedClock(now)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", Path, nil))
	var body Response
	decodeResponse(t, response, &body)
	if body.Status != "degraded" || len(body.Items) != 3 {
		t.Fatalf("body=%+v", body)
	}
	spot := body.Items[0]
	if spot.AttemptCount != 2 || spot.SuccessCount != 1 || spot.AgeSeconds == nil || *spot.AgeSeconds != 2 {
		t.Fatalf("spot=%+v", spot)
	}
	if spot.PriorityCounts.P0 != 1 || spot.PriorityCounts.P1 != 2 || spot.PriorityCounts.P2 != 3 || spot.Gate.Status != quality.StatusRecovering || spot.Gate.HealthyWindowStreak != 1 {
		t.Fatalf("priority/gate=%+v/%+v", spot.PriorityCounts, spot.Gate)
	}
	ticker := spot.Capabilities[0]
	ohlcv := spot.Capabilities[1]
	if ticker.SampleCount != 2 || ticker.ValidSampleCount != 1 || ticker.SuccessCount != 1 || ticker.AgeSeconds == nil || *ticker.AgeSeconds != 2 || ticker.Coverage.Numerator != 1 {
		t.Fatalf("ticker=%+v", ticker)
	}
	if ohlcv.SampleCount != 0 || ohlcv.SuccessCount != 0 || ohlcv.LastSuccessAt != nil || ohlcv.Status != quality.StatusInsufficient {
		t.Fatalf("ohlcv invented success: %+v", ohlcv)
	}
}

func TestFutureAgeIsClampedWhileQuarantineReasonRemainsAuditable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	window, err := quality.NewEvidenceWindow(now.Add(-5*time.Minute), now, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	negativeAge := -time.Second
	future := now.Add(time.Second)
	report := quality.Report{
		Source: quality.SourceBinanceSpot, Window: window, Status: quality.StatusQuarantined,
		License: quality.LicenseApproved, Reasons: []string{"hard_fault:future"},
		LastAttemptAt: &future, LastSuccessAt: &future, Age: &negativeAge, AttemptCount: 1, SuccessCount: 1,
		Capabilities: []quality.CapabilityReport{
			{Capability: quality.CapabilitySpotTicker, MinSamples: 5, MaxAge: 5 * time.Second, SampleCount: 1, ValidSampleCount: 1, SuccessCount: 1, LastAttemptAt: &future, LastSuccessAt: &future, Age: &negativeAge, Status: quality.StatusQuarantined, Reasons: []string{"future"}},
			{Capability: quality.CapabilityOHLCV, MinSamples: 2, MaxAge: 65 * time.Second, Status: quality.StatusInsufficient, Reasons: []string{"min_samples"}},
		},
		Gate: quality.GateState{Source: quality.SourceBinanceSpot, Status: quality.StatusQuarantined, RecoveryRequired: 3, Reasons: []string{"future"}},
	}
	handler := NewHandler(fixedReporter{reports: quality.ReportSet{Reports: []quality.Report{report}}})
	handler.clock = fixedClock(now)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", Path, nil))
	var body Response
	decodeResponse(t, response, &body)
	spot := body.Items[0]
	if spot.AgeSeconds == nil || *spot.AgeSeconds != 0 || spot.Capabilities[0].AgeSeconds == nil || *spot.Capabilities[0].AgeSeconds != 0 {
		t.Fatalf("future ages were not safely clamped: %+v", spot)
	}
	if spot.Status != quality.StatusQuarantined || spot.Gate.Status != quality.StatusQuarantined || spot.Reasons[0] != "hard_fault:future" {
		t.Fatalf("future quarantine was hidden: %+v", spot)
	}
}

func TestReporterErrorFailsClosed(t *testing.T) {
	t.Parallel()
	handler := NewHandler(fixedReporter{err: errors.New("private detail must not escape")})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", Path, nil))
	var body Response
	decodeResponse(t, response, &body)
	if body.Status != "degraded" || body.Error == nil || *body.Error != "quality monitor is unavailable" {
		t.Fatalf("body=%+v", body)
	}
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func durationPointer(value time.Duration) *time.Duration { return &value }
