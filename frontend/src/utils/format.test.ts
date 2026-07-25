import { describe, expect, it } from 'vitest'

import { formatPercent } from './format'

describe('formatPercent', () => {
  it('does not render a negative zero after rounding', () => {
    expect(formatPercent(-0.0001)).toBe('0.00%')
  })

  it('keeps real positive and negative changes signed', () => {
    expect(formatPercent(1.234)).toBe('+1.23%')
    expect(formatPercent(-1.234)).toBe('-1.23%')
  })
})
