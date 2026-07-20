import { describe, expect, it } from 'vitest'

import { formatMultiplier } from '../formatters'

describe('formatMultiplier', () => {
  it('keeps meaningful multiplier precision without unnecessary trailing zeros', () => {
    expect(formatMultiplier(0)).toBe('0.00')
    expect(formatMultiplier(1)).toBe('1.00')
    expect(formatMultiplier(0.05)).toBe('0.05')
    expect(formatMultiplier(0.085)).toBe('0.085')
    expect(formatMultiplier(0.0851)).toBe('0.0851')
    expect(formatMultiplier(0.001)).toBe('0.001')
    expect(formatMultiplier(0.0001)).toBe('0.0001')
  })

  it('retains precision for values below the database display scale', () => {
    expect(formatMultiplier(0.00001)).toBe('0.000010')
  })

  it('rejects non-finite values', () => {
    expect(formatMultiplier(Number.NaN)).toBe('-')
    expect(formatMultiplier(Number.POSITIVE_INFINITY)).toBe('-')
  })
})
