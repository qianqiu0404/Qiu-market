import { describe, expect, it } from 'vitest'
import {
  beginPanelRead,
  completePanelRead,
  createPanelReadState,
  failPanelRead,
  panelReadAgeSeconds,
  panelReadAvailability,
} from './panel-state'

describe('trade panel read state', () => {
  it('keeps the last successful timestamp when a later read fails', () => {
    const loading = beginPanelRead(createPanelReadState(), 1_000)
    const ready = completePanelRead(loading, 1_200)
    const failed = failPanelRead(ready, 'order book timed out', 2_000)

    expect(failed).toEqual({
      lastAttemptAt: 2_000,
      lastSuccessAt: 1_200,
      error: 'order book timed out',
    })
    expect(panelReadAvailability(failed)).toBe('last-good')
    expect(panelReadAgeSeconds(failed, 4_299)).toBe(3)
  })

  it('distinguishes a cold failure from a last-good degradation', () => {
    const coldFailure = failPanelRead(
      createPanelReadState(),
      'balances unavailable',
      3_000,
    )

    expect(panelReadAvailability(coldFailure)).toBe('unavailable')
    expect(panelReadAgeSeconds(coldFailure, 4_000)).toBe(-1)
    expect(panelReadAvailability(createPanelReadState())).toBe('loading')
  })

  it('clears only the panel error after that panel recovers', () => {
    const failed = failPanelRead(
      completePanelRead(createPanelReadState(), 1_000),
      'temporary failure',
      2_000,
    )
    const recovered = completePanelRead(failed, 3_000)

    expect(recovered.error).toBe('')
    expect(recovered.lastSuccessAt).toBe(3_000)
    expect(panelReadAvailability(recovered)).toBe('current')
  })
})
