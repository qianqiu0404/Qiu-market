import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

vi.mock('@vercel/functions', () => ({
  getCache: () => ({
    get: async () => null, set: async () => undefined, delete: async () => undefined,
    expireTag: async () => undefined,
  }),
  waitUntil: () => undefined,
}))

import handler from '../api/_default-dashboard'

const release = '19928325f9a1104d1dd3505a004dffb9fe52a714'

function request(query: Record<string, string | string[] | undefined> = {}) {
  return {
    method: 'GET', url: '/api/market/default-dashboard', query,
    headers: {
      host: 'evil.example', accept: 'text/html', 'user-agent': 'evil-agent',
      'x-request-id': 'attacker-controlled', authorization: 'Bearer forbidden',
      cookie: 'must-not-forward=true', 'x-csrf-token': 'must-not-forward',
    },
  }
}

function response() {
  const headers = new Map<string, string | number | readonly string[]>()
  const result = { status: 200, body: Buffer.alloc(0) }
  const value = {
    setHeader(name: string, header: string | number | readonly string[]) {
      headers.set(name.toLowerCase(), header); return value
    },
    removeHeader(name: string) { headers.delete(name.toLowerCase()) },
    status(code: number) { result.status = code; return value },
    send(body: Buffer) { result.body = Buffer.from(body) },
    json(body: unknown) { result.body = Buffer.from(JSON.stringify(body)) },
  }
  return { value, result, headers }
}

function dashboardBody() {
  return JSON.stringify({
    code: 2000, result: [], total: 0,
    overview: {
      venue: 'all', asset_count: 1, priced_asset_count: 0,
      fresh_asset_count: 0, stale_asset_count: 0, unavailable_asset_count: 1,
    },
    snapshot_id: 'snp_00000000000000000000000000000001',
    snapshot_as_of: 1785196800000,
    snapshot_schema: 'qiu.market-snapshot.v1',
  })
}

function contracted(body: string, init: RequestInit, overrides: Record<string, string> = {}) {
  const requestHeaders = new Headers(init.headers)
  return new Response(body, { status: 200, headers: {
    'Content-Type': 'application/json',
    'X-Qiu-Market-Backend-Release-Commit': release,
    'X-Qiu-Market-Data-Mode': 'live',
    'X-Qiu-Market-Provider-Policy': 'restricted-no-bypass.v1',
    'X-Qiu-Market-Contract-Schema': 'qiu.market-read-contract.v1',
    'X-Qiu-Market-Snapshot-Schema': 'qiu.market-snapshot.v1',
    'X-Qiu-Market-Edge-Release-Commit': release,
    'X-Qiu-Market-Edge-Data-Mode': 'live',
    'X-Qiu-Market-Edge-Contract-Schema': 'qiu.market-edge-contract.v1',
    'X-Qiu-Market-Backend-Request-Nonce': requestHeaders.get('X-Qiu-Market-Nonce') ?? '',
    ...overrides,
  } })
}

beforeEach(() => {
  process.env.S78_BACKEND_ORIGIN = 'https://backend.example'
  process.env.S78_PROXY_HMAC_SECRET = 'unit-test-secret'
  process.env.QIU_MARKET_RELEASE_COMMIT = release
  process.env.VERCEL_DEPLOYMENT_ID = 'dpl_PreviewFixture123'
  process.env.VERCEL_URL = 'qiu-market-preview.vercel.app'
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  for (const key of ['S78_BACKEND_ORIGIN', 'S78_PROXY_HMAC_SECRET',
    'QIU_MARKET_RELEASE_COMMIT', 'VERCEL_DEPLOYMENT_ID', 'VERCEL_URL']) {
    delete process.env[key]
  }
})

describe('default dashboard CDN GET', () => {
  it('routes the exact GET function before the generic API proxy without a self rewrite', () => {
    const config = JSON.parse(readFileSync(resolve(process.cwd(), 'vercel.json'), 'utf8'))
    expect(config.rewrites[0]).toEqual({
      source: '/api/market/default-dashboard',
      destination: '/api/_default-dashboard',
    })
    expect(config.rewrites[0].destination).not.toBe(config.rewrites[0].source)
    expect(config.rewrites[1].source).toBe('/api/:path*')
  })

  it('signs every origin miss with a fresh nonce and caches only verified 200', async () => {
    const fetchMock = vi.fn(async (_url: URL, init: RequestInit) => contracted(dashboardBody(), init))
    vi.stubGlobal('fetch', fetchMock)
    for (const filter of ['assets', 'gainers']) {
      const target = response()
      await handler(request({ venue: 'all', filter }) as never, target.value as never)
      expect(target.result.status).toBe(200)
      expect(target.headers.get('cache-control')).toBe('public, max-age=0, must-revalidate')
      expect(target.headers.get('vercel-cdn-cache-control')).toBe(
        'public, s-maxage=15, stale-while-revalidate=45',
      )
    }
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const headers = fetchMock.mock.calls.map(([, init]) => new Headers(init.headers))
    expect(new Set(headers.map((item) => item.get('x-qiu-market-nonce'))).size).toBe(2)
    expect(headers.every((item) => !item.has('cookie') && !item.has('x-csrf-token'))).toBe(true)
    expect(headers[0].get('accept')).toBe('application/json')
    expect(headers[0].get('user-agent')).toBe('qiu-market-default-dashboard')
    expect(headers[0].get('x-request-id')).not.toBe('attacker-controlled')
    expect(headers[0].has('authorization')).toBe(false)
    expect(fetchMock.mock.calls.map(([, init]) => JSON.parse(String(init.body)))).toEqual(
      ['assets', 'gainers'].map((filter) => expect.objectContaining({
        venue: 'all', filter, page: 1, page_size: 50, search: '',
        sort_by: 'rank', sort_direction: 'desc', include_uncovered: true,
        universe: 'provider_union', snapshot_id: '',
      })),
    )
  })

  it('rejects dynamic or unknown query input before signing', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    for (const query of [
      { venue: 'kraken' }, { venue: ['all', 'okx'] }, { search: 'btc' },
      { filter: 'unknown' }, { page: '2' },
    ]) {
      const target = response()
      await handler(request(query) as never, target.value as never)
      expect(target.result.status).toBe(400)
      expect(target.headers.get('cache-control')).toBe('no-store')
      expect(target.headers.has('vercel-cdn-cache-control')).toBe(false)
    }
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('does not CDN-cache a contract mismatch', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_url: URL, init: RequestInit) =>
      contracted(dashboardBody(), init, { 'X-Qiu-Market-Edge-Release-Commit': '0'.repeat(40) })))
    const target = response()
    await handler(request({ venue: 'binance' }) as never, target.value as never)
    expect(target.result.status).toBe(502)
    expect(target.headers.get('cache-control')).toBe('no-store')
    expect(target.headers.has('vercel-cdn-cache-control')).toBe(false)
  })

  it('serves runtime last-good as no-store so the CDN cannot compound staleness', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-12T00:00:00Z'))
    vi.stubGlobal('fetch', vi.fn(async (_url: URL, init: RequestInit) =>
      contracted(dashboardBody(), init)))
    await handler(request({ filter: 'losers' }) as never, response().value as never)
    vi.setSystemTime(new Date('2026-08-12T00:00:16Z'))
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('transport down')))
    const target = response()
    await handler(request({ filter: 'losers' }) as never, target.value as never)
    expect(target.result.status).toBe(200)
    expect(target.headers.get('x-qiu-market-cache')).toBe('STALE')
    expect(target.headers.get('cache-control')).toBe('no-store')
    expect(target.headers.has('vercel-cdn-cache-control')).toBe(false)
  })

  it('strips cookies and unsafe vary and refuses CDN caching', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_url: URL, init: RequestInit) =>
      contracted(dashboardBody(), init, { 'Set-Cookie': 'private=1', Vary: 'Cookie' })))
    const target = response()
    await handler(request({ venue: 'okx' }) as never, target.value as never)
    expect(target.result.status).toBe(200)
    expect(target.headers.has('set-cookie')).toBe(false)
    expect(target.headers.has('vary')).toBe(false)
    expect(target.headers.get('cache-control')).toBe('no-store')
    expect(target.headers.has('vercel-cdn-cache-control')).toBe(false)
  })

  it('accepts only a normalized single Accept-Encoding vary after inner decompression', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_url: URL, init: RequestInit) =>
      contracted(dashboardBody(), init, { Vary: '  AcCePt-EnCoDiNg  ' })))
    const target = response()
    await handler(request({ venue: 'coinbase', filter: 'losers' }) as never, target.value as never)
    expect(target.result.status).toBe(200)
    expect(target.headers.has('vary')).toBe(false)
    expect(target.headers.get('cache-control')).toBe('public, max-age=0, must-revalidate')
    expect(target.headers.get('vercel-cdn-cache-control')).toBe(
      'public, s-maxage=15, stale-while-revalidate=45',
    )
  })

  it.each([
    ['Cookie', 'uniswap'],
    ['Authorization', 'pancakeswap'],
    ['Accept-Encoding, Cookie', 'hyperliquid'],
    ['Origin', 'bybit'],
  ])('rejects unsafe or multi-token Vary %s', async (vary, venue) => {
    vi.stubGlobal('fetch', vi.fn(async (_url: URL, init: RequestInit) =>
      contracted(dashboardBody(), init, { Vary: vary })))
    const target = response()
    await handler(request({ venue, filter: 'gainers' }) as never, target.value as never)
    expect(target.result.status).toBe(200)
    expect(target.headers.has('vary')).toBe(false)
    expect(target.headers.get('cache-control')).toBe('no-store')
    expect(target.headers.has('vercel-cdn-cache-control')).toBe(false)
  })
})
