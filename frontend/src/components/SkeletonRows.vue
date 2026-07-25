<script setup lang="ts">
withDefaults(
  defineProps<{
    rows?: number
    /** rows = stacked lines; cards = grid of card blocks */
    variant?: 'rows' | 'cards'
    height?: number
  }>(),
  { rows: 5, variant: 'rows', height: 14 },
)
</script>

<template>
  <div v-if="variant === 'rows'" class="skeleton-rows" role="status" aria-label="Loading">
    <div
      v-for="i in rows"
      :key="i"
      class="shimmer skeleton-line"
      :style="{ height: `${height}px`, width: `${100 - ((i * 13) % 30)}%` }"
    ></div>
  </div>
  <div v-else class="skeleton-cards" role="status" aria-label="Loading">
    <div v-for="i in rows" :key="i" class="shimmer skeleton-card"></div>
  </div>
</template>

<style scoped>
.skeleton-rows {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 8px 0;
}

.skeleton-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 14px;
}

.skeleton-card {
  height: 88px;
  border-radius: var(--radius-card);
}
</style>
