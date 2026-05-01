import type { MarketData } from '../types/market'

export const mockMarkets: MarketData[] = [
  { symbol: 'BTC/USDT', price: 65432.1, change24h: 2.5, volume: 1200.5, lastUpdated: new Date().toISOString() },
  { symbol: 'ETH/USDT', price: 3456.7, change24h: -1.2, volume: 8500.2, lastUpdated: new Date().toISOString() },
  { symbol: 'SOL/USDT', price: 145.2, change24h: 5.8, volume: 45000.0, lastUpdated: new Date().toISOString() },
  { symbol: 'BNB/USDT', price: 580.4, change24h: 0.3, volume: 3200.1, lastUpdated: new Date().toISOString() },
  { symbol: 'ADA/USDT', price: 0.45, change24h: -2.1, volume: 150000.0, lastUpdated: new Date().toISOString() },
]