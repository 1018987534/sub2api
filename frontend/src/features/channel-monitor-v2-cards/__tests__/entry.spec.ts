import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref, nextTick } from 'vue'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import ChannelStatusView from '@/views/user/ChannelStatusView.vue'
const mode = vi.hoisted(() => ({ v1: null as unknown, v2: null as unknown }))
vi.mock('@/utils/featureFlags', () => ({
 isChannelMonitorV1Mode: () => (mode.v1 as { value: boolean }).value,
 isChannelMonitorV2Mode: () => (mode.v2 as { value: boolean }).value
}))
vi.mock('@/views/user/ChannelStatusV1View.vue', () => ({ default: { template: '<div>official-v1</div>' } }))
vi.mock('../ChannelStatusCardsView.vue', () => ({ default: { template: '<div>configured-v2-cards</div>' } }))
describe('single official customer monitor entry', () => {
 it('switches the original route by configuration and renders nothing when disabled', async () => {
  mode.v1 = ref(true); mode.v2 = ref(false)
  const wrapper = mount(ChannelStatusView)
  expect(wrapper.text()).toBe('official-v1')
  ;(mode.v1 as { value: boolean }).value = false
  ;(mode.v2 as { value: boolean }).value = true
  await nextTick()
  expect(wrapper.text()).toBe('configured-v2-cards')
  ;(mode.v2 as { value: boolean }).value = false
  await nextTick()
  expect(wrapper.text()).toBe('')
  wrapper.unmount()
 })
 it('has only the original shared customer/admin self-nav item and redirects old bookmarks', () => {
  const sidebar = readFileSync(resolve('src/components/layout/AppSidebar.vue'), 'utf8')
  const routes = readFileSync(resolve('src/router/index.ts'), 'utf8')
  expect(sidebar).not.toContain("path: '/monitor-v2'")
  expect(sidebar.match(/path: '\/monitor'/g)).toHaveLength(1)
  expect(routes).toMatch(/path: '\/monitor-v2',[\s\S]*?redirect: '\/monitor'/)
  expect(routes).toMatch(/path: '\/monitor',[\s\S]*?requiresAuth: true/)
 })
})
