import type { CustomPlazaTier } from '@/api/customModelPlaza'

export function tierLabel({ min_tokens: min, max_tokens: max }: CustomPlazaTier): string {
  const compact = (n: number) => n >= 1000 && n % 1000 === 0 ? `${n / 1000}K` : String(n)
  if (max > 0 && min > 0) return `>${compact(min)} – ≤${compact(max)}`
  if (max > 0) return `≤${compact(max)}`
  return min > 0 ? `>${compact(min)}` : ''
}

const usd = new Intl.NumberFormat('en-US', {
  style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 10,
})

export function displayPrice(price?: number | null): string {
  return price == null ? '—' : usd.format(price)
}
