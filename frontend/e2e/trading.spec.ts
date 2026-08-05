import {
  expect,
  test,
  type Page,
  type Route,
  type WebSocketRoute,
} from '@playwright/test'

const principal = {
  account_id: 'github:qianqiu0404',
  github_login: 'qianqiu0404',
  admin: true,
}

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

interface HarnessOptions {
  realKlines?: boolean
  authDisabled?: boolean
  publicReadDelayMs?: number
  submitCommittedButResponseLostOnce?: boolean
  submitResponseLostBeforeCommitOnce?: boolean
  cancelCommittedButResponseLostOnce?: boolean
  cancelResponseLostBeforeCommitOnce?: boolean
  fundCommittedButResponseLostOnce?: boolean
  recoveryStatus?: Record<string, unknown>
  recoveryUnavailable?: boolean
  recoveryLegacy404?: boolean
  recoveryGateEnabled?: boolean
}

type HarnessPanel =
  | 'kline'
  | 'orderbook'
  | 'publicTrades'
  | 'status'
  | 'balances'
  | 'orders'
  | 'privateTrades'

async function installHarness(page: Page, options: HarnessOptions = {}) {
  let loggedIn = false
  let sequence = 10
  let logoutAttempts = 0
  let submitAttempts = 0
  let cancelAttempts = 0
  let fundAttempts = 0
  let activePublicReads = 0
  let maximumConcurrentPublicReads = 0
  let publicReadDelayMs = options.publicReadDelayMs ?? 0
  let recoveryStatus = options.recoveryStatus ?? writableRecoveryStatus()
  let recoveryUnavailable = options.recoveryUnavailable === true
  let loseSubmitBeforeCommit = options.submitResponseLostBeforeCommitOnce === true
  let loseSubmitAfterCommit = options.submitCommittedButResponseLostOnce === true
  let loseCancelBeforeCommit = options.cancelResponseLostBeforeCommitOnce === true
  let balances: Array<{ asset: string; available: string; held: string }> = []
  let orders: Array<Record<string, unknown>> = []
  let trades: Array<Record<string, unknown>> = []
  const cancelRequestIDs: string[] = []
  const submitRequestIDs: string[] = []
  const operationTrace: string[] = []
  const fundRequests: Array<{
    request_id: string
    account_id: string
    asset: string
    amount: string
  }> = []
  const cancelResults = new Map<string, {
    sequence: string
    status: string
    order_id: string
  }>()
  const fundResults = new Map<string, {
    sequence: string
    status: string
  }>()
  const failedPanels = new Set<HarnessPanel>()
  const sockets: WebSocketRoute[] = []
  const socketURLs: string[] = []

  await page.routeWebSocket('**/api/v1/trading/events/ws**', (webSocket) => {
    sockets.push(webSocket)
    socketURLs.push(webSocket.url())
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
    const publicJSON = async (status: number, body: unknown) => {
      activePublicReads += 1
      maximumConcurrentPublicReads = Math.max(
        maximumConcurrentPublicReads,
        activePublicReads,
      )
      try {
        if (publicReadDelayMs) {
          await new Promise((resolve) => setTimeout(resolve, publicReadDelayMs))
        }
        await json(status, body)
      } finally {
        activePublicReads -= 1
      }
    }
    const marketEnvelope = (result: unknown) => json(200, { code: 2000, result })
    const failRead = async (panel: HarnessPanel) => {
      if (!failedPanels.has(panel)) return false
      await json(503, {
        code: 'backend_unavailable',
        message: `${panel} test read is unavailable`,
      })
      return true
    }

    if (path === '/api/v2/get_asset_dashboard') {
      await json(200, { code: 2000, result: [btcAsset], total: 1 })
      return
    }
    if (path === '/api/v2/get_asset_markets' || path === '/api/v2/get_asset_venues') {
      await marketEnvelope([btcVenue])
      return
    }
    if (path === '/api/v1/get_klines') {
      if (await failRead('kline')) return
      const candles = options.realKlines === false
        ? []
        : [
            { timestamp: 1785035940000, open: '64950', high: '65020', low: '64900', close: '65000', volume: '8.2' },
            { timestamp: 1785036000000, open: '65000', high: '65080', low: '64980', close: '65040', volume: '7.1' },
          ]
      await marketEnvelope(candles)
      return
    }
    if (path === '/api/v1/get_system_overview') {
      await marketEnvelope({
        crawler_status: 'running',
        redis_status: 'running',
        database_status: 'running',
        worker_status: 'running',
        api_status: 'running',
        provider_statuses: [],
      })
      return
    }

    if (path === '/api/v1/trading/auth/local') {
      loggedIn = true
      await json(200, { principal, local: true })
      return
    }
    if (path === '/api/v1/trading/auth/capabilities') {
      await json(200, {
        github_oauth_enabled: false,
        local_login_enabled: !options.authDisabled,
        recovery_gate_enabled: options.recoveryGateEnabled ?? !options.recoveryLegacy404,
      })
      return
    }
    if (path === '/api/v1/trading/session') {
      await json(
        loggedIn ? 200 : 401,
        loggedIn
          ? { principal, expires_at: '2026-07-27T00:00:00Z' }
          : { code: 'invalid_session', message: 'session is invalid or expired' },
      )
      return
    }
    if (path === '/api/v1/trading/auth/logout') {
      logoutAttempts += 1
      loggedIn = false
      await route.fulfill({ status: 204 })
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/orderbook') {
      if (await failRead('orderbook')) return
      await publicJSON(200, {
        market_id: 'BTC-USDT',
        sequence: String(sequence),
        bids: [{ price: '64990', quantity: '0.5', order_count: 1 }],
        asks: [{ price: '65010', quantity: '0.5', order_count: 1 }],
      })
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/status') {
      if (await failRead('status')) return
      await publicJSON(200, {
        market_id: 'BTC-USDT',
        state: 'ready',
        sequence: String(sequence),
        queue_depth: 0,
        recovery_count: '1',
        last_error: '',
        outbox_state: 'ready',
        outbox_checkpoint_sequence: String(sequence),
        outbox_checkpoint_event_index: 1,
      })
      return
    }
    if (path === '/api/v1/trading/recovery/status') {
      if (recoveryUnavailable) {
        await json(503, {
          code: 'recovery_in_progress',
          message: 'recovery evidence unavailable',
        })
      } else if (options.recoveryLegacy404) {
        await json(404, { code: 'not_found', message: 'recovery gate not enabled' })
      } else {
        await json(200, recoveryStatus)
      }
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/trades') {
      if (await failRead('publicTrades')) return
      await publicJSON(200, { trades })
      return
    }
    if (path === '/api/v1/trading/balances') {
      if (await failRead('balances')) return
      await json(loggedIn ? 200 : 401, loggedIn
        ? { balances }
        : { code: 'invalid_session', message: 'session is invalid or expired' })
      return
    }
    if (path === '/api/v1/trading/orders' && request.method() === 'GET') {
      if (await failRead('orders')) return
      operationTrace.push('query:orders')
      await json(loggedIn ? 200 : 401, loggedIn
        ? { orders }
        : { code: 'invalid_session', message: 'session is invalid or expired' })
      return
    }
    if (path === '/api/v1/trading/trades') {
      if (await failRead('privateTrades')) return
      await json(loggedIn ? 200 : 401, loggedIn
        ? { trades }
        : { code: 'invalid_session', message: 'session is invalid or expired' })
      return
    }
    if (path === '/api/v1/trading/ws-ticket') {
      await json(201, {
        ticket: 'one-time-browser-ticket',
        expires_at: '2026-07-26T04:00:30Z',
      })
      return
    }
    if (path === '/api/v1/trading/admin/fund') {
      const body = request.postDataJSON() as {
        request_id: string
        account_id: string
        asset: string
        amount: string
      }
      fundAttempts += 1
      fundRequests.push(body)
      let result = fundResults.get(body.request_id)
      if (!result) {
        const existing = balances.find((balance) => balance.asset === body.asset)
        if (existing) existing.available = body.amount
        else balances = [...balances, { asset: body.asset, available: body.amount, held: '0' }]
        sequence += 1
        result = { sequence: String(sequence), status: 'unknown' }
        fundResults.set(body.request_id, result)
        if (options.fundCommittedButResponseLostOnce) {
          await json(504, {
            code: 'backend_timeout',
            message: 'committed response was lost',
          })
          return
        }
      }
      await json(200, result)
      return
    }
    if (path === '/api/v1/trading/orders' && request.method() === 'POST') {
      const body = request.postDataJSON() as Record<string, unknown>
      expect(body.account_id).toBeUndefined()
      const requestID = String(body.client_order_id ?? '')
      submitAttempts += 1
      submitRequestIDs.push(requestID)
      operationTrace.push(`submit:${requestID}`)
      if (loseSubmitBeforeCommit) {
        loseSubmitBeforeCommit = false
        await json(504, {
          code: 'backend_timeout',
          message: 'request outcome is not yet visible',
        })
        return
      }
      const existingOrder = orders.find(
        (candidate) => candidate.client_order_id === requestID,
      )
      if (existingOrder) {
        await json(200, {
          sequence: existingOrder.accepted_sequence,
          status: 'accepted',
          order_id: existingOrder.id,
        })
        return
      }
      sequence += 1
      const id = `order-${orders.length + 1}`
      const marketOrder = body.type === 'market'
      const order = {
        id,
        client_order_id: body.client_order_id,
        account_id: principal.account_id,
        market_id: 'BTC-USDT',
        side: body.side,
        type: body.type,
        time_in_force: body.time_in_force,
        post_only: body.post_only,
        price: body.price,
        original_quantity: marketOrder ? '0.001' : body.quantity,
        remaining_quantity: marketOrder ? '0' : body.quantity,
        filled_quantity: marketOrder ? '0.001' : '0',
        original_quote_budget: body.quote_budget,
        remaining_quote_budget: marketOrder ? '34.99' : '0',
        spent_quote: marketOrder ? '65.01' : '0',
        held_asset: marketOrder ? '' : 'USDT',
        held_amount: marketOrder ? '0' : '65.01',
        status: marketOrder ? 'filled' : 'open',
        accepted_sequence: String(sequence),
        last_sequence: String(sequence),
        reject_reason: '',
      }
      orders = [order, ...orders]
      if (marketOrder) {
        trades = [{
          id: 'trade-1',
          market_id: 'BTC-USDT',
          price: '65010',
          quantity: '0.001',
          quote_amount: '65.01',
          maker_order_id: 'maker-ask-1',
          taker_order_id: id,
          maker_account_id: 'system:demo-maker',
          taker_account_id: principal.account_id,
          buyer_account_id: principal.account_id,
          seller_account_id: 'system:demo-maker',
          buyer_fee: {
            account_id: principal.account_id,
            asset: 'BTC',
            amount: '0.000002',
            rate_bps: '20',
            role: 'taker',
          },
          seller_fee: {
            account_id: 'system:demo-maker',
            asset: 'USDT',
            amount: '0.06501',
            rate_bps: '10',
            role: 'maker',
          },
        }]
      }
      if (loseSubmitAfterCommit) {
        loseSubmitAfterCommit = false
        await json(504, {
          code: 'backend_timeout',
          message: 'committed response was lost',
        })
        return
      }
      await json(200, { sequence: String(sequence), status: 'accepted', order_id: id })
      return
    }
    if (
      /^\/api\/v1\/trading\/orders\/[^/]+$/.test(path) &&
      request.method() === 'GET'
    ) {
      const id = path.split('/')[5]
      const order = orders.find((candidate) => candidate.id === id)
      await json(
        order ? 200 : 404,
        order ?? { code: 'order_not_found', message: 'order not found' },
      )
      return
    }
    if (/^\/api\/v1\/trading\/orders\/[^/]+\/cancel$/.test(path)) {
      const id = path.split('/')[5]
      const body = request.postDataJSON() as { request_id: string }
      cancelAttempts += 1
      cancelRequestIDs.push(body.request_id)
      if (loseCancelBeforeCommit) {
        loseCancelBeforeCommit = false
        await json(504, {
          code: 'backend_timeout',
          message: 'request outcome is not yet visible',
        })
        return
      }
      let result = cancelResults.get(body.request_id)
      if (!result) {
        orders = orders.map((order) => order.id === id
          ? { ...order, status: 'canceled', held_amount: '0', last_sequence: String(sequence + 1) }
          : order)
        sequence += 1
        result = {
          sequence: String(sequence),
          status: 'accepted',
          order_id: id,
        }
        cancelResults.set(body.request_id, result)
        if (options.cancelCommittedButResponseLostOnce) {
          await json(504, {
            code: 'backend_timeout',
            message: 'committed response was lost',
          })
          return
        }
      }
      await json(200, result)
      return
    }

    await json(404, { code: 'not_found', message: `Unhandled test route: ${path}` })
  })

  return {
    get orders() { return orders },
    get trades() { return trades },
    get sequence() { return sequence },
    get submitAttempts() { return submitAttempts },
    get submitRequestIDs() { return [...submitRequestIDs] },
    get operationTrace() { return [...operationTrace] },
    get logoutAttempts() { return logoutAttempts },
    get cancelAttempts() { return cancelAttempts },
    get cancelRequestIDs() { return [...cancelRequestIDs] },
    get fundAttempts() { return fundAttempts },
    get fundRequests() { return [...fundRequests] },
    get maximumConcurrentPublicReads() { return maximumConcurrentPublicReads },
    get socketCount() { return sockets.length },
    get socketURLs() { return [...socketURLs] },
    sendEvent(event: {
      market_id?: string
      sequence: string
      event_index: number
      event?: unknown
    }) {
      const webSocket = sockets.at(-1)
      if (!webSocket) throw new Error('no routed trading WebSocket')
      const numericSequence = Number(event.sequence)
      if (Number.isSafeInteger(numericSequence)) {
        sequence = Math.max(sequence, numericSequence)
      }
      webSocket.send(JSON.stringify({
        market_id: 'BTC-USDT',
        event: {},
        ...event,
      }))
    },
    async closeLatestSocket() {
      const webSocket = sockets.at(-1)
      if (!webSocket) throw new Error('no routed trading WebSocket')
      await webSocket.close({ code: 1012, reason: 'test disconnect' })
    },
    setPublicReadDelay(delayMs: number) {
      publicReadDelayMs = delayMs
    },
    setRecoveryStatus(next: Record<string, unknown>) {
      recoveryStatus = next
      recoveryUnavailable = false
    },
    setRecoveryUnavailable(unavailable = true) {
      recoveryUnavailable = unavailable
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

test('trade page refuses to invent K-lines when the trusted source has no candles', async ({ page }) => {
  await installHarness(page, { realKlines: false })
  await page.goto('/trade/BTC-USDT')

  await expect(page.getByRole('heading', { name: 'BTC / USDT' })).toBeVisible()
  await expect(page.getByText('NO MOCK · NO STATIC FALLBACK')).toBeVisible()
  await expect(page.getByText('The selected venue returned no real candles')).toBeVisible()
})

test('trade page does not probe a session when every login method is disabled', async ({ page }) => {
  let sessionRequests = 0
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/v1/trading/session') {
      sessionRequests++
    }
  })
  await installHarness(page, { authDisabled: true })
  await page.goto('/trade/BTC-USDT')

  await expect(page.getByText('登录未配置')).toBeVisible()
  await expect(page.getByText('offline', { exact: true })).toHaveCount(0)
  await expect(page.getByTestId('terminal-availability')).toHaveText('LIVE')
  await expect(page.getByTestId('transport-state')).toHaveText('polling')
  expect(sessionRequests).toBe(0)
})

test('recovery admission exposes proof, blocks writes and switches to Chinese', async ({ page }) => {
  await installHarness(page, {
    recoveryStatus: {
      schema_version: 2,
	  provenance: writableRecoveryStatus().provenance,
      market_id: 'BTC-USDT',
      epoch_id: '0123456789abcdef0123456789abcdef',
      phase: 'transport_warmup',
      runtime_sequence: '42',
      state_hash: 'a'.repeat(64),
      ledger_balanced: true,
      event_continuous: true,
      projection_caught_up: true,
      outbox_caught_up: true,
      transport_healthy: false,
      writes_enabled: false,
      continuity_uncertain: false,
      continuity_error: '',
      version: '6',
      started_at: '2026-08-05T00:00:00Z',
      updated_at: '2026-08-05T00:01:00Z',
    },
  })
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()

  await expect(page.getByTestId('recovery-phase')).toHaveText('Transport warmup')
  await expect(page.getByTestId('recovery-epoch')).toContainText('012345678')
  await expect(page.getByTestId('recovery-server-flag')).toHaveText('Blocked')
  await expect(page.getByTestId('recovery-effective-admission')).toHaveText('Blocked')
  await expect(page.getByTestId('recovery-proof-count')).toHaveText('5 / 6')
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'write_gate=recovery_transport_warmup',
  )
  await expect(page.getByRole('button', { name: '买入 BTC' })).toBeDisabled()
  await expect(page.getByRole('button', { name: '发放虚拟资金' })).toBeDisabled()

  await page.getByRole('button', { name: '中文' }).click()
  await expect(page.getByTestId('recovery-phase')).toHaveText('传输预热')
  await expect(page.getByTestId('recovery-server-flag')).toHaveText('已禁止')
  await expect(page.getByTestId('recovery-effective-admission')).toHaveText('已禁止')
  await expect(page.getByText('本面板只镜像展示证据；服务端 runner 与 gateway 才是权威准入门禁。')).toBeVisible()
})

test('recovery 404 remains explicit compatibility without claiming writable proof', async ({ page }) => {
  await installHarness(page, {
    recoveryLegacy404: true,
    recoveryGateEnabled: false,
  })
  await page.goto('/trade/BTC-USDT')
  await expect(page.getByTestId('recovery-admission')).toHaveAttribute(
    'data-recovery-mode',
    'not_enabled',
  )
  await expect(page.getByTestId('recovery-server-flag')).toHaveText('Not reported')
  await expect(page.getByTestId('recovery-effective-admission')).toHaveText('Legacy gate')
  await expect(page.getByText(/trusted capability explicitly reports/)).toBeVisible()
  await expect(page.getByTestId('recovery-proof-count')).toHaveCount(0)
  await expect(page.getByText('Continuity', { exact: true })).toHaveCount(0)
})

test('blocked recovery reconciles pending submit by query only and never replays POST', async ({ page }) => {
  const harness = await installHarness(page, {
    submitResponseLostBeforeCommitOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()
  await page.getByLabel('价格 · USDT').fill('64900')
  await page.getByLabel('数量 · BTC').fill('0.001')
  await page.getByRole('button', { name: '买入 BTC' }).click()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toBeVisible()
  expect(harness.submitAttempts).toBe(1)

  harness.setRecoveryStatus(writableRecoveryStatus({
    phase: 'writable',
    writes_enabled: true,
    transport_healthy: false,
    continuity_uncertain: true,
    continuity_error: 'store save result is uncertain',
    last_error: 'continuity probe failed',
    version: '7',
  }))
  await page.getByRole('button', { name: '刷新' }).click()
  await expect(page.getByTestId('recovery-admission')).toHaveAttribute(
    'data-recovery-mode',
    'blocked',
  )
  await expect(page.getByTestId('recovery-phase')).toHaveText('Writable')
  await expect(page.getByTestId('recovery-server-flag')).toHaveText('Enabled')
  await expect(page.getByTestId('recovery-effective-admission')).toHaveText('Blocked')
  await expect(page.getByTestId('recovery-admission')).toContainText(
    'store save result is uncertain',
  )

  await page.getByRole('button', { name: '使用原 ID 核对' }).click()
  await expect(page.getByText(/queried only/)).toBeVisible()
  expect(harness.submitAttempts).toBe(1)
  expect(JSON.parse(await page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2') ?? '{}',
  )).state).toBe('unknown')
})

test('same-epoch recovery version rollback fails closed', async ({ page }) => {
  const harness = await installHarness(page)
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()
  await expect(page.getByTestId('write-gate-reason')).toContainText('write_gate=ready')

  harness.setRecoveryStatus(writableRecoveryStatus({ version: '5' }))
  await page.getByRole('button', { name: '刷新' }).click()
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'write_gate=recovery_status_unavailable',
  )
  await expect(page.getByRole('button', { name: '买入 BTC' })).toBeDisabled()
})

test('status older than ten seconds degrades the terminal and closes every write gate', async ({ page }) => {
  await page.addInitScript(() => {
    const realNow = Date.now.bind(Date)
    const clock = window as typeof window & { __tradeClockOffset?: number }
    clock.__tradeClockOffset = 0
    Date.now = () => realNow() + (clock.__tradeClockOffset ?? 0)
  })
  const harness = await installHarness(page)
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()
  await expect(page.getByTestId('terminal-availability')).toHaveText('LIVE')
  await expect(page.getByTestId('write-gate-reason')).toContainText('write_gate=ready')

  harness.setPanelFailure('status')
  await page.evaluate(() => {
    const clock = window as typeof window & { __tradeClockOffset?: number }
    clock.__tradeClockOffset = 11_500
  })

  await expect(page.getByTestId('terminal-availability')).toHaveText('DEGRADED')
  await expect(page.getByTestId('matching-state')).toHaveText('stale')
  await expect(page.getByText(/data_age_seconds=1[1-9]/)).toBeVisible()
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'write_gate=matching_status_stale',
  )
  await expect(page.getByRole('button', { name: '买入 BTC' })).toBeDisabled()
  await expect(page.getByRole('button', { name: '发放虚拟资金' })).toBeDisabled()
})

test('websocket cursor deduplicates replay, reconciles gaps and resumes after polling', async ({ page }) => {
  const harness = await installHarness(page)
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()

  await expect(page.getByTestId('transport-state')).toHaveText('websocket')
  await expect.poll(() => harness.socketCount).toBe(1)
  expect(harness.socketURLs[0]).toContain('sequence=10&event_index=1')

  harness.sendEvent({ sequence: '11', event_index: 1 })
  await expect(page.getByTestId('event-cursor-state')).toContainText('11:1')

  harness.sendEvent({ sequence: '11', event_index: 1 })
  await expect(page.getByTestId('event-duplicate-count')).toContainText('1')
  await expect(page.getByTestId('event-cursor-state')).toContainText('11:1')

  harness.setPublicReadDelay(800)
  harness.sendEvent({ sequence: '13', event_index: 1 })
  await expect(page.getByTestId('event-gap-count')).toContainText('1')
  await expect(page.getByTestId('transport-reconcile')).toContainText(
    'cursor_reconcile=pending',
  )
  await expect(page.getByTestId('terminal-availability')).toHaveText('DEGRADED')
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'write_gate=transport_reconcile_pending',
  )
  await expect(page.getByRole('button', { name: '买入 BTC' })).toBeDisabled()

  await expect(page.getByTestId('event-cursor-state')).toContainText('13:1')
  await expect(page.getByTestId('transport-reconcile')).toHaveCount(0)
  harness.setPublicReadDelay(0)
  await expect.poll(() => harness.socketCount).toBeGreaterThan(1)
  await expect(page.getByTestId('transport-state')).toHaveText('websocket')
  expect(harness.socketURLs.at(-1)).toContain('sequence=13&event_index=1')

  const socketsBeforeDisconnect = harness.socketCount
  await harness.closeLatestSocket()
  await expect(page.getByTestId('transport-state')).toHaveText('polling')
  await expect.poll(() => harness.socketCount).toBeGreaterThan(socketsBeforeDisconnect)
  expect(harness.socketURLs.at(-1)).toContain('sequence=13&event_index=1')

  harness.sendEvent({ sequence: '13', event_index: 1 })
  await expect(page.getByTestId('event-duplicate-count')).toContainText('2')
  await expect(page.getByTestId('event-cursor-state')).toContainText('13:1')
})

test('slow public polling reuses one in-flight refresh batch', async ({ page }) => {
  const harness = await installHarness(page, {
    authDisabled: true,
    publicReadDelayMs: 3_500,
  })
  await page.goto('/trade/BTC-USDT')

  await expect(page.getByTestId('matching-state')).toHaveText('ready')
  await page.waitForTimeout(7_000)

  expect(harness.maximumConcurrentPublicReads).toBe(3)
})

test('panel failures retain last-good data and never become a full-page outage', async ({ page }) => {
  const harness = await installHarness(page)
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()

  await page.getByLabel('数量', { exact: true }).last().fill('10000')
  await page.getByRole('button', { name: '发放虚拟资金' }).click()
  await page.getByRole('button', { name: 'Market' }).click()
  await page.getByLabel('Quote Budget · USDT').fill('100')
  await page.getByRole('button', { name: '买入 BTC' }).click()
  await expect(page.getByText('我的成交 · 1')).toBeVisible()

  for (const panel of [
    'kline',
    'orderbook',
    'publicTrades',
    'balances',
    'orders',
    'privateTrades',
  ] as const) {
    harness.setPanelFailure(panel)
  }
  await page.getByRole('button', { name: '1m' }).click()
  await page.getByRole('button', { name: '刷新' }).click()

  for (const testID of [
    'panel-kline-state',
    'panel-orderbook-state',
    'panel-public-trades-state',
    'panel-balances-state',
    'panel-orders-state',
    'panel-private-trades-state',
  ]) {
    await expect(page.getByTestId(testID)).toContainText('LAST GOOD')
  }
  await expect(page.getByRole('button', { name: 'Bid 64990' })).toBeVisible()
  await expect(page.getByText('10000', { exact: true })).toBeVisible()
  await expect(page.getByText('历史委托 · 1')).toBeVisible()
  await expect(page.locator('.trade-chart canvas')).toBeVisible()
  await expect(page.locator('.trade-toast--error')).toHaveCount(0)
})

test('admin can fund, place, cancel and fill virtual orders with fee evidence', async ({ page }) => {
  const harness = await installHarness(page)
  await page.goto('/trade/BTC-USDT')

  await page.getByRole('button', { name: '本地登录' }).click()
  await expect(page.getByText('身份已绑定')).toBeVisible()

  await page.getByLabel('数量', { exact: true }).last().fill('10000')
  await page.getByRole('button', { name: '发放虚拟资金' }).click()
  await expect(page.getByText('10,000')).toHaveCount(0)
  await expect(page.getByText('10000', { exact: true })).toBeVisible()

  await page.getByLabel('价格 · USDT').fill('64900')
  await page.getByLabel('数量 · BTC').fill('0.001')
  await page.getByRole('button', { name: '买入 BTC' }).click()
  await expect(page.getByText('当前委托 · 1')).toBeVisible()
  await page.getByRole('button', { name: '撤单' }).click()
  await expect(page.getByText('历史委托 · 1')).toBeVisible()

  await page.getByRole('button', { name: 'Market' }).click()
  await page.getByLabel('Quote Budget · USDT').fill('100')
  await page.getByRole('button', { name: '买入 BTC' }).click()

  await expect(page.getByText('我的成交 · 1')).toBeVisible()
  await expect(page.getByText('0.000002 BTC')).toBeVisible()
  expect(harness.orders).toHaveLength(2)
  expect(harness.trades).toHaveLength(1)
})

test('committed submit response loss resolves from the order fact without replay', async ({ page }) => {
  const harness = await installHarness(page, {
    submitCommittedButResponseLostOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()

  const requestID = await page.getByLabel('Client Order ID').inputValue()
  await page.getByLabel('价格 · USDT').fill('64900')
  await page.getByLabel('数量 · BTC').fill('0.001')
  await page.getByRole('button', { name: '买入 BTC' }).click()
  await expect(page.getByText(/submitted\/unknown/)).toBeVisible()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toHaveCount(0, {
    timeout: 4_000,
  })

  expect(harness.submitAttempts).toBe(1)
  expect(harness.submitRequestIDs).toEqual([requestID])
  expect(harness.orders).toHaveLength(1)
  expect(await page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
})

test('submit unknown persists operation identity and queries before same-ID replay', async ({ page }) => {
  const harness = await installHarness(page, {
    submitResponseLostBeforeCommitOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()

  const requestID = await page.getByLabel('Client Order ID').inputValue()
  await page.getByLabel('价格 · USDT').fill('64900')
  await page.getByLabel('数量 · BTC').fill('0.001')
  await page.getByRole('button', { name: '买入 BTC' }).click()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toBeVisible()

  const stored = await page.evaluate(() => JSON.parse(
    window.localStorage.getItem('qiu-market.pending-trading-write.v2') ?? '{}',
  ) as {
    operation_id?: string
    operation?: string
    account_id?: string
    request_id?: string
    state?: string
    payload?: { client_order_id?: string }
  })
  expect(stored.operation_id).toMatch(/^operation-/)
  expect(stored.operation).toBe('submit')
  expect(stored.account_id).toBe(principal.account_id)
  expect(stored.request_id).toBe(requestID)
  expect(stored.state).toBe('unknown')
  expect(stored.payload?.client_order_id).toBe(requestID)
  expect(await page.evaluate(() =>
    window.sessionStorage.getItem('qiu-market.pending-trading-write.v1'),
  )).toBeNull()

  const traceStart = harness.operationTrace.length
  await page.getByRole('button', { name: '使用原 ID 核对' }).click()
  await expect.poll(() => page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toHaveCount(0)

  expect(harness.submitAttempts).toBe(2)
  expect(harness.submitRequestIDs).toEqual([requestID, requestID])
  expect(harness.operationTrace.slice(traceStart)).toEqual([
    'query:orders',
    `submit:${requestID}`,
    'query:orders',
  ])
  expect(harness.orders).toHaveLength(1)
  expect(await page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
})

test('terminal cancel fact clears unknown without issuing a second cancel', async ({ page }) => {
  const harness = await installHarness(page, {
    cancelCommittedButResponseLostOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()

  await page.getByLabel('价格 · USDT').fill('64900')
  await page.getByLabel('数量 · BTC').fill('0.001')
  await page.getByRole('button', { name: '买入 BTC' }).click()
  await expect(page.getByText('当前委托 · 1')).toBeVisible()

  await page.getByRole('button', { name: '撤单' }).click()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toBeVisible()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toHaveCount(0, {
    timeout: 4_000,
  })

  expect(harness.cancelAttempts).toBe(1)
  expect(harness.cancelRequestIDs).toHaveLength(1)
  expect(harness.orders[0]?.status).toBe('canceled')
})

test('open cancel unknown replays the original request ID exactly once', async ({ page }) => {
  const harness = await installHarness(page, {
    cancelResponseLostBeforeCommitOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()

  await page.getByLabel('价格 · USDT').fill('64900')
  await page.getByLabel('数量 · BTC').fill('0.001')
  await page.getByRole('button', { name: '买入 BTC' }).click()
  await expect(page.getByText('当前委托 · 1')).toBeVisible()

  await page.getByRole('button', { name: '撤单' }).click()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toBeVisible()
  await expect(page.getByTestId('terminal-availability')).toHaveText('DEGRADED')
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'write_gate=reconcile_pending',
  )
  await expect(page.getByRole('button', { name: '买入 BTC' })).toBeDisabled()
  const stored = await page.evaluate(() => JSON.parse(
    window.localStorage.getItem('qiu-market.pending-trading-write.v2') ?? '{}',
  ) as {
    operation_id?: string
    operation?: string
    request_id?: string
    order_id?: string
  })
  expect(stored.operation_id).toMatch(/^operation-/)
  expect(stored.operation).toBe('cancel')
  expect(stored.request_id).toBe(harness.cancelRequestIDs[0])
  expect(stored.order_id).toBe(harness.orders[0]?.id)
  expect(harness.orders[0]?.status).toBe('open')

  await page.getByRole('button', { name: '使用原 ID 核对' }).click()
  await expect.poll(() => page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toHaveCount(0)

  expect(harness.cancelAttempts).toBe(2)
  expect(harness.cancelRequestIDs[0]).toBeTruthy()
  expect(harness.cancelRequestIDs[1]).toBe(harness.cancelRequestIDs[0])
  expect(harness.orders[0]?.status).toBe('canceled')
  expect(harness.sequence).toBe(12)
})

test('fund unknown survives reload and replays the same actor-bound request', async ({ page }) => {
  const harness = await installHarness(page, {
    fundCommittedButResponseLostOnce: true,
  })
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()

  const targetAccount = 'github:virtual-beneficiary'
  await page.getByLabel('数量', { exact: true }).last().fill('250')
  await page.getByLabel('目标账户（留空为自己）').fill(targetAccount)
  await page.getByRole('button', { name: '发放虚拟资金' }).click()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toBeVisible()

  const stored = await page.evaluate(() => JSON.parse(
    window.localStorage.getItem('qiu-market.pending-trading-write.v2') ?? '{}',
  ) as {
    operation_id?: string
    operation?: string
    account_id?: string
    request_id?: string
    payload?: {
      account_id?: string
      asset?: string
      amount?: string
    }
  })
  expect(stored.operation_id).toMatch(/^operation-/)
  expect(stored.operation).toBe('fund')
  expect(stored.account_id).toBe(principal.account_id)
  expect(stored.payload?.account_id).toBe(targetAccount)
  expect(stored.payload?.asset).toBe('USDT')
  expect(stored.payload?.amount).toBe('250')
  expect(stored.request_id).toBe(harness.fundRequests[0]?.request_id)

  await page.getByRole('button', { name: /退出/ }).click()
  await expect(page.getByRole('button', { name: '原账户登录后核对' })).toBeDisabled()
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'write_gate=login_required',
  )
  expect(harness.fundAttempts).toBe(1)
  await page.getByRole('button', { name: '本地登录' }).click()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toBeEnabled()

  await page.reload()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toBeVisible()
  await page.getByRole('button', { name: '使用原 ID 核对' }).click()
  await expect.poll(() => page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toHaveCount(0)

  expect(harness.fundAttempts).toBe(2)
  expect(harness.fundRequests.map((request) => request.request_id)).toEqual([
    stored.request_id,
    stored.request_id,
  ])
  expect(harness.fundRequests.map((request) => request.account_id)).toEqual([
    targetAccount,
    targetAccount,
  ])
  expect(harness.sequence).toBe(11)
  expect(await page.evaluate(() =>
    window.localStorage.getItem('qiu-market.pending-trading-write.v2'),
  )).toBeNull()
})

test('logout clears private last-good views, closes writes and does not reconnect', async ({ page }) => {
  const harness = await installHarness(page)
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()
  await page.getByLabel('数量', { exact: true }).last().fill('10000')
  await page.getByRole('button', { name: '发放虚拟资金' }).click()
  await expect(page.getByText('10000', { exact: true })).toBeVisible()
  await expect.poll(() => harness.socketCount).toBe(1)

  await page.getByRole('button', { name: /退出/ }).click()

  expect(harness.logoutAttempts).toBe(1)
  await expect(page.getByRole('button', { name: '本地登录' })).toBeVisible()
  await expect(page.getByText('10000', { exact: true })).toHaveCount(0)
  await expect(page.getByText('登录后查看账户余额')).toBeVisible()
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'write_gate=login_required',
  )
  await expect(page.getByRole('button', { name: '登录后下单' })).toBeDisabled()
  await expect(page.getByRole('button', { name: '发放虚拟资金' })).toBeDisabled()
  await expect(page.getByTestId('transport-state')).toHaveText('polling')
  await page.waitForTimeout(1_500)
  expect(harness.socketCount).toBe(1)
})

test('a server-expired session invalidates every private panel on the next read', async ({ page }) => {
  const harness = await installHarness(page)
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: '本地登录' }).click()
  await page.getByLabel('数量', { exact: true }).last().fill('500')
  await page.getByRole('button', { name: '发放虚拟资金' }).click()
  await expect(page.getByText('500', { exact: true })).toBeVisible()

  harness.expireSession()
  await page.getByRole('button', { name: '刷新' }).click()

  await expect(page.getByText('会话已失效；私有视图和写入口已清除，请重新登录')).toBeVisible()
  await expect(page.getByRole('button', { name: '本地登录' })).toBeVisible()
  await expect(page.getByText('500', { exact: true })).toHaveCount(0)
  await expect(page.getByTestId('panel-balances-state')).toContainText('LOADING')
  await expect(page.getByTestId('panel-orders-state')).toContainText('LOADING')
  await expect(page.getByTestId('write-gate-reason')).toContainText(
    'write_gate=login_required',
  )
  await expect(page.getByTestId('transport-state')).toHaveText('polling')
})

for (const viewport of [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'compact', width: 1180, height: 820 },
  { name: 'mobile', width: 768, height: 900 },
]) {
  test(`trade terminal contains page overflow at ${viewport.name} width`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await installHarness(page)
    await page.goto('/trade/BTC-USDT')
    await expect(page.getByRole('heading', { name: 'BTC / USDT' })).toBeVisible()

    const dimensions = await page.evaluate(() => ({
      viewport: document.documentElement.clientWidth,
      page: document.documentElement.scrollWidth,
    }))
    expect(dimensions.page).toBeLessThanOrEqual(dimensions.viewport)
  })
}
