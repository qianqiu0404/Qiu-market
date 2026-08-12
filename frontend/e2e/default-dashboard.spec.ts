import { expect, test } from '@playwright/test'

test.skip(
  !/^[0-9a-f]{40}$/.test(process.env.QIU_MARKET_RELEASE_COMMIT ?? ''),
  'requires an exact release SHA so the client selects the cacheable GET path',
)

const available = (value: string) => ({ value, available: true })
const unavailable = { value: null, available: false }

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/get_system_overview', async (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({
      code: 2000,
      result: {
        crawler_status: 'running', redis_status: 'running', database_status: 'running',
        worker_status: 'running', api_status: 'running', provider_statuses: [],
      },
    }),
  }))
})

test('exact-release default GET prefetches other venues without replacing the visible query', async ({ page }) => {
  const pageErrors: string[] = []
  page.on('pageerror', (error) => pageErrors.push(error.message))
  const requestedVenues = new Set<string>()
  let dashboardPosts = 0
  let releaseHeldPrefetch!: () => void
  let signalHeldPrefetch!: () => void
  const heldPrefetch = new Promise<void>((resolve) => { releaseHeldPrefetch = resolve })
  const heldPrefetchStarted = new Promise<void>((resolve) => { signalHeldPrefetch = resolve })
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/v2/get_asset_dashboard') dashboardPosts += 1
  })
  await page.route('**/api/market/default-dashboard**', async (route) => {
    const now = Date.now()
    const url = new URL(route.request().url())
    expect(route.request().method()).toBe('GET')
    const venue = url.searchParams.get('venue') ?? 'all'
    requestedVenues.add(venue)
    if (venue === 'binance') {
      signalHeldPrefetch()
      await heldPrefetch
      return
    }
    const venueHex = Buffer.from(venue).toString('hex').slice(0, 32).padEnd(32, '0')
    const snapshotID = `snp_${venueHex}`
    const visible = venue === 'all'
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        snapshot_id: snapshotID,
        snapshot_as_of: now,
        snapshot_schema: 'qiu.market-snapshot.v1',
        overview: {
          venue,
          snapshot_id: snapshotID,
          snapshot_as_of: now,
          snapshot_schema: 'qiu.market-snapshot.v1',
          asset_count: 1,
          priced_asset_count: 1,
          displayed_asset_count: 1,
          unpriced_asset_count: 0,
          fresh_asset_count: 1,
          stale_asset_count: 0,
          unavailable_asset_count: 0,
          single_venue_priced_asset_count: 1,
          multi_venue_priced_asset_count: 0,
          global_market_cap_usd: available('1000000000'),
          covered_spot_volume_24h_usd: available('1000000'),
          btc_dominance_pct: available('50'),
          coverage_ratio_pct: available('100'),
        },
        result: [{
          rank: 1,
          asset_id: `asset-${venue}`,
          asset_symbol: visible ? 'VIS' : venue.toUpperCase(),
          asset_name: visible ? 'Visible All Asset' : `Prefetch ${venue}`,
          price_usd: available('123.45'),
          composite_price_usd: available('123.45'),
          change_24h_pct: available('1.5'),
          market_cap_usd: available('1000000000'),
          covered_turnover_24h_usd: unavailable,
          circulating_supply: unavailable,
          contributor_count: 1,
          priced_venue_count: 1,
          quality: 'low',
          price_kind: 'venue_spot',
          price_source: venue,
          coverage_status: 'covered',
          available: true,
          observed_at: now,
          source_time: now,
        }],
        total: 1,
        universe: venue === 'all' ? 'provider_union' : 'provider_top50',
      }),
    })
  })
  await page.route('**/api/v2/get_market_price_ticks', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as { venue?: string }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000, venue: request.venue ?? 'all', server_time: Date.now(), result: [],
      }),
    })
  })
  await page.route('**/api/v1/get_fiat_rates', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        result: { base: 'USD', rates: { USD: 1, CNY: 7.2, HKD: 7.8 }, source: 'test' },
      }),
    })
  })

  await page.goto('/markets')
  await expect(page.getByRole('heading', { name: 'Market Overview' })).toBeVisible()
  await expect(page.locator('tbody')).toContainText('Visible All Asset')
  await heldPrefetchStarted
  await page.getByRole('heading', { name: 'Market Overview' }).click()
  releaseHeldPrefetch()
  await page.waitForTimeout(750)
  expect([...requestedVenues]).toEqual(['all', 'binance'])
  await expect(page).toHaveURL(/\/markets$/)
  await expect(page.locator('tbody')).toContainText('Visible All Asset')
  await expect(page.locator('tbody')).not.toContainText('Prefetch')
  expect(dashboardPosts).toBe(0)
  expect(pageErrors).toEqual([])
})

test('same-release IndexedDB restores within 200ms and survives a delayed 504', async ({ page }) => {
  const pageErrors: string[] = []
  page.on('pageerror', (error) => pageErrors.push(error.message))
  const release = process.env.QIU_MARKET_RELEASE_COMMIT as string
  const query = {
    venue: 'all', page: 1, pageSize: 50, search: '', filter: 'assets',
    sortBy: 'rank', sortDirection: 'desc', universe: 'provider_union',
  }
  const queryKey = JSON.stringify(query)
  const snapshotID = 'snp_11111111111111111111111111111111'
  const now = Date.now()
  const value = {
    queryKey,
    data: {
      snapshot_id: snapshotID,
      snapshot_as_of: now,
      snapshot_schema: 'qiu.market-snapshot.v1',
      total: 1,
      overview: {
        venue: 'all', snapshot_id: snapshotID, snapshot_as_of: now,
        snapshot_schema: 'qiu.market-snapshot.v1', asset_count: 1,
        priced_asset_count: 1, displayed_asset_count: 1, unpriced_asset_count: 0,
        fresh_asset_count: 1, stale_asset_count: 0, unavailable_asset_count: 0,
        single_venue_priced_asset_count: 1, multi_venue_priced_asset_count: 0,
        global_market_cap_usd: { value: 1_000_000_000, available: true },
        covered_spot_volume_24h_usd: { value: 1_000_000, available: true },
        btc_dominance_pct: { value: 50, available: true },
        coverage_ratio_pct: { value: 100, available: true },
        advance_ratio_pct: { value: 100, available: true },
        display_coverage_ratio_pct: { value: 100, available: true },
      },
      items: [{
        rank: 1, asset_id: 'asset-idb', asset_symbol: 'IDB',
        asset_name: 'Persisted Last Good', price_usd: { value: 100, available: true },
        composite_price_usd: { value: 100, available: true },
        change_24h_pct: { value: 1, available: true },
        market_cap_usd: { value: 1_000_000_000, available: true },
        covered_turnover_24h_usd: { value: null, available: false },
        circulating_supply: { value: null, available: false },
        contributor_count: 1, priced_venue_count: 1,
        spot_market_count: 1, perp_market_count: 0, dex_route_count: 0,
        confidence: 'low', quality: 'low', price_kind: 'composite_spot', price_source: 'all',
        coverage_status: 'covered', coverage_reason: '', available: true,
        source_time: now, observed_at: now,
        index_updated_at: now, provider_updated_at: now,
        sparkline_available: false,
        display_price: {
          price_usd: { value: 100, available: true },
          change_24h_pct: { value: 1, available: true },
          turnover_24h_usd: { value: null, available: false }, available: true,
          kind: 'composite_reference', source: 'cex_composite', source_time: now,
          observed_at: now, last_success_at: now, freshness_status: 'fresh',
          freshness_age_seconds: 0, quality: 'low', contributor_count: 1,
          contributors: ['okx'], version: 1,
        },
        venue_price: {
          price_usd: { value: null, available: false },
          change_24h_pct: { value: null, available: false },
          turnover_24h_usd: { value: null, available: false }, available: false,
          kind: 'unavailable', source: '', source_time: 0, observed_at: 0,
          last_success_at: 0, freshness_status: 'unavailable', freshness_age_seconds: 0,
          quality: 'unavailable', contributor_count: 0, contributors: [], version: 0,
        },
      }],
    },
  }
  await page.goto('/not-found')
  await page.evaluate(async ({ releaseCommit, key, snapshot }) => {
    const database = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open('qiu-market-last-good-v1', 1)
      request.onupgradeneeded = () => request.result.createObjectStore('dashboards')
      request.onsuccess = () => resolve(request.result)
      request.onerror = () => reject(request.error)
    })
    await new Promise<void>((resolve, reject) => {
      const request = database.transaction('dashboards', 'readwrite').objectStore('dashboards').put({
        schema: 'qiu.market-dashboard-last-good.v1', releaseCommit, queryKey: key,
        venue: 'all', storedAt: Date.now(), value: snapshot,
      }, `${releaseCommit}:${key}`)
      request.onsuccess = () => resolve()
      request.onerror = () => reject(request.error)
    })
  }, { releaseCommit: release, key: queryKey, snapshot: value })
  await page.route('**/api/market/default-dashboard**', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 9_000))
    await route.fulfill({
      status: 504, contentType: 'application/json',
      body: JSON.stringify({ code: 5040, message: 'Qiu Market backend timed out.' }),
    })
  })
  await page.route('**/api/v1/get_fiat_rates', async (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({
      code: 2000, result: { base: 'USD', rates: { USD: 1, CNY: 7.2, HKD: 7.8 } },
    }),
  }))
  await page.route('**/api/v2/get_market_price_ticks', async (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ code: 2000, venue: 'all', server_time: Date.now(), result: [] }),
  }))
  await page.addInitScript(() => {
    const started = performance.now()
    const observe = () => {
      const observer = new MutationObserver(() => {
        if (!document.body?.textContent?.includes('Persisted Last Good')) return
        ;(window as unknown as { __qiuRestoreMs: number }).__qiuRestoreMs = performance.now() - started
        observer.disconnect()
      })
      observer.observe(document.documentElement, { childList: true, subtree: true, characterData: true })
    }
    if (document.documentElement) observe()
    else document.addEventListener('DOMContentLoaded', observe, { once: true })
  })
  await page.goto('/markets')
  try {
    await expect(page.locator('tbody')).toContainText('Persisted Last Good', { timeout: 1_000 })
  } catch (error) {
    if (pageErrors.length > 0) throw new Error(`page errors: ${pageErrors.join('; ')}`)
    throw error
  }
  const restoreMs = await page.evaluate(() =>
    (window as unknown as { __qiuRestoreMs: number }).__qiuRestoreMs)
  expect(restoreMs).toBeLessThanOrEqual(200)
  await expect(page.getByText(/showing this query's last successful snapshot/)).toBeVisible()
  await expect(page.getByText(/Live refresh was delayed/)).toBeVisible({ timeout: 10_000 })
  await expect(page.locator('tbody')).toContainText('Persisted Last Good')
  await expect(page.getByText(/showing this query's last successful snapshot/)).toHaveCount(0)
  expect(pageErrors).toEqual([])
})
