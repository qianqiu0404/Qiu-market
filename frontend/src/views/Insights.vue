<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart, ScatterChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import PageHeader from '../components/PageHeader.vue'
import StatCard from '../components/StatCard.vue'
import StatusBadge from '../components/StatusBadge.vue'
import ErrorState from '../components/ErrorState.vue'
import EmptyState from '../components/EmptyState.vue'
import SkeletonRows from '../components/SkeletonRows.vue'
import { usePolling } from '../composables/usePolling'
import {
  getAssetMomentum,
  getMarketInsights,
  getSystemOverview,
  getTop50VenueInsights,
  type AssetMomentumItem,
  type MomentumWindow,
} from '../api/market'
import {
  formatAbbr,
  formatDelay,
  formatPercent,
  formatPrice,
  freshnessFromDelay,
  isHealthyStatus,
} from '../utils/format'

echarts.use([BarChart, ScatterChart, GridComponent, TooltipComponent, CanvasRenderer])

const WINDOWS: Array<{ value: MomentumWindow; label: string }> = [
  { value: '24h', label: '24H' },
  { value: '7d', label: '7D' },
  { value: '30d', label: '30D' },
]
const windowValue = ref<MomentumWindow>('7d')

const realtime = usePolling(getMarketInsights, { interval: 30_000 })
const venues = usePolling(getTop50VenueInsights, { interval: 30_000 })
async function getAvailableMomentum() {
  const system = await getSystemOverview()
  if (!isHealthyStatus(system.dw_status)) {
    throw new Error(
      `Historical momentum is unavailable because Doris is ${system.dw_status || 'not configured'}.`,
    )
  }
  return getAssetMomentum(windowValue.value)
}
const momentum = usePolling(getAvailableMomentum, { interval: 60_000 })
watch(windowValue, () => void momentum.refresh())

const breadth = computed(() => realtime.data.value?.breadth)
const crossVenue = computed(() => realtime.data.value?.cross_venue ?? [])
const venueCoverage = computed(() => venues.data.value?.coverage ?? [])
const cexDispersion = computed(() => venues.data.value?.dispersion.slice(0, 10) ?? [])
const dexRouteMonitor = computed(() => venues.data.value?.dex_routes ?? [])
// Never render a previously successful historical snapshot without an error
// marker after its availability gate or refresh fails.
const momentumItems = computed(() =>
  momentum.error.value ? [] : momentum.data.value?.items ?? [])
const chartMomentum = computed(() => momentumItems.value.filter((item) => !item.low_coverage))
const lowCoverageCount = computed(
  () => momentumItems.value.filter((item) => item.low_coverage).length,
)
const distributionEl = ref<HTMLDivElement | null>(null)
const momentumEl = ref<HTMLDivElement | null>(null)
let distributionChart: echarts.ECharts | null = null
let momentumChart: echarts.ECharts | null = null

function renderDistribution(): void {
  if (!distributionChart && distributionEl.value) distributionChart = echarts.init(distributionEl.value)
  if (!distributionChart) return
  const rows = realtime.data.value?.distribution ?? []
  distributionChart.setOption(
    {
      animation: false,
      grid: {
        left: 8,
        right: 8,
        top: 18,
        bottom: 8,
        outerBoundsMode: 'same',
        outerBoundsContain: 'axisLabel',
      },
      xAxis: {
        type: 'category',
        data: rows.map((row) => row.label),
        axisTick: { show: false },
        axisLine: { lineStyle: { color: '#D2D2D7' } },
        axisLabel: { color: '#6E6E73', fontSize: 10, interval: 0 },
      },
      yAxis: {
        type: 'value',
        minInterval: 1,
        axisLabel: { color: '#6E6E73', fontSize: 10 },
        splitLine: { lineStyle: { color: '#ECECF0' } },
      },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        backgroundColor: '#FFFFFF',
        borderColor: '#D2D2D7',
        textStyle: { color: '#1D1D1F', fontSize: 12 },
        formatter: (params: unknown) => {
          const list = Array.isArray(params) ? (params as Array<{ dataIndex: number }>) : []
          const row = rows[list[0]?.dataIndex ?? -1]
          return row ? `${row.label}<br/><strong>${row.count}</strong> assets` : ''
        },
      },
      series: [
        {
          type: 'bar',
          data: rows.map((row, index) => ({
            value: row.count,
            itemStyle: {
              color: index < 4 ? '#D92D4C' : index === 4 ? '#86868B' : '#16825D',
              opacity: 0.85,
              borderRadius: [3, 3, 0, 0],
            },
          })),
          barMaxWidth: 46,
        },
      ],
    },
    { notMerge: true },
  )
}

function renderMomentum(): void {
  if (!momentumChart && momentumEl.value) momentumChart = echarts.init(momentumEl.value)
  if (!momentumChart) return
  const rows = chartMomentum.value
  momentumChart.setOption(
    {
      animation: false,
      grid: {
        left: 12,
        right: 18,
        top: 20,
        bottom: 10,
        outerBoundsMode: 'same',
        outerBoundsContain: 'axisLabel',
      },
      xAxis: {
        type: 'value',
        name: 'Volatility %',
        nameTextStyle: { color: '#6E6E73', fontSize: 10 },
        axisLabel: { color: '#6E6E73', fontSize: 10, formatter: '{value}%' },
        splitLine: { lineStyle: { color: '#ECECF0' } },
      },
      yAxis: {
        type: 'value',
        name: 'Return %',
        nameTextStyle: { color: '#6E6E73', fontSize: 10 },
        axisLabel: { color: '#6E6E73', fontSize: 10, formatter: '{value}%' },
        splitLine: { lineStyle: { color: '#ECECF0' } },
      },
      tooltip: {
        trigger: 'item',
        backgroundColor: '#FFFFFF',
        borderColor: '#D2D2D7',
        textStyle: { color: '#1D1D1F', fontSize: 12 },
        formatter: (params: unknown) => {
          const value = (params as { data?: { row?: AssetMomentumItem } })?.data?.row
          if (!value) return ''
          return [
            `<strong>${value.asset_symbol}</strong> · ${value.exchange}`,
            `Return ${formatPercent(value.return_pct)}`,
            `Volatility ${formatPercent(value.volatility_pct)}`,
            `Range ${formatPercent(value.high_low_range_pct)}`,
            `Coverage ${value.coverage_pct.toFixed(1)}%`,
          ].join('<br/>')
        },
      },
      series: [
        {
          type: 'scatter',
          symbolSize: 12,
          data: rows.map((row) => ({
            value: [row.volatility_pct, row.return_pct],
            row,
            itemStyle: { color: row.return_pct >= 0 ? '#16825D' : '#D92D4C' },
          })),
        },
      ],
    },
    { notMerge: true },
  )
}

watch(() => realtime.data.value?.distribution, renderDistribution, { deep: true })
watch(chartMomentum, renderMomentum)
watch(distributionEl, (value) => value && renderDistribution())
watch(momentumEl, (value) => {
  if (!value) {
    momentumChart?.dispose()
    momentumChart = null
    return
  }
  renderMomentum()
})

function resizeCharts(): void {
  distributionChart?.resize()
  momentumChart?.resize()
}
onMounted(() => window.addEventListener('resize', resizeCharts))
onUnmounted(() => {
  window.removeEventListener('resize', resizeCharts)
  distributionChart?.dispose()
  momentumChart?.dispose()
})

function signedClass(value: number): string {
  return value >= 0 ? 'up-text' : 'down-text'
}
</script>

<template>
  <section>
    <PageHeader
      title="Insights"
      subtitle="Market breadth, spot–perp comparison and fixed-window historical momentum"
      :refreshed-at="realtime.lastUpdated.value"
    />

    <section class="insight-section">
      <div class="section-heading">
        <div>
          <h2>Provider Selection Coverage</h2>
          <p>Every provider owns a stable reviewed selection. CEX targets 50; DEX shows only the real assets it can qualify. All remains the deduplicated CEX union.</p>
        </div>
      </div>
      <ErrorState v-if="venues.error.value && !venues.data.value" :message="venues.error.value" @retry="venues.refresh" />
      <SkeletonRows v-else-if="venues.loading.value && !venues.data.value" variant="cards" :rows="4" />
      <template v-else>
        <div class="coverage-grid">
          <article v-for="row in venueCoverage" :key="row.venue" class="card coverage-card">
            <span>{{ row.venue }}</span>
            <strong v-if="row.available" class="num">{{ row.priced }} / {{ row.total }}</strong>
            <strong v-else class="unavailable">Unavailable</strong>
            <small>{{ row.available ? `${row.coverage_pct.toFixed(1)}% ${row.coverage_kind}` : row.error }}</small>
          </article>
        </div>
        <div class="monitor-grid">
          <div class="table-card card">
            <div class="monitor-title">
              <strong>CEX Quote Dispersion</strong>
              <small>max minus min, relative to midpoint</small>
            </div>
            <div class="table-scroll">
              <table class="compact-table">
                <thead><tr><th>Asset</th><th>Venues</th><th class="align-right">Dispersion</th></tr></thead>
                <tbody v-if="cexDispersion.length">
                  <tr v-for="row in cexDispersion" :key="row.asset_id">
                    <td class="asset-symbol">{{ row.asset_symbol }}</td>
                    <td class="num">{{ row.venue_count }}</td>
                    <td class="align-right num">{{ formatPercent(row.dispersion_pct, 3) }}</td>
                  </tr>
                </tbody>
                <tbody v-else><tr><td colspan="3" class="unavailable">No asset has two fresh CEX quotes yet.</td></tr></tbody>
              </table>
            </div>
          </div>
          <div class="table-card card">
            <div class="monitor-title">
              <strong>DEX Route Monitor</strong>
              <small>tiered $10K / $1K / $100 indicative routes; not an arbitrage signal</small>
            </div>
            <div class="table-scroll">
              <table class="compact-table">
                <thead><tr><th>Asset</th><th>Provider</th><th class="align-right">Routes</th><th class="align-right">Quality</th></tr></thead>
                <tbody v-if="dexRouteMonitor.length">
                  <tr v-for="row in dexRouteMonitor" :key="`${row.provider}:${row.asset_id}`">
                    <td class="asset-symbol">{{ row.asset_symbol }}</td>
                    <td>{{ row.provider }}</td>
                    <td class="align-right num">{{ row.route_count }}</td>
                    <td class="align-right"><StatusBadge :variant="row.available ? 'live' : 'stale'" :label="row.quality" /></td>
                  </tr>
                </tbody>
                <tbody v-else><tr><td colspan="4" class="unavailable">No reviewed DEX route is publishing a quote.</td></tr></tbody>
              </table>
            </div>
          </div>
        </div>
      </template>
    </section>

    <section class="insight-section">
      <div class="section-heading">
        <div>
          <h2>24h Market Breadth · Full Catalog</h2>
          <p>Broader active-market catalog, independent from the four CEX selection union. One reference-market vote per asset; missing changes remain Unknown.</p>
        </div>
      </div>
      <ErrorState v-if="realtime.error.value && !breadth" :message="realtime.error.value" @retry="realtime.refresh" />
      <template v-else>
        <SkeletonRows v-if="realtime.loading.value && !breadth" variant="cards" :rows="4" />
        <div v-else-if="breadth" class="breadth-grid">
          <StatCard label="Assets" :value="breadth.asset_count" :hint="`${breadth.unknown} unknown`" />
          <StatCard label="Advancing" :value="breadth.advancers" :hint="`${breadth.advance_ratio.toFixed(1)}% of known`" tone="up" />
          <StatCard label="Declining" :value="breadth.decliners" :hint="`${breadth.flat} unchanged`" tone="down" />
          <StatCard label="Median 24h" :value="formatPercent(breadth.median_change24h)" hint="Asset-level median" tone="accent" />
          <StatCard label="24h Turnover" :value="formatAbbr(breadth.turnover24h, '$')" hint="USD-family not FX-normalized" />
        </div>
        <div v-if="breadth" class="chart-card card">
          <div class="chart-caption">
            <span>Change distribution</span>
            <span>{{ breadth.advancers + breadth.decliners + breadth.flat }} known assets</span>
          </div>
          <div ref="distributionEl" class="distribution-chart"></div>
        </div>
      </template>
    </section>

    <section class="insight-section">
      <div class="section-heading">
        <div>
          <h2>Cross-Venue Monitor</h2>
          <p>Indicative spot–perp comparison for shared assets. This is not an executable arbitrage price.</p>
        </div>
      </div>
      <ErrorState v-if="realtime.error.value && crossVenue.length === 0" :message="realtime.error.value" @retry="realtime.refresh" />
      <SkeletonRows v-else-if="realtime.loading.value && crossVenue.length === 0" :rows="6" />
      <EmptyState v-else-if="crossVenue.length === 0" title="No shared assets" message="No active asset currently has both a spot and perpetual market." />
      <div v-else class="table-card card">
        <div class="table-scroll">
          <table>
            <thead>
              <tr>
                <th>Asset</th>
                <th>Spot</th>
                <th>Perp</th>
                <th class="align-right">Indicative Spread</th>
                <th class="align-right">24h Change Gap</th>
                <th class="align-right">Turnover Share</th>
                <th class="align-center">Freshness</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in crossVenue" :key="row.asset_id">
                <td class="asset-symbol">{{ row.asset_symbol }}</td>
                <td>
                  <span class="venue-price">{{ row.spot_exchange }} · {{ row.spot_quote_asset }}</span>
                  <span class="num sub-value">${{ formatPrice(row.spot_price) }}</span>
                </td>
                <td>
                  <span class="venue-price">{{ row.perp_exchange }} · {{ row.perp_quote_asset }}</span>
                  <span class="num sub-value">${{ formatPrice(row.perp_price) }}</span>
                </td>
                <td class="align-right">
                  <span v-if="row.spread_available" class="num" :class="signedClass(row.indicative_spread_pct)">
                    {{ formatPercent(row.indicative_spread_pct, 3) }}
                  </span>
                  <span v-else class="unavailable">Unavailable</span>
                </td>
                <td class="align-right">
                  <span v-if="row.change_gap_available" class="num" :class="signedClass(row.change_gap_pct_points)">
                    {{ row.change_gap_pct_points > 0 ? '+' : '' }}{{ row.change_gap_pct_points.toFixed(2) }} pp
                  </span>
                  <span v-else class="unavailable">—</span>
                </td>
                <td class="align-right share-cell">
                  <span class="num">Spot {{ row.spot_turnover_share.toFixed(1) }}%</span>
                  <span class="num">Perp {{ row.perp_turnover_share.toFixed(1) }}%</span>
                </td>
                <td class="align-center freshness-pair">
                  <StatusBadge :variant="freshnessFromDelay(row.spot_delay_seconds)" :label="`S ${formatDelay(row.spot_delay_seconds)}`" :dot="false" />
                  <StatusBadge :variant="freshnessFromDelay(row.perp_delay_seconds)" :label="`P ${formatDelay(row.perp_delay_seconds)}`" :dot="false" />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </section>

    <section class="insight-section">
      <div class="section-heading momentum-heading">
        <div>
          <h2>Historical Momentum</h2>
          <p>Closed 1h candles only. Coverage below 90% stays in the table but is excluded from the scatter plot.</p>
        </div>
        <div class="segmented" role="group" aria-label="Momentum window">
          <button
            v-for="option in WINDOWS"
            :key="option.value"
            type="button"
            :class="{ active: windowValue === option.value }"
            @click="windowValue = option.value"
          >
            {{ option.label }}
          </button>
        </div>
      </div>
      <ErrorState v-if="momentum.error.value" :message="momentum.error.value" @retry="momentum.refresh" />
      <SkeletonRows v-else-if="momentum.loading.value && momentumItems.length === 0" :rows="6" />
      <EmptyState
        v-else-if="momentumItems.length === 0"
        title="Historical module has no data"
        message="Doris may be unavailable or the selected closed-candle window has not been synchronized yet. Real-time breadth remains independent."
      />
      <template v-else>
        <div class="chart-card card">
          <div class="chart-caption">
            <span>Return × volatility</span>
            <span>{{ chartMomentum.length }} plotted · {{ lowCoverageCount }} low coverage</span>
          </div>
          <div v-if="chartMomentum.length > 0" ref="momentumEl" class="momentum-chart"></div>
          <EmptyState
            v-else
            title="No assets meet the coverage threshold"
            message="The table remains available below. Assets need at least 90% of expected closed 1h candles before they appear in this comparison."
          />
        </div>
        <div class="table-card card momentum-table">
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Asset</th>
                  <th>Reference Market</th>
                  <th class="align-right">Return</th>
                  <th class="align-right">1h Volatility</th>
                  <th class="align-right">High–Low Range</th>
                  <th class="align-right">Coverage</th>
                  <th class="align-right">Candles</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in momentumItems" :key="row.asset_id">
                  <td class="asset-symbol">{{ row.asset_symbol }}</td>
                  <td>{{ row.exchange }} · {{ row.market_code }}</td>
                  <td class="align-right num" :class="signedClass(row.return_pct)">{{ formatPercent(row.return_pct) }}</td>
                  <td class="align-right num">{{ formatPercent(row.volatility_pct) }}</td>
                  <td class="align-right num">{{ formatPercent(row.high_low_range_pct) }}</td>
                  <td class="align-right">
                    <StatusBadge
                      :variant="row.low_coverage ? 'delayed' : 'live'"
                      :label="row.low_coverage ? `Low ${row.coverage_pct.toFixed(1)}%` : `${row.coverage_pct.toFixed(1)}%`"
                      :dot="false"
                    />
                  </td>
                  <td class="align-right num">{{ row.candle_count }} / {{ row.expected_candles }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>
    </section>
  </section>
</template>

<style scoped>
.insight-section { margin-bottom: 34px; }
.section-heading {
  display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; margin-bottom: 12px;
}
.section-heading h2 { font-size: 16px; color: var(--text-1); }
.section-heading p { margin: 4px 0 0; color: var(--text-3); font-size: 12px; }
.breadth-grid {
  display: grid; grid-template-columns: repeat(5, minmax(150px, 1fr)); gap: 12px; margin-bottom: 12px;
}
.coverage-grid {
  display: grid;
  grid-template-columns: repeat(8, minmax(110px, 1fr));
  gap: 8px;
  margin-bottom: 12px;
}
.coverage-card {
  display: grid;
  gap: 5px;
  padding: 12px;
}
.coverage-card span {
  color: var(--text-3);
  font-size: 10px;
  text-transform: capitalize;
}
.coverage-card strong { font-size: 16px; }
.coverage-card small { color: var(--text-3); font-size: 10px; }
.monitor-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}
.monitor-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--border);
}
.monitor-title strong { font-size: 12px; }
.monitor-title small { color: var(--text-3); font-size: 10px; }
.compact-table { min-width: 0; }
.chart-card { padding: 12px 14px 8px; }
.chart-caption {
  display: flex; align-items: center; justify-content: space-between; gap: 12px;
  color: var(--text-3); font-size: 11px; text-transform: uppercase; letter-spacing: .04em;
}
.distribution-chart { width: 100%; height: 250px; }
.momentum-chart { width: 100%; height: 360px; }
.table-card { overflow: hidden; }
.table-scroll { overflow-x: auto; }
table { width: 100%; min-width: 860px; border-collapse: collapse; font-size: 13px; }
th, td { padding: 11px 14px; border-bottom: 1px solid var(--border); white-space: nowrap; }
th {
  color: var(--text-3); text-align: left; text-transform: uppercase; letter-spacing: .05em;
  font-size: 11px; font-weight: 600;
}
tbody tr:last-child td { border-bottom: 0; }
tbody tr:hover td { background: #f8fbff; }
.align-right { text-align: right; }
.align-center { text-align: center; }
.asset-symbol { font-weight: 700; }
.venue-price, .sub-value { display: block; }
.venue-price { color: var(--text-2); }
.sub-value { color: var(--text-3); font-size: 11px; }
.share-cell span, .freshness-pair :deep(.badge) { display: block; }
.share-cell span + span { margin-top: 2px; color: var(--text-3); }
.freshness-pair { display: flex; justify-content: center; gap: 5px; }
.unavailable { color: var(--text-3); font-size: 11px; }
.up-text { color: var(--up); }
.down-text { color: var(--down); }
.momentum-table { margin-top: 12px; }
@media (max-width: 1400px) {
  .breadth-grid { grid-template-columns: repeat(3, minmax(160px, 1fr)); }
  .coverage-grid { grid-template-columns: repeat(4, minmax(120px, 1fr)); }
}
@media (max-width: 1100px) {
  .breadth-grid { grid-template-columns: repeat(2, minmax(180px, 1fr)); }
  .monitor-grid { grid-template-columns: 1fr; }
}
@media (max-width: 767px) {
  .breadth-grid { grid-template-columns: 1fr; }
  .coverage-grid { grid-template-columns: repeat(2, minmax(120px, 1fr)); }
  .momentum-heading { align-items: flex-start; flex-direction: column; }
  .distribution-chart { height: 220px; }
  .momentum-chart { height: 280px; }
}
</style>
