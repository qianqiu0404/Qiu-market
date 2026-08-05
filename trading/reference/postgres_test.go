package reference

import (
	"math"
	"testing"
)

func TestDecimalAtomsUsesQuoteScaleAndFloors(t *testing.T) {
	t.Parallel()
	atoms, err := decimalAtoms("61234.5678909", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if atoms != 61_234_567_890 {
		t.Fatalf("atoms = %d", atoms)
	}
}

func TestDecimalAtomsRejectsInvalidAndOverflow(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "0", "-1", "NaN"} {
		if _, err := decimalAtoms(value, 1_000_000); err == nil {
			t.Fatalf("accepted invalid reference %q", value)
		}
	}
	overflow := "9223372036854.775808"
	if _, err := decimalAtoms(overflow, 1_000_000); err == nil {
		t.Fatalf("accepted value above %d atoms", int64(math.MaxInt64))
	}
}
