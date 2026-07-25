package server

import (
	"fmt"
	"strconv"

	"github.com/the-web3/s78-market-services/trading/decimal"
	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

func parseOrderRequest(
	market domain.Market,
	request *tradingv1.SubmitOrderRequest,
) (domain.NewOrder, error) {
	side, err := parseSide(request.GetSide())
	if err != nil {
		return domain.NewOrder{}, err
	}
	orderType, err := parseOrderType(request.GetType())
	if err != nil {
		return domain.NewOrder{}, err
	}
	timeInForce, err := parseTimeInForce(request.GetTimeInForce())
	if err != nil {
		return domain.NewOrder{}, err
	}
	price, err := parseOptionalDecimal(request.GetPrice(), market.QuoteScale)
	if err != nil {
		return domain.NewOrder{}, fmt.Errorf("price: %w", err)
	}
	quantity, err := parseOptionalDecimal(request.GetQuantity(), market.BaseScale)
	if err != nil {
		return domain.NewOrder{}, fmt.Errorf("quantity: %w", err)
	}
	quoteBudget, err := parseOptionalDecimal(request.GetQuoteBudget(), market.QuoteScale)
	if err != nil {
		return domain.NewOrder{}, fmt.Errorf("quote_budget: %w", err)
	}
	return domain.NewOrder{
		ClientOrderID: request.GetClientOrderId(),
		AccountID:     domain.AccountID(request.GetAccountId()),
		Side:          side,
		Type:          orderType,
		TimeInForce:   timeInForce,
		PostOnly:      request.GetPostOnly(),
		Price:         price,
		Quantity:      quantity,
		QuoteBudget:   quoteBudget,
	}, nil
}

func parseSide(value tradingv1.Side) (domain.Side, error) {
	switch value {
	case tradingv1.Side_SIDE_BUY:
		return domain.SideBuy, nil
	case tradingv1.Side_SIDE_SELL:
		return domain.SideSell, nil
	default:
		return 0, fmt.Errorf("side must be BUY or SELL")
	}
}

func parseOrderType(value tradingv1.OrderType) (domain.OrderType, error) {
	switch value {
	case tradingv1.OrderType_ORDER_TYPE_LIMIT:
		return domain.OrderTypeLimit, nil
	case tradingv1.OrderType_ORDER_TYPE_MARKET:
		return domain.OrderTypeMarket, nil
	default:
		return 0, fmt.Errorf("type must be LIMIT or MARKET")
	}
}

func parseTimeInForce(value tradingv1.TimeInForce) (domain.TimeInForce, error) {
	switch value {
	case tradingv1.TimeInForce_TIME_IN_FORCE_GTC:
		return domain.TimeInForceGTC, nil
	case tradingv1.TimeInForce_TIME_IN_FORCE_IOC:
		return domain.TimeInForceIOC, nil
	case tradingv1.TimeInForce_TIME_IN_FORCE_FOK:
		return domain.TimeInForceFOK, nil
	default:
		return 0, fmt.Errorf("time_in_force must be GTC, IOC or FOK")
	}
}

func parseOptionalDecimal(value string, scale int64) (int64, error) {
	if value == "" {
		return 0, nil
	}
	return decimal.Parse(value, scale)
}

func toCommandResult(
	market domain.Market,
	result domain.Result,
) (*tradingv1.CommandResult, error) {
	response := &tradingv1.CommandResult{
		Sequence: strconv.FormatUint(result.Sequence, 10),
		OrderId:  string(result.OrderID),
		Status:   result.Status.String(),
		Events:   make([]*tradingv1.Event, 0, len(result.Events)),
	}
	for _, event := range result.Events {
		converted, err := toEvent(market, event)
		if err != nil {
			return nil, err
		}
		response.Events = append(response.Events, converted)
	}
	return response, nil
}

func toOrder(market domain.Market, order domain.Order) (*tradingv1.Order, error) {
	price, err := decimal.Format(order.Price, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	originalQuantity, err := decimal.Format(order.OriginalQuantity, market.BaseScale)
	if err != nil {
		return nil, err
	}
	remainingQuantity, err := decimal.Format(order.RemainingQuantity, market.BaseScale)
	if err != nil {
		return nil, err
	}
	filledQuantity, err := decimal.Format(order.FilledQuantity, market.BaseScale)
	if err != nil {
		return nil, err
	}
	originalBudget, err := decimal.Format(order.OriginalQuoteBudget, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	remainingBudget, err := decimal.Format(order.RemainingQuoteBudget, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	spentQuote, err := decimal.Format(order.SpentQuote, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	heldAmount := "0"
	if order.HeldAsset != "" {
		scale, scaleErr := assetScale(market, order.HeldAsset)
		if scaleErr != nil {
			return nil, scaleErr
		}
		heldAmount, err = decimal.Format(order.HeldAmount, scale)
		if err != nil {
			return nil, err
		}
	}
	return &tradingv1.Order{
		Id:                   string(order.ID),
		ClientOrderId:        order.ClientOrderID,
		AccountId:            string(order.AccountID),
		MarketId:             string(order.MarketID),
		Side:                 order.Side.String(),
		Type:                 order.Type.String(),
		TimeInForce:          order.TimeInForce.String(),
		PostOnly:             order.PostOnly,
		Price:                price,
		OriginalQuantity:     originalQuantity,
		RemainingQuantity:    remainingQuantity,
		FilledQuantity:       filledQuantity,
		OriginalQuoteBudget:  originalBudget,
		RemainingQuoteBudget: remainingBudget,
		SpentQuote:           spentQuote,
		HeldAsset:            string(order.HeldAsset),
		HeldAmount:           heldAmount,
		Status:               order.Status.String(),
		AcceptedSequence:     strconv.FormatUint(order.AcceptedSequence, 10),
		LastSequence:         strconv.FormatUint(order.LastSequence, 10),
		RejectReason:         order.RejectReason,
	}, nil
}

func toTrade(market domain.Market, trade domain.Trade) (*tradingv1.Trade, error) {
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
	buyerFee, err := toFee(market, trade.BuyerFee)
	if err != nil {
		return nil, err
	}
	sellerFee, err := toFee(market, trade.SellerFee)
	if err != nil {
		return nil, err
	}
	return &tradingv1.Trade{
		Id:              string(trade.ID),
		MarketId:        string(trade.MarketID),
		Price:           price,
		Quantity:        quantity,
		QuoteAmount:     quoteAmount,
		MakerOrderId:    string(trade.MakerOrderID),
		TakerOrderId:    string(trade.TakerOrderID),
		MakerAccountId:  string(trade.MakerAccountID),
		TakerAccountId:  string(trade.TakerAccountID),
		BuyerAccountId:  string(trade.BuyerAccountID),
		SellerAccountId: string(trade.SellerAccountID),
		BuyerFee:        buyerFee,
		SellerFee:       sellerFee,
	}, nil
}

func toFee(market domain.Market, fee domain.Fee) (*tradingv1.Fee, error) {
	scale, err := assetScale(market, fee.Asset)
	if err != nil {
		return nil, err
	}
	amount, err := decimal.Format(fee.Amount, scale)
	if err != nil {
		return nil, err
	}
	return &tradingv1.Fee{
		AccountId: string(fee.AccountID),
		Asset:     string(fee.Asset),
		Amount:    amount,
		RateBps:   strconv.FormatInt(fee.RateBPS, 10),
		Role:      fee.Role.String(),
	}, nil
}

func toEvent(market domain.Market, event domain.Event) (*tradingv1.Event, error) {
	price, err := decimal.Format(event.Price, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	quantity, err := decimal.Format(event.Quantity, market.BaseScale)
	if err != nil {
		return nil, err
	}
	remaining, err := decimal.Format(event.Remaining, market.BaseScale)
	if err != nil {
		return nil, err
	}
	quoteAmount, err := decimal.Format(event.QuoteAmount, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	amount := "0"
	if event.Asset != "" {
		scale, scaleErr := assetScale(market, event.Asset)
		if scaleErr != nil {
			return nil, scaleErr
		}
		amount, err = decimal.Format(event.Amount, scale)
		if err != nil {
			return nil, err
		}
	}
	var trade *tradingv1.Trade
	if event.Trade != nil {
		trade, err = toTrade(market, *event.Trade)
		if err != nil {
			return nil, err
		}
	}
	return &tradingv1.Event{
		Sequence:      strconv.FormatUint(event.Sequence, 10),
		Index:         event.Index,
		Type:          string(event.Type),
		AccountId:     string(event.AccountID),
		OrderId:       string(event.OrderID),
		ClientOrderId: event.ClientOrderID,
		Status:        event.Status.String(),
		Side:          event.Side.String(),
		Price:         price,
		Quantity:      quantity,
		Remaining:     remaining,
		QuoteAmount:   quoteAmount,
		Asset:         string(event.Asset),
		Amount:        amount,
		Reason:        event.Reason,
		Trade:         trade,
	}, nil
}

func toOrderBook(
	market domain.Market,
	book exchange.OrderBookView,
) (*tradingv1.OrderBook, error) {
	response := &tradingv1.OrderBook{
		MarketId: string(book.MarketID),
		Sequence: strconv.FormatUint(book.Sequence, 10),
		Bids:     make([]*tradingv1.PriceLevel, 0, len(book.Bids)),
		Asks:     make([]*tradingv1.PriceLevel, 0, len(book.Asks)),
	}
	for _, level := range book.Bids {
		converted, err := toPriceLevel(market, level)
		if err != nil {
			return nil, err
		}
		response.Bids = append(response.Bids, converted)
	}
	for _, level := range book.Asks {
		converted, err := toPriceLevel(market, level)
		if err != nil {
			return nil, err
		}
		response.Asks = append(response.Asks, converted)
	}
	return response, nil
}

func toPriceLevel(
	market domain.Market,
	level exchange.PriceLevelView,
) (*tradingv1.PriceLevel, error) {
	price, err := decimal.Format(level.Price, market.QuoteScale)
	if err != nil {
		return nil, err
	}
	quantity, err := decimal.Format(level.Quantity, market.BaseScale)
	if err != nil {
		return nil, err
	}
	return &tradingv1.PriceLevel{
		Price:      price,
		Quantity:   quantity,
		OrderCount: uint32(level.OrderCount),
	}, nil
}

func assetScale(market domain.Market, asset domain.Asset) (int64, error) {
	switch asset {
	case market.BaseAsset:
		return market.BaseScale, nil
	case market.QuoteAsset:
		return market.QuoteScale, nil
	default:
		return 0, fmt.Errorf("asset %q is not supported by market %q", asset, market.ID)
	}
}
