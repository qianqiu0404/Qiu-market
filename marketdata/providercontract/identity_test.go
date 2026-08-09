package providercontract

import (
	"reflect"
	"testing"
)

func TestNormalizeIdentityBuildsAuditableMarketCode(t *testing.T) {
	market, err := NormalizeMarket(Market{
		ID:    "market-btc-usdt",
		Venue: " Binance ",
		Base:  Asset{ID: "BITCOIN", Symbol: "btc"},
		Quote: Asset{ID: "TETHER", Symbol: "usdt"},
		Type:  MarketTypeSpot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if market.Code != "binance:BTC/USDT:spot" || market.Base.ID != "bitcoin" ||
		market.Quote.ID != "tether" {
		t.Fatalf("market was not canonicalized: %+v", market)
	}

	market.Code = "binance:BTC/USDT:perp"
	if _, err := NormalizeMarket(market); kind(err) != ErrorConflict {
		t.Fatalf("conflicting market type error = %v", err)
	}
}

func TestNormalizeSourceRequiresAuditableReference(t *testing.T) {
	_, err := NormalizeSourceRef(SourceRef{Provider: "binance", Key: "spot-tickers"})
	if kind(err) != ErrorInvalidIdentity {
		t.Fatalf("missing reference error = %v", err)
	}

	source, err := NormalizeSourceRef(SourceRef{
		Provider: " BINANCE ", Key: "SPOT-TICKERS",
		URL: "https://api.binance.com/api/v3/ticker#discarded",
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.Provider != "binance" || source.Key != "spot-tickers" ||
		source.URL != "https://api.binance.com/api/v3/ticker" {
		t.Fatalf("source = %+v", source)
	}
}

func TestNormalizeCapabilitiesAndRequestAreDeterministic(t *testing.T) {
	capabilities, err := NormalizeCapabilities([]Capability{
		CapabilitySignals, CapabilitySpotTicker, CapabilitySignals,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capabilities, []Capability{CapabilitySignals, CapabilitySpotTicker}) {
		t.Fatalf("capabilities = %v", capabilities)
	}

	request, err := NormalizeRequest(Request{
		Capability: CapabilityOHLCV,
		Key:        "btc-history",
		Parameters: []Parameter{{Key: "venue", Value: "binance"}, {Key: "asset", Value: "bitcoin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Parameters[0].Key != "asset" || request.Parameters[1].Key != "venue" {
		t.Fatalf("parameters are not sorted: %+v", request.Parameters)
	}
	request.Parameters = append(request.Parameters, Parameter{Key: "asset", Value: "btc"})
	if _, err := NormalizeRequest(request); kind(err) != ErrorConflict {
		t.Fatalf("duplicate parameter error = %v", err)
	}
}

func kind(err error) ErrorKind {
	value, _ := ErrorKindOf(err)
	return value
}
