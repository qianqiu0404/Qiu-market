package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/services/http/model"
)

// openErAPIResponse represents open.er-api.com API response
type openErAPIResponse struct {
	Rates map[string]float64 `json:"rates"`
}

// 内存缓存
var (
	cachedRates   map[string]float64
	cachedSource  string
	cachedAt      time.Time
	cacheMu       sync.RWMutex
	cacheDuration = 10 * time.Minute
)

func (h HandleSvc) GetFiatRates(request *model.FiatRatesRequest) (*model.FiatRatesResponse, error) {
	rates, source, err := fetchFiatRates()
	if err != nil {
		return nil, err
	}

	return &model.FiatRatesResponse{
		Code:    2000,
		Message: "get fiat rates success",
		Result: model.FiatRatesResult{
			Base:   "USD",
			Rates:  rates,
			Source: source,
		},
	}, nil
}

// fetchFiatRates tries the fresh cache first, then the external API, and finally
// a stale cache. It never fabricates exchange-rate data.
func fetchFiatRates() (rates map[string]float64, source string, err error) {
	// 1. 检查缓存
	cacheMu.RLock()
	hit := cachedRates != nil && time.Since(cachedAt) < cacheDuration
	if hit {
		r := cachedRates
		s := cachedSource
		cacheMu.RUnlock()
		log.Debug("Fiat rates cache hit", "source", s)
		return r, "cached:" + s, nil
	}
	cacheMu.RUnlock()

	// 2. 请求外部 API
	url := "https://open.er-api.com/v6/latest/USD"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			body, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				var result openErAPIResponse
				if decodeErr := json.Unmarshal(body, &result); decodeErr == nil {
					if cny, ok := result.Rates["CNY"]; ok && cny > 0 {
						if hkd, ok := result.Rates["HKD"]; ok && hkd > 0 {
							r := map[string]float64{"USD": 1, "CNY": cny, "HKD": hkd}
							cacheMu.Lock()
							cachedRates = r
							cachedSource = "open.er-api"
							cachedAt = time.Now()
							cacheMu.Unlock()
							log.Info("Fiat rates fetched from open.er-api", "CNY", cny, "HKD", hkd)
							return r, "open.er-api", nil
						}
					}
				}
			}
		}
	}
	if err != nil {
		log.Error("Fiat rates fetch failed", "error", err)
	} else {
		log.Error("Fiat rates fetch returned invalid data", "status", resp.StatusCode)
	}

	// 3. 外部 API 失败 → 有旧缓存则返回旧缓存
	cacheMu.RLock()
	hasOld := cachedRates != nil
	if hasOld {
		r := cachedRates
		cacheMu.RUnlock()
		log.Warn("Using stale cache for fiat rates")
		return r, "stale-cache", nil
	}
	cacheMu.RUnlock()

	return nil, "", fmt.Errorf("fiat-rate provider unavailable and no cached data exists")
}
