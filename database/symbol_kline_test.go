package database

import (
	"testing"
	"time"
)

func TestKlineMateriallyChanged(t *testing.T) {
	base := SymbolKline{
		MarketID: "es1", Interval: "1m", OpenTime: time.Unix(100, 0),
		OpenPrice: "1", HighPrice: "2", LowPrice: "0.5", ClosePrice: "1.5",
		Volume: "10", MarketCap: "0", IsActive: true,
		IngestedAt: time.Unix(101, 0), UpdatedAt: time.Unix(102, 0), SyncSeq: 7,
	}
	touched := base
	touched.UpdatedAt = time.Unix(999, 0)
	touched.IngestedAt = time.Unix(999, 0)
	touched.SyncSeq = 999
	if KlineMateriallyChanged(base, touched) {
		t.Fatal("audit-only fields must not count as a material K-line change")
	}

	changed := base
	changed.ClosePrice = "1.6"
	if !KlineMateriallyChanged(base, changed) {
		t.Fatal("OHLCV change must count as a material K-line change")
	}
}
