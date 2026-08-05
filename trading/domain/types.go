package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
)

type Asset string
type MarketID string
type AccountID string
type OrderID string
type TradeID string

type Side uint8

const (
	SideBuy Side = iota + 1
	SideSell
)

func (s Side) String() string {
	switch s {
	case SideBuy:
		return "buy"
	case SideSell:
		return "sell"
	default:
		return "unknown"
	}
}

type OrderType uint8

const (
	OrderTypeLimit OrderType = iota + 1
	OrderTypeMarket
)

func (t OrderType) String() string {
	switch t {
	case OrderTypeLimit:
		return "limit"
	case OrderTypeMarket:
		return "market"
	default:
		return "unknown"
	}
}

type TimeInForce uint8

const (
	TimeInForceGTC TimeInForce = iota + 1
	TimeInForceIOC
	TimeInForceFOK
)

func (t TimeInForce) String() string {
	switch t {
	case TimeInForceGTC:
		return "gtc"
	case TimeInForceIOC:
		return "ioc"
	case TimeInForceFOK:
		return "fok"
	default:
		return "unknown"
	}
}

type OrderStatus uint8

const (
	OrderStatusReceived OrderStatus = iota + 1
	OrderStatusRejected
	OrderStatusOpen
	OrderStatusPartiallyFilled
	OrderStatusFilled
	OrderStatusCanceled
)

func (s OrderStatus) String() string {
	switch s {
	case OrderStatusReceived:
		return "received"
	case OrderStatusRejected:
		return "rejected"
	case OrderStatusOpen:
		return "open"
	case OrderStatusPartiallyFilled:
		return "partially_filled"
	case OrderStatusFilled:
		return "filled"
	case OrderStatusCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

type LiquidityRole uint8

const (
	LiquidityRoleMaker LiquidityRole = iota + 1
	LiquidityRoleTaker
)

func (r LiquidityRole) String() string {
	switch r {
	case LiquidityRoleMaker:
		return "maker"
	case LiquidityRoleTaker:
		return "taker"
	default:
		return "unknown"
	}
}

type CommandKind uint8

const (
	CommandKindFund CommandKind = iota + 1
	CommandKindSubmitOrder
	CommandKindCancelOrder
)

func (k CommandKind) String() string {
	switch k {
	case CommandKindFund:
		return "fund"
	case CommandKindSubmitOrder:
		return "submit_order"
	case CommandKindCancelOrder:
		return "cancel_order"
	default:
		return "unknown"
	}
}

type EventType string

const (
	EventAccountFunded      EventType = "account_funded"
	EventOrderAccepted      EventType = "order_accepted"
	EventOrderRejected      EventType = "order_rejected"
	EventTradeExecuted      EventType = "trade_executed"
	EventOrderRested        EventType = "order_rested"
	EventOrderFilled        EventType = "order_filled"
	EventOrderCanceled      EventType = "order_canceled"
	EventCancelRejected     EventType = "cancel_rejected"
	EventSelfTradePrevented EventType = "self_trade_prevented"
)

var (
	ErrInvalidMarket       = errors.New("invalid market")
	ErrInvalidOrder        = errors.New("invalid order")
	ErrInvalidRequest      = errors.New("invalid request")
	ErrArithmeticOverflow  = errors.New("integer arithmetic overflow")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different payload")
)

type Market struct {
	ID                 MarketID `json:"id"`
	BaseAsset          Asset    `json:"base_asset"`
	QuoteAsset         Asset    `json:"quote_asset"`
	BaseScale          int64    `json:"base_scale"`
	QuoteScale         int64    `json:"quote_scale"`
	PriceTick          int64    `json:"price_tick"`
	QuantityStep       int64    `json:"quantity_step"`
	MinQuantity        int64    `json:"min_quantity"`
	MinNotional        int64    `json:"min_notional"`
	MakerFeeBPS        int64    `json:"maker_fee_bps"`
	TakerFeeBPS        int64    `json:"taker_fee_bps"`
	ConfigurationEpoch uint64   `json:"configuration_epoch"`
}

func (m Market) Validate() error {
	if m.ID == "" || m.BaseAsset == "" || m.QuoteAsset == "" || m.BaseAsset == m.QuoteAsset {
		return fmt.Errorf("%w: market identity and distinct assets are required", ErrInvalidMarket)
	}
	if !isPowerOfTen(m.BaseScale) || !isPowerOfTen(m.QuoteScale) {
		return fmt.Errorf("%w: base and quote scales must be positive powers of ten", ErrInvalidMarket)
	}
	if m.PriceTick <= 0 || m.QuantityStep <= 0 || m.MinQuantity <= 0 || m.MinNotional <= 0 {
		return fmt.Errorf("%w: tick, step, minimum quantity, and minimum notional must be positive", ErrInvalidMarket)
	}
	if m.MinQuantity%m.QuantityStep != 0 {
		return fmt.Errorf("%w: minimum quantity must align to quantity step", ErrInvalidMarket)
	}
	if m.MakerFeeBPS < 0 || m.MakerFeeBPS >= 10_000 || m.TakerFeeBPS < 0 || m.TakerFeeBPS >= 10_000 {
		return fmt.Errorf("%w: fee rates must be in [0, 10000)", ErrInvalidMarket)
	}
	if m.ConfigurationEpoch == 0 {
		return fmt.Errorf("%w: configuration epoch must be positive", ErrInvalidMarket)
	}
	return nil
}

// DefaultBTCUSDTMarket returns the first vertical-slice market. Price values are
// quote atoms per one whole base unit, quantities are base atoms, and notionals
// are quote atoms.
func DefaultBTCUSDTMarket() Market {
	return Market{
		ID:                 "BTC-USDT",
		BaseAsset:          "BTC",
		QuoteAsset:         "USDT",
		BaseScale:          100_000_000,
		QuoteScale:         1_000_000,
		PriceTick:          10_000,
		QuantityStep:       100,
		MinQuantity:        1_000,
		MinNotional:        5_000_000,
		MakerFeeBPS:        10,
		TakerFeeBPS:        20,
		ConfigurationEpoch: 1,
	}
}

// QuoteAmountFloor converts a price and base quantity into quote atoms,
// rounding down. Settlement always uses this rule.
func (m Market) QuoteAmountFloor(price, quantity int64) (int64, error) {
	return CheckedMulDivFloor(price, quantity, m.BaseScale)
}

// QuoteAmountCeil converts a price and base quantity into quote atoms,
// rounding up. Buy-side reservations always use this rule.
func (m Market) QuoteAmountCeil(price, quantity int64) (int64, error) {
	return CheckedMulDivCeil(price, quantity, m.BaseScale)
}

// AffordableQuantity returns the base atoms that a quote budget can buy at a
// price, rounded down to the market quantity step.
func (m Market) AffordableQuantity(quoteBudget, price int64) (int64, error) {
	quantity, err := CheckedMulDivFloor(quoteBudget, m.BaseScale, price)
	if err != nil {
		return 0, err
	}
	return quantity - quantity%m.QuantityStep, nil
}

type FundRequest struct {
	RequestID string    `json:"request_id"`
	AccountID AccountID `json:"account_id"`
	Asset     Asset     `json:"asset"`
	Amount    int64     `json:"amount"`
}

func (r FundRequest) Validate(m Market) error {
	if r.RequestID == "" || r.AccountID == "" || r.Amount <= 0 {
		return fmt.Errorf("%w: funding request id, account id, and positive amount are required", ErrInvalidRequest)
	}
	if r.Asset != m.BaseAsset && r.Asset != m.QuoteAsset {
		return fmt.Errorf("%w: asset %q is not supported by market %q", ErrInvalidRequest, r.Asset, m.ID)
	}
	return nil
}

type NewOrder struct {
	ClientOrderID string      `json:"client_order_id"`
	AccountID     AccountID   `json:"account_id"`
	Side          Side        `json:"side"`
	Type          OrderType   `json:"type"`
	TimeInForce   TimeInForce `json:"time_in_force"`
	Price         int64       `json:"price"`
	Quantity      int64       `json:"quantity"`
	QuoteBudget   int64       `json:"quote_budget"`
	PostOnly      bool        `json:"post_only"`
}

func (o NewOrder) Validate(m Market) error {
	if o.ClientOrderID == "" || o.AccountID == "" {
		return fmt.Errorf("%w: client order id and account id are required", ErrInvalidOrder)
	}
	if o.Side != SideBuy && o.Side != SideSell {
		return fmt.Errorf("%w: unsupported side", ErrInvalidOrder)
	}
	switch o.Type {
	case OrderTypeLimit:
		if o.Price <= 0 || o.Price%m.PriceTick != 0 {
			return fmt.Errorf("%w: limit price must be positive and aligned to price tick", ErrInvalidOrder)
		}
		if o.Quantity < m.MinQuantity || o.Quantity%m.QuantityStep != 0 {
			return fmt.Errorf("%w: limit quantity must meet minimum and align to quantity step", ErrInvalidOrder)
		}
		if o.QuoteBudget != 0 {
			return fmt.Errorf("%w: quote budget is only valid for market buys", ErrInvalidOrder)
		}
		if o.TimeInForce != TimeInForceGTC && o.TimeInForce != TimeInForceIOC && o.TimeInForce != TimeInForceFOK {
			return fmt.Errorf("%w: unsupported time in force", ErrInvalidOrder)
		}
		if o.PostOnly && o.TimeInForce != TimeInForceGTC {
			return fmt.Errorf("%w: post only requires limit GTC", ErrInvalidOrder)
		}
		stepNotional, err := m.QuoteAmountFloor(o.Price, m.QuantityStep)
		if err != nil || stepNotional <= 0 {
			return fmt.Errorf("%w: price and quantity step cannot produce a positive quote atom", ErrInvalidOrder)
		}
		notional, err := m.QuoteAmountFloor(o.Price, o.Quantity)
		if err != nil {
			return fmt.Errorf("%w: limit notional: %v", ErrInvalidOrder, err)
		}
		if notional < m.MinNotional {
			return fmt.Errorf("%w: notional is below market minimum", ErrInvalidOrder)
		}
	case OrderTypeMarket:
		if o.Price != 0 || o.PostOnly || o.TimeInForce != TimeInForceIOC {
			return fmt.Errorf("%w: market orders require zero price, IOC, and post_only=false", ErrInvalidOrder)
		}
		switch o.Side {
		case SideBuy:
			if o.QuoteBudget < m.MinNotional || o.Quantity != 0 {
				return fmt.Errorf("%w: market buy requires quote budget and zero base quantity", ErrInvalidOrder)
			}
		case SideSell:
			if o.Quantity < m.MinQuantity || o.Quantity%m.QuantityStep != 0 || o.QuoteBudget != 0 {
				return fmt.Errorf("%w: market sell requires aligned base quantity and zero quote budget", ErrInvalidOrder)
			}
		}
	default:
		return fmt.Errorf("%w: unsupported order type", ErrInvalidOrder)
	}
	return nil
}

type CancelOrder struct {
	RequestID string    `json:"request_id"`
	AccountID AccountID `json:"account_id"`
	OrderID   OrderID   `json:"order_id"`
}

func (o CancelOrder) Validate() error {
	if o.RequestID == "" || o.AccountID == "" || o.OrderID == "" {
		return fmt.Errorf("%w: cancel request id, account id, and order id are required", ErrInvalidRequest)
	}
	return nil
}

type Order struct {
	ID                   OrderID     `json:"id"`
	ClientOrderID        string      `json:"client_order_id"`
	AccountID            AccountID   `json:"account_id"`
	MarketID             MarketID    `json:"market_id"`
	Side                 Side        `json:"side"`
	Type                 OrderType   `json:"type"`
	TimeInForce          TimeInForce `json:"time_in_force"`
	PostOnly             bool        `json:"post_only"`
	Price                int64       `json:"price"`
	OriginalQuantity     int64       `json:"original_quantity"`
	RemainingQuantity    int64       `json:"remaining_quantity"`
	FilledQuantity       int64       `json:"filled_quantity"`
	OriginalQuoteBudget  int64       `json:"original_quote_budget"`
	RemainingQuoteBudget int64       `json:"remaining_quote_budget"`
	SpentQuote           int64       `json:"spent_quote"`
	HeldAsset            Asset       `json:"held_asset,omitempty"`
	HeldAmount           int64       `json:"held_amount,omitempty"`
	Status               OrderStatus `json:"status"`
	AcceptedSequence     uint64      `json:"accepted_sequence"`
	LastSequence         uint64      `json:"last_sequence"`
	RejectReason         string      `json:"reject_reason,omitempty"`
}

func (o Order) IsOpen() bool {
	return o.Status == OrderStatusOpen || o.Status == OrderStatusPartiallyFilled
}

type Fee struct {
	AccountID AccountID     `json:"account_id"`
	Asset     Asset         `json:"asset"`
	Amount    int64         `json:"amount"`
	RateBPS   int64         `json:"rate_bps"`
	Role      LiquidityRole `json:"role"`
}

type Trade struct {
	ID              TradeID   `json:"id"`
	MarketID        MarketID  `json:"market_id"`
	Price           int64     `json:"price"`
	Quantity        int64     `json:"quantity"`
	QuoteAmount     int64     `json:"quote_amount"`
	MakerOrderID    OrderID   `json:"maker_order_id"`
	TakerOrderID    OrderID   `json:"taker_order_id"`
	MakerAccountID  AccountID `json:"maker_account_id"`
	TakerAccountID  AccountID `json:"taker_account_id"`
	BuyerAccountID  AccountID `json:"buyer_account_id"`
	SellerAccountID AccountID `json:"seller_account_id"`
	BuyerFee        Fee       `json:"buyer_fee"`
	SellerFee       Fee       `json:"seller_fee"`
}

type Event struct {
	Sequence      uint64      `json:"sequence"`
	Index         uint32      `json:"index"`
	Type          EventType   `json:"type"`
	AccountID     AccountID   `json:"account_id,omitempty"`
	OrderID       OrderID     `json:"order_id,omitempty"`
	ClientOrderID string      `json:"client_order_id,omitempty"`
	Status        OrderStatus `json:"status,omitempty"`
	Side          Side        `json:"side,omitempty"`
	Price         int64       `json:"price,omitempty"`
	Quantity      int64       `json:"quantity,omitempty"`
	Remaining     int64       `json:"remaining,omitempty"`
	// RemainingQuoteBudget is the V1 event field for Market Buy budget state.
	// Emitters added by the V1 implementation set it; historical events are
	// reconstructed from the accepted quote budget minus cumulative quote amounts.
	RemainingQuoteBudget *int64 `json:"remaining_quote_budget,omitempty"`
	QuoteAmount          int64  `json:"quote_amount,omitempty"`
	Asset                Asset  `json:"asset,omitempty"`
	Amount               int64  `json:"amount,omitempty"`
	Reason               string `json:"reason,omitempty"`
	Trade                *Trade `json:"trade,omitempty"`
}

type Result struct {
	Sequence uint64      `json:"sequence"`
	OrderID  OrderID     `json:"order_id,omitempty"`
	Status   OrderStatus `json:"status,omitempty"`
	Events   []Event     `json:"events"`
}

type Command struct {
	Sequence    uint64         `json:"sequence"`
	RequestID   string         `json:"request_id"`
	RequestKey  IdempotencyKey `json:"request_key"`
	Fingerprint string         `json:"fingerprint"`
	Kind        CommandKind    `json:"kind"`
	Fund        *FundRequest   `json:"fund,omitempty"`
	Submit      *NewOrder      `json:"submit,omitempty"`
	Cancel      *CancelOrder   `json:"cancel,omitempty"`
}

type IdempotencyKey struct {
	MarketID  MarketID    `json:"market_id"`
	AccountID AccountID   `json:"account_id"`
	Operation CommandKind `json:"operation"`
	RequestID string      `json:"request_id"`
}

func NewIdempotencyKey(marketID MarketID, accountID AccountID, operation CommandKind, requestID string) IdempotencyKey {
	return IdempotencyKey{
		MarketID:  marketID,
		AccountID: accountID,
		Operation: operation,
		RequestID: requestID,
	}
}

func (k IdempotencyKey) Validate() error {
	if k.MarketID == "" || k.AccountID == "" || k.RequestID == "" ||
		(k.Operation != CommandKindFund && k.Operation != CommandKindSubmitOrder && k.Operation != CommandKindCancelOrder) {
		return fmt.Errorf("%w: invalid idempotency scope", ErrInvalidRequest)
	}
	return nil
}

func (k IdempotencyKey) String() string {
	return fmt.Sprintf("%s\x1f%s\x1f%d\x1f%s", k.MarketID, k.AccountID, k.Operation, k.RequestID)
}

func Fingerprint(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal fingerprint payload: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func CheckedAdd(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrArithmeticOverflow
	}
	return a + b, nil
}

func CheckedMul(a, b int64) (int64, error) {
	return CheckedMulDivFloor(a, b, 1)
}

func CheckedMulDivFloor(a, b, denominator int64) (int64, error) {
	quotient, _, err := checkedMulDiv(a, b, denominator)
	return quotient, err
}

func CheckedMulDivCeil(a, b, denominator int64) (int64, error) {
	quotient, remainder, err := checkedMulDiv(a, b, denominator)
	if err != nil {
		return 0, err
	}
	if remainder == 0 {
		return quotient, nil
	}
	if quotient == math.MaxInt64 {
		return 0, ErrArithmeticOverflow
	}
	return quotient + 1, nil
}

func FeeAmount(amount, rateBPS int64) (int64, error) {
	if amount < 0 || rateBPS < 0 || rateBPS >= 10_000 {
		return 0, fmt.Errorf("%w: invalid fee operands", ErrArithmeticOverflow)
	}
	if amount == 0 || rateBPS == 0 {
		return 0, nil
	}
	return CheckedMulDivFloor(amount, rateBPS, 10_000)
}

func checkedMulDiv(a, b, denominator int64) (int64, uint64, error) {
	if a < 0 || b < 0 || denominator <= 0 {
		return 0, 0, fmt.Errorf("%w: mul-div requires non-negative operands and a positive denominator", ErrArithmeticOverflow)
	}
	high, low := bits.Mul64(uint64(a), uint64(b))
	divisor := uint64(denominator)
	if high >= divisor {
		return 0, 0, ErrArithmeticOverflow
	}
	quotient, remainder := bits.Div64(high, low, divisor)
	if quotient > math.MaxInt64 {
		return 0, 0, ErrArithmeticOverflow
	}
	return int64(quotient), remainder, nil
}

func isPowerOfTen(value int64) bool {
	if value <= 0 {
		return false
	}
	for value > 1 {
		if value%10 != 0 {
			return false
		}
		value /= 10
	}
	return true
}
