package dw

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/the-web3/s78-market-services/database"
)

func TestUnscaleDecimalString(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		decimals int
		want     string
	}{
		{"empty", "", 8, "0"},
		{"zero", "0", 8, "0"},
		{"scaled int", "137660000", 8, "1.3766"},
		{"scaled int one", "100000000", 8, "1"},
		{"scaled small", "123", 8, "0.00000123"},
		{"scaled with zero fraction", "137660000.000000000000000000", 8, "1.3766"},
		{"scaled large volume", "137660000123456789", 8, "1376600001.23456789"},
		{"already decimal", "0.00000123", 8, "0.00000123"},
		{"already decimal trailing zeros", "1.230000000000000000", 8, "1.23"},
		{"already decimal integer-looking", "123.000000000000000001", 8, "123.000000000000000001"},
		{"negative scaled", "-500000000", 8, "-5"},
		{"garbage", "not-a-number", 8, "0"},
		{"six decimals", "1230000", 6, "1.23"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, UnscaleDecimalString(c.in, c.decimals))
		})
	}
}

func TestFormatDorisDateTime(t *testing.T) {
	ts := time.Date(2026, 8, 1, 12, 34, 56, 789000000, time.UTC)
	assert.Equal(t, "2026-08-01 12:34:56", FormatDorisDateTime(ts))

	// 同一瞬间换时区表示，输出必须仍归一到 UTC
	loc := time.FixedZone("CST", 8*3600)
	assert.Equal(t, "2026-08-01 12:34:56", FormatDorisDateTime(ts.In(loc)))
}

func TestBuildKlineJSONRows(t *testing.T) {
	created := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	list := []*database.SymbolKline{
		{
			Guid:       "s1-1h-1785974400000",
			SymbolGuid: "s1",
			Interval:   "1h",
			OpenPrice:  "137660000.000000000000000000",
			HighPrice:  "138000000",
			LowPrice:   "137000000",
			ClosePrice: "137500000",
			Volume:     "137660000123456789",
			CreatedAt:  created,
		},
	}
	rows := BuildKlineJSONRows(list)
	assert.Len(t, rows, 1)
	assert.Equal(t, "s1", rows[0].SymbolGuid)
	assert.Equal(t, "1h", rows[0].Interval)
	assert.Equal(t, "2026-08-01 00:00:00", rows[0].OpenTime)
	assert.Equal(t, "1.3766", rows[0].OpenPrice)
	assert.Equal(t, "1.38", rows[0].HighPrice)
	assert.Equal(t, "1.37", rows[0].LowPrice)
	assert.Equal(t, "1.375", rows[0].ClosePrice)
	assert.Equal(t, "1376600001.23456789", rows[0].Volume)
}

func TestBuildKlineV2JSONRowsUsesExplicitOpenTime(t *testing.T) {
	legacyCreated := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	explicitOpen := legacyCreated.Add(time.Hour)
	rows := BuildKlineV2JSONRows([]*database.SymbolKline{{
		MarketID:   "es1",
		SymbolGuid: "s1",
		Interval:   "1h",
		OpenTime:   explicitOpen,
		OpenPrice:  "100000000",
		HighPrice:  "200000000",
		LowPrice:   "50000000",
		ClosePrice: "150000000",
		Volume:     "300000000",
		MarketCap:  "0",
		IsActive:   true,
		IngestedAt: explicitOpen.Add(time.Minute),
		UpdatedAt:  explicitOpen.Add(2 * time.Minute),
		SyncSeq:    101,
		CreatedAt:  legacyCreated,
	}}, "binance:BTC/USDT:spot")

	assert.Len(t, rows, 1)
	assert.Equal(t, "es1", rows[0].MarketID)
	assert.Equal(t, "binance:BTC/USDT:spot", rows[0].MarketCode)
	assert.Equal(t, "2026-08-01 01:00:00", rows[0].OpenTime)
	assert.Equal(t, int64(101), rows[0].SyncSeq)
}

func TestBuildSnapshotJSONRows(t *testing.T) {
	captured := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	change := "0.0123"
	list := []*database.MarketSnapshotRow{
		{SymbolGuid: "s1", Exchange: "Binance", Price: "137660000", Volume: "1000000000000", MarketCap: "0", Change24hPct: &change},
		{SymbolGuid: "h-s-BTC", Exchange: "Hyperliquid", Price: "6700000000000", Volume: "0", MarketCap: "0"},
	}
	rows := BuildSnapshotJSONRows(list, captured)
	assert.Len(t, rows, 2)

	assert.Equal(t, "2026-08-01 12:00:00", rows[0].CapturedAt)
	assert.Equal(t, "Binance", rows[0].Exchange)
	assert.Equal(t, "1.3766", rows[0].Price)
	assert.Equal(t, "10000", rows[0].Volume)
	assert.Equal(t, "0.0123", rows[0].Change24h)

	// The old rollback table cannot store NULL. Only this compatibility stream
	// maps unknown to zero; public readers and the v2 table remain nullable.
	assert.Equal(t, "0", rows[1].Change24h)
	assert.Equal(t, "67000", rows[1].Price)

	v2Rows := BuildSnapshotV2JSONRows(list, captured)
	assert.Equal(t, "0.0123", *v2Rows[0].Change24hPct)
	assert.Nil(t, v2Rows[1].Change24hPct)
}

func TestStreamLoadLabel(t *testing.T) {
	ts := time.Unix(1785974400, 123)
	label := StreamLoadLabel("dwd_symbol_kline", ts)
	assert.Contains(t, label, "s78-dw-dwd_symbol_kline-")
	assert.NotEqual(t,
		StreamLoadLabel("dwd_symbol_kline", ts),
		StreamLoadLabel("dwd_symbol_kline", ts.Add(time.Nanosecond)),
	)
}

func TestStreamLoadSeqLabelIsDeterministic(t *testing.T) {
	payload := []byte(`[{"market_id":"es1"}]`)
	first := StreamLoadSeqLabel("dwd_market_kline_v2", "kline-v2:es1:1m", 10, 20, payload)
	assert.Equal(t, first, StreamLoadSeqLabel("dwd_market_kline_v2", "kline-v2:es1:1m", 10, 20, payload))
	assert.NotEqual(t, first, StreamLoadSeqLabel("dwd_market_kline_v2", "kline-v2:es1:1m", 10, 21, payload))
	assert.NotEqual(t, first, StreamLoadSeqLabel("dwd_market_kline_v2", "kline-v2:es1:1m", 10, 20, []byte("different")))
}

func TestReplayScanStartRecoversLateLowerSequence(t *testing.T) {
	// T2 (seq 101) committed first and advanced the persisted watermark. T1
	// (seq 100) committed afterwards. The next scan starts below both rows.
	assert.Equal(t, int64(0), ReplayScanStart(101, 1000))
	assert.Equal(t, int64(4000), ReplayScanStart(5000, 1000))
}
