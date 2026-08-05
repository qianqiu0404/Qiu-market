package reliability_test

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/reliability"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	"github.com/the-web3/s78-market-services/trading/store"
)

const (
	defaultReliabilitySeed  int64 = 20_260_730
	defaultReliabilitySteps       = 192
	maxReliabilitySteps           = 2_048
)

var (
	reliabilitySeed = flag.Int64(
		"reliability.seed",
		defaultReliabilitySeed,
		"seed for bounded trading reliability command tests",
	)
	reliabilitySteps = flag.Int(
		"reliability.steps",
		defaultReliabilitySteps,
		"bounded command count for trading reliability tests",
	)
)

func TestBoundedRandomCommandSequence(t *testing.T) {
	steps := boundedReliabilitySteps(*reliabilitySteps)
	t.Logf(
		"seed=%d steps=%d replay: go test ./trading/reliability -run '^TestBoundedRandomCommandSequence$' -count=1 -args -reliability.seed=%d -reliability.steps=%d",
		*reliabilitySeed,
		steps,
		*reliabilitySeed,
		steps,
	)
	_, _, summary := runBoundedRandomCommands(t, *reliabilitySeed, steps)
	t.Logf(
		"seed=%d final_sequence=%d records=%d final_hash=%s",
		summary.Seed,
		summary.Sequence,
		summary.Records,
		summary.StateHash,
	)
}

func TestConcurrentSameIDAndUniqueCommands(t *testing.T) {
	const (
		duplicateWorkers = 32
		uniqueWorkers    = 32
		initialAmount    = int64(1_000)
		duplicateAmount  = int64(50)
	)
	persistence := store.NewMemory()
	runner, err := tradingruntime.NewMarketRunner(
		context.Background(),
		reliabilityMarket(),
		persistence,
		persistence,
		tradingruntime.Config{
			QueueSize:       duplicateWorkers + uniqueWorkers + 8,
			SnapshotEvery:   1_000,
			SnapshotTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runner.Close(ctx); err != nil {
			t.Errorf("close concurrent runner: %v", err)
		}
	})
	fundRunner(t, runner, "parallel-initial", "parallel", "USDT", initialAmount)

	type concurrentResult struct {
		duplicate bool
		index     int
		result    domain.Result
		err       error
	}
	start := make(chan struct{})
	results := make(chan concurrentResult, duplicateWorkers+uniqueWorkers)
	var workers sync.WaitGroup
	for worker := 0; worker < duplicateWorkers; worker++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			result, err := runner.Fund(context.Background(), domain.FundRequest{
				RequestID: "parallel-shared-id",
				AccountID: "parallel",
				Asset:     "USDT",
				Amount:    duplicateAmount,
			})
			results <- concurrentResult{
				duplicate: true,
				index:     index,
				result:    result,
				err:       err,
			}
		}(worker)
	}
	for worker := 0; worker < uniqueWorkers; worker++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			result, err := runner.Fund(context.Background(), domain.FundRequest{
				RequestID: fmt.Sprintf("parallel-unique-%02d", index),
				AccountID: "parallel",
				Asset:     "USDT",
				Amount:    int64(index + 1),
			})
			results <- concurrentResult{
				index:  index,
				result: result,
				err:    err,
			}
		}(worker)
	}
	close(start)
	workers.Wait()
	close(results)

	var duplicateSequence uint64
	uniqueSequences := make(map[uint64]int, uniqueWorkers)
	for concurrent := range results {
		if concurrent.err != nil {
			t.Fatalf("worker duplicate=%t index=%d: %v",
				concurrent.duplicate, concurrent.index, concurrent.err)
		}
		if concurrent.duplicate {
			if duplicateSequence == 0 {
				duplicateSequence = concurrent.result.Sequence
			} else if concurrent.result.Sequence != duplicateSequence {
				t.Fatalf("same-id sequences = %d and %d",
					duplicateSequence, concurrent.result.Sequence)
			}
			continue
		}
		if previous, duplicate := uniqueSequences[concurrent.result.Sequence]; duplicate {
			t.Fatalf("unique workers %d and %d share sequence %d",
				previous, concurrent.index, concurrent.result.Sequence)
		}
		uniqueSequences[concurrent.result.Sequence] = concurrent.index
	}
	if duplicateSequence == 0 {
		t.Fatal("same-id workers produced no committed sequence")
	}
	if _, collision := uniqueSequences[duplicateSequence]; collision {
		t.Fatalf("same-id sequence %d collided with a unique command", duplicateSequence)
	}

	wantRecords := uint64(1 + 1 + uniqueWorkers)
	if persistence.RecordCount() != wantRecords ||
		runner.Status().Sequence != wantRecords {
		t.Fatalf("concurrent durable/runtime sequences = %d/%d, want %d",
			persistence.RecordCount(), runner.Status().Sequence, wantRecords)
	}
	wantBalance := initialAmount + duplicateAmount +
		int64(uniqueWorkers*(uniqueWorkers+1)/2)
	balance, err := runner.Balance("parallel", "USDT")
	if err != nil {
		t.Fatal(err)
	}
	if balance != (exchange.BalanceView{Available: wantBalance}) {
		t.Fatalf("concurrent balance = %+v, want available=%d", balance, wantBalance)
	}

	proof, err := reliability.ProveRecovery(
		context.Background(),
		reliabilityMarket(),
		persistence,
		persistence,
	)
	if err != nil {
		t.Fatal(err)
	}
	if proof.RestoredSequence != wantRecords ||
		proof.Ledger.Transactions != int(wantRecords) ||
		proof.Ledger.AssetNet["USDT"] != 0 {
		t.Fatalf("concurrent recovery proof = %+v", proof)
	}
}

func FuzzBoundedCommandRecovery(f *testing.F) {
	f.Add(defaultReliabilitySeed, uint8(32))
	f.Add(int64(1), uint8(63))
	f.Add(int64(-1), uint8(17))

	f.Fuzz(func(t *testing.T, seed int64, rawSteps uint8) {
		steps := 1 + int(rawSteps%64)
		runBoundedRandomCommands(t, seed, steps)
	})
}

func BenchmarkAuditAndRestore(b *testing.B) {
	b.StopTimer()
	_, persistence, summary := runBoundedRandomCommands(
		b,
		defaultReliabilitySeed,
		96,
	)
	b.ReportAllocs()
	b.ReportMetric(float64(summary.Records), "records/op")
	b.StartTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		proof, err := reliability.ProveRecovery(
			context.Background(),
			reliabilityMarket(),
			persistence,
			persistence,
		)
		if err != nil {
			b.Fatal(err)
		}
		if proof.RestoredSequence != summary.Sequence ||
			proof.RestoredStateHash != summary.StateHash {
			b.Fatalf("iteration %d proof = %+v summary=%+v",
				iteration, proof, summary)
		}
	}
}

type randomRunSummary struct {
	Seed      int64
	Sequence  uint64
	Records   int
	StateHash string
}

type replayableCommand struct {
	kind     domain.CommandKind
	fund     domain.FundRequest
	submit   domain.NewOrder
	cancel   domain.CancelOrder
	expected domain.Result
	label    string
}

func (command replayableCommand) execute(
	ctx context.Context,
	live *exchange.Exchange,
) (domain.Result, error) {
	switch command.kind {
	case domain.CommandKindFund:
		return live.Fund(ctx, command.fund)
	case domain.CommandKindSubmitOrder:
		return live.Submit(ctx, command.submit)
	case domain.CommandKindCancelOrder:
		return live.Cancel(ctx, command.cancel)
	default:
		return domain.Result{}, fmt.Errorf("unsupported randomized command kind %d", command.kind)
	}
}

func runBoundedRandomCommands(
	t testing.TB,
	seed int64,
	steps int,
) (*exchange.Exchange, *store.Memory, randomRunSummary) {
	t.Helper()
	steps = boundedReliabilitySteps(steps)
	random := rand.New(rand.NewSource(seed))
	persistence := store.NewMemory()
	live, err := exchange.New(reliabilityMarket(), persistence, persistence)
	if err != nil {
		randomFailure(t, seed, -1, "new exchange", "%v", err)
	}
	history := make([]replayableCommand, 0, steps+6)
	for _, accountID := range []domain.AccountID{"alice", "bob", "carol"} {
		for _, asset := range []domain.Asset{"BTC", "USDT"} {
			command := replayableCommand{
				kind: domain.CommandKindFund,
				fund: domain.FundRequest{
					RequestID: fmt.Sprintf("seed-%d-initial-%s-%s", seed, accountID, asset),
					AccountID: accountID,
					Asset:     asset,
					Amount:    1_000_000_000,
				},
				label: fmt.Sprintf("initial fund %s/%s", accountID, asset),
			}
			command.expected, err = command.execute(context.Background(), live)
			if err != nil {
				randomFailure(t, seed, -1, command.label, "%v", err)
			}
			history = append(history, command)
		}
	}

	for step := 0; step < steps; step++ {
		action := random.Intn(100)
		label := ""
		switch {
		case action < 10:
			command := randomFundCommand(seed, step, random)
			label = command.label
			command.expected, err = command.execute(context.Background(), live)
			if err != nil {
				randomFailure(t, seed, step, label, "%v", err)
			}
			history = append(history, command)

		case action < 67:
			command := randomSubmitCommand(seed, step, random)
			label = command.label
			command.expected, err = command.execute(context.Background(), live)
			if err != nil {
				randomFailure(t, seed, step, label, "%v", err)
			}
			history = append(history, command)

		case action < 81:
			command, found := randomCancelCommand(seed, step, random, live)
			if !found {
				command = randomSubmitCommand(seed, step, random)
			}
			label = command.label
			command.expected, err = command.execute(context.Background(), live)
			if err != nil {
				randomFailure(t, seed, step, label, "%v", err)
			}
			history = append(history, command)

		case action < 91:
			command := history[random.Intn(len(history))]
			label = "replay " + command.label
			beforeRecords := persistence.RecordCount()
			result, executeErr := command.execute(context.Background(), live)
			if executeErr != nil {
				randomFailure(t, seed, step, label, "%v", executeErr)
			}
			if !reflect.DeepEqual(result, command.expected) {
				randomFailure(
					t,
					seed,
					step,
					label,
					"result=%+v want=%+v",
					result,
					command.expected,
				)
			}
			if persistence.RecordCount() != beforeRecords {
				randomFailure(
					t,
					seed,
					step,
					label,
					"record count changed from %d to %d",
					beforeRecords,
					persistence.RecordCount(),
				)
			}

		case action < 96:
			label = "save snapshot"
			if _, err := live.SaveSnapshot(context.Background()); err != nil {
				randomFailure(t, seed, step, label, "%v", err)
			}

		default:
			label = "restore snapshot plus tail"
			live = restoreAndCompareRandomState(t, seed, step, label, live, persistence)
		}

		if err := live.Validate(); err != nil {
			randomFailure(t, seed, step, label, "validate: %v", err)
		}
		if step%8 == 0 {
			assertRandomHistory(t, seed, step, label, live, persistence)
		}
		if step%17 == 16 {
			live = restoreAndCompareRandomState(
				t,
				seed,
				step,
				label+"; periodic restore",
				live,
				persistence,
			)
		}
		if step%31 == 15 {
			if _, err := live.SaveSnapshot(context.Background()); err != nil {
				randomFailure(t, seed, step, label+"; periodic snapshot", "%v", err)
			}
		}
	}
	assertRandomHistory(t, seed, steps, "final audit", live, persistence)
	stateHash, err := live.StateHash()
	if err != nil {
		randomFailure(t, seed, steps, "final hash", "%v", err)
	}
	proof, err := reliability.ProveRecovery(
		context.Background(),
		reliabilityMarket(),
		persistence,
		persistence,
	)
	if err != nil {
		randomFailure(t, seed, steps, "final recovery proof", "%v", err)
	}
	if proof.RestoredSequence != live.Sequence() ||
		proof.RestoredStateHash != stateHash {
		randomFailure(
			t,
			seed,
			steps,
			"final recovery proof",
			"proof=%+v live_sequence=%d live_hash=%s",
			proof,
			live.Sequence(),
			stateHash,
		)
	}
	return live, persistence, randomRunSummary{
		Seed:      seed,
		Sequence:  live.Sequence(),
		Records:   proof.Ledger.Records,
		StateHash: stateHash,
	}
}

func randomFundCommand(seed int64, step int, random *rand.Rand) replayableCommand {
	accounts := []domain.AccountID{"alice", "bob", "carol"}
	assets := []domain.Asset{"BTC", "USDT"}
	accountID := accounts[random.Intn(len(accounts))]
	asset := assets[random.Intn(len(assets))]
	return replayableCommand{
		kind: domain.CommandKindFund,
		fund: domain.FundRequest{
			RequestID: fmt.Sprintf("seed-%d-fund-%04d", seed, step),
			AccountID: accountID,
			Asset:     asset,
			Amount:    int64(1 + random.Intn(10_000)),
		},
		label: fmt.Sprintf("fund %s/%s step=%d", accountID, asset, step),
	}
}

func randomSubmitCommand(seed int64, step int, random *rand.Rand) replayableCommand {
	accounts := []domain.AccountID{"alice", "bob", "carol"}
	accountID := accounts[random.Intn(len(accounts))]
	side := domain.SideBuy
	if random.Intn(2) == 1 {
		side = domain.SideSell
	}
	request := domain.NewOrder{
		ClientOrderID: fmt.Sprintf("seed-%d-order-%04d", seed, step),
		AccountID:     accountID,
		Side:          side,
	}
	if random.Intn(5) == 0 {
		request.Type = domain.OrderTypeMarket
		request.TimeInForce = domain.TimeInForceIOC
		if side == domain.SideBuy {
			request.QuoteBudget = int64(1 + random.Intn(25_000))
		} else {
			request.Quantity = int64(1 + random.Intn(250))
		}
	} else {
		request.Type = domain.OrderTypeLimit
		request.Price = int64(80 + random.Intn(41))
		request.Quantity = int64(1 + random.Intn(250))
		switch random.Intn(3) {
		case 0:
			request.TimeInForce = domain.TimeInForceGTC
		case 1:
			request.TimeInForce = domain.TimeInForceIOC
		default:
			request.TimeInForce = domain.TimeInForceFOK
		}
		if request.TimeInForce == domain.TimeInForceGTC && random.Intn(8) == 0 {
			request.PostOnly = true
		}
	}
	return replayableCommand{
		kind:   domain.CommandKindSubmitOrder,
		submit: request,
		label: fmt.Sprintf(
			"submit account=%s side=%s type=%s tif=%s step=%d",
			accountID,
			side,
			request.Type,
			request.TimeInForce,
			step,
		),
	}
}

func randomCancelCommand(
	seed int64,
	step int,
	random *rand.Rand,
	live *exchange.Exchange,
) (replayableCommand, bool) {
	openOnly := random.Intn(4) != 0
	orders := live.Orders("", openOnly)
	if len(orders) == 0 && openOnly {
		orders = live.Orders("", false)
	}
	if len(orders) == 0 {
		return replayableCommand{}, false
	}
	order := orders[random.Intn(len(orders))]
	return replayableCommand{
		kind: domain.CommandKindCancelOrder,
		cancel: domain.CancelOrder{
			RequestID: fmt.Sprintf("seed-%d-cancel-%04d", seed, step),
			AccountID: order.AccountID,
			OrderID:   order.ID,
		},
		label: fmt.Sprintf("cancel account=%s order=%s step=%d",
			order.AccountID, order.ID, step),
	}, true
}

func restoreAndCompareRandomState(
	t testing.TB,
	seed int64,
	step int,
	label string,
	live *exchange.Exchange,
	persistence *store.Memory,
) *exchange.Exchange {
	t.Helper()
	beforeHash, err := live.StateHash()
	if err != nil {
		randomFailure(t, seed, step, label, "hash before restore: %v", err)
	}
	restored, err := exchange.Restore(
		context.Background(),
		reliabilityMarket(),
		persistence,
		persistence,
	)
	if err != nil {
		randomFailure(t, seed, step, label, "restore: %v", err)
	}
	afterHash, err := restored.StateHash()
	if err != nil {
		randomFailure(t, seed, step, label, "hash after restore: %v", err)
	}
	if restored.Sequence() != live.Sequence() || afterHash != beforeHash {
		randomFailure(
			t,
			seed,
			step,
			label,
			"restore sequence/hash=%d/%s want=%d/%s",
			restored.Sequence(),
			afterHash,
			live.Sequence(),
			beforeHash,
		)
	}
	return restored
}

func assertRandomHistory(
	t testing.TB,
	seed int64,
	step int,
	label string,
	live *exchange.Exchange,
	persistence *store.Memory,
) {
	t.Helper()
	records, err := persistence.RecordsAfter(context.Background(), 0)
	if err != nil {
		randomFailure(t, seed, step, label, "load records: %v", err)
	}
	proof, err := reliability.AuditRecords(reliabilityMarket(), records)
	if err != nil {
		randomFailure(t, seed, step, label, "audit records: %v", err)
	}
	stateHash, err := live.StateHash()
	if err != nil {
		randomFailure(t, seed, step, label, "state hash: %v", err)
	}
	if proof.FinalSequence != live.Sequence() ||
		proof.FinalStateHash != stateHash {
		randomFailure(
			t,
			seed,
			step,
			label,
			"audit sequence/hash=%d/%s want=%d/%s",
			proof.FinalSequence,
			proof.FinalStateHash,
			live.Sequence(),
			stateHash,
		)
	}
	for asset, total := range proof.AssetNet {
		if total != 0 {
			randomFailure(t, seed, step, label, "asset %s net=%d", asset, total)
		}
	}
}

func boundedReliabilitySteps(steps int) int {
	if steps < 1 {
		return 1
	}
	if steps > maxReliabilitySteps {
		return maxReliabilitySteps
	}
	return steps
}

func randomFailure(
	t testing.TB,
	seed int64,
	step int,
	command string,
	format string,
	args ...any,
) {
	t.Helper()
	t.Fatalf(
		"seed=%d step=%d command=%q: %s",
		seed,
		step,
		command,
		fmt.Sprintf(format, args...),
	)
}
