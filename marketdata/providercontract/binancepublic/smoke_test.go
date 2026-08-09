//go:build online

package binancepublic

import (
	"context"
	"flag"
	"sync"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

var runOnlineSmoke = flag.Bool(
	"binance-public-online",
	false,
	"perform two read-only requests to the allowlisted Binance public origin",
)

type smokeObservationSink struct {
	mu     sync.Mutex
	values []Observation
}

func (s *smokeObservationSink) Observe(value Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = append(s.values, value)
}

func (s *smokeObservationSink) Values() []Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Observation(nil), s.values...)
}

func TestBinancePublicOnlineSmoke(t *testing.T) {
	if !*runOnlineSmoke {
		t.Skipf("provider=%s capability=smoke status=disabled", ProviderID)
	}

	sink := &smokeObservationSink{}
	// Separate clients prevent net/http from transparently retrying the second
	// GET on a previously used keep-alive connection.
	tickerReader, err := NewReader(Config{Enabled: true, ObservationSink: sink})
	if err != nil {
		t.Fatalf("provider=%s capability=reader status=%s", ProviderID, smokeErrorStatus(err))
	}
	ohlcvReader, err := NewReader(Config{Enabled: true, ObservationSink: sink})
	if err != nil {
		t.Fatalf("provider=%s capability=reader status=%s", ProviderID, smokeErrorStatus(err))
	}
	consumer := providercontract.NewDefaultConsumer()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	tickerResult, err := tickerReader.SpotTicker(ctx)
	if err != nil {
		t.Fatalf("provider=%s capability=%s status=%s", ProviderID, providercontract.CapabilitySpotTicker, smokeErrorStatus(err))
	}
	ticker, err := consumer.NormalizeDispatch(tickerResult, time.Now().UTC())
	if err != nil {
		t.Fatalf("provider=%s capability=%s status=%s", ProviderID, providercontract.CapabilitySpotTicker, smokeErrorStatus(err))
	}
	if !validSmokeTicker(ticker) {
		t.Fatalf("provider=%s capability=%s status=invalid", ProviderID, providercontract.CapabilitySpotTicker)
	}

	ohlcvResult, err := ohlcvReader.OHLCV(ctx)
	if err != nil {
		t.Fatalf("provider=%s capability=%s status=%s", ProviderID, providercontract.CapabilityOHLCV, smokeErrorStatus(err))
	}
	ohlcv, err := consumer.NormalizeDispatch(ohlcvResult, time.Now().UTC())
	if err != nil {
		t.Fatalf("provider=%s capability=%s status=%s", ProviderID, providercontract.CapabilityOHLCV, smokeErrorStatus(err))
	}
	if !validSmokeOHLCV(ohlcv) {
		t.Fatalf("provider=%s capability=%s status=invalid", ProviderID, providercontract.CapabilityOHLCV)
	}

	observations := sink.Values()
	if len(observations) != 2 ||
		observations[0].Operation != OperationTicker24h ||
		observations[1].Operation != OperationKlines ||
		observations[0].StatusCode != 200 ||
		observations[1].StatusCode != 200 {
		t.Fatalf("provider=%s capability=smoke count=%d status=invalid", ProviderID, len(observations))
	}
	t.Logf(
		"provider=%s capability=%s count=1 status=%d freshness=%s latency=%s",
		ProviderID,
		providercontract.CapabilitySpotTicker,
		observations[0].StatusCode,
		ticker.Envelope.Freshness,
		observations[0].Duration.Round(time.Millisecond),
	)
	t.Logf(
		"provider=%s capability=%s count=%d status=%d freshness=%s latency=%s",
		ProviderID,
		providercontract.CapabilityOHLCV,
		len(ohlcv.Envelope.OHLCV.Data),
		observations[1].StatusCode,
		ohlcv.Envelope.Freshness,
		observations[1].Duration.Round(time.Millisecond),
	)
}

func validSmokeTicker(value providercontract.NormalizedDispatch) bool {
	envelope := value.Envelope
	return envelope.Usable &&
		envelope.Freshness == providercontract.FreshnessFresh &&
		envelope.Source.Provider == ProviderID &&
		envelope.SpotTicker != nil &&
		envelope.SpotTicker.Market.Code == "binance:BTC/USDT:spot" &&
		envelope.SpotTicker.Data.ProviderSymbol == providerSymbol &&
		envelope.SpotTicker.Data.LastPrice.Unit == providercontract.UnitQuoteAsset &&
		envelope.SpotTicker.Data.LastPrice.Scale >= 0 &&
		envelope.SpotTicker.Data.LastPrice.Scale <= providercontract.MaximumDecimalScale
}

func validSmokeOHLCV(value providercontract.NormalizedDispatch) bool {
	envelope := value.Envelope
	if !envelope.Usable ||
		envelope.Freshness != providercontract.FreshnessFresh ||
		envelope.Source.Provider != ProviderID ||
		envelope.OHLCV == nil ||
		envelope.OHLCV.Market.Code != "binance:BTC/USDT:spot" ||
		envelope.OHLCV.Interval != "1m" ||
		len(envelope.OHLCV.Data) == 0 || len(envelope.OHLCV.Data) > 10 {
		return false
	}
	for index, candle := range envelope.OHLCV.Data {
		if candle.Open.Unit != providercontract.UnitQuoteAsset ||
			candle.High.Unit != providercontract.UnitQuoteAsset ||
			candle.Low.Unit != providercontract.UnitQuoteAsset ||
			candle.Close.Unit != providercontract.UnitQuoteAsset ||
			candle.Volume.Unit != providercontract.UnitBaseAsset ||
			!candle.CloseTime.Equal(candle.OpenTime.Add(time.Minute)) ||
			(index > 0 && !candle.OpenTime.After(envelope.OHLCV.Data[index-1].OpenTime)) {
			return false
		}
	}
	return true
}

func smokeErrorStatus(err error) string {
	if kind, ok := providercontract.ErrorKindOf(err); ok {
		return string(kind)
	}
	if err == context.Canceled {
		return "canceled"
	}
	if err == context.DeadlineExceeded {
		return "timeout"
	}
	return "untyped"
}
