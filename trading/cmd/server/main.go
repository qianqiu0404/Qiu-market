package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/the-web3/s78-market-services/trading/auth"
	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/httpapi"
	"github.com/the-web3/s78-market-services/trading/outbox"
	tradingv1 "github.com/the-web3/s78-market-services/trading/rpc/pb"
	tradingserver "github.com/the-web3/s78-market-services/trading/rpc/server"
	tradingruntime "github.com/the-web3/s78-market-services/trading/runtime"
	postgresstore "github.com/the-web3/s78-market-services/trading/store/postgres"
)

func main() {
	if err := run(); err != nil {
		log.Printf("trading server stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, config.postgresDSN)
	if err != nil {
		return fmt.Errorf("open trading database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping trading database: %w", err)
	}
	if err := postgresstore.EnsureSchema(ctx, pool); err != nil {
		return err
	}
	market := domain.DefaultBTCUSDTMarket()
	persistence, err := postgresstore.New(ctx, pool, market)
	if err != nil {
		return err
	}
	runner, err := tradingruntime.NewMarketRunner(
		ctx,
		market,
		persistence,
		persistence,
		tradingruntime.DefaultConfig(),
	)
	if err != nil {
		return err
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := runner.Close(closeContext); closeErr != nil {
			log.Printf("close trading runner: %v", closeErr)
		}
	}()
	publisher, err := outbox.New(persistence, outbox.DefaultConfig())
	if err != nil {
		return err
	}
	publisherContext, publisherCancel := context.WithCancel(ctx)
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		publisher.Run(publisherContext)
	}()
	defer func() {
		publisherCancel()
		<-publisherDone
	}()

	rpcService, err := tradingserver.New(
		runner,
		tradingserver.NewPostgresEventSource(persistence),
		tradingserver.DefaultConfig(),
		publisher,
	)
	if err != nil {
		return err
	}
	grpcListener, err := net.Listen("tcp", config.grpcAddress)
	if err != nil {
		return fmt.Errorf("listen trading gRPC: %w", err)
	}
	defer grpcListener.Close()
	grpcServer := grpc.NewServer()
	tradingv1.RegisterTradingServiceServer(grpcServer, rpcService)
	grpcErrors := make(chan error, 1)
	go func() {
		grpcErrors <- grpcServer.Serve(grpcListener)
	}()

	dialContext, cancelDial := context.WithTimeout(ctx, 5*time.Second)
	grpcConnection, err := grpc.DialContext(
		dialContext,
		config.grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	cancelDial()
	if err != nil {
		grpcServer.Stop()
		return fmt.Errorf("connect local trading gRPC: %w", err)
	}
	defer grpcConnection.Close()

	sessions, err := auth.NewPostgresSessionStore(pool, "qianqiu0404")
	if err != nil {
		return err
	}
	tickets, err := auth.NewTicketManager(30 * time.Second)
	if err != nil {
		return err
	}
	oauthStates, err := auth.NewOAuthStateManager(10 * time.Minute)
	if err != nil {
		return err
	}
	var github *auth.GitHubOAuth
	if config.githubClientID != "" && config.githubSecret != "" {
		github, err = auth.NewGitHubOAuth(auth.GitHubConfig{
			ClientID:     config.githubClientID,
			ClientSecret: config.githubSecret,
			RedirectURL:  config.githubRedirect,
		})
		if err != nil {
			return err
		}
	}
	httpConfig := httpapi.DefaultConfig()
	httpConfig.BindAddress = config.httpAddress
	httpConfig.AllowedOrigins = config.allowedOrigins
	httpConfig.LocalMode = config.localAuth
	httpConfig.SecureCookies = config.secureCookies
	api, err := httpapi.New(
		tradingv1.NewTradingServiceClient(grpcConnection),
		sessions,
		tickets,
		oauthStates,
		github,
		httpConfig,
	)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              config.httpAddress,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	httpErrors := make(chan error, 1)
	go func() {
		httpErrors <- httpServer.ListenAndServe()
	}()
	log.Printf(
		"S78 virtual trading ready: gRPC=%s HTTP=%s market=%s local_auth=%t",
		config.grpcAddress,
		config.httpAddress,
		market.ID,
		config.localAuth,
	)

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-grpcErrors:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			runErr = fmt.Errorf("trading gRPC server: %w", err)
		}
	case err := <-httpErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("trading HTTP server: %w", err)
		}
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownContext); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown trading HTTP server: %w", err)
	}
	grpcStopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcStopped)
	}()
	select {
	case <-grpcStopped:
	case <-shutdownContext.Done():
		grpcServer.Stop()
		if runErr == nil {
			runErr = fmt.Errorf("trading gRPC graceful shutdown timed out")
		}
	}
	return runErr
}
