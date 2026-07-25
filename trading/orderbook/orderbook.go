package orderbook

import (
	"errors"
	"fmt"
	"sort"

	"github.com/the-web3/s78-market-services/trading/domain"
)

var (
	ErrDuplicateOrder   = errors.New("duplicate order")
	ErrOrderNotFound    = errors.New("order not found")
	ErrInvalidBookOrder = errors.New("invalid order for order book")
)

type StopReason string

const (
	StopInputExhausted  StopReason = "input_exhausted"
	StopNoLiquidity     StopReason = "no_liquidity"
	StopPriceLimit      StopReason = "price_limit"
	StopBudgetExhausted StopReason = "budget_exhausted"
	StopSelfTrade       StopReason = "self_trade_prevented"
)

type RawFill struct {
	MakerOrderID   domain.OrderID   `json:"maker_order_id"`
	MakerAccountID domain.AccountID `json:"maker_account_id"`
	MakerSide      domain.Side      `json:"maker_side"`
	MakerRemaining int64            `json:"maker_remaining"`
	Price          int64            `json:"price"`
	Quantity       int64            `json:"quantity"`
	QuoteAmount    int64            `json:"quote_amount"`
}

type MatchResult struct {
	Fills      []RawFill  `json:"fills"`
	StopReason StopReason `json:"stop_reason"`
}

type Snapshot struct {
	Bids []domain.Order `json:"bids"`
	Asks []domain.Order `json:"asks"`
}

type Book struct {
	market    domain.Market
	bids      map[int64][]domain.OrderID
	asks      map[int64][]domain.OrderID
	bidPrices []int64
	askPrices []int64
	orders    map[domain.OrderID]domain.Order
}

func New(market domain.Market) (*Book, error) {
	if err := market.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBookOrder, err)
	}
	return &Book{
		market: market,
		bids:   make(map[int64][]domain.OrderID),
		asks:   make(map[int64][]domain.OrderID),
		orders: make(map[domain.OrderID]domain.Order),
	}, nil
}

func (b *Book) Clone() (*Book, error) {
	return FromSnapshot(b.market, b.Snapshot())
}

func (b *Book) Add(order domain.Order) error {
	if order.ID == "" || order.Type != domain.OrderTypeLimit || order.TimeInForce != domain.TimeInForceGTC ||
		order.MarketID != b.market.ID || order.Price <= 0 || order.Price%b.market.PriceTick != 0 ||
		order.RemainingQuantity <= 0 || order.RemainingQuantity%b.market.QuantityStep != 0 ||
		!order.IsOpen() {
		return fmt.Errorf("%w: only open limit GTC orders with aligned remaining quantity may rest", ErrInvalidBookOrder)
	}
	if _, exists := b.orders[order.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateOrder, order.ID)
	}
	b.orders[order.ID] = order
	switch order.Side {
	case domain.SideBuy:
		if _, exists := b.bids[order.Price]; !exists {
			b.bidPrices = insertPrice(b.bidPrices, order.Price, true)
		}
		b.bids[order.Price] = append(b.bids[order.Price], order.ID)
	case domain.SideSell:
		if _, exists := b.asks[order.Price]; !exists {
			b.askPrices = insertPrice(b.askPrices, order.Price, false)
		}
		b.asks[order.Price] = append(b.asks[order.Price], order.ID)
	default:
		delete(b.orders, order.ID)
		return fmt.Errorf("%w: unsupported side", ErrInvalidBookOrder)
	}
	return nil
}

func (b *Book) Cancel(orderID domain.OrderID) (domain.Order, error) {
	order, exists := b.orders[orderID]
	if !exists {
		return domain.Order{}, fmt.Errorf("%w: %s", ErrOrderNotFound, orderID)
	}
	if err := b.remove(order); err != nil {
		return domain.Order{}, err
	}
	return order, nil
}

func (b *Book) WouldCross(side domain.Side, price int64) bool {
	switch side {
	case domain.SideBuy:
		return len(b.askPrices) > 0 && price >= b.askPrices[0]
	case domain.SideSell:
		return len(b.bidPrices) > 0 && price <= b.bidPrices[0]
	default:
		return false
	}
}

func (b *Book) CanFillFOK(accountID domain.AccountID, side domain.Side, limitPrice, quantity int64) (bool, error) {
	if quantity <= 0 {
		return false, nil
	}
	remaining := quantity
	var prices []int64
	var levels map[int64][]domain.OrderID
	switch side {
	case domain.SideBuy:
		prices, levels = b.askPrices, b.asks
	case domain.SideSell:
		prices, levels = b.bidPrices, b.bids
	default:
		return false, fmt.Errorf("%w: unsupported side", ErrInvalidBookOrder)
	}
	for _, price := range prices {
		if !crosses(side, domain.OrderTypeLimit, limitPrice, price) {
			break
		}
		for _, orderID := range levels[price] {
			maker := b.orders[orderID]
			if maker.AccountID == accountID {
				return false, nil
			}
			if maker.RemainingQuantity >= remaining {
				return true, nil
			}
			remaining -= maker.RemainingQuantity
		}
	}
	return false, nil
}

func (b *Book) Match(incoming *domain.Order) (MatchResult, error) {
	if incoming == nil || incoming.ID == "" {
		return MatchResult{}, fmt.Errorf("%w: incoming order is required", ErrInvalidBookOrder)
	}
	result := MatchResult{StopReason: StopNoLiquidity}
	for {
		maker, ok := b.bestOpposing(incoming.Side)
		if !ok {
			result.StopReason = StopNoLiquidity
			return result, nil
		}
		if !crosses(incoming.Side, incoming.Type, incoming.Price, maker.Price) {
			result.StopReason = StopPriceLimit
			return result, nil
		}
		if maker.AccountID == incoming.AccountID {
			result.StopReason = StopSelfTrade
			return result, nil
		}

		quantity, stop, err := b.executableQuantity(incoming, maker)
		if err != nil {
			return MatchResult{}, err
		}
		if quantity == 0 {
			result.StopReason = stop
			return result, nil
		}
		quoteAmount, err := b.market.QuoteAmountFloor(maker.Price, quantity)
		if err != nil {
			return MatchResult{}, fmt.Errorf("match quote amount: %w", err)
		}
		if quoteAmount <= 0 {
			return MatchResult{}, fmt.Errorf("match quote amount must be positive")
		}

		maker.RemainingQuantity -= quantity
		maker.FilledQuantity, err = domain.CheckedAdd(maker.FilledQuantity, quantity)
		if err != nil {
			return MatchResult{}, fmt.Errorf("update maker filled quantity: %w", err)
		}
		maker.SpentQuote, err = domain.CheckedAdd(maker.SpentQuote, quoteAmount)
		if err != nil {
			return MatchResult{}, fmt.Errorf("update maker spent quote: %w", err)
		}
		maker.LastSequence = incoming.AcceptedSequence
		if maker.RemainingQuantity == 0 {
			maker.Status = domain.OrderStatusFilled
			if err := b.remove(maker); err != nil {
				return MatchResult{}, err
			}
		} else {
			maker.Status = domain.OrderStatusPartiallyFilled
			b.orders[maker.ID] = maker
		}

		incoming.FilledQuantity, err = domain.CheckedAdd(incoming.FilledQuantity, quantity)
		if err != nil {
			return MatchResult{}, fmt.Errorf("update taker filled quantity: %w", err)
		}
		incoming.SpentQuote, err = domain.CheckedAdd(incoming.SpentQuote, quoteAmount)
		if err != nil {
			return MatchResult{}, fmt.Errorf("update taker spent quote: %w", err)
		}
		incoming.LastSequence = incoming.AcceptedSequence
		if incoming.Type == domain.OrderTypeMarket && incoming.Side == domain.SideBuy {
			incoming.RemainingQuoteBudget -= quoteAmount
		} else {
			incoming.RemainingQuantity -= quantity
		}
		result.Fills = append(result.Fills, RawFill{
			MakerOrderID:   maker.ID,
			MakerAccountID: maker.AccountID,
			MakerSide:      maker.Side,
			MakerRemaining: maker.RemainingQuantity,
			Price:          maker.Price,
			Quantity:       quantity,
			QuoteAmount:    quoteAmount,
		})

		if incoming.Type == domain.OrderTypeMarket && incoming.Side == domain.SideBuy {
			if incoming.RemainingQuoteBudget == 0 {
				result.StopReason = StopBudgetExhausted
				return result, nil
			}
			continue
		}
		if incoming.RemainingQuantity == 0 {
			result.StopReason = StopInputExhausted
			return result, nil
		}
	}
}

func (b *Book) Order(orderID domain.OrderID) (domain.Order, bool) {
	order, ok := b.orders[orderID]
	return order, ok
}

// Update replaces the mutable fields of an already-resting order without
// changing its FIFO position.
func (b *Book) Update(order domain.Order) error {
	current, exists := b.orders[order.ID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrOrderNotFound, order.ID)
	}
	if current.Side != order.Side || current.Price != order.Price || current.AccountID != order.AccountID ||
		current.AcceptedSequence != order.AcceptedSequence || !order.IsOpen() || order.RemainingQuantity <= 0 {
		return fmt.Errorf("%w: resting order identity or open state changed", ErrInvalidBookOrder)
	}
	b.orders[order.ID] = order
	return nil
}

func (b *Book) Snapshot() Snapshot {
	result := Snapshot{}
	for _, price := range b.bidPrices {
		for _, orderID := range b.bids[price] {
			result.Bids = append(result.Bids, b.orders[orderID])
		}
	}
	for _, price := range b.askPrices {
		for _, orderID := range b.asks[price] {
			result.Asks = append(result.Asks, b.orders[orderID])
		}
	}
	return result
}

func FromSnapshot(market domain.Market, snapshot Snapshot) (*Book, error) {
	book, err := New(market)
	if err != nil {
		return nil, err
	}
	for _, order := range snapshot.Bids {
		if order.Side != domain.SideBuy {
			return nil, fmt.Errorf("restore order book: bid %s has side %s", order.ID, order.Side)
		}
		if err := book.Add(order); err != nil {
			return nil, fmt.Errorf("restore bid %s: %w", order.ID, err)
		}
	}
	for _, order := range snapshot.Asks {
		if order.Side != domain.SideSell {
			return nil, fmt.Errorf("restore order book: ask %s has side %s", order.ID, order.Side)
		}
		if err := book.Add(order); err != nil {
			return nil, fmt.Errorf("restore ask %s: %w", order.ID, err)
		}
	}
	if err := book.Validate(); err != nil {
		return nil, err
	}
	return book, nil
}

func (b *Book) Validate() error {
	if !sort.SliceIsSorted(b.bidPrices, func(i, j int) bool { return b.bidPrices[i] > b.bidPrices[j] }) {
		return fmt.Errorf("bid prices are not strictly descending")
	}
	if !sort.SliceIsSorted(b.askPrices, func(i, j int) bool { return b.askPrices[i] < b.askPrices[j] }) {
		return fmt.Errorf("ask prices are not strictly ascending")
	}
	seen := make(map[domain.OrderID]struct{}, len(b.orders))
	check := func(side domain.Side, prices []int64, levels map[int64][]domain.OrderID) error {
		for _, price := range prices {
			queue := levels[price]
			if len(queue) == 0 {
				return fmt.Errorf("empty price level %d", price)
			}
			for _, orderID := range queue {
				if _, duplicate := seen[orderID]; duplicate {
					return fmt.Errorf("order %s occurs more than once", orderID)
				}
				order, ok := b.orders[orderID]
				if !ok {
					return fmt.Errorf("price level references missing order %s", orderID)
				}
				if order.Side != side || order.Price != price || !order.IsOpen() || order.RemainingQuantity <= 0 {
					return fmt.Errorf("order %s is inconsistent with its price level", orderID)
				}
				seen[orderID] = struct{}{}
			}
		}
		return nil
	}
	if err := check(domain.SideBuy, b.bidPrices, b.bids); err != nil {
		return err
	}
	if err := check(domain.SideSell, b.askPrices, b.asks); err != nil {
		return err
	}
	if len(seen) != len(b.orders) {
		return fmt.Errorf("order index contains %d orders but price levels contain %d", len(b.orders), len(seen))
	}
	return nil
}

func (b *Book) bestOpposing(side domain.Side) (domain.Order, bool) {
	switch side {
	case domain.SideBuy:
		if len(b.askPrices) == 0 {
			return domain.Order{}, false
		}
		queue := b.asks[b.askPrices[0]]
		return b.orders[queue[0]], true
	case domain.SideSell:
		if len(b.bidPrices) == 0 {
			return domain.Order{}, false
		}
		queue := b.bids[b.bidPrices[0]]
		return b.orders[queue[0]], true
	default:
		return domain.Order{}, false
	}
}

func (b *Book) executableQuantity(incoming *domain.Order, maker domain.Order) (int64, StopReason, error) {
	if incoming.Type == domain.OrderTypeMarket && incoming.Side == domain.SideBuy {
		affordable, err := b.market.AffordableQuantity(incoming.RemainingQuoteBudget, maker.Price)
		if err != nil {
			return 0, "", fmt.Errorf("calculate affordable quantity: %w", err)
		}
		if affordable == 0 {
			return 0, StopBudgetExhausted, nil
		}
		if maker.RemainingQuantity < affordable {
			return maker.RemainingQuantity, "", nil
		}
		return affordable, "", nil
	}
	if incoming.RemainingQuantity <= 0 {
		return 0, StopInputExhausted, nil
	}
	if maker.RemainingQuantity < incoming.RemainingQuantity {
		return maker.RemainingQuantity, "", nil
	}
	return incoming.RemainingQuantity, "", nil
}

func (b *Book) remove(order domain.Order) error {
	var levels map[int64][]domain.OrderID
	var prices *[]int64
	switch order.Side {
	case domain.SideBuy:
		levels, prices = b.bids, &b.bidPrices
	case domain.SideSell:
		levels, prices = b.asks, &b.askPrices
	default:
		return fmt.Errorf("%w: unsupported side", ErrInvalidBookOrder)
	}
	queue, exists := levels[order.Price]
	if !exists {
		return fmt.Errorf("remove order %s: missing price level", order.ID)
	}
	index := -1
	for i, orderID := range queue {
		if orderID == order.ID {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("remove order %s: missing from price level", order.ID)
	}
	queue = append(queue[:index], queue[index+1:]...)
	if len(queue) == 0 {
		delete(levels, order.Price)
		*prices = removePrice(*prices, order.Price)
	} else {
		levels[order.Price] = queue
	}
	delete(b.orders, order.ID)
	return nil
}

func crosses(side domain.Side, orderType domain.OrderType, incomingPrice, makerPrice int64) bool {
	if orderType == domain.OrderTypeMarket {
		return true
	}
	switch side {
	case domain.SideBuy:
		return incomingPrice >= makerPrice
	case domain.SideSell:
		return incomingPrice <= makerPrice
	default:
		return false
	}
}

func insertPrice(prices []int64, price int64, descending bool) []int64 {
	index := sort.Search(len(prices), func(i int) bool {
		if descending {
			return prices[i] <= price
		}
		return prices[i] >= price
	})
	if index < len(prices) && prices[index] == price {
		return prices
	}
	prices = append(prices, 0)
	copy(prices[index+1:], prices[index:])
	prices[index] = price
	return prices
}

func removePrice(prices []int64, price int64) []int64 {
	for i, candidate := range prices {
		if candidate == price {
			return append(prices[:i], prices[i+1:]...)
		}
	}
	return prices
}
