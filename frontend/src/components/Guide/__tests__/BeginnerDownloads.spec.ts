import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import BeginnerGuide from '../BeginnerGuide.vue'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post } }))

describe('protected beginner downloads', () => {
  afterEach(() => { vi.unstubAllGlobals(); localStorage.clear(); post.mockReset() })
  it('requests only a file ID and shows an actionable quota message with official fallback', async () => {
    localStorage.setItem('auth_token', 'test-token')
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ schema: 1, updated_at: '2026-10-24T00:00:00Z', ccswitch_version: 'v4.0.19', files: { 'ccswitch-macos': { url: '/downloads/clients/releases/test-01/test.dmg', name: 'test.dmg', bytes: 100, sha256: 'a'.repeat(64), source: 'https://github.com/farion1231/cc-switch/releases/latest' } } }) }))
    post.mockRejectedValue({ status: 429, message: '本站下载次数或流量额度已达上限，请稍后再试，或使用官方下载。' })
    const wrapper = mount(BeginnerGuide)
    await flushPromises()
    await wrapper.get('button[aria-pressed]').trigger('click')
    const link = wrapper.get('[data-testid="ccswitch-download"]')
    expect(link.attributes('href')).toBe('#download')
    await link.trigger('click'); await flushPromises()
    expect(post).toHaveBeenCalledWith('/client-downloads/tickets', { id: 'ccswitch-macos' })
    expect(wrapper.get('[role="alert"]').text()).toContain('额度已达上限')
    expect(wrapper.findAll('a').some(a => a.text().includes('官方下载') && a.attributes('href')?.startsWith('https://github.com/'))).toBe(true)
    expect(wrapper.findAll('img')).toHaveLength(3)
    wrapper.unmount()
  })
})
