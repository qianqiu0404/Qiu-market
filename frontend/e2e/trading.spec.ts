import {
  expect,
  test,
  type Page,
  type Route,
  type WebSocketRoute,
} from '@playwright/test'

/*
 * PRD-QM-TRADE-001 browser contract.
 *
 * This fixture proves browser/API behavior only. It deliberately does not
 * claim PostgreSQL recovery, a live matching engine, or production funding.
 * Those gates remain backend integration and release evidence.
 */

const principal = {
  account_id: 'github:qianqiu0404',
  github_login: 'qianqiu0404',
  admin: true,
}

const observedAt = '2026-08-05T10:00:00.000Z'

function writableRecoveryStatus(
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  return {
    schema_version: 2,
    provenance: {
      production_origin: 'https://qiu-market.vercel.app',
      deployment_id: 'dpl_PreviewFixture123',
      deployment_url: 'https://qiu-market-preview-fixture.vercel.app',
      release_commit: 'd'.repeat(40),
      source_digest: 'e'.repeat(64),
    },
    market_id: 'BTC-USDT',
    epoch_id: '0123456789abcdef0123456789abcdef',
    phase: 'writable',
    runtime_sequence: '10',
    state_hash: 'a'.repeat(64),
    ledger_balanced: true,
    event_continuous: true,
    projection_caught_up: true,
    outbox_caught_up: true,
    transport_healthy: true,
    writes_enabled: true,
    continuity_uncertain: false,
    continuity_error: '',
    last_error: '',
    version: '6',
    started_at: '2026-08-05T00:00:00Z',
    updated_at: '2026-08-05T00:01:00Z',
    ...overrides,
  }
}

const btcAsset = {
  rank: 1,
  selection_version: 4,
  selection_rank: 1,
  asset_id: 'asset-btc',
  asset_symbol: 'BTC',
  asset_name: 'Bitcoin',
  logo: '',
  price_usd: { value: '65000.00', available: true },
  composite_price_usd: { value: '65000.00', available: true },
  change_24h_pct: { value: '1.2', available: true },
  market_cap_usd: { value: '1300000000000', available: true },
  covered_turnover_24h_usd: { value: '20000000000', available: true },
  circulating_supply: { value: '20000000', available: true },
  spot_market_count: 4,
  perp_market_count: 1,
  dex_route_count: 0,
  contributor_count: 4,
  priced_venue_count: 4,
  confidence: 'high',
  quality: 'high',
  price_kind: 'composite_spot',
  price_source: 'all',
  coverage_status: 'covered',
  coverage_reason: '',
  freshness_status: 'fresh',
  freshness_age_seconds: 2,
  last_attempt_at: 1785036000000,
  last_success_at: 1785036000000,
  last_error_class: '',
  available: true,
  source_time: 1785036000000,
  observed_at: 1785036000000,
  index_updated_at: 1785036000000,
  provider_updated_at: 1785036000000,
  sparkline_available: false,
}

const btcVenue = {
  market_id: 'market-binance-btc-usdt',
  market_code: 'binance:BTC/USDT:spot',
  provider: 'binance',
  symbol: 'BTC/USDT',
  market_type: 'spot',
  quote_asset: 'USDT',
  price: { value: '65000', available: true },
  relative_deviation_pct: { value: '0', available: true },
  change_24h_pct: { value: '1.2', available: true },
  turnover_24h: { value: '12000000000', available: true },
  freshness_status: 'fresh',
  provider_updated_at: 1785036000000,
  confidence: 'high',
  quality: 'high',
  has_kline: true,
  venue_kind: 'cex',
  chain: '',
  protocol: '',
  route_key: '',
  route: [],
  pool_addresses: [],
  quote_notional_usd: { value: null, available: false },
  quote_reference_kind: '',
  tvl_usd: { value: null, available: false },
  price_impact_pct: { value: null, available: false },
  round_trip_spread_pct: { value: null, available: false },
  block_number: 0,
  block_timestamp: 0,
  available: true,
  unavailable_reason: '',
}

function systemStatusFixture() {
  const now = Date.now()
  const evidence = (reason: string, source: string) => ({
    state: 'live',
    last_success_at: now,
    age_seconds: 0,
    reason,
    source,
  })
  const unavailable = { available: false, value: null, reason: 'Browser contract fixture' }
  const components = {
    matching: evidence('Matching engine reports ready.', 'trading status'),
    liquidity: evidence('Two-sided liquidity is visible.', 'order book'),
    transport: evidence('Trading reads succeeded.', 'browser contract'),
    market_data: evidence('Reference data is current.', 'asset index'),
    outbox: evidence('Projection cursor is current.', 'event cursor'),
    database: evidence('Read model is available.', 'browser contract'),
    disk: evidence('Disk evidence is outside browser scope.', 'browser contract'),
    retention: evidence('Retention evidence is outside browser scope.', 'browser contract'),
  }
  return {
    schema_version: 'system-status.v1',
    formula_version: 'system-display.v1',
    source_mode: 'native',
    generated_at: now,
    overall: evidence('All browser-contract probes succeeded.', 'browser contract'),
    components,
    processes: [],
    storage: {
      database_bytes: unavailable,
      kline_table_bytes: unavailable,
      kline_heap_bytes: unavailable,
      kline_index_bytes: unavailable,
      kline_estimated_rows: unavailable,
      disk_free_bytes: unavailable,
      disk_state: 'unknown',
      warning_below_bytes: 0,
      critical_below_bytes: 0,
      retention_last_started_at: unavailable,
      retention_last_success_at: unavailable,
      retention_last_error: '',
      retention_deleted_rows: {},
      kline_intervals: [],
    },
    price_sources: [],
    provider_statuses: [],
  }
}

interface HarnessOptions {
  realKlines?: boolean
  authDisabled?: boolean
  initiallyLoggedIn?: boolean
  starterFunded?: boolean
  publicReadDelayMs?: number
  submitResponseLostBeforeCommitOnce?: boolean
  submitCommittedButResponseLostOnce?: boolean
  cancelResponseLostBeforeCommitOnce?: boolean
  cancelCommittedButResponseLostOnce?: boolean
  fundCommittedButResponseLostOnce?: boolean
  recoveryStatus?: Record<string, unknown>
  practiceMode?: boolean
  liquidityState?: 'disabled' | 'recovering' | 'active' | 'paused'
}

type HarnessPanel =
  | 'kline'
  | 'orderbook'
  | 'publicTrades'
  | 'status'
  | 'balances'
  | 'orders'
  | 'privateTrades'
  | 'ledger'

type BrowserOrder = Record<string, unknown> & {
  id: string
  client_order_id: string
  status: string
  remaining_quantity: string
  filled_quantity: string
  held_amount: string
}

async function installBrowserContract(page: Page, options: HarnessOptions = {}) {
  let loggedIn = options.initiallyLoggedIn === true
  let sessionRequests = 0
  let sequence = options.starterFunded === true ? 12 : 10
  let recoveryCount = 1
  let recoveryStatus = options.recoveryStatus ?? writableRecoveryStatus()
  let loseSubmitBeforeCommit = options.submitResponseLostBeforeCommitOnce === true
  let loseSubmitAfterCommit = options.submitCommittedButResponseLostOnce === true
  let loseCancelBeforeCommit = options.cancelResponseLostBeforeCommitOnce === true
  let loseCancelAfterCommit = options.cancelCommittedButResponseLostOnce === true
  let loseFundAfterCommit = options.fundCommittedButResponseLostOnce === true
  let publicReadDelayMs = options.publicReadDelayMs ?? 0
  let liquidityState = options.liquidityState ?? 'active'
  let activePublicReads = 0
  let maximumConcurrentPublicReads = 0
  let balances: Array<{ asset: string; available: string; held: string }> =
    options.starterFunded === true
      ? [
          { asset: 'USDT', available: '10000', held: '0' },
          { asset: 'BTC', available: '0.1', held: '0' },
        ]
      : []
  let orders: BrowserOrder[] = []
  let accountTrades: Array<Record<string, unknown>> = []
  let publicTrades: Array<Record<string, unknown>> = []
  let ledgerEntries: Array<Record<string, unknown>> = []
  const orderEvents = new Map<string, Array<Record<string, unknown>>>()
  const submitRequestIDs: string[] = []
  const cancelRequestIDs: string[] = []
  const fundRequests: Array<{
    request_id: string
    account_id: string
    asset: string
    amount: string
  }> = options.starterFunded === true
    ? [
        {
          request_id: 'starter-v1-usdt',
          account_id: principal.account_id,
          asset: 'USDT',
          amount: '10000',
        },
        {
          request_id: 'starter-v1-btc',
          account_id: principal.account_id,
          asset: 'BTC',
          amount: '0.1',
        },
      ]
    : []
  const fundResults = new Map<string, { sequence: string; status: string }>(
    options.starterFunded === true
      ? [
          ['starter-v1-usdt', { sequence: '11', status: 'accepted' }],
          ['starter-v1-btc', { sequence: '12', status: 'accepted' }],
        ]
      : [],
  )
  const starterTrace: string[] = []
  const cancelResults = new Map<string, { sequence: string; status: string; order_id: string }>()
  const failedPanels = new Set<HarnessPanel>()
  const sockets: WebSocketRoute[] = []
  const socketURLs: string[] = []

  function balance(asset: string) {
    let current = balances.find((candidate) => candidate.asset === asset)
    if (!current) {
      current = { asset, available: '0', held: '0' }
      balances = [...balances, current]
    }
    return current
  }

  function appendEvent(orderID: string, event: Record<string, unknown>) {
    const events = orderEvents.get(orderID) ?? []
    orderEvents.set(orderID, [...events, {
      event_id: `${orderID}-event-${events.length + 1}`,
      market_id: 'BTC-USDT',
      order_id: orderID,
      sequence: String(sequence),
      event_index: 1,
      timeline_index: events.length + 1,
      source_kind: 'event',
      quantity: '',
      price: '',
      remaining_quantity: '',
      remaining_quote_budget: '',
      trade_id: '',
      balance_effects: [],
      reason: '',
      occurred_at: observedAt,
      ...event,
    }])
  }

  function appendLedger(entry: Record<string, unknown>) {
    ledgerEntries = [{
      entry_id: `ledger-${ledgerEntries.length + 1}`,
      market_id: 'BTC-USDT',
      sequence: String(sequence),
      transaction_id: `tx-${sequence}`,
      entry_index: ledgerEntries.length + 1,
      asset: 'USDT',
      bucket: 'available',
      amount: '0',
      reason: 'other',
      reference: '',
      order_id: '',
      trade_id: '',
      occurred_at: observedAt,
      ...entry,
    }, ...ledgerEntries]
  }

  await page.routeWebSocket('**/api/v1/trading/events/ws**', (socket) => {
    sockets.push(socket)
    socketURLs.push(socket.url())
  })

  await page.route(/\/api\/v[12]\//, async (route: Route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    const json = async (status: number, body: unknown) => route.fulfill({
      status,
      contentType: 'application/json',
      body: JSON.stringify(body),
    })
    const requireSession = async () => {
      if (loggedIn) return true
      await json(401, { code: 'invalid_session', message: 'session is invalid or expired' })
      return false
    }
    const delayPublicRead = async () => {
      activePublicReads += 1
      maximumConcurrentPublicReads = Math.max(maximumConcurrentPublicReads, activePublicReads)
      try {
        if (publicReadDelayMs > 0) {
          await new Promise((resolve) => setTimeout(resolve, publicReadDelayMs))
        }
      } finally {
        activePublicReads -= 1
      }
    }

    if (path === '/api/v2/get_asset_dashboard') {
      const snapshot = {
        snapshot_id: 'snp_74726164696e672d6274630000000000',
        snapshot_as_of: 1_785_938_400_000,
        snapshot_schema: 'qiu.market-snapshot.v1',
      }
      await json(200, {
        code: 2000,
        ...snapshot,
        overview: {
          venue: 'all',
          ...snapshot,
          asset_count: 1,
          priced_asset_count: 1,
          displayed_asset_count: 1,
          unpriced_asset_count: 0,
          fresh_asset_count: 1,
          stale_asset_count: 0,
          unavailable_asset_count: 0,
          single_venue_priced_asset_count: 0,
          multi_venue_priced_asset_count: 1,
          global_market_cap_usd: { value: '1300000000000', available: true },
          covered_spot_volume_24h_usd: { value: '20000000000', available: true },
          btc_dominance_pct: { value: '50', available: true },
          coverage_ratio_pct: { value: '100', available: true },
        },
        result: [btcAsset],
        total: 1,
      })
      return
    }
    if (path === '/api/v2/get_asset_markets' || path === '/api/v2/get_asset_venues') {
      await json(200, { code: 2000, result: [btcVenue] })
      return
    }
    if (path === '/api/v1/get_klines') {
      if (failedPanels.has('kline')) {
        await json(503, { code: 'backend_unavailable', message: 'kline unavailable' })
        return
      }
      const candles = options.realKlines === false ? [] : [
        { timestamp: 1785035940000, open: '64950', high: '65020', low: '64900', close: '65000', volume: '8.2' },
        { timestamp: 1785036000000, open: '65000', high: '65080', low: '64980', close: '65040', volume: '7.1' },
      ]
      await json(200, { code: 2000, result: candles })
      return
    }
    if (path === '/api/v1/get_system_status') {
      await json(200, { code: 2000, result: systemStatusFixture() })
      return
    }
    if (path === '/api/v1/get_system_overview') {
      await json(200, {
        code: 2000,
        result: {
          crawler_status: 'Running',
          redis_status: 'Running',
          database_status: 'Running',
          worker_status: 'Running',
          api_status: 'Running',
        },
      })
      return
    }
    if (path === '/api/v1/trading/auth/capabilities') {
      await json(200, {
        github_oauth_enabled: false,
        local_login_enabled: options.authDisabled !== true,
        recovery_gate_enabled: true,
        practice_mode_enabled: options.practiceMode === true,
        starter_funds_enabled: options.practiceMode === true,
        virtual_liquidity_enabled: options.practiceMode === true,
      })
      return
    }
    if (path === '/api/v1/trading/auth/local') {
      loggedIn = true
      await json(200, { principal, local: true })
      return
    }
    if (path === '/api/v1/trading/session') {
      sessionRequests += 1
      await json(loggedIn ? 200 : 401, loggedIn
        ? { principal, expires_at: '2026-08-06T00:00:00Z' }
        : { code: 'invalid_session', message: 'session is invalid or expired' })
      return
    }
    if (path === '/api/v1/trading/auth/logout') {
      loggedIn = false
      await route.fulfill({ status: 204 })
      return
    }
    if (path === '/api/v1/trading/recovery/status') {
      await json(200, recoveryStatus)
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/status') {
      if (failedPanels.has('status')) {
        await json(503, { code: 'backend_unavailable', message: 'status unavailable' })
        return
      }
      await delayPublicRead()
      await json(200, {
        market_id: 'BTC-USDT',
        state: 'ready',
        sequence: String(sequence),
        queue_depth: 0,
        recovery_count: String(recoveryCount),
        last_error: '',
        outbox_state: 'ready',
        outbox_checkpoint_sequence: String(sequence),
        outbox_checkpoint_event_index: 1,
        virtual_liquidity: options.practiceMode === true ? {
          provider: 'Qiu Virtual Liquidity',
          state: liquidityState,
          reason: liquidityState === 'paused' ? 'reference_stale' : '',
          bid_levels: liquidityState === 'active' ? 3 : 0,
          ask_levels: liquidityState === 'active' ? 3 : 0,
          reference_observed_at: observedAt,
          last_refresh_at: observedAt,
        } : undefined,
      })
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/orderbook') {
      if (failedPanels.has('orderbook')) {
        await json(503, { code: 'backend_unavailable', message: 'orderbook unavailable' })
        return
      }
      await delayPublicRead()
      await json(200, {
        market_id: 'BTC-USDT',
        sequence: String(sequence),
        bids: [{ price: '64990', quantity: '0.5', order_count: 1 }],
        asks: [{ price: '65010', quantity: '0.5', order_count: 1 }],
      })
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/trades') {
      if (failedPanels.has('publicTrades')) {
        await json(503, { code: 'backend_unavailable', message: 'public trades unavailable' })
        return
      }
      await delayPublicRead()
      await json(200, { trades: publicTrades })
      return
    }
    if (path === '/api/v1/trading/ws-ticket') {
      await json(201, {
        ticket: 'one-time-browser-contract-ticket',
        expires_at: '2026-08-05T10:00:30Z',
      })
      return
    }
    if (path === '/api/v1/trading/balances') {
      if (!await requireSession()) return
      if (failedPanels.has('balances')) {
        await json(503, { code: 'backend_unavailable', message: 'balances unavailable' })
        return
      }
      await json(200, { balances })
      return
    }
    if (path === '/api/v1/trading/orders' && request.method() === 'GET') {
      if (!await requireSession()) return
      if (failedPanels.has('orders')) {
        await json(503, { code: 'backend_unavailable', message: 'orders unavailable' })
        return
      }
      const scope = url.searchParams.get('scope') ?? 'open'
      const filtered = scope === 'open'
        ? orders.filter((order) => ['open', 'partially_filled'].includes(order.status))
        : scope === 'history'
          ? orders.filter((order) => !['open', 'partially_filled'].includes(order.status))
          : orders
      await json(200, { orders: filtered, next_cursor: '' })
      return
    }
    if (path === '/api/v1/trading/account/trades') {
      if (!await requireSession()) return
      if (failedPanels.has('privateTrades')) {
        await json(503, { code: 'backend_unavailable', message: 'private trades unavailable' })
        return
      }
      await json(200, { trades: accountTrades, next_cursor: '' })
      return
    }
    if (path === '/api/v1/trading/ledger/entries') {
      if (!await requireSession()) return
      if (failedPanels.has('ledger')) {
        await json(503, { code: 'backend_unavailable', message: 'ledger unavailable' })
        return
      }
      await json(200, { entries: ledgerEntries, next_cursor: '' })
      return
    }
    if (/^\/api\/v1\/trading\/orders\/[^/]+\/events$/.test(path)) {
      if (!await requireSession()) return
      const orderID = decodeURIComponent(path.split('/')[5])
      await json(200, { events: orderEvents.get(orderID) ?? [], next_cursor: '' })
      return
    }
    if (
      /^\/api\/v1\/trading\/orders\/[^/]+$/.test(path) &&
      request.method() === 'GET'
    ) {
      if (!await requireSession()) return
      const orderID = decodeURIComponent(path.split('/')[5])
      const order = orders.find((candidate) => candidate.id === orderID)
      await json(order ? 200 : 404, order ?? {
        code: 'order_not_found',
        message: 'order not found',
      })
      return
    }
    if (path === '/api/v1/trading/admin/fund') {
      if (!await requireSession()) return
      const body = request.postDataJSON() as {
        request_id: string
        account_id: string
        asset: string
        amount: string
      }
      if (body.request_id.startsWith('starter-v1-')) starterTrace.push(`fund:${body.request_id}`)
      fundRequests.push(body)
      let result = fundResults.get(body.request_id)
      if (!result) {
        sequence += 1
        const current = balance(body.asset)
        current.available = String(Number(current.available) + Number(body.amount))
        appendLedger({
          asset: body.asset,
          amount: body.amount,
          reason: 'virtual_fund',
          reference: body.request_id,
        })
        result = { sequence: String(sequence), status: 'accepted' }
        fundResults.set(body.request_id, result)
        if (loseFundAfterCommit) {
          loseFundAfterCommit = false
          await json(504, { code: 'backend_timeout', message: 'committed response was lost' })
          return
        }
      }
      await json(200, result)
      return
    }
    if (/^\/api\/v1\/trading\/account\/funding\/[^/]+$/.test(path)) {
      if (!await requireSession()) return
      const requestID = decodeURIComponent(path.split('/').at(-1) ?? '')
      if (requestID.startsWith('starter-v1-')) starterTrace.push(`query:${requestID}`)
      const result = fundResults.get(requestID)
      const funding = fundRequests.find((item) => item.request_id === requestID)
      if (!result || !funding) {
        await json(404, {
          code: 'funding_request_not_found', message: 'funding request was not found',
        })
        return
      }
      await json(200, {
        market_id: 'BTC-USDT',
        request_id: requestID,
        funding_event_id: `${result.sequence}:1`,
        sequence: result.sequence,
        asset: funding.asset,
        amount: funding.amount,
        projection_result: 'applied',
        ledger_balanced: true,
        occurred_at: observedAt,
      })
      return
    }
    if (path === '/api/v1/trading/orders' && request.method() === 'POST') {
      if (!await requireSession()) return
      const body = request.postDataJSON() as Record<string, unknown>
      expect(body.account_id).toBeUndefined()
      const requestID = String(body.client_order_id ?? '')
      submitRequestIDs.push(requestID)
      if (loseSubmitBeforeCommit) {
        loseSubmitBeforeCommit = false
        await json(504, { code: 'backend_timeout', message: 'request outcome is not yet visible' })
        return
      }
      const existing = orders.find((order) => order.client_order_id === requestID)
      if (existing) {
        await json(200, {
          sequence: existing.accepted_sequence,
          status: 'accepted',
          order_id: existing.id,
        })
        return
      }
      sequence += 1
      const orderID = `order-${orders.length + 1}`
      const order: BrowserOrder = {
        id: orderID,
        client_order_id: requestID,
        market_id: 'BTC-USDT',
        side: String(body.side),
        type: String(body.type),
        time_in_force: String(body.time_in_force),
        post_only: body.post_only === true,
        price: String(body.price ?? ''),
        original_quantity: String(body.quantity ?? ''),
        remaining_quantity: String(body.quantity ?? ''),
        filled_quantity: '0',
        average_fill_price: '',
        original_quote_budget: String(body.quote_budget ?? ''),
        remaining_quote_budget: String(body.quote_budget ?? ''),
        spent_quote: '0',
        held_asset: body.side === 'buy' ? 'USDT' : 'BTC',
        held_amount: body.side === 'buy' ? '64.9' : String(body.quantity ?? ''),
        status: 'open',
        accepted_sequence: String(sequence),
        last_sequence: String(sequence),
        reject_reason: '',
        created_at: observedAt,
        updated_at: observedAt,
      }
      orders = [order, ...orders]
      const quote = balance('USDT')
      if (body.side === 'buy') {
        quote.available = String(Number(quote.available) - 64.9)
        quote.held = String(Number(quote.held) + 64.9)
        appendLedger({
          asset: 'USDT',
          bucket: 'held',
          amount: '64.9',
          reason: 'order_hold',
          reference: orderID,
          order_id: orderID,
        })
      }
      appendEvent(orderID, { type: 'order_accepted', status: 'received' })
      appendEvent(orderID, {
        type: 'order_rested',
        status: 'open',
        quantity: order.original_quantity,
        price: order.price,
        remaining_quantity: order.remaining_quantity,
      })
      if (loseSubmitAfterCommit) {
        loseSubmitAfterCommit = false
        await json(504, { code: 'backend_timeout', message: 'committed response was lost' })
        return
      }
      await json(200, { sequence: String(sequence), status: 'accepted', order_id: orderID })
      return
    }
    if (/^\/api\/v1\/trading\/orders\/[^/]+\/cancel$/.test(path)) {
      if (!await requireSession()) return
      const orderID = decodeURIComponent(path.split('/')[5])
      const body = request.postDataJSON() as { request_id: string }
      cancelRequestIDs.push(body.request_id)
      if (loseCancelBeforeCommit) {
        loseCancelBeforeCommit = false
        await json(504, { code: 'backend_timeout', message: 'request outcome is not yet visible' })
        return
      }
      let result = cancelResults.get(body.request_id)
      if (!result) {
        sequence += 1
        const order = orders.find((candidate) => candidate.id === orderID)
        if (order) {
          const released = order.held_amount
          order.status = 'canceled'
          order.held_amount = '0'
          order.last_sequence = String(sequence)
          order.updated_at = observedAt
          const quote = balance('USDT')
          quote.held = String(Number(quote.held) - Number(released))
          quote.available = String(Number(quote.available) + Number(released))
          appendLedger({
            asset: 'USDT',
            amount: released,
            reason: 'order_release',
            reference: orderID,
            order_id: orderID,
          })
          appendEvent(orderID, {
            type: 'order_canceled',
            status: 'canceled',
            remaining_quantity: order.remaining_quantity,
          })
        }
        result = { sequence: String(sequence), status: 'accepted', order_id: orderID }
        cancelResults.set(body.request_id, result)
      }
      if (loseCancelAfterCommit) {
        loseCancelAfterCommit = false
        await json(504, { code: 'backend_timeout', message: 'committed response was lost' })
        return
      }
      await json(200, result)
      return
    }

    await json(404, { code: 'not_found', message: `Unhandled browser-contract route: ${path}` })
  })

  return {
    get sequence() { return sequence },
    get orders() { return orders },
    get submitRequestIDs() { return [...submitRequestIDs] },
    get cancelRequestIDs() { return [...cancelRequestIDs] },
    get fundRequests() { return [...fundRequests] },
    get starterTrace() { return [...starterTrace] },
    get sessionRequests() { return sessionRequests },
    get maximumConcurrentPublicReads() { return maximumConcurrentPublicReads },
    get socketCount() { return sockets.length },
    get socketURLs() { return [...socketURLs] },
    setLiquidityState(next: 'disabled' | 'recovering' | 'active' | 'paused') {
      liquidityState = next
    },
    partialFill(orderID: string) {
      const order = orders.find((candidate) => candidate.id === orderID)
      if (!order) throw new Error(`order ${orderID} is absent from the browser fixture`)
      sequence += 1
      order.status = 'partially_filled'
      order.filled_quantity = '0.0004'
      order.remaining_quantity = '0.0006'
      order.average_fill_price = '64900'
      order.spent_quote = '25.96'
      order.held_amount = '38.94'
      order.last_sequence = String(sequence)
      order.updated_at = observedAt
      const quote = balance('USDT')
      quote.held = '38.94'
      const btc = balance('BTC')
      btc.available = '0.0004'
      const trade = {
        id: 'trade-maker-partial-1',
        market_id: 'BTC-USDT',
        order_id: orderID,
        side: 'buy',
        liquidity_role: 'maker',
        price: '64900',
        quantity: '0.0004',
        quote_amount: '25.96',
        fee_asset: 'USDT',
        fee_amount: '0.02596',
        fee_rate_bps: '10',
        sequence: String(sequence),
        event_index: 1,
        occurred_at: observedAt,
      }
      accountTrades = [trade, ...accountTrades]
      publicTrades = [{
        id: trade.id,
        market_id: 'BTC-USDT',
        price: trade.price,
        quantity: trade.quantity,
        quote_amount: trade.quote_amount,
        maker_order_id: orderID,
        taker_order_id: 'system:e2e-taker-order',
        maker_account_id: principal.account_id,
        taker_account_id: 'system:e2e-taker',
        buyer_account_id: principal.account_id,
        seller_account_id: 'system:e2e-taker',
      }]
      appendLedger({
        asset: 'USDT',
        bucket: 'held',
        amount: '-25.96',
        reason: 'trade_settlement',
        reference: trade.id,
        order_id: orderID,
        trade_id: trade.id,
      })
      appendLedger({
        asset: 'BTC',
        amount: '0.0004',
        reason: 'trade_settlement',
        reference: trade.id,
        order_id: orderID,
        trade_id: trade.id,
      })
      appendEvent(orderID, {
        type: 'trade_executed',
        status: 'partially_filled',
        quantity: trade.quantity,
        price: trade.price,
        remaining_quantity: order.remaining_quantity,
        trade_id: trade.id,
        fee: {
          asset: 'USDT',
          amount: trade.fee_amount,
          rate_bps: trade.fee_rate_bps,
          role: 'maker',
        },
        balance_effects: [{
          asset: 'BTC',
          bucket: 'available',
          amount: '0.0004',
          reason: 'trade_settlement',
          transaction_id: `tx-${sequence}`,
        }],
      })
    },
    sendEvent(event: { sequence: string; event_index: number }) {
      const socket = sockets.at(-1)
      if (!socket) throw new Error('no routed browser-contract WebSocket')
      const numeric = Number(event.sequence)
      if (Number.isSafeInteger(numeric)) sequence = Math.max(sequence, numeric)
      socket.send(JSON.stringify({ market_id: 'BTC-USDT', event: {}, ...event }))
    },
    async closeLatestSocket() {
      const socket = sockets.at(-1)
      if (!socket) throw new Error('no routed browser-contract WebSocket')
      await socket.close({ code: 1012, reason: 'browser-contract disconnect' })
    },
    restartAuthoritativeFixture() {
      recoveryCount += 1
      recoveryStatus = writableRecoveryStatus({
        version: '7',
        runtime_sequence: String(sequence),
        updated_at: '2026-08-05T00:02:00Z',
      })
    },
    setPublicReadDelay(delayMs: number) {
      publicReadDelayMs = delayMs
    },
    setRecoveryStatus(next: Record<string, unknown>) {
      recoveryStatus = next
    },
    expireSession() {
      loggedIn = false
    },
    setPanelFailure(panel: HarnessPanel, failed = true) {
      if (failed) failedPanels.add(panel)
      else failedPanels.delete(panel)
    },
  }
}

async function signIn(page: Page) {
  await page.getByRole('button', { name: 'Local sign in' }).click()
  await expect(page.getByText('Identity bound')).toBeVisible()
}

async function fundFromSystem(page: Page, amount: string) {
  await page.goto('/system')
  await expect(page.getByTestId('system-trading-admin')).toBeVisible()
  await page.getByLabel('Amount').fill(amount)
  await page.getByTestId('funding-submit').click()
  await expect(page.getByRole('status')).toContainText('Virtual funding completed')
}

async function placeLimitMaker(page: Page) {
  await page.getByLabel('Price · USDT').fill('64900')
  await page.getByLabel('Quantity · BTC').fill('0.001')
  await page.getByLabel('Post Only').check()
  const requestID = await page.getByLabel('Client Order ID').inputValue()
  await page.getByRole('button', { name: 'Buy BTC' }).click()
  await expect(page.getByText('Partially filled')).toHaveCount(0)
  await expect(page.getByText('Open', { exact: true })).toBeVisible()
  return requestID
}

test('does not invent K-lines when the trusted venue returns no candles', async ({ page }) => {
  await installBrowserContract(page, { realKlines: false })
  await page.goto('/trade/BTC-USDT')

  await expect(page.getByRole('heading', { name: 'BTC / USDT' })).toBeVisible()
  await expect(page.getByText('NO MOCK · NO STATIC FALLBACK')).toBeVisible()
  await expect(page.getByText('No real candles available')).toBeVisible()
})

test('does not probe a private session when every login method is disabled', async ({ page }) => {
  const harness = await installBrowserContract(page, { authDisabled: true })
  await page.goto('/trade/BTC-USDT')

  await expect(page.getByText('Sign in unavailable')).toBeVisible()
  await expect(page.getByTestId('terminal-availability')).toHaveText('LIVE')
  expect(harness.sessionRequests).toBe(0)
})

test('local practice starter funding is query-first, fixed-ID and idempotent across reload', async ({ page }) => {
  const harness = await installBrowserContract(page, { practiceMode: true })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)

  await expect(page.getByText('VIRTUAL ONLY')).toBeVisible()
  await expect(page.getByText('Qiu Virtual Order Book', { exact: true })).toBeVisible()
  await expect(page.getByText('Qiu Virtual Matcher', { exact: true })).toBeVisible()
  await expect(page.getByTestId('reference-not-executable')).toContainText(
    'Reference price is not executable',
  )

  await page.goto('/system')
  const practice = page.getByTestId('system-trading-practice')
  await expect(practice).toContainText('Local practice enabled')
  await expect(practice).toContainText('Qiu Virtual Liquidity')
  await expect(practice).toContainText('active')

  const starter = page.getByTestId('starter-funds')
  await expect(starter).toContainText('10,000 Virtual USDT')
  await expect(starter).toContainText('0.1 Virtual BTC')
  await page.getByTestId('starter-funds-submit').click()
  await expect(page.getByRole('status')).toContainText(
    'Both starter funding events are applied and balanced',
  )

  expect(harness.fundRequests).toEqual([
    {
      request_id: 'starter-v1-usdt',
      account_id: principal.account_id,
      asset: 'USDT',
      amount: '10000',
    },
    {
      request_id: 'starter-v1-btc',
      account_id: principal.account_id,
      asset: 'BTC',
      amount: '0.1',
    },
  ])
  for (const requestID of ['starter-v1-usdt', 'starter-v1-btc']) {
    const fundIndex = harness.starterTrace.indexOf(`fund:${requestID}`)
    expect(fundIndex).toBeGreaterThan(0)
    expect(harness.starterTrace.slice(0, fundIndex)).toContain(`query:${requestID}`)
    expect(harness.starterTrace.slice(fundIndex + 1)).toContain(`query:${requestID}`)
  }

  await page.reload()
  await expect(page.getByTestId('starter-funds-submit')).toBeDisabled()
  await expect(page.getByTestId('starter-funds-submit')).toHaveText('Starter funds applied')
  expect(harness.fundRequests).toHaveLength(2)
})

test('paused virtual liquidity blocks Submit while an existing order can still be canceled', async ({ page }) => {
  const harness = await installBrowserContract(page, { practiceMode: true })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')
  await placeLimitMaker(page)

  harness.setLiquidityState('paused')
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'Qiu Virtual Liquidity is paused',
  )
  await expect(page.locator('.submit-order')).toBeDisabled()

  const cancel = page.getByRole('button', { name: 'Cancel', exact: true })
  await expect(cancel).toBeEnabled()
  await cancel.click()
  expect(harness.orders[0]?.status).toBe('canceled')
  await page.getByRole('button', { name: 'Order history', exact: true }).click()
  await expect(page.getByText('Canceled')).toBeVisible()
})

test('localizes recovery admission without exposing internal write-gate codes', async ({ page }) => {
  await installBrowserContract(page, {
    recoveryStatus: writableRecoveryStatus({
      phase: 'transport_warmup',
      transport_healthy: false,
      writes_enabled: false,
    }),
  })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)

  await expect(page.getByTestId('write-gate-reason')).toContainText('recovery proof blocks writes')
  await expect(page.getByTestId('write-gate-reason')).not.toContainText('write_gate=')
  await page.getByRole('button', { name: '中文' }).click()
  await expect(page.getByTestId('write-gate-reason')).toContainText('恢复证明尚未允许写入')
  await expect(page.getByRole('button', { name: '请修正订单输入' })).toBeDisabled()
  await expect(page.getByTestId('write-gate-reason')).not.toContainText('recovery_transport_warmup')
})

test('blocked recovery reconciles an unknown submit by query only and never replays POST', async ({ page }) => {
  const harness = await installBrowserContract(page, {
    submitResponseLostBeforeCommitOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')
  await page.getByLabel('Price · USDT').fill('64900')
  await page.getByLabel('Quantity · BTC').fill('0.001')
  await page.getByRole('button', { name: 'Buy BTC' }).click()
  await expect(page.getByRole('button', { name: 'Reconcile with original ID' })).toBeVisible()
  expect(harness.submitRequestIDs).toHaveLength(1)

  harness.setRecoveryStatus(writableRecoveryStatus({
    version: '7',
    continuity_uncertain: true,
    continuity_error: 'store save result is uncertain',
    last_error: 'continuity probe failed',
  }))
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByTestId('terminal-availability')).toHaveText('DEGRADED')
  await expect(page.getByTestId('write-gate-reason')).toContainText('reconciliation pending')

  await page.getByRole('button', { name: 'Reconcile with original ID' }).click()
  await expect(page.getByText(/queried only/i)).toBeVisible()
  expect(harness.submitRequestIDs).toHaveLength(1)
  expect(JSON.parse(await page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2') ?? '{}',
  )).state).toBe('unknown')
})

test('same-epoch recovery version rollback fails closed', async ({ page }) => {
  const harness = await installBrowserContract(page)
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await expect(page.getByTestId('terminal-availability')).toHaveText('LIVE')

  harness.setRecoveryStatus(writableRecoveryStatus({ version: '5' }))
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByTestId('write-gate-reason')).toContainText('recovery proof')
  await expect(page.locator('.submit-order')).toBeDisabled()
})

test('stale matching evidence closes the write gate after the last-good deadline', async ({ page }) => {
  await page.addInitScript(() => {
    const realNow = Date.now.bind(Date)
    const clock = window as typeof window & { __tradeClockOffset?: number }
    clock.__tradeClockOffset = 0
    Date.now = () => realNow() + (clock.__tradeClockOffset ?? 0)
  })
  const harness = await installBrowserContract(page)
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await expect(page.getByTestId('terminal-availability')).toHaveText('LIVE')

  harness.setPanelFailure('status')
  await page.evaluate(() => {
    const clock = window as typeof window & { __tradeClockOffset?: number }
    clock.__tradeClockOffset = 11_500
  })
  await expect(page.getByTestId('terminal-availability')).toHaveText('DEGRADED')
  await expect(page.locator('.submit-order')).toBeDisabled()
  await expect(page.getByTestId('matching-state')).toContainText('Stale')
})

test('panel failures retain explicitly marked last-good data instead of a full-page outage', async ({ page }) => {
  const harness = await installBrowserContract(page)
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')
  await placeLimitMaker(page)
  await expect(page.getByTestId('panel-kline-state')).toContainText('CURRENT')

  for (const panel of [
    'kline',
    'orderbook',
    'publicTrades',
    'balances',
    'orders',
    'privateTrades',
    'ledger',
  ] as const) {
    harness.setPanelFailure(panel)
  }
  await page.getByRole('button', { name: '1m' }).click()
  await page.getByRole('button', { name: 'Refresh' }).click()

  for (const testID of [
    'panel-kline-state',
    'panel-orderbook-state',
    'panel-public-trades-state',
    'panel-balances-state',
    'panel-orders-state',
  ]) {
    await expect(page.getByTestId(testID)).toContainText('LAST GOOD')
  }
  await expect(page.getByText('935.1', { exact: true })).toBeVisible()
  await expect(page.getByText('Open', { exact: true })).toBeVisible()
})

test('slow public polling reuses one in-flight refresh batch', async ({ page }) => {
  const harness = await installBrowserContract(page, {
    authDisabled: true,
    publicReadDelayMs: 3_500,
  })
  await page.goto('/trade/BTC-USDT')
  await expect(page.getByTestId('matching-state')).toContainText('Ready')
  await page.waitForTimeout(7_000)

  expect(harness.maximumConcurrentPublicReads).toBe(3)
})

test('a System journal written in another tab immediately locks Trade without being overwritten', async ({ page, context }) => {
  await installBrowserContract(page)
  await page.goto('/trade/BTC-USDT')
  await signIn(page)

  const systemPage = await context.newPage()
  await installBrowserContract(systemPage)
  await systemPage.goto('/system')
  const journal = {
    operation_id: 'operation-cross-tab-fund',
    operation: 'fund',
    account_id: principal.account_id,
    request_id: 'fund-cross-tab-stable-id',
    state: 'unknown',
    created_at: Date.now(),
    updated_at: Date.now(),
    payload: {
      account_id: 'github:virtual-beneficiary',
      asset: 'USDT',
      amount: '250',
    },
  }
  await systemPage.evaluate(({ key, value }) => {
    window.localStorage.setItem(key, JSON.stringify(value))
  }, { key: 'qiu-market.pending-trading-write.v2', value: journal })

  await expect(page.getByText(/Virtual funding.*fund-cross-tab-stable-id/)).toBeVisible()
  await expect(page.locator('.submit-order')).toBeDisabled()
  expect(JSON.parse(await page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2') ?? '{}',
  ))).toEqual(journal)
  await systemPage.close()
})

test('Trade Product V1 browser contract covers funding, partial fill, fees, cancel, timeline, ledger and restart readback', async ({ page }) => {
  const harness = await installBrowserContract(page)
  await page.goto('/trade/BTC-USDT')
  await signIn(page)

  await fundFromSystem(page, '1000')
  expect(harness.fundRequests).toHaveLength(1)
  await page.goto('/trade/BTC-USDT')
  await expect(page.getByText('1000', { exact: true })).toBeVisible()

  const clientOrderID = await placeLimitMaker(page)
  const orderID = harness.orders[0]?.id
  expect(orderID).toBeTruthy()
  expect(harness.submitRequestIDs).toEqual([clientOrderID])

  harness.partialFill(orderID)
  harness.sendEvent({ sequence: String(harness.sequence), event_index: 1 })
  await expect(page.getByText('Partially filled')).toBeVisible()

  await page.locator('.record-row.order-grid.clickable').first().click()
  const drawer = page.getByRole('dialog', { name: 'Order details' })
  await expect(drawer).toContainText('Order accepted')
  await expect(drawer).toContainText('Order rested')
  await expect(drawer).toContainText('Trade executed')
  await expect(drawer).toContainText('0.02596 USDT')
  await expect(drawer).toContainText('10 bps')
  await expect(drawer).toContainText('event truth')
  await expect(drawer).not.toContainText('trade_executed')
  await page.keyboard.press('Escape')
  await expect(drawer).toHaveCount(0)

  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await page.getByRole('button', { name: 'Order history', exact: true }).click()
  await expect(page.getByText('Canceled')).toBeVisible()
  await page.locator('.record-row.order-grid.clickable').first().click()
  await expect(page.getByRole('dialog', { name: 'Order details' })).toContainText('Order canceled')
  await page.keyboard.press('Escape')

  await page.getByRole('button', { name: 'My trades' }).click()
  await expect(page.getByText('0.02596 USDT · 10 bps')).toBeVisible()
  await page.getByRole('button', { name: 'Ledger' }).click()
  await expect(page.getByText('Virtual funding')).toBeVisible()
  await expect(page.getByText('Order hold')).toBeVisible()
  await expect(page.getByText('Trade settlement').first()).toBeVisible()
  await expect(page.getByText('Order release')).toBeVisible()
  await expect(page.locator('.ledger-grid code').filter({ hasText: /^\d+:\d+$/ }).first()).toBeVisible()

  await page.getByRole('button', { name: 'Refresh' }).click()
  harness.restartAuthoritativeFixture()
  await page.reload()
  await expect(page.getByText('1000', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Order history', exact: true }).click()
  await expect(page.getByText('Canceled')).toBeVisible()
  await page.getByRole('button', { name: 'Ledger' }).click()
  await expect(page.getByText('Order release')).toBeVisible()
})

test('logout closes an open order drawer and clears all private account facts', async ({ page }) => {
  await installBrowserContract(page)
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')
  await placeLimitMaker(page)
  await page.locator('.record-row.order-grid.clickable').first().click()
  await expect(page.getByRole('dialog', { name: 'Order details' })).toBeVisible()

  await page.getByRole('button', { name: /Sign out/ }).evaluate((button: HTMLButtonElement) => button.click())
  await expect(page.getByRole('dialog', { name: 'Order details' })).toHaveCount(0)
  await expect(page.getByText('1000', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Local sign in' })).toBeVisible()
  await expect(page.locator('.submit-order')).toBeDisabled()
})

test('an expired server session closes the order drawer on the next private refresh', async ({ page }) => {
  const harness = await installBrowserContract(page)
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')
  await placeLimitMaker(page)
  await page.locator('.record-row.order-grid.clickable').first().click()
  await expect(page.getByRole('dialog', { name: 'Order details' })).toBeVisible()

  harness.expireSession()
  await page.getByRole('button', { name: 'Refresh' }).evaluate((button: HTMLButtonElement) => button.click())
  await expect(page.getByRole('dialog', { name: 'Order details' })).toHaveCount(0)
  await expect(page.getByText('1000', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Local sign in' })).toBeVisible()
  await expect(page.locator('.submit-order')).toBeDisabled()
})

test('submit unknown queries authority then replays only the original client order ID', async ({ page }) => {
  const harness = await installBrowserContract(page, {
    submitResponseLostBeforeCommitOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')

  const requestID = await page.getByLabel('Client Order ID').inputValue()
  await page.getByLabel('Price · USDT').fill('64900')
  await page.getByLabel('Quantity · BTC').fill('0.001')
  await page.getByRole('button', { name: 'Buy BTC' }).click()
  await expect(page.getByRole('button', { name: 'Reconcile with original ID' })).toBeVisible()

  await page.getByRole('button', { name: 'Reconcile with original ID' }).click()
  await expect.poll(() => page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
  expect(harness.submitRequestIDs).toEqual([requestID, requestID])
  expect(harness.orders).toHaveLength(1)
})

test('committed submit response loss resolves from authoritative order history without replay', async ({ page }) => {
  const harness = await installBrowserContract(page, {
    submitCommittedButResponseLostOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')

  const requestID = await page.getByLabel('Client Order ID').inputValue()
  await page.getByLabel('Price · USDT').fill('64900')
  await page.getByLabel('Quantity · BTC').fill('0.001')
  await page.getByRole('button', { name: 'Buy BTC' }).click()
  await expect(page.getByRole('button', { name: 'Reconcile with original ID' })).toBeVisible()

  await page.getByRole('button', { name: 'Reconcile with original ID' }).click()
  await expect.poll(() => page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
  expect(harness.submitRequestIDs).toEqual([requestID])
  expect(harness.orders).toHaveLength(1)
})

test('cancel unknown replays only the persisted original request ID', async ({ page }) => {
  const harness = await installBrowserContract(page, {
    cancelResponseLostBeforeCommitOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')
  await placeLimitMaker(page)

  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Reconcile with original ID' })).toBeVisible()
  const firstRequestID = harness.cancelRequestIDs[0]
  expect(firstRequestID).toBeTruthy()

  await page.getByRole('button', { name: 'Reconcile with original ID' }).click()
  await expect.poll(() => page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
  expect(harness.cancelRequestIDs).toEqual([firstRequestID, firstRequestID])
  expect(harness.orders[0]?.status).toBe('canceled')
})

test('committed cancel response loss resolves from the terminal order without replay', async ({ page }) => {
  const harness = await installBrowserContract(page, {
    cancelCommittedButResponseLostOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await fundFromSystem(page, '1000')
  await page.goto('/trade/BTC-USDT')
  await placeLimitMaker(page)

  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Reconcile with original ID' })).toBeVisible()
  const requestID = harness.cancelRequestIDs[0]
  expect(requestID).toBeTruthy()

  await page.getByRole('button', { name: 'Reconcile with original ID' }).click()
  await expect.poll(() => page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
  expect(harness.cancelRequestIDs).toEqual([requestID])
  expect(harness.orders[0]?.status).toBe('canceled')
})

test('System funding unknown survives reload and reconciles with the actor-bound original ID', async ({ page }) => {
  const harness = await installBrowserContract(page, {
    fundCommittedButResponseLostOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await signIn(page)
  await page.goto('/system')
  await expect(page.getByTestId('system-trading-admin')).toBeVisible()
  await page.getByLabel('Target account').fill('github:virtual-beneficiary')
  await page.getByLabel('Amount').fill('250')
  await page.getByTestId('funding-submit').click()
  await expect(page.getByTestId('funding-pending')).toBeVisible()

  const stored = await page.evaluate(() => JSON.parse(
    window.localStorage.getItem('qiu-market.pending-trading-write.v2') ?? '{}',
  ) as { operation?: string; request_id?: string; account_id?: string })
  expect(stored.operation).toBe('fund')
  expect(stored.account_id).toBe(principal.account_id)
  expect(stored.request_id).toBe(harness.fundRequests[0]?.request_id)

  await page.reload()
  await page.getByTestId('funding-reconcile').click()
  await expect.poll(() => page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
  expect(harness.fundRequests.map((request) => request.request_id)).toEqual([
    stored.request_id,
    stored.request_id,
  ])
  expect(harness.fundRequests.map((request) => request.account_id)).toEqual([
    'github:virtual-beneficiary',
    'github:virtual-beneficiary',
  ])
})

test('transport cursor deduplicates replay, reconciles a gap and resumes from the authoritative cursor', async ({ page }) => {
  const harness = await installBrowserContract(page)
  await page.goto('/trade/BTC-USDT')
  await signIn(page)

  await expect.poll(() => harness.socketCount).toBe(1)
  expect(harness.socketURLs[0]).toContain('sequence=10&event_index=1')
  harness.sendEvent({ sequence: '11', event_index: 1 })
  harness.sendEvent({ sequence: '11', event_index: 1 })
  const socketsBeforeReplayResume = harness.socketCount
  await harness.closeLatestSocket()
  await expect.poll(() => harness.socketCount).toBeGreaterThan(socketsBeforeReplayResume)
  expect(harness.socketURLs.at(-1)).toContain('sequence=11&event_index=1')

  harness.setPublicReadDelay(700)
  const socketsBeforeGap = harness.socketCount
  harness.sendEvent({ sequence: '13', event_index: 1 })
  await expect(page.getByTestId('transport-reconcile')).toContainText('reconciling')
  await expect(page.getByTestId('terminal-availability')).toHaveText('DEGRADED')
  await expect(page.getByTestId('write-gate-reason')).toContainText('reconciliation')
  await expect(page.getByTestId('write-gate-reason')).not.toContainText('transport_reconcile_pending')
  harness.setPublicReadDelay(0)
  await expect.poll(() => harness.socketCount).toBeGreaterThan(socketsBeforeGap)
  expect(harness.socketURLs.at(-1)).toContain('sequence=13&event_index=1')

  const socketsBeforeDisconnect = harness.socketCount
  await harness.closeLatestSocket()
  await expect(page.getByTestId('transport-state')).toContainText('Polling')
  await expect.poll(() => harness.socketCount).toBeGreaterThan(socketsBeforeDisconnect)
  expect(harness.socketURLs.at(-1)).toContain('sequence=13&event_index=1')
})

for (const viewport of [
  { name: 'desktop', width: 1440, height: 1000 },
  { name: 'mobile-390', width: 390, height: 844 },
  { name: 'mobile-320', width: 320, height: 844 },
]) {
  test(`local practice Trade and System stay accessible without page overflow at ${viewport.name}`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    const consoleErrors: string[] = []
    const pageErrors: string[] = []
    const failedRequests: string[] = []
    const serverErrors: string[] = []
    const clientErrors: string[] = []
    page.on('console', (message) => {
      if (message.type() === 'error') consoleErrors.push(message.text())
    })
    page.on('pageerror', (error) => pageErrors.push(error.message))
    page.on('requestfailed', (request) => failedRequests.push(request.url()))
    page.on('response', (response) => {
      if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`)
      else if (response.status() >= 400) clientErrors.push(`${response.status()} ${response.url()}`)
    })

    await installBrowserContract(page, {
      practiceMode: true,
      initiallyLoggedIn: true,
      starterFunded: true,
    })
    await page.goto('/trade/BTC-USDT')
    await expect(page.getByRole('heading', { name: 'BTC / USDT' })).toBeVisible()
    await expect(page.getByText('Identity bound')).toBeVisible()

    const tradeDimensions = await page.evaluate(() => ({
      viewport: document.documentElement.clientWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
    }))
    expect(tradeDimensions.document).toBeLessThanOrEqual(tradeDimensions.viewport)
    expect(tradeDimensions.body).toBeLessThanOrEqual(tradeDimensions.viewport)

    const shortTradeTargets = await page.locator([
      '.trade-page button:visible',
      '.trade-page input:not([type="checkbox"]):visible',
      '.trade-page select:visible',
      '.trade-page label.checkbox:visible',
      '.trade-page a.btn:visible',
    ].join(',')).evaluateAll((elements) => elements.flatMap((element) => {
      const box = element.getBoundingClientRect()
      return box.height + 0.01 < 44
        ? [{ tag: element.tagName, text: element.textContent?.trim() ?? '', height: box.height }]
        : []
    }))
    expect(shortTradeTargets).toEqual([])

    await page.goto('/system')
    await expect(page.getByTestId('system-trading-practice')).toBeVisible()
    const systemDimensions = await page.evaluate(() => ({
      viewport: document.documentElement.clientWidth,
      document: document.documentElement.scrollWidth,
      body: document.body.scrollWidth,
    }))
    expect(systemDimensions.document).toBeLessThanOrEqual(systemDimensions.viewport)
    expect(systemDimensions.body).toBeLessThanOrEqual(systemDimensions.viewport)

    const shortSystemTargets = await page.getByTestId('system-trading-practice').locator([
      'button:visible',
      'input:visible',
      'select:visible',
      'summary:visible',
    ].join(',')).evaluateAll((elements) => elements.flatMap((element) => {
      const box = element.getBoundingClientRect()
      return box.height + 0.01 < 44
        ? [{ tag: element.tagName, text: element.textContent?.trim() ?? '', height: box.height }]
        : []
    }))
    expect(shortSystemTargets).toEqual([])
    expect({ consoleErrors, clientErrors }).toEqual({ consoleErrors: [], clientErrors: [] })
    expect(pageErrors).toEqual([])
    expect(failedRequests).toEqual([])
    expect(serverErrors).toEqual([])
  })
}
