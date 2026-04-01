package config

import (
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
	ExchangeRatePlatforms []ExchangeRatePlatformConfig
	BaseCurrency          string
	APIKeyConfig          APIKeyConfig
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
	}
}
