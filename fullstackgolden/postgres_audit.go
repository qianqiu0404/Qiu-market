package fullstackgolden

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/decimal"
	"github.com/the-web3/s78-market-services/trading/domain"
)

var canonicalTradingMigrations = []string{
	"2026082100023.sql",
	"2026082300025.sql",
	"2026082800030.sql",
}

// ApplyTradingMigrations applies only the canonical trading DDL to a new,
// isolated database. It deliberately reads repository-owned SQL rather than
// embedding a second schema or connecting to the application's shared DB.
func ApplyTradingMigrations(ctx context.Context, pool *pgxpool.Pool, repoRoot string) error {
	if pool == nil {
		return fmt.Errorf("full-stack PostgreSQL pool is required")
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil || root != filepath.Clean(root) {
		return fmt.Errorf("full-stack repository root must be absolute and clean")
	}
	for _, name := range canonicalTradingMigrations {
		path := filepath.Join(root, "migrations", name)
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read canonical migration %s: %w", name, readErr)
		}
		if _, execErr := pool.Exec(ctx, string(payload)); execErr != nil {
			return fmt.Errorf("apply canonical migration %s: %w", name, execErr)
		}
	}
	return nil
}

// AuditDatabase independently reconstructs the durable facts required by the
// browser proof. Returned rows are stably ordered and contain no credentials.
func AuditDatabase(ctx context.Context, pool *pgxpool.Pool) (DatabaseState, error) {
	if pool == nil {
		return DatabaseState{}, fmt.Errorf("full-stack PostgreSQL pool is required")
	}
	market := domain.DefaultBTCUSDTMarket()
	state := DatabaseState{
		BuyerBalances:  map[string]BalanceEvidence{},
		SellerBalances: map[string]BalanceEvidence{},
		PlatformFees:   map[string]string{},
		Orders:         map[string]OrderEvidence{},
		JournalSums:    map[string]string{},
	}
	if err := pool.QueryRow(ctx, `
		SELECT market.current_sequence, snapshot.sequence,
		       head.state_hash, snapshot.state_hash,
		       COALESCE(snapshot_event.state_hash, '')
		FROM trading_market AS market
		JOIN trading_snapshot AS snapshot USING (market_id)
		JOIN trading_event_batch AS head
		  ON head.market_id=market.market_id
		 AND head.sequence=market.current_sequence
		LEFT JOIN trading_event_batch AS snapshot_event
		  ON snapshot_event.market_id=snapshot.market_id
		 AND snapshot_event.sequence=snapshot.sequence
		WHERE market.market_id=$1
	`, MarketID).Scan(&state.Sequence, &state.SnapshotSequence, &state.EventHash, &state.SnapshotHash, &state.SnapshotEventHash); err != nil {
		return DatabaseState{}, fmt.Errorf("audit trading head/snapshot: %w", err)
	}
	if state.Sequence < state.SnapshotSequence || state.EventHash == "" ||
		state.SnapshotSequence == 0 || state.SnapshotEventHash == "" ||
		state.SnapshotHash != state.SnapshotEventHash {
		return DatabaseState{}, fmt.Errorf("audit trading snapshot is not a valid prefix of event head")
	}
	countQueries := []struct {
		query string
		dest  *uint64
	}{
		{`SELECT count(*) FROM trading_event_batch WHERE market_id=$1`, &state.Counts.Facts},
		{`SELECT count(*) FROM trading_trade WHERE market_id=$1`, &state.Counts.Trades},
		{`SELECT count(DISTINCT transaction_id) FROM trading_ledger_entry WHERE market_id=$1`, &state.Counts.LedgerTransactions},
		{`SELECT count(*) FROM trading_ledger_entry WHERE market_id=$1`, &state.Counts.LedgerEntries},
		{`SELECT count(*) FROM trading_order WHERE market_id=$1`, &state.Counts.Orders},
	}
	for _, item := range countQueries {
		if err := pool.QueryRow(ctx, item.query, MarketID).Scan(item.dest); err != nil {
			return DatabaseState{}, fmt.Errorf("audit trading count: %w", err)
		}
	}
	if err := auditBalances(ctx, pool, market, &state); err != nil {
		return DatabaseState{}, err
	}
	if err := auditOrders(ctx, pool, market, &state); err != nil {
		return DatabaseState{}, err
	}
	if err := auditTrades(ctx, pool, market, &state); err != nil {
		return DatabaseState{}, err
	}
	if err := auditLedger(ctx, pool, market, &state); err != nil {
		return DatabaseState{}, err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return DatabaseState{}, fmt.Errorf("marshal audited database state: %w", err)
	}
	digest := sha256.Sum256(payload)
	state.Digest = hex.EncodeToString(digest[:])
	return state, nil
}

func auditBalances(ctx context.Context, pool *pgxpool.Pool, market domain.Market, state *DatabaseState) error {
	rows, err := pool.Query(ctx, `
		SELECT account_id, asset, available, held
		FROM trading_balance
		WHERE market_id=$1 AND account_id IN ($2,$3)
		ORDER BY account_id, asset
	`, MarketID, BuyerAccount, SellerAccount)
	if err != nil {
		return fmt.Errorf("query trading balances: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var account, asset string
		var available, held int64
		if err := rows.Scan(&account, &asset, &available, &held); err != nil {
			return fmt.Errorf("scan trading balance: %w", err)
		}
		availableText, formatErr := formatAmount(market, asset, available)
		if formatErr != nil {
			return fmt.Errorf("format available balance: %w", formatErr)
		}
		heldText, formatErr := formatAmount(market, asset, held)
		if formatErr != nil {
			return fmt.Errorf("format held balance: %w", formatErr)
		}
		formatted := BalanceEvidence{Available: availableText, Held: heldText}
		if account == BuyerAccount {
			state.BuyerBalances[asset] = formatted
		} else {
			state.SellerBalances[asset] = formatted
		}
	}
	return rows.Err()
}

func auditOrders(ctx context.Context, pool *pgxpool.Pool, market domain.Market, state *DatabaseState) error {
	rows, err := pool.Query(ctx, `SELECT payload FROM trading_order WHERE market_id=$1 ORDER BY order_id`, MarketID)
	if err != nil {
		return fmt.Errorf("query trading orders: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		var order domain.Order
		if err := rows.Scan(&payload); err != nil {
			return fmt.Errorf("scan trading order: %w", err)
		}
		if err := json.Unmarshal(payload, &order); err != nil {
			return fmt.Errorf("decode trading order: %w", err)
		}
		evidence, formatErr := orderEvidence(market, order)
		if formatErr != nil {
			return fmt.Errorf("format trading order: %w", formatErr)
		}
		state.Orders[order.ClientOrderID] = evidence
	}
	return rows.Err()
}

func queryOrderEvidence(ctx context.Context, pool *pgxpool.Pool, clientOrderID string) (OrderEvidence, bool, error) {
	var payload []byte
	err := pool.QueryRow(ctx, `
		SELECT payload FROM trading_order
		WHERE market_id=$1 AND account_id=$2 AND payload->>'client_order_id'=$3
	`, MarketID, BuyerAccount, clientOrderID).Scan(&payload)
	if err == pgx.ErrNoRows {
		return OrderEvidence{}, false, nil
	}
	if err != nil {
		return OrderEvidence{}, false, fmt.Errorf("query exact trading order: %w", err)
	}
	var order domain.Order
	if err := json.Unmarshal(payload, &order); err != nil {
		return OrderEvidence{}, false, fmt.Errorf("decode exact trading order: %w", err)
	}
	evidence, err := orderEvidence(domain.DefaultBTCUSDTMarket(), order)
	return evidence, err == nil, err
}

func orderEvidence(market domain.Market, order domain.Order) (OrderEvidence, error) {
	price, err := formatSignedDecimal(order.Price, market.QuoteScale)
	if err != nil {
		return OrderEvidence{}, err
	}
	original, err := formatSignedDecimal(order.OriginalQuantity, market.BaseScale)
	if err != nil {
		return OrderEvidence{}, err
	}
	filled, err := formatSignedDecimal(order.FilledQuantity, market.BaseScale)
	if err != nil {
		return OrderEvidence{}, err
	}
	remaining, err := formatSignedDecimal(order.RemainingQuantity, market.BaseScale)
	if err != nil {
		return OrderEvidence{}, err
	}
	held, err := formatAmount(market, string(order.HeldAsset), order.HeldAmount)
	if err != nil {
		return OrderEvidence{}, err
	}
	return OrderEvidence{
		ClientOrderID: order.ClientOrderID, OrderID: string(order.ID), Status: order.Status.String(),
		Side: order.Side.String(), Type: order.Type.String(), TimeInForce: order.TimeInForce.String(), PostOnly: order.PostOnly,
		Price: price, OriginalQuantity: original, FilledQuantity: filled, RemainingQuantity: remaining,
		HeldAsset: string(order.HeldAsset), HeldAmount: held,
	}, nil
}

func auditTrades(ctx context.Context, pool *pgxpool.Pool, market domain.Market, state *DatabaseState) error {
	rows, err := pool.Query(ctx, `SELECT sequence,payload FROM trading_trade WHERE market_id=$1 ORDER BY sequence,trade_id`, MarketID)
	if err != nil {
		return fmt.Errorf("query trading trades: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence uint64
		var payload []byte
		var trade domain.Trade
		if err := rows.Scan(&sequence, &payload); err != nil {
			return fmt.Errorf("scan trading trade: %w", err)
		}
		if err := json.Unmarshal(payload, &trade); err != nil {
			return fmt.Errorf("decode trading trade: %w", err)
		}
		buyerOrder, sellerOrder := trade.TakerOrderID, trade.MakerOrderID
		if trade.MakerAccountID == trade.BuyerAccountID {
			buyerOrder, sellerOrder = trade.MakerOrderID, trade.TakerOrderID
		}
		price, formatErr := formatSignedDecimal(trade.Price, market.QuoteScale)
		if formatErr != nil {
			return fmt.Errorf("format trade price: %w", formatErr)
		}
		quantity, formatErr := formatSignedDecimal(trade.Quantity, market.BaseScale)
		if formatErr != nil {
			return fmt.Errorf("format trade quantity: %w", formatErr)
		}
		quote, formatErr := formatSignedDecimal(trade.QuoteAmount, market.QuoteScale)
		if formatErr != nil {
			return fmt.Errorf("format trade quote: %w", formatErr)
		}
		buyerFee, formatErr := formatAmount(market, string(trade.BuyerFee.Asset), trade.BuyerFee.Amount)
		if formatErr != nil {
			return fmt.Errorf("format buyer fee: %w", formatErr)
		}
		sellerFee, formatErr := formatAmount(market, string(trade.SellerFee.Asset), trade.SellerFee.Amount)
		if formatErr != nil {
			return fmt.Errorf("format seller fee: %w", formatErr)
		}
		state.Trades = append(state.Trades, TradeEvidence{
			TradeID: string(trade.ID), Sequence: sequence, Price: price, Quantity: quantity, QuoteAmount: quote,
			BuyerOrderID: string(buyerOrder), SellerOrderID: string(sellerOrder),
			MakerOrderID: string(trade.MakerOrderID), TakerOrderID: string(trade.TakerOrderID),
			BuyerFeeAsset: string(trade.BuyerFee.Asset), BuyerFeeAmount: buyerFee,
			BuyerFeeRateBPS: trade.BuyerFee.RateBPS, BuyerFeeRole: trade.BuyerFee.Role.String(),
			SellerFeeAsset: string(trade.SellerFee.Asset), SellerFeeAmount: sellerFee,
			SellerFeeRateBPS: trade.SellerFee.RateBPS, SellerFeeRole: trade.SellerFee.Role.String(),
		})
	}
	return rows.Err()
}

func auditLedger(ctx context.Context, pool *pgxpool.Pool, market domain.Market, state *DatabaseState) error {
	rows, err := pool.Query(ctx, `
		SELECT sequence,transaction_id,entry_index,account,asset,amount,reference
		FROM trading_ledger_entry WHERE market_id=$1
		ORDER BY sequence,transaction_id,entry_index
	`, MarketID)
	if err != nil {
		return fmt.Errorf("query trading ledger: %w", err)
	}
	defer rows.Close()
	var current *LedgerTransactionEvidence
	sums := map[string]int64{}
	seen := map[string]struct{}{}
	tradeIDs := map[string]struct{}{}
	orderIDs := map[string]struct{}{}
	for _, trade := range state.Trades {
		tradeIDs[trade.TradeID] = struct{}{}
	}
	for _, order := range state.Orders {
		orderIDs[order.OrderID] = struct{}{}
	}
	for rows.Next() {
		var sequence uint64
		var transactionID, account, asset, reference string
		var index uint32
		var amount int64
		if err := rows.Scan(&sequence, &transactionID, &index, &account, &asset, &amount, &reference); err != nil {
			return fmt.Errorf("scan trading ledger: %w", err)
		}
		if current == nil || current.TransactionID != transactionID {
			if _, exists := seen[transactionID]; exists {
				state.DuplicateTransactions = true
			}
			seen[transactionID] = struct{}{}
			state.LedgerTransactions = append(state.LedgerTransactions, LedgerTransactionEvidence{TransactionID: transactionID, Sequence: sequence, Reference: reference})
			current = &state.LedgerTransactions[len(state.LedgerTransactions)-1]
		}
		if current.Sequence != sequence || current.Reference != reference {
			state.ReferenceMismatch = true
		}
		formattedAmount, formatErr := formatAmount(market, asset, amount)
		if formatErr != nil {
			return fmt.Errorf("format trading ledger entry: %w", formatErr)
		}
		current.Entries = append(current.Entries, LedgerEntryEvidence{Index: index, Account: account, Asset: asset, Amount: formattedAmount})
		next, addErr := domain.CheckedAdd(sums[asset], amount)
		if addErr != nil {
			return fmt.Errorf("sum trading ledger %s: %w", asset, addErr)
		}
		sums[asset] = next
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, transaction := range state.LedgerTransactions {
		if !validReference(transaction.Reference, tradeIDs, orderIDs) {
			state.ReferenceMismatch = true
		}
	}
	assets := make([]string, 0, len(sums))
	for asset := range sums {
		assets = append(assets, asset)
	}
	sort.Strings(assets)
	for _, asset := range assets {
		formatted, formatErr := formatAmount(market, asset, sums[asset])
		if formatErr != nil {
			return fmt.Errorf("format journal sum: %w", formatErr)
		}
		state.JournalSums[asset] = formatted
	}
	for _, asset := range []string{"BTC", "USDT"} {
		var amount int64
		if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM trading_ledger_entry WHERE market_id=$1 AND account=$2 AND asset=$3`, MarketID, "platform:fee:"+asset, asset).Scan(&amount); err != nil && err != pgx.ErrNoRows {
			return fmt.Errorf("query platform fee %s: %w", asset, err)
		}
		formatted, formatErr := formatAmount(market, asset, amount)
		if formatErr != nil {
			return fmt.Errorf("format platform fee: %w", formatErr)
		}
		state.PlatformFees[asset] = formatted
	}
	return nil
}

func validReference(reference string, trades, orders map[string]struct{}) bool {
	for prefix, authority := range map[string]map[string]struct{}{"matched-trade:": trades, "order-hold:": orders, "order-release:": orders, "order-cancel:": orders, "maker-rounding-release:": orders} {
		if strings.HasPrefix(reference, prefix) {
			_, ok := authority[strings.TrimPrefix(reference, prefix)]
			return ok
		}
	}
	return strings.HasPrefix(reference, "virtual-funding:")
}

func formatAmount(market domain.Market, asset string, amount int64) (string, error) {
	scale := market.QuoteScale
	if asset == string(market.BaseAsset) || asset == "" {
		scale = market.BaseScale
	}
	return formatSignedDecimal(amount, scale)
}

func formatSignedDecimal(atoms, scale int64) (string, error) {
	if atoms >= 0 {
		return decimal.Format(atoms, scale)
	}
	if scale <= 0 {
		return "", fmt.Errorf("scale must be a positive power of ten")
	}
	precision := 0
	for remaining := scale; remaining > 1; remaining /= 10 {
		if remaining%10 != 0 {
			return "", fmt.Errorf("scale must be a positive power of ten")
		}
		precision++
	}
	magnitude := uint64(-(atoms + 1)) + 1
	whole := magnitude / uint64(scale)
	fraction := magnitude % uint64(scale)
	if fraction == 0 {
		return "-" + strconv.FormatUint(whole, 10), nil
	}
	formatted := fmt.Sprintf("%d.%0*d", whole, precision, fraction)
	return "-" + strings.TrimRight(formatted, "0"), nil
}
