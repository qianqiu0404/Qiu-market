package exchange

import (
	"errors"
	"fmt"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/ledger"
	"github.com/the-web3/s78-market-services/trading/orderbook"
)

func (s *state) applySubmit(command domain.Command) (domain.Result, error) {
	if command.Submit == nil {
		return domain.Result{}, fmt.Errorf("submit command payload is required")
	}
	request := *command.Submit
	if err := request.Validate(s.market); err != nil {
		return domain.Result{}, err
	}
	order := domain.Order{
		ID:                   domain.OrderID(fmt.Sprintf("O-%020d", command.Sequence)),
		ClientOrderID:        request.ClientOrderID,
		AccountID:            request.AccountID,
		MarketID:             s.market.ID,
		Side:                 request.Side,
		Type:                 request.Type,
		TimeInForce:          request.TimeInForce,
		PostOnly:             request.PostOnly,
		Price:                request.Price,
		OriginalQuantity:     request.Quantity,
		RemainingQuantity:    request.Quantity,
		OriginalQuoteBudget:  request.QuoteBudget,
		RemainingQuoteBudget: request.QuoteBudget,
		Status:               domain.OrderStatusReceived,
		AcceptedSequence:     command.Sequence,
		LastSequence:         command.Sequence,
	}

	if request.PostOnly && s.book.WouldCross(request.Side, request.Price) {
		return s.rejectOrder(command.Sequence, order, "post_only_would_cross"), nil
	}
	if request.TimeInForce == domain.TimeInForceFOK {
		fillable, err := s.book.CanFillFOK(request.AccountID, request.Side, request.Price, request.Quantity)
		if err != nil {
			return domain.Result{}, err
		}
		if !fillable {
			return s.rejectOrder(command.Sequence, order, "fok_not_fillable"), nil
		}
	}

	reserveAsset, reserveAmount, err := s.reserveFor(order)
	if err != nil {
		return domain.Result{}, err
	}
	order.HeldAsset = reserveAsset
	order.HeldAmount = reserveAmount
	if err := s.ledger.Hold(
		fmt.Sprintf("hold:%020d", command.Sequence),
		"order-hold:"+string(order.ID),
		order.AccountID,
		reserveAsset,
		reserveAmount,
	); err != nil {
		if errors.Is(err, ledger.ErrInsufficientBalance) {
			order.HeldAsset = ""
			order.HeldAmount = 0
			return s.rejectOrder(command.Sequence, order, "insufficient_balance"), nil
		}
		return domain.Result{}, fmt.Errorf("hold funds for order %s: %w", order.ID, err)
	}

	events := []domain.Event{{
		Sequence:             command.Sequence,
		Index:                1,
		Type:                 domain.EventOrderAccepted,
		AccountID:            order.AccountID,
		OrderID:              order.ID,
		ClientOrderID:        order.ClientOrderID,
		Status:               domain.OrderStatusReceived,
		Side:                 order.Side,
		Price:                order.Price,
		Quantity:             order.OriginalQuantity,
		QuoteAmount:          order.OriginalQuoteBudget,
		RemainingQuoteBudget: remainingQuoteBudgetFor(order),
	}}

	matchResult, err := s.book.Match(&order)
	if err != nil {
		return domain.Result{}, fmt.Errorf("match order %s: %w", order.ID, err)
	}
	// Match mutates the incoming order to its final state before settlement is
	// journaled. Lifecycle events must instead carry the state after each fill,
	// otherwise a multi-level taker reports the same final remainder on every
	// trade and the durable projection correctly rejects the batch.
	eventRemainingQuantity := request.Quantity
	eventRemainingQuoteBudget := request.QuoteBudget
	for fillIndex, fill := range matchResult.Fills {
		trade, settleErr := s.settleFill(command.Sequence, fillIndex+1, order, fill)
		if settleErr != nil {
			return domain.Result{}, settleErr
		}
		if err := consumeOrderHold(&order, fill); err != nil {
			return domain.Result{}, err
		}

		maker := s.orders[fill.MakerOrderID]
		maker.RemainingQuantity = fill.MakerRemaining
		maker.FilledQuantity, err = domain.CheckedAdd(maker.FilledQuantity, fill.Quantity)
		if err != nil {
			return domain.Result{}, fmt.Errorf("update maker filled quantity: %w", err)
		}
		maker.SpentQuote, err = domain.CheckedAdd(maker.SpentQuote, fill.QuoteAmount)
		if err != nil {
			return domain.Result{}, fmt.Errorf("update maker spent quote: %w", err)
		}
		maker.LastSequence = command.Sequence
		if maker.RemainingQuantity == 0 {
			maker.Status = domain.OrderStatusFilled
		} else {
			maker.Status = domain.OrderStatusPartiallyFilled
		}
		if err := consumeOrderHold(&maker, fill); err != nil {
			return domain.Result{}, err
		}
		if err := s.releaseExcessBuyHold(command.Sequence, fillIndex+1, &maker); err != nil {
			return domain.Result{}, err
		}
		if maker.Status == domain.OrderStatusFilled {
			if maker.HeldAmount != 0 {
				return domain.Result{}, fmt.Errorf("filled maker order %s retains held amount %d", maker.ID, maker.HeldAmount)
			}
			maker.HeldAsset = ""
		} else if err := s.book.Update(maker); err != nil {
			return domain.Result{}, fmt.Errorf("update resting maker %s: %w", maker.ID, err)
		}
		s.orders[maker.ID] = maker
		s.trades = append(s.trades, trade)
		var eventQuoteBudget *int64
		if order.Type == domain.OrderTypeMarket && order.Side == domain.SideBuy {
			if eventRemainingQuoteBudget < fill.QuoteAmount {
				return domain.Result{}, fmt.Errorf("event remaining quote budget became negative")
			}
			eventRemainingQuoteBudget -= fill.QuoteAmount
			remaining := eventRemainingQuoteBudget
			eventQuoteBudget = &remaining
		} else {
			if eventRemainingQuantity < fill.Quantity {
				return domain.Result{}, fmt.Errorf("event remaining quantity became negative")
			}
			eventRemainingQuantity -= fill.Quantity
		}

		events = appendEvent(events, domain.Event{
			Sequence:             command.Sequence,
			Type:                 domain.EventTradeExecuted,
			OrderID:              order.ID,
			Status:               domain.OrderStatusPartiallyFilled,
			Price:                fill.Price,
			Quantity:             fill.Quantity,
			Remaining:            eventRemainingQuantity,
			RemainingQuoteBudget: eventQuoteBudget,
			QuoteAmount:          fill.QuoteAmount,
			Trade:                &trade,
		})
		if maker.Status == domain.OrderStatusFilled {
			events = appendEvent(events, domain.Event{
				Sequence:  command.Sequence,
				Type:      domain.EventOrderFilled,
				AccountID: maker.AccountID,
				OrderID:   maker.ID,
				Status:    maker.Status,
				Side:      maker.Side,
				Price:     maker.Price,
				Quantity:  maker.OriginalQuantity,
			})
		}
	}

	if matchResult.StopReason == orderbook.StopSelfTrade {
		events = appendEvent(events, domain.Event{
			Sequence:  command.Sequence,
			Type:      domain.EventSelfTradePrevented,
			AccountID: order.AccountID,
			OrderID:   order.ID,
			Status:    domain.OrderStatusCanceled,
			Reason:    "cancel_taker",
		})
	}

	shouldRest := order.Type == domain.OrderTypeLimit &&
		order.TimeInForce == domain.TimeInForceGTC &&
		order.RemainingQuantity > 0 &&
		matchResult.StopReason != orderbook.StopSelfTrade

	desiredHold := int64(0)
	switch {
	case shouldRest:
		if order.FilledQuantity > 0 {
			order.Status = domain.OrderStatusPartiallyFilled
		} else {
			order.Status = domain.OrderStatusOpen
		}
		if order.Side == domain.SideBuy {
			desiredHold, err = s.market.QuoteAmountCeil(order.Price, order.RemainingQuantity)
			if err != nil {
				return domain.Result{}, fmt.Errorf("calculate remaining buy hold: %w", err)
			}
		} else {
			desiredHold = order.RemainingQuantity
		}
	case order.Type == domain.OrderTypeMarket && order.Side == domain.SideBuy:
		if len(matchResult.Fills) > 0 && matchResult.StopReason == orderbook.StopBudgetExhausted {
			order.Status = domain.OrderStatusFilled
		} else {
			order.Status = domain.OrderStatusCanceled
		}
	case order.RemainingQuantity == 0:
		order.Status = domain.OrderStatusFilled
	default:
		order.Status = domain.OrderStatusCanceled
	}

	if order.HeldAmount < desiredHold {
		return domain.Result{}, fmt.Errorf("order %s current hold %d below required %d", order.ID, order.HeldAmount, desiredHold)
	}
	releaseAmount := order.HeldAmount - desiredHold
	if releaseAmount > 0 {
		if err := s.ledger.Release(
			fmt.Sprintf("release:%020d", command.Sequence),
			"order-release:"+string(order.ID),
			order.AccountID,
			reserveAsset,
			releaseAmount,
		); err != nil {
			return domain.Result{}, fmt.Errorf("release funds for order %s: %w", order.ID, err)
		}
		order.HeldAmount -= releaseAmount
	}
	if order.HeldAmount == 0 {
		order.HeldAsset = ""
	}

	if shouldRest {
		if err := s.book.Add(order); err != nil {
			return domain.Result{}, fmt.Errorf("rest order %s: %w", order.ID, err)
		}
		events = appendEvent(events, domain.Event{
			Sequence:      command.Sequence,
			Type:          domain.EventOrderRested,
			AccountID:     order.AccountID,
			OrderID:       order.ID,
			ClientOrderID: order.ClientOrderID,
			Status:        order.Status,
			Side:          order.Side,
			Price:         order.Price,
			Quantity:      order.OriginalQuantity,
			Remaining:     order.RemainingQuantity,
		})
	} else if order.Status == domain.OrderStatusFilled {
		events = appendEvent(events, domain.Event{
			Sequence:             command.Sequence,
			Type:                 domain.EventOrderFilled,
			AccountID:            order.AccountID,
			OrderID:              order.ID,
			ClientOrderID:        order.ClientOrderID,
			Status:               order.Status,
			Side:                 order.Side,
			Price:                order.Price,
			Quantity:             order.FilledQuantity,
			QuoteAmount:          order.SpentQuote,
			RemainingQuoteBudget: remainingQuoteBudgetFor(order),
		})
	} else {
		reason := "unfilled_remainder"
		if matchResult.StopReason == orderbook.StopSelfTrade {
			reason = "self_trade_prevented"
		}
		events = appendEvent(events, domain.Event{
			Sequence:             command.Sequence,
			Type:                 domain.EventOrderCanceled,
			AccountID:            order.AccountID,
			OrderID:              order.ID,
			ClientOrderID:        order.ClientOrderID,
			Status:               order.Status,
			Side:                 order.Side,
			Price:                order.Price,
			Quantity:             order.FilledQuantity,
			Remaining:            order.RemainingQuantity,
			QuoteAmount:          order.SpentQuote,
			RemainingQuoteBudget: remainingQuoteBudgetFor(order),
			Reason:               reason,
		})
	}
	s.orders[order.ID] = order
	return domain.Result{
		Sequence: command.Sequence,
		OrderID:  order.ID,
		Status:   order.Status,
		Events:   events,
	}, nil
}

func (s *state) applyCancel(command domain.Command) (domain.Result, error) {
	if command.Cancel == nil {
		return domain.Result{}, fmt.Errorf("cancel command payload is required")
	}
	request := *command.Cancel
	if err := request.Validate(); err != nil {
		return domain.Result{}, err
	}
	order, exists := s.orders[request.OrderID]
	if !exists {
		return cancelRejected(command.Sequence, request, ErrOrderNotFound.Error()), nil
	}
	if order.AccountID != request.AccountID {
		return cancelRejected(command.Sequence, request, ErrOrderOwnerMismatch.Error()), nil
	}
	if !order.IsOpen() {
		return cancelRejected(command.Sequence, request, ErrOrderNotOpen.Error()), nil
	}
	if _, err := s.book.Cancel(order.ID); err != nil {
		return domain.Result{}, fmt.Errorf("remove canceled order %s: %w", order.ID, err)
	}

	var (
		releaseAsset  domain.Asset
		releaseAmount int64
	)
	if order.Side == domain.SideBuy {
		releaseAsset = s.market.QuoteAsset
		releaseAmount = order.HeldAmount
	} else {
		releaseAsset = s.market.BaseAsset
		releaseAmount = order.HeldAmount
	}
	if releaseAmount <= 0 || order.HeldAsset != releaseAsset {
		return domain.Result{}, fmt.Errorf("order %s has invalid held funds", order.ID)
	}
	if err := s.ledger.Release(
		fmt.Sprintf("cancel-release:%020d", command.Sequence),
		"order-cancel:"+string(order.ID),
		order.AccountID,
		releaseAsset,
		releaseAmount,
	); err != nil {
		return domain.Result{}, fmt.Errorf("release canceled order %s: %w", order.ID, err)
	}
	order.Status = domain.OrderStatusCanceled
	order.LastSequence = command.Sequence
	order.HeldAsset = ""
	order.HeldAmount = 0
	s.orders[order.ID] = order
	event := domain.Event{
		Sequence:      command.Sequence,
		Index:         1,
		Type:          domain.EventOrderCanceled,
		AccountID:     order.AccountID,
		OrderID:       order.ID,
		ClientOrderID: order.ClientOrderID,
		Status:        order.Status,
		Side:          order.Side,
		Price:         order.Price,
		Quantity:      order.FilledQuantity,
		Remaining:     order.RemainingQuantity,
		QuoteAmount:   order.SpentQuote,
		Reason:        "user_requested",
	}
	return domain.Result{
		Sequence: command.Sequence,
		OrderID:  order.ID,
		Status:   order.Status,
		Events:   []domain.Event{event},
	}, nil
}

func (s *state) rejectOrder(sequence uint64, order domain.Order, reason string) domain.Result {
	order.Status = domain.OrderStatusRejected
	order.RejectReason = reason
	order.LastSequence = sequence
	order.HeldAsset = ""
	order.HeldAmount = 0
	s.orders[order.ID] = order
	event := domain.Event{
		Sequence:             sequence,
		Index:                1,
		Type:                 domain.EventOrderRejected,
		AccountID:            order.AccountID,
		OrderID:              order.ID,
		ClientOrderID:        order.ClientOrderID,
		Status:               order.Status,
		Side:                 order.Side,
		Price:                order.Price,
		Quantity:             order.OriginalQuantity,
		QuoteAmount:          order.OriginalQuoteBudget,
		RemainingQuoteBudget: remainingQuoteBudgetFor(order),
		Reason:               reason,
	}
	return domain.Result{
		Sequence: sequence,
		OrderID:  order.ID,
		Status:   order.Status,
		Events:   []domain.Event{event},
	}
}

func remainingQuoteBudgetFor(order domain.Order) *int64 {
	if order.Type != domain.OrderTypeMarket || order.Side != domain.SideBuy {
		return nil
	}
	remaining := order.RemainingQuoteBudget
	return &remaining
}

func cancelRejected(sequence uint64, request domain.CancelOrder, reason string) domain.Result {
	event := domain.Event{
		Sequence:  sequence,
		Index:     1,
		Type:      domain.EventCancelRejected,
		AccountID: request.AccountID,
		OrderID:   request.OrderID,
		Status:    domain.OrderStatusRejected,
		Reason:    reason,
	}
	return domain.Result{
		Sequence: sequence,
		OrderID:  request.OrderID,
		Status:   domain.OrderStatusRejected,
		Events:   []domain.Event{event},
	}
}

func (s *state) reserveFor(order domain.Order) (domain.Asset, int64, error) {
	if order.Side == domain.SideSell {
		return s.market.BaseAsset, order.RemainingQuantity, nil
	}
	if order.Type == domain.OrderTypeMarket {
		return s.market.QuoteAsset, order.RemainingQuoteBudget, nil
	}
	amount, err := s.market.QuoteAmountCeil(order.Price, order.RemainingQuantity)
	if err != nil {
		return "", 0, fmt.Errorf("calculate buy reserve: %w", err)
	}
	return s.market.QuoteAsset, amount, nil
}

func consumeOrderHold(order *domain.Order, fill orderbook.RawFill) error {
	if order == nil {
		return fmt.Errorf("order is required")
	}
	consumed := fill.Quantity
	if order.Side == domain.SideBuy {
		consumed = fill.QuoteAmount
	}
	if consumed < 0 || order.HeldAmount < consumed {
		return fmt.Errorf("order %s hold became negative: held=%d consumed=%d", order.ID, order.HeldAmount, consumed)
	}
	order.HeldAmount -= consumed
	return nil
}

func (s *state) releaseExcessBuyHold(sequence uint64, fillIndex int, order *domain.Order) error {
	if order == nil || order.Side != domain.SideBuy {
		return nil
	}
	desired := int64(0)
	var err error
	if order.RemainingQuantity > 0 {
		desired, err = s.market.QuoteAmountCeil(order.Price, order.RemainingQuantity)
		if err != nil {
			return fmt.Errorf("calculate maker hold for order %s: %w", order.ID, err)
		}
	}
	if order.HeldAmount < desired {
		return fmt.Errorf("maker order %s hold %d below required %d", order.ID, order.HeldAmount, desired)
	}
	release := order.HeldAmount - desired
	if release == 0 {
		return nil
	}
	if err := s.ledger.Release(
		fmt.Sprintf("maker-release:%020d:%04d", sequence, fillIndex),
		"maker-rounding-release:"+string(order.ID),
		order.AccountID,
		s.market.QuoteAsset,
		release,
	); err != nil {
		return fmt.Errorf("release maker hold for order %s: %w", order.ID, err)
	}
	order.HeldAmount = desired
	return nil
}

func (s *state) settleFill(sequence uint64, fillIndex int, taker domain.Order, fill orderbook.RawFill) (domain.Trade, error) {
	maker, exists := s.orders[fill.MakerOrderID]
	if !exists {
		return domain.Trade{}, fmt.Errorf("maker order %s is absent from order index", fill.MakerOrderID)
	}
	var (
		buyer         domain.Order
		seller        domain.Order
		buyerRole     domain.LiquidityRole
		sellerRole    domain.LiquidityRole
		buyerRateBPS  int64
		sellerRateBPS int64
	)
	if maker.Side == domain.SideBuy {
		buyer, seller = maker, taker
		buyerRole, sellerRole = domain.LiquidityRoleMaker, domain.LiquidityRoleTaker
		buyerRateBPS, sellerRateBPS = s.market.MakerFeeBPS, s.market.TakerFeeBPS
	} else {
		buyer, seller = taker, maker
		buyerRole, sellerRole = domain.LiquidityRoleTaker, domain.LiquidityRoleMaker
		buyerRateBPS, sellerRateBPS = s.market.TakerFeeBPS, s.market.MakerFeeBPS
	}
	buyerFeeAmount, err := domain.FeeAmount(fill.Quantity, buyerRateBPS)
	if err != nil {
		return domain.Trade{}, fmt.Errorf("calculate buyer fee: %w", err)
	}
	sellerFeeAmount, err := domain.FeeAmount(fill.QuoteAmount, sellerRateBPS)
	if err != nil {
		return domain.Trade{}, fmt.Errorf("calculate seller fee: %w", err)
	}
	buyerNet := fill.Quantity - buyerFeeAmount
	sellerNet := fill.QuoteAmount - sellerFeeAmount
	if buyerNet <= 0 || sellerNet <= 0 {
		return domain.Trade{}, fmt.Errorf("fees consume the entire trade settlement")
	}

	tradeID := domain.TradeID(fmt.Sprintf("T-%020d-%04d", sequence, fillIndex))
	entries := []ledger.Entry{
		{Account: ledger.UserHeld(buyer.AccountID), Asset: s.market.QuoteAsset, Amount: -fill.QuoteAmount},
		{Account: ledger.UserAvailable(seller.AccountID), Asset: s.market.QuoteAsset, Amount: sellerNet},
		{Account: ledger.UserHeld(seller.AccountID), Asset: s.market.BaseAsset, Amount: -fill.Quantity},
		{Account: ledger.UserAvailable(buyer.AccountID), Asset: s.market.BaseAsset, Amount: buyerNet},
	}
	if sellerFeeAmount > 0 {
		entries = append(entries, ledger.Entry{
			Account: ledger.PlatformFee(s.market.QuoteAsset),
			Asset:   s.market.QuoteAsset,
			Amount:  sellerFeeAmount,
		})
	}
	if buyerFeeAmount > 0 {
		entries = append(entries, ledger.Entry{
			Account: ledger.PlatformFee(s.market.BaseAsset),
			Asset:   s.market.BaseAsset,
			Amount:  buyerFeeAmount,
		})
	}
	if err := s.ledger.Post(ledger.Transaction{
		ID:        "trade:" + string(tradeID),
		Reference: "matched-trade:" + string(tradeID),
		Entries:   entries,
	}); err != nil {
		return domain.Trade{}, fmt.Errorf("settle trade %s: %w", tradeID, err)
	}

	return domain.Trade{
		ID:              tradeID,
		MarketID:        s.market.ID,
		Price:           fill.Price,
		Quantity:        fill.Quantity,
		QuoteAmount:     fill.QuoteAmount,
		MakerOrderID:    maker.ID,
		TakerOrderID:    taker.ID,
		MakerAccountID:  maker.AccountID,
		TakerAccountID:  taker.AccountID,
		BuyerAccountID:  buyer.AccountID,
		SellerAccountID: seller.AccountID,
		BuyerFee: domain.Fee{
			AccountID: buyer.AccountID,
			Asset:     s.market.BaseAsset,
			Amount:    buyerFeeAmount,
			RateBPS:   buyerRateBPS,
			Role:      buyerRole,
		},
		SellerFee: domain.Fee{
			AccountID: seller.AccountID,
			Asset:     s.market.QuoteAsset,
			Amount:    sellerFeeAmount,
			RateBPS:   sellerRateBPS,
			Role:      sellerRole,
		},
	}, nil
}
