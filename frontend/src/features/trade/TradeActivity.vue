<script setup lang="ts">
import { ref } from 'vue'
import type {
  TradeV1AccountTrade,
  TradeV1LedgerEntry,
  TradeV1Order,
  TradeV1OrderScope,
} from '../../api/trade-v1-contract'
import { useI18n } from '../../i18n'
import { shortTradeID } from './useTradeTerminal'
import { tradeEnumKey } from './labels'
import TradePagination from './TradePagination.vue'

defineProps<{
  orders: TradeV1Order[]
  trades: TradeV1AccountTrade[]
  ledger: TradeV1LedgerEntry[]
  scope: TradeV1OrderScope
  ordersPage: number
  ordersNext: boolean
  tradesPage: number
  tradesNext: boolean
  ledgerPage: number
  ledgerNext: boolean
  ordersError: string
  tradesError: string
  ledgerError: string
  ordersState: string
  ordersClass: string
  tradesState: string
  tradesClass: string
  ledgerState: string
  ledgerClass: string
  writesEnabled: boolean
  ordersBusy: boolean
  tradesBusy: boolean
  ledgerBusy: boolean
}>()
const emit = defineEmits<{
  refresh: []
  scope: [value: TradeV1OrderScope]
  order: [value: TradeV1Order]
  cancel: [value: TradeV1Order]
  ordersPrevious: []
  ordersNext: []
  tradesPrevious: []
  tradesNext: []
  ledgerPrevious: []
  ledgerNext: []
}>()
const { locale, tr } = useI18n()
const active = ref<'orders' | 'trades' | 'ledger'>('orders')

function time(value: string): string {
  if (!value) return '—'
  return new Intl.DateTimeFormat(locale.value, {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
  }).format(new Date(value))
}
function enumLabel(value: string): string {
  return tr(tradeEnumKey(value))
}
</script>

<template>
  <section class="card activity-card">
    <header>
      <h2>{{ tr('trade.activity.title') }}</h2>
      <button class="btn" @click="emit('refresh')">{{ tr('trade.activity.refresh') }}</button>
    </header>
    <nav class="tabs">
      <button :class="{ active: active === 'orders' }" @click="active = 'orders'">{{ tr('trade.activity.open') }} / {{ tr('trade.activity.history') }}</button>
      <button :class="{ active: active === 'trades' }" @click="active = 'trades'">{{ tr('trade.activity.trades') }}</button>
      <button :class="{ active: active === 'ledger' }" @click="active = 'ledger'">{{ tr('trade.activity.ledger') }}</button>
    </nav>

    <div v-if="active === 'orders'" class="activity-body">
      <div class="activity-toolbar">
        <div class="scope-switch"><button v-for="value in (['open','history','all'] as const)" :key="value" :class="{ active: scope === value }" @click="emit('scope', value)">{{ value === 'open' ? tr('trade.activity.open') : value === 'history' ? tr('trade.activity.history') : tr('trade.activity.all') }}</button></div>
        <span data-testid="panel-orders-state" class="panel-state" :class="ordersClass">{{ ordersState }}</span>
      </div>
      <p v-if="ordersError" class="panel-warning">{{ tr('trade.panel.failed', { panel: tr('trade.activity.open'), error: tr('trade.error.backend_unavailable') }) }}</p>
      <div class="record-table"><div class="record-head order-grid"><span>{{ tr('trade.activity.order') }}</span><span>{{ tr('trade.activity.side') }}</span><span>{{ tr('trade.activity.price') }}</span><span>{{ tr('trade.activity.filled') }}</span><span>{{ tr('trade.activity.remaining') }}</span><span>{{ tr('trade.activity.status') }}</span><span></span></div><div v-for="order in orders" :key="order.id" class="record-row order-grid clickable" @click="emit('order', order)"><button class="order-details" :aria-label="`${tr('trade.activity.details')} ${shortTradeID(order.id)}`" @click.stop="emit('order', order)"><code>{{ shortTradeID(order.id) }}</code></button><span :class="order.side">{{ enumLabel(order.side) }}</span><span>{{ order.price || enumLabel('market') }}</span><span>{{ order.filled_quantity }}</span><span>{{ order.remaining_quantity || order.remaining_quote_budget }}</span><span>{{ enumLabel(order.status) }}</span><span><button v-if="order.status === 'open' || order.status === 'partially_filled'" class="cancel" :disabled="!writesEnabled" @click.stop="emit('cancel', order)">{{ tr('trade.activity.cancel') }}</button><span v-else>{{ tr('trade.activity.details') }}</span></span></div><div v-if="!orders.length" class="empty">{{ tr('trade.activity.emptyOrders') }}</div></div>
      <TradePagination :page="ordersPage" :has-previous="ordersPage > 1" :has-next="ordersNext" :busy="ordersBusy" @previous="emit('ordersPrevious')" @next="emit('ordersNext')" />
    </div>

    <div v-else-if="active === 'trades'" class="activity-body">
      <div class="activity-toolbar"><span data-testid="panel-private-trades-state" class="panel-state" :class="tradesClass">{{ tradesState }}</span></div>
      <p v-if="tradesError" class="panel-warning">{{ tr('trade.panel.failed', { panel: tr('trade.activity.trades'), error: tr('trade.error.backend_unavailable') }) }}</p>
      <div class="record-table"><div class="record-head trade-grid"><span>ID</span><span>{{ tr('trade.activity.side') }}</span><span>{{ tr('trade.activity.price') }}</span><span>{{ tr('trade.activity.filled') }}</span><span>{{ tr('trade.activity.role') }}</span><span>{{ tr('trade.activity.fee') }}</span><span>{{ tr('trade.activity.time') }}</span></div><div v-for="trade in trades" :key="`${trade.id}:${trade.order_id}`" class="record-row trade-grid"><code>{{ shortTradeID(trade.id) }}</code><span :class="trade.side">{{ enumLabel(trade.side) }}</span><span>{{ trade.price }}</span><span>{{ trade.quantity }}</span><span>{{ enumLabel(trade.liquidity_role) }}</span><span>{{ trade.fee_amount }} {{ trade.fee_asset }} · {{ trade.fee_rate_bps }} bps</span><span>{{ time(trade.occurred_at) }}</span></div><div v-if="!trades.length" class="empty">{{ tr('trade.activity.emptyTrades') }}</div></div>
      <TradePagination :page="tradesPage" :has-previous="tradesPage > 1" :has-next="tradesNext" :busy="tradesBusy" @previous="emit('tradesPrevious')" @next="emit('tradesNext')" />
    </div>

    <div v-else class="activity-body">
      <div class="activity-toolbar"><span data-testid="panel-ledger-state" class="panel-state" :class="ledgerClass">{{ ledgerState }}</span></div>
      <p v-if="ledgerError" class="panel-warning">{{ tr('trade.panel.failed', { panel: tr('trade.activity.ledger'), error: tr('trade.error.backend_unavailable') }) }}</p>
      <div class="record-table"><div class="record-head ledger-grid"><span>{{ tr('trade.activity.time') }}</span><span>{{ tr('trade.activity.asset') }}</span><span>{{ tr('trade.activity.bucket') }}</span><span>{{ tr('trade.activity.amount') }}</span><span>{{ tr('trade.activity.reason') }}</span><span>{{ tr('trade.activity.reference') }}</span><span>{{ tr('trade.activity.cursor') }}</span></div><div v-for="entry in ledger" :key="entry.entry_id" class="record-row ledger-grid"><span>{{ time(entry.occurred_at) }}</span><span>{{ entry.asset }}</span><span>{{ enumLabel(entry.bucket) }}</span><strong :class="entry.amount.startsWith('-') ? 'debit' : 'credit'">{{ entry.amount }}</strong><span>{{ enumLabel(entry.reason) }}</span><code>{{ shortTradeID(entry.reference) }}</code><code>{{ entry.sequence }}:{{ entry.entry_index }}</code></div><div v-if="!ledger.length" class="empty">{{ tr('trade.activity.emptyLedger') }}</div></div>
      <TradePagination :page="ledgerPage" :has-previous="ledgerPage > 1" :has-next="ledgerNext" :busy="ledgerBusy" @previous="emit('ledgerPrevious')" @next="emit('ledgerNext')" />
    </div>
  </section>
</template>

<style scoped>
.activity-card { overflow: hidden; }header { min-height: 60px; padding: 14px 18px; display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid var(--border); }header h2 { font-size: 16px; }.tabs { display: flex; gap: 2px; padding: 8px 18px 0; border-bottom: 1px solid var(--border); }.tabs button { min-height: 44px; border: 0; border-bottom: 2px solid transparent; padding: 10px 12px; color: var(--text-3); background: transparent; cursor: pointer; }.tabs button.active { color: var(--accent); border-bottom-color: var(--accent); }.activity-body { padding: 14px 18px 18px; }.activity-toolbar { min-height: 44px; display: flex; justify-content: flex-end; align-items: center; gap: 10px; margin-bottom: 8px; }.scope-switch { display: flex; margin-right: auto; border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }.scope-switch button { min-height: 44px; min-width: 44px; border: 0; padding: 6px 10px; color: var(--text-2); background: transparent; cursor: pointer; }.scope-switch button.active { color: var(--accent); background: var(--bg-panel-2); }.panel-state { color: var(--text-3); font: 600 10px var(--font-mono); }.panel-state--current { color: var(--up); }.panel-state--last-good { color: var(--warn); }.panel-state--unavailable { color: var(--down); }.panel-warning { padding: 8px 10px; color: #805700; background: #fff8df; font-size: 11px; }.record-table { overflow-x: auto; border: 1px solid var(--border); border-radius: var(--radius-sm); }.record-head,.record-row { min-width: 960px; align-items: center; gap: 12px; padding: 10px 12px; font-size: 11px; }.record-head { color: var(--text-3); background: var(--bg-panel-2); font-weight: 600; }.record-row { width: 100%; border: 0; border-top: 1px solid var(--border); text-align: left; color: var(--text-1); background: var(--bg-panel); }.record-row.clickable { cursor: pointer; }.record-row.clickable:hover { background: var(--bg-panel-2); }.order-details,.cancel { min-height: 44px; min-width: 44px; border: 0; background: transparent; cursor: pointer; }.order-details { justify-self: start; padding: 0; color: inherit; }.order-details:focus,.order-details:focus-visible { outline: 3px solid color-mix(in srgb, var(--accent) 72%, white); outline-offset: 2px; }.order-grid { display: grid; grid-template-columns: 1.2fr .55fr .75fr .75fr .75fr .9fr .6fr; }.trade-grid { display: grid; grid-template-columns: 1fr .5fr .7fr .7fr .55fr 1.3fr 1fr; }.ledger-grid { display: grid; grid-template-columns: 1fr .45fr .65fr .8fr 1fr 1.25fr .8fr; }.buy,.credit { color: var(--up); }.sell,.debit { color: var(--down); }.cancel { color: var(--down); }.empty { padding: 20px; color: var(--text-3); text-align: center; font-size: 12px; }
</style>
