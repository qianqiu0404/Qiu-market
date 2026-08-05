package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/the-web3/s78-market-services/trading/domain"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
)

func (s *Server) serveWebSocket(writer http.ResponseWriter, request *http.Request) {
	if !s.validOrigin(request) {
		writeError(writer, http.StatusForbidden, "origin_denied", "request origin is not allowed")
		return
	}
	principal, ok := s.tickets.Consume(request.URL.Query().Get("ticket"))
	if !ok {
		writeError(writer, http.StatusUnauthorized, "invalid_ticket", "WebSocket ticket is invalid or expired")
		return
	}
	eventIndex, err := strconv.ParseUint(request.URL.Query().Get("event_index"), 10, 32)
	if request.URL.Query().Get("event_index") == "" {
		eventIndex = 0
		err = nil
	}
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_cursor", "event_index must be an unsigned integer")
		return
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		CheckOrigin: func(upgradeRequest *http.Request) bool {
			return s.validOrigin(upgradeRequest)
		},
	}
	connection, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(1024)

	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	stream, err := s.client.SubscribeEvents(ctx, &tradingv1.SubscribeEventsRequest{
		MarketId:         s.config.MarketID,
		CursorSequence:   request.URL.Query().Get("sequence"),
		CursorEventIndex: uint32(eventIndex),
	})
	if err != nil {
		_ = connection.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "event stream unavailable"),
			time.Now().Add(time.Second),
		)
		return
	}

	const pongWait = 60 * time.Second
	_ = connection.SetReadDeadline(time.Now().Add(pongWait))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		for {
			if _, _, readErr := connection.NextReader(); readErr != nil {
				return
			}
		}
	}()

	type received struct {
		event *tradingv1.EventEnvelope
		err   error
	}
	events := make(chan received, 1)
	go func() {
		for {
			event, receiveErr := stream.Recv()
			select {
			case events <- received{event: event, err: receiveErr}:
			case <-ctx.Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()

	pings := time.NewTicker(25 * time.Second)
	defer pings.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-clientDone:
			return
		case <-pings.C:
			if err := connection.WriteControl(
				websocket.PingMessage,
				nil,
				time.Now().Add(5*time.Second),
			); err != nil {
				return
			}
		case value := <-events:
			if value.err != nil {
				if !errors.Is(value.err, io.EOF) {
					_ = connection.WriteControl(
						websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "event stream ended"),
						time.Now().Add(time.Second),
					)
				}
				return
			}
			sanitizeEventEnvelope(value.event, principal.AccountID)
			payload, marshalErr := json.Marshal(value.event)
			if marshalErr != nil {
				return
			}
			if err := connection.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return
			}
			if err := connection.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		}
	}
}

func sanitizeEventEnvelope(envelope *tradingv1.EventEnvelope, ownAccount string) {
	if envelope == nil || envelope.Event == nil {
		return
	}
	event := envelope.Event
	if event.Type == string(domain.EventAccountFunded) && event.AccountId != ownAccount {
		envelope.Event = nil
		return
	}
	if event.AccountId != ownAccount {
		event.AccountId = ""
	}
	hideTradeAccounts(event.Trade, ownAccount)
}
