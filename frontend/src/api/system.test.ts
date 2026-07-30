import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  buildLegacySystemStatus,
  getSystemStatus,
  normalizeSystemStatusSnapshot,
} from './system'

const live = (source: string) => ({
  state: 'live',
  last_success_at: 1785384000000,
  age_seconds: 0,
  reason: 'explicit success',
  source,
})

const metric = (value: number) => ({
  available: true,
  value,
  reason: '',
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('normalizeSystemStatusSnapshot', () => {
  it('keeps explicit zero metrics available and accepts LIVE only with every required probe', () => {
    const snapshot = normalizeSystemStatusSnapshot({
      schema_version: 'system-status.v1',
      formula_version: 'system-display.v1',
      generated_at: 1785384000000,
      overall: live('system-display.v1'),
      components: {
        matching: live('matching'),
        liquidity: live('book'),
        transport: live('transport'),
        market_data: live('index'),
        outbox: live('outbox'),
        database: live('database'),
        disk: live('disk'),
        retention: live('retention'),
      },
      storage: {
        database_bytes: metric(100),
        kline_table_bytes: metric(50),
        kline_heap_bytes: metric(30),
        kline_index_bytes: metric(20),
        kline_estimated_rows: metric(0),
        disk_free_bytes: metric(40 * 2 ** 30),
        disk_state: 'healthy',
        retention_last_started_at: metric(1785380000000),
        retention_last_success_at: metric(1785381000000),
        retention_deleted_rows: {
          '1m': metric(0),
          '15m': metric(1),
          '1h': metric(2),
        },
        kline_intervals: [],
      },
      price_sources: [],
      processes: [],
      provider_statuses: [],
    })

    expect(snapshot.overall.state).toBe('live')
    expect(snapshot.storage.kline_estimated_rows).toEqual(metric(0))
    expect(snapshot.storage.retention_deleted_rows['1m']).toEqual(metric(0))
  })

  it('recomputes a claimed LIVE response as degraded when required fields are missing', () => {
    const snapshot = normalizeSystemStatusSnapshot({
      overall: live('untrusted'),
      components: {
        matching: live('matching'),
      },
      storage: {},
    })

    expect(snapshot.overall.state).toBe('degraded')
    expect(snapshot.components.outbox.state).toBe('unknown')
    expect(snapshot.storage.database_bytes).toMatchObject({
      available: false,
      value: null,
    })
  })

  it('uses DEMO SNAPSHOT only when the response explicitly identifies that source mode', () => {
    const snapshot = normalizeSystemStatusSnapshot({
      source_mode: 'demo_snapshot',
      components: {},
      storage: {},
    })

    expect(snapshot.overall.state).toBe('demo_snapshot')
  })
})

describe('buildLegacySystemStatus', () => {
  const now = 1785384000000

  it('keeps missing old-backend storage and outbox fields unknown instead of ready or zero', () => {
    const snapshot = buildLegacySystemStatus({
      overview: {
        crawler_status: 'running',
        dex_status: 'running',
        dw_status: 'running',
        rpc_status: 'running',
        redis_status: 'connected',
        database_status: 'connected',
        worker_status: 'running',
        api_status: 'healthy',
        provider_statuses: [],
      },
      referenceOverview: {
        priced_asset_count: 4,
        index_updated_at: now - 90_000,
      },
      uniswapOverview: {
        routable_asset_count: 1,
        index_updated_at: now - 5_000,
      },
      pancakeOverview: {
        routable_asset_count: 0,
        index_updated_at: now - 5_000,
      },
      tradingStatus: {
        state: 'ready',
      },
      orderBook: {
        bids: [{ price: '1' }],
        asks: [{ price: '2' }],
      },
    }, now)

    expect(snapshot.source_mode).toBe('legacy')
    expect(snapshot.components.market_data.state).toBe('cached')
    expect(snapshot.components.outbox.state).toBe('unknown')
    expect(snapshot.components.disk.state).toBe('unknown')
    expect(snapshot.components.retention.state).toBe('unknown')
    expect(snapshot.storage.database_bytes.available).toBe(false)
    expect(snapshot.storage.retention_deleted_rows['1m'].available).toBe(false)
    expect(snapshot.overall.state).toBe('degraded')
  })

  it('reports stale data, database loss, critical disk, and retention failure independently', () => {
    const snapshot = buildLegacySystemStatus({
      overview: {
        database_status: 'disconnected',
        storage: {
          database_bytes: 100,
          kline_table_bytes: 50,
          kline_heap_bytes: 30,
          kline_index_bytes: 20,
          kline_estimated_rows: 0,
          disk_free_bytes: 10 * 2 ** 30,
          disk_state: 'critical',
          retention_last_success_at: now - 3_600_000,
          retention_last_error: 'delete failed',
          retention_deleted_rows: { '1m': 0, '15m': 0, '1h': 0 },
          kline_intervals: [],
        },
      },
      referenceOverview: {
        priced_asset_count: 4,
        index_updated_at: now - 10 * 60_000,
      },
      uniswapOverview: {
        routable_asset_count: 1,
        index_updated_at: now - 5_000,
      },
      pancakeOverview: {
        routable_asset_count: 0,
        index_updated_at: now - 5_000,
      },
      tradingStatus: {
        state: 'ready',
        outbox_state: 'ready',
      },
      orderBook: {
        bids: [{ price: '1' }],
        asks: [{ price: '2' }],
      },
    }, now)

    expect(snapshot.components.market_data.state).toBe('degraded')
    expect(snapshot.components.database.state).toBe('offline')
    expect(snapshot.components.disk.state).toBe('degraded')
    expect(snapshot.components.retention.state).toBe('degraded')
    expect(snapshot.storage.kline_estimated_rows).toEqual(metric(0))
    expect(snapshot.overall.state).toBe('degraded')
  })
})

describe('getSystemStatus', () => {
  it('falls back to the legacy read-only probes only for an old backend route', async () => {
    const now = Date.now()
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), 'http://qiu.test').pathname
      if (path === '/api/v1/get_system_status') {
        return new Response(JSON.stringify({ message: 'not found' }), {
          status: 404,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (path === '/api/v1/get_system_overview') {
        return new Response(JSON.stringify({
          code: 2000,
          result: {
            database_status: 'connected',
            api_status: 'healthy',
            provider_statuses: [],
          },
        }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (path === '/api/v2/get_market_overview') {
        const body = JSON.parse(String(init?.body)) as { venue: string }
        return new Response(JSON.stringify({
          code: 2000,
          result: body.venue === 'all'
            ? { priced_asset_count: 1, index_updated_at: now }
            : { routable_asset_count: 1, index_updated_at: now },
        }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (path.endsWith('/status')) {
        return new Response(JSON.stringify({ state: 'ready' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (path.endsWith('/orderbook')) {
        return new Response(JSON.stringify({
          bids: [{ price: '1' }],
          asks: [{ price: '2' }],
        }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      throw new Error(`unexpected path ${path}`)
    })
    vi.stubGlobal('fetch', fetchMock)

    const snapshot = await getSystemStatus()

    expect(snapshot.source_mode).toBe('legacy')
    expect(snapshot.components.matching.state).toBe('live')
    expect(snapshot.components.outbox.state).toBe('unknown')
    expect(fetchMock).toHaveBeenCalledTimes(7)
  })

  it('does not hide a real system-status endpoint failure behind legacy probes', async () => {
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({
        code: 5000,
        message: 'system status failed',
      }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(getSystemStatus()).rejects.toThrow('system status failed')
    expect(fetchMock).toHaveBeenCalledOnce()
  })
})
