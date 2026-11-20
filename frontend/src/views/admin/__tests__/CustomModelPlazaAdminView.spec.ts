import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import CustomModelPlazaAdminView from '../CustomModelPlazaAdminView.vue'
import { getCustomModelPlazaAdmin, saveCustomModelPlazaPrices, type CustomPlazaAdminModel } from '@/api/customModelPlaza'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/api/customModelPlaza', async importOriginal => ({
  ...await importOriginal<typeof import('@/api/customModelPlaza')>(),
  getCustomModelPlazaAdmin: vi.fn(),
  saveCustomModelPlazaPrices: vi.fn(),
}))

describe('custom model plaza editor', () => {
  it('preserves empty fields, sends explicit zero to the selected tier, and resets the model', async () => {
    const model: CustomPlazaAdminModel = {
      id: 'gpt-6.2-astra', family: 'gpt', overrides: [],
      default_tiers: [
        { min_tokens: 0, max_tokens: 272000, input: 8, output: 40, cache_read: 0.4 },
        { min_tokens: 272000, max_tokens: 0, input: 16, output: 60, cache_read: 0.8 },
      ],
    }
    const override = { min_tokens: 272000, max_tokens: 0, cache_read: 0 }
    vi.mocked(getCustomModelPlazaAdmin).mockResolvedValue([model])
    vi.mocked(saveCustomModelPlazaPrices).mockResolvedValueOnce({ ...model, overrides: [override] }).mockResolvedValueOnce(model)
    const wrapper = mount(CustomModelPlazaAdminView, { global: { stubs: { RouterLink: RouterLinkStub } } })
    await flushPromises()
    const tiers = wrapper.findAll('fieldset')
    expect(tiers.map(tier => tier.findAll('input').length)).toEqual([3, 3])
    expect(tiers[1].findAll('input')[0].attributes('placeholder')).toBe('$16.00')
    await tiers[1].findAll('input')[2].setValue('0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveCustomModelPlazaPrices).toHaveBeenNthCalledWith(1, model.id, [
      { min_tokens: 0, max_tokens: 272000 }, override,
    ])
    expect(wrapper.get('[role="status"]').text()).toBe('customModelPlaza.saved')
    const reset = wrapper.findAll('button').find(button => button.text() === 'customModelPlaza.reset')!
    await reset.trigger('click')
    await flushPromises()
    expect(saveCustomModelPlazaPrices).toHaveBeenNthCalledWith(2, model.id, [])
    expect(wrapper.get('[role="status"]').text()).toBe('customModelPlaza.resetDone')
  })
})
