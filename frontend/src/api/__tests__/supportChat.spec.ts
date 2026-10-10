import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import supportChatAPI from '../supportChat'

vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))
const originalAdapter = apiClient.defaults.adapter
const adapter = vi.fn(async config => ({ status: 200, statusText: 'OK', headers: {}, config, data: { code: 0, data: { id: 1 } } }))
beforeEach(() => { adapter.mockClear(); apiClient.defaults.adapter = adapter })
afterEach(() => { apiClient.defaults.adapter = originalAdapter; vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('support chat request serialization', () => {
  it.each(['user', 'admin'])('keeps %s file-only messages as multipart data through the real Axios transformer', async role => {
    const file = new File(['file contents'], 'notes.txt', { type: 'text/plain' })
    if (role === 'admin') await supportChatAPI.adminSend(41, '', file)
    else await supportChatAPI.send('', file)
    const config = adapter.mock.calls[0][0]
    expect(config.url).toBe(role === 'admin' ? '/admin/chat/conversations/41/messages' : '/chat/messages')
    expect(config.data).toBeInstanceOf(FormData)
    expect(config.data.get('content')).toBe('')
    expect(config.data.get('file')).toBe(file)
    expect(config.headers.get('Content-Type')).not.toBe('application/json')
    expect(config.headers.get('Idempotency-Key')).toBeTruthy()
  })
  it('still serializes ordinary text as JSON', async () => {
    await supportChatAPI.send('你好')
    const config = adapter.mock.calls[0][0]
    expect(JSON.parse(config.data).content).toBe('你好')
    expect(config.headers.get('Content-Type')).toBe('application/json')
  })
  it.each([false, true])('downloads untrusted attachment bytes with their filename (admin=%s)', async admin => {
    vi.useFakeTimers()
    const fetch = vi.spyOn(supportChatAPI, 'fetchAttachment').mockResolvedValue(new Blob(['<script>alert(1)</script>'], { type: 'text/html' }))
    const createObjectURL = vi.fn((_blob: Blob) => 'blob:download')
    const revokeObjectURL = vi.fn()
    vi.stubGlobal('URL', { createObjectURL, revokeObjectURL })
    const clicked: Array<{ href: string; download: string }> = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { clicked.push({ href: this.getAttribute('href')!, download: this.download }) })
    await supportChatAPI.downloadAttachment(4, 'probe.html', admin)
    expect(fetch).toHaveBeenCalledWith(4, admin)
    expect(createObjectURL.mock.calls[0][0].type).toBe('application/octet-stream')
    expect(clicked).toEqual([{ href: 'blob:download', download: 'probe.html' }])
    expect(document.querySelector('a[download]')).toBeNull()
    await vi.advanceTimersByTimeAsync(60000)
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:download')
  })
})
