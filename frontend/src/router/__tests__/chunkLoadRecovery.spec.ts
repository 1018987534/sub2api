import { describe, expect, it, vi } from 'vitest'

import {
  isChunkLoadError,
  recoverFromRouterError,
  stripChunkReloadMarker,
} from '../chunkLoadRecovery'

const createStorage = () => {
  const values = new Map<string, string>()
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  }
}

describe('chunkLoadRecovery', () => {
  it.each([
    new Error('Failed to fetch dynamically imported module: /assets/pricing.js'),
    new Error('Importing a module script failed.'),
    new Error('Failed to load module script: MIME type text/html'),
    new Error('Unable to preload CSS for /assets/pricing.css'),
    Object.assign(new Error('Loading chunk 42 failed'), { name: 'ChunkLoadError' }),
  ])('识别部署后的分片加载错误', (error) => {
    expect(isChunkLoadError(error)).toBe(true)
  })

  it('不将普通路由错误识别为分片错误', () => {
    expect(isChunkLoadError(new Error('Request failed with status code 500'))).toBe(false)
  })

  it('任何路由错误都会结束全局导航加载', () => {
    const endNavigation = vi.fn()
    const reload = vi.fn()

    const result = recoverFromRouterError(new Error('guard failed'), '/admin/dashboard', {
      endNavigation,
      reload,
      origin: 'https://xiaohondou.com',
    })

    expect(result).toBe('ended')
    expect(endNavigation).toHaveBeenCalledOnce()
    expect(reload).not.toHaveBeenCalled()
  })

  it('分片错误会硬刷新到用户原本点击的目标路由', () => {
    const endNavigation = vi.fn()
    const reload = vi.fn()

    const result = recoverFromRouterError(
      new Error('Failed to fetch dynamically imported module'),
      '/admin/channels/pricing?group=5#prices',
      {
        endNavigation,
        reload,
        storage: createStorage(),
        now: () => 12345,
        origin: 'https://xiaohondou.com',
      }
    )

    expect(result).toBe('reloading')
    expect(endNavigation).toHaveBeenCalledOnce()
    expect(reload).toHaveBeenCalledWith(
      '/admin/channels/pricing?group=5&__sub2api_chunk_reload=12345#prices'
    )
  })

  it('同一目标十秒内只自动刷新一次', () => {
    const storage = createStorage()
    const reload = vi.fn()
    const options = {
      endNavigation: vi.fn(),
      reload,
      storage,
      now: () => 20_000,
      origin: 'https://xiaohondou.com',
    }
    const error = new Error('Importing a module script failed')

    expect(recoverFromRouterError(error, '/admin/channels/pricing', options)).toBe('reloading')
    expect(recoverFromRouterError(error, '/admin/channels/pricing', options)).toBe('throttled')
    expect(reload).toHaveBeenCalledOnce()
    expect(options.endNavigation).toHaveBeenCalledTimes(2)
  })

  it('成功加载后清理硬刷新标记并保留原查询和锚点', () => {
    expect(
      stripChunkReloadMarker(
        'https://xiaohondou.com/admin/channels/pricing?group=5&__sub2api_chunk_reload=12345#prices'
      )
    ).toBe('/admin/channels/pricing?group=5#prices')
    expect(stripChunkReloadMarker('https://xiaohondou.com/admin/dashboard')).toBeNull()
  })
})
