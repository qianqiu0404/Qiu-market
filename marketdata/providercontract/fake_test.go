package providercontract

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManualClock(t *testing.T) {
	start := time.Date(2026, 8, 10, 1, 2, 3, 0, time.FixedZone("fixture", 8*60*60))
	clock := NewManualClock(start)
	require.Equal(t, start.UTC(), clock.Now())
	clock.Advance(90 * time.Second)
	require.Equal(t, start.UTC().Add(90*time.Second), clock.Now())
}

func TestFakeProviderConsumesNormalizedScriptDeterministically(t *testing.T) {
	provider := NewFakeProvider(ProviderIdentity{
		ID: "fixture", Capabilities: []Capability{CapabilitySpotTicker},
	})
	scriptRequest := Request{
		Capability: CapabilitySpotTicker,
		Key:        " BTC-USDT ",
		Parameters: []Parameter{{Key: "venue", Value: " binance "}, {Key: "asset", Value: " BTC "}},
	}
	fetchRequest := Request{
		Capability: CapabilitySpotTicker,
		Key:        "BTC-USDT",
		Parameters: []Parameter{{Key: "asset", Value: "BTC"}, {Key: "venue", Value: "binance"}},
	}
	first := fixtureResponse("fixture", CapabilitySpotTicker, "one")
	secondErr := NewRetryError(ErrorTimeout, "fixture", "fetch", time.Second, context.DeadlineExceeded)
	require.NoError(t, provider.Script(scriptRequest, FakeStep{Response: first}, FakeStep{Err: secondErr}))

	got, err := provider.Fetch(context.Background(), fetchRequest)
	require.NoError(t, err)
	require.Equal(t, first, got)
	_, err = provider.Fetch(context.Background(), fetchRequest)
	require.ErrorIs(t, err, &ProviderError{Kind: ErrorTimeout, Provider: "fixture"})
	require.Zero(t, provider.Remaining(fetchRequest))
	require.Len(t, provider.Calls(), 2)

	_, err = provider.Fetch(context.Background(), fetchRequest)
	require.ErrorIs(t, err, &ProviderError{Kind: ErrorUnsupported, Provider: "fixture"})
}

func TestFakeProviderHonorsCanceledContextWithoutConsumingScript(t *testing.T) {
	provider := NewFakeProvider(ProviderIdentity{
		ID: "fixture", Capabilities: []Capability{CapabilitySpotTicker},
	})
	request := Request{Capability: CapabilitySpotTicker, Key: "btc"}
	require.NoError(t, provider.Script(request, FakeStep{Response: fixtureResponse("fixture", CapabilitySpotTicker, "one")}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.Fetch(ctx, request)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, provider.Remaining(request))
	require.Empty(t, provider.Calls())
}

func fixtureResponse(provider ProviderID, capability Capability, sourceID string) Response {
	observed := time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)
	source := SourceRef{Provider: provider, Key: "fixture", SourceID: sourceID}
	meta := Metadata{
		SchemaVersion: SchemaVersion,
		Source:        source,
		Capability:    capability,
		ObservedAt:    observed,
		ReceivedAt:    observed,
		TTL:           24 * time.Hour,
		Quality:       []QualityFlag{},
	}
	value := any(SpotTickerEnvelope{
		Meta: meta,
		Market: Market{
			ID: "btc-usdt", Code: string(provider) + ":BTC/USDT:spot", Venue: string(provider),
			Base:  Asset{ID: "bitcoin", Symbol: "BTC"},
			Quote: Asset{ID: "tether", Symbol: "USDT"},
			Type:  MarketTypeSpot,
		},
		Data: SpotTicker{
			LastPrice:      DecimalValue{Value: "60000", Unit: UnitQuoteAsset, Scale: 2},
			ProviderSymbol: "BTCUSDT",
		},
	})
	return Response{
		Capability: capability,
		Value:      value,
		Meta:       meta,
	}
}
