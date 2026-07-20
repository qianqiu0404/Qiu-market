<template>
  <div class="page">
    <div class="header">
      <div>
        <h1>Dashboard</h1>
        <div class="header-desc" v-if="overview">
          Last updated {{ formatTime(overview.updated_at) }} · Delay {{ formatDelay(overview.data_delay_seconds) }}
        </div>
      </div>
      <div class="controls">
        <span :class="['badge', sourceClass]">
          <span class="dot" :class="sourceClass === 'error' ? 'off' : 'on'"></span>
          {{ sourceText }}
        </span>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="empty-state">Loading system data...</div>

    <!-- Error -->
    <div v-else-if="!overview" class="empty-state">
      Unable to load dashboard data. The API may be unreachable.
    </div>

    <!-- Stats Grid -->
    <div v-else class="stats-grid">
      <div class="stat-card stat-primary">
        <div class="stat-label">Total Market Cap</div>
        <div class="stat-value">${{ formatAbbr(overview.total_market_cap) }}</div>
      </div>
      <div class="stat-card stat-primary">
        <div class="stat-label">24h Volume</div>
        <div class="stat-value">${{ formatAbbr(overview.total_volume) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Assets</div>
        <div class="stat-value stat-num">{{ overview.asset_count }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Symbols</div>
        <div class="stat-value stat-num">{{ overview.symbol_count }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Markets</div>
        <div class="stat-value stat-num">{{ overview.market_count }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Exchanges</div>
        <div class="stat-value stat-num">{{ overview.exchange_count }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">Data Freshness</div>
        <div :class="['stat-value', 'stat-num', getDelayClass(overview.data_delay_seconds)]">
          {{ formatDelay(overview.data_delay_seconds) }}
        </div>
      </div>
    </div>

    <!-- Top Assets Trend -->
    <div class="trend-section" v-if="overview">
      <h2>Top Assets Trend</h2>
      <div class="trend-grid">
        <div v-for="a in assetTrends" :key="a.symbol" class="trend-card">
          <div class="trend-header">
            <span class="trend-name">{{ a.symbol }}</span>
            <span class="trend-price">${{ formatPrice(a.price) }}</span>
          </div>
          <div class="trend-chart" v-if="a.points.length > 1">
            <svg :viewBox="`0 0 ${a.svgW} ${a.svgH}`" width="100%" height="60">
              <polyline
                :points="a.svgPoints"
                :stroke="a.color"
                fill="none"
                stroke-width="1.5"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </div>
          <div v-else class="trend-chart loading">Loading...</div>
        </div>
      </div>
    </div>

    <!-- Data Source Status -->
    <div class="sources-section" v-if="overview">
      <h2>Data Sources</h2>
      <div class="sources-grid">
        <div class="source-item">
          <span class="dot on"></span>
          <div>
            <div class="source-name">Binance</div>
            <div class="source-desc">Prices &amp; Klines</div>
          </div>
          <span :class="['source-status', getDelayClass(overview.data_delay_seconds)]">
            {{ getDelayStatus(overview.data_delay_seconds) }}
          </span>
        </div>
        <div class="source-item">
          <span class="dot on"></span>
          <div>
            <div class="source-name">CoinGecko</div>
            <div class="source-desc">Market Cap</div>
          </div>
          <span class="source-status up">Running</span>
        </div>
        <div class="source-item">
          <span class="dot on"></span>
          <div>
            <div class="source-name">Open ER API</div>
            <div class="source-desc">Fiat Exchange Rates</div>
          </div>
          <span class="source-status up">Running</span>
        </div>
        <div class="source-item">
          <span class="dot on"></span>
          <div>
            <div class="source-name">PostgreSQL</div>
            <div class="source-desc">Data Storage</div>
          </div>
          <span class="source-status up">{{ overview.database_status }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, reactive } from 'vue'
import { request } from '../api/common'

const overview = ref(null)
const source = ref('Loading...')
const loading = ref(true)
const klineData = reactive({})

const trendAssets = [
  { symbol: 'BTC', guid: 's1', price: '—' },
  { symbol: 'ETH', guid: 's2', price: '—' },
  { symbol: 'SOL', guid: 's3', price: '—' },
]

const sourceClass = computed(() => {
  const s = source.value.toLowerCase()
  if (s === 'loading...') return ''
  if (s === 'connected') return 'connected'
  return 'error'
})
const sourceText = computed(() => {
  if (source.value === 'Loading...' || source.value === 'Connected') return 'Live'
  return 'Offline'
})

const formatAbbr = (val) => {
  const num = parseFloat(val)
  if (isNaN(num)) return '0'
  if (num >= 1e12) return (num / 1e12).toFixed(2) + 'T'
  if (num >= 1e9) return (num / 1e9).toFixed(2) + 'B'
  if (num >= 1e6) return (num / 1e6).toFixed(2) + 'M'
  if (num >= 1e3) return (num / 1e3).toFixed(2) + 'K'
  return num.toLocaleString(undefined, { maximumFractionDigits: 2 })
}

const formatTime = (ts) => {
  if (!ts) return 'Unknown'
  return new Date(ts).toLocaleString()
}

const formatDelay = (seconds) => {
  const n = Number(seconds)
  if (!Number.isFinite(n) || n < 0) return 'Unknown'
  if (n < 60) return `${Math.round(n)}s`
  if (n < 3600) return `${Math.round(n / 60)}m`
  if (n < 86400) return `${Math.round(n / 3600)}h`
  return `${Math.round(n / 86400)}d`
}

const getDelayClass = (seconds) => {
  const n = Number(seconds)
  if (!Number.isFinite(n) || n < 0) return 'stale'
  if (n <= 300) return 'up'
  if (n <= 1800) return 'delayed'
  return 'stale'
}

const getDelayStatus = (seconds) => {
  const n = Number(seconds)
  if (!Number.isFinite(n) || n < 0) return 'Unknown'
  if (n <= 300) return 'Fresh'
  if (n <= 1800) return 'Delayed'
  return 'Stale'
}

const formatPrice = (p) => {
  const num = parseFloat(p)
  if (isNaN(num)) return '—'
  if (num >= 1000) return num.toLocaleString(undefined, { maximumFractionDigits: 2 })
  if (num >= 1) return num.toFixed(2)
  if (num >= 0.01) return num.toFixed(4)
  return num.toFixed(8)
}

const assetTrends = computed(() => {
  return trendAssets.map(a => {
    const raw = klineData[a.guid] || []
    const closes = raw.map(k => parseFloat(k.close)).filter(v => !isNaN(v))
    const price = closes.length > 0 ? closes[closes.length - 1] : null
    const svgW = 200
    const svgH = 40
    if (closes.length < 2) {
      return { ...a, price: price || '—', points: [], svgW, svgH, svgPoints: '', color: '#64748b' }
    }
    const min = Math.min(...closes)
    const max = Math.max(...closes)
    const range = max - min || 1
    const pad = 2
    const stepX = (svgW - pad * 2) / (closes.length - 1)
    const points = closes.map((v, i) => {
      const x = pad + i * stepX
      const y = svgH - pad - ((v - min) / range) * (svgH - pad * 2)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    }).join(' ')
    const color = closes[closes.length - 1] >= closes[0] ? '#22c55e' : '#ef4444'
    return { ...a, price: price || '—', points: closes, svgW, svgH, svgPoints: points, color }
  })
})

onMounted(async () => {
  const res = await request('/api/v1/get_system_overview')
  loading.value = false
  if (res.data) {
    overview.value = res.data
    source.value = res.source
  } else {
    source.value = 'Error'
  }

  // Fetch Klines for trend charts
  for (const a of trendAssets) {
    try {
      const kr = await request('/api/v1/get_klines', {
        method: 'POST',
        body: JSON.stringify({ symbol_guid: a.guid, limit: 30, interval: '1h' })
      })
      if (kr.source === 'Connected' && Array.isArray(kr.data)) {
        klineData[a.guid] = kr.data
      }
    } catch (e) {}
  }
})
</script>

<style scoped>
.stats-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; margin-top: 0; }
.stat-card {
  background: var(--bg-card); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 20px; box-shadow: var(--shadow);
}
.stat-card.stat-primary {
  background: linear-gradient(135deg, #ffffff 0%, #eff6ff 100%);
  border-color: var(--border-light);
}
.stat-primary .stat-value { font-size: 1.6rem; color: var(--accent-cyan); }
.stat-label { color: var(--text-muted); font-size: 0.78rem; font-weight: 500; margin-bottom: 8px; }
.stat-value { font-size: 1.4rem; font-weight: 700; font-family: 'SF Mono', 'Fira Code', 'Consolas', monospace; letter-spacing: -0.02em; }
.stat-num { color: var(--accent-blue); }

.sources-section { margin-top: 40px; }
.sources-section h2 { font-size: 1rem; font-weight: 600; margin-bottom: 16px; color: var(--text-secondary); }
.sources-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 12px; }
.source-item {
  display: flex; align-items: center; gap: 12px;
  background: var(--bg-card); border: 1px solid var(--border);
  border-radius: var(--radius-sm); padding: 14px 16px;
}
.source-name { font-size: 0.88rem; font-weight: 600; }
.source-desc { font-size: 0.72rem; color: var(--text-muted); margin-top: 1px; }
.source-status { margin-left: auto; font-size: 0.72rem; font-weight: 600; }
.source-status.up { color: var(--accent-green); }
.source-status.delayed, .stat-value.delayed { color: #facc15; }
.source-status.stale, .stat-value.stale { color: #f87171; }

.trend-section { margin-top: 40px; }
.trend-section h2 { font-size: 1rem; font-weight: 600; margin-bottom: 16px; color: var(--text-secondary); }
.trend-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
.trend-card {
  background: var(--bg-card); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 16px; box-shadow: var(--shadow);
}
.trend-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.trend-name { font-size: 0.9rem; font-weight: 600; }
.trend-price { font-size: 0.85rem; font-weight: 600; font-family: 'SF Mono', 'Fira Code', 'Consolas', monospace; color: var(--text-primary); }
.trend-chart { height: 60px; display: flex; align-items: flex-end; }
.trend-chart svg { display: block; }
.trend-chart.loading { color: var(--text-muted); font-size: 0.75rem; align-items: center; justify-content: center; }

@media (max-width: 768px) {
  .trend-grid { grid-template-columns: 1fr; }
}
</style>
