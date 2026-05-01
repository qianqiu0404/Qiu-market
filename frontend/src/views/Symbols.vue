<template>
  <div class="page">
    <div class="header">
      <h1>Symbols</h1>
      <span :class="['badge', source.toLowerCase()]">{{ source }}</span>
    </div>
    <div class="table-container">
      <table>
        <thead>
          <tr>
            <th>SYMBOL</th>
            <th>BASE ASSET</th>
            <th>QUOTE ASSET</th>
            <th>GUID</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in symbols" :key="s.guid">
            <td>{{ s.symbol_name }}</td>
            <td>{{ s.base_asset }}</td>
            <td>{{ s.quote_asset }}</td>
            <td class="guid">{{ s.guid }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { request } from '../api/common'

const symbols = ref([])
const source = ref('Loading...')

onMounted(async () => {
  const res = await request('/api/v1/get_symbols')
  if (res.data) {
    symbols.value = res.data
    source.value = res.source
  } else {
    source.value = 'Mock fallback'
    symbols.value = [
      { guid: 's1', symbol_name: 'BTC/USDT', base_asset: 'BTC', quote_asset: 'USDT' },
      { guid: 's2', symbol_name: 'ETH/USDT', base_asset: 'ETH', quote_asset: 'USDT' }
    ]
  }
})
</script>

<style scoped>
.table-container { margin-top: 24px; background: #1e293b; border-radius: 12px; overflow: hidden; border: 1px solid #334155; }
table { width: 100%; border-collapse: collapse; text-align: left; }
th { background: #0f172a; padding: 16px; color: #94a3b8; font-size: 0.75rem; text-transform: uppercase; }
td { padding: 16px; border-top: 1px solid #334155; }
.guid { font-family: monospace; color: #64748b; font-size: 0.75rem; }
.badge { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; }
.badge.connected { background: #065f46; color: #34d399; }
.badge.mock { background: #7c2d12; color: #fb923c; }
</style>
