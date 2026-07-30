import { ApiError, request } from './common'
import { tradingAPI } from './trading'
import type { ProviderStatusItem } from './market'

export type SystemState =
  | 'live'
  | 'cached'
  | 'demo_snapshot'
  | 'degraded'
  | 'offline'
  | 'unknown'

export type SystemSourceMode = 'native' | 'legacy' | 'demo_snapshot'

export interface StatusEvidence {
  state: SystemState
  last_success_at: number | null
  age_seconds: number | null
  reason: string
  source: string
}

export interface SystemComponents {
  matching: StatusEvidence
  liquidity: StatusEvidence
  transport: StatusEvidence
  market_data: StatusEvidence
  outbox: StatusEvidence
  database: StatusEvidence
  disk: StatusEvidence
  retention: StatusEvidence
}

export interface ProcessStatus {
  key: string
  label: string
  raw_status: string
  status: StatusEvidence
}

export interface OptionalMetric {
  available: boolean
  value: number | null
  reason: string
}

export interface KlineIntervalStorage {
  interval: string
  oldest_at: OptionalMetric
  newest_at: OptionalMetric
}

export interface SystemStorage {
  database_bytes: OptionalMetric
  kline_table_bytes: OptionalMetric
  kline_heap_bytes: OptionalMetric
  kline_index_bytes: OptionalMetric
  kline_estimated_rows: OptionalMetric
  disk_free_bytes: OptionalMetric
  disk_state: string
  warning_below_bytes: number
  critical_below_bytes: number
  retention_last_started_at: OptionalMetric
  retention_last_success_at: OptionalMetric
  retention_last_error: string
  retention_deleted_rows: Record<string, OptionalMetric>
  kline_intervals: KlineIntervalStorage[]
}

export interface PriceSourceStatus {
  key: 'route_price' | 'reference_display_price' | string
  label: string
  status: StatusEvidence
  source: string
  meaning: string
  boundary: string
}

export interface SystemStatusSnapshot {
  schema_version: string
  formula_version: string
  source_mode: SystemSourceMode
  generated_at: number
  overall: StatusEvidence
  components: SystemComponents
  processes: ProcessStatus[]
  storage: SystemStorage
  price_sources: PriceSourceStatus[]
  provider_statuses: ProviderStatusItem[]
}

export interface LegacySystemInputs {
  overview?: Record<string, unknown>
  overviewFailed?: boolean
  referenceOverview?: Record<string, unknown>
  referenceFailed?: boolean
  uniswapOverview?: Record<string, unknown>
  uniswapFailed?: boolean
  pancakeOverview?: Record<string, unknown>
  pancakeFailed?: boolean
  tradingStatus?: Record<string, unknown>
  tradingStatusFailed?: boolean
  orderBook?: Record<string, unknown>
  orderBookFailed?: boolean
}

const SYSTEM_STATUS_PATH = '/api/v1/get_system_status'
const UNKNOWN_REASON = 'The backend did not report this field.'
const EXPECTED_INTERVALS = ['1m', '15m', '1h', '1d']
const REQUIRED_COMPONENT_KEYS: Array<keyof SystemComponents> = [
  'matching',
  'liquidity',
  'transport',
  'market_data',
  'outbox',
  'database',
  'disk',
  'retention',
]

export const SYSTEM_STATE_LABELS: Record<SystemState, string> = {
  live: 'LIVE',
  cached: 'CACHED',
  demo_snapshot: 'DEMO SNAPSHOT',
  degraded: 'DEGRADED',
  offline: 'OFFLINE',
  unknown: 'UNKNOWN',
}

function asRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {}
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function hasOwn(record: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key)
}

function text(value: unknown): string {
  return value == null ? '' : String(value)
}

function finiteNumber(value: unknown): number | null {
  if (value === '' || value == null) return null
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? parsed : null
}

function boolean(value: unknown): boolean {
  return value === true
}

function normalizeState(value: unknown): SystemState {
  switch (value) {
  case 'live':
  case 'cached':
  case 'demo_snapshot':
  case 'degraded':
  case 'offline':
  case 'unknown':
    return value
  default:
    return 'unknown'
  }
}

function unavailableMetric(reason = UNKNOWN_REASON): OptionalMetric {
  return { available: false, value: null, reason }
}

function availableMetric(value: number): OptionalMetric {
  return { available: true, value, reason: '' }
}

function metricFromContract(value: unknown): OptionalMetric {
  const raw = asRecord(value)
  const parsed = finiteNumber(raw.value)
  if (raw.available === true && parsed !== null) {
    return availableMetric(parsed)
  }
  return unavailableMetric(text(raw.reason) || UNKNOWN_REASON)
}

function metricFromLegacy(
  raw: Record<string, unknown>,
  key: string,
  available: boolean,
  reason = UNKNOWN_REASON,
): OptionalMetric {
  const parsed = finiteNumber(raw[key])
  if (available && hasOwn(raw, key) && parsed !== null) {
    return availableMetric(parsed)
  }
  return unavailableMetric(reason)
}

function normalizeEvidence(value: unknown, fallbackReason = UNKNOWN_REASON): StatusEvidence {
  const raw = asRecord(value)
  return {
    state: normalizeState(raw.state),
    last_success_at: finiteNumber(raw.last_success_at),
    age_seconds: finiteNumber(raw.age_seconds),
    reason: text(raw.reason) || fallbackReason,
    source: text(raw.source) || 'not reported',
  }
}

function evidence(
  state: SystemState,
  reason: string,
  source: string,
  lastSuccessAt: number | null = null,
  ageSeconds: number | null = null,
): StatusEvidence {
  return {
    state,
    last_success_at: lastSuccessAt,
    age_seconds: ageSeconds,
    reason,
    source,
  }
}

function currentProbeEvidence(
  state: SystemState,
  reason: string,
  source: string,
  now: number,
): StatusEvidence {
  return evidence(state, reason, source, now, 0)
}

function timedEvidence(
  lastSuccessAt: number | null,
  now: number,
  liveSeconds: number,
  cachedSeconds: number,
  source: string,
  reasons: { live: string; cached: string; stale: string },
): StatusEvidence {
  if (lastSuccessAt === null || lastSuccessAt <= 0) {
    return evidence('unknown', 'Last successful observation was not reported.', source)
  }
  const ageSeconds = Math.max(0, Math.floor((now - lastSuccessAt) / 1000))
  if (ageSeconds <= liveSeconds) {
    return evidence('live', reasons.live, source, lastSuccessAt, ageSeconds)
  }
  if (ageSeconds <= cachedSeconds) {
    return evidence('cached', reasons.cached, source, lastSuccessAt, ageSeconds)
  }
  return evidence('degraded', reasons.stale, source, lastSuccessAt, ageSeconds)
}

export function deriveOverallStatus(
  components: SystemComponents,
  sourceMode: SystemSourceMode,
): StatusEvidence {
  if (sourceMode === 'demo_snapshot') {
    return evidence(
      'demo_snapshot',
      'The response explicitly identifies itself as a non-live demo snapshot.',
      'system-display.v1',
    )
  }
  if (REQUIRED_COMPONENT_KEYS.every((key) => components[key].state === 'live')) {
    return evidence(
      'live',
      'All required read-only probes have explicit current success evidence.',
      'system-display.v1',
    )
  }
  if (
    components.market_data.state === 'cached' &&
    REQUIRED_COMPONENT_KEYS
      .filter((key) => key !== 'market_data')
      .every((key) => components[key].state === 'live')
  ) {
    return evidence(
      'cached',
      'Only market data is using a retained last success within five minutes.',
      'system-display.v1',
    )
  }
  if (
    components.transport.state === 'offline' &&
    components.database.state === 'offline' &&
    !['live', 'cached'].includes(components.market_data.state)
  ) {
    return evidence(
      'offline',
      'Trading transport and the database-backed market view are unavailable.',
      'system-display.v1',
    )
  }
  return evidence(
    'degraded',
    'One or more required probes are stale, failed, or missing explicit evidence.',
    'system-display.v1',
  )
}

function normalizeComponents(value: unknown): SystemComponents {
  const raw = asRecord(value)
  return {
    matching: normalizeEvidence(raw.matching),
    liquidity: normalizeEvidence(raw.liquidity),
    transport: normalizeEvidence(raw.transport),
    market_data: normalizeEvidence(raw.market_data),
    outbox: normalizeEvidence(raw.outbox),
    database: normalizeEvidence(raw.database),
    disk: normalizeEvidence(raw.disk),
    retention: normalizeEvidence(raw.retention),
  }
}

function normalizeProcess(value: unknown): ProcessStatus {
  const raw = asRecord(value)
  return {
    key: text(raw.key) || 'unknown',
    label: text(raw.label) || 'Unknown process',
    raw_status: text(raw.raw_status) || 'unknown',
    status: normalizeEvidence(raw.status),
  }
}

function normalizeProvider(value: unknown): ProviderStatusItem {
  const raw = asRecord(value)
  return {
    provider: text(raw.provider),
    status: text(raw.status),
    operational_status: text(raw.operational_status),
    primary_source_key: text(raw.primary_source_key),
    source_count: finiteNumber(raw.source_count) ?? 0,
    failing_source_count: finiteNumber(raw.failing_source_count) ?? 0,
    last_attempt_at: finiteNumber(raw.last_attempt_at) ?? 0,
    last_success_at: finiteNumber(raw.last_success_at) ?? 0,
    last_source_time: finiteNumber(raw.last_source_time) ?? 0,
    consecutive_failures: finiteNumber(raw.consecutive_failures) ?? 0,
    last_error_class: text(raw.last_error_class),
    rollout_mode: text(raw.rollout_mode),
    rank_limit: finiteNumber(raw.rank_limit) ?? 0,
    min_soak_until: finiteNumber(raw.min_soak_until) ?? 0,
    next_retry_at: finiteNumber(raw.next_retry_at) ?? 0,
    attempt_count: finiteNumber(raw.attempt_count) ?? 0,
    success_count: finiteNumber(raw.success_count) ?? 0,
    success_rate_pct: text(raw.success_rate_pct),
    observation_started_at: finiteNumber(raw.observation_started_at) ?? 0,
    readiness_not_before: finiteNumber(raw.readiness_not_before) ?? 0,
    rollout_ready: boolean(raw.rollout_ready),
    rollout_blockers: asArray(raw.rollout_blockers).map(text),
    received_count: finiteNumber(raw.received_count) ?? 0,
    matched_asset_count: finiteNumber(raw.matched_asset_count) ?? 0,
    price_available_count: finiteNumber(raw.price_available_count) ?? 0,
    change_available_count: finiteNumber(raw.change_available_count) ?? 0,
    local_preview_enabled: boolean(raw.local_preview_enabled),
    preview_source_key: text(raw.preview_source_key),
    preview_covered_count: finiteNumber(raw.preview_covered_count) ?? 0,
    selection_version: finiteNumber(raw.selection_version) ?? 0,
    selection_target_count: finiteNumber(raw.selection_target_count) ?? 0,
    selection_count: finiteNumber(raw.selection_count) ?? 0,
    selection_candidate_count: finiteNumber(raw.selection_candidate_count) ?? 0,
    selection_generated_at: finiteNumber(raw.selection_generated_at) ?? 0,
    feed_mode: text(raw.feed_mode),
    kline_status: text(raw.kline_status),
    kline_market_count: finiteNumber(raw.kline_market_count) ?? 0,
    kline_candle_count: finiteNumber(raw.kline_candle_count) ?? 0,
    kline_last_success_at: finiteNumber(raw.kline_last_success_at) ?? 0,
    sources: asArray(raw.sources).map((sourceValue) => {
      const source = asRecord(sourceValue)
      return {
        source_key: text(source.source_key),
        capability: text(source.capability),
        status: text(source.status),
        last_attempt_at: finiteNumber(source.last_attempt_at) ?? 0,
        last_success_at: finiteNumber(source.last_success_at) ?? 0,
        last_source_time: finiteNumber(source.last_source_time) ?? 0,
        next_retry_at: finiteNumber(source.next_retry_at) ?? 0,
        consecutive_failures: finiteNumber(source.consecutive_failures) ?? 0,
        attempt_count: finiteNumber(source.attempt_count) ?? 0,
        success_count: finiteNumber(source.success_count) ?? 0,
        success_rate_pct: text(source.success_rate_pct),
        last_error_class: text(source.last_error_class),
        received_count: finiteNumber(source.received_count) ?? 0,
        matched_asset_count: finiteNumber(source.matched_asset_count) ?? 0,
        written_count: finiteNumber(source.written_count) ?? 0,
      }
    }),
  }
}

function normalizeStorage(value: unknown): SystemStorage {
  const raw = asRecord(value)
  const deleted = asRecord(raw.retention_deleted_rows)
  const intervals = new Map(
    asArray(raw.kline_intervals).map((value) => {
      const interval = asRecord(value)
      return [text(interval.interval), interval] as const
    }),
  )
  return {
    database_bytes: metricFromContract(raw.database_bytes),
    kline_table_bytes: metricFromContract(raw.kline_table_bytes),
    kline_heap_bytes: metricFromContract(raw.kline_heap_bytes),
    kline_index_bytes: metricFromContract(raw.kline_index_bytes),
    kline_estimated_rows: metricFromContract(raw.kline_estimated_rows),
    disk_free_bytes: metricFromContract(raw.disk_free_bytes),
    disk_state: text(raw.disk_state) || 'unknown',
    warning_below_bytes: finiteNumber(raw.warning_below_bytes) ?? 25 * 2 ** 30,
    critical_below_bytes: finiteNumber(raw.critical_below_bytes) ?? 15 * 2 ** 30,
    retention_last_started_at: metricFromContract(raw.retention_last_started_at),
    retention_last_success_at: metricFromContract(raw.retention_last_success_at),
    retention_last_error: text(raw.retention_last_error),
    retention_deleted_rows: Object.fromEntries(
      ['1m', '15m', '1h'].map((interval) => [
        interval,
        metricFromContract(deleted[interval]),
      ]),
    ),
    kline_intervals: EXPECTED_INTERVALS.map((name) => {
      const interval = intervals.get(name) ?? {}
      return {
        interval: name,
        oldest_at: metricFromContract(interval.oldest_at),
        newest_at: metricFromContract(interval.newest_at),
      }
    }),
  }
}

function normalizePriceSource(value: unknown): PriceSourceStatus {
  const raw = asRecord(value)
  return {
    key: text(raw.key),
    label: text(raw.label) || 'Unknown price source',
    status: normalizeEvidence(raw.status),
    source: text(raw.source) || 'not reported',
    meaning: text(raw.meaning) || UNKNOWN_REASON,
    boundary: text(raw.boundary) || UNKNOWN_REASON,
  }
}

export function normalizeSystemStatusSnapshot(value: unknown): SystemStatusSnapshot {
  const raw = asRecord(value)
  const sourceMode: SystemSourceMode = raw.source_mode === 'demo_snapshot'
    ? 'demo_snapshot'
    : raw.source_mode === 'legacy'
      ? 'legacy'
      : 'native'
  const components = normalizeComponents(raw.components)
  const derivedOverall = deriveOverallStatus(components, sourceMode)
  const reportedOverall = normalizeEvidence(raw.overall)
  const overall = reportedOverall.state === derivedOverall.state
    ? { ...reportedOverall, state: derivedOverall.state }
    : derivedOverall
  return {
    schema_version: text(raw.schema_version) || 'system-status.unknown',
    formula_version: text(raw.formula_version) || 'system-display.v1',
    source_mode: sourceMode,
    generated_at: finiteNumber(raw.generated_at) ?? Date.now(),
    overall,
    components,
    processes: asArray(raw.processes).map(normalizeProcess),
    storage: normalizeStorage(raw.storage),
    price_sources: asArray(raw.price_sources).map(normalizePriceSource),
    provider_statuses: asArray(raw.provider_statuses).map(normalizeProvider),
  }
}

function legacyProcess(key: string, label: string, rawValue: unknown): ProcessStatus {
  const raw = text(rawValue) || 'unknown'
  const normalized = raw.trim().toLowerCase()
  let status = evidence(
    'unknown',
    'Process heartbeat was not reported.',
    'legacy system overview heartbeat',
  )
  if (['running', 'connected', 'healthy', 'ready'].includes(normalized)) {
    status = evidence(
      'live',
      'The latest heartbeat or dependency probe succeeded.',
      'legacy system overview heartbeat',
    )
  } else if (['stopped', 'disconnected', 'offline', 'failed'].includes(normalized)) {
    status = evidence(
      'offline',
      'The heartbeat is absent or the dependency probe failed.',
      'legacy system overview heartbeat',
    )
  }
  return { key, label, raw_status: raw, status }
}

function legacyDatabaseStatus(rawValue: unknown): StatusEvidence {
  const normalized = text(rawValue).trim().toLowerCase()
  if (['running', 'connected', 'healthy', 'ready'].includes(normalized)) {
    return evidence(
      'live',
      'PostgreSQL read probe succeeded.',
      'legacy system overview database_status',
    )
  }
  if (['stopped', 'disconnected', 'offline', 'failed'].includes(normalized)) {
    return evidence(
      'offline',
      'PostgreSQL read probe failed.',
      'legacy system overview database_status',
    )
  }
  return evidence(
    'unknown',
    'Database status was not reported.',
    'legacy system overview database_status',
  )
}

function legacyStorage(
  overview: Record<string, unknown>,
  now: number,
): { storage: SystemStorage; disk: StatusEvidence; retention: StatusEvidence } {
  const hasStorage = hasOwn(overview, 'storage') &&
    Object.keys(asRecord(overview.storage)).length > 0
  const raw = asRecord(overview.storage)
  const databaseBytes = finiteNumber(raw.database_bytes)
  const metricsAvailable = hasStorage && databaseBytes !== null && databaseBytes > 0
  const deleted = asRecord(raw.retention_deleted_rows)
  const byInterval = new Map(
    asArray(raw.kline_intervals).map((value) => {
      const item = asRecord(value)
      return [text(item.interval), item] as const
    }),
  )
  const diskBytes = finiteNumber(raw.disk_free_bytes)
  const diskState = text(raw.disk_state).toLowerCase() || 'unknown'
  const storage: SystemStorage = {
    database_bytes: metricFromLegacy(raw, 'database_bytes', metricsAvailable),
    kline_table_bytes: metricFromLegacy(raw, 'kline_table_bytes', metricsAvailable),
    kline_heap_bytes: metricFromLegacy(raw, 'kline_heap_bytes', metricsAvailable),
    kline_index_bytes: metricFromLegacy(raw, 'kline_index_bytes', metricsAvailable),
    kline_estimated_rows: metricFromLegacy(raw, 'kline_estimated_rows', metricsAvailable),
    disk_free_bytes: metricFromLegacy(raw, 'disk_free_bytes', diskBytes !== null && diskBytes > 0),
    disk_state: diskState,
    warning_below_bytes: 25 * 2 ** 30,
    critical_below_bytes: 15 * 2 ** 30,
    retention_last_started_at: metricFromLegacy(
      raw,
      'retention_last_started_at',
      (finiteNumber(raw.retention_last_started_at) ?? 0) > 0,
      'Retention has not recorded a start.',
    ),
    retention_last_success_at: metricFromLegacy(
      raw,
      'retention_last_success_at',
      (finiteNumber(raw.retention_last_success_at) ?? 0) > 0,
      'Retention has not recorded a success.',
    ),
    retention_last_error: text(raw.retention_last_error),
    retention_deleted_rows: Object.fromEntries(
      ['1m', '15m', '1h'].map((interval) => [
        interval,
        metricFromLegacy(
          deleted,
          interval,
          hasOwn(deleted, interval),
          'Retention did not report a deleted-row count.',
        ),
      ]),
    ),
    kline_intervals: EXPECTED_INTERVALS.map((interval) => {
      const item = byInterval.get(interval) ?? {}
      return {
        interval,
        oldest_at: metricFromLegacy(
          item,
          'oldest_at',
          (finiteNumber(item.oldest_at) ?? 0) > 0,
          'No oldest candle was reported for this interval.',
        ),
        newest_at: metricFromLegacy(
          item,
          'newest_at',
          (finiteNumber(item.newest_at) ?? 0) > 0,
          'No newest candle was reported for this interval.',
        ),
      }
    }),
  }
  let disk = evidence(
    'unknown',
    'Free disk bytes were not measured.',
    'legacy filesystem statfs',
  )
  if (diskBytes !== null && diskBytes > 0 && diskState === 'healthy') {
    disk = evidence(
      'live',
      'Free disk is above the warning threshold.',
      'legacy filesystem statfs',
    )
  } else if (diskBytes !== null && diskBytes > 0 && ['warning', 'critical'].includes(diskState)) {
    disk = evidence(
      'degraded',
      diskState === 'critical' ? 'Free disk is below 15 GB.' : 'Free disk is below 25 GB.',
      'legacy filesystem statfs',
    )
  }
  const lastRetentionSuccess = finiteNumber(raw.retention_last_success_at)
  const retentionError = text(raw.retention_last_error)
  let retention: StatusEvidence
  if (retentionError) {
    retention = timedEvidence(
      lastRetentionSuccess,
      now,
      36 * 60 * 60,
      36 * 60 * 60,
      'legacy kline_retention_status',
      {
        live: 'The latest retention run failed.',
        cached: 'The latest retention run failed.',
        stale: 'The latest retention run failed.',
      },
    )
    retention.state = 'degraded'
  } else if (lastRetentionSuccess === null || lastRetentionSuccess <= 0) {
    retention = evidence(
      'unknown',
      'Retention has no recorded successful run.',
      'legacy kline_retention_status',
    )
  } else {
    retention = timedEvidence(
      lastRetentionSuccess,
      now,
      36 * 60 * 60,
      36 * 60 * 60,
      'legacy kline_retention_status',
      {
        live: 'Retention succeeded within the expected daily window.',
        cached: 'Retention succeeded within the expected daily window.',
        stale: 'Retention success is older than 36 hours.',
      },
    )
    if (retention.state === 'cached') retention.state = 'live'
  }
  return { storage, disk, retention }
}

function legacyReferenceStatus(
  raw: Record<string, unknown> | undefined,
  failed: boolean,
  now: number,
): StatusEvidence {
  if (failed) {
    return evidence(
      'degraded',
      'CEX Spot reference overview is unavailable.',
      'legacy asset_price_index',
    )
  }
  if (!raw || !hasOwn(raw, 'priced_asset_count')) {
    return evidence(
      'unknown',
      'The backend did not report CEX Spot reference coverage.',
      'legacy asset_price_index',
    )
  }
  if ((finiteNumber(raw.priced_asset_count) ?? 0) <= 0) {
    return evidence(
      'degraded',
      'No CEX Spot reference prices are available.',
      'legacy asset_price_index',
    )
  }
  return timedEvidence(
    finiteNumber(raw.index_updated_at),
    now,
    30,
    300,
    'legacy asset_price_index',
    {
      live: 'CEX Spot reference data is current.',
      cached: 'CEX Spot reference data is using the retained last success.',
      stale: 'CEX Spot reference data is stale.',
    },
  )
}

function legacyRouteStatus(
  inputs: LegacySystemInputs,
  now: number,
): StatusEvidence {
  if (inputs.uniswapFailed && inputs.pancakeFailed) {
    return evidence(
      'degraded',
      'DEX route summaries are unavailable.',
      'legacy Uniswap and PancakeSwap route summaries',
    )
  }
  const summaries = [inputs.uniswapOverview, inputs.pancakeOverview].filter(
    (value): value is Record<string, unknown> => value !== undefined,
  )
  if (summaries.length === 0 ||
    summaries.every((summary) => !hasOwn(summary, 'routable_asset_count'))) {
    return evidence(
      'unknown',
      'The backend did not report DEX route coverage.',
      'legacy Uniswap and PancakeSwap route summaries',
    )
  }
  const routable = summaries.reduce(
    (sum, summary) => sum + (finiteNumber(summary.routable_asset_count) ?? 0),
    0,
  )
  if (routable <= 0) {
    return evidence(
      'degraded',
      'No current DEX route prices are available.',
      'legacy Uniswap and PancakeSwap route summaries',
    )
  }
  const latest = summaries.reduce(
    (maximum, summary) => Math.max(maximum, finiteNumber(summary.index_updated_at) ?? 0),
    0,
  )
  return timedEvidence(
    latest,
    now,
    60,
    300,
    'legacy Uniswap and PancakeSwap route summaries',
    {
      live: 'DEX route summaries are current.',
      cached: 'DEX route summaries are cached.',
      stale: 'DEX route summaries are stale.',
    },
  )
}

function legacyTradingComponents(
  inputs: LegacySystemInputs,
  now: number,
): Pick<SystemComponents, 'matching' | 'liquidity' | 'transport' | 'outbox'> {
  const hasStatus = inputs.tradingStatus !== undefined && !inputs.tradingStatusFailed
  const hasBook = inputs.orderBook !== undefined && !inputs.orderBookFailed
  const successes = Number(hasStatus) + Number(hasBook)
  const transport = successes === 2
    ? currentProbeEvidence(
      'live',
      'Trading status and order book reads both succeeded.',
      'legacy trading REST',
      now,
    )
    : successes === 1
      ? currentProbeEvidence(
        'degraded',
        'Only one trading read succeeded.',
        'legacy trading REST',
        now,
      )
      : evidence(
        'offline',
        'Trading status and order book are unreachable.',
        'legacy trading REST',
      )
  let matching: StatusEvidence
  if (!hasStatus) {
    matching = evidence('offline', 'Matching status is unreachable.', 'legacy trading status')
  } else if (!hasOwn(inputs.tradingStatus ?? {}, 'state') || !text(inputs.tradingStatus?.state)) {
    matching = currentProbeEvidence(
      'unknown',
      'Matching state was not reported.',
      'legacy trading status',
      now,
    )
  } else {
    const state = text(inputs.tradingStatus?.state).trim().toLowerCase()
    matching = currentProbeEvidence(
      state === 'ready' ? 'live' : 'degraded',
      state === 'ready'
        ? 'Matching engine explicitly reports ready.'
        : `Matching engine reports ${state}.`,
      'legacy trading status',
      now,
    )
  }
  let liquidity: StatusEvidence
  if (!hasBook) {
    liquidity = evidence('offline', 'Order book is unreachable.', 'legacy BTC-USDT order book')
  } else if (!hasOwn(inputs.orderBook ?? {}, 'bids') || !hasOwn(inputs.orderBook ?? {}, 'asks')) {
    liquidity = currentProbeEvidence(
      'unknown',
      'Order book sides were not reported.',
      'legacy BTC-USDT order book',
      now,
    )
  } else {
    const bids = asArray(inputs.orderBook?.bids)
    const asks = asArray(inputs.orderBook?.asks)
    liquidity = currentProbeEvidence(
      bids.length > 0 && asks.length > 0 ? 'live' : 'degraded',
      bids.length > 0 && asks.length > 0
        ? 'Two-sided BTC-USDT liquidity is visible.'
        : 'Two-sided BTC-USDT liquidity is not visible.',
      'legacy BTC-USDT order book',
      now,
    )
  }
  let outbox: StatusEvidence
  if (!hasStatus) {
    outbox = evidence('offline', 'Outbox status is unreachable.', 'legacy trading status')
  } else if (
    !hasOwn(inputs.tradingStatus ?? {}, 'outbox_state') ||
    !text(inputs.tradingStatus?.outbox_state)
  ) {
    outbox = currentProbeEvidence(
      'unknown',
      'The legacy backend does not expose outbox state.',
      'legacy trading status',
      now,
    )
  } else {
    const state = text(inputs.tradingStatus?.outbox_state).trim().toLowerCase()
    const hasError = text(inputs.tradingStatus?.outbox_last_error) !== ''
    outbox = currentProbeEvidence(
      state === 'ready' && !hasError ? 'live' : 'degraded',
      state === 'ready' && !hasError
        ? 'Outbox publisher explicitly reports ready.'
        : `Outbox publisher reports ${state}.`,
      'legacy trading status',
      now,
    )
    const lastPublished = Date.parse(text(inputs.tradingStatus?.outbox_last_published_at))
    if (Number.isFinite(lastPublished)) {
      outbox.last_success_at = lastPublished
      outbox.age_seconds = Math.max(0, Math.floor((now - lastPublished) / 1000))
    }
  }
  return { matching, liquidity, transport, outbox }
}

export function buildLegacySystemStatus(
  inputs: LegacySystemInputs,
  now = Date.now(),
): SystemStatusSnapshot {
  const overview = inputs.overview ?? {}
  const storageResult = legacyStorage(overview, now)
  const trading = legacyTradingComponents(inputs, now)
  const reference = legacyReferenceStatus(
    inputs.referenceOverview,
    inputs.referenceFailed === true,
    now,
  )
  const route = legacyRouteStatus(inputs, now)
  const components: SystemComponents = {
    ...trading,
    market_data: reference,
    database: inputs.overviewFailed
      ? evidence(
        'offline',
        'System overview is unavailable.',
        'legacy system overview database_status',
      )
      : legacyDatabaseStatus(overview.database_status),
    disk: storageResult.disk,
    retention: storageResult.retention,
  }
  const processes = [
    legacyProcess('crawler', 'Spot ingest supervisor', overview.crawler_status),
    legacyProcess('dex', 'DEX ingest supervisor', overview.dex_status),
    legacyProcess('worker', 'Repair worker', overview.worker_status),
    legacyProcess('dw', 'DW sync', overview.dw_status),
    legacyProcess('rpc', 'gRPC', overview.rpc_status),
    legacyProcess('redis', 'Redis', overview.redis_status),
    legacyProcess('database', 'PostgreSQL', overview.database_status),
    legacyProcess('api', 'API', overview.api_status),
  ]
  return {
    schema_version: 'system-status.legacy-adapter.v1',
    formula_version: 'system-display.v1',
    source_mode: 'legacy',
    generated_at: now,
    overall: deriveOverallStatus(components, 'legacy'),
    components,
    processes,
    storage: storageResult.storage,
    price_sources: [
      {
        key: 'route_price',
        label: 'Route price',
        status: route,
        source: 'Uniswap and PancakeSwap venue route summaries',
        meaning: 'Venue-specific indicative route quotes at the reported notional.',
        boundary: 'Never substituted for the CEX Spot reference display price.',
      },
      {
        key: 'reference_display_price',
        label: 'Reference display price',
        status: reference,
        source: 'asset_price_index built from fresh CEX Spot contributors',
        meaning: 'Read-only composite reference used for display and the virtual demo-maker.',
        boundary: 'Not an executable route price and never filled from DEX or mock data.',
      },
    ],
    provider_statuses: asArray(overview.provider_statuses).map(normalizeProvider),
  }
}

function fulfilledRecord(
  result: PromiseSettledResult<unknown>,
): Record<string, unknown> | undefined {
  return result.status === 'fulfilled' ? asRecord(result.value) : undefined
}

async function legacySystemStatus(): Promise<SystemStatusSnapshot> {
  const results = await Promise.allSettled([
    request<Record<string, unknown>>('/api/v1/get_system_overview').then((value) => value.result),
    request<Record<string, unknown>>('/api/v2/get_market_overview', { venue: 'all' }).then((value) => value.result),
    request<Record<string, unknown>>('/api/v2/get_market_overview', { venue: 'uniswap' }).then((value) => value.result),
    request<Record<string, unknown>>('/api/v2/get_market_overview', { venue: 'pancakeswap' }).then((value) => value.result),
    tradingAPI.status(),
    tradingAPI.orderBook(),
  ])
  if (results.every((result) => result.status === 'rejected')) {
    throw new ApiError('Network error: the API is unreachable')
  }
  return buildLegacySystemStatus({
    overview: fulfilledRecord(results[0]),
    overviewFailed: results[0].status === 'rejected',
    referenceOverview: fulfilledRecord(results[1]),
    referenceFailed: results[1].status === 'rejected',
    uniswapOverview: fulfilledRecord(results[2]),
    uniswapFailed: results[2].status === 'rejected',
    pancakeOverview: fulfilledRecord(results[3]),
    pancakeFailed: results[3].status === 'rejected',
    tradingStatus: fulfilledRecord(results[4]),
    tradingStatusFailed: results[4].status === 'rejected',
    orderBook: fulfilledRecord(results[5]),
    orderBookFailed: results[5].status === 'rejected',
  })
}

export async function getSystemStatus(): Promise<SystemStatusSnapshot> {
  try {
    const { result } = await request<Record<string, unknown>>(SYSTEM_STATUS_PATH)
    return normalizeSystemStatusSnapshot(result)
  } catch (error) {
    if (
      !(error instanceof ApiError) ||
      ![404, 405, 501].includes(error.status ?? 0)
    ) {
      throw error
    }
    return legacySystemStatus()
  }
}
