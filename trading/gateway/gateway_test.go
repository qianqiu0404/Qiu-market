package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateConfigAndUnavailableContract(t *testing.T) {
	t.Parallel()
	config := Config{
		PostgresURL:    "postgres://example.invalid/s78",
		GRPCAddress:    "127.0.0.1:9094",
		BindAddress:    "127.0.0.1:9092",
		AllowedOrigins: []string{"http://127.0.0.1:5174"},
	}
	if err := validateConfig(config); err != nil {
		t.Fatal(err)
	}
	config.GitHubClientID = "id-only"
	if err := validateConfig(config); err == nil {
		t.Fatal("accepted partial GitHub OAuth configuration")
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/trading/session", nil)
	response := httptest.NewRecorder()
	UnavailableHandler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unavailable response may be cached")
	}
}

func TestOptionalGitHubOAuthDoesNotCreateTypedNilInterface(t *testing.T) {
	t.Parallel()
	config := Config{
		AllowedOrigins: []string{"https://qiu-market.vercel.app"},
	}
	github, err := optionalGitHubOAuth(config)
	if err != nil {
		t.Fatal(err)
	}
	if github != nil {
		t.Fatal("missing credentials enabled GitHub OAuth")
	}

	config.GitHubClientID = "client-id"
	config.GitHubSecret = "client-secret"
	github, err = optionalGitHubOAuth(config)
	if err != nil {
		t.Fatal(err)
	}
	if github == nil {
		t.Fatal("complete credentials did not enable GitHub OAuth")
	}
}
