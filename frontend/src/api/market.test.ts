import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  getAssetDashboardV2,
  getAssetVenuesV2,
  getMarketDashboard,
  getMarketPriceTicks,
  getTop50VenueInsights,
} from './market'

const available = (value: string) => ({ value, available: true })
const priceFact = (
  value: string,
  kind: string,
  source: string,
  contributors: string[],
) => ({
  price_usd: available(value),
  change_24h_pct: available('1.25'),
  turnover_24h_usd: available('900000000'),
  available: true,
  kind,
  source,
  source_time: 1785400001000,
  observed_at: 1785400002000,
  last_success_at: 1785400002000,
  freshness_status: 'fresh',
  freshness_age_seconds: 1,
  quality: contributors.length >= 3 ? 'high' : 'low',
  contributor_count: contributors.length,
  contributors,
  version: 101,
})

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
        display_price_usd: { value: '65705.12', available: true },
        display_price_kind: 'composite_reference',
        display_change_24h_pct: { value: '1.25', available: true },
        display_change_kind: 'composite_reference',
        display_available: true,
        dex_route_available: false,
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
    expect(result.items[0]?.display_price_kind).toBe('composite_reference')
    expect(result.items[0]?.display_price_usd.value).toBe(65705.12)
    expect(result.items[0]?.display_change_24h_pct.value).toBe(1.25)
    expect(result.items[0]?.dex_route_available).toBe(false)
    expect(result.items[0]?.coverage_reason).toBe('missing_24h_reference')
    expect(result.items[0]).not.toHaveProperty('markets')
  })

  it('never falls back to five-minute raw route fields for a DEX display', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({
        code: 2000,
        result: [{
          asset_id: 'asset-old-route',
          asset_symbol: 'OLD',
          price_usd: { value: '42', available: true },
          change_24h_pct: { value: '9.5', available: true },
          price_kind: 'dex_route',
          dex_route_available: false,
          dex_route_count: 0,
          available: true,
          freshness_status: 'stale',
          freshness_age_seconds: 90,
        }],
        total: 1,
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })))

    const result = await getAssetDashboardV2(1, 50, {
      venue: 'uniswap',
      universe: 'provider_top50',
    })
    const row = result.items[0]

    expect(row?.price_usd).toEqual({ value: 42, available: true })
    expect(row?.change_24h_pct).toEqual({ value: 9.5, available: true })
    expect(row?.display_price_usd).toEqual({ value: null, available: false })
    expect(row?.display_price_kind).toBe('unavailable')
    expect(row?.display_change_24h_pct).toEqual({
      value: null,
      available: false,
    })
    expect(row?.display_change_kind).toBe('unavailable')
    expect(row?.display_available).toBe(false)
  })

  it('maps venue, DEX route, and display reference facts without merging provenance', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({
        code: 2000,
        result: [{
          asset_id: 'asset-btc',
          asset_symbol: 'BTC',
          venue_price: priceFact('64213.56', 'venue_spot', 'binance', ['binance']),
          dex_route_price: priceFact('64211.10', 'dex_route', 'uniswap', ['uniswap']),
          display_price: priceFact(
            '64203.13',
            'composite_reference',
            'cex_composite',
            ['binance', 'coinbase', 'bybit', 'okx'],
          ),
        }],
        total: 1,
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })))

    const result = await getAssetDashboardV2(1, 50, {
      venue: 'uniswap',
      universe: 'provider_top50',
    })
    const row = result.items[0]

    expect(row?.venue_price).toMatchObject({
      source: 'binance',
      kind: 'venue_spot',
      contributors: ['binance'],
    })
    expect(row?.dex_route_price).toMatchObject({
      source: 'uniswap',
      kind: 'dex_route',
      price_usd: { value: 64211.1, available: true },
    })
    expect(row?.display_price).toMatchObject({
      source: 'cex_composite',
      kind: 'composite_reference',
      contributor_count: 4,
      contributors: ['binance', 'coinbase', 'bybit', 'okx'],
    })
    expect(row?.display_price.price_usd).not.toEqual(row?.dex_route_price.price_usd)
  })
})

describe('getMarketPriceTicks', () => {
  it('keeps the requested venue and maps decimal strings without losing version metadata', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      expect(JSON.parse(String(init?.body))).toMatchObject({
        venue: 'coinbase',
        asset_ids: ['asset-btc'],
      })
      return new Response(JSON.stringify({
        code: 2000,
        venue: 'coinbase',
        server_time: 1785400003000,
        result: [{
          asset_id: 'asset-btc',
          provider: 'coinbase',
          price_kind: 'venue_spot',
          price_usd: available('64224.23'),
          change_24h_pct: available('-0.19'),
          turnover_24h_usd: available('500000000'),
          available: true,
          freshness_status: 'fresh',
          freshness_age_seconds: 1,
          source_time: 1785400001000,
          observed_at: 1785400002000,
          last_success_at: 1785400002000,
          version: 101,
          venue_price: priceFact(
            '64224.23',
            'venue_spot',
            'coinbase',
            ['coinbase'],
          ),
        }],
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await getMarketPriceTicks('coinbase', [
      'asset-btc', 'asset-btc', '',
    ])

    expect(result.venue).toBe('coinbase')
    expect(result.server_time).toBe(1785400003000)
    expect(result.items).toEqual([expect.objectContaining({
      asset_id: 'asset-btc',
      provider: 'coinbase',
      price_kind: 'venue_spot',
      price_usd: { value: 64224.23, available: true },
      venue_price: expect.objectContaining({
        source: 'coinbase',
        kind: 'venue_spot',
        contributors: ['coinbase'],
      }),
      version: 101,
    })])
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

  it('bounds venue fanout and preserves successful providers when one fails', async () => {
    let active = 0
    let maximumActive = 0
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as { venue: string }
      active += 1
      maximumActive = Math.max(maximumActive, active)
      await new Promise((resolve) => setTimeout(resolve, 5))
      active -= 1
      if (body.venue === 'bybit') {
        return new Response(JSON.stringify({
          code: 5000,
          message: 'provider snapshot unavailable',
        }), {
          status: 504,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      return new Response(JSON.stringify({
        code: 2000,
        result: [{
          asset_id: `asset-${body.venue}`,
          asset_symbol: body.venue.toUpperCase(),
          available: true,
          price_usd: { value: '1', available: true },
        }],
        total: 1,
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }))

    const result = await getTop50VenueInsights()
    const bybit = result.coverage.find((row) => row.venue === 'bybit')
    const binance = result.coverage.find((row) => row.venue === 'binance')

    expect(maximumActive).toBeLessThanOrEqual(2)
    expect(bybit).toMatchObject({
      available: false,
      priced: 0,
      total: 0,
    })
    expect(bybit?.error).toContain('provider snapshot unavailable')
    expect(binance).toMatchObject({
      available: true,
      priced: 1,
      total: 1,
      coverage_pct: 100,
    })
  })

  it('publishes only fresh on-chain routes in the DEX route monitor', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as { venue: string }
      const result = body.venue === 'uniswap'
        ? [
            {
              asset_id: 'asset-reference-only',
              asset_symbol: 'REF',
              available: true,
              price_usd: { value: '10', available: true },
              display_price_usd: { value: '9.9', available: true },
              display_price_kind: 'composite_reference',
              display_available: true,
              dex_route_available: false,
              dex_route_count: 0,
              quality: 'medium',
            },
            {
              asset_id: 'asset-live-route',
              asset_symbol: 'LIVE',
              available: true,
              price_usd: { value: '20', available: true },
              display_price_usd: { value: '20', available: true },
              display_price_kind: 'dex_route',
              display_available: true,
              dex_route_available: true,
              dex_route_count: 2,
              quality: 'high',
            },
          ]
        : body.venue === 'pancakeswap'
          ? [{
              asset_id: 'asset-expired-route',
              asset_symbol: 'OLD',
              available: true,
              price_usd: { value: '30', available: true },
              display_price_usd: { value: null, available: false },
              display_price_kind: 'unavailable',
              display_available: false,
              dex_route_available: false,
              dex_route_count: 3,
              quality: 'stale',
            }]
          : []
      return new Response(JSON.stringify({
        code: 2000,
        result,
        total: result.length,
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }))

    const result = await getTop50VenueInsights()
    const uniswap = result.coverage.find((row) => row.venue === 'uniswap')
    const pancakeswap = result.coverage.find((row) => row.venue === 'pancakeswap')

    expect(result.dex_routes).toEqual([{
      asset_id: 'asset-live-route',
      asset_symbol: 'LIVE',
      provider: 'uniswap',
      price: 20,
      available: true,
      route_count: 2,
      quality: 'high',
    }])
    expect(uniswap).toMatchObject({
      priced: 2,
      total: 2,
      coverage_pct: 100,
      coverage_kind: 'displayed',
    })
    expect(pancakeswap).toMatchObject({
      priced: 0,
      total: 1,
      coverage_pct: 0,
      coverage_kind: 'displayed',
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
