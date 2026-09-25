import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import ChannelStatusCardsView from '../ChannelStatusCardsView.vue'
import { getMatrix, getSnapshot, type MonitorMatrixResponse, type MonitorSnapshot } from '@/api/channelMonitorV2'
import { getIntelligenceStatus } from '@/api/intelligence'

const state = vi.hoisted(() => ({ enabled: null as unknown }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/utils/featureFlags', () => ({ isChannelMonitorV2Mode: () => (state.enabled as { value: boolean }).value }))
vi.mock('@/api/channelMonitorV2', () => ({ getMatrix: vi.fn(), getSnapshot: vi.fn() }))
vi.mock('@/api/intelligence', () => ({ getIntelligenceStatus: vi.fn() }))
const view = () => mount(ChannelStatusCardsView, { global: { stubs: {
  PlatformIcon: true,
  GroupMonitorCard: { props: ['row'], template: '<article>{{ row.group_name }}</article>' }
} } })
beforeEach(() => {
  vi.clearAllMocks()
  state.enabled = ref(true)
  vi.mocked(getMatrix).mockResolvedValue({ items: [{ group_id: 1, group_name: 'Visible Group', platform: 'openai' }] } as MonitorMatrixResponse)
  vi.mocked(getSnapshot).mockResolvedValue({
    config: { refresh_interval_seconds: 60 }, health: { overall: 'healthy' },
    coverage: { coverage_complete: true, data_through: '2026-09-25T12:00:00Z' },
    metrics: { error_rate: 0, cache_rate: 0.8 }
  } as MonitorSnapshot)
  vi.mocked(getIntelligenceStatus).mockResolvedValue({ groups: { 1: [] }, window_minutes: 60, server_time: new Date().toISOString() })
})
describe('cards view independent read path', () => {
  it('uses existing matrix/snapshot APIs and clears data when V2 is disabled', async () => {
    const wrapper = view(); await flushPromises()
    expect(getMatrix).toHaveBeenCalledWith(expect.objectContaining({ range: '90m' }), 'platform_group', false, expect.any(AbortSignal))
    expect(getIntelligenceStatus).toHaveBeenCalledWith([1], expect.any(AbortSignal))
    expect(wrapper.text()).toContain('Visible Group')
    ;(state.enabled as { value: boolean }).value = false
    await flushPromises()
    expect(wrapper.text()).not.toContain('Visible Group')
    expect(wrapper.text()).toContain('请先在系统配置中启用渠道监控 V2')
    wrapper.unmount()
  })
  it('retains passive metrics and marks a detection API failure explicitly', async () => {
    vi.mocked(getIntelligenceStatus).mockRejectedValue(new Error('offline'))
    const wrapper = view(); await flushPromises()
    expect(wrapper.text()).toContain('Visible Group')
    expect(wrapper.text()).toContain('降智检测状态暂时无法更新')
    wrapper.unmount()
  })
  it('does not synthesize cards on initial passive API failure', async () => {
    vi.mocked(getMatrix).mockRejectedValue(new Error('offline'))
    const wrapper = view(); await flushPromises()
    expect(wrapper.text()).toContain('监控数据加载失败')
    expect(wrapper.findAll('article')).toHaveLength(0)
    expect(getIntelligenceStatus).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
