import { describe, expect, it } from 'vitest'
import { vi } from 'vitest'
import {
  otherMarketVenues,
  runSequentialDashboardPrefetch,
  shouldContinueDashboardPrefetch,
} from './dashboard-prefetch'

describe('dashboard idle prefetch boundary', () => {
  it('returns the other seven venues in stable sequential order', () => {
    expect(otherMarketVenues('all')).toEqual([
      'binance', 'coinbase', 'bybit', 'okx', 'hyperliquid', 'uniswap', 'pancakeswap',
    ])
  })

  it('aborts for hidden, offline, save-data, or user interaction', () => {
    expect(shouldContinueDashboardPrefetch({
      hidden: false, online: true, saveData: false, interacted: false,
    })).toBe(true)
    for (const blocked of [
      { hidden: true, online: true, saveData: false, interacted: false },
      { hidden: false, online: false, saveData: false, interacted: false },
      { hidden: false, online: true, saveData: true, interacted: false },
      { hidden: false, online: true, saveData: false, interacted: true },
    ]) expect(shouldContinueDashboardPrefetch(blocked)).toBe(false)
  })

  it('runs with max concurrency one and writes only through the persistence boundary', async () => {
    let active = 0
    let maxActive = 0
    const visible = { venue: 'all', value: 'unchanged' }
    const persist = vi.fn(async () => true)
    const completed = await runSequentialDashboardPrefetch(['binance', 'coinbase', 'okx'], {
      signal: new AbortController().signal,
      shouldContinue: () => true,
      load: async (venue) => {
        active += 1
        maxActive = Math.max(maxActive, active)
        await Promise.resolve()
        active -= 1
        return { venue }
      },
      persist,
    })
    expect(completed).toEqual(['binance', 'coinbase', 'okx'])
    expect(maxActive).toBe(1)
    expect(persist).toHaveBeenCalledTimes(3)
    expect(visible).toEqual({ venue: 'all', value: 'unchanged' })
  })

  it('propagates a query abort signal and does not persist its late result', async () => {
    const controller = new AbortController()
    let release!: () => void
    const pending = new Promise<void>((resolve) => { release = resolve })
    const persist = vi.fn(async () => true)
    const run = runSequentialDashboardPrefetch(['binance', 'coinbase'], {
      signal: controller.signal,
      shouldContinue: () => true,
      load: async (venue) => { await pending; return { venue } },
      persist,
    })
    controller.abort()
    release()
    await expect(run).resolves.toEqual([])
    expect(persist).not.toHaveBeenCalled()
  })
})
