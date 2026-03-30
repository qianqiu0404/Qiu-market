package database

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
)

type ExchangeSymbol struct {
	Guid         string    `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	ExchangeGuid string    `gorm:"column:exchange_guid;type:varchar(100);not null" json:"exchange_guid"`
	SymbolGuid   string    `gorm:"column:symbol_guid;type:varchar(100);not null" json:"symbol_guid"`
	Price        float64   `gorm:"column:price;type:numeric(65,18);not null;default:0" json:"price"`
	AskPrice     float64   `gorm:"column:ask_price;type:numeric(65,18);not null;default:0" json:"ask_price"`
	BidPrice     float64   `gorm:"column:bid_price;type:numeric(65,18);not null;default:0" json:"bid_price"`
	Volume       string    `gorm:"column:volume;type:text;not null" json:"volume"`
	Radio        float64   `gorm:"column:radio;type:numeric(65,18);not null;default:0" json:"radio"`
	IsActive     bool      `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (ExchangeSymbol) TableName() string {
	return "exchange_symbol"
}

type ExchangeSymbolView interface {
	QueryExchangeSymbolList(page, pageSize int64) ([]*ExchangeSymbol, int64, error)
}

type ExchangeSymbolDB interface {
	ExchangeSymbolView

	StoreExchangeSymbols([]ExchangeSymbol) error
	StoreExchangeSymbol(*ExchangeSymbol) error
}

type exchangeSymbolDB struct {
	gorm *gorm.DB
}

func NewExchangeSymbolDB(db *gorm.DB) ExchangeSymbolDB {
	return &exchangeSymbolDB{gorm: db}
}

func (e *exchangeSymbolDB) QueryExchangeSymbolList(page, pageSize int64) ([]*ExchangeSymbol, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	var list []*ExchangeSymbol
	query := e.gorm.Model(&ExchangeSymbol{})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		log.Error("Failed to query exchange_symbol count", "error", err)
		return nil, 0, err
	}

	if err := query.Order("created_at DESC").Limit(int(pageSize)).Offset(int(offset)).Find(&list).Error; err != nil {
		log.Error("Failed to query exchange_symbol list", "error", err)
		return nil, 0, err
	}

	return list, total, nil
}

func (e *exchangeSymbolDB) StoreExchangeSymbols(list []ExchangeSymbol) error {
	if err := e.gorm.Table("exchange_symbol").CreateInBatches(&list, len(list)).Error; err != nil {
		log.Error("Failed to store exchange_symbol list", "error", err)
		return err
	}
	return nil
}

func (e *exchangeSymbolDB) StoreExchangeSymbol(item *ExchangeSymbol) error {
	if err := e.gorm.Table("exchange_symbol").Create(item).Error; err != nil {
		log.Error("Failed to store exchange_symbol", "error", err)
		return err
	}
	return nil
}
