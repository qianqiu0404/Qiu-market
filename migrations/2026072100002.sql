-- ============================================
-- symbol_kline: 新增原生周期字段（interval）
-- K线按周期（1m/15m/1h/1d）原生存储，不再由 1m 聚合
-- ============================================

ALTER TABLE symbol_kline ADD COLUMN IF NOT EXISTS "interval" varchar(10) NOT NULL DEFAULT '1m';

CREATE INDEX IF NOT EXISTS idx_symbol_kline_symbol_interval_created_at
    ON symbol_kline (symbol_guid, "interval", created_at DESC);
