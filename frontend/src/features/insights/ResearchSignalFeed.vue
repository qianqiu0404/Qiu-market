<script setup lang="ts">
import { computed } from 'vue'
import StatusBadge, { type BadgeVariant } from '../../components/StatusBadge.vue'
import type {
  ResearchFeedStatus,
  ResearchSignalEvent,
  ResearchSignalEventPage,
  ResearchSignalSummary,
} from '../../api/research'
import { useI18n } from '../../i18n'

const props = defineProps<{
  feed: ResearchSignalEventPage | null
  summary: ResearchSignalSummary | null
  loading: boolean
  error: string | null
}>()
const emit = defineEmits<{ retry: [] }>()
const { locale } = useI18n()

const copy = computed(() => locale.value === 'zh-CN' ? {
  title: 'BTC 研究信息流',
  description: '统一展示事件来源、时间与待观察条件；不生成方向判断，也不提供可执行价格。',
  nonExecutable: '研究信息 · 不可执行',
  retry: '重试',
  source: '来源',
  provider: '接入方',
  eventTime: '事件时间',
  publishedAt: '发布时间',
  receivedAt: '接收时间',
  observedAt: '观察时间',
  notSupplied: '未提供',
  priority: '研究优先级（非交易建议）',
  watchFor: '继续观察',
  invalidation: '失效条件',
  quality: '质量标记',
  noQuality: '无附加标记',
  sourceStatus: '来源状态',
  events24h: '24 小时事件',
  delayed: '来源延迟',
  emptyTitle: '当前窗口没有研究事件',
  emptyMessage: '这表示当前窗口确实为空，不代表数据源不可用。',
  unconfiguredTitle: '研究来源尚未配置',
  unconfiguredMessage: '来源未启用；不能将此状态解释为没有事件。',
  legacyTitle: '旧版来源语义',
  legacyMessage: '内容可供审阅，但缺少当前契约的完整新鲜度证据。',
  degradedTitle: '研究来源降级',
  degradedMessage: '部分来源不可用；保留已验证内容并明确标记缺口。',
  staleTitle: '研究信息已过期',
  staleMessage: '这些事件仅供历史参考，不应用于当前判断。',
  partialTitle: '研究信息不完整',
  partialMessage: '只展示已验证字段；缺失字段不会被推测补齐。',
  loadErrorTitle: '研究信息请求失败',
  loading: '正在加载研究信息…',
  fresh: '新鲜', empty: '空', legacy: '旧版', degraded: '降级', stale: '过期', partial: '部分', unconfigured: '未配置',
  healthy: '正常',
} : {
  title: 'BTC Research Feed',
  description: 'Source-, time-, and watch-condition-aware events without directional judgment or executable prices.',
  nonExecutable: 'Research information · Not executable',
  retry: 'Retry',
  source: 'Source',
  provider: 'Provider',
  eventTime: 'Event time',
  publishedAt: 'Published',
  receivedAt: 'Received',
  observedAt: 'Observed',
  notSupplied: 'Not supplied',
  priority: 'Research priority (not trading advice)',
  watchFor: 'Watch for',
  invalidation: 'Invalidation',
  quality: 'Quality flags',
  noQuality: 'No additional flags',
  sourceStatus: 'Source status',
  events24h: '24h events',
  delayed: 'Source delayed',
  emptyTitle: 'No research events in this window',
  emptyMessage: 'This is a verified empty window, not an unavailable source.',
  unconfiguredTitle: 'Research source is not configured',
  unconfiguredMessage: 'The source is disabled; this must not be interpreted as no events.',
  legacyTitle: 'Legacy source semantics',
  legacyMessage: 'Content remains reviewable, but current-contract freshness evidence is incomplete.',
  degradedTitle: 'Research source degraded',
  degradedMessage: 'Some sources are unavailable; verified content and gaps remain explicit.',
  staleTitle: 'Research information is stale',
  staleMessage: 'These events are historical context and should not inform current decisions.',
  partialTitle: 'Research information is partial',
  partialMessage: 'Only verified fields are shown; absent fields are never inferred.',
  loadErrorTitle: 'Research request failed',
  loading: 'Loading research information…',
  fresh: 'Fresh', empty: 'Empty', legacy: 'Legacy', degraded: 'Degraded', stale: 'Stale', partial: 'Partial', unconfigured: 'Unconfigured',
  healthy: 'Healthy',
})

const status = computed<ResearchFeedStatus>(() => {
  if (props.error) return 'degraded'
  const values = [props.feed?.status, props.summary?.status].filter((value): value is ResearchFeedStatus => Boolean(value))
  // Never let a successful/empty list hide a less trustworthy summary state.
  for (const candidate of ['unconfigured', 'degraded', 'stale', 'partial', 'legacy'] as ResearchFeedStatus[]) {
    if (values.includes(candidate)) return candidate
  }
  if (props.feed?.data.nextCursor) return 'partial'
  return props.feed?.status ?? props.summary?.status ?? 'unconfigured'
})
const items = computed(() => props.feed?.data.items ?? [])
const contractError = computed(() => props.feed?.error?.message ?? props.summary?.error?.message ?? null)

function statusVariant(value: ResearchFeedStatus): BadgeVariant {
  if (value === 'fresh') return 'live'
  if (value === 'empty' || value === 'legacy' || value === 'partial') return 'delayed'
  if (value === 'stale') return 'stale'
  return 'offline'
}

function statusLabel(value: ResearchFeedStatus): string {
  return copy.value[value]
}

function notice(value: ResearchFeedStatus): { title: string; message: string } | null {
  switch (value) {
  case 'empty': return { title: copy.value.emptyTitle, message: copy.value.emptyMessage }
  case 'legacy': return { title: copy.value.legacyTitle, message: copy.value.legacyMessage }
  case 'degraded': return { title: copy.value.degradedTitle, message: copy.value.degradedMessage }
  case 'stale': return { title: copy.value.staleTitle, message: copy.value.staleMessage }
  case 'partial': return { title: copy.value.partialTitle, message: copy.value.partialMessage }
  case 'unconfigured': return { title: copy.value.unconfiguredTitle, message: copy.value.unconfiguredMessage }
  default: return null
  }
}

function formatTime(value: string | null): string {
  if (!value) return copy.value.notSupplied
  return new Intl.DateTimeFormat(locale.value, {
    dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC',
  }).format(new Date(value)) + ' UTC'
}

function safeSourceURL(item: ResearchSignalEvent): string | null {
  try {
    const parsed = new URL(item.sourceUrl)
    const prefix = '/market-radar/events/'
    const encodedID = parsed.pathname.startsWith(prefix) ? parsed.pathname.slice(prefix.length) : ''
    return parsed.origin === 'https://xiuqiu-site.vercel.app'
      && !parsed.username && !parsed.password && !parsed.search && !parsed.hash
      && decodeURIComponent(encodedID) === item.id
      ? parsed.toString() : null
  } catch {
    return null
  }
}

function sourceStateLabel(value: 'healthy' | 'degraded' | 'unconfigured'): string {
  if (value === 'healthy') return copy.value.healthy
  return statusLabel(value)
}
</script>

<template>
  <section class="research-feed insight-section" data-testid="research-signal-feed">
    <div class="research-heading">
      <div>
        <span class="research-eyebrow">{{ copy.nonExecutable }}</span>
        <h2>{{ copy.title }}</h2>
        <p>{{ copy.description }}</p>
      </div>
      <StatusBadge
        data-testid="research-feed-status"
        :variant="statusVariant(status)"
        :label="statusLabel(status)"
      />
    </div>

    <div v-if="summary" class="research-summary" data-testid="research-summary">
      <div>
        <span>{{ copy.events24h }}</span>
        <strong class="num">{{ summary.data.eventCount24h }}</strong>
        <small>P0 {{ summary.data.p0Count24h }} · P1 {{ summary.data.p1Count24h }}</small>
      </div>
      <div v-for="source in summary.data.sources" :key="source.source">
        <span>{{ copy.sourceStatus }}</span>
        <strong>{{ source.source }}</strong>
        <small>{{ sourceStateLabel(source.status) }} · {{ formatTime(source.lastSuccessAt) }}</small>
        <small v-if="source.message">{{ source.message }}</small>
      </div>
      <div v-if="summary.data.isDelayed">
        <span>{{ copy.delayed }}</span>
        <strong>{{ summary.data.freshnessMinutes ?? copy.notSupplied }}</strong>
        <small v-if="summary.data.freshnessMinutes !== null">min</small>
      </div>
    </div>

    <div v-if="loading && !feed" class="research-state card" aria-live="polite">
      {{ copy.loading }}
    </div>
    <div v-else-if="error && !feed" class="research-state research-state--degraded card" role="alert">
      <strong>{{ copy.loadErrorTitle }}</strong>
      <span>{{ error }}</span>
      <button type="button" class="btn" @click="emit('retry')">{{ copy.retry }}</button>
    </div>
    <div
      v-else-if="notice(status)"
      class="research-state card"
      :class="`research-state--${status}`"
      :data-testid="`research-state-${status}`"
    >
      <strong>{{ notice(status)?.title }}</strong>
      <span>{{ notice(status)?.message }}</span>
      <span v-if="error || contractError">{{ error ?? contractError }}</span>
      <button v-if="status !== 'empty'" type="button" class="btn" @click="emit('retry')">{{ copy.retry }}</button>
    </div>

    <div v-if="items.length" class="research-list">
      <article
        v-for="item in items"
        :key="item.id"
        class="research-card card"
        :data-testid="`research-event-${item.id}`"
      >
        <header>
          <div class="research-title">
            <div class="research-tags">
              <span class="priority" :class="`priority--${item.editorialPriority.toLowerCase()}`">{{ copy.priority }} {{ item.editorialPriority }}</span>
              <StatusBadge :variant="statusVariant(item.freshness)" :label="statusLabel(item.freshness)" :dot="false" />
              <span v-for="asset in item.assets" :key="asset" class="asset-tag">{{ asset }}</span>
            </div>
            <h3>{{ item.title }}</h3>
          </div>
          <span class="non-executable">{{ copy.nonExecutable }}</span>
        </header>
        <p class="research-summary-text">{{ item.summary }}</p>

        <dl class="research-provenance">
          <div>
            <dt>{{ copy.source }}</dt>
            <dd>
              <a
                v-if="safeSourceURL(item)"
                :href="safeSourceURL(item) ?? undefined"
                target="_blank"
                rel="noopener noreferrer"
              >{{ item.source }}</a>
              <span v-else>{{ item.source }}</span>
            </dd>
          </div>
          <div><dt>{{ copy.provider }}</dt><dd>{{ item.provider }} · {{ item.sourceKind }}</dd></div>
          <div><dt>{{ copy.eventTime }}</dt><dd>{{ formatTime(item.eventTime) }}</dd></div>
          <div><dt>{{ copy.publishedAt }}</dt><dd>{{ formatTime(item.publishedAt) }}</dd></div>
          <div><dt>{{ copy.receivedAt }}</dt><dd>{{ formatTime(item.receivedAt) }}</dd></div>
          <div><dt>{{ copy.observedAt }}</dt><dd>{{ formatTime(item.observedAt) }}</dd></div>
        </dl>

        <div class="research-conditions">
          <div><span>{{ copy.watchFor }}</span><strong>{{ item.watchFor ?? copy.notSupplied }}</strong></div>
          <div><span>{{ copy.invalidation }}</span><strong>{{ item.invalidation ?? copy.notSupplied }}</strong></div>
        </div>
        <footer>
          <span>{{ copy.quality }}</span>
          <code v-if="!item.qualityFlags.length">{{ copy.noQuality }}</code>
          <code v-for="flag in item.qualityFlags" :key="flag">{{ flag }}</code>
        </footer>
      </article>
    </div>
  </section>
</template>

<style scoped>
.research-feed { min-width: 0; margin-bottom: 34px; }
.research-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 12px; }
.research-heading > div { min-width: 0; }
.research-heading h2 { margin-top: 3px; font-size: 18px; }
.research-heading p { max-width: 760px; margin: 5px 0 0; color: var(--text-3); font-size: 12px; overflow-wrap: anywhere; }
.research-eyebrow { color: var(--accent); font-size: 10px; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
.research-summary { display: grid; grid-template-columns: repeat(auto-fit,minmax(180px,1fr)); gap: 8px; margin-bottom: 10px; }
.research-summary > div { display: grid; min-width: 0; gap: 2px; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-panel-2); }
.research-summary span,.research-summary small { color: var(--text-3); font-size: 10px; overflow-wrap: anywhere; }
.research-summary strong { font-size: 12px; overflow-wrap: anywhere; }
.research-state { display: grid; justify-items: start; gap: 7px; padding: 20px; color: var(--text-2); overflow-wrap: anywhere; }
.research-state--empty { border-color: var(--border); }
.research-state--legacy,.research-state--partial { border-color: #f0d5a9; background: #fffaf1; }
.research-state--degraded,.research-state--stale { border-color: #f5c9d1; background: #fff7f8; }
.research-state--unconfigured { background: #f7f7f8; }
.research-list { display: grid; gap: 10px; min-width: 0; }
.research-card { min-width: 0; padding: 18px; overflow: hidden; }
.research-card header { display: flex; align-items: flex-start; justify-content: space-between; gap: 14px; }
.research-title { min-width: 0; }
.research-title h3 { margin-top: 8px; font-size: 16px; overflow-wrap: anywhere; }
.research-tags { display: flex; flex-wrap: wrap; gap: 6px; }
.priority,.asset-tag,.non-executable { display: inline-flex; padding: 3px 8px; border-radius: 999px; font-size: 10px; white-space: nowrap; }
.priority { color: var(--accent); background: var(--accent-soft); }
.priority--p0 { color: var(--down); background: #fff0f2; }
.asset-tag { color: var(--text-2); background: var(--bg-panel-2); border: 1px solid var(--border); }
.non-executable { flex: none; color: var(--warn); background: #fff5e5; border: 1px solid #f0d5a9; }
.research-summary-text { margin: 12px 0; color: var(--text-2); font-size: 13px; white-space: pre-wrap; overflow-wrap: anywhere; }
.research-provenance { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 8px; margin: 0; }
.research-provenance > div,.research-conditions > div { min-width: 0; padding: 9px 10px; border-radius: 10px; background: var(--bg-panel-2); }
.research-provenance dt,.research-conditions span { color: var(--text-3); font-size: 9px; text-transform: uppercase; }
.research-provenance dd { margin: 3px 0 0; font: 10px var(--font-mono); overflow-wrap: anywhere; }
.research-provenance a { display: inline-flex; min-width: 44px; min-height: 44px; align-items: center; }
.research-conditions { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-top: 8px; }
.research-conditions > div { display: grid; gap: 4px; }
.research-conditions strong { color: var(--text-2); font-size: 12px; font-weight: 500; white-space: pre-wrap; overflow-wrap: anywhere; }
.research-card footer { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-top: 10px; color: var(--text-3); font-size: 10px; }
.research-card code { max-width: 100%; padding: 2px 6px; border-radius: 6px; background: #f0f2f5; color: var(--text-2); overflow-wrap: anywhere; }
@media(max-width:760px){
  .research-heading,.research-card header { align-items: stretch; flex-direction: column; }
  .research-heading .badge,.non-executable { align-self: flex-start; white-space: normal; }
  .research-provenance { grid-template-columns: 1fr 1fr; }
  .research-conditions { grid-template-columns: 1fr; }
}
@media(max-width:480px){
  .research-provenance { grid-template-columns: 1fr; }
  .research-card { padding: 14px; }
}
</style>
