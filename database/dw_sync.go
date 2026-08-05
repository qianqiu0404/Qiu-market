package database

import (
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/gorm"
)

// DwSyncState 是 dw 进程（PostgreSQL -> Doris）每条同步流的水位记录。
// stream_name 约定：K 线为 "kline:<symbol_guid>:<interval>"，行情快照为
// "market_snapshot"。last_synced_at 是已同步水位（K 线 = open_time，
// 快照 = captured_at），rows_loaded 累计已推入行数，便于排障。
type DwSyncState struct {
	StreamName   string    `gorm:"primaryKey;column:stream_name;type:text" json:"stream_name"`
	LastSyncedAt time.Time `gorm:"column:last_synced_at" json:"last_synced_at"`
	LastSyncSeq  int64     `gorm:"column:last_sync_seq;not null;default:0" json:"last_sync_seq"`
	RowsLoaded   int64     `gorm:"column:rows_loaded;not null;default:0" json:"rows_loaded"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (DwSyncState) TableName() string {
	return "dw_sync_state"
}

// KlineStream 是 symbol_kline 中实际存在的一组 (symbol_guid, interval)。
type KlineStream struct {
	SymbolGuid string `gorm:"column:symbol_guid" json:"symbol_guid"`
	Interval   string `gorm:"column:interval" json:"interval"`
}

// KlineV2Stream is the shadow pipeline identity. market_id owns the business
// key; market_code is carried into Doris for operator-visible audit trails.
type KlineV2Stream struct {
	MarketID   string `gorm:"column:market_id" json:"market_id"`
	MarketCode string `gorm:"column:market_code" json:"market_code"`
	SymbolGuid string `gorm:"column:symbol_guid" json:"symbol_guid"`
	Interval   string `gorm:"column:interval" json:"interval"`
}

// MarketSnapshotRow 是一次行情快照同步的输入行：symbol_market 当前行
// 加上经 exchange_symbol + exchange 解析出的交易所名（取第一个关联交易所）。
type MarketSnapshotRow struct {
	MarketID     string     `gorm:"column:market_id" json:"market_id"`
	MarketCode   string     `gorm:"column:market_code" json:"market_code"`
	SymbolGuid   string     `gorm:"column:symbol_guid" json:"symbol_guid"`
	Exchange     string     `gorm:"column:exchange" json:"exchange"`
	Price        string     `gorm:"column:price" json:"price"`
	Volume       string     `gorm:"column:volume" json:"volume"`
	MarketCap    string     `gorm:"column:market_cap" json:"market_cap"`
	Change24hPct *string    `gorm:"column:change_24h_pct" json:"change_24h_pct"`
	ObservedAt   *time.Time `gorm:"column:observed_at" json:"observed_at"`
}

type DwSyncView interface {
	// GetSyncState 读取某条同步流的水位；不存在时返回 (nil, nil)。
	GetSyncState(streamName string) (*DwSyncState, error)
	// QueryDistinctKlineStreams 返回 symbol_kline 中存在的全部 (symbol, interval) 组合。
	QueryDistinctKlineStreams() ([]*KlineStream, error)
	// QuerySymbolKlinesAfter 按 created_at ASC 取水位之后的 K 线，最多 limit 条。
	QuerySymbolKlinesAfter(symbolGuid, interval string, after time.Time, limit int) ([]*SymbolKline, error)
	// QueryMarketSnapshotRows 取 symbol_market 当前全量行并解析交易所名。
	QueryMarketSnapshotRows() ([]*MarketSnapshotRow, error)
	QueryDistinctKlineV2Streams() ([]*KlineV2Stream, error)
	QueryKlineSyncCeiling() (int64, error)
	QuerySymbolKlinesBySyncSeq(marketID, interval string, afterSeq, ceiling int64, limit int) ([]*SymbolKline, error)
	QueryAllSymbolKlinesV2(marketID, interval string) ([]*SymbolKline, error)
	QuerySymbolKlinesV2ThroughSeq(marketID, interval string, maxSyncSeq int64) ([]*SymbolKline, error)
}

type DwSyncDB interface {
	DwSyncView

	// UpsertSyncState 推进水位并累加行数（Save 即 upsert，单写者安全）。
	UpsertSyncState(streamName string, watermark time.Time, rowsLoaded int64) error
	UpsertSyncStateSeq(streamName string, lastSyncSeq, rowsLoaded int64) error
}

type dwSyncDB struct {
	gorm *gorm.DB
}

func NewDwSyncDB(db *gorm.DB) DwSyncDB {
	return &dwSyncDB{gorm: db}
}

func (d *dwSyncDB) GetSyncState(streamName string) (*DwSyncState, error) {
	var state DwSyncState
	result := d.gorm.Model(&DwSyncState{}).
		Where("stream_name = ?", streamName).
		Limit(1).
		Find(&state)
	if result.Error != nil {
		err := result.Error
		log.Error("Failed to query dw_sync_state", "stream_name", streamName, "error", err)
		return nil, err
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &state, nil
}

func (d *dwSyncDB) QueryDistinctKlineStreams() ([]*KlineStream, error) {
	var streams []*KlineStream
	if err := d.gorm.Model(&SymbolKline{}).
		Select("symbol_guid", `"interval"`).
		Group("symbol_guid").
		Group(`"interval"`).
		Scan(&streams).Error; err != nil {
		log.Error("Failed to query distinct kline streams", "error", err)
		return nil, err
	}
	return streams, nil
}

func (d *dwSyncDB) QueryDistinctKlineV2Streams() ([]*KlineV2Stream, error) {
	var streams []*KlineV2Stream
	if err := d.gorm.Table("symbol_kline AS sk").
		Select("sk.market_id, es.market_code, sk.symbol_guid, sk.\"interval\"").
		Joins("JOIN exchange_symbol es ON es.guid = sk.market_id").
		Group("sk.market_id, es.market_code, sk.symbol_guid, sk.\"interval\"").
		Scan(&streams).Error; err != nil {
		log.Error("Failed to query distinct kline v2 streams", "error", err)
		return nil, err
	}
	return streams, nil
}

func (d *dwSyncDB) QueryKlineSyncCeiling() (int64, error) {
	var ceiling int64
	if err := d.gorm.Raw("SELECT last_value FROM symbol_kline_sync_seq").Scan(&ceiling).Error; err != nil {
		return 0, err
	}
	return ceiling, nil
}

func (d *dwSyncDB) QuerySymbolKlinesBySyncSeq(marketID, interval string, afterSeq, ceiling int64, limit int) ([]*SymbolKline, error) {
	if limit <= 0 {
		limit = 500
	}
	var list []*SymbolKline
	if err := d.gorm.Model(&SymbolKline{}).
		Where("market_id = ?", marketID).
		Where(`"interval" = ?`, interval).
		Where("sync_seq > ? AND sync_seq <= ?", afterSeq, ceiling).
		Order("sync_seq ASC").
		Limit(limit).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (d *dwSyncDB) QueryAllSymbolKlinesV2(marketID, interval string) ([]*SymbolKline, error) {
	var list []*SymbolKline
	if err := d.gorm.Model(&SymbolKline{}).
		Where("market_id = ?", marketID).
		Where(`"interval" = ?`, interval).
		Order("open_time ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// QuerySymbolKlinesV2ThroughSeq returns only rows the persisted v2 watermark
// says Doris has successfully accepted. Newer committed rows are deliberately
// excluded from reconciliation so an in-flight current-candle correction is
// not reported as a content conflict.
func (d *dwSyncDB) QuerySymbolKlinesV2ThroughSeq(marketID, interval string, maxSyncSeq int64) ([]*SymbolKline, error) {
	if maxSyncSeq <= 0 {
		return nil, nil
	}
	var list []*SymbolKline
	if err := d.gorm.Model(&SymbolKline{}).
		Where("market_id = ?", marketID).
		Where(`"interval" = ?`, interval).
		Where("sync_seq <= ?", maxSyncSeq).
		Order("open_time ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (d *dwSyncDB) QuerySymbolKlinesAfter(symbolGuid, interval string, after time.Time, limit int) ([]*SymbolKline, error) {
	if limit <= 0 {
		limit = 500
	}
	var list []*SymbolKline
	if err := d.gorm.Model(&SymbolKline{}).
		Where("symbol_guid = ?", symbolGuid).
		Where(`"interval" = ?`, interval).
		Where("created_at > ?", after).
		Order("created_at ASC").
		Limit(limit).
		Find(&list).Error; err != nil {
		log.Error("Failed to query symbol_kline after watermark", "symbol_guid", symbolGuid, "interval", interval, "error", err)
		return nil, err
	}
	return list, nil
}

// QueryMarketSnapshotRows 的子查询为每个 symbol 取第一个活跃交易所名；
// symbol_market 本身不带交易所字段，与 QuerySymbolMarketList 的过滤逻辑同源。
func (d *dwSyncDB) QueryMarketSnapshotRows() ([]*MarketSnapshotRow, error) {
	var rows []*MarketSnapshotRow
	if err := d.gorm.Table("symbol_market AS sm").
		Select(`sm.market_id, es.market_code, sm.symbol_guid,
			sm.price, sm.volume, sm.market_cap, sm.change_24h_pct, sm.observed_at,
			COALESCE(e.name, '') AS exchange`).
		Joins("JOIN exchange_symbol es ON es.guid = sm.market_id").
		Joins("JOIN exchange e ON e.guid = es.exchange_guid").
		Scan(&rows).Error; err != nil {
		log.Error("Failed to query market snapshot rows", "error", err)
		return nil, err
	}
	return rows, nil
}

func (d *dwSyncDB) UpsertSyncState(streamName string, watermark time.Time, rowsLoaded int64) error {
	state := &DwSyncState{
		StreamName:   streamName,
		LastSyncedAt: watermark,
		RowsLoaded:   rowsLoaded,
		UpdatedAt:    time.Now(),
	}
	if err := d.gorm.Table("dw_sync_state").Save(state).Error; err != nil {
		log.Error("Failed to upsert dw_sync_state", "stream_name", streamName, "error", err)
		return err
	}
	return nil
}

func (d *dwSyncDB) UpsertSyncStateSeq(streamName string, lastSyncSeq, rowsLoaded int64) error {
	result := d.gorm.Exec(`
		INSERT INTO dw_sync_state (stream_name, last_synced_at, last_sync_seq, rows_loaded, updated_at)
		VALUES (?, '1970-01-01 00:00:00+00', ?, ?, now())
		ON CONFLICT (stream_name) DO UPDATE SET
			last_sync_seq = GREATEST(dw_sync_state.last_sync_seq, EXCLUDED.last_sync_seq),
			rows_loaded = EXCLUDED.rows_loaded,
			updated_at = now()`, streamName, lastSyncSeq, rowsLoaded)
	if result.Error != nil {
		log.Error("Failed to upsert sequence dw_sync_state", "stream_name", streamName, "error", result.Error)
	}
	return result.Error
}
