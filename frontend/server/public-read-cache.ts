import type { RuntimeCache } from '@vercel/functions'

export interface PublicReadCacheEntry {
  status: number
  body: Buffer
  contentType?: string
  vary?: string
  storedAt: number
  contract: BackendMarketContract
}

export interface BackendMarketContract {
  releaseCommit: string
  dataMode: string
  providerPolicy: string
  contractSchema: string
  snapshotSchema: string
	edgeReleaseCommit: string
	edgeDataMode: string
	edgeContractSchema: string
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
		private readonly staleForMs = 240_000,
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

function availableDecimal(
  value: unknown,
): value is { value: unknown; available: boolean } {
  if (!value || typeof value !== 'object') return false
  return typeof (value as { available?: unknown }).available === 'boolean'
}

/**
 * Cached dashboard rows contain source freshness calculated by PostgreSQL at
 * cache-write time. Fold the transport cache age back into those fields so a
 * last-good DEX route can never keep presenting itself as a current route.
 */
export function agePublicReadBody(
  pathname: string,
  body: Buffer,
  cacheAgeSeconds: number,
): Buffer {
  if (
    pathname !== '/api/v2/get_asset_dashboard' ||
    cacheAgeSeconds <= 0
  ) {
    return body
  }
  try {
    const envelope = JSON.parse(body.toString()) as {
      result?: Array<Record<string, unknown>>
		snapshot_id?: unknown
		snapshot_as_of?: unknown
		snapshot_schema?: unknown
    }
	// Snapshot responses are immutable database classifications. Transport
	// staleness is expressed by Age/Warning; rewriting only dashboard rows
	// would break overview/dashboard conservation for the same snapshot ID.
	if (
		typeof envelope.snapshot_id === 'string' &&
		/^snp_[0-9a-f]{32}$/.test(envelope.snapshot_id) &&
		Number(envelope.snapshot_as_of) > 0 &&
		envelope.snapshot_schema === 'qiu.market-snapshot.v1'
	) {
		return body
	}
    if (!Array.isArray(envelope.result)) return body
    for (const candidate of envelope.result) {
      if (!candidate || typeof candidate !== 'object') continue
      const row = candidate as Record<string, unknown>
      const rawSourceAge = row.freshness_age_seconds
      const sourceAge = Number(rawSourceAge)
      const hasSourceAge =
        rawSourceAge !== null &&
        rawSourceAge !== undefined &&
        rawSourceAge !== '' &&
        Number.isFinite(sourceAge) &&
        sourceAge >= 0
      const initialFreshness = String(row.freshness_status ?? '')
      const effectiveAge = hasSourceAge
        ? sourceAge + cacheAgeSeconds
        : null
      if (effectiveAge !== null) {
        row.freshness_age_seconds = effectiveAge
      }
      if (
        !hasSourceAge ||
        initialFreshness === 'unavailable' ||
        (effectiveAge !== null && effectiveAge > 300)
      ) {
        row.freshness_status = 'unavailable'
        row.available = false
      } else if (effectiveAge !== null && effectiveAge > 30) {
        row.freshness_status = 'stale'
      }

      const routeExpired =
        row.dex_route_available === true &&
        (
          !hasSourceAge ||
          initialFreshness === 'unavailable' ||
          (effectiveAge !== null && effectiveAge > 60)
        )
      if (!routeExpired) continue
      row.dex_route_available = false
      row.dex_route_count = 0
      row.available = false
      row.price_kind = 'unavailable'
      row.price_source = ''
      if (availableDecimal(row.price_usd)) {
        row.price_usd = { ...row.price_usd, available: false }
      }
      if (availableDecimal(row.change_24h_pct)) {
        row.change_24h_pct = {
          ...row.change_24h_pct,
          available: false,
        }
      }
      let hasReference = false
      if (
        availableDecimal(row.composite_price_usd) &&
        row.composite_price_usd.available
      ) {
        row.display_price_usd = row.composite_price_usd
        row.display_price_kind = 'composite_reference'
        row.display_available = true
        hasReference = true
      } else if (
        availableDecimal(row.market_reference_price_usd) &&
        row.market_reference_price_usd.available
      ) {
        row.display_price_usd = row.market_reference_price_usd
        row.display_price_kind = 'market_reference'
        row.display_available = true
        hasReference = true
      } else {
        row.display_price_usd = { value: null, available: false }
        row.display_price_kind = 'unavailable'
        row.display_available = false
      }
      // Route change and observation metadata cannot be reused for a fallback
      // reference source because the cached envelope has no source-specific
      // timestamp or 24h-change provenance.
      row.display_observed_at = 0
      row.display_change_24h_pct = { value: null, available: false }
      row.display_change_kind = 'unavailable'
      row.coverage_status = hasReference
        ? 'reference_only'
        : 'source_unavailable'
      row.coverage_reason = 'cached_route_expired'
    }
    return Buffer.from(JSON.stringify(envelope))
  } catch {
    return body
  }
}

interface RuntimePublicReadValue {
  schemaVersion: 2
  status: number
  bodyBase64: string
  contentType?: string
  vary?: string
  storedAt: number
  contract: BackendMarketContract
}

function isBackendMarketContract(value: unknown): value is BackendMarketContract {
  if (!value || typeof value !== 'object') return false
  const contract = value as Partial<BackendMarketContract>
  return (
    typeof contract.releaseCommit === 'string' &&
    typeof contract.dataMode === 'string' &&
    typeof contract.providerPolicy === 'string' &&
    typeof contract.contractSchema === 'string' &&
		typeof contract.snapshotSchema === 'string' &&
		typeof contract.edgeReleaseCommit === 'string' &&
		typeof contract.edgeDataMode === 'string' &&
		typeof contract.edgeContractSchema === 'string'
  )
}

function isRuntimePublicReadValue(value: unknown): value is RuntimePublicReadValue {
  if (!value || typeof value !== 'object') return false
  const entry = value as Partial<RuntimePublicReadValue>
  return (
    entry.schemaVersion === 2 &&
    Number.isInteger(entry.status) &&
    typeof entry.bodyBase64 === 'string' &&
    Number.isFinite(entry.storedAt) &&
    isBackendMarketContract(entry.contract)
  )
}

export class RuntimePublicReadCache {
  constructor(
    private readonly cache: RuntimeCache,
    private readonly freshForMs = 15_000,
		private readonly staleForMs = 240_000,
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
        contract: value.contract,
      },
      state: ageMs <= this.freshForMs ? 'fresh' : 'stale',
      ageSeconds: Math.floor(ageMs / 1000),
    }
  }

  async put(key: string, entry: PublicReadCacheEntry): Promise<boolean> {
    if (entry.body.byteLength > this.maximumEntryBytes) return false
    const value: RuntimePublicReadValue = {
      schemaVersion: 2,
      status: entry.status,
      bodyBase64: entry.body.toString('base64'),
      contentType: entry.contentType,
      vary: entry.vary,
      storedAt: entry.storedAt,
      contract: entry.contract,
    }
    await this.cache.set(key, value, {
      ttl: Math.ceil((this.freshForMs + this.staleForMs) / 1_000),
      tags: ['qiu-market-public-read'],
      name: 'Qiu Market public read',
    })
    return true
  }
}
