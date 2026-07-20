import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  IMAGE_STUDIO_MODEL,
  ImageStudioAPIError,
  extractImageStudioOutputs,
  isAsyncImageUnavailable,
  deleteImageStudioTask,
  listImageStudioTasks,
  listImageStudioModels,
  submitImageStudioTask,
} from '@/api/imageStudio'

describe('imageStudio API', () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    fetchMock.mockReset()
  })

  it('loads models with the selected API key', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({
      data: [{ id: IMAGE_STUDIO_MODEL }, { id: 'gpt-5.4' }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    await expect(listImageStudioModels('sk-image')).resolves.toEqual([IMAGE_STUDIO_MODEL, 'gpt-5.4'])

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/v1/models'),
      expect.objectContaining({ headers: expect.objectContaining({ Authorization: 'Bearer sk-image' }) }),
    )
  })

  it('submits generation JSON to the async images endpoint', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ id: 'imgtask_1', status: 'processing' }), {
      status: 202,
      headers: { 'Content-Type': 'application/json' },
    }))

    await submitImageStudioTask('sk-image', 'generate', {
      model: IMAGE_STUDIO_MODEL,
      prompt: 'draw a lighthouse',
      size: '1024x1024',
      quality: 'high',
      background: 'auto',
      output_format: 'png',
      n: 1,
      response_format: 'b64_json',
    })

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('POST')
    expect(init.headers).toMatchObject({
      Authorization: 'Bearer sk-image',
      'Content-Type': 'application/json',
    })
    expect(JSON.parse(String(init.body))).toMatchObject({
      model: IMAGE_STUDIO_MODEL,
      prompt: 'draw a lighthouse',
    })
  })

  it('keeps multipart content type browser-managed for image edits', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ id: 'imgtask_2', status: 'processing' }), {
      status: 202,
      headers: { 'Content-Type': 'application/json' },
    }))
    const form = new FormData()
    form.append('model', IMAGE_STUDIO_MODEL)
    form.append('image', new File(['image'], 'source.png', { type: 'image/png' }))

    await submitImageStudioTask('sk-image', 'edit', form)

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/v1/images/edits/async')
    expect(init.headers).toEqual({ Authorization: 'Bearer sk-image' })
    expect(init.body).toBe(form)
  })

  it('normalizes URL and base64 outputs', () => {
    expect(extractImageStudioOutputs({
      data: [
        { url: 'https://cdn.example.com/a.png', revised_prompt: 'revised' },
        { b64_json: 'aW1hZ2U=', output_format: 'webp' },
      ],
    })).toEqual([
      { url: 'https://cdn.example.com/a.png', revisedPrompt: 'revised' },
      { url: 'data:image/webp;base64,aW1hZ2U=', revisedPrompt: undefined },
    ])
  })

  it('lists and deletes persisted image tasks with the selected API key', async () => {
    fetchMock
      .mockResolvedValueOnce(new Response(JSON.stringify({ object: 'list', data: [{ id: 'imgtask_1', status: 'completed' }] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))

    await expect(listImageStudioTasks('sk-image', 50)).resolves.toEqual([{ id: 'imgtask_1', status: 'completed' }])
    await expect(deleteImageStudioTask('sk-image', 'imgtask_1')).resolves.toBeUndefined()

    expect(fetchMock.mock.calls[0][0]).toContain('/v1/images/tasks?limit=50')
    expect(fetchMock.mock.calls[1]).toEqual([
      expect.stringContaining('/v1/images/tasks/imgtask_1'),
      expect.objectContaining({ method: 'DELETE', headers: { Authorization: 'Bearer sk-image' } }),
    ])
  })

  it('only treats the disabled async feature response as a sync fallback signal', () => {
    expect(isAsyncImageUnavailable(new ImageStudioAPIError('async image tasks are not enabled', 404))).toBe(true)
    expect(isAsyncImageUnavailable(new ImageStudioAPIError('model not found', 404))).toBe(false)
    expect(isAsyncImageUnavailable(new ImageStudioAPIError('upstream failed', 502))).toBe(false)
  })
})
