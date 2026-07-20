import { describe, expect, it } from 'vitest'
import {
  IMAGE_STUDIO_PREVIEW_EMAIL,
  canAccessImageStudioPreview,
} from '@/utils/imageStudioAccess'

describe('canAccessImageStudioPreview', () => {
  it('allows the designated administrator', () => {
    expect(canAccessImageStudioPreview({ role: 'admin', email: IMAGE_STUDIO_PREVIEW_EMAIL })).toBe(true)
  })

  it('normalizes surrounding whitespace and case', () => {
    expect(canAccessImageStudioPreview({ role: 'admin', email: '  MengHuanDeYao@163.COM ' })).toBe(true)
  })

  it('rejects other administrators', () => {
    expect(canAccessImageStudioPreview({ role: 'admin', email: 'other-admin@example.com' })).toBe(false)
  })

  it('rejects a non-admin account with the same email', () => {
    expect(canAccessImageStudioPreview({ role: 'user', email: IMAGE_STUDIO_PREVIEW_EMAIL })).toBe(false)
  })
})
