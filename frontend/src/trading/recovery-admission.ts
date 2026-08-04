import type { TradingRecoveryStatus } from '../api/trading'

export interface RecoveryAdmission {
  mode: 'not_enabled' | 'writable' | 'blocked' | 'unavailable'
  writesAllowed: boolean
  reason: string
  completedProofs: number
  totalProofs: number
}

export interface RecoveryEvidenceWindow {
  lastSuccessAt: number
  now: number
  maximumAgeMs: number
}

const PROOF_KEYS = [
  'ledger_balanced',
  'event_continuous',
  'projection_caught_up',
  'outbox_caught_up',
  'transport_healthy',
] as const

export function deriveRecoveryAdmission(
  status: TradingRecoveryStatus,
  readError: string,
  evidenceWindow?: RecoveryEvidenceWindow,
): RecoveryAdmission {
  const completedProofs = PROOF_KEYS.filter((key) => status.proof[key]).length +
    (status.proof.state_hash.length === 64 ? 1 : 0)
  const totalProofs = PROOF_KEYS.length + 1

  const stale = evidenceWindow !== undefined && (
    evidenceWindow.lastSuccessAt <= 0 ||
    evidenceWindow.now - evidenceWindow.lastSuccessAt > evidenceWindow.maximumAgeMs
  )
  if (readError || stale) {
    return {
      mode: 'unavailable',
      writesAllowed: false,
      reason: 'recovery_status_unavailable',
      completedProofs,
      totalProofs,
    }
  }
  if (!status.supported) {
    return {
      mode: 'not_enabled',
      writesAllowed: true,
      reason: 'recovery_gate_not_enabled',
      completedProofs,
      totalProofs,
    }
  }
  if (status.continuity_uncertain) {
    return {
      mode: 'blocked',
      writesAllowed: false,
      reason: 'recovery_continuity_blocked',
      completedProofs,
      totalProofs,
    }
  }
  if (status.phase === 'writable' && status.writes_enabled &&
      completedProofs === totalProofs) {
    return {
      mode: 'writable',
      writesAllowed: true,
      reason: 'recovery_writable',
      completedProofs,
      totalProofs,
    }
  }
  if (status.phase === 'writable' && status.writes_enabled) {
    return {
      mode: 'blocked',
      writesAllowed: false,
      reason: 'recovery_proof_incomplete',
      completedProofs,
      totalProofs,
    }
  }
  if (status.phase === 'writable') {
    return {
      mode: 'blocked',
      writesAllowed: false,
      reason: 'recovery_continuity_blocked',
      completedProofs,
      totalProofs,
    }
  }
  return {
    mode: 'blocked',
    writesAllowed: false,
    reason: `recovery_${status.phase}`,
    completedProofs,
    totalProofs,
  }
}

function recoveryFingerprint(status: TradingRecoveryStatus): string {
  return JSON.stringify({
    schema_version: status.schema_version,
    market_id: status.market_id,
    epoch_id: status.epoch_id,
    phase: status.phase,
    proof: status.proof,
    writes_enabled: status.writes_enabled,
    last_error: status.last_error,
    continuity_uncertain: status.continuity_uncertain,
    continuity_error: status.continuity_error,
    version: status.version,
    started_at: status.started_at,
    updated_at: status.updated_at,
  })
}

/**
 * Rejects evidence rollback before it replaces last-good UI state. Recovery
 * versions are decimal strings so the comparison never crosses JS number
 * precision. A supported coordinator may not silently downgrade to legacy.
 */
export function recoveryStatusRegression(
  previous: TradingRecoveryStatus,
  candidate: TradingRecoveryStatus,
): string {
  if (previous.supported && !candidate.supported) {
    return 'recovery_status_downgraded_to_legacy'
  }
  if (!previous.supported || !candidate.supported) return ''
  const previousVersion = BigInt(previous.version)
  const candidateVersion = BigInt(candidate.version)
  if (candidateVersion < previousVersion) return 'recovery_version_regressed'
  if (
    candidateVersion === previousVersion &&
    recoveryFingerprint(previous) !== recoveryFingerprint(candidate)
  ) {
    return 'recovery_version_conflict'
  }
  return ''
}
