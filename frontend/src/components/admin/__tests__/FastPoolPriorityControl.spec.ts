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
    getSettings.mockResolvedValue({ mode: 'rate' })
    updateSettings.mockImplementation(async (mode: string) => ({ mode }))
  })

  it('loads the shared saved mode and emits only saved changes', async () => {
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('rate')
    expect(wrapper.emitted('change')).toEqual([['rate']])
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('select').setValue('intelligence')
    expect(wrapper.text()).toContain('admin.dashboard.fastPoolIntelligencePriorityHint')
    expect(wrapper.emitted('change')).toEqual([['rate']])
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith('intelligence')
    expect(wrapper.emitted('change')).toEqual([['rate'], ['intelligence']])
    expect(showSuccess).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('blocks duplicate saves and uses the server response', async () => {
    let resolveSave!: (value: object) => void
    updateSettings.mockImplementationOnce(() => new Promise(resolve => { resolveSave = resolve }))
    const wrapper = mount(FastPoolPriorityControl)
    await flushPromises()
    await wrapper.get('select').setValue('intelligence')
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
    await wrapper.get('select').setValue('intelligence')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(showError).toHaveBeenCalledOnce()
    expect(wrapper.emitted('change')).toEqual([['rate']])
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('intelligence')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.emitted('change')).toEqual([['rate'], ['intelligence']])
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
})
