package dw

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReconcileRetryDelayIsBounded(t *testing.T) {
	assert.Equal(t, time.Duration(0), reconcileRetryDelay(0))
	assert.Equal(t, 5*time.Minute, reconcileRetryDelay(1))
	assert.Equal(t, 10*time.Minute, reconcileRetryDelay(2))
	assert.Equal(t, time.Hour, reconcileRetryDelay(100))
}

func TestDiffKlineV2(t *testing.T) {
	base := KlineV2JSONRow{
		MarketID: "es1", MarketCode: "binance:BTC/USDT:spot", SymbolGuid: "s1",
		Interval: "1m", OpenTime: "2026-07-22 00:00:00",
		OpenPrice: "1", HighPrice: "2", LowPrice: "0.5", ClosePrice: "1.5",
		Volume: "10", MarketCap: "0", IsActive: true, SyncSeq: 10,
	}
	missing := base
	missing.OpenTime = "2026-07-22 00:01:00"
	missing.SyncSeq = 11
	extra := base
	extra.OpenTime = "2026-07-22 00:02:00"
	extra.SyncSeq = 12
	mismatched := base
	mismatched.ClosePrice = "1.6"

	diff := DiffKlineV2(
		[]KlineV2JSONRow{base, missing},
		[]KlineV2JSONRow{mismatched, extra},
	)
	assert.Equal(t, []string{"es1|1m|2026-07-22 00:01:00"}, diff.Missing)
	assert.Equal(t, []string{"es1|1m|2026-07-22 00:02:00"}, diff.Extra)
	assert.Equal(t, []string{"es1|1m|2026-07-22 00:00:00"}, diff.Mismatched)
	assert.True(t, diff.BlocksCutover())
}

func TestDiffKlineV2NormalizesDecimalScale(t *testing.T) {
	pg := KlineV2JSONRow{MarketID: "es1", Interval: "1m", OpenTime: "2026-07-22 00:00:00", OpenPrice: "1", SyncSeq: 1}
	doris := pg
	doris.OpenPrice = "1.000000000000"
	assert.Empty(t, DiffKlineV2([]KlineV2JSONRow{pg}, []KlineV2JSONRow{doris}).Mismatched)
}

func TestExcludePendingKlineVersions(t *testing.T) {
	staleDoris := KlineV2JSONRow{
		MarketID: "es1", Interval: "1m",
		OpenTime: "2026-07-24 01:30:00", SyncSeq: 100,
	}
	currentPG := staleDoris
	currentPG.SyncSeq = 120

	filtered := excludePendingKlineVersions(
		[]KlineV2JSONRow{staleDoris},
		[]KlineV2JSONRow{currentPG},
		110,
	)
	assert.Empty(t, filtered, "a key with a newer PG version above the watermark is pending, not extra")

	filtered = excludePendingKlineVersions(
		[]KlineV2JSONRow{staleDoris},
		[]KlineV2JSONRow{currentPG},
		120,
	)
	assert.Equal(t, []KlineV2JSONRow{staleDoris}, filtered)
}

func TestMissingOnlyDoesNotBlockCutoverAfterAutomaticRepair(t *testing.T) {
	diff := KlineV2Diff{Missing: []string{"es1|1m|2026-07-22 00:00:00"}}
	assert.False(t, diff.BlocksCutover())
}
