package domain_test

import (
	"errors"
	"math"
	"testing"

	"github.com/the-web3/s78-market-services/trading/domain"
)

func TestCheckedArithmetic(t *testing.T) {
	t.Parallel()

	if got, err := domain.CheckedAdd(40, 2); err != nil || got != 42 {
		t.Fatalf("CheckedAdd(40, 2) = %d, %v", got, err)
	}
	if _, err := domain.CheckedAdd(math.MaxInt64, 1); !errors.Is(err, domain.ErrArithmeticOverflow) {
		t.Fatalf("CheckedAdd overflow error = %v", err)
	}
	if got, err := domain.CheckedMul(6_000, 100_000); err != nil || got != 600_000_000 {
		t.Fatalf("CheckedMul = %d, %v", got, err)
	}
	if _, err := domain.CheckedMul(math.MaxInt64, 2); !errors.Is(err, domain.ErrArithmeticOverflow) {
		t.Fatalf("CheckedMul overflow error = %v", err)
	}
	if got, err := domain.CheckedMulDivFloor(math.MaxInt64, math.MaxInt64, math.MaxInt64); err != nil || got != math.MaxInt64 {
		t.Fatalf("CheckedMulDivFloor 128-bit intermediate = %d, %v", got, err)
	}
	if got, err := domain.CheckedMulDivCeil(10, 10, 6); err != nil || got != 17 {
		t.Fatalf("CheckedMulDivCeil = %d, %v", got, err)
	}
}

func TestNewOrderValidation(t *testing.T) {
	t.Parallel()

	market := testMarket()
	valid := domain.NewOrder{
		ClientOrderID: "limit-1",
		AccountID:     "alice",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeLimit,
		TimeInForce:   domain.TimeInForceGTC,
		Price:         60_000,
		Quantity:      100,
	}
	if err := valid.Validate(market); err != nil {
		t.Fatalf("valid limit order rejected: %v", err)
	}

	postOnlyIOC := valid
	postOnlyIOC.ClientOrderID = "invalid-post-only"
	postOnlyIOC.PostOnly = true
	postOnlyIOC.TimeInForce = domain.TimeInForceIOC
	if err := postOnlyIOC.Validate(market); !errors.Is(err, domain.ErrInvalidOrder) {
		t.Fatalf("post-only IOC error = %v", err)
	}

	marketBuy := domain.NewOrder{
		ClientOrderID: "market-buy",
		AccountID:     "alice",
		Side:          domain.SideBuy,
		Type:          domain.OrderTypeMarket,
		TimeInForce:   domain.TimeInForceIOC,
		QuoteBudget:   1_000_000,
	}
	if err := marketBuy.Validate(market); err != nil {
		t.Fatalf("valid market buy rejected: %v", err)
	}

	overflow := valid
	overflow.ClientOrderID = "overflow"
	overflow.Price = math.MaxInt64
	overflow.Quantity = 2
	if err := overflow.Validate(market); !errors.Is(err, domain.ErrInvalidOrder) {
		t.Fatalf("overflow order error = %v", err)
	}
}

func TestFeeAmountUsesIntegerFloor(t *testing.T) {
	t.Parallel()

	got, err := domain.FeeAmount(12_345, 17)
	if err != nil {
		t.Fatal(err)
	}
	if got != 20 {
		t.Fatalf("FeeAmount = %d, want 20", got)
	}
}

func TestDefaultBTCUSDTFixedPointAndRounding(t *testing.T) {
	t.Parallel()

	market := domain.DefaultBTCUSDTMarket()
	if err := market.Validate(); err != nil {
		t.Fatal(err)
	}
	invalidEpoch := market
	invalidEpoch.ConfigurationEpoch = 0
	if err := invalidEpoch.Validate(); !errors.Is(err, domain.ErrInvalidMarket) {
		t.Fatalf("zero configuration epoch error = %v", err)
	}
	const (
		price    = int64(60_000_010_000)
		quantity = int64(1_001)
	)
	floor, err := market.QuoteAmountFloor(price, quantity)
	if err != nil {
		t.Fatal(err)
	}
	ceil, err := market.QuoteAmountCeil(price, quantity)
	if err != nil {
		t.Fatal(err)
	}
	if floor != 600_600 || ceil != 600_601 {
		t.Fatalf("quote rounding floor/ceil = %d/%d, want 600600/600601", floor, ceil)
	}
	affordable, err := market.AffordableQuantity(floor, price)
	if err != nil {
		t.Fatal(err)
	}
	if affordable != 1_000 {
		t.Fatalf("affordable quantity = %d, want 1000 aligned base atoms", affordable)
	}
}

func testMarket() domain.Market {
	return domain.Market{
		ID:                 "BTC-USDT",
		BaseAsset:          "BTC",
		QuoteAsset:         "USDT",
		BaseScale:          1,
		QuoteScale:         1,
		PriceTick:          1,
		QuantityStep:       1,
		MinQuantity:        1,
		MinNotional:        1,
		MakerFeeBPS:        10,
		TakerFeeBPS:        20,
		ConfigurationEpoch: 1,
	}
}
