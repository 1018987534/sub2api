import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import QQBindingEntry from '../QQBindingEntry.vue'

const api = vi.hoisted(() => ({ binding: vi.fn(), unbind: vi.fn() }))
vi.mock('@/api/qqActivity', () => ({ qqActivityAPI: api }))
const bound = { bound: true, qq_email: '12****78@qq.com' }
let view: VueWrapper | undefined
function render() {
  view = mount(QQBindingEntry, { global: { stubs: {
    BaseDialog: { props: ['show'], template: '<section v-if="show"><slot /></section>' },
    QQBindingForm: { template: '<form data-testid="mail-proof" />' },
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
