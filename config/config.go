package config

import (
	"github.com/urfave/cli/v2"

	"github.com/the-web3/s78-market-services/flags"
)

type Config struct {
	Migrations string
	RpcServer  ServerConfig
	RestServer ServerConfig
	Metrics    ServerConfig
	MasterDB   DBConfig
	SlaveDB    DBConfig
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
	}
}
