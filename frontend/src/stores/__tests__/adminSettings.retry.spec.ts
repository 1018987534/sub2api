import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAdminSettingsStore } from '../adminSettings'

const mocks = vi.hoisted(() => ({ getSidebarSettings: vi.fn(), getSettings: vi.fn(), getConfig: vi.fn() }))
vi.mock('@/api', () => ({ adminAPI: { settings: { getSidebarSettings: mocks.getSidebarSettings, getSettings: mocks.getSettings }, payment: { getConfig: mocks.getConfig } } }))
beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  setActivePinia(createPinia())
  mocks.getSidebarSettings.mockResolvedValue({ ops_monitoring_enabled: true, payment_enabled: true, custom_menu_items: [{ id: 'custom', title: 'Custom' }] })
  vi.spyOn(console, 'error').mockImplementation(() => {})
})
afterEach(() => { vi.restoreAllMocks(); localStorage.clear() })

describe('admin settings fetch retry', () => {
  it('retries a failed initial sidebar request without forcing a refresh', async () => {
    mocks.getSidebarSettings.mockRejectedValueOnce(new Error('Temporarily unavailable'))
    const store = useAdminSettingsStore()
    await store.fetch()
    expect(store.loaded).toBe(false)
    expect(store.loading).toBe(false)
    await store.fetch()
    expect(mocks.getSidebarSettings).toHaveBeenCalledTimes(2)
    expect(mocks.getSettings).not.toHaveBeenCalled()
    expect(mocks.getConfig).not.toHaveBeenCalled()
    expect(store.loaded).toBe(true)
    expect(store.paymentEnabled).toBe(true)
    expect(store.customMenuItems).toEqual([{ id: 'custom', title: 'Custom' }])
  })

  it('keeps cached values visible when the initial request fails', async () => {
    localStorage.setItem('ops_monitoring_enabled_cached', 'false')
    localStorage.setItem('payment_enabled_cached', 'true')
    mocks.getSidebarSettings.mockRejectedValueOnce(new Error('Offline'))
    const store = useAdminSettingsStore()
    await store.fetch()
    expect(store.opsMonitoringEnabled).toBe(false)
    expect(store.paymentEnabled).toBe(true)
    expect(localStorage.getItem('payment_enabled_cached')).toBe('true')
  })

  it('still reuses successfully loaded settings', async () => {
    const store = useAdminSettingsStore()
    await store.fetch()
    await store.fetch()
    expect(mocks.getSidebarSettings).toHaveBeenCalledTimes(1)
  })

  it('deduplicates pending requests and allows a forced refresh afterward', async () => {
    let resolve!: (value: Record<string, unknown>) => void
    mocks.getSidebarSettings.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const store = useAdminSettingsStore()
    const pending = store.fetch()
    await store.fetch(true)
    expect(mocks.getSidebarSettings).toHaveBeenCalledTimes(1)
    resolve({ payment_enabled: true })
    await pending
    await store.fetch(true)
    expect(mocks.getSidebarSettings).toHaveBeenCalledTimes(2)
  })
})
