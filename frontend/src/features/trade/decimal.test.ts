import { describe, expect, it } from 'vitest'
import {
  applyBalancePercent,
  formatAtoms,
  parseDecimal,
  previewOrder,
} from './decimal'

describe('trade decimal calculations', () => {
  it('parses and formats exact decimal atoms without JavaScript number arithmetic', () => {
    expect(parseDecimal('64999.01', 6)).toBe(64_999_010_000n)
    expect(parseDecimal('0.000001', 8)).toBe(100n)
    expect(parseDecimal('0.000000001', 8)).toBeNull()
    expect(formatAtoms(64_999_010_000n, 6)).toBe('64999.01')
  })

  it('computes limit buy hold, fee assets, and rule validation exactly', () => {
    const preview = previewOrder({
      side: 'buy',
      type: 'limit',
      timeInForce: 'gtc',
      postOnly: true,
      price: '65000.01',
      quantity: '0.00100000',
      quoteBudget: '',
      availableBTC: '0',
      availableUSDT: '1000',
    })
    expect(preview.valid).toBe(true)
    expect(formatAtoms(preview.heldAtoms, 6)).toBe('65.00001')
    expect(preview.feeAsset).toBe('BTC')
    expect(formatAtoms(preview.takerFeeAtoms, 8)).toBe('0.000002')
  })

  it('aligns balance shortcuts down to market steps and never overspends', () => {
    expect(applyBalancePercent({
      side: 'buy',
      type: 'limit',
      price: '65000',
      availableBTC: '0',
      availableUSDT: '100',
    }, 100)).toEqual({ quantity: '0.001538' })
    expect(applyBalancePercent({
      side: 'sell',
      type: 'market',
      price: '',
      availableBTC: '0.12345678',
      availableUSDT: '0',
    }, 25)).toEqual({ quantity: '0.030864' })
  })

  it('estimates market buy and sell from the current book side without promising fills', () => {
    const buy = previewOrder({
      side: 'buy', type: 'market', timeInForce: 'ioc', postOnly: false,
      price: '', quantity: '', quoteBudget: '100', marketPrice: '65010',
      availableBTC: '0', availableUSDT: '100',
    })
    expect(formatAtoms(buy.takerFeeAtoms, 8)).toBe('0.00000307')
    expect(buy.heldAsset).toBe('USDT')
    const sell = previewOrder({
      side: 'sell', type: 'market', timeInForce: 'ioc', postOnly: false,
      price: '', quantity: '0.001', quoteBudget: '', marketPrice: '64990',
      availableBTC: '0.001', availableUSDT: '0',
    })
    expect(formatAtoms(sell.notionalAtoms, 6)).toBe('64.99')
    expect(formatAtoms(sell.takerFeeAtoms, 6)).toBe('0.12998')
  })

  it('rejects off-tick, below-minimum and incompatible post-only orders', () => {
    const preview = previewOrder({
      side: 'buy',
      type: 'limit',
      timeInForce: 'fok',
      postOnly: true,
      price: '65000.001',
      quantity: '0.000001',
      quoteBudget: '',
      availableBTC: '0',
      availableUSDT: '1000',
    })
    expect(preview.errorKeys).toEqual(expect.arrayContaining([
      'trade.validation.priceTick',
      'trade.validation.minQuantity',
      'trade.validation.minNotional',
      'trade.validation.postOnlyGTC',
    ]))
  })
})
