package dw

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	_ "github.com/go-sql-driver/mysql"

	"github.com/the-web3/s78-market-services/common/tasks"
	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
)

const (
	// syncInterval 是数仓同步节奏：每 60s 把 PG 增量推到 Doris。
	syncInterval = 60 * time.Second
	// klineBatchSize 单批 Stream Load 的 K 线行数上限。
	klineBatchSize = 500
	// maxKlineBatchesPerCycle 单条同步流每周期最多追的批数，防止首轮追历史
	// 把单个周期拖得过长（追不完的留给下一周期，水位已推进）。
	maxKlineBatchesPerCycle = 20
	// klineV2ReplayLookback protects against PostgreSQL sequence allocation
	// order differing from transaction commit order.
	klineV2ReplayLookback int64 = 1000
	klineV2Table                = "dwd_market_kline_v2"
	// marketSnapshotStream 行情快照流在 dw_sync_state 中的固定流名。
	marketSnapshotStream     = "market_snapshot"
	marketSnapshotV2Stream   = "market_snapshot_v2"
	marketSnapshotV2Table    = "dws_market_snapshot_v2"
	klineV2ReconcileInterval = 24 * time.Hour
	// A full personal-server reconciliation walks every market/interval stream.
	// The 2.5M-row Mac mini baseline exceeded the old two-minute budget while
	// still making progress, so allow one bounded five-minute audit per day.
	klineV2ReconcileTimeout  = 5 * time.Minute
	klineV2ReconcileMinRetry = 5 * time.Minute
	klineV2ReconcileMaxRetry = time.Hour
)

// DW 是 PostgreSQL -> Apache Doris 的数仓同步进程。
// 独立于 worker 运行（cli 的 dw 模式）：worker 负责实时聚合（Redis -> PG），
// DW 负责分析型同步（PG -> Doris），两者节奏、容错语义完全不同。
type DW struct {
	db         *database.DB
	loader     *StreamLoader
	dorisQuery *sql.DB
	cfg        config.DorisConfig
	stopped    atomic.Bool

	resourceCtx                  context.Context
	resourceCancel               context.CancelFunc
	tasks                        tasks.Group
	lastKlineV2ReconcileAttempt  time.Time
	lastKlineV2ReconcileSuccess  time.Time
	nextKlineV2ReconcileAttempt  time.Time
	klineV2ReconcileFailureCount int
}

// NewDW 创建数仓同步进程；cfg.Enabled() 为 false 时返回明确错误（拒绝启动）。
func NewDW(db *database.DB, cfg config.DorisConfig, shutdown context.CancelCauseFunc) (*DW, error) {
	if !cfg.Enabled() {
		return nil, fmt.Errorf("doris data warehouse is not configured: MARKET_DORIS_HOST is empty (set it or start the compose doris service)")
	}
	resourceCtx, resourceCancel := context.WithCancel(context.Background())
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?timeout=5s&readTimeout=30s&interpolateParams=true",
		cfg.User, cfg.Password, cfg.Host, cfg.QueryPort, cfg.Database)
	dorisQuery, err := sql.Open("mysql", dsn)
	if err != nil {
		resourceCancel()
		return nil, fmt.Errorf("open Doris reconciliation connection: %w", err)
	}
	dorisQuery.SetMaxOpenConns(2)
	dorisQuery.SetMaxIdleConns(1)
	dorisQuery.SetConnMaxLifetime(5 * time.Minute)
	return &DW{
		db:             db,
		loader:         NewStreamLoader(cfg),
		dorisQuery:     dorisQuery,
		cfg:            cfg,
		resourceCtx:    resourceCtx,
		resourceCancel: resourceCancel,
		tasks: tasks.Group{HandleCrit: func(err error) {
			shutdown(fmt.Errorf("dw sync critical error: %v", err))
		}},
	}, nil
}

func (d *DW) Start(ctx context.Context) error {
	log.Info("Starting dw (PostgreSQL -> Doris sync)",
		"doris", fmt.Sprintf("%s:%d", d.cfg.Host, d.cfg.HttpPort),
		"database", d.cfg.Database,
		"interval", syncInterval.String())

	d.tasks.Go(func() error {
		// 启动即跑一轮，不等第一个 tick
		d.syncCycle()
		ticker := time.NewTicker(syncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.syncCycle()
			case <-d.resourceCtx.Done():
				log.Info("dw sync shutting down")
				return fmt.Errorf("dw sync service stopped")
			}
		}
	})
	return nil
}

// syncCycle 跑一轮完整同步；任何一步失败只记日志，不中断进程（log-and-continue）。
func (d *DW) syncCycle() {
	cycleStart := time.Now()
	klinesLoaded := d.syncKlines()
	klinesV2Loaded := d.syncKlinesV2()
	snapshotsLoaded := d.syncMarketSnapshot()
	d.maybeReconcileKlinesV2(time.Now())
	log.Info("dw sync cycle done",
		"kline_rows", klinesLoaded,
		"kline_v2_rows", klinesV2Loaded,
		"snapshot_rows", snapshotsLoaded,
		"elapsed", time.Since(cycleStart).Round(time.Millisecond).String())
}

func reconcileRetryDelay(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	delay := klineV2ReconcileMinRetry
	for attempt := 1; attempt < failures && delay < klineV2ReconcileMaxRetry; attempt++ {
		delay *= 2
		if delay >= klineV2ReconcileMaxRetry {
			return klineV2ReconcileMaxRetry
		}
	}
	return delay
}

func (d *DW) maybeReconcileKlinesV2(now time.Time) {
	if !d.nextKlineV2ReconcileAttempt.IsZero() && now.Before(d.nextKlineV2ReconcileAttempt) {
		return
	}
	if !d.lastKlineV2ReconcileSuccess.IsZero() &&
		now.Sub(d.lastKlineV2ReconcileSuccess) < klineV2ReconcileInterval {
		d.nextKlineV2ReconcileAttempt = d.lastKlineV2ReconcileSuccess.Add(klineV2ReconcileInterval)
		return
	}

	d.lastKlineV2ReconcileAttempt = now
	if err := d.db.DWAcceptance.RecordAttempt(database.KlineV2AcceptanceStream, now); err != nil {
		log.Error("dw reconciliation acceptance attempt write failed", "error", err)
	}
	if err := d.reconcileKlinesV2(); err != nil {
		if stateErr := d.db.DWAcceptance.RecordFailure(
			database.KlineV2AcceptanceStream, now, err.Error(),
		); stateErr != nil {
			log.Error("dw reconciliation acceptance failure write failed", "error", stateErr)
		}
		d.klineV2ReconcileFailureCount++
		retryIn := reconcileRetryDelay(d.klineV2ReconcileFailureCount)
		d.nextKlineV2ReconcileAttempt = now.Add(retryIn)
		log.Error("dw shadow kline v2 reconciliation fail",
			"error", err,
			"attempted_at", now.UTC().Format(time.RFC3339),
			"last_success_at", formatOptionalTime(d.lastKlineV2ReconcileSuccess),
			"retry_in", retryIn.String())
		return
	}

	if err := d.db.DWAcceptance.RecordSuccess(database.KlineV2AcceptanceStream, now); err != nil {
		log.Error("dw reconciliation acceptance success write failed", "error", err)
	}
	d.lastKlineV2ReconcileSuccess = now
	d.klineV2ReconcileFailureCount = 0
	d.nextKlineV2ReconcileAttempt = now.Add(klineV2ReconcileInterval)
	log.Info("dw shadow kline v2 reconciliation cycle passed",
		"attempted_at", d.lastKlineV2ReconcileAttempt.UTC().Format(time.RFC3339),
		"next_attempt_at", d.nextKlineV2ReconcileAttempt.UTC().Format(time.RFC3339))
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
}

// syncKlinesV2 is a shadow path: it cannot affect the old Doris table, old
// watermarks, or public analytics queries. One sequence ceiling is captured at
// cycle start so the batch has a stable upper boundary.
func (d *DW) syncKlinesV2() int64 {
	ceiling, err := d.db.DwSync.QueryKlineSyncCeiling()
	if err != nil {
		log.Error("dw query kline v2 sequence ceiling fail", "error", err)
		return 0
	}
	streams, err := d.db.DwSync.QueryDistinctKlineV2Streams()
	if err != nil {
		log.Error("dw query kline v2 streams fail", "error", err)
		return 0
	}

	var total int64
	for _, stream := range streams {
		loaded, err := d.syncOneKlineV2Stream(stream, ceiling)
		if err != nil {
			log.Error("dw sync kline v2 stream fail",
				"market_id", stream.MarketID, "market_code", stream.MarketCode,
				"interval", stream.Interval, "ceiling", ceiling, "error", err)
			continue
		}
		total += loaded
	}
	return total
}

func (d *DW) syncOneKlineV2Stream(stream *database.KlineV2Stream, ceiling int64) (int64, error) {
	streamName := fmt.Sprintf("kline-v2:%s:%s", stream.MarketID, stream.Interval)
	state, err := d.db.DwSync.GetSyncState(streamName)
	if err != nil {
		return 0, err
	}
	var lastSyncSeq, rowsLoaded int64
	if state != nil {
		lastSyncSeq = state.LastSyncSeq
		rowsLoaded = state.RowsLoaded
	}
	cursor := ReplayScanStart(lastSyncSeq, klineV2ReplayLookback)
	maxLoadedSeq := lastSyncSeq
	var synced int64

	for batch := 0; batch < maxKlineBatchesPerCycle; batch++ {
		list, err := d.db.DwSync.QuerySymbolKlinesBySyncSeq(
			stream.MarketID, stream.Interval, cursor, ceiling, klineBatchSize,
		)
		if err != nil {
			return synced, err
		}
		if len(list) == 0 {
			break
		}

		rows := BuildKlineV2JSONRows(list, stream.MarketCode)
		payload, err := json.Marshal(rows)
		if err != nil {
			return synced, fmt.Errorf("marshal kline v2 batch: %w", err)
		}
		minSeq, maxSeq := list[0].SyncSeq, list[len(list)-1].SyncSeq
		label := StreamLoadSeqLabel(klineV2Table, streamName, minSeq, maxSeq, payload)
		n, err := d.loader.Load(d.resourceCtx, klineV2Table, label, payload)
		if err != nil {
			return synced, err
		}

		cursor = maxSeq
		if maxSeq > maxLoadedSeq {
			maxLoadedSeq = maxSeq
		}
		rowsLoaded += n
		synced += n
		if err := d.db.DwSync.UpsertSyncStateSeq(streamName, maxLoadedSeq, rowsLoaded); err != nil {
			return synced, err
		}
		log.Debug("dw stream loaded shadow kline v2 batch",
			"market_id", stream.MarketID, "market_code", stream.MarketCode,
			"interval", stream.Interval, "rows", n,
			"min_seq", minSeq, "max_seq", maxSeq, "ceiling", ceiling)

		if len(list) < klineBatchSize {
			break
		}
	}
	return synced, nil
}

// syncKlines 按 (symbol_guid, interval) 逐条流做增量同步，返回本周期推入总行数。
func (d *DW) syncKlines() int64 {
	streams, err := d.db.DwSync.QueryDistinctKlineStreams()
	if err != nil {
		log.Error("dw query kline streams fail", "error", err)
		return 0
	}

	var total int64
	for _, stream := range streams {
		loaded, err := d.syncOneKlineStream(stream.SymbolGuid, stream.Interval)
		if err != nil {
			// 单条流失败不影响其它流；水位不推进，下周期重试
			log.Error("dw sync kline stream fail",
				"symbol_guid", stream.SymbolGuid, "interval", stream.Interval, "error", err)
			continue
		}
		total += loaded
	}
	return total
}

func (d *DW) syncOneKlineStream(symbolGuid, interval string) (int64, error) {
	streamName := fmt.Sprintf("kline:%s:%s", symbolGuid, interval)

	state, err := d.db.DwSync.GetSyncState(streamName)
	if err != nil {
		return 0, err
	}
	watermark := time.Time{} // 首次同步：从零水位追全量（分批，见 maxKlineBatchesPerCycle）
	var rowsLoaded int64
	if state != nil {
		watermark = state.LastSyncedAt
		rowsLoaded = state.RowsLoaded
	}

	var synced int64
	for batch := 0; batch < maxKlineBatchesPerCycle; batch++ {
		list, err := d.db.DwSync.QuerySymbolKlinesAfter(symbolGuid, interval, watermark, klineBatchSize)
		if err != nil {
			return synced, err
		}
		if len(list) == 0 {
			break
		}

		payload, err := json.Marshal(BuildKlineJSONRows(list))
		if err != nil {
			return synced, fmt.Errorf("marshal kline batch: %w", err)
		}

		label := StreamLoadLabel("dwd_symbol_kline", time.Now())
		n, err := d.loader.Load(d.resourceCtx, "dwd_symbol_kline", label, payload)
		if err != nil {
			return synced, err
		}

		// 只有 Stream Load 成功才推进水位（at-least-once 之外的另一条路是
		// 不重推丢数据，代价不可接受；重复推由 DUPLICATE KEY 表容忍）
		watermark = list[len(list)-1].CreatedAt
		rowsLoaded += n
		synced += n
		if err := d.db.DwSync.UpsertSyncState(streamName, watermark, rowsLoaded); err != nil {
			return synced, err
		}
		log.Debug("dw stream loaded kline batch",
			"symbol_guid", symbolGuid, "interval", interval,
			"rows", n, "watermark", watermark.UTC().Format(time.RFC3339))

		if len(list) < klineBatchSize {
			break // 已追平
		}
	}
	return synced, nil
}

// syncMarketSnapshot 对 symbol_market 当前状态做一次全量快照，返回推入行数。
func (d *DW) syncMarketSnapshot() int64 {
	rows, err := d.db.DwSync.QueryMarketSnapshotRows()
	if err != nil {
		log.Error("dw query market snapshot rows fail", "error", err)
		return 0
	}
	if len(rows) == 0 {
		return 0
	}

	capturedAt := time.Now().UTC().Truncate(time.Second)
	payload, err := json.Marshal(BuildSnapshotJSONRows(rows, capturedAt))
	if err != nil {
		log.Error("dw marshal snapshot batch fail", "error", err)
	}
	var total int64
	if err == nil {
		total += d.loadMarketSnapshotTable(
			"dws_symbol_market_snapshot",
			marketSnapshotStream,
			payload,
			capturedAt,
		)
	}
	v2Payload, err := json.Marshal(BuildSnapshotV2JSONRows(rows, capturedAt))
	if err != nil {
		log.Error("dw marshal market snapshot v2 batch fail", "error", err)
		return total
	}
	total += d.loadMarketSnapshotTable(
		marketSnapshotV2Table,
		marketSnapshotV2Stream,
		v2Payload,
		capturedAt,
	)
	return total
}

func (d *DW) loadMarketSnapshotTable(
	table, stream string,
	payload []byte,
	capturedAt time.Time,
) int64 {
	label := StreamLoadLabel(table, capturedAt)
	loaded, err := d.loader.Load(d.resourceCtx, table, label, payload)
	if err != nil {
		log.Error("dw stream load market snapshot table fail",
			"table", table, "label", label, "error", err)
		return 0
	}
	state, err := d.db.DwSync.GetSyncState(stream)
	if err != nil {
		log.Error("dw query market snapshot sync state fail", "table", table, "error", err)
		return loaded
	}
	var rowsLoaded int64
	if state != nil {
		rowsLoaded = state.RowsLoaded
	}
	if err := d.db.DwSync.UpsertSyncState(
		stream,
		capturedAt,
		rowsLoaded+loaded,
	); err != nil {
		log.Error("dw upsert market snapshot sync state fail", "table", table, "error", err)
	}
	log.Debug("dw stream loaded market snapshot",
		"table", table, "rows", loaded, "captured_at", capturedAt.Format(time.RFC3339))
	return loaded
}

func (d *DW) Stop(ctx context.Context) error {
	log.Info("Stopping dw")
	d.resourceCancel()
	if d.dorisQuery != nil {
		_ = d.dorisQuery.Close()
	}
	if err := d.tasks.Wait(); err != nil {
		log.Debug("dw tasks wait", "err", err)
	}
	d.stopped.Store(true)
	return nil
}

func (d *DW) Stopped() bool {
	return d.stopped.Load()
}
