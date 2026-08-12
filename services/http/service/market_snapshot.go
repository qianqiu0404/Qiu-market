package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/shopspring/decimal"

	"github.com/the-web3/s78-market-services/database"
	qredis "github.com/the-web3/s78-market-services/redis"
)

const (
	MarketSnapshotSchema       = "qiu.market-snapshot.v1"
	marketSnapshotTTL          = 5 * time.Minute
	marketSnapshotCurrentFor   = 15 * time.Second
	marketSnapshotMaximumItems = 64
	marketSnapshotMinimumRows  = 1
	marketSnapshotMaximumRows  = 200
)

var (
	ErrMarketSnapshotExpired     = errors.New("market snapshot is unknown or expired")
	ErrMarketSnapshotVenue       = errors.New("market snapshot venue mismatch")
	ErrMarketSnapshotUnavailable = errors.New("market snapshot authority unavailable")
	ErrMarketSnapshotInvalid     = errors.New("market snapshot authority returned invalid data")
)

type MarketSnapshotContract struct {
	ReleaseCommit  string `json:"release_commit"`
	DataMode       string `json:"data_mode"`
	ProviderPolicy string `json:"provider_policy"`
	ContractSchema string `json:"contract_schema"`
	SnapshotSchema string `json:"snapshot_schema"`
}

type marketSnapshotSource interface {
	QueryMarketReadSnapshot(venue string) (*database.MarketReadSnapshot, error)
}

type marketSnapshot struct {
	ID        string                       `json:"id"`
	Venue     string                       `json:"venue"`
	CreatedAt time.Time                    `json:"created_at"`
	ExpiresAt time.Time                    `json:"expires_at"`
	Schema    string                       `json:"schema"`
	Contract  MarketSnapshotContract       `json:"contract"`
	Read      *database.MarketReadSnapshot `json:"read"`
}

type marketSnapshotStore struct {
	source    marketSnapshotSource
	redis     *qredis.Client
	contract  MarketSnapshotContract
	namespace string
	now       func() time.Time
}

func newMarketSnapshotStore(
	source marketSnapshotSource,
	redisClient *qredis.Client,
	contract MarketSnapshotContract,
) *marketSnapshotStore {
	contract.SnapshotSchema = MarketSnapshotSchema
	encodedContract, _ := json.Marshal(contract)
	contractDigest := sha256.Sum256(encodedContract)
	return &marketSnapshotStore{
		source:    source,
		redis:     redisClient,
		contract:  contract,
		namespace: fmt.Sprintf("qiu:market-read:%x", contractDigest[:12]),
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (s *marketSnapshotStore) resolve(venue, requestedID string) (*marketSnapshot, error) {
	started := time.Now()
	venue = strings.ToLower(strings.TrimSpace(venue))
	if s.redis == nil {
		return nil, ErrMarketSnapshotUnavailable
	}
	if requestedID != "" {
		redisStarted := time.Now()
		entry, err := s.read(strings.TrimSpace(requestedID), venue)
		log.Info("Market snapshot read timing", "venue", venue, "source", "redis_explicit",
			"snapshot_id", strings.TrimSpace(requestedID), "redis_read_ms", time.Since(redisStarted).Milliseconds(),
			"postgres_build_ms", 0, "redis_store_ms", 0, "total_ms", time.Since(started).Milliseconds())
		return entry, err
	}

	// The 15-second bucket is the current pointer. Every API instance derives
	// the same opaque ID and races a single Redis SET NX, so there is no second
	// pointer key that can survive a partial write or select another live read.
	now := s.now().UTC()
	bucket := now.Truncate(marketSnapshotCurrentFor)
	id := s.bucketID(venue, bucket)
	redisStarted := time.Now()
	if existing, err := s.read(id, venue); err == nil {
		log.Info("Market snapshot read timing", "venue", venue, "source", "redis_current",
			"snapshot_id", id, "redis_read_ms", time.Since(redisStarted).Milliseconds(),
			"postgres_build_ms", 0, "redis_store_ms", 0, "total_ms", time.Since(started).Milliseconds())
		return existing, nil
	} else if !errors.Is(err, ErrMarketSnapshotExpired) {
		return nil, err
	}

	redisReadDuration := time.Since(redisStarted)
	postgresStarted := time.Now()
	read, err := s.source.QueryMarketReadSnapshot(venue)
	postgresDuration := time.Since(postgresStarted)
	if err != nil {
		log.Error("Market snapshot read timing", "venue", venue, "source", "postgres_error",
			"snapshot_id", id, "redis_read_ms", redisReadDuration.Milliseconds(),
			"postgres_build_ms", postgresDuration.Milliseconds(), "redis_store_ms", 0,
			"total_ms", time.Since(started).Milliseconds(), "error_class", "snapshot_build")
		return nil, err
	}
	entry := &marketSnapshot{
		ID: id, Venue: venue, CreatedAt: now, ExpiresAt: now.Add(marketSnapshotTTL),
		Schema: MarketSnapshotSchema, Contract: s.contract, Read: read,
	}
	if err := s.validate(entry, venue, now); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("%w: encode snapshot: %v", ErrMarketSnapshotInvalid, err)
	}
	storeStarted := time.Now()
	created, err := s.store(id, payload, now)
	storeDuration := time.Since(storeStarted)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMarketSnapshotUnavailable, err)
	}
	if created {
		log.Info("Market snapshot read timing", "venue", venue, "source", "postgres_winner",
			"snapshot_id", id, "redis_read_ms", redisReadDuration.Milliseconds(),
			"postgres_build_ms", postgresDuration.Milliseconds(), "redis_store_ms", storeDuration.Milliseconds(),
			"total_ms", time.Since(started).Milliseconds())
		return entry, nil
	}
	// Another instance won this bucket. Redis is the authority: discard our
	// database read and return the winner's complete immutable value.
	winnerStarted := time.Now()
	winner, winnerErr := s.read(id, venue)
	log.Info("Market snapshot read timing", "venue", venue, "source", "redis_winner",
		"snapshot_id", id, "redis_read_ms", (redisReadDuration + time.Since(winnerStarted)).Milliseconds(),
		"postgres_build_ms", postgresDuration.Milliseconds(), "redis_store_ms", storeDuration.Milliseconds(),
		"total_ms", time.Since(started).Milliseconds())
	return winner, winnerErr
}

func (s *marketSnapshotStore) bucketID(venue string, bucket time.Time) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d", s.namespace, venue, bucket.Unix())))
	return "snp_" + hex.EncodeToString(digest[:16])
}

func (s *marketSnapshotStore) snapshotKey(id string) string {
	return s.namespace + ":snapshot:" + id
}

func (s *marketSnapshotStore) store(id string, payload []byte, now time.Time) (bool, error) {
	const atomicStore = `
local created = redis.call("SET", KEYS[1], ARGV[1], "NX", "PX", ARGV[2])
if not created then
  return 0
end
redis.call("ZADD", KEYS[2], ARGV[3], KEYS[1])
local excess = redis.call("ZCARD", KEYS[2]) - tonumber(ARGV[4])
if excess > 0 then
  local victims = redis.call("ZRANGE", KEYS[2], 0, excess - 1)
  for _, key in ipairs(victims) do
    redis.call("DEL", key)
    redis.call("ZREM", KEYS[2], key)
  end
end
redis.call("PEXPIRE", KEYS[2], ARGV[5])
return 1
`
	result, err := s.redis.Eval(
		context.Background(),
		atomicStore,
		[]string{s.snapshotKey(id), s.namespace + ":index"},
		payload,
		marketSnapshotTTL.Milliseconds(),
		now.UnixMilli(),
		marketSnapshotMaximumItems,
		(marketSnapshotTTL + marketSnapshotCurrentFor).Milliseconds(),
	)
	if err != nil {
		return false, err
	}
	created, ok := result.(int64)
	return ok && created == 1, nil
}

func (s *marketSnapshotStore) read(id, venue string) (*marketSnapshot, error) {
	payload, err := s.redis.Get(context.Background(), s.snapshotKey(id))
	if qredis.IsNotFound(err) {
		return nil, ErrMarketSnapshotExpired
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMarketSnapshotUnavailable, err)
	}
	var entry marketSnapshot
	if err := json.Unmarshal([]byte(payload), &entry); err != nil {
		return nil, fmt.Errorf("%w: decode snapshot: %v", ErrMarketSnapshotInvalid, err)
	}
	if entry.ID != id {
		return nil, fmt.Errorf("%w: snapshot id mismatch", ErrMarketSnapshotInvalid)
	}
	if entry.Venue != venue {
		return nil, ErrMarketSnapshotVenue
	}
	if err := s.validate(&entry, venue, s.now().UTC()); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (s *marketSnapshotStore) validate(entry *marketSnapshot, venue string, now time.Time) error {
	if entry == nil || entry.Read == nil || entry.Schema != MarketSnapshotSchema || entry.Contract != s.contract {
		return fmt.Errorf("%w: schema or contract mismatch", ErrMarketSnapshotInvalid)
	}
	if entry.Venue != venue {
		return ErrMarketSnapshotVenue
	}
	if entry.CreatedAt.IsZero() || entry.Read.AsOf.IsZero() || entry.Read.AsOf.After(entry.ExpiresAt) ||
		!entry.ExpiresAt.After(now) || entry.ExpiresAt.After(entry.CreatedAt.Add(marketSnapshotTTL)) {
		return ErrMarketSnapshotExpired
	}
	read := entry.Read
	if read.Total < marketSnapshotMinimumRows || read.Total > marketSnapshotMaximumRows ||
		int64(len(read.Rows)) != read.Total {
		return fmt.Errorf("%w: snapshot row bound", ErrMarketSnapshotInvalid)
	}
	seen := make(map[string]struct{}, len(read.Rows))
	var fresh, stale, unavailable int64
	for _, row := range read.Rows {
		assetID := strings.TrimSpace(row.AssetID)
		if assetID == "" {
			return fmt.Errorf("%w: empty asset id", ErrMarketSnapshotInvalid)
		}
		if _, exists := seen[assetID]; exists {
			return fmt.Errorf("%w: duplicate asset id", ErrMarketSnapshotInvalid)
		}
		seen[assetID] = struct{}{}
		switch row.FreshnessStatus {
		case "fresh":
			fresh++
		case "stale":
			stale++
		case "unavailable":
			unavailable++
		default:
			return fmt.Errorf("%w: unknown freshness state", ErrMarketSnapshotInvalid)
		}
	}
	if fresh != read.FreshAssetCount || stale != read.StaleAssetCount ||
		unavailable != read.UnavailableAssetCount || fresh+stale+unavailable != read.Total ||
		read.Summary.AssetCount != read.Total ||
		read.Summary.DisplayedAssetCount+read.Summary.UnpricedAssetCount != read.Total ||
		read.Summary.PricedAssetCount != read.Summary.DisplayedAssetCount {
		return fmt.Errorf("%w: snapshot count invariant", ErrMarketSnapshotInvalid)
	}
	if venue == "all" {
		priced := fresh + stale
		if read.Summary.PricedAssetCount != priced ||
			read.Summary.UnpricedAssetCount != unavailable ||
			read.Summary.SingleVenuePricedAssetCount+
				read.Summary.MultiVenuePricedAssetCount != priced {
			return fmt.Errorf("%w: all-market count invariant", ErrMarketSnapshotInvalid)
		}
	}
	return nil
}

func snapshotDashboardPage(
	snapshot *marketSnapshot,
	query database.AssetIndexDashboardQuery,
) ([]database.AssetIndexDashboardRow, int64) {
	rows := append([]database.AssetIndexDashboardRow(nil), snapshot.Read.Rows...)
	search := strings.ToLower(strings.TrimSpace(query.Search))
	filtered := rows[:0]
	for _, row := range rows {
		if query.Venue != "all" && !query.IncludeUncovered && !row.Published {
			continue
		}
		if search != "" &&
			!strings.Contains(strings.ToLower(row.AssetID), search) &&
			!strings.Contains(strings.ToLower(row.AssetSymbol), search) &&
			!strings.Contains(strings.ToLower(row.AssetName), search) {
			continue
		}
		change := decimalPointer(row.DisplayChange24hPct)
		switch strings.ToLower(strings.TrimSpace(query.Filter)) {
		case "gainers":
			if change == nil || !change.IsPositive() {
				continue
			}
		case "losers":
			if change == nil || !change.IsNegative() {
				continue
			}
		}
		filtered = append(filtered, row)
	}
	rows = filtered
	sortSnapshotRows(rows, query.SortBy, query.SortDirection)
	total := int64(len(rows))
	page, pageSize := query.Page, query.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []database.AssetIndexDashboardRow{}, total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return rows[start:end], total
}

func sortSnapshotRows(rows []database.AssetIndexDashboardRow, sortBy, direction string) {
	desc := !strings.EqualFold(strings.TrimSpace(direction), "asc")
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		var compared int
		switch strings.ToLower(strings.TrimSpace(sortBy)) {
		case "asset", "symbol":
			compared = strings.Compare(strings.ToUpper(a.AssetSymbol), strings.ToUpper(b.AssetSymbol))
			if desc {
				compared = -compared
			}
		case "price":
			compared = compareSnapshotDecimal(a.DisplayPrice, b.DisplayPrice, desc)
		case "change24h":
			compared = compareSnapshotDecimal(a.DisplayChange24hPct, b.DisplayChange24hPct, desc)
		case "volume", "turnover24h":
			var av, bv *string
			if a.FreshnessStatus == "fresh" {
				av = a.Turnover24hUSD
			}
			if b.FreshnessStatus == "fresh" {
				bv = b.Turnover24hUSD
			}
			compared = compareSnapshotDecimal(av, bv, desc)
		case "market_cap":
			compared = compareSnapshotDecimal(a.MarketCapUSD, b.MarketCapUSD, desc)
			if compared == 0 {
				compared = compareSnapshotRank(a.Rank, b.Rank)
			}
		default:
			compared = compareSnapshotRank(a.Rank, b.Rank)
			if compared == 0 {
				compared = compareSnapshotDecimal(a.Turnover24hUSD, b.Turnover24hUSD, true)
			}
		}
		if compared == 0 {
			compared = strings.Compare(a.AssetID, b.AssetID)
		}
		return compared < 0
	})
}

func compareSnapshotRank(a, b *int) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	if *a < *b {
		return -1
	}
	if *a > *b {
		return 1
	}
	return 0
}

func decimalPointer(value *string) *decimal.Decimal {
	if value == nil {
		return nil
	}
	parsed, err := decimal.NewFromString(*value)
	if err != nil {
		return nil
	}
	return &parsed
}

func compareSnapshotDecimal(a, b *string, desc bool) int {
	left, right := decimalPointer(a), decimalPointer(b)
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}
	compared := left.Cmp(*right)
	if desc {
		compared = -compared
	}
	return compared
}
