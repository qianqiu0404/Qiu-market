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

interface PartialGoldenState {
  market_id: string
  runtime_state: string
  sequence: number
  fact_count: number
  state_hash: string
  buyer_orders: number
  buyer_trades: number
  buyer_balances: Record<string, { available: string; held: string }>
  seller_balances: Record<string, { available: string; held: string }>
  platform_fees: Record<string, string>
  replay_evidence: { cancel_replays: number }
  ledger: { transaction_count: number; entry_count: number; balanced: boolean }
  journal_sums: Record<string, string>
  duplicate_transactions: boolean
}

interface RestartResult {
  snapshot_found: boolean
  snapshot_sequence: number
  snapshot_hash: string
  before_sequence: number
  before_state_hash: string
  after_sequence: number
  after_state_hash: string
  record_count_before: number
  record_count_after: number
  unchanged: boolean
}

const harnessPort = Number(process.env.QIU_PARTIAL_GOLDEN_HARNESS_PORT ?? 19093)
const harnessURL = `http://127.0.0.1:${harnessPort}`
const controlBase = '/__partial-golden'
const orderPrice = '60000'
const orderQuantity = '0.02'

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
    return { status: response.status, body: await response.json() as T }
  }, path)
}

async function browserWriteJSON<T>(
  page: Page,
  path: string,
  body: Record<string, string>,
): Promise<BrowserResult<T>> {
  return page.evaluate(async ({ requestPath, requestBody }) => {
    const prefix = `${encodeURIComponent('s78_trading_csrf')}=`
    const encodedCSRF = document.cookie.split(';')
      .map((item) => item.trim())
      .find((item) => item.startsWith(prefix))
      ?.slice(prefix.length) ?? ''
    const response = await fetch(requestPath, {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-CSRF-Token': decodeURIComponent(encodedCSRF),
      },
      body: JSON.stringify(requestBody),
    })
    return { status: response.status, body: await response.json() as T }
  }, { requestPath: path, requestBody: body })
}

async function controlGet<T>(request: APIRequestContext, path: string): Promise<T> {
  const response = await request.get(`${harnessURL}${controlBase}${path}`)
  expect(response.status()).toBe(200)
  return response.json() as Promise<T>
}

async function controlPost<T>(
  request: APIRequestContext,
  path: string,
  body: Record<string, string> = {},
): Promise<T> {
  const response = await request.post(`${harnessURL}${controlBase}${path}`, { data: body })
  expect(response.status()).toBe(200)
  return response.json() as Promise<T>
}

async function commandResult(response: Response): Promise<CommandResult> {
  expect(response.status()).toBe(200)
  return response.json() as Promise<CommandResult>
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.clear()
    window.localStorage.setItem('qiu-market.locale', 'en')
  })
})

test('partial fill survives restart, then UI cancel and cancel replay stay idempotent', async ({
  page,
  request,
}) => {
  const writes: Array<{ path: string; body: Record<string, unknown> }> = []
  page.on('request', (candidate) => {
    const path = new URL(candidate.url()).pathname
    if (candidate.method() !== 'POST' || !path.startsWith('/api/v1/trading/')) return
    writes.push({
      path,
      body: candidate.postDataJSON() as Record<string, unknown>,
    })
  })

  await controlGet(request, '/ready')
  await page.goto('/trade/BTC-USDT')
  await page.getByRole('button', { name: 'Local sign in' }).click()
  await expect(page.getByText('Identity bound')).toBeVisible()

  const startingBalances = await browserJSON<{ balances: Balance[] }>(
    page,
    '/api/v1/trading/balances',
  )
  expect(startingBalances.status).toBe(200)
  expect(balance(startingBalances.body.balances, 'USDT')).toEqual({
    available: '2000',
    held: '0',
  })

  await page.getByLabel('Price · USDT').fill(orderPrice)
  await page.getByLabel('Quantity · BTC').fill(orderQuantity)
  await page.getByLabel('Post Only').check()
  const clientOrderID = await page.getByLabel('Client Order ID').inputValue()
  const submitResponse = page.waitForResponse((candidate) =>
    candidate.request().method() === 'POST' &&
    new URL(candidate.url()).pathname === '/api/v1/trading/orders')
  await page.getByRole('button', { name: 'Buy BTC' }).click()
  const submitted = await commandResult(await submitResponse)
  expect(submitted).toMatchObject({ status: 'open' })

  await controlPost(request, '/partial-fill', {
    resting_client_order_id: clientOrderID,
  })
  await expect.poll(async () => {
    const orders = await browserJSON<TradeV1OrderPage>(
      page,
      '/api/v1/trading/orders?scope=all&limit=50',
    )
    return orders.body.orders[0]?.status
  }).toBe('partially_filled')
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByText('Partially filled', { exact: true })).toBeVisible()

  const partialOrders = await browserJSON<TradeV1OrderPage>(
    page,
    '/api/v1/trading/orders?scope=all&limit=50',
  )
  expect(partialOrders.status).toBe(200)
  expect(partialOrders.body.orders).toHaveLength(1)
  expect(partialOrders.body.orders[0]).toMatchObject({
    id: submitted.order_id,
    client_order_id: clientOrderID,
    status: 'partially_filled',
    original_quantity: '0.02',
    filled_quantity: '0.01',
    remaining_quantity: '0.01',
    held_asset: 'USDT',
    held_amount: '600',
  })
  const partialBalances = await browserJSON<{ balances: Balance[] }>(
    page,
    '/api/v1/trading/balances',
  )
  expect(balance(partialBalances.body.balances, 'USDT')).toEqual({
    available: '800',
    held: '600',
  })
  expect(balance(partialBalances.body.balances, 'BTC')).toEqual({
    available: '0.00999',
    held: '0',
  })

  const partialTrades = await browserJSON<TradeV1AccountTradePage>(
    page,
    '/api/v1/trading/account/trades?limit=50',
  )
  expect(partialTrades.body.trades).toHaveLength(1)
  expect(partialTrades.body.trades[0]).toMatchObject({
    order_id: submitted.order_id,
    price: '60000',
    quantity: '0.01',
    quote_amount: '600',
    liquidity_role: 'maker',
    fee_asset: 'BTC',
    fee_amount: '0.00001',
    fee_rate_bps: '10',
  })
  const partialLedger = await browserJSON<TradeV1LedgerPage>(
    page,
    '/api/v1/trading/ledger/entries?limit=50&asset=all&reason=all',
  )
  expect(partialLedger.body.entries).toHaveLength(5)

  const beforeRestart = await controlGet<PartialGoldenState>(request, '/state')
  expect(beforeRestart).toMatchObject({
    sequence: 4,
    fact_count: 4,
    buyer_orders: 1,
    buyer_trades: 1,
    buyer_balances: {
      BTC: { available: '0.00999', held: '0' },
      USDT: { available: '800', held: '600' },
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
    duplicate_transactions: false,
    journal_sums: {
      BTC: '0',
      USDT: '0',
    },
  })
  const restart = await controlPost<RestartResult>(request, '/restart')
  expect(restart).toEqual({
    snapshot_found: true,
    snapshot_sequence: beforeRestart.sequence,
    snapshot_hash: beforeRestart.state_hash,
    before_sequence: beforeRestart.sequence,
    before_state_hash: beforeRestart.state_hash,
    after_sequence: beforeRestart.sequence,
    after_state_hash: beforeRestart.state_hash,
    record_count_before: beforeRestart.fact_count,
    record_count_after: beforeRestart.fact_count,
    unchanged: true,
  })
  const afterRestart = await controlGet<PartialGoldenState>(request, '/state')
  expect(afterRestart).toMatchObject({
    runtime_state: 'ready',
    sequence: beforeRestart.sequence,
    fact_count: beforeRestart.fact_count,
    state_hash: beforeRestart.state_hash,
    buyer_orders: beforeRestart.buyer_orders,
    buyer_trades: beforeRestart.buyer_trades,
    buyer_balances: beforeRestart.buyer_balances,
    seller_balances: beforeRestart.seller_balances,
    platform_fees: beforeRestart.platform_fees,
    ledger: beforeRestart.ledger,
    journal_sums: beforeRestart.journal_sums,
  })
  const restartedOrders = await browserJSON<TradeV1OrderPage>(
    page,
    '/api/v1/trading/orders?scope=all&limit=50',
  )
  expect(restartedOrders.body.orders).toEqual(partialOrders.body.orders)
  const restartedBalances = await browserJSON<{ balances: Balance[] }>(
    page,
    '/api/v1/trading/balances',
  )
  expect(restartedBalances.body.balances).toEqual(partialBalances.body.balances)
  const restartedTrades = await browserJSON<TradeV1AccountTradePage>(
    page,
    '/api/v1/trading/account/trades?limit=50',
  )
  expect(restartedTrades.body).toEqual(partialTrades.body)
  const restartedLedger = await browserJSON<TradeV1LedgerPage>(
    page,
    '/api/v1/trading/ledger/entries?limit=50&asset=all&reason=all',
  )
  expect(restartedLedger.body).toEqual(partialLedger.body)

  const cancelRequest = page.waitForRequest((candidate) =>
    candidate.method() === 'POST' &&
    new URL(candidate.url()).pathname ===
      `/api/v1/trading/orders/${encodeURIComponent(submitted.order_id)}/cancel`)
  const cancelResponse = page.waitForResponse((candidate) =>
    candidate.request().method() === 'POST' &&
    new URL(candidate.url()).pathname ===
      `/api/v1/trading/orders/${encodeURIComponent(submitted.order_id)}/cancel`)
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  const uiCancelRequest = await cancelRequest
  const cancelBody = uiCancelRequest.postDataJSON() as { request_id: string }
  expect(cancelBody.request_id).toMatch(/^cancel-/)
  const canceled = await commandResult(await cancelResponse)
  expect(canceled).toMatchObject({
    order_id: submitted.order_id,
    status: 'canceled',
    sequence: '5',
  })

  await expect.poll(async () => {
    const orders = await browserJSON<TradeV1OrderPage>(
      page,
      '/api/v1/trading/orders?scope=all&limit=50',
    )
    return orders.body.orders[0]?.status
  }).toBe('canceled')
  await page.getByRole('button', { name: 'Refresh' }).click()
  await page.getByRole('button', { name: 'Order history', exact: true }).click()
  await expect(page.getByText('Canceled', { exact: true })).toBeVisible()

  const finalOrders = await browserJSON<TradeV1OrderPage>(
    page,
    '/api/v1/trading/orders?scope=all&limit=50',
  )
  expect(finalOrders.body.orders).toHaveLength(1)
  expect(finalOrders.body.orders[0]).toMatchObject({
    id: submitted.order_id,
    status: 'canceled',
    filled_quantity: '0.01',
    remaining_quantity: '0.01',
    held_amount: '0',
  })
  const finalBalances = await browserJSON<{ balances: Balance[] }>(
    page,
    '/api/v1/trading/balances',
  )
  expect(balance(finalBalances.body.balances, 'USDT')).toEqual({
    available: '1400',
    held: '0',
  })
  expect(balance(finalBalances.body.balances, 'BTC')).toEqual({
    available: '0.00999',
    held: '0',
  })
  const finalTrades = await browserJSON<TradeV1AccountTradePage>(
    page,
    '/api/v1/trading/account/trades?limit=50',
  )
  expect(finalTrades.body.trades).toHaveLength(1)
  expect(finalTrades.body).toEqual(partialTrades.body)
  const finalLedger = await browserJSON<TradeV1LedgerPage>(
    page,
    '/api/v1/trading/ledger/entries?limit=50&asset=all&reason=all',
  )
  expect(finalLedger.body.entries).toHaveLength(7)
  const releaseEntries = finalLedger.body.entries.filter((entry) =>
    entry.reason === 'order_release')
  expect(releaseEntries).toHaveLength(2)
  const releaseTransactions = new Set(releaseEntries.map((entry) => entry.transaction_id))
  expect(releaseTransactions).toHaveProperty('size', 1)
  expect([...releaseTransactions][0]).toMatch(/^cancel-release:/)
  expect(new Set(releaseEntries.map((entry) => entry.reference))).toEqual(
    new Set([`order-cancel:${submitted.order_id}`]),
  )
  expect(releaseEntries.every((entry) => entry.order_id === submitted.order_id)).toBe(true)
  expect(releaseEntries).toEqual(expect.arrayContaining([
    expect.objectContaining({ asset: 'USDT', bucket: 'held', amount: '-600' }),
    expect.objectContaining({ asset: 'USDT', bucket: 'available', amount: '600' }),
  ]))

  const beforeCancelReplay = await controlGet<PartialGoldenState>(request, '/state')
  expect(beforeCancelReplay).toMatchObject({
    sequence: 5,
    fact_count: 5,
    buyer_orders: 1,
    buyer_trades: 1,
    buyer_balances: {
      BTC: { available: '0.00999', held: '0' },
      USDT: { available: '1400', held: '0' },
    },
    seller_balances: beforeRestart.seller_balances,
    platform_fees: beforeRestart.platform_fees,
    replay_evidence: {
      cancel_replays: 0,
    },
    ledger: {
      transaction_count: 6,
      entry_count: 16,
      balanced: true,
    },
    duplicate_transactions: false,
    journal_sums: {
      BTC: '0',
      USDT: '0',
    },
  })
  const replay = await browserWriteJSON<CommandResult>(
    page,
    `/api/v1/trading/orders/${encodeURIComponent(submitted.order_id)}/cancel`,
    cancelBody,
  )
  expect(replay.status).toBe(200)
  expect(replay.body).toEqual(canceled)
  const afterCancelReplay = await controlGet<PartialGoldenState>(request, '/state')
  expect(afterCancelReplay).toMatchObject({
    sequence: beforeCancelReplay.sequence,
    fact_count: beforeCancelReplay.fact_count,
    state_hash: beforeCancelReplay.state_hash,
    buyer_orders: beforeCancelReplay.buyer_orders,
    buyer_trades: beforeCancelReplay.buyer_trades,
    buyer_balances: beforeCancelReplay.buyer_balances,
    seller_balances: beforeCancelReplay.seller_balances,
    platform_fees: beforeCancelReplay.platform_fees,
    ledger: beforeCancelReplay.ledger,
    journal_sums: beforeCancelReplay.journal_sums,
    duplicate_transactions: false,
  })
  expect(afterCancelReplay.replay_evidence.cancel_replays)
    .toBe(beforeCancelReplay.replay_evidence.cancel_replays + 1)
  const replayedOrders = await browserJSON<TradeV1OrderPage>(
    page,
    '/api/v1/trading/orders?scope=all&limit=50',
  )
  expect(replayedOrders.body).toEqual(finalOrders.body)
  const replayedBalances = await browserJSON<{ balances: Balance[] }>(
    page,
    '/api/v1/trading/balances',
  )
  expect(replayedBalances.body).toEqual(finalBalances.body)
  const replayedTrades = await browserJSON<TradeV1AccountTradePage>(
    page,
    '/api/v1/trading/account/trades?limit=50',
  )
  expect(replayedTrades.body).toEqual(finalTrades.body)
  const replayedLedger = await browserJSON<TradeV1LedgerPage>(
    page,
    '/api/v1/trading/ledger/entries?limit=50&asset=all&reason=all',
  )
  expect(replayedLedger.body).toEqual(finalLedger.body)

  expect(writes.filter((write) => write.path === '/api/v1/trading/orders')).toEqual([
    expect.objectContaining({
      body: expect.objectContaining({
        client_order_id: clientOrderID,
        price: orderPrice,
        quantity: orderQuantity,
      }),
    }),
  ])
  expect(writes.filter((write) => write.path.endsWith('/cancel'))).toEqual([
    expect.objectContaining({ body: cancelBody }),
    expect.objectContaining({ body: cancelBody }),
  ])
})
