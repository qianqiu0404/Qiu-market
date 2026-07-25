<script setup lang="ts">
import { computed } from 'vue'

export type IconName =
  | 'dashboard'
  | 'markets'
  | 'klines'
  | 'analytics'
  | 'system'
  | 'assets'
  | 'exchanges'
  | 'symbols'
  | 'menu'
  | 'close'
  | 'search'
  | 'refresh'
  | 'alert'
  | 'empty'
  | 'chevron-left'
  | 'chevron-right'
  | 'clock'
  | 'globe'

const PATHS: Record<IconName, string> = {
  dashboard: 'M4 4h7v7H4z M13 4h7v4h-7z M13 10h7v10h-7z M4 13h7v7H4z',
  markets: 'M3 17l6-6 4 4 8-8 M15 7h6v6',
  klines: 'M7 4v3 M7 17v3 M5 7h4v10H5z M17 2v4 M17 18v4 M15 6h4v12h-4z',
  analytics: 'M2 20h20 M5 20v-5 M10 20V8 M15 20v-8 M20 20v-3',
  system:
    'M6 15a3 3 0 1 0 0-.01z M18 9a3 3 0 1 0 0-.01z M18 21a3 3 0 1 0 0-.01z M8.6 13.7l6.8-3.4 M8.6 16.3l6.8 3.4',
  assets:
    'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z M12 7v10 M14.8 9.2c-.4-1.3-1.5-2.2-2.8-2.2-1.7 0-3 .9-3 2.2 0 3.1 6 1.6 6 4.6 0 1.3-1.3 2.2-3 2.2-1.4 0-2.5-1-2.8-2.2',
  exchanges: 'M3 21h18 M5 21V8l7-5 7 5v13 M9 21v-4h6v4 M9 12h.01 M12 12h.01 M15 12h.01',
  symbols: 'M8 6h13 M8 12h13 M8 18h13 M3.5 6h.01 M3.5 12h.01 M3.5 18h.01',
  menu: 'M4 6h16 M4 12h16 M4 18h16',
  close: 'M6 6l12 12 M18 6L6 18',
  search: 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16z M21 21l-4.3-4.3',
  refresh: 'M20 11a8 8 0 0 0-14.9-3 M4 5v4h4 M4 13a8 8 0 0 0 14.9 3 M20 19v-4h-4',
  alert:
    'M12 9v4 M12 17h.01 M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z',
  empty:
    'M22 12h-6l-2 3h-4l-2-3H2 M5.5 5.1 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.5-6.9A2 2 0 0 0 16.7 4H7.3a2 2 0 0 0-1.8 1.1z',
  'chevron-left': 'M15 18l-6-6 6-6',
  'chevron-right': 'M9 18l6-6-6-6',
  clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z M12 7v5l3 2',
  globe:
    'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18z M3 12h18 M12 3c2.5 2.6 3.9 5.7 3.9 9S14.5 18.4 12 21c-2.5-2.6-3.9-5.7-3.9-9S9.5 5.6 12 3z',
}

const props = withDefaults(defineProps<{ name: IconName; size?: number }>(), { size: 18 })

const d = computed(() => PATHS[props.name])
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    class="app-icon"
  >
    <path :d="d" />
  </svg>
</template>

<style scoped>
.app-icon {
  display: inline-block;
  vertical-align: middle;
  flex: none;
}
</style>
