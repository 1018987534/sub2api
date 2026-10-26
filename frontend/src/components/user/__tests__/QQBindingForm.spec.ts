import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import QQBindingForm from '../QQBindingForm.vue'

const api = vi.hoisted(() => ({ sendBindingCode: vi.fn(), verifyBinding: vi.fn() }))
vi.mock('@/api/qqActivity', () => ({ qqActivityAPI: api }))
const challenge = { challenge: 'a'.repeat(48), qq_email: '12****78@qq.com', expires_at: '2026-10-25T12:50:00Z', resend_after: 60 }
let view: VueWrapper | undefined
beforeEach(() => {
  vi.clearAllMocks()
  api.sendBindingCode.mockResolvedValue(challenge)
  api.verifyBinding.mockResolvedValue({ bound: true, qq_email: challenge.qq_email })
})
afterEach(() => { view?.unmount(); view = undefined })

describe('verified QQ mailbox form', () => {
  it('renders configured plain text and allows empty explanatory copy', async () => {
    view = mount(QQBindingForm, { props: { intro: '第一行\n第二行 <b>文本</b>', note: '自定义提示' } })
    expect(view.text()).toContain('第一行\n第二行 <b>文本</b>')
    expect(view.text()).toContain('自定义提示')
    expect(view.find('b').exists()).toBe(false)
    await view.setProps({ intro: '', note: '' })
    expect(view.findAll('p')).toHaveLength(0)
    expect(view.get('input[name="qq-email"]').exists()).toBe(true)
  })

  it.each(['12345678', '12345678@qq.com'])('accepts %s and sends the proof only to the website', async email => {
    view = mount(QQBindingForm)
    await view.get('input[name="qq-email"]').setValue(email)
    await view.get('button[type="button"]').trigger('click'); await flushPromises()
    expect(api.sendBindingCode).toHaveBeenCalledWith(email)
    expect(view.get('[role="status"]').text()).toContain(challenge.qq_email)
    expect(view.get('[role="status"]').text()).not.toContain('12345678@qq.com')
    expect(view.get('button[type="button"]').attributes('disabled')).toBeDefined()
    await view.get('input[name="qq-code"]').setValue('123456')
    await view.get('form').trigger('submit'); await flushPromises()
    expect(api.verifyBinding).toHaveBeenCalledWith(email, challenge.challenge, '123456')
    expect(view.emitted('verified')).toEqual([[{ bound: true, qq_email: challenge.qq_email }]])
  })

  it('discards a challenge when the QQ email is edited', async () => {
    view = mount(QQBindingForm)
    await view.get('input[name="qq-email"]').setValue('12345678')
    await view.get('button[type="button"]').trigger('click'); await flushPromises()
    await view.get('input[name="qq-email"]').setValue('23456789@qq.com')
    await view.get('form').trigger('submit'); await flushPromises()
    expect(api.verifyBinding).not.toHaveBeenCalled()
    expect(view.find('[role="status"]').exists()).toBe(false)
    expect(view.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  })

  it('shows a rejected email code without claiming success', async () => {
    api.verifyBinding.mockRejectedValue({ message: '验证码无效或已过期，请重新获取后重试。' })
    view = mount(QQBindingForm)
    await view.get('input[name="qq-email"]').setValue('12345678')
    await view.get('button[type="button"]').trigger('click'); await flushPromises()
    await view.get('input[name="qq-code"]').setValue('123456')
    await view.get('form').trigger('submit'); await flushPromises()
    expect(view.get('[role="alert"]').text()).toContain('验证码无效')
    expect(view.emitted('verified')).toBeUndefined()
  })
})
