import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import FeedbackView from '../FeedbackView.vue'

const { adminList } = vi.hoisted(() => ({ adminList: vi.fn() }))
vi.mock('@/api/feedback', () => ({ default: { adminList } }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('vue-i18n', async original => ({
  ...await original<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: ref('zh-CN') }),
}))
const item = { id: 41, user_id: 7, user_email: 'fixture@example.test', user_username: '测试用户', category: 'problem', subject: '请求出错', content: '<script>不执行</script>\n使用时提示超时', created_at: '2026-11-07T04:00:00Z' }
const render = () => mount(FeedbackView, { global: { stubs: { Pagination: { props: ['page', 'total'], template: '<button @click="$emit(\'update:page\', page + 1)">Next</button>' } } } })
beforeEach(() => adminList.mockReset().mockResolvedValue({ items: [item], total: 21, page: 1 }))

describe('administrator feedback viewing', () => {
  it('shows customer feedback safely and has no reply form', async () => {
    const view = render()
    await flushPromises()
    expect(adminList).toHaveBeenCalledWith(1, 20)
    expect(view.get('article').text()).toContain(item.content)
    expect(view.get('article').text()).toContain(item.user_email)
    expect(view.find('script').exists()).toBe(false)
    expect(view.find('form, textarea, input, a[href="/admin/support"]').exists()).toBe(false)
    expect(view.text()).toContain('feedback.adminDescription')
    await view.get('button').trigger('click')
    await flushPromises()
    expect(adminList).toHaveBeenCalledTimes(2)
    await view.findAll('button')[1].trigger('click')
    await flushPromises()
    expect(adminList).toHaveBeenLastCalledWith(2, 20)
    view.unmount()
  })

  it('renders an empty feedback box', async () => {
    adminList.mockResolvedValue({ items: [], total: 0, page: 1 })
    const view = render()
    await flushPromises()
    expect(view.text()).toContain('feedback.empty')
    view.unmount()
  })

  it('allows retrying a failed read', async () => {
    adminList.mockRejectedValueOnce(new Error('offline'))
    const view = render()
    await flushPromises()
    expect(view.get('[role="alert"]').text()).toBe('feedback.loadFailed')
    await view.get('button').trigger('click')
    await flushPromises()
    expect(view.find('[role="alert"]').exists()).toBe(false)
    expect(view.find('article').exists()).toBe(true)
    view.unmount()
  })
})
