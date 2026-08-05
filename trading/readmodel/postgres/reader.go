// Package postgres implements the Trade Product V1 query.Reader from
// rebuildable PostgreSQL projections. It never reads browser-supplied account
// identity and never returns the counterparty side of a trade or ledger entry.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/query"
	sharedreadmodel "github.com/the-web3/s78-market-services/trading/readmodel"
)

var (
	ErrInvalidQuery = errors.New("invalid Trade V1 query")
	ErrIntegrity    = errors.New("Trade V1 read-model integrity error")
)

type Reader struct {
	pool   *pgxpool.Pool
	market domain.Market
}

var _ query.Reader = (*Reader)(nil)

func New(pool *pgxpool.Pool, market domain.Market) (*Reader, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: postgres pool is required", ErrInvalidQuery)
	}
	if err := market.Validate(); err != nil {
		return nil, err
	}
	return &Reader{pool: pool, market: market}, nil
}

func (r *Reader) GetOrder(
	ctx context.Context,
	accountID domain.AccountID,
	orderID domain.OrderID,
) (query.OrderView, bool, error) {
	if accountID == "" || orderID == "" {
		return query.OrderView{}, false, fmt.Errorf(
			"%w: account and order are required",
			ErrInvalidQuery,
		)
	}
	var (
		payload          []byte
		acceptedSequence int64
		updatedSequence  int64
		createdAt        time.Time
		updatedAt        time.Time
	)
	err := r.pool.QueryRow(ctx, `
		SELECT payload, accepted_sequence, updated_sequence, created_at, updated_at
		FROM trading_order
		WHERE market_id=$1 AND account_id=$2 AND order_id=$3
	`, r.market.ID, accountID, orderID).Scan(
		&payload,
		&acceptedSequence,
		&updatedSequence,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return query.OrderView{}, false, nil
	}
	if err != nil {
		return query.OrderView{}, false, fmt.Errorf("query account order: %w", err)
	}
	view, err := r.orderView(
		payload,
		acceptedSequence,
		updatedSequence,
		createdAt,
		updatedAt,
		accountID,
	)
	if err != nil {
		return query.OrderView{}, false, err
	}
	if view.Order.ID != orderID {
		return query.OrderView{}, false, fmt.Errorf("%w: order row identity mismatch", ErrIntegrity)
	}
	return view, true, nil
}

func (r *Reader) ListOrders(
	ctx context.Context,
	accountID domain.AccountID,
	filter query.OrderFilter,
	cursor *query.OrderCursor,
	limit int,
) (query.OrderPage, error) {
	if accountID == "" {
		return query.OrderPage{}, fmt.Errorf("%w: account is required", ErrInvalidQuery)
	}
	if err := validateLimit(limit); err != nil {
		return query.OrderPage{}, err
	}
	scope, err := normalizeOrderScope(filter.Scope)
	if err != nil {
		return query.OrderPage{}, err
	}
	status, err := normalizeOrderStatus(filter.Status)
	if err != nil {
		return query.OrderPage{}, err
	}
	side, err := normalizeSide(filter.Side)
	if err != nil {
		return query.OrderPage{}, err
	}
	orderType, err := normalizeOrderType(filter.Type)
	if err != nil {
		return query.OrderPage{}, err
	}
	var cursorSequence int64 = math.MaxInt64
	cursorOrderID := "\uffff"
	if cursor != nil {
		if cursor.AcceptedSequence == 0 || cursor.AcceptedSequence > math.MaxInt64 ||
			cursor.OrderID == "" {
			return query.OrderPage{}, fmt.Errorf("%w: invalid order cursor", ErrInvalidQuery)
		}
		cursorSequence = int64(cursor.AcceptedSequence)
		cursorOrderID = string(cursor.OrderID)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT order_id, payload, accepted_sequence, updated_sequence, created_at, updated_at
		FROM trading_order
		WHERE market_id=$1
		  AND account_id=$2
		  AND status IN ('open','partially_filled','filled','canceled','rejected')
		  AND (
		      $3='all'
		      OR ($3='open' AND status IN ('open','partially_filled'))
		      OR ($3='history' AND status IN ('filled','canceled','rejected'))
		  )
		  AND ($4='' OR status=$4)
		  AND ($5::smallint=0 OR (payload->>'side')::smallint=$5)
		  AND ($6::smallint=0 OR (payload->>'type')::smallint=$6)
		  AND (accepted_sequence, order_id) < ($7,$8)
		ORDER BY accepted_sequence DESC, order_id DESC
		LIMIT $9
	`, r.market.ID, accountID, scope, status, int16(side), int16(orderType),
		cursorSequence, cursorOrderID, limit+1)
	if err != nil {
		return query.OrderPage{}, fmt.Errorf("query account orders: %w", err)
	}
	defer rows.Close()

	views := make([]query.OrderView, 0, limit+1)
	for rows.Next() {
		var (
			rowOrderID       domain.OrderID
			payload          []byte
			acceptedSequence int64
			updatedSequence  int64
			createdAt        time.Time
			updatedAt        time.Time
		)
		if err := rows.Scan(
			&rowOrderID,
			&payload,
			&acceptedSequence,
			&updatedSequence,
			&createdAt,
			&updatedAt,
		); err != nil {
			return query.OrderPage{}, fmt.Errorf("scan account order: %w", err)
		}
		view, err := r.orderView(
			payload,
			acceptedSequence,
			updatedSequence,
			createdAt,
			updatedAt,
			accountID,
		)
		if err != nil {
			return query.OrderPage{}, err
		}
		if view.Order.ID != rowOrderID {
			return query.OrderPage{}, fmt.Errorf("%w: order row identity mismatch", ErrIntegrity)
		}
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		return query.OrderPage{}, fmt.Errorf("iterate account orders: %w", err)
	}

	page := query.OrderPage{Orders: views}
	if len(views) > limit {
		page.Orders = views[:limit]
		last := page.Orders[len(page.Orders)-1].Order
		page.NextCursor = &query.OrderCursor{
			AcceptedSequence: last.AcceptedSequence,
			OrderID:          last.ID,
		}
	}
	return page, nil
}

func (r *Reader) ListAccountTrades(
	ctx context.Context,
	accountID domain.AccountID,
	filter query.TradeFilter,
	cursor *query.TradeCursor,
	limit int,
) (query.TradePage, error) {
	if accountID == "" {
		return query.TradePage{}, fmt.Errorf("%w: account is required", ErrInvalidQuery)
	}
	if err := validateLimit(limit); err != nil {
		return query.TradePage{}, err
	}
	side, err := normalizeSide(filter.Side)
	if err != nil {
		return query.TradePage{}, err
	}
	cursorSequence := int64(math.MaxInt64)
	cursorIndex := int32(math.MaxInt32)
	cursorTradeID := "\uffff"
	if cursor != nil {
		if cursor.Sequence == 0 || cursor.Sequence > math.MaxInt64 ||
			cursor.EventIndex == 0 || cursor.EventIndex > math.MaxInt32 ||
			cursor.TradeID == "" {
			return query.TradePage{}, fmt.Errorf("%w: invalid trade cursor", ErrInvalidQuery)
		}
		cursorSequence = int64(cursor.Sequence)
		cursorIndex = int32(cursor.EventIndex)
		cursorTradeID = string(cursor.TradeID)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT trade.trade_id, trade.payload, trade.sequence, trade.event_index, batch.created_at
		FROM trading_trade AS trade
		JOIN trading_event_batch AS batch
		  ON batch.market_id=trade.market_id AND batch.sequence=trade.sequence
		WHERE trade.market_id=$1
		  AND (trade.buyer_account_id=$2 OR trade.seller_account_id=$2)
		  AND (
		      $3::smallint=0
		      OR ($3::smallint=1 AND trade.buyer_account_id=$2)
		      OR ($3::smallint=2 AND trade.seller_account_id=$2)
		  )
		  AND (trade.sequence, trade.event_index, trade.trade_id) < ($4,$5,$6)
		ORDER BY trade.sequence DESC, trade.event_index DESC, trade.trade_id DESC
		LIMIT $7
	`, r.market.ID, accountID, int16(side), cursorSequence, cursorIndex,
		cursorTradeID, limit+1)
	if err != nil {
		return query.TradePage{}, fmt.Errorf("query account trades: %w", err)
	}
	defer rows.Close()

	trades := make([]query.AccountTrade, 0, limit+1)
	for rows.Next() {
		var (
			rowTradeID domain.TradeID
			payload    []byte
			sequence   int64
			eventIndex int32
			occurredAt time.Time
		)
		if err := rows.Scan(&rowTradeID, &payload, &sequence, &eventIndex, &occurredAt); err != nil {
			return query.TradePage{}, fmt.Errorf("scan account trade: %w", err)
		}
		var trade domain.Trade
		if err := json.Unmarshal(payload, &trade); err != nil {
			return query.TradePage{}, fmt.Errorf("%w: decode trade: %v", ErrIntegrity, err)
		}
		if trade.ID != rowTradeID {
			return query.TradePage{}, fmt.Errorf("%w: trade row identity mismatch", ErrIntegrity)
		}
		view, err := accountTradeView(
			trade,
			accountID,
			sequence,
			eventIndex,
			occurredAt,
			r.market,
		)
		if err != nil {
			return query.TradePage{}, err
		}
		trades = append(trades, view)
	}
	if err := rows.Err(); err != nil {
		return query.TradePage{}, fmt.Errorf("iterate account trades: %w", err)
	}

	page := query.TradePage{Trades: trades}
	if len(trades) > limit {
		page.Trades = trades[:limit]
		last := page.Trades[len(page.Trades)-1]
		page.NextCursor = &query.TradeCursor{
			Sequence:   last.Sequence,
			EventIndex: last.EventIndex,
			TradeID:    last.ID,
		}
	}
	return page, nil
}

func (r *Reader) ListOrderEvents(
	ctx context.Context,
	accountID domain.AccountID,
	orderID domain.OrderID,
	cursor *query.TimelineCursor,
	limit int,
) (query.OrderEventPage, error) {
	if accountID == "" || orderID == "" {
		return query.OrderEventPage{}, fmt.Errorf(
			"%w: account and order are required",
			ErrInvalidQuery,
		)
	}
	if err := validateLimit(limit); err != nil {
		return query.OrderEventPage{}, err
	}
	orderView, found, err := r.GetOrder(ctx, accountID, orderID)
	if err != nil {
		return query.OrderEventPage{}, err
	} else if !found {
		return query.OrderEventPage{}, nil
	}
	if err := r.requireTimelineCaughtUp(ctx); err != nil {
		return query.OrderEventPage{}, err
	}
	var cursorSequence int64
	var cursorEventIndex, cursorTimelineIndex int32
	if cursor != nil {
		if cursor.Sequence == 0 || cursor.Sequence > math.MaxInt64 ||
			cursor.EventIndex == 0 || cursor.EventIndex > math.MaxInt32 ||
			cursor.TimelineIndex > math.MaxInt32 {
			return query.OrderEventPage{}, fmt.Errorf("%w: invalid timeline cursor", ErrInvalidQuery)
		}
		cursorSequence = int64(cursor.Sequence)
		cursorEventIndex = int32(cursor.EventIndex)
		cursorTimelineIndex = int32(cursor.TimelineIndex)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT payload, sequence, event_index, timeline_index, occurred_at
		FROM trading_order_event
		WHERE market_id=$1 AND account_id=$2 AND order_id=$3
		  AND (sequence, event_index, timeline_index) > ($4,$5,$6)
		ORDER BY sequence ASC, event_index ASC, timeline_index ASC
		LIMIT $7
	`, r.market.ID, accountID, orderID, cursorSequence, cursorEventIndex,
		cursorTimelineIndex, limit+1)
	if err != nil {
		return query.OrderEventPage{}, fmt.Errorf("query order lifecycle: %w", err)
	}
	defer rows.Close()

	events := make([]query.OrderEvent, 0, limit+1)
	for rows.Next() {
		var (
			payload       []byte
			sequence      int64
			eventIndex    int32
			timelineIndex int32
			occurredAt    time.Time
		)
		if err := rows.Scan(
			&payload,
			&sequence,
			&eventIndex,
			&timelineIndex,
			&occurredAt,
		); err != nil {
			return query.OrderEventPage{}, fmt.Errorf("scan order lifecycle: %w", err)
		}
		var event query.OrderEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return query.OrderEventPage{}, fmt.Errorf(
				"%w: decode lifecycle event: %v",
				ErrIntegrity,
				err,
			)
		}
		if event.MarketID != r.market.ID || event.OrderID != orderID ||
			event.Sequence != uint64(sequence) || event.EventIndex != uint32(eventIndex) ||
			event.TimelineIndex != uint32(timelineIndex) ||
			event.EventID != eventID(event.Sequence, event.EventIndex, event.TimelineIndex) ||
			event.SourceKind != query.SourceKindEvent || !event.OccurredAt.Equal(occurredAt) {
			return query.OrderEventPage{}, fmt.Errorf(
				"%w: lifecycle event identity mismatch",
				ErrIntegrity,
			)
		}
		if err := r.validateTimelineEvent(event, orderView.Order); err != nil {
			return query.OrderEventPage{}, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return query.OrderEventPage{}, fmt.Errorf("iterate order lifecycle: %w", err)
	}
	page := query.OrderEventPage{Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		last := page.Events[len(page.Events)-1]
		page.NextCursor = &query.TimelineCursor{
			Sequence:      last.Sequence,
			EventIndex:    last.EventIndex,
			TimelineIndex: last.TimelineIndex,
		}
	}
	return page, nil
}

func (r *Reader) ListLedgerEntries(
	ctx context.Context,
	accountID domain.AccountID,
	filter query.LedgerFilter,
	cursor *query.LedgerCursor,
	limit int,
) (query.LedgerPage, error) {
	if accountID == "" {
		return query.LedgerPage{}, fmt.Errorf("%w: account is required", ErrInvalidQuery)
	}
	if err := validateLimit(limit); err != nil {
		return query.LedgerPage{}, err
	}
	asset, err := r.normalizeAsset(filter.Asset)
	if err != nil {
		return query.LedgerPage{}, err
	}
	reason, err := normalizeLedgerReason(filter.Reason)
	if err != nil {
		return query.LedgerPage{}, err
	}
	cursorSequence := int64(math.MaxInt64)
	cursorTransaction := "\uffff"
	cursorEntryIndex := int32(math.MaxInt32)
	if cursor != nil {
		if cursor.Sequence == 0 || cursor.Sequence > math.MaxInt64 ||
			cursor.TransactionID == "" || cursor.EntryIndex == 0 ||
			cursor.EntryIndex > math.MaxInt32 {
			return query.LedgerPage{}, fmt.Errorf("%w: invalid ledger cursor", ErrInvalidQuery)
		}
		cursorSequence = int64(cursor.Sequence)
		cursorTransaction = cursor.TransactionID
		cursorEntryIndex = int32(cursor.EntryIndex)
	}
	available := ledger.UserAvailable(accountID)
	held := ledger.UserHeld(accountID)
	rows, err := r.pool.Query(ctx, `
		WITH account_entries AS (
			SELECT entry.*,
			       batch.created_at,
			       CASE
			         WHEN transaction_id LIKE 'fund:%' AND reference LIKE 'virtual-funding:%'
			           THEN 'virtual_fund'
			         WHEN transaction_id LIKE 'hold:%' AND reference LIKE 'order-hold:%'
			           THEN 'order_hold'
			         WHEN (
			           (transaction_id LIKE 'release:%' AND reference LIKE 'order-release:%') OR
			           (transaction_id LIKE 'cancel-release:%' AND reference LIKE 'order-cancel:%') OR
			           (transaction_id LIKE 'maker-release:%' AND reference LIKE 'maker-rounding-release:%')
			         ) THEN 'order_release'
			         WHEN transaction_id LIKE 'trade:%' AND reference LIKE 'matched-trade:%'
			           THEN 'trade_settlement'
			         ELSE 'other'
			       END AS reason
			FROM trading_ledger_entry AS entry
			JOIN trading_event_batch AS batch
			  ON batch.market_id=entry.market_id AND batch.sequence=entry.sequence
			WHERE entry.market_id=$1 AND entry.account IN ($2,$3)
		)
		SELECT account_entries.sequence,
		       account_entries.transaction_id,
		       account_entries.entry_index,
		       account_entries.account,
		       account_entries.asset,
		       account_entries.amount,
		       account_entries.reference,
		       account_entries.reason,
		       account_entries.created_at,
		       trade.payload
		FROM account_entries
		LEFT JOIN trading_trade AS trade
		  ON trade.market_id=account_entries.market_id
		 AND account_entries.reference LIKE 'matched-trade:%'
		 AND trade.trade_id=substring(account_entries.reference FROM 15)
		WHERE ($4='' OR account_entries.asset=$4)
		  AND ($5='' OR account_entries.reason=$5)
		  AND (
		      account_entries.sequence,
		      account_entries.transaction_id,
		      account_entries.entry_index
		  ) < ($6,$7,$8)
		ORDER BY account_entries.sequence DESC,
		         account_entries.transaction_id DESC,
		         account_entries.entry_index DESC
		LIMIT $9
	`, r.market.ID, available, held, asset, reason, cursorSequence,
		cursorTransaction, cursorEntryIndex, limit+1)
	if err != nil {
		return query.LedgerPage{}, fmt.Errorf("query account ledger: %w", err)
	}
	defer rows.Close()

	entries := make([]query.LedgerEntry, 0, limit+1)
	for rows.Next() {
		var (
			sequence      int64
			transactionID string
			entryIndex    int32
			account       string
			entryAsset    domain.Asset
			amount        int64
			reference     string
			storedReason  string
			occurredAt    time.Time
			tradePayload  []byte
		)
		if err := rows.Scan(
			&sequence,
			&transactionID,
			&entryIndex,
			&account,
			&entryAsset,
			&amount,
			&reference,
			&storedReason,
			&occurredAt,
			&tradePayload,
		); err != nil {
			return query.LedgerPage{}, fmt.Errorf("scan account ledger: %w", err)
		}
		entry, err := r.ledgerEntry(
			accountID,
			available,
			held,
			sequence,
			transactionID,
			entryIndex,
			account,
			entryAsset,
			amount,
			reference,
			storedReason,
			occurredAt,
			tradePayload,
		)
		if err != nil {
			return query.LedgerPage{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return query.LedgerPage{}, fmt.Errorf("iterate account ledger: %w", err)
	}
	page := query.LedgerPage{Entries: entries}
	if len(entries) > limit {
		page.Entries = entries[:limit]
		last := page.Entries[len(page.Entries)-1]
		page.NextCursor = &query.LedgerCursor{
			Sequence:      last.Sequence,
			TransactionID: last.TransactionID,
			EntryIndex:    last.EntryIndex,
		}
	}
	return page, nil
}

func (r *Reader) orderView(
	payload []byte,
	acceptedSequence int64,
	updatedSequence int64,
	createdAt time.Time,
	updatedAt time.Time,
	accountID domain.AccountID,
) (query.OrderView, error) {
	var order domain.Order
	if err := json.Unmarshal(payload, &order); err != nil {
		return query.OrderView{}, fmt.Errorf("%w: decode order: %v", ErrIntegrity, err)
	}
	if order.ID == "" || order.MarketID != r.market.ID || order.AccountID != accountID ||
		acceptedSequence <= 0 || updatedSequence < acceptedSequence ||
		order.AcceptedSequence != uint64(acceptedSequence) ||
		order.LastSequence != uint64(updatedSequence) || createdAt.IsZero() ||
		updatedAt.Before(createdAt) {
		return query.OrderView{}, fmt.Errorf("%w: order identity or time mismatch", ErrIntegrity)
	}
	switch order.Status {
	case domain.OrderStatusRejected,
		domain.OrderStatusOpen,
		domain.OrderStatusPartiallyFilled,
		domain.OrderStatusFilled,
		domain.OrderStatusCanceled:
	default:
		return query.OrderView{}, fmt.Errorf("%w: non-queryable order status", ErrIntegrity)
	}
	var average *int64
	if order.FilledQuantity > 0 {
		value, err := domain.CheckedMulDivFloor(
			order.SpentQuote,
			r.market.BaseScale,
			order.FilledQuantity,
		)
		if err != nil {
			return query.OrderView{}, fmt.Errorf("%w: average fill price: %v", ErrIntegrity, err)
		}
		average = &value
	} else if order.SpentQuote != 0 {
		return query.OrderView{}, fmt.Errorf("%w: zero-filled order spent quote", ErrIntegrity)
	}
	return query.OrderView{
		Order:            order,
		AverageFillPrice: average,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}, nil
}

func accountTradeView(
	trade domain.Trade,
	accountID domain.AccountID,
	sequence int64,
	eventIndex int32,
	occurredAt time.Time,
	market domain.Market,
) (query.AccountTrade, error) {
	if trade.MarketID != market.ID || trade.ID == "" || trade.Price <= 0 ||
		trade.Quantity <= 0 || trade.QuoteAmount <= 0 || trade.MakerOrderID == "" ||
		trade.TakerOrderID == "" || trade.MakerAccountID == "" || trade.TakerAccountID == "" ||
		trade.BuyerAccountID == "" || trade.SellerAccountID == "" || sequence <= 0 ||
		eventIndex <= 0 || occurredAt.IsZero() {
		return query.AccountTrade{}, fmt.Errorf("%w: trade identity mismatch", ErrIntegrity)
	}
	var (
		orderID domain.OrderID
		role    domain.LiquidityRole
	)
	isMaker := trade.MakerAccountID == accountID
	isTaker := trade.TakerAccountID == accountID
	if isMaker == isTaker {
		return query.AccountTrade{}, fmt.Errorf("%w: ambiguous trade role", ErrIntegrity)
	}
	if isMaker {
		orderID = trade.MakerOrderID
		role = domain.LiquidityRoleMaker
	} else {
		orderID = trade.TakerOrderID
		role = domain.LiquidityRoleTaker
	}
	var (
		side domain.Side
		fee  domain.Fee
	)
	isBuyer := trade.BuyerAccountID == accountID
	isSeller := trade.SellerAccountID == accountID
	if isBuyer == isSeller {
		return query.AccountTrade{}, fmt.Errorf("%w: ambiguous trade side", ErrIntegrity)
	}
	if isBuyer {
		side = domain.SideBuy
		fee = trade.BuyerFee
	} else {
		side = domain.SideSell
		fee = trade.SellerFee
	}
	if fee.AccountID != accountID || fee.Role != role || fee.Asset == "" || fee.Amount < 0 {
		return query.AccountTrade{}, fmt.Errorf("%w: trade fee identity mismatch", ErrIntegrity)
	}
	expectedRate := market.TakerFeeBPS
	if role == domain.LiquidityRoleMaker {
		expectedRate = market.MakerFeeBPS
	}
	expectedAsset := market.QuoteAsset
	feeBasis := trade.QuoteAmount
	if side == domain.SideBuy {
		expectedAsset = market.BaseAsset
		feeBasis = trade.Quantity
	}
	expectedAmount, err := domain.FeeAmount(feeBasis, expectedRate)
	if err != nil || fee.Asset != expectedAsset || fee.RateBPS != expectedRate ||
		fee.Amount != expectedAmount {
		return query.AccountTrade{}, fmt.Errorf("%w: trade fee formula mismatch", ErrIntegrity)
	}
	return query.AccountTrade{
		ID:            trade.ID,
		MarketID:      market.ID,
		OrderID:       orderID,
		Side:          side,
		LiquidityRole: role,
		Price:         trade.Price,
		Quantity:      trade.Quantity,
		QuoteAmount:   trade.QuoteAmount,
		FeeAsset:      fee.Asset,
		FeeAmount:     fee.Amount,
		FeeRateBPS:    fee.RateBPS,
		Sequence:      uint64(sequence),
		EventIndex:    uint32(eventIndex),
		OccurredAt:    occurredAt,
	}, nil
}

func (r *Reader) requireTimelineCaughtUp(ctx context.Context) error {
	var current int64
	var checkpoint *int64
	if err := r.pool.QueryRow(ctx, `
		SELECT market.current_sequence, lifecycle.sequence
		FROM trading_market AS market
		LEFT JOIN trading_order_event_checkpoint AS lifecycle
		  ON lifecycle.market_id=market.market_id
		WHERE market.market_id=$1
	`, r.market.ID).Scan(&current, &checkpoint); err != nil {
		return fmt.Errorf("read lifecycle checkpoint: %w", err)
	}
	if checkpoint == nil || *checkpoint != current {
		return fmt.Errorf(
			"%w: lifecycle checkpoint is not at event head",
			ErrIntegrity,
		)
	}
	return nil
}

func (r *Reader) validateTimelineEvent(
	event query.OrderEvent,
	order domain.Order,
) error {
	switch event.Type {
	case domain.EventOrderAccepted,
		domain.EventOrderRejected,
		domain.EventOrderRested,
		domain.EventTradeExecuted,
		domain.EventOrderFilled,
		domain.EventOrderCanceled,
		domain.EventCancelRejected,
		domain.EventSelfTradePrevented:
	default:
		return fmt.Errorf("%w: unsupported lifecycle type", ErrIntegrity)
	}
	if event.Status < domain.OrderStatusReceived || event.Status > domain.OrderStatusCanceled {
		return fmt.Errorf("%w: invalid lifecycle status", ErrIntegrity)
	}
	if event.Quantity != nil && *event.Quantity <= 0 {
		return fmt.Errorf("%w: invalid lifecycle quantity", ErrIntegrity)
	}
	if event.Price != nil && *event.Price <= 0 {
		return fmt.Errorf("%w: invalid lifecycle price", ErrIntegrity)
	}
	marketBuy := order.Type == domain.OrderTypeMarket && order.Side == domain.SideBuy
	if marketBuy {
		if event.RemainingQuantity != nil || event.RemainingQuoteBudget == nil ||
			*event.RemainingQuoteBudget < 0 {
			return fmt.Errorf("%w: invalid Market Buy lifecycle remainder", ErrIntegrity)
		}
	} else if event.RemainingQuantity == nil || *event.RemainingQuantity < 0 ||
		event.RemainingQuoteBudget != nil {
		return fmt.Errorf("%w: invalid lifecycle remainder", ErrIntegrity)
	}
	if event.Fee != nil {
		if (event.Fee.Asset != r.market.BaseAsset && event.Fee.Asset != r.market.QuoteAsset) ||
			event.Fee.Amount < 0 || event.Fee.RateBPS < 0 ||
			(event.Fee.Role != domain.LiquidityRoleMaker &&
				event.Fee.Role != domain.LiquidityRoleTaker) {
			return fmt.Errorf("%w: invalid lifecycle fee", ErrIntegrity)
		}
		expectedRate := r.market.TakerFeeBPS
		if event.Fee.Role == domain.LiquidityRoleMaker {
			expectedRate = r.market.MakerFeeBPS
		}
		if event.Quantity == nil || event.Price == nil || event.Type != domain.EventTradeExecuted {
			return fmt.Errorf("%w: lifecycle fee lacks trade basis", ErrIntegrity)
		}
		expectedAsset := r.market.QuoteAsset
		feeBasis, err := r.market.QuoteAmountFloor(*event.Price, *event.Quantity)
		if order.Side == domain.SideBuy {
			expectedAsset = r.market.BaseAsset
			feeBasis = *event.Quantity
		}
		if err != nil {
			return fmt.Errorf("%w: lifecycle fee basis: %v", ErrIntegrity, err)
		}
		expectedAmount, err := domain.FeeAmount(feeBasis, expectedRate)
		if err != nil || event.Fee.Asset != expectedAsset ||
			event.Fee.RateBPS != expectedRate || event.Fee.Amount != expectedAmount {
			return fmt.Errorf("%w: lifecycle fee formula mismatch", ErrIntegrity)
		}
	}
	for _, effect := range event.BalanceEffects {
		if (effect.Asset != r.market.BaseAsset && effect.Asset != r.market.QuoteAsset) ||
			effect.Amount == 0 || effect.TransactionID == "" ||
			(effect.Bucket != query.BalanceBucketAvailable &&
				effect.Bucket != query.BalanceBucketHeld) {
			return fmt.Errorf("%w: invalid lifecycle balance effect", ErrIntegrity)
		}
		switch effect.Reason {
		case query.LedgerReasonOrderHold,
			query.LedgerReasonOrderRelease,
			query.LedgerReasonTradeSettlement:
		default:
			return fmt.Errorf("%w: invalid lifecycle balance reason", ErrIntegrity)
		}
	}
	return nil
}

func (r *Reader) ledgerEntry(
	accountID domain.AccountID,
	available string,
	held string,
	sequence int64,
	transactionID string,
	entryIndex int32,
	account string,
	asset domain.Asset,
	amount int64,
	reference string,
	storedReason string,
	occurredAt time.Time,
	tradePayload []byte,
) (query.LedgerEntry, error) {
	if sequence <= 0 || transactionID == "" || entryIndex <= 0 || amount == 0 ||
		(asset != r.market.BaseAsset && asset != r.market.QuoteAsset) || occurredAt.IsZero() {
		return query.LedgerEntry{}, fmt.Errorf("%w: invalid ledger row", ErrIntegrity)
	}
	var bucket query.BalanceBucket
	switch account {
	case available:
		bucket = query.BalanceBucketAvailable
	case held:
		bucket = query.BalanceBucketHeld
	default:
		return query.LedgerEntry{}, fmt.Errorf("%w: ledger account scope mismatch", ErrIntegrity)
	}
	reason := sharedreadmodel.ClassifyLedgerReason(transactionID, reference)
	if storedReason != string(reason) {
		return query.LedgerEntry{}, fmt.Errorf("%w: ledger reason mismatch", ErrIntegrity)
	}
	orderID := sharedreadmodel.OrderIDFromReference(reference)
	tradeID := sharedreadmodel.TradeIDFromReference(reference)
	if (reason == query.LedgerReasonOrderHold || reason == query.LedgerReasonOrderRelease) &&
		orderID == "" {
		return query.LedgerEntry{}, fmt.Errorf("%w: order ledger link is missing", ErrIntegrity)
	}
	if reason == query.LedgerReasonTradeSettlement && tradeID == "" {
		return query.LedgerEntry{}, fmt.Errorf("%w: trade ledger reference is missing", ErrIntegrity)
	}
	if tradeID != "" {
		if len(tradePayload) == 0 {
			return query.LedgerEntry{}, fmt.Errorf("%w: trade ledger link is missing", ErrIntegrity)
		}
		var trade domain.Trade
		if err := json.Unmarshal(tradePayload, &trade); err != nil {
			return query.LedgerEntry{}, fmt.Errorf("%w: decode linked trade: %v", ErrIntegrity, err)
		}
		view, err := accountTradeView(trade, accountID, sequence, 1, occurredAt, r.market)
		if err != nil {
			return query.LedgerEntry{}, err
		}
		orderID = view.OrderID
	}
	return query.LedgerEntry{
		EntryID:       fmt.Sprintf("%d:%s:%d", sequence, transactionID, entryIndex),
		MarketID:      r.market.ID,
		Sequence:      uint64(sequence),
		TransactionID: transactionID,
		EntryIndex:    uint32(entryIndex),
		Asset:         asset,
		Bucket:        bucket,
		Amount:        amount,
		Reason:        reason,
		Reference:     reference,
		OrderID:       orderID,
		TradeID:       tradeID,
		OccurredAt:    occurredAt,
	}, nil
}

func validateLimit(limit int) error {
	if limit < 1 || limit > 100 {
		return fmt.Errorf("%w: limit must be in [1,100]", ErrInvalidQuery)
	}
	return nil
}

func normalizeOrderScope(scope query.OrderScope) (string, error) {
	if scope == "" {
		return string(query.OrderScopeAll), nil
	}
	switch scope {
	case query.OrderScopeAll, query.OrderScopeOpen, query.OrderScopeHistory:
		return string(scope), nil
	default:
		return "", fmt.Errorf("%w: unsupported order scope", ErrInvalidQuery)
	}
}

func normalizeOrderStatus(status domain.OrderStatus) (string, error) {
	if status == 0 {
		return "", nil
	}
	switch status {
	case domain.OrderStatusRejected,
		domain.OrderStatusOpen,
		domain.OrderStatusPartiallyFilled,
		domain.OrderStatusFilled,
		domain.OrderStatusCanceled:
		return status.String(), nil
	default:
		return "", fmt.Errorf("%w: unsupported order status", ErrInvalidQuery)
	}
}

func normalizeSide(side domain.Side) (domain.Side, error) {
	if side == 0 || side == domain.SideBuy || side == domain.SideSell {
		return side, nil
	}
	return 0, fmt.Errorf("%w: unsupported side", ErrInvalidQuery)
}

func normalizeOrderType(orderType domain.OrderType) (domain.OrderType, error) {
	if orderType == 0 || orderType == domain.OrderTypeLimit ||
		orderType == domain.OrderTypeMarket {
		return orderType, nil
	}
	return 0, fmt.Errorf("%w: unsupported order type", ErrInvalidQuery)
}

func (r *Reader) normalizeAsset(asset domain.Asset) (string, error) {
	if asset == "" {
		return "", nil
	}
	if asset != r.market.BaseAsset && asset != r.market.QuoteAsset {
		return "", fmt.Errorf("%w: unsupported ledger asset", ErrInvalidQuery)
	}
	return string(asset), nil
}

func normalizeLedgerReason(reason query.LedgerReason) (string, error) {
	if reason == "" {
		return "", nil
	}
	switch reason {
	case query.LedgerReasonVirtualFund,
		query.LedgerReasonOrderHold,
		query.LedgerReasonOrderRelease,
		query.LedgerReasonTradeSettlement,
		query.LedgerReasonOther:
		return string(reason), nil
	default:
		return "", fmt.Errorf("%w: unsupported ledger reason", ErrInvalidQuery)
	}
}

func eventID(sequence uint64, eventIndex, timelineIndex uint32) string {
	return fmt.Sprintf("%d:%d:%d", sequence, eventIndex, timelineIndex)
}
