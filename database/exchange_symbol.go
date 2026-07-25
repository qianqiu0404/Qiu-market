package database

import (
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
)

type ExchangeSymbol struct {
	Guid         string    `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	MarketCode   string    `gorm:"column:market_code;type:text;not null;uniqueIndex" json:"market_code"`
	SourceSymbol *string   `gorm:"column:source_symbol;type:text" json:"source_symbol,omitempty"`
	ExchangeGuid string    `gorm:"column:exchange_guid;type:varchar(100);not null" json:"exchange_guid"`
	SymbolGuid   string    `gorm:"column:symbol_guid;type:varchar(100);not null" json:"symbol_guid"`
	Price        float64   `gorm:"column:price;type:numeric(65,18);not null;default:0" json:"price"`
	AskPrice     float64   `gorm:"column:ask_price;type:numeric(65,18);not null;default:0" json:"ask_price"`
	BidPrice     float64   `gorm:"column:bid_price;type:numeric(65,18);not null;default:0" json:"bid_price"`
	Volume       string    `gorm:"column:volume;type:text;not null" json:"volume"`
	Radio        float64   `gorm:"column:radio;type:numeric(65,18);not null;default:0" json:"radio"`
	KlineEnabled bool      `gorm:"column:kline_enabled;type:boolean;not null;default:false" json:"kline_enabled"`
	IsActive     bool      `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (ExchangeSymbol) TableName() string {
	return "exchange_symbol"
}

type ExchangeSymbolView interface {
	QuerySymbolsByExchangeId(exchangeGuid string) ([]*ExchangeSymbol, error)
	QueryExchangeSymbolList(page, pageSize int64) ([]*ExchangeSymbol, int64, error)
	// QueryUniqueActiveMarketBySymbol resolves the legacy symbol identity only
	// when exactly one active venue exists. Multi-venue symbols are ambiguous.
	QueryUniqueActiveMarketBySymbol(symbolGuid string) (*ExchangeSymbol, error)
	// QueryExchangeNamesBySymbolGuids 批量解析 symbol_guid -> 交易所名（经 exchange 表 JOIN）。
	// 用于 dashboard / symbols 接口给每个交易对补充 exchange 字段，避免 N+1 查询。
	QueryExchangeNamesBySymbolGuids(symbolGuids []string) (map[string]string, error)
	QueryMarketMetadataByIDs(marketIDs []string) (map[string]MarketMetadata, error)
	QueryProviderMarkets(provider string) ([]ProviderMarket, error)
	QueryProviderKlineMarkets(provider string) ([]ProviderMarket, error)
	ReconcileProviderKlineSelection(provider string) ([]ProviderMarket, error)
	QueryAssetExternalMappings(provider string) ([]AssetExternalMapping, error)
}

type MarketMetadata struct {
	MarketID   string
	MarketCode string
	SymbolGuid string
	Exchange   string
	MarketType string
}

func (e *exchangeSymbolDB) QueryUniqueActiveMarketBySymbol(symbolGuid string) (*ExchangeSymbol, error) {
	var list []*ExchangeSymbol
	if err := e.gorm.Table("exchange_symbol").
		Where("symbol_guid = ? AND is_active = ?", symbolGuid, true).
		Limit(2).
		Find(&list).Error; err != nil {
		return nil, err
	}
	if len(list) != 1 {
		return nil, fmt.Errorf("ambiguous_market: symbol_guid %q resolves to %d active markets", symbolGuid, len(list))
	}
	return list[0], nil
}

type ExchangeSymbolDB interface {
	ExchangeSymbolView

	StoreExchangeSymbols([]ExchangeSymbol) error
	StoreExchangeSymbol(*ExchangeSymbol) error
	UpdateSourceSymbol(marketID, sourceSymbol string) error
}

type exchangeSymbolDB struct {
	gorm *gorm.DB
}

func (e *exchangeSymbolDB) QuerySymbolsByExchangeId(exchangeGuid string) ([]*ExchangeSymbol, error) {
	var symbols []*ExchangeSymbol
	if err := e.gorm.Table("exchange_symbol").Where("exchange_guid = ? and is_active = ?", exchangeGuid, true).Find(&symbols).Error; err != nil {
		log.Error("Query exchange symbol fail:", err)
		return nil, err
	}
	return symbols, nil
}

func (e *exchangeSymbolDB) QueryExchangeNamesBySymbolGuids(symbolGuids []string) (map[string]string, error) {
	result := make(map[string]string, len(symbolGuids))
	if len(symbolGuids) == 0 {
		return result, nil
	}
	var rows []struct {
		SymbolGuid string `gorm:"column:symbol_guid"`
		Name       string `gorm:"column:name"`
	}
	if err := e.gorm.Table("exchange_symbol").
		Select("exchange_symbol.symbol_guid, exchange.name").
		Joins("JOIN exchange ON exchange.guid = exchange_symbol.exchange_guid").
		Where("exchange_symbol.symbol_guid IN ? AND exchange_symbol.is_active = ?", symbolGuids, true).
		Scan(&rows).Error; err != nil {
		log.Error("Query exchange names by symbol guids fail:", err)
		return nil, err
	}
	for _, r := range rows {
		result[r.SymbolGuid] = r.Name
	}
	return result, nil
}

func (e *exchangeSymbolDB) QueryMarketMetadataByIDs(marketIDs []string) (map[string]MarketMetadata, error) {
	result := make(map[string]MarketMetadata, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		MarketID   string `gorm:"column:market_id"`
		MarketCode string `gorm:"column:market_code"`
		SymbolGuid string `gorm:"column:symbol_guid"`
		Exchange   string `gorm:"column:exchange"`
		MarketType string `gorm:"column:market_type"`
	}
	if err := e.gorm.Table("exchange_symbol").
		Select(`exchange_symbol.guid AS market_id,
			exchange_symbol.market_code,
			exchange_symbol.symbol_guid,
			exchange.name AS exchange,
			symbol.market_type`).
		Joins("JOIN exchange ON exchange.guid = exchange_symbol.exchange_guid").
		Joins("JOIN symbol ON symbol.guid = exchange_symbol.symbol_guid").
		Where("exchange_symbol.guid IN ? AND exchange_symbol.is_active = ?", marketIDs, true).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.MarketID] = MarketMetadata{
			MarketID:   row.MarketID,
			MarketCode: row.MarketCode,
			SymbolGuid: row.SymbolGuid,
			Exchange:   row.Exchange,
			MarketType: row.MarketType,
		}
	}
	return result, nil
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
