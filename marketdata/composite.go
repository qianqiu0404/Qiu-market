package marketdata

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/the-web3/s78-market-services/database"
)

const (
	CompositeFreshnessLimit = 30 * time.Second
	StableRateFreshness     = 10 * time.Minute
	compositeOutlierLimit   = 0.03
	compositeVenueWeightCap = 0.40
)

type CompositeCandidate struct {
	MarketID     string
	MarketCode   string
	Provider     string
	MarketType   string
	QuoteAsset   string
	Price        decimal.Decimal
	Open24h      *decimal.Decimal
	Change24hPct *decimal.Decimal
	Turnover24h  decimal.Decimal
	ObservedAt   time.Time
	SourceTime   *time.Time
	QuoteToUSD   *decimal.Decimal
}

type CompositeContributor struct {
	MarketID       string `json:"market_id"`
	MarketCode     string `json:"market_code"`
	Provider       string `json:"provider"`
	Weight         string `json:"weight"`
	PriceUSD       string `json:"price_usd"`
	Turnover24hUSD string `json:"turnover_24h_usd"`
}

type CompositeExclusion struct {
	MarketID string `json:"market_id"`
	Reason   string `json:"reason"`
}

type CompositeResult struct {
	Available        bool
	PriceUSD         *decimal.Decimal
	Open24hUSD       *decimal.Decimal
	Change24hPct     *decimal.Decimal
	Turnover24hUSD   *decimal.Decimal
	ContributorCount int
	Confidence       string
	Contributors     []CompositeContributor
	Exclusions       []CompositeExclusion
}

type weightedCandidate struct {
	candidate CompositeCandidate
	priceUSD  decimal.Decimal
	openUSD   *decimal.Decimal
	turnover  decimal.Decimal
	weight    decimal.Decimal
}

func BuildComposite(candidates []CompositeCandidate, now time.Time) CompositeResult {
	valid := make([]weightedCandidate, 0, len(candidates))
	exclusions := make([]CompositeExclusion, 0)
	for _, candidate := range candidates {
		reason := ""
		switch {
		case !strings.EqualFold(candidate.MarketType, "spot"):
			reason = "not_spot"
		case candidate.Price.LessThanOrEqual(decimal.Zero):
			reason = "non_positive_price"
		case candidate.ObservedAt.IsZero() || now.Sub(candidate.ObservedAt) > CompositeFreshnessLimit:
			reason = "stale"
		case candidate.ObservedAt.After(now.Add(5 * time.Second)):
			reason = "future_observation"
		case candidate.QuoteToUSD == nil || candidate.QuoteToUSD.LessThanOrEqual(decimal.Zero):
			reason = "missing_quote_rate"
		}
		if reason != "" {
			exclusions = append(exclusions, CompositeExclusion{MarketID: candidate.MarketID, Reason: reason})
			continue
		}
		rate := *candidate.QuoteToUSD
		priceUSD := candidate.Price.Mul(rate)
		turnover := candidate.Turnover24h.Mul(rate)
		var openUSD *decimal.Decimal
		open24h := candidate.Open24h
		if open24h == nil && candidate.Change24hPct != nil {
			denominator := decimal.NewFromInt(1).
				Add(candidate.Change24hPct.Div(decimal.NewFromInt(100)))
			if denominator.GreaterThan(decimal.Zero) {
				value := candidate.Price.Div(denominator)
				open24h = &value
			}
		}
		if open24h != nil && open24h.GreaterThan(decimal.Zero) {
			value := open24h.Mul(rate)
			openUSD = &value
		}
		valid = append(valid, weightedCandidate{
			candidate: candidate,
			priceUSD:  priceUSD,
			openUSD:   openUSD,
			turnover:  decimal.Max(turnover, decimal.Zero),
		})
	}
	if len(valid) == 0 {
		return CompositeResult{
			Confidence: "unknown", Contributors: []CompositeContributor{}, Exclusions: exclusions,
		}
	}

	prices := make([]decimal.Decimal, len(valid))
	for i := range valid {
		prices[i] = valid[i].priceUSD
	}
	median := decimalMedian(prices)
	filtered := valid[:0]
	for _, candidate := range valid {
		deviation := candidate.priceUSD.Sub(median).Abs().Div(median)
		if deviation.GreaterThan(decimal.NewFromFloat(compositeOutlierLimit)) {
			exclusions = append(exclusions, CompositeExclusion{
				MarketID: candidate.candidate.MarketID,
				Reason:   "median_deviation_gt_3pct",
			})
			continue
		}
		filtered = append(filtered, candidate)
	}
	valid = filtered
	if len(valid) == 0 {
		return CompositeResult{
			Confidence: "unknown", Contributors: []CompositeContributor{}, Exclusions: exclusions,
		}
	}

	assignCompositeWeights(valid)
	participatingVenues := make(map[string]struct{}, len(valid))
	price := decimal.Zero
	open := decimal.Zero
	turnover := decimal.Zero
	openAvailable := true
	contributors := make([]CompositeContributor, 0, len(valid))
	for _, candidate := range valid {
		participatingVenues[strings.ToLower(strings.TrimSpace(candidate.candidate.Provider))] = struct{}{}
		price = price.Add(candidate.priceUSD.Mul(candidate.weight))
		turnover = turnover.Add(candidate.turnover)
		if candidate.openUSD == nil {
			openAvailable = false
		} else {
			open = open.Add(candidate.openUSD.Mul(candidate.weight))
		}
		contributors = append(contributors, CompositeContributor{
			MarketID:       candidate.candidate.MarketID,
			MarketCode:     candidate.candidate.MarketCode,
			Provider:       candidate.candidate.Provider,
			Weight:         candidate.weight.String(),
			PriceUSD:       candidate.priceUSD.String(),
			Turnover24hUSD: candidate.turnover.String(),
		})
	}
	sort.Slice(contributors, func(i, j int) bool {
		if contributors[i].Provider != contributors[j].Provider {
			return contributors[i].Provider < contributors[j].Provider
		}
		return contributors[i].MarketID < contributors[j].MarketID
	})

	result := CompositeResult{
		Available:        true,
		PriceUSD:         decimalPtr(price),
		Turnover24hUSD:   decimalPtr(turnover),
		ContributorCount: len(participatingVenues),
		Confidence:       compositeConfidence(len(participatingVenues)),
		Contributors:     contributors,
		Exclusions:       exclusions,
	}
	if openAvailable && open.GreaterThan(decimal.Zero) {
		result.Open24hUSD = decimalPtr(open)
		change := price.Sub(open).Div(open).Mul(decimal.NewFromInt(100))
		result.Change24hPct = decimalPtr(change)
	}
	return result
}

func assignCompositeWeights(candidates []weightedCandidate) {
	venueTurnover := make(map[string]decimal.Decimal)
	venueCounts := make(map[string]int64)
	totalTurnover := decimal.Zero
	for _, candidate := range candidates {
		provider := strings.ToLower(strings.TrimSpace(candidate.candidate.Provider))
		venueTurnover[provider] = venueTurnover[provider].Add(candidate.turnover)
		venueCounts[provider]++
		totalTurnover = totalTurnover.Add(candidate.turnover)
	}

	if len(venueTurnover) == 0 {
		return
	}
	rawWeights := make(map[string]decimal.Decimal, len(venueTurnover))
	venueCount := decimal.NewFromInt(int64(len(venueTurnover)))
	for provider, turnover := range venueTurnover {
		rawWeights[provider] = decimal.NewFromInt(1).Div(venueCount)
		if totalTurnover.GreaterThan(decimal.Zero) {
			rawWeights[provider] = turnover.Div(totalTurnover)
		}
	}
	cappedVenueWeights := cappedNormalizedWeights(rawWeights, decimal.NewFromFloat(compositeVenueWeightCap))

	for i := range candidates {
		provider := strings.ToLower(strings.TrimSpace(candidates[i].candidate.Provider))
		venueWeight := cappedVenueWeights[provider]
		insideVenueWeight := decimal.NewFromInt(1).Div(decimal.NewFromInt(venueCounts[provider]))
		if venueTurnover[provider].GreaterThan(decimal.Zero) {
			insideVenueWeight = candidates[i].turnover.Div(venueTurnover[provider])
		}
		candidates[i].weight = venueWeight.Mul(insideVenueWeight)
	}
}

// cappedNormalizedWeights applies a water-filling cap. A simple
// min(raw, cap)+renormalize is unsafe: a 90/5/5 split would become 80/10/10
// after renormalization. With at least three venues this function produces
// 40/30/30, so the final normalized weights still respect the 40% ceiling.
// One or two venues cannot mathematically sum to 100% under a 40% cap, so
// those low/medium-confidence cases retain their normalized raw weights.
func cappedNormalizedWeights(raw map[string]decimal.Decimal, cap decimal.Decimal) map[string]decimal.Decimal {
	result := make(map[string]decimal.Decimal, len(raw))
	if len(raw) <= 2 {
		for provider, weight := range raw {
			result[provider] = weight
		}
		return result
	}
	active := make(map[string]decimal.Decimal, len(raw))
	for provider, weight := range raw {
		active[provider] = weight
	}
	remaining := decimal.NewFromInt(1)
	for len(active) > 0 {
		activeTotal := decimal.Zero
		for _, weight := range active {
			activeTotal = activeTotal.Add(weight)
		}
		if activeTotal.IsZero() {
			equal := remaining.Div(decimal.NewFromInt(int64(len(active))))
			for provider := range active {
				result[provider] = equal
			}
			break
		}
		cappedAny := false
		for provider, weight := range active {
			proposed := remaining.Mul(weight).Div(activeTotal)
			if proposed.GreaterThan(cap) {
				result[provider] = cap
				remaining = remaining.Sub(cap)
				delete(active, provider)
				cappedAny = true
			}
		}
		if cappedAny {
			continue
		}
		for provider, weight := range active {
			result[provider] = remaining.Mul(weight).Div(activeTotal)
		}
		break
	}
	return result
}

func decimalMedian(values []decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}
	copyValues := append([]decimal.Decimal(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i].LessThan(copyValues[j]) })
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return copyValues[middle]
	}
	return copyValues[middle-1].Add(copyValues[middle]).Div(decimal.NewFromInt(2))
}

func compositeConfidence(count int) string {
	switch {
	case count >= 3:
		return "high"
	case count == 2:
		return "medium"
	case count == 1:
		return "low"
	default:
		return "unknown"
	}
}

func decimalPtr(value decimal.Decimal) *decimal.Decimal { return &value }

type CompositeStore interface {
	QueryTopAssetIDs(limit int) (map[string]struct{}, error)
	QueryUSDReferenceRates(maxAge time.Duration, now time.Time) (map[string]string, error)
	QueryCompositeMarketCandidates() ([]database.CompositeMarketCandidate, error)
	UpsertAssetPriceIndexes([]database.AssetPriceIndex) error
	UpsertAssetVenueSnapshots([]database.AssetVenueSnapshot) error
}

type selectedAssetUnionStore interface {
	QuerySelectedAssetUnionIDs() (map[string]struct{}, error)
}

type CompositeIndexer struct{ store CompositeStore }

func NewCompositeIndexer(store CompositeStore) *CompositeIndexer {
	return &CompositeIndexer{store: store}
}

func (i *CompositeIndexer) RunOnce(now time.Time) error {
	topAssets, err := i.store.QueryTopAssetIDs(50)
	if err != nil {
		return err
	}
	if selectionStore, ok := i.store.(selectedAssetUnionStore); ok {
		selectedAssets, selectionErr := selectionStore.QuerySelectedAssetUnionIDs()
		if selectionErr != nil {
			return selectionErr
		}
		if len(selectedAssets) > 0 {
			topAssets = selectedAssets
		}
	}
	rates, err := i.store.QueryUSDReferenceRates(StableRateFreshness, now)
	if err != nil {
		return err
	}
	rows, err := i.store.QueryCompositeMarketCandidates()
	if err != nil {
		return err
	}
	grouped := make(map[string][]CompositeCandidate, len(topAssets))
	providerGroups := make(map[string]map[string][]CompositeCandidate)
	for _, provider := range []string{"binance", "coinbase", "bybit", "okx"} {
		providerGroups[provider] = make(map[string][]CompositeCandidate, len(topAssets))
	}
	for assetID := range topAssets {
		grouped[assetID] = nil
		for provider := range providerGroups {
			providerGroups[provider][assetID] = nil
		}
	}
	for _, row := range rows {
		price, priceErr := scaledDecimal(row.Price)
		if priceErr != nil {
			continue
		}
		turnover := decimal.Zero
		if row.QuoteTurnover24h != nil {
			if parsed, parseErr := scaledDecimal(*row.QuoteTurnover24h); parseErr == nil {
				turnover = parsed
			}
		}
		var open *decimal.Decimal
		if row.Open24h != nil {
			if parsed, parseErr := scaledDecimal(*row.Open24h); parseErr == nil {
				open = &parsed
			}
		}
		var change *decimal.Decimal
		if row.Change24hPct != nil {
			if parsed, parseErr := decimal.NewFromString(strings.TrimSpace(*row.Change24hPct)); parseErr == nil {
				change = &parsed
			}
		}
		var rate *decimal.Decimal
		if raw, ok := rates[strings.ToUpper(row.QuoteAsset)]; ok {
			if parsed, parseErr := decimal.NewFromString(raw); parseErr == nil {
				rate = &parsed
			}
		}
		observed := time.Time{}
		if row.ObservedAt != nil {
			observed = row.ObservedAt.UTC()
		}
		candidate := CompositeCandidate{
			MarketID: row.MarketID, MarketCode: row.MarketCode, Provider: row.Provider,
			MarketType: row.MarketType, QuoteAsset: row.QuoteAsset, Price: price,
			Open24h: open, Change24hPct: change,
			Turnover24h: turnover, ObservedAt: observed,
			SourceTime: row.SourceTime, QuoteToUSD: rate,
		}
		grouped[row.AssetID] = append(grouped[row.AssetID], candidate)
		provider := strings.ToLower(strings.TrimSpace(row.Provider))
		if byAsset, exists := providerGroups[provider]; exists {
			byAsset[row.AssetID] = append(byAsset[row.AssetID], candidate)
		}
	}
	indexes := make([]database.AssetPriceIndex, 0, len(grouped))
	snapshots := make([]database.AssetVenueSnapshot, 0, len(grouped)*5)
	for assetID, candidates := range grouped {
		result := BuildComposite(candidates, now)
		contributors, _ := json.Marshal(result.Contributors)
		exclusions, _ := json.Marshal(result.Exclusions)
		index := database.AssetPriceIndex{
			AssetGuid:        assetID,
			ContributorCount: result.ContributorCount,
			Confidence:       result.Confidence,
			Available:        result.Available,
			ObservedAt:       now.UTC(),
			Contributors:     contributors,
			Exclusions:       exclusions,
		}
		index.PriceUSD = decimalString(result.PriceUSD)
		index.Open24hUSD = decimalString(result.Open24hUSD)
		index.Change24hPct = decimalString(result.Change24hPct)
		index.Turnover24hUSD = decimalString(result.Turnover24hUSD)
		indexes = append(indexes, index)
		snapshots = append(snapshots, venueSnapshot(
			assetID, "all", "composite_spot", candidates, result, now,
			contributors, exclusions,
		))
	}
	for provider, byAsset := range providerGroups {
		for assetID, candidates := range byAsset {
			result := BuildComposite(candidates, now)
			contributors, _ := json.Marshal(result.Contributors)
			exclusions, _ := json.Marshal(result.Exclusions)
			snapshots = append(snapshots, venueSnapshot(
				assetID, provider, "venue_spot", candidates, result, now,
				contributors, exclusions,
			))
		}
	}
	sort.Slice(indexes, func(a, b int) bool { return indexes[a].AssetGuid < indexes[b].AssetGuid })
	sort.Slice(snapshots, func(a, b int) bool {
		if snapshots[a].Provider != snapshots[b].Provider {
			return snapshots[a].Provider < snapshots[b].Provider
		}
		return snapshots[a].AssetGuid < snapshots[b].AssetGuid
	})
	if err := i.store.UpsertAssetPriceIndexes(indexes); err != nil {
		return err
	}
	return i.store.UpsertAssetVenueSnapshots(snapshots)
}

func venueSnapshot(
	assetID, provider, priceKind string,
	candidates []CompositeCandidate,
	result CompositeResult,
	now time.Time,
	contributors, exclusions []byte,
) database.AssetVenueSnapshot {
	spotMarketCount := 0
	var sourceTime *time.Time
	for _, candidate := range candidates {
		if strings.EqualFold(candidate.MarketType, "spot") {
			spotMarketCount++
		}
		if candidate.SourceTime != nil && (sourceTime == nil || candidate.SourceTime.After(*sourceTime)) {
			value := candidate.SourceTime.UTC()
			sourceTime = &value
		}
	}
	metadata, _ := json.Marshal(map[string]json.RawMessage{
		"contributors": contributors,
		"exclusions":   exclusions,
	})
	return database.AssetVenueSnapshot{
		AssetGuid: assetID, Provider: provider, PriceKind: priceKind,
		PriceUSD: decimalString(result.PriceUSD), Open24hUSD: decimalString(result.Open24hUSD),
		Change24hPct:     decimalString(result.Change24hPct),
		Turnover24hUSD:   decimalString(result.Turnover24hUSD),
		ContributorCount: result.ContributorCount, MarketCount: spotMarketCount,
		Confidence: result.Confidence, Quality: result.Confidence,
		Available: result.Available, SourceTime: sourceTime, ObservedAt: now.UTC(),
		Metadata: metadata,
	}
}

func scaledDecimal(value string) (decimal.Decimal, error) {
	parsed, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return decimal.Zero, err
	}
	return parsed.Shift(-8), nil
}

func decimalString(value *decimal.Decimal) *string {
	if value == nil {
		return nil
	}
	text := value.String()
	return &text
}
