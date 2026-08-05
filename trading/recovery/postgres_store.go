package recovery

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/the-web3/s78-market-services/trading/domain"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("recovery PostgreSQL pool is required")
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Load(
	ctx context.Context,
	marketID domain.MarketID,
) (Status, bool, error) {
	var (
		status      Status
		version     int64
		seq         int64
		firstSample *time.Time
		lastSample  *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT epoch.schema_version, epoch.market_id, epoch.epoch_id,
		       epoch.phase, epoch.runtime_sequence, epoch.state_hash,
		       epoch.ledger_balanced, epoch.event_continuous,
		       epoch.projection_caught_up, epoch.outbox_caught_up,
		       epoch.transport_healthy, epoch.writes_enabled,
		       epoch.transport_sample_count, epoch.transport_first_sample_at,
		       epoch.transport_last_sample_at, epoch.transport_maximum_gap_ms,
		       epoch.transport_evidence_sha256,
		       epoch.production_origin, epoch.deployment_id, epoch.deployment_url,
		       epoch.release_commit, epoch.source_digest,
		       epoch.last_error, epoch.version, epoch.started_at, epoch.updated_at
		FROM trading_recovery_current current
		JOIN trading_recovery_epoch epoch
		  ON epoch.market_id=current.market_id AND epoch.epoch_id=current.epoch_id
		WHERE current.market_id=$1
	`, marketID).Scan(
		&status.SchemaVersion,
		&status.MarketID,
		&status.EpochID,
		&status.Phase,
		&seq,
		&status.Proof.StateHash,
		&status.Proof.LedgerBalanced,
		&status.Proof.EventContinuous,
		&status.Proof.ProjectionCaughtUp,
		&status.Proof.OutboxCaughtUp,
		&status.Proof.TransportHealthy,
		&status.WritesEnabled,
		&status.Transport.SampleCount,
		&firstSample,
		&lastSample,
		&status.Transport.MaximumGapMS,
		&status.Transport.EvidenceSHA256,
		&status.Provenance.ProductionOrigin,
		&status.Provenance.DeploymentID,
		&status.Provenance.DeploymentURL,
		&status.Provenance.ReleaseCommit,
		&status.Provenance.SourceDigest,
		&status.LastError,
		&version,
		&status.StartedAt,
		&status.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{}, false, nil
	}
	if err != nil {
		return Status{}, false, fmt.Errorf("load trading recovery state: %w", err)
	}
	if version < 0 || seq < 0 {
		return Status{}, false, fmt.Errorf("trading recovery state contains a negative counter")
	}
	status.Version = uint64(version)
	status.Proof.RuntimeSequence = uint64(seq)
	if firstSample != nil {
		status.Transport.FirstSampleAt = *firstSample
	}
	if lastSample != nil {
		status.Transport.LastSampleAt = *lastSample
	}
	return status, true, nil
}

func (s *PostgresStore) Save(
	ctx context.Context,
	expectedVersion uint64,
	next Status,
) error {
	if expectedVersion > math.MaxInt64 || next.Version > math.MaxInt64 ||
		next.Proof.RuntimeSequence > math.MaxInt64 {
		return fmt.Errorf("trading recovery counter exceeds PostgreSQL bigint")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin trading recovery state update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		currentEpoch   string
		currentVersion int64
	)
	err = tx.QueryRow(ctx, `
		SELECT current.epoch_id, epoch.version
		FROM trading_recovery_current current
		JOIN trading_recovery_epoch epoch
		  ON epoch.market_id=current.market_id AND epoch.epoch_id=current.epoch_id
		WHERE current.market_id=$1
		FOR UPDATE OF current
	`, next.MarketID).Scan(&currentEpoch, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		if expectedVersion != 0 {
			return ErrVersionConflict
		}
		if err := insertEpochRow(ctx, tx, next); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading_recovery_current (market_id, epoch_id, updated_at)
			VALUES ($1,$2,$3)
		`, next.MarketID, next.EpochID, next.UpdatedAt); err != nil {
			return fmt.Errorf("select current trading recovery epoch: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit initial trading recovery epoch: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock current trading recovery epoch: %w", err)
	}
	if currentVersion < 0 || uint64(currentVersion) != expectedVersion ||
		next.Version != expectedVersion+1 {
		return ErrVersionConflict
	}
	if currentEpoch != next.EpochID {
		if err := insertEpochRow(ctx, tx, next); err != nil {
			return err
		}
		tag, updateErr := tx.Exec(ctx, `
			UPDATE trading_recovery_current
			SET epoch_id=$2, updated_at=$3
			WHERE market_id=$1 AND epoch_id=$4
		`, next.MarketID, next.EpochID, next.UpdatedAt, currentEpoch)
		if updateErr != nil {
			return fmt.Errorf("switch current trading recovery epoch: %w", updateErr)
		}
		if tag.RowsAffected() != 1 {
			return ErrVersionConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit new trading recovery epoch: %w", err)
		}
		return nil
	}

	tag, err := tx.Exec(ctx, `
		UPDATE trading_recovery_epoch epoch
		SET phase=$4,
		    runtime_sequence=$5,
		    state_hash=$6,
		    ledger_balanced=$7,
		    event_continuous=$8,
		    projection_caught_up=$9,
		    outbox_caught_up=$10,
		    transport_healthy=$11,
		    writes_enabled=$12,
		    transport_sample_count=$13,
		    transport_first_sample_at=$14,
		    transport_last_sample_at=$15,
		    transport_maximum_gap_ms=$16,
		    transport_evidence_sha256=$17,
		    production_origin=$18,
		    deployment_id=$19,
		    deployment_url=$20,
		    release_commit=$21,
		    source_digest=$22,
		    last_error=$23,
		    version=$24,
		    updated_at=$25
		FROM trading_recovery_current current
		WHERE epoch.market_id=$1 AND epoch.epoch_id=$2 AND epoch.version=$3
		  AND current.market_id=epoch.market_id AND current.epoch_id=epoch.epoch_id
	`,
		next.MarketID,
		next.EpochID,
		int64(expectedVersion),
		next.Phase,
		int64(next.Proof.RuntimeSequence),
		next.Proof.StateHash,
		next.Proof.LedgerBalanced,
		next.Proof.EventContinuous,
		next.Proof.ProjectionCaughtUp,
		next.Proof.OutboxCaughtUp,
		next.Proof.TransportHealthy,
		next.WritesEnabled,
		next.Transport.SampleCount,
		nullTime(next.Transport.FirstSampleAt),
		nullTime(next.Transport.LastSampleAt),
		next.Transport.MaximumGapMS,
		next.Transport.EvidenceSHA256,
		next.Provenance.ProductionOrigin,
		next.Provenance.DeploymentID,
		next.Provenance.DeploymentURL,
		next.Provenance.ReleaseCommit,
		next.Provenance.SourceDigest,
		next.LastError,
		int64(next.Version),
		next.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update trading recovery epoch: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrVersionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit trading recovery update: %w", err)
	}
	return nil
}

func insertEpochRow(ctx context.Context, tx pgx.Tx, next Status) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO trading_recovery_epoch (
			schema_version, market_id, epoch_id, phase, runtime_sequence,
			state_hash, ledger_balanced, event_continuous,
			projection_caught_up, outbox_caught_up, transport_healthy,
			writes_enabled, transport_sample_count, transport_first_sample_at,
			transport_last_sample_at, transport_maximum_gap_ms,
			transport_evidence_sha256, production_origin, deployment_id, deployment_url,
			release_commit, source_digest, last_error, version, started_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)
	`,
		next.SchemaVersion,
		next.MarketID,
		next.EpochID,
		next.Phase,
		int64(next.Proof.RuntimeSequence),
		next.Proof.StateHash,
		next.Proof.LedgerBalanced,
		next.Proof.EventContinuous,
		next.Proof.ProjectionCaughtUp,
		next.Proof.OutboxCaughtUp,
		next.Proof.TransportHealthy,
		next.WritesEnabled,
		next.Transport.SampleCount,
		nullTime(next.Transport.FirstSampleAt),
		nullTime(next.Transport.LastSampleAt),
		next.Transport.MaximumGapMS,
		next.Transport.EvidenceSHA256,
		next.Provenance.ProductionOrigin,
		next.Provenance.DeploymentID,
		next.Provenance.DeploymentURL,
		next.Provenance.ReleaseCommit,
		next.Provenance.SourceDigest,
		next.LastError,
		int64(next.Version),
		next.StartedAt,
		next.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert trading recovery epoch: %w", err)
	}
	return nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
