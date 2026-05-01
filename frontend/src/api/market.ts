import type { MarketData } from '../types/market'

export const fetchMarkets = async (page: number = 1, pageSize: number = 10): Promise<{ items: MarketData[], total: number, source: string }> => {
  try {
    const response = await fetch('/api/v1/get_market_dashboard', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        consumer_token: 'frontend-dashboard',
        page: page,
        page_size: pageSize
      })
    })
    
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }
    
    const data = await response.json()
    
    if (data.code === 2000 && Array.isArray(data.result)) {
      return {
        items: data.result.map((item: any) => ({
          symbol: item.symbol,
          price: parseFloat(item.price) || 0, 
          change24h: parseFloat(item.change24h) || 0, 
          volume: parseFloat(item.volume) || 0, 
          name: item.name,
          logo: item.logo
        })),
        total: data.total || data.result.length,
        source: 'Connected'
      }
    }
    
    throw new Error(data.message || 'Invalid API response')
  } catch (error) {
    console.error('Failed to fetch markets:', error)
    // Fallback to mock data for MarketTable (used in other parts of the UI)
    return { 
      items: [
        { name: 'Bitcoin', symbol: 'BTC/USDT', price: 65000, volume: 1234567, change24h: 2.5, logo: '' },
        { name: 'Ethereum', symbol: 'ETH/USDT', price: 3500, volume: 987654, change24h: -1.2, logo: '' }
      ], 
      total: 2,
      source: 'Mock fallback'
    }
  }
}
