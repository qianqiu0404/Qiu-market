package markettime

import (
	"fmt"
	"time"
)

// Duration returns the fixed duration of every K-line interval currently
// supported by S78. A future non-continuous trading calendar must not be added
// here as a guessed duration; it needs an explicit close-time design.
func Duration(interval string) (time.Duration, error) {
	switch interval {
	case "1m":
		return time.Minute, nil
	case "15m":
		return 15 * time.Minute, nil
	case "1h":
		return time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported kline interval %q", interval)
	}
}

// CloseTime derives the exclusive close boundary from the business open time.
func CloseTime(openTime time.Time, interval string) (time.Time, error) {
	duration, err := Duration(interval)
	if err != nil {
		return time.Time{}, err
	}
	return openTime.Add(duration), nil
}
