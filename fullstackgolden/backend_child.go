package fullstackgolden

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	tradingservice "github.com/the-web3/s78-market-services/trading/service"
)

// BackendChildConfig is the private process boundary used by the full-stack
// coordinator. The DSN is process-injected and must never be serialized or
// logged. Only an explicit loopback gRPC address is accepted.
type BackendChildConfig struct {
	PostgresURL string `json:"-"`
	GRPCAddress string `json:"grpc_address"`
}

func (c BackendChildConfig) Validate() error {
	if strings.TrimSpace(c.PostgresURL) == "" {
		return fmt.Errorf("full-stack backend: PostgreSQL URL is required")
	}
	host, port, err := net.SplitHostPort(c.GRPCAddress)
	if err != nil || port == "" {
		return fmt.Errorf("full-stack backend: explicit gRPC host and port are required")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("full-stack backend: gRPC must bind to an IP loopback address")
	}
	return nil
}

// RunBackendChild runs the production PostgreSQL-backed matching service in a
// dedicated process. Cancellation performs a graceful runner/snapshot/outbox
// shutdown before the process exits.
func RunBackendChild(ctx context.Context, config BackendChildConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	childContext, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	backend, err := tradingservice.New(childContext, tradingservice.Config{
		PostgresURL:       config.PostgresURL,
		GRPCAddress:       config.GRPCAddress,
		DemoMakerEnabled:  false,
		CursorHMACCurrent: "full-stack-golden:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		SnapshotEvery:     4,
	}, cancel)
	if err != nil {
		return fmt.Errorf("construct full-stack backend: %w", err)
	}
	if err := backend.Start(childContext); err != nil {
		stopContext, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		return fmt.Errorf("start full-stack backend: %w", errorsJoin(err, backend.Stop(stopContext)))
	}
	<-childContext.Done()
	stopContext, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCancel()
	if err := backend.Stop(stopContext); err != nil {
		return fmt.Errorf("stop full-stack backend: %w", err)
	}
	if cause := context.Cause(childContext); cause != nil && cause != context.Canceled {
		return cause
	}
	return nil
}

func errorsJoin(left, right error) error {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return fmt.Errorf("%v; cleanup: %w", left, right)
}
