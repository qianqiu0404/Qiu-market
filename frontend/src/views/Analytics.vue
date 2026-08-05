<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import PageHeader from '../components/PageHeader.vue'
import StatCard from '../components/StatCard.vue'
import StatusBadge from '../components/StatusBadge.vue'
import SkeletonRows from '../components/SkeletonRows.vue'
import EmptyState from '../components/EmptyState.vue'
import ErrorState from '../components/ErrorState.vue'
import {
  getKlineAnalytics,
  type KlineAnalyticsItem,
  type KlineInterval,
} from '../api/market'
import { usePolling } from '../composables/usePolling'
import { formatAbbr, formatPercent, formatPrice, type Freshness } from '../utils/format'

/* Tree-shaken echarts, same pattern as Klines.vue: bar chart + grid + tooltip only. */
echarts.use([BarChart, GridComponent, TooltipComponent, CanvasRenderer])

const UP = '#16825D'
const ACCENT = '#0071E3'
const INTERVALS: KlineInterval[] = ['1m', '15m', '1h', '1d']
const CHART_TOP_N = 10

const interval = ref<KlineInterval>('1h')

/* Doris analytics are batch-refreshed by the dw process every 60s, so polling
 * faster than that would just re-read the same snapshot. */
const { data, loading, error, refresh } = usePolling(
  () => getKlineAnalytics(interval.value, 20),
  { interval: 60_000 },
)

watch(interval, () => {
  void refresh()
})

const items = computed<KlineAnalyticsItem[]>(() => data.value?.items ?? [])

/* The analytics API resolves guid -> symbol_name server-side (symbol/asset
 * tables); fall back to the raw guid only if mapping failed upstream. */
function symbolLabel(row: KlineAnalyticsItem): string {
  return row.symbol_name || row.symbol_guid
}

/* Plain-language explanation of what this page computes, shown under the
 * header so the metrics are not a black box. */
const METRIC_NOTES: Array<[string, string]> = [
  ['Change', 'last close vs first open of the window, in %'],
  ['Volatility', 'stddev of close-to-close returns, in %'],
  ['Total / Avg Volume', 'sum / mean of candle volumes in the window'],
  ['High / Low', 'highest high and lowest low of the window'],
  ['Window', 'all klines synced into Doris for the selected interval'],
]

const freshness = computed<Freshness>(() => {
  if (error.value) return 'offline'
  return items.value.length > 0 ? 'live' : 'stale'
})

/* ===== Summary cards ===== */
const totalVolume = computed(() => items.value.reduce((sum, i) => sum + i.total_volume, 0))
const avgVolatility = computed(() => {
  const withVol = items.value.filter((i) => i.candle_count > 1)
  if (withVol.length === 0) return 0
  return withVol.reduce((sum, i) => sum + i.volatility_pct, 0) / withVol.length
})
const topGainer = computed<KlineAnalyticsItem | null>(() => {
  if (items.value.length === 0) return null
  return items.value.reduce((best, i) => (i.price_change_pct > best.price_change_pct ? i : best))
})

/* ===== Bar chart: top N symbols by total volume ===== */
const chartEl = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null

function buildOption(rows: KlineAnalyticsItem[]): echarts.EChartsCoreOption {
  const top = rows.slice(0, CHART_TOP_N)
  return {
    animation: false,
    backgroundColor: 'transparent',
    grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
    xAxis: {
      type: 'category',
      data: top.map((r) => symbolLabel(r)),
      axisLine: { lineStyle: { color: '#D2D2D7' } },
      axisLabel: { color: '#6E6E73', fontSize: 10, hideOverlap: true },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: '#6E6E73', fontSize: 10, formatter: (v: number) => formatAbbr(v) },
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
        const row = top[list[0]?.dataIndex ?? -1]
        if (!row) return ''
        const lines = [
          ['Total volume', formatAbbr(row.total_volume)],
          ['Avg volume', formatAbbr(row.avg_volume)],
          ['Candles', String(row.candle_count)],
        ]
          .map(
            ([label, value]) =>
              `<div style="display:flex;justify-content:space-between;gap:16px"><span style="color:#6E6E73">${label}</span><span>${value}</span></div>`,
          )
          .join('')
        return `<div style="margin-bottom:4px;color:#6E6E73">${symbolLabel(row)}</div>${lines}`
      },
    },
    series: [
      {
        type: 'bar',
        name: 'Total volume',
        data: top.map((r) => r.total_volume),
        barMaxWidth: 28,
        itemStyle: { color: ACCENT, borderRadius: [3, 3, 0, 0] },
      },
    ],
  }
}

function renderChart(): void {
  if (!chart && chartEl.value) {
    chart = echarts.init(chartEl.value)
  }
  if (!chart) return
  chart.setOption(buildOption(items.value), { notMerge: true })
}

watch(items, () => renderChart())
watch(chartEl, (el) => {
  if (el && items.value.length > 0) renderChart()
})

const handleResize = (): void => {
  chart?.resize()
}

onMounted(() => {
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  chart?.dispose()
  chart = null
})

/* status badge color for signed percentages */
function changeVariant(value: number): 'up' | 'down' {
  return value >= 0 ? 'up' : 'down'
}

const upColor = UP // referenced in template for inline accents
</script>

<template>
  <section>
    <PageHeader
      title="Analytics"
      subtitle="Which symbols are moving — change, volatility and volume aggregated over all synced klines (Doris OLAP, refreshed every 60s)"
      :freshness="freshness"
    >
      <template #actions>
        <div class="segmented" role="group" aria-label="Interval">
          <button
            v-for="i in INTERVALS"
            :key="i"
            type="button"
            :class="{ active: interval === i }"
            @click="interval = i"
          >
            {{ i }}
          </button>
        </div>
      </template>
    </PageHeader>

    <div class="metric-notes card">
      <span v-for="([term, def], i) in METRIC_NOTES" :key="term" class="metric-note">
        <span class="metric-term">{{ term }}</span>
        <span class="metric-def">{{ def }}</span>
        <span v-if="i < METRIC_NOTES.length - 1" class="metric-sep" aria-hidden="true">·</span>
      </span>
    </div>

    <ErrorState v-if="error && items.length === 0" :message="error" @retry="refresh" />
    <template v-else>
      <SkeletonRows v-if="loading" variant="cards" :rows="4" />
      <div v-else class="stat-strip">
        <StatCard label="Symbols Analyzed" :value="data?.total ?? 0" hint="Ranked by total volume" />
        <StatCard label="Total Volume" :value="formatAbbr(totalVolume)" hint="Sum over ranked symbols" />
        <StatCard
          label="Avg Volatility"
          :value="formatPercent(avgVolatility)"
          hint="Stddev of close-to-close returns"
          tone="accent"
        />
        <StatCard
          label="Top Gainer"
          :value="topGainer ? symbolLabel(topGainer) : '—'"
          :tone="topGainer && topGainer.price_change_pct >= 0 ? 'up' : 'down'"
        >
          <span v-if="topGainer" :style="{ color: topGainer.price_change_pct >= 0 ? upColor : undefined }">
            {{ formatPercent(topGainer.price_change_pct) }} this period
          </span>
        </StatCard>
      </div>

      <h2 class="section-title">Top {{ Math.min(CHART_TOP_N, items.length) }} by Volume</h2>
      <SkeletonRows v-if="loading" :rows="6" />
      <EmptyState
        v-else-if="items.length === 0"
        title="No analytics yet"
        message="The dw process syncs klines into Doris every 60s. Start Doris (docker compose up -d doris), run script/doris-init.sql, then make dw."
      />
      <div v-else class="card chart-card">
        <div ref="chartEl" class="chart"></div>
      </div>

      <h2 class="section-title">Ranked Symbols</h2>
      <SkeletonRows v-if="loading" :rows="8" />
      <div v-else-if="items.length > 0" class="card table-card">
        <table class="analytics-table">
          <thead>
            <tr>
              <th class="num-col">#</th>
              <th>Symbol</th>
              <th class="num-col">Change</th>
              <th class="num-col">Volatility</th>
              <th class="num-col">Total Volume</th>
              <th class="num-col">Avg Volume</th>
              <th class="num-col">High / Low</th>
              <th class="num-col">Candles</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, idx) in items" :key="row.symbol_guid">
              <td class="num-col rank">{{ idx + 1 }}</td>
              <td class="symbol-cell" :title="row.symbol_guid">{{ symbolLabel(row) }}</td>
              <td class="num-col">
                <StatusBadge
                  :variant="changeVariant(row.price_change_pct)"
                  :label="formatPercent(row.price_change_pct)"
                  :dot="false"
                />
              </td>
              <td class="num-col num">{{ formatPercent(row.volatility_pct) }}</td>
              <td class="num-col num">{{ formatAbbr(row.total_volume) }}</td>
              <td class="num-col num">{{ formatAbbr(row.avg_volume) }}</td>
              <td class="num-col num range-cell">
                {{ formatPrice(row.period_high) }} / {{ formatPrice(row.period_low) }}
              </td>
              <td class="num-col num">{{ row.candle_count }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </section>
</template>

<style scoped>
.stat-strip {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 14px;
}

.metric-notes {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 10px;
  padding: 10px 14px;
  margin-bottom: 14px;
  font-size: 12px;
  color: var(--text-3);
}

.metric-note {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  white-space: nowrap;
}

.metric-term {
  font-weight: 600;
  color: var(--text-2);
}

.metric-sep {
  margin-left: 4px;
  color: var(--border);
}

.section-title {
  font-size: 15px;
  margin: 28px 0 12px;
  color: var(--text-1);
}

.chart-card {
  padding: 8px;
}

.chart {
  width: 100%;
  height: 320px;
}

.table-card {
  overflow-x: auto;
}

.analytics-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  min-width: 760px;
}

.analytics-table th {
  text-align: left;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-3);
  padding: 12px 14px;
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}

.analytics-table td {
  padding: 10px 14px;
  border-bottom: 1px solid var(--border);
  color: var(--text-1);
  white-space: nowrap;
}

.analytics-table tbody tr:last-child td {
  border-bottom: 0;
}

.analytics-table tbody tr:hover {
  background: #f8fbff;
}

.num-col {
  text-align: right;
}

.rank {
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
}

.symbol-cell {
  font-weight: 600;
}

.range-cell {
  color: var(--text-2);
  font-size: 12px;
}

@media (max-width: 767px) {
  .chart {
    height: 240px;
  }
}
</style>
