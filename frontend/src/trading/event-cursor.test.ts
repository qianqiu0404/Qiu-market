import { describe, expect, it } from 'vitest'
import {
  advanceTradingEventCursor,
  compareTradingEventCursor,
  latestTradingEventCursor,
  normalizeTradingEventCursor,
  type TradingEventCursor,
} from './event-cursor'

const marketID = 'BTC-USDT'
const cursor = (
  sequence: string,
  eventIndex: number,
): TradingEventCursor => ({
  market_id: marketID,
  sequence,
  event_index: eventIndex,
})

describe('trading event cursor', () => {
  it('accepts contiguous events inside and across market sequences', () => {
    expect(advanceTradingEventCursor(
      cursor('42', 1),
      cursor('42', 2),
      marketID,
    )).toMatchObject({ kind: 'accepted', cursor: cursor('42', 2) })
    expect(advanceTradingEventCursor(
      cursor('42', 7),
      cursor('43', 1),
      marketID,
    )).toMatchObject({ kind: 'accepted', cursor: cursor('43', 1) })
  })

  it('drops duplicate and out-of-order replay without moving backward', () => {
    expect(advanceTradingEventCursor(
      cursor('42', 2),
      cursor('42', 2),
      marketID,
    )).toMatchObject({ kind: 'duplicate', cursor: cursor('42', 2) })
    expect(advanceTradingEventCursor(
      cursor('42', 2),
      cursor('41', 9),
      marketID,
    )).toMatchObject({ kind: 'duplicate', cursor: cursor('42', 2) })
  })

  it('detects a missing event index and a missing market sequence', () => {
    expect(advanceTradingEventCursor(
      cursor('42', 1),
      cursor('42', 3),
      marketID,
    )).toMatchObject({ kind: 'gap', cursor: cursor('42', 3) })
    expect(advanceTradingEventCursor(
      cursor('42', 2),
      cursor('44', 1),
      marketID,
    )).toMatchObject({ kind: 'gap', cursor: cursor('44', 1) })
  })

  it('compares decimal sequence strings beyond Number safe integer range', () => {
    const older = cursor('9007199254740993', 8)
    const newer = cursor('9007199254740994', 1)

    expect(compareTradingEventCursor(older, newer)).toBeLessThan(0)
    expect(latestTradingEventCursor(older, newer)).toEqual(newer)
  })

  it('normalizes a valid checkpoint and rejects another market or event zero', () => {
    expect(normalizeTradingEventCursor({
      market_id: marketID,
      sequence: '0001',
      event_index: 1,
    }, marketID)).toBeNull()
    expect(normalizeTradingEventCursor(
      cursor('1', 0),
      marketID,
    )).toEqual(cursor('1', 0))
    expect(advanceTradingEventCursor(
      undefined,
      cursor('1', 0),
      marketID,
    )).toMatchObject({ kind: 'invalid' })
    expect(normalizeTradingEventCursor({
      market_id: 'ETH-USDT',
      sequence: '1',
      event_index: 1,
    }, marketID)).toBeNull()
  })
})
