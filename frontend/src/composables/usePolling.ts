import { onMounted, onUnmounted, ref, type Ref } from 'vue'

export interface PollingOptions {
  /** Poll interval in ms. Defaults to 30s. */
  interval?: number
  /** Fetch immediately on mount. Defaults to true. */
  immediate?: boolean
}

export interface PollingResult<T> {
  data: Ref<T | null>
  /** True only during the initial load (subsequent polls stay silent). */
  loading: Ref<boolean>
  error: Ref<string | null>
  lastUpdated: Ref<Date | null>
  refresh: () => Promise<void>
}

/**
 * Generic polling composable: immediate fetch + setInterval, pauses while the
 * document is hidden, cleans up on unmount. Manual refreshes abort an older
 * request and only the newest result is allowed to update state.
 */
export function usePolling<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
  options?: PollingOptions,
): PollingResult<T>
export function usePolling<T>(
  fetcher: () => Promise<T>,
  options?: PollingOptions,
): PollingResult<T>
export function usePolling<T>(
  fetcher: ((signal: AbortSignal) => Promise<T>) | (() => Promise<T>),
  options: PollingOptions = {},
): PollingResult<T> {
  const interval = options.interval ?? 30_000
  const immediate = options.immediate ?? true

  const data = ref<T | null>(null) as Ref<T | null>
  const loading = ref(false)
  const error = ref<string | null>(null)
  const lastUpdated = ref<Date | null>(null)

  let timer: number | undefined
  let inFlight = false
  let rerunRequested = false
  let stopped = false
  let activeController: AbortController | null = null
  let activeGeneration = 0

  const run = async (queueIfBusy: boolean): Promise<void> => {
    if (stopped) return
    if (inFlight) {
      if (queueIfBusy) {
        rerunRequested = true
        activeController?.abort()
      }
      return
    }
    inFlight = true
    const controller = new AbortController()
    activeController = controller
    const generation = ++activeGeneration
    if (data.value === null) loading.value = true
    try {
      const next = await (fetcher as (signal: AbortSignal) => Promise<T>)(controller.signal)
      if (controller.signal.aborted || stopped || generation !== activeGeneration) return
      data.value = next
      error.value = null
      lastUpdated.value = new Date()
    } catch (e) {
      if (controller.signal.aborted) return
      error.value = e instanceof Error ? e.message : 'Unknown error'
    } finally {
      if (activeController === controller) activeController = null
      loading.value = false
      inFlight = false
      if (rerunRequested && !stopped) {
        rerunRequested = false
        void run(true)
      }
    }
  }

  const refresh = (): Promise<void> => run(true)

  const tick = (): void => {
    if (typeof document !== 'undefined' && document.hidden) return
    // Interval ticks are best-effort. Queueing them behind a slow request can
    // turn a degraded upstream into permanent back-to-back load.
    void run(false)
  }

  const handleVisibilityChange = (): void => {
    if (!document.hidden) void refresh()
  }

  onMounted(() => {
    if (immediate) void refresh()
    timer = window.setInterval(tick, interval)
    document.addEventListener('visibilitychange', handleVisibilityChange)
  })

  onUnmounted(() => {
    stopped = true
    rerunRequested = false
    activeController?.abort()
    activeGeneration += 1
    if (timer !== undefined) window.clearInterval(timer)
    document.removeEventListener('visibilitychange', handleVisibilityChange)
  })

  return { data, loading, error, lastUpdated, refresh }
}
