<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppIcon from './components/AppIcon.vue'
import type { IconName } from './components/AppIcon.vue'
import { usePolling } from './composables/usePolling'
import { getSystemOverview } from './api/market'
import { isHealthyStatus } from './utils/format'
import { useI18n, type Locale } from './i18n'

interface NavItem {
  to: string
  label: { en: string; 'zh-CN': string }
  icon: IconName
}

const NAV: NavItem[] = [
  { to: '/markets', label: { en: 'Markets', 'zh-CN': '行情' }, icon: 'markets' },
  { to: '/trade/BTC-USDT', label: { en: 'Trade', 'zh-CN': '交易' }, icon: 'trade' },
  { to: '/insights', label: { en: 'Insights', 'zh-CN': '洞察' }, icon: 'analytics' },
  { to: '/system', label: { en: 'System', 'zh-CN': '系统' }, icon: 'system' },
]

const route = useRoute()
const drawerOpen = ref(false)
const navigationDrawer = ref<HTMLElement | null>(null)
const navigationButton = ref<HTMLButtonElement | null>(null)
const { locale, setLocale } = useI18n()

function openNavigation(): void {
  drawerOpen.value = true
  void nextTick(() => navigationDrawer.value?.querySelector<HTMLElement>('.drawer-close')?.focus())
}

function closeNavigation(returnFocus = true): void {
  drawerOpen.value = false
  if (returnFocus) void nextTick(() => navigationButton.value?.focus())
}

function handleNavigationKeydown(event: KeyboardEvent): void {
  if (!drawerOpen.value || !navigationDrawer.value) return
  if (event.key === 'Escape') {
    event.preventDefault()
    closeNavigation()
    return
  }
  if (event.key !== 'Tab') return
  const focusable = [...navigationDrawer.value.querySelectorAll<HTMLElement>(
    'button:not([disabled]),a[href],[tabindex]:not([tabindex="-1"])',
  )]
  if (!focusable.length) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

const copy = computed(() => locale.value === 'zh-CN' ? {
  brandSub: '行情数据平台',
  openNavigation: '打开导航',
  closeNavigation: '关闭导航',
  language: '语言',
  apiOffline: 'API 离线',
  connecting: '正在连接…',
  coreRunning: '核心进程运行中',
  coreDown: '核心进程全部停止',
  coreDegraded: (count: number) => `${count} 个核心进程异常`,
} : {
  brandSub: 'Market Data Platform',
  openNavigation: 'Open navigation',
  closeNavigation: 'Close navigation',
  language: 'Language',
  apiOffline: 'API offline',
  connecting: 'Connecting…',
  coreRunning: 'Core processes running',
  coreDown: 'Core processes down',
  coreDegraded: (count: number) => `${count} core process${count > 1 ? 'es' : ''} degraded`,
})

function navLabel(item: NavItem): string {
  return item.label[locale.value]
}

function chooseLocale(value: Locale): void {
  setLocale(value)
}

const routeTitles: Record<string, { en: string; 'zh-CN': string }> = {
  markets: { en: 'Markets', 'zh-CN': '行情' },
  'market-detail': { en: 'Market Chart', 'zh-CN': '市场图表' },
  'trade-btc-usdt': { en: 'Virtual Spot', 'zh-CN': '虚拟现货' },
  insights: { en: 'Insights', 'zh-CN': '市场洞察' },
  system: { en: 'System', 'zh-CN': '系统状态' },
  'not-found': { en: 'Not Found', 'zh-CN': '页面不存在' },
}

watch(
  [() => route.name, locale],
  ([name]) => {
    const title = typeof name === 'string' ? routeTitles[name]?.[locale.value] : ''
    document.title = title ? `${title} · Qiu Market` : 'Qiu Market'
  },
  { immediate: true },
)

watch(
  () => route.fullPath,
  () => {
    if (drawerOpen.value) closeNavigation()
  },
)

/* Core process health only; provider source health remains explicit in System. */
const { data: overview, error } = usePolling(getSystemOverview, { interval: 30_000 })

const health = computed<{ tone: 'ok' | 'degraded' | 'down'; label: string }>(() => {
  if (error.value) return { tone: 'down', label: copy.value.apiOffline }
  const ov = overview.value
  if (!ov) return { tone: 'degraded', label: copy.value.connecting }
  const statuses = [
    ov.crawler_status,
    ov.redis_status,
    ov.database_status,
    ov.worker_status,
    ov.api_status,
  ]
  const unhealthy = statuses.filter((s) => !isHealthyStatus(s)).length
  if (unhealthy === 0) return { tone: 'ok', label: copy.value.coreRunning }
  if (unhealthy >= statuses.length) return { tone: 'down', label: copy.value.coreDown }
  return { tone: 'degraded', label: copy.value.coreDegraded(unhealthy) }
})
</script>

<template>
  <div class="app-shell">
    <header class="topbar">
      <button ref="navigationButton" type="button" class="topbar-menu" :aria-label="copy.openNavigation" @click="openNavigation">
        <AppIcon name="menu" :size="20" />
      </button>
      <span class="topbar-brand">Qiu Market</span>
    </header>

    <div v-if="drawerOpen" class="scrim" @click="closeNavigation()"></div>

    <aside
      ref="navigationDrawer"
      class="sidebar"
      :class="{ open: drawerOpen }"
      :role="drawerOpen ? 'dialog' : undefined"
      :aria-modal="drawerOpen ? 'true' : undefined"
      :aria-label="drawerOpen ? copy.openNavigation : undefined"
      @keydown="handleNavigationKeydown"
    >
      <div class="brand">
        <span class="brand-tile">Q</span>
        <span class="brand-text">
          <span class="brand-name">Qiu Market</span>
          <span class="brand-sub">{{ copy.brandSub }}</span>
        </span>
        <button
          type="button"
          class="drawer-close"
          :aria-label="copy.closeNavigation"
          @click="closeNavigation()"
        >
          <AppIcon name="close" :size="18" />
        </button>
      </div>

      <nav class="nav">
        <RouterLink
          v-for="item in NAV"
          :key="item.to"
          :to="item.to"
          class="nav-item"
          active-class="active"
          :title="navLabel(item)"
        >
          <AppIcon :name="item.icon" :size="18" />
          <span class="nav-label">{{ navLabel(item) }}</span>
        </RouterLink>
      </nav>

      <div class="locale-switch" role="group" :aria-label="copy.language">
        <button
          type="button"
          :class="{ active: locale === 'zh-CN' }"
          :aria-pressed="locale === 'zh-CN'"
          @click="chooseLocale('zh-CN')"
        >
          中文
        </button>
        <button
          type="button"
          :class="{ active: locale === 'en' }"
          :aria-pressed="locale === 'en'"
          @click="chooseLocale('en')"
        >
          EN
        </button>
      </div>

      <div class="sidebar-footer">
        <span class="health-dot" :class="`health-dot--${health.tone}`"></span>
        <span class="nav-label health-label">{{ health.label }}</span>
      </div>
    </aside>

    <main class="content" :inert="drawerOpen || undefined" :aria-hidden="drawerOpen ? 'true' : undefined">
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.app-shell {
  min-height: 100vh;
}

/* ===== Sidebar ===== */
.sidebar {
  position: fixed;
  inset: 0 auto 0 0;
  width: var(--sidebar-w);
  background: rgba(255, 255, 255, 0.84);
  -webkit-backdrop-filter: saturate(180%) blur(22px);
  backdrop-filter: saturate(180%) blur(22px);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  z-index: 40;
  transition: transform 0.2s ease, width 0.2s ease;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 20px 18px;
  border-bottom: 1px solid var(--border);
}

.brand-tile {
  width: 36px;
  height: 36px;
  border-radius: 11px;
  background: var(--accent);
  color: #fff;
  font-weight: 700;
  font-size: 16px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
}

.brand-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.brand-name {
  font-weight: 600;
  font-size: 15px;
  white-space: nowrap;
}

.brand-sub {
  font-size: 12px;
  color: var(--text-3);
  white-space: nowrap;
}

.drawer-close {
  display: none;
  margin-left: auto;
  appearance: none;
  border: 0;
  background: transparent;
  color: var(--text-2);
  cursor: pointer;
  min-width: 44px;
  min-height: 44px;
  padding: 4px;
}

.nav {
  flex: 1;
  overflow-y: auto;
  padding: 18px 12px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  color: var(--text-2);
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
  transition: background 0.15s ease, color 0.15s ease;
}

.nav-item:hover {
  background: var(--bg-panel-2);
  color: var(--text-1);
}

.nav-item.active {
  background: var(--accent-soft);
  color: var(--accent);
}

.sidebar-footer {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 14px 16px;
  border-top: 1px solid var(--border);
  font-size: 12px;
  color: var(--text-2);
  white-space: nowrap;
  overflow: hidden;
}

.locale-switch {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 3px;
  margin: 0 12px 12px;
  padding: 3px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-panel-2);
}

.locale-switch button {
  min-height: 44px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--text-3);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
  font-weight: 600;
}

.locale-switch button.active {
  background: var(--bg-panel);
  color: var(--accent);
  box-shadow: 0 1px 3px rgba(15, 42, 72, 0.08);
}

.health-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}

.health-dot--ok {
  background: var(--up);
  box-shadow: 0 0 0 3px #d6f2e6;
}

.health-dot--degraded {
  background: var(--warn);
  box-shadow: 0 0 0 3px #f9e8c8;
}

.health-dot--down {
  background: var(--down);
  box-shadow: 0 0 0 3px #f8dce2;
}

.health-label {
  overflow: hidden;
  text-overflow: ellipsis;
}

/* ===== Top bar (mobile only) ===== */
.topbar {
  display: none;
}

.scrim {
  display: none;
}

/* ===== Content ===== */
.content {
  margin-left: var(--sidebar-w);
  padding: 34px 38px 64px;
  max-width: 1680px;
  min-width: 0;
}

/* ===== Collapsed (icon-only) sidebar ===== */
@media (max-width: 1023px) {
  .sidebar {
    width: var(--sidebar-w-collapsed);
  }

  .brand {
    justify-content: center;
    padding: 18px 8px;
  }

  .brand-text,
  .nav-label {
    display: none;
  }

  .locale-switch {
    grid-template-columns: 1fr;
    margin: 0 5px 10px;
    padding: 2px;
  }

  .locale-switch button {
    min-height: 44px;
    padding: 0;
    font-size: 9px;
  }

  .nav-item {
    justify-content: center;
    padding: 9px;
  }

  .sidebar-footer {
    justify-content: center;
    padding: 14px 8px;
  }

  .content {
    margin-left: var(--sidebar-w-collapsed);
    padding: 20px 20px 48px;
  }
}

/* ===== Mobile overlay drawer ===== */
@media (max-width: 767px) {
  .topbar {
    display: flex;
    align-items: center;
    gap: 12px;
    position: sticky;
    top: 0;
    z-index: 30;
    height: var(--topbar-h);
    padding: 0 14px;
    background: rgba(255, 255, 255, 0.86);
    -webkit-backdrop-filter: saturate(180%) blur(18px);
    backdrop-filter: saturate(180%) blur(18px);
    border-bottom: 1px solid var(--border);
  }

  .topbar-menu {
    appearance: none;
    border: 1px solid var(--border);
    background: var(--bg-panel);
    color: var(--text-1);
    border-radius: var(--radius-sm);
    padding: 6px 8px;
    cursor: pointer;
    display: inline-flex;
    min-width: 44px;
    min-height: 44px;
    align-items: center;
    justify-content: center;
  }

  .topbar-brand {
    font-weight: 600;
    font-size: 14px;
  }

  .sidebar {
    width: var(--sidebar-w);
    transform: translateX(-100%);
    visibility: hidden;
    pointer-events: none;
    box-shadow: none;
  }

  .sidebar.open {
    transform: translateX(0);
    visibility: visible;
    pointer-events: auto;
  }

  .sidebar .brand-text,
  .sidebar .nav-group-label,
  .sidebar .nav-label {
    display: flex;
  }

  .sidebar .locale-switch {
    display: grid;
    grid-template-columns: 1fr 1fr;
    margin: 0 12px 12px;
    padding: 3px;
  }

  .sidebar .locale-switch button {
    min-height: 44px;
    font-size: 11px;
  }

  .sidebar .nav-group-label {
    display: block;
  }

  .sidebar .brand {
    justify-content: flex-start;
    padding: 18px 16px;
  }

  .sidebar .nav-item {
    justify-content: flex-start;
    padding: 8px 10px;
  }

  .sidebar .sidebar-footer {
    justify-content: flex-start;
    padding: 14px 16px;
  }

  .drawer-close {
    display: inline-flex;
  }

  .scrim {
    display: block;
    position: fixed;
    inset: 0;
    background: rgba(29, 29, 31, 0.24);
    z-index: 35;
  }

  .content {
    margin-left: 0;
    padding: 18px 14px 48px;
  }
}
</style>
