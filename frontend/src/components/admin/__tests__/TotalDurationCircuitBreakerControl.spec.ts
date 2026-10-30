import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import TotalDurationCircuitBreakerControl from '../TotalDurationCircuitBreakerControl.vue'

const { getSettings, updateSettings, showSuccess, showError } = vi.hoisted(() => ({
  getSettings: vi.fn(), updateSettings: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { settings: {
  getTotalDurationCircuitBreakerSettings: getSettings,
  updateTotalDurationCircuitBreakerSettings: updateSettings
} } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: { count?: number }) => params?.count ? `${key}:${params.count}` : key }) }))

describe('TotalDurationCircuitBreakerControl', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    getSettings.mockResolvedValue({ max_slow_requests: 15, threshold_seconds: 180, window_seconds: 1800 })
    updateSettings.mockImplementation(async (limit: number) => ({ max_slow_requests: limit, threshold_seconds: 180, window_seconds: 1800 }))
  })

  it('shows the saved limit and explains that more than fifteen means request sixteen', async () => {
    const wrapper = mount(TotalDurationCircuitBreakerControl)
    await flushPromises()
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('15')
    expect(wrapper.text()).toContain('admin.dashboard.circuitBreakerHint:16')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('saves only the edited count, reads back the server response, and blocks duplicate saves', async () => {
    let resolveSave!: (value: object) => void
    updateSettings.mockImplementationOnce(() => new Promise(resolve => { resolveSave = resolve }))
    const wrapper = mount(TotalDurationCircuitBreakerControl)
    await flushPromises()
    await wrapper.get('input').setValue('7')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).toHaveBeenCalledTimes(1)
    expect(updateSettings).toHaveBeenCalledWith(7)
    expect(wrapper.get('input').attributes('disabled')).toBeDefined()
    resolveSave({ max_slow_requests: 7, threshold_seconds: 180, window_seconds: 1800 })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.dashboard.circuitBreakerHint:8')
    expect(showSuccess).toHaveBeenCalledOnce()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it.each(['-1', '1.5', '10000', ''])('rejects invalid count %j', async value => {
    const wrapper = mount(TotalDurationCircuitBreakerControl)
    await flushPromises()
    await wrapper.get('input').setValue(value)
    expect(wrapper.get('input').attributes('aria-invalid')).toBe('true')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('allows zero to mean the first slow request triggers the circuit', async () => {
    const wrapper = mount(TotalDurationCircuitBreakerControl)
    await flushPromises()
    await wrapper.get('input').setValue('0')
    expect(wrapper.text()).toContain('admin.dashboard.circuitBreakerHint:1')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledTimes(1)
    expect(updateSettings).toHaveBeenCalledWith(0)
    wrapper.unmount()
  })

  it('preserves edits after a failed save so the user can retry', async () => {
    updateSettings.mockRejectedValueOnce(new Error('save failed'))
    const wrapper = mount(TotalDurationCircuitBreakerControl)
    await flushPromises()
    await wrapper.get('input').setValue('7')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(showError).toHaveBeenCalledOnce()
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('7')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(showSuccess).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('does not show an invented current value when loading fails and supports retry', async () => {
    getSettings.mockRejectedValueOnce(new Error('load failed'))
    const wrapper = mount(TotalDurationCircuitBreakerControl)
    await flushPromises()
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('admin.dashboard.circuitBreakerLoadFailed')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('15')
    wrapper.unmount()
  })
})
