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
}

async function installHarness(page: Page, options: HarnessOptions = {}) {
  let loggedIn = false
  let sequence = 10
  let balances: Array<{ asset: string; available: string; held: string }> = []
  let orders: Array<Record<string, unknown>> = []
  let trades: Array<Record<string, unknown>> = []

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
      await json(200, {
        market_id: 'BTC-USDT',
        sequence: String(sequence),
        bids: [{ price: '64990', quantity: '0.5', order_count: 1 }],
        asks: [{ price: '65010', quantity: '0.5', order_count: 1 }],
      })
      return
    }
    if (path === '/api/v1/trading/markets/BTC-USDT/status') {
      await json(200, {
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
      await json(200, { trades })
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
      const body = request.postDataJSON() as { asset: string; amount: string }
      const existing = balances.find((balance) => balance.asset === body.asset)
      if (existing) existing.available = body.amount
      else balances = [...balances, { asset: body.asset, available: body.amount, held: '0' }]
      sequence += 1
      await json(200, { sequence: String(sequence), status: 'accepted' })
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
    if (/^\/api\/v1\/trading\/orders\/[^/]+\/cancel$/.test(path)) {
      const id = path.split('/')[5]
      orders = orders.map((order) => order.id === id
        ? { ...order, status: 'canceled', held_amount: '0', last_sequence: String(sequence + 1) }
        : order)
      sequence += 1
      await json(200, { sequence: String(sequence), status: 'accepted', order_id: id })
      return
    }

    await json(404, { code: 'not_found', message: `Unhandled test route: ${path}` })
  })

  return {
    get orders() { return orders },
    get trades() { return trades },
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
  expect(sessionRequests).toBe(0)
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
