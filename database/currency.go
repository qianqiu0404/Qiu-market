package database

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
)

type Currency struct {
	Guid         string    `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	CurrencyName string    `gorm:"column:currency_name;type:varchar(100);not null" json:"currency_name"`
	CurrencyCode string    `gorm:"column:currency_code;type:varchar(100);not null" json:"currency_code"`
	Rate         float64   `gorm:"column:rate;type:numeric(65,18);not null;default:0" json:"rate"`
	BuySpread    float64   `gorm:"column:buy_spread;type:numeric(65,18);not null;default:0" json:"buy_spread"`
	SellSpread   float64   `gorm:"column:sell_spread;type:numeric(65,18);not null;default:0" json:"sell_spread"`
	IsActive     bool      `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Currency) TableName() string {
	return "currency"
}

type CurrencyView interface {
	QueryCurrencyList(page, pageSize int64) ([]*Currency, int64, error)
}

type CurrencyDB interface {
	CurrencyView

	StoreCurrencies([]Currency) error
	StoreCurrency(*Currency) error
}

type currencyDB struct {
	gorm *gorm.DB
}

func NewCurrencyDB(db *gorm.DB) CurrencyDB {
	return &currencyDB{gorm: db}
}

func (c *currencyDB) QueryCurrencyList(page, pageSize int64) ([]*Currency, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	var list []*Currency
	query := c.gorm.Model(&Currency{})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		log.Error("Failed to query currency count", "error", err)
		return nil, 0, err
	}

	if err := query.Order("created_at DESC").Limit(int(pageSize)).Offset(int(offset)).Find(&list).Error; err != nil {
		log.Error("Failed to query currency list", "error", err)
		return nil, 0, err
	}

	return list, total, nil
}

func (c *currencyDB) StoreCurrencies(currencies []Currency) error {
	if err := c.gorm.Table("currency").CreateInBatches(&currencies, len(currencies)).Error; err != nil {
		log.Error("Failed to store currency list", "error", err)
		return err
	}
	return nil
}

func (c *currencyDB) StoreCurrency(currency *Currency) error {
	if err := c.gorm.Table("currency").Create(currency).Error; err != nil {
		log.Error("Failed to store currency", "error", err)
		return err
	}
	return nil
}
