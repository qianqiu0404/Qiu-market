//go:build research_online

package researchsignal

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// Run only with both gates:
// QIU_RESEARCH_ONLINE_SMOKE=1 go test -tags=research_online -run TestOnlineSmoke ./marketdata/researchsignal
func TestOnlineSmoke(t *testing.T) {
	if os.Getenv("QIU_RESEARCH_ONLINE_SMOKE") != "1" {
		t.Skip("set the explicit read-only smoke flag")
	}
	client, err := New(Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	summary, err := client.Summary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := client.Events(t.Context(), EventQuery{Market: "crypto", Asset: "BTC", Window: 168, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	freshness := "null"
	if summary.Data.FreshnessMinutes != nil {
		freshness = fmt.Sprintf("%d", *summary.Data.FreshnessMinutes)
	}
	t.Logf("read-only smoke summary_status=%s event_status=%s event_count=%d freshness_minutes=%s latency=%s",
		summary.Status, events.Status, len(events.Data.Items), freshness, time.Since(started))
}
