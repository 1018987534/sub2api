import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdminQQActivity from '@/views/admin/QQActivityView.vue'
import UserQQActivity from '@/views/user/QQActivityView.vue'

const mocks = vi.hoisted(() => ({ admin: vi.fn(), control: vi.fn(), current: vi.fn(), claim: vi.fn(), route: { query: {} as Record<string, string> } }))
vi.mock('@/api/qqActivity', () => ({ qqActivityAPI: mocks }))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
const config = () => ({ status: 'active', started_at: '2026-10-24T10:00:00Z', initialized_at: '2026-10-24T10:00:00Z', ended_at: null, newcomer_enabled: true, invitation_enabled: true, auto_process_join: true, group_messages_enabled: true })
const report = () => ({ config: config(), baseline_users: 100, members_count: 163, bindings_count: 20, newcomer_paid: 2, invitation_paid: 4, pending_amount: 2, unsent_messages: 0, members: [{ group_id: '87654321', qq: '12345678', in_group: true, robot: false, legacy: true, match_state: 'bound', account: '12****78@qq.com', level: 7, bound: true }], invitations: [] })
const options = { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } }
beforeEach(() => { vi.clearAllMocks(); mocks.route.query = {}; mocks.admin.mockResolvedValue(report()); mocks.control.mockResolvedValue({ success: true }); mocks.current.mockResolvedValue({ config: config(), bound: true, account: '12****78@qq.com', qq: '12345678', welcome_status: 'not_eligible', welcome_text: '当前暂不符合本次 $2 体验额度领取条件。', invitation_paid: 2, invitation_pending: 0, pending_inviter_binding: 0, rewards: [] }) })

describe('QQ activity administration', () => {
  it('closes and reopens through the persisted API without resetting the campaign', async () => {
    const state = report()
    mocks.admin.mockImplementation(async () => structuredClone(state))
    mocks.control.mockImplementation(async (data) => { state.config.status = data.status })
    const wrapper = mount(AdminQQActivity, options); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '关闭活动')!.trigger('click'); await flushPromises()
    expect(mocks.control).toHaveBeenCalledWith({ action: 'status', status: 'paused' })
    expect(wrapper.text()).toContain('活动已关闭，绑定和查询保留')
    await wrapper.findAll('button').find(b => b.text() === '开启活动')!.trigger('click'); await flushPromises()
    expect(mocks.control).toHaveBeenLastCalledWith({ action: 'status', status: 'active' })
    expect(state.config.started_at).toBe('2026-10-24T10:00:00Z')
    wrapper.unmount()
  })
  it('saves all four configuration switches and only displays masked account data', async () => {
    const wrapper = mount(AdminQQActivity, options); await flushPromises()
    const inputs = wrapper.findAll('input[type="checkbox"]'); expect(inputs).toHaveLength(4)
    await inputs[0].setValue(false); await inputs[3].setValue(false)
    await wrapper.findAll('button').find(b => b.text() === '保存配置')!.trigger('click'); await flushPromises()
    expect(mocks.control).toHaveBeenCalledWith({ action: 'settings', settings: { newcomer_enabled: false, invitation_enabled: true, auto_process_join: true, group_messages_enabled: false } })
    expect(wrapper.text()).toContain('12****78@qq.com'); expect(wrapper.text()).not.toContain('12345678@qq.com')
    expect(wrapper.text()).toContain('邀请人本人不限等级')
    wrapper.unmount()
  })
  it('cannot enable an incomplete initialization', async () => {
    const state = report(); state.config.status = 'preparing'; state.config.started_at = ''
    mocks.admin.mockResolvedValue(state)
    const wrapper = mount(AdminQQActivity, options); await flushPromises()
    const button = wrapper.findAll('button').find(b => b.text() === '开启活动')!
    expect(button.attributes('disabled')).toBeDefined(); expect(wrapper.text()).toContain('初始化尚未完成')
    wrapper.unmount()
  })
  it('only reports a saved configuration after the server confirms success', async () => {
    mocks.control.mockRejectedValue(new Error('unavailable'))
    const wrapper = mount(AdminQQActivity, options); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '保存配置')!.trigger('click'); await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toContain('操作未完成')
    expect(wrapper.text()).not.toContain('配置已保存')
    wrapper.unmount()
  })
})
describe('QQ activity user page', () => {
  it('shows the common ineligible copy without explaining an existing-user classification', async () => {
    const wrapper = mount(UserQQActivity, options); await flushPromises()
    expect(wrapper.text()).toContain('当前暂不符合本次 $2 体验额度领取条件。')
    expect(wrapper.text()).not.toContain('活动前已有用户'); expect(wrapper.text()).not.toContain('12345678@qq.com')
    wrapper.unmount()
  })
  it('requires explicit account confirmation before generating a code', async () => {
    mocks.route.query = { claim: 'test-token' }
    mocks.claim.mockResolvedValueOnce({ account: '12****78@qq.com', qq_email: '12****78@qq.com', consumed: false, expires_at: '2026-10-24T11:00:00Z' }).mockResolvedValueOnce({ account: '12****78@qq.com', qq_email: '12****78@qq.com', consumed: false, code: '123456', expires_at: '2026-10-24T11:00:00Z' })
    const wrapper = mount(UserQQActivity, options); await flushPromises()
    expect(mocks.claim).toHaveBeenCalledWith('test-token'); expect(wrapper.text()).not.toContain('绑定 123456')
    await wrapper.findAll('button').find(b => b.text().includes('生成绑定码'))!.trigger('click'); await flushPromises()
    expect(mocks.claim).toHaveBeenLastCalledWith('test-token', true); expect(wrapper.text()).toContain('绑定 123456')
    wrapper.unmount()
  })
})
