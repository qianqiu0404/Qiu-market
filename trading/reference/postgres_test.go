package reference

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

func TestPostgresReferenceQueryHasBoundedTimeout(t *testing.T) {
	t.Parallel()
	source, err := newPostgresSource(hungQueryer{}, 1_000_000, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = source.Current(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung query error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("hung query exceeded bounded timeout: %s", elapsed)
	}
}

type hungQueryer struct{}

func (hungQueryer) QueryRow(ctx context.Context, _ string, _ ...any) pgx.Row {
	return contextRow{ctx: ctx}
}

type contextRow struct{ ctx context.Context }

func (r contextRow) Scan(...any) error {
	<-r.ctx.Done()
	return r.ctx.Err()
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
