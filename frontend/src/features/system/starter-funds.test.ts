import { describe, expect, it } from 'vitest'
import { TradingRequestError, type FundingRequestResult } from '../../api/trading'
import {
  STARTER_FUNDING_STEPS,
  fundingRequestNotFound,
  validateStarterFundingResult,
} from './starter-funds'

function result(overrides: Partial<FundingRequestResult> = {}): FundingRequestResult {
  return {
    market_id: 'BTC-USDT',
    request_id: 'starter-v1-usdt',
    funding_event_id: 'event:12:0',
    sequence: '12',
    asset: 'USDT',
    amount: '10000',
    projection_result: 'applied',
    ledger_balanced: true,
    occurred_at: '2026-08-13T01:02:03Z',
    ...overrides,
  }
}

describe('starter funding truth', () => {
  it('freezes the two account-level request IDs and amounts', () => {
    expect(STARTER_FUNDING_STEPS).toEqual([
      { requestID: 'starter-v1-usdt', asset: 'USDT', amount: '10000' },
      { requestID: 'starter-v1-btc', asset: 'BTC', amount: '0.1' },
    ])
  })

  it('accepts only the matching event and balanced applied projection', () => {
    expect(validateStarterFundingResult(result(), STARTER_FUNDING_STEPS[0]).sequence)
      .toBe('12')
    for (const invalid of [
      result({ request_id: 'different' }),
      result({ asset: 'BTC' }),
      result({ amount: '9999' }),
      result({ projection_result: 'pending' as 'applied' }),
      result({ ledger_balanced: false }),
      result({ funding_event_id: '' }),
      result({ sequence: '12.5' }),
    ]) {
      expect(() => validateStarterFundingResult(invalid, STARTER_FUNDING_STEPS[0]))
        .toThrow('Starter funding truth is malformed')
    }
  })

  it('distinguishes the private not-found contract from transport and auth errors', () => {
    expect(fundingRequestNotFound(new TradingRequestError(
      'not found', 'funding_request_not_found', 404, false,
    ))).toBe(true)
    expect(fundingRequestNotFound(new TradingRequestError(
      'not found', 'funding_request_not_found', 503, false,
    ))).toBe(false)
    expect(fundingRequestNotFound(new TradingRequestError(
      'session', 'authentication_required', 401, false,
    ))).toBe(false)
  })
})
