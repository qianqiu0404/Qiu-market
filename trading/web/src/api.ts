import type {
  APIError,
  Balance,
  EventEnvelope,
  Order,
  OrderBook,
  SessionResponse,
  Status,
  Trade,
} from './types'

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
  const response = await fetch(`${base}${path}`, {
    ...options,
    headers,
    credentials: 'same-origin',
  })
  if (!response.ok) {
    let failure: APIError = { code: 'request_failed', message: `HTTP ${response.status}` }
    try {
      failure = await response.json() as APIError
    } catch {
      // Keep the bounded fallback; never expose an HTML proxy error as app data.
    }
    throw new Error(failure.message)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export const tradingAPI = {
  session: () => request<SessionResponse>('/session'),
  localLogin: () => request<{ principal: SessionResponse['principal'] }>(
    '/auth/local',
    { method: 'POST', body: '{}' },
  ),
  logout: () => request<void>('/auth/logout', { method: 'POST', body: '{}' }, true),
  orderBook: () => request<OrderBook>('/markets/BTC-USDT/orderbook?levels=20'),
  publicTrades: () => request<{ trades: Trade[] }>('/markets/BTC-USDT/trades?limit=40'),
  status: () => request<Status>('/markets/BTC-USDT/status'),
  balances: () => request<{ balances: Balance[] }>('/balances'),
  orders: (openOnly = false) => request<{ orders: Order[] }>(
    `/orders?limit=100&open_only=${openOnly}`,
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
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const query = new URLSearchParams({ ticket })
  if (cursor) {
    query.set('sequence', cursor.sequence)
    query.set('event_index', String(cursor.event_index))
  }
  return `${protocol}//${location.host}${base}/events/ws?${query}`
}
