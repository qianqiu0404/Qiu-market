package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Run against an isolated migrated PostgreSQL database only:
// S78_TEST_DATABASE_DSN='host=... dbname=... user=... password=... sslmode=disable' go test ./database -run Integration
func TestIntegrationSymbolKlineUpsertChangeDetection(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	tx := gormDB.Begin()
	defer tx.Rollback()

	var original SymbolKline
	if err := tx.Order("sync_seq ASC").First(&original).Error; err != nil {
		t.Fatal(err)
	}
	writer := NewSymbolKlineDB(tx)

	noOp := original
	noOp.UpdatedAt = time.Now().Add(time.Hour)
	if err := writer.StoreSymbolKline(&noOp); err != nil {
		t.Fatal(err)
	}
	var afterNoOp SymbolKline
	if err := tx.Where("guid = ?", original.Guid).First(&afterNoOp).Error; err != nil {
		t.Fatal(err)
	}
	if afterNoOp.SyncSeq != original.SyncSeq || !afterNoOp.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("no-op upsert changed audit state: before seq=%d updated=%s; after seq=%d updated=%s",
			original.SyncSeq, original.UpdatedAt, afterNoOp.SyncSeq, afterNoOp.UpdatedAt)
	}

	changed := original
	if changed.ClosePrice == "1" {
		changed.ClosePrice = "2"
	} else {
		changed.ClosePrice = "1"
	}
	if err := writer.StoreSymbolKline(&changed); err != nil {
		t.Fatal(err)
	}
	var afterChange SymbolKline
	if err := tx.Where("guid = ?", original.Guid).First(&afterChange).Error; err != nil {
		t.Fatal(err)
	}
	if afterChange.SyncSeq <= original.SyncSeq {
		t.Fatalf("material upsert did not advance sync_seq: before=%d after=%d", original.SyncSeq, afterChange.SyncSeq)
	}
}

func TestIntegrationMarketSnapshotDualWriteAndCorrection(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	tx := gormDB.Begin()
	defer tx.Rollback()

	var current SymbolMarket
	if err := tx.Order("market_id ASC").First(&current).Error; err != nil {
		t.Fatal(err)
	}
	writer := NewSymbolMarketDB(tx)
	change := "12.3456"
	sourceTime := time.Now().UTC()
	observedAt := sourceTime.Add(time.Second)
	result, err := writer.ApplyMarketSnapshot(MarketSnapshotInput{
		MarketID:       current.MarketID,
		SymbolGuid:     current.SymbolGuid,
		Price:          current.Price,
		AskPrice:       current.AskPrice,
		BidPrice:       current.BidPrice,
		Volume:         current.Volume,
		Change24hPct:   &change,
		IsActive:       current.IsActive,
		ObservedAt:     observedAt,
		SourceTime:     &sourceTime,
		SourceTimeKind: stringPointer("ticker_window_close"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != MarketSnapshotUpdated && result.Action != MarketSnapshotCorrection {
		t.Fatalf("snapshot action = %q; want material update", result.Action)
	}
	var updated SymbolMarket
	if err := tx.Where("market_id = ?", current.MarketID).First(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Change24hPct == nil ||
		!equalNumericString(*updated.Change24hPct, change) ||
		!equalNumericString(updated.Radio, change) {
		t.Fatalf("canonical/legacy dual write = %v/%q; want %q/%q",
			updated.Change24hPct, updated.Radio, change, change)
	}
}

func TestIntegrationCompletedRepairTaskReopensWhenGapStillExists(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	tx := gormDB.Begin()
	defer tx.Rollback()

	var market ExchangeSymbol
	if err := tx.Where("source_symbol IS NOT NULL").Order("guid ASC").First(&market).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	task := KlineRepairTask{
		Provider:      "binance",
		MarketID:      market.Guid,
		SourceSymbol:  *market.SourceSymbol,
		Interval:      "1m",
		GapStart:      start,
		GapEnd:        start.Add(time.Minute),
		NextAttemptAt: start,
	}
	task.TaskKey = RepairTaskKey(task.Provider, task.MarketID, task.Interval, task.GapStart, task.GapEnd)
	writer := NewKlineRepairDB(tx)
	if _, err := writer.UpsertRepairTasks([]KlineRepairTask{task}); err != nil {
		t.Fatal(err)
	}
	if err := writer.CompleteRepairTask(task.TaskKey, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.UpsertRepairTasks([]KlineRepairTask{task}); err != nil {
		t.Fatal(err)
	}
	var reopened KlineRepairTask
	if err := tx.Where("task_key = ?", task.TaskKey).First(&reopened).Error; err != nil {
		t.Fatal(err)
	}
	if reopened.Status != "pending" || reopened.AttemptCount != 0 {
		t.Fatalf("reopened repair task = status %q attempts %d; want pending/0",
			reopened.Status, reopened.AttemptCount)
	}
}

func TestIntegrationKlineRetentionDeletesOnlyExpiredBoundedIntervals(t *testing.T) {
	dsn := os.Getenv("S78_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_DATABASE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := gormDB.Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(databaseName, "qiu_market_retention_test_") {
		t.Skip("K-line retention integration test requires a disposable qiu_market_retention_test_* database")
	}

	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	fixtures := []struct {
		guid     string
		interval string
		openTime time.Time
	}{
		{guid: "retention-old-1m", interval: "1m", openTime: now.Add(-8 * 24 * time.Hour)},
		{guid: "retention-new-1m", interval: "1m", openTime: now.Add(-6 * 24 * time.Hour)},
		{guid: "retention-old-15m", interval: "15m", openTime: now.Add(-91 * 24 * time.Hour)},
		{guid: "retention-old-1h", interval: "1h", openTime: now.Add(-366 * 24 * time.Hour)},
		{guid: "retention-old-1d", interval: "1d", openTime: now.Add(-10 * 365 * 24 * time.Hour)},
	}
	for _, fixture := range fixtures {
		if err := gormDB.Exec(`
INSERT INTO symbol_kline(
	guid, symbol_guid, market_id, "interval", open_time,
	open_price, high_price, low_price, close_price, volume, market_cap,
	is_active, created_at, updated_at, ingested_at
) VALUES (?, 'retention-symbol', ?, ?, ?, 1, 1, 1, 1, 0, 0, TRUE, ?, ?, ?)
`, fixture.guid, "retention-market-"+fixture.interval, fixture.interval, fixture.openTime,
			fixture.openTime, fixture.openTime, fixture.openTime).Error; err != nil {
			t.Fatal(err)
		}
	}

	store := NewKlineRetentionDB(gormDB)
	result, err := store.Run(
		context.Background(),
		now,
		ExtremeSpaceKlineRetentionPolicies(),
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted["1m"] < 1 || result.Deleted["15m"] < 1 || result.Deleted["1h"] < 1 {
		t.Fatalf("deleted rows = %+v", result.Deleted)
	}
	var remaining []string
	if err := gormDB.Model(&SymbolKline{}).
		Where("guid LIKE 'retention-%'").
		Order("guid").
		Pluck("guid", &remaining).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{"retention-new-1m", "retention-old-1d"}
	if len(remaining) != len(want) {
		t.Fatalf("remaining = %v; want %v", remaining, want)
	}
	for index := range want {
		if remaining[index] != want[index] {
			t.Fatalf("remaining = %v; want %v", remaining, want)
		}
	}
}

func TestIntegrationKlineStorageStatsReadOnly(t *testing.T) {
	dsn := os.Getenv("S78_TEST_STORAGE_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_STORAGE_DSN is not set")
	}
	gormDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stats, err := NewKlineRetentionDB(gormDB).QueryStorageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.DatabaseBytes <= 0 || stats.TableBytes <= 0 || len(stats.Intervals) != 4 {
		t.Fatalf("storage stats = %+v", stats)
	}
}
