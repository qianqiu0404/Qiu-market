<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  TradingRequestError,
  tradingAPI,
  type AuthCapabilities,
  type FundingRequestResult,
  type Principal,
  type TradingStatus,
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
import {
  STARTER_FUNDING_STEPS,
  fundingRequestNotFound,
  validateStarterFundingResult,
  type StarterFundingStep,
} from './starter-funds'

const props = defineProps<{
  admission: RecoveryAdmission | null
}>()

const { tr } = useI18n()

const principal = ref<Principal | null>(null)
const capabilities = ref<AuthCapabilities>({
  github_oauth_enabled: false,
  local_login_enabled: false,
})
const practiceStatus = ref<TradingStatus | null>(null)
const sessionResolved = ref(false)
const busy = ref(false)
const pending = ref<PendingTradingWrite | null>(null)
const journalBlocked = ref(false)
const notice = ref('')
const error = ref('')
const starterResolved = ref(false)
const starterResults = reactive<Record<string, FundingRequestResult | null>>({
  'starter-v1-usdt': null,
  'starter-v1-btc': null,
})
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
const starterPending = computed(() => pending.value?.operation === 'fund' &&
  STARTER_FUNDING_STEPS.some((step) => step.requestID === pending.value?.request_id))
const starterComplete = computed(() =>
  STARTER_FUNDING_STEPS.every((step) => starterResults[step.requestID] !== null))
const starterAllowed = computed(() => visible.value &&
  capabilities.value.practice_mode_enabled === true &&
  capabilities.value.starter_funds_enabled === true && writesAllowed.value &&
  !starterComplete.value && !busy.value && !journalBlocked.value &&
  (pending.value === null || starterPending.value))
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

function starterResultLabel(step: StarterFundingStep): string {
  const result = starterResults[step.requestID]
  return result
    ? tr('system.trading.starter.applied', { sequence: result.sequence })
    : tr('system.trading.starter.pending')
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

function createFundingOperation(
  requestID: string,
  asset: 'BTC' | 'USDT',
  amount: string,
  targetAccount: string,
): PendingTradingWrite {
  const at = Date.now()
  return {
    operation_id: `operation-${crypto.randomUUID()}`,
    operation: 'fund',
    account_id: principal.value?.account_id ?? '',
    request_id: requestID,
    state: 'submitted',
    created_at: at,
    updated_at: at,
    payload: { account_id: targetAccount, asset, amount },
  }
}

async function queryStarter(step: StarterFundingStep): Promise<FundingRequestResult | null> {
  try {
    const result = validateStarterFundingResult(
      await tradingAPI.fundingRequest(step.requestID),
      step,
    )
    starterResults[step.requestID] = result
    return result
  } catch (failure) {
    if (fundingRequestNotFound(failure)) {
      starterResults[step.requestID] = null
      return null
    }
    throw failure
  }
}

async function refreshStarterTruth(): Promise<void> {
  if (!principal.value || capabilities.value.starter_funds_enabled !== true) {
    starterResolved.value = true
    return
  }
  try {
    for (const step of STARTER_FUNDING_STEPS) await queryStarter(step)
  } catch (failure) {
    error.value = fundingFailure(failure)
  } finally {
    starterResolved.value = true
  }
}

async function confirmStarter(
  step: StarterFundingStep,
  operation: PendingTradingWrite,
): Promise<boolean> {
  try {
    const result = await queryStarter(step)
    if (!result) return false
    if (!clearPending(operation)) throw new Error('starter journal could not be cleared')
    return true
  } catch (failure) {
    transitionPending(operation, 'unknown')
    throw failure
  }
}

async function fundStarterStep(step: StarterFundingStep): Promise<void> {
  if (!principal.value) throw new Error('starter session is unavailable')
  const existing = await queryStarter(step)
  const authoritativeBefore = readAuthoritative()
  if (authoritativeBefore === undefined) throw new Error('starter journal is unavailable')
  if (existing) {
    if (authoritativeBefore?.request_id === step.requestID) clearPending(authoritativeBefore)
    return
  }
  if (!writesAllowed.value) throw new Error('starter writes are blocked')

  let operation = authoritativeBefore
  if (operation) {
    if (
      operation.operation !== 'fund' || operation.request_id !== step.requestID ||
      operation.account_id !== principal.value.account_id ||
      operation.payload.account_id !== principal.value.account_id ||
      operation.payload.asset !== step.asset || operation.payload.amount !== step.amount
    ) {
      throw new Error('another trading write owns the shared journal')
    }
    if (!transitionPending(operation, 'reconciling')) {
      throw new Error('starter journal could not enter reconciliation')
    }
    operation = pending.value ?? operation
  } else {
    operation = createFundingOperation(
      step.requestID,
      step.asset,
      step.amount,
      principal.value.account_id,
    )
    if (!storePending(operation, null)) throw new Error('starter journal could not be persisted')
  }

  let firstUncertain = false
  try {
    await tradingAPI.fund(step.requestID, step.asset, step.amount, principal.value.account_id)
  } catch (failure) {
    if (!(failure instanceof TradingRequestError && failure.uncertain)) {
      clearPending(operation)
      throw failure
    }
    firstUncertain = true
    transitionPending(operation, 'unknown')
  }
  if (await confirmStarter(step, operation)) return

  // A success response can still race a missing read projection, and an unknown
  // response may already have committed. One exact-ID replay is safe only after
  // the authoritative query returned not-found and the write gate is still open.
  if (!writesAllowed.value || !transitionPending(operation, 'reconciling')) {
    throw new Error('starter funding remains unresolved')
  }
  try {
    await tradingAPI.fund(step.requestID, step.asset, step.amount, principal.value.account_id)
  } catch (failure) {
    transitionPending(operation, 'unknown')
    throw failure
  }
  if (!(await confirmStarter(step, operation))) {
    transitionPending(operation, 'unknown')
    throw new Error(firstUncertain
      ? 'starter funding remains unknown after exact-ID replay'
      : 'starter funding was not projected after exact-ID replay')
  }
}

async function ensureStarterFunds(): Promise<void> {
  if (!starterAllowed.value) return
  busy.value = true
  notice.value = ''
  error.value = ''
  try {
    for (const step of STARTER_FUNDING_STEPS) await fundStarterStep(step)
    notice.value = tr('system.trading.starter.completed')
  } catch (failure) {
    error.value = fundingFailure(failure)
  } finally {
    busy.value = false
    starterResolved.value = true
  }
}

async function submitFunding(): Promise<void> {
  if (!submitAllowed.value || !principal.value) return
  notice.value = ''
  error.value = ''
  const requestID = `fund-${crypto.randomUUID()}`
  const targetAccount = form.accountID.trim() || principal.value.account_id
  const operation = createFundingOperation(requestID, form.asset, form.amount, targetAccount)
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

function starterStepForPending(current: PendingTradingWrite): StarterFundingStep | null {
  const step = STARTER_FUNDING_STEPS.find((candidate) =>
    candidate.requestID === current.request_id)
  if (!step || !principal.value ||
    current.account_id !== principal.value.account_id ||
    current.payload.account_id !== principal.value.account_id ||
    current.payload.asset !== step.asset || current.payload.amount !== step.amount) {
    return null
  }
  return step
}

function isReservedStarterRequest(requestID: string): boolean {
  return STARTER_FUNDING_STEPS.some((step) => step.requestID === requestID)
}

async function reconcileStarterFunding(
  current: PendingTradingWrite,
  step: StarterFundingStep,
): Promise<void> {
  if (!principal.value) return
  notice.value = ''
  error.value = ''
  busy.value = true
  try {
    // This is the only safe first action after a response was lost: the
    // account-scoped event may already exist even though the browser journal
    // still says unknown.
    if (await queryStarter(step)) {
      if (!clearPending(current)) return
      notice.value = tr('system.trading.notice.reconciled', { requestID: step.requestID })
      return
    }
    if (!writesAllowed.value || !transitionPending(current, 'reconciling')) {
      throw new Error('starter funding remains unresolved')
    }
    const authoritative = pending.value
    if (!authoritative || authoritative.operation_id !== current.operation_id ||
      starterStepForPending(authoritative)?.requestID !== step.requestID) {
      throw new Error('starter funding journal identity changed')
    }
    // The authoritative GET returned not-found and all write gates are still
    // open, so one exact-ID replay is allowed. Never generate a replacement ID.
    await tradingAPI.fund(step.requestID, step.asset, step.amount, principal.value.account_id)
    if (!(await queryStarter(step))) {
      transitionPending(authoritative, 'unknown')
      throw new Error('starter funding was not projected after exact-ID replay')
    }
    if (!clearPending(authoritative)) return
    notice.value = tr('system.trading.notice.reconciled', { requestID: step.requestID })
  } catch (failure) {
    const authoritative = pending.value
    if (authoritative?.operation_id === current.operation_id) {
      transitionPending(authoritative, 'unknown')
    }
    error.value = fundingFailure(failure)
  } finally {
    busy.value = false
    starterResolved.value = true
  }
}

async function reconcileFunding(): Promise<void> {
  const current = pending.value
  if (!current || current.operation !== 'fund' || !replayAllowed.value) return
  if (isReservedStarterRequest(current.request_id)) {
    const step = starterStepForPending(current)
    if (!step) {
      error.value = fundingFailure(new Error('starter funding journal identity is invalid'))
      return
    }
    await reconcileStarterFunding(current, step)
    return
  }
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
  const [nextCapabilities, nextStatus] = await Promise.allSettled([
    tradingAPI.authCapabilities(),
    tradingAPI.status(),
  ])
  if (nextCapabilities.status === 'fulfilled') capabilities.value = nextCapabilities.value
  if (nextStatus.status === 'fulfilled') practiceStatus.value = nextStatus.value
  await refreshStarterTruth()
})
onBeforeUnmount(() => window.removeEventListener('storage', handleStorage))
</script>

<template>
  <section v-if="sessionResolved" class="trading-admin" data-testid="system-trading-practice">
    <div class="section-heading">
      <div>
        <h2>{{ tr('system.trading.title') }}</h2>
        <p>{{ tr('system.trading.description') }}</p>
      </div>
      <span v-if="visible" class="admin-label">{{ tr('system.trading.adminLabel') }}</span>
    </div>

    <div class="admin-card card">
      <div class="practice-facts" data-testid="practice-runtime-facts">
        <div>
          <span>{{ tr('system.trading.practice.mode') }}</span>
          <strong>{{ capabilities.practice_mode_enabled
            ? tr('system.trading.practice.enabled')
            : tr('system.trading.practice.disabled') }}</strong>
        </div>
        <div>
          <span>{{ tr('system.trading.practice.matching') }}</span>
          <strong>{{ practiceStatus?.state || tr('trade.status.unavailable') }}</strong>
        </div>
        <div>
          <span>{{ tr('system.trading.practice.liquidity') }}</span>
          <strong>{{ practiceStatus?.virtual_liquidity?.state || 'disabled' }}</strong>
          <small>{{ practiceStatus?.virtual_liquidity?.reason }}</small>
        </div>
        <div>
          <span>{{ tr('system.trading.practice.reference') }}</span>
          <strong>{{ practiceStatus?.virtual_liquidity?.reference_observed_at || tr('trade.status.noObservation') }}</strong>
        </div>
        <div>
          <span>{{ tr('system.trading.practice.transport') }}</span>
          <strong>{{ practiceStatus?.outbox_state || tr('trade.status.unavailable') }}</strong>
        </div>
        <div>
          <span>{{ tr('system.trading.practice.recovery') }}</span>
          <strong>{{ admission?.mode || tr('trade.status.unavailable') }}</strong>
        </div>
      </div>

      <div v-if="visible" class="admin-actions" data-testid="system-trading-admin">

      <section
        v-if="capabilities.practice_mode_enabled && capabilities.starter_funds_enabled"
        class="starter-card"
        data-testid="starter-funds"
      >
        <div>
          <h3>{{ tr('system.trading.starter.title') }}</h3>
          <p>{{ tr('system.trading.starter.description') }}</p>
        </div>
        <ul>
          <li v-for="step in STARTER_FUNDING_STEPS" :key="step.requestID">
            <span>{{ step.amount }} Virtual {{ step.asset }}</span>
            <strong :data-testid="`starter-${step.asset.toLowerCase()}-state`">
              {{ starterResultLabel(step) }}
            </strong>
          </li>
        </ul>
        <button
          type="button"
          :disabled="!starterAllowed || starterComplete"
          data-testid="starter-funds-submit"
          @click="ensureStarterFunds"
        >
          {{ busy
            ? tr('system.trading.starter.reconciling')
            : starterComplete
              ? tr('system.trading.starter.complete')
              : tr('system.trading.starter.action') }}
        </button>
        <small v-if="starterResolved">{{ tr('system.trading.starter.queryFirst') }}</small>
      </section>

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

      <details class="advanced-funding" open>
        <summary>{{ tr('system.trading.advanced.title') }}</summary>
        <p>{{ tr('system.trading.advanced.description') }}</p>
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
      </details>

      <p v-if="!writesAllowed" class="gate-message" data-testid="funding-write-gate">
        {{ tr('system.trading.gate.blocked') }}
      </p>
      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      </div>
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

.admin-actions { min-width: 0; }

.practice-facts {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 14px;
}

.practice-facts > div {
  display: grid;
  gap: 4px;
  min-width: 0;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface-2);
}

.practice-facts span,
.practice-facts small { color: var(--text-3); font-size: 10px; }
.practice-facts strong { font: 12px var(--font-mono); overflow-wrap: anywhere; }

.starter-card {
  display: grid;
  grid-template-columns: minmax(0, 1.3fr) minmax(240px, 1fr) auto;
  align-items: center;
  gap: 14px;
  margin-bottom: 14px;
  padding: 14px;
  border: 1px solid #a8c9ff;
  border-radius: 8px;
  background: #f2f7ff;
}

.starter-card h3 { margin: 0; font-size: 14px; }
.starter-card p,
.starter-card > small { margin: 4px 0 0; color: var(--text-3); font-size: 10px; }
.starter-card ul { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
.starter-card li { display: flex; justify-content: space-between; gap: 10px; font-size: 11px; }
.starter-card li strong { color: var(--accent); font: 10px var(--font-mono); }
.starter-card button { min-height: 44px; padding: 0 14px; border: 1px solid var(--accent); border-radius: 8px; color: white; background: var(--accent); }
.starter-card > small { grid-column: 1 / -1; }

.advanced-funding { border-top: 1px solid var(--border); padding-top: 12px; }
.advanced-funding summary { min-height: 44px; display: flex; align-items: center; cursor: pointer; color: var(--text-2); font-weight: 600; }
.advanced-funding > p { margin: 0 0 12px; color: var(--text-3); font-size: 11px; }

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
  height: 44px;
  padding: 0 11px;
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--text-1);
  background: var(--surface-1);
}

.funding-form button,
.pending-banner button {
  min-height: 44px;
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
  .practice-facts { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .starter-card { grid-template-columns: 1fr 1fr; }
  .starter-card > div,
  .starter-card > small { grid-column: 1 / -1; }
  .funding-form {
    grid-template-columns: 1fr 1fr;
  }
}

@media (max-width: 560px) {
  .practice-facts,
  .starter-card,
  .section-heading,
  .pending-banner,
  .pending-banner--warning,
  .funding-form {
    grid-template-columns: 1fr;
  }

  .starter-card > div,
  .starter-card > small { grid-column: auto; }

  .section-heading {
    display: grid;
    gap: 8px;
  }
}
</style>
