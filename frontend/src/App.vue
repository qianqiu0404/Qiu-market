<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppIcon from './components/AppIcon.vue'
import type { IconName } from './components/AppIcon.vue'
import { usePolling } from './composables/usePolling'
import { getSystemOverview } from './api/market'
import { isHealthyStatus } from './utils/format'

interface NavItem {
  to: string
  label: string
  icon: IconName
}

const NAV: NavItem[] = [
  { to: '/markets', label: 'Markets', icon: 'markets' },
  { to: '/insights', label: 'Insights', icon: 'analytics' },
  { to: '/system', label: 'System', icon: 'system' },
]

const route = useRoute()
const drawerOpen = ref(false)

watch(
  () => route.fullPath,
  () => {
    drawerOpen.value = false
  },
)

/* Core process health only; provider source health remains explicit in System. */
const { data: overview, error } = usePolling(getSystemOverview, { interval: 30_000 })

const health = computed<{ tone: 'ok' | 'degraded' | 'down'; label: string }>(() => {
  if (error.value) return { tone: 'down', label: 'API offline' }
  const ov = overview.value
  if (!ov) return { tone: 'degraded', label: 'Connecting…' }
  const statuses = [
    ov.crawler_status,
    ov.redis_status,
    ov.database_status,
    ov.worker_status,
    ov.api_status,
  ]
  const unhealthy = statuses.filter((s) => !isHealthyStatus(s)).length
  if (unhealthy === 0) return { tone: 'ok', label: 'Core processes running' }
  if (unhealthy >= statuses.length) return { tone: 'down', label: 'Core processes down' }
  return { tone: 'degraded', label: `${unhealthy} core process${unhealthy > 1 ? 'es' : ''} degraded` }
})
</script>

<template>
  <div class="app-shell">
    <header class="topbar">
      <button type="button" class="topbar-menu" aria-label="Open navigation" @click="drawerOpen = true">
        <AppIcon name="menu" :size="20" />
      </button>
      <span class="topbar-brand">Qiu Market</span>
    </header>

    <div v-if="drawerOpen" class="scrim" @click="drawerOpen = false"></div>

    <aside class="sidebar" :class="{ open: drawerOpen }">
      <div class="brand">
        <span class="brand-tile">Q</span>
        <span class="brand-text">
          <span class="brand-name">Qiu Market</span>
          <span class="brand-sub">Market Data Platform</span>
        </span>
        <button
          type="button"
          class="drawer-close"
          aria-label="Close navigation"
          @click="drawerOpen = false"
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
          :title="item.label"
        >
          <AppIcon :name="item.icon" :size="18" />
          <span class="nav-label">{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div class="sidebar-footer">
        <span class="health-dot" :class="`health-dot--${health.tone}`"></span>
        <span class="nav-label health-label">{{ health.label }}</span>
      </div>
    </aside>

    <main class="content">
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
  }

  .topbar-brand {
    font-weight: 600;
    font-size: 14px;
  }

  .sidebar {
    width: var(--sidebar-w);
    transform: translateX(-100%);
    box-shadow: none;
  }

  .sidebar.open {
    transform: translateX(0);
  }

  .sidebar .brand-text,
  .sidebar .nav-group-label,
  .sidebar .nav-label {
    display: flex;
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
