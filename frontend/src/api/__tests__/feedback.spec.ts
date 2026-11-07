import { beforeEach, describe, expect, it, vi } from 'vitest'
const { post, get } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post, get } }))
import feedbackAPI from '@/api/feedback'

describe('independent feedback API', () => {
  beforeEach(() => { post.mockReset(); get.mockReset() })

  it('submits structured feedback with an idempotency key', async () => {
    const input = { category: 'problem' as const, subject: '请求出错', content: '使用时提示超时' }
    post.mockResolvedValue({ data: { id: 41, ...input } })
    expect(await feedbackAPI.create(input, 'feedback-fixture')).toEqual({ id: 41, ...input })
    expect(post).toHaveBeenCalledWith('/feedback', input, { headers: { 'Idempotency-Key': 'feedback-fixture' } })
  })

  it('only exposes a paginated read operation for administrators', async () => {
    const result = { items: [], total: 0, page: 2, page_size: 20, pages: 0 }
    get.mockResolvedValue({ data: result })
    expect(await feedbackAPI.adminList(2, 20)).toEqual(result)
    expect(get).toHaveBeenCalledWith('/admin/feedback', { params: { page: 2, page_size: 20 } })
    expect(Object.keys(feedbackAPI).sort()).toEqual(['adminList', 'create'])
    expect(post).not.toHaveBeenCalled()
  })
})
