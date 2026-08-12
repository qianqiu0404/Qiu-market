export interface Principal {
  account_id: string
  github_login: string
  admin: boolean
}

export interface SessionResponse {
  principal: Principal
  expires_at: string
}

export interface AuthCapabilities {
  github_oauth_enabled: boolean
  local_login_enabled: boolean
  /**
   * Explicit server-owned compatibility signal. Missing means unknown and must
   * never turn a recovery endpoint 404 into writable legacy mode.
   */
  recovery_gate_enabled?: boolean
  /** Local-only practice runtime capability. Missing means disabled. */
  practice_mode_enabled?: boolean
  /** The fixed, query-first starter funding workflow is available. */
  starter_funds_enabled?: boolean
  /** Qiu Virtual Liquidity may publish Post Only practice quotes. */
  virtual_liquidity_enabled?: boolean
}

export interface PriceLevel {
  price: string
  quantity: string
  order_count: number
}

export interface OrderBook {
  market_id: string
  sequence: string
  bids: PriceLevel[]
  asks: PriceLevel[]
}

export interface Fee {
  account_id: string
  asset: string
  amount: string
  rate_bps: string
  role: string
}

export interface Trade {
  id: string
  market_id: string
  price: string
  quantity: string
  quote_amount: string
  maker_order_id: string
  taker_order_id: string
  maker_account_id: string
  taker_account_id: string
  buyer_account_id: string
  seller_account_id: string
  buyer_fee?: Fee
  seller_fee?: Fee
}

export interface Order {
  id: string
  client_order_id: string
  account_id: string
  market_id: string
  side: string
  type: string
  time_in_force: string
  post_only: boolean
  price: string
  original_quantity: string
  remaining_quantity: string
  filled_quantity: string
  original_quote_budget: string
  remaining_quote_budget: string
  spent_quote: string
  held_asset: string
  held_amount: string
  status: string
  accepted_sequence: string
  last_sequence: string
  reject_reason: string
}

export interface Balance {
  asset: string
  available: string
  held: string
}

export interface TradingStatus {
  market_id: string
  state: string
  sequence: string
  queue_depth: number
  recovery_count: string
  last_error: string
  last_incident?: string
  last_incident_at?: string
  last_recovered_at?: string
  outbox_state?: string
  outbox_checkpoint_sequence?: string
  outbox_checkpoint_event_index?: number
  outbox_last_error?: string
  outbox_last_published_at?: string
  outbox_last_cleanup_at?: string
  virtual_liquidity?: VirtualLiquidityStatus
}

export type VirtualLiquidityState = 'disabled' | 'recovering' | 'active' | 'paused'

export interface VirtualLiquidityStatus {
  provider: string
  state: VirtualLiquidityState
  reason: string
  bid_levels: number
  ask_levels: number
  reference_observed_at: string
  last_refresh_at: string
}

export interface FundingRequestResult {
  market_id: string
  request_id: string
  funding_event_id: string
  sequence: string
  asset: 'BTC' | 'USDT'
  amount: string
  projection_result: 'applied'
  ledger_balanced: boolean
  occurred_at: string
}

export type TradingRecoveryPhase =
  | 'not_enabled'
  | 'uninitialized'
  | 'bootstrap'
  | 'dependencies_ready'
  | 'trading_replay'
  | 'reconciling'
  | 'read_only'
  | 'transport_warmup'
  | 'writable'
  | 'offline'
  | 'manual_review'

export interface TradingRecoveryProof {
  runtime_sequence: string
  state_hash: string
  ledger_balanced: boolean
  event_continuous: boolean
  projection_caught_up: boolean
  outbox_caught_up: boolean
  transport_healthy: boolean
}

export interface TradingRecoveryProvenance {
  production_origin: string
  deployment_id: string
  deployment_url: string
  release_commit: string
  source_digest: string
}

export interface TradingRecoveryStatus {
  supported: boolean
  schema_version: number | null
  market_id: string
  epoch_id: string
  phase: TradingRecoveryPhase
  proof: TradingRecoveryProof
  provenance: TradingRecoveryProvenance | null
  writes_enabled: boolean
  last_error: string
  continuity_uncertain: boolean
  continuity_error: string
  version: string
  started_at: string
  updated_at: string
}

export interface EventEnvelope {
  market_id: string
  sequence: string
  event_index: number
  event?: unknown
}

interface APIError {
  code: string
  message: string
}

const EMPTY_RECOVERY_PROOF: TradingRecoveryProof = {
  runtime_sequence: '0',
  state_hash: '',
  ledger_balanced: false,
  event_continuous: false,
  projection_caught_up: false,
  outbox_caught_up: false,
  transport_healthy: false,
}

export function recoveryNotEnabled(): TradingRecoveryStatus {
  return {
    supported: false,
    schema_version: null,
    market_id: '',
    epoch_id: '',
    phase: 'not_enabled',
    proof: { ...EMPTY_RECOVERY_PROOF },
    provenance: null,
    writes_enabled: false,
    last_error: '',
    continuity_uncertain: false,
    continuity_error: '',
    version: '0',
    started_at: '',
    updated_at: '',
  }
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {}
}

function text(value: unknown): string {
  return value == null ? '' : String(value)
}

function bool(value: unknown): boolean {
  return value === true
}

function decimalText(value: unknown, field: string): string {
  if (typeof value === 'string' && /^\d+$/.test(value)) return value
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) {
    return String(value)
  }
  throw new Error(`Trading recovery ${field} is not a safe decimal value`)
}

const RECOVERY_PHASES = new Set<TradingRecoveryPhase>([
  'bootstrap',
  'dependencies_ready',
  'trading_replay',
  'reconciling',
  'read_only',
  'transport_warmup',
  'writable',
  'offline',
  'manual_review',
])

export function normalizeRecoveryStatus(value: unknown): TradingRecoveryStatus {
  const raw = record(value)
  const nestedProof = record(raw.proof)
  const proof = Object.keys(nestedProof).length ? nestedProof : raw
  const phase = text(raw.phase) as TradingRecoveryPhase
  const provenance = record(raw.provenance)
  if (
    raw.schema_version !== 2 ||
    text(raw.market_id) !== 'BTC-USDT' ||
    !RECOVERY_PHASES.has(phase) ||
    !text(raw.epoch_id) ||
    typeof raw.continuity_uncertain !== 'boolean'
  ) {
    throw new Error('Trading recovery status is malformed')
  }
  const version = decimalText(raw.version, 'version')
  if (!/^[1-9]\d*$/.test(version)) {
    throw new Error('Trading recovery version must be a positive decimal value')
  }
  const productionOrigin = text(provenance.production_origin)
  const deploymentID = text(provenance.deployment_id)
  const deploymentURL = text(provenance.deployment_url)
  const releaseCommit = text(provenance.release_commit).toLowerCase()
  const sourceDigest = text(provenance.source_digest).toLowerCase()
  let production: URL
  let deployment: URL
  try {
    production = new URL(productionOrigin)
    deployment = new URL(deploymentURL)
  } catch {
    throw new Error('Trading recovery provenance is malformed')
  }
  if (
    production.protocol !== 'https:' || production.origin !== productionOrigin ||
    deployment.protocol !== 'https:' || deployment.origin !== deploymentURL ||
    deployment.port !== '' || !deployment.hostname.endsWith('.vercel.app') ||
    deploymentURL === productionOrigin ||
    !/^dpl_[A-Za-z0-9]{8,128}$/.test(deploymentID) ||
    !/^[0-9a-f]{40}$/.test(releaseCommit) || !/^[0-9a-f]{64}$/.test(sourceDigest)
  ) {
    throw new Error('Trading recovery provenance is malformed')
  }
  return {
    supported: true,
    schema_version: 2,
    market_id: text(raw.market_id),
    epoch_id: text(raw.epoch_id),
    phase,
    proof: {
      runtime_sequence: decimalText(proof.runtime_sequence, 'runtime_sequence'),
      state_hash: text(proof.state_hash),
      ledger_balanced: bool(proof.ledger_balanced),
      event_continuous: bool(proof.event_continuous),
      projection_caught_up: bool(proof.projection_caught_up),
      outbox_caught_up: bool(proof.outbox_caught_up),
      transport_healthy: bool(proof.transport_healthy),
    },
    provenance: {
      production_origin: productionOrigin,
      deployment_id: deploymentID,
      deployment_url: deploymentURL,
      release_commit: releaseCommit,
      source_digest: sourceDigest,
    },
    writes_enabled: bool(raw.writes_enabled),
    last_error: text(raw.last_error),
    continuity_uncertain: bool(raw.continuity_uncertain),
    continuity_error: text(raw.continuity_error),
    version,
    started_at: text(raw.started_at),
    updated_at: text(raw.updated_at),
  }
}

export class TradingRequestError extends Error {
  constructor(
    message: string,
    readonly code: string,
    readonly status: number,
    readonly uncertain: boolean,
  ) {
    super(message)
    this.name = 'TradingRequestError'
  }
}

function recoveryGateEnabled(value: unknown): boolean {
  if (!value || typeof value !== 'object' || Array.isArray(value)
    || typeof (value as Record<string, unknown>).recovery_gate_enabled !== 'boolean') {
    throw new TradingRequestError(
      'Recovery capability is missing or malformed',
      'invalid_recovery_capability',
      0,
      false,
    )
  }
  return (value as Record<string, unknown>).recovery_gate_enabled as boolean
}

const base = '/api/v1/trading'
export const TRADING_WRITE_TIMEOUT_MS = 10_000

function queryPath(
  path: string,
  values: Record<string, string | number | undefined>,
): string {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(values)) {
    if (value !== undefined && value !== '') query.set(key, String(value))
  }
  const encoded = query.toString()
  return encoded ? `${path}?${encoded}` : path
}

function cookie(name: string): string {
  const prefix = `${encodeURIComponent(name)}=`
  for (const item of document.cookie.split(';')) {
    const trimmed = item.trim()
    if (trimmed.startsWith(prefix)) {
      return decodeURIComponent(trimmed.slice(prefix.length))
    }
  }
  return ''
}

async function request<T>(
  path: string,
  options: RequestInit = {},
  write = false,
): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if (write) headers.set('X-CSRF-Token', cookie('s78_trading_csrf'))
  let timedOut = false
  let timeout: ReturnType<typeof setTimeout> | undefined
  let detachAbort: (() => void) | undefined
  let signal = options.signal
  if (write) {
    const controller = new AbortController()
    const abortFromCaller = () => controller.abort(options.signal?.reason)
    if (options.signal?.aborted) {
      abortFromCaller()
    } else if (options.signal) {
      options.signal.addEventListener('abort', abortFromCaller, { once: true })
      detachAbort = () => options.signal?.removeEventListener('abort', abortFromCaller)
    }
    timeout = globalThis.setTimeout(() => {
      timedOut = true
      controller.abort()
    }, TRADING_WRITE_TIMEOUT_MS)
    signal = controller.signal
  }
  let response: Response
  try {
    response = await fetch(`${base}${path}`, {
      ...options,
      headers,
      credentials: 'same-origin',
      signal,
    })
  } catch (error) {
    throw new TradingRequestError(
      timedOut
        ? 'Trading write timed out; request outcome is unknown'
        : (error instanceof Error ? error.message : 'Network request failed'),
      timedOut ? 'request_timeout' : 'network_error',
      0,
      write,
    )
  } finally {
    if (timeout !== undefined) globalThis.clearTimeout(timeout)
    detachAbort?.()
  }
  if (!response.ok) {
    let failure: APIError = {
      code: 'request_failed',
      message: `Request failed (HTTP ${response.status})`,
    }
    try {
      failure = await response.json() as APIError
    } catch {
      // Keep the bounded fallback; never surface an HTML proxy body as data.
    }
    throw new TradingRequestError(
      failure.message,
      failure.code,
      response.status,
      write && failure.code !== 'recovery_in_progress' &&
      failure.code !== 'trading_write_paused' && (
        response.status === 502 ||
        response.status === 503 ||
        response.status === 504 ||
        failure.code === 'backend_timeout' ||
        failure.code === 'backend_unavailable'
      ),
    )
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const tradingAPI = {
  authCapabilities: () => request<AuthCapabilities>('/auth/capabilities'),
  session: () => request<SessionResponse>('/session'),
  localLogin: () => request<{ principal: Principal }>(
    '/auth/local',
    { method: 'POST', body: '{}' },
  ),
  logout: () => request<void>('/auth/logout', { method: 'POST', body: '{}' }, true),
  orderBook: () => request<OrderBook>('/markets/BTC-USDT/orderbook?levels=20'),
  publicTrades: () => request<{ trades: Trade[] }>('/markets/BTC-USDT/trades?limit=40'),
  status: () => request<TradingStatus>('/markets/BTC-USDT/status'),
  recoveryStatus: async (): Promise<TradingRecoveryStatus> => {
    const enabled = recoveryGateEnabled(await request<unknown>('/auth/capabilities'))
    if (!enabled) return recoveryNotEnabled()
    return normalizeRecoveryStatus(await request<unknown>('/recovery/status'))
  },
  balances: () => request<{ balances: Balance[] }>('/balances'),
  orders: (openOnly = false) => request<{ orders: Order[] }>(
    `/orders?limit=100&open_only=${openOnly}`,
  ),
  orderPage: (
    scope: TradeV1OrderScope,
    cursor = '',
    limit = 50,
  ) => request<TradeV1OrderPage>(queryPath('/orders', { scope, cursor, limit })),
  order: (orderID: string) => request<TradeV1Order>(
    `/orders/${encodeURIComponent(orderID)}`,
  ),
  trades: () => request<{ trades: Trade[] }>('/trades?limit=100'),
  accountTradePage: (cursor = '', limit = 50) =>
    request<TradeV1AccountTradePage>(queryPath('/account/trades', { cursor, limit })),
  orderEventPage: (orderID: string, cursor = '', limit = 50) =>
    request<TradeV1OrderEventPage>(queryPath(
      `/orders/${encodeURIComponent(orderID)}/events`,
      { cursor, limit },
    )),
  ledgerPage: (
    cursor = '',
    limit = 50,
    asset: 'all' | 'BTC' | 'USDT' = 'all',
    reason: 'all' | 'virtual_fund' | 'order_hold' | 'order_release' |
      'trade_settlement' | 'other' = 'all',
  ) => request<TradeV1LedgerPage>(queryPath('/ledger/entries', {
    cursor,
    limit,
    asset,
    reason,
  })),
  submit: (body: Record<string, unknown>) => request<unknown>(
    '/orders',
    { method: 'POST', body: JSON.stringify(body) },
    true,
  ),
  cancel: (orderID: string, requestID: string) => request<unknown>(
    `/orders/${encodeURIComponent(orderID)}/cancel`,
    { method: 'POST', body: JSON.stringify({ request_id: requestID }) },
    true,
  ),
  fund: (requestID: string, asset: string, amount: string, accountID = '') =>
    request<unknown>(
      '/admin/fund',
      {
        method: 'POST',
        body: JSON.stringify({
          request_id: requestID,
          account_id: accountID,
          asset,
          amount,
        }),
      },
      true,
    ),
  fundingRequest: (requestID: string) => request<FundingRequestResult>(
    `/account/funding/${encodeURIComponent(requestID)}`,
  ),
  ticket: () => request<{ ticket: string; expires_at: string }>(
    '/ws-ticket',
    { method: 'POST', body: '{}' },
    true,
  ),
}

export function eventSocketURL(ticket: string, cursor?: EventEnvelope): string {
  const configuredOrigin = (import.meta.env.VITE_TRADING_WS_ORIGIN ?? '').trim()
  const socketURL = new URL(configuredOrigin || location.origin)
  if (socketURL.protocol === 'https:') socketURL.protocol = 'wss:'
  if (socketURL.protocol === 'http:') socketURL.protocol = 'ws:'
  if (socketURL.protocol !== 'ws:' && socketURL.protocol !== 'wss:') {
    throw new Error('VITE_TRADING_WS_ORIGIN must use HTTPS or WSS')
  }
  const query = new URLSearchParams({ ticket })
  if (cursor) {
    query.set('sequence', cursor.sequence)
    query.set('event_index', String(cursor.event_index))
  }
  socketURL.pathname = `${base}/events/ws`
  socketURL.search = query.toString()
  socketURL.hash = ''
  return socketURL.toString()
}

export function tradingEventMode(): 'websocket' | 'polling' {
  return import.meta.env.VITE_TRADING_EVENT_MODE === 'polling'
    ? 'polling'
    : 'websocket'
}
import type {
  TradeV1AccountTradePage,
  TradeV1LedgerPage,
  TradeV1Order,
  TradeV1OrderEventPage,
  TradeV1OrderPage,
  TradeV1OrderScope,
} from './trade-v1-contract'
