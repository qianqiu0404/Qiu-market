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
 * document is hidden, cleans up on unmount. Overlapping requests are skipped.
 */
export function usePolling<T>(
  fetcher: () => Promise<T>,
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

  const refresh = async (): Promise<void> => {
    if (inFlight) return
    inFlight = true
    if (data.value === null) loading.value = true
    try {
      data.value = await fetcher()
      error.value = null
      lastUpdated.value = new Date()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Unknown error'
    } finally {
      loading.value = false
      inFlight = false
    }
  }

  const tick = (): void => {
    if (typeof document !== 'undefined' && document.hidden) return
    void refresh()
  }

  onMounted(() => {
    if (immediate) void refresh()
    timer = window.setInterval(tick, interval)
  })

  onUnmounted(() => {
    if (timer !== undefined) window.clearInterval(timer)
  })

  return { data, loading, error, lastUpdated, refresh }
}
