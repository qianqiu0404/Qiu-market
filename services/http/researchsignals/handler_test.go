package researchsignals

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/the-web3/s78-market-services/marketdata/researchsignal"
)

type fakeReader struct {
	summary      researchsignal.SummaryResult
	list         researchsignal.ListResult
	detail       researchsignal.DetailResult
	summaryError error
	listError    error
	detailError  error
	summaryCalls int
	eventCalls   int
}

func (f *fakeReader) Summary(context.Context) (researchsignal.SummaryResult, error) {
	f.summaryCalls++
	return f.summary, f.summaryError
}

func TestInvalidInputMakesNoUpstreamCall(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{}
	badCursor := request(t, reader, EventsPath+"?market=crypto&asset=BTC&window=168&limit=20&cursor=bad%20cursor")
	if badCursor.Code != http.StatusBadRequest || reader.summaryCalls != 0 || reader.eventCalls != 0 {
		t.Fatalf("cursor status=%d summary=%d events=%d", badCursor.Code, reader.summaryCalls, reader.eventCalls)
	}
	badID := request(t, reader, EventsPath+"/bad%20id")
	if badID.Code != http.StatusBadRequest || reader.summaryCalls != 0 || reader.eventCalls != 0 {
		t.Fatalf("id status=%d summary=%d events=%d", badID.Code, reader.summaryCalls, reader.eventCalls)
	}
}

func TestStaleSummaryAllowsReadOnlyItems(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	reader := &fakeReader{
		summary: researchsignal.SummaryResult{Status: researchsignal.StatusStale, GeneratedAt: now},
		list:    researchsignal.ListResult{Status: researchsignal.StatusStale, GeneratedAt: now, Data: researchsignal.EventList{Items: []researchsignal.Signal{{ID: "stale", Executable: false}}}},
	}
	response := request(t, reader, EventsPath+"?market=crypto&asset=BTC&window=168&limit=20")
	if response.Code != http.StatusOK || reader.eventCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, reader.eventCalls, response.Body.String())
	}
}

func TestDisabledIsExplicitUnconfiguredNotEmpty(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{summaryError: &researchsignal.Error{Code: researchsignal.ErrorDisabled}}
	response := request(t, reader, SummaryPath)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Status researchsignal.Status     `json:"status"`
		Error  *researchsignal.WireError `json:"error"`
	}
	decode(t, response, &body)
	if body.Status != researchsignal.StatusUnconfigured || body.Error == nil || body.Error.Code != researchsignal.ErrorDisabled {
		t.Fatalf("body=%+v", body)
	}
}

func TestDisabledEventsAreUnconfiguredWithoutEventCall(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{summaryError: &researchsignal.Error{Code: researchsignal.ErrorDisabled}}
	response := request(t, reader, EventsPath+"?market=crypto&asset=BTC&window=168&limit=20")
	if response.Code != http.StatusOK || reader.eventCalls != 0 {
		t.Fatalf("status=%d eventCalls=%d body=%s", response.Code, reader.eventCalls, response.Body.String())
	}
	var body struct {
		Status researchsignal.Status     `json:"status"`
		Error  *researchsignal.WireError `json:"error"`
	}
	decode(t, response, &body)
	if body.Status != researchsignal.StatusUnconfigured || body.Error == nil || body.Error.Code != researchsignal.ErrorDisabled {
		t.Fatalf("body=%+v", body)
	}
}

func (f *fakeReader) Events(context.Context, researchsignal.EventQuery) (researchsignal.ListResult, error) {
	f.eventCalls++
	return f.list, f.listError
}

func (f *fakeReader) Event(context.Context, string) (researchsignal.DetailResult, error) {
	f.eventCalls++
	return f.detail, f.detailError
}

func TestEventsGateDoesNotPublishDegradedSource(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	message := "source delayed"
	reader := &fakeReader{summary: researchsignal.SummaryResult{Status: researchsignal.StatusDegraded, GeneratedAt: now, Message: &message}}
	response := request(t, reader, EventsPath+"?market=crypto&asset=BTC&window=168&limit=20")
	if response.Code != http.StatusOK || reader.eventCalls != 0 {
		t.Fatalf("status=%d eventCalls=%d body=%s", response.Code, reader.eventCalls, response.Body.String())
	}
	var body struct {
		Status researchsignal.Status `json:"status"`
		Data   struct {
			Items      []researchsignal.Signal `json:"items"`
			NextCursor *string                 `json:"nextCursor"`
		} `json:"data"`
		Error *researchsignal.WireError `json:"error"`
	}
	decode(t, response, &body)
	if body.Status != researchsignal.StatusDegraded || body.Error == nil || body.Data.Items == nil || len(body.Data.Items) != 0 || body.Data.NextCursor != nil {
		t.Fatalf("unexpected gated body: %+v", body)
	}
}

func TestEventsStatusAndQueryMatrix(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	reader := &fakeReader{
		summary: researchsignal.SummaryResult{Status: researchsignal.StatusFresh, GeneratedAt: now},
		list:    researchsignal.ListResult{Status: researchsignal.StatusPartial, GeneratedAt: now, Data: researchsignal.EventList{Items: []researchsignal.Signal{}}},
	}
	response := request(t, reader, EventsPath+"?market=crypto&asset=BTC&window=168&limit=50&cursor=opaque%7Ccursor")
	if response.Code != http.StatusOK || reader.eventCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, reader.eventCalls, response.Body.String())
	}
	var body struct {
		Status researchsignal.Status     `json:"status"`
		Error  *researchsignal.WireError `json:"error"`
	}
	decode(t, response, &body)
	if body.Status != researchsignal.StatusPartial || body.Error != nil {
		t.Fatalf("partial response=%+v", body)
	}

	bad := request(t, reader, EventsPath+"?market=crypto&asset=BTC&window=169&limit=50")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad query status=%d body=%s", bad.Code, bad.Body.String())
	}
}

func TestDetailNotFoundKeepsExplicitNull(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	reader := &fakeReader{
		summary:     researchsignal.SummaryResult{Status: researchsignal.StatusFresh, GeneratedAt: now},
		detailError: &researchsignal.Error{Code: researchsignal.ErrorNotFound, Cause: errors.New("fixture missing")},
	}
	response := request(t, reader, EventsPath+"/missing")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	decode(t, response, &body)
	data, ok := body["data"].(map[string]any)
	if !ok || data["item"] != nil {
		t.Fatalf("detail null semantics changed: %#v", body)
	}
}

func TestSummarySourceHealthWire(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	reader := &fakeReader{summary: researchsignal.SummaryResult{
		Status: researchsignal.StatusFresh, GeneratedAt: now,
		Data: researchsignal.Summary{Sources: []researchsignal.SourceStatus{{Source: "github_releases", Status: researchsignal.SourceHealthy}}},
	}}
	response := request(t, reader, SummaryPath)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	decode(t, response, &body)
	data := body["data"].(map[string]any)
	sources := data["sources"].([]any)
	if sources[0].(map[string]any)["status"] != "healthy" || body["error"] != nil {
		t.Fatalf("summary wire=%#v", body)
	}
}

func request(t *testing.T, reader researchsignal.Reader, target string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	Mount(router, reader)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

func decode(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}
