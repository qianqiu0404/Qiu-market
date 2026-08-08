-- Support the PostgreSQL -> Doris legacy K-line backfill cursor without a
-- full symbol_kline scan for every 500-row batch. CONCURRENTLY keeps market
-- ingestion available while the index is built on the Mac mini staging DB.

CREATE INDEX CONCURRENTLY IF NOT EXISTS symbol_kline_dw_stream_created_at_idx
    ON symbol_kline (symbol_guid, "interval", created_at);
