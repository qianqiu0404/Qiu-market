package grpc

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
	"github.com/the-web3/s78-market-services/services/grpc/proto"
	mktsvc "github.com/the-web3/s78-market-services/services/http/service"
)

const MaxRecvMessageSize = 1024 * 1024 * 30000

type MarketRpcConfig struct {
	Host string
	Port int
}

type MarketRpcService struct {
	*MarketRpcConfig

	db *database.DB
	// svc 是与 HTTP API 完全共用的业务层（services/http/service.HandleSvc），
	// gRPC 各 RPC 只做 proto <-> model 的转换与参数校验，不重复实现业务逻辑。
	svc mktsvc.RestService

	grpcServer *grpc.Server

	proto.UnimplementedMarketServiceServer
	stopped atomic.Bool
}

func NewMarketRpcService(conf *MarketRpcConfig, db *database.DB, redisCli *redis.Client) (*MarketRpcService, error) {
	return &MarketRpcService{
		MarketRpcConfig: conf,
		db:              db,
		svc: mktsvc.NewHandleSvc(
			db,
			db.Asset, db.Symbol, db.SymbolMarket,
			db.Exchange, db.ExchangeSymbol, db.SymbolKline,
			db.ProviderStatus,
			db.MarketAggregation,
			redisCli,
			nil, // gRPC 不提供分析 RPC，Doris 连接仅 HTTP API 使用
		),
	}, nil
}

func (ms *MarketRpcService) Start(ctx context.Context) error {
	go func(ms *MarketRpcService) {
		rpcAddr := fmt.Sprintf("%s:%d", ms.MarketRpcConfig.Host, ms.MarketRpcConfig.Port)
		listener, err := net.Listen("tcp", rpcAddr)
		if err != nil {
			log.Error("Could not start tcp listener", "addr", rpcAddr, "err", err)
			return
		}

		opt := grpc.MaxRecvMsgSize(MaxRecvMessageSize)

		gs := grpc.NewServer(opt)
		ms.grpcServer = gs

		reflection.Register(gs)

		proto.RegisterMarketServiceServer(gs, ms)

		log.Info("grpc market service started", "addr", listener.Addr())

		if err := gs.Serve(listener); err != nil {
			log.Error("start rpc server fail", "err", err)
		}
	}(ms)
	return nil
}

func (ms *MarketRpcService) Stop(ctx context.Context) error {
	if ms.grpcServer != nil {
		ms.grpcServer.GracefulStop()
	}
	ms.stopped.Store(true)
	return nil
}

func (ms *MarketRpcService) Stopped() bool {
	return ms.stopped.Load()
}
