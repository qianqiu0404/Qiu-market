import { expect, test, type Page } from '@playwright/test'
import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'

interface FixtureEvidence {
  schemaVersion: 'quality-golden-evidence/v1'
  qualityReads: number
  legacyReadRequests: number
  tradingMutations: number
  providerNetworkRequests: number
  databaseWrites: number
}

interface Counter { numerator: number; denominator: number; bps: number | null }
interface Capability {
  capability: string
  maxAgeSeconds: number
  sampleCount: number
  validSampleCount: number
  minSamples: number
  successCount: number
  ageSeconds: number | null
  coverage: Counter
  status: string
  reasons: string[]
}
interface QualityItem {
  source: string
  sourceName: string
  class: string
  windowSeconds: number
  sampleCount: number
  minSamples: number
  attemptCount: number
  successCount: number
  ageSeconds: number | null
  coverage: Counter
  technicalScoreBps: number | null
  grade: string | null
  status: string
  reasons: string[]
  license: string
  publicEligible: boolean
  tradeEligible: boolean
  readOnlyUse: string
  capabilities: Capability[]
  dimensions: Array<Counter & { metric: string; polarity: string }>
  errorCounts: Record<string, number>
  cacheHitCount: number
  staleServeCount: number
  priorityCounts: { p0: number; p1: number; p2: number }
  gate: { status: string; healthyWindowStreak: number; recoveryRequired: number; reasons: string[] }
}
interface QualitySummary {
  schemaVersion: 'data-quality/v1'
  status: string
  generatedAt: string
  items: QualityItem[]
  error: string | null
}

const apiURL = 'http://127.0.0.1:19097'
const primaryMetrics = ['freshness', 'availability', 'completeness', 'schema', 'consistency', 'coverage']

function item(summary: QualitySummary, source: string): QualityItem {
  const result = summary.items.find((entry) => entry.source === source)
  expect(result, `quality source ${source} exists`).toBeDefined()
  return result!
}

function capability(source: QualityItem, name: string): Capability {
  const result = source.capabilities.find((entry) => entry.capability === name)
  expect(result, `${source.source} capability ${name} exists`).toBeDefined()
  return result!
}

function expectPerfectDimensions(source: QualityItem, expectedCoverage: Counter): void {
  const dimensions = Object.fromEntries(source.dimensions.map((entry) => [entry.metric, entry]))
  for (const metric of primaryMetrics) {
    const expected = metric === 'coverage'
      ? expectedCoverage
      : { numerator: source.successCount, denominator: source.successCount, bps: 10_000 }
    expect(dimensions[metric], `${source.source} ${metric}`).toMatchObject(expected)
  }
}

async function expectNoOverflow(page: Page): Promise<void> {
  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    await page.evaluate(() => new Promise<void>((resolve) => {
      requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
    }))
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, `${viewport.width}px viewport has no document overflow`).toBeLessThanOrEqual(0)
    const cards = page.locator('[data-testid^="quality-"]')
    for (let index = 0; index < await cards.count(); index++) {
      const cardOverflow = await cards.nth(index).evaluate((element) => element.scrollWidth - element.clientWidth)
      expect(cardOverflow, `${viewport.width}px quality card ${index} has no overflow`).toBeLessThanOrEqual(0)
    }
  }
}

async function sourceFiles(root: string): Promise<string[]> {
  const result: string[] = []
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const absolute = path.join(root, entry.name)
    if (entry.isDirectory()) result.push(...await sourceFiles(absolute))
    else if (/\.(?:ts|vue)$/u.test(entry.name)) result.push(absolute)
  }
  return result
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.clear()
    window.localStorage.setItem('qiu-market.locale', 'en')
  })
})

test('quality UI and trading frontend remain bidirectionally isolated', async () => {
  const frontendRoot = path.resolve(process.cwd(), 'src')
  const qualityFiles = [
    path.join(frontendRoot, 'api/dataQuality.ts'),
    path.join(frontendRoot, 'features/insights/DataQualityPanel.vue'),
  ]
  const tradingFiles = [
    path.join(frontendRoot, 'api/trading.ts'),
    path.join(frontendRoot, 'api/trade-v1-contract.ts'),
    path.join(frontendRoot, 'views/Trade.vue'),
    path.join(frontendRoot, 'features/system/TradingAdminCard.vue'),
    ...await sourceFiles(path.join(frontendRoot, 'features/trade')),
    ...await sourceFiles(path.join(frontendRoot, 'trading')),
  ]

  for (const file of qualityFiles) {
    const body = await readFile(file, 'utf8')
    expect(body, `${path.basename(file)} has no trading API or write-chain dependency`)
      .not.toMatch(/(?:api|features)?\/trading|\/api\/v1\/trading|reference|exchange|ledger/iu)
  }
  for (const file of tradingFiles) {
    const body = await readFile(file, 'utf8')
    expect(body, `${path.relative(frontendRoot, file)} has no quality dependency`)
      .not.toMatch(/dataQuality|DataQualityPanel|\/data-quality\//u)
  }
})

test('Vue renders exact independent quality evidence without provider, database, or trading writes', async ({ page, request }) => {
  const consoleMessages: string[] = []
  const pageErrors: string[] = []
  const failedResponses: Array<{ method: string; path: string; status: number }> = []
  const qualityResponses: Array<{ method: string; path: string; status: number }> = []
  const tradingMutations: Array<{ method: string; path: string }> = []

  page.on('console', (message) => {
    const location = message.location()
    consoleMessages.push(`${message.type()}: ${message.text()} @ ${location.url || '<unknown>'}:${location.lineNumber ?? 0}`)
  })
  page.on('pageerror', (error) => pageErrors.push(error.message))
  page.on('request', (browserRequest) => {
    const url = new URL(browserRequest.url())
    if (url.pathname.startsWith('/api/v1/trading/') && browserRequest.method() !== 'GET') {
      tradingMutations.push({ method: browserRequest.method(), path: url.pathname })
    }
  })
  page.on('response', (response) => {
    const url = new URL(response.url())
    if (response.status() >= 400) {
      failedResponses.push({ method: response.request().method(), path: url.pathname, status: response.status() })
    }
    if (url.pathname === '/api/v1/data-quality/summary') {
      qualityResponses.push({ method: response.request().method(), path: url.pathname, status: response.status() })
    }
  })

  const beforeResponse = await request.get(`${apiURL}/__fixture/evidence`)
  expect(beforeResponse.status()).toBe(200)
  const before = await beforeResponse.json() as FixtureEvidence
  expect(before).toEqual({
    schemaVersion: 'quality-golden-evidence/v1', qualityReads: 0, legacyReadRequests: 0,
    tradingMutations: 0, providerNetworkRequests: 0, databaseWrites: 0,
  })

  const health = await request.get(`${apiURL}/healthz`)
  expect(health.status()).toBe(200)
  await expect(health.json()).resolves.toEqual({ status: 'ready', schemaVersion: 'quality-golden/v1', tradingMutations: 0 })

  const apiResponse = await request.get(`${apiURL}/api/v1/data-quality/summary`)
  expect(apiResponse.status()).toBe(200)
  expect(apiResponse.headers()['content-type']).toContain('application/json')
  const summary = await apiResponse.json() as QualitySummary
  expect(summary.schemaVersion).toBe('data-quality/v1')
  expect(summary.status).toBe('quarantined')
  expect(summary.error).toBeNull()
  expect(Number.isNaN(Date.parse(summary.generatedAt))).toBe(false)
  expect(summary.items.map((entry) => entry.source)).toEqual([
    'binance_spot', 'coinglass_derivatives', 'xiuqiu_research',
  ])

  const binance = item(summary, 'binance_spot')
  expect(binance).toMatchObject({
    sourceName: 'Binance Public', class: 'spot', windowSeconds: 300,
    sampleCount: 7, minSamples: 7, attemptCount: 7, successCount: 7, ageSeconds: 1,
    coverage: { numerator: 2, denominator: 2, bps: 10_000 }, technicalScoreBps: 10_000,
    grade: 'A', status: 'healthy', reasons: ['no_new_evidence'], license: 'approved',
    publicEligible: true, tradeEligible: false, readOnlyUse: 'market_context',
    errorCounts: {}, cacheHitCount: 0, staleServeCount: 0, priorityCounts: { p0: 0, p1: 0, p2: 0 },
    gate: { status: 'healthy', healthyWindowStreak: 0, recoveryRequired: 3, reasons: [] },
  })
  expect(capability(binance, 'spot_ticker')).toMatchObject({
    maxAgeSeconds: 5, sampleCount: 5, validSampleCount: 5, minSamples: 5, successCount: 5,
    ageSeconds: 1, coverage: { numerator: 5, denominator: 5, bps: 10_000 }, status: 'healthy', reasons: [],
  })
  expect(capability(binance, 'ohlcv')).toMatchObject({
    maxAgeSeconds: 65, sampleCount: 2, validSampleCount: 2, minSamples: 2, successCount: 2,
    ageSeconds: 1, coverage: { numerator: 2, denominator: 2, bps: 10_000 }, status: 'healthy', reasons: [],
  })
  expectPerfectDimensions(binance, { numerator: 2, denominator: 2, bps: 10_000 })

  const coinglass = item(summary, 'coinglass_derivatives')
  expect(coinglass).toMatchObject({
    sourceName: 'CoinGlass', class: 'derivatives', windowSeconds: 18_000,
    sampleCount: 2, minSamples: 2, attemptCount: 2, successCount: 2, ageSeconds: 3_600,
    coverage: { numerator: 2, denominator: 2, bps: 10_000 }, technicalScoreBps: 10_000,
    grade: 'A', status: 'quarantined', reasons: ['license_restricted', 'not_live'], license: 'restricted',
    publicEligible: false, tradeEligible: false, readOnlyUse: 'derivatives_context',
    errorCounts: {}, cacheHitCount: 0, staleServeCount: 0, priorityCounts: { p0: 0, p1: 0, p2: 0 },
    gate: { status: 'quarantined', healthyWindowStreak: 0, recoveryRequired: 3, reasons: ['license_restricted', 'not_live'] },
  })
  for (const name of ['open_interest', 'liquidation']) {
    expect(capability(coinglass, name)).toMatchObject({
      maxAgeSeconds: 18_000, sampleCount: 1, validSampleCount: 1, minSamples: 1, successCount: 1,
      ageSeconds: 3_600, coverage: { numerator: 1, denominator: 1, bps: 10_000 }, status: 'healthy', reasons: [],
    })
  }
  expectPerfectDimensions(coinglass, { numerator: 2, denominator: 2, bps: 10_000 })

  const research = item(summary, 'xiuqiu_research')
  expect(research).toMatchObject({
    sourceName: 'xiuqiu-site Market Radar', class: 'research', windowSeconds: 604_800,
    sampleCount: 2, minSamples: 2, attemptCount: 2, successCount: 2, ageSeconds: 1,
    coverage: { numerator: 6, denominator: 6, bps: 10_000 }, technicalScoreBps: 10_000,
    grade: 'A', status: 'quarantined', reasons: ['license_unknown'], license: 'unknown',
    publicEligible: false, tradeEligible: false, readOnlyUse: 'research_context',
    errorCounts: {}, cacheHitCount: 0, staleServeCount: 0, priorityCounts: { p0: 0, p1: 1, p2: 0 },
    gate: { status: 'quarantined', healthyWindowStreak: 0, recoveryRequired: 3, reasons: ['license_unknown'] },
  })
  expect(capability(research, 'research_summary')).toMatchObject({
    maxAgeSeconds: 604_800, sampleCount: 1, validSampleCount: 1, minSamples: 1, successCount: 1,
    ageSeconds: 2, coverage: { numerator: 1, denominator: 1, bps: 10_000 }, status: 'healthy', reasons: [],
  })
  expect(capability(research, 'research_events')).toMatchObject({
    maxAgeSeconds: 604_800, sampleCount: 1, validSampleCount: 1, minSamples: 1, successCount: 1,
    ageSeconds: 1, coverage: { numerator: 1, denominator: 1, bps: 10_000 }, status: 'healthy', reasons: [],
  })
  expectPerfectDimensions(research, { numerator: 6, denominator: 6, bps: 10_000 })
  const researchDimensions = Object.fromEntries(research.dimensions.map((entry) => [entry.metric, entry]))
  expect(researchDimensions.research_source).toMatchObject({ numerator: 2, denominator: 2, bps: 10_000 })
  expect(researchDimensions.research_watch).toMatchObject({ numerator: 1, denominator: 1, bps: 10_000 })
  expect(researchDimensions.research_invalidation).toMatchObject({ numerator: 1, denominator: 1, bps: 10_000 })
  expect(researchDimensions.research_priority).toMatchObject({ numerator: 1, denominator: 1, bps: 10_000 })

  await page.goto('/insights')
  const panel = page.getByTestId('data-quality-panel')
  await expect(panel).toBeVisible()
  await expect(page.getByTestId('data-quality-status')).toContainText('quarantined')
  await expect(panel.getByText('Read-only quality · not trading advice')).toHaveCount(4)

  const binanceCard = page.getByTestId('quality-binance_spot')
  await expect(binanceCard).toContainText('Binance Public')
  await expect(binanceCard).toContainText('7 / 7')
  await expect(binanceCard).toContainText('100.00 · A')
  await expect(binanceCard).toContainText('approved')
  await expect(binanceCard).toContainText('no_new_evidence')

  const coinglassCard = page.getByTestId('quality-coinglass_derivatives')
  await expect(coinglassCard).toContainText('CoinGlass')
  await expect(coinglassCard).toContainText('2 / 2')
  await expect(coinglassCard).toContainText('100.00 · A')
  await expect(coinglassCard).toContainText('restricted')
  await expect(coinglassCard).toContainText('license_restricted')
  await expect(coinglassCard).toContainText('not_live')

  const researchCard = page.getByTestId('quality-xiuqiu_research')
  await expect(researchCard).toContainText('xiuqiu-site Market Radar')
  await expect(researchCard).toContainText('2 / 2')
  await expect(researchCard).toContainText('100.00 · A')
  await expect(researchCard).toContainText('unknown')
  await expect(researchCard).toContainText('license_unknown')
  await expect(researchCard).toContainText('P0=0 · P1=1 · P2=0')

  for (const name of ['spot_ticker', 'ohlcv', 'open_interest', 'liquidation', 'research_summary', 'research_events']) {
    const cap = page.getByTestId(`quality-capability-${name}`)
    await expect(cap).toBeVisible()
    await expect(cap).toContainText('healthy')
    await expect(cap).toContainText('100.00%')
  }
  await expect(panel.getByText('Trade eligible')).toHaveCount(3)
  await expect(binanceCard.getByText('No', { exact: true })).toHaveCount(1)
  await expect(coinglassCard.getByText('No', { exact: true })).toHaveCount(2)
  await expect(researchCard.getByText('No', { exact: true })).toHaveCount(2)

  await expect.poll(() => qualityResponses.filter((response) =>
    response.method === 'GET' && response.path === '/api/v1/data-quality/summary' && response.status === 200).length,
  ).toBeGreaterThanOrEqual(1)
  await expectNoOverflow(page)

  const afterResponse = await request.get(`${apiURL}/__fixture/evidence`)
  expect(afterResponse.status()).toBe(200)
  const after = await afterResponse.json() as FixtureEvidence
  expect(after.qualityReads).toBeGreaterThan(before.qualityReads)
  expect(after.legacyReadRequests).toBeGreaterThan(before.legacyReadRequests)
  expect(after).toMatchObject({
    schemaVersion: 'quality-golden-evidence/v1', tradingMutations: 0,
    providerNetworkRequests: 0, databaseWrites: 0,
  })
  expect(tradingMutations).toEqual([])
  expect(failedResponses).toEqual([])
  expect(pageErrors).toEqual([])
  expect(consoleMessages).toEqual([])
})
