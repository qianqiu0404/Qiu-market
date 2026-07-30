package reliability

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/exchange"
	"github.com/the-web3/s78-market-services/trading/store"
)

var ErrLedgerProofFailed = errors.New("ledger proof failed")

// LedgerProof summarizes a full immutable event history. AssetNet records the
// signed sum of every journal entry by asset; a valid proof always contains
// zero for every observed asset.
type LedgerProof struct {
	MarketID       domain.MarketID
	FirstSequence  uint64
	FinalSequence  uint64
	FinalStateHash string
	Records        int
	Transactions   int
	Entries        int
	AssetNet       map[domain.Asset]int64
}

// RecoveryProof binds the full-history ledger proof to the state produced by
// the existing exchange restore path.
type RecoveryProof struct {
	Ledger            LedgerProof
	RestoredSequence  uint64
	RestoredStateHash string
}

// AuditRecords proves sequence continuity, event cursor continuity, globally
// unique transaction IDs, per-transaction/per-asset double-entry balance, and
// cumulative asset conservation for a complete event history beginning at
// sequence one.
func AuditRecords(market domain.Market, records []store.Record) (LedgerProof, error) {
	if err := market.Validate(); err != nil {
		return LedgerProof{}, fmt.Errorf("%w: market: %v", ErrLedgerProofFailed, err)
	}

	proof := LedgerProof{
		MarketID: market.ID,
		Records:  len(records),
		AssetNet: make(map[domain.Asset]int64),
	}
	transactionIDs := make(map[string]struct{})
	for recordIndex, record := range records {
		wantSequence := uint64(recordIndex + 1)
		if recordIndex == 0 {
			proof.FirstSequence = wantSequence
		}
		if record.MarketID != market.ID {
			return LedgerProof{}, proofError(
				wantSequence,
				"market id have=%s want=%s",
				record.MarketID,
				market.ID,
			)
		}
		if !supportedRecordSchema(record.SchemaVersion) {
			return LedgerProof{}, proofError(
				wantSequence,
				"unsupported schema version %d",
				record.SchemaVersion,
			)
		}
		if record.Command.Sequence != wantSequence ||
			record.Result.Sequence != wantSequence {
			return LedgerProof{}, proofError(
				wantSequence,
				"sequence command=%d result=%d",
				record.Command.Sequence,
				record.Result.Sequence,
			)
		}
		if record.Command.Fingerprint == "" ||
			record.Command.RequestKey.MarketID != market.ID {
			return LedgerProof{}, proofError(
				wantSequence,
				"command identity or fingerprint is missing",
			)
		}
		if err := record.Command.RequestKey.Validate(); err != nil {
			return LedgerProof{}, proofError(
				wantSequence,
				"invalid request key: %v",
				err,
			)
		}
		for eventIndex, event := range record.Result.Events {
			wantEventIndex := uint32(eventIndex + 1)
			if event.Sequence != wantSequence || event.Index != wantEventIndex {
				return LedgerProof{}, proofError(
					wantSequence,
					"event cursor have=(%d,%d) want=(%d,%d)",
					event.Sequence,
					event.Index,
					wantSequence,
					wantEventIndex,
				)
			}
		}

		for _, transaction := range record.Journal {
			if transaction.ID == "" || transaction.Reference == "" ||
				len(transaction.Entries) < 2 {
				return LedgerProof{}, proofError(
					wantSequence,
					"transaction requires id, reference, and at least two entries",
				)
			}
			if _, duplicate := transactionIDs[transaction.ID]; duplicate {
				return LedgerProof{}, proofError(
					wantSequence,
					"duplicate transaction id %s",
					transaction.ID,
				)
			}
			transactionIDs[transaction.ID] = struct{}{}
			transactionNet := make(map[domain.Asset]int64)
			for _, entry := range transaction.Entries {
				if entry.Account == "" || entry.Asset == "" || entry.Amount == 0 {
					return LedgerProof{}, proofError(
						wantSequence,
						"transaction %s has an invalid entry",
						transaction.ID,
					)
				}
				next, err := domain.CheckedAdd(transactionNet[entry.Asset], entry.Amount)
				if err != nil {
					return LedgerProof{}, proofError(
						wantSequence,
						"transaction %s asset %s overflow: %v",
						transaction.ID,
						entry.Asset,
						err,
					)
				}
				transactionNet[entry.Asset] = next
				total, err := domain.CheckedAdd(proof.AssetNet[entry.Asset], entry.Amount)
				if err != nil {
					return LedgerProof{}, proofError(
						wantSequence,
						"history asset %s overflow: %v",
						entry.Asset,
						err,
					)
				}
				proof.AssetNet[entry.Asset] = total
				proof.Entries++
			}
			for asset, total := range transactionNet {
				if total != 0 {
					return LedgerProof{}, proofError(
						wantSequence,
						"transaction %s asset %s net=%d",
						transaction.ID,
						asset,
						total,
					)
				}
			}
			proof.Transactions++
		}
		if !validStateHash(record.StateHash) {
			return LedgerProof{}, proofError(
				wantSequence,
				"invalid state hash %q",
				record.StateHash,
			)
		}
		proof.FinalSequence = wantSequence
		proof.FinalStateHash = record.StateHash
	}
	for asset, total := range proof.AssetNet {
		if total != 0 {
			return LedgerProof{}, fmt.Errorf(
				"%w: total asset %s net=%d",
				ErrLedgerProofFailed,
				asset,
				total,
			)
		}
	}
	return proof, nil
}

// ProveRecovery audits the complete event history and then delegates state
// reconstruction to exchange.Restore. It does not maintain a second matching,
// balance, or order state.
func ProveRecovery(
	ctx context.Context,
	market domain.Market,
	eventLog store.EventStore,
	snapshots store.SnapshotStore,
) (RecoveryProof, error) {
	if eventLog == nil || snapshots == nil {
		return RecoveryProof{}, exchange.ErrMissingStore
	}
	records, err := eventLog.RecordsAfter(ctx, 0)
	if err != nil {
		return RecoveryProof{}, fmt.Errorf("load full history for proof: %w", err)
	}
	ledgerProof, err := AuditRecords(market, records)
	if err != nil {
		return RecoveryProof{}, err
	}
	restored, err := exchange.Restore(ctx, market, eventLog, snapshots)
	if err != nil {
		return RecoveryProof{}, fmt.Errorf("restore audited history: %w", err)
	}
	if err := restored.Validate(); err != nil {
		return RecoveryProof{}, fmt.Errorf("validate restored exchange: %w", err)
	}
	stateHash, err := restored.StateHash()
	if err != nil {
		return RecoveryProof{}, fmt.Errorf("hash restored exchange: %w", err)
	}
	if restored.Sequence() != ledgerProof.FinalSequence {
		return RecoveryProof{}, fmt.Errorf(
			"%w: restored sequence have=%d want=%d",
			exchange.ErrRecoveryDiverged,
			restored.Sequence(),
			ledgerProof.FinalSequence,
		)
	}
	if ledgerProof.Records > 0 && stateHash != ledgerProof.FinalStateHash {
		return RecoveryProof{}, fmt.Errorf(
			"%w: restored hash have=%s want=%s",
			exchange.ErrRecoveryDiverged,
			stateHash,
			ledgerProof.FinalStateHash,
		)
	}
	return RecoveryProof{
		Ledger:            ledgerProof,
		RestoredSequence:  restored.Sequence(),
		RestoredStateHash: stateHash,
	}, nil
}

func proofError(sequence uint64, format string, args ...any) error {
	return fmt.Errorf(
		"%w: sequence %d: %s",
		ErrLedgerProofFailed,
		sequence,
		fmt.Sprintf(format, args...),
	)
}

func supportedRecordSchema(schemaVersion uint16) bool {
	return schemaVersion == store.LegacySchemaVersion ||
		schemaVersion == store.PreviousSchemaVersion ||
		schemaVersion == store.CurrentSchemaVersion
}

func validStateHash(stateHash string) bool {
	decoded, err := hex.DecodeString(stateHash)
	return err == nil && len(decoded) == 32
}
