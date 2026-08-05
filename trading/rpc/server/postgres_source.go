package server

import (
	"context"

	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

type PostgresEventFeed interface {
	FeedAfter(context.Context, postgresstore.Cursor, int) ([]postgresstore.OutboxEvent, error)
	EventBatchSize(context.Context, uint64) (uint32, bool, error)
}

type PostgresEventSource struct {
	store PostgresEventFeed
}

func NewPostgresEventSource(store PostgresEventFeed) *PostgresEventSource {
	return &PostgresEventSource{store: store}
}

func (s *PostgresEventSource) EventsAfter(
	ctx context.Context,
	cursor Cursor,
	limit int,
) ([]StoredEvent, error) {
	events, err := s.store.FeedAfter(ctx, postgresstore.Cursor{
		Sequence:   cursor.Sequence,
		EventIndex: cursor.EventIndex,
	}, limit)
	if err != nil {
		return nil, err
	}
	result := make([]StoredEvent, 0, len(events))
	for _, event := range events {
		result = append(result, StoredEvent{
			MarketID: event.MarketID,
			Cursor: Cursor{
				Sequence:   event.Sequence,
				EventIndex: event.EventIndex,
			},
			BatchEventCount: event.BatchEventCount,
			Event:           event.Event,
		})
	}
	return result, nil
}

func (s *PostgresEventSource) BatchEventCount(
	ctx context.Context,
	sequence uint64,
) (uint32, bool, error) {
	return s.store.EventBatchSize(ctx, sequence)
}
