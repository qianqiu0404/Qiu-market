package recovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
)

const SchemaVersion = 1

var (
	ErrNotInitialized    = errors.New("trading recovery is not initialized")
	ErrWriteBlocked      = errors.New("trading writes are blocked by recovery")
	ErrInvalidTransition = errors.New("invalid trading recovery phase transition")
	ErrProofIncomplete   = errors.New("trading recovery proof is incomplete")
	ErrVersionConflict   = errors.New("trading recovery version conflict")
)

type Phase string

const (
	PhaseBootstrap         Phase = "bootstrap"
	PhaseDependenciesReady Phase = "dependencies_ready"
	PhaseTradingReplay     Phase = "trading_replay"
	PhaseReconciling       Phase = "reconciling"
	PhaseReadOnly          Phase = "read_only"
	PhaseTransportWarmup   Phase = "transport_warmup"
	PhaseWritable          Phase = "writable"
	PhaseOffline           Phase = "offline"
	PhaseManualReview      Phase = "manual_review"
)

type Proof struct {
	RuntimeSequence    uint64 `json:"runtime_sequence"`
	StateHash          string `json:"state_hash,omitempty"`
	LedgerBalanced     bool   `json:"ledger_balanced"`
	EventContinuous    bool   `json:"event_continuous"`
	ProjectionCaughtUp bool   `json:"projection_caught_up"`
	OutboxCaughtUp     bool   `json:"outbox_caught_up"`
	TransportHealthy   bool   `json:"transport_healthy"`
}

type Status struct {
	SchemaVersion int             `json:"schema_version"`
	MarketID      domain.MarketID `json:"market_id"`
	EpochID       string          `json:"epoch_id"`
	Phase         Phase           `json:"phase"`
	Proof         Proof           `json:"proof"`
	WritesEnabled bool            `json:"writes_enabled"`
	LastError     string          `json:"last_error,omitempty"`
	Version       uint64          `json:"version"`
	StartedAt     time.Time       `json:"started_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Store interface {
	Load(context.Context, domain.MarketID) (Status, bool, error)
	Save(context.Context, uint64, Status) error
}

type Coordinator struct {
	store    Store
	marketID domain.MarketID
	now      func() time.Time
	newID    func() (string, error)
}

func NewCoordinator(store Store, marketID domain.MarketID) (*Coordinator, error) {
	if store == nil || marketID == "" {
		return nil, fmt.Errorf("recovery store and market id are required")
	}
	return &Coordinator{
		store:    store,
		marketID: marketID,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    newEpochID,
	}, nil
}

func (c *Coordinator) Begin(ctx context.Context) (Status, error) {
	current, found, err := c.store.Load(ctx, c.marketID)
	if err != nil {
		return Status{}, fmt.Errorf("load previous recovery epoch: %w", err)
	}
	expected := uint64(0)
	if found {
		expected = current.Version
	}
	epochID, err := c.newID()
	if err != nil {
		return Status{}, err
	}
	now := c.now()
	next := Status{
		SchemaVersion: SchemaVersion,
		MarketID:      c.marketID,
		EpochID:       epochID,
		Phase:         PhaseBootstrap,
		Version:       expected + 1,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if err := c.store.Save(ctx, expected, next); err != nil {
		return Status{}, err
	}
	return next, nil
}

func (c *Coordinator) Status(ctx context.Context) (Status, error) {
	current, found, err := c.store.Load(ctx, c.marketID)
	if err != nil {
		return Status{}, err
	}
	if !found {
		return Status{}, ErrNotInitialized
	}
	return current, nil
}

func (c *Coordinator) Advance(
	ctx context.Context,
	nextPhase Phase,
	proof Proof,
) (Status, error) {
	current, err := c.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	if !allowedTransition(current.Phase, nextPhase) {
		return Status{}, fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition,
			current.Phase,
			nextPhase,
		)
	}
	merged := mergeProof(current.Proof, proof)
	if requiresLocalProof(nextPhase) && !localProofComplete(merged) {
		return Status{}, ErrProofIncomplete
	}
	if nextPhase == PhaseWritable && !merged.TransportHealthy {
		return Status{}, ErrProofIncomplete
	}
	next := current
	next.Phase = nextPhase
	next.Proof = merged
	next.WritesEnabled = nextPhase == PhaseWritable
	next.LastError = ""
	next.Version++
	next.UpdatedAt = c.now()
	if err := c.store.Save(ctx, current.Version, next); err != nil {
		return Status{}, err
	}
	return next, nil
}

func (c *Coordinator) Fail(
	ctx context.Context,
	phase Phase,
	cause error,
) (Status, error) {
	if phase != PhaseOffline && phase != PhaseManualReview {
		return Status{}, fmt.Errorf("%w: failure phase %s", ErrInvalidTransition, phase)
	}
	current, err := c.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	next := current
	next.Phase = phase
	next.WritesEnabled = false
	next.Version++
	next.UpdatedAt = c.now()
	if cause != nil {
		next.LastError = cause.Error()
	}
	if err := c.store.Save(ctx, current.Version, next); err != nil {
		return Status{}, err
	}
	return next, nil
}

func (c *Coordinator) RequireWritable(ctx context.Context) error {
	current, err := c.Status(ctx)
	if err != nil {
		return errors.Join(ErrWriteBlocked, err)
	}
	if current.Phase != PhaseWritable || !current.WritesEnabled ||
		!localProofComplete(current.Proof) || !current.Proof.TransportHealthy {
		return fmt.Errorf("%w: phase=%s epoch=%s", ErrWriteBlocked, current.Phase, current.EpochID)
	}
	return nil
}

func allowedTransition(current, next Phase) bool {
	if next == PhaseOffline || next == PhaseManualReview {
		return current != PhaseOffline && current != PhaseManualReview
	}
	switch current {
	case PhaseBootstrap:
		return next == PhaseDependenciesReady
	case PhaseDependenciesReady:
		return next == PhaseTradingReplay
	case PhaseTradingReplay:
		return next == PhaseReconciling
	case PhaseReconciling:
		return next == PhaseReadOnly
	case PhaseReadOnly:
		return next == PhaseTransportWarmup
	case PhaseTransportWarmup:
		return next == PhaseWritable
	default:
		return false
	}
}

func requiresLocalProof(phase Phase) bool {
	return phase == PhaseReadOnly || phase == PhaseTransportWarmup || phase == PhaseWritable
}

func localProofComplete(proof Proof) bool {
	return len(proof.StateHash) == 64 &&
		proof.LedgerBalanced &&
		proof.EventContinuous &&
		proof.ProjectionCaughtUp &&
		proof.OutboxCaughtUp
}

func mergeProof(current, update Proof) Proof {
	if update.RuntimeSequence != 0 || current.RuntimeSequence == 0 {
		current.RuntimeSequence = update.RuntimeSequence
	}
	if update.StateHash != "" {
		current.StateHash = update.StateHash
	}
	current.LedgerBalanced = current.LedgerBalanced || update.LedgerBalanced
	current.EventContinuous = current.EventContinuous || update.EventContinuous
	current.ProjectionCaughtUp = current.ProjectionCaughtUp || update.ProjectionCaughtUp
	current.OutboxCaughtUp = current.OutboxCaughtUp || update.OutboxCaughtUp
	current.TransportHealthy = update.TransportHealthy
	return current
}

func newEpochID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate recovery epoch id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
