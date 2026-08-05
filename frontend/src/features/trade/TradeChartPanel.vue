<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { CandlestickChart } from 'echarts/charts'
import { DataZoomComponent, GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { Kline, KlineInterval } from '../../api/market'
import { useI18n } from '../../i18n'

echarts.use([CandlestickChart, GridComponent, TooltipComponent, DataZoomComponent, CanvasRenderer])

const props = defineProps<{
  provider: string
  interval: KlineInterval
  intervals: KlineInterval[]
  klines: Kline[]
  error: string
  referenceError: string
  referenceObservedAt: number
  panelState: string
  panelClass: string
}>()
const emit = defineEmits<{ 'update:interval': [value: KlineInterval] }>()
const { locale, tr } = useI18n()
const chartElement = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null

function render(): void {
  if (!chartElement.value || !props.klines.length) {
    chart?.clear()
    return
  }
  if (!chart) chart = echarts.init(chartElement.value)
  chart.setOption({
    animation: false,
    grid: { left: 16, right: 58, top: 16, bottom: 42 },
    tooltip: { trigger: 'axis' },
    xAxis: { type: 'time', axisLabel: { hideOverlap: true }, splitLine: { show: false } },
    yAxis: { scale: true, position: 'right' },
    dataZoom: [{ type: 'inside' }, { type: 'slider', height: 16, bottom: 6 }],
    series: [{
      type: 'candlestick',
      data: props.klines.map((item) => [item.timestamp, item.open, item.close, item.low, item.high]),
      itemStyle: {
        color: '#16825d', color0: '#d92d4c',
        borderColor: '#16825d', borderColor0: '#d92d4c',
      },
    }],
  }, true)
}

function resize(): void { chart?.resize() }
watch(() => props.klines, () => void nextTick(render), { deep: true })
onMounted(() => { render(); window.addEventListener('resize', resize) })
onBeforeUnmount(() => { window.removeEventListener('resize', resize); chart?.dispose() })

function observedAt(): string {
  if (!props.referenceObservedAt) return tr('trade.status.noObservation')
  const milliseconds = props.referenceObservedAt < 1e12
    ? props.referenceObservedAt * 1000
    : props.referenceObservedAt
  return new Intl.DateTimeFormat(locale.value, {
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  }).format(new Date(milliseconds))
}
</script>

<template>
  <article class="card chart-panel">
    <header class="trade-panel-head">
      <div>
        <span>{{ tr('trade.chart.eyebrow', { provider: provider || 'unresolved' }) }}</span>
        <h2>{{ tr('trade.chart.title') }}</h2>
      </div>
      <div class="chart-actions">
        <span data-testid="panel-kline-state" class="panel-state" :class="panelClass">{{ panelState }}</span>
        <div class="intervals">
          <button
            v-for="value in intervals"
            :key="value"
            :class="{ active: interval === value }"
            @click="emit('update:interval', value)"
          >{{ value }}</button>
        </div>
      </div>
    </header>
    <div v-if="error && klines.length" class="panel-warning">
      {{ tr('trade.panel.failed', { panel: tr('trade.chart.title'), error: tr('trade.error.backend_unavailable') }) }}
      {{ tr('trade.panel.keepLastGood') }}
    </div>
    <div v-show="klines.length" ref="chartElement" class="trade-chart"></div>
    <div v-if="!klines.length" class="truth-empty">
      <strong>{{ tr('trade.chart.emptyTitle') }}</strong>
      <p>{{ error || referenceError ? tr('trade.chart.sourceUnavailable') : tr('trade.chart.emptyBody') }}</p>
      <span>{{ tr('trade.chart.noMock') }}</span>
    </div>
    <footer>
      <span>{{ referenceError ? tr('trade.chart.referenceUnavailable') : tr('trade.chart.referenceCaption') }}</span>
      <span>{{ observedAt() }}</span>
    </footer>
  </article>
</template>

<style scoped>
.chart-panel { overflow: hidden; }
.trade-panel-head { min-height: 64px; padding: 14px 18px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.trade-panel-head span { color: var(--text-3); font-size: 11px; font-weight: 600; text-transform: uppercase; }
.trade-panel-head h2 { margin-top: 3px; font-size: 16px; }
.chart-actions { display: flex; align-items: center; gap: 8px; }
.panel-state { font: 600 10px var(--font-mono); }
.panel-state--current { color: var(--up) !important; }
.panel-state--last-good { color: var(--warn) !important; }
.panel-state--unavailable { color: var(--down) !important; }
.intervals { display: flex; gap: 2px; padding: 3px; border: 1px solid var(--border); border-radius: 10px; background: var(--bg-panel-2); }
.intervals button { border: 0; border-radius: 7px; padding: 6px 10px; color: var(--text-2); background: transparent; cursor: pointer; }
.intervals button.active { color: var(--accent); background: var(--bg-panel); box-shadow: 0 1px 5px rgba(0,36,77,.12); }
.trade-chart { width: 100%; height: 400px; }
.truth-empty { min-height: 400px; display: grid; place-content: center; padding: 28px; text-align: center; background: linear-gradient(var(--border) 1px,transparent 1px),linear-gradient(90deg,var(--border) 1px,transparent 1px); background-size: 100% 60px,80px 100%; }
.truth-empty p { max-width: 460px; color: var(--text-2); }
.truth-empty span { color: var(--down); font-size: 10px; font-weight: 700; letter-spacing: .1em; }
.panel-warning { padding: 8px 18px; color: #805700; background: #fff8df; font-size: 11px; }
footer { min-height: 40px; padding: 9px 18px; border-top: 1px solid var(--border); display: flex; justify-content: space-between; gap: 12px; color: var(--text-3); font-size: 10px; }
@media (max-width: 700px) { .trade-panel-head,.chart-actions,footer { align-items: flex-start; flex-direction: column; } }
</style>
