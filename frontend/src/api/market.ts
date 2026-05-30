import { request } from './common'
import type { MarketData } from '../types/market'

export type MarketFetchResult = {
  items: MarketData[]
  total: number
  source: 'Connected' | 'Error'
  error?: string
}

export const fetchMarkets = async (page: number = 1, pageSize: number = 10): Promise<MarketFetchResult> => {
  const res = await request<unknown[]>('/api/v1/get_market_dashboard', {
    data: {
      page,
      page_size: pageSize,
    }
  })

  if (res.source !== 'Connected' || !Array.isArray(res.data)) {
    return {
      items: [],
      total: 0,
      source: 'Error',
      error: res.error || 'Unable to load market data',
    }
  }

  return {
    items: res.data.map((item: any) => ({
      symbol: item.symbol,
      price: parseFloat(item.price) || 0,
      change24h: parseFloat(item.change24h) || 0,
      volume: parseFloat(item.volume) || 0,
      market_cap: parseFloat(item.market_cap) || 0,
      name: item.name,
      logo: item.logo,
    })),
    total: res.total || res.data.length,
    source: 'Connected',
  }
}
