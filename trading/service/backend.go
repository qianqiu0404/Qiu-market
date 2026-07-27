package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/marketmaker"
	"github.com/the-web3/s78-market-services/trading/netutil"
	"github.com/the-web3/s78-market-services/trading/reference"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

const (
	demoMakerAccount = domain.AccountID("system:demo-maker")
	demoMakerBTC     = int64(1_000_000_000)     // 10 virtual BTC
	demoMakerUSDT    = int64(1_000_000_000_000) // 1,000,000 virtual USDT
)

type Config struct {
	PostgresURL      string
	GRPCAddress      string
	DemoMakerEnabled bool
	DiskPath         string
	MinWriteBytes    int64
}

// Backend is the only integrated process that owns the in-memory matching
// state. It exposes loopback gRPC but deliberately does not run an HTTP server.
type Backend struct {
	config   Config
	shutdown context.CancelCauseFunc

	pool        *pgxpool.Pool
	runner      *tradingruntime.MarketRunner
	grpcServer  *grpc.Server
	listener    net.Listener
	maker       *marketmaker.Maker
	makerCancel context.CancelFunc
	makerDone   chan struct{}

	started  atomic.Bool
	stopped  atomic.Bool
	stopOnce sync.Once
	stopErr  error
}

func New(
	ctx context.Context,
	config Config,
	shutdown context.CancelCauseFunc,
) (*Backend, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	pool, err := pgxpool.New(ctx, config.PostgresURL)
	if err != nil {
		return nil, fmt.Errorf("open trading PostgreSQL pool: %w", err)
	}
	cleanupPool := true
	defer func() {
		if cleanupPool {
			pool.Close()
		}
	}()
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping trading PostgreSQL: %w", err)
	}
	if err := postgresstore.VerifySchema(ctx, pool); err != nil {
		return nil, err
	}

	market := domain.DefaultBTCUSDTMarket()
	persistence, err := postgresstore.New(ctx, pool, market)
	if err != nil {
		return nil, err
	}
	runner, err := tradingruntime.NewMarketRunner(
		ctx,
		market,
		persistence,
		persistence,
		tradingruntime.DefaultConfig(),
	)
	if err != nil {
		return nil, err
	}
	cleanupRunner := true
	defer func() {
		if cleanupRunner {
			closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = runner.Close(closeContext)
			cancel()
		}
	}()

	rpcService, err := tradingserver.New(
		runner,
		tradingserver.NewPostgresEventSource(persistence),
		tradingserver.DefaultConfig(),
	)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", config.GRPCAddress)
	if err != nil {
		return nil, fmt.Errorf("listen trading gRPC on %s: %w", config.GRPCAddress, err)
	}
	cleanupListener := true
	defer func() {
		if cleanupListener {
			_ = listener.Close()
		}
	}()
	grpcServer := grpc.NewServer()
	tradingv1.RegisterTradingServiceServer(grpcServer, rpcService)

	var maker *marketmaker.Maker
	if config.DemoMakerEnabled {
		if err := bootstrapDemoMaker(ctx, runner, market); err != nil {
			return nil, err
		}
		source, err := reference.NewPostgresSource(pool, market.QuoteScale)
		if err != nil {
			return nil, err
		}
		var makerSource marketmaker.ReferenceSource = source
		if config.MinWriteBytes > 0 {
			makerSource = diskAwareReferenceSource{
				next:    source,
				path:    config.DiskPath,
				minimum: config.MinWriteBytes,
			}
		}
		makerConfig := marketmaker.DefaultConfig()
		makerConfig.AccountID = demoMakerAccount
		makerConfig.RequestPrefix, err = randomRequestPrefix()
		if err != nil {
			return nil, err
		}
		maker, err = marketmaker.New(market, runner, makerSource, makerConfig)
		if err != nil {
			return nil, err
		}
	}

	cleanupPool = false
	cleanupRunner = false
	cleanupListener = false
	return &Backend{
		config:     config,
		shutdown:   shutdown,
		pool:       pool,
		runner:     runner,
		grpcServer: grpcServer,
		listener:   listener,
		maker:      maker,
	}, nil
}

func validateConfig(config Config) error {
	if config.PostgresURL == "" {
		return fmt.Errorf("trading PostgreSQL URL is required")
	}
	if !netutil.IsIPLoopbackAddress(config.GRPCAddress) {
		return fmt.Errorf("trading gRPC must bind to an explicit IP loopback address")
	}
	if config.MinWriteBytes < 0 ||
		(config.MinWriteBytes > 0 && config.DiskPath == "") {
		return fmt.Errorf("demo maker disk guard requires a path and non-negative floor")
	}
	return nil
}

type diskAwareReferenceSource struct {
	next    marketmaker.ReferenceSource
	path    string
	minimum int64
}

func (s diskAwareReferenceSource) Current(ctx context.Context) (marketmaker.Reference, error) {
	var fileSystem syscall.Statfs_t
	if err := syscall.Statfs(s.path, &fileSystem); err != nil {
		return marketmaker.Reference{}, fmt.Errorf("check demo maker disk capacity: %w", err)
	}
	available := int64(fileSystem.Bavail) * int64(fileSystem.Bsize)
	if available < s.minimum {
		return marketmaker.Reference{}, fmt.Errorf(
			"demo maker paused: free disk %d is below %d",
			available,
			s.minimum,
		)
	}
	return s.next.Current(ctx)
}

func (b *Backend) Start(ctx context.Context) error {
	if !b.started.CompareAndSwap(false, true) {
		return fmt.Errorf("trading backend already started")
	}
	go func() {
		err := b.grpcServer.Serve(b.listener)
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) && !b.stopped.Load() {
			wrapped := fmt.Errorf("trading gRPC server: %w", err)
			log.Error("trading gRPC stopped unexpectedly", "err", wrapped)
			if b.shutdown != nil {
				b.shutdown(wrapped)
			}
		}
	}()
	if b.maker != nil {
		makerContext, makerCancel := context.WithCancel(ctx)
		b.makerCancel = makerCancel
		b.makerDone = make(chan struct{})
		go func() {
			defer close(b.makerDone)
			if err := b.maker.Run(makerContext); err != nil && !errors.Is(err, context.Canceled) {
				// Unsafe references pause and recover inside the maker. Only
				// an infrastructure error can terminate this goroutine.
				log.Warn("virtual demo maker terminated after infrastructure failure", "err", err)
			}
		}()
	}
	log.Info(
		"virtual trading backend ready",
		"grpc", b.listener.Addr().String(),
		"market", domain.DefaultBTCUSDTMarket().ID,
		"demo_maker", b.maker != nil,
	)
	return nil
}

func (b *Backend) Stop(ctx context.Context) error {
	b.stopOnce.Do(func() {
		b.stopped.Store(true)
		if b.makerCancel != nil {
			b.makerCancel()
		}
		if b.makerDone != nil {
			select {
			case <-b.makerDone:
			case <-ctx.Done():
				b.stopErr = errors.Join(b.stopErr, fmt.Errorf("stop demo maker: %w", ctx.Err()))
			}
		}

		grpcStopped := make(chan struct{})
		go func() {
			b.grpcServer.GracefulStop()
			close(grpcStopped)
		}()
		select {
		case <-grpcStopped:
		case <-ctx.Done():
			b.grpcServer.Stop()
			b.stopErr = errors.Join(b.stopErr, fmt.Errorf("stop trading gRPC: %w", ctx.Err()))
		}

		if err := b.runner.Close(ctx); err != nil {
			b.stopErr = errors.Join(b.stopErr, fmt.Errorf("close trading runner: %w", err))
		}
		_ = b.listener.Close()
		b.pool.Close()
		log.Info("virtual trading backend stopped")
	})
	return b.stopErr
}

func (b *Backend) Stopped() bool {
	return b.stopped.Load()
}

// GRPCAddress returns the actual loopback listener address. It is useful when
// callers request port 0 for isolated integration tests.
func (b *Backend) GRPCAddress() string {
	if b.listener == nil {
		return ""
	}
	return b.listener.Addr().String()
}

func bootstrapDemoMaker(
	ctx context.Context,
	runner *tradingruntime.MarketRunner,
	market domain.Market,
) error {
	requests := []domain.FundRequest{
		{
			RequestID: "demo-maker-bootstrap-btc-v1",
			AccountID: demoMakerAccount,
			Asset:     market.BaseAsset,
			Amount:    demoMakerBTC,
		},
		{
			RequestID: "demo-maker-bootstrap-usdt-v1",
			AccountID: demoMakerAccount,
			Asset:     market.QuoteAsset,
			Amount:    demoMakerUSDT,
		},
	}
	for _, request := range requests {
		balance, err := runner.Balance(request.AccountID, request.Asset)
		if err != nil {
			return fmt.Errorf("read virtual demo maker asset %s: %w", request.Asset, err)
		}
		if balance.Available > 0 || balance.Held > 0 {
			continue
		}
		if _, err := runner.Fund(ctx, request); err != nil {
			return fmt.Errorf("fund virtual demo maker asset %s: %w", request.Asset, err)
		}
	}
	return nil
}

func randomRequestPrefix() (string, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate demo-maker request prefix: %w", err)
	}
	return "demo-maker-" + hex.EncodeToString(value[:]), nil
}
