package main

import (
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/quality"
)

func TestGoldenMonitorHasThreeSourcesSixCapabilitiesAndNoTradingEligibility(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	monitor, err := goldenMonitor(now)
	if err != nil {
		t.Fatal(err)
	}
	set, err := monitor.Reports()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Reports) != 3 {
		t.Fatalf("reports=%d", len(set.Reports))
	}
	capabilities := 0
	for _, report := range set.Reports {
		capabilities += len(report.Capabilities)
		if report.TradeEligible || report.MatcherEligible || report.LedgerEligible {
			t.Fatalf("unsafe report=%+v", report)
		}
	}
	if capabilities != 6 {
		t.Fatalf("capabilities=%d", capabilities)
	}
	if set.Reports[0].Source != quality.SourceBinanceSpot || set.Reports[0].Status != quality.StatusHealthy {
		t.Fatalf("Binance report=%+v", set.Reports[0])
	}
	if set.Reports[1].Status != quality.StatusQuarantined || set.Reports[2].Status != quality.StatusQuarantined {
		t.Fatalf("license gates were bypassed: %+v", set.Reports)
	}
}
