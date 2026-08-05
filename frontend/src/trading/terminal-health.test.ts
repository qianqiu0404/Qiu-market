import { describe, expect, it } from 'vitest'
import { completePanelRead, createPanelReadState, failPanelRead } from './panel-state'
import { deriveTerminalHealth, type TerminalHealthInput } from './terminal-health'

function healthyInput(
  overrides: Partial<TerminalHealthInput> = {},
): TerminalHealthInput {
  return {
    now: 10_000,
    loggedIn: true,
    matchingStatus: 'ready',
    statusRead: completePanelRead(createPanelReadState(), 9_000),
    orderbookRead: completePanelRead(createPanelReadState(), 9_000),
    hasBids: true,
    hasAsks: true,
    eventTransport: 'live',
    privateDataAt: 9_000,
    reconcileComplete: true,
    ...overrides,
  }
}

describe('trade terminal health', () => {
  it('reports every explicit state for a healthy websocket terminal', () => {
    expect(deriveTerminalHealth(healthyInput())).toEqual({
      matchingState: 'ready',
      liquidityState: 'active',
      transportState: 'websocket',
      dataAgeSeconds: 1,
      availability: 'LIVE',
      writesAllowed: true,
      writeBlockReason: '',
    })
  })

  it('degrades and disables writes when matching state is older than ten seconds', () => {
    const health = deriveTerminalHealth(healthyInput({ now: 20_001 }))

    expect(health.matchingState).toBe('stale')
    expect(health.dataAgeSeconds).toBe(11)
    expect(health.availability).toBe('DEGRADED')
    expect(health.writesAllowed).toBe(false)
    expect(health.writeBlockReason).toBe('matching_status_stale')
  })

  it('keeps anonymous fresh polling online without granting writes', () => {
    const health = deriveTerminalHealth(healthyInput({
      loggedIn: false,
      eventTransport: 'polling',
      privateDataAt: 0,
    }))

    expect(health.transportState).toBe('polling')
    expect(health.availability).toBe('LIVE')
    expect(health.writesAllowed).toBe(false)
    expect(health.writeBlockReason).toBe('login_required')
  })

  it('blocks every write while an unknown operation is not reconciled', () => {
    const health = deriveTerminalHealth(healthyInput({
      reconcileComplete: false,
    }))

    expect(health.availability).toBe('DEGRADED')
    expect(health.writesAllowed).toBe(false)
    expect(health.writeBlockReason).toBe('reconcile_pending')
  })

  it('distinguishes last-good degradation from a cold offline terminal', () => {
    const degraded = deriveTerminalHealth(healthyInput({
      orderbookRead: failPanelRead(
        completePanelRead(createPanelReadState(), 9_000),
        'timeout',
        9_500,
      ),
    }))
    const offline = deriveTerminalHealth(healthyInput({
      statusRead: createPanelReadState(),
      orderbookRead: createPanelReadState(),
      eventTransport: 'offline',
    }))

    expect(degraded.availability).toBe('DEGRADED')
    expect(degraded.liquidityState).toBe('stale')
    expect(offline.availability).toBe('OFFLINE')
  })
})
