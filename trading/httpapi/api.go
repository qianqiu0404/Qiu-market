package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/the-web3/s78-market-services/trading/auth"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

const (
	defaultSessionCookie = "s78_trading_session"
	defaultCSRFCookie    = "s78_trading_csrf"
	oauthStateCookie     = "s78_trading_oauth_state"
)

type GitHubOAuth interface {
	AuthorizationURL(state, codeChallenge string) string
	Exchange(context.Context, string, string) (string, error)
}

type Config struct {
	MarketID         string
	AllowedLogin     string
	AllowedOrigins   []string
	BindAddress      string
	LocalMode        bool
	LocalAccountID   string
	SecureCookies    bool
	RecoveryGate     bool
	SessionCookie    string
	CSRFCookie       string
	SessionTTL       time.Duration
	WriteLimit       int
	WriteWindow      time.Duration
	FrontendRedirect string
}

func DefaultConfig() Config {
	return Config{
		MarketID:         "BTC-USDT",
		AllowedLogin:     "qianqiu0404",
		AllowedOrigins:   []string{"http://127.0.0.1:8084"},
		BindAddress:      "127.0.0.1:8084",
		LocalAccountID:   "github:qianqiu0404",
		SessionCookie:    defaultSessionCookie,
		CSRFCookie:       defaultCSRFCookie,
		SessionTTL:       12 * time.Hour,
		WriteLimit:       30,
		WriteWindow:      time.Minute,
		FrontendRedirect: "/trade/BTC-USDT",
	}
}

type Server struct {
	client      tradingv1.TradingServiceClient
	sessions    auth.SessionStore
	tickets     *auth.TicketManager
	oauthStates *auth.OAuthStateManager
	github      GitHubOAuth
	config      Config
	origins     map[string]struct{}
	limiter     *writeLimiter
	mux         *http.ServeMux
}

func New(
	client tradingv1.TradingServiceClient,
	sessions auth.SessionStore,
	tickets *auth.TicketManager,
	oauthStates *auth.OAuthStateManager,
	github GitHubOAuth,
	config Config,
) (*Server, error) {
	if client == nil || sessions == nil || tickets == nil || oauthStates == nil {
		return nil, fmt.Errorf("trading client, sessions, tickets and OAuth states are required")
	}
	if config.MarketID == "" || config.AllowedLogin == "" || config.SessionTTL <= 0 ||
		config.WriteLimit <= 0 || config.WriteWindow <= 0 {
		return nil, fmt.Errorf("invalid trading HTTP configuration")
	}
	if config.SessionCookie == "" {
		config.SessionCookie = defaultSessionCookie
	}
	if config.CSRFCookie == "" {
		config.CSRFCookie = defaultCSRFCookie
	}
	if config.LocalMode {
		if !loopbackAddress(config.BindAddress) || config.LocalAccountID == "" {
			return nil, fmt.Errorf("local auth requires a non-empty account and an IP loopback bind address")
		}
	}
	if !config.SecureCookies && !loopbackAddress(config.BindAddress) {
		return nil, fmt.Errorf("insecure cookies are allowed only on an IP loopback bind address")
	}
	origins := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, origin := range config.AllowedOrigins {
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			return nil, err
		}
		origins[normalized] = struct{}{}
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("at least one allowed origin is required")
	}
	server := &Server{
		client:      client,
		sessions:    sessions,
		tickets:     tickets,
		oauthStates: oauthStates,
		github:      github,
		config:      config,
		origins:     origins,
		limiter:     newWriteLimiter(config.WriteLimit, config.WriteWindow),
		mux:         http.NewServeMux(),
	}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "same-origin")
		writer.Header().Set("X-Frame-Options", "DENY")
		s.mux.ServeHTTP(writer, request)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/trading/auth/capabilities", s.authCapabilities)
	s.mux.HandleFunc("GET /api/v1/trading/auth/github/start", s.githubStart)
	s.mux.HandleFunc("GET /api/v1/trading/auth/github/callback", s.githubCallback)
	s.mux.HandleFunc("POST /api/v1/trading/auth/local", s.localLogin)
	s.mux.HandleFunc("POST /api/v1/trading/auth/logout", s.logout)
	s.mux.HandleFunc("GET /api/v1/trading/session", s.getSession)
	s.mux.HandleFunc("GET /api/v1/trading/markets/{market}/orderbook", s.getOrderBook)
	s.mux.HandleFunc("GET /api/v1/trading/markets/{market}/trades", s.getPublicTrades)
	s.mux.HandleFunc("GET /api/v1/trading/markets/{market}/status", s.getStatus)
	s.mux.HandleFunc("POST /api/v1/trading/orders", s.submitOrder)
	s.mux.HandleFunc("GET /api/v1/trading/orders", s.listOrders)
	s.mux.HandleFunc("GET /api/v1/trading/orders/{order}", s.getOrder)
	s.mux.HandleFunc("POST /api/v1/trading/orders/{order}/cancel", s.cancelOrder)
	s.mux.HandleFunc("GET /api/v1/trading/trades", s.listTrades)
	s.mux.HandleFunc("GET /api/v1/trading/balances", s.getBalances)
	s.mux.HandleFunc("POST /api/v1/trading/admin/fund", s.fundVirtual)
	s.mux.HandleFunc("POST /api/v1/trading/ws-ticket", s.issueWebSocketTicket)
	s.mux.HandleFunc("GET /api/v1/trading/events/ws", s.serveWebSocket)
}

func (s *Server) authCapabilities(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]bool{
		"github_oauth_enabled":  s.github != nil,
		"local_login_enabled":   s.config.LocalMode,
		"recovery_gate_enabled": s.config.RecoveryGate,
	})
}

func (s *Server) githubStart(writer http.ResponseWriter, request *http.Request) {
	if s.github == nil {
		writeError(writer, http.StatusServiceUnavailable, "oauth_unavailable", "GitHub OAuth is not configured")
		return
	}
	state, challenge, err := s.oauthStates.Issue()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "oauth_state_failed", "unable to start login")
		return
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/api/v1/trading/auth/github/callback",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   s.config.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(writer, request, s.github.AuthorizationURL(state, challenge), http.StatusFound)
}

func (s *Server) githubCallback(writer http.ResponseWriter, request *http.Request) {
	if s.github == nil {
		writeError(writer, http.StatusServiceUnavailable, "oauth_unavailable", "GitHub OAuth is not configured")
		return
	}
	stateCookie, err := request.Cookie(oauthStateCookie)
	if err != nil || !constantTimeEqual(stateCookie.Value, request.URL.Query().Get("state")) {
		writeError(writer, http.StatusBadRequest, "invalid_oauth_state", "OAuth state is invalid or expired")
		return
	}
	verifier, ok := s.oauthStates.Consume(stateCookie.Value)
	if !ok {
		writeError(writer, http.StatusBadRequest, "invalid_oauth_state", "OAuth state is invalid or expired")
		return
	}
	login, err := s.github.Exchange(request.Context(), request.URL.Query().Get("code"), verifier)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "github_oauth_failed", "GitHub login failed")
		return
	}
	if !strings.EqualFold(login, s.config.AllowedLogin) {
		writeError(writer, http.StatusForbidden, "login_denied", "this GitHub account is not allowed")
		return
	}
	if err := s.createSession(request.Context(), writer, auth.Principal{
		AccountID:   "github:" + strings.ToLower(s.config.AllowedLogin),
		GitHubLogin: s.config.AllowedLogin,
		Admin:       true,
	}); err != nil {
		writeError(writer, http.StatusInternalServerError, "session_failed", "unable to create session")
		return
	}
	s.expireCookie(writer, oauthStateCookie, "/api/v1/trading/auth/github/callback", true)
	http.Redirect(writer, request, s.config.FrontendRedirect, http.StatusFound)
}

func (s *Server) localLogin(writer http.ResponseWriter, request *http.Request) {
	if !s.config.LocalMode || !remoteIsLoopback(request.RemoteAddr) {
		writeError(writer, http.StatusNotFound, "not_found", "not found")
		return
	}
	if !s.validOrigin(request) {
		writeError(writer, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	if !s.limiter.allow("local-login:" + remoteHost(request.RemoteAddr)) {
		writeError(writer, http.StatusTooManyRequests, "rate_limited", "too many write requests")
		return
	}
	principal := auth.Principal{
		AccountID:   s.config.LocalAccountID,
		GitHubLogin: s.config.AllowedLogin,
		Admin:       true,
	}
	if err := s.createSession(request.Context(), writer, principal); err != nil {
		writeError(writer, http.StatusInternalServerError, "session_failed", "unable to create session")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"principal": principal, "local": true})
}

func (s *Server) logout(writer http.ResponseWriter, request *http.Request) {
	session, token, ok := s.requireSession(writer, request, true)
	if !ok {
		return
	}
	_ = session
	if err := s.sessions.Delete(request.Context(), token); err != nil {
		writeError(writer, http.StatusInternalServerError, "session_delete_failed", "unable to delete session")
		return
	}
	s.expireCookie(writer, s.config.SessionCookie, "/", true)
	s.expireCookie(writer, s.config.CSRFCookie, "/", false)
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) getSession(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, false)
	if !ok {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"principal":  session.Principal,
		"expires_at": session.ExpiresAt,
	})
}

func (s *Server) getOrderBook(writer http.ResponseWriter, request *http.Request) {
	if !s.requireMarket(writer, request.PathValue("market")) {
		return
	}
	levels := uint32(parseLimit(request.URL.Query().Get("levels"), 20, 200))
	response, err := s.client.GetOrderBook(request.Context(), &tradingv1.GetOrderBookRequest{
		MarketId: s.config.MarketID,
		Levels:   levels,
	})
	s.writeGRPC(writer, response, err)
}

func (s *Server) getPublicTrades(writer http.ResponseWriter, request *http.Request) {
	if !s.requireMarket(writer, request.PathValue("market")) {
		return
	}
	response, err := s.client.ListTrades(request.Context(), &tradingv1.ListTradesRequest{
		MarketId: s.config.MarketID,
		Limit:    uint32(parseLimit(request.URL.Query().Get("limit"), 100, 500)),
	})
	if err == nil {
		for _, trade := range response.Trades {
			hideTradeAccounts(trade, "")
		}
	}
	s.writeGRPC(writer, response, err)
}

func (s *Server) getStatus(writer http.ResponseWriter, request *http.Request) {
	if !s.requireMarket(writer, request.PathValue("market")) {
		return
	}
	response, err := s.client.GetStatus(request.Context(), &tradingv1.GetStatusRequest{
		MarketId: s.config.MarketID,
	})
	if err == nil && response.LastError != "" {
		response.LastError = "internal error recorded"
	}
	if err == nil && response.LastIncident != "" {
		response.LastIncident = "internal incident recorded"
	}
	if err == nil && response.OutboxLastError != "" {
		response.OutboxLastError = "internal outbox error recorded"
	}
	s.writeGRPC(writer, response, err)
}

type submitOrderBody struct {
	AccountID     string `json:"account_id"`
	ClientOrderID string `json:"client_order_id"`
	Side          string `json:"side"`
	Type          string `json:"type"`
	TimeInForce   string `json:"time_in_force"`
	PostOnly      bool   `json:"post_only"`
	Price         string `json:"price"`
	Quantity      string `json:"quantity"`
	QuoteBudget   string `json:"quote_budget"`
}

func (s *Server) submitOrder(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, true)
	if !ok {
		return
	}
	var body submitOrderBody
	if !decodeJSON(writer, request, &body) {
		return
	}
	side, orderType, timeInForce, err := parseOrderEnums(body.Side, body.Type, body.TimeInForce)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_order", err.Error())
		return
	}
	response, err := s.client.SubmitOrder(request.Context(), &tradingv1.SubmitOrderRequest{
		MarketId:      s.config.MarketID,
		AccountId:     session.Principal.AccountID,
		ClientOrderId: body.ClientOrderID,
		Side:          side,
		Type:          orderType,
		TimeInForce:   timeInForce,
		PostOnly:      body.PostOnly,
		Price:         body.Price,
		Quantity:      body.Quantity,
		QuoteBudget:   body.QuoteBudget,
	})
	s.writeGRPC(writer, response, err)
}

func (s *Server) listOrders(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, false)
	if !ok {
		return
	}
	response, err := s.client.ListOrders(request.Context(), &tradingv1.ListOrdersRequest{
		MarketId:  s.config.MarketID,
		AccountId: session.Principal.AccountID,
		OpenOnly:  proto.Bool(parseBool(request.URL.Query().Get("open_only"))),
		Limit:     uint32(parseLimit(request.URL.Query().Get("limit"), 100, 500)),
	})
	s.writeGRPC(writer, response, err)
}

func (s *Server) getOrder(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, false)
	if !ok {
		return
	}
	response, err := s.client.GetOrder(request.Context(), &tradingv1.GetOrderRequest{
		MarketId:  s.config.MarketID,
		AccountId: session.Principal.AccountID,
		OrderId:   request.PathValue("order"),
	})
	s.writeGRPC(writer, response, err)
}

type cancelOrderBody struct {
	AccountID string `json:"account_id"`
	RequestID string `json:"request_id"`
}

func (s *Server) cancelOrder(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, true)
	if !ok {
		return
	}
	var body cancelOrderBody
	if !decodeJSON(writer, request, &body) {
		return
	}
	response, err := s.client.CancelOrder(request.Context(), &tradingv1.CancelOrderRequest{
		MarketId:  s.config.MarketID,
		AccountId: session.Principal.AccountID,
		RequestId: body.RequestID,
		OrderId:   request.PathValue("order"),
	})
	s.writeGRPC(writer, response, err)
}

func (s *Server) listTrades(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, false)
	if !ok {
		return
	}
	response, err := s.client.ListTrades(request.Context(), &tradingv1.ListTradesRequest{
		MarketId:  s.config.MarketID,
		AccountId: session.Principal.AccountID,
		Limit:     uint32(parseLimit(request.URL.Query().Get("limit"), 100, 500)),
	})
	s.writeGRPC(writer, response, err)
}

func (s *Server) getBalances(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, false)
	if !ok {
		return
	}
	response, err := s.client.GetBalances(request.Context(), &tradingv1.GetBalancesRequest{
		MarketId:  s.config.MarketID,
		AccountId: session.Principal.AccountID,
	})
	s.writeGRPC(writer, response, err)
}

type fundBody struct {
	AccountID string `json:"account_id"`
	RequestID string `json:"request_id"`
	Asset     string `json:"asset"`
	Amount    string `json:"amount"`
}

func (s *Server) fundVirtual(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, true)
	if !ok {
		return
	}
	if !session.Principal.Admin {
		writeError(writer, http.StatusForbidden, "admin_required", "administrator access is required")
		return
	}
	var body fundBody
	if !decodeJSON(writer, request, &body) {
		return
	}
	target := body.AccountID
	if target == "" {
		target = session.Principal.AccountID
	}
	response, err := s.client.AdminFundVirtual(
		request.Context(),
		&tradingv1.AdminFundVirtualRequest{
			MarketId:  s.config.MarketID,
			RequestId: body.RequestID,
			AccountId: target,
			Asset:     body.Asset,
			Amount:    body.Amount,
		},
	)
	s.writeGRPC(writer, response, err)
}

func (s *Server) issueWebSocketTicket(writer http.ResponseWriter, request *http.Request) {
	session, _, ok := s.requireSession(writer, request, true)
	if !ok {
		return
	}
	ticket, expiresAt, err := s.tickets.Issue(session.Principal)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "ticket_failed", "unable to issue ticket")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"ticket":     ticket,
		"expires_at": expiresAt,
	})
}

func (s *Server) createSession(
	ctx context.Context,
	writer http.ResponseWriter,
	principal auth.Principal,
) error {
	credentials, err := s.sessions.Create(ctx, principal, s.config.SessionTTL)
	if err != nil {
		return err
	}
	maxAge := int(time.Until(credentials.ExpiresAt).Seconds())
	http.SetCookie(writer, &http.Cookie{
		Name:     s.config.SessionCookie,
		Value:    credentials.SessionToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.config.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(writer, &http.Cookie{
		Name:     s.config.CSRFCookie,
		Value:    credentials.CSRFToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   s.config.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func (s *Server) requireSession(
	writer http.ResponseWriter,
	request *http.Request,
	write bool,
) (auth.Session, string, bool) {
	cookie, err := request.Cookie(s.config.SessionCookie)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "authentication_required", "login is required")
		return auth.Session{}, "", false
	}
	session, found, err := s.sessions.Lookup(request.Context(), cookie.Value)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "session_unavailable", "session service is unavailable")
		return auth.Session{}, "", false
	}
	if !found {
		writeError(writer, http.StatusUnauthorized, "invalid_session", "session is invalid or expired")
		return auth.Session{}, "", false
	}
	if write {
		if !s.validOrigin(request) {
			writeError(writer, http.StatusForbidden, "origin_denied", "request origin is not allowed")
			return auth.Session{}, "", false
		}
		csrfCookie, cookieErr := request.Cookie(s.config.CSRFCookie)
		csrfHeader := request.Header.Get("X-CSRF-Token")
		if cookieErr != nil || !constantTimeEqual(csrfCookie.Value, csrfHeader) ||
			!auth.ValidateCSRF(session, csrfHeader) {
			writeError(writer, http.StatusForbidden, "csrf_failed", "CSRF validation failed")
			return auth.Session{}, "", false
		}
		if !s.limiter.allow("account:" + session.Principal.AccountID) {
			writeError(writer, http.StatusTooManyRequests, "rate_limited", "too many write requests")
			return auth.Session{}, "", false
		}
	}
	return session, cookie.Value, true
}

func (s *Server) requireMarket(writer http.ResponseWriter, market string) bool {
	if market != s.config.MarketID {
		writeError(writer, http.StatusNotFound, "market_not_found", "market not found")
		return false
	}
	return true
}

func (s *Server) validOrigin(request *http.Request) bool {
	origin, err := normalizeOrigin(request.Header.Get("Origin"))
	if err != nil {
		return false
	}
	_, allowed := s.origins[origin]
	return allowed
}

func (s *Server) writeGRPC(writer http.ResponseWriter, value any, err error) {
	if err == nil {
		if message, ok := value.(proto.Message); ok {
			writeProtoJSON(writer, http.StatusOK, message)
			return
		}
		writeJSON(writer, http.StatusOK, value)
		return
	}
	current := status.Convert(err)
	httpStatus := http.StatusInternalServerError
	switch current.Code() {
	case codes.InvalidArgument:
		httpStatus = http.StatusBadRequest
	case codes.Unauthenticated:
		httpStatus = http.StatusUnauthorized
	case codes.PermissionDenied:
		httpStatus = http.StatusForbidden
	case codes.NotFound:
		httpStatus = http.StatusNotFound
	case codes.AlreadyExists:
		httpStatus = http.StatusConflict
	case codes.FailedPrecondition:
		httpStatus = http.StatusUnprocessableEntity
	case codes.ResourceExhausted:
		httpStatus = http.StatusTooManyRequests
	case codes.Unavailable:
		httpStatus = http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		httpStatus = http.StatusGatewayTimeout
	}
	writeError(writer, httpStatus, strings.ToLower(current.Code().String()), current.Message())
}

func (s *Server) expireCookie(writer http.ResponseWriter, name, path string, httpOnly bool) {
	http.SetCookie(writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: httpOnly,
		Secure:   s.config.SecureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body is invalid")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must contain one object")
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, statusCode int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeProtoJSON(writer http.ResponseWriter, statusCode int, message proto.Message) {
	data, err := (protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}).Marshal(message)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "response_encode_failed", "unable to encode response")
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(statusCode)
	_, _ = writer.Write(append(data, '\n'))
}

func writeError(writer http.ResponseWriter, statusCode int, code, message string) {
	writeJSON(writer, statusCode, map[string]string{"code": code, "message": message})
}

func normalizeOrigin(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid origin %q", value)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func loopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func remoteIsLoopback(address string) bool {
	ip := net.ParseIP(remoteHost(address))
	return ip != nil && ip.IsLoopback()
}

func remoteHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	return host
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) || len(left) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func parseLimit(value string, fallback, maximum int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	if parsed > maximum {
		return maximum
	}
	return parsed
}

func parseBool(value string) bool {
	parsed, _ := strconv.ParseBool(value)
	return parsed
}

func parseOrderEnums(
	side string,
	orderType string,
	timeInForce string,
) (tradingv1.Side, tradingv1.OrderType, tradingv1.TimeInForce, error) {
	var parsedSide tradingv1.Side
	switch strings.ToLower(side) {
	case "buy":
		parsedSide = tradingv1.Side_SIDE_BUY
	case "sell":
		parsedSide = tradingv1.Side_SIDE_SELL
	default:
		return 0, 0, 0, fmt.Errorf("side must be buy or sell")
	}
	var parsedType tradingv1.OrderType
	switch strings.ToLower(orderType) {
	case "limit":
		parsedType = tradingv1.OrderType_ORDER_TYPE_LIMIT
	case "market":
		parsedType = tradingv1.OrderType_ORDER_TYPE_MARKET
	default:
		return 0, 0, 0, fmt.Errorf("type must be limit or market")
	}
	var parsedTIF tradingv1.TimeInForce
	switch strings.ToLower(timeInForce) {
	case "gtc":
		parsedTIF = tradingv1.TimeInForce_TIME_IN_FORCE_GTC
	case "ioc":
		parsedTIF = tradingv1.TimeInForce_TIME_IN_FORCE_IOC
	case "fok":
		parsedTIF = tradingv1.TimeInForce_TIME_IN_FORCE_FOK
	default:
		return 0, 0, 0, fmt.Errorf("time_in_force must be gtc, ioc or fok")
	}
	return parsedSide, parsedType, parsedTIF, nil
}

func hideTradeAccounts(trade *tradingv1.Trade, ownAccount string) {
	if trade == nil {
		return
	}
	if trade.MakerAccountId != ownAccount {
		trade.MakerAccountId = ""
	}
	if trade.TakerAccountId != ownAccount {
		trade.TakerAccountId = ""
	}
	if trade.BuyerAccountId != ownAccount {
		trade.BuyerAccountId = ""
	}
	if trade.SellerAccountId != ownAccount {
		trade.SellerAccountId = ""
	}
	if trade.BuyerFee != nil && trade.BuyerFee.AccountId != ownAccount {
		trade.BuyerFee.AccountId = ""
	}
	if trade.SellerFee != nil && trade.SellerFee.AccountId != ownAccount {
		trade.SellerFee.AccountId = ""
	}
}
