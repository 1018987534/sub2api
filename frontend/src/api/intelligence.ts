import { apiClient } from './client'
import { repeatedArrayParamsSerializer } from './channelMonitorV2'

export interface IntelligenceConfig {
 group_id: number
 probe_id?: number
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
export interface IntelligenceRecordDetail {
 id: number
 group_id: number
 status: IntelligenceRecord['status']
 answer: string
 model: string
 reasoning_effort: string
}
export async function getIntelligenceRecordDetail(groupId: number, recordId: number, signal?: AbortSignal) {
 return (await apiClient.get<IntelligenceRecordDetail>(
  '/intelligence-checks/' + groupId + '/records/' + recordId, { signal }
 )).data
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
export interface IntelligenceMetadata {
 model: string
 reasoning_effort: IntelligenceConfig['reasoning_effort']
 protocol?: IntelligenceConfig['protocol']
 interval_minutes?: number
 enabled?: boolean
}
export interface IntelligenceModelTimeline extends IntelligenceMetadata { probe_id: number; records: IntelligenceRecord[] }
export interface IntelligenceStatus {
 metadata?: Record<string, IntelligenceMetadata>
 groups: Record<string, IntelligenceRecord[]>
 models?: Record<string, IntelligenceModelTimeline[]>
 server_time: string
 window_minutes: number
 record_limit?: number
}
export async function getIntelligenceStatus(groupIds: number[], signal?: AbortSignal) {
 const { data } = await apiClient.get<IntelligenceStatus>('/intelligence-checks/status', {
  params: { group_id: groupIds }, paramsSerializer: { serialize: repeatedArrayParamsSerializer }, signal
 })
 return data
}
export async function getIntelligenceConfigs() {
 return (await apiClient.get<{
  items: IntelligenceConfig[]
  defaults: IntelligenceConfig
  max_models_per_group?: number
  pause_threshold: number
  max_pause_threshold: number
 }>('/admin/intelligence-checks/config')).data
}
export async function saveIntelligencePauseThreshold(pauseThreshold: number) {
 return (await apiClient.put<{ pause_threshold: number }>('/admin/intelligence-checks/protection', {
  pause_threshold: pauseThreshold,
 })).data
}
export async function saveIntelligenceConfig(config: IntelligenceConfig) {
 if (config.probe_id) {
  return (await apiClient.put<IntelligenceConfig>(intelligenceModelsPath(config.group_id) + '/' + config.probe_id, config)).data
 }
 return (await apiClient.put<IntelligenceConfig>('/admin/intelligence-checks/' + config.group_id + '/config', config)).data
}
const intelligenceModelsPath = (groupId: number) => '/admin/intelligence-checks/' + groupId + '/models'
export async function addIntelligenceModel(config: IntelligenceConfig) {
 return (await apiClient.post<IntelligenceConfig>(intelligenceModelsPath(config.group_id), config)).data
}
export async function deleteIntelligenceModel(groupId: number, probeId: number, version: number) {
 return (await apiClient.delete<{ deleted: boolean }>(intelligenceModelsPath(groupId) + '/' + probeId, { params: { version } })).data
}
export async function runIntelligenceCheck(groupId: number, probeId?: number) {
 return (await apiClient.post<{ started: boolean }>('/admin/intelligence-checks/' + groupId + '/run', undefined, { params: { probe_id: probeId } })).data
}
export async function getIntelligenceHistory(groupId: number, beforeId = 0, probeId?: number) {
 return (await apiClient.get<{ items: IntelligenceRecord[]; has_more: boolean }>('/admin/intelligence-checks/' + groupId + '/history', { params: { before_id: beforeId, probe_id: probeId } })).data
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
