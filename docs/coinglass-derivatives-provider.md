# CoinGlass derivatives provider (Q-M4B)

Q-M4B adds a default-disabled, read-only CoinGlass adapter under
`marketdata/providercontract`. It is an offline-verified contract seam: it is
not registered with the crawler, `SnapshotWriter`, public API/UI, trading
reference prices, matching, balances, or the ledger.

## Official contract and provider decision

The review below uses provider-owned documentation checked on 2026-08-10.

- CoinGlass API V4 uses the fixed origin
  `https://open-api-v4.coinglass.com`; every request requires the
  `CG-API-KEY` header. The response headers `API-KEY-MAX-LIMIT` and
  `API-KEY-USE-LIMIT` describe the key's per-minute allowance and use.
- Hobbyist, Startup, Standard, and Professional currently advertise
  30/80/300/1200 requests per minute respectively. Hobbyist and Startup are
  personal-use plans; Standard and Professional advertise commercial use.
- The selected OI and liquidation history endpoints are available on every
  plan, but Hobbyist is limited to intervals of at least 4h. The adapter
  therefore freezes `interval=4h` and a two-row response limit.
- CoinGlass was selected over CoinMarketCap for this milestone because
  CoinGlass publishes first-class pair liquidation history as well as OI and
  funding families. CoinMarketCap's official derivatives overview documents
  OI and funding fields, but does not establish an equivalent liquidation
  capability for this slice.

Official sources:

- CoinGlass [API introduction and base URL](https://docs.coinglass.com/reference/getting-started-with-your-api),
  [authentication](https://docs.coinglass.com/reference/authentication), and
  [errors/rate limits](https://docs.coinglass.com/reference/responses-error-codes).
- CoinGlass [supported futures instruments](https://docs.coinglass.com/reference/futures-suported-exchange-pairs),
  [OI history](https://docs.coinglass.com/reference/oi-ohlc-histroy),
  [liquidation history](https://docs.coinglass.com/reference/liquidation-history),
  [funding by exchange](https://docs.coinglass.com/reference/fr-exchange-list),
  and [funding arbitrage](https://docs.coinglass.com/reference/fr-arbitrage).
- CoinGlass [pricing](https://www.coinglass.com/pricing) and
  [Terms of Service](https://www.coinglass.com/terms).
- CoinMarketCap [derivatives overview](https://coinmarketcap.com/api/documentation/pro-api-reference/endpoint-overview)
  and [authentication](https://coinmarketcap.com/api/documentation/guides/authentication).

## Frozen identity and capability matrix

The official instrument catalog explicitly identifies Binance
`BTCUSD_PERP`, with base `BTC`, quote `USD`, and settlement currency `USDT`.
The canonical Q-M3 market is therefore `binance:BTC/USD:perp`; USD and USDT
are never interchanged. Settlement currency remains in the auditable contract
identity/source reference because Q-M3 `Market` does not model settlement.

| Request | Exact upstream operation | Mapping | Result |
|---|---|---|---|
| Open interest | `GET /api/futures/open-interest/history?exchange=Binance&symbol=BTCUSD_PERP&interval=4h&limit=2&unit=usd` | latest valid row `close` -> `open_interest`, unit USD | supported |
| Liquidation | `GET /api/futures/liquidation/history?exchange=Binance&symbol=BTCUSD_PERP&interval=4h&limit=2` | latest valid row long/short amounts -> USD, window 14,400 seconds | supported |
| Funding rate | no request | related CoinGlass pages do not consistently state whether their value is a ratio or percentage; Q-M3 requires one explicit unit | typed `unsupported`, zero HTTP |

The funding decision is deliberate. CoinGlass's arbitrage endpoint explicitly
labels its own funding field as percent, but the exchange-list/history pages
do not state that unit. Semantics from one endpoint are not silently copied to
another. Q-M4C may enable funding only after CoinGlass confirms the exact unit
for the selected endpoint, or after a separately modelled endpoint with an
unambiguous contract is approved.

Both implemented responses use the official row timestamp as `event_time`.
CoinGlass does not provide a separate source observation timestamp in these
schemas, so `observed_at` is the local HTTP receipt time and the envelope is
marked derived/partial. The adapter does not claim whether the row
timestamp is the interval's open or close. A bounded source-age check prevents
an old history row from being presented as current, while Q-M3 independently
rejects future time and stale receipt metadata.

Rows are normalized into deterministic time order. An identical same-time row
is retained once with a duplicate flag; a same-time row with different data is
a typed conflict. Decimal strings remain exact and never pass through
`float64`.

## Authentication, transport, and budget boundary

`Enabled` is false by default. Enabling requires an injected secret-provider
object; this package does not read environment files or environment variables
and does not provide a static-key production helper. A missing or blank key is
a typed configuration/auth failure before network I/O. The key is accepted
only as the `CG-API-KEY` request header and is excluded from URL/query,
configuration JSON, errors, observations, fixtures, and source references.

Production transport is restricted to the exact HTTPS origin and two GET
path/query combinations above. It disables environment proxies, redirects,
cookies, and arbitrary caller origins; requires certificate verification and
TLS 1.2 or newer; applies bounded timeouts and response limits; accepts a
single JSON value with the expected media type; and requires the CoinGlass
envelope code `0`. Test transport injection is package-private and restricted
to verified-TLS loopback servers.

HTTP 401 is `auth`, 403 is `permission`, 408 is `timeout`, 429 is
`rate_limit`, and 5xx is `upstream_5xx`. Retry-After is preserved without
including response bodies in errors. A conservative local budget is bounded
by the lowest documented 30-request/minute plan, and the bounded Q-M3 cache
stores only validated fresh success. Errors are never cached as data.

## Licence and activation boundary

CoinGlass's Terms prohibit copying, selling, or redistributing data and
commercial use without authorization. A plan labelled “commercial use” does
not by itself establish public redistribution, archival, or derived-data
rights. Consequently Q-M4B does not persist raw responses or expose facts to a
user interface.

Q-M4C live activation requires all of the following owner inputs:

1. an approved server-side secret-provider integration (only the presence of
   the channel is reported; the key is never printed);
2. the purchased plan and its effective per-minute limit;
3. written confirmation covering the intended display, caching, retention,
   attribution, and derived-data use;
4. CoinGlass confirmation of the selected funding endpoint's unit if funding
   is to be enabled;
5. a separately approved opt-in, read-only smoke and rollout registration.

Until those gates are met, “contract verified” means only that checked-in
official-schema fixtures pass without network access. It is not live data,
permission to redistribute, or an executable trading price.

The offline contract gate is:

```bash
go test -count=1 ./marketdata/providercontract/coinglass
go test -race -count=1 ./marketdata/providercontract/coinglass
go vet ./marketdata/providercontract/coinglass
```

These commands need no key and perform no CoinGlass request. Q-M4B deliberately
does not add an environment-reading smoke test; live evidence belongs to Q-M4C
after the approved secret channel and licence inputs exist.
