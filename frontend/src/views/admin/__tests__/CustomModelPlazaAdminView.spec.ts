import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CustomModelPlazaAdminView from '../CustomModelPlazaAdminView.vue'
import messages from '@/i18n/locales/zh/customModelPlaza'
import {
  deleteCustomModelPlazaModel, getCustomModelPlazaAdmin, getCustomModelPlazaDefaults,
  saveCustomModelPlazaConfig, type CustomPlazaAdminModel,
} from '@/api/customModelPlaza'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
vi.mock('@/api/customModelPlaza', async importOriginal => ({
  ...await importOriginal<typeof import('@/api/customModelPlaza')>(),
  getCustomModelPlazaAdmin: vi.fn(), getCustomModelPlazaDefaults: vi.fn(),
  saveCustomModelPlazaConfig: vi.fn(), deleteCustomModelPlazaModel: vi.fn(),
}))

const defaults = [
  { min_tokens: 0, max_tokens: 272000, input: 8, output: 40, cache_read: 0.4 },
  { min_tokens: 272000, max_tokens: 0, input: 16, output: 60, cache_read: 0.8 },
]
const seed: CustomPlazaAdminModel = {
  id: 'gpt-6.2-astra', family: 'gpt', hidden: false, gpt_cache_free: true, context_limits: null,
  official_tiers: defaults, default_tiers: defaults, overrides: [],
}
const base = { min_tokens: 0, max_tokens: 0 }
const newModel: CustomPlazaAdminModel = {
  ...seed, id: 'vendor/new-model', family: 'Lab Models', gpt_cache_free: false, official_tiers: [base], default_tiers: [base],
}
const stubs = { RouterLink: RouterLinkStub, teleport: true }
const buttons = (wrapper: ReturnType<typeof mount>, title: string) =>
  wrapper.findAll('button').find(button => button.text() === title)!

afterEach(() => vi.resetAllMocks())

describe('model plaza admin', () => {
  it('uses the public and admin display names without 定制', () => {
    expect(messages.title).toBe('模型广场')
    expect(messages.adminTitle).toBe('模型广场定价')
    expect(messages.adminNote).not.toContain('定制')
  })

  it('searches, edits an existing model and preserves zero without exposing GPT cache writes', async () => {
    vi.mocked(getCustomModelPlazaAdmin).mockResolvedValue([seed, newModel])
    const override = { min_tokens: 272000, max_tokens: 0, cache_read: 0 }
    vi.mocked(saveCustomModelPlazaConfig).mockResolvedValue({ ...seed, overrides: [override] })
    const wrapper = mount(CustomModelPlazaAdminView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('input[type=search]').setValue('Gpt-6.2')
    expect(wrapper.text()).not.toContain(newModel.id)
    await wrapper.get(`[aria-label="customModelPlaza.edit: ${seed.id}"]`).trigger('click')
    const tiers = wrapper.findAll('fieldset').filter(el => el.find('legend').exists()).slice(1)
    expect(tiers.map(tier => tier.findAll('input').length)).toEqual([3, 3])
    expect(tiers[1].findAll('input')[0].attributes('placeholder')).toBe('$16.00')
    await tiers[1].findAll('input')[2].setValue('0')
    await wrapper.get('#plaza-model-form').trigger('submit')
    await flushPromises()
    expect(saveCustomModelPlazaConfig).toHaveBeenCalledWith({ id: seed.id, family: seed.family, hidden: false, context_limits: null, tiers: [
      { min_tokens: 0, max_tokens: 272000 }, override,
    ] }, false)
    expect(wrapper.get('[role="status"]').text()).toBe('customModelPlaza.saved')
    wrapper.unmount()
  })

  it('creates an arbitrary model and family, configures tiers, and confirms deletion', async () => {
    vi.mocked(getCustomModelPlazaAdmin).mockResolvedValue([seed])
    vi.mocked(getCustomModelPlazaDefaults).mockResolvedValue(newModel)
    vi.mocked(saveCustomModelPlazaConfig).mockResolvedValue(newModel)
    vi.mocked(deleteCustomModelPlazaModel).mockResolvedValue()
    const wrapper = mount(CustomModelPlazaAdminView, { global: { stubs } })
    await flushPromises()
    await buttons(wrapper, 'customModelPlaza.create').trigger('click')
    const form = wrapper.get('#plaza-model-form')
    await form.get('input[maxlength="200"]').setValue(newModel.id)
    await form.get('input[list]').setValue(newModel.family)
    await flushPromises()
    expect(getCustomModelPlazaDefaults).toHaveBeenCalledWith(newModel.id, newModel.family)
    await form.get('select').setValue('manual')
    await buttons(wrapper, 'customModelPlaza.addLimit').trigger('click')
    await form.get('input[step="1"]').setValue('42000')
    await form.findAll('input[step="any"]')[0].setValue('3.75')
    await form.trigger('submit')
    await flushPromises()
    expect(saveCustomModelPlazaConfig).toHaveBeenCalledWith({ id: newModel.id, family: newModel.family, hidden: false, context_limits: [42000], tiers: [
      { min_tokens: 0, max_tokens: 42000, input: 3.75 }, { min_tokens: 42000, max_tokens: 0 },
    ] }, true)
    await wrapper.get(`[aria-label="customModelPlaza.delete: ${newModel.id}"]`).trigger('click')
    expect(deleteCustomModelPlazaModel).not.toHaveBeenCalled()
    await buttons(wrapper, 'common.delete').trigger('click')
    await flushPromises()
    expect(deleteCustomModelPlazaModel).toHaveBeenCalledWith(newModel.id)
    expect(wrapper.text()).not.toContain(newModel.id)
    wrapper.unmount()
  })
})
