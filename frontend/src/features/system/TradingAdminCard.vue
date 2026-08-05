<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  TradingRequestError,
  tradingAPI,
  type Principal,
} from '../../api/trading'
import { useI18n } from '../../i18n'
import {
  LEGACY_PENDING_TRADING_WRITE_STORAGE_KEY,
  PENDING_TRADING_WRITE_STORAGE_KEY,
  pendingTradingWriteMutationAllowed,
  readLocalPendingTradingWrite,
  readPersistedPendingTradingWrite,
  updatePendingTradingWriteState,
  type PendingTradingWrite,
} from '../../trading/pending-write'
import type { RecoveryAdmission } from '../../trading/recovery-admission'
import { parseDecimal } from '../trade/decimal'

const props = defineProps<{
  admission: RecoveryAdmission | null
}>()

const { tr } = useI18n()

const principal = ref<Principal | null>(null)
const sessionResolved = ref(false)
const busy = ref(false)
const pending = ref<PendingTradingWrite | null>(null)
const journalBlocked = ref(false)
const notice = ref('')
const error = ref('')
const form = reactive({
  accountID: '',
  asset: 'USDT' as 'BTC' | 'USDT',
  amount: '',
})

const visible = computed(() => sessionResolved.value && principal.value?.admin === true)
const foreignPending = computed(() => pending.value !== null && pending.value.operation !== 'fund')
const accountMismatch = computed(() => pending.value !== null &&
  pending.value.account_id !== principal.value?.account_id)
const writesAllowed = computed(() => props.admission?.writesAllowed === true)
const submitAllowed = computed(() => visible.value && writesAllowed.value &&
  !busy.value && !journalBlocked.value && pending.value === null &&
  validAmount(form.amount, form.asset))
const replayAllowed = computed(() => visible.value && writesAllowed.value &&
  !busy.value && !journalBlocked.value && pending.value?.operation === 'fund' &&
  pending.value.state === 'unknown' && !accountMismatch.value)

function validAmount(value: string, asset: 'BTC' | 'USDT'): boolean {
  const atoms = parseDecimal(value, asset === 'BTC' ? 8 : 6)
  return atoms !== null && atoms > 0n
}

function pendingStateLabel(value: PendingTradingWrite['state']): string {
  if (value === 'submitted') return tr('system.trading.pending.state.submitted')
  if (value === 'reconciling') return tr('system.trading.pending.state.reconciling')
  return tr('system.trading.pending.state.unknown')
}

function fundingFailure(failure: unknown): string {
  if (failure instanceof TradingRequestError) {
    if (failure.code === 'idempotency_conflict') {
      return tr('system.trading.error.idempotencyConflict')
    }
    if (failure.code === 'authentication_required' || failure.status === 401) {
      return tr('system.trading.error.sessionExpired')
    }
    if (failure.code === 'trading_write_paused' || failure.code === 'recovery_in_progress') {
      return tr('system.trading.error.recoveryPaused')
    }
  }
  return tr('system.trading.error.generic')
}

function applyPendingToForm(current: PendingTradingWrite | null): void {
  if (current?.operation !== 'fund') return
  form.accountID = String(current.payload.account_id ?? '')
  form.asset = current.payload.asset === 'BTC' ? 'BTC' : 'USDT'
  form.amount = String(current.payload.amount ?? '')
}

function mirrorAuthoritative(current: PendingTradingWrite | null): void {
  pending.value = current
  journalBlocked.value = false
  applyPendingToForm(current)
}

function readAuthoritative(): PendingTradingWrite | null | undefined {
  try {
    const current = readLocalPendingTradingWrite(window.localStorage)
    mirrorAuthoritative(current)
    return current
  } catch {
    journalBlocked.value = true
    error.value = tr('system.trading.error.journalRead')
    return undefined
  }
}

function persistPendingUnchecked(next: PendingTradingWrite | null): boolean {
  try {
    if (next) {
      window.localStorage.setItem(PENDING_TRADING_WRITE_STORAGE_KEY, JSON.stringify(next))
    } else {
      window.localStorage.removeItem(PENDING_TRADING_WRITE_STORAGE_KEY)
    }
    window.localStorage.removeItem(LEGACY_PENDING_TRADING_WRITE_STORAGE_KEY)
    window.sessionStorage.removeItem(PENDING_TRADING_WRITE_STORAGE_KEY)
    window.sessionStorage.removeItem(LEGACY_PENDING_TRADING_WRITE_STORAGE_KEY)
    mirrorAuthoritative(next)
    return true
  } catch {
    error.value = tr('system.trading.error.persist')
    return false
  }
}

function storePending(
  next: PendingTradingWrite | null,
  expectedOperationID: string | null,
): boolean {
  const authoritative = readAuthoritative()
  if (authoritative === undefined) return false
  if (!pendingTradingWriteMutationAllowed(authoritative, next, expectedOperationID)) {
    error.value = tr('system.trading.error.persist')
    return false
  }
  return persistPendingUnchecked(next)
}

function transitionPending(
  current: PendingTradingWrite,
  state: PendingTradingWrite['state'],
): boolean {
  const authoritative = readAuthoritative()
  if (
    authoritative === undefined ||
    authoritative === null ||
    authoritative.operation_id !== current.operation_id
  ) {
    return false
  }
  return storePending(
    updatePendingTradingWriteState(authoritative, state, Date.now()),
    current.operation_id,
  )
}

function clearPending(current: PendingTradingWrite): boolean {
  return storePending(null, current.operation_id)
}

function restorePending(): void {
  try {
    const restored = readPersistedPendingTradingWrite(
      window.localStorage,
      window.sessionStorage,
    )
    if (!restored) {
      mirrorAuthoritative(null)
      return
    }
    const uncertain = updatePendingTradingWriteState(restored, 'unknown', Date.now())
    const shared = readLocalPendingTradingWrite(window.localStorage)
    if (shared) {
      if (!storePending(uncertain, restored.operation_id)) return
    } else {
      if (!storePending(uncertain, null)) return
    }
  } catch {
    journalBlocked.value = true
    error.value = tr('system.trading.error.journalRead')
  }
}

function mirrorPendingStorage(): void {
  try {
    mirrorAuthoritative(readPersistedPendingTradingWrite(
      window.localStorage,
      window.sessionStorage,
    ))
  } catch {
    // A malformed non-empty shared journal is still a lock. Do not mutate it
    // or discard the last known operation from another tab.
    journalBlocked.value = true
    error.value = tr('system.trading.error.journalRead')
  }
}

function fundingPayload(current: PendingTradingWrite): {
  accountID: string
  asset: string
  amount: string
} {
  return {
    accountID: String(current.payload.account_id ?? ''),
    asset: String(current.payload.asset ?? ''),
    amount: String(current.payload.amount ?? ''),
  }
}

async function submitFunding(): Promise<void> {
  if (!submitAllowed.value || !principal.value) return
  notice.value = ''
  error.value = ''
  const at = Date.now()
  const requestID = `fund-${crypto.randomUUID()}`
  const targetAccount = form.accountID.trim() || principal.value.account_id
  const operation: PendingTradingWrite = {
    operation_id: `operation-${crypto.randomUUID()}`,
    operation: 'fund',
    account_id: principal.value.account_id,
    request_id: requestID,
    state: 'submitted',
    created_at: at,
    updated_at: at,
    payload: {
      account_id: targetAccount,
      asset: form.asset,
      amount: form.amount,
    },
  }
  if (!storePending(operation, null)) return

  busy.value = true
  try {
    await tradingAPI.fund(requestID, form.asset, form.amount, targetAccount)
    if (!clearPending(operation)) return
    notice.value = tr('system.trading.notice.completed', { requestID })
    form.amount = ''
  } catch (failure) {
    if (failure instanceof TradingRequestError && failure.uncertain) {
      if (transitionPending(operation, 'unknown')) {
        notice.value = tr('system.trading.notice.unknown', { requestID })
      }
    } else {
      clearPending(operation)
      error.value = fundingFailure(failure)
    }
  } finally {
    busy.value = false
  }
}

async function reconcileFunding(): Promise<void> {
  const current = pending.value
  if (!current || current.operation !== 'fund' || !replayAllowed.value) return
  notice.value = ''
  error.value = ''
  if (!transitionPending(current, 'reconciling')) return
  const authoritative = pending.value
  if (
    !authoritative ||
    authoritative.operation !== 'fund' ||
    authoritative.operation_id !== current.operation_id
  ) return
  const payload = fundingPayload(authoritative)

  busy.value = true
  try {
    await tradingAPI.fund(
      authoritative.request_id,
      payload.asset,
      payload.amount,
      payload.accountID,
    )
    if (!clearPending(authoritative)) return
    notice.value = tr('system.trading.notice.reconciled', {
      requestID: authoritative.request_id,
    })
    form.amount = ''
  } catch (failure) {
    transitionPending(authoritative, 'unknown')
    error.value = fundingFailure(failure)
  } finally {
    busy.value = false
  }
}

const handleStorage = () => mirrorPendingStorage()

onMounted(async () => {
  window.addEventListener('storage', handleStorage)
  restorePending()
  try {
    principal.value = (await tradingAPI.session()).principal
    if (!form.accountID && principal.value.admin) form.accountID = principal.value.account_id
  } catch {
    principal.value = null
  } finally {
    sessionResolved.value = true
  }
})
onBeforeUnmount(() => window.removeEventListener('storage', handleStorage))
</script>

<template>
  <section v-if="visible" class="trading-admin" data-testid="system-trading-admin">
    <div class="section-heading">
      <div>
        <h2>{{ tr('system.trading.title') }}</h2>
        <p>{{ tr('system.trading.description') }}</p>
      </div>
      <span class="admin-label">{{ tr('system.trading.adminLabel') }}</span>
    </div>

    <div class="admin-card card">
      <div v-if="foreignPending" class="pending-banner pending-banner--warning">
        <strong>{{ tr('system.trading.pending.foreignTitle') }}</strong>
        <span>{{ tr('system.trading.pending.foreignBody') }}</span>
        <a href="/trade/BTC-USDT">{{ tr('system.trading.pending.openTrade') }}</a>
      </div>

      <div v-else-if="pending" class="pending-banner" data-testid="funding-pending">
        <strong>{{ tr('system.trading.pending.title') }}</strong>
        <span class="mono">{{ pending.request_id }} · {{ pendingStateLabel(pending.state) }}</span>
        <span v-if="accountMismatch">
          {{ tr('system.trading.pending.accountMismatch') }}
        </span>
        <button
          v-else
          type="button"
          :disabled="!replayAllowed"
          data-testid="funding-reconcile"
          @click="reconcileFunding"
        >
          {{ busy ? tr('system.trading.pending.reconciling') : tr('system.trading.pending.reconcile') }}
        </button>
      </div>

      <form class="funding-form" @submit.prevent="submitFunding">
        <label>
          <span>{{ tr('system.trading.field.account') }}</span>
          <input
            v-model="form.accountID"
            name="account_id"
            autocomplete="off"
            :disabled="busy || pending !== null"
          >
        </label>
        <label>
          <span>{{ tr('system.trading.field.asset') }}</span>
          <select v-model="form.asset" name="asset" :disabled="busy || pending !== null">
            <option value="USDT">USDT</option>
            <option value="BTC">BTC</option>
          </select>
        </label>
        <label>
          <span>{{ tr('system.trading.field.amount') }}</span>
          <input
            v-model="form.amount"
            name="amount"
            inputmode="decimal"
            autocomplete="off"
            placeholder="0.00"
            :disabled="busy || pending !== null"
          >
        </label>
        <button type="submit" :disabled="!submitAllowed" data-testid="funding-submit">
          {{ busy ? tr('system.trading.action.submitting') : tr('system.trading.action.fund') }}
        </button>
      </form>

      <p v-if="!writesAllowed" class="gate-message" data-testid="funding-write-gate">
        {{ tr('system.trading.gate.blocked') }}
      </p>
      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
    </div>
  </section>
</template>

<style scoped>
.trading-admin {
  margin-top: 24px;
}

.section-heading {
  display: flex;
  align-items: end;
  justify-content: space-between;
  margin: 4px 0 12px;
}

.section-heading h2 {
  margin: 0;
  font-size: 15px;
}

.section-heading p {
  margin: 3px 0 0;
  color: var(--text-3);
  font-size: 12px;
}

.admin-label {
  color: var(--accent);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: .08em;
}

.admin-card {
  padding: 18px;
}

.funding-form {
  display: grid;
  grid-template-columns: minmax(220px, 1.5fr) minmax(100px, .5fr) minmax(150px, .75fr) auto;
  align-items: end;
  gap: 12px;
}

.funding-form label {
  display: grid;
  gap: 6px;
  color: var(--text-3);
  font-size: 11px;
}

.funding-form input,
.funding-form select {
  min-width: 0;
  height: 38px;
  padding: 0 11px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-1);
  background: var(--surface-1);
}

.funding-form button,
.pending-banner button {
  min-height: 38px;
  padding: 0 14px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-1);
  background: var(--surface-2);
  cursor: pointer;
}

button:disabled,
input:disabled,
select:disabled {
  cursor: not-allowed;
  opacity: .55;
}

.pending-banner {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 10px;
  margin-bottom: 14px;
  padding: 12px;
  border: 1px solid #dca900;
  border-radius: 8px;
  color: #805700;
  background: #fff8df;
  font-size: 11px;
}

.pending-banner--warning {
  grid-template-columns: minmax(0, 1fr) minmax(0, 2fr) auto;
}

.pending-banner a {
  color: inherit;
  font-weight: 600;
}

.gate-message,
.notice,
.error {
  margin: 12px 0 0;
  font-size: 11px;
}

.gate-message {
  color: var(--text-3);
}

.notice {
  color: #287a55;
}

.error {
  color: #be2d42;
}

@media (max-width: 900px) {
  .funding-form {
    grid-template-columns: 1fr 1fr;
  }
}

@media (max-width: 560px) {
  .section-heading,
  .pending-banner,
  .pending-banner--warning,
  .funding-form {
    grid-template-columns: 1fr;
  }

  .section-heading {
    display: grid;
    gap: 8px;
  }
}
</style>
