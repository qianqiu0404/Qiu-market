export const DASHBOARD_LAST_GOOD_TTL_MS = 5 * 60 * 1_000
const MAX_DASHBOARD_LAST_GOOD_ENTRIES = 64

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
