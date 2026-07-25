-- ============================================
-- S78 Market Services · Apache Doris 数仓初始化
-- 执行方式（Doris all-in-one 启动并就绪后）：
--   mysql -h127.0.0.1 -P9030 -uroot < script/doris-init.sql
-- 或交互式：mysql -h127.0.0.1 -P9030 -uroot 后 source 本文件。
-- 语句全部幂等（IF NOT EXISTS），可重复执行。
-- ============================================

CREATE DATABASE IF NOT EXISTS s78_market_dw;

USE s78_market_dw;

-- --------------------------------------------
-- DWD 层：K 线明细
-- 来源：PostgreSQL symbol_kline（dw 进程每 60s 增量同步）。
-- 精度：PG 侧价格 / 成交量是「1e8 放大的整数字符串」（numeric(65,18) / uint256），
--       入仓前由 dw 进程统一还原为人类可读小数，这里用 DECIMAL(38,12) 精确存储，
--       不用 DOUBLE —— 金额类字段避免二进制浮点误差在聚合（SUM/AVG/STDDEV）中累积，
--       与本项目「不使用 float 存价格」的原则一致。38 位总宽足以容纳
--       还原后的成交量与市值量级，12 位小数覆盖 1e8 还原精度（8 位）并留有余量。
-- 表模型：DUPLICATE KEY —— K 线是 append-only 明细，同 key 重复行保留两份也无
--         业务含义冲突（dw 的 watermark 保证不重复推）；键列顺序
--         (symbol_guid, interval, open_time) 与分析查询的过滤 / 排序模式一致
--         （按 symbol+interval 过滤、按 open_time 排序取首末价）。
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS dwd_symbol_kline (
    symbol_guid VARCHAR(100)   NOT NULL COMMENT '交易对 GUID，对应 PG symbol.guid',
    `interval`  VARCHAR(10)    NOT NULL COMMENT 'K 线周期：1m/15m/1h/1d',
    open_time   DATETIME       NOT NULL COMMENT 'K 线开盘时间（UTC，取 PG symbol_kline.created_at）',
    open_price  DECIMAL(38,12) NOT NULL DEFAULT '0' COMMENT '开盘价（已从 1e8 整数还原）',
    high_price  DECIMAL(38,12) NOT NULL DEFAULT '0' COMMENT '最高价（已还原）',
    low_price   DECIMAL(38,12) NOT NULL DEFAULT '0' COMMENT '最低价（已还原）',
    close_price DECIMAL(38,12) NOT NULL DEFAULT '0' COMMENT '收盘价（已还原）',
    volume      DECIMAL(38,12) NOT NULL DEFAULT '0' COMMENT '成交量（已还原）'
)
DUPLICATE KEY(symbol_guid, `interval`, open_time)
COMMENT 'DWD：K 线明细（自 PG symbol_kline 增量同步）'
DISTRIBUTED BY HASH(symbol_guid) BUCKETS 4
PROPERTIES (
    "replication_num" = "1"
);

-- --------------------------------------------
-- Slice 1 shadow table: stable market identity + explicit open_time.
-- The old dwd_symbol_kline remains untouched for rollback. UNIQUE KEY makes
-- the fixed sync_seq replay window and exact-key repair idempotent overwrites.
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS dwd_market_kline_v2 (
    market_id   VARCHAR(100)   NOT NULL COMMENT 'Opaque exchange_symbol.guid',
    `interval`  VARCHAR(10)    NOT NULL COMMENT 'K-line interval',
    open_time   DATETIME       NOT NULL COMMENT 'Explicit PG business open_time (UTC)',
    market_code VARCHAR(255)   NOT NULL COMMENT 'Auditable exchange:BASE/QUOTE:type code',
    symbol_guid VARCHAR(100)   NOT NULL COMMENT 'Legacy compatibility identity',
    open_price  DECIMAL(38,12) NOT NULL DEFAULT '0',
    high_price  DECIMAL(38,12) NOT NULL DEFAULT '0',
    low_price   DECIMAL(38,12) NOT NULL DEFAULT '0',
    close_price DECIMAL(38,12) NOT NULL DEFAULT '0',
    volume      DECIMAL(38,12) NOT NULL DEFAULT '0',
    market_cap  DECIMAL(38,12) NOT NULL DEFAULT '0',
    is_active   BOOLEAN        NOT NULL DEFAULT '1',
    ingested_at DATETIME       NOT NULL COMMENT 'First PG ingestion (legacy rows are approximated)',
    updated_at  DATETIME       NOT NULL COMMENT 'Last material-content change',
    sync_seq    BIGINT         NOT NULL COMMENT 'PG material-content change sequence'
)
UNIQUE KEY(market_id, `interval`, open_time)
COMMENT 'Slice 1 shadow DWD K-lines; not used by public analytics until cutover'
DISTRIBUTED BY HASH(market_id) BUCKETS 4
PROPERTIES (
    "replication_num" = "1",
    "enable_unique_key_merge_on_write" = "true"
);

-- --------------------------------------------
-- DWS 层：行情快照
-- 来源：PostgreSQL symbol_market（dw 进程每 60s 对当前行情全量快照一次），
--       exchange 由 exchange_symbol + exchange 关联解析（一个 symbol 取第一个交易所）。
-- change24h 原样透传 PG symbol_market.radio（注意：Binance 链路写入的是百分比，
--       worker 的 calcRate 会按「当日首行比率」覆盖同一行，口径混用是既有行为，
--       见 docs/dex-hyperliquid.md「涨跌幅双写」一节，数仓不擅自修正口径）。
-- 表模型：DUPLICATE KEY(symbol_guid, captured_at) —— 同一交易对同一秒的快照天然
--         唯一，重复推同一批快照只是产生重复行，聚合查询用 max_by 兜底不受影响。
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS dws_symbol_market_snapshot (
    symbol_guid VARCHAR(100)   NOT NULL COMMENT '交易对 GUID',
    captured_at DATETIME       NOT NULL COMMENT '快照采集时间（UTC，dw 进程每 60s 一批）',
    exchange    VARCHAR(64)    NOT NULL DEFAULT '' COMMENT '交易所名（Binance / Hyperliquid / 空）',
    price       DECIMAL(38,12) NOT NULL DEFAULT '0' COMMENT '最新价（已还原）',
    volume      DECIMAL(38,12) NOT NULL DEFAULT '0' COMMENT '24h 成交量（已还原）',
    market_cap  DECIMAL(38,12) NOT NULL DEFAULT '0' COMMENT '市值（已还原，DEX 恒为 0）',
    change24h   DECIMAL(18,6)  NOT NULL DEFAULT '0' COMMENT '24h 涨跌幅（透传 PG radio）'
)
DUPLICATE KEY(symbol_guid, captured_at)
COMMENT 'DWS：symbol_market 每 60s 全量快照'
DISTRIBUTED BY HASH(symbol_guid) BUCKETS 4
PROPERTIES (
    "replication_num" = "1"
);

CREATE TABLE IF NOT EXISTS dws_market_snapshot_v2 (
    market_id           VARCHAR(100)   NOT NULL COMMENT 'Opaque exchange_symbol.guid',
    captured_at         DATETIME       NOT NULL COMMENT 'DW observation time in UTC',
    market_code         VARCHAR(255)   NOT NULL COMMENT 'Auditable market code',
    symbol_guid         VARCHAR(100)   NOT NULL COMMENT 'Legacy compatibility identity',
    exchange            VARCHAR(64)    NOT NULL DEFAULT '',
    price               DECIMAL(38,12) NOT NULL DEFAULT '0',
    volume              DECIMAL(38,12) NOT NULL DEFAULT '0',
    market_cap          DECIMAL(38,12) NOT NULL DEFAULT '0',
    change_24h_pct      DECIMAL(18,6)  NULL COMMENT 'Canonical 24h percentage; NULL means unknown',
    provider_updated_at DATETIME       NULL COMMENT 'Latest accepted provider observation'
)
UNIQUE KEY(market_id, captured_at)
COMMENT 'Slice 2 market snapshots with stable identity and nullable canonical change'
DISTRIBUTED BY HASH(market_id) BUCKETS 4
PROPERTIES (
    "replication_num" = "1",
    "enable_unique_key_merge_on_write" = "true"
);

-- --------------------------------------------
-- 生产环境提示（本地 all-in-one 不需要）：
-- 1. replication_num 应提升为 3；
-- 2. 明细表数据量大后可按天 RANGE 分区（PARTITION BY RANGE(open_time)），
--    并配合动态分区属性自动滚动；
-- 3. BUCKETS 数可按 BE 节点数 × 磁盘数调整。
-- 这些属于容量规划动作，刻意不放进本地初始化脚本，保持「开箱即用」。
-- --------------------------------------------
