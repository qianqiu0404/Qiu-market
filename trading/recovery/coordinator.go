package recovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
)

const SchemaVersion = 1

const (
	MinimumTransportSamples = 7
	MinimumTransportWindow  = 30 * time.Second
	MaximumTransportGap     = 8 * time.Second
)

var (
	ErrNotInitialized    = errors.New("trading recovery is not initialized")
	ErrWriteBlocked      = errors.New("trading writes are blocked by recovery")
	ErrInvalidTransition = errors.New("invalid trading recovery phase transition")
	ErrProofIncomplete   = errors.New("trading recovery proof is incomplete")
	ErrVersionConflict   = errors.New("trading recovery version conflict")
	ErrBindingMismatch   = errors.New("trading recovery binding mismatch")
	ErrTransportEvidence = errors.New("trading transport evidence is invalid")
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

// Binding is copied from recovery status before an operator starts observing
// transport health. Promotion is rejected unless every field still identifies
// the exact immutable runtime state observed throughout that window.
type Binding struct {
	MarketID        domain.MarketID `json:"market_id"`
	EpochID         string          `json:"epoch_id"`
	Version         uint64          `json:"version"`
	RuntimeSequence uint64          `json:"runtime_sequence"`
	StateHash       string          `json:"state_hash"`
}

type TransportEvidence struct {
	SampleCount    int       `json:"sample_count"`
	FirstSampleAt  time.Time `json:"first_sample_at"`
	LastSampleAt   time.Time `json:"last_sample_at"`
	MaximumGapMS   int64     `json:"maximum_gap_ms"`
	EvidenceSHA256 string    `json:"evidence_sha256"`
}

type AdmissionMode string

const (
	AdmissionNormal       AdmissionMode = "normal"
	AdmissionSafetyCancel AdmissionMode = "safety_cancel"
	AdmissionBootstrap    AdmissionMode = "bootstrap"
)

// Admission binds an accepted command to the exact recovery control-plane
// state that authorized it. MarketRunner validates it again when the command
// reaches the single writer, so a queued command cannot cross a phase/version
// change unnoticed.
type Admission struct {
	MarketID  domain.MarketID
	EpochID   string
	Version   uint64
	Phase     Phase
	Mode      AdmissionMode
	AccountID domain.AccountID
}

type Status struct {
	SchemaVersion       int               `json:"schema_version"`
	MarketID            domain.MarketID   `json:"market_id"`
	EpochID             string            `json:"epoch_id"`
	Phase               Phase             `json:"phase"`
	Proof               Proof             `json:"proof"`
	Transport           TransportEvidence `json:"transport_evidence"`
	WritesEnabled       bool              `json:"writes_enabled"`
	LastError           string            `json:"last_error,omitempty"`
	Version             uint64            `json:"version"`
	StartedAt           time.Time         `json:"started_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	ContinuityUncertain bool              `json:"continuity_uncertain"`
	ContinuityError     string            `json:"continuity_error,omitempty"`
}

type Store interface {
	Load(context.Context, domain.MarketID) (Status, bool, error)
	Save(context.Context, uint64, Status) error
}

type Coordinator struct {
	store               Store
	marketID            domain.MarketID
	now                 func() time.Time
	newID               func() (string, error)
	continuityMu        sync.RWMutex
	lastObservedEpoch   string
	continuityEpoch     string
	continuityUncertain bool
	continuityError     string
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
		c.markContinuityUncertain(err)
		return Status{}, fmt.Errorf("load previous recovery epoch: %w", err)
	}
	if found {
		c.observeEpoch(current.EpochID)
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
		c.markContinuityUncertain(err)
		return Status{}, err
	}
	c.observeNewEpoch(next.EpochID)
	return next, nil
}

func (c *Coordinator) Status(ctx context.Context) (Status, error) {
	current, found, err := c.store.Load(ctx, c.marketID)
	if err != nil {
		c.markContinuityUncertain(err)
		return Status{}, err
	}
	if !found {
		return Status{}, ErrNotInitialized
	}
	c.observeEpoch(current.EpochID)
	return c.effectiveStatus(current), nil
}

func (c *Coordinator) Advance(
	ctx context.Context,
	nextPhase Phase,
	proof Proof,
) (Status, error) {
	if nextPhase == PhaseWritable {
		return Status{}, fmt.Errorf(
			"%w: writable requires bound transport promotion",
			ErrInvalidTransition,
		)
	}
	current, err := c.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	if current.ContinuityUncertain {
		return Status{}, ErrWriteBlocked
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
	next := current
	next.Phase = nextPhase
	next.Proof = merged
	next.WritesEnabled = nextPhase == PhaseWritable
	next.LastError = ""
	next.Version++
	next.UpdatedAt = c.now()
	if err := c.store.Save(ctx, current.Version, next); err != nil {
		c.markContinuityUncertain(err)
		return Status{}, err
	}
	return next, nil
}

// Promote is the only supported transition from transport_warmup to writable.
// It deliberately does not retry a stale CAS: an epoch, version, sequence or
// hash change invalidates the observations and requires a new operator run.
func (c *Coordinator) Promote(
	ctx context.Context,
	binding Binding,
	evidence TransportEvidence,
) (Status, error) {
	current, err := c.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	if current.ContinuityUncertain {
		return Status{}, ErrWriteBlocked
	}
	if current.Phase != PhaseTransportWarmup || !matchesBinding(current, binding) {
		return Status{}, fmt.Errorf("%w: current=%s/%s/v%d/%d/%s",
			ErrBindingMismatch,
			current.MarketID,
			current.EpochID,
			current.Version,
			current.Proof.RuntimeSequence,
			current.Proof.StateHash,
		)
	}
	if !localProofComplete(current.Proof) || !validTransportEvidence(evidence, c.now()) {
		return Status{}, ErrTransportEvidence
	}
	next := current
	next.Phase = PhaseWritable
	next.Proof.TransportHealthy = true
	next.Transport = evidence
	next.WritesEnabled = true
	next.LastError = ""
	next.Version++
	next.UpdatedAt = c.now()
	if err := c.store.Save(ctx, current.Version, next); err != nil {
		c.markContinuityUncertain(err)
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
		c.markContinuityUncertain(err)
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

func (c *Coordinator) Admit(
	ctx context.Context,
	mode AdmissionMode,
	accountID domain.AccountID,
) (Admission, error) {
	current, err := c.Status(ctx)
	if err != nil {
		return Admission{}, errors.Join(ErrWriteBlocked, err)
	}
	if err := authorize(current, mode, accountID); err != nil {
		return Admission{}, err
	}
	return Admission{
		MarketID: c.marketID, EpochID: current.EpochID, Version: current.Version,
		Phase: current.Phase, Mode: mode, AccountID: accountID,
	}, nil
}

// ValidateAdmission is the fail-closed dequeue fence. It intentionally checks
// the current durable recovery row again instead of trusting an enqueue-time
// boolean. A mismatch means the caller must reconcile using its idempotency ID.
func (c *Coordinator) ValidateAdmission(ctx context.Context, admission Admission) error {
	current, err := c.Status(ctx)
	if err != nil {
		return errors.Join(ErrWriteBlocked, err)
	}
	if admission.MarketID != current.MarketID ||
		admission.EpochID == "" || admission.EpochID != current.EpochID ||
		admission.Version != current.Version || admission.Phase != current.Phase {
		return ErrWriteBlocked
	}
	return authorize(current, admission.Mode, admission.AccountID)
}

func authorize(current Status, mode AdmissionMode, accountID domain.AccountID) error {
	if current.ContinuityUncertain {
		return ErrWriteBlocked
	}
	switch mode {
	case AdmissionNormal:
		if current.Phase == PhaseWritable && current.WritesEnabled &&
			localProofComplete(current.Proof) && current.Proof.TransportHealthy {
			return nil
		}
	case AdmissionSafetyCancel:
		if accountID == domain.AccountID("system:demo-maker") {
			switch current.Phase {
			case PhaseTradingReplay, PhaseWritable, PhaseOffline, PhaseManualReview:
				return nil
			}
		}
	case AdmissionBootstrap:
		if accountID == domain.AccountID("system:demo-maker") &&
			current.Phase == PhaseTradingReplay && !current.WritesEnabled {
			return nil
		}
	}
	return ErrWriteBlocked
}

// AuthorizeSafetyCancel permits only the system demo-maker to unwind existing
// quotes while normal writes remain blocked. The command still passes through
// MarketRunner's single writer and the coordinator; it is not a second write
// path and cannot submit or fund anything.
func (c *Coordinator) AuthorizeSafetyCancel(
	ctx context.Context,
	accountID domain.AccountID,
) error {
	_, err := c.Admit(ctx, AdmissionSafetyCancel, accountID)
	return err
}

// AuthorizeBootstrapFund allows only the idempotent system demo-maker funding
// performed during trading_replay, before the durable recovery proof is taken.
func (c *Coordinator) AuthorizeBootstrapFund(
	ctx context.Context,
	accountID domain.AccountID,
) error {
	_, err := c.Admit(ctx, AdmissionBootstrap, accountID)
	return err
}

func (c *Coordinator) markContinuityUncertain(cause error) {
	c.continuityMu.Lock()
	defer c.continuityMu.Unlock()
	if c.continuityUncertain {
		return
	}
	c.continuityUncertain = true
	c.continuityEpoch = c.lastObservedEpoch
	if cause != nil {
		c.continuityError = cause.Error()
	}
}

func (c *Coordinator) observeEpoch(epochID string) {
	c.continuityMu.Lock()
	defer c.continuityMu.Unlock()
	c.lastObservedEpoch = epochID
	if c.continuityUncertain && c.continuityEpoch != "" &&
		c.continuityEpoch != epochID {
		c.continuityUncertain = false
		c.continuityEpoch = ""
		c.continuityError = ""
	}
}

func (c *Coordinator) observeNewEpoch(epochID string) {
	c.continuityMu.Lock()
	defer c.continuityMu.Unlock()
	c.lastObservedEpoch = epochID
	c.continuityUncertain = false
	c.continuityEpoch = ""
	c.continuityError = ""
}

func (c *Coordinator) effectiveStatus(current Status) Status {
	c.continuityMu.RLock()
	defer c.continuityMu.RUnlock()
	if !c.continuityUncertain ||
		(c.continuityEpoch != "" && c.continuityEpoch != current.EpochID) {
		return current
	}
	current.WritesEnabled = false
	current.Proof.TransportHealthy = false
	current.ContinuityUncertain = true
	current.ContinuityError = c.continuityError
	if current.LastError == "" {
		current.LastError = "recovery store continuity is uncertain; a new epoch is required"
	}
	return current
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
		return false
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

func matchesBinding(status Status, binding Binding) bool {
	return binding.MarketID == status.MarketID &&
		binding.EpochID != "" && binding.EpochID == status.EpochID &&
		binding.Version == status.Version &&
		binding.RuntimeSequence == status.Proof.RuntimeSequence &&
		len(binding.StateHash) == 64 && binding.StateHash == status.Proof.StateHash
}

func validTransportEvidence(evidence TransportEvidence, now time.Time) bool {
	if evidence.SampleCount < MinimumTransportSamples ||
		evidence.FirstSampleAt.IsZero() ||
		evidence.LastSampleAt.Before(evidence.FirstSampleAt) ||
		evidence.MaximumGapMS <= 0 ||
		evidence.MaximumGapMS > MaximumTransportGap.Milliseconds() ||
		evidence.LastSampleAt.After(now.Add(time.Second)) ||
		now.Sub(evidence.LastSampleAt) > MaximumTransportGap {
		return false
	}
	decodedSHA, err := hex.DecodeString(evidence.EvidenceSHA256)
	if err != nil || len(decodedSHA) != 32 {
		return false
	}
	span := evidence.LastSampleAt.Sub(evidence.FirstSampleAt)
	if span < MinimumTransportWindow {
		return false
	}
	maximumGap := time.Duration(evidence.MaximumGapMS) * time.Millisecond
	requiredIntervals := int64(span / maximumGap)
	if span%maximumGap != 0 {
		requiredIntervals++
	}
	return requiredIntervals <= int64(evidence.SampleCount-1)
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
