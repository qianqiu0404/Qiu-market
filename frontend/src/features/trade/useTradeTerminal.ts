import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
  type Ref,
} from 'vue'
import {
  getAssetDashboardV2,
  getAssetMarketsV2,
  getKlines,
  type Kline,
  type KlineInterval,
} from '../../api/market'
import type {
  TradeV1AccountTrade,
  TradeV1LedgerEntry,
  TradeV1Order,
  TradeV1OrderEvent,
  TradeV1OrderScope,
} from '../../api/trade-v1-contract'
import {
  eventSocketURL,
  recoveryNotEnabled,
  TradingRequestError,
  tradingAPI,
  tradingEventMode,
  type AuthCapabilities,
  type Balance,
  type EventEnvelope,
  type OrderBook,
  type Principal,
  type Trade,
  type TradingRecoveryStatus,
  type TradingStatus,
} from '../../api/trading'
import {
  advanceTradingEventCursor,
  latestTradingEventCursor,
  normalizeTradingEventCursor,
  type TradingEventCursor,
} from '../../trading/event-cursor'
import {
  beginPanelRead,
  completePanelRead,
  createPanelReadState,
  failPanelRead,
  panelReadAgeSeconds,
  panelReadAvailability,
  type PanelReadState,
} from '../../trading/panel-state'
import {
  LEGACY_PENDING_TRADING_WRITE_STORAGE_KEY,
  PENDING_TRADING_WRITE_STORAGE_KEY,
  pendingTradingWriteMutationAllowed,
  pendingTradingWriteResolvedByOrders,
  readLocalPendingTradingWrite,
  readPersistedPendingTradingWrite,
  updatePendingTradingWriteState,
  type PendingTradingOperation,
  type PendingTradingWrite,
} from '../../trading/pending-write'
import {
  deriveRecoveryAdmission,
  recoveryStatusRegression,
} from '../../trading/recovery-admission'
import {
  deriveTerminalHealth,
  type EventTransportState,
} from '../../trading/terminal-health'
import { useI18n, type MessageKey } from '../../i18n'
import {
  applyBalancePercent,
  previewOrder,
} from './decimal'

const MARKET_ID = 'BTC-USDT'
const EVENT_CURSOR_STORAGE_KEY = `qiu-market.trading-event-cursor.${MARKET_ID}.v1`
const RECOVERY_EVIDENCE_MAX_AGE_MS = 10_000
const PAGE_SIZE = 25
const TRADING_ERROR_MESSAGES: Partial<Record<string, MessageKey>> = {
  validation_failed: 'trade.error.validation_failed',
  invalid_cursor: 'trade.error.invalid_cursor',
  authentication_required: 'trade.error.authentication_required',
  csrf_failed: 'trade.error.csrf_failed',
  origin_rejected: 'trade.error.origin_rejected',
  order_not_found: 'trade.error.order_not_found',
  operation_not_found: 'trade.error.operation_not_found',
  idempotency_conflict: 'trade.error.idempotency_conflict',
  reconcile_pending: 'trade.error.reconcile_pending',
  rate_limited: 'trade.error.rate_limited',
  recovery_in_progress: 'trade.error.recovery_in_progress',
  trading_write_paused: 'trade.error.trading_write_paused',
  liquidity_paused: 'trade.error.liquidity_paused',
  backend_unavailable: 'trade.error.backend_unavailable',
  backend_timeout: 'trade.error.backend_timeout',
}

export const TRADE_KLINE_INTERVALS: KlineInterval[] = ['1m', '15m', '1h', '1d']

export const TRADE_PANEL_NAMES = [
  'reference',
  'kline',
  'orderbook',
  'publicTrades',
  'status',
  'recovery',
  'balances',
  'orders',
  'privateTrades',
  'ledger',
  'orderEvents',
] as const
export type TradePanelName = typeof TRADE_PANEL_NAMES[number]

interface CursorPage {
  cursor: string
  nextCursor: string
  page: number
}

function emptyPage(): CursorPage {
  return { cursor: '', nextCursor: '', page: 1 }
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function randomID(prefix: string): string {
  return `${prefix}-${crypto.randomUUID()}`
}

export function shortTradeID(value: string): string {
  if (!value) return '—'
  return value.length > 18 ? `${value.slice(0, 9)}…${value.slice(-6)}` : value
}

export function useTradeTerminal() {
  const { locale, tr } = useI18n()
  const principal = ref<Principal | null>(null)
  const authCapabilities = ref<AuthCapabilities>({
    github_oauth_enabled: false,
    local_login_enabled: false,
    practice_mode_enabled: false,
    starter_funds_enabled: false,
    virtual_liquidity_enabled: false,
  })
  const book = ref<OrderBook>({ market_id: MARKET_ID, sequence: '0', bids: [], asks: [] })
  const publicTrades = ref<Trade[]>([])
  const balances = ref<Balance[]>([])
  const orders = ref<TradeV1Order[]>([])
  const privateTrades = ref<TradeV1AccountTrade[]>([])
  const ledgerEntries = ref<TradeV1LedgerEntry[]>([])
  const selectedOrder = ref<TradeV1Order | null>(null)
  const orderEvents = ref<TradeV1OrderEvent[]>([])
  const status = ref<TradingStatus>({
    market_id: MARKET_ID,
    state: 'connecting',
    sequence: '0',
    queue_depth: 0,
    recovery_count: '0',
    last_error: '',
  })
  const recoveryStatus = ref<TradingRecoveryStatus>(recoveryNotEnabled())
  const panels = reactive<Record<TradePanelName, PanelReadState>>(
    Object.fromEntries(TRADE_PANEL_NAMES.map((name) => [name, createPanelReadState()])) as
      Record<TradePanelName, PanelReadState>,
  )
  const errorMessage = ref('')
  const notice = ref('')
  const busy = ref(false)
  const nowMs = ref(Date.now())
  const pendingWrite = ref<PendingTradingWrite | null>(null)
  const pendingJournalBlocked = ref(false)
  const pageBusy = reactive({ orders: false, trades: false, ledger: false, events: false })
  const pageRequestEpoch = reactive({ orders: 0, trades: 0, ledger: 0, events: 0 })

  const referencePrice = ref<number | null>(null)
  const referenceFreshness = ref('unavailable')
  const referenceConfidence = ref('unknown')
  const referenceObservedAt = ref(0)
  const referenceProvider = ref('')
  const referenceError = ref('')
  const klineMarketID = ref('')
  const klineProvider = ref('')
  const klineInterval = ref<KlineInterval>('15m')
  const klines = ref<Kline[]>([])
  const klineError = ref('')

  const orderScope = ref<TradeV1OrderScope>('open')
  const orderPage = reactive<CursorPage>(emptyPage())
  const orderPrevious = ref<string[]>([])
  const tradePage = reactive<CursorPage>(emptyPage())
  const tradePrevious = ref<string[]>([])
  const ledgerPage = reactive<CursorPage>(emptyPage())
  const ledgerPrevious = ref<string[]>([])
  const eventPage = reactive<CursorPage>(emptyPage())
  const eventPrevious = ref<string[]>([])

  const wsState = ref<EventTransportState>('polling')
  const eventCursor = ref<TradingEventCursor>()
  const eventReconcilePending = ref(false)
  const cursorError = ref('')
  const duplicateEventCount = ref(0)
  const cursorGapCount = ref(0)
  const lastPrivateAt = ref(0)

  const form = reactive({
    clientOrderID: crypto.randomUUID(),
    side: 'buy' as 'buy' | 'sell',
    type: 'limit' as 'limit' | 'market',
    timeInForce: 'gtc' as 'gtc' | 'ioc' | 'fok',
    postOnly: false,
    price: '',
    quantity: '0.001',
    quoteBudget: '100',
  })

  let socket: WebSocket | undefined
  let reconnectTimer: number | undefined
  let socketConnectTimer: number | undefined
  let publicTimer: number | undefined
  let marketTimer: number | undefined
  let clockTimer: number | undefined
  let refreshTimer: number | undefined
  let publicRefreshPromise: Promise<void> | undefined
  let marketRefreshRunning = false
  let pendingGapCursor: TradingEventCursor | undefined
  let eventReconcilePromise: Promise<void> | undefined
  let sessionGeneration = 0

  const loggedIn = computed(() => principal.value !== null)
  const marketBuy = computed(() => form.type === 'market' && form.side === 'buy')
  const bestAsk = computed(() => book.value.asks[0]?.price ?? '')
  const bestBid = computed(() => book.value.bids[0]?.price ?? '')
  const availableBTC = computed(() =>
    balances.value.find((item) => item.asset === 'BTC')?.available ?? '0')
  const availableUSDT = computed(() =>
    balances.value.find((item) => item.asset === 'USDT')?.available ?? '0')
  const orderPreview = computed(() => previewOrder({
    ...form,
    availableBTC: availableBTC.value,
    availableUSDT: availableUSDT.value,
    marketPrice: form.side === 'buy' ? bestAsk.value : bestBid.value,
  }))
  const terminalHealth = computed(() => deriveTerminalHealth({
    now: nowMs.value,
    loggedIn: loggedIn.value,
    matchingStatus: status.value.state,
    statusRead: panels.status,
    orderbookRead: panels.orderbook,
    hasBids: book.value.bids.length > 0,
    hasAsks: book.value.asks.length > 0,
    eventTransport: wsState.value,
    privateDataAt: lastPrivateAt.value,
    reconcileComplete: pendingWrite.value === null && !eventReconcilePending.value,
  }))
  const pendingReplayHealth = computed(() => deriveTerminalHealth({
    now: nowMs.value,
    loggedIn: loggedIn.value,
    matchingStatus: status.value.state,
    statusRead: panels.status,
    orderbookRead: panels.orderbook,
    hasBids: book.value.bids.length > 0,
    hasAsks: book.value.asks.length > 0,
    eventTransport: wsState.value,
    privateDataAt: lastPrivateAt.value,
    reconcileComplete: !eventReconcilePending.value,
  }))
  const recoveryAdmission = computed(() => deriveRecoveryAdmission(
    recoveryStatus.value,
    panels.recovery.error,
    {
      lastSuccessAt: panels.recovery.lastSuccessAt,
      now: nowMs.value,
      maximumAgeMs: RECOVERY_EVIDENCE_MAX_AGE_MS,
    },
  ))
  const writesEnabled = computed(() =>
    !busy.value && terminalHealth.value.writesAllowed && recoveryAdmission.value.writesAllowed)
  const virtualLiquidityState = computed(() =>
    status.value.virtual_liquidity?.state ?? 'disabled')
  const virtualLiquiditySubmitAllowed = computed(() =>
    authCapabilities.value.practice_mode_enabled !== true || (
      authCapabilities.value.virtual_liquidity_enabled === true &&
      virtualLiquidityState.value === 'active'
    ))
  const submitEnabled = computed(() => writesEnabled.value &&
    virtualLiquiditySubmitAllowed.value &&
    !pendingJournalBlocked.value && orderPreview.value.valid)
  const writeGateReason = computed(() => {
    if (busy.value) return 'request_in_flight'
    if (!loggedIn.value) return 'login_required'
    if (pendingJournalBlocked.value) return 'reconcile_pending'
    if (pendingWrite.value) return 'reconcile_pending'
    if (eventReconcilePending.value) return 'transport_reconcile_pending'
    if (!recoveryAdmission.value.writesAllowed) return recoveryAdmission.value.reason
    if (!terminalHealth.value.writesAllowed) return terminalHealth.value.writeBlockReason
    if (!virtualLiquiditySubmitAllowed.value) return 'liquidity_paused'
    if (!orderPreview.value.valid) return 'validation_failed'
    return 'ready'
  })
  const canReconcilePending = computed(() =>
    pendingWrite.value?.state === 'unknown' && loggedIn.value &&
      pendingWrite.value.account_id === principal.value?.account_id && !busy.value)
  const pendingWriteLabel = computed(() => {
    const pending = pendingWrite.value
    if (!pending) return ''
    return tr('trade.unknown.locked', {
      operation: tr(`trade.operation.${pending.operation}`),
      operationID: shortTradeID(pending.operation_id),
      requestID: pending.request_id,
    })
  })
  const lastSuccessAt = computed(() => Math.max(
    panels.status.lastSuccessAt,
    panels.orderbook.lastSuccessAt,
    panels.publicTrades.lastSuccessAt,
    lastPrivateAt.value,
  ))

  function beginPanel(name: TradePanelName): void {
    panels[name] = beginPanelRead(panels[name], Date.now())
  }

  function completePanel(name: TradePanelName): void {
    const at = Date.now()
    nowMs.value = at
    panels[name] = completePanelRead(panels[name], at)
  }

  function failPanel(name: TradePanelName, error: unknown): void {
    const at = Date.now()
    nowMs.value = at
    panels[name] = failPanelRead(panels[name], errorText(error), at)
  }

  function panelStateLabel(name: TradePanelName): string {
    const availability = panelReadAvailability(panels[name])
    const age = panelReadAgeSeconds(panels[name], nowMs.value)
    if (availability === 'current') return tr('trade.panel.current', { age })
    if (availability === 'last-good') return tr('trade.panel.lastGood', { age })
    if (availability === 'unavailable') return tr('trade.panel.unavailable')
    return tr('trade.panel.loading')
  }

  function panelStateClass(name: TradePanelName): string {
    return `panel-state--${panelReadAvailability(panels[name])}`
  }

  function showError(error: unknown): void {
    const key = error instanceof TradingRequestError
      ? TRADING_ERROR_MESSAGES[error.code]
      : undefined
    errorMessage.value = key
      ? tr(key)
      : typeof error === 'string'
        ? error
        : tr('trade.error.backend_unavailable')
    window.setTimeout(() => { errorMessage.value = '' }, 6000)
  }

  function invalidateTradingSession(message = ''): void {
    sessionGeneration += 1
    invalidatePageRequests()
    principal.value = null
    balances.value = []
    orders.value = []
    privateTrades.value = []
    ledgerEntries.value = []
    resetPrivatePagination()
    resetOrderDetails()
    lastPrivateAt.value = 0
    closeEvents()
    if (message) notice.value = message
  }

  function privateRequestIsCurrent(generation: number, accountID: string): boolean {
    return generation === sessionGeneration && principal.value?.account_id === accountID
  }

  function invalidatePageRequests(): void {
    for (const name of ['orders', 'trades', 'ledger', 'events'] as const) {
      pageRequestEpoch[name] += 1
      pageBusy[name] = false
    }
  }

  function resetPrivatePagination(): void {
    Object.assign(orderPage, emptyPage())
    Object.assign(tradePage, emptyPage())
    Object.assign(ledgerPage, emptyPage())
    orderPrevious.value = []
    tradePrevious.value = []
    ledgerPrevious.value = []
  }

  function resetOrderDetails(): void {
    selectedOrder.value = null
    orderEvents.value = []
    Object.assign(eventPage, emptyPage())
    eventPrevious.value = []
    pageBusy.events = false
  }

  function invalidSessionFailure(error: unknown): boolean {
    return error instanceof TradingRequestError && error.status === 401 &&
      ['invalid_session', 'unauthorized', 'authentication_required'].includes(error.code)
  }

  function persistPendingWrite(next: PendingTradingWrite | null): boolean {
    try {
      if (next) {
        window.localStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, JSON.stringify(next))
      } else {
        window.localStorage.removeItem(PENDING_TRADING_WRITE_STORAGE_KEY)
      }
      window.sessionStorage.removeItem(PENDING_TRADING_WRITE_STORAGE_KEY)
      window.sessionStorage.removeItem(LEGACY_PENDING_TRADING_WRITE_STORAGE_KEY)
      window.localStorage.removeItem(LEGACY_PENDING_TRADING_WRITE_STORAGE_KEY)
      pendingJournalBlocked.value = false
      return true
    } catch {
      return false
    }
  }

  function storePendingWrite(
    next: PendingTradingWrite | null,
    expectedOperationID: string | null,
  ): boolean {
    if (pendingJournalBlocked.value) return false
    let authoritative: PendingTradingWrite | null
    try {
      authoritative = readLocalPendingTradingWrite(window.localStorage)
    } catch {
      return false
    }
    if (!pendingTradingWriteMutationAllowed(authoritative, next, expectedOperationID)) {
      pendingWrite.value = authoritative
      return false
    }
    if (!persistPendingWrite(next)) return false
    pendingWrite.value = next
    return true
  }

  function preparePendingWrite(
    operation: PendingTradingOperation,
    requestID: string,
    payload: Record<string, string | boolean>,
    orderID?: string,
  ): PendingTradingWrite | null {
    if (!principal.value) {
      showError(tr('trade.error.accountRequired'))
      return null
    }
    if (pendingJournalBlocked.value) {
      showError(tr('trade.error.persist'))
      return null
    }
    const at = Date.now()
    const prepared: PendingTradingWrite = {
      operation_id: randomID('operation'),
      operation,
      account_id: principal.value.account_id,
      request_id: requestID,
      state: 'submitted',
      created_at: at,
      updated_at: at,
      order_id: orderID,
      payload,
    }
    if (!storePendingWrite(prepared, null)) {
      showError(tr('trade.error.persist'))
      return null
    }
    return prepared
  }

  function transitionPendingWrite(
    pending: PendingTradingWrite,
    state: PendingTradingWrite['state'],
  ): boolean {
    let authoritative: PendingTradingWrite | null
    try {
      authoritative = readLocalPendingTradingWrite(window.localStorage)
    } catch {
      return false
    }
    if (!authoritative || authoritative.operation_id !== pending.operation_id) {
      pendingWrite.value = authoritative
      return false
    }
    return storePendingWrite(
      updatePendingTradingWriteState(authoritative, state, Date.now()),
      pending.operation_id,
    )
  }

  function restorePendingWrite(): void {
    try {
      const restored = readPersistedPendingTradingWrite(
        window.localStorage,
        window.sessionStorage,
      )
      if (!restored) {
        pendingWrite.value = null
        pendingJournalBlocked.value = false
        return
      }
      const uncertain = updatePendingTradingWriteState(restored, 'unknown', Date.now())
      const shared = readLocalPendingTradingWrite(window.localStorage)
      if (shared) {
        storePendingWrite(uncertain, restored.operation_id)
      } else {
        // Migration from this tab's old session journal: the shared journal is
        // empty, so it is safe to claim it with the exact persisted operation.
        storePendingWrite(uncertain, null)
      }
    } catch {
      pendingWrite.value = null
      pendingJournalBlocked.value = true
    }
  }

  function handlePendingWriteStorage(event: StorageEvent): void {
    if (event.storageArea && event.storageArea !== window.localStorage) return
    if (![PENDING_TRADING_WRITE_STORAGE_KEY, LEGACY_PENDING_TRADING_WRITE_STORAGE_KEY]
      .includes(event.key ?? '')) return
    try {
      // Do not persist from a storage listener. It is a mirror of the shared
      // authority, and writing here would create cross-tab event ping-pong.
      pendingWrite.value = readPersistedPendingTradingWrite(
        window.localStorage,
        window.sessionStorage,
      )
      pendingJournalBlocked.value = false
    } catch {
      // Storage becoming unreadable is fail-closed: preserve the last lock.
      pendingJournalBlocked.value = true
    }
  }

  function restoreEventCursor(): void {
    try {
      const raw = window.localStorage.getItem(EVENT_CURSOR_STORAGE_KEY)
      if (!raw) return
      const restored = normalizeTradingEventCursor(JSON.parse(raw), MARKET_ID)
      if (restored) eventCursor.value = restored
    } catch {
      cursorError.value = tr('trade.error.cursorUnreadable')
    }
  }

  function adoptEventCursor(next: TradingEventCursor): void {
    const latest = latestTradingEventCursor(eventCursor.value, next)
    if (eventCursor.value === latest) return
    eventCursor.value = latest
    try {
      window.localStorage.setItem(EVENT_CURSOR_STORAGE_KEY, JSON.stringify(latest))
    } catch {
      cursorError.value = tr('trade.error.cursorStorage')
    }
  }

  function statusCheckpoint(): TradingEventCursor | null {
    return normalizeTradingEventCursor({
      market_id: MARKET_ID,
      sequence: status.value.outbox_checkpoint_sequence,
      event_index: status.value.outbox_checkpoint_event_index,
    }, MARKET_ID)
  }

  function adoptStatusCheckpoint(): void {
    const checkpoint = statusCheckpoint()
    if (checkpoint && !panels.status.error) adoptEventCursor(checkpoint)
  }

  async function loadPublicOnce(): Promise<void> {
    for (const name of ['orderbook', 'publicTrades', 'status', 'recovery'] as const) beginPanel(name)
    const results = await Promise.allSettled([
      tradingAPI.orderBook(),
      tradingAPI.publicTrades(),
      tradingAPI.status(),
      tradingAPI.recoveryStatus(),
    ])
    if (results[0].status === 'fulfilled') {
      book.value = { ...results[0].value, bids: results[0].value.bids ?? [], asks: results[0].value.asks ?? [] }
      completePanel('orderbook')
    } else failPanel('orderbook', results[0].reason)
    if (results[1].status === 'fulfilled') {
      publicTrades.value = results[1].value.trades ?? []
      completePanel('publicTrades')
    } else failPanel('publicTrades', results[1].reason)
    if (results[2].status === 'fulfilled') {
      status.value = results[2].value
      completePanel('status')
    } else failPanel('status', results[2].reason)
    if (results[3].status === 'fulfilled') {
      const regression = recoveryStatusRegression(recoveryStatus.value, results[3].value)
      if (regression) failPanel('recovery', regression)
      else {
        recoveryStatus.value = results[3].value
        completePanel('recovery')
      }
    } else failPanel('recovery', results[3].reason)
  }

  function loadPublic(): Promise<void> {
    if (publicRefreshPromise) return publicRefreshPromise
    publicRefreshPromise = loadPublicOnce().finally(() => { publicRefreshPromise = undefined })
    return publicRefreshPromise
  }

  async function loadBalances(): Promise<boolean> {
    const generation = sessionGeneration
    const accountID = principal.value?.account_id ?? ''
    if (!accountID) return false
    beginPanel('balances')
    try {
      const next = (await tradingAPI.balances()).balances ?? []
      if (!privateRequestIsCurrent(generation, accountID)) return false
      balances.value = next
      completePanel('balances')
      return true
    } catch (error) {
      if (privateRequestIsCurrent(generation, accountID)) {
        failPanel('balances', error)
        if (invalidSessionFailure(error)) throw error
      }
      return false
    }
  }

  async function loadOrders(cursor = orderPage.cursor): Promise<boolean> {
    if (pageBusy.orders) return false
    const generation = sessionGeneration
    const accountID = principal.value?.account_id ?? ''
    if (!accountID) return false
    const requestEpoch = ++pageRequestEpoch.orders
    pageBusy.orders = true
    beginPanel('orders')
    try {
      const page = await tradingAPI.orderPage(orderScope.value, cursor, PAGE_SIZE)
      if (!privateRequestIsCurrent(generation, accountID)) return false
      orders.value = page.orders ?? []
      orderPage.cursor = cursor
      orderPage.nextCursor = page.next_cursor ?? ''
      completePanel('orders')
      return true
    } catch (error) {
      if (privateRequestIsCurrent(generation, accountID) &&
          requestEpoch === pageRequestEpoch.orders) {
        failPanel('orders', error)
        if (invalidSessionFailure(error)) throw error
      }
      return false
    } finally {
      if (requestEpoch === pageRequestEpoch.orders) pageBusy.orders = false
    }
  }

  async function loadPrivateTrades(cursor = tradePage.cursor): Promise<boolean> {
    if (pageBusy.trades) return false
    const generation = sessionGeneration
    const accountID = principal.value?.account_id ?? ''
    if (!accountID) return false
    const requestEpoch = ++pageRequestEpoch.trades
    pageBusy.trades = true
    beginPanel('privateTrades')
    try {
      const page = await tradingAPI.accountTradePage(cursor, PAGE_SIZE)
      if (!privateRequestIsCurrent(generation, accountID)) return false
      privateTrades.value = page.trades ?? []
      tradePage.cursor = cursor
      tradePage.nextCursor = page.next_cursor ?? ''
      completePanel('privateTrades')
      return true
    } catch (error) {
      if (privateRequestIsCurrent(generation, accountID) &&
          requestEpoch === pageRequestEpoch.trades) {
        failPanel('privateTrades', error)
        if (invalidSessionFailure(error)) throw error
      }
      return false
    } finally {
      if (requestEpoch === pageRequestEpoch.trades) pageBusy.trades = false
    }
  }

  async function loadLedger(cursor = ledgerPage.cursor): Promise<boolean> {
    if (pageBusy.ledger) return false
    const generation = sessionGeneration
    const accountID = principal.value?.account_id ?? ''
    if (!accountID) return false
    const requestEpoch = ++pageRequestEpoch.ledger
    pageBusy.ledger = true
    beginPanel('ledger')
    try {
      const page = await tradingAPI.ledgerPage(cursor, PAGE_SIZE)
      if (!privateRequestIsCurrent(generation, accountID)) return false
      ledgerEntries.value = page.entries ?? []
      ledgerPage.cursor = cursor
      ledgerPage.nextCursor = page.next_cursor ?? ''
      completePanel('ledger')
      return true
    } catch (error) {
      if (privateRequestIsCurrent(generation, accountID) &&
          requestEpoch === pageRequestEpoch.ledger) {
        failPanel('ledger', error)
        if (invalidSessionFailure(error)) throw error
      }
      return false
    } finally {
      if (requestEpoch === pageRequestEpoch.ledger) pageBusy.ledger = false
    }
  }

  async function loadPrivate(): Promise<void> {
    if (!principal.value) return
    try {
      const results = await Promise.all([
        loadBalances(),
        loadOrders(),
        loadPrivateTrades(),
        loadLedger(),
      ])
      if (results.every(Boolean)) lastPrivateAt.value = Date.now()
      const active = pendingWrite.value
      if (active && pendingTradingWriteResolvedByOrders(active, orders.value)) {
        if (storePendingWrite(null, active.operation_id)) {
          notice.value = tr('trade.notice.reconciled', { id: active.request_id })
          form.clientOrderID = crypto.randomUUID()
        }
      }
    } catch (error) {
      if (invalidSessionFailure(error)) invalidateTradingSession(tr('trade.notice.sessionExpired'))
    }
  }

  async function openOrder(order: TradeV1Order): Promise<void> {
    selectedOrder.value = order
    orderEvents.value = []
    Object.assign(eventPage, emptyPage())
    eventPrevious.value = []
    await loadOrderEvents('')
  }

  async function loadOrderEvents(cursor = eventPage.cursor): Promise<boolean> {
    if (!selectedOrder.value || pageBusy.events) return false
    const generation = sessionGeneration
    const accountID = principal.value?.account_id ?? ''
    const orderID = selectedOrder.value.id
    if (!accountID) return false
    const requestEpoch = ++pageRequestEpoch.events
    pageBusy.events = true
    beginPanel('orderEvents')
    try {
      const page = await tradingAPI.orderEventPage(orderID, cursor, PAGE_SIZE)
      if (!privateRequestIsCurrent(generation, accountID) || selectedOrder.value?.id !== orderID) {
        return false
      }
      orderEvents.value = page.events ?? []
      eventPage.cursor = cursor
      eventPage.nextCursor = page.next_cursor ?? ''
      completePanel('orderEvents')
      return true
    } catch (error) {
      if (privateRequestIsCurrent(generation, accountID) &&
          selectedOrder.value?.id === orderID && requestEpoch === pageRequestEpoch.events) {
        failPanel('orderEvents', error)
      }
      return false
    } finally {
      if (requestEpoch === pageRequestEpoch.events) pageBusy.events = false
    }
  }

  function closeOrder(): void {
    pageRequestEpoch.events += 1
    resetOrderDetails()
  }

  async function discoverSession(): Promise<void> {
    const generation = ++sessionGeneration
    invalidatePageRequests()
    try {
      const next = (await tradingAPI.session()).principal
      if (generation !== sessionGeneration) return
      principal.value = next
      resetPrivatePagination()
      await loadPrivate()
      adoptStatusCheckpoint()
      void connectEvents()
    } catch {
      if (generation === sessionGeneration) invalidateTradingSession()
    }
  }

  async function loadAuthCapabilities(): Promise<void> {
    try {
      authCapabilities.value = await tradingAPI.authCapabilities()
    } catch {
      authCapabilities.value = {
        github_oauth_enabled: false,
        local_login_enabled: false,
        practice_mode_enabled: false,
        starter_funds_enabled: false,
        virtual_liquidity_enabled: false,
      }
    }
  }

  async function localLogin(): Promise<void> {
    const generation = ++sessionGeneration
    invalidatePageRequests()
    busy.value = true
    try {
      const next = (await tradingAPI.localLogin()).principal
      if (generation !== sessionGeneration) return
      principal.value = next
      resetPrivatePagination()
      notice.value = tr('trade.notice.localLogin')
      await loadPrivate()
      adoptStatusCheckpoint()
      void connectEvents()
    } catch (error) {
      if (generation === sessionGeneration) showError(error)
    } finally {
      busy.value = false
    }
  }

  async function logout(): Promise<void> {
    busy.value = true
    invalidateTradingSession(tr('trade.notice.logout'))
    try { await tradingAPI.logout() } catch (error) { showError(error) } finally { busy.value = false }
  }

  async function submitOrder(): Promise<void> {
    if (!submitEnabled.value) return
    const requestID = form.clientOrderID
    const payload: Record<string, string | boolean> = {
      client_order_id: requestID,
      side: form.side,
      type: form.type,
      time_in_force: form.type === 'market' ? 'ioc' : form.timeInForce,
      post_only: form.type === 'limit' && form.postOnly,
      price: form.type === 'limit' ? form.price : '',
      quantity: marketBuy.value ? '' : form.quantity,
      quote_budget: marketBuy.value ? form.quoteBudget : '',
    }
    const operation = preparePendingWrite('submit', requestID, payload)
    if (!operation) return
    busy.value = true
    try {
      await tradingAPI.submit(payload)
      if (!storePendingWrite(null, operation.operation_id)) {
        throw new Error(tr('trade.error.journalClear'))
      }
      notice.value = tr('trade.notice.submitHandled', { id: requestID })
      form.clientOrderID = crypto.randomUUID()
      await refreshAll()
    } catch (error) {
      if (error instanceof TradingRequestError && error.uncertain) {
        transitionPendingWrite(operation, 'unknown')
        notice.value = tr('trade.notice.submitUnknown', { id: requestID })
        window.setTimeout(() => void loadPrivate(), 1500)
      } else {
        storePendingWrite(null, operation.operation_id)
        showError(error)
      }
    } finally {
      busy.value = false
    }
  }

  async function cancelOrder(order: TradeV1Order): Promise<void> {
    const requestID = randomID('cancel')
    const operation = preparePendingWrite('cancel', requestID, {}, order.id)
    if (!operation) return
    busy.value = true
    try {
      await tradingAPI.cancel(order.id, requestID)
      if (!storePendingWrite(null, operation.operation_id)) {
        throw new Error(tr('trade.error.journalClear'))
      }
      await refreshAll()
    } catch (error) {
      if (error instanceof TradingRequestError && error.uncertain) {
        transitionPendingWrite(operation, 'unknown')
        notice.value = tr('trade.notice.cancelUnknown', { id: requestID })
      } else {
        storePendingWrite(null, operation.operation_id)
        showError(error)
      }
    } finally {
      busy.value = false
    }
  }

  async function reconcilePendingWrite(): Promise<void> {
    let current: PendingTradingWrite | null
    try {
      current = readLocalPendingTradingWrite(window.localStorage)
      pendingWrite.value = current
    } catch {
      showError(tr('trade.error.persist'))
      return
    }
    if (!current || current.state === 'reconciling') return
    if (!principal.value || current.account_id !== principal.value.account_id) {
      showError(tr('trade.error.accountMismatch'))
      return
    }
    if (!transitionPendingWrite(current, 'reconciling')) {
      showError(tr('trade.error.persist'))
      return
    }
    try {
      if (current.operation === 'submit') {
        const authoritative = await tradingAPI.orderPage('all', '', 100)
        if (!pendingTradingWriteResolvedByOrders(current, authoritative.orders)) {
          if (!pendingReplayHealth.value.writesAllowed || !recoveryAdmission.value.writesAllowed) {
            transitionPendingWrite(current, 'unknown')
            notice.value = tr('trade.notice.queryOnly', { id: current.request_id })
            return
          }
          await tradingAPI.submit(current.payload)
        }
      } else if (current.operation === 'cancel') {
        const orderID = current.order_id ?? ''
        const authoritative = await tradingAPI.order(orderID)
        if (['open', 'partially_filled'].includes(authoritative.status)) {
          if (!pendingReplayHealth.value.writesAllowed || !recoveryAdmission.value.writesAllowed) {
            transitionPendingWrite(current, 'unknown')
            notice.value = tr('trade.notice.queryOnly', { id: current.request_id })
            return
          }
          await tradingAPI.cancel(orderID, current.request_id)
        }
      } else {
        if (!pendingReplayHealth.value.writesAllowed || !recoveryAdmission.value.writesAllowed) {
          transitionPendingWrite(current, 'unknown')
          notice.value = tr('trade.notice.queryOnly', { id: current.request_id })
          return
        }
        await tradingAPI.fund(
          current.request_id,
          String(current.payload.asset ?? ''),
          String(current.payload.amount ?? ''),
          String(current.payload.account_id ?? ''),
        )
      }
      if (!storePendingWrite(null, current.operation_id)) {
        throw new Error(tr('trade.error.journalClear'))
      }
      form.clientOrderID = crypto.randomUUID()
      notice.value = tr('trade.notice.reconciled', { id: current.request_id })
      await refreshAll()
    } catch (error) {
      transitionPendingWrite(current, 'unknown')
      showError(error)
    }
  }

  async function refreshAll(): Promise<void> {
    await Promise.all([loadPublic(), loadPrivate()])
    if (selectedOrder.value) {
      const generation = sessionGeneration
      const accountID = principal.value?.account_id ?? ''
      const orderID = selectedOrder.value.id
      try {
        const current = await tradingAPI.order(orderID)
        if (privateRequestIsCurrent(generation, accountID) && selectedOrder.value?.id === orderID) {
          selectedOrder.value = current
        }
      } catch (error) {
        if (privateRequestIsCurrent(generation, accountID) && selectedOrder.value?.id === orderID) {
          failPanel('orderEvents', error)
        }
      }
      await loadOrderEvents(eventPage.cursor)
    }
  }

  function scheduleRefresh(): void {
    if (refreshTimer) window.clearTimeout(refreshTimer)
    refreshTimer = window.setTimeout(() => void refreshAll(), 80)
  }

  function handleEventMessage(message: MessageEvent): void {
    try {
      if (typeof message.data !== 'string') throw new Error('event frame must be JSON text')
      const decision = advanceTradingEventCursor(
        eventCursor.value,
        JSON.parse(message.data) as EventEnvelope,
        MARKET_ID,
      )
      if (decision.kind === 'duplicate') {
        duplicateEventCount.value += 1
        return
      }
      if (decision.kind === 'invalid' || decision.kind === 'gap') {
        cursorGapCount.value += 1
        cursorError.value = decision.kind === 'invalid'
          ? decision.reason
          : `cursor gap before ${decision.cursor.sequence}:${decision.cursor.event_index}`
        if (decision.kind === 'gap') {
          pendingGapCursor = latestTradingEventCursor(pendingGapCursor, decision.cursor)
        }
        eventReconcilePending.value = true
        socket?.close(4000, 'event cursor reconcile required')
        return
      }
      adoptEventCursor(decision.cursor)
      scheduleRefresh()
    } catch (error) {
      cursorGapCount.value += 1
      cursorError.value = errorText(error)
      eventReconcilePending.value = true
      socket?.close(4000, 'invalid event frame')
    }
  }

  function snapshotsCoverCursor(target?: TradingEventCursor): boolean {
    if (!panels.status.lastSuccessAt || panels.status.error) return false
    if (target) {
      try { return BigInt(status.value.sequence) >= BigInt(target.sequence) } catch { return false }
    }
    return true
  }

  function reconcileEventTransport(): Promise<void> {
    if (eventReconcilePromise) return eventReconcilePromise
    eventReconcilePending.value = true
    eventReconcilePromise = (async () => {
      await refreshAll()
      if (!snapshotsCoverCursor(pendingGapCursor)) throw new Error('snapshot cursor gap remains')
      adoptStatusCheckpoint()
      if (pendingGapCursor) adoptEventCursor(pendingGapCursor)
      pendingGapCursor = undefined
      cursorError.value = ''
      eventReconcilePending.value = false
    })().catch((error) => {
      cursorError.value = errorText(error)
      wsState.value = 'polling'
    }).finally(() => { eventReconcilePromise = undefined })
    return eventReconcilePromise
  }

  function scheduleReconnect(delay: number): void {
    if (reconnectTimer) window.clearTimeout(reconnectTimer)
    if (!principal.value || tradingEventMode() === 'polling') return
    reconnectTimer = window.setTimeout(() => void connectEvents(), delay)
  }

  function activatePolling(reason: string): void {
    wsState.value = 'polling'
    if (!principal.value) return
    eventReconcilePending.value = true
    cursorError.value = reason
    void reconcileEventTransport().finally(() => scheduleReconnect(1500))
  }

  async function connectEvents(): Promise<void> {
    if (tradingEventMode() === 'polling') {
      wsState.value = 'polling'
      eventReconcilePending.value = false
      adoptStatusCheckpoint()
      return
    }
    if (!principal.value || ['connecting', 'live'].includes(wsState.value)) return
    if (eventReconcilePending.value) await reconcileEventTransport()
    if (eventReconcilePending.value) return
    wsState.value = 'connecting'
    try {
      const ticket = await tradingAPI.ticket()
      const opened = new WebSocket(eventSocketURL(ticket.ticket, eventCursor.value))
      socket = opened
      socketConnectTimer = window.setTimeout(() => opened.close(4001, 'connect timeout'), 5000)
      opened.onopen = () => {
        if (socket !== opened) return
        if (socketConnectTimer) window.clearTimeout(socketConnectTimer)
        wsState.value = 'live'
        cursorError.value = ''
      }
      opened.onmessage = handleEventMessage
      opened.onerror = () => opened.close()
      opened.onclose = () => {
        if (socket !== opened) return
        socket = undefined
        activatePolling(cursorError.value || 'websocket disconnected')
      }
    } catch (error) {
      activatePolling(errorText(error))
    }
  }

  function closeEvents(): void {
    if (reconnectTimer) window.clearTimeout(reconnectTimer)
    if (socketConnectTimer) window.clearTimeout(socketConnectTimer)
    reconnectTimer = undefined
    socketConnectTimer = undefined
    socket?.close()
    socket = undefined
    eventReconcilePending.value = false
    wsState.value = 'polling'
  }

  async function loadReference(): Promise<void> {
    beginPanel('reference')
    try {
      const dashboard = await getAssetDashboardV2(1, 10, {
        venue: 'all', search: 'BTC', filter: 'assets', sortBy: 'rank',
        sortDirection: 'asc', universe: 'provider_union',
      })
      const btc = dashboard.items.find((item) => item.asset_symbol.toUpperCase() === 'BTC')
      if (!btc) throw new Error('BTC is absent from the provider union')
      referencePrice.value = btc.composite_price_usd.available
        ? btc.composite_price_usd.value
        : btc.price_usd.value
      referenceFreshness.value = btc.freshness_status || 'unavailable'
      referenceConfidence.value = btc.confidence || 'unknown'
      referenceObservedAt.value = btc.last_success_at || btc.observed_at
      referenceProvider.value = btc.display_price.contributors.length
        ? btc.display_price.contributors.join(' + ')
        : btc.display_price.source || btc.price_source || 'unavailable'
      referenceError.value = referencePrice.value === null
        ? btc.coverage_reason || 'No fresh CEX Spot contributor'
        : ''
      completePanel('reference')
      if (!klineMarketID.value) {
        const markets = await getAssetMarketsV2(btc.asset_id, 'all')
        const priority = ['binance', 'coinbase', 'bybit', 'okx']
        const source = markets.filter((item) =>
          item.has_kline && item.market_type.toLowerCase() === 'spot')
          .sort((a, b) => priority.indexOf(a.provider) - priority.indexOf(b.provider))[0]
        if (source) {
          klineMarketID.value = source.market_id
          klineProvider.value = source.provider
        }
      }
      await loadKline()
    } catch (error) {
      referenceError.value = errorText(error)
      failPanel('reference', error)
      await loadKline()
    }
  }

  async function loadKline(): Promise<void> {
    if (!klineMarketID.value) {
      klineError.value = 'No reviewed BTC venue K-line is currently available'
      failPanel('kline', klineError.value)
      return
    }
    beginPanel('kline')
    try {
      klines.value = await getKlines(klineMarketID.value, klineInterval.value, 160)
      klineError.value = klines.value.length ? '' : 'The selected venue returned no real candles'
      completePanel('kline')
    } catch (error) {
      klineError.value = errorText(error)
      failPanel('kline', error)
    }
  }

  async function refreshMarketData(): Promise<void> {
    if (marketRefreshRunning) return
    marketRefreshRunning = true
    try { await loadReference() } finally { marketRefreshRunning = false }
  }

  function useReferencePrice(): void {
    if (referencePrice.value !== null && referenceFreshness.value === 'fresh') {
      form.price = referencePrice.value.toFixed(2)
    }
  }

  function useBookPrice(side: 'bid' | 'ask'): void {
    const value = side === 'bid' ? bestBid.value : bestAsk.value
    if (value) form.price = value
  }

  function applyPercent(percent: 25 | 50 | 75 | 100): void {
    const result = applyBalancePercent({
      side: form.side,
      type: form.type,
      price: form.price,
      availableBTC: availableBTC.value,
      availableUSDT: availableUSDT.value,
    }, percent)
    if (result.quantity !== undefined) form.quantity = result.quantity
    if (result.quoteBudget !== undefined) form.quoteBudget = result.quoteBudget
  }

  async function changeOrderScope(scope: TradeV1OrderScope): Promise<void> {
    if (pageBusy.orders) return
    orderScope.value = scope
    orders.value = []
    Object.assign(orderPage, emptyPage())
    orderPrevious.value = []
    await loadOrders('')
  }

  async function nextCursor(
    page: CursorPage,
    previous: Ref<string[]>,
    loader: (cursor: string) => Promise<boolean>,
  ): Promise<void> {
    if (!page.nextCursor) return
    const priorCursor = page.cursor
    if (await loader(page.nextCursor)) {
      previous.value.push(priorCursor)
      page.page += 1
    }
  }

  async function previousCursor(
    page: CursorPage,
    previous: Ref<string[]>,
    loader: (cursor: string) => Promise<boolean>,
  ): Promise<void> {
    const cursor = previous.value[previous.value.length - 1]
    if (cursor === undefined) return
    if (await loader(cursor)) {
      previous.value.pop()
      page.page = Math.max(1, page.page - 1)
    }
  }

  watch(klineInterval, () => void loadKline())
  watch(() => [form.type, form.timeInForce] as const, ([type, tif]) => {
    if (type === 'market') {
      form.timeInForce = 'ioc'
      form.postOnly = false
    } else if (tif === 'ioc' && form.postOnly) {
      form.postOnly = false
    }
  })

  onMounted(async () => {
    restoreEventCursor()
    window.addEventListener('storage', handlePendingWriteStorage)
    restorePendingWrite()
    clockTimer = window.setInterval(() => { nowMs.value = Date.now() }, 1000)
    await Promise.all([loadPublic(), refreshMarketData(), loadAuthCapabilities()])
    if (authCapabilities.value.github_oauth_enabled || authCapabilities.value.local_login_enabled) {
      await discoverSession()
    }
    publicTimer = window.setInterval(() => void loadPublic().then(() => {
      if (wsState.value === 'polling') {
        void loadPrivate()
        adoptStatusCheckpoint()
      }
    }), 3000)
    marketTimer = window.setInterval(() => void refreshMarketData(), 15_000)
  })

  onBeforeUnmount(() => {
    window.removeEventListener('storage', handlePendingWriteStorage)
    if (publicTimer) window.clearInterval(publicTimer)
    if (marketTimer) window.clearInterval(marketTimer)
    if (clockTimer) window.clearInterval(clockTimer)
    if (refreshTimer) window.clearTimeout(refreshTimer)
    closeEvents()
  })

  return {
    locale,
    tr,
    principal,
    authCapabilities,
    book,
    publicTrades,
    balances,
    orders,
    privateTrades,
    ledgerEntries,
    selectedOrder,
    orderEvents,
    status,
    panels,
    errorMessage,
    notice,
    busy,
    pendingWrite,
    pendingJournalBlocked,
    pageBusy,
    pendingWriteLabel,
    canReconcilePending,
    referencePrice,
    referenceFreshness,
    referenceConfidence,
    referenceObservedAt,
    referenceProvider,
    referenceError,
    klineProvider,
    klineInterval,
    klines,
    klineError,
    form,
    loggedIn,
    marketBuy,
    bestAsk,
    bestBid,
    availableBTC,
    availableUSDT,
    orderPreview,
    terminalHealth,
    writesEnabled,
    submitEnabled,
    virtualLiquidityState,
    virtualLiquiditySubmitAllowed,
    writeGateReason,
    lastSuccessAt,
    wsState,
    eventCursor,
    eventReconcilePending,
    cursorError,
    duplicateEventCount,
    cursorGapCount,
    orderScope,
    orderPage,
    tradePage,
    ledgerPage,
    eventPage,
    panelStateLabel,
    panelStateClass,
    localLogin,
    logout,
    submitOrder,
    cancelOrder,
    reconcilePendingWrite,
    refreshAll,
    openOrder,
    closeOrder,
    loadOrderEvents,
    changeOrderScope,
    useReferencePrice,
    useBookPrice,
    applyPercent,
    resetClientOrderID: () => { form.clientOrderID = crypto.randomUUID() },
    nextOrders: () => nextCursor(orderPage, orderPrevious, loadOrders),
    previousOrders: () => previousCursor(orderPage, orderPrevious, loadOrders),
    nextTrades: () => nextCursor(tradePage, tradePrevious, loadPrivateTrades),
    previousTrades: () => previousCursor(tradePage, tradePrevious, loadPrivateTrades),
    nextLedger: () => nextCursor(ledgerPage, ledgerPrevious, loadLedger),
    previousLedger: () => previousCursor(ledgerPage, ledgerPrevious, loadLedger),
    nextEvents: () => nextCursor(eventPage, eventPrevious, loadOrderEvents),
    previousEvents: () => previousCursor(eventPage, eventPrevious, loadOrderEvents),
  }
}

export type TradeTerminalController = ReturnType<typeof useTradeTerminal>
