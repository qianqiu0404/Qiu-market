export interface TradingEventCursor {
  market_id: string
  sequence: string
  event_index: number
}

export type CursorAdvance =
  | { kind: 'accepted'; cursor: TradingEventCursor }
  | { kind: 'duplicate'; cursor: TradingEventCursor }
  | { kind: 'gap'; cursor: TradingEventCursor }
  | { kind: 'invalid'; reason: string }

const decimalSequence = /^(0|[1-9]\d*)$/
const maximumEventIndex = 0xffff_ffff

export function normalizeTradingEventCursor(
  value: Partial<TradingEventCursor> | null | undefined,
  marketID: string,
): TradingEventCursor | null {
  if (
    !value ||
    value.market_id !== marketID ||
    typeof value.sequence !== 'string' ||
    !decimalSequence.test(value.sequence) ||
    typeof value.event_index !== 'number' ||
    !Number.isInteger(value.event_index) ||
    value.event_index < 0 ||
    value.event_index > maximumEventIndex
  ) {
    return null
  }
  return {
    market_id: marketID,
    sequence: BigInt(value.sequence).toString(),
    event_index: value.event_index,
  }
}

export function compareTradingEventCursor(
  left: TradingEventCursor,
  right: TradingEventCursor,
): number {
  const leftSequence = BigInt(left.sequence)
  const rightSequence = BigInt(right.sequence)
  if (leftSequence < rightSequence) return -1
  if (leftSequence > rightSequence) return 1
  return left.event_index - right.event_index
}

export function latestTradingEventCursor(
  left: TradingEventCursor | undefined,
  right: TradingEventCursor,
): TradingEventCursor {
  if (!left || compareTradingEventCursor(left, right) < 0) return right
  return left
}

export function advanceTradingEventCursor(
  current: TradingEventCursor | undefined,
  incomingValue: Partial<TradingEventCursor>,
  marketID: string,
): CursorAdvance {
  const incoming = normalizeTradingEventCursor(incomingValue, marketID)
  if (!incoming || incoming.event_index === 0) {
    return { kind: 'invalid', reason: 'invalid_event_cursor' }
  }
  if (!current) return { kind: 'accepted', cursor: incoming }
  const normalizedCurrent = normalizeTradingEventCursor(current, marketID)
  if (!normalizedCurrent) {
    return { kind: 'invalid', reason: 'invalid_current_cursor' }
  }
  const comparison = compareTradingEventCursor(incoming, normalizedCurrent)
  if (comparison <= 0) {
    return { kind: 'duplicate', cursor: normalizedCurrent }
  }

  const currentSequence = BigInt(normalizedCurrent.sequence)
  const incomingSequence = BigInt(incoming.sequence)
  const sameBatchNextEvent = incomingSequence === currentSequence &&
    incoming.event_index === normalizedCurrent.event_index + 1
  const nextBatchFirstEvent = incomingSequence === currentSequence + 1n &&
    incoming.event_index === 1
  if (sameBatchNextEvent || nextBatchFirstEvent) {
    return { kind: 'accepted', cursor: incoming }
  }
  return { kind: 'gap', cursor: incoming }
}
