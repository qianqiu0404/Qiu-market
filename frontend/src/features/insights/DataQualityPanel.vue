<script setup lang="ts">
import { computed } from 'vue'
import StatusBadge, { type BadgeVariant } from '../../components/StatusBadge.vue'
import type {
  DataQualityCounter, DataQualityItem, DataQualityOverallStatus, DataQualitySummary,
} from '../../api/dataQuality'
import { useI18n } from '../../i18n'

const props = defineProps<{ summary: DataQualitySummary | null; loading: boolean; error: string | null }>()
const emit = defineEmits<{ retry: [] }>()
const { locale } = useI18n()

const copy = computed(() => locale.value === 'zh-CN' ? {
  title: '数据质量门', description: '三个来源分别评分；每项能力保留自己的样本、新鲜度与缺口，不复用来源分数。',
  boundary: '只读质量 · 不是交易建议', retry: '重试', loading: '正在加载质量报告…', unavailable: '质量报告不可用',
  source: '来源 / 分类', capabilities: '能力证据', window: '证据窗口', samples: '样本 / 最低要求',
  attempts: '成功 / 尝试', lastAttempt: '最近尝试', lastSuccess: '最近成功', age: '年龄', coverage: '覆盖率',
  score: '来源技术分 / 等级', status: '状态', reasons: '原因', license: '许可', publicUse: '公开展示',
  readOnlyUse: '只读用途', tradeUse: '交易资格', dimensions: '六项稳定维度', errors: '错误分布 / 缓存',
  cacheHits: '缓存命中', staleServes: '过期缓存返回', yes: '是', no: '否', insufficient: '样本不足',
  recovery: '恢复窗口', priority: '研究优先级分布', noReasons: '无附加原因', notAvailable: '未提供', seconds: '秒', noErrors: '无错误计数',
} : {
  title: 'Data Quality Gate', description: 'Three sources are scored independently; every capability keeps its own samples, freshness, and gaps.',
  boundary: 'Read-only quality · not trading advice', retry: 'Retry', loading: 'Loading quality reports…', unavailable: 'Quality report unavailable',
  source: 'Source / class', capabilities: 'Capability evidence', window: 'Evidence window', samples: 'Samples / minimum',
  attempts: 'Success / attempts', lastAttempt: 'Last attempt', lastSuccess: 'Last success', age: 'Age', coverage: 'Coverage',
  score: 'Source technical score / grade', status: 'Status', reasons: 'Reasons', license: 'License', publicUse: 'Public display',
  readOnlyUse: 'Read-only use', tradeUse: 'Trade eligible', dimensions: 'Six stable dimensions', errors: 'Error distribution / cache',
  cacheHits: 'Cache hits', staleServes: 'Stale serves', yes: 'Yes', no: 'No', insufficient: 'Insufficient evidence',
  recovery: 'Recovery windows', priority: 'Research priority distribution', noReasons: 'No additional reasons', notAvailable: 'Not available', seconds: 'seconds', noErrors: 'No error counts',
})

const status = computed<DataQualityOverallStatus>(() => props.error ? 'degraded' : props.summary?.status ?? 'unconfigured')
const primaryMetrics = new Set(['freshness', 'availability', 'completeness', 'schema', 'consistency', 'coverage'])

function variant(value: DataQualityOverallStatus): BadgeVariant {
  if (value === 'healthy') return 'live'
  if (value === 'insufficient' || value === 'recovering') return 'delayed'
  if (value === 'quarantined') return 'stale'
  return 'offline'
}

function label(value: string): string { return value.replace(/_/g, ' ') }
function formatTime(value: string | null): string {
  if (!value) return copy.value.notAvailable
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium', timeZone: 'UTC' }).format(new Date(value)) + ' UTC'
}
function ratio(value: DataQualityCounter): string {
  if (value.bps === null) return `${value.numerator}/${value.denominator} · ${copy.value.insufficient}`
  return `${value.numerator}/${value.denominator} · ${(value.bps / 100).toFixed(2)}%`
}
function score(item: DataQualityItem): string {
  if (item.technicalScoreBps === null || item.grade === null) return copy.value.insufficient
  return `${(item.technicalScoreBps / 100).toFixed(2)} · ${item.grade}`
}
function errors(item: DataQualityItem): Array<[string, number]> {
  return Object.entries(item.errorCounts).filter((entry): entry is [string, number] => typeof entry[1] === 'number' && entry[1] > 0)
}
</script>

<template>
  <section class="quality-panel insight-section" data-testid="data-quality-panel">
    <div class="quality-heading">
      <div><span class="quality-eyebrow">{{ copy.boundary }}</span><h2>{{ copy.title }}</h2><p>{{ copy.description }}</p></div>
      <StatusBadge data-testid="data-quality-status" :variant="variant(status)" :label="label(status)" />
    </div>

    <div v-if="loading && !summary" class="quality-state card" aria-live="polite">{{ copy.loading }}</div>
    <div v-else-if="error && !summary" class="quality-state card" role="alert">
      <strong>{{ copy.unavailable }}</strong><span>{{ error }}</span><button type="button" class="btn" @click="emit('retry')">{{ copy.retry }}</button>
    </div>
    <div v-else-if="summary?.error" class="quality-state card" role="alert">
      <strong>{{ copy.unavailable }}</strong><span>{{ summary.error }}</span><button type="button" class="btn" @click="emit('retry')">{{ copy.retry }}</button>
    </div>

    <div v-if="summary" class="quality-grid">
      <article v-for="item in summary.items" :key="item.source" class="quality-card card" :data-testid="`quality-${item.source}`">
        <header>
          <div><strong>{{ item.sourceName }}</strong><small>{{ label(item.source) }} · {{ item.class }}</small></div>
          <StatusBadge :variant="variant(item.status)" :label="label(item.status)" :dot="false" />
        </header>
        <span class="quality-boundary">{{ copy.boundary }}</span>
        <dl class="source-facts">
          <div><dt>{{ copy.window }}</dt><dd>{{ formatTime(item.windowStart) }} → {{ formatTime(item.windowEnd) }}</dd></div>
          <div><dt>{{ copy.samples }}</dt><dd>{{ item.sampleCount }} / {{ item.minSamples }}</dd></div>
          <div><dt>{{ copy.attempts }}</dt><dd>{{ item.successCount }} / {{ item.attemptCount }}</dd></div>
          <div><dt>{{ copy.lastAttempt }}</dt><dd>{{ formatTime(item.lastAttemptAt) }}</dd></div>
          <div><dt>{{ copy.lastSuccess }}</dt><dd>{{ formatTime(item.lastSuccessAt) }}</dd></div>
          <div><dt>{{ copy.age }}</dt><dd>{{ item.ageSeconds === null ? copy.notAvailable : `${item.ageSeconds} ${copy.seconds}` }}</dd></div>
          <div><dt>{{ copy.coverage }}</dt><dd>{{ ratio(item.coverage) }}</dd></div>
          <div><dt>{{ copy.score }}</dt><dd>{{ score(item) }}</dd></div>
          <div><dt>{{ copy.license }}</dt><dd>{{ label(item.license) }}</dd></div>
          <div><dt>{{ copy.publicUse }}</dt><dd>{{ item.publicEligible ? copy.yes : copy.no }}</dd></div>
          <div><dt>{{ copy.readOnlyUse }}</dt><dd>{{ label(item.readOnlyUse) }}</dd></div>
          <div><dt>{{ copy.tradeUse }}</dt><dd>{{ copy.no }}</dd></div>
          <div><dt>{{ copy.recovery }}</dt><dd>{{ item.gate.healthyWindowStreak }} / {{ item.gate.recoveryRequired }}</dd></div>
          <div v-if="item.source === 'xiuqiu_research'"><dt>{{ copy.priority }}</dt><dd>P0={{ item.priorityCounts.p0 }} · P1={{ item.priorityCounts.p1 }} · P2={{ item.priorityCounts.p2 }}</dd></div>
        </dl>

        <section class="quality-subsection dimensions">
          <h3>{{ copy.dimensions }}</h3>
          <div class="dimension-grid">
            <div v-for="dimension in item.dimensions.filter((entry) => primaryMetrics.has(entry.metric))" :key="dimension.metric">
              <span>{{ label(dimension.metric) }}</span><strong>{{ ratio(dimension) }}</strong>
            </div>
          </div>
        </section>

        <section class="quality-subsection">
          <h3>{{ copy.capabilities }}</h3>
          <div class="capability-grid">
            <article v-for="capability in item.capabilities" :key="capability.capability" class="capability-card" :data-testid="`quality-capability-${capability.capability}`">
              <header><strong>{{ label(capability.capability) }}</strong><StatusBadge :variant="variant(capability.status)" :label="label(capability.status)" :dot="false" /></header>
              <dl>
                <div><dt>{{ copy.samples }}</dt><dd>{{ capability.validSampleCount }} / {{ capability.minSamples }}</dd></div>
                <div><dt>{{ copy.attempts }}</dt><dd>{{ capability.successCount }} / {{ capability.sampleCount }}</dd></div>
                <div><dt>{{ copy.lastSuccess }}</dt><dd>{{ formatTime(capability.lastSuccessAt) }}</dd></div>
                <div><dt>{{ copy.age }}</dt><dd>{{ capability.ageSeconds === null ? copy.notAvailable : `${capability.ageSeconds} ${copy.seconds}` }}</dd></div>
                <div><dt>{{ copy.coverage }}</dt><dd>{{ ratio(capability.coverage) }}</dd></div>
              </dl>
              <footer><code v-if="!capability.reasons.length">{{ copy.noReasons }}</code><code v-for="reason in capability.reasons" :key="reason">{{ reason }}</code></footer>
            </article>
          </div>
        </section>

        <section class="quality-subsection audit-counters">
          <h3>{{ copy.errors }}</h3>
          <div><code v-if="!errors(item).length">{{ copy.noErrors }}</code><code v-for="entry in errors(item)" :key="entry[0]">{{ label(entry[0]) }}={{ entry[1] }}</code><code>{{ copy.cacheHits }}={{ item.cacheHitCount }}</code><code>{{ copy.staleServes }}={{ item.staleServeCount }}</code></div>
        </section>
        <footer class="source-reasons"><span>{{ copy.reasons }}</span><code v-if="!item.reasons.length">{{ copy.noReasons }}</code><code v-for="reason in item.reasons" :key="reason">{{ reason }}</code></footer>
      </article>
    </div>
  </section>
</template>

<style scoped>
.quality-panel { min-width: 0; margin-bottom: 34px; }
.quality-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 12px; }
.quality-heading > div,.quality-card header > div { min-width: 0; }
.quality-heading h2 { margin-top: 3px; font-size: 18px; }
.quality-heading p { max-width: 760px; margin: 5px 0 0; color: var(--text-3); font-size: 12px; overflow-wrap: anywhere; }
.quality-eyebrow { color: var(--accent); font-size: 10px; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
.quality-state { display: grid; justify-items: start; gap: 7px; padding: 20px; overflow-wrap: anywhere; }
.quality-grid { display: grid; gap: 12px; }
.quality-card { min-width: 0; padding: 16px; overflow: hidden; }
.quality-card > header,.capability-card > header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
.quality-card > header > div { display: grid; gap: 3px; }
.quality-card > header strong { overflow-wrap: anywhere; }
.quality-card > header small { color: var(--text-3); font-size: 10px; overflow-wrap: anywhere; }
.quality-boundary { display: inline-flex; margin-top: 9px; padding: 3px 8px; border: 1px solid #f0d5a9; border-radius: 999px; color: var(--warn); background: #fff5e5; font-size: 10px; }
dl { margin: 0; }
.source-facts { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)); gap: 7px; margin-top: 12px; }
dl > div { min-width: 0; padding: 8px 9px; border-radius: 9px; background: var(--bg-panel-2); }
dt { color: var(--text-3); font-size: 9px; text-transform: uppercase; }
dd { margin: 3px 0 0; font: 10px var(--font-mono); overflow-wrap: anywhere; }
.quality-subsection { min-width: 0; margin-top: 14px; }
.quality-subsection h3 { margin: 0 0 7px; color: var(--text-3); font-size: 10px; text-transform: uppercase; }
.dimension-grid,.capability-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 7px; }
.dimension-grid > div { display: flex; justify-content: space-between; gap: 8px; min-width: 0; padding: 8px 9px; border: 1px solid var(--border); border-radius: 8px; font: 10px var(--font-mono); }
.dimension-grid strong,.dimension-grid span { min-width: 0; overflow-wrap: anywhere; }
.capability-card { min-width: 0; padding: 10px; border: 1px solid var(--border); border-radius: 10px; }
.capability-card header strong { font-size: 11px; overflow-wrap: anywhere; }
.capability-card dl { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 6px; margin-top: 8px; }
.capability-card footer,.source-reasons,.audit-counters > div { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; margin-top: 8px; color: var(--text-3); font-size: 10px; }
code { max-width: 100%; padding: 2px 6px; border-radius: 6px; color: var(--text-2); background: #f0f2f5; overflow-wrap: anywhere; white-space: normal; }
@media(max-width:900px){ .source-facts { grid-template-columns: repeat(2,minmax(0,1fr)); } }
@media(max-width:640px){ .quality-heading,.quality-card > header { flex-direction: column; align-items: stretch; } .dimension-grid,.capability-grid { grid-template-columns: 1fr; } }
@media(max-width:460px){ .source-facts,.capability-card dl { grid-template-columns: 1fr; } .quality-card { padding: 14px; } }
</style>
