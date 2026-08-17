import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import type { DashboardStats } from '@/types'
import DashboardView from '../DashboardView.vue'

const { getSnapshotV2, getUserUsageTrend, getUserSpendingRanking, getFirstTokenLatencies } = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn(),
  getFirstTokenLatencies: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUserUsageTrend,
      getUserSpendingRanking
    },
    accounts: { getFirstTokenLatencies }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const createDashboardStats = (): DashboardStats => ({
  total_users: 0,
  today_new_users: 0,
  active_users: 0,
  hourly_active_users: 0,
  stats_updated_at: '',
  stats_stale: false,
  total_api_keys: 0,
  active_api_keys: 0,
  total_accounts: 0,
  normal_accounts: 0,
  error_accounts: 0,
  ratelimit_accounts: 0,
  overload_accounts: 0,
  total_requests: 0,
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_creation_tokens: 0,
  total_cache_read_tokens: 0,
  total_tokens: 0,
  total_cost: 0,
  total_actual_cost: 0,
  today_requests: 0,
  today_input_tokens: 0,
  today_output_tokens: 0,
  today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
  average_duration_ms: 0,
  uptime: 0,
  rpm: 0,
  tpm: 0
})

describe('admin DashboardView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())

    getSnapshotV2.mockReset()
    getUserUsageTrend.mockReset()
    getUserSpendingRanking.mockReset()
    getFirstTokenLatencies.mockReset()

    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats(),
      trend: [],
      models: []
    })
    getUserUsageTrend.mockResolvedValue({
      trend: [],
      start_date: '',
      end_date: '',
      granularity: 'hour'
    })
    getUserSpendingRanking.mockResolvedValue({
      ranking: [],
      total_actual_cost: 0,
      total_requests: 0,
      total_tokens: 0,
      start_date: '',
      end_date: ''
    })
    getFirstTokenLatencies.mockResolvedValue({ items: [], total: 0 })
  })

  it('uses today as default dashboard range', async () => {
    mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()

    const now = new Date()
    const today = formatLocalDate(now)

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: today,
      end_date: today,
      granularity: 'hour'
    }))
    expect(getFirstTokenLatencies).toHaveBeenCalledTimes(1)
  })

  it('renders enabled API-key relay first-token predictions', async () => {
    getFirstTokenLatencies.mockResolvedValueOnce({
      items: [
        {
          account_id: 42,
          account_name: 'relay-fast',
          predicted_ms: 4321,
          has_prediction: true,
          is_fast_pool: true,
          scheduling_rate_multiplier: 0.045,
          groups: [{ group_id: 10, group_name: 'Alpha' }],
          sample_count: 8,
          updated_at: '2026-08-12T01:00:00Z',
          slow_streak: 0,
          recovery_fast_streak: 0,
          probe_interval_seconds: 120,
          cache_rate: 0.25,
          cache_read_tokens: 75,
          cache_rate_denominator: 300
        },
        {
          account_id: 44,
          account_name: 'relay-fast-expensive',
          predicted_ms: 2100,
          has_prediction: true,
          is_fast_pool: true,
          scheduling_rate_multiplier: 0.2,
          groups: [{ group_id: 10, group_name: 'Alpha' }],
          sample_count: 9,
          updated_at: '2026-08-12T01:00:00Z',
          slow_streak: 0,
          recovery_fast_streak: 0,
          probe_interval_seconds: 120,
          cache_rate: null,
          cache_read_tokens: 0,
          cache_rate_denominator: 0
        },
        {
          account_id: 43,
          account_name: 'relay-recovering',
          predicted_ms: 7000,
          has_prediction: true,
          is_fast_pool: false,
          scheduling_rate_multiplier: 0.08,
          groups: [{ group_id: 20, group_name: 'Beta' }],
          sample_count: 10,
          updated_at: '2026-08-12T01:00:00Z',
          slow_streak: 0,
          recovery_fast_streak: 1,
          probe_interval_seconds: 60,
          cache_rate: 0,
          cache_read_tokens: 0,
          cache_rate_denominator: 100
        }
      ],
      total: 3
    })

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })
    await flushPromises()

    expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('relay-fast')
    expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('4.32s')
    expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('Alpha')
    expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('Beta')
    expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('admin.dashboard.firstTokenPoolCounts')
    expect(wrapper.findAll('[data-testid="first-token-group-section"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-testid="first-token-latency-row"]')).toHaveLength(3)
    expect(wrapper.findAll('[data-testid="first-token-mobile-row"]')).toHaveLength(3)
    const rows = wrapper.findAll('[data-testid="first-token-latency-row"]')
    expect(rows[0].text()).toContain('relay-fast')
    expect(rows[1].text()).toContain('relay-fast-expensive')
    expect(rows[2].text()).toContain('relay-recovering')
    const pools = wrapper.findAll('[data-testid="first-token-pool"]')
    expect(pools[0].text()).toContain('admin.dashboard.firstTokenFastPool')
    expect(pools[1].text()).toContain('admin.dashboard.firstTokenFastPool')
    expect(pools[2].text()).toContain('admin.dashboard.firstTokenSlowPoolRecovering')
    const predictions = wrapper.findAll('[data-testid="first-token-prediction"]')
    expect(predictions[0].classes()).toContain('text-emerald-600')
    expect(predictions[1].classes()).toContain('text-emerald-600')
    expect(predictions[2].classes()).toContain('text-amber-600')
    const rates = wrapper.findAll('[data-testid="first-token-scheduling-rate"]')
    expect(rates[0].text()).toBe('0.045x')
    expect(rates[1].text()).toBe('0.20x')
    expect(rates[2].text()).toBe('0.08x')
    const cacheRates = wrapper.findAll('[data-testid="first-token-cache-rate"]')
    expect(cacheRates).toHaveLength(6)
    expect(cacheRates.filter((item) => item.text() === '25.0%')).toHaveLength(2)
    expect(cacheRates.filter((item) => item.text() === '-')).toHaveLength(2)
    expect(cacheRates.filter((item) => item.text() === '0.0%')).toHaveLength(2)
  })
})
