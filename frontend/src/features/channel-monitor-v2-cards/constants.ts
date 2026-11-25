import type { IntelligenceMetadata } from '@/api/intelligence'

export const INTELLIGENCE_CAPTION = '每 1 分钟检测 · 近 60 次 · 仅显示已检测记录'
export const INTELLIGENCE_LEGEND = '正常绿 · 异常黄 · 错误红'
export const INTELLIGENCE_WINDOW_MS = 60 * 60 * 1000

export function formatIntelligenceModelLabel(metadata?: IntelligenceMetadata): string {
 const name = metadata?.model.trim() || '—'
 const effort = metadata?.protocol === 'messages' ? '' : ` · ${metadata?.reasoning_effort || '—'}`
 return `${name}${effort}`
}
