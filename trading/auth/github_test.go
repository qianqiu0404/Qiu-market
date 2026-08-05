package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/the-web3/s78-market-services/trading/auth"
)

func TestGitHubOAuthUsesPKCEAndReadsAuthenticatedLogin(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/token", func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if request.Form.Get("code") != "code-1" ||
			request.Form.Get("code_verifier") != "verifier-1" ||
			request.Header.Get("Accept") != "application/json" {
			t.Errorf("token request = form:%v headers:%v", request.Form, request.Header)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": "secret-token"})
	})
	mux.HandleFunc("/user", func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret-token" ||
			request.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("user headers = %v", request.Header)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"login": "qianqiu0404"})
	})

	provider, err := auth.NewGitHubOAuth(auth.GitHubConfig{
		ClientID:              "client-id",
		ClientSecret:          "client-secret",
		RedirectURL:           "http://127.0.0.1/callback",
		AuthorizationEndpoint: server.URL + "/authorize",
		TokenEndpoint:         server.URL + "/token",
		UserEndpoint:          server.URL + "/user",
		HTTPClient:            server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(provider.AuthorizationURL("state-1", "challenge-1"))
	if err != nil {
		t.Fatal(err)
	}
	if authorizationURL.Query().Get("state") != "state-1" ||
		authorizationURL.Query().Get("code_challenge") != "challenge-1" ||
		authorizationURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL = %s", authorizationURL)
	}
	login, err := provider.Exchange(context.Background(), "code-1", "verifier-1")
	if err != nil || login != "qianqiu0404" {
		t.Fatalf("exchange = %q, %v", login, err)
	}
}
