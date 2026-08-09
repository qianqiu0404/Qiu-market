import { expect, test, type APIRequestContext, type Page, type Response } from '@playwright/test'
import type {
  TradeV1AccountTradePage,
  TradeV1LedgerPage,
  TradeV1OrderPage,
} from '../src/api/trade-v1-contract'
import type { Balance } from '../src/api/trading'

interface CommandResult {
  order_id: string
  sequence: string
  status: string
}

interface BrowserResult<T> {
  status: number
  body: T
}

interface GoldenState {
  market_id: string
  runtime_state: string
  sequence: number
  fact_count: number
  buyer_orders: number
  buyer_trades: number
  buyer_balances: Record<string, { available: string; held: string }>
  seller_balances: Record<string, { available: string; held: string }>
  platform_fees: Record<string, string>
  replay_evidence: { order_replays: number; fill_replays: number }
  ledger: { transaction_count: number; entry_count: number; balanced: boolean }
  journal_sums: Record<string, string>
}

const harnessPort = Number(process.env.QIU_GOLDEN_HARNESS_PORT ?? 19092)
const harnessURL = `http://127.0.0.1:${harnessPort}`
const orderPrice = '60000'
const orderQuantity = '0.01'

function balance(
  balances: Balance[],
  asset: 'BTC' | 'USDT',
): { available: string; held: string } {
  const value = balances.find((item) => item.asset === asset)
  expect(value, `${asset} balance is present`).toBeDefined()
  return { available: value?.available ?? '', held: value?.held ?? '' }
}

async function browserJSON<T>(page: Page, path: string): Promise<BrowserResult<T>> {
  return page.evaluate(async (requestPath) => {
    const response = await fetch(requestPath, {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    })
    return {
      status: response.status,
      body: await response.json() as T,
    }
  }, path)
}

async function browserWriteJSON<T>(
  page: Page,
  path: string,
  body: Record<string, string | boolean>,
): Promise<BrowserResult<T>> {
  return page.evaluate(async ({ requestPath, requestBody }) => {
    const csrfPrefix = `${encodeURIComponent('s78_trading_csrf')}=`
    const csrf = document.cookie.split(';')
      .map((item) => item.trim())
      .find((item) => item.startsWith(csrfPrefix))
      ?.slice(csrfPrefix.length) ?? ''
    const response = await fetch(requestPath, {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-CSRF-Token': decodeURIComponent(csrf),
      },
      body: JSON.stringify(requestBody),
    })
    return {
      status: response.status,
      body: await response.json() as T,
    }
  }, { requestPath: path, requestBody: body })
}

async function commandResult(response: Response): Promise<CommandResult> {
  expect(response.status()).toBe(200)
  return response.json() as Promise<CommandResult>
}

async function submitCurrentOrder(page: Page): Promise<CommandResult> {
  const response = page.waitForResponse((candidate) =>
    candidate.request().method() === 'POST' &&
    new URL(candidate.url()).pathname === '/api/v1/trading/orders')
  await page.getByRole('button', { name: 'Buy BTC' }).click()
  return commandResult(await response)
}

async function harnessPost(
  request: APIRequestContext,
  path: string,
  body: Record<string, string>,
): Promise<unknown> {
  const response = await request.post(`${harnessURL}${path}`, { data: body })
  expect(response.status()).toBe(200)
  return response.json()
}

async function harnessGet<T>(
  request: APIRequestContext,
  path: string,
): Promise<T> {
  const response = await request.get(`${harnessURL}${path}`)
  expect(response.status()).toBe(200)
  return response.json() as Promise<T>
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.clear()
    window.localStorage.setItem('qiu-market.locale', 'en')
  })
})

test('Vue drives the real HTTP harness from open through fill without duplicating an idempotent replay', async ({
  page,
  request,
}) => {
  const observedTradingResponses: Array<{ method: string; path: string; status: number }> = []
  page.on('response', (response) => {
    const url = new URL(response.url())
    if (url.pathname.startsWith('/api/v1/trading/')) {
      observedTradingResponses.push({
        method: response.request().method(),
        path: url.pathname,
        status: response.status(),
      })
    }
  })

  const ready = await request.get(`${harnessURL}/__golden/ready`)
  expect(ready.status()).toBe(200)
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: 'Local sign in' }).click()
  await expect(page.getByText('Identity bound')).toBeVisible()
  await expect(page.getByTestId('terminal-availability')).toHaveText('DEGRADED')
  await expect(page.getByText('liquidity Paused', { exact: true })).toBeVisible()
  await expect(page.locator('button.submit-order')).toHaveText('Fix order inputs')
  await expect(page.locator('button.submit-order')).toBeDisabled()

  const startingBalances = await browserJSON<{ balances: Balance[] }>(
    page,
    '/api/v1/trading/balances',
  )
  expect(startingBalances.status).toBe(200)
  expect(balance(startingBalances.body.balances, 'USDT')).toEqual({
    available: '1000',
    held: '0',
  })

  await page.getByLabel('Price · USDT').fill(orderPrice)
  await page.getByLabel('Quantity · BTC').fill(orderQuantity)
  await page.getByLabel('Post Only').check()
  await expect(page.getByRole('button', { name: 'Buy BTC' })).toBeEnabled()
  const clientOrderID = await page.getByLabel('Client Order ID').inputValue()

  const first = await submitCurrentOrder(page)
  expect(first).toMatchObject({ status: 'open' })
  expect(first.order_id).not.toBe('')
  await expect(page.locator('.record-row.order-grid')).toHaveCount(1)
  await expect(page.getByText('Open', { exact: true })).toBeVisible()

  const openBalances = await browserJSON<{ balances: Balance[] }>(page, '/api/v1/trading/balances')
  expect(openBalances.status).toBe(200)
  expect(balance(openBalances.body.balances, 'USDT')).toEqual({
    available: '400',
    held: '600',
  })
  const openLedger = await browserJSON<TradeV1LedgerPage>(
    page,
    '/api/v1/trading/ledger/entries?limit=50&asset=all&reason=all',
  )
  expect(openLedger.status).toBe(200)
  const openState = await harnessGet<GoldenState>(request, '/__golden/state')
  expect(openState).toMatchObject({
    market_id: 'BTC-USDT',
    runtime_state: 'ready',
    sequence: 3,
    fact_count: 3,
    buyer_orders: 1,
    buyer_trades: 0,
    buyer_balances: {
      USDT: { available: '400', held: '600' },
    },
  })
  expect(openState.replay_evidence.order_replays).toBe(0)
  expect(openState.ledger.balanced).toBe(true)

  const replay = await browserWriteJSON<CommandResult>(
    page,
    '/api/v1/trading/orders',
    {
      client_order_id: clientOrderID,
      side: 'buy',
      type: 'limit',
      time_in_force: 'gtc',
      post_only: true,
      price: orderPrice,
      quantity: orderQuantity,
      quote_budget: '',
    },
  )
  expect(replay.status).toBe(200)
  expect(replay.body).toEqual(first)
  await expect(page.locator('.record-row.order-grid')).toHaveCount(1)

  const replayedOrders = await browserJSON<TradeV1OrderPage>(
    page,
    '/api/v1/trading/orders?scope=all&limit=50',
  )
  expect(replayedOrders.status).toBe(200)
  expect(replayedOrders.body.orders).toHaveLength(1)
  expect(replayedOrders.body.orders[0]).toMatchObject({
    id: first.order_id,
    client_order_id: clientOrderID,
    status: 'open',
  })
  const replayLedger = await browserJSON<TradeV1LedgerPage>(
    page,
    '/api/v1/trading/ledger/entries?limit=50&asset=all&reason=all',
  )
  expect(replayLedger.status).toBe(200)
  expect(replayLedger.body.entries).toHaveLength(openLedger.body.entries.length)
  const replayState = await harnessGet<GoldenState>(request, '/__golden/state')
  expect(replayState).toMatchObject({
    sequence: openState.sequence,
    fact_count: openState.fact_count,
    buyer_orders: openState.buyer_orders,
    buyer_trades: openState.buyer_trades,
    ledger: {
      transaction_count: openState.ledger.transaction_count,
      entry_count: openState.ledger.entry_count,
      balanced: true,
    },
  })
  expect(replayState.replay_evidence.order_replays)
    .toBe(openState.replay_evidence.order_replays + 1)

  await harnessPost(request, '/__golden/fill', {
    resting_client_order_id: clientOrderID,
  })
  await expect.poll(async () => {
    const orders = await browserJSON<TradeV1OrderPage>(
      page,
      '/api/v1/trading/orders?scope=all&limit=50',
    )
    return orders.body.orders[0]?.status
  }).toBe('filled')

  await page.getByRole('button', { name: 'Refresh' }).click()
  await page.locator('.scope-switch').getByRole('button', { name: 'Order history' }).click()
  await expect(
    page.locator('.record-row.order-grid').getByText('Filled', { exact: true }),
  ).toBeVisible()

  const filledBalances = await browserJSON<{ balances: Balance[] }>(page, '/api/v1/trading/balances')
  expect(filledBalances.status).toBe(200)
  expect(balance(filledBalances.body.balances, 'USDT')).toEqual({
    available: '400',
    held: '0',
  })
  expect(balance(filledBalances.body.balances, 'BTC')).toEqual({
    available: '0.00999',
    held: '0',
  })

  await page.locator('.tabs').getByRole('button', { name: 'My trades' }).click()
  await expect(page.locator('.record-row.trade-grid')).toHaveCount(1)
  const trades = await browserJSON<TradeV1AccountTradePage>(
    page,
    '/api/v1/trading/account/trades?limit=50',
  )
  expect(trades.status).toBe(200)
  expect(trades.body.trades).toHaveLength(1)
  const tradeID = trades.body.trades[0]?.id
  expect(tradeID).toBeTruthy()
  expect(trades.body.trades[0]).toMatchObject({
    order_id: first.order_id,
    price: '60000',
    quantity: '0.01',
    quote_amount: '600',
    liquidity_role: 'maker',
    fee_asset: 'BTC',
    fee_amount: '0.00001',
    fee_rate_bps: '10',
  })

  await page.locator('.tabs').getByRole('button', { name: 'Ledger' }).click()
  const ledger = await browserJSON<TradeV1LedgerPage>(
    page,
    '/api/v1/trading/ledger/entries?limit=50&asset=all&reason=all',
  )
  expect(ledger.status).toBe(200)
  expect(ledger.body.entries).toHaveLength(5)
  expect(new Set(ledger.body.entries.map((entry) => entry.reason))).toEqual(
    new Set(['virtual_fund', 'order_hold', 'trade_settlement']),
  )
  const settlementEntries = ledger.body.entries.filter((entry) =>
    entry.reason === 'trade_settlement')
  expect(settlementEntries).toHaveLength(2)
  expect(new Set(settlementEntries.map((entry) => entry.transaction_id))).toEqual(
    new Set([`trade:${tradeID}`]),
  )
  expect(new Set(settlementEntries.map((entry) => entry.reference))).toEqual(
    new Set([`matched-trade:${tradeID}`]),
  )
  expect(settlementEntries.every((entry) => entry.trade_id === tradeID)).toBe(true)
  expect(settlementEntries).toEqual(expect.arrayContaining([
    expect.objectContaining({ asset: 'USDT', bucket: 'held', amount: '-600' }),
    expect.objectContaining({ asset: 'BTC', bucket: 'available', amount: '0.00999' }),
  ]))
  const holdEntries = ledger.body.entries.filter((entry) => entry.reason === 'order_hold')
  expect(holdEntries).toHaveLength(2)
  expect(holdEntries.every((entry) => entry.order_id === first.order_id)).toBe(true)
  expect(holdEntries).toEqual(expect.arrayContaining([
    expect.objectContaining({ asset: 'USDT', bucket: 'available', amount: '-600' }),
    expect.objectContaining({ asset: 'USDT', bucket: 'held', amount: '600' }),
  ]))
  await expect(page.locator('.record-row.ledger-grid')).toHaveCount(ledger.body.entries.length)

  const state = await harnessGet<GoldenState>(request, '/__golden/state')
  expect(state).toMatchObject({
    market_id: 'BTC-USDT',
    runtime_state: 'ready',
    sequence: 4,
    fact_count: 4,
    buyer_orders: 1,
    buyer_trades: 1,
    buyer_balances: {
      BTC: { available: '0.00999', held: '0' },
      USDT: { available: '400', held: '0' },
    },
    seller_balances: {
      BTC: { available: '0', held: '0' },
      USDT: { available: '598.8', held: '0' },
    },
    platform_fees: {
      BTC: '0.00001',
      USDT: '1.2',
    },
    ledger: {
      transaction_count: 5,
      entry_count: 14,
      balanced: true,
    },
    replay_evidence: {
      fill_replays: 0,
    },
    journal_sums: {
      BTC: '0',
      USDT: '0',
    },
  })
  expect(observedTradingResponses.filter((item) =>
    item.method === 'POST' && item.path === '/api/v1/trading/orders')).toHaveLength(2)
  expect(observedTradingResponses.every((item) => item.status < 500)).toBe(true)
})
