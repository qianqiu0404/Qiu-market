package httpapi

import (
	"sync"
	"time"
)

type limitWindow struct {
	start time.Time
	count int
}

type writeLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	now     func() time.Time
	entries map[string]limitWindow
}

func newWriteLimiter(max int, window time.Duration) *writeLimiter {
	return &writeLimiter{
		max:     max,
		window:  window,
		now:     time.Now,
		entries: make(map[string]limitWindow),
	}
}

func (l *writeLimiter) allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.entries[key]
	if current.start.IsZero() || now.Sub(current.start) >= l.window {
		l.entries[key] = limitWindow{start: now, count: 1}
		return true
	}
	if current.count >= l.max {
		return false
	}
	current.count++
	l.entries[key] = current
	return true
}
