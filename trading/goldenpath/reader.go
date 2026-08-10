package goldenpath

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/query"
	"github.com/the-web3/s78-market-services/trading/readmodel"
	"github.com/the-web3/s78-market-services/trading/store"
)

type memoryReader struct {
	store  *store.Memory
	market domain.Market
}

func fixtureTime(sequence uint64, index uint32) time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, int(index), time.UTC).Add(time.Duration(sequence) * time.Second)
}

func (r *memoryReader) records(ctx context.Context) ([]store.Record, error) {
	return r.store.RecordsAfter(ctx, 0)
}

func (r *memoryReader) orderViews(ctx context.Context, account domain.AccountID) ([]query.OrderView, error) {
	records, err := r.records(ctx)
	if err != nil {
		return nil, err
	}
	orders := make(map[domain.OrderID]domain.Order)
	for _, record := range records {
		for _, order := range record.Projection.Orders {
			if order.AccountID == account {
				orders[order.ID] = order
			}
		}
	}
	views := make([]query.OrderView, 0, len(orders))
	for _, order := range orders {
		created := fixtureTime(order.AcceptedSequence, 0)
		view := query.OrderView{Order: order, CreatedAt: created, UpdatedAt: fixtureTime(order.LastSequence, 0)}
		if order.FilledQuantity > 0 {
			average, averageErr := domain.CheckedMulDivFloor(order.SpentQuote, r.market.BaseScale, order.FilledQuantity)
			if averageErr != nil {
				return nil, averageErr
			}
			view.AverageFillPrice = &average
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Order.AcceptedSequence != views[j].Order.AcceptedSequence {
			return views[i].Order.AcceptedSequence > views[j].Order.AcceptedSequence
		}
		return views[i].Order.ID > views[j].Order.ID
	})
	return views, nil
}

func (r *memoryReader) GetOrder(ctx context.Context, account domain.AccountID, id domain.OrderID) (query.OrderView, bool, error) {
	views, err := r.orderViews(ctx, account)
	if err != nil {
		return query.OrderView{}, false, err
	}
	for _, view := range views {
		if view.Order.ID == id {
			return view, true, nil
		}
	}
	return query.OrderView{}, false, nil
}

func (r *memoryReader) ListOrders(ctx context.Context, account domain.AccountID, filter query.OrderFilter, cursor *query.OrderCursor, limit int) (query.OrderPage, error) {
	views, err := r.orderViews(ctx, account)
	if err != nil {
		return query.OrderPage{}, err
	}
	filtered := views[:0]
	for _, view := range views {
		open := view.Order.IsOpen()
		if (filter.Scope == query.OrderScopeOpen && !open) || (filter.Scope == query.OrderScopeHistory && open) {
			continue
		}
		if cursor != nil && (view.Order.AcceptedSequence > cursor.AcceptedSequence ||
			(view.Order.AcceptedSequence == cursor.AcceptedSequence && view.Order.ID >= cursor.OrderID)) {
			continue
		}
		filtered = append(filtered, view)
	}
	if limit <= 0 {
		return query.OrderPage{}, fmt.Errorf("limit must be positive")
	}
	page := query.OrderPage{Orders: filtered}
	if len(filtered) > limit {
		page.Orders = filtered[:limit]
		last := page.Orders[len(page.Orders)-1].Order
		page.NextCursor = &query.OrderCursor{AcceptedSequence: last.AcceptedSequence, OrderID: last.ID}
	}
	return page, nil
}

func (r *memoryReader) ListAccountTrades(ctx context.Context, account domain.AccountID, filter query.TradeFilter, cursor *query.TradeCursor, limit int) (query.TradePage, error) {
	records, err := r.records(ctx)
	if err != nil {
		return query.TradePage{}, err
	}
	var trades []query.AccountTrade
	for _, record := range records {
		for _, trade := range record.Projection.Trades {
			var side domain.Side
			var orderID domain.OrderID
			var fee domain.Fee
			switch account {
			case trade.BuyerAccountID:
				side, fee = domain.SideBuy, trade.BuyerFee
			case trade.SellerAccountID:
				side, fee = domain.SideSell, trade.SellerFee
			default:
				continue
			}
			if account == trade.MakerAccountID {
				orderID = trade.MakerOrderID
			} else {
				orderID = trade.TakerOrderID
			}
			var eventIndex uint32
			for _, event := range record.Result.Events {
				if event.Trade != nil && event.Trade.ID == trade.ID {
					eventIndex = event.Index
					break
				}
			}
			item := query.AccountTrade{ID: trade.ID, MarketID: trade.MarketID, OrderID: orderID, Side: side,
				LiquidityRole: fee.Role, Price: trade.Price, Quantity: trade.Quantity, QuoteAmount: trade.QuoteAmount,
				FeeAsset: fee.Asset, FeeAmount: fee.Amount, FeeRateBPS: fee.RateBPS,
				Sequence: record.Command.Sequence, EventIndex: eventIndex, OccurredAt: fixtureTime(record.Command.Sequence, eventIndex)}
			if filter.Side == 0 || filter.Side == side {
				trades = append(trades, item)
			}
		}
	}
	sort.Slice(trades, func(i, j int) bool {
		if trades[i].Sequence != trades[j].Sequence {
			return trades[i].Sequence > trades[j].Sequence
		}
		if trades[i].EventIndex != trades[j].EventIndex {
			return trades[i].EventIndex > trades[j].EventIndex
		}
		return trades[i].ID > trades[j].ID
	})
	if cursor != nil {
		kept := trades[:0]
		for _, item := range trades {
			if item.Sequence < cursor.Sequence || (item.Sequence == cursor.Sequence &&
				(item.EventIndex < cursor.EventIndex || (item.EventIndex == cursor.EventIndex && item.ID < cursor.TradeID))) {
				kept = append(kept, item)
			}
		}
		trades = kept
	}
	page := query.TradePage{Trades: trades}
	if len(trades) > limit {
		page.Trades = trades[:limit]
		last := page.Trades[len(page.Trades)-1]
		page.NextCursor = &query.TradeCursor{Sequence: last.Sequence, EventIndex: last.EventIndex, TradeID: last.ID}
	}
	return page, nil
}

func (r *memoryReader) ListOrderEvents(ctx context.Context, account domain.AccountID, orderID domain.OrderID, cursor *query.TimelineCursor, limit int) (query.OrderEventPage, error) {
	if _, ok, err := r.GetOrder(ctx, account, orderID); err != nil || !ok {
		return query.OrderEventPage{}, err
	}
	records, err := r.records(ctx)
	if err != nil {
		return query.OrderEventPage{}, err
	}
	var events []query.OrderEvent
	for _, record := range records {
		for _, event := range record.Result.Events {
			if event.OrderID != orderID {
				continue
			}
			item := query.OrderEvent{EventID: fmt.Sprintf("%d:%d", event.Sequence, event.Index), MarketID: r.market.ID,
				OrderID: orderID, Sequence: event.Sequence, EventIndex: event.Index, SourceKind: query.SourceKindEvent,
				Type: event.Type, Status: event.Status, Reason: event.Reason, OccurredAt: fixtureTime(event.Sequence, event.Index)}
			if event.Quantity != 0 {
				value := event.Quantity
				item.Quantity = &value
			}
			if event.Price != 0 {
				value := event.Price
				item.Price = &value
			}
			if event.Remaining != 0 {
				value := event.Remaining
				item.RemainingQuantity = &value
			}
			item.RemainingQuoteBudget = event.RemainingQuoteBudget
			if event.Trade != nil {
				item.TradeID = event.Trade.ID
			}
			events = append(events, item)
		}
	}
	if cursor != nil {
		kept := events[:0]
		for _, item := range events {
			if item.Sequence > cursor.Sequence || (item.Sequence == cursor.Sequence && item.EventIndex > cursor.EventIndex) {
				kept = append(kept, item)
			}
		}
		events = kept
	}
	page := query.OrderEventPage{Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		last := page.Events[len(page.Events)-1]
		page.NextCursor = &query.TimelineCursor{Sequence: last.Sequence, EventIndex: last.EventIndex, TimelineIndex: last.TimelineIndex}
	}
	return page, nil
}

func (r *memoryReader) ListLedgerEntries(ctx context.Context, account domain.AccountID, filter query.LedgerFilter, cursor *query.LedgerCursor, limit int) (query.LedgerPage, error) {
	records, err := r.records(ctx)
	if err != nil {
		return query.LedgerPage{}, err
	}
	available, held := ledger.UserAvailable(account), ledger.UserHeld(account)
	var entries []query.LedgerEntry
	for _, record := range records {
		for _, tx := range record.Journal {
			for index, entry := range tx.Entries {
				var bucket query.BalanceBucket
				switch entry.Account {
				case available:
					bucket = query.BalanceBucketAvailable
				case held:
					bucket = query.BalanceBucketHeld
				default:
					continue
				}
				reason := readmodel.ClassifyLedgerReason(tx.ID, tx.Reference)
				if (filter.Asset != "" && filter.Asset != entry.Asset) || (filter.Reason != "" && filter.Reason != reason) {
					continue
				}
				item := query.LedgerEntry{EntryID: fmt.Sprintf("%d:%s:%d", record.Command.Sequence, tx.ID, index), MarketID: r.market.ID,
					Sequence: record.Command.Sequence, TransactionID: tx.ID, EntryIndex: uint32(index), Asset: entry.Asset,
					Bucket: bucket, Amount: entry.Amount, Reason: reason, Reference: tx.Reference,
					OrderID: readmodel.OrderIDFromReference(tx.Reference), TradeID: readmodel.TradeIDFromReference(tx.Reference),
					OccurredAt: fixtureTime(record.Command.Sequence, uint32(index))}
				entries = append(entries, item)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Sequence != entries[j].Sequence {
			return entries[i].Sequence > entries[j].Sequence
		}
		if entries[i].TransactionID != entries[j].TransactionID {
			return entries[i].TransactionID > entries[j].TransactionID
		}
		return entries[i].EntryIndex > entries[j].EntryIndex
	})
	if cursor != nil {
		kept := entries[:0]
		for _, item := range entries {
			if item.Sequence < cursor.Sequence || (item.Sequence == cursor.Sequence &&
				(item.TransactionID < cursor.TransactionID || (item.TransactionID == cursor.TransactionID && item.EntryIndex < cursor.EntryIndex))) {
				kept = append(kept, item)
			}
		}
		entries = kept
	}
	page := query.LedgerPage{Entries: entries}
	if len(entries) > limit {
		page.Entries = entries[:limit]
		last := page.Entries[len(page.Entries)-1]
		page.NextCursor = &query.LedgerCursor{Sequence: last.Sequence, TransactionID: last.TransactionID, EntryIndex: last.EntryIndex}
	}
	return page, nil
}
