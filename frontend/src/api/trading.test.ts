import { afterEach, describe, expect, it, vi } from 'vitest'
import { eventSocketURL, tradingAPI } from './trading'

describe('trading API', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
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
})
