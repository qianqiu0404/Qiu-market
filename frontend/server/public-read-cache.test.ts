import { describe, expect, it } from 'vitest'
import {
  PublicReadCache,
  RuntimePublicReadCache,
  agePublicReadBody,
  isPublicMarketRead,
  publicReadCachePayload,
} from './public-read-cache'
import type { RuntimeCache } from '@vercel/functions'

describe('isPublicMarketRead', () => {
  it('allows only versioned public market read envelopes', () => {
    expect(isPublicMarketRead('POST', '/api/v1/get_system_overview')).toBe(true)
    expect(isPublicMarketRead('POST', '/api/v2/get_asset_dashboard')).toBe(true)
    expect(isPublicMarketRead('GET', '/api/v2/get_asset_dashboard')).toBe(false)
    expect(isPublicMarketRead('POST', '/api/v1/trading/balances')).toBe(false)
    expect(isPublicMarketRead('POST', '/api/v1/trading/fund')).toBe(false)
    expect(isPublicMarketRead('POST', '/api/v1/get_balances')).toBe(false)
    expect(isPublicMarketRead('POST', '/api/v2/get_future_private_data')).toBe(false)
  })
})

describe('PublicReadCache', () => {
  it('transitions from fresh to stale and then expires', () => {
    const cache = new PublicReadCache(15_000, 300_000)
    cache.put('market', {
      status: 200,
      body: Buffer.from('{"code":2000}'),
      contentType: 'application/json',
      storedAt: 1_000,
    })

    expect(cache.lookup('market', 16_000)?.state).toBe('fresh')
    expect(cache.lookup('market', 16_001)?.state).toBe('stale')
    expect(cache.lookup('market', 316_000)?.state).toBe('stale')
    expect(cache.lookup('market', 316_001)).toBeUndefined()
  })

  it('evicts the oldest entry when bounded capacity is exceeded', () => {
    const cache = new PublicReadCache(15_000, 300_000, 2)
    for (const key of ['one', 'two', 'three']) {
      cache.put(key, {
        status: 200,
        body: Buffer.from(key),
        storedAt: 1_000,
      })
    }

    expect(cache.lookup('one', 1_000)).toBeUndefined()
    expect(cache.lookup('two', 1_000)?.entry.body.toString()).toBe('two')
    expect(cache.lookup('three', 1_000)?.entry.body.toString()).toBe('three')
  })

  it('bounds total bytes and rejects oversized entries', () => {
    const cache = new PublicReadCache(15_000, 300_000, 10, 6, 4)
    expect(
      cache.put('one', {
        status: 200,
        body: Buffer.from('1111'),
        storedAt: 1_000,
      }),
    ).toBe(true)
    expect(
      cache.put('two', {
        status: 200,
        body: Buffer.from('22'),
        storedAt: 1_000,
      }),
    ).toBe(true)
    expect(
      cache.put('three', {
        status: 200,
        body: Buffer.from('333'),
        storedAt: 1_000,
      }),
    ).toBe(true)

    expect(cache.lookup('one', 1_000)).toBeUndefined()
    expect(cache.lookup('two', 1_000)?.entry.body.toString()).toBe('22')
    expect(cache.lookup('three', 1_000)?.entry.body.toString()).toBe('333')
    expect(
      cache.put('oversized', {
        status: 200,
        body: Buffer.from('12345'),
        storedAt: 1_000,
      }),
    ).toBe(false)
  })
})

describe('agePublicReadBody', () => {
  it('expires a cached DEX route into an explicit composite reference', () => {
    const aged = agePublicReadBody(
      '/api/v2/get_asset_dashboard',
      Buffer.from(JSON.stringify({
        code: 2000,
        result: [{
          asset_symbol: 'BTC',
          price_usd: { value: '65000', available: true },
          change_24h_pct: { value: '1.25', available: true },
          composite_price_usd: { value: '64950', available: true },
          market_reference_price_usd: { value: '64900', available: true },
          display_price_usd: { value: '65000', available: true },
          display_price_kind: 'dex_route',
          display_observed_at: 1_785_200_000,
          display_change_24h_pct: { value: '1.25', available: true },
          display_change_kind: 'dex_route',
          display_available: true,
          dex_route_available: true,
          dex_route_count: 3,
          price_kind: 'dex_route',
          price_source: 'uniswap',
          available: true,
          freshness_status: 'fresh',
          freshness_age_seconds: 20,
        }],
      })),
      45,
    )
    const row = JSON.parse(aged.toString()).result[0]

    expect(row.freshness_age_seconds).toBe(65)
    expect(row.freshness_status).toBe('stale')
    expect(row.dex_route_available).toBe(false)
    expect(row.dex_route_count).toBe(0)
    expect(row.price_usd.available).toBe(false)
    expect(row.display_price_usd).toEqual({
      value: '64950',
      available: true,
    })
    expect(row.display_price_kind).toBe('composite_reference')
    expect(row.display_available).toBe(true)
    expect(row.display_observed_at).toBe(0)
    expect(row.display_change_24h_pct).toEqual({
      value: null,
      available: false,
    })
    expect(row.display_change_kind).toBe('unavailable')
    expect(row.price_kind).toBe('unavailable')
    expect(row.price_source).toBe('')
    expect(row.change_24h_pct).toEqual({
      value: '1.25',
      available: false,
    })
    expect(row.coverage_status).toBe('reference_only')
    expect(row.coverage_reason).toBe('cached_route_expired')
  })

  it('does not claim reference-only coverage when an expired route has no fallback', () => {
    const aged = agePublicReadBody(
      '/api/v2/get_asset_dashboard',
      Buffer.from(JSON.stringify({
        result: [{
          price_usd: { value: '65000', available: true },
          composite_price_usd: { value: null, available: false },
          market_reference_price_usd: { value: null, available: false },
          display_price_usd: { value: '65000', available: true },
          display_price_kind: 'dex_route',
          display_available: true,
          dex_route_available: true,
          dex_route_count: 1,
          available: true,
          freshness_status: 'fresh',
          freshness_age_seconds: 55,
        }],
      })),
      10,
    )
    const row = JSON.parse(aged.toString()).result[0]

    expect(row.dex_route_available).toBe(false)
    expect(row.display_price_usd).toEqual({
      value: null,
      available: false,
    })
    expect(row.display_available).toBe(false)
    expect(row.coverage_status).toBe('source_unavailable')
    expect(row.coverage_reason).toBe('cached_route_expired')
  })

  it('keeps unavailable rows unavailable when source age is missing', () => {
    const aged = agePublicReadBody(
      '/api/v2/get_asset_dashboard',
      Buffer.from(JSON.stringify({
        result: [{
          available: false,
          freshness_status: 'unavailable',
          dex_route_available: false,
        }],
      })),
      31,
    )
    const row = JSON.parse(aged.toString()).result[0]

    expect(row.freshness_status).toBe('unavailable')
    expect(row.available).toBe(false)
    expect(row).not.toHaveProperty('freshness_age_seconds')
  })

  it('marks an expired cached quote unavailable without changing other endpoints', () => {
    const dashboard = Buffer.from(JSON.stringify({
      result: [{
        freshness_status: 'fresh',
        freshness_age_seconds: 10,
        available: true,
        dex_route_available: false,
      }],
    }))
    const row = JSON.parse(
      agePublicReadBody(
        '/api/v2/get_asset_dashboard',
        dashboard,
        300,
      ).toString(),
    ).result[0]

    expect(row.freshness_age_seconds).toBe(310)
    expect(row.freshness_status).toBe('unavailable')
    expect(row.available).toBe(false)
    expect(
      agePublicReadBody('/api/v1/get_system_overview', dashboard, 300),
    ).toBe(dashboard)
  })
})

describe('publicReadCachePayload', () => {
  it('ignores logging-only consumer tokens while preserving query identity', () => {
    const browser = Buffer.from(
      '{"consumer_token":"frontend-dashboard","venue":"pancakeswap","page":1}',
    )
    const observer = Buffer.from(
      '{"page":1,"consumer_token":"production-observer","venue":"pancakeswap"}',
    )
    const differentPage = Buffer.from(
      '{"consumer_token":"frontend-dashboard","venue":"pancakeswap","page":2}',
    )

    expect(publicReadCachePayload(browser)).toEqual(
      publicReadCachePayload(observer),
    )
    expect(publicReadCachePayload(browser)).not.toEqual(
      publicReadCachePayload(differentPage),
    )
  })

  it('does not remove nested fields that may affect query identity', () => {
    const first = Buffer.from(
      '{"filter":{"consumer_token":"asset-a"},"consumer_token":"frontend-dashboard"}',
    )
    const second = Buffer.from(
      '{"filter":{"consumer_token":"asset-b"},"consumer_token":"frontend-dashboard"}',
    )
    expect(publicReadCachePayload(first)).not.toEqual(
      publicReadCachePayload(second),
    )
  })
})

class FakeRuntimeCache implements RuntimeCache {
  readonly values = new Map<string, unknown>()
  lastTTL = 0

  async delete(key: string): Promise<void> {
    this.values.delete(key)
  }

  async get(key: string): Promise<unknown | null> {
    return this.values.get(key) ?? null
  }

  async set(
    key: string,
    value: unknown,
    options?: { ttl?: number },
  ): Promise<void> {
    this.values.set(key, value)
    this.lastTTL = options?.ttl ?? 0
  }

  async expireTag(): Promise<void> {}
}

describe('RuntimePublicReadCache', () => {
  it('shares a bounded serializable last-good entry with the configured TTL', async () => {
    const runtime = new FakeRuntimeCache()
    const cache = new RuntimePublicReadCache(runtime, 15_000, 300_000, 64)
    expect(
      await cache.put('market', {
        status: 200,
        body: Buffer.from('{"code":2000}'),
        contentType: 'application/json',
        storedAt: 1_000,
      }),
    ).toBe(true)

    expect(runtime.lastTTL).toBe(315)
    const lookup = await cache.lookup('market', 20_000)
    expect(lookup?.state).toBe('stale')
    expect(lookup?.entry.body.toString()).toBe('{"code":2000}')
  })

  it('rejects oversized shared entries', async () => {
    const runtime = new FakeRuntimeCache()
    const cache = new RuntimePublicReadCache(runtime, 15_000, 300_000, 4)
    expect(
      await cache.put('oversized', {
        status: 200,
        body: Buffer.from('12345'),
        storedAt: 1_000,
      }),
    ).toBe(false)
    expect(runtime.values.size).toBe(0)
  })
})
