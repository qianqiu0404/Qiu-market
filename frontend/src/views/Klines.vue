<template>
  <div class="page">
    <div class="header">
      <h1>Klines</h1>
      <div class="controls">
        <select v-model="selectedSymbol" @change="fetchKlines" class="symbol-select">
          <option value="s1">BTC/USDT</option>
          <option value="s2">ETH/USDT</option>
          <option value="s3">SOL/USDT</option>
        </select>
        <div class="interval-group">
          <button v-for="iv in intervals" :key="iv"
            :class="['interval-btn', { active: selectedInterval === iv }]"
            @click="changeInterval(iv)">{{ iv }}</button>
        </div>
        <span :class="['badge', source.toLowerCase().replace(' ', '-')]">{{ source }}</span>
      </div>
    </div>

    <div class="chart-container" ref="chartRef">
      <div v-if="error" class="chart-error">{{ error }}</div>
    </div>

    <div class="table-container">
      <div class="table-title">Recent Klines (Debug - last 20)</div>
      <table>
        <thead>
          <tr>
            <th>TIMESTAMP</th>
            <th>OPEN</th>
            <th>HIGH</th>
            <th>LOW</th>
            <th>CLOSE</th>
            <th>VOLUME</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(k, idx) in klines.slice(0, 20)" :key="idx">
            <td>{{ formatTime(k.timestamp) }}</td>
            <td class="mono">{{ formatNum(k.open) }}</td>
            <td class="mono">{{ formatNum(k.high) }}</td>
            <td class="mono">{{ formatNum(k.low) }}</td>
            <td class="mono">{{ formatNum(k.close) }}</td>
            <td class="mono">{{ formatNum(k.volume) }}</td>
          </tr>
        </tbody>
      </table>
      <div v-if="klines.length === 0" class="empty-state">
        No kline data available.
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { request } from '../api/common'
import { use, init } from 'echarts/core'
import { BarChart, CandlestickChart } from 'echarts/charts'
import { DataZoomComponent, GridComponent, TooltipComponent } from 'echarts/components'
import { SVGRenderer } from 'echarts/renderers'

use([
  BarChart,
  CandlestickChart,
  DataZoomComponent,
  GridComponent,
  TooltipComponent,
  SVGRenderer,
])

const selectedSymbol = ref('s1')
const selectedInterval = ref('1m')
const intervals = ['1m', '15m', '1h', '1d']
const klines = ref([])
const source = ref('Loading...')
const error = ref('')
const chartRef = ref(null)
let chartInstance = null

const formatTime = (ts) => {
  if (!ts) return '-'
  const d = new Date(ts)
  return d.getHours().toString().padStart(2, '0') + ':' +
         d.getMinutes().toString().padStart(2, '0') + ':' +
         d.getSeconds().toString().padStart(2, '0')
}

const formatNum = (val) => {
  const num = parseFloat(val)
  if (isNaN(num)) return '0.00'
  return num.toFixed(4)
}

const initChart = () => {
  if (chartRef.value) {
    if (chartInstance) chartInstance.dispose()
    chartInstance = init(chartRef.value, 'dark', { renderer: 'svg' })
  }
}

const changeInterval = (iv) => {
  selectedInterval.value = iv
  fetchKlines()
}

const updateChart = (data) => {
  if (!chartInstance) return
  if (data.length === 0) {
    chartInstance.clear()
    return
  }

  const now = Date.now()
  const minValid = new Date('2020-01-01').getTime()
  const maxValid = now + 24 * 60 * 60 * 1000
  const sorted = [...data]
    .sort((a, b) => a.timestamp - b.timestamp)
    .filter(k => k.timestamp >= minValid && k.timestamp <= maxValid)

  console.log('Raw Klines Count:', data.length)
  console.log('Filtered Klines Count:', sorted.length)

  if (sorted.length === 0) {
    console.log('No valid kline data')
    return
  }

  const len = sorted.length
  const categoryData = []
  const candleValues = []
  const volumeValues = []
  const volumeColors = []

  sorted.forEach(k => {
    const open = parseFloat(k.open)
    const close = parseFloat(k.close)
    let high = parseFloat(k.high)
    let low = parseFloat(k.low)
    const vol = parseFloat(k.volume)

    if (open === close && open === high && open === low) {
      high += 0.0001
      low -= 0.0001
    }

    const d = new Date(k.timestamp)
    const label = d.getHours().toString().padStart(2, '0') + ':' +
                  d.getMinutes().toString().padStart(2, '0')
    categoryData.push(label)
    candleValues.push([open, close, low, high])
    volumeValues.push(vol)
    volumeColors.push(close >= open ? '#10b981' : '#ef4444')
  })

  console.log('Processed Klines Count:', len)

  // X label interval: show ~6 labels, more aggressive for large data
  const labelInterval = len < 30 ? Math.floor(len / 6) : Math.max(1, Math.floor(len / 6) - 1)

  // Bar width: wider when data is sparse, keep tight when dense
  const barWidthPct = len < 30 ? '90%' : '85%'

  const option = {
    backgroundColor: 'transparent',
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross' },
      formatter: (params) => {
        const p = params[0]
        const idx = p.dataIndex
        const d = new Date(idx < sorted.length ? sorted[idx].timestamp : now)
        const k = sorted[idx]
        if (!k) return ''
        return `<div style="font-weight:bold">${d.toLocaleString()}</div>` +
               `O: ${parseFloat(k.open).toFixed(2)}<br/>` +
               `H: ${parseFloat(k.high).toFixed(2)}<br/>` +
               `L: ${parseFloat(k.low).toFixed(2)}<br/>` +
               `C: ${parseFloat(k.close).toFixed(2)}<br/>` +
               `Vol: ${parseFloat(k.volume).toLocaleString(undefined, {maximumFractionDigits: 0})}`
      }
    },
    grid: [
      { left: '6%', right: '3%', top: '3%', height: '68%' },
      { left: '6%', right: '3%', top: '77%', height: '18%' }
    ],
    xAxis: [
      {
        type: 'category',
        data: categoryData,
        boundaryGap: true,
        axisLine: { onZero: false },
        splitLine: { show: false },
        axisLabel: {
          fontSize: 10,
          interval: labelInterval
        },
        gridIndex: 0
      },
      {
        type: 'category',
        data: categoryData,
        boundaryGap: true,
        axisLine: { show: false },
        axisTick: { show: false },
        axisLabel: { show: false },
        splitLine: { show: false },
        gridIndex: 1
      }
    ],
    yAxis: [
      {
        scale: true,
        splitArea: { show: true },
        gridIndex: 0,
        axisLabel: { fontSize: 10 }
      },
      {
        scale: true,
        splitNumber: 2,
        gridIndex: 1,
        axisLabel: { show: false },
        splitLine: { show: false }
      }
    ],
    dataZoom: [
      {
        type: 'inside',
        xAxisIndex: [0, 1],
        start: 0,
        end: 100,
        zoomOnMouseWheel: true,
        moveOnMouseMove: true
      }
    ],
    series: [
      {
        name: 'KLine',
        type: 'candlestick',
        data: candleValues,
        xAxisIndex: 0,
        yAxisIndex: 0,
        itemStyle: {
          color: '#10b981',
          color0: '#ef4444',
          borderColor: '#10b981',
          borderColor0: '#ef4444'
        },
        barWidth: barWidthPct
      },
      {
        name: 'Volume',
        type: 'bar',
        data: volumeValues,
        xAxisIndex: 1,
        yAxisIndex: 1,
        itemStyle: {
          color: (params) => volumeColors[params.dataIndex]
        },
        barWidth: barWidthPct
      }
    ]
  }
  chartInstance.setOption(option, true)
}

const fetchKlines = async () => {
  try {
    error.value = ''
    const body = {
      symbol_guid: selectedSymbol.value,
      limit: 100
    }
    // 向后兼容：如果后端支持 interval，传入；不支持则忽略
    body.interval = selectedInterval.value

    const res = await request('/api/v1/get_klines', {
      method: 'POST',
      body: JSON.stringify(body)
    })

    if (res.source === 'Connected') {
      klines.value = Array.isArray(res.data) ? res.data : []
      source.value = 'Connected'
      updateChart(klines.value)
    } else {
      throw new Error(res.error || 'Unable to load kline data')
    }
  } catch (err) {
    source.value = 'Error'
    error.value = err instanceof Error ? err.message : 'Unable to load kline data. The API may be unreachable.'
    klines.value = []
    updateChart([])
  }
}

const handleResize = () => chartInstance?.resize()

onMounted(() => {
  initChart()
  fetchKlines()
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  chartInstance?.dispose()
  window.removeEventListener('resize', handleResize)
})
</script>

<style scoped>
.header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; }
.controls { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.symbol-select { background: #1e293b; color: #f8fafc; border: 1px solid #334155; padding: 6px 12px; border-radius: 6px; outline: none; }
.interval-group { display: flex; gap: 2px; background: #0f172a; border-radius: 6px; padding: 2px; }
.interval-btn { background: transparent; color: #94a3b8; border: none; padding: 4px 10px; border-radius: 4px; font-size: 0.75rem; cursor: pointer; transition: all 0.15s; }
.interval-btn:hover { color: #e2e8f0; }
.interval-btn.active { background: #334155; color: #f8fafc; }
.chart-container { width: 100%; height: 520px; background: #1e293b; border-radius: 12px; margin-bottom: 24px; border: 1px solid #334155; }
.chart-error { height: 100%; display: flex; align-items: center; justify-content: center; color: #fecaca; text-align: center; padding: 24px; }
.table-container { background: #1e293b; border-radius: 12px; overflow: hidden; border: 1px solid #334155; }
.table-title { padding: 12px 16px; background: #0f172a; border-bottom: 1px solid #334155; font-size: 0.875rem; color: #94a3b8; font-weight: bold; }
table { width: 100%; border-collapse: collapse; text-align: left; }
th { background: #0f172a; padding: 12px 16px; color: #94a3b8; font-size: 0.75rem; text-transform: uppercase; }
td { padding: 12px 16px; border-top: 1px solid #334155; color: #e2e8f0; font-size: 0.875rem; }
.mono { font-family: monospace; }
.badge { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; font-weight: bold; }
.badge.connected { background: #065f46; color: #34d399; }
.badge.error { background: #7f1d1d; color: #fecaca; }
.empty-state { padding: 40px; text-align: center; color: #94a3b8; }
</style>
