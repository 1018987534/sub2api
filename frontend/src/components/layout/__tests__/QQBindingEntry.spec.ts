import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import QQBindingEntry from '../QQBindingEntry.vue'
import QQBindingForm from '@/components/user/QQBindingForm.vue'

const api = vi.hoisted(() => ({ binding: vi.fn(), unbind: vi.fn() }))
vi.mock('@/api/qqActivity', () => ({ qqActivityAPI: api }))
const bound = { bound: true, qq_email: '12****78@qq.com' }
let view: VueWrapper | undefined
function render() {
  view = mount(QQBindingEntry, { global: { stubs: {
    BaseDialog: { props: ['show'], template: '<section v-if="show"><slot /></section>' },
    QQBindingForm: { props: ['intro', 'note'], template: '<form data-testid="mail-proof" />' },
    Icon: true,
  } } })
  return view
}
async function click(text: string) {
  const button = view!.findAll('button').find(b => b.text() === text)
  expect(button).toBeDefined()
  await button!.trigger('click')
  await flushPromises()
}
beforeEach(() => { vi.clearAllMocks(); api.binding.mockResolvedValue(bound); api.unbind.mockResolvedValue({ bound: false }) })
afterEach(() => { view?.unmount(); view = undefined })

describe('header QQ binding status and unbinding', () => {
  const config = () => ({ status: 'active', binding_display: { group_number: '87654321', activity_title: '本月活动', activity_text: '第一行\n第二行 <script>alert(1)</script>', binding_intro: '自定义绑定说明', binding_note: '自定义底部提示' } })

  it('refreshes persisted copy when opening and renders activity text safely for bound users', async () => {
    const state = config()
    api.binding.mockImplementation(async () => ({ ...bound, config: structuredClone(state) }))
    const w = render(); await flushPromises()
    state.binding_display.activity_title = '最新活动'
    state.status = 'paused'
    await w.get('button').trigger('click'); await flushPromises()
    expect(w.text()).toContain('QQ 群号：87654321')
    expect(w.text()).toContain('最新活动')
    expect(w.text()).toContain('奖励已暂停')
    expect(w.text()).toContain('第一行\n第二行 <script>alert(1)</script>')
    expect(w.find('script').exists()).toBe(false)
    expect(w.text()).toContain('12****78@qq.com')
  })

  it('passes configured binding copy to the form and retains activity copy after verification and unbinding', async () => {
    const state = config()
    api.binding.mockResolvedValue({ bound: false, config: state })
    const w = render(); await flushPromises()
    await w.get('button').trigger('click'); await flushPromises()
    expect(w.findComponent(QQBindingForm).props()).toMatchObject({ intro: '自定义绑定说明', note: '自定义底部提示' })
    w.findComponent(QQBindingForm).vm.$emit('verified', bound)
    await flushPromises()
    expect(w.text()).toContain('本月活动')
    expect(w.text()).toContain('12****78@qq.com')
    await click('取消绑定'); await click('确认取消绑定')
    expect(w.text()).toContain('本月活动')
    expect(w.text()).toContain('87654321')
    expect(w.findComponent(QQBindingForm).props('intro')).toBe('自定义绑定说明')
  })

  it('shows the masked binding and requires confirmation before unbinding', async () => {
    const w = render(); await flushPromises()
    expect(w.get('[data-testid="qq-binding-entry"]').attributes('aria-label')).toBe('QQ 已绑定')
    await w.get('button').trigger('click'); await flushPromises()
    expect(w.text()).toContain('12****78@qq.com')
    expect(w.text()).not.toContain('12345678@qq.com')
    await click('取消绑定')
    expect(api.unbind).not.toHaveBeenCalled()
    await click('保留绑定')
    expect(w.text()).not.toContain('确认取消绑定')
    await click('取消绑定')
    api.binding.mockResolvedValue({ bound: false })
    await click('确认取消绑定')
    expect(api.unbind).toHaveBeenCalledOnce()
    expect(w.get('[data-testid="qq-binding-entry"]').attributes('aria-label')).toBe('QQ 未绑定')
    expect(w.get('[role="status"]').text()).toContain('历史和领奖记录已保留')
    expect(w.find('[data-testid="mail-proof"]').exists()).toBe(true)
  })

  it('keeps the binding visible when unbinding is rejected', async () => {
    api.unbind.mockRejectedValue(new Error('restricted'))
    const w = render(); await flushPromises()
    await w.get('button').trigger('click'); await flushPromises()
    await click('取消绑定'); await click('确认取消绑定')
    expect(w.get('[data-testid="qq-binding-entry"]').attributes('aria-label')).toBe('QQ 已绑定')
    expect(w.get('[role="alert"]').text()).toContain('取消绑定未完成')
  })

  it('ignores a status request started before the successful unbind', async () => {
    const w = render(); await flushPromises()
    await w.get('button').trigger('click'); await flushPromises()
    let resolveOld!: (v: typeof bound) => void
    api.binding.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    window.dispatchEvent(new Event('focus'))
    await click('取消绑定'); await click('确认取消绑定')
    resolveOld(bound); await flushPromises()
    expect(w.get('[data-testid="qq-binding-entry"]').attributes('aria-label')).toBe('QQ 未绑定')
  })

  it('does not offer a binding form while its status is unknown', async () => {
    api.binding.mockRejectedValue(new Error('offline'))
    const w = render(); await flushPromises()
    await w.get('button').trigger('click'); await flushPromises()
    expect(w.get('[data-testid="qq-binding-entry"]').attributes('aria-label')).toBe('QQ 状态重试')
    expect(w.find('[data-testid="mail-proof"]').exists()).toBe(false)
    expect(w.text()).toContain('重试')
  })
})
