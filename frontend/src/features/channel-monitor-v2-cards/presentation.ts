import type { IntelligenceRecord } from '@/api/intelligence'
import type { MonitorMatrixRow, MonitorMetric, HealthState, MonitorMatrixBucket } from '@/api/channelMonitorV2'
import { INTELLIGENCE_WINDOW_MS } from './constants'

export const statusLabels = { normal: '正常', degraded: '答案异常', error: '超时或错误' }
export const healthLabels: Record<HealthState, string> = { healthy: '正常', warning: '波动', critical: '异常', unknown: '样本不足' }
export function recentRecords(records: IntelligenceRecord[], now: number): IntelligenceRecord[] {
 return records.filter(r => { const time = Date.parse(r.checked_at); return time >= now - INTELLIGENCE_WINDOW_MS && time <= now })
  .sort((a, b) => Date.parse(a.checked_at) - Date.parse(b.checked_at) || a.id - b.id)
}
export function percent(value: number | undefined, known = true): string {
 return known && value != null && Number.isFinite(value) ? (value * 100).toFixed(1) + '%' : '—'
}
export function latency(metrics: MonitorMetric): string {
 const ms = metrics.ttft.p50_ms
 return ms != null && Number.isFinite(ms) ? (ms / 1000).toFixed(1) + 's' : '—'
}
export function platformGroups(rows: MonitorMatrixRow[]): Array<{ platform: string; rows: MonitorMatrixRow[] }> {
 const grouped = new Map<string, MonitorMatrixRow[]>()
 for (const row of rows) {
  if (!row.group_id) continue
  if (!grouped.has(row.platform)) grouped.set(row.platform, [])
  grouped.get(row.platform)!.push(row)
 }
 const order = ['openai', 'anthropic', 'grok', 'gemini']
 return [...grouped].map(([platform, items]) => ({
  platform,
  rows: [...items].sort((a, b) => {
   const sortOrder = (a.sort_order ?? 0) - (b.sort_order ?? 0)
   if (sortOrder !== 0) return sortOrder
   const groupID = (a.group_id ?? 0) - (b.group_id ?? 0)
   if (groupID !== 0) return groupID
   return (a.group_name || '').localeCompare(b.group_name || '')
  }),
 })).sort((a, b) => (order.indexOf(a.platform) < 0 ? 99 : order.indexOf(a.platform)) - (order.indexOf(b.platform) < 0 ? 99 : order.indexOf(b.platform)) || a.platform.localeCompare(b.platform))
}
export function platformName(platform: string): string {
 return ({ openai: 'OpenAI', anthropic: 'Anthropic', claude: 'Anthropic', gemini: 'Gemini', antigravity: 'Antigravity', grok: 'Grok' } as Record<string, string>)[platform] || platform
}

export function platformIconClass(platform: string): string {
 const colors: Record<string, string> = {
  openai: 'from-emerald-50 to-emerald-100 dark:from-emerald-500/10 dark:to-emerald-500/20',
  anthropic: 'from-orange-50 to-amber-100 dark:from-orange-500/10 dark:to-amber-500/20',
  gemini: 'from-sky-50 to-indigo-100 dark:from-sky-500/10 dark:to-indigo-500/20',
  grok: 'from-zinc-50 to-neutral-200 dark:from-zinc-500/10 dark:to-neutral-500/20'
 }
 return 'bg-gradient-to-br ' + (colors[platform === 'claude' ? 'anthropic' : platform] || 'from-gray-50 to-gray-100 dark:from-gray-500/10 dark:to-gray-500/20')
}
export function platformBadgeClass(platform: string): string {
 return ({ openai: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300', anthropic: 'bg-orange-100 text-orange-700 dark:bg-orange-500/15 dark:text-orange-300', claude: 'bg-orange-100 text-orange-700 dark:bg-orange-500/15 dark:text-orange-300', gemini: 'bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300', grok: 'bg-zinc-100 text-zinc-700 dark:bg-zinc-500/15 dark:text-zinc-300' } as Record<string, string>)[platform] || 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
}
export const barHeight = (bucket: MonitorMatrixBucket): number => ({ healthy: 100, warning: 65, critical: 35, unknown: 15 })[bucket.health.overall]
// Match the reference availability palette; insufficient samples remain gray.
export function passiveColor(bucket: MonitorMatrixBucket): string {
 const availability = (1 - bucket.metrics.error_rate) * 100
 if (bucket.health.overall === 'unknown' || !Number.isFinite(availability)) return 'bg-gray-300 dark:bg-dark-600'
 if (availability < 30) return 'bg-gray-950 dark:bg-black'
 if (availability < 50) return 'bg-red-500 dark:bg-red-400'
 if (availability < 60) return 'bg-amber-400 dark:bg-amber-300'
 if (availability < 80) return 'bg-yellow-300 dark:bg-yellow-200'
 if (availability < 90) return 'bg-emerald-400 dark:bg-emerald-300'
 return 'bg-emerald-600 dark:bg-emerald-400'
}
export function probeMinuteSlots(records: IntelligenceRecord[], now: number): IntelligenceRecord[][] {
 const slots = Array.from({ length: 60 }, () => [] as IntelligenceRecord[])
 for (const record of recentRecords(records, now)) {
  // Keep every actual result, including multiple manual checks in one minute.
  const slot = Math.max(0, 59 - Math.floor((now - Date.parse(record.checked_at)) / 60000))
  slots[slot].push(record)
 }
 return slots
}

// Keep loaded history stable across clock ticks and empty rolling-window refreshes.
// Only actual incoming records can append, update an existing sample or evict the oldest.
export function appendHistory<T>(previous: T[], incoming: T[], id: (item: T) => string | number, time: (item: T) => number, limit: number): T[] {
 const merged = new Map(previous.map(item => [id(item), item]))
 for (const item of incoming) if (Number.isFinite(time(item))) merged.set(id(item), item)
 return [...merged.values()].sort((a, b) => time(a) - time(b) || String(id(a)).localeCompare(String(id(b)), undefined, { numeric: true })).slice(-limit)
}
export function groupMultiplier(value: number | undefined): string {
 if (value == null || !Number.isFinite(value)) return '—'
 return value.toFixed(4).replace(/(\.\d{2})0+$/, '$1').replace(/(\.\d{2,}?)0+$/, '$1') + 'x'
}
