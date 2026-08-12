package researchsnapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBinanceBaseURL = "https://data-api.binance.vision"

type binanceTicker struct {
	Symbol      string `json:"symbol"`
	OpenPrice   string `json:"openPrice"`
	HighPrice   string `json:"highPrice"`
	LowPrice    string `json:"lowPrice"`
	LastPrice   string `json:"lastPrice"`
	Volume      string `json:"volume"`
	QuoteVolume string `json:"quoteVolume"`
	OpenTime    int64  `json:"openTime"`
	CloseTime   int64  `json:"closeTime"`
	FirstID     int64  `json:"firstId"`
	LastID      int64  `json:"lastId"`
	Count       int64  `json:"count"`
}

func Capture(ctx context.Context, client *http.Client, now time.Time) (Snapshot, error) {
	client = noRedirectClient(client, 8*time.Second)
	now = now.UTC()
	tickers, providerReason := fetchBinance(ctx, client)
	quotes := make([]Quote, 0, 3)
	coverage := make([]Coverage, 0, len(universe))
	for _, asset := range universe {
		if asset.Provider != "binance" {
			coverage = append(coverage, Coverage{AssetID: asset.ID, Status: "unavailable", MarketState: "unknown", Reason: "provider_not_added_in_m2a"})
			continue
		}
		ticker, ok := tickers[asset.Symbol]
		if !ok {
			reason := providerReason
			if reason == "" {
				reason = "symbol_missing"
			}
			coverage = append(coverage, Coverage{AssetID: asset.ID, Status: "unavailable", MarketState: "open", Reason: reason})
			continue
		}
		observedAt := time.UnixMilli(ticker.CloseTime).UTC()
		if observedAt.After(now.Add(2 * time.Minute)) {
			coverage = append(coverage, Coverage{AssetID: asset.ID, Status: "unavailable", MarketState: "open", Reason: "future_source_time"})
			continue
		}
		delay := max(int64(0), int64(now.Sub(observedAt).Seconds()))
		status := "healthy"
		if delay > 300 {
			status = "stale"
		}
		quotes = append(quotes, Quote{
			AssetID: asset.ID, Role: "display", Price: normalizeDecimal(ticker.LastPrice), Currency: "USDT",
			ObservedAt: observedAt.Format(time.RFC3339Nano), DelaySeconds: delay, Provider: "binance_public",
			Mode: "live", DisplayScope: "private",
		})
		coverage = append(coverage, Coverage{AssetID: asset.ID, Status: status, MarketState: "open"})
	}
	return Finalize(Snapshot{
		AsOf: now.Format(time.RFC3339Nano), GeneratedAt: now.Format(time.RFC3339Nano), Mode: "mixed",
		Quotes: quotes, Coverage: coverage,
	})
}

func fetchBinance(ctx context.Context, client *http.Client) (map[string]binanceTicker, string) {
	symbols, _ := json.Marshal([]string{"BTCUSDT", "ETHUSDT", "SOLUSDT"})
	target := DefaultBinanceBaseURL + "/api/v3/ticker/24hr?type=MINI&symbols=" + url.QueryEscape(string(symbols))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "request_build_failed"
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, "provider_unavailable"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Sprintf("provider_http_%d", response.StatusCode)
	}
	var payload []binanceTicker
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, "invalid_provider_payload"
	}
	result := make(map[string]binanceTicker, len(payload))
	for _, ticker := range payload {
		price := normalizeDecimal(ticker.LastPrice)
		if ticker.Symbol == "" || ticker.CloseTime <= 0 || !decimalPattern.MatchString(ticker.LastPrice) || price == "0" {
			continue
		}
		result[ticker.Symbol] = ticker
	}
	return result, ""
}

func noRedirectClient(client *http.Client, timeout time.Duration) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	if clone.Timeout <= 0 {
		clone.Timeout = timeout
	}
	clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func normalizeDecimal(value string) string {
	if !strings.Contains(value, ".") {
		return value
	}
	value = strings.TrimRight(value, "0")
	value = strings.TrimRight(value, ".")
	return value
}
