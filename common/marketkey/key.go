package marketkey

// RankChange24hKey 是 24h 涨跌幅榜单的 Redis ZSET key：
// member 为 symbol_guid，score 为 24h 涨跌幅百分比（浮点）。
// crawler 每次 ticker 更新时 ZADD 覆盖写，API 用 ZREVRANGE/ZRANGE 直接读榜。
const RankChange24hKey = "market:rank:change24h"

func Build(exchangeGuid, exchangeName, symbolGuid, symbolName string) string {
	return exchangeGuid + "%" + exchangeName + "%" + symbolGuid + "%" + symbolName
}
