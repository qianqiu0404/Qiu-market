package reference

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/the-web3/s78-market-services/trading/marketmaker"
)

const bitcoinExternalID = "bitcoin"

const referenceQueryTimeout = 2 * time.Second

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// PostgresSource reads only S78's canonical BTC composite index. The index is
// an audited USD price built from fresh CEX Spot contributors and stable-quote
// conversion; the virtual BTC/USDT market treats those USD quote atoms as its
// reference, never as an executable venue price.
type PostgresSource struct {
	queryer      rowQuerier
	quoteScale   int64
	queryTimeout time.Duration
}

func NewPostgresSource(pool *pgxpool.Pool, quoteScale int64) (*PostgresSource, error) {
	if pool == nil || quoteScale <= 0 {
		return nil, fmt.Errorf("PostgreSQL pool and positive quote scale are required")
	}
	return newPostgresSource(pool, quoteScale, referenceQueryTimeout)
}

func newPostgresSource(queryer rowQuerier, quoteScale int64, timeout time.Duration) (*PostgresSource, error) {
	if queryer == nil || quoteScale <= 0 || timeout <= 0 {
		return nil, fmt.Errorf("PostgreSQL queryer, positive quote scale and timeout are required")
	}
	return &PostgresSource{queryer: queryer, quoteScale: quoteScale, queryTimeout: timeout}, nil
}

func (s *PostgresSource) Current(ctx context.Context) (marketmaker.Reference, error) {
	queryContext, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()
	var (
		priceText  string
		observedAt time.Time
	)
	err := s.queryer.QueryRow(queryContext, `
		SELECT index.price_usd::text, index.observed_at
		FROM asset_price_index AS index
		JOIN asset_external_mapping AS mapping
		  ON mapping.asset_guid = index.asset_guid
		WHERE mapping.provider = 'coingecko'
		  AND mapping.external_id = $1
		  AND index.available = TRUE
		  AND index.price_usd > 0
		ORDER BY index.observed_at DESC
		LIMIT 1
	`, bitcoinExternalID).Scan(&priceText, &observedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return marketmaker.Reference{}, fmt.Errorf("fresh BTC composite reference is unavailable")
	}
	if err != nil {
		return marketmaker.Reference{}, fmt.Errorf("query BTC composite reference: %w", err)
	}
	atoms, err := decimalAtoms(priceText, s.quoteScale)
	if err != nil {
		return marketmaker.Reference{}, fmt.Errorf("convert BTC composite reference: %w", err)
	}
	return marketmaker.Reference{
		Price:      atoms,
		ObservedAt: observedAt.UTC(),
	}, nil
}

func decimalAtoms(value string, scale int64) (int64, error) {
	price, err := decimal.NewFromString(value)
	if err != nil || price.LessThanOrEqual(decimal.Zero) {
		return 0, fmt.Errorf("invalid positive decimal %q", value)
	}
	scaled := price.Mul(decimal.NewFromInt(scale)).Truncate(0)
	if scaled.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, fmt.Errorf("decimal %q exceeds int64 atoms", value)
	}
	atoms, err := strconv.ParseInt(scaled.StringFixed(0), 10, 64)
	if err != nil || atoms <= 0 {
		return 0, fmt.Errorf("invalid atom value for %q", value)
	}
	return atoms, nil
}
