package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/urfave/cli/v2"

	"github.com/the-web3/s78-market-services/common/cliapp"
	"github.com/the-web3/s78-market-services/common/opio"
	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/crawler"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/dw"
	flags2 "github.com/the-web3/s78-market-services/flags"
	"github.com/the-web3/s78-market-services/redis"
	"github.com/the-web3/s78-market-services/services/grpc"
	rest "github.com/the-web3/s78-market-services/services/http"
	tradingservice "github.com/the-web3/s78-market-services/trading/service"
	"github.com/the-web3/s78-market-services/worker"
)

func runRpc(ctx *cli.Context, shutdown context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	ctx.Context = opio.CancelOnInterrupt(ctx.Context)
	log.Info("running rpc service...")
	cfg := config.NewConfig(ctx)
	db, err := database.NewDB(ctx.Context, cfg.MasterDB)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		return nil, err
	}

	// Redis 与 api 模式同样采用容错初始化：连不上只告警，
	// GetTopMovers 会自动回退 SQL 排序，不影响其它 RPC。
	var redisCli *redis.Client
	rc, err := redis.New(redis.Config{
		Address:  cfg.RedisConfig.Addr,
		Password: cfg.RedisConfig.Password,
		DB:       cfg.RedisConfig.DB,
	})
	if err != nil {
		log.Warn("redis unavailable, GetTopMovers will use SQL fallback", "err", err)
	} else {
		redisCli = rc
		redisCli.StartHeartbeat(ctx.Context, "rpc", 5*time.Second, 15*time.Second)
	}

	markConfig := grpc.MarketRpcConfig{
		Host: cfg.RpcServer.Host,
		Port: cfg.RpcServer.Port,
	}

	return grpc.NewMarketRpcService(&markConfig, db, redisCli)
}

func runMigrations(ctx *cli.Context) error {
	ctx.Context = opio.CancelOnInterrupt(ctx.Context)
	log.Info("running migrations...")
	cfg := config.NewConfig(ctx)
	db, err := database.NewDB(ctx.Context, cfg.MasterDB)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		return err
	}
	defer func(db *database.DB) {
		err := db.Close()
		if err != nil {
			log.Error("fail to close database", "err", err)
		}
	}(db)
	return db.ExecuteSQLMigration(cfg.Migrations)
}

func runRestApi(ctx *cli.Context, shutdown context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	cfg := config.NewConfig(ctx)
	return rest.NewApi(context.Background(), &cfg)
}

func runTrading(ctx *cli.Context, shutdown context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	log.Info("run isolated virtual BTC/USDT trading service...")
	cfg := config.NewConfig(ctx)
	return tradingservice.New(ctx.Context, tradingservice.Config{
		PostgresURL:      cfg.MasterDB.PostgresURL(),
		GRPCAddress:      cfg.Trading.GRPCAddress,
		DemoMakerEnabled: cfg.Trading.DemoMakerEnabled,
	}, shutdown)
}

func runCrawler(ctx *cli.Context, shutdown context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	log.Info("run orderbook crawler...")
	cfg := config.NewConfig(ctx)
	db, err := database.NewDB(ctx.Context, cfg.MasterDB)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		return nil, err
	}

	redisConfig := redis.Config{
		Address:  cfg.RedisConfig.Addr,
		Password: cfg.RedisConfig.Password,
		DB:       cfg.RedisConfig.DB,
	}

	redisClient, err := redis.New(redisConfig)
	if err != nil {
		log.Error("fail to connect to redis", "err", err)
		return nil, err
	}
	redisClient.StartHeartbeat(ctx.Context, "crawler", 5*time.Second, 15*time.Second)
	return crawler.NewCrawler(db, redisClient, &cfg, shutdown)
}

func runDex(ctx *cli.Context, shutdown context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	log.Info("run dex supervisor (Hyperliquid, Uniswap V2/V3, PancakeSwap V2/V3)...")
	cfg := config.NewConfig(ctx)
	db, err := database.NewDB(ctx.Context, cfg.MasterDB)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		return nil, err
	}

	redisConfig := redis.Config{
		Address:  cfg.RedisConfig.Addr,
		Password: cfg.RedisConfig.Password,
		DB:       cfg.RedisConfig.DB,
	}

	redisClient, err := redis.New(redisConfig)
	if err != nil {
		log.Error("fail to connect to redis", "err", err)
		return nil, err
	}
	redisClient.StartHeartbeat(ctx.Context, "dex", 5*time.Second, 15*time.Second)
	return crawler.NewDexSupervisor(db, redisClient, &cfg), nil
}

func runWorker(ctx *cli.Context, shutdown context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	log.Info("run worker...")
	cfg := config.NewConfig(ctx)
	db, err := database.NewDB(ctx.Context, cfg.MasterDB)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		return nil, err
	}

	redisConfig := redis.Config{
		Address:  cfg.RedisConfig.Addr,
		Password: cfg.RedisConfig.Password,
		DB:       cfg.RedisConfig.DB,
	}

	redisClient, err := redis.New(redisConfig)
	if err != nil {
		log.Error("fail to connect to redis", "err", err)
		return nil, err
	}
	redisClient.StartHeartbeat(ctx.Context, "worker", 5*time.Second, 15*time.Second)
	return worker.NewWorker(db, redisClient, &cfg, shutdown)
}

// runDw 数仓同步模式（PostgreSQL -> Apache Doris）。Doris 未配置 / 未运行时
// 返回明确错误拒绝启动，不影响 api / crawler / worker / dex / rpc。
func runDw(ctx *cli.Context, shutdown context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	log.Info("run dw (PostgreSQL -> Doris data warehouse sync)...")
	cfg := config.NewConfig(ctx)
	if !cfg.Doris.Enabled() {
		return nil, fmt.Errorf("doris data warehouse is not configured: set MARKET_DORIS_HOST / start the compose doris service, or explicitly disable with MARKET_DORIS_HOST=")
	}
	db, err := database.NewDB(ctx.Context, cfg.MasterDB)
	if err != nil {
		log.Error("failed to connect to database", "err", err)
		return nil, err
	}
	// dw 本来不依赖 Redis，心跳按容错模式初始化：连不上只告警，不阻断同步。
	if rc, err := redis.New(redis.Config{
		Address:  cfg.RedisConfig.Addr,
		Password: cfg.RedisConfig.Password,
		DB:       cfg.RedisConfig.DB,
	}); err != nil {
		log.Warn("redis unavailable, dw heartbeat disabled", "err", err)
	} else {
		rc.StartHeartbeat(ctx.Context, "dw", 5*time.Second, 15*time.Second)
	}
	return dw.NewDW(db, cfg.Doris, shutdown)
}

func NewCli(GitCommit string, GitData string) *cli.App {
	flags := flags2.Flags

	return &cli.App{
		Version:              "0.0.1",
		Description:          "An  market services with rpc",
		EnableBashCompletion: true,
		Commands: []*cli.Command{
			catalogCommand(),
			{
				Name:        "migrate",
				Flags:       flags,
				Description: "Run database migrations",
				Action:      runMigrations,
			},
			{
				Name:        "rpc",
				Flags:       flags,
				Description: "Run rpc services",
				Action:      cliapp.LifecycleCmd(runRpc),
			},
			{
				Name:        "api",
				Flags:       flags,
				Description: "Run HTTP market-data API and trading gateway",
				Action:      cliapp.LifecycleCmd(runRestApi),
			},
			{
				Name:        "trading",
				Flags:       flags,
				Description: "Run isolated virtual BTC/USDT matching and ledger service",
				Action:      cliapp.LifecycleCmd(runTrading),
			},
			{
				Name:        "crawler",
				Flags:       flags,
				Description: "Run crawler services",
				Action:      cliapp.LifecycleCmd(runCrawler),
			},
			{
				Name:        "dex",
				Flags:       flags,
				Description: "Run isolated Hyperliquid and AMM market-data supervisors",
				Action:      cliapp.LifecycleCmd(runDex),
			},
			{
				Name:        "worker",
				Flags:       flags,
				Description: "Run worker business",
				Action:      cliapp.LifecycleCmd(runWorker),
			},
			{
				Name:        "dw",
				Flags:       flags,
				Description: "Run data warehouse sync (PostgreSQL -> Apache Doris)",
				Action:      cliapp.LifecycleCmd(runDw),
			},
			{
				Name:        "version",
				Description: "Show project version",
				Action: func(ctx *cli.Context) error {
					cli.ShowVersion(ctx)
					return nil
				},
			},
		},
	}
}
