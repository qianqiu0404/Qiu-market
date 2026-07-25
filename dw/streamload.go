package dw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/the-web3/s78-market-services/config"
)

// streamLoadResponse 是 Doris Stream Load 的响应体（字段只取我们关心的）。
type streamLoadResponse struct {
	TxnID                  int64  `json:"TxnId"`
	Label                  string `json:"Label"`
	Status                 string `json:"Status"`
	Message                string `json:"Message"`
	ErrorURL               string `json:"ErrorURL"`
	NumberLoadedRows       int64  `json:"NumberLoadedRows"`
	NumberFilteredRows     int64  `json:"NumberFilteredRows"`
	StreamLoadPutTimeMs    int64  `json:"StreamLoadPutTimeMs"`
	LoadTimeMs             int64  `json:"LoadTimeMs"`
	ExistingJobStatus      string `json:"ExistingJobStatus"`
	NumberTotalRows        int64  `json:"NumberTotalRows"`
	NumberSkippedRows      int64  `json:"NumberSkippedRows"`
	NumberUnselectedRows   int64  `json:"NumberUnselectedRows"`
	LoadBytes              int64  `json:"LoadBytes"`
	StreamLoadTimeMs       int64  `json:"StreamLoadTimeMs"`
	BeginTxnTimeMs         int64  `json:"BeginTxnTimeMs"`
	ReceiveDataTimeMs      int64  `json:"ReceiveDataTimeMs"`
	ReadDataTimeMs         int64  `json:"ReadDataTimeMs"`
	WriteDataTimeMs        int64  `json:"WriteDataTimeMs"`
	CommitAndPublishTimeMs int64  `json:"CommitAndPublishTimeMs"`
}

// StreamLoadError keeps the operator-facing Doris status, label, and a bounded
// error summary together. It deliberately excludes request URLs, credentials,
// payloads, and the remote ErrorURL because those can contain deployment
// details that do not belong in application logs.
type StreamLoadError struct {
	HTTPStatus int
	Label      string
	Status     string
	Summary    string
}

func (e *StreamLoadError) Error() string {
	return fmt.Sprintf(
		"stream load failed: http_status=%d status=%q label=%q summary=%q",
		e.HTTPStatus,
		e.Status,
		e.Label,
		e.Summary,
	)
}

// StreamLoader 通过 Doris FE 的 HTTP 端口执行 Stream Load。
//
// 两个 Doris 协议细节决定了实现方式：
//  1. FE 收到 Stream Load 请求后用 307 重定向到某个 BE。Go 默认的
//     http.Client 跟随重定向时会把 Authorization 视为敏感头丢弃（跨 host），
//     导致 BE 返回 401 —— 因此这里第一次请求禁用自动重定向，拿到 Location
//     后手工带全请求头重发。
//  2. Doris 要求 Expect: 100-continue：客户端先发请求头，等 FE 的 100/307
//     响应后再决定是否发送 body，配合上面的重定向刚好避免把整批数据发到
//     FE 又被丢弃（broken pipe）。
type StreamLoader struct {
	httpBase   string // http://<host>:<httpPort>
	database   string
	user       string
	password   string
	httpClient *http.Client
}

const streamLoadMaxAttempts = 3

func NewStreamLoader(cfg config.DorisConfig) *StreamLoader {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Doris FE answers the first Expect: 100-continue request with a redirect
	// before it consumes the body. Reusing that connection can leave Go and
	// Jetty disagreeing about the next request boundary and Doris returns a
	// bare HTML 400. A Stream Load is infrequent and batch-oriented, so one
	// fresh connection per FE/BE request is a deliberate reliability tradeoff.
	transport.DisableKeepAlives = true
	transport.ExpectContinueTimeout = 5 * time.Second
	return &StreamLoader{
		httpBase: fmt.Sprintf("http://%s:%d", cfg.Host, cfg.HttpPort),
		database: cfg.Database,
		user:     cfg.User,
		password: cfg.Password,
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
			// 禁用自动重定向：Doris FE 用 307 把 Stream Load 重定向到 BE，
			// 自动跟随会丢 Authorization 头，且 100-continue 语义下 body 可能
			// 已发出被截断。手工处理一次重定向即可。
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Load 把一批 JSON 行（JSON 数组）Stream Load 进指定表，返回加载行数。
// payload 必须是 JSON 数组（对应 strip_outer_array: true）。
func (l *StreamLoader) Load(ctx context.Context, table, label string, payload []byte) (int64, error) {
	var lastErr error
	for attempt := 1; attempt <= streamLoadMaxAttempts; attempt++ {
		loaded, err := l.loadOnce(ctx, table, label, payload)
		if err == nil {
			return loaded, nil
		}
		lastErr = err
		if attempt == streamLoadMaxAttempts || !retryableStreamLoadError(err) {
			return 0, err
		}
		delay := time.Duration(attempt*attempt) * 100 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, context.Cause(ctx)
		case <-timer.C:
		}
	}
	return 0, lastErr
}

func (l *StreamLoader) loadOnce(ctx context.Context, table, label string, payload []byte) (int64, error) {
	url := fmt.Sprintf("%s/api/%s/%s/_stream_load", l.httpBase, l.database, table)

	resp, err := l.doPut(ctx, url, label, payload)
	if err != nil {
		return 0, err
	}

	// FE 307 重定向到 BE：带全头重发到 Location
	if resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusPermanentRedirect {
		location := resp.Header.Get("Location")
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if location == "" {
			return 0, fmt.Errorf("stream load redirect (%d) without Location header", resp.StatusCode)
		}
		resp, err = l.doPut(ctx, location, label, payload)
		if err != nil {
			return 0, err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("read stream load response for label %q: %w", label, err)
	}

	var result streamLoadResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, &StreamLoadError{
			HTTPStatus: resp.StatusCode,
			Label:      label,
			Status:     "unparseable_response",
			Summary:    sanitizeStreamLoadSummary(string(body)),
		}
	}
	if result.Label == "" {
		result.Label = label
	}
	if resp.StatusCode != http.StatusOK {
		return 0, streamLoadResultError(resp.StatusCode, result, label)
	}

	switch result.Status {
	case "Success", "Publish Timeout":
		// Publish Timeout：数据已进 BE，发布超时只是可见性延迟，按成功处理
		return result.NumberLoadedRows, nil
	case "Label Already Exists":
		// label 幂等：同 label 已成功过，视为已同步，不算错误
		if result.ExistingJobStatus == "FINISHED" {
			return 0, nil
		}
		return 0, &StreamLoadError{
			HTTPStatus: resp.StatusCode,
			Label:      label,
			Status:     result.Status,
			Summary:    sanitizeStreamLoadSummary("existing job status: " + result.ExistingJobStatus),
		}
	default:
		return 0, streamLoadResultError(resp.StatusCode, result, label)
	}
}

func retryableStreamLoadError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var loadErr *StreamLoadError
	if errors.As(err, &loadErr) {
		if loadErr.Status == "unparseable_response" && loadErr.HTTPStatus == http.StatusBadRequest {
			// Jetty/BE occasionally emits a bare HTML 400 before a Stream Load
			// transaction exists. A parsed Doris "Fail" 400 is deterministic and
			// intentionally does not enter this branch.
			return true
		}
		return loadErr.HTTPStatus == http.StatusRequestTimeout ||
			loadErr.HTTPStatus == http.StatusTooManyRequests ||
			loadErr.HTTPStatus >= http.StatusInternalServerError
	}
	// Network-level failures are safe to retry with the same deterministic
	// label: Doris reports a completed prior attempt as Label Already Exists.
	return true
}

func streamLoadResultError(httpStatus int, result streamLoadResponse, fallbackLabel string) error {
	label := result.Label
	if label == "" {
		label = fallbackLabel
	}
	summary := result.Message
	if summary == "" {
		summary = http.StatusText(httpStatus)
	}
	return &StreamLoadError{
		HTTPStatus: httpStatus,
		Label:      label,
		Status:     result.Status,
		Summary:    sanitizeStreamLoadSummary(summary),
	}
}

func sanitizeStreamLoadSummary(value string) string {
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t':
			return ' '
		default:
			return r
		}
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	return truncate(value, 300)
}

func (l *StreamLoader) doPut(ctx context.Context, url, label string, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Expect", "100-continue")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("format", "json")
	req.Header.Set("strip_outer_array", "true")
	req.Header.Set("label", label)
	req.SetBasicAuth(l.user, l.password)
	return l.httpClient.Do(req)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
