import type { MarketVenue } from '../../api/market'

export const MARKET_VENUES: MarketVenue[] = [
  'all', 'binance', 'coinbase', 'bybit', 'okx', 'hyperliquid', 'uniswap',
  'pancakeswap',
]

export interface PrefetchEnvironment {
  hidden: boolean
  online: boolean
  saveData: boolean
  interacted: boolean
}

export function shouldContinueDashboardPrefetch(environment: PrefetchEnvironment): boolean {
  return !environment.hidden && environment.online &&
    !environment.saveData && !environment.interacted
}

export function otherMarketVenues(current: MarketVenue): MarketVenue[] {
  return MARKET_VENUES.filter((venue) => venue !== current)
}

export async function runSequentialDashboardPrefetch<T>(
  venues: MarketVenue[],
  options: {
    signal: AbortSignal
    shouldContinue: () => boolean
    load: (venue: MarketVenue, signal: AbortSignal) => Promise<T>
    persist: (venue: MarketVenue, value: T) => Promise<boolean>
  },
): Promise<MarketVenue[]> {
  const completed: MarketVenue[] = []
  for (const venue of venues) {
    if (options.signal.aborted || !options.shouldContinue()) return completed
    try {
      const value = await options.load(venue, options.signal)
      if (options.signal.aborted || !options.shouldContinue()) return completed
      if (await options.persist(venue, value)) completed.push(venue)
    } catch {
      if (options.signal.aborted) return completed
    }
  }
  return completed
}
