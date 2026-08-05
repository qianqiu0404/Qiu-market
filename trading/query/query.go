// Package query defines the read-only Trade Product V1 boundary between the
// PostgreSQL projections and the gRPC/HTTP transport. Implementations must be
// rebuildable from the trading event stream and immutable ledger journal.
package query

import (
	"context"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
)

type OrderScope string
type SourceKind string
type BalanceBucket string
type LedgerReason string

const (
	// OrderScopeOpen is exactly open + partially_filled. OrderScopeHistory is
	// exactly filled + canceled + rejected. The transient received state is not
	// queryable as an order projection.
	OrderScopeAll     OrderScope = "all"
	OrderScopeOpen    OrderScope = "open"
	OrderScopeHistory OrderScope = "history"

	SourceKindEvent SourceKind = "event"

	BalanceBucketAvailable BalanceBucket = "available"
	BalanceBucketHeld      BalanceBucket = "held"

	LedgerReasonVirtualFund     LedgerReason = "virtual_fund"
	LedgerReasonOrderHold       LedgerReason = "order_hold"
	LedgerReasonOrderRelease    LedgerReason = "order_release"
	LedgerReasonTradeSettlement LedgerReason = "trade_settlement"
	LedgerReasonOther           LedgerReason = "other"
)

type OrderFilter struct {
	Scope  OrderScope
	Status domain.OrderStatus
	Side   domain.Side
	Type   domain.OrderType
}

type OrderCursor struct {
	AcceptedSequence uint64
	OrderID          domain.OrderID
}

type OrderView struct {
	Order            domain.Order
	AverageFillPrice *int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type OrderPage struct {
	Orders     []OrderView
	NextCursor *OrderCursor
}

type TradeCursor struct {
	Sequence   uint64
	EventIndex uint32
	TradeID    domain.TradeID
}

type TradeFilter struct {
	Side domain.Side
}

// AccountTrade is deliberately scoped to one account. It must never contain
// the counterparty account or order identity.
type AccountTrade struct {
	ID            domain.TradeID
	MarketID      domain.MarketID
	OrderID       domain.OrderID
	Side          domain.Side
	LiquidityRole domain.LiquidityRole
	Price         int64
	Quantity      int64
	QuoteAmount   int64
	FeeAsset      domain.Asset
	FeeAmount     int64
	FeeRateBPS    int64
	Sequence      uint64
	EventIndex    uint32
	OccurredAt    time.Time
}

type TradePage struct {
	Trades     []AccountTrade
	NextCursor *TradeCursor
}

type TimelineCursor struct {
	Sequence      uint64
	EventIndex    uint32
	TimelineIndex uint32
}

type FeeView struct {
	Asset   domain.Asset
	Amount  int64
	RateBPS int64
	Role    domain.LiquidityRole
}

type BalanceEffect struct {
	Asset         domain.Asset
	Bucket        BalanceBucket
	Amount        int64
	Reason        LedgerReason
	TransactionID string
}

type OrderEvent struct {
	EventID              string
	MarketID             domain.MarketID
	OrderID              domain.OrderID
	Sequence             uint64
	EventIndex           uint32
	TimelineIndex        uint32
	SourceKind           SourceKind
	Type                 domain.EventType
	Status               domain.OrderStatus
	Quantity             *int64
	Price                *int64
	RemainingQuantity    *int64
	RemainingQuoteBudget *int64
	TradeID              domain.TradeID
	Fee                  *FeeView
	BalanceEffects       []BalanceEffect
	Reason               string
	OccurredAt           time.Time
}

type OrderEventPage struct {
	Events     []OrderEvent
	NextCursor *TimelineCursor
}

type LedgerCursor struct {
	Sequence      uint64
	TransactionID string
	EntryIndex    uint32
}

type LedgerFilter struct {
	Asset  domain.Asset
	Reason LedgerReason
}

type LedgerEntry struct {
	EntryID       string
	MarketID      domain.MarketID
	Sequence      uint64
	TransactionID string
	EntryIndex    uint32
	Asset         domain.Asset
	Bucket        BalanceBucket
	Amount        int64
	Reason        LedgerReason
	Reference     string
	OrderID       domain.OrderID
	TradeID       domain.TradeID
	OccurredAt    time.Time
}

type LedgerPage struct {
	Entries    []LedgerEntry
	NextCursor *LedgerCursor
}

// Reader is the only read-model dependency the V1 transport may consume.
// Account identity is always supplied by the authenticated server boundary.
type Reader interface {
	GetOrder(context.Context, domain.AccountID, domain.OrderID) (OrderView, bool, error)
	ListOrders(context.Context, domain.AccountID, OrderFilter, *OrderCursor, int) (OrderPage, error)
	ListAccountTrades(context.Context, domain.AccountID, TradeFilter, *TradeCursor, int) (TradePage, error)
	ListOrderEvents(context.Context, domain.AccountID, domain.OrderID, *TimelineCursor, int) (OrderEventPage, error)
	ListLedgerEntries(context.Context, domain.AccountID, LedgerFilter, *LedgerCursor, int) (LedgerPage, error)
}
