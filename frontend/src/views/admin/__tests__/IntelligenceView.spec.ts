import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligenceView from '../IntelligenceView.vue'
import { addIntelligenceModel, deleteIntelligenceModel, getIntelligenceConfigs, getIntelligenceHistory, getIntelligenceRecoveryHistory, getIntelligenceKeyOptions, saveIntelligenceConfig, runIntelligenceCheck, type IntelligenceConfig } from '@/api/intelligence'
import { getAll } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-router', () => ({ onBeforeRouteLeave: vi.fn() }))
vi.mock('@/api/admin/groups', () => ({ getAll: vi.fn() }))
vi.mock('@/api/intelligence', () => ({
  addIntelligenceModel: vi.fn(), deleteIntelligenceModel: vi.fn(), getIntelligenceConfigs: vi.fn(), getIntelligenceHistory: vi.fn(), getIntelligenceRecoveryHistory: vi.fn(), getIntelligenceKeyOptions: vi.fn(), saveIntelligenceConfig: vi.fn(), runIntelligenceCheck: vi.fn()
}))
const config: IntelligenceConfig = {
  group_id: 1, version: 2, enabled: false, interval_minutes: 10, timeout_seconds: 30,
  model: 'actual-model', reasoning_effort: 'low', protocol: 'responses', prompt: 'prompt',
  api_key_id: 12, expected: ['21', '手感'], match_mode: 'contains_any', updated_by: 9
}
const mountView = () => mount(IntelligenceView, { global: { stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } } } })
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(getAll).mockResolvedValue([
    { id: 1, name: '不降智 Group One', platform: 'openai', status: 'active' },
    { id: 2, name: '不降智 Group Two', platform: 'openai', status: 'active' }
  ] as AdminGroup[])
  vi.mocked(getIntelligenceConfigs).mockResolvedValue({ items: [{ ...config, expected: [...config.expected] }], defaults: { ...config, group_id: 0, version: 0, api_key_id: 0 } })
  vi.mocked(getIntelligenceHistory).mockResolvedValue({ items: [], has_more: false })
  vi.mocked(getIntelligenceRecoveryHistory).mockResolvedValue({ items: [], queue: [], has_more: false, server_time: '2026-10-08T01:00:00Z' })
  vi.mocked(getIntelligenceKeyOptions).mockResolvedValue({ items: [
    { id: 12, name: 'Admin probe', group_id: 1, available: true, quota_remaining: -1, expires_at: null },
    { id: 13, name: 'Admin backup', group_id: 1, available: true, quota_remaining: 5, expires_at: '2027-01-01T00:00:00Z' },
    { id: 14, name: 'Expired probe', group_id: 1, available: false, unavailable_reason: 'Key 已过期', quota_remaining: 5, expires_at: '2020-01-01T00:00:00Z' }
  ], page: 1, has_more: false })
})
describe('intelligence admin', () => {
  it('only lists active groups containing 不降智 and selects the first eligible group', async () => {
    vi.mocked(getAll).mockResolvedValue([
      { id: 111, name: '013不降智', platform: 'openai', status: 'inactive' },
      { id: 114, name: 'DeepSeek', platform: 'deepseek', status: 'active' },
      { id: 5, name: '不降智-稳定', platform: 'openai', status: 'active' },
      { id: 99, name: '018不降智(超高缓存)', platform: 'openai', status: 'inactive' },
      { id: 79, name: '不降智-VIP', platform: 'openai', status: 'active' }
    ] as AdminGroup[])
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.findAll('.group-option').map(button => button.text())).toEqual(['不降智-稳定#5', '不降智-VIP#79'])
    expect(wrapper.get('[aria-pressed="true"]').text()).toContain('不降智-稳定')
    expect(wrapper.find('.group-sidebar').text()).not.toContain('013不降智')
    expect(wrapper.find('.group-sidebar').text()).not.toContain('018不降智')
    expect(getIntelligenceHistory).toHaveBeenCalledWith(5, 0)
    expect(getIntelligenceKeyOptions).toHaveBeenCalledWith(5, 1, '', expect.any(AbortSignal))
    wrapper.unmount()
  })
  it('leaves configuration empty when no groups are eligible', async () => {
    vi.mocked(getAll).mockResolvedValue([
      { id: 111, name: '013不降智', platform: 'openai', status: 'inactive' },
      { id: 114, name: 'DeepSeek', platform: 'deepseek', status: 'active' }
    ] as AdminGroup[])
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.findAll('.group-option')).toHaveLength(0)
    expect(wrapper.text()).toContain('暂无分组')
    expect(wrapper.find('.config-panel').exists()).toBe(false)
    expect(getIntelligenceHistory).not.toHaveBeenCalled()
    expect(getIntelligenceKeyOptions).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('opens global recovery records independently of the selected group and preserves unsaved config', async () => {
    const wrapper = mountView(); await flushPromises()
    await wrapper.findAll('textarea')[0].setValue('unsaved config')
    await wrapper.get('[role="tab"][aria-selected="false"]').trigger('click'); await flushPromises()
    expect(getIntelligenceRecoveryHistory).toHaveBeenCalledWith(0, expect.any(AbortSignal))
    expect(wrapper.get('[aria-label="恢复检测调度"]').exists()).toBe(true)
    await wrapper.findAll('[role="tab"]').find(b => b.text() === '分组检测')!.trigger('click')
    expect((wrapper.findAll('textarea')[0].element as HTMLTextAreaElement).value).toBe('unsaved config')
    wrapper.unmount()
  })
  it('saves the configured interval and answers', async () => {
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.text()).toContain('actual-model · low · 自动检测已停用 · 近 60 次 · 仅显示已检测记录')
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
  it('previews the model and effort with the automatic detection state', async () => {
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.findAll('.form-section')).toHaveLength(4)
    expect(wrapper.get('.group-option[aria-pressed="true"]').text()).toContain('Group One')
    await wrapper.get('input[maxlength="128"]').setValue('configured-model')
    const effort = wrapper.findAll('select').find(s => s.find('option[value="xhigh"]').exists())!
    await effort.setValue('high')
    expect(wrapper.get('[data-testid="public-caption"]').text()).toBe('configured-model · high · 自动检测已停用 · 近 60 次 · 仅显示已检测记录')
    expect((wrapper.findAll('input[type="number"]')[0].element as HTMLInputElement).value).toBe('10')
    expect(saveIntelligenceConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('creates, runs, edits and removes the selected additional model without replacing the primary config', async () => {
    vi.mocked(addIntelligenceModel).mockImplementation(async c => ({ ...c, version: 1, probe_id: 21 }))
    vi.mocked(saveIntelligenceConfig).mockImplementation(async c => ({ ...c, version: 2 }))
    vi.mocked(runIntelligenceCheck).mockResolvedValue({ started: true })
    vi.mocked(deleteIntelligenceModel).mockResolvedValue({ deleted: true })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = mountView(); await flushPromises()
    const button = (text: string) => wrapper.findAll('button').find(b => b.text() === text)!
    const model = wrapper.get('input[maxlength="128"]')
    try {
      await button('添加检测模型').trigger('click')
      await model.setValue(config.model)
      expect(button('保存配置').attributes('disabled')).toBeDefined()
      await model.setValue('second-model')
      await button('保存配置').trigger('click'); await flushPromises()
      expect(addIntelligenceModel).toHaveBeenCalledWith(expect.objectContaining({ group_id: 1, enabled: false, version: 0, model: 'second-model' }))
      expect(wrapper.findAll('button.model-option')).toHaveLength(2)
      expect(getIntelligenceHistory).toHaveBeenLastCalledWith(1, 0, 21)
      await button('立即检测').trigger('click'); await flushPromises()
      expect(runIntelligenceCheck).toHaveBeenCalledWith(1, 21)
      await model.setValue('updated-second-model')
      await button('保存配置').trigger('click'); await flushPromises()
      expect(saveIntelligenceConfig).toHaveBeenCalledWith(expect.objectContaining({ probe_id: 21, version: 1, model: 'updated-second-model' }))
      await button('移除模型').trigger('click'); await flushPromises()
      expect(deleteIntelligenceModel).toHaveBeenCalledWith(1, 21, 2)
      expect(wrapper.findAll('button.model-option')).toHaveLength(1)
      expect((model.element as HTMLInputElement).value).toBe(config.model)
      expect(getIntelligenceHistory).toHaveBeenLastCalledWith(1, 0)
    } finally {
      confirm.mockRestore()
      wrapper.unmount()
    }
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
  it('selects an owned group key and saves its numeric ID without credentials', async () => {
    const wrapper = mountView(); await flushPromises()
    expect(getIntelligenceKeyOptions).toHaveBeenCalledWith(1, 1, '', expect.any(AbortSignal))
    const picker = wrapper.get('[data-testid="intelligence-key-picker"]')
    expect(picker.element.tagName).toBe('SELECT')
    expect(wrapper.findAll('input[type="number"]')).toHaveLength(2)
    expect(picker.text()).toContain('Admin probe · #12 · 不限额 · 不过期')
    expect(picker.find('option[value="14"]').attributes('disabled')).toBeDefined()
    expect(picker.text()).toContain('Key 已过期')
    await picker.setValue('13')
    vi.mocked(saveIntelligenceConfig).mockImplementation(async c => ({ ...c, version: 3 }))
    await wrapper.findAll('button').find(b => b.text() === '保存配置')!.trigger('click')
    await flushPromises()
    expect(saveIntelligenceConfig).toHaveBeenCalledWith(expect.objectContaining({ api_key_id: 13, group_id: 1 }))
    expect(saveIntelligenceConfig).toHaveBeenCalledTimes(1)
    expect(vi.mocked(saveIntelligenceConfig).mock.calls[0][0]).not.toHaveProperty('key')
    expect(wrapper.text()).toContain('配置已保存')
    wrapper.unmount()
  })
  it('blocks automatic enablement without a key and explains the empty list', async () => {
    vi.mocked(getIntelligenceKeyOptions).mockResolvedValue({ items: [], page: 1, has_more: false })
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.text()).toContain('未找到绑定本分组的 API Key')
    await wrapper.get('[data-testid="intelligence-key-picker"]').setValue('0')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    expect(wrapper.findAll('button').find(b => b.text() === '保存配置')!.attributes('disabled')).toBeDefined()
    expect(saveIntelligenceConfig).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('disables a saved unavailable key rather than silently replacing it', async () => {
    vi.mocked(getIntelligenceKeyOptions).mockResolvedValue({ items: [
      { id: 12, name: 'Quota exhausted', group_id: 1, available: false, quota_remaining: 0, expires_at: null, unavailable_reason: 'Key 额度已用完' }
    ], page: 1, has_more: false })
    const wrapper = mountView(); await flushPromises()
    expect((wrapper.get('[data-testid="intelligence-key-picker"]').element as HTMLSelectElement).value).toBe('12')
    expect(wrapper.findAll('button').find(b => b.text() === '立即检测')!.attributes('disabled')).toBeDefined()
    await wrapper.findAll('textarea')[0].setValue('updated')
    expect(wrapper.findAll('button').find(b => b.text() === '保存配置')!.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('preserves the saved ID and blocks actions on a key loading failure until retry', async () => {
    vi.mocked(getIntelligenceKeyOptions).mockRejectedValueOnce(new Error('Key endpoint unavailable'))
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.text()).toContain('Key endpoint unavailable')
    expect(wrapper.text()).toContain('已保存 Key #12')
    expect(wrapper.findAll('button').find(b => b.text() === '立即检测')!.attributes('disabled')).toBeDefined()
    await wrapper.findAll('button').find(b => b.text() === '刷新 Key')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).not.toContain('Key endpoint unavailable')
    expect((wrapper.get('[data-testid="intelligence-key-picker"]').element as HTMLSelectElement).value).toBe('12')
    expect(wrapper.findAll('button').find(b => b.text() === '立即检测')!.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
  it('aborts and ignores a late key response after switching groups', async () => {
    let resolveOld!: (result: Awaited<ReturnType<typeof getIntelligenceKeyOptions>>) => void
    vi.mocked(getIntelligenceKeyOptions).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = mountView(); await flushPromises()
    const signal = vi.mocked(getIntelligenceKeyOptions).mock.calls[0][3]!
    expect(wrapper.get('[data-testid="intelligence-key-picker"]').attributes('disabled')).toBeDefined()
    await wrapper.findAll('button').find(b => b.text().includes('Group Two'))!.trigger('click'); await flushPromises()
    expect(signal.aborted).toBe(true)
    resolveOld({ items: [{ id: 77, name: 'Stale key', group_id: 1, available: true, quota_remaining: -1, expires_at: null }], page: 1, has_more: false })
    await flushPromises()
    expect(wrapper.text()).not.toContain('Stale key')
    expect(wrapper.text()).not.toContain('Admin probe')
    expect((wrapper.get('[data-testid="intelligence-key-picker"]').element as HTMLSelectElement).value).toBe('0')
    wrapper.unmount()
  })
  it('searches and pages keys without clearing the saved selection', async () => {
    const wrapper = mountView(); await flushPromises()
    vi.mocked(getIntelligenceKeyOptions).mockResolvedValueOnce({ items: [
      { id: 20, name: 'Search result', group_id: 1, available: true, quota_remaining: -1, expires_at: null }
    ], page: 1, has_more: true })
    await wrapper.get('[aria-label="搜索管理员 API Key"]').setValue('  search  ')
    await wrapper.get('[aria-label="搜索管理员 API Key"]').trigger('keydown', { key: 'Enter' }); await flushPromises()
    expect(getIntelligenceKeyOptions).toHaveBeenLastCalledWith(1, 1, 'search', expect.any(AbortSignal))
    expect((wrapper.get('[data-testid="intelligence-key-picker"]').element as HTMLSelectElement).value).toBe('12')
    vi.mocked(getIntelligenceKeyOptions).mockResolvedValueOnce({ items: [
      { id: 20, name: 'Search result', group_id: 1, available: true, quota_remaining: -1, expires_at: null },
      { id: 21, name: 'Next page', group_id: 1, available: true, quota_remaining: -1, expires_at: null }
    ], page: 2, has_more: false })
    await wrapper.get('[aria-label="搜索管理员 API Key"]').setValue('unsubmitted')
    await wrapper.findAll('button').find(b => b.text() === '更多')!.trigger('click'); await flushPromises()
    expect(getIntelligenceKeyOptions).toHaveBeenLastCalledWith(1, 2, 'search', expect.any(AbortSignal))
    expect(wrapper.findAll('option[value="20"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('Next page')
    expect(wrapper.text()).toContain('已保存 Key #12')
    wrapper.unmount()
  })
})
