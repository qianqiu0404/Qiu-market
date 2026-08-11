package rest

import (
	"net/http"
	"regexp"
	"strings"
)

const (
	MarketReadContractSchema = "qiu.market-read-contract.v1"
	MarketReadDataMode       = "live"
	MarketReadProviderPolicy = "restricted-no-bypass.v1"
	MarketReadSnapshotSchema = "qiu.market-snapshot.v1"
)

var exactReleaseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type MarketReadContract struct {
	ReleaseCommit  string
	DataMode       string
	ProviderPolicy string
	ContractSchema string
	SnapshotSchema string
}

func NewMarketReadContract(releaseCommit string) MarketReadContract {
	releaseCommit = strings.ToLower(strings.TrimSpace(releaseCommit))
	if !exactReleaseCommitPattern.MatchString(releaseCommit) {
		releaseCommit = ""
	}
	return MarketReadContract{
		ReleaseCommit: releaseCommit, DataMode: MarketReadDataMode,
		ProviderPolicy: MarketReadProviderPolicy, ContractSchema: MarketReadContractSchema,
		SnapshotSchema: MarketReadSnapshotSchema,
	}
}

func marketReadContractMiddleware(contract MarketReadContract) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if contract.ReleaseCommit != "" {
				w.Header().Set("X-Qiu-Market-Backend-Release-Commit", contract.ReleaseCommit)
			}
			w.Header().Set("X-Qiu-Market-Data-Mode", contract.DataMode)
			w.Header().Set("X-Qiu-Market-Provider-Policy", contract.ProviderPolicy)
			w.Header().Set("X-Qiu-Market-Contract-Schema", contract.ContractSchema)
			w.Header().Set("X-Qiu-Market-Snapshot-Schema", contract.SnapshotSchema)
			if nonce := strings.TrimSpace(r.Header.Get(publicProxyNonceHeader)); nonce != "" {
				w.Header().Set("X-Qiu-Market-Backend-Request-Nonce", nonce)
			}
			next.ServeHTTP(w, r)
		})
	}
}
