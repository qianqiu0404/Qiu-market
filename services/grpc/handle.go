package grpc

import (
	"context"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/grpc/proto"
	"github.com/the-web3/s78-market-services/services/http/model"
)

// 本文件是 MarketService 的 RPC 实现。每个方法只做三件事：
//  1. 校验请求参数（非法参数 → codes.InvalidArgument）；
//  2. 把 proto 请求转成 services/http/model 请求，调用与 HTTP API 共用的业务层；
//  3. 把业务层响应转回 proto（业务层错误 → codes.Internal）。
//
// 数值字段（价格 / 成交量 / 市值）直接透传业务层输出的 1e8 还原十进制字符串，
// 与 HTTP API 返回的数据完全一致。

func invalidArg(format string, args ...interface{}) error {
	return status.Errorf(codes.InvalidArgument, format, args...)
}

func internalErr(method string, err error) error {
	log.Error("grpc handler failed", "method", method, "error", err)
	return status.Errorf(codes.Internal, "%s failed: %v", method, err)
}

func (ms *MarketRpcService) GetMarketDashboard(ctx context.Context, in *proto.MarketDashboardRequest) (*proto.MarketDashboardResponse, error) {
	if in.Page < 1 {
		return nil, invalidArg("page must be >= 1, got %d", in.Page)
	}
	if in.PageSize < 1 {
		return nil, invalidArg("page_size must be >= 1, got %d", in.PageSize)
	}

	resp, err := ms.svc.GetMarketDashboard(&model.MarketDashboardRequest{
		ConsumerToken: in.ConsumerToken,
		Page:          in.Page,
		PageSize:      in.PageSize,
		Exchange:      in.Exchange,
		Search:        in.Search,
		MarketID:      in.MarketId,
		SortBy:        in.SortBy,
		SortDirection: in.SortDirection,
	})
	if err != nil {
		return nil, internalErr("GetMarketDashboard", err)
	}

	items := make([]*proto.MarketDashboardItem, 0, len(resp.Result))
	for _, it := range resp.Result {
		items = append(items, &proto.MarketDashboardItem{
			Symbol:            it.Symbol,
			Price:             it.Price,
			Change24H:         it.Change24h,
			Volume:            it.Volume,
			MarketCap:         it.MarketCap,
			Name:              it.Name,
			Logo:              it.Logo,
			Exchange:          it.Exchange,
			UpdatedAt:         it.UpdatedAt,
			DataDelaySeconds:  it.DataDelaySeconds,
			MarketId:          it.MarketID,
			MarketCode:        it.MarketCode,
			MarketType:        it.MarketType,
			HasKline:          it.HasKline,
			ChangeAvailable:   it.ChangeAvailable,
			ChangeSource:      it.ChangeSource,
			BaseAssetId:       it.BaseAssetID,
			BaseAsset:         it.BaseAsset,
			QuoteAssetId:      it.QuoteAssetID,
			QuoteAsset:        it.QuoteAsset,
			ProviderUpdatedAt: it.ProviderUpdatedAt,
			FreshnessStatus:   it.FreshnessStatus,
		})
	}
	return &proto.MarketDashboardResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result:     items,
		Total:      resp.Total,
	}, nil
}

func (ms *MarketRpcService) GetAssetDashboard(ctx context.Context, in *proto.AssetDashboardRequest) (*proto.AssetDashboardResponse, error) {
	if in.Page < 1 {
		return nil, invalidArg("page must be >= 1, got %d", in.Page)
	}
	if in.PageSize < 1 {
		return nil, invalidArg("page_size must be >= 1, got %d", in.PageSize)
	}
	resp, err := ms.svc.GetAssetDashboard(&model.AssetDashboardRequest{
		ConsumerToken: in.ConsumerToken,
		Page:          in.Page,
		PageSize:      in.PageSize,
		Search:        in.Search,
		SortBy:        in.SortBy,
		SortDirection: in.SortDirection,
	})
	if err != nil {
		return nil, internalErr("GetAssetDashboard", err)
	}
	items := make([]*proto.AssetDashboardItem, 0, len(resp.Result))
	for _, asset := range resp.Result {
		markets := make([]*proto.AssetMarketItem, 0, len(asset.Markets))
		for _, market := range asset.Markets {
			markets = append(markets, &proto.AssetMarketItem{
				MarketId:          market.MarketID,
				MarketCode:        market.MarketCode,
				Symbol:            market.Symbol,
				Exchange:          market.Exchange,
				MarketType:        market.MarketType,
				QuoteAssetId:      market.QuoteAssetID,
				QuoteAsset:        market.QuoteAsset,
				Price:             market.Price,
				Change24H:         market.Change24h,
				ChangeAvailable:   market.ChangeAvailable,
				Volume:            market.Volume,
				MarketCap:         market.MarketCap,
				HasKline:          market.HasKline,
				UpdatedAt:         market.UpdatedAt,
				DataDelaySeconds:  market.DataDelaySeconds,
				IsReference:       market.IsReference,
				ProviderUpdatedAt: market.ProviderUpdatedAt,
				FreshnessStatus:   market.FreshnessStatus,
			})
		}
		items = append(items, &proto.AssetDashboardItem{
			AssetId:             asset.AssetID,
			AssetSymbol:         asset.AssetSymbol,
			AssetName:           asset.AssetName,
			Logo:                asset.Logo,
			ReferenceMarketId:   asset.ReferenceMarketID,
			ReferenceMarketCode: asset.ReferenceMarketCode,
			ReferenceExchange:   asset.ReferenceExchange,
			ReferenceMarketType: asset.ReferenceMarketType,
			Price:               asset.Price,
			Change24H:           asset.Change24h,
			ChangeAvailable:     asset.ChangeAvailable,
			MarketCap:           asset.MarketCap,
			Turnover24H:         asset.Turnover24h,
			MarketCount:         asset.MarketCount,
			HasKline:            asset.HasKline,
			UpdatedAt:           asset.UpdatedAt,
			DataDelaySeconds:    asset.DataDelaySeconds,
			Markets:             markets,
			ProviderUpdatedAt:   asset.ProviderUpdatedAt,
			FreshnessStatus:     asset.FreshnessStatus,
		})
	}
	return &proto.AssetDashboardResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result:     items,
		Total:      resp.Total,
	}, nil
}

func (ms *MarketRpcService) GetMarketInsights(ctx context.Context, in *proto.MarketInsightsRequest) (*proto.MarketInsightsResponse, error) {
	resp, err := ms.svc.GetMarketInsights(&model.MarketInsightsRequest{ConsumerToken: in.ConsumerToken})
	if err != nil {
		return nil, internalErr("GetMarketInsights", err)
	}
	distribution := make([]*proto.ChangeDistributionBucket, 0, len(resp.Result.Distribution))
	for _, bucket := range resp.Result.Distribution {
		distribution = append(distribution, &proto.ChangeDistributionBucket{
			Key: bucket.Key, Label: bucket.Label, Min: bucket.Min, Max: bucket.Max, Count: bucket.Count,
		})
	}
	crossVenue := make([]*proto.CrossVenueItem, 0, len(resp.Result.CrossVenue))
	for _, item := range resp.Result.CrossVenue {
		crossVenue = append(crossVenue, &proto.CrossVenueItem{
			AssetId:             item.AssetID,
			AssetSymbol:         item.AssetSymbol,
			AssetName:           item.AssetName,
			SpotMarketId:        item.SpotMarketID,
			SpotMarketCode:      item.SpotMarketCode,
			SpotExchange:        item.SpotExchange,
			SpotQuoteAsset:      item.SpotQuoteAsset,
			SpotPrice:           item.SpotPrice,
			SpotChange24H:       item.SpotChange24h,
			SpotChangeAvailable: item.SpotChangeAvailable,
			SpotTurnover24H:     item.SpotTurnover24h,
			SpotTurnoverShare:   item.SpotTurnoverShare,
			SpotDelaySeconds:    item.SpotDelaySeconds,
			PerpMarketId:        item.PerpMarketID,
			PerpMarketCode:      item.PerpMarketCode,
			PerpExchange:        item.PerpExchange,
			PerpQuoteAsset:      item.PerpQuoteAsset,
			PerpPrice:           item.PerpPrice,
			PerpChange24H:       item.PerpChange24h,
			PerpChangeAvailable: item.PerpChangeAvailable,
			PerpTurnover24H:     item.PerpTurnover24h,
			PerpTurnoverShare:   item.PerpTurnoverShare,
			PerpDelaySeconds:    item.PerpDelaySeconds,
			IndicativeSpreadPct: item.IndicativeSpreadPct,
			SpreadAvailable:     item.SpreadAvailable,
			ChangeGapPctPoints:  item.ChangeGapPctPoints,
			ChangeGapAvailable:  item.ChangeGapAvailable,
		})
	}
	breadth := resp.Result.Breadth
	return &proto.MarketInsightsResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result: &proto.MarketInsightsResult{
			Breadth: &proto.MarketBreadth{
				AssetCount:      breadth.AssetCount,
				Advancers:       breadth.Advancers,
				Decliners:       breadth.Decliners,
				Flat:            breadth.Flat,
				Unknown:         breadth.Unknown,
				AdvanceRatio:    breadth.AdvanceRatio,
				MedianChange24H: breadth.MedianChange24h,
				Turnover24H:     breadth.Turnover24h,
			},
			Distribution: distribution,
			CrossVenue:   crossVenue,
			UpdatedAt:    resp.Result.UpdatedAt,
		},
	}, nil
}

func (ms *MarketRpcService) GetMarketOverview(ctx context.Context, in *proto.MarketOverviewRequest) (*proto.MarketOverviewResponse, error) {
	resp, err := ms.svc.GetMarketOverview(&model.MarketOverviewRequest{
		ConsumerToken: in.ConsumerToken,
		Venue:         in.Venue,
		Universe:      in.Universe,
	})
	if err != nil {
		if errors.Is(err, database.ErrInvalidDashboardVenue) {
			return nil, invalidArg("%v", err)
		}
		return nil, internalErr("GetMarketOverview", err)
	}
	result := resp.Result
	return &proto.MarketOverviewResponse{
		ReturnCode: resp.Code, Message: resp.Message,
		Result: &proto.MarketOverviewResult{
			GlobalMarketCapUsd:       protoAvailableDecimal(result.GlobalMarketCapUSD),
			CoveredSpotVolume_24HUsd: protoAvailableDecimal(result.CoveredSpotVolume),
			BtcDominancePct:          protoAvailableDecimal(result.BTCDominancePct),
			AssetCount:               result.AssetCount, Advancers: result.Advancers,
			Decliners: result.Decliners, Flat: result.Flat, Unknown: result.Unknown,
			AdvanceRatioPct:   protoAvailableDecimal(result.AdvanceRatioPct),
			ProviderUpdatedAt: result.ProviderUpdatedAt, IndexUpdatedAt: result.IndexUpdatedAt,
			Venue: result.Venue, RankedAssetCount: result.RankedAssetCount,
			PricedAssetCount:            result.PricedAssetCount,
			CoverageRatioPct:            protoAvailableDecimal(result.CoverageRatioPct),
			Top50UniverseCount:          result.Top50UniverseCount,
			EligibleAssetCount:          result.EligibleAssetCount,
			PublishedAssetCount:         result.PublishedAssetCount,
			ChangeAvailableCount:        result.ChangeAvailableCount,
			ContributingProviderCount:   result.ContributingProviderCount,
			SingleVenuePricedAssetCount: result.SingleVenuePricedAssetCount,
			MultiVenuePricedAssetCount:  result.MultiVenuePricedAssetCount,
			LocalPreviewEnabled:         result.LocalPreviewEnabled,
			PreviewSourceKey:            result.PreviewSourceKey,
			PreviewCoveredCount:         result.PreviewCoveredCount,
			Universe:                    result.Universe,
			SelectionVersion:            result.SelectionVersion,
		},
	}, nil
}

func (ms *MarketRpcService) GetAssetDashboardV2(ctx context.Context, in *proto.AssetDashboardV2Request) (*proto.AssetDashboardV2Response, error) {
	if in.Page < 1 || in.PageSize < 1 {
		return nil, invalidArg("page and page_size must be >= 1")
	}
	resp, err := ms.svc.GetAssetDashboardV2(&model.AssetDashboardV2Request{
		ConsumerToken: in.ConsumerToken, Page: in.Page, PageSize: in.PageSize,
		Venue: in.Venue, IncludeUncovered: in.IncludeUncovered,
		Universe: in.Universe,
		Search:   in.Search, Filter: in.Filter, SortBy: in.SortBy, SortDirection: in.SortDirection,
	})
	if err != nil {
		if errors.Is(err, database.ErrInvalidDashboardVenue) {
			return nil, invalidArg("%v", err)
		}
		return nil, internalErr("GetAssetDashboardV2", err)
	}
	items := make([]*proto.AssetDashboardV2Item, 0, len(resp.Result))
	for _, item := range resp.Result {
		var rank *int32
		if item.Rank != nil {
			value := int32(*item.Rank)
			rank = &value
		}
		items = append(items, &proto.AssetDashboardV2Item{
			Rank: rank, AssetId: item.AssetID, AssetSymbol: item.AssetSymbol,
			AssetName: item.AssetName, Logo: item.Logo,
			PriceUsd:               protoAvailableDecimal(item.PriceUSD),
			CompositePriceUsd:      protoAvailableDecimal(item.CompositePriceUSD),
			Change_24HPct:          protoAvailableDecimal(item.Change24hPct),
			MarketCapUsd:           protoAvailableDecimal(item.MarketCapUSD),
			CoveredTurnover_24HUsd: protoAvailableDecimal(item.Turnover24hUSD),
			CirculatingSupply:      protoAvailableDecimal(item.CirculatingSupply),
			SpotMarketCount:        item.SpotMarketCount, PerpMarketCount: item.PerpMarketCount,
			DexRouteCount: item.DexRouteCount, PricedVenueCount: int32(item.PricedVenueCount),
			ContributorCount: int32(item.ContributorCount), Confidence: item.Confidence,
			Quality: item.Quality, PriceKind: item.PriceKind, PriceSource: item.PriceSource,
			CoverageStatus: item.CoverageStatus, CoverageReason: item.CoverageReason,
			SelectionVersion: item.SelectionVersion, SelectionRank: int32(item.SelectionRank),
			FreshnessStatus:     item.FreshnessStatus,
			FreshnessAgeSeconds: item.FreshnessAgeSeconds,
			LastAttemptAt:       item.LastAttemptAt, LastSuccessAt: item.LastSuccessAt,
			LastErrorClass: item.LastErrorClass,
			Available:      item.Available,
			SourceTime:     item.SourceTime, ObservedAt: item.ObservedAt,
			IndexUpdatedAt: item.IndexUpdatedAt, ProviderUpdatedAt: item.ProviderUpdatedAt,
			SparklineAvailable: item.SparklineAvailable,
		})
	}
	return &proto.AssetDashboardV2Response{
		ReturnCode: resp.Code, Message: resp.Message, Result: items, Total: resp.Total,
		Universe: resp.Universe,
	}, nil
}

func (ms *MarketRpcService) GetAssetMarkets(ctx context.Context, in *proto.AssetMarketsRequest) (*proto.AssetMarketsResponse, error) {
	if strings.TrimSpace(in.AssetId) == "" {
		return nil, invalidArg("asset_id is required")
	}
	resp, err := ms.svc.GetAssetMarkets(&model.AssetMarketsRequest{
		ConsumerToken: in.ConsumerToken, AssetID: in.AssetId, Venue: in.Venue,
	})
	if err != nil {
		return nil, internalErr("GetAssetMarkets", err)
	}
	return assetVenuesToProto(resp), nil
}

func (ms *MarketRpcService) GetAssetVenues(ctx context.Context, in *proto.AssetMarketsRequest) (*proto.AssetMarketsResponse, error) {
	if strings.TrimSpace(in.AssetId) == "" {
		return nil, invalidArg("asset_id is required")
	}
	resp, err := ms.svc.GetAssetVenues(&model.AssetVenuesRequest{
		ConsumerToken: in.ConsumerToken, AssetID: in.AssetId, Venue: in.Venue,
	})
	if err != nil {
		return nil, internalErr("GetAssetVenues", err)
	}
	return assetVenuesToProto(resp), nil
}

func assetVenuesToProto(resp *model.AssetVenuesResponse) *proto.AssetMarketsResponse {
	items := make([]*proto.AssetMarketV2Item, 0, len(resp.Result))
	for _, item := range resp.Result {
		items = append(items, &proto.AssetMarketV2Item{
			MarketId: item.MarketID, MarketCode: item.MarketCode, Provider: item.Provider,
			Symbol: item.Symbol, MarketType: item.MarketType, QuoteAsset: item.QuoteAsset,
			Price:                protoAvailableDecimal(item.Price),
			RelativeDeviationPct: protoAvailableDecimal(item.RelativeDeviationPct),
			Change_24HPct:        protoAvailableDecimal(item.Change24hPct),
			Turnover_24H:         protoAvailableDecimal(item.Turnover24h),
			FreshnessStatus:      item.FreshnessStatus, ProviderUpdatedAt: item.ProviderUpdatedAt,
			Confidence: item.Confidence, Quality: item.Quality, HasKline: item.HasKline,
			VenueKind: item.VenueKind, Chain: item.Chain, Protocol: item.Protocol,
			RouteKey: item.RouteKey, Route: item.Route, PoolAddresses: item.PoolAddresses,
			QuoteNotionalUsd:   protoAvailableDecimal(item.QuoteNotionalUSD),
			TvlUsd:             protoAvailableDecimal(item.TVLUSD),
			PriceImpactPct:     protoAvailableDecimal(item.PriceImpactPct),
			RoundTripSpreadPct: protoAvailableDecimal(item.RoundTripSpreadPct),
			BlockNumber:        item.BlockNumber, BlockTimestamp: item.BlockTimestamp,
			Available: item.Available, UnavailableReason: item.UnavailableReason,
			QuoteReferenceKind: item.QuoteReferenceKind,
		})
	}
	return &proto.AssetMarketsResponse{
		ReturnCode: resp.Code, Message: resp.Message, Result: items,
	}
}

func (ms *MarketRpcService) GetProviderCatalogAudit(ctx context.Context, in *proto.ProviderCatalogAuditRequest) (*proto.ProviderCatalogAuditResponse, error) {
	if in.Page < 1 || in.PageSize < 1 {
		return nil, invalidArg("page and page_size must be >= 1")
	}
	resp, err := ms.svc.GetProviderCatalogAudit(&model.ProviderCatalogAuditRequest{
		ConsumerToken: in.ConsumerToken, Provider: in.Provider, Status: in.Status,
		Page: in.Page, PageSize: in.PageSize, RankLimit: int(in.RankLimit),
	})
	if err != nil {
		return nil, internalErr("GetProviderCatalogAudit", err)
	}
	items := make([]*proto.ProviderCatalogAuditItem, 0, len(resp.Result))
	for _, item := range resp.Result {
		var rank *int32
		if item.Rank != nil {
			value := int32(*item.Rank)
			rank = &value
		}
		items = append(items, &proto.ProviderCatalogAuditItem{
			Provider: item.Provider, SourceSymbol: item.SourceSymbol, MarketType: item.MarketType,
			BaseAlias: item.BaseAlias, QuoteAlias: item.QuoteAlias,
			UpstreamStatus: item.UpstreamStatus, ResolutionStatus: item.ResolutionStatus,
			BaseAssetId: item.BaseAssetID, QuoteAssetId: item.QuoteAssetID,
			Reason: item.Reason, LastSeenAt: item.LastSeenAt,
			Rank: rank, CandidateKind: item.CandidateKind, AliasReview: item.AliasReview,
			RolloutMode: item.RolloutMode, ResolutionSource: item.ResolutionSource,
		})
	}
	counts := make([]*proto.CatalogAuditCount, 0, len(resp.Counts))
	for _, count := range resp.Counts {
		counts = append(counts, &proto.CatalogAuditCount{Status: count.Status, Count: count.Count})
	}
	return &proto.ProviderCatalogAuditResponse{
		ReturnCode: resp.Code, Message: resp.Message, Result: items, Counts: counts, Total: resp.Total,
	}, nil
}

func protoAvailableDecimal(value model.AvailableDecimal) *proto.AvailableDecimal {
	return &proto.AvailableDecimal{Value: value.Value, Available: value.Available}
}

func (ms *MarketRpcService) GetKlines(ctx context.Context, in *proto.KlinesRequest) (*proto.KlinesResponse, error) {
	if strings.TrimSpace(in.MarketId) == "" && strings.TrimSpace(in.SymbolGuid) == "" {
		return nil, invalidArg("market_id or symbol_guid is required")
	}
	switch in.Interval {
	case "", "1m", "15m", "1h", "1d":
	default:
		return nil, invalidArg("interval must be one of 1m/15m/1h/1d, got %q", in.Interval)
	}
	if in.Limit < 0 {
		return nil, invalidArg("limit must be >= 0, got %d", in.Limit)
	}

	resp, err := ms.svc.GetKlines(&model.KlinesRequest{
		MarketID:   in.MarketId,
		SymbolGuid: in.SymbolGuid,
		Limit:      in.Limit,
		Interval:   in.Interval,
	})
	if err != nil {
		return nil, internalErr("GetKlines", err)
	}

	items := make([]*proto.KlineItem, 0, len(resp.Result))
	for _, k := range resp.Result {
		items = append(items, &proto.KlineItem{
			Timestamp: k.Timestamp,
			Open:      k.Open,
			High:      k.High,
			Low:       k.Low,
			Close:     k.Close,
			Volume:    k.Volume,
		})
	}
	return &proto.KlinesResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result:     items,
	}, nil
}

func (ms *MarketRpcService) GetSystemOverview(ctx context.Context, in *proto.CommonRequest) (*proto.SystemOverviewResponse, error) {
	resp, err := ms.svc.GetSystemOverview(&model.CommonRequest{ConsumerToken: in.ConsumerToken})
	if err != nil {
		return nil, internalErr("GetSystemOverview", err)
	}
	o := resp.Result
	providers := make([]*proto.ProviderStatusItem, 0, len(o.ProviderStatuses))
	for _, provider := range o.ProviderStatuses {
		sources := make([]*proto.ProviderSourceStatusItem, 0, len(provider.Sources))
		for _, source := range provider.Sources {
			sources = append(sources, &proto.ProviderSourceStatusItem{
				SourceKey: source.SourceKey, Status: source.Status,
				LastAttemptAt: source.LastAttemptAt, LastSuccessAt: source.LastSuccessAt,
				LastSourceTime: source.LastSourceTime, NextRetryAt: source.NextRetryAt,
				ConsecutiveFailures: source.ConsecutiveFailures,
				AttemptCount:        source.AttemptCount, SuccessCount: source.SuccessCount,
				SuccessRatePct: source.SuccessRatePct, LastErrorClass: source.LastErrorClass,
				Capability: source.Capability, ReceivedCount: source.ReceivedCount,
				MatchedAssetCount: source.MatchedAssetCount,
				WrittenCount:      source.WrittenCount,
			})
		}
		providers = append(providers, &proto.ProviderStatusItem{
			Provider:                provider.Provider,
			Status:                  provider.Status,
			SourceCount:             provider.SourceCount,
			FailingSourceCount:      provider.FailingSourceCount,
			LastAttemptAt:           provider.LastAttemptAt,
			LastSuccessAt:           provider.LastSuccessAt,
			LastSourceTime:          provider.LastSourceTime,
			ConsecutiveFailures:     provider.ConsecutiveFailures,
			LastErrorClass:          provider.LastErrorClass,
			RolloutMode:             provider.RolloutMode,
			RankLimit:               int32(provider.RankLimit),
			MinSoakUntil:            provider.MinSoakUntil,
			NextRetryAt:             provider.NextRetryAt,
			AttemptCount:            provider.AttemptCount,
			SuccessCount:            provider.SuccessCount,
			SuccessRatePct:          provider.SuccessRatePct,
			PrimarySourceKey:        provider.PrimarySourceKey,
			OperationalStatus:       provider.OperationalStatus,
			ObservationStartedAt:    provider.ObservationStartedAt,
			ReadinessNotBefore:      provider.ReadinessNotBefore,
			RolloutReady:            provider.RolloutReady,
			RolloutBlockers:         provider.RolloutBlockers,
			ReceivedCount:           provider.ReceivedCount,
			MatchedAssetCount:       provider.MatchedAssetCount,
			PriceAvailableCount:     provider.PriceAvailableCount,
			ChangeAvailableCount:    provider.ChangeAvailableCount,
			LocalPreviewEnabled:     provider.LocalPreviewEnabled,
			PreviewSourceKey:        provider.PreviewSourceKey,
			PreviewCoveredCount:     provider.PreviewCoveredCount,
			SelectionVersion:        provider.SelectionVersion,
			SelectionTargetCount:    int32(provider.SelectionTargetCount),
			SelectionCount:          int32(provider.SelectionCount),
			SelectionCandidateCount: int32(provider.SelectionCandidateCount),
			SelectionGeneratedAt:    provider.SelectionGeneratedAt,
			FeedMode:                provider.FeedMode,
			KlineStatus:             provider.KlineStatus,
			KlineMarketCount:        provider.KlineMarketCount,
			KlineCandleCount:        provider.KlineCandleCount,
			KlineLastSuccessAt:      provider.KlineLastSuccessAt,
			Sources:                 sources,
		})
	}
	return &proto.SystemOverviewResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result: &proto.SystemOverview{
			CrawlerStatus:    o.CrawlerStatus,
			DexStatus:        o.DexStatus,
			DwStatus:         o.DwStatus,
			RpcStatus:        o.RpcStatus,
			RedisStatus:      o.RedisStatus,
			DatabaseStatus:   o.DatabaseStatus,
			WorkerStatus:     o.WorkerStatus,
			ApiStatus:        o.ApiStatus,
			MarketCount:      o.MarketCount,
			AssetCount:       o.AssetCount,
			SymbolCount:      o.SymbolCount,
			ExchangeCount:    o.ExchangeCount,
			TotalMarketCap:   o.TotalMarketCap,
			TotalVolume:      o.TotalVolume,
			UpdatedAt:        o.UpdatedAt,
			DataDelaySeconds: o.DataDelaySeconds,
			ProviderStatuses: providers,
		},
	}, nil
}

func (ms *MarketRpcService) GetSupportAssets(ctx context.Context, in *proto.SupportAssetRequest) (*proto.SupportAssetResponse, error) {
	resp, err := ms.svc.GetSupportAssets(&model.SupportAssetRequest{ConsumerToken: in.ConsumerToken})
	if err != nil {
		return nil, internalErr("GetSupportAssets", err)
	}
	assets := make([]*proto.AssetItem, 0, len(resp.Result))
	for _, a := range resp.Result {
		assets = append(assets, &proto.AssetItem{
			Guid:        a.Guid,
			AssetName:   a.AssetName,
			AssetSymbol: a.AssetSymbol,
			AssetLogo:   a.AssetLogo,
		})
	}
	return &proto.SupportAssetResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Asset:      assets,
	}, nil
}

func (ms *MarketRpcService) GetSymbols(ctx context.Context, in *proto.CommonRequest) (*proto.SymbolResponse, error) {
	resp, err := ms.svc.GetSymbols(&model.CommonRequest{ConsumerToken: in.ConsumerToken})
	if err != nil {
		return nil, internalErr("GetSymbols", err)
	}
	symbols := make([]*proto.Symbol, 0, len(resp.Result))
	for _, s := range resp.Result {
		symbols = append(symbols, &proto.Symbol{
			Guid:         s.Guid,
			BaseAsset:    s.BaseAsset,
			QuoteAsset:   s.QuoteAsset,
			SymbolName:   s.SymbolName,
			BaseAssetId:  s.BaseAssetId,
			QuoteAssetId: s.QuoteAssetId,
			Exchange:     s.Exchange,
			MarketType:   s.MarketType,
		})
	}
	return &proto.SymbolResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result:     symbols,
		Total:      resp.Total,
	}, nil
}

func (ms *MarketRpcService) GetExchanges(ctx context.Context, in *proto.CommonRequest) (*proto.ExchangeResponse, error) {
	resp, err := ms.svc.GetExchanges(&model.CommonRequest{ConsumerToken: in.ConsumerToken})
	if err != nil {
		return nil, internalErr("GetExchanges", err)
	}
	exchanges := make([]*proto.Exchange, 0, len(resp.Result))
	for _, e := range resp.Result {
		exchanges = append(exchanges, &proto.Exchange{
			Guid: e.Guid,
			Name: e.Name,
			Logo: e.Logo,
		})
	}
	return &proto.ExchangeResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result:     exchanges,
		Total:      resp.Total,
	}, nil
}

func (ms *MarketRpcService) GetFiatRates(ctx context.Context, in *proto.FiatRatesRequest) (*proto.FiatRatesResponse, error) {
	resp, err := ms.svc.GetFiatRates(&model.FiatRatesRequest{ConsumerToken: in.ConsumerToken})
	if err != nil {
		return nil, internalErr("GetFiatRates", err)
	}
	return &proto.FiatRatesResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Base:       resp.Result.Base,
		Rates:      resp.Result.Rates,
		Source:     resp.Result.Source,
	}, nil
}

func (ms *MarketRpcService) GetTopMovers(ctx context.Context, in *proto.TopMoversRequest) (*proto.TopMoversResponse, error) {
	direction := strings.ToLower(strings.TrimSpace(in.Direction))
	switch direction {
	case "", "gainers", "losers":
	default:
		return nil, invalidArg("direction must be gainers or losers, got %q", in.Direction)
	}
	if in.Limit < 0 {
		return nil, invalidArg("limit must be >= 0, got %d", in.Limit)
	}

	resp, err := ms.svc.GetTopMovers(&model.TopMoversRequest{
		ConsumerToken: in.ConsumerToken,
		Direction:     direction,
		Limit:         in.Limit,
	})
	if err != nil {
		return nil, internalErr("GetTopMovers", err)
	}

	items := make([]*proto.TopMoverItem, 0, len(resp.Result))
	for _, it := range resp.Result {
		items = append(items, &proto.TopMoverItem{
			Rank:             it.Rank,
			Symbol:           it.Symbol,
			Price:            it.Price,
			Change24H:        it.Change24h,
			Volume:           it.Volume,
			MarketCap:        it.MarketCap,
			Name:             it.Name,
			Logo:             it.Logo,
			UpdatedAt:        it.UpdatedAt,
			DataDelaySeconds: it.DataDelaySeconds,
			MarketId:         it.MarketID,
			MarketCode:       it.MarketCode,
			Exchange:         it.Exchange,
			MarketType:       it.MarketType,
			ChangeAvailable:  it.ChangeAvailable,
		})
	}
	return &proto.TopMoversResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result:     items,
		Total:      resp.Total,
		Direction:  resp.Direction,
		Source:     resp.Source,
	}, nil
}

func (ms *MarketRpcService) GetMarketSparklines(ctx context.Context, in *proto.MarketSparklinesRequest) (*proto.MarketSparklinesResponse, error) {
	if len(in.MarketIds) > 100 {
		return nil, invalidArg("market_ids supports at most 100 entries")
	}
	switch in.Interval {
	case "", "1m", "15m", "1h", "1d":
	default:
		return nil, invalidArg("interval must be one of 1m/15m/1h/1d, got %q", in.Interval)
	}
	resp, err := ms.svc.GetMarketSparklines(&model.MarketSparklinesRequest{
		MarketIDs: in.MarketIds,
		Interval:  in.Interval,
		Limit:     in.Limit,
	})
	if err != nil {
		return nil, internalErr("GetMarketSparklines", err)
	}
	result := make([]*proto.MarketSparkline, 0, len(resp.Result))
	for _, line := range resp.Result {
		points := make([]*proto.SparklinePoint, 0, len(line.Points))
		for _, point := range line.Points {
			points = append(points, &proto.SparklinePoint{
				Timestamp: point.Timestamp,
				Close:     point.Close,
			})
		}
		result = append(result, &proto.MarketSparkline{
			MarketId: line.MarketID,
			Points:   points,
		})
	}
	return &proto.MarketSparklinesResponse{
		ReturnCode: resp.Code,
		Message:    resp.Message,
		Result:     result,
	}, nil
}
