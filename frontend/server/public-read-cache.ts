import type { RuntimeCache } from '@vercel/functions'

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
  private totalBytes = 0

  constructor(
    private readonly freshForMs = 15_000,
    private readonly staleForMs = 300_000,
    private readonly maximumEntries = 128,
    private readonly maximumBytes = 16 << 20,
    private readonly maximumEntryBytes = 1_400_000,
  ) {}

  lookup(key: string, now = Date.now()): PublicReadCacheLookup | undefined {
    const entry = this.entries.get(key)
    if (!entry) return undefined

    const ageMs = Math.max(0, now - entry.storedAt)
    if (ageMs > this.freshForMs + this.staleForMs) {
      this.delete(key)
      return undefined
    }
    return {
      entry,
      state: ageMs <= this.freshForMs ? 'fresh' : 'stale',
      ageSeconds: Math.floor(ageMs / 1000),
    }
  }

  put(key: string, entry: PublicReadCacheEntry): boolean {
    if (entry.body.byteLength > this.maximumEntryBytes) return false
    this.delete(key)
    this.entries.set(key, {
      ...entry,
      body: Buffer.from(entry.body),
    })
    this.totalBytes += entry.body.byteLength
    while (
      this.entries.size > this.maximumEntries ||
      this.totalBytes > this.maximumBytes
    ) {
      const oldestKey = this.entries.keys().next().value
      if (typeof oldestKey !== 'string') break
      this.delete(oldestKey)
    }
    return this.entries.has(key)
  }

  private delete(key: string): void {
    const entry = this.entries.get(key)
    if (!entry) return
    this.totalBytes = Math.max(0, this.totalBytes - entry.body.byteLength)
    this.entries.delete(key)
  }
}

const PUBLIC_MARKET_READ_PATHS = new Set([
  '/api/v1/get_support_assets',
  '/api/v1/get_market_dashboard',
  '/api/v1/get_asset_dashboard',
  '/api/v1/get_market_insights',
  '/api/v1/get_exchanges',
  '/api/v1/get_symbols',
  '/api/v1/get_klines',
  '/api/v1/get_market_sparklines',
  '/api/v1/get_system_overview',
  '/api/v1/get_fiat_rates',
  '/api/v1/get_top_movers',
  '/api/v1/get_kline_analytics',
  '/api/v1/get_asset_momentum',
  '/api/v2/get_market_overview',
  '/api/v2/get_asset_dashboard',
  '/api/v2/get_asset_markets',
  '/api/v2/get_asset_venues',
  '/api/v2/get_provider_catalog_audit',
])

export function isPublicMarketRead(method: string, pathname: string): boolean {
  return method === 'POST' && PUBLIC_MARKET_READ_PATHS.has(pathname)
}

function stableJSON(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableJSON)
  if (value === null || typeof value !== 'object') return value

  const source = value as Record<string, unknown>
  return Object.fromEntries(
    Object.keys(source)
      .sort()
      .map((key) => [key, stableJSON(source[key])]),
  )
}

export function publicReadCachePayload(body: Buffer): Buffer {
  try {
    const parsed = JSON.parse(body.toString())
    if (
      parsed !== null &&
      typeof parsed === 'object' &&
      !Array.isArray(parsed)
    ) {
      delete (parsed as Record<string, unknown>).consumer_token
    }
    return Buffer.from(JSON.stringify(stableJSON(parsed)))
  } catch {
    return body
  }
}

interface RuntimePublicReadValue {
  schemaVersion: 1
  status: number
  bodyBase64: string
  contentType?: string
  vary?: string
  storedAt: number
}

function isRuntimePublicReadValue(value: unknown): value is RuntimePublicReadValue {
  if (!value || typeof value !== 'object') return false
  const entry = value as Partial<RuntimePublicReadValue>
  return (
    entry.schemaVersion === 1 &&
    Number.isInteger(entry.status) &&
    typeof entry.bodyBase64 === 'string' &&
    Number.isFinite(entry.storedAt)
  )
}

export class RuntimePublicReadCache {
  constructor(
    private readonly cache: RuntimeCache,
    private readonly freshForMs = 15_000,
    private readonly staleForMs = 300_000,
    private readonly maximumEntryBytes = 1_400_000,
  ) {}

  async lookup(
    key: string,
    now = Date.now(),
  ): Promise<PublicReadCacheLookup | undefined> {
    const value = await this.cache.get(key)
    if (!isRuntimePublicReadValue(value)) return undefined

    const body = Buffer.from(value.bodyBase64, 'base64')
    if (body.byteLength > this.maximumEntryBytes) {
      await this.cache.delete(key)
      return undefined
    }
    const ageMs = Math.max(0, now - value.storedAt)
    if (ageMs > this.freshForMs + this.staleForMs) {
      await this.cache.delete(key)
      return undefined
    }
    return {
      entry: {
        status: value.status,
        body,
        contentType: value.contentType,
        vary: value.vary,
        storedAt: value.storedAt,
      },
      state: ageMs <= this.freshForMs ? 'fresh' : 'stale',
      ageSeconds: Math.floor(ageMs / 1000),
    }
  }

  async put(key: string, entry: PublicReadCacheEntry): Promise<boolean> {
    if (entry.body.byteLength > this.maximumEntryBytes) return false
    const value: RuntimePublicReadValue = {
      schemaVersion: 1,
      status: entry.status,
      bodyBase64: entry.body.toString('base64'),
      contentType: entry.contentType,
      vary: entry.vary,
      storedAt: entry.storedAt,
    }
    await this.cache.set(key, value, {
      ttl: Math.ceil((this.freshForMs + this.staleForMs) / 1_000),
      tags: ['qiu-market-public-read'],
      name: 'Qiu Market public read',
    })
    return true
  }
}
