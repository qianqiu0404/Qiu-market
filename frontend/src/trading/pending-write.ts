export type PendingTradingOperation = 'submit' | 'cancel' | 'fund'
export type PendingTradingState = 'unknown' | 'reconciling'

export interface PendingTradingWrite {
  operation: PendingTradingOperation
  account_id: string
  request_id: string
  state: PendingTradingState
  created_at: number
  order_id?: string
  payload: Record<string, string | boolean>
}

export interface PendingOrderFact {
  id: string
  client_order_id: string
  status: string
}

export const PENDING_TRADING_WRITE_STORAGE_KEY =
  'qiu-market.pending-trading-write.v1'

const terminalOrderStatuses = new Set([
  'filled',
  'canceled',
  'rejected',
  'expired',
])

export function parsePendingTradingWrite(
  raw: string | null,
): PendingTradingWrite | null {
  if (!raw) return null
  try {
    const value = JSON.parse(raw) as Partial<PendingTradingWrite>
    if (
      !['submit', 'cancel', 'fund'].includes(value.operation ?? '') ||
      !['unknown', 'reconciling'].includes(value.state ?? '') ||
      typeof value.account_id !== 'string' ||
      value.account_id.length === 0 ||
      typeof value.request_id !== 'string' ||
      value.request_id.length === 0 ||
      typeof value.created_at !== 'number' ||
      !Number.isFinite(value.created_at) ||
      value.payload === null ||
      typeof value.payload !== 'object' ||
      Array.isArray(value.payload)
    ) {
      return null
    }
    if (
      value.operation === 'cancel' &&
      (typeof value.order_id !== 'string' || value.order_id.length === 0)
    ) {
      return null
    }
    return {
      operation: value.operation as PendingTradingOperation,
      account_id: value.account_id,
      request_id: value.request_id,
      state: value.state as PendingTradingState,
      created_at: value.created_at,
      order_id: typeof value.order_id === 'string' ? value.order_id : undefined,
      payload: value.payload as Record<string, string | boolean>,
    }
  } catch {
    return null
  }
}

export function pendingTradingWriteResolvedByOrders(
  pending: PendingTradingWrite,
  orders: PendingOrderFact[],
): boolean {
  if (pending.operation === 'submit') {
    return orders.some((order) => order.client_order_id === pending.request_id)
  }
  if (pending.operation === 'cancel') {
    return orders.some(
      (order) =>
        order.id === pending.order_id &&
        terminalOrderStatuses.has(order.status),
    )
  }
  return false
}
