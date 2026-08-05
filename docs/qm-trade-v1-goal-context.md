# Qiu Market Trade Product V1 Goal Context

## Goal

Deliver one auditable BTC/USDT virtual spot terminal on top of the existing
Qiu Market matching engine, ledger, PostgreSQL event stream, recovery gate,
REST, and event transport. This goal remains active until the implementation,
review, immutable candidate, Preview acceptance, Production promotion, and
external observation evidence are closed.

The user-visible completion story is:

```text
sign in
-> admin virtual funding in System
-> place a Limit maker order
-> execute a deterministic partial fill
-> inspect fees and double-entry ledger effects
-> cancel the remainder
-> inspect the event-truth order timeline
-> reconcile an uncertain response with the original request ID
-> restart and recover the same balances, orders, cursors, and state hash
```

## Frozen scope

- One virtual `BTC-USDT` spot market.
- Available and held balances, per-fill fee evidence, ledger entries, and
  asset-change reasons. Aggregate fee statistics and reference valuation remain
  the PRD P1 backlog and are not required to close this V1 target.
- Cursor-paginated orders, account trades, order events, and ledger entries.
- Bilingual Trade and System user flows.
- `submitted/unknown` recovery for submit, cancel, and virtual funding.
- Trade shows only actionable `LIVE / DEGRADED / OFFLINE` state; recovery epoch,
  state hash, deployment identity, and transport proof remain in System.

## Explicit non-goals

- PnL or cost-basis accounting.
- Stop, Stop Limit, Stop Market, or OCO orders.
- Multiple markets, margin, perpetuals, options, or strategy/backtest labs.
- Real deposits, withdrawals, private keys, exchange order routing, or funds.
- A second matching engine, ledger, event stream, or browser-only truth source.

## Authorities and invariants

- The PostgreSQL event stream is the final business truth.
- Order timelines are event-derived projections, not UI-synthesized history.
- Account identity is determined by the server session; browser `account_id`
  fields never authorize access.
- Every write is persisted in the browser journal before dispatch and is bound
  to `(market, account, operation, request_id)`.
- An uncertain response never creates a new request ID. The client queries
  authoritative facts first and may replay only the exact original operation.
- Cursor tokens are account/query-bound, HMAC authenticated, and backed by a
  persistent private rotation key. Missing key material fails startup closed.
- Projection readiness requires durable progress plus integrity evidence; a
  sequence equal to stream head alone is insufficient.
- All money uses decimal strings at browser boundaries and checked integer
  atoms in Go. JavaScript numbers are not an accounting authority.

## Delivery ownership

- Contract changes land before implementations that consume them.
- Parallel work uses disjoint file ownership: frontend interaction, query
  transport, PostgreSQL read model, and reliability tests.
- The primary agent owns shared contracts, integration wiring, total review,
  release commits, PR handling, and deployment evidence.
- Concurrent worktrees and unrelated dirty changes are preserved; no reset,
  clean, stash, force push, or main-branch overwrite is allowed.

## Evidence states

| State | Meaning |
|---|---|
| `implemented` | Code exists but has not passed its full gate. |
| `build-verified` | Deterministic unit, type, build, vet, and diff gates pass. |
| `integration-verified` | PostgreSQL, restart, browser, hash, and ledger flows pass together. |
| `environment-pending` | External credentials, Preview, Production, or observation evidence is still missing. |
| `production-recommendation` | The immutable candidate passed the declared external observation gate. |

Local fixtures, browser mocks, loopback HTTP, and virtual balances never prove
real-funds or commercial production safety.

## Terminal condition

The goal is complete only when one reviewed commit is merged, the same immutable
candidate is accepted in Protected Preview and promoted without rebuild, and
the agreed external observation gates are recorded. Until then, reports must
state the current evidence level and continue the goal instead of proposing a
new product phase.
