<template>
  <div class="page">
    <div class="header">
        <div>
          <h1>Market Prices</h1>
          <div class="header-desc">
            Real-time cryptocurrency prices
            <span v-if="latestUpdatedAt"> · Last updated {{ formatDateTime(latestUpdatedAt) }} · Delay {{ formatDelay(latestDelaySeconds) }}</span>
          </div>
        </div>
      <div class="controls">
        <div class="fiat-group">
          <button v-for="f in fiatList" :key="f"
            :class="['fiat-btn', { active: selectedFiat === f }]"
            @click="selectedFiat = f">{{ f }}</button>
        </div>
        <span :class="['badge', sourceClass]">
          <span class="dot" :class="sourceClass === 'connected' ? 'on' : 'off'"></span>
          {{ sourceText }}
        </span>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="empty-state">Loading market data...</div>

    <!-- Error -->
    <div v-else-if="markets.length === 0 && sourceClass !== 'connected'" class="empty-state">
      Unable to load market data. The API may be unreachable.
    </div>

    <!-- Table -->
    <div v-else class="table-container">
      <table>
        <thead>
          <tr>
            <th style="width:36px">#</th>
            <th>Asset</th>
            <th style="text-align:right">Price</th>
            <th style="text-align:right">24h %</th>
            <th style="text-align:right">Volume (24h)</th>
            <th style="text-align:right">Market Cap</th>
            <th style="text-align:right">Freshness</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(m, i) in markets" :key="m.symbol">
            <td class="rank mono">{{ i + 1 }}</td>
            <td>
              <div class="asset-cell">
                <div class="logo-wrapper">
                  <img :src="m.logo" class="tiny-logo" v-if="m.logo" @error="handleImgError($event, m)">
                  <div class="logo-placeholder" v-else>{{ getInitial(m) }}</div>
                </div>
                <div>
                  <div class="asset-name">{{ m.name || 'Unknown' }}</div>
                  <div class="asset-symbol">{{ m.symbol }}</div>
                </div>
              </div>
            </td>
            <td class="price" style="text-align:right">{{ formatFiat(m.price, true) }}</td>
            <td :class="['change', getChangeClass(m.change24h)]" style="text-align:right">
              {{ formatChange(m.change24h) }}
            </td>
            <td style="text-align:right" class="mono">{{ formatFiat(m.volume) }}</td>
            <td style="text-align:right" class="mono">{{ formatFiat(m.market_cap) }}</td>
            <td style="text-align:right" class="mono">
              <span :class="['freshness', getFreshnessClass(m.data_delay_seconds)]">
                {{ formatDelay(m.data_delay_seconds) }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { request } from '../api/common'

const markets = ref([])
const source = ref('Loading...')
const loading = ref(true)
const selectedFiat = ref('USD')
const fiatList = ['USD', 'CNY', 'HKD']

const fiatRates = ref({ USD: 1, CNY: 7.2, HKD: 7.8 })
const fiatSymbols = { USD: '$', CNY: '¥', HKD: 'HK$' }

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
const latestUpdatedAt = computed(() => {
  const times = markets.value.map(m => Number(m.updated_at || 0)).filter(t => t > 0)
  if (times.length === 0) return 0
  return Math.max(...times)
})
const latestDelaySeconds = computed(() => {
  const delays = markets.value.map(m => Number(m.data_delay_seconds)).filter(d => d >= 0)
  if (delays.length === 0) return -1
  return Math.min(...delays)
})

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
    if (converted < 0.00001) return sym + converted.toExponential(2)
    if (converted < 0.01) return sym + converted.toFixed(6)
    if (converted < 1) return sym + converted.toFixed(4)
    if (converted < 1000) return sym + converted.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
    return sym + converted.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  }
  return sym + formatAbbr(converted)
}

const formatAbbr = (num) => {
  if (num >= 1e12) return (num / 1e12).toFixed(2) + 'T'
  if (num >= 1e9) return (num / 1e9).toFixed(2) + 'B'
  if (num >= 1e6) return (num / 1e6).toFixed(2) + 'M'
  if (num >= 1e3) return (num / 1e3).toFixed(2) + 'K'
  return num.toLocaleString(undefined, { maximumFractionDigits: 2 })
}

const formatChange = (c) => {
  const num = parseFloat(c)
  if (isNaN(num)) return '0.00%'
  const sign = num > 0 ? '+' : ''
  return `${sign}${num.toFixed(2)}%`
}

const formatDateTime = (ts) => {
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

const getFreshnessClass = (seconds) => {
  const n = Number(seconds)
  if (!Number.isFinite(n) || n < 0) return 'unknown'
  if (n <= 300) return 'fresh'
  if (n <= 1800) return 'delayed'
  return 'stale'
}

const getChangeClass = (c) => {
  const num = parseFloat(c)
  if (isNaN(num) || num === 0) return ''
  return num > 0 ? 'pos' : 'neg'
}

onMounted(async () => {
  try {
    const fr = await request('/api/v1/get_fiat_rates')
    if (fr.source === 'Connected' && fr.data && fr.data.rates) {
      fiatRates.value = { ...fiatRates.value, ...fr.data.rates }
    }
  } catch (e) {}

  const res = await request('/api/v1/get_market_dashboard')
  loading.value = false
  if (res.data) {
    markets.value = Array.isArray(res.data) ? res.data : []
    source.value = res.source
  } else {
    source.value = 'Error'
  }
})
</script>

<style scoped>
.rank { color: var(--text-muted); text-align: center; font-size: 0.75rem; }
.asset-cell { display: flex; align-items: center; gap: 12px; }
.logo-wrapper { width: 28px; height: 28px; flex-shrink: 0; display: flex; align-items: center; justify-content: center; }
.tiny-logo { width: 28px; height: 28px; border-radius: 50%; object-fit: contain; }
.logo-placeholder {
  width: 28px; height: 28px; background: var(--border); border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  font-size: 11px; font-weight: 700; color: var(--text-muted);
}
.asset-name { font-size: 0.88rem; font-weight: 600; }
.asset-symbol { font-size: 0.72rem; color: var(--text-muted); }
.change { font-family: 'SF Mono', 'Fira Code', 'Consolas', monospace; font-weight: 600; }
.freshness { font-size: 0.75rem; font-weight: 700; }
.freshness.fresh { color: #34d399; }
.freshness.delayed { color: #facc15; }
.freshness.stale, .freshness.unknown { color: #f87171; }
</style>
