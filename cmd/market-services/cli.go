package main

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/log"
	"github.com/urfave/cli/v2"

	"github.com/the-web3/s78-market-services/common/cliapp"
	"github.com/the-web3/s78-market-services/common/opio"
	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
	flags2 "github.com/the-web3/s78-market-services/flags"
)

func runRpc(ctx *cli.Context, shutdown context.CancelCauseFunc) (cliapp.Lifecycle, error) {
	fmt.Println("running grpc services...")
	return nil, nil
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
	fmt.Println("running rest api...")
	return nil, nil
}

func NewCli(GitCommit string, GitData string) *cli.App {
	flags := flags2.Flags

	return &cli.App{
		Version:              "0.0.1",
		Description:          "An  market services with rpc",
		EnableBashCompletion: true,
		Commands: []*cli.Command{
			{
				Name:        "migrate",
				Flags:       flags,
				Description: "Run database migrations",
				Action:      runMigrations,
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
