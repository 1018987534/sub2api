import { apiClient } from './client'

export interface SupportConversation {
  id: number
  user_id: number
  user_email?: string
  user_username?: string
  unread_by_user: number
  unread_by_admin: number
  manually_unread_by_admin?: boolean
  last_message_at?: string
  updated_at: string
}

export interface SupportMessage {
  id: number
  conversation_id: number
  sender_type: 'user' | 'admin'
  sender_id: number
  content: string
  kind: string
  created_at: string
  recalled_at?: string
}

interface Page<T> { items: T[]; total: number; page: number; page_size: number; pages: number }

const key = (prefix: string) => `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`.slice(0, 128)

export const supportChatAPI = {
  conversation: async () => (await apiClient.get<SupportConversation>('/chat/conversation')).data,
  messages: async (pageSize = 100) => (await apiClient.get<Page<SupportMessage>>('/chat/messages', { params: { page_size: pageSize } })).data,
  send: async (content: string) => { const idempotencyKey = key('user-chat'); return (await apiClient.post<SupportMessage>('/chat/messages', { content, kind: 'text', idempotency_key: idempotencyKey }, { headers: { 'Idempotency-Key': idempotencyKey } })).data },
  read: async () => { await apiClient.post('/chat/read') },
  unreadCount: async () => (await apiClient.get<{ unread_count: number }>('/chat/unread-count')).data.unread_count,
  adminConversations: async (params: { search?: string; unread_only?: boolean } = {}) => (await apiClient.get<Page<SupportConversation>>('/admin/chat/conversations', { params })).data,
  adminMessages: async (id: number) => (await apiClient.get<Page<SupportMessage>>(`/admin/chat/conversations/${id}/messages`)).data,
  adminSend: async (id: number, content: string) => (await apiClient.post<SupportMessage>(`/admin/chat/conversations/${id}/messages`, { content, kind: 'text', idempotency_key: key('admin-chat') })).data,
  adminRead: async (id: number) => { await apiClient.post(`/admin/chat/conversations/${id}/read`) },
  adminUnreadCount: async () => (await apiClient.get<{ unread_count: number }>('/admin/chat/unread-count')).data.unread_count,
}

export default supportChatAPI
