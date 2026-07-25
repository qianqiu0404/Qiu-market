<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import * as echarts from 'echarts/core'
import { BarChart, CandlestickChart, LineChart } from 'echarts/charts'
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  MarkLineComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import PageHeader from '../components/PageHeader.vue'
import EmptyState from '../components/EmptyState.vue'
import ErrorState from '../components/ErrorState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import AssetLogo from '../components/AssetLogo.vue'
import { getMarketDashboard, getKlines, type Kline, type KlineInterval, type MarketItem } from '../api/market'
import {
  formatAbbr,
  formatPercent,
  formatPrice,
  formatTime,
  isInProgressCandle,
  providerFreshnessVariant,
} from '../utils/format'

echarts.use([
  CandlestickChart,
  BarChart,
  LineChart,
  GridComponent,
  TooltipComponent,
  DataZoomComponent,
  LegendComponent,
  MarkLineComponent,
  CanvasRenderer,
])

const UP = '#16825D'
const DOWN = '#D92D4C'
const MA7_COLOR = '#A35F00'
const MA25_COLOR = '#0071E3'
const VOL_UP = 'rgba(22, 130, 93, 0.28)'
const VOL_DOWN = 'rgba(217, 45, 76, 0.28)'
const INTERVALS: KlineInterval[] = ['1m', '15m', '1h', '1d']

const route = useRoute()
const router = useRouter()
const marketId = computed(() => String(route.params.marketId ?? ''))
const market = ref<MarketItem | null>(null)
const marketError = ref<string | null>(null)
const marketLoading = ref(true)
const refreshedAt = ref<Date | null>(null)

async function loadMarket(): Promise<void> {
  marketLoading.value = true
  marketError.value = null
  try {
    const response = await getMarketDashboard(1, 1, { marketId: marketId.value })
    const selected = response.items[0]
    if (!selected || !selected.has_kline) throw new Error('This market has no K-line data')
    market.value = selected
    refreshedAt.value = new Date()
  } catch (e) {
    market.value = null
    marketError.value = e instanceof Error ? e.message : 'Unable to load market'
  } finally {
    marketLoading.value = false
  }
}

/* ===== Kline data + chart ===== */
const interval = ref<KlineInterval>('1h')
const klines = ref<Kline[]>([])
const klinesError = ref<string | null>(null)
const klinesLoading = ref(false)
const chartEl = ref<HTMLDivElement | null>(null)

let chart: echarts.ECharts | null = null
let refreshTimer: number | undefined
let chartReady = false

/** Simple moving average over closes; null until `period` closes exist. */
function movingAverage(data: Kline[], period: number): Array<[number, number | null]> {
  const out: Array<[number, number | null]> = []
  let sum = 0
  for (let i = 0; i < data.length; i++) {
    sum += data[i]!.close
    if (i >= period) sum -= data[i - period]!.close
    out.push([data[i]!.timestamp, i >= period - 1 ? sum / period : null])
  }
  return out
}

function buildOption(data: Kline[], includeZoomRange: boolean): echarts.EChartsCoreOption {
  // Continuous time axis: [timestamp, open, close, low, high] tuples.
  // No synthetic candles — real gaps in the data stay visible as gaps.
  // The in-progress candle (open + interval > now, still being upserted by
  // the crawler) is drawn with a dashed border + reduced opacity so it reads
  // as "still forming", not as a stuck or final value.
  const candles = data.map((k, i) => {
    const value = [k.timestamp, k.open, k.close, k.low, k.high]
    if (i === data.length - 1 && isInProgressCandle(k.timestamp, interval.value)) {
      return { value, itemStyle: { borderType: 'dashed' as const, opacity: 0.85 } }
    }
    return { value }
  })
  const volumes = data.map((k) => ({
    value: [k.timestamp, k.volume],
    itemStyle: { color: k.close >= k.open ? VOL_UP : VOL_DOWN },
  }))
  const ma7 = movingAverage(data, 7)
  const ma25 = movingAverage(data, 25)
  const last = data.length > 0 ? data[data.length - 1]! : null
  const lastColor = last ? (last.close >= last.open ? UP : DOWN) : UP
  const lastLive = last !== null && isInProgressCandle(last.timestamp, interval.value)

  const option: echarts.EChartsCoreOption = {
    animation: false,
    backgroundColor: 'transparent',
    axisPointer: { link: [{ xAxisIndex: 'all' }] },
    legend: {
      show: true,
      top: 0,
      right: 8,
      icon: 'roundRect',
      itemWidth: 12,
      itemHeight: 2,
      data: ['MA7', 'MA25'],
      textStyle: { color: '#515154', fontSize: 11 },
      inactiveColor: '#86868B',
    },
    grid: [
      { left: 20, right: 82, top: 24, height: '60%' },
      { left: 20, right: 82, top: '72%', height: '18%' },
    ],
    xAxis: [
      {
        type: 'time',
        gridIndex: 0,
        axisLine: { lineStyle: { color: '#D2D2D7' } },
        axisLabel: { show: false },
        axisTick: { show: false },
        splitLine: { show: false },
      },
      {
        type: 'time',
        gridIndex: 1,
        axisLine: { lineStyle: { color: '#D2D2D7' } },
        axisLabel: { color: '#6E6E73', fontSize: 10, hideOverlap: true },
        axisTick: { show: false },
        splitLine: { show: false },
      },
    ],
    yAxis: [
      {
        scale: true,
        gridIndex: 0,
        position: 'right',
        axisLabel: { color: '#6E6E73', fontSize: 10, margin: 10 },
        splitLine: { lineStyle: { color: '#ECECF0' } },
      },
      {
        scale: true,
        gridIndex: 1,
        position: 'right',
        axisLabel: { color: '#6E6E73', fontSize: 10, margin: 10, formatter: (v: number) => formatAbbr(v) },
        splitLine: { show: false },
      },
    ],
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross', label: { backgroundColor: '#515154' } },
      backgroundColor: '#FFFFFF',
      borderColor: '#D2D2D7',
      textStyle: { color: '#1D1D1F', fontSize: 12 },
      formatter: (params: unknown) => {
        const list = Array.isArray(params) ? (params as Array<{ seriesType?: string; dataIndex: number }>) : []
        const candle = list.find((p) => p.seriesType === 'candlestick')
        if (!candle) return ''
        const k = data[candle.dataIndex]
        if (!k) return ''
        const rows = [
          ['Open', formatPrice(k.open)],
          ['High', formatPrice(k.high)],
          ['Low', formatPrice(k.low)],
          ['Close', formatPrice(k.close)],
          ['Volume', formatAbbr(k.volume)],
        ]
        const lines = rows
          .map(
            ([label, value]) =>
              `<div style="display:flex;justify-content:space-between;gap:16px"><span style="color:#6E6E73">${label}</span><span>${value}</span></div>`,
          )
          .join('')
        return `<div style="margin-bottom:4px;color:#6E6E73">${formatTime(k.timestamp)}</div>${lines}`
      },
    },
    dataZoom: [
      { type: 'inside', xAxisIndex: [0, 1] },
      {
        type: 'slider',
        xAxisIndex: [0, 1],
        bottom: 4,
        height: 18,
        borderColor: '#D2D2D7',
        backgroundColor: 'transparent',
        fillerColor: 'rgba(0,113,227,0.12)',
        handleStyle: { color: '#0071E3' },
        textStyle: { color: '#6E6E73', fontSize: 10 },
      },
    ],
    series: [
      {
        type: 'candlestick',
        name: 'Kline',
        data: candles,
        xAxisIndex: 0,
        yAxisIndex: 0,
        itemStyle: {
          color: UP,
          color0: DOWN,
          borderColor: UP,
          borderColor0: DOWN,
        },
        markLine: last
          ? {
              symbol: 'none',
              silent: true,
              animation: false,
              data: [{ yAxis: last.close }],
              lineStyle: { color: lastColor, type: 'dashed', width: 1 },
              label: {
                show: true,
                position: 'end',
                formatter: `${lastLive ? 'LIVE ' : ''}${formatPrice(last.close)}`,
                color: '#FFFFFF',
                backgroundColor: lastColor,
                padding: [2, 4],
                borderRadius: 3,
                fontSize: 10,
              },
            }
          : undefined,
      },
      {
        type: 'line',
        name: 'MA7',
        data: ma7,
        xAxisIndex: 0,
        yAxisIndex: 0,
        showSymbol: false,
        lineStyle: { color: MA7_COLOR, width: 1.2 },
        emphasis: { disabled: true },
        z: 3,
      },
      {
        type: 'line',
        name: 'MA25',
        data: ma25,
        xAxisIndex: 0,
        yAxisIndex: 0,
        showSymbol: false,
        lineStyle: { color: MA25_COLOR, width: 1.2 },
        emphasis: { disabled: true },
        z: 3,
      },
      {
        type: 'bar',
        name: 'Volume',
        data: volumes,
        xAxisIndex: 1,
        yAxisIndex: 1,
        barMaxWidth: 8,
      },
    ],
  }
  if (includeZoomRange && data.length > 80) {
    const start = Math.round((1 - 80 / data.length) * 100)
    option.dataZoom = (option.dataZoom as Array<Record<string, unknown>>).map((dz) => ({
      ...dz,
      start,
      end: 100,
    }))
  }
  return option
}

function renderChart(includeZoomRange: boolean): void {
  if (!chart && chartEl.value) {
    chart = echarts.init(chartEl.value)
  }
  if (!chart) return
  // notMerge=false keeps user zoom on silent refreshes; includeZoomRange only
  // sets an initial window when (re)loading a new symbol/interval.
  chart.setOption(buildOption(klines.value, includeZoomRange), { notMerge: !chartReady && includeZoomRange })
  chartReady = true
}

async function loadKlines(resetZoom: boolean): Promise<void> {
  if (!marketId.value) return
  if (klines.value.length === 0) klinesLoading.value = true
  try {
    klines.value = await getKlines(marketId.value, interval.value, 200)
    klinesError.value = null
    renderChart(resetZoom)
  } catch (e) {
    klinesError.value = e instanceof Error ? e.message : 'Unable to load klines'
  } finally {
    klinesLoading.value = false
  }
}

function scheduleAutoRefresh(): void {
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer)
  refreshTimer = window.setInterval(() => {
    if (document.hidden) return
    void loadMarket()
    void loadKlines(false)
  }, 60_000)
}

const handleResize = (): void => {
  chart?.resize()
}

watch([marketId, interval], () => {
  klines.value = []
  chartReady = false
  scheduleAutoRefresh()
  void loadKlines(true)
})

watch(chartEl, (el) => {
  if (el && klines.value.length > 0) renderChart(!chartReady)
})

onMounted(() => {
  window.addEventListener('resize', handleResize)
  scheduleAutoRefresh()
  void loadMarket()
  void loadKlines(true)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  if (refreshTimer !== undefined) window.clearInterval(refreshTimer)
  chart?.dispose()
  chart = null
})
</script>

<template>
  <section>
    <PageHeader
      :title="market?.symbol ?? 'Market chart'"
      :subtitle="market ? `${market.exchange} · ${market.market_type || 'Market'}` : 'Loading market…'"
      :refreshed-at="refreshedAt"
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

    <button type="button" class="back-link" @click="router.back()">← Back to Markets</button>

    <div v-if="market" class="market-strip card">
      <div class="market-identity">
        <AssetLogo :src="market.logo" :name="market.symbol" :size="38" />
        <div>
          <div class="market-name">{{ market.name || market.symbol }}</div>
          <div class="market-code">{{ market.market_code }}</div>
        </div>
      </div>
      <div class="market-metric">
        <span>Last price</span>
        <strong class="num">${{ formatPrice(market.price) }}</strong>
      </div>
      <div class="market-metric">
        <span>24h change</span>
        <StatusBadge
          v-if="market.change_available"
          :variant="market.change24h >= 0 ? 'up' : 'down'"
          :label="formatPercent(market.change24h)"
          :dot="false"
        />
        <strong v-else>—</strong>
      </div>
      <div class="market-metric">
        <span>Updated</span>
        <strong>{{ formatTime(market.updated_at) }}</strong>
      </div>
      <div class="market-metric">
        <span>Source</span>
        <StatusBadge
          :variant="providerFreshnessVariant(market.freshness_status)"
          :label="market.freshness_status || 'Unknown'"
        />
      </div>
    </div>

    <ErrorState
      v-if="marketError"
      :message="marketError"
      @retry="loadMarket"
    />
    <ErrorState
      v-else-if="klinesError && klines.length === 0"
      :message="klinesError"
      @retry="() => loadKlines(true)"
    />
    <EmptyState
      v-else-if="!marketLoading && !klinesLoading && marketId && klines.length === 0"
      title="No kline data"
      message="This market has no candles for the selected interval yet."
    />
    <div v-show="!marketError && (klines.length > 0 || klinesLoading)" class="card chart-card">
      <div v-if="klinesLoading" class="chart-skeleton shimmer"></div>
      <div ref="chartEl" class="chart"></div>
    </div>
  </section>
</template>

<style scoped>
.back-link {
  appearance: none;
  border: 0;
  background: transparent;
  color: var(--text-2);
  font: inherit;
  font-size: 13px;
  padding: 0;
  margin: -4px 0 14px;
  cursor: pointer;
}

.back-link:hover {
  color: var(--accent);
}

.market-strip {
  display: flex;
  align-items: center;
  gap: 28px;
  padding: 14px 18px;
  margin-bottom: 14px;
}

.market-identity {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 220px;
}

.market-name {
  font-weight: 650;
}

.market-code {
  margin-top: 2px;
  font-size: 11px;
  color: var(--text-3);
}

.market-metric {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 12px;
}

.market-metric > span {
  color: var(--text-3);
}

.market-metric strong {
  font-size: 14px;
}

.chart-card {
  position: relative;
  padding: 8px;
}

.chart {
  width: 100%;
  height: 460px;
}

.chart-skeleton {
  position: absolute;
  inset: 8px;
  border-radius: var(--radius-card);
  z-index: 1;
}

@media (max-width: 900px) {
  .market-strip {
    align-items: flex-start;
    flex-wrap: wrap;
    gap: 16px 24px;
  }
}

@media (max-width: 767px) {
  .chart {
    height: 360px;
  }
}
</style>
