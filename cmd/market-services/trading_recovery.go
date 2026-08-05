package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/the-web3/s78-market-services/trading/domain"
	tradingoperator "github.com/the-web3/s78-market-services/trading/operator"
	"github.com/the-web3/s78-market-services/trading/recovery"
)

func tradingRecoveryCommand() *cli.Command {
	return &cli.Command{
		Name:  "trading-recovery",
		Usage: "inspect or promote the fail-closed virtual trading recovery epoch",
		Subcommands: []*cli.Command{
			{
				Name:  "status",
				Usage: "read the exact current recovery binding without changing it",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "market", Value: "BTC-USDT"},
					&cli.StringFlag{Name: "grpc-address", Value: "127.0.0.1:9094"},
				},
				Action: runTradingRecoveryStatus,
			},
			{
				Name:  "promote",
				Usage: "observe exact HTTP/gRPC state continuously, then CAS-promote it",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "market", Value: "BTC-USDT"},
					&cli.StringFlag{Name: "epoch", Required: true},
					&cli.Uint64Flag{Name: "version", Required: true},
					&cli.Uint64Flag{Name: "runtime-sequence", Required: true},
					&cli.StringFlag{Name: "state-hash", Required: true},
					&cli.StringFlag{Name: "status-url", Required: true},
					&cli.StringFlag{Name: "production-origin", Required: true},
					&cli.StringFlag{Name: "deployment-id", Required: true},
					&cli.StringFlag{Name: "deployment-url", Required: true},
					&cli.StringFlag{Name: "release-commit", Required: true},
					&cli.StringFlag{Name: "source-digest", Required: true},
					&cli.StringFlag{Name: "grpc-address", Value: "127.0.0.1:9094"},
					&cli.IntFlag{Name: "minimum-samples", Value: recovery.MinimumTransportSamples},
					&cli.DurationFlag{Name: "minimum-window", Value: recovery.MinimumTransportWindow},
					&cli.DurationFlag{Name: "sample-every", Value: 5 * time.Second},
					&cli.DurationFlag{Name: "maximum-gap", Value: recovery.MaximumTransportGap},
					&cli.DurationFlag{Name: "probe-timeout", Value: 3 * time.Second},
				},
				Action: runTradingRecoveryPromote,
			},
		},
	}
}

func runTradingRecoveryStatus(ctx *cli.Context) error {
	connectContext, cancel := context.WithTimeout(ctx.Context, 5*time.Second)
	client, err := tradingoperator.DialRecoveryClient(
		connectContext,
		strings.TrimSpace(ctx.String("grpc-address")),
	)
	cancel()
	if err != nil {
		return err
	}
	defer client.Close()
	status, err := client.Status(
		ctx.Context,
		domain.MarketID(strings.TrimSpace(ctx.String("market"))),
	)
	if err != nil {
		return err
	}
	return writeTradingRecoveryJSON(ctx, status)
}

func runTradingRecoveryPromote(ctx *cli.Context) error {
	binding := recovery.Binding{
		MarketID:        domain.MarketID(strings.TrimSpace(ctx.String("market"))),
		EpochID:         strings.TrimSpace(ctx.String("epoch")),
		Version:         ctx.Uint64("version"),
		RuntimeSequence: ctx.Uint64("runtime-sequence"),
		StateHash:       strings.ToLower(strings.TrimSpace(ctx.String("state-hash"))),
		Provenance: recovery.Provenance{
			ProductionOrigin: strings.TrimSpace(ctx.String("production-origin")),
			DeploymentID:     strings.TrimSpace(ctx.String("deployment-id")),
			DeploymentURL:    strings.TrimSpace(ctx.String("deployment-url")),
			ReleaseCommit:    strings.TrimSpace(ctx.String("release-commit")),
			SourceDigest:     strings.TrimSpace(ctx.String("source-digest")),
		},
	}
	if binding.MarketID == "" || binding.EpochID == "" || binding.Version == 0 ||
		len(binding.StateHash) != 64 {
		return fmt.Errorf("market, epoch, positive version and 64-character state hash are required")
	}
	if _, err := hex.DecodeString(binding.StateHash); err != nil {
		return fmt.Errorf("state hash must be 64 lowercase hexadecimal characters")
	}
	var err error
	binding.Provenance, err = recovery.NormalizeProvenance(binding.Provenance)
	if err != nil {
		return err
	}
	policy := tradingoperator.ObservationPolicy{
		MinimumSamples: ctx.Int("minimum-samples"),
		MinimumWindow:  ctx.Duration("minimum-window"),
		SampleEvery:    ctx.Duration("sample-every"),
		MaximumGap:     ctx.Duration("maximum-gap"),
		ProbeTimeout:   ctx.Duration("probe-timeout"),
	}
	connectContext, cancel := context.WithTimeout(ctx.Context, 5*time.Second)
	probe, err := tradingoperator.NewTransportProbe(
		connectContext,
		binding,
		strings.TrimSpace(ctx.String("status-url")),
		strings.TrimSpace(ctx.String("grpc-address")),
		nil,
	)
	cancel()
	if err != nil {
		return err
	}
	defer probe.Close()
	evidence, samples, err := tradingoperator.CollectTransportEvidence(
		ctx.Context,
		binding,
		probe,
		policy,
	)
	if err != nil {
		return fmt.Errorf("transport proof failed closed: %w", err)
	}
	promoted, err := probe.Promote(ctx.Context, evidence)
	if err != nil {
		return fmt.Errorf("recovery promotion failed closed: %w", err)
	}
	return writeTradingRecoveryJSON(ctx, map[string]any{
		"status":             promoted,
		"transport_evidence": evidence,
		"samples":            samples,
	})
}

func writeTradingRecoveryJSON(ctx *cli.Context, value any) error {
	encoder := json.NewEncoder(catalogWriter(ctx))
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
