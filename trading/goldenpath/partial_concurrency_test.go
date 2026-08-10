package goldenpath

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/ledger"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

func TestPartialCancelVersusFOKMatchLinearizes100Rounds(t *testing.T) {
	const rounds = 100
	outcomes := map[domain.OrderStatus]int{}
	for round := 0; round < rounds; round++ {
		market := domain.DefaultBTCUSDTMarket()
		memory := store.NewMemory()
		runner, err := tradingruntime.NewMarketRunner(context.Background(), market, memory, memory, tradingruntime.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		fundForRace(t, runner, fmt.Sprintf("race-buyer-fund-%d", round), PartialBuyerAccount, market.QuoteAsset, 2_000_000_000)
		fundForRace(t, runner, fmt.Sprintf("race-seller-fund-%d", round), PartialSellerAccount, market.BaseAsset, 2_000_000)
		buyer, err := runner.Submit(context.Background(), domain.NewOrder{ClientOrderID: fmt.Sprintf("race-buyer-%d", round), AccountID: PartialBuyerAccount, Side: domain.SideBuy, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC, PostOnly: true, Price: 60_000_000_000, Quantity: 2_000_000})
		if err != nil || buyer.Status != domain.OrderStatusOpen {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d buyer=%+v err=%v", round, buyer, err)
		}
		partial, err := runner.Submit(context.Background(), domain.NewOrder{ClientOrderID: fmt.Sprintf("race-partial-%d", round), AccountID: PartialSellerAccount, Side: domain.SideSell, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC, Price: 60_000_000_000, Quantity: 1_000_000})
		if err != nil || partial.Status != domain.OrderStatusFilled {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d partial=%+v err=%v", round, partial, err)
		}
		beforeRestart := runner.Status()
		closeRaceRunner(t, runner)
		snapshot, snapshotFound, err := memory.Load(context.Background())
		if err != nil || !snapshotFound || snapshot.Sequence != beforeRestart.Sequence || snapshot.StateHash != beforeRestart.StateHash {
			t.Fatalf("round %d snapshot found=%v snapshot=%+v err=%v", round, snapshotFound, snapshot, err)
		}
		runner, err = tradingruntime.NewMarketRunner(context.Background(), market, memory, memory, tradingruntime.DefaultConfig())
		if err != nil {
			t.Fatalf("round %d restore: %v", round, err)
		}
		afterRestart := runner.Status()
		if afterRestart.Sequence != beforeRestart.Sequence || afterRestart.StateHash != beforeRestart.StateHash {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d restart changed status before=%+v after=%+v", round, beforeRestart, afterRestart)
		}
		type result struct {
			value domain.Result
			err   error
		}
		barrier := make(chan struct{})
		cancelDone := make(chan result, 1)
		fillDone := make(chan result, 1)
		cancelRequest := domain.CancelOrder{RequestID: fmt.Sprintf("race-cancel-%d", round), AccountID: PartialBuyerAccount, OrderID: buyer.OrderID}
		go func() {
			<-barrier
			value, commandErr := runner.Cancel(context.Background(), cancelRequest)
			cancelDone <- result{value, commandErr}
		}()
		go func() {
			<-barrier
			value, commandErr := runner.Submit(context.Background(), domain.NewOrder{ClientOrderID: fmt.Sprintf("race-fok-%d", round), AccountID: PartialSellerAccount, Side: domain.SideSell, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceFOK, Price: 60_000_000_000, Quantity: 1_000_000})
			fillDone <- result{value, commandErr}
		}()
		close(barrier)
		cancelResult := <-cancelDone
		fillResult := <-fillDone
		if cancelResult.err != nil || fillResult.err != nil {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d command errors cancel=%v fill=%v", round, cancelResult.err, fillResult.err)
		}
		final, found, err := runner.Order(buyer.OrderID)
		if err != nil || !found {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d buyer lookup found=%v err=%v", round, found, err)
		}
		if final.Status != domain.OrderStatusCanceled && final.Status != domain.OrderStatusFilled {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d final=%+v", round, final)
		}
		if final.HeldAsset != "" || final.HeldAmount != 0 {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d retained hold=%+v", round, final)
		}
		outcomes[final.Status]++
		trades, err := runner.Trades(PartialBuyerAccount)
		if err != nil {
			closeRaceRunner(t, runner)
			t.Fatal(err)
		}
		records, err := memory.RecordsAfter(context.Background(), 0)
		if err != nil {
			closeRaceRunner(t, runner)
			t.Fatal(err)
		}
		if len(records) != 6 {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d facts=%d want6", round, len(records))
		}
		for index, record := range records {
			if record.Command.Sequence != uint64(index+1) || record.Result.Sequence != uint64(index+1) {
				closeRaceRunner(t, runner)
				t.Fatalf("round %d non-contiguous record %d=%+v", round, index, record)
			}
		}
		transactions, entries, sums, fees, duplicate, unbalancedTransaction, references := auditRaceJournal(records)
		if duplicate || unbalancedTransaction || sums[market.BaseAsset] != 0 || sums[market.QuoteAsset] != 0 {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d journal duplicate=%v unbalanced_tx=%v sums=%v", round, duplicate, unbalancedTransaction, sums)
		}
		if _, ok := references["order-hold:"+string(buyer.OrderID)]; !ok {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d missing buyer hold reference", round)
		}
		for _, trade := range trades {
			if _, ok := references["matched-trade:"+string(trade.ID)]; !ok {
				closeRaceRunner(t, runner)
				t.Fatalf("round %d missing trade reference for %s", round, trade.ID)
			}
		}
		buyerBalances, err := runner.Balances(PartialBuyerAccount)
		if err != nil {
			closeRaceRunner(t, runner)
			t.Fatal(err)
		}
		sellerBalances, err := runner.Balances(PartialSellerAccount)
		if err != nil {
			closeRaceRunner(t, runner)
			t.Fatal(err)
		}
		assertNoNegativeRaceBalance(t, round, buyerBalances)
		assertNoNegativeRaceBalance(t, round, sellerBalances)
		switch final.Status {
		case domain.OrderStatusCanceled:
			if len(trades) != 1 || transactions != 6 || entries != 16 || fees[market.BaseAsset] != 1_000 || fees[market.QuoteAsset] != 1_200_000 || cancelResult.value.Status != domain.OrderStatusCanceled || fillResult.value.Status != domain.OrderStatusRejected {
				closeRaceRunner(t, runner)
				t.Fatalf("round %d canceled matrix trades=%d tx=%d entries=%d fees=%v cancel=%s fill=%s", round, len(trades), transactions, entries, fees, cancelResult.value.Status, fillResult.value.Status)
			}
			if _, ok := references["order-cancel:"+string(buyer.OrderID)]; !ok {
				closeRaceRunner(t, runner)
				t.Fatalf("round %d missing cancel release reference", round)
			}
			assertRaceBalance(t, round, buyerBalances, market.QuoteAsset, 1_400_000_000, 0)
			assertRaceBalance(t, round, buyerBalances, market.BaseAsset, 999_000, 0)
			assertRaceBalance(t, round, sellerBalances, market.BaseAsset, 1_000_000, 0)
			assertRaceBalance(t, round, sellerBalances, market.QuoteAsset, 598_800_000, 0)
		case domain.OrderStatusFilled:
			if len(trades) != 2 || transactions != 7 || entries != 22 || fees[market.BaseAsset] != 2_000 || fees[market.QuoteAsset] != 2_400_000 || fillResult.value.Status != domain.OrderStatusFilled || cancelResult.value.Status != domain.OrderStatusRejected {
				closeRaceRunner(t, runner)
				t.Fatalf("round %d filled matrix trades=%d tx=%d entries=%d fees=%v cancel=%s fill=%s", round, len(trades), transactions, entries, fees, cancelResult.value.Status, fillResult.value.Status)
			}
			assertRaceBalance(t, round, buyerBalances, market.QuoteAsset, 800_000_000, 0)
			assertRaceBalance(t, round, buyerBalances, market.BaseAsset, 1_998_000, 0)
			assertRaceBalance(t, round, sellerBalances, market.BaseAsset, 0, 0)
			assertRaceBalance(t, round, sellerBalances, market.QuoteAsset, 1_197_600_000, 0)
		}
		statusBeforeReplay := runner.Status()
		recordsBeforeReplay := records
		tradesBeforeReplay := trades
		buyerBeforeReplay := buyerBalances
		sellerBeforeReplay := sellerBalances
		replayedCancel, replayErr := runner.Cancel(context.Background(), cancelRequest)
		if replayErr != nil || !reflect.DeepEqual(replayedCancel, cancelResult.value) {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d cancel replay=%+v err=%v want=%+v", round, replayedCancel, replayErr, cancelResult.value)
		}
		recordsAfterReplay, _ := memory.RecordsAfter(context.Background(), 0)
		tradesAfterReplay, _ := runner.Trades(PartialBuyerAccount)
		buyerAfterReplay, _ := runner.Balances(PartialBuyerAccount)
		sellerAfterReplay, _ := runner.Balances(PartialSellerAccount)
		statusAfterReplay := runner.Status()
		if !reflect.DeepEqual(recordsBeforeReplay, recordsAfterReplay) || !reflect.DeepEqual(tradesBeforeReplay, tradesAfterReplay) ||
			!reflect.DeepEqual(buyerBeforeReplay, buyerAfterReplay) || !reflect.DeepEqual(sellerBeforeReplay, sellerAfterReplay) ||
			statusBeforeReplay.Sequence != statusAfterReplay.Sequence || statusBeforeReplay.StateHash != statusAfterReplay.StateHash {
			closeRaceRunner(t, runner)
			t.Fatalf("round %d idempotent cancel replay changed durable/runtime state", round)
		}
		closeRaceRunner(t, runner)
	}
	t.Logf("100 valid serializations: canceled=%d filled=%d", outcomes[domain.OrderStatusCanceled], outcomes[domain.OrderStatusFilled])
}

func TestPartialCancelAndFOKDirectedSerializations(t *testing.T) {
	for _, test := range []struct {
		name        string
		cancelFirst bool
		wantStatus  domain.OrderStatus
		wantTrades  int
		wantTx      int
		wantEntries int
	}{
		{name: "cancel_then_fok", cancelFirst: true, wantStatus: domain.OrderStatusCanceled, wantTrades: 1, wantTx: 6, wantEntries: 16},
		{name: "fok_then_cancel", cancelFirst: false, wantStatus: domain.OrderStatusFilled, wantTrades: 2, wantTx: 7, wantEntries: 22},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner, memory, market, buyer := newRestoredPartialRace(t, test.name)
			defer closeRaceRunner(t, runner)
			cancelRequest := domain.CancelOrder{RequestID: "directed-cancel-" + test.name, AccountID: PartialBuyerAccount, OrderID: buyer.OrderID}
			fillRequest := domain.NewOrder{ClientOrderID: "directed-fok-" + test.name, AccountID: PartialSellerAccount, Side: domain.SideSell, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceFOK, Price: 60_000_000_000, Quantity: 1_000_000}
			var cancelResult, fillResult domain.Result
			var err error
			if test.cancelFirst {
				cancelResult, err = runner.Cancel(context.Background(), cancelRequest)
				if err == nil {
					fillResult, err = runner.Submit(context.Background(), fillRequest)
				}
			} else {
				fillResult, err = runner.Submit(context.Background(), fillRequest)
				if err == nil {
					cancelResult, err = runner.Cancel(context.Background(), cancelRequest)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			final, found, err := runner.Order(buyer.OrderID)
			if err != nil || !found || final.Status != test.wantStatus || final.HeldAsset != "" || final.HeldAmount != 0 {
				t.Fatalf("final=%+v found=%v err=%v", final, found, err)
			}
			trades, _ := runner.Trades(PartialBuyerAccount)
			records, _ := memory.RecordsAfter(context.Background(), 0)
			tx, entries, sums, fees, duplicate, unbalanced, refs := auditRaceJournal(records)
			if len(records) != 6 || len(trades) != test.wantTrades || tx != test.wantTx || entries != test.wantEntries || duplicate || unbalanced || sums[market.BaseAsset] != 0 || sums[market.QuoteAsset] != 0 {
				t.Fatalf("matrix records=%d trades=%d tx=%d entries=%d duplicate=%v unbalanced=%v sums=%v", len(records), len(trades), tx, entries, duplicate, unbalanced, sums)
			}
			for _, trade := range trades {
				if _, ok := refs["matched-trade:"+string(trade.ID)]; !ok {
					t.Fatalf("missing trade ref %s", trade.ID)
				}
			}
			if _, ok := refs["order-hold:"+string(buyer.OrderID)]; !ok {
				t.Fatalf("missing buyer hold reference")
			}
			buyerBalances, err := runner.Balances(PartialBuyerAccount)
			if err != nil {
				t.Fatal(err)
			}
			sellerBalances, err := runner.Balances(PartialSellerAccount)
			if err != nil {
				t.Fatal(err)
			}
			if test.cancelFirst {
				if cancelResult.Status != domain.OrderStatusCanceled || fillResult.Status != domain.OrderStatusRejected || fees[market.BaseAsset] != 1_000 || fees[market.QuoteAsset] != 1_200_000 {
					t.Fatalf("cancel-first cancel=%s fill=%s fees=%v", cancelResult.Status, fillResult.Status, fees)
				}
				if _, ok := refs["order-cancel:"+string(buyer.OrderID)]; !ok {
					t.Fatalf("missing cancel release reference")
				}
				assertRaceBalance(t, 0, buyerBalances, market.QuoteAsset, 1_400_000_000, 0)
				assertRaceBalance(t, 0, buyerBalances, market.BaseAsset, 999_000, 0)
				assertRaceBalance(t, 0, sellerBalances, market.BaseAsset, 1_000_000, 0)
				assertRaceBalance(t, 0, sellerBalances, market.QuoteAsset, 598_800_000, 0)
			} else if fillResult.Status != domain.OrderStatusFilled || cancelResult.Status != domain.OrderStatusRejected || fees[market.BaseAsset] != 2_000 || fees[market.QuoteAsset] != 2_400_000 {
				t.Fatalf("fill-first cancel=%s fill=%s fees=%v", cancelResult.Status, fillResult.Status, fees)
			} else {
				assertRaceBalance(t, 0, buyerBalances, market.QuoteAsset, 800_000_000, 0)
				assertRaceBalance(t, 0, buyerBalances, market.BaseAsset, 1_998_000, 0)
				assertRaceBalance(t, 0, sellerBalances, market.BaseAsset, 0, 0)
				assertRaceBalance(t, 0, sellerBalances, market.QuoteAsset, 1_197_600_000, 0)
			}

			statusBeforeReplay := runner.Status()
			replayedCancel, replayErr := runner.Cancel(context.Background(), cancelRequest)
			if replayErr != nil || !reflect.DeepEqual(replayedCancel, cancelResult) {
				t.Fatalf("directed cancel replay=%+v err=%v want=%+v", replayedCancel, replayErr, cancelResult)
			}
			recordsAfterReplay, err := memory.RecordsAfter(context.Background(), 0)
			if err != nil {
				t.Fatal(err)
			}
			tradesAfterReplay, _ := runner.Trades(PartialBuyerAccount)
			buyerAfterReplay, _ := runner.Balances(PartialBuyerAccount)
			sellerAfterReplay, _ := runner.Balances(PartialSellerAccount)
			statusAfterReplay := runner.Status()
			if !reflect.DeepEqual(records, recordsAfterReplay) || !reflect.DeepEqual(trades, tradesAfterReplay) ||
				!reflect.DeepEqual(buyerBalances, buyerAfterReplay) || !reflect.DeepEqual(sellerBalances, sellerAfterReplay) ||
				statusBeforeReplay.Sequence != statusAfterReplay.Sequence || statusBeforeReplay.StateHash != statusAfterReplay.StateHash {
				t.Fatalf("directed cancel replay changed records/trades/balances/sequence/hash")
			}
		})
	}
}

func newRestoredPartialRace(t *testing.T, suffix string) (*tradingruntime.MarketRunner, *store.Memory, domain.Market, domain.Result) {
	t.Helper()
	market := domain.DefaultBTCUSDTMarket()
	memory := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(context.Background(), market, memory, memory, tradingruntime.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	fundForRace(t, runner, "directed-buyer-fund-"+suffix, PartialBuyerAccount, market.QuoteAsset, 2_000_000_000)
	fundForRace(t, runner, "directed-seller-fund-"+suffix, PartialSellerAccount, market.BaseAsset, 2_000_000)
	buyer, err := runner.Submit(context.Background(), domain.NewOrder{ClientOrderID: "directed-buyer-" + suffix, AccountID: PartialBuyerAccount, Side: domain.SideBuy, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC, PostOnly: true, Price: 60_000_000_000, Quantity: 2_000_000})
	if err != nil {
		closeRaceRunner(t, runner)
		t.Fatal(err)
	}
	partial, err := runner.Submit(context.Background(), domain.NewOrder{ClientOrderID: "directed-partial-" + suffix, AccountID: PartialSellerAccount, Side: domain.SideSell, Type: domain.OrderTypeLimit, TimeInForce: domain.TimeInForceGTC, Price: 60_000_000_000, Quantity: 1_000_000})
	if err != nil || partial.Status != domain.OrderStatusFilled {
		closeRaceRunner(t, runner)
		t.Fatalf("partial=%+v err=%v", partial, err)
	}
	before := runner.Status()
	closeRaceRunner(t, runner)
	restored, err := tradingruntime.NewMarketRunner(context.Background(), market, memory, memory, tradingruntime.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	after := restored.Status()
	if before.Sequence != after.Sequence || before.StateHash != after.StateHash {
		closeRaceRunner(t, restored)
		t.Fatalf("restart changed state")
	}
	return restored, memory, market, buyer
}

func fundForRace(t *testing.T, runner *tradingruntime.MarketRunner, id string, account domain.AccountID, asset domain.Asset, amount int64) {
	t.Helper()
	if _, err := runner.Fund(context.Background(), domain.FundRequest{RequestID: id, AccountID: account, Asset: asset, Amount: amount}); err != nil {
		t.Fatal(err)
	}
}
func closeRaceRunner(t *testing.T, runner *tradingruntime.MarketRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runner.Close(ctx); err != nil {
		t.Errorf("close runner: %v", err)
	}
}
func auditRaceJournal(records []store.Record) (transactions, entries int, sums, fees map[domain.Asset]int64, duplicate, unbalancedTransaction bool, references map[string]struct{}) {
	sums = map[domain.Asset]int64{}
	fees = map[domain.Asset]int64{}
	references = map[string]struct{}{}
	ids := map[string]struct{}{}
	for _, record := range records {
		for _, tx := range record.Journal {
			transactions++
			references[tx.Reference] = struct{}{}
			if _, ok := ids[tx.ID]; ok {
				duplicate = true
			}
			ids[tx.ID] = struct{}{}
			transactionSums := map[domain.Asset]int64{}
			for _, entry := range tx.Entries {
				entries++
				sums[entry.Asset] += entry.Amount
				transactionSums[entry.Asset] += entry.Amount
				if entry.Account == ledger.PlatformFee(entry.Asset) {
					fees[entry.Asset] += entry.Amount
				}
			}
			for _, sum := range transactionSums {
				if sum != 0 {
					unbalancedTransaction = true
				}
			}
		}
	}
	return
}
func assertNoNegativeRaceBalance(t *testing.T, round int, balances []exchange.AssetBalanceView) {
	t.Helper()
	for _, balance := range balances {
		if balance.Available < 0 || balance.Held < 0 {
			t.Fatalf("round %d negative balance %+v", round, balance)
		}
	}
}
func assertRaceBalance(t *testing.T, round int, balances []exchange.AssetBalanceView, asset domain.Asset, available, held int64) {
	t.Helper()
	for _, balance := range balances {
		if balance.Asset == asset {
			if balance.Available != available || balance.Held != held {
				t.Fatalf("round %d %s balance=%+v want available=%d held=%d", round, asset, balance, available, held)
			}
			return
		}
	}
	t.Fatalf("round %d missing %s balance", round, asset)
}
