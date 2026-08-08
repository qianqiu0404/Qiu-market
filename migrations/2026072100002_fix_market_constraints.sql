-- 24-hour market changes can be negative, so radio must not have a
-- non-negative check. Keep this migration idempotent for existing databases.
ALTER TABLE exchange_symbol
    DROP CONSTRAINT IF EXISTS exchange_symbol_radio_check;

ALTER TABLE symbol_market
    DROP CONSTRAINT IF EXISTS symbol_market_radio_check;

DO
$$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'currency_sell_spread_nonnegative'
          AND conrelid = 'currency'::regclass
    ) THEN
        ALTER TABLE currency
            ADD CONSTRAINT currency_sell_spread_nonnegative CHECK (sell_spread >= 0);
    END IF;
END
$$;
