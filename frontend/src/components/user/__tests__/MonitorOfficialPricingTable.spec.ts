import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import MonitorOfficialPricingTable from '../monitor/MonitorOfficialPricingTable.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

describe('MonitorOfficialPricingTable', () => {
  it('shows only official token price columns', () => {
    const wrapper = mount(MonitorOfficialPricingTable, {
      props: {
        models: [{
          name: 'gpt-5.6-sol',
          platform: 'openai',
          official_pricing: {
            input_price: 0.000001,
            output_price: 0.000002,
            cache_write_price: 0.00000125,
            cache_write_1h_price: 0.000002,
            cache_read_price: 0.0000001,
          },
        }],
      },
    })
    const text = wrapper.text()
    expect(text).toContain('gpt-5.6-sol')
    expect(text).toContain('channelStatus.models.input')
    expect(text).toContain('channelStatus.models.output')
    expect(text).toContain('channelStatus.models.cacheWrite')
    expect(text).toContain('channelStatus.models.cacheRead')
    expect(text).not.toContain('modelPlaza.table.paidPrice')
    expect(text).not.toContain('modelPlaza.table.rate')
  })
})
