import { createHash, createHmac } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const vercelFunctions = vi.hoisted(() => {
  const values = new Map<string, unknown>()
  const waitTasks: Array<Promise<unknown>> = []
  return {
    values,
    waitTasks,
    cache: {
      async get(key: string) {
        return values.get(key) ?? null
      },
      async set(key: string, value: unknown) {
        values.set(key, value)
      },
      async delete(key: string) {
        values.delete(key)
      },
      async expireTag() {},
    },
  }
})

vi.mock('@vercel/functions', () => ({
  getCache: () => vercelFunctions.cache,
  waitUntil: (task: Promise<unknown>) => {
    vercelFunctions.waitTasks.push(task)
  },
}))

import handler, {
  isRetryableUpstreamRequest,
  publicProxyCanonical,
  releaseProvenance,
} from '../api/proxy'

interface CanonicalFixture {
  name: string
  absoluteURL: string
  method: string
  requestURI: string
  body: string
}

const canonicalFixtures = JSON.parse(readFileSync(
  resolve(process.cwd(), '../services/http/testdata/public_proxy_canonical.json'),
  'utf8',
)) as CanonicalFixture[]

function proxyRequest(pageSize = 37) {
  return {
    method: 'POST',
    url: '/api/proxy?path=v2/get_asset_dashboard',
    query: { path: 'v2/get_asset_dashboard' },
    headers: {
      host: 'qiu-market-preview.vercel.app',
      'content-type': 'application/json',
    },
    body: {
      venue: 'uniswap',
      page: 1,
      page_size: pageSize,
      currency: 'USD',
    },
  }
}

function proxyGETRequest(path: string) {
  return {
    method: 'GET',
    url: `/api/proxy?path=${encodeURIComponent(path)}`,
    query: { path },
    headers: {
      host: 'qiu-market-preview.vercel.app',
      accept: 'application/json',
    },
  }
}

function proxyResponse() {
  const headers = new Map<string, string | string[]>()
  const result = {
    statusCode: 200,
    body: Buffer.alloc(0),
    headers,
  }
  const response = {
    setHeader(name: string, value: string | string[]) {
      headers.set(name.toLowerCase(), value)
      return response
    },
    status(code: number) {
      result.statusCode = code
      return response
    },
    send(value: Buffer) {
      result.body = Buffer.from(value)
    },
    json(value: unknown) {
      result.body = Buffer.from(JSON.stringify(value))
    },
  }
  return { response, result }
}

beforeEach(() => {
  process.env.S78_BACKEND_ORIGIN = 'https://backend.example'
  process.env.S78_PROXY_HMAC_SECRET = 'unit-test-secret'
  process.env.QIU_MARKET_RELEASE_COMMIT =
    '19928325f9a1104d1dd3505a004dffb9fe52a714'
  process.env.VERCEL_DEPLOYMENT_ID = 'dpl_PreviewFixture123'
  process.env.VERCEL_URL = 'qiu-market-preview.vercel.app'
  vercelFunctions.values.clear()
  vercelFunctions.waitTasks.length = 0
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  delete process.env.S78_BACKEND_ORIGIN
  delete process.env.S78_PROXY_HMAC_SECRET
  delete process.env.QIU_MARKET_RELEASE_COMMIT
  delete process.env.QIU_MARKET_DEPLOYMENT_ID
  delete process.env.QIU_MARKET_DEPLOYMENT_URL
  delete process.env.VERCEL_DEPLOYMENT_ID
  delete process.env.VERCEL_URL
})

describe('releaseProvenance', () => {
  it('normalizes immutable release identity without trusting request input', () => {
    expect(releaseProvenance()).toEqual({
      status: 'VERIFIED',
      commit: '19928325f9a1104d1dd3505a004dffb9fe52a714',
      deploymentID: 'dpl_PreviewFixture123',
      deploymentURL: 'https://qiu-market-preview.vercel.app',
    })
  })

  it('fails closed when either release identity component is invalid', () => {
    process.env.QIU_MARKET_RELEASE_COMMIT = '1992832'
    process.env.QIU_MARKET_DEPLOYMENT_URL = 'https://bad.example/\r\ninjected'
    expect(releaseProvenance()).toEqual({ status: 'UNCONFIGURED' })
  })

  it('does not claim verified provenance without the deployment ID', () => {
    delete process.env.VERCEL_DEPLOYMENT_ID
    expect(releaseProvenance()).toEqual({ status: 'UNCONFIGURED' })
  })

  it('attaches provenance even to BFF validation failures', async () => {
    const rejected = proxyResponse()
    await handler({
      ...proxyRequest(),
      query: {},
    } as never, rejected.response as never)

    expect(rejected.result.statusCode).toBe(400)
    expect(rejected.result.headers.get('x-qiu-market-provenance')).toBe('VERIFIED')
    expect(rejected.result.headers.get('x-qiu-market-release-commit')).toBe(
      '19928325f9a1104d1dd3505a004dffb9fe52a714',
    )
    expect(rejected.result.headers.get('x-qiu-market-deployment-id')).toBe(
      'dpl_PreviewFixture123',
    )
    expect(rejected.result.headers.get('x-qiu-market-deployment-url')).toBe(
      'https://qiu-market-preview.vercel.app',
    )
  })

  it('rejects repeated and encoded traversal proxy paths before fetch', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    for (const path of [
      ['v2/get_asset_dashboard', 'v1/get_symbols'],
      '%2e%2e/healthz',
      'v1/trading/orders%3Fadmin=true',
    ]) {
      const rejected = proxyResponse()
      await handler({
        ...proxyRequest(),
        query: { path },
      } as never, rejected.response as never)
      expect(rejected.result.statusCode).toBe(400)
      expect(rejected.result.headers.get('cache-control')).toBe('no-store')
    }
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('rejects backend URLs that are not exact origins', async () => {
    process.env.S78_BACKEND_ORIGIN = 'https://backend.example/private-prefix'
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const rejected = proxyResponse()
    await handler(proxyRequest() as never, rejected.response as never)
    expect(rejected.result.statusCode).toBe(502)
    expect(rejected.result.headers.get('cache-control')).toBe('no-store')
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('isRetryableUpstreamRequest', () => {
  it('retries only explicitly safe GET reads', () => {
    expect(
      isRetryableUpstreamRequest(
        'GET',
        '/api/v1/trading/markets/BTC-USDT/status',
      ),
    ).toBe(true)
    expect(
      isRetryableUpstreamRequest('GET', '/api/v1/trading/orders/order-1'),
    ).toBe(true)
    expect(
      isRetryableUpstreamRequest('POST', '/api/v2/get_asset_dashboard'),
    ).toBe(false)
  })

  it('never retries OAuth navigation or callback requests', () => {
    expect(
      isRetryableUpstreamRequest(
        'GET',
        '/api/v1/trading/auth/github/start',
      ),
    ).toBe(false)
    expect(
      isRetryableUpstreamRequest(
        'GET',
        '/api/v1/trading/auth/github/callback',
      ),
    ).toBe(false)
  })

  it('does not infer retry safety from the HTTP method alone', () => {
    expect(isRetryableUpstreamRequest('GET', '/api/v1/future-write')).toBe(false)
    expect(isRetryableUpstreamRequest('HEAD', '/api/v1/future-write')).toBe(false)
    expect(isRetryableUpstreamRequest('POST', '/api/v1/trading/orders')).toBe(false)
  })
})

describe('upstream HMAC replay protection', () => {
  it('shares exact path and query canonicalization with the Go verifier', () => {
    expect(canonicalFixtures).toHaveLength(3)
    for (const [index, fixture] of canonicalFixtures.entries()) {
      const timestamp = '1800000000'
      const nonce = (index + 16).toString(16).padStart(32, '0')
      const url = new URL(fixture.absoluteURL)
      expect(url.pathname + url.search).toBe(fixture.requestURI)
      const digest = createHash('sha256').update(fixture.body).digest('hex')
      expect(publicProxyCanonical(
        timestamp,
        nonce,
        fixture.method,
        url,
        digest,
      )).toBe([
        timestamp,
        nonce,
        fixture.method,
        fixture.requestURI,
        digest,
      ].join('\n'))
    }
  })

  it('uses a separately signed canonical nonce for every safe-read retry', async () => {
    const payload = JSON.stringify({ principal: { account_id: 'demo' } })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('temporary', { status: 503 }))
      .mockResolvedValueOnce(new Response(payload, {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
    vi.stubGlobal('fetch', fetchMock)

    const proxied = proxyResponse()
    await handler(
      proxyGETRequest('v1/trading/session') as never,
      proxied.response as never,
    )

    expect(proxied.result.statusCode).toBe(200)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const nonces: string[] = []
    for (const [rawURL, options] of fetchMock.mock.calls) {
      const url = rawURL as URL
      const request = options as RequestInit
      const headers = new Headers(request.headers)
      const timestamp = headers.get('X-Qiu-Market-Timestamp')
      const nonce = headers.get('X-Qiu-Market-Nonce')
      const digest = headers.get('X-Qiu-Market-Content-SHA256')
      expect(timestamp).toMatch(/^\d{10}$/)
      expect(nonce).toMatch(/^[0-9a-f]{32}$/)
      expect(digest).toBe(
        'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      )
      const canonical = [
        timestamp,
        nonce,
        'GET',
        url.pathname + url.search,
        digest,
      ].join('\n')
      expect(headers.get('X-Qiu-Market-Signature')).toBe(
        createHmac('sha256', 'unit-test-secret').update(canonical).digest('hex'),
      )
      nonces.push(nonce ?? '')
    }
    expect(new Set(nonces).size).toBe(2)
  })

  it('keeps upstream failures non-cacheable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ code: 'backend_failed' }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }),
    ))
    const proxied = proxyResponse()
    await handler({
      ...proxyGETRequest('v1/trading/auth/github/start'),
      headers: {
        ...proxyGETRequest('v1/trading/auth/github/start').headers,
        accept: 'application/json',
      },
    } as never, proxied.response as never)
    expect(proxied.result.statusCode).toBe(500)
    expect(proxied.result.headers.get('cache-control')).toBe('no-store')
  })
})

describe('public stale-while-revalidate', () => {
  it('returns aged last-good immediately and deduplicates the background refresh', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-28T00:00:00Z'))
    const cachedPayload = JSON.stringify({
      code: 2000,
      result: [{
        asset_symbol: 'BTC',
        price_usd: { value: '65000', available: true },
        composite_price_usd: { value: '64950', available: true },
        market_reference_price_usd: { value: '64900', available: true },
        display_price_usd: { value: '65000', available: true },
        display_price_kind: 'dex_route',
        display_available: true,
        dex_route_available: true,
        dex_route_count: 3,
        available: true,
        freshness_status: 'stale',
        freshness_age_seconds: 50,
      }],
    })
    let completeRevalidation: ((response: Response) => void) | undefined
    const pendingRevalidation = new Promise<Response>((resolve) => {
      completeRevalidation = resolve
    })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(cachedPayload, {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockImplementationOnce(() => pendingRevalidation)
    vi.stubGlobal('fetch', fetchMock)

    const first = proxyResponse()
    await handler(
      proxyRequest() as never,
      first.response as never,
    )
    await Promise.all([...vercelFunctions.waitTasks])
    expect(first.result.headers.get('x-qiu-market-cache')).toBe('MISS')
    expect(first.result.headers.get('x-qiu-market-provenance')).toBe('VERIFIED')
    expect(first.result.headers.get('x-qiu-market-release-commit')).toBe(
      '19928325f9a1104d1dd3505a004dffb9fe52a714',
    )

    vercelFunctions.waitTasks.length = 0
    vi.setSystemTime(new Date('2026-07-28T00:00:16Z'))
    const staleOne = proxyResponse()
    const staleTwo = proxyResponse()
    await handler(proxyRequest() as never, staleOne.response as never)
    await handler(proxyRequest() as never, staleTwo.response as never)

    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(vercelFunctions.waitTasks).toHaveLength(1)
    expect(staleOne.result.statusCode).toBe(200)
    expect(staleOne.result.headers.get('x-qiu-market-cache')).toBe('STALE')
    expect(staleOne.result.headers.get('x-qiu-market-deployment-url')).toBe(
      'https://qiu-market-preview.vercel.app',
    )
    expect(staleOne.result.headers.get('age')).toBe('16')
    const agedRow = JSON.parse(staleOne.result.body.toString()).result[0]
    expect(agedRow.freshness_age_seconds).toBe(66)
    expect(agedRow.dex_route_available).toBe(false)
    expect(agedRow.display_price_kind).toBe('composite_reference')

    completeRevalidation?.(new Response(cachedPayload, {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    await Promise.all([...vercelFunctions.waitTasks])
    const cached = Array.from(vercelFunctions.values.values())[0] as {
      storedAt?: number
    }
    expect(cached.storedAt).toBe(Date.now())
  })

  it('drops excess different-key refreshes instead of queueing past the function lifetime', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-28T01:00:00Z'))
    const payload = JSON.stringify({ code: 2000, result: [], total: 0 })
    const pending: Array<() => void> = []
    let active = 0
    let maximumActive = 0
    const fetchMock = vi.fn(async () => {
      if (fetchMock.mock.calls.length <= 3) {
        return new Response(payload, {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      active += 1
      maximumActive = Math.max(maximumActive, active)
      return new Promise<Response>((resolve) => {
        pending.push(() => {
          active -= 1
          resolve(new Response(payload, {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }))
        })
      })
    })
    vi.stubGlobal('fetch', fetchMock)

    for (const pageSize of [31, 32, 33]) {
      const primed = proxyResponse()
      await handler(proxyRequest(pageSize) as never, primed.response as never)
    }
    await Promise.all([...vercelFunctions.waitTasks])
    vercelFunctions.waitTasks.length = 0
    vi.setSystemTime(new Date('2026-07-28T01:00:16Z'))

    await Promise.all([31, 32, 33].map(async (pageSize) => {
      const stale = proxyResponse()
      await handler(proxyRequest(pageSize) as never, stale.response as never)
      expect(stale.result.headers.get('x-qiu-market-cache')).toBe('STALE')
    }))

    expect(active).toBe(2)
    expect(maximumActive).toBe(2)
    expect(fetchMock).toHaveBeenCalledTimes(5)
    expect(vercelFunctions.waitTasks).toHaveLength(2)

    pending.shift()?.()
    await vercelFunctions.waitTasks[0]
    expect(active).toBe(1)
    const retried = proxyResponse()
    await handler(proxyRequest(33) as never, retried.response as never)
    expect(retried.result.headers.get('x-qiu-market-cache')).toBe('STALE')
    expect(fetchMock).toHaveBeenCalledTimes(6)
    expect(vercelFunctions.waitTasks).toHaveLength(3)
    expect(maximumActive).toBe(2)
    while (pending.length > 0) pending.shift()?.()
    await Promise.all([...vercelFunctions.waitTasks])
    expect(active).toBe(0)
  })
})
