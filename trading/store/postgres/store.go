package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	corestore "github.com/the-web3/s78-market-services/trading/store"
)

var (
	ErrMarketConfigConflict = errors.New("persisted market configuration differs")
	ErrCommitOutcomeUnknown = errors.New("postgres commit outcome unknown")
)

type Store struct {
	pool   *pgxpool.Pool
	market domain.Market
}

type Cursor struct {
	Sequence   uint64 `json:"sequence"`
	EventIndex uint32 `json:"event_index"`
}

type OutboxEvent struct {
	MarketID        domain.MarketID  `json:"market_id"`
	Sequence        uint64           `json:"sequence"`
	EventIndex      uint32           `json:"event_index"`
	BatchEventCount uint32           `json:"batch_event_count,omitempty"`
	EventType       domain.EventType `json:"event_type"`
	Event           domain.Event     `json:"event"`
	PublishedAt     *time.Time       `json:"published_at,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
}

type ProjectionCheckpoint struct {
	MarketID   domain.MarketID `json:"market_id"`
	Sequence   uint64          `json:"sequence"`
	EventIndex uint32          `json:"event_index"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type PublishResult struct {
	Published  int
	Checkpoint Cursor
}

type recordQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("postgres pool is required")
	}
	if _, err := pool.Exec(ctx, Schema); err != nil {
		return fmt.Errorf("create trading schema: %w", err)
	}
	return nil
}

func New(ctx context.Context, pool *pgxpool.Pool, market domain.Market) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres pool is required")
	}
	if err := market.Validate(); err != nil {
		return nil, err
	}
	if market.ConfigurationEpoch == 0 || market.ConfigurationEpoch > math.MaxInt64 {
		return nil, fmt.Errorf("%w: configuration epoch is outside PostgreSQL bigint range", domain.ErrInvalidMarket)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO trading_market (
			market_id, base_asset, quote_asset, base_scale, quote_scale,
			price_tick, quantity_step, min_quantity, min_notional,
			maker_fee_bps, taker_fee_bps, configuration_epoch
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (market_id) DO NOTHING
	`,
		market.ID, market.BaseAsset, market.QuoteAsset, market.BaseScale, market.QuoteScale,
		market.PriceTick, market.QuantityStep, market.MinQuantity, market.MinNotional,
		market.MakerFeeBPS, market.TakerFeeBPS, int64(market.ConfigurationEpoch),
	)
	if err != nil {
		return nil, fmt.Errorf("register trading market: %w", err)
	}

	persisted, err := loadMarket(ctx, pool, market.ID)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(persisted, market) {
		return nil, fmt.Errorf("%w: market=%s", ErrMarketConfigConflict, market.ID)
	}
	return &Store{pool: pool, market: market}, nil
}

func (s *Store) Append(ctx context.Context, expectedSequence uint64, record corestore.Record) error {
	if err := validateRecord(s.market, expectedSequence, record); err != nil {
		return err
	}
	if expectedSequence > math.MaxInt64 || record.Command.Sequence > math.MaxInt64 {
		return fmt.Errorf("%w: sequence exceeds PostgreSQL bigint", corestore.ErrSequenceConflict)
	}

	commandPayload, err := json.Marshal(record.Command)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	resultPayload, err := json.Marshal(record.Result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	journalPayload, err := json.Marshal(record.Journal)
	if err != nil {
		return fmt.Errorf("marshal journal: %w", err)
	}
	projectionPayload, err := json.Marshal(record.Projection)
	if err != nil {
		return fmt.Errorf("marshal projection: %w", err)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin event append: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var current int64
	if err := tx.QueryRow(ctx,
		`SELECT current_sequence FROM trading_market WHERE market_id=$1 FOR UPDATE`,
		s.market.ID,
	).Scan(&current); err != nil {
		return fmt.Errorf("lock market stream: %w", err)
	}
	if uint64(current) != expectedSequence {
		return fmt.Errorf("%w: have=%d expected=%d", corestore.ErrSequenceConflict, current, expectedSequence)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO trading_event_batch (
			market_id, sequence, schema_version, operation, account_id,
			request_id, fingerprint, command_payload, result_payload,
			journal_payload, projection_payload, state_hash
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10::jsonb,$11::jsonb,$12)
	`,
		record.MarketID, int64(record.Command.Sequence), int16(record.SchemaVersion),
		int16(record.Command.Kind), record.Command.RequestKey.AccountID,
		record.Command.RequestID, record.Command.Fingerprint,
		string(commandPayload), string(resultPayload), string(journalPayload), string(projectionPayload), record.StateHash,
	)
	if err != nil {
		return classifyWriteError("insert event batch", err)
	}

	for _, event := range record.Result.Events {
		payload, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return fmt.Errorf("marshal outbox event: %w", marshalErr)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading_outbox (
				market_id, sequence, event_index, event_type, payload
			)
			VALUES ($1,$2,$3,$4,$5::jsonb)
		`, record.MarketID, int64(record.Command.Sequence), int32(event.Index), event.Type, string(payload)); err != nil {
			return classifyWriteError("insert outbox event", err)
		}
	}
	if err := applyProjection(ctx, tx, record); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE trading_market
		SET current_sequence=$2, updated_at=now()
		WHERE market_id=$1 AND current_sequence=$3
	`, s.market.ID, int64(record.Command.Sequence), int64(expectedSequence))
	if err != nil {
		return fmt.Errorf("advance market stream: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: stream compare-and-set failed", corestore.ErrSequenceConflict)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrCommitOutcomeUnknown, err)
	}
	return nil
}

func (s *Store) RecordsAfter(ctx context.Context, sequence uint64) ([]corestore.Record, error) {
	if sequence > math.MaxInt64 {
		return nil, fmt.Errorf("%w: sequence exceeds PostgreSQL bigint", corestore.ErrSequenceConflict)
	}
	return queryRecordsAfter(ctx, s.pool, s.market.ID, sequence)
}

func queryRecordsAfter(
	ctx context.Context,
	querier recordQuerier,
	marketID domain.MarketID,
	sequence uint64,
) ([]corestore.Record, error) {
	rows, err := querier.Query(ctx, `
		SELECT schema_version, sequence, command_payload, result_payload,
		       journal_payload, projection_payload, state_hash
		FROM trading_event_batch
		WHERE market_id=$1 AND sequence>$2
		ORDER BY sequence ASC
	`, marketID, int64(sequence))
	if err != nil {
		return nil, fmt.Errorf("query event records: %w", err)
	}
	defer rows.Close()

	var records []corestore.Record
	for rows.Next() {
		var (
			schemaVersion     int16
			storedSequence    int64
			commandPayload    []byte
			resultPayload     []byte
			journalPayload    []byte
			projectionPayload []byte
			stateHash         string
		)
		if err := rows.Scan(
			&schemaVersion, &storedSequence, &commandPayload, &resultPayload,
			&journalPayload, &projectionPayload, &stateHash,
		); err != nil {
			return nil, fmt.Errorf("scan event record: %w", err)
		}
		var command domain.Command
		if err := json.Unmarshal(commandPayload, &command); err != nil {
			return nil, fmt.Errorf("decode command at sequence %d: %w", storedSequence, err)
		}
		var result domain.Result
		if err := json.Unmarshal(resultPayload, &result); err != nil {
			return nil, fmt.Errorf("decode result at sequence %d: %w", storedSequence, err)
		}
		var journal []ledger.Transaction
		if err := json.Unmarshal(journalPayload, &journal); err != nil {
			return nil, fmt.Errorf("decode journal at sequence %d: %w", storedSequence, err)
		}
		var projection corestore.Projection
		if err := json.Unmarshal(projectionPayload, &projection); err != nil {
			return nil, fmt.Errorf("decode projection at sequence %d: %w", storedSequence, err)
		}
		record := corestore.Record{
			SchemaVersion: uint16(schemaVersion),
			MarketID:      marketID,
			Command:       command,
			Result:        result,
			Journal:       journal,
			Projection:    projection,
			StateHash:     stateHash,
		}
		if uint64(storedSequence) != command.Sequence {
			return nil, fmt.Errorf("stored sequence %d differs from command %d", storedSequence, command.Sequence)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate event records: %w", err)
	}
	return records, nil
}

func (s *Store) Save(ctx context.Context, snapshot corestore.Snapshot) error {
	if snapshot.SchemaVersion != corestore.CurrentSchemaVersion || snapshot.MarketID != s.market.ID ||
		snapshot.StateHash == "" || snapshot.Payload == nil || snapshot.Sequence > math.MaxInt64 {
		return fmt.Errorf("invalid snapshot metadata")
	}
	var current int64
	if err := s.pool.QueryRow(ctx,
		`SELECT current_sequence FROM trading_market WHERE market_id=$1`,
		s.market.ID,
	).Scan(&current); err != nil {
		return fmt.Errorf("read stream sequence for snapshot: %w", err)
	}
	if snapshot.Sequence > uint64(current) {
		return fmt.Errorf("%w: snapshot=%d stream=%d", corestore.ErrSequenceConflict, snapshot.Sequence, current)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trading_snapshot (
			market_id, schema_version, sequence, state_hash, payload
		)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (market_id) DO UPDATE
		SET schema_version=EXCLUDED.schema_version,
		    sequence=EXCLUDED.sequence,
		    state_hash=EXCLUDED.state_hash,
		    payload=EXCLUDED.payload,
		    updated_at=now()
		WHERE trading_snapshot.sequence <= EXCLUDED.sequence
	`, snapshot.MarketID, int16(snapshot.SchemaVersion), int64(snapshot.Sequence), snapshot.StateHash, snapshot.Payload)
	if err != nil {
		return fmt.Errorf("save trading snapshot: %w", err)
	}
	return nil
}

func (s *Store) Load(ctx context.Context) (corestore.Snapshot, bool, error) {
	var (
		schemaVersion int16
		sequence      int64
		stateHash     string
		payload       []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT schema_version, sequence, state_hash, payload
		FROM trading_snapshot
		WHERE market_id=$1
	`, s.market.ID).Scan(&schemaVersion, &sequence, &stateHash, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return corestore.Snapshot{}, false, nil
	}
	if err != nil {
		return corestore.Snapshot{}, false, fmt.Errorf("load trading snapshot: %w", err)
	}
	return corestore.Snapshot{
		SchemaVersion: uint16(schemaVersion),
		MarketID:      s.market.ID,
		Sequence:      uint64(sequence),
		StateHash:     stateHash,
		Payload:       append([]byte(nil), payload...),
	}, true, nil
}

func (s *Store) CurrentSequence(ctx context.Context) (uint64, error) {
	var sequence int64
	if err := s.pool.QueryRow(ctx,
		`SELECT current_sequence FROM trading_market WHERE market_id=$1`,
		s.market.ID,
	).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("read current stream sequence: %w", err)
	}
	return uint64(sequence), nil
}

// EventHead is the last immutable event cursor. It is used only by recovery
// admission control to prove that projections and the published feed caught up
// with the event authority before writes reopen.
func (s *Store) EventHead(ctx context.Context) (Cursor, error) {
	var (
		sequence   int64
		eventIndex int32
	)
	err := s.pool.QueryRow(ctx, `
		SELECT sequence,
		       COALESCE(jsonb_array_length(result_payload->'events'), 0)
		FROM trading_event_batch
		WHERE market_id=$1
		ORDER BY sequence DESC
		LIMIT 1
	`, s.market.ID).Scan(&sequence, &eventIndex)
	if errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, nil
	}
	if err != nil {
		return Cursor{}, fmt.Errorf("read trading event head: %w", err)
	}
	if sequence < 0 || eventIndex < 0 {
		return Cursor{}, fmt.Errorf("trading event head contains a negative cursor")
	}
	return Cursor{Sequence: uint64(sequence), EventIndex: uint32(eventIndex)}, nil
}

func (s *Store) GetOrder(ctx context.Context, orderID domain.OrderID) (domain.Order, bool, error) {
	if orderID == "" {
		return domain.Order{}, false, fmt.Errorf("order id is required")
	}
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT payload
		FROM trading_order
		WHERE market_id=$1 AND order_id=$2
	`, s.market.ID, orderID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, false, nil
	}
	if err != nil {
		return domain.Order{}, false, fmt.Errorf("query order projection %s: %w", orderID, err)
	}
	var order domain.Order
	if err := json.Unmarshal(payload, &order); err != nil {
		return domain.Order{}, false, fmt.Errorf("decode order projection %s: %w", orderID, err)
	}
	if order.ID != orderID || order.MarketID != s.market.ID {
		return domain.Order{}, false, fmt.Errorf("order projection identity mismatch")
	}
	return order, true, nil
}

func (s *Store) ListOrders(
	ctx context.Context,
	accountID domain.AccountID,
	openOnly bool,
	limit int,
) ([]domain.Order, error) {
	if limit <= 0 || limit > 1_000 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT payload
		FROM trading_order
		WHERE market_id=$1
		  AND ($2::text='' OR account_id=$2)
		  AND (NOT $3 OR status IN ('open', 'partially_filled'))
		ORDER BY updated_sequence DESC, order_id DESC
		LIMIT $4
	`, s.market.ID, accountID, openOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("query order projections: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan order projection: %w", err)
		}
		var order domain.Order
		if err := json.Unmarshal(payload, &order); err != nil {
			return nil, fmt.Errorf("decode order projection: %w", err)
		}
		if order.MarketID != s.market.ID || (accountID != "" && order.AccountID != accountID) {
			return nil, fmt.Errorf("order projection identity mismatch")
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order projections: %w", err)
	}
	return orders, nil
}

func (s *Store) ListTrades(
	ctx context.Context,
	accountID domain.AccountID,
	limit int,
) ([]domain.Trade, error) {
	if limit <= 0 || limit > 1_000 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT payload
		FROM trading_trade
		WHERE market_id=$1
		  AND ($2::text='' OR buyer_account_id=$2 OR seller_account_id=$2)
		ORDER BY sequence DESC, event_index DESC
		LIMIT $3
	`, s.market.ID, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("query trade projections: %w", err)
	}
	defer rows.Close()

	var trades []domain.Trade
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan trade projection: %w", err)
		}
		var trade domain.Trade
		if err := json.Unmarshal(payload, &trade); err != nil {
			return nil, fmt.Errorf("decode trade projection: %w", err)
		}
		if trade.MarketID != s.market.ID ||
			(accountID != "" && trade.BuyerAccountID != accountID && trade.SellerAccountID != accountID) {
			return nil, fmt.Errorf("trade projection identity mismatch")
		}
		trades = append(trades, trade)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trade projections: %w", err)
	}
	return trades, nil
}

func (s *Store) Balances(
	ctx context.Context,
	accountID domain.AccountID,
) ([]corestore.BalanceProjection, error) {
	if accountID == "" {
		return nil, fmt.Errorf("account id is required")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT asset, available, held
		FROM trading_balance
		WHERE market_id=$1 AND account_id=$2
		ORDER BY asset ASC
	`, s.market.ID, accountID)
	if err != nil {
		return nil, fmt.Errorf("query balance projections: %w", err)
	}
	defer rows.Close()

	var balances []corestore.BalanceProjection
	for rows.Next() {
		balance := corestore.BalanceProjection{AccountID: accountID}
		if err := rows.Scan(&balance.Asset, &balance.Available, &balance.Held); err != nil {
			return nil, fmt.Errorf("scan balance projection: %w", err)
		}
		if balance.Asset == "" || balance.Available < 0 || balance.Held < 0 {
			return nil, fmt.Errorf("invalid persisted balance projection")
		}
		balances = append(balances, balance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate balance projections: %w", err)
	}
	return balances, nil
}

func (s *Store) ProjectionCheckpoint(ctx context.Context) (ProjectionCheckpoint, bool, error) {
	var (
		sequence   int64
		eventIndex int32
		updatedAt  time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT sequence, event_index, updated_at
		FROM trading_projection_checkpoint
		WHERE market_id=$1
	`, s.market.ID).Scan(&sequence, &eventIndex, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectionCheckpoint{}, false, nil
	}
	if err != nil {
		return ProjectionCheckpoint{}, false, fmt.Errorf("query projection checkpoint: %w", err)
	}
	return ProjectionCheckpoint{
		MarketID:   s.market.ID,
		Sequence:   uint64(sequence),
		EventIndex: uint32(eventIndex),
		UpdatedAt:  updatedAt,
	}, true, nil
}

func (s *Store) RebuildProjections(ctx context.Context) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin projection rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var current int64
	if err := tx.QueryRow(ctx,
		`SELECT current_sequence FROM trading_market WHERE market_id=$1 FOR UPDATE`,
		s.market.ID,
	).Scan(&current); err != nil {
		return fmt.Errorf("lock market stream for projection rebuild: %w", err)
	}
	records, err := queryRecordsAfter(ctx, tx, s.market.ID, 0)
	if err != nil {
		return fmt.Errorf("read records for projection rebuild: %w", err)
	}
	if uint64(len(records)) != uint64(current) {
		return fmt.Errorf("%w: stream has %d records at sequence %d",
			corestore.ErrSequenceConflict, len(records), current)
	}
	for index, record := range records {
		if err := validateRecord(s.market, uint64(index), record); err != nil {
			return fmt.Errorf("validate rebuild record %d: %w", index+1, err)
		}
	}

	for _, table := range []string{
		"trading_order",
		"trading_trade",
		"trading_balance",
		"trading_ledger_entry",
		"trading_projection_checkpoint",
	} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE market_id=$1`, s.market.ID); err != nil {
			return fmt.Errorf("clear %s projection: %w", table, err)
		}
	}
	for _, record := range records {
		if err := applyProjection(ctx, tx, record); err != nil {
			return fmt.Errorf("rebuild projection at sequence %d: %w", record.Command.Sequence, err)
		}
	}
	if len(records) == 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading_projection_checkpoint (market_id, sequence, event_index)
			VALUES ($1,0,0)
		`, s.market.ID); err != nil {
			return fmt.Errorf("initialize empty projection checkpoint: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: projection rebuild: %v", ErrCommitOutcomeUnknown, err)
	}
	return nil
}

func (s *Store) OutboxAfter(ctx context.Context, cursor Cursor, limit int) ([]OutboxEvent, error) {
	if cursor.Sequence > math.MaxInt64 || cursor.EventIndex > math.MaxInt32 {
		return nil, fmt.Errorf("cursor exceeds PostgreSQL integer range")
	}
	if limit <= 0 || limit > 1_000 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT sequence, event_index, event_type, payload, published_at, created_at
		FROM trading_outbox
		WHERE market_id=$1
		  AND (sequence>$2 OR (sequence=$2 AND event_index>$3))
		ORDER BY sequence ASC, event_index ASC
		LIMIT $4
	`, s.market.ID, int64(cursor.Sequence), int32(cursor.EventIndex), limit)
	if err != nil {
		return nil, fmt.Errorf("query trading outbox: %w", err)
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var (
			sequence    int64
			eventIndex  int32
			eventType   string
			payload     []byte
			publishedAt *time.Time
			createdAt   time.Time
		)
		if err := rows.Scan(&sequence, &eventIndex, &eventType, &payload, &publishedAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan trading outbox: %w", err)
		}
		var event domain.Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("decode outbox event at %d/%d: %w", sequence, eventIndex, err)
		}
		if event.Sequence != uint64(sequence) || event.Index != uint32(eventIndex) ||
			event.Type != domain.EventType(eventType) {
			return nil, fmt.Errorf("outbox event identity mismatch at %d/%d", sequence, eventIndex)
		}
		events = append(events, OutboxEvent{
			MarketID:    s.market.ID,
			Sequence:    uint64(sequence),
			EventIndex:  uint32(eventIndex),
			EventType:   domain.EventType(eventType),
			Event:       event,
			PublishedAt: publishedAt,
			CreatedAt:   createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trading outbox: %w", err)
	}
	return events, nil
}

func (s *Store) PublishOutboxBatch(
	ctx context.Context,
	limit int,
) (PublishResult, error) {
	if limit <= 0 || limit > 1_000 {
		limit = 100
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PublishResult{}, fmt.Errorf("begin outbox publish: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(
			hashtext('qiu-market-outbox'),
			hashtext($1)
		)
	`, s.market.ID); err != nil {
		return PublishResult{}, fmt.Errorf("lock outbox publisher: %w", err)
	}

	var (
		published  int64
		sequence   int64
		eventIndex int32
	)
	if err := tx.QueryRow(ctx, `
		WITH picked AS MATERIALIZED (
			SELECT market_id, sequence, event_index, event_type, payload
			FROM trading_outbox
			WHERE market_id=$1 AND published_at IS NULL
			ORDER BY sequence ASC, event_index ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		),
		fed AS (
			INSERT INTO trading_event_feed (
				market_id, sequence, event_index, event_type, payload, published_at
			)
			SELECT market_id, sequence, event_index, event_type, payload, clock_timestamp()
			FROM picked
			ON CONFLICT (market_id, sequence, event_index) DO NOTHING
		),
		marked AS (
			UPDATE trading_outbox AS destination
			SET published_at=COALESCE(destination.published_at, clock_timestamp())
			FROM picked
			WHERE destination.market_id=picked.market_id
			  AND destination.sequence=picked.sequence
			  AND destination.event_index=picked.event_index
			RETURNING destination.sequence, destination.event_index
		)
		SELECT
			COUNT(*),
			COALESCE((
				SELECT sequence FROM marked
				ORDER BY sequence DESC, event_index DESC LIMIT 1
			), 0),
			COALESCE((
				SELECT event_index FROM marked
				ORDER BY sequence DESC, event_index DESC LIMIT 1
			), 0)
		FROM marked
	`, s.market.ID, limit).Scan(&published, &sequence, &eventIndex); err != nil {
		return PublishResult{}, fmt.Errorf("publish outbox batch: %w", err)
	}
	if published > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading_outbox_checkpoint (
				market_id, sequence, event_index, updated_at
			)
			VALUES ($1,$2,$3,clock_timestamp())
			ON CONFLICT (market_id) DO UPDATE
			SET sequence=EXCLUDED.sequence,
			    event_index=EXCLUDED.event_index,
			    updated_at=EXCLUDED.updated_at
			WHERE (
				trading_outbox_checkpoint.sequence,
				trading_outbox_checkpoint.event_index
			) < (EXCLUDED.sequence, EXCLUDED.event_index)
		`, s.market.ID, sequence, eventIndex); err != nil {
			return PublishResult{}, fmt.Errorf("advance outbox checkpoint: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PublishResult{}, fmt.Errorf("%w: publish outbox: %v", ErrCommitOutcomeUnknown, err)
	}
	return PublishResult{
		Published: int(published),
		Checkpoint: Cursor{
			Sequence:   uint64(sequence),
			EventIndex: uint32(eventIndex),
		},
	}, nil
}

func (s *Store) EventBatchSize(
	ctx context.Context,
	sequence uint64,
) (uint32, bool, error) {
	if sequence == 0 || sequence > math.MaxInt64 {
		return 0, false, fmt.Errorf("event batch sequence is outside PostgreSQL bigint range")
	}
	var count int32
	err := s.pool.QueryRow(ctx, `
		SELECT jsonb_array_length(result_payload->'events')
		FROM trading_event_batch
		WHERE market_id=$1 AND sequence=$2
	`, s.market.ID, int64(sequence)).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query trading event batch size: %w", err)
	}
	if count <= 0 {
		return 0, false, fmt.Errorf("trading event batch %d is empty", sequence)
	}
	return uint32(count), true, nil
}

func (s *Store) FeedAfter(ctx context.Context, cursor Cursor, limit int) ([]OutboxEvent, error) {
	if cursor.Sequence > math.MaxInt64 || cursor.EventIndex > math.MaxInt32 {
		return nil, fmt.Errorf("cursor exceeds PostgreSQL integer range")
	}
	if limit <= 0 || limit > 1_000 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT feed.sequence,
		       feed.event_index,
		       jsonb_array_length(batch.result_payload->'events'),
		       feed.event_type,
		       feed.payload,
		       feed.published_at
		FROM trading_event_feed AS feed
		JOIN trading_event_batch AS batch
		  ON batch.market_id=feed.market_id
		 AND batch.sequence=feed.sequence
		WHERE feed.market_id=$1
		  AND (feed.sequence>$2 OR (feed.sequence=$2 AND feed.event_index>$3))
		ORDER BY feed.sequence ASC, feed.event_index ASC
		LIMIT $4
	`, s.market.ID, int64(cursor.Sequence), int32(cursor.EventIndex), limit)
	if err != nil {
		return nil, fmt.Errorf("query trading event feed: %w", err)
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var (
			sequence        int64
			eventIndex      int32
			batchEventCount int32
			eventType       string
			payload         []byte
			publishedAt     time.Time
		)
		if err := rows.Scan(
			&sequence,
			&eventIndex,
			&batchEventCount,
			&eventType,
			&payload,
			&publishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan trading event feed: %w", err)
		}
		var event domain.Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf(
				"decode event feed at %d/%d: %w",
				sequence,
				eventIndex,
				err,
			)
		}
		if event.Sequence != uint64(sequence) ||
			event.Index != uint32(eventIndex) ||
			batchEventCount <= 0 ||
			eventIndex > batchEventCount ||
			event.Type != domain.EventType(eventType) {
			return nil, fmt.Errorf(
				"event feed identity mismatch at %d/%d",
				sequence,
				eventIndex,
			)
		}
		events = append(events, OutboxEvent{
			MarketID:        s.market.ID,
			Sequence:        uint64(sequence),
			EventIndex:      uint32(eventIndex),
			BatchEventCount: uint32(batchEventCount),
			EventType:       domain.EventType(eventType),
			Event:           event,
			PublishedAt:     &publishedAt,
			CreatedAt:       publishedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trading event feed: %w", err)
	}
	return events, nil
}

func (s *Store) OutboxCheckpoint(
	ctx context.Context,
) (Cursor, bool, error) {
	var (
		sequence   int64
		eventIndex int32
	)
	err := s.pool.QueryRow(ctx, `
		SELECT sequence, event_index
		FROM trading_outbox_checkpoint
		WHERE market_id=$1
	`, s.market.ID).Scan(&sequence, &eventIndex)
	if errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, false, nil
	}
	if err != nil {
		return Cursor{}, false, fmt.Errorf("read outbox checkpoint: %w", err)
	}
	return Cursor{
		Sequence:   uint64(sequence),
		EventIndex: uint32(eventIndex),
	}, true, nil
}

func (s *Store) CleanupPublishedOutbox(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	if before.IsZero() {
		return 0, fmt.Errorf("outbox cleanup boundary is required")
	}
	if limit <= 0 || limit > 10_000 {
		limit = 1_000
	}
	tag, err := s.pool.Exec(ctx, `
		WITH doomed AS (
			SELECT source.market_id, source.sequence, source.event_index
			FROM trading_outbox AS source
			WHERE source.market_id=$1
			  AND source.published_at IS NOT NULL
			  AND source.published_at < $2
			  AND EXISTS (
				SELECT 1
				FROM trading_event_feed AS feed
				WHERE feed.market_id=source.market_id
				  AND feed.sequence=source.sequence
				  AND feed.event_index=source.event_index
			  )
			ORDER BY source.sequence ASC, source.event_index ASC
			LIMIT $3
		)
		DELETE FROM trading_outbox AS destination
		USING doomed
		WHERE destination.market_id=doomed.market_id
		  AND destination.sequence=doomed.sequence
		  AND destination.event_index=doomed.event_index
	`, s.market.ID, before.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("cleanup published outbox: %w", err)
	}
	return tag.RowsAffected(), nil
}

func validateRecord(market domain.Market, expectedSequence uint64, record corestore.Record) error {
	if record.SchemaVersion != corestore.CurrentSchemaVersion || record.MarketID != market.ID ||
		record.Command.Sequence != expectedSequence+1 || record.Result.Sequence != record.Command.Sequence ||
		record.Command.RequestKey.MarketID != market.ID || record.Command.RequestKey.Operation != record.Command.Kind ||
		record.Command.RequestKey.RequestID != record.Command.RequestID ||
		record.Command.Fingerprint == "" || record.StateHash == "" {
		return fmt.Errorf("invalid event record metadata")
	}
	if err := record.Command.RequestKey.Validate(); err != nil {
		return err
	}
	seen := make(map[uint32]struct{}, len(record.Result.Events))
	tradeEvents := make(map[domain.TradeID]domain.Event)
	for index, event := range record.Result.Events {
		if event.Sequence != record.Command.Sequence || event.Index != uint32(index+1) ||
			event.Index > math.MaxInt32 {
			return fmt.Errorf("event identity does not match batch")
		}
		if _, duplicate := seen[event.Index]; duplicate {
			return fmt.Errorf("duplicate event index %d", event.Index)
		}
		seen[event.Index] = struct{}{}
		if event.Trade != nil {
			if event.Trade.ID == "" || event.Trade.MarketID != market.ID {
				return fmt.Errorf("invalid trade event projection identity")
			}
			if _, duplicate := tradeEvents[event.Trade.ID]; duplicate {
				return fmt.Errorf("duplicate trade event %s", event.Trade.ID)
			}
			tradeEvents[event.Trade.ID] = event
		}
	}
	orderIDs := make(map[domain.OrderID]struct{}, len(record.Projection.Orders))
	for _, order := range record.Projection.Orders {
		if order.ID == "" || order.AccountID == "" || order.MarketID != market.ID ||
			order.HeldAmount < 0 || order.LastSequence != record.Command.Sequence ||
			order.LastSequence > math.MaxInt64 ||
			order.Status < domain.OrderStatusReceived || order.Status > domain.OrderStatusCanceled ||
			(order.HeldAsset != "" && order.HeldAsset != market.BaseAsset && order.HeldAsset != market.QuoteAsset) {
			return fmt.Errorf("invalid order projection")
		}
		if _, duplicate := orderIDs[order.ID]; duplicate {
			return fmt.Errorf("duplicate order projection %s", order.ID)
		}
		orderIDs[order.ID] = struct{}{}
	}
	tradeIDs := make(map[domain.TradeID]struct{}, len(record.Projection.Trades))
	for _, trade := range record.Projection.Trades {
		if trade.ID == "" || trade.MarketID != market.ID || trade.BuyerAccountID == "" ||
			trade.SellerAccountID == "" {
			return fmt.Errorf("invalid trade projection")
		}
		if _, duplicate := tradeIDs[trade.ID]; duplicate {
			return fmt.Errorf("duplicate trade projection %s", trade.ID)
		}
		tradeIDs[trade.ID] = struct{}{}
		event, exists := tradeEvents[trade.ID]
		if !exists {
			return fmt.Errorf("trade projection %s has no matching event", trade.ID)
		}
		if !reflect.DeepEqual(*event.Trade, trade) {
			return fmt.Errorf("trade projection %s differs from event", trade.ID)
		}
	}
	balanceKeys := make(map[string]struct{}, len(record.Projection.Balances))
	for _, balance := range record.Projection.Balances {
		if balance.AccountID == "" || balance.Asset == "" ||
			(balance.Asset != market.BaseAsset && balance.Asset != market.QuoteAsset) ||
			balance.Available < 0 || balance.Held < 0 {
			return fmt.Errorf("invalid balance projection")
		}
		key := string(balance.AccountID) + "\x00" + string(balance.Asset)
		if _, duplicate := balanceKeys[key]; duplicate {
			return fmt.Errorf("duplicate balance projection %s/%s", balance.AccountID, balance.Asset)
		}
		balanceKeys[key] = struct{}{}
	}
	transactionIDs := make(map[string]struct{}, len(record.Journal))
	for _, transaction := range record.Journal {
		if transaction.ID == "" || transaction.Reference == "" || len(transaction.Entries) == 0 ||
			len(transaction.Entries) > math.MaxInt32 {
			return fmt.Errorf("invalid ledger transaction projection")
		}
		if _, duplicate := transactionIDs[transaction.ID]; duplicate {
			return fmt.Errorf("duplicate ledger transaction projection %s", transaction.ID)
		}
		transactionIDs[transaction.ID] = struct{}{}
		totals := make(map[domain.Asset]int64)
		for _, entry := range transaction.Entries {
			if entry.Account == "" || entry.Asset == "" ||
				(entry.Asset != market.BaseAsset && entry.Asset != market.QuoteAsset) ||
				entry.Amount == 0 {
				return fmt.Errorf("invalid ledger entry projection")
			}
			total, err := domain.CheckedAdd(totals[entry.Asset], entry.Amount)
			if err != nil {
				return fmt.Errorf("ledger transaction %s overflows: %w", transaction.ID, err)
			}
			totals[entry.Asset] = total
		}
		for asset, total := range totals {
			if total != 0 {
				return fmt.Errorf("ledger transaction %s is unbalanced for %s", transaction.ID, asset)
			}
		}
	}
	return nil
}

func applyProjection(ctx context.Context, tx pgx.Tx, record corestore.Record) error {
	for _, order := range record.Projection.Orders {
		payload, err := json.Marshal(order)
		if err != nil {
			return fmt.Errorf("marshal order projection %s: %w", order.ID, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading_order (
				market_id, order_id, account_id, status, updated_sequence, payload
			)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb)
			ON CONFLICT (market_id, order_id) DO UPDATE
			SET account_id=EXCLUDED.account_id,
			    status=EXCLUDED.status,
			    updated_sequence=EXCLUDED.updated_sequence,
			    payload=EXCLUDED.payload
			WHERE trading_order.updated_sequence < EXCLUDED.updated_sequence
		`, record.MarketID, order.ID, order.AccountID, order.Status.String(),
			int64(record.Command.Sequence), string(payload)); err != nil {
			return fmt.Errorf("upsert order projection %s: %w", order.ID, err)
		}
	}

	tradeIndexes := make(map[domain.TradeID]uint32)
	for _, event := range record.Result.Events {
		if event.Trade != nil {
			tradeIndexes[event.Trade.ID] = event.Index
		}
	}
	for _, trade := range record.Projection.Trades {
		eventIndex, exists := tradeIndexes[trade.ID]
		if !exists {
			return fmt.Errorf("trade projection %s has no matching event", trade.ID)
		}
		payload, err := json.Marshal(trade)
		if err != nil {
			return fmt.Errorf("marshal trade projection %s: %w", trade.ID, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading_trade (
				market_id, trade_id, sequence, event_index,
				buyer_account_id, seller_account_id, payload
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)
		`, record.MarketID, trade.ID, int64(record.Command.Sequence), int32(eventIndex),
			trade.BuyerAccountID, trade.SellerAccountID, string(payload)); err != nil {
			return fmt.Errorf("insert trade projection %s: %w", trade.ID, err)
		}
	}

	for _, balance := range record.Projection.Balances {
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading_balance (
				market_id, account_id, asset, available, held, updated_sequence
			)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (market_id, account_id, asset) DO UPDATE
			SET available=EXCLUDED.available,
			    held=EXCLUDED.held,
			    updated_sequence=EXCLUDED.updated_sequence
			WHERE trading_balance.updated_sequence < EXCLUDED.updated_sequence
		`, record.MarketID, balance.AccountID, balance.Asset, balance.Available,
			balance.Held, int64(record.Command.Sequence)); err != nil {
			return fmt.Errorf("upsert balance projection %s/%s: %w",
				balance.AccountID, balance.Asset, err)
		}
	}

	for _, transaction := range record.Journal {
		for index, entry := range transaction.Entries {
			if _, err := tx.Exec(ctx, `
				INSERT INTO trading_ledger_entry (
					market_id, sequence, transaction_id, entry_index,
					account, asset, amount, reference
				)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			`, record.MarketID, int64(record.Command.Sequence), transaction.ID, index+1,
				entry.Account, entry.Asset, entry.Amount, transaction.Reference); err != nil {
				return fmt.Errorf("insert ledger projection %s/%d: %w", transaction.ID, index+1, err)
			}
		}
	}

	var lastEventIndex uint32
	if count := len(record.Result.Events); count > 0 {
		lastEventIndex = record.Result.Events[count-1].Index
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trading_projection_checkpoint (
			market_id, sequence, event_index
		)
		VALUES ($1,$2,$3)
		ON CONFLICT (market_id) DO UPDATE
		SET sequence=EXCLUDED.sequence,
		    event_index=EXCLUDED.event_index,
		    updated_at=now()
		WHERE (trading_projection_checkpoint.sequence,
		       trading_projection_checkpoint.event_index)
		      < (EXCLUDED.sequence, EXCLUDED.event_index)
	`, record.MarketID, int64(record.Command.Sequence), int32(lastEventIndex)); err != nil {
		return fmt.Errorf("advance projection checkpoint: %w", err)
	}
	return nil
}

func classifyWriteError(operation string, err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return fmt.Errorf("%w: %s", corestore.ErrSequenceConflict, operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func loadMarket(ctx context.Context, pool *pgxpool.Pool, marketID domain.MarketID) (domain.Market, error) {
	var (
		market             domain.Market
		configurationEpoch int64
	)
	err := pool.QueryRow(ctx, `
		SELECT market_id, base_asset, quote_asset, base_scale, quote_scale,
		       price_tick, quantity_step, min_quantity, min_notional,
		       maker_fee_bps, taker_fee_bps, configuration_epoch
		FROM trading_market
		WHERE market_id=$1
	`, marketID).Scan(
		&market.ID, &market.BaseAsset, &market.QuoteAsset, &market.BaseScale, &market.QuoteScale,
		&market.PriceTick, &market.QuantityStep, &market.MinQuantity, &market.MinNotional,
		&market.MakerFeeBPS, &market.TakerFeeBPS, &configurationEpoch,
	)
	if err != nil {
		return domain.Market{}, fmt.Errorf("load trading market: %w", err)
	}
	market.ConfigurationEpoch = uint64(configurationEpoch)
	return market, nil
}
