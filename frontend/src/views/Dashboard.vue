<template>
  <div class="page">
    <div class="header">
      <h1>Dashboard</h1>
      <span :class="['badge', source.toLowerCase()]">{{ source }}</span>
    </div>

    <div class="stats-grid" v-if="overview">
      <div class="stat-card">
        <label>Total Assets</label>
        <div class="value">{{ overview.asset_count }}</div>
      </div>
      <div class="stat-card">
        <label>Total Symbols</label>
        <div class="value">{{ overview.symbol_count }}</div>
      </div>
      <div class="stat-card">
        <label>Total Markets</label>
        <div class="value">{{ overview.market_count }}</div>
      </div>
      <div class="stat-card">
        <label>Exchanges</label>
        <div class="value">{{ overview.exchange_count }}</div>
      </div>
      <div class="stat-card span-2">
        <label>Total Market Cap</label>
        <div class="value">${{ formatAbbr(overview.total_market_cap) }}</div>
      </div>
      <div class="stat-card span-2">
        <label>24h Volume</label>
        <div class="value">${{ formatAbbr(overview.total_volume) }}</div>
      </div>
    </div>

    <div class="status-list" v-if="overview">
      <h2>System Status</h2>
      <div class="status-item">
        <span>Crawler</span>
        <span class="status-val">{{ overview.crawler_status }}</span>
      </div>
      <div class="status-item">
        <span>Redis</span>
        <span class="status-val">{{ overview.redis_status }}</span>
      </div>
      <div class="status-item">
        <span>Database</span>
        <span class="status-val">{{ overview.database_status }}</span>
      </div>
      <div class="status-item">
        <span>API</span>
        <span class="status-val">{{ overview.api_status }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { request } from '../api/common'

const overview = ref(null)
const source = ref('Loading...')

const formatAbbr = (val) => {
  const num = parseFloat(val)
  if (isNaN(num)) return '0'
  if (num >= 1e12) return (num / 1e12).toFixed(2) + 'T'
  if (num >= 1e9) return (num / 1e9).toFixed(2) + 'B'
  if (num >= 1e6) return (num / 1e6).toFixed(2) + 'M'
  if (num >= 1e3) return (num / 1e3).toFixed(2) + 'K'
  return num.toFixed(2)
}

onMounted(async () => {
  const res = await request('/api/v1/get_system_overview')
  if (res.data) {
    overview.value = res.data
    source.value = res.source
  } else {
    source.value = 'Error'
  }
})
</script>

<style scoped>
.stats-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 20px; margin-top: 24px; }
.stats-grid .span-2 { grid-column: span 2; }
.stat-card { background: #1e293b; padding: 24px; border-radius: 12px; border: 1px solid #334155; }
.stat-card label { color: #94a3b8; font-size: 0.875rem; display: block; }
.stat-card .value { font-size: 2rem; font-weight: bold; margin-top: 8px; color: #38bdf8; }
.status-list { margin-top: 40px; background: #1e293b; padding: 24px; border-radius: 12px; }
.status-item { display: flex; justify-content: space-between; padding: 12px 0; border-bottom: 1px solid #334155; }
.status-val { color: #10b981; font-weight: bold; }
.badge { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; }
.badge.connected { background: #065f46; color: #34d399; }
.badge.error { background: #7f1d1d; color: #fca5a5; }
</style>
