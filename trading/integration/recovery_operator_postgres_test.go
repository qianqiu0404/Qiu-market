package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/domain"
	tradingoperator "github.com/the-web3/s78-market-services/trading/operator"
	"github.com/the-web3/s78-market-services/trading/recovery"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

func TestRecoveryMigrationsAndConcurrentPostgresPromotionCAS(t *testing.T) {
	dsn := isolatedRecoveryTestDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	pool, secondPool := isolatedRecoverySchemaPools(t, ctx, dsn)
	applyMigrationOnce(t, ctx, pool, "2026082100023.sql")
	market := domain.DefaultBTCUSDTMarket()
	if _, err := postgresstore.New(ctx, pool, market); err != nil {
		t.Fatal(err)
	}
	applyMigrationOnce(t, ctx, pool, "2026082400026.sql")

	now := time.Now().UTC()
	legacyEpoch := "legacy-writable-without-transport-evidence"
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading_recovery_epoch (
			schema_version, market_id, epoch_id, phase, runtime_sequence,
			state_hash, ledger_balanced, event_continuous,
			projection_caught_up, outbox_caught_up, transport_healthy,
			writes_enabled, last_error, version, started_at, updated_at
		) VALUES (1,$1,$2,'writable',0,$3,TRUE,TRUE,TRUE,TRUE,TRUE,TRUE,'',1,$4,$4)
	`, market.ID, legacyEpoch, strings.Repeat("a", 64), now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading_recovery_current (market_id, epoch_id, updated_at)
		VALUES ($1,$2,$3)
	`, market.ID, legacyEpoch, now); err != nil {
		t.Fatal(err)
	}

	// Migration 27 explicitly closes legacy writable epochs which predate bound
	// transport evidence. This is an existing migration contract, not a test-only
	// policy invented here.
	applyMigrationOnce(t, ctx, pool, "2026082500027.sql")
	var (
		phase            recovery.Phase
		writesEnabled    bool
		transportHealthy bool
		lastError        string
	)
	if err := pool.QueryRow(ctx, `
		SELECT phase, writes_enabled, transport_healthy, last_error
		FROM trading_recovery_epoch WHERE market_id=$1 AND epoch_id=$2
	`, market.ID, legacyEpoch).Scan(
		&phase, &writesEnabled, &transportHealthy, &lastError,
	); err != nil {
		t.Fatal(err)
	}
	if phase != recovery.PhaseManualReview || writesEnabled || transportHealthy ||
		!strings.Contains(lastError, "legacy writable epoch lacks bound transport evidence") {
		t.Fatalf("legacy writable migration result phase=%s writes=%v transport=%v error=%q",
			phase, writesEnabled, transportHealthy, lastError)
	}

	// The real migration constraint must reject a writable row whose claimed
	// maximum gap exceeds the recovery contract.
	_, err := pool.Exec(ctx, `
		UPDATE trading_recovery_epoch
		SET phase='writable', writes_enabled=TRUE, transport_healthy=TRUE,
		    transport_sample_count=7,
		    transport_first_sample_at=$3,
		    transport_last_sample_at=$4,
		    transport_maximum_gap_ms=8001,
		    transport_evidence_sha256=$5
		WHERE market_id=$1 AND epoch_id=$2
	`, market.ID, legacyEpoch, now.Add(-31*time.Second), now, strings.Repeat("b", 64))
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
		t.Fatalf("invalid writable evidence constraint error = %v", err)
	}

	// Applying migration 27 again is expected to be safe after its legacy-data
	// rewrite and constraint installation.
	applyMigrationOnce(t, ctx, pool, "2026082500027.sql")
	for _, table := range []string{"trading_recovery_current", "trading_recovery_epoch"} {
		if _, err := pool.Exec(ctx, `DELETE FROM `+table+` WHERE market_id=$1`, market.ID); err != nil {
			t.Fatal(err)
		}
	}

	primaryStore, err := recovery.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := recovery.NewCoordinator(primaryStore, market.ID)
	if err != nil {
		t.Fatal(err)
	}
	warmup := advancePostgresRecoveryToWarmup(t, ctx, coordinator)

	secondaryStore, err := recovery.NewPostgresStore(secondPool)
	if err != nil {
		t.Fatal(err)
	}
	barrier := newTwoPartyLoadBarrier()
	firstContender, _ := recovery.NewCoordinator(
		&barrierRecoveryStore{next: primaryStore, barrier: barrier}, market.ID,
	)
	secondContender, _ := recovery.NewCoordinator(
		&barrierRecoveryStore{next: secondaryStore, barrier: barrier}, market.ID,
	)
	binding := recoveryBinding(warmup)
	evidence := validTransportEvidence()
	results := make(chan error, 2)
	var contenders sync.WaitGroup
	for _, contender := range []*recovery.Coordinator{firstContender, secondContender} {
		contenders.Add(1)
		go func(candidate *recovery.Coordinator) {
			defer contenders.Done()
			_, promoteErr := candidate.Promote(ctx, binding, evidence)
			results <- promoteErr
		}(contender)
	}
	contenders.Wait()
	close(results)
	var succeeded, conflicted int
	for promoteErr := range results {
		switch {
		case promoteErr == nil:
			succeeded++
		case errors.Is(promoteErr, recovery.ErrVersionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent promotion error = %v", promoteErr)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent promotion winners=%d CAS_conflicts=%d", succeeded, conflicted)
	}
	freshCoordinator, _ := recovery.NewCoordinator(primaryStore, market.ID)
	current, err := freshCoordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != recovery.PhaseWritable || !current.WritesEnabled ||
		current.EpochID != warmup.EpochID || current.Version != warmup.Version+1 {
		t.Fatalf("concurrent promotion current status = %+v", current)
	}
}

func TestTransportProbeLoopbackGRPCPostgresVertical(t *testing.T) {
	dsn := isolatedRecoveryTestDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
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
		t.Fatal("operator vertical refuses to reuse an existing BTC-USDT stream")
	}
	if _, err := postgresstore.New(ctx, pool, domain.DefaultBTCUSDTMarket()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		cleanupRecoveryMarket(t, cleanupContext, pool)
	})

	stack := startRecoveryIntegratedStack(t, ctx, dsn)
	defer stack.stop(t)
	coordinator, _ := openRecoveryCoordinator(t, pool)
	warmup := waitRecoveryPhase(t, ctx, coordinator, recovery.PhaseTransportWarmup)
	binding := recoveryBinding(warmup)
	publicStatus := readRecoveryStatusDocument(
		t, stack.server.URL+"/api/v1/trading/recovery/status",
	)

	for _, testCase := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "tampered state hash",
			mutate: func(document map[string]any) {
				document["state_hash"] = strings.Repeat("f", 64)
			},
		},
		{
			name: "stale epoch",
			mutate: func(document map[string]any) {
				document["epoch_id"] = "stale-" + binding.EpochID
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			document := cloneJSONDocument(publicStatus)
			testCase.mutate(document)
			fixture := recoveryStatusFixture(t, document)
			defer fixture.Close()
			probe := newOperatorProbe(
				t, ctx, binding,
				fixture.URL+"/api/v1/trading/recovery/status",
				stack.backend.GRPCAddress(),
			)
			defer probe.Close()
			if _, samples, err := tradingoperator.CollectTransportEvidence(
				ctx, binding, probe, tradingoperator.DefaultObservationPolicy(),
			); err == nil || len(samples) != 0 {
				t.Fatalf("invalid public status evidence samples=%d err=%v", len(samples), err)
			}
		})
	}

	probe := newOperatorProbe(
		t, ctx, binding,
		stack.server.URL+"/api/v1/trading/recovery/status",
		stack.backend.GRPCAddress(),
	)
	defer probe.Close()
	evidence, samples, err := tradingoperator.CollectTransportEvidence(
		ctx, binding, probe, tradingoperator.DefaultObservationPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) < recovery.MinimumTransportSamples ||
		evidence.SampleCount != len(samples) ||
		evidence.LastSampleAt.Sub(evidence.FirstSampleAt) < recovery.MinimumTransportWindow ||
		len(evidence.EvidenceSHA256) != 64 {
		t.Fatalf("operator transport evidence=%+v samples=%d", evidence, len(samples))
	}
	promoted, err := probe.Promote(ctx, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !promoted.GetWritesEnabled() || promoted.GetPhase() != string(recovery.PhaseWritable) ||
		promoted.GetEpochId() != binding.EpochID {
		t.Fatalf("operator gRPC promotion = %+v", promoted)
	}
	durable, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Phase != recovery.PhaseWritable || !durable.WritesEnabled ||
		durable.Transport.SampleCount != evidence.SampleCount ||
		durable.Transport.EvidenceSHA256 != evidence.EvidenceSHA256 ||
		durable.Transport.MaximumGapMS != evidence.MaximumGapMS {
		t.Fatalf("durable operator promotion = %+v evidence=%+v", durable, evidence)
	}
}

func isolatedRecoveryTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("S78_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("S78_TEST_POSTGRES_DSN is not set")
	}
	if os.Getenv("S78_TEST_POSTGRES_ISOLATED") != "1" {
		t.Skip("recovery integration requires S78_TEST_POSTGRES_ISOLATED=1")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(config.ConnConfig.Database, "qiu_recovery_it_") {
		t.Fatalf("isolated recovery tests require qiu_recovery_it_* database, have %q",
			config.ConnConfig.Database)
	}
	return dsn
}

func isolatedRecoverySchemaPools(
	t *testing.T,
	ctx context.Context,
	dsn string,
) (*pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	admin := openPool(t, ctx, dsn)
	schemaName := fmt.Sprintf("qiu_recovery_migration_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	first, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	second, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		first.Close()
		admin.Close()
		t.Fatal(err)
	}
	if err := first.Ping(ctx); err != nil {
		second.Close()
		first.Close()
		admin.Close()
		t.Fatal(err)
	}
	if err := second.Ping(ctx); err != nil {
		second.Close()
		first.Close()
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		second.Close()
		first.Close()
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupContext, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated recovery schema: %v", err)
		}
		admin.Close()
	})
	return first, second
}

func applyMigrationOnce(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	migrationName string,
) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve recovery operator integration test path")
	}
	migrationPath := filepath.Join(filepath.Dir(filename), "..", "..", "migrations", migrationName)
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply %s: %v", migrationName, err)
	}
}

func advancePostgresRecoveryToWarmup(
	t *testing.T,
	ctx context.Context,
	coordinator *recovery.Coordinator,
) recovery.Status {
	t.Helper()
	current, err := coordinator.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proof := recovery.Proof{
		RuntimeSequence: 0, StateHash: strings.Repeat("c", 64),
		LedgerBalanced: true, EventContinuous: true,
		ProjectionCaughtUp: true, OutboxCaughtUp: true,
	}
	for _, phase := range []recovery.Phase{
		recovery.PhaseDependenciesReady,
		recovery.PhaseTradingReplay,
		recovery.PhaseReconciling,
		recovery.PhaseReadOnly,
		recovery.PhaseTransportWarmup,
	} {
		phaseProof := recovery.Proof{}
		if phase == recovery.PhaseReadOnly || phase == recovery.PhaseTransportWarmup {
			phaseProof = proof
		}
		current, err = coordinator.Advance(ctx, phase, phaseProof)
		if err != nil {
			t.Fatalf("advance PostgreSQL recovery to %s: %v", phase, err)
		}
	}
	return current
}

type twoPartyLoadBarrier struct {
	mu       sync.Mutex
	arrived  int
	released chan struct{}
}

func newTwoPartyLoadBarrier() *twoPartyLoadBarrier {
	return &twoPartyLoadBarrier{released: make(chan struct{})}
}

func (b *twoPartyLoadBarrier) arrive(ctx context.Context) error {
	b.mu.Lock()
	b.arrived++
	if b.arrived == 2 {
		close(b.released)
	}
	b.mu.Unlock()
	select {
	case <-b.released:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type barrierRecoveryStore struct {
	next    recovery.Store
	barrier *twoPartyLoadBarrier
}

func (s *barrierRecoveryStore) Load(
	ctx context.Context,
	marketID domain.MarketID,
) (recovery.Status, bool, error) {
	current, found, err := s.next.Load(ctx, marketID)
	if err != nil || !found {
		return current, found, err
	}
	if err := s.barrier.arrive(ctx); err != nil {
		return recovery.Status{}, false, err
	}
	return current, true, nil
}

func (s *barrierRecoveryStore) Save(
	ctx context.Context,
	expectedVersion uint64,
	next recovery.Status,
) error {
	return s.next.Save(ctx, expectedVersion, next)
}

func readRecoveryStatusDocument(t *testing.T, endpoint string) map[string]any {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("read recovery status HTTP %d", response.StatusCode)
	}
	var document map[string]any
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func cloneJSONDocument(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func recoveryStatusFixture(t *testing.T, document map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet ||
			request.URL.Path != "/api/v1/trading/recovery/status" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(writer).Encode(document); err != nil {
			t.Errorf("encode recovery status fixture: %v", err)
		}
	}))
}

func newOperatorProbe(
	t *testing.T,
	ctx context.Context,
	binding recovery.Binding,
	statusURL string,
	grpcAddress string,
) *tradingoperator.TransportProbe {
	t.Helper()
	connectContext, connectCancel := context.WithTimeout(ctx, 5*time.Second)
	defer connectCancel()
	probe, err := tradingoperator.NewTransportProbe(
		connectContext, binding, statusURL, grpcAddress, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return probe
}
