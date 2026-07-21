import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn(),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    post,
  },
}))

import {
  batchUpdateLimits,
  bindUserAuthIdentity,
  sendReengagementEmail,
  type AdminBindAuthIdentityRequest,
  type AdminBoundAuthIdentity,
  type BatchUpdateUserLimitsRequest,
  type BatchUpdateUserLimitsResponse,
  type SendUserReengagementEmailRequest,
  type SendUserReengagementEmailResponse,
} from '@/api/admin/users'

type Assert<T extends true> = T
type IsExact<T, U> = (
  (<G>() => G extends T ? 1 : 2) extends (<G>() => G extends U ? 1 : 2)
    ? ((<G>() => G extends U ? 1 : 2) extends (<G>() => G extends T ? 1 : 2) ? true : false)
    : false
)

type ExpectedAdminBindAuthIdentityRequest = {
  provider_type: string
  provider_key: string
  provider_subject: string
  issuer?: string
  metadata?: Record<string, unknown>
  channel?: {
    channel: string
    channel_app_id: string
    channel_subject: string
    metadata?: Record<string, unknown>
  }
}

type ExpectedAdminBoundAuthIdentity = {
  user_id: number
  provider_type: string
  provider_key: string
  provider_subject: string
  verified_at?: string | null
  issuer?: string | null
  metadata: Record<string, unknown> | null
  created_at: string
  updated_at: string
  channel?: {
    channel: string
    channel_app_id: string
    channel_subject: string
    metadata: Record<string, unknown> | null
    created_at: string
    updated_at: string
  } | null
}

const requestContractExact: Assert<
  IsExact<AdminBindAuthIdentityRequest, ExpectedAdminBindAuthIdentityRequest>
> = true
const responseContractExact: Assert<
  IsExact<AdminBoundAuthIdentity, ExpectedAdminBoundAuthIdentity>
> = true
const batchRequestContractExact: Assert<
  IsExact<
    BatchUpdateUserLimitsRequest,
    {
      user_ids: number[]
      all?: boolean
      concurrency?: number
      rpm_limit?: number
    }
  >
> = true
const batchResponseContractExact: Assert<
  IsExact<BatchUpdateUserLimitsResponse, { affected: number }>
> = true
const reengagementRequestContractExact: Assert<
  IsExact<
    SendUserReengagementEmailRequest,
    {
      status?: 'active' | 'disabled'
      role?: 'admin' | 'user'
      search?: string
      group_name?: string
      api_key_group_id?: number
      attributes?: Record<number, string>
      has_recharged?: boolean
      user_ids?: number[]
      send_all?: boolean
      inactive_days?: number
      never_used?: boolean
    }
  >
> = true
const reengagementResponseContractExact: Assert<
  IsExact<
    SendUserReengagementEmailResponse,
    {
      queued: boolean
      selected: number
      matched: number
      sent: number
      skipped: number
      failed: number
    }
  >
> = true

describe('admin users api auth identity binding', () => {
  beforeEach(() => {
    post.mockReset()
  })

  it('posts the backend-compatible auth identity bind payload and returns the backend response shape', async () => {
    const payload: AdminBindAuthIdentityRequest = {
      provider_type: 'wechat',
      provider_key: 'wechat-main',
      provider_subject: 'union-123',
      metadata: { source: 'admin-repair' },
      channel: {
        channel: 'open',
        channel_app_id: 'wx-open',
        channel_subject: 'openid-123',
        metadata: { scene: 'migration' },
      },
    }

    const response: AdminBoundAuthIdentity = {
      user_id: 9,
      provider_type: 'wechat',
      provider_key: 'wechat-main',
      provider_subject: 'union-123',
      verified_at: '2026-04-22T00:00:00Z',
      issuer: null,
      metadata: { source: 'admin-repair' },
      created_at: '2026-04-22T00:00:00Z',
      updated_at: '2026-04-22T00:00:00Z',
      channel: {
        channel: 'open',
        channel_app_id: 'wx-open',
        channel_subject: 'openid-123',
        metadata: { scene: 'migration' },
        created_at: '2026-04-22T00:00:00Z',
        updated_at: '2026-04-22T00:00:00Z',
      },
    }
    post.mockResolvedValue({ data: response })

    const result = await bindUserAuthIdentity(9, payload)

    expect(post).toHaveBeenCalledWith('/admin/users/9/auth-identities', payload)
    expect(result).toEqual(response)
  })

  it('keeps bind auth identity request and response types aligned with the backend contract', () => {
    expect(requestContractExact).toBe(true)
    expect(responseContractExact).toBe(true)
  })

  it('posts batch limit updates once with only the supplied limit fields', async () => {
    const request: BatchUpdateUserLimitsRequest = {
      user_ids: [4, 7],
      all: false,
      rpm_limit: 0,
    }
    post.mockResolvedValue({ data: { affected: 2 } satisfies BatchUpdateUserLimitsResponse })

    const result = await batchUpdateLimits(request)

    expect(post).toHaveBeenCalledWith('/admin/users/batch-limits', request)
    expect(result).toEqual({ affected: 2 })
    expect(batchRequestContractExact).toBe(true)
    expect(batchResponseContractExact).toBe(true)
  })

  it('posts selected users and the inactivity window for reengagement', async () => {
    const request: SendUserReengagementEmailRequest = {
      user_ids: [4, 7],
      inactive_days: 14,
    }
    const response: SendUserReengagementEmailResponse = {
      queued: false,
      selected: 2,
      matched: 1,
      sent: 1,
      skipped: 1,
      failed: 0,
    }
    post.mockResolvedValue({ data: response })

    const result = await sendReengagementEmail(request)

    expect(post).toHaveBeenCalledWith('/admin/users/send-reengagement-email', request)
    expect(result).toEqual(response)
    expect(reengagementRequestContractExact).toBe(true)
    expect(reengagementResponseContractExact).toBe(true)
  })

  it('posts every current audience filter for a background reengagement campaign', async () => {
    const request: SendUserReengagementEmailRequest = {
      send_all: true,
      inactive_days: 14,
      status: 'active',
      role: 'user',
      search: 'legacy',
      group_name: 'GPT',
      api_key_group_id: 5,
      attributes: { 3: 'enterprise' },
      has_recharged: false,
    }
    const response: SendUserReengagementEmailResponse = {
      queued: true,
      selected: 602,
      matched: 602,
      sent: 0,
      skipped: 0,
      failed: 0,
    }
    post.mockResolvedValue({ data: response })

    const result = await sendReengagementEmail(request)

    expect(post).toHaveBeenCalledWith('/admin/users/send-reengagement-email', request)
    expect(result).toEqual(response)
  })
})
