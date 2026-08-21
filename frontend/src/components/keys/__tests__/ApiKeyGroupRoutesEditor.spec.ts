import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { Group } from '@/types'
import ApiKeyGroupRoutesEditor from '../ApiKeyGroupRoutesEditor.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: '<button type="button"><slot name="selected" :option="null" /></button>'
}

const groups = [
  { id: 11, name: 'Primary OpenAI', platform: 'openai', rate_multiplier: 0.15 },
  { id: 12, name: 'Backup OpenAI', platform: 'openai', rate_multiplier: 0.2 },
  { id: 13, name: 'Second Backup OpenAI', platform: 'openai', rate_multiplier: 0.25 },
  { id: 21, name: 'Gemini', platform: 'gemini', rate_multiplier: 0.1 }
] as Group[]

const mountEditor = () =>
  mount(ApiKeyGroupRoutesEditor, {
    props: {
      groupId: 11,
      routes: [
        { group_id: 11, max_rate_multiplier: 0.2 },
        { group_id: 12, max_rate_multiplier: null }
      ],
      groups,
      userGroupRates: {}
    },
    global: {
      stubs: {
        Select: SelectStub,
        GroupBadge: true,
        GroupOptionItem: true,
        Icon: true
      }
    }
  })

describe('ApiKeyGroupRoutesEditor', () => {
  it('explains the cap, normalizes an empty cap, and adds a same-platform backup', async () => {
    const wrapper = mountEditor()

    expect(wrapper.text()).toContain('keys.maxAcceptedMultiplier')
    expect(wrapper.text()).toContain('keys.maxMultiplierHint')

    await wrapper.get('[data-testid="group-route-cap-0"]').setValue('')
    expect(wrapper.emitted('update:routes')?.at(-1)?.[0]).toEqual([
      { group_id: 11, max_rate_multiplier: null },
      { group_id: 12, max_rate_multiplier: null }
    ])

    await wrapper.get('[data-testid="add-backup-group"]').trigger('click')
    expect(wrapper.emitted('update:routes')?.at(-1)?.[0]).toEqual([
      { group_id: 11, max_rate_multiplier: 0.2 },
      { group_id: 12, max_rate_multiplier: null },
      { group_id: 13, max_rate_multiplier: null }
    ])
  })

  it('accepts arbitrary positive multiplier caps without a browser step mismatch', async () => {
    const wrapper = mountEditor()
    const cap = wrapper.get('[data-testid="group-route-cap-0"]')
    const input = cap.element as HTMLInputElement

    await cap.setValue('0.09')
    expect(input.validity.valid).toBe(true)
    expect(input.validity.stepMismatch).toBe(false)
    expect(wrapper.emitted('update:routes')?.at(-1)?.[0]).toEqual([
      { group_id: 11, max_rate_multiplier: 0.09 },
      { group_id: 12, max_rate_multiplier: null }
    ])

    await cap.setValue('0')
    expect(input.validity.rangeUnderflow).toBe(true)

    await cap.setValue('-0.01')
    expect(input.validity.rangeUnderflow).toBe(true)
  })

  it('drops incompatible backups when the primary platform changes', async () => {
    const wrapper = mountEditor()
    const primarySelect = wrapper.findAllComponents({ name: 'Select' })[0]

    await primarySelect.vm.$emit('update:modelValue', 21)

    expect(wrapper.emitted('update:groupId')?.at(-1)?.[0]).toBe(21)
    expect(wrapper.emitted('update:routes')?.at(-1)?.[0]).toEqual([
      { group_id: 21, max_rate_multiplier: 0.2 }
    ])
  })

  it('lets an existing backup become primary and removes the duplicate backup route', async () => {
    const wrapper = mountEditor()
    const primarySelect = wrapper.findAllComponents({ name: 'Select' })[0]

    expect(primarySelect.props('options')).toEqual(expect.arrayContaining([
      expect.objectContaining({ value: 12 })
    ]))

    await primarySelect.vm.$emit('update:modelValue', 12)

    expect(wrapper.emitted('update:groupId')?.at(-1)?.[0]).toBe(12)
    expect(wrapper.emitted('update:routes')?.at(-1)?.[0]).toEqual([
      { group_id: 12, max_rate_multiplier: 0.2 }
    ])
  })
})
