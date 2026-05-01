export interface MarketData {
  symbol: string;
  price: number;
  change24h: number;
  volume: number;
  name?: string;
  logo?: string;
}
