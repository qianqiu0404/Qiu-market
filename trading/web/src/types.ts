export interface Principal {
  account_id: string
  github_login: string
  admin: boolean
}

export interface SessionResponse {
  principal: Principal
  expires_at: string
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

export interface Status {
  market_id: string
  state: string
  sequence: string
  queue_depth: number
  recovery_count: string
  last_error: string
}

export interface EventEnvelope {
  market_id: string
  sequence: string
  event_index: number
  event?: unknown
}

export interface APIError {
  code: string
  message: string
}
