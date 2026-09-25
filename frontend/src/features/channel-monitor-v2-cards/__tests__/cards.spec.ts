import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import GroupMonitorCard from '../GroupMonitorCard.vue'
import cardSource from '../GroupMonitorCard.vue?raw'
import { recentRecords, percent, platformGroups, appendHistory, groupMultiplier } from '../presentation'
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
    success_requests: 1, error_requests: 0, request_count: 1, token_count: 0, rpm: 0, tpm: 0,
    error_rate: 0.02, cache_rate: 0.853, cache_rate_numerator: 0, cache_rate_denominator: 0,
    ttft: { sample_count: 0, p50_ms: 2100, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null }
  },
  buckets: []
}
describe('independent V2 cards', () => {
  it('preserves the requested display copy without inventing minute samples', () => {
    expect(INTELLIGENCE_CAPTION).toBe('每分钟检测 · 近 60 次 · 仅显示已检测记录')
    const wrapper = mount(GroupMonitorCard, { props: { row, records, now, countdown: 30 }, global: { stubs: { PlatformIcon: true } } })
    expect(wrapper.findAll('.probe-bar')).toHaveLength(6)
    expect(wrapper.text()).toContain(INTELLIGENCE_CAPTION)
    expect(wrapper.text()).toContain('85.3%')
    expect(wrapper.text()).toContain('98.0%')
    expect(wrapper.text()).toContain('2.1s')
    expect(wrapper.findAll('.probe-bar.error')).toHaveLength(1)
    expect(wrapper.findAll('.probe-bar.degraded')).toHaveLength(1)
  })
  it('keeps actual records fixed when only the clock changes', async () => {
    const wrapper = mount(GroupMonitorCard, { props: { row, records, now, countdown: 0 }, global: { stubs: { PlatformIcon: true } } })
    await wrapper.setProps({ now: now + 60 * 60000 })
    expect(wrapper.findAll('.probe-bar')).toHaveLength(6)
    expect(wrapper.findAll('.probe-slot')[59].find('.probe-bar').exists()).toBe(true)
  })
  it('keeps detection space without a baseline for unconfigured OpenAI groups', () => {
    const wrapper = mount(GroupMonitorCard, { props: { row, now, countdown: 60 }, global: { stubs: { PlatformIcon: true } } })
    expect(wrapper.find('[aria-label="降智状态"]').exists()).toBe(true)
    expect(wrapper.findAll('.probe-slot')).toHaveLength(60)
    expect(wrapper.findAll('.probe-bar')).toHaveLength(0)
  })
  it('filters future and invalid times and sorts without changing input', () => {
    const input = [...records, { ...records[0], id: 9, checked_at: 'bad' }, { ...records[0], id: 10, checked_at: new Date(now + 1).toISOString() }]
    expect(recentRecords(input, now).map(r => r.id)).toEqual([6, 5, 4, 3, 2, 1])
    expect(input[0].id).toBe(1)
    expect(percent(undefined)).toBe('—')
    expect(percent(0, false)).toBe('—')
    expect(percent(NaN)).toBe('—')
  })
  it('uses non-clickable probe marks and reveals no detail panel', async () => {
    const wrapper = mount(GroupMonitorCard, { props: { row, records: records.map(r => ({ ...r, answer: 'private answer' })), now, countdown: 0 }, global: { stubs: { PlatformIcon: true } } })
    await wrapper.find('.probe-bar').trigger('click')
    expect(wrapper.find('.probe-bar').element.tagName).toBe('SPAN')
    expect(wrapper.find('.probe-bar').attributes('title')).toContain('2.4s')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(wrapper.findAll('button')).toHaveLength(0)
    expect(wrapper.text()).toContain('21 绿 · 其他黄 · 错误红')
    expect(wrapper.text()).not.toContain('private answer')
  })
})

describe('reference card geometry and density', () => {
 const card = (overrides = {}) => mount(GroupMonitorCard, { props: { row, now, countdown: 30, ...overrides }, global: { stubs: { PlatformIcon: true } } })
 it('uses the Anthropic warm gradient, neutral glyph and orange tag', () => {
  const wrapper = card({ row: { ...row, platform: 'anthropic' } })
  expect(wrapper.find('.platform-icon').classes()).toContain('dark:from-orange-500/10')
  expect(wrapper.find('.platform-icon').classes()).toContain('dark:text-gray-100')
  expect(wrapper.find('.platform-badge').classes()).toContain('dark:text-orange-300')
  expect(wrapper.find('.intelligence-section').exists()).toBe(false)
  expect(wrapper.classes()).toContain('p-5')
  expect(wrapper.classes()).toContain('rounded-[24px]')
 })
 it('preserves slot geometry without drawing either baseline', () => {
  const wrapper = card()
  expect(wrapper.findAll('.history-slot')).toHaveLength(18)
  expect(wrapper.findAll('.history-bar')).toHaveLength(0)
  expect(wrapper.findAll('.passive-track .empty-bar')).toHaveLength(0)
  expect(wrapper.findAll('.probe-track .empty-bar')).toHaveLength(0)
 })
 it('does not stretch sparse passive buckets or sparse probes', () => {
  const wrapper = card({ row: { ...row, buckets: [{ bucket_start: new Date(now).toISOString(), health: { ...row.health, overall: 'warning' }, metrics: row.metrics }] }, records })
  expect(wrapper.findAll('.history-slot')).toHaveLength(18)
  expect(wrapper.findAll('.history-bar')).toHaveLength(1)
  expect(wrapper.find('.history-bar').attributes('style')).toContain('65%')
  expect(wrapper.findAll('.probe-slot')).toHaveLength(60)
  expect(wrapper.findAll('.probe-bar')).toHaveLength(6)
  expect(wrapper.findAll('.probe-track .empty-bar')).toHaveLength(0)
 })
 it('places same-minute results in consecutive record slots', () => {
  const wrapper = card({ records: [records[0], { ...records[0], id: 99, status: 'error' }] })
  expect(wrapper.findAll('.probe-slot')).toHaveLength(60)
  expect(wrapper.findAll('.probe-bar')).toHaveLength(2)
  expect(wrapper.findAll('.probe-slot').filter(slot => slot.findAll('.probe-bar').length === 1)).toHaveLength(2)
 })
 it.each([18, 24, 14, 30])('keeps %i passive slots for the selected range', timelineLength => {
  expect(card({ timelineLength }).findAll('.history-slot')).toHaveLength(timelineLength)
 })
 it('retains full-density history without clock-based motion', async () => {
  const full = Array.from({ length: 60 }, (_, i) => ({ ...records[0], id: i, checked_at: new Date(now - i * 60000).toISOString() }))
  const wrapper = card({ records: full })
  expect(wrapper.findAll('.probe-bar')).toHaveLength(60)
  expect(wrapper.findAll('.probe-track .empty-bar')).toHaveLength(0)
  await wrapper.setProps({ now: now + 61 * 60000 })
  expect(wrapper.findAll('.probe-bar')).toHaveLength(60)
  expect(wrapper.findAll('.probe-track .empty-bar')).toHaveLength(0)
 })
})

describe('passive hover only interaction', () => {
 it('shows a floating tooltip and neighbor animation without opening a click panel', async () => {
  const buckets = Array.from({ length: 18 }, (_, i) => ({ bucket_start: new Date(now - i * 300000).toISOString(), health: row.health, metrics: row.metrics }))
  const wrapper = mount(GroupMonitorCard, { attachTo: document.body, props: { row: { ...row, buckets }, records, now, countdown: 30 } })
  const mark = wrapper.findAll('.history-hitbox')[8]
  expect(mark.element.tagName).toBe('SPAN')
  expect(mark.attributes('title')).toBeUndefined()
  await mark.trigger('click')
  expect(wrapper.find('[role="status"]').exists()).toBe(false)
  await vi.waitFor(() => expect(document.querySelector('[role="tooltip"]')).toBeNull())
  await wrapper.findAll('.history-slot')[8].trigger('mouseenter')
  expect(document.querySelector('[role="tooltip"]')?.textContent).toContain('可用率 98.0% · 缓存率 85.3% · 首 Token 2.1s')
  expect(wrapper.findAll('.history-visual')[8].attributes('style')).toContain('scaleY(1.1)')
  expect(wrapper.findAll('.history-visual')[7].attributes('style')).not.toContain('opacity: 1;')
  await wrapper.findAll('.history-slot')[8].trigger('mouseleave')
  await vi.waitFor(() => expect(document.querySelector('[role="tooltip"]')).toBeNull())
  await mark.trigger('focus')
  expect(document.querySelector('[role="tooltip"]')).not.toBeNull()
  await mark.trigger('keydown', { key: 'Escape' })
  await vi.waitFor(() => expect(document.querySelector('[role="tooltip"]')).toBeNull())
  wrapper.unmount()
 })
})

describe('group-management ordering', () => {
 it('keeps cards in groups.sort_order order with stable id fallback', () => {
  const rows = [
   { ...row, group_id: 20, group_name: 'later', sort_order: 20 },
   { ...row, group_id: 10, group_name: 'first', sort_order: 10 },
   { ...row, group_id: 11, group_name: 'same-order', sort_order: 10 },
  ]
  expect(platformGroups(rows)[0].rows.map(item => item.group_id)).toEqual([10, 11, 20])
 })
})

 describe('configured group multiplier', () => {
  it.each([0, 0.2, 1.5])('renders group rate %s even without usage', (rate) => {
   const wrapper = mount(GroupMonitorCard, { props: { row: { ...row, current_multiplier: rate, health: { ...row.health, overall: 'unknown' } }, now, countdown: 30 }, global: { stubs: { PlatformIcon: true } } })
   expect(wrapper.text()).toContain('用户倍率 ' + rate.toFixed(2) + 'x')
  })
 })

describe('append-only real sample history', () => {
 it('compacts detection records, holds on clock and empty refresh, then appends', async () => {
  const bucket = (offset: number, count = 1) => ({ bucket_start: new Date(now + offset * 60000).toISOString(), health: row.health, metrics: { ...row.metrics, request_count: count, success_requests: count } })
  const real = [bucket(-20), bucket(-10)]
  const wrapper = mount(GroupMonitorCard, { props: { row: { ...row, buckets: [real[0], bucket(-15, 0), real[1]] }, records: [records[0], records[2]], now, countdown: 30 }, global: { stubs: { PlatformIcon: true } } })
  const positions = (slot: string, mark: string) => wrapper.findAll(slot).flatMap((el, i) => el.find(mark).exists() ? [i] : [])
  expect(positions('.probe-slot', '.probe-bar')).toEqual([58, 59])
  await wrapper.setProps({ now: now + 2 * 3600000, records: [], row: { ...row, buckets: [] } })
  expect(positions('.probe-slot', '.probe-bar')).toEqual([58, 59])
  await wrapper.setProps({ records: [{ ...records[0], id: 100, checked_at: new Date(now + 3600000).toISOString() }], row: { ...row, buckets: [bucket(60)] } })
  expect(positions('.probe-slot', '.probe-bar')).toEqual([57, 58, 59])
  wrapper.unmount()
 })
 it('deduplicates refreshes and evicts only when new records exceed capacity', () => {
  const merge = (a: number[], b: number[]) => appendHistory(a,b,x=>x,x=>x,3)
  expect(merge([1,2,3],[])).toEqual([1,2,3])
  expect(merge([1,2,3],[2,3])).toEqual([1,2,3])
  expect(merge([1,2,3],[4])).toEqual([2,3,4])
 })
 it('preserves configured fractional rates rather than rounding 0.065 to 0.07', () => {
  expect(groupMultiplier(0.065)).toBe('0.065x')
  expect(groupMultiplier(0)).toBe('0.00x')
  expect(groupMultiplier(0.2)).toBe('0.20x')
 })
})

describe('right-aligned detection sequence', () => {
 it('pins newest on the right and moves old records left only for a new detection', async () => {
  const first = { ...records[0], id: 101, checked_at: new Date(now - 600000).toISOString(), status: 'normal' as const }
  const second = { ...first, id: 102, checked_at: new Date(now - 60000).toISOString(), status: 'degraded' as const }
  const third = { ...first, id: 103, checked_at: new Date(now).toISOString(), status: 'error' as const }
  const w = mount(GroupMonitorCard, { props: { row, records: [second, first], now, countdown: 30 }, global: { stubs: { PlatformIcon: true } } })
  const marks = () => w.findAll('.probe-slot').flatMap((slot, index) => slot.find('.probe-bar').exists() ? [{ index, title: slot.find('.probe-bar').attributes('title') }] : [])
  expect(marks().map(x => x.index)).toEqual([58, 59])
  expect(marks()[0].title).toContain('正常')
  expect(marks()[1].title).toContain('答案异常')
  const initial = marks()
  await w.setProps({ now: now + 7200000, records: [first, second] })
  expect(marks()).toEqual(initial)
  await w.setProps({ records: [third, second, first] })
  expect(marks().map(x => x.index)).toEqual([57, 58, 59])
  expect(marks()[2].title).toContain('超时或错误')
  await w.setProps({ records: [] })
  expect(marks().map(x => x.index)).toEqual([57, 58, 59])
  w.unmount()
 })
})

it('does not draw gray underline or empty-sample marks for either timeline', () => {
 const w = mount(GroupMonitorCard, { props: { row, records: [], now, countdown: 30 }, global: { stubs: { PlatformIcon: true } } })
 expect(w.findAll('.empty-bar')).toHaveLength(0)
 expect(w.findAll('.history-slot')).toHaveLength(18)
 expect(w.findAll('.probe-slot')).toHaveLength(60)
 expect(cardSource).not.toMatch(/\.timeline-track:{1,2}before/)
 expect(cardSource).not.toContain('empty-bar')
 w.unmount()
})

 it('renders current group model and effort dynamically while preserving the fixed suffix', async () => {
  const w = mount(GroupMonitorCard, { props: { row, records: [], now, countdown: 30, probeMetadata: { model: 'gpt-6-astra', reasoning_effort: 'low' } }, global: { stubs: { PlatformIcon: true } } })
  expect(w.find('.intelligence-caption').text()).toBe('gpt-6-astra · low · 每分钟检测 · 近 60 次 · 仅显示已检测记录')
  await w.setProps({ probeMetadata: { model: 'configured-model', reasoning_effort: 'high' } })
  expect(w.find('.intelligence-caption').text()).toBe('configured-model · high · 每分钟检测 · 近 60 次 · 仅显示已检测记录')
  await w.setProps({ probeMetadata: undefined })
  expect(w.find('.intelligence-caption').text()).toBe('— · — · 每分钟检测 · 近 60 次 · 仅显示已检测记录')
  w.unmount()
 })
