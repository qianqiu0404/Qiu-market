import { describe, expect, it } from 'vitest'
import {
  DASHBOARD_LAST_GOOD_TTL_MS,
  isDashboardLastGoodCurrent,
  isCurrentDashboardRequest,
  readDashboardLastGood,
  writeDashboardLastGood,
} from './dashboard-cache'

describe('dashboard last-good cache', () => {
  it('expires a visible snapshot after five minutes even when the query never changes', () => {
    expect(isDashboardLastGoodCurrent(1_000, 1_000 + DASHBOARD_LAST_GOOD_TTL_MS)).toBe(true)
    expect(isDashboardLastGoodCurrent(1_000, 1_001 + DASHBOARD_LAST_GOOD_TTL_MS)).toBe(false)
    expect(isDashboardLastGoodCurrent(0, 1_000)).toBe(false)
  })

  it('returns only the same query and expires after five minutes', () => {
    const cache = new Map()
    writeDashboardLastGood(cache, 'binance:assets', { venue: 'binance' }, 1_000)
    expect(readDashboardLastGood(cache, 'binance:assets', 1_100)?.value).toEqual({ venue: 'binance' })
    expect(readDashboardLastGood(cache, 'bybit:assets', 1_100)).toBeNull()
    expect(readDashboardLastGood(
      cache,
      'binance:assets',
      1_000 + DASHBOARD_LAST_GOOD_TTL_MS + 1,
    )).toBeNull()
  })

  it('rejects a late response from an old venue generation', () => {
    expect(isCurrentDashboardRequest(8, 9, 'binance', 'bybit')).toBe(false)
    expect(isCurrentDashboardRequest(9, 9, 'binance', 'bybit')).toBe(false)
    expect(isCurrentDashboardRequest(9, 9, 'bybit', 'bybit')).toBe(true)
  })
})
