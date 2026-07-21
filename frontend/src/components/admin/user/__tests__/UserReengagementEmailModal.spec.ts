import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UserReengagementEmailModal from '../UserReengagementEmailModal.vue'

const { sendReengagementEmail, showSuccess, showError } = vi.hoisted(() => ({
  sendReengagementEmail: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      sendReengagementEmail,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const BaseDialogStub = {
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
}

describe('UserReengagementEmailModal', () => {
  beforeEach(() => {
    sendReengagementEmail.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    sendReengagementEmail.mockResolvedValue({
      selected: 2,
      matched: 2,
      sent: 2,
      skipped: 0,
      failed: 0,
    })
  })

  it('uses the current 14-day list filter and sends selected user IDs', async () => {
    const wrapper = mount(UserReengagementEmailModal, {
      props: {
        show: false,
        selectedIds: [4, 7],
        initialActivity: '14',
      },
      global: { stubs: { BaseDialog: BaseDialogStub } },
    })
    await wrapper.setProps({ show: true })

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(sendReengagementEmail).toHaveBeenCalledWith({
      user_ids: [4, 7],
      inactive_days: 14,
    })
    expect(showSuccess).toHaveBeenCalled()
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('sends the never-used audience without an inactivity-day value', async () => {
    const wrapper = mount(UserReengagementEmailModal, {
      props: {
        show: false,
        selectedIds: [9],
        initialActivity: 'never',
      },
      global: { stubs: { BaseDialog: BaseDialogStub } },
    })
    await wrapper.setProps({ show: true })

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(sendReengagementEmail).toHaveBeenCalledWith({
      user_ids: [9],
      never_used: true,
    })
  })
})
