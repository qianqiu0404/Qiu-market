// Package binancepublic provides a default-disabled ticker and OHLCV adapter
// plus its restricted transport for Binance's public market-data-only REST API.
package binancepublic

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
	"time"

	"github.com/the-web3/s78-market-services/marketdata/providercontract"
)

const (
	productionOrigin = "https://data-api.binance.vision"
	requestTimeout   = 5 * time.Second
	tickerBodyLimit  = 64 << 10
	klinesBodyLimit  = 1 << 20
	defaultRetryWait = time.Second
	maximumRetryWait = 7 * 24 * time.Hour

	OperationTicker24h = "ticker_24h"
	OperationKlines    = "klines"

	tickerPath = "/api/v3/ticker/24hr"
	klinesPath = "/api/v3/klines"
)

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
}

type ObservationSink interface {
	Observe(Observation)
}

type clientOptions struct {
	Clock providercontract.Clock
	Sink  ObservationSink
}

type httpClient struct {
	origin     *url.URL
	httpClient *http.Client
	clock      providercontract.Clock
	sink       ObservationSink
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
		clock: clock,
		sink:  options.Sink,
	}, nil
}

// DoJSON executes one of the two frozen public operations. Both path and query
// are revalidated here, so adapters cannot turn this client into an arbitrary
// URL fetcher.
func (c *httpClient) DoJSON(
	ctx context.Context,
	operation string,
	path string,
	query url.Values,
	dst any,
) (receivedAt time.Time, returnErr error) {
	label, capability, bodyLimit := operationPolicy(operation)
	startedAt := c.now()
	observation := Observation{
		Provider: ProviderID, Operation: label, Capability: capability, Outcome: "error",
	}
	defer func() {
		observation.Duration = c.now().Sub(startedAt)
		if returnErr == nil {
			observation.Outcome = "success"
		} else if kind, ok := providercontract.ErrorKindOf(returnErr); ok {
			observation.ErrorKind = kind
		} else if errors.Is(returnErr, context.Canceled) {
			observation.Outcome = "canceled"
		}
		if c != nil && c.sink != nil {
			c.sink.Observe(observation)
		}
	}()

	if c == nil || c.origin == nil || c.httpClient == nil || c.clock == nil {
		return time.Time{}, typed(providercontract.ErrorUnconfigured, label, errors.New("nil or incomplete client"))
	}
	if err := validateOperation(operation, path, query); err != nil {
		return time.Time{}, err
	}
	if err := validateDestination(dst); err != nil {
		return time.Time{}, typed(providercontract.ErrorBadRequest, label, err)
	}

	requestURL := *c.origin
	requestURL.Path = path
	requestURL.RawQuery = query.Encode()
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return time.Time{}, typed(providercontract.ErrorBadRequest, label, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "qiu-market/providercontract-binancepublic/1.0")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return time.Time{}, classifyTransportError(label, requestCtx, err)
	}
	defer response.Body.Close()
	observation.StatusCode = response.StatusCode

	body, readErr := io.ReadAll(io.LimitReader(response.Body, int64(bodyLimit)+1))
	observation.ResponseBytes = int64(len(body))
	if readErr != nil {
		return time.Time{}, typed(providercontract.ErrorNetwork, label, fmt.Errorf("read response: %w", readErr))
	}
	if response.StatusCode != http.StatusOK {
		statusErr := classifyStatus(label, response.StatusCode, response.Header, c.now())
		var providerError *providercontract.ProviderError
		if errors.As(statusErr, &providerError) {
			observation.RetryAfter = providerError.RetryAfter
		}
		return time.Time{}, statusErr
	}
	if len(body) > bodyLimit {
		return time.Time{}, typed(providercontract.ErrorBadPayload, label, fmt.Errorf("response exceeds %d byte limit", bodyLimit))
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		return time.Time{}, typed(providercontract.ErrorBadPayload, label, errors.New("response content type must be application/json"))
	}
	if err := decodeOneJSON(body, dst); err != nil {
		return time.Time{}, typed(providercontract.ErrorBadPayload, label, err)
	}
	receivedAt = c.now()
	return receivedAt, nil
}

func operationPolicy(operation string) (string, providercontract.Capability, int) {
	switch operation {
	case OperationTicker24h:
		return OperationTicker24h, providercontract.CapabilitySpotTicker, tickerBodyLimit
	case OperationKlines:
		return OperationKlines, providercontract.CapabilityOHLCV, klinesBodyLimit
	default:
		return "invalid", "", tickerBodyLimit
	}
}

func validateOperation(operation, path string, query url.Values) error {
	switch operation {
	case OperationTicker24h:
		expected := url.Values{
			"symbol": {"BTCUSDT"}, "type": {"FULL"}, "symbolStatus": {"TRADING"},
		}
		if path != tickerPath || query.Encode() != expected.Encode() {
			return typed(providercontract.ErrorBadRequest, operation, errors.New("ticker path or query is not allowlisted"))
		}
	case OperationKlines:
		if path != klinesPath || len(query) != 4 ||
			singleValue(query, "symbol") != "BTCUSDT" ||
			singleValue(query, "timeZone") != "0" ||
			singleValue(query, "interval") != "1m" ||
			singleValue(query, "limit") != "10" {
			return typed(providercontract.ErrorBadRequest, operation, errors.New("klines path or query is not allowlisted"))
		}
	default:
		return typed(providercontract.ErrorBadRequest, "invalid", errors.New("operation is not allowlisted"))
	}
	return nil
}

func singleValue(values url.Values, key string) string {
	items, ok := values[key]
	if !ok || len(items) != 1 {
		return ""
	}
	return items[0]
}

func validateOrigin(raw string, allowTestLoopback bool) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, typed(providercontract.ErrorUnconfigured, "client", errors.New("origin must be a credential-free HTTPS origin"))
	}
	if !allowTestLoopback {
		if parsed.String() != productionOrigin || parsed.Hostname() != "data-api.binance.vision" || (parsed.Port() != "" && parsed.Port() != "443") {
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

func decodeOneJSON(body []byte, dst any) error {
	value := reflect.ValueOf(dst)
	temporary := reflect.New(value.Elem().Type())
	decoder := json.NewDecoder(bytes.NewReader(body))
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
	kind := providercontract.ErrorBadRequest
	switch {
	case status == http.StatusUnauthorized:
		kind = providercontract.ErrorAuth
	case status == http.StatusForbidden || status == http.StatusUnavailableForLegalReasons:
		kind = providercontract.ErrorPermission
	case status == http.StatusRequestTimeout:
		kind = providercontract.ErrorTimeout
	case status == http.StatusTeapot || status == http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(header.Get("Retry-After"), now)
		return providercontract.NewRetryError(
			providercontract.ErrorRateLimit, ProviderID, operation, retryAfter,
			fmt.Errorf("HTTP %d", status),
		)
	case status >= 500 && status <= 599:
		kind = providercontract.ErrorUpstream5xx
	case status >= 300 && status <= 399:
		kind = providercontract.ErrorBadPayload
	}
	return providercontract.NewError(kind, ProviderID, operation, fmt.Errorf("HTTP %d", status))
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		if seconds > int64(maximumRetryWait/time.Second) {
			return maximumRetryWait
		}
		wait := time.Duration(seconds) * time.Second
		if wait < defaultRetryWait {
			return defaultRetryWait
		}
		return wait
	}
	if deadline, err := http.ParseTime(value); err == nil {
		wait := deadline.Sub(now.UTC())
		if wait < defaultRetryWait {
			return defaultRetryWait
		}
		if wait > maximumRetryWait {
			return maximumRetryWait
		}
		return wait
	}
	return defaultRetryWait
}

func classifyTransportError(operation string, requestCtx context.Context, err error) error {
	if errors.Is(requestCtx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
		return typed(providercontract.ErrorTimeout, operation, err)
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return typed(providercontract.ErrorTimeout, operation, err)
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateError x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) || errors.As(err, &certificateError) {
		return typed(providercontract.ErrorInvalidIdentity, operation, errors.New("TLS peer identity verification failed"))
	}
	return typed(providercontract.ErrorNetwork, operation, err)
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
