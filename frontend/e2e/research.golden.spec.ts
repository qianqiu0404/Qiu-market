import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

interface FixtureEvidence {
  schemaVersion: 'research-golden-evidence/v1'
  upstreamReads: number
  upstreamNonGet: number
  fixtureControlWrites: number
  scenario: string
  tradingMutations: number
}

const fixtureURL = 'http://127.0.0.1:19095'
const apiURL = 'http://127.0.0.1:19096'

async function setScenario(request: APIRequestContext, page: Page, scenario: string): Promise<void> {
  const response = await request.post(`${fixtureURL}/__fixture/control`, { data: { scenario } })
  expect(response.status()).toBe(200)
  const control = await response.json() as { scenario: string; waitMilliseconds: number }
  expect(control.scenario).toBe(scenario)
  await page.waitForTimeout(control.waitMilliseconds + 50)
  await page.reload()
  await expect(page.getByTestId('research-signal-feed')).toBeVisible()
}

async function expectNoOverflow(page: Page): Promise<void> {
  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    // Insights contains canvas charts which resize on the window event; wait
    // for two frames so the assertion measures the settled responsive layout.
    await page.evaluate(() => new Promise<void>((resolve) => {
      requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
    }))
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, `${viewport.width}px viewport has no horizontal overflow`).toBeLessThanOrEqual(0)
  }
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.clear()
    window.localStorage.setItem('qiu-market.locale', 'en')
  })
})

test('Vue renders the real read-only research handler and loopback upstream without trading writes', async ({ page, request }) => {
  const consoleMessages: string[] = []
  const pageErrors: string[] = []
  const researchResponses: Array<{ method: string; path: string; status: number }> = []
  const failedResponses: Array<{ method: string; path: string; status: number }> = []
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
    if (url.pathname.startsWith('/api/v1/research/signals/')) {
      researchResponses.push({
        method: response.request().method(),
        path: url.pathname,
        status: response.status(),
      })
    }
  })

  const before = await request.get(`${fixtureURL}/__fixture/evidence`)
  expect(before.status()).toBe(200)
  const beforeEvidence = await before.json() as FixtureEvidence
  expect(beforeEvidence).toMatchObject({
    schemaVersion: 'research-golden-evidence/v1',
    upstreamNonGet: 0,
    fixtureControlWrites: 0,
    tradingMutations: 0,
  })

  const health = await request.get(`${apiURL}/healthz`)
  expect(health.status()).toBe(200)
  await expect(health.json()).resolves.toMatchObject({
    status: 'ready',
    schemaVersion: 'research-golden/v1',
    tradingMutations: 0,
  })

  await page.goto('/insights')
  const feed = page.getByTestId('research-signal-feed')
  await expect(feed).toBeVisible()
  await expect(page.getByTestId('research-feed-status')).toContainText('Fresh')
  await expect(feed.getByText('Research information · Not executable')).toHaveCount(2)
  await expect(feed.getByText('BTC 确定性研究信号')).toBeVisible()
  await expect(feed.getByText('xiuqiu-site Market Radar')).toBeVisible()
  await expect(feed.getByText('xiuqiu-site · xiuqiu_automated_dynamic')).toBeVisible()
  await expect(feed.getByText('Research priority (not trading advice) P1')).toBeVisible()
  await expect(feed.getByText('等待下一条官方来源确认。')).toBeVisible()
  await expect(feed.getByText('官方来源撤回或更正。')).toBeVisible()
  await expect(feed.getByText('Event time')).toBeVisible()
  await expect(feed.getByText('Published')).toBeVisible()
  await expect(feed.getByText('Received')).toBeVisible()
  await expect(feed.getByText('Not supplied')).toBeVisible()

  await expect.poll(() => researchResponses.filter((item) =>
    item.method === 'GET' && item.path === '/api/v1/research/signals/summary' && item.status === 200).length,
  ).toBeGreaterThanOrEqual(1)
  await expect.poll(() => researchResponses.filter((item) =>
    item.method === 'GET' && item.path === '/api/v1/research/signals/events' && item.status === 200).length,
  ).toBeGreaterThanOrEqual(1)

  await expectNoOverflow(page)

  await setScenario(request, page, 'legacy')
  await expect(page.getByTestId('research-feed-status')).toContainText('Legacy')
  await expect(page.getByTestId('research-state-legacy')).toContainText('Legacy source semantics')
  await expect(feed.getByText('Research information · Not executable')).toHaveCount(2)
  await expect(feed.getByText('legacy_fields_missing')).toBeVisible()
  await expect(feed.getByText('Not supplied')).toHaveCount(3)
  await expectNoOverflow(page)

  await setScenario(request, page, 'empty')
  await expect(page.getByTestId('research-feed-status')).toContainText('Empty')
  await expect(page.getByTestId('research-state-empty')).toContainText('No research events in this window')
  await expect(page.getByTestId('research-state-empty')).toContainText('verified empty window')
  await expect(feed.getByText('BTC 确定性研究信号')).toHaveCount(0)
  await expectNoOverflow(page)

  await setScenario(request, page, 'degraded')
  await expect(page.getByTestId('research-feed-status')).toContainText('Degraded')
  await expect(page.getByTestId('research-state-degraded')).toContainText('Research source degraded')
  await expect(feed.getByText('No research events in this window')).toHaveCount(0)
  await expectNoOverflow(page)

  await setScenario(request, page, 'error')
  await expect(page.getByTestId('research-feed-status')).toContainText('Degraded')
  await expect(page.getByTestId('research-state-degraded')).toContainText('Research source degraded')
  await expect(feed.getByText('No research events in this window')).toHaveCount(0)
  await expectNoOverflow(page)

  await setScenario(request, page, 'success')
  await expect(page.getByTestId('research-feed-status')).toContainText('Fresh')
  await expect(feed.getByText('BTC 确定性研究信号')).toBeVisible()
  await expect(feed.getByText('Research information · Not executable')).toHaveCount(2)
  await expectNoOverflow(page)

  const after = await request.get(`${fixtureURL}/__fixture/evidence`)
  expect(after.status()).toBe(200)
  const afterEvidence = await after.json() as FixtureEvidence
  expect(afterEvidence.upstreamReads).toBeGreaterThan(beforeEvidence.upstreamReads)
  expect(afterEvidence).toMatchObject({
    upstreamNonGet: 0,
    fixtureControlWrites: 5,
    scenario: 'success',
    tradingMutations: 0,
  })
  expect(tradingMutations).toEqual([])
  expect(failedResponses).toEqual([])
  expect(pageErrors).toEqual([])
  expect(consoleMessages).toEqual([])
})
