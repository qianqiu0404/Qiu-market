package reliability

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/the-web3/s78-market-services/trading/domain"
)

var (
	ErrInvalidCursor       = errors.New("invalid trading event cursor")
	ErrCursorGap           = errors.New("trading event cursor gap")
	ErrCursorConflict      = errors.New("trading event cursor metadata conflict")
	ErrCursorRegression    = errors.New("trading event head regressed")
	ErrReconcileIncomplete = errors.New("trading event reconciliation is incomplete")
)

// Cursor is the durable identity of one event within a market command batch.
// Cursors are ordered lexicographically by market sequence and event index.
type Cursor struct {
	MarketSequence uint64 `json:"market_sequence"`
	EventIndex     uint32 `json:"event_index"`
}

// Checkpoint persists both the last applied cursor and the authoritative number
// of events in that cursor's command batch. BatchEventCount is what lets a
// reconnect distinguish a valid sequence transition from a missing tail event.
type Checkpoint struct {
	Cursor
	BatchEventCount uint32 `json:"batch_event_count"`
}

// EventEnvelope carries an existing domain event plus its authoritative batch
// size. It does not contain or rebuild matching, order, balance, or ledger state.
type EventEnvelope struct {
	Cursor
	BatchEventCount uint32       `json:"batch_event_count"`
	Event           domain.Event `json:"event"`
}

type ReconcileReport struct {
	Start      Cursor `json:"start"`
	Head       Cursor `json:"head"`
	End        Cursor `json:"end"`
	Applied    int    `json:"applied"`
	Duplicates int    `json:"duplicates"`
	Complete   bool   `json:"complete"`
}

type ApplyEvent func(EventEnvelope) error

// EventReconciler is a cursor gate for an event consumer. It applies each
// cursor at most once, fails closed on gaps, and becomes writable only after a
// caller has compared it with an authoritative head.
type EventReconciler struct {
	mu               sync.RWMutex
	checkpoint       Checkpoint
	head             Cursor
	reconcileStarted bool
	complete         bool
}

func NewEventReconciler(checkpoint Checkpoint) (*EventReconciler, error) {
	if err := validateCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	return &EventReconciler{checkpoint: checkpoint}, nil
}

func (r *EventReconciler) Reconcile(
	head Cursor,
	events []EventEnvelope,
	apply ApplyEvent,
) (ReconcileReport, error) {
	if apply == nil {
		return ReconcileReport{}, fmt.Errorf("event apply function is required")
	}
	if err := validateCursor(head); err != nil {
		return ReconcileReport{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	report := ReconcileReport{
		Start: r.checkpoint.Cursor,
		Head:  head,
		End:   r.checkpoint.Cursor,
	}
	if compareCursor(head, r.checkpoint.Cursor) < 0 {
		return report, fmt.Errorf(
			"%w: head=%s checkpoint=%s",
			ErrCursorRegression,
			formatCursor(head),
			formatCursor(r.checkpoint.Cursor),
		)
	}

	for _, envelope := range events {
		if err := validateEnvelope(envelope); err != nil {
			return report, err
		}
		if compareCursor(envelope.Cursor, head) > 0 {
			return report, fmt.Errorf(
				"%w: event=%s is after head=%s",
				ErrCursorConflict,
				formatCursor(envelope.Cursor),
				formatCursor(head),
			)
		}

		comparison := compareCursor(envelope.Cursor, r.checkpoint.Cursor)
		if comparison <= 0 {
			if comparison == 0 &&
				r.checkpoint.BatchEventCount != envelope.BatchEventCount {
				return report, fmt.Errorf(
					"%w: cursor=%s batch_count=%d/%d",
					ErrCursorConflict,
					formatCursor(envelope.Cursor),
					r.checkpoint.BatchEventCount,
					envelope.BatchEventCount,
				)
			}
			report.Duplicates++
			continue
		}

		expected, err := nextCursor(r.checkpoint)
		if err != nil {
			return report, err
		}
		if envelope.Cursor != expected {
			return report, fmt.Errorf(
				"%w: have=%s want=%s",
				ErrCursorGap,
				formatCursor(envelope.Cursor),
				formatCursor(expected),
			)
		}
		if envelope.MarketSequence == r.checkpoint.MarketSequence &&
			r.checkpoint.MarketSequence != 0 &&
			envelope.BatchEventCount != r.checkpoint.BatchEventCount {
			return report, fmt.Errorf(
				"%w: sequence=%d batch_count=%d/%d",
				ErrCursorConflict,
				envelope.MarketSequence,
				r.checkpoint.BatchEventCount,
				envelope.BatchEventCount,
			)
		}
		if err := apply(envelope); err != nil {
			return report, fmt.Errorf(
				"apply trading event %s: %w",
				formatCursor(envelope.Cursor),
				err,
			)
		}
		r.checkpoint = Checkpoint{
			Cursor:          envelope.Cursor,
			BatchEventCount: envelope.BatchEventCount,
		}
		report.Applied++
		report.End = envelope.Cursor
	}

	r.head = head
	r.reconcileStarted = true
	r.complete = r.checkpoint.Cursor == head
	report.End = r.checkpoint.Cursor
	report.Complete = r.complete
	return report, nil
}

func (r *EventReconciler) Checkpoint() Checkpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.checkpoint
}

func (r *EventReconciler) RequireWritable() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.reconcileStarted || !r.complete || r.checkpoint.Cursor != r.head {
		return fmt.Errorf(
			"%w: checkpoint=%s head=%s",
			ErrReconcileIncomplete,
			formatCursor(r.checkpoint.Cursor),
			formatCursor(r.head),
		)
	}
	return nil
}

func validateCheckpoint(checkpoint Checkpoint) error {
	if err := validateCursor(checkpoint.Cursor); err != nil {
		return err
	}
	if checkpoint.MarketSequence == 0 {
		if checkpoint.BatchEventCount != 0 {
			return fmt.Errorf("%w: zero cursor cannot have batch metadata", ErrInvalidCursor)
		}
		return nil
	}
	if checkpoint.BatchEventCount == 0 ||
		checkpoint.EventIndex > checkpoint.BatchEventCount {
		return fmt.Errorf(
			"%w: cursor=%s batch_count=%d",
			ErrInvalidCursor,
			formatCursor(checkpoint.Cursor),
			checkpoint.BatchEventCount,
		)
	}
	return nil
}

func validateEnvelope(envelope EventEnvelope) error {
	if envelope.EventIndex == 0 {
		return fmt.Errorf(
			"%w: event envelope requires a non-zero event index",
			ErrInvalidCursor,
		)
	}
	if err := validateCheckpoint(Checkpoint{
		Cursor:          envelope.Cursor,
		BatchEventCount: envelope.BatchEventCount,
	}); err != nil {
		return err
	}
	if envelope.Event.Sequence != envelope.MarketSequence ||
		envelope.Event.Index != envelope.EventIndex {
		return fmt.Errorf(
			"%w: envelope=%s event=%d/%d",
			ErrCursorConflict,
			formatCursor(envelope.Cursor),
			envelope.Event.Sequence,
			envelope.Event.Index,
		)
	}
	return nil
}

func validateCursor(cursor Cursor) error {
	if cursor.MarketSequence == 0 {
		if cursor.EventIndex != 0 {
			return fmt.Errorf("%w: zero sequence requires zero event index", ErrInvalidCursor)
		}
		return nil
	}
	return nil
}

func nextCursor(checkpoint Checkpoint) (Cursor, error) {
	if checkpoint.MarketSequence == 0 {
		return Cursor{MarketSequence: 1, EventIndex: 1}, nil
	}
	if checkpoint.EventIndex < checkpoint.BatchEventCount {
		return Cursor{
			MarketSequence: checkpoint.MarketSequence,
			EventIndex:     checkpoint.EventIndex + 1,
		}, nil
	}
	if checkpoint.MarketSequence == math.MaxUint64 {
		return Cursor{}, fmt.Errorf("%w: market sequence overflow", ErrInvalidCursor)
	}
	return Cursor{
		MarketSequence: checkpoint.MarketSequence + 1,
		EventIndex:     1,
	}, nil
}

func compareCursor(left, right Cursor) int {
	switch {
	case left.MarketSequence < right.MarketSequence:
		return -1
	case left.MarketSequence > right.MarketSequence:
		return 1
	case left.EventIndex < right.EventIndex:
		return -1
	case left.EventIndex > right.EventIndex:
		return 1
	default:
		return 0
	}
}

func formatCursor(cursor Cursor) string {
	return fmt.Sprintf("%d/%d", cursor.MarketSequence, cursor.EventIndex)
}
