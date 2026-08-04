import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  eventSocketURL,
  normalizeRecoveryStatus,
  TRADING_WRITE_TIMEOUT_MS,
  tradingAPI,
  tradingEventMode,
} from './trading'

describe('trading API', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
    vi.useRealTimers()
  })

  it('preserves stable empty order-book arrays', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      market_id: 'BTC-USDT',
      sequence: '0',
      bids: [],
      asks: [],
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })))
    const book = await tradingAPI.orderBook()
    expect(book.bids).toEqual([])
    expect(book.asks).toEqual([])
  })

  it('surfaces the bounded JSON error message', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: 'trading_unavailable',
      message: 'virtual trading is temporarily unavailable',
    }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    })))
    await expect(tradingAPI.status()).rejects.toThrow(
      'virtual trading is temporarily unavailable',
    )
  })

  it('uses the configured direct WSS origin and preserves the event cursor', () => {
    vi.stubEnv('VITE_TRADING_WS_ORIGIN', 'https://qiu-market.example.ts.net')
    expect(eventSocketURL('opaque-ticket', {
      market_id: 'BTC-USDT',
      sequence: '42',
      event_index: 3,
    })).toBe(
      'wss://qiu-market.example.ts.net/api/v1/trading/events/ws' +
      '?ticket=opaque-ticket&sequence=42&event_index=3',
    )
  })

  it('marks a write transport failure as submitted unknown without retrying', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('network disconnected'))
    vi.stubGlobal('fetch', fetchMock)
    const request = tradingAPI.submit({
      client_order_id: 'stable-order-id',
      side: 'buy',
      type: 'limit',
      time_in_force: 'gtc',
      price: '60000',
      quantity: '0.001',
    })
    await expect(request).rejects.toMatchObject({
      code: 'network_error',
      status: 0,
      uncertain: true,
    })
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('times out a write once and exposes an unknown outcome for same-ID reconcile', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn((_url: string | URL | Request, init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => {
          reject(new DOMException('aborted', 'AbortError'))
        }, { once: true })
      }))
    vi.stubGlobal('fetch', fetchMock)

    const request = tradingAPI.fund(
      'fund-stable-id',
      'USDT',
      '100',
      'github:beneficiary',
    )
    const rejection = expect(request).rejects.toMatchObject({
      code: 'request_timeout',
      status: 0,
      uncertain: true,
    })
    await vi.advanceTimersByTimeAsync(TRADING_WRITE_TIMEOUT_MS)
    await rejection

    expect(fetchMock).toHaveBeenCalledOnce()
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toMatchObject({
      request_id: 'fund-stable-id',
      account_id: 'github:beneficiary',
    })
  })

  it('supports explicit same-origin polling fallback', () => {
    vi.stubEnv('VITE_TRADING_EVENT_MODE', 'polling')
    expect(tradingEventMode()).toBe('polling')
  })

  it('normalizes public recovery proof without treating numeric sequences as arithmetic', () => {
    const status = normalizeRecoveryStatus({
      schema_version: 1,
      market_id: 'BTC-USDT',
      epoch_id: 'epoch-1',
      phase: 'transport_warmup',
      runtime_sequence: '9007199254740993',
      state_hash: 'a'.repeat(64),
      ledger_balanced: true,
      event_continuous: true,
      projection_caught_up: true,
      outbox_caught_up: true,
      transport_healthy: false,
      writes_enabled: false,
      continuity_uncertain: true,
      continuity_error: 'store save result is uncertain',
      version: '7',
    })
    expect(status.proof.runtime_sequence).toBe('9007199254740993')
    expect(status.phase).toBe('transport_warmup')
    expect(status.writes_enabled).toBe(false)
    expect(status.continuity_uncertain).toBe(true)
    expect(status.continuity_error).toBe('store save result is uncertain')
  })

  it('rejects an already unsafe numeric recovery sequence instead of stringifying it', () => {
    expect(() => normalizeRecoveryStatus({
      schema_version: 1,
      market_id: 'BTC-USDT',
      epoch_id: 'epoch-unsafe',
      phase: 'read_only',
      runtime_sequence: 9_007_199_254_740_993,
      writes_enabled: false,
      continuity_uncertain: false,
      version: 1,
    })).toThrow('runtime_sequence is not a safe decimal value')
  })

  it('binds recovery evidence to schema one, BTC-USDT and a positive version', () => {
    const valid = {
      schema_version: 1,
      market_id: 'BTC-USDT',
      epoch_id: 'epoch-bound',
      phase: 'read_only',
      runtime_sequence: '0',
      state_hash: 'a'.repeat(64),
      ledger_balanced: true,
      event_continuous: true,
      projection_caught_up: true,
      outbox_caught_up: true,
      transport_healthy: false,
      writes_enabled: false,
      continuity_uncertain: false,
      version: '1',
    }
    expect(() => normalizeRecoveryStatus({ ...valid, schema_version: 2 }))
      .toThrow('status is malformed')
    expect(() => normalizeRecoveryStatus({ ...valid, market_id: 'ETH-USDT' }))
      .toThrow('status is malformed')
    expect(() => normalizeRecoveryStatus({ ...valid, version: '0' }))
      .toThrow('version must be a positive decimal value')
    expect(() => normalizeRecoveryStatus({ ...valid, version: undefined }))
      .toThrow('version is not a safe decimal value')
    expect(() => normalizeRecoveryStatus({ ...valid, runtime_sequence: undefined }))
      .toThrow('runtime_sequence is not a safe decimal value')
    expect(() => normalizeRecoveryStatus({ ...valid, continuity_uncertain: undefined }))
      .toThrow('status is malformed')
  })

  it('rejects an unsafe numeric recovery version symmetrically', () => {
    expect(() => normalizeRecoveryStatus({
      schema_version: 1,
      market_id: 'BTC-USDT',
      epoch_id: 'epoch-unsafe-version',
      phase: 'read_only',
      runtime_sequence: '1',
      writes_enabled: false,
      continuity_uncertain: false,
      version: 9_007_199_254_740_993,
    })).toThrow('version is not a safe decimal value')
  })

  it('allows recovery 404 compatibility only after a trusted disabled capability', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        code: 'not_found',
        message: 'not found',
      }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        github_oauth_enabled: true,
        local_login_enabled: false,
        recovery_gate_enabled: false,
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(tradingAPI.recoveryStatus()).resolves.toMatchObject({
      supported: false,
      phase: 'not_enabled',
      writes_enabled: false,
    })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('fails closed when recovery 404 has no explicit disabled capability', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('{}', {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        github_oauth_enabled: true,
        local_login_enabled: false,
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(tradingAPI.recoveryStatus()).rejects.toMatchObject({ status: 404 })
  })

  it('treats recovery_in_progress as a definite server rejection, not unknown', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      code: 'recovery_in_progress',
      message: 'writes are blocked',
    }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    })))
    await expect(tradingAPI.submit({ client_order_id: 'stable-id' })).rejects.toMatchObject({
      code: 'recovery_in_progress',
      uncertain: false,
    })
  })
})
