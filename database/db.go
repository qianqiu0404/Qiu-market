package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gorm.io/gorm"

	"github.com/pkg/errors"
	"gorm.io/driver/postgres"

	"github.com/the-web3/s78-market-services/common/retry"
	"github.com/the-web3/s78-market-services/config"
)

type DB struct {
	gorm                *gorm.DB
	Asset               AssetDB
	Currency            CurrencyDB
	Exchange            ExchangeDB
	ExchangeSymbol      ExchangeSymbolDB
	ExchangeSymbolKline ExchangeSymbolKlineDB
	Symbol              SymbolDB
	SymbolKline         SymbolKlineDB
	SymbolMarket        SymbolMarketDB
	SymbolMarketCurrey  SymbolMarketCurreyDB
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
	}

	retryStrategy := &retry.ExponentialStrategy{Min: 1000, Max: 20_000, MaxJitter: 250}
	gorm, err := retry.Do[*gorm.DB](ctx, 10, retryStrategy, func() (*gorm.DB, error) {
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
		gorm:                gorm,
		Asset:               NewAssetDB(gorm),
		Currency:            NewCurrencyDB(gorm),
		Exchange:            NewExchangeDB(gorm),
		ExchangeSymbol:      NewExchangeSymbolDB(gorm),
		ExchangeSymbolKline: NewExchangeSymbolKlineDB(gorm),
		Symbol:              NewSymbolDB(gorm),
		SymbolKline:         NewSymbolKlineDB(gorm),
		SymbolMarket:        NewSymbolMarketDB(gorm),
		SymbolMarketCurrey:  NewSymbolMarketCurreyDB(gorm),
	}
	return db, nil
}

func (db *DB) Transaction(fn func(db *DB) error) error {
	return db.gorm.Transaction(func(tx *gorm.DB) error {
		txDB := &DB{
			gorm:                tx,
			Asset:               NewAssetDB(tx),
			Currency:            NewCurrencyDB(tx),
			Exchange:            NewExchangeDB(tx),
			ExchangeSymbol:      NewExchangeSymbolDB(tx),
			ExchangeSymbolKline: NewExchangeSymbolKlineDB(tx),
			Symbol:              NewSymbolDB(tx),
			SymbolKline:         NewSymbolKlineDB(tx),
			SymbolMarket:        NewSymbolMarketDB(tx),
			SymbolMarketCurrey:  NewSymbolMarketCurreyDB(tx),
		}
		return fn(txDB)
	})
}

func (db *DB) Close() error {
	sql, err := db.gorm.DB()
	if err != nil {
		return err
	}
	return sql.Close()
}

func (db *DB) ExecuteSQLMigration(migrationsFolder string) error {
	err := filepath.Walk(migrationsFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("Failed to process migration file: %s", path))
		}
		if info.IsDir() {
			return nil
		}
		fileContent, readErr := os.ReadFile(path)
		if readErr != nil {
			return errors.Wrap(readErr, fmt.Sprintf("Error reading SQL file: %s", path))
		}

		execErr := db.gorm.Exec(string(fileContent)).Error
		if execErr != nil {
			return errors.Wrap(execErr, fmt.Sprintf("Error executing SQL script: %s", path))
		}
		return nil
	})
	return err
}
