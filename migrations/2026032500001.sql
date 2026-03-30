-- ============================================
-- 行情服务数据库初始化脚本
-- Market Services System Database Schema
-- ============================================

-- 创建自定义类型：UINT256（无符号 256 位整数）
DO
$$
BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'uint256') THEN
            CREATE DOMAIN UINT256 AS NUMERIC CHECK (VALUE >= 0 AND VALUE < POWER(CAST(2 AS NUMERIC), CAST(256 AS NUMERIC)) AND SCALE(VALUE) = 0);
        ELSE
            ALTER DOMAIN UINT256 DROP CONSTRAINT uint256_check;
            ALTER DOMAIN UINT256 ADD CHECK (VALUE >= 0 AND VALUE < POWER(CAST(2 AS NUMERIC), CAST(256 AS NUMERIC)) AND SCALE(VALUE) = 0);
        END IF;
    END
$$;

-- 启用 UUID 扩展
CREATE EXTENSION IF NOT EXISTS "uuid-ossp" CASCADE;


-- ============================================
-- 资产配置表 (Asset)
-- ============================================
CREATE TABLE IF NOT EXISTS asset (
    guid                 TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),  -- 主键：资产唯一标识
    asset_name           VARCHAR(100) NOT NULL DEFAULT 'Tether USDT',  -- 资产名称（如：Tether USDT）
    asset_symbol         VARCHAR(20)  NOT NULL DEFAULT 'USDT',     -- 资产符号（如：USD、BTC、ETH）
    asset_logo           VARCHAR(500) NOT NULL,                   -- 资产图标 URL
    is_active            BOOLEAN NOT NULL DEFAULT TRUE,           -- 是否启用（新增字段）
    created_at           TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,  -- 创建时间
    updated_at           TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP  -- 更新时间
);
CREATE INDEX IF NOT EXISTS idx_asset_guid ON asset (guid);
CREATE INDEX IF NOT EXISTS idx_asset_symbol ON asset (asset_symbol);  -- 新增：按符号查询
CREATE INDEX IF NOT EXISTS idx_asset_is_active ON asset (is_active);  -- 新增：按状态查询


-- --------------------------------------------
-- 交易所表 (exchange)
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS exchange(
    guid               TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),  -- 交易所唯一标识
    name               VARCHAR(100) NOT NULL UNIQUE,             -- 交易所名称（唯一），如：Binance、OKX、ByteLink事件预测平台
    config             JSONB,                                    -- 交易所配置（JSON格式），存储API Key、Secret、Endpoint等敏感信息
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,            -- 是否启用（新增字段）
    created_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,   -- 创建时间
    updated_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP    -- 更新时间
);
CREATE INDEX IF NOT EXISTS idx_exchange_guid ON exchange (guid);
CREATE INDEX IF NOT EXISTS idx_exchange_is_active ON exchange (is_active);
CREATE INDEX IF NOT EXISTS idx_exchange_created_at ON exchange (created_at);

-- --------------------------------------------
-- 交易对表 (symbol)
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS symbol(
    guid               TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),
    symbol_name        VARCHAR(100) NOT NULL,
    base_asset_guid    VARCHAR(100) NOT NULL,
    qoute_asset_guid   VARCHAR(100) NOT NULL,
    market_type        VARCHAR(100) NOT NULL DEFAULT 'SPOT',  -- SPOT:现货,FUTURE:期货,OPTION期权
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_symbol_guid ON symbol (guid);
CREATE INDEX IF NOT EXISTS idx_symbol_is_active ON symbol (is_active);
CREATE INDEX IF NOT EXISTS idx_symbol_created_at ON symbol (created_at);

-- --------------------------------------------
-- 法币汇率表 (currency)
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS currency(
    guid               TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),
    currency_name      VARCHAR(100) NOT NULL,
    currency_code      VARCHAR(100) NOT NULL,
    rate               NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (rate >= 0),
    buy_spread         NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (buy_spread >= 0),
    sell_spread        NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (buy_spread >= 0),
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_currency_guid ON currency (guid);
CREATE INDEX IF NOT EXISTS idx_currency_is_active ON currency (is_active);
CREATE INDEX IF NOT EXISTS idx_currency_created_at ON currency (created_at);


-- --------------------------------------------
-- 交易所和交易对关联表 (exchange_symbol)
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS exchange_symbol(
    guid               TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),
    exchange_guid      VARCHAR(100) NOT NULL,
    symbol_guid        VARCHAR(100) NOT NULL,
    price              NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (price >= 0),
    ask_price          NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (ask_price >= 0),
    bid_price          NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (bid_price >= 0),
    volume             UINT256 NOT NULL,
    radio              NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (radio >= 0),
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_exchange_symbol_guid ON exchange_symbol (guid);
CREATE INDEX IF NOT EXISTS idx_exchange_symbol_is_active ON exchange_symbol (is_active);
CREATE INDEX IF NOT EXISTS idx_exchange_symbol_created_at ON exchange_symbol (created_at);

-- --------------------------------------------
-- exchange_symbol_kline
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS exchange_symbol_kline(
    guid               TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),
    exchange_guid      VARCHAR(100) NOT NULL,
    symbol_guid        VARCHAR(100) NOT NULL,
    open_price         NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (open_price >= 0),
    close_price        NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (close_price >= 0),
    high_price         NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (high_price >= 0),
    low_price         NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (low_price >= 0),
    volume             UINT256 NOT NULL,
    market_cap         UINT256 NOT NULL,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_exchange_symbol_kline_guid ON exchange_symbol_kline (guid);
CREATE INDEX IF NOT EXISTS idx_exchange_symbol_kline_is_active ON exchange_symbol_kline (is_active);
CREATE INDEX IF NOT EXISTS idx_exchange_symbol_kline_created_at ON exchange_symbol_kline (created_at);

-- --------------------------------------------
-- symbol_market
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS symbol_market(
    guid               TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),
    symbol_guid        VARCHAR(100) NOT NULL,
    price              NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (price >= 0),
    ask_price          NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (ask_price >= 0),
    bid_price          NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (bid_price >= 0),
    volume             UINT256 NOT NULL,
    market_cap         UINT256 NOT NULL,
    radio              NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (radio >= 0),
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_symbol_market_guid ON symbol_market (guid);
CREATE INDEX IF NOT EXISTS idx_symbol_market_is_active ON symbol_market (is_active);
CREATE INDEX IF NOT EXISTS idx_symbol_market_created_at ON symbol_market (created_at);

-- --------------------------------------------
-- symbol_market_currey
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS symbol_market_currey(
    guid               TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),
    symbol_guid        VARCHAR(100) NOT NULL,
    currey_guid        VARCHAR(100) NOT NULL,
    price              NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (price >= 0),
    ask_price          NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (ask_price >= 0),
    bid_price          NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (bid_price >= 0),
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_symbol_market_currey_guid ON symbol_market_currey (guid);
CREATE INDEX IF NOT EXISTS idx_symbol_market_currey_is_active ON symbol_market_currey (is_active);
CREATE INDEX IF NOT EXISTS idx_symbol_market_currey_created_at ON symbol_market_currey (created_at);

-- --------------------------------------------
-- symbol_kline
-- --------------------------------------------
CREATE TABLE IF NOT EXISTS symbol_kline(
    guid               TEXT PRIMARY KEY DEFAULT replace(uuid_generate_v4()::text, '-', ''),
    symbol_guid        VARCHAR(100) NOT NULL,
    open_price         NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (open_price >= 0),
    close_price        NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (close_price >= 0),
    high_price         NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (high_price >= 0),
    low_price          NUMERIC(65, 18) NOT NULL DEFAULT 0 CHECK (low_price >= 0),
    volume             UINT256 NOT NULL,
    market_cap         UINT256 NOT NULL,
    is_active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP(0) DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_symbol_kline_guid ON symbol_kline (guid);
CREATE INDEX IF NOT EXISTS idx_symbol_kline_is_active ON symbol_kline (is_active);
CREATE INDEX IF NOT EXISTS idx_symbol_kline_created_at ON symbol_kline (created_at);