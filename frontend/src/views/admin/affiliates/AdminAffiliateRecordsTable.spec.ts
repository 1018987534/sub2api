import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AdminAffiliateRecordsTable from './AdminAffiliateRecordsTable.vue'

const {
  listInviteRecords,
  lookupUsers,
  createInviteMatch,
  showSuccess,
  showError,
} = vi.hoisted(() => ({
  listInviteRecords: vi.fn(),
  lookupUsers: vi.fn(),
  createInviteMatch: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/affiliates', () => {
  const api = {
    listInviteRecords,
    listRebateRecords: vi.fn(),
    listTransferRecords: vi.fn(),
    lookupUsers,
    createInviteMatch,
    getUserOverview: vi.fn(),
  }
  return { affiliatesAPI: api, default: api }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

function mountTable() {
  return mount(AdminAffiliateRecordsTable, {
    props: { type: 'invites' },
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: true,
        Pagination: true,
        BaseDialog: {
          props: ['show', 'title'],
          emits: ['close'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>',
        },
        Icon: true,
        OrderStatusBadge: true,
      },
    },
  })
}

describe('admin affiliate manual matching', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    for (const fn of [listInviteRecords, lookupUsers, createInviteMatch, showSuccess, showError]) {
      fn.mockReset()
    }
    listInviteRecords.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    lookupUsers.mockImplementation(async (query: string) => query === 'customer'
      ? [{ id: 21, email: 'customer@example.com', username: 'customer' }]
      : [{ id: 10, email: 'inviter@example.com', username: 'inviter' }])
    createInviteMatch.mockResolvedValue({ inviter_id: 10, invitee_id: 21, bind_source: 'admin' })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('selects two users and submits the manual match', async () => {
    const wrapper = mountTable()
    await flushPromises()

    await wrapper.get('[data-testid="manual-match-open"]').trigger('click')
    await wrapper.get('[data-testid="manual-match-invitee-search"]').setValue('customer')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    await wrapper.get('[data-testid="manual-match-invitee-user-21"]').trigger('click')

    await wrapper.get('[data-testid="manual-match-inviter-search"]').setValue('inviter')
    await vi.advanceTimersByTimeAsync(250)
    await flushPromises()
    await wrapper.get('[data-testid="manual-match-inviter-user-10"]').trigger('click')
    await wrapper.get('[data-testid="manual-match-submit"]').trigger('click')
    await flushPromises()

    expect(createInviteMatch).toHaveBeenCalledWith({ invitee_id: 21, inviter_id: 10 })
    expect(showSuccess).toHaveBeenCalledWith('admin.affiliates.manualMatch.success')
    expect(listInviteRecords).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})
