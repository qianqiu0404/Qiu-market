package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/the-web3/s78-market-services/trading/decimal"
	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/query"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

func (s *Server) GetFundingRequest(
	ctx context.Context,
	request *tradingv1.GetFundingRequestRequest,
) (*tradingv1.FundingRequestResponse, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" || request.GetRequestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id and request_id are required")
	}
	if s.funding == nil {
		return nil, status.Error(codes.Unimplemented, "funding request query is unavailable")
	}
	funding, found, err := s.funding.GetFundingRequest(
		ctx, domain.AccountID(request.GetAccountId()), request.GetRequestId(),
	)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "funding request query is unavailable")
	}
	if !found {
		return nil, status.Error(codes.NotFound, "funding_request_not_found")
	}
	scale, err := assetScale(market, funding.Asset)
	if err != nil {
		return nil, status.Error(codes.Internal, "format funding request")
	}
	amount, err := decimal.Format(funding.Amount, scale)
	if err != nil {
		return nil, status.Error(codes.Internal, "format funding request")
	}
	return &tradingv1.FundingRequestResponse{
		MarketId: string(funding.MarketID), RequestId: funding.RequestID,
		FundingEventId: funding.FundingEventID,
		Sequence:       strconv.FormatUint(funding.Sequence, 10), Asset: string(funding.Asset),
		Amount: amount, ProjectionResult: "applied", LedgerBalanced: funding.LedgerBalanced,
		OccurredAt: funding.OccurredAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

const (
	orderCursorKind    = "orders"
	tradeCursorKind    = "account_trades"
	timelineCursorKind = "order_events"
	ledgerCursorKind   = "ledger_entries"

	orderCursorSort    = "accepted_sequence_desc,order_id_desc"
	tradeCursorSort    = "sequence_desc,event_index_desc,trade_id_desc"
	timelineCursorSort = "sequence_asc,event_index_asc,timeline_index_asc"
	ledgerCursorSort   = "sequence_desc,transaction_id_desc,entry_index_desc"
)

func (s *Server) listOrdersV1(
	ctx context.Context,
	request *tradingv1.ListOrdersRequest,
) (*tradingv1.ListOrdersResponse, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" {
		return nil, validationError("account_id is required")
	}
	if err := s.requireQueries(); err != nil {
		return nil, err
	}
	if request.GetStatus() != "" || request.GetSide() != "" || request.GetType() != "" {
		return nil, validationError("status, side and type filters are not available in P0")
	}
	scope, err := parseOrderScope(request)
	if err != nil {
		return nil, err
	}
	limit, err := parseQueryLimit(request.GetLimit())
	if err != nil {
		return nil, err
	}
	filter := query.OrderFilter{Scope: scope}
	filterBinding := "scope=" + string(scope)
	cursor, err := s.decodeOrderCursor(
		request.GetCursor(), request.GetMarketId(), request.GetAccountId(), filterBinding,
	)
	if err != nil {
		return nil, err
	}
	page, err := s.queries.ListOrders(
		ctx, domain.AccountID(request.GetAccountId()), filter, cursor, limit,
	)
	if err != nil {
		return nil, mapQueryError(err)
	}
	response := &tradingv1.ListOrdersResponse{
		Orders: make([]*tradingv1.Order, 0, len(page.Orders)),
	}
	for _, item := range page.Orders {
		if item.Order.AccountID != domain.AccountID(request.GetAccountId()) ||
			item.Order.MarketID != market.ID {
			return nil, status.Error(codes.Internal, "order query identity mismatch")
		}
		converted, convertErr := toOrderView(market, item)
		if convertErr != nil {
			return nil, status.Error(codes.Internal, "unable to encode order query result")
		}
		response.Orders = append(response.Orders, converted)
	}
	if page.NextCursor != nil {
		if page.NextCursor.AcceptedSequence == 0 || page.NextCursor.OrderID == "" {
			return nil, status.Error(codes.Internal, "invalid order next cursor")
		}
		response.NextCursor, err = s.cursors.Encode(
			orderCursorKind,
			request.GetMarketId(),
			request.GetAccountId(),
			filterBinding,
			orderCursorSort,
			[]string{
				strconv.FormatUint(page.NextCursor.AcceptedSequence, 10),
				string(page.NextCursor.OrderID),
			},
		)
		if err != nil {
			return nil, status.Error(codes.Internal, "unable to encode next cursor")
		}
	}
	return response, nil
}

func (s *Server) ListAccountTrades(
	ctx context.Context,
	request *tradingv1.ListAccountTradesRequest,
) (*tradingv1.ListAccountTradesResponse, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" {
		return nil, validationError("account_id is required")
	}
	if err := s.requireQueries(); err != nil {
		return nil, err
	}
	if request.GetSide() != "" {
		return nil, validationError("side filter is not available in P0")
	}
	limit, err := parseQueryLimit(request.GetLimit())
	if err != nil {
		return nil, err
	}
	const filterBinding = "side=all"
	cursor, err := s.decodeTradeCursor(
		request.GetCursor(), request.GetMarketId(), request.GetAccountId(), filterBinding,
	)
	if err != nil {
		return nil, err
	}
	page, err := s.queries.ListAccountTrades(
		ctx,
		domain.AccountID(request.GetAccountId()),
		query.TradeFilter{},
		cursor,
		limit,
	)
	if err != nil {
		return nil, mapQueryError(err)
	}
	response := &tradingv1.ListAccountTradesResponse{
		Trades: make([]*tradingv1.AccountTrade, 0, len(page.Trades)),
	}
	for _, item := range page.Trades {
		if item.MarketID != market.ID || item.OrderID == "" {
			return nil, status.Error(codes.Internal, "account trade query identity mismatch")
		}
		converted, convertErr := toAccountTrade(market, item)
		if convertErr != nil {
			return nil, status.Error(codes.Internal, "unable to encode account trade result")
		}
		response.Trades = append(response.Trades, converted)
	}
	if page.NextCursor != nil {
		if page.NextCursor.Sequence == 0 || page.NextCursor.TradeID == "" {
			return nil, status.Error(codes.Internal, "invalid trade next cursor")
		}
		response.NextCursor, err = s.cursors.Encode(
			tradeCursorKind,
			request.GetMarketId(),
			request.GetAccountId(),
			filterBinding,
			tradeCursorSort,
			[]string{
				strconv.FormatUint(page.NextCursor.Sequence, 10),
				strconv.FormatUint(uint64(page.NextCursor.EventIndex), 10),
				string(page.NextCursor.TradeID),
			},
		)
		if err != nil {
			return nil, status.Error(codes.Internal, "unable to encode next cursor")
		}
	}
	return response, nil
}

func (s *Server) ListOrderEvents(
	ctx context.Context,
	request *tradingv1.ListOrderEventsRequest,
) (*tradingv1.ListOrderEventsResponse, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" || request.GetOrderId() == "" {
		return nil, validationError("account_id and order_id are required")
	}
	if err := s.requireQueries(); err != nil {
		return nil, err
	}
	accountID := domain.AccountID(request.GetAccountId())
	orderID := domain.OrderID(request.GetOrderId())
	limit, err := parseQueryLimit(request.GetLimit())
	if err != nil {
		return nil, err
	}
	filterBinding := "order_id=" + request.GetOrderId()
	cursor, err := s.decodeTimelineCursor(
		request.GetCursor(), request.GetMarketId(), request.GetAccountId(), filterBinding,
	)
	if err != nil {
		return nil, err
	}
	order, found, err := s.queries.GetOrder(ctx, accountID, orderID)
	if err != nil {
		return nil, mapQueryError(err)
	}
	if !found {
		return nil, status.Error(codes.NotFound, "order_not_found")
	}
	if order.Order.AccountID != accountID || order.Order.MarketID != market.ID ||
		order.Order.ID != orderID {
		return nil, status.Error(codes.Internal, "order query identity mismatch")
	}
	page, err := s.queries.ListOrderEvents(ctx, accountID, orderID, cursor, limit)
	if err != nil {
		return nil, mapQueryError(err)
	}
	response := &tradingv1.ListOrderEventsResponse{
		Events: make([]*tradingv1.OrderEvent, 0, len(page.Events)),
	}
	for _, item := range page.Events {
		if item.MarketID != market.ID || item.OrderID != orderID {
			return nil, status.Error(codes.Internal, "order event query identity mismatch")
		}
		converted, convertErr := toOrderEvent(market, item)
		if convertErr != nil {
			return nil, status.Error(codes.Internal, "unable to encode order event result")
		}
		response.Events = append(response.Events, converted)
	}
	if page.NextCursor != nil {
		if page.NextCursor.Sequence == 0 {
			return nil, status.Error(codes.Internal, "invalid timeline next cursor")
		}
		response.NextCursor, err = s.cursors.Encode(
			timelineCursorKind,
			request.GetMarketId(),
			request.GetAccountId(),
			filterBinding,
			timelineCursorSort,
			[]string{
				strconv.FormatUint(page.NextCursor.Sequence, 10),
				strconv.FormatUint(uint64(page.NextCursor.EventIndex), 10),
				strconv.FormatUint(uint64(page.NextCursor.TimelineIndex), 10),
			},
		)
		if err != nil {
			return nil, status.Error(codes.Internal, "unable to encode next cursor")
		}
	}
	return response, nil
}

func (s *Server) ListLedgerEntries(
	ctx context.Context,
	request *tradingv1.ListLedgerEntriesRequest,
) (*tradingv1.ListLedgerEntriesResponse, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" {
		return nil, validationError("account_id is required")
	}
	if err := s.requireQueries(); err != nil {
		return nil, err
	}
	filter, filterBinding, err := parseLedgerFilter(market, request.GetAsset(), request.GetReason())
	if err != nil {
		return nil, err
	}
	limit, err := parseQueryLimit(request.GetLimit())
	if err != nil {
		return nil, err
	}
	cursor, err := s.decodeLedgerCursor(
		request.GetCursor(), request.GetMarketId(), request.GetAccountId(), filterBinding,
	)
	if err != nil {
		return nil, err
	}
	page, err := s.queries.ListLedgerEntries(
		ctx, domain.AccountID(request.GetAccountId()), filter, cursor, limit,
	)
	if err != nil {
		return nil, mapQueryError(err)
	}
	response := &tradingv1.ListLedgerEntriesResponse{
		Entries: make([]*tradingv1.AccountLedgerEntry, 0, len(page.Entries)),
	}
	for _, item := range page.Entries {
		if item.MarketID != market.ID {
			return nil, status.Error(codes.Internal, "ledger query identity mismatch")
		}
		converted, convertErr := toAccountLedgerEntry(market, item)
		if convertErr != nil {
			return nil, status.Error(codes.Internal, "unable to encode ledger query result")
		}
		response.Entries = append(response.Entries, converted)
	}
	if page.NextCursor != nil {
		if page.NextCursor.Sequence == 0 || page.NextCursor.TransactionID == "" {
			return nil, status.Error(codes.Internal, "invalid ledger next cursor")
		}
		response.NextCursor, err = s.cursors.Encode(
			ledgerCursorKind,
			request.GetMarketId(),
			request.GetAccountId(),
			filterBinding,
			ledgerCursorSort,
			[]string{
				strconv.FormatUint(page.NextCursor.Sequence, 10),
				page.NextCursor.TransactionID,
				strconv.FormatUint(uint64(page.NextCursor.EntryIndex), 10),
			},
		)
		if err != nil {
			return nil, status.Error(codes.Internal, "unable to encode next cursor")
		}
	}
	return response, nil
}

func (s *Server) requireQueries() error {
	if s.queries == nil || s.cursors == nil {
		return status.Error(codes.Unimplemented, "Trade Product V1 query reader is not configured")
	}
	return nil
}

func parseOrderScope(request *tradingv1.ListOrdersRequest) (query.OrderScope, error) {
	if request.OpenOnly != nil && request.GetScope() != "" {
		return "", validationError("open_only and scope cannot be combined")
	}
	if request.OpenOnly != nil {
		if request.GetOpenOnly() {
			return query.OrderScopeOpen, nil
		}
		return query.OrderScopeAll, nil
	}
	scope := query.OrderScope(request.GetScope())
	if scope == "" {
		scope = query.OrderScopeAll
	}
	switch scope {
	case query.OrderScopeAll, query.OrderScopeOpen, query.OrderScopeHistory:
		return scope, nil
	default:
		return "", validationError("scope must be all, open or history")
	}
}

func parseLedgerFilter(
	market domain.Market,
	assetValue string,
	reasonValue string,
) (query.LedgerFilter, string, error) {
	if assetValue == "" {
		assetValue = "all"
	}
	if reasonValue == "" {
		reasonValue = "all"
	}
	filter := query.LedgerFilter{}
	switch assetValue {
	case "all":
	case string(market.BaseAsset), string(market.QuoteAsset):
		filter.Asset = domain.Asset(assetValue)
	default:
		return query.LedgerFilter{}, "", validationError("asset must be all, BTC or USDT")
	}
	switch query.LedgerReason(reasonValue) {
	case "all":
	case query.LedgerReasonVirtualFund,
		query.LedgerReasonOrderHold,
		query.LedgerReasonOrderRelease,
		query.LedgerReasonTradeSettlement,
		query.LedgerReasonOther:
		filter.Reason = query.LedgerReason(reasonValue)
	default:
		return query.LedgerFilter{}, "", validationError(
			"reason must be all, virtual_fund, order_hold, order_release, trade_settlement or other",
		)
	}
	return filter, "asset=" + assetValue + "&reason=" + reasonValue, nil
}

func parseQueryLimit(value uint32) (int, error) {
	if value == 0 {
		return 50, nil
	}
	if value > 100 {
		return 0, validationError("limit must be in [1,100]")
	}
	return int(value), nil
}

func validationError(message string) error {
	return status.Error(codes.InvalidArgument, "validation_failed: "+message)
}

func invalidCursorError() error {
	return status.Error(codes.InvalidArgument, "invalid_cursor: cursor is invalid or expired")
}

func mapQueryError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "query canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "query deadline exceeded")
	default:
		return status.Error(codes.Unavailable, "query reader is unavailable")
	}
}

func (s *Server) decodeOrderCursor(
	value, market, account, filters string,
) (*query.OrderCursor, error) {
	if value == "" {
		return nil, nil
	}
	position, err := s.cursors.Decode(
		value, orderCursorKind, market, account, filters, orderCursorSort, 2,
	)
	if err != nil {
		return nil, invalidCursorError()
	}
	sequence, err := strconv.ParseUint(position[0], 10, 64)
	if err != nil || sequence == 0 || position[1] == "" {
		return nil, invalidCursorError()
	}
	return &query.OrderCursor{
		AcceptedSequence: sequence, OrderID: domain.OrderID(position[1]),
	}, nil
}

func (s *Server) decodeTradeCursor(
	value, market, account, filters string,
) (*query.TradeCursor, error) {
	if value == "" {
		return nil, nil
	}
	position, err := s.cursors.Decode(
		value, tradeCursorKind, market, account, filters, tradeCursorSort, 3,
	)
	if err != nil {
		return nil, invalidCursorError()
	}
	sequence, eventIndex, err := parseSequenceAndIndex(position[0], position[1])
	if err != nil || position[2] == "" {
		return nil, invalidCursorError()
	}
	return &query.TradeCursor{
		Sequence: sequence, EventIndex: eventIndex, TradeID: domain.TradeID(position[2]),
	}, nil
}

func (s *Server) decodeTimelineCursor(
	value, market, account, filters string,
) (*query.TimelineCursor, error) {
	if value == "" {
		return nil, nil
	}
	position, err := s.cursors.Decode(
		value, timelineCursorKind, market, account, filters, timelineCursorSort, 3,
	)
	if err != nil {
		return nil, invalidCursorError()
	}
	sequence, eventIndex, err := parseSequenceAndIndex(position[0], position[1])
	if err != nil {
		return nil, invalidCursorError()
	}
	timelineIndex, err := strconv.ParseUint(position[2], 10, 32)
	if err != nil {
		return nil, invalidCursorError()
	}
	return &query.TimelineCursor{
		Sequence: sequence, EventIndex: eventIndex, TimelineIndex: uint32(timelineIndex),
	}, nil
}

func (s *Server) decodeLedgerCursor(
	value, market, account, filters string,
) (*query.LedgerCursor, error) {
	if value == "" {
		return nil, nil
	}
	position, err := s.cursors.Decode(
		value, ledgerCursorKind, market, account, filters, ledgerCursorSort, 3,
	)
	if err != nil {
		return nil, invalidCursorError()
	}
	sequence, err := strconv.ParseUint(position[0], 10, 64)
	if err != nil || sequence == 0 || position[1] == "" {
		return nil, invalidCursorError()
	}
	entryIndex, err := strconv.ParseUint(position[2], 10, 32)
	if err != nil {
		return nil, invalidCursorError()
	}
	return &query.LedgerCursor{
		Sequence: sequence, TransactionID: position[1], EntryIndex: uint32(entryIndex),
	}, nil
}

func parseSequenceAndIndex(sequenceValue, indexValue string) (uint64, uint32, error) {
	sequence, err := strconv.ParseUint(sequenceValue, 10, 64)
	if err != nil || sequence == 0 {
		return 0, 0, fmt.Errorf("invalid sequence")
	}
	index, err := strconv.ParseUint(indexValue, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid event index")
	}
	return sequence, uint32(index), nil
}

func toOrderView(market domain.Market, view query.OrderView) (*tradingv1.Order, error) {
	if view.Order.ID == "" || view.Order.AcceptedSequence == 0 || view.Order.AccountID == "" ||
		view.Order.MarketID == "" {
		return nil, fmt.Errorf("invalid order query identity")
	}
	response, err := toOrder(market, view.Order)
	if err != nil {
		return nil, err
	}
	if view.AverageFillPrice != nil {
		response.AverageFillPrice, err = decimal.Format(*view.AverageFillPrice, market.QuoteScale)
		if err != nil {
			return nil, err
		}
	}
	response.CreatedAt = formatQueryTime(view.CreatedAt)
	response.UpdatedAt = formatQueryTime(view.UpdatedAt)
	if view.Order.Type == domain.OrderTypeLimit {
		response.OriginalQuoteBudget = ""
		response.RemainingQuoteBudget = ""
	} else if view.Order.Type == domain.OrderTypeMarket {
		response.Price = ""
		if view.Order.Side == domain.SideBuy {
			response.OriginalQuantity = ""
			response.RemainingQuantity = ""
		} else {
			response.OriginalQuoteBudget = ""
			response.RemainingQuoteBudget = ""
		}
	}
	return response, nil
}

func toAccountTrade(
	market domain.Market,
	trade query.AccountTrade,
) (*tradingv1.AccountTrade, error) {
	if trade.ID == "" || trade.OrderID == "" || trade.Sequence == 0 ||
		(trade.Side != domain.SideBuy && trade.Side != domain.SideSell) ||
		(trade.LiquidityRole != domain.LiquidityRoleMaker &&
			trade.LiquidityRole != domain.LiquidityRoleTaker) {
		return nil, fmt.Errorf("invalid account trade side or liquidity role")
	}
	price, err := decimal.Format(trade.Price, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	quantity, err := decimal.Format(trade.Quantity, market.BaseScale)
	if err != nil {
		return nil, err
	}
	quoteAmount, err := decimal.Format(trade.QuoteAmount, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	feeScale, err := assetScale(market, trade.FeeAsset)
	if err != nil {
		return nil, err
	}
	feeAmount, err := decimal.Format(trade.FeeAmount, feeScale)
	if err != nil {
		return nil, err
	}
	return &tradingv1.AccountTrade{
		Id:            string(trade.ID),
		MarketId:      string(trade.MarketID),
		OrderId:       string(trade.OrderID),
		Side:          trade.Side.String(),
		LiquidityRole: trade.LiquidityRole.String(),
		Price:         price,
		Quantity:      quantity,
		QuoteAmount:   quoteAmount,
		FeeAsset:      string(trade.FeeAsset),
		FeeAmount:     feeAmount,
		FeeRateBps:    strconv.FormatInt(trade.FeeRateBPS, 10),
		Sequence:      strconv.FormatUint(trade.Sequence, 10),
		EventIndex:    trade.EventIndex,
		OccurredAt:    formatQueryTime(trade.OccurredAt),
	}, nil
}

func toOrderEvent(
	market domain.Market,
	event query.OrderEvent,
) (*tradingv1.OrderEvent, error) {
	if event.EventID == "" || event.Sequence == 0 || event.SourceKind != query.SourceKindEvent ||
		!validOrderEventType(event.Type) || !validOrderStatus(event.Status) {
		return nil, fmt.Errorf("invalid order event projection")
	}
	response := &tradingv1.OrderEvent{
		EventId:        event.EventID,
		MarketId:       string(event.MarketID),
		OrderId:        string(event.OrderID),
		Sequence:       strconv.FormatUint(event.Sequence, 10),
		EventIndex:     event.EventIndex,
		TimelineIndex:  event.TimelineIndex,
		SourceKind:     string(event.SourceKind),
		Type:           string(event.Type),
		Status:         event.Status.String(),
		TradeId:        string(event.TradeID),
		Reason:         event.Reason,
		OccurredAt:     formatQueryTime(event.OccurredAt),
		BalanceEffects: make([]*tradingv1.BalanceEffect, 0, len(event.BalanceEffects)),
	}
	var err error
	if event.Quantity != nil {
		response.Quantity, err = decimal.Format(*event.Quantity, market.BaseScale)
		if err != nil {
			return nil, err
		}
	}
	if event.Price != nil {
		response.Price, err = decimal.Format(*event.Price, market.QuoteScale)
		if err != nil {
			return nil, err
		}
	}
	if event.RemainingQuantity != nil {
		response.RemainingQuantity, err = decimal.Format(
			*event.RemainingQuantity, market.BaseScale,
		)
		if err != nil {
			return nil, err
		}
	}
	if event.RemainingQuoteBudget != nil {
		response.RemainingQuoteBudget, err = decimal.Format(
			*event.RemainingQuoteBudget, market.QuoteScale,
		)
		if err != nil {
			return nil, err
		}
	}
	if event.Fee != nil {
		response.Fee, err = toAccountFee(market, *event.Fee)
		if err != nil {
			return nil, err
		}
	}
	for _, effect := range event.BalanceEffects {
		converted, convertErr := toBalanceEffect(market, effect)
		if convertErr != nil {
			return nil, convertErr
		}
		response.BalanceEffects = append(response.BalanceEffects, converted)
	}
	return response, nil
}

func toAccountFee(market domain.Market, fee query.FeeView) (*tradingv1.AccountFee, error) {
	scale, err := assetScale(market, fee.Asset)
	if err != nil {
		return nil, err
	}
	amount, err := decimal.Format(fee.Amount, scale)
	if err != nil {
		return nil, err
	}
	return &tradingv1.AccountFee{
		Asset: string(fee.Asset), Amount: amount,
		RateBps: strconv.FormatInt(fee.RateBPS, 10), Role: fee.Role.String(),
	}, nil
}

func toBalanceEffect(
	market domain.Market,
	effect query.BalanceEffect,
) (*tradingv1.BalanceEffect, error) {
	if effect.Bucket != query.BalanceBucketAvailable && effect.Bucket != query.BalanceBucketHeld {
		return nil, fmt.Errorf("invalid balance effect bucket")
	}
	if !validLedgerReason(effect.Reason) {
		return nil, fmt.Errorf("invalid balance effect reason")
	}
	scale, err := assetScale(market, effect.Asset)
	if err != nil {
		return nil, err
	}
	amount, err := formatSignedDecimal(effect.Amount, scale)
	if err != nil {
		return nil, err
	}
	return &tradingv1.BalanceEffect{
		Asset: string(effect.Asset), Bucket: string(effect.Bucket), Amount: amount,
		Reason: string(effect.Reason), TransactionId: effect.TransactionID,
	}, nil
}

func toAccountLedgerEntry(
	market domain.Market,
	entry query.LedgerEntry,
) (*tradingv1.AccountLedgerEntry, error) {
	if entry.EntryID == "" || entry.Sequence == 0 || entry.TransactionID == "" {
		return nil, fmt.Errorf("invalid ledger identity")
	}
	if entry.Bucket != query.BalanceBucketAvailable && entry.Bucket != query.BalanceBucketHeld {
		return nil, fmt.Errorf("invalid ledger bucket")
	}
	if !validLedgerReason(entry.Reason) {
		return nil, fmt.Errorf("invalid ledger reason")
	}
	scale, err := assetScale(market, entry.Asset)
	if err != nil {
		return nil, err
	}
	amount, err := formatSignedDecimal(entry.Amount, scale)
	if err != nil {
		return nil, err
	}
	return &tradingv1.AccountLedgerEntry{
		EntryId:       entry.EntryID,
		MarketId:      string(entry.MarketID),
		Sequence:      strconv.FormatUint(entry.Sequence, 10),
		TransactionId: entry.TransactionID,
		EntryIndex:    entry.EntryIndex,
		Asset:         string(entry.Asset),
		Bucket:        string(entry.Bucket),
		Amount:        amount,
		Reason:        string(entry.Reason),
		Reference:     entry.Reference,
		OrderId:       string(entry.OrderID),
		TradeId:       string(entry.TradeID),
		OccurredAt:    formatQueryTime(entry.OccurredAt),
	}, nil
}

func formatQueryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func validLedgerReason(reason query.LedgerReason) bool {
	switch reason {
	case query.LedgerReasonVirtualFund,
		query.LedgerReasonOrderHold,
		query.LedgerReasonOrderRelease,
		query.LedgerReasonTradeSettlement,
		query.LedgerReasonOther:
		return true
	default:
		return false
	}
}

func validOrderStatus(orderStatus domain.OrderStatus) bool {
	switch orderStatus {
	case domain.OrderStatusReceived,
		domain.OrderStatusRejected,
		domain.OrderStatusOpen,
		domain.OrderStatusPartiallyFilled,
		domain.OrderStatusFilled,
		domain.OrderStatusCanceled:
		return true
	default:
		return false
	}
}

func validOrderEventType(eventType domain.EventType) bool {
	switch eventType {
	case domain.EventOrderAccepted,
		domain.EventOrderRejected,
		domain.EventTradeExecuted,
		domain.EventOrderRested,
		domain.EventOrderFilled,
		domain.EventOrderCanceled,
		domain.EventCancelRejected,
		domain.EventSelfTradePrevented:
		return true
	default:
		return false
	}
}

func formatSignedDecimal(atoms, scale int64) (string, error) {
	if atoms >= 0 {
		return decimal.Format(atoms, scale)
	}
	if scale <= 0 {
		return "", fmt.Errorf("scale must be a positive power of ten")
	}
	precision := 0
	for remaining := scale; remaining > 1; remaining /= 10 {
		if remaining%10 != 0 {
			return "", fmt.Errorf("scale must be a positive power of ten")
		}
		precision++
	}
	magnitude := uint64(-(atoms + 1)) + 1
	whole := magnitude / uint64(scale)
	fraction := magnitude % uint64(scale)
	if fraction == 0 {
		return "-" + strconv.FormatUint(whole, 10), nil
	}
	formatted := fmt.Sprintf("%d.%0*d", whole, precision, fraction)
	return "-" + strings.TrimRight(formatted, "0"), nil
}
