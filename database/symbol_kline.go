package database

import (
	"errors"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SymbolKline struct {
	Guid       string    `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	MarketID   string    `gorm:"column:market_id;type:text;not null;index:idx_symbol_kline_business_key,unique" json:"market_id"`
	SymbolGuid string    `gorm:"column:symbol_guid;type:varchar(100);not null;default:''" json:"symbol_guid"`
	Interval   string    `gorm:"column:interval;type:varchar(10);not null;default:'1m';index:idx_symbol_kline_business_key,unique" json:"interval"`
	OpenTime   time.Time `gorm:"column:open_time;not null;index:idx_symbol_kline_business_key,unique" json:"open_time"`
	OpenPrice  string    `gorm:"column:open_price;type:numeric(65,18);not null;default:0" json:"open_price"`
	ClosePrice string    `gorm:"column:close_price;type:numeric(65,18);not null;default:0" json:"close_price"`
	HighPrice  string    `gorm:"column:high_price;type:numeric(65,18);not null;default:0" json:"high_price"`
	LowPrice   string    `gorm:"column:low_price;type:numeric(65,18);not null;default:0" json:"low_price"`
	Volume     string    `gorm:"column:volume;type:uint256;not null;default:0" json:"volume"`
	MarketCap  string    `gorm:"column:market_cap;type:uint256;not null;default:0" json:"market_cap"`
	IsActive   bool      `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	IngestedAt time.Time `gorm:"column:ingested_at;not null;autoCreateTime" json:"ingested_at"`
	SyncSeq    int64     `gorm:"column:sync_seq;not null;->" json:"sync_seq"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SymbolKline) TableName() string {
	return "symbol_kline"
}

type SymbolKlineView interface {
	QueryLatestSymbolKline(symbolGuid, interval string) (*SymbolKline, error)
	QueryLatestMarketKline(marketID, interval string) (*SymbolKline, error)
	QuerySymbolKlines(symbolGuid, interval string, limit int) ([]*SymbolKline, error)
	QueryMarketKlines(marketID, interval string, limit int) ([]*SymbolKline, error)
	QueryMarketKlinesBetween(marketID, interval string, start, end time.Time) ([]SymbolKline, error)
	QueryMarketKlineAvailability(marketIDs []string) (map[string]bool, error)
	QueryMarketSparklines(marketIDs []string, interval string, limit int) (map[string][]KlinePoint, error)
}

type KlinePoint struct {
	MarketID   string
	OpenTime   time.Time
	ClosePrice string
}

type SymbolKlineDB interface {
	SymbolKlineView

	StoreSymbolKlines([]SymbolKline) error
	StoreSymbolKline(*SymbolKline) error
}

type symbolKlineDB struct {
	gorm *gorm.DB
}

func NewSymbolKlineDB(db *gorm.DB) SymbolKlineDB {
	return &symbolKlineDB{gorm: db}
}

// QueryLatestSymbolKline returns the newest kline row (by explicit open_time) for a
// symbol+interval pair. It returns (nil, nil) when no row exists.
func (s *symbolKlineDB) QueryLatestSymbolKline(symbolGuid, interval string) (*SymbolKline, error) {
	var kline SymbolKline
	err := s.gorm.Model(&SymbolKline{}).
		Where("symbol_guid = ?", symbolGuid).
		Where(`"interval" = ?`, interval).
		Order("open_time DESC").
		Limit(1).
		First(&kline).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		log.Error("Failed to query latest symbol_kline", "symbol_guid", symbolGuid, "interval", interval, "error", err)
		return nil, err
	}
	return &kline, nil
}

func (s *symbolKlineDB) QueryLatestMarketKline(marketID, interval string) (*SymbolKline, error) {
	var kline SymbolKline
	err := s.gorm.Model(&SymbolKline{}).
		Where("market_id = ? AND \"interval\" = ? AND is_active = ?", marketID, interval, true).
		Order("open_time DESC").
		Limit(1).
		First(&kline).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &kline, nil
}

// QuerySymbolKlines returns the newest `limit` kline rows (open_time DESC)
// for a symbol+interval pair.
func (s *symbolKlineDB) QuerySymbolKlines(symbolGuid, interval string, limit int) ([]*SymbolKline, error) {
	if limit <= 0 {
		limit = 50
	}

	var list []*SymbolKline
	if err := s.gorm.Model(&SymbolKline{}).
		Where("symbol_guid = ?", symbolGuid).
		Where(`"interval" = ?`, interval).
		Order("open_time DESC").
		Limit(limit).
		Find(&list).Error; err != nil {
		log.Error("Failed to query symbol_kline by symbol and interval", "symbol_guid", symbolGuid, "interval", interval, "error", err)
		return nil, err
	}

	return list, nil
}

func (s *symbolKlineDB) QueryMarketKlines(marketID, interval string, limit int) ([]*SymbolKline, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []*SymbolKline
	if err := s.gorm.Model(&SymbolKline{}).
		Where("market_id = ? AND \"interval\" = ? AND is_active = ?", marketID, interval, true).
		Order("open_time DESC").
		Limit(limit).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (s *symbolKlineDB) QueryMarketKlinesBetween(
	marketID, interval string,
	start, end time.Time,
) ([]SymbolKline, error) {
	var rows []SymbolKline
	err := s.gorm.Model(&SymbolKline{}).
		Where(`market_id = ? AND "interval" = ? AND open_time >= ? AND open_time < ? AND is_active = ?`,
			marketID, interval, start.UTC(), end.UTC(), true).
		Order("open_time ASC").
		Find(&rows).Error
	return rows, err
}

func (s *symbolKlineDB) QueryMarketKlineAvailability(marketIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	var ids []string
	if err := s.gorm.Model(&SymbolKline{}).
		Distinct("market_id").
		Where("market_id IN ? AND is_active = ?", marketIDs, true).
		Pluck("market_id", &ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		result[id] = true
	}
	return result, nil
}

func (s *symbolKlineDB) QueryMarketSparklines(marketIDs []string, interval string, limit int) (map[string][]KlinePoint, error) {
	result := make(map[string][]KlinePoint, len(marketIDs))
	if len(marketIDs) == 0 {
		return result, nil
	}
	if limit <= 0 {
		limit = 168
	}
	var points []KlinePoint
	query := `
SELECT market_id, open_time, close_price
FROM (
	SELECT market_id, open_time, close_price,
	       ROW_NUMBER() OVER (PARTITION BY market_id ORDER BY open_time DESC) AS row_num
	FROM symbol_kline
	WHERE market_id IN ? AND "interval" = ? AND is_active = TRUE
) ranked
WHERE row_num <= ?
ORDER BY market_id ASC, open_time ASC`
	if err := s.gorm.Raw(query, marketIDs, interval, limit).Scan(&points).Error; err != nil {
		return nil, err
	}
	for _, point := range points {
		result[point.MarketID] = append(result[point.MarketID], point)
	}
	return result, nil
}

func (s *symbolKlineDB) StoreSymbolKlines(list []SymbolKline) error {
	list = RetainedKlines(list, time.Now().UTC())
	if len(list) == 0 {
		return nil
	}
	if err := s.gorm.Table("symbol_kline").
		Clauses(klineUpsertClause()).
		Create(&list).Error; err != nil {
		log.Error("Failed to store symbol_kline list", "error", err)
		return err
	}
	return nil
}

func (s *symbolKlineDB) StoreSymbolKline(data *SymbolKline) error {
	if data == nil {
		return nil
	}
	if cutoff, bounded := KlineRetentionCutoff(data.Interval, time.Now().UTC()); bounded &&
		data.OpenTime.UTC().Before(cutoff) {
		return nil
	}
	if err := s.gorm.Table("symbol_kline").
		Clauses(klineUpsertClause()).
		Create(data).Error; err != nil {
		log.Error("Failed to store symbol_kline", "error", err)
		return err
	}
	return nil
}

// RetainedKlines is the write-side guard for late repair tasks and provider
// backfills. The daily retention worker reclaims existing rows, while this
// filter prevents bounded intervals from being reintroduced afterwards.
func RetainedKlines(list []SymbolKline, now time.Time) []SymbolKline {
	if len(list) == 0 {
		return nil
	}
	retained := make([]SymbolKline, 0, len(list))
	for _, item := range list {
		cutoff, bounded := KlineRetentionCutoff(item.Interval, now)
		if bounded && item.OpenTime.UTC().Before(cutoff) {
			continue
		}
		retained = append(retained, item)
	}
	return retained
}

func klineUpsertClause() clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{
			{Name: "market_id"}, {Name: "interval"}, {Name: "open_time"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"open_price", "high_price", "low_price", "close_price",
			"volume", "market_cap", "is_active", "updated_at",
		}),
		Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: `
			ROW(symbol_kline.market_id, symbol_kline."interval", symbol_kline.open_time,
				symbol_kline.open_price, symbol_kline.high_price, symbol_kline.low_price,
				symbol_kline.close_price, symbol_kline.volume, symbol_kline.market_cap,
				symbol_kline.is_active)
			IS DISTINCT FROM
			ROW(EXCLUDED.market_id, EXCLUDED."interval", EXCLUDED.open_time,
				EXCLUDED.open_price, EXCLUDED.high_price, EXCLUDED.low_price,
				EXCLUDED.close_price, EXCLUDED.volume, EXCLUDED.market_cap,
				EXCLUDED.is_active)`}}},
	}
}

// KlineMateriallyChanged mirrors the database trigger's exact materialized
// field set and is kept pure for deterministic unit tests and caller decisions.
func KlineMateriallyChanged(oldRow, newRow SymbolKline) bool {
	return oldRow.MarketID != newRow.MarketID ||
		oldRow.Interval != newRow.Interval ||
		!oldRow.OpenTime.Equal(newRow.OpenTime) ||
		oldRow.OpenPrice != newRow.OpenPrice ||
		oldRow.HighPrice != newRow.HighPrice ||
		oldRow.LowPrice != newRow.LowPrice ||
		oldRow.ClosePrice != newRow.ClosePrice ||
		oldRow.Volume != newRow.Volume ||
		oldRow.MarketCap != newRow.MarketCap ||
		oldRow.IsActive != newRow.IsActive
}
