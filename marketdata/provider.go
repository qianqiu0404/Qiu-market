package marketdata

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/the-web3/s78-market-services/database"
)

type ProviderReporter struct {
	store database.ProviderStatusDB
}

func NewProviderReporter(store database.ProviderStatusDB) *ProviderReporter {
	return &ProviderReporter{store: store}
}

func (r *ProviderReporter) Attempt(provider, sourceKey string, at time.Time) {
	if r == nil || r.store == nil {
		return
	}
	if err := r.store.RecordAttempt(provider, sourceKey, at.UTC()); err != nil {
		log.Warn("provider attempt status write failed", "provider", provider, "source_key", sourceKey, "error", err)
	}
}

func (r *ProviderReporter) Success(provider, sourceKey string, at time.Time, sourceTime *time.Time) {
	if r == nil || r.store == nil {
		return
	}
	if err := r.store.RecordSuccess(provider, sourceKey, at.UTC(), sourceTime); err != nil {
		log.Warn("provider success status write failed", "provider", provider, "source_key", sourceKey, "error", err)
	}
}

func (r *ProviderReporter) SuccessWithDetails(
	provider, sourceKey string,
	at time.Time,
	sourceTime *time.Time,
	details database.ProviderStatusDetails,
) {
	if r == nil || r.store == nil {
		return
	}
	if err := r.store.RecordSuccessWithDetails(
		provider, sourceKey, at.UTC(), sourceTime, details,
	); err != nil {
		log.Warn("provider success details write failed",
			"provider", provider, "source_key", sourceKey, "error", err)
	}
}

func (r *ProviderReporter) Failure(provider, sourceKey string, at time.Time, err error, statusCode int) {
	if r == nil || r.store == nil {
		return
	}
	class := ClassifyProviderError(err, statusCode)
	summary := "provider request failed"
	if err != nil {
		summary = err.Error()
	} else if statusCode > 0 {
		summary = fmt.Sprintf("HTTP %d %s", statusCode, http.StatusText(statusCode))
	}
	if writeErr := r.store.RecordFailure(provider, sourceKey, at.UTC(), class, summary); writeErr != nil {
		log.Warn("provider failure status write failed", "provider", provider, "source_key", sourceKey, "error", writeErr)
	}
}

func (r *ProviderReporter) NextRetry(provider, sourceKey string, at time.Time) {
	if r == nil || r.store == nil {
		return
	}
	value := at.UTC()
	if err := r.store.SetNextRetry(provider, sourceKey, &value); err != nil {
		log.Warn("provider retry status write failed", "provider", provider, "source_key", sourceKey, "error", err)
	}
}

func ClassifyProviderError(err error, statusCode int) string {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return "rate_limited"
	case statusCode >= 500:
		return "upstream_5xx"
	case statusCode >= 400:
		return "upstream_4xx"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return "timeout"
		}
		return "network"
	}
	message := strings.ToLower(fmt.Sprint(err))
	switch {
	case strings.Contains(message, "unconfigured"):
		return "unconfigured"
	case strings.Contains(message, "decode"), strings.Contains(message, "json"), strings.Contains(message, "payload"):
		return "invalid_response"
	case err != nil:
		return "request_error"
	default:
		return "unknown"
	}
}
