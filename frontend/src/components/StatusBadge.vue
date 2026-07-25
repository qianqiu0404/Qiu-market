<script setup lang="ts">
import { computed } from 'vue'

export type BadgeVariant = 'live' | 'delayed' | 'stale' | 'offline' | 'error' | 'up' | 'down' | 'accent'

const DEFAULT_LABELS: Record<BadgeVariant, string> = {
  live: 'Live',
  delayed: 'Delayed',
  stale: 'Stale',
  offline: 'Offline',
  error: 'Error',
  up: 'Up',
  down: 'Down',
  accent: 'Info',
}

const props = withDefaults(
  defineProps<{
    variant: BadgeVariant
    label?: string
    /** Show the status dot. Defaults to true. */
    dot?: boolean
  }>(),
  { label: undefined, dot: true },
)

const text = computed(() => props.label ?? DEFAULT_LABELS[props.variant])
const pulse = computed(() => props.variant === 'live')
</script>

<template>
  <span class="badge" :class="`badge--${variant}`">
    <span v-if="dot" class="dot" :class="{ 'dot-pulse': pulse }"></span>
    {{ text }}
  </span>
</template>
