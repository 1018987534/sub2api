import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))

vi.mock('@/api/client', () => ({
  apiClient: { post },
}))

import { createInviteMatch } from '@/api/admin/affiliates'

describe('admin affiliate API', () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({
      data: { inviter_id: 10, invitee_id: 21, bind_source: 'admin' },
    })
  })

  it('creates a manual inviter and invitee match', async () => {
    await expect(createInviteMatch({ inviter_id: 10, invitee_id: 21 })).resolves.toEqual({
      inviter_id: 10,
      invitee_id: 21,
      bind_source: 'admin',
    })
    expect(post).toHaveBeenCalledWith('/admin/affiliates/invites', {
      inviter_id: 10,
      invitee_id: 21,
    })
  })
})
