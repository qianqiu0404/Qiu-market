package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/httpapi"
	"github.com/the-web3/s78-market-services/trading/netutil"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

type Config struct {
	PostgresURL    string
	GRPCAddress    string
	BindAddress    string
	AllowedOrigins []string
	LocalAuth      bool
	SecureCookies  bool
	GitHubClientID string
	GitHubSecret   string
	GitHubRedirect string
}

// Gateway owns browser authentication and protocol adaptation, but no trading
// state. Its gRPC connection is non-blocking so the market-data API can start
// and remain healthy while the trading process is unavailable.
type Gateway struct {
	pool       *pgxpool.Pool
	connection *grpc.ClientConn
	handler    http.Handler
}

func New(ctx context.Context, config Config) (*Gateway, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, config.PostgresURL)
	if err != nil {
		return nil, fmt.Errorf("open trading session pool: %w", err)
	}
	cleanupPool := true
	defer func() {
		if cleanupPool {
			pool.Close()
		}
	}()
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping trading session database: %w", err)
	}
	if err := postgresstore.VerifySchema(ctx, pool); err != nil {
		return nil, err
	}

	connection, err := grpc.NewClient(
		config.GRPCAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create trading gRPC client: %w", err)
	}
	cleanupConnection := true
	defer func() {
		if cleanupConnection {
			_ = connection.Close()
		}
	}()

	sessions, err := auth.NewPostgresSessionStore(pool, "qianqiu0404")
	if err != nil {
		return nil, err
	}
	tickets, err := auth.NewTicketManager(30 * time.Second)
	if err != nil {
		return nil, err
	}
	oauthStates, err := auth.NewOAuthStateManager(10 * time.Minute)
	if err != nil {
		return nil, err
	}
	var github *auth.GitHubOAuth
	if config.GitHubClientID != "" && config.GitHubSecret != "" {
		redirect := config.GitHubRedirect
		if redirect == "" {
			redirect = strings.TrimRight(config.AllowedOrigins[0], "/") +
				"/api/v1/trading/auth/github/callback"
		}
		github, err = auth.NewGitHubOAuth(auth.GitHubConfig{
			ClientID:     config.GitHubClientID,
			ClientSecret: config.GitHubSecret,
			RedirectURL:  redirect,
		})
		if err != nil {
			return nil, err
		}
	}

	httpConfig := httpapi.DefaultConfig()
	httpConfig.BindAddress = config.BindAddress
	httpConfig.AllowedOrigins = config.AllowedOrigins
	httpConfig.LocalMode = config.LocalAuth
	httpConfig.SecureCookies = config.SecureCookies
	server, err := httpapi.New(
		tradingv1.NewTradingServiceClient(connection),
		sessions,
		tickets,
		oauthStates,
		github,
		httpConfig,
	)
	if err != nil {
		return nil, err
	}

	cleanupPool = false
	cleanupConnection = false
	return &Gateway{
		pool:       pool,
		connection: connection,
		handler:    boundedREST(server.Handler()),
	}, nil
}

func validateConfig(config Config) error {
	if config.PostgresURL == "" || config.BindAddress == "" {
		return fmt.Errorf("trading PostgreSQL URL and HTTP bind address are required")
	}
	if !netutil.IsIPLoopbackAddress(config.GRPCAddress) {
		return fmt.Errorf("trading gateway may connect only to an explicit IP loopback address")
	}
	if len(config.AllowedOrigins) == 0 {
		return fmt.Errorf("at least one trading browser origin is required")
	}
	if (config.GitHubClientID == "") != (config.GitHubSecret == "") {
		return fmt.Errorf("GitHub OAuth client id and secret must be configured together")
	}
	return nil
}

func boundedREST(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/trading/events/ws" {
			next.ServeHTTP(writer, request)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
		defer cancel()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (g *Gateway) Handler() http.Handler {
	return g.handler
}

func (g *Gateway) Close() error {
	var result error
	if g.connection != nil {
		result = g.connection.Close()
	}
	if g.pool != nil {
		g.pool.Close()
	}
	return result
}

// UnavailableHandler keeps the failure local to /api/v1/trading/** and avoids
// leaking setup or database details into the browser.
func UnavailableHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(writer).Encode(map[string]string{
			"code":    "trading_unavailable",
			"message": "virtual trading is temporarily unavailable",
		})
	})
}
