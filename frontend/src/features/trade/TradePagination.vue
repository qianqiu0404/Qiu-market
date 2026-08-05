<script setup lang="ts">
import { useI18n } from '../../i18n'
defineProps<{ page: number; hasPrevious: boolean; hasNext: boolean; busy?: boolean }>()
const emit = defineEmits<{ previous: []; next: [] }>()
const { tr } = useI18n()
</script>
<template><nav class="pagination" aria-label="Cursor pagination"><button class="btn" :disabled="!hasPrevious || busy" @click="emit('previous')">{{ tr('trade.activity.previous') }}</button><span>{{ tr('trade.activity.page', { page }) }}</span><button class="btn" :disabled="!hasNext || busy" @click="emit('next')">{{ tr('trade.activity.next') }}</button></nav></template>
<style scoped>.pagination{display:flex;align-items:center;justify-content:flex-end;gap:8px;padding-top:10px}.pagination span{color:var(--text-3);font-size:10px}</style>
