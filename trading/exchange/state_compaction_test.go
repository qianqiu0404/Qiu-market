package exchange

import (
	"context"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/store"
)

type legacyUpgradeStore struct {
	snapshot store.Snapshot
	saved    store.Snapshot
	records  []store.Record
}

func (s *legacyUpgradeStore) Append(context.Context, uint64, store.Record) error {
	return nil
}

func (s *legacyUpgradeStore) RecordsAfter(context.Context, uint64) ([]store.Record, error) {
	return append([]store.Record(nil), s.records...), nil
}

func TestRestoreVerifiesLegacyReplayAtUpgradeBoundary(t *testing.T) {
	t.Parallel()
	market := domain.DefaultBTCUSDTMarket()
	initial, err := newState(market)
	if err != nil {
		t.Fatal(err)
	}
	initialPayload, err := initial.marshal()
	if err != nil {
		t.Fatal(err)
	}
	initialHash, err := initial.hash()
	if err != nil {
		t.Fatal(err)
	}

	request := domain.FundRequest{
		RequestID: "legacy-fund",
		AccountID: "github:qianqiu0404",
		Asset:     market.QuoteAsset,
		Amount:    10_000_000,
	}
	fingerprint, err := domain.Fingerprint(request)
	if err != nil {
		t.Fatal(err)
	}
	command := domain.Command{
		Sequence:    1,
		RequestID:   request.RequestID,
		RequestKey:  domain.NewIdempotencyKey(market.ID, request.AccountID, domain.CommandKindFund, request.RequestID),
		Fingerprint: fingerprint,
		Kind:        domain.CommandKindFund,
		Fund:        &request,
	}
	legacyAfter, err := initial.clone()
	if err != nil {
		t.Fatal(err)
	}
	legacyAfter.sequence = command.Sequence
	journalStart := legacyAfter.ledger.JournalLen()
	result, err := legacyAfter.apply(command)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := legacyAfter.ledger.JournalFrom(journalStart)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := legacyAfter.buildProjection(command, result)
	if err != nil {
		t.Fatal(err)
	}
	legacyAfter.requests[command.RequestKey] = requestRecord{
		Fingerprint: fingerprint,
		Result:      cloneResult(result),
	}
	afterHash, err := legacyAfter.hash()
	if err != nil {
		t.Fatal(err)
	}
	persistence := &legacyUpgradeStore{
		snapshot: store.Snapshot{
			SchemaVersion: store.PreviousSchemaVersion,
			MarketID:      market.ID,
			StateHash:     initialHash,
			Payload:       initialPayload,
		},
		records: []store.Record{{
			SchemaVersion: store.PreviousSchemaVersion,
			MarketID:      market.ID,
			Command:       command,
			Result:        result,
			Journal:       journal,
			Projection:    projection,
			StateHash:     afterHash,
		}},
	}

	restored, err := Restore(context.Background(), market, persistence, persistence)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Sequence() != 1 || persistence.saved.SchemaVersion != store.CurrentSchemaVersion {
		t.Fatalf(
			"restored sequence=%d saved schema=%d",
			restored.Sequence(),
			persistence.saved.SchemaVersion,
		)
	}
}

func (s *legacyUpgradeStore) Save(_ context.Context, snapshot store.Snapshot) error {
	s.saved = snapshot
	return nil
}

func (s *legacyUpgradeStore) Load(context.Context) (store.Snapshot, bool, error) {
	return s.snapshot, true, nil
}

func TestRestoreUpgradesLegacySnapshotWithoutDeletingAuditableUserState(t *testing.T) {
	t.Parallel()
	market := domain.DefaultBTCUSDTMarket()
	legacy, err := newState(market)
	if err != nil {
		t.Fatal(err)
	}
	legacy.orders["maker-closed"] = domain.Order{
		ID:        "maker-closed",
		AccountID: ephemeralDemoMakerAccount,
		Status:    domain.OrderStatusCanceled,
	}
	legacy.orders["user-closed"] = domain.Order{
		ID:        "user-closed",
		AccountID: "github:qianqiu0404",
		Status:    domain.OrderStatusCanceled,
	}
	makerKey := domain.NewIdempotencyKey(
		market.ID,
		ephemeralDemoMakerAccount,
		domain.CommandKindSubmitOrder,
		"maker-request",
	)
	userKey := domain.NewIdempotencyKey(
		market.ID,
		"github:qianqiu0404",
		domain.CommandKindSubmitOrder,
		"user-request",
	)
	legacy.requests[makerKey] = requestRecord{Fingerprint: "maker"}
	makerFundKey := domain.NewIdempotencyKey(
		market.ID,
		ephemeralDemoMakerAccount,
		domain.CommandKindFund,
		"maker-fund",
	)
	legacy.requests[makerFundKey] = requestRecord{Fingerprint: "maker-fund"}
	legacy.requests[userKey] = requestRecord{Fingerprint: "user"}
	payload, err := legacy.marshal()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := legacy.hash()
	if err != nil {
		t.Fatal(err)
	}
	persistence := &legacyUpgradeStore{snapshot: store.Snapshot{
		SchemaVersion: store.LegacySchemaVersion,
		MarketID:      market.ID,
		StateHash:     hash,
		Payload:       payload,
	}}

	restored, err := Restore(context.Background(), market, persistence, persistence)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := restored.state.orders["maker-closed"]; exists {
		t.Fatal("closed demo-maker order survived snapshot compaction")
	}
	if _, exists := restored.state.orders["user-closed"]; !exists {
		t.Fatal("user order was removed by system-history compaction")
	}
	if _, exists := restored.state.requests[makerKey]; exists {
		t.Fatal("demo-maker request survived snapshot compaction")
	}
	if _, exists := restored.state.requests[makerFundKey]; !exists {
		t.Fatal("demo-maker bootstrap fund idempotency was removed")
	}
	if _, exists := restored.state.requests[userKey]; !exists {
		t.Fatal("user idempotency record was removed")
	}
	if persistence.saved.SchemaVersion != store.CurrentSchemaVersion ||
		len(persistence.saved.Payload) >= len(payload) {
		t.Fatalf(
			"upgraded snapshot = version %d bytes %d; legacy bytes %d",
			persistence.saved.SchemaVersion,
			len(persistence.saved.Payload),
			len(payload),
		)
	}
}
