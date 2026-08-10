package researchsignal

import (
	"sync"
	"time"
)

type cacheEntry struct {
	key          string
	body         []byte
	etag         string
	lastModified string
	storedAt     time.Time
	receivedAt   time.Time
}

type responseCache struct {
	mu      sync.Mutex
	limit   int
	ttl     time.Duration
	entries map[string]cacheEntry
	order   []string
}

func newResponseCache(limit int, ttl time.Duration) *responseCache {
	return &responseCache{limit: limit, ttl: ttl, entries: make(map[string]cacheEntry, limit)}
}

func (c *responseCache) get(key string, now time.Time) (cacheEntry, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false, false
	}
	c.touch(key)
	return entry, true, now.Sub(entry.storedAt) <= c.ttl
}

func (c *responseCache) put(entry cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.body = append([]byte(nil), entry.body...)
	if _, ok := c.entries[entry.key]; !ok && len(c.entries) >= c.limit {
		oldest := c.order[0]
		delete(c.entries, oldest)
		c.order = c.order[1:]
	}
	c.entries[entry.key] = entry
	c.touch(entry.key)
}

func (c *responseCache) refresh(key string, now time.Time) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	entry.storedAt = now
	entry.receivedAt = now
	c.entries[key] = entry
	c.touch(key)
	return entry, true
}

func (c *responseCache) touch(key string) {
	for index, candidate := range c.order {
		if candidate == key {
			c.order = append(c.order[:index], c.order[index+1:]...)
			break
		}
	}
	c.order = append(c.order, key)
}
