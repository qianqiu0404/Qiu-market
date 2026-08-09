package researchsignal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const signalFreshnessTTL = 168 * time.Hour

var (
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$`)
	symbolPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,31}$`)
)

type upstreamSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type upstreamAsset struct {
	Namespace string      `json:"namespace"`
	Symbol    string      `json:"symbol"`
	Relevance json.Number `json:"relevance"`
}

type upstreamReaction struct {
	Status    string       `json:"status"`
	Benchmark *string      `json:"benchmark"`
	Return5m  *json.Number `json:"return5m"`
	Return30m *json.Number `json:"return30m"`
	Return4h  *json.Number `json:"return4h"`
	Excess5m  *json.Number `json:"excess5m"`
	Excess30m *json.Number `json:"excess30m"`
	Excess4h  *json.Number `json:"excess4h"`
}

type upstreamEvent struct {
	ID             string            `json:"id"`
	Slug           string            `json:"slug"`
	Market         string            `json:"market"`
	Priority       string            `json:"priority"`
	Score          json.Number       `json:"score"`
	TitleZH        string            `json:"titleZh"`
	SummaryZH      string            `json:"summaryZh"`
	WhyItMattersZH string            `json:"whyItMattersZh"`
	WatchFor       *string           `json:"watchFor"`
	Invalidation   *string           `json:"invalidation"`
	EventType      string            `json:"eventType"`
	NewsDirection  string            `json:"newsDirection"`
	SystemJudgment string            `json:"systemJudgment"`
	Horizon        string            `json:"horizon"`
	OccurredAt     string            `json:"occurredAt"`
	PublishedAt    string            `json:"publishedAt"`
	SourceCount    int64             `json:"sourceCount"`
	Sources        []upstreamSource  `json:"sources"`
	Assets         []upstreamAsset   `json:"assets"`
	Reaction       *upstreamReaction `json:"reaction"`
}

type upstreamList struct {
	Status     string          `json:"status"`
	Items      []upstreamEvent `json:"items"`
	NextCursor *string         `json:"nextCursor"`
	Message    *string         `json:"message"`
}

type upstreamSourceStatus struct {
	Source        string  `json:"source"`
	Health        string  `json:"health"`
	LastSuccessAt *string `json:"lastSuccessAt"`
	Message       *string `json:"message"`
}

type upstreamSummary struct {
	Status           string                 `json:"status"`
	GeneratedAt      string                 `json:"generatedAt"`
	LatestEventAt    *string                `json:"latestEventAt"`
	FreshnessMinutes *int64                 `json:"freshnessMinutes"`
	IsDelayed        bool                   `json:"isDelayed"`
	EventCount24h    int64                  `json:"eventCount24h"`
	P0Count24h       int64                  `json:"p0Count24h"`
	P1Count24h       int64                  `json:"p1Count24h"`
	Sources          []upstreamSourceStatus `json:"sources"`
	Message          *string                `json:"message"`
}

func normalizeEvent(input upstreamEvent, receivedAt, now time.Time) (Signal, error) {
	if !idPattern.MatchString(input.ID) {
		return Signal{}, fmt.Errorf("invalid event id")
	}
	if !idPattern.MatchString(input.Slug) {
		return Signal{}, fmt.Errorf("invalid event slug")
	}
	if input.Market != "crypto" {
		return Signal{}, fmt.Errorf("BTC research feed requires crypto market")
	}
	if input.Priority != "P0" && input.Priority != "P1" && input.Priority != "P2" {
		return Signal{}, fmt.Errorf("invalid editorial priority")
	}
	score, err := strconv.ParseInt(input.Score.String(), 10, 64)
	if err != nil || score < 0 || score > 100 {
		return Signal{}, fmt.Errorf("invalid score")
	}
	if err := validateText(input.TitleZH, 1, 300); err != nil {
		return Signal{}, fmt.Errorf("title: %w", err)
	}
	if err := validateText(input.SummaryZH, 1, 4000); err != nil {
		return Signal{}, fmt.Errorf("summary: %w", err)
	}
	if err := validateText(input.WhyItMattersZH, 1, 4000); err != nil {
		return Signal{}, fmt.Errorf("whyItMattersZh: %w", err)
	}
	if err := validateText(input.EventType, 1, 100); err != nil {
		return Signal{}, fmt.Errorf("eventType: %w", err)
	}
	if input.NewsDirection != "bullish" && input.NewsDirection != "bearish" && input.NewsDirection != "mixed" && input.NewsDirection != "neutral" {
		return Signal{}, fmt.Errorf("invalid newsDirection")
	}
	if err := validateText(input.SystemJudgment, 1, 1000); err != nil {
		return Signal{}, fmt.Errorf("systemJudgment: %w", err)
	}
	if input.Horizon != "intraday" && input.Horizon != "days" && input.Horizon != "weeks" {
		return Signal{}, fmt.Errorf("invalid horizon")
	}
	if len(input.Sources) == 0 || len(input.Sources) > 16 {
		return Signal{}, fmt.Errorf("invalid source count")
	}
	if input.SourceCount != int64(len(input.Sources)) {
		return Signal{}, fmt.Errorf("sourceCount does not match sources")
	}
	sources := append([]upstreamSource(nil), input.Sources...)
	seenSourceURLs := make(map[string]struct{}, len(sources))
	for index := range sources {
		if err := validateText(sources[index].Name, 1, 160); err != nil {
			return Signal{}, fmt.Errorf("source name: %w", err)
		}
		if err := validatePublicURL(sources[index].URL); err != nil {
			return Signal{}, fmt.Errorf("source url: %w", err)
		}
		if _, duplicate := seenSourceURLs[sources[index].URL]; duplicate {
			return Signal{}, fmt.Errorf("duplicate source url")
		}
		seenSourceURLs[sources[index].URL] = struct{}{}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].URL < sources[j].URL })
	assets := make([]string, 0, len(input.Assets))
	seenAssets := make(map[string]string, len(input.Assets))
	hasBTC := false
	for _, asset := range input.Assets {
		if asset.Namespace != "crypto" && asset.Namespace != "us_equity" && asset.Namespace != "macro" {
			return Signal{}, fmt.Errorf("invalid asset namespace")
		}
		symbol := strings.ToUpper(strings.TrimSpace(asset.Symbol))
		if !symbolPattern.MatchString(symbol) {
			return Signal{}, fmt.Errorf("invalid asset symbol")
		}
		relevance, err := strconv.ParseInt(asset.Relevance.String(), 10, 64)
		if err != nil || relevance < 0 || relevance > 100 {
			return Signal{}, fmt.Errorf("invalid asset relevance")
		}
		if namespace, ok := seenAssets[symbol]; ok && namespace != asset.Namespace {
			return Signal{}, fmt.Errorf("ambiguous asset namespace")
		}
		if _, ok := seenAssets[symbol]; !ok {
			seenAssets[symbol] = asset.Namespace
			assets = append(assets, symbol)
		}
		if asset.Namespace == "crypto" && symbol == "BTC" {
			hasBTC = true
		}
	}
	if len(assets) == 0 || len(assets) > 32 || !hasBTC {
		return Signal{}, fmt.Errorf("invalid assets")
	}
	if input.Reaction != nil {
		if err := validateReaction(*input.Reaction); err != nil {
			return Signal{}, err
		}
	}
	sort.Strings(assets)
	eventTime, err := time.Parse(time.RFC3339Nano, input.OccurredAt)
	if err != nil {
		return Signal{}, fmt.Errorf("event time: %w", err)
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, input.PublishedAt)
	if err != nil {
		return Signal{}, fmt.Errorf("published time: %w", err)
	}
	eventTime, publishedAt = eventTime.UTC(), publishedAt.UTC()
	if eventTime.After(now.Add(2*time.Second)) || publishedAt.After(receivedAt.Add(2*time.Second)) {
		return Signal{}, fmt.Errorf("future timestamp")
	}
	if publishedAt.Before(eventTime.Add(-2 * time.Second)) {
		return Signal{}, fmt.Errorf("published timestamp precedes event")
	}
	watchFor, err := normalizeNullableText(input.WatchFor, 1000)
	if err != nil {
		return Signal{}, fmt.Errorf("watchFor: %w", err)
	}
	invalidation, err := normalizeNullableText(input.Invalidation, 1000)
	if err != nil {
		return Signal{}, fmt.Errorf("invalidation: %w", err)
	}
	freshness := FreshnessFresh
	flags := []string{"observed_time_missing"}
	if watchFor == nil && invalidation == nil {
		flags = append(flags, "legacy_fields_missing")
	}
	if now.Sub(eventTime) > signalFreshnessTTL {
		freshness = FreshnessStale
		flags = append(flags, "stale")
	}
	received := receivedAt.UTC().Format(time.RFC3339Nano)
	result := Signal{
		ID: input.ID, SchemaVersion: ItemSchemaVersion, Type: "market_event",
		Title: input.TitleZH, Summary: input.SummaryZH,
		Source: "xiuqiu-site Market Radar", Provider: Provider,
		SourceURL: productionOrigin + "/market-radar/events/" + url.PathEscape(input.ID),
		Assets:    assets, EventTime: eventTime.Format(time.RFC3339Nano), ObservedAt: nil,
		ReceivedAt: received, PublishedAt: publishedAt.Format(time.RFC3339Nano),
		Freshness: freshness, Priority: input.Priority, WatchFor: watchFor,
		Invalidation: invalidation, QualityFlags: flags, Executable: false,
		SourceKind: SourceKind,
	}
	result.ContentHash, err = contentHash(result)
	return result, err
}

func contentHash(signal Signal) (string, error) {
	copyValue := signal
	copyValue.ReceivedAt = ""
	copyValue.Freshness = ""
	copyValue.QualityFlags = nil
	copyValue.ContentHash = ""
	payload, err := json.Marshal(copyValue)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func normalizeEvents(inputs []upstreamEvent, receivedAt, now time.Time) ([]Signal, bool, error) {
	items := make([]Signal, 0, len(inputs))
	byID := make(map[string]string, len(inputs))
	conflicted := make(map[string]struct{})
	partial := false
	for _, input := range inputs {
		item, err := normalizeEvent(input, receivedAt, now)
		if err != nil {
			return nil, false, err
		}
		if hash, ok := byID[item.ID]; ok {
			partial = true
			if hash == item.ContentHash && len(items) > 0 {
				for index := range items {
					if items[index].ID == item.ID {
						items[index].QualityFlags = append(items[index].QualityFlags, "duplicate")
						break
					}
				}
			} else {
				conflicted[item.ID] = struct{}{}
			}
			continue
		}
		byID[item.ID] = item.ContentHash
		items = append(items, item)
	}
	if len(conflicted) > 0 {
		filtered := items[:0]
		for _, item := range items {
			if _, conflict := conflicted[item.ID]; !conflict {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	return items, partial, nil
}

func validateReaction(value upstreamReaction) error {
	if value.Status != "pending" && value.Status != "confirmed" && value.Status != "priced_in" && value.Status != "ignored" && value.Status != "contradicted" {
		return fmt.Errorf("invalid reaction status")
	}
	if value.Benchmark != nil {
		if err := validateText(*value.Benchmark, 1, 80); err != nil {
			return fmt.Errorf("reaction benchmark: %w", err)
		}
	}
	for _, number := range []*json.Number{value.Return5m, value.Return30m, value.Return4h, value.Excess5m, value.Excess30m, value.Excess4h} {
		if number == nil {
			continue
		}
		if _, err := strconv.ParseFloat(number.String(), 64); err != nil {
			return fmt.Errorf("invalid reaction number")
		}
	}
	return nil
}

func validateText(value string, min, max int) error {
	if value != strings.TrimSpace(value) || len([]rune(value)) < min || len([]rune(value)) > max {
		return fmt.Errorf("must be trimmed and %d..%d characters", min, max)
	}
	for _, current := range value {
		if unicode.IsControl(current) && current != '\n' && current != '\t' {
			return fmt.Errorf("contains control character")
		}
	}
	return nil
}

func normalizeNullableText(value *string, max int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	if err := validateText(*value, 1, max); err != nil {
		return nil, err
	}
	copyValue := *value
	return &copyValue, nil
}

func validatePublicURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("must be an auditable HTTPS URL")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified()) {
		return fmt.Errorf("private address is forbidden")
	}
	return nil
}
