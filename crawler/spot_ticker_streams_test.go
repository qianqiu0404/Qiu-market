package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func websocketFixture(
	t *testing.T,
	readSubscription bool,
	payload string,
) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		}).Upgrade(writer, request, nil)
		require.NoError(t, err)
		defer connection.Close()
		if readSubscription {
			_, _, err = connection.ReadMessage()
			require.NoError(t, err)
		}
		require.NoError(t, connection.WriteMessage(websocket.TextMessage, []byte(payload)))
		time.Sleep(25 * time.Millisecond)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func firstStreamEvent(
	t *testing.T,
	adapter spotTickerStreamAdapter,
	symbols []string,
) normalizedSpotTicker {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out := make(chan normalizedSpotTicker, 1)
	go func() {
		_ = adapter.Stream(ctx, symbols, out)
	}()
	select {
	case event := <-out:
		return event
	case <-ctx.Done():
		t.Fatal("stream produced no ticker event")
		return normalizedSpotTicker{}
	}
}

func TestBinanceTickerStreamNormalizesRollingTicker(t *testing.T) {
	url := websocketFixture(t, true, `{
		"E": 1700000000000,
		"s": "BTCUSDT",
		"c": "65000",
		"o": "64000",
		"O": 1699913600100,
		"P": "1.5625",
		"b": "64999",
		"a": "65001",
		"q": "123456",
		"C": 1700000000100
	}`)
	event := firstStreamEvent(t, &binanceTickerStreamAdapter{URL: url}, []string{"BTCUSDT"})
	require.Equal(t, "BTCUSDT", event.SourceSymbol)
	require.Equal(t, "65000", event.Last)
	require.Equal(t, "64000", event.Open24h)
	require.Equal(t, "1.5625", event.Change24hPct)
	require.Equal(t, "123456", event.QuoteTurnover)
	require.Equal(t, "ticker_window_close", event.SourceTimeKind)
	require.Equal(t, time.UnixMilli(1700000000100).UTC(), *event.SourceTime)
}

func TestBinanceTickerDecoderDoesNotOverwriteOpenPriceWithWindowStart(t *testing.T) {
	rows, err := decodeBinanceTickerRows([]byte(`{
		"E":1784905092000,
		"s":"BTCUSDT",
		"c":"64217.09",
		"o":"64014.11",
		"O":1784818692010,
		"P":"0.317",
		"C":1784905092010
	}`))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "64014.11", string(rows[0].OpenPrice))
	require.Equal(t, int64(1784818692010), int64(rows[0].StatisticsOpenAt))
	require.Equal(t, int64(1784905092010), int64(rows[0].StatisticsCloseAt))
}

func TestBinanceTickerDecoderKeepsArrayCompatibility(t *testing.T) {
	rows, err := decodeBinanceTickerRows([]byte(`[{"s":"BTCUSDT","c":"65000"}]`))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "BTCUSDT", rows[0].Symbol)
}

func TestBinanceTickerDecoderAcceptsNumericDecimalFields(t *testing.T) {
	rows, err := decodeBinanceTickerRows([]byte(
		`{"E":"1700000000000","s":"BTCUSDT","c":65000,"o":64000,"P":1.5625}`,
	))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "65000", string(rows[0].LastPrice))
	require.Equal(t, "64000", string(rows[0].OpenPrice))
	require.Equal(t, int64(1700000000000), int64(rows[0].EventTime))
}

func TestChunkStringValuesRespectsBybitSpotLimit(t *testing.T) {
	values := []string{
		"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11",
	}
	chunks := chunkStringValues(values, 10)
	require.Len(t, chunks, 2)
	require.Len(t, chunks[0], 10)
	require.Equal(t, []string{"11"}, chunks[1])
}

func TestBybitTickerStreamConvertsFractionalPercent(t *testing.T) {
	url := websocketFixture(t, true, `{
		"topic": "tickers.BTCUSDT",
		"ts": 1700000000000,
		"type": "snapshot",
		"data": {
			"symbol": "BTCUSDT",
			"lastPrice": "65000",
			"prevPrice24h": "64000",
			"turnover24h": "123456",
			"price24hPcnt": "0.015625",
			"bid1Price": "64999",
			"ask1Price": "65001"
		}
	}`)
	event := firstStreamEvent(t, &bybitTickerStreamAdapter{URL: url}, []string{"BTCUSDT"})
	require.Equal(t, "BTCUSDT", event.SourceSymbol)
	require.Equal(t, "1.5625", event.Change24hPct)
	require.Equal(t, "64000", event.Open24h)
	require.Equal(t, "ticker_event", event.SourceTimeKind)
}

func TestOKXTickerStreamKeepsProviderOpen(t *testing.T) {
	url := websocketFixture(t, true, `{
		"arg": {"channel": "tickers", "instId": "BTC-USDT"},
		"data": [{
			"instId": "BTC-USDT",
			"last": "65000",
			"bidPx": "64999",
			"askPx": "65001",
			"open24h": "64000",
			"volCcy24h": "123456",
			"ts": "1700000000000"
		}]
	}`)
	event := firstStreamEvent(t, &okxTickerStreamAdapter{URL: url}, []string{"BTC-USDT"})
	require.Equal(t, "BTC-USDT", event.SourceSymbol)
	require.Equal(t, "65000", event.Last)
	require.Equal(t, "64000", event.Open24h)
	require.Equal(t, "123456", event.QuoteTurnover)
	require.Equal(t, time.UnixMilli(1700000000000).UTC(), *event.SourceTime)
}
