<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '../components/PageHeader.vue'
import AssetLogo from '../components/AssetLogo.vue'
import StatusBadge from '../components/StatusBadge.vue'
import ErrorState from '../components/ErrorState.vue'
import AppIcon from '../components/AppIcon.vue'
import { usePolling } from '../composables/usePolling'
import { ApiError } from '../api/common'
import {
  getAssetDashboardV2,
  getAssetVenuesV2,
  getFiatRates,
  getMarketOverviewV2,
  getMarketPriceTicks,
  marketTickCacheKey,
  mergeMarketPriceTickSnapshot,
  unavailableMarketPriceFact,
  validatedDexRoutePriceFact,
  validatedDisplayReferencePriceFact,
  type AssetDashboardV2Item,
  type AssetFilter,
  type AssetMarketV2Item,
  type AvailableDecimal,
  type MarketOverviewV2,
  type MarketPriceFact,
  type MarketPriceTickSnapshot,
  type MarketTickState,
  type MarketVenue,
  type Paged,
} from '../api/market'
import { formatAbbr, formatPercent, formatPrice, providerFreshnessVariant } from '../utils/format'

type Fiat = 'USD' | 'CNY' | 'HKD'

const FIAT_SYMBOLS: Record<Fiat, string> = { USD: '$', CNY: '¥', HKD: 'HK$' }
const FALLBACK_RATES: Record<Fiat, number> = { USD: 1, CNY: 7.2, HKD: 7.8 }
const FILTERS: Array<{ value: AssetFilter; label: string }> = [
  { value: 'assets', label: 'Assets' },
  { value: 'gainers', label: 'Gainers' },
  { value: 'losers', label: 'Losers' },
]
const VENUE_GROUPS: Array<{
  label: string
  venues: Array<{ value: MarketVenue; label: string }>
}> = [
  { label: 'Index', venues: [{ value: 'all', label: 'All' }] },
  {
    label: 'CEX',
    venues: [
      { value: 'binance', label: 'Binance' },
      { value: 'coinbase', label: 'Coinbase' },
      { value: 'bybit', label: 'Bybit' },
      { value: 'okx', label: 'OKX' },
    ],
  },
  {
    label: 'DEX',
    venues: [
      { value: 'hyperliquid', label: 'Hyperliquid' },
      { value: 'uniswap', label: 'Uniswap' },
      { value: 'pancakeswap', label: 'PancakeSwap' },
    ],
  },
]
const VALID_VENUES = new Set(VENUE_GROUPS.flatMap((group) => group.venues.map((item) => item.value)))
const REALTIME_VENUES = new Set<MarketVenue>([
  'all', 'binance', 'coinbase', 'bybit', 'okx', 'hyperliquid',
])

const route = useRoute()
const router = useRouter()
const fiat = ref<Fiat>('USD')
const rates = ref<Record<string, number> | null>(null)
const ratesFailed = ref(false)
const venue = ref<MarketVenue>(
  typeof route.query.venue === 'string' && VALID_VENUES.has(route.query.venue as MarketVenue)
    ? route.query.venue as MarketVenue
    : 'all',
)
const filter = ref<AssetFilter>(
  route.query.filter === 'gainers' || route.query.filter === 'losers'
    ? route.query.filter
    : 'assets',
)
const page = ref(positiveInt(route.query.page, 1))
const pageSize = ref(positiveInt(route.query.page_size, 50))
const search = ref(typeof route.query.search === 'string' ? route.query.search : '')
const sortKey = ref(
  typeof route.query.sort_by === 'string' ? route.query.sort_by : 'rank',
)
const sortDir = ref<'asc' | 'desc'>(route.query.sort_direction === 'asc' ? 'asc' : 'desc')
const drawerMarkets = ref<AssetMarketV2Item[]>([])
const drawerLoading = ref(false)
const drawerError = ref('')

const selectedAssetID = computed(() =>
  typeof route.query.asset === 'string' ? route.query.asset : '',
)

function positiveInt(value: unknown, fallback: number): number {
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback
}

const requestedUniverse = computed<'provider_top50' | 'provider_union'>(() => {
  if (venue.value === 'all') return 'provider_union'
  return 'provider_top50'
})
const overview = usePolling(async () => {
  const requestedVenue = venue.value
  return {
    venue: requestedVenue,
    data: await getMarketOverviewV2(requestedVenue),
  }
}, { interval: 30_000 })
const pollingOverview = computed<MarketOverviewV2 | null>(() => {
  const snapshot = overview.data.value
  return snapshot?.venue === venue.value ? snapshot.data : null
})

interface DashboardQuery {
  venue: MarketVenue
  page: number
  pageSize: number
  search: string
  filter: AssetFilter
  sortBy: 'rank' | 'market_cap' | 'turnover24h' | 'change24h' | 'price' | 'symbol'
  sortDirection: 'asc' | 'desc'
  universe: 'provider_top50' | 'provider_union'
}

interface DashboardSnapshot {
  queryKey: string
  data: Paged<AssetDashboardV2Item>
	overview: MarketOverviewV2
}

interface DashboardFailure {
  queryKey: string
  message: string
}

function readDashboardQuery(): DashboardQuery {
  return {
    venue: venue.value,
    page: page.value,
    pageSize: pageSize.value,
    search: search.value,
    filter: filter.value,
    sortBy: sortKey.value as DashboardQuery['sortBy'],
    sortDirection: sortDir.value,
    universe: requestedUniverse.value,
  }
}

function dashboardQueryKey(query: DashboardQuery): string {
  return JSON.stringify(query)
}

const currentDashboardQueryKey = computed(() =>
  dashboardQueryKey(readDashboardQuery()))
const dashboardFailure = ref<DashboardFailure | null>(null)
const dashboard = usePolling(
  async (): Promise<DashboardSnapshot> => {
    const query = readDashboardQuery()
    const queryKey = dashboardQueryKey(query)
    try {
		let boundOverview = await getMarketOverviewV2(query.venue)
		const loadDashboard = (): Promise<Paged<AssetDashboardV2Item>> =>
			getAssetDashboardV2(query.page, query.pageSize, {
				venue: query.venue,
				search: query.search,
				filter: query.filter,
				sortBy: query.sortBy,
				sortDirection: query.sortDirection,
				includeUncovered: true,
				universe: query.universe,
				snapshotID: boundOverview.snapshot_id,
			})
		let data: Paged<AssetDashboardV2Item>
		try {
			data = await loadDashboard()
		} catch (error) {
			if (!(error instanceof ApiError) || error.status !== 409) throw error
			boundOverview = await getMarketOverviewV2(query.venue)
			data = await loadDashboard()
		}
      if (dashboardFailure.value?.queryKey === queryKey) {
        dashboardFailure.value = null
      }
		return { queryKey, data, overview: boundOverview }
    } catch (error) {
      dashboardFailure.value = {
        queryKey,
        message: error instanceof Error ? error.message : 'Unable to load market results',
      }
      throw error
    }
  },
  { interval: 15_000 },
)

const currentOverview = computed<MarketOverviewV2 | null>(() => {
	const snapshot = dashboard.data.value
	if (snapshot?.queryKey === currentDashboardQueryKey.value) return snapshot.overview
	return pollingOverview.value
})
const currentDashboard = computed(() => {
  const snapshot = dashboard.data.value
  return snapshot?.queryKey === currentDashboardQueryKey.value
	&& snapshot.data.snapshot_id === currentOverview.value?.snapshot_id
    ? snapshot.data
    : null
})
const currentDashboardError = computed(() =>
  dashboardFailure.value?.queryKey === currentDashboardQueryKey.value
    ? dashboardFailure.value.message
    : null)
const currentDashboardLoading = computed(() =>
  currentDashboard.value === null && currentDashboardError.value === null)
const assets = computed(() => currentDashboard.value?.items ?? [])
const total = computed(() => currentDashboard.value?.total ?? 0)
interface PriceTickSnapshot {
  queryKey: string
  generation: number
  data: MarketPriceTickSnapshot
}

interface PriceTickFailure {
  queryKey: string
  generation: number
  message: string
}

function priceTickQueryKey(selectedVenue = venue.value, rows = assets.value): string {
  return JSON.stringify({
    venue: selectedVenue,
    asset_ids: rows.map((asset) => asset.asset_id),
  })
}

const currentPriceTickQueryKey = computed(() => priceTickQueryKey())
const priceTickGeneration = ref(0)
const priceTickFailure = ref<PriceTickFailure | null>(null)
const lastGoodTickFacts = ref<Map<string, MarketPriceFact>>(new Map())
const latestTickStates = ref<Map<string, MarketTickState>>(new Map())
const priceTicks = usePolling(
  async (): Promise<PriceTickSnapshot> => {
    const requestedVenue = venue.value
    const assetIDs = assets.value.map((asset) => asset.asset_id)
    const queryKey = priceTickQueryKey(requestedVenue, assets.value)
    const generation = priceTickGeneration.value
    if (!REALTIME_VENUES.has(requestedVenue) || assetIDs.length === 0) {
      return {
        queryKey,
        generation,
        data: { venue: requestedVenue, server_time: 0, items: [] },
      }
    }
    try {
      const data = await getMarketPriceTicks(requestedVenue, assetIDs)
      if (generation === priceTickGeneration.value &&
        queryKey === currentPriceTickQueryKey.value) {
        priceTickFailure.value = null
      }
      return { queryKey, generation, data }
    } catch (error) {
      priceTickFailure.value = {
        queryKey,
        generation,
        message: error instanceof Error ? error.message : 'Live tick request failed',
      }
      throw error
    }
  },
  { interval: 3_000 },
)
const currentPriceTicks = computed(() => {
  const snapshot = priceTicks.data.value
  return snapshot?.queryKey === currentPriceTickQueryKey.value &&
    snapshot.generation === priceTickGeneration.value &&
    snapshot.data.venue === venue.value
    ? snapshot.data
    : null
})
const currentPriceTickFailure = computed(() => {
  const failure = priceTickFailure.value
  return failure?.queryKey === currentPriceTickQueryKey.value &&
    failure.generation === priceTickGeneration.value
    ? failure
    : null
})
watch(currentPriceTicks, (snapshot) => {
  if (!snapshot) return
  const merged = mergeMarketPriceTickSnapshot(
    lastGoodTickFacts.value,
    snapshot,
    venue.value,
    assets.value.map((asset) => asset.asset_id),
  )
  lastGoodTickFacts.value = merged.lastGood
  latestTickStates.value = merged.states
}, { immediate: true })
const priceRefreshedAt = computed(() => {
  const serverTime = currentPriceTicks.value?.server_time ?? 0
  return serverTime > 0
    ? new Date(serverTime)
    : currentDashboard.value
      ? dashboard.lastUpdated.value
      : null
})
const dexCoverage = computed(() => {
  if (
    !isDexVenue() ||
    page.value !== 1 ||
    search.value.trim() !== '' ||
    filter.value !== 'assets' ||
    total.value <= 0 ||
    assets.value.length !== total.value
  ) {
    return null
  }
  const routed = assets.value.filter((asset) => dexRouteFact(asset).available).length
  const displayed = assets.value.filter((asset) =>
    dexRouteFact(asset).available || dexReferenceFact(asset).available).length
  return {
    displayed,
    routed,
    referenceOnly: assets.value.filter((asset) =>
      dexReferenceFact(asset).available && !dexRouteFact(asset).available).length,
    unavailable: Math.max(0, total.value - displayed),
    coveragePct: displayed / total.value * 100,
  }
})
const coveragePercentLabel = computed(() => {
  if (isDexVenue() && dexCoverage.value) {
    return `${dexCoverage.value.coveragePct.toFixed(1)}%`
  }
  const metric = isDexVenue()
    ? currentOverview.value?.display_coverage_ratio_pct
    : currentOverview.value?.coverage_ratio_pct
  return metric?.available
    ? `${(metric.value ?? 0).toFixed(1)}%`
    : '—'
})
const selectedAsset = computed(() =>
  assets.value.find((asset) => asset.asset_id === selectedAssetID.value) ?? null,
)
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))
const rangeStart = computed(() => (total.value === 0 ? 0 : (page.value - 1) * pageSize.value + 1))
const rangeEnd = computed(() => Math.min(total.value, (page.value - 1) * pageSize.value + assets.value.length))

onMounted(async () => {
  window.addEventListener('keydown', onKeydown)
  try {
    rates.value = (await getFiatRates()).rates
  } catch {
    ratesFailed.value = true
  }
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  if (searchTimer !== undefined) window.clearTimeout(searchTimer)
})

const rate = computed(() => {
  if (fiat.value === 'USD') return 1
  const value = rates.value?.[fiat.value]
  return typeof value === 'number' && value > 0 ? value : FALLBACK_RATES[fiat.value]
})
const usingFallbackRates = computed(
  () => fiat.value !== 'USD' && (ratesFailed.value || !rates.value?.[fiat.value]),
)

function syncRoute(): void {
  void router.replace({
    query: {
      ...(filter.value !== 'assets' ? { filter: filter.value } : {}),
      ...(venue.value !== 'all' ? { venue: venue.value } : {}),
      ...(page.value > 1 ? { page: String(page.value) } : {}),
      ...(pageSize.value !== 50 ? { page_size: String(pageSize.value) } : {}),
      ...(search.value ? { search: search.value } : {}),
      ...(sortKey.value !== 'rank' ? { sort_by: sortKey.value } : {}),
      ...(sortDir.value !== 'desc' ? { sort_direction: sortDir.value } : {}),
      ...(selectedAssetID.value ? { asset: selectedAssetID.value } : {}),
    },
  })
}

watch([venue, filter, page, pageSize, sortKey, sortDir], ([nextVenue, nextFilter], [previousVenue, previousFilter]) => {
  if (nextVenue !== previousVenue || nextFilter !== previousFilter) page.value = 1
  syncRoute()
  void overview.refresh()
  void dashboard.refresh()
})

watch(
  currentPriceTickQueryKey,
  () => {
    priceTickGeneration.value += 1
    latestTickStates.value = new Map()
    void priceTicks.refresh()
  },
  { flush: 'sync' },
)

let searchTimer: number | undefined
watch(search, () => {
  page.value = 1
  syncRoute()
  if (searchTimer !== undefined) window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => void dashboard.refresh(), 250)
})

let drawerRequest = 0
watch([selectedAssetID, venue], async ([assetID, selectedVenue]) => {
  const requestID = ++drawerRequest
  drawerMarkets.value = []
  drawerError.value = ''
  if (!assetID) return
  drawerLoading.value = true
  try {
    const markets = await getAssetVenuesV2(assetID, selectedVenue)
    if (requestID === drawerRequest) drawerMarkets.value = markets
  } catch (error) {
    if (requestID === drawerRequest) {
      drawerError.value = error instanceof Error ? error.message : 'Unable to load markets'
    }
  } finally {
    if (requestID === drawerRequest) drawerLoading.value = false
  }
}, { immediate: true })

const spotMarkets = computed(() => drawerMarkets.value.filter((market) => market.market_type === 'spot'))
const perpMarkets = computed(() => drawerMarkets.value.filter((market) => market.market_type === 'perp'))
const dexRoutes = computed(() => drawerMarkets.value.filter((market) => market.market_type === 'dex_route'))

const selectedVenueLabel = computed(() =>
  VENUE_GROUPS.flatMap((group) => group.venues).find((item) => item.value === venue.value)?.label ?? 'All',
)
const pageSubtitle = computed(() => {
  const current = currentOverview.value
  if (venue.value === 'all') {
    if (!current) return 'One asset per row · seven-provider union · globally ordered by market cap'
		return `${current.fresh_asset_count} fresh · ${current.stale_asset_count} stale · ` +
			`${current.unavailable_asset_count} unavailable · ${current.contributing_provider_count} current CEX contributor${current.contributing_provider_count === 1 ? '' : 's'}`
  }
  if (!current) return `${selectedVenueLabel.value} · loading reviewed asset selection`
  const preview = current.local_preview_enabled ? ' · Local preview' : ''
  const version = current.selection_version > 0 ? ` · selection v${current.selection_version}` : ''
  if (venue.value === 'uniswap' || venue.value === 'pancakeswap') {
    const coverage = dexCoverage.value
    if (coverage) {
      return `${coverage.displayed}/${total.value} displayed · ` +
        `${coverage.routed} current routes · ${coverage.referenceOnly} reference only${version}${preview}`
    }
    return `${current.displayed_asset_count}/${current.asset_count} displayed snapshot · ` +
      `route freshness pending full-page verification${version}${preview}`
  }
  const product = venue.value === 'hyperliquid'
    ? 'perpetual marks'
    : 'spot markets'
	return `${current.fresh_asset_count} fresh · ${current.stale_asset_count} stale · ` +
		`${current.unavailable_asset_count} unavailable ${product}${version}${preview}`
})
const pageTitle = computed(() =>
  venue.value === 'all'
    ? 'Market Overview'
    : venue.value === 'hyperliquid'
      ? 'Hyperliquid Perpetuals'
      : venue.value === 'uniswap' || venue.value === 'pancakeswap'
        ? `${selectedVenueLabel.value} Assets`
        : `${selectedVenueLabel.value} Spot`,
)
const searchPlaceholder = computed(() =>
  venue.value === 'all' ? 'Search the provider union…' : `Search ${selectedVenueLabel.value} selection…`,
)

function setSort(key: string): void {
  if (sortKey.value === key) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortKey.value = key
    sortDir.value = key === 'rank' ? 'asc' : 'desc'
  }
  page.value = 1
}

function openAsset(asset: AssetDashboardV2Item): void {
  void router.replace({ query: { ...route.query, asset: asset.asset_id } })
}

function closeDrawer(): void {
  const query = { ...route.query }
  delete query.asset
  void router.replace({ query })
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape' && selectedAssetID.value) closeDrawer()
}

function openMarket(market: AssetMarketV2Item): void {
  if (market.has_kline) {
    void router.push({ name: 'market-detail', params: { marketId: market.market_id } })
  }
}

function formatMetric(metric: AvailableDecimal, prefix = '', multiplier = 1): string {
  if (!metric.available || metric.value == null) return '—'
  return formatAbbr(metric.value * multiplier, prefix)
}

function formatMetricPrice(metric: AvailableDecimal): string {
  if (!metric.available || metric.value == null) return '—'
  return `${FIAT_SYMBOLS[fiat.value]}${formatPrice(metric.value * rate.value)}`
}

function confidenceVariant(confidence: string): 'live' | 'delayed' | 'accent' {
  if (confidence === 'high') return 'live'
  if (confidence === 'medium') return 'delayed'
  return 'accent'
}

function dexRouteFact(asset: AssetDashboardV2Item): MarketPriceFact {
  if (!isDexVenue()) return unavailableMarketPriceFact()
  return validatedDexRoutePriceFact(asset.dex_route_price, venue.value)
}

function dexReferenceFact(asset: AssetDashboardV2Item): MarketPriceFact {
  if (!isDexVenue()) return unavailableMarketPriceFact()
  return validatedDisplayReferencePriceFact(asset.display_price)
}

function priceFactQualityLabel(fact: MarketPriceFact): string {
  if (!fact.available) return 'unavailable'
  return fact.quality || 'unknown'
}

function priceFactQualityVariant(
  fact: MarketPriceFact,
): 'live' | 'delayed' | 'accent' {
  if (!fact.available) return 'accent'
  if (fact.freshness_status === 'stale') return 'delayed'
  return confidenceVariant(fact.quality)
}

function assetQualityLabel(asset: AssetDashboardV2Item): string {
  if (REALTIME_VENUES.has(venue.value)) {
    const resolved = resolveRealtimePrice(asset)
    if (resolved.mode === 'last_good') return 'last-good'
    if (resolved.mode === 'unavailable') return 'unavailable'
    return resolved.fact.quality || asset.quality || asset.confidence || 'unknown'
  }
  if (asset.freshness_status === 'stale') return 'stale'
  if (asset.freshness_status === 'unavailable') return 'unavailable'
  return asset.quality || asset.confidence || 'unknown'
}

function assetQualityVariant(asset: AssetDashboardV2Item): 'live' | 'delayed' | 'accent' {
  if (REALTIME_VENUES.has(venue.value)) {
    const resolved = resolveRealtimePrice(asset)
    if (resolved.mode === 'last_good' || resolved.mode === 'dashboard') return 'delayed'
    if (resolved.mode === 'unavailable') return 'accent'
    return confidenceVariant(resolved.fact.quality)
  }
  return confidenceVariant(asset.confidence)
}

function isDexVenue(): boolean {
  return venue.value === 'uniswap' || venue.value === 'pancakeswap'
}

type RealtimePriceMode = 'live' | 'last_good' | 'dashboard' | 'unavailable'

interface RealtimePriceResolution {
  fact: MarketPriceFact
  mode: RealtimePriceMode
  reason: string
}

function dashboardRealtimeFact(asset: AssetDashboardV2Item): MarketPriceFact {
  const fact = venue.value === 'all' ? asset.display_price : asset.venue_price
  const expectedSource = venue.value === 'all' ? 'cex_composite' : venue.value
  const expectedKind = venue.value === 'all'
    ? 'composite_reference'
    : venue.value === 'hyperliquid'
      ? 'perp_mark'
      : 'venue_spot'
  return fact.source === expectedSource && fact.kind === expectedKind
    ? fact
    : unavailableMarketPriceFact()
}

function priceFactAgeSeconds(fact: MarketPriceFact): number {
  if (!fact.available || fact.observed_at <= 0) return Number.POSITIVE_INFINITY
  const wallAge = Math.max(0, Math.floor((Date.now() - fact.observed_at) / 1_000))
  return Math.max(fact.freshness_age_seconds, wallAge)
}

function fresherFact(
  left: MarketPriceFact | undefined,
  right: MarketPriceFact | undefined,
): MarketPriceFact | undefined {
  if (!left?.available) return right?.available ? right : undefined
  if (!right?.available) return left
  if (left.version !== right.version) return left.version > right.version ? left : right
  return left.observed_at >= right.observed_at ? left : right
}

function resolveRealtimePrice(asset: AssetDashboardV2Item): RealtimePriceResolution {
  const state = latestTickStates.value.get(asset.asset_id)
  if (!currentPriceTickFailure.value && state?.status === 'live' && state.fact) {
    return { fact: state.fact, mode: 'live', reason: '' }
  }
  const cached = lastGoodTickFacts.value.get(
    marketTickCacheKey(venue.value, asset.asset_id),
  )
  const dashboardFact = dashboardRealtimeFact(asset)
  const fallback = fresherFact(cached, dashboardFact)
  const reason = currentPriceTickFailure.value
    ? 'request_failed'
    : state?.status ?? 'pending'
  if (fallback?.available && priceFactAgeSeconds(fallback) <= 300) {
    const degraded = Boolean(currentPriceTickFailure.value) ||
      (state != null && state.status !== 'live') ||
      priceFactAgeSeconds(fallback) > 30 ||
      fallback.freshness_status !== 'fresh'
    return {
      fact: fallback,
      mode: degraded || cached === fallback ? 'last_good' : 'dashboard',
      reason,
    }
  }
  return {
    fact: unavailableMarketPriceFact(),
    mode: 'unavailable',
    reason,
  }
}

function displayPrice(asset: AssetDashboardV2Item): AvailableDecimal {
  if (isDexVenue()) return dexRouteFact(asset).price_usd
  if (REALTIME_VENUES.has(venue.value)) {
    return resolveRealtimePrice(asset).fact.price_usd
  }
  return asset.display_price_usd
}

function displayChange(asset: AssetDashboardV2Item): AvailableDecimal {
  if (isDexVenue()) return dexRouteFact(asset).change_24h_pct
  if (REALTIME_VENUES.has(venue.value)) {
    return resolveRealtimePrice(asset).fact.change_24h_pct
  }
  return asset.change_24h_pct
}

function displayTurnover(asset: AssetDashboardV2Item): AvailableDecimal {
  if (isDexVenue()) return dexRouteFact(asset).turnover_24h_usd
  if (REALTIME_VENUES.has(venue.value)) {
    return resolveRealtimePrice(asset).fact.turnover_24h_usd
  }
  return asset.covered_turnover_24h_usd
}

function marketCount(asset: AssetDashboardV2Item): number {
  if (venue.value === 'hyperliquid') return asset.perp_market_count
  if (venue.value === 'uniswap' || venue.value === 'pancakeswap') return asset.dex_route_count
  if (venue.value === 'all') return asset.spot_market_count + asset.perp_market_count + asset.dex_route_count
  return asset.spot_market_count
}

function marketCountLabel(asset: AssetDashboardV2Item): string {
  const count = marketCount(asset)
  if (venue.value === 'uniswap' || venue.value === 'pancakeswap') {
    return `${count} route${count === 1 ? '' : 's'}`
  }
  return `${count} market${count === 1 ? '' : 's'}`
}

function priceCaption(asset: AssetDashboardV2Item): string {
  if (isDexVenue()) return dexRouteCaption(asset)
  if (REALTIME_VENUES.has(venue.value)) {
    const resolved = resolveRealtimePrice(asset)
    const source = venue.value === 'all' ? 'Composite' : selectedVenueLabel.value
    const age = priceFactAgeSeconds(resolved.fact)
    if (resolved.mode === 'live') return `${source} live · ${age}s old`
    if (resolved.mode === 'dashboard') {
      return `${source} verified snapshot · live tick pending · ${age}s old`
    }
    const reason = tickDegradationLabel(resolved.reason)
    if (resolved.mode === 'last_good') {
      return `${source} last-good · ${reason} · ${age}s old`
    }
    return `${source} unavailable · ${reason}`
  }
  if (asset.freshness_status === 'stale') {
    return `Stale · ${asset.freshness_age_seconds}s old`
  }
  if (!asset.available) return coverageReasonLabel(asset.coverage_reason || asset.coverage_status)
  if (venue.value === 'all') {
    return `${asset.priced_venue_count || asset.contributor_count} contributor${(asset.priced_venue_count || asset.contributor_count) === 1 ? '' : 's'}`
  }
  if (venue.value === 'hyperliquid') return 'Perpetual mark'
  return `${selectedVenueLabel.value} spot`
}

function dexRouteCaption(asset: AssetDashboardV2Item): string {
  const fact = dexRouteFact(asset)
  if (!fact.available) return 'Route unavailable'
  return `${selectedVenueLabel.value} route · ${fact.freshness_status} · ` +
    `${priceFactAgeSeconds(fact)}s old`
}

function dexReferenceCaption(asset: AssetDashboardV2Item): string {
  const fact = dexReferenceFact(asset)
  if (!fact.available) return 'Reference unavailable'
  const source = fact.kind === 'composite_reference'
    ? 'CEX composite'
    : 'CoinGecko market reference'
  return `${source} · ${fact.freshness_status} · ${priceFactAgeSeconds(fact)}s old`
}

function tickDegradationLabel(reason: string): string {
  switch (reason) {
    case 'request_failed': return 'tick request failed'
    case 'missing': return 'asset missing from tick'
    case 'unavailable': return 'venue feed unavailable'
    case 'delayed': return 'tick delayed'
    case 'source_mismatch': return 'wrong source rejected'
    case 'out_of_order': return 'older tick rejected'
    default: return 'live tick pending'
  }
}

function quoteNotionalLabel(market: AssetMarketV2Item): string {
  const value = market.quote_notional_usd.available
    ? Number(market.quote_notional_usd.value)
    : 0
  if (value >= 1000) return `$${formatAbbr(value)} quote`
  if (value > 0) return `$${value.toLocaleString()} quote`
  return 'Route quote'
}

function quoteReferenceLabel(kind: string): string {
  if (kind === 'cex_correlated') return 'CEX corroborated'
  if (kind === 'onchain_only') return 'On-chain only'
  return 'Unavailable'
}

function coverageReasonLabel(reason: string): string {
  switch (reason) {
    case 'stale': return 'Stale'
    case 'source_unavailable': return 'Source unavailable'
    case 'rollout_pending': return 'Rollout pending'
    case 'identity_pending': return 'Identity pending'
    case 'not_listed': return 'Not covered'
    case 'not_covered': return 'Not covered'
    case 'missing_24h_reference': return '24h reference missing'
    default: return 'Not covered'
  }
}
</script>

<template>
  <section data-screen-label="CMC-style asset market">
    <PageHeader
      :title="pageTitle"
      :subtitle="pageSubtitle"
      :refreshed-at="priceRefreshedAt"
    >
      <template #actions>
        <div class="segmented" role="group" aria-label="Fiat currency">
          <button
            v-for="currency in (['USD', 'CNY', 'HKD'] as Fiat[])"
            :key="currency"
            type="button"
            :class="{ active: fiat === currency }"
            @click="fiat = currency"
          >
            {{ currency }}
          </button>
        </div>
      </template>
    </PageHeader>

    <p v-if="usingFallbackRates" class="fallback-hint">
      Fiat endpoint unavailable — the non-USD display uses a local fallback rate.
    </p>
    <p
      v-if="REALTIME_VENUES.has(venue) && currentPriceTickFailure"
      class="fallback-hint"
    >
      Live price ticks are delayed — an age-bounded last-good venue fact remains visible.
    </p>

    <div class="market-overview-strip" aria-label="Global market overview">
      <article>
        <span>Global Market Cap</span>
        <strong class="num">
          {{ currentOverview
            ? formatMetric(currentOverview.global_market_cap_usd, '$')
            : '—' }}
        </strong>
        <small>CoinGecko global</small>
      </article>
      <article>
        <span>{{ selectedVenueLabel }} Coverage</span>
        <strong class="num">{{ coveragePercentLabel }}</strong>
        <small v-if="isDexVenue() && dexCoverage">
          {{ dexCoverage.routed }} current routes ·
          {{ dexCoverage.referenceOnly }} reference only ·
          {{ dexCoverage.unavailable }} unavailable
        </small>
        <small v-else-if="currentOverview && isDexVenue()">
          Snapshot only · route freshness pending full-page verification
        </small>
        <small v-else-if="currentOverview">
					{{ currentOverview.fresh_asset_count }} fresh ·
					{{ currentOverview.stale_asset_count }} stale ·
					{{ currentOverview.unavailable_asset_count }} unavailable
        </small>
        <small v-else>Selected provider assets</small>
      </article>
      <article>
        <span>BTC Dominance</span>
        <strong class="num">
          {{ currentOverview?.btc_dominance_pct.available
            ? formatPercent(currentOverview.btc_dominance_pct.value ?? 0)
            : '—' }}
        </strong>
        <small>CoinGecko global</small>
      </article>
      <article>
        <span>Market Breadth</span>
        <strong class="num">
          {{ currentOverview?.advance_ratio_pct.available
            ? `${(currentOverview.advance_ratio_pct.value ?? 0).toFixed(1)}% up`
            : '—' }}
        </strong>
        <small v-if="currentOverview">
          {{ currentOverview.advancers }} up · {{ currentOverview.decliners }} down ·
          {{ currentOverview.unknown }} unknown
        </small>
        <small v-else>Composite index</small>
      </article>
    </div>

    <div class="market-list card">
      <div class="venue-switcher" aria-label="Market venue">
        <div v-for="group in VENUE_GROUPS" :key="group.label" class="venue-group">
          <span>{{ group.label }}</span>
          <div class="segmented">
            <button
              v-for="option in group.venues"
              :key="option.value"
              type="button"
              :class="{ active: venue === option.value }"
              @click="venue = option.value"
            >
              {{ option.label }}
            </button>
          </div>
        </div>
      </div>
      <div class="market-list-toolbar">
        <div class="segmented asset-filter" role="group" aria-label="Asset filter">
          <button
            v-for="option in FILTERS"
            :key="option.value"
            type="button"
            :class="{ active: filter === option.value }"
            @click="filter = option.value"
          >
            {{ option.label }}
          </button>
        </div>
        <label class="table-search">
          <AppIcon name="search" :size="15" />
          <input v-model="search" type="search" :placeholder="searchPlaceholder" />
        </label>
      </div>

      <ErrorState
        v-if="currentDashboardError && assets.length === 0"
        :message="currentDashboardError"
        @retry="dashboard.refresh"
      />

      <p
        v-else-if="currentDashboardLoading"
        class="market-loading-state"
        role="status"
        aria-live="polite"
      >
        Loading market results…
      </p>

      <p
        v-else-if="assets.length === 0"
        class="market-empty-state"
        role="status"
      >
        {{ venue === 'all'
          ? 'No assets in the provider union matched this search.'
          : currentOverview?.published_asset_count === 0
            ? `${selectedVenueLabel} is unavailable in this deployment — no provider assets are currently published.`
            : `No ${selectedVenueLabel} selected assets matched this search.` }}
      </p>

      <div v-else class="table-scroll">
        <table>
          <thead>
            <tr>
              <th><button type="button" class="sort-label" @click="setSort('rank')">#</button></th>
              <th><button type="button" class="sort-label" @click="setSort('symbol')">Asset</button></th>
              <th class="align-right">
                <button type="button" class="sort-label" @click="setSort('price')">
                  {{ isDexVenue() ? 'Route / Reference' : 'Price' }}
                </button>
              </th>
              <th class="align-right">
                <button type="button" class="sort-label" @click="setSort('change24h')">
                  {{ isDexVenue() ? 'Route / Ref 24h %' : '24h %' }}
                </button>
              </th>
              <th class="align-right"><button type="button" class="sort-label" @click="setSort('market_cap')">Market Cap</button></th>
              <th class="align-right">
                <button type="button" class="sort-label" @click="setSort('turnover24h')">
                  {{ isDexVenue() ? 'Route Volume' : 'Venue Volume' }}
                </button>
              </th>
              <th class="align-center">Markets / Routes</th>
              <th class="align-center">{{ isDexVenue() ? 'Route / Ref Quality' : 'Quality' }}</th>
            </tr>
          </thead>
          <tbody v-if="assets.length">
            <tr
              v-for="asset in assets"
              :key="asset.asset_id"
              class="asset-row"
              @click="openAsset(asset)"
            >
              <td class="num rank">{{ asset.rank ?? '—' }}</td>
              <td>
                <span class="asset-cell">
                  <AssetLogo :src="asset.logo" :name="asset.asset_symbol" :size="28" />
                  <span class="asset-names">
                    <span class="asset-name">{{ asset.asset_name || asset.asset_symbol }}</span>
                    <span
                      v-if="asset.asset_name.trim().toUpperCase() !== asset.asset_symbol.trim().toUpperCase()"
                      class="asset-symbol"
                    >
                      {{ asset.asset_symbol }}
                    </span>
                  </span>
                </span>
              </td>
              <td class="align-right">
                <div v-if="isDexVenue()" class="dex-price-lanes">
                  <span class="dex-price-lane" data-testid="dex-route-price">
                    <small class="dex-lane-label">Route</small>
                    <strong class="num">{{ formatMetricPrice(dexRouteFact(asset).price_usd) }}</strong>
                    <small class="contributors">{{ dexRouteCaption(asset) }}</small>
                  </span>
                  <span class="dex-price-lane" data-testid="dex-reference-price">
                    <small class="dex-lane-label">Reference</small>
                    <strong class="num">{{ formatMetricPrice(dexReferenceFact(asset).price_usd) }}</strong>
                    <small class="contributors">{{ dexReferenceCaption(asset) }}</small>
                  </span>
                </div>
                <template v-else>
                  <strong class="num">{{ formatMetricPrice(displayPrice(asset)) }}</strong>
                  <small class="contributors">{{ priceCaption(asset) }}</small>
                </template>
              </td>
              <td class="align-right">
                <div v-if="isDexVenue()" class="dex-change-lanes">
                  <span class="dex-change-lane" data-testid="dex-route-change">
                    <small class="dex-lane-label">Route</small>
                    <StatusBadge
                      v-if="dexRouteFact(asset).change_24h_pct.available"
                      :variant="(dexRouteFact(asset).change_24h_pct.value ?? 0) >= 0 ? 'up' : 'down'"
                      :label="formatPercent(dexRouteFact(asset).change_24h_pct.value ?? 0)"
                      :dot="false"
                    />
                    <strong v-else class="unavailable">—</strong>
                  </span>
                  <span class="dex-change-lane" data-testid="dex-reference-change">
                    <small class="dex-lane-label">Reference</small>
                    <StatusBadge
                      v-if="dexReferenceFact(asset).change_24h_pct.available"
                      :variant="(dexReferenceFact(asset).change_24h_pct.value ?? 0) >= 0 ? 'up' : 'down'"
                      :label="formatPercent(dexReferenceFact(asset).change_24h_pct.value ?? 0)"
                      :dot="false"
                    />
                    <strong v-else class="unavailable">—</strong>
                  </span>
                </div>
                <template v-else>
                  <StatusBadge
                    v-if="displayChange(asset).available"
                    :variant="(displayChange(asset).value ?? 0) >= 0 ? 'up' : 'down'"
                    :label="formatPercent(displayChange(asset).value ?? 0)"
                    :dot="false"
                  />
                  <span
                    v-else
                    class="unavailable missing-24h"
                    :title="coverageReasonLabel(asset.coverage_reason || 'missing_24h_reference')"
                  >
                    <strong>—</strong>
                    <small>{{ coverageReasonLabel(asset.coverage_reason || 'missing_24h_reference') }}</small>
                  </span>
                </template>
              </td>
              <td class="align-right num">
                {{ formatMetric(asset.market_cap_usd, FIAT_SYMBOLS[fiat], rate) }}
              </td>
              <td class="align-right num">
                {{ formatMetric(displayTurnover(asset), FIAT_SYMBOLS[fiat], rate) }}
              </td>
              <td class="align-center">
                <button
                  type="button"
                  class="market-count"
                  :aria-label="`Open ${asset.asset_symbol} markets`"
                  @click.stop="openAsset(asset)"
                >
                  {{ marketCountLabel(asset) }}
                  <AppIcon name="chevron-right" :size="13" />
                </button>
              </td>
              <td class="align-center">
                <div v-if="isDexVenue()" class="dex-quality-lanes">
                  <StatusBadge
                    data-testid="dex-route-quality"
                    :variant="priceFactQualityVariant(dexRouteFact(asset))"
                    :label="`Route · ${priceFactQualityLabel(dexRouteFact(asset))}`"
                  />
                  <StatusBadge
                    data-testid="dex-reference-quality"
                    :variant="priceFactQualityVariant(dexReferenceFact(asset))"
                    :label="`Reference · ${priceFactQualityLabel(dexReferenceFact(asset))}`"
                  />
                </div>
                <StatusBadge
                  v-else
                  :variant="assetQualityVariant(asset)"
                  :label="assetQualityLabel(asset)"
                />
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <footer class="table-footer">
        <span class="num">{{ rangeStart }}–{{ rangeEnd }} of {{ total }}</span>
        <div class="pager">
          <label>Rows
            <select v-model.number="pageSize" class="input">
              <option :value="10">10</option>
              <option :value="25">25</option>
              <option :value="50">50</option>
            </select>
          </label>
          <button type="button" class="btn" :disabled="page <= 1" @click="page -= 1">
            <AppIcon name="chevron-left" :size="15" />
          </button>
          <span class="num">{{ page }} / {{ pageCount }}</span>
          <button type="button" class="btn" :disabled="page >= pageCount" @click="page += 1">
            <AppIcon name="chevron-right" :size="15" />
          </button>
        </div>
      </footer>
    </div>

    <div v-if="selectedAssetID" class="asset-drawer-scrim" @click="closeDrawer"></div>
    <aside
      v-if="selectedAssetID"
      class="asset-drawer"
      role="dialog"
      aria-modal="true"
      :aria-label="`${selectedAsset?.asset_symbol ?? 'Asset'} markets`"
    >
      <header class="drawer-header">
        <div v-if="selectedAsset" class="drawer-asset">
          <AssetLogo :src="selectedAsset.logo" :name="selectedAsset.asset_symbol" :size="36" />
          <span>
            <strong>{{ selectedAsset.asset_name }}</strong>
            <small>{{ selectedAsset.asset_symbol }} · {{ selectedVenueLabel }} · {{ selectedAsset.coverage_status }}</small>
          </span>
        </div>
        <div v-else>
          <strong>Asset markets</strong>
          <small>{{ selectedAssetID }}</small>
        </div>
        <button type="button" class="drawer-close-button" aria-label="Close" @click="closeDrawer">
          <AppIcon name="close" :size="18" />
        </button>
      </header>

      <div v-if="drawerLoading" class="drawer-loading">
        <div class="shimmer"></div>
        <div class="shimmer"></div>
      </div>
      <ErrorState v-else-if="drawerError" :message="drawerError" />
      <template v-else>
        <section class="drawer-section">
          <div class="drawer-section-title">
            <h3>Spot Markets</h3>
            <span>{{ spotMarkets.length }}</span>
          </div>
          <article v-for="market in spotMarkets" :key="market.market_id" class="drawer-market">
            <div class="drawer-market-heading">
              <span>
                <strong>{{ market.provider }}</strong>
                <small>{{ market.symbol }}</small>
              </span>
              <StatusBadge
                :variant="providerFreshnessVariant(market.freshness_status)"
                :label="market.freshness_status || 'Unknown'"
              />
            </div>
            <dl>
              <div><dt>Price</dt><dd class="num">{{ market.price.available ? `${formatPrice(market.price.value ?? 0)} ${market.quote_asset}` : '—' }}</dd></div>
              <div><dt>vs composite</dt><dd class="num">{{ market.relative_deviation_pct.available ? formatPercent(market.relative_deviation_pct.value ?? 0) : '—' }}</dd></div>
              <div><dt>24h turnover</dt><dd class="num">{{ market.turnover_24h.available ? `${formatAbbr(market.turnover_24h.value ?? 0)} ${market.quote_asset}` : '—' }}</dd></div>
              <div><dt>24h</dt><dd class="num">{{ market.change_24h_pct.available ? formatPercent(market.change_24h_pct.value ?? 0) : '—' }}</dd></div>
              <div><dt>Composite role</dt><dd>{{ market.confidence === 'excluded' ? 'Excluded' : market.confidence }}</dd></div>
            </dl>
            <button v-if="market.has_kline" type="button" class="btn drawer-chart-button" @click="openMarket(market)">
              Open venue chart <AppIcon name="chevron-right" :size="14" />
            </button>
          </article>
          <p v-if="spotMarkets.length === 0" class="drawer-empty">No enabled spot markets.</p>
        </section>

        <section class="drawer-section">
          <div class="drawer-section-title">
            <h3>Perpetual Markets</h3>
            <span>{{ perpMarkets.length }}</span>
          </div>
          <p class="drawer-disclosure">Perpetual prices are shown for comparison and never contribute to the composite spot index.</p>
          <article v-for="market in perpMarkets" :key="market.market_id" class="drawer-market">
            <div class="drawer-market-heading">
              <span><strong>{{ market.provider }}</strong><small>{{ market.symbol }}</small></span>
              <StatusBadge
                :variant="providerFreshnessVariant(market.freshness_status)"
                :label="market.freshness_status || 'Unknown'"
              />
            </div>
            <dl>
              <div><dt>Price</dt><dd class="num">{{ market.price.available ? `${formatPrice(market.price.value ?? 0)} ${market.quote_asset}` : '—' }}</dd></div>
              <div><dt>vs composite</dt><dd class="num">{{ market.relative_deviation_pct.available ? formatPercent(market.relative_deviation_pct.value ?? 0) : '—' }}</dd></div>
              <div><dt>24h turnover</dt><dd class="num">{{ market.turnover_24h.available ? `${formatAbbr(market.turnover_24h.value ?? 0)} ${market.quote_asset}` : '—' }}</dd></div>
              <div><dt>Index role</dt><dd>Excluded</dd></div>
            </dl>
          </article>
          <p v-if="perpMarkets.length === 0" class="drawer-empty">No perpetual markets.</p>
        </section>

        <section class="drawer-section">
          <div class="drawer-section-title">
            <h3>DEX Routes</h3>
            <span>{{ dexRoutes.length }}</span>
          </div>
          <p class="drawer-disclosure">
            Routes try $10K, then $1K and $100 when larger quotes fail quality limits.
            They are read-only indications, not executable orders or arbitrage signals.
          </p>
          <article v-for="market in dexRoutes" :key="market.route_key" class="drawer-market">
            <div class="drawer-market-heading">
              <span>
                <strong>{{ market.provider }} · {{ market.chain }}</strong>
                <small>{{ market.symbol }}</small>
              </span>
              <StatusBadge
                :variant="market.available ? providerFreshnessVariant(market.freshness_status) : 'offline'"
                :label="market.available ? (market.quality || market.freshness_status) : 'Unavailable'"
              />
            </div>
            <dl>
              <div><dt>{{ quoteNotionalLabel(market) }}</dt><dd class="num">{{ market.price.available ? `$${formatPrice(market.price.value ?? 0)}` : '—' }}</dd></div>
              <div><dt>Protocol path</dt><dd>{{ market.protocol || '—' }}</dd></div>
              <div><dt>Pools</dt><dd class="num">{{ market.pool_addresses.length }}</dd></div>
              <div><dt>Validation</dt><dd>{{ quoteReferenceLabel(market.quote_reference_kind) }}</dd></div>
              <div><dt>vs composite</dt><dd class="num">{{ market.relative_deviation_pct.available ? formatPercent(market.relative_deviation_pct.value ?? 0) : '—' }}</dd></div>
              <div><dt>Quote-side impact</dt><dd class="num">{{ market.price_impact_pct.available ? formatPercent(market.price_impact_pct.value ?? 0) : '—' }}</dd></div>
              <div><dt>Round-trip spread</dt><dd class="num">{{ market.round_trip_spread_pct.available ? formatPercent(market.round_trip_spread_pct.value ?? 0) : '—' }}</dd></div>
              <div><dt>TVL</dt><dd class="num">{{ market.tvl_usd.available ? formatAbbr(market.tvl_usd.value ?? 0, '$') : '—' }}</dd></div>
              <div><dt>Block time</dt><dd class="num">{{ market.block_timestamp ? new Date(market.block_timestamp).toLocaleTimeString() : '—' }}</dd></div>
            </dl>
            <p v-if="market.unavailable_reason" class="route-reason">{{ market.unavailable_reason }}</p>
          </article>
          <p v-if="dexRoutes.length === 0" class="drawer-empty">No reviewed DEX route is available.</p>
        </section>
      </template>
    </aside>
  </section>
</template>

<style scoped>
.fallback-hint {
  margin: -8px 0 12px;
  color: var(--warn);
  font-size: 12px;
}
.market-overview-strip {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-bottom: 22px;
  border: 1px solid var(--border);
  border-radius: var(--radius-card);
  background: var(--bg-panel);
  overflow: hidden;
  box-shadow: var(--shadow-card);
}
.market-overview-strip article {
  display: grid;
  gap: 5px;
  min-width: 0;
  padding: 19px 21px;
  border-right: 1px solid var(--border);
}
.market-overview-strip article:last-child { border-right: 0; }
.market-overview-strip span,
.market-overview-strip small { color: var(--text-3); font-size: 12px; }
.market-overview-strip strong {
  color: var(--text-1);
  font-size: clamp(18px, 1.6vw, 24px);
  font-weight: 600;
  letter-spacing: -.025em;
}
.market-list { overflow: hidden; }
.venue-switcher {
  display: flex;
  align-items: center;
  gap: 18px;
  padding: 16px 20px;
  overflow-x: auto;
  border-bottom: 1px solid var(--border);
}
.venue-group {
  display: flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
}
.venue-group > span {
  color: var(--text-3);
  font-size: 9px;
  font-weight: 650;
  letter-spacing: .08em;
  text-transform: uppercase;
}
.market-list-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 20px;
  border-bottom: 1px solid var(--border);
}
.table-search {
  display: flex;
  align-items: center;
  gap: 8px;
  width: min(360px, 44vw);
  padding: 0 12px;
  color: var(--text-3);
  background: var(--bg-panel-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}
.table-search:focus-within { border-color: var(--accent); }
.table-search input {
  width: 100%;
  padding: 8px 0;
  color: var(--text-1);
  background: transparent;
  border: 0;
  outline: 0;
  font: inherit;
  font-size: 13px;
}
.table-scroll { overflow-x: auto; }
table { width: 100%; min-width: 920px; border-collapse: collapse; font-size: 13px; }
th, td { padding: 11px 13px; border-bottom: 1px solid var(--border); white-space: nowrap; }
th {
  color: var(--text-3);
  text-align: left;
  text-transform: uppercase;
  letter-spacing: .05em;
  font-size: 10px;
  font-weight: 600;
}
.sort-label {
  padding: 0;
  color: inherit;
  background: transparent;
  border: 0;
  cursor: pointer;
  font: inherit;
  text-transform: inherit;
  letter-spacing: inherit;
}
.sort-label:hover { color: var(--text-1); }
.align-right { text-align: right; }
.align-center { text-align: center; }
.asset-row { cursor: pointer; }
.asset-row:hover td { background: #f5f9ff; }
.asset-cell { display: inline-flex; align-items: center; gap: 10px; }
.asset-names { display: flex; flex-direction: column; gap: 1px; }
.asset-name { color: var(--text-1); font-weight: 600; }
.asset-symbol,
.contributors { display: block; color: var(--text-3); font-size: 10px; font-weight: 400; }
.dex-price-lanes,
.dex-change-lanes,
.dex-quality-lanes {
  display: inline-grid;
  gap: 6px;
}
.dex-price-lanes { min-width: 200px; }
.dex-price-lane {
  display: grid;
  grid-template-columns: 58px minmax(110px, 1fr);
  align-items: baseline;
  gap: 1px 8px;
}
.dex-price-lane .contributors { grid-column: 1 / -1; }
.dex-change-lane {
  display: grid;
  grid-template-columns: 58px minmax(64px, auto);
  align-items: center;
  justify-content: end;
  gap: 7px;
}
.dex-quality-lanes { justify-items: stretch; }
.dex-lane-label {
  color: var(--text-3);
  font-size: 9px;
  font-weight: 600;
  letter-spacing: .05em;
  text-transform: uppercase;
}
.rank,
.unavailable { color: var(--text-3); }
.missing-24h {
  display: inline-flex;
  align-items: flex-end;
  flex-direction: column;
  gap: 1px;
}
.missing-24h strong { color: var(--text-2); font-weight: 500; }
.missing-24h small { max-width: 105px; font-size: 9px; }
.market-count {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 5px 8px;
  color: var(--text-2);
  background: var(--bg-panel-2);
  border: 1px solid var(--border);
  border-radius: 999px;
  cursor: pointer;
  font: inherit;
  font-size: 10px;
}
.market-count:hover {
  color: var(--accent);
  background: var(--accent-soft);
  border-color: #bad8f6;
}
.market-empty-state,
.market-loading-state {
  margin: 0;
  padding: 44px 20px;
  color: var(--text-3);
  border-bottom: 1px solid var(--border);
  text-align: center;
  overflow-wrap: anywhere;
}
.market-loading-state { color: var(--text-2); }
.table-footer,
.pager,
.pager label { display: flex; align-items: center; gap: 8px; }
.table-footer {
  justify-content: space-between;
  padding: 10px 16px;
  color: var(--text-3);
  font-size: 12px;
}
.pager .input { padding: 4px 8px; font-size: 12px; }
.pager .btn { padding: 5px 8px; }
.pager .btn:disabled { opacity: .4; cursor: not-allowed; }

.asset-drawer-scrim {
  position: fixed;
  inset: 0;
  z-index: 70;
  background: rgba(29, 29, 31, .22);
  -webkit-backdrop-filter: blur(4px);
  backdrop-filter: blur(4px);
}
.asset-drawer {
  position: fixed;
  inset: 0 0 0 auto;
  z-index: 71;
  width: min(500px, calc(100vw - 28px));
  overflow-y: auto;
  padding: 24px;
  background: rgba(255, 255, 255, .96);
  -webkit-backdrop-filter: saturate(180%) blur(24px);
  backdrop-filter: saturate(180%) blur(24px);
  border-left: 1px solid var(--border);
  box-shadow: var(--shadow-float);
}
.drawer-header,
.drawer-asset,
.drawer-market-heading,
.drawer-market-heading > span,
.drawer-header > div:not(.drawer-asset) { display: flex; }
.drawer-header {
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 18px;
  border-bottom: 1px solid var(--border);
}
.drawer-asset { align-items: center; gap: 12px; }
.drawer-asset > span,
.drawer-market-heading > span,
.drawer-header > div:not(.drawer-asset) { display: flex; flex-direction: column; gap: 2px; }
.drawer-header small,
.drawer-market-heading small { color: var(--text-3); font-size: 11px; }
.drawer-close-button {
  display: inline-flex;
  padding: 7px;
  color: var(--text-2);
  background: var(--bg-panel-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.drawer-section { margin-top: 20px; }
.drawer-section-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}
.drawer-section-title h3 { margin: 0; font-size: 13px; }
.drawer-section-title span { color: var(--text-3); font-size: 11px; }
.drawer-disclosure,
.drawer-empty {
  margin: 0 0 10px;
  color: var(--text-3);
  font-size: 11px;
  line-height: 1.5;
}
.drawer-market {
  margin-bottom: 9px;
  padding: 16px;
  background: #f8fbff;
  border: 1px solid var(--border);
  border-radius: var(--radius-card);
}
.drawer-market-heading { align-items: flex-start; justify-content: space-between; gap: 12px; }
.drawer-market dl {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin: 15px 0 0;
}
.drawer-market dt {
  margin-bottom: 3px;
  color: var(--text-3);
  font-size: 9px;
  text-transform: uppercase;
  letter-spacing: .05em;
}
.drawer-market dd { margin: 0; font-size: 12px; }
.drawer-chart-button { width: 100%; justify-content: center; margin-top: 13px; }
.drawer-loading { display: grid; gap: 10px; margin-top: 18px; }
.drawer-loading .shimmer { height: 130px; }
.route-reason {
  margin: 10px 0 0;
  color: var(--warn);
  font-size: 10px;
}

@media (max-width: 1280px) {
  table { min-width: 820px; }
}
@media (max-width: 900px) {
  .market-overview-strip { grid-template-columns: 1fr 1fr; }
  .market-overview-strip article:nth-child(2) { border-right: 0; }
  .market-overview-strip article:nth-child(-n+2) { border-bottom: 1px solid var(--border); }
  .market-list-toolbar { align-items: stretch; flex-direction: column; }
  .table-search { width: 100%; }
}
</style>
