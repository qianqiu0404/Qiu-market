<template>
  <div class="page">
    <div class="header">
      <h1>Symbols</h1>
      <span :class="['badge', source.toLowerCase()]">{{ source }}</span>
    </div>
    <div v-if="source === 'Error'" class="empty-state">Unable to load symbols. The API may be unreachable.</div>
    <div v-else class="table-container">
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
    source.value = 'Error'
    symbols.value = []
  }
})
</script>

<style scoped>
.table-container { margin-top: 24px; background: var(--bg-card); border-radius: 12px; overflow: hidden; border: 1px solid var(--border); box-shadow: var(--shadow); }
table { width: 100%; border-collapse: collapse; text-align: left; }
th { background: #f8fafc; padding: 16px; color: var(--text-muted); font-size: 0.75rem; text-transform: uppercase; }
td { padding: 16px; border-top: 1px solid var(--border); }
.guid { font-family: monospace; color: var(--text-muted); font-size: 0.75rem; }
.badge { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; }
.badge.connected { background: rgba(22,163,74,0.10); color: var(--accent-green); }
.badge.error { background: rgba(220,38,38,0.10); color: var(--accent-red); }
.empty-state { padding: 40px; text-align: center; color: var(--text-muted); }
</style>
