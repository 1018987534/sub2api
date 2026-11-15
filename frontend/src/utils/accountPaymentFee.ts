export const isValidPaymentFeePercent = (value: unknown): value is number =>
  typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 100

export const readPaymentFeePercent = (extra: unknown): number => {
  const value = (extra as Record<string, unknown> | undefined)?.upstream_payment_fee_percent
  return isValidPaymentFeePercent(value) ? value : 0
}

export const applyPaymentFee = (rate: number, percent: number): number =>
  Math.round(rate * (1 + percent / 100) * 1e12) / 1e12
