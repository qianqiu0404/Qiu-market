package database

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ProviderMarket struct {
	Provider         string `gorm:"column:provider"`
	ExchangeGuid     string `gorm:"column:exchange_guid"`
	ExchangeName     string `gorm:"column:exchange_name"`
	MarketID         string `gorm:"column:market_id"`
	MarketCode       string `gorm:"column:market_code"`
	SymbolGuid       string `gorm:"column:symbol_guid"`
	SymbolName       string `gorm:"column:symbol_name"`
	MarketType       string `gorm:"column:market_type"`
	SourceSymbol     string `gorm:"column:source_symbol"`
	BaseAssetID      string `gorm:"column:base_asset_id"`
	QuoteAssetID     string `gorm:"column:quote_asset_id"`
	QuoteAsset       string `gorm:"column:quote_asset"`
	SelectionVersion int64  `gorm:"column:selection_version"`
	SelectionRank    int    `gorm:"column:selection_rank"`
}

type ProviderKlineSelection struct {
	Provider         string    `gorm:"column:provider;primaryKey"`
	SelectionVersion int64     `gorm:"column:selection_version;primaryKey"`
	AssetGuid        string    `gorm:"column:asset_guid;primaryKey"`
	MarketID         string    `gorm:"column:market_id"`
	SourceSymbol     string    `gorm:"column:source_symbol"`
	QuoteAssetGuid   string    `gorm:"column:quote_asset_guid"`
	SelectionRank    int       `gorm:"column:selection_rank"`
	SelectionReason  string    `gorm:"column:selection_reason"`
	SelectedAt       time.Time `gorm:"column:selected_at"`
	CreatedAt        time.Time `gorm:"column:created_at"`
}

func (ProviderKlineSelection) TableName() string {
	return "provider_kline_selection"
}

type AssetExternalMapping struct {
	Provider   string `gorm:"column:provider"`
	AssetGuid  string `gorm:"column:asset_guid"`
	ExternalID string `gorm:"column:external_id"`
}

func (e *exchangeSymbolDB) QueryProviderMarkets(provider string) ([]ProviderMarket, error) {
	var rows []ProviderMarket
	if err := e.gorm.Table("exchange_symbol es").
		Select(`exchange.code AS provider,
			es.exchange_guid,
			exchange.name AS exchange_name,
			es.guid AS market_id,
			es.market_code,
			es.symbol_guid,
			symbol.symbol_name,
			symbol.market_type,
			es.source_symbol,
			symbol.base_asset_guid AS base_asset_id,
			symbol.qoute_asset_guid AS quote_asset_id,
			quote_asset.asset_symbol AS quote_asset`).
		Joins("JOIN exchange ON exchange.guid = es.exchange_guid").
		Joins("JOIN symbol ON symbol.guid = es.symbol_guid").
		Joins("JOIN asset quote_asset ON quote_asset.guid = symbol.qoute_asset_guid").
		Where("exchange.code = ? AND es.is_active = ? AND symbol.is_active = ?", normalizedProvider(provider), true, true).
		Order("es.market_code ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// QueryProviderKlineMarkets deliberately restricts repair work to markets
// explicitly approved for the K-line product. Existing accidental rows are
// not proof of product membership: enabling a ticker-only Top 50 market must
// not silently create historical backfill work.
func (e *exchangeSymbolDB) QueryProviderKlineMarkets(provider string) ([]ProviderMarket, error) {
	return queryProviderSelectionKlineMarkets(e.gorm, provider)
}

func queryProviderSelectionKlineMarkets(
	db *gorm.DB,
	provider string,
) ([]ProviderMarket, error) {
	var rows []ProviderMarket
	err := db.Raw(`
		SELECT exchange.code AS provider,
			es.exchange_guid,
			exchange.name AS exchange_name,
			es.guid AS market_id,
			es.market_code,
			es.symbol_guid,
			symbol.symbol_name,
			symbol.market_type,
			es.source_symbol,
			symbol.base_asset_guid AS base_asset_id,
			symbol.qoute_asset_guid AS quote_asset_id,
			quote_asset.asset_symbol AS quote_asset,
			selection.selection_version,
			selection.selection_rank
		FROM provider_kline_selection selection
		JOIN provider_asset_selection_state selection_state
		  ON selection_state.provider = selection.provider
		 AND selection_state.active_version = selection.selection_version
		JOIN exchange_symbol es ON es.guid = selection.market_id
		JOIN exchange ON exchange.guid = es.exchange_guid
		JOIN symbol ON symbol.guid = es.symbol_guid
		JOIN asset quote_asset ON quote_asset.guid = symbol.qoute_asset_guid
		WHERE selection.provider = ?
		  AND es.is_active = TRUE
		  AND es.kline_enabled = TRUE
		  AND symbol.is_active = TRUE
		ORDER BY selection.selection_rank ASC, es.guid ASC`,
		normalizedProvider(provider),
	).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (e *exchangeSymbolDB) ReconcileProviderKlineSelection(
	provider string,
) ([]ProviderMarket, error) {
	provider = normalizedProvider(provider)
	switch provider {
	case "binance", "coinbase", "bybit", "okx":
	default:
		return nil, fmt.Errorf("provider %s does not support CEX K-lines", provider)
	}
	var selected []ProviderMarket
	err := e.gorm.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			"SELECT pg_advisory_xact_lock(hashtext(?))",
			"provider_kline_selection:"+provider,
		).Error; err != nil {
			return err
		}
		var state ProviderAssetSelectionState
		if err := tx.Where("provider = ?", provider).First(&state).Error; err != nil {
			return err
		}
		query := `
WITH ranked AS (
	SELECT exchange.code AS provider,
		es.exchange_guid,
		exchange.name AS exchange_name,
		es.guid AS market_id,
		es.market_code,
		es.symbol_guid,
		symbol.symbol_name,
		symbol.market_type,
		es.source_symbol,
		symbol.base_asset_guid AS base_asset_id,
		symbol.qoute_asset_guid AS quote_asset_id,
		quote_asset.asset_symbol AS quote_asset,
		selection.selection_version,
		selection.selection_rank,
		ROW_NUMBER() OVER (
			PARTITION BY selection.asset_guid
			ORDER BY
				COALESCE(market.volume, 0)::numeric DESC,
				CASE UPPER(quote_asset.asset_symbol)
					WHEN 'USD' THEN 0
					WHEN 'USDT' THEN 1
					WHEN 'USDC' THEN 2
					ELSE 3
				END,
				es.guid ASC
		) AS market_choice
	FROM provider_asset_selection selection
	JOIN provider_asset_selection_state selection_state
	  ON selection_state.provider = selection.provider
	 AND selection_state.active_version = selection.selection_version
	JOIN exchange ON exchange.code = selection.provider
	JOIN exchange_symbol es
	  ON es.exchange_guid = exchange.guid
	 AND es.is_active = TRUE
	JOIN symbol
	  ON symbol.guid = es.symbol_guid
	 AND symbol.is_active = TRUE
	 AND LOWER(symbol.market_type) = 'spot'
	 AND symbol.base_asset_guid = selection.asset_guid
	JOIN asset quote_asset
	  ON quote_asset.guid = symbol.qoute_asset_guid
	 AND UPPER(quote_asset.asset_symbol) IN ('USD', 'USDT', 'USDC')
	LEFT JOIN symbol_market market
	  ON market.market_id = es.guid
	 AND market.is_active = TRUE
	WHERE selection.provider = ?
	  AND selection.selection_version = selection_state.active_version
	  AND NULLIF(BTRIM(es.source_symbol), '') IS NOT NULL
)
SELECT provider, exchange_guid, exchange_name, market_id, market_code,
	symbol_guid, symbol_name, market_type, source_symbol, base_asset_id,
	quote_asset_id, quote_asset, selection_version, selection_rank
FROM ranked
WHERE market_choice = 1
ORDER BY selection_rank ASC, market_id ASC`
		if err := tx.Raw(query, provider).Scan(&selected).Error; err != nil {
			return err
		}
		if len(selected) != state.SelectedCount {
			return fmt.Errorf(
				"provider %s K-line selection resolved %d markets for %d selected assets",
				provider, len(selected), state.SelectedCount,
			)
		}
		now := time.Now().UTC()
		rows := make([]ProviderKlineSelection, 0, len(selected))
		marketIDs := make([]string, 0, len(selected))
		for _, market := range selected {
			if strings.TrimSpace(market.SourceSymbol) == "" {
				return fmt.Errorf("provider %s selected market %s has no source symbol", provider, market.MarketID)
			}
			rows = append(rows, ProviderKlineSelection{
				Provider: provider, SelectionVersion: market.SelectionVersion,
				AssetGuid: market.BaseAssetID, MarketID: market.MarketID,
				SourceSymbol: market.SourceSymbol, QuoteAssetGuid: market.QuoteAssetID,
				SelectionRank:   market.SelectionRank,
				SelectionReason: "provider-asset-selection-and-fresh-turnover",
				SelectedAt:      now, CreatedAt: now,
			})
			marketIDs = append(marketIDs, market.MarketID)
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "provider"}, {Name: "selection_version"}, {Name: "asset_guid"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"market_id", "source_symbol", "quote_asset_guid",
				"selection_rank", "selection_reason", "selected_at",
			}),
		}).CreateInBatches(rows, 100).Error; err != nil {
			return err
		}
		if err := tx.Exec(`
			UPDATE exchange_symbol
			SET kline_enabled = FALSE, updated_at = clock_timestamp()
			WHERE exchange_guid = (SELECT guid FROM exchange WHERE code = ?)
			  AND kline_enabled = TRUE`, provider).Error; err != nil {
			return err
		}
		if len(marketIDs) > 0 {
			if err := tx.Table("exchange_symbol").
				Where("guid IN ?", marketIDs).
				Updates(map[string]interface{}{
					"kline_enabled": true,
					"updated_at":    now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return queryProviderSelectionKlineMarkets(e.gorm, provider)
}

func (e *exchangeSymbolDB) QueryAssetExternalMappings(provider string) ([]AssetExternalMapping, error) {
	var rows []AssetExternalMapping
	if err := e.gorm.Table("asset_external_mapping").
		Where("provider = ?", normalizedProvider(provider)).
		Order("asset_guid ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (e *exchangeSymbolDB) UpdateSourceSymbol(marketID, sourceSymbol string) error {
	return e.gorm.Table("exchange_symbol").
		Where("guid = ?", marketID).
		Update("source_symbol", sourceSymbol).Error
}
