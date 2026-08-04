package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/the-web3/s78-market-services/trading/decimal"
	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/outbox"
	"github.com/the-web3/s78-market-services/trading/recovery"
	"github.com/the-web3/s78-market-services/trading/reliability"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
)

type Engine interface {
	Submit(context.Context, domain.NewOrder) (domain.Result, error)
	Cancel(context.Context, domain.CancelOrder) (domain.Result, error)
	Fund(context.Context, domain.FundRequest) (domain.Result, error)
	Market() (domain.Market, error)
	Order(domain.OrderID) (domain.Order, bool, error)
	Orders(domain.AccountID, bool) ([]domain.Order, error)
	Trades(domain.AccountID) ([]domain.Trade, error)
	Balances(domain.AccountID) ([]exchange.AssetBalanceView, error)
	Depth(int) (exchange.OrderBookView, error)
	Status() tradingruntime.Status
}

type Cursor struct {
	Sequence   uint64
	EventIndex uint32
}

type StoredEvent struct {
	MarketID        domain.MarketID
	Cursor          Cursor
	BatchEventCount uint32
	Event           domain.Event
}

type EventSource interface {
	EventsAfter(context.Context, Cursor, int) ([]StoredEvent, error)
	BatchEventCount(context.Context, uint64) (uint32, bool, error)
}

type DeliveryStatusSource interface {
	Status() outbox.Status
}

type Config struct {
	EventBatchSize int
	EventPollEvery time.Duration
	Recovery       interface {
		Status(context.Context) (recovery.Status, error)
		Promote(context.Context, recovery.Binding, recovery.TransportEvidence) (recovery.Status, error)
	}
}

func (s *Server) GetRecoveryStatus(
	ctx context.Context,
	request *tradingv1.GetRecoveryStatusRequest,
) (*tradingv1.RecoveryStatusResponse, error) {
	if _, err := s.market(request.GetMarketId()); err != nil {
		return nil, err
	}
	if s.config.Recovery == nil {
		return nil, status.Error(codes.FailedPrecondition, "recovery gate is not enabled")
	}
	current, err := s.config.Recovery.Status(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "recovery status is unavailable")
	}
	return toRecoveryStatus(current), nil
}

func (s *Server) PromoteRecovery(
	ctx context.Context,
	request *tradingv1.PromoteRecoveryRequest,
) (*tradingv1.RecoveryStatusResponse, error) {
	if s.config.Recovery == nil {
		return nil, status.Error(codes.FailedPrecondition, "recovery gate is not enabled")
	}
	bindingRequest := request.GetBinding()
	evidenceRequest := request.GetTransportEvidence()
	if bindingRequest == nil || evidenceRequest == nil {
		return nil, status.Error(codes.InvalidArgument, "binding and transport evidence are required")
	}
	if _, err := s.market(bindingRequest.GetMarketId()); err != nil {
		return nil, err
	}
	version, err := strconv.ParseUint(bindingRequest.GetVersion(), 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid recovery version")
	}
	sequence, err := strconv.ParseUint(bindingRequest.GetRuntimeSequence(), 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid runtime sequence")
	}
	maximumGap, err := strconv.ParseInt(evidenceRequest.GetMaximumGapMs(), 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid maximum sample gap")
	}
	firstSample, err := time.Parse(time.RFC3339Nano, evidenceRequest.GetFirstSampleAt())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid first sample timestamp")
	}
	lastSample, err := time.Parse(time.RFC3339Nano, evidenceRequest.GetLastSampleAt())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid last sample timestamp")
	}
	runtimeStatus := s.engine.Status()
	if runtimeStatus.State != tradingruntime.StateReady ||
		runtimeStatus.Sequence != sequence ||
		runtimeStatus.StateHash != bindingRequest.GetStateHash() ||
		runtimeStatus.QueueDepth != 0 || runtimeStatus.LastError != "" {
		return nil, status.Error(
			codes.FailedPrecondition,
			"runtime changed after transport observation",
		)
	}
	if s.delivery != nil {
		deliveryStatus := s.delivery.Status()
		if deliveryStatus.State != "ready" || deliveryStatus.LastError != "" ||
			deliveryStatus.Checkpoint.Sequence != sequence {
			return nil, status.Error(
				codes.FailedPrecondition,
				"outbox changed after transport observation",
			)
		}
	}
	promoted, err := s.config.Recovery.Promote(ctx, recovery.Binding{
		MarketID:        domain.MarketID(bindingRequest.GetMarketId()),
		EpochID:         bindingRequest.GetEpochId(),
		Version:         version,
		RuntimeSequence: sequence,
		StateHash:       bindingRequest.GetStateHash(),
	}, recovery.TransportEvidence{
		SampleCount:    int(evidenceRequest.GetSampleCount()),
		FirstSampleAt:  firstSample,
		LastSampleAt:   lastSample,
		MaximumGapMS:   maximumGap,
		EvidenceSHA256: evidenceRequest.GetEvidenceSha256(),
	})
	if err != nil {
		switch {
		case errors.Is(err, recovery.ErrVersionConflict):
			return nil, status.Error(codes.Aborted, "recovery promotion CAS failed")
		case errors.Is(err, recovery.ErrBindingMismatch),
			errors.Is(err, recovery.ErrTransportEvidence),
			errors.Is(err, recovery.ErrWriteBlocked):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Unavailable, "recovery promotion is unavailable")
		}
	}
	return toRecoveryStatus(promoted), nil
}

func toRecoveryStatus(current recovery.Status) *tradingv1.RecoveryStatusResponse {
	return &tradingv1.RecoveryStatusResponse{
		MarketId:            string(current.MarketID),
		EpochId:             current.EpochID,
		Phase:               string(current.Phase),
		Version:             strconv.FormatUint(current.Version, 10),
		RuntimeSequence:     strconv.FormatUint(current.Proof.RuntimeSequence, 10),
		StateHash:           current.Proof.StateHash,
		WritesEnabled:       current.WritesEnabled,
		ContinuityUncertain: current.ContinuityUncertain,
		ContinuityError:     current.ContinuityError,
		UpdatedAt:           current.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func DefaultConfig() Config {
	return Config{
		EventBatchSize: 100,
		EventPollEvery: 100 * time.Millisecond,
	}
}

type Server struct {
	tradingv1.UnimplementedTradingServiceServer

	engine   Engine
	events   EventSource
	delivery DeliveryStatusSource
	config   Config
}

func New(
	engine Engine,
	events EventSource,
	config Config,
	delivery ...DeliveryStatusSource,
) (*Server, error) {
	if engine == nil {
		return nil, fmt.Errorf("trading engine is required")
	}
	if config.EventBatchSize <= 0 || config.EventBatchSize > 1_000 {
		return nil, fmt.Errorf("event batch size must be in [1,1000]")
	}
	if config.EventPollEvery <= 0 {
		return nil, fmt.Errorf("event poll interval must be positive")
	}
	if len(delivery) > 1 {
		return nil, fmt.Errorf("at most one delivery status source is supported")
	}
	server := &Server{engine: engine, events: events, config: config}
	if len(delivery) == 1 {
		server.delivery = delivery[0]
	}
	return server, nil
}

func (s *Server) SubmitOrder(
	ctx context.Context,
	request *tradingv1.SubmitOrderRequest,
) (*tradingv1.CommandResult, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" || request.GetClientOrderId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id and client_order_id are required")
	}
	order, err := parseOrderRequest(market, request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	result, err := s.engine.Submit(ctx, order)
	if err != nil {
		return nil, mapError(err)
	}
	response, err := toCommandResult(market, result)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return response, nil
}

func (s *Server) CancelOrder(
	ctx context.Context,
	request *tradingv1.CancelOrderRequest,
) (*tradingv1.CommandResult, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" || request.GetRequestId() == "" || request.GetOrderId() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"account_id, request_id and order_id are required",
		)
	}
	order, found, err := s.engine.Order(domain.OrderID(request.GetOrderId()))
	if err != nil {
		return nil, mapError(err)
	}
	if !found {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if order.AccountID != domain.AccountID(request.GetAccountId()) {
		return nil, status.Error(codes.PermissionDenied, "order belongs to another account")
	}
	// Let the exchange perform the open-state check after its idempotency
	// lookup. A retry of a committed cancellation must return the original
	// result even though the projected order is now closed.
	result, err := s.engine.Cancel(ctx, domain.CancelOrder{
		RequestID: request.GetRequestId(),
		AccountID: domain.AccountID(request.GetAccountId()),
		OrderID:   domain.OrderID(request.GetOrderId()),
	})
	if err != nil {
		return nil, mapError(err)
	}
	if result.Status == domain.OrderStatusRejected {
		return nil, status.Error(codes.FailedPrecondition, "order is not open")
	}
	response, err := toCommandResult(market, result)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return response, nil
}

func (s *Server) GetOrder(
	_ context.Context,
	request *tradingv1.GetOrderRequest,
) (*tradingv1.Order, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" || request.GetOrderId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id and order_id are required")
	}
	order, found, err := s.engine.Order(domain.OrderID(request.GetOrderId()))
	if err != nil {
		return nil, mapError(err)
	}
	if !found {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	if order.AccountID != domain.AccountID(request.GetAccountId()) {
		return nil, status.Error(codes.PermissionDenied, "order belongs to another account")
	}
	response, err := toOrder(market, order)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return response, nil
}

func (s *Server) ListOrders(
	_ context.Context,
	request *tradingv1.ListOrdersRequest,
) (*tradingv1.ListOrdersResponse, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	orders, err := s.engine.Orders(domain.AccountID(request.GetAccountId()), request.GetOpenOnly())
	if err != nil {
		return nil, mapError(err)
	}
	orders = limitSlice(orders, request.GetLimit(), 100)
	response := &tradingv1.ListOrdersResponse{Orders: make([]*tradingv1.Order, 0, len(orders))}
	for _, order := range orders {
		converted, convertErr := toOrder(market, order)
		if convertErr != nil {
			return nil, status.Error(codes.Internal, convertErr.Error())
		}
		response.Orders = append(response.Orders, converted)
	}
	return response, nil
}

func (s *Server) ListTrades(
	_ context.Context,
	request *tradingv1.ListTradesRequest,
) (*tradingv1.ListTradesResponse, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	trades, err := s.engine.Trades(domain.AccountID(request.GetAccountId()))
	if err != nil {
		return nil, mapError(err)
	}
	trades = limitSlice(trades, request.GetLimit(), 100)
	response := &tradingv1.ListTradesResponse{Trades: make([]*tradingv1.Trade, 0, len(trades))}
	for _, trade := range trades {
		converted, convertErr := toTrade(market, trade)
		if convertErr != nil {
			return nil, status.Error(codes.Internal, convertErr.Error())
		}
		response.Trades = append(response.Trades, converted)
	}
	return response, nil
}

func (s *Server) GetBalances(
	_ context.Context,
	request *tradingv1.GetBalancesRequest,
) (*tradingv1.GetBalancesResponse, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetAccountId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	balances, err := s.engine.Balances(domain.AccountID(request.GetAccountId()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &tradingv1.GetBalancesResponse{
		Balances: make([]*tradingv1.Balance, 0, len(balances)),
	}
	for _, balance := range balances {
		scale, scaleErr := assetScale(market, balance.Asset)
		if scaleErr != nil {
			return nil, status.Error(codes.Internal, scaleErr.Error())
		}
		available, formatErr := decimal.Format(balance.Available, scale)
		if formatErr != nil {
			return nil, status.Error(codes.Internal, formatErr.Error())
		}
		held, formatErr := decimal.Format(balance.Held, scale)
		if formatErr != nil {
			return nil, status.Error(codes.Internal, formatErr.Error())
		}
		response.Balances = append(response.Balances, &tradingv1.Balance{
			Asset:     string(balance.Asset),
			Available: available,
			Held:      held,
		})
	}
	return response, nil
}

func (s *Server) GetOrderBook(
	_ context.Context,
	request *tradingv1.GetOrderBookRequest,
) (*tradingv1.OrderBook, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	levels := int(request.GetLevels())
	if levels <= 0 || levels > 200 {
		levels = 20
	}
	book, err := s.engine.Depth(levels)
	if err != nil {
		return nil, mapError(err)
	}
	response, err := toOrderBook(market, book)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return response, nil
}

func (s *Server) GetStatus(
	_ context.Context,
	request *tradingv1.GetStatusRequest,
) (*tradingv1.StatusResponse, error) {
	if _, err := s.market(request.GetMarketId()); err != nil {
		return nil, err
	}
	current := s.engine.Status()
	response := &tradingv1.StatusResponse{
		MarketId:        string(current.MarketID),
		State:           string(current.State),
		Sequence:        strconv.FormatUint(current.Sequence, 10),
		QueueDepth:      uint32(current.QueueDepth),
		RecoveryCount:   strconv.FormatUint(current.RecoveryCount, 10),
		LastError:       current.LastError,
		LastIncident:    current.LastIncident,
		LastIncidentAt:  current.LastIncidentAt,
		LastRecoveredAt: current.LastRecoveredAt,
		StateHash:       current.StateHash,
	}
	if s.delivery != nil {
		delivery := s.delivery.Status()
		response.OutboxState = delivery.State
		response.OutboxCheckpointSequence = strconv.FormatUint(
			delivery.Checkpoint.Sequence,
			10,
		)
		response.OutboxCheckpointEventIndex = delivery.Checkpoint.EventIndex
		response.OutboxLastError = delivery.LastError
		response.OutboxLastPublishedAt = formatOptionalTime(delivery.LastPublishedAt)
		response.OutboxLastCleanupAt = formatOptionalTime(delivery.LastCleanupAt)
	}
	return response, nil
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (s *Server) AdminFundVirtual(
	ctx context.Context,
	request *tradingv1.AdminFundVirtualRequest,
) (*tradingv1.CommandResult, error) {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return nil, err
	}
	if request.GetRequestId() == "" || request.GetAccountId() == "" || request.GetAsset() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"request_id, account_id and asset are required",
		)
	}
	asset := domain.Asset(request.GetAsset())
	scale, err := assetScale(market, asset)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	amount, err := decimal.Parse(request.GetAmount(), scale)
	if err != nil || amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be a positive exact decimal")
	}
	result, err := s.engine.Fund(ctx, domain.FundRequest{
		RequestID: request.GetRequestId(),
		AccountID: domain.AccountID(request.GetAccountId()),
		Asset:     asset,
		Amount:    amount,
	})
	if err != nil {
		return nil, mapError(err)
	}
	response, err := toCommandResult(market, result)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return response, nil
}

func (s *Server) SubscribeEvents(
	request *tradingv1.SubscribeEventsRequest,
	stream tradingv1.TradingService_SubscribeEventsServer,
) error {
	market, err := s.market(request.GetMarketId())
	if err != nil {
		return err
	}
	if s.events == nil {
		return status.Error(codes.Unavailable, "event source is unavailable")
	}
	cursor, err := parseCursor(request.GetCursorSequence(), request.GetCursorEventIndex())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	checkpoint := reliability.Checkpoint{
		Cursor: reliability.Cursor{
			MarketSequence: cursor.Sequence,
			EventIndex:     cursor.EventIndex,
		},
	}
	if cursor.Sequence > 0 {
		eventCount, found, readErr := s.events.BatchEventCount(
			stream.Context(),
			cursor.Sequence,
		)
		if readErr != nil {
			return status.Error(codes.Unavailable, "read trading cursor metadata")
		}
		if !found {
			return status.Error(codes.InvalidArgument, "cursor sequence is unavailable")
		}
		checkpoint.BatchEventCount = eventCount
	}
	reconciler, err := reliability.NewEventReconciler(checkpoint)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-stream.Context().Done():
			return mapError(stream.Context().Err())
		case <-timer.C:
		}
		events, readErr := s.events.EventsAfter(stream.Context(), cursor, s.config.EventBatchSize)
		if readErr != nil {
			return status.Error(codes.Unavailable, "read trading events")
		}
		head := reconciler.Checkpoint().Cursor
		envelopes := make([]reliability.EventEnvelope, 0, len(events))
		for _, stored := range events {
			if stored.MarketID != market.ID {
				return status.Error(codes.Internal, "event source returned another market")
			}
			eventCursor := reliability.Cursor{
				MarketSequence: stored.Cursor.Sequence,
				EventIndex:     stored.Cursor.EventIndex,
			}
			if cursorAfter(eventCursor, head) {
				head = eventCursor
			}
			envelopes = append(envelopes, reliability.EventEnvelope{
				Cursor:          eventCursor,
				BatchEventCount: stored.BatchEventCount,
				Event:           stored.Event,
			})
		}
		var (
			convertErr error
			sendErr    error
		)
		report, reconcileErr := reconciler.Reconcile(
			head,
			envelopes,
			func(event reliability.EventEnvelope) error {
				converted, err := toEvent(market, event.Event)
				if err != nil {
					convertErr = err
					return err
				}
				sendErr = stream.Send(&tradingv1.EventEnvelope{
					MarketId:   string(market.ID),
					Sequence:   strconv.FormatUint(event.MarketSequence, 10),
					EventIndex: event.EventIndex,
					Event:      converted,
				})
				return sendErr
			},
		)
		if convertErr != nil {
			return status.Error(codes.Internal, convertErr.Error())
		}
		if sendErr != nil {
			return mapError(sendErr)
		}
		if reconcileErr != nil {
			return status.Error(codes.DataLoss, "trading event reconciliation failed")
		}
		cursor = Cursor{
			Sequence:   report.End.MarketSequence,
			EventIndex: report.End.EventIndex,
		}
		if len(events) == s.config.EventBatchSize {
			timer.Reset(0)
		} else {
			timer.Reset(s.config.EventPollEvery)
		}
	}
}

func cursorAfter(left, right reliability.Cursor) bool {
	return left.MarketSequence > right.MarketSequence ||
		(left.MarketSequence == right.MarketSequence &&
			left.EventIndex > right.EventIndex)
}

func (s *Server) market(requested string) (domain.Market, error) {
	market, err := s.engine.Market()
	if err != nil {
		return domain.Market{}, mapError(err)
	}
	if requested == "" || domain.MarketID(requested) != market.ID {
		return domain.Market{}, status.Error(codes.InvalidArgument, "unknown market_id")
	}
	return market, nil
}

func parseCursor(sequence string, eventIndex uint32) (Cursor, error) {
	if sequence == "" {
		if eventIndex != 0 {
			return Cursor{}, fmt.Errorf("event index requires a sequence")
		}
		return Cursor{}, nil
	}
	value, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor_sequence must be an unsigned integer")
	}
	return Cursor{Sequence: value, EventIndex: eventIndex}, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	case errors.Is(err, domain.ErrInvalidMarket),
		errors.Is(err, domain.ErrInvalidOrder),
		errors.Is(err, domain.ErrInvalidRequest),
		errors.Is(err, domain.ErrArithmeticOverflow):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, exchange.ErrOrderNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, exchange.ErrOrderOwnerMismatch):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, exchange.ErrOrderNotOpen):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ledger.ErrInsufficientBalance):
		return status.Error(codes.FailedPrecondition, "insufficient virtual balance")
	case errors.Is(err, tradingruntime.ErrQueueFull):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, tradingruntime.ErrRecoveryInProgress):
		return status.Error(codes.Unavailable, "recovery_in_progress")
	case errors.Is(err, tradingruntime.ErrUnavailable),
		errors.Is(err, tradingruntime.ErrClosed),
		errors.Is(err, exchange.ErrPersistence):
		return status.Error(codes.Unavailable, "trading service unavailable")
	default:
		return status.Error(codes.Internal, "internal trading error")
	}
}

func limitSlice[T any](values []T, requested uint32, fallback int) []T {
	limit := int(requested)
	if limit <= 0 || limit > 1_000 {
		limit = fallback
	}
	if len(values) > limit {
		return values[:limit]
	}
	return values
}
