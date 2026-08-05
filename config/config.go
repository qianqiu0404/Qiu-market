package config

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/urfave/cli/v2"

	"github.com/the-web3/s78-market-services/flags"
)

type Config struct {
	Migrations            string
	RpcServer             ServerConfig
	RestServer            ServerConfig
	RedisConfig           RedisConfig
	Metrics               ServerConfig
	MasterDB              DBConfig
	SlaveDB               DBConfig
	Doris                 DorisConfig
	ExchangeRatePlatforms []ExchangeRatePlatformConfig
	BaseCurrency          string
	APIKeyConfig          APIKeyConfig
	MultiVenueEnabled     bool
	EthereumRPCURL        string
	BSCRPCURL             string
	UniswapV3SubgraphURL  string
	PancakeV3SubgraphURL  string
	DexPublicFallback     bool
	PublicProxyHMACSecret string
	Trading               TradingConfig
}

// TradingConfig keeps the virtual spot bounded context on a separate
// loopback-only gRPC endpoint. The existing HTTP API uses these values only as
// a transport/authentication gateway; matching state remains owned by the
// trading process.
type TradingConfig struct {
	GRPCAddress      string
	AllowedOrigins   []string
	LocalAuth        bool
	SecureCookies    bool
	GitHubClientID   string
	GitHubSecret     string
	GitHubRedirect   string
	DemoMakerEnabled bool
	RecoveryGate     bool
	ProductionOrigin string
	DeploymentID     string
	DeploymentURL    string
	ReleaseCommit    string
	SourceDigest     string
}

// DorisConfig Apache Doris 连接配置。Host 为空表示未配置数仓：
// dw 进程拒绝启动，get_kline_analytics 返回明确错误，其它功能不受影响。
type DorisConfig struct {
	Host      string
	HttpPort  int // FE HTTP 端口（Stream Load），默认 8030
	QueryPort int // FE MySQL 协议端口（分析查询），默认 9030
	User      string
	Password  string
	Database  string
}

// Enabled Doris 是否已配置（Host 非空即视为启用）。
func (d DorisConfig) Enabled() bool {
	return d.Host != ""
}

type APIKeyConfig struct {
	ExchangeRate      string `yaml:"exchange_rate"`
	FixerIO           string `yaml:"fixer_io"`
	OpenExchangeRates string `yaml:"open_exchange_rates"`
	Currency          string `yaml:"currency"`
	CurrencyBeacon    string `yaml:"currency_beacon"`
	CurrencyFreaks    string `yaml:"currency_freaks"`
}

type ExchangeRatePlatformConfig struct {
	Name    string
	BaseURL string
}

type ServerConfig struct {
	Host string
	Port int
}

type DBConfig struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
}

// PostgresURL returns a pgx-compatible URL without exposing credentials in
// logs. URL escaping also keeps passwords containing spaces or punctuation
// from corrupting the connection string.
func (d DBConfig) PostgresURL() string {
	host := strings.TrimSpace(d.Host)
	if d.Port > 0 {
		host = net.JoinHostPort(host, strconv.Itoa(d.Port))
	}
	connection := &url.URL{
		Scheme: "postgres",
		Host:   host,
		Path:   "/" + strings.TrimPrefix(d.Name, "/"),
	}
	if d.User != "" {
		if d.Password == "" {
			connection.User = url.User(d.User)
		} else {
			connection.User = url.UserPassword(d.User, d.Password)
		}
	}
	query := connection.Query()
	query.Set("sslmode", "disable")
	connection.RawQuery = query.Encode()
	return connection.String()
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`     // Redis地址，格式: host:port
	Password string `yaml:"password"` // Redis密码（可选）
	DB       int    `yaml:"db"`       // Redis数据库索引
}

func NewConfig(ctx *cli.Context) Config {
	return Config{
		Migrations: ctx.String(flags.MigrationsFlag.Name),
		RpcServer: ServerConfig{
			Host: ctx.String(flags.RpcHostFlag.Name),
			Port: ctx.Int(flags.RpcPortFlag.Name),
		},
		RestServer: ServerConfig{
			Host: ctx.String(flags.HttpHostFlag.Name),
			Port: ctx.Int(flags.HttpPortFlag.Name),
		},
		Metrics: ServerConfig{
			Host: ctx.String(flags.MetricsHostFlag.Name),
			Port: ctx.Int(flags.MetricsPortFlag.Name),
		},
		MasterDB: DBConfig{
			Host:     ctx.String(flags.MasterDbHostFlag.Name),
			Port:     ctx.Int(flags.MasterDbPortFlag.Name),
			Name:     ctx.String(flags.MasterDbNameFlag.Name),
			User:     ctx.String(flags.MasterDbUserFlag.Name),
			Password: ctx.String(flags.MasterDbPasswordFlag.Name),
		},
		SlaveDB: DBConfig{
			Host:     ctx.String(flags.SlaveDbHostFlag.Name),
			Port:     ctx.Int(flags.SlaveDbPortFlag.Name),
			Name:     ctx.String(flags.SlaveDbNameFlag.Name),
			User:     ctx.String(flags.SlaveDbUserFlag.Name),
			Password: ctx.String(flags.SlaveDbPasswordFlag.Name),
		},
		RedisConfig: RedisConfig{
			Addr:     ctx.String(flags.RedisAddressFlag.Name),
			Password: ctx.String(flags.RedisPasswordFlag.Name),
			DB:       ctx.Int(flags.RedisDbIndexFlag.Name),
		},
		MultiVenueEnabled:     ctx.Bool(flags.MultiVenueEnabledFlag.Name),
		EthereumRPCURL:        ctx.String(flags.EthereumRPCURLFlag.Name),
		BSCRPCURL:             ctx.String(flags.BSCRPCURLFlag.Name),
		UniswapV3SubgraphURL:  ctx.String(flags.UniswapV3SubgraphURLFlag.Name),
		PancakeV3SubgraphURL:  ctx.String(flags.PancakeV3SubgraphURLFlag.Name),
		DexPublicFallback:     ctx.Bool(flags.DexPublicFallbackFlag.Name),
		PublicProxyHMACSecret: ctx.String(flags.PublicProxyHMACSecretFlag.Name),
		Trading: TradingConfig{
			GRPCAddress:      ctx.String(flags.TradingGRPCAddressFlag.Name),
			AllowedOrigins:   splitCSV(ctx.String(flags.TradingAllowedOriginsFlag.Name)),
			LocalAuth:        ctx.Bool(flags.TradingLocalAuthFlag.Name),
			SecureCookies:    ctx.Bool(flags.TradingSecureCookiesFlag.Name),
			GitHubClientID:   ctx.String(flags.TradingGitHubClientIDFlag.Name),
			GitHubSecret:     ctx.String(flags.TradingGitHubSecretFlag.Name),
			GitHubRedirect:   ctx.String(flags.TradingGitHubRedirectFlag.Name),
			DemoMakerEnabled: ctx.Bool(flags.TradingDemoMakerFlag.Name),
			RecoveryGate:     ctx.Bool(flags.TradingRecoveryGateFlag.Name),
			ProductionOrigin: ctx.String(flags.TradingProductionOriginFlag.Name),
			DeploymentID:     ctx.String(flags.TradingDeploymentIDFlag.Name),
			DeploymentURL:    ctx.String(flags.TradingDeploymentURLFlag.Name),
			ReleaseCommit:    ctx.String(flags.TradingReleaseCommitFlag.Name),
			SourceDigest:     ctx.String(flags.TradingSourceDigestFlag.Name),
		},
		Doris: DorisConfig{
			Host:      ctx.String(flags.DorisHostFlag.Name),
			HttpPort:  ctx.Int(flags.DorisHttpPortFlag.Name),
			QueryPort: ctx.Int(flags.DorisQueryPortFlag.Name),
			User:      ctx.String(flags.DorisUserFlag.Name),
			Password:  ctx.String(flags.DorisPasswordFlag.Name),
			Database:  ctx.String(flags.DorisDbFlag.Name),
		},
	}
}

func splitCSV(value string) []string {
	values := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}
