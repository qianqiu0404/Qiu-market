package providercontract

import (
	"sort"
	"sync"
	"time"
)

// Clock makes cache expiry and provider rate-limit windows deterministic in
// tests. Production callers may pass nil to use UTC wall-clock time.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

type cacheEntry struct {
	response  Response
	expiresAt time.Time
	lastUsed  uint64
}

// Cache is a bounded, concurrency-safe LRU cache. It stores successful
// responses only; routing failures never reach Put.
type Cache struct {
	mu       sync.Mutex
	capacity int
	clock    Clock
	entries  map[string]cacheEntry
	tick     uint64
}

func NewCache(capacity int, clock Clock) *Cache {
	if clock == nil {
		clock = wallClock{}
	}
	if capacity < 0 {
		capacity = 0
	}
	return &Cache{
		capacity: capacity,
		clock:    clock,
		entries:  make(map[string]cacheEntry, capacity),
	}
}

func (c *Cache) Get(key string) (Response, bool) {
	if c == nil || key == "" {
		return Response{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return Response{}, false
	}
	if !c.clock.Now().UTC().Before(entry.expiresAt) {
		delete(c.entries, key)
		return Response{}, false
	}
	c.tick++
	entry.lastUsed = c.tick
	c.entries[key] = entry
	return cloneResponse(entry.response), true
}

// Put inserts a response for ttl. A non-positive TTL deliberately disables
// storage for the key, which prevents accidentally serving indefinitely fresh
// data when a provider omits a freshness policy.
func (c *Cache) Put(key string, response Response, ttl time.Duration) {
	if c == nil || key == "" {
		return
	}
	if ttl <= 0 {
		c.putUntil(key, response, time.Time{})
		return
	}
	c.putUntil(key, response, c.clock.Now().UTC().Add(ttl))
}

func (c *Cache) putUntil(key string, response Response, expiresAt time.Time) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now().UTC()
	if c.capacity == 0 || expiresAt.IsZero() || !now.Before(expiresAt) {
		delete(c.entries, key)
		return
	}
	c.removeExpiredLocked(now)
	c.tick++
	c.entries[key] = cacheEntry{
		response:  cloneResponse(response),
		expiresAt: expiresAt.UTC(),
		lastUsed:  c.tick,
	}
	for len(c.entries) > c.capacity {
		c.evictOneLocked()
	}
}

func cloneResponse(response Response) Response {
	response.Meta = cloneMetadata(response.Meta)
	switch value := response.Value.(type) {
	case SpotTickerEnvelope:
		response.Value = cloneSpotTickerEnvelope(value)
	case *SpotTickerEnvelope:
		if value != nil {
			cloned := cloneSpotTickerEnvelope(*value)
			response.Value = &cloned
		}
	case OHLCVEnvelope:
		response.Value = cloneOHLCVEnvelope(value)
	case *OHLCVEnvelope:
		if value != nil {
			cloned := cloneOHLCVEnvelope(*value)
			response.Value = &cloned
		}
	case DerivativeSnapshotEnvelope:
		response.Value = cloneDerivativeEnvelope(value)
	case *DerivativeSnapshotEnvelope:
		if value != nil {
			cloned := cloneDerivativeEnvelope(*value)
			response.Value = &cloned
		}
	case SignalEnvelope:
		response.Value = cloneSignalEnvelope(value)
	case *SignalEnvelope:
		if value != nil {
			cloned := cloneSignalEnvelope(*value)
			response.Value = &cloned
		}
	}
	return response
}

func cloneMetadata(value Metadata) Metadata {
	if value.EventTime != nil {
		eventTime := *value.EventTime
		value.EventTime = &eventTime
	}
	if value.Quality != nil {
		quality := make([]QualityFlag, len(value.Quality))
		copy(quality, value.Quality)
		value.Quality = quality
	}
	return value
}

func cloneSpotTickerEnvelope(value SpotTickerEnvelope) SpotTickerEnvelope {
	value.Meta = cloneMetadata(value.Meta)
	value.Data.BidPrice = cloneDecimal(value.Data.BidPrice)
	value.Data.AskPrice = cloneDecimal(value.Data.AskPrice)
	value.Data.Open24h = cloneDecimal(value.Data.Open24h)
	value.Data.Change24hPct = cloneDecimal(value.Data.Change24hPct)
	value.Data.QuoteTurnover = cloneDecimal(value.Data.QuoteTurnover)
	return value
}

func cloneOHLCVEnvelope(value OHLCVEnvelope) OHLCVEnvelope {
	value.Meta = cloneMetadata(value.Meta)
	value.Data = append([]OHLCV(nil), value.Data...)
	return value
}

func cloneDerivativeEnvelope(value DerivativeSnapshotEnvelope) DerivativeSnapshotEnvelope {
	value.Meta = cloneMetadata(value.Meta)
	value.Data.MarkPrice = cloneDecimal(value.Data.MarkPrice)
	value.Data.IndexPrice = cloneDecimal(value.Data.IndexPrice)
	value.Data.FundingRate = cloneDecimal(value.Data.FundingRate)
	value.Data.OpenInterest = cloneDecimal(value.Data.OpenInterest)
	value.Data.LongLiquidations = cloneDecimal(value.Data.LongLiquidations)
	value.Data.ShortLiquidations = cloneDecimal(value.Data.ShortLiquidations)
	if value.Data.FundingIntervalSec != nil {
		interval := *value.Data.FundingIntervalSec
		value.Data.FundingIntervalSec = &interval
	}
	if value.Data.LiquidationWindowSec != nil {
		window := *value.Data.LiquidationWindowSec
		value.Data.LiquidationWindowSec = &window
	}
	return value
}

func cloneSignalEnvelope(value SignalEnvelope) SignalEnvelope {
	value.Meta = cloneMetadata(value.Meta)
	value.Data.Value = cloneDecimal(value.Data.Value)
	value.Data.Confidence = cloneDecimal(value.Data.Confidence)
	if value.Asset != nil {
		asset := *value.Asset
		value.Asset = &asset
	}
	if value.Market != nil {
		market := *value.Market
		value.Market = &market
	}
	return value
}

func cloneDecimal(value *DecimalValue) *DecimalValue {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeExpiredLocked(c.clock.Now().UTC())
	return len(c.entries)
}

func (c *Cache) Delete(key string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Keys returns a stable diagnostic view; it never exposes map iteration order.
func (c *Cache) Keys() []string {
	if c == nil {
		return []string{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeExpiredLocked(c.clock.Now().UTC())
	keys := make([]string, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c *Cache) removeExpiredLocked(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

func (c *Cache) evictOneLocked() {
	var victim string
	var oldest uint64
	first := true
	for key, entry := range c.entries {
		if first || entry.lastUsed < oldest || (entry.lastUsed == oldest && key < victim) {
			victim = key
			oldest = entry.lastUsed
			first = false
		}
	}
	if !first {
		delete(c.entries, victim)
	}
}
