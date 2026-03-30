package database

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
)

type ExchangeSymbolKline struct {
	Guid         string    `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	ExchangeGuid string    `gorm:"column:exchange_guid;type:varchar(100);not null;default:''" json:"exchange_guid"`
	SymbolGuid   string    `gorm:"column:symbol_guid;type:varchar(100);not null;default:''" json:"symbol_guid"`
	OpenPrice    string    `gorm:"column:open_price;type:numeric(65,18);not null;default:0" json:"open_price"`
	ClosePrice   string    `gorm:"column:close_price;type:numeric(65,18);not null;default:0" json:"close_price"`
	HighPrice    string    `gorm:"column:high_price;type:numeric(65,18);not null;default:0" json:"high_price"`
	LowPrice     string    `gorm:"column:low_price;type:numeric(65,18);not null;default:0" json:"low_price"`
	Volume       string    `gorm:"column:volume;type:uint256;not null;default:0" json:"volume"`
	MarketCap    string    `gorm:"column:market_cap;type:uint256;not null;default:0" json:"market_cap"`
	IsActive     bool      `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt    time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (ExchangeSymbolKline) TableName() string {
	return "exchange_symbol_kline"
}

type ExchangeSymbolKlineView interface {
	QueryExchangeSymbolKlineList(page, pageSize int64) ([]*ExchangeSymbolKline, int64, error)
}

type ExchangeSymbolKlineDB interface {
	ExchangeSymbolKlineView

	StoreExchangeSymbolKlines([]ExchangeSymbolKline) error
	StoreExchangeSymbolKline(*ExchangeSymbolKline) error
}

type exchangeSymbolKlineDB struct {
	gorm *gorm.DB
}

func NewExchangeSymbolKlineDB(db *gorm.DB) ExchangeSymbolKlineDB {
	return &exchangeSymbolKlineDB{gorm: db}
}

func (e *exchangeSymbolKlineDB) QueryExchangeSymbolKlineList(page, pageSize int64) ([]*ExchangeSymbolKline, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	var list []*ExchangeSymbolKline
	query := e.gorm.Model(&ExchangeSymbolKline{})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		log.Error("Failed to query exchange_symbol_kline count", "error", err)
		return nil, 0, err
	}

	if err := query.Order("created_at DESC").
		Limit(int(pageSize)).
		Offset(int(offset)).
		Find(&list).Error; err != nil {
		log.Error("Failed to query exchange_symbol_kline list", "error", err)
		return nil, 0, err
	}

	return list, total, nil
}

func (e *exchangeSymbolKlineDB) StoreExchangeSymbolKlines(list []ExchangeSymbolKline) error {
	if err := e.gorm.Table("exchange_symbol_kline").
		CreateInBatches(&list, len(list)).Error; err != nil {
		log.Error("Failed to store exchange_symbol_kline list", "error", err)
		return err
	}
	return nil
}

func (e *exchangeSymbolKlineDB) StoreExchangeSymbolKline(data *ExchangeSymbolKline) error {
	if err := e.gorm.Table("exchange_symbol_kline").
		Create(&data).Error; err != nil {
		log.Error("Failed to store exchange_symbol_kline", "error", err)
		return err
	}
	return nil
}
