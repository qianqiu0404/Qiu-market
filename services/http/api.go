package rest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/go-sql-driver/mysql" // Doris MySQL 协议驱动（分析查询只读使用）

	"github.com/the-web3/s78-market-services/common/httputil"
	"github.com/the-web3/s78-market-services/config"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/redis"
	"github.com/the-web3/s78-market-services/services/http/routes"
	"github.com/the-web3/s78-market-services/services/http/service"
	tradinggateway "github.com/the-web3/s78-market-services/trading/gateway"
)

const (
	HealthPath               = "/healthz"
	SupportAssetPath         = "/api/v1/get_support_assets"
	MarketDashboardPath      = "/api/v1/get_market_dashboard"
	AssetDashboardPath       = "/api/v1/get_asset_dashboard"
	MarketInsightsPath       = "/api/v1/get_market_insights"
	ExchangePath             = "/api/v1/get_exchanges"
	SymbolPath               = "/api/v1/get_symbols"
	KlinePath                = "/api/v1/get_klines"
	MarketSparklinesPath     = "/api/v1/get_market_sparklines"
	SystemOverviewPath       = "/api/v1/get_system_overview"
	FiatRatePath             = "/api/v1/get_fiat_rates"
	TopMoversPath            = "/api/v1/get_top_movers"
	KlineAnalyticsPath       = "/api/v1/get_kline_analytics"
	AssetMomentumPath        = "/api/v1/get_asset_momentum"
	MarketOverviewPath       = "/api/v2/get_market_overview"
	AssetDashboardV2Path     = "/api/v2/get_asset_dashboard"
	MarketPriceTicksPath     = "/api/v2/get_market_price_ticks"
	AssetMarketsPath         = "/api/v2/get_asset_markets"
	AssetVenuesPath          = "/api/v2/get_asset_venues"
	ProviderCatalogAuditPath = "/api/v2/get_provider_catalog_audit"
)

type ApiConfig struct {
	HttpServer   config.ServerConfig
	MetricServer config.ServerConfig
}

type API struct {
	router         *chi.Mux
	apiSvr         *httputil.HTTPServer
	db             *database.DB
	redisCli       *redis.Client
	dorisDB        *sql.DB
	trading        *tradinggateway.Gateway
	tradingHandler http.Handler
	stopped        atomic.Bool
}

func NewApi(ctx context.Context, cfg *config.Config) (*API, error) {
	out := &API{}
	if err := out.initFromConfig(ctx, cfg); err != nil {
		return nil, errors.Join(err, out.Stop(ctx))
	}
	return out, nil
}

func (a *API) initFromConfig(ctx context.Context, cfg *config.Config) error {
	if err := a.initDB(ctx, cfg); err != nil {
		return fmt.Errorf("failed to init DB: %w", err)
	}
	a.initRedis(cfg)
	a.initDoris(cfg)
	a.initTrading(ctx, cfg)
	a.initRouter(cfg.RestServer, cfg)
	if err := a.startServer(cfg.RestServer); err != nil {
		return fmt.Errorf("failed to start API server: %w", err)
	}
	return nil
}

// initTrading is deliberately fail-open for the market-data API. A missing
// migration, invalid local-auth boundary, or unavailable trading process
// degrades only /api/v1/trading/**; Markets and all existing read APIs still
// start normally.
func (a *API) initTrading(ctx context.Context, cfg *config.Config) {
	bindAddress := net.JoinHostPort(cfg.RestServer.Host, strconv.Itoa(cfg.RestServer.Port))
	gateway, err := tradinggateway.New(ctx, tradinggateway.Config{
		PostgresURL:    cfg.MasterDB.PostgresURL(),
		GRPCAddress:    cfg.Trading.GRPCAddress,
		BindAddress:    bindAddress,
		AllowedOrigins: cfg.Trading.AllowedOrigins,
		LocalAuth:      cfg.Trading.LocalAuth,
		SecureCookies:  cfg.Trading.SecureCookies,
		GitHubClientID: cfg.Trading.GitHubClientID,
		GitHubSecret:   cfg.Trading.GitHubSecret,
		GitHubRedirect: cfg.Trading.GitHubRedirect,
		DiskPath:       "/",
		MinWriteBytes:  15 << 30,
	})
	if err != nil {
		log.Warn("virtual trading gateway unavailable; market-data API remains healthy", "err", err)
		a.tradingHandler = tradinggateway.UnavailableHandler()
		return
	}
	a.trading = gateway
	a.tradingHandler = gateway.Handler()
}

// initRedis 与 runCrawler 的 redis 初始化方式一致，但 API 侧容忍失败：
// Redis 只是榜单接口的加速读路径，连不上时 GetTopMovers 会回退 SQL 排序，
// 不影响其它接口，因此只告警不阻断启动。
func (a *API) initRedis(cfg *config.Config) {
	redisClient, err := redis.New(redis.Config{
		Address:  cfg.RedisConfig.Addr,
		Password: cfg.RedisConfig.Password,
		DB:       cfg.RedisConfig.DB,
	})
	if err != nil {
		log.Warn("redis unavailable, top movers endpoint will use SQL fallback", "err", err)
		return
	}
	a.redisCli = redisClient
}

// initDoris 与 initRedis 同样的容错策略：Doris 只服务 get_kline_analytics，
// 启动时连不上只告警（dorisDB 保持 nil），接口调用时返回 ErrDorisUnavailable
// 标准错误信封，绝不阻断 api 进程启动或影响其它接口。
func (a *API) initDoris(cfg *config.Config) {
	if !cfg.Doris.Enabled() {
		log.Info("doris data warehouse disabled (MARKET_DORIS_HOST empty), get_kline_analytics will report unavailable")
		return
	}
	// interpolateParams=true：Doris 3.0 服务端预处理（COM_STMT_PREPARE）不支持
	// LIMIT ? 占位符（报 mismatched input 'LIMIT'），改为客户端插值后发送完整 SQL。
	// 该连接只跑 get_kline_analytics 一条查询，入参 interval 走白名单、limit 为
	// 有界整数，无注入面。
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?timeout=5s&readTimeout=10s&interpolateParams=true",
		cfg.Doris.User, cfg.Doris.Password, cfg.Doris.Host, cfg.Doris.QueryPort, cfg.Doris.Database)
	dorisDB, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Warn("doris unavailable, kline analytics endpoint will report unavailable", "err", err)
		return
	}
	// 只读分析连接池刻意收小，避免分析查询拖垮 all-in-one 单节点
	dorisDB.SetMaxOpenConns(4)
	dorisDB.SetMaxIdleConns(2)
	dorisDB.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dorisDB.PingContext(pingCtx); err != nil {
		log.Warn("doris unavailable, kline analytics endpoint will report unavailable", "err", err)
		_ = dorisDB.Close()
		return
	}
	log.Info("doris data warehouse connected", "host", cfg.Doris.Host, "query_port", cfg.Doris.QueryPort, "db", cfg.Doris.Database)
	a.dorisDB = dorisDB
}

func (a *API) initRouter(conf config.ServerConfig, cfg *config.Config) {

	svc := service.NewHandleSvc(
		a.db,
		a.db.Asset,
		a.db.Symbol,
		a.db.SymbolMarket,
		a.db.Exchange,
		a.db.ExchangeSymbol,
		a.db.SymbolKline,
		a.db.ProviderStatus,
		a.db.MarketAggregation,
		a.redisCli,
		a.dorisDB,
	)
	apiRouter := chi.NewRouter()
	h := routes.NewRoutes(apiRouter, svc)

	apiRouter.Use(middleware.Recoverer)
	apiRouter.Use(middleware.Heartbeat(HealthPath))
	apiRouter.Use(publicProxyHMACMiddleware(cfg.PublicProxyHMACSecret))

	// Unary market-data routes retain their bounded timeout. Trading WebSocket
	// upgrades are mounted outside this group so a long-lived event stream is
	// not terminated by the 12-second market-data middleware.
	apiRouter.Group(func(router chi.Router) {
		router.Use(middleware.Timeout(time.Second * 12))
		router.Post(fmt.Sprintf(SupportAssetPath), h.GetSupportAssets)
		router.Post(fmt.Sprintf(MarketDashboardPath), h.GetMarketDashboard)
		router.Post(fmt.Sprintf(AssetDashboardPath), h.GetAssetDashboard)
		router.Post(fmt.Sprintf(MarketInsightsPath), h.GetMarketInsights)
		router.Post(fmt.Sprintf(ExchangePath), h.GetExchanges)
		router.Post(fmt.Sprintf(SymbolPath), h.GetSymbols)
		router.Post(fmt.Sprintf(KlinePath), h.GetKlines)
		router.Post(fmt.Sprintf(MarketSparklinesPath), h.GetMarketSparklines)
		router.Post(fmt.Sprintf(SystemOverviewPath), h.GetSystemOverview)
		router.Post(fmt.Sprintf(FiatRatePath), h.GetFiatRates)
		router.Post(fmt.Sprintf(TopMoversPath), h.GetTopMovers)
		router.Post(fmt.Sprintf(KlineAnalyticsPath), h.GetKlineAnalytics)
		router.Post(fmt.Sprintf(AssetMomentumPath), h.GetAssetMomentum)
		router.Post(fmt.Sprintf(MarketOverviewPath), h.GetMarketOverview)
		router.Post(fmt.Sprintf(AssetDashboardV2Path), h.GetAssetDashboardV2)
		router.Post(fmt.Sprintf(MarketPriceTicksPath), h.GetMarketPriceTicks)
		router.Post(fmt.Sprintf(AssetMarketsPath), h.GetAssetMarkets)
		router.Post(fmt.Sprintf(AssetVenuesPath), h.GetAssetVenues)
		router.Post(fmt.Sprintf(ProviderCatalogAuditPath), h.GetProviderCatalogAudit)
	})
	mountTradingRoutes(apiRouter, a.tradingHandler)

	a.router = apiRouter
}

func mountTradingRoutes(router chi.Router, handler http.Handler) {
	router.Handle("/api/v1/trading/*", handler)
}

func (a *API) initDB(ctx context.Context, cfg *config.Config) error {
	initDb, err := database.NewDB(ctx, cfg.MasterDB)
	if err != nil {
		log.Error("failed to connect to master database", "err", err)
		return err
	}
	a.db = initDb
	return nil
}

func (a *API) Start(ctx context.Context) error {
	return nil
}

func (a *API) Stop(ctx context.Context) error {
	var result error
	if a.apiSvr != nil {
		if err := a.apiSvr.Stop(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to stop API server: %w", err))
		}
	}
	if a.db != nil {
		if err := a.db.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close DB: %w", err))
		}
	}
	if a.redisCli != nil {
		if err := a.redisCli.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close redis: %w", err))
		}
	}
	if a.dorisDB != nil {
		if err := a.dorisDB.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close doris: %w", err))
		}
	}
	if a.trading != nil {
		if err := a.trading.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("failed to close trading gateway: %w", err))
		}
	}
	a.stopped.Store(true)
	log.Info("API service shutdown complete")
	return result
}

func (a *API) startServer(serverConfig config.ServerConfig) error {
	log.Debug("API server listening...", "port", serverConfig.Port)
	addr := net.JoinHostPort(serverConfig.Host, strconv.Itoa(serverConfig.Port))
	srv, err := httputil.StarHttpServer(addr, a.router)
	if err != nil {
		return fmt.Errorf("failed to start API server: %w", err)
	}
	log.Info("API server started", "addr", srv.Addr().String())
	a.apiSvr = srv
	return nil
}

func (a *API) Stopped() bool {
	return a.stopped.Load()
}
