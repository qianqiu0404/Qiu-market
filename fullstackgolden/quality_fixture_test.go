package fullstackgolden

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
	"github.com/the-web3/s78-market-services/marketdata/quality"
	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
)

func TestQualityHarnessUsesRealAdaptersForFaultAndRecoveryMatrix(t *testing.T) {
	fixture := &fixtureServer{researchScenario: "fresh", providerScenario: "healthy", logicalNow: time.Now().UTC()}
	server := httptest.NewUnstartedServer(loopbackOnly(fixture))
	server.StartTLS()
	t.Cleanup(server.Close)
	certificate, err := x509.ParseCertificate(server.Certificate().Raw)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	clock := newMutableClock(time.Now().UTC().Add(-120 * 24 * time.Hour))
	research, err := researchsignal.NewLoopbackGoldenFixtureReader(server.URL, caPEM, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	harness, err := newQualityHarness(server.URL, caPEM, clock, research)
	if err != nil {
		t.Fatal(err)
	}
	setScenario := func(_ context.Context, domain, scenario string, at time.Time) error {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.logicalNow = at.UTC()
		if domain == "provider" {
			fixture.providerScenario = scenario
		} else {
			fixture.researchScenario = scenario
		}
		return nil
	}

	wants := map[string]struct {
		source     quality.SourceKind
		kind       providercontract.ErrorKind
		status     int
		retryAfter int64
		hardFault  quality.HardFault
	}{
		"binance_429":   {quality.SourceBinanceSpot, providercontract.ErrorRateLimit, 429, 1, ""},
		"coinglass_5xx": {quality.SourceCoinGlassDerivative, providercontract.ErrorUpstream5xx, 502, 0, ""},
		"timeout":       {quality.SourceBinanceSpot, providercontract.ErrorTimeout, 0, 0, ""},
		"stale":         {quality.SourceBinanceSpot, providercontract.ErrorStale, 200, 0, quality.HardFaultStale},
		"future":        {quality.SourceBinanceSpot, providercontract.ErrorFuture, 200, 0, quality.HardFaultFuture},
		"conflict":      {quality.SourceBinanceSpot, providercontract.ErrorConflict, 200, 0, quality.HardFaultConflict},
	}
	for scenario, want := range wants {
		evidence, err := harness.recordWindow(context.Background(), scenario, setScenario)
		if err != nil {
			t.Fatalf("%s: %v", scenario, err)
		}
		fault := faultForSource(t, evidence, want.source)
		if fault.NormalizedErrorKind != want.kind || fault.HTTPStatus != want.status || fault.RetryAfterSeconds != want.retryAfter {
			t.Fatalf("%s fault=%+v", scenario, fault)
		}
		if want.hardFault != "" && !containsHardFault(fault.HardFaults, want.hardFault) {
			t.Fatalf("%s missing hard fault %s: %+v", scenario, want.hardFault, fault)
		}
	}

	cache, err := harness.recordWindow(context.Background(), "cache_hit", setScenario)
	if err != nil {
		t.Fatal(err)
	}
	noData, err := harness.recordWindow(context.Background(), "no_data", setScenario)
	if err != nil {
		t.Fatal(err)
	}
	if sourceFor(t, cache, quality.SourceBinanceSpot).HealthyWindowStreak != 0 || sourceFor(t, noData, quality.SourceBinanceSpot).HealthyWindowStreak != 0 {
		t.Fatalf("cache/no-data advanced recovery: cache=%+v noData=%+v", cache, noData)
	}
	for sequence := uint32(1); sequence <= 3; sequence++ {
		recovered, err := harness.recordWindow(context.Background(), "recover", setScenario)
		if err != nil {
			t.Fatal(err)
		}
		source := sourceFor(t, recovered, quality.SourceBinanceSpot)
		if source.HealthyWindowStreak != sequence || !source.GoldenLicenseAssumption || source.OriginalLicense != quality.LicenseUnknown {
			t.Fatalf("recovery window %d=%+v", sequence, source)
		}
		if sequence < 3 && source.Status != quality.StatusRecovering {
			t.Fatalf("recovery window %d status=%s", sequence, source.Status)
		}
		if sequence == 3 && source.Status != quality.StatusHealthy {
			t.Fatalf("third recovery status=%s", source.Status)
		}
	}
}

func TestResearchFixtureSatisfiesStrictSourceContract(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fixture := &fixtureServer{researchScenario: "fresh", providerScenario: "healthy", logicalNow: now}
	server := httptest.NewUnstartedServer(loopbackOnly(fixture))
	server.StartTLS()
	t.Cleanup(server.Close)
	certificate, err := x509.ParseCertificate(server.Certificate().Raw)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	reader, err := researchsignal.NewLoopbackGoldenFixtureReader(server.URL, caPEM, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != researchsignal.StatusFresh || len(result.Data.Sources) != 5 {
		t.Fatalf("strict research summary=%+v", result)
	}
	for _, source := range result.Data.Sources {
		if source.Status != researchsignal.SourceHealthy {
			t.Fatalf("source %s health=%s", source.Source, source.Status)
		}
	}
}

func faultForSource(t *testing.T, window QualityWindowEvidence, source quality.SourceKind) QualityFaultEvidence {
	t.Helper()
	for _, fault := range window.Faults {
		if fault.Source == source {
			return fault
		}
	}
	t.Fatalf("missing %s fault: %+v", source, window)
	return QualityFaultEvidence{}
}

func sourceFor(t *testing.T, window QualityWindowEvidence, source quality.SourceKind) QualitySourceEvidence {
	t.Helper()
	for _, item := range window.Sources {
		if item.Source == source {
			return item
		}
	}
	t.Fatalf("missing %s report: %+v", source, window)
	return QualitySourceEvidence{}
}

func containsHardFault(values []quality.HardFault, want quality.HardFault) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
