<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    label: string
    value: string | number
    hint?: string
    tone?: 'default' | 'up' | 'down' | 'warn' | 'accent'
  }>(),
  { hint: undefined, tone: 'default' },
)

const toneClass = computed(() => (props.tone === 'default' ? '' : `stat-value--${props.tone}`))
</script>

<template>
  <div class="card card-pad stat-card">
    <div class="stat-label">{{ label }}</div>
    <div class="stat-value" :class="toneClass">{{ value }}</div>
    <div v-if="hint || $slots.default" class="stat-hint">
      <slot>{{ hint }}</slot>
    </div>
  </div>
</template>

<style scoped>
.stat-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.stat-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.stat-value {
  font-size: 24px;
  font-weight: 600;
  line-height: 1.2;
  color: var(--text-1);
  overflow-wrap: anywhere;
}

.stat-value--up {
  color: var(--up);
}

.stat-value--down {
  color: var(--down);
}

.stat-value--warn {
  color: var(--warn);
}

.stat-value--accent {
  color: var(--accent);
}

.stat-hint {
  font-size: 12px;
  color: var(--text-2);
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}
</style>
