<script setup lang="ts">
import type { Balance } from '../../api/trading'
import { useI18n } from '../../i18n'

defineProps<{
  balances: Balance[]
  loggedIn: boolean
  error: string
  lastGood: boolean
  panelState: string
  panelClass: string
}>()
const { tr } = useI18n()
</script>

<template>
  <article class="card balance-card">
    <header>
      <div><span>{{ tr('trade.balance.eyebrow') }}</span><h2>{{ tr('trade.balance.title') }}</h2></div>
      <span data-testid="panel-balances-state" class="panel-state" :class="panelClass">{{ panelState }}</span>
    </header>
    <div v-if="error" class="panel-warning">
      {{ tr('trade.panel.failed', { panel: tr('trade.balance.title'), error }) }}
      <template v-if="lastGood">{{ tr('trade.panel.keepLastGood') }}</template>
    </div>
    <div class="balances">
      <div v-for="balance in balances" :key="balance.asset" class="asset-balance">
        <strong>{{ balance.asset }}</strong>
        <dl><div><dt>{{ tr('trade.balance.available') }}</dt><dd>{{ balance.available }}</dd></div><div><dt>{{ tr('trade.balance.held') }}</dt><dd>{{ balance.held }}</dd></div></dl>
      </div>
      <div v-if="!balances.length" class="empty">{{ loggedIn ? tr('trade.balance.empty') : tr('trade.balance.login') }}</div>
    </div>
  </article>
</template>

<style scoped>
.balance-card { overflow: hidden; }
header { min-height: 64px; padding: 14px 18px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; gap: 12px; }
header span { color: var(--text-3); font-size: 11px; font-weight: 600; text-transform: uppercase; } header h2 { margin-top: 3px; font-size: 16px; }
.panel-state { font: 600 10px var(--font-mono); }.panel-state--current { color: var(--up) !important; }.panel-state--last-good { color: var(--warn) !important; }.panel-state--unavailable { color: var(--down) !important; }
.panel-warning { padding: 8px 18px; color: #805700; background: #fff8df; font-size: 11px; }
.balances { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 10px; padding: 16px 18px 18px; }.asset-balance { padding: 14px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-panel-2); }.asset-balance>strong { display: block; margin-bottom: 10px; font-size: 12px; }.asset-balance dl,.asset-balance dl div { margin: 0; display: grid; gap: 4px; }.asset-balance dl { grid-template-columns: 1fr 1fr; }.asset-balance dt { color: var(--text-3); font-size: 9px; text-transform: uppercase; }.asset-balance dd { margin: 0; font: 13px var(--font-mono); overflow-wrap: anywhere; }
.empty { grid-column: 1/-1; padding: 18px; color: var(--text-3); font-size: 12px; text-align: center; }
@media(max-width:520px){.balances{grid-template-columns:1fr}}
</style>
