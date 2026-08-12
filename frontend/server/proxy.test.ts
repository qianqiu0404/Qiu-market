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
	requiresBackendMarketContract,
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

function proxyRequest(pageSize = 37, snapshotID = '') {
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
		snapshot_id: snapshotID,
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

function contractedResponse(
  body: string,
  request: RequestInit,
  overrides: Record<string, string> = {},
	status = 200,
): Response {
  const requestHeaders = new Headers(request.headers)
  return new Response(body, {
		status,
    headers: {
      'Content-Type': 'application/json',
      'X-Qiu-Market-Backend-Release-Commit':
        process.env.QIU_MARKET_RELEASE_COMMIT ?? '',
      'X-Qiu-Market-Data-Mode': 'live',
      'X-Qiu-Market-Provider-Policy': 'restricted-no-bypass.v1',
      'X-Qiu-Market-Contract-Schema': 'qiu.market-read-contract.v1',
      'X-Qiu-Market-Snapshot-Schema': 'qiu.market-snapshot.v1',
		'X-Qiu-Market-Edge-Release-Commit': process.env.QIU_MARKET_RELEASE_COMMIT ?? '',
		'X-Qiu-Market-Edge-Data-Mode': 'live',
		'X-Qiu-Market-Edge-Contract-Schema': 'qiu.market-edge-contract.v1',
      'X-Qiu-Market-Backend-Request-Nonce':
        requestHeaders.get('X-Qiu-Market-Nonce') ?? '',
      ...overrides,
    },
  })
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
  it('retries only explicitly safe reads', () => {
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
    ).toBe(true)
		expect(
			isRetryableUpstreamRequest('POST', '/api/v2/get_market_price_ticks'),
		).toBe(true)
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
		expect(isRetryableUpstreamRequest('POST', '/api/v1/trading/fund')).toBe(false)
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

  it('re-signs a read-only market POST retry with a new nonce', async () => {
		const payload = JSON.stringify({
			code: 2000, result: [], total: 0, overview: {
				venue: 'all', asset_count: 1, priced_asset_count: 0,
				fresh_asset_count: 0, stale_asset_count: 0, unavailable_asset_count: 1,
			},
			snapshot_id: 'snp_00000000000000000000000000000001',
			snapshot_as_of: 1785196800000,
			snapshot_schema: 'qiu.market-snapshot.v1',
		})
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(new Response('{}', { status: 504 }))
			.mockImplementationOnce(async (_url: URL, request: RequestInit) =>
				contractedResponse(payload, request))
		vi.stubGlobal('fetch', fetchMock)

		const proxied = proxyResponse()
		await handler(proxyRequest(89) as never, proxied.response as never)
		expect(proxied.result.statusCode).toBe(200)
		expect(fetchMock).toHaveBeenCalledTimes(2)
		const nonces = fetchMock.mock.calls.map(([, request]) =>
			new Headers((request as RequestInit).headers).get('X-Qiu-Market-Nonce'))
		expect(nonces[0]).toMatch(/^[0-9a-f]{32}$/)
		expect(nonces[1]).toMatch(/^[0-9a-f]{32}$/)
		expect(nonces[0]).not.toBe(nonces[1])
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

describe('public cache contract boundary', () => {
  const payload = JSON.stringify({
    code: 2000,
    result: [],
    total: 0,
		overview: {
			venue: 'all', asset_count: 1, priced_asset_count: 0,
			fresh_asset_count: 0, stale_asset_count: 0, unavailable_asset_count: 1,
		},
    snapshot_id: 'snp_00000000000000000000000000000001',
		snapshot_as_of: 1785196800000,
    snapshot_schema: 'qiu.market-snapshot.v1',
  })

	it('does not cache an explicit snapshot ID past Redis authority', async () => {
		const snapshotID = 'snp_00000000000000000000000000000001'
		const fetchMock = vi.fn(async (_url: URL, request: RequestInit) =>
			contractedResponse(payload, request))
		vi.stubGlobal('fetch', fetchMock)
		await handler(proxyRequest(95, snapshotID) as never, proxyResponse().response as never)
		fetchMock.mockImplementationOnce(async (_url: URL, request: RequestInit) =>
			contractedResponse(JSON.stringify({ code: 4000, message: 'market snapshot is unknown or expired' }), request, {}, 409))
		const expired = proxyResponse()
		await handler(proxyRequest(95, snapshotID) as never, expired.response as never)
		expect(fetchMock).toHaveBeenCalledTimes(2)
		expect(expired.result.statusCode).toBe(409)
	})

  it('binds fresh cache responses to the verified backend contract', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-28T00:00:00Z'))
    const fetchMock = vi.fn(async (_url: URL, request: RequestInit) =>
      contractedResponse(payload, request))
    vi.stubGlobal('fetch', fetchMock)
    const primed = proxyResponse()
    await handler(proxyRequest(91) as never, primed.response as never)
    await Promise.all([...vercelFunctions.waitTasks])

    vi.setSystemTime(new Date('2026-07-28T00:00:10Z'))
    const cached = proxyResponse()
    await handler(proxyRequest(91) as never, cached.response as never)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(cached.result.headers.get('x-qiu-market-cache')).toBe('FRESH')
    expect(cached.result.headers.get('x-qiu-market-backend-release-commit')).toBe(
      process.env.QIU_MARKET_RELEASE_COMMIT,
    )
    expect(cached.result.headers.get('x-qiu-market-snapshot-schema')).toBe(
      'qiu.market-snapshot.v1',
    )
  })

  it('fails typed 502 instead of hiding a wrong backend behind stale cache', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-28T01:00:00Z'))
    const fetchMock = vi.fn(async (_url: URL, request: RequestInit) =>
      contractedResponse(payload, request))
    vi.stubGlobal('fetch', fetchMock)
    await handler(proxyRequest(92) as never, proxyResponse().response as never)
    await Promise.all([...vercelFunctions.waitTasks])

    vi.setSystemTime(new Date('2026-07-28T01:00:16Z'))
    fetchMock.mockImplementationOnce(async (_url: URL, request: RequestInit) =>
      contractedResponse(payload, request, {
        'X-Qiu-Market-Backend-Release-Commit': '0000000000000000000000000000000000000000',
      }))
    const rejected = proxyResponse()
    await handler(proxyRequest(92) as never, rejected.response as never)
    expect(rejected.result.statusCode).toBe(502)
    expect(rejected.result.headers.get('cache-control')).toBe('no-store')
    expect(JSON.parse(rejected.result.body.toString())).toMatchObject({
      code: 'backend_contract_mismatch',
      result: { reason: 'releaseCommit_mismatch' },
    })
  })

  it('rejects a replayed response nonce even when every static field matches', async () => {
    const fetchMock = vi.fn(async (_url: URL, request: RequestInit) =>
      contractedResponse(payload, request, {
        'X-Qiu-Market-Backend-Request-Nonce': '00000000000000000000000000000000',
      }))
    vi.stubGlobal('fetch', fetchMock)
    const rejected = proxyResponse()
    await handler(proxyRequest(93) as never, rejected.response as never)
    expect(rejected.result.statusCode).toBe(502)
    expect(JSON.parse(rejected.result.body.toString())).toMatchObject({
      code: 'backend_contract_mismatch',
      result: { reason: 'request_nonce_mismatch' },
    })
  })

  it('uses contract-bound stale data only for transport failure', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-28T02:00:00Z'))
    const fetchMock = vi.fn(async (_url: URL, request: RequestInit) =>
      contractedResponse(payload, request))
    vi.stubGlobal('fetch', fetchMock)
    await handler(proxyRequest(94) as never, proxyResponse().response as never)
    await Promise.all([...vercelFunctions.waitTasks])
    vi.setSystemTime(new Date('2026-07-28T02:00:16Z'))
    fetchMock.mockRejectedValue(new Error('transport down'))
    const stale = proxyResponse()
    await handler(proxyRequest(94) as never, stale.response as never)
    expect(stale.result.statusCode).toBe(200)
    expect(stale.result.headers.get('x-qiu-market-cache')).toBe('STALE')
  })
})

describe('uncached ticks contract boundary', () => {
	function ticksRequest() {
		return {
			...proxyRequest(),
			url: '/api/proxy?path=v2/get_market_price_ticks',
			query: { path: 'v2/get_market_price_ticks' },
			body: { venue: 'all', asset_ids: ['asset-btc'] },
		}
	}

	it('requires the backend and edge contract without making ticks cacheable', () => {
		expect(requiresBackendMarketContract('POST', '/api/v2/get_market_price_ticks')).toBe(true)
	})

	it.each([
		['wrong release', { 'X-Qiu-Market-Backend-Release-Commit': '0000000000000000000000000000000000000000' }, 'releaseCommit_mismatch'],
		['replayed nonce', { 'X-Qiu-Market-Backend-Request-Nonce': '00000000000000000000000000000000' }, 'request_nonce_mismatch'],
		['legacy replay mode', { 'X-Qiu-Data-Mode': 'd1_deterministic_replay' }, 'legacy_data_mode_mismatch'],
		['direct backend without edge', { 'X-Qiu-Market-Edge-Release-Commit': '' }, 'edgeReleaseCommit_mismatch'],
	])('rejects %s with typed 502', async (_name, overrides, reason) => {
		vi.stubGlobal('fetch', vi.fn(async (_url: URL, request: RequestInit) =>
			contractedResponse(JSON.stringify({ code: 2000, result: [] }), request, overrides)))
		const rejected = proxyResponse()
		await handler(ticksRequest() as never, rejected.response as never)
		expect(rejected.result.statusCode).toBe(502)
		expect(JSON.parse(rejected.result.body.toString())).toMatchObject({
			code: 'backend_contract_mismatch', result: { reason },
		})
	})
})
