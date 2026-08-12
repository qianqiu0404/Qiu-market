import { describe, expect, it } from 'vitest'
import { qualityGradeBadge } from './quality-grade'

describe('qualityGradeBadge', () => {
  it.each([
    [4, ['coinbase', 'okx', 'binance', 'bybit'], '', 'High', 'live'],
    [3, ['coinbase', 'okx', 'binance'], '', 'High', 'live'],
    [2, ['coinbase', 'okx'], '', 'Medium', 'delayed'],
    [1, ['coinbase'], '', 'Low', 'accent'],
  ] as const)('maps %i independent contributors to a quality grade', (
    count,
    contributors,
    quality,
    label,
    variant,
  ) => {
    expect(qualityGradeBadge({
      available: true,
      source: 'cex_composite',
      quality,
      contributor_count: count,
      contributors: [...contributors],
    })).toMatchObject({ count, label, variant })
  })

  it('does not let a backend route grade overstate the independent contributor count', () => {
    expect(qualityGradeBadge({
      available: true,
      source: 'okx',
      quality: 'medium',
      contributor_count: 1,
      contributors: ['okx'],
    }).label).toBe('Low')
  })

  it('uses the named contributor identities when a declared count disagrees', () => {
    expect(qualityGradeBadge({
      available: true,
      source: 'cex_composite',
      quality: 'high',
      contributor_count: 4,
      contributors: ['coinbase', 'coinbase'],
    })).toMatchObject({ count: 1, label: 'Low' })
  })

  it('keeps unavailable separate from quality and freshness', () => {
    expect(qualityGradeBadge({
      available: false,
      source: 'okx',
      quality: 'high',
      contributor_count: 3,
      contributors: ['okx', 'coinbase', 'bybit'],
    })).toEqual({ count: 0, label: 'Unavailable', variant: 'accent' })
  })

  it('does not count DEX, perpetual, or external reference identities as CEX support', () => {
    for (const source of ['uniswap', 'pancakeswap', 'hyperliquid', 'coingecko']) {
      expect(qualityGradeBadge({
        available: true,
        source,
        quality: 'high',
        contributor_count: 1,
        contributors: [source],
      })).toEqual({ count: 0, label: 'Unavailable', variant: 'accent' })
    }
  })

  it('does not trust a declared composite count without contributor identities', () => {
    expect(qualityGradeBadge({
      available: true,
      source: 'cex_composite',
      quality: 'high',
      contributor_count: 4,
      contributors: [],
    })).toEqual({ count: 0, label: 'Unavailable', variant: 'accent' })
  })

  it('deduplicates allowlisted CEX identities and ignores mixed non-CEX names', () => {
    expect(qualityGradeBadge({
      available: true,
      source: 'cex_composite',
      quality: 'high',
      contributor_count: 5,
      contributors: ['coinbase', 'coinbase', 'uniswap', 'unknown-provider', 'okx'],
    })).toEqual({ count: 2, label: 'Medium', variant: 'delayed' })
  })
})
