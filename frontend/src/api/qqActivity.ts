import { apiClient } from './client'

export interface QQBindingDisplay {
  group_number: string
  activity_title: string
  activity_text: string
  binding_intro: string
  binding_note: string
}
export interface QQActivityConfig {
  status: 'preparing' | 'active' | 'paused' | 'ended'
  initialized_at: string | null
  started_at: string | null
  ended_at: string | null
  newcomer_enabled: boolean
  invitation_enabled: boolean
  auto_process_join: boolean
  group_messages_enabled: boolean
  binding_display: QQBindingDisplay
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
export interface QQBindingStatus {
  bound: boolean
  qq_email?: string
  config?: QQActivityConfig
}
export interface QQEmailChallenge {
  challenge: string
  qq_email: string
  expires_at: string
  resend_after: number
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
  async binding() { return (await apiClient.get<QQBindingStatus>('/qq-activity/binding')).data },
  async unbind() { return (await apiClient.post<QQBindingStatus>('/qq-activity/binding/unbind')).data },
  async sendBindingCode(email: string) { return (await apiClient.post<QQEmailChallenge>('/qq-activity/binding/send-code', { email })).data },
  async verifyBinding(email: string, challenge: string, code: string) { return (await apiClient.post<QQBindingStatus>('/qq-activity/binding/verify', { email, challenge, code })).data },
  async current() { return (await apiClient.get<QQActivitySummary>('/qq-activity/current')).data },
  async claim(claim: string, issueCode = false) { return (await apiClient.post<QQClaim>('/qq-activity/claim', { claim }, { params: { issue_code: issueCode } })).data },
  async admin() { return (await apiClient.get<QQAdminReport>('/admin/qq-activity')).data },
  async control(data: Record<string, unknown>) { await apiClient.post('/admin/qq-activity/control', data) }
}
