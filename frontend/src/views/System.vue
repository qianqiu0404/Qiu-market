<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '../components/PageHeader.vue'
import StatusBadge from '../components/StatusBadge.vue'
import SkeletonRows from '../components/SkeletonRows.vue'
import ErrorState from '../components/ErrorState.vue'
import { usePolling } from '../composables/usePolling'
import {
  getSystemStatus,
  SYSTEM_STATE_LABELS,
} from '../api/system'
import { formatDelay, formatTime } from '../utils/format'
import type { BadgeVariant } from '../components/StatusBadge.vue'
import type {
  OptionalMetric,
  StatusEvidence,
  SystemState,
} from '../api/system'

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

/* 15s poll for the read-only observability view. */
const system = usePolling(getSystemStatus, { interval: 15_000 })

const componentLabels: Record<string, string> = {
  matching: 'Matching',
  liquidity: 'Liquidity',
  transport: 'Transport',
  market_data: 'Market data',
  outbox: 'Outbox',
  database: 'Database',
  disk: 'Disk',
  retention: 'Retention',
}

const componentRows = computed(() => {
  const components = system.data.value?.components
  if (!components) return []
  return Object.entries(components).map(([key, status]) => ({
    key,
    label: componentLabels[key] ?? key,
    status,
  }))
})

function evidenceVariant(state: SystemState): BadgeVariant {
  switch (state) {
  case 'live':
    return 'live'
  case 'cached':
    return 'stale'
  case 'demo_snapshot':
    return 'accent'
  case 'degraded':
    return 'error'
  case 'offline':
    return 'offline'
  default:
    return 'stale'
  }
}

function providerStatusVariant(status: string): BadgeVariant {
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

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let amount = value
  let unit = 0
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024
    unit += 1
  }
  return `${amount.toFixed(unit >= 3 ? 1 : 0)} ${units[unit]}`
}

function metricLabel(
  metric: OptionalMetric | undefined,
  formatter: (value: number) => string,
): string {
  if (!metric?.available || metric.value === null) {
    return `Unavailable · ${metric?.reason || 'No reason reported'}`
  }
  return formatter(metric.value)
}

function bytesMetric(metric: OptionalMetric | undefined): string {
  return metricLabel(metric, formatBytes)
}

function numberMetric(metric: OptionalMetric | undefined): string {
  return metricLabel(metric, (value) => value.toLocaleString('en-US'))
}

function timeMetric(metric: OptionalMetric | undefined): string {
  return metricLabel(metric, (value) => formatTime(value))
}

function evidenceLastSuccess(status: StatusEvidence): string {
  return status.last_success_at === null
    ? `Unavailable · ${status.reason}`
    : formatTime(status.last_success_at)
}

function evidenceAge(status: StatusEvidence): string {
  return status.age_seconds === null
    ? 'Age unavailable'
    : `${formatDelay(status.age_seconds)} old`
}

function stateLabel(state: SystemState | undefined): string {
  return SYSTEM_STATE_LABELS[state ?? 'unknown']
}

function sourceModeLabel(value: string | undefined): string {
  switch (value) {
  case 'native':
    return 'Native system-status contract'
  case 'legacy':
    return 'Legacy backend compatibility'
  case 'demo_snapshot':
    return 'Explicit demo snapshot'
  default:
    return 'Unknown source mode'
  }
}
</script>

<template>
  <section>
    <PageHeader
      :title="pageTitle"
      :subtitle="pageSubtitle"
      :refreshed-at="activeTab === 'status' ? system.lastUpdated.value : null"
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
      <SkeletonRows v-if="system.loading.value" :rows="5" />
      <ErrorState
        v-else-if="system.error.value && !system.data.value"
        :message="system.error.value"
        @retry="system.refresh"
      />
      <template v-else-if="system.data.value">
        <div
          class="card status-summary"
          :data-state="system.data.value.overall.state"
        >
          <div>
            <span class="summary-kicker">
              {{ sourceModeLabel(system.data.value.source_mode) }} ·
              {{ system.data.value.formula_version }}
            </span>
            <h2>{{ stateLabel(system.data.value.overall.state) }}</h2>
            <p>{{ system.data.value.overall.reason }}</p>
          </div>
          <StatusBadge
            :variant="evidenceVariant(system.data.value.overall.state)"
            :label="stateLabel(system.data.value.overall.state)"
          />
          <div class="formula-note">
            <strong>Status formula</strong>
            <span>LIVE requires explicit current success from all eight required probes.</span>
            <span>CACHED is allowed only when market data alone is 30s–5m old.</span>
            <span>DEMO SNAPSHOT requires an explicit source flag; missing fields become DEGRADED, never LIVE.</span>
            <span>OFFLINE means both trading transport and the database-backed market view are unavailable.</span>
          </div>
        </div>

        <div class="section-heading provider-heading">
          <div>
            <h2>Runtime truth</h2>
            <p>Each state carries its own last success, age, reason, and source.</p>
          </div>
        </div>
        <div class="component-grid">
          <article
            v-for="row in componentRows"
            :key="row.key"
            class="card component-card"
            :data-component="row.key"
          >
            <div class="component-title">
              <h3>{{ row.label }}</h3>
              <StatusBadge
                :variant="evidenceVariant(row.status.state)"
                :label="stateLabel(row.status.state)"
              />
            </div>
            <p>{{ row.status.reason }}</p>
            <div class="component-meta mono">
              <span>success {{ evidenceLastSuccess(row.status) }}</span>
              <span>{{ evidenceAge(row.status) }}</span>
            </div>
            <small>{{ row.status.source }}</small>
          </article>
        </div>

        <div class="section-heading provider-heading">
          <div>
            <h2>Price sources</h2>
            <p>Route price and reference display price are deliberately separate facts.</p>
          </div>
        </div>
        <div class="price-source-grid">
          <article
            v-for="priceSource in system.data.value.price_sources"
            :key="priceSource.key"
            class="card price-source-card"
            :data-price-source="priceSource.key"
          >
            <div class="component-title">
              <h3>{{ priceSource.label }}</h3>
              <StatusBadge
                :variant="evidenceVariant(priceSource.status.state)"
                :label="stateLabel(priceSource.status.state)"
              />
            </div>
            <dl>
              <div>
                <dt>Source</dt>
                <dd>{{ priceSource.source }}</dd>
              </div>
              <div>
                <dt>Meaning</dt>
                <dd>{{ priceSource.meaning }}</dd>
              </div>
              <div>
                <dt>Boundary</dt>
                <dd>{{ priceSource.boundary }}</dd>
              </div>
              <div>
                <dt>Last success</dt>
                <dd class="mono">
                  {{ evidenceLastSuccess(priceSource.status) }} ·
                  {{ evidenceAge(priceSource.status) }}
                </dd>
              </div>
            </dl>
          </article>
        </div>

        <div class="section-heading provider-heading">
          <div>
            <h2>Processes & dependencies</h2>
            <p>A heartbeat proves the process is running, not that its upstream source is usable.</p>
          </div>
        </div>
        <div class="card detail-card">
        <div
          v-for="row in system.data.value.processes"
          :key="row.key"
          class="detail-row"
        >
          <span class="detail-name">{{ row.label }}</span>
          <StatusBadge
            :variant="evidenceVariant(row.status.state)"
            :label="stateLabel(row.status.state)"
          />
          <span class="detail-meta mono">
            reported {{ row.raw_status }}
          </span>
          <span class="detail-meta detail-reason">
            {{ row.status.reason }} · {{ row.status.source }}
          </span>
        </div>
        </div>

        <div class="section-heading provider-heading">
          <div>
            <h2>Storage & retention</h2>
            <p>Missing metrics show Unavailable with a reason; they never become zero or healthy by default.</p>
          </div>
        </div>
        <div class="card detail-card">
        <div class="detail-row">
          <span class="detail-name">Mac mini free disk</span>
          <StatusBadge
            :variant="evidenceVariant(system.data.value.components.disk.state)"
            :label="stateLabel(system.data.value.components.disk.state)"
          />
          <span class="detail-meta mono">
            {{ bytesMetric(system.data.value.storage.disk_free_bytes) }}
          </span>
          <span class="detail-meta detail-reason">
            {{ system.data.value.components.disk.reason }} ·
            warning &lt;{{ formatBytes(system.data.value.storage.warning_below_bytes) }} ·
            critical &lt;{{ formatBytes(system.data.value.storage.critical_below_bytes) }}
          </span>
        </div>
        <div class="detail-row">
          <span class="detail-name">PostgreSQL / K-lines</span>
          <StatusBadge
            :variant="evidenceVariant(system.data.value.components.database.state)"
            :label="stateLabel(system.data.value.components.database.state)"
          />
          <span class="detail-meta mono">
            DB {{ bytesMetric(system.data.value.storage.database_bytes) }} ·
            K-lines {{ bytesMetric(system.data.value.storage.kline_table_bytes) }}
          </span>
          <span class="detail-meta detail-reason mono">
            heap {{ bytesMetric(system.data.value.storage.kline_heap_bytes) }} ·
            indexes {{ bytesMetric(system.data.value.storage.kline_index_bytes) }} ·
            rows {{ numberMetric(system.data.value.storage.kline_estimated_rows) }}
          </span>
        </div>
        <div
          v-for="item in system.data.value.storage.kline_intervals"
          :key="item.interval"
          class="detail-row"
        >
          <span class="detail-name mono">{{ item.interval }} candles</span>
          <StatusBadge
            :variant="item.oldest_at.available || item.newest_at.available ? 'accent' : 'stale'"
            :label="item.oldest_at.available || item.newest_at.available ? 'OBSERVED' : 'UNAVAILABLE'"
          />
          <span class="detail-meta mono">oldest {{ timeMetric(item.oldest_at) }}</span>
          <span class="detail-meta detail-reason mono">
            newest {{ timeMetric(item.newest_at) }} ·
            policy {{ item.interval === '1d' ? 'indefinite' : 'bounded' }}
          </span>
        </div>
        <div class="detail-row">
          <span class="detail-name">Retention job</span>
          <StatusBadge
            :variant="evidenceVariant(system.data.value.components.retention.state)"
            :label="stateLabel(system.data.value.components.retention.state)"
          />
          <span class="detail-meta mono">
            success {{ timeMetric(system.data.value.storage.retention_last_success_at) }} ·
            started {{ timeMetric(system.data.value.storage.retention_last_started_at) }}
          </span>
          <span class="detail-meta detail-reason">
            {{ system.data.value.storage.retention_last_error ||
              system.data.value.components.retention.reason }}
          </span>
        </div>
        <div class="detail-row">
          <span class="detail-name">Deleted rows · last run</span>
          <StatusBadge variant="accent" label="EVIDENCE" />
          <span class="detail-meta mono">
            1m {{ numberMetric(system.data.value.storage.retention_deleted_rows['1m']) }} ·
            15m {{ numberMetric(system.data.value.storage.retention_deleted_rows['15m']) }}
          </span>
          <span class="detail-meta detail-reason mono">
            1h {{ numberMetric(system.data.value.storage.retention_deleted_rows['1h']) }}
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
          v-for="provider in system.data.value.provider_statuses"
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
              :variant="providerStatusVariant(provider.status)"
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
                  :variant="providerStatusVariant(source.status)"
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
          v-if="!system.data.value.provider_statuses.length"
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

.status-summary {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 18px 24px;
  padding: 22px;
}

.summary-kicker {
  color: var(--text-3);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.status-summary h2 {
  margin: 5px 0 4px;
  font-size: 28px;
  letter-spacing: -0.02em;
}

.status-summary p,
.component-card p {
  margin: 0;
  color: var(--text-2);
  font-size: 13px;
  line-height: 1.5;
}

.formula-note {
  display: grid;
  grid-column: 1 / -1;
  gap: 4px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
  color: var(--text-3);
  font-size: 11px;
  line-height: 1.45;
}

.formula-note strong {
  color: var(--text-2);
}

.component-grid,
.price-source-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

.price-source-grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.component-card,
.price-source-card {
  min-width: 0;
  padding: 16px;
}

.component-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 10px;
}

.component-title h3 {
  font-size: 13px;
}

.component-meta {
  display: grid;
  gap: 3px;
  margin-top: 12px;
  color: var(--text-3);
  font-size: 11px;
}

.component-card small {
  display: block;
  margin-top: 8px;
  color: var(--text-3);
  font-size: 10px;
  overflow-wrap: anywhere;
}

.price-source-card dl {
  display: grid;
  gap: 10px;
  margin: 0;
}

.price-source-card dl div {
  display: grid;
  grid-template-columns: 92px minmax(0, 1fr);
  gap: 12px;
}

.price-source-card dt {
  color: var(--text-3);
  font-size: 11px;
}

.price-source-card dd {
  margin: 0;
  color: var(--text-2);
  font-size: 12px;
  line-height: 1.45;
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

.detail-reason {
  max-width: 520px;
  white-space: normal;
  text-align: right;
  overflow-wrap: anywhere;
}

@media (max-width: 1180px) {
  .component-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .detail-row,
  .provider-row {
    grid-template-columns: minmax(160px, 1fr) auto minmax(160px, auto);
  }

  .detail-row > :last-child,
  .provider-row > :last-child {
    grid-column: 1 / -1;
    max-width: none;
    text-align: left;
  }
}

@media (max-width: 767px) {
  .status-summary,
  .component-grid,
  .price-source-grid {
    grid-template-columns: 1fr;
  }

  .status-summary > .badge {
    justify-self: start;
  }

  .price-source-card dl div {
    grid-template-columns: 1fr;
    gap: 2px;
  }

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

  .detail-row > :last-child,
  .provider-row > :last-child,
  .source-row > :last-child {
    grid-column: 1 / -1;
    text-align: left;
  }
}
</style>
