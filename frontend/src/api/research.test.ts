import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  getResearchSignalEvent,
  getResearchSignalEvents,
  getResearchSignalSummary,
  ResearchSignalsRequestError,
} from './research'

const item = {
  id: 'evt-1',
  schemaVersion: 'researchsignal/v1',
  type: 'market_event',
  title: 'Federal Reserve schedule',
  summary: 'A deterministic research fixture.',
  source: 'xiuqiu-site Market Radar',
  provider: 'xiuqiu-site',
  sourceUrl: 'https://xiuqiu-site.vercel.app/market-radar/events/evt-1',
  assets: ['BTC'],
  eventTime: '2026-08-10T01:00:00Z',
  observedAt: null,
  receivedAt: '2026-08-10T01:00:02Z',
  publishedAt: '2026-08-10T00:55:00Z',
  freshness: 'fresh',
  priority: 'P1',
  watchFor: 'Watch BTC volatility after the event.',
  invalidation: null,
  qualityFlags: ['observed_time_missing'],
  contentHash: `sha256:${'a'.repeat(64)}`,
  executable: false,
  sourceKind: 'xiuqiu_automated_dynamic',
}

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function page(overrides: Record<string, unknown> = {}) {
  return {
    schemaVersion: 'researchsignals/v1',
    status: 'fresh',
    generatedAt: '2026-08-10T01:00:03Z',
    data: { items: [item], nextCursor: null },
    error: null,
    ...overrides,
  }
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('research signal API', () => {
  it('keeps the research API and component statically isolated from trading modules and routes', () => {
    for (const path of [
      resolve(process.cwd(), 'src/api/research.ts'),
      resolve(process.cwd(), 'src/features/insights/ResearchSignalFeed.vue'),
    ]) {
      const source = readFileSync(path, 'utf8')
      expect(source).not.toMatch(/(?:from\s+['"][^'"]*trading|\/api\/v1\/trading\/)/)
    }
  })

  it('uses the frozen GET query and preserves strings, nulls, source and non-executable semantics', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toBe('/api/v1/research/signals/events?market=crypto&asset=BTC&window=168&limit=20')
      expect(init).toMatchObject({ method: 'GET', credentials: 'same-origin' })
      return response(page())
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await getResearchSignalEvents()
    expect(result.status).toBe('fresh')
    expect(result.data.items[0]).toMatchObject({
      provider: 'xiuqiu-site',
      observedAt: null,
      executable: false,
      editorialPriority: 'P1',
      contentHash: `sha256:${'a'.repeat(64)}`,
    })
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it.each(['fresh', 'empty', 'legacy', 'degraded', 'stale', 'partial', 'unconfigured']) (
    'accepts the explicit top-level %s state',
    async (status) => {
      const statusItem = status === 'stale'
        ? { ...item, freshness: 'stale', qualityFlags: ['observed_time_missing', 'stale'] }
        : item
      vi.stubGlobal('fetch', vi.fn(async () => response(page({
        status,
        data: { items: status === 'empty' || status === 'degraded' || status === 'unconfigured' ? [] : [statusItem], nextCursor: null },
        error: status === 'degraded' || status === 'unconfigured'
          ? { code: 'upstream', message: 'source unavailable', retryAfterSeconds: null }
          : null,
      }))))
      expect((await getResearchSignalEvents()).status).toBe(status)
    },
  )

  it('fails closed if an item claims to be executable or changes source authority', async () => {
    for (const patch of [{ executable: true }, { provider: 'coinglass' }]) {
      vi.stubGlobal('fetch', vi.fn(async () => response(page({
        data: { items: [{ ...item, ...patch }], nextCursor: null },
      }))))
      await expect(getResearchSignalEvents()).rejects.toMatchObject({ code: 'invalid_response' })
    }
  })

  it('rejects unsafe source URLs and malformed timestamps', async () => {
    for (const patch of [
      { sourceUrl: 'javascript:alert(1)' },
      { receivedAt: 'yesterday' },
    ]) {
      vi.stubGlobal('fetch', vi.fn(async () => response(page({
        data: { items: [{ ...item, ...patch }], nextCursor: null },
      }))))
      await expect(getResearchSignalEvents()).rejects.toBeInstanceOf(ResearchSignalsRequestError)
    }
  })

  it('preserves a fixed-shape typed degraded response instead of presenting false empty data', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => response({
      schemaVersion: 'researchsignals/v1',
      status: 'degraded',
      generatedAt: '2026-08-10T01:00:03Z',
      data: { items: [], nextCursor: null },
      error: { code: 'timeout', message: 'Research source timed out', retryAfterSeconds: 30 },
    }, 503)))

    await expect(getResearchSignalEvents()).resolves.toMatchObject({
      status: 'degraded',
      data: { items: [] },
      error: { code: 'timeout', retryAfterSeconds: 30 },
    })
  })

  it('encodes an opaque detail ID as one path segment', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      expect(String(input)).toBe('/api/v1/research/signals/events/evt%3Aone')
      return response(page({ data: { item: {
        ...item,
        id: 'evt:one',
        sourceUrl: 'https://xiuqiu-site.vercel.app/market-radar/events/evt%3Aone',
      } } }))
    })
    vi.stubGlobal('fetch', fetchMock)

    expect((await getResearchSignalEvent('evt:one')).data.item?.id).toBe('evt:one')
  })

  it('parses the frozen summary without confusing unavailable source state with no events', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      expect(String(input)).toBe('/api/v1/research/signals/summary')
      return response(page({
        status: 'degraded',
        data: {
          latestEventAt: '2026-08-10T01:00:00Z', freshnessMinutes: 2,
          isDelayed: false, eventCount24h: 4, p0Count24h: 0, p1Count24h: 2,
          sources: [{
            source: 'xiuqiu-site', status: 'degraded',
            lastSuccessAt: '2026-08-10T01:00:02Z', message: 'one feed is delayed',
          }],
        },
        error: { code: 'upstream', message: 'source degraded', retryAfterSeconds: null },
      }), 503)
    }))

    const result = await getResearchSignalSummary()
    expect(result).toMatchObject({
      status: 'degraded',
      data: { eventCount24h: 4, p1Count24h: 2, sources: [{ status: 'degraded' }] },
    })
  })

  it('rejects client-side limits beyond the server maximum before fetch', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(getResearchSignalEvents({ limit: 51 })).rejects.toMatchObject({ code: 'invalid_request' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('keeps the BTC research scope fixed and bounds opaque cursors without interpreting them', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      expect(String(input)).toBe('/api/v1/research/signals/events?market=crypto&asset=BTC&window=168&limit=7&cursor=opaque%3Apage-2')
      return response(page())
    })
    vi.stubGlobal('fetch', fetchMock)
    await getResearchSignalEvents({ limit: 7, cursor: 'opaque:page-2' })
    await expect(getResearchSignalEvents({ cursor: `bad\u0000cursor` })).rejects.toMatchObject({ code: 'invalid_request' })
    await expect(getResearchSignalEvents({ cursor: 'x'.repeat(513) })).rejects.toMatchObject({ code: 'invalid_request' })
    await expect(getResearchSignalEvents({ cursor: 'contains space' })).rejects.toMatchObject({ code: 'invalid_request' })
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('requires JSON content type and rejects oversized payloads', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', {
      status: 200, headers: { 'Content-Type': 'text/html' },
    })))
    await expect(getResearchSignalEvents()).rejects.toMatchObject({ code: 'invalid_content_type' })

    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json', 'Content-Length': String(512 * 1024 + 1) },
    })))
    await expect(getResearchSignalEvents()).rejects.toMatchObject({ code: 'response_too_large' })

    const chunk = new Uint8Array(256 * 1024).fill(97)
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new ReadableStream({
      start(controller) {
        controller.enqueue(chunk)
        controller.enqueue(chunk)
        controller.enqueue(new Uint8Array([97]))
        controller.close()
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    await expect(getResearchSignalEvents()).rejects.toMatchObject({ code: 'response_too_large' })
  })

  it('aborts a request after the bounded total timeout', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    })))
    const pending = getResearchSignalEvents()
    const assertion = expect(pending).rejects.toMatchObject({ code: 'timeout' })
    await vi.advanceTimersByTimeAsync(8_001)
    await assertion
  })

  it('bounds text, assets, quality flags, editorial priority and cross-field states', async () => {
    const invalidItems = [
      { ...item, title: 'x'.repeat(301) },
      { ...item, assets: ['ETH'] },
      { ...item, qualityFlags: ['invented'] },
      { ...item, priority: 'P3' },
      { ...item, source: 'Federal Reserve' },
      { ...item, sourceUrl: 'https://example.test/research/evt-1' },
      { ...item, observedAt: '2026-08-10T01:00:01Z' },
      { ...item, freshness: 'stale' },
      { ...item, qualityFlags: ['observed_time_missing', 'stale'] },
      { ...item, watchFor: null, invalidation: null },
      { ...item, qualityFlags: ['observed_time_missing', 'legacy_fields_missing'] },
      { ...item, qualityFlags: ['observed_time_missing', 'duplicate'] },
    ]
    for (const invalidItem of invalidItems) {
      vi.stubGlobal('fetch', vi.fn(async () => response(page({ data: { items: [invalidItem], nextCursor: null } }))))
      await expect(getResearchSignalEvents()).rejects.toMatchObject({ code: 'invalid_response' })
    }
    vi.stubGlobal('fetch', vi.fn(async () => response(page({ status: 'empty' }))))
    await expect(getResearchSignalEvents()).rejects.toMatchObject({ code: 'invalid_response' })
    vi.stubGlobal('fetch', vi.fn(async () => response(page({ status: 'degraded', error: null }))))
    await expect(getResearchSignalEvents()).rejects.toMatchObject({ code: 'invalid_response' })
  })
})
