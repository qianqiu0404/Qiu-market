// Shared browser DTOs frozen by PRD-QM-TRADE-001. This file intentionally
// contains no requests or UI behavior; feature code consumes these contracts.

export type TradeV1OrderScope = 'all' | 'open' | 'history'
export type TradeV1SourceKind = 'event'
export type TradeV1BalanceBucket = 'available' | 'held'
export type TradeV1OrderStatus =
  | 'rejected'
  | 'open'
  | 'partially_filled'
  | 'filled'
  | 'canceled'
export type TradeV1LifecycleStatus = TradeV1OrderStatus | 'received'
export type TradeV1OrderEventType =
  | 'order_accepted'
  | 'order_rejected'
  | 'order_rested'
  | 'trade_executed'
  | 'order_filled'
  | 'order_canceled'
  | 'cancel_rejected'
  | 'self_trade_prevented'
export type TradeV1LedgerReason =
  | 'virtual_fund'
  | 'order_hold'
  | 'order_release'
  | 'trade_settlement'
  | 'other'

export interface TradeV1Order {
  id: string
  client_order_id: string
  market_id: 'BTC-USDT'
  side: 'buy' | 'sell'
  type: 'limit' | 'market'
  time_in_force: 'gtc' | 'ioc' | 'fok'
  post_only: boolean
  price: string
  original_quantity: string
  remaining_quantity: string
  filled_quantity: string
  average_fill_price: string
  original_quote_budget: string
  remaining_quote_budget: string
  spent_quote: string
  held_asset: '' | 'BTC' | 'USDT'
  held_amount: string
  status: TradeV1OrderStatus
  accepted_sequence: string
  last_sequence: string
  reject_reason: string
  created_at: string
  updated_at: string
}

export interface TradeV1AccountTrade {
  id: string
  market_id: 'BTC-USDT'
  order_id: string
  side: 'buy' | 'sell'
  liquidity_role: 'maker' | 'taker'
  price: string
  quantity: string
  quote_amount: string
  fee_asset: 'BTC' | 'USDT'
  fee_amount: string
  fee_rate_bps: string
  sequence: string
  event_index: number
  occurred_at: string
}

export interface TradeV1Fee {
  asset: 'BTC' | 'USDT'
  amount: string
  rate_bps: string
  role: 'maker' | 'taker'
}

export interface TradeV1BalanceEffect {
  asset: 'BTC' | 'USDT'
  bucket: TradeV1BalanceBucket
  amount: string
  reason: TradeV1LedgerReason
  transaction_id: string
}

export interface TradeV1OrderEvent {
  event_id: string
  market_id: 'BTC-USDT'
  order_id: string
  sequence: string
  event_index: number
  timeline_index: number
  source_kind: TradeV1SourceKind
  type: TradeV1OrderEventType
  status: TradeV1LifecycleStatus
  quantity: string
  price: string
  remaining_quantity: string
  remaining_quote_budget: string
  trade_id: string
  fee?: TradeV1Fee
  balance_effects: TradeV1BalanceEffect[]
  reason: string
  occurred_at: string
}

export interface TradeV1LedgerEntry {
  entry_id: string
  market_id: 'BTC-USDT'
  sequence: string
  transaction_id: string
  entry_index: number
  asset: 'BTC' | 'USDT'
  bucket: TradeV1BalanceBucket
  amount: string
  reason: TradeV1LedgerReason
  reference: string
  order_id: string
  trade_id: string
  occurred_at: string
}

export interface TradeV1OrderPage {
  orders: TradeV1Order[]
  next_cursor: string
}

export interface TradeV1AccountTradePage {
  trades: TradeV1AccountTrade[]
  next_cursor: string
}

export interface TradeV1OrderEventPage {
  events: TradeV1OrderEvent[]
  next_cursor: string
}

export interface TradeV1LedgerPage {
  entries: TradeV1LedgerEntry[]
  next_cursor: string
}
