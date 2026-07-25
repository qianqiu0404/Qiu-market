<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    points: number[]
    width?: number
    height?: number
  }>(),
  { width: 120, height: 36 },
)

const trend = computed<'up' | 'down' | 'flat'>(() => {
  const pts = props.points
  if (pts.length < 2) return 'flat'
  const first = pts[0]!
  const last = pts[pts.length - 1]!
  if (last > first) return 'up'
  if (last < first) return 'down'
  return 'flat'
})

const path = computed(() => {
  const pts = props.points.filter((p) => Number.isFinite(p))
  if (pts.length < 2) return ''
  const min = Math.min(...pts)
  const max = Math.max(...pts)
  const span = max - min || 1
  const w = props.width
  const h = props.height
  const padY = 2
  const stepX = w / (pts.length - 1)
  return pts
    .map((p, i) => {
      const x = i * stepX
      const y = padY + (1 - (p - min) / span) * (h - padY * 2)
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`
    })
    .join(' ')
})
</script>

<template>
  <svg
    :width="width"
    :height="height"
    :viewBox="`0 0 ${width} ${height}`"
    class="sparkline"
    :class="`sparkline--${trend}`"
    aria-hidden="true"
  >
    <path v-if="path" :d="path" fill="none" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round" />
  </svg>
</template>

<style scoped>
.sparkline--up {
  stroke: var(--up);
}

.sparkline--down {
  stroke: var(--down);
}

.sparkline--flat {
  stroke: var(--text-3);
}
</style>
