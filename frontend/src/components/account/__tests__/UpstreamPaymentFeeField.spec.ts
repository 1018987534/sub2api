import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import UpstreamPaymentFeeField from '../UpstreamPaymentFeeField.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: { rate: number }) => params ? String(params.rate) + 'x' : key })
}))

describe('UpstreamPaymentFeeField', () => {
  it('previews the real multiplier and emits a percentage without changing the base', async () => {
    const wrapper = mount(UpstreamPaymentFeeField, {
      props: { modelValue: 3, baseRate: 0.1 },

    })
    expect(wrapper.get('[data-testid="upstream-payment-real-rate"]').text()).toContain('0.103x')
    await wrapper.setProps({ modelValue: 5 })
    expect(wrapper.text()).toContain('0.105x')
    await wrapper.get('input').setValue('')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([0])
    expect(wrapper.props('baseRate')).toBe(0.1)
    await wrapper.setProps({ modelValue: -1 })
    expect(wrapper.get('[data-testid="upstream-payment-real-rate"]').text()).toContain('-x')
  })
})
