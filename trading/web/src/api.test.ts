import { afterEach, describe, expect, it, vi } from 'vitest'
import { eventSocketURL, tradingAPI } from './api'

afterEach(() => {
  document.cookie = 's78_trading_csrf=; Max-Age=0; Path=/'
  vi.unstubAllGlobals()
})

describe('trading browser contract', () => {
  it('uses the one-time ticket and durable cursor in a same-origin WebSocket URL', () => {
    expect(eventSocketURL('ticket-1', {
      market_id: 'BTC-USDT',
      sequence: '42',
      event_index: 3,
    })).toBe(
      'ws://127.0.0.1:5175/api/v1/trading/events/ws?' +
      'ticket=ticket-1&sequence=42&event_index=3',
    )
  })

  it('sends CSRF on writes and never sends an account id for cancellation', async () => {
    document.cookie = 's78_trading_csrf=csrf-1; Path=/'
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    await tradingAPI.cancel('order-1', 'cancel-1')

    expect(fetchMock).toHaveBeenCalledOnce()
    const [path, options] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/trading/orders/order-1/cancel')
    expect(options.credentials).toBe('same-origin')
    expect(new Headers(options.headers).get('X-CSRF-Token')).toBe('csrf-1')
    expect(JSON.parse(String(options.body))).toEqual({ request_id: 'cancel-1' })
  })
})
