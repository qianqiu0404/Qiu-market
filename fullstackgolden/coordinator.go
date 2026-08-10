package fullstackgolden

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/the-web3/s78-market-services/marketdata/quality"
	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
	"github.com/the-web3/s78-market-services/services/http/dataquality"
	"github.com/the-web3/s78-market-services/services/http/researchsignals"
	"github.com/the-web3/s78-market-services/trading/gateway"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

const childDSNEnvironment = "QIU_FULLSTACK_POSTGRES_DSN"

type CoordinatorConfig struct {
	PostgresURL    string `json:"-"`
	RepoRoot       string
	Executable     string
	HTTPAddress    string
	GRPCAddress    string
	FrontendOrigin string
	ManifestPath   string
	Postgres       PostgresEvidence
	FixturePID     int
	VuePID         int
	FixtureOrigin  string
	FixtureCAPath  string
}

func (c CoordinatorConfig) Validate() error {
	if strings.TrimSpace(c.PostgresURL) == "" || !filepath.IsAbs(c.RepoRoot) ||
		!filepath.IsAbs(c.Executable) || !filepath.IsAbs(c.ManifestPath) {
		return fmt.Errorf("full-stack coordinator requires private PostgreSQL, absolute repository/executable/manifest paths")
	}
	for label, address := range map[string]string{"HTTP": c.HTTPAddress, "gRPC": c.GRPCAddress} {
		host, _, err := net.SplitHostPort(address)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return fmt.Errorf("full-stack coordinator %s address must use an IP loopback", label)
		}
	}
	if err := validateExactLoopbackOrigin(c.FrontendOrigin, "http"); err != nil {
		return fmt.Errorf("full-stack frontend origin must be loopback HTTP")
	}
	if c.Postgres.PID <= 0 || c.Postgres.Version == "" || c.Postgres.Authority != "isolated_ephemeral_postgresql" {
		return fmt.Errorf("full-stack coordinator requires isolated PostgreSQL process evidence")
	}
	if err := validateLoopbackOrigin(c.FixtureOrigin); err != nil || c.FixturePID <= 0 || !filepath.IsAbs(c.FixtureCAPath) {
		return fmt.Errorf("full-stack coordinator requires an isolated loopback fixture process")
	}
	return nil
}

type backendProcess struct {
	generation Generation
	command    *exec.Cmd
	done       chan error
	pid        int
}

type Coordinator struct {
	config   CoordinatorConfig
	listener net.Listener
	server   *http.Server
	pool     *pgxpool.Pool
	gateway  *gateway.Gateway
	grpcConn *grpc.ClientConn
	client   tradingv1.TradingServiceClient
	child    *backendProcess
	mu       sync.Mutex
	// gatewaySwitch keeps short-lived REST reads on the stable side of an
	// A-to-B backend replacement. WebSocket streams never hold this lock.
	gatewaySwitch sync.RWMutex
	phase         Phase
	generation    Generation
	backendA      ProcessEvidence
	backendB      ProcessEvidence
	partial       *DatabaseState
	final         *DatabaseState
	restore       RestoreEvidence
	replay        ReplayEvidence
	reference     ReferenceEvidence
	quality       *qualityHarness
	fixtureCA     []byte
	spy           spyCounters
	closeOnce     sync.Once
	closeErr      error
}

type spyCounters struct {
	researchReads, providerReads, qualityReads, legacyReads atomic.Uint64
	controls, browserMutations, bootstrapFunds, fillWrites  atomic.Uint64
	readTrading, readReference, readFunds, forbidden        atomic.Uint64
	publicNetwork, fixtureNonGET                            atomic.Uint64
}

func (s *spyCounters) snapshot() SpyEvidence {
	return SpyEvidence{
		ResearchReads: s.researchReads.Load(), ProviderReads: s.providerReads.Load(), QualityReads: s.qualityReads.Load(),
		LegacyReadRequests: s.legacyReads.Load(), FixtureControlWrites: s.controls.Load(),
		AllowedBrowserTradingMutations: s.browserMutations.Load(), AllowedBootstrapFundWrites: s.bootstrapFunds.Load(),
		DeterministicFillWrites: s.fillWrites.Load(), ReadDomainTradingMutations: s.readTrading.Load(),
		ReadDomainReferenceWrites: s.readReference.Load(), ReadDomainFundWrites: s.readFunds.Load(),
		ForbiddenWrites: s.forbidden.Load(), PublicNetworkRequests: s.publicNetwork.Load(), FixtureNonGETRequests: s.fixtureNonGET.Load(),
	}
}

func StartCoordinator(ctx context.Context, config CoordinatorConfig) (*Coordinator, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, config.PostgresURL)
	if err != nil {
		return nil, fmt.Errorf("open full-stack PostgreSQL: %w", err)
	}
	cleanupPool := true
	defer func() {
		if cleanupPool {
			pool.Close()
		}
	}()
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping full-stack PostgreSQL: %w", err)
	}
	if err := ApplyTradingMigrations(ctx, pool, config.RepoRoot); err != nil {
		return nil, err
	}
	fixtureCA, err := os.ReadFile(config.FixtureCAPath)
	if err != nil || len(fixtureCA) == 0 || len(fixtureCA) > 64<<10 {
		return nil, fmt.Errorf("read bounded full-stack fixture CA")
	}
	listener, err := net.Listen("tcp", config.HTTPAddress)
	if err != nil {
		return nil, fmt.Errorf("listen full-stack HTTP: %w", err)
	}
	cleanupListener := true
	defer func() {
		if cleanupListener {
			_ = listener.Close()
		}
	}()
	coordinator := &Coordinator{config: config, listener: listener, pool: pool, phase: PhaseReadyA, generation: GenerationA, fixtureCA: append([]byte(nil), fixtureCA...)}
	coordinator.reference.Before = deterministicReference(time.Now().UTC())
	coordinator.reference.After = coordinator.reference.Before
	coordinator.reference.Unchanged = true
	if err := coordinator.startBackend(ctx, GenerationA, 0); err != nil {
		_ = coordinator.Close(context.Background())
		return nil, err
	}
	apiOrigin := "http://" + listener.Addr().String()
	tradingGateway, err := gateway.New(ctx, gateway.Config{
		PostgresURL: config.PostgresURL, GRPCAddress: config.GRPCAddress,
		BindAddress: listener.Addr().String(), AllowedOrigins: []string{config.FrontendOrigin, apiOrigin},
		LocalAuth: true, SecureCookies: false,
	})
	if err != nil {
		_ = coordinator.Close(context.Background())
		return nil, fmt.Errorf("construct full-stack gateway: %w", err)
	}
	coordinator.gateway = tradingGateway
	// The complete 17-window golden story advances in six-minute steps and
	// finishes near wall time, while every Binance window remains independent.
	qualityClock := newMutableClock(time.Now().UTC().Add(-18 * 6 * time.Minute))
	researchReader, err := researchsignal.NewLoopbackGoldenFixtureReader(config.FixtureOrigin, fixtureCA, qualityClock.Now)
	if err != nil {
		_ = coordinator.Close(context.Background())
		return nil, err
	}
	qualityMonitor, err := newQualityHarness(config.FixtureOrigin, fixtureCA, qualityClock, researchReader)
	if err != nil {
		_ = coordinator.Close(context.Background())
		return nil, err
	}
	coordinator.quality = qualityMonitor
	for range 3 {
		if _, err := coordinator.quality.recordWindow(ctx, "healthy", coordinator.setFixtureScenarioAt); err != nil {
			_ = coordinator.Close(context.Background())
			return nil, fmt.Errorf("prime full-stack quality window: %w", err)
		}
	}
	if err := coordinator.bootstrap(ctx); err != nil {
		_ = coordinator.Close(context.Background())
		return nil, err
	}
	router := chi.NewRouter()
	router.Use(loopbackOnly)
	router.Handle("/api/v1/trading/*", coordinator.observeTrading(tradingGateway.Handler()))
	researchsignals.Mount(router, researchReader)
	dataquality.Mount(router, countingQualityReporter{monitor: qualityMonitor.monitor, reads: &coordinator.spy.qualityReads})
	coordinator.mountLegacyReads(router)
	router.Get("/__full-stack/ready", coordinator.readyHTTP)
	router.Post("/__full-stack/control", coordinator.controlHTTP)
	router.Get("/__full-stack/state", coordinator.stateHTTP)
	router.Get("/__full-stack/evidence", coordinator.evidenceHTTP)
	router.Get("/__full-stack/reference", coordinator.referenceHTTP)
	coordinator.server = &http.Server{Handler: router, ReadHeaderTimeout: 3 * time.Second}
	go func() { _ = coordinator.server.Serve(listener) }()
	if err := coordinator.writeManifest(); err != nil {
		_ = coordinator.Close(context.Background())
		return nil, err
	}
	cleanupPool = false
	cleanupListener = false
	return coordinator, nil
}

func (c *Coordinator) APIOrigin() string { return "http://" + c.listener.Addr().String() }

func (c *Coordinator) startBackend(ctx context.Context, generation Generation, expectedSequence uint64) error {
	command := exec.Command(c.config.Executable, "--role", "backend-child", "--grpc-address", c.config.GRPCAddress)
	command.Env = []string{childDSNEnvironment + "=" + c.config.PostgresURL}
	for _, name := range []string{"TMPDIR", "LANG", "LC_ALL"} {
		if value := os.Getenv(name); value != "" {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	command.Stdout, command.Stderr = os.Stderr, os.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start backend %s: %w", generation, err)
	}
	child := &backendProcess{generation: generation, command: command, done: make(chan error, 1), pid: command.Process.Pid}
	go func() { child.done <- command.Wait() }()
	c.child = child
	if c.grpcConn == nil {
		connection, err := grpc.NewClient(c.config.GRPCAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			_ = c.stopBackend(false)
			return fmt.Errorf("dial full-stack backend: %w", err)
		}
		c.grpcConn = connection
		c.client = tradingv1.NewTradingServiceClient(connection)
	}
	status, err := c.waitBackend(ctx, expectedSequence)
	if err != nil {
		_ = c.stopBackend(false)
		return err
	}
	proof := processEvidence(generation, child.pid, status)
	if generation == GenerationA {
		c.backendA = proof
	} else {
		c.backendB = proof
	}
	c.generation = generation
	return nil
}

func (c *Coordinator) waitBackend(ctx context.Context, expected uint64) (*tradingv1.StatusResponse, error) {
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		requestContext, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		status, err := c.client.GetStatus(requestContext, &tradingv1.GetStatusRequest{MarketId: MarketID})
		cancel()
		if err == nil {
			sequence, parseErr := strconv.ParseUint(status.Sequence, 10, 64)
			if parseErr == nil && status.State == "ready" && sequence == expected && status.StateHash != "" {
				return status, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("backend did not become ready at sequence %d", expected)
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) bootstrap(ctx context.Context) error {
	requests := []*tradingv1.AdminFundVirtualRequest{
		{MarketId: MarketID, RequestId: "full-stack-fund-buyer-v1", AccountId: BuyerAccount, Asset: "USDT", Amount: "3000"},
		{MarketId: MarketID, RequestId: "full-stack-fund-seller-v1", AccountId: SellerAccount, Asset: "BTC", Amount: "0.03000000"},
	}
	for index, request := range requests {
		result, err := c.client.AdminFundVirtual(ctx, request)
		if err != nil || result.Sequence != strconv.Itoa(index+1) {
			return fmt.Errorf("bootstrap isolated account %d: result=%v err=%w", index, result, err)
		}
		c.spy.bootstrapFunds.Add(1)
	}
	status, err := c.client.GetStatus(ctx, &tradingv1.GetStatusRequest{MarketId: MarketID})
	if err != nil {
		return err
	}
	c.backendA = processEvidence(GenerationA, c.child.pid, status)
	return nil
}

func (c *Coordinator) stopBackend(graceful bool) error {
	if c.child == nil || c.child.command == nil || c.child.command.Process == nil {
		return nil
	}
	child := c.child
	c.child = nil
	var err error
	if graceful {
		err = child.command.Process.Signal(syscall.SIGTERM)
	} else {
		err = child.command.Process.Kill()
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case waitErr := <-child.done:
		if graceful && waitErr != nil {
			return waitErr
		}
	case <-time.After(15 * time.Second):
		_ = child.command.Process.Kill()
		return fmt.Errorf("backend %s did not exit", child.generation)
	}
	return nil
}

func (c *Coordinator) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		if c.server != nil {
			c.closeErr = errors.Join(c.closeErr, c.server.Shutdown(ctx))
		}
		c.closeErr = errors.Join(c.closeErr, c.stopBackend(true))
		if c.gateway != nil {
			c.closeErr = errors.Join(c.closeErr, c.gateway.Close())
		}
		if c.grpcConn != nil {
			c.closeErr = errors.Join(c.closeErr, c.grpcConn.Close())
		}
		if c.pool != nil {
			c.pool.Close()
		}
	})
	return c.closeErr
}

func (c *Coordinator) readyHTTP(writer http.ResponseWriter, request *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	status, err := c.client.GetStatus(request.Context(), &tradingv1.GetStatusRequest{MarketId: MarketID})
	if err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]any{"schema_version": SchemaReady, "ready": false, "error": "backend_unavailable"})
		return
	}
	backend := processEvidence(c.generation, c.child.pid, status)
	writeJSON(writer, http.StatusOK, Ready{SchemaVersion: SchemaReady, Ready: true, Phase: c.phase, Generation: c.generation,
		APIOrigin: c.APIOrigin(), CoordinatorPID: os.Getpid(), FixturePID: c.config.FixturePID, VuePID: c.config.VuePID, Postgres: c.postgresEvidence(backend), Backend: backend,
		Fixtures: FixtureState{Research: "fresh", Provider: "healthy"}, Spy: c.spy.snapshot()})
}

func (c *Coordinator) controlHTTP(writer http.ResponseWriter, request *http.Request) {
	var body ControlRequest
	if !decodeOne(writer, request, &body) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_control"})
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var response ControlResponse
	var err error
	switch body.Action {
	case "full_fill":
		response, err = c.fill(request.Context(), body.ClientOrderID, true)
	case "partial_fill":
		response, err = c.fill(request.Context(), body.ClientOrderID, false)
	case "restart_backend":
		if body.ClientOrderID != "" || body.Scenario != "" {
			err = fmt.Errorf("restart_backend accepts no extra fields")
		} else {
			response, err = c.restart(request.Context())
		}
	case "research_scenario":
		if body.ClientOrderID != "" {
			err = fmt.Errorf("research scenario does not accept a client order ID")
		} else {
			// The research adapter cache uses the same deterministic clock as the
			// quality harness. Advancing that clock is required to make each
			// browser scenario a real upstream revalidation rather than a replay
			// of the prior validated response.
			observedAt := c.quality.clock.advance(time.Second)
			err = c.setFixtureScenarioAt(request.Context(), "research", body.Scenario, observedAt)
			if err == nil {
				response = c.controlSnapshot("research_scenario", body.Scenario)
				response.WaitMilliseconds = 160
			}
		}
	case "quality_window":
		var qualityEvidence QualityWindowEvidence
		qualityEvidence, err = c.quality.recordWindow(request.Context(), body.Scenario, c.setFixtureScenarioAt)
		if err == nil {
			response = c.controlSnapshot("quality_window", body.Scenario)
			response.Quality = &qualityEvidence
		}
	default:
		err = fmt.Errorf("unsupported action")
	}
	if err != nil {
		writeJSON(writer, http.StatusConflict, map[string]string{"code": "control_rejected", "error": err.Error()})
		return
	}
	c.spy.controls.Add(1)
	writeJSON(writer, http.StatusOK, response)
}

func (c *Coordinator) fill(ctx context.Context, clientOrderID string, full bool) (ControlResponse, error) {
	want, quantity, canonicalQuantity, held, sellerID, nextPhase, expectedSequence := FullClientOrderID, FullQuantity, "0.01", "600", "full-stack-seller-full-v1", PhaseFullFilled, uint64(4)
	if !full {
		want, quantity, canonicalQuantity, held, sellerID, nextPhase, expectedSequence = PartialClientOrderID, FillQuantity, "0.01", "1200", "full-stack-seller-partial-v1", PhasePartial, 6
	}
	if clientOrderID != want {
		return ControlResponse{}, fmt.Errorf("action is bound to %s", want)
	}
	order, ok, err := queryOrderEvidence(ctx, c.pool, want)
	if err != nil {
		return ControlResponse{}, err
	}
	expectedOriginal := canonicalQuantity
	if !full {
		expectedOriginal = "0.02"
	}
	if !ok || order.Status != "open" || order.Side != "buy" || order.Type != "limit" || order.TimeInForce != "gtc" || !order.PostOnly ||
		order.Price != "60000" || order.OriginalQuantity != expectedOriginal || order.RemainingQuantity != expectedOriginal ||
		order.FilledQuantity != "0" || order.HeldAsset != "USDT" || order.HeldAmount != held {
		return ControlResponse{}, fmt.Errorf("buyer order is not the exact open resting order")
	}
	result, err := c.client.SubmitOrder(ctx, &tradingv1.SubmitOrderRequest{MarketId: MarketID, AccountId: SellerAccount,
		ClientOrderId: sellerID, Side: tradingv1.Side_SIDE_SELL, Type: tradingv1.OrderType_ORDER_TYPE_LIMIT,
		TimeInForce: tradingv1.TimeInForce_TIME_IN_FORCE_FOK, Price: Price, Quantity: quantity})
	if err != nil {
		return ControlResponse{}, err
	}
	after, err := AuditDatabase(ctx, c.pool)
	if err != nil {
		return ControlResponse{}, err
	}
	order = after.Orders[want]
	seller, sellerOK := after.Orders[sellerID]
	buyerExact := order.FilledQuantity == "0.01"
	if full {
		buyerExact = buyerExact && order.RemainingQuantity == "0" && order.HeldAsset == "" && order.HeldAmount == "0"
	} else {
		buyerExact = buyerExact && order.RemainingQuantity == "0.01" && order.HeldAsset == "USDT" && order.HeldAmount == "600"
	}
	if after.Sequence != expectedSequence || result.Sequence != strconv.FormatUint(expectedSequence, 10) || result.Status != "filled" || !sellerOK ||
		seller.Status != "filled" || seller.Side != "sell" || seller.Type != "limit" || seller.TimeInForce != "fok" || seller.PostOnly ||
		seller.Price != "60000" || seller.OriginalQuantity != canonicalQuantity || seller.RemainingQuantity != "0" ||
		seller.FilledQuantity != canonicalQuantity || seller.HeldAsset != "" || seller.HeldAmount != "0" || !buyerExact ||
		(full && order.Status != "filled") || (!full && order.Status != "partially_filled") {
		return ControlResponse{}, fmt.Errorf("fill reached unexpected durable state")
	}
	c.phase = nextPhase
	c.spy.fillWrites.Add(1)
	status, err := c.client.GetStatus(ctx, &tradingv1.GetStatusRequest{MarketId: MarketID})
	if err != nil || status.Sequence != result.Sequence || status.StateHash != after.EventHash {
		return ControlResponse{}, fmt.Errorf("runtime and PostgreSQL diverged after fill")
	}
	c.backendA = processEvidence(GenerationA, c.child.pid, status)
	if !full {
		copy := after
		c.partial = &copy
	}
	return ControlResponse{SchemaVersion: SchemaControl, Action: map[bool]string{true: "full_fill", false: "partial_fill"}[full],
		Phase: c.phase, Generation: c.generation, BackendPID: c.child.pid, Sequence: after.Sequence,
		StateHash: after.EventHash, Order: &order}, nil
}

func (c *Coordinator) restart(ctx context.Context) (ControlResponse, error) {
	c.gatewaySwitch.Lock()
	defer c.gatewaySwitch.Unlock()
	if c.phase != PhasePartial || c.partial == nil || c.generation != GenerationA {
		return ControlResponse{}, fmt.Errorf("partial fill must complete before restart")
	}
	before := c.backendA
	if before.Sequence != c.partial.Sequence || before.StateHash != c.partial.EventHash || c.partial.SnapshotSequence != 4 {
		return ControlResponse{}, fmt.Errorf("backend A lacks snapshot-plus-tail proof")
	}
	if err := c.stopBackend(false); err != nil {
		return ControlResponse{}, err
	}
	before.Exited = true
	c.backendA = before
	if err := c.startBackend(ctx, GenerationB, c.partial.Sequence); err != nil {
		return ControlResponse{}, err
	}
	afterState, err := AuditDatabase(ctx, c.pool)
	if err != nil {
		return ControlResponse{}, err
	}
	status, err := c.client.GetStatus(ctx, &tradingv1.GetStatusRequest{MarketId: MarketID})
	if err != nil {
		return ControlResponse{}, err
	}
	after := processEvidence(GenerationB, c.child.pid, status)
	if after.PID == before.PID || after.Sequence != before.Sequence || after.StateHash != before.StateHash || afterState.Digest != c.partial.Digest {
		return ControlResponse{}, fmt.Errorf("backend B did not restore the exact durable state")
	}
	c.backendB = after
	c.restore = RestoreEvidence{Before: before, After: after, SameSequence: true, SameStateHash: true}
	c.phase = PhaseRestoredB
	return ControlResponse{SchemaVersion: SchemaControl, Action: "restart_backend", Phase: c.phase, Generation: GenerationB,
		BackendPID: after.PID, Sequence: after.Sequence, StateHash: after.StateHash, PreviousBackend: &before, CurrentBackend: &after, Restored: true}, nil
}

func (c *Coordinator) stateHTTP(writer http.ResponseWriter, request *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := AuditDatabase(request.Context(), c.pool)
	if err != nil {
		writeJSON(writer, http.StatusConflict, map[string]string{"code": "state_unavailable"})
		return
	}
	writeJSON(writer, http.StatusOK, State{SchemaVersion: SchemaState, ObservedAt: time.Now().UTC(), Phase: c.phase,
		Generation: c.generation, BackendPID: c.child.pid, Database: state})
}

func (c *Coordinator) evidenceHTTP(writer http.ResponseWriter, request *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	runtime, err := c.refreshRuntimeEvidence(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusConflict, map[string]string{"code": "runtime_evidence_unavailable"})
		return
	}
	fixture, _ := c.fixtureEvidence(request.Context())
	writeJSON(writer, http.StatusOK, Evidence{SchemaVersion: SchemaEvidence, ObservedAt: time.Now().UTC(), Postgres: c.postgresEvidence(runtime),
		CoordinatorPID: os.Getpid(), FixturePID: c.config.FixturePID, VuePID: c.config.VuePID, BackendA: c.backendA, BackendB: c.backendB,
		Restore: c.restore, Replay: c.replay, Reference: c.reference, Fixture: fixture, Partial: c.partial, Final: c.final,
		Quality: c.quality.evidence(), Spy: c.spy.snapshot(), CleanupArmed: true})
}

func (c *Coordinator) refreshRuntimeEvidence(parent context.Context) (ProcessEvidence, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	status, err := c.client.GetStatus(ctx, &tradingv1.GetStatusRequest{MarketId: MarketID})
	if err != nil {
		return ProcessEvidence{}, fmt.Errorf("read active backend status: %w", err)
	}
	proof := processEvidence(c.generation, c.child.pid, status)
	if c.generation == GenerationB {
		c.backendB = proof
	} else {
		c.backendA = proof
	}
	return proof, nil
}

func (c *Coordinator) controlSnapshot(action, scenario string) ControlResponse {
	active := c.activeBackend()
	return ControlResponse{SchemaVersion: SchemaControl, Action: action, Phase: c.phase, Generation: c.generation,
		BackendPID: active.PID, Sequence: active.Sequence, StateHash: active.StateHash, Scenario: scenario}
}

func (c *Coordinator) activeBackend() ProcessEvidence {
	if c.generation == GenerationB {
		return c.backendB
	}
	return c.backendA
}

func (c *Coordinator) setFixtureScenario(ctx context.Context, domain, scenario string) error {
	return c.setFixtureScenarioAt(ctx, domain, scenario, time.Time{})
}

func (c *Coordinator) setFixtureScenarioAt(ctx context.Context, domain, scenario string, observedAt time.Time) error {
	body := map[string]string{"domain": domain, "scenario": scenario}
	if !observedAt.IsZero() {
		body["observed_at"] = observedAt.UTC().Format(time.RFC3339Nano)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.FixtureOrigin+"/__fixture/control", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.fixtureHTTPClient().Do(request)
	if err != nil {
		return fmt.Errorf("control loopback fixture: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("loopback fixture rejected scenario")
	}
	time.Sleep(160 * time.Millisecond)
	return nil
}

type countingQualityReporter struct {
	monitor *quality.Monitor
	reads   *atomic.Uint64
}

func (r countingQualityReporter) Reports() (quality.ReportSet, error) {
	if r.reads != nil {
		r.reads.Add(1)
	}
	return r.monitor.Reports()
}

func (c *Coordinator) fixtureEvidence(ctx context.Context) (FixtureEvidence, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.FixtureOrigin+"/__fixture/evidence", nil)
	if err != nil {
		return FixtureEvidence{}, err
	}
	response, err := c.fixtureHTTPClient().Do(request)
	if err != nil {
		return FixtureEvidence{}, err
	}
	defer response.Body.Close()
	var evidence FixtureEvidence
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if response.StatusCode != http.StatusOK || decoder.Decode(&evidence) != nil {
		return FixtureEvidence{}, fmt.Errorf("invalid fixture evidence")
	}
	return evidence, nil
}

func (c *Coordinator) fixtureHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(c.fixtureCA) {
		return &http.Client{Timeout: time.Nanosecond}
	}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func validateLoopbackOrigin(origin string) error {
	return validateExactLoopbackOrigin(origin, "https")
}

func validateExactLoopbackOrigin(origin, scheme string) error {
	request, err := http.NewRequest(http.MethodGet, origin, nil)
	if err != nil || request.URL.Scheme != scheme || request.URL.User != nil || request.URL.Path != "" || request.URL.RawQuery != "" || request.URL.Fragment != "" {
		return fmt.Errorf("origin must be exact credential-free loopback %s", scheme)
	}
	host, port, err := net.SplitHostPort(request.URL.Host)
	if err != nil || port == "" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("origin must use an IP loopback address")
	}
	return nil
}

func (c *Coordinator) referenceHTTP(writer http.ResponseWriter, _ *http.Request) {
	c.spy.providerReads.Add(1)
	writeJSON(writer, http.StatusOK, c.reference.Before)
}

func (c *Coordinator) observeTrading(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path != "/api/v1/trading/events/ws" {
			if !c.acquireStableGatewayRead(request.Context()) {
				writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"code": "backend_transition_timeout"})
				return
			}
			defer c.gatewaySwitch.RUnlock()
		}
		if request.Method != http.MethodPost {
			next.ServeHTTP(writer, request)
			return
		}
		isSubmit := request.URL.Path == "/api/v1/trading/orders"
		isCancel := strings.HasPrefix(request.URL.Path, "/api/v1/trading/orders/") && strings.HasSuffix(request.URL.Path, "/cancel")
		if !isSubmit && !isCancel {
			next.ServeHTTP(writer, request)
			return
		}
		cancelRequestID := ""
		if isCancel {
			var err error
			cancelRequestID, err = copyCancelRequestID(writer, request)
			if err != nil {
				writeJSON(writer, http.StatusBadRequest, map[string]string{"code": "invalid_cancel_evidence"})
				return
			}
		}
		capture := &statusCapture{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(capture, request)
		if capture.status < 200 || capture.status >= 300 {
			return
		}
		c.spy.browserMutations.Add(1)
		if !isCancel {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		state, err := AuditDatabase(context.Background(), c.pool)
		if err != nil {
			c.spy.forbidden.Add(1)
			return
		}
		runtime, err := c.refreshRuntimeEvidence(context.Background())
		if err != nil || runtime.Sequence != state.Sequence || runtime.StateHash != state.EventHash {
			c.spy.forbidden.Add(1)
			return
		}
		c.replay.CancelRequests++
		if c.replay.CancelRequests == 1 {
			c.replay.CancelRequestID = cancelRequestID
		} else if c.replay.CancelRequestID != cancelRequestID {
			c.spy.forbidden.Add(1)
			return
		}
		if c.replay.CancelRequests == 1 {
			c.replay.OriginalSequence, c.replay.OriginalStatus = state.Sequence, state.Orders[PartialClientOrderID].Status
			c.replay.BeforeCounts, c.replay.BeforeDigest, c.replay.BeforeEventHash = state.Counts, state.Digest, state.EventHash
			copy := state
			c.final = &copy
			c.phase = PhaseCanceled
		} else if c.replay.CancelRequests == 2 {
			c.replay.ReplaySequence, c.replay.ReplayStatus = state.Sequence, state.Orders[PartialClientOrderID].Status
			c.replay.AfterCounts, c.replay.AfterDigest, c.replay.AfterEventHash = state.Counts, state.Digest, state.EventHash
			c.replay.NoDelta = c.replay.OriginalSequence == c.replay.ReplaySequence && c.replay.BeforeDigest == c.replay.AfterDigest && c.replay.BeforeEventHash == c.replay.AfterEventHash
		}
	})
}

func (c *Coordinator) acquireStableGatewayRead(ctx context.Context) bool {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if c.gatewaySwitch.TryRLock() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func copyCancelRequestID(writer http.ResponseWriter, request *http.Request) (string, error) {
	payload, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 2048))
	if err != nil {
		return "", err
	}
	request.Body = io.NopCloser(bytes.NewReader(payload))
	var body struct {
		RequestID string `json:"request_id"`
		AccountID string `json:"account_id,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF || body.RequestID == "" {
		return "", fmt.Errorf("invalid cancel body")
	}
	return body.RequestID, nil
}

func (c *Coordinator) mountLegacyReads(router chi.Router) {
	respond := func(result any, total *int) http.HandlerFunc {
		return func(writer http.ResponseWriter, _ *http.Request) {
			c.spy.legacyReads.Add(1)
			body := map[string]any{"code": 2000, "message": "success", "result": result}
			if total != nil {
				body["total"] = *total
			}
			writeJSON(writer, http.StatusOK, body)
		}
	}
	router.Post("/api/v1/get_market_insights", respond(map[string]any{}, nil))
	router.Post("/api/v1/get_system_overview", respond(map[string]any{"dw_status": "unavailable"}, nil))
	router.Post("/api/v2/get_asset_dashboard", func(writer http.ResponseWriter, _ *http.Request) {
		c.spy.legacyReads.Add(1)
		c.spy.providerReads.Add(1)
		observed := c.reference.Before.ObservedAt.UnixMilli()
		writeJSON(writer, http.StatusOK, map[string]any{"code": 2000, "total": 1, "result": []any{map[string]any{
			"rank": 1, "asset_id": "bitcoin", "asset_symbol": "BTC", "asset_name": "Bitcoin",
			"price_usd": map[string]any{"available": true, "value": "60000"}, "composite_price_usd": map[string]any{"available": true, "value": "60000"},
			"freshness_status": "fresh", "confidence": 1, "quality": "high", "available": true,
			"observed_at": observed, "last_success_at": observed, "provider_updated_at": observed, "index_updated_at": observed,
		}}})
	})
	market := map[string]any{"asset_id": "bitcoin", "market_id": "binance:BTC-USDT", "market_code": "binance:BTC-USDT",
		"provider": "binance", "venue": "binance", "symbol": "BTCUSDT", "base_asset": "BTC", "quote_asset": "USDT",
		"market_type": "spot", "has_kline": true, "status": "online", "available": true,
		"price": map[string]any{"available": true, "value": "60000"}, "freshness_status": "fresh"}
	for _, path := range []string{"/api/v2/get_asset_markets", "/api/v2/get_asset_venues"} {
		router.Post(path, respond([]any{market}, nil))
	}
	router.Post("/api/v1/get_klines", func(writer http.ResponseWriter, _ *http.Request) {
		c.spy.legacyReads.Add(1)
		c.spy.providerReads.Add(1)
		first := c.reference.Before.ObservedAt.Truncate(time.Minute).Add(-time.Minute).UnixMilli()
		writeJSON(writer, http.StatusOK, map[string]any{"code": 2000, "result": []any{
			map[string]any{"timestamp": first, "open": "60000", "high": "60010", "low": "59990", "close": "60000", "volume": "1"},
			map[string]any{"timestamp": first + 60_000, "open": "60000", "high": "60010", "low": "59990", "close": "60000", "volume": "1"},
		}})
	})
}

func (c *Coordinator) postgresEvidence(runtime ProcessEvidence) PostgresEvidence {
	result := c.config.Postgres
	state, err := AuditDatabase(context.Background(), c.pool)
	if err == nil {
		result.HeadSequence, result.SnapshotSequence = state.Sequence, state.SnapshotSequence
		result.SnapshotMatchesHead = state.Sequence == state.SnapshotSequence && state.EventHash == state.SnapshotHash
		result.SnapshotMatchesRuntime = state.EventHash == runtime.StateHash && state.Sequence == runtime.Sequence
	}
	return result
}

func (c *Coordinator) writeManifest() error {
	manifest := Manifest{SchemaVersion: SchemaManifest, APIOrigin: c.APIOrigin(), ReadyURL: c.APIOrigin() + "/__full-stack/ready",
		ControlURL: c.APIOrigin() + "/__full-stack/control", StateURL: c.APIOrigin() + "/__full-stack/state", EvidenceURL: c.APIOrigin() + "/__full-stack/evidence",
		CoordinatorPID: os.Getpid(), FixturePID: c.config.FixturePID, VuePID: c.config.VuePID, Postgres: c.config.Postgres, Backend: c.backendA}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	temporary := c.config.ManifestPath + ".tmp"
	if err := os.WriteFile(temporary, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, c.config.ManifestPath)
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host, _, err := net.SplitHostPort(request.RemoteAddr)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			http.NotFound(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (w *statusCapture) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func decodeOne(writer http.ResponseWriter, request *http.Request, destination any) bool {
	if request.Header.Get("Content-Type") != "application/json" {
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 2048))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination) == nil && decoder.Decode(&struct{}{}) == io.EOF
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func processEvidence(generation Generation, pid int, status *tradingv1.StatusResponse) ProcessEvidence {
	sequence, _ := strconv.ParseUint(status.Sequence, 10, 64)
	return ProcessEvidence{Generation: generation, PID: pid, Sequence: sequence, StateHash: status.StateHash}
}

func deterministicReference(observedAt time.Time) ReferenceFact {
	fact := ReferenceFact{Source: "full-stack deterministic market-data fixture", MarketID: MarketID, Price: "60000",
		ObservedAt: observedAt.UTC().Truncate(time.Millisecond)}
	payload := fact.Source + "|" + fact.MarketID + "|" + fact.Price + "|" + fact.ObservedAt.Format(time.RFC3339Nano)
	sum := sha256.Sum256([]byte(payload))
	fact.Hash = hex.EncodeToString(sum[:])
	return fact
}
