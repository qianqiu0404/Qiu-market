package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func TestCatalogCommandExposesDocumentedOperations(t *testing.T) {
	command := catalogCommand()
	names := make([]string, 0, len(command.Subcommands))
	for _, subcommand := range command.Subcommands {
		names = append(names, subcommand.Name)
	}

	require.ElementsMatch(t, []string{
		"apply-mappings",
		"select-assets",
		"audit",
		"rollout-status",
		"rollout",
		"preview",
		"endpoint-check",
	}, names)
}

func TestCatalogApplyMappingsDryRunDoesNotRequireRuntimeInfrastructure(t *testing.T) {
	output, err := runCatalogTestApp(
		t, "catalog", "apply-mappings", "--dry-run",
	)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &result))
	require.Equal(t, true, result["dry_run"])
	require.Equal(t, float64(1), result["version"])
	require.Equal(t, "catalog/provider-asset-mappings.yaml", result["source"])
	require.NotZero(t, result["aliases"])
}

func TestCatalogDatabaseActionFailsClosedWhenConfigIsMissing(t *testing.T) {
	t.Setenv("MARKET_MASTER_DB_HOST", "")
	t.Setenv("MARKET_MASTER_DB_NAME", "")

	_, err := runCatalogTestApp(t, "catalog", "audit")

	require.Error(t, err)
	require.ErrorContains(t, err, "--master-db-host")
	require.ErrorContains(t, err, "--master-db-name")
}

func TestCatalogPreviewChecksExplicitGuardBeforeDatabase(t *testing.T) {
	t.Setenv("S78_LOCAL_PREVIEW", "")

	_, err := runCatalogTestApp(
		t, "catalog", "preview", "--provider", "binance", "--enable",
	)

	require.EqualError(t, err, "local preview requires S78_LOCAL_PREVIEW=1")
}

func TestCatalogEndpointCheckRejectsCEXWithoutNetwork(t *testing.T) {
	_, err := runCatalogTestApp(
		t, "catalog", "endpoint-check", "--provider", "binance",
	)

	require.EqualError(t, err, "endpoint-check supports uniswap or pancakeswap")
}

func TestParseCatalogProvidersNormalizesAndDeduplicates(t *testing.T) {
	providers, err := parseCatalogProviders(" Binance,coinbase,binance ")

	require.NoError(t, err)
	require.Equal(t, []string{"binance", "coinbase"}, providers)

	_, err = parseCatalogProviders("binance,unknown")
	require.EqualError(t, err, `unsupported provider "unknown"`)
}

func TestCatalogSoakHoursEnforcesFormalMinimums(t *testing.T) {
	hours, err := catalogSoakHours("canary", 0)
	require.NoError(t, err)
	require.Equal(t, 24, hours)

	hours, err = catalogSoakHours("enabled", 72)
	require.NoError(t, err)
	require.Equal(t, 72, hours)

	_, err = catalogSoakHours("enabled", 47)
	require.EqualError(t, err, "enabled rollout requires at least 48 soak hours")

	_, err = catalogSoakHours("paused", -1)
	require.EqualError(t, err, "soak-hours must not be negative")
}

func TestLocalDatabaseHostAllowsOnlyLoopback(t *testing.T) {
	require.True(t, localDatabaseHost("localhost"))
	require.True(t, localDatabaseHost("127.0.0.1"))
	require.True(t, localDatabaseHost("[::1]"))
	require.False(t, localDatabaseHost("postgres.internal"))
	require.False(t, localDatabaseHost("0.0.0.0"))
}

func runCatalogTestApp(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var output bytes.Buffer
	app := &cli.App{
		Commands:  []*cli.Command{catalogCommand()},
		Writer:    &output,
		ErrWriter: &output,
	}
	argv := append([]string{"market-services"}, args...)
	err := app.RunContext(context.Background(), argv)
	return output.String(), err
}
