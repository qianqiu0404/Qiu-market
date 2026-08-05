package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	githubAuthorizeEndpoint = "https://github.com/login/oauth/authorize"
	githubTokenEndpoint     = "https://github.com/login/oauth/access_token"
	githubUserEndpoint      = "https://api.github.com/user"
)

type GitHubConfig struct {
	ClientID              string
	ClientSecret          string
	RedirectURL           string
	AuthorizationEndpoint string
	TokenEndpoint         string
	UserEndpoint          string
	HTTPClient            *http.Client
}

type GitHubOAuth struct {
	config GitHubConfig
}

func NewGitHubOAuth(config GitHubConfig) (*GitHubOAuth, error) {
	if config.ClientID == "" || config.ClientSecret == "" || config.RedirectURL == "" {
		return nil, fmt.Errorf("GitHub OAuth client id, secret and redirect URL are required")
	}
	if _, err := url.ParseRequestURI(config.RedirectURL); err != nil {
		return nil, fmt.Errorf("invalid GitHub OAuth redirect URL: %w", err)
	}
	if config.AuthorizationEndpoint == "" {
		config.AuthorizationEndpoint = githubAuthorizeEndpoint
	}
	if config.TokenEndpoint == "" {
		config.TokenEndpoint = githubTokenEndpoint
	}
	if config.UserEndpoint == "" {
		config.UserEndpoint = githubUserEndpoint
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &GitHubOAuth{config: config}, nil
}

func (g *GitHubOAuth) AuthorizationURL(state, codeChallenge string) string {
	values := url.Values{
		"client_id":             {g.config.ClientID},
		"redirect_uri":          {g.config.RedirectURL},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return g.config.AuthorizationEndpoint + "?" + values.Encode()
}

func (g *GitHubOAuth) Exchange(
	ctx context.Context,
	code string,
	codeVerifier string,
) (string, error) {
	if code == "" || codeVerifier == "" {
		return "", fmt.Errorf("GitHub OAuth code and PKCE verifier are required")
	}
	values := url.Values{
		"client_id":     {g.config.ClientID},
		"client_secret": {g.config.ClientSecret},
		"code":          {code},
		"redirect_uri":  {g.config.RedirectURL},
		"code_verifier": {codeVerifier},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		g.config.TokenEndpoint,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("build GitHub token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := g.config.HTTPClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("exchange GitHub OAuth code: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read GitHub token response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub token endpoint returned %d", response.StatusCode)
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return "", fmt.Errorf("decode GitHub token response: %w", err)
	}
	if tokenResponse.Error != "" || tokenResponse.AccessToken == "" {
		return "", fmt.Errorf("GitHub rejected OAuth code")
	}

	userRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, g.config.UserEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build GitHub user request: %w", err)
	}
	userRequest.Header.Set("Accept", "application/vnd.github+json")
	userRequest.Header.Set("Authorization", "Bearer "+tokenResponse.AccessToken)
	userRequest.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	userRequest.Header.Set("User-Agent", "s78-virtual-trading")
	userResponse, err := g.config.HTTPClient.Do(userRequest)
	if err != nil {
		return "", fmt.Errorf("fetch GitHub user: %w", err)
	}
	defer userResponse.Body.Close()
	body, err = io.ReadAll(io.LimitReader(userResponse.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read GitHub user response: %w", err)
	}
	if userResponse.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub user endpoint returned %d", userResponse.StatusCode)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", fmt.Errorf("decode GitHub user response: %w", err)
	}
	if user.Login == "" {
		return "", fmt.Errorf("GitHub user response has no login")
	}
	return user.Login, nil
}
