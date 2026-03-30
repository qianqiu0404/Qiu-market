package database

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Exchange struct {
	Guid      string         `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	Name      string         `gorm:"column:name;type:varchar(100);not null;unique" json:"name"`
	Config    datatypes.JSON `gorm:"column:config;type:jsonb" json:"config"`
	IsActive  bool           `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt time.Time      `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Exchange) TableName() string {
	return "exchange"
}

type ExchangeView interface {
	QueryExchangeList(page, pageSize int64) ([]*Exchange, int64, error)
}

type ExchangeDB interface {
	ExchangeView

	StoreExchanges([]Exchange) error
	StoreExchange(*Exchange) error
}

type exchangeDB struct {
	gorm *gorm.DB
}

func NewExchangeDB(db *gorm.DB) ExchangeDB {
	return &exchangeDB{gorm: db}
}

func (e *exchangeDB) QueryExchangeList(page, pageSize int64) ([]*Exchange, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize

	var list []*Exchange
	query := e.gorm.Model(&Exchange{})

	var total int64
	if err := query.Count(&total).Error; err != nil {
		log.Error("Failed to query exchange count", "error", err)
		return nil, 0, err
	}

	if err := query.Order("created_at DESC").Limit(int(pageSize)).Offset(int(offset)).Find(&list).Error; err != nil {
		log.Error("Failed to query exchange list", "error", err)
		return nil, 0, err
	}

	return list, total, nil
}

func (e *exchangeDB) StoreExchanges(exchanges []Exchange) error {
	if err := e.gorm.Table("exchange").CreateInBatches(&exchanges, len(exchanges)).Error; err != nil {
		log.Error("Failed to store exchange list", "error", err)
		return err
	}
	return nil
}

func (e *exchangeDB) StoreExchange(exchange *Exchange) error {
	if err := e.gorm.Table("exchange").Create(exchange).Error; err != nil {
		log.Error("Failed to store exchange", "error", err)
		return err
	}
	return nil
}
