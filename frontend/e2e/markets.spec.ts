import { expect, test } from '@playwright/test'

const available = (value: string) => ({ value, available: true })
const unavailable = { value: null, available: false }
const unavailablePriceFact = () => ({
  price_usd: unavailable,
  change_24h_pct: unavailable,
  turnover_24h_usd: unavailable,
  available: false,
  kind: 'unavailable',
  source: '',
  source_time: 0,
  observed_at: 0,
  last_success_at: 0,
  freshness_status: 'unavailable',
  freshness_age_seconds: 0,
  quality: 'unavailable',
  contributor_count: 0,
  contributors: [],
  version: 0,
})
const marketPriceFact = (
  value: string,
  kind: string,
  source: string,
  observedAt = Date.now(),
  contributors = [source],
) => ({
  price_usd: available(value),
  change_24h_pct: available('1.25'),
  turnover_24h_usd: available('500000000'),
  available: true,
  kind,
  source,
  source_time: observedAt - 500,
  observed_at: observedAt,
  last_success_at: observedAt,
  freshness_status: 'fresh',
  freshness_age_seconds: 0,
  quality: contributors.length >= 3 ? 'high' : contributors.length === 2 ? 'medium' : 'low',
  contributor_count: contributors.length,
  contributors,
  version: 10,
})

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

const snapshotForVenue = (venue: string) => {
  const encodedVenue = Buffer.from(venue).toString('hex').slice(0, 32).padEnd(32, '0')
  const venueOffset = [...venue].reduce((total, character) => total + character.charCodeAt(0), 0)
  return {
    snapshot_id: `snp_${encodedVenue}`,
    snapshot_as_of: 1_784_880_000_000 + venueOffset,
    snapshot_schema: 'qiu.market-snapshot.v1',
  }
}

const dashboardSnapshot = (venue: string, requestedSnapshotID?: string) => ({
  ...snapshotForVenue(venue),
  snapshot_id: requestedSnapshotID ?? snapshotForVenue(venue).snapshot_id,
})

const liveBTCPrices: Record<string, string> = {
  all: '64203.13',
  binance: '64213.56',
  coinbase: '64173.20',
  bybit: '64232.46',
  okx: '64238.61',
  hyperliquid: '64220.12',
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
      snapshot_id?: string
      page?: number
      page_size?: number
      asset_ids?: string[]
    }
    const venue = request.venue ?? 'all'
    const universe = providerAssets[venue] ?? assetUniverse
    const pageNumber = request.page ?? 1
    const pageSize = request.page_size ?? 50
    const pageStart = (pageNumber - 1) * pageSize
    let body: Record<string, unknown>
    if (path.endsWith('/get_market_price_ticks')) {
      const priceKind = venue === 'all'
        ? 'composite_reference'
        : venue === 'hyperliquid'
          ? 'perp_mark'
          : 'venue_spot'
      const source = venue === 'all' ? 'cex_composite' : venue
      body = {
        code: 2000,
        venue,
        server_time: Date.now(),
        result: (request.asset_ids ?? []).map((assetID) => ({
          asset_id: assetID,
          provider: venue,
          price_kind: venue === 'all' ? 'composite_spot' : priceKind,
          price_usd: assetID === 'asset-btc'
            ? available(liveBTCPrices[venue] ?? '64200')
            : unavailable,
          change_24h_pct: assetID === 'asset-btc' ? available('1.25') : unavailable,
          turnover_24h_usd: assetID === 'asset-btc' ? available('500000000') : unavailable,
          available: assetID === 'asset-btc',
          freshness_status: assetID === 'asset-btc' ? 'fresh' : 'unavailable',
          freshness_age_seconds: 1,
          source_time: Date.now() - 1_000,
          observed_at: Date.now() - 500,
          last_success_at: Date.now() - 500,
          version: 10,
          ...(assetID === 'asset-btc' && venue === 'all'
            ? {
                display_price: marketPriceFact(
                  liveBTCPrices[venue] ?? '64200',
                  priceKind,
                  source,
                  Date.now(),
                  ['binance', 'coinbase', 'bybit', 'okx'],
                ),
              }
            : {}),
          ...(assetID === 'asset-btc' && venue !== 'all'
            ? {
                venue_price: marketPriceFact(
                  liveBTCPrices[venue] ?? '64200',
                  priceKind,
                  source,
                ),
              }
            : {}),
        })),
      }
    } else if (path.endsWith('/get_market_overview')) {
      body = {
        code: 2000,
        ...snapshotForVenue(venue),
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
          displayed_asset_count: 1,
          unpriced_asset_count: universe.length - 1,
          fresh_asset_count: 1,
          stale_asset_count: 0,
          unavailable_asset_count: universe.length - 1,
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
        ...dashboardSnapshot(venue, request.snapshot_id),
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
  await expect(page.getByText('$64,203.13')).toBeVisible()
  await expect(page.getByRole('columnheader', { name: 'Sources' })).toBeVisible()
  const bitcoinRow = page.locator('tbody tr').filter({ hasText: 'Bitcoin' })
  await expect(bitcoinRow).toContainText('3+ sources')
  await expect(bitcoinRow).not.toContainText(/\b(high|medium|low)\b/i)
  await expect(page.getByText(/Freshness is a separate dimension/)).toBeVisible()
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
  const coinbaseBitcoinRow = page.locator('tbody tr').filter({ hasText: 'Bitcoin' })
  await expect(coinbaseBitcoinRow).toHaveCount(1)
  await expect(coinbaseBitcoinRow).toContainText('1 source')

  await page.getByRole('button', { name: 'Uniswap', exact: true }).click()
  await expect(page).toHaveURL(/venue=uniswap/)
  await expect(page.locator('tbody tr').filter({ hasText: 'Bitcoin' })).toHaveCount(1)
})

test('a disabled provider is explicit instead of looking like an empty search', async ({ page }) => {
  await page.route('**/api/v2/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      venue?: string
    }
    if (request.venue !== 'binance') {
      await route.fallback()
      return
    }
    const body = path.endsWith('/get_market_overview')
      ? {
          code: 2000,
          ...snapshotForVenue('binance'),
          result: {
            venue: 'binance',
            universe: 'provider_top50',
            asset_count: 50,
            eligible_asset_count: 0,
            published_asset_count: 0,
            priced_asset_count: 0,
            displayed_asset_count: 0,
            unpriced_asset_count: 50,
            fresh_asset_count: 0,
            stale_asset_count: 0,
            unavailable_asset_count: 50,
            local_preview_enabled: false,
          },
        }
      : {
          code: 2000,
          ...dashboardSnapshot('binance', request.snapshot_id),
          result: [],
          total: 0,
          universe: 'provider_top50',
        }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    })
  })

  await page.goto('/markets?venue=binance')

  await expect(page.locator('.market-empty-state')).toContainText(
    'Binance is unavailable in this deployment',
  )
  await expect(page.locator('.market-overview-strip')).toContainText('0 fresh')
  await expect(page.locator('.market-overview-strip')).toContainText('50 unavailable')
  await expect(page.getByText(/matched this search/)).toHaveCount(0)
})

test('slow search distinguishes loading from a settled empty result', async ({ page }) => {
  let releaseSlowSearch: (() => void) | undefined
  let signalSlowSearch: (() => void) | undefined
  const slowSearchRequested = new Promise<void>((resolve) => {
    signalSlowSearch = resolve
  })
  const slowSearchResponse = new Promise<void>((resolve) => {
    releaseSlowSearch = resolve
  })
  const monero = {
    ...asset,
    rank: 24,
    asset_id: 'asset-xmr',
    asset_symbol: 'XMR',
    asset_name: 'Monero',
  }

  await page.route('**/api/v2/get_asset_dashboard', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      search?: string
      snapshot_id?: string
    }
    if (request.search === 'XMR') {
      signalSlowSearch?.()
      await slowSearchResponse
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 2000,
          ...dashboardSnapshot('all', request.snapshot_id),
          result: [monero],
          total: 1,
          universe: 'provider_union',
        }),
      })
      return
    }
    if (request.search === 'NO-SUCH-ASSET') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 2000,
          ...dashboardSnapshot('all', request.snapshot_id),
          result: [],
          total: 0,
          universe: 'provider_union',
        }),
      })
      return
    }
    await route.fallback()
  })

  await page.goto('/markets')
  await expect(page.locator('tbody tr')).toHaveCount(50)

  const search = page.getByPlaceholder('Search the provider union…')
  await search.fill('XMR')
  await slowSearchRequested
  await expect(page.getByText('Loading market results…')).toBeVisible()
  await expect(page.getByText(/matched this search/)).toHaveCount(0)

  releaseSlowSearch?.()
  await expect(page.locator('tbody tr').filter({ hasText: 'Monero' })).toHaveCount(1)
  await expect(page.getByText('Loading market results…')).toHaveCount(0)

  await search.fill('NO-SUCH-ASSET')
  await expect(page.getByText('No assets in the provider union matched this search.')).toBeVisible()
  await expect(page.getByText('Loading market results…')).toHaveCount(0)
})

test('rapid CEX switching renders the selected venue tick instead of the previous venue response', async ({ page }) => {
  await page.goto('/markets?venue=coinbase')
  await expect(page.locator('tbody tr').filter({ hasText: 'Bitcoin' }))
    .toContainText('$64,173.20')

  await page.getByRole('button', { name: 'Bybit', exact: true }).click()
  await expect(page).toHaveURL(/venue=bybit/)
  const bitcoin = page.locator('tbody tr').filter({ hasText: 'Bitcoin' })
  await expect(bitcoin).toContainText('$64,232.46')
  await expect(bitcoin).toContainText('Bybit live')
  await expect(bitcoin).not.toContainText('$64,173.20')
})

test('A to B to A switching rejects the old A generation even when the query key matches again', async ({ page }) => {
  let releaseOld: (() => void) | undefined
  let releaseNew: (() => void) | undefined
  let signalOld: (() => void) | undefined
  let signalNew: (() => void) | undefined
  const oldRequested = new Promise<void>((resolve) => { signalOld = resolve })
  const newRequested = new Promise<void>((resolve) => { signalNew = resolve })
  const oldResponse = new Promise<void>((resolve) => { releaseOld = resolve })
  const newResponse = new Promise<void>((resolve) => { releaseNew = resolve })
  let coinbaseRequests = 0

  await page.route('**/api/v2/get_market_price_ticks', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      venue?: string
      asset_ids?: string[]
    }
    if (request.venue !== 'coinbase') {
      await route.fallback()
      return
    }
    coinbaseRequests += 1
    const isOld = coinbaseRequests === 1
    if (isOld) {
      signalOld?.()
      await oldResponse
    } else {
      signalNew?.()
      await newResponse
    }
    const value = isOld ? '63000.00' : '64180.00'
    const version = isOld ? 1 : 2
    const observedAt = Date.now()
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        venue: 'coinbase',
        server_time: observedAt,
        result: (request.asset_ids ?? []).map((assetID) => ({
          asset_id: assetID,
          provider: 'coinbase',
          price_kind: 'venue_spot',
          available: assetID === 'asset-btc',
          price_usd: assetID === 'asset-btc' ? available(value) : unavailable,
          change_24h_pct: assetID === 'asset-btc' ? available('1') : unavailable,
          turnover_24h_usd: assetID === 'asset-btc' ? available('1') : unavailable,
          freshness_status: assetID === 'asset-btc' ? 'fresh' : 'unavailable',
          freshness_age_seconds: 0,
          observed_at: observedAt,
          last_success_at: observedAt,
          version,
          ...(assetID === 'asset-btc'
            ? {
                venue_price: {
                  ...marketPriceFact(value, 'venue_spot', 'coinbase', observedAt),
                  version,
                },
              }
            : {}),
        })),
      }),
    })
  })

  await page.goto('/markets?venue=coinbase')
  await oldRequested
  await page.getByRole('button', { name: 'Bybit', exact: true }).click()
  await page.getByRole('button', { name: 'Coinbase', exact: true }).click()
  releaseOld?.()
  await newRequested

  const bitcoin = page.locator('tbody tr').filter({ hasText: 'Bitcoin' })
  await expect(bitcoin).not.toContainText('$63,000.00')
  releaseNew?.()
  await expect(bitcoin).toContainText('$64,180.00')
  await expect(bitcoin).toContainText('Coinbase live')
})

test('a failed live tick keeps the verified venue snapshot without falling back to composite', async ({ page }) => {
  await page.route('**/api/v2/get_market_price_ticks', async (route) => {
    await route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({ code: 5030, message: 'tick feed unavailable' }),
    })
  })
  await page.route('**/api/v2/get_asset_dashboard', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      venue?: string
      snapshot_id?: string
    }
    if (request.venue !== 'binance') {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        ...dashboardSnapshot('binance', request.snapshot_id),
        result: [{
          ...asset,
          price_usd: available('64111.25'),
          venue_price: marketPriceFact(
            '64111.25',
            'venue_spot',
            'binance',
          ),
          display_price_usd: available('65000.00'),
          display_price_kind: 'composite_reference',
          price_kind: 'venue_spot',
          price_source: 'binance',
        }],
        total: 1,
        universe: 'provider_top50',
      }),
    })
  })

  await page.goto('/markets?venue=binance')
  const bitcoin = page.locator('tbody tr').filter({ hasText: 'Bitcoin' })
  await expect(bitcoin).toContainText('$64,111.25')
  await expect(bitcoin).not.toContainText('$65,000.00')
  await expect(bitcoin).toContainText('Binance last-good · tick request failed')
  await expect(page.getByText(/Live price ticks are delayed/)).toBeVisible()
})

test('a late lower-version tick preserves the newer cached fact', async ({ page }) => {
  let tickRequests = 0
  const newestObservedAt = Date.now()
  let releaseOlderResponse: (() => void) | undefined
  const olderResponse = new Promise<void>((resolve) => {
    releaseOlderResponse = resolve
  })

  await page.route('**/api/v2/get_market_price_ticks', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      venue?: string
      asset_ids?: string[]
    }
    if (request.venue !== 'bybit') {
      await route.fallback()
      return
    }
    tickRequests += 1
    const newest = tickRequests === 1
    if (!newest) await olderResponse
    const value = newest ? '64250.00' : '64100.00'
    const observedAt = newest ? newestObservedAt : newestObservedAt - 5_000
    const version = newest ? 12 : 11
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        venue: 'bybit',
        server_time: Date.now(),
        result: (request.asset_ids ?? []).map((assetID) => ({
          asset_id: assetID,
          provider: 'bybit',
          price_kind: 'venue_spot',
          price_usd: assetID === 'asset-btc' ? available(value) : unavailable,
          change_24h_pct: assetID === 'asset-btc' ? available('1.25') : unavailable,
          turnover_24h_usd: assetID === 'asset-btc'
            ? available('500000000')
            : unavailable,
          available: assetID === 'asset-btc',
          freshness_status: assetID === 'asset-btc' ? 'fresh' : 'unavailable',
          freshness_age_seconds: 0,
          source_time: observedAt - 500,
          observed_at: observedAt,
          last_success_at: observedAt,
          version,
          ...(assetID === 'asset-btc'
            ? {
                venue_price: {
                  ...marketPriceFact(value, 'venue_spot', 'bybit', observedAt),
                  version,
                },
              }
            : {}),
        })),
      }),
    })
  })

  await page.goto('/markets?venue=bybit')
  const bitcoin = page.locator('tbody tr').filter({ hasText: 'Bitcoin' })
  await expect(bitcoin).toContainText('$64,250.00')
  await expect(bitcoin).toContainText('Bybit live')

  releaseOlderResponse?.()
  await expect.poll(() => tickRequests, { timeout: 7_000 }).toBeGreaterThanOrEqual(2)
  await expect(bitcoin).toContainText('$64,250.00')
  await expect(bitcoin).not.toContainText('$64,100.00')
  await expect(bitcoin).toContainText('Bybit last-good · older tick rejected')
})

test('one offline asset does not degrade the other assets in the same tick batch', async ({ page }) => {
  let tickRequests = 0
  const initialObservedAt = Date.now()
  let releasePartialResponse: (() => void) | undefined
  const partialResponse = new Promise<void>((resolve) => {
    releasePartialResponse = resolve
  })
  const ethAsset = {
    ...asset,
    rank: 2,
    asset_id: 'asset-eth',
    asset_symbol: 'ETH',
    asset_name: 'Ethereum',
    price_usd: available('3200.00'),
    market_cap_usd: available('384000000000'),
    venue_price: marketPriceFact(
      '3200.00',
      'venue_spot',
      'okx',
      initialObservedAt,
    ),
    price_kind: 'venue_spot',
    price_source: 'okx',
  }

  await page.route('**/api/v2/get_asset_dashboard', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      venue?: string
      snapshot_id?: string
    }
    if (request.venue !== 'okx') {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        ...dashboardSnapshot('okx', request.snapshot_id),
        result: [{
          ...asset,
          price_usd: available('64200.00'),
          venue_price: marketPriceFact(
            '64200.00',
            'venue_spot',
            'okx',
            initialObservedAt,
          ),
          price_kind: 'venue_spot',
          price_source: 'okx',
        }, ethAsset],
        total: 2,
        universe: 'provider_top50',
      }),
    })
  })
  await page.route('**/api/v2/get_market_price_ticks', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      venue?: string
      asset_ids?: string[]
    }
    if (request.venue !== 'okx') {
      await route.fallback()
      return
    }
    tickRequests += 1
    const secondBatch = tickRequests >= 2
    if (secondBatch) await partialResponse
    const observedAt = initialObservedAt + (secondBatch ? 3_000 : 0)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        venue: 'okx',
        server_time: Date.now(),
        result: (request.asset_ids ?? []).map((assetID) => {
          const unavailableETH = secondBatch && assetID === 'asset-eth'
          const value = assetID === 'asset-btc'
            ? secondBatch ? '64250.00' : '64200.00'
            : '3200.00'
          const version = secondBatch ? 11 : 10
          return {
            asset_id: assetID,
            provider: 'okx',
            price_kind: 'venue_spot',
            price_usd: unavailableETH ? unavailable : available(value),
            change_24h_pct: unavailableETH ? unavailable : available('1.25'),
            turnover_24h_usd: unavailableETH
              ? unavailable
              : available('500000000'),
            available: !unavailableETH,
            freshness_status: unavailableETH ? 'unavailable' : 'fresh',
            freshness_age_seconds: 0,
            source_time: observedAt - 500,
            observed_at: observedAt,
            last_success_at: observedAt,
            version,
            venue_price: unavailableETH
              ? unavailablePriceFact()
              : {
                  ...marketPriceFact(value, 'venue_spot', 'okx', observedAt),
                  version,
                },
          }
        }),
      }),
    })
  })

  await page.goto('/markets?venue=okx')
  const bitcoin = page.locator('tbody tr').filter({ hasText: 'Bitcoin' })
  const ethereum = page.locator('tbody tr').filter({ hasText: 'Ethereum' })
  await expect(bitcoin).toContainText('$64,200.00')
  await expect(ethereum).toContainText('$3,200.00')
  await expect(bitcoin).toContainText('OKX live')
  await expect(ethereum).toContainText('OKX live')

  releasePartialResponse?.()
  await expect.poll(() => tickRequests, { timeout: 7_000 }).toBeGreaterThanOrEqual(2)
  await expect(bitcoin).toContainText('$64,250.00')
  await expect(bitcoin).toContainText('OKX live')
  await expect(ethereum).toContainText('$3,200.00')
  await expect(ethereum).toContainText('OKX last-good · venue feed unavailable')
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

test('DEX route and reference stay in separate lanes after route expiry', async ({ page }) => {
  const observedAt = Date.now() - 2_000
  const routeFact = {
    ...marketPriceFact('64211.10', 'dex_route', 'uniswap', observedAt),
    change_24h_pct: available('0.42'),
    turnover_24h_usd: available('1000000'),
    quality: 'high',
  }
  const compositeReference = {
    ...marketPriceFact(
      '64203.13',
      'composite_reference',
      'cex_composite',
      observedAt,
    ),
    change_24h_pct: available('1.25'),
    quality: 'high',
  }
  const marketReference = {
    ...marketPriceFact(
      '64100.00',
      'market_reference',
      'coingecko',
      observedAt,
    ),
    change_24h_pct: unavailable,
    turnover_24h_usd: unavailable,
    quality: 'reference',
  }

  await page.route('**/api/v2/get_asset_dashboard', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      venue?: string
      snapshot_id?: string
    }
    if (request.venue !== 'uniswap') {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        ...dashboardSnapshot('uniswap', request.snapshot_id),
        result: [
          {
            ...asset,
            asset_id: 'asset-fresh-route',
            asset_symbol: 'FRESH',
            asset_name: 'Fresh Route',
            price_usd: available('64211.10'),
            change_24h_pct: available('0.42'),
            price_kind: 'dex_route',
            price_source: 'uniswap',
            dex_route_available: true,
            dex_route_count: 1,
            dex_route_price: routeFact,
            display_price: compositeReference,
          },
          {
            ...asset,
            rank: 2,
            asset_id: 'asset-expired-route',
            asset_symbol: 'OLD',
            asset_name: 'Expired Route',
            price_usd: available('63999.00'),
            change_24h_pct: available('9.50'),
            covered_turnover_24h_usd: available('123456'),
            price_kind: 'dex_route',
            price_source: 'uniswap',
            available: true,
            freshness_status: 'stale',
            freshness_age_seconds: 90,
            dex_route_available: false,
            dex_route_count: 0,
            dex_route_price: unavailablePriceFact(),
            display_price: marketReference,
          },
        ],
        total: 2,
        universe: 'provider_top50',
      }),
    })
  })

  await page.goto('/markets?venue=uniswap')

  const freshRow = page.locator('tbody tr').filter({ hasText: 'Fresh Route' })
  await expect(freshRow.getByTestId('dex-route-price')).toContainText('$64,211.10')
  await expect(freshRow.getByTestId('dex-route-price')).toContainText('Uniswap route')
  await expect(freshRow.getByTestId('dex-reference-price')).toContainText('$64,203.13')
  await expect(freshRow.getByTestId('dex-reference-price')).toContainText('CEX composite')
  await expect(freshRow.getByTestId('dex-route-change')).toContainText('0.42%')
  await expect(freshRow.getByTestId('dex-reference-change')).toContainText('1.25%')
  await expect(freshRow.getByTestId('dex-route-quality')).toContainText('Route · 1 source')
  await expect(freshRow.getByTestId('dex-reference-quality')).toContainText('Reference · 1 source')

  const expiredRow = page.locator('tbody tr').filter({ hasText: 'Expired Route' })
  await expect(expiredRow.getByTestId('dex-route-price')).toContainText('Route unavailable')
  await expect(expiredRow.getByTestId('dex-route-price')).not.toContainText('Uniswap')
  await expect(expiredRow.getByTestId('dex-route-price')).not.toContainText('$63,999.00')
  await expect(expiredRow.getByTestId('dex-route-change')).not.toContainText('9.50%')
  await expect(expiredRow.getByTestId('dex-route-quality')).toContainText('Route · unavailable')
  await expect(expiredRow.getByTestId('dex-reference-price')).toContainText('$64,100.00')
  await expect(expiredRow.getByTestId('dex-reference-price')).toContainText(
    'CoinGecko market reference',
  )
  await expect(expiredRow.getByTestId('dex-reference-quality')).toContainText(
    'Reference · 1 source',
  )
})

test('DEX coverage never reuses a previous search response as the canonical universe', async ({ page }) => {
  let releaseFullResponse: (() => void) | undefined
  let signalFullRequest: (() => void) | undefined
  const fullRequest = new Promise<void>((resolve) => {
    signalFullRequest = resolve
  })
  const fullResponse = new Promise<void>((resolve) => {
    releaseFullResponse = resolve
  })
  const displayRow = (row: typeof asset) => ({
    ...row,
    display_price_usd: available('1'),
    display_price_kind: 'composite_reference',
    display_change_24h_pct: unavailable,
    display_change_kind: 'unavailable',
    display_available: true,
    dex_route_available: false,
    dex_route_price: unavailablePriceFact(),
    display_price: marketPriceFact(
      '1',
      'composite_reference',
      'cex_composite',
    ),
  })

  await page.route('**/api/v2/get_asset_dashboard', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as {
      search?: string
      snapshot_id?: string
    }
    if (request.search === 'BTC') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 2000,
          ...dashboardSnapshot('uniswap', request.snapshot_id),
          result: [displayRow(asset)],
          total: 1,
        }),
      })
      return
    }
    signalFullRequest?.()
    await fullResponse
    const rows = providerAssets.uniswap.map(displayRow)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        ...dashboardSnapshot('uniswap', request.snapshot_id),
        result: rows,
        total: rows.length,
      }),
    })
  })

  await page.goto('/markets?venue=uniswap&search=BTC')
  await expect(page.locator('tbody tr')).toHaveCount(1)
  const coverage = page.locator('.market-overview-strip article').filter({
    hasText: 'Uniswap Coverage',
  })

  await page.getByPlaceholder('Search Uniswap selection…').fill('')
  await fullRequest
  await expect(page.locator('tbody tr').filter({ hasText: 'Bitcoin' })).toHaveCount(0)
  await expect(coverage.locator('strong')).not.toHaveText('100.0%')
  await expect(coverage).toContainText('Snapshot only')

  releaseFullResponse?.()
  await expect(page.locator('tbody tr')).toHaveCount(50)
  await expect(coverage.locator('strong')).toHaveText('100.0%')
})

test('unknown composite values are rendered as unavailable, never fake zero', async ({ page }) => {
  await page.route('**/api/v2/get_asset_dashboard', async (route) => {
    const request = JSON.parse(route.request().postData() ?? '{}') as { snapshot_id?: string }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        ...dashboardSnapshot('all', request.snapshot_id),
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
    const request = JSON.parse(route.request().postData() ?? '{}') as { snapshot_id?: string }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        code: 2000,
        ...dashboardSnapshot('all', request.snapshot_id),
        result: [{ ...asset, asset_name: 'BTC', asset_symbol: 'BTC' }],
        total: 1,
      }),
    })
  })
  await page.goto('/markets')
  const assetRow = page.locator('tbody tr').filter({ hasText: '$64,203.13' })
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
