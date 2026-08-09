//go:build quality_online

package qualityadapters

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
	"github.com/the-web3/s78-market-services/marketdata/providercontract/binancepublic"
	"github.com/the-web3/s78-market-services/marketdata/providercontract/coinglass"
	"github.com/the-web3/s78-market-services/marketdata/quality"
	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
)

var qualitySmoke = flag.Bool("quality-smoke", false, "run four allowlisted read-only quality smoke requests")

type onlineObservationSink struct {
	mu    sync.Mutex
	items []binancepublic.Observation
}

func (s *onlineObservationSink) Observe(value binancepublic.Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, value)
}
func (s *onlineObservationSink) take() (binancepublic.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) != 1 {
		return binancepublic.Observation{}, fmt.Errorf("quality smoke: observations=%d, want one", len(s.items))
	}
	value := s.items[0]
	s.items = nil
	return value, nil
}

func TestOnlineReadOnlyQualitySmoke(t *testing.T) {
	if !*qualitySmoke {
		t.Skip("requires //go:build quality_online and explicit -quality-smoke")
	}
	now := time.Now().UTC()
	monitor, err := quality.NewMonitor(quality.DefaultPolicies(), quality.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	sink := &onlineObservationSink{}
	binance, err := binancepublic.NewReader(binancepublic.Config{Enabled: true, ObservationSink: sink})
	if err != nil {
		t.Fatal(err)
	}
	requests := []struct {
		capability quality.Capability
		fetch      func() (providercontract.DispatchResult, error)
	}{
		{quality.CapabilitySpotTicker, func() (providercontract.DispatchResult, error) { return binance.SpotTicker(t.Context()) }},
		{quality.CapabilityOHLCV, func() (providercontract.DispatchResult, error) { return binance.OHLCV(t.Context()) }},
	}
	liveEvidence := make([]quality.Evidence, 0, 4)
	for index, request := range requests {
		started := time.Now()
		result, fetchErr := request.fetch()
		observation, observationErr := sink.take()
		if observationErr != nil {
			t.Fatal(observationErr)
		}
		id := fmt.Sprintf("online-binance-%d-%d", index, time.Now().UnixNano())
		if err := RecordBinance(monitor, id, time.Now().UTC(), observation, result, fetchErr); err != nil {
			t.Fatal(err)
		}
		evidence, err := BinanceEvidence(id, time.Now().UTC(), observation, result, fetchErr)
		if err != nil {
			t.Fatal(err)
		}
		liveEvidence = append(liveEvidence, evidence)
		t.Logf("source=%s capability=%s attempt=1 success=%t sample=%d window=live latency=%s status=%s reasons=[no_data=%t]", quality.SourceBinanceSpot, request.capability, evidence.Outcome == quality.OutcomeSuccess, boolSample(!evidence.NoData), time.Since(started).Round(time.Millisecond), evidence.Outcome, evidence.NoData)
	}

	research, err := researchsignal.New(researchsignal.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	summary, summaryErr := research.Summary(t.Context())
	if err := RecordResearchSummary(monitor, fmt.Sprintf("online-research-summary-%d", time.Now().UnixNano()), time.Now().UTC(), time.Since(started), summary, summaryErr, true); err != nil {
		t.Fatal(err)
	}
	summaryEvidence, err := ResearchSummaryEvidence("online-summary-log", time.Now().UTC(), time.Since(started), summary, summaryErr, true)
	if err != nil {
		t.Fatal(err)
	}
	liveEvidence = append(liveEvidence, summaryEvidence)
	t.Logf("source=%s capability=%s attempt=1 success=%t sample=%d window=live latency=%s status=%s reasons=[no_data=%t]", quality.SourceXiuqiuResearch, quality.CapabilityResearchSummary, summaryEvidence.Outcome == quality.OutcomeSuccess, boolSample(!summaryEvidence.NoData), time.Since(started).Round(time.Millisecond), summaryEvidence.Outcome, summaryEvidence.NoData)
	started = time.Now()
	events, eventsErr := research.Events(t.Context(), researchsignal.EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 20})
	if err := RecordResearchEvents(monitor, fmt.Sprintf("online-research-events-%d", time.Now().UnixNano()), time.Now().UTC(), time.Since(started), events, eventsErr, true); err != nil {
		t.Fatal(err)
	}
	eventsEvidence, err := ResearchEventsEvidence("online-events-log", time.Now().UTC(), time.Since(started), events, eventsErr, true)
	if err != nil {
		t.Fatal(err)
	}
	liveEvidence = append(liveEvidence, eventsEvidence)
	t.Logf("source=%s capability=%s attempt=1 success=%t sample=%d window=live latency=%s status=%s reasons=[no_data=%t]", quality.SourceXiuqiuResearch, quality.CapabilityResearchEvents, eventsEvidence.Outcome == quality.OutcomeSuccess, boolSample(!eventsEvidence.NoData), time.Since(started).Round(time.Millisecond), eventsEvidence.Outcome, eventsEvidence.NoData)

	for index, fixturePath := range []string{"../providercontract/coinglass/testdata/open_interest.json", "../providercontract/coinglass/testdata/liquidation.json"} {
		body, err := os.ReadFile(fixturePath)
		if err != nil {
			t.Fatalf("CoinGlass fixture %d invalid: %v", index, err)
		}
		capability := []quality.Capability{quality.CapabilityOpenInterest, quality.CapabilityLiquidation}[index]
		operation := []string{coinglass.OperationOpenInterestHistory, coinglass.OperationLiquidationHistory}[index]
		result, err := onlineCoinGlassResult(now, capability, body)
		if err != nil {
			t.Fatal(err)
		}
		if err := RecordCoinGlass(monitor, fmt.Sprintf("fixture-coinglass-%d", index), now, coinglass.Observation{
			Provider: coinglass.ProviderID, Operation: operation, Capability: providercontract.CapabilityDerivatives,
			Outcome: "success", Duration: time.Millisecond,
		}, result, nil); err != nil {
			t.Fatal(err)
		}
		t.Logf("source=%s capability=%s attempt=1 success=true sample=1 window=fixture status=not_live", quality.SourceCoinGlassDerivative, capability)
	}
	liveSuccess, liveSamples := smokeEvidenceCounts(liveEvidence)
	t.Logf("source=all capability=quality_smoke attempt=%d success=%d live_sample=%d fixture_sample=2 window=live_plus_fixture status=read_only reasons=[binance_max_get_2 xiuqiu_logical_get_2 xiuqiu_transport_retry_max_4 coinglass_get_0]", len(liveEvidence), liveSuccess, liveSamples)

	set, err := monitor.Reports()
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range set.Reports {
		t.Logf("source=%s attempt=%d success=%d sample=%d window=%s age=%v score=%v grade=%v status=%s reasons=%v", report.Source, report.AttemptCount, report.SuccessCount, report.Window.SampleCount, report.Window.Duration, nullableDuration(report.Age), nullableUint32(report.TechnicalScoreBPS), nullableGrade(report.Grade), report.Status, report.Reasons)
	}
}

func smokeEvidenceCounts(items []quality.Evidence) (success, samples int) {
	for _, item := range items {
		if item.Outcome == quality.OutcomeSuccess {
			success++
			if !item.NoData {
				samples++
			}
		}
	}
	return success, samples
}

func nullableDuration(value *time.Duration) any {
	if value == nil {
		return nil
	}
	return value.String()
}
func boolSample(value bool) int {
	if value {
		return 1
	}
	return 0
}
func nullableUint32(value *uint32) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableGrade(value *quality.Grade) any {
	if value == nil {
		return nil
	}
	return string(*value)
}

type onlineOIRow struct {
	Time                   int64 `json:"time"`
	Open, High, Low, Close string
}
type onlineLiquidationRow struct {
	Time  int64  `json:"time"`
	Long  string `json:"long_liquidation_usd"`
	Short string `json:"short_liquidation_usd"`
}
type onlineOIEnvelope struct {
	Code    string        `json:"code"`
	Message string        `json:"msg"`
	Data    []onlineOIRow `json:"data"`
}
type onlineLiquidationEnvelope struct {
	Code    string                 `json:"code"`
	Message string                 `json:"msg"`
	Data    []onlineLiquidationRow `json:"data"`
}

func onlineCoinGlassResult(now time.Time, capability quality.Capability, body []byte) (providercontract.DispatchResult, error) {
	operationKey, endpoint := "open-interest-history-4h", "/api/futures/open-interest/history"
	data := providercontract.DerivativeSnapshot{}
	var eventMillis int64
	if capability == quality.CapabilityOpenInterest {
		var fixture onlineOIEnvelope
		if err := decodeOnlineFixture(body, &fixture); err != nil || fixture.Code != "0" || fixture.Message != "success" || len(fixture.Data) != 2 {
			return providercontract.DispatchResult{}, fmt.Errorf("invalid CoinGlass OI fixture: %w", err)
		}
		latest := fixture.Data[len(fixture.Data)-1]
		eventMillis = latest.Time
		value := providercontract.DecimalValue{Value: latest.Close, Unit: providercontract.UnitUSD, Scale: 3}
		data.OpenInterest = &value
	} else {
		operationKey, endpoint = "liquidation-history-4h", "/api/futures/liquidation/history"
		var fixture onlineLiquidationEnvelope
		if err := decodeOnlineFixture(body, &fixture); err != nil || fixture.Code != "0" || fixture.Message != "success" || len(fixture.Data) != 2 {
			return providercontract.DispatchResult{}, fmt.Errorf("invalid CoinGlass liquidation fixture: %w", err)
		}
		latest := fixture.Data[len(fixture.Data)-1]
		eventMillis = latest.Time
		longValue := providercontract.DecimalValue{Value: latest.Long, Unit: providercontract.UnitUSD, Scale: 5}
		shortValue := providercontract.DecimalValue{Value: latest.Short, Unit: providercontract.UnitUSD, Scale: 5}
		window := int64(4 * time.Hour / time.Second)
		data.LongLiquidations, data.ShortLiquidations, data.LiquidationWindowSec = &longValue, &shortValue, &window
	}
	eventTime := time.UnixMilli(eventMillis).UTC()
	meta := providercontract.Metadata{
		SchemaVersion: providercontract.SchemaVersion,
		Source: providercontract.SourceRef{Provider: coinglass.ProviderID, Key: operationKey,
			SourceID: fmt.Sprintf("endpoint=%s;exchange=Binance;instrument=BTCUSD_PERP;settlement=USDT;time=%d", endpoint, eventTime.UnixMilli())},
		Capability: providercontract.CapabilityDerivatives, ObservedAt: now, EventTime: &eventTime, ReceivedAt: now,
		TTL: 5 * time.Hour, Quality: []providercontract.QualityFlag{providercontract.QualityDerived, providercontract.QualityPartial},
	}
	envelope := providercontract.DerivativeSnapshotEnvelope{
		Meta: meta, Market: providercontract.Market{ID: "market-btc-usd-perp", Venue: "binance",
			Base: providercontract.Asset{ID: "bitcoin", Symbol: "BTC"}, Quote: providercontract.Asset{ID: "usd", Symbol: "USD"}, Type: providercontract.MarketTypePerp}, Data: data,
	}
	return providercontract.DispatchResult{
		Response: providercontract.Response{Capability: providercontract.CapabilityDerivatives, Value: envelope, Meta: meta},
		Trace: providercontract.DispatchTrace{ActualProvider: coinglass.ProviderID, Source: meta.Source,
			Attempts: []providercontract.AttemptTrace{{Provider: coinglass.ProviderID, Capability: providercontract.CapabilityDerivatives}}},
	}, nil
}

func decodeOnlineFixture(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing fixture JSON")
	}
	return nil
}
