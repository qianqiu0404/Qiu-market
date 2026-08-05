package tradingv1

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestAccountTradeHasNoCounterpartyIdentity(t *testing.T) {
	t.Parallel()

	tradeDescriptor := (&AccountTrade{}).ProtoReflect().Descriptor()
	want := []protoreflect.Name{
		"id",
		"market_id",
		"order_id",
		"side",
		"liquidity_role",
		"price",
		"quantity",
		"quote_amount",
		"fee_asset",
		"fee_amount",
		"fee_rate_bps",
		"sequence",
		"event_index",
		"occurred_at",
	}
	fields := tradeDescriptor.Fields()
	if fields.Len() != len(want) {
		t.Fatalf("account-scoped trade fields = %d, want %d", fields.Len(), len(want))
	}
	for index, name := range want {
		if got := fields.Get(index).Name(); got != name {
			t.Fatalf("account-scoped trade field %d = %s, want %s", index, got, name)
		}
	}

	responseFields := (&ListAccountTradesResponse{}).ProtoReflect().Descriptor().Fields()
	wantResponse := []protoreflect.Name{"trades", "next_cursor"}
	if responseFields.Len() != len(wantResponse) {
		t.Fatalf("account trade response fields = %d, want %d", responseFields.Len(), len(wantResponse))
	}
	for index, name := range wantResponse {
		if got := responseFields.Get(index).Name(); got != name {
			t.Fatalf("account trade response field %d = %s, want %s", index, got, name)
		}
	}
	trades := responseFields.ByName("trades")
	if trades == nil || trades.Message() == nil ||
		trades.Message().FullName() != tradeDescriptor.FullName() {
		t.Fatalf("ListAccountTradesResponse.trades does not use AccountTrade")
	}
}
