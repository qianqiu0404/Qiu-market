import { afterEach, describe, expect, it, vi } from 'vitest'
import { tradingAPI } from './trading'

describe('trading API', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
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
})
