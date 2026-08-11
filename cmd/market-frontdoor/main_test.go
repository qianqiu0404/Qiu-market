package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const frontdoorTestCommit = "0123456789abcdef0123456789abcdef01234567"

func writeFrontdoorAuthority(t *testing.T, ready bool) edgeAuthority {
	t.Helper()
	dir := t.TempDir()
	release := activeRelease{
		SchemaVersion: activeReleaseSchema, Commit: frontdoorTestCommit,
		DataMode: liveDataMode, ProviderPolicy: providerPolicy,
		ContractSchema: marketContract, SnapshotSchema: marketSnapshot,
		EdgeSchema: edgeContractSchema, GenerationID: "generation-1",
		GenerationOwner: "owner-token-1", FrontdoorPort: 18084,
		TunnelTarget: "http://127.0.0.1:18084",
	}
	generation := committedGeneration{
		SchemaVersion: generationSchema, GenerationID: release.GenerationID,
		OwnerToken: release.GenerationOwner, Commit: release.Commit,
		DataMode: liveDataMode, FrontdoorPort: 18084, UpstreamPort: 18080,
		TunnelTarget: release.TunnelTarget, Ready: ready,
		VerifiedAt: "2026-08-12T00:00:00Z",
	}
	manifestPath := filepath.Join(dir, "active-release.json")
	generationPath := filepath.Join(dir, "committed-generation.json")
	for path, value := range map[string]any{manifestPath: release, generationPath: generation} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, encoded, 0o600))
	}
	return edgeAuthority{manifestPath: manifestPath, generationPath: generationPath}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func contractedUpstream(t *testing.T, headers map[string]string, body string) (*url.URL, http.RoundTripper) {
	t.Helper()
	upstream, err := url.Parse("http://127.0.0.1:18080")
	require.NoError(t, err)
	return upstream, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		responseHeaders := make(http.Header)
		for name, value := range map[string]string{
			"X-Qiu-Market-Backend-Release-Commit": frontdoorTestCommit,
			"X-Qiu-Market-Data-Mode":              liveDataMode,
			"X-Qiu-Market-Provider-Policy":        providerPolicy,
			"X-Qiu-Market-Contract-Schema":        marketContract,
			"X-Qiu-Market-Snapshot-Schema":        marketSnapshot,
		} {
			responseHeaders.Set(name, value)
		}
		for name, value := range headers {
			responseHeaders.Set(name, value)
		}
		responseHeaders.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK, Header: responseHeaders,
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
}

func TestFrontdoorPassesBodyUnchangedAndAttestsEdge(t *testing.T) {
	authority := writeFrontdoorAuthority(t, true)
	body := `{"code":2000,"result":{"asset_count":106}}`
	upstream, transport := contractedUpstream(t, nil, body)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/get_market_overview", nil)
	response := httptest.NewRecorder()
	newFrontdoorWithTransport(authority, upstream, transport).ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, body, response.Body.String())
	require.Equal(t, frontdoorTestCommit, response.Header().Get("X-Qiu-Market-Edge-Release-Commit"))
	require.Equal(t, liveDataMode, response.Header().Get("X-Qiu-Market-Edge-Data-Mode"))
	require.Equal(t, edgeContractSchema, response.Header().Get("X-Qiu-Market-Edge-Contract-Schema"))
	require.Equal(t, liveDataMode, response.Header().Get("X-Qiu-Data-Mode"))
}

func TestFrontdoorRejectsReplayAndWrongBackendWithoutLeakingBody(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers map[string]string
	}{
		{name: "deterministic replay", headers: map[string]string{"X-Qiu-Data-Mode": "d1_deterministic_replay"}},
		{name: "wrong release", headers: map[string]string{"X-Qiu-Market-Backend-Release-Commit": "0000000000000000000000000000000000000000"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority := writeFrontdoorAuthority(t, true)
			upstream, transport := contractedUpstream(t, test.headers, `{"secret":"must-not-pass"}`)
			response := httptest.NewRecorder()
			newFrontdoorWithTransport(authority, upstream, transport).ServeHTTP(
				response, httptest.NewRequest(http.MethodPost, "/api/v2/get_market_overview", nil),
			)
			require.Equal(t, http.StatusBadGateway, response.Code)
			require.NotContains(t, response.Body.String(), "must-not-pass")
		})
	}
}

func TestFrontdoorDrainFailsBeforeContactingUpstream(t *testing.T) {
	authority := writeFrontdoorAuthority(t, false)
	called := false
	upstream, err := url.Parse("http://127.0.0.1:18080")
	require.NoError(t, err)
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not be called")
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Qiu-Market-Nonce", "0123456789abcdef0123456789abcdef")
	newFrontdoorWithTransport(authority, upstream, transport).ServeHTTP(
		response, request,
	)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, frontdoorTestCommit, response.Header().Get("X-Qiu-Market-Backend-Release-Commit"))
	require.Equal(t, "0123456789abcdef0123456789abcdef", response.Header().Get("X-Qiu-Market-Backend-Request-Nonce"))
	require.Equal(t, frontdoorTestCommit, response.Header().Get("X-Qiu-Market-Edge-Release-Commit"))
	require.False(t, called)
}
