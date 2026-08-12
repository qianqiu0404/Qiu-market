import { describe, expect, it } from 'vitest'
import {
  DASHBOARD_LAST_GOOD_TTL_MS,
  isDashboardLastGoodCurrent,
  isCurrentDashboardRequest,
  readPersistedDashboard,
  readDashboardLastGood,
  writePersistedDashboard,
  writeDashboardLastGood,
} from './dashboard-cache'

const release = '19928325f9a1104d1dd3505a004dffb9fe52a714'

function validSnapshot(venue = 'all') {
  const snapshotID = 'snp_00000000000000000000000000000001'
  return {
    queryKey: `${venue}:assets`,
    data: {
      snapshot_id: snapshotID, snapshot_as_of: 1785196800000,
      snapshot_schema: 'qiu.market-snapshot.v1', items: [], total: 0,
      overview: {
        venue, snapshot_id: snapshotID, snapshot_as_of: 1785196800000,
        snapshot_schema: 'qiu.market-snapshot.v1', asset_count: 2,
        priced_asset_count: 1, displayed_asset_count: 1, unpriced_asset_count: 1,
        fresh_asset_count: 1, stale_asset_count: 0, unavailable_asset_count: 1,
        single_venue_priced_asset_count: 1, multi_venue_priced_asset_count: 0,
      },
    },
  }
}

function memoryPersistence() {
  const values = new Map<string, unknown>()
  return {
    values,
    persistence: {
      get: async (key: string) => values.get(key),
      put: async (key: string, value: unknown) => { values.set(key, value) },
      delete: async (key: string) => { values.delete(key) },
      entries: async () => [...values.entries()],
    },
  }
}

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

  it('persists and restores only an exact release/query/venue conserved snapshot', async () => {
    const store = memoryPersistence()
    const value = validSnapshot()
    expect(await writePersistedDashboard('all:assets', 'all', value, 1_000,
      store.persistence, release)).toBe(true)
    expect((await readPersistedDashboard('all:assets', 'all', 1_100,
      store.persistence, release))?.value).toEqual(value)
    expect(await readPersistedDashboard('all:assets', 'binance', 1_100,
      store.persistence, release)).toBeNull()
    expect(await readPersistedDashboard('all:assets', 'all', 1_100,
      store.persistence, '2'.repeat(40))).toBeNull()
  })

  it('rejects a query key mismatch before writing persistent state', async () => {
    const store = memoryPersistence()
    const value = validSnapshot()
    expect(await writePersistedDashboard('binance:assets', 'all', value, 1_000,
      store.persistence, release)).toBe(false)
    expect(store.values.size).toBe(0)
  })

  it('fails closed and removes stale or tampered snapshot conservation', async () => {
    const store = memoryPersistence()
    const value = validSnapshot()
    await writePersistedDashboard('all:assets', 'all', value, 1_000,
      store.persistence, release)
    const [key, raw] = [...store.values.entries()][0]
    const tampered = structuredClone(raw) as { value: ReturnType<typeof validSnapshot> }
    tampered.value.data.overview.unavailable_asset_count = 0
    store.values.set(key, tampered)
    expect(await readPersistedDashboard('all:assets', 'all', 1_100,
      store.persistence, release)).toBeNull()
    expect(store.values.size).toBe(0)

    await writePersistedDashboard('all:assets', 'all', value, 1_000,
      store.persistence, release)
    expect(await readPersistedDashboard('all:assets', 'all',
      1_001 + DASHBOARD_LAST_GOOD_TTL_MS, store.persistence, release)).toBeNull()
  })

  it.each([
    ['displayed count', 'displayed_asset_count', 0],
    ['unpriced count', 'unpriced_asset_count', 0],
    ['venue support count', 'single_venue_priced_asset_count', 0],
  ])('rejects a tampered %s', async (_name, field, value) => {
    const store = memoryPersistence()
    const snapshot = validSnapshot()
    ;(snapshot.data.overview as Record<string, unknown>)[field] = value
    expect(await writePersistedDashboard(snapshot.queryKey, 'all', snapshot, 1_000,
      store.persistence, release)).toBe(false)
  })

  it('rejects duplicate/empty rows and bounds persisted entries and bytes', async () => {
    const store = memoryPersistence()
    const invalid = validSnapshot()
    invalid.data.items = [
      { asset_id: 'asset-btc' }, { asset_id: 'asset-btc' },
    ] as never[]
    invalid.data.total = 2
    expect(await writePersistedDashboard('invalid', 'all', invalid, 1_000,
      store.persistence, release)).toBe(false)

    for (let index = 0; index < 65; index += 1) {
      const value = validSnapshot()
      value.queryKey = `query-${index}`
      await writePersistedDashboard(value.queryKey, 'all', value, 1_000 + index,
        store.persistence, release)
    }
    expect(store.values.size).toBe(64)

    const oversized = validSnapshot() as ReturnType<typeof validSnapshot> & { padding?: string }
    oversized.padding = 'x'.repeat(1_500_000)
    expect(await writePersistedDashboard('oversized', 'all', oversized, 2_000,
      store.persistence, release)).toBe(false)
  })
})
