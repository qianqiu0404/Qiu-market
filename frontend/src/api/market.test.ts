import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  getAssetDashboardV2,
  getAssetVenuesV2,
  getMarketDashboard,
  getTop50VenueInsights,
} from './market'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('getAssetVenuesV2', () => {
  it('passes the selected provider so the drawer does not mix venues', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      expect(JSON.parse(String(init?.body))).toMatchObject({
        asset_id: 'asset-btc',
        venue: 'coinbase',
      })
      return new Response(JSON.stringify({ code: 2000, result: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    await getAssetVenuesV2('asset-btc', 'coinbase')
    expect(fetchMock).toHaveBeenCalledOnce()
  })
})

describe('getAssetDashboardV2', () => {
  it('preserves nullable composite fields and does not inflate the response with markets', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      expect(JSON.parse(String(init?.body))).toMatchObject({ include_uncovered: false, venue: 'binance' })
      return new Response(JSON.stringify({
      code: 2000,
      result: [{
        rank: 1,
        asset_id: 'asset-btc',
        asset_symbol: 'BTC',
        composite_price_usd: { value: '65705.12', available: true },
        change_24h_pct: { value: null, available: false },
        spot_market_count: 4,
        perp_market_count: 1,
        confidence: 'high',
        coverage_reason: 'missing_24h_reference',
      }],
      total: 1,
      }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await getAssetDashboardV2(1, 20, {
      venue: 'binance',
      includeUncovered: false,
    })
    expect(result.items[0]?.composite_price_usd.value).toBe(65705.12)
    expect(result.items[0]?.change_24h_pct).toEqual({ value: null, available: false })
    expect(result.items[0]?.coverage_reason).toBe('missing_24h_reference')
    expect(result.items[0]).not.toHaveProperty('markets')
  })
})

describe('getTop50VenueInsights', () => {
  it('uses the CEX union for All and stable provider selections for every venue', async () => {
    const bodies: Array<Record<string, unknown>> = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      bodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>)
      return new Response(JSON.stringify({ code: 2000, result: [], total: 0 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }))

    await getTop50VenueInsights()

    expect(bodies).toHaveLength(8)
    expect(Object.fromEntries(bodies.map((body) => [body.venue, body.universe]))).toEqual({
      all: 'provider_union',
      binance: 'provider_top50',
      coinbase: 'provider_top50',
      bybit: 'provider_top50',
      okx: 'provider_top50',
      hyperliquid: 'provider_top50',
      uniswap: 'provider_top50',
      pancakeswap: 'provider_top50',
    })
  })
})

describe('getMarketDashboard', () => {
  it('keeps a missing change percentage unavailable instead of inventing 0%', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      code: 2000,
      result: [{
        market_id: 'm1',
        symbol: 'BTC/USDT',
        change24h: '',
        change_available: false,
        freshness_status: 'Unknown',
      }],
      total: 1,
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })))

    const result = await getMarketDashboard()
    expect(result.items[0]?.change_available).toBe(false)
    expect(result.items[0]?.freshness_status).toBe('Unknown')
  })
})
