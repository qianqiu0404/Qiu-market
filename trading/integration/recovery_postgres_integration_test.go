package integration_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/gateway"
	"github.com/the-web3/s78-market-services/trading/recovery"
	"github.com/the-web3/s78-market-services/trading/reliability"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingservice "github.com/the-web3/s78-market-services/trading/service"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

// TestPostgresRecoveryGateAcrossOutageAndProcessRebuild is deliberately one
// vertical scenario. It prevents separate unit assertions from being combined
// into a recovery claim that was never exercised against one PostgreSQL truth.
func TestPostgresRecoveryGateAcrossOutageAndProcessRebuild(t *testing.T) {
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not set")
	}
	if os.Getenv("S78_TEST_POSTGRES_ISOLATED") != "1" {
		t.Skip("recovery integration requires S78_TEST_POSTGRES_ISOLATED=1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := openPool(t, ctx, dsn)
	t.Cleanup(pool.Close)
	applyCanonicalMigration(t, ctx, pool)
	applyRecoveryMigrations(t, ctx, pool)
	if err := postgresstore.VerifySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var existing bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM trading_market WHERE market_id=$1)`,
		integratedMarketID,
	).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing {
		t.Fatal("recovery integration refuses to reuse an existing BTC-USDT stream")
	}
	market := domain.DefaultBTCUSDTMarket()
	persistence, err := postgresstore.New(ctx, pool, market)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanupRecoveryMarket(t, cleanupContext, pool)
	})

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}

	first := startRecoveryIntegratedStack(t, ctx, dsn)
	defer first.stop(t)
	coordinator, recoveryStore := openRecoveryCoordinator(t, pool)
	firstWarmup := waitRecoveryPhase(t, ctx, coordinator, recovery.PhaseTransportWarmup)
	loginRecoveryClient(t, client, first.server.URL)
	csrf := cookieValue(t, jar, first.server.URL, "s78_trading_csrf")
	fundBody := map[string]any{
		"request_id": "recovery-original-fund-id",
		"asset":      "USDT",
		"amount":     "25",
	}
	assertRecoveryWriteBlocked(t, client, first.server.URL, fundBody, csrf)
	firstWritable := promoteRecoveryEpoch(t, ctx, coordinator, firstWarmup)

	funded := new(tradingv1.CommandResult)
	doJSON(t, client, http.MethodPost,
		first.server.URL+"/api/v1/trading/admin/fund",
		fundBody, csrf, http.StatusOK, funded)
	if funded.Sequence != "1" {
		t.Fatalf("initial promoted fund = %+v", funded)
	}
	waitDurableCursors(t, ctx, persistence, 1)

	// Fault injection: the random test database rejects new connections and all
	// existing sessions are terminated. The gateway must fail closed rather
	// than treating the previous writable epoch as an in-memory permission.
	restoreDatabase := suspendIsolatedTestDatabase(t, dsn)
	// Hold the outage across several 250ms publisher/lifecycle observations.
	// This makes the injected dependency regression explicit and reproducible,
	// rather than relying on a sub-poll-interval connection flap.
	time.Sleep(1500 * time.Millisecond)
	assertRecoveryWriteBlocked(t, client, first.server.URL, fundBody, csrf)
	restoreDatabase()
	offline := waitRecoveryPhase(t, ctx, coordinator, recovery.PhaseOffline)
	if offline.WritesEnabled {
		t.Fatalf("database outage left recovery epoch writable: %+v", offline)
	}
	waitDurableCursors(t, ctx, persistence, 1)
	assertRecoveryWriteBlocked(t, client, first.server.URL, fundBody, csrf)

	first.stop(t)
	beforeRestart := provePostgresRecovery(t, ctx, pool, persistence)

	// A valid event stream is not enough if the stored snapshot hash is no
	// longer the state it claims to represent. Startup must persist a
	// fail-closed manual-review epoch rather than expose a partially restored
	// runner.
	if _, err := pool.Exec(ctx, `
		UPDATE trading_snapshot SET state_hash=repeat('0', 64) WHERE market_id=$1
	`, integratedMarketID); err != nil {
		t.Fatal(err)
	}
	assertBackendRecoveryFailsClosed(t, ctx, dsn, pool, "snapshot state hash mismatch")
	if _, err := pool.Exec(ctx, `
		UPDATE trading_snapshot SET state_hash=$2 WHERE market_id=$1
	`, integratedMarketID, beforeRestart.SnapshotHash); err != nil {
		t.Fatal(err)
	}

	// A projection behind the immutable event head must likewise fail closed.
	// The checkpoint is restored before the successful process reconstruction.
	if _, err := pool.Exec(ctx, `
		UPDATE trading_projection_checkpoint
		SET event_index=0, updated_at=clock_timestamp()
		WHERE market_id=$1
	`, integratedMarketID); err != nil {
		t.Fatal(err)
	}
	assertBackendRecoveryFailsClosed(t, ctx, dsn, pool, "projection checkpoint mismatch")
	if _, err := pool.Exec(ctx, `
		UPDATE trading_projection_checkpoint
		SET sequence=$2, event_index=$3, updated_at=clock_timestamp()
		WHERE market_id=$1
	`, integratedMarketID,
		beforeRestart.ProjectionCheckpoint.Sequence,
		beforeRestart.ProjectionCheckpoint.EventIndex,
	); err != nil {
		t.Fatal(err)
	}

	// Process reconstruction starts a new epoch even though the previous epoch
	// was writable. Until the new proof is promoted, the original ID is blocked.
	second := startRecoveryIntegratedStack(t, ctx, dsn)
	defer second.stop(t)
	secondWarmup := waitRecoveryPhase(t, ctx, coordinator, recovery.PhaseTransportWarmup)
	csrf = cookieValue(t, jar, second.server.URL, "s78_trading_csrf")
	assertRecoveryWriteBlocked(t, client, second.server.URL, fundBody, csrf)
	if secondWarmup.EpochID == firstWritable.EpochID {
		t.Fatalf("process rebuild reused recovery epoch %s", secondWarmup.EpochID)
	}
	if _, err := coordinator.Promote(
		ctx,
		recoveryBinding(firstWarmup),
		validTransportEvidence(firstWarmup.Provenance),
	); !errors.Is(err, recovery.ErrBindingMismatch) {
		t.Fatalf("stale recovery epoch public promotion error = %v", err)
	}

	// An actor holding the former current version cannot promote it after the
	// current pointer has atomically switched to the new process epoch.
	stalePromotion := firstWritable
	stalePromotion.Phase = recovery.PhaseWritable
	stalePromotion.WritesEnabled = true
	stalePromotion.Version++
	stalePromotion.UpdatedAt = time.Now().UTC()
	if err := recoveryStore.Save(ctx, firstWritable.Version, stalePromotion); !errors.Is(err, recovery.ErrVersionConflict) {
		t.Fatalf("stale recovery epoch promotion error = %v", err)
	}
	stillCurrent, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stillCurrent.EpochID != secondWarmup.EpochID || stillCurrent.WritesEnabled {
		t.Fatalf("stale epoch changed current recovery state: %+v", stillCurrent)
	}

	promoteRecoveryEpoch(t, ctx, coordinator, stillCurrent)
	afterProcessRecovery := new(tradingv1.CommandResult)
	doJSON(t, client, http.MethodPost,
		second.server.URL+"/api/v1/trading/admin/fund",
		fundBody, csrf, http.StatusOK, afterProcessRecovery)
	assertSameCommandResult(t, funded, afterProcessRecovery)

	second.stop(t)
	afterRestart := provePostgresRecovery(t, ctx, pool, persistence)
	if afterRestart != beforeRestart {
		t.Fatalf("process rebuild changed durable recovery proof: before=%+v after=%+v",
			beforeRestart, afterRestart)
	}
	var epochCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM trading_recovery_epoch WHERE market_id=$1`,
		integratedMarketID,
	).Scan(&epochCount); err != nil {
		t.Fatal(err)
	}
	if epochCount != 4 {
		t.Fatalf("recovery epoch history count = %d, want 4", epochCount)
	}
}

func startRecoveryIntegratedStack(
	t *testing.T,
	parent context.Context,
	dsn string,
) *integratedStack {
	t.Helper()
	appContext, appCancel := context.WithCancel(parent)
	server := httptest.NewUnstartedServer(nil)
	productionOrigin := "https://" + server.Listener.Addr().String()
	backend, err := tradingservice.New(appContext, tradingservice.Config{
		PostgresURL:        dsn,
		GRPCAddress:        "127.0.0.1:0",
		DemoMakerEnabled:   false,
		CursorHMACCurrent:  integratedCursorKey,
		RecoveryGate:       true,
		RecoveryProvenance: integratedRecoveryProvenance(productionOrigin),
	}, func(error) { appCancel() })
	if err != nil {
		appCancel()
		t.Fatal(err)
	}
	if err := backend.Start(appContext); err != nil {
		appCancel()
		t.Fatal(err)
	}
	tradingGateway, err := gateway.New(appContext, gateway.Config{
		PostgresURL:        dsn,
		GRPCAddress:        backend.GRPCAddress(),
		BindAddress:        "127.0.0.1:0",
		AllowedOrigins:     []string{integratedOrigin},
		LocalAuth:          true,
		SecureCookies:      false,
		RecoveryGate:       true,
		RecoveryProvenance: integratedRecoveryProvenance(productionOrigin),
	})
	if err != nil {
		stopBackend(t, backend)
		appCancel()
		t.Fatal(err)
	}
	provenance := integratedRecoveryProvenance(productionOrigin)
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Qiu-Market-Provenance", "VERIFIED")
		writer.Header().Set("X-Qiu-Market-Deployment-ID", provenance.DeploymentID)
		writer.Header().Set("X-Qiu-Market-Deployment-URL", provenance.DeploymentURL)
		writer.Header().Set("X-Qiu-Market-Release-Commit", provenance.ReleaseCommit)
		tradingGateway.Handler().ServeHTTP(writer, request)
	})
	server.StartTLS()
	return &integratedStack{
		backend: backend,
		gateway: tradingGateway,
		server:  server,
		cancel:  appCancel,
	}
}

func applyRecoveryMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve recovery integration test path")
	}
	for _, migrationName := range []string{
		"2026082300025.sql",
		"2026082500027.sql",
		"2026082600028.sql",
		"2026082700029.sql",
	} {
		migrationPath := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", migrationName)
		migration, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatal(err)
		}
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := pool.Exec(ctx, string(migration)); err != nil {
				t.Fatalf("apply %s attempt %d: %v", migrationName, attempt+1, err)
			}
		}
	}
}

func openRecoveryCoordinator(
	t *testing.T,
	pool *pgxpool.Pool,
) (*recovery.Coordinator, *recovery.PostgresStore) {
	t.Helper()
	store, err := recovery.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := recovery.NewCoordinator(store, integratedMarketID)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator, store
}

func waitRecoveryPhase(
	t *testing.T,
	ctx context.Context,
	coordinator *recovery.Coordinator,
	want recovery.Phase,
) recovery.Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last recovery.Status
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = coordinator.Status(ctx)
		if lastErr == nil && last.Phase == want {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("recovery phase did not reach %s: last=%+v err=%v", want, last, lastErr)
	return recovery.Status{}
}

func promoteRecoveryEpoch(
	t *testing.T,
	ctx context.Context,
	coordinator *recovery.Coordinator,
	current recovery.Status,
) recovery.Status {
	t.Helper()
	if current.Phase != recovery.PhaseTransportWarmup {
		t.Fatalf("promotion requires transport warmup, have %+v", current)
	}
	promoted, err := coordinator.Promote(
		ctx,
		recoveryBinding(current),
		validTransportEvidence(current.Provenance),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !promoted.WritesEnabled {
		t.Fatalf("promoted recovery epoch is not writable: status=%+v", promoted)
	}
	if err := coordinator.RequireWritable(ctx); err != nil {
		t.Fatalf("promoted recovery epoch is not writable: status=%+v err=%v", promoted, err)
	}
	return promoted
}

func recoveryBinding(status recovery.Status) recovery.Binding {
	return recovery.Binding{
		MarketID:        status.MarketID,
		EpochID:         status.EpochID,
		Version:         status.Version,
		RuntimeSequence: status.Proof.RuntimeSequence,
		StateHash:       status.Proof.StateHash,
		Provenance:      status.Provenance,
	}
}

func validTransportEvidence(values ...recovery.Provenance) recovery.TransportEvidence {
	now := time.Now().UTC()
	provenance := integratedRecoveryProvenance("https://qiu-market.example")
	if len(values) == 1 {
		provenance = values[0]
	}
	return recovery.TransportEvidence{
		SampleCount:    recovery.MinimumTransportSamples,
		FirstSampleAt:  now.Add(-recovery.MinimumTransportWindow - time.Second),
		LastSampleAt:   now,
		MaximumGapMS:   int64((recovery.MaximumTransportGap - time.Second) / time.Millisecond),
		EvidenceSHA256: strings.Repeat("a", 64),
		Provenance:     provenance,
	}
}

func integratedRecoveryProvenance(origin string) recovery.Provenance {
	digest, err := recovery.ExecutableSourceDigest()
	if err != nil {
		panic(err)
	}
	return recovery.Provenance{
		ProductionOrigin: origin,
		DeploymentID:     "dpl_integrationdeployment",
		DeploymentURL:    "https://qiu-market-integration-deployment.vercel.app",
		ReleaseCommit:    strings.Repeat("d", 40),
		SourceDigest:     digest,
	}
}

func loginRecoveryClient(t *testing.T, client *http.Client, serverURL string) {
	t.Helper()
	var login struct {
		Principal auth.Principal `json:"principal"`
	}
	doJSON(t, client, http.MethodPost,
		serverURL+"/api/v1/trading/auth/local",
		map[string]any{}, "", http.StatusOK, &login)
	if login.Principal.AccountID != "github:qianqiu0404" || !login.Principal.Admin {
		t.Fatalf("recovery local principal = %+v", login.Principal)
	}
}

func assertRecoveryWriteBlocked(
	t *testing.T,
	client *http.Client,
	serverURL string,
	body map[string]any,
	csrf string,
) {
	t.Helper()
	var response map[string]any
	doJSON(t, client, http.MethodPost,
		serverURL+"/api/v1/trading/admin/fund",
		body, csrf, http.StatusServiceUnavailable, &response)
	if response["code"] != "recovery_in_progress" {
		t.Fatalf("blocked recovery response = %+v", response)
	}
}

func assertSameCommandResult(t *testing.T, want, got *tradingv1.CommandResult) {
	t.Helper()
	if !proto.Equal(want, got) {
		t.Fatalf("idempotent result = %+v, want %+v", got, want)
	}
}

func assertBackendRecoveryFailsClosed(
	t *testing.T,
	ctx context.Context,
	dsn string,
	pool *pgxpool.Pool,
	label string,
) {
	t.Helper()
	appContext, appCancel := context.WithCancel(ctx)
	backend, err := tradingservice.New(appContext, tradingservice.Config{
		PostgresURL:        dsn,
		GRPCAddress:        "127.0.0.1:0",
		DemoMakerEnabled:   false,
		CursorHMACCurrent:  integratedCursorKey,
		RecoveryGate:       true,
		RecoveryProvenance: integratedRecoveryProvenance("https://qiu-market.example"),
	}, func(error) { appCancel() })
	appCancel()
	if err == nil {
		stopBackend(t, backend)
		t.Fatalf("%s unexpectedly constructed a writable-capable backend", label)
	}
	coordinator, _ := openRecoveryCoordinator(t, pool)
	status, statusErr := coordinator.Status(ctx)
	if statusErr != nil {
		t.Fatalf("%s recovery status: %v", label, statusErr)
	}
	if status.Phase != recovery.PhaseManualReview || status.WritesEnabled ||
		status.LastError == "" {
		t.Fatalf("%s did not fail closed: %+v (startup error %v)", label, status, err)
	}
	if writableErr := coordinator.RequireWritable(ctx); !errors.Is(writableErr, recovery.ErrWriteBlocked) {
		t.Fatalf("%s writable gate error = %v", label, writableErr)
	}
}

type durableRecoveryProof struct {
	Sequence             uint64
	StateHash            string
	SnapshotSequence     int64
	SnapshotHash         string
	ProjectionCheckpoint postgresstore.Cursor
	OutboxCheckpoint     postgresstore.Cursor
	EventBatches         int
	LedgerEntries        int
}

func provePostgresRecovery(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	persistence *postgresstore.Store,
) durableRecoveryProof {
	t.Helper()
	proof, err := reliability.ProveRecovery(
		ctx,
		domain.DefaultBTCUSDTMarket(),
		persistence,
		persistence,
	)
	if err != nil {
		t.Fatal(err)
	}
	for asset, net := range proof.Ledger.AssetNet {
		if net != 0 {
			t.Fatalf("ledger proof asset %s net = %d", asset, net)
		}
	}
	eventHead, err := persistence.EventHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projection, found, err := persistence.ProjectionCheckpoint(ctx)
	if err != nil || !found {
		t.Fatalf("projection checkpoint found=%v err=%v", found, err)
	}
	outbox, found, err := persistence.OutboxCheckpoint(ctx)
	if err != nil || !found {
		t.Fatalf("outbox checkpoint found=%v err=%v", found, err)
	}
	if projection.Sequence != eventHead.Sequence || projection.EventIndex != eventHead.EventIndex {
		t.Fatalf("projection checkpoint=%+v event head=%+v", projection, eventHead)
	}
	if outbox != eventHead {
		t.Fatalf("outbox checkpoint=%+v event head=%+v", outbox, eventHead)
	}

	result := durableRecoveryProof{
		Sequence:             proof.RestoredSequence,
		StateHash:            proof.RestoredStateHash,
		ProjectionCheckpoint: postgresstore.Cursor{Sequence: projection.Sequence, EventIndex: projection.EventIndex},
		OutboxCheckpoint:     outbox,
	}
	var ledgerImbalances int
	var unpublished int
	if err := pool.QueryRow(ctx, `
		SELECT snapshot.sequence, snapshot.state_hash,
		       (SELECT count(*) FROM trading_event_batch WHERE market_id=$1),
		       (SELECT count(*) FROM trading_ledger_entry WHERE market_id=$1),
		       (SELECT count(*) FROM trading_outbox WHERE market_id=$1 AND published_at IS NULL),
		       (SELECT count(*) FROM (
		          SELECT transaction_id, asset
		          FROM trading_ledger_entry
		          WHERE market_id=$1
		          GROUP BY transaction_id, asset
		          HAVING sum(amount) <> 0
		        ) AS imbalanced)
		FROM trading_snapshot snapshot
		WHERE snapshot.market_id=$1
	`, integratedMarketID).Scan(
		&result.SnapshotSequence,
		&result.SnapshotHash,
		&result.EventBatches,
		&result.LedgerEntries,
		&unpublished,
		&ledgerImbalances,
	); err != nil {
		t.Fatal(err)
	}
	if result.Sequence != eventHead.Sequence || result.StateHash == "" ||
		result.SnapshotSequence != int64(result.Sequence) ||
		result.SnapshotHash != result.StateHash ||
		result.EventBatches != 1 || result.LedgerEntries != 2 ||
		unpublished != 0 || ledgerImbalances != 0 {
		t.Fatalf("durable PostgreSQL recovery proof = %+v unpublished=%d ledger_imbalances=%d",
			result, unpublished, ledgerImbalances)
	}
	return result
}

func waitDurableCursors(
	t *testing.T,
	ctx context.Context,
	persistence *postgresstore.Store,
	wantSequence uint64,
) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		head, headErr := persistence.EventHead(ctx)
		projection, projectionFound, projectionErr := persistence.ProjectionCheckpoint(ctx)
		outbox, outboxFound, outboxErr := persistence.OutboxCheckpoint(ctx)
		if headErr == nil && projectionErr == nil && outboxErr == nil &&
			head.Sequence == wantSequence && projectionFound && outboxFound &&
			projection.Sequence == head.Sequence && projection.EventIndex == head.EventIndex &&
			outbox == head {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("durable cursors did not converge at sequence %d", wantSequence)
}

func suspendIsolatedTestDatabase(t *testing.T, dsn string) func() {
	t.Helper()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := config.ConnConfig.Database
	if !strings.HasPrefix(databaseName, "qiu_recovery_it_") {
		t.Skipf("PostgreSQL fault injection requires qiu_recovery_it_* database, have %q", databaseName)
	}
	adminConfig := config.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(context.Background(), adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(context.Background()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	quotedDatabase := pgx.Identifier{databaseName}.Sanitize()
	if _, err := admin.Exec(context.Background(),
		"ALTER DATABASE "+quotedDatabase+" WITH ALLOW_CONNECTIONS false"); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.Exec(context.Background(),
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`,
		databaseName,
	); err != nil {
		_, _ = admin.Exec(context.Background(),
			"ALTER DATABASE "+quotedDatabase+" WITH ALLOW_CONNECTIONS true")
		admin.Close()
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		if _, restoreErr := admin.Exec(context.Background(),
			"ALTER DATABASE "+quotedDatabase+" WITH ALLOW_CONNECTIONS true"); restoreErr != nil {
			t.Errorf("restore isolated PostgreSQL connections: %v", restoreErr)
		}
		admin.Close()
	}
	t.Cleanup(restore)
	return restore
}

func cleanupRecoveryMarket(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	for _, table := range []string{
		"trading_recovery_current",
		"trading_recovery_epoch",
	} {
		if _, err := pool.Exec(ctx,
			`DELETE FROM `+table+` WHERE market_id=$1`, integratedMarketID); err != nil {
			t.Errorf("clean %s: %v", table, err)
		}
	}
	cleanupMarket(t, ctx, pool)
}
