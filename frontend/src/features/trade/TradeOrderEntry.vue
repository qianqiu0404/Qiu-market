<script setup lang="ts">
import { computed } from 'vue'
import type { OrderPreview } from './decimal'
import { useI18n } from '../../i18n'
import { tradeEnumKey } from './labels'

const props = defineProps<{
  form: {
    clientOrderID: string
    side: 'buy' | 'sell'
    type: 'limit' | 'market'
    timeInForce: 'gtc' | 'ioc' | 'fok'
    postOnly: boolean
    price: string
    quantity: string
    quoteBudget: string
  }
  loggedIn: boolean
  writesEnabled: boolean
  referenceFresh: boolean
  availableBTC: string
  availableUSDT: string
  preview: OrderPreview
  pending: boolean
}>()
const emit = defineEmits<{
  submit: []
  reset: []
  reference: []
  percent: [value: 25 | 50 | 75 | 100]
}>()
const { tr } = useI18n()
function enumLabel(value: string): string { return tr(tradeEnumKey(value)) }
const marketBuy = computed(() => props.form.type === 'market' && props.form.side === 'buy')
const inputAsset = computed(() => marketBuy.value || props.form.side === 'buy' ? 'USDT' : 'BTC')
const available = computed(() => inputAsset.value === 'USDT' ? props.availableUSDT : props.availableBTC)

function previewText(value: bigint, asset: 'BTC' | 'USDT'): string {
  const precision = asset === 'BTC' ? 8 : 6
  const divisor = 10n ** BigInt(precision)
  const whole = value / divisor
  const fraction = (value % divisor).toString().padStart(precision, '0').replace(/0+$/, '')
  return `${fraction ? `${whole}.${fraction}` : whole} ${asset}`
}
</script>

<template>
  <article class="card entry-card">
    <header>
      <div><span>{{ tr('trade.entry.eyebrow') }}</span><h2>{{ tr('trade.entry.title') }}</h2></div>
      <span class="badge" :class="loggedIn ? 'badge--live' : 'badge--offline'">
        {{ loggedIn ? tr('trade.entry.bound') : tr('trade.entry.loginRequired') }}
      </span>
    </header>
    <form class="entry-body" @submit.prevent="emit('submit')">
      <div class="switch"><button type="button" :class="{ active: form.side === 'buy' }" @click="form.side = 'buy'">{{ tr('trade.entry.buy') }}</button><button type="button" :class="{ active: form.side === 'sell' }" @click="form.side = 'sell'">{{ tr('trade.entry.sell') }}</button></div>
      <div class="switch"><button type="button" :class="{ active: form.type === 'limit' }" @click="form.type = 'limit'">{{ enumLabel('limit') }}</button><button type="button" :class="{ active: form.type === 'market' }" @click="form.type = 'market'">{{ enumLabel('market') }}</button></div>
      <label>{{ tr('trade.entry.clientOrderID') }}<span class="input-pair"><input v-model="form.clientOrderID" class="input" autocomplete="off" /><button type="button" class="btn" :disabled="pending" @click="emit('reset')">{{ tr('trade.entry.reset') }}</button></span></label>
      <label v-if="form.type === 'limit'">{{ tr('trade.entry.price') }}<span class="input-pair"><input v-model="form.price" class="input" inputmode="decimal" :placeholder="tr('trade.entry.pricePlaceholder')" /><button type="button" class="btn" :disabled="!referenceFresh" @click="emit('reference')">{{ tr('trade.entry.referencePrice') }}</button></span></label>
      <label v-if="!marketBuy">{{ tr('trade.entry.quantity') }}<input v-model="form.quantity" class="input" inputmode="decimal" /></label>
      <label v-else>{{ tr('trade.entry.quoteBudget') }}<input v-model="form.quoteBudget" class="input" inputmode="decimal" /></label>
      <div class="percent-row">
        <span>{{ tr('trade.entry.available') }} {{ available }} {{ inputAsset }}</span>
        <div><button v-for="value in ([25,50,75,100] as const)" :key="value" type="button" @click="emit('percent', value)">{{ tr('trade.entry.percent', { percent: value }) }}</button></div>
      </div>
      <div v-if="form.type === 'limit'" class="tif-row">
        <label>{{ tr('trade.entry.timeInForce') }}<select v-model="form.timeInForce" class="input"><option value="gtc">{{ enumLabel('gtc') }}</option><option value="ioc">{{ enumLabel('ioc') }}</option><option value="fok">{{ enumLabel('fok') }}</option></select></label>
        <label class="checkbox"><input v-model="form.postOnly" type="checkbox" /> {{ tr('trade.entry.postOnly') }}</label>
      </div>
      <p class="hint">{{ form.type === 'market' ? (marketBuy ? tr('trade.entry.marketBuyHint') : tr('trade.entry.marketSellHint')) : tr('trade.entry.rules') }}</p>
      <p v-if="form.postOnly" class="hint">{{ tr('trade.entry.postOnlyHelp') }}</p>
      <div class="preview-grid">
        <span>{{ tr('trade.entry.total') }}<strong>{{ previewText(preview.notionalAtoms, 'USDT') }}</strong></span>
        <span>{{ tr('trade.entry.estimatedHeld') }}<strong>{{ previewText(preview.heldAtoms, preview.heldAsset) }}</strong></span>
        <span>{{ tr('trade.entry.estimatedFee') }}<strong>{{ tr('trade.entry.feeRange', { maker: previewText(preview.makerFeeAtoms, preview.feeAsset), taker: previewText(preview.takerFeeAtoms, preview.feeAsset) }) }}</strong></span>
        <span>{{ tr('trade.entry.estimatedReceive') }}<strong>{{ previewText(preview.receiveTakerAtoms, preview.feeAsset) }}</strong></span>
      </div>
      <ul v-if="preview.errorKeys.length" class="validation-list"><li v-for="key in preview.errorKeys" :key="key">{{ tr(key) }}</li></ul>
      <button class="submit-order" :class="`submit-order--${form.side}`" :disabled="!writesEnabled">
        {{ pending ? tr('trade.entry.confirming') : !loggedIn ? tr('trade.entry.loginToTrade') : !preview.valid ? tr('trade.entry.fixValidation') : form.side === 'buy' ? tr('trade.entry.submitBuy') : tr('trade.entry.submitSell') }}
      </button>
    </form>
  </article>
</template>

<style scoped>
.entry-card { overflow: hidden; }
header { min-height: 64px; padding: 14px 18px; border-bottom: 1px solid var(--border); display: flex; align-items: center; justify-content: space-between; gap: 12px; }
header span { color: var(--text-3); font-size: 11px; font-weight: 600; text-transform: uppercase; } header h2 { margin-top: 3px; font-size: 16px; }
.entry-body { display: grid; gap: 12px; padding: 16px 18px 18px; }
.entry-body label { display: grid; gap: 6px; color: var(--text-2); font-size: 11px; font-weight: 600; }
.switch { display: flex; gap: 2px; padding: 3px; border: 1px solid var(--border); border-radius: 10px; background: var(--bg-panel-2); }.switch button { flex: 1; min-height: 44px; border: 0; border-radius: 7px; padding: 7px; background: transparent; cursor: pointer; }.switch button.active { color: var(--accent); background: var(--bg-panel); box-shadow: 0 1px 5px rgba(0,36,77,.12); }
.input-pair { display: grid; grid-template-columns: 1fr auto; gap: 7px; }
.percent-row { display: flex; justify-content: space-between; align-items: center; gap: 8px; color: var(--text-3); font-size: 10px; }.percent-row div { display: flex; gap: 4px; }.percent-row button { min-width: 44px; min-height: 44px; border: 1px solid var(--border); border-radius: 7px; padding: 4px 6px; color: var(--accent); background: var(--bg-panel); cursor: pointer; }
.tif-row { display: grid; grid-template-columns: 1fr auto; align-items: end; gap: 12px; }.checkbox { display: flex !important; min-height: 44px; align-items: center; padding-bottom: 0; }.checkbox input { accent-color: var(--accent); }
.hint { margin: 0; color: var(--text-3); font-size: 10px; line-height: 1.45; }
.preview-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 7px; }.preview-grid span { display: grid; gap: 4px; padding: 8px; border-radius: 8px; color: var(--text-3); background: var(--bg-panel-2); font-size: 9px; }.preview-grid strong { color: var(--text-1); font: 10px var(--font-mono); overflow-wrap: anywhere; }
.validation-list { display: grid; gap: 3px; margin: 0; padding-left: 18px; color: var(--down); font-size: 10px; }
.submit-order { min-height: 44px; border: 0; border-radius: var(--radius-sm); color: #fff; font-weight: 600; cursor: pointer; }.submit-order--buy { background: var(--up); }.submit-order--sell { background: var(--down); }.submit-order:disabled { opacity: .45; cursor: not-allowed; }
@media(max-width:450px){.percent-row{align-items:flex-start;flex-direction:column}.preview-grid{grid-template-columns:1fr}}
</style>
