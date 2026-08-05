import type { MessageKey } from '../../i18n'

export const TRADE_MARKET_RULES = {
  baseAsset: 'BTC',
  quoteAsset: 'USDT',
  baseScale: 100_000_000n,
  quoteScale: 1_000_000n,
  priceTick: 10_000n,
  quantityStep: 100n,
  minimumQuantity: 1_000n,
  minimumNotional: 5_000_000n,
  makerFeeBPS: 10n,
  takerFeeBPS: 20n,
} as const

export interface OrderPreviewInput {
  side: 'buy' | 'sell'
  type: 'limit' | 'market'
  timeInForce: 'gtc' | 'ioc' | 'fok'
  postOnly: boolean
  price: string
  quantity: string
  quoteBudget: string
  availableBTC: string
  availableUSDT: string
  marketPrice?: string
}

export interface OrderPreview {
  valid: boolean
  errorKeys: MessageKey[]
  priceAtoms: bigint
  quantityAtoms: bigint
  quoteBudgetAtoms: bigint
  notionalAtoms: bigint
  heldAtoms: bigint
  heldAsset: 'BTC' | 'USDT'
  makerFeeAtoms: bigint
  takerFeeAtoms: bigint
  feeAsset: 'BTC' | 'USDT'
  receiveMakerAtoms: bigint
  receiveTakerAtoms: bigint
}

export function parseDecimal(value: string, precision: number): bigint | null {
  const normalized = value.trim()
  if (!/^(?:0|[1-9]\d*)(?:\.\d+)?$/.test(normalized)) return null
  const [whole, fraction = ''] = normalized.split('.')
  if (fraction.length > precision) return null
  try {
    return BigInt(whole) * 10n ** BigInt(precision) +
      BigInt((fraction + '0'.repeat(precision)).slice(0, precision))
  } catch {
    return null
  }
}

export function formatAtoms(value: bigint, precision: number, trim = true): string {
  const negative = value < 0n
  const absolute = negative ? -value : value
  const divisor = 10n ** BigInt(precision)
  const whole = absolute / divisor
  let fraction = (absolute % divisor).toString().padStart(precision, '0')
  if (trim) fraction = fraction.replace(/0+$/, '')
  const formatted = fraction ? `${whole}.${fraction}` : whole.toString()
  return negative ? `-${formatted}` : formatted
}

export function floorToStep(value: bigint, step: bigint): bigint {
  if (value <= 0n) return 0n
  return value - value % step
}

export function mulDivFloor(left: bigint, right: bigint, divisor: bigint): bigint {
  return left * right / divisor
}

export function mulDivCeil(left: bigint, right: bigint, divisor: bigint): bigint {
  const product = left * right
  return product === 0n ? 0n : (product + divisor - 1n) / divisor
}

function safeAtoms(value: string, precision: number): bigint {
  return parseDecimal(value, precision) ?? 0n
}

export function previewOrder(input: OrderPreviewInput): OrderPreview {
  const priceAtoms = parseDecimal(input.price, 6) ?? 0n
  const quantityAtoms = parseDecimal(input.quantity, 8) ?? 0n
  const quoteBudgetAtoms = parseDecimal(input.quoteBudget, 6) ?? 0n
  const availableBTC = safeAtoms(input.availableBTC, 8)
  const availableUSDT = safeAtoms(input.availableUSDT, 6)
  const errors: MessageKey[] = []

  if (input.type === 'limit') {
    if (priceAtoms <= 0n) errors.push('trade.validation.invalidPrice')
    else if (priceAtoms % TRADE_MARKET_RULES.priceTick !== 0n) {
      errors.push('trade.validation.priceTick')
    }
  }
  if (input.type === 'market' && input.side === 'buy') {
    if (quoteBudgetAtoms <= 0n) errors.push('trade.validation.invalidBudget')
    else if (quoteBudgetAtoms < TRADE_MARKET_RULES.minimumNotional) {
      errors.push('trade.validation.minNotional')
    }
    if (quoteBudgetAtoms > availableUSDT) errors.push('trade.validation.insufficientUSDT')
  } else {
    if (quantityAtoms <= 0n) errors.push('trade.validation.invalidQuantity')
    else if (quantityAtoms % TRADE_MARKET_RULES.quantityStep !== 0n) {
      errors.push('trade.validation.quantityStep')
    } else if (quantityAtoms < TRADE_MARKET_RULES.minimumQuantity) {
      errors.push('trade.validation.minQuantity')
    }
    if (input.side === 'sell' && quantityAtoms > availableBTC) {
      errors.push('trade.validation.insufficientBTC')
    }
  }
  if (input.postOnly && (input.type !== 'limit' || input.timeInForce !== 'gtc')) {
    errors.push('trade.validation.postOnlyGTC')
  }

  const marketPriceAtoms = parseDecimal(input.marketPrice ?? '', 6) ?? 0n
  const pricingPriceAtoms = input.type === 'limit' ? priceAtoms : marketPriceAtoms
  const estimatedQuantityAtoms = input.type === 'market' && input.side === 'buy' && pricingPriceAtoms > 0n
    ? floorToStep(
      mulDivFloor(quoteBudgetAtoms, TRADE_MARKET_RULES.baseScale, pricingPriceAtoms),
      TRADE_MARKET_RULES.quantityStep,
    )
    : quantityAtoms
  const notionalAtoms = input.type === 'market' && input.side === 'buy'
    ? quoteBudgetAtoms
    : pricingPriceAtoms > 0n && quantityAtoms > 0n
      ? mulDivFloor(pricingPriceAtoms, quantityAtoms, TRADE_MARKET_RULES.baseScale)
      : 0n
  if (input.type === 'limit' && notionalAtoms < TRADE_MARKET_RULES.minimumNotional) {
    errors.push('trade.validation.minNotional')
  }

  const heldAsset = input.side === 'buy' ? 'USDT' : 'BTC'
  const heldAtoms = input.side === 'buy'
    ? input.type === 'market'
      ? quoteBudgetAtoms
      : mulDivCeil(priceAtoms, quantityAtoms, TRADE_MARKET_RULES.baseScale)
    : quantityAtoms
  if (input.side === 'buy' && heldAtoms > availableUSDT) {
    errors.push('trade.validation.insufficientUSDT')
  }

  const feeAsset = input.side === 'buy' ? 'BTC' : 'USDT'
  const feeBasis = input.side === 'buy' ? estimatedQuantityAtoms : notionalAtoms
  const makerFeeAtoms = mulDivFloor(feeBasis, TRADE_MARKET_RULES.makerFeeBPS, 10_000n)
  const takerFeeAtoms = mulDivFloor(feeBasis, TRADE_MARKET_RULES.takerFeeBPS, 10_000n)
  const receiveBasis = input.side === 'buy' ? estimatedQuantityAtoms : notionalAtoms

  return {
    valid: errors.length === 0,
    errorKeys: [...new Set(errors)],
    priceAtoms,
    quantityAtoms,
    quoteBudgetAtoms,
    notionalAtoms,
    heldAtoms,
    heldAsset,
    makerFeeAtoms,
    takerFeeAtoms,
    feeAsset,
    receiveMakerAtoms: receiveBasis > makerFeeAtoms ? receiveBasis - makerFeeAtoms : 0n,
    receiveTakerAtoms: receiveBasis > takerFeeAtoms ? receiveBasis - takerFeeAtoms : 0n,
  }
}

export function applyBalancePercent(
  input: Pick<OrderPreviewInput, 'side' | 'type' | 'price' | 'availableBTC' | 'availableUSDT'>,
  percent: 25 | 50 | 75 | 100,
): { quantity?: string; quoteBudget?: string } {
  if (input.side === 'buy' && input.type === 'market') {
    const available = safeAtoms(input.availableUSDT, 6)
    const budget = floorToStep(available * BigInt(percent) / 100n, 1n)
    return { quoteBudget: formatAtoms(budget, 6) }
  }
  if (input.side === 'sell') {
    const available = safeAtoms(input.availableBTC, 8)
    const quantity = floorToStep(
      available * BigInt(percent) / 100n,
      TRADE_MARKET_RULES.quantityStep,
    )
    return { quantity: formatAtoms(quantity, 8) }
  }
  const price = parseDecimal(input.price, 6) ?? 0n
  if (price <= 0n) return { quantity: '0' }
  const available = safeAtoms(input.availableUSDT, 6)
  const budget = available * BigInt(percent) / 100n
  const affordable = floorToStep(
    mulDivFloor(budget, TRADE_MARKET_RULES.baseScale, price),
    TRADE_MARKET_RULES.quantityStep,
  )
  return { quantity: formatAtoms(affordable, 8) }
}
