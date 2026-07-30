import { request } from './common'

/* ===== Response types ===== */

export interface SystemOverview {
  crawler_status: string
  dex_status: string
  dw_status: string
  rpc_status: string
  redis_status: string
  database_status: string
  worker_status: string
  api_status: string
  market_count: number
  asset_count: number
  symbol_count: number
  exchange_count: number
  total_market_cap: number
  total_volume: number
  updated_at: number
  data_delay_seconds: number
  provider_statuses: ProviderStatusItem[]
  storage: StorageStatus
}

export interface StorageStatus {
  database_bytes: number
  kline_table_bytes: number
  kline_heap_bytes: number
  kline_index_bytes: number
  kline_estimated_rows: number
  disk_free_bytes: number
  disk_state: 'healthy' | 'warning' | 'critical' | 'unknown' | string
  retention_last_started_at: number
  retention_last_success_at: number
  retention_last_error: string
  retention_deleted_rows: Record<string, number>
  kline_intervals: Array<{
    interval: string
    oldest_at: number
    newest_at: number
  }>
}

export interface ProviderStatusItem {
  provider: string
  status: string
  operational_status: string
  primary_source_key: string
  source_count: number
  failing_source_count: number
  last_attempt_at: number
  last_success_at: number
  last_source_time: number
  consecutive_failures: number
  last_error_class: string
  rollout_mode: string
  rank_limit: number
  min_soak_until: number
  next_retry_at: number
  attempt_count: number
  success_count: number
  success_rate_pct: string
  observation_started_at: number
  readiness_not_before: number
  rollout_ready: boolean
  rollout_blockers: string[]
  received_count: number
  matched_asset_count: number
  price_available_count: number
  change_available_count: number
  local_preview_enabled: boolean
  preview_source_key: string
  preview_covered_count: number
  selection_version: number
  selection_target_count: number
  selection_count: number
  selection_candidate_count: number
  selection_generated_at: number
  feed_mode: string
  kline_status: string
  kline_market_count: number
  kline_candle_count: number
  kline_last_success_at: number
  sources: ProviderSourceStatusItem[]
}

export interface ProviderSourceStatusItem {
  source_key: string
  capability: string
  status: string
  last_attempt_at: number
  last_success_at: number
  last_source_time: number
  next_retry_at: number
  consecutive_failures: number
  attempt_count: number
  success_count: number
  success_rate_pct: string
  last_error_class: string
  received_count: number
  matched_asset_count: number
  written_count: number
}

export interface AvailableDecimal {
  value: number | null
  available: boolean
}

export interface MarketPriceFact {
  price_usd: AvailableDecimal
  change_24h_pct: AvailableDecimal
  turnover_24h_usd: AvailableDecimal
  available: boolean
  kind: string
  source: string
  source_time: number
  observed_at: number
  last_success_at: number
  freshness_status: string
  freshness_age_seconds: number
  quality: string
  contributor_count: number
  contributors: string[]
  version: number
}

export interface MarketOverviewV2 {
  global_market_cap_usd: AvailableDecimal
  covered_spot_volume_24h_usd: AvailableDecimal
  btc_dominance_pct: AvailableDecimal
  asset_count: number
  advancers: number
  decliners: number
  flat: number
  unknown: number
  advance_ratio_pct: AvailableDecimal
  provider_updated_at: number
  index_updated_at: number
  venue: MarketVenue
  ranked_asset_count: number
  top50_universe_count: number
  eligible_asset_count: number
  published_asset_count: number
  priced_asset_count: number
  displayed_asset_count: number
  routable_asset_count: number
  reference_only_asset_count: number
  unpriced_asset_count: number
  change_available_count: number
  contributing_provider_count: number
  single_venue_priced_asset_count: number
  multi_venue_priced_asset_count: number
  coverage_ratio_pct: AvailableDecimal
  display_coverage_ratio_pct: AvailableDecimal
  local_preview_enabled: boolean
  preview_source_key: string
  preview_covered_count: number
  universe: string
  selection_version: number
}

export type AssetFilter = 'assets' | 'gainers' | 'losers'
export type MarketVenue =
  | 'all'
  | 'binance'
  | 'coinbase'
  | 'bybit'
  | 'okx'
  | 'hyperliquid'
  | 'uniswap'
  | 'pancakeswap'

export interface AssetDashboardV2Item {
  rank: number | null
  selection_version: number
  selection_rank: number
  asset_id: string
  asset_symbol: string
  asset_name: string
  logo: string
  price_usd: AvailableDecimal
  composite_price_usd: AvailableDecimal
  market_reference_price_usd: AvailableDecimal
  display_price_usd: AvailableDecimal
  display_price_kind: 'dex_route' | 'composite_reference' | 'market_reference' | 'unavailable' | string
  display_change_24h_pct: AvailableDecimal
  display_change_kind: string
  display_available: boolean
  display_observed_at: number
  dex_route_available: boolean
  venue_price: MarketPriceFact
  dex_route_price: MarketPriceFact
  display_price: MarketPriceFact
  change_24h_pct: AvailableDecimal
  market_cap_usd: AvailableDecimal
  covered_turnover_24h_usd: AvailableDecimal
  circulating_supply: AvailableDecimal
  spot_market_count: number
  perp_market_count: number
  dex_route_count: number
  contributor_count: number
  priced_venue_count: number
  confidence: string
  quality: string
  price_kind: string
  price_source: string
  coverage_status: string
  coverage_reason: string
  freshness_status: string
  freshness_age_seconds: number
  last_attempt_at: number
  last_success_at: number
  last_error_class: string
  available: boolean
  source_time: number
  observed_at: number
  index_updated_at: number
  provider_updated_at: number
  sparkline_available: boolean
}

export interface MarketPriceTick {
  asset_id: string
  provider: string
  price_kind: string
  price_usd: AvailableDecimal
  change_24h_pct: AvailableDecimal
  turnover_24h_usd: AvailableDecimal
  venue_price: MarketPriceFact
  dex_route_price: MarketPriceFact
  display_price: MarketPriceFact
  available: boolean
  freshness_status: string
  freshness_age_seconds: number
  source_time: number
  observed_at: number
  last_success_at: number
  version: number
}

export interface MarketPriceTickSnapshot {
  venue: MarketVenue
  server_time: number
  items: MarketPriceTick[]
}

export type MarketTickStateStatus =
  | 'live'
  | 'missing'
  | 'unavailable'
  | 'delayed'
  | 'source_mismatch'
  | 'out_of_order'

export interface MarketTickState {
  status: MarketTickStateStatus
  fact: MarketPriceFact | null
}

export interface MarketTickMergeResult {
  lastGood: Map<string, MarketPriceFact>
  states: Map<string, MarketTickState>
}

export interface AssetMarketV2Item {
  market_id: string
  market_code: string
  provider: string
  symbol: string
  market_type: string
  quote_asset: string
  price: AvailableDecimal
  relative_deviation_pct: AvailableDecimal
  change_24h_pct: AvailableDecimal
  turnover_24h: AvailableDecimal
  freshness_status: string
  provider_updated_at: number
  confidence: string
  quality: string
  has_kline: boolean
  venue_kind: string
  chain: string
  protocol: string
  route_key: string
  route: string[]
  pool_addresses: string[]
  quote_notional_usd: AvailableDecimal
  quote_reference_kind: string
  tvl_usd: AvailableDecimal
  price_impact_pct: AvailableDecimal
  round_trip_spread_pct: AvailableDecimal
  block_number: number
  block_timestamp: number
  available: boolean
  unavailable_reason: string
}

export interface CatalogAuditItem {
  provider: string
  source_symbol: string
  market_type: string
  base_alias: string
  quote_alias: string
  upstream_status: string | null
  resolution_status: string
  base_asset_id: string | null
  quote_asset_id: string | null
  reason: string | null
  last_seen_at: number
  rank: number | null
  candidate_kind: string
  alias_review: string
  rollout_mode: string
  resolution_source: string
}

export interface CatalogAuditResult {
  items: CatalogAuditItem[]
  counts: Array<{ status: string; count: number }>
  total: number
}

export interface VenueCoverageRow {
  venue: MarketVenue
  priced: number
  total: number
  coverage_pct: number
  coverage_kind: 'priced' | 'displayed'
  available: boolean
  error: string
}

export interface CexDispersionRow {
  asset_id: string
  asset_symbol: string
  venue_count: number
  min_price: number
  max_price: number
  dispersion_pct: number
}

export interface DexRouteMonitorRow {
  asset_id: string
  asset_symbol: string
  provider: 'uniswap' | 'pancakeswap'
  price: number | null
  available: boolean
  route_count: number
  quality: string
}

export interface Top50VenueInsights {
  coverage: VenueCoverageRow[]
  dispersion: CexDispersionRow[]
  dex_routes: DexRouteMonitorRow[]
}

export interface AssetMarketItem {
  market_id: string
  market_code: string
  symbol: string
  exchange: string
  market_type: string
  quote_asset_id: string
  quote_asset: string
  price: number
  change24h: number
  change_available: boolean
  volume: number
  market_cap: number
  has_kline: boolean
  updated_at: number
  data_delay_seconds: number
  is_reference: boolean
  provider_updated_at: number
  freshness_status: string
}

export interface AssetDashboardItem {
  asset_id: string
  asset_symbol: string
  asset_name: string
  logo: string
  reference_market_id: string
  reference_market_code: string
  reference_exchange: string
  reference_market_type: string
  price: number
  change24h: number
  change_available: boolean
  market_cap: number
  turnover24h: number
  market_count: number
  has_kline: boolean
  updated_at: number
  data_delay_seconds: number
  markets: AssetMarketItem[]
  provider_updated_at: number
  freshness_status: string
}

export interface MarketItem {
  market_id: string
  market_code: string
  symbol: string
  price: number
  change24h: number
  volume: number
  market_cap: number
  name: string
  logo: string
  exchange: string
  market_type: string
  has_kline: boolean
  change_available: boolean
  change_source: string
  updated_at: number
  data_delay_seconds: number
  base_asset_id: string
  base_asset: string
  quote_asset_id: string
  quote_asset: string
  provider_updated_at: number
  freshness_status: string
}

export type KlineInterval = '1m' | '15m' | '1h' | '1d'

export type TopMoversDirection = 'gainers' | 'losers'

export interface TopMoverItem {
  rank: number
  market_id: string
  market_code: string
  symbol: string
  price: number
  change24h: number
  volume: number
  market_cap: number
  name: string
  logo: string
  exchange: string
  market_type: string
  change_available: boolean
  updated_at: number
  data_delay_seconds: number
}

export interface TopMoversResult {
  items: TopMoverItem[]
  total: number
}

export interface Kline {
  timestamp: number
  open: number
  high: number
  low: number
  close: number
  volume: number
}

export interface SupportAsset {
  guid: string
  asset_name: string
  asset_symbol: string
  asset_logo: string
}

export interface ExchangeInfo {
  guid: string
  name: string
  logo: string
}

export interface SymbolInfo {
  guid: string
  base_asset: string
  quote_asset: string
  symbol_name: string
  base_asset_id: string
  quote_asset_id: string
  exchange: string
  market_type: string
}

export interface MarketDashboardQuery {
  exchange?: string
  search?: string
  marketId?: string
  sortBy?: 'rank' | 'market_cap' | 'volume' | 'change24h' | 'price' | 'symbol'
  sortDirection?: 'asc' | 'desc'
}

export interface AssetDashboardQuery {
  search?: string
  sortBy?: 'rank' | 'market_cap' | 'turnover24h' | 'change24h' | 'price' | 'symbol'
  sortDirection?: 'asc' | 'desc'
}

export interface SparklinePoint {
  timestamp: number
  close: number
}

export interface MarketSparkline {
  market_id: string
  points: SparklinePoint[]
}

export interface FiatRates {
  base: string
  rates: Record<string, number>
  source: string
}

export interface Paged<T> {
  items: T[]
  total: number
}

/* ===== Coercion helpers (backend may send numbers as strings) ===== */

function toNum(value: unknown): number {
  const n = typeof value === 'number' ? value : parseFloat(String(value))
  return Number.isFinite(n) ? n : 0
}

function toStr(value: unknown): string {
  return value == null ? '' : String(value)
}

function toArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function toAvailableDecimal(value: unknown): AvailableDecimal {
  const raw = (value ?? {}) as Record<string, unknown>
  const available = Boolean(raw.available)
  if (!available || raw.value == null || raw.value === '') {
    return { value: null, available: false }
  }
  const parsed = Number(raw.value)
  return Number.isFinite(parsed)
    ? { value: parsed, available: true }
    : { value: null, available: false }
}

export function unavailableMarketPriceFact(): MarketPriceFact {
  return {
    price_usd: { value: null, available: false },
    change_24h_pct: { value: null, available: false },
    turnover_24h_usd: { value: null, available: false },
    available: false,
    kind: 'unavailable',
    source: '',
    source_time: 0,
    observed_at: 0,
    last_success_at: 0,
    freshness_status: 'unavailable',
    freshness_age_seconds: 0,
    quality: 'unavailable',
    contributor_count: 0,
    contributors: [],
    version: 0,
  }
}

function toMarketPriceFact(value: unknown): MarketPriceFact {
  if (value == null || typeof value !== 'object') {
    return unavailableMarketPriceFact()
  }
  const raw = value as Record<string, unknown>
  const price = toAvailableDecimal(raw.price_usd)
  const available = Boolean(raw.available) && price.available
  return {
    price_usd: available ? price : { value: null, available: false },
    change_24h_pct: available
      ? toAvailableDecimal(raw.change_24h_pct)
      : { value: null, available: false },
    turnover_24h_usd: available
      ? toAvailableDecimal(raw.turnover_24h_usd)
      : { value: null, available: false },
    available,
    kind: available ? (toStr(raw.kind) || 'unknown') : 'unavailable',
    source: available ? toStr(raw.source) : '',
    source_time: available ? toNum(raw.source_time) : 0,
    observed_at: available ? toNum(raw.observed_at) : 0,
    last_success_at: available ? toNum(raw.last_success_at) : 0,
    freshness_status: available
      ? (toStr(raw.freshness_status) || 'unknown')
      : 'unavailable',
    freshness_age_seconds: available ? toNum(raw.freshness_age_seconds) : 0,
    quality: available ? (toStr(raw.quality) || 'unknown') : 'unavailable',
    contributor_count: available ? toNum(raw.contributor_count) : 0,
    contributors: available
      ? toArray(raw.contributors).map(toStr).filter(Boolean)
      : [],
    version: available ? toNum(raw.version) : 0,
  }
}

function legacyTickPriceFact(
  raw: Record<string, unknown>,
  kind: string,
  source: string,
): MarketPriceFact {
  const price = toAvailableDecimal(raw.price_usd)
  if (!Boolean(raw.available) || !price.available) {
    return unavailableMarketPriceFact()
  }
  const contributors = source === 'cex_composite' ? [] : [source]
  return {
    price_usd: price,
    change_24h_pct: toAvailableDecimal(raw.change_24h_pct),
    turnover_24h_usd: toAvailableDecimal(raw.turnover_24h_usd),
    available: true,
    kind,
    source,
    source_time: toNum(raw.source_time),
    observed_at: toNum(raw.observed_at),
    last_success_at: toNum(raw.last_success_at),
    freshness_status: toStr(raw.freshness_status) || 'unknown',
    freshness_age_seconds: toNum(raw.freshness_age_seconds),
    quality: toStr(raw.quality) || 'unknown',
    contributor_count: Math.max(toNum(raw.contributor_count), contributors.length),
    contributors,
    version: toNum(raw.version),
  }
}

/* ===== Endpoints ===== */

export async function getSystemOverview(): Promise<SystemOverview> {
  const { result } = await request<Record<string, unknown>>('/api/v1/get_system_overview')
  const r = result ?? {}
  const storage = (r.storage ?? {}) as Record<string, unknown>
  const deletedRows = (storage.retention_deleted_rows ?? {}) as Record<string, unknown>
  return {
    crawler_status: toStr(r.crawler_status),
    dex_status: toStr(r.dex_status),
    dw_status: toStr(r.dw_status),
    rpc_status: toStr(r.rpc_status),
    redis_status: toStr(r.redis_status),
    database_status: toStr(r.database_status),
    worker_status: toStr(r.worker_status),
    api_status: toStr(r.api_status),
    market_count: toNum(r.market_count),
    asset_count: toNum(r.asset_count),
    symbol_count: toNum(r.symbol_count),
    exchange_count: toNum(r.exchange_count),
    total_market_cap: toNum(r.total_market_cap),
    total_volume: toNum(r.total_volume),
    updated_at: toNum(r.updated_at),
    data_delay_seconds: toNum(r.data_delay_seconds),
    storage: {
      database_bytes: toNum(storage.database_bytes),
      kline_table_bytes: toNum(storage.kline_table_bytes),
      kline_heap_bytes: toNum(storage.kline_heap_bytes),
      kline_index_bytes: toNum(storage.kline_index_bytes),
      kline_estimated_rows: toNum(storage.kline_estimated_rows),
      disk_free_bytes: toNum(storage.disk_free_bytes),
      disk_state: toStr(storage.disk_state),
      retention_last_started_at: toNum(storage.retention_last_started_at),
      retention_last_success_at: toNum(storage.retention_last_success_at),
      retention_last_error: toStr(storage.retention_last_error),
      retention_deleted_rows: Object.fromEntries(
        Object.entries(deletedRows).map(([key, value]) => [key, toNum(value)]),
      ),
      kline_intervals: toArray(storage.kline_intervals).map((raw) => {
        const item = (raw ?? {}) as Record<string, unknown>
        return {
          interval: toStr(item.interval),
          oldest_at: toNum(item.oldest_at),
          newest_at: toNum(item.newest_at),
        }
      }),
    },
    provider_statuses: toArray(r.provider_statuses).map((raw): ProviderStatusItem => {
      const item = (raw ?? {}) as Record<string, unknown>
      return {
        provider: toStr(item.provider),
        status: toStr(item.status),
        operational_status: toStr(item.operational_status),
        primary_source_key: toStr(item.primary_source_key),
        source_count: toNum(item.source_count),
        failing_source_count: toNum(item.failing_source_count),
        last_attempt_at: toNum(item.last_attempt_at),
        last_success_at: toNum(item.last_success_at),
        last_source_time: toNum(item.last_source_time),
        consecutive_failures: toNum(item.consecutive_failures),
        last_error_class: toStr(item.last_error_class),
        rollout_mode: toStr(item.rollout_mode),
        rank_limit: toNum(item.rank_limit),
        min_soak_until: toNum(item.min_soak_until),
        next_retry_at: toNum(item.next_retry_at),
        attempt_count: toNum(item.attempt_count),
        success_count: toNum(item.success_count),
        success_rate_pct: toStr(item.success_rate_pct),
        observation_started_at: toNum(item.observation_started_at),
        readiness_not_before: toNum(item.readiness_not_before),
        rollout_ready: Boolean(item.rollout_ready),
        rollout_blockers: toArray(item.rollout_blockers).map(toStr),
        received_count: toNum(item.received_count),
        matched_asset_count: toNum(item.matched_asset_count),
        price_available_count: toNum(item.price_available_count),
        change_available_count: toNum(item.change_available_count),
        local_preview_enabled: Boolean(item.local_preview_enabled),
        preview_source_key: toStr(item.preview_source_key),
        preview_covered_count: toNum(item.preview_covered_count),
        selection_version: toNum(item.selection_version),
        selection_target_count: toNum(item.selection_target_count),
        selection_count: toNum(item.selection_count),
        selection_candidate_count: toNum(item.selection_candidate_count),
        selection_generated_at: toNum(item.selection_generated_at),
        feed_mode: toStr(item.feed_mode),
        kline_status: toStr(item.kline_status),
        kline_market_count: toNum(item.kline_market_count),
        kline_candle_count: toNum(item.kline_candle_count),
        kline_last_success_at: toNum(item.kline_last_success_at),
        sources: toArray(item.sources).map((sourceRaw): ProviderSourceStatusItem => {
          const source = (sourceRaw ?? {}) as Record<string, unknown>
          return {
            source_key: toStr(source.source_key),
            capability: toStr(source.capability),
            status: toStr(source.status),
            last_attempt_at: toNum(source.last_attempt_at),
            last_success_at: toNum(source.last_success_at),
            last_source_time: toNum(source.last_source_time),
            next_retry_at: toNum(source.next_retry_at),
            consecutive_failures: toNum(source.consecutive_failures),
            attempt_count: toNum(source.attempt_count),
            success_count: toNum(source.success_count),
            success_rate_pct: toStr(source.success_rate_pct),
            last_error_class: toStr(source.last_error_class),
            received_count: toNum(source.received_count),
            matched_asset_count: toNum(source.matched_asset_count),
            written_count: toNum(source.written_count),
          }
        }),
      }
    }),
  }
}

export async function getMarketOverviewV2(venue: MarketVenue = 'all'): Promise<MarketOverviewV2> {
  const { result } = await request<Record<string, unknown>>('/api/v2/get_market_overview', { venue })
  const item = result ?? {}
  const assetCount = toNum(item.asset_count)
  const pricedAssetCount = toNum(item.priced_asset_count)
  const displayedAssetCount = item.displayed_asset_count == null
    ? pricedAssetCount
    : toNum(item.displayed_asset_count)
  const coverageRatio = toAvailableDecimal(item.coverage_ratio_pct)
  return {
    global_market_cap_usd: toAvailableDecimal(item.global_market_cap_usd),
    covered_spot_volume_24h_usd: toAvailableDecimal(item.covered_spot_volume_24h_usd),
    btc_dominance_pct: toAvailableDecimal(item.btc_dominance_pct),
    asset_count: assetCount,
    advancers: toNum(item.advancers),
    decliners: toNum(item.decliners),
    flat: toNum(item.flat),
    unknown: toNum(item.unknown),
    advance_ratio_pct: toAvailableDecimal(item.advance_ratio_pct),
    provider_updated_at: toNum(item.provider_updated_at),
    index_updated_at: toNum(item.index_updated_at),
    venue: (toStr(item.venue) || venue) as MarketVenue,
    ranked_asset_count: toNum(item.ranked_asset_count),
    top50_universe_count: toNum(item.top50_universe_count),
    eligible_asset_count: toNum(item.eligible_asset_count),
    published_asset_count: toNum(item.published_asset_count),
    priced_asset_count: pricedAssetCount,
    displayed_asset_count: displayedAssetCount,
    routable_asset_count: item.routable_asset_count == null
      ? pricedAssetCount
      : toNum(item.routable_asset_count),
    reference_only_asset_count: toNum(item.reference_only_asset_count),
    unpriced_asset_count: item.unpriced_asset_count == null
      ? Math.max(0, assetCount - displayedAssetCount)
      : toNum(item.unpriced_asset_count),
    change_available_count: toNum(item.change_available_count),
    contributing_provider_count: toNum(item.contributing_provider_count),
    single_venue_priced_asset_count: toNum(item.single_venue_priced_asset_count),
    multi_venue_priced_asset_count: toNum(item.multi_venue_priced_asset_count),
    coverage_ratio_pct: coverageRatio,
    display_coverage_ratio_pct: item.display_coverage_ratio_pct == null
      ? coverageRatio
      : toAvailableDecimal(item.display_coverage_ratio_pct),
    local_preview_enabled: Boolean(item.local_preview_enabled),
    preview_source_key: toStr(item.preview_source_key),
    preview_covered_count: toNum(item.preview_covered_count),
    universe: toStr(item.universe),
    selection_version: toNum(item.selection_version),
  }
}

export async function getAssetDashboardV2(
  page = 1,
  pageSize = 20,
  options: {
    venue?: MarketVenue
    search?: string
    filter?: AssetFilter
    sortBy?: 'rank' | 'market_cap' | 'turnover24h' | 'change24h' | 'price' | 'symbol'
    sortDirection?: 'asc' | 'desc'
    includeUncovered?: boolean
    universe?: 'provider_top50' | 'provider_union' | 'legacy_top50'
  } = {},
): Promise<Paged<AssetDashboardV2Item>> {
  const requestedVenue = options.venue ?? 'all'
  const strictDexDisplay =
    requestedVenue === 'uniswap' || requestedVenue === 'pancakeswap'
  const { result, total } = await request<unknown>('/api/v2/get_asset_dashboard', {
    page,
    page_size: pageSize,
    venue: requestedVenue,
    search: options.search ?? '',
    filter: options.filter ?? 'assets',
    sort_by: options.sortBy ?? 'rank',
    sort_direction: options.sortDirection ?? 'desc',
    include_uncovered: options.includeUncovered ?? true,
    universe: options.universe ?? (requestedVenue === 'all' ? 'provider_union' : 'provider_top50'),
  })
  const items = toArray(result).map((raw): AssetDashboardV2Item => {
    const item = (raw ?? {}) as Record<string, unknown>
    const rank = item.rank == null ? null : toNum(item.rank)
    const priceUSD = toAvailableDecimal(item.price_usd)
    const change24hPct = toAvailableDecimal(item.change_24h_pct)
    const displayPriceUSD =
      item.display_price_usd == null && !strictDexDisplay
        ? priceUSD
        : toAvailableDecimal(item.display_price_usd)
    const displayPriceKind =
      toStr(item.display_price_kind) ||
      (strictDexDisplay ? 'unavailable' : toStr(item.price_kind)) ||
      'unavailable'
    const displayChange24hPct =
      item.display_change_24h_pct == null && !strictDexDisplay
        ? change24hPct
        : toAvailableDecimal(item.display_change_24h_pct)
    let venuePrice = toMarketPriceFact(item.venue_price)
    const dexRoutePrice = toMarketPriceFact(item.dex_route_price)
    let displayPrice = toMarketPriceFact(item.display_price)
    if (item.venue_price == null &&
      ['binance', 'coinbase', 'bybit', 'okx', 'hyperliquid'].includes(requestedVenue)) {
      const legacySource = (toStr(item.price_source) || requestedVenue).toLowerCase()
      if (legacySource === requestedVenue) {
        venuePrice = legacyTickPriceFact(item, toStr(item.price_kind), legacySource)
      }
    }
    if (item.display_price == null && requestedVenue === 'all') {
      displayPrice = legacyTickPriceFact(item, 'composite_reference', 'cex_composite')
    }
    return {
      rank,
      selection_version: toNum(item.selection_version),
      selection_rank: toNum(item.selection_rank),
      asset_id: toStr(item.asset_id),
      asset_symbol: toStr(item.asset_symbol),
      asset_name: toStr(item.asset_name),
      logo: toStr(item.logo),
      price_usd: priceUSD,
      composite_price_usd: toAvailableDecimal(item.composite_price_usd),
      market_reference_price_usd: toAvailableDecimal(item.market_reference_price_usd),
      display_price_usd: displayPriceUSD,
      display_price_kind: displayPriceKind,
      display_change_24h_pct: displayChange24hPct,
      display_change_kind:
        toStr(item.display_change_kind) ||
        (strictDexDisplay ? 'unavailable' : ''),
      display_available: item.display_available == null
        ? displayPriceUSD.available
        : Boolean(item.display_available),
      display_observed_at: toNum(item.display_observed_at),
      dex_route_available: Boolean(item.dex_route_available),
      venue_price: venuePrice,
      dex_route_price: dexRoutePrice,
      display_price: displayPrice,
      change_24h_pct: toAvailableDecimal(item.change_24h_pct),
      market_cap_usd: toAvailableDecimal(item.market_cap_usd),
      covered_turnover_24h_usd: toAvailableDecimal(item.covered_turnover_24h_usd),
      circulating_supply: toAvailableDecimal(item.circulating_supply),
      spot_market_count: toNum(item.spot_market_count),
      perp_market_count: toNum(item.perp_market_count),
      dex_route_count: toNum(item.dex_route_count),
      contributor_count: toNum(item.contributor_count),
      priced_venue_count: toNum(item.priced_venue_count),
      confidence: toStr(item.confidence),
      quality: toStr(item.quality),
      price_kind: toStr(item.price_kind),
      price_source: toStr(item.price_source),
      coverage_status: toStr(item.coverage_status),
      coverage_reason: toStr(item.coverage_reason),
      freshness_status: toStr(item.freshness_status),
      freshness_age_seconds: toNum(item.freshness_age_seconds),
      last_attempt_at: toNum(item.last_attempt_at),
      last_success_at: toNum(item.last_success_at),
      last_error_class: toStr(item.last_error_class),
      available: Boolean(item.available),
      source_time: toNum(item.source_time),
      observed_at: toNum(item.observed_at),
      index_updated_at: toNum(item.index_updated_at),
      provider_updated_at: toNum(item.provider_updated_at),
      sparkline_available: Boolean(item.sparkline_available),
    }
  })
  return { items, total: typeof total === 'number' ? total : items.length }
}

export async function getMarketPriceTicks(
  venue: MarketVenue,
  assetIDs: string[],
): Promise<MarketPriceTickSnapshot> {
  const uniqueAssetIDs = [...new Set(assetIDs.map((value) => value.trim()).filter(Boolean))]
  if (uniqueAssetIDs.length > 100) {
    throw new Error('Market tick requests are limited to 100 assets')
  }
  const response = await request<unknown>('/api/v2/get_market_price_ticks', {
    venue,
    asset_ids: uniqueAssetIDs,
  })
  const responseVenue = (toStr(response.venue) || venue) as MarketVenue
  return {
    venue: responseVenue,
    server_time: toNum(response.server_time),
    items: toArray(response.result).map((raw): MarketPriceTick => {
      const item = (raw ?? {}) as Record<string, unknown>
      const provider = toStr(item.provider)
      const priceKind = toStr(item.price_kind)
      let venuePrice = toMarketPriceFact(item.venue_price)
      let dexRoutePrice = toMarketPriceFact(item.dex_route_price)
      let displayPrice = toMarketPriceFact(item.display_price)
      if (item.venue_price == null &&
        ['binance', 'coinbase', 'bybit', 'okx', 'hyperliquid'].includes(responseVenue)) {
        venuePrice = legacyTickPriceFact(item, priceKind, provider)
      }
      if (item.dex_route_price == null &&
        (responseVenue === 'uniswap' || responseVenue === 'pancakeswap')) {
        dexRoutePrice = legacyTickPriceFact(item, 'dex_route', provider)
      }
      if (item.display_price == null && responseVenue === 'all') {
        displayPrice = legacyTickPriceFact(item, 'composite_reference', 'cex_composite')
      }
      return {
        asset_id: toStr(item.asset_id),
        provider,
        price_kind: priceKind,
        price_usd: toAvailableDecimal(item.price_usd),
        change_24h_pct: toAvailableDecimal(item.change_24h_pct),
        turnover_24h_usd: toAvailableDecimal(item.turnover_24h_usd),
        venue_price: venuePrice,
        dex_route_price: dexRoutePrice,
        display_price: displayPrice,
        available: Boolean(item.available),
        freshness_status: toStr(item.freshness_status),
        freshness_age_seconds: toNum(item.freshness_age_seconds),
        source_time: toNum(item.source_time),
        observed_at: toNum(item.observed_at),
        last_success_at: toNum(item.last_success_at),
        version: toNum(item.version),
      }
    }),
  }
}

export function marketTickCacheKey(venue: MarketVenue, assetID: string): string {
  return `${venue}\u0000${assetID.trim()}`
}

export function isMarketPriceFactMonotonic(
  previous: MarketPriceFact,
  next: MarketPriceFact,
): boolean {
  if (previous.source !== next.source || previous.kind !== next.kind) return false
  if (previous.version > 0 && next.version > 0 && next.version < previous.version) return false
  if (previous.observed_at > 0 && next.observed_at < previous.observed_at) return false
  if (previous.source_time > 0 && next.source_time > 0 &&
    next.source_time < previous.source_time) return false
  return true
}

function tickFactForVenue(
  tick: MarketPriceTick,
  venue: MarketVenue,
): MarketTickState {
  const provider = tick.provider.trim().toLowerCase()
  let fact = unavailableMarketPriceFact()
  let expectedKind = ''
  let expectedSource: string = venue
  switch (venue) {
    case 'all':
      if (provider !== 'all') return { status: 'source_mismatch', fact: null }
      fact = tick.display_price
      expectedKind = 'composite_reference'
      expectedSource = 'cex_composite'
      break
    case 'binance':
    case 'coinbase':
    case 'bybit':
    case 'okx':
      if (provider !== venue) return { status: 'source_mismatch', fact: null }
      fact = tick.venue_price
      expectedKind = 'venue_spot'
      break
    case 'hyperliquid':
      if (provider !== venue) return { status: 'source_mismatch', fact: null }
      fact = tick.venue_price
      expectedKind = 'perp_mark'
      break
    case 'uniswap':
    case 'pancakeswap':
      if (provider !== venue) return { status: 'source_mismatch', fact: null }
      fact = tick.dex_route_price
      expectedKind = 'dex_route'
      break
  }
  if (!fact.available || !fact.price_usd.available) {
    return { status: 'unavailable', fact: null }
  }
  if (fact.source.trim().toLowerCase() !== expectedSource ||
    fact.kind.trim().toLowerCase() !== expectedKind) {
    return { status: 'source_mismatch', fact: null }
  }
  if (fact.freshness_status !== 'fresh') {
    return { status: 'delayed', fact: null }
  }
  return { status: 'live', fact }
}

export function mergeMarketPriceTickSnapshot(
  previousLastGood: ReadonlyMap<string, MarketPriceFact>,
  snapshot: MarketPriceTickSnapshot,
  expectedVenue: MarketVenue,
  expectedAssetIDs: string[],
): MarketTickMergeResult {
  const lastGood = new Map(previousLastGood)
  const states = new Map<string, MarketTickState>()
  const assetIDs = [...new Set(expectedAssetIDs.map((value) => value.trim()).filter(Boolean))]
  if (snapshot.venue !== expectedVenue) {
    for (const assetID of assetIDs) {
      const previous = lastGood.get(marketTickCacheKey(expectedVenue, assetID)) ?? null
      states.set(assetID, { status: 'source_mismatch', fact: previous })
    }
    return { lastGood, states }
  }
  const received = new Map<string, MarketPriceTick>()
  for (const tick of snapshot.items) {
    if (tick.asset_id.trim() !== '') received.set(tick.asset_id.trim(), tick)
  }
  for (const assetID of assetIDs) {
    const cacheKey = marketTickCacheKey(expectedVenue, assetID)
    const previous = lastGood.get(cacheKey) ?? null
    const tick = received.get(assetID)
    if (!tick) {
      states.set(assetID, { status: 'missing', fact: previous })
      continue
    }
    const candidate = tickFactForVenue(tick, expectedVenue)
    if (candidate.status !== 'live' || candidate.fact == null) {
      states.set(assetID, { status: candidate.status, fact: previous })
      continue
    }
    if (previous && !isMarketPriceFactMonotonic(previous, candidate.fact)) {
      states.set(assetID, { status: 'out_of_order', fact: previous })
      continue
    }
    lastGood.set(cacheKey, candidate.fact)
    states.set(assetID, candidate)
  }
  return { lastGood, states }
}

export async function getTop50VenueInsights(): Promise<Top50VenueInsights> {
  const venues: MarketVenue[] = [
    'all', 'binance', 'coinbase', 'bybit', 'okx', 'hyperliquid', 'uniswap', 'pancakeswap',
  ]
  type VenueSnapshot = {
    venue: MarketVenue
    page: Paged<AssetDashboardV2Item>
  }
  const outcomes = new Array<PromiseSettledResult<VenueSnapshot>>(venues.length)
  let nextVenue = 0
  await Promise.all(
    Array.from({ length: 2 }, async () => {
      while (nextVenue < venues.length) {
        const index = nextVenue
        nextVenue += 1
        const venue = venues[index]
        try {
          outcomes[index] = {
            status: 'fulfilled',
            value: {
              venue,
              page: await getAssetDashboardV2(1, 100, {
                venue,
                filter: 'assets',
                sortBy: 'rank',
                sortDirection: 'asc',
                universe: venue === 'all'
                  ? 'provider_union'
                  : 'provider_top50',
              }),
            },
          }
        } catch (error) {
          outcomes[index] = {
            status: 'rejected',
            reason: error,
          }
        }
      }
    }),
  )
  const snapshots = outcomes.flatMap((outcome) =>
    outcome.status === 'fulfilled' ? [outcome.value] : [])
  const coverage = outcomes.map((outcome, index): VenueCoverageRow => {
    const venue = venues[index]
    const dexDisplay =
      venue === 'uniswap' || venue === 'pancakeswap'
    if (outcome.status === 'rejected') {
      return {
        venue,
        priced: 0,
        total: 0,
        coverage_pct: 0,
        coverage_kind: dexDisplay ? 'displayed' : 'priced',
        available: false,
        error: outcome.reason instanceof Error
          ? outcome.reason.message
          : 'Provider snapshot unavailable',
      }
    }
    const page = outcome.value.page
    const priced = page.items.filter((item) =>
      dexDisplay
        ? item.display_available && item.display_price_usd.available
        : item.available && item.price_usd.available).length
    return {
      venue,
      priced,
      total: page.total,
      coverage_pct: page.total > 0 ? priced / page.total * 100 : 0,
      coverage_kind: dexDisplay ? 'displayed' : 'priced',
      available: true,
      error: '',
    }
  })

  const pricesByAsset = new Map<string, {
    symbol: string
    prices: number[]
  }>()
  for (const snapshot of snapshots.filter(({ venue }) =>
    venue === 'binance' || venue === 'coinbase' || venue === 'bybit' || venue === 'okx')) {
    for (const item of snapshot.page.items) {
      if (!item.available || !item.price_usd.available || item.price_usd.value == null) continue
      const aggregate = pricesByAsset.get(item.asset_id) ?? { symbol: item.asset_symbol, prices: [] }
      aggregate.prices.push(item.price_usd.value)
      pricesByAsset.set(item.asset_id, aggregate)
    }
  }
  const dispersion = Array.from(pricesByAsset.entries())
    .filter(([, value]) => value.prices.length >= 2)
    .map(([asset_id, value]): CexDispersionRow => {
      const min = Math.min(...value.prices)
      const max = Math.max(...value.prices)
      const midpoint = (min + max) / 2
      return {
        asset_id,
        asset_symbol: value.symbol,
        venue_count: value.prices.length,
        min_price: min,
        max_price: max,
        dispersion_pct: midpoint > 0 ? (max - min) / midpoint * 100 : 0,
      }
    })
    .sort((left, right) => right.dispersion_pct - left.dispersion_pct)

  const dex_routes = snapshots
    .filter(({ venue }) => venue === 'uniswap' || venue === 'pancakeswap')
    .flatMap(({ venue, page }) => page.items
      .filter((item) => item.dex_route_available && item.dex_route_count > 0)
      .map((item): DexRouteMonitorRow => ({
        asset_id: item.asset_id,
        asset_symbol: item.asset_symbol,
        provider: venue as 'uniswap' | 'pancakeswap',
        price: item.dex_route_available && item.price_usd.available
          ? item.price_usd.value
          : null,
        available: item.dex_route_available,
        route_count: item.dex_route_count,
        quality: item.quality || 'unknown',
      })))

  return { coverage, dispersion, dex_routes }
}

export async function getAssetVenuesV2(
  assetId: string,
  venue: MarketVenue = 'all',
): Promise<AssetMarketV2Item[]> {
  const { result } = await request<unknown>('/api/v2/get_asset_venues', {
    asset_id: assetId,
    venue,
  })
  return toArray(result).map((raw): AssetMarketV2Item => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      market_id: toStr(item.market_id),
      market_code: toStr(item.market_code),
      provider: toStr(item.provider),
      symbol: toStr(item.symbol),
      market_type: toStr(item.market_type),
      quote_asset: toStr(item.quote_asset),
      price: toAvailableDecimal(item.price),
      relative_deviation_pct: toAvailableDecimal(item.relative_deviation_pct),
      change_24h_pct: toAvailableDecimal(item.change_24h_pct),
      turnover_24h: toAvailableDecimal(item.turnover_24h),
      freshness_status: toStr(item.freshness_status),
      provider_updated_at: toNum(item.provider_updated_at),
      confidence: toStr(item.confidence),
      quality: toStr(item.quality),
      has_kline: Boolean(item.has_kline),
      venue_kind: toStr(item.venue_kind),
      chain: toStr(item.chain),
      protocol: toStr(item.protocol),
      route_key: toStr(item.route_key),
      route: toArray(item.route).map(toStr),
      pool_addresses: toArray(item.pool_addresses).map(toStr),
      quote_notional_usd: toAvailableDecimal(item.quote_notional_usd),
      quote_reference_kind: toStr(item.quote_reference_kind),
      tvl_usd: toAvailableDecimal(item.tvl_usd),
      price_impact_pct: toAvailableDecimal(item.price_impact_pct),
      round_trip_spread_pct: toAvailableDecimal(item.round_trip_spread_pct),
      block_number: toNum(item.block_number),
      block_timestamp: toNum(item.block_timestamp),
      available: Boolean(item.available),
      unavailable_reason: toStr(item.unavailable_reason),
    }
  })
}

export const getAssetMarketsV2 = getAssetVenuesV2

export async function getProviderCatalogAudit(
  page = 1,
  pageSize = 50,
  provider = '',
  status = '',
  rankLimit = 50,
): Promise<CatalogAuditResult> {
  const response = await request<unknown>('/api/v2/get_provider_catalog_audit', {
    page,
    page_size: pageSize,
    provider,
    status,
    rank_limit: rankLimit,
  })
  const items = toArray(response.result).map((raw): CatalogAuditItem => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      provider: toStr(item.provider),
      source_symbol: toStr(item.source_symbol),
      market_type: toStr(item.market_type),
      base_alias: toStr(item.base_alias),
      quote_alias: toStr(item.quote_alias),
      upstream_status: item.upstream_status == null ? null : toStr(item.upstream_status),
      resolution_status: toStr(item.resolution_status),
      base_asset_id: item.base_asset_id == null ? null : toStr(item.base_asset_id),
      quote_asset_id: item.quote_asset_id == null ? null : toStr(item.quote_asset_id),
      reason: item.reason == null ? null : toStr(item.reason),
      last_seen_at: toNum(item.last_seen_at),
      rank: item.rank == null ? null : toNum(item.rank),
      candidate_kind: toStr(item.candidate_kind),
      alias_review: toStr(item.alias_review),
      rollout_mode: toStr(item.rollout_mode),
      resolution_source: toStr(item.resolution_source),
    }
  })
  const payload = response as unknown as Record<string, unknown>
  const counts = toArray(payload.counts).map((raw) => {
    const item = (raw ?? {}) as Record<string, unknown>
    return { status: toStr(item.status), count: toNum(item.count) }
  })
  return { items, counts, total: typeof response.total === 'number' ? response.total : items.length }
}

export async function getMarketDashboard(
  page = 1,
  pageSize = 20,
  query: string | MarketDashboardQuery = '',
): Promise<Paged<MarketItem>> {
  const options: MarketDashboardQuery = typeof query === 'string' ? { exchange: query } : query
  const { result, total } = await request<unknown>('/api/v1/get_market_dashboard', {
    page,
    page_size: pageSize,
    exchange: options.exchange ?? '',
    search: options.search ?? '',
    market_id: options.marketId ?? '',
    sort_by: options.sortBy ?? 'rank',
    sort_direction: options.sortDirection ?? 'desc',
  })
  const items = toArray(result).map((raw): MarketItem => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      market_id: toStr(item.market_id),
      market_code: toStr(item.market_code),
      symbol: toStr(item.symbol),
      price: toNum(item.price),
      change24h: toNum(item.change24h),
      volume: toNum(item.volume),
      market_cap: toNum(item.market_cap),
      name: toStr(item.name),
      logo: toStr(item.logo),
      exchange: toStr(item.exchange),
      market_type: toStr(item.market_type),
      has_kline: Boolean(item.has_kline),
      change_available: Boolean(item.change_available),
      change_source: toStr(item.change_source),
      updated_at: toNum(item.updated_at),
      data_delay_seconds: toNum(item.data_delay_seconds),
      base_asset_id: toStr(item.base_asset_id),
      base_asset: toStr(item.base_asset),
      quote_asset_id: toStr(item.quote_asset_id),
      quote_asset: toStr(item.quote_asset),
      provider_updated_at: toNum(item.provider_updated_at),
      freshness_status: toStr(item.freshness_status),
    }
  })
  return { items, total: typeof total === 'number' ? total : items.length }
}

function parseAssetMarket(raw: unknown): AssetMarketItem {
  const item = (raw ?? {}) as Record<string, unknown>
  return {
    market_id: toStr(item.market_id),
    market_code: toStr(item.market_code),
    symbol: toStr(item.symbol),
    exchange: toStr(item.exchange),
    market_type: toStr(item.market_type),
    quote_asset_id: toStr(item.quote_asset_id),
    quote_asset: toStr(item.quote_asset),
    price: toNum(item.price),
    change24h: toNum(item.change24h),
    change_available: Boolean(item.change_available),
    volume: toNum(item.volume),
    market_cap: toNum(item.market_cap),
    has_kline: Boolean(item.has_kline),
    updated_at: toNum(item.updated_at),
    data_delay_seconds: toNum(item.data_delay_seconds),
    is_reference: Boolean(item.is_reference),
    provider_updated_at: toNum(item.provider_updated_at),
    freshness_status: toStr(item.freshness_status),
  }
}

export async function getAssetDashboard(
  page = 1,
  pageSize = 20,
  query: AssetDashboardQuery = {},
): Promise<Paged<AssetDashboardItem>> {
  const { result, total } = await request<unknown>('/api/v1/get_asset_dashboard', {
    page,
    page_size: pageSize,
    search: query.search ?? '',
    sort_by: query.sortBy ?? 'rank',
    sort_direction: query.sortDirection ?? 'desc',
  })
  const items = toArray(result).map((raw): AssetDashboardItem => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      asset_id: toStr(item.asset_id),
      asset_symbol: toStr(item.asset_symbol),
      asset_name: toStr(item.asset_name),
      logo: toStr(item.logo),
      reference_market_id: toStr(item.reference_market_id),
      reference_market_code: toStr(item.reference_market_code),
      reference_exchange: toStr(item.reference_exchange),
      reference_market_type: toStr(item.reference_market_type),
      price: toNum(item.price),
      change24h: toNum(item.change24h),
      change_available: Boolean(item.change_available),
      market_cap: toNum(item.market_cap),
      turnover24h: toNum(item.turnover24h),
      market_count: toNum(item.market_count),
      has_kline: Boolean(item.has_kline),
      updated_at: toNum(item.updated_at),
      data_delay_seconds: toNum(item.data_delay_seconds),
      markets: toArray(item.markets).map(parseAssetMarket),
      provider_updated_at: toNum(item.provider_updated_at),
      freshness_status: toStr(item.freshness_status),
    }
  })
  return { items, total: typeof total === 'number' ? total : items.length }
}

export async function getTopMovers(
  direction: TopMoversDirection,
  limit = 5,
): Promise<TopMoversResult> {
  const { result, total } = await request<unknown>('/api/v1/get_top_movers', {
    direction,
    limit,
  })
  const items = toArray(result).map((raw): TopMoverItem => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      rank: toNum(item.rank),
      market_id: toStr(item.market_id),
      market_code: toStr(item.market_code),
      symbol: toStr(item.symbol),
      price: toNum(item.price),
      change24h: toNum(item.change24h),
      volume: toNum(item.volume),
      market_cap: toNum(item.market_cap),
      name: toStr(item.name),
      logo: toStr(item.logo),
      exchange: toStr(item.exchange),
      market_type: toStr(item.market_type),
      change_available: Boolean(item.change_available),
      updated_at: toNum(item.updated_at),
      data_delay_seconds: toNum(item.data_delay_seconds),
    }
  })
  return { items, total: typeof total === 'number' ? total : items.length }
}

export async function getKlines(
  identity: string,
  interval: KlineInterval,
  limit = 200,
  identityKind: 'market_id' | 'symbol_guid' = 'market_id',
): Promise<Kline[]> {
  const { result } = await request<unknown>('/api/v1/get_klines', {
    market_id: identityKind === 'market_id' ? identity : '',
    symbol_guid: identityKind === 'symbol_guid' ? identity : '',
    interval,
    limit,
  })
  return toArray(result)
    .map((raw): Kline => {
      const item = (raw ?? {}) as Record<string, unknown>
      let ts = toNum(item.timestamp)
      // Normalize second-based timestamps to milliseconds.
      if (ts > 0 && ts < 1e12) ts *= 1000
      return {
        timestamp: ts,
        open: toNum(item.open),
        high: toNum(item.high),
        low: toNum(item.low),
        close: toNum(item.close),
        volume: toNum(item.volume),
      }
    })
    .filter((k) => k.timestamp > 0)
    .sort((a, b) => a.timestamp - b.timestamp)
}

export async function getMarketSparklines(
  marketIds: string[],
  interval: KlineInterval = '1h',
  limit = 168,
): Promise<Map<string, number[]>> {
  const { result } = await request<unknown>('/api/v1/get_market_sparklines', {
    market_ids: marketIds,
    interval,
    limit,
  })
  const lines = new Map<string, number[]>()
  for (const raw of toArray(result)) {
    const item = (raw ?? {}) as Record<string, unknown>
    const points = toArray(item.points)
      .map((pointRaw): SparklinePoint => {
        const point = (pointRaw ?? {}) as Record<string, unknown>
        return { timestamp: toNum(point.timestamp), close: toNum(point.close) }
      })
      .filter((point) => point.timestamp > 0 && Number.isFinite(point.close))
      .sort((a, b) => a.timestamp - b.timestamp)
    if (points.length >= 2) lines.set(toStr(item.market_id), points.map((point) => point.close))
  }
  return lines
}

export async function getSupportAssets(): Promise<SupportAsset[]> {
  const { result } = await request<unknown>('/api/v1/get_support_assets')
  return toArray(result).map((raw): SupportAsset => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      guid: toStr(item.guid),
      asset_name: toStr(item.asset_name),
      asset_symbol: toStr(item.asset_symbol),
      asset_logo: toStr(item.asset_logo),
    }
  })
}

export async function getExchanges(page = 1, pageSize = 100): Promise<Paged<ExchangeInfo>> {
  const { result, total } = await request<unknown>('/api/v1/get_exchanges', {
    page,
    page_size: pageSize,
  })
  const items = toArray(result).map((raw): ExchangeInfo => {
    const item = (raw ?? {}) as Record<string, unknown>
    return { guid: toStr(item.guid), name: toStr(item.name), logo: toStr(item.logo) }
  })
  return { items, total: typeof total === 'number' ? total : items.length }
}

export async function getSymbols(page = 1, pageSize = 20): Promise<Paged<SymbolInfo>> {
  const { result, total } = await request<unknown>('/api/v1/get_symbols', {
    page,
    page_size: pageSize,
  })
  const items = toArray(result).map((raw): SymbolInfo => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      guid: toStr(item.guid),
      base_asset: toStr(item.base_asset),
      quote_asset: toStr(item.quote_asset),
      symbol_name: toStr(item.symbol_name),
      base_asset_id: toStr(item.base_asset_id),
      quote_asset_id: toStr(item.quote_asset_id),
      exchange: toStr(item.exchange),
      market_type: toStr(item.market_type),
    }
  })
  return { items, total: typeof total === 'number' ? total : items.length }
}

/** Fetch every symbol (used by pickers / lookups). Caps at 20 pages. */
export async function getAllSymbols(pageSize = 200): Promise<SymbolInfo[]> {
  const first = await getSymbols(1, pageSize)
  const all = [...first.items]
  const pages = Math.min(Math.ceil(first.total / pageSize), 20)
  for (let p = 2; p <= pages; p += 1) {
    const next = await getSymbols(p, pageSize)
    all.push(...next.items)
    if (next.items.length < pageSize) break
  }
  return all
}

export async function getFiatRates(): Promise<FiatRates> {
  const { result } = await request<Record<string, unknown>>('/api/v1/get_fiat_rates')
  const r = result ?? {}
  const rates: Record<string, number> = {}
  const rawRates = (r.rates ?? {}) as Record<string, unknown>
  for (const [key, value] of Object.entries(rawRates)) {
    rates[key] = toNum(value)
  }
  return { base: toStr(r.base) || 'USD', rates, source: toStr(r.source) }
}

/* ===== Asset insights ===== */

export interface MarketBreadth {
  asset_count: number
  advancers: number
  decliners: number
  flat: number
  unknown: number
  advance_ratio: number
  median_change24h: number
  turnover24h: number
}

export interface ChangeDistributionBucket {
  key: string
  label: string
  min: string
  max: string
  count: number
}

export interface CrossVenueItem {
  asset_id: string
  asset_symbol: string
  asset_name: string
  spot_market_id: string
  spot_market_code: string
  spot_exchange: string
  spot_quote_asset: string
  spot_price: number
  spot_change24h: number
  spot_change_available: boolean
  spot_turnover24h: number
  spot_turnover_share: number
  spot_delay_seconds: number
  perp_market_id: string
  perp_market_code: string
  perp_exchange: string
  perp_quote_asset: string
  perp_price: number
  perp_change24h: number
  perp_change_available: boolean
  perp_turnover24h: number
  perp_turnover_share: number
  perp_delay_seconds: number
  indicative_spread_pct: number
  spread_available: boolean
  change_gap_pct_points: number
  change_gap_available: boolean
}

export interface MarketInsights {
  breadth: MarketBreadth
  distribution: ChangeDistributionBucket[]
  cross_venue: CrossVenueItem[]
  updated_at: number
}

export async function getMarketInsights(): Promise<MarketInsights> {
  const { result } = await request<Record<string, unknown>>('/api/v1/get_market_insights')
  const raw = result ?? {}
  const breadth = (raw.breadth ?? {}) as Record<string, unknown>
  return {
    breadth: {
      asset_count: toNum(breadth.asset_count),
      advancers: toNum(breadth.advancers),
      decliners: toNum(breadth.decliners),
      flat: toNum(breadth.flat),
      unknown: toNum(breadth.unknown),
      advance_ratio: toNum(breadth.advance_ratio),
      median_change24h: toNum(breadth.median_change24h),
      turnover24h: toNum(breadth.turnover24h),
    },
    distribution: toArray(raw.distribution).map((value): ChangeDistributionBucket => {
      const item = (value ?? {}) as Record<string, unknown>
      return {
        key: toStr(item.key),
        label: toStr(item.label),
        min: toStr(item.min),
        max: toStr(item.max),
        count: toNum(item.count),
      }
    }),
    cross_venue: toArray(raw.cross_venue).map((value): CrossVenueItem => {
      const item = (value ?? {}) as Record<string, unknown>
      return {
        asset_id: toStr(item.asset_id),
        asset_symbol: toStr(item.asset_symbol),
        asset_name: toStr(item.asset_name),
        spot_market_id: toStr(item.spot_market_id),
        spot_market_code: toStr(item.spot_market_code),
        spot_exchange: toStr(item.spot_exchange),
        spot_quote_asset: toStr(item.spot_quote_asset),
        spot_price: toNum(item.spot_price),
        spot_change24h: toNum(item.spot_change24h),
        spot_change_available: Boolean(item.spot_change_available),
        spot_turnover24h: toNum(item.spot_turnover24h),
        spot_turnover_share: toNum(item.spot_turnover_share),
        spot_delay_seconds: toNum(item.spot_delay_seconds),
        perp_market_id: toStr(item.perp_market_id),
        perp_market_code: toStr(item.perp_market_code),
        perp_exchange: toStr(item.perp_exchange),
        perp_quote_asset: toStr(item.perp_quote_asset),
        perp_price: toNum(item.perp_price),
        perp_change24h: toNum(item.perp_change24h),
        perp_change_available: Boolean(item.perp_change_available),
        perp_turnover24h: toNum(item.perp_turnover24h),
        perp_turnover_share: toNum(item.perp_turnover_share),
        perp_delay_seconds: toNum(item.perp_delay_seconds),
        indicative_spread_pct: toNum(item.indicative_spread_pct),
        spread_available: Boolean(item.spread_available),
        change_gap_pct_points: toNum(item.change_gap_pct_points),
        change_gap_available: Boolean(item.change_gap_available),
      }
    }),
    updated_at: toNum(raw.updated_at),
  }
}

export type MomentumWindow = '24h' | '7d' | '30d'

export interface AssetMomentumItem {
  asset_id: string
  asset_symbol: string
  asset_name: string
  market_id: string
  market_code: string
  exchange: string
  return_pct: number
  volatility_pct: number
  high_low_range_pct: number
  candle_count: number
  expected_candles: number
  coverage_pct: number
  low_coverage: boolean
}

export interface AssetMomentumResult {
  items: AssetMomentumItem[]
  total: number
  window: MomentumWindow
  interval: '1h'
  window_start: number
  window_end: number
}

export async function getAssetMomentum(window: MomentumWindow): Promise<AssetMomentumResult> {
  const response = await request<unknown>('/api/v1/get_asset_momentum', { window })
  const items = toArray(response.result).map((value): AssetMomentumItem => {
    const item = (value ?? {}) as Record<string, unknown>
    return {
      asset_id: toStr(item.asset_id),
      asset_symbol: toStr(item.asset_symbol),
      asset_name: toStr(item.asset_name),
      market_id: toStr(item.market_id),
      market_code: toStr(item.market_code),
      exchange: toStr(item.exchange),
      return_pct: toNum(item.return_pct),
      volatility_pct: toNum(item.volatility_pct),
      high_low_range_pct: toNum(item.high_low_range_pct),
      candle_count: toNum(item.candle_count),
      expected_candles: toNum(item.expected_candles),
      coverage_pct: toNum(item.coverage_pct),
      low_coverage: Boolean(item.low_coverage),
    }
  })
  const raw = response as unknown as Record<string, unknown>
  return {
    items,
    total: typeof response.total === 'number' ? response.total : items.length,
    window: (toStr(raw.window) || window) as MomentumWindow,
    interval: '1h',
    window_start: toNum(raw.window_start),
    window_end: toNum(raw.window_end),
  }
}

/* ===== Kline analytics (Apache Doris OLAP) ===== */

export interface KlineAnalyticsItem {
  symbol_guid: string
  symbol_name: string
  base_asset: string
  quote_asset: string
  candle_count: number
  price_change_pct: number
  period_high: number
  period_low: number
  high_low_range: number
  volatility_pct: number
  avg_volume: number
  total_volume: number
}

export interface KlineAnalyticsResult {
  items: KlineAnalyticsItem[]
  total: number
}

/**
 * Aggregated kline analytics computed in Apache Doris (not PostgreSQL).
 * Throws ApiError when the data warehouse is unavailable — the page renders
 * ErrorState; analytics are never faked or recomputed client-side.
 */
export async function getKlineAnalytics(
  interval: KlineInterval,
  limit = 20,
): Promise<KlineAnalyticsResult> {
  const { result, total } = await request<unknown>('/api/v1/get_kline_analytics', {
    interval,
    limit,
  })
  const items = toArray(result).map((raw): KlineAnalyticsItem => {
    const item = (raw ?? {}) as Record<string, unknown>
    return {
      symbol_guid: toStr(item.symbol_guid),
      symbol_name: toStr(item.symbol_name) || toStr(item.symbol_guid),
      base_asset: toStr(item.base_asset),
      quote_asset: toStr(item.quote_asset),
      candle_count: toNum(item.candle_count),
      price_change_pct: toNum(item.price_change_pct),
      period_high: toNum(item.period_high),
      period_low: toNum(item.period_low),
      high_low_range: toNum(item.high_low_range),
      volatility_pct: toNum(item.volatility_pct),
      avg_volume: toNum(item.avg_volume),
      total_volume: toNum(item.total_volume),
    }
  })
  return { items, total: typeof total === 'number' ? total : items.length }
}
