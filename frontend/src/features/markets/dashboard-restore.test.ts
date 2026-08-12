import { describe, expect, it, vi } from 'vitest'
import { applyLateDashboardRestore } from './dashboard-restore'
import { isCurrentDashboardRequest } from './dashboard-cache'

describe('parallel persisted dashboard restore', () => {
  it('keeps refreshing only while the network remains in flight', () => {
    expect(applyLateDashboardRestore(false, '')).toEqual({
      loading: false, refreshing: true, failure: '', refreshError: '',
    })
  })

  it('turns a settled network failure into refresh error without a stuck spinner', () => {
    expect(applyLateDashboardRestore(true, 'backend timeout')).toEqual({
      loading: false, refreshing: false, failure: '', refreshError: 'backend timeout',
    })
  })

  it('restores within 200ms in parallel and rejects a late old-generation network result', async () => {
    vi.useFakeTimers()
    let visible = 'loading'
    let generation = 4
    const persisted = new Promise<string>((resolve) => {
      setTimeout(() => resolve('persisted-last-good'), 150)
    }).then((value) => {
      if (isCurrentDashboardRequest(4, generation, 'all', 'all')) visible = value
      return applyLateDashboardRestore(false, '')
    })
    await vi.advanceTimersByTimeAsync(150)
    expect(await persisted).toMatchObject({ refreshing: true })
    expect(visible).toBe('persisted-last-good')
    generation = 5
    if (isCurrentDashboardRequest(4, generation, 'all', 'all')) visible = 'late-network'
    expect(visible).toBe('persisted-last-good')
    vi.useRealTimers()
  })

  it('shows late persisted data plus refresh error after a fast network failure', async () => {
    let networkSettled = false
    let networkError = ''
    const failure = Promise.resolve().then(() => {
      networkSettled = true
      networkError = 'backend timeout'
    })
    await failure
    await expect(Promise.resolve('persisted').then(() =>
      applyLateDashboardRestore(networkSettled, networkError))).resolves.toEqual({
      loading: false, refreshing: false, failure: '', refreshError: 'backend timeout',
    })
  })
})
