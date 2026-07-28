import { describe, expect, it } from 'vitest'
import { isRetryableUpstreamRequest } from './proxy'

describe('isRetryableUpstreamRequest', () => {
  it('retries only explicitly safe trading reads and public market envelopes', () => {
    expect(
      isRetryableUpstreamRequest(
        'GET',
        '/api/v1/trading/markets/BTC-USDT/status',
      ),
    ).toBe(true)
    expect(
      isRetryableUpstreamRequest('GET', '/api/v1/trading/orders/order-1'),
    ).toBe(true)
    expect(
      isRetryableUpstreamRequest('POST', '/api/v2/get_asset_dashboard'),
    ).toBe(true)
  })

  it('never retries OAuth navigation or callback requests', () => {
    expect(
      isRetryableUpstreamRequest(
        'GET',
        '/api/v1/trading/auth/github/start',
      ),
    ).toBe(false)
    expect(
      isRetryableUpstreamRequest(
        'GET',
        '/api/v1/trading/auth/github/callback',
      ),
    ).toBe(false)
  })

  it('does not infer retry safety from the HTTP method alone', () => {
    expect(isRetryableUpstreamRequest('GET', '/api/v1/future-write')).toBe(false)
    expect(isRetryableUpstreamRequest('HEAD', '/api/v1/future-write')).toBe(false)
    expect(isRetryableUpstreamRequest('POST', '/api/v1/trading/orders')).toBe(false)
  })
})
