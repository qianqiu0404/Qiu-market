package operator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/recovery"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

type ObservationPolicy struct {
	MinimumSamples int
	MinimumWindow  time.Duration
	SampleEvery    time.Duration
	MaximumGap     time.Duration
	ProbeTimeout   time.Duration
}

func DefaultObservationPolicy() ObservationPolicy {
	return ObservationPolicy{
		MinimumSamples: recovery.MinimumTransportSamples,
		MinimumWindow:  recovery.MinimumTransportWindow,
		SampleEvery:    5 * time.Second,
		MaximumGap:     recovery.MaximumTransportGap,
		ProbeTimeout:   3 * time.Second,
	}
}

type Sample struct {
	ObservedAt                   time.Time       `json:"observed_at"`
	MarketID                     domain.MarketID `json:"market_id"`
	EpochID                      string          `json:"epoch_id"`
	RecoveryVersion              uint64          `json:"recovery_version"`
	RecoveryPhase                recovery.Phase  `json:"recovery_phase"`
	RecoveryWritesEnabled        bool            `json:"recovery_writes_enabled"`
	AuthorityEpochID             string          `json:"authority_epoch_id"`
	AuthorityVersion             uint64          `json:"authority_version"`
	AuthorityPhase               recovery.Phase  `json:"authority_phase"`
	AuthorityRuntimeSequence     uint64          `json:"authority_runtime_sequence"`
	AuthorityStateHash           string          `json:"authority_state_hash"`
	AuthorityWritesEnabled       bool            `json:"authority_writes_enabled"`
	AuthorityContinuityUncertain bool            `json:"authority_continuity_uncertain"`
	PublicRuntimeSequence        uint64          `json:"public_runtime_sequence"`
	PublicStateHash              string          `json:"public_state_hash"`
	PublicLocalProof             bool            `json:"public_local_proof"`
	PublicTransportHealthy       bool            `json:"public_transport_healthy"`
	PublicContinuityUncertain    bool            `json:"public_continuity_uncertain"`
	RuntimeState                 string          `json:"runtime_state"`
	RuntimeSequence              uint64          `json:"runtime_sequence"`
	RuntimeStateHash             string          `json:"runtime_state_hash"`
	RuntimeQueueDepth            uint32          `json:"runtime_queue_depth"`
	RuntimeLastError             string          `json:"runtime_last_error,omitempty"`
	OutboxState                  string          `json:"outbox_state"`
	OutboxSequence               uint64          `json:"outbox_sequence"`
	OutboxLastError              string          `json:"outbox_last_error,omitempty"`
}

type SampleSource interface {
	Sample(context.Context) (Sample, error)
}

type TransportProbe struct {
	binding    recovery.Binding
	statusURL  string
	httpClient *http.Client
	connection *grpc.ClientConn
	client     tradingv1.TradingServiceClient
}

type RecoveryClient struct {
	connection *grpc.ClientConn
	client     tradingv1.TradingServiceClient
}

func DialRecoveryClient(
	ctx context.Context,
	grpcAddress string,
) (*RecoveryClient, error) {
	if !loopbackAddress(grpcAddress) {
		return nil, fmt.Errorf("recovery operator gRPC address must be explicit loopback")
	}
	connection, err := grpc.DialContext(
		ctx,
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect trading recovery operator: %w", err)
	}
	return &RecoveryClient{
		connection: connection,
		client:     tradingv1.NewTradingServiceClient(connection),
	}, nil
}

func (c *RecoveryClient) Close() error {
	if c == nil || c.connection == nil {
		return nil
	}
	return c.connection.Close()
}

func (c *RecoveryClient) Status(
	ctx context.Context,
	marketID domain.MarketID,
) (*tradingv1.RecoveryStatusResponse, error) {
	return c.client.GetRecoveryStatus(ctx, &tradingv1.GetRecoveryStatusRequest{
		MarketId: string(marketID),
	})
}

func (c *RecoveryClient) Promote(
	ctx context.Context,
	binding recovery.Binding,
	evidence recovery.TransportEvidence,
) (*tradingv1.RecoveryStatusResponse, error) {
	return c.client.PromoteRecovery(ctx, &tradingv1.PromoteRecoveryRequest{
		Binding: &tradingv1.RecoveryBinding{
			MarketId:        string(binding.MarketID),
			EpochId:         binding.EpochID,
			Version:         strconv.FormatUint(binding.Version, 10),
			RuntimeSequence: strconv.FormatUint(binding.RuntimeSequence, 10),
			StateHash:       binding.StateHash,
		},
		TransportEvidence: &tradingv1.RecoveryTransportEvidence{
			SampleCount:    uint32(evidence.SampleCount),
			FirstSampleAt:  evidence.FirstSampleAt.UTC().Format(time.RFC3339Nano),
			LastSampleAt:   evidence.LastSampleAt.UTC().Format(time.RFC3339Nano),
			MaximumGapMs:   strconv.FormatInt(evidence.MaximumGapMS, 10),
			EvidenceSha256: evidence.EvidenceSHA256,
		},
	})
}

func NewTransportProbe(
	ctx context.Context,
	binding recovery.Binding,
	statusURL string,
	grpcAddress string,
	httpClient *http.Client,
) (*TransportProbe, error) {
	if err := validateStatusURL(statusURL); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	operatorClient, err := DialRecoveryClient(ctx, grpcAddress)
	if err != nil {
		return nil, err
	}
	return &TransportProbe{
		binding: binding, statusURL: statusURL, httpClient: httpClient,
		connection: operatorClient.connection, client: operatorClient.client,
	}, nil
}

func (p *TransportProbe) Close() error {
	if p == nil || p.connection == nil {
		return nil
	}
	return p.connection.Close()
}

func (p *TransportProbe) Promote(
	ctx context.Context,
	evidence recovery.TransportEvidence,
) (*tradingv1.RecoveryStatusResponse, error) {
	client := &RecoveryClient{client: p.client}
	return client.Promote(ctx, p.binding, evidence)
}

func (p *TransportProbe) Sample(ctx context.Context) (Sample, error) {
	authorityStatus, err := p.client.GetRecoveryStatus(
		ctx,
		&tradingv1.GetRecoveryStatusRequest{MarketId: string(p.binding.MarketID)},
	)
	if err != nil {
		return Sample{}, fmt.Errorf("probe authoritative recovery status: %w", err)
	}
	runtimeStatus, err := p.client.GetStatus(ctx, &tradingv1.GetStatusRequest{
		MarketId: string(p.binding.MarketID),
	})
	if err != nil {
		return Sample{}, fmt.Errorf("probe loopback trading status: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.statusURL, nil)
	if err != nil {
		return Sample{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := p.httpClient.Do(request)
	if err != nil {
		return Sample{}, fmt.Errorf("probe public recovery status: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Sample{}, fmt.Errorf("public recovery status returned HTTP %d", response.StatusCode)
	}
	var publicStatus publicRecoveryStatus
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(&publicStatus); err != nil {
		return Sample{}, fmt.Errorf("decode public recovery status: %w", err)
	}
	publicVersion, err := strconv.ParseUint(publicStatus.Version, 10, 64)
	if err != nil {
		return Sample{}, fmt.Errorf("parse public recovery version: %w", err)
	}
	publicSequence, err := strconv.ParseUint(publicStatus.RuntimeSequence, 10, 64)
	if err != nil {
		return Sample{}, fmt.Errorf("parse public runtime sequence: %w", err)
	}
	authorityVersion, err := strconv.ParseUint(authorityStatus.GetVersion(), 10, 64)
	if err != nil {
		return Sample{}, fmt.Errorf("parse authoritative recovery version: %w", err)
	}
	authoritySequence, err := strconv.ParseUint(authorityStatus.GetRuntimeSequence(), 10, 64)
	if err != nil {
		return Sample{}, fmt.Errorf("parse authoritative runtime sequence: %w", err)
	}
	runtimeSequence, err := strconv.ParseUint(runtimeStatus.GetSequence(), 10, 64)
	if err != nil {
		return Sample{}, fmt.Errorf("parse runtime sequence: %w", err)
	}
	outboxSequence, err := strconv.ParseUint(runtimeStatus.GetOutboxCheckpointSequence(), 10, 64)
	if err != nil {
		return Sample{}, fmt.Errorf("parse outbox checkpoint sequence: %w", err)
	}
	return Sample{
		ObservedAt:                   time.Now().UTC(),
		MarketID:                     domain.MarketID(publicStatus.MarketID),
		EpochID:                      publicStatus.EpochID,
		RecoveryVersion:              publicVersion,
		RecoveryPhase:                recovery.Phase(publicStatus.Phase),
		RecoveryWritesEnabled:        publicStatus.WritesEnabled,
		AuthorityEpochID:             authorityStatus.GetEpochId(),
		AuthorityVersion:             authorityVersion,
		AuthorityPhase:               recovery.Phase(authorityStatus.GetPhase()),
		AuthorityRuntimeSequence:     authoritySequence,
		AuthorityStateHash:           authorityStatus.GetStateHash(),
		AuthorityWritesEnabled:       authorityStatus.GetWritesEnabled(),
		AuthorityContinuityUncertain: authorityStatus.GetContinuityUncertain(),
		PublicRuntimeSequence:        publicSequence,
		PublicStateHash:              publicStatus.StateHash,
		PublicLocalProof: publicStatus.LedgerBalanced && publicStatus.EventContinuous &&
			publicStatus.ProjectionCaughtUp && publicStatus.OutboxCaughtUp,
		PublicTransportHealthy:    publicStatus.TransportHealthy,
		PublicContinuityUncertain: publicStatus.ContinuityUncertain,
		RuntimeState:              runtimeStatus.GetState(),
		RuntimeSequence:           runtimeSequence,
		RuntimeStateHash:          runtimeStatus.GetStateHash(),
		RuntimeQueueDepth:         runtimeStatus.GetQueueDepth(),
		RuntimeLastError:          runtimeStatus.GetLastError(),
		OutboxState:               runtimeStatus.GetOutboxState(),
		OutboxSequence:            outboxSequence,
		OutboxLastError:           runtimeStatus.GetOutboxLastError(),
	}, nil
}

type publicRecoveryStatus struct {
	MarketID            string `json:"market_id"`
	EpochID             string `json:"epoch_id"`
	Phase               string `json:"phase"`
	RuntimeSequence     string `json:"runtime_sequence"`
	StateHash           string `json:"state_hash"`
	LedgerBalanced      bool   `json:"ledger_balanced"`
	EventContinuous     bool   `json:"event_continuous"`
	ProjectionCaughtUp  bool   `json:"projection_caught_up"`
	OutboxCaughtUp      bool   `json:"outbox_caught_up"`
	TransportHealthy    bool   `json:"transport_healthy"`
	WritesEnabled       bool   `json:"writes_enabled"`
	Version             string `json:"version"`
	ContinuityUncertain bool   `json:"continuity_uncertain"`
}

func CollectTransportEvidence(
	ctx context.Context,
	binding recovery.Binding,
	source SampleSource,
	policy ObservationPolicy,
) (recovery.TransportEvidence, []Sample, error) {
	if source == nil {
		return recovery.TransportEvidence{}, nil, fmt.Errorf("transport sample source is required")
	}
	if err := validatePolicy(policy); err != nil {
		return recovery.TransportEvidence{}, nil, err
	}
	ticker := time.NewTicker(policy.SampleEvery)
	defer ticker.Stop()
	samples := make([]Sample, 0, policy.MinimumSamples)
	var maximumGap time.Duration
	for {
		probeContext, cancel := context.WithTimeout(ctx, policy.ProbeTimeout)
		sample, err := source.Sample(probeContext)
		cancel()
		if err != nil {
			return recovery.TransportEvidence{}, samples, err
		}
		if err := validateSample(binding, sample); err != nil {
			return recovery.TransportEvidence{}, samples, err
		}
		if len(samples) > 0 {
			gap := sample.ObservedAt.Sub(samples[len(samples)-1].ObservedAt)
			if gap <= 0 || gap > policy.MaximumGap {
				return recovery.TransportEvidence{}, samples,
					fmt.Errorf("transport sample gap %s is outside (0,%s]", gap, policy.MaximumGap)
			}
			if gap > maximumGap {
				maximumGap = gap
			}
		}
		samples = append(samples, sample)
		window := sample.ObservedAt.Sub(samples[0].ObservedAt)
		if len(samples) >= policy.MinimumSamples && window >= policy.MinimumWindow {
			payload, err := json.Marshal(samples)
			if err != nil {
				return recovery.TransportEvidence{}, samples, err
			}
			digest := sha256.Sum256(payload)
			return recovery.TransportEvidence{
				SampleCount:    len(samples),
				FirstSampleAt:  samples[0].ObservedAt,
				LastSampleAt:   sample.ObservedAt,
				MaximumGapMS:   durationMillisecondsCeil(maximumGap),
				EvidenceSHA256: hex.EncodeToString(digest[:]),
			}, samples, nil
		}
		select {
		case <-ctx.Done():
			return recovery.TransportEvidence{}, samples, ctx.Err()
		case <-ticker.C:
		}
	}
}

func durationMillisecondsCeil(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return int64((value + time.Millisecond - 1) / time.Millisecond)
}

func validateSample(binding recovery.Binding, sample Sample) error {
	if sample.ObservedAt.IsZero() ||
		sample.MarketID != binding.MarketID ||
		sample.EpochID != binding.EpochID ||
		sample.RecoveryVersion != binding.Version ||
		sample.RecoveryPhase != recovery.PhaseTransportWarmup ||
		sample.RecoveryWritesEnabled ||
		sample.AuthorityEpochID != binding.EpochID ||
		sample.AuthorityVersion != binding.Version ||
		sample.AuthorityPhase != recovery.PhaseTransportWarmup ||
		sample.AuthorityRuntimeSequence != binding.RuntimeSequence ||
		sample.AuthorityStateHash != binding.StateHash ||
		sample.AuthorityWritesEnabled || sample.AuthorityContinuityUncertain ||
		sample.PublicRuntimeSequence != binding.RuntimeSequence ||
		sample.PublicStateHash != binding.StateHash ||
		!sample.PublicLocalProof || sample.PublicTransportHealthy ||
		sample.PublicContinuityUncertain ||
		sample.RuntimeState != "ready" ||
		sample.RuntimeSequence != binding.RuntimeSequence ||
		sample.RuntimeStateHash != binding.StateHash ||
		sample.RuntimeQueueDepth != 0 ||
		sample.RuntimeLastError != "" ||
		sample.OutboxState != "ready" ||
		sample.OutboxSequence != binding.RuntimeSequence ||
		sample.OutboxLastError != "" {
		return fmt.Errorf("transport sample changed the bound recovery state")
	}
	return nil
}

func validatePolicy(policy ObservationPolicy) error {
	if policy.MinimumSamples < recovery.MinimumTransportSamples ||
		policy.MinimumWindow < recovery.MinimumTransportWindow ||
		policy.SampleEvery <= 0 ||
		policy.MaximumGap <= 0 || policy.MaximumGap > recovery.MaximumTransportGap ||
		policy.SampleEvery > policy.MaximumGap ||
		policy.ProbeTimeout <= 0 || policy.ProbeTimeout >= policy.MaximumGap {
		return fmt.Errorf("invalid recovery transport observation policy")
	}
	return nil
}

func validateStatusURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" ||
		parsed.Path != "/api/v1/trading/recovery/status" || parsed.RawQuery != "" {
		return fmt.Errorf("status URL must be the exact recovery status endpoint")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") ||
		(net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())) {
		return nil
	}
	return fmt.Errorf("status URL must use HTTPS unless it is explicit loopback")
}

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
