// Command full-stack-golden runs the loopback-only Qiu full-stack verification
// roles. The coordinator role is added incrementally; backend-child is the
// durable PostgreSQL matching process used for cross-PID recovery evidence.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/the-web3/s78-market-services/fullstackgolden"
)

const postgresDSNEnvironment = "QIU_FULLSTACK_POSTGRES_DSN"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("full-stack-golden", flag.ContinueOnError)
	role := flags.String("role", "", "process role: coordinator or backend-child")
	grpcAddress := flags.String("grpc-address", "", "explicit loopback gRPC address")
	fixtureAddress := flags.String("fixture-address", "", "explicit loopback fixture address")
	fixtureCert := flags.String("fixture-cert", "", "absolute loopback fixture TLS certificate")
	fixtureKey := flags.String("fixture-key", "", "absolute loopback fixture TLS private key")
	httpAddress := flags.String("http-address", "127.0.0.1:0", "explicit loopback coordinator HTTP address")
	repoRoot := flags.String("repo-root", "", "absolute repository root")
	manifest := flags.String("manifest", "", "absolute runtime manifest path")
	frontendOrigin := flags.String("frontend-origin", "", "loopback Vue origin")
	postgresPID := flags.Int("postgres-pid", 0, "isolated PostgreSQL server PID")
	postgresVersion := flags.String("postgres-version", "", "isolated PostgreSQL version")
	fixturePID := flags.Int("fixture-pid", 0, "loopback upstream fixture PID")
	fixtureOrigin := flags.String("fixture-origin", "", "loopback upstream fixture origin")
	fixtureCA := flags.String("fixture-ca", "", "absolute loopback fixture CA certificate")
	vuePID := flags.Int("vue-pid", 0, "Vue preview PID")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("full-stack-golden: unexpected positional arguments")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	switch *role {
	case "fixture":
		return fullstackgolden.RunFixture(ctx, *fixtureAddress, *fixtureCert, *fixtureKey)
	case "backend-child":
		return fullstackgolden.RunBackendChild(ctx, fullstackgolden.BackendChildConfig{
			PostgresURL: os.Getenv(postgresDSNEnvironment),
			GRPCAddress: *grpcAddress,
		})
	case "coordinator":
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		coordinator, err := fullstackgolden.StartCoordinator(ctx, fullstackgolden.CoordinatorConfig{
			PostgresURL: os.Getenv(postgresDSNEnvironment), RepoRoot: *repoRoot, Executable: executable,
			HTTPAddress: *httpAddress, GRPCAddress: *grpcAddress, FrontendOrigin: *frontendOrigin,
			ManifestPath: *manifest, FixturePID: *fixturePID, VuePID: *vuePID, FixtureOrigin: *fixtureOrigin, FixtureCAPath: *fixtureCA,
			Postgres: fullstackgolden.PostgresEvidence{PID: *postgresPID, Version: *postgresVersion, Authority: "isolated_ephemeral_postgresql"},
		})
		if err != nil {
			return err
		}
		<-ctx.Done()
		closeContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return coordinator.Close(closeContext)
	default:
		return fmt.Errorf("full-stack-golden: unsupported role %q", *role)
	}
}
