package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h HandleSvc) GetMarketOverview(request *model.MarketOverviewRequest) (*model.MarketOverviewResponse, error) {
	venue, _, err := database.NormalizeDashboardVenue(request.Venue)
	if err != nil {
		return nil, err
	}
	snapshot, err := h.marketSnapshots.resolve(venue, request.SnapshotID)
	if err != nil {
		return nil, err
	}
	summary := &snapshot.Read.Summary
	global := snapshot.Read.Global
	result := model.MarketOverviewResult{
		AssetCount: summary.AssetCount, Advancers: summary.Advancers,
		Decliners: summary.Decliners, Flat: summary.Flat, Unknown: summary.Unknown,
		CoveredSpotVolume: availableDecimal(summary.CoveredVolume),
		Venue:             venue, RankedAssetCount: summary.AssetCount,
		Top50UniverseCount:          summary.Top50UniverseCount,
		EligibleAssetCount:          summary.EligibleAssetCount,
		PublishedAssetCount:         summary.PublishedAssetCount,
		PricedAssetCount:            summary.PricedAssetCount,
		DisplayedAssetCount:         summary.DisplayedAssetCount,
		RoutableAssetCount:          summary.RoutableAssetCount,
		ReferenceOnlyAssetCount:     summary.ReferenceOnlyAssetCount,
		UnpricedAssetCount:          summary.UnpricedAssetCount,
		FreshAssetCount:             snapshot.Read.FreshAssetCount,
		StaleAssetCount:             snapshot.Read.StaleAssetCount,
		UnavailableAssetCount:       snapshot.Read.UnavailableAssetCount,
		ChangeAvailableCount:        summary.ChangeAvailableCount,
		ContributingProviderCount:   summary.ContributingProviderCount,
		SingleVenuePricedAssetCount: summary.SingleVenuePricedAssetCount,
		MultiVenuePricedAssetCount:  summary.MultiVenuePricedAssetCount,
		LocalPreviewEnabled:         summary.LocalPreviewEnabled,
		PreviewSourceKey:            summary.PreviewSourceKey,
		PreviewCoveredCount:         summary.PreviewCoveredCount,
		Universe:                    dashboardUniverse(venue),
	}
	if venue != "all" {
		if selection, selectionErr := h.marketAggregationView.QueryProviderAssetSelectionState(venue); selectionErr == nil && selection != nil {
			result.SelectionVersion = selection.ActiveVersion
		}
	}
	if summary.AssetCount > 0 {
		value := decimal.NewFromInt(summary.PricedAssetCount).
			Div(decimal.NewFromInt(summary.AssetCount)).
			Mul(decimal.NewFromInt(100)).String()
		result.CoverageRatioPct = model.AvailableDecimal{Value: &value, Available: true}
		displayValue := decimal.NewFromInt(summary.DisplayedAssetCount).
			Div(decimal.NewFromInt(summary.AssetCount)).
			Mul(decimal.NewFromInt(100)).String()
		result.DisplayCoverageRatioPct = model.AvailableDecimal{Value: &displayValue, Available: true}
	}
	known := summary.Advancers + summary.Decliners + summary.Flat
	if known > 0 {
		value := decimal.NewFromInt(summary.Advancers).
			Div(decimal.NewFromInt(known)).
			Mul(decimal.NewFromInt(100)).String()
		result.AdvanceRatioPct = model.AvailableDecimal{Value: &value, Available: true}
	}
	if summary.ObservedAt != nil {
		result.IndexUpdatedAt = summary.ObservedAt.UnixMilli()
	}
	if global != nil {
		result.GlobalMarketCapUSD = availableDecimal(global.TotalMarketCapUSD)
		result.BTCDominancePct = availableDecimal(global.BTCDominancePct)
		if global.ProviderUpdatedAt != nil {
			result.ProviderUpdatedAt = global.ProviderUpdatedAt.UnixMilli()
		} else {
			result.ProviderUpdatedAt = global.ObservedAt.UnixMilli()
		}
	}
	return &model.MarketOverviewResponse{
		Code: 2000, Message: "get market overview success", Result: result,
		SnapshotID: snapshot.ID, SnapshotAsOf: snapshot.Read.AsOf.UnixMilli(),
		SnapshotSchema: MarketSnapshotSchema,
	}, nil
}

func (h HandleSvc) GetAssetDashboardV2(request *model.AssetDashboardV2Request) (*model.AssetDashboardV2Response, error) {
	venue, _, err := database.NormalizeDashboardVenue(request.Venue)
	if err != nil {
		return nil, err
	}
	expectedUniverse := dashboardUniverse(venue)
	if requestedUniverse := strings.TrimSpace(request.Universe); requestedUniverse != "" &&
		requestedUniverse != expectedUniverse {
		return nil, fmt.Errorf(
			"universe %s is incompatible with venue %s; expected %s",
			requestedUniverse, venue, expectedUniverse,
		)
	}
	includeUncovered := true
	if request.IncludeUncovered != nil {
		includeUncovered = *request.IncludeUncovered
	}
	snapshot, err := h.marketSnapshots.resolve(venue, request.SnapshotID)
	if err != nil {
		return nil, err
	}
	rows, total := snapshotDashboardPage(snapshot, database.AssetIndexDashboardQuery{
		Page: request.Page, PageSize: request.PageSize, Venue: venue,
		Universe:         request.Universe,
		IncludeUncovered: includeUncovered, Search: request.Search,
		Filter: request.Filter, SortBy: request.SortBy, SortDirection: request.SortDirection,
	})
	now := snapshot.Read.AsOf
	result := make([]model.AssetDashboardV2Item, 0, len(rows))
	for _, row := range rows {
		venuePrice, dexRoutePrice, displayPrice := dashboardPriceFacts(row, venue, now)
		item := model.AssetDashboardV2Item{
			Rank: row.Rank, AssetID: row.AssetID, AssetSymbol: row.AssetSymbol,
			SelectionVersion: row.SelectionVersion, SelectionRank: row.SelectionRank,
			AssetName: row.AssetName, Logo: row.Logo,
			PriceUSD:                availableDecimal(row.Price),
			CompositePriceUSD:       availableDecimal(row.CompositePrice),
			MarketReferencePriceUSD: availableDecimal(row.MarketReferencePrice),
			DisplayPriceUSD:         availableDecimal(row.DisplayPrice),
			DisplayPriceKind:        row.DisplayPriceKind,
			DisplayChange24hPct:     availableDecimal(row.DisplayChange24hPct),
			DisplayChangeKind:       row.DisplayChangeKind,
			DisplayAvailable:        row.DisplayAvailable,
			DexRouteAvailable:       row.DexRouteAvailable,
			VenuePrice:              venuePrice,
			DexRoutePrice:           dexRoutePrice,
			DisplayPrice:            displayPrice,
			Change24hPct:            availableDecimal(row.Change24hPct),
			MarketCapUSD:            availableDecimal(row.MarketCapUSD),
			Turnover24hUSD:          availableDecimal(row.Turnover24hUSD),
			CirculatingSupply:       availableDecimal(row.CirculatingSupply),
			SpotMarketCount:         row.SpotMarketCount, PerpMarketCount: row.PerpMarketCount,
			DexRouteCount: row.DexRouteCount, ContributorCount: row.ContributorCount,
			PricedVenueCount: row.PricedVenueCount, Confidence: row.Confidence,
			Quality: row.Quality, PriceKind: row.PriceKind, PriceSource: row.PriceSource,
			CoverageStatus: row.CoverageStatus, CoverageReason: row.CoverageReason,
			FreshnessStatus:    row.FreshnessStatus,
			Available:          row.Available,
			SparklineAvailable: false,
		}
		if row.SourceTime != nil {
			item.SourceTime = row.SourceTime.UnixMilli()
		}
		if row.ObservedAt != nil {
			item.ObservedAt = row.ObservedAt.UnixMilli()
			item.IndexUpdatedAt = row.ObservedAt.UnixMilli()
		}
		if row.DisplayObservedAt != nil {
			item.DisplayObservedAt = row.DisplayObservedAt.UnixMilli()
		}
		if row.ProviderUpdatedAt != nil {
			item.ProviderUpdatedAt = row.ProviderUpdatedAt.UnixMilli()
		}
		if row.FreshnessAgeSeconds != nil {
			item.FreshnessAgeSeconds = *row.FreshnessAgeSeconds
		}
		if row.LastAttemptAt != nil {
			item.LastAttemptAt = row.LastAttemptAt.UnixMilli()
		}
		if row.LastSuccessAt != nil {
			item.LastSuccessAt = row.LastSuccessAt.UnixMilli()
		}
		if row.LastErrorClass != nil {
			item.LastErrorClass = *row.LastErrorClass
		}
		result = append(result, item)
	}
	return &model.AssetDashboardV2Response{
		Code: 2000, Message: "get asset dashboard v2 success", Result: result,
		Total: total, Universe: expectedUniverse,
		SnapshotID: snapshot.ID, SnapshotAsOf: snapshot.Read.AsOf.UnixMilli(),
		SnapshotSchema: MarketSnapshotSchema,
	}, nil
}

func (h HandleSvc) GetMarketPriceTicks(request *model.MarketPriceTicksRequest) (*model.MarketPriceTicksResponse, error) {
	venue, _, err := database.NormalizeDashboardVenue(request.Venue)
	if err != nil {
		return nil, err
	}
	if len(request.AssetIDs) > 100 {
		return nil, fmt.Errorf("asset_ids cannot contain more than 100 items")
	}
	rows, err := h.marketAggregationView.QueryMarketPriceTicks(database.MarketPriceTickQuery{
		Venue: venue, AssetIDs: request.AssetIDs,
	})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result := make([]model.MarketPriceTickItem, 0, len(rows))
	for _, row := range rows {
		priceFact := tickPriceFact(row, venue, now)
		item := model.MarketPriceTickItem{
			AssetID: row.AssetID, Provider: row.Provider, PriceKind: row.PriceKind,
			PriceUSD:       availableDecimal(row.PriceUSD),
			Change24hPct:   availableDecimal(row.Change24hPct),
			Turnover24hUSD: availableDecimal(row.Turnover24hUSD),
			Available:      row.Available,
			Version:        row.Version,
		}
		switch {
		case venue == "all":
			item.DisplayPrice = priceFact
		case venue == "uniswap" || venue == "pancakeswap":
			item.DexRoutePrice = priceFact
		default:
			item.VenuePrice = priceFact
		}
		if row.SourceTime != nil {
			item.SourceTime = row.SourceTime.UnixMilli()
		}
		if row.ObservedAt != nil {
			item.ObservedAt = row.ObservedAt.UnixMilli()
		}
		if row.LastSuccessAt != nil {
			item.LastSuccessAt = row.LastSuccessAt.UnixMilli()
			age := now.Sub(row.LastSuccessAt.UTC())
			if age < 0 {
				age = 0
			}
			item.FreshnessAgeSeconds = int64(age / time.Second)
		}
		switch {
		case !row.Available:
			item.FreshnessStatus = "unavailable"
		case item.FreshnessAgeSeconds <= 30:
			item.FreshnessStatus = "fresh"
		case item.FreshnessAgeSeconds <= 300:
			item.FreshnessStatus = "stale"
		default:
			item.FreshnessStatus = "unavailable"
			item.Available = false
		}
		if !item.Available {
			item.PriceUSD = model.AvailableDecimal{}
			item.Change24hPct = model.AvailableDecimal{}
			item.Turnover24hUSD = model.AvailableDecimal{}
		}
		result = append(result, item)
	}
	return &model.MarketPriceTicksResponse{
		Code: 2000, Message: "get market price ticks success", Result: result,
		Venue: venue, ServerTime: now.UnixMilli(),
	}, nil
}

func dashboardUniverse(venue string) string {
	switch strings.ToLower(strings.TrimSpace(venue)) {
	case "all":
		return "provider_union"
	case "binance", "coinbase", "bybit", "okx",
		"hyperliquid", "uniswap", "pancakeswap":
		return "provider_top50"
	default:
		return "provider_top50"
	}
}

func dashboardPriceFacts(
	row database.AssetIndexDashboardRow,
	venue string,
	now time.Time,
) (model.MarketPriceFact, model.MarketPriceFact, model.MarketPriceFact) {
	venuePrice := unavailablePriceFact()
	dexRoutePrice := unavailablePriceFact()
	displayPrice := unavailablePriceFact()

	switch venue {
	case "binance", "coinbase", "bybit", "okx", "hyperliquid":
		venuePrice = newMarketPriceFact(
			row.Price,
			row.Change24hPct,
			row.Turnover24hUSD,
			row.PriceKind,
			venue,
			row.SourceTime,
			row.ObservedAt,
			row.LastSuccessAt,
			now,
			30*time.Second,
			5*time.Minute,
			firstNonEmpty(row.Quality, row.Confidence),
			row.ContributorCount,
			[]string{venue},
			row.VenuePriceVersion,
		)
	case "uniswap", "pancakeswap":
		if row.DexRouteAvailable {
			dexRoutePrice = newMarketPriceFact(
				row.Price,
				row.Change24hPct,
				row.Turnover24hUSD,
				"dex_route",
				venue,
				row.SourceTime,
				row.ObservedAt,
				row.LastSuccessAt,
				now,
				30*time.Second,
				time.Minute,
				firstNonEmpty(row.Quality, row.Confidence),
				row.ContributorCount,
				[]string{venue},
				row.VenuePriceVersion,
			)
		}
	}

	if row.CompositePrice != nil && row.CompositeObservedAt != nil {
		displayPrice = newMarketPriceFact(
			row.CompositePrice,
			row.CompositeChange24h,
			row.CompositeTurnover24h,
			"composite_reference",
			"cex_composite",
			nil,
			row.CompositeObservedAt,
			row.CompositeObservedAt,
			now,
			30*time.Second,
			30*time.Second,
			row.CompositeConfidence,
			row.CompositeCount,
			compositeContributorProviders(row.CompositeContributors),
			row.CompositeVersion,
		)
	} else if (venue == "uniswap" || venue == "pancakeswap") &&
		row.MarketReferencePrice != nil && row.ReferenceObservedAt != nil {
		displayPrice = newMarketPriceFact(
			row.MarketReferencePrice,
			nil,
			nil,
			"market_reference",
			"coingecko",
			row.ReferenceSourceTime,
			row.ReferenceObservedAt,
			row.ReferenceObservedAt,
			now,
			5*time.Minute,
			15*time.Minute,
			"reference",
			1,
			[]string{"coingecko"},
			0,
		)
	}

	return venuePrice, dexRoutePrice, displayPrice
}

func tickPriceFact(
	row database.MarketPriceTickRow,
	venue string,
	now time.Time,
) model.MarketPriceFact {
	if !row.Available {
		return unavailablePriceFact()
	}
	kind := row.PriceKind
	source := row.Provider
	freshFor := 30 * time.Second
	contributors := []string{row.Provider}
	sourceTime := row.SourceTime
	if venue == "all" {
		kind = "composite_reference"
		source = "cex_composite"
		sourceTime = nil
		contributors = compositeContributorProviders(row.Contributors)
	} else if venue == "uniswap" || venue == "pancakeswap" {
		freshFor = time.Minute
	}
	return newMarketPriceFact(
		row.PriceUSD,
		row.Change24hPct,
		row.Turnover24hUSD,
		kind,
		source,
		sourceTime,
		row.ObservedAt,
		row.LastSuccessAt,
		now,
		freshFor,
		freshFor,
		firstNonEmpty(row.Quality, row.Confidence),
		row.ContributorCount,
		contributors,
		row.Version,
	)
}

func newMarketPriceFact(
	price, change, turnover *string,
	kind, source string,
	sourceTime, observedAt, lastSuccessAt *time.Time,
	now time.Time,
	freshFor, readableFor time.Duration,
	quality string,
	contributorCount int,
	contributors []string,
	version int64,
) model.MarketPriceFact {
	if price == nil || observedAt == nil {
		return unavailablePriceFact()
	}
	priceUSD := availableDecimal(price)
	if !priceUSD.Available || observedAt.After(now.Add(5*time.Second)) {
		return unavailablePriceFact()
	}
	age := now.Sub(observedAt.UTC())
	if age < 0 {
		age = 0
	}
	status := "unavailable"
	switch {
	case age <= freshFor:
		status = "fresh"
	case age <= readableFor:
		status = "stale"
	default:
		return unavailablePriceFact()
	}
	normalizedContributors := uniqueNonEmpty(contributors)
	if contributorCount < len(normalizedContributors) {
		contributorCount = len(normalizedContributors)
	}
	fact := model.MarketPriceFact{
		PriceUSD:            priceUSD,
		Change24hPct:        availableDecimal(change),
		Turnover24hUSD:      availableDecimal(turnover),
		Available:           true,
		Kind:                firstNonEmpty(kind, "unknown"),
		Source:              strings.TrimSpace(source),
		FreshnessStatus:     status,
		FreshnessAgeSeconds: int64(age / time.Second),
		Quality:             firstNonEmpty(quality, "unknown"),
		ContributorCount:    contributorCount,
		Contributors:        normalizedContributors,
		Version:             version,
	}
	if sourceTime != nil {
		fact.SourceTime = sourceTime.UnixMilli()
	}
	fact.ObservedAt = observedAt.UnixMilli()
	if lastSuccessAt != nil {
		fact.LastSuccessAt = lastSuccessAt.UnixMilli()
	} else {
		fact.LastSuccessAt = fact.ObservedAt
	}
	return fact
}

func unavailablePriceFact() model.MarketPriceFact {
	return model.MarketPriceFact{
		Kind:            "unavailable",
		FreshnessStatus: "unavailable",
		Quality:         "unavailable",
		Contributors:    []string{},
	}
}

func compositeContributorProviders(raw []byte) []string {
	var contributors []struct {
		Provider string `json:"provider"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &contributors) != nil {
		return []string{}
	}
	providers := make([]string, 0, len(contributors))
	for _, contributor := range contributors {
		providers = append(providers, contributor.Provider)
	}
	return uniqueNonEmpty(providers)
}

func uniqueNonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if normalized := strings.TrimSpace(value); normalized != "" {
			return normalized
		}
	}
	return ""
}

func (h HandleSvc) GetAssetMarkets(request *model.AssetMarketsRequest) (*model.AssetMarketsResponse, error) {
	assetID := strings.TrimSpace(request.AssetID)
	if assetID == "" {
		return nil, fmt.Errorf("asset_id is required")
	}
	indexRows, _, err := h.marketAggregationView.QueryAssetIndexDashboard(database.AssetIndexDashboardQuery{
		Page: 1, PageSize: 1, Venue: "all", Search: assetID,
	})
	if err != nil {
		return nil, err
	}
	var composite *decimal.Decimal
	assetConfidence := "unknown"
	if len(indexRows) == 1 && indexRows[0].AssetID == assetID && indexRows[0].CompositePrice != nil {
		if value, parseErr := decimal.NewFromString(*indexRows[0].CompositePrice); parseErr == nil {
			composite = &value
			assetConfidence = indexRows[0].Confidence
		}
	}
	rows, err := h.marketAggregationView.QueryAssetMarkets(assetID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	rates, rateErr := h.marketAggregationView.QueryUSDReferenceRates(10*time.Minute, now)
	if rateErr != nil {
		rates = map[string]string{"USD": "1"}
	}
	result := make([]model.AssetMarketV2Item, 0, len(rows))
	for _, row := range rows {
		if request.Venue != "" && request.Venue != "all" &&
			!strings.EqualFold(row.Provider, request.Venue) {
			continue
		}
		price := unscaleOptional(row.Price)
		turnover := availableDecimal(unscalePointer(row.Turnover24h))
		priceUSD := normalizeQuoteAmount(price, rates[row.QuoteAsset])
		item := model.AssetMarketV2Item{
			MarketID: row.MarketID, MarketCode: row.MarketCode, Provider: row.Provider,
			Symbol: row.Symbol, MarketType: row.MarketType, QuoteAsset: row.QuoteAsset,
			Price: price, Change24hPct: availableDecimal(row.Change24hPct),
			Turnover24h:     turnover,
			FreshnessStatus: "Unknown", Confidence: "excluded", HasKline: row.HasKline,
		}
		if row.ObservedAt != nil {
			item.ProviderUpdatedAt = row.ObservedAt.UnixMilli()
			delay := now.Sub(row.ObservedAt.UTC())
			switch {
			case delay <= 30*time.Second:
				item.FreshnessStatus = "Healthy"
			case delay <= 2*time.Minute:
				item.FreshnessStatus = "Stale"
			default:
				item.FreshnessStatus = "Unavailable"
			}
		}
		if strings.EqualFold(row.MarketType, "spot") &&
			item.FreshnessStatus == "Healthy" &&
			row.CompositeContributor {
			item.Confidence = assetConfidence
		} else if strings.EqualFold(row.MarketType, "perp") {
			item.Confidence = "excluded_perp"
		}
		if composite != nil && priceUSD.Available && priceUSD.Value != nil && composite.GreaterThan(decimal.Zero) {
			if marketPrice, parseErr := decimal.NewFromString(*priceUSD.Value); parseErr == nil {
				value := marketPrice.Sub(*composite).Div(*composite).Mul(decimal.NewFromInt(100)).String()
				item.RelativeDeviationPct = model.AvailableDecimal{Value: &value, Available: true}
			}
		}
		result = append(result, item)
	}
	dexRoutes, err := h.marketAggregationView.QueryPublishedDexRoutes(assetID)
	if err != nil {
		return nil, err
	}
	for _, route := range dexRoutes {
		if request.Venue != "" && request.Venue != "all" &&
			!strings.EqualFold(route.Provider, request.Venue) {
			continue
		}
		var path []string
		var pools []string
		var protocols []string
		_ = json.Unmarshal(route.Path, &path)
		_ = json.Unmarshal(route.PoolAddresses, &pools)
		_ = json.Unmarshal(route.ProtocolVersions, &protocols)
		chain := fmt.Sprintf("chain:%d", route.ChainID)
		switch route.ChainID {
		case 1:
			chain = "Ethereum"
		case 56:
			chain = "BNB Chain"
		}
		providerUpdatedAt, freshnessStatus := marketFreshness(route.Provider, &route.ObservedAt)
		item := model.AssetMarketV2Item{
			MarketID:   "dex:" + route.Provider + ":" + route.RouteKey,
			MarketCode: route.Provider + ":" + route.RouteKey,
			Provider:   route.Provider, Symbol: strings.Join(path, " → "),
			MarketType: "dex_route", QuoteAsset: "USD",
			Price:             availableDecimal(route.PriceUSD),
			Change24hPct:      availableDecimal(route.Change24hPct),
			Turnover24h:       availableDecimal(route.Turnover24hUSD),
			FreshnessStatus:   freshnessStatus,
			ProviderUpdatedAt: providerUpdatedAt,
			Confidence:        route.Quality, VenueKind: "dex_route",
			Chain: chain, Protocol: formatRouteProtocols(protocols),
			RouteKey: route.RouteKey,
			Route:    path, PoolAddresses: pools,
			QuoteNotionalUSD:   availableDecimal(&route.QuoteNotionalUSD),
			QuoteReferenceKind: route.QuoteReferenceKind,
			TVLUSD:             availableDecimal(route.TVLUSD),
			PriceImpactPct:     availableDecimal(route.PriceImpactPct),
			RoundTripSpreadPct: availableDecimal(route.RoundTripSpreadPct),
			Quality:            route.Quality, Available: route.Available,
		}
		if route.BlockNumber != nil {
			item.BlockNumber = *route.BlockNumber
		}
		if route.BlockTimestamp != nil {
			item.BlockTimestamp = route.BlockTimestamp.UnixMilli()
		}
		if route.UnavailableReason != nil {
			item.UnavailableReason = *route.UnavailableReason
		}
		if composite != nil && route.PriceUSD != nil {
			if priceValue, parseErr := decimal.NewFromString(*route.PriceUSD); parseErr == nil &&
				composite.GreaterThan(decimal.Zero) {
				deviation := priceValue.Sub(*composite).Div(*composite).Mul(decimal.NewFromInt(100)).String()
				item.RelativeDeviationPct = model.AvailableDecimal{Value: &deviation, Available: true}
			}
		}
		result = append(result, item)
	}
	return &model.AssetMarketsResponse{
		Code: 2000, Message: "get asset markets success", Result: result,
	}, nil
}

func (h HandleSvc) GetAssetVenues(request *model.AssetVenuesRequest) (*model.AssetVenuesResponse, error) {
	return h.GetAssetMarkets(request)
}

func (h HandleSvc) GetProviderCatalogAudit(request *model.ProviderCatalogAuditRequest) (*model.ProviderCatalogAuditResponse, error) {
	rows, counts, total, err := h.marketAggregationView.QueryCatalogAudit(
		request.Provider, request.Status, request.RankLimit, request.Page, request.PageSize,
	)
	if err != nil {
		return nil, err
	}
	result := make([]model.ProviderCatalogAuditItem, 0, len(rows))
	for _, row := range rows {
		result = append(result, model.ProviderCatalogAuditItem{
			Provider: row.Provider, SourceSymbol: row.SourceSymbol, MarketType: row.MarketType,
			BaseAlias: row.BaseAlias, QuoteAlias: row.QuoteAlias,
			UpstreamStatus: row.UpstreamStatus, ResolutionStatus: row.ResolutionStatus,
			BaseAssetID: row.BaseAssetGuid, QuoteAssetID: row.QuoteAssetGuid,
			Reason: row.RejectionReason, LastSeenAt: row.LastSeenAt.UnixMilli(),
			Rank: row.Rank, CandidateKind: row.CandidateKind, AliasReview: row.AliasReview,
			RolloutMode: row.RolloutMode, ResolutionSource: row.ResolutionSource,
		})
	}
	countResult := make([]model.CatalogAuditCount, 0, len(counts))
	for _, count := range counts {
		countResult = append(countResult, model.CatalogAuditCount{Status: count.Status, Count: count.Count})
	}
	return &model.ProviderCatalogAuditResponse{
		Code: 2000, Message: "get provider catalog audit success",
		Result: result, Counts: countResult, Total: total,
	}, nil
}

func availableDecimal(value *string) model.AvailableDecimal {
	if value == nil || strings.TrimSpace(*value) == "" {
		return model.AvailableDecimal{}
	}
	copy := strings.TrimSpace(*value)
	return model.AvailableDecimal{Value: &copy, Available: true}
}

func formatRouteProtocols(protocols []string) string {
	if len(protocols) == 0 {
		return "unknown"
	}
	normalized := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		value := strings.ToLower(strings.TrimSpace(protocol))
		if value != "v2" && value != "v3" {
			value = "unknown"
		}
		normalized = append(normalized, value)
	}
	return strings.Join(normalized, " → ")
}

func unscaleOptional(value string) model.AvailableDecimal {
	if strings.TrimSpace(value) == "" {
		return model.AvailableDecimal{}
	}
	unscaled := unscaleString(value, 8)
	if parsed, err := decimal.NewFromString(unscaled); err != nil || parsed.LessThanOrEqual(decimal.Zero) {
		return model.AvailableDecimal{}
	}
	return model.AvailableDecimal{Value: &unscaled, Available: true}
}

func unscalePointer(value *string) *string {
	if value == nil {
		return nil
	}
	unscaled := unscaleString(*value, 8)
	return &unscaled
}

func normalizeQuoteAmount(value model.AvailableDecimal, rawRate string) model.AvailableDecimal {
	if !value.Available || value.Value == nil {
		return model.AvailableDecimal{}
	}
	amount, amountErr := decimal.NewFromString(*value.Value)
	rate, rateErr := decimal.NewFromString(strings.TrimSpace(rawRate))
	if amountErr != nil || rateErr != nil || rate.LessThanOrEqual(decimal.Zero) {
		return model.AvailableDecimal{}
	}
	normalized := amount.Mul(rate).String()
	return model.AvailableDecimal{Value: &normalized, Available: true}
}
