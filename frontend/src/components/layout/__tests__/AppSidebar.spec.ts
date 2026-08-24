import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('persists the latest position while the sidebar is scrolling', () => {
    expect(componentSource).toContain('@scroll.passive="handleSidebarScroll"')
    expect(componentSource).toContain('lastKnownSidebarScrollTop.value = scrollTop')
    expect(componentSource).toContain('appStore.sidebarScrollTop = scrollTop')
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })

  it('preserves scroll position when the image studio layout is kept alive', () => {
    expect(componentSource).toContain('onActivated(restoreSidebarScrollPosition)')
    expect(componentSource).toContain('onDeactivated(persistSidebarScrollPosition)')
    expect(componentSource).toContain('onBeforeUnmount(persistSidebarScrollPosition)')
    expect(componentSource).toContain('appStore.sidebarScrollTop = lastKnownSidebarScrollTop.value')
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })
})

describe('AppSidebar image studio access', () => {
  it('shows image studio below profile and before custom recharge menus', () => {
    const profileIndex = componentSource.indexOf("{ path: '/profile', label: t('nav.profile'), icon: UserIcon }")
    const imageStudioIndex = componentSource.indexOf("{ path: '/image-studio', label: t('nav.imageStudio'), icon: AIImageIcon }")
    const customMenuIndex = componentSource.indexOf('...customMenuItemsForUser.value.map')

    expect(profileIndex).toBeGreaterThan(-1)
    expect(imageStudioIndex).toBeGreaterThan(profileIndex)
    expect(customMenuIndex).toBeGreaterThan(imageStudioIndex)
    expect(componentSource).not.toContain('flagImageStudioPreview')
    expect(componentSource).not.toContain('canAccessImageStudioPreview')
  })

  it('uses a dedicated colored sparkle icon instead of the batch camera icon', () => {
    expect(componentSource).toContain('const AIImageIcon = {')
    expect(componentSource).toContain("stroke: '#2dd4bf'")
    expect(componentSource).toContain("stroke: '#34d399'")
    expect(componentSource).toContain("{ path: '/image-studio', label: t('nav.imageStudio'), icon: AIImageIcon }")
  })
})

describe('AppSidebar support unread badge', () => {
  it('polls the role-specific support unread count and renders a red status dot', () => {
    expect(componentSource).toContain("supportChatAPI.adminUnreadCount()")
    expect(componentSource).toContain("supportChatAPI.unreadCount()")
    expect(componentSource).toContain("path === '/admin/support'")
    expect(componentSource).toContain("path === '/support'")
    expect(componentSource).toContain('bg-red-500')
    expect(componentSource).toContain("window.addEventListener('support-chat-read'")
  })
})

describe('AppSidebar customer support icon', () => {
  it('uses the customer service avatar for user support and the admin inbox', () => {
    expect(componentSource).toContain('const CustomerSupportIcon = {')
    expect(componentSource).toContain("{ path: '/support', label: t('nav.support'), icon: CustomerSupportIcon }")
    expect(componentSource).toContain("{ path: '/admin/support', label: t('nav.supportInbox'), icon: CustomerSupportIcon }")
  })

  it('keeps the ticket icon for redeem codes', () => {
    expect(componentSource).toContain("{ path: '/admin/redeem', label: t('nav.redeemCodes'), icon: TicketIcon")
  })
})
