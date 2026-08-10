import { expect, test, type Locator, type Page, type Request } from '@playwright/test'
import { readFile, stat } from 'node:fs/promises'

interface Manifest {
  schema_version: 'qiu.full-stack.manifest.v1'
  api_origin: string
  ready_url: string
  evidence_url: string
  coordinator_pid: number
  fixture_pid: number
  vue_pid: number
  postgres: { pid: number; version: string; authority: string }
  backend: { generation: 'A'; pid: number }
}

interface Evidence {
  schema_version: 'qiu.full-stack.evidence.v1'
  spy: {
    read_domain_trading_mutations: number
    read_domain_reference_writes: number
    read_domain_fund_writes: number
    forbidden_writes: number
    public_network_requests: number
    fixture_non_get_requests: number
  }
}

interface BrowserAudit {
  consoleMessages: string[]
  pageErrors: string[]
  failedResponses: string[]
  preAuthenticationResponses: string[]
  expectedResourceConsoleMessages: string[]
  observedResourceConsoleMessages: string[]
  failedRequests: string[]
  tradingMutations: string[]
  apiDurations: number[]
  origins: Set<string>
  recoveryStatusRequests: number
  failuresEnabled: boolean
}

const frontendOrigin = requiredLoopbackOrigin('QIU_FULLSTACK_FRONTEND_ORIGIN')
const apiOrigin = requiredLoopbackOrigin('QIU_FULLSTACK_API_ORIGIN')
const manifestPath = process.env.QIU_FULLSTACK_MANIFEST?.trim() ?? ''

function requiredLoopbackOrigin(name: string): string {
  const raw = process.env[name]?.trim()
  if (!raw) throw new Error(`${name} is required`)
  const value = new URL(raw)
  if (
    value.protocol !== 'http:' || value.hostname !== '127.0.0.1' ||
    value.username || value.password || value.pathname !== '/' || value.search || value.hash
  ) {
    throw new Error(`${name} must be an exact credential-free loopback HTTP origin`)
  }
  return value.origin
}

async function readManifest(): Promise<Manifest> {
  expect(manifestPath).not.toBe('')
  const metadata = await stat(manifestPath)
  expect(metadata.isFile()).toBe(true)
  expect(metadata.mode & 0o777, 'manifest permissions').toBe(0o600)
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8')) as Manifest
  expect(manifest).toMatchObject({
    schema_version: 'qiu.full-stack.manifest.v1',
    api_origin: apiOrigin,
    coordinator_pid: expect.any(Number),
    fixture_pid: expect.any(Number),
    vue_pid: expect.any(Number),
    postgres: { pid: expect.any(Number), version: expect.any(String), authority: 'isolated_ephemeral_postgresql' },
    backend: { generation: 'A', pid: expect.any(Number) },
  })
  const pids = [manifest.postgres.pid, manifest.coordinator_pid, manifest.fixture_pid, manifest.vue_pid, manifest.backend.pid]
  expect(pids.every((pid) => Number.isSafeInteger(pid) && pid > 0), 'manifest process IDs').toBe(true)
  expect(new Set(pids).size, 'PostgreSQL/coordinator/fixture/Vue/backend A are distinct processes').toBe(pids.length)
  expect(new URL(manifest.ready_url).origin).toBe(apiOrigin)
  expect(new URL(manifest.evidence_url).origin).toBe(apiOrigin)
  return manifest
}

function installAudit(page: Page): BrowserAudit {
  const audit: BrowserAudit = {
    consoleMessages: [], pageErrors: [], failedResponses: [], preAuthenticationResponses: [],
    expectedResourceConsoleMessages: [], observedResourceConsoleMessages: [], failedRequests: [],
    tradingMutations: [], apiDurations: [], origins: new Set<string>(), recoveryStatusRequests: 0, failuresEnabled: false,
  }
  const starts = new Map<Request, number>()
  const beforeAuthentication = new WeakSet<Request>()
  page.on('console', (message) => {
    const rendered = `${message.type()}: ${message.text()}`
    if (rendered === resourceConsoleMessage()) {
      audit.observedResourceConsoleMessages.push(rendered)
    } else {
      audit.consoleMessages.push(rendered)
    }
  })
  page.on('pageerror', (error) => audit.pageErrors.push(error.message))
  page.on('request', (request) => {
    if (!audit.failuresEnabled) beforeAuthentication.add(request)
    const url = new URL(request.url())
    if (url.pathname === '/api/v1/trading/recovery/status') audit.recoveryStatusRequests++
    if (['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol)) audit.origins.add(url.origin)
    if (url.pathname.startsWith('/api/')) starts.set(request, performance.now())
    if (
      url.pathname.startsWith('/api/v1/trading/') && request.method() !== 'GET' &&
      url.pathname !== '/api/v1/trading/auth/local' &&
      url.pathname !== '/api/v1/trading/ws-ticket'
    ) {
      audit.tradingMutations.push(`${request.method()} ${url.pathname}`)
    }
  })
  page.on('requestfailed', (request) => {
    audit.failedRequests.push(`${request.method()} ${request.url()} ${request.failure()?.errorText ?? ''}`)
  })
  page.on('response', (response) => {
    const request = response.request()
    const url = new URL(response.url())
    if (response.status() >= 400) {
      const failure = `${request.method()} ${url.pathname} ${response.status()}`
      if (beforeAuthentication.has(request) && request.method() === 'GET' &&
        url.pathname === '/api/v1/trading/session' && response.status() === 401) {
        audit.preAuthenticationResponses.push(failure)
        audit.expectedResourceConsoleMessages.push(resourceConsoleMessage())
      } else {
        audit.failedResponses.push(failure)
      }
    }
    const started = starts.get(request)
    if (started !== undefined) audit.apiDurations.push(performance.now() - started)
  })
  return audit
}

function resourceConsoleMessage(): string {
  return 'error: Failed to load resource: the server responded with a status of 401 (Unauthorized)'
}

async function expectNoDocumentOverflow(page: Page, label: string): Promise<void> {
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  }))
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow, label).toBeLessThanOrEqual(0)
}

async function expectFocusRing(locator: Locator, label: string): Promise<void> {
  await locator.focus()
  const outline = await locator.evaluate((element) => {
    const style = getComputedStyle(element)
    return { style: style.outlineStyle, width: Number.parseFloat(style.outlineWidth) }
  })
  expect(outline.style, `${label} outline style`).not.toBe('none')
  expect(outline.width, `${label} outline width`).toBeGreaterThanOrEqual(2)
}

async function visibleTargetFailures(page: Page): Promise<string[]> {
  return page.locator('button, a[href], input:not([type="hidden"]), select, [role="button"]').evaluateAll((elements) => {
    const failures: string[] = []
    for (const element of elements) {
      const target = element instanceof HTMLInputElement && ['checkbox', 'radio'].includes(element.type)
        ? element.closest('label') ?? element
        : element
      const style = getComputedStyle(target)
      const box = target.getBoundingClientRect()
      if (
        style.display === 'none' || style.visibility === 'hidden' ||
        box.width === 0 || box.height === 0 || target.closest('[inert]')
      ) continue
      if (box.width + 0.01 < 44 || box.height + 0.01 < 44) {
        const name = element.getAttribute('aria-label') ?? element.textContent?.trim().slice(0, 60) ?? element.tagName
        failures.push(`${element.tagName.toLowerCase()}[${name}] ${box.width.toFixed(2)}x${box.height.toFixed(2)}`)
      }
    }
    return failures
  })
}

async function motionFailures(page: Page): Promise<string[]> {
  return page.locator('body *').evaluateAll((elements) => {
    const seconds = (value: string): number => value.split(',').reduce((maximum, item) => {
      const trimmed = item.trim()
      const parsed = Number.parseFloat(trimmed)
      if (!Number.isFinite(parsed)) return maximum
      return Math.max(maximum, trimmed.endsWith('ms') ? parsed / 1000 : parsed)
    }, 0)
    const failures: string[] = []
    for (const element of elements) {
      const style = getComputedStyle(element)
      const transition = seconds(style.transitionDuration)
      const animation = seconds(style.animationDuration)
      if (transition > 0.001 || animation > 0.001) {
        failures.push(`${element.tagName.toLowerCase()}.${element.className}: transition=${transition}s animation=${animation}s`)
      }
    }
    return failures.slice(0, 20)
  })
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.clear()
    window.localStorage.setItem('qiu-market.locale', 'en')
  })
})

test('the recovered full stack remains keyboard-safe, bounded, loopback-only, and read-only', async ({ page, request }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  expect(await page.evaluate(() => window.matchMedia('(prefers-reduced-motion: reduce)').matches),
    'browser reduced-motion media preference').toBe(true)
  const audit = installAudit(page)
  const manifest = await readManifest()
  const ready = await request.get(manifest.ready_url)
  expect(ready.status()).toBe(200)

  const started = performance.now()
  await page.goto('/trade/BTC-USDT')
  const login = page.getByRole('button', { name: 'Local sign in' })
  await expect(login).toBeVisible()
  await expectFocusRing(login, 'local sign in')
  await login.press('Enter')
  await expect(page.getByText('Identity bound')).toBeVisible()
  audit.failuresEnabled = true
  expect(performance.now() - started, 'critical trade UI ready').toBeLessThan(5_000)
  await expectNoDocumentOverflow(page, 'desktop trade document overflow')
  expect(await visibleTargetFailures(page), 'desktop trade 44px targets').toEqual([])
  expect(await page.locator('[tabindex]:not([tabindex="0"]):not([tabindex="-1"])').count(), 'positive tabindex').toBe(0)

  const side = page.getByRole('button', { name: 'Sell', exact: true })
  await expectFocusRing(side, 'sell switch')
  await side.press(' ')
  await expect(side).toHaveClass(/active/u)
  await page.getByRole('button', { name: 'Buy', exact: true }).press('Enter')

  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(page.getByText('Identity bound')).toBeVisible()
  await expectNoDocumentOverflow(page, 'mobile trade document overflow')
  const menu = page.getByRole('button', { name: 'Open navigation' })
  await expectFocusRing(menu, 'mobile navigation')
  await menu.press('Enter')
  const drawer = page.getByRole('dialog', { name: 'Open navigation' })
  await expect(drawer).toBeVisible()
  await expect(page.locator('main')).toHaveAttribute('inert', '')
  await expect(drawer.getByRole('button', { name: 'Close navigation' })).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  expect(await drawer.evaluate((node) => node.contains(document.activeElement)), 'focus is trapped').toBe(true)
  await page.keyboard.press('Escape')
  await expect(drawer).toHaveCount(0)
  await expect(menu).toBeFocused()
  expect(await visibleTargetFailures(page), 'mobile trade 44px targets').toEqual([])

  await page.goto('/insights')
  await expectNoDocumentOverflow(page, 'mobile insights document overflow')
  expect(await visibleTargetFailures(page), 'mobile insights 44px targets').toEqual([])
  await page.setViewportSize({ width: 1440, height: 1000 })
  await expectNoDocumentOverflow(page, 'desktop insights document overflow')
  expect(await visibleTargetFailures(page), 'desktop insights 44px targets').toEqual([])
  expect(await motionFailures(page), 'prefers-reduced-motion disables visible motion').toEqual([])

  const encodedAssetBytes = await page.evaluate(() => performance.getEntriesByType('resource')
    .filter((entry) => ['script', 'link'].includes((entry as PerformanceResourceTiming).initiatorType))
    .reduce((sum, entry) => sum + (entry as PerformanceResourceTiming).encodedBodySize, 0))
  expect(encodedAssetBytes, 'same-origin encoded JS+CSS bytes').toBeLessThanOrEqual(2 * 1024 * 1024)
  expect(audit.apiDurations.length).toBeGreaterThan(0)
  expect(Math.max(...audit.apiDurations), 'browser API latency').toBeLessThan(2_000)

  const evidenceResponse = await request.get(manifest.evidence_url)
  expect(evidenceResponse.status()).toBe(200)
  const evidence = await evidenceResponse.json() as Evidence
  expect(evidence.schema_version).toBe('qiu.full-stack.evidence.v1')
  expect(evidence.spy).toMatchObject({
    read_domain_trading_mutations: 0,
    read_domain_reference_writes: 0,
    read_domain_fund_writes: 0,
    forbidden_writes: 0,
    public_network_requests: 0,
    fixture_non_get_requests: 0,
  })

  expect(audit.origins, 'browser network origins').toEqual(new Set([frontendOrigin]))
  expect(audit.tradingMutations, 'C browser performs no trading writes').toEqual([])
  expect(audit.preAuthenticationResponses.toSorted(), 'exact anonymous-session bootstrap probes').toEqual([
    'GET /api/v1/trading/session 401',
  ])
  expect(audit.recoveryStatusRequests, 'capability-first flow never probes an unavailable recovery endpoint').toBe(0)
  expect(audit.observedResourceConsoleMessages.toSorted(), 'each generic Chrome resource error binds to an exact audited response')
    .toEqual(audit.expectedResourceConsoleMessages.toSorted())
  expect(audit.failedResponses).toEqual([])
  expect(audit.failedRequests).toEqual([])
  expect(audit.pageErrors).toEqual([])
  expect(audit.consoleMessages).toEqual([])
})
