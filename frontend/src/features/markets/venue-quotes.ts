import type { AssetMarketV2Item } from '../../api/market'

export type QuoteVenueStatus = 'live' | 'stale' | 'unavailable'

export interface VenueQuoteRow {
  provider: 'binance' | 'coinbase' | 'bybit' | 'okx' | 'hyperliquid' | 'uniswap' | 'pancakeswap'
  label: string
  product: 'Spot' | 'Perpetual' | 'DEX Route'
  market: AssetMarketV2Item | null
  marketCount: number
  status: QuoteVenueStatus
  reason: string
}

const QUOTE_VENUES: Array<Pick<VenueQuoteRow, 'provider' | 'label' | 'product'>> = [
  { provider: 'binance', label: 'Binance', product: 'Spot' },
  { provider: 'coinbase', label: 'Coinbase', product: 'Spot' },
  { provider: 'bybit', label: 'Bybit', product: 'Spot' },
  { provider: 'okx', label: 'OKX', product: 'Spot' },
  { provider: 'hyperliquid', label: 'Hyperliquid', product: 'Perpetual' },
  { provider: 'uniswap', label: 'Uniswap', product: 'DEX Route' },
  { provider: 'pancakeswap', label: 'PancakeSwap', product: 'DEX Route' },
]

const QUOTE_PRIORITY: Record<string, number> = { USD: 0, USDT: 1, USDC: 2 }
const EVM_ADDRESS = /^0x[0-9a-f]{40}$/i
const DEX_ROUTE_MAX_AGE_MS = 60_000

function marketScore(market: AssetMarketV2Item): number {
  const freshness = market.freshness_status.trim().toLowerCase()
  const quotePriority = QUOTE_PRIORITY[market.quote_asset.toUpperCase()] ?? 10
  return (market.price.available ? 0 : 1_000)
    + (freshness === 'healthy' || freshness === 'fresh' ? 0 : freshness === 'stale' ? 100 : 200)
    + (market.confidence === 'excluded' || market.confidence.startsWith('excluded_') ? 20 : 0)
    + quotePriority
}

export function hasValidDexRouteIdentity(market: AssetMarketV2Item, now = Date.now()): boolean {
  if (market.market_type !== 'dex_route') return true
  const chain = market.chain.trim().toLowerCase()
  const expectedChain = market.provider === 'uniswap'
    ? chain === 'ethereum' || chain === 'chain:1'
    : market.provider === 'pancakeswap'
      ? chain === 'bnb chain' || chain === 'bsc' || chain === 'chain:56'
      : false
  const observedAt = market.block_timestamp
  return expectedChain
    && market.protocol.trim() !== ''
    && market.route_key.trim() !== ''
    && market.route.length >= 2
    && market.route.every((token) => token.trim() !== '')
    && market.pool_addresses.length > 0
    && market.pool_addresses.every((address) => EVM_ADDRESS.test(address))
    && market.block_number > 0
    && observedAt > 0
    && observedAt <= now + 5_000
    && now - observedAt <= DEX_ROUTE_MAX_AGE_MS
}

function quoteStatus(market: AssetMarketV2Item | null, now: number): QuoteVenueStatus {
  if (!market?.price.available
    || (market.market_type === 'dex_route' && (!market.available || !hasValidDexRouteIdentity(market, now)))) {
    return 'unavailable'
  }
  const freshness = market.freshness_status.trim().toLowerCase()
  if (freshness === 'healthy' || freshness === 'fresh') return 'live'
  return 'stale'
}

function dexRouteScore(market: AssetMarketV2Item, now: number): number {
  const freshness = market.freshness_status.trim().toLowerCase()
  const quality = market.quality.trim().toLowerCase()
  const notional = market.quote_notional_usd.available
    ? Number(market.quote_notional_usd.value ?? 0)
    : 0
  return (market.available && market.price.available && hasValidDexRouteIdentity(market, now) ? 0 : 10_000)
    + (freshness === 'healthy' || freshness === 'fresh' ? 0 : freshness === 'stale' ? 1_000 : 2_000)
    + (quality === 'high' ? 0 : quality === 'medium' ? 100 : 200)
    - Math.min(Math.max(notional, 0), 100_000) / 100_000
}

export function buildVenueQuoteRows(markets: AssetMarketV2Item[], now = Date.now()): VenueQuoteRow[] {
  return QUOTE_VENUES.map((venue) => {
    const marketType = venue.product === 'Spot'
      ? 'spot'
      : venue.product === 'Perpetual'
        ? 'perp'
        : 'dex_route'
    const candidates = markets
      .filter((market) => market.provider === venue.provider && market.market_type === marketType)
      .sort((left, right) => marketType === 'dex_route'
        ? dexRouteScore(left, now) - dexRouteScore(right, now)
        : marketScore(left) - marketScore(right))
    const market = candidates[0] ?? null
    const status = quoteStatus(market, now)
    const reason = status === 'unavailable'
      ? market?.unavailable_reason || (marketType === 'dex_route'
        ? market && !hasValidDexRouteIdentity(market, now)
          ? 'Route identity or block freshness is not verified'
          : 'No reviewed route in the current public preview'
        : 'Unavailable in the current deployment')
      : status === 'stale'
        ? 'Last known quote — waiting for a fresh provider update'
        : ''
    return { ...venue, market, marketCount: candidates.length, status, reason }
  })
}
