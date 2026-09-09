import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import supportChatAPI from '../supportChat'

vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))
const originalAdapter = apiClient.defaults.adapter
const adapter = vi.fn(async config => ({ status: 200, statusText: 'OK', headers: {}, config, data: { code: 0, data: { id: 1 } } }))
beforeEach(() => { adapter.mockClear(); apiClient.defaults.adapter = adapter })
afterEach(() => { apiClient.defaults.adapter = originalAdapter })

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
})
