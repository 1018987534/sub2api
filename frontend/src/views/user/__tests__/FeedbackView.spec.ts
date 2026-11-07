import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import FeedbackView from '../FeedbackView.vue'

const { send } = vi.hoisted(() => ({ send: vi.fn() }))
vi.mock('@/api/supportChat', () => ({ default: { send } }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => ({
  'feedback.messageLabel': '意见箱',
  'feedback.categories.problem': '问题反馈',
  'feedback.categories.suggestion': '功能建议',
  'feedback.categories.other': '其他意见',
}[key] || key) }),
}))

function render() {
  return mount(FeedbackView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
  } } })
}
async function fill(view: ReturnType<typeof render>) {
  await view.get('#feedback-subject').setValue('  请求出错  ')
  await view.get('#feedback-details').setValue('  使用时提示超时，请协助排查。  ')
}
beforeEach(() => { send.mockReset().mockResolvedValue({ id: 41 }) })

describe('FeedbackView', () => {
  it.each([
    ['problem', '问题反馈'], ['suggestion', '功能建议'], ['other', '其他意见'],
  ])('delivers %s to the support inbox and links to replies', async (category, label) => {
    const view = render()
    await fill(view)
    await view.get('#feedback-category').setValue(category)
    await view.get('form').trigger('submit')
    await flushPromises()
    expect(send).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledWith(`【意见箱 · ${label}】请求出错\n\n使用时提示超时，请协助排查。`)
    expect(view.get('[role="status"]').text()).toContain('feedback.success')
    expect(view.get('[role="status"] a').attributes('href')).toBe('/support')
    expect((view.get('#feedback-subject').element as HTMLInputElement).value).toBe('')
    expect((view.get('#feedback-details').element as HTMLTextAreaElement).value).toBe('')
    view.unmount()
  })

  it('rejects whitespace-only feedback without sending a message', async () => {
    const view = render()
    await view.get('#feedback-subject').setValue('  ')
    await view.get('#feedback-details').setValue('\n  ')
    await view.get('form').trigger('submit')
    expect(send).not.toHaveBeenCalled()
    expect(view.get('[role="alert"]').text()).toBe('feedback.required')
    view.unmount()
  })

  it('keeps a failed draft and allows a successful retry', async () => {
    send.mockRejectedValueOnce({ message: '请稍后重试' })
    const view = render()
    await fill(view)
    await view.get('form').trigger('submit')
    await flushPromises()
    expect(view.get('[role="alert"]').text()).toBe('请稍后重试')
    expect(view.find('[role="status"]').exists()).toBe(false)
    expect((view.get('#feedback-subject').element as HTMLInputElement).value).toContain('请求出错')
    expect((view.get('#feedback-details').element as HTMLTextAreaElement).value).toContain('超时')
    await view.get('form').trigger('submit')
    await flushPromises()
    expect(send).toHaveBeenCalledTimes(2)
    expect(view.get('[role="status"]').text()).toContain('feedback.success')
    view.unmount()
  })

  it('allows only one submission while the request is pending', async () => {
    let resolve!: (value: { id: number }) => void
    send.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const view = render()
    await fill(view)
    await view.get('form').trigger('submit')
    await view.get('form').trigger('submit')
    expect(send).toHaveBeenCalledTimes(1)
    expect(view.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    resolve({ id: 41 })
    await flushPromises()
    expect(view.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    view.unmount()
  })
})
