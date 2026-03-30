package database

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
)

type Symbol struct {
	Guid           string    `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	SymbolName     string    `gorm:"column:symbol_name;type:varchar(100);not null" json:"symbol_name"`
	BaseAssetGuid  string    `gorm:"column:base_asset_guid;type:varchar(100);not null" json:"base_asset_guid"`
	QuoteAssetGuid string    `gorm:"column:qoute_asset_guid;type:varchar(100);not null" json:"quote_asset_guid"`
	MarketType     string    `gorm:"column:market_type;type:varchar(100);not null;default:'SPOT'" json:"market_type"`
	IsActive       bool      `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Symbol) TableName() string {
	return "symbol"
}

type SymbolView interface {
	QuerySymbolList(page, pageSize int64) ([]*Symbol, int64, error)
}

type SymbolDB interface {
	SymbolView

	StoreSymbols([]Symbol) error
	StoreSymbol(*Symbol) error
}

type symbolDB struct {
	gorm *gorm.DB
}

func NewSymbolDB(db *gorm.DB) SymbolDB {
	return &symbolDB{gorm: db}
}

func (s *symbolDB) QuerySymbolList(page, pageSize int64) ([]*Symbol, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	var list []*Symbol
	query := s.gorm.Model(&Symbol{})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		log.Error("Failed to query symbol count", "error", err)
		return nil, 0, err
	}

	if err := query.Order("created_at DESC").Limit(int(pageSize)).Offset(int(offset)).Find(&list).Error; err != nil {
		log.Error("Failed to query symbol list", "error", err)
		return nil, 0, err
	}

	return list, total, nil
}

func (s *symbolDB) StoreSymbols(symbols []Symbol) error {
	if err := s.gorm.Table("symbol").CreateInBatches(&symbols, len(symbols)).Error; err != nil {
		log.Error("Failed to store symbol list", "error", err)
		return err
	}
	return nil
}

func (s *symbolDB) StoreSymbol(symbol *Symbol) error {
	if err := s.gorm.Table("symbol").Create(symbol).Error; err != nil {
		log.Error("Failed to store symbol", "error", err)
		return err
	}
	return nil
}
