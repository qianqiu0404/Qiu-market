package marketmaker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
)

func TestRejectedQuoteClassificationAndCleanup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		reason        string
		submitErr     error
		cancelErr     error
		wantBlocked   bool
		wantErr       string
		wantCancelled bool
	}{
		{
			name:          "post only crossing is recoverable",
			reason:        "post_only_would_cross",
			wantBlocked:   true,
			wantCancelled: true,
		},
		{
			name:          "insufficient balance stays fatal",
			reason:        "insufficient_balance",
			wantErr:       "rejected with status rejected",
			wantCancelled: true,
		},
		{
			name:          "cleanup failure stays fatal",
			reason:        "post_only_would_cross",
			cancelErr:     errors.New("cancel persistence unavailable"),
			wantErr:       "clean up partial demo-maker quote set: cancel persistence unavailable",
			wantCancelled: true,
		},
		{
			name:          "submit transport failure stays fatal",
			submitErr:     errors.New("submit transport unavailable"),
			wantErr:       "submit demo-maker bid: submit transport unavailable",
			wantCancelled: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := &rejectingEngine{reason: test.reason, submitErr: test.submitErr, cancelErr: test.cancelErr}
			tracker := NewStatusTracker(true)
			config := DefaultConfig()
			config.SpreadsBPS = []int64{10}
			config.Status = tracker
			observedAt := time.Now().Add(-time.Second).UTC()
			maker, err := New(
				domain.DefaultBTCUSDTMarket(),
				engine,
				fixedReferenceSource{reference: Reference{Price: 60_000_000_000, ObservedAt: observedAt}},
				config,
			)
			if err != nil {
				t.Fatal(err)
			}
			err = maker.Refresh(context.Background())
			if errors.Is(err, ErrQuoteBlocked) != test.wantBlocked {
				t.Fatalf("Refresh error = %v; blocked=%v, want %v", err, errors.Is(err, ErrQuoteBlocked), test.wantBlocked)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("Refresh error = %v, want substring %q", err, test.wantErr)
			}
			if engine.cancelCalled != test.wantCancelled {
				t.Fatalf("cleanup cancel called = %v, want %v", engine.cancelCalled, test.wantCancelled)
			}
			if test.wantBlocked {
				status := tracker.Status()
				if status.State != LiquidityRecovering || !status.ReferenceObservedAt.Equal(observedAt) || status.LastRefreshAt.IsZero() {
					t.Fatalf("recoverable status = %+v, want observed_at=%s and refreshed timestamp", status, observedAt)
				}
			}
		})
	}
}

type fixedReferenceSource struct {
	reference Reference
}

func (s fixedReferenceSource) Current(context.Context) (Reference, error) {
	return s.reference, nil
}

type rejectingEngine struct {
	reason       string
	submitErr    error
	cancelErr    error
	submitted    bool
	cancelCalled bool
}

func (e *rejectingEngine) Submit(context.Context, domain.NewOrder) (domain.Result, error) {
	e.submitted = true
	return domain.Result{
		Status: domain.OrderStatusRejected,
		Events: []domain.Event{{Type: domain.EventOrderRejected, Reason: e.reason}},
	}, e.submitErr
}

func (e *rejectingEngine) Cancel(context.Context, domain.CancelOrder) (domain.Result, error) {
	e.cancelCalled = true
	return domain.Result{}, e.cancelErr
}

func (e *rejectingEngine) Orders(domain.AccountID, bool) ([]domain.Order, error) {
	if !e.submitted {
		return nil, nil
	}
	return []domain.Order{{ID: "partial-maker-quote"}}, nil
}

func (e *rejectingEngine) Depth(int) (exchange.OrderBookView, error) {
	return exchange.OrderBookView{}, nil
}
