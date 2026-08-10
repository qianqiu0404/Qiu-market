<script setup lang="ts">
import type { OrderBook } from '../../api/trading'
import { useI18n } from '../../i18n'
import { tradeEnumKey } from './labels'

defineProps<{
  book: OrderBook
  bestBid: string
  bestAsk: string
  matchingState: string
  error: string
  lastGood: boolean
  panelState: string
  panelClass: string
}>()
const emit = defineEmits<{
  price: [value: string]
  bid: []
  ask: []
}>()
const { tr } = useI18n()
function enumLabel(value: string): string { return tr(tradeEnumKey(value)) }
</script>

<template>
  <article class="card book-card">
    <header>
      <div><span>{{ tr('trade.book.eyebrow') }}</span><h2>{{ tr('trade.book.title') }}</h2></div>
      <div class="book-head-meta">
        <span data-testid="panel-orderbook-state" class="panel-state" :class="panelClass">{{ panelState }}</span>
        <code>#{{ book.sequence }}</code>
      </div>
    </header>
    <div v-if="error" class="panel-warning">
      {{ tr('trade.panel.failed', { panel: tr('trade.book.title'), error: tr('trade.error.backend_unavailable') }) }}
      <template v-if="lastGood">{{ tr('trade.panel.keepLastGood') }}</template>
    </div>
    <div class="book-heading"><span>{{ tr('trade.book.price') }}</span><span>{{ tr('trade.book.quantity') }}</span><span>{{ tr('trade.book.orders') }}</span></div>
    <div class="book-side asks">
      <button v-for="level in [...book.asks].reverse()" :key="`ask-${level.price}`" @click="emit('price', level.price)">
        <span>{{ level.price }}</span><span>{{ level.quantity }}</span><span>{{ level.order_count }}</span>
      </button>
      <div v-if="!book.asks.length" class="empty">{{ tr('trade.book.noAsks') }}</div>
    </div>
    <div class="spread">
      <button :disabled="!bestBid" @click="emit('bid')">{{ tr('trade.book.bid', { price: bestBid || '—' }) }}</button>
      <strong>{{ enumLabel(matchingState) }}</strong>
      <button :disabled="!bestAsk" @click="emit('ask')">{{ tr('trade.book.ask', { price: bestAsk || '—' }) }}</button>
    </div>
    <div class="book-side bids">
      <button v-for="level in book.bids" :key="`bid-${level.price}`" @click="emit('price', level.price)">
        <span>{{ level.price }}</span><span>{{ level.quantity }}</span><span>{{ level.order_count }}</span>
      </button>
      <div v-if="!book.bids.length" class="empty">{{ tr('trade.book.noBids') }}</div>
    </div>
  </article>
</template>

<style scoped>
.book-card { overflow: hidden; }
header { min-height: 64px; padding: 14px 16px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; gap: 12px; }
header span { color: var(--text-3); font-size: 11px; font-weight: 600; text-transform: uppercase; }
header h2 { margin-top: 3px; font-size: 16px; }
.book-head-meta { display: flex; align-items: center; gap: 8px; }
.book-head-meta code { color: var(--text-3); font-size: 10px; }
.panel-state { font: 600 10px var(--font-mono); }
.panel-state--current { color: var(--up) !important; }.panel-state--last-good { color: var(--warn) !important; }.panel-state--unavailable { color: var(--down) !important; }
.panel-warning { padding: 8px 16px; color: #805700; background: #fff8df; font-size: 11px; }
.book-heading,.book-side button { display: grid; grid-template-columns: 1.2fr 1fr .45fr; gap: 8px; padding: 7px 16px; text-align: right; font: 11px var(--font-mono); }
.book-heading { color: var(--text-3); }.book-heading span:first-child,.book-side button span:first-child { text-align: left; }
.book-side { min-height: 136px; }.book-side button { width: 100%; min-height: 44px; border: 0; color: var(--text-1); background: transparent; cursor: pointer; }.book-side button:hover { background: var(--bg-panel-2); }
.asks button span:first-child { color: var(--down); }.bids button span:first-child { color: var(--up); }
.spread { display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; gap: 8px; padding: 9px 16px; border-block: 1px solid var(--border); background: var(--bg-panel-2); }
.spread button { min-height: 44px; border: 0; color: var(--text-3); background: transparent; font-size: 10px; }.spread button:last-child { text-align: right; }.spread strong { color: var(--accent); font-size: 11px; }
.empty { padding: 18px; color: var(--text-3); font-size: 12px; text-align: center; }
</style>
