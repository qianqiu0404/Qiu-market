<template>
  <div class="page">
    <div class="header">
      <h1>Exchanges</h1>
      <span :class="['badge', source.toLowerCase()]">{{ source }}</span>
    </div>
    <div class="card-grid">
      <div v-for="e in exchanges" :key="e.guid" class="exchange-card">
        <div class="logo-container">
          <img v-if="e.logo" :src="e.logo" class="logo" @error="(err) => { err.target.style.display='none'; err.target.nextElementSibling.style.display='flex' }">
          <div class="logo-placeholder" :style="{ display: e.logo ? 'none' : 'flex' }">{{ e.name?.charAt(0) }}</div>
        </div>
        <div class="name">{{ e.name }}</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { request } from '../api/common'

const exchanges = ref([])
const source = ref('Loading...')

onMounted(async () => {
  const res = await request('/api/v1/get_exchanges')
  if (res.data) {
    exchanges.value = res.data
    source.value = res.source
  } else {
    source.value = 'Mock fallback'
    exchanges.value = [
      { guid: '1', name: 'Binance', logo: '' },
      { guid: '2', name: 'OKX', logo: '' }
    ]
  }
})
</script>

<style scoped>
.card-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 20px; margin-top: 24px; }
.exchange-card { background: #1e293b; padding: 24px; border-radius: 12px; text-align: center; border: 1px solid #334155; }
.logo-container { display: flex; justify-content: center; margin-bottom: 12px; }
.logo { width: 48px; height: 48px; border-radius: 8px; object-fit: contain; }
.logo-placeholder { 
  width: 48px; height: 48px; background: #334155; border-radius: 8px;
  display: flex; align-items: center; justify-content: center;
  font-size: 20px; font-weight: bold; color: #94a3b8;
}
.name { font-weight: bold; }
.badge { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; }
.badge.connected { background: #065f46; color: #34d399; }
.badge.mock { background: #7c2d12; color: #fb923c; }
</style>
