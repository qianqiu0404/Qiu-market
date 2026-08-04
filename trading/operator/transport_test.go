package operator

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/outbox"
	"github.com/the-web3/s78-market-services/trading/recovery"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

type fixedDeliveryStatus struct {
	value outbox.Status
}

func (s fixedDeliveryStatus) Status() outbox.Status { return s.value }

func TestCollectTransportEvidenceRequiresConsecutiveExactSamples(t *testing.T) {
	binding := testBinding()
	first := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	samples := make([]Sample, recovery.MinimumTransportSamples)
	for index := range samples {
		samples[index] = healthySample(binding, first.Add(time.Duration(index)*5*time.Second))
	}
	evidence, observed, err := CollectTransportEvidence(
		context.Background(),
		binding,
		&fixtureSource{samples: samples},
		testPolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != recovery.MinimumTransportSamples ||
		evidence.SampleCount != recovery.MinimumTransportSamples ||
		evidence.LastSampleAt.Sub(evidence.FirstSampleAt) != recovery.MinimumTransportWindow ||
		evidence.MaximumGapMS != 5000 || len(evidence.EvidenceSHA256) != 64 {
		t.Fatalf("transport evidence = %+v samples=%d", evidence, len(observed))
	}
}

func TestCollectTransportEvidenceFailsImmediatelyOnBindingChange(t *testing.T) {
	binding := testBinding()
	first := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	samples := make([]Sample, recovery.MinimumTransportSamples)
	for index := range samples {
		samples[index] = healthySample(binding, first.Add(time.Duration(index)*5*time.Second))
	}
	samples[3].RecoveryVersion++
	evidence, observed, err := CollectTransportEvidence(
		context.Background(),
		binding,
		&fixtureSource{samples: samples},
		testPolicy(),
	)
	if err == nil || evidence != (recovery.TransportEvidence{}) || len(observed) != 3 {
		t.Fatalf("binding change evidence=%+v samples=%d err=%v", evidence, len(observed), err)
	}
}

func TestTransportProbeSamplesAndPromotesThroughRealLoopbackGRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	market := domain.DefaultBTCUSDTMarket()
	coordinator, err := recovery.NewCoordinator(recovery.NewMemoryStore(), market.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(
		ctx, market, memory, memory, tradingruntime.DefaultConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()
	fingerprint := runner.Status()
	proof := recovery.Proof{
		RuntimeSequence: fingerprint.Sequence, StateHash: fingerprint.StateHash,
		LedgerBalanced: true, EventContinuous: true,
		ProjectionCaughtUp: true, OutboxCaughtUp: true,
	}
	var warmup recovery.Status
	for _, phase := range []recovery.Phase{
		recovery.PhaseDependenciesReady, recovery.PhaseTradingReplay,
		recovery.PhaseReconciling, recovery.PhaseReadOnly,
		recovery.PhaseTransportWarmup,
	} {
		phaseProof := recovery.Proof{}
		if phase == recovery.PhaseReadOnly || phase == recovery.PhaseTransportWarmup {
			phaseProof = proof
		}
		warmup, err = coordinator.Advance(ctx, phase, phaseProof)
		if err != nil {
			t.Fatal(err)
		}
	}
	binding := recovery.Binding{
		MarketID: warmup.MarketID, EpochID: warmup.EpochID, Version: warmup.Version,
		RuntimeSequence: warmup.Proof.RuntimeSequence, StateHash: warmup.Proof.StateHash,
	}
	config := tradingserver.DefaultConfig()
	config.Recovery = coordinator
	service, err := tradingserver.New(
		runner, nil, config,
		fixedDeliveryStatus{value: outbox.Status{
			State: "ready", Checkpoint: postgresstore.Cursor{Sequence: binding.RuntimeSequence},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	tradingv1.RegisterTradingServiceServer(grpcServer, service)
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	statusServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(publicRecoveryStatus{
			MarketID: string(binding.MarketID), EpochID: binding.EpochID,
			Phase:           string(recovery.PhaseTransportWarmup),
			RuntimeSequence: strconv.FormatUint(binding.RuntimeSequence, 10),
			StateHash:       binding.StateHash, LedgerBalanced: true, EventContinuous: true,
			ProjectionCaughtUp: true, OutboxCaughtUp: true,
			Version: strconv.FormatUint(binding.Version, 10),
		})
	}))
	defer statusServer.Close()
	probe, err := NewTransportProbe(
		ctx, binding,
		statusServer.URL+"/api/v1/trading/recovery/status",
		listener.Addr().String(), statusServer.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	sample, err := probe.Sample(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSample(binding, sample); err != nil {
		t.Fatalf("real transport sample = %+v err=%v", sample, err)
	}
	last := time.Now().UTC()
	promoted, err := probe.Promote(ctx, recovery.TransportEvidence{
		SampleCount:   recovery.MinimumTransportSamples,
		FirstSampleAt: last.Add(-recovery.MinimumTransportWindow), LastSampleAt: last,
		MaximumGapMS: 5000, EvidenceSHA256: strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if promoted.GetPhase() != string(recovery.PhaseWritable) || !promoted.GetWritesEnabled() {
		t.Fatalf("real transport promotion = %+v", promoted)
	}
}

type fixtureSource struct {
	samples []Sample
	index   int
}

func (s *fixtureSource) Sample(context.Context) (Sample, error) {
	if s.index >= len(s.samples) {
		return Sample{}, context.DeadlineExceeded
	}
	result := s.samples[s.index]
	s.index++
	return result, nil
}

func testBinding() recovery.Binding {
	return recovery.Binding{
		MarketID: "BTC-USDT", EpochID: "epoch-1", Version: 6,
		RuntimeSequence: 42, StateHash: strings.Repeat("a", 64),
	}
}

func healthySample(binding recovery.Binding, observedAt time.Time) Sample {
	return Sample{
		ObservedAt:               observedAt,
		MarketID:                 domain.MarketID(binding.MarketID),
		EpochID:                  binding.EpochID,
		RecoveryVersion:          binding.Version,
		RecoveryPhase:            recovery.PhaseTransportWarmup,
		AuthorityEpochID:         binding.EpochID,
		AuthorityVersion:         binding.Version,
		AuthorityPhase:           recovery.PhaseTransportWarmup,
		AuthorityRuntimeSequence: binding.RuntimeSequence,
		AuthorityStateHash:       binding.StateHash,
		PublicRuntimeSequence:    binding.RuntimeSequence,
		PublicStateHash:          binding.StateHash,
		PublicLocalProof:         true,
		RuntimeState:             "ready",
		RuntimeSequence:          binding.RuntimeSequence,
		RuntimeStateHash:         binding.StateHash,
		OutboxState:              "ready",
		OutboxSequence:           binding.RuntimeSequence,
	}
}

func testPolicy() ObservationPolicy {
	return ObservationPolicy{
		MinimumSamples: recovery.MinimumTransportSamples,
		MinimumWindow:  recovery.MinimumTransportWindow,
		SampleEvery:    time.Millisecond,
		MaximumGap:     recovery.MaximumTransportGap,
		ProbeTimeout:   time.Second,
	}
}
