package grpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/grpc/proto"
)

const MaxRecvMessageSize = 16 * 1024 * 1024

type MarketRpcConfig struct {
	Host string
	Port int
}

type MarketRpcService struct {
	*MarketRpcConfig

	db       *database.DB
	mu       sync.Mutex
	server   *grpc.Server
	listener net.Listener

	proto.UnimplementedMarketServicesServer
	stopped atomic.Bool
}

func NewMarketRpcService(conf *MarketRpcConfig, db *database.DB) (*MarketRpcService, error) {
	return &MarketRpcService{
		MarketRpcConfig: conf,
		db:              db,
	}, nil
}

func (ms *MarketRpcService) Start(ctx context.Context) error {
	rpcAddr := net.JoinHostPort(ms.MarketRpcConfig.Host, fmt.Sprint(ms.MarketRpcConfig.Port))
	listener, err := net.Listen("tcp", rpcAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", rpcAddr, err)
	}

	gs := grpc.NewServer(grpc.MaxRecvMsgSize(MaxRecvMessageSize))
	reflection.Register(gs)
	proto.RegisterMarketServicesServer(gs, ms)

	ms.mu.Lock()
	ms.server = gs
	ms.listener = listener
	ms.mu.Unlock()

	log.Info("grpc server started", "addr", listener.Addr())
	go func() {
		if err := gs.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			log.Error("gRPC server stopped unexpectedly", "err", err)
		}
	}()
	return nil
}

func (ms *MarketRpcService) Stop(ctx context.Context) error {
	if ms.stopped.Swap(true) {
		return nil
	}

	ms.mu.Lock()
	gs := ms.server
	ms.server = nil
	ms.listener = nil
	ms.mu.Unlock()

	if gs != nil {
		done := make(chan struct{})
		go func() {
			gs.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
			gs.Stop()
			<-done
		}
	}

	if ms.db != nil {
		if err := ms.db.Close(); err != nil {
			return fmt.Errorf("close database: %w", err)
		}
	}
	log.Info("gRPC server stopped")
	return nil
}

func (ms *MarketRpcService) Stopped() bool {
	return ms.stopped.Load()
}
