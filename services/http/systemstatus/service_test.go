package systemstatus

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/the-web3/s78-market-services/services/http/model"
)

type fakeMarketSource struct {
	overview    *model.SystemOverviewResponse
	overviewErr error
	markets     map[string]*model.MarketOverviewResponse
	marketErrs  map[string]error
}

func (f fakeMarketSource) GetSystemOverview(
	*model.CommonRequest,
) (*model.SystemOverviewResponse, error) {
	return f.overview, f.overviewErr
}

func (f fakeMarketSource) GetMarketOverview(
	request *model.MarketOverviewRequest,
) (*model.MarketOverviewResponse, error) {
	if err := f.marketErrs[request.Venue]; err != nil {
		return nil, err
	}
	return f.markets[request.Venue], nil
}

type fakeTradingProbe struct {
	result TradingProbeResult
}

func (f fakeTradingProbe) Probe(context.Context) TradingProbeResult {
	return f.result
}

func TestSnapshotRequiresExplicitHealthyEvidence(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	service := healthyService(now)

	snapshot := service.Snapshot(context.Background())

	if snapshot.Overall.State != StateLive {
		t.Fatalf("overall = %+v", snapshot.Overall)
	}
	if snapshot.Components.Matching.State != StateLive ||
		snapshot.Components.Liquidity.State != StateLive ||
		snapshot.Components.Transport.State != StateLive ||
		snapshot.Components.MarketData.State != StateLive ||
		snapshot.Components.Outbox.State != StateLive ||
		snapshot.Components.Database.State != StateLive ||
		snapshot.Components.Disk.State != StateLive ||
		snapshot.Components.Retention.State != StateLive {
		t.Fatalf("components = %+v", snapshot.Components)
	}
	if !snapshot.Storage.DatabaseBytes.Available ||
		snapshot.Storage.DatabaseBytes.Value == nil ||
		*snapshot.Storage.DatabaseBytes.Value != 8<<30 {
		t.Fatalf("database bytes = %+v", snapshot.Storage.DatabaseBytes)
	}
	deleted := snapshot.Storage.RetentionDeleted["1m"]
	if !deleted.Available || deleted.Value == nil || *deleted.Value != 0 {
		t.Fatalf("explicit zero deleted rows lost availability: %+v", deleted)
	}
	if len(snapshot.PriceSources) != 2 ||
		snapshot.PriceSources[0].Key != "route_price" ||
		snapshot.PriceSources[1].Key != "reference_display_price" {
		t.Fatalf("price sources = %+v", snapshot.PriceSources)
	}
}

func TestSnapshotClassifiesCachedAndStaleMarketData(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		age         time.Duration
		wantMarket  State
		wantOverall State
	}{
		{name: "cached", age: 90 * time.Second, wantMarket: StateCached, wantOverall: StateCached},
		{name: "stale", age: 6 * time.Minute, wantMarket: StateDegraded, wantOverall: StateDegraded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := healthyService(now)
			source := service.market.(fakeMarketSource)
			source.markets["all"].Result.IndexUpdatedAt = now.Add(-test.age).UnixMilli()
			service.market = source

			snapshot := service.Snapshot(context.Background())
			if snapshot.Components.MarketData.State != test.wantMarket {
				t.Fatalf("market data = %+v", snapshot.Components.MarketData)
			}
			if snapshot.Overall.State != test.wantOverall {
				t.Fatalf("overall = %+v", snapshot.Overall)
			}
		})
	}
}

func TestSnapshotDegradesDatabaseDiskAndRetentionFailures(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		mutate    func(*model.SystemOverview)
		component func(Components) Evidence
		want      State
	}{
		{
			name: "database unavailable",
			mutate: func(overview *model.SystemOverview) {
				overview.DatabaseStatus = "Disconnected"
			},
			component: func(components Components) Evidence { return components.Database },
			want:      StateOffline,
		},
		{
			name: "disk warning",
			mutate: func(overview *model.SystemOverview) {
				overview.Storage.DiskState = "warning"
				overview.Storage.DiskFreeBytes = 20 << 30
			},
			component: func(components Components) Evidence { return components.Disk },
			want:      StateDegraded,
		},
		{
			name: "disk critical",
			mutate: func(overview *model.SystemOverview) {
				overview.Storage.DiskState = "critical"
				overview.Storage.DiskFreeBytes = 10 << 30
			},
			component: func(components Components) Evidence { return components.Disk },
			want:      StateDegraded,
		},
		{
			name: "retention failed",
			mutate: func(overview *model.SystemOverview) {
				overview.Storage.RetentionLastError = "delete batch failed"
			},
			component: func(components Components) Evidence { return components.Retention },
			want:      StateDegraded,
		},
		{
			name: "retention never succeeded",
			mutate: func(overview *model.SystemOverview) {
				overview.Storage.RetentionLastSuccessAt = 0
			},
			component: func(components Components) Evidence { return components.Retention },
			want:      StateUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := healthyService(now)
			source := service.market.(fakeMarketSource)
			test.mutate(&source.overview.Result)
			service.market = source

			snapshot := service.Snapshot(context.Background())
			if got := test.component(snapshot.Components); got.State != test.want {
				t.Fatalf("component = %+v", got)
			}
			if snapshot.Overall.State != StateDegraded {
				t.Fatalf("overall = %+v", snapshot.Overall)
			}
		})
	}
}

func TestSnapshotPartialTradingFailureAndLegacyFieldsStayNonHealthy(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	service := healthyService(now)
	service.trading = fakeTradingProbe{result: TradingProbeResult{
		ProbedAt: now,
		Status: &TradingStatus{
			State: stringPointer("ready"),
		},
		OrderBookError: errors.New("order book unavailable"),
	}}
	source := service.market.(fakeMarketSource)
	source.overview.Result.Storage = model.StorageStatus{}
	service.market = source

	snapshot := service.Snapshot(context.Background())

	if snapshot.Components.Transport.State != StateDegraded {
		t.Fatalf("transport = %+v", snapshot.Components.Transport)
	}
	if snapshot.Components.Liquidity.State != StateOffline {
		t.Fatalf("liquidity = %+v", snapshot.Components.Liquidity)
	}
	if snapshot.Components.Outbox.State != StateUnknown {
		t.Fatalf("missing legacy outbox fields became healthy: %+v", snapshot.Components.Outbox)
	}
	if snapshot.Components.Disk.State != StateUnknown ||
		snapshot.Components.Retention.State != StateUnknown {
		t.Fatalf("missing storage became healthy: %+v", snapshot.Components)
	}
	if snapshot.Storage.DatabaseBytes.Available ||
		snapshot.Storage.RetentionDeleted["1m"].Available {
		t.Fatalf("missing legacy metrics became zero values: %+v", snapshot.Storage)
	}
	if snapshot.Overall.State != StateDegraded {
		t.Fatalf("overall = %+v", snapshot.Overall)
	}
}

func TestSnapshotReportsOfflineOnlyWhenBothDomainsAreUnreachable(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	service := NewService(
		fakeMarketSource{
			overviewErr: errors.New("overview unavailable"),
			marketErrs: map[string]error{
				"all":         errors.New("market unavailable"),
				"uniswap":     errors.New("market unavailable"),
				"pancakeswap": errors.New("market unavailable"),
			},
		},
		fakeTradingProbe{result: TradingProbeResult{
			ProbedAt:       now,
			StatusError:    errors.New("status unavailable"),
			OrderBookError: errors.New("order book unavailable"),
		}},
	)
	service.now = func() time.Time { return now }

	snapshot := service.Snapshot(context.Background())

	if snapshot.Overall.State != StateOffline {
		t.Fatalf("overall = %+v", snapshot.Overall)
	}
	if snapshot.Components.MarketData.State != StateDegraded ||
		snapshot.Components.Transport.State != StateOffline {
		t.Fatalf("components = %+v", snapshot.Components)
	}
}

func TestHandlerReturnsPartialSnapshotWithNoStore(t *testing.T) {
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	service := healthyService(now)
	handler := NewHandler(service)

	request := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
	}
	if !strings.Contains(response.Body.String(), `"schema_version":"system-status.v1"`) ||
		!strings.Contains(response.Body.String(), `"state":"live"`) {
		t.Fatalf("body = %s", response.Body.String())
	}

	getRequest := httptest.NewRequest(http.MethodGet, Path, nil)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", getResponse.Code)
	}
}

func TestHandlerTradingProbeReadsPublicStatusAndOrderBook(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/trading/markets/BTC-USDT/status":
			_, _ = writer.Write([]byte(`{"state":"ready","outbox_state":"ready"}`))
		case "/api/v1/trading/markets/BTC-USDT/orderbook":
			_, _ = writer.Write([]byte(`{"bids":[{"price":"1"}],"asks":[{"price":"2"}]}`))
		default:
			http.NotFound(writer, request)
		}
	})
	probe := NewHandlerTradingProbe(handler)
	now := time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC)
	probe.now = func() time.Time { return now }

	result := probe.Probe(context.Background())

	if result.StatusError != nil || result.OrderBookError != nil {
		t.Fatalf("probe errors = %v / %v", result.StatusError, result.OrderBookError)
	}
	if result.Status == nil || result.Status.State == nil ||
		*result.Status.State != "ready" {
		t.Fatalf("status = %+v", result.Status)
	}
	if result.OrderBook == nil || result.OrderBook.Bids == nil ||
		len(*result.OrderBook.Bids) != 1 ||
		result.OrderBook.Asks == nil ||
		len(*result.OrderBook.Asks) != 1 {
		t.Fatalf("order book = %+v", result.OrderBook)
	}
}

func healthyService(now time.Time) *Service {
	retentionSuccess := now.Add(-time.Hour)
	oldest := now.Add(-7 * 24 * time.Hour)
	newest := now.Add(-time.Minute)
	overview := &model.SystemOverviewResponse{
		Code: 2000,
		Result: model.SystemOverview{
			CrawlerStatus:  "Running",
			DexStatus:      "Running",
			DwStatus:       "Running",
			RpcStatus:      "Running",
			RedisStatus:    "Connected",
			DatabaseStatus: "Connected",
			WorkerStatus:   "Running",
			ApiStatus:      "Healthy",
			Storage: model.StorageStatus{
				DatabaseBytes:          8 << 30,
				KlineTableBytes:        4 << 30,
				KlineHeapBytes:         3 << 30,
				KlineIndexBytes:        1 << 30,
				KlineEstimatedRows:     1000,
				DiskFreeBytes:          40 << 30,
				DiskState:              "healthy",
				RetentionLastStartedAt: retentionSuccess.Add(-time.Minute).UnixMilli(),
				RetentionLastSuccessAt: retentionSuccess.UnixMilli(),
				RetentionDeletedRows: map[string]int64{
					"1m": 0, "15m": 2, "1h": 3,
				},
				KlineIntervals: []model.KlineIntervalStorage{
					{Interval: "1m", OldestAt: oldest.UnixMilli(), NewestAt: newest.UnixMilli()},
					{Interval: "15m", OldestAt: oldest.UnixMilli(), NewestAt: newest.UnixMilli()},
					{Interval: "1h", OldestAt: oldest.UnixMilli(), NewestAt: newest.UnixMilli()},
					{Interval: "1d", OldestAt: oldest.UnixMilli(), NewestAt: newest.UnixMilli()},
				},
			},
		},
	}
	markets := map[string]*model.MarketOverviewResponse{
		"all": {
			Code: 2000,
			Result: model.MarketOverviewResult{
				Venue: "all", PricedAssetCount: 4,
				IndexUpdatedAt: now.Add(-5 * time.Second).UnixMilli(),
			},
		},
		"uniswap": {
			Code: 2000,
			Result: model.MarketOverviewResult{
				Venue: "uniswap", RoutableAssetCount: 2,
				IndexUpdatedAt: now.Add(-10 * time.Second).UnixMilli(),
			},
		},
		"pancakeswap": {
			Code: 2000,
			Result: model.MarketOverviewResult{
				Venue: "pancakeswap", RoutableAssetCount: 1,
				IndexUpdatedAt: now.Add(-10 * time.Second).UnixMilli(),
			},
		},
	}
	bids := []PriceLevel{{Price: "64990", Quantity: "0.5", OrderCount: 1}}
	asks := []PriceLevel{{Price: "65010", Quantity: "0.5", OrderCount: 1}}
	published := now.Add(-2 * time.Second).Format(time.RFC3339Nano)
	service := NewService(
		fakeMarketSource{overview: overview, markets: markets},
		fakeTradingProbe{result: TradingProbeResult{
			ProbedAt: now,
			Status: &TradingStatus{
				State:                 stringPointer("ready"),
				OutboxState:           stringPointer("ready"),
				OutboxCheckpoint:      stringPointer("10"),
				OutboxLastPublishedAt: &published,
			},
			OrderBook: &OrderBook{Bids: &bids, Asks: &asks},
		}},
	)
	service.now = func() time.Time { return now }
	return service
}

func stringPointer(value string) *string {
	return &value
}
