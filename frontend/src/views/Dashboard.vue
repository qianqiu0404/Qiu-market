<script setup lang="ts">
import { computed, onUnmounted, watch } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import StatCard from '../components/StatCard.vue'
import StatusBadge from '../components/StatusBadge.vue'
import AssetLogo from '../components/AssetLogo.vue'
import Sparkline from '../components/Sparkline.vue'
import SkeletonRows from '../components/SkeletonRows.vue'
import EmptyState from '../components/EmptyState.vue'
import ErrorState from '../components/ErrorState.vue'
import {
  getSystemOverview,
  getAllSymbols,
  getSupportAssets,
  getKlines,
  getTopMovers,
  type SupportAsset,
  type SymbolInfo,
  type TopMoversDirection,
  type TopMoversResult,
} from '../api/market'
import { usePolling, type PollingResult } from '../composables/usePolling'
import {
  formatAbbr,
  formatDelay,
  formatPercent,
  formatPrice,
  freshnessFromDelay,
  isHealthyStatus,
} from '../utils/format'

/* ===== System overview ===== */
const overview = usePolling(getSystemOverview, { interval: 30_000 })

const freshness = computed(() =>
  freshnessFromDelay(overview.data.value?.data_delay_seconds ?? null, Boolean(overview.error.value)),
)

const freshnessTone = computed<'up' | 'warn' | 'down' | 'default'>(() => {
  switch (freshness.value) {
    case 'live':
      return 'up'
    case 'delayed':
      return 'warn'
    case 'stale':
      return 'down'
    default:
      return 'default'
  }
})

/* ===== Top assets (BTC / ETH / SOL) resolved from real APIs ===== */
interface TopAsset {
  key: string
  symbolName: string
  name: string
  logo: string
  price: number
  change24h: number
  spark: number[]
}

const WANTED = ['BTC/USDT', 'ETH/USDT', 'SOL/USDT']

function findAssetForSymbol(symbol: SymbolInfo, assets: SupportAsset[]): SupportAsset | undefined {
  return (
    assets.find((a) => a.guid === symbol.base_asset_id) ??
    assets.find((a) => a.asset_symbol === symbol.base_asset)
  )
}

async function loadTopAssets(): Promise<TopAsset[]> {
  const [symbols, assets] = await Promise.all([getAllSymbols(), getSupportAssets()])
  const picked = WANTED.map((name) => symbols.find((s) => s.symbol_name === name)).filter(
    (s): s is SymbolInfo => Boolean(s),
  )
  if (picked.length === 0) return []
  return Promise.all(
    picked.map(async (symbol): Promise<TopAsset> => {
      const klines = await getKlines(symbol.guid, '1h', 30, 'symbol_guid')
      const closes = klines.map((k) => k.close).filter((c) => c > 0)
      const last = closes.length > 0 ? closes[closes.length - 1]! : 0
      const ref = closes.length >= 25 ? closes[closes.length - 25]! : closes[0]!
      const change24h = ref > 0 ? ((last - ref) / ref) * 100 : 0
      const asset = findAssetForSymbol(symbol, assets)
      return {
        key: symbol.guid,
        symbolName: symbol.symbol_name,
        name: asset?.asset_name ?? symbol.base_asset,
        logo: asset?.asset_logo ?? '',
        price: last,
        change24h,
        spark: closes,
      }
    }),
  )
}

const topAssets = usePolling(loadTopAssets, { interval: 30_000 })

/* ===== Top movers (24h 涨跌幅榜, Redis ZSET 榜单接口) ===== */
interface MoverPanel {
  key: TopMoversDirection
  title: string
  state: PollingResult<TopMoversResult>
}

const moverPanels: MoverPanel[] = [
  { key: 'gainers', title: '涨幅榜', state: usePolling(() => getTopMovers('gainers', 5), { interval: 30_000 }) },
  { key: 'losers', title: '跌幅榜', state: usePolling(() => getTopMovers('losers', 5), { interval: 30_000 }) },
]
const moverWarmupTimers: number[] = []
const moverWarmupAttempts = new Map<TopMoversDirection, number>()
for (const panel of moverPanels) {
  watch(
    () => panel.state.data.value,
    (data) => {
      if (!data || data.items.some((item) => item.change_available)) {
        moverWarmupAttempts.set(panel.key, 0)
        return
      }
      const attempts = moverWarmupAttempts.get(panel.key) ?? 0
      if (attempts >= 3) return
      moverWarmupAttempts.set(panel.key, attempts + 1)
      const timer = window.setTimeout(() => void panel.state.refresh(), 2_000)
      moverWarmupTimers.push(timer)
    },
  )
}
onUnmounted(() => moverWarmupTimers.forEach((timer) => window.clearTimeout(timer)))

/* ===== System health cards ===== */
interface HealthCard {
  key: string
  label: string
  status: string
  healthy: boolean
}

const healthCards = computed<HealthCard[]>(() => {
  const ov = overview.data.value
  if (!ov) return []
  const entries: Array<[string, string]> = [
    ['Crawler', ov.crawler_status],
    ['Worker', ov.worker_status],
    ['Redis', ov.redis_status],
    ['Database', ov.database_status],
    ['API', ov.api_status],
  ]
  return entries.map(([label, status]) => ({
    key: label,
    label,
    status: status || 'unknown',
    healthy: isHealthyStatus(status),
  }))
})
</script>

<template>
  <section>
    <PageHeader
      title="Dashboard"
      subtitle="Platform-wide market data overview"
      :freshness="freshness"
    />

    <!-- Stat strip -->
    <SkeletonRows v-if="overview.loading.value" variant="cards" :rows="4" />
    <ErrorState
      v-else-if="overview.error.value && !overview.data.value"
      :message="overview.error.value"
      @retry="overview.refresh"
    />
    <div v-else-if="overview.data.value" class="stat-strip">
      <StatCard
        label="Total Market Cap"
        :value="formatAbbr(overview.data.value.total_market_cap, '$')"
      />
      <StatCard label="24h Volume" :value="formatAbbr(overview.data.value.total_volume, '$')" />
      <StatCard
        label="Assets / Symbols"
        :value="`${overview.data.value.asset_count} / ${overview.data.value.symbol_count}`"
      >
        <span class="num">{{ overview.data.value.market_count }} markets</span>
        <span class="sep">·</span>
        <span class="num">{{ overview.data.value.exchange_count }} exchanges</span>
      </StatCard>
      <StatCard
        label="Data Freshness"
        :value="formatDelay(overview.data.value.data_delay_seconds)"
        :tone="freshnessTone"
      >
        <StatusBadge :variant="freshness" />
      </StatCard>
    </div>

    <!-- Top assets -->
    <h2 class="section-title">Top Assets</h2>
    <SkeletonRows v-if="topAssets.loading.value" variant="cards" :rows="3" />
    <EmptyState
      v-else-if="topAssets.error.value || !topAssets.data.value || topAssets.data.value.length === 0"
      title="Top assets unavailable"
      :message="topAssets.error.value ?? 'Could not resolve BTC/ETH/SOL symbols from the API.'"
    >
      <button v-if="topAssets.error.value" type="button" class="btn" @click="topAssets.refresh">
        Retry
      </button>
    </EmptyState>
    <div v-else class="top-assets">
      <div v-for="asset in topAssets.data.value" :key="asset.key" class="card card-pad top-asset">
        <div class="top-asset-head">
          <AssetLogo :src="asset.logo" :name="asset.symbolName" :size="34" />
          <div class="top-asset-name">
            <span class="top-asset-symbol">{{ asset.symbolName }}</span>
            <span class="top-asset-fullname">{{ asset.name }}</span>
          </div>
          <Sparkline :points="asset.spark" :width="110" :height="34" />
        </div>
        <div class="top-asset-figures">
          <span class="top-asset-price num">${{ formatPrice(asset.price) }}</span>
          <StatusBadge
            :variant="asset.change24h >= 0 ? 'up' : 'down'"
            :label="formatPercent(asset.change24h)"
            :dot="false"
          />
        </div>
      </div>
    </div>

    <!-- Top movers (24h) -->
    <h2 class="section-title">Top Movers（24h）</h2>
    <div class="movers-grid">
      <div v-for="panel in moverPanels" :key="panel.key" class="card card-pad mover-panel">
        <div class="mover-panel-title">{{ panel.title }}</div>
        <SkeletonRows v-if="panel.state.loading.value" :rows="5" />
        <ErrorState
          v-else-if="panel.state.error.value && !panel.state.data.value"
          :message="panel.state.error.value"
          @retry="panel.state.refresh"
        />
        <EmptyState
          v-else-if="!panel.state.data.value || panel.state.data.value.items.length === 0"
          title="No movers yet"
          message="Ranking data is not available yet. The crawler updates it on every ticker tick."
        />
        <ul v-else class="mover-list">
          <li v-for="m in panel.state.data.value.items" :key="`${m.rank}-${m.symbol}`" class="mover-row">
            <span class="mover-rank num">{{ m.rank }}</span>
            <AssetLogo :src="m.logo" :name="m.symbol" :size="26" />
            <div class="mover-names">
              <span class="mover-symbol">{{ m.symbol }}</span>
              <span class="mover-name">{{ m.name }}</span>
            </div>
            <span class="mover-price num">${{ formatPrice(m.price) }}</span>
            <StatusBadge
              v-if="m.change_available"
              :variant="m.change24h >= 0 ? 'up' : 'down'"
              :label="formatPercent(m.change24h)"
              :dot="false"
            />
            <span v-else class="muted">—</span>
          </li>
        </ul>
      </div>
    </div>

    <!-- System health -->
    <h2 class="section-title">System Health</h2>
    <SkeletonRows v-if="overview.loading.value" variant="cards" :rows="5" />
    <div v-else-if="healthCards.length > 0" class="health-grid">
      <div v-for="card in healthCards" :key="card.key" class="card card-pad health-card">
        <span class="health-name">{{ card.label }}</span>
        <StatusBadge
          :variant="card.healthy ? 'live' : 'error'"
          :label="card.status"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.stat-strip {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 14px;
}

.sep {
  color: var(--text-3);
}

.section-title {
  font-size: 15px;
  margin: 28px 0 12px;
  color: var(--text-1);
}

.top-assets {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 14px;
}

.top-asset {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.top-asset-head {
  display: flex;
  align-items: center;
  gap: 12px;
}

.top-asset-name {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.top-asset-symbol {
  font-weight: 600;
  font-size: 14px;
}

.top-asset-fullname {
  font-size: 12px;
  color: var(--text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.top-asset-figures {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.top-asset-price {
  font-size: 22px;
  font-weight: 600;
}

.movers-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 14px;
}

.mover-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.mover-panel-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-2);
}

.mover-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}

.mover-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 0;
  border-bottom: 1px solid var(--border);
}

.mover-row:last-child {
  border-bottom: none;
}

.mover-rank {
  width: 18px;
  text-align: center;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-3);
  flex: none;
}

.mover-names {
  display: flex;
  flex-direction: column;
  min-width: 0;
  flex: 1;
}

.mover-symbol {
  font-size: 13px;
  font-weight: 600;
}

.mover-name {
  font-size: 12px;
  color: var(--text-3);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mover-price {
  font-size: 13px;
  flex: none;
}

.health-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 14px;
}

.health-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: flex-start;
}

.health-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-2);
}
</style>
