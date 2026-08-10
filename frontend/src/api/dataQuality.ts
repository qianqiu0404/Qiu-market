export const DATA_QUALITY_SCHEMA = 'data-quality/v1' as const

export type DataQualityStatus = 'insufficient' | 'healthy' | 'degraded' | 'quarantined' | 'recovering'
export type DataQualityOverallStatus = DataQualityStatus | 'unconfigured'
export type DataQualityLicense = 'approved' | 'unknown' | 'restricted' | 'prohibited'
export type DataQualityGrade = 'A' | 'B' | 'C' | 'D' | 'F'
export type DataQualitySource = 'binance_spot' | 'coinglass_derivatives' | 'xiuqiu_research'
export type DataQualityCapability =
  | 'spot_ticker' | 'ohlcv' | 'open_interest' | 'liquidation'
  | 'research_summary' | 'research_events'
export type DataQualityMetric =
  | 'freshness' | 'latency' | 'availability' | 'completeness' | 'schema' | 'consistency'
  | 'duplicate' | 'conflict' | 'out_of_order' | 'future' | 'stale' | 'unit' | 'precision'
  | 'identity' | 'coverage' | 'rate_limit' | 'upstream_5xx' | 'timeout' | 'cache_hit'
  | 'stale_serve' | 'research_source' | 'research_watch' | 'research_invalidation'
  | 'research_legacy' | 'research_priority' | 'content_hash_conflict'
export type DataQualityOutcome =
  | 'success' | 'rate_limit' | 'upstream_5xx' | 'timeout' | 'bad_payload' | 'unsupported'
  | 'auth' | 'permission' | 'network' | 'unconfigured' | 'stale'

export interface DataQualityCounter {
  numerator: number
  denominator: number
  bps: number | null
}

export interface DataQualityDimension extends DataQualityCounter {
  metric: DataQualityMetric
  polarity: 'positive' | 'fault' | 'informational'
}

export interface DataQualityCapabilityItem {
  capability: DataQualityCapability
  maxAgeSeconds: number
  sampleCount: number
  validSampleCount: number
  minSamples: number
  successCount: number
  lastAttemptAt: string | null
  lastSuccessAt: string | null
  ageSeconds: number | null
  coverage: DataQualityCounter
  status: DataQualityStatus
  reasons: string[]
}

export interface DataQualityPriorityCounts { p0: number; p1: number; p2: number }
export interface DataQualityGate {
  status: DataQualityStatus
  healthyWindowStreak: number
  recoveryRequired: number
  reasons: string[]
}

export interface DataQualityItem {
  source: DataQualitySource
  sourceName: string
  class: 'spot' | 'derivatives' | 'research'
  windowStart: string | null
  windowEnd: string | null
  windowSeconds: number | null
  sampleCount: number
  minSamples: number
  attemptCount: number
  successCount: number
  lastAttemptAt: string | null
  lastSuccessAt: string | null
  ageSeconds: number | null
  coverage: DataQualityCounter
  technicalScoreBps: number | null
  grade: DataQualityGrade | null
  status: DataQualityStatus
  reasons: string[]
  license: DataQualityLicense
  publicEligible: boolean
  tradeEligible: false
  readOnlyUse: 'market_context' | 'derivatives_context' | 'research_context'
  capabilities: DataQualityCapabilityItem[]
  dimensions: DataQualityDimension[]
  errorCounts: Partial<Record<DataQualityOutcome, number>>
  cacheHitCount: number
  staleServeCount: number
  priorityCounts: DataQualityPriorityCounts
  gate: DataQualityGate
}

export interface DataQualitySummary {
  schemaVersion: typeof DATA_QUALITY_SCHEMA
  status: DataQualityOverallStatus
  generatedAt: string
  items: DataQualityItem[]
  error: string | null
}

export class DataQualityRequestError extends Error {
  readonly code: string

  constructor(message: string, code: string) {
    super(message)
    this.name = 'DataQualityRequestError'
    this.code = code
  }
}

const SOURCE_RULES = {
  binance_spot: { name: 'Binance Public', class: 'spot', use: 'market_context', capabilities: ['spot_ticker', 'ohlcv'] },
  coinglass_derivatives: { name: 'CoinGlass', class: 'derivatives', use: 'derivatives_context', capabilities: ['open_interest', 'liquidation'] },
  xiuqiu_research: { name: 'xiuqiu-site Market Radar', class: 'research', use: 'research_context', capabilities: ['research_summary', 'research_events'] },
} as const
const SOURCES = new Set<DataQualitySource>(Object.keys(SOURCE_RULES) as DataQualitySource[])
const STATUSES = new Set<DataQualityStatus>(['insufficient', 'healthy', 'degraded', 'quarantined', 'recovering'])
const OVERALL = new Set<DataQualityOverallStatus>(['unconfigured', ...STATUSES])
const LICENSES = new Set<DataQualityLicense>(['approved', 'unknown', 'restricted', 'prohibited'])
const GRADES = new Set<DataQualityGrade>(['A', 'B', 'C', 'D', 'F'])
const METRICS = new Set<DataQualityMetric>([
  'freshness', 'latency', 'availability', 'completeness', 'schema', 'consistency', 'duplicate',
  'conflict', 'out_of_order', 'future', 'stale', 'unit', 'precision', 'identity', 'coverage',
  'rate_limit', 'upstream_5xx', 'timeout', 'cache_hit', 'stale_serve', 'research_source',
  'research_watch', 'research_invalidation', 'research_legacy', 'research_priority', 'content_hash_conflict',
])
const REQUIRED_DIMENSIONS = ['freshness', 'availability', 'completeness', 'schema', 'consistency', 'coverage'] as const
const OUTCOMES = new Set<DataQualityOutcome>([
  'success', 'rate_limit', 'upstream_5xx', 'timeout', 'bad_payload', 'unsupported', 'auth',
  'permission', 'network', 'unconfigured', 'stale',
])
const CAPABILITY_HARD_FAULT_REASONS = new Set([
  'hard_fault', 'future', 'schema', 'identity', 'unit', 'precision', 'conflict',
  'stale_serve', 'content_hash_conflict', 'stale',
])
const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/
const MAX_BODY = 256 * 1024
const TIMEOUT_MS = 8_000

function invalid(message: string): DataQualityRequestError {
  return new DataQualityRequestError(`Invalid data quality response: ${message}`, 'invalid_response')
}

function record(value: unknown, field: string): Record<string, unknown> {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) throw invalid(`${field} must be an object`)
  return value as Record<string, unknown>
}

function text(value: unknown, field: string, max = 256): string {
  if (typeof value !== 'string' || !value || value !== value.trim() || [...value].length > max || /[\u0000-\u001f\u007f]/u.test(value)) {
    throw invalid(`${field} is invalid`)
  }
  return value
}

function integer(value: unknown, field: string, maximum = Number.MAX_SAFE_INTEGER): number {
  if (!Number.isSafeInteger(value) || Number(value) < 0 || Number(value) > maximum) throw invalid(`${field} is invalid`)
  return Number(value)
}

function timestamp(value: unknown, field: string): string {
  const result = text(value, field, 64)
  if (!RFC3339.test(result) || !Number.isFinite(Date.parse(result))) throw invalid(`${field} must be RFC3339`)
  return result
}

function nullableTimestamp(value: unknown, field: string): string | null {
  return value === null ? null : timestamp(value, field)
}

function nullableInteger(value: unknown, field: string, maximum = Number.MAX_SAFE_INTEGER): number | null {
  return value === null ? null : integer(value, field, maximum)
}

function reasons(value: unknown, field: string): string[] {
  if (!Array.isArray(value) || value.length > 32) throw invalid(`${field} is invalid`)
  const result = value.map((reason, index) => text(reason, `${field}[${index}]`, 128))
  if (new Set(result).size !== result.length) throw invalid(`${field} contains duplicates`)
  return result
}

function parseCounter(value: unknown, field: string): DataQualityCounter {
  const raw = record(value, field)
  const numerator = integer(raw.numerator, `${field}.numerator`)
  const denominator = integer(raw.denominator, `${field}.denominator`)
  const bps = nullableInteger(raw.bps, `${field}.bps`, 10_000)
  if (numerator > denominator || (denominator === 0) !== (bps === null)) throw invalid(`${field} ratio is inconsistent`)
  if (bps !== null && bps !== Math.round(numerator * 10_000 / denominator)) throw invalid(`${field}.bps is not exact`)
  return { numerator, denominator, bps }
}

function parseCapability(value: unknown, source: DataQualitySource, index: number): DataQualityCapabilityItem {
  const field = `item.capabilities[${index}]`
  const raw = record(value, field)
  const capability = text(raw.capability, `${field}.capability`, 64) as DataQualityCapability
  if (!SOURCE_RULES[source].capabilities.includes(capability as never)) throw invalid(`${field}.capability conflicts with source`)
  const status = text(raw.status, `${field}.status`, 32) as DataQualityStatus
  if (!STATUSES.has(status)) throw invalid(`${field}.status is unsupported`)
  const sampleCount = integer(raw.sampleCount, `${field}.sampleCount`)
  const validSampleCount = integer(raw.validSampleCount, `${field}.validSampleCount`)
  const minSamples = integer(raw.minSamples, `${field}.minSamples`)
  const successCount = integer(raw.successCount, `${field}.successCount`)
  const lastAttemptAt = nullableTimestamp(raw.lastAttemptAt, `${field}.lastAttemptAt`)
  const lastSuccessAt = nullableTimestamp(raw.lastSuccessAt, `${field}.lastSuccessAt`)
  const ageSeconds = nullableInteger(raw.ageSeconds, `${field}.ageSeconds`)
  const capabilityReasons = reasons(raw.reasons, `${field}.reasons`)
  if (minSamples === 0 || successCount > sampleCount || validSampleCount > successCount) throw invalid(`${field} sample counters are inconsistent`)
  if ((sampleCount === 0) !== (lastAttemptAt === null)) throw invalid(`${field}.lastAttemptAt conflicts with samples`)
  if ((successCount === 0) !== (lastSuccessAt === null && ageSeconds === null)) throw invalid(`${field} success time is inconsistent`)
  if (validSampleCount < minSamples) {
    const hardFaultQuarantine = status === 'quarantined'
      && capabilityReasons.some((reason) => CAPABILITY_HARD_FAULT_REASONS.has(reason))
    if (status !== 'insufficient' && !hardFaultQuarantine) {
      throw invalid(`${field} below-minimum status lacks a hard-fault quarantine reason`)
    }
  }
  const coverage = parseCounter(raw.coverage, `${field}.coverage`)
  if (coverage.numerator !== Math.min(validSampleCount, minSamples) || coverage.denominator !== minSamples) throw invalid(`${field}.coverage conflicts with valid samples`)
  return {
    capability, maxAgeSeconds: integer(raw.maxAgeSeconds, `${field}.maxAgeSeconds`), sampleCount, validSampleCount,
    minSamples, successCount, lastAttemptAt, lastSuccessAt, ageSeconds,
    coverage, status, reasons: capabilityReasons,
  }
}

function expectedGrade(score: number): DataQualityGrade {
  if (score >= 9_000) return 'A'
  if (score >= 8_000) return 'B'
  if (score >= 7_000) return 'C'
  if (score >= 5_000) return 'D'
  return 'F'
}

function parseItem(value: unknown): DataQualityItem {
  const raw = record(value, 'item')
  const source = text(raw.source, 'item.source', 64) as DataQualitySource
  if (!SOURCES.has(source)) throw invalid('item.source is unsupported')
  const rule = SOURCE_RULES[source]
  if (raw.sourceName !== rule.name || raw.class !== rule.class || raw.readOnlyUse !== rule.use) throw invalid('item canonical identity conflicts with source')
  const status = text(raw.status, 'item.status', 32) as DataQualityStatus
  if (!STATUSES.has(status)) throw invalid('item.status is unsupported')
  const license = text(raw.license, 'item.license', 32) as DataQualityLicense
  if (!LICENSES.has(license)) throw invalid('item.license is unsupported')
  const gradeRaw = raw.grade
  const grade = gradeRaw === null ? null : text(gradeRaw, 'item.grade', 1) as DataQualityGrade
  if (grade !== null && !GRADES.has(grade)) throw invalid('item.grade is unsupported')
  const score = nullableInteger(raw.technicalScoreBps, 'item.technicalScoreBps', 10_000)
  if ((score === null) !== (grade === null) || (score !== null && grade !== expectedGrade(score))) throw invalid('item score and grade are inconsistent')

  const windowStart = nullableTimestamp(raw.windowStart, 'item.windowStart')
  const windowEnd = nullableTimestamp(raw.windowEnd, 'item.windowEnd')
  const windowSeconds = nullableInteger(raw.windowSeconds, 'item.windowSeconds')
  const noWindow = windowStart === null && windowEnd === null && windowSeconds === null
  if (!noWindow && (windowStart === null || windowEnd === null || windowSeconds === null)) throw invalid('item window fields must be jointly absent or present')
  if (!noWindow && (Date.parse(windowEnd!) - Date.parse(windowStart!)) / 1000 !== windowSeconds) throw invalid('item windowSeconds is inconsistent')

  const sampleCount = integer(raw.sampleCount, 'item.sampleCount')
  const minSamples = integer(raw.minSamples, 'item.minSamples')
  const attemptCount = integer(raw.attemptCount, 'item.attemptCount')
  const successCount = integer(raw.successCount, 'item.successCount')
  const lastAttemptAt = nullableTimestamp(raw.lastAttemptAt, 'item.lastAttemptAt')
  const lastSuccessAt = nullableTimestamp(raw.lastSuccessAt, 'item.lastSuccessAt')
  const ageSeconds = nullableInteger(raw.ageSeconds, 'item.ageSeconds')
  if (successCount > attemptCount || attemptCount > sampleCount) throw invalid('item attempt counters are inconsistent')
  if ((attemptCount === 0) !== (lastAttemptAt === null)) throw invalid('item.lastAttemptAt conflicts with attempts')
  if ((successCount === 0) !== (lastSuccessAt === null && ageSeconds === null)) throw invalid('item success time is inconsistent')
  if ((status === 'insufficient' || attemptCount === 0 || sampleCount < minSamples) && (score !== null || grade !== null)) throw invalid('insufficient item cannot have a score')

  if (!Array.isArray(raw.capabilities) || raw.capabilities.length !== 2) throw invalid('item.capabilities must contain two entries')
  const capabilities = raw.capabilities.map((value, index) => parseCapability(value, source, index))
  if (capabilities.map(({ capability }) => capability).join(',') !== rule.capabilities.join(',')) throw invalid('item.capabilities order/identity is unstable')
  if (status === 'healthy' && capabilities.some((capability) => capability.status !== 'healthy')) throw invalid('healthy source contains non-healthy capability')

  if (!Array.isArray(raw.dimensions) || raw.dimensions.length > 32) throw invalid('item.dimensions is invalid')
  const dimensions = raw.dimensions.map((value, index): DataQualityDimension => {
    const field = `item.dimensions[${index}]`
    const dimension = record(value, field)
    const metric = text(dimension.metric, `${field}.metric`, 64) as DataQualityMetric
    if (!METRICS.has(metric)) throw invalid(`${field}.metric is unsupported`)
    const polarity = text(dimension.polarity, `${field}.polarity`, 32) as DataQualityDimension['polarity']
    if (!['positive', 'fault', 'informational'].includes(polarity)) throw invalid(`${field}.polarity is unsupported`)
    return { metric, polarity, ...parseCounter(dimension, field) }
  })
  if (new Set(dimensions.map(({ metric }) => metric)).size !== dimensions.length) throw invalid('item.dimensions contains duplicates')
  if (attemptCount > 0 && REQUIRED_DIMENSIONS.some((metric) => !dimensions.some((dimension) => dimension.metric === metric))) throw invalid('item.dimensions is incomplete')

  const errorRaw = record(raw.errorCounts, 'item.errorCounts')
  const errorCounts: Partial<Record<DataQualityOutcome, number>> = {}
  for (const [key, value] of Object.entries(errorRaw)) {
    if (!OUTCOMES.has(key as DataQualityOutcome)) throw invalid('item.errorCounts contains an unsupported outcome')
    errorCounts[key as DataQualityOutcome] = integer(value, `item.errorCounts.${key}`)
  }
  const countedErrors = Object.values(errorCounts).reduce((total, count) => total + (count ?? 0), 0)
  if (successCount + countedErrors !== attemptCount) throw invalid('item outcomes do not reconcile with attempts')

  const priorityRaw = record(raw.priorityCounts, 'item.priorityCounts')
  const priorityCounts = {
    p0: integer(priorityRaw.p0, 'item.priorityCounts.p0'),
    p1: integer(priorityRaw.p1, 'item.priorityCounts.p1'),
    p2: integer(priorityRaw.p2, 'item.priorityCounts.p2'),
  }
  if (source !== 'xiuqiu_research' && priorityCounts.p0 + priorityCounts.p1 + priorityCounts.p2 !== 0) throw invalid('priority counts belong only to research')
  const gateRaw = record(raw.gate, 'item.gate')
  const gateStatus = text(gateRaw.status, 'item.gate.status', 32) as DataQualityStatus
  if (!STATUSES.has(gateStatus) || gateStatus !== status) throw invalid('item.gate.status conflicts with source status')
  const recoveryRequired = integer(gateRaw.recoveryRequired, 'item.gate.recoveryRequired', 100)
  const healthyWindowStreak = integer(gateRaw.healthyWindowStreak, 'item.gate.healthyWindowStreak', 100)
  if (recoveryRequired === 0 || healthyWindowStreak > recoveryRequired) throw invalid('item.gate recovery counters are inconsistent')
  const gate = { status: gateStatus, healthyWindowStreak, recoveryRequired, reasons: reasons(gateRaw.reasons, 'item.gate.reasons') }
  const publicEligible = raw.publicEligible
  if (typeof publicEligible !== 'boolean' || raw.tradeEligible !== false) throw invalid('item eligibility is unsafe')
  if (license !== 'approved' && publicEligible) throw invalid('unapproved license cannot be public')
  const coverage = parseCounter(raw.coverage, 'item.coverage')
  if (publicEligible && (status !== 'healthy' || score === null || coverage.denominator === 0)) throw invalid('item public eligibility conflicts with quality')

  return {
    source, sourceName: rule.name, class: rule.class, windowStart, windowEnd, windowSeconds,
    sampleCount, minSamples, attemptCount, successCount, lastAttemptAt, lastSuccessAt, ageSeconds,
    coverage, technicalScoreBps: score, grade, status, reasons: reasons(raw.reasons, 'item.reasons'),
    license, publicEligible, tradeEligible: false, readOnlyUse: rule.use, capabilities, dimensions,
    errorCounts, cacheHitCount: integer(raw.cacheHitCount, 'item.cacheHitCount'),
    staleServeCount: integer(raw.staleServeCount, 'item.staleServeCount'), priorityCounts, gate,
  }
}

async function readBoundedJSON(response: Response): Promise<unknown> {
  if (!response.body) throw new DataQualityRequestError('Data quality API returned an empty body', 'invalid_json')
  const reader = response.body.getReader()
  const chunks: Uint8Array[] = []
  let size = 0
  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      size += value.byteLength
      if (size > MAX_BODY) {
        await reader.cancel()
        throw new DataQualityRequestError('Data quality response is too large', 'response_too_large')
      }
      chunks.push(value)
    }
  } finally {
    reader.releaseLock()
  }
  const body = new Uint8Array(size)
  let offset = 0
  for (const chunk of chunks) { body.set(chunk, offset); offset += chunk.byteLength }
  try { return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(body)) }
  catch { throw new DataQualityRequestError('Data quality API returned invalid JSON', 'invalid_json') }
}

export async function getDataQualitySummary(): Promise<DataQualitySummary> {
  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), TIMEOUT_MS)
  try {
    const response = await fetch('/api/v1/data-quality/summary', {
      method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json' }, signal: controller.signal,
    })
    const mediaType = response.headers.get('Content-Type')?.split(';', 1)[0]?.trim().toLowerCase()
    if (mediaType !== 'application/json') throw new DataQualityRequestError('Data quality API returned an unsupported content type', 'invalid_content_type')
    const raw = record(await readBoundedJSON(response), 'response')
    if (raw.schemaVersion !== DATA_QUALITY_SCHEMA) throw invalid('response.schemaVersion is unsupported')
    const status = text(raw.status, 'response.status', 32) as DataQualityOverallStatus
    if (!OVERALL.has(status)) throw invalid('response.status is unsupported')
    if (!Array.isArray(raw.items) || raw.items.length !== 3) throw invalid('response.items must contain three sources')
    const items = raw.items.map(parseItem)
    if (items.map(({ source }) => source).join(',') !== Object.keys(SOURCE_RULES).join(',')) throw invalid('response.items identity/order is unstable')
    const error = raw.error === null ? null : text(raw.error, 'response.error', 1_000)
    if (status === 'unconfigured' && items.some((item) => item.status !== 'insufficient')) throw invalid('unconfigured response contains configured quality')
    if (error !== null && status !== 'degraded') throw invalid('response.error conflicts with status')
    if (error === null && status !== 'unconfigured' && status !== derivedOverallStatus(items)) throw invalid('response.status understates or overstates source quality')
    return { schemaVersion: DATA_QUALITY_SCHEMA, status, generatedAt: timestamp(raw.generatedAt, 'response.generatedAt'), items, error }
  } catch (error) {
    if (controller.signal.aborted) throw new DataQualityRequestError('Data quality API timed out', 'timeout')
    if (error instanceof DataQualityRequestError) throw error
    throw new DataQualityRequestError('Data quality API is unreachable', 'network_error')
  } finally {
    window.clearTimeout(timer)
  }
}

function derivedOverallStatus(items: DataQualityItem[]): DataQualityStatus {
  if (items.some((item) => item.status === 'quarantined')) return 'quarantined'
  if (items.some((item) => item.status === 'degraded' || item.status === 'recovering')) return 'degraded'
  if (items.some((item) => item.status === 'insufficient')) return 'insufficient'
  return 'healthy'
}
