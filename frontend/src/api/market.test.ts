import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  getAssetDashboardV2,
  getAssetVenuesV2,
  getMarketDashboard,
	getMarketOverviewV2,
  getMarketPriceTicks,
  getTop50VenueInsights,
  isMarketPriceFactMonotonic,
  marketTickCacheKey,
  mergeMarketPriceTickSnapshot,
  unavailableMarketPriceFact,
  validatedDexRoutePriceFact,
  validatedDisplayReferencePriceFact,
  type MarketPriceFact,
  type MarketPriceTick,
  type MarketVenue,
} from './market'

const available = (value: string) => ({ value, available: true })
const SNAPSHOT_META = {
	snapshot_id: 'snp_00000000000000000000000000000001',
	snapshot_as_of: 1785200000000,
	snapshot_schema: 'qiu.market-snapshot.v1',
	overview: {
		asset_count: 1,
		priced_asset_count: 1,
		displayed_asset_count: 1,
		fresh_asset_count: 1,
		stale_asset_count: 0,
		unavailable_asset_count: 0,
	},
}
const priceFact = (
  value: string,
  kind: string,
  source: string,
  contributors: string[],
  observedAt = Date.now(),
) => ({
  price_usd: available(value),
  change_24h_pct: available('1.25'),
  turnover_24h_usd: available('900000000'),
  available: true,
  kind,
  source,
  source_time: observedAt - 1_000,
  observed_at: observedAt,
  last_success_at: observedAt,
  freshness_status: 'fresh',
  freshness_age_seconds: 1,
  quality: contributors.length >= 3 ? 'high' : 'low',
  contributor_count: contributors.length,
  contributors,
  version: 101,
})
const typedPriceFact = (
  value: string,
  kind: string,
  source: string,
  contributors: string[],
): MarketPriceFact => {
  const raw = priceFact(value, kind, source, contributors)
  return {
    ...raw,
    price_usd: { value: Number(raw.price_usd.value), available: true },
    change_24h_pct: { value: Number(raw.change_24h_pct.value), available: true },
    turnover_24h_usd: {
      value: Number(raw.turnover_24h_usd.value),
      available: true,
    },
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('getMarketOverviewV2 snapshot conservation', () => {
	it('maps fresh stale and unavailable counts from one frozen snapshot', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
			...SNAPSHOT_META,
			code: 2000,
			result: {
				venue: 'all', asset_count: 106, priced_asset_count: 61,
				displayed_asset_count: 61, unpriced_asset_count: 45,
				fresh_asset_count: 40, stale_asset_count: 21, unavailable_asset_count: 45,
			},
		}), { status: 200, headers: { 'Content-Type': 'application/json' } })))
		const overview = await getMarketOverviewV2('all')
		expect(overview).toMatchObject({
			snapshot_id: SNAPSHOT_META.snapshot_id,
			asset_count: 106, priced_asset_count: 61,
			fresh_asset_count: 40, stale_asset_count: 21, unavailable_asset_count: 45,
		})
	})

	it('rejects a non-conserving snapshot instead of rendering mixed counts', async () => {
		vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
			...SNAPSHOT_META,
			code: 2000,
			result: {
				venue: 'all', asset_count: 106, priced_asset_count: 61,
				displayed_asset_count: 61, fresh_asset_count: 40,
				stale_asset_count: 20, unavailable_asset_count: 45,
			},
		}), { status: 200, headers: { 'Content-Type': 'application/json' } })))
		await expect(getMarketOverviewV2('all')).rejects.toThrow('do not conserve')
	})
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
			...SNAPSHOT_META,
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
		expect(result.overview).toMatchObject({
			snapshot_id: SNAPSHOT_META.snapshot_id,
			venue: 'binance',
			asset_count: 1,
			fresh_asset_count: 1,
		})
  })

  it('never falls back to five-minute raw route fields for a DEX display', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({
		...SNAPSHOT_META,
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
    expect(row?.dex_route_price).toMatchObject({
      available: false,
      kind: 'unavailable',
      source: '',
      change_24h_pct: { value: null, available: false },
    })
    expect(row?.display_price).toMatchObject({
      available: false,
      kind: 'unavailable',
      source: '',
    })
  })

  it('keeps an explicit legacy DEX reference out of the route lane', async () => {
    const observedAt = Date.now() - 2_000
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({
		...SNAPSHOT_META,
        code: 2000,
        result: [{
          asset_id: 'asset-reference-only',
          asset_symbol: 'REF',
          price_usd: available('42'),
          change_24h_pct: available('9.5'),
          price_kind: 'dex_route',
          price_source: 'uniswap',
          available: true,
          freshness_status: 'stale',
          freshness_age_seconds: 90,
          dex_route_available: false,
          display_price_usd: available('41.75'),
          display_price_kind: 'market_reference',
          display_change_24h_pct: { value: null, available: false },
          display_available: true,
          display_observed_at: observedAt,
          provider_updated_at: observedAt - 1_000,
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

    expect(row?.dex_route_price).toMatchObject({
      available: false,
      kind: 'unavailable',
      source: '',
    })
    expect(row?.display_price).toMatchObject({
      available: true,
      kind: 'market_reference',
      source: 'coingecko',
      price_usd: { value: 41.75, available: true },
      change_24h_pct: { value: null, available: false },
    })
  })

  it('maps venue, DEX route, and display reference facts without merging provenance', async () => {
    vi.stubGlobal('fetch', vi.fn(async () =>
      new Response(JSON.stringify({
		...SNAPSHOT_META,
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

describe('DEX price fact lane validation', () => {
  it('expires route semantics at 60 seconds and never accepts a route as reference', () => {
    const now = Date.now()
    const expiredRoute: MarketPriceFact = {
      ...typedPriceFact('64000', 'dex_route', 'uniswap', ['uniswap']),
      change_24h_pct: { value: 9.5, available: true },
      observed_at: now - 61_000,
      last_success_at: now - 61_000,
      freshness_status: 'stale',
      freshness_age_seconds: 61,
    }

    expect(validatedDexRoutePriceFact(expiredRoute, 'uniswap', now)).toEqual(
      unavailableMarketPriceFact(),
    )
    expect(validatedDisplayReferencePriceFact(expiredRoute, now)).toEqual(
      unavailableMarketPriceFact(),
    )
  })

  it('keeps a readable route and composite reference as distinct facts', () => {
    const now = Date.now()
    const route: MarketPriceFact = {
      ...typedPriceFact('64211.10', 'dex_route', 'uniswap', ['uniswap']),
      observed_at: now - 45_000,
      last_success_at: now - 45_000,
      freshness_status: 'fresh',
      freshness_age_seconds: 1,
    }
    const reference = typedPriceFact(
      '64203.13',
      'composite_reference',
      'cex_composite',
      ['binance', 'coinbase'],
    )

    expect(validatedDexRoutePriceFact(route, 'uniswap', now)).toMatchObject({
      available: true,
      kind: 'dex_route',
      source: 'uniswap',
      freshness_status: 'stale',
      freshness_age_seconds: 45,
    })
    expect(validatedDisplayReferencePriceFact(reference, now)).toMatchObject({
      available: true,
      kind: 'composite_reference',
      source: 'cex_composite',
    })
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

describe('market tick identity and monotonic merge', () => {
  const tick = (
    venue: MarketVenue,
    assetID: string,
    fact: MarketPriceFact,
  ): MarketPriceTick => ({
    asset_id: assetID,
    provider: venue,
    price_kind: venue === 'all' ? 'composite_spot' : 'venue_spot',
    price_usd: fact.price_usd,
    change_24h_pct: fact.change_24h_pct,
    turnover_24h_usd: fact.turnover_24h_usd,
    venue_price: fact,
    dex_route_price: unavailableMarketPriceFact(),
    display_price: venue === 'all' ? fact : unavailableMarketPriceFact(),
    available: fact.available,
    freshness_status: fact.freshness_status,
    freshness_age_seconds: fact.freshness_age_seconds,
    source_time: fact.source_time,
    observed_at: fact.observed_at,
    last_success_at: fact.last_success_at,
    version: fact.version,
  })

  it('keeps identical prices isolated by real CEX identity', () => {
    let lastGood = new Map<string, MarketPriceFact>()
    for (const venue of ['binance', 'coinbase', 'bybit', 'okx'] as const) {
      const fact = typedPriceFact('64200.00', 'venue_spot', venue, [venue])
      const merged = mergeMarketPriceTickSnapshot(lastGood, {
        venue,
        server_time: 1785400003000,
        items: [tick(venue, 'asset-btc', fact)],
      }, venue, ['asset-btc'])
      lastGood = merged.lastGood
      expect(merged.states.get('asset-btc')?.status).toBe('live')
    }

    expect([...lastGood.entries()].map(([key, fact]) => [key, fact.source])).toEqual([
      [marketTickCacheKey('binance', 'asset-btc'), 'binance'],
      [marketTickCacheKey('coinbase', 'asset-btc'), 'coinbase'],
      [marketTickCacheKey('bybit', 'asset-btc'), 'bybit'],
      [marketTickCacheKey('okx', 'asset-btc'), 'okx'],
    ])
  })

  it('rejects a same-price fact when its source belongs to another CEX', () => {
    const binance = typedPriceFact(
      '64200.00',
      'venue_spot',
      'binance',
      ['binance'],
    )
    const disguisedCoinbase = typedPriceFact(
      '64200.00',
      'venue_spot',
      'coinbase',
      ['coinbase'],
    )
    const cacheKey = marketTickCacheKey('binance', 'asset-btc')
    const merged = mergeMarketPriceTickSnapshot(new Map([[cacheKey, binance]]), {
      venue: 'binance',
      server_time: disguisedCoinbase.observed_at,
      items: [tick('binance', 'asset-btc', disguisedCoinbase)],
    }, 'binance', ['asset-btc'])

    expect(merged.states.get('asset-btc')).toMatchObject({
      status: 'source_mismatch',
      fact: binance,
    })
    expect(merged.lastGood.get(cacheKey)).toEqual(binance)
  })

  it('accepts a same-version same-price refresh with a newer observation time', () => {
    const previous = typedPriceFact('64200', 'venue_spot', 'binance', ['binance'])
    const next = {
      ...previous,
      observed_at: previous.observed_at + 3_000,
      last_success_at: previous.last_success_at + 3_000,
      source_time: previous.source_time + 3_000,
      freshness_age_seconds: 0,
    }

    expect(isMarketPriceFactMonotonic(previous, next)).toBe(true)
    const merged = mergeMarketPriceTickSnapshot(new Map([
      [marketTickCacheKey('binance', 'asset-btc'), previous],
    ]), {
      venue: 'binance',
      server_time: next.observed_at,
      items: [tick('binance', 'asset-btc', next)],
    }, 'binance', ['asset-btc'])

    expect(merged.states.get('asset-btc')?.status).toBe('live')
    expect(merged.lastGood.get(marketTickCacheKey('binance', 'asset-btc'))?.observed_at)
      .toBe(next.observed_at)
  })

  it('rejects lower-version or older cached facts and preserves last-good', () => {
    const previous = {
      ...typedPriceFact('64210', 'venue_spot', 'bybit', ['bybit']),
      version: 12,
    }
    const cached = {
      ...typedPriceFact('64100', 'venue_spot', 'bybit', ['bybit']),
      version: 11,
      observed_at: previous.observed_at - 5_000,
      last_success_at: previous.last_success_at - 5_000,
    }
    const cacheKey = marketTickCacheKey('bybit', 'asset-btc')
    const merged = mergeMarketPriceTickSnapshot(new Map([[cacheKey, previous]]), {
      venue: 'bybit',
      server_time: 1785400003000,
      items: [tick('bybit', 'asset-btc', cached)],
    }, 'bybit', ['asset-btc'])

    expect(merged.states.get('asset-btc')).toMatchObject({
      status: 'out_of_order',
      fact: previous,
    })
    expect(merged.lastGood.get(cacheKey)).toEqual(previous)
  })

  it('preserves only the affected asset last-good when a venue is partially offline', () => {
    const btc = typedPriceFact('64210', 'venue_spot', 'okx', ['okx'])
    const eth = typedPriceFact('3200', 'venue_spot', 'okx', ['okx'])
    const previous = new Map([
      [marketTickCacheKey('okx', 'asset-btc'), btc],
      [marketTickCacheKey('okx', 'asset-eth'), eth],
    ])
    const newerBTC = { ...btc, observed_at: btc.observed_at + 3_000 }
    const merged = mergeMarketPriceTickSnapshot(previous, {
      venue: 'okx',
      server_time: 1785400005000,
      items: [
        tick('okx', 'asset-btc', newerBTC),
        tick('okx', 'asset-eth', unavailableMarketPriceFact()),
      ],
    }, 'okx', ['asset-btc', 'asset-eth'])

    expect(merged.states.get('asset-btc')?.status).toBe('live')
    expect(merged.states.get('asset-eth')).toMatchObject({
      status: 'unavailable',
      fact: eth,
    })
    expect(merged.lastGood.get(marketTickCacheKey('okx', 'asset-eth'))).toEqual(eth)
  })

  it('rejects a composite-labeled fact on a CEX venue', () => {
    const composite = typedPriceFact(
      '64203',
      'composite_reference',
      'cex_composite',
      ['binance', 'coinbase'],
    )
    const merged = mergeMarketPriceTickSnapshot(new Map(), {
      venue: 'coinbase',
      server_time: 1785400003000,
      items: [tick('coinbase', 'asset-btc', composite)],
    }, 'coinbase', ['asset-btc'])

    expect(merged.states.get('asset-btc')?.status).toBe('source_mismatch')
    expect(merged.lastGood.size).toBe(0)
  })
})

describe('getTop50VenueInsights', () => {
  it('uses the CEX union for All and stable provider selections for every venue', async () => {
    const bodies: Array<Record<string, unknown>> = []
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      bodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>)
		return new Response(JSON.stringify({ ...SNAPSHOT_META, code: 2000, result: [], total: 0 }), {
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
		...SNAPSHOT_META,
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
              dex_route_price: unavailableMarketPriceFact(),
              display_price: priceFact(
                '9.9',
                'composite_reference',
                'cex_composite',
                ['binance', 'coinbase'],
              ),
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
              dex_route_price: {
                ...priceFact('20', 'dex_route', 'uniswap', ['uniswap']),
                quality: 'high',
              },
              display_price: unavailableMarketPriceFact(),
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
              dex_route_price: unavailableMarketPriceFact(),
              display_price: unavailableMarketPriceFact(),
            }]
          : []
      return new Response(JSON.stringify({
		...SNAPSHOT_META,
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
