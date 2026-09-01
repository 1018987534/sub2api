import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import type { AccountFirstTokenLatencyMetric, DashboardStats } from '@/types'
import DashboardView from '../DashboardView.vue'

const { getSnapshotV2, getUserUsageTrend, getUserSpendingRanking, getFirstTokenLatencies, requestFirstTokenManualProbe, showSuccess, showError } = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn(),
  getFirstTokenLatencies: vi.fn(),
  requestFirstTokenManualProbe: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUserUsageTrend,
      getUserSpendingRanking
    },
    accounts: { getFirstTokenLatencies, requestFirstTokenManualProbe }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess,
    showError
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

const createFirstTokenMetric = (
  overrides: Partial<AccountFirstTokenLatencyMetric> = {}
): AccountFirstTokenLatencyMetric => ({
  account_id: 42,
  account_name: 'relay-fast',
  predicted_ms: 4321,
  normal_total_ms: 4321,
  p50_ms: 3900,
  p90_ms: 8100,
  has_prediction: true,
  is_fast_pool: true,
  scheduling_rate_multiplier: 0.045,
  groups: [{ group_id: 10, group_name: 'Alpha' }],
  sample_count: 8,
  sample_window_size: 10,
  updated_at: '2026-08-12T01:00:00Z',
  slow_streak: 0,
  recovery_fast_streak: 0,
  probe_interval_seconds: 120,
  cache_rate: 0.25,
  cache_read_tokens: 75,
  cache_rate_denominator: 300,
  ...overrides
})

describe('admin DashboardView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())

    getSnapshotV2.mockReset()
    getUserUsageTrend.mockReset()
    getUserSpendingRanking.mockReset()
    getFirstTokenLatencies.mockReset()
    requestFirstTokenManualProbe.mockReset()
    showSuccess.mockReset()
    showError.mockReset()

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
    requestFirstTokenManualProbe.mockResolvedValue({ account_id: 42, queued: true })
  })

  it('uses today as default dashboard range', async () => {
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

    const now = new Date()
    const today = formatLocalDate(now)

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: today,
      end_date: today,
      granularity: 'hour'
    }))
    expect(getFirstTokenLatencies).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('renders enabled API-key relay first-token predictions', async () => {
    getFirstTokenLatencies.mockResolvedValueOnce({
      items: [
        {
          account_id: 42,
          account_name: 'relay-fast',
          predicted_ms: 4321,
          normal_total_ms: 7400,
          p50_ms: 6000,
          p90_ms: 14000,
          has_prediction: true,
          is_fast_pool: true,
          scheduling_rate_multiplier: 0.045,
          groups: [{ group_id: 10, group_name: 'Alpha' }],
          sample_count: 8,
          sample_window_size: 10,
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
    expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('7.40s')
    expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('P50 6.00s / P90 14.00s')
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

    const manualProbeButtons = wrapper.findAll('[data-testid="first-token-manual-probe"]')
    expect(manualProbeButtons).toHaveLength(6)
    await manualProbeButtons[0].trigger('click')
    await flushPromises()
    expect(requestFirstTokenManualProbe).toHaveBeenCalledWith(42)
    expect(getFirstTokenLatencies).toHaveBeenCalledTimes(2)
    expect(showSuccess).toHaveBeenCalledWith('admin.dashboard.firstTokenManualProbeQueued')
    wrapper.unmount()
  })

  it('refreshes first-token metrics periodically and stops after unmount', async () => {
    vi.useFakeTimers()
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

    try {
      await flushPromises()
      expect(getFirstTokenLatencies).toHaveBeenCalledTimes(1)

      await vi.advanceTimersByTimeAsync(30_000)
      await flushPromises()
      expect(getFirstTokenLatencies).toHaveBeenCalledTimes(2)

      wrapper.unmount()
      await vi.advanceTimersByTimeAsync(30_000)
      expect(getFirstTokenLatencies).toHaveBeenCalledTimes(2)
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it('preserves existing metrics when a background refresh fails', async () => {
    vi.useFakeTimers()
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    getFirstTokenLatencies
      .mockResolvedValueOnce({ items: [createFirstTokenMetric()], total: 1 })
      .mockRejectedValueOnce(new Error('temporary refresh failure'))

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

    try {
      await flushPromises()
      expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('relay-fast')

      await vi.advanceTimersByTimeAsync(30_000)
      await flushPromises()
      expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('relay-fast')
      expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).not.toContain('admin.dashboard.firstTokenLatencyFailed')
    } finally {
      wrapper.unmount()
      consoleError.mockRestore()
      vi.useRealTimers()
    }
  })

  it('does not overlap periodic refresh requests', async () => {
    vi.useFakeTimers()
    let finishRefresh: ((value: { items: AccountFirstTokenLatencyMetric[]; total: number }) => void) | undefined
    const pendingRefresh = new Promise<{ items: AccountFirstTokenLatencyMetric[]; total: number }>((resolve) => {
      finishRefresh = resolve
    })
    getFirstTokenLatencies
      .mockResolvedValueOnce({ items: [createFirstTokenMetric()], total: 1 })
      .mockReturnValueOnce(pendingRefresh)

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

    try {
      await flushPromises()
      await vi.advanceTimersByTimeAsync(60_000)
      expect(getFirstTokenLatencies).toHaveBeenCalledTimes(2)
      finishRefresh?.({ items: [createFirstTokenMetric()], total: 1 })
      await flushPromises()
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it('shows an error when a manual first-token probe cannot be queued', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    getFirstTokenLatencies.mockResolvedValueOnce({ items: [createFirstTokenMetric()], total: 1 })
    requestFirstTokenManualProbe.mockRejectedValueOnce(new Error('probe unavailable'))
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

    try {
      await flushPromises()
      await wrapper.findAll('[data-testid="first-token-manual-probe"]')[0].trigger('click')
      await flushPromises()
      expect(requestFirstTokenManualProbe).toHaveBeenCalledTimes(1)
      expect(getFirstTokenLatencies).toHaveBeenCalledTimes(1)
      expect(showError).toHaveBeenCalledWith('admin.dashboard.firstTokenManualProbeFailed')
    } finally {
      wrapper.unmount()
      consoleError.mockRestore()
    }
  })

  it('renders the first-token error state when the initial request fails', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    getFirstTokenLatencies.mockRejectedValueOnce(new Error('metrics unavailable'))
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

    try {
      await flushPromises()
      expect(wrapper.get('[data-testid="first-token-latency-panel"]').text()).toContain('admin.dashboard.firstTokenLatencyFailed')
    } finally {
      wrapper.unmount()
      consoleError.mockRestore()
    }
  })

  it('ignores repeated manual probe clicks while the first request is pending', async () => {
    let finishProbe: ((value: { account_id: number; queued: boolean }) => void) | undefined
    const pendingProbe = new Promise<{ account_id: number; queued: boolean }>((resolve) => {
      finishProbe = resolve
    })
    getFirstTokenLatencies.mockResolvedValue({ items: [createFirstTokenMetric()], total: 1 })
    requestFirstTokenManualProbe.mockReturnValueOnce(pendingProbe)
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

    try {
      await flushPromises()
      const probeButton = wrapper.findAll('[data-testid="first-token-manual-probe"]')[0]
      await probeButton.trigger('click')
      await probeButton.trigger('click')
      expect(requestFirstTokenManualProbe).toHaveBeenCalledTimes(1)

      finishProbe?.({ account_id: 42, queued: true })
      await flushPromises()
      expect(showSuccess).toHaveBeenCalledWith('admin.dashboard.firstTokenManualProbeQueued')
    } finally {
      wrapper.unmount()
    }
  })
})
