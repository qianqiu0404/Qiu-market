<script setup lang="ts">
import type { TradeV1Order, TradeV1OrderEvent } from '../../api/trade-v1-contract'
import type { PendingTradingWrite } from '../../trading/pending-write'
import { useI18n } from '../../i18n'
import { shortTradeID } from './useTradeTerminal'
import TradePagination from './TradePagination.vue'

const props = defineProps<{
  order: TradeV1Order
  events: TradeV1OrderEvent[]
  pending: PendingTradingWrite | null
  error: string
  page: number
  hasNext: boolean
}>()
const emit = defineEmits<{ close: []; previous: []; next: [] }>()
const { locale, tr } = useI18n()

function time(value: string): string {
  return value ? new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value)) : '—'
}
function pendingForOrder(): boolean {
  return props.pending?.order_id === props.order.id || props.pending?.request_id === props.order.client_order_id
}
</script>

<template>
  <Teleport to="body">
    <div class="drawer-backdrop" @click.self="emit('close')">
      <aside class="order-drawer" role="dialog" aria-modal="true" :aria-label="tr('trade.drawer.title')">
        <header><div><span>{{ tr('trade.drawer.title') }}</span><h2>{{ shortTradeID(order.id) }}</h2></div><button class="btn" @click="emit('close')">{{ tr('trade.drawer.close') }}</button></header>
        <section class="order-summary"><div><span>{{ tr('trade.activity.side') }}</span><strong :class="order.side">{{ order.side }} · {{ order.type }}</strong></div><div><span>{{ tr('trade.activity.status') }}</span><strong>{{ order.status }}</strong></div><div><span>{{ tr('trade.drawer.original') }}</span><strong>{{ order.original_quantity || order.original_quote_budget }}</strong></div><div><span>{{ tr('trade.activity.filled') }}</span><strong>{{ order.filled_quantity }}</strong></div><div><span>{{ tr('trade.drawer.average') }}</span><strong>{{ order.average_fill_price || '—' }}</strong></div><div><span>{{ tr('trade.drawer.held') }}</span><strong>{{ order.held_amount }} {{ order.held_asset }}</strong></div><div><span>{{ tr('trade.drawer.spent') }}</span><strong>{{ order.spent_quote }}</strong></div><div><span>{{ tr('trade.drawer.clientID') }}</span><code>{{ order.client_order_id }}</code></div></section>
        <section class="timeline-section"><h3>{{ tr('trade.drawer.timeline') }}</h3><p>{{ tr('trade.drawer.eventTruth') }}</p><div v-if="pendingForOrder()" class="client-observation"><strong>{{ tr('trade.drawer.clientObservation') }}</strong><span>{{ tr('trade.drawer.unknownObservation') }}</span><code>{{ pending?.request_id }}</code></div><p v-if="error" class="panel-warning">{{ error }}</p><ol class="timeline"><li v-for="event in events" :key="event.event_id"><span class="timeline-dot"></span><div class="timeline-card"><header><strong>{{ event.type }}</strong><time>{{ time(event.occurred_at) }}</time></header><p>{{ event.status }}<template v-if="event.quantity"> · {{ event.quantity }} BTC</template><template v-if="event.price"> @ {{ event.price }} USDT</template></p><p v-if="event.fee">{{ tr('trade.activity.fee') }} {{ event.fee.amount }} {{ event.fee.asset }} · {{ event.fee.role }} {{ event.fee.rate_bps }} bps</p><div v-if="event.balance_effects.length" class="effects"><span>{{ tr('trade.drawer.balanceEffects') }}</span><code v-for="effect in event.balance_effects" :key="`${effect.transaction_id}:${effect.asset}:${effect.bucket}`">{{ effect.amount }} {{ effect.asset }} · {{ effect.bucket }} · {{ effect.reason }}</code></div><small>{{ tr('trade.drawer.source', { cursor: `${event.sequence}:${event.event_index}:${event.timeline_index}` }) }}</small></div></li></ol><div v-if="!events.length && !error" class="empty">{{ tr('trade.drawer.noEvents') }}</div><TradePagination :page="page" :has-previous="page > 1" :has-next="hasNext" @previous="emit('previous')" @next="emit('next')" /></section>
      </aside>
    </div>
  </Teleport>
</template>

<style scoped>
.drawer-backdrop { position: fixed; inset: 0; z-index: 110; display: flex; justify-content: flex-end; background: rgba(29,29,31,.28); }.order-drawer { width: min(620px,100vw); height: 100%; overflow-y: auto; background: var(--bg-panel); box-shadow: -18px 0 48px rgba(0,0,0,.16); }.order-drawer>header { position: sticky; top: 0; z-index: 2; min-height: 68px; padding: 14px 18px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; background: rgba(255,255,255,.96); }.order-drawer>header span { color: var(--text-3); font-size: 10px; text-transform: uppercase; }.order-drawer h2 { margin-top: 4px; font-size: 17px; }.order-summary { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 8px; padding: 16px 18px; border-bottom: 1px solid var(--border); }.order-summary>div { display: grid; gap: 4px; padding: 10px; border-radius: 8px; background: var(--bg-panel-2); }.order-summary span { color: var(--text-3); font-size: 9px; text-transform: uppercase; }.order-summary strong,.order-summary code { font: 11px var(--font-mono); overflow-wrap: anywhere; }.buy { color: var(--up); }.sell { color: var(--down); }.timeline-section { padding: 18px; }.timeline-section>h3 { font-size: 15px; }.timeline-section>p { color: var(--text-3); font-size: 11px; }.client-observation { display: grid; gap: 4px; margin: 12px 0; padding: 10px; border: 1px dashed var(--warn); border-radius: 8px; color: #805700; font-size: 11px; }.timeline { list-style: none; margin: 18px 0 0; padding: 0; }.timeline li { position: relative; display: grid; grid-template-columns: 16px 1fr; gap: 8px; padding-bottom: 12px; }.timeline li:not(:last-child)::before { content: ''; position: absolute; left: 5px; top: 12px; bottom: -4px; width: 1px; background: var(--border); }.timeline-dot { position: relative; z-index: 1; width: 11px; height: 11px; margin-top: 4px; border: 2px solid var(--accent); border-radius: 50%; background: var(--bg-panel); }.timeline-card { display: grid; gap: 6px; padding: 12px; border: 1px solid var(--border); border-radius: 10px; }.timeline-card header { display: flex; justify-content: space-between; gap: 8px; }.timeline-card time,.timeline-card small { color: var(--text-3); font-size: 9px; }.timeline-card p { margin: 0; font-size: 11px; }.effects { display: grid; gap: 4px; padding-top: 5px; border-top: 1px solid var(--border); }.effects span { color: var(--text-3); font-size: 9px; text-transform: uppercase; }.effects code { font-size: 10px; }.panel-warning { padding: 8px 10px; color: #805700; background: #fff8df; font-size: 11px; }.empty { padding: 20px; color: var(--text-3); text-align: center; font-size: 12px; }@media(max-width:520px){.order-summary{grid-template-columns:1fr}}
</style>
