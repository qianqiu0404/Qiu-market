package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/query"
	corestore "github.com/the-web3/s78-market-services/trading/store"
)

type lifecycleRow struct {
	AccountID domain.AccountID
	Event     query.OrderEvent
}

type journalBinding struct {
	transactions map[string]ledger.Transaction
	bound        map[string]bool
}

func (s *Store) ensureTradeV1Projections(ctx context.Context) error {
	var current int64
	if err := s.pool.QueryRow(ctx, `
		SELECT current_sequence FROM trading_market WHERE market_id=$1
	`, s.market.ID).Scan(&current); err != nil {
		return fmt.Errorf("read stream for Trade V1 projection: %w", err)
	}
	if current == 0 {
		return nil
	}
	var checkpoint int64
	err := s.pool.QueryRow(ctx, `
		SELECT sequence
		FROM trading_order_event_checkpoint
		WHERE market_id=$1
	`, s.market.ID).Scan(&checkpoint)
	if err == nil && checkpoint == current {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read order-event checkpoint: %w", err)
	}
	if checkpoint > current {
		return fmt.Errorf(
			"order-event checkpoint %d is ahead of event stream %d",
			checkpoint,
			current,
		)
	}
	if err := s.RebuildProjections(ctx); err != nil {
		return fmt.Errorf(
			"rebuild Trade V1 projections from event authority at %d/%d: %w",
			checkpoint,
			current,
			err,
		)
	}
	return nil
}

func applyOrderLifecycleProjection(
	ctx context.Context,
	tx pgx.Tx,
	record corestore.Record,
	occurredAt time.Time,
) error {
	if record.Command.Sequence == 0 || record.Command.Sequence > math.MaxInt64 {
		return fmt.Errorf("invalid lifecycle sequence %d", record.Command.Sequence)
	}
	var checkpoint int64
	err := tx.QueryRow(ctx, `
		SELECT sequence
		FROM trading_order_event_checkpoint
		WHERE market_id=$1
		FOR UPDATE
	`, record.MarketID).Scan(&checkpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		checkpoint = 0
	} else if err != nil {
		return fmt.Errorf("lock order-event checkpoint: %w", err)
	}
	expected := int64(record.Command.Sequence - 1)
	if checkpoint != expected {
		return fmt.Errorf(
			"order lifecycle projection is not continuous: checkpoint=%d expected=%d",
			checkpoint,
			expected,
		)
	}

	previous, err := loadPreviousOrders(ctx, tx, record)
	if err != nil {
		return err
	}
	rows, err := buildLifecycleRows(record, previous, occurredAt)
	if err != nil {
		return fmt.Errorf("build order lifecycle at sequence %d: %w", record.Command.Sequence, err)
	}
	for _, row := range rows {
		payload, err := json.Marshal(row.Event)
		if err != nil {
			return fmt.Errorf("marshal lifecycle event %s: %w", row.Event.EventID, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading_order_event (
				market_id, account_id, order_id, sequence, event_index,
				timeline_index, event_type, payload, occurred_at
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
		`, record.MarketID, row.AccountID, row.Event.OrderID,
			int64(row.Event.Sequence), int32(row.Event.EventIndex),
			int32(row.Event.TimelineIndex), row.Event.Type, string(payload),
			occurredAt); err != nil {
			return fmt.Errorf("insert lifecycle event %s: %w", row.Event.EventID, err)
		}
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO trading_order_event_checkpoint (market_id, sequence, updated_at)
		VALUES ($1,$2,$3)
		ON CONFLICT (market_id) DO UPDATE
		SET sequence=EXCLUDED.sequence,
		    updated_at=EXCLUDED.updated_at
		WHERE trading_order_event_checkpoint.sequence=$4
	`, record.MarketID, int64(record.Command.Sequence), occurredAt, expected)
	if err != nil {
		return fmt.Errorf("advance order-event checkpoint: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("order-event checkpoint compare-and-set failed")
	}
	return nil
}

func loadPreviousOrders(
	ctx context.Context,
	tx pgx.Tx,
	record corestore.Record,
) (map[domain.OrderID]domain.Order, error) {
	ids := make(map[domain.OrderID]struct{}, len(record.Projection.Orders)+1)
	for _, order := range record.Projection.Orders {
		ids[order.ID] = struct{}{}
	}
	if record.Command.Cancel != nil {
		ids[record.Command.Cancel.OrderID] = struct{}{}
	}
	previous := make(map[domain.OrderID]domain.Order, len(ids))
	for orderID := range ids {
		var payload []byte
		err := tx.QueryRow(ctx, `
			SELECT payload
			FROM trading_order
			WHERE market_id=$1 AND order_id=$2
		`, record.MarketID, orderID).Scan(&payload)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load prior order %s: %w", orderID, err)
		}
		var order domain.Order
		if err := json.Unmarshal(payload, &order); err != nil {
			return nil, fmt.Errorf("decode prior order %s: %w", orderID, err)
		}
		if order.ID != orderID || order.MarketID != record.MarketID {
			return nil, fmt.Errorf("prior order %s identity mismatch", orderID)
		}
		previous[orderID] = order
	}
	return previous, nil
}

func buildLifecycleRows(
	record corestore.Record,
	previous map[domain.OrderID]domain.Order,
	occurredAt time.Time,
) ([]lifecycleRow, error) {
	if occurredAt.IsZero() {
		return nil, fmt.Errorf("event batch time is required")
	}
	finalOrders := make(map[domain.OrderID]domain.Order, len(record.Projection.Orders))
	states := make(map[domain.OrderID]domain.Order, len(previous)+1)
	for orderID, order := range previous {
		states[orderID] = order
	}
	for _, order := range record.Projection.Orders {
		finalOrders[order.ID] = order
	}
	if record.Command.Submit != nil {
		orderID := record.Result.OrderID
		final, ok := finalOrders[orderID]
		if !ok || orderID == "" {
			return nil, fmt.Errorf("submit result has no final order projection")
		}
		initial := final
		initial.Status = domain.OrderStatusReceived
		initial.RemainingQuantity = initial.OriginalQuantity
		initial.FilledQuantity = 0
		initial.RemainingQuoteBudget = initial.OriginalQuoteBudget
		initial.SpentQuote = 0
		initial.HeldAsset = ""
		initial.HeldAmount = 0
		initial.RejectReason = ""
		states[orderID] = initial
	}

	bindings, err := newJournalBinding(record.Journal)
	if err != nil {
		return nil, err
	}
	if record.Command.Fund != nil {
		fundID := fmt.Sprintf("fund:%020d", record.Command.Sequence)
		fundRef := "virtual-funding:" + record.Command.Fund.RequestID
		if _, err := bindings.take(fundID, fundRef, true); err != nil {
			return nil, err
		}
	}

	rows := make([]lifecycleRow, 0, len(record.Result.Events)+2)
	tradeOrdinal := 0
	for _, event := range record.Result.Events {
		switch event.Type {
		case domain.EventAccountFunded:
			continue
		case domain.EventTradeExecuted:
			tradeOrdinal++
			tradeRows, err := buildTradeRows(
				record,
				event,
				tradeOrdinal,
				states,
				bindings,
				occurredAt,
			)
			if err != nil {
				return nil, err
			}
			rows = append(rows, tradeRows...)
		default:
			row, include, err := buildSingleOrderRow(
				record,
				event,
				states,
				bindings,
				occurredAt,
			)
			if err != nil {
				return nil, err
			}
			if include {
				rows = append(rows, row)
			}
		}
	}
	if err := bindings.assertAllBound(); err != nil {
		return nil, err
	}
	for orderID, final := range finalOrders {
		state, ok := states[orderID]
		if !ok {
			return nil, fmt.Errorf("final order %s was not reconstructed", orderID)
		}
		if state.AccountID != final.AccountID || state.MarketID != final.MarketID ||
			state.AcceptedSequence != final.AcceptedSequence || state.Status != final.Status ||
			state.OriginalQuantity != final.OriginalQuantity ||
			state.RemainingQuantity != final.RemainingQuantity ||
			state.FilledQuantity != final.FilledQuantity ||
			state.OriginalQuoteBudget != final.OriginalQuoteBudget ||
			state.RemainingQuoteBudget != final.RemainingQuoteBudget ||
			state.SpentQuote != final.SpentQuote {
			return nil, fmt.Errorf("reconstructed order %s differs from final projection", orderID)
		}
	}
	return rows, nil
}

func buildTradeRows(
	record corestore.Record,
	event domain.Event,
	tradeOrdinal int,
	states map[domain.OrderID]domain.Order,
	bindings *journalBinding,
	occurredAt time.Time,
) ([]lifecycleRow, error) {
	if event.Trade == nil || event.Trade.ID == "" {
		return nil, fmt.Errorf("trade event %d has no trade", event.Index)
	}
	trade := *event.Trade
	if event.OrderID != trade.TakerOrderID {
		return nil, fmt.Errorf("trade %s taker order mismatch", trade.ID)
	}
	taker, ok := states[trade.TakerOrderID]
	if !ok {
		return nil, fmt.Errorf("trade %s taker order is missing", trade.ID)
	}
	maker, ok := states[trade.MakerOrderID]
	if !ok {
		return nil, fmt.Errorf("trade %s maker order is missing", trade.ID)
	}
	if err := applyTimelineFill(&taker, trade); err != nil {
		return nil, fmt.Errorf("apply taker fill %s: %w", trade.ID, err)
	}
	if err := applyTimelineFill(&maker, trade); err != nil {
		return nil, fmt.Errorf("apply maker fill %s: %w", trade.ID, err)
	}
	if event.RemainingQuoteBudget != nil &&
		taker.RemainingQuoteBudget != *event.RemainingQuoteBudget {
		return nil, fmt.Errorf("trade %s remaining quote budget mismatch", trade.ID)
	}
	if !(taker.Type == domain.OrderTypeMarket && taker.Side == domain.SideBuy) &&
		event.Remaining != taker.RemainingQuantity {
		return nil, fmt.Errorf("trade %s taker remaining quantity mismatch", trade.ID)
	}
	states[taker.ID] = taker
	states[maker.ID] = maker

	settlement, err := bindings.take(
		"trade:"+string(trade.ID),
		"matched-trade:"+string(trade.ID),
		true,
	)
	if err != nil {
		return nil, err
	}
	makerReleaseID := fmt.Sprintf(
		"maker-release:%020d:%04d",
		record.Command.Sequence,
		tradeOrdinal,
	)
	makerRelease, err := bindings.take(
		makerReleaseID,
		"maker-rounding-release:"+string(maker.ID),
		false,
	)
	if err != nil {
		return nil, err
	}

	rows := make([]lifecycleRow, 0, 2)
	for _, view := range []struct {
		order         domain.Order
		timelineIndex uint32
	}{
		{order: taker, timelineIndex: 0},
		{order: maker, timelineIndex: 1},
	} {
		fee, err := feeForAccount(trade, view.order.AccountID)
		if err != nil {
			return nil, err
		}
		effects, err := effectsFor(
			view.order.AccountID,
			settlement,
			query.LedgerReasonTradeSettlement,
		)
		if err != nil {
			return nil, err
		}
		if makerRelease != nil && view.order.ID == maker.ID {
			releaseEffects, err := effectsFor(
				view.order.AccountID,
				makerRelease,
				query.LedgerReasonOrderRelease,
			)
			if err != nil {
				return nil, err
			}
			effects = append(effects, releaseEffects...)
		}
		price := trade.Price
		quantity := trade.Quantity
		row := lifecycleRow{
			AccountID: view.order.AccountID,
			Event: query.OrderEvent{
				EventID:        timelineEventID(event.Sequence, event.Index, view.timelineIndex),
				MarketID:       record.MarketID,
				OrderID:        view.order.ID,
				Sequence:       event.Sequence,
				EventIndex:     event.Index,
				TimelineIndex:  view.timelineIndex,
				SourceKind:     query.SourceKindEvent,
				Type:           event.Type,
				Status:         view.order.Status,
				Quantity:       &quantity,
				Price:          &price,
				TradeID:        trade.ID,
				Fee:            fee,
				BalanceEffects: effects,
				OccurredAt:     occurredAt,
			},
		}
		setTimelineRemaining(&row.Event, view.order)
		rows = append(rows, row)
	}
	return rows, nil
}

func buildSingleOrderRow(
	record corestore.Record,
	event domain.Event,
	states map[domain.OrderID]domain.Order,
	bindings *journalBinding,
	occurredAt time.Time,
) (lifecycleRow, bool, error) {
	order, ok := states[event.OrderID]
	if !ok {
		// A not-found or owner-mismatch cancel is authoritative for the caller,
		// but cannot be attached to somebody else's order lifecycle.
		if event.Type == domain.EventCancelRejected {
			return lifecycleRow{}, false, nil
		}
		return lifecycleRow{}, false, fmt.Errorf(
			"event %s references missing order %s",
			event.Type,
			event.OrderID,
		)
	}
	if event.Type == domain.EventCancelRejected && event.AccountID != order.AccountID {
		return lifecycleRow{}, false, nil
	}

	var effects []query.BalanceEffect
	switch event.Type {
	case domain.EventOrderAccepted:
		order.Status = domain.OrderStatusReceived
		hold, err := bindings.take(
			fmt.Sprintf("hold:%020d", record.Command.Sequence),
			"order-hold:"+string(order.ID),
			true,
		)
		if err != nil {
			return lifecycleRow{}, false, err
		}
		effects, err = effectsFor(order.AccountID, hold, query.LedgerReasonOrderHold)
		if err != nil {
			return lifecycleRow{}, false, err
		}
	case domain.EventOrderRejected:
		order.Status = domain.OrderStatusRejected
		order.RejectReason = event.Reason
	case domain.EventOrderRested:
		order.Status = event.Status
		release, err := bindings.take(
			fmt.Sprintf("release:%020d", record.Command.Sequence),
			"order-release:"+string(order.ID),
			false,
		)
		if err != nil {
			return lifecycleRow{}, false, err
		}
		if release != nil {
			effects, err = effectsFor(order.AccountID, release, query.LedgerReasonOrderRelease)
			if err != nil {
				return lifecycleRow{}, false, err
			}
		}
	case domain.EventOrderFilled:
		order.Status = domain.OrderStatusFilled
		release, err := bindings.take(
			fmt.Sprintf("release:%020d", record.Command.Sequence),
			"order-release:"+string(order.ID),
			false,
		)
		if err != nil {
			return lifecycleRow{}, false, err
		}
		if release != nil {
			effects, err = effectsFor(order.AccountID, release, query.LedgerReasonOrderRelease)
			if err != nil {
				return lifecycleRow{}, false, err
			}
		}
	case domain.EventOrderCanceled:
		order.Status = domain.OrderStatusCanceled
		var release *ledger.Transaction
		var err error
		if event.Reason == "user_requested" {
			release, err = bindings.take(
				fmt.Sprintf("cancel-release:%020d", record.Command.Sequence),
				"order-cancel:"+string(order.ID),
				true,
			)
		} else {
			release, err = bindings.take(
				fmt.Sprintf("release:%020d", record.Command.Sequence),
				"order-release:"+string(order.ID),
				false,
			)
		}
		if err != nil {
			return lifecycleRow{}, false, err
		}
		if release != nil {
			effects, err = effectsFor(order.AccountID, release, query.LedgerReasonOrderRelease)
			if err != nil {
				return lifecycleRow{}, false, err
			}
		}
	case domain.EventSelfTradePrevented:
		order.Status = domain.OrderStatusCanceled
	case domain.EventCancelRejected:
		// The order stays in its authoritative state; only this attempt failed.
	default:
		return lifecycleRow{}, false, fmt.Errorf("unsupported lifecycle event type %s", event.Type)
	}
	states[order.ID] = order

	rowStatus := order.Status
	if event.Type == domain.EventCancelRejected {
		rowStatus = domain.OrderStatusRejected
	}
	row := lifecycleRow{
		AccountID: order.AccountID,
		Event: query.OrderEvent{
			EventID:        timelineEventID(event.Sequence, event.Index, 0),
			MarketID:       record.MarketID,
			OrderID:        order.ID,
			Sequence:       event.Sequence,
			EventIndex:     event.Index,
			TimelineIndex:  0,
			SourceKind:     query.SourceKindEvent,
			Type:           event.Type,
			Status:         rowStatus,
			BalanceEffects: effects,
			Reason:         event.Reason,
			OccurredAt:     occurredAt,
		},
	}
	if order.Price > 0 {
		price := order.Price
		row.Event.Price = &price
	}
	if order.OriginalQuantity > 0 {
		quantity := order.OriginalQuantity
		row.Event.Quantity = &quantity
	}
	setTimelineRemaining(&row.Event, order)
	return row, true, nil
}

func applyTimelineFill(order *domain.Order, trade domain.Trade) error {
	if order == nil || trade.Quantity <= 0 || trade.QuoteAmount <= 0 {
		return fmt.Errorf("order and positive trade amounts are required")
	}
	nextFilled, err := domain.CheckedAdd(order.FilledQuantity, trade.Quantity)
	if err != nil {
		return err
	}
	nextSpent, err := domain.CheckedAdd(order.SpentQuote, trade.QuoteAmount)
	if err != nil {
		return err
	}
	order.FilledQuantity = nextFilled
	order.SpentQuote = nextSpent
	if order.Type == domain.OrderTypeMarket && order.Side == domain.SideBuy {
		if order.RemainingQuoteBudget < trade.QuoteAmount {
			return fmt.Errorf("remaining quote budget became negative")
		}
		order.RemainingQuoteBudget -= trade.QuoteAmount
		order.Status = domain.OrderStatusPartiallyFilled
		return nil
	}
	if order.RemainingQuantity < trade.Quantity {
		return fmt.Errorf("remaining quantity became negative")
	}
	order.RemainingQuantity -= trade.Quantity
	if order.RemainingQuantity == 0 {
		order.Status = domain.OrderStatusFilled
	} else {
		order.Status = domain.OrderStatusPartiallyFilled
	}
	return nil
}

func setTimelineRemaining(event *query.OrderEvent, order domain.Order) {
	if order.Type == domain.OrderTypeMarket && order.Side == domain.SideBuy {
		remaining := order.RemainingQuoteBudget
		event.RemainingQuoteBudget = &remaining
		event.RemainingQuantity = nil
		return
	}
	remaining := order.RemainingQuantity
	event.RemainingQuantity = &remaining
	event.RemainingQuoteBudget = nil
}

func feeForAccount(trade domain.Trade, accountID domain.AccountID) (*query.FeeView, error) {
	var fee domain.Fee
	switch accountID {
	case trade.BuyerAccountID:
		fee = trade.BuyerFee
	case trade.SellerAccountID:
		fee = trade.SellerFee
	default:
		return nil, fmt.Errorf("trade %s does not belong to account %s", trade.ID, accountID)
	}
	if fee.AccountID != accountID || fee.Asset == "" || fee.Amount < 0 ||
		(fee.Role != domain.LiquidityRoleMaker && fee.Role != domain.LiquidityRoleTaker) {
		return nil, fmt.Errorf("trade %s has invalid account fee", trade.ID)
	}
	return &query.FeeView{
		Asset:   fee.Asset,
		Amount:  fee.Amount,
		RateBPS: fee.RateBPS,
		Role:    fee.Role,
	}, nil
}

func effectsFor(
	accountID domain.AccountID,
	transaction *ledger.Transaction,
	reason query.LedgerReason,
) ([]query.BalanceEffect, error) {
	if transaction == nil {
		return nil, nil
	}
	available := ledger.UserAvailable(accountID)
	held := ledger.UserHeld(accountID)
	effects := make([]query.BalanceEffect, 0, 2)
	for _, entry := range transaction.Entries {
		var bucket query.BalanceBucket
		switch entry.Account {
		case available:
			bucket = query.BalanceBucketAvailable
		case held:
			bucket = query.BalanceBucketHeld
		default:
			continue
		}
		effects = append(effects, query.BalanceEffect{
			Asset:         entry.Asset,
			Bucket:        bucket,
			Amount:        entry.Amount,
			Reason:        reason,
			TransactionID: transaction.ID,
		})
	}
	if len(effects) == 0 {
		return nil, fmt.Errorf(
			"journal transaction %s has no entry for account %s",
			transaction.ID,
			accountID,
		)
	}
	return effects, nil
}

func newJournalBinding(transactions []ledger.Transaction) (*journalBinding, error) {
	result := &journalBinding{
		transactions: make(map[string]ledger.Transaction, len(transactions)),
		bound:        make(map[string]bool, len(transactions)),
	}
	for _, transaction := range transactions {
		if transaction.ID == "" || transaction.Reference == "" {
			return nil, fmt.Errorf("journal transaction identity is required")
		}
		if _, exists := result.transactions[transaction.ID]; exists {
			return nil, fmt.Errorf("journal transaction %s is duplicated", transaction.ID)
		}
		result.transactions[transaction.ID] = transaction
	}
	return result, nil
}

func (b *journalBinding) take(
	id string,
	reference string,
	required bool,
) (*ledger.Transaction, error) {
	transaction, exists := b.transactions[id]
	if !exists {
		if required {
			return nil, fmt.Errorf("required journal transaction %s is missing", id)
		}
		return nil, nil
	}
	if transaction.Reference != reference {
		return nil, fmt.Errorf(
			"journal transaction %s reference=%q want=%q",
			id,
			transaction.Reference,
			reference,
		)
	}
	if b.bound[id] {
		return nil, fmt.Errorf("journal transaction %s is bound more than once", id)
	}
	b.bound[id] = true
	copy := transaction
	return &copy, nil
}

func (b *journalBinding) assertAllBound() error {
	for id := range b.transactions {
		if !b.bound[id] {
			return fmt.Errorf("journal transaction %s was not bound to an event", id)
		}
	}
	return nil
}

func timelineEventID(sequence uint64, eventIndex, timelineIndex uint32) string {
	return fmt.Sprintf("%d:%d:%d", sequence, eventIndex, timelineIndex)
}
