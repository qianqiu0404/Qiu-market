-- Clean existing data first
DELETE FROM symbol_market;
DELETE FROM exchange_symbol;
DELETE FROM symbol;
DELETE FROM exchange;
DELETE FROM asset;

-- Insert Assets (7)
INSERT INTO asset (guid, asset_name, asset_symbol, asset_logo) VALUES
('a1', 'Bitcoin', 'BTC', 'https://cryptologos.cc/logos/bitcoin-btc-logo.png'),
('a2', 'Ethereum', 'ETH', 'https://cryptologos.cc/logos/ethereum-eth-logo.png'),
('a3', 'Tether', 'USDT', 'https://cryptologos.cc/logos/tether-usdt-logo.png'),
('a4', 'Solana', 'SOL', 'https://cryptologos.cc/logos/solana-sol-logo.png'),
('a5', 'BNB', 'BNB', 'https://cryptologos.cc/logos/bnb-bnb-logo.png'),
('a6', 'XRP', 'XRP', 'https://cryptologos.cc/logos/xrp-xrp-logo.png'),
('a7', 'Dogecoin', 'DOGE', 'https://cryptologos.cc/logos/dogecoin-doge-logo.png')
ON CONFLICT (guid) DO UPDATE SET
    asset_name = EXCLUDED.asset_name,
    asset_symbol = EXCLUDED.asset_symbol,
    asset_logo = EXCLUDED.asset_logo;

-- Insert Exchanges (3)
INSERT INTO exchange (guid, name) VALUES
('e1', 'Binance'),
('e2', 'OKX'),
('e3', 'Bybit')
ON CONFLICT (guid) DO UPDATE SET
    name = EXCLUDED.name;

-- Insert Symbols (6)
INSERT INTO symbol (guid, base_asset_guid, qoute_asset_guid, symbol_name) VALUES
('s1', 'a1', 'a3', 'BTC/USDT'),
('s2', 'a2', 'a3', 'ETH/USDT'),
('s3', 'a4', 'a3', 'SOL/USDT'),
('s4', 'a5', 'a3', 'BNB/USDT'),
('s5', 'a6', 'a3', 'XRP/USDT'),
('s6', 'a7', 'a3', 'DOGE/USDT')
ON CONFLICT (guid) DO UPDATE SET
    base_asset_guid = EXCLUDED.base_asset_guid,
    qoute_asset_guid = EXCLUDED.qoute_asset_guid,
    symbol_name = EXCLUDED.symbol_name;

-- Link Binance to all demo symbols so worker can read Redis ticker keys and write market rows.
INSERT INTO exchange_symbol (guid, exchange_guid, symbol_guid, volume) VALUES
('es1', 'e1', 's1', 0),
('es2', 'e1', 's2', 0),
('es3', 'e1', 's3', 0),
('es4', 'e1', 's4', 0),
('es5', 'e1', 's5', 0),
('es6', 'e1', 's6', 0)
ON CONFLICT (guid) DO UPDATE SET
    exchange_guid = EXCLUDED.exchange_guid,
    symbol_guid = EXCLUDED.symbol_guid;

-- Insert Market Data (6)
INSERT INTO symbol_market (guid, symbol_guid, price, volume, market_cap, radio) VALUES
('m1', 's1', '7700000000000', 100000000000, 7700000000000000, 1.68),
('m2', 's2', '230000000000', 500000000000, 1150000000000000, 2.21),
('m3', 's3', '8494000000', 2000000000000, 169880000000000, 1.39),
('m4', 's4', '58050000000', 800000000000, 464400000000000, 0.86),
('m5', 's5', '62000000', 50000000000000, 31000000000000, 1.25),
('m6', 's6', '16000000', 100000000000000, 16000000000000, 2.34)
ON CONFLICT (guid) DO UPDATE SET
    price = EXCLUDED.price,
    volume = EXCLUDED.volume,
    market_cap = EXCLUDED.market_cap,
    radio = EXCLUDED.radio;
