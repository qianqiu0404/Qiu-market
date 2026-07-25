package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedManifestIsUnambiguous(t *testing.T) {
	manifest, err := LoadEmbedded()
	require.NoError(t, err)
	require.Equal(t, 1, manifest.Version)
	require.Len(t, manifest.Assets, 83)
	var tether *ManifestAsset
	for index := range manifest.Assets {
		if manifest.Assets[index].CoinGeckoID == "tether" {
			tether = &manifest.Assets[index]
			break
		}
	}
	require.NotNil(t, tether)
	require.Equal(t, "a3", tether.CanonicalAssetID)
}

func TestEmbeddedManifestCanSeedFourIndependentSelections(t *testing.T) {
	manifest, err := LoadEmbedded()
	require.NoError(t, err)

	aliases := map[string]map[string]struct{}{
		"binance": {}, "coinbase": {}, "bybit": {}, "okx": {},
	}
	for _, asset := range manifest.Assets {
		for provider, values := range asset.Aliases {
			target, tracked := aliases[provider]
			if !tracked {
				continue
			}
			for _, value := range values {
				target[value] = struct{}{}
			}
		}
	}
	for provider, values := range aliases {
		require.GreaterOrEqual(t, len(values), 50, provider)
	}
	require.Contains(t, aliases["coinbase"], "USD1")
	require.Contains(t, aliases["coinbase"], "WLFI")
}

func TestManifestRejectsAliasAndContractCollisions(t *testing.T) {
	manifest := Manifest{Version: 1, Assets: []ManifestAsset{
		{CoinGeckoID: "one", Aliases: map[string][]string{"coinbase": {"ABC"}}},
		{CoinGeckoID: "two", Aliases: map[string][]string{"coinbase": {"ABC"}}},
	}}
	require.ErrorContains(t, manifest.Validate(), "maps to both")

	manifest = Manifest{Version: 1, Assets: []ManifestAsset{
		{
			CoinGeckoID: "one",
			Representations: []ManifestRepresentation{{
				ChainID: 1, ContractAddress: "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2",
			}},
		},
		{
			CoinGeckoID: "two",
			Representations: []ManifestRepresentation{{
				ChainID: 1, ContractAddress: "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2",
			}},
		},
	}}
	require.ErrorContains(t, manifest.Validate(), "maps to both")
}

func TestLoadFileUsesTheReviewedManifestContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reviewed.yaml")
	require.NoError(t, os.WriteFile(path, embeddedProviderManifest, 0o600))
	manifest, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, 1, manifest.Version)
	require.Len(t, manifest.Assets, 83)

	_, err = LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorContains(t, err, "read catalog manifest")
}
