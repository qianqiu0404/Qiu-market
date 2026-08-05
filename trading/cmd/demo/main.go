package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/store"
)

func main() {
	ctx := context.Background()
	market := domain.DefaultBTCUSDTMarket()
	memory := store.NewMemory()
	trading, err := exchange.New(market, memory, memory)
	must(err)

	fmt.Println("S78 Trading Core Lab — BTC/USDT virtual funds only")
	fmt.Println(strings.Repeat("=", 64))

	run(trading.Fund(ctx, domain.FundRequest{
		RequestID: "demo-fund-maker-btc",
		AccountID: "maker",
		Asset:     "BTC",
		Amount:    20_000_000,
	}))
	run(trading.Fund(ctx, domain.FundRequest{
		RequestID: "demo-fund-taker-usdt",
		AccountID: "taker",
		Asset:     "USDT",
		Amount:    20_000_000_000,
	}))
	printBalances(trading)

	makerOrder := run(trading.Submit(ctx, domain.NewOrder{
		ClientOrderID: "demo-maker-sell",
		AccountID:     "maker",
		Side:          domain.SideSell,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         60_000_000_000,
		Quantity:      10_000_000,
	}))
	printResult("maker places 0.1 BTC sell @ 60,000", makerOrder)
	printBalances(trading)

	takerOrder := run(trading.Submit(ctx, domain.NewOrder{
		ClientOrderID: "demo-taker-buy",
		AccountID:     "taker",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceIOC,
		Price:         60_200_000_000,
		Quantity:      7_000_000,
	}))
	printResult("taker buys 0.07 BTC with price improvement", takerOrder)
	printBalances(trading)
	fmt.Printf("platform fees: BTC=%s, USDT=%s\n",
		formatFixed(trading.PlatformFees("BTC"), 8),
		formatFixed(trading.PlatformFees("USDT"), 6),
	)

	cancel := run(trading.Cancel(ctx, domain.CancelOrder{
		RequestID: "demo-cancel-maker",
		AccountID: "maker",
		OrderID:   makerOrder.OrderID,
	}))
	printResult("maker cancels the remaining 0.03 BTC", cancel)
	printBalances(trading)

	snapshot, err := trading.SaveSnapshot(ctx)
	must(err)
	beforeHash, err := trading.StateHash()
	must(err)
	restored, err := exchange.Restore(ctx, market, memory, memory)
	must(err)
	afterHash, err := restored.StateHash()
	must(err)
	if beforeHash != afterHash {
		log.Fatalf("restored state hash mismatch: before=%s after=%s", beforeHash, afterHash)
	}
	fmt.Println(strings.Repeat("-", 64))
	fmt.Printf("snapshot sequence: %d\n", snapshot.Sequence)
	fmt.Printf("restored sequence: %d\n", restored.Sequence())
	fmt.Printf("state hash: %s\n", afterHash)
	fmt.Println("recovery verified: snapshot + event log reproduced the same state")
}

func run(result domain.Result, err error) domain.Result {
	must(err)
	return result
}

func printResult(title string, result domain.Result) {
	fmt.Println(strings.Repeat("-", 64))
	fmt.Printf("%s\nsequence=%d order=%s status=%s\n", title, result.Sequence, result.OrderID, result.Status)
	for _, event := range result.Events {
		if event.Trade == nil {
			fmt.Printf("  [%d] %s reason=%s remaining=%d\n",
				event.Index, event.Type, event.Reason, event.Remaining)
			continue
		}
		trade := event.Trade
		fmt.Printf("  [%d] trade=%s price=%s qty=%s quote=%s maker=%s taker=%s\n",
			event.Index,
			trade.ID,
			formatFixed(trade.Price, 6),
			formatFixed(trade.Quantity, 8),
			formatFixed(trade.QuoteAmount, 6),
			trade.MakerOrderID,
			trade.TakerOrderID,
		)
	}
}

func printBalances(trading *exchange.Exchange) {
	fmt.Println("balances:")
	for _, accountID := range []domain.AccountID{"maker", "taker"} {
		btc := trading.Balance(accountID, "BTC")
		usdt := trading.Balance(accountID, "USDT")
		fmt.Printf("  %-5s BTC available=%-12s held=%-12s | USDT available=%-14s held=%s\n",
			accountID,
			formatFixed(btc.Available, 8),
			formatFixed(btc.Held, 8),
			formatFixed(usdt.Available, 6),
			formatFixed(usdt.Held, 6),
		)
	}
}

func formatFixed(value int64, scale int) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	divisor := int64(1)
	for i := 0; i < scale; i++ {
		divisor *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", sign, value/divisor, scale, value%divisor)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
