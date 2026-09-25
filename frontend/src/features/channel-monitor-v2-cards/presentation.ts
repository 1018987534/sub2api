import type { IntelligenceRecord } from '@/api/intelligence'
import type { MonitorMatrixRow, MonitorMetric, HealthState } from '@/api/channelMonitorV2'
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
 return [...grouped].map(([platform, items]) => ({ platform, rows: items }))
}
export function platformName(platform: string): string {
 return ({ openai: 'OpenAI', anthropic: 'Anthropic', claude: 'Anthropic', gemini: 'Gemini', antigravity: 'Antigravity', grok: 'Grok' } as Record<string, string>)[platform] || platform
}
