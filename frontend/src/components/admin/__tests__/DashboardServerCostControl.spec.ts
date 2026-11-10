import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DashboardServerCostControl from '../DashboardServerCostControl.vue'
import type { DashboardProfitSettingsUpdate } from '@/api/admin/settings'

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
    getSettings.mockResolvedValue({ monthly_server_cost_usd: 930, daily_server_cost_usd: 30, month_days: 31, withdrawal_fee_rate_percent: 1.8 })
    updateSettings.mockImplementation(async (update: DashboardProfitSettingsUpdate) => ({
      monthly_server_cost_usd: update.monthly_server_cost_usd ?? 930,
      daily_server_cost_usd: (update.monthly_server_cost_usd ?? 930) / 31,
      month_days: 31,
      withdrawal_fee_rate_percent: update.withdrawal_fee_rate_percent ?? 1.8
    }))
  })

  it('loads the saved amount and previews the current calendar month allocation', async () => {
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    expect((wrapper.get('[data-testid="monthly-server-cost"]').element as HTMLInputElement).value).toBe('930')
    expect((wrapper.get('[data-testid="withdrawal-fee-rate"]').element as HTMLInputElement).value).toBe('1.8')
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
    expect(updateSettings).toHaveBeenCalledWith({ monthly_server_cost_usd: monthly })
    expect(wrapper.emitted('saved')?.[0]).toEqual([{ monthly_server_cost_usd: monthly, daily_server_cost_usd: monthly / 31, month_days: 31, withdrawal_fee_rate_percent: 1.8 }])
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

  it.each([3.2, 0, 100])('saves a %s percent withdrawal rate and reloads the saved value', async (rate) => {
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    await wrapper.get('[data-testid="withdrawal-fee-rate"]').setValue(String(rate))
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({ withdrawal_fee_rate_percent: rate })
    const settings = { monthly_server_cost_usd: 930, daily_server_cost_usd: 30, month_days: 31, withdrawal_fee_rate_percent: rate }
    expect(wrapper.emitted('saved')?.[0]).toEqual([settings])
    expect(wrapper.get('[data-testid="save-server-cost"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
    getSettings.mockResolvedValueOnce(settings)
    const reloaded = mount(DashboardServerCostControl)
    await flushPromises()
    expect((reloaded.get('[data-testid="withdrawal-fee-rate"]').element as HTMLInputElement).value).toBe(String(rate))
    reloaded.unmount()
  })

  it.each(['-0.01', '100.01', ''])('rejects an invalid withdrawal rate %s', async (value) => {
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    await wrapper.get('[data-testid="withdrawal-fee-rate"]').setValue(value)
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="withdrawal-fee-rate"]').attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('[data-testid="save-server-cost"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('submits both changed fields together and retains the fee draft after a failed save', async () => {
    updateSettings.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mount(DashboardServerCostControl)
    await flushPromises()
    await wrapper.get('[data-testid="monthly-server-cost"]').setValue('600')
    await wrapper.get('[data-testid="withdrawal-fee-rate"]').setValue('3.2')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith({ monthly_server_cost_usd: 600, withdrawal_fee_rate_percent: 3.2 })
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect((wrapper.get('[data-testid="withdrawal-fee-rate"]').element as HTMLInputElement).value).toBe('3.2')
    expect(showError).toHaveBeenCalled()
    wrapper.unmount()
  })
})
