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
import ResearchSignalFeed from '../features/insights/ResearchSignalFeed.vue'
import DataQualityPanel from '../features/insights/DataQualityPanel.vue'
import { usePolling } from '../composables/usePolling'
import { getDataQualitySummary } from '../api/dataQuality'
import { getResearchSignalEvents, getResearchSignalSummary } from '../api/research'
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
import { useI18n } from '../i18n'

echarts.use([BarChart, ScatterChart, GridComponent, TooltipComponent, CanvasRenderer])

const { locale } = useI18n()
const copy = computed(() => locale.value === 'zh-CN' ? {
  title: '市场洞察',
  subtitle: '用于分析市场宽度、跨场所价差和固定时间窗口的历史动量；这里展示研究信号，不提供可执行交易价格。',
  selectionTitle: '数据源入选覆盖',
  selectionDescription: '每个数据源都有稳定且经过审核的资产集合。CEX 目标为 50 项；DEX 只统计通过身份与路线资格校验的真实资产；All 是 CEX 资产的去重并集。',
  unavailable: '不可用',
  cexDispersion: 'CEX 报价离散度',
  cexDispersionHint: '最高价减最低价，再除以中间价',
  asset: '资产',
  venues: '场所数',
  dispersion: '离散度',
  noDispersion: '暂无资产同时拥有两个新鲜的 CEX 报价。',
  dexMonitor: 'DEX 路线路径监控',
  dexMonitorHint: '按 $10K / $1K / $100 分档询价；不是套利信号',
  provider: '数据源',
  routes: '路线数',
  quality: '质量',
  noDexRoutes: '暂无通过审核的 DEX 路线正在发布报价。',
  breadthTitle: '24 小时市场宽度 · 完整目录',
  breadthDescription: '使用更广的活跃市场目录，独立于四家 CEX 的入选并集。每项资产只采用一个参考市场；缺失涨跌幅仍记为未知。',
  assets: '资产数',
  unknown: '未知',
  advancing: '上涨',
  knownRatio: '占已知资产',
  declining: '下跌',
  unchanged: '持平',
  median24h: '24h 中位数',
  assetMedian: '资产级中位数',
  turnover24h: '24h 成交额',
  notFxNormalized: 'USD 系资产，未做外汇归一',
  changeDistribution: '涨跌分布',
  knownAssets: '个已知资产',
  crossVenueTitle: '跨场所监控',
  crossVenueDescription: '比较同一资产的现货与永续合约指示价；结果不是可执行套利价格。',
  noSharedAssets: '暂无共享资产',
  noSharedAssetsMessage: '当前没有同时具备活跃现货与永续市场的资产。',
  spot: '现货',
  perp: '永续',
  indicativeSpread: '指示价差',
  changeGap24h: '24h 涨跌差',
  turnoverShare: '成交额占比',
  freshness: '新鲜度',
  historicalTitle: '历史动量',
  historicalDescription: '只使用已闭合的 1 小时 K 线。覆盖率低于 90% 的资产保留在表格中，但不进入散点图。',
  momentumWindow: '动量窗口',
  historicalNoData: '历史模块暂无数据',
  historicalNoDataMessage: 'Doris 可能不可用，或所选闭合 K 线窗口尚未同步。实时市场宽度不受此模块影响。',
  returnVolatility: '收益率 × 波动率',
  plotted: '个已绘制',
  lowCoverage: '个低覆盖',
  noCoverageAssets: '没有资产达到覆盖率门槛',
  noCoverageAssetsMessage: '下方表格仍可查看。资产需要至少覆盖 90% 的预期闭合 1 小时 K 线，才会进入比较图。',
  referenceMarket: '参考市场',
  return: '收益率',
  volatility1h: '1h 波动率',
  highLowRange: '高低区间',
  coverage: '覆盖率',
  candles: 'K 线数',
  low: '低',
  historicalUnavailable: (status: string) => `历史动量不可用，因为 Doris 当前状态为 ${status || '未配置'}。`,
  tooltipAssets: '项资产',
  volatility: '波动率',
  range: '区间',
} : {
  title: 'Insights',
  subtitle: 'Analyze market breadth, cross-venue spreads, and fixed-window historical momentum. These are research signals, not executable prices.',
  selectionTitle: 'Provider Selection Coverage',
  selectionDescription: 'Every provider owns a stable reviewed selection. CEX targets 50; DEX shows only the real assets it can qualify. All remains the deduplicated CEX union.',
  unavailable: 'Unavailable',
  cexDispersion: 'CEX Quote Dispersion',
  cexDispersionHint: 'max minus min, relative to midpoint',
  asset: 'Asset',
  venues: 'Venues',
  dispersion: 'Dispersion',
  noDispersion: 'No asset has two fresh CEX quotes yet.',
  dexMonitor: 'DEX Route Monitor',
  dexMonitorHint: 'tiered $10K / $1K / $100 indicative routes; not an arbitrage signal',
  provider: 'Provider',
  routes: 'Routes',
  quality: 'Quality',
  noDexRoutes: 'No reviewed DEX route is publishing a quote.',
  breadthTitle: '24h Market Breadth · Full Catalog',
  breadthDescription: 'Broader active-market catalog, independent from the four CEX selection union. One reference-market vote per asset; missing changes remain Unknown.',
  assets: 'Assets',
  unknown: 'unknown',
  advancing: 'Advancing',
  knownRatio: 'of known',
  declining: 'Declining',
  unchanged: 'unchanged',
  median24h: 'Median 24h',
  assetMedian: 'Asset-level median',
  turnover24h: '24h Turnover',
  notFxNormalized: 'USD-family not FX-normalized',
  changeDistribution: 'Change distribution',
  knownAssets: 'known assets',
  crossVenueTitle: 'Cross-Venue Monitor',
  crossVenueDescription: 'Indicative spot–perp comparison for shared assets. This is not an executable arbitrage price.',
  noSharedAssets: 'No shared assets',
  noSharedAssetsMessage: 'No active asset currently has both a spot and perpetual market.',
  spot: 'Spot',
  perp: 'Perp',
  indicativeSpread: 'Indicative Spread',
  changeGap24h: '24h Change Gap',
  turnoverShare: 'Turnover Share',
  freshness: 'Freshness',
  historicalTitle: 'Historical Momentum',
  historicalDescription: 'Closed 1h candles only. Coverage below 90% stays in the table but is excluded from the scatter plot.',
  momentumWindow: 'Momentum window',
  historicalNoData: 'Historical module has no data',
  historicalNoDataMessage: 'Doris may be unavailable or the selected closed-candle window has not been synchronized yet. Real-time breadth remains independent.',
  returnVolatility: 'Return × volatility',
  plotted: 'plotted',
  lowCoverage: 'low coverage',
  noCoverageAssets: 'No assets meet the coverage threshold',
  noCoverageAssetsMessage: 'The table remains available below. Assets need at least 90% of expected closed 1h candles before they appear in this comparison.',
  referenceMarket: 'Reference Market',
  return: 'Return',
  volatility1h: '1h Volatility',
  highLowRange: 'High–Low Range',
  coverage: 'Coverage',
  candles: 'Candles',
  low: 'Low',
  historicalUnavailable: (status: string) => `Historical momentum is unavailable because Doris is ${status || 'not configured'}.`,
  tooltipAssets: 'assets',
  volatility: 'Volatility',
  range: 'Range',
})

const WINDOWS: Array<{ value: MomentumWindow; label: string }> = [
  { value: '24h', label: '24H' },
  { value: '7d', label: '7D' },
  { value: '30d', label: '30D' },
]
const windowValue = ref<MomentumWindow>('7d')

const realtime = usePolling(getMarketInsights, { interval: 30_000 })
const venues = usePolling(getTop50VenueInsights, { interval: 30_000 })
const researchEvents = usePolling(() => getResearchSignalEvents(), { interval: 60_000 })
const researchSummary = usePolling(getResearchSignalSummary, { interval: 60_000 })
const dataQuality = usePolling(getDataQualitySummary, { interval: 60_000 })

async function refreshResearch(): Promise<void> {
  await Promise.all([researchEvents.refresh(), researchSummary.refresh()])
}
async function getAvailableMomentum() {
  const system = await getSystemOverview()
  if (!isHealthyStatus(system.dw_status)) {
    throw new Error(copy.value.historicalUnavailable(system.dw_status))
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
          return row ? `${row.label}<br/><strong>${row.count}</strong> ${copy.value.tooltipAssets}` : ''
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
        name: `${copy.value.volatility} %`,
        nameTextStyle: { color: '#6E6E73', fontSize: 10 },
        axisLabel: { color: '#6E6E73', fontSize: 10, formatter: '{value}%' },
        splitLine: { lineStyle: { color: '#ECECF0' } },
      },
      yAxis: {
        type: 'value',
        name: `${copy.value.return} %`,
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
            `${copy.value.return} ${formatPercent(value.return_pct)}`,
            `${copy.value.volatility} ${formatPercent(value.volatility_pct)}`,
            `${copy.value.range} ${formatPercent(value.high_low_range_pct)}`,
            `${copy.value.coverage} ${value.coverage_pct.toFixed(1)}%`,
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
watch(locale, () => {
  renderDistribution()
  renderMomentum()
})
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
      :title="copy.title"
      :subtitle="copy.subtitle"
      :refreshed-at="realtime.lastUpdated.value"
    />

    <ResearchSignalFeed
      :feed="researchEvents.data.value"
      :summary="researchSummary.data.value"
      :loading="researchEvents.loading.value || researchSummary.loading.value"
      :error="researchEvents.error.value || researchSummary.error.value"
      @retry="refreshResearch"
    />

    <DataQualityPanel
      :summary="dataQuality.data.value"
      :loading="dataQuality.loading.value"
      :error="dataQuality.error.value"
      @retry="dataQuality.refresh"
    />

    <section class="insight-section">
      <div class="section-heading">
        <div>
          <h2>{{ copy.selectionTitle }}</h2>
          <p>{{ copy.selectionDescription }}</p>
        </div>
      </div>
      <ErrorState v-if="venues.error.value && !venues.data.value" :message="venues.error.value" @retry="venues.refresh" />
      <SkeletonRows v-else-if="venues.loading.value && !venues.data.value" variant="cards" :rows="4" />
      <template v-else>
        <div class="coverage-grid">
          <article v-for="row in venueCoverage" :key="row.venue" class="card coverage-card">
            <span>{{ row.venue }}</span>
            <strong v-if="row.available" class="num">{{ row.priced }} / {{ row.total }}</strong>
            <strong v-else class="unavailable">{{ copy.unavailable }}</strong>
            <small>{{ row.available ? `${row.coverage_pct.toFixed(1)}% ${row.coverage_kind}` : row.error }}</small>
          </article>
        </div>
        <div class="monitor-grid">
          <div class="table-card card">
            <div class="monitor-title">
              <strong>{{ copy.cexDispersion }}</strong>
              <small>{{ copy.cexDispersionHint }}</small>
            </div>
            <div class="table-scroll">
              <table class="compact-table">
                <thead><tr><th>{{ copy.asset }}</th><th>{{ copy.venues }}</th><th class="align-right">{{ copy.dispersion }}</th></tr></thead>
                <tbody v-if="cexDispersion.length">
                  <tr v-for="row in cexDispersion" :key="row.asset_id">
                    <td class="asset-symbol">{{ row.asset_symbol }}</td>
                    <td class="num">{{ row.venue_count }}</td>
                    <td class="align-right num">{{ formatPercent(row.dispersion_pct, 3) }}</td>
                  </tr>
                </tbody>
                <tbody v-else><tr><td colspan="3" class="unavailable">{{ copy.noDispersion }}</td></tr></tbody>
              </table>
            </div>
          </div>
          <div class="table-card card">
            <div class="monitor-title">
              <strong>{{ copy.dexMonitor }}</strong>
              <small>{{ copy.dexMonitorHint }}</small>
            </div>
            <div class="table-scroll">
              <table class="compact-table">
                <thead><tr><th>{{ copy.asset }}</th><th>{{ copy.provider }}</th><th class="align-right">{{ copy.routes }}</th><th class="align-right">{{ copy.quality }}</th></tr></thead>
                <tbody v-if="dexRouteMonitor.length">
                  <tr v-for="row in dexRouteMonitor" :key="`${row.provider}:${row.asset_id}`">
                    <td class="asset-symbol">{{ row.asset_symbol }}</td>
                    <td>{{ row.provider }}</td>
                    <td class="align-right num">{{ row.route_count }}</td>
                    <td class="align-right"><StatusBadge :variant="row.available ? 'live' : 'stale'" :label="row.quality" /></td>
                  </tr>
                </tbody>
                <tbody v-else><tr><td colspan="4" class="unavailable">{{ copy.noDexRoutes }}</td></tr></tbody>
              </table>
            </div>
          </div>
        </div>
      </template>
    </section>

    <section class="insight-section">
      <div class="section-heading">
        <div>
          <h2>{{ copy.breadthTitle }}</h2>
          <p>{{ copy.breadthDescription }}</p>
        </div>
      </div>
      <ErrorState v-if="realtime.error.value && !breadth" :message="realtime.error.value" @retry="realtime.refresh" />
      <template v-else>
        <SkeletonRows v-if="realtime.loading.value && !breadth" variant="cards" :rows="4" />
        <div v-else-if="breadth" class="breadth-grid">
          <StatCard :label="copy.assets" :value="breadth.asset_count" :hint="`${breadth.unknown} ${copy.unknown}`" />
          <StatCard :label="copy.advancing" :value="breadth.advancers" :hint="`${breadth.advance_ratio.toFixed(1)}% ${copy.knownRatio}`" tone="up" />
          <StatCard :label="copy.declining" :value="breadth.decliners" :hint="`${breadth.flat} ${copy.unchanged}`" tone="down" />
          <StatCard :label="copy.median24h" :value="formatPercent(breadth.median_change24h)" :hint="copy.assetMedian" tone="accent" />
          <StatCard :label="copy.turnover24h" :value="formatAbbr(breadth.turnover24h, '$')" :hint="copy.notFxNormalized" />
        </div>
        <div v-if="breadth" class="chart-card card">
          <div class="chart-caption">
            <span>{{ copy.changeDistribution }}</span>
            <span>{{ breadth.advancers + breadth.decliners + breadth.flat }} {{ copy.knownAssets }}</span>
          </div>
          <div ref="distributionEl" class="distribution-chart"></div>
        </div>
      </template>
    </section>

    <section class="insight-section">
      <div class="section-heading">
        <div>
          <h2>{{ copy.crossVenueTitle }}</h2>
          <p>{{ copy.crossVenueDescription }}</p>
        </div>
      </div>
      <ErrorState v-if="realtime.error.value && crossVenue.length === 0" :message="realtime.error.value" @retry="realtime.refresh" />
      <SkeletonRows v-else-if="realtime.loading.value && crossVenue.length === 0" :rows="6" />
      <EmptyState v-else-if="crossVenue.length === 0" :title="copy.noSharedAssets" :message="copy.noSharedAssetsMessage" />
      <div v-else class="table-card card">
        <div class="table-scroll">
          <table>
            <thead>
              <tr>
                <th>{{ copy.asset }}</th>
                <th>{{ copy.spot }}</th>
                <th>{{ copy.perp }}</th>
                <th class="align-right">{{ copy.indicativeSpread }}</th>
                <th class="align-right">{{ copy.changeGap24h }}</th>
                <th class="align-right">{{ copy.turnoverShare }}</th>
                <th class="align-center">{{ copy.freshness }}</th>
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
                  <span v-else class="unavailable">{{ copy.unavailable }}</span>
                </td>
                <td class="align-right">
                  <span v-if="row.change_gap_available" class="num" :class="signedClass(row.change_gap_pct_points)">
                    {{ row.change_gap_pct_points > 0 ? '+' : '' }}{{ row.change_gap_pct_points.toFixed(2) }} pp
                  </span>
                  <span v-else class="unavailable">—</span>
                </td>
                <td class="align-right share-cell">
                  <span class="num">{{ copy.spot }} {{ row.spot_turnover_share.toFixed(1) }}%</span>
                  <span class="num">{{ copy.perp }} {{ row.perp_turnover_share.toFixed(1) }}%</span>
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
          <h2>{{ copy.historicalTitle }}</h2>
          <p>{{ copy.historicalDescription }}</p>
        </div>
        <div class="segmented" role="group" :aria-label="copy.momentumWindow">
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
        :title="copy.historicalNoData"
        :message="copy.historicalNoDataMessage"
      />
      <template v-else>
        <div class="chart-card card">
          <div class="chart-caption">
            <span>{{ copy.returnVolatility }}</span>
            <span>{{ chartMomentum.length }} {{ copy.plotted }} · {{ lowCoverageCount }} {{ copy.lowCoverage }}</span>
          </div>
          <div v-if="chartMomentum.length > 0" ref="momentumEl" class="momentum-chart"></div>
          <EmptyState
            v-else
            :title="copy.noCoverageAssets"
            :message="copy.noCoverageAssetsMessage"
          />
        </div>
        <div class="table-card card momentum-table">
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>{{ copy.asset }}</th>
                  <th>{{ copy.referenceMarket }}</th>
                  <th class="align-right">{{ copy.return }}</th>
                  <th class="align-right">{{ copy.volatility1h }}</th>
                  <th class="align-right">{{ copy.highLowRange }}</th>
                  <th class="align-right">{{ copy.coverage }}</th>
                  <th class="align-right">{{ copy.candles }}</th>
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
                      :label="row.low_coverage ? `${copy.low} ${row.coverage_pct.toFixed(1)}%` : `${row.coverage_pct.toFixed(1)}%`"
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
