import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import MonitorGroupMetrics from '../monitor/MonitorGroupMetrics.vue'
import type { UserMonitorGroupMetric } from '@/api/channelMonitor'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      const messages: Record<string, string> = {
        'channelStatus.groupMetrics.title': '监控分组近期表现',
        'channelStatus.groupMetrics.description': '近 24 小时真实请求汇总',
        'channelStatus.groupMetrics.count': `${params?.n ?? 0} 个分组`,
        'channelStatus.groupMetrics.group': '监控分组',
        'channelStatus.groupMetrics.firstToken': '首字 P50',
        'channelStatus.groupMetrics.cacheRate': '平均缓存率',
        'channelStatus.groupMetrics.empty': '暂无数据',
      }
      return messages[key] || key
    },
  }),
}))

const metric = (overrides: Partial<UserMonitorGroupMetric> = {}): UserMonitorGroupMetric => ({
  platform: 'openai',
  group_id: 10,
  group_name: '性价比分组',
  first_token_p50_ms: 1250,
  first_token_sample_count: 24,
  cache_rate: 0.625,
  ...overrides,
})

describe('MonitorGroupMetrics', () => {
  it('shows P50, cache rate, sample count, and empty values without overflow', () => {
    const wrapper = mount(MonitorGroupMetrics, {
      props: {
        rows: [
          metric(),
          metric({
            group_id: 11,
            group_name: '无样本',
            first_token_p50_ms: null,
            first_token_sample_count: 0,
            cache_rate: null,
          }),
        ],
        loading: false,
      },
    })

    const rows = wrapper.findAll('[data-testid="monitor-group-metric-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('1.25s')
    expect(rows[0].text()).toContain('62.5%')
    expect(rows[0].text()).toContain('24')
    expect(rows[1].text()).toContain('无样本')
    expect(rows[1].text()).toContain('-')
    expect(wrapper.findAll('[data-testid="monitor-group-metric-card"]')).toHaveLength(2)
    expect(wrapper.find('table').classes()).toContain('min-w-[560px]')
  })

  it('shows a compact loading state', () => {
    const wrapper = mount(MonitorGroupMetrics, {
      props: { rows: [], loading: true },
    })
    expect(wrapper.find('[data-testid="monitor-group-metrics"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('暂无数据')
  })
})
