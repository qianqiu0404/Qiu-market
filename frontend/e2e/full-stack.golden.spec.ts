import { expect, test, type APIRequestContext, type Locator, type Page, type Request } from '@playwright/test'
import { readFile, stat } from 'node:fs/promises'
import type { TradeV1AccountTradePage, TradeV1LedgerPage, TradeV1OrderPage } from '../src/api/trade-v1-contract'
import type { Balance } from '../src/api/trading'

interface ProcessEvidence {
  generation: 'A' | 'B'
  pid: number
  exited?: boolean
  sequence?: number
  state_hash?: string
}
interface Manifest {
  schema_version: 'qiu.full-stack.manifest.v1'
  api_origin: string
  ready_url: string
  control_url: string
  state_url: string
  evidence_url: string
  coordinator_pid: number
  fixture_pid: number
  vue_pid: number
  postgres: { pid: number; version: string; authority: 'isolated_ephemeral_postgresql'; snapshot_sequence?: number; head_sequence?: number; snapshot_matches_head?: boolean; snapshot_matches_runtime?: boolean }
  backend: ProcessEvidence
}
interface OrderEvidence {
  client_order_id: string
  order_id: string
  status: string
  filled_quantity: string
  remaining_quantity: string
  held_amount: string
}
interface QualityFaultEvidence {
  source: string
  operation: string
  http_outcome: string
  http_error_kind?: string
  normalized_error_kind?: string
  quality_outcome: string
  hard_faults: string[]
  http_status?: number
  retry_after_seconds?: number
}
interface QualityWindowEvidence {
  scenario: string
  end: string
  sources: Array<{
    source: string; status: string; score_bps: number | null; reasons: string[];
    healthy_window_streak: number; original_license: string; golden_license_assumption: boolean;
  }>
  faults: QualityFaultEvidence[]
}
interface ControlResponse {
  schema_version: 'qiu.full-stack.control.v1'
  action: string
  phase: string
  generation: 'A' | 'B'
  backend_pid: number
  sequence: number
  state_hash: string
  order?: OrderEvidence
  previous_backend?: ProcessEvidence
  current_backend?: ProcessEvidence
  restored?: boolean
  scenario?: string
  wait_milliseconds?: number
  quality?: QualityWindowEvidence
}
interface DatabaseCounts {
  facts: number
  trades: number
  ledger_transactions: number
  ledger_entries: number
  orders: number
}
interface ReferenceFact {
  source: string
  market_id: string
  price: string
  observed_at: string
  hash: string
}
interface DatabaseState {
  digest: string
  sequence: number
  snapshot_sequence: number
  event_hash: string
  snapshot_hash: string
  counts: DatabaseCounts
  buyer_balances: Record<string, { available: string; held: string }>
  seller_balances: Record<string, { available: string; held: string }>
  platform_fees: Record<string, string>
  orders: Record<string, OrderEvidence>
  trades: Array<{
    trade_id: string; sequence: number; price: string; quantity: string; quote_amount: string;
    buyer_order_id: string; seller_order_id: string; maker_order_id: string; taker_order_id: string;
    buyer_fee_asset: string; buyer_fee_amount: string; buyer_fee_rate_bps: number; buyer_fee_role: string;
    seller_fee_asset: string; seller_fee_amount: string; seller_fee_rate_bps: number; seller_fee_role: string;
  }>
  ledger_transactions: Array<{
    transaction_id: string; sequence: number; reference: string;
    entries: Array<{ index: number; account: string; asset: string; amount: string }>
  }>
  journal_sums: Record<string, string>
  duplicate_transactions: boolean
  reference_mismatch: boolean
}
interface FullStackState {
  schema_version: 'qiu.full-stack.state.v1'
  observed_at: string
  phase: string
  generation: 'A' | 'B'
  backend_pid: number
  database: DatabaseState
}
interface FullStackEvidence {
  schema_version: 'qiu.full-stack.evidence.v1'
  postgres: Manifest['postgres']
  coordinator_pid: number
  fixture_pid: number
  vue_pid: number
  backend_a: ProcessEvidence
  backend_b: ProcessEvidence
  restore: { before: ProcessEvidence; after: ProcessEvidence; same_sequence: boolean; same_state_hash: boolean }
  replay: {
    cancel_request_id: string; cancel_requests: number; original_sequence: number; replay_sequence: number;
    original_status: string; replay_status: string; before_counts: DatabaseCounts; after_counts: DatabaseCounts;
    before_digest: string; after_digest: string; before_event_hash: string; after_event_hash: string; no_delta: boolean;
  }
  reference: { before: ReferenceFact; after: ReferenceFact; unchanged: boolean }
  partial: DatabaseState
  final: DatabaseState
  quality: QualityWindowEvidence[]
  spy: Record<string, number>
  cleanup_armed: boolean
}
interface CommandResult { order_id: string; sequence: string; status: string }
interface BrowserResult<T> { status: number; body: T }

const apiOrigin = requiredLoopbackOrigin('QIU_FULLSTACK_API_ORIGIN')
const frontendOrigin = requiredLoopbackOrigin('QIU_FULLSTACK_FRONTEND_ORIGIN')
const manifestPath = process.env.QIU_FULLSTACK_MANIFEST?.trim() ?? ''
const fullClientOrderID = 'full-stack-full-v1'
const partialClientOrderID = 'full-stack-partial-v1'

function requiredLoopbackOrigin(name: string): string {
  const raw = process.env[name]?.trim()
  if (!raw) throw new Error(`${name} is required`)
  const value = new URL(raw)
  if (
    value.protocol !== 'http:' || value.hostname !== '127.0.0.1' || value.username || value.password
    || value.pathname !== '/' || value.search !== '' || value.hash !== ''
  ) {
    throw new Error(`${name} must be an exact credential-free loopback HTTP origin`)
  }
  return value.origin
}

function expectBalance(
  balances: Balance[], asset: 'BTC' | 'USDT', expected: { available: string; held: string },
): void {
  expect(balances.find((item) => item.asset === asset), `${asset} balance`).toMatchObject(expected)
}

function assetAtoms(value: string, asset: 'BTC' | 'USDT'): bigint {
  const scale = asset === 'BTC' ? 8 : 6
  const match = /^(-?)(0|[1-9]\d*)(?:\.(\d+))?$/u.exec(value)
  expect(match, `canonical ${asset} decimal ${value}`).not.toBeNull()
  const fraction = match?.[3] ?? ''
  expect(fraction.length, `${asset} precision`).toBeLessThanOrEqual(scale)
  const magnitude = BigInt(match?.[2] ?? '0') * 10n ** BigInt(scale)
    + BigInt(fraction.padEnd(scale, '0') || '0')
  return match?.[1] === '-' ? -magnitude : magnitude
}

function expectLedgerConservation(database: DatabaseState): void {
  expect(database.ledger_transactions).toHaveLength(database.counts.ledger_transactions)
  expect(new Set(database.ledger_transactions.map((item) => item.transaction_id)).size)
    .toBe(database.ledger_transactions.length)
  expect(database.ledger_transactions.reduce((sum, item) => sum + item.entries.length, 0))
    .toBe(database.counts.ledger_entries)
  for (const transaction of database.ledger_transactions) {
    expect(transaction.transaction_id).not.toBe('')
    expect(transaction.reference).not.toBe('')
    expect(transaction.sequence).toBeGreaterThan(0)
    expect(transaction.sequence).toBeLessThanOrEqual(database.sequence)
    expect(transaction.entries.length).toBeGreaterThanOrEqual(2)
    expect(transaction.entries.map((entry) => entry.index)).toEqual(
      [...transaction.entries.keys()].map((index) => index + 1),
    )
    const sums = new Map<string, bigint>()
    for (const entry of transaction.entries) {
      expect(entry.account).not.toBe('')
      expect(['BTC', 'USDT']).toContain(entry.asset)
      const asset = entry.asset as 'BTC' | 'USDT'
      sums.set(asset, (sums.get(asset) ?? 0n) + assetAtoms(entry.amount, asset))
    }
    for (const [asset, sum] of sums) expect(sum, `${transaction.transaction_id} ${asset} balances`).toBe(0n)
  }
}

function expectFinalAssetConservation(database: DatabaseState): void {
  const btc = assetAtoms(database.buyer_balances.BTC.available, 'BTC')
    + assetAtoms(database.buyer_balances.BTC.held, 'BTC')
    + assetAtoms(database.seller_balances.BTC.available, 'BTC')
    + assetAtoms(database.seller_balances.BTC.held, 'BTC')
    + assetAtoms(database.platform_fees.BTC, 'BTC')
  const usdt = assetAtoms(database.buyer_balances.USDT.available, 'USDT')
    + assetAtoms(database.buyer_balances.USDT.held, 'USDT')
    + assetAtoms(database.seller_balances.USDT.available, 'USDT')
    + assetAtoms(database.seller_balances.USDT.held, 'USDT')
    + assetAtoms(database.platform_fees.USDT, 'USDT')
  expect(btc).toBe(assetAtoms('0.03', 'BTC'))
  expect(usdt).toBe(assetAtoms('3000', 'USDT'))
}

async function readManifest(): Promise<Manifest> {
  expect(manifestPath).not.toBe('')
  const metadata = await stat(manifestPath)
  expect(metadata.mode & 0o777, 'manifest is owner-readable only').toBe(0o600)
  const value = JSON.parse(await readFile(manifestPath, 'utf8')) as Manifest
  expect(value).toMatchObject({
    schema_version: 'qiu.full-stack.manifest.v1', api_origin: apiOrigin,
    coordinator_pid: expect.any(Number), fixture_pid: expect.any(Number), vue_pid: expect.any(Number),
    postgres: { pid: expect.any(Number), version: expect.any(String), authority: 'isolated_ephemeral_postgresql' },
    backend: { generation: 'A', pid: expect.any(Number) },
  })
  for (const field of ['ready_url', 'control_url', 'state_url', 'evidence_url'] as const) {
    expect(new URL(value[field]).origin, field).toBe(apiOrigin)
  }
  const pids = [value.postgres.pid, value.coordinator_pid, value.fixture_pid, value.vue_pid, value.backend.pid]
  expect(pids.every((pid) => Number.isSafeInteger(pid) && pid > 0)).toBe(true)
  expect(new Set(pids).size, 'PG/coordinator/fixture/Vue/backend A are different processes').toBe(pids.length)
  return value
}

async function timedJSON<T>(request: APIRequestContext, url: string): Promise<T> {
  const started = performance.now()
  const response = await request.get(url)
  expect(response.status(), url).toBe(200)
  expect(performance.now() - started, `${url} responds within 2 seconds`).toBeLessThan(2_000)
  return response.json() as Promise<T>
}

async function control(
  request: APIRequestContext, manifest: Manifest, body: Record<string, string>,
): Promise<ControlResponse> {
  const started = performance.now()
  const response = await request.post(manifest.control_url, { data: body })
  expect(response.status()).toBe(200)
  expect(performance.now() - started, `${body.action} responds within 2 seconds`).toBeLessThan(2_000)
  const value = await response.json() as ControlResponse
  expect(value).toMatchObject({ schema_version: 'qiu.full-stack.control.v1', action: body.action })
  return value
}

async function browserJSON<T>(page: Page, path: string): Promise<BrowserResult<T>> {
  return page.evaluate(async (requestPath) => {
    const response = await fetch(requestPath, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
    return { status: response.status, body: await response.json() as T }
  }, path)
}

async function browserWriteJSON<T>(page: Page, path: string, body: Record<string, string>): Promise<BrowserResult<T>> {
  return page.evaluate(async ({ requestPath, requestBody }) => {
    const prefix = `${encodeURIComponent('s78_trading_csrf')}=`
    const token = document.cookie.split(';').map((item) => item.trim())
      .find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? ''
    const response = await fetch(requestPath, {
      method: 'POST', credentials: 'same-origin',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-CSRF-Token': decodeURIComponent(token) },
      body: JSON.stringify(requestBody),
    })
    return { status: response.status, body: await response.json() as T }
  }, { requestPath: path, requestBody: body })
}

async function expectNoDocumentOverflow(page: Page, label: string): Promise<void> {
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), label).toBeLessThanOrEqual(0)
}

async function expectTouchTarget(locator: Locator, label: string): Promise<void> {
  const box = await locator.boundingBox()
  expect(box, `${label} is visible`).not.toBeNull()
  expect(box!.width, `${label} width`).toBeGreaterThanOrEqual(44)
  expect(box!.height, `${label} height`).toBeGreaterThanOrEqual(44)
}

async function expectVisibleFocus(locator: Locator, label: string): Promise<void> {
  await locator.focus()
  const style = await locator.evaluate((element) => {
    const value = getComputedStyle(element)
    return { style: value.outlineStyle, width: Number.parseFloat(value.outlineWidth) }
  })
  expect(style.style, `${label} focus style`).not.toBe('none')
  expect(style.width, `${label} focus width`).toBeGreaterThanOrEqual(2)
}

async function setScenario(
  page: Page, request: APIRequestContext, manifest: Manifest, action: string, scenario: string,
): Promise<ControlResponse> {
  const result = await control(request, manifest, { action, scenario })
  expect(result.scenario).toBe(scenario)
  if (result.wait_milliseconds) await page.waitForTimeout(result.wait_milliseconds + 25)
  await navigateApp(page, 'Trade', '/trade/BTC-USDT')
  await navigateApp(page, 'Insights', '/insights')
  return result
}

async function navigateApp(
  page: Page, label: 'Trade' | 'Insights', path: '/trade/BTC-USDT' | '/insights',
): Promise<void> {
  let scope: Page | Locator = page
  if ((page.viewportSize()?.width ?? 1440) < 768) {
    await page.getByRole('button', { name: 'Open navigation' }).click()
    scope = page.getByRole('dialog', { name: 'Open navigation' })
  }
  await scope.getByRole('link', { name: label, exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`${path}$`, 'u'))
  await expect(page.locator('main')).toHaveCount(1)
  if (path === '/trade/BTC-USDT') {
    await expect(page.locator('.trade-page')).toBeVisible()
    await expect(page.getByText('Identity bound')).toBeVisible()
  }
}

async function expectTradingReady(page: Page): Promise<void> {
  const status = await browserJSON<{ state: string; sequence: string }>(page, '/api/v1/trading/markets/BTC-USDT/status')
  expect(status.status).toBe(200)
  expect(status.body).toMatchObject({ state: 'ready', sequence: '7' })
}

function installBrowserAudit(page: Page): {
  consoleMessages: string[]; resourceConsoleMessages: string[]; expectedResourceConsoleMessages: string[];
  pageErrors: string[]; failedResponses: string[]; preAuthenticationResponses: string[]; failedRequests: string[];
  recoveryStatusRequests: string[]; apiDurations: number[]; origins: Set<string>; failuresEnabled: boolean;
} {
  const audit = {
    consoleMessages: [] as string[], resourceConsoleMessages: [] as string[], expectedResourceConsoleMessages: [] as string[],
    pageErrors: [] as string[], failedResponses: [] as string[], preAuthenticationResponses: [] as string[],
    failedRequests: [] as string[], recoveryStatusRequests: [] as string[], apiDurations: [] as number[],
    origins: new Set<string>(), failuresEnabled: false,
  }
  const starts = new Map<Request, number>()
  const beforeAuthentication = new WeakSet<Request>()
  page.on('console', (message) => {
    const rendered = `${message.type()}: ${message.text()}`
    if (rendered === resourceConsoleMessage(401) || rendered === resourceConsoleMessage(404)) {
      audit.resourceConsoleMessages.push(rendered)
    } else audit.consoleMessages.push(rendered)
  })
  page.on('pageerror', (error) => audit.pageErrors.push(error.message))
  page.on('request', (value) => {
    if (!audit.failuresEnabled) beforeAuthentication.add(value)
    const url = new URL(value.url())
    if (url.pathname === '/api/v1/trading/recovery/status') {
      audit.recoveryStatusRequests.push(`${value.method()} ${url.pathname}`)
    }
    if (url.protocol === 'http:' || url.protocol === 'https:' || url.protocol === 'ws:' || url.protocol === 'wss:') audit.origins.add(url.origin)
    if (url.pathname.startsWith('/api/')) starts.set(value, performance.now())
  })
  page.on('requestfailed', (value) => audit.failedRequests.push(`${value.method()} ${value.url()} ${value.failure()?.errorText ?? ''}`))
  page.on('response', (response) => {
    const request = response.request()
    const url = new URL(response.url())
    if (response.status() >= 400) {
      const failure = `${request.method()} ${url.pathname} ${response.status()}`
      const expectedAnonymousRead = beforeAuthentication.has(request) && request.method() === 'GET'
        && url.pathname === '/api/v1/trading/session' && response.status() === 401
      if (expectedAnonymousRead) {
        audit.preAuthenticationResponses.push(failure)
        audit.expectedResourceConsoleMessages.push(resourceConsoleMessage(response.status() as 401 | 404))
      } else audit.failedResponses.push(failure)
    }
    const started = starts.get(request)
    if (started !== undefined) audit.apiDurations.push(performance.now() - started)
  })
  return audit
}

function resourceConsoleMessage(status: 401 | 404): string {
  const reason = status === 401 ? 'Unauthorized' : 'Not Found'
  return `error: Failed to load resource: the server responded with a status of ${status} (${reason})`
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.clear()
    window.localStorage.setItem('qiu-market.locale', 'en')
  })
})

test('one real browser preserves trading, PostgreSQL recovery, research, and quality truth end to end', async ({ page, request }) => {
  const storyStarted = performance.now()
  const audit = installBrowserAudit(page)
  const manifest = await readManifest()
  const ready = await timedJSON<Record<string, unknown>>(request, manifest.ready_url)
  expect(ready).toMatchObject({
    schema_version: 'qiu.full-stack.ready.v1', ready: true, phase: 'ready_a', generation: 'A',
    api_origin: apiOrigin, coordinator_pid: manifest.coordinator_pid,
    postgres: { pid: manifest.postgres.pid, version: manifest.postgres.version },
    fixture_pid: manifest.fixture_pid, vue_pid: manifest.vue_pid,
    backend: { generation: 'A', pid: manifest.backend.pid },
  })
  const referenceBefore = await timedJSON<ReferenceFact>(request, `${apiOrigin}/__full-stack/reference`)
  expect(referenceBefore).toMatchObject({
    source: 'full-stack deterministic market-data fixture', market_id: 'BTC-USDT', price: '60000',
    observed_at: expect.any(String), hash: expect.stringMatching(/^[0-9a-f]{64}$/u),
  })

  const tradeStarted = performance.now()
  await page.goto('/trade/BTC-USDT')
  await expect(page.locator('main')).toHaveCount(1)
  await expectNoDocumentOverflow(page, 'desktop trade has no page overflow')
  const login = page.getByRole('button', { name: 'Local sign in' })
  await expectTouchTarget(login, 'local sign in')
  await expectVisibleFocus(login, 'local sign in')
  await login.press('Enter')
  await expect(page.getByText('Identity bound')).toBeVisible()
  audit.failuresEnabled = true
  await expect(page.locator('.market-header')).toContainText('60,000.00 USDT')
  await expect(page.locator('.market-header')).toContainText('Fresh')
  await expect(page.locator('.chart-panel')).toContainText('Real venue candles · binance')
  expect(performance.now() - tradeStarted, 'critical trade UI is ready within 5 seconds').toBeLessThan(5_000)

  let balances = await browserJSON<{ balances: Balance[] }>(page, '/api/v1/trading/balances')
  expect(balances.status).toBe(200)
  expectBalance(balances.body.balances, 'USDT', { available: '3000', held: '0' })

  await page.getByLabel('Client Order ID').fill(fullClientOrderID)
  await page.getByLabel('Price · USDT').fill('60000')
  await page.getByLabel('Quantity · BTC').fill('0.01')
  await page.getByLabel('Post Only').check()
  const submit = page.getByRole('button', { name: 'Buy BTC' })
  await expect(submit).toBeEnabled()
  await expectTouchTarget(submit, 'buy order')
  const fullResponse = page.waitForResponse((value) => value.request().method() === 'POST' && new URL(value.url()).pathname === '/api/v1/trading/orders')
  await submit.focus()
  await submit.press('Enter')
  const fullOpen = await (await fullResponse).json() as CommandResult
  expect(fullOpen).toMatchObject({ status: 'open' })
  const full = await control(request, manifest, { action: 'full_fill', client_order_id: fullClientOrderID })
  expect(full).toMatchObject({ phase: 'full_filled', generation: 'A', sequence: 4, order: { client_order_id: fullClientOrderID, status: 'filled', filled_quantity: '0.01', remaining_quantity: '0', held_amount: '0' } })
  await page.getByRole('button', { name: 'Refresh' }).click()
  await page.getByRole('button', { name: 'Order history', exact: true }).click()
  await expect(page.getByText('Filled', { exact: true })).toBeVisible()
  balances = await browserJSON<{ balances: Balance[] }>(page, '/api/v1/trading/balances')
  expectBalance(balances.body.balances, 'USDT', { available: '2400', held: '0' })
  expectBalance(balances.body.balances, 'BTC', { available: '0.00999', held: '0' })
  let state = await timedJSON<FullStackState>(request, manifest.state_url)
  expect(state).toMatchObject({
    phase: 'full_filled', generation: 'A', backend_pid: manifest.backend.pid,
    database: { sequence: 4, counts: { facts: 4, trades: 1, ledger_transactions: 5, ledger_entries: 14, orders: 2 },
      buyer_balances: { USDT: { available: '2400', held: '0' }, BTC: { available: '0.00999', held: '0' } },
      platform_fees: { BTC: '0.00001', USDT: '1.2' }, journal_sums: { BTC: '0', USDT: '0' }, duplicate_transactions: false, reference_mismatch: false },
  })

  await page.getByLabel('Client Order ID').fill(partialClientOrderID)
  await page.getByLabel('Quantity · BTC').fill('0.02')
  await page.getByRole('button', { name: 'Open orders', exact: true }).click()
  const partialResponse = page.waitForResponse((value) => value.request().method() === 'POST' && new URL(value.url()).pathname === '/api/v1/trading/orders')
  await page.getByRole('button', { name: 'Buy BTC' }).click()
  expect(await (await partialResponse).json()).toMatchObject({ status: 'open' })
  state = await timedJSON<FullStackState>(request, manifest.state_url)
  expect(state.database).toMatchObject({ sequence: 5, counts: { facts: 5, trades: 1, orders: 3 }, buyer_balances: { USDT: { available: '1200', held: '1200' }, BTC: { available: '0.00999', held: '0' } } })

  const partial = await control(request, manifest, { action: 'partial_fill', client_order_id: partialClientOrderID })
  expect(partial).toMatchObject({ phase: 'partial_filled', generation: 'A', sequence: 6, order: { client_order_id: partialClientOrderID, status: 'partially_filled', filled_quantity: '0.01', remaining_quantity: '0.01', held_amount: '600' } })
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByText('Partially filled', { exact: true })).toBeVisible()
  state = await timedJSON<FullStackState>(request, manifest.state_url)
  expect(state.database).toMatchObject({ sequence: 6, counts: { facts: 6, trades: 2, orders: 4 }, buyer_balances: { USDT: { available: '1200', held: '600' }, BTC: { available: '0.01998', held: '0' } }, platform_fees: { BTC: '0.00002', USDT: '2.4' } })
  const beforeRestartHash = partial.state_hash
  const beforeDatabaseEventHash = state.database.event_hash
  const beforeSnapshotHash = state.database.snapshot_hash

  const restart = await control(request, manifest, { action: 'restart_backend' })
  expect(restart).toMatchObject({
    phase: 'restored_b', generation: 'B', sequence: 6, restored: true,
    previous_backend: { generation: 'A', pid: manifest.backend.pid, exited: true, sequence: 6, state_hash: beforeRestartHash },
    current_backend: { generation: 'B', pid: expect.any(Number), sequence: 6, state_hash: beforeRestartHash },
  })
  expect(restart.current_backend?.pid).not.toBe(manifest.backend.pid)
  state = await timedJSON<FullStackState>(request, manifest.state_url)
  expect(state).toMatchObject({ phase: 'restored_b', generation: 'B', backend_pid: restart.current_backend?.pid, database: { sequence: 6, event_hash: beforeDatabaseEventHash, snapshot_hash: beforeSnapshotHash, buyer_balances: { USDT: { available: '1200', held: '600' }, BTC: { available: '0.01998', held: '0' } } } })

  await page.getByRole('button', { name: 'Refresh' }).click()
  const cancelRequest = page.waitForRequest((value) => value.method() === 'POST' && new URL(value.url()).pathname === `/api/v1/trading/orders/${partial.order?.order_id}/cancel`)
  const cancelResponse = page.waitForResponse((value) => value.request().method() === 'POST' && new URL(value.url()).pathname.endsWith('/cancel'))
  const cancelButton = page.getByRole('button', { name: 'Cancel', exact: true })
  await expectTouchTarget(cancelButton, 'cancel remainder')
  await cancelButton.click()
  const cancelBody = (await cancelRequest).postDataJSON() as { request_id: string }
  const canceled = await (await cancelResponse).json() as CommandResult
  expect(canceled).toMatchObject({ status: 'canceled', sequence: '7' })
  const replay = await browserWriteJSON<CommandResult>(page, `/api/v1/trading/orders/${partial.order?.order_id}/cancel`, cancelBody)
  expect(replay.status).toBe(200)
  expect(replay.body).toEqual(canceled)
  await page.getByRole('button', { name: 'Refresh' }).click()
  await page.getByRole('button', { name: 'Order history', exact: true }).click()
  await expect(page.getByText('Canceled', { exact: true })).toBeVisible()

  balances = await browserJSON<{ balances: Balance[] }>(page, '/api/v1/trading/balances')
  expectBalance(balances.body.balances, 'USDT', { available: '1800', held: '0' })
  expectBalance(balances.body.balances, 'BTC', { available: '0.01998', held: '0' })
  const orders = await browserJSON<TradeV1OrderPage>(page, '/api/v1/trading/orders?scope=all&limit=50')
  expect(orders.body.orders).toHaveLength(2)
  expect(orders.body.orders.find((item) => item.client_order_id === partialClientOrderID)).toMatchObject({ status: 'canceled', filled_quantity: '0.01', remaining_quantity: '0.01', held_amount: '0' })
  const trades = await browserJSON<TradeV1AccountTradePage>(page, '/api/v1/trading/account/trades?limit=50')
  expect(trades.body.trades).toHaveLength(2)
  const ledger = await browserJSON<TradeV1LedgerPage>(page, '/api/v1/trading/ledger/entries?limit=50&asset=all&reason=all')
  expect(ledger.body.entries).toHaveLength(11)
  state = await timedJSON<FullStackState>(request, manifest.state_url)
  expect(state).toMatchObject({
    phase: 'canceled', generation: 'B', database: { sequence: 7, counts: { facts: 7, trades: 2, ledger_transactions: 9, ledger_entries: 26, orders: 4 },
      buyer_balances: { USDT: { available: '1800', held: '0' }, BTC: { available: '0.01998', held: '0' } },
      seller_balances: { USDT: { available: '1197.6', held: '0' }, BTC: { available: '0.01', held: '0' } },
      platform_fees: { BTC: '0.00002', USDT: '2.4' }, journal_sums: { BTC: '0', USDT: '0' }, duplicate_transactions: false, reference_mismatch: false },
  })
  expect(state.database.trades).toHaveLength(2)
  expect(state.database.trades.map((item) => item.sequence)).toEqual([4, 6])
  for (const trade of state.database.trades) {
    expect(trade).toMatchObject({ price: '60000', quantity: '0.01', quote_amount: '600', buyer_fee_asset: 'BTC', buyer_fee_amount: '0.00001', buyer_fee_rate_bps: 10, buyer_fee_role: 'maker', seller_fee_asset: 'USDT', seller_fee_amount: '1.2', seller_fee_rate_bps: 20, seller_fee_role: 'taker' })
    expect(trade.trade_id).not.toBe('')
    expect(trade.buyer_order_id).not.toBe('')
    expect(trade.seller_order_id).not.toBe('')
    expect(trade.maker_order_id).toBe(trade.buyer_order_id)
    expect(trade.taker_order_id).toBe(trade.seller_order_id)
  }
  expect(new Set(state.database.trades.map((item) => item.trade_id)).size).toBe(2)
  expect(new Set(state.database.trades.map((item) => item.buyer_order_id)).size).toBe(2)
  expect(new Set(state.database.trades.map((item) => item.seller_order_id)).size).toBe(2)
  expectLedgerConservation(state.database)
  const transactions = state.database.ledger_transactions
  expect(transactions.filter((item) => item.transaction_id.startsWith('fund:') && item.reference.startsWith('virtual-funding:'))).toHaveLength(2)
  const holds = transactions.filter((item) => item.transaction_id.startsWith('hold:') && item.reference.startsWith('order-hold:'))
  expect(holds).toHaveLength(4)
  expect(new Set(holds.map((item) => item.reference.slice('order-hold:'.length)))).toEqual(
    new Set(Object.values(state.database.orders).map((item) => item.order_id)),
  )
  const settlements = transactions.filter((item) => item.transaction_id.startsWith('trade:') && item.reference.startsWith('matched-trade:'))
  expect(settlements).toHaveLength(2)
  expect(new Set(settlements.map((item) => item.reference.slice('matched-trade:'.length)))).toEqual(
    new Set(state.database.trades.map((item) => item.trade_id)),
  )
  const releases = transactions.filter((item) => item.transaction_id.startsWith('cancel-release:') && item.reference.startsWith('order-cancel:'))
  expect(releases).toHaveLength(1)
  expect(releases[0]?.reference).toBe(`order-cancel:${partial.order?.order_id}`)
  expectFinalAssetConservation(state.database)
  const finalTradingState = state
  expect(performance.now() - tradeStarted, 'full fill plus partial/restart/cancel/replay finishes within 15 seconds').toBeLessThan(15_000)

  // Mobile readback uses the same recovered process and PostgreSQL facts.
  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expectNoDocumentOverflow(page, 'mobile recovered trade has no page overflow')
  await page.getByRole('button', { name: 'Order history', exact: true }).click()
  const details = page.locator('.order-details').first()
  await expectTouchTarget(details, 'keyboard order details')
  await expectVisibleFocus(details, 'keyboard order details')
  await details.press('Enter')
  const orderDrawer = page.getByRole('dialog', { name: 'Order details' })
  await expect(orderDrawer).toBeVisible()
  await expect(orderDrawer.getByRole('button', { name: 'Close' })).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  expect(await orderDrawer.evaluate((node) => node.contains(document.activeElement))).toBe(true)
  await page.keyboard.press('Escape')
  await expect(orderDrawer).toHaveCount(0)
  await expect(details).toBeFocused()

  const menu = page.getByRole('button', { name: 'Open navigation' })
  await expectTouchTarget(menu, 'mobile navigation')
  await menu.click()
  const navDialog = page.getByRole('dialog', { name: 'Open navigation' })
  await expect(navDialog).toBeVisible()
  await expect(page.locator('main')).toHaveAttribute('inert', '')
  await page.keyboard.press('Shift+Tab')
  expect(await navDialog.evaluate((node) => node.contains(document.activeElement))).toBe(true)
  await page.keyboard.press('Escape')
  await expect(navDialog).toHaveCount(0)
  await expect(menu).toBeFocused()

  await navigateApp(page, 'Insights', '/insights')
  for (const scenario of ['fresh', 'legacy', 'empty', 'degraded', 'stale']) {
    await setScenario(page, request, manifest, 'research_scenario', scenario)
    await expect(page.getByTestId('research-feed-status')).toContainText(new RegExp(scenario, 'i'))
    if (scenario === 'fresh') await expect(page.locator('[data-testid^="research-event-"]')).toHaveCount(1)
    else await expect(page.getByTestId(`research-state-${scenario}`)).toBeVisible()
    if (scenario === 'empty') {
      const verifiedEmpty = page.getByTestId('research-state-empty')
      await expect(verifiedEmpty).toContainText('No research events in this window')
      await expect(verifiedEmpty).toContainText('This is a verified empty window, not an unavailable source.')
    }
    if (scenario === 'degraded') await expect(page.getByTestId('research-state-degraded')).not.toContainText(/No verified events/u)
  }
  await setScenario(page, request, manifest, 'research_scenario', 'fresh')

  await setScenario(page, request, manifest, 'quality_window', 'healthy')
  await expect(page.getByTestId('data-quality-status')).toContainText('quarantined')
  await expect(page.getByTestId('quality-binance_spot')).toContainText('healthy')
  await expect(page.getByTestId('quality-coinglass_derivatives')).toContainText('quarantined')
  await expect(page.getByTestId('quality-xiuqiu_research')).toContainText('quarantined')
  for (const source of ['quality-binance_spot', 'quality-coinglass_derivatives', 'quality-xiuqiu_research']) {
    await expect(page.getByTestId(source).locator('dl > div').filter({ hasText: 'Trade eligible' }).first()).toContainText('No')
  }
  await expectTradingReady(page)
  const faultMatrix: Array<{
    scenario: string; source: string; sourceKind: string; typed: RegExp;
    normalizedKind: string; status: number; retryAfter: number;
  }> = [
    { scenario: 'binance_429', source: 'quality-binance_spot', sourceKind: 'binance_spot', typed: /rate limit=[1-9]\d*/i, normalizedKind: 'rate_limit', status: 429, retryAfter: 1 },
    { scenario: 'coinglass_5xx', source: 'quality-coinglass_derivatives', sourceKind: 'coinglass_derivatives', typed: /upstream 5xx=[1-9]\d*/i, normalizedKind: 'upstream_5xx', status: 502, retryAfter: 0 },
    { scenario: 'timeout', source: 'quality-binance_spot', sourceKind: 'binance_spot', typed: /timeout=[1-9]\d*/i, normalizedKind: 'timeout', status: 0, retryAfter: 0 },
    { scenario: 'stale', source: 'quality-binance_spot', sourceKind: 'binance_spot', typed: /stale/i, normalizedKind: 'stale', status: 200, retryAfter: 0 },
    { scenario: 'future', source: 'quality-binance_spot', sourceKind: 'binance_spot', typed: /future/i, normalizedKind: 'future', status: 200, retryAfter: 0 },
    { scenario: 'conflict', source: 'quality-binance_spot', sourceKind: 'binance_spot', typed: /(?:conflict|identity|bad payload)/i, normalizedKind: 'conflict', status: 200, retryAfter: 0 },
  ]
  for (const fault of faultMatrix) {
    const controlled = await setScenario(page, request, manifest, 'quality_window', fault.scenario)
    expect(controlled.quality).toMatchObject({ scenario: fault.scenario, faults: expect.any(Array) })
    const typedFault = controlled.quality?.faults.find((value) => value.source === fault.sourceKind && value.normalized_error_kind === fault.normalizedKind)
    expect(typedFault, `${fault.scenario} returns typed quality evidence`).toBeDefined()
    expect(typedFault?.operation).not.toBe('')
    expect(typedFault?.http_status ?? 0).toBe(fault.status)
    expect(typedFault?.retry_after_seconds ?? 0).toBe(fault.retryAfter)
    await expect(page.getByTestId(fault.source)).toContainText(fault.typed)
    await expect(page.getByTestId('data-quality-status')).toContainText('quarantined')
    await expect(page.getByTestId('research-state-empty')).toHaveCount(0)
    await expectTradingReady(page)
  }

  // Cache/no-data facts are auditable but cannot advance a quarantined gate.
  await setScenario(page, request, manifest, 'quality_window', 'cache_hit')
  await expect(page.getByTestId('quality-binance_spot')).toContainText('0 / 3')
  await setScenario(page, request, manifest, 'quality_window', 'no_data')
  await expect(page.getByTestId('quality-binance_spot')).toContainText('0 / 3')

  // One healthy fact starts recovery; a new hard fault must reset the streak.
  await setScenario(page, request, manifest, 'quality_window', 'recover')
  await expect(page.getByTestId('quality-binance_spot')).toContainText('1 / 3')
  await setScenario(page, request, manifest, 'quality_window', 'conflict')
  await expect(page.getByTestId('quality-binance_spot')).toContainText('0 / 3')
  for (let window = 1; window <= 3; window++) {
    await setScenario(page, request, manifest, 'quality_window', 'recover')
    const expected = window < 3 ? 'recovering' : 'healthy'
    await expect(page.getByTestId('data-quality-status')).toContainText('quarantined')
    await expect(page.getByTestId('quality-binance_spot')).toContainText(expected)
    await expect(page.getByTestId('quality-binance_spot')).toContainText(`${window} / 3`)
  }
  await expectNoDocumentOverflow(page, 'mobile insights has no page overflow')
  await page.setViewportSize({ width: 1440, height: 1000 })
  await expectNoDocumentOverflow(page, 'desktop insights has no page overflow')

  const referenceAfter = await timedJSON<ReferenceFact>(request, `${apiOrigin}/__full-stack/reference`)
  expect(referenceAfter).toEqual(referenceBefore)

  expect(await page.locator('[tabindex]:not([tabindex="0"]):not([tabindex="-1"])').count(), 'no positive tabindex').toBe(0)
  const reducedMotion = await page.locator('.sidebar').evaluate((element) => {
    const style = getComputedStyle(element)
    return { transition: style.transitionDuration, animation: style.animationDuration }
  })
  expect(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(true)
  expect(reducedMotion.transition.split(',').every((value) => Number.parseFloat(value) <= 0.001)).toBe(true)
  expect(reducedMotion.animation.split(',').every((value) => Number.parseFloat(value) <= 0.001)).toBe(true)
  const assetBytes = await page.evaluate(() => {
    const unique = new Map<string, number>()
    for (const entry of performance.getEntriesByType('resource') as PerformanceResourceTiming[]) {
      if (!['script', 'link'].includes(entry.initiatorType)) continue
      unique.set(entry.name, Math.max(unique.get(entry.name) ?? 0, entry.decodedBodySize))
    }
    return [...unique.values()].reduce((sum, value) => sum + value, 0)
  })
  expect(assetBytes, 'same-origin JS+CSS decoded bytes').toBeLessThanOrEqual(2 * 1024 * 1024)

  const evidence = await timedJSON<FullStackEvidence>(request, manifest.evidence_url)
  expect(evidence).toMatchObject({
    schema_version: 'qiu.full-stack.evidence.v1', coordinator_pid: manifest.coordinator_pid,
    postgres: { pid: manifest.postgres.pid, version: manifest.postgres.version, authority: 'isolated_ephemeral_postgresql', snapshot_sequence: 4, head_sequence: 7, snapshot_matches_head: false, snapshot_matches_runtime: true },
    fixture_pid: manifest.fixture_pid, vue_pid: manifest.vue_pid, cleanup_armed: true,
    backend_a: { generation: 'A', pid: manifest.backend.pid, exited: true, sequence: 6 },
    backend_b: { generation: 'B', pid: restart.current_backend?.pid, sequence: 7 },
    restore: { same_sequence: true, same_state_hash: true },
    replay: {
      cancel_request_id: cancelBody.request_id, cancel_requests: 2, original_sequence: 7, replay_sequence: 7,
      original_status: 'canceled', replay_status: 'canceled', before_counts: finalTradingState.database.counts,
      after_counts: finalTradingState.database.counts, before_digest: finalTradingState.database.digest,
      after_digest: finalTradingState.database.digest, before_event_hash: finalTradingState.database.event_hash,
      after_event_hash: finalTradingState.database.event_hash, no_delta: true,
    },
    reference: { unchanged: true }, partial: { sequence: 6, snapshot_sequence: 4 }, final: finalTradingState.database,
  })
  expect(evidence.reference.before).toEqual(referenceBefore)
  expect(evidence.reference.after).toEqual(referenceAfter)
  expect(evidence.quality.map((window) => window.scenario)).toEqual([
    'healthy', 'healthy', 'healthy', 'healthy',
    'binance_429', 'coinglass_5xx', 'timeout', 'stale', 'future', 'conflict',
    'cache_hit', 'no_data', 'recover', 'conflict', 'recover', 'recover', 'recover',
  ])
  expect(evidence.backend_b.pid).not.toBe(evidence.backend_a.pid)
  const finalPIDs = [evidence.postgres.pid, evidence.coordinator_pid, evidence.fixture_pid, evidence.vue_pid, evidence.backend_a.pid, evidence.backend_b.pid]
  expect(finalPIDs.every((pid) => Number.isSafeInteger(pid) && pid > 0)).toBe(true)
  expect(new Set(finalPIDs).size, 'all six durable/browser harness processes are distinct').toBe(finalPIDs.length)
  for (const field of ['forbidden_writes', 'read_domain_trading_mutations', 'read_domain_reference_writes', 'read_domain_fund_writes', 'public_network_requests', 'fixture_non_get_requests']) {
    expect(evidence.spy[field], `spy ${field}`).toBe(0)
  }
  expect(evidence.spy.allowed_browser_trading_mutations).toBe(4)
  expect(evidence.spy.allowed_bootstrap_fund_writes).toBe(2)
  expect(evidence.spy.deterministic_fill_writes).toBe(2)

  expect(audit.origins, 'browser network is loopback-only').toEqual(new Set([frontendOrigin]))
  expect(audit.apiDurations.length).toBeGreaterThan(0)
  expect(Math.max(...audit.apiDurations), 'every browser API response is within 2 seconds').toBeLessThan(2_000)
  expect(audit.preAuthenticationResponses.toSorted(), 'exact anonymous read probes before login').toEqual([
    'GET /api/v1/trading/session 401',
  ])
  expect(audit.recoveryStatusRequests, 'disabled recovery capability prevents all status probes').toEqual([])
  expect(audit.failedResponses).toEqual([])
  expect(audit.failedRequests).toEqual([])
  expect(audit.resourceConsoleMessages.toSorted(), 'each generic Chrome resource error binds to an exact audited response')
    .toEqual(audit.expectedResourceConsoleMessages.toSorted())
  expect(audit.pageErrors).toEqual([])
  expect(audit.consoleMessages).toEqual([])
  expect(performance.now() - storyStarted).toBeLessThan(90_000)
})
