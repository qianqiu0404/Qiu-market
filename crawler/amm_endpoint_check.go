package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

type AMMEndpointCheckResult struct {
	Provider        string
	ChainID         int64
	LatestBlock     uint64
	BlockAge        time.Duration
	SubgraphBlock   int64
	SamplePoolCount int
	DiscoverySource string
}

// CheckAMMEndpoints validates only public, read-only behavior. Errors are
// deliberately sanitized so a managed endpoint or embedded API key never
// reaches command output or service logs.
func CheckAMMEndpoints(
	ctx context.Context,
	provider, rpcURL, subgraphURL string,
	publicFallback ...bool,
) (*AMMEndpointCheckResult, error) {
	config, err := endpointCheckConfig(provider)
	if err != nil {
		return nil, err
	}
	if err := validateAMMProviderConfig(config); err != nil {
		return nil, err
	}
	usePublicFallback := len(publicFallback) > 0 && publicFallback[0]
	if usePublicFallback {
		if strings.TrimSpace(rpcURL) == "" {
			if config.ChainID == 1 {
				rpcURL = publicEthereumRPCURL
			} else {
				rpcURL = publicBSCRPCURL
			}
		}
		config.PublicDiscovery = strings.TrimSpace(subgraphURL) == ""
		if config.PublicDiscovery {
			config.DiscoveryURL = dexScreenerAPIURL
		}
	}
	if strings.TrimSpace(rpcURL) == "" ||
		(strings.TrimSpace(subgraphURL) == "" && !config.PublicDiscovery) {
		return nil, fmt.Errorf("%s endpoints are unconfigured", config.Provider)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(checkCtx, strings.TrimSpace(rpcURL))
	if err != nil {
		return nil, fmt.Errorf("%s RPC connection failed", config.Provider)
	}
	defer client.Close()
	chainID, err := client.ChainID(checkCtx)
	if err != nil || chainID.Int64() != config.ChainID {
		return nil, fmt.Errorf("%s RPC chain identity mismatch", config.Provider)
	}
	header, err := client.HeaderByNumber(checkCtx, nil)
	if err != nil || header == nil {
		return nil, fmt.Errorf("%s RPC latest block unavailable", config.Provider)
	}
	blockAt := time.Unix(int64(header.Time), 0).UTC()
	blockAge := time.Since(blockAt)
	if blockAge < -5*time.Second || blockAge > 60*time.Second {
		return nil, fmt.Errorf("%s RPC latest block is stale", config.Provider)
	}
	for label, address := range map[string]string{
		"V2 Factory": config.V2FactoryAddress,
		"V2 Router":  config.V2RouterAddress,
		"V3 Factory": config.V3FactoryAddress,
		"QuoterV2":   config.V3QuoterAddress,
	} {
		code, codeErr := client.CodeAt(checkCtx, common.HexToAddress(address), nil)
		if codeErr != nil || len(code) == 0 {
			return nil, fmt.Errorf("%s %s contract code unavailable", config.Provider, label)
		}
	}

	discoverySource := "subgraph"
	subgraphBlock := int64(0)
	poolCount := 0
	if config.PublicDiscovery {
		discoverySource = "dexscreener-onchain-verified"
		poolCount, err = checkPublicAMMDiscovery(
			checkCtx, http.DefaultClient, config,
		)
		if err != nil {
			return nil, fmt.Errorf("%s public discovery validation failed", config.Provider)
		}
	} else {
		subgraphBlock, poolCount, err = checkAMMSubgraph(
			checkCtx, http.DefaultClient, strings.TrimSpace(subgraphURL),
		)
		if err != nil {
			return nil, fmt.Errorf("%s Subgraph validation failed", config.Provider)
		}
	}
	return &AMMEndpointCheckResult{
		Provider: config.Provider, ChainID: config.ChainID,
		LatestBlock: header.Number.Uint64(), BlockAge: blockAge,
		SubgraphBlock: subgraphBlock, SamplePoolCount: poolCount,
		DiscoverySource: discoverySource,
	}, nil
}

func checkPublicAMMDiscovery(
	ctx context.Context,
	client *http.Client,
	config ammProviderConfig,
) (int, error) {
	chain := "ethereum"
	if config.ChainID == 56 {
		chain = "bsc"
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf(
			"%s/%s/%s", publicDiscoveryBaseURL(config), chain,
			config.StableAddress,
		),
		nil,
	)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var pairs []dexScreenerPair
	if err := json.NewDecoder(response.Body).Decode(&pairs); err != nil {
		return 0, err
	}
	count := 0
	for _, pair := range pairs {
		if strings.EqualFold(pair.ChainID, chain) &&
			strings.EqualFold(pair.DexID, config.Provider) &&
			dexPairProtocol(pair.Labels) != "" &&
			common.IsHexAddress(pair.PairAddress) {
			count++
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("no reviewed protocol pools")
	}
	return count, nil
}

func endpointCheckConfig(provider string) (ammProviderConfig, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "uniswap":
		return uniswapAMMConfig, nil
	case "pancakeswap":
		return pancakeAMMConfig, nil
	default:
		return ammProviderConfig{}, fmt.Errorf("endpoint-check supports uniswap or pancakeswap")
	}
}

func checkAMMSubgraph(
	ctx context.Context,
	client *http.Client,
	endpoint string,
) (int64, int, error) {
	body, _ := json.Marshal(map[string]string{
		"query": `{ _meta { block { number } hasIndexingErrors } pools(first: 1) { id } }`,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Data struct {
			Meta struct {
				Block struct {
					Number int64 `json:"number"`
				} `json:"block"`
				HasIndexingErrors bool `json:"hasIndexingErrors"`
			} `json:"_meta"`
			Pools []struct {
				ID string `json:"id"`
			} `json:"pools"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return 0, 0, err
	}
	if len(envelope.Errors) > 0 || envelope.Data.Meta.HasIndexingErrors ||
		envelope.Data.Meta.Block.Number <= 0 || len(envelope.Data.Pools) == 0 {
		return 0, 0, fmt.Errorf("unhealthy graph response")
	}
	return envelope.Data.Meta.Block.Number, len(envelope.Data.Pools), nil
}
