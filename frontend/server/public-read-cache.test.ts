import { describe, expect, it } from 'vitest'
import {
  PublicReadCache,
  isPublicMarketRead,
  publicReadCachePayload,
} from './public-read-cache'

describe('isPublicMarketRead', () => {
  it('allows only versioned public market read envelopes', () => {
    expect(isPublicMarketRead('POST', '/api/v1/get_system_overview')).toBe(true)
    expect(isPublicMarketRead('POST', '/api/v2/get_asset_dashboard')).toBe(true)
    expect(isPublicMarketRead('GET', '/api/v2/get_asset_dashboard')).toBe(false)
    expect(isPublicMarketRead('POST', '/api/v1/trading/balances')).toBe(false)
    expect(isPublicMarketRead('POST', '/api/v1/trading/fund')).toBe(false)
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
})
