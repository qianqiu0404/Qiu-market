package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/the-web3/s78-market-services/trading/domain"
)

var ErrSequenceConflict = errors.New("event store sequence conflict")

type Record struct {
	Command   domain.Command `json:"command"`
	Result    domain.Result  `json:"result"`
	StateHash string         `json:"state_hash"`
}

type Snapshot struct {
	Sequence  uint64 `json:"sequence"`
	StateHash string `json:"state_hash"`
	Payload   []byte `json:"payload"`
}

type EventStore interface {
	Append(ctx context.Context, expectedSequence uint64, record Record) error
	RecordsAfter(ctx context.Context, sequence uint64) ([]Record, error)
}

type SnapshotStore interface {
	Save(ctx context.Context, snapshot Snapshot) error
	Load(ctx context.Context) (Snapshot, bool, error)
}

type Memory struct {
	mu       sync.RWMutex
	records  []Record
	snapshot *Snapshot
}

func NewMemory() *Memory {
	return &Memory{}
}

func (m *Memory) Append(ctx context.Context, expectedSequence uint64, record Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	current := uint64(len(m.records))
	if current != expectedSequence || record.Command.Sequence != expectedSequence+1 ||
		record.Result.Sequence != record.Command.Sequence {
		return fmt.Errorf("%w: have=%d expected=%d command=%d result=%d",
			ErrSequenceConflict, current, expectedSequence, record.Command.Sequence, record.Result.Sequence)
	}
	cloned, err := cloneRecord(record)
	if err != nil {
		return err
	}
	m.records = append(m.records, cloned)
	return nil
}

func (m *Memory) RecordsAfter(ctx context.Context, sequence uint64) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if sequence > uint64(len(m.records)) {
		return nil, fmt.Errorf("%w: requested sequence %d after current %d", ErrSequenceConflict, sequence, len(m.records))
	}
	result := make([]Record, 0, len(m.records)-int(sequence))
	for _, record := range m.records[sequence:] {
		cloned, err := cloneRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, cloned)
	}
	return result, nil
}

func (m *Memory) Save(ctx context.Context, snapshot Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if snapshot.Sequence > uint64(len(m.records)) {
		return fmt.Errorf("%w: snapshot sequence %d after current %d", ErrSequenceConflict, snapshot.Sequence, len(m.records))
	}
	cloned := snapshot
	cloned.Payload = append([]byte(nil), snapshot.Payload...)
	m.snapshot = &cloned
	return nil
}

func (m *Memory) Load(ctx context.Context) (Snapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.snapshot == nil {
		return Snapshot{}, false, nil
	}
	cloned := *m.snapshot
	cloned.Payload = append([]byte(nil), m.snapshot.Payload...)
	return cloned, true, nil
}

func (m *Memory) CorruptRecord(sequence uint64, mutate func(*Record)) error {
	if sequence == 0 || mutate == nil {
		return fmt.Errorf("sequence and mutate function are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if sequence > uint64(len(m.records)) {
		return fmt.Errorf("record %d does not exist", sequence)
	}
	mutate(&m.records[sequence-1])
	return nil
}

func (m *Memory) RecordCount() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return uint64(len(m.records))
}

func cloneRecord(record Record) (Record, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return Record{}, fmt.Errorf("clone event record: %w", err)
	}
	var cloned Record
	if err := json.Unmarshal(data, &cloned); err != nil {
		return Record{}, fmt.Errorf("clone event record: %w", err)
	}
	return cloned, nil
}
