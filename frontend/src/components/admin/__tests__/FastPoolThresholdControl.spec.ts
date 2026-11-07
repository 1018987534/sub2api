import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FastPoolThresholdControl from '../FastPoolThresholdControl.vue'

const { getSettings, updateSettings, showSuccess, showError } = vi.hoisted(() => ({
  getSettings: vi.fn(), updateSettings: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { settings: {
  getFastPoolThresholdSettings: getSettings,
  updateFastPoolThresholdSettings: updateSettings
} } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('FastPoolThresholdControl', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    getSettings.mockResolvedValue({ enter_seconds: 30, exit_seconds: 33 })
    updateSettings.mockImplementation(async settings => settings)
  })

  it('shows exactly two saved second inputs and saves both together', async () => {
    const wrapper = mount(FastPoolThresholdControl)
    await flushPromises()
    expect(wrapper.findAll('input')).toHaveLength(2)
    expect((wrapper.get('#fast-pool-enter-seconds').element as HTMLInputElement).value).toBe('30')
    expect((wrapper.get('#fast-pool-exit-seconds').element as HTMLInputElement).value).toBe('33')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('#fast-pool-enter-seconds').setValue('28')
    await wrapper.get('#fast-pool-exit-seconds').setValue('31')
    expect(updateSettings).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({ enter_seconds: 28, exit_seconds: 31 })
    expect(showSuccess).toHaveBeenCalledOnce()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it.each([['0', '33'], ['34', '33'], ['28.5', '33'], ['28', '86401'], ['', '33']])(
    'rejects invalid or reversed values %s / %s', async (enter, exit) => {
      const wrapper = mount(FastPoolThresholdControl)
      await flushPromises()
      await wrapper.get('#fast-pool-enter-seconds').setValue(enter)
      await wrapper.get('#fast-pool-exit-seconds').setValue(exit)
      expect(wrapper.get('[role="alert"]').text()).toContain('admin.dashboard.fastPoolThresholdInvalid')
      await wrapper.get('form').trigger('submit')
      expect(updateSettings).not.toHaveBeenCalled()
      wrapper.unmount()
    }
  )

  it('prevents duplicate writes and shows the server readback', async () => {
    let resolveSave!: (settings: object) => void
    updateSettings.mockImplementationOnce(() => new Promise(resolve => { resolveSave = resolve }))
    const wrapper = mount(FastPoolThresholdControl)
    await flushPromises()
    await wrapper.get('#fast-pool-enter-seconds').setValue('28')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).toHaveBeenCalledOnce()
    expect(wrapper.get('input').attributes('disabled')).toBeDefined()
    resolveSave({ enter_seconds: 29, exit_seconds: 32 })
    await flushPromises()
    expect((wrapper.get('#fast-pool-enter-seconds').element as HTMLInputElement).value).toBe('29')
    expect((wrapper.get('#fast-pool-exit-seconds').element as HTMLInputElement).value).toBe('32')
    wrapper.unmount()
  })

  it('keeps entered values after a failed save and supports retry', async () => {
    updateSettings.mockRejectedValueOnce(new Error('write failed'))
    const wrapper = mount(FastPoolThresholdControl)
    await flushPromises()
    await wrapper.get('#fast-pool-enter-seconds').setValue('28')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(showError).toHaveBeenCalledOnce()
    expect(showSuccess).not.toHaveBeenCalled()
    expect((wrapper.get('#fast-pool-enter-seconds').element as HTMLInputElement).value).toBe('28')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(showSuccess).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('retries a failed load without showing invented settings', async () => {
    getSettings.mockRejectedValueOnce(new Error('read failed'))
    const wrapper = mount(FastPoolThresholdControl)
    await flushPromises()
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('admin.dashboard.fastPoolThresholdLoadFailed')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('input')).toHaveLength(2)
    wrapper.unmount()
  })
})
