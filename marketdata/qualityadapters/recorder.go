// Package qualityadapters converts already-normalized, read-only provider and
// research fetch results into quality evidence. It cannot import trading,
// persistence, order-book, matching, or ledger packages.
package qualityadapters

import (
	"fmt"

	"github.com/the-web3/s78-market-services/marketdata/quality"
)

type Recorder interface {
	Record(quality.Evidence) error
}

func record(recorder Recorder, evidence quality.Evidence, err error) error {
	if err != nil {
		return err
	}
	if recorder == nil {
		return fmt.Errorf("quality adapter: nil recorder")
	}
	return recorder.Record(evidence)
}
