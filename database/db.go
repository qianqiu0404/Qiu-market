package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	stdlog "log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"gorm.io/driver/postgres"

	"github.com/the-web3/s78-market-services/common/retry"
	"github.com/the-web3/s78-market-services/config"
)

type DB struct {
	gorm              *gorm.DB
	Asset             AssetDB
	Currency          CurrencyDB
	Exchange          ExchangeDB
	ExchangeSymbol    ExchangeSymbolDB
	Symbol            SymbolDB
	SymbolKline       SymbolKlineDB
	SymbolMarket      SymbolMarketDB
	DwSync            DwSyncDB
	DWAcceptance      DWAcceptanceDB
	ProviderStatus    ProviderStatusDB
	KlineRepair       KlineRepairDB
	KlineRetention    KlineRetentionDB
	MarketAggregation MarketAggregationDB
}

func NewDB(ctx context.Context, dbConfig config.DBConfig) (*DB, error) {
	dsn := fmt.Sprintf("host=%s dbname=%s sslmode=disable", dbConfig.Host, dbConfig.Name)
	if dbConfig.Port != 0 {
		dsn += fmt.Sprintf(" port=%d", dbConfig.Port)
	}
	if dbConfig.User != "" {
		dsn += fmt.Sprintf(" user=%s", dbConfig.User)
	}
	if dbConfig.Password != "" {
		dsn += fmt.Sprintf(" password=%s", dbConfig.Password)
	}

	gormConfig := gorm.Config{
		SkipDefaultTransaction: true,
		CreateBatchSize:        3_000,
		Logger: gormlogger.New(
			stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags),
			gormlogger.Config{
				SlowThreshold:             2 * time.Second,
				LogLevel:                  gormlogger.Warn,
				IgnoreRecordNotFoundError: true,
				ParameterizedQueries:      true,
				Colorful:                  false,
			},
		),
	}

	retryStrategy := &retry.ExponentialStrategy{Min: 1000, Max: 20_000, MaxJitter: 250}
	gorm, err := retry.Do[*gorm.DB](context.Background(), 10, retryStrategy, func() (*gorm.DB, error) {
		gorm, err := gorm.Open(postgres.Open(dsn), &gormConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to database: %w", err)
		}
		return gorm, nil
	})

	if err != nil {
		return nil, err
	}

	db := &DB{
		gorm:              gorm,
		Asset:             NewAssetDB(gorm),
		Currency:          NewCurrencyDB(gorm),
		Exchange:          NewExchangeDB(gorm),
		ExchangeSymbol:    NewExchangeSymbolDB(gorm),
		Symbol:            NewSymbolDB(gorm),
		SymbolKline:       NewSymbolKlineDB(gorm),
		SymbolMarket:      NewSymbolMarketDB(gorm),
		DwSync:            NewDwSyncDB(gorm),
		DWAcceptance:      NewDWAcceptanceDB(gorm),
		ProviderStatus:    NewProviderStatusDB(gorm),
		KlineRepair:       NewKlineRepairDB(gorm),
		KlineRetention:    NewKlineRetentionDB(gorm),
		MarketAggregation: NewMarketAggregationDB(gorm),
	}
	return db, nil
}

func (db *DB) Transaction(fn func(db *DB) error) error {
	return db.gorm.Transaction(func(tx *gorm.DB) error {
		txDB := &DB{
			gorm:              tx,
			Asset:             NewAssetDB(tx),
			Currency:          NewCurrencyDB(tx),
			Exchange:          NewExchangeDB(tx),
			ExchangeSymbol:    NewExchangeSymbolDB(tx),
			Symbol:            NewSymbolDB(tx),
			SymbolKline:       NewSymbolKlineDB(tx),
			SymbolMarket:      NewSymbolMarketDB(tx),
			DwSync:            NewDwSyncDB(tx),
			DWAcceptance:      NewDWAcceptanceDB(tx),
			ProviderStatus:    NewProviderStatusDB(tx),
			KlineRepair:       NewKlineRepairDB(tx),
			KlineRetention:    NewKlineRetentionDB(tx),
			MarketAggregation: NewMarketAggregationDB(tx),
		}
		return fn(txDB)
	})
}

// ApplyMarketSnapshot guarantees that the row-level ordering decision and the
// write execute inside one PostgreSQL transaction.
func (db *DB) ApplyMarketSnapshot(input MarketSnapshotInput) (MarketSnapshotResult, error) {
	var result MarketSnapshotResult
	err := db.Transaction(func(tx *DB) error {
		var err error
		result, err = tx.SymbolMarket.ApplyMarketSnapshot(input)
		return err
	})
	return result, err
}

func (db *DB) Close() error {
	sql, err := db.gorm.DB()
	if err != nil {
		return err
	}
	return sql.Close()
}

func (db *DB) ExecuteSQLMigration(migrationsFolder string) error {
	files, err := collectSQLMigrations(migrationsFolder)
	if err != nil {
		return err
	}
	if err := db.gorm.Exec(`
		CREATE TABLE IF NOT EXISTS s78_schema_migrations (
			filename TEXT PRIMARY KEY,
			checksum_sha256 TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
		)
	`).Error; err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	type appliedMigration struct {
		Filename string `gorm:"column:filename"`
		Checksum string `gorm:"column:checksum_sha256"`
	}
	var appliedRows []appliedMigration
	if err := db.gorm.Table("s78_schema_migrations").Find(&appliedRows).Error; err != nil {
		return fmt.Errorf("read migration ledger: %w", err)
	}
	applied := make(map[string]string, len(appliedRows))
	for _, row := range appliedRows {
		applied[row.Filename] = row.Checksum
	}
	for _, migration := range files {
		if checksum, ok := applied[migration.filename]; ok {
			if checksum != migration.checksum {
				return fmt.Errorf(
					"migration %s changed after it was applied: recorded=%s current=%s",
					migration.filename, checksum, migration.checksum,
				)
			}
			continue
		}
		if execErr := db.gorm.Exec(string(migration.content)).Error; execErr != nil {
			return fmt.Errorf("execute migration %s: %w", migration.filename, execErr)
		}
		if insertErr := db.gorm.Exec(`
			INSERT INTO s78_schema_migrations(filename, checksum_sha256)
			VALUES (?, ?)
			ON CONFLICT (filename) DO NOTHING
		`, migration.filename, migration.checksum).Error; insertErr != nil {
			return fmt.Errorf("record migration %s: %w", migration.filename, insertErr)
		}
	}
	return nil
}

type sqlMigrationFile struct {
	filename string
	content  []byte
	checksum string
}

func collectSQLMigrations(migrationsFolder string) ([]sqlMigrationFile, error) {
	files := make([]sqlMigrationFile, 0)
	err := filepath.Walk(migrationsFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("process migration path %s: %w", path, err)
		}
		if info.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(info.Name()), ".sql") {
			return nil
		}
		fileContent, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read migration file %s: %w", path, readErr)
		}
		digest := sha256.Sum256(fileContent)
		files = append(files, sqlMigrationFile{
			filename: filepath.Base(path),
			content:  fileContent,
			checksum: hex.EncodeToString(digest[:]),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].filename < files[j].filename
	})
	return files, nil
}
