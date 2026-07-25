package catalog

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"gopkg.in/yaml.v3"

	"github.com/the-web3/s78-market-services/database"
)

//go:embed provider-asset-mappings.yaml
var embeddedProviderManifest []byte

type Manifest struct {
	Version     int                  `yaml:"version"`
	QuoteAssets []ManifestQuoteAsset `yaml:"quote_assets"`
	Assets      []ManifestAsset      `yaml:"assets"`
}

type ManifestQuoteAsset struct {
	Symbol        string   `yaml:"symbol"`
	Name          string   `yaml:"name"`
	Providers     []string `yaml:"providers"`
	CanonicalKind string   `yaml:"canonical_kind"`
}

type ManifestAsset struct {
	CoinGeckoID      string                   `yaml:"coingecko_id"`
	CanonicalAssetID string                   `yaml:"canonical_asset_id"`
	Aliases          map[string][]string      `yaml:"aliases"`
	Representations  []ManifestRepresentation `yaml:"representations"`
}

type ManifestRepresentation struct {
	ChainID         int64  `yaml:"chain_id"`
	ContractAddress string `yaml:"contract_address"`
	TokenSymbol     string `yaml:"token_symbol"`
	Decimals        int    `yaml:"decimals"`
	Kind            string `yaml:"kind"`
	Source          string `yaml:"source"`
	Note            string `yaml:"note"`
}

type ApplyResult struct {
	AliasCount          int
	RepresentationCount int
	MissingAssetIDs     []string
}

func LoadEmbedded() (*Manifest, error) {
	return decodeManifest(embeddedProviderManifest, "embedded provider asset manifest")
}

func LoadFile(path string) (*Manifest, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("catalog manifest file is required")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog manifest %s: %w", path, err)
	}
	return decodeManifest(payload, path)
}

func decodeManifest(payload []byte, source string) (*Manifest, error) {
	var manifest Manifest
	if err := yaml.Unmarshal(payload, &manifest); err != nil {
		return nil, fmt.Errorf("decode catalog manifest %s: %w", source, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (m *Manifest) Validate() error {
	if m.Version != 1 {
		return fmt.Errorf("unsupported catalog manifest version %d", m.Version)
	}
	seenExternalIDs := make(map[string]struct{})
	seenAliases := make(map[string]string)
	seenContracts := make(map[string]string)
	for _, quote := range m.QuoteAssets {
		symbol := strings.ToUpper(strings.TrimSpace(quote.Symbol))
		if symbol != "USD" || quote.CanonicalKind != "fiat" {
			return fmt.Errorf("unsupported reviewed quote asset %q (%s)", symbol, quote.CanonicalKind)
		}
		for _, provider := range quote.Providers {
			switch strings.ToLower(strings.TrimSpace(provider)) {
			case "binance", "coinbase", "bybit", "okx":
			default:
				return fmt.Errorf("unsupported quote provider %q", provider)
			}
		}
	}
	for _, asset := range m.Assets {
		externalID := strings.TrimSpace(asset.CoinGeckoID)
		if externalID == "" {
			return fmt.Errorf("catalog manifest contains an empty coingecko_id")
		}
		if _, exists := seenExternalIDs[externalID]; exists {
			return fmt.Errorf("duplicate coingecko_id %q", externalID)
		}
		seenExternalIDs[externalID] = struct{}{}
		if strings.ContainsAny(strings.TrimSpace(asset.CanonicalAssetID), " \t\r\n") {
			return fmt.Errorf("canonical_asset_id for %s contains whitespace", externalID)
		}
		for provider, aliases := range asset.Aliases {
			provider = strings.ToLower(strings.TrimSpace(provider))
			switch provider {
			case "binance", "coinbase", "bybit", "okx", "hyperliquid":
			default:
				return fmt.Errorf("unsupported alias provider %q", provider)
			}
			for _, alias := range aliases {
				key := provider + ":" + strings.ToUpper(strings.TrimSpace(alias))
				if previous, exists := seenAliases[key]; exists && previous != externalID {
					return fmt.Errorf("alias %s maps to both %s and %s", key, previous, externalID)
				}
				seenAliases[key] = externalID
			}
		}
		for _, representation := range asset.Representations {
			if representation.ChainID != 1 && representation.ChainID != 56 {
				return fmt.Errorf("unsupported chain_id %d for %s", representation.ChainID, externalID)
			}
			if !common.IsHexAddress(representation.ContractAddress) {
				return fmt.Errorf("invalid contract address %q for %s", representation.ContractAddress, externalID)
			}
			if representation.Decimals < 0 || representation.Decimals > 36 {
				return fmt.Errorf("invalid token decimals for %s", externalID)
			}
			key := fmt.Sprintf("%d:%s", representation.ChainID, strings.ToLower(representation.ContractAddress))
			if previous, exists := seenContracts[key]; exists && previous != externalID {
				return fmt.Errorf("contract %s maps to both %s and %s", key, previous, externalID)
			}
			seenContracts[key] = externalID
		}
	}
	return nil
}

// ApplyEmbedded writes only identities that exist in the current CoinGecko
// external mapping. Missing assets are reported, not invented.
func ApplyEmbedded(db *database.DB) (ApplyResult, error) {
	manifest, err := LoadEmbedded()
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyManifest(db, manifest, "catalog/provider-asset-mappings.yaml")
}

// ApplyManifest writes only reviewed identities from the caller-provided
// manifest. The source is retained in every review row so operators can audit
// exactly which file authorized an alias or chain representation.
func ApplyManifest(db *database.DB, manifest *Manifest, source string) (ApplyResult, error) {
	if manifest == nil {
		return ApplyResult{}, fmt.Errorf("catalog manifest is nil")
	}
	if err := manifest.Validate(); err != nil {
		return ApplyResult{}, err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return ApplyResult{}, fmt.Errorf("catalog manifest source is required")
	}
	mappings, err := db.ExchangeSymbol.QueryAssetExternalMappings("coingecko")
	if err != nil {
		return ApplyResult{}, err
	}
	assetByExternalID := make(map[string]string, len(mappings))
	for _, mapping := range mappings {
		assetByExternalID[mapping.ExternalID] = mapping.AssetGuid
	}
	now := time.Now().UTC()
	reviewer := "top50-manifest-v1"
	aliases := make([]database.AssetAlias, 0)
	representations := make([]database.AssetRepresentation, 0)
	result := ApplyResult{}
	existingAssets, err := db.Asset.QueryAssets()
	if err != nil {
		return result, err
	}
	for _, quote := range manifest.QuoteAssets {
		symbol := strings.ToUpper(strings.TrimSpace(quote.Symbol))
		matches := make([]*database.Asset, 0, 1)
		for _, item := range existingAssets {
			if strings.EqualFold(item.AssetSymbol, symbol) {
				matches = append(matches, item)
			}
		}
		if len(matches) > 1 {
			return result, fmt.Errorf("reviewed quote asset %s has %d canonical candidates", symbol, len(matches))
		}
		assetID := ""
		if len(matches) == 1 {
			assetID = matches[0].Guid
		} else {
			assetID = "fiat-" + strings.ToLower(symbol)
			if err := db.Asset.StoreAsset(&database.Asset{
				Guid: assetID, AssetName: quote.Name, AssetSymbol: symbol,
				AssetLogo: "", IsActive: true,
			}); err != nil {
				return result, fmt.Errorf("create reviewed quote asset %s: %w", symbol, err)
			}
		}
		for _, provider := range quote.Providers {
			note := "Reviewed canonical quote asset " + symbol
			aliases = append(aliases, database.AssetAlias{
				Provider: strings.ToLower(provider), Alias: symbol, AssetGuid: assetID,
				ReviewStatus: "approved", ReviewedBy: &reviewer, ReviewedAt: &now,
				ReviewSource: &source, ReviewNote: &note,
				CreatedAt: now, UpdatedAt: now,
			})
		}
	}
	for _, asset := range manifest.Assets {
		assetID := assetByExternalID[asset.CoinGeckoID]
		if assetID == "" {
			result.MissingAssetIDs = append(result.MissingAssetIDs, asset.CoinGeckoID)
			continue
		}
		for provider, names := range asset.Aliases {
			for _, name := range names {
				note := "Reviewed provider alias for CoinGecko asset " + asset.CoinGeckoID
				aliases = append(aliases, database.AssetAlias{
					Provider: strings.ToLower(provider), Alias: strings.ToUpper(strings.TrimSpace(name)),
					AssetGuid: assetID, ReviewStatus: "approved",
					ReviewedBy: &reviewer, ReviewedAt: &now,
					ReviewSource: &source, ReviewNote: &note,
					CreatedAt: now, UpdatedAt: now,
				})
			}
		}
		for _, item := range asset.Representations {
			reviewSource := item.Source
			reviewNote := item.Note
			representations = append(representations, database.AssetRepresentation{
				AssetGuid: assetID, ChainID: item.ChainID,
				ContractAddress:    strings.ToLower(item.ContractAddress),
				RepresentationKind: item.Kind, TokenSymbol: strings.ToUpper(item.TokenSymbol),
				Decimals: item.Decimals, ReviewStatus: "approved",
				ReviewSource: &reviewSource, ReviewNote: &reviewNote,
				ReviewedAt: &now, CreatedAt: now, UpdatedAt: now,
			})
		}
	}
	if err := db.MarketAggregation.ApplyReviewedAssetAliases(aliases); err != nil {
		return result, err
	}
	if err := db.MarketAggregation.UpsertAssetRepresentations(representations); err != nil {
		return result, err
	}
	result.AliasCount = len(aliases)
	result.RepresentationCount = len(representations)
	return result, nil
}
