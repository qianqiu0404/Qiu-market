package dw

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/database"
)

// scaleDecimals 是 PG 侧价格 / 成交量 / 市值的放大倍数位数（1e8），
// 与 crawler 的 decimalStringToUint256String(x, 8) 对应。
const scaleDecimals = 8

// UnscaleDecimalString 把 PG 存储的数值字符串还原为人类可读小数字符串。
// PG 侧的两种形态（与 services/http/service/asset.go 的 unscaleString 同规则）：
//   - crawler 写入的 1e8 放大整数："137660000" 或 "137660000.000000000000000000"
//     （小数部分全 0）→ 除以 1e8 → "1.3766"
//   - seed 写入的原始小数："0.00000123"（小数部分有非 0）→ 原样清理后返回
//
// 纯函数，不依赖任何外部状态；非法输入返回 "0"。
func UnscaleDecimalString(valStr string, decimals int) string {
	if valStr == "" || valStr == "0" {
		return "0"
	}

	if strings.Contains(valStr, ".") {
		parts := strings.Split(valStr, ".")
		if len(parts) == 2 {
			decimalPart := strings.TrimRight(parts[1], "0")
			if decimalPart == "" {
				// 小数部分全 0 → 1e8 放大整数 → 除以 1e8
				return unscaleScaledInt(parts[0], decimals)
			}
			// 小数部分有非 0 → 已是人类可读小数 → 清理多余 0 后返回
			out := strings.TrimRight(valStr, "0")
			out = strings.TrimRight(out, ".")
			if out == "" || out == "-" {
				return "0"
			}
			return out
		}
	}

	// 无小数点 → 1e8 放大整数 → 除以 1e8
	return unscaleScaledInt(valStr, decimals)
}

// unscaleScaledInt 用 big.Rat 做精确除法（不走 float，避免二进制误差），
// 输出去掉尾部多余 0 的十进制串。
func unscaleScaledInt(intStr string, decimals int) string {
	bi, ok := new(big.Int).SetString(intStr, 10)
	if !ok {
		return "0"
	}
	if bi.Sign() == 0 {
		return "0"
	}
	multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	rat := new(big.Rat).SetFrac(bi, multiplier)

	// 最高保留 decimals 位小数，再清理尾部 0
	out := rat.FloatString(decimals)
	out = strings.TrimRight(out, "0")
	out = strings.TrimRight(out, ".")
	if out == "" || out == "-" {
		return "0"
	}
	return out
}

// FormatDorisDateTime 把时间格式化为 Doris DATETIME 列接受的 "YYYY-MM-DD HH:MM:SS"（UTC）。
func FormatDorisDateTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// KlineJSONRow 是 dwd_symbol_kline 一行 Stream Load JSON。
// 数值字段以字符串形式给出，Doris 对 DECIMAL 列接受字符串或数字字面量，
// 字符串可避免大数在 JSON 数字解析中丢失精度。
type KlineJSONRow struct {
	SymbolGuid string `json:"symbol_guid"`
	Interval   string `json:"interval"`
	OpenTime   string `json:"open_time"`
	OpenPrice  string `json:"open_price"`
	HighPrice  string `json:"high_price"`
	LowPrice   string `json:"low_price"`
	ClosePrice string `json:"close_price"`
	Volume     string `json:"volume"`
}

// KlineV2JSONRow is the shadow Doris row keyed by the stable market identity
// and explicit business open time.
type KlineV2JSONRow struct {
	MarketID   string `json:"market_id"`
	MarketCode string `json:"market_code"`
	SymbolGuid string `json:"symbol_guid"`
	Interval   string `json:"interval"`
	OpenTime   string `json:"open_time"`
	OpenPrice  string `json:"open_price"`
	HighPrice  string `json:"high_price"`
	LowPrice   string `json:"low_price"`
	ClosePrice string `json:"close_price"`
	Volume     string `json:"volume"`
	MarketCap  string `json:"market_cap"`
	IsActive   bool   `json:"is_active"`
	IngestedAt string `json:"ingested_at"`
	UpdatedAt  string `json:"updated_at"`
	SyncSeq    int64  `json:"sync_seq"`
}

// BuildKlineJSONRows 把 PG symbol_kline 行（1e8 放大整数）转为 Doris JSON 行
// （人类可读小数）。open_time 取 created_at —— crawler 落库时
// created_at = K 线开盘时间（time.UnixMilli(openTime)），见 crawler/binance_ticker.go。
func BuildKlineJSONRows(list []*database.SymbolKline) []KlineJSONRow {
	rows := make([]KlineJSONRow, 0, len(list))
	for _, k := range list {
		rows = append(rows, KlineJSONRow{
			SymbolGuid: k.SymbolGuid,
			Interval:   k.Interval,
			OpenTime:   FormatDorisDateTime(k.CreatedAt),
			OpenPrice:  UnscaleDecimalString(k.OpenPrice, scaleDecimals),
			HighPrice:  UnscaleDecimalString(k.HighPrice, scaleDecimals),
			LowPrice:   UnscaleDecimalString(k.LowPrice, scaleDecimals),
			ClosePrice: UnscaleDecimalString(k.ClosePrice, scaleDecimals),
			Volume:     UnscaleDecimalString(k.Volume, scaleDecimals),
		})
	}
	return rows
}

func BuildKlineV2JSONRows(list []*database.SymbolKline, marketCode string) []KlineV2JSONRow {
	rows := make([]KlineV2JSONRow, 0, len(list))
	for _, k := range list {
		rows = append(rows, KlineV2JSONRow{
			MarketID:   k.MarketID,
			MarketCode: marketCode,
			SymbolGuid: k.SymbolGuid,
			Interval:   k.Interval,
			OpenTime:   FormatDorisDateTime(k.OpenTime),
			OpenPrice:  UnscaleDecimalString(k.OpenPrice, scaleDecimals),
			HighPrice:  UnscaleDecimalString(k.HighPrice, scaleDecimals),
			LowPrice:   UnscaleDecimalString(k.LowPrice, scaleDecimals),
			ClosePrice: UnscaleDecimalString(k.ClosePrice, scaleDecimals),
			Volume:     UnscaleDecimalString(k.Volume, scaleDecimals),
			MarketCap:  UnscaleDecimalString(k.MarketCap, scaleDecimals),
			IsActive:   k.IsActive,
			IngestedAt: FormatDorisDateTime(k.IngestedAt),
			UpdatedAt:  FormatDorisDateTime(k.UpdatedAt),
			SyncSeq:    k.SyncSeq,
		})
	}
	return rows
}

// SnapshotJSONRow 是 dws_symbol_market_snapshot 一行 Stream Load JSON。
type SnapshotJSONRow struct {
	SymbolGuid string `json:"symbol_guid"`
	CapturedAt string `json:"captured_at"`
	Exchange   string `json:"exchange"`
	Price      string `json:"price"`
	Volume     string `json:"volume"`
	MarketCap  string `json:"market_cap"`
	Change24h  string `json:"change24h"`
}

type SnapshotV2JSONRow struct {
	MarketID          string  `json:"market_id"`
	MarketCode        string  `json:"market_code"`
	SymbolGuid        string  `json:"symbol_guid"`
	CapturedAt        string  `json:"captured_at"`
	Exchange          string  `json:"exchange"`
	Price             string  `json:"price"`
	Volume            string  `json:"volume"`
	MarketCap         string  `json:"market_cap"`
	Change24hPct      *string `json:"change_24h_pct"`
	ProviderUpdatedAt *string `json:"provider_updated_at"`
}

// BuildSnapshotJSONRows 把 symbol_market 当前行转为快照 JSON 行。
// change24h comes from the canonical nullable percentage. The legacy radio
// column is intentionally not consulted.
func BuildSnapshotJSONRows(list []*database.MarketSnapshotRow, capturedAt time.Time) []SnapshotJSONRow {
	rows := make([]SnapshotJSONRow, 0, len(list))
	captured := FormatDorisDateTime(capturedAt)
	for _, m := range list {
		change := "0"
		if m.Change24hPct != nil {
			value := strings.TrimSpace(*m.Change24hPct)
			if value != "" {
				change = value
			}
		}
		rows = append(rows, SnapshotJSONRow{
			SymbolGuid: m.SymbolGuid,
			CapturedAt: captured,
			Exchange:   m.Exchange,
			Price:      UnscaleDecimalString(m.Price, scaleDecimals),
			Volume:     UnscaleDecimalString(m.Volume, scaleDecimals),
			MarketCap:  UnscaleDecimalString(m.MarketCap, scaleDecimals),
			Change24h:  change,
		})
	}
	return rows
}

func BuildSnapshotV2JSONRows(list []*database.MarketSnapshotRow, capturedAt time.Time) []SnapshotV2JSONRow {
	rows := make([]SnapshotV2JSONRow, 0, len(list))
	captured := FormatDorisDateTime(capturedAt)
	for _, market := range list {
		var change *string
		if market.Change24hPct != nil {
			value := strings.TrimSpace(*market.Change24hPct)
			if value != "" {
				change = &value
			}
		}
		var providerUpdatedAt *string
		if market.ObservedAt != nil && !market.ObservedAt.IsZero() {
			value := FormatDorisDateTime(*market.ObservedAt)
			providerUpdatedAt = &value
		}
		rows = append(rows, SnapshotV2JSONRow{
			MarketID:          market.MarketID,
			MarketCode:        market.MarketCode,
			SymbolGuid:        market.SymbolGuid,
			CapturedAt:        captured,
			Exchange:          market.Exchange,
			Price:             UnscaleDecimalString(market.Price, scaleDecimals),
			Volume:            UnscaleDecimalString(market.Volume, scaleDecimals),
			MarketCap:         UnscaleDecimalString(market.MarketCap, scaleDecimals),
			Change24hPct:      change,
			ProviderUpdatedAt: providerUpdatedAt,
		})
	}
	return rows
}

// StreamLoadLabel 生成唯一的 Stream Load label（Doris 按 label 幂等去重）。
func StreamLoadLabel(table string, t time.Time) string {
	return fmt.Sprintf("s78-dw-%s-%d", table, t.UnixNano())
}

var streamLabelUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// StreamLoadSeqLabel is stable for an identical sequence batch. Doris can
// therefore recognize a client retry after a lost response without loading
// the same payload twice.
func StreamLoadSeqLabel(table, stream string, minSeq, maxSeq int64, payload []byte) string {
	safeStream := streamLabelUnsafe.ReplaceAllString(stream, "_")
	if len(safeStream) > 40 {
		safeStream = safeStream[:40]
	}
	digest := sha256.Sum256(append(append([]byte(stream), 0), payload...))
	return fmt.Sprintf("s78-%s-%s-%d-%d-%x", table, safeStream, minSeq, maxSeq, digest[:8])
}

// ReplayScanStart applies the fixed sequence lookback used to recover a lower
// sequence number that commits after a higher one already advanced the state.
func ReplayScanStart(lastSyncSeq, lookback int64) int64 {
	if lookback <= 0 || lastSyncSeq <= lookback {
		return 0
	}
	return lastSyncSeq - lookback
}
