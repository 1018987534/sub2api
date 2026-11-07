import { apiClient } from './client'
import type { BasePaginationResponse } from '@/types'

export interface CreateFeedbackInput {
  category: 'problem' | 'suggestion' | 'other'
  subject: string
  content: string
}

export interface FeedbackSubmission extends CreateFeedbackInput {
  id: number
  user_id: number
  created_at: string
  user_email?: string
  user_username?: string
}

export default {
  create: async (input: CreateFeedbackInput, idempotencyKey: string) =>
    (await apiClient.post<FeedbackSubmission>('/feedback', input, { headers: { 'Idempotency-Key': idempotencyKey } })).data,
  adminList: async (page = 1, pageSize = 20) =>
    (await apiClient.get<BasePaginationResponse<FeedbackSubmission>>('/admin/feedback', { params: { page, page_size: pageSize } })).data,
}
