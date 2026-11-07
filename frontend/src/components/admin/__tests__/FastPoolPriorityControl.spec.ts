import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import FastPoolPriorityControl from '../FastPoolPriorityControl.vue'

const { getSettings, updateSettings, showSuccess, showError } = vi.hoisted(() => ({
  getSettings: vi.fn(), updateSettings: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { settings: {
  getFastPoolPrioritySettings: getSettings,
  updateFastPoolPrioritySettings: updateSettings
} } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('FastPoolPriorityControl', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    getSettings.mockResolvedValue({ mode: 'rate', cache_rate_threshold_percent: null })
    updateSettings.mockImplementation(async (mode: string, cache_rate_threshold_percent: number | null) => ({ mode, cache_rate_threshold_percent }))
  })

  it('loads the shared saved mode and emits only saved changes', async () => {
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('rate')
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null }]])
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('select').setValue('latency')
    expect(wrapper.text()).toContain('admin.dashboard.fastPoolLatencyPriorityHint')
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null }]])
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith('latency', null)
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null }], [{ mode: 'latency', cache_rate_threshold_percent: null }]])
    expect(showSuccess).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('blocks duplicate saves and uses the server response', async () => {
    let resolveSave!: (value: object) => void
    updateSettings.mockImplementationOnce(() => new Promise(resolve => { resolveSave = resolve }))
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    await wrapper.get('select').setValue('latency')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).toHaveBeenCalledTimes(1)
    expect(wrapper.get('select').attributes('disabled')).toBeDefined()
    resolveSave({ mode: 'rate' })
    await flushPromises()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('rate')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('preserves a failed save for retry without changing the active mode', async () => {
    updateSettings.mockRejectedValueOnce(new Error('save failed'))
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    await wrapper.get('select').setValue('latency')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(showError).toHaveBeenCalledOnce()
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null }]])
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('latency')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null }], [{ mode: 'latency', cache_rate_threshold_percent: null }]])
    wrapper.unmount()
  })

  it('supports retry after a failed initial load', async () => {
    getSettings.mockRejectedValueOnce(new Error('load failed'))
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    expect(wrapper.find('select').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('admin.dashboard.fastPoolPriorityLoadFailed')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('rate')
    wrapper.unmount()
  })

  it('loads, changes and clears the optional percentage independently of mode', async () => {
    getSettings.mockResolvedValueOnce({ mode: 'rate', cache_rate_threshold_percent: 80 })
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    const input = wrapper.get('input')
    expect((input.element as HTMLInputElement).value).toBe('80')
    await input.setValue('90.5')
    expect(wrapper.emitted('change')).toHaveLength(1)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenLastCalledWith('rate', 90.5)
    expect(wrapper.emitted('change')?.at(-1)).toEqual([{ mode: 'rate', cache_rate_threshold_percent: 90.5 }])
    await input.setValue('')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenLastCalledWith('rate', null)
    expect(wrapper.emitted('change')?.at(-1)).toEqual([{ mode: 'rate', cache_rate_threshold_percent: null }])
    wrapper.unmount()
  })

  it('defaults a new configuration to eighty percent', async () => {
    getSettings.mockResolvedValueOnce({ mode: 'rate', cache_rate_threshold_percent: 80 })
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('80')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: 80 }]])
    wrapper.unmount()
  })

  it.each(['-1', '100.01'])('rejects percentage %s without saving', async value => {
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    await wrapper.get('input').setValue(value)
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('admin.dashboard.fastPoolCacheRateThresholdInvalid')
    wrapper.unmount()
  })

  it.each([0, 100])('saves boundary percentage %s', async value => {
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    await wrapper.get('input').setValue(value)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith('rate', value)
    wrapper.unmount()
  })
})
