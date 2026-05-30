<template>
  <div class="page">
    <div class="header">
      <h1>Assets</h1>
      <span :class="['badge', source.toLowerCase()]">{{ source }}</span>
    </div>
    <div v-if="source === 'Error'" class="empty-state">Unable to load assets. The API may be unreachable.</div>
    <div v-else class="card-grid">
      <div v-for="a in assets" :key="a.guid" class="asset-card">
        <img :src="a.asset_logo" class="logo">
        <div class="info">
          <div class="symbol">{{ a.asset_symbol }}</div>
          <div class="name">{{ a.asset_name }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { request } from '../api/common'

const assets = ref([])
const source = ref('Loading...')

onMounted(async () => {
  const res = await request('/api/v1/get_support_assets')
  if (res.data) {
    assets.value = res.data
    source.value = res.source
  } else {
    source.value = 'Error'
    assets.value = []
  }
})
</script>

<style scoped>
.card-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 20px; margin-top: 24px; }
.asset-card { background: #1e293b; padding: 20px; border-radius: 12px; display: flex; align-items: center; border: 1px solid #334155; }
.logo { width: 32px; height: 32px; margin-right: 16px; }
.symbol { font-weight: bold; font-size: 1.1rem; }
.name { color: #94a3b8; font-size: 0.875rem; }
.badge { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; }
.badge.connected { background: #065f46; color: #34d399; }
.badge.error { background: #7f1d1d; color: #fecaca; }
.empty-state { padding: 40px; text-align: center; color: #94a3b8; }
</style>
