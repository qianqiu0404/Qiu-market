import { expect, test, type Page, type Route } from '@playwright/test'

const principal = {
  account_id: 'github:qianqiu0404',
  github_login: 'qianqiu0404',
  admin: true,
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
  cancelCommittedButResponseLostOnce?: boolean
  cancelResponseLostBeforeCommitOnce?: boolean
  fundCommittedButResponseLostOnce?: boolean
}

async function installHarness(page: Page, options: HarnessOptions = {}) {
  let loggedIn = false
  let sequence = 10
  let cancelAttempts = 0
  let fundAttempts = 0
  let activePublicReads = 0
  let maximumConcurrentPublicReads = 0
  let loseCancelBeforeCommit = options.cancelResponseLostBeforeCommitOnce === true
  let balances: Array<{ asset: string; available: string; held: string }> = []
  let orders: Array<Record<string, unknown>> = []
  let trades: Array<Record<string, unknown>> = []
  const cancelRequestIDs: string[] = []
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

  await page.routeWebSocket('**/api/v1/trading/events/ws**', () => {
    // Keeping the mocked socket open is enough for the page to expose "live".
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
        if (options.publicReadDelayMs) {
          await new Promise((resolve) => setTimeout(resolve, options.publicReadDelayMs))
        }
        await json(status, body)
      } finally {
        activePublicReads -= 1
      }
    }
    const marketEnvelope = (result: unknown) => json(200, { code: 2000, result })

    if (path === '/api/v2/get_asset_dashboard') {
      await json(200, { code: 2000, result: [btcAsset], total: 1 })
      return
    }
    if (path === '/api/v2/get_asset_markets' || path === '/api/v2/get_asset_venues') {
      await marketEnvelope([btcVenue])
      return
    }
    if (path === '/api/v1/get_klines') {
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
      loggedIn = false
      await route.fulfill({ status: 204 })
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/orderbook') {
      await publicJSON(200, {
        market_id: 'BTC-USDT',
        sequence: String(sequence),
        bids: [{ price: '64990', quantity: '0.5', order_count: 1 }],
        asks: [{ price: '65010', quantity: '0.5', order_count: 1 }],
      })
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/status') {
      await publicJSON(200, {
        market_id: 'BTC-USDT',
        state: 'ready',
        sequence: String(sequence),
        queue_depth: 0,
        recovery_count: '1',
        last_error: '',
      })
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/trades') {
      await publicJSON(200, { trades })
      return
    }
    if (path === '/api/v1/trading/balances') {
      await json(loggedIn ? 200 : 401, loggedIn
        ? { balances }
        : { code: 'invalid_session', message: 'session is invalid or expired' })
      return
    }
    if (path === '/api/v1/trading/orders' && request.method() === 'GET') {
      await json(200, { orders })
      return
    }
    if (path === '/api/v1/trading/trades') {
      await json(200, { trades })
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
    get cancelAttempts() { return cancelAttempts },
    get cancelRequestIDs() { return [...cancelRequestIDs] },
    get fundAttempts() { return fundAttempts },
    get fundRequests() { return [...fundRequests] },
    get maximumConcurrentPublicReads() { return maximumConcurrentPublicReads },
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
  await expect(page.getByText('polling', { exact: true })).toHaveCount(2)
  await expect(page.getByText('offline', { exact: true })).toHaveCount(0)
  expect(sessionRequests).toBe(0)
})

test('slow public polling reuses one in-flight refresh batch', async ({ page }) => {
  const harness = await installHarness(page, {
    authDisabled: true,
    publicReadDelayMs: 3_500,
  })
  await page.goto('/trade/BTC-USDT')

  await expect(page.getByText('sequence 10', { exact: false })).toBeVisible()
  await page.waitForTimeout(7_000)

  expect(harness.maximumConcurrentPublicReads).toBe(3)
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
  expect(harness.orders[0]?.status).toBe('open')

  await page.getByRole('button', { name: '使用原 ID 核对' }).click()
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
    window.sessionStorage.getItem('qiu-market.pending-trading-write.v1') ?? '{}',
  ) as {
    operation?: string
    account_id?: string
    request_id?: string
    payload?: {
      account_id?: string
      asset?: string
      amount?: string
    }
  })
  expect(stored.operation).toBe('fund')
  expect(stored.account_id).toBe(principal.account_id)
  expect(stored.payload?.account_id).toBe(targetAccount)
  expect(stored.payload?.asset).toBe('USDT')
  expect(stored.payload?.amount).toBe('250')
  expect(stored.request_id).toBe(harness.fundRequests[0]?.request_id)

  await page.reload()
  await expect(page.getByRole('button', { name: '使用原 ID 核对' })).toBeVisible()
  await page.getByRole('button', { name: '使用原 ID 核对' }).click()
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
    window.sessionStorage.getItem('qiu-market.pending-trading-write.v1'),
  )).toBeNull()
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
