-- ============================================
-- dw_sync_state: dw 进程（PostgreSQL -> Doris 数仓同步）的增量水位表
-- 每个同步流一行：kline:<symbol_guid>:<interval> / market_snapshot
-- last_synced_at 为该流已同步到的水位（K 线为 open_time，快照为 captured_at）
-- ============================================

CREATE TABLE IF NOT EXISTS dw_sync_state (
    stream_name    text        PRIMARY KEY,
    last_synced_at timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    rows_loaded    bigint      NOT NULL DEFAULT 0,
    updated_at     timestamptz NOT NULL DEFAULT now()
);
