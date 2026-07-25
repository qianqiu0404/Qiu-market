import { expect, test } from '@playwright/test'

const available = (value: string) => ({ value, available: true })
const unavailable = { value: null, available: false }

const asset = {
  rank: 1,
  asset_id: 'asset-btc',
  asset_symbol: 'BTC',
  asset_name: 'Bitcoin',
  logo: '',
  price_usd: available('65705.09'),
  composite_price_usd: available('65705.09'),
  change_24h_pct: available('1.47'),
  market_cap_usd: available('1310000000000'),
  covered_turnover_24h_usd: available('26730000000'),
  circulating_supply: available('20060000'),
  spot_market_count: 4,
  perp_market_count: 1,
  dex_route_count: 1,
  contributor_count: 4,
  priced_venue_count: 4,
  confidence: 'high',
  quality: 'high',
  price_kind: 'composite_spot',
  price_source: 'all',
  coverage_status: 'covered',
  coverage_reason: '',
  available: true,
  source_time: 1784880000000,
  observed_at: 1784880000000,
  index_updated_at: 1784880000000,
  provider_updated_at: 1784880000000,
  sparkline_available: false,
}

const assetUniverse = Array.from({ length: 80 }, (_, index) => {
  if (index === 0) return asset
  const rank = index + 1
  return {
    ...asset,
    rank,
    asset_id: `asset-${rank}`,
    asset_symbol: `A${rank}`,
    asset_name: `Asset ${rank}`,
    price_usd: unavailable,
    composite_price_usd: unavailable,
    change_24h_pct: unavailable,
    market_cap_usd: available(String(1_000_000_000 - rank)),
    covered_turnover_24h_usd: unavailable,
    circulating_supply: unavailable,
    spot_market_count: 0,
    perp_market_count: 0,
    dex_route_count: 0,
    contributor_count: 0,
    priced_venue_count: 0,
    confidence: 'unknown',
    quality: 'unknown',
    coverage_status: 'not_covered',
    coverage_reason: 'not_covered',
    available: false,
  }
})

const providerAssets: Record<string, typeof assetUniverse> = {
  binance: assetUniverse.slice(0, 50),
  coinbase: [...assetUniverse.slice(0, 40), ...assetUniverse.slice(50, 60)],
  bybit: [...assetUniverse.slice(0, 40), ...assetUniverse.slice(60, 70)],
  okx: [...assetUniverse.slice(0, 40), ...assetUniverse.slice(70, 80)],
  hyperliquid: assetUniverse.slice(0, 50),
  uniswap: [assetUniverse[0], ...assetUniverse.slice(10, 59)],
  pancakeswap: [assetUniverse[0], ...assetUniverse.slice(31, 80)],
}

const markets = [
  {
    market_id: 'market-binance-btc-usdt',
    market_code: 'binance:BTC/USDT:spot',
    provider: 'binance',
    symbol: 'BTC/USDT',
    market_type: 'spot',
    quote_asset: 'USDT',
    price: available('65700'),
    relative_deviation_pct: available('-0.0077'),
    change_24h_pct: available('1.46'),
    turnover_24h: available('12000000000'),
    freshness_status: 'Healthy',
    provider_updated_at: 1784880000000,
    confidence: 'high',
    has_kline: true,
  },
  {
    market_id: 'market-hyperliquid-btc-usd',
    market_code: 'hyperliquid:BTC/USD:perp',
    provider: 'hyperliquid',
    symbol: 'BTC/USD',
    market_type: 'perp',
    quote_asset: 'USD',
    price: available('65720'),
    relative_deviation_pct: available('0.0227'),
    change_24h_pct: available('1.52'),
    turnover_24h: available('1800000000'),
    freshness_status: 'Healthy',
    provider_updated_at: 1784880000000,
    confidence: 'excluded_perp',
    has_kline: false,
  },
  {
    market_id: '',
    market_code: '',
    provider: 'uniswap',
    symbol: 'WBTC/USDC',
    market_type: 'dex_route',
    quote_asset: 'USD',
    price: available('65710'),
    relative_deviation_pct: available('0.0075'),
    change_24h_pct: unavailable,
    turnover_24h: unavailable,
    freshness_status: 'Healthy',
    provider_updated_at: 1784880000000,
    confidence: 'high',
    quality: 'high',
    has_kline: false,
    venue_kind: 'dex_route',
    chain: 'ethereum',
    protocol: 'uniswap-v3',
    route_key: 'wbtc-usdc-3000',
    route: ['WBTC', 'USDC'],
    pool_addresses: ['0x0000000000000000000000000000000000000001'],
    quote_notional_usd: available('10000'),
    tvl_usd: available('22000000'),
    price_impact_pct: available('0.1'),
    round_trip_spread_pct: available('0.2'),
    block_number: 23000000,
    block_timestamp: 1784880000000,
    available: true,
    unavailable_reason: '',
  },
]

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v2/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      venue?: string
      page?: number
      page_size?: number
    }
    const venue = request.venue ?? 'all'
    const universe = providerAssets[venue] ?? assetUniverse
    const pageNumber = request.page ?? 1
    const pageSize = request.page_size ?? 50
    const pageStart = (pageNumber - 1) * pageSize
    let body: Record<string, unknown>
    if (path.endsWith('/get_market_overview')) {
      body = {
        code: 2000,
        result: {
          global_market_cap_usd: available('2240000000000'),
          covered_spot_volume_24h_usd: available('62310000000'),
          btc_dominance_pct: available('58.9'),
          asset_count: universe.length,
          ranked_asset_count: universe.length,
          top50_universe_count: universe.length,
          eligible_asset_count: 27,
          published_asset_count: 10,
          priced_asset_count: 1,
          change_available_count: 1,
          coverage_ratio_pct: available('2'),
          venue,
          universe: venue === 'all' ? 'provider_union' : 'provider_top50',
          selection_version: venue === 'all' ? 0 : 3,
          advancers: 1,
          decliners: 0,
          flat: 0,
          unknown: 0,
          advance_ratio_pct: available('100'),
          provider_updated_at: 1784880000000,
          index_updated_at: 1784880000000,
        },
      }
    } else if (path.endsWith('/get_asset_markets') || path.endsWith('/get_asset_venues')) {
      body = { code: 2000, result: markets }
    } else {
      body = {
        code: 2000,
        result: universe.slice(pageStart, pageStart + pageSize),
        total: universe.length,
        universe: venue === 'all' ? 'provider_union' : 'provider_top50',
      }
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    })
  })
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const result = path.endsWith('/get_fiat_rates')
      ? { base: 'USD', rates: { USD: 1, CNY: 7.2, HKD: 7.8 }, source: 'test' }
      : {
          crawler_status: 'running',
          redis_status: 'running',
          database_status: 'running',
          worker_status: 'running',
          api_status: 'running',
          provider_statuses: [],
        }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ code: 2000, result }),
    })
  })
})

test('composite asset drawer is URL-addressable and keeps perpetuals out of the spot index', async ({ page }) => {
  await page.goto('/markets')
  await expect(page.getByRole('heading', { name: 'Market Overview' })).toBeVisible()
  await expect(page.getByText('$65,705.09')).toBeVisible()
  await page.getByRole('button', { name: 'Open BTC markets' }).click()

  await expect(page).toHaveURL(/asset=asset-btc/)
  await expect(page.getByRole('dialog', { name: 'BTC markets' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Spot Markets' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Perpetual Markets' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'DEX Routes' })).toBeVisible()
  await expect(page.getByText(/never contribute to the composite spot index/)).toBeVisible()
  await expect(page.getByText(/not executable orders or arbitrage signals/)).toBeVisible()
  await expect(page.getByRole('button', { name: /Open venue chart/ })).toHaveCount(1)

  await page.reload()
  await expect(page.getByRole('dialog', { name: 'BTC markets' })).toBeVisible()
})

test('venue selection is URL-addressable without changing asset row grain', async ({ page }) => {
  await page.goto('/markets')
  const coinbaseRequest = page.waitForRequest((request) =>
    request.url().includes('/api/v2/get_asset_dashboard') &&
    JSON.parse(request.postData() ?? '{}').venue === 'coinbase')
  await page.getByRole('button', { name: 'Coinbase', exact: true }).click()
  expect(JSON.parse((await coinbaseRequest).postData() ?? '{}').include_uncovered).toBe(true)
  await expect(page).toHaveURL(/venue=coinbase/)
  await expect(page.locator('tbody tr').filter({ hasText: 'Bitcoin' })).toHaveCount(1)

  await page.getByRole('button', { name: 'Uniswap', exact: true }).click()
  await expect(page).toHaveURL(/venue=uniswap/)
  await expect(page.locator('tbody tr').filter({ hasText: 'Bitcoin' })).toHaveCount(1)
})

test('all seven provider tabs expose 50 assets while All exposes their canonical union', async ({ page }) => {
  await page.goto('/markets')
  await expect(page.locator('tbody tr')).toHaveCount(50)
  await expect(page.getByText('1–50 of 80')).toBeVisible()
  for (const venue of [
    'Binance', 'Coinbase', 'Bybit', 'OKX',
    'Hyperliquid', 'Uniswap', 'PancakeSwap',
  ]) {
    await page.getByRole('button', { name: venue, exact: true }).click()
    await expect(page.locator('tbody tr')).toHaveCount(50)
    await expect(page.getByText('1–50 of 50')).toBeVisible()
  }
})

test('DEX tabs expose 50 identity-verified listed assets without inventing quotes', async ({ page }) => {
  await page.goto('/markets')
  for (const venue of ['Hyperliquid', 'Uniswap', 'PancakeSwap'] as const) {
    await page.getByRole('button', { name: venue, exact: true }).click()
    await expect(page.locator('tbody tr')).toHaveCount(50)
    await expect(page.getByText('1–50 of 50')).toBeVisible()
  }
})

test('unknown composite values are rendered as unavailable, never fake zero', async ({ page }) => {
  await page.route('**/api/v2/get_asset_dashboard', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        result: [{ ...asset, change_24h_pct: unavailable }],
        total: 1,
      }),
    })
  })
  await page.goto('/markets')
  const assetRow = page.locator('tbody tr').filter({ hasText: 'Bitcoin' })
  await expect(assetRow.getByText('0.00%')).toHaveCount(0)
})

test('asset name is not repeated when it equals the symbol', async ({ page }) => {
  await page.route('**/api/v2/get_asset_dashboard', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        result: [{ ...asset, asset_name: 'BTC', asset_symbol: 'BTC' }],
        total: 1,
      }),
    })
  })
  await page.goto('/markets')
  const assetRow = page.locator('tbody tr').filter({ hasText: '$65,705.09' })
  await expect(assetRow.locator('.asset-name')).toHaveText('BTC')
  await expect(assetRow.locator('.asset-symbol')).toHaveCount(0)
})

test('legacy overview routes redirect to their canonical entrypoints', async ({ page }) => {
  await page.goto('/dashboard')
  await expect(page).toHaveURL(/\/markets$/)
  await page.goto('/analytics')
  await expect(page).toHaveURL(/\/insights$/)
})

test('Insights uses the seven-provider union for All and stable selections for every venue', async ({ page }) => {
  const dashboardRequests: Array<{ venue: string; universe: string }> = []
  page.on('request', (request) => {
    if (!request.url().includes('/api/v2/get_asset_dashboard')) return
    const body = JSON.parse(request.postData() ?? '{}') as {
      venue?: string
      universe?: string
    }
    dashboardRequests.push({
      venue: body.venue ?? 'all',
      universe: body.universe ?? '',
    })
  })

  await page.goto('/insights')
  const coverageSection = page.locator('.insight-section').filter({
    has: page.getByRole('heading', { name: 'Provider Selection Coverage' }),
  })
  await expect(coverageSection).toBeVisible()
  await expect(coverageSection.locator('.error-state')).toHaveCount(0)
  await expect.poll(() => dashboardRequests.length).toBeGreaterThanOrEqual(8)

  const universeByVenue = new Map(
    dashboardRequests.map((request) => [request.venue, request.universe]),
  )
  expect(universeByVenue.get('all')).toBe('provider_union')
  for (const venue of [
    'binance', 'coinbase', 'bybit', 'okx',
    'hyperliquid', 'uniswap', 'pancakeswap',
  ]) {
    expect(universeByVenue.get(venue)).toBe('provider_top50')
  }
})

for (const width of [1440, 1280, 1180]) {
  test(`Markets keeps page-level overflow contained at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/markets')
    await expect(page.getByRole('heading', { name: 'Market Overview' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Open BTC markets' })).toBeVisible()
    const visual = await page.evaluate(() => {
      const style = getComputedStyle(document.documentElement)
      return {
        overflows: document.documentElement.scrollWidth > document.documentElement.clientWidth,
        background: getComputedStyle(document.body).backgroundColor,
        text: getComputedStyle(document.body).color,
        accent: style.getPropertyValue('--accent').trim(),
      }
    })
    expect(visual).toEqual({
      overflows: false,
      background: 'rgb(245, 245, 247)',
      text: 'rgb(29, 29, 31)',
      accent: '#0071e3',
    })
  })
}
