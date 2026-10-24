import { apiClient } from './client'

export interface QQActivityConfig {
  status: 'preparing' | 'active' | 'paused' | 'ended'
  initialized_at: string | null
  started_at: string | null
  ended_at: string | null
  newcomer_enabled: boolean
  invitation_enabled: boolean
  auto_process_join: boolean
  group_messages_enabled: boolean
}
export interface QQReward {
  id: number
  kind: 'newcomer' | 'invitation'
  amount: number
  status: 'pending' | 'paid'
  paid_at?: string
}
export interface QQActivitySummary {
  config: QQActivityConfig
  bound: boolean
  qq?: string
  account: string
  welcome_status: string
  welcome_text: string
  invitation_paid: number
  invitation_pending: number
  pending_inviter_binding: number
  rewards: QQReward[]
}
export interface QQClaim {
  account: string
  qq_email: string
  expires_at: string
  code?: string
  consumed: boolean
}
export interface QQMember {
  group_id: string
  qq: string
  in_group: boolean
  robot: boolean
  legacy: boolean
  match_state: string
  account: string
  level: number | null
  bound: boolean
}
export interface QQInvitation {
  invitee: string
  inviter: string
  group_id: string
  evidence: string
  status: string
  reason: string
}
export interface QQAdminReport {
  config: QQActivityConfig
  baseline_users: number
  members_count: number
  bindings_count: number
  newcomer_paid: number
  invitation_paid: number
  pending_amount: number
  unsent_messages: number
  members: QQMember[]
  invitations: QQInvitation[]
}
export const qqActivityAPI = {
  async current() { return (await apiClient.get<QQActivitySummary>('/qq-activity/current')).data },
  async claim(claim: string, issueCode = false) { return (await apiClient.post<QQClaim>('/qq-activity/claim', { claim }, { params: { issue_code: issueCode } })).data },
  async admin() { return (await apiClient.get<QQAdminReport>('/admin/qq-activity')).data },
  async control(data: Record<string, unknown>) { await apiClient.post('/admin/qq-activity/control', data) }
}
