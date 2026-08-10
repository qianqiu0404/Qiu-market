package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/marketmaker"
	"github.com/the-web3/s78-market-services/trading/netutil"
	"github.com/the-web3/s78-market-services/trading/outbox"
	readmodelpostgres "github.com/the-web3/s78-market-services/trading/readmodel/postgres"
	"github.com/the-web3/s78-market-services/trading/recovery"
	"github.com/the-web3/s78-market-services/trading/reference"
	"github.com/the-web3/s78-market-services/trading/reliability"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

const (
	demoMakerAccount = domain.AccountID("system:demo-maker")
	demoMakerBTC     = int64(1_000_000_000)     // 10 virtual BTC
	demoMakerUSDT    = int64(1_000_000_000_000) // 1,000,000 virtual USDT
)

type Config struct {
	PostgresURL        string
	GRPCAddress        string
	DemoMakerEnabled   bool
	DiskPath           string
	MinWriteBytes      int64
	CursorHMACCurrent  string
	CursorHMACPrevious string
	// SnapshotEvery optionally selects the standard runner snapshot cadence.
	// Zero preserves the production default. Isolated recovery verification may
	// use a smaller positive cadence to prove snapshot-plus-event-tail replay.
	SnapshotEvery      uint64
	RecoveryGate       bool
	RecoveryProvenance recovery.Provenance
}

// Backend is the only integrated process that owns the in-memory matching
// state. It exposes loopback gRPC but deliberately does not run an HTTP server.
type Backend struct {
	config   Config
	shutdown context.CancelCauseFunc

	pool               *pgxpool.Pool
	runner             *tradingruntime.MarketRunner
	grpcServer         *grpc.Server
	listener           net.Listener
	maker              *marketmaker.Maker
	makerEnabled       bool
	makerSource        marketmaker.ReferenceSource
	makerConfig        marketmaker.Config
	makerMu            sync.Mutex
	makerCancel        context.CancelFunc
	makerDone          chan struct{}
	publisher          *outbox.Publisher
	publisherCancel    context.CancelFunc
	publisherDone      chan struct{}
	recovery           *recovery.Coordinator
	recoveryProof      recovery.Proof
	recoveryHead       postgresstore.Cursor
	recoveryIncidentMu sync.Mutex
	recoveryIncident   error

	started  atomic.Bool
	stopped  atomic.Bool
	stopOnce sync.Once
	stopErr  error
}

func New(
	ctx context.Context,
	config Config,
	shutdown context.CancelCauseFunc,
) (*Backend, error) {
	if config.RecoveryGate {
		var err error
		config.RecoveryProvenance, err = recovery.BindExecutableSourceDigest(
			config.RecoveryProvenance,
		)
		if err != nil {
			return nil, fmt.Errorf("bind trading recovery executable: %w", err)
		}
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, config.PostgresURL)
	if err != nil {
		return nil, fmt.Errorf("open trading PostgreSQL pool: %w", err)
	}
	cleanupPool := true
	defer func() {
		if cleanupPool {
			pool.Close()
		}
	}()
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping trading PostgreSQL: %w", err)
	}
	if err := postgresstore.VerifySchema(ctx, pool); err != nil {
		return nil, err
	}

	market := domain.DefaultBTCUSDTMarket()
	var recoveryCoordinator *recovery.Coordinator
	var recoveryEpochID string
	if config.RecoveryGate {
		recoveryStore, recoveryErr := recovery.NewPostgresStore(pool)
		if recoveryErr != nil {
			return nil, recoveryErr
		}
		recoveryCoordinator, recoveryErr = recovery.NewCoordinator(
			recoveryStore, market.ID, config.RecoveryProvenance,
		)
		if recoveryErr != nil {
			return nil, recoveryErr
		}
		started, beginErr := recoveryCoordinator.Begin(ctx)
		if beginErr != nil {
			recoveryErr = beginErr
			return nil, fmt.Errorf("begin fail-closed trading recovery: %w", recoveryErr)
		}
		recoveryEpochID = started.EpochID
		if _, recoveryErr = recoveryCoordinator.Advance(
			ctx,
			recovery.PhaseDependenciesReady,
			recovery.Proof{},
		); recoveryErr != nil {
			return nil, recoveryErr
		}
		if _, recoveryErr = recoveryCoordinator.Advance(
			ctx,
			recovery.PhaseTradingReplay,
			recovery.Proof{},
		); recoveryErr != nil {
			return nil, recoveryErr
		}
	}
	persistence, err := postgresstore.New(ctx, pool, market)
	if err != nil {
		return nil, err
	}
	queries, err := readmodelpostgres.New(pool, market)
	if err != nil {
		return nil, fmt.Errorf("create Trade V1 reader: %w", err)
	}
	cursors, err := tradingserver.ParseCursorConfig(
		config.CursorHMACCurrent,
		config.CursorHMACPrevious,
	)
	if err != nil {
		return nil, fmt.Errorf("configure Trade V1 cursors: %w", err)
	}
	runnerConfig := runnerConfigFor(config)
	if recoveryCoordinator != nil {
		runnerConfig.WriteGate = recoveryCoordinator
	}
	runner, err := tradingruntime.NewMarketRunner(
		ctx,
		market,
		persistence,
		persistence,
		runnerConfig,
	)
	if err != nil {
		if recoveryCoordinator != nil {
			_, _ = recoveryCoordinator.Fail(ctx, recovery.PhaseManualReview, err)
		}
		return nil, err
	}
	cleanupRunner := true
	defer func() {
		if cleanupRunner {
			closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = runner.Close(closeContext)
			cancel()
		}
	}()
	if recoveryCoordinator != nil && config.DemoMakerEnabled {
		if err := bootstrapDemoMaker(ctx, runner, market, true, recoveryEpochID); err != nil {
			_, _ = recoveryCoordinator.Fail(ctx, recovery.PhaseManualReview, err)
			return nil, err
		}
		if err := cancelRecoveredDemoMakerOrders(
			ctx,
			runner,
			recoveryCoordinator,
		); err != nil {
			_, _ = recoveryCoordinator.Fail(ctx, recovery.PhaseManualReview, err)
			return nil, err
		}
	}
	var (
		recoveryProof recovery.Proof
		recoveryHead  postgresstore.Cursor
	)
	if recoveryCoordinator != nil {
		if _, err = recoveryCoordinator.Advance(
			ctx,
			recovery.PhaseReconciling,
			recovery.Proof{},
		); err != nil {
			return nil, err
		}
		proof, proofErr := reliability.ProveRecovery(ctx, market, persistence, persistence)
		if proofErr != nil {
			_, _ = recoveryCoordinator.Fail(ctx, recovery.PhaseManualReview, proofErr)
			return nil, proofErr
		}
		recoveryHead, proofErr = persistence.EventHead(ctx)
		if proofErr != nil {
			_, _ = recoveryCoordinator.Fail(ctx, recovery.PhaseManualReview, proofErr)
			return nil, proofErr
		}
		projection, found, projectionErr := persistence.ProjectionCheckpoint(ctx)
		if projectionErr != nil {
			_, _ = recoveryCoordinator.Fail(ctx, recovery.PhaseManualReview, projectionErr)
			return nil, projectionErr
		}
		projectionCaughtUp := recoveryHead == (postgresstore.Cursor{}) && !found
		if found {
			projectionCaughtUp = projection.Sequence == recoveryHead.Sequence &&
				projection.EventIndex == recoveryHead.EventIndex
		}
		recoveryProof = recovery.Proof{
			RuntimeSequence:    proof.RestoredSequence,
			StateHash:          proof.RestoredStateHash,
			LedgerBalanced:     true,
			EventContinuous:    true,
			ProjectionCaughtUp: projectionCaughtUp,
		}
		if !projectionCaughtUp {
			proofErr = fmt.Errorf(
				"trading projection checkpoint is behind event head %d/%d",
				recoveryHead.Sequence,
				recoveryHead.EventIndex,
			)
			_, _ = recoveryCoordinator.Fail(ctx, recovery.PhaseManualReview, proofErr)
			return nil, proofErr
		}
	}
	publisher, err := outbox.New(persistence, outbox.DefaultConfig())
	if err != nil {
		return nil, err
	}
	rpcConfig := tradingserver.DefaultConfig()
	rpcConfig.Recovery = recoveryCoordinator
	rpcConfig.Queries = queries
	rpcConfig.Cursors = cursors
	rpcService, err := tradingserver.New(
		runner,
		tradingserver.NewPostgresEventSource(persistence),
		rpcConfig,
		publisher,
	)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", config.GRPCAddress)
	if err != nil {
		return nil, fmt.Errorf("listen trading gRPC on %s: %w", config.GRPCAddress, err)
	}
	cleanupListener := true
	defer func() {
		if cleanupListener {
			_ = listener.Close()
		}
	}()
	grpcServer := grpc.NewServer()
	tradingv1.RegisterTradingServiceServer(grpcServer, rpcService)

	var (
		maker       *marketmaker.Maker
		makerSource marketmaker.ReferenceSource
		makerConfig marketmaker.Config
	)
	if config.DemoMakerEnabled {
		requestPrefix, prefixErr := randomRequestPrefix()
		if prefixErr != nil {
			return nil, prefixErr
		}
		if recoveryCoordinator == nil {
			if err := bootstrapDemoMaker(ctx, runner, market, false, requestPrefix); err != nil {
				return nil, err
			}
		}
		source, err := reference.NewPostgresSource(pool, market.QuoteScale)
		if err != nil {
			return nil, err
		}
		makerSource = source
		if config.MinWriteBytes > 0 {
			makerSource = diskAwareReferenceSource{
				next:    source,
				path:    config.DiskPath,
				minimum: config.MinWriteBytes,
			}
		}
		makerConfig = marketmaker.DefaultConfig()
		makerConfig.AccountID = demoMakerAccount
		makerConfig.RequestPrefix = requestPrefix
		if recoveryCoordinator == nil {
			maker, err = marketmaker.New(market, runner, makerSource, makerConfig)
			if err != nil {
				return nil, err
			}
		}
	}

	cleanupPool = false
	cleanupRunner = false
	cleanupListener = false
	return &Backend{
		config:        config,
		shutdown:      shutdown,
		pool:          pool,
		runner:        runner,
		grpcServer:    grpcServer,
		listener:      listener,
		maker:         maker,
		makerEnabled:  config.DemoMakerEnabled,
		makerSource:   makerSource,
		makerConfig:   makerConfig,
		publisher:     publisher,
		recovery:      recoveryCoordinator,
		recoveryProof: recoveryProof,
		recoveryHead:  recoveryHead,
	}, nil
}

func runnerConfigFor(config Config) tradingruntime.Config {
	result := tradingruntime.DefaultConfig()
	if config.SnapshotEvery > 0 {
		result.SnapshotEvery = config.SnapshotEvery
	}
	return result
}

func validateConfig(config Config) error {
	if config.PostgresURL == "" {
		return fmt.Errorf("trading PostgreSQL URL is required")
	}
	if !netutil.IsIPLoopbackAddress(config.GRPCAddress) {
		return fmt.Errorf("trading gRPC must bind to an explicit IP loopback address")
	}
	if config.MinWriteBytes < 0 ||
		(config.MinWriteBytes > 0 && config.DiskPath == "") {
		return fmt.Errorf("demo maker disk guard requires a path and non-negative floor")
	}
	if config.RecoveryGate {
		if _, err := recovery.NormalizeProvenance(config.RecoveryProvenance); err != nil {
			return fmt.Errorf("invalid trading recovery provenance: %w", err)
		}
	}
	if _, err := tradingserver.ParseCursorConfig(
		config.CursorHMACCurrent,
		config.CursorHMACPrevious,
	); err != nil {
		return fmt.Errorf("invalid Trade V1 cursor configuration: %w", err)
	}
	return nil
}

type diskAwareReferenceSource struct {
	next    marketmaker.ReferenceSource
	path    string
	minimum int64
}

func (s diskAwareReferenceSource) Current(ctx context.Context) (marketmaker.Reference, error) {
	var fileSystem syscall.Statfs_t
	if err := syscall.Statfs(s.path, &fileSystem); err != nil {
		return marketmaker.Reference{}, fmt.Errorf("check demo maker disk capacity: %w", err)
	}
	available := int64(fileSystem.Bavail) * int64(fileSystem.Bsize)
	if available < s.minimum {
		return marketmaker.Reference{}, fmt.Errorf(
			"demo maker paused: free disk %d is below %d",
			available,
			s.minimum,
		)
	}
	return s.next.Current(ctx)
}

func (b *Backend) Start(ctx context.Context) error {
	if !b.started.CompareAndSwap(false, true) {
		return fmt.Errorf("trading backend already started")
	}
	publisherContext, publisherCancel := context.WithCancel(ctx)
	b.publisherCancel = publisherCancel
	b.publisherDone = make(chan struct{})
	go func() {
		defer close(b.publisherDone)
		b.publisher.Run(publisherContext)
	}()
	go func() {
		err := b.grpcServer.Serve(b.listener)
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) && !b.stopped.Load() {
			wrapped := fmt.Errorf("trading gRPC server: %w", err)
			log.Error("trading gRPC stopped unexpectedly", "err", wrapped)
			if b.shutdown != nil {
				b.shutdown(wrapped)
			}
		}
	}()
	if b.recovery == nil && b.makerConfigured() {
		if err := b.startMaker(ctx); err != nil {
			return err
		}
	}
	if b.recovery != nil {
		go b.completeLocalRecovery(publisherContext)
		go b.monitorRecoveryLifecycle(publisherContext)
	}
	log.Info(
		"virtual trading backend ready",
		"grpc", b.listener.Addr().String(),
		"market", domain.DefaultBTCUSDTMarket().ID,
		"demo_maker", b.makerRunning(),
	)
	return nil
}

func (b *Backend) completeLocalRecovery(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		delivery := b.publisher.Status()
		if delivery.State == "ready" &&
			delivery.Checkpoint.Sequence == b.recoveryHead.Sequence &&
			delivery.Checkpoint.EventIndex == b.recoveryHead.EventIndex {
			proof := b.recoveryProof
			proof.OutboxCaughtUp = true
			if _, err := b.recovery.Advance(ctx, recovery.PhaseReadOnly, proof); err != nil {
				_, _ = b.recovery.Fail(ctx, recovery.PhaseManualReview, err)
				return
			}
			if _, err := b.recovery.Advance(
				ctx,
				recovery.PhaseTransportWarmup,
				proof,
			); err != nil {
				_, _ = b.recovery.Fail(ctx, recovery.PhaseManualReview, err)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Backend) monitorRecoveryLifecycle(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := b.reconcileRecoveryLifecycle(ctx); err != nil && ctx.Err() == nil {
			log.Warn("trading recovery lifecycle remains fail-closed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Backend) reconcileRecoveryLifecycle(ctx context.Context) error {
	if b.recovery == nil {
		return nil
	}
	current, err := b.recovery.Status(ctx)
	if err != nil {
		b.markRecoveryIncident(fmt.Errorf("recovery status unavailable: %w", err))
		_ = b.stopMaker(ctx, true)
		return fmt.Errorf("read recovery lifecycle: %w", err)
	}
	if current.ContinuityUncertain {
		b.markRecoveryIncident(errors.New(
			"recovery store continuity is uncertain; current epoch is invalid",
		))
	}
	if incident := b.currentRecoveryIncident(); incident != nil &&
		current.Phase == recovery.PhaseWritable {
		if _, failErr := b.recovery.Fail(ctx, recovery.PhaseOffline, incident); failErr != nil {
			return errors.Join(incident, failErr)
		}
		_ = b.stopMaker(ctx, true)
		return incident
	}
	if current.Phase != recovery.PhaseWritable || !current.WritesEnabled {
		if current.Phase == recovery.PhaseTransportWarmup &&
			!current.ContinuityUncertain {
			b.clearRecoveryIncident()
		}
		return b.stopMaker(ctx, true)
	}
	runnerStatus := b.runner.Status()
	delivery := b.publisher.Status()
	if incidentAfter(runnerStatus.LastIncidentAt, current.UpdatedAt) ||
		delivery.LastIncidentAt.After(current.UpdatedAt) {
		b.markRecoveryIncident(fmt.Errorf(
			"writable dependency reported an incident after promotion: runner_at=%s outbox_at=%s",
			runnerStatus.LastIncidentAt,
			delivery.LastIncidentAt.UTC().Format(time.RFC3339Nano),
		))
		return b.reconcileRecoveryLifecycle(ctx)
	}
	if runnerStatus.State != tradingruntime.StateReady || runnerStatus.LastError != "" ||
		delivery.State != "ready" || delivery.LastError != "" {
		cause := fmt.Errorf(
			"writable dependency regressed: runner=%s runner_error=%q outbox=%s outbox_error=%q",
			runnerStatus.State,
			runnerStatus.LastError,
			delivery.State,
			delivery.LastError,
		)
		b.markRecoveryIncident(cause)
		_, _ = b.recovery.Fail(ctx, recovery.PhaseOffline, cause)
		_ = b.stopMaker(ctx, true)
		return cause
	}
	if b.makerEnabled {
		return b.startMaker(ctx)
	}
	return nil
}

func (b *Backend) markRecoveryIncident(err error) {
	if err == nil {
		return
	}
	b.recoveryIncidentMu.Lock()
	if b.recoveryIncident == nil {
		b.recoveryIncident = err
	}
	b.recoveryIncidentMu.Unlock()
}

func (b *Backend) currentRecoveryIncident() error {
	b.recoveryIncidentMu.Lock()
	defer b.recoveryIncidentMu.Unlock()
	return b.recoveryIncident
}

func (b *Backend) clearRecoveryIncident() {
	b.recoveryIncidentMu.Lock()
	b.recoveryIncident = nil
	b.recoveryIncidentMu.Unlock()
}

func incidentAfter(value string, threshold time.Time) bool {
	if value == "" {
		return false
	}
	observed, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || observed.After(threshold)
}

func (b *Backend) startMaker(ctx context.Context) error {
	b.makerMu.Lock()
	defer b.makerMu.Unlock()
	if b.makerDone != nil {
		select {
		case <-b.makerDone:
			b.makerDone = nil
			b.makerCancel = nil
			b.maker = nil
		default:
			return nil
		}
	}
	if !b.makerEnabled && b.maker == nil {
		return nil
	}
	if b.maker == nil {
		maker, err := marketmaker.New(
			domain.DefaultBTCUSDTMarket(),
			b.runner,
			b.makerSource,
			b.makerConfig,
		)
		if err != nil {
			return err
		}
		b.maker = maker
	}
	makerContext, makerCancel := context.WithCancel(ctx)
	done := make(chan struct{})
	maker := b.maker
	b.makerCancel = makerCancel
	b.makerDone = done
	go func() {
		defer close(done)
		if err := maker.Run(makerContext); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("virtual demo maker terminated after infrastructure failure", "err", err)
			if b.recovery != nil {
				failureContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _ = b.recovery.Fail(failureContext, recovery.PhaseOffline, err)
				cancel()
			}
		}
	}()
	return nil
}

func (b *Backend) stopMaker(ctx context.Context, safety bool) error {
	if !b.makerEnabled && !b.makerConfigured() {
		return nil
	}
	b.makerMu.Lock()
	cancel := b.makerCancel
	done := b.makerDone
	b.makerCancel = nil
	b.makerDone = nil
	b.maker = nil
	b.makerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	var result error
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			result = errors.Join(result, fmt.Errorf("stop demo maker: %w", ctx.Err()))
		}
	}
	if safety && b.makerEnabled {
		if err := b.cancelDemoMakerOrders(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("safety cancel demo maker: %w", err))
		}
	}
	return result
}

func (b *Backend) makerConfigured() bool {
	b.makerMu.Lock()
	defer b.makerMu.Unlock()
	return b.maker != nil || b.makerEnabled
}

func (b *Backend) makerRunning() bool {
	b.makerMu.Lock()
	defer b.makerMu.Unlock()
	if b.makerDone == nil {
		return false
	}
	select {
	case <-b.makerDone:
		return false
	default:
		return true
	}
}

func (b *Backend) cancelDemoMakerOrders(ctx context.Context) error {
	return cancelRecoveredDemoMakerOrders(ctx, b.runner, b.recovery)
}

func cancelRecoveredDemoMakerOrders(
	ctx context.Context,
	runner *tradingruntime.MarketRunner,
	coordinator *recovery.Coordinator,
) error {
	current, err := coordinator.Status(ctx)
	if err != nil {
		return err
	}
	orders, err := runner.Orders(demoMakerAccount, true)
	if err != nil {
		return err
	}
	var result error
	for _, order := range orders {
		_, cancelErr := runner.CancelSafety(ctx, domain.CancelOrder{
			RequestID: fmt.Sprintf(
				"recovery-%s-cancel-%s",
				current.EpochID,
				order.ID,
			),
			AccountID: demoMakerAccount,
			OrderID:   order.ID,
		})
		if cancelErr != nil {
			result = errors.Join(result, cancelErr)
		}
	}
	return result
}

func (b *Backend) Stop(ctx context.Context) error {
	b.stopOnce.Do(func() {
		b.stopped.Store(true)
		if err := b.stopMaker(ctx, b.recovery != nil); err != nil {
			b.stopErr = errors.Join(b.stopErr, err)
		}

		grpcStopped := make(chan struct{})
		go func() {
			b.grpcServer.GracefulStop()
			close(grpcStopped)
		}()
		select {
		case <-grpcStopped:
		case <-ctx.Done():
			b.grpcServer.Stop()
			b.stopErr = errors.Join(b.stopErr, fmt.Errorf("stop trading gRPC: %w", ctx.Err()))
		}

		if err := b.runner.Close(ctx); err != nil {
			b.stopErr = errors.Join(b.stopErr, fmt.Errorf("close trading runner: %w", err))
		}
		if b.publisherCancel != nil {
			b.publisherCancel()
		}
		if b.publisherDone != nil {
			select {
			case <-b.publisherDone:
			case <-ctx.Done():
				b.stopErr = errors.Join(
					b.stopErr,
					fmt.Errorf("stop outbox publisher: %w", ctx.Err()),
				)
			}
		}
		_ = b.listener.Close()
		b.pool.Close()
		log.Info("virtual trading backend stopped")
	})
	return b.stopErr
}

func (b *Backend) Stopped() bool {
	return b.stopped.Load()
}

// GRPCAddress returns the actual loopback listener address. It is useful when
// callers request port 0 for isolated integration tests.
func (b *Backend) GRPCAddress() string {
	if b.listener == nil {
		return ""
	}
	return b.listener.Addr().String()
}

func bootstrapDemoMaker(
	ctx context.Context,
	runner *tradingruntime.MarketRunner,
	market domain.Market,
	safety bool,
	requestScope string,
) error {
	if requestScope == "" {
		return errors.New("demo maker bootstrap request scope is required")
	}
	requests := []domain.FundRequest{
		{
			RequestID: "demo-maker-bootstrap-btc-" + requestScope,
			AccountID: demoMakerAccount,
			Asset:     market.BaseAsset,
			Amount:    demoMakerBTC,
		},
		{
			RequestID: "demo-maker-bootstrap-usdt-" + requestScope,
			AccountID: demoMakerAccount,
			Asset:     market.QuoteAsset,
			Amount:    demoMakerUSDT,
		},
	}
	for _, request := range requests {
		balance, err := runner.Balance(request.AccountID, request.Asset)
		if err != nil {
			return fmt.Errorf("read virtual demo maker asset %s: %w", request.Asset, err)
		}
		if balance.Available > 0 || balance.Held > 0 {
			continue
		}
		var fundErr error
		if safety {
			_, fundErr = runner.FundSafety(ctx, request)
		} else {
			_, fundErr = runner.Fund(ctx, request)
		}
		if fundErr != nil {
			return fmt.Errorf("fund virtual demo maker asset %s: %w", request.Asset, fundErr)
		}
		funded, balanceErr := runner.Balance(request.AccountID, request.Asset)
		if balanceErr != nil {
			return fmt.Errorf("re-read virtual demo maker asset %s: %w", request.Asset, balanceErr)
		}
		if funded.Available <= 0 && funded.Held <= 0 {
			return fmt.Errorf("virtual demo maker asset %s remains depleted", request.Asset)
		}
	}
	return nil
}

func randomRequestPrefix() (string, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate demo-maker request prefix: %w", err)
	}
	return "demo-maker-" + hex.EncodeToString(value[:]), nil
}
