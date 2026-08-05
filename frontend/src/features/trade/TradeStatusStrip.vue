<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '../../i18n'

const props = defineProps<{
  availability: 'LIVE' | 'DEGRADED' | 'OFFLINE'
  matchingState: string
  liquidityState: string
  transportState: string
  dataAgeSeconds: number
  lastSuccessAt: number
  writeGateReason: string
}>()

const { locale, tr } = useI18n()
const lastSuccess = computed(() => props.lastSuccessAt
  ? new Intl.DateTimeFormat(locale.value, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    .format(new Date(props.lastSuccessAt))
  : tr('trade.status.noObservation'))
</script>

<template>
  <section class="trade-status card" :class="`trade-status--${availability.toLowerCase()}`">
    <div class="trade-status__primary">
      <span class="trade-status__dot" aria-hidden="true"></span>
      <strong data-testid="terminal-availability">{{ availability }}</strong>
      <span>{{ tr('trade.status.lastSuccess', { time: lastSuccess }) }}</span>
    </div>
    <div class="trade-status__facts">
      <span data-testid="matching-state">{{ tr('trade.status.matching', { state: matchingState }) }}</span>
      <span>{{ tr('trade.status.liquidity', { state: liquidityState }) }}</span>
      <span data-testid="transport-state">{{ tr('trade.status.transport', { state: transportState }) }}</span>
      <span>{{ tr('trade.status.age', { age: dataAgeSeconds < 0 ? '—' : `${dataAgeSeconds}s` }) }}</span>
    </div>
    <div class="trade-status__gate" data-testid="write-gate-reason">
      {{ writeGateReason === 'ready'
        ? tr('trade.status.writeReady')
        : tr('trade.status.writeBlocked', { reason: writeGateReason }) }}
      <RouterLink to="/system">{{ tr('trade.status.systemLink') }}</RouterLink>
    </div>
  </section>
</template>

<style scoped>
.trade-status {
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: center;
  gap: 18px;
  padding: 12px 16px;
  border-left: 4px solid var(--warn);
  font-size: 11px;
}
.trade-status--live { border-left-color: var(--up); }
.trade-status--offline { border-left-color: var(--down); }
.trade-status__primary,
.trade-status__facts,
.trade-status__gate { display: flex; align-items: center; gap: 9px; }
.trade-status__primary strong { font-size: 13px; }
.trade-status__primary span,
.trade-status__facts { color: var(--text-3); }
.trade-status__facts { justify-content: center; flex-wrap: wrap; font-family: var(--font-mono); }
.trade-status__gate { justify-content: flex-end; color: var(--text-2); }
.trade-status__gate a { color: var(--accent); white-space: nowrap; }
.trade-status__dot { width: 8px; height: 8px; border-radius: 50%; background: var(--warn); }
.trade-status--live .trade-status__dot { background: var(--up); }
.trade-status--offline .trade-status__dot { background: var(--down); }
@media (max-width: 900px) {
  .trade-status { grid-template-columns: 1fr; gap: 7px; }
  .trade-status__facts,
  .trade-status__gate { justify-content: flex-start; }
}
</style>
