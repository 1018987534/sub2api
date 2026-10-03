import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UserSupportChat from '../user/SupportChatView.vue'
import AdminSupportChat from '../admin/SupportChatView.vue'

const api = vi.hoisted(() => ({
  messages: vi.fn(), adminConversations: vi.fn(), read: vi.fn(), adminRead: vi.fn(),
}))
vi.mock('@/api/supportChat', () => ({ default: api }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/support/SupportMessageComposer.vue', () => ({ default: { template: '<div />' } }))

beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  api.messages.mockResolvedValue({ items: [] })
  api.adminConversations.mockResolvedValue({ items: [] })
  api.read.mockResolvedValue(undefined)
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
})
afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

describe.each([
  ['user', UserSupportChat, 'messages'],
  ['admin', AdminSupportChat, 'adminConversations'],
] as const)('%s support polling lifecycle', (_, component, method) => {
  it('does not start polling when the initial request resolves after unmount', async () => {
    for (let visit = 0; visit < 20; visit++) {
      let resolve!: (value: { items: never[] }) => void
      api[method].mockImplementationOnce(() => new Promise(done => { resolve = done }))
      const wrapper = mount(component)
      wrapper.unmount()
      resolve({ items: [] })
      await flushPromises()
    }
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(api[method]).toHaveBeenCalledTimes(20)
  })

  it('polls while mounted and clears the timer on ordinary unmount', async () => {
    const wrapper = mount(component)
    await flushPromises()
    expect(api[method]).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(15_000)
    expect(api[method]).toHaveBeenCalledTimes(2)
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(api[method]).toHaveBeenCalledTimes(2)
  })
})
