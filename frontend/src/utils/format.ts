/** Shared formatting + freshness helpers. Pure functions, fully typed. */

export type Freshness = 'live' | 'delayed' | 'stale' | 'offline'

const NA = '—'

/** 1.2K / 3.4M / 5.6B / 7.8T style abbreviation. */
export function formatAbbr(value: number | null | undefined, prefix = ''): string {
  if (value == null || !Number.isFinite(value)) return NA
  const abs = Math.abs(value)
  const units: Array<[number, string]> = [
    [1e12, 'T'],
    [1e9, 'B'],
    [1e6, 'M'],
    [1e3, 'K'],
  ]
  for (const [div, suffix] of units) {
    if (abs >= div) {
      const n = value / div
      const text = n.toFixed(1).replace(/\.0$/, '')
      return `${prefix}${text}${suffix}`
    }
  }
  return `${prefix}${formatPrice(value)}`
}

/** Adaptive-decimal price formatting with thousands separators. */
export function formatPrice(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return NA
  const abs = Math.abs(value)
  const maxDecimals = abs >= 100 ? 2 : abs >= 1 ? 4 : abs >= 0.01 ? 6 : 8
  return value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: maxDecimals,
  })
}

/** Percent with explicit sign, e.g. "+1.23%" / "-0.45%". */
export function formatPercent(value: number | null | undefined, decimals = 2): string {
  if (value == null || !Number.isFinite(value)) return NA
  const threshold = 0.5 * 10 ** -decimals
  const normalized = Math.abs(value) < threshold ? 0 : value
  const sign = normalized > 0 ? '+' : ''
  return `${sign}${normalized.toFixed(decimals)}%`
}

/** Compact delay label: "3s" / "2m" / "1h 5m". */
export function formatDelay(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return NA
  if (seconds < 1) return '<1s'
  if (seconds < 60) return `${Math.round(seconds)}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return m > 0 ? `${h}h ${m}m` : `${h}h`
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function toDate(input: string | number | Date): Date | null {
  const d = input instanceof Date ? input : new Date(input)
  return Number.isNaN(d.getTime()) ? null : d
}

/** HH:mm:ss — used by the live clock. */
export function formatClock(input: string | number | Date): string {
  const d = toDate(input)
  if (!d) return NA
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** Human timestamp: "HH:mm:ss" today, otherwise "MMM d, HH:mm:ss". */
export function formatTime(input: string | number | Date | null | undefined): string {
  if (input == null) return NA
  const d = toDate(input)
  if (!d) return NA
  const clock = formatClock(d)
  const now = new Date()
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate()
  if (sameDay) return clock
  const month = d.toLocaleString('en-US', { month: 'short' })
  return `${month} ${d.getDate()}, ${clock}`
}

/** Freshness thresholds: <15s live, <60s delayed, otherwise stale; failed fetch → offline. */
export function freshnessFromDelay(
  delaySeconds: number | null | undefined,
  failed = false,
): Freshness {
  if (failed) return 'offline'
  if (delaySeconds == null || !Number.isFinite(delaySeconds)) return 'offline'
  if (delaySeconds < 15) return 'live'
  if (delaySeconds < 60) return 'delayed'
  return 'stale'
}

/** Kline interval string ('1m' | '15m' | '1h' | '1d') in milliseconds. */
export const KLINE_INTERVAL_MS: Record<string, number> = {
  '1m': 60_000,
  '15m': 15 * 60_000,
  '1h': 3_600_000,
  '1d': 86_400_000,
}

/**
 * Kline-aware freshness: the last candle's OPEN time lags wall clock by up to
 * one full interval even when the pipeline is healthy (the in-progress candle
 * keeps that open timestamp until it closes). So judge by interval, not by a
 * flat 60s threshold: within 2 intervals → live, 3 → delayed, beyond → stale.
 */
export function klineFreshness(
  lastOpenMs: number | null | undefined,
  interval: string,
  failed = false,
): Freshness {
  if (failed) return 'offline'
  if (lastOpenMs == null || !Number.isFinite(lastOpenMs)) return 'offline'
  const intervalMs = KLINE_INTERVAL_MS[interval] ?? 60_000
  const lag = Date.now() - lastOpenMs
  if (lag <= 2 * intervalMs) return 'live'
  if (lag <= 3 * intervalMs) return 'delayed'
  return 'stale'
}

/** True when the candle opened at `openMs` is still in progress for `interval`. */
export function isInProgressCandle(openMs: number, interval: string): boolean {
  const intervalMs = KLINE_INTERVAL_MS[interval] ?? 60_000
  return openMs + intervalMs > Date.now()
}

export const FRESHNESS_LABELS: Record<Freshness, string> = {
  live: 'Live',
  delayed: 'Delayed',
  stale: 'Stale',
  offline: 'Offline',
}

/** Loose check for "healthy" status strings coming from the backend. */
export function isHealthyStatus(status: string | null | undefined): boolean {
  if (!status) return false
  return ['ok', 'running', 'healthy', 'up', 'connected', 'online', 'active', 'normal', 'ready'].includes(
    status.trim().toLowerCase(),
  )
}

export function providerFreshnessVariant(status: string | null | undefined): Freshness {
  switch (status?.trim().toLowerCase()) {
  case 'healthy':
    return 'live'
  case 'stale':
    return 'stale'
  default:
    return 'offline'
  }
}

/** Truncate long ids keeping both ends: "abcd…wxyz". */
export function truncateMiddle(text: string, head = 8, tail = 6): string {
  if (text.length <= head + tail + 1) return text
  return `${text.slice(0, head)}…${text.slice(-tail)}`
}
