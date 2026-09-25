import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import GroupMonitorCard from '../GroupMonitorCard.vue'
import { recentRecords, percent } from '../presentation'
import { INTELLIGENCE_CAPTION } from '../constants'
import type { IntelligenceRecord } from '@/api/intelligence'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'

const now = Date.parse('2026-09-25T12:00:00Z')
const records: IntelligenceRecord[] = Array.from({ length: 6 }, (_, i) => ({
  id: i + 1, group_id: 1, checked_at: new Date(now - (i * 10 + 1) * 60000).toISOString(),
  duration_ms: 2400, status: i === 1 ? 'degraded' : i === 2 ? 'error' : 'normal'
}))
const row: MonitorMatrixRow = {
  platform: 'openai', group_id: 1, group_name: '测试分组', current_multiplier: 0.5,
  health: { overall: 'healthy', error_rate: 'healthy', ttft: 'healthy', minimum_sample: 5 },
  metrics: {
    success_requests: 0, error_requests: 0, request_count: 0, token_count: 0, rpm: 0, tpm: 0,
    error_rate: 0.02, cache_rate: 0.853, cache_rate_numerator: 0, cache_rate_denominator: 0,
    ttft: { sample_count: 0, p50_ms: 2100, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null }
  },
  buckets: []
}
describe('independent V2 cards', () => {
  it('preserves the requested display copy without inventing minute samples', () => {
    expect(INTELLIGENCE_CAPTION).toBe('gpt-6-astra · low · 每分钟检测 · 近 60 分钟 · 仅显示已检测记录')
    const wrapper = mount(GroupMonitorCard, { props: { row, records, now, countdown: 30 }, global: { stubs: { PlatformIcon: true } } })
    expect(wrapper.findAll('.probe-bar')).toHaveLength(6)
    expect(wrapper.text()).toContain(INTELLIGENCE_CAPTION)
    expect(wrapper.text()).toContain('85.3%')
    expect(wrapper.text()).toContain('98.0%')
    expect(wrapper.text()).toContain('2.1s')
    expect(wrapper.findAll('.probe-bar.error')).toHaveLength(1)
    expect(wrapper.findAll('.probe-bar.degraded')).toHaveLength(1)
  })
  it('ages out real records and never maps no-data to green', async () => {
    const wrapper = mount(GroupMonitorCard, { props: { row, records, now, countdown: 0 }, global: { stubs: { PlatformIcon: true } } })
    await wrapper.setProps({ now: now + 60 * 60000 })
    expect(wrapper.findAll('.probe-bar')).toHaveLength(0)
    expect(wrapper.text()).toContain('暂无检测')
    expect(wrapper.text()).toContain('近 60 分钟没有已完成检测')
  })
  it('omits an unconfigured detection block', () => {
    const wrapper = mount(GroupMonitorCard, { props: { row, now, countdown: 60 }, global: { stubs: { PlatformIcon: true } } })
    expect(wrapper.find('[aria-label="降智状态"]').exists()).toBe(false)
  })
  it('filters future and invalid times and sorts without changing input', () => {
    const input = [...records, { ...records[0], id: 9, checked_at: 'bad' }, { ...records[0], id: 10, checked_at: new Date(now + 1).toISOString() }]
    expect(recentRecords(input, now).map(r => r.id)).toEqual([6, 5, 4, 3, 2, 1])
    expect(input[0].id).toBe(1)
    expect(percent(undefined)).toBe('—')
    expect(percent(0, false)).toBe('—')
    expect(percent(NaN)).toBe('—')
  })
  it('reveals only the timestamp, status and duration on click', async () => {
    const wrapper = mount(GroupMonitorCard, { props: { row, records: records.map(r => ({ ...r, answer: 'private answer' })), now, countdown: 0 }, global: { stubs: { PlatformIcon: true } } })
    await wrapper.find('.probe-bar').trigger('click')
    expect(wrapper.find('[role="status"]').text()).toContain('2.4s')
    expect(wrapper.text()).not.toContain('private answer')
  })
})
