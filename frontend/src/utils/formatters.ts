/**
 * 格式化缓存 token 数量（1K/1M 缩写）
 */
export function formatCacheTokens(tokens: number): string {
  if (tokens >= 1000000) return `${(tokens / 1000000).toFixed(1)}M`
  if (tokens >= 1000) return `${(tokens / 1000).toFixed(1)}K`
  return tokens.toLocaleString()
}

/**
 * 自适应精度格式化倍率，保留最少 2 位并显示额外有效小数。
 */
export function formatMultiplier(val: number): string {
  if (!Number.isFinite(val)) return '-'
  if (val !== 0 && Math.abs(val) < 0.0001) return val.toPrecision(2)

  const [integer, fraction = ''] = val.toFixed(4).split('.')
  const significantFraction = fraction.replace(/0+$/, '')
  return `${integer}.${significantFraction.padEnd(2, '0')}`
}
