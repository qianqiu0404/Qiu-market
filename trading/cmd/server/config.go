package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/the-web3/s78-market-services/trading/netutil"
)

type config struct {
	postgresDSN        string
	grpcAddress        string
	httpAddress        string
	allowedOrigins     []string
	localAuth          bool
	secureCookies      bool
	githubClientID     string
	githubSecret       string
	githubRedirect     string
	cursorHMACCurrent  string
	cursorHMACPrevious string
}

func loadConfig() (config, error) {
	result := config{
		postgresDSN:    os.Getenv("S78_TRADING_POSTGRES_DSN"),
		grpcAddress:    envOrDefault("S78_TRADING_GRPC_ADDR", "127.0.0.1:9094"),
		httpAddress:    envOrDefault("S78_TRADING_HTTP_ADDR", "127.0.0.1:8084"),
		githubClientID: os.Getenv("S78_TRADING_GITHUB_CLIENT_ID"),
		githubSecret:   os.Getenv("S78_TRADING_GITHUB_CLIENT_SECRET"),
		cursorHMACCurrent: firstEnvironment(
			"MARKET_TRADING_CURSOR_HMAC_CURRENT",
			"S78_TRADING_CURSOR_HMAC_CURRENT",
		),
		cursorHMACPrevious: firstEnvironment(
			"MARKET_TRADING_CURSOR_HMAC_PREVIOUS",
			"S78_TRADING_CURSOR_HMAC_PREVIOUS",
		),
	}
	if result.postgresDSN == "" {
		return config{}, fmt.Errorf("S78_TRADING_POSTGRES_DSN is required")
	}
	if !netutil.IsIPLoopbackAddress(result.grpcAddress) {
		return config{}, fmt.Errorf("trading gRPC must bind to an IP loopback address")
	}
	var err error
	result.localAuth, err = optionalBool("S78_TRADING_LOCAL_AUTH", false)
	if err != nil {
		return config{}, err
	}
	result.secureCookies, err = optionalBool("S78_TRADING_SECURE_COOKIES", false)
	if err != nil {
		return config{}, err
	}
	origins := os.Getenv("S78_TRADING_ALLOWED_ORIGINS")
	if origins == "" {
		result.allowedOrigins = []string{"http://" + result.httpAddress}
	} else {
		for _, origin := range strings.Split(origins, ",") {
			origin = strings.TrimSpace(origin)
			if origin != "" {
				result.allowedOrigins = append(result.allowedOrigins, origin)
			}
		}
	}
	result.githubRedirect = os.Getenv("S78_TRADING_GITHUB_REDIRECT_URL")
	if result.githubRedirect == "" {
		result.githubRedirect = "http://" + result.httpAddress +
			"/api/v1/trading/auth/github/callback"
	}
	if !result.localAuth && (result.githubClientID == "" || result.githubSecret == "") {
		return config{}, fmt.Errorf(
			"GitHub OAuth credentials are required unless S78_TRADING_LOCAL_AUTH=true",
		)
	}
	if _, err := url.ParseRequestURI(result.githubRedirect); err != nil {
		return config{}, fmt.Errorf("invalid GitHub redirect URL: %w", err)
	}
	return result, nil
}

func firstEnvironment(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func optionalBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return parsed, nil
}
