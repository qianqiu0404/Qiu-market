import type { IncomingMessage, ServerResponse } from 'node:http'
import { randomUUID } from 'node:crypto'
import proxyHandler from '../proxy.js'

const VENUES = new Set([
  'all', 'binance', 'coinbase', 'bybit', 'okx', 'hyperliquid', 'uniswap',
  'pancakeswap',
])
const FILTERS = new Set(['assets', 'gainers', 'losers'])
const CDN_CACHE_CONTROL = 'public, s-maxage=15, stale-while-revalidate=45'
const BROWSER_CACHE_CONTROL = 'public, max-age=0, must-revalidate'

interface DefaultDashboardRequest extends IncomingMessage {
  query: Record<string, string | string[] | undefined>
}

interface DefaultDashboardResponse extends ServerResponse {
  status(code: number): DefaultDashboardResponse
  json(value: unknown): void
  send(value: Buffer): void
}

function exactDefaultQuery(
  query: Record<string, string | string[] | undefined>,
): { venue: string; filter: string } | null {
  if (Object.keys(query).some((key) => key !== 'venue' && key !== 'filter')) return null
  const venue = typeof query.venue === 'string' ? query.venue : query.venue == null ? 'all' : ''
  const filter = typeof query.filter === 'string' ? query.filter : query.filter == null ? 'assets' : ''
  return VENUES.has(venue) && FILTERS.has(filter) ? { venue, filter } : null
}

function capturedProxyResponse() {
  const headers = new Map<string, string | number | readonly string[]>()
  let statusCode = 200
  let body = Buffer.alloc(0)
  const response = {
    setHeader(name: string, value: string | number | readonly string[]) {
      headers.set(name.toLowerCase(), value)
      return response
    },
    status(code: number) { statusCode = code; return response },
    send(value: Buffer) { body = Buffer.from(value) },
    json(value: unknown) {
      body = Buffer.from(JSON.stringify(value))
      headers.set('content-type', 'application/json; charset=utf-8')
    },
  }
  return { response, headers, result: () => ({ statusCode, body }) }
}

export default async function handler(
  request: DefaultDashboardRequest,
  response: DefaultDashboardResponse,
): Promise<void> {
  response.setHeader('Cache-Control', 'no-store')
  if ((request.method ?? 'GET').toUpperCase() !== 'GET') {
    response.status(405).json({ code: 'method_not_allowed', message: 'GET is required.' })
    return
  }
  const fixed = exactDefaultQuery(request.query)
  if (!fixed) {
    response.status(400).json({
      code: 'invalid_default_dashboard_query',
      message: 'Only a supported venue and assets/gainers/losers filter are allowed.',
    })
    return
  }

  const captured = capturedProxyResponse()
  const safeHeaders = {
    accept: 'application/json', host: 'qiu-market.vercel.app',
    'user-agent': 'qiu-market-default-dashboard', 'x-request-id': randomUUID(),
  }
  await proxyHandler({
    method: 'POST',
    url: '/api/proxy?path=v2/get_asset_dashboard',
    query: { path: 'v2/get_asset_dashboard' },
    headers: safeHeaders,
    body: {
      consumer_token: 'frontend-dashboard', venue: fixed.venue, filter: fixed.filter,
      page: 1, page_size: 50, search: '', sort_by: 'rank', sort_direction: 'desc',
      include_uncovered: true,
      universe: fixed.venue === 'all' ? 'provider_union' : 'provider_top50',
      snapshot_id: '',
    },
  } as never, captured.response as never)

  const result = captured.result()
  for (const [name, value] of captured.headers) {
    if (name !== 'set-cookie' && name !== 'vary') response.setHeader(name, value)
  }
  const stale = String(captured.headers.get('x-qiu-market-cache') ?? '').toUpperCase() === 'STALE'
  const cacheState = String(captured.headers.get('x-qiu-market-cache') ?? '').toUpperCase()
  const verified = captured.headers.get('x-qiu-market-provenance') === 'VERIFIED' &&
    typeof captured.headers.get('x-qiu-market-backend-release-commit') === 'string' &&
    captured.headers.get('x-qiu-market-data-mode') === 'live'
  const hasCookies = captured.headers.has('set-cookie')
  const vary = String(captured.headers.get('vary') ?? '').trim()
  if (result.statusCode === 200 && !stale && verified &&
    (cacheState === 'MISS' || cacheState === 'FRESH') && !hasCookies && vary === '') {
    response.setHeader('Cache-Control', BROWSER_CACHE_CONTROL)
    response.setHeader('Vercel-CDN-Cache-Control', CDN_CACHE_CONTROL)
  } else {
    response.setHeader('Cache-Control', 'no-store')
    response.removeHeader('Vercel-CDN-Cache-Control')
  }
  response.status(result.statusCode).send(result.body)
}

export const config = { maxDuration: 10 }
