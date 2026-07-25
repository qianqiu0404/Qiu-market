package markettime

import (
	"testing"
	"time"
)

func TestCloseTime(t *testing.T) {
	open := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	tests := map[string]time.Time{
		"1m":  open.Add(time.Minute),
		"15m": open.Add(15 * time.Minute),
		"1h":  open.Add(time.Hour),
		"1d":  open.Add(24 * time.Hour),
	}
	for interval, want := range tests {
		got, err := CloseTime(open, interval)
		if err != nil {
			t.Fatalf("CloseTime(%q): %v", interval, err)
		}
		if !got.Equal(want) {
			t.Fatalf("CloseTime(%q) = %v, want %v", interval, got, want)
		}
	}
}

func TestCloseTimeRejectsUnknownInterval(t *testing.T) {
	if _, err := CloseTime(time.Now(), "1w"); err == nil {
		t.Fatal("expected unsupported interval error")
	}
}
