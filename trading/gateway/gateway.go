package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/httpapi"
	"github.com/the-web3/s78-market-services/trading/netutil"
	"github.com/the-web3/s78-market-services/trading/recovery"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

const practiceOwnerMarker = "qiu-market/trading-practice/v1"

type Config struct {
	PostgresURL             string
	PracticeMode            bool
	VirtualLiquidityEnabled bool
	GRPCAddress             string
	BindAddress             string
	AllowedOrigins          []string
	LocalAuth               bool
	SecureCookies           bool
	GitHubClientID          string
	GitHubSecret            string
	GitHubRedirect          string
	DiskPath                string
	MinWriteBytes           int64
	RecoveryGate            bool
	RecoveryProvenance      recovery.Provenance
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
	if config.RecoveryGate {
		var err error
		config.RecoveryProvenance, err = recovery.BindExecutableSourceDigest(
			config.RecoveryProvenance,
		)
		if err != nil {
			return nil, fmt.Errorf("bind trading gateway executable: %w", err)
		}
	}
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
	if config.PracticeMode {
		var marker string
		if err := pool.QueryRow(ctx, `
			SELECT owner_key FROM qiu_trading_practice_owner WHERE singleton=TRUE
		`).Scan(&marker); err != nil || marker != practiceOwnerMarker {
			return nil, fmt.Errorf("trading state PostgreSQL ownership marker is missing or invalid")
		}
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
	github, err := optionalGitHubOAuth(config)
	if err != nil {
		return nil, err
	}

	httpConfig := httpapi.DefaultConfig()
	httpConfig.BindAddress = config.BindAddress
	httpConfig.AllowedOrigins = config.AllowedOrigins
	httpConfig.LocalMode = config.LocalAuth
	httpConfig.SecureCookies = config.SecureCookies
	httpConfig.RecoveryGate = config.RecoveryGate
	httpConfig.PracticeMode = config.PracticeMode
	httpConfig.VirtualLiquidityEnabled = config.VirtualLiquidityEnabled
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
	var recoveryCoordinator *recovery.Coordinator
	if config.RecoveryGate {
		recoveryStore, storeErr := recovery.NewPostgresStore(pool)
		if storeErr != nil {
			return nil, storeErr
		}
		recoveryCoordinator, storeErr = recovery.NewCoordinator(
			recoveryStore,
			"BTC-USDT",
			config.RecoveryProvenance,
		)
		if storeErr != nil {
			return nil, storeErr
		}
	}

	cleanupPool = false
	cleanupConnection = false
	handler := diskWriteGuard(server.Handler(), config.DiskPath, config.MinWriteBytes)
	if recoveryCoordinator != nil {
		handler = recoveryWriteGuard(handler, recoveryCoordinator)
	}
	return &Gateway{
		pool:       pool,
		connection: connection,
		handler:    boundedREST(handler),
	}, nil
}

func validateConfig(config Config) error {
	if config.PostgresURL == "" || config.BindAddress == "" {
		return fmt.Errorf("trading PostgreSQL URL and HTTP bind address are required")
	}
	if !netutil.IsIPLoopbackAddress(config.GRPCAddress) {
		return fmt.Errorf("trading gateway may connect only to an explicit IP loopback address")
	}
	if config.PracticeMode {
		if !netutil.IsIPLoopbackAddress(config.BindAddress) || !config.LocalAuth ||
			config.SecureCookies || config.GitHubClientID != "" || config.GitHubSecret != "" ||
			config.GitHubRedirect != "" {
			return fmt.Errorf("practice mode requires loopback HTTP, local auth, insecure loopback cookies, and OAuth disabled")
		}
		for _, origin := range config.AllowedOrigins {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Host == "" || !netutil.IsIPLoopbackAddress(parsed.Host) {
				return fmt.Errorf("practice mode allows only explicit IP loopback browser origins")
			}
		}
	}
	if len(config.AllowedOrigins) == 0 {
		return fmt.Errorf("at least one trading browser origin is required")
	}
	if (config.GitHubClientID == "") != (config.GitHubSecret == "") {
		return fmt.Errorf("GitHub OAuth client id and secret must be configured together")
	}
	if config.MinWriteBytes < 0 ||
		(config.MinWriteBytes > 0 && strings.TrimSpace(config.DiskPath) == "") {
		return fmt.Errorf("trading disk write guard requires a path and non-negative floor")
	}
	if config.RecoveryGate {
		if _, err := recovery.NormalizeProvenance(config.RecoveryProvenance); err != nil {
			return fmt.Errorf("invalid trading gateway recovery provenance: %w", err)
		}
	}
	return nil
}

func optionalGitHubOAuth(config Config) (httpapi.GitHubOAuth, error) {
	if config.GitHubClientID == "" && config.GitHubSecret == "" {
		return nil, nil
	}
	redirect := config.GitHubRedirect
	if redirect == "" {
		redirect = strings.TrimRight(config.AllowedOrigins[0], "/") +
			"/api/v1/trading/auth/github/callback"
	}
	return auth.NewGitHubOAuth(auth.GitHubConfig{
		ClientID:     config.GitHubClientID,
		ClientSecret: config.GitHubSecret,
		RedirectURL:  redirect,
	})
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

func diskWriteGuard(next http.Handler, path string, minimum int64) http.Handler {
	if minimum <= 0 {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isTradingMutation(request) {
			next.ServeHTTP(writer, request)
			return
		}
		var fileSystem syscall.Statfs_t
		if err := syscall.Statfs(path, &fileSystem); err != nil {
			writeDiskPaused(writer)
			return
		}
		available := int64(fileSystem.Bavail) * int64(fileSystem.Bsize)
		if available < minimum {
			writeDiskPaused(writer)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func isTradingMutation(request *http.Request) bool {
	if request.Method != http.MethodPost {
		return false
	}
	path := request.URL.Path
	return path == "/api/v1/trading/orders" ||
		path == "/api/v1/trading/admin/fund" ||
		(strings.HasPrefix(path, "/api/v1/trading/orders/") &&
			strings.HasSuffix(path, "/cancel"))
}

func writeDiskPaused(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(writer).Encode(map[string]string{
		"code":    "trading_write_paused",
		"message": "virtual trading writes are paused because local storage is critical",
	})
}

type recoveryGate interface {
	Status(context.Context) (recovery.Status, error)
	RequireWritable(context.Context) error
}

func recoveryWriteGuard(next http.Handler, gate recoveryGate) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet &&
			request.URL.Path == "/api/v1/trading/recovery/status" {
			writeRecoveryStatus(writer, request, gate)
			return
		}
		if !isTradingMutation(request) {
			next.ServeHTTP(writer, request)
			return
		}
		if err := gate.RequireWritable(request.Context()); err != nil {
			writeRecoveryBlocked(writer, request, gate)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func writeRecoveryStatus(
	writer http.ResponseWriter,
	request *http.Request,
	gate recoveryGate,
) {
	status, err := gate.Status(request.Context())
	if err != nil {
		writeRecoveryUnavailable(writer)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(recoveryStatusDTO{
		SchemaVersion:       status.SchemaVersion,
		MarketID:            string(status.MarketID),
		EpochID:             status.EpochID,
		Phase:               string(status.Phase),
		RuntimeSequence:     strconv.FormatUint(status.Proof.RuntimeSequence, 10),
		StateHash:           status.Proof.StateHash,
		LedgerBalanced:      status.Proof.LedgerBalanced,
		EventContinuous:     status.Proof.EventContinuous,
		ProjectionCaughtUp:  status.Proof.ProjectionCaughtUp,
		OutboxCaughtUp:      status.Proof.OutboxCaughtUp,
		TransportHealthy:    status.Proof.TransportHealthy,
		WritesEnabled:       status.WritesEnabled,
		LastError:           status.LastError,
		Version:             strconv.FormatUint(status.Version, 10),
		StartedAt:           status.StartedAt,
		UpdatedAt:           status.UpdatedAt,
		ContinuityUncertain: status.ContinuityUncertain,
		ContinuityError:     status.ContinuityError,
		Provenance:          status.Provenance,
	})
}

// recoveryStatusDTO keeps counters as decimal strings at the browser boundary;
// internal CAS types remain uint64 and never depend on JavaScript precision.
type recoveryStatusDTO struct {
	SchemaVersion       int                 `json:"schema_version"`
	MarketID            string              `json:"market_id"`
	EpochID             string              `json:"epoch_id"`
	Phase               string              `json:"phase"`
	RuntimeSequence     string              `json:"runtime_sequence"`
	StateHash           string              `json:"state_hash"`
	LedgerBalanced      bool                `json:"ledger_balanced"`
	EventContinuous     bool                `json:"event_continuous"`
	ProjectionCaughtUp  bool                `json:"projection_caught_up"`
	OutboxCaughtUp      bool                `json:"outbox_caught_up"`
	TransportHealthy    bool                `json:"transport_healthy"`
	WritesEnabled       bool                `json:"writes_enabled"`
	LastError           string              `json:"last_error,omitempty"`
	Version             string              `json:"version"`
	StartedAt           time.Time           `json:"started_at"`
	UpdatedAt           time.Time           `json:"updated_at"`
	ContinuityUncertain bool                `json:"continuity_uncertain"`
	ContinuityError     string              `json:"continuity_error,omitempty"`
	Provenance          recovery.Provenance `json:"provenance"`
}

func writeRecoveryBlocked(
	writer http.ResponseWriter,
	request *http.Request,
	gate recoveryGate,
) {
	status, err := gate.Status(request.Context())
	if err != nil {
		writeRecoveryUnavailable(writer)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"code":           "recovery_in_progress",
		"message":        "virtual trading writes are blocked until recovery proof completes",
		"market_id":      status.MarketID,
		"recovery_epoch": status.EpochID,
		"phase":          status.Phase,
		"writes_enabled": false,
	})
}

func writeRecoveryUnavailable(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"code":           "recovery_in_progress",
		"message":        "virtual trading recovery state is unavailable",
		"phase":          "uninitialized",
		"writes_enabled": false,
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
