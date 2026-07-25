package ledger

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/the-web3/s78-market-services/trading/domain"
)

var (
	ErrDuplicateTransaction  = errors.New("duplicate ledger transaction")
	ErrUnbalancedTransaction = errors.New("unbalanced ledger transaction")
	ErrInsufficientBalance   = errors.New("insufficient balance")
	ErrInvalidTransaction    = errors.New("invalid ledger transaction")
)

type Entry struct {
	Account string       `json:"account"`
	Asset   domain.Asset `json:"asset"`
	Amount  int64        `json:"amount"`
}

type Transaction struct {
	ID        string  `json:"id"`
	Reference string  `json:"reference"`
	Entries   []Entry `json:"entries"`
}

type Balance struct {
	Account string       `json:"account"`
	Asset   domain.Asset `json:"asset"`
	Amount  int64        `json:"amount"`
}

type Snapshot struct {
	Balances []Balance     `json:"balances"`
	Journal  []Transaction `json:"journal"`
}

type balanceKey struct {
	account string
	asset   domain.Asset
}

type Ledger struct {
	balances       map[balanceKey]int64
	journal        []Transaction
	transactionIDs map[string]struct{}
}

func New() *Ledger {
	return &Ledger{
		balances:       make(map[balanceKey]int64),
		transactionIDs: make(map[string]struct{}),
	}
}

func UserAvailable(accountID domain.AccountID) string {
	return fmt.Sprintf("user:%s:available", accountID)
}

func UserHeld(accountID domain.AccountID) string {
	return fmt.Sprintf("user:%s:held", accountID)
}

func PlatformFee(asset domain.Asset) string {
	return fmt.Sprintf("platform:fee:%s", asset)
}

func SystemTreasury(asset domain.Asset) string {
	return fmt.Sprintf("system:treasury:%s", asset)
}

func (l *Ledger) Clone() *Ledger {
	cloned := New()
	for key, amount := range l.balances {
		cloned.balances[key] = amount
	}
	cloned.journal = cloneJournal(l.journal)
	for id := range l.transactionIDs {
		cloned.transactionIDs[id] = struct{}{}
	}
	return cloned
}

func (l *Ledger) Post(tx Transaction) error {
	if tx.ID == "" || tx.Reference == "" || len(tx.Entries) < 2 {
		return fmt.Errorf("%w: id, reference, and at least two entries are required", ErrInvalidTransaction)
	}
	if _, exists := l.transactionIDs[tx.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateTransaction, tx.ID)
	}

	sums := make(map[domain.Asset]int64)
	pending := make(map[balanceKey]int64)
	for _, entry := range tx.Entries {
		if entry.Account == "" || entry.Asset == "" || entry.Amount == 0 {
			return fmt.Errorf("%w: entries require account, asset, and non-zero amount", ErrInvalidTransaction)
		}
		sum, err := domain.CheckedAdd(sums[entry.Asset], entry.Amount)
		if err != nil {
			return fmt.Errorf("%w: sum entries for %s", ErrInvalidTransaction, entry.Asset)
		}
		sums[entry.Asset] = sum

		key := balanceKey{account: entry.Account, asset: entry.Asset}
		current, exists := pending[key]
		if !exists {
			current = l.balances[key]
		}
		next, err := domain.CheckedAdd(current, entry.Amount)
		if err != nil {
			return fmt.Errorf("%w: account %s asset %s", ErrInvalidTransaction, entry.Account, entry.Asset)
		}
		if next < 0 && !isNegativeBalanceAllowed(entry.Account) {
			return fmt.Errorf("%w: account %s asset %s", ErrInsufficientBalance, entry.Account, entry.Asset)
		}
		pending[key] = next
	}
	for asset, sum := range sums {
		if sum != 0 {
			return fmt.Errorf("%w: asset %s sums to %d", ErrUnbalancedTransaction, asset, sum)
		}
	}

	for key, amount := range pending {
		l.balances[key] = amount
	}
	l.transactionIDs[tx.ID] = struct{}{}
	l.journal = append(l.journal, cloneTransaction(tx))
	return nil
}

func (l *Ledger) FundVirtual(txID, reference string, accountID domain.AccountID, asset domain.Asset, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("%w: funding amount must be positive", ErrInvalidTransaction)
	}
	return l.Post(Transaction{
		ID:        txID,
		Reference: reference,
		Entries: []Entry{
			{Account: SystemTreasury(asset), Asset: asset, Amount: -amount},
			{Account: UserAvailable(accountID), Asset: asset, Amount: amount},
		},
	})
}

func (l *Ledger) Hold(txID, reference string, accountID domain.AccountID, asset domain.Asset, amount int64) error {
	return l.move(txID, reference, UserAvailable(accountID), UserHeld(accountID), asset, amount)
}

func (l *Ledger) Release(txID, reference string, accountID domain.AccountID, asset domain.Asset, amount int64) error {
	return l.move(txID, reference, UserHeld(accountID), UserAvailable(accountID), asset, amount)
}

func (l *Ledger) move(txID, reference, from, to string, asset domain.Asset, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("%w: transfer amount must be positive", ErrInvalidTransaction)
	}
	return l.Post(Transaction{
		ID:        txID,
		Reference: reference,
		Entries: []Entry{
			{Account: from, Asset: asset, Amount: -amount},
			{Account: to, Asset: asset, Amount: amount},
		},
	})
}

func (l *Ledger) Balance(account string, asset domain.Asset) int64 {
	return l.balances[balanceKey{account: account, asset: asset}]
}

func (l *Ledger) UserBalance(accountID domain.AccountID, asset domain.Asset) (available, held int64) {
	return l.Balance(UserAvailable(accountID), asset), l.Balance(UserHeld(accountID), asset)
}

func (l *Ledger) Balances() []Balance {
	result := make([]Balance, 0, len(l.balances))
	for key, amount := range l.balances {
		result = append(result, Balance{Account: key.account, Asset: key.asset, Amount: amount})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Account != result[j].Account {
			return result[i].Account < result[j].Account
		}
		return result[i].Asset < result[j].Asset
	})
	return result
}

func (l *Ledger) Journal() []Transaction {
	return cloneJournal(l.journal)
}

func (l *Ledger) Snapshot() Snapshot {
	return Snapshot{
		Balances: l.Balances(),
		Journal:  l.Journal(),
	}
}

func FromSnapshot(snapshot Snapshot) (*Ledger, error) {
	restored := New()
	for _, tx := range snapshot.Journal {
		if err := restored.Post(tx); err != nil {
			return nil, fmt.Errorf("restore ledger transaction %s: %w", tx.ID, err)
		}
	}
	actual := restored.Balances()
	expected := append([]Balance(nil), snapshot.Balances...)
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].Account != expected[j].Account {
			return expected[i].Account < expected[j].Account
		}
		return expected[i].Asset < expected[j].Asset
	})
	if len(actual) != len(expected) {
		return nil, fmt.Errorf("restore ledger: balance count mismatch")
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return nil, fmt.Errorf("restore ledger: balance mismatch at index %d", i)
		}
	}
	return restored, nil
}

func (l *Ledger) Validate() error {
	recomputed := New()
	for _, tx := range l.journal {
		if err := recomputed.Post(tx); err != nil {
			return fmt.Errorf("recompute transaction %s: %w", tx.ID, err)
		}
	}
	actual := l.Balances()
	expected := recomputed.Balances()
	if len(actual) != len(expected) {
		return fmt.Errorf("ledger balance count mismatch: have %d want %d", len(actual), len(expected))
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return fmt.Errorf("ledger balance mismatch at index %d", i)
		}
	}
	return nil
}

func isNegativeBalanceAllowed(account string) bool {
	return strings.HasPrefix(account, "system:treasury:")
}

func cloneJournal(journal []Transaction) []Transaction {
	result := make([]Transaction, len(journal))
	for i, tx := range journal {
		result[i] = cloneTransaction(tx)
	}
	return result
}

func cloneTransaction(tx Transaction) Transaction {
	cloned := tx
	cloned.Entries = append([]Entry(nil), tx.Entries...)
	return cloned
}
