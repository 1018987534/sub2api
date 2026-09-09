import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UserBalanceHistoryModal from '../UserBalanceHistoryModal.vue'
import common from '@/i18n/locales/zh/common'
import misc from '@/i18n/locales/zh/misc'
import dashboard from '@/i18n/locales/zh/dashboard'
import overview from '@/i18n/locales/zh/admin/overview'
import type { AdminUser } from '@/types'

const { getHistory } = vi.hoisted(() => ({ getHistory: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { users: { getUserBalanceHistory: getHistory } } }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({
    t: (key: string, params: Record<string, string | number> = {}) => {
      const messages = { ...common, ...misc, ...dashboard, admin: overview }
      const message = key.split('.').reduce<any>((value, part) => value?.[part], messages)
      return typeof message === 'string' ? message.replace(/\{(\w+)\}/g, (_, name) => String(params[name] ?? '')) : key
    },
  }),
}))

const award = {
  id: -19, code: 'LOT-19', type: 'lottery_reward', value: 5,
  lottery_round_no: 10, balance_before: 10.18850296, balance_after: 15.18850296,
  used_at: '2026-09-09T23:08:55+08:00', created_at: '2026-09-09T23:08:55+08:00', notes: '',
}
const response = (items = [award], total = items.length) => ({ items, total, total_recharged: 10 })

async function openModal() {
  const wrapper = mount(UserBalanceHistoryModal, {
    props: {
      show: false,
      user: { id: 7, email: 'winner@example.test', balance: 15.18850296, created_at: '2026-09-09T19:36:53+08:00' } as AdminUser,
    },
    global: {
      stubs: {
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
        Icon: true,
        Select: {
          props: ['modelValue', 'options'], emits: ['update:modelValue', 'change'],
          template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value); $emit(\'change\')"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>',
        },
      },
    },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  getHistory.mockResolvedValue(response())
})

describe('UserBalanceHistoryModal lottery awards', () => {
  it('shows a historical award with round, currency and balance change while preserving recharge total', async () => {
    const wrapper = await openModal()
    expect(getHistory).toHaveBeenCalledWith(7, 1, 15, undefined)
    expect(wrapper.text()).toContain('抽奖奖励')
    expect(wrapper.text()).toContain('第 10 期')
    expect(wrapper.text()).toContain('+$5.00')
    expect(wrapper.text()).toContain('余额：$10.19 → $15.19')
    expect(wrapper.text()).toMatch(/总充值:\s*\$10.00/)
    expect(wrapper.text()).not.toContain('LOT-19...')
    wrapper.unmount()
  })

  it('filters lottery awards and keeps that filter when paging', async () => {
    const wrapper = await openModal()
    getHistory.mockResolvedValue(response([award], 16))
    await wrapper.get('select').setValue('lottery_reward')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(7, 1, 15, 'lottery_reward')
    const next = wrapper.findAll('button').find(button => button.text() === '下一页')
    expect(next).toBeDefined()
    await next!.trigger('click')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(7, 2, 15, 'lottery_reward')
    await wrapper.get('select').setValue('')
    await flushPromises()
    expect(getHistory).toHaveBeenLastCalledWith(7, 1, 15, undefined)
    wrapper.unmount()
  })

  it('keeps affiliate and lottery rows with the same source ID distinct', async () => {
    const affiliate = { ...award, type: 'affiliate_balance', code: 'AFF-19', value: 3 }
    getHistory.mockResolvedValue(response([award, affiliate]))
    const wrapper = await openModal()
    expect(wrapper.text()).toContain('+$5.00')
    expect(wrapper.text()).toContain('+$3.00')
    getHistory.mockResolvedValue(response([affiliate, award]))
    await wrapper.get('select').trigger('change')
    await flushPromises()
    expect(wrapper.text()).toContain('第 10 期')
    expect(wrapper.text()).toContain('+$3.00')
    expect(wrapper.text()).toContain('+$5.00')
    wrapper.unmount()
  })
})
