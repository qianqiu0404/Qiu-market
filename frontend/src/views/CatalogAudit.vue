<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import StatusBadge from '../components/StatusBadge.vue'
import ErrorState from '../components/ErrorState.vue'
import { usePolling } from '../composables/usePolling'
import { getProviderCatalogAudit } from '../api/market'
import { formatTime } from '../utils/format'

const provider = ref('')
const status = ref('')
const rankLimit = ref(50)
const page = ref(1)
const pageSize = 50
const audit = usePolling(
  () => getProviderCatalogAudit(page.value, pageSize, provider.value, status.value, rankLimit.value),
  { interval: 30_000 },
)

watch([provider, status, rankLimit], () => {
  page.value = 1
  void audit.refresh()
})

const pageCount = computed(() => Math.max(1, Math.ceil((audit.data.value?.total ?? 0) / pageSize)))

function statusVariant(value: string): 'live' | 'delayed' | 'error' | 'accent' {
  if (value === 'enabled') return 'live'
  if (value === 'resolved') return 'delayed'
  if (value === 'ambiguous' || value === 'rejected') return 'error'
  return 'accent'
}
</script>

<template>
  <section class="catalog-audit">
    <div class="audit-summary">
      <article v-for="count in audit.data.value?.counts ?? []" :key="count.status">
        <span>{{ count.status }}</span>
        <strong class="num">{{ count.count }}</strong>
      </article>
    </div>

    <div class="audit-toolbar">
      <label>
        Provider
        <select v-model="provider" class="input">
          <option value="">All providers</option>
          <option value="binance">Binance</option>
          <option value="coinbase">Coinbase</option>
          <option value="bybit">Bybit</option>
          <option value="okx">OKX</option>
          <option value="hyperliquid">Hyperliquid</option>
          <option value="uniswap">Uniswap V2 + V3</option>
          <option value="pancakeswap">PancakeSwap V2 + V3</option>
        </select>
      </label>
      <label>
        Asset window
        <select v-model.number="rankLimit" class="input">
          <option :value="50">Top 50</option>
          <option :value="200">Top 200 candidates</option>
          <option :value="0">All discovered</option>
        </select>
      </label>
      <label>
        Resolution
        <select v-model="status" class="input">
          <option value="">All states</option>
          <option value="discovered">Discovered</option>
          <option value="resolved">Resolved</option>
          <option value="enabled">Enabled</option>
          <option value="ambiguous">Ambiguous</option>
          <option value="rejected">Rejected</option>
        </select>
      </label>
    </div>

    <ErrorState v-if="audit.error.value && !audit.data.value" :message="audit.error.value" @retry="audit.refresh" />
    <div v-else class="card audit-table-scroll">
      <table>
        <thead>
          <tr>
            <th>Provider</th>
            <th>Source market</th>
            <th>Identity / Rank</th>
            <th>Status</th>
            <th>Reason</th>
            <th>Last seen</th>
          </tr>
        </thead>
        <tbody v-if="audit.loading.value && !audit.data.value">
          <tr v-for="index in 8" :key="index"><td colspan="6"><div class="shimmer"></div></td></tr>
        </tbody>
        <tbody v-else-if="audit.data.value?.items.length">
          <tr v-for="item in audit.data.value.items" :key="`${item.provider}:${item.source_symbol}:${item.market_type}`">
            <td class="provider">{{ item.provider }}</td>
            <td>
              <strong>{{ item.source_symbol }}</strong>
              <small>{{ item.market_type }} · {{ item.upstream_status || 'unknown upstream state' }}</small>
            </td>
            <td>
              <span>{{ item.base_alias }}/{{ item.quote_alias }}</span>
              <small>
                {{ item.rank ? `#${item.rank}` : 'outside ranked window' }} ·
                {{ item.alias_review || (item.base_asset_id ? 'resolved' : 'review required') }}
              </small>
            </td>
            <td><StatusBadge :variant="statusVariant(item.resolution_status)" :label="item.resolution_status" /></td>
            <td class="reason">
              {{ item.reason || '—' }}
              <small>{{ item.rollout_mode || 'unconfigured' }} · {{ item.resolution_source || 'no review source' }}</small>
            </td>
            <td class="num">{{ formatTime(item.last_seen_at) }}</td>
          </tr>
        </tbody>
        <tbody v-else>
          <tr><td colspan="6" class="empty">No catalog candidates match this filter.</td></tr>
        </tbody>
      </table>
      <footer>
        <span class="num">{{ audit.data.value?.total ?? 0 }} candidates</span>
        <div>
          <button class="btn" type="button" :disabled="page <= 1" @click="page -= 1; audit.refresh()">Previous</button>
          <span class="num">{{ page }} / {{ pageCount }}</span>
          <button class="btn" type="button" :disabled="page >= pageCount" @click="page += 1; audit.refresh()">Next</button>
        </div>
      </footer>
    </div>
  </section>
</template>

<style scoped>
.catalog-audit { display: grid; gap: 14px; }
.audit-summary {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  border: 1px solid var(--border);
  border-radius: var(--radius-card);
  overflow: hidden;
}
.audit-summary article {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  background: var(--bg-panel);
  border-right: 1px solid var(--border);
}
.audit-summary article:last-child { border-right: 0; }
.audit-summary span { color: var(--text-3); font-size: 11px; text-transform: capitalize; }
.audit-summary strong { font-size: 16px; }
.audit-toolbar { display: flex; gap: 10px; }
.audit-toolbar label { display: flex; align-items: center; gap: 7px; color: var(--text-3); font-size: 11px; }
.audit-table-scroll { overflow-x: auto; }
table { width: 100%; min-width: 920px; border-collapse: collapse; font-size: 12px; }
th, td { padding: 11px 13px; border-bottom: 1px solid var(--border); text-align: left; }
th { color: var(--text-3); font-size: 10px; text-transform: uppercase; letter-spacing: .05em; }
td strong, td span, td small { display: block; }
td small { margin-top: 2px; color: var(--text-3); font-size: 10px; }
.provider { text-transform: capitalize; font-weight: 600; }
.reason { color: var(--text-3); }
.empty { padding: 36px; color: var(--text-3); text-align: center; }
footer, footer div { display: flex; align-items: center; gap: 9px; }
footer { justify-content: space-between; padding: 10px 13px; color: var(--text-3); }
@media (max-width: 900px) {
  .audit-summary { grid-template-columns: 1fr 1fr; }
  .audit-toolbar { align-items: stretch; flex-direction: column; }
}
</style>
