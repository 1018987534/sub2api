import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { ChannelMonitor } from '@/api/admin/channelMonitor'
import MonitorOrderDialog from '../MonitorOrderDialog.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const monitor = (id: number, name: string): ChannelMonitor => ({
  id,
  name,
  provider: 'openai',
  api_mode: 'chat_completions',
  endpoint: 'https://api.example.com',
  api_key_masked: 'sk-t***',
  primary_model: 'gpt-5',
  extra_models: [],
  group_name: '',
  enabled: true,
  sort_order: id * 10,
  interval_seconds: 60,
  jitter_seconds: 0,
  last_checked_at: null,
  created_by: 1,
  created_at: '2026-08-24T00:00:00Z',
  updated_at: '2026-08-24T00:00:00Z',
  primary_status: 'operational',
  primary_latency_ms: 100,
  availability_7d: 100,
  extra_models_status: [],
  template_id: null,
  extra_headers: {},
  body_override_mode: 'off',
  body_override: null,
  check_mode: 'probe',
  account_id: null,
})

describe('MonitorOrderDialog', () => {
  it('moves monitors with icon controls and emits the complete order', async () => {
    const wrapper = mount(MonitorOrderDialog, {
      props: { show: true, items: [monitor(1, 'PLUS'), monitor(2, 'PRO'), monitor(3, 'KIRO')], loading: false, submitting: false },
      global: { stubs: { Teleport: true } },
    })

    await wrapper.findAll('[data-testid="monitor-order-up"]')[1].trigger('click')
    await wrapper.findAll('button').at(-1)!.trigger('click')

    expect(wrapper.emitted('save')).toEqual([[[2, 1, 3]]])
  })

  it('disables controls at list boundaries', () => {
    const wrapper = mount(MonitorOrderDialog, {
      props: { show: true, items: [monitor(1, 'PLUS'), monitor(2, 'PRO')], loading: false, submitting: false },
      global: { stubs: { Teleport: true } },
    })

    expect(wrapper.findAll('[data-testid="monitor-order-up"]')[0].attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('[data-testid="monitor-order-down"]')[1].attributes('disabled')).toBeDefined()
  })
})
