<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '../components/PageHeader.vue'
import StatusBadge from '../components/StatusBadge.vue'
import SkeletonRows from '../components/SkeletonRows.vue'
import ErrorState from '../components/ErrorState.vue'
import { usePolling } from '../composables/usePolling'
import { getSystemOverview } from '../api/market'
import { formatDelay, formatTime, isHealthyStatus } from '../utils/format'
import type { BadgeVariant } from '../components/StatusBadge.vue'

type SystemTab = 'status' | 'audit' | 'assets' | 'exchanges' | 'symbols'

const SYSTEM_TABS: Array<{ value: SystemTab; label: string }> = [
  { value: 'status', label: 'Status' },
  { value: 'audit', label: 'Catalog Audit' },
  { value: 'assets', label: 'Assets' },
  { value: 'exchanges', label: 'Exchanges' },
  { value: 'symbols', label: 'Symbols' },
]

const catalogComponents = {
  audit: defineAsyncComponent(() => import('./CatalogAudit.vue')),
  assets: defineAsyncComponent(() => import('./Assets.vue')),
  exchanges: defineAsyncComponent(() => import('./Exchanges.vue')),
  symbols: defineAsyncComponent(() => import('./Symbols.vue')),
}

const route = useRoute()
const router = useRouter()

function normalizeTab(value: unknown): SystemTab {
  return SYSTEM_TABS.some((tab) => tab.value === value) ? (value as SystemTab) : 'status'
}

const activeTab = computed<SystemTab>({
  get: () => normalizeTab(route.query.tab),
  set: (value) => {
    void router.replace({
      path: '/system',
      query: value === 'status' ? {} : { tab: value },
    })
  },
})

const activeCatalog = computed(() =>
  activeTab.value === 'status' ? null : catalogComponents[activeTab.value],
)

const pageTitle = computed(() => {
  const tab = SYSTEM_TABS.find((item) => item.value === activeTab.value)
  return activeTab.value === 'status' ? 'System' : tab?.label ?? 'System'
})

const pageSubtitle = computed(() => {
  switch (activeTab.value) {
  case 'audit':
    return 'Discovered markets and identity-resolution gates'
  case 'assets':
    return 'Supported asset catalog'
  case 'exchanges':
    return 'Connected exchange catalog'
  case 'symbols':
    return 'Tracked trading-pair catalog'
  default:
    return 'Ingestion process and dependency status'
  }
})

/* 15s poll for the pipeline view */
const overview = usePolling(getSystemOverview, { interval: 15_000 })

interface DetailRow {
  key: string
  label: string
  status: string
  healthy: boolean
}

const processes = computed<DetailRow[]>(() => {
  const ov = overview.data.value
  if (!ov) return []
  const entries: Array<[string, string]> = [
    ['Spot ingest supervisor', ov.crawler_status],
    ['DEX ingest supervisor', ov.dex_status],
    ['Repair worker', ov.worker_status],
    ['DW sync', ov.dw_status],
    ['gRPC', ov.rpc_status],
    ['Redis', ov.redis_status],
    ['PostgreSQL', ov.database_status],
    ['API', ov.api_status],
  ]
  return entries.map(([label, status]) => ({
    key: label,
    label,
    status: status || 'unknown',
    healthy: isHealthyStatus(status),
  }))
})

function statusVariant(status: string): BadgeVariant {
  if (status === 'Healthy') return 'live'
  if (status === 'Observing' || status === 'Unconfigured' ||
      status === 'Paused' || status === 'Local Preview') return 'accent'
  if (status === 'Stale') return 'stale'
  return 'error'
}

function feedModeLabel(value: string): string {
  const labels: Record<string, string> = {
    websocket_primary_rest_reconcile: 'WebSocket primary + REST reconcile',
    websocket_primary: 'WebSocket primary',
    rest_reconcile_only: 'REST reconcile only',
    http_polling: 'HTTP polling',
    native_rpc_routes: 'Native RPC routes',
    http_catalog: 'HTTP catalog',
    unobserved: 'Not observed',
    provider_specific: 'Provider specific',
  }
  return labels[value] ?? value ?? '—'
}
</script>

<template>
  <section>
    <PageHeader
      :title="pageTitle"
      :subtitle="pageSubtitle"
      :refreshed-at="activeTab === 'status' ? overview.lastUpdated.value : null"
    >
      <template #actions>
        <div class="segmented" role="group" aria-label="System section">
          <button
            v-for="tab in SYSTEM_TABS"
            :key="tab.value"
            type="button"
            :class="{ active: activeTab === tab.value }"
            @click="activeTab = tab.value"
          >
            {{ tab.label }}
          </button>
        </div>
      </template>
    </PageHeader>

    <div v-if="activeCatalog" class="catalog-view">
      <component :is="activeCatalog" />
    </div>
    <template v-else>
      <SkeletonRows v-if="overview.loading.value" :rows="5" />
      <ErrorState
        v-else-if="overview.error.value && !overview.data.value"
        :message="overview.error.value"
        @retry="overview.refresh"
      />
      <template v-else>
      <div class="section-heading">
        <div>
          <h2>Processes & dependencies</h2>
          <p>A heartbeat proves the process is running, not that its upstream source is usable.</p>
        </div>
      </div>
      <div class="card detail-card">
        <div
          v-for="row in processes"
          :key="row.key"
          class="detail-row"
        >
          <span class="detail-name">{{ row.label }}</span>
          <StatusBadge :variant="row.healthy ? 'live' : 'error'" :label="row.status" />
          <span class="detail-meta mono">
            updated {{ overview.data.value ? formatTime(overview.data.value.updated_at) : '—' }}
          </span>
          <span class="detail-meta mono">
            delay {{ overview.data.value ? formatDelay(overview.data.value.data_delay_seconds) : '—' }}
          </span>
        </div>
      </div>

      <div class="section-heading provider-heading">
        <div>
          <h2>Data sources</h2>
          <p>Operational health follows the active capability; rollout readiness remains evidence-only and requires a manual CLI action.</p>
        </div>
      </div>
      <div class="card detail-card">
        <div
          v-for="provider in overview.data.value?.provider_statuses ?? []"
          :key="provider.provider"
          class="provider-block"
        >
          <div class="provider-row">
            <div>
              <span class="detail-name provider-name">{{ provider.provider }}</span>
              <span class="provider-meta">
                {{ provider.local_preview_enabled ? 'local preview' : (provider.rollout_mode || 'unconfigured') }} ·
                Top {{ provider.rank_limit || '—' }} ·
                {{ feedModeLabel(provider.feed_mode) }} ·
                success {{ provider.success_rate_pct ? `${provider.success_rate_pct}%` : '—' }}
              </span>
              <span class="provider-meta evidence-line">
                {{ provider.received_count }} received · {{ provider.matched_asset_count }} matched ·
                {{ provider.local_preview_enabled ? provider.preview_covered_count : provider.price_available_count }} priced ·
                {{ provider.change_available_count }} with 24h ·
                operational {{ provider.operational_status || 'Unknown' }}
              </span>
              <span v-if="provider.selection_version" class="provider-meta evidence-line">
                selection v{{ provider.selection_version }} ·
                {{ provider.selection_count }}/{{ provider.selection_target_count }} selected ·
                {{ provider.selection_candidate_count }} valid Top 200 candidates ·
                generated {{ formatTime(provider.selection_generated_at || null) }}
              </span>
              <span v-if="provider.kline_status" class="provider-meta evidence-line">
                K-lines {{ provider.kline_status }} ·
                {{ provider.kline_market_count }}/{{ provider.selection_count || provider.selection_target_count }} markets ·
                {{ provider.kline_candle_count }} source candles this cycle ·
                success {{ formatTime(provider.kline_last_success_at || null) }}
              </span>
            </div>
            <StatusBadge
              :variant="statusVariant(provider.status)"
              :label="provider.status"
            />
            <span class="detail-meta mono">last success {{ formatTime(provider.last_success_at || null) }}</span>
            <span class="detail-meta mono">
              {{ provider.rollout_ready
                ? 'ready for manual promotion'
                : provider.readiness_not_before
                  ? `not before ${formatTime(provider.readiness_not_before)}`
                  : provider.next_retry_at
                ? `retry ${formatTime(provider.next_retry_at)}`
                : provider.last_error_class || (provider.min_soak_until ? `soak until ${formatTime(provider.min_soak_until)}` : 'no active error') }}
            </span>
          </div>
          <div v-if="provider.rollout_blockers.length" class="rollout-blockers">
            <strong>Promotion blockers</strong>
            <span v-for="blocker in provider.rollout_blockers.slice(0, 3)" :key="blocker">{{ blocker }}</span>
            <span v-if="provider.rollout_blockers.length > 3">
              +{{ provider.rollout_blockers.length - 3 }} more in rollout-status JSON
            </span>
          </div>
          <details v-if="provider.sources.length" class="source-details">
            <summary>
              {{ provider.sources.length }} capability observation{{ provider.sources.length === 1 ? '' : 's' }}
            </summary>
            <div class="source-matrix">
              <div v-for="source in provider.sources" :key="source.source_key" class="source-row">
                <span>
                  <span class="mono">{{ source.source_key }}</span>
                  <small>{{ source.capability || 'other' }}</small>
                </span>
                <StatusBadge
                  :variant="statusVariant(source.status)"
                  :label="source.status"
                />
                <span class="detail-meta mono">
                  {{ source.success_count }}/{{ source.attempt_count }}
                  {{ source.success_rate_pct ? `(${source.success_rate_pct}%)` : '' }}
                </span>
                <span class="detail-meta mono">
                  {{ source.matched_asset_count
                    ? `${source.matched_asset_count} matched · ${source.written_count || source.received_count} rows`
                    : source.next_retry_at
                      ? `retry ${formatTime(source.next_retry_at)}`
                      : `success ${formatTime(source.last_success_at || null)}` }}
                </span>
              </div>
            </div>
          </details>
        </div>
        <div
          v-if="!(overview.data.value?.provider_statuses?.length)"
          class="provider-empty"
        >
          No provider observations recorded yet.
        </div>
      </div>
      </template>
    </template>
  </section>
</template>

<style scoped>
.catalog-view :deep(.page-header) {
  display: none;
}

.section-heading {
  display: flex;
  align-items: end;
  justify-content: space-between;
  margin: 4px 0 12px;
}

.section-heading h2 {
  font-size: 15px;
}

.section-heading p {
  margin: 3px 0 0;
  color: var(--text-3);
  font-size: 12px;
}

.provider-heading {
  margin-top: 24px;
}

.detail-card {
  overflow: hidden;
}

.detail-row {
  display: grid;
  grid-template-columns: minmax(120px, 1fr) auto auto auto;
  align-items: center;
  gap: 16px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}

.detail-row:last-child {
  border-bottom: 0;
}

.detail-name {
  font-weight: 600;
}

.provider-row {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) auto minmax(180px, auto) minmax(140px, auto);
  align-items: center;
  gap: 16px;
  padding: 14px 18px;
}

.provider-block {
  border-bottom: 1px solid var(--border);
}

.provider-block:last-child {
  border-bottom: 0;
}

.source-matrix {
  margin-top: 8px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface-2);
}

.source-details {
  margin: -4px 18px 12px;
}

.source-details summary {
  width: fit-content;
  cursor: pointer;
  color: var(--text-3);
  font-size: 12px;
  user-select: none;
}

.source-details[open] summary {
  color: var(--text-2);
}

.source-row {
  display: grid;
  grid-template-columns: minmax(130px, 1fr) auto minmax(130px, auto) minmax(190px, auto);
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  font-size: 12px;
}

.source-row:last-child {
  border-bottom: 0;
}

.provider-name {
  display: block;
  text-transform: capitalize;
}

.provider-meta {
  color: var(--text-3);
  font-size: 12px;
}

.evidence-line {
  display: block;
  margin-top: 3px;
}

.rollout-blockers {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 12px;
  margin: -4px 18px 12px;
  color: var(--text-3);
  font-size: 11px;
}

.rollout-blockers strong {
  color: var(--text-2);
}

.source-row small {
  display: block;
  margin-top: 2px;
  color: var(--text-3);
  text-transform: capitalize;
}

.provider-empty {
  padding: 28px 18px;
  color: var(--text-3);
  text-align: center;
}

.detail-meta {
  color: var(--text-3);
  font-size: 12px;
  white-space: nowrap;
}

@media (max-width: 767px) {
  .detail-row {
    grid-template-columns: 1fr auto;
    row-gap: 6px;
  }

  .provider-row {
    grid-template-columns: 1fr auto;
  }

  .source-row {
    grid-template-columns: 1fr auto;
  }
}
</style>
