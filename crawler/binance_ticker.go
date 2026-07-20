package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/common/marketkey"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
)

// Binance 24hr ticker 响应字段
type binanceTicker struct {
	Symbol      string `json:"symbol"`
	LastPrice   string `json:"lastPrice"`
	BidPrice    string `json:"bidPrice"`
	AskPrice    string `json:"askPrice"`
	PriceChange string `json:"priceChangePercent"`
	Volume      string `json:"volume"`
	QuoteVolume string `json:"quoteVolume"`
}

// CoinGecko 价格响应
type coingeckoPriceResponse map[string]struct {
	Usd          float64 `json:"usd"`
	UsdMarketCap float64 `json:"usd_market_cap"`
	Usd24hVol    float64 `json:"usd_24h_vol"`
	Usd24hChange float64 `json:"usd_24h_change"`
}

// symbol_guid 到 CoinGecko coin id 映射
var coingeckoIDMap = map[string]string{
	"s1": "bitcoin",
	"s2": "ethereum",
	"s3": "solana",
	"s4": "binancecoin",
	"s5": "ripple",
	"s6": "dogecoin",
}

// symbol 到 symbol_guid 映射
var symbolGuidMap = map[string]string{
	"BTCUSDT":  "s1",
	"ETHUSDT":  "s2",
	"SOLUSDT":  "s3",
	"BNBUSDT":  "s4",
	"XRPUSDT":  "s5",
	"DOGEUSDT": "s6",
}

// 需要抓取的交易对
var tickerSymbols = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "XRPUSDT", "DOGEUSDT"}

type BinanceTickerCrawler struct {
	db       *database.DB
	redisCli *redis.Client
	stopped  atomic.Bool
	cancel   context.CancelFunc
	// CoinGecko 调用节流
	lastCGTime time.Time
}

func NewBinanceTickerCrawler(db *database.DB, redisClient *redis.Client) *BinanceTickerCrawler {
	return &BinanceTickerCrawler{
		db:       db,
		redisCli: redisClient,
	}
}

func (b *BinanceTickerCrawler) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	go b.runLoop(ctx)
	log.Info("BinanceTickerCrawler started")
	return nil
}

func (b *BinanceTickerCrawler) Stop() error {
	if b.cancel != nil {
		b.cancel()
	}
	b.stopped.Store(true)
	log.Info("BinanceTickerCrawler stopped")
	return nil
}

func (b *BinanceTickerCrawler) Stopped() bool {
	return b.stopped.Load()
}

func (b *BinanceTickerCrawler) runLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.fetchAndStore()
			// CoinGecko 每 60 秒调用一次（避免被限流）
			if time.Since(b.lastCGTime) > 60*time.Second {
				b.fetchAndStoreMarketCap()
				b.lastCGTime = time.Now()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (b *BinanceTickerCrawler) fetchAndStore() {
	client := &http.Client{Timeout: 10 * time.Second}

	for _, symbol := range tickerSymbols {
		guid, ok := symbolGuidMap[symbol]
		if !ok {
			continue
		}

		url := fmt.Sprintf("https://api.binance.com/api/v3/ticker/24hr?symbol=%s", symbol)
		resp, err := client.Get(url)
		if err != nil {
			log.Error("BinanceTickerCrawler HTTP request failed", "symbol", symbol, "error", err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Error("BinanceTickerCrawler read body failed", "symbol", symbol, "error", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.Error("BinanceTickerCrawler bad status", "symbol", symbol, "status", resp.StatusCode)
			continue
		}

		var ticker binanceTicker
		if err := json.Unmarshal(body, &ticker); err != nil {
			log.Error("BinanceTickerCrawler JSON parse failed", "symbol", symbol, "error", err)
			continue
		}

		if ticker.LastPrice == "" || ticker.Volume == "" {
			log.Warn("BinanceTickerCrawler empty price/volume", "symbol", symbol)
			continue
		}

		// 将小数转为整数，放大 1e8
		scaledPrice, err := decimalStringToUint256String(ticker.LastPrice, 8)
		if err != nil {
			log.Error("BinanceTickerCrawler scale price failed", "symbol", symbol, "price", ticker.LastPrice, "error", err)
			continue
		}
		// 使用 quoteVolume（USDT 计价）替代 base volume，展示更有意义的交易量
		volString := ticker.QuoteVolume
		if volString == "" {
			volString = ticker.Volume // fallback
		}
		scaledVolume, err := decimalStringToUint256String(volString, 8)
		if err != nil {
			log.Error("BinanceTickerCrawler scale volume failed", "symbol", symbol, "volume", volString, "error", err)
			continue
		}
		askPrice := fallbackString(ticker.AskPrice, ticker.LastPrice)
		bidPrice := fallbackString(ticker.BidPrice, ticker.LastPrice)
		scaledAskPrice, err := decimalStringToUint256String(askPrice, 8)
		if err != nil {
			log.Error("BinanceTickerCrawler scale ask price failed", "symbol", symbol, "askPrice", askPrice, "error", err)
			continue
		}
		scaledBidPrice, err := decimalStringToUint256String(bidPrice, 8)
		if err != nil {
			log.Error("BinanceTickerCrawler scale bid price failed", "symbol", symbol, "bidPrice", bidPrice, "error", err)
			continue
		}

		if err := b.db.SymbolMarket.UpdateSymbolMarketTickerWithChange(guid, scaledPrice, scaledVolume, ticker.PriceChange); err != nil {
			log.Error("BinanceTickerCrawler DB update failed", "symbol", symbol, "guid", guid, "error", err)
			continue
		}
		b.cacheTicker(guid, symbol, scaledPrice, scaledAskPrice, scaledBidPrice, scaledVolume)

		// 最小 K线闭环
		b.fetchAndStoreKline(symbol, guid)

		log.Info("BinanceTickerCrawler updated", "symbol", symbol, "guid", guid, "price", scaledPrice, "volume", scaledVolume)
	}
}

func (b *BinanceTickerCrawler) cacheTicker(symbolGuid, binanceSymbol, price, askPrice, bidPrice, volume string) {
	if b.redisCli == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	symbolName := binanceSymbolToPair(binanceSymbol)
	key := marketkey.Build("e1", "Binance", symbolGuid, symbolName)
	if err := b.redisCli.Set(ctx, key, price, 10*time.Minute); err != nil {
		log.Error("BinanceTickerCrawler cache price failed", "key", key, "error", err)
	}
	_ = b.redisCli.Set(ctx, key+"askPrice", askPrice, 10*time.Minute)
	_ = b.redisCli.Set(ctx, key+"bidPrice", bidPrice, 10*time.Minute)
	_ = b.redisCli.Set(ctx, key+"volume", volume, 10*time.Minute)
}

func fallbackString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func binanceSymbolToPair(symbol string) string {
	if len(symbol) > 4 && symbol[len(symbol)-4:] == "USDT" {
		return symbol[:len(symbol)-4] + "/USDT"
	}
	return symbol
}

// fetchAndStoreMarketCap 从 CoinGecko 获取市值数据并写入 DB
func (b *BinanceTickerCrawler) fetchAndStoreMarketCap() {
	ids := "bitcoin,ethereum,solana,binancecoin,ripple,dogecoin"
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd&include_24hr_vol=true&include_24hr_change=true&include_market_cap=true", ids)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Error("CoinGecko request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Error("CoinGecko bad status", "status", resp.StatusCode)
		return
	}

	var data coingeckoPriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Error("CoinGecko decode failed", "error", err)
		return
	}

	// CoinGecko coin id 到 guid 的反向映射，与 coingeckoIDMap 一致
	coinToGuid := map[string]string{
		"bitcoin":     "s1",
		"ethereum":    "s2",
		"solana":      "s3",
		"binancecoin": "s4",
		"ripple":      "s5",
		"dogecoin":    "s6",
	}

	for coinID, guid := range coinToGuid {
		info, ok := data[coinID]
		if !ok {
			log.Warn("CoinGecko no data for coin", "coin_id", coinID, "guid", guid)
			continue
		}
		// market_cap 转为 uint256 字符串（1e8 放大）
		mcStr := fmt.Sprintf("%.0f", info.UsdMarketCap)
		scaledMC, err := decimalStringToUint256String(mcStr, 8)
		if err != nil {
			log.Error("CoinGecko scale market_cap failed", "coin", coinID, "market_cap", mcStr, "error", err)
			continue
		}
		if err := b.db.SymbolMarket.UpdateSymbolMarketFull(guid, scaledMC); err != nil {
			log.Error("CoinGecko DB update failed", "guid", guid, "error", err)
		}
		log.Info("CoinGecko market_cap updated", "guid", guid, "market_cap", mcStr)
	}
}

func (b *BinanceTickerCrawler) fetchAndStoreKline(symbol, guid string) {
	url := fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=%s&interval=1m&limit=20", symbol)
	resp, err := http.Get(url)
	if err != nil {
		log.Error("fetch kline failed", "symbol", symbol, "err", err)
		return
	}
	defer resp.Body.Close()

	var klines [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&klines); err != nil {
		log.Error("decode kline failed", "symbol", symbol, "err", err)
		return
	}

	for _, k := range klines {
		if len(k) < 6 {
			continue
		}

		// Binance returns openTime as a number (float64 in encoding/json)
		// We use int64 to avoid scientific notation
		openTimeRaw := k[0]
		var openTime int64
		switch v := openTimeRaw.(type) {
		case float64:
			openTime = int64(v)
		case int64:
			openTime = v
		case string:
			fmt.Sscanf(v, "%d", &openTime)
		default:
			openTime = time.Now().UnixMilli()
		}

		open, _ := decimalStringToUint256String(fmt.Sprintf("%v", k[1]), 8)
		high, _ := decimalStringToUint256String(fmt.Sprintf("%v", k[2]), 8)
		low, _ := decimalStringToUint256String(fmt.Sprintf("%v", k[3]), 8)
		close, _ := decimalStringToUint256String(fmt.Sprintf("%v", k[4]), 8)
		volume, _ := decimalStringToUint256String(fmt.Sprintf("%v", k[5]), 8)

		kline := database.SymbolKline{
			Guid:       fmt.Sprintf("%s-%d", guid, openTime),
			SymbolGuid: guid,
			OpenPrice:  open,
			HighPrice:  high,
			LowPrice:   low,
			ClosePrice: close,
			Volume:     volume,
			MarketCap:  "0",
			IsActive:   true,
			CreatedAt:  time.UnixMilli(openTime),
			UpdatedAt:  time.Now(),
		}
		_ = b.db.SymbolKline.StoreSymbolKline(&kline)
	}
}

func decimalStringToUint256String(value string, scale int64) (string, error) {
	val, ok := new(big.Float).SetString(value)
	if !ok {
		return "", fmt.Errorf("invalid decimal string: %s", value)
	}

	// multiplier = 10^scale
	multiplier := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(scale), nil))

	// scaled = val * multiplier
	scaled := new(big.Float).Mul(val, multiplier)

	// to integer string (remove fractional part)
	result := new(big.Int)
	scaled.Int(result)

	return result.String(), nil
}
