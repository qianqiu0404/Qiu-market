package flags

import "github.com/urfave/cli/v2"

const evnVarPrefix = "MARKET"

func prefixEnvVars(name string) []string {
	return []string{evnVarPrefix + "_" + name}
}

var (
	MigrationsFlag = &cli.StringFlag{
		Name:    "migrations-dir",
		Value:   "./migrations",
		Usage:   "path for database migrations",
		EnvVars: prefixEnvVars("MIGRATIONS_DIR"),
	}

	// RpcHostFlag RPC Service
	RpcHostFlag = &cli.StringFlag{
		Name:     "rpc-host",
		Usage:    "The host of the rpc",
		EnvVars:  prefixEnvVars("RPC_HOST"),
		Required: true,
	}
	RpcPortFlag = &cli.IntFlag{
		Name:     "rpc-port",
		Usage:    "The port of the rpc",
		EnvVars:  prefixEnvVars("RPC_PORT"),
		Required: true,
	}

	// HttpHostFlag RPC Service
	HttpHostFlag = &cli.StringFlag{
		Name:     "http-host",
		Usage:    "The host of the http",
		EnvVars:  prefixEnvVars("HTTP_HOST"),
		Required: true,
	}
	HttpPortFlag = &cli.IntFlag{
		Name:     "http-port",
		Usage:    "The port of the http",
		EnvVars:  prefixEnvVars("HTTP_PORT"),
		Required: true,
	}

	// MasterDbHostFlag Flags
	MasterDbHostFlag = &cli.StringFlag{
		Name:     "master-db-host",
		Usage:    "The host of the master database",
		EnvVars:  prefixEnvVars("MASTER_DB_HOST"),
		Required: true,
	}
	MasterDbPortFlag = &cli.IntFlag{
		Name:     "master-db-port",
		Usage:    "The port of the master database",
		EnvVars:  prefixEnvVars("MASTER_DB_PORT"),
		Required: true,
	}
	MasterDbUserFlag = &cli.StringFlag{
		Name:     "master-db-user",
		Usage:    "The user of the master database",
		EnvVars:  prefixEnvVars("MASTER_DB_USER"),
		Required: true,
	}
	MasterDbPasswordFlag = &cli.StringFlag{
		Name:     "master-db-password",
		Usage:    "The host of the master database",
		EnvVars:  prefixEnvVars("MASTER_DB_PASSWORD"),
		Required: true,
	}
	MasterDbNameFlag = &cli.StringFlag{
		Name:     "master-db-name",
		Usage:    "The db name of the master database",
		EnvVars:  prefixEnvVars("MASTER_DB_NAME"),
		Required: true,
	}

	// Slave DB  flags
	SlaveDbHostFlag = &cli.StringFlag{
		Name:    "slave-db-host",
		Usage:   "The host of the slave database",
		EnvVars: prefixEnvVars("SLAVE_DB_HOST"),
	}
	SlaveDbPortFlag = &cli.IntFlag{
		Name:    "slave-db-port",
		Usage:   "The port of the slave database",
		EnvVars: prefixEnvVars("SLAVE_DB_PORT"),
	}
	SlaveDbUserFlag = &cli.StringFlag{
		Name:    "slave-db-user",
		Usage:   "The user of the slave database",
		EnvVars: prefixEnvVars("SLAVE_DB_USER"),
	}
	SlaveDbPasswordFlag = &cli.StringFlag{
		Name:    "slave-db-password",
		Usage:   "The host of the slave database",
		EnvVars: prefixEnvVars("SLAVE_DB_PASSWORD"),
	}
	SlaveDbNameFlag = &cli.StringFlag{
		Name:    "slave-db-name",
		Usage:   "The db name of the slave database",
		EnvVars: prefixEnvVars("SLAVE_DB_NAME"),
	}

	MetricsHostFlag = &cli.StringFlag{
		Name:     "metric-host",
		Usage:    "The host of the metric",
		EnvVars:  prefixEnvVars("METRIC_HOST"),
		Required: true,
	}
	MetricsPortFlag = &cli.IntFlag{
		Name:     "metric-port",
		Usage:    "The port of the metric",
		EnvVars:  prefixEnvVars("METRIC_PORT"),
		Required: true,
	}

	RedisAddressFlag = &cli.StringFlag{
		Name:     "redis-address",
		Usage:    "The address of the redis",
		EnvVars:  prefixEnvVars("REDIS_ADDRESS"),
		Required: true,
	}
	RedisPasswordFlag = &cli.StringFlag{
		Name:    "redis-password",
		Usage:   "The password of the redis",
		EnvVars: prefixEnvVars("REDIS_PASSWORD"),
	}
	RedisDbIndexFlag = &cli.IntFlag{
		Name:     "redis-db-index",
		Usage:    "The DB index of the redis",
		EnvVars:  prefixEnvVars("REDIS_DB_INDEX"),
		Required: true,
	}
	MultiVenueEnabledFlag = &cli.BoolFlag{
		Name:    "multi-venue-enabled",
		Usage:   "enable reviewed multi-venue candidates in the formal snapshot/composite read model",
		Value:   false,
		EnvVars: prefixEnvVars("MULTI_VENUE_ENABLED"),
	}
	EthereumRPCURLFlag = &cli.StringFlag{
		Name:    "ethereum-rpc-url",
		Usage:   "private Ethereum mainnet JSON-RPC endpoint for Uniswap V2/V3 read-only quotes",
		EnvVars: prefixEnvVars("ETHEREUM_RPC_URL"),
	}
	BSCRPCURLFlag = &cli.StringFlag{
		Name:    "bsc-rpc-url",
		Usage:   "private BNB Smart Chain JSON-RPC endpoint for PancakeSwap V2/V3 read-only quotes",
		EnvVars: prefixEnvVars("BSC_RPC_URL"),
	}
	UniswapV3SubgraphURLFlag = &cli.StringFlag{
		Name:    "uniswap-v3-subgraph-url",
		Usage:   "The Graph endpoint for the reviewed Uniswap V3 Ethereum subgraph",
		EnvVars: prefixEnvVars("UNISWAP_V3_SUBGRAPH_URL"),
	}
	PancakeV3SubgraphURLFlag = &cli.StringFlag{
		Name:    "pancake-v3-subgraph-url",
		Usage:   "Graph endpoint for the reviewed PancakeSwap V3 BSC subgraph",
		EnvVars: prefixEnvVars("PANCAKE_V3_SUBGRAPH_URL"),
	}
	DexPublicFallbackFlag = &cli.BoolFlag{
		Name: "dex-public-fallback",
		Usage: "use rate-limited public RPC and DEX Screener discovery for local " +
			"Uniswap/Pancake preview when private endpoints are absent",
		Value:   false,
		EnvVars: prefixEnvVars("DEX_PUBLIC_FALLBACK"),
	}
	PublicProxyHMACSecretFlag = &cli.StringFlag{
		Name:    "public-proxy-hmac-secret",
		Usage:   "shared secret required for REST requests forwarded by the Qiu Market public BFF",
		EnvVars: prefixEnvVars("PUBLIC_PROXY_HMAC_SECRET"),
	}
	TradingGRPCAddressFlag = &cli.StringFlag{
		Name:    "trading-grpc-address",
		Usage:   "loopback address of the virtual spot TradingService",
		Value:   "127.0.0.1:9094",
		EnvVars: prefixEnvVars("TRADING_GRPC_ADDR"),
	}
	TradingAllowedOriginsFlag = &cli.StringFlag{
		Name:    "trading-allowed-origins",
		Usage:   "comma-separated browser origins allowed by the trading REST/WebSocket gateway",
		Value:   "http://127.0.0.1:5174",
		EnvVars: prefixEnvVars("TRADING_ALLOWED_ORIGINS"),
	}
	TradingLocalAuthFlag = &cli.BoolFlag{
		Name:    "trading-local-auth",
		Usage:   "explicitly allow the loopback-only local trading login",
		Value:   false,
		EnvVars: prefixEnvVars("TRADING_LOCAL_AUTH"),
	}
	TradingSecureCookiesFlag = &cli.BoolFlag{
		Name:    "trading-secure-cookies",
		Usage:   "mark trading session and CSRF cookies Secure (required behind HTTPS)",
		Value:   false,
		EnvVars: prefixEnvVars("TRADING_SECURE_COOKIES"),
	}
	TradingGitHubClientIDFlag = &cli.StringFlag{
		Name:    "trading-github-client-id",
		Usage:   "GitHub OAuth client ID for the single-user virtual trading terminal",
		EnvVars: prefixEnvVars("TRADING_GITHUB_CLIENT_ID"),
	}
	TradingGitHubSecretFlag = &cli.StringFlag{
		Name:    "trading-github-client-secret",
		Usage:   "GitHub OAuth client secret for the single-user virtual trading terminal",
		EnvVars: prefixEnvVars("TRADING_GITHUB_CLIENT_SECRET"),
	}
	TradingGitHubRedirectFlag = &cli.StringFlag{
		Name:    "trading-github-redirect-url",
		Usage:   "GitHub OAuth callback URL for the trading gateway",
		EnvVars: prefixEnvVars("TRADING_GITHUB_REDIRECT_URL"),
	}
	TradingDemoMakerFlag = &cli.BoolFlag{
		Name:    "trading-demo-maker",
		Usage:   "run the virtual system:demo-maker against a fresh S78 BTC composite reference",
		Value:   true,
		EnvVars: prefixEnvVars("TRADING_DEMO_MAKER_ENABLED"),
	}
	TradingRecoveryGateFlag = &cli.BoolFlag{
		Name:    "trading-recovery-gate",
		Usage:   "enable the durable fail-closed trading recovery write gate; activation requires an operator recovery workflow",
		Value:   false,
		EnvVars: prefixEnvVars("TRADING_RECOVERY_GATE_ENABLED"),
	}

	// Doris OLAP 数仓（全部可选）：默认值与 docker-compose 的 doris 服务匹配。
	// 显式将 MARKET_DORIS_HOST 置空可禁用数仓；Doris 未运行时 dw 模式与
	// get_kline_analytics 显式报错，其余进程完全不受影响。
	DorisHostFlag = &cli.StringFlag{
		Name:    "doris-host",
		Usage:   "The host of the Apache Doris FE (set empty to disable the data warehouse)",
		Value:   "127.0.0.1",
		EnvVars: prefixEnvVars("DORIS_HOST"),
	}
	DorisHttpPortFlag = &cli.IntFlag{
		Name:    "doris-http-port",
		Usage:   "The FE HTTP port of Doris (Stream Load)",
		Value:   8030,
		EnvVars: prefixEnvVars("DORIS_HTTP_PORT"),
	}
	DorisQueryPortFlag = &cli.IntFlag{
		Name:    "doris-query-port",
		Usage:   "The FE MySQL protocol port of Doris (analytics queries)",
		Value:   9030,
		EnvVars: prefixEnvVars("DORIS_QUERY_PORT"),
	}
	DorisUserFlag = &cli.StringFlag{
		Name:    "doris-user",
		Usage:   "The user of Doris",
		Value:   "root",
		EnvVars: prefixEnvVars("DORIS_USER"),
	}
	DorisPasswordFlag = &cli.StringFlag{
		Name:    "doris-password",
		Usage:   "The password of Doris (all-in-one 默认 root 空密码)",
		EnvVars: prefixEnvVars("DORIS_PASSWORD"),
	}
	DorisDbFlag = &cli.StringFlag{
		Name:    "doris-db",
		Usage:   "The database name of Doris data warehouse",
		Value:   "s78_market_dw",
		EnvVars: prefixEnvVars("DORIS_DB"),
	}
)

var requireFlags = []cli.Flag{
	MigrationsFlag,
	RpcHostFlag,
	RpcPortFlag,
	HttpHostFlag,
	HttpPortFlag,
	MasterDbHostFlag,
	MasterDbPortFlag,
	MasterDbUserFlag,
	MasterDbPasswordFlag,
	MasterDbNameFlag,
	RedisAddressFlag,
	RedisDbIndexFlag,
}

var optionalFlags = []cli.Flag{
	SlaveDbHostFlag,
	SlaveDbPortFlag,
	SlaveDbUserFlag,
	SlaveDbPasswordFlag,
	SlaveDbNameFlag,
	MetricsHostFlag,
	MetricsPortFlag,
	RedisPasswordFlag,
	MultiVenueEnabledFlag,
	EthereumRPCURLFlag,
	BSCRPCURLFlag,
	UniswapV3SubgraphURLFlag,
	PancakeV3SubgraphURLFlag,
	DexPublicFallbackFlag,
	PublicProxyHMACSecretFlag,
	TradingGRPCAddressFlag,
	TradingAllowedOriginsFlag,
	TradingLocalAuthFlag,
	TradingSecureCookiesFlag,
	TradingGitHubClientIDFlag,
	TradingGitHubSecretFlag,
	TradingGitHubRedirectFlag,
	TradingDemoMakerFlag,
	TradingRecoveryGateFlag,
	DorisHostFlag,
	DorisHttpPortFlag,
	DorisQueryPortFlag,
	DorisUserFlag,
	DorisPasswordFlag,
	DorisDbFlag,
}

func init() {
	Flags = append(requireFlags, optionalFlags...)
}

var Flags []cli.Flag
