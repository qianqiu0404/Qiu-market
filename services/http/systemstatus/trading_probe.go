package systemstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

const (
	tradingStatusPath    = "/api/v1/trading/markets/BTC-USDT/status"
	tradingOrderBookPath = "/api/v1/trading/markets/BTC-USDT/orderbook?levels=20"
)

type TradingStatus struct {
	State                 *string `json:"state"`
	OutboxState           *string `json:"outbox_state"`
	OutboxCheckpoint      *string `json:"outbox_checkpoint_sequence"`
	OutboxLastError       *string `json:"outbox_last_error"`
	OutboxLastPublishedAt *string `json:"outbox_last_published_at"`
}

type PriceLevel struct {
	Price      string `json:"price"`
	Quantity   string `json:"quantity"`
	OrderCount int    `json:"order_count"`
}

type OrderBook struct {
	Bids *[]PriceLevel `json:"bids"`
	Asks *[]PriceLevel `json:"asks"`
}

type TradingProbeResult struct {
	ProbedAt       time.Time
	Status         *TradingStatus
	StatusError    error
	OrderBook      *OrderBook
	OrderBookError error
}

type TradingProbe interface {
	Probe(context.Context) TradingProbeResult
}

type HandlerTradingProbe struct {
	handler http.Handler
	now     func() time.Time
}

func NewHandlerTradingProbe(handler http.Handler) *HandlerTradingProbe {
	return &HandlerTradingProbe{handler: handler, now: time.Now}
}

func (p *HandlerTradingProbe) Probe(ctx context.Context) TradingProbeResult {
	result := TradingProbeResult{ProbedAt: p.now().UTC()}
	if p == nil || p.handler == nil {
		result.StatusError = fmt.Errorf("trading handler is unavailable")
		result.OrderBookError = fmt.Errorf("trading handler is unavailable")
		return result
	}

	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		var status TradingStatus
		result.StatusError = p.readJSON(ctx, tradingStatusPath, &status)
		if result.StatusError == nil {
			result.Status = &status
		}
	}()
	go func() {
		defer wait.Done()
		var orderBook OrderBook
		result.OrderBookError = p.readJSON(ctx, tradingOrderBookPath, &orderBook)
		if result.OrderBookError == nil {
			result.OrderBook = &orderBook
		}
	}()
	wait.Wait()
	return result
}

func (p *HandlerTradingProbe) readJSON(ctx context.Context, path string, target any) error {
	request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	response := httptest.NewRecorder()
	p.handler.ServeHTTP(response, request)
	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
		return fmt.Errorf("trading probe returned HTTP %d", response.Code)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode trading probe response: %w", err)
	}
	return nil
}
