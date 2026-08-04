<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'
import AppIcon from './AppIcon.vue'
import { formatClock, FRESHNESS_LABELS, type Freshness } from '../utils/format'
import { useI18n } from '../i18n'

const { locale } = useI18n()

withDefaults(
  defineProps<{
    title: string
    subtitle?: string
    freshness?: Freshness | null
    refreshedAt?: Date | null
  }>(),
  { subtitle: undefined, freshness: null, refreshedAt: null },
)
</script>

<template>
  <header class="page-header">
    <div class="page-header-text">
      <h1>{{ title }}</h1>
      <p v-if="subtitle" class="page-subtitle">{{ subtitle }}</p>
    </div>
    <div class="page-header-side">
      <slot name="actions" />
      <span v-if="refreshedAt" class="page-clock mono">
        <AppIcon name="clock" :size="14" />
        {{ locale === 'zh-CN' ? '页面刷新于' : 'Page refreshed' }} {{ formatClock(refreshedAt) }}
      </span>
      <StatusBadge
        v-if="freshness"
        :variant="freshness"
        :label="FRESHNESS_LABELS[freshness]"
      />
    </div>
  </header>
</template>

<style scoped>
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  margin-bottom: 24px;
}

.page-header h1 {
  font-size: clamp(24px, 2.2vw, 32px);
  font-weight: 600;
  letter-spacing: -0.035em;
}

.page-subtitle {
  margin: 7px 0 0;
  font-size: 14px;
  color: var(--text-3);
}

.page-header-side {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.page-clock {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-2);
  font-size: 13px;
  min-height: 38px;
  padding: 7px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-panel);
}
</style>
