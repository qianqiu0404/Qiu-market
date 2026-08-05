package server

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestQueryCursorBindsAccountMarketFilterAndRejectsTampering(t *testing.T) {
	now := time.Date(2026, 8, 5, 1, 2, 3, 0, time.UTC)
	codec := mustCursorCodec(t, CursorConfig{
		Current: cursorTestKey("current", 0x11),
		Now:     func() time.Time { return now },
	})
	token, err := codec.Encode(
		orderCursorKind,
		"BTC-USDT",
		"github:qianqiu0404",
		"scope=all",
		orderCursorSort,
		[]string{"123", "order-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) > cursorMaximumBytes || strings.Contains(token, "github:qianqiu0404") {
		t.Fatalf("cursor leaked account or exceeded bound: %q", token)
	}
	position, err := codec.Decode(
		token,
		orderCursorKind,
		"BTC-USDT",
		"github:qianqiu0404",
		"scope=all",
		orderCursorSort,
		2,
	)
	if err != nil || len(position) != 2 || position[0] != "123" || position[1] != "order-1" {
		t.Fatalf("decode position=%v err=%v", position, err)
	}

	for name, mismatch := range map[string]struct {
		account string
		market  string
		filter  string
	}{
		"account": {"github:other", "BTC-USDT", "scope=all"},
		"market":  {"github:qianqiu0404", "ETH-USDT", "scope=all"},
		"filter":  {"github:qianqiu0404", "BTC-USDT", "scope=open"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := codec.Decode(
				token, orderCursorKind, mismatch.market, mismatch.account,
				mismatch.filter, orderCursorSort, 2,
			); err == nil {
				t.Fatal("mismatched cursor was accepted")
			}
		})
	}

	tampered := []byte(token)
	if tampered[3] == 'a' {
		tampered[3] = 'b'
	} else {
		tampered[3] = 'a'
	}
	if _, err := codec.Decode(
		string(tampered), orderCursorKind, "BTC-USDT", "github:qianqiu0404",
		"scope=all", orderCursorSort, 2,
	); err == nil {
		t.Fatal("tampered cursor was accepted")
	}
}

func TestQueryCursorExpiryAndRotation(t *testing.T) {
	issuedAt := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	oldKey := cursorTestKey("old", 0x22)
	oldCodec := mustCursorCodec(t, CursorConfig{
		Current: oldKey, Now: func() time.Time { return issuedAt },
	})
	oldToken, err := oldCodec.Encode(
		tradeCursorKind,
		"BTC-USDT",
		"alice",
		"side=all",
		tradeCursorSort,
		[]string{"9", "2", "trade-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	rotated := mustCursorCodec(t, CursorConfig{
		Current:  cursorTestKey("new", 0x33),
		Previous: &oldKey,
		Now:      func() time.Time { return issuedAt.Add(time.Hour) },
	})
	if _, err := rotated.Decode(
		oldToken, tradeCursorKind, "BTC-USDT", "alice", "side=all", tradeCursorSort, 3,
	); err != nil {
		t.Fatalf("previous key cursor rejected during rotation: %v", err)
	}
	newToken, err := rotated.Encode(
		tradeCursorKind,
		"BTC-USDT",
		"alice",
		"side=all",
		tradeCursorSort,
		[]string{"8", "1", "trade-2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldCodec.Decode(
		newToken, tradeCursorKind, "BTC-USDT", "alice", "side=all", tradeCursorSort, 3,
	); err == nil {
		t.Fatal("old-only verifier accepted a cursor signed by the new key")
	}

	expired := mustCursorCodec(t, CursorConfig{
		Current: oldKey,
		Now:     func() time.Time { return issuedAt.Add(cursorLifetime) },
	})
	if _, err := expired.Decode(
		oldToken, tradeCursorKind, "BTC-USDT", "alice", "side=all", tradeCursorSort, 3,
	); err == nil {
		t.Fatal("cursor remained valid at its exact expiry")
	}
}

func TestQueryCursorConfigurationFailsClosed(t *testing.T) {
	for name, config := range map[string]CursorConfig{
		"missing": {},
		"short-secret": {
			Current: CursorKeyConfig{KeyID: "short", Secret: []byte("short")},
		},
		"same-key-id": {
			Current: CursorKeyConfig{KeyID: "same", Secret: bytes.Repeat([]byte{1}, 32)},
			Previous: &CursorKeyConfig{
				KeyID: "same", Secret: bytes.Repeat([]byte{2}, 32),
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newQueryCursorCodec(config); err == nil {
				t.Fatal("invalid cursor configuration was accepted")
			}
		})
	}
}

func cursorTestKey(keyID string, fill byte) CursorKeyConfig {
	return CursorKeyConfig{KeyID: keyID, Secret: bytes.Repeat([]byte{fill}, 32)}
}

func mustCursorCodec(t *testing.T, config CursorConfig) *queryCursorCodec {
	t.Helper()
	codec, err := newQueryCursorCodec(config)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}
