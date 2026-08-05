import { createApp, h } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { usePolling, type PollingResult } from './usePolling'

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('usePolling', () => {
  it('coalesces a refresh requested while the previous request is in flight', async () => {
    let finishFirst: ((value: string) => void) | undefined
    const fetcher = vi.fn()
      .mockImplementationOnce(() => new Promise<string>((resolve) => {
        finishFirst = resolve
      }))
      .mockResolvedValueOnce('new-query')
    let polling: PollingResult<string> | undefined
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      setup() {
        polling = usePolling(fetcher, {
          immediate: false,
          interval: 60_000,
        })
        return () => h('div')
      },
    })
    app.mount(host)

    const first = polling?.refresh()
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
    await polling?.refresh()
    finishFirst?.('old-query')
    await first

    await vi.waitFor(() => {
      expect(fetcher).toHaveBeenCalledTimes(2)
      expect(polling?.data.value).toBe('new-query')
    })
    app.unmount()
  })

  it('skips interval ticks while a slow request is in flight', async () => {
    vi.useFakeTimers()
    let finish: ((value: string) => void) | undefined
    const fetcher = vi.fn(() => new Promise<string>((resolve) => {
      finish = resolve
    }))
    let polling: PollingResult<string> | undefined
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      setup() {
        polling = usePolling(fetcher, {
          immediate: false,
          interval: 1_000,
        })
        return () => h('div')
      },
    })
    app.mount(host)

    const first = polling?.refresh()
    await Promise.resolve()
    expect(fetcher).toHaveBeenCalledOnce()
    await vi.advanceTimersByTimeAsync(3_000)
    expect(fetcher).toHaveBeenCalledOnce()

    finish?.('complete')
    await first
    await Promise.resolve()
    expect(fetcher).toHaveBeenCalledOnce()
    app.unmount()
  })

  it('refreshes immediately when a hidden document becomes visible', async () => {
    let hidden = true
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
    const fetcher = vi.fn().mockResolvedValue('visible')
    let polling: PollingResult<string> | undefined
    const host = document.createElement('div')
    document.body.append(host)
    const app = createApp({
      setup() {
        polling = usePolling(fetcher, { immediate: false, interval: 60_000 })
        return () => h('div')
      },
    })
    app.mount(host)

    document.dispatchEvent(new Event('visibilitychange'))
    expect(fetcher).not.toHaveBeenCalled()
    hidden = false
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalledOnce())
    expect(polling?.data.value).toBe('visible')
    app.unmount()
  })
})
