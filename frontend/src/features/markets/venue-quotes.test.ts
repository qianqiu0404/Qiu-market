import { describe, expect, it } from 'vitest'
import type { AssetMarketV2Item } from '../../api/market'
import { buildVenueQuoteRows, hasValidDexRouteIdentity } from './venue-quotes'

function market(overrides: Partial<AssetMarketV2Item>): AssetMarketV2Item {
  return {
    market_id: 'market-btc', market_code: 'provider:BTC/USD:spot', provider: 'coinbase',
    symbol: 'BTC/USD', market_type: 'spot', quote_asset: 'USD',
    price: { value: 64_000, available: true }, relative_deviation_pct: { value: 0, available: true },
    change_24h_pct: { value: 1, available: true }, turnover_24h: { value: 10, available: true },
    freshness_status: 'Healthy', provider_updated_at: Date.now(),
    confidence: 'medium', quality: 'medium', has_kline: true, venue_kind: 'cex',
    chain: '', protocol: '', route_key: '', route: [], pool_addresses: [],
    quote_notional_usd: { value: null, available: false }, quote_reference_kind: '',
    tvl_usd: { value: null, available: false }, price_impact_pct: { value: null, available: false },
    round_trip_spread_pct: { value: null, available: false }, block_number: 0,
    block_timestamp: Date.now(), available: true, unavailable_reason: '', ...overrides,
  }
}

describe('buildVenueQuoteRows', () => {
  it('always returns the seven provider rows and marks unpublished venues unavailable', () => {
    const rows = buildVenueQuoteRows([market({})])
    expect(rows.map((row) => row.label)).toEqual([
      'Binance', 'Coinbase', 'Bybit', 'OKX', 'Hyperliquid', 'Uniswap', 'PancakeSwap',
    ])
    expect(rows.find((row) => row.provider === 'coinbase')?.status).toBe('live')
    expect(rows.find((row) => row.provider === 'binance')).toMatchObject({
      status: 'unavailable', reason: 'Unavailable in the current deployment',
    })
    expect(rows.find((row) => row.provider === 'uniswap')).toMatchObject({
      product: 'DEX Route', status: 'unavailable',
      reason: 'No reviewed route in the current public preview',
    })
  })

  it('prefers a fresh included quote and keeps stale pricing visibly stale', () => {
    const rows = buildVenueQuoteRows([
      market({ provider: 'okx', symbol: 'BTC/USDT', quote_asset: 'USDT', confidence: 'excluded' }),
      market({ provider: 'okx', symbol: 'BTC/USDC', quote_asset: 'USDC', confidence: 'medium' }),
      market({ provider: 'coinbase', freshness_status: 'Stale' }),
    ])
    expect(rows.find((row) => row.provider === 'okx')?.market?.symbol).toBe('BTC/USDC')
    expect(rows.find((row) => row.provider === 'coinbase')?.status).toBe('stale')
  })

  it('keeps Hyperliquid explicitly separate as a perpetual quote', () => {
    const rows = buildVenueQuoteRows([
      market({ provider: 'hyperliquid', market_type: 'perp', confidence: 'excluded_perp' }),
    ])
    expect(rows.find((row) => row.provider === 'hyperliquid')).toMatchObject({
      product: 'Perpetual', status: 'live',
    })
  })

  it('selects the best fresh DEX route without treating a rejected route as a quote', () => {
    const rows = buildVenueQuoteRows([
      market({
        provider: 'uniswap', market_type: 'dex_route', venue_kind: 'dex_route',
        freshness_status: 'Stale', quality: 'high', chain: 'Ethereum', protocol: 'V3',
        route_key: 'wbtc-usdc-v3', route: ['WBTC', 'USDC'],
        pool_addresses: ['0x0000000000000000000000000000000000000001'], block_number: 1,
        quote_notional_usd: { value: 10_000, available: true },
      }),
      market({
        provider: 'uniswap', market_type: 'dex_route', venue_kind: 'dex_route',
        freshness_status: 'Healthy', quality: 'medium', chain: 'Ethereum', protocol: 'V2',
        route_key: 'wbtc-usdc-v2', route: ['WBTC', 'USDC'],
        pool_addresses: ['0x0000000000000000000000000000000000000002'], block_number: 2,
        quote_notional_usd: { value: 1_000, available: true },
      }),
      market({
        provider: 'pancakeswap', market_type: 'dex_route', venue_kind: 'dex_route',
        chain: 'BNB Chain', protocol: 'V3', route_key: 'btcb-usdt',
        route: ['BTCB', 'USDT'], pool_addresses: ['0x0000000000000000000000000000000000000003'],
        block_number: 3, available: false, unavailable_reason: 'round_trip_spread_exceeded',
      }),
    ])
    expect(rows.find((row) => row.provider === 'uniswap')?.market?.quote_notional_usd.value).toBe(1_000)
    expect(rows.find((row) => row.provider === 'uniswap')?.status).toBe('live')
    expect(rows.find((row) => row.provider === 'pancakeswap')).toMatchObject({
      status: 'unavailable', reason: 'round_trip_spread_exceeded',
    })
  })

  it.each([
    { chain: 'BNB Chain' },
    { protocol: '' },
    { route_key: '' },
    { route: ['WBTC'] },
    { pool_addresses: ['not-an-address'] },
    { block_number: 0 },
    { block_timestamp: Date.now() - 61_000 },
  ])('fails a malformed or stale DEX identity closed: %o', (invalid) => {
    const candidate = market({
      provider: 'uniswap', market_type: 'dex_route', venue_kind: 'dex_route',
      chain: 'Ethereum', protocol: 'V3', route_key: 'wbtc-usdc', route: ['WBTC', 'USDC'],
      pool_addresses: ['0x0000000000000000000000000000000000000001'], block_number: 1,
      ...invalid,
    })
    expect(hasValidDexRouteIdentity(candidate)).toBe(false)
    const rows = buildVenueQuoteRows([candidate])
    expect(rows.find((row) => row.provider === 'uniswap')).toMatchObject({
      status: 'unavailable', reason: 'Route identity or block freshness is not verified',
    })
  })
})
