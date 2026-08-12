package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/the-web3/s78-market-services/marketdata/researchsnapshot"
)

func marketSnapshotCommand() *cli.Command {
	return &cli.Command{
		Name:        "market-snapshot",
		Description: "Capture the fixed 21-asset research snapshot once and optionally publish it to xiuqiu-site",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "publish-key-id", EnvVars: []string{"MARKET_RESEARCH_PUBLISH_KEY_ID"}},
			&cli.StringFlag{Name: "publish-secret", EnvVars: []string{"MARKET_RESEARCH_PUBLISH_SECRET"}},
			&cli.BoolFlag{Name: "publish", Value: false, EnvVars: []string{"MARKET_RESEARCH_PUBLISH"}},
		},
		Action: runMarketSnapshot,
	}
}

func runMarketSnapshot(ctx *cli.Context) error {
	requestCtx, cancel := context.WithTimeout(ctx.Context, 25*time.Second)
	defer cancel()
	now := time.Now().UTC()
	snapshot, err := researchsnapshot.Capture(requestCtx, &http.Client{Timeout: 8 * time.Second}, now)
	if err != nil {
		return err
	}
	if ctx.Bool("publish") {
		publisher := researchsnapshot.Publisher{
			URL: researchsnapshot.DefaultPreviewIngestURL, KeyID: ctx.String("publish-key-id"), Secret: ctx.String("publish-secret"),
		}
		if err := publisher.Publish(requestCtx, snapshot); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "published market snapshot %s at %s\n", snapshot.SnapshotID, snapshot.AsOf)
		return nil
	}
	if strings.TrimSpace(ctx.String("publish-key-id")) != "" || strings.TrimSpace(ctx.String("publish-secret")) != "" {
		return errors.New("publishing credentials were supplied without --publish")
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(snapshot)
}
