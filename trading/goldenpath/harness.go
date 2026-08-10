// Package goldenpath wires the production trading transports to deterministic,
// process-local dependencies for the browser golden path. It never listens on
// a non-loopback address and never shares a database or external market feed.
package goldenpath

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/decimal"
	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/httpapi"
	"github.com/the-web3/s78-market-services/trading/ledger"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	rpcserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

const (
	BuyerAccount          domain.AccountID = "golden:buyer"
	SellerAccount         domain.AccountID = "golden:seller"
	Price                                  = "60000.00"
	Quantity                               = "0.01000000"
	DefaultFrontendOrigin                  = "http://127.0.0.1:4176"
)

type Harness struct {
	market               domain.Market
	store                *store.Memory
	runner               *tradingruntime.MarketRunner
	grpcServer           *grpc.Server
	grpcConn             *grpc.ClientConn
	client               tradingv1.TradingServiceClient
	handler              http.Handler
	fillMu               sync.Mutex
	fillResult           *tradingv1.CommandResult
	fillForClientOrderID string
	observedAt           time.Time
	funded               bool
	orderReplays         atomic.Uint64
	fillReplays          atomic.Uint64
}

type Running struct {
	URL       string
	Harness   *Harness
	server    *http.Server
	listener  net.Listener
	closeOnce sync.Once
}

type State struct {
	MarketID       string                      `json:"market_id"`
	RuntimeState   string                      `json:"runtime_state"`
	Sequence       uint64                      `json:"sequence"`
	FactCount      uint64                      `json:"fact_count"`
	BuyerOrders    int                         `json:"buyer_orders"`
	BuyerTrades    int                         `json:"buyer_trades"`
	BuyerBalances  map[string]FormattedBalance `json:"buyer_balances"`
	SellerBalances map[string]FormattedBalance `json:"seller_balances"`
	PlatformFees   map[string]string           `json:"platform_fees"`
	ReplayEvidence ReplayEvidence              `json:"replay_evidence"`
	Ledger         LedgerEvidence              `json:"ledger"`
	JournalSums    map[string]string           `json:"journal_sums"`
}

type ReplayEvidence struct {
	OrderReplays uint64 `json:"order_replays"`
	FillReplays  uint64 `json:"fill_replays"`
}

type LedgerEvidence struct {
	TransactionCount int  `json:"transaction_count"`
	EntryCount       int  `json:"entry_count"`
	Balanced         bool `json:"balanced"`
}

type FormattedBalance struct {
	Available string `json:"available"`
	Held      string `json:"held"`
}

func Start(ctx context.Context, bindAddress string) (*Running, error) {
	listener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		return nil, err
	}
	if !listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		_ = listener.Close()
		return nil, fmt.Errorf("golden harness requires an IP loopback bind address")
	}
	address := listener.Addr().String()
	frontendOrigin := os.Getenv("QIU_GOLDEN_FRONTEND_ORIGIN")
	if frontendOrigin == "" {
		frontendOrigin = DefaultFrontendOrigin
	}
	harness, err := newHarness(ctx, address, []string{frontendOrigin, "http://" + address})
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	running := &Running{URL: "http://" + address, Harness: harness, listener: listener}
	running.server = &http.Server{Handler: harness.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = running.server.Serve(listener) }()
	return running, nil
}

func newHarness(ctx context.Context, httpBind string, origins []string) (*Harness, error) {
	market := domain.DefaultBTCUSDTMarket()
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(ctx, market, memory, memory, tradingruntime.DefaultConfig())
	if err != nil {
		return nil, err
	}
	h := &Harness{market: market, store: memory, runner: runner, observedAt: time.Now().UTC()}
	cleanup := true
	defer func() {
		if cleanup {
			_ = h.Close(context.Background())
		}
	}()

	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	cursors := rpcserver.CursorConfig{Current: rpcserver.CursorKeyConfig{KeyID: "golden-v1", Secret: []byte("golden-path-cursor-key-32-bytes!!")}}
	rpcService, err := rpcserver.New(runner, nil, rpcserver.Config{
		EventBatchSize: 100, EventPollEvery: 50 * time.Millisecond,
		Queries: &memoryReader{store: memory, market: market}, Cursors: cursors,
	})
	if err != nil {
		_ = grpcListener.Close()
		return nil, err
	}
	h.grpcServer = grpc.NewServer()
	tradingv1.RegisterTradingServiceServer(h.grpcServer, rpcService)
	go func() { _ = h.grpcServer.Serve(grpcListener) }()
	h.grpcConn, err = grpc.NewClient(grpcListener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	h.client = tradingv1.NewTradingServiceClient(h.grpcConn)

	if _, err = h.client.AdminFundVirtual(ctx, &tradingv1.AdminFundVirtualRequest{MarketId: string(market.ID), RequestId: "golden-fund-buyer", AccountId: string(BuyerAccount), Asset: string(market.QuoteAsset), Amount: "1000.000000"}); err != nil {
		return nil, fmt.Errorf("fund buyer: %w", err)
	}
	if _, err = h.client.AdminFundVirtual(ctx, &tradingv1.AdminFundVirtualRequest{MarketId: string(market.ID), RequestId: "golden-fund-seller", AccountId: string(SellerAccount), Asset: string(market.BaseAsset), Amount: Quantity}); err != nil {
		return nil, fmt.Errorf("fund seller: %w", err)
	}
	h.funded = true

	tickets, err := auth.NewTicketManager(time.Minute)
	if err != nil {
		return nil, err
	}
	oauthStates, err := auth.NewOAuthStateManager(10 * time.Minute)
	if err != nil {
		return nil, err
	}
	httpConfig := httpapi.DefaultConfig()
	httpConfig.BindAddress = httpBind
	httpConfig.AllowedOrigins = origins
	httpConfig.AllowedLogin = "golden-local"
	httpConfig.LocalMode = true
	httpConfig.LocalAccountID = string(BuyerAccount)
	httpConfig.SecureCookies = false
	httpConfig.RecoveryGate = false
	httpConfig.WriteLimit = 100
	production, err := httpapi.New(h.client, newMemorySessionStore(), tickets, oauthStates, nil, httpConfig)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/trading/", h.trackOrderReplays(production.Handler()))
	mux.HandleFunc("POST /api/v2/get_asset_dashboard", h.assetDashboard)
	mux.HandleFunc("POST /api/v2/get_asset_markets", h.assetMarkets)
	mux.HandleFunc("POST /api/v2/get_asset_venues", h.assetMarkets)
	mux.HandleFunc("POST /api/v1/get_klines", h.klines)
	mux.HandleFunc("GET /__golden/ready", h.ready)
	mux.HandleFunc("POST /__golden/fill", h.fill)
	mux.HandleFunc("GET /__golden/state", h.stateHTTP)
	mux.HandleFunc("GET /__golden/market-data/reference", h.referencePrice)
	h.handler = loopbackOnly(mux)
	cleanup = false
	return h, nil
}

func (h *Harness) Handler() http.Handler { return h.handler }

func (h *Harness) Close(ctx context.Context) error {
	if h.grpcConn != nil {
		_ = h.grpcConn.Close()
	}
	if h.grpcServer != nil {
		h.grpcServer.Stop()
	}
	if h.runner != nil {
		return h.runner.Close(ctx)
	}
	return nil
}

func (r *Running) Close(ctx context.Context) error {
	var result error
	r.closeOnce.Do(func() {
		serverErr := r.server.Shutdown(ctx)
		harnessErr := r.Harness.Close(ctx)
		result = errors.Join(serverErr, harnessErr)
	})
	return result
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Harness) ready(w http.ResponseWriter, _ *http.Request) {
	status := h.runner.Status()
	ready := status.State == tradingruntime.StateReady && h.funded
	statusCode := http.StatusOK
	if !ready {
		statusCode = http.StatusServiceUnavailable
	}
	writeJSON(w, statusCode, map[string]any{"ready": ready, "funded": h.funded, "market_id": status.MarketID, "sequence": status.Sequence})
}

func (h *Harness) fill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RestingClientOrderID string `json:"resting_client_order_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.RestingClientOrderID == "" || decoder.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resting_client_order_id is required"})
		return
	}
	h.fillMu.Lock()
	defer h.fillMu.Unlock()
	if h.fillResult != nil {
		if h.fillForClientOrderID != body.RestingClientOrderID {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "fill control is bound to a different resting order"})
			return
		}
		h.fillReplays.Add(1)
	} else {
		priceAtoms, priceErr := decimal.Parse(Price, h.market.QuoteScale)
		quantityAtoms, quantityErr := decimal.Parse(Quantity, h.market.BaseScale)
		expectedHeld, heldErr := h.market.QuoteAmountCeil(priceAtoms, quantityAtoms)
		orders, ordersErr := h.runner.Orders(BuyerAccount, true)
		if priceErr != nil || quantityErr != nil || heldErr != nil || ordersErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to validate resting order"})
			return
		}
		var resting *domain.Order
		for index := range orders {
			if orders[index].ClientOrderID == body.RestingClientOrderID {
				resting = &orders[index]
				break
			}
		}
		if !isFixedRestingBuyerOrder(resting, h.market, priceAtoms, quantityAtoms, expectedHeld) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "buyer order is not the fixed open resting order"})
			return
		}
		result, err := h.client.SubmitOrder(r.Context(), &tradingv1.SubmitOrderRequest{
			MarketId: string(h.market.ID), AccountId: string(SellerAccount), ClientOrderId: "golden-seller-fill-v1",
			Side: tradingv1.Side_SIDE_SELL, Type: tradingv1.OrderType_ORDER_TYPE_LIMIT,
			TimeInForce: tradingv1.TimeInForce_TIME_IN_FORCE_GTC, Price: Price, Quantity: Quantity,
		})
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if !isFilledResult(result) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "seller order did not completely fill the resting buyer order"})
			return
		}
		h.fillResult = result
		h.fillForClientOrderID = body.RestingClientOrderID
	}
	writeJSON(w, http.StatusOK, map[string]any{"order_id": h.fillResult.GetOrderId(), "status": h.fillResult.GetStatus(), "sequence": h.fillResult.GetSequence()})
}

func isFixedRestingBuyerOrder(order *domain.Order, market domain.Market, price, quantity, held int64) bool {
	return order != nil && order.AccountID == BuyerAccount && order.Status == domain.OrderStatusOpen &&
		order.Side == domain.SideBuy && order.Type == domain.OrderTypeLimit && order.Price == price &&
		order.OriginalQuantity == quantity && order.RemainingQuantity == quantity &&
		order.HeldAsset == market.QuoteAsset && order.HeldAmount == held
}

func isFilledResult(result *tradingv1.CommandResult) bool {
	return result != nil && result.GetStatus() == domain.OrderStatusFilled.String()
}

func (h *Harness) stateHTTP(w http.ResponseWriter, r *http.Request) {
	state, err := h.State(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (h *Harness) State(ctx context.Context) (State, error) {
	status := h.runner.Status()
	orders, err := h.runner.Orders(BuyerAccount, false)
	if err != nil {
		return State{}, err
	}
	trades, err := h.runner.Trades(BuyerAccount)
	if err != nil {
		return State{}, err
	}
	state := State{MarketID: string(h.market.ID), RuntimeState: string(status.State), Sequence: status.Sequence,
		FactCount: h.store.RecordCount(), BuyerOrders: len(orders), BuyerTrades: len(trades),
		BuyerBalances: map[string]FormattedBalance{}, SellerBalances: map[string]FormattedBalance{}, PlatformFees: map[string]string{},
		ReplayEvidence: ReplayEvidence{OrderReplays: h.orderReplays.Load(), FillReplays: h.fillReplays.Load()},
		Ledger:         LedgerEvidence{Balanced: true}, JournalSums: map[string]string{}}
	for account, destination := range map[domain.AccountID]map[string]FormattedBalance{BuyerAccount: state.BuyerBalances, SellerAccount: state.SellerBalances} {
		balances, balanceErr := h.runner.Balances(account)
		if balanceErr != nil {
			return State{}, balanceErr
		}
		for _, balance := range balances {
			scale := h.market.BaseScale
			if balance.Asset == h.market.QuoteAsset {
				scale = h.market.QuoteScale
			}
			available, _ := decimal.Format(balance.Available, scale)
			held, _ := decimal.Format(balance.Held, scale)
			destination[string(balance.Asset)] = FormattedBalance{Available: available, Held: held}
		}
	}
	records, err := h.store.RecordsAfter(ctx, 0)
	if err != nil {
		return State{}, err
	}
	fees := map[domain.Asset]int64{}
	sums := map[domain.Asset]int64{}
	for _, record := range records {
		for _, tx := range record.Journal {
			state.Ledger.TransactionCount++
			for _, entry := range tx.Entries {
				state.Ledger.EntryCount++
				sums[entry.Asset] += entry.Amount
				if entry.Account == ledger.PlatformFee(entry.Asset) {
					fees[entry.Asset] += entry.Amount
				}
			}
		}
	}
	for asset, amount := range fees {
		scale := h.market.BaseScale
		if asset == h.market.QuoteAsset {
			scale = h.market.QuoteScale
		}
		state.PlatformFees[string(asset)], _ = decimal.Format(amount, scale)
	}
	for _, asset := range []domain.Asset{h.market.BaseAsset, h.market.QuoteAsset} {
		scale := h.market.BaseScale
		if asset == h.market.QuoteAsset {
			scale = h.market.QuoteScale
		}
		state.JournalSums[string(asset)], _ = decimal.Format(sums[asset], scale)
		if sums[asset] != 0 {
			state.Ledger.Balanced = false
		}
	}
	return state, nil
}

func (h *Harness) referencePrice(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"market_id": h.market.ID, "price": Price, "observed_at": h.observedAt.Format(time.RFC3339Nano), "source": "golden_fixture", "freshness": "fresh", "executable": false})
}

func (h *Harness) trackOrderReplays(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/trading/orders" {
			next.ServeHTTP(w, r)
			return
		}
		before := h.store.RecordCount()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status >= 200 && recorder.status < 300 && h.store.RecordCount() == before {
			h.orderReplays.Add(1)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (h *Harness) assetDashboard(w http.ResponseWriter, _ *http.Request) {
	observedAt := h.observedAt.UnixMilli()
	writeJSON(w, http.StatusOK, map[string]any{"code": 2000, "total": 1, "result": []any{map[string]any{
		"rank": 1, "asset_id": "bitcoin", "asset_symbol": "BTC", "asset_name": "Bitcoin",
		"price_usd":           map[string]any{"available": true, "value": "60000"},
		"composite_price_usd": map[string]any{"available": true, "value": "60000"},
		"freshness_status":    "fresh", "confidence": 1, "quality": "high", "available": true,
		"observed_at": observedAt, "last_success_at": observedAt, "provider_updated_at": observedAt, "index_updated_at": observedAt,
	}}})
}

func (h *Harness) assetMarkets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"code": 2000, "result": []any{map[string]any{
		"asset_id": "bitcoin", "market_id": "binance:BTC-USDT", "market_code": "binance:BTC-USDT",
		"provider": "binance", "venue": "binance", "symbol": "BTCUSDT", "base_asset": "BTC", "quote_asset": "USDT",
		"market_type": "spot", "has_kline": true, "status": "online", "available": true,
		"price": map[string]any{"available": true, "value": "60000"}, "freshness_status": "fresh",
	}}})
}

func (h *Harness) klines(w http.ResponseWriter, _ *http.Request) {
	first := h.observedAt.Truncate(time.Minute).Add(-time.Minute).UnixMilli()
	writeJSON(w, http.StatusOK, map[string]any{"code": 2000, "result": []any{
		map[string]any{"timestamp": first, "open": "60000", "high": "60010", "low": "59990", "close": "60000", "volume": "1"},
		map[string]any{"timestamp": first + 60_000, "open": "60000", "high": "60010", "low": "59990", "close": "60000", "volume": "1"},
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
