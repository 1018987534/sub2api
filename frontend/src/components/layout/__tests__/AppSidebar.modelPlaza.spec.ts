import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AppSidebar from '../AppSidebar.vue'

const auth = vi.hoisted(() => ({ isAdmin: false, isAuthenticated: true, isSimpleMode: false, user: { id: 1 } }))

vi.mock('@/api/supportChat', () => ({ default: { adminUnreadCount: async () => 0, unreadCount: async () => 0 } }))
vi.mock('@/api/lottery', () => ({ default: { getCurrent: async () => ({ enabled: false }) } }))
vi.mock('@/components/common/VersionBadge.vue', () => ({ default: { template: '<span />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ path: '/custom-model-plaza' }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('@/composables/useBatchImageAccess', () => ({ useBatchImageAccess: () => ({ canUseBatchImage: { value: false }, refreshBatchImageAccess: vi.fn() }) }))
vi.mock('@/utils/featureFlags', () => ({ FeatureFlags: {}, makeSidebarFlag: () => () => false }))
vi.mock('@/utils/siteBillingMode', () => ({ resolveSiteBillingMode: () => 'recharge_only' }))
vi.mock('@/stores', () => ({
  useAuthStore: () => auth,
  useAppStore: () => ({
    sidebarCollapsed: false, mobileOpen: false, sidebarScrollTop: 0,
    siteName: 'Test', siteLogo: '', siteVersion: '', publicSettingsLoaded: true,
    cachedPublicSettings: { lottery_enabled: false, custom_menu_items: [] },
    fetchPublicSettings: vi.fn(),
  }),
  useAdminSettingsStore: () => ({ customMenuItems: [], fetch: vi.fn(), opsMonitoringEnabled: false, paymentEnabled: false }),
  useOnboardingStore: () => ({ isCurrentStep: () => false }),
}))

function sidebarLinks(isAdmin: boolean) {
  auth.isAdmin = isAdmin
  const wrapper = mount(AppSidebar, {
    global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' }, Icon: true } },
  })
  try {
    return wrapper.findAll('nav a').map(link => link.attributes('href'))
  } finally {
    wrapper.unmount()
  }
}

describe('AppSidebar model plaza', () => {
  it('leaves the user sidebar free of the model plaza link', () => {
    const links = sidebarLinks(false)
    expect(links).toContain('/keys')
    expect(links).not.toContain('/admin/custom-model-plaza')
    expect(links).not.toContain('/custom-model-plaza')
  })

  it('keeps pricing administration but omits the personal model plaza link', () => {
    const links = sidebarLinks(true)
    expect(links).toContain('/keys')
    expect(links).toContain('/admin/custom-model-plaza')
    expect(links).not.toContain('/custom-model-plaza')
  })
})
