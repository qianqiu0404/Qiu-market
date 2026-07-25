package database

import (
	"os"
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
