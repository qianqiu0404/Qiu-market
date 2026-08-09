// Package researchsignals exposes the read-only research signal contract over
// HTTP. It has no trading service, account, order, or ledger dependency.
package researchsignals

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
)

const (
	SummaryPath = "/api/v1/research/signals/summary"
	EventsPath  = "/api/v1/research/signals/events"
	EventPath   = "/api/v1/research/signals/events/{id}"
)

type Handler struct {
	reader researchsignal.Reader
	now    func() time.Time
}

type envelope[T any] struct {
	SchemaVersion string                    `json:"schemaVersion"`
	Status        researchsignal.Status     `json:"status"`
	GeneratedAt   string                    `json:"generatedAt"`
	Data          T                         `json:"data"`
	Message       *string                   `json:"message"`
	Error         *researchsignal.WireError `json:"error"`
}

func New(reader researchsignal.Reader) *Handler {
	return &Handler{reader: reader, now: time.Now}
}

func Mount(router chi.Router, reader researchsignal.Reader) {
	handler := New(reader)
	router.Get(SummaryPath, handler.summary)
	router.Get(EventsPath, handler.events)
	router.Get(EventPath, handler.event)
}

func (h *Handler) summary(writer http.ResponseWriter, request *http.Request) {
	if h.reader == nil {
		h.writeError(writer, researchsignal.Summary{}, researchsignal.StatusUnconfigured, &researchsignal.Error{Code: researchsignal.ErrorDisabled}, nil)
		return
	}
	result, err := h.reader.Summary(request.Context())
	if err != nil {
		h.writeError(writer, researchsignal.Summary{Sources: []researchsignal.SourceStatus{}}, statusForError(err), err, nil)
		return
	}
	statusCode, wireError := statusHTTP(result.Status)
	h.write(writer, statusCode, envelope[researchsignal.Summary]{
		SchemaVersion: researchsignal.SchemaVersion, Status: result.Status,
		GeneratedAt: result.GeneratedAt.UTC().Format(time.RFC3339Nano), Data: result.Data,
		Message: result.Message, Error: wireError,
	})
}

func (h *Handler) events(writer http.ResponseWriter, request *http.Request) {
	query, err := parseEventQuery(request)
	if err != nil {
		h.writeError(writer, researchsignal.EventList{Items: []researchsignal.Signal{}}, researchsignal.StatusDegraded, &researchsignal.Error{Code: researchsignal.ErrorBadRequest, Cause: err}, nil)
		return
	}
	if status, message, gateErr := h.gate(request.Context()); gateErr != nil {
		h.writeError(writer, researchsignal.EventList{Items: []researchsignal.Signal{}}, status, gateErr, message)
		return
	}
	result, err := h.reader.Events(request.Context(), query)
	if err != nil {
		h.writeError(writer, researchsignal.EventList{Items: []researchsignal.Signal{}}, researchsignal.StatusDegraded, err, nil)
		return
	}
	statusCode, wireError := statusHTTP(result.Status)
	h.write(writer, statusCode, envelope[researchsignal.EventList]{
		SchemaVersion: researchsignal.SchemaVersion, Status: result.Status,
		GeneratedAt: result.GeneratedAt.UTC().Format(time.RFC3339Nano), Data: result.Data,
		Message: result.Message, Error: wireError,
	})
}

func (h *Handler) event(writer http.ResponseWriter, request *http.Request) {
	id := chi.URLParam(request, "id")
	if err := researchsignal.ValidateEventID(id); err != nil {
		h.writeError(writer, researchsignal.EventDetail{Item: nil}, researchsignal.StatusDegraded, &researchsignal.Error{Code: researchsignal.ErrorBadRequest, Cause: err}, nil)
		return
	}
	if status, message, gateErr := h.gate(request.Context()); gateErr != nil {
		h.writeError(writer, researchsignal.EventDetail{Item: nil}, status, gateErr, message)
		return
	}
	result, err := h.reader.Event(request.Context(), id)
	if err != nil {
		status := researchsignal.StatusDegraded
		var typed *researchsignal.Error
		if errors.As(err, &typed) && typed.Code == researchsignal.ErrorNotFound {
			status = researchsignal.StatusEmpty
		}
		h.writeError(writer, researchsignal.EventDetail{Item: nil}, status, err, nil)
		return
	}
	statusCode, wireError := statusHTTP(result.Status)
	h.write(writer, statusCode, envelope[researchsignal.EventDetail]{
		SchemaVersion: researchsignal.SchemaVersion, Status: result.Status,
		GeneratedAt: result.GeneratedAt.UTC().Format(time.RFC3339Nano), Data: result.Data,
		Message: result.Message, Error: wireError,
	})
}

func (h *Handler) gate(ctx context.Context) (researchsignal.Status, *string, error) {
	if h.reader == nil {
		return researchsignal.StatusUnconfigured, nil, &researchsignal.Error{Code: researchsignal.ErrorDisabled}
	}
	summary, err := h.reader.Summary(ctx)
	if err != nil {
		return statusForError(err), nil, err
	}
	switch summary.Status {
	case researchsignal.StatusDegraded, researchsignal.StatusUnconfigured:
		return summary.Status, summary.Message, &researchsignal.Error{Code: researchsignal.ErrorUpstream, Cause: errors.New("research source is not publishable")}
	default:
		return summary.Status, summary.Message, nil
	}
}

func parseEventQuery(request *http.Request) (researchsignal.EventQuery, error) {
	values := request.URL.Query()
	allowed := map[string]bool{"market": true, "asset": true, "window": true, "limit": true, "cursor": true}
	for key, items := range values {
		if !allowed[key] || len(items) != 1 {
			return researchsignal.EventQuery{}, errors.New("query contains an unknown or repeated field")
		}
	}
	if values.Get("market") != "crypto" || values.Get("asset") != "BTC" || values.Get("window") != "168" {
		return researchsignal.EventQuery{}, errors.New("market=crypto, asset=BTC, and window=168 are required")
	}
	limit, err := strconv.Atoi(values.Get("limit"))
	if err != nil || limit < 1 || limit > 50 {
		return researchsignal.EventQuery{}, errors.New("limit must be within 1..50")
	}
	if cursor := values.Get("cursor"); cursor != "" {
		if err := researchsignal.ValidateCursor(cursor); err != nil {
			return researchsignal.EventQuery{}, err
		}
	}
	return researchsignal.EventQuery{
		Market: "crypto", Asset: "BTC", Window: 168, Limit: limit, Cursor: values.Get("cursor"),
	}, nil
}

func statusHTTP(status researchsignal.Status) (int, *researchsignal.WireError) {
	switch status {
	case researchsignal.StatusFresh, researchsignal.StatusEmpty, researchsignal.StatusLegacy, researchsignal.StatusStale:
		return http.StatusOK, nil
	case researchsignal.StatusPartial:
		return http.StatusOK, nil
	case researchsignal.StatusUnconfigured:
		return http.StatusOK, wire(researchsignal.ErrorDisabled, 0)
	case researchsignal.StatusDegraded:
		return http.StatusOK, wire(researchsignal.ErrorUpstream, 0)
	default:
		return http.StatusOK, wire(researchsignal.ErrorBadPayload, 0)
	}
}

func (h *Handler) writeError(writer http.ResponseWriter, data any, status researchsignal.Status, err error, message *string) {
	httpStatus := http.StatusOK
	code := researchsignal.ErrorUpstream
	var retryAfter time.Duration
	var typed *researchsignal.Error
	if errors.As(err, &typed) {
		code = typed.Code
		retryAfter = typed.RetryAfter
		switch code {
		case researchsignal.ErrorBadRequest:
			httpStatus = http.StatusBadRequest
		case researchsignal.ErrorNotFound:
			httpStatus = http.StatusNotFound
		default:
			httpStatus = http.StatusOK
		}
	}
	if retryAfter > 0 {
		seconds := int64(retryAfter.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	h.write(writer, httpStatus, envelope[any]{
		SchemaVersion: researchsignal.SchemaVersion, Status: status,
		GeneratedAt: h.now().UTC().Format(time.RFC3339Nano), Data: data,
		Message: message, Error: wire(code, retryAfter),
	})
}

func statusForError(err error) researchsignal.Status {
	var typed *researchsignal.Error
	if errors.As(err, &typed) && typed.Code == researchsignal.ErrorDisabled {
		return researchsignal.StatusUnconfigured
	}
	return researchsignal.StatusDegraded
}

func wire(code researchsignal.ErrorCode, retryAfter time.Duration) *researchsignal.WireError {
	messages := map[researchsignal.ErrorCode]string{
		researchsignal.ErrorDisabled:   "Research signals are not configured.",
		researchsignal.ErrorBadRequest: "The research signal request is invalid.",
		researchsignal.ErrorNotFound:   "The research event was not found.",
		researchsignal.ErrorRateLimit:  "The research source is rate limited.",
		researchsignal.ErrorTimeout:    "The research source timed out.",
		researchsignal.ErrorNetwork:    "The research source is unavailable.",
		researchsignal.ErrorUpstream:   "The research source is degraded.",
		researchsignal.ErrorBadPayload: "The research source returned an invalid contract.",
		researchsignal.ErrorConflict:   "The research source returned conflicting facts.",
	}
	result := &researchsignal.WireError{Code: code, Message: messages[code]}
	if retryAfter > 0 {
		seconds := int64(retryAfter.Round(time.Second) / time.Second)
		result.RetryAfterSeconds = &seconds
	}
	return result
}

func (h *Handler) write(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
