package crawler

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/the-web3/s78-market-services/database"
)

func TestValidateBinanceCatalogAllowsReviewedExpansion(t *testing.T) {
	rows := make([]database.ProviderMarket, 0, len(legacyBinanceCatalog))
	for source, guid := range legacyBinanceCatalog {
		rows = append(rows, database.ProviderMarket{
			SourceSymbol: source,
			SymbolGuid:   guid,
			MarketType:   "spot",
			MarketCode:   "binance:" + source + ":spot",
		})
	}
	validated, err := validateBinanceCatalog(rows)
	require.NoError(t, err)
	require.Len(t, validated, len(legacyBinanceCatalog))

	expanded := append(rows, database.ProviderMarket{
		SourceSymbol: "ADAUSDT", SymbolGuid: "s-ada", MarketType: "spot",
		MarketCode: "binance:ADA/USDT:spot",
	})
	validated, err = validateBinanceCatalog(expanded)
	require.NoError(t, err)
	require.Len(t, validated, len(legacyBinanceCatalog)+1)

	duplicate := append([]database.ProviderMarket(nil), rows...)
	duplicate[1].SourceSymbol = duplicate[0].SourceSymbol
	_, err = validateBinanceCatalog(duplicate)
	require.ErrorContains(t, err, "duplicated")
}
