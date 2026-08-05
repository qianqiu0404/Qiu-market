<script setup lang="ts">
import { computed } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import TradeActivity from '../features/trade/TradeActivity.vue'
import TradeBalances from '../features/trade/TradeBalances.vue'
import TradeChartPanel from '../features/trade/TradeChartPanel.vue'
import TradeOrderBook from '../features/trade/TradeOrderBook.vue'
import TradeOrderDrawer from '../features/trade/TradeOrderDrawer.vue'
import TradeOrderEntry from '../features/trade/TradeOrderEntry.vue'
import TradePublicTrades from '../features/trade/TradePublicTrades.vue'
import TradeStatusStrip from '../features/trade/TradeStatusStrip.vue'
import { tradeEnumKey } from '../features/trade/labels'
import {
  TRADE_KLINE_INTERVALS,
  useTradeTerminal,
} from '../features/trade/useTradeTerminal'

const terminal = useTradeTerminal()
const referenceFormatted = computed(() => terminal.referencePrice.value === null
  ? terminal.tr('trade.status.unavailable')
  : new Intl.NumberFormat(terminal.locale.value, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(terminal.referencePrice.value))
const referenceFresh = computed(() =>
  terminal.referencePrice.value !== null && terminal.referenceFreshness.value === 'fresh')
const referenceStateLabel = computed(() =>
  `${terminal.tr(tradeEnumKey(terminal.referenceFreshness.value))} · ${terminal.tr(tradeEnumKey(terminal.referenceConfidence.value))}`)
</script>

<template>
  <div class="trade-page">
    <PageHeader :title="terminal.tr('trade.title')" :subtitle="terminal.tr('trade.subtitle')">
      <template #actions>
        <span class="badge badge--delayed">{{ terminal.tr('trade.virtualOnly') }}</span>
        <a
          v-if="!terminal.principal.value && terminal.authCapabilities.value.github_oauth_enabled"
          class="btn"
          href="/api/v1/trading/auth/github/start"
        >{{ terminal.tr('trade.login.github') }}</a>
        <button
          v-if="!terminal.principal.value && terminal.authCapabilities.value.local_login_enabled"
          class="btn btn--accent"
          :disabled="terminal.busy.value"
          @click="terminal.localLogin"
        >{{ terminal.tr('trade.login.local') }}</button>
        <span
          v-if="!terminal.principal.value && !terminal.authCapabilities.value.github_oauth_enabled && !terminal.authCapabilities.value.local_login_enabled"
          class="badge badge--offline"
        >{{ terminal.tr('trade.login.unavailable') }}</span>
        <button v-if="terminal.principal.value" class="btn" @click="terminal.logout">
          {{ terminal.tr('trade.logout', { login: terminal.principal.value.github_login }) }}
        </button>
      </template>
    </PageHeader>

    <div v-if="terminal.errorMessage.value" class="trade-toast trade-toast--error">{{ terminal.errorMessage.value }}</div>
    <div v-if="terminal.notice.value" class="trade-toast trade-toast--success">{{ terminal.notice.value }}</div>
    <div v-if="terminal.pendingWrite.value" class="pending-write card">
      <span>{{ terminal.pendingWriteLabel.value }}</span>
      <button class="btn" :disabled="!terminal.canReconcilePending.value" @click="terminal.reconcilePendingWrite">
        {{ terminal.pendingWrite.value.state === 'submitted'
          ? terminal.tr('trade.unknown.submitted')
          : terminal.pendingWrite.value.state === 'reconciling'
            ? terminal.tr('trade.unknown.reconciling')
            : terminal.canReconcilePending.value
              ? terminal.tr('trade.unknown.reconcile')
              : terminal.tr('trade.unknown.originalAccount') }}
      </button>
    </div>

    <section class="market-header card">
      <div><span>BTC / USDT</span><strong>{{ referenceFormatted }} <small>USDT</small></strong></div>
      <div><span>{{ terminal.tr('trade.status.reference') }}</span><strong :class="`freshness--${terminal.referenceFreshness.value}`">{{ referenceStateLabel }}</strong></div>
      <div><span>{{ terminal.tr('trade.status.bestBid') }}</span><strong>{{ terminal.bestBid.value || '—' }}</strong></div>
      <div><span>{{ terminal.tr('trade.status.bestAsk') }}</span><strong>{{ terminal.bestAsk.value || '—' }}</strong></div>
    </section>

    <TradeStatusStrip
      :availability="terminal.terminalHealth.value.availability"
      :matching-state="terminal.terminalHealth.value.matchingState"
      :liquidity-state="terminal.terminalHealth.value.liquidityState"
      :transport-state="terminal.terminalHealth.value.transportState"
      :data-age-seconds="terminal.terminalHealth.value.dataAgeSeconds"
      :last-success-at="terminal.lastSuccessAt.value"
      :write-gate-reason="terminal.writeGateReason.value"
    />
    <div v-if="terminal.eventReconcilePending.value || terminal.cursorError.value" class="transport-warning" data-testid="transport-reconcile" aria-live="polite">
      {{ terminal.eventReconcilePending.value
        ? terminal.tr('trade.transport.reconciling')
        : terminal.tr('trade.transport.degraded') }}
    </div>

    <section class="trading-workspace">
      <TradeChartPanel
        :provider="terminal.klineProvider.value"
        :interval="terminal.klineInterval.value"
        :intervals="TRADE_KLINE_INTERVALS"
        :klines="terminal.klines.value"
        :error="terminal.klineError.value || terminal.panels.kline.error"
        :reference-error="terminal.referenceError.value"
        :reference-observed-at="terminal.referenceObservedAt.value"
        :panel-state="terminal.panelStateLabel('kline')"
        :panel-class="terminal.panelStateClass('kline')"
        @update:interval="terminal.klineInterval.value = $event"
      />
      <TradeOrderBook
        :book="terminal.book.value"
        :best-bid="terminal.bestBid.value"
        :best-ask="terminal.bestAsk.value"
        :matching-state="terminal.terminalHealth.value.matchingState"
        :error="terminal.panels.orderbook.error"
        :last-good="terminal.panels.orderbook.lastSuccessAt > 0"
        :panel-state="terminal.panelStateLabel('orderbook')"
        :panel-class="terminal.panelStateClass('orderbook')"
        @price="terminal.form.price = $event"
        @bid="terminal.useBookPrice('bid')"
        @ask="terminal.useBookPrice('ask')"
      />
      <TradeOrderEntry
        :form="terminal.form"
        :logged-in="terminal.loggedIn.value"
        :writes-enabled="terminal.submitEnabled.value"
        :reference-fresh="referenceFresh"
        :available-b-t-c="terminal.availableBTC.value"
        :available-u-s-d-t="terminal.availableUSDT.value"
        :preview="terminal.orderPreview.value"
        :pending="Boolean(terminal.pendingWrite.value)"
        @submit="terminal.submitOrder"
        @reset="terminal.resetClientOrderID"
        @reference="terminal.useReferencePrice"
        @percent="terminal.applyPercent"
      />
    </section>

    <section class="account-row">
      <TradeBalances
        :balances="terminal.balances.value"
        :logged-in="terminal.loggedIn.value"
        :error="terminal.panels.balances.error"
        :last-good="terminal.panels.balances.lastSuccessAt > 0"
        :panel-state="terminal.panelStateLabel('balances')"
        :panel-class="terminal.panelStateClass('balances')"
      />
      <TradePublicTrades
        :trades="terminal.publicTrades.value"
        :transport-state="terminal.terminalHealth.value.transportState"
        :error="terminal.panels.publicTrades.error"
        :last-good="terminal.panels.publicTrades.lastSuccessAt > 0"
        :panel-state="terminal.panelStateLabel('publicTrades')"
        :panel-class="terminal.panelStateClass('publicTrades')"
      />
    </section>

    <TradeActivity
      :orders="terminal.orders.value"
      :trades="terminal.privateTrades.value"
      :ledger="terminal.ledgerEntries.value"
      :scope="terminal.orderScope.value"
      :orders-page="terminal.orderPage.page"
      :orders-next="Boolean(terminal.orderPage.nextCursor)"
      :trades-page="terminal.tradePage.page"
      :trades-next="Boolean(terminal.tradePage.nextCursor)"
      :ledger-page="terminal.ledgerPage.page"
      :ledger-next="Boolean(terminal.ledgerPage.nextCursor)"
      :orders-error="terminal.panels.orders.error"
      :trades-error="terminal.panels.privateTrades.error"
      :ledger-error="terminal.panels.ledger.error"
      :orders-state="terminal.panelStateLabel('orders')"
      :orders-class="terminal.panelStateClass('orders')"
      :trades-state="terminal.panelStateLabel('privateTrades')"
      :trades-class="terminal.panelStateClass('privateTrades')"
      :ledger-state="terminal.panelStateLabel('ledger')"
      :ledger-class="terminal.panelStateClass('ledger')"
      :writes-enabled="terminal.writesEnabled.value"
      :orders-busy="terminal.pageBusy.orders"
      :trades-busy="terminal.pageBusy.trades"
      :ledger-busy="terminal.pageBusy.ledger"
      @refresh="terminal.refreshAll"
      @scope="terminal.changeOrderScope"
      @order="terminal.openOrder"
      @cancel="terminal.cancelOrder"
      @orders-previous="terminal.previousOrders"
      @orders-next="terminal.nextOrders"
      @trades-previous="terminal.previousTrades"
      @trades-next="terminal.nextTrades"
      @ledger-previous="terminal.previousLedger"
      @ledger-next="terminal.nextLedger"
    />

    <TradeOrderDrawer
      v-if="terminal.selectedOrder.value"
      :order="terminal.selectedOrder.value"
      :events="terminal.orderEvents.value"
      :pending="terminal.pendingWrite.value"
      :error="terminal.panels.orderEvents.error"
      :page="terminal.eventPage.page"
      :has-next="Boolean(terminal.eventPage.nextCursor)"
      :busy="terminal.pageBusy.events"
      @close="terminal.closeOrder"
      @previous="terminal.previousEvents"
      @next="terminal.nextEvents"
    />
  </div>
</template>

<style scoped>
.trade-page { display: grid; gap: 14px; max-width: 1560px; margin: 0 auto; }
.trade-toast { position: fixed; top: 72px; left: 50%; z-index: 90; transform: translateX(-50%); width: min(560px,calc(100vw - 32px)); padding: 12px 16px; border: 1px solid var(--border); border-radius: var(--radius-sm); box-shadow: var(--shadow-float); }.trade-toast--error { color: var(--down); background: #fff0f2; }.trade-toast--success { color: var(--up); background: #e9f7f1; }
.pending-write { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 16px; color: #805700; background: #fff8df; font-size: 11px; }
.market-header { display: grid; grid-template-columns: 1.4fr repeat(3,1fr); overflow: hidden; }.market-header>div { display: grid; gap: 5px; padding: 13px 18px; border-right: 1px solid var(--border); }.market-header>div:last-child { border-right: 0; }.market-header span { color: var(--text-3); font-size: 9px; text-transform: uppercase; }.market-header strong { font: 15px var(--font-mono); }.market-header>div:first-child strong { font-size: 22px; }.market-header small { color: var(--text-3); font-size: 10px; }.freshness--fresh { color: var(--up); }.freshness--stale { color: var(--warn); }.freshness--unavailable { color: var(--down); }
.transport-warning { padding: 10px 14px; border: 1px solid #f0d58a; border-radius: var(--radius-sm); color: #805700; background: #fff8df; font: 11px var(--font-mono); }
.trading-workspace { display: grid; grid-template-columns: minmax(0,1.7fr) minmax(300px,.72fr) minmax(310px,.72fr); gap: 14px; align-items: start; }.account-row { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
@media(max-width:1260px){.trading-workspace{grid-template-columns:minmax(0,1.4fr) minmax(300px,.75fr)}.trading-workspace>:last-child{grid-column:1/-1}.market-header{grid-template-columns:repeat(2,1fr)}}
@media(max-width:900px){.trading-workspace,.account-row{grid-template-columns:1fr}.trading-workspace>:last-child{grid-column:auto}.market-header{grid-template-columns:1fr 1fr}}
@media(max-width:560px){.market-header{grid-template-columns:1fr}.market-header>div{border-right:0;border-bottom:1px solid var(--border)}.pending-write{align-items:flex-start;flex-direction:column}}
</style>
