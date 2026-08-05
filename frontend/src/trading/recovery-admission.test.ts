import { describe, expect, it } from 'vitest'
import { recoveryNotEnabled, type TradingRecoveryStatus } from '../api/trading'
import {
  deriveRecoveryAdmission,
  recoveryStatusRegression,
} from './recovery-admission'

function recoveryStatus(
  overrides: Partial<TradingRecoveryStatus> = {},
): TradingRecoveryStatus {
  return {
    ...recoveryNotEnabled(),
    supported: true,
    schema_version: 2,
    market_id: 'BTC-USDT',
    epoch_id: '0123456789abcdef0123456789abcdef',
    phase: 'transport_warmup',
    proof: {
      runtime_sequence: '42',
      state_hash: 'a'.repeat(64),
      ledger_balanced: true,
      event_continuous: true,
      projection_caught_up: true,
      outbox_caught_up: true,
      transport_healthy: false,
    },
    provenance: {
      production_origin: 'https://qiu-market.vercel.app',
      deployment_id: 'dpl_PreviewFixture123',
      deployment_url: 'https://qiu-market-preview-fixture.vercel.app',
      release_commit: 'd'.repeat(40),
      source_digest: 'e'.repeat(64),
    },
    ...overrides,
  }
}

describe('recovery admission evidence', () => {
  it('keeps explicit 404 compatibility outside the new recovery gate', () => {
    expect(deriveRecoveryAdmission(recoveryNotEnabled(), '')).toMatchObject({
      mode: 'not_enabled',
      writesAllowed: true,
      reason: 'recovery_gate_not_enabled',
    })
  })

  it('blocks writes until the authoritative phase is writable', () => {
    expect(deriveRecoveryAdmission(recoveryStatus(), '')).toMatchObject({
      mode: 'blocked',
      writesAllowed: false,
      reason: 'recovery_transport_warmup',
      completedProofs: 5,
      totalProofs: 6,
    })
  })

  it('allows the UI mirror only when phase and writes flag both agree', () => {
    const status = recoveryStatus({
      phase: 'writable',
      writes_enabled: true,
      proof: {
        ...recoveryStatus().proof,
        transport_healthy: true,
      },
    })
    expect(deriveRecoveryAdmission(status, '')).toMatchObject({
      mode: 'writable',
      writesAllowed: true,
      completedProofs: 6,
    })
    expect(deriveRecoveryAdmission({ ...status, writes_enabled: false }, '').writesAllowed)
      .toBe(false)
  })

  it('rejects a contradictory writable flag when the public proof is incomplete', () => {
    const status = recoveryStatus({ phase: 'writable', writes_enabled: true })
    expect(deriveRecoveryAdmission(status, '')).toMatchObject({
      mode: 'blocked',
      writesAllowed: false,
      reason: 'recovery_proof_incomplete',
    })
  })

  it('describes a writable phase with disabled writes as a continuity block', () => {
    const status = recoveryStatus({
      phase: 'writable',
      writes_enabled: false,
      last_error: 'continuity probe failed',
      continuity_uncertain: true,
      continuity_error: 'store continuity is uncertain',
      proof: {
        ...recoveryStatus().proof,
        transport_healthy: false,
      },
    })
    expect(deriveRecoveryAdmission(status, '')).toMatchObject({
      mode: 'blocked',
      writesAllowed: false,
      reason: 'recovery_continuity_blocked',
    })
  })

  it('fails closed when recovery evidence cannot be read', () => {
    expect(deriveRecoveryAdmission(recoveryNotEnabled(), 'network error')).toMatchObject({
      mode: 'unavailable',
      writesAllowed: false,
      reason: 'recovery_status_unavailable',
    })
  })

  it('fails closed when the last successful observation is too old', () => {
    expect(deriveRecoveryAdmission(
      recoveryStatus({
        phase: 'writable',
        writes_enabled: true,
        proof: { ...recoveryStatus().proof, transport_healthy: true },
      }),
      '',
      { lastSuccessAt: 1_000, now: 12_001, maximumAgeMs: 10_000 },
    )).toMatchObject({
      mode: 'unavailable',
      writesAllowed: false,
      reason: 'recovery_status_unavailable',
    })
  })

  it('rejects same-epoch version rollback and conflicting equal versions', () => {
    const current = recoveryStatus({ version: '8' })
    expect(recoveryStatusRegression(current, { ...current, version: '7' }))
      .toBe('recovery_version_regressed')
    expect(recoveryStatusRegression(current, { ...current, phase: 'read_only' }))
      .toBe('recovery_version_conflict')
    expect(recoveryStatusRegression(current, {
      ...current,
      epoch_id: 'new-epoch',
      version: '9',
      phase: 'bootstrap',
    })).toBe('')
    expect(recoveryStatusRegression(current, {
      ...current,
      epoch_id: 'stale-old-epoch',
      version: '7',
      phase: 'writable',
    })).toBe('recovery_version_regressed')
    expect(recoveryStatusRegression(current, {
      ...current,
      epoch_id: 'conflicting-epoch',
      version: '8',
    })).toBe('recovery_version_conflict')
    expect(recoveryStatusRegression(current, recoveryNotEnabled()))
      .toBe('recovery_status_downgraded_to_legacy')
  })

  it('rejects same-epoch provenance changes even when version advances', () => {
    const current = recoveryStatus({ version: '8' })
    const candidate = recoveryStatus({
      version: '9',
      provenance: {
        ...current.provenance!,
        deployment_url: 'https://other-immutable-deployment.vercel.app',
      },
    })
    expect(recoveryStatusRegression(current, candidate))
      .toBe('recovery_provenance_conflict')
    expect(recoveryStatusRegression(current, {
      ...candidate,
      epoch_id: 'fedcba9876543210fedcba9876543210',
    })).toBe('')
  })
})
