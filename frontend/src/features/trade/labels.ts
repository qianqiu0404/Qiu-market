import type { MessageKey } from '../../i18n'

const enumValues = new Set([
  'buy', 'sell', 'limit', 'market', 'gtc', 'ioc', 'fok',
  'open', 'partially_filled', 'filled', 'canceled', 'rejected', 'received',
  'maker', 'taker', 'available', 'held', 'virtual_fund', 'order_hold',
  'order_release', 'trade_settlement', 'other', 'order_accepted',
  'order_rejected', 'order_rested', 'trade_executed', 'order_filled',
  'order_canceled', 'cancel_rejected', 'self_trade_prevented', 'yes', 'no',
  'ready', 'connecting', 'recovering', 'failed', 'healthy', 'unavailable',
  'live', 'polling', 'offline', 'stale', 'paused', 'disabled',
  'fresh', 'high', 'medium', 'low', 'unknown',
  'active', 'one-sided', 'websocket', 'reconnecting',
  'post_only_would_cross', 'fok_not_fillable', 'insufficient_balance',
])

export function tradeEnumKey(value: string): MessageKey {
  return enumValues.has(value)
    ? `trade.enum.${value}` as MessageKey
    : 'trade.enum.unavailable'
}

const exactWriteReasons = new Set([
  'request_in_flight', 'login_required', 'reconcile_pending',
  'transport_reconcile_pending', 'validation_failed', 'matching_status_missing',
  'matching_status_stale', 'transport_not_reconciled', 'liquidity_paused',
])

export function tradeWriteReasonKey(value: string): MessageKey {
  if (exactWriteReasons.has(value)) return `trade.status.reason.${value}` as MessageKey
  if (value.startsWith('recovery_')) return 'trade.status.reason.recovery'
  return 'trade.status.reason.runtime'
}
