<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { computed } from 'vue'
import { useI18n } from '../i18n'

const props = withDefaults(
  defineProps<{
    message?: string
  }>(),
  { message: undefined },
)

const { locale } = useI18n()
const message = computed(() => props.message || (locale.value === 'zh-CN'
  ? '无法加载数据，API 可能不可达。'
  : 'Unable to load data. The API may be unreachable.'))

const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="error-state">
    <AppIcon name="alert" :size="36" />
    <div class="error-title">{{ locale === 'zh-CN' ? '出现问题' : 'Something went wrong' }}</div>
    <div class="error-message">{{ message }}</div>
    <button type="button" class="btn" @click="emit('retry')">
      <AppIcon name="refresh" :size="15" />
      {{ locale === 'zh-CN' ? '重试' : 'Retry' }}
    </button>
  </div>
</template>

<style scoped>
.error-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 48px 24px;
  color: var(--down);
  text-align: center;
}

.error-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--text-1);
}

.error-message {
  font-size: 13px;
  color: var(--text-2);
  max-width: 460px;
  overflow-wrap: anywhere;
}

.error-state .btn {
  margin-top: 8px;
  color: var(--text-1);
}
</style>
