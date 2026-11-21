import { apiClient } from './client'

export const plazaRateKeys = ['input', 'output', 'cache_read', 'cache_write', 'cache_write_1h', 'cache_write_12h'] as const
export type PlazaRateKey = typeof plazaRateKeys[number]
export type CustomPlazaTier = { min_tokens: number; max_tokens: number } & Partial<Record<PlazaRateKey, number | null>>

export const plazaFamilies = [
  { id: 'gpt', name: 'GPT' },
  { id: 'claude', name: 'Claude' },
  { id: 'kimi', name: 'Kimi' },
  { id: 'glm', name: 'GLM' },
  { id: 'deepseek', name: 'DeepSeek' },
] as const
export function plazaFamilyName(family: string): string {
  return plazaFamilies.find(entry => entry.id === family.toLowerCase())?.name ?? family
}

export interface CustomPlazaModel {
  id: string
  family: string
  tiers: CustomPlazaTier[]
}

export interface CustomPlazaAdminModel {
  id: string
  family: string
  hidden: boolean
  gpt_cache_free: boolean
  context_limits: number[] | null
  official_tiers: CustomPlazaTier[]
  default_tiers: CustomPlazaTier[]
  overrides: CustomPlazaTier[]
}

export async function getCustomModelPlaza(): Promise<CustomPlazaModel[]> {
  const { data } = await apiClient.get<CustomPlazaModel[]>('/custom-model-plaza')
  return data
}

export async function getCustomModelPlazaAdmin(): Promise<CustomPlazaAdminModel[]> {
  const { data } = await apiClient.get<CustomPlazaAdminModel[]>('/admin/custom-model-plaza')
  return data
}

export type CustomPlazaConfig = Pick<CustomPlazaAdminModel, 'id' | 'family' | 'hidden' | 'context_limits'> & { tiers: CustomPlazaTier[] }

export async function getCustomModelPlazaDefaults(id: string, family: string): Promise<CustomPlazaAdminModel> {
  const { data } = await apiClient.get<CustomPlazaAdminModel>('/admin/custom-model-plaza/defaults', { params: { model: id, family } })
  return data
}

export async function saveCustomModelPlazaConfig(config: CustomPlazaConfig, create: boolean): Promise<CustomPlazaAdminModel> {
  const { data } = await apiClient.request<CustomPlazaAdminModel>({
    url: '/admin/custom-model-plaza', method: create ? 'post' : 'put', data: config,
  })
  return data
}

export async function deleteCustomModelPlazaModel(id: string): Promise<void> {
  await apiClient.delete('/admin/custom-model-plaza', { params: { model: id } })
}
