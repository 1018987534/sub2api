import { beforeEach, describe, expect, it, vi } from 'vitest'
const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post } }))
import lotteryAPI from '@/api/lottery'

describe('lottery login captcha API', () => {
  beforeEach(() => post.mockReset().mockResolvedValue({ data: { round_id: 3 } }))

  it.each([
    { turnstile_token: 'fixture-turnstile-or-aliyun-token' },
    { tencent_captcha_ticket: 'fixture-ticket', tencent_captcha_randstr: 'fixture-randstr' },
    {},
  ])('submits login proof without slider coordinates: %j', async proof => {
    expect(await lotteryAPI.join(proof)).toEqual({ round_id: 3 })
    expect(post).toHaveBeenCalledWith('/lottery/join', proof)
  })
})
