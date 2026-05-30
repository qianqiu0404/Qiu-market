export interface MarketData {
  symbol: string;
  price: number;
  change24h: number;
  volume: number;
  market_cap?: number;
  name?: string;
  logo?: string;
  lastUpdated?: string;
}
