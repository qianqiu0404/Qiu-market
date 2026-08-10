package fullstack_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	golden "github.com/the-web3/s78-market-services/fullstackgolden"
)

const (
	qaGateEnvironment   = "QIU_FULLSTACK_QA"
	manifestEnvironment = "QIU_FULLSTACK_MANIFEST"
)

func TestIndependentFullStackEvidence(t *testing.T) {
	if os.Getenv(qaGateEnvironment) != "1" {
		t.Skip("set QIU_FULLSTACK_QA=1 only inside the isolated full-stack gate")
	}
	manifestPath := os.Getenv(manifestEnvironment)
	if !filepath.IsAbs(manifestPath) || filepath.Clean(manifestPath) != manifestPath {
		t.Fatalf("manifest path is not absolute and clean")
	}
	metadata, err := os.Lstat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 || metadata.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %v, want regular 0600", metadata.Mode())
	}
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest golden.Manifest
	decodeStrict(t, payload, &manifest)
	if manifest.SchemaVersion != golden.SchemaManifest {
		t.Fatalf("manifest schema = %q", manifest.SchemaVersion)
	}
	for label, raw := range map[string]string{
		"api": manifest.APIOrigin, "ready": manifest.ReadyURL, "control": manifest.ControlURL,
		"state": manifest.StateURL, "evidence": manifest.EvidenceURL,
	} {
		requireLoopbackURL(t, label, raw, manifest.APIOrigin)
	}

	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext},
		Timeout:   2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("redirects are forbidden")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifest.EvidenceURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("evidence response status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	evidencePayload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	var evidence golden.Evidence
	decodeStrict(t, evidencePayload, &evidence)
	verifyEvidence(t, manifest, evidence)
}

func verifyEvidence(t *testing.T, manifest golden.Manifest, evidence golden.Evidence) {
	t.Helper()
	if evidence.SchemaVersion != golden.SchemaEvidence || !evidence.CleanupArmed {
		t.Fatalf("evidence schema=%q cleanup_armed=%v", evidence.SchemaVersion, evidence.CleanupArmed)
	}
	if evidence.CoordinatorPID != manifest.CoordinatorPID || evidence.FixturePID != manifest.FixturePID ||
		evidence.VuePID != manifest.VuePID || evidence.Postgres.PID != manifest.Postgres.PID {
		t.Fatal("manifest and final process evidence differ")
	}
	pids := []int{evidence.Postgres.PID, evidence.CoordinatorPID, evidence.FixturePID, evidence.VuePID, evidence.BackendA.PID, evidence.BackendB.PID}
	seenPIDs := map[int]struct{}{}
	for _, pid := range pids {
		if pid <= 0 {
			t.Fatalf("invalid process id %d", pid)
		}
		if _, duplicate := seenPIDs[pid]; duplicate {
			t.Fatalf("duplicate process id %d in %v", pid, pids)
		}
		seenPIDs[pid] = struct{}{}
	}
	if evidence.Postgres.Authority != "isolated_ephemeral_postgresql" || evidence.Postgres.SnapshotSequence != 4 ||
		evidence.Postgres.HeadSequence != 7 || evidence.Postgres.SnapshotMatchesHead || !evidence.Postgres.SnapshotMatchesRuntime {
		t.Fatalf("invalid PostgreSQL authority evidence: %+v", evidence.Postgres)
	}
	if evidence.BackendA.Generation != golden.GenerationA || !evidence.BackendA.Exited || evidence.BackendA.Sequence != 6 ||
		evidence.BackendB.Generation != golden.GenerationB || evidence.BackendB.Exited || evidence.BackendB.Sequence != 7 {
		t.Fatalf("invalid backend generations: A=%+v B=%+v", evidence.BackendA, evidence.BackendB)
	}
	if !evidence.Restore.SameSequence || !evidence.Restore.SameStateHash ||
		evidence.Restore.Before.PID != evidence.BackendA.PID || evidence.Restore.After.PID != evidence.BackendB.PID ||
		evidence.Restore.Before.Sequence != 6 || evidence.Restore.After.Sequence != 6 ||
		evidence.Restore.Before.StateHash == "" || evidence.Restore.Before.StateHash != evidence.Restore.After.StateHash {
		t.Fatalf("invalid cross-process restore evidence: %+v", evidence.Restore)
	}

	if evidence.Partial == nil || evidence.Final == nil {
		t.Fatal("partial and final PostgreSQL states are required")
	}
	verifyPartial(t, *evidence.Partial)
	verifyFinal(t, *evidence.Final)
	verifyReplay(t, evidence.Replay, *evidence.Final)
	verifyReference(t, evidence.Reference)
	verifyQuality(t, evidence.Quality)
	verifySpies(t, evidence)
}

func verifyQuality(t *testing.T, windows []golden.QualityWindowEvidence) {
	t.Helper()
	wantScenarios := []string{
		"healthy", "healthy", "healthy", // coordinator priming
		"healthy",
		"binance_429", "coinglass_5xx", "timeout", "stale", "future", "conflict",
		"cache_hit", "no_data", "recover", "conflict", "recover", "recover", "recover",
	}
	if len(windows) != len(wantScenarios) {
		t.Fatalf("quality windows = %d, want %d", len(windows), len(wantScenarios))
	}
	for index, window := range windows {
		if window.Scenario != wantScenarios[index] || window.End.IsZero() ||
			(index > 0 && !window.End.After(windows[index-1].End)) {
			t.Fatalf("quality window %d = %+v", index, window)
		}
		if len(window.Sources) != 3 {
			t.Fatalf("quality window %s sources = %d", window.Scenario, len(window.Sources))
		}
		binance := qualitySource(t, window, "binance_spot")
		coinGlass := qualitySource(t, window, "coinglass_derivatives")
		research := qualitySource(t, window, "xiuqiu_research")
		if binance.OriginalLicense != "unknown" || !binance.GoldenLicenseAssumption ||
			coinGlass.OriginalLicense != "restricted" || coinGlass.GoldenLicenseAssumption ||
			research.OriginalLicense != "unknown" || research.GoldenLicenseAssumption {
			t.Fatalf("quality license provenance in %s is invalid: Binance=%+v CoinGlass=%+v research=%+v", window.Scenario, binance, coinGlass, research)
		}
		if coinGlass.Status != "quarantined" || research.Status != "quarantined" {
			t.Fatalf("restricted/unknown sources became usable in %s: CoinGlass=%s research=%s", window.Scenario, coinGlass.Status, research.Status)
		}
	}

	assertQualityFault(t, windows[4], "binance_spot", "rate_limit", 429, 1)
	assertQualityFault(t, windows[5], "coinglass_derivatives", "upstream_5xx", 502, 0)
	assertQualityFault(t, windows[6], "binance_spot", "timeout", 0, 0)
	assertQualityFault(t, windows[7], "binance_spot", "stale", 200, 0)
	assertQualityFault(t, windows[8], "binance_spot", "future", 200, 0)
	assertQualityFault(t, windows[9], "binance_spot", "conflict", 200, 0)

	if qualitySource(t, windows[9], "binance_spot").HealthyWindowStreak != 0 ||
		qualitySource(t, windows[10], "binance_spot").HealthyWindowStreak != 0 ||
		qualitySource(t, windows[11], "binance_spot").HealthyWindowStreak != 0 {
		t.Fatal("hard fault, cache hit, or no-data advanced Binance recovery")
	}
	firstRecovery := qualitySource(t, windows[12], "binance_spot")
	reset := qualitySource(t, windows[13], "binance_spot")
	if firstRecovery.Status != "recovering" || firstRecovery.HealthyWindowStreak != 1 ||
		reset.Status != "quarantined" || reset.HealthyWindowStreak != 0 {
		t.Fatalf("fault did not reset recovery: first=%+v reset=%+v", firstRecovery, reset)
	}
	for offset, wantStatus := range []string{"recovering", "recovering", "healthy"} {
		value := qualitySource(t, windows[14+offset], "binance_spot")
		if string(value.Status) != wantStatus || value.HealthyWindowStreak != uint32(offset+1) {
			t.Fatalf("recovery window %d = %+v", offset+1, value)
		}
	}
}

func qualitySource(t *testing.T, window golden.QualityWindowEvidence, source string) golden.QualitySourceEvidence {
	t.Helper()
	for _, value := range window.Sources {
		if string(value.Source) == source {
			return value
		}
	}
	t.Fatalf("quality window %s lacks source %s", window.Scenario, source)
	return golden.QualitySourceEvidence{}
}

func assertQualityFault(t *testing.T, window golden.QualityWindowEvidence, source, kind string, status int, retryAfter int64) {
	t.Helper()
	wantHTTPOutcome, wantQualityOutcome, wantHardFault := "error", kind, ""
	switch kind {
	case "stale":
		wantHTTPOutcome, wantQualityOutcome, wantHardFault = "success", "stale", "stale"
	case "future":
		wantHTTPOutcome, wantQualityOutcome, wantHardFault = "success", "bad_payload", "future"
	case "conflict":
		wantHTTPOutcome, wantQualityOutcome, wantHardFault = "success", "bad_payload", "conflict"
	}
	for _, fault := range window.Faults {
		if string(fault.Source) != source || string(fault.NormalizedErrorKind) != kind {
			continue
		}
		if fault.HTTPStatus != status || fault.RetryAfterSeconds != retryAfter || fault.Operation == "" ||
			fault.HTTPOutcome != wantHTTPOutcome || string(fault.QualityOutcome) != wantQualityOutcome ||
			(wantHardFault != "" && !containsHardFault(fault, wantHardFault)) {
			t.Fatalf("quality fault %s/%s = %+v", source, kind, fault)
		}
		return
	}
	t.Fatalf("quality window %s lacks typed fault %s/%s: %+v", window.Scenario, source, kind, window.Faults)
}

func containsHardFault(fault golden.QualityFaultEvidence, target string) bool {
	for _, value := range fault.HardFaults {
		if string(value) == target {
			return true
		}
	}
	return false
}

func verifyPartial(t *testing.T, state golden.DatabaseState) {
	t.Helper()
	expectStateHeader(t, state, 6, golden.DatabaseCounts{Facts: 6, Trades: 2, LedgerTransactions: 8, LedgerEntries: 24, Orders: 4})
	expectBalance(t, state.BuyerBalances, "USDT", "1200", "600")
	expectBalance(t, state.BuyerBalances, "BTC", "0.01998", "0")
	expectBalance(t, state.SellerBalances, "USDT", "1197.6", "0")
	expectBalance(t, state.SellerBalances, "BTC", "0.01", "0")
	expectFees(t, state.PlatformFees)
	verifyOrders(t, state.Orders, "partially_filled", "USDT", "600")
	verifyTrades(t, state.Trades)
	verifyLedger(t, state, false)
}

func verifyFinal(t *testing.T, state golden.DatabaseState) {
	t.Helper()
	expectStateHeader(t, state, 7, golden.DatabaseCounts{Facts: 7, Trades: 2, LedgerTransactions: 9, LedgerEntries: 26, Orders: 4})
	expectBalance(t, state.BuyerBalances, "USDT", "1800", "0")
	expectBalance(t, state.BuyerBalances, "BTC", "0.01998", "0")
	expectBalance(t, state.SellerBalances, "USDT", "1197.6", "0")
	expectBalance(t, state.SellerBalances, "BTC", "0.01", "0")
	expectFees(t, state.PlatformFees)
	verifyOrders(t, state.Orders, "canceled", "", "0")
	verifyTrades(t, state.Trades)
	verifyLedger(t, state, true)
	btcTotal := atoms(t, state.BuyerBalances["BTC"].Available, 8) + atoms(t, state.BuyerBalances["BTC"].Held, 8) +
		atoms(t, state.SellerBalances["BTC"].Available, 8) + atoms(t, state.SellerBalances["BTC"].Held, 8) + atoms(t, state.PlatformFees["BTC"], 8)
	usdtTotal := atoms(t, state.BuyerBalances["USDT"].Available, 6) + atoms(t, state.BuyerBalances["USDT"].Held, 6) +
		atoms(t, state.SellerBalances["USDT"].Available, 6) + atoms(t, state.SellerBalances["USDT"].Held, 6) + atoms(t, state.PlatformFees["USDT"], 6)
	if btcTotal != atoms(t, "0.03", 8) || usdtTotal != atoms(t, "3000", 6) {
		t.Fatalf("asset conservation BTC=%d USDT=%d", btcTotal, usdtTotal)
	}
}

func expectStateHeader(t *testing.T, state golden.DatabaseState, sequence uint64, counts golden.DatabaseCounts) {
	t.Helper()
	if state.Sequence != sequence || state.SnapshotSequence != 4 || state.SnapshotSequence >= state.Sequence || state.Counts != counts {
		t.Fatalf("state header sequence=%d snapshot=%d counts=%+v", state.Sequence, state.SnapshotSequence, state.Counts)
	}
	for label, value := range map[string]string{"digest": state.Digest, "event": state.EventHash, "snapshot": state.SnapshotHash, "snapshot_event": state.SnapshotEventHash} {
		if len(value) != sha256.Size*2 {
			t.Fatalf("%s hash has length %d", label, len(value))
		}
		if _, err := hex.DecodeString(value); err != nil {
			t.Fatalf("%s hash is not hex: %v", label, err)
		}
	}
	if state.SnapshotHash != state.SnapshotEventHash || state.EventHash == state.SnapshotHash || state.DuplicateTransactions || state.ReferenceMismatch {
		t.Fatalf("invalid snapshot/journal integrity: %+v", state)
	}
	if state.JournalSums["BTC"] != "0" || state.JournalSums["USDT"] != "0" {
		t.Fatalf("journal sums = %+v", state.JournalSums)
	}
}

func expectBalance(t *testing.T, balances map[string]golden.BalanceEvidence, asset, available, held string) {
	t.Helper()
	if balances[asset] != (golden.BalanceEvidence{Available: available, Held: held}) {
		t.Fatalf("%s balance = %+v", asset, balances[asset])
	}
}

func expectFees(t *testing.T, fees map[string]string) {
	t.Helper()
	if fees["BTC"] != "0.00002" || fees["USDT"] != "2.4" {
		t.Fatalf("platform fees = %+v", fees)
	}
}

func verifyOrders(t *testing.T, orders map[string]golden.OrderEvidence, partialStatus, partialHeldAsset, partialHeld string) {
	t.Helper()
	if len(orders) != 4 {
		t.Fatalf("orders = %d", len(orders))
	}
	expectOrder(t, orders[golden.FullClientOrderID], golden.FullClientOrderID, orderID(3), "filled", "buy", "0.01", "0.01", "0", "", "0")
	expectOrder(t, orders["full-stack-seller-full-v1"], "full-stack-seller-full-v1", orderID(4), "filled", "sell", "0.01", "0.01", "0", "", "0")
	expectOrder(t, orders[golden.PartialClientOrderID], golden.PartialClientOrderID, orderID(5), partialStatus, "buy", "0.02", "0.01", "0.01", partialHeldAsset, partialHeld)
	expectOrder(t, orders["full-stack-seller-partial-v1"], "full-stack-seller-partial-v1", orderID(6), "filled", "sell", "0.01", "0.01", "0", "", "0")
}

func expectOrder(t *testing.T, order golden.OrderEvidence, clientID, id, status, side, original, filled, remaining, heldAsset, held string) {
	t.Helper()
	if order.ClientOrderID != clientID || order.OrderID != id || order.Status != status || order.Side != side ||
		order.Type != "limit" || order.Price != "60000" || order.OriginalQuantity != original ||
		order.FilledQuantity != filled || order.RemainingQuantity != remaining || order.HeldAsset != heldAsset || order.HeldAmount != held {
		t.Fatalf("order %s = %+v", clientID, order)
	}
}

func verifyTrades(t *testing.T, trades []golden.TradeEvidence) {
	t.Helper()
	if len(trades) != 2 {
		t.Fatalf("trades = %d", len(trades))
	}
	for index, trade := range trades {
		sequence := uint64(4 + index*2)
		if trade.TradeID != tradeID(sequence) || trade.Sequence != sequence || trade.Price != "60000" ||
			trade.Quantity != "0.01" || trade.QuoteAmount != "600" || trade.MakerOrderID != orderID(sequence-1) ||
			trade.TakerOrderID != orderID(sequence) || trade.BuyerOrderID != orderID(sequence-1) || trade.SellerOrderID != orderID(sequence) ||
			trade.BuyerFeeAsset != "BTC" || trade.BuyerFeeAmount != "0.00001" || trade.BuyerFeeRateBPS != 10 || trade.BuyerFeeRole != "maker" ||
			trade.SellerFeeAsset != "USDT" || trade.SellerFeeAmount != "1.2" || trade.SellerFeeRateBPS != 20 || trade.SellerFeeRole != "taker" {
			t.Fatalf("trade %d = %+v", index, trade)
		}
	}
}

func verifyLedger(t *testing.T, state golden.DatabaseState, includeCancel bool) {
	t.Helper()
	expected := expectedLedger(includeCancel)
	if len(state.LedgerTransactions) != len(expected) {
		t.Fatalf("ledger transactions = %d, want %d", len(state.LedgerTransactions), len(expected))
	}
	seen := map[string]struct{}{}
	entryCount := 0
	for _, transaction := range state.LedgerTransactions {
		want, ok := expected[transaction.TransactionID]
		if !ok {
			t.Fatalf("unexpected ledger transaction %+v", transaction)
		}
		if _, duplicate := seen[transaction.TransactionID]; duplicate {
			t.Fatalf("duplicate transaction %s", transaction.TransactionID)
		}
		seen[transaction.TransactionID] = struct{}{}
		if transaction.Sequence != want.Sequence || transaction.Reference != want.Reference || !reflect.DeepEqual(transaction.Entries, want.Entries) {
			t.Fatalf("transaction %s = %+v, want %+v", transaction.TransactionID, transaction, want)
		}
		entryCount += len(transaction.Entries)
		sums := map[string]int64{}
		for index, entry := range transaction.Entries {
			if entry.Index != uint32(index+1) {
				t.Fatalf("transaction %s entry index=%d want=%d", transaction.TransactionID, entry.Index, index+1)
			}
			scale := 6
			if entry.Asset == "BTC" {
				scale = 8
			}
			sums[entry.Asset] += atoms(t, entry.Amount, scale)
		}
		for asset, sum := range sums {
			if sum != 0 {
				t.Fatalf("transaction %s asset %s sum=%d", transaction.TransactionID, asset, sum)
			}
		}
	}
	if entryCount != int(state.Counts.LedgerEntries) || len(seen) != int(state.Counts.LedgerTransactions) {
		t.Fatalf("ledger count mismatch transactions=%d entries=%d", len(seen), entryCount)
	}
}

func expectedLedger(includeCancel bool) map[string]golden.LedgerTransactionEvidence {
	buyerAvailable := "user:" + golden.BuyerAccount + ":available"
	buyerHeld := "user:" + golden.BuyerAccount + ":held"
	sellerAvailable := "user:" + golden.SellerAccount + ":available"
	sellerHeld := "user:" + golden.SellerAccount + ":held"
	result := map[string]golden.LedgerTransactionEvidence{}
	add := func(id string, sequence uint64, reference string, entries ...golden.LedgerEntryEvidence) {
		result[id] = golden.LedgerTransactionEvidence{TransactionID: id, Sequence: sequence, Reference: reference, Entries: entries}
	}
	entry := func(index uint32, account, asset, amount string) golden.LedgerEntryEvidence {
		return golden.LedgerEntryEvidence{Index: index, Account: account, Asset: asset, Amount: amount}
	}
	add(transactionID("fund", 1), 1, "virtual-funding:full-stack-fund-buyer-v1", entry(1, "system:treasury:USDT", "USDT", "-3000"), entry(2, buyerAvailable, "USDT", "3000"))
	add(transactionID("fund", 2), 2, "virtual-funding:full-stack-fund-seller-v1", entry(1, "system:treasury:BTC", "BTC", "-0.03"), entry(2, sellerAvailable, "BTC", "0.03"))
	add(transactionID("hold", 3), 3, "order-hold:"+orderID(3), entry(1, buyerAvailable, "USDT", "-600"), entry(2, buyerHeld, "USDT", "600"))
	add(transactionID("hold", 4), 4, "order-hold:"+orderID(4), entry(1, sellerAvailable, "BTC", "-0.01"), entry(2, sellerHeld, "BTC", "0.01"))
	add("trade:"+tradeID(4), 4, "matched-trade:"+tradeID(4), tradeEntries(entry, buyerHeld, sellerAvailable, sellerHeld, buyerAvailable)...)
	add(transactionID("hold", 5), 5, "order-hold:"+orderID(5), entry(1, buyerAvailable, "USDT", "-1200"), entry(2, buyerHeld, "USDT", "1200"))
	add(transactionID("hold", 6), 6, "order-hold:"+orderID(6), entry(1, sellerAvailable, "BTC", "-0.01"), entry(2, sellerHeld, "BTC", "0.01"))
	add("trade:"+tradeID(6), 6, "matched-trade:"+tradeID(6), tradeEntries(entry, buyerHeld, sellerAvailable, sellerHeld, buyerAvailable)...)
	if includeCancel {
		add(transactionID("cancel-release", 7), 7, "order-cancel:"+orderID(5), entry(1, buyerHeld, "USDT", "-600"), entry(2, buyerAvailable, "USDT", "600"))
	}
	return result
}

func tradeEntries(
	entry func(uint32, string, string, string) golden.LedgerEntryEvidence,
	buyerHeld, sellerAvailable, sellerHeld, buyerAvailable string,
) []golden.LedgerEntryEvidence {
	return []golden.LedgerEntryEvidence{
		entry(1, buyerHeld, "USDT", "-600"), entry(2, sellerAvailable, "USDT", "598.8"),
		entry(3, sellerHeld, "BTC", "-0.01"), entry(4, buyerAvailable, "BTC", "0.00999"),
		entry(5, "platform:fee:USDT", "USDT", "1.2"), entry(6, "platform:fee:BTC", "BTC", "0.00001"),
	}
}

func verifyReplay(t *testing.T, replay golden.ReplayEvidence, final golden.DatabaseState) {
	t.Helper()
	if replay.CancelRequestID == "" || replay.CancelRequests != 2 || replay.OriginalSequence != 7 || replay.ReplaySequence != 7 ||
		replay.OriginalStatus != "canceled" || replay.ReplayStatus != "canceled" || replay.BeforeCounts != final.Counts ||
		replay.AfterCounts != final.Counts || replay.BeforeDigest != final.Digest || replay.AfterDigest != final.Digest ||
		replay.BeforeEventHash != final.EventHash || replay.AfterEventHash != final.EventHash || !replay.NoDelta {
		t.Fatalf("invalid cancel replay: %+v", replay)
	}
}

func verifyReference(t *testing.T, reference golden.ReferenceEvidence) {
	t.Helper()
	if !reference.Unchanged || !reflect.DeepEqual(reference.Before, reference.After) || reference.Before.Source == "" ||
		reference.Before.MarketID != golden.MarketID || reference.Before.Price != "60000" {
		t.Fatalf("invalid reference evidence: %+v", reference)
	}
	payload := reference.Before.Source + "|" + reference.Before.MarketID + "|" + reference.Before.Price + "|" + reference.Before.ObservedAt.UTC().Format(time.RFC3339Nano)
	digest := sha256.Sum256([]byte(payload))
	if reference.Before.Hash != hex.EncodeToString(digest[:]) {
		t.Fatalf("reference hash = %q", reference.Before.Hash)
	}
	age := time.Since(reference.Before.ObservedAt)
	if age < -time.Minute || age > 5*time.Minute {
		t.Fatalf("reference observed_at is not fresh: %s age=%s", reference.Before.ObservedAt, age)
	}
}

func verifySpies(t *testing.T, evidence golden.Evidence) {
	t.Helper()
	spy := evidence.Spy
	if spy.AllowedBrowserTradingMutations != 4 || spy.AllowedBootstrapFundWrites != 2 || spy.DeterministicFillWrites != 2 ||
		spy.QualityReads == 0 || spy.LegacyReadRequests == 0 ||
		spy.ReadDomainTradingMutations != 0 || spy.ReadDomainReferenceWrites != 0 || spy.ReadDomainFundWrites != 0 ||
		spy.ForbiddenWrites != 0 || spy.PublicNetworkRequests != 0 || spy.FixtureNonGETRequests != 0 {
		t.Fatalf("invalid mutation/network spy evidence: %+v", spy)
	}
	if evidence.Fixture.SchemaVersion != "qiu.full-stack.fixture-evidence.v1" || evidence.Fixture.NonGETRequests != 0 ||
		evidence.Fixture.ResearchReads == 0 || evidence.Fixture.ProviderReads == 0 || evidence.Fixture.ControlWrites == 0 {
		t.Fatalf("invalid independent fixture evidence: %+v", evidence.Fixture)
	}
}

func atoms(t *testing.T, value string, scale int) int64 {
	t.Helper()
	if value == "" || strings.HasPrefix(value, "+") {
		t.Fatalf("invalid decimal %q", value)
	}
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(value, "-")
	parts := strings.Split(unsigned, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts[0]) > 1 && strings.HasPrefix(parts[0], "0")) {
		t.Fatalf("invalid canonical decimal %q", value)
	}
	for _, part := range parts {
		if part == "" {
			t.Fatalf("invalid canonical decimal %q", value)
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			t.Fatalf("invalid decimal %q", value)
		}
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) > scale || strings.HasSuffix(fraction, "0") {
			t.Fatalf("non-canonical precision %q", value)
		}
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	power := int64(1)
	for range scale {
		power *= 10
	}
	fractionAtoms := int64(0)
	if fraction != "" {
		fractionAtoms, err = strconv.ParseInt(fraction+strings.Repeat("0", scale-len(fraction)), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	result := whole*power + fractionAtoms
	if negative {
		result = -result
	}
	return result
}

func decodeStrict(t *testing.T, payload []byte, destination any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("JSON has trailing content: %v", err)
	}
}

func requireLoopbackURL(t *testing.T, label, raw, apiOrigin string) {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("%s URL is unsafe: %q", label, raw)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || port == "" || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatalf("%s URL is not explicit loopback: %q", label, raw)
	}
	if label == "api" {
		if parsed.Path != "" {
			t.Fatalf("API origin has path %q", parsed.Path)
		}
		return
	}
	wantPaths := map[string]string{"ready": "/__full-stack/ready", "control": "/__full-stack/control", "state": "/__full-stack/state", "evidence": "/__full-stack/evidence"}
	if parsed.Path != wantPaths[label] || parsed.Scheme+"://"+parsed.Host != apiOrigin {
		t.Fatalf("%s URL = %q", label, raw)
	}
}

func orderID(sequence uint64) string { return fmt.Sprintf("O-%020d", sequence) }
func tradeID(sequence uint64) string { return fmt.Sprintf("T-%020d-0001", sequence) }
func transactionID(prefix string, sequence uint64) string {
	return fmt.Sprintf("%s:%020d", prefix, sequence)
}
