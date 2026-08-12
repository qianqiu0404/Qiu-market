export const DASHBOARD_LAST_GOOD_TTL_MS = 5 * 60 * 1_000
const MAX_DASHBOARD_LAST_GOOD_ENTRIES = 64
const PERSISTED_SCHEMA = 'qiu.market-dashboard-last-good.v1'
const SNAPSHOT_SCHEMA = 'qiu.market-snapshot.v1'
const DB_NAME = 'qiu-market-last-good-v1'
const STORE_NAME = 'dashboards'
const MAX_PERSISTED_BYTES = 1_500_000
const MAX_PERSISTED_ENTRIES = 64

export function configuredReleaseCommit(explicit?: string): string {
  const candidate = explicit ?? (typeof __QIU_MARKET_RELEASE_COMMIT__ === 'string'
    ? __QIU_MARKET_RELEASE_COMMIT__
    : '')
  return /^[0-9a-f]{40}$/.test(candidate)
    ? candidate
    : ''
}

interface PersistedDashboard<T> {
  schema: string
  releaseCommit: string
  queryKey: string
  venue: string
  storedAt: number
  value: T
}

export interface DashboardPersistence {
  get(key: string): Promise<unknown>
  put(key: string, value: unknown): Promise<void>
  delete(key: string): Promise<void>
  entries?(): Promise<Array<[string, unknown]>>
}

function indexedDBPersistence(): DashboardPersistence | null {
  if (typeof indexedDB === 'undefined') return null
  const database = new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1)
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE_NAME)) {
        request.result.createObjectStore(STORE_NAME)
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
  const transaction = async <T>(
    mode: IDBTransactionMode,
    operation: (store: IDBObjectStore) => IDBRequest<T>,
  ): Promise<T> => {
    const db = await database
    return await new Promise<T>((resolve, reject) => {
      const request = operation(db.transaction(STORE_NAME, mode).objectStore(STORE_NAME))
      request.onsuccess = () => resolve(request.result)
      request.onerror = () => reject(request.error)
    })
  }
  return {
    get: (key) => transaction('readonly', (store) => store.get(key)),
    put: async (key, value) => { await transaction('readwrite', (store) => store.put(value, key)) },
    delete: async (key) => { await transaction('readwrite', (store) => store.delete(key)) },
    entries: async () => {
      const db = await database
      return await new Promise<Array<[string, unknown]>>((resolve, reject) => {
        const request = db.transaction(STORE_NAME, 'readonly').objectStore(STORE_NAME).openCursor()
        const entries: Array<[string, unknown]> = []
        request.onsuccess = () => {
          const cursor = request.result
          if (!cursor) { resolve(entries); return }
          entries.push([String(cursor.key), cursor.value])
          cursor.continue()
        }
        request.onerror = () => reject(request.error)
      })
    },
  }
}

export function validPersistedSnapshot(
  value: unknown,
  expectedVenue: string,
): boolean {
  if (!value || typeof value !== 'object') return false
  const snapshot = value as Record<string, unknown>
  const overview = snapshot.overview as Record<string, unknown> | undefined
  if (!overview || snapshot.snapshot_schema !== SNAPSHOT_SCHEMA ||
    !/^snp_[0-9a-f]{32}$/.test(String(snapshot.snapshot_id ?? '')) ||
    Number(snapshot.snapshot_as_of) <= 0 || overview.venue !== expectedVenue ||
    overview.snapshot_schema !== SNAPSHOT_SCHEMA ||
    overview.snapshot_id !== snapshot.snapshot_id ||
    Number(overview.snapshot_as_of) !== Number(snapshot.snapshot_as_of)) return false
  const items = snapshot.items
  const total = Number(snapshot.total)
  if (!Array.isArray(items) || items.length > 50 || !Number.isInteger(total) ||
    total < items.length || total > 200) return false
  const assetIDs = new Set<string>()
  for (const raw of items) {
    if (!raw || typeof raw !== 'object') return false
    const row = raw as Record<string, unknown>
    const assetID = typeof row.asset_id === 'string' ? row.asset_id.trim() : ''
    if (!assetID || assetIDs.has(assetID)) return false
    assetIDs.add(assetID)
    if (row.snapshot_id != null && row.snapshot_id !== snapshot.snapshot_id) return false
    if (row.snapshot_schema != null && row.snapshot_schema !== SNAPSHOT_SCHEMA) return false
    if (row.venue != null && row.venue !== expectedVenue) return false
  }
  const assetCount = Number(overview.asset_count)
  const fresh = Number(overview.fresh_asset_count)
  const stale = Number(overview.stale_asset_count)
  const unavailable = Number(overview.unavailable_asset_count)
  const priced = Number(overview.priced_asset_count)
  const displayed = Number(overview.displayed_asset_count)
  const unpriced = Number(overview.unpriced_asset_count)
  const single = Number(overview.single_venue_priced_asset_count)
  const multi = Number(overview.multi_venue_priced_asset_count)
  return Number.isInteger(assetCount) && assetCount >= 1 && assetCount <= 200 &&
    [fresh, stale, unavailable].every((count) => Number.isInteger(count) && count >= 0) &&
    fresh + stale + unavailable === assetCount &&
    priced === fresh + stale && displayed === priced && unpriced === unavailable &&
    [single, multi].every((count) => Number.isInteger(count) && count >= 0) &&
    single + multi === priced
}

function persistedKey(releaseCommit: string, queryKey: string): string {
  return `${releaseCommit}:${queryKey}`
}

export async function readPersistedDashboard<T>(
  queryKey: string,
  venue: string,
  now = Date.now(),
  persistence: DashboardPersistence | null = indexedDBPersistence(),
  releaseOverride?: string,
): Promise<DashboardLastGood<T> | null> {
  const releaseCommit = configuredReleaseCommit(releaseOverride)
  if (!releaseCommit || !persistence) return null
  const key = persistedKey(releaseCommit, queryKey)
  try {
    const raw = await persistence.get(key)
    if (!raw || typeof raw !== 'object') return null
    const entry = raw as PersistedDashboard<T>
    if (entry.schema !== PERSISTED_SCHEMA || entry.releaseCommit !== releaseCommit ||
      entry.queryKey !== queryKey || entry.venue !== venue ||
      !isDashboardLastGoodCurrent(entry.storedAt, now) ||
      !entry.value || typeof entry.value !== 'object' ||
      (entry.value as { queryKey?: unknown }).queryKey !== queryKey ||
      !validPersistedSnapshot((entry.value as { data?: unknown }).data, venue)) {
      await persistence.delete(key)
      return null
    }
    return { value: entry.value, storedAt: entry.storedAt }
  } catch {
    return null
  }
}

export async function writePersistedDashboard<T>(
  queryKey: string,
  venue: string,
  value: T,
  now = Date.now(),
  persistence: DashboardPersistence | null = indexedDBPersistence(),
  releaseOverride?: string,
): Promise<boolean> {
  const releaseCommit = configuredReleaseCommit(releaseOverride)
  if (!releaseCommit || !persistence || !value || typeof value !== 'object' ||
    (value as { queryKey?: unknown }).queryKey !== queryKey ||
    !validPersistedSnapshot((value as { data?: unknown }).data, venue)) return false
  const entry: PersistedDashboard<T> = {
    schema: PERSISTED_SCHEMA, releaseCommit, queryKey, venue, storedAt: now, value,
  }
  try {
    const encoded = JSON.stringify(entry)
    if (new TextEncoder().encode(encoded).byteLength > MAX_PERSISTED_BYTES) return false
    await persistence.put(persistedKey(releaseCommit, queryKey), entry)
    if (persistence.entries) {
      const entries = await persistence.entries()
      const ordered = entries.sort((left, right) => {
        const leftAt = Number((left[1] as { storedAt?: unknown })?.storedAt)
        const rightAt = Number((right[1] as { storedAt?: unknown })?.storedAt)
        return rightAt - leftAt
      })
      for (const [key] of ordered.slice(MAX_PERSISTED_ENTRIES)) await persistence.delete(key)
    }
    return true
  } catch {
    return false
  }
}

export interface DashboardLastGood<T> {
  value: T
  storedAt: number
}

export function isDashboardLastGoodCurrent(
  storedAt: number,
  now = Date.now(),
): boolean {
  return storedAt > 0 &&
    now >= storedAt &&
    now - storedAt <= DASHBOARD_LAST_GOOD_TTL_MS
}

export function readDashboardLastGood<T>(
  cache: Map<string, DashboardLastGood<T>>,
  queryKey: string,
  now = Date.now(),
): DashboardLastGood<T> | null {
  const entry = cache.get(queryKey)
  if (!entry) return null
  if (!isDashboardLastGoodCurrent(entry.storedAt, now)) {
    cache.delete(queryKey)
    return null
  }
  return entry
}

export function writeDashboardLastGood<T>(
  cache: Map<string, DashboardLastGood<T>>,
  queryKey: string,
  value: T,
  now = Date.now(),
): DashboardLastGood<T> {
  const entry = { value, storedAt: now }
  cache.delete(queryKey)
  cache.set(queryKey, entry)
  while (cache.size > MAX_DASHBOARD_LAST_GOOD_ENTRIES) {
    const oldest = cache.keys().next().value
    if (typeof oldest !== 'string') break
    cache.delete(oldest)
  }
  return entry
}

export function isCurrentDashboardRequest(
  requestGeneration: number,
  currentGeneration: number,
  requestQueryKey: string,
  currentQueryKey: string,
): boolean {
  return requestGeneration === currentGeneration && requestQueryKey === currentQueryKey
}
