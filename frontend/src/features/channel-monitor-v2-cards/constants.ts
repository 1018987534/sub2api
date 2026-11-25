import type { IntelligenceMetadata } from '@/api/intelligence'

// Fallback copy for older status responses without cadence metadata.
export const INTELLIGENCE_CAPTION = '每分钟检测 · 近 60 次 · 仅显示已检测记录'
export const INTELLIGENCE_LEGEND = '正常绿 · 异常黄 · 错误红'
export const INTELLIGENCE_WINDOW_MS = 60 * 60 * 1000

export function formatIntelligenceCaption(metadata?: IntelligenceMetadata): string {
 const name = metadata?.model.trim() || '—'
 const effort = metadata?.protocol === 'messages' ? '' : ` · ${metadata?.reasoning_effort || '—'}`
 const cadence = metadata?.enabled === false ? '自动检测已停用' : metadata?.interval_minutes ? `每 ${metadata.interval_minutes} 分钟检测` : ''
 return `${name}${effort} · ${cadence ? cadence + ' · 近 60 次 · 仅显示已检测记录' : INTELLIGENCE_CAPTION}`
}
