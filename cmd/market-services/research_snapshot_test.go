package main

import (
	"context"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestMarketSnapshotCommandDefaultsToNoPublish(t *testing.T) {
	command := marketSnapshotCommand()
	var publishDefault bool
	for _, flag := range command.Flags {
		if flag.Names()[0] == "publish" {
			publishDefault = flag.(*cli.BoolFlag).Value
		}
		if flag.Names()[0] == "publish-url" || flag.Names()[0] == "binance-base-url" {
			t.Fatalf("provider and Preview targets must not be operator-configurable")
		}
	}
	if publishDefault {
		t.Fatal("market snapshot publishing must be disabled by default")
	}
}

func TestMarketSnapshotCommandRejectsCredentialsWithoutPublish(t *testing.T) {
	app := cli.NewApp()
	app.Commands = []*cli.Command{marketSnapshotCommand()}
	err := app.RunContext(context.Background(), []string{"market-services", "market-snapshot", "--publish-key-id", "m2-preview"})
	if err == nil || !strings.Contains(err.Error(), "without --publish") {
		t.Fatalf("expected explicit publish gate, got %v", err)
	}
}
