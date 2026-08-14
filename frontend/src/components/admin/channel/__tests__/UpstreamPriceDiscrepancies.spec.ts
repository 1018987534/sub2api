import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import UpstreamPriceDiscrepancies from '../UpstreamPriceDiscrepancies.vue'
import type { UpstreamBillingPriceDiscrepancy } from '@/api/admin/accounts'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const discrepancy: UpstreamBillingPriceDiscrepancy = {
  account_id: 12017,
  account_name: 'plus-shayu-plus',
  model: 'gpt-5.6-luna',
  current_source: 'local',
  current_price: {
    input_price_per_token: 0.2e-6,
    output_price_per_token: 1.2e-6
  },
  inferred_price: {
    input_price_per_token: 1e-6,
    output_price_per_token: 6e-6,
    sample_count: 3,
    input_sample_count: 3,
    observed_at: '2026-08-15T03:00:00Z'
  }
}

function mountComponent(props: {
  items?: UpstreamBillingPriceDiscrepancy[]
  loading?: boolean
  syncingKey?: string | null
} = {}) {
  return mount(UpstreamPriceDiscrepancies, {
    props: {
      items: props.items ?? [discrepancy],
      loading: props.loading ?? false,
      syncingKey: props.syncingKey ?? null
    },
    global: {
      stubs: {
        Icon: { props: ['name'], template: '<i :data-icon="name" />' }
      }
    }
  })
}

describe('UpstreamPriceDiscrepancies', () => {
  it('shows current and inferred per-million prices with account evidence', () => {
    const wrapper = mountComponent()

    expect(wrapper.text()).toContain('plus-shayu-plus')
    expect(wrapper.text()).toContain('#12017')
    expect(wrapper.text()).toContain('gpt-5.6-luna')
    expect(wrapper.text()).toContain('$0.2/M')
    expect(wrapper.text()).toContain('$1/M')
    expect(wrapper.text()).not.toContain('$1.2/M')
    expect(wrapper.findAll('[data-testid="upstream-price-discrepancy-row"]')).toHaveLength(1)
  })

  it('emits a model-scoped confirmation and disables it while syncing', async () => {
    const wrapper = mountComponent({ syncingKey: '12017:gpt-5.6-luna' })
    const button = wrapper.get('[data-testid="confirm-upstream-price"]')

    expect(button.attributes('disabled')).toBeDefined()
    await wrapper.setProps({ syncingKey: null })
    await button.trigger('click')
    expect(wrapper.emitted('confirm')).toEqual([[discrepancy]])
  })

  it('does not occupy the page when there are no discrepancies', () => {
    const wrapper = mountComponent({ items: [] })
    expect(wrapper.find('[data-testid="upstream-price-discrepancies"]').exists()).toBe(false)
  })
})
