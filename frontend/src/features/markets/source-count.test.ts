import { describe, expect, it } from 'vitest'
import { sourceCountBadge } from './source-count'

describe('sourceCountBadge', () => {
  it.each([
    [4, ['coinbase', 'okx', 'binance', 'bybit'], '3+ sources', 'live'],
    [3, [], '3+ sources', 'live'],
    [2, [], '2 sources', 'delayed'],
    [1, [], '1 source', 'accent'],
  ] as const)('renders %i contributors without a quality grade', (count, contributors, label, variant) => {
    expect(sourceCountBadge({
      available: true,
      source: 'cex_composite',
      contributor_count: count,
      contributors: [...contributors],
    })).toMatchObject({ count, label, variant })
  })

  it('uses an identified available source when a legacy count is absent', () => {
    expect(sourceCountBadge({
      available: true,
      source: 'okx',
      contributor_count: 0,
      contributors: [],
    }).label).toBe('1 source')
  })

  it('keeps unavailable separate from freshness or quality strings', () => {
    expect(sourceCountBadge({
      available: false,
      source: 'okx',
      contributor_count: 3,
      contributors: ['okx', 'coinbase', 'bybit'],
    })).toEqual({ count: 0, label: 'unavailable', variant: 'accent' })
  })
})
