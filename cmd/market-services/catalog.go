package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/the-web3/s78-market-services/catalog"
	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/crawler"
	"github.com/the-web3/s78-market-services/database"
	flags2 "github.com/the-web3/s78-market-services/flags"
)

var catalogProviders = map[string]struct{}{
	"binance": {}, "coinbase": {}, "bybit": {}, "okx": {},
	"hyperliquid": {}, "uniswap": {}, "pancakeswap": {},
}

func catalogCommand() *cli.Command {
	return &cli.Command{
		Name:  "catalog",
		Usage: "audit and explicitly govern provider catalog rollout",
		Subcommands: []*cli.Command{
			{
				Name:  "apply-mappings",
				Usage: "validate or apply the code-reviewed identity manifest",
				Flags: catalogDBFlags(
					&cli.StringFlag{Name: "file"},
					&cli.BoolFlag{Name: "dry-run"},
				),
				Action: runCatalogApplyMappings,
			},
			{
				Name:  "select-assets",
				Usage: "preview or explicitly create provider selection versions",
				Flags: catalogDBFlags(
					&cli.StringFlag{Name: "provider", Required: true},
					&cli.IntFlag{Name: "limit", Value: 50},
					&cli.StringFlag{Name: "reason"},
					&cli.BoolFlag{Name: "dry-run"},
				),
				Action: runCatalogSelectAssets,
			},
			{
				Name:  "audit",
				Usage: "read provider candidate identity audit rows",
				Flags: catalogDBFlags(
					&cli.StringFlag{Name: "provider"},
					&cli.StringFlag{Name: "status"},
					&cli.IntFlag{Name: "rank-limit", Value: 50},
					&cli.Int64Flag{Name: "page", Value: 1},
					&cli.Int64Flag{Name: "page-size", Value: 100},
				),
				Action: runCatalogAudit,
			},
			{
				Name:  "rollout-status",
				Usage: "evaluate the exact promotion gate without changing it",
				Flags: catalogDBFlags(
					&cli.StringFlag{Name: "provider", Required: true},
					&cli.IntFlag{Name: "rank-limit", Value: 50},
					&cli.BoolFlag{Name: "json"},
				),
				Action: runCatalogRolloutStatus,
			},
			{
				Name:  "rollout",
				Usage: "perform one guarded provider rollout transition",
				Flags: catalogDBFlags(
					&cli.StringFlag{Name: "provider", Required: true},
					&cli.StringFlag{Name: "mode", Required: true},
					&cli.IntFlag{Name: "rank-limit", Value: 50},
					&cli.IntFlag{Name: "soak-hours"},
				),
				Action: runCatalogRollout,
			},
			{
				Name:  "preview",
				Usage: "toggle loopback-only local product preview",
				Flags: catalogDBFlags(
					&cli.StringFlag{Name: "provider", Required: true},
					&cli.BoolFlag{Name: "enable"},
					&cli.BoolFlag{Name: "disable"},
				),
				Action: runCatalogPreview,
			},
			{
				Name:  "endpoint-check",
				Usage: "validate read-only AMM endpoint identity and freshness",
				Flags: catalogEndpointFlags(
					&cli.StringFlag{Name: "provider", Required: true},
				),
				Action: runCatalogEndpointCheck,
			},
		},
	}
}

// Catalog subcommands intentionally register only the configuration they use.
// The service-wide flag set contains required HTTP/Redis flags, which would
// make a manifest-only dry run depend on unrelated runtime infrastructure.
func catalogDBFlags(extra ...cli.Flag) []cli.Flag {
	result := []cli.Flag{
		cloneCatalogStringFlag(flags2.MasterDbHostFlag),
		cloneCatalogIntFlag(flags2.MasterDbPortFlag),
		cloneCatalogStringFlag(flags2.MasterDbUserFlag),
		cloneCatalogStringFlag(flags2.MasterDbPasswordFlag),
		cloneCatalogStringFlag(flags2.MasterDbNameFlag),
	}
	return append(result, extra...)
}

func catalogEndpointFlags(extra ...cli.Flag) []cli.Flag {
	result := []cli.Flag{
		cloneCatalogStringFlag(flags2.EthereumRPCURLFlag),
		cloneCatalogStringFlag(flags2.BSCRPCURLFlag),
		cloneCatalogStringFlag(flags2.UniswapV3SubgraphURLFlag),
		cloneCatalogStringFlag(flags2.PancakeV3SubgraphURLFlag),
		cloneCatalogBoolFlag(flags2.DexPublicFallbackFlag),
	}
	return append(result, extra...)
}

func cloneCatalogStringFlag(source *cli.StringFlag) *cli.StringFlag {
	return &cli.StringFlag{
		Name: source.Name, Usage: source.Usage, Value: source.Value,
		EnvVars: append([]string(nil), source.EnvVars...),
	}
}

func cloneCatalogIntFlag(source *cli.IntFlag) *cli.IntFlag {
	return &cli.IntFlag{
		Name: source.Name, Usage: source.Usage, Value: source.Value,
		EnvVars: append([]string(nil), source.EnvVars...),
	}
}

func cloneCatalogBoolFlag(source *cli.BoolFlag) *cli.BoolFlag {
	return &cli.BoolFlag{
		Name: source.Name, Usage: source.Usage, Value: source.Value,
		EnvVars: append([]string(nil), source.EnvVars...),
	}
}

func runCatalogApplyMappings(ctx *cli.Context) error {
	path := strings.TrimSpace(ctx.String("file"))
	var (
		manifest *catalog.Manifest
		err      error
	)
	if path == "" {
		manifest, err = catalog.LoadEmbedded()
		path = "catalog/provider-asset-mappings.yaml"
	} else {
		manifest, err = catalog.LoadFile(path)
	}
	if err != nil {
		return err
	}
	if ctx.Bool("dry-run") {
		aliases, representations := manifestCounts(manifest)
		return writeCatalogJSON(ctx, map[string]any{
			"dry_run": true, "source": path, "version": manifest.Version,
			"assets": len(manifest.Assets), "aliases": aliases,
			"representations": representations,
		})
	}
	return withCatalogDB(ctx, func(db *database.DB) error {
		result, err := catalog.ApplyManifest(db, manifest, path)
		if err != nil {
			return err
		}
		return writeCatalogJSON(ctx, result)
	})
}

func runCatalogSelectAssets(ctx *cli.Context) error {
	providers, err := parseCatalogProviders(ctx.String("provider"))
	if err != nil {
		return err
	}
	limit := ctx.Int("limit")
	if limit < 1 || limit > 200 {
		return fmt.Errorf("limit must be between 1 and 200")
	}
	reason := strings.TrimSpace(ctx.String("reason"))
	if !ctx.Bool("dry-run") && reason == "" {
		return fmt.Errorf("--reason is required when creating a selection version")
	}
	return withCatalogDB(ctx, func(db *database.DB) error {
		results := make([]map[string]any, 0, len(providers))
		for _, provider := range providers {
			if ctx.Bool("dry-run") {
				eligible, queryErr := db.MarketAggregation.QueryEligibleProviderAssetIDs(provider, 200)
				if queryErr != nil {
					return queryErr
				}
				selected := eligible
				if len(selected) > limit {
					selected = selected[:limit]
				}
				results = append(results, map[string]any{
					"provider": provider, "dry_run": true,
					"candidate_count": len(eligible), "selected_count": len(selected),
					"asset_ids": selected,
				})
				continue
			}
			state, refreshErr := db.MarketAggregation.RefreshProviderAssetSelection(
				provider, limit, reason,
			)
			if refreshErr != nil {
				return refreshErr
			}
			results = append(results, map[string]any{
				"provider": provider, "dry_run": false, "state": state,
			})
		}
		return writeCatalogJSON(ctx, results)
	})
}

func runCatalogAudit(ctx *cli.Context) error {
	provider := strings.ToLower(strings.TrimSpace(ctx.String("provider")))
	if provider != "" {
		if _, ok := catalogProviders[provider]; !ok {
			return fmt.Errorf("unsupported provider %q", provider)
		}
	}
	rankLimit, err := catalogRankLimit(ctx)
	if err != nil {
		return err
	}
	page, pageSize := ctx.Int64("page"), ctx.Int64("page-size")
	if page < 1 || pageSize < 1 || pageSize > 250 {
		return fmt.Errorf("page must be positive and page-size in [1,250]")
	}
	return withCatalogDB(ctx, func(db *database.DB) error {
		rows, counts, total, err := db.MarketAggregation.QueryCatalogAudit(
			provider,
			strings.ToLower(strings.TrimSpace(ctx.String("status"))),
			rankLimit,
			page,
			pageSize,
		)
		if err != nil {
			return err
		}
		return writeCatalogJSON(ctx, map[string]any{
			"provider": provider, "page": page, "page_size": pageSize,
			"total": total, "counts": counts, "items": rows,
		})
	})
}

func runCatalogRolloutStatus(ctx *cli.Context) error {
	provider, err := singleCatalogProvider(ctx.String("provider"))
	if err != nil {
		return err
	}
	rankLimit, err := catalogRankLimit(ctx)
	if err != nil {
		return err
	}
	return withCatalogDB(ctx, func(db *database.DB) error {
		readiness, err := database.EvaluateProviderRolloutReadiness(
			db, provider, rankLimit, time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		if ctx.Bool("json") {
			return writeCatalogJSON(ctx, readiness)
		}
		writer := catalogWriter(ctx)
		_, _ = fmt.Fprintf(
			writer,
			"%s: current=%s target=%s ready=%t attempts=%d success_rate=%s%%\n",
			readiness.Provider,
			readiness.CurrentMode,
			readiness.Target,
			readiness.Ready,
			readiness.AttemptCount,
			readiness.SuccessRatePct,
		)
		for _, blocker := range readiness.Blockers {
			_, _ = fmt.Fprintf(writer, "- %s\n", blocker)
		}
		return nil
	})
}

func runCatalogRollout(ctx *cli.Context) error {
	provider, err := singleCatalogProvider(ctx.String("provider"))
	if err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(ctx.String("mode")))
	switch mode {
	case "shadow", "canary", "enabled", "paused":
	default:
		return fmt.Errorf("unsupported rollout mode %q", mode)
	}
	rankLimit, err := catalogRankLimit(ctx)
	if err != nil {
		return err
	}
	soakHours, err := catalogSoakHours(mode, ctx.Int("soak-hours"))
	if err != nil {
		return err
	}
	return withCatalogDB(ctx, func(db *database.DB) error {
		state, err := db.MarketAggregation.QueryProviderRollout(provider)
		if err != nil {
			return err
		}
		if state != nil && state.Mode == mode && state.RankLimit == rankLimit {
			return fmt.Errorf(
				"provider %s is already %s at rank limit %d; refusing to reset its observation window",
				provider, mode, rankLimit,
			)
		}

		var canaryIDs []string
		if state != nil && len(state.CanaryAssetIDs) > 0 {
			if err := json.Unmarshal(state.CanaryAssetIDs, &canaryIDs); err != nil {
				return fmt.Errorf("decode persisted canary assets: %w", err)
			}
		}
		if mode == "canary" || mode == "enabled" {
			readiness, evalErr := database.EvaluateProviderRolloutReadiness(
				db, provider, rankLimit, time.Now().UTC(),
			)
			if evalErr != nil {
				return evalErr
			}
			if readiness.Target != mode || !readiness.Ready {
				return fmt.Errorf(
					"provider %s is not ready for %s (current=%s target=%s): %s",
					provider,
					mode,
					readiness.CurrentMode,
					readiness.Target,
					strings.Join(readiness.Blockers, "; "),
				)
			}
			if mode == "canary" {
				canaryIDs = readiness.CanaryAssetIDs
			}
		}

		var minSoakUntil *time.Time
		if soakHours > 0 {
			value := time.Now().UTC().Add(time.Duration(soakHours) * time.Hour)
			minSoakUntil = &value
		}
		if err := db.MarketAggregation.SetProviderRollout(
			provider, mode, rankLimit, canaryIDs, minSoakUntil,
		); err != nil {
			return err
		}
		return writeCatalogJSON(ctx, map[string]any{
			"provider": provider, "mode": mode, "rank_limit": rankLimit,
			"canary_asset_ids": canaryIDs, "min_soak_until": minSoakUntil,
		})
	})
}

func runCatalogPreview(ctx *cli.Context) error {
	if os.Getenv("S78_LOCAL_PREVIEW") != "1" {
		return fmt.Errorf("local preview requires S78_LOCAL_PREVIEW=1")
	}
	if ctx.Bool("enable") == ctx.Bool("disable") {
		return fmt.Errorf("exactly one of --enable or --disable is required")
	}
	cfg := config.NewConfig(ctx)
	if !localDatabaseHost(cfg.MasterDB.Host) {
		return fmt.Errorf("local preview requires a loopback PostgreSQL host")
	}
	providers, err := parseCatalogProviders(ctx.String("provider"))
	if err != nil {
		return err
	}
	enabled := ctx.Bool("enable")
	return withCatalogDB(ctx, func(db *database.DB) error {
		for _, provider := range providers {
			if err := db.MarketAggregation.SetProviderLocalPreview(provider, enabled); err != nil {
				return err
			}
		}
		return writeCatalogJSON(ctx, map[string]any{
			"providers": providers, "local_preview_enabled": enabled,
		})
	})
}

func runCatalogEndpointCheck(ctx *cli.Context) error {
	provider, err := singleCatalogProvider(ctx.String("provider"))
	if err != nil {
		return err
	}
	cfg := config.NewConfig(ctx)
	rpcURL, subgraphURL := "", ""
	switch provider {
	case "uniswap":
		rpcURL, subgraphURL = cfg.EthereumRPCURL, cfg.UniswapV3SubgraphURL
	case "pancakeswap":
		rpcURL, subgraphURL = cfg.BSCRPCURL, cfg.PancakeV3SubgraphURL
	default:
		return fmt.Errorf("endpoint-check supports uniswap or pancakeswap")
	}
	result, err := crawler.CheckAMMEndpoints(
		ctx.Context, provider, rpcURL, subgraphURL, cfg.DexPublicFallback,
	)
	if err != nil {
		return err
	}
	return writeCatalogJSON(ctx, map[string]any{
		"provider": result.Provider, "chain_id": result.ChainID,
		"latest_block": result.LatestBlock, "block_age": result.BlockAge.String(),
		"subgraph_block":    result.SubgraphBlock,
		"sample_pool_count": result.SamplePoolCount,
		"discovery_source":  result.DiscoverySource,
	})
}

func withCatalogDB(ctx *cli.Context, fn func(*database.DB) error) error {
	dbConfig, err := catalogDatabaseConfig(ctx)
	if err != nil {
		return err
	}
	db, err := database.NewDB(ctx.Context, dbConfig)
	if err != nil {
		return err
	}
	runErr := fn(db)
	closeErr := db.Close()
	if runErr != nil {
		return runErr
	}
	return closeErr
}

func catalogDatabaseConfig(ctx *cli.Context) (config.DBConfig, error) {
	dbConfig := config.NewConfig(ctx).MasterDB
	missing := make([]string, 0, 2)
	if strings.TrimSpace(dbConfig.Host) == "" {
		missing = append(missing, "--"+flags2.MasterDbHostFlag.Name)
	}
	if strings.TrimSpace(dbConfig.Name) == "" {
		missing = append(missing, "--"+flags2.MasterDbNameFlag.Name)
	}
	if len(missing) > 0 {
		return config.DBConfig{}, fmt.Errorf(
			"catalog database configuration requires %s",
			strings.Join(missing, " and "),
		)
	}
	if dbConfig.Port < 0 || dbConfig.Port > 65535 {
		return config.DBConfig{}, fmt.Errorf("master database port must be in [0,65535]")
	}
	return dbConfig, nil
}

func parseCatalogProviders(value string) ([]string, error) {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		provider := strings.ToLower(strings.TrimSpace(item))
		if provider == "" {
			continue
		}
		if _, ok := catalogProviders[provider]; !ok {
			return nil, fmt.Errorf("unsupported provider %q", provider)
		}
		if _, duplicate := seen[provider]; duplicate {
			continue
		}
		seen[provider] = struct{}{}
		result = append(result, provider)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("at least one provider is required")
	}
	return result, nil
}

func singleCatalogProvider(value string) (string, error) {
	providers, err := parseCatalogProviders(value)
	if err != nil {
		return "", err
	}
	if len(providers) != 1 {
		return "", fmt.Errorf("this command requires exactly one provider")
	}
	return providers[0], nil
}

func catalogRankLimit(ctx *cli.Context) (int, error) {
	rankLimit := ctx.Int("rank-limit")
	if rankLimit < 1 || rankLimit > 200 {
		return 0, fmt.Errorf("rank-limit must be between 1 and 200")
	}
	return rankLimit, nil
}

func catalogSoakHours(mode string, requested int) (int, error) {
	if requested < 0 {
		return 0, fmt.Errorf("soak-hours must not be negative")
	}
	minimum := 0
	switch mode {
	case "canary":
		minimum = 24
	case "enabled":
		minimum = 48
	}
	if requested == 0 {
		return minimum, nil
	}
	if requested < minimum {
		return 0, fmt.Errorf("%s rollout requires at least %d soak hours", mode, minimum)
	}
	return requested, nil
}

func manifestCounts(manifest *catalog.Manifest) (int, int) {
	aliases := 0
	representations := 0
	for _, quote := range manifest.QuoteAssets {
		aliases += len(quote.Providers)
	}
	for _, asset := range manifest.Assets {
		for _, values := range asset.Aliases {
			aliases += len(values)
		}
		representations += len(asset.Representations)
	}
	return aliases, representations
}

func localDatabaseHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func catalogWriter(ctx *cli.Context) io.Writer {
	if ctx != nil && ctx.App != nil && ctx.App.Writer != nil {
		return ctx.App.Writer
	}
	return os.Stdout
}

func writeCatalogJSON(ctx *cli.Context, value any) error {
	encoder := json.NewEncoder(catalogWriter(ctx))
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
