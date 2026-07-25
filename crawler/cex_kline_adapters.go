package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type normalizedCEXKline struct {
	OpenTime time.Time
	Open     string
	High     string
	Low      string
	Close    string
	Volume   string
}

type cexKlineAdapter interface {
	Provider() string
	PageLimit() int
	Fetch1m(context.Context, string, time.Time, time.Time, int) ([]normalizedCEXKline, error)
}

type baseCEXKlineAdapter struct {
	client  *http.Client
	baseURL string
}

func (a baseCEXKlineAdapter) get(
	ctx context.Context,
	endpoint string,
	target any,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "S78-Qiu-Market/1.0")
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return fmt.Errorf("K-line HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode K-line payload: %w", err)
	}
	return nil
}

type binanceKlineAdapter struct{ baseCEXKlineAdapter }

func (*binanceKlineAdapter) Provider() string { return "binance" }
func (*binanceKlineAdapter) PageLimit() int   { return 1000 }

func (a *binanceKlineAdapter) Fetch1m(
	ctx context.Context,
	symbol string,
	start, end time.Time,
	limit int,
) ([]normalizedCEXKline, error) {
	values := url.Values{
		"symbol":    []string{symbol},
		"interval":  []string{"1m"},
		"startTime": []string{strconv.FormatInt(start.UnixMilli(), 10)},
		"endTime":   []string{strconv.FormatInt(end.Add(-time.Millisecond).UnixMilli(), 10)},
		"limit":     []string{strconv.Itoa(limit)},
	}
	var payload [][]json.RawMessage
	if err := a.get(ctx, a.baseURL+"/api/v3/klines?"+values.Encode(), &payload); err != nil {
		return nil, err
	}
	return normalizeRawKlineRows(payload, klineRawLayout{
		TimeIndex: 0, OpenIndex: 1, HighIndex: 2, LowIndex: 3,
		CloseIndex: 4, VolumeIndex: 5, TimestampUnit: time.Millisecond,
	}, start, end)
}

type coinbaseKlineAdapter struct{ baseCEXKlineAdapter }

func (*coinbaseKlineAdapter) Provider() string { return "coinbase" }
func (*coinbaseKlineAdapter) PageLimit() int   { return 300 }

func (a *coinbaseKlineAdapter) Fetch1m(
	ctx context.Context,
	symbol string,
	start, end time.Time,
	limit int,
) ([]normalizedCEXKline, error) {
	values := url.Values{
		"granularity": []string{"60"},
		"start":       []string{start.UTC().Format(time.RFC3339)},
		"end":         []string{end.UTC().Format(time.RFC3339)},
	}
	var payload [][]json.RawMessage
	endpoint := fmt.Sprintf(
		"%s/products/%s/candles?%s",
		a.baseURL, url.PathEscape(symbol), values.Encode(),
	)
	if err := a.get(ctx, endpoint, &payload); err != nil {
		return nil, err
	}
	return normalizeRawKlineRows(payload, klineRawLayout{
		TimeIndex: 0, LowIndex: 1, HighIndex: 2, OpenIndex: 3,
		CloseIndex: 4, VolumeIndex: 5, TimestampUnit: time.Second,
	}, start, end)
}

type bybitKlineAdapter struct{ baseCEXKlineAdapter }

func (*bybitKlineAdapter) Provider() string { return "bybit" }
func (*bybitKlineAdapter) PageLimit() int   { return 1000 }

func (a *bybitKlineAdapter) Fetch1m(
	ctx context.Context,
	symbol string,
	start, end time.Time,
	limit int,
) ([]normalizedCEXKline, error) {
	values := url.Values{
		"category": []string{"spot"},
		"symbol":   []string{symbol},
		"interval": []string{"1"},
		"start":    []string{strconv.FormatInt(start.UnixMilli(), 10)},
		"end":      []string{strconv.FormatInt(end.Add(-time.Millisecond).UnixMilli(), 10)},
		"limit":    []string{strconv.Itoa(limit)},
	}
	var payload struct {
		ReturnCode int    `json:"retCode"`
		Message    string `json:"retMsg"`
		Result     struct {
			List [][]json.RawMessage `json:"list"`
		} `json:"result"`
	}
	if err := a.get(ctx, a.baseURL+"/v5/market/kline?"+values.Encode(), &payload); err != nil {
		return nil, err
	}
	if payload.ReturnCode != 0 {
		return nil, fmt.Errorf("Bybit K-line error %d: %s", payload.ReturnCode, payload.Message)
	}
	return normalizeRawKlineRows(payload.Result.List, klineRawLayout{
		TimeIndex: 0, OpenIndex: 1, HighIndex: 2, LowIndex: 3,
		CloseIndex: 4, VolumeIndex: 5, TimestampUnit: time.Millisecond,
	}, start, end)
}

type okxKlineAdapter struct{ baseCEXKlineAdapter }

func (*okxKlineAdapter) Provider() string { return "okx" }
func (*okxKlineAdapter) PageLimit() int   { return 300 }

func (a *okxKlineAdapter) Fetch1m(
	ctx context.Context,
	symbol string,
	start, end time.Time,
	limit int,
) ([]normalizedCEXKline, error) {
	values := url.Values{
		"instId": []string{symbol},
		"bar":    []string{"1m"},
		"before": []string{strconv.FormatInt(start.UnixMilli(), 10)},
		"after":  []string{strconv.FormatInt(end.UnixMilli(), 10)},
		"limit":  []string{strconv.Itoa(limit)},
	}
	var payload struct {
		Code string              `json:"code"`
		Msg  string              `json:"msg"`
		Data [][]json.RawMessage `json:"data"`
	}
	if err := a.get(ctx, a.baseURL+"/api/v5/market/history-candles?"+values.Encode(), &payload); err != nil {
		return nil, err
	}
	if payload.Code != "0" {
		return nil, fmt.Errorf("OKX K-line error %s: %s", payload.Code, payload.Msg)
	}
	return normalizeRawKlineRows(payload.Data, klineRawLayout{
		TimeIndex: 0, OpenIndex: 1, HighIndex: 2, LowIndex: 3,
		CloseIndex: 4, VolumeIndex: 5, TimestampUnit: time.Millisecond,
	}, start, end)
}

type klineRawLayout struct {
	TimeIndex     int
	OpenIndex     int
	HighIndex     int
	LowIndex      int
	CloseIndex    int
	VolumeIndex   int
	TimestampUnit time.Duration
}

func normalizeRawKlineRows(
	rows [][]json.RawMessage,
	layout klineRawLayout,
	start, end time.Time,
) ([]normalizedCEXKline, error) {
	maxIndex := layout.TimeIndex
	for _, index := range []int{
		layout.OpenIndex, layout.HighIndex, layout.LowIndex,
		layout.CloseIndex, layout.VolumeIndex,
	} {
		if index > maxIndex {
			maxIndex = index
		}
	}
	result := make([]normalizedCEXKline, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if len(row) <= maxIndex {
			continue
		}
		timestampText, err := rawKlineValue(row[layout.TimeIndex])
		if err != nil {
			return nil, err
		}
		timestamp, err := strconv.ParseInt(strings.TrimSpace(timestampText), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse K-line timestamp %q: %w", timestampText, err)
		}
		if layout.TimestampUnit == time.Second {
			timestamp *= 1000
		}
		openTime := time.UnixMilli(timestamp).UTC().Truncate(time.Minute)
		if openTime.Before(start.UTC()) || !openTime.Before(end.UTC()) {
			continue
		}
		if _, duplicate := seen[openTime.UnixMilli()]; duplicate {
			continue
		}
		values := make([]string, 5)
		for index, rawIndex := range []int{
			layout.OpenIndex, layout.HighIndex, layout.LowIndex,
			layout.CloseIndex, layout.VolumeIndex,
		} {
			values[index], err = rawKlineValue(row[rawIndex])
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(values[index]) == "" {
				return nil, fmt.Errorf("K-line decimal field is empty")
			}
		}
		seen[openTime.UnixMilli()] = struct{}{}
		result = append(result, normalizedCEXKline{
			OpenTime: openTime, Open: values[0], High: values[1],
			Low: values[2], Close: values[3], Volume: values[4],
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].OpenTime.Before(result[j].OpenTime)
	})
	return result, nil
}

func rawKlineValue(value json.RawMessage) (string, error) {
	text := strings.TrimSpace(string(value))
	if text == "" || text == "null" {
		return "", nil
	}
	if strings.HasPrefix(text, `"`) {
		var decoded string
		if err := json.Unmarshal(value, &decoded); err != nil {
			return "", err
		}
		return decoded, nil
	}
	return text, nil
}
