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
    getSettings.mockResolvedValue({ mode: 'rate', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' })
    updateSettings.mockImplementation(async (mode: string, cache_rate_threshold_percent: number | null, below_threshold_mode: string) => ({ mode, cache_rate_threshold_percent, below_threshold_mode }))
  })

  it('loads the shared saved mode and emits only saved changes', async () => {
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    expect((wrapper.get('[data-testid="fast-pool-priority-mode"]').element as HTMLSelectElement).value).toBe('rate')
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' }]])
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="fast-pool-priority-mode"]').setValue('latency')
    expect(wrapper.text()).toContain('admin.dashboard.fastPoolLatencyPriorityHint')
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' }]])
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith('latency', null, 'intelligence')
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' }], [{ mode: 'latency', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' }]])
    expect(showSuccess).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('blocks duplicate saves and uses the server response', async () => {
    let resolveSave!: (value: object) => void
    updateSettings.mockImplementationOnce(() => new Promise(resolve => { resolveSave = resolve }))
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    await wrapper.get('[data-testid="fast-pool-priority-mode"]').setValue('latency')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="fast-pool-priority-mode"]').attributes('disabled')).toBeDefined()
    resolveSave({ mode: 'rate' })
    await flushPromises()
    expect((wrapper.get('[data-testid="fast-pool-priority-mode"]').element as HTMLSelectElement).value).toBe('rate')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('preserves a failed save for retry without changing the active mode', async () => {
    updateSettings.mockRejectedValueOnce(new Error('save failed'))
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    await wrapper.get('[data-testid="fast-pool-priority-mode"]').setValue('latency')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(showError).toHaveBeenCalledOnce()
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' }]])
    expect((wrapper.get('[data-testid="fast-pool-priority-mode"]').element as HTMLSelectElement).value).toBe('latency')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' }], [{ mode: 'latency', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' }]])
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
    expect((wrapper.get('[data-testid="fast-pool-priority-mode"]').element as HTMLSelectElement).value).toBe('rate')
    wrapper.unmount()
  })

  it('loads, changes and clears the optional percentage independently of mode', async () => {
    getSettings.mockResolvedValueOnce({ mode: 'rate', cache_rate_threshold_percent: 80, below_threshold_mode: 'intelligence' })
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    const input = wrapper.get('input')
    expect((input.element as HTMLInputElement).value).toBe('80')
    await input.setValue('90.5')
    expect(wrapper.emitted('change')).toHaveLength(1)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenLastCalledWith('rate', 90.5, 'intelligence')
    expect(wrapper.emitted('change')?.at(-1)).toEqual([{ mode: 'rate', cache_rate_threshold_percent: 90.5, below_threshold_mode: 'intelligence' }])
    await input.setValue('')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenLastCalledWith('rate', null, 'intelligence')
    expect(wrapper.emitted('change')?.at(-1)).toEqual([{ mode: 'rate', cache_rate_threshold_percent: null, below_threshold_mode: 'intelligence' }])
    wrapper.unmount()
  })

  it('defaults a new configuration to eighty percent', async () => {
    getSettings.mockResolvedValueOnce({ mode: 'rate', cache_rate_threshold_percent: 80, below_threshold_mode: 'intelligence' })
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    expect((wrapper.get('input').element as HTMLInputElement).value).toBe('80')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.emitted('change')).toEqual([[{ mode: 'rate', cache_rate_threshold_percent: 80, below_threshold_mode: 'intelligence' }]])
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
    expect(updateSettings).toHaveBeenCalledWith('rate', value, 'intelligence')
    wrapper.unmount()
  })

  it.each(['intelligence', 'latency', 'rate'])('saves the below-threshold %s strategy independently', async belowMode => {
    getSettings.mockResolvedValueOnce({ mode: 'rate', cache_rate_threshold_percent: 80, below_threshold_mode: 'latency' })
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    const select = wrapper.get('[data-testid="fast-pool-below-threshold-mode"]')
    expect((select.element as HTMLSelectElement).value).toBe('latency')
    await select.setValue(belowMode)
    await wrapper.get('input').setValue(90)
    expect(wrapper.emitted('change')).toHaveLength(1)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith('rate', 90, belowMode)
    expect(wrapper.emitted('change')?.at(-1)).toEqual([{ mode: 'rate', cache_rate_threshold_percent: 90, below_threshold_mode: belowMode }])
    wrapper.unmount()
  })

  it('defaults older settings to cache priority and disables the extra strategy when the threshold is cleared', async () => {
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    const select = wrapper.get('[data-testid="fast-pool-below-threshold-mode"]')
    expect((select.element as HTMLSelectElement).value).toBe('intelligence')
    expect(select.attributes('disabled')).toBeDefined()
    await wrapper.get('input').setValue(80)
    expect(select.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})
