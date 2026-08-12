package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"

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
	ResearchSignals       ResearchSignalsConfig
	Trading               TradingConfig
}

type ResearchSignalsConfig struct {
	Enabled bool
}

// TradingConfig keeps the virtual spot bounded context on a separate
// loopback-only gRPC endpoint. The existing HTTP API uses these values only as
// a transport/authentication gateway; matching state remains owned by the
// trading process.
type TradingConfig struct {
	GRPCAddress        string
	PracticeMode       bool
	StateDSNFile       string
	ReferenceDSNFile   string
	AllowedOrigins     []string
	LocalAuth          bool
	SecureCookies      bool
	GitHubClientID     string
	GitHubSecret       string
	GitHubRedirect     string
	DemoMakerEnabled   bool
	CursorHMACCurrent  string
	CursorHMACPrevious string
	RecoveryGate       bool
	ProductionOrigin   string
	DeploymentID       string
	DeploymentURL      string
	ReleaseCommit      string
	SourceDigest       string
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
		ResearchSignals: ResearchSignalsConfig{
			Enabled: ctx.Bool(flags.ResearchSignalsEnabledFlag.Name),
		},
		Trading: TradingConfig{
			GRPCAddress:        ctx.String(flags.TradingGRPCAddressFlag.Name),
			PracticeMode:       ctx.Bool(flags.TradingPracticeModeFlag.Name),
			StateDSNFile:       ctx.String(flags.TradingStateDSNFileFlag.Name),
			ReferenceDSNFile:   ctx.String(flags.TradingReferenceDSNFileFlag.Name),
			AllowedOrigins:     splitCSV(ctx.String(flags.TradingAllowedOriginsFlag.Name)),
			LocalAuth:          ctx.Bool(flags.TradingLocalAuthFlag.Name),
			SecureCookies:      ctx.Bool(flags.TradingSecureCookiesFlag.Name),
			GitHubClientID:     ctx.String(flags.TradingGitHubClientIDFlag.Name),
			GitHubSecret:       ctx.String(flags.TradingGitHubSecretFlag.Name),
			GitHubRedirect:     ctx.String(flags.TradingGitHubRedirectFlag.Name),
			DemoMakerEnabled:   ctx.Bool(flags.TradingDemoMakerFlag.Name),
			CursorHMACCurrent:  ctx.String(flags.TradingCursorHMACCurrentFlag.Name),
			CursorHMACPrevious: ctx.String(flags.TradingCursorHMACPreviousFlag.Name),
			RecoveryGate:       ctx.Bool(flags.TradingRecoveryGateFlag.Name),
			ProductionOrigin:   ctx.String(flags.TradingProductionOriginFlag.Name),
			DeploymentID:       ctx.String(flags.TradingDeploymentIDFlag.Name),
			DeploymentURL:      ctx.String(flags.TradingDeploymentURLFlag.Name),
			ReleaseCommit:      ctx.String(flags.TradingReleaseCommitFlag.Name),
			SourceDigest:       ctx.String(flags.TradingSourceDigestFlag.Name),
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

// TradingPostgresURLs resolves the trading write model and the market-data
// reference reader without ever returning file contents in an error. Practice
// mode requires two distinct owner-only files; legacy deployments continue to
// use the existing master database until they explicitly opt in.
func (c Config) TradingPostgresURLs() (string, string, error) {
	if !c.Trading.PracticeMode && c.Trading.StateDSNFile == "" && c.Trading.ReferenceDSNFile == "" {
		legacy := c.MasterDB.PostgresURL()
		return legacy, legacy, nil
	}
	if c.Trading.StateDSNFile == "" || c.Trading.ReferenceDSNFile == "" {
		return "", "", fmt.Errorf("trading state and reference DSN files are both required")
	}
	state, err := readPrivatePostgresDSN(c.Trading.StateDSNFile)
	if err != nil {
		return "", "", fmt.Errorf("invalid trading state DSN file: %w", err)
	}
	reference, err := readPrivatePostgresDSN(c.Trading.ReferenceDSNFile)
	if err != nil {
		return "", "", fmt.Errorf("invalid trading reference DSN file: %w", err)
	}
	if state == reference {
		return "", "", fmt.Errorf("trading state and reference PostgreSQL must be independent")
	}
	return state, reference, nil
}

func readPrivatePostgresDSN(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("private file is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 || int(stat.Uid) != os.Getuid() {
		return "", fmt.Errorf("private file must be an owner-only regular non-symlink 0600 file")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("private file is unreadable")
	}
	value := strings.TrimSpace(string(contents))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("private file must contain exactly one non-empty line")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.Host == "" || strings.TrimPrefix(parsed.Path, "/") == "" {
		return "", fmt.Errorf("private file does not contain a PostgreSQL DSN")
	}
	return value, nil
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
