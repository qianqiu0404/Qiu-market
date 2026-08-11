package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	activeReleaseSchema = "qiu.d1.active-release.v2"
	generationSchema    = "qiu.d1.committed-generation.v2"
	edgeContractSchema  = "qiu.market-edge-contract.v1"
	liveDataMode        = "live"
	providerPolicy      = "restricted-no-bypass.v1"
	marketContract      = "qiu.market-read-contract.v1"
	marketSnapshot      = "qiu.market-snapshot.v1"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type activeRelease struct {
	SchemaVersion       string `json:"schema_version"`
	Commit              string `json:"commit"`
	DataMode            string `json:"data_mode"`
	ProviderPolicy      string `json:"provider_policy"`
	ContractSchema      string `json:"contract_schema"`
	SnapshotSchema      string `json:"snapshot_schema"`
	EdgeSchema          string `json:"edge_schema"`
	GenerationID        string `json:"generation_id"`
	GenerationOwner     string `json:"generation_owner_token"`
	FrontdoorPort       int    `json:"frontdoor_port"`
	TunnelTarget        string `json:"tunnel_target"`
	DeploymentID        string `json:"deployment_id"`
	Origin              string `json:"origin"`
	SourcePath          string `json:"source_path"`
	ConfigPath          string `json:"config_path"`
	BinaryPath          string `json:"binary_path"`
	BinarySHA256        string `json:"binary_sha256"`
	FrontdoorBinaryPath string `json:"frontdoor_binary_path"`
	FrontdoorSHA256     string `json:"frontdoor_sha256"`
	GatePath            string `json:"gate_path"`
	GateSHA256          string `json:"gate_sha256"`
	AttestationPath     string `json:"attestation_path"`
	AttestationSHA256   string `json:"attestation_sha256"`
	SelectedAt          string `json:"selected_at"`
}

type committedGeneration struct {
	SchemaVersion string `json:"schema_version"`
	GenerationID  string `json:"generation_id"`
	OwnerToken    string `json:"owner_token"`
	Commit        string `json:"commit"`
	DataMode      string `json:"data_mode"`
	FrontdoorPort int    `json:"frontdoor_port"`
	UpstreamPort  int    `json:"upstream_port"`
	TunnelTarget  string `json:"tunnel_target"`
	Ready         bool   `json:"ready"`
	VerifiedAt    string `json:"verified_at"`
}

type edgeAuthority struct {
	manifestPath   string
	generationPath string
}

func (a edgeAuthority) loadRelease() (*activeRelease, error) {
	var release activeRelease
	if err := decodeFile(a.manifestPath, &release); err != nil {
		return nil, fmt.Errorf("active release: %w", err)
	}
	if release.SchemaVersion != activeReleaseSchema || !commitPattern.MatchString(release.Commit) ||
		release.DataMode != liveDataMode || release.ProviderPolicy != providerPolicy ||
		release.ContractSchema != marketContract || release.SnapshotSchema != marketSnapshot ||
		release.EdgeSchema != edgeContractSchema || release.FrontdoorPort != 18084 ||
		release.TunnelTarget != "http://127.0.0.1:18084" ||
		strings.TrimSpace(release.GenerationID) == "" || strings.TrimSpace(release.GenerationOwner) == "" {
		return nil, errors.New("active release contract is invalid")
	}
	return &release, nil
}

func decodeFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("file contains trailing JSON")
	}
	return nil
}

func (a edgeAuthority) load() (*activeRelease, error) {
	release, err := a.loadRelease()
	if err != nil {
		return nil, err
	}
	var generation committedGeneration
	if err := decodeFile(a.generationPath, &generation); err != nil {
		return nil, fmt.Errorf("committed generation: %w", err)
	}
	if generation.SchemaVersion != generationSchema || !generation.Ready ||
		generation.GenerationID != release.GenerationID || generation.OwnerToken != release.GenerationOwner ||
		generation.Commit != release.Commit || generation.DataMode != liveDataMode ||
		generation.FrontdoorPort != 18084 || generation.UpstreamPort != 18080 ||
		generation.TunnelTarget != release.TunnelTarget {
		return nil, errors.New("committed generation does not own the active release")
	}
	return release, nil
}

func setSyntheticContractHeaders(w http.ResponseWriter, request *http.Request, release *activeRelease) {
	w.Header().Set("X-Qiu-Market-Backend-Release-Commit", release.Commit)
	w.Header().Set("X-Qiu-Market-Data-Mode", release.DataMode)
	w.Header().Set("X-Qiu-Market-Provider-Policy", release.ProviderPolicy)
	w.Header().Set("X-Qiu-Market-Contract-Schema", release.ContractSchema)
	w.Header().Set("X-Qiu-Market-Snapshot-Schema", release.SnapshotSchema)
	w.Header().Set("X-Qiu-Market-Edge-Release-Commit", release.Commit)
	w.Header().Set("X-Qiu-Market-Edge-Data-Mode", release.DataMode)
	w.Header().Set("X-Qiu-Market-Edge-Contract-Schema", release.EdgeSchema)
	w.Header().Set("X-Qiu-Data-Mode", release.DataMode)
	if nonce := strings.TrimSpace(request.Header.Get("X-Qiu-Market-Nonce")); nonce != "" {
		w.Header().Set("X-Qiu-Market-Backend-Request-Nonce", nonce)
	}
}

func newFrontdoor(authority edgeAuthority, upstream *url.URL) http.Handler {
	return newFrontdoorWithTransport(authority, upstream, http.DefaultTransport)
}

func newFrontdoorWithTransport(
	authority edgeAuthority,
	upstream *url.URL,
	transport http.RoundTripper,
) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, request *http.Request, _ error) {
		if release, loadErr := authority.loadRelease(); loadErr == nil {
			setSyntheticContractHeaders(w, request, release)
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"code":"edge_upstream_contract_mismatch","message":"Qiu Market edge rejected the upstream response."}`)
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		release, err := authority.load()
		if err != nil {
			return err
		}
		if strings.HasPrefix(response.Request.URL.Path, "/api/") {
			expected := map[string]string{
				"X-Qiu-Market-Backend-Release-Commit": release.Commit,
				"X-Qiu-Market-Data-Mode":              release.DataMode,
				"X-Qiu-Market-Provider-Policy":        release.ProviderPolicy,
				"X-Qiu-Market-Contract-Schema":        release.ContractSchema,
				"X-Qiu-Market-Snapshot-Schema":        release.SnapshotSchema,
			}
			for name, value := range expected {
				if response.Header.Get(name) != value {
					_ = response.Body.Close()
					return fmt.Errorf("backend response %s mismatch", name)
				}
			}
			if mode := strings.TrimSpace(response.Header.Get("X-Qiu-Data-Mode")); mode != "" && mode != liveDataMode {
				_ = response.Body.Close()
				return errors.New("backend response carries replay data mode")
			}
		}
		response.Header.Set("X-Qiu-Data-Mode", liveDataMode)
		response.Header.Set("X-Qiu-Market-Edge-Release-Commit", release.Commit)
		response.Header.Set("X-Qiu-Market-Edge-Data-Mode", liveDataMode)
		response.Header.Set("X-Qiu-Market-Edge-Contract-Schema", edgeContractSchema)
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if _, err := authority.load(); err != nil {
			if release, loadErr := authority.loadRelease(); loadErr == nil {
				setSyntheticContractHeaders(w, request, release)
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":"edge_generation_unavailable","message":"Qiu Market edge generation is not ready."}`)
			return
		}
		proxy.ServeHTTP(w, request)
	})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:18084", "fixed loopback listener")
	upstreamValue := flag.String("upstream", "http://127.0.0.1:18080", "fixed loopback upstream")
	manifest := flag.String("manifest", "", "private active release manifest")
	generation := flag.String("generation", "", "private committed generation")
	flag.Parse()
	if *listen != "127.0.0.1:18084" || *upstreamValue != "http://127.0.0.1:18080" ||
		strings.TrimSpace(*manifest) == "" || strings.TrimSpace(*generation) == "" {
		fmt.Fprintln(os.Stderr, "market-frontdoor requires fixed 18084 -> 18080 and both authority files")
		os.Exit(64)
	}
	upstream, err := url.Parse(*upstreamValue)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(64)
	}
	server := &http.Server{
		Addr: *listen, Handler: newFrontdoor(edgeAuthority{*manifest, *generation}, upstream),
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
