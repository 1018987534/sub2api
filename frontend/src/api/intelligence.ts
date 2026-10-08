import { apiClient } from './client'
import { repeatedArrayParamsSerializer } from './channelMonitorV2'

export interface IntelligenceConfig {
 group_id: number
 version: number
 enabled: boolean
 interval_minutes: number
 model: string
 reasoning_effort: 'none' | 'low' | 'medium' | 'high' | 'xhigh'
 protocol: 'responses' | 'chat_completions' | 'messages'
 prompt: string
 timeout_seconds: number
 api_key_id: number
 match_mode: 'contains_any' | 'exact'
 expected: string[]
 updated_by: number
}
export interface IntelligenceRecord {
 account_id?: number
 id: number
 group_id: number
 checked_at: string
 duration_ms: number
 status: 'normal' | 'degraded' | 'error'
 answer?: string
 error?: string
 config?: IntelligenceConfig
}
export interface IntelligenceRecoveryRecord {
 id: number
 account_id: number
 account_name: string
 group_id: number
 group_name: string
 model: string
 reasoning_effort: string
 protocol: string
 retry_step: number
 started_at: string
 finished_at: string | null
 duration_ms: number
 status: 'running' | 'normal' | 'degraded' | 'error'
 outcome: 'running' | 'recovered' | 'retry' | 'superseded' | 'interrupted' | 'recovery_failed'
}
export interface IntelligenceRecoveryQueueEntry {
 account_id: number
 account_name: string
 retry_step: number
 interval_minutes: number
 next_run_at: string
 running: boolean
 last_checked_at: string | null
 last_status: 'normal' | 'degraded' | 'error' | null
}
export async function getIntelligenceRecoveryHistory(beforeId = 0, signal?: AbortSignal) {
 return (await apiClient.get<{ items: IntelligenceRecoveryRecord[]; queue: IntelligenceRecoveryQueueEntry[]; has_more: boolean; server_time: string }>(
  '/admin/intelligence-checks/recovery/history', { params: { before_id: beforeId }, signal }
 )).data
}
export interface IntelligenceMetadata { model: string; reasoning_effort: IntelligenceConfig['reasoning_effort'] }
export interface IntelligenceStatus { metadata?: Record<string, IntelligenceMetadata>; groups: Record<string, IntelligenceRecord[]>; server_time: string; window_minutes: number; record_limit?: number }
export async function getIntelligenceStatus(groupIds: number[], signal?: AbortSignal) {
 const { data } = await apiClient.get<IntelligenceStatus>('/intelligence-checks/status', {
  params: { group_id: groupIds }, paramsSerializer: { serialize: repeatedArrayParamsSerializer }, signal
 })
 return data
}
export async function getIntelligenceConfigs() {
 return (await apiClient.get<{ items: IntelligenceConfig[]; defaults: IntelligenceConfig }>('/admin/intelligence-checks/config')).data
}
export async function saveIntelligenceConfig(config: IntelligenceConfig) {
 return (await apiClient.put<IntelligenceConfig>('/admin/intelligence-checks/' + config.group_id + '/config', config)).data
}
export async function runIntelligenceCheck(groupId: number) {
 return (await apiClient.post<{ started: boolean }>('/admin/intelligence-checks/' + groupId + '/run')).data
}
export async function getIntelligenceHistory(groupId: number, beforeId = 0) {
 return (await apiClient.get<{ items: IntelligenceRecord[]; has_more: boolean }>('/admin/intelligence-checks/' + groupId + '/history', { params: { before_id: beforeId } })).data
}

// Safe metadata only: no API credential is returned by this endpoint.
export interface IntelligenceKeyOption {
 id: number
 name: string
 group_id: number
 available: boolean
 unavailable_reason?: string
 quota_remaining: number
 expires_at: string | null
}
export async function getIntelligenceKeyOptions(groupId: number, page = 1, search = '', signal?: AbortSignal) {
 return (await apiClient.get<{ items: IntelligenceKeyOption[]; page: number; has_more: boolean }>(
  '/admin/intelligence-checks/' + groupId + '/keys', { params: { page, search }, signal }
 )).data
}
