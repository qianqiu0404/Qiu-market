package crawler

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/gorilla/websocket"

	"github.com/the-web3/s78-market-services/database"
)

const (
	spotTickerFlushInterval = 5 * time.Second
	spotStreamReadTimeout   = 65 * time.Second
	spotStreamHeartbeat     = 20 * time.Second
)

func (s *SpotTickerSupervisor) superviseStreamAdapter(
	ctx context.Context,
	adapter spotTickerStreamAdapter,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error("spot stream adapter panic isolated",
				"provider", adapter.Provider(), "panic", recovered,
				"stack", string(debug.Stack()))
			if ctx.Err() == nil {
				go s.superviseStreamAdapter(ctx, adapter)
			}
		}
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		if err := s.runStreamSession(ctx, adapter); err != nil && ctx.Err() == nil {
			sourceKey := s.streamSourceKey(adapter.Provider())
			s.reporter.Failure(adapter.Provider(), sourceKey, time.Now().UTC(), err, 0)
			s.reporter.NextRetry(
				adapter.Provider(), sourceKey, time.Now().UTC().Add(backoff),
			)
			log.Warn("spot ticker stream disconnected",
				"provider", adapter.Provider(), "error", err, "retry_in", backoff)
		} else {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (s *SpotTickerSupervisor) streamSourceKey(provider string) string {
	rollout, err := s.db.MarketAggregation.QueryProviderRollout(provider)
	if err == nil && rollout != nil && rollout.LocalPreviewEnabled {
		return "spot-tickers-preview"
	}
	if err == nil && rollout != nil &&
		(rollout.Mode == "shadow" || rollout.Mode == "paused") {
		return "spot-tickers-shadow"
	}
	return "spot-tickers"
}

func (s *SpotTickerSupervisor) runStreamSession(
	parent context.Context,
	adapter spotTickerStreamAdapter,
) error {
	provider := adapter.Provider()
	rollout, err := s.db.MarketAggregation.QueryProviderRollout(provider)
	if err != nil {
		return err
	}
	if rollout == nil {
		return waitForStreamRetry(parent, 30*time.Second)
	}
	published := rollout.LocalPreviewEnabled ||
		rollout.Mode == "canary" || rollout.Mode == "enabled"
	sourceKey := s.streamSourceKey(provider)
	var markets []database.ProviderMarket
	var candidates []database.ProviderFeedMarket
	var sourceSymbols []string
	if published {
		allowed, _, queryErr := s.db.MarketAggregation.QueryPublishedAssetIDs(provider)
		if queryErr != nil {
			return queryErr
		}
		if len(allowed) == 0 {
			return waitForStreamRetry(parent, 30*time.Second)
		}
		markets, err = s.db.ExchangeSymbol.QueryProviderMarkets(provider)
		if err != nil {
			return err
		}
		markets = filterProviderMarketsByAssetIDs(markets, allowed)
		for _, market := range markets {
			if strings.EqualFold(market.MarketType, "spot") {
				sourceSymbols = append(sourceSymbols, market.SourceSymbol)
			}
		}
	} else {
		candidates, err = s.db.MarketAggregation.QueryEligibleProviderFeedMarkets(
			provider, rollout.RankLimit,
		)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			sourceSymbols = append(sourceSymbols, candidate.SourceSymbol)
		}
	}
	sourceSymbols = uniqueStrings(sourceSymbols)
	sort.Strings(sourceSymbols)
	if len(sourceSymbols) == 0 {
		return waitForStreamRetry(parent, 30*time.Second)
	}

	sessionCtx, cancel := context.WithCancel(parent)
	defer cancel()
	events := make(chan normalizedSpotTicker, 1024)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- adapter.Stream(sessionCtx, sourceSymbols, events)
	}()

	s.reporter.Attempt(provider, sourceKey, time.Now().UTC())
	flushTicker := time.NewTicker(spotTickerFlushInterval)
	defer flushTicker.Stop()
	pending := make(map[string]normalizedSpotTicker, len(sourceSymbols))
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		s.reporter.Attempt(provider, sourceKey, time.Now().UTC())
		var latest *time.Time
		var details database.ProviderStatusDetails
		if published {
			var written int
			latest, written = s.writeProviderTickers(sessionCtx, markets, pending)
			if written == 0 {
				return fmt.Errorf("%s websocket matched no published markets", provider)
			}
			details = tickerEvidenceForProviderMarkets(
				markets, pending, written, time.Now().UTC(),
			)
		} else {
			details, latest = tickerEvidenceForFeedMarkets(
				candidates, pending, time.Now().UTC(),
			)
		}
		s.reporter.SuccessWithDetails(
			provider, sourceKey, time.Now().UTC(), latest, details,
		)
		log.Debug("spot ticker websocket observed",
			"provider", provider, "received", len(pending),
			"written", details.WrittenCount,
			"matched_assets", details.MatchedAssetCount)
		pending = make(map[string]normalizedSpotTicker, len(sourceSymbols))
		current, queryErr := s.db.MarketAggregation.QueryProviderRollout(provider)
		if queryErr != nil {
			return queryErr
		}
		if current == nil || current.Mode != rollout.Mode ||
			current.LocalPreviewEnabled != rollout.LocalPreviewEnabled {
			return errRolloutChanged
		}
		return nil
	}

	for {
		select {
		case event := <-events:
			if event.SourceSymbol != "" {
				pending[event.SourceSymbol] = event
			}
		case <-flushTicker.C:
			if err := flush(); err != nil {
				if err == errRolloutChanged {
					return nil
				}
				return err
			}
		case err := <-streamErr:
			if err == nil && parent.Err() == nil {
				err = fmt.Errorf("%s websocket closed", provider)
			}
			return err
		case <-parent.Done():
			return parent.Err()
		}
	}
}

var errRolloutChanged = fmt.Errorf("provider rollout changed")

func waitForStreamRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type binanceTickerStreamAdapter struct {
	URL    string
	Dialer *websocket.Dialer
}

func (*binanceTickerStreamAdapter) Provider() string { return "binance" }

func (a *binanceTickerStreamAdapter) Stream(
	ctx context.Context,
	symbols []string,
	out chan<- normalizedSpotTicker,
) error {
	url := a.URL
	if url == "" {
		url = "wss://stream.binance.com:443/ws"
	}
	allowed := stringSet(symbols)
	params := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		params = append(params, strings.ToLower(symbol)+"@ticker")
	}
	subscribe := map[string]any{
		"method": "SUBSCRIBE",
		"params": params,
		"id":     1,
	}
	return readSpotStream(
		ctx, a.Dialer, url, []any{subscribe}, websocketControlHeartbeat,
		func(payload []byte) error {
			rows, err := decodeBinanceTickerRows(payload)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if _, ok := allowed[row.Symbol]; !ok {
					continue
				}
				sourceMillis := row.StatisticsCloseAt
				if sourceMillis <= 0 {
					sourceMillis = row.EventTime
				}
				source := time.UnixMilli(int64(sourceMillis)).UTC()
				event := normalizedSpotTicker{
					SourceSymbol: row.Symbol, Last: string(row.LastPrice),
					Bid: string(row.BidPrice), Ask: string(row.AskPrice),
					Open24h: string(row.OpenPrice), Change24hPct: string(row.PriceChangePct),
					QuoteTurnover: string(row.QuoteVolume), SourceTime: &source,
					SourceTimeKind: "ticker_window_close",
				}
				select {
				case out <- event:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
	)
}

type binanceTickerRow struct {
	EventTime         flexibleInt64  `json:"E"`
	Symbol            string         `json:"s"`
	LastPrice         flexibleString `json:"c"`
	OpenPrice         flexibleString `json:"o"`
	StatisticsOpenAt  flexibleInt64  `json:"O"`
	PriceChangePct    flexibleString `json:"P"`
	BidPrice          flexibleString `json:"b"`
	AskPrice          flexibleString `json:"a"`
	QuoteVolume       flexibleString `json:"q"`
	StatisticsCloseAt flexibleInt64  `json:"C"`
}

type flexibleInt64 int64
type flexibleString string

func (value *flexibleString) UnmarshalJSON(payload []byte) error {
	text := strings.TrimSpace(string(payload))
	if text == "" || text == "null" {
		*value = ""
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var decoded string
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return err
		}
		*value = flexibleString(decoded)
		return nil
	}
	*value = flexibleString(text)
	return nil
}

func (value *flexibleInt64) UnmarshalJSON(payload []byte) error {
	text := strings.Trim(string(payload), `"`)
	if text == "" || text == "null" {
		*value = 0
		return nil
	}
	parsed, err := parseInt64(text)
	if err != nil {
		return err
	}
	*value = flexibleInt64(parsed)
	return nil
}

func parseInt64(value string) (int64, error) {
	var parsed int64
	_, err := fmt.Sscan(value, &parsed)
	return parsed, err
}

func decodeBinanceTickerRows(payload []byte) ([]binanceTickerRow, error) {
	var rows []binanceTickerRow
	if err := json.Unmarshal(payload, &rows); err == nil {
		return rows, nil
	}
	var row binanceTickerRow
	if err := json.Unmarshal(payload, &row); err != nil {
		return nil, err
	}
	if row.Symbol == "" {
		return nil, nil
	}
	return []binanceTickerRow{row}, nil
}

func websocketControlHeartbeat(connection *websocket.Conn) error {
	return connection.WriteControl(
		websocket.PingMessage,
		nil,
		time.Now().Add(5*time.Second),
	)
}

func bybitHeartbeat(connection *websocket.Conn) error {
	return connection.WriteJSON(map[string]any{
		"req_id": fmt.Sprintf("s78-%d", time.Now().UnixMilli()),
		"op":     "ping",
	})
}

func okxHeartbeat(connection *websocket.Conn) error {
	return connection.WriteMessage(websocket.TextMessage, []byte("ping"))
}

func chunkStringValues(values []string, size int) [][]string {
	if size <= 0 {
		return nil
	}
	result := make([][]string, 0, (len(values)+size-1)/size)
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		result = append(result, values[start:end])
	}
	return result
}

type bybitTickerStreamAdapter struct {
	URL    string
	Dialer *websocket.Dialer
}

func (*bybitTickerStreamAdapter) Provider() string { return "bybit" }

func (a *bybitTickerStreamAdapter) Stream(
	ctx context.Context,
	symbols []string,
	out chan<- normalizedSpotTicker,
) error {
	url := a.URL
	if url == "" {
		url = "wss://stream.bybit.com/v5/public/spot"
	}
	args := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, "tickers."+symbol)
	}
	subscriptions := make([]any, 0, (len(args)+9)/10)
	for index, batch := range chunkStringValues(args, 10) {
		subscriptions = append(subscriptions, map[string]any{
			"req_id": fmt.Sprintf("s78-subscribe-%d", index+1),
			"op":     "subscribe",
			"args":   batch,
		})
	}
	return readSpotStream(ctx, a.Dialer, url, subscriptions, bybitHeartbeat, func(payload []byte) error {
		var message struct {
			Topic string `json:"topic"`
			Time  int64  `json:"ts"`
			Data  struct {
				Symbol       string `json:"symbol"`
				LastPrice    string `json:"lastPrice"`
				HighPrice24h string `json:"highPrice24h"`
				LowPrice24h  string `json:"lowPrice24h"`
				PrevPrice24h string `json:"prevPrice24h"`
				Volume24h    string `json:"volume24h"`
				Turnover24h  string `json:"turnover24h"`
				Price24hPcnt string `json:"price24hPcnt"`
				BestBidPrice string `json:"bid1Price"`
				BestAskPrice string `json:"ask1Price"`
			} `json:"data"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			return err
		}
		if message.Data.Symbol == "" {
			return nil
		}
		source := time.UnixMilli(message.Time).UTC()
		change := ""
		if strings.TrimSpace(message.Data.Price24hPcnt) != "" {
			change = multiplyDecimalStrings(message.Data.Price24hPcnt, "100")
		}
		event := normalizedSpotTicker{
			SourceSymbol: message.Data.Symbol, Last: message.Data.LastPrice,
			Bid: message.Data.BestBidPrice, Ask: message.Data.BestAskPrice,
			Open24h: message.Data.PrevPrice24h, Change24hPct: change,
			QuoteTurnover: message.Data.Turnover24h, SourceTime: &source,
			SourceTimeKind: "ticker_event",
		}
		select {
		case out <- event:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

type okxTickerStreamAdapter struct {
	URL    string
	Dialer *websocket.Dialer
}

func (*okxTickerStreamAdapter) Provider() string { return "okx" }

func (a *okxTickerStreamAdapter) Stream(
	ctx context.Context,
	symbols []string,
	out chan<- normalizedSpotTicker,
) error {
	url := a.URL
	if url == "" {
		url = "wss://ws.okx.com:8443/ws/v5/public"
	}
	args := make([]map[string]string, 0, len(symbols))
	for _, symbol := range symbols {
		args = append(args, map[string]string{"channel": "tickers", "instId": symbol})
	}
	subscribe := map[string]any{"op": "subscribe", "args": args}
	return readSpotStream(ctx, a.Dialer, url, []any{subscribe}, okxHeartbeat, func(payload []byte) error {
		var message struct {
			Event string `json:"event"`
			Data  []struct {
				InstrumentID string `json:"instId"`
				Last         string `json:"last"`
				Bid          string `json:"bidPx"`
				Ask          string `json:"askPx"`
				Open24h      string `json:"open24h"`
				QuoteVolume  string `json:"volCcy24h"`
				Timestamp    string `json:"ts"`
			} `json:"data"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			return err
		}
		for _, row := range message.Data {
			event := normalizedSpotTicker{
				SourceSymbol: row.InstrumentID, Last: row.Last,
				Bid: row.Bid, Ask: row.Ask, Open24h: row.Open24h,
				QuoteTurnover:  row.QuoteVolume,
				SourceTime:     parseTickerMilliseconds(row.Timestamp),
				SourceTimeKind: "ticker_event",
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
}

func readSpotStream(
	ctx context.Context,
	dialer *websocket.Dialer,
	url string,
	subscriptions []any,
	heartbeat func(*websocket.Conn) error,
	handle func([]byte) error,
) error {
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	connection, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return err
	}
	defer connection.Close()
	for index, subscribe := range subscriptions {
		if err := connection.WriteJSON(subscribe); err != nil {
			return err
		}
		if index+1 < len(subscriptions) {
			select {
			case <-time.After(100 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(spotStreamReadTimeout))
	})
	heartbeatErr := make(chan error, 1)
	if heartbeat != nil {
		go func() {
			ticker := time.NewTicker(spotStreamHeartbeat)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := heartbeat(connection); err != nil {
						select {
						case heartbeatErr <- err:
						default:
						}
						_ = connection.Close()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	for {
		if err := connection.SetReadDeadline(time.Now().Add(spotStreamReadTimeout)); err != nil {
			return err
		}
		_, payload, err := connection.ReadMessage()
		if err != nil {
			select {
			case heartbeatFailure := <-heartbeatErr:
				return heartbeatFailure
			default:
			}
			return err
		}
		if string(payload) == "pong" {
			continue
		}
		if err := handle(payload); err != nil {
			return err
		}
	}
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
