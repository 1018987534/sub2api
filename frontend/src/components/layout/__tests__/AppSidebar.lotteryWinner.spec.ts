import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AppSidebar from '../AppSidebar.vue'

const state = vi.hoisted(() => ({
  lottery: vi.fn(),
  auth: { isAdmin: true, isAuthenticated: true, isSimpleMode: false, user: { id: 7 } },
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
vi.mock('@/stores', async () => {
  const { reactive } = await import('vue')
  state.auth = reactive(state.auth)
  return {
  useAuthStore: () => state.auth,
  useAppStore: () => state.app,
  useAdminSettingsStore: () => ({ customMenuItems: [], fetch: vi.fn(), opsMonitoringEnabled: false, paymentEnabled: false }),
  useOnboardingStore: () => ({ isCurrentStep: () => false }),
  }
})

const winner = { id: 9, round_no: 22, prize_amount: 5 }
const current = (won: boolean, joined = true) => ({
  enabled: true, current_round: { status: 'drawn' }, joined,
  eligibility: { eligible: false }, recent_winners: [winner],
  my_recent_winners: won ? [winner] : [],
  unread_winner_id: won ? winner.id : 0,
  current_round_unread: true,
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
  state.auth.user = { id: 7 }
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

  it('shows only unread winnings, keeps read prizes hidden after remount, and alerts on a new win', async () => {
    const visible = () => wrapper!.find('a[href="/lottery"] [aria-label="您已中奖，查看中奖记录"]').exists()
    const observed: Record<string, boolean> = {}
    render()
    await flushPromises()
    observed.nonWinner = visible()
    state.lottery.mockResolvedValue(current(true))
    window.dispatchEvent(new Event('lottery-availability-changed'))
    await flushPromises()
    observed.unreadWin = visible()
    state.lottery.mockResolvedValue({ ...current(true), unread_winner_id: 0 })
    window.dispatchEvent(new Event('lottery-availability-changed'))
    await flushPromises()
    observed.readWin = visible()
    wrapper!.unmount()
    render()
    await flushPromises()
    observed.readAfterRemount = visible()
    state.lottery.mockResolvedValue({ ...current(true), unread_winner_id: 10 })
    await vi.advanceTimersByTimeAsync(15_000)
    observed.newWin = visible()
    console.info(`badge-transitions=${JSON.stringify(observed)}`)
    expect(observed).toEqual({ nonWinner: false, unreadWin: true, readWin: false, readAfterRemount: false, newWin: true })
  })

  it('clears the previous account prize immediately and ignores its late response', async () => {
    let resolvePrevious!: (value: ReturnType<typeof current>) => void
    state.lottery.mockImplementationOnce(() => new Promise(resolve => { resolvePrevious = resolve }))
    const view = render()
    state.auth.user = { id: 571 }
    state.lottery.mockResolvedValue(current(false))
    await flushPromises()
    resolvePrevious(current(true))
    await flushPromises()
    console.info(`account-switch: nonWinnerPrize=${view.find('a[href="/lottery"] [role="status"]').exists()}`)
    expect(view.find('a[href="/lottery"] [role="status"]').exists()).toBe(false)
  })

  it('clears the participation dot on read without joining and shows it for the next unread round', async () => {
    const available = { ...current(false, false), current_round: { id: 22, status: 'open' }, eligibility: { eligible: true } }
    state.lottery.mockResolvedValue(available)
    render()
    await flushPromises()
    const visible = () => wrapper!.find('a[href="/lottery"] [aria-label="nav.lotteryAvailable"]').exists()
    const observed: Record<string, boolean> = { unreadRound: visible() }
    state.lottery.mockResolvedValue({ ...available, current_round_unread: false })
    window.dispatchEvent(new Event('lottery-availability-changed'))
    await flushPromises()
    observed.readWithoutJoining = visible()
    wrapper!.unmount()
    render()
    await flushPromises()
    observed.readAfterRemount = visible()
    state.lottery.mockResolvedValue({ ...available, current_round: { id: 23, status: 'open' } })
    await vi.advanceTimersByTimeAsync(15_000)
    observed.nextRound = visible()
    console.info(`dot-transitions=${JSON.stringify(observed)}`)
    expect(observed).toEqual({ unreadRound: true, readWithoutJoining: false, readAfterRemount: false, nextRound: true })
  })
})
