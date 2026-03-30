package database

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
)

type SymbolMarketCurrey struct {
	Guid       string    `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	SymbolGuid string    `gorm:"column:symbol_guid;type:varchar(100);not null;default:''" json:"symbol_guid"`
	CurreyGuid string    `gorm:"column:currey_guid;type:varchar(100);not null;default:''" json:"currey_guid"`
	Price      string    `gorm:"column:price;type:numeric(65,18);not null;default:0" json:"price"`
	AskPrice   string    `gorm:"column:ask_price;type:numeric(65,18);not null;default:0" json:"ask_price"`
	BidPrice   string    `gorm:"column:bid_price;type:numeric(65,18);not null;default:0" json:"bid_price"`
	IsActive   bool      `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SymbolMarketCurrey) TableName() string {
	return "symbol_market_currey"
}

type SymbolMarketCurreyView interface {
	QuerySymbolMarketCurreyList(page, pageSize int64) ([]*SymbolMarketCurrey, int64, error)
}

type SymbolMarketCurreyDB interface {
	SymbolMarketCurreyView

	StoreSymbolMarketCurreys([]SymbolMarketCurrey) error
	StoreSymbolMarketCurrey(*SymbolMarketCurrey) error
}

type symbolMarketCurreyDB struct {
	gorm *gorm.DB
}

func NewSymbolMarketCurreyDB(db *gorm.DB) SymbolMarketCurreyDB {
	return &symbolMarketCurreyDB{gorm: db}
}

func (s *symbolMarketCurreyDB) QuerySymbolMarketCurreyList(page, pageSize int64) ([]*SymbolMarketCurrey, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	var list []*SymbolMarketCurrey
	query := s.gorm.Model(&SymbolMarketCurrey{})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		log.Error("Failed to query symbol_market_currey count", "error", err)
		return nil, 0, err
	}

	if err := query.Order("created_at DESC").
		Limit(int(pageSize)).
		Offset(int(offset)).
		Find(&list).Error; err != nil {
		log.Error("Failed to query symbol_market_currey list", "error", err)
		return nil, 0, err
	}

	return list, total, nil
}

func (s *symbolMarketCurreyDB) StoreSymbolMarketCurreys(list []SymbolMarketCurrey) error {
	if err := s.gorm.Table("symbol_market_currey").
		CreateInBatches(&list, len(list)).Error; err != nil {
		log.Error("Failed to store symbol_market_currey list", "error", err)
		return err
	}
	return nil
}

func (s *symbolMarketCurreyDB) StoreSymbolMarketCurrey(data *SymbolMarketCurrey) error {
	if err := s.gorm.Table("symbol_market_currey").
		Create(&data).Error; err != nil {
		log.Error("Failed to store symbol_market_currey", "error", err)
		return err
	}
	return nil
}
