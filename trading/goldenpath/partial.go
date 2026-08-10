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
	"strings"
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
	PartialBuyerAccount          domain.AccountID = "partial-golden:buyer"
	PartialSellerAccount         domain.AccountID = "partial-golden:seller"
	PartialPrice                                  = "60000.00"
	PartialBuyerQuantity                          = "0.02000000"
	PartialFillQuantity                           = "0.01000000"
	DefaultPartialFrontendOrigin                  = "http://127.0.0.1:4177"
)

type PartialHarness struct {
	market        domain.Market
	store         *store.Memory
	engine        *runnerFacade
	grpcServer    *grpc.Server
	grpcConn      *grpc.ClientConn
	client        tradingv1.TradingServiceClient
	handler       http.Handler
	observedAt    time.Time
	funded        bool
	controlMu     sync.Mutex
	partial       *tradingv1.CommandResult
	partialFor    string
	buyerOrderID  domain.OrderID
	restartProof  RestartProof
	restartCount  uint64
	cancelReplays atomic.Uint64
}

type PartialRunning struct {
	URL       string
	Harness   *PartialHarness
	server    *http.Server
	listener  net.Listener
	closeOnce sync.Once
}

type PartialOrderState struct {
	ID                string `json:"id"`
	ClientOrderID     string `json:"client_order_id"`
	Status            string `json:"status"`
	OriginalQuantity  string `json:"original_quantity"`
	FilledQuantity    string `json:"filled_quantity"`
	RemainingQuantity string `json:"remaining_quantity"`
	HeldAsset         string `json:"held_asset"`
	HeldAmount        string `json:"held_amount"`
}

type PartialReplayEvidence struct {
	CancelReplays uint64 `json:"cancel_replays"`
}

type PartialState struct {
	MarketID              string                      `json:"market_id"`
	RuntimeState          string                      `json:"runtime_state"`
	Sequence              uint64                      `json:"sequence"`
	FactCount             uint64                      `json:"fact_count"`
	StateHash             string                      `json:"state_hash"`
	BuyerOrders           int                         `json:"buyer_orders"`
	BuyerTrades           int                         `json:"buyer_trades"`
	BuyerBalances         map[string]FormattedBalance `json:"buyer_balances"`
	SellerBalances        map[string]FormattedBalance `json:"seller_balances"`
	PlatformFees          map[string]string           `json:"platform_fees"`
	Order                 *PartialOrderState          `json:"order,omitempty"`
	ReplayEvidence        PartialReplayEvidence       `json:"replay_evidence"`
	Ledger                LedgerEvidence              `json:"ledger"`
	JournalSums           map[string]string           `json:"journal_sums"`
	DuplicateTransactions bool                        `json:"duplicate_transactions"`
	RestartCount          uint64                      `json:"restart_count"`
	RestartProof          RestartProof                `json:"restart_proof"`
}

func StartPartial(ctx context.Context, bindAddress string) (*PartialRunning, error) {
	listener, err := net.Listen("tcp", bindAddress)
	if err != nil {
		return nil, err
	}
	if !listener.Addr().(*net.TCPAddr).IP.IsLoopback() {
		_ = listener.Close()
		return nil, fmt.Errorf("partial golden harness requires an IP loopback bind address")
	}
	address := listener.Addr().String()
	origin := os.Getenv("QIU_PARTIAL_GOLDEN_FRONTEND_ORIGIN")
	if origin == "" {
		origin = DefaultPartialFrontendOrigin
	}
	harness, err := newPartialHarness(ctx, address, []string{origin, "http://" + address})
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	running := &PartialRunning{URL: "http://" + address, Harness: harness, listener: listener}
	running.server = &http.Server{Handler: harness.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = running.server.Serve(listener) }()
	return running, nil
}

func newPartialHarness(ctx context.Context, httpBind string, origins []string) (*PartialHarness, error) {
	market := domain.DefaultBTCUSDTMarket()
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(ctx, market, memory, memory, tradingruntime.DefaultConfig())
	if err != nil {
		return nil, err
	}
	h := &PartialHarness{market: market, store: memory, engine: newRunnerFacade(market, memory, runner), observedAt: time.Now().UTC()}
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
	rpcService, err := rpcserver.New(h.engine, nil, rpcserver.Config{EventBatchSize: 100, EventPollEvery: 50 * time.Millisecond,
		Queries: &memoryReader{store: memory, market: market}, Cursors: rpcserver.CursorConfig{Current: rpcserver.CursorKeyConfig{KeyID: "partial-golden-v1", Secret: []byte("partial-golden-cursor-key-32-bytes")}}})
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
	if _, err = h.client.AdminFundVirtual(ctx, &tradingv1.AdminFundVirtualRequest{MarketId: string(market.ID), RequestId: "partial-fund-buyer", AccountId: string(PartialBuyerAccount), Asset: "USDT", Amount: "2000"}); err != nil {
		return nil, fmt.Errorf("fund partial buyer: %w", err)
	}
	if _, err = h.client.AdminFundVirtual(ctx, &tradingv1.AdminFundVirtualRequest{MarketId: string(market.ID), RequestId: "partial-fund-seller", AccountId: string(PartialSellerAccount), Asset: "BTC", Amount: PartialFillQuantity}); err != nil {
		return nil, fmt.Errorf("fund partial seller: %w", err)
	}
	h.funded = true
	tickets, err := auth.NewTicketManager(time.Minute)
	if err != nil {
		return nil, err
	}
	oauth, err := auth.NewOAuthStateManager(10 * time.Minute)
	if err != nil {
		return nil, err
	}
	config := httpapi.DefaultConfig()
	config.BindAddress = httpBind
	config.AllowedOrigins = origins
	config.AllowedLogin = "partial-golden-local"
	config.LocalMode = true
	config.LocalAccountID = string(PartialBuyerAccount)
	config.SecureCookies = false
	config.RecoveryGate = false
	config.WriteLimit = 500
	production, err := httpapi.New(h.client, newMemorySessionStore(), tickets, oauth, nil, config)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/trading/", h.trackCancelReplays(production.Handler()))
	mux.HandleFunc("POST /api/v2/get_asset_dashboard", h.assetDashboard)
	mux.HandleFunc("POST /api/v2/get_asset_markets", h.assetMarkets)
	mux.HandleFunc("POST /api/v2/get_asset_venues", h.assetMarkets)
	mux.HandleFunc("POST /api/v1/get_klines", h.klines)
	mux.HandleFunc("GET /__partial-golden/ready", h.ready)
	mux.HandleFunc("POST /__partial-golden/partial-fill", h.partialFill)
	mux.HandleFunc("POST /__partial-golden/restart", h.restart)
	mux.HandleFunc("GET /__partial-golden/state", h.stateHTTP)
	h.handler = loopbackOnly(mux)
	cleanup = false
	return h, nil
}

func (h *PartialHarness) Handler() http.Handler { return h.handler }
func (h *PartialHarness) Close(ctx context.Context) error {
	if h.grpcConn != nil {
		_ = h.grpcConn.Close()
	}
	if h.grpcServer != nil {
		h.grpcServer.Stop()
	}
	if h.engine != nil {
		return h.engine.Close(ctx)
	}
	return nil
}
func (r *PartialRunning) Close(ctx context.Context) error {
	var result error
	r.closeOnce.Do(func() { result = errors.Join(r.server.Shutdown(ctx), r.Harness.Close(ctx)) })
	return result
}

func (h *PartialHarness) ready(w http.ResponseWriter, _ *http.Request) {
	status := h.engine.Status()
	ready := h.funded && status.State == tradingruntime.StateReady
	code := http.StatusOK
	if !ready {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"ready": ready, "funded": h.funded, "market_id": status.MarketID, "sequence": status.Sequence, "state_hash": status.StateHash})
}

func decodeOneObject(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination) == nil && decoder.Decode(&struct{}{}) == io.EOF
}

func (h *PartialHarness) partialFill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RestingClientOrderID string `json:"resting_client_order_id"`
	}
	if !decodeOneObject(w, r, &body) || body.RestingClientOrderID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resting_client_order_id is required"})
		return
	}
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	if h.partial != nil {
		if h.partialFor != body.RestingClientOrderID {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "partial fill is bound to a different resting order"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"order_id": h.partial.OrderId, "status": h.partial.Status, "sequence": h.partial.Sequence})
		return
	}
	price, _ := decimal.Parse(PartialPrice, h.market.QuoteScale)
	quantity, _ := decimal.Parse(PartialBuyerQuantity, h.market.BaseScale)
	held, _ := h.market.QuoteAmountCeil(price, quantity)
	orders, err := h.engine.Orders(PartialBuyerAccount, true)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	var resting *domain.Order
	for index := range orders {
		if orders[index].ClientOrderID == body.RestingClientOrderID {
			resting = &orders[index]
			break
		}
	}
	if resting == nil || resting.Status != domain.OrderStatusOpen || resting.Side != domain.SideBuy || resting.Type != domain.OrderTypeLimit || resting.Price != price || resting.OriginalQuantity != quantity || resting.RemainingQuantity != quantity || resting.HeldAsset != h.market.QuoteAsset || resting.HeldAmount != held {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "buyer order is not the fixed open resting order"})
		return
	}
	result, err := h.client.SubmitOrder(r.Context(), &tradingv1.SubmitOrderRequest{MarketId: string(h.market.ID), AccountId: string(PartialSellerAccount), ClientOrderId: "partial-seller-fill-v1", Side: tradingv1.Side_SIDE_SELL, Type: tradingv1.OrderType_ORDER_TYPE_LIMIT, TimeInForce: tradingv1.TimeInForce_TIME_IN_FORCE_GTC, Price: PartialPrice, Quantity: PartialFillQuantity})
	if err != nil || !isFilledResult(result) {
		message := "seller order did not fill"
		if err != nil {
			message = err.Error()
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": message})
		return
	}
	updated, found, err := h.engine.Order(resting.ID)
	remaining, _ := decimal.Parse(PartialFillQuantity, h.market.BaseScale)
	remainingHeld, _ := h.market.QuoteAmountCeil(price, remaining)
	if err != nil || !found || updated.Status != domain.OrderStatusPartiallyFilled || updated.FilledQuantity != remaining || updated.RemainingQuantity != remaining || updated.HeldAsset != h.market.QuoteAsset || updated.HeldAmount != remainingHeld {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "buyer order did not reach the exact partial state"})
		return
	}
	h.partial = result
	h.partialFor = body.RestingClientOrderID
	h.buyerOrderID = resting.ID
	writeJSON(w, http.StatusOK, map[string]any{"order_id": result.OrderId, "status": updated.Status.String(), "sequence": result.Sequence, "buyer_order_id": resting.ID})
}

func (h *PartialHarness) restart(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decodeOneObject(w, r, &body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must be an empty object"})
		return
	}
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	if h.buyerOrderID == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "partial fill must complete before restart"})
		return
	}
	proof, err := h.engine.Restart(r.Context(), PartialBuyerAccount, h.buyerOrderID)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "restart_proof": proof})
		return
	}
	h.restartProof = proof
	h.restartCount++
	writeJSON(w, http.StatusOK, proof)
}

func (h *PartialHarness) stateHTTP(w http.ResponseWriter, r *http.Request) {
	state, err := h.State(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, state)
}

func (h *PartialHarness) State(ctx context.Context) (PartialState, error) {
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	status := h.engine.Status()
	orders, err := h.engine.Orders(PartialBuyerAccount, false)
	if err != nil {
		return PartialState{}, err
	}
	trades, err := h.engine.Trades(PartialBuyerAccount)
	if err != nil {
		return PartialState{}, err
	}
	state := PartialState{MarketID: string(h.market.ID), RuntimeState: string(status.State), Sequence: status.Sequence, FactCount: h.store.RecordCount(), StateHash: status.StateHash, BuyerOrders: len(orders), BuyerTrades: len(trades), BuyerBalances: map[string]FormattedBalance{}, SellerBalances: map[string]FormattedBalance{}, PlatformFees: map[string]string{}, ReplayEvidence: PartialReplayEvidence{CancelReplays: h.cancelReplays.Load()}, Ledger: LedgerEvidence{Balanced: true}, JournalSums: map[string]string{}, RestartCount: h.restartCount, RestartProof: h.restartProof}
	for account, destination := range map[domain.AccountID]map[string]FormattedBalance{PartialBuyerAccount: state.BuyerBalances, PartialSellerAccount: state.SellerBalances} {
		balances, balanceErr := h.engine.Balances(account)
		if balanceErr != nil {
			return PartialState{}, balanceErr
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
	if len(orders) > 0 {
		order := orders[0]
		original, _ := decimal.Format(order.OriginalQuantity, h.market.BaseScale)
		filled, _ := decimal.Format(order.FilledQuantity, h.market.BaseScale)
		remaining, _ := decimal.Format(order.RemainingQuantity, h.market.BaseScale)
		heldScale := h.market.QuoteScale
		if order.HeldAsset == h.market.BaseAsset {
			heldScale = h.market.BaseScale
		}
		held, _ := decimal.Format(order.HeldAmount, heldScale)
		state.Order = &PartialOrderState{ID: string(order.ID), ClientOrderID: order.ClientOrderID, Status: order.Status.String(), OriginalQuantity: original, FilledQuantity: filled, RemainingQuantity: remaining, HeldAsset: string(order.HeldAsset), HeldAmount: held}
	}
	records, err := h.store.RecordsAfter(ctx, 0)
	if err != nil {
		return PartialState{}, err
	}
	sums := map[domain.Asset]int64{}
	fees := map[domain.Asset]int64{}
	ids := map[string]struct{}{}
	for _, record := range records {
		for _, tx := range record.Journal {
			state.Ledger.TransactionCount++
			if _, exists := ids[tx.ID]; exists {
				state.DuplicateTransactions = true
			}
			ids[tx.ID] = struct{}{}
			for _, entry := range tx.Entries {
				state.Ledger.EntryCount++
				sums[entry.Asset] += entry.Amount
				if entry.Account == ledger.PlatformFee(entry.Asset) {
					fees[entry.Asset] += entry.Amount
				}
			}
		}
	}
	for _, asset := range []domain.Asset{h.market.BaseAsset, h.market.QuoteAsset} {
		scale := h.market.BaseScale
		if asset == h.market.QuoteAsset {
			scale = h.market.QuoteScale
		}
		state.JournalSums[string(asset)], _ = decimal.Format(sums[asset], scale)
		state.PlatformFees[string(asset)], _ = decimal.Format(fees[asset], scale)
		if sums[asset] != 0 {
			state.Ledger.Balanced = false
		}
	}
	return state, nil
}

func (h *PartialHarness) trackCancelReplays(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/v1/trading/orders/") || !strings.HasSuffix(r.URL.Path, "/cancel") {
			next.ServeHTTP(w, r)
			return
		}
		before := h.store.RecordCount()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status >= 200 && recorder.status < 300 && h.store.RecordCount() == before {
			h.cancelReplays.Add(1)
		}
	})
}

func (h *PartialHarness) assetDashboard(w http.ResponseWriter, _ *http.Request) {
	observed := h.observedAt.UnixMilli()
	writeJSON(w, 200, map[string]any{"code": 2000, "total": 1, "result": []any{map[string]any{"rank": 1, "asset_id": "bitcoin", "asset_symbol": "BTC", "asset_name": "Bitcoin", "price_usd": map[string]any{"available": true, "value": "60000"}, "composite_price_usd": map[string]any{"available": true, "value": "60000"}, "freshness_status": "fresh", "confidence": 1, "quality": "high", "available": true, "observed_at": observed, "last_success_at": observed}}})
}
func (h *PartialHarness) assetMarkets(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"code": 2000, "result": []any{map[string]any{"asset_id": "bitcoin", "market_id": "binance:BTC-USDT", "provider": "binance", "venue": "binance", "symbol": "BTCUSDT", "base_asset": "BTC", "quote_asset": "USDT", "market_type": "spot", "has_kline": true, "status": "online", "available": true, "price": map[string]any{"available": true, "value": "60000"}, "freshness_status": "fresh"}}})
}
func (h *PartialHarness) klines(w http.ResponseWriter, _ *http.Request) {
	stamp := h.observedAt.Truncate(time.Minute).UnixMilli()
	writeJSON(w, 200, map[string]any{"code": 2000, "result": []any{map[string]any{"timestamp": stamp, "open": "60000", "high": "60010", "low": "59990", "close": "60000", "volume": "1"}}})
}
