import { createApp, h, nextTick, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TradeV1Order, TradeV1OrderEvent } from '../../api/trade-v1-contract'
import {
  recoveryNotEnabled,
  TradingRequestError,
  tradingAPI,
} from '../../api/trading'
import {
  pendingTradingWriteMutationAllowed,
  PENDING_TRADING_WRITE_STORAGE_KEY,
  readPersistedPendingTradingWrite,
  type PendingTradingWrite,
} from '../../trading/pending-write'
import { useTradeTerminal } from './useTradeTerminal'

vi.mock('../../api/market', () => ({
  getAssetDashboardV2: vi.fn().mockRejectedValue(new Error('reference unavailable in unit test')),
  getAssetMarketsV2: vi.fn().mockResolvedValue([]),
  getKlines: vi.fn().mockResolvedValue([]),
}))

type Terminal = ReturnType<typeof useTradeTerminal>

let mountedApp: App<Element> | null = null
let host: HTMLElement | null = null
let terminal: Terminal

const otherTabFund: PendingTradingWrite = {
  operation_id: 'operation-system-fund',
  operation: 'fund',
  account_id: 'github:admin',
  request_id: 'fund-stable-id',
  state: 'unknown',
  created_at: 1,
  updated_at: 2,
  payload: { account_id: 'github:beneficiary', asset: 'USDT', amount: '100' },
}

const selectedOrder: TradeV1Order = {
  id: 'order-private-a',
  client_order_id: 'client-private-a',
  market_id: 'BTC-USDT',
  side: 'buy',
  type: 'limit',
  time_in_force: 'gtc',
  post_only: true,
  price: '65000',
  original_quantity: '0.001',
  remaining_quantity: '0.001',
  filled_quantity: '0',
  average_fill_price: '0',
  original_quote_budget: '0',
  remaining_quote_budget: '0',
  spent_quote: '0',
  held_asset: 'USDT',
  held_amount: '65',
  status: 'open',
  accepted_sequence: '1',
  last_sequence: '1',
  reject_reason: '',
  created_at: '2026-08-05T00:00:00Z',
  updated_at: '2026-08-05T00:00:00Z',
}

const selectedEvent = {
  event_id: 'event-private-a',
  market_id: 'BTC-USDT',
  order_id: selectedOrder.id,
  sequence: '1',
  event_index: 0,
  timeline_index: 0,
  source_kind: 'event',
  type: 'order_accepted',
  status: 'open',
  quantity: '0.001',
  price: '65000',
  remaining_quantity: '0.001',
  remaining_quote_budget: '0',
  trade_id: '',
  balance_effects: [],
  reason: '',
  occurred_at: '2026-08-05T00:00:00Z',
} satisfies TradeV1OrderEvent

async function settle(): Promise<void> {
  await Promise.resolve()
  await nextTick()
  await new Promise((resolve) => window.setTimeout(resolve, 0))
  await nextTick()
}

async function mountTerminal(): Promise<Terminal> {
  host = document.createElement('div')
  document.body.append(host)
  mountedApp = createApp({
    setup() {
      terminal = useTradeTerminal()
      return () => h('div')
    },
  })
  mountedApp.mount(host)
  await settle()
  return terminal
}

function makeTerminalWritable(current: Terminal): void {
  current.principal.value = {
    account_id: 'github:trader',
    github_login: 'trader',
    admin: false,
  }
  current.wsState.value = 'live'
  current.balances.value = [
    { asset: 'BTC', available: '1', held: '0' },
    { asset: 'USDT', available: '1000', held: '0' },
  ]
  current.form.type = 'limit'
  current.form.side = 'buy'
  current.form.timeInForce = 'gtc'
  current.form.postOnly = true
  current.form.price = '65000'
  current.form.quantity = '0.001'
}

function seedPrivateDrawer(current: Terminal): void {
  current.principal.value = {
    account_id: 'github:trader',
    github_login: 'trader',
    admin: false,
  }
  current.selectedOrder.value = selectedOrder
  current.orderEvents.value = [selectedEvent]
  current.eventPage.cursor = 'private-event-cursor'
  current.eventPage.nextCursor = 'private-event-next'
  current.eventPage.page = 3
  current.pageBusy.events = true
}

beforeEach(() => {
  vi.spyOn(tradingAPI, 'authCapabilities').mockResolvedValue({
    github_oauth_enabled: false,
    local_login_enabled: false,
  })
  vi.spyOn(tradingAPI, 'orderBook').mockResolvedValue({
    market_id: 'BTC-USDT',
    sequence: '1',
    bids: [{ price: '64999', quantity: '1', order_count: 1 }],
    asks: [{ price: '65000', quantity: '1', order_count: 1 }],
  })
  vi.spyOn(tradingAPI, 'publicTrades').mockResolvedValue({ trades: [] })
  vi.spyOn(tradingAPI, 'status').mockResolvedValue({
    market_id: 'BTC-USDT',
    state: 'ready',
    sequence: '1',
    queue_depth: 0,
    recovery_count: '0',
    last_error: '',
  })
  vi.spyOn(tradingAPI, 'recoveryStatus').mockResolvedValue(recoveryNotEnabled())
})

afterEach(() => {
  mountedApp?.unmount()
  host?.remove()
  mountedApp = null
  host = null
  window.localStorage.clear()
  window.sessionStorage.clear()
  vi.restoreAllMocks()
})

describe('Trade pending-write journal ownership', () => {
  it('prefers the shared local journal and rejects stale-tab replacement or clearing', () => {
    window.localStorage.setItem(
      PENDING_TRADING_WRITE_STORAGE_KEY,
      JSON.stringify(otherTabFund),
    )
    window.sessionStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, JSON.stringify({
      ...otherTabFund,
      operation_id: 'stale-session-submit',
      operation: 'submit',
    }))

    const authoritative = readPersistedPendingTradingWrite(
      window.localStorage,
      window.sessionStorage,
    )
    expect(authoritative).toEqual(otherTabFund)
    expect(pendingTradingWriteMutationAllowed(authoritative, {
      ...otherTabFund,
      operation_id: 'new-trade-submit',
      operation: 'submit',
    }, null)).toBe(false)
    expect(pendingTradingWriteMutationAllowed(
      authoritative,
      null,
      'stale-session-submit',
    )).toBe(false)
  })

  it('mirrors cross-tab journal creation and removal without writing it back', async () => {
    const current = await mountTerminal()
    const persisted = JSON.stringify(otherTabFund)

    window.localStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, persisted)
    expect(current.pendingWrite.value).toBeNull()
    window.dispatchEvent(new StorageEvent('storage', {
      key: PENDING_TRADING_WRITE_STORAGE_KEY,
      newValue: persisted,
      storageArea: window.localStorage,
    }))
    expect(current.pendingWrite.value).toEqual(otherTabFund)
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBe(persisted)

    window.localStorage.removeItem(PENDING_TRADING_WRITE_STORAGE_KEY)
    window.dispatchEvent(new StorageEvent('storage', {
      key: PENDING_TRADING_WRITE_STORAGE_KEY,
      oldValue: persisted,
      newValue: null,
      storageArea: window.localStorage,
    }))
    expect(current.pendingWrite.value).toBeNull()
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBeNull()
  })

  it('re-reads the authoritative journal immediately before a submit', async () => {
    const current = await mountTerminal()
    makeTerminalWritable(current)
    expect(current.submitEnabled.value).toBe(true)
    const submit = vi.spyOn(tradingAPI, 'submit')

    // Deliberately omit a storage event: this models another tab writing in
    // the gap between the last render and the user click.
    window.localStorage.setItem(
      PENDING_TRADING_WRITE_STORAGE_KEY,
      JSON.stringify(otherTabFund),
    )
    expect(current.pendingWrite.value).toBeNull()
    await current.submitOrder()

    expect(submit).not.toHaveBeenCalled()
    expect(current.pendingWrite.value).toEqual(otherTabFund)
    expect(JSON.parse(
      window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY) ?? '{}',
    )).toEqual(otherTabFund)
  })

  it('fails closed instead of overwriting a malformed nonempty shared journal', async () => {
    const current = await mountTerminal()
    makeTerminalWritable(current)
    const submit = vi.spyOn(tradingAPI, 'submit')
    window.localStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, '{malformed')

    await current.submitOrder()

    expect(submit).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBe('{malformed')
  })

  it('fails closed on a malformed legacy session journal during migration', async () => {
    window.sessionStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, '{malformed-session')
    const current = await mountTerminal()
    makeTerminalWritable(current)
    const submit = vi.spyOn(tradingAPI, 'submit')

    expect(current.pendingJournalBlocked.value).toBe(true)
    expect(current.submitEnabled.value).toBe(false)
    await current.submitOrder()

    expect(submit).not.toHaveBeenCalled()
    expect(window.sessionStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY))
      .toBe('{malformed-session')
    expect(window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY)).toBeNull()
  })

  it('updates this tab synchronously when it creates its own journal entry', async () => {
    const current = await mountTerminal()
    makeTerminalWritable(current)
    let resolveSubmit!: () => void
    vi.spyOn(tradingAPI, 'submit').mockImplementation(() => new Promise((resolve) => {
      resolveSubmit = () => resolve(undefined)
    }))

    const request = current.submitOrder()
    const persisted = JSON.parse(
      window.localStorage.getItem(PENDING_TRADING_WRITE_STORAGE_KEY) ?? '{}',
    ) as PendingTradingWrite
    expect(persisted.operation).toBe('submit')
    expect(current.pendingWrite.value?.operation_id).toBe(persisted.operation_id)

    resolveSubmit()
    await request
  })
})

describe('Trade private drawer session isolation', () => {
  it('clears order detail data, pagination, and busy state immediately on logout', async () => {
    const current = await mountTerminal()
    seedPrivateDrawer(current)
    vi.spyOn(tradingAPI, 'logout').mockResolvedValue(undefined)

    await current.logout()

    expect(current.principal.value).toBeNull()
    expect(current.selectedOrder.value).toBeNull()
    expect(current.orderEvents.value).toEqual([])
    expect(current.eventPage).toEqual({ cursor: '', nextCursor: '', page: 1 })
    expect(current.pageBusy.events).toBe(false)
  })

  it('clears old-account drawer state when a private read reports an invalid session', async () => {
    const current = await mountTerminal()
    seedPrivateDrawer(current)
    vi.spyOn(tradingAPI, 'balances').mockRejectedValue(new TradingRequestError(
      'expired',
      'invalid_session',
      401,
      false,
    ))
    vi.spyOn(tradingAPI, 'orderPage').mockResolvedValue({ orders: [], next_cursor: '' })
    vi.spyOn(tradingAPI, 'accountTradePage').mockResolvedValue({ trades: [], next_cursor: '' })
    vi.spyOn(tradingAPI, 'ledgerPage').mockResolvedValue({ entries: [], next_cursor: '' })

    await current.refreshAll()

    expect(current.principal.value).toBeNull()
    expect(current.selectedOrder.value).toBeNull()
    expect(current.orderEvents.value).toEqual([])
    expect(current.eventPage).toEqual({ cursor: '', nextCursor: '', page: 1 })
    expect(current.pageBusy.events).toBe(false)
  })
})
