package server_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/the-web3/s78-market-services/trading/domain"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
)

type scriptedEventSource struct {
	events      []tradingserver.StoredEvent
	batchCounts map[uint64]uint32
}

func (s *scriptedEventSource) EventsAfter(
	context.Context,
	tradingserver.Cursor,
	int,
) ([]tradingserver.StoredEvent, error) {
	return append([]tradingserver.StoredEvent(nil), s.events...), nil
}

func (s *scriptedEventSource) BatchEventCount(
	_ context.Context,
	sequence uint64,
) (uint32, bool, error) {
	count, found := s.batchCounts[sequence]
	return count, found, nil
}

func TestTradingGRPCSubscribeEventsDeduplicatesReconnectCursor(t *testing.T) {
	source := &scriptedEventSource{
		batchCounts: map[uint64]uint32{1: 2},
		events: []tradingserver.StoredEvent{
			storedEvent(1, 1, 2),
			storedEvent(1, 2, 2),
		},
	}
	client, _ := newTestClient(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	stream, err := client.SubscribeEvents(ctx, &tradingv1.SubscribeEventsRequest{
		MarketId:         "BTC-USDT",
		CursorSequence:   "1",
		CursorEventIndex: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != "1" || event.EventIndex != 2 {
		t.Fatalf("reconnected event = %+v", event)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("duplicate cursor was redelivered, second receive error = %v", err)
	}
}

func TestTradingGRPCSubscribeEventsFailsClosedOnGap(t *testing.T) {
	source := &scriptedEventSource{
		batchCounts: map[uint64]uint32{1: 3},
		events: []tradingserver.StoredEvent{
			storedEvent(1, 3, 3),
		},
	}
	client, _ := newTestClient(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := client.SubscribeEvents(ctx, &tradingv1.SubscribeEventsRequest{
		MarketId:         "BTC-USDT",
		CursorSequence:   "1",
		CursorEventIndex: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.DataLoss {
		t.Fatalf("gap stream error = %v", err)
	}
}

func storedEvent(
	sequence uint64,
	index, batchEventCount uint32,
) tradingserver.StoredEvent {
	return tradingserver.StoredEvent{
		MarketID:        "BTC-USDT",
		Cursor:          tradingserver.Cursor{Sequence: sequence, EventIndex: index},
		BatchEventCount: batchEventCount,
		Event: domain.Event{
			Sequence:  sequence,
			Index:     index,
			Type:      domain.EventOrderAccepted,
			AccountID: "alice",
			OrderID:   domain.OrderID("order-1"),
		},
	}
}
