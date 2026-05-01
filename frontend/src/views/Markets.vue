<template>
  <div class="page">
    <div class="header">
      <h1>Market Prices</h1>
      <div class="controls">
        <div class="fiat-group">
          <button v-for="f in fiatList" :key="f"
            :class="['fiat-btn', { active: selectedFiat === f }]"
            @click="selectedFiat = f">{{ f }}</button>
        </div>
        <span :class="['badge', source.toLowerCase().replace(' ', '-')]">{{ source }}</span>
      </div>
    </div>
    <div class="table-container">
      <table>
        <thead>
          <tr>
            <th>ASSET</th>
            <th>SYMBOL</th>
            <th>PRICE</th>
            <th>24H CHANGE</th>
            <th>VOLUME (24H)</th>
            <th>MARKET CAP</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="m in markets" :key="m.symbol">
            <td>
              <div class="asset-cell">
                <div class="logo-wrapper">
                  <img :src="m.logo" class="tiny-logo" v-if="m.logo" @error="handleImgError($event, m)">
                  <div class="logo-placeholder" v-else>{{ getInitial(m) }}</div>
                </div>
                <span>{{ m.name || 'Unknown' }}</span>
              </div>
            </td>
            <td>{{ m.symbol }}</td>
            <td class="price">{{ formatFiat(m.price, true) }}</td>
            <td :class="['change', getChangeClass(m.change24h)]">
              {{ formatChange(m.change24h) }}%
            </td>
            <td>{{ formatFiat(m.volume) }}</td>
            <td>{{ formatFiat(m.market_cap) }}</td>
          </tr>
        </tbody>
      </table>
      <div v-if="markets.length === 0" class="empty-state">
        No market data available.
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { request } from '../api/common'

const markets = ref([])
const source = ref('Loading...')
const selectedFiat = ref('USD')
const fiatList = ['USD', 'CNY', 'HKD']

const fiatRates = ref({ USD: 1, CNY: 7.2, HKD: 7.8 })
const fiatSource = ref('')
const fiatSymbols = { USD: '$', CNY: '¥', HKD: 'HK$' }

const getFiatRate = () => fiatRates.value[selectedFiat.value] || 1
const getFiatSymbol = () => fiatSymbols[selectedFiat.value] || '$'

const getInitial = (m) => {
  if (m.symbol && m.symbol.includes('XRP')) return 'X';
  return (m.name || m.symbol || 'U').charAt(0).toUpperCase()
}

const handleImgError = (e, m) => {
  const target = e.target;
  const parent = target.parentNode;
  target.style.display = 'none';
  if (!parent.querySelector('.logo-placeholder')) {
    const placeholder = document.createElement('div');
    placeholder.className = 'logo-placeholder';
    placeholder.innerText = getInitial(m);
    parent.appendChild(placeholder);
  }
}

const formatFiat = (val, isPrice = false) => {
  const num = parseFloat(val)
  if (isNaN(num)) return getFiatSymbol() + '0'
  const converted = num * getFiatRate()
  const sym = getFiatSymbol()
  if (isPrice) {
    // Price: 2-6 decimal places, keep small coins visible
    if (converted < 0.01) return sym + converted.toFixed(8)
    if (converted < 1) return sym + converted.toFixed(4)
    return sym + converted.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  }
  // Volume / Market cap: abbreviated
  const abbr = formatAbbr(converted)
  return sym + abbr
}

const formatAbbr = (num) => {
  if (num >= 1e12) return (num / 1e12).toFixed(2) + 'T'
  if (num >= 1e9) return (num / 1e9).toFixed(2) + 'B'
  if (num >= 1e6) return (num / 1e6).toFixed(2) + 'M'
  if (num >= 1e3) return (num / 1e3).toFixed(2) + 'K'
  return num.toFixed(2)
}

const formatChange = (c) => {
  const num = parseFloat(c)
  if (isNaN(num)) return '0.00'
  return (num > 0 ? '+' : '') + num.toFixed(2)
}

const getChangeClass = (c) => {
  const num = parseFloat(c)
  if (isNaN(num) || num === 0) return ''
  return num > 0 ? 'pos' : 'neg'
}

onMounted(async () => {
  // Fetch fiat rates from backend
  try {
    const fr = await request('/api/v1/get_fiat_rates')
    if (fr.source === 'Connected' && fr.data && fr.data.rates) {
      fiatRates.value = { ...fiatRates.value, ...fr.data.rates }
      fiatSource.value = fr.data.source || ''
    }
  } catch (e) {
    // Keep fallback rates
    fiatSource.value = 'fallback'
  }

  const res = await request('/api/v1/get_market_dashboard')
  source.value = res.source
  markets.value = Array.isArray(res.data) ? res.data : []
})
</script>

<style scoped>
.header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; }
.controls { display: flex; align-items: center; gap: 12px; }
.fiat-group { display: flex; gap: 2px; background: #0f172a; border-radius: 6px; padding: 2px; }
.fiat-btn { background: transparent; color: #94a3b8; border: none; padding: 4px 10px; border-radius: 4px; font-size: 0.75rem; cursor: pointer; transition: all 0.15s; }
.fiat-btn:hover { color: #e2e8f0; }
.fiat-btn.active { background: #334155; color: #f8fafc; }
.table-container { margin-top: 24px; background: #1e293b; border-radius: 12px; overflow: hidden; border: 1px solid #334155; }
table { width: 100%; border-collapse: collapse; text-align: left; }
th { background: #0f172a; padding: 16px; color: #94a3b8; font-size: 0.75rem; text-transform: uppercase; }
td { padding: 16px; border-top: 1px solid #334155; }
.asset-cell { display: flex; align-items: center; gap: 12px; }
.logo-wrapper { width: 24px; height: 24px; flex-shrink: 0; display: flex; align-items: center; justify-content: center; }
.tiny-logo { width: 24px; height: 24px; border-radius: 50%; object-fit: contain; }
:deep(.logo-placeholder) { 
  width: 24px; height: 24px; background: #334155; border-radius: 50%; 
  display: flex; align-items: center; justify-content: center; 
  font-size: 10px; font-weight: bold; color: #94a3b8;
}
.price { color: #f8fafc; font-weight: bold; font-family: monospace; }
.change { font-family: monospace; font-weight: bold; }
.pos { color: #10b981; }
.neg { color: #ef4444; }
.badge { padding: 4px 8px; border-radius: 4px; font-size: 0.75rem; font-weight: bold; }
.badge.connected { background: #065f46; color: #34d399; }
.badge.error { background: #7f1d1d; color: #fca5a5; }
.empty-state { padding: 40px; text-align: center; color: #94a3b8; }
</style>
