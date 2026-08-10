export const RESEARCH_SIGNAL_SCHEMA = 'researchsignal/v1' as const
export const RESEARCH_SIGNALS_SCHEMA = 'researchsignals/v1' as const

export type ResearchFeedStatus =
  | 'fresh'
  | 'empty'
  | 'legacy'
  | 'degraded'
  | 'stale'
  | 'partial'
  | 'unconfigured'

export type ResearchPriority = 'P0' | 'P1' | 'P2'

export interface ResearchSignalErrorBody {
  code: string
  message: string
  retryAfterSeconds: number | null
}

export interface ResearchSignalEvent {
  id: string
  schemaVersion: typeof RESEARCH_SIGNAL_SCHEMA
  type: 'market_event'
  title: string
  summary: string
  source: string
  provider: 'xiuqiu-site'
  sourceUrl: string
  assets: string[]
  eventTime: string
  observedAt: string | null
  receivedAt: string
  publishedAt: string
  freshness: 'fresh' | 'stale'
  /** Editorial research urgency from the publisher; never trading priority. */
  editorialPriority: ResearchPriority
  watchFor: string | null
  invalidation: string | null
  qualityFlags: Array<'observed_time_missing' | 'legacy_fields_missing' | 'stale' | 'duplicate'>
  contentHash: string
  executable: false
  sourceKind: 'xiuqiu_automated_dynamic'
}

export interface ResearchSignalEventPage {
  schemaVersion: typeof RESEARCH_SIGNALS_SCHEMA
  status: ResearchFeedStatus
  generatedAt: string
  data: { items: ResearchSignalEvent[]; nextCursor: string | null }
  error: ResearchSignalErrorBody | null
}

export interface ResearchSignalDetail {
  schemaVersion: typeof RESEARCH_SIGNALS_SCHEMA
  status: ResearchFeedStatus
  generatedAt: string
  data: { item: ResearchSignalEvent | null }
  error: ResearchSignalErrorBody | null
}

export interface ResearchSignalSourceSummary {
  source: string
  status: 'healthy' | 'degraded' | 'unconfigured'
  lastSuccessAt: string | null
  message: string | null
}

export interface ResearchSignalSummary {
  schemaVersion: typeof RESEARCH_SIGNALS_SCHEMA
  status: ResearchFeedStatus
  generatedAt: string
  data: {
    latestEventAt: string | null
    freshnessMinutes: number | null
    isDelayed: boolean
    eventCount24h: number
    p0Count24h: number
    p1Count24h: number
    sources: ResearchSignalSourceSummary[]
  }
  error: ResearchSignalErrorBody | null
}

export class ResearchSignalsRequestError extends Error {
  readonly status: number
  readonly code: string
  readonly retryAfterSeconds: number | null

  constructor(message: string, status: number, code = 'research_request_failed', retryAfterSeconds: number | null = null) {
    super(message)
    this.name = 'ResearchSignalsRequestError'
    this.status = status
    this.code = code
    this.retryAfterSeconds = retryAfterSeconds
  }
}

const FEED_STATUSES = new Set<ResearchFeedStatus>([
  'fresh', 'empty', 'legacy', 'degraded', 'stale', 'partial', 'unconfigured',
])
const PRIORITIES = new Set<ResearchPriority>(['P0', 'P1', 'P2'])
const QUALITY_FLAGS = new Set<ResearchSignalEvent['qualityFlags'][number]>([
  'observed_time_missing', 'legacy_fields_missing', 'stale', 'duplicate',
])
const ERROR_CODES = new Set([
  'disabled', 'bad_request', 'not_found', 'rate_limit', 'timeout',
  'network', 'upstream', 'bad_payload', 'conflict',
])
const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/
const EVENT_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$/
const ASSET_SYMBOL = /^[A-Z0-9][A-Z0-9._-]{0,31}$/
const REQUEST_TIMEOUT_MS = 8_000
const MAX_RESPONSE_BYTES = 512 * 1024
const MAX_CURSOR_BYTES = 512
const SOURCE_NAME = 'xiuqiu-site Market Radar'
const SOURCE_ORIGIN = 'https://xiuqiu-site.vercel.app'
const SOURCE_PATH_PREFIX = '/market-radar/events/'

function malformed(message: string): ResearchSignalsRequestError {
  return new ResearchSignalsRequestError(`Invalid research signal response: ${message}`, 0, 'invalid_response')
}

function record(value: unknown, field: string): Record<string, unknown> {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw malformed(`${field} must be an object`)
  }
  return value as Record<string, unknown>
}

function stringValue(value: unknown, field: string, options: {
  max?: number
  allowEmpty?: boolean
  trimmed?: boolean
} = {}): string {
  const { max = 4_000, allowEmpty = false, trimmed = true } = options
  if (typeof value !== 'string' || (!allowEmpty && value.trim() === '')) {
    throw malformed(`${field} must be a${allowEmpty ? '' : ' non-empty'} string`)
  }
  if ((trimmed && value !== value.trim()) || [...value].length > max || /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/u.test(value)) {
    throw malformed(`${field} exceeds its contract bounds`)
  }
  return value
}

function nullableString(value: unknown, field: string, max = 1_000): string | null {
  if (value === null) return null
  return stringValue(value, field, { max })
}

function timestamp(value: unknown, field: string): string {
  const text = stringValue(value, field, { max: 64 })
  if (!RFC3339.test(text) || !Number.isFinite(Date.parse(text))) {
    throw malformed(`${field} must be an RFC3339 timestamp`)
  }
  return text
}

function nullableTimestamp(value: unknown, field: string): string | null {
  if (value === null) return null
  return timestamp(value, field)
}

function statusValue(value: unknown, field: string): ResearchFeedStatus {
  const text = stringValue(value, field, { max: 32 }) as ResearchFeedStatus
  if (!FEED_STATUSES.has(text)) throw malformed(`${field} is unsupported`)
  return text
}

function errorBody(value: unknown): ResearchSignalErrorBody | null {
  if (value === null) return null
  if (value === undefined) throw malformed('error must be explicitly null or an object')
  const raw = record(value, 'error')
  if (!Object.prototype.hasOwnProperty.call(raw, 'retryAfterSeconds')) {
    throw malformed('error.retryAfterSeconds must be explicit')
  }
  const retry = raw.retryAfterSeconds
  if (retry != null && (!Number.isSafeInteger(retry) || Number(retry) < 0)) {
    throw malformed('error.retryAfterSeconds must be a non-negative integer or null')
  }
  const code = stringValue(raw.code, 'error.code', { max: 64 })
  if (!ERROR_CODES.has(code)) throw malformed('error.code is unsupported')
  return {
    code,
    message: stringValue(raw.message, 'error.message', { max: 1_000 }),
    retryAfterSeconds: retry == null ? null : Number(retry),
  }
}

function sourceURL(value: unknown, id: string): string {
  const text = stringValue(value, 'item.sourceUrl', { max: 2_048 })
  let parsed: URL
  try {
    parsed = new URL(text)
  } catch {
    throw malformed('item.sourceUrl must be an absolute HTTPS URL')
  }
  const sourceID = parsed.pathname.startsWith(SOURCE_PATH_PREFIX)
    ? parsed.pathname.slice(SOURCE_PATH_PREFIX.length)
    : ''
  let decodedID = ''
  try {
    decodedID = decodeURIComponent(sourceID)
  } catch {
    throw malformed('item.sourceUrl event ID encoding is invalid')
  }
  if (parsed.origin !== SOURCE_ORIGIN || parsed.username || parsed.password || parsed.search || parsed.hash || decodedID !== id) {
    throw malformed('item.sourceUrl must be the canonical publisher detail URL')
  }
  return parsed.toString()
}

function parseItem(value: unknown): ResearchSignalEvent {
  const raw = record(value, 'item')
  if (raw.schemaVersion !== RESEARCH_SIGNAL_SCHEMA) throw malformed('item.schemaVersion is unsupported')
  if (raw.type !== 'market_event') throw malformed('item.type is unsupported')
  if (raw.provider !== 'xiuqiu-site') throw malformed('item.provider is unsupported')
  if (raw.sourceKind !== 'xiuqiu_automated_dynamic') throw malformed('item.sourceKind is unsupported')
  if (raw.executable !== false) throw malformed('item.executable must be false')
  if (raw.observedAt !== null) throw malformed('item.observedAt must remain explicitly null')

  const id = stringValue(raw.id, 'item.id', { max: 160 })
  if (!EVENT_ID.test(id)) throw malformed('item.id is unsupported')
  const priority = stringValue(raw.priority, 'item.priority', { max: 2 }) as ResearchPriority
  if (!PRIORITIES.has(priority)) throw malformed('item.priority is unsupported')
  const freshness = stringValue(raw.freshness, 'item.freshness', { max: 8 })
  if (freshness !== 'fresh' && freshness !== 'stale') throw malformed('item.freshness is unsupported')
  const watchFor = nullableString(raw.watchFor, 'item.watchFor')
  const invalidation = nullableString(raw.invalidation, 'item.invalidation')

  if (!Array.isArray(raw.assets) || raw.assets.length === 0 || raw.assets.length > 32) {
    throw malformed('item.assets must contain 1..32 canonical assets')
  }
  const assets = raw.assets.map((item, index) => stringValue(item, `item.assets[${index}]`, { max: 32 }))
  if (assets.some((asset) => !ASSET_SYMBOL.test(asset)) || !assets.includes('BTC') || new Set(assets).size !== assets.length) {
    throw malformed('item.assets must be unique canonical symbols including BTC')
  }

  if (!Array.isArray(raw.qualityFlags) || raw.qualityFlags.length > QUALITY_FLAGS.size) {
    throw malformed('item.qualityFlags exceeds its contract bounds')
  }
  const qualityFlags = raw.qualityFlags.map((item, index) =>
    stringValue(item, `item.qualityFlags[${index}]`, { max: 64 })) as ResearchSignalEvent['qualityFlags']
  if (qualityFlags.some((flag) => !QUALITY_FLAGS.has(flag)) || new Set(qualityFlags).size !== qualityFlags.length) {
    throw malformed('item.qualityFlags contains unsupported or duplicate values')
  }
  if (!qualityFlags.includes('observed_time_missing')) {
    throw malformed('item.qualityFlags must preserve missing observation-time evidence')
  }
  if (qualityFlags.includes('stale') !== (freshness === 'stale')) {
    throw malformed('item stale flag must agree with item freshness')
  }
  if (qualityFlags.includes('legacy_fields_missing') !== (watchFor === null && invalidation === null)) {
    throw malformed('item legacy flag must agree with missing research conditions')
  }

  const contentHash = stringValue(raw.contentHash, 'item.contentHash', { max: 71 })
  if (!/^sha256:[0-9a-f]{64}$/.test(contentHash)) throw malformed('item.contentHash must be a lowercase SHA-256 digest')
  return {
    id,
    schemaVersion: RESEARCH_SIGNAL_SCHEMA,
    type: 'market_event',
    title: stringValue(raw.title, 'item.title', { max: 300 }),
    summary: stringValue(raw.summary, 'item.summary', { max: 4_000 }),
    source: (() => {
      const source = stringValue(raw.source, 'item.source', { max: 160 })
      if (source !== SOURCE_NAME) throw malformed('item.source must identify the canonical publisher')
      return source
    })(),
    provider: 'xiuqiu-site',
    sourceUrl: sourceURL(raw.sourceUrl, id),
    assets,
    eventTime: timestamp(raw.eventTime, 'item.eventTime'),
    observedAt: null,
    receivedAt: timestamp(raw.receivedAt, 'item.receivedAt'),
    publishedAt: timestamp(raw.publishedAt, 'item.publishedAt'),
    freshness,
    editorialPriority: priority,
    watchFor,
    invalidation,
    qualityFlags,
    contentHash,
    executable: false,
    sourceKind: 'xiuqiu_automated_dynamic',
  }
}

function envelope(value: unknown): {
  raw: Record<string, unknown>
  status: ResearchFeedStatus
  generatedAt: string
  error: ResearchSignalErrorBody | null
} {
  const raw = record(value, 'response')
  if (raw.schemaVersion !== RESEARCH_SIGNALS_SCHEMA) throw malformed('response.schemaVersion is unsupported')
  return {
    raw,
    status: statusValue(raw.status, 'response.status'),
    generatedAt: timestamp(raw.generatedAt, 'response.generatedAt'),
    error: errorBody(raw.error),
  }
}

async function requestJSON(path: string): Promise<unknown> {
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS)
  try {
    return await requestJSONWithSignal(path, controller)
  } catch (error) {
    if (controller.signal.aborted) {
      throw new ResearchSignalsRequestError('Research signal API timed out', 0, 'timeout')
    }
    throw error
  } finally {
    window.clearTimeout(timeout)
  }
}

async function requestJSONWithSignal(path: string, controller: AbortController): Promise<unknown> {
  let response: Response
  try {
    response = await fetch(path, {
      method: 'GET',
      headers: { Accept: 'application/json' },
      credentials: 'same-origin',
      signal: controller.signal,
    })
  } catch {
    if (controller.signal.aborted) {
      throw new ResearchSignalsRequestError('Research signal API timed out', 0, 'timeout')
    }
    throw new ResearchSignalsRequestError('Research signal API is unreachable', 0, 'network_error')
  }

  const contentType = response.headers.get('Content-Type')?.split(';', 1)[0]?.trim().toLowerCase()
  if (contentType !== 'application/json') {
    throw new ResearchSignalsRequestError('Research signal API returned an unsupported content type', response.status, 'invalid_content_type')
  }
  const contentLength = response.headers.get('Content-Length')
  const declaredLength = contentLength === null ? null : Number(contentLength)
  if (declaredLength !== null && Number.isFinite(declaredLength) && declaredLength > MAX_RESPONSE_BYTES) {
    throw new ResearchSignalsRequestError('Research signal API response is too large', response.status, 'response_too_large')
  }

  let payload: unknown
  try {
    const body = await readBoundedBody(response)
    payload = JSON.parse(body)
  } catch (error) {
    if (error instanceof ResearchSignalsRequestError) throw error
    throw new ResearchSignalsRequestError('Research signal API returned invalid JSON', response.status, 'invalid_json')
  }
  // HTTP error responses use the same fixed-shape, typed read contract. Parse
  // them below so the UI can distinguish degraded from unconfigured without
  // treating either as a verified empty feed.
  return payload
}

async function readBoundedBody(response: Response): Promise<string> {
  if (!response.body) {
    const body = await response.arrayBuffer()
    if (body.byteLength > MAX_RESPONSE_BYTES) throw responseTooLarge(response.status)
    try {
      return new TextDecoder('utf-8', { fatal: true }).decode(body)
    } catch {
      throw new ResearchSignalsRequestError('Research signal API returned invalid UTF-8', response.status, 'invalid_json')
    }
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8', { fatal: true })
  let bytes = 0
  let text = ''
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      bytes += value.byteLength
      if (bytes > MAX_RESPONSE_BYTES) {
        await reader.cancel()
        throw responseTooLarge(response.status)
      }
      text += decoder.decode(value, { stream: true })
    }
    return text + decoder.decode()
  } catch (error) {
    if (error instanceof ResearchSignalsRequestError) throw error
    throw new ResearchSignalsRequestError('Research signal API returned invalid UTF-8', response.status, 'invalid_json')
  }
}

function responseTooLarge(status: number): ResearchSignalsRequestError {
  return new ResearchSignalsRequestError('Research signal API response is too large', status, 'response_too_large')
}

function validOpaque(value: string): boolean {
  return Boolean(value)
    && value === value.trim()
    && new TextEncoder().encode(value).byteLength <= MAX_CURSOR_BYTES
    && !/[\u0000-\u0020\u007f\s]/u.test(value)
}

function validateOpaque(value: string, field: string): void {
  if (!validOpaque(value)) {
    throw new ResearchSignalsRequestError(`${field} is invalid`, 0, 'invalid_request')
  }
}

function validateEventState(status: ResearchFeedStatus, items: ResearchSignalEvent[], error: ResearchSignalErrorBody | null): void {
  if (status === 'empty' && (items.length !== 0 || error !== null)) throw malformed('empty response must have no items or error')
  if ((status === 'fresh' || status === 'stale') && (items.length === 0 || error !== null)) {
    throw malformed(`${status} response must contain items and no error`)
  }
  if ((status === 'legacy' || status === 'partial') && error !== null) {
    throw malformed(`${status} response must not include an error`)
  }
  if (status === 'degraded' || status === 'unconfigured') {
    if (error === null || items.length !== 0) throw malformed(`${status} response must contain an error and no items`)
  }
  if (status === 'fresh' && items.some((item) => item.freshness !== 'fresh')) throw malformed('fresh response contains stale items')
  if (status === 'stale' && items.some((item) => item.freshness !== 'stale')) throw malformed('stale response contains fresh items')
  if (status !== 'partial' && items.some((item) => item.qualityFlags.includes('duplicate'))) {
    throw malformed('duplicate evidence requires a partial response')
  }
}

function validateDetailState(status: ResearchFeedStatus, item: ResearchSignalEvent | null, error: ResearchSignalErrorBody | null): void {
  if ((status === 'fresh' || status === 'legacy' || status === 'stale') && (item === null || error !== null)) {
    throw malformed(`${status} detail must contain an item and no error`)
  }
  if (status === 'empty' && (item !== null || error?.code !== 'not_found')) {
    throw malformed('empty detail must contain an explicit not-found error and no item')
  }
  if (status === 'degraded' || status === 'unconfigured') {
    if (error === null || item !== null) throw malformed(`${status} detail must contain an error and no item`)
  }
  if (status === 'partial') throw malformed('partial detail is unsupported')
}

export async function getResearchSignalEvents(options: {
  limit?: number
  cursor?: string
} = {}): Promise<ResearchSignalEventPage> {
  const limit = options.limit ?? 20
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 50) {
    throw new ResearchSignalsRequestError('Research event limit must be between 1 and 50', 0, 'invalid_request')
  }
  const query = new URLSearchParams({ market: 'crypto', asset: 'BTC', window: '168', limit: String(limit) })
  if (options.cursor !== undefined) {
    validateOpaque(options.cursor, 'Research cursor')
    query.set('cursor', options.cursor)
  }
  const parsed = envelope(await requestJSON(`/api/v1/research/signals/events?${query.toString()}`))
  const data = record(parsed.raw.data, 'response.data')
  if (!Array.isArray(data.items) || data.items.length > 50) throw malformed('response.data.items must be an array of at most 50 items')
  const nextCursor = data.nextCursor
  if (nextCursor !== null && typeof nextCursor !== 'string') throw malformed('response.data.nextCursor must be a string or null')
  if (typeof nextCursor === 'string' && !validOpaque(nextCursor)) throw malformed('response.data.nextCursor is invalid')
  const items = data.items.map(parseItem)
  validateEventState(parsed.status, items, parsed.error)
  if (parsed.status === 'empty' && nextCursor !== null) throw malformed('empty response must not have a next cursor')
  if ((parsed.status === 'degraded' || parsed.status === 'unconfigured') && nextCursor !== null) {
    throw malformed(`${parsed.status} response must not have a next cursor`)
  }
  return {
    schemaVersion: RESEARCH_SIGNALS_SCHEMA,
    status: parsed.status,
    generatedAt: parsed.generatedAt,
    data: { items, nextCursor },
    error: parsed.error,
  }
}

export async function getResearchSignalEvent(id: string): Promise<ResearchSignalDetail> {
  const trimmed = id.trim()
  if (!EVENT_ID.test(trimmed)) {
    throw new ResearchSignalsRequestError('Research event ID is invalid', 0, 'invalid_request')
  }
  const parsed = envelope(await requestJSON(`/api/v1/research/signals/events/${encodeURIComponent(trimmed)}`))
  const data = record(parsed.raw.data, 'response.data')
  const item = data.item == null ? null : parseItem(data.item)
  if (item !== null && item.id !== trimmed) throw malformed('detail item ID does not match the request')
  validateDetailState(parsed.status, item, parsed.error)
  return {
    schemaVersion: RESEARCH_SIGNALS_SCHEMA,
    status: parsed.status,
    generatedAt: parsed.generatedAt,
    data: { item },
    error: parsed.error,
  }
}

function nonNegativeInteger(value: unknown, field: string): number {
  if (!Number.isSafeInteger(value) || Number(value) < 0) throw malformed(`${field} must be a non-negative integer`)
  return Number(value)
}

export async function getResearchSignalSummary(): Promise<ResearchSignalSummary> {
  const parsed = envelope(await requestJSON('/api/v1/research/signals/summary'))
  const data = record(parsed.raw.data, 'response.data')
  const freshness = data.freshnessMinutes
  if (freshness != null && (!Number.isSafeInteger(freshness) || Number(freshness) < 0)) {
    throw malformed('response.data.freshnessMinutes must be a non-negative integer or null')
  }
  if (typeof data.isDelayed !== 'boolean') throw malformed('response.data.isDelayed must be boolean')
  if (!Array.isArray(data.sources) || data.sources.length > 16) throw malformed('response.data.sources exceeds its contract bounds')
  const sources = data.sources.map((value, index): ResearchSignalSourceSummary => {
    const source = record(value, `response.data.sources[${index}]`)
    const sourceStatus = stringValue(source.status, `response.data.sources[${index}].status`, { max: 32 })
    if (sourceStatus !== 'healthy' && sourceStatus !== 'degraded' && sourceStatus !== 'unconfigured') {
      throw malformed(`response.data.sources[${index}].status is unsupported`)
    }
    return {
      source: stringValue(source.source, `response.data.sources[${index}].source`, { max: 160 }),
      status: sourceStatus,
      lastSuccessAt: nullableTimestamp(source.lastSuccessAt, `response.data.sources[${index}].lastSuccessAt`),
      message: nullableString(source.message, `response.data.sources[${index}].message`),
    }
  })
  if ((parsed.status === 'fresh' || parsed.status === 'empty' || parsed.status === 'stale') && parsed.error !== null) {
    throw malformed(`${parsed.status} summary must not include an error`)
  }
  if ((parsed.status === 'degraded' || parsed.status === 'unconfigured') && parsed.error === null) {
    throw malformed(`${parsed.status} summary must include an error`)
  }
  if (parsed.status === 'legacy' || parsed.status === 'partial') throw malformed(`${parsed.status} summary is unsupported`)
  const eventCount24h = nonNegativeInteger(data.eventCount24h, 'response.data.eventCount24h')
  const p0Count24h = nonNegativeInteger(data.p0Count24h, 'response.data.p0Count24h')
  const p1Count24h = nonNegativeInteger(data.p1Count24h, 'response.data.p1Count24h')
  if (p0Count24h + p1Count24h > eventCount24h) throw malformed('response.data priority counts exceed the event count')
  if (parsed.status === 'empty' && eventCount24h !== 0) throw malformed('empty summary must have zero events')
  return {
    schemaVersion: RESEARCH_SIGNALS_SCHEMA,
    status: parsed.status,
    generatedAt: parsed.generatedAt,
    data: {
      latestEventAt: nullableTimestamp(data.latestEventAt, 'response.data.latestEventAt'),
      freshnessMinutes: freshness == null ? null : Number(freshness),
      isDelayed: data.isDelayed,
      eventCount24h,
      p0Count24h,
      p1Count24h,
      sources,
    },
    error: parsed.error,
  }
}
