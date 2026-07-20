import { buildGatewayUrl } from './client'

export const IMAGE_STUDIO_MODEL = 'gpt-image-2'

export type ImageStudioMode = 'generate' | 'edit'

export interface ImageStudioGenerationPayload {
  model: string
  prompt: string
  size: string
  quality: string
  background: string
  output_format: string
  n: number
  response_format: 'b64_json'
}

export interface ImageStudioOutput {
  url: string
  revisedPrompt?: string
}

export interface ImageStudioResponse {
  created?: number
  data?: Array<{
    url?: string
    b64_json?: string
    revised_prompt?: string
    output_format?: string
  }>
}

export interface ImageStudioTask {
  id: string
  task_id?: string
  object?: string
  status: 'processing' | 'completed' | 'failed' | string
  poll_url?: string
  http_status?: number
  image_url?: string
  result?: ImageStudioResponse
  error?: {
    type?: string
    code?: string
    message?: string
  }
  created_at?: number
  completed_at?: number
  expires_at?: number
}

export class ImageStudioAPIError extends Error {
  status: number
  code: string

  constructor(message: string, status: number, code = '') {
    super(message)
    this.name = 'ImageStudioAPIError'
    this.status = status
    this.code = code
  }
}

async function parseError(response: Response): Promise<ImageStudioAPIError> {
  let message = response.statusText || `HTTP ${response.status}`
  let code = ''
  try {
    const body = await response.json()
    message = body?.error?.message || body?.message || message
    code = body?.error?.code || body?.code || ''
  } catch {
    // Keep the HTTP fallback for non-JSON proxy errors.
  }
  return new ImageStudioAPIError(message, response.status, String(code))
}

function authHeaders(apiKey: string, body: BodyInit): HeadersInit {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${apiKey}`,
  }
  if (!(body instanceof FormData)) {
    headers['Content-Type'] = 'application/json'
  }
  return headers
}

async function requestJSON<T>(
  path: string,
  apiKey: string,
  init: RequestInit = {},
): Promise<T> {
  const response = await fetch(buildGatewayUrl(path), {
    ...init,
    headers: {
      Authorization: `Bearer ${apiKey}`,
      ...init.headers,
    },
  })
  if (!response.ok) throw await parseError(response)
  return response.json() as Promise<T>
}

export async function listImageStudioModels(apiKey: string, signal?: AbortSignal): Promise<string[]> {
  const body = await requestJSON<{ data?: Array<{ id?: string; name?: string }> }>('/v1/models', apiKey, { signal })
  return (body.data || [])
    .map((item) => String(item.id || item.name || '').trim())
    .filter(Boolean)
}

export async function submitImageStudioTask(
  apiKey: string,
  mode: ImageStudioMode,
  body: ImageStudioGenerationPayload | FormData,
  signal?: AbortSignal,
): Promise<ImageStudioTask> {
  const endpoint = mode === 'edit' ? '/v1/images/edits/async' : '/v1/images/generations/async'
  const requestBody = body instanceof FormData ? body : JSON.stringify(body)
  const response = await fetch(buildGatewayUrl(endpoint), {
    method: 'POST',
    headers: authHeaders(apiKey, requestBody),
    body: requestBody,
    signal,
  })
  if (!response.ok) throw await parseError(response)
  return response.json() as Promise<ImageStudioTask>
}

export async function getImageStudioTask(
  apiKey: string,
  taskID: string,
  signal?: AbortSignal,
): Promise<ImageStudioTask> {
  return requestJSON<ImageStudioTask>(`/v1/images/tasks/${encodeURIComponent(taskID)}`, apiKey, { signal })
}

export async function generateImageStudioSync(
  apiKey: string,
  mode: ImageStudioMode,
  body: ImageStudioGenerationPayload | FormData,
  signal?: AbortSignal,
): Promise<ImageStudioResponse> {
  const endpoint = mode === 'edit' ? '/v1/images/edits' : '/v1/images/generations'
  const requestBody = body instanceof FormData ? body : JSON.stringify(body)
  const response = await fetch(buildGatewayUrl(endpoint), {
    method: 'POST',
    headers: authHeaders(apiKey, requestBody),
    body: requestBody,
    signal,
  })
  if (!response.ok) throw await parseError(response)
  return response.json() as Promise<ImageStudioResponse>
}

export function isAsyncImageUnavailable(error: unknown): boolean {
  if (!(error instanceof ImageStudioAPIError) || error.status !== 404) return false
  const message = error.message.toLowerCase()
  return message.includes('async image tasks') || message.includes('not enabled')
}

export function extractImageStudioOutputs(
  response: ImageStudioResponse | undefined,
  fallbackFormat = 'png',
): ImageStudioOutput[] {
  if (!response) return []
  return (response.data || []).flatMap((item) => {
    const format = String(item.output_format || fallbackFormat).toLowerCase().replace(/^image\//, '')
    const url = String(item.url || '').trim() || (item.b64_json ? `data:image/${format};base64,${item.b64_json}` : '')
    if (!url) return []
    return [{ url, revisedPrompt: item.revised_prompt || undefined }]
  })
}

export const imageStudioAPI = {
  listModels: listImageStudioModels,
  submitTask: submitImageStudioTask,
  getTask: getImageStudioTask,
  generateSync: generateImageStudioSync,
}

export default imageStudioAPI
