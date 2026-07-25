package dw

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/the-web3/s78-market-services/database"
)

type KlineV2Diff struct {
	Missing    []string
	Extra      []string
	Mismatched []string
}

func (d KlineV2Diff) BlocksCutover() bool {
	return len(d.Extra) > 0 || len(d.Mismatched) > 0
}

func klineV2Key(row KlineV2JSONRow) string {
	return row.MarketID + "|" + row.Interval + "|" + row.OpenTime
}

func canonicalDecimal(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "0"
	}
	if rational, ok := new(big.Rat).SetString(value); ok {
		return rational.RatString()
	}
	return value
}

func equalKlineV2Content(a, b KlineV2JSONRow) bool {
	return a.MarketID == b.MarketID &&
		a.MarketCode == b.MarketCode &&
		a.SymbolGuid == b.SymbolGuid &&
		a.Interval == b.Interval &&
		a.OpenTime == b.OpenTime &&
		canonicalDecimal(a.OpenPrice) == canonicalDecimal(b.OpenPrice) &&
		canonicalDecimal(a.HighPrice) == canonicalDecimal(b.HighPrice) &&
		canonicalDecimal(a.LowPrice) == canonicalDecimal(b.LowPrice) &&
		canonicalDecimal(a.ClosePrice) == canonicalDecimal(b.ClosePrice) &&
		canonicalDecimal(a.Volume) == canonicalDecimal(b.Volume) &&
		canonicalDecimal(a.MarketCap) == canonicalDecimal(b.MarketCap) &&
		a.IsActive == b.IsActive &&
		a.SyncSeq == b.SyncSeq
}

// DiffKlineV2 compares business keys and materialized content. Timestamp audit
// columns are intentionally excluded because Doris stores second precision.
func DiffKlineV2(pgRows, dorisRows []KlineV2JSONRow) KlineV2Diff {
	pgByKey := make(map[string]KlineV2JSONRow, len(pgRows))
	dorisByKey := make(map[string]KlineV2JSONRow, len(dorisRows))
	for _, row := range pgRows {
		pgByKey[klineV2Key(row)] = row
	}
	for _, row := range dorisRows {
		dorisByKey[klineV2Key(row)] = row
	}

	var diff KlineV2Diff
	for key, pgRow := range pgByKey {
		dorisRow, ok := dorisByKey[key]
		if !ok {
			diff.Missing = append(diff.Missing, key)
			continue
		}
		if !equalKlineV2Content(pgRow, dorisRow) {
			diff.Mismatched = append(diff.Mismatched, key)
		}
	}
	for key := range dorisByKey {
		if _, ok := pgByKey[key]; !ok {
			diff.Extra = append(diff.Extra, key)
		}
	}
	sort.Strings(diff.Missing)
	sort.Strings(diff.Extra)
	sort.Strings(diff.Mismatched)
	return diff
}

func excludePendingKlineVersions(
	dorisRows []KlineV2JSONRow,
	currentPGRows []KlineV2JSONRow,
	throughSyncSeq int64,
) []KlineV2JSONRow {
	pendingKeys := make(map[string]struct{})
	for _, row := range currentPGRows {
		if row.SyncSeq > throughSyncSeq {
			pendingKeys[klineV2Key(row)] = struct{}{}
		}
	}
	if len(pendingKeys) == 0 {
		return dorisRows
	}
	filtered := make([]KlineV2JSONRow, 0, len(dorisRows))
	for _, row := range dorisRows {
		if _, pending := pendingKeys[klineV2Key(row)]; pending {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func (d *DW) reconcileKlinesV2() error {
	if d.dorisQuery == nil {
		return fmt.Errorf("Doris query connection is unavailable")
	}
	streams, err := d.db.DwSync.QueryDistinctKlineV2Streams()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(d.resourceCtx, 2*time.Minute)
	defer cancel()
	for _, stream := range streams {
		if err := d.reconcileOneKlineV2Stream(ctx, stream); err != nil {
			return fmt.Errorf("%s/%s: %w", stream.MarketID, stream.Interval, err)
		}
	}
	return nil
}

func (d *DW) reconcileOneKlineV2Stream(ctx context.Context, stream *database.KlineV2Stream) error {
	streamName := fmt.Sprintf("kline-v2:%s:%s", stream.MarketID, stream.Interval)
	state, err := d.db.DwSync.GetSyncState(streamName)
	if err != nil {
		return err
	}
	if state == nil || state.LastSyncSeq <= 0 {
		log.Info("dw shadow kline v2 reconciliation skipped before first successful load",
			"market_id", stream.MarketID, "interval", stream.Interval)
		return nil
	}
	maxSyncSeq := state.LastSyncSeq
	pgModels, err := d.db.DwSync.QuerySymbolKlinesV2ThroughSeq(
		stream.MarketID, stream.Interval, maxSyncSeq,
	)
	if err != nil {
		return err
	}
	pgRows := BuildKlineV2JSONRows(pgModels, stream.MarketCode)
	currentPGModels, err := d.db.DwSync.QueryAllSymbolKlinesV2(stream.MarketID, stream.Interval)
	if err != nil {
		return err
	}
	currentPGRows := BuildKlineV2JSONRows(currentPGModels, stream.MarketCode)
	dorisRows, err := d.queryDorisKlineV2Rows(ctx, stream.MarketID, stream.Interval, maxSyncSeq)
	if err != nil {
		return err
	}
	// A current candle can have an old Doris version below the successful
	// watermark while its corrected PG version is already above it. That key
	// is pending synchronization, not an extra Doris key.
	dorisRows = excludePendingKlineVersions(dorisRows, currentPGRows, maxSyncSeq)
	diff := DiffKlineV2(pgRows, dorisRows)
	if len(diff.Extra) > 0 {
		log.Error("dw shadow kline v2 reconciliation blocks cutover",
			"market_id", stream.MarketID, "interval", stream.Interval,
			"through_sync_seq", maxSyncSeq, "extra_keys", diff.Extra)
		return fmt.Errorf("Doris reconciliation conflict: extra_keys=%d; cutover is blocked",
			len(diff.Extra))
	}
	if len(diff.Missing) == 0 && len(diff.Mismatched) == 0 {
		log.Debug("dw shadow kline v2 reconciliation passed",
			"market_id", stream.MarketID, "interval", stream.Interval,
			"through_sync_seq", maxSyncSeq, "keys", len(pgRows))
		return nil
	}

	pgByKey := make(map[string]KlineV2JSONRow, len(pgRows))
	for _, row := range pgRows {
		pgByKey[klineV2Key(row)] = row
	}
	repairKeys := append(append([]string(nil), diff.Missing...), diff.Mismatched...)
	repairRows := make([]KlineV2JSONRow, 0, len(repairKeys))
	for _, key := range repairKeys {
		repairRows = append(repairRows, pgByKey[key])
	}
	sort.Slice(repairRows, func(i, j int) bool { return repairRows[i].SyncSeq < repairRows[j].SyncSeq })
	repairStreamName := fmt.Sprintf("reconcile:%s:%s", stream.MarketID, stream.Interval)
	for start := 0; start < len(repairRows); start += klineBatchSize {
		end := min(start+klineBatchSize, len(repairRows))
		batch := repairRows[start:end]
		payload, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		minSeq, maxSeq := batch[0].SyncSeq, batch[len(batch)-1].SyncSeq
		label := StreamLoadSeqLabel(klineV2Table, repairStreamName, minSeq, maxSeq, payload)
		if _, err := d.loader.Load(ctx, klineV2Table, label, payload); err != nil {
			return fmt.Errorf("automatic exact-key replay: %w", err)
		}
	}

	verifiedRows, err := d.queryDorisKlineV2Rows(ctx, stream.MarketID, stream.Interval, maxSyncSeq)
	if err != nil {
		return fmt.Errorf("verify automatic replay: %w", err)
	}
	verifiedRows = excludePendingKlineVersions(verifiedRows, currentPGRows, maxSyncSeq)
	remaining := DiffKlineV2(pgRows, verifiedRows)
	if len(remaining.Missing) > 0 || len(remaining.Extra) > 0 || len(remaining.Mismatched) > 0 {
		return fmt.Errorf(
			"Doris reconciliation still differs after replay: missing=%d extra=%d content_conflicts=%d",
			len(remaining.Missing), len(remaining.Extra), len(remaining.Mismatched),
		)
	}
	log.Warn("dw shadow kline v2 differences automatically replayed and verified",
		"market_id", stream.MarketID, "interval", stream.Interval,
		"through_sync_seq", maxSyncSeq,
		"missing", len(diff.Missing), "content_conflicts", len(diff.Mismatched))
	return nil
}

func (d *DW) queryDorisKlineV2Rows(ctx context.Context, marketID, interval string, maxSyncSeq int64) ([]KlineV2JSONRow, error) {
	rows, err := d.dorisQuery.QueryContext(ctx, `
		SELECT market_id, market_code, symbol_guid, `+"`interval`"+`, open_time,
		       open_price, high_price, low_price, close_price, volume, market_cap,
		       is_active, ingested_at, updated_at, sync_seq
		FROM dwd_market_kline_v2
		WHERE market_id = ? AND `+"`interval`"+` = ? AND sync_seq <= ?`,
		marketID, interval, maxSyncSeq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []KlineV2JSONRow
	for rows.Next() {
		var row KlineV2JSONRow
		if err := rows.Scan(
			&row.MarketID, &row.MarketCode, &row.SymbolGuid, &row.Interval, &row.OpenTime,
			&row.OpenPrice, &row.HighPrice, &row.LowPrice, &row.ClosePrice,
			&row.Volume, &row.MarketCap, &row.IsActive,
			&row.IngestedAt, &row.UpdatedAt, &row.SyncSeq,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
