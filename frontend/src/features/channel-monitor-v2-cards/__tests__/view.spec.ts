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
  GroupMonitorCard: { props: ['row', 'probeMetadata'], template: '<article>{{ row.group_name }} {{ probeMetadata?.model }} {{ probeMetadata?.reasoning_effort }}</article>' }
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
  vi.mocked(getIntelligenceStatus).mockResolvedValue({ groups: { 1: [] }, metadata: { 1: { model: "dynamic-model", reasoning_effort: "medium" } }, window_minutes: 60, server_time: new Date().toISOString() })
})
describe('cards view independent read path', () => {
  it('renders cards and detection status while the snapshot is still pending', async () => {
    let resolve!: (value: MonitorSnapshot) => void
    vi.mocked(getSnapshot).mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = view(); await flushPromises()
    expect(wrapper.text()).toContain('Visible Group')
    expect(wrapper.text()).toContain('dynamic-model medium')
    expect(wrapper.text()).not.toContain('可用率 100%')
    expect(wrapper.get('[aria-label="刷新渠道状态"]').attributes('disabled')).toBeDefined()
    resolve({ config: { refresh_interval_seconds: 60 }, health: { overall: 'healthy' }, coverage: { coverage_complete: true, data_through: '2026-10-01T00:00:00Z' }, metrics: { error_rate: 0, cache_rate: 0.8 } } as MonitorSnapshot)
    await flushPromises()
    expect(wrapper.text()).toContain('缓存率 80.0%')
    wrapper.unmount()
  })

  it('retains cards and marks snapshot failure explicitly', async () => {
    vi.mocked(getSnapshot).mockRejectedValueOnce(new Error('offline'))
    const wrapper = view(); await flushPromises()
    expect(wrapper.text()).toContain('Visible Group')
    expect(wrapper.text()).toContain('监控汇总加载失败')
    expect(wrapper.get('[aria-label="刷新渠道状态"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('ignores stale matrix and snapshot responses after changing the range', async () => {
    let resolveMatrix!: (value: MonitorMatrixResponse) => void
    let resolveSnapshot!: (value: MonitorSnapshot) => void
    vi.mocked(getMatrix).mockReturnValueOnce(new Promise(done => { resolveMatrix = done }))
    vi.mocked(getSnapshot).mockReturnValueOnce(new Promise(done => { resolveSnapshot = done }))
    const wrapper = view(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === '24h')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Visible Group')
    resolveMatrix({ items: [{ group_id: 2, group_name: 'Stale Group', platform: 'openai' }] } as MonitorMatrixResponse)
    resolveSnapshot({ coverage: { data_through: '2000-01-01T00:00:00Z' } } as MonitorSnapshot)
    await flushPromises()
    expect(wrapper.text()).not.toContain('Stale Group')
    expect(wrapper.text()).not.toContain('2000')
    expect(getIntelligenceStatus).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('preserves last successful cards and summary after a failed refresh', async () => {
    const wrapper = view(); await flushPromises()
    vi.mocked(getMatrix).mockRejectedValueOnce(new Error('offline'))
    vi.mocked(getSnapshot).mockRejectedValueOnce(new Error('offline'))
    await wrapper.get('[aria-label="刷新渠道状态"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Visible Group')
    expect(wrapper.text()).toContain('缓存率 80.0%')
    expect(wrapper.text()).toContain('监控数据加载失败；监控汇总加载失败')
    wrapper.unmount()
  })

  it('does not restore a pending snapshot after V2 is disabled', async () => {
    let resolve!: (value: MonitorSnapshot) => void
    vi.mocked(getSnapshot).mockReturnValueOnce(new Promise(done => { resolve = done }))
    const wrapper = view(); await flushPromises()
    ;(state.enabled as { value: boolean }).value = false
    await flushPromises()
    resolve({ coverage: { data_through: '2000-01-01T00:00:00Z' } } as MonitorSnapshot)
    await flushPromises()
    expect(wrapper.text()).not.toContain('Visible Group')
    expect(wrapper.text()).not.toContain('2000')
    wrapper.unmount()
  })

  it('uses existing matrix/snapshot APIs and clears data when V2 is disabled', async () => {
    const wrapper = view(); await flushPromises()
    expect(getMatrix).toHaveBeenCalledWith(expect.objectContaining({ range: '90m' }), 'platform_group', false, expect.any(AbortSignal))
    expect(getIntelligenceStatus).toHaveBeenCalledWith([1], expect.any(AbortSignal))
    expect(wrapper.text()).toContain('Visible Group')
    expect(wrapper.text()).toContain('dynamic-model medium')
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
