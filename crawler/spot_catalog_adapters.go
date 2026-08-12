package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type BinanceSpotAdapter struct {
	client  *http.Client
	baseURL string
}

func NewBinanceSpotAdapter(client *http.Client) *BinanceSpotAdapter {
	return &BinanceSpotAdapter{client: client, baseURL: binanceMarketDataRESTBaseURL}
}

func (*BinanceSpotAdapter) Provider() string { return "binance" }

func (a *BinanceSpotAdapter) Discover(ctx context.Context) ([]DiscoveredMarket, error) {
	var payload struct {
		Symbols []struct {
			Symbol               string `json:"symbol"`
			Status               string `json:"status"`
			BaseAsset            string `json:"baseAsset"`
			QuoteAsset           string `json:"quoteAsset"`
			IsSpotTradingAllowed bool   `json:"isSpotTradingAllowed"`
		} `json:"symbols"`
	}
	if err := getProviderJSON(ctx, a.client, a.baseURL+"/api/v3/exchangeInfo", &payload); err != nil {
		return nil, err
	}
	result := make([]DiscoveredMarket, 0, len(payload.Symbols))
	for _, item := range payload.Symbols {
		raw, _ := json.Marshal(item)
		result = append(result, DiscoveredMarket{
			SourceSymbol: item.Symbol, BaseAlias: item.BaseAsset, QuoteAlias: item.QuoteAsset,
			MarketType: "spot", UpstreamStatus: item.Status,
			Tradable:    strings.EqualFold(item.Status, "TRADING") && item.IsSpotTradingAllowed,
			RawMetadata: raw,
		})
	}
	return result, nil
}

type CoinbaseSpotAdapter struct {
	client  *http.Client
	baseURL string
}

func NewCoinbaseSpotAdapter(client *http.Client) *CoinbaseSpotAdapter {
	return &CoinbaseSpotAdapter{client: client, baseURL: "https://api.exchange.coinbase.com"}
}

func (*CoinbaseSpotAdapter) Provider() string { return "coinbase" }

func (a *CoinbaseSpotAdapter) Discover(ctx context.Context) ([]DiscoveredMarket, error) {
	var payload []struct {
		ID              string `json:"id"`
		BaseCurrency    string `json:"base_currency"`
		QuoteCurrency   string `json:"quote_currency"`
		Status          string `json:"status"`
		TradingDisabled bool   `json:"trading_disabled"`
		CancelOnly      bool   `json:"cancel_only"`
		PostOnly        bool   `json:"post_only"`
	}
	if err := getProviderJSON(ctx, a.client, a.baseURL+"/products", &payload); err != nil {
		return nil, err
	}
	result := make([]DiscoveredMarket, 0, len(payload))
	for _, item := range payload {
		raw, _ := json.Marshal(item)
		result = append(result, DiscoveredMarket{
			SourceSymbol: item.ID, BaseAlias: item.BaseCurrency, QuoteAlias: item.QuoteCurrency,
			MarketType: "spot", UpstreamStatus: item.Status,
			Tradable: strings.EqualFold(item.Status, "online") &&
				!item.TradingDisabled && !item.CancelOnly && !item.PostOnly,
			RawMetadata: raw,
		})
	}
	return result, nil
}

type BybitSpotAdapter struct {
	client  *http.Client
	baseURL string
}

func NewBybitSpotAdapter(client *http.Client) *BybitSpotAdapter {
	return &BybitSpotAdapter{client: client, baseURL: bybitV5RESTBaseURL}
}

func (*BybitSpotAdapter) Provider() string { return "bybit" }

func (a *BybitSpotAdapter) Discover(ctx context.Context) ([]DiscoveredMarket, error) {
	type response struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			NextPageCursor string `json:"nextPageCursor"`
			List           []struct {
				Symbol    string `json:"symbol"`
				BaseCoin  string `json:"baseCoin"`
				QuoteCoin string `json:"quoteCoin"`
				Status    string `json:"status"`
			} `json:"list"`
		} `json:"result"`
	}
	var payload response
	if err := getProviderJSON(
		ctx, a.client, a.baseURL+"/v5/market/instruments-info?category=spot", &payload,
	); err != nil {
		return nil, err
	}
	if payload.RetCode != 0 {
		return nil, fmt.Errorf("Bybit catalog retCode=%d retMsg=%s", payload.RetCode, payload.RetMsg)
	}
	result := make([]DiscoveredMarket, 0, len(payload.Result.List))
	for _, item := range payload.Result.List {
		raw, _ := json.Marshal(item)
		result = append(result, DiscoveredMarket{
			SourceSymbol: item.Symbol, BaseAlias: item.BaseCoin, QuoteAlias: item.QuoteCoin,
			MarketType: "spot", UpstreamStatus: item.Status,
			Tradable:    strings.EqualFold(item.Status, "Trading"),
			RawMetadata: raw,
		})
	}
	return result, nil
}

type OKXSpotAdapter struct {
	client  *http.Client
	baseURL string
}

func NewOKXSpotAdapter(client *http.Client) *OKXSpotAdapter {
	return &OKXSpotAdapter{client: client, baseURL: "https://www.okx.com"}
}

func (*OKXSpotAdapter) Provider() string { return "okx" }

func (a *OKXSpotAdapter) Discover(ctx context.Context) ([]DiscoveredMarket, error) {
	var payload struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			InstID   string `json:"instId"`
			BaseCcy  string `json:"baseCcy"`
			QuoteCcy string `json:"quoteCcy"`
			State    string `json:"state"`
		} `json:"data"`
	}
	if err := getProviderJSON(ctx, a.client, a.baseURL+"/api/v5/public/instruments?instType=SPOT", &payload); err != nil {
		return nil, err
	}
	if payload.Code != "0" {
		return nil, fmt.Errorf("OKX catalog code=%s msg=%s", payload.Code, payload.Msg)
	}
	result := make([]DiscoveredMarket, 0, len(payload.Data))
	for _, item := range payload.Data {
		raw, _ := json.Marshal(item)
		result = append(result, DiscoveredMarket{
			SourceSymbol: item.InstID, BaseAlias: item.BaseCcy, QuoteAlias: item.QuoteCcy,
			MarketType: "spot", UpstreamStatus: item.State,
			Tradable:    strings.EqualFold(item.State, "live"),
			RawMetadata: raw,
		})
	}
	return result, nil
}

type providerHTTPError struct {
	host   string
	status int
}

func (e *providerHTTPError) Error() string {
	return fmt.Sprintf("%s HTTP %d", e.host, e.status)
}

func getProviderJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "s78-market-services/1.0")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &providerHTTPError{host: request.URL.Host, status: response.StatusCode}
	}
	return json.NewDecoder(response.Body).Decode(target)
}
