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
export type PlazaFamily = typeof plazaFamilies[number]['id']

export interface CustomPlazaModel {
  id: string
  family: PlazaFamily
  tiers: CustomPlazaTier[]
}

export interface CustomPlazaAdminModel {
  id: string
  family: PlazaFamily
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

export async function saveCustomModelPlazaPrices(id: string, tiers: CustomPlazaTier[]): Promise<CustomPlazaAdminModel> {
  const { data } = await apiClient.put<CustomPlazaAdminModel>(`/admin/custom-model-plaza/${encodeURIComponent(id)}`, { tiers })
  return data
}
