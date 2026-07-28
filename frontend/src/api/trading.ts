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

const base = '/api/v1/trading'

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
  let response: Response
  try {
    response = await fetch(`${base}${path}`, {
      ...options,
      headers,
      credentials: 'same-origin',
    })
  } catch (error) {
    throw new TradingRequestError(
      error instanceof Error ? error.message : 'Network request failed',
      'network_error',
      0,
      write,
    )
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
      write && (
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
  balances: () => request<{ balances: Balance[] }>('/balances'),
  orders: (openOnly = false) => request<{ orders: Order[] }>(
    `/orders?limit=100&open_only=${openOnly}`,
  ),
  order: (orderID: string) => request<Order>(
    `/orders/${encodeURIComponent(orderID)}`,
  ),
  trades: () => request<{ trades: Trade[] }>('/trades?limit=100'),
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
