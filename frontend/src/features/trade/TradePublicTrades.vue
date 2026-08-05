<script setup lang="ts">
import type { Trade } from '../../api/trading'
import { useI18n } from '../../i18n'
import { shortTradeID } from './useTradeTerminal'
import { tradeEnumKey } from './labels'

defineProps<{
  trades: Trade[]
  transportState: string
  error: string
  lastGood: boolean
  panelState: string
  panelClass: string
}>()
const { tr } = useI18n()
function enumLabel(value: string): string { return tr(tradeEnumKey(value)) }
</script>

<template>
  <article class="card public-card">
    <header>
      <div><span>{{ tr('trade.public.eyebrow') }}</span><h2>{{ tr('trade.public.title') }}</h2></div>
      <div><span data-testid="panel-public-trades-state" class="panel-state" :class="panelClass">{{ panelState }}</span><span class="badge" :class="transportState === 'websocket' ? 'badge--live' : 'badge--delayed'">{{ enumLabel(transportState) }}</span></div>
    </header>
    <div v-if="error" class="panel-warning">{{ tr('trade.panel.failed', { panel: tr('trade.public.title'), error: tr('trade.error.backend_unavailable') }) }}<template v-if="lastGood">{{ tr('trade.panel.keepLastGood') }}</template></div>
    <div class="trade-list">
      <div v-for="trade in trades" :key="trade.id" class="trade-row"><strong>{{ trade.price }}</strong><span>{{ trade.quantity }} BTC</span><code>{{ shortTradeID(trade.id) }}</code></div>
      <div v-if="!trades.length" class="empty">{{ tr('trade.public.empty') }}</div>
    </div>
  </article>
</template>

<style scoped>
.public-card { overflow: hidden; }header { min-height: 64px; padding: 14px 18px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; gap: 12px; }header>div:last-child { display: flex; align-items: center; gap: 8px; }header span { color: var(--text-3); font-size: 11px; font-weight: 600; text-transform: uppercase; }header h2 { margin-top: 3px; font-size: 16px; }.panel-state { font: 600 10px var(--font-mono); }.panel-state--current{color:var(--up)!important}.panel-state--last-good{color:var(--warn)!important}.panel-state--unavailable{color:var(--down)!important}.panel-warning { padding: 8px 18px; color: #805700; background: #fff8df; font-size: 11px; }.trade-list { max-height: 300px; overflow: auto; }.trade-row { display: grid; grid-template-columns: .8fr 1fr 1.1fr; gap: 8px; padding: 10px 16px; border-bottom: 1px solid var(--border); font: 11px var(--font-mono); }.trade-row strong { color: var(--up); }.trade-row code { color: var(--text-3); }.empty { padding: 18px; color: var(--text-3); font-size: 12px; text-align: center; }
</style>
