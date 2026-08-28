import { apiClient } from './client'
import type { BasePaginationResponse } from '@/types'

export interface LotteryConfig {
  enabled: boolean
  participant_threshold: number
  prize_count: number
  prize_amount: number
  draw_mode: 'auto' | 'manual'
  next_round_mode: 'auto' | 'manual'
  require_recharge: boolean
  min_recharge_amount: number
  min_account_age_days: number
  recent_recharge_days: number
  updated_at?: string
}

export interface LotteryRound {
  id: number
  round_no: number
  status: 'open' | 'drawn' | 'cancelled' | string
  participant_threshold: number
  prize_count: number
  prize_amount: number
  draw_mode?: 'auto' | 'manual' | string
  next_round_mode?: 'auto' | 'manual' | string
  require_recharge: boolean
  min_recharge_amount: number
  min_account_age_days: number
  participant_count: number
  manual_progress_count?: number
  real_participant_count?: number
  winner_count: number
  unique_ip_count?: number
  started_at: string
  drawn_at?: string | null
  updated_at?: string
}

export interface LotteryWinner {
  id: number
  round_id: number
  round_no: number
  email: string
  prize_amount: number
  awarded_at: string
  participated_at: string
}

export interface LotteryEligibility {
  eligible: boolean
  reason?: string
  total_recharge: number
}

export interface LotteryCurrent {
  enabled: boolean
  current_round?: LotteryRound | null
  joined: boolean
  eligibility: LotteryEligibility
  recent_winners: LotteryWinner[]
  my_recent_winners: LotteryWinner[]
}

export interface LotteryDrawResult {
  round: LotteryRound
  winners: LotteryWinner[]
  next_round?: LotteryRound
}

export interface LotteryCaptchaProof {
  turnstile_token?: string
  tencent_captcha_ticket?: string
  tencent_captcha_randstr?: string
}

export const lotteryAPI = {
  async getCurrent() {
    const { data } = await apiClient.get<LotteryCurrent>('/lottery/current')
    return data
  },
  async join(proof: LotteryCaptchaProof) {
    const { data } = await apiClient.post<{ round_id: number; round_no: number; participant_count: number; joined_at: string }>('/lottery/join', proof)
    return data
  },
  async getRounds(page = 1, pageSize = 8) {
    const { data } = await apiClient.get<BasePaginationResponse<LotteryRound>>('/lottery/rounds', { params: { page, page_size: pageSize } })
    return data
  },
  async getAdminConfig() {
    const { data } = await apiClient.get<LotteryConfig>('/admin/lottery/config')
    return data
  },
  async updateAdminConfig(payload: LotteryConfig) {
    const { data } = await apiClient.put<LotteryConfig>('/admin/lottery/config', payload)
    return data
  },
  async startRound() {
    const { data } = await apiClient.post<LotteryRound>('/admin/lottery/rounds')
    return data
  },
  async getAdminRounds(page = 1, pageSize = 20) {
    const { data } = await apiClient.get<BasePaginationResponse<LotteryRound>>('/admin/lottery/rounds', { params: { page, page_size: pageSize } })
    return data
  },
  async updateRoundProgress(roundId: number, participantCount: number) {
    const { data } = await apiClient.put<LotteryRound>(`/admin/lottery/rounds/${roundId}/progress`, { participant_count: participantCount })
    return data
  },
  async drawRound(roundId: number) {
    const { data } = await apiClient.post<LotteryDrawResult>(`/admin/lottery/rounds/${roundId}/draw`)
    return data
  }
}

export default lotteryAPI
