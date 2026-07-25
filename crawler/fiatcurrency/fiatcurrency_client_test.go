package fiatcurrency

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExchangeRateAPIParserPreservesProviderTimestamp(t *testing.T) {
	payload, err := exchangeRateAPIResponseParser([]byte(`{
		"result":"success",
		"time_last_update_unix":1784860800,
		"conversion_rates":{"CNY":7.2,"HKD":7.8}
	}`), []string{"CNY"})
	require.NoError(t, err)
	require.Equal(t, 7.2, payload.Rates["CNY"])
	require.NotNil(t, payload.SourceTime)
	require.Equal(t, time.Unix(1784860800, 0).UTC(), *payload.SourceTime)
}
