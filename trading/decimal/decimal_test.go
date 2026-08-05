package decimal_test

import (
	"errors"
	"math"
	"testing"

	"github.com/the-web3/s78-market-services/trading/decimal"
)

func TestParseAndFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		scale int64
		atoms int64
	}{
		{value: "0", scale: 100_000_000, atoms: 0},
		{value: "0.000001", scale: 100_000_000, atoms: 100},
		{value: "60000.01", scale: 1_000_000, atoms: 60_000_010_000},
		{value: "20000", scale: 1_000_000, atoms: 20_000_000_000},
	}
	for _, test := range tests {
		atoms, err := decimal.Parse(test.value, test.scale)
		if err != nil {
			t.Fatalf("Parse(%q): %v", test.value, err)
		}
		if atoms != test.atoms {
			t.Fatalf("Parse(%q) = %d, want %d", test.value, atoms, test.atoms)
		}
		formatted, err := decimal.Format(atoms, test.scale)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := decimal.Parse(formatted, test.scale)
		if err != nil || roundTrip != atoms {
			t.Fatalf("round trip %q = %d, %v", formatted, roundTrip, err)
		}
	}
}

func TestRejectsRoundingSignsExponentWhitespaceAndOverflow(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"", " 1", "1 ", "+1", "-1", "1e2", ".1", "1.", "1.0000001", "a",
	} {
		if _, err := decimal.Parse(value, 1_000_000); !errors.Is(err, decimal.ErrInvalidDecimal) {
			t.Fatalf("Parse(%q) error = %v", value, err)
		}
	}
	if _, err := decimal.Parse("9223372036854775808", 1); !errors.Is(err, decimal.ErrInvalidDecimal) {
		t.Fatalf("overflow error = %v", err)
	}
	if formatted, err := decimal.Format(math.MaxInt64, 1); err != nil ||
		formatted != "9223372036854775807" {
		t.Fatalf("max format = %q, %v", formatted, err)
	}
	if _, err := decimal.Format(-1, 100); !errors.Is(err, decimal.ErrInvalidDecimal) {
		t.Fatalf("negative format error = %v", err)
	}
}
