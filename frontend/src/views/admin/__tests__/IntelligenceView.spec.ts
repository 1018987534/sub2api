import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligenceView from '../IntelligenceView.vue'
import { getIntelligenceConfigs, getIntelligenceHistory, saveIntelligenceConfig, runIntelligenceCheck, type IntelligenceConfig } from '@/api/intelligence'
import { getAllIncludingInactive } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-router', () => ({ onBeforeRouteLeave: vi.fn() }))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: vi.fn() }))
vi.mock('@/api/intelligence', () => ({
  getIntelligenceConfigs: vi.fn(), getIntelligenceHistory: vi.fn(), saveIntelligenceConfig: vi.fn(), runIntelligenceCheck: vi.fn()
}))
const config: IntelligenceConfig = {
  group_id: 1, version: 2, enabled: false, interval_minutes: 10, timeout_seconds: 30,
  model: 'actual-model', reasoning_effort: 'low', protocol: 'responses', prompt: 'prompt',
  api_key_id: 12, expected: ['21', '手感'], match_mode: 'contains_any', updated_by: 9
}
const mountView = () => mount(IntelligenceView, { global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } } } })
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getAllIncludingInactive).mockResolvedValue([
    { id: 1, name: 'Group One', platform: 'openai', status: 'active' },
    { id: 2, name: 'Group Two', platform: 'openai', status: 'active' }
  ] as AdminGroup[])
  vi.mocked(getIntelligenceConfigs).mockResolvedValue({ items: [{ ...config, expected: [...config.expected] }], defaults: { ...config, group_id: 0, version: 0, api_key_id: 0 } })
  vi.mocked(getIntelligenceHistory).mockResolvedValue({ items: [], has_more: false })
})
describe('intelligence admin', () => {
  it('saves actual interval and keyword changes while keeping the fixed public caption', async () => {
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.text()).toContain('gpt-6-astra · low · 每分钟检测 · 近 60 分钟 · 仅显示已检测记录')
    const textareas = wrapper.findAll('textarea')
    await textareas[1].setValue('42\nnew')
    await wrapper.findAll('input[type="number"]')[0].setValue(15)
    vi.mocked(saveIntelligenceConfig).mockImplementation(async c => ({ ...c, version: 3 }))
    await wrapper.findAll('button').find(b => b.text() === '保存配置')!.trigger('click')
    await flushPromises()
    expect(saveIntelligenceConfig).toHaveBeenCalledWith(expect.objectContaining({ interval_minutes: 15, expected: ['42', 'new'], version: 2, model: 'actual-model' }))
    expect(wrapper.text()).toContain('实际运行间隔为 15 分钟')
    wrapper.unmount()
  })
  it('reports manual enqueue failure and does not synthesize a successful result', async () => {
    const wrapper = mountView(); await flushPromises()
    vi.mocked(runIntelligenceCheck).mockRejectedValue({ response: { data: { message: '检测正在运行' } } })
    await wrapper.findAll('button').find(b => b.text() === '立即检测')!.trigger('click'); await flushPromises()
    expect(runIntelligenceCheck).toHaveBeenCalledWith(1)
    expect(wrapper.text()).toContain('检测正在运行')
    expect(wrapper.text()).toContain('暂无已检测记录')
    wrapper.unmount()
  })
  it('ignores a late history response from a previously selected group', async () => {
    let resolveOld!: (result: Awaited<ReturnType<typeof getIntelligenceHistory>>) => void
    vi.mocked(getIntelligenceHistory).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = mountView(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text().includes('Group Two'))!.trigger('click')
    await flushPromises()
    resolveOld({ items: [{ id: 99, group_id: 1, checked_at: '2026-09-25T12:00:00Z', duration_ms: 100, status: 'normal', answer: 'old group secret' }], has_more: false })
    await flushPromises()
    expect(wrapper.text()).not.toContain('#99')
    expect(wrapper.text()).toContain('暂无已检测记录')
    wrapper.unmount()
  })
  it('shows conflicts without overwriting the unsaved draft', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.findAll('textarea')[0].setValue('unsaved prompt')
    vi.mocked(saveIntelligenceConfig).mockRejectedValue({ response: { data: { message: 'configuration changed' } } })
    await wrapper.findAll('button').find(b => b.text() === '保存配置')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('configuration changed')
    expect(wrapper.findAll('textarea')[0].element.value).toBe('unsaved prompt')
    wrapper.unmount()
  })
})
