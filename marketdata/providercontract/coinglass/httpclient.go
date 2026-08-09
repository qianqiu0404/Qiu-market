// Package coinglass provides a default-disabled, credential-gated derivatives
// adapter and a restricted transport for CoinGlass Open API V4.
package coinglass

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

const (
	productionOrigin  = "https://open-api-v4.coinglass.com"
	requestTimeout    = 5 * time.Second
	responseBodyLimit = 512 << 10
	defaultRetryWait  = time.Minute
	maximumRetryWait  = 7 * 24 * time.Hour
	localRateWindow   = time.Minute
	localRateLimit    = 30
	maximumKeyBytes   = 1024

	OperationOpenInterestHistory = "open_interest_history"
	OperationLiquidationHistory  = "liquidation_history"

	openInterestHistoryPath = "/api/futures/open-interest/history"
	liquidationHistoryPath  = "/api/futures/liquidation/history"
)

// SecretProvider supplies a disposable copy of the API key from the hosting
// process. APIKey must not return the provider's long-lived backing storage;
// this package clears the returned slice and its own header copy.
type SecretProvider interface {
	APIKey(context.Context) ([]byte, error)
}

// Observation deliberately has no URL, query, header, payload, credential or
// upstream error string. Its dimensions remain safe and low-cardinality.
type Observation struct {
	Provider      providercontract.ProviderID `json:"provider"`
	Operation     string                      `json:"operation"`
	Capability    providercontract.Capability `json:"capability"`
	Outcome       string                      `json:"outcome"`
	ErrorKind     providercontract.ErrorKind  `json:"error_kind,omitempty"`
	StatusCode    int                         `json:"status_code,omitempty"`
	Duration      time.Duration               `json:"duration"`
	ResponseBytes int64                       `json:"response_bytes"`
	RetryAfter    time.Duration               `json:"retry_after,omitempty"`
	QuotaMaximum  int                         `json:"quota_maximum,omitempty"`
	QuotaUsed     int                         `json:"quota_used,omitempty"`
}

type ObservationSink interface {
	Observe(Observation)
}

type clientOptions struct {
	Clock          providercontract.Clock
	Sink           ObservationSink
	SecretProvider SecretProvider
}

type httpClient struct {
	origin         *url.URL
	httpClient     *http.Client
	clock          providercontract.Clock
	sink           ObservationSink
	secretProvider SecretProvider
	budget         *requestBudget
}

type requestBudget struct {
	mu       sync.Mutex
	attempts []time.Time
}

type utcClock struct{}

func (utcClock) Now() time.Time { return time.Now().UTC() }

func newProductionClient(options clientOptions) (*httpClient, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout: 2 * time.Second, KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 2 * time.Second
	transport.ResponseHeaderTimeout = 3 * time.Second
	transport.IdleConnTimeout = 30 * time.Second
	return newHTTPClient(productionOrigin, transport, options, false)
}

func newTestClient(origin string, transport *http.Transport, options clientOptions) (*httpClient, error) {
	return newHTTPClient(origin, transport, options, true)
}

func newHTTPClient(
	origin string,
	transport *http.Transport,
	options clientOptions,
	allowTestLoopback bool,
) (*httpClient, error) {
	parsed, err := validateOrigin(origin, allowTestLoopback)
	if err != nil {
		return nil, err
	}
	if transport == nil {
		return nil, typed(providercontract.ErrorUnconfigured, "client", errors.New("HTTP transport is required"))
	}
	transport = transport.Clone()
	transport.Proxy = nil
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		if transport.TLSClientConfig.InsecureSkipVerify {
			return nil, typed(providercontract.ErrorUnconfigured, "client", errors.New("TLS verification cannot be disabled"))
		}
		if transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			transport.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	clock := options.Clock
	if clock == nil {
		clock = utcClock{}
	}
	return &httpClient{
		origin: parsed,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		clock:          clock,
		sink:           options.Sink,
		secretProvider: options.SecretProvider,
		budget:         &requestBudget{},
	}, nil
}

// DoJSON executes one frozen read-only operation. Path and query are checked
// before credential resolution, and the credential can only enter the
// CG-API-KEY header after those checks pass.
func (c *httpClient) DoJSON(
	ctx context.Context,
	operation string,
	path string,
	query url.Values,
	dst any,
) (receivedAt time.Time, returnErr error) {
	label := operationLabel(operation)
	observation := Observation{
		Provider: ProviderID, Operation: label,
		Capability: providercontract.CapabilityDerivatives, Outcome: "error",
	}
	startedAt := time.Now()
	defer func() {
		observation.Duration = time.Since(startedAt)
		if returnErr == nil {
			observation.Outcome = "success"
		} else if kind, ok := providercontract.ErrorKindOf(returnErr); ok {
			observation.ErrorKind = kind
			setObservationRetryAfter(&observation, returnErr)
		} else if errors.Is(returnErr, context.Canceled) {
			observation.Outcome = "canceled"
		}
		if c != nil && c.sink != nil {
			c.sink.Observe(observation)
		}
	}()

	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	if c == nil || c.origin == nil || c.httpClient == nil || c.clock == nil || c.budget == nil {
		return time.Time{}, typed(providercontract.ErrorUnconfigured, label, errors.New("nil or incomplete client"))
	}
	if err := validateOperation(operation, path, query); err != nil {
		return time.Time{}, err
	}
	if err := validateDestination(dst); err != nil {
		return time.Time{}, typed(providercontract.ErrorBadRequest, label, err)
	}
	key, err := c.resolveAPIKey(ctx, label)
	if err != nil {
		return time.Time{}, err
	}
	defer clear(key)

	if allowed, retryAfter := c.budget.Allow(c.now()); !allowed {
		return time.Time{}, providercontract.NewRetryError(
			providercontract.ErrorRateLimit, ProviderID, label, retryAfter,
			errors.New("local CoinGlass request budget is exhausted"),
		)
	}

	requestURL := *c.origin
	requestURL.Path = path
	requestURL.RawQuery = query.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return time.Time{}, typed(providercontract.ErrorBadRequest, label, errors.New("construct request"))
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "qiu-market/providercontract-coinglass/1.0")
	request.Header.Set("CG-API-KEY", string(key))
	defer request.Header.Del("CG-API-KEY")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return time.Time{}, classifyTransportError(label, requestCtx, err)
	}
	defer response.Body.Close()
	observation.StatusCode = response.StatusCode
	observation.QuotaMaximum = parseQuotaHeader(response.Header.Get("API-KEY-MAX-LIMIT"))
	observation.QuotaUsed = parseQuotaHeader(response.Header.Get("API-KEY-USE-LIMIT"))

	body, readErr := io.ReadAll(io.LimitReader(response.Body, responseBodyLimit+1))
	observation.ResponseBytes = int64(len(body))
	if readErr != nil {
		return time.Time{}, typed(providercontract.ErrorNetwork, label, errors.New("read response"))
	}
	if response.StatusCode != http.StatusOK {
		statusErr := classifyStatus(label, response.StatusCode, response.Header, c.now())
		setObservationRetryAfter(&observation, statusErr)
		return time.Time{}, statusErr
	}
	if len(body) > responseBodyLimit {
		return time.Time{}, typed(providercontract.ErrorBadPayload, label, fmt.Errorf("response exceeds %d byte limit", responseBodyLimit))
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		return time.Time{}, typed(providercontract.ErrorBadPayload, label, errors.New("response content type must be application/json"))
	}
	if err := validateEnvelopeCode(body, label, response.Header, c.now()); err != nil {
		setObservationRetryAfter(&observation, err)
		return time.Time{}, err
	}
	if err := decodeOneJSON(body, dst); err != nil {
		return time.Time{}, typed(providercontract.ErrorBadPayload, label, err)
	}
	return c.now(), nil
}

func (c *httpClient) resolveAPIKey(ctx context.Context, operation string) ([]byte, error) {
	if c.secretProvider == nil {
		return nil, typed(providercontract.ErrorUnconfigured, operation, errors.New("CoinGlass secret provider is not configured"))
	}
	raw, err := c.secretProvider.APIKey(ctx)
	if err != nil {
		return nil, typed(providercontract.ErrorAuth, operation, errors.New("CoinGlass API credential is unavailable"))
	}
	defer clear(raw)
	key := bytes.Clone(raw)
	if len(key) == 0 {
		return nil, typed(providercontract.ErrorAuth, operation, errors.New("CoinGlass API credential is empty"))
	}
	if len(key) > maximumKeyBytes || !visibleASCII(key) {
		clear(key)
		return nil, typed(providercontract.ErrorAuth, operation, errors.New("CoinGlass API credential has an invalid header form"))
	}
	return key, nil
}

func visibleASCII(value []byte) bool {
	for _, item := range value {
		if item < 0x21 || item > 0x7e {
			return false
		}
	}
	return true
}

func (b *requestBudget) Allow(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now = now.UTC()
	cutoff := now.Add(-localRateWindow)
	firstCurrent := 0
	for firstCurrent < len(b.attempts) && !b.attempts[firstCurrent].After(cutoff) {
		firstCurrent++
	}
	if firstCurrent > 0 {
		copy(b.attempts, b.attempts[firstCurrent:])
		b.attempts = b.attempts[:len(b.attempts)-firstCurrent]
	}
	if len(b.attempts) >= localRateLimit {
		retryAfter := b.attempts[0].Add(localRateWindow).Sub(now)
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		return false, retryAfter
	}
	b.attempts = append(b.attempts, now)
	return true, 0
}

func operationLabel(operation string) string {
	switch operation {
	case OperationOpenInterestHistory, OperationLiquidationHistory:
		return operation
	default:
		return "invalid"
	}
}

func validateOperation(operation, path string, query url.Values) error {
	var expected url.Values
	switch operation {
	case OperationOpenInterestHistory:
		if path != openInterestHistoryPath {
			return typed(providercontract.ErrorBadRequest, operation, errors.New("open-interest path is not allowlisted"))
		}
		expected = url.Values{
			"exchange": {"Binance"}, "symbol": {"BTCUSD_PERP"},
			"interval": {"4h"}, "limit": {"2"}, "unit": {"usd"},
		}
	case OperationLiquidationHistory:
		if path != liquidationHistoryPath {
			return typed(providercontract.ErrorBadRequest, operation, errors.New("liquidation path is not allowlisted"))
		}
		expected = url.Values{
			"exchange": {"Binance"}, "symbol": {"BTCUSD_PERP"},
			"interval": {"4h"}, "limit": {"2"},
		}
	default:
		return typed(providercontract.ErrorBadRequest, "invalid", errors.New("operation is not allowlisted"))
	}
	if query.Encode() != expected.Encode() {
		return typed(providercontract.ErrorBadRequest, operation, errors.New("query is not allowlisted"))
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" || expected.Get(key) != values[0] {
			return typed(providercontract.ErrorBadRequest, operation, errors.New("query must have exact single values"))
		}
	}
	return nil
}

func validateOrigin(raw string, allowTestLoopback bool) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, typed(providercontract.ErrorUnconfigured, "client", errors.New("origin must be a credential-free HTTPS origin"))
	}
	if !allowTestLoopback {
		if parsed.String() != productionOrigin || parsed.Hostname() != "open-api-v4.coinglass.com" || (parsed.Port() != "" && parsed.Port() != "443") {
			return nil, typed(providercontract.ErrorUnconfigured, "client", errors.New("production origin is not allowlisted"))
		}
	} else {
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || !ip.IsLoopback() || parsed.Port() == "" {
			return nil, typed(providercontract.ErrorUnconfigured, "client", errors.New("test origin must be an explicit loopback IP and port"))
		}
	}
	parsed.Path = ""
	return parsed, nil
}

func validateDestination(dst any) error {
	value := reflect.ValueOf(dst)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("JSON destination must be a non-nil pointer")
	}
	return nil
}

func validateEnvelopeCode(body []byte, operation string, header http.Header, now time.Time) error {
	var probe struct {
		Code json.RawMessage `json:"code"`
		Msg  json.RawMessage `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := decodeOneJSON(body, &probe); err != nil {
		return typed(providercontract.ErrorBadPayload, operation, err)
	}
	var code string
	if len(probe.Code) == 0 || json.Unmarshal(probe.Code, &code) != nil || code == "" {
		return typed(providercontract.ErrorBadPayload, operation, errors.New("response code must be a non-empty string"))
	}
	switch code {
	case "0":
		return nil
	case "400", "404", "405", "422":
		return typed(providercontract.ErrorBadRequest, operation, fmt.Errorf("CoinGlass response code %s", code))
	case "401":
		return typed(providercontract.ErrorAuth, operation, errors.New("CoinGlass response code 401"))
	case "403":
		return typed(providercontract.ErrorPermission, operation, errors.New("CoinGlass response code 403"))
	case "408":
		return typed(providercontract.ErrorTimeout, operation, errors.New("CoinGlass response code 408"))
	case "429":
		return providercontract.NewRetryError(
			providercontract.ErrorRateLimit, ProviderID, operation,
			parseRetryAfter(header.Get("Retry-After"), now),
			errors.New("CoinGlass response code 429"),
		)
	case "500":
		return typed(providercontract.ErrorUpstream5xx, operation, errors.New("CoinGlass response code 500"))
	default:
		return typed(providercontract.ErrorBadPayload, operation, errors.New("unrecognized CoinGlass response code"))
	}
}

func decodeOneJSON(body []byte, dst any) error {
	value := reflect.ValueOf(dst)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return errors.New("JSON destination must be a non-nil pointer")
	}
	temporary := reflect.New(value.Elem().Type())
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(temporary.Interface()); err != nil {
		return fmt.Errorf("decode JSON response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("response contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	value.Elem().Set(temporary.Elem())
	return nil
}

func classifyStatus(operation string, status int, header http.Header, now time.Time) error {
	switch {
	case status == http.StatusUnauthorized:
		return typed(providercontract.ErrorAuth, operation, errors.New("HTTP 401"))
	case status == http.StatusForbidden || status == http.StatusUnavailableForLegalReasons:
		return typed(providercontract.ErrorPermission, operation, fmt.Errorf("HTTP %d", status))
	case status == http.StatusRequestTimeout:
		return typed(providercontract.ErrorTimeout, operation, errors.New("HTTP 408"))
	case status == http.StatusTooManyRequests:
		return providercontract.NewRetryError(
			providercontract.ErrorRateLimit, ProviderID, operation,
			parseRetryAfter(header.Get("Retry-After"), now), errors.New("HTTP 429"),
		)
	case status >= 500 && status <= 599:
		return typed(providercontract.ErrorUpstream5xx, operation, fmt.Errorf("HTTP %d", status))
	case status >= 300 && status <= 399:
		return typed(providercontract.ErrorBadPayload, operation, fmt.Errorf("HTTP %d redirect rejected", status))
	default:
		return typed(providercontract.ErrorBadRequest, operation, fmt.Errorf("HTTP %d", status))
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		if seconds > int64(maximumRetryWait/time.Second) {
			return maximumRetryWait
		}
		wait := time.Duration(seconds) * time.Second
		if wait < time.Second {
			return time.Second
		}
		return wait
	}
	if deadline, err := http.ParseTime(value); err == nil {
		wait := deadline.Sub(now.UTC())
		if wait < time.Second {
			return time.Second
		}
		if wait > maximumRetryWait {
			return maximumRetryWait
		}
		return wait
	}
	return defaultRetryWait
}

func parseQuotaHeader(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func setObservationRetryAfter(observation *Observation, err error) {
	if observation == nil {
		return
	}
	var providerError *providercontract.ProviderError
	if errors.As(err, &providerError) {
		observation.RetryAfter = providerError.RetryAfter
	}
}

func classifyTransportError(operation string, requestCtx context.Context, err error) error {
	if errors.Is(requestCtx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
		return typed(providercontract.ErrorTimeout, operation, errors.New("request timed out"))
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return typed(providercontract.ErrorTimeout, operation, errors.New("request timed out"))
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateError x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) || errors.As(err, &certificateError) {
		return typed(providercontract.ErrorInvalidIdentity, operation, errors.New("TLS peer identity verification failed"))
	}
	return typed(providercontract.ErrorNetwork, operation, errors.New("network request failed"))
}

func typed(kind providercontract.ErrorKind, operation string, cause error) error {
	return providercontract.NewError(kind, ProviderID, operation, cause)
}

func (c *httpClient) now() time.Time {
	if c == nil || c.clock == nil {
		return time.Now().UTC()
	}
	return c.clock.Now().UTC()
}
