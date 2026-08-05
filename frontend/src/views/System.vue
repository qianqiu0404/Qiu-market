<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '../components/PageHeader.vue'
import StatusBadge from '../components/StatusBadge.vue'
import SkeletonRows from '../components/SkeletonRows.vue'
import ErrorState from '../components/ErrorState.vue'
import TradingAdminCard from '../features/system/TradingAdminCard.vue'
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
import { useI18n } from '../i18n'
import {
  tradingAPI,
  type TradingRecoveryPhase,
  type TradingRecoveryStatus,
} from '../api/trading'
import {
  deriveRecoveryAdmission,
  recoveryStatusRegression,
} from '../trading/recovery-admission'

type SystemTab = 'status' | 'audit' | 'assets' | 'exchanges' | 'symbols'
const RECOVERY_EVIDENCE_MAX_AGE_MS = 30_000

const SYSTEM_TABS: Array<{ value: SystemTab; en: string; zh: string }> = [
  { value: 'status', en: 'Status', zh: '状态' },
  { value: 'audit', en: 'Catalog Audit', zh: '目录审计' },
  { value: 'assets', en: 'Assets', zh: '资产' },
  { value: 'exchanges', en: 'Exchanges', zh: '交易所' },
  { value: 'symbols', en: 'Symbols', zh: '交易对' },
]

const catalogComponents = {
  audit: defineAsyncComponent(() => import('./CatalogAudit.vue')),
  assets: defineAsyncComponent(() => import('./Assets.vue')),
  exchanges: defineAsyncComponent(() => import('./Exchanges.vue')),
  symbols: defineAsyncComponent(() => import('./Symbols.vue')),
}

const route = useRoute()
const router = useRouter()
const { locale } = useI18n()

function t(en: string, zh: string): string {
  return locale.value === 'zh-CN' ? zh : en
}

function tabLabel(tab: (typeof SYSTEM_TABS)[number]): string {
  return locale.value === 'zh-CN' ? tab.zh : tab.en
}

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
  return activeTab.value === 'status'
    ? t('System', '系统状态')
    : tab ? tabLabel(tab) : t('System', '系统状态')
})

const pageSubtitle = computed(() => {
  switch (activeTab.value) {
  case 'audit':
    return t('Discovered markets and identity-resolution gates', '查看已发现市场与身份解析门禁')
  case 'assets':
    return t('Supported asset catalog', '查看支持的资产目录')
  case 'exchanges':
    return t('Connected exchange catalog', '查看已连接的交易所目录')
  case 'symbols':
    return t('Tracked trading-pair catalog', '查看正在跟踪的交易对目录')
  default:
    return t(
      'Health and recovery evidence, plus an admin-only virtual-funding control. This page does not start, stop, or promote services.',
      '查看健康与恢复证据，并提供仅管理员可用的虚拟入金入口；本页不会启动、停止服务或执行切流。',
    )
  }
})

/* 15s poll for the read-only observability view. */
const system = usePolling(getSystemStatus, { interval: 15_000 })
let acceptedRecoveryStatus: TradingRecoveryStatus | null = null
async function fetchRecoveryStatus(): Promise<TradingRecoveryStatus> {
  const candidate = await tradingAPI.recoveryStatus()
  if (acceptedRecoveryStatus) {
    const regression = recoveryStatusRegression(acceptedRecoveryStatus, candidate)
    if (regression) throw new Error(regression)
  }
  acceptedRecoveryStatus = candidate
  return candidate
}
const recovery = usePolling(fetchRecoveryStatus, { interval: 15_000 })
const recoveryNowMs = ref(Date.now())
let recoveryClock: number | undefined
onMounted(() => {
  recoveryClock = window.setInterval(() => {
    recoveryNowMs.value = Date.now()
  }, 1_000)
})
onUnmounted(() => {
  if (recoveryClock !== undefined) window.clearInterval(recoveryClock)
})
const recoveryAdmission = computed(() => recovery.data.value
  ? deriveRecoveryAdmission(
      recovery.data.value,
      recovery.error.value ?? '',
      {
        lastSuccessAt: recovery.lastUpdated.value?.getTime() ?? 0,
        now: recoveryNowMs.value,
        maximumAgeMs: RECOVERY_EVIDENCE_MAX_AGE_MS,
      },
    )
  : null)

const componentLabels = computed<Record<string, string>>(() => ({
  matching: t('Matching', '撮合'),
  liquidity: t('Liquidity', '流动性'),
  transport: t('Transport', '传输'),
  market_data: t('Market data', '行情数据'),
  outbox: t('Outbox', '事件发件箱'),
  database: t('Database', '数据库'),
  disk: t('Disk', '磁盘'),
  retention: t('Retention', '保留任务'),
}))

const componentRows = computed(() => {
  const components = system.data.value?.components
  if (!components) return []
  return Object.entries(components).map(([key, status]) => ({
    key,
    label: componentLabels.value[key] ?? key,
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
  const labels: Record<string, [string, string]> = {
    websocket_primary_rest_reconcile: ['WebSocket primary + REST reconcile', 'WebSocket 主链路 + REST 对账'],
    websocket_primary: ['WebSocket primary', 'WebSocket 主链路'],
    rest_reconcile_only: ['REST reconcile only', '仅 REST 对账'],
    http_polling: ['HTTP polling', 'HTTP 轮询'],
    native_rpc_routes: ['Native RPC routes', '原生 RPC 路线'],
    http_catalog: ['HTTP catalog', 'HTTP 目录'],
    unobserved: ['Not observed', '尚未观测'],
    provider_specific: ['Provider specific', '数据源专用'],
  }
  const label = labels[value]
  return label ? t(label[0], label[1]) : value ?? '—'
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
    return `${t('Unavailable', '不可用')} · ${contractText(metric?.reason || t('No reason reported', '未报告原因'))}`
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
    ? `${t('Unavailable', '不可用')} · ${contractText(status.reason)}`
    : formatTime(status.last_success_at)
}

function evidenceAge(status: StatusEvidence): string {
  return status.age_seconds === null
    ? t('Age unavailable', '数据年龄不可用')
    : t(`${formatDelay(status.age_seconds)} old`, `距今 ${formatDelay(status.age_seconds)}`)
}

function stateLabel(state: SystemState | undefined): string {
  const normalized = state ?? 'unknown'
  if (locale.value === 'en') return SYSTEM_STATE_LABELS[normalized]
  const labels: Record<SystemState, string> = {
    live: '实时',
    cached: '缓存',
    demo_snapshot: '演示快照',
    degraded: '降级',
    offline: '离线',
    unknown: '未知',
  }
  return labels[normalized]
}

function providerStatusLabel(status: string): string {
  if (locale.value === 'en') return status
  const labels: Record<string, string> = {
    Healthy: '健康',
    Observing: '观察中',
    Unconfigured: '未配置',
    Paused: '已暂停',
    'Local Preview': '本地预览',
    Stale: '陈旧',
    Unavailable: '不可用',
  }
  return labels[status] ?? status
}

function sourceModeLabel(value: string | undefined): string {
  switch (value) {
  case 'native':
    return t('Native system-status contract', '原生系统状态契约')
  case 'legacy':
    return t('Legacy backend compatibility', '旧版后端兼容模式')
  case 'demo_snapshot':
    return t('Explicit demo snapshot', '明确标记的演示快照')
  default:
    return t('Unknown source mode', '未知来源模式')
  }
}

const CONTRACT_ZH: Record<string, string> = {
  'all required read-only probes have explicit current success evidence': '所有必需的只读探针都有明确的当前成功证据',
  'only market data is using a retained last success within five minutes': '只有行情数据使用了五分钟内保留的最近成功值',
  'one or more required probes are stale, failed, or missing explicit evidence': '至少一个必需探针已陈旧、失败或缺少明确证据',
  'system overview and trading reads are both unavailable': '系统概览和交易只读请求均不可用',
  'trading status and order book reads both succeeded': '交易状态和订单簿读取均成功',
  'only one trading read succeeded': '两项交易读取中只有一项成功',
  'trading status and order book are unreachable': '交易状态和订单簿均不可达',
  'matching engine explicitly reports ready': '撮合引擎明确报告 ready',
  'two-sided BTC-USDT liquidity is visible': 'BTC-USDT 买卖双边流动性可见',
  'two-sided BTC-USDT liquidity is not visible': 'BTC-USDT 买卖双边流动性不完整',
  'outbox publisher explicitly reports ready': 'Outbox 发布器明确报告 ready',
  'PostgreSQL read probe succeeded': 'PostgreSQL 读取探针成功',
  'PostgreSQL read probe failed': 'PostgreSQL 读取探针失败',
  'free disk is above the warning threshold': '可用磁盘空间高于告警阈值',
  'free disk is below 25 GB': '可用磁盘空间低于 25 GB',
  'free disk is below 15 GB': '可用磁盘空间低于 15 GB',
  'retention succeeded within the expected daily window': '保留任务在预期的每日窗口内成功',
  'retention success is older than 36 hours': '最近一次保留任务成功已超过 36 小时',
  'the latest heartbeat exists': '存在最新心跳',
  'the heartbeat is absent or the dependency probe failed': '心跳不存在或依赖探针失败',
  'DEX route summaries are current': 'DEX 路线摘要当前有效',
  'DEX route summaries are cached': 'DEX 路线摘要来自缓存',
  'DEX route summaries are stale': 'DEX 路线摘要已陈旧',
  'CEX Spot reference data is current': 'CEX 现货参考数据当前有效',
  'CEX Spot reference data is served from the retained last success': 'CEX 现货参考数据来自保留的最近成功值',
  'CEX Spot reference data is stale': 'CEX 现货参考数据已陈旧',
  'Venue-specific indicative route quotes at the reported notional.': '按所示名义金额计算的特定场所指示性路线报价。',
  'Never substituted for the CEX Spot reference display price.': '绝不替代 CEX 现货参考展示价。',
  'Read-only composite reference used for display and the virtual demo-maker.': '用于展示和虚拟做市账户的只读综合参考价。',
  'Not an executable route price and never filled from DEX or mock data.': '不是可执行路线价格，也绝不使用 DEX 或模拟数据补值。',
  'Uniswap and PancakeSwap venue route summaries': 'Uniswap 与 PancakeSwap 场所路线摘要',
  'asset_price_index built from fresh CEX Spot contributors': '由新鲜 CEX 现货贡献者构建的 asset_price_index',
  'loopback trading REST over gRPC': '本机回环 trading REST → gRPC',
  'BTC-USDT public order book': 'BTC-USDT 公共订单簿',
  'trading GetStatus': 'trading GetStatus',
  'trading GetStatus outbox fields': 'trading GetStatus 的 Outbox 字段',
  'filesystem statfs': '文件系统 statfs',
  'Redis heartbeat existence': 'Redis 心跳存在性',
}

function contractText(value: string | undefined): string {
  if (!value || locale.value === 'en') return value || '—'
  if (CONTRACT_ZH[value]) return CONTRACT_ZH[value]
  if (value.startsWith('matching engine reports ')) {
    return `撮合引擎报告 ${value.slice('matching engine reports '.length)}`
  }
  if (value.startsWith('outbox publisher reports ')) {
    return `Outbox 发布器报告 ${value.slice('outbox publisher reports '.length)}`
  }
  return value
}

function priceSourceLabel(key: string, fallback: string): string {
  if (key === 'route_price') return t('Route price', '路线价格')
  if (key === 'reference_display_price') return t('Reference display price', '参考展示价')
  return contractText(fallback)
}

function recoveryPhaseLabel(phase: TradingRecoveryPhase): string {
  const labels: Record<TradingRecoveryPhase, [string, string]> = {
    not_enabled: ['Not enabled', '未启用'],
    uninitialized: ['Uninitialized', '未初始化'],
    bootstrap: ['Bootstrap', '启动准备'],
    dependencies_ready: ['Dependencies ready', '依赖已就绪'],
    trading_replay: ['Trading replay', '交易事件重放'],
    reconciling: ['Reconciling', '正在对账'],
    read_only: ['Read only', '只读'],
    transport_warmup: ['Transport warmup', '传输预热'],
    writable: ['Writable', '可写'],
    offline: ['Offline', '离线'],
    manual_review: ['Manual review', '人工检查'],
  }
  const label = labels[phase]
  return t(label[0], label[1])
}

function shortRecoveryID(value: string): string {
  if (!value) return '—'
  return value.length > 18 ? `${value.slice(0, 9)}…${value.slice(-6)}` : value
}

function recoveryVariant(): BadgeVariant {
  if (recoveryAdmission.value?.mode === 'unavailable') return 'error'
  if (!recovery.data.value?.supported) return 'accent'
  return recoveryAdmission.value?.writesAllowed ? 'live' : 'error'
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
        <div class="segmented" role="group" :aria-label="t('System section', '系统栏目')">
          <button
            v-for="tab in SYSTEM_TABS"
            :key="tab.value"
            type="button"
            :class="{ active: activeTab === tab.value }"
            @click="activeTab = tab.value"
          >
            {{ tabLabel(tab) }}
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
            <p>{{ contractText(system.data.value.overall.reason) }}</p>
          </div>
          <StatusBadge
            :variant="evidenceVariant(system.data.value.overall.state)"
            :label="stateLabel(system.data.value.overall.state)"
          />
          <div class="formula-note">
            <strong>{{ t('Status formula', '状态公式') }}</strong>
            <span>{{ t('LIVE requires explicit current success from all eight required probes.', 'LIVE 要求八个必需探针都提供明确的当前成功证据。') }}</span>
            <span>{{ t('CACHED is allowed only when market data alone is 30s–5m old.', '只有行情数据单独处于 30 秒至 5 分钟的保留窗口时，才允许显示 CACHED。') }}</span>
            <span>{{ t('DEMO SNAPSHOT requires an explicit source flag; missing fields become DEGRADED, never LIVE.', 'DEMO SNAPSHOT 必须有明确来源标记；缺失字段只能降级为 DEGRADED，不能变成 LIVE。') }}</span>
            <span>{{ t('OFFLINE means both trading transport and the database-backed market view are unavailable.', 'OFFLINE 表示交易传输和数据库支持的行情视图都不可用。') }}</span>
          </div>
        </div>

        <div class="section-heading provider-heading">
          <div>
            <h2>{{ t('Recovery admission', '恢复准入') }}</h2>
            <p>{{ t('Independent write-admission evidence; it is not a ninth input to the existing eight-probe display formula.', '独立的写入准入证据；它不会成为现有八探针总状态公式的第九项输入。') }}</p>
          </div>
        </div>
        <div
          class="card recovery-evidence"
          :data-recovery-mode="recoveryAdmission?.mode || 'loading'"
          data-testid="system-recovery-admission"
        >
          <template v-if="recovery.data.value">
            <div class="component-title">
              <h3>{{ t('Virtual trading write gate', '虚拟交易写门禁') }}</h3>
              <StatusBadge
                :variant="recoveryVariant()"
                :label="recovery.data.value.supported
                  ? recoveryPhaseLabel(recovery.data.value.phase)
                  : t('NOT ENABLED', '未启用')"
              />
            </div>
            <div class="recovery-evidence-grid">
              <span>{{ t('Epoch', '恢复批次') }} <b class="mono">{{ shortRecoveryID(recovery.data.value.epoch_id) }}</b></span>
              <span>{{ t('Server flag', '服务端标志') }} <b data-testid="system-recovery-server-flag">{{ recovery.data.value.supported
                ? recovery.data.value.writes_enabled
                  ? t('Enabled', '已开放')
                  : t('Blocked', '已禁止')
                : t('Not reported', '未报告') }}</b></span>
              <span>{{ t('Effective admission', '当前有效准入') }} <b data-testid="system-recovery-effective-admission">{{ recoveryAdmission?.mode === 'unavailable'
                ? t('Unavailable', '不可用')
                : !recovery.data.value.supported
                  ? t('Legacy gate', '兼容旧门禁')
                  : recoveryAdmission?.mode === 'writable'
                    ? t('Enabled', '已开放')
                    : t('Blocked', '已禁止') }}</b></span>
            </div>
            <div v-if="recovery.data.value.supported" class="recovery-evidence-grid">
              <span>{{ t('Proofs passed', '已通过证明') }} <b>{{ recoveryAdmission?.completedProofs ?? 0 }} / {{ recoveryAdmission?.totalProofs ?? 6 }}</b></span>
              <span>{{ t('Runtime sequence', '运行序列') }} <b>{{ recovery.data.value.proof.runtime_sequence }}</b></span>
              <span>{{ t('State hash', '状态哈希') }} <b class="mono">{{ shortRecoveryID(recovery.data.value.proof.state_hash) }}</b></span>
              <span>{{ t('Continuity', '存储连续性') }} <b>{{ recovery.data.value.continuity_uncertain ? t('Uncertain', '不确定') : t('Verified', '已确认') }}</b></span>
              <span>{{ t('Last error', '最近错误') }} <b>{{ recovery.data.value.last_error || '—' }}</b></span>
              <span>{{ t('Continuity error', '连续性错误') }} <b>{{ recovery.data.value.continuity_error || '—' }}</b></span>
              <span>{{ t('Production origin', '生产入口') }} <b class="mono">{{ recovery.data.value.provenance?.production_origin || '—' }}</b></span>
              <span>{{ t('Deployment', '部署标识') }} <b class="mono">{{ recovery.data.value.provenance ? shortRecoveryID(recovery.data.value.provenance.deployment_id) : '—' }}</b></span>
              <span>{{ t('Immutable deployment', '不可变部署') }} <b class="mono">{{ recovery.data.value.provenance?.deployment_url || '—' }}</b></span>
              <span>{{ t('Release commit', '发布提交') }} <b class="mono">{{ recovery.data.value.provenance ? shortRecoveryID(recovery.data.value.provenance.release_commit) : '—' }}</b></span>
              <span>{{ t('Source digest', '源码摘要') }} <b class="mono">{{ recovery.data.value.provenance ? shortRecoveryID(recovery.data.value.provenance.source_digest) : '—' }}</b></span>
            </div>
            <p v-if="recoveryAdmission?.mode === 'unavailable'">
              {{ t('The latest public recovery read failed or became stale. Last-good fields remain visible for diagnosis, but write admission is unavailable.', '最近一次公开恢复状态读取失败或已经过龄。为便于诊断仍展示 last-good 字段，但写入准入当前不可用。') }}
            </p>
            <p v-else-if="!recovery.data.value.supported">
              {{ t('The endpoint returned 404 and the trusted capability explicitly reports recovery_gate_enabled=false. This is legacy UI compatibility only, not a writable proof; existing server gates remain authoritative.', '该接口返回 404，且可信 capability 明确报告 recovery_gate_enabled=false。这只是旧版界面兼容，不是“可写”证明；现有服务端门禁仍然具有权威性。') }}
            </p>
            <p v-else>
              {{ t('The server-side runner and gateway enforce this decision. System only displays the public proof and cannot promote an epoch.', '服务端 runner 与 gateway 强制执行该决定；System 只展示公开证明，不能推进恢复批次。') }}
            </p>
          </template>
          <template v-else>
            <StatusBadge :variant="recovery.error.value ? 'error' : 'stale'" :label="recovery.error.value ? t('UNAVAILABLE', '不可用') : t('LOADING', '加载中')" />
            <p>{{ recovery.error.value || t('Reading the public recovery status.', '正在读取公开恢复状态。') }}</p>
          </template>
        </div>

        <TradingAdminCard :admission="recoveryAdmission" />

        <div class="section-heading provider-heading">
          <div>
            <h2>{{ t('Runtime truth', '运行时事实') }}</h2>
            <p>{{ t('Each state carries its own last success, age, reason, and source.', '每项状态都携带自己的最近成功时间、数据年龄、原因和来源。') }}</p>
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
            <p>{{ contractText(row.status.reason) }}</p>
            <div class="component-meta mono">
              <span>{{ t('success', '成功') }} {{ evidenceLastSuccess(row.status) }}</span>
              <span>{{ evidenceAge(row.status) }}</span>
            </div>
            <small>{{ contractText(row.status.source) }}</small>
          </article>
        </div>

        <div class="section-heading provider-heading">
          <div>
            <h2>{{ t('Price sources', '价格来源') }}</h2>
            <p>{{ t('Route price and reference display price are deliberately separate facts.', '路线价格与参考展示价是刻意分离的两类事实，绝不互相补值。') }}</p>
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
              <h3>{{ priceSourceLabel(priceSource.key, priceSource.label) }}</h3>
              <StatusBadge
                :variant="evidenceVariant(priceSource.status.state)"
                :label="stateLabel(priceSource.status.state)"
              />
            </div>
            <dl>
              <div>
                <dt>{{ t('Source', '来源') }}</dt>
                <dd>{{ contractText(priceSource.source) }}</dd>
              </div>
              <div>
                <dt>{{ t('Meaning', '含义') }}</dt>
                <dd>{{ contractText(priceSource.meaning) }}</dd>
              </div>
              <div>
                <dt>{{ t('Boundary', '边界') }}</dt>
                <dd>{{ contractText(priceSource.boundary) }}</dd>
              </div>
              <div>
                <dt>{{ t('Last success', '最近成功') }}</dt>
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
            <h2>{{ t('Processes & dependencies', '进程与依赖') }}</h2>
            <p>{{ t('A heartbeat proves the process is running, not that its upstream source is usable.', '心跳只能证明进程正在运行，不能证明它的上游数据源可用。') }}</p>
          </div>
        </div>
        <div class="card detail-card">
        <div
          v-for="row in system.data.value.processes"
          :key="row.key"
          class="detail-row"
        >
          <span class="detail-name">{{ contractText(row.label) }}</span>
          <StatusBadge
            :variant="evidenceVariant(row.status.state)"
            :label="stateLabel(row.status.state)"
          />
          <span class="detail-meta mono">
            {{ t('reported', '报告值') }} {{ row.raw_status }}
          </span>
          <span class="detail-meta detail-reason">
            {{ contractText(row.status.reason) }} · {{ contractText(row.status.source) }}
          </span>
        </div>
        </div>

        <div class="section-heading provider-heading">
          <div>
            <h2>{{ t('Storage & retention', '存储与保留策略') }}</h2>
            <p>{{ t('Missing metrics show Unavailable with a reason; they never become zero or healthy by default.', '缺失指标会显示“不可用”及原因，绝不会默认变成零或健康。') }}</p>
          </div>
        </div>
        <div class="card detail-card">
        <div class="detail-row">
          <span class="detail-name">{{ t('Mac mini free disk', 'Mac mini 可用磁盘') }}</span>
          <StatusBadge
            :variant="evidenceVariant(system.data.value.components.disk.state)"
            :label="stateLabel(system.data.value.components.disk.state)"
          />
          <span class="detail-meta mono">
            {{ bytesMetric(system.data.value.storage.disk_free_bytes) }}
          </span>
          <span class="detail-meta detail-reason">
            {{ system.data.value.components.disk.reason }} ·
            {{ t('warning', '告警') }} &lt;{{ formatBytes(system.data.value.storage.warning_below_bytes) }} ·
            {{ t('critical', '严重') }} &lt;{{ formatBytes(system.data.value.storage.critical_below_bytes) }}
          </span>
        </div>
        <div class="detail-row">
          <span class="detail-name">{{ t('PostgreSQL / K-lines', 'PostgreSQL / K 线') }}</span>
          <StatusBadge
            :variant="evidenceVariant(system.data.value.components.database.state)"
            :label="stateLabel(system.data.value.components.database.state)"
          />
          <span class="detail-meta mono">
            {{ t('DB', '数据库') }} {{ bytesMetric(system.data.value.storage.database_bytes) }} ·
            {{ t('K-lines', 'K 线') }} {{ bytesMetric(system.data.value.storage.kline_table_bytes) }}
          </span>
          <span class="detail-meta detail-reason mono">
            {{ t('heap', '数据') }} {{ bytesMetric(system.data.value.storage.kline_heap_bytes) }} ·
            {{ t('indexes', '索引') }} {{ bytesMetric(system.data.value.storage.kline_index_bytes) }} ·
            {{ t('rows', '行数') }} {{ numberMetric(system.data.value.storage.kline_estimated_rows) }}
          </span>
        </div>
        <div
          v-for="item in system.data.value.storage.kline_intervals"
          :key="item.interval"
          class="detail-row"
        >
          <span class="detail-name mono">{{ item.interval }} {{ t('candles', 'K 线') }}</span>
          <StatusBadge
            :variant="item.oldest_at.available || item.newest_at.available ? 'accent' : 'stale'"
            :label="item.oldest_at.available || item.newest_at.available ? t('OBSERVED', '已观测') : t('UNAVAILABLE', '不可用')"
          />
          <span class="detail-meta mono">{{ t('oldest', '最早') }} {{ timeMetric(item.oldest_at) }}</span>
          <span class="detail-meta detail-reason mono">
            {{ t('newest', '最新') }} {{ timeMetric(item.newest_at) }} ·
            {{ t('policy', '策略') }} {{ item.interval === '1d' ? t('indefinite', '永久') : t('bounded', '有界保留') }}
          </span>
        </div>
        <div class="detail-row">
          <span class="detail-name">{{ t('Retention job', '保留任务') }}</span>
          <StatusBadge
            :variant="evidenceVariant(system.data.value.components.retention.state)"
            :label="stateLabel(system.data.value.components.retention.state)"
          />
          <span class="detail-meta mono">
            {{ t('success', '成功') }} {{ timeMetric(system.data.value.storage.retention_last_success_at) }} ·
            {{ t('started', '开始') }} {{ timeMetric(system.data.value.storage.retention_last_started_at) }}
          </span>
          <span class="detail-meta detail-reason">
            {{ contractText(system.data.value.storage.retention_last_error ||
              system.data.value.components.retention.reason) }}
          </span>
        </div>
        <div class="detail-row">
          <span class="detail-name">{{ t('Deleted rows · last run', '最近一次删除行数') }}</span>
          <StatusBadge variant="accent" :label="t('EVIDENCE', '证据')" />
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
            <h2>{{ t('Data sources', '数据源') }}</h2>
            <p>{{ t('Operational health follows the active capability; rollout readiness remains evidence-only and requires a manual CLI action.', '运行健康度以当前启用能力为准；切流就绪状态只是证据，仍需人工执行 CLI 操作。') }}</p>
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
                {{ provider.local_preview_enabled ? t('local preview', '本地预览') : (provider.rollout_mode || t('unconfigured', '未配置')) }} ·
                Top {{ provider.rank_limit || '—' }} ·
                {{ feedModeLabel(provider.feed_mode) }} ·
                {{ t('success', '成功率') }} {{ provider.success_rate_pct ? `${provider.success_rate_pct}%` : '—' }}
              </span>
              <span class="provider-meta evidence-line">
                {{ provider.received_count }} {{ t('received', '已接收') }} · {{ provider.matched_asset_count }} {{ t('matched', '已匹配') }} ·
                {{ provider.local_preview_enabled ? provider.preview_covered_count : provider.price_available_count }} {{ t('priced', '有价格') }} ·
                {{ provider.change_available_count }} {{ t('with 24h', '含 24h 涨跌') }} ·
                {{ t('operational', '运行状态') }} {{ provider.operational_status || t('Unknown', '未知') }}
              </span>
              <span v-if="provider.selection_version" class="provider-meta evidence-line">
                {{ t('selection', '入选集') }} v{{ provider.selection_version }} ·
                {{ provider.selection_count }}/{{ provider.selection_target_count }} {{ t('selected', '已入选') }} ·
                {{ provider.selection_candidate_count }} {{ t('valid Top 200 candidates', '个有效 Top 200 候选') }} ·
                {{ t('generated', '生成于') }} {{ formatTime(provider.selection_generated_at || null) }}
              </span>
              <span v-if="provider.kline_status" class="provider-meta evidence-line">
                {{ t('K-lines', 'K 线') }} {{ provider.kline_status }} ·
                {{ provider.kline_market_count }}/{{ provider.selection_count || provider.selection_target_count }} {{ t('markets', '个市场') }} ·
                {{ provider.kline_candle_count }} {{ t('source candles this cycle', '根本周期来源 K 线') }} ·
                {{ t('success', '成功') }} {{ formatTime(provider.kline_last_success_at || null) }}
              </span>
            </div>
            <StatusBadge
              :variant="providerStatusVariant(provider.status)"
              :label="providerStatusLabel(provider.status)"
            />
            <span class="detail-meta mono">{{ t('last success', '最近成功') }} {{ formatTime(provider.last_success_at || null) }}</span>
            <span class="detail-meta mono">
              {{ provider.rollout_ready
                ? t('ready for manual promotion', '已具备人工切流条件')
                : provider.readiness_not_before
                  ? t(`not before ${formatTime(provider.readiness_not_before)}`, `最早于 ${formatTime(provider.readiness_not_before)}`)
                  : provider.next_retry_at
                ? t(`retry ${formatTime(provider.next_retry_at)}`, `重试于 ${formatTime(provider.next_retry_at)}`)
                : provider.last_error_class || (provider.min_soak_until ? t(`soak until ${formatTime(provider.min_soak_until)}`, `观察至 ${formatTime(provider.min_soak_until)}`) : t('no active error', '无当前错误')) }}
            </span>
          </div>
          <div v-if="provider.rollout_blockers.length" class="rollout-blockers">
            <strong>{{ t('Promotion blockers', '切流阻塞项') }}</strong>
            <span v-for="blocker in provider.rollout_blockers.slice(0, 3)" :key="blocker">{{ blocker }}</span>
            <span v-if="provider.rollout_blockers.length > 3">
              +{{ provider.rollout_blockers.length - 3 }} {{ t('more in rollout-status JSON', '项见 rollout-status JSON') }}
            </span>
          </div>
          <details v-if="provider.sources.length" class="source-details">
            <summary>
              {{ provider.sources.length }} {{ t(`capability observation${provider.sources.length === 1 ? '' : 's'}`, '项能力观测') }}
            </summary>
            <div class="source-matrix">
              <div v-for="source in provider.sources" :key="source.source_key" class="source-row">
                <span>
                  <span class="mono">{{ source.source_key }}</span>
                  <small>{{ source.capability || 'other' }}</small>
                </span>
                <StatusBadge
                  :variant="providerStatusVariant(source.status)"
                  :label="providerStatusLabel(source.status)"
                />
                <span class="detail-meta mono">
                  {{ source.success_count }}/{{ source.attempt_count }}
                  {{ source.success_rate_pct ? `(${source.success_rate_pct}%)` : '' }}
                </span>
                <span class="detail-meta mono">
                  {{ source.matched_asset_count
                    ? t(`${source.matched_asset_count} matched · ${source.written_count || source.received_count} rows`, `${source.matched_asset_count} 已匹配 · ${source.written_count || source.received_count} 行`)
                    : source.next_retry_at
                      ? t(`retry ${formatTime(source.next_retry_at)}`, `重试于 ${formatTime(source.next_retry_at)}`)
                      : t(`success ${formatTime(source.last_success_at || null)}`, `成功于 ${formatTime(source.last_success_at || null)}`) }}
                </span>
              </div>
            </div>
          </details>
        </div>
        <div
          v-if="!system.data.value.provider_statuses.length"
          class="provider-empty"
        >
          {{ t('No provider observations recorded yet.', '尚未记录任何数据源观测。') }}
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
.price-source-card,
.recovery-evidence {
  min-width: 0;
  padding: 16px;
}

.recovery-evidence-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px 16px;
}

.recovery-evidence-grid span {
  display: grid;
  gap: 3px;
  color: var(--text-3);
  font-size: 11px;
}

.recovery-evidence-grid b {
  color: var(--text-2);
  overflow-wrap: anywhere;
}

.recovery-evidence > p {
  margin: 12px 0 0;
  color: var(--text-3);
  font-size: 12px;
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

  .recovery-evidence-grid {
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

  .recovery-evidence-grid {
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
