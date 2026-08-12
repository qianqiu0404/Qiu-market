package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSpotCatalogAdaptersNormalizeProviderSpecificPayloads(t *testing.T) {
	tests := []struct {
		name     string
		response string
		build    func(*http.Client, string) CatalogAdapter
	}{
		{
			name:     "binance",
			response: `{"symbols":[{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","isSpotTradingAllowed":true}]}`,
			build: func(client *http.Client, endpoint string) CatalogAdapter {
				return &BinanceSpotAdapter{client: client, baseURL: endpoint}
			},
		},
		{
			name:     "coinbase",
			response: `[{"id":"BTC-USDC","base_currency":"BTC","quote_currency":"USDC","status":"online","trading_disabled":false,"cancel_only":false,"post_only":false}]`,
			build: func(client *http.Client, endpoint string) CatalogAdapter {
				return &CoinbaseSpotAdapter{client: client, baseURL: endpoint}
			},
		},
		{
			name:     "bybit",
			response: `{"retCode":0,"retMsg":"OK","result":{"nextPageCursor":"","list":[{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"Trading"}]}}`,
			build: func(client *http.Client, endpoint string) CatalogAdapter {
				return &BybitSpotAdapter{client: client, baseURL: endpoint}
			},
		},
		{
			name:     "okx",
			response: `{"code":"0","msg":"","data":[{"instId":"BTC-USDT","baseCcy":"BTC","quoteCcy":"USDT","state":"live"}]}`,
			build: func(client *http.Client, endpoint string) CatalogAdapter {
				return &OKXSpotAdapter{client: client, baseURL: endpoint}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.response))
			}))
			defer server.Close()

			items, err := test.build(server.Client(), server.URL).Discover(context.Background())
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, "BTC", items[0].BaseAlias)
			require.True(t, items[0].Tradable)
		})
	}
}

func TestDefaultProviderEndpointsKeepBinanceMarketDataOnlyAndBybitV5(t *testing.T) {
	require.Equal(t, "https://data-api.binance.vision", binanceMarketDataRESTBaseURL)
	require.Equal(t, "wss://data-stream.binance.vision:443/ws", binanceMarketDataWebSocketURL)
	require.Equal(t, "https://api.bybit.com", bybitV5RESTBaseURL)
	require.Equal(t, "wss://stream.bybit.com/v5/public/spot", bybitV5SpotWebSocketURL)
	require.Equal(t, binanceMarketDataRESTBaseURL, NewBinanceSpotAdapter(http.DefaultClient).baseURL)
	require.Equal(t, bybitV5RESTBaseURL, NewBybitSpotAdapter(http.DefaultClient).baseURL)
}

func TestBybitSpotCatalogDoesNotUseUnsupportedPagination(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		require.Equal(t, "spot", request.URL.Query().Get("category"))
		require.Empty(t, request.URL.Query().Get("cursor"))
		require.Empty(t, request.URL.Query().Get("limit"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(
			`{"retCode":0,"retMsg":"OK","result":{"nextPageCursor":"ignored-for-spot","list":[{"symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"Trading"}]}}`,
		))
	}))
	defer server.Close()

	items, err := (&BybitSpotAdapter{client: server.Client(), baseURL: server.URL}).
		Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, 1, requestCount)
}

func TestProviderHTTPBoundaryRejectsRateLimitAndMalformedJSON(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"rate limit": func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "slow down", http.StatusTooManyRequests)
		},
		"malformed": func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"broken"`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			var target map[string]any
			err := getProviderJSON(context.Background(), server.Client(), server.URL, &target)
			require.Error(t, err)
			if name == "rate limit" {
				require.Equal(t, http.StatusTooManyRequests, httpStatusFromError(err))
			}
		})
	}
}

func TestRestrictedProviderHTTPStatusPropagatesAcrossCatalogTickerAndKline(t *testing.T) {
	providers := []struct {
		name    string
		catalog func(*http.Client, string) CatalogAdapter
		ticker  func(*http.Client, string) spotTickerBatchAdapter
		kline   func(*http.Client, string) cexKlineAdapter
	}{
		{
			name: "binance",
			catalog: func(client *http.Client, endpoint string) CatalogAdapter {
				return &BinanceSpotAdapter{client: client, baseURL: endpoint}
			},
			ticker: func(client *http.Client, endpoint string) spotTickerBatchAdapter {
				return &binanceBatchTickerAdapter{client: client, baseURL: endpoint}
			},
			kline: func(client *http.Client, endpoint string) cexKlineAdapter {
				return &binanceKlineAdapter{baseCEXKlineAdapter{client: client, baseURL: endpoint}}
			},
		},
		{
			name: "bybit",
			catalog: func(client *http.Client, endpoint string) CatalogAdapter {
				return &BybitSpotAdapter{client: client, baseURL: endpoint}
			},
			ticker: func(client *http.Client, endpoint string) spotTickerBatchAdapter {
				return &bybitBatchTickerAdapter{client: client, baseURL: endpoint}
			},
			kline: func(client *http.Client, endpoint string) cexKlineAdapter {
				return &bybitKlineAdapter{baseCEXKlineAdapter{client: client, baseURL: endpoint}}
			},
		},
	}
	for _, status := range []int{http.StatusForbidden, http.StatusUnavailableForLegalReasons} {
		for _, provider := range providers {
			t.Run(provider.name+"/"+http.StatusText(status), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					http.Error(writer, "deployment unavailable", status)
				}))
				defer server.Close()

				_, catalogErr := provider.catalog(server.Client(), server.URL).Discover(context.Background())
				require.Error(t, catalogErr)
				require.Equal(t, status, httpStatusFromError(catalogErr))

				_, tickerErr := provider.ticker(server.Client(), server.URL).Fetch(context.Background())
				require.Error(t, tickerErr)
				require.Equal(t, status, httpStatusFromError(tickerErr))

				start := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
				_, klineErr := provider.kline(server.Client(), server.URL).
					Fetch1m(context.Background(), "BTCUSDT", start, start.Add(time.Minute), 1)
				require.Error(t, klineErr)
				require.Equal(t, status, httpStatusFromError(klineErr))
			})
		}
	}
}

func TestBatchTickerAdaptersPreserveOpenAndQuoteTurnover(t *testing.T) {
	tests := []struct {
		name     string
		response string
		build    func(*http.Client, string) spotTickerBatchAdapter
		source   string
	}{
		{
			name:     "binance",
			response: `[{"symbol":"BTCUSDT","lastPrice":"105","bidPrice":"104.9","askPrice":"105.1","priceChangePercent":"5","quoteVolume":"1234","closeTime":1784880000000}]`,
			build: func(client *http.Client, endpoint string) spotTickerBatchAdapter {
				return &binanceBatchTickerAdapter{client: client, baseURL: endpoint}
			},
			source: "BTCUSDT",
		},
		{
			name:     "bybit",
			response: `{"retCode":0,"retMsg":"OK","time":1784880000000,"result":{"list":[{"symbol":"BTCUSDT","lastPrice":"105","bid1Price":"104.9","ask1Price":"105.1","prevPrice24h":"100","turnover24h":"1234"}]}}`,
			build: func(client *http.Client, endpoint string) spotTickerBatchAdapter {
				return &bybitBatchTickerAdapter{client: client, baseURL: endpoint}
			},
			source: "BTCUSDT",
		},
		{
			name:     "okx",
			response: `{"code":"0","msg":"","data":[{"instId":"BTC-USDT","last":"105","bidPx":"104.9","askPx":"105.1","open24h":"100","volCcy24h":"1234","ts":"1784880000000"}]}`,
			build: func(client *http.Client, endpoint string) spotTickerBatchAdapter {
				return &okxBatchTickerAdapter{client: client, baseURL: endpoint}
			},
			source: "BTC-USDT",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.response))
			}))
			defer server.Close()

			items, err := test.build(server.Client(), server.URL).Fetch(context.Background())
			require.NoError(t, err)
			require.Equal(t, "100", items[test.source].Open24h)
			require.Equal(t, "1234", items[test.source].QuoteTurnover)
			if test.name == "binance" {
				require.Equal(t, "5", items[test.source].Change24hPct)
			}
		})
	}
}
