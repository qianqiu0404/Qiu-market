// Package reliability contains deterministic failure-injection proofs for the
// existing trading engine. It deliberately reuses runtime.MarketRunner,
// exchange.Exchange, and store.EventStore instead of implementing another
// matcher, ledger, or persistence path.
package reliability
