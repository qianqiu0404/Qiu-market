package gateway

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPracticeStateOwnershipMarker(t *testing.T) {
	dsn := os.Getenv("QIU_T1_TEST_STATE_DSN")
	if dsn == "" {
		t.Skip("QIU_T1_TEST_STATE_DSN is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var marker string
	if err := pool.QueryRow(context.Background(), `
		SELECT owner_key FROM qiu_trading_practice_owner WHERE singleton=TRUE
	`).Scan(&marker); err != nil || marker != practiceOwnerMarker {
		t.Fatalf("marker=%q err=%v", marker, err)
	}
}
