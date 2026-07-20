import type { User } from '@/types'

export const IMAGE_STUDIO_PREVIEW_EMAIL = 'menghuandeyao@163.com'

export function canAccessImageStudioPreview(
  user: Pick<User, 'role' | 'email'> | null | undefined,
): boolean {
  return user?.role === 'admin' && user.email.trim().toLowerCase() === IMAGE_STUDIO_PREVIEW_EMAIL
}
