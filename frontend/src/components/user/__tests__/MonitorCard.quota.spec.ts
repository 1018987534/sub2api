import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { UserMonitorView } from '@/api/channelMonitor'
import MonitorCard from '../monitor/MonitorCard.vue'

const { isQuotaVisible } = vi.hoisted(() => ({
  isQuotaVisible: vi.fn(() => false),
}))

vi.mock('@/utils/featureFlags', () => ({
  isChannelMonitorQuotaVisible: () => isQuotaVisible(),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, te: () => true }),
  }
})

function makeItem(overrides: Partial<UserMonitorView> = {}): UserMonitorView {
  return {
    id: 1,
    name: 'claude-main',
    provider: 'kimi',
    group_name: '',
    primary_model: 'quota',
    primary_status: 'operational',
    primary_latency_ms: null,
    primary_ping_latency_ms: null,
    availability_7d: 100,
    extra_models: [],
    timeline: [],
    model_count: 0,
    ...overrides,
  }
}

function mountCard(item: UserMonitorView) {
  return mount(MonitorCard, {
    props: {
      item,
      window: '7d',
      availabilityValue: 100,
      countdownSeconds: 0,
    },
    global: {
      stubs: {
        MonitorMetricPair: true,
        MonitorAvailabilityRow: true,
        MonitorTimeline: true,
      },
    },
  })
}

describe('MonitorCard quota snapshot visibility', () => {
  it('hides the quota block when the system switch is off even if data exists', () => {
    isQuotaVisible.mockReturnValue(false)
    const wrapper = mountCard(
      makeItem({
        latest_quota: { source: 'cn_quota', success: true, fetched_at: '2026-08-18T00:00:00Z' },
      }),
    )
    expect(wrapper.find('[data-testid="monitor-quota-view"]').exists()).toBe(false)
  })

  it('renders the quota block when the switch is on and a snapshot exists', () => {
    isQuotaVisible.mockReturnValue(true)
    const wrapper = mountCard(
      makeItem({
        latest_quota: {
          source: 'cn_quota',
          success: true,
          plan_level: 'kimi-plus',
          tiers: [{ window: 'daily', label: 'requests', used_percent: 60 }],
          fetched_at: '2026-08-18T00:00:00Z',
        },
      }),
    )
    expect(wrapper.find('[data-testid="monitor-quota-view"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('kimi-plus')
  })

  it('never renders the quota block without a snapshot', () => {
    isQuotaVisible.mockReturnValue(true)
    const wrapper = mountCard(makeItem())
    expect(wrapper.find('[data-testid="monitor-quota-view"]').exists()).toBe(false)
  })

  // 占位符 "quota" 是主模型存储值，不得作为假模型名直接透出到用户端。
  it('shows the localized quota label instead of the raw placeholder model', () => {
    const wrapper = mountCard(makeItem())
    expect(wrapper.text()).toContain('monitorCommon.checkMode.quota')
  })

  it('keeps the real model name for probe monitors', () => {
    const wrapper = mountCard(makeItem({ primary_model: 'claude-sonnet-4-5' }))
    expect(wrapper.text()).toContain('claude-sonnet-4-5')
    expect(wrapper.text()).not.toContain('monitorCommon.checkMode.quota')
  })

  it('renders the three models supplied by the live catalog preview', () => {
    const wrapper = mountCard(makeItem({
      model_count: 5,
      model_preview: [
        { name: 'gpt-5.6-sol', platform: 'openai', official_pricing: null },
        { name: 'gpt-5.6-terra', platform: 'openai', official_pricing: null },
        { name: 'gpt-5.6-luna', platform: 'openai', official_pricing: null },
      ],
    }))
    const preview = wrapper.get('[data-testid="monitor-model-preview"]')
    expect(preview.text()).toContain('gpt-5.6-sol')
    expect(preview.text()).toContain('gpt-5.6-terra')
    expect(preview.text()).toContain('gpt-5.6-luna')
    expect(preview.text()).toContain('+2')
  })

  it('does not substitute configured probe models when the live catalog is empty', () => {
    const wrapper = mountCard(makeItem({ primary_model: 'configured-probe', model_count: 0, model_preview: [] }))
    expect(wrapper.find('[data-testid="monitor-model-preview"]').exists()).toBe(false)
  })

  it('shows first-token latency without exposing its sample count', () => {
    const wrapper = mountCard(makeItem({
      group_first_token_p50_ms: 5210,
      group_first_token_sample_count: 7298,
    }))

    const metrics = wrapper.get('[data-testid="monitor-group-metrics"]')
    expect(metrics.text()).toContain('5.21s')
    expect(metrics.text()).not.toContain('7298')
  })
})
