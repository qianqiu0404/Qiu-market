package reliability_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/reliability"
)

func TestEventReconcilerDeduplicatesAndCompletesAtHead(t *testing.T) {
	reconciler, err := reliability.NewEventReconciler(reliability.Checkpoint{})
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.RequireWritable(); !errors.Is(err, reliability.ErrReconcileIncomplete) {
		t.Fatalf("write gate before reconcile = %v", err)
	}

	events := []reliability.EventEnvelope{
		reconcileEvent(1, 1, 2),
		reconcileEvent(1, 1, 2),
		reconcileEvent(1, 2, 2),
		reconcileEvent(1, 2, 2),
		reconcileEvent(2, 1, 1),
	}
	var applied []string
	report, err := reconciler.Reconcile(
		reliability.Cursor{MarketSequence: 2, EventIndex: 1},
		events,
		func(event reliability.EventEnvelope) error {
			applied = append(applied, cursorLabel(event.Cursor))
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Applied != 3 || report.Duplicates != 2 ||
		report.End != (reliability.Cursor{MarketSequence: 2, EventIndex: 1}) {
		t.Fatalf("dedupe report = %+v", report)
	}
	if fmt.Sprint(applied) != "[1/1 1/2 2/1]" {
		t.Fatalf("applied cursors = %v", applied)
	}
	if err := reconciler.RequireWritable(); err != nil {
		t.Fatalf("write gate after complete reconcile = %v", err)
	}
}

func TestEventReconcilerFailsClosedOnGaps(t *testing.T) {
	tests := []struct {
		name       string
		checkpoint reliability.Checkpoint
		head       reliability.Cursor
		event      reliability.EventEnvelope
	}{
		{
			name: "missing event within batch",
			checkpoint: reliability.Checkpoint{
				Cursor:          reliability.Cursor{MarketSequence: 1, EventIndex: 1},
				BatchEventCount: 3,
			},
			head:  reliability.Cursor{MarketSequence: 1, EventIndex: 3},
			event: reconcileEvent(1, 3, 3),
		},
		{
			name: "missing entire command batch",
			checkpoint: reliability.Checkpoint{
				Cursor:          reliability.Cursor{MarketSequence: 1, EventIndex: 2},
				BatchEventCount: 2,
			},
			head:  reliability.Cursor{MarketSequence: 3, EventIndex: 1},
			event: reconcileEvent(3, 1, 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reconciler, err := reliability.NewEventReconciler(test.checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			report, err := reconciler.Reconcile(
				test.head,
				[]reliability.EventEnvelope{test.event},
				func(reliability.EventEnvelope) error { return nil },
			)
			if !errors.Is(err, reliability.ErrCursorGap) {
				t.Fatalf("gap error = %v report=%+v", err, report)
			}
			if reconciler.Checkpoint() != test.checkpoint {
				t.Fatalf("gap advanced checkpoint = %+v", reconciler.Checkpoint())
			}
			if err := reconciler.RequireWritable(); !errors.Is(err, reliability.ErrReconcileIncomplete) {
				t.Fatalf("write gate after gap = %v", err)
			}
		})
	}
}

func TestEventReconcilerReconnectsInPagesWithoutReapplying(t *testing.T) {
	reconciler, err := reliability.NewEventReconciler(reliability.Checkpoint{})
	if err != nil {
		t.Fatal(err)
	}
	head := reliability.Cursor{MarketSequence: 3, EventIndex: 1}
	var applied []string
	apply := func(event reliability.EventEnvelope) error {
		applied = append(applied, cursorLabel(event.Cursor))
		return nil
	}

	first, err := reconciler.Reconcile(
		head,
		[]reliability.EventEnvelope{reconcileEvent(1, 1, 1)},
		apply,
	)
	if err != nil || first.Complete {
		t.Fatalf("first disconnected page = %+v error=%v", first, err)
	}
	if err := reconciler.RequireWritable(); !errors.Is(err, reliability.ErrReconcileIncomplete) {
		t.Fatalf("write gate after first page = %v", err)
	}

	second, err := reconciler.Reconcile(
		head,
		[]reliability.EventEnvelope{
			reconcileEvent(1, 1, 1),
			reconcileEvent(2, 1, 2),
		},
		apply,
	)
	if err != nil || second.Complete || second.Applied != 1 || second.Duplicates != 1 {
		t.Fatalf("second disconnected page = %+v error=%v", second, err)
	}

	third, err := reconciler.Reconcile(
		head,
		[]reliability.EventEnvelope{
			reconcileEvent(2, 1, 2),
			reconcileEvent(2, 2, 2),
			reconcileEvent(3, 1, 1),
		},
		apply,
	)
	if err != nil || !third.Complete || third.Applied != 2 || third.Duplicates != 1 {
		t.Fatalf("final reconnect page = %+v error=%v", third, err)
	}
	if fmt.Sprint(applied) != "[1/1 2/1 2/2 3/1]" {
		t.Fatalf("reconnect applied cursors = %v", applied)
	}
	if err := reconciler.RequireWritable(); err != nil {
		t.Fatalf("write gate after reconnect = %v", err)
	}
}

func TestEventReconcilerReplaysOnlyAfterSnapshotCheckpoint(t *testing.T) {
	snapshot := reliability.Checkpoint{
		Cursor:          reliability.Cursor{MarketSequence: 2, EventIndex: 2},
		BatchEventCount: 2,
	}
	reconciler, err := reliability.NewEventReconciler(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var applied []string
	report, err := reconciler.Reconcile(
		reliability.Cursor{MarketSequence: 3, EventIndex: 2},
		[]reliability.EventEnvelope{
			reconcileEvent(1, 1, 1),
			reconcileEvent(2, 2, 2),
			reconcileEvent(3, 1, 2),
			reconcileEvent(3, 2, 2),
		},
		func(event reliability.EventEnvelope) error {
			applied = append(applied, cursorLabel(event.Cursor))
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Applied != 2 || report.Duplicates != 2 ||
		fmt.Sprint(applied) != "[3/1 3/2]" {
		t.Fatalf("snapshot replay report=%+v applied=%v", report, applied)
	}
}

func reconcileEvent(
	sequence uint64,
	index, batchEventCount uint32,
) reliability.EventEnvelope {
	return reliability.EventEnvelope{
		Cursor: reliability.Cursor{
			MarketSequence: sequence,
			EventIndex:     index,
		},
		BatchEventCount: batchEventCount,
		Event: domain.Event{
			Sequence: sequence,
			Index:    index,
			Type:     domain.EventOrderAccepted,
		},
	}
}

func cursorLabel(cursor reliability.Cursor) string {
	return fmt.Sprintf("%d/%d", cursor.MarketSequence, cursor.EventIndex)
}
