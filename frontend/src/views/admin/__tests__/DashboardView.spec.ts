import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import type { AccountFirstTokenLatencyMetric, DashboardStats } from '@/types'
import DashboardView from '../DashboardView.vue'

const { getSnapshotV2, getUserUsageTrend, getUserSpendingRanking, getFirstTokenLatencies, requestFirstTokenManualProbe, getTotalDurationCircuitBreakerSettings, getFastPoolPrioritySettings, updateFastPoolPrioritySettings, getDashboardProfitSettings, updateDashboardProfitSettings, showSuccess, showError } = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn(),
  getFirstTokenLatencies: vi.fn(),
  requestFirstTokenManualProbe: vi.fn(),
  getTotalDurationCircuitBreakerSettings: vi.fn(),
  getFastPoolPrioritySettings: vi.fn(),
  updateFastPoolPrioritySettings: vi.fn(),
  getDashboardProfitSettings: vi.fn(),
  updateDashboardProfitSettings: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('vue-chartjs', () => ({
  Line: { name: 'Line', props: ['data', 'options'], template: '<div class="line-chart" />' }
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUserUsageTrend,
      getUserSpendingRanking
    },
    accounts: { getFirstTokenLatencies, requestFirstTokenManualProbe },
    settings: { getTotalDurationCircuitBreakerSettings, getFastPoolPrioritySettings, updateFastPoolPrioritySettings, getDashboardProfitSettings, updateDashboardProfitSettings }
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
  total_account_cost: 0,
  today_requests: 0,
  today_input_tokens: 0,
  today_output_tokens: 0,
  today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
  today_account_cost: 0,
  today_server_cost_usd: 0,
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
  window_hours: 6,
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
    getTotalDurationCircuitBreakerSettings.mockReset()
    getTotalDurationCircuitBreakerSettings.mockResolvedValue({ max_slow_requests: 15, threshold_seconds: 180, window_seconds: 1800 })
    getFastPoolPrioritySettings.mockReset()
    getFastPoolPrioritySettings.mockResolvedValue({ mode: 'intelligence', cache_rate_threshold_percent: null })
    updateFastPoolPrioritySettings.mockReset()
    updateFastPoolPrioritySettings.mockImplementation(async (mode: string, cache_rate_threshold_percent: number | null) => ({ mode, cache_rate_threshold_percent }))
    getDashboardProfitSettings.mockReset()
    getDashboardProfitSettings.mockResolvedValue({ monthly_server_cost_usd: 0, daily_server_cost_usd: 0, month_days: 30 })
    updateDashboardProfitSettings.mockReset()
    updateDashboardProfitSettings.mockImplementation(async (monthly: number) => ({ monthly_server_cost_usd: monthly, daily_server_cost_usd: monthly / 30, month_days: 30 }))
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

  it.each([
    { actual: 315.88, cost: 202, own: 20, lottery: 0, fee: 5.32584, profit: 88.55416, display: '$88.55', color: 'text-emerald-600' },
    { actual: 315.88, cost: 202, own: 20, lottery: 5, fee: 5.32584, profit: 83.55416, display: '$83.55', color: 'text-emerald-600' },
    { actual: 10, cost: 5, own: 2, lottery: 10, fee: 0.144, profit: -7.144, display: '-$7.14', color: 'text-red-600' },
    { actual: 0, cost: 0, own: 0, lottery: 0, fee: 0, profit: 0, display: '$0.00', color: 'text-emerald-600' },
    { actual: 251.93, cost: 136.25, own: 3.61, lottery: 0, fee: 4.46976, profit: 107.60024, display: '$107.60', color: 'text-emerald-600' },
    { actual: 251.93, cost: 136.25, own: 3.61, lottery: 0, fee: 4.46976, serverCost: 20, profit: 87.60024, display: '$87.60', color: 'text-emerald-600' }
  ])('shows daily profit including withdrawal fees and server cost ($display)', async ({ actual, cost, own, lottery, fee, serverCost = 0, profit, display, color }) => {
    getSnapshotV2.mockResolvedValueOnce({
      stats: { ...createDashboardStats(), today_actual_cost: actual, today_account_cost: cost, today_admin_actual_cost: own, today_lottery_cost: lottery, today_withdrawal_fee: fee, today_server_cost_usd: serverCost, today_profit: profit },
      trend: [], models: []
    })
    const wrapper = mount(DashboardView, {
      global: { stubs: {
        AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true,
        Icon: true, DateRangePicker: true, Select: true, ModelDistributionChart: true, TokenUsageTrend: true
      } }
    })
    await flushPromises()
    expect(wrapper.get('[data-testid="today-profit"]').text()).toBe(display)
    expect(wrapper.get('[data-testid="today-profit"]').classes()).toContain(color)
    const breakdown = wrapper.get('[data-testid="today-profit-breakdown"]').text()
    expect(breakdown).toContain(`admin.dashboard.actual $${actual.toFixed(2)}`)
    expect(breakdown).toContain(`admin.dashboard.accountCost $${cost.toFixed(2)}`)
    expect(breakdown).toContain(`admin.dashboard.adminSelfUsage $${own.toFixed(2)}`)
    expect(breakdown).toContain(`admin.dashboard.lotteryCost $${lottery.toFixed(2)}`)
    expect(breakdown).toContain(`admin.dashboard.withdrawalFee $${fee.toFixed(2)}`)
    expect(breakdown).toContain(`admin.dashboard.dailyServerCost $${serverCost.toFixed(2)}`)
    wrapper.unmount()
  })

  it('hides profit until the backend provides lottery payouts', async () => {
    getSnapshotV2.mockResolvedValueOnce({
      stats: { ...createDashboardStats(), today_admin_actual_cost: 20, today_withdrawal_fee: 5.32584, today_profit: 88.55416 },
      trend: [], models: []
    })
    const wrapper = mount(DashboardView, {
      global: { stubs: {
        AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true,
        Icon: true, DateRangePicker: true, Select: true, ModelDistributionChart: true, TokenUsageTrend: true
      } }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="today-profit"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('hides profit until the backend provides withdrawal fees', async () => {
    getSnapshotV2.mockResolvedValueOnce({
      stats: { ...createDashboardStats(), today_admin_actual_cost: 20, today_lottery_cost: 5, today_profit: 88.88 },
      trend: [], models: []
    })
    const wrapper = mount(DashboardView, {
      global: { stubs: {
        AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true,
        Icon: true, DateRangePicker: true, Select: true, ModelDistributionChart: true, TokenUsageTrend: true
      } }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="today-profit"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('refreshes profit immediately after the monthly server expense is saved', async () => {
    getSnapshotV2.mockResolvedValueOnce({
      stats: { ...createDashboardStats(), today_admin_actual_cost: 3.61, today_lottery_cost: 0, today_withdrawal_fee: 4.46976, today_profit: 107.60024 },
      trend: [], models: []
    })
    const wrapper = mount(DashboardView, {
      global: { stubs: {
        AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true,
        Icon: true, DateRangePicker: true, Select: true, ModelDistributionChart: true, TokenUsageTrend: true
      } }
    })
    await flushPromises()
    getSnapshotV2.mockResolvedValueOnce({
      stats: { ...createDashboardStats(), today_admin_actual_cost: 3.61, today_lottery_cost: 0, today_withdrawal_fee: 4.46976, today_server_cost_usd: 20, today_profit: 87.60024 },
      trend: [], models: []
    })
    await wrapper.get('[data-testid="monthly-server-cost"]').setValue('600')
    await wrapper.get('[data-testid="server-cost-form"]').trigger('submit')
    await flushPromises()
    expect(updateDashboardProfitSettings).toHaveBeenCalledWith(600)
    expect(getSnapshotV2).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="today-profit"]').text()).toBe('$87.60')
    expect(wrapper.get('[data-testid="today-profit-breakdown"]').text()).toContain('admin.dashboard.dailyServerCost $20.00')
    wrapper.unmount()
  })

  it('hides profit until the backend provides daily server cost', async () => {
    getSnapshotV2.mockResolvedValueOnce({
      stats: { ...createDashboardStats(), today_server_cost_usd: undefined, today_lottery_cost: 0, today_withdrawal_fee: 0, today_profit: 0 },
      trend: [], models: []
    })
    const wrapper = mount(DashboardView, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true, Icon: true, DateRangePicker: true, Select: true, ModelDistributionChart: true, TokenUsageTrend: true } }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="today-profit"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('switches metrics and ignores a stale tokens response', async () => {
    let resolveTokens!: (value: any) => void
    getUserUsageTrend.mockImplementationOnce(() => new Promise(resolve => { resolveTokens = resolve }))
    getUserUsageTrend.mockResolvedValueOnce({ trend: [{ date: '2026-01-01', user_id: 2, username: 'spender', email: '', requests: 1, tokens: 10, cost: 5, actual_cost: 5 }] })
    const wrapper = mount(DashboardView, {
      global: { stubs: {
        AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true,
        Icon: true, DateRangePicker: true, Select: true, ModelDistributionChart: true,
        TokenUsageTrend: true
      } }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="total-duration-circuit-limit"]').exists()).toBe(true)
    expect(getUserUsageTrend).toHaveBeenCalledWith(expect.objectContaining({ metric: 'tokens', limit: 12 }))
    await wrapper.findAll('button').find(button => button.text() === 'admin.dashboard.actualSpending')!.trigger('click')
    expect(getUserUsageTrend).toHaveBeenCalledWith(expect.objectContaining({ metric: 'actual_cost', limit: 12 }))
    await flushPromises()
    const chart = wrapper.findComponent({ name: 'Line' })
    expect(chart.props('data').datasets[0].data).toEqual([5])
    expect(chart.props('options').scales.y.ticks.callback(5)).toBe('$5.00')
    resolveTokens({ trend: [{ date: '2026-01-01', user_id: 1, username: 'tokens', email: '', requests: 1, tokens: 100, cost: 1, actual_cost: 1 }] })
    await flushPromises()
    expect(chart.props('data').datasets[0].data).toEqual([5])
  })

  it('switches the displayed fast-pool order only after saving and keeps slow accounts last', async () => {
    getFirstTokenLatencies.mockResolvedValueOnce({ items: [
      createFirstTokenMetric({ account_id: 1, account_name: 'cheap-fast', scheduling_rate_multiplier: 0.05, cache_rate: 0.5 }),
      createFirstTokenMetric({ account_id: 2, account_name: 'cache-fast', scheduling_rate_multiplier: 0.5, cache_rate: 0.95 }),
      createFirstTokenMetric({ account_id: 4, account_name: 'slower-cache-fast', scheduling_rate_multiplier: 0.8, cache_rate: 0.99, predicted_ms: 9000, normal_total_ms: 9000 }),
      createFirstTokenMetric({ account_id: 3, account_name: 'slow-high-cache', scheduling_rate_multiplier: 0.01, cache_rate: 0.99, is_fast_pool: false })
    ], total: 4 })
    const wrapper = mount(DashboardView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true, Icon: true,
      DateRangePicker: true, Select: true, ModelDistributionChart: true, TokenUsageTrend: true, Line: true
    } } })
    await flushPromises()
    const rows = () => wrapper.findAll('[data-testid="first-token-latency-row"]').map(row => row.text())
    expect(rows()[0]).toContain('slower-cache-fast')
    expect(rows()[3]).toContain('slow-high-cache')
    await wrapper.get('[data-testid="fast-pool-priority-mode"]').setValue('latency')
    await wrapper.get('[data-testid="fast-pool-priority-control"] form').trigger('submit')
    await flushPromises()
    expect(updateFastPoolPrioritySettings).toHaveBeenCalledWith('latency', null)
    expect(rows()[0]).toContain('cheap-fast')
    await wrapper.get('[data-testid="fast-pool-priority-mode"]').setValue('rate')
    expect(rows()[0]).toContain('cheap-fast')
    await wrapper.get('[data-testid="fast-pool-priority-control"] form').trigger('submit')
    await flushPromises()
    expect(updateFastPoolPrioritySettings).toHaveBeenCalledWith('rate', null)
    expect(rows()[0]).toContain('cheap-fast')
    expect(rows()[3]).toContain('slow-high-cache')
    wrapper.unmount()
  })

  it.each([
    { mode: 'rate', threshold: 80, expected: ['exact', 'higher', 'lower', 'unknown', 'slow'] },
    { mode: 'latency', threshold: 80, expected: ['higher', 'exact', 'unknown', 'lower', 'slow'] },
    { mode: 'rate', threshold: 90, expected: ['higher', 'lower', 'exact', 'unknown', 'slow'] },
    { mode: 'latency', threshold: null, expected: ['unknown', 'lower', 'higher', 'exact', 'slow'] }
  ])('uses the saved $mode percentage tier ($threshold) and restores normal order when cleared', async ({ mode, threshold, expected }) => {
    getFastPoolPrioritySettings.mockResolvedValueOnce({ mode, cache_rate_threshold_percent: threshold })
    getFirstTokenLatencies.mockResolvedValueOnce({ items: [
      createFirstTokenMetric({ account_id: 1, account_name: 'lower', scheduling_rate_multiplier: 0.05, cache_rate: 0.79999, predicted_ms: 1000, normal_total_ms: 1000 }),
      createFirstTokenMetric({ account_id: 2, account_name: 'exact', scheduling_rate_multiplier: 0.1, cache_rate: 0.8, predicted_ms: 9000, normal_total_ms: 9000 }),
      createFirstTokenMetric({ account_id: 3, account_name: 'higher', scheduling_rate_multiplier: 0.5, cache_rate: 0.95, predicted_ms: 6000, normal_total_ms: 6000 }),
      createFirstTokenMetric({ account_id: 4, account_name: 'unknown', scheduling_rate_multiplier: 0.6, cache_rate: 1, cache_rate_denominator: 0, predicted_ms: 500, normal_total_ms: 500 }),
      createFirstTokenMetric({ account_id: 5, account_name: 'slow', scheduling_rate_multiplier: 0.01, cache_rate: 1, is_fast_pool: false })
    ], total: 5 })
    const wrapper = mount(DashboardView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true, Icon: true,
      DateRangePicker: true, Select: true, ModelDistributionChart: true, TokenUsageTrend: true, Line: true
    } } })
    await flushPromises()
    const rows = () => wrapper.findAll('[data-testid="first-token-latency-row"]').map(row => row.text())
    expected.forEach((name, index) => expect(rows()[index]).toContain(name))
    if (threshold !== null) {
      await wrapper.get('[data-testid="fast-pool-cache-rate-threshold"]').setValue('')
      expected.forEach((name, index) => expect(rows()[index]).toContain(name))
      await wrapper.get('[data-testid="fast-pool-priority-control"] form').trigger('submit')
      await flushPromises()
      expect(updateFastPoolPrioritySettings).toHaveBeenCalledWith(mode, null)
      expect(rows()[0]).toContain(mode === 'rate' ? 'lower' : 'unknown')
      expect(rows()[4]).toContain('slow')
    }
    wrapper.unmount()
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
          window_hours: 6,
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
    expect(cacheRates.filter((item) => item.text() === '100.0%')).toHaveLength(2)
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

  it('shows shared account isolation separately for each group', async () => {
    getFirstTokenLatencies.mockResolvedValueOnce({ items: [{
      account_id: 12463, account_name: 'shared-degraded', predicted_ms: 0,
      has_prediction: false, is_fast_pool: false, circuit_broken: true,
      scheduling_rate_multiplier: 0.08, sample_count: 0, window_hours: 6,
      updated_at: '2026-10-05T00:00:00Z', slow_streak: 0, recovery_fast_streak: 0,
      probe_interval_seconds: 60, cache_rate: null, cache_read_tokens: 0, cache_rate_denominator: 0,
      groups: [
        { group_id: 111, group_name: '010不降智', intelligence_exempt: true },
        { group_id: 5, group_name: '013不降智', intelligence_blocked: true }
      ]
    }], total: 1 })
    const wrapper = mount(DashboardView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true,
      Icon: true, DateRangePicker: true, Select: true, ModelDistributionChart: true,
      TokenUsageTrend: true, Line: true
    } } })
    await flushPromises()
    const rows = wrapper.findAll('[data-testid="first-token-latency-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('admin.dashboard.firstTokenIntelligenceExempt')
    expect(rows[1].text()).toContain('admin.dashboard.firstTokenIntelligenceBlocked')
    expect(rows[0].get('button').attributes('disabled')).toBeUndefined()
    expect(rows[1].get('button').attributes('disabled')).toBeDefined()
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
