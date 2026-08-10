package fullstackgolden

import (
	"math"
	"testing"
)

func TestFormatSignedDecimalIsExactAndNeverOverflowsMinimumInt64(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		atoms int64
		scale int64
		want  string
	}{
		"positive": {1_234_500, 1_000_000, "1.2345"},
		"negative": {-1_234_500, 1_000_000, "-1.2345"},
		"zero":     {0, 100, "0"},
		"minimum":  {math.MinInt64, 1_000_000, "-9223372036854.775808"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := formatSignedDecimal(test.atoms, test.scale)
			if err != nil || got != test.want {
				t.Fatalf("formatSignedDecimal(%d,%d)=%q,%v want %q", test.atoms, test.scale, got, err, test.want)
			}
		})
	}
	if _, err := formatSignedDecimal(-1, 12); err == nil {
		t.Fatal("accepted a non-decimal scale")
	}
}
