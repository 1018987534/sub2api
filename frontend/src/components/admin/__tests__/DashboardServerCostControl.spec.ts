import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DashboardServerCostControl from '../DashboardServerCostControl.vue'

const { getSettings, updateSettings, showSuccess, showError } = vi.hoisted(() => ({
  getSettings: vi.fn(), updateSettings: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { settings: { getDashboardProfitSettings: getSettings, updateDashboardProfitSettings: updateSettings } }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, values?: object) => `${key} ${values ? JSON.stringify(values) : ''}`.trim() }) }))

describe('DashboardServerCostControl', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getSettings.mockResolvedValue({ monthly_server_cost_usd: 930, daily_server_cost_usd: 30, month_days: 31 })
    updateSettings.mockImplementation(async (monthly: number) => ({ monthly_server_cost_usd: monthly, daily_server_cost_usd: monthly / 31, month_days: 31 }))
  })

  it('loads the saved amount and previews the current calendar month allocation', async () => {
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    expect((wrapper.get('[data-testid="monthly-server-cost"]').element as HTMLInputElement).value).toBe('930')
    expect(wrapper.text()).toContain('"days":31')
    expect(wrapper.text()).toContain('$30.00')
    expect(wrapper.get('[data-testid="save-server-cost"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="monthly-server-cost"]').setValue('600')
    expect(wrapper.text()).toContain('$19.35')
    wrapper.unmount()
  })

  it.each([600, 0])('persists %s and emits the saved settings to refresh profit', async (monthly) => {
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    await wrapper.get('[data-testid="monthly-server-cost"]').setValue(String(monthly))
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith(monthly)
    expect(wrapper.emitted('saved')?.[0]).toEqual([{ monthly_server_cost_usd: monthly, daily_server_cost_usd: monthly / 31, month_days: 31 }])
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })

  it.each(['-1', ''])('rejects an invalid amount %s without writing settings', async (value) => {
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    await wrapper.get('[data-testid="monthly-server-cost"]').setValue(value)
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="save-server-cost"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps the draft and existing profit when saving fails', async () => {
    updateSettings.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    await wrapper.get('[data-testid="monthly-server-cost"]').setValue('600')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(showError).toHaveBeenCalled()
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect((wrapper.get('[data-testid="monthly-server-cost"]').element as HTMLInputElement).value).toBe('600')
    wrapper.unmount()
  })

  it('retries a failed load and only enables edits after settings are available', async () => {
    getSettings.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    expect(wrapper.find('[data-testid="monthly-server-cost"]').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="monthly-server-cost"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
