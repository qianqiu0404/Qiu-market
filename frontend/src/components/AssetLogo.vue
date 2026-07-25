<script setup lang="ts">
import { computed, ref, watch } from 'vue'

const props = withDefaults(
  defineProps<{
    src?: string | null
    name: string
    size?: number
  }>(),
  { src: null, size: 32 },
)

const failed = ref(false)
watch(
  () => props.src,
  () => {
    failed.value = false
  },
)

const showImage = computed(() => Boolean(props.src) && !failed.value)

const initial = computed(() => {
  const trimmed = props.name.trim()
  return trimmed.length > 0 ? trimmed[0]!.toUpperCase() : '?'
})

/** Deterministic tile color derived from the name. */
const tileStyle = computed(() => {
  let hash = 0
  for (let i = 0; i < props.name.length; i += 1) {
    hash = (hash * 31 + props.name.charCodeAt(i)) % 360
  }
  return {
    width: `${props.size}px`,
    height: `${props.size}px`,
    fontSize: `${Math.max(11, Math.round(props.size * 0.42))}px`,
    background: `hsl(${hash} 45% 32%)`,
    color: `hsl(${hash} 80% 85%)`,
  }
})

const onError = (): void => {
  failed.value = true
}
</script>

<template>
  <img
    v-if="showImage"
    :src="src ?? ''"
    :alt="name"
    :width="size"
    :height="size"
    class="asset-logo"
    loading="lazy"
    @error="onError"
  />
  <span v-else class="asset-logo asset-logo--fallback" :style="tileStyle" aria-hidden="true">
    {{ initial }}
  </span>
</template>

<style scoped>
.asset-logo {
  border-radius: 50%;
  object-fit: cover;
  flex: none;
  background: var(--bg-panel-2);
}

.asset-logo--fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
}
</style>
