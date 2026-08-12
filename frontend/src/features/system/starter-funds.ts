import {
  TradingRequestError,
  type FundingRequestResult,
} from '../../api/trading'

export const STARTER_FUNDING_STEPS = [
  { requestID: 'starter-v1-usdt', asset: 'USDT', amount: '10000' },
  { requestID: 'starter-v1-btc', asset: 'BTC', amount: '0.1' },
] as const

export type StarterFundingStep = typeof STARTER_FUNDING_STEPS[number]

export function fundingRequestNotFound(error: unknown): boolean {
  return error instanceof TradingRequestError &&
    error.status === 404 && error.code === 'funding_request_not_found'
}

export function validateStarterFundingResult(
  result: FundingRequestResult,
  step: StarterFundingStep,
): FundingRequestResult {
  if (
    result.market_id !== 'BTC-USDT' ||
    result.request_id !== step.requestID ||
    result.asset !== step.asset ||
    result.amount !== step.amount ||
    result.projection_result !== 'applied' ||
    result.ledger_balanced !== true ||
    !result.funding_event_id ||
    !/^\d+$/.test(result.sequence) ||
    !result.occurred_at
  ) {
    throw new Error(`Starter funding truth is malformed for ${step.requestID}`)
  }
  return result
}
