export interface PublicReadCacheEntry {
  status: number
  body: Buffer
  contentType?: string
  vary?: string
  storedAt: number
}

export interface PublicReadCacheLookup {
  entry: PublicReadCacheEntry
  state: 'fresh' | 'stale'
  ageSeconds: number
}

export class PublicReadCache {
  private readonly entries = new Map<string, PublicReadCacheEntry>()

  constructor(
    private readonly freshForMs = 15_000,
    private readonly staleForMs = 300_000,
    private readonly maximumEntries = 128,
  ) {}

  lookup(key: string, now = Date.now()): PublicReadCacheLookup | undefined {
    const entry = this.entries.get(key)
    if (!entry) return undefined

    const ageMs = Math.max(0, now - entry.storedAt)
    if (ageMs > this.freshForMs + this.staleForMs) {
      this.entries.delete(key)
      return undefined
    }
    return {
      entry,
      state: ageMs <= this.freshForMs ? 'fresh' : 'stale',
      ageSeconds: Math.floor(ageMs / 1000),
    }
  }

  put(key: string, entry: PublicReadCacheEntry): void {
    this.entries.delete(key)
    this.entries.set(key, {
      ...entry,
      body: Buffer.from(entry.body),
    })
    while (this.entries.size > this.maximumEntries) {
      const oldestKey = this.entries.keys().next().value
      if (typeof oldestKey !== 'string') break
      this.entries.delete(oldestKey)
    }
  }
}

export function isPublicMarketRead(method: string, pathname: string): boolean {
  return (
    method === 'POST' &&
    /^\/api\/v[12]\/get_[a-z0-9_]+$/.test(pathname)
  )
}
