package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"gorm.io/datatypes"

	"github.com/the-web3/s78-market-services/common/marketidentity"
	"github.com/the-web3/s78-market-services/database"
	"github.com/the-web3/s78-market-services/marketdata"
	"github.com/the-web3/s78-market-services/redis"
)

// Hyperliquid 固定标识。guid 沿用种子数据的短前缀约定（e1=Binance），h 代表 Hyperliquid。
const (
	hyperliquidInfoURL      = "https://api.hyperliquid.xyz/info"
	hyperliquidExchangeGuid = "h1"
	hyperliquidExchangeName = "Hyperliquid"
	hyperliquidExchangeCode = "hyperliquid"
	hyperliquidQuoteSymbol  = "USD"
)

// hyperliquidMeta 是 metaAndAssetCtxs 响应的第一个元素：永续合约元信息。
type hyperliquidMeta struct {
	Universe []struct {
		Name       string `json:"name"`
		IsDelisted bool   `json:"isDelisted"`
	} `json:"universe"`
}

// hyperliquidAssetCtx 是 metaAndAssetCtxs 响应第二个元素中每个合约的行情上下文。
// 所有字段都是字符串形式的十进制数，价格以 USD 计价（USDC 保证金永续）。
type hyperliquidAssetCtx struct {
	MarkPx    string `json:"markPx"`
	MidPx     string `json:"midPx"`
	PrevDayPx string `json:"prevDayPx"`
	DayNtlVlm string `json:"dayNtlVlm"`
}

// DexCrawler 是 Hyperliquid Perp 行情采集进程（make dex）。
// 每 5 秒拉取一次 metaAndAssetCtxs；只有 provider alias 已审核且 base
// 位于 Top 200 的市场才能建立/复用 symbol 和 exchange_symbol。未知资产只
// 进入 Catalog Audit，不能按 symbol 自动创建。快照与 Spot adapter 共用
// PostgreSQL-first 的 SnapshotWriter；Perp 永不参加综合现货价。
type DexCrawler struct {
	db         *database.DB
	writer     *marketdata.SnapshotWriter
	reporter   *marketdata.ProviderReporter
	httpClient *http.Client
	stopped    atomic.Bool
	cancel     context.CancelFunc

	// 注册表缓存：进程启动时从 DB 加载，之后只在出现新币时追加。
	// dex 进程是 h-* 行的唯一写入方，读缓存 + 缺失才插入即可保证幂等。
	mu                   sync.Mutex
	assetGuidBySymbol    map[string]string // 资产符号（大写） -> asset guid
	symbolGuidByName     map[string]string // Hyperliquid 合约名 -> symbol guid
	linkedSymbols        map[string]bool   // 已建立 exchange_symbol 关联的 symbol guid
	sourceBySymbol       map[string]string // symbol guid -> provider source symbol
	approvedAssetByAlias map[string]string // provider-reviewed alias -> canonical asset guid
	usdAssetGuid         string
}

func NewDexCrawler(db *database.DB, redisClient *redis.Client) *DexCrawler {
	return &DexCrawler{
		db:         db,
		writer:     marketdata.NewSnapshotWriter(db, redisClient),
		reporter:   marketdata.NewProviderReporter(db.ProviderStatus),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (d *DexCrawler) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel

	if err := d.initRegistry(); err != nil {
		// 注册表加载失败不阻断启动：fetchAndStore 每轮都会重试注册
		log.Error("DexCrawler init registry failed, will retry in loop", "err", err)
	}

	go d.runLoop(runCtx)
	log.Info("DexCrawler started", "exchange", hyperliquidExchangeName)
	return nil
}

func (d *DexCrawler) Stop(ctx context.Context) error {
	if d.cancel != nil {
		d.cancel()
	}
	d.stopped.Store(true)
	log.Info("DexCrawler stopped")
	return nil
}

func (d *DexCrawler) Stopped() bool {
	return d.stopped.Load()
}

func (d *DexCrawler) runLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// 启动后立即抓一轮，不等第一个 5 秒
	d.fetchAndStore()

	for {
		select {
		case <-ticker.C:
			d.fetchAndStore()
		case <-ctx.Done():
			return
		}
	}
}

// initRegistry 加载已存在的 asset / symbol / exchange_symbol 到内存缓存，
// 保证进程重启后不会重复插入（幂等注册的前提）。
func (d *DexCrawler) initRegistry() error {
	assets, err := d.db.Asset.QueryAssets()
	if err != nil {
		return fmt.Errorf("query assets: %w", err)
	}
	symbols, err := d.db.Symbol.QuerySymbols()
	if err != nil {
		return fmt.Errorf("query symbols: %w", err)
	}
	links, err := d.db.ExchangeSymbol.QuerySymbolsByExchangeId(hyperliquidExchangeGuid)
	if err != nil {
		return fmt.Errorf("query exchange symbols: %w", err)
	}
	approvedAliases, err := d.db.MarketAggregation.QueryApprovedAliases(hyperliquidExchangeCode)
	if err != nil {
		return fmt.Errorf("query Hyperliquid approved aliases: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.assetGuidBySymbol = make(map[string]string, len(assets))
	for _, a := range assets {
		d.assetGuidBySymbol[strings.ToUpper(a.AssetSymbol)] = a.Guid
	}
	d.symbolGuidByName = make(map[string]string)
	for _, s := range symbols {
		if strings.HasPrefix(s.Guid, "h-s-") {
			d.symbolGuidByName[strings.TrimPrefix(s.Guid, "h-s-")] = s.Guid
		}
	}
	d.linkedSymbols = make(map[string]bool, len(links))
	d.sourceBySymbol = make(map[string]string, len(links))
	d.approvedAssetByAlias = approvedAliases
	for _, l := range links {
		d.linkedSymbols[l.SymbolGuid] = true
		if l.SourceSymbol != nil {
			d.sourceBySymbol[l.SymbolGuid] = *l.SourceSymbol
		}
	}
	if guid, ok := d.approvedAssetByAlias[hyperliquidQuoteSymbol]; ok {
		d.usdAssetGuid = guid
	}
	return nil
}

// fetchAndStore 抓取一轮 Hyperliquid 全市场永续行情并落库。任何单步失败
// 只记录日志并继续，dex 进程永远不因为外部数据问题崩溃。
func (d *DexCrawler) fetchAndStore() {
	sourceKey := "metaAndAssetCtxs"
	if rollout, err := d.db.MarketAggregation.QueryProviderRollout(
		hyperliquidExchangeCode,
	); err == nil && rollout != nil && rollout.LocalPreviewEnabled {
		sourceKey = "metaAndAssetCtxs-preview"
	}
	attemptedAt := time.Now().UTC()
	d.reporter.Attempt("hyperliquid", sourceKey, attemptedAt)
	meta, ctxs, err := d.fetchMetaAndAssetCtxs()
	if err != nil {
		d.reporter.Failure("hyperliquid", sourceKey, time.Now().UTC(), err, 0)
		d.reporter.NextRetry(
			"hyperliquid", sourceKey, time.Now().UTC().Add(5*time.Second),
		)
		log.Error("DexCrawler fetch metaAndAssetCtxs failed", "err", err)
		return
	}
	if len(meta.Universe) != len(ctxs) {
		log.Warn("DexCrawler universe/assetCtxs length mismatch",
			"universe", len(meta.Universe), "assetCtxs", len(ctxs))
	}
	if err := d.auditCatalog(meta); err != nil {
		log.Warn("DexCrawler catalog audit write failed", "error", err)
	}
	selection, selectionErr := d.db.MarketAggregation.EnsureProviderAssetSelection(
		hyperliquidExchangeCode, 50, "hyperliquid-catalog-refresh",
	)
	if selectionErr != nil {
		log.Warn("Hyperliquid provider selection refresh failed", "error", selectionErr)
	} else {
		log.Debug("Hyperliquid provider selection ready",
			"version", selection.ActiveVersion,
			"selected", selection.SelectedCount,
			"candidates", selection.CandidateCount)
	}
	allowedAssets, rollout, err := d.db.MarketAggregation.QueryPublishedAssetIDs(
		hyperliquidExchangeCode,
	)
	if err != nil {
		d.reporter.Failure("hyperliquid", sourceKey, time.Now().UTC(), err, 0)
		log.Warn("DexCrawler rollout state unavailable", "error", err)
		return
	}

	updated := 0
	for i, asset := range meta.Universe {
		if i >= len(ctxs) {
			break
		}
		if asset.IsDelisted || asset.Name == "" {
			continue
		}
		assetID := d.approvedAssetByAlias[strings.ToUpper(asset.Name)]
		if _, allowed := allowedAssets[assetID]; !allowed {
			continue
		}
		if d.processPerp(asset.Name, assetID, &ctxs[i]) {
			updated++
		}
	}
	d.reporter.Success("hyperliquid", sourceKey, time.Now().UTC(), nil)
	rolloutMode := "shadow"
	if rollout != nil {
		rolloutMode = rollout.Mode
	}
	log.Info("DexCrawler updated",
		"exchange", hyperliquidExchangeName, "perps", updated, "rollout_mode", rolloutMode)
}

// fetchMetaAndAssetCtxs 调用 POST /info {"type":"metaAndAssetCtxs"}。
// 响应是二元数组 [meta, assetCtxs]，用 RawMessage 分两段解析。
func (d *DexCrawler) fetchMetaAndAssetCtxs() (*hyperliquidMeta, []hyperliquidAssetCtx, error) {
	resp, err := d.httpClient.Post(hyperliquidInfoURL, "application/json",
		bytes.NewReader([]byte(`{"type":"metaAndAssetCtxs"}`)))
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("hyperliquid bad status: %d", resp.StatusCode)
	}

	var payload []json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, fmt.Errorf("hyperliquid decode envelope: %w", err)
	}
	if len(payload) != 2 {
		return nil, nil, fmt.Errorf("hyperliquid unexpected payload segments: %d", len(payload))
	}

	var meta hyperliquidMeta
	if err := json.Unmarshal(payload[0], &meta); err != nil {
		return nil, nil, fmt.Errorf("hyperliquid decode meta: %w", err)
	}
	var ctxs []hyperliquidAssetCtx
	if err := json.Unmarshal(payload[1], &ctxs); err != nil {
		return nil, nil, fmt.Errorf("hyperliquid decode assetCtxs: %w", err)
	}
	return &meta, ctxs, nil
}

// processPerp 处理单个永续合约：注册（幂等）→ 计算涨跌幅 → 落库 → 缓存 → 榜单。
// 返回是否成功更新了行情。
func (d *DexCrawler) processPerp(
	name, assetID string,
	ctx *hyperliquidAssetCtx,
) bool {
	// 价格优先 midPx（中间价更贴近真实成交），缺失时退回 markPx
	price := ctx.MidPx
	if price == "" {
		price = ctx.MarkPx
	}
	if price == "" || ctx.MarkPx == "" || ctx.PrevDayPx == "" || ctx.DayNtlVlm == "" {
		log.Warn("DexCrawler incomplete asset ctx, skip", "name", name)
		return false
	}

	markPx, err := strconv.ParseFloat(ctx.MarkPx, 64)
	if err != nil {
		log.Warn("DexCrawler invalid markPx, skip", "name", name, "markPx", ctx.MarkPx)
		return false
	}
	prevDayPx, err := strconv.ParseFloat(ctx.PrevDayPx, 64)
	if err != nil || prevDayPx <= 0 {
		log.Warn("DexCrawler invalid prevDayPx, skip", "name", name, "prevDayPx", ctx.PrevDayPx)
		return false
	}
	// 24h 涨跌幅 % = (markPx - prevDayPx) / prevDayPx * 100，与 Binance 的
	// priceChangePercent 语义对齐，ZSET 榜单与 symbol_market.radio 口径一致。
	changePct := (markPx - prevDayPx) / prevDayPx * 100
	changeStr := strconv.FormatFloat(changePct, 'f', 4, 64)

	scaledPrice, err := decimalStringToUint256String(price, 8)
	if err != nil {
		log.Error("DexCrawler scale price failed", "name", name, "price", price, "err", err)
		return false
	}
	scaledVolume, err := decimalStringToUint256String(ctx.DayNtlVlm, 8)
	if err != nil {
		log.Error("DexCrawler scale volume failed", "name", name, "volume", ctx.DayNtlVlm, "err", err)
		return false
	}
	scaledOpen, err := decimalStringToUint256String(ctx.PrevDayPx, 8)
	if err != nil {
		log.Error("DexCrawler scale open price failed", "name", name, "open", ctx.PrevDayPx, "err", err)
		return false
	}

	symbolGuid, symbolName, err := d.ensureRegistered(name)
	if err != nil {
		log.Error("DexCrawler register symbol failed", "name", name, "err", err)
		return false
	}

	marketID := "h-es-" + normalizeDexName(name)
	observedAt := time.Now().UTC()
	result, err := d.writer.Write(context.Background(), marketdata.Snapshot{
		MarketSnapshotInput: database.MarketSnapshotInput{
			Guid:             "h-m-" + normalizeDexName(name),
			MarketID:         marketID,
			SymbolGuid:       symbolGuid,
			Price:            scaledPrice,
			AskPrice:         scaledPrice, // metaAndAssetCtxs has no top of book.
			BidPrice:         scaledPrice,
			Volume:           scaledVolume,
			Open24h:          &scaledOpen,
			QuoteTurnover24h: &scaledVolume,
			Change24hPct:     &changeStr,
			IsActive:         true,
			ObservedAt:       observedAt,
			// metaAndAssetCtxs does not publish an event timestamp.
			SourceTime:     nil,
			SourceTimeKind: nil,
		},
		ExchangeGuid: hyperliquidExchangeGuid,
		ExchangeName: hyperliquidExchangeName,
		SymbolName:   symbolName,
	})
	if err != nil {
		log.Error("DexCrawler snapshot write failed",
			"name", name, "market_id", marketID, "err", err)
		return false
	}
	metadata, _ := json.Marshal(map[string]string{
		"market_id":  marketID,
		"mark_price": ctx.MarkPx,
		"mid_price":  price,
	})
	if err := d.db.MarketAggregation.UpsertAssetVenueSnapshots([]database.AssetVenueSnapshot{{
		AssetGuid: assetID, Provider: hyperliquidExchangeCode, PriceKind: "perp_mark",
		PriceUSD: &price, Open24hUSD: &ctx.PrevDayPx, Change24hPct: &changeStr,
		Turnover24hUSD: &ctx.DayNtlVlm, ContributorCount: 1, MarketCount: 1,
		Confidence: "low", Quality: "medium", Available: true,
		ObservedAt: observedAt, Metadata: metadata,
	}}); err != nil {
		log.Warn("Hyperliquid venue snapshot write failed",
			"name", name, "market_id", marketID, "error", err)
	}
	log.Debug("DexCrawler snapshot observed", "market_id", marketID, "action", result.Action)
	return true
}

func (d *DexCrawler) auditCatalog(meta *hyperliquidMeta) error {
	approvedAliases, err := d.db.MarketAggregation.QueryApprovedAliases(
		hyperliquidExchangeCode,
	)
	if err != nil {
		return err
	}
	d.approvedAssetByAlias = approvedAliases
	topAssets, err := d.db.MarketAggregation.QueryTopAssetIDs(200)
	if err != nil {
		return err
	}
	uniqueSymbols, err := d.db.MarketAggregation.QueryUniqueTopAssetSymbols(200)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	candidates := make([]database.ProviderMarketCandidate, 0, len(meta.Universe))
	suggestions := make([]database.AssetAlias, 0)
	for _, item := range meta.Universe {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		alias := strings.ToUpper(item.Name)
		raw, _ := json.Marshal(item)
		candidate := database.ProviderMarketCandidate{
			Provider: hyperliquidExchangeCode, SourceSymbol: item.Name, MarketType: "perp",
			BaseAlias: alias, QuoteAlias: hyperliquidQuoteSymbol,
			UpstreamStatus: optionalText("live"), ResolutionStatus: "discovered",
			FirstSeenAt: now, LastSeenAt: now, RawMetadata: datatypes.JSON(raw),
		}
		if item.IsDelisted {
			candidate.UpstreamStatus = optionalText("delisted")
			candidate.ResolutionStatus = "rejected"
			candidate.RejectionReason = optionalText("upstream_not_tradable")
			candidates = append(candidates, candidate)
			continue
		}
		baseID, baseApproved := d.approvedAssetByAlias[alias]
		quoteID, quoteApproved := d.approvedAssetByAlias[hyperliquidQuoteSymbol]
		if !baseApproved {
			if suggestion, unique := uniqueSymbols[alias]; unique {
				suggestions = append(suggestions, database.AssetAlias{
					Provider: hyperliquidExchangeCode, Alias: alias, AssetGuid: suggestion,
					ReviewStatus: "pending", CreatedAt: now, UpdatedAt: now,
				})
				candidate.RejectionReason = optionalText("base_alias_review_required")
			} else {
				candidate.ResolutionStatus = "ambiguous"
				candidate.RejectionReason = optionalText("base_alias_ambiguous_or_outside_top200")
			}
			candidates = append(candidates, candidate)
			continue
		}
		if !quoteApproved {
			candidate.ResolutionStatus = "ambiguous"
			candidate.RejectionReason = optionalText("quote_alias_review_required")
			candidates = append(candidates, candidate)
			continue
		}
		if _, included := topAssets[baseID]; !included {
			candidate.ResolutionStatus = "rejected"
			candidate.RejectionReason = optionalText("base_asset_outside_top200")
			candidates = append(candidates, candidate)
			continue
		}
		candidate.BaseAssetGuid = &baseID
		candidate.QuoteAssetGuid = &quoteID
		candidate.ResolvedAt = &now
		symbolID := d.symbolGuidByName[normalizeDexName(item.Name)]
		if symbolID != "" && d.linkedSymbols[symbolID] {
			candidate.ResolutionStatus = "enabled"
			candidate.EnabledAt = &now
		} else {
			candidate.ResolutionStatus = "resolved"
		}
		candidates = append(candidates, candidate)
	}
	if err := d.db.MarketAggregation.UpsertAssetAliases(suggestions); err != nil {
		return err
	}
	return d.db.MarketAggregation.UpsertProviderMarketCandidates(candidates)
}

// ensureRegistered 保证 exchange / asset / symbol / exchange_symbol 四行存在，
// 全部幂等：已存在则直接复用 guid，不存在才插入。
func (d *DexCrawler) ensureRegistered(name string) (symbolGuid, symbolName string, err error) {
	key := normalizeDexName(name)

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.assetGuidBySymbol == nil {
		return "", "", fmt.Errorf("registry not initialized")
	}

	if err := d.ensureExchangeLocked(); err != nil {
		return "", "", err
	}
	if err := d.ensureQuoteAssetLocked(); err != nil {
		return "", "", err
	}

	upperName := strings.ToUpper(name)
	baseAssetGuid, ok := d.approvedAssetByAlias[upperName]
	if !ok {
		return "", "", fmt.Errorf("Hyperliquid alias %s is not approved; candidate remains audit-only", upperName)
	}

	symbolGuid, ok = d.symbolGuidByName[key]
	symbolName = upperName + "/" + hyperliquidQuoteSymbol
	if !ok {
		symbolGuid = "h-s-" + key
		symbol := &database.Symbol{
			Guid:           symbolGuid,
			SymbolName:     symbolName,
			BaseAssetGuid:  baseAssetGuid,
			QuoteAssetGuid: d.usdAssetGuid,
			MarketType:     "PERP",
			IsActive:       true,
		}
		if err := d.db.Symbol.StoreSymbol(symbol); err != nil {
			return "", "", fmt.Errorf("store symbol %s: %w", symbolName, err)
		}
		d.symbolGuidByName[key] = symbolGuid
		log.Info("DexCrawler registered symbol", "name", symbolName, "guid", symbolGuid)
	}

	if !d.linkedSymbols[symbolGuid] {
		marketCode, err := marketidentity.GenerateMarketCode(hyperliquidExchangeCode, symbolName, "PERP")
		if err != nil {
			return "", "", err
		}
		sourceSymbol := name
		link := &database.ExchangeSymbol{
			Guid:         "h-es-" + key,
			MarketCode:   marketCode,
			SourceSymbol: &sourceSymbol,
			ExchangeGuid: hyperliquidExchangeGuid,
			SymbolGuid:   symbolGuid,
			Volume:       "0",
			IsActive:     true,
		}
		if err := d.db.ExchangeSymbol.StoreExchangeSymbol(link); err != nil {
			return "", "", fmt.Errorf("store exchange_symbol %s: %w", symbolGuid, err)
		}
		d.linkedSymbols[symbolGuid] = true
		d.sourceBySymbol[symbolGuid] = name
		log.Info("DexCrawler linked exchange_symbol", "symbol_guid", symbolGuid, "exchange", hyperliquidExchangeName)
	} else if d.sourceBySymbol[symbolGuid] != name {
		if err := d.db.ExchangeSymbol.UpdateSourceSymbol("h-es-"+key, name); err != nil {
			return "", "", fmt.Errorf("update Hyperliquid source_symbol %s: %w", symbolGuid, err)
		}
		d.sourceBySymbol[symbolGuid] = name
	}

	return symbolGuid, symbolName, nil
}

// ensureExchangeLocked 保证 exchange 表存在 h1 / Hyperliquid 行。
func (d *DexCrawler) ensureExchangeLocked() error {
	exchanges, err := d.db.Exchange.QueryExchanges()
	if err != nil {
		return fmt.Errorf("query exchanges: %w", err)
	}
	for _, e := range exchanges {
		if e.Guid == hyperliquidExchangeGuid || e.Name == hyperliquidExchangeName {
			return nil
		}
	}
	exchange := &database.Exchange{
		Guid:     hyperliquidExchangeGuid,
		Code:     hyperliquidExchangeCode,
		Name:     hyperliquidExchangeName,
		Config:   datatypes.JSON([]byte(`{"type":"dex","venue":"hyperliquid"}`)),
		IsActive: true,
	}
	if err := d.db.Exchange.StoreExchange(exchange); err != nil {
		return fmt.Errorf("store exchange: %w", err)
	}
	log.Info("DexCrawler registered exchange", "guid", hyperliquidExchangeGuid, "name", hyperliquidExchangeName)
	return nil
}

// ensureQuoteAssetLocked 保证 USD 计价资产存在（Hyperliquid 永续为 USDC 保证金，按 USD 口径记账）。
func (d *DexCrawler) ensureQuoteAssetLocked() error {
	if d.usdAssetGuid != "" {
		return nil
	}
	if guid, ok := d.approvedAssetByAlias[hyperliquidQuoteSymbol]; ok {
		d.usdAssetGuid = guid
		return nil
	}
	return fmt.Errorf("Hyperliquid quote alias %s is not approved", hyperliquidQuoteSymbol)
}

// normalizeDexName 把 Hyperliquid 合约名转成 guid 安全形式（大写字母数字，其余替换为 -）。
// 例如 "BTC" -> "BTC"，"kPEPE" -> "KPEPE"。
func normalizeDexName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
