package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/store"
)

func TestMemoryStoreEnforcesExpectedSequenceAndCopiesData(t *testing.T) {
	t.Parallel()

	memory := store.NewMemory()
	record := store.Record{
		Command: domain.Command{
			Sequence:    1,
			RequestID:   "r1",
			Fingerprint: "fingerprint",
			Kind:        domain.CommandKindFund,
		},
		Result: domain.Result{
			Sequence: 1,
			Events: []domain.Event{{
				Sequence: 1,
				Index:    1,
				Type:     domain.EventAccountFunded,
			}},
		},
		StateHash: "hash-1",
	}
	if err := memory.Append(context.Background(), 0, record); err != nil {
		t.Fatal(err)
	}
	record.StateHash = "mutated-by-caller"
	records, err := memory.RecordsAfter(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].StateHash != "hash-1" {
		t.Fatalf("stored records = %+v", records)
	}
	if err := memory.Append(context.Background(), 0, record); !errors.Is(err, store.ErrSequenceConflict) {
		t.Fatalf("sequence conflict error = %v", err)
	}
}
