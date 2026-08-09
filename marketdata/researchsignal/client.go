package researchsignal

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	productionOrigin = "https://xiuqiu-site.vercel.app"
	maxResponseBytes = int64(1 << 20)
	requestTimeout   = 5 * time.Second
	cacheTTL         = 30 * time.Second
	cacheEntries     = 64
)

type Config struct {
	Enabled bool
}

type Client struct {
	enabled bool
	origin  *url.URL
	http    *http.Client
	now     func() time.Time
	cache   *responseCache

	mu           sync.Mutex
	blockedUntil time.Time
}

type fetched struct {
	body       []byte
	receivedAt time.Time
	candidate  *cacheEntry
}

func New(config Config) (*Client, error) {
	origin, _ := url.Parse(productionOrigin)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are forbidden")
		},
	}
	return &Client{
		enabled: config.Enabled, origin: origin, http: httpClient,
		now: time.Now, cache: newResponseCache(cacheEntries, cacheTTL),
	}, nil
}

// newTestClient is package-private so production callers cannot override the
// allowlisted origin. It accepts only an explicit loopback IP and port.
func newTestClient(origin string, httpClient *http.Client, now func() time.Time) (*Client, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.User != nil {
		return nil, fmt.Errorf("invalid test origin")
	}
	ip := net.ParseIP(parsed.Hostname())
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || ip == nil || !ip.IsLoopback() || parsed.Port() == "" {
		return nil, fmt.Errorf("test origin must be an explicit loopback IP and port")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	if now == nil {
		now = time.Now
	}
	return &Client{enabled: true, origin: parsed, http: httpClient, now: now, cache: newResponseCache(cacheEntries, cacheTTL)}, nil
}

func (c *Client) Summary(ctx context.Context) (SummaryResult, error) {
	response, err := c.fetch(ctx, "/api/market-radar/summary", nil)
	if err != nil {
		return SummaryResult{}, err
	}
	var input upstreamSummary
	if err := decodeStrict(response.body, &input); err != nil {
		return SummaryResult{}, typed(ErrorBadPayload, err)
	}
	result, err := normalizeSummary(input, c.now().UTC())
	if err == nil && cacheableStatus(result.Status) {
		c.accept(response)
	}
	return result, err
}

func (c *Client) Events(ctx context.Context, query EventQuery) (ListResult, error) {
	if err := validateQuery(query); err != nil {
		return ListResult{}, typed(ErrorBadRequest, err)
	}
	values := url.Values{
		"market": {"crypto"}, "asset": {"BTC"}, "window": {"168"},
		"limit": {strconv.Itoa(query.Limit)},
	}
	if query.Cursor != "" {
		values.Set("cursor", query.Cursor)
	}
	response, err := c.fetch(ctx, "/api/market-radar/events", values)
	if err != nil {
		return ListResult{}, err
	}
	var input upstreamList
	if err := decodeStrict(response.body, &input); err != nil {
		return ListResult{}, typed(ErrorBadPayload, err)
	}
	status, err := upstreamStatus(input.Status)
	if err != nil {
		return ListResult{}, typed(ErrorBadPayload, err)
	}
	if input.Message != nil {
		if err := validateText(*input.Message, 1, 1000); err != nil {
			return ListResult{}, typed(ErrorBadPayload, fmt.Errorf("message: %w", err))
		}
	}
	if input.NextCursor != nil {
		if err := ValidateCursor(*input.NextCursor); err != nil {
			return ListResult{}, typed(ErrorBadPayload, fmt.Errorf("next cursor: %w", err))
		}
	}
	if len(input.Items) > query.Limit {
		return ListResult{}, typed(ErrorBadPayload, errors.New("upstream page exceeds requested limit"))
	}
	if input.NextCursor != nil && (len(input.Items) == 0 || *input.NextCursor == query.Cursor) {
		return ListResult{}, typed(ErrorBadPayload, errors.New("invalid cursor progression"))
	}
	items, partial, err := normalizeEvents(input.Items, response.receivedAt, c.now().UTC())
	if err != nil {
		return ListResult{}, typed(ErrorBadPayload, err)
	}
	status = listStatus(status, items, partial)
	result := ListResult{
		Status: status, GeneratedAt: response.receivedAt.UTC(), Message: input.Message,
		Data: EventList{Items: items, NextCursor: input.NextCursor},
	}
	if partial && result.Message == nil {
		message := "duplicate or conflicting event versions were suppressed"
		result.Message = &message
	}
	if cacheableStatus(result.Status) {
		c.accept(response)
	}
	return result, nil
}

func (c *Client) Event(ctx context.Context, id string) (DetailResult, error) {
	if !idPattern.MatchString(id) {
		return DetailResult{}, typed(ErrorBadRequest, errors.New("invalid event id"))
	}
	response, err := c.fetch(ctx, "/api/market-radar/events/"+url.PathEscape(id), nil)
	if err != nil {
		return DetailResult{}, err
	}
	var input upstreamEvent
	if err := decodeStrict(response.body, &input); err != nil {
		return DetailResult{}, typed(ErrorBadPayload, err)
	}
	item, err := normalizeEvent(input, response.receivedAt, c.now().UTC())
	if err != nil {
		return DetailResult{}, typed(ErrorBadPayload, err)
	}
	if item.ID != id {
		return DetailResult{}, typed(ErrorConflict, errors.New("detail id does not match request"))
	}
	status := StatusFresh
	if item.Freshness == FreshnessStale {
		status = StatusStale
	} else if containsQuality(item.QualityFlags, "legacy_fields_missing") {
		status = StatusLegacy
	}
	result := DetailResult{Status: status, GeneratedAt: response.receivedAt.UTC(), Data: EventDetail{Item: &item}}
	if cacheableStatus(result.Status) {
		c.accept(response)
	}
	return result, nil
}

func (c *Client) fetch(ctx context.Context, path string, query url.Values) (fetched, error) {
	if !c.enabled {
		return fetched{}, typed(ErrorDisabled, errors.New("research signals are disabled"))
	}
	if err := c.validateTarget(path, query); err != nil {
		return fetched{}, typed(ErrorBadRequest, err)
	}
	requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	now := c.now().UTC()
	c.mu.Lock()
	blockedUntil := c.blockedUntil
	c.mu.Unlock()
	if now.Before(blockedUntil) {
		return fetched{}, &Error{Code: ErrorRateLimit, RetryAfter: blockedUntil.Sub(now), Cause: errors.New("upstream retry window is active")}
	}
	key := path
	if len(query) > 0 {
		key += "?" + query.Encode()
	}
	entry, cached, fresh := c.cache.get(key, now)
	if fresh {
		return fetched{body: entry.body, receivedAt: entry.receivedAt}, nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, retry, err := c.request(requestContext, key, entry, cached)
		if err == nil {
			return result, nil
		}
		if !retry || attempt == 1 {
			return fetched{}, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-requestContext.Done():
			timer.Stop()
			return fetched{}, typed(ErrorTimeout, requestContext.Err())
		case <-timer.C:
		}
	}
	return fetched{}, typed(ErrorUpstream, errors.New("retry exhausted"))
}

func (c *Client) request(ctx context.Context, key string, cached cacheEntry, hasCache bool) (fetched, bool, error) {
	target := *c.origin
	parsedKey, _ := url.Parse(key)
	target.Path = parsedKey.Path
	target.RawQuery = parsedKey.RawQuery
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fetched{}, false, typed(ErrorBadRequest, err)
	}
	request.Header.Set("Accept", "application/json")
	if hasCache {
		if cached.etag != "" {
			request.Header.Set("If-None-Match", cached.etag)
		}
		if cached.lastModified != "" {
			request.Header.Set("If-Modified-Since", cached.lastModified)
		}
	}
	response, err := c.http.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || isTimeout(err) {
			return fetched{}, true, typed(ErrorTimeout, err)
		}
		return fetched{}, false, typed(ErrorNetwork, err)
	}
	defer response.Body.Close()
	receivedAt := c.now().UTC()
	if response.StatusCode == http.StatusNotModified {
		entry, ok := c.cache.refresh(key, receivedAt)
		if !ok {
			return fetched{}, false, typed(ErrorBadPayload, errors.New("304 without cached representation"))
		}
		return fetched{body: entry.body, receivedAt: entry.receivedAt}, false, nil
	}
	if response.StatusCode == http.StatusTooManyRequests {
		retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), receivedAt)
		if retryAfter <= 0 {
			retryAfter = time.Minute
		}
		c.mu.Lock()
		c.blockedUntil = receivedAt.Add(retryAfter)
		c.mu.Unlock()
		return fetched{}, false, &Error{Code: ErrorRateLimit, RetryAfter: retryAfter, Cause: errors.New("upstream HTTP 429")}
	}
	if response.StatusCode == http.StatusNotFound {
		return fetched{}, false, typed(ErrorNotFound, errors.New("event not found"))
	}
	if response.StatusCode >= 500 && response.StatusCode <= 599 {
		return fetched{}, true, typed(ErrorUpstream, fmt.Errorf("upstream HTTP %d", response.StatusCode))
	}
	if response.StatusCode != http.StatusOK {
		return fetched{}, false, typed(ErrorUpstream, fmt.Errorf("upstream HTTP %d", response.StatusCode))
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fetched{}, false, typed(ErrorBadPayload, errors.New("content type must be application/json"))
	}
	if response.ContentLength > maxResponseBytes {
		return fetched{}, false, typed(ErrorBadPayload, errors.New("response exceeds maximum body size"))
	}
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return fetched{}, false, typed(ErrorNetwork, err)
	}
	if int64(len(body)) > maxResponseBytes {
		return fetched{}, false, typed(ErrorBadPayload, errors.New("response exceeds maximum body size"))
	}
	if len(body) == 0 {
		return fetched{}, false, typed(ErrorBadPayload, errors.New("empty response"))
	}
	entry := cacheEntry{
		key: key, body: body, etag: boundedHeader(response.Header.Get("ETag")),
		lastModified: boundedHeader(response.Header.Get("Last-Modified")),
		storedAt:     receivedAt, receivedAt: receivedAt,
	}
	return fetched{body: body, receivedAt: receivedAt, candidate: &entry}, false, nil
}

func (c *Client) validateTarget(path string, query url.Values) error {
	production := c.origin.String() == productionOrigin
	if production && (c.origin.Scheme != "https" || c.origin.Host != "xiuqiu-site.vercel.app") {
		return errors.New("production origin is not allowlisted")
	}
	if path == "/api/market-radar/summary" && len(query) == 0 {
		return nil
	}
	if path == "/api/market-radar/events" {
		if query.Get("market") != "crypto" || query.Get("asset") != "BTC" || query.Get("window") != "168" {
			return errors.New("event query is not allowlisted")
		}
		if len(query) > 5 {
			return errors.New("unexpected event query")
		}
		for key := range query {
			if key != "market" && key != "asset" && key != "window" && key != "limit" && key != "cursor" {
				return errors.New("unexpected event query key")
			}
		}
		return nil
	}
	if strings.HasPrefix(path, "/api/market-radar/events/") && len(query) == 0 && idPattern.MatchString(strings.TrimPrefix(path, "/api/market-radar/events/")) {
		return nil
	}
	return errors.New("path is not allowlisted")
}

func normalizeSummary(input upstreamSummary, now time.Time) (SummaryResult, error) {
	generatedAt, err := time.Parse(time.RFC3339Nano, input.GeneratedAt)
	if err != nil {
		return SummaryResult{}, typed(ErrorBadPayload, fmt.Errorf("generatedAt: %w", err))
	}
	status, err := upstreamStatus(input.Status)
	if err != nil {
		return SummaryResult{}, typed(ErrorBadPayload, err)
	}
	if generatedAt.After(now.Add(2 * time.Second)) {
		return SummaryResult{}, typed(ErrorBadPayload, errors.New("future summary timestamp"))
	}
	if status == StatusFresh && input.IsDelayed {
		status = StatusStale
	} else if status == StatusFresh && input.EventCount24h == 0 {
		status = StatusEmpty
	}
	if input.EventCount24h < 0 || input.P0Count24h < 0 || input.P1Count24h < 0 || input.P0Count24h+input.P1Count24h > input.EventCount24h {
		return SummaryResult{}, typed(ErrorBadPayload, errors.New("invalid summary counts"))
	}
	if input.FreshnessMinutes != nil && *input.FreshnessMinutes < 0 {
		return SummaryResult{}, typed(ErrorBadPayload, errors.New("negative freshness"))
	}
	if input.Message != nil {
		if err := validateText(*input.Message, 1, 1000); err != nil {
			return SummaryResult{}, typed(ErrorBadPayload, err)
		}
	}
	if input.LatestEventAt != nil {
		latest, err := time.Parse(time.RFC3339Nano, *input.LatestEventAt)
		if err != nil || latest.After(generatedAt.Add(2*time.Second)) {
			return SummaryResult{}, typed(ErrorBadPayload, errors.New("invalid latest event timestamp"))
		}
	}
	if len(input.Sources) != len(allowedSummarySources) {
		return SummaryResult{}, typed(ErrorBadPayload, errors.New("incomplete source status set"))
	}
	sources := make([]SourceStatus, 0, len(input.Sources))
	seenSources := make(map[string]struct{}, len(input.Sources))
	for _, source := range input.Sources {
		if err := validateText(source.Source, 1, 100); err != nil {
			return SummaryResult{}, typed(ErrorBadPayload, err)
		}
		if _, allowed := allowedSummarySources[source.Source]; !allowed {
			return SummaryResult{}, typed(ErrorBadPayload, errors.New("unknown summary source"))
		}
		if _, duplicate := seenSources[source.Source]; duplicate {
			return SummaryResult{}, typed(ErrorBadPayload, errors.New("duplicate summary source"))
		}
		seenSources[source.Source] = struct{}{}
		if source.Health != "healthy" && source.Health != "degraded" && source.Health != "unconfigured" {
			return SummaryResult{}, typed(ErrorBadPayload, errors.New("invalid source status"))
		}
		if source.LastSuccessAt != nil {
			parsed, err := time.Parse(time.RFC3339Nano, *source.LastSuccessAt)
			if err != nil || parsed.After(generatedAt.Add(2*time.Second)) {
				return SummaryResult{}, typed(ErrorBadPayload, errors.New("invalid source timestamp"))
			}
		}
		if source.Message != nil {
			if err := validateText(*source.Message, 1, 500); err != nil {
				return SummaryResult{}, typed(ErrorBadPayload, err)
			}
		}
		sources = append(sources, SourceStatus{Source: source.Source, Status: SourceHealth(source.Health), LastSuccessAt: source.LastSuccessAt, Message: source.Message})
	}
	return SummaryResult{
		Status: status, GeneratedAt: generatedAt.UTC(), Message: input.Message,
		Data: Summary{
			LatestEventAt: input.LatestEventAt, FreshnessMinutes: input.FreshnessMinutes,
			IsDelayed: input.IsDelayed, EventCount24h: input.EventCount24h,
			P0Count24h: input.P0Count24h, P1Count24h: input.P1Count24h, Sources: sources,
		},
	}, nil
}

var allowedSummarySources = map[string]struct{}{
	"github_releases": {}, "sec_edgar": {}, "federal_reserve": {},
	"binance_market_data": {}, "qiu_market": {},
}

func (c *Client) accept(response fetched) {
	if response.candidate != nil {
		c.cache.put(*response.candidate)
	}
}

func cacheableStatus(status Status) bool {
	return status == StatusFresh || status == StatusEmpty || status == StatusLegacy
}

func upstreamStatus(value string) (Status, error) {
	switch value {
	case "healthy":
		return StatusFresh, nil
	case "degraded":
		return StatusDegraded, nil
	case "unconfigured":
		return StatusUnconfigured, nil
	default:
		return "", fmt.Errorf("invalid upstream status %q", value)
	}
}

func listStatus(upstream Status, items []Signal, partial bool) Status {
	if upstream == StatusDegraded || upstream == StatusUnconfigured {
		return upstream
	}
	if partial {
		return StatusPartial
	}
	if len(items) == 0 {
		return StatusEmpty
	}
	stale := 0
	legacy := 0
	for _, item := range items {
		if item.Freshness == FreshnessStale {
			stale++
		}
		if containsQuality(item.QualityFlags, "legacy_fields_missing") {
			legacy++
		}
	}
	if stale == len(items) {
		return StatusStale
	}
	if stale > 0 {
		return StatusPartial
	}
	if legacy == len(items) {
		return StatusLegacy
	}
	if legacy > 0 {
		return StatusPartial
	}
	return StatusFresh
}

func containsQuality(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func validateQuery(query EventQuery) error {
	if query.Market != "crypto" || query.Asset != "BTC" || query.Window != 168 {
		return errors.New("market=crypto, asset=BTC, and window=168 are required")
	}
	if query.Limit < 1 || query.Limit > 50 {
		return errors.New("limit must be within 1..50")
	}
	if query.Cursor != "" {
		return ValidateCursor(query.Cursor)
	}
	return nil
}

func ValidateCursor(value string) error {
	if len(value) < 1 || len(value) > 512 || strings.TrimSpace(value) != value {
		return errors.New("cursor must be opaque, trimmed, and at most 512 bytes")
	}
	for _, current := range value {
		if current < 0x21 || current == 0x7f {
			return errors.New("cursor contains control or whitespace")
		}
	}
	return nil
}

func ValidateEventID(value string) error {
	if !idPattern.MatchString(value) {
		return errors.New("invalid event id")
	}
	return nil
}

func decodeStrict(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("response must contain one JSON value")
	}
	return nil
}

func typed(code ErrorCode, cause error) *Error { return &Error{Code: code, Cause: cause} }

func isTimeout(err error) bool {
	var value net.Error
	return errors.As(err, &value) && value.Timeout()
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	const maximum = 15 * time.Minute
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 {
		result := time.Duration(seconds) * time.Second
		if result > maximum || result < 0 {
			return maximum
		}
		return result
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		result := date.Sub(now)
		if result > maximum {
			return maximum
		}
		return result
	}
	return 0
}

func boundedHeader(value string) string {
	if len(value) > 512 {
		return ""
	}
	return value
}
