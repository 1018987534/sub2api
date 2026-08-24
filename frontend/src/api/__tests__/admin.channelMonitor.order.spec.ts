import { describe, expect, it, vi } from 'vitest'

const { put } = vi.hoisted(() => ({ put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { put } }))

import { updateSortOrder } from '@/api/admin/channelMonitor'

describe('admin channel monitor order API', () => {
  it('sends the complete ordered monitor ID list', async () => {
    put.mockResolvedValueOnce({ data: { message: 'ok' } })

    await updateSortOrder([9, 4, 7])

    expect(put).toHaveBeenCalledWith('/admin/channel-monitors/sort-order', { ordered_ids: [9, 4, 7] })
  })
})
