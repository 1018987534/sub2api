import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { groupMetrics } from '../channelMonitor'

afterEach(() => vi.restoreAllMocks())

describe('channel monitor user metrics API', () => {
  it('loads recent metrics from the dedicated read-only endpoint', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { items: [] } })

    await groupMetrics()

    expect(get).toHaveBeenCalledWith('/channel-monitors/group-metrics', { signal: undefined })
  })
})
