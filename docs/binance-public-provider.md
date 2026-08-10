# Binance public spot provider (Q-M4A)

Q-M4A adds one opt-in, read-only adapter for the Binance Spot market-data-only
origin. It is a consumer of `marketdata/providercontract`; it is not registered
with the crawler, `SnapshotWriter`, the trading reference-price path, or any
order/ledger component.

## Provider decision

The comparison below uses provider-owned documentation reviewed on
2026-08-10.

| Candidate | Public ticker and OHLCV | Time and identity | Official limit | Decision |
|---|---|---|---|---|
| Coinbase Exchange | `GET /products/{product_id}/ticker` and `/candles`; no key | `BTC-USD` is explicit and ticker includes the last-trade time; candles use Unix seconds | Public REST: 10 requests/s, burst 15 | Rejected for this milestone. Coinbase Market Data Terms prohibit third-party display or redistribution without written consent. |
| Kraken Spot | `GET /0/public/Ticker` and `/OHLC`; no key | `assetVersion=1` improves `BTC/USD` identity, but ticker has no provider timestamp; the final OHLC row is the uncommitted interval | The official guide says 1 request/s or less remains within limits | Rejected for this milestone because source-time semantics are weaker and public redistribution permission was not established. |
| Binance Spot | `GET /api/v3/ticker/24hr` and `/api/v3/klines`; no key on the market-data-only origin | Exact `BTCUSDT`; ticker has window open/close milliseconds; klines have open and inclusive close milliseconds | Both selected endpoints have IP weight 2; 429/418 carry `Retry-After` | Selected. It has the clearest source times, exact quote turnover, a dedicated no-auth origin, and the smallest identity ambiguity. |

Official references:

- Binance [market-data-only URLs](https://developers.binance.com/en/docs/products/spot/faqs/market_data_only), [Spot REST rules and rate limits](https://developers.binance.com/en/docs/products/spot/rest-api), and [ticker/kline schemas](https://developers.binance.com/en/docs/catalog/core-trading-spot-trading/api/rest-api/market).
- Coinbase [ticker](https://docs.cdp.coinbase.com/api-reference/exchange-api/rest-api/products/get-product-ticker), [candles](https://docs.cdp.coinbase.com/api-reference/exchange-api/rest-api/products/get-product-candles), [public REST limits](https://docs.cdp.coinbase.com/exchange/rest-api/rate-limits), and [Market Data Terms](https://www.coinbase.com/legal/market_data).
- Kraken [ticker](https://docs.kraken.com/api-reference/market-data/get-ticker-information), [OHLC](https://docs.kraken.com/api-reference/market-data/get-ohlc-data), and [public REST limits](https://support.kraken.com/articles/206548367-what-are-the-api-rate-limits-).

Binance documents dashboard, analytics, and reporting integrations, but its
general product terms do not grant an explicit blanket redistribution licence.
This adapter therefore remains disabled and disconnected from public UI/API
responses until the owner confirms jurisdiction and redistribution permission.

## Frozen contract

- Provider: `binance-public`; venue: `binance`.
- Canonical market: `binance:BTC/USDT:spot`; provider symbol: exact `BTCUSDT`.
- Assets: `bitcoin` / `BTC` and `tether` / `USDT`. USD and USDT are never
  interchanged.
- Ticker source: `/api/v3/ticker/24hr` with one symbol. `lastPrice`, bid, ask,
  open, percent change, and the official `quoteVolume` remain decimal strings.
  `closeTime` is both the source observation and ticker event time.
- OHLCV source: `/api/v3/klines`, interval `1m`, limit `10`, UTC. Binance close
  time is inclusive; the adapter validates `closeTime + 1ms == openTime + 1m`
  before emitting the contract's exclusive close time.
- Price and turnover units are quote asset; candle volume is base asset.
  Decimal scale is derived from the original non-exponent string and validated
  by the Q-M3 normalizer. No market numeric value crosses this boundary as a
  floating-point number.

The production origin is exactly `https://data-api.binance.vision`. There is no
environment-variable or caller-supplied base URL. Requests are GET-only, reject
redirects, do not use an environment proxy, require TLS 1.2 or newer and JSON,
and enforce endpoint-specific response limits. Test origin injection is
unexported and accepts only loopback TLS servers; certificate verification
cannot be disabled, and the checked-in fixtures trust only their generated
test certificate through a dedicated root CA pool.

## Freshness, cache, and failures

Ticker and OHLCV responses are normalized by Q-M3 before they can enter the
bounded cache. Stale/future timestamps, identity conflicts, invalid units,
conflicting candles, bad JSON, wrong media types, and oversized bodies fail
closed. Equal duplicate or out-of-order candles are normalized into stable time
order with quality flags. Failed responses are never cached.

HTTP 418/429 is a typed `rate_limit` with `Retry-After`; 5xx, timeout, and
network failures remain retryable. Auth, permission, bad request, invalid
identity, and bad payload are not silently masked by fallback. The actual
provider and source reference survive every successful dispatch.

## Evidence and activation boundary

Normal tests use checked-in official-schema fixtures and no network. The online
smoke is separately gated, performs only the ticker and candle GETs, and logs
only provider, capability, HTTP status, count, latency, and freshness—not raw
payloads, URLs, headers, or credentials. Passing the smoke proves current read
compatibility; it does not authorize public display and does not make the data
an executable trading price.

```bash
go test -count=1 ./marketdata/providercontract/binancepublic
go test -tags=online -count=1 \
  -run '^TestBinancePublicOnlineSmoke$' -v \
  ./marketdata/providercontract/binancepublic \
  -args -binance-public-online
```

The build tag and test flag are both required for network access. Omitting the
tag excludes the smoke from ordinary CI; supplying the tag without the flag
produces an explicit skip and still performs zero requests.
