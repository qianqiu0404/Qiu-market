<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { fetchMarkets } from '../api/market'
import type { MarketData } from '../types/market'

const markets = ref<MarketData[]>([])
const loading = ref(true)
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const source = ref('')
const error = ref('')

const loadData = async () => {
  loading.value = true
  error.value = ''
  const res = await fetchMarkets(page.value, pageSize.value)
  markets.value = res.items
  total.value = res.total
  source.value = res.source
  if (res.source === 'Error') {
    error.value = res.error || 'Unable to load market data. The API may be unreachable.'
  }
  loading.value = false
}

onMounted(async () => {
  await loadData()
})
</script>

<template>
  <div class="market-table-container">
    <div class="table-header">
       <span v-if="source" :class="['badge', source.toLowerCase().replace(' ', '-')]">{{ source }}</span>
    </div>
    <div v-if="loading" class="loading">Loading market data...</div>
    <div v-else-if="error" class="error-state">{{ error }}</div>
    <div v-else>
      <table class="market-table">
        <thead>
          <tr>
            <th>Asset</th>
            <th>Symbol</th>
            <th>Price</th>
            <th>24h Change</th>
            <th>Volume</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="m in markets" :key="m.symbol">
            <td>
              <div class="asset-info">
                <div class="logo-wrapper">
                  <img v-if="m.logo" :src="m.logo" class="asset-logo" @error="(e: any) => { e.target.style.display='none'; e.target.nextElementSibling.style.display='flex' }" />
                  <div class="logo-placeholder" :style="{ display: m.logo ? 'none' : 'flex' }">{{ m.name?.charAt(0) || m.symbol?.charAt(0) }}</div>
                </div>
                <span>{{ m.name || 'Unknown' }}</span>
              </div>
            </td>
            <td>{{ m.symbol }}</td>
            <td class="price-cell">${{ m.price.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 6 }) }}</td>
            <td :class="{ 'pos': m.change24h > 0, 'neg': m.change24h < 0 }">
              {{ m.change24h > 0 ? '+' : '' }}{{ m.change24h.toFixed(2) }}%
            </td>
            <td>{{ m.volume.toLocaleString() }}</td>
          </tr>
        </tbody>
      </table>
      <div class="pagination">
        <span>Total: {{ total }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.market-table-container { background: var(--bg-card); border: 1px solid var(--border); border-radius: 8px; overflow: hidden; box-shadow: var(--shadow); }
.table-header { padding: 8px 16px; display: flex; justify-content: flex-end; }
.loading { padding: 2rem; text-align: center; }
.error-state { padding: 2rem; text-align: center; color: var(--accent-red); }
.market-table { width: 100%; border-collapse: collapse; }
th, td { padding: 12px 16px; text-align: left; border-bottom: 1px solid var(--border); }
th { background: #f8fafc; color: var(--text-muted); font-size: 0.85rem; text-transform: uppercase; }
.price-cell { font-family: monospace; font-weight: bold; }
.pos { color: var(--accent-green); }
.neg { color: var(--accent-red); }
tr:hover { background: #f8fafc; }

.asset-info { display: flex; align-items: center; gap: 10px; }
.logo-wrapper { width: 24px; height: 24px; flex-shrink: 0; }
.asset-logo { width: 24px; height: 24px; border-radius: 50%; background: #f1f5f9; object-fit: contain; }
.logo-placeholder {
  width: 24px; height: 24px; background: #eef2ff; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  font-size: 10px; font-weight: bold; color: var(--accent-blue);
}

.badge { padding: 2px 6px; border-radius: 4px; font-size: 0.7rem; font-weight: bold; }
.badge.connected { background: rgba(22,163,74,0.10); color: var(--accent-green); }
.badge.error { background: rgba(220,38,38,0.10); color: var(--accent-red); }

.pagination { padding: 1rem; border-top: 1px solid var(--border); color: var(--text-muted); font-size: 0.9rem; }
</style>
