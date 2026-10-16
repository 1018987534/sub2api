import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AppSidebar from '../AppSidebar.vue'

const state = vi.hoisted(() => ({
  lottery: vi.fn(),
  auth: { isAdmin: true, isAuthenticated: true, isSimpleMode: false },
  app: {
    sidebarCollapsed: false, mobileOpen: false, sidebarScrollTop: 0,
    siteName: 'Test', siteLogo: '', siteVersion: '', publicSettingsLoaded: true,
    backendModeEnabled: false, cachedPublicSettings: { lottery_enabled: true, custom_menu_items: [] },
    fetchPublicSettings: vi.fn(),
  },
}))
vi.mock('@/api/lottery', () => ({ default: { getCurrent: state.lottery } }))
vi.mock('@/api/supportChat', () => ({ default: { adminUnreadCount: async () => 0, unreadCount: async () => 0 } }))
vi.mock('@/components/common/VersionBadge.vue', () => ({ default: { template: '<span />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => ({
  'nav.lotteryPrizeBadge': '奖', 'nav.lotteryWon': '您已中奖，查看中奖记录',
}[key] || key) }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ path: '/monitor' }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/composables/useBatchImageAccess', () => ({ useBatchImageAccess: () => ({ canUseBatchImage: { value: false }, refreshBatchImageAccess: vi.fn() }) }))
vi.mock('@/utils/featureFlags', () => ({ FeatureFlags: {}, makeSidebarFlag: () => () => true }))
vi.mock('@/utils/siteBillingMode', () => ({ resolveSiteBillingMode: () => 'recharge_only' }))
vi.mock('@/stores', () => ({
  useAuthStore: () => state.auth,
  useAppStore: () => state.app,
  useAdminSettingsStore: () => ({ customMenuItems: [], fetch: vi.fn(), opsMonitoringEnabled: false, paymentEnabled: false }),
  useOnboardingStore: () => ({ isCurrentStep: () => false }),
}))

const winner = { id: 9, round_no: 22, prize_amount: 5 }
const current = (won: boolean, joined = true) => ({
  enabled: true, current_round: { status: 'drawn' }, joined,
  eligibility: { eligible: false }, recent_winners: [winner],
  my_recent_winners: won ? [winner] : [],
})
let wrapper: VueWrapper | undefined
function render() {
  wrapper = mount(AppSidebar, { global: { stubs: {
    RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' }, VersionBadge: true,
  } } })
  return wrapper
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  state.auth.isAdmin = true
  state.auth.isAuthenticated = true
  state.app.sidebarCollapsed = false
  state.lottery.mockResolvedValue(current(false))
})
afterEach(() => { wrapper?.unmount(); wrapper = undefined; vi.clearAllTimers(); vi.useRealTimers() })

describe('AppSidebar lottery winner badge', () => {
  it.each([true, false])('marks personal winnings for admin=%s even after the round is drawn', async admin => {
    state.auth.isAdmin = admin
    state.lottery.mockResolvedValue(current(true))
    const view = render()
    await flushPromises()
    const personal = view.get('a[href="/lottery"]')
    console.info(`lottery: admin=${admin} joined=true status=drawn prizeBadge=${personal.text().includes('奖')}`)
    expect(personal.get('[role="status"]').text()).toBe('奖')
    expect(personal.get('[role="status"]').attributes('aria-label')).toBe('您已中奖，查看中奖记录')
    expect(view.find('a[href="/admin/lottery"] [role="status"]').exists()).toBe(false)
  })

  it('keeps the prize visible with a collapsed sidebar', async () => {
    state.app.sidebarCollapsed = true
    state.lottery.mockResolvedValue(current(true))
    const view = render()
    await flushPromises()
    const badge = view.get('a[href="/lottery"] [role="status"]')
    expect(badge.text()).toBe('奖')
    expect(badge.attributes('aria-hidden')).toBeUndefined()
  })

  it('does not mark another user winning and preserves the available lottery dot', async () => {
    state.lottery.mockResolvedValue({ ...current(false, false), current_round: { status: 'open' }, eligibility: { eligible: true } })
    const view = render()
    await flushPromises()
    const badge = view.get('a[href="/lottery"] [role="status"]')
    expect(badge.attributes('aria-label')).toBe('nav.lotteryAvailable')
    expect(badge.text()).not.toBe('奖')
  })

  it('refreshes the prize from personal results and clears it when absent', async () => {
    const view = render()
    await flushPromises()
    expect(view.find('a[href="/lottery"] [role="status"]').exists()).toBe(false)
    state.lottery.mockResolvedValue(current(true, false))
    await vi.advanceTimersByTimeAsync(15_000)
    expect(view.get('a[href="/lottery"] [role="status"]').text()).toBe('奖')
    state.lottery.mockResolvedValue(current(false))
    window.dispatchEvent(new Event('lottery-availability-changed'))
    await flushPromises()
    expect(view.find('a[href="/lottery"] [role="status"]').exists()).toBe(false)
  })
})
