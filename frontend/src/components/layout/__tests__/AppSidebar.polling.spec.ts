import { defineComponent, h, KeepAlive, nextTick, shallowRef } from 'vue'
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AppSidebar from '../AppSidebar.vue'

const api = vi.hoisted(() => ({
  unread: vi.fn(), lottery: vi.fn(), publicSettings: vi.fn(),
}))
vi.mock('@/api/supportChat', () => ({ default: { adminUnreadCount: api.unread, unreadCount: api.unread } }))
vi.mock('@/api/lottery', () => ({ default: { getCurrent: api.lottery } }))
vi.mock('@/components/common/VersionBadge.vue', () => ({ default: { template: '<span />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ path: '/monitor' }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/composables/useBatchImageAccess', () => ({ useBatchImageAccess: () => ({ canUseBatchImage: { value: false }, refreshBatchImageAccess: vi.fn() }) }))
vi.mock('@/utils/featureFlags', () => ({ FeatureFlags: {}, makeSidebarFlag: () => () => false }))
vi.mock('@/utils/siteBillingMode', () => ({ resolveSiteBillingMode: () => 'recharge_only' }))
vi.mock('@/stores', () => ({
  useAuthStore: () => ({ isAdmin: true, isAuthenticated: true, isSimpleMode: false }),
  useAppStore: () => ({
    sidebarCollapsed: false, mobileOpen: false, sidebarScrollTop: 0,
    siteName: 'Test', siteLogo: '', siteVersion: '', publicSettingsLoaded: true,
    cachedPublicSettings: { lottery_enabled: true, custom_menu_items: [] },
    fetchPublicSettings: api.publicSettings,
  }),
  useAdminSettingsStore: () => ({ customMenuItems: [], fetch: vi.fn(), opsMonitoringEnabled: false, paymentEnabled: false }),
  useOnboardingStore: () => ({ isCurrentStep: () => false }),
}))

let wrapper: VueWrapper | undefined
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  api.unread.mockResolvedValue(0)
  api.lottery.mockResolvedValue({ enabled: true, joined: false, eligibility: { eligible: true } })
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.clearAllTimers(); vi.useRealTimers() })

function mountViews() {
  const studio = defineComponent({ name: 'ImageStudioView', setup: () => () => h(AppSidebar) })
  const ordinary = defineComponent({ name: 'OrdinaryView', setup: () => () => h(AppSidebar) })
  const empty = defineComponent({ name: 'EmptyView', setup: () => () => h('div') })
  const current = shallowRef(studio)
  wrapper = mount(defineComponent({
    setup: () => () => h(KeepAlive, { include: 'ImageStudioView' }, { default: () => h(current.value) }),
  }), { global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, VersionBadge: true, Icon: true } } })
  return { current, studio, ordinary, empty }
}

describe('AppSidebar polling with KeepAlive', () => {
  it('keeps a single active sidebar after repeated visits to the cached image studio', async () => {
    const { current, studio, ordinary } = mountViews()
    await flushPromises()
    expect(api.unread).toHaveBeenCalledTimes(1) // mounted + activated must not start twice
    expect(vi.getTimerCount()).toBe(2)
    for (let visit = 0; visit < 20; visit++) {
      current.value = ordinary; await nextTick(); await flushPromises()
      current.value = studio; await nextTick(); await flushPromises()
    }
    current.value = ordinary; await nextTick(); await flushPromises()
    expect(vi.getTimerCount()).toBe(2)
    api.unread.mockClear(); api.lottery.mockClear()
    await vi.advanceTimersByTimeAsync(15_000)
    expect(api.unread).toHaveBeenCalledTimes(1)
    expect(api.lottery).toHaveBeenCalledTimes(1)
    window.dispatchEvent(new Event('support-chat-read'))
    window.dispatchEvent(new Event('lottery-availability-changed'))
    await flushPromises()
    expect(api.unread).toHaveBeenCalledTimes(2)
    expect(api.lottery).toHaveBeenCalledTimes(2)
  })

  it('stops timers and badge listeners while deactivated, then resumes on activation', async () => {
    const { current, studio, empty } = mountViews()
    await flushPromises()
    current.value = empty; await nextTick(); await flushPromises()
    api.unread.mockClear(); api.lottery.mockClear()
    window.dispatchEvent(new Event('support-chat-read'))
    window.dispatchEvent(new Event('lottery-availability-changed'))
    await vi.advanceTimersByTimeAsync(30_000)
    expect(api.unread).not.toHaveBeenCalled()
    expect(api.lottery).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    current.value = studio; await nextTick(); await flushPromises()
    expect(api.unread).toHaveBeenCalledTimes(1)
    expect(api.lottery).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(2)
    wrapper!.unmount(); wrapper = undefined
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not chain a public settings request from a late hidden lottery response', async () => {
    let resolve!: (value: { enabled: boolean }) => void
    api.lottery.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const { current, empty } = mountViews()
    current.value = empty; await nextTick()
    resolve({ enabled: false })
    await flushPromises()
    expect(api.publicSettings).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })
})
