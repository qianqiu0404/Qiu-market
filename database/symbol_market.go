package database

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SymbolMarket struct {
	Guid             string  `gorm:"primaryKey;column:guid;type:text" json:"guid"`
	MarketID         string  `gorm:"column:market_id;type:text;not null;uniqueIndex" json:"market_id"`
	SymbolGuid       string  `gorm:"column:symbol_guid;type:varchar(100);not null;default:''" json:"symbol_guid"`
	Price            string  `gorm:"column:price;type:numeric(65,18);not null;default:0" json:"price"`
	AskPrice         string  `gorm:"column:ask_price;type:numeric(65,18);not null;default:0" json:"ask_price"`
	BidPrice         string  `gorm:"column:bid_price;type:numeric(65,18);not null;default:0" json:"bid_price"`
	Volume           string  `gorm:"column:volume;type:uint256;not null;default:0" json:"volume"`
	MarketCap        string  `gorm:"column:market_cap;type:uint256;not null;default:0" json:"market_cap"`
	Radio            string  `gorm:"column:radio;type:numeric(65,18);not null;default:0" json:"radio"`
	Open24h          *string `gorm:"column:open_24h;type:numeric(65,18)" json:"open_24h,omitempty"`
	QuoteTurnover24h *string `gorm:"column:quote_turnover_24h;type:numeric(65,18)" json:"quote_turnover_24h,omitempty"`
	QuoteTurnoverUSD *string `gorm:"column:quote_turnover_usd;type:numeric(65,18)" json:"quote_turnover_usd,omitempty"`
	// Change24hPct is the canonical percentage. It remains nullable so a
	// missing upstream value can never be mistaken for a real 0% move.
	Change24hPct   *string    `gorm:"column:change_24h_pct;type:numeric(65,18)" json:"change_24h_pct,omitempty"`
	ObservedAt     *time.Time `gorm:"column:observed_at" json:"observed_at,omitempty"`
	SourceTime     *time.Time `gorm:"column:source_time" json:"source_time,omitempty"`
	SourceTimeKind *string    `gorm:"column:source_time_kind;type:text" json:"source_time_kind,omitempty"`
	IsActive       bool       `gorm:"column:is_active;type:boolean;not null;default:true" json:"is_active"`
	CreatedAt      time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (SymbolMarket) TableName() string {
	return "symbol_market"
}

type SymbolMarketView interface {
	QuerySymbolMarketList(query SymbolMarketListQuery) ([]*SymbolMarket, int64, error)
	// QueryMarketReadRows returns the denormalized, active market read model used
	// by asset aggregation and real-time insights. Search is applied before the
	// service layer groups rows by base_asset_id.
	QueryMarketReadRows(search string) ([]MarketReadRow, error)
	QuerySymbolMarketByMarketID(marketID string) (*SymbolMarket, error)
	QuerySymbolMarketTodayFirstData() (*SymbolMarket, error)
	QuerySymbolMarketTodayFirstDataBySymbol(symbolGuid string) (*SymbolMarket, error)
	// QuerySymbolMarketsByGuids 按 symbol_guid 批量查行情行（榜单接口按 ZSET 读出的 guid 回表）。
	QuerySymbolMarketsByGuids(symbolGuids []string) ([]*SymbolMarket, error)
	// QuerySymbolMarketsByChange 按 24h 涨跌幅 radio 排序取前 limit 条（ZSET 为空时的 SQL 回退）。
	// direction 只允许 "desc"（涨幅榜）或 "asc"（跌幅榜）。
	QuerySymbolMarketsByChange(direction string, limit int64) ([]*SymbolMarket, error)
}

// MarketReadRow is a read-only projection. It deliberately preserves stable
// database identities alongside human-readable asset codes so callers never
// infer identity by splitting symbols such as BTC/USDT.
type MarketReadRow struct {
	MarketID         string     `gorm:"column:market_id"`
	MarketCode       string     `gorm:"column:market_code"`
	SymbolGuid       string     `gorm:"column:symbol_guid"`
	SymbolName       string     `gorm:"column:symbol_name"`
	BaseAssetID      string     `gorm:"column:base_asset_id"`
	BaseAsset        string     `gorm:"column:base_asset"`
	BaseAssetName    string     `gorm:"column:base_asset_name"`
	BaseAssetLogo    string     `gorm:"column:base_asset_logo"`
	QuoteAssetID     string     `gorm:"column:quote_asset_id"`
	QuoteAsset       string     `gorm:"column:quote_asset"`
	Exchange         string     `gorm:"column:exchange"`
	MarketType       string     `gorm:"column:market_type"`
	Price            string     `gorm:"column:price"`
	Volume           string     `gorm:"column:volume"`
	MarketCap        string     `gorm:"column:market_cap"`
	Open24h          *string    `gorm:"column:open_24h"`
	QuoteTurnover24h *string    `gorm:"column:quote_turnover_24h"`
	QuoteTurnoverUSD *string    `gorm:"column:quote_turnover_usd"`
	Change24hPct     *string    `gorm:"column:change_24h_pct"`
	ObservedAt       *time.Time `gorm:"column:observed_at"`
	SourceTime       *time.Time `gorm:"column:source_time"`
	SourceTimeKind   *string    `gorm:"column:source_time_kind"`
	UpdatedAt        time.Time  `gorm:"column:updated_at"`
	MarketIsActive   bool       `gorm:"column:market_is_active"`
	SymbolIsActive   bool       `gorm:"column:symbol_is_active"`
}

// SymbolMarketListQuery is the server-side contract for the paginated Markets
// table. Sorting is whitelisted in QuerySymbolMarketList; callers never pass
// raw SQL fragments.
type SymbolMarketListQuery struct {
	Page          int64
	PageSize      int64
	Exchange      string
	Search        string
	MarketID      string
	SortBy        string
	SortDirection string
	RankOrder     []string
}

type SymbolMarketDB interface {
	SymbolMarketView

	StoreSymbolMarkets([]SymbolMarket) error
	StoreSymbolMarket(*SymbolMarket) error
	UpdateSymbolMarketTicker(symbolGuid, price, volume string) error
	UpdateSymbolMarketTickerWithChange(symbolGuid, price, volume, change string) error
	UpsertSymbolMarketTicker(data *SymbolMarket) error
	UpdateSymbolMarketFull(symbolGuid, marketCap string) error
	ApplyMarketSnapshot(input MarketSnapshotInput) (MarketSnapshotResult, error)
}

type MarketSnapshotInput struct {
	Guid             string
	MarketID         string
	SymbolGuid       string
	Price            string
	AskPrice         string
	BidPrice         string
	Volume           string
	Open24h          *string
	QuoteTurnover24h *string
	QuoteTurnoverUSD *string
	Change24hPct     *string
	IsActive         bool
	ObservedAt       time.Time
	SourceTime       *time.Time
	SourceTimeKind   *string
}

type MarketSnapshotAction string

const (
	MarketSnapshotInserted   MarketSnapshotAction = "inserted"
	MarketSnapshotUpdated    MarketSnapshotAction = "updated"
	MarketSnapshotObserved   MarketSnapshotAction = "observed"
	MarketSnapshotNoop       MarketSnapshotAction = "noop"
	MarketSnapshotDiscarded  MarketSnapshotAction = "discarded"
	MarketSnapshotCorrection MarketSnapshotAction = "correction"
)

type MarketSnapshotResult struct {
	Action MarketSnapshotAction
}

type symbolMarketDB struct {
	gorm *gorm.DB
}

func NewSymbolMarketDB(db *gorm.DB) SymbolMarketDB {
	return &symbolMarketDB{gorm: db}
}

func (s *symbolMarketDB) QuerySymbolMarketTodayFirstData() (*SymbolMarket, error) {
	var symbolMarket SymbolMarket
	now := time.Now().UTC()
	utcStartOfDay := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		time.UTC,
	)
	result := s.gorm.Table("symbol_market").
		Where("created_at >= ?", utcStartOfDay).
		Order("created_at ASC").
		Limit(1).
		Find(&symbolMarket)
	if result.Error != nil {
		log.Error("QuerySymbolMarketTodayFirstData failed", "error", result.Error)
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &symbolMarket, nil
}

func (s *symbolMarketDB) QuerySymbolMarketTodayFirstDataBySymbol(symbolGuid string) (*SymbolMarket, error) {
	var symbolMarket SymbolMarket
	now := time.Now().UTC()
	utcStartOfDay := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		time.UTC,
	)
	result := s.gorm.Table("symbol_market").
		Where("symbol_guid = ? AND created_at >= ?", symbolGuid, utcStartOfDay).
		Order("created_at ASC").
		Limit(1).
		Find(&symbolMarket)
	if result.Error != nil {
		log.Error("QuerySymbolMarketTodayFirstDataBySymbol failed",
			"symbol_guid", symbolGuid, "error", result.Error)
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &symbolMarket, nil
}

// QuerySymbolMarketList 分页查询行情表。exchange 非空时按交易所名过滤：
// symbol_market 本身不带交易所字段，经由 exchange_symbol（symbol ↔ exchange 的
// 关联表）JOIN exchange 过滤，因此 total 与分页在 SQL 层保持正确。
func (s *symbolMarketDB) QuerySymbolMarketList(input SymbolMarketListQuery) ([]*SymbolMarket, int64, error) {
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PageSize <= 0 {
		input.PageSize = 10
	}

	offset := (input.Page - 1) * input.PageSize

	var list []*SymbolMarket
	query := s.gorm.Model(&SymbolMarket{}).
		Joins("JOIN exchange_symbol ON exchange_symbol.guid = symbol_market.market_id AND exchange_symbol.is_active = ?", true).
		Joins("JOIN exchange ON exchange.guid = exchange_symbol.exchange_guid").
		Joins("JOIN symbol ON symbol.guid = exchange_symbol.symbol_guid").
		Joins("JOIN asset base_asset ON base_asset.guid = symbol.base_asset_guid")
	if input.Exchange != "" {
		query = query.
			Where("exchange.name = ?", input.Exchange)
	}
	if input.MarketID != "" {
		query = query.Where("symbol_market.market_id = ?", input.MarketID)
	}
	if search := strings.TrimSpace(input.Search); search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where(
			"LOWER(symbol.symbol_name) LIKE ? OR LOWER(base_asset.asset_name) LIKE ? OR LOWER(base_asset.asset_symbol) LIKE ? OR LOWER(exchange_symbol.market_code) LIKE ?",
			like, like, like, like,
		)
	}
	if strings.EqualFold(strings.TrimSpace(input.SortBy), "change24h") && len(input.RankOrder) > 0 {
		placeholders := make([]string, 0, len(input.RankOrder))
		args := make([]interface{}, 0, len(input.RankOrder)*2)
		for index, guid := range input.RankOrder {
			// Explicit casts keep pgx from inferring the whole VALUES row as
			// text when a large Redis rank list is bound through placeholders.
			placeholders = append(placeholders, "(CAST(? AS text), CAST(? AS integer))")
			args = append(args, guid, index)
		}
		query = query.Joins(
			"LEFT JOIN (VALUES "+strings.Join(placeholders, ",")+") AS change_rank(symbol_guid, rank) ON change_rank.symbol_guid = symbol_market.symbol_guid",
			args...,
		)
	}

	var total int64
	// Every join above follows a primary-key/unique-key edge from market_id, so
	// one market produces exactly one row. Avoid DISTINCT here: GORM carries it
	// into the later SELECT, which makes PostgreSQL reject ORDER BY expressions
	// that are intentionally not part of the public result shape.
	if err := query.Count(&total).Error; err != nil {
		log.Error("Failed to query symbol_market count", "error", err)
		return nil, 0, err
	}

	if err := query.
		Select("symbol_market.*").
		Order(symbolMarketOrder(input.SortBy, input.SortDirection, input.RankOrder)).
		Limit(int(input.PageSize)).
		Offset(int(offset)).
		Find(&list).Error; err != nil {
		log.Error("Failed to query symbol_market list", "error", err)
		return nil, 0, err
	}

	return list, total, nil
}

func (s *symbolMarketDB) QueryMarketReadRows(search string) ([]MarketReadRow, error) {
	var rows []MarketReadRow
	query := s.gorm.Table("symbol_market").
		Select(`symbol_market.market_id,
			exchange_symbol.market_code,
			symbol_market.symbol_guid,
			symbol.symbol_name,
			symbol.base_asset_guid AS base_asset_id,
			base_asset.asset_symbol AS base_asset,
			base_asset.asset_name AS base_asset_name,
			base_asset.asset_logo AS base_asset_logo,
			symbol.qoute_asset_guid AS quote_asset_id,
			quote_asset.asset_symbol AS quote_asset,
			exchange.name AS exchange,
			symbol.market_type,
			symbol_market.price,
			symbol_market.volume,
			symbol_market.market_cap,
			symbol_market.open_24h,
			symbol_market.quote_turnover_24h,
			symbol_market.quote_turnover_usd,
			symbol_market.change_24h_pct,
			symbol_market.observed_at,
			symbol_market.source_time,
			symbol_market.source_time_kind,
			symbol_market.updated_at,
			exchange_symbol.is_active AS market_is_active,
			symbol.is_active AS symbol_is_active`).
		Joins("JOIN exchange_symbol ON exchange_symbol.guid = symbol_market.market_id").
		Joins("JOIN exchange ON exchange.guid = exchange_symbol.exchange_guid").
		Joins("JOIN symbol ON symbol.guid = exchange_symbol.symbol_guid").
		Joins("JOIN asset base_asset ON base_asset.guid = symbol.base_asset_guid").
		Joins("JOIN asset quote_asset ON quote_asset.guid = symbol.qoute_asset_guid").
		Where("symbol_market.is_active = ? AND exchange_symbol.is_active = ? AND symbol.is_active = ?",
			true, true, true)

	if search = strings.TrimSpace(search); search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where(
			`LOWER(base_asset.asset_symbol) LIKE ?
			 OR LOWER(base_asset.asset_name) LIKE ?
			 OR LOWER(symbol.base_asset_guid) LIKE ?
			 OR LOWER(symbol.symbol_name) LIKE ?
			 OR LOWER(exchange_symbol.market_code) LIKE ?
			 OR LOWER(exchange.name) LIKE ?`,
			like, like, like, like, like, like,
		)
	}
	if err := query.Order("symbol_market.market_id ASC").Scan(&rows).Error; err != nil {
		log.Error("Failed to query market read rows", "error", err)
		return nil, err
	}
	return rows, nil
}

func symbolMarketOrder(sortBy, direction string, rankOrder []string) string {
	dir := "DESC"
	if strings.EqualFold(direction, "asc") {
		dir = "ASC"
	}
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "price":
		return "symbol_market.price " + dir + ", symbol_market.market_id ASC"
	case "volume":
		return "symbol_market.volume " + dir + ", symbol_market.market_id ASC"
	case "change24h":
		if len(rankOrder) > 0 {
			return "change_rank.rank ASC NULLS LAST, symbol_market.market_id ASC"
		}
		return "symbol_market.change_24h_pct " + dir + " NULLS LAST, symbol_market.market_id ASC"
	case "symbol":
		return "symbol.symbol_name " + dir + ", symbol_market.market_id ASC"
	case "market_cap":
		return "symbol_market.market_cap " + dir + ", symbol_market.volume DESC, symbol_market.market_id ASC"
	default:
		return "CASE WHEN symbol_market.market_cap > 0 THEN 0 ELSE 1 END ASC, symbol_market.market_cap DESC, symbol_market.volume DESC, symbol_market.market_id ASC"
	}
}

func (s *symbolMarketDB) QuerySymbolMarketByMarketID(marketID string) (*SymbolMarket, error) {
	var market SymbolMarket
	if err := s.gorm.Table("symbol_market").
		Where("market_id = ?", marketID).
		First(&market).Error; err != nil {
		return nil, err
	}
	return &market, nil
}

func (s *symbolMarketDB) QuerySymbolMarketsByGuids(symbolGuids []string) ([]*SymbolMarket, error) {
	if len(symbolGuids) == 0 {
		return nil, nil
	}
	var list []*SymbolMarket
	if err := s.gorm.Table("symbol_market").
		Where("symbol_guid IN ?", symbolGuids).
		Find(&list).Error; err != nil {
		log.Error("Failed to query symbol_market by guids", "error", err)
		return nil, err
	}
	return list, nil
}

func (s *symbolMarketDB) QuerySymbolMarketsByChange(direction string, limit int64) ([]*SymbolMarket, error) {
	if limit <= 0 {
		limit = 5
	}
	// direction 白名单，避免拼接排序方向造成注入
	order := "change_24h_pct DESC NULLS LAST"
	if direction == "asc" {
		order = "change_24h_pct ASC NULLS LAST"
	}
	var list []*SymbolMarket
	if err := s.gorm.Table("symbol_market").
		Where("change_24h_pct IS NOT NULL").
		Order(order).
		Limit(int(limit)).
		Find(&list).Error; err != nil {
		log.Error("Failed to query symbol_market by change", "direction", direction, "error", err)
		return nil, err
	}
	return list, nil
}

func (s *symbolMarketDB) StoreSymbolMarkets(list []SymbolMarket) error {
	if err := s.gorm.Table("symbol_market").
		CreateInBatches(&list, len(list)).Error; err != nil {
		log.Error("Failed to store symbol_market list", "error", err)
		return err
	}
	return nil
}

func (s *symbolMarketDB) StoreSymbolMarket(data *SymbolMarket) error {
	if err := s.gorm.Table("symbol_market").
		Create(&data).Error; err != nil {
		log.Error("Failed to store symbol_market", "error", err)
		return err
	}
	return nil
}

func (s *symbolMarketDB) UpdateSymbolMarketTicker(symbolGuid, price, volume string) error {
	if err := s.gorm.Table("symbol_market").
		Where("symbol_guid = ?", symbolGuid).
		Updates(map[string]interface{}{
			"price":      price,
			"volume":     volume,
			"updated_at": time.Now(),
		}).Error; err != nil {
		log.Error("Failed to update symbol_market ticker", "symbol_guid", symbolGuid, "error", err)
		return err
	}
	return nil
}

func (s *symbolMarketDB) UpdateSymbolMarketTickerWithChange(symbolGuid, price, volume, change string) error {
	if err := s.gorm.Table("symbol_market").
		Where("symbol_guid = ?", symbolGuid).
		Updates(map[string]interface{}{
			"price":      price,
			"volume":     volume,
			"radio":      change,
			"updated_at": time.Now(),
		}).Error; err != nil {
		log.Error("Failed to update symbol_market ticker with change", "symbol_guid", symbolGuid, "error", err)
		return err
	}
	return nil
}

func (s *symbolMarketDB) UpsertSymbolMarketTicker(data *SymbolMarket) error {
	now := time.Now()
	updates := map[string]interface{}{
		"price":      data.Price,
		"ask_price":  data.AskPrice,
		"bid_price":  data.BidPrice,
		"volume":     data.Volume,
		"radio":      data.Radio,
		"is_active":  data.IsActive,
		"updated_at": now,
	}

	tx := s.gorm.Table("symbol_market").
		Where("symbol_guid = ?", data.SymbolGuid).
		Updates(updates)
	if tx.Error != nil {
		log.Error("Failed to update symbol_market ticker", "symbol_guid", data.SymbolGuid, "error", tx.Error)
		return tx.Error
	}
	if tx.RowsAffected > 0 {
		return nil
	}

	if data.MarketCap == "" {
		data.MarketCap = "0"
	}
	if data.MarketID == "" {
		var marketIDs []string
		if err := s.gorm.Table("exchange_symbol").
			Where("symbol_guid = ? AND is_active = ?", data.SymbolGuid, true).
			Limit(2).
			Pluck("guid", &marketIDs).Error; err != nil {
			return err
		}
		if len(marketIDs) != 1 {
			return fmt.Errorf("ambiguous_market: symbol_guid %q resolves to %d active markets", data.SymbolGuid, len(marketIDs))
		}
		data.MarketID = marketIDs[0]
	}
	if data.CreatedAt.IsZero() {
		data.CreatedAt = now
	}
	data.UpdatedAt = now
	if err := s.gorm.Table("symbol_market").Create(data).Error; err != nil {
		log.Error("Failed to create symbol_market ticker", "symbol_guid", data.SymbolGuid, "error", err)
		return err
	}
	return nil
}

func (s *symbolMarketDB) UpdateSymbolMarketFull(symbolGuid, marketCap string) error {
	if err := s.gorm.Table("symbol_market").
		Where("symbol_guid = ?", symbolGuid).
		Updates(map[string]interface{}{
			"market_cap": marketCap,
			"updated_at": time.Now(),
		}).Error; err != nil {
		log.Error("Failed to update symbol_market full", "symbol_guid", symbolGuid, "error", err)
		return err
	}
	return nil
}

// ApplyMarketSnapshot is the database boundary of the single snapshot writer.
// The row lock serializes adapters for one market, while the deterministic
// decision rules keep late source events from overwriting a trusted snapshot.
func (s *symbolMarketDB) ApplyMarketSnapshot(input MarketSnapshotInput) (MarketSnapshotResult, error) {
	if strings.TrimSpace(input.MarketID) == "" {
		return MarketSnapshotResult{}, fmt.Errorf("market snapshot market_id is required")
	}
	if strings.TrimSpace(input.SymbolGuid) == "" {
		return MarketSnapshotResult{}, fmt.Errorf("market snapshot symbol_guid is required")
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = time.Now()
	}

	var current SymbolMarket
	query := s.gorm.Table("symbol_market").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("market_id = ?", input.MarketID).
		Limit(1).
		Find(&current)
	if query.Error != nil {
		return MarketSnapshotResult{}, query.Error
	}
	if query.RowsAffected == 0 {
		guid := strings.TrimSpace(input.Guid)
		if guid == "" {
			guid = input.MarketID
		}
		radio := "0"
		if input.Change24hPct != nil {
			radio = *input.Change24hPct
		}
		row := &SymbolMarket{
			Guid:             guid,
			MarketID:         input.MarketID,
			SymbolGuid:       input.SymbolGuid,
			Price:            input.Price,
			AskPrice:         input.AskPrice,
			BidPrice:         input.BidPrice,
			Volume:           input.Volume,
			MarketCap:        "0",
			Radio:            radio,
			Open24h:          cloneString(input.Open24h),
			QuoteTurnover24h: cloneString(input.QuoteTurnover24h),
			QuoteTurnoverUSD: cloneString(input.QuoteTurnoverUSD),
			Change24hPct:     cloneString(input.Change24hPct),
			ObservedAt:       cloneTime(&input.ObservedAt),
			SourceTime:       cloneTime(input.SourceTime),
			SourceTimeKind:   cloneString(input.SourceTimeKind),
			IsActive:         input.IsActive,
			CreatedAt:        input.ObservedAt,
			UpdatedAt:        input.ObservedAt,
		}
		if err := s.gorm.Table("symbol_market").Create(row).Error; err != nil {
			return MarketSnapshotResult{}, err
		}
		return MarketSnapshotResult{Action: MarketSnapshotInserted}, nil
	}

	action := decideMarketSnapshot(current, input)
	if action == MarketSnapshotDiscarded || action == MarketSnapshotNoop {
		return MarketSnapshotResult{Action: action}, nil
	}
	if action == MarketSnapshotObserved {
		updates := map[string]interface{}{
			"observed_at":      input.ObservedAt,
			"source_time":      input.SourceTime,
			"source_time_kind": input.SourceTimeKind,
		}
		if err := s.gorm.Table("symbol_market").
			Where("market_id = ?", input.MarketID).
			UpdateColumns(updates).Error; err != nil {
			return MarketSnapshotResult{}, err
		}
		return MarketSnapshotResult{Action: action}, nil
	}

	updates := map[string]interface{}{
		"price":              input.Price,
		"ask_price":          input.AskPrice,
		"bid_price":          input.BidPrice,
		"volume":             input.Volume,
		"open_24h":           input.Open24h,
		"quote_turnover_24h": input.QuoteTurnover24h,
		"quote_turnover_usd": input.QuoteTurnoverUSD,
		"change_24h_pct":     input.Change24hPct,
		"radio":              legacyChangeValue(input.Change24hPct),
		"is_active":          input.IsActive,
		"observed_at":        input.ObservedAt,
		"source_time":        input.SourceTime,
		"source_time_kind":   input.SourceTimeKind,
		"updated_at":         input.ObservedAt,
	}
	if err := s.gorm.Table("symbol_market").
		Where("market_id = ?", input.MarketID).
		UpdateColumns(updates).Error; err != nil {
		return MarketSnapshotResult{}, err
	}
	return MarketSnapshotResult{Action: action}, nil
}

func decideMarketSnapshot(current SymbolMarket, incoming MarketSnapshotInput) MarketSnapshotAction {
	if current.SourceTime != nil && incoming.SourceTime != nil {
		switch {
		case incoming.SourceTime.Before(*current.SourceTime):
			return MarketSnapshotDiscarded
		case incoming.SourceTime.Equal(*current.SourceTime):
			if equalSnapshotContent(current, incoming) {
				return MarketSnapshotNoop
			}
			if current.ObservedAt != nil && !incoming.ObservedAt.After(*current.ObservedAt) {
				return MarketSnapshotDiscarded
			}
			return MarketSnapshotCorrection
		}
	}
	if equalSnapshotContent(current, incoming) {
		if current.ObservedAt != nil && !incoming.ObservedAt.After(*current.ObservedAt) {
			return MarketSnapshotNoop
		}
		return MarketSnapshotObserved
	}
	return MarketSnapshotUpdated
}

func equalSnapshotContent(current SymbolMarket, incoming MarketSnapshotInput) bool {
	return equalNumericString(current.Price, incoming.Price) &&
		equalNumericString(current.AskPrice, incoming.AskPrice) &&
		equalNumericString(current.BidPrice, incoming.BidPrice) &&
		equalNumericString(current.Volume, incoming.Volume) &&
		equalOptionalNumericString(current.Open24h, incoming.Open24h) &&
		equalOptionalNumericString(current.QuoteTurnover24h, incoming.QuoteTurnover24h) &&
		equalOptionalNumericString(current.QuoteTurnoverUSD, incoming.QuoteTurnoverUSD) &&
		equalOptionalNumericString(current.Change24hPct, incoming.Change24hPct) &&
		current.IsActive == incoming.IsActive
}

func equalNumericString(left, right string) bool {
	leftRat, leftOK := new(big.Rat).SetString(strings.TrimSpace(left))
	rightRat, rightOK := new(big.Rat).SetString(strings.TrimSpace(right))
	if leftOK && rightOK {
		return leftRat.Cmp(rightRat) == 0
	}
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

func equalOptionalNumericString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return equalNumericString(*left, *right)
}

func legacyChangeValue(value *string) string {
	if value == nil {
		return "0"
	}
	return *value
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
