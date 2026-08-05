// Package readmodel contains deterministic projection rules shared by Trade
// Product V1 read-model components.
package readmodel

import (
	"strings"

	"github.com/the-web3/s78-market-services/trading/domain"
	"github.com/the-web3/s78-market-services/trading/query"
)

// ClassifyLedgerReason uses only the immutable transaction and reference
// namespaces frozen by PRD-QM-TRADE-001. Unknown pairs deliberately remain
// other; callers must not infer a reason from amount or time proximity.
func ClassifyLedgerReason(transactionID, reference string) query.LedgerReason {
	switch {
	case strings.HasPrefix(transactionID, "fund:") &&
		strings.HasPrefix(reference, "virtual-funding:"):
		return query.LedgerReasonVirtualFund
	case strings.HasPrefix(transactionID, "hold:") &&
		strings.HasPrefix(reference, "order-hold:"):
		return query.LedgerReasonOrderHold
	case strings.HasPrefix(transactionID, "release:") &&
		strings.HasPrefix(reference, "order-release:"):
		return query.LedgerReasonOrderRelease
	case strings.HasPrefix(transactionID, "cancel-release:") &&
		strings.HasPrefix(reference, "order-cancel:"):
		return query.LedgerReasonOrderRelease
	case strings.HasPrefix(transactionID, "maker-release:") &&
		strings.HasPrefix(reference, "maker-rounding-release:"):
		return query.LedgerReasonOrderRelease
	case strings.HasPrefix(transactionID, "trade:") &&
		strings.HasPrefix(reference, "matched-trade:"):
		return query.LedgerReasonTradeSettlement
	default:
		return query.LedgerReasonOther
	}
}

func OrderIDFromReference(reference string) domain.OrderID {
	for _, prefix := range []string{
		"order-hold:",
		"order-release:",
		"order-cancel:",
		"maker-rounding-release:",
	} {
		if strings.HasPrefix(reference, prefix) {
			return domain.OrderID(strings.TrimPrefix(reference, prefix))
		}
	}
	return ""
}

func TradeIDFromReference(reference string) domain.TradeID {
	if strings.HasPrefix(reference, "matched-trade:") {
		return domain.TradeID(strings.TrimPrefix(reference, "matched-trade:"))
	}
	return ""
}
