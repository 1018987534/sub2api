import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post, put }
}))

import {
  confirmUpstreamBillingPrice,
  getUpstreamBillingProbeSettings,
  listUpstreamBillingPriceDiscrepancies,
  probeUpstreamBilling,
  probeUpstreamBillingBatch,
  setUpstreamBillingProbeEnabled,
  updateUpstreamBillingProbeSettings
} from '@/api/admin/accounts'

describe('admin account upstream billing probe API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
  })

  it('reads and updates global settings', async () => {
    const settings = { enabled: true, interval_minutes: 30 }
    get.mockResolvedValueOnce({ data: settings })
    put.mockResolvedValueOnce({ data: settings })

    await expect(getUpstreamBillingProbeSettings()).resolves.toEqual(settings)
    await expect(updateUpstreamBillingProbeSettings(settings)).resolves.toEqual(settings)
    expect(get).toHaveBeenCalledWith('/admin/accounts/upstream-billing-probe/settings')
    expect(put).toHaveBeenCalledWith('/admin/accounts/upstream-billing-probe/settings', settings)
  })

  it('uses dedicated account and batch endpoints', async () => {
    const result = { account_id: 7, snapshot: { status: 'unsupported' } }
    put.mockResolvedValueOnce({ data: {} })
    post.mockResolvedValueOnce({ data: result })
    post.mockResolvedValueOnce({ data: { results: [result] } })

    await setUpstreamBillingProbeEnabled(7, true)
    await expect(probeUpstreamBilling(7)).resolves.toEqual(result)
    await expect(probeUpstreamBillingBatch([7])).resolves.toEqual([result])

    expect(put).toHaveBeenCalledWith('/admin/accounts/7/upstream-billing-probe', { enabled: true })
    expect(post).toHaveBeenNthCalledWith(1, '/admin/accounts/7/upstream-billing-probe')
    expect(post).toHaveBeenNthCalledWith(2, '/admin/accounts/upstream-billing-probe/batch', { account_ids: [7] })
  })

  it('lists inferred discrepancies and confirms one model at a time', async () => {
    const item = {
      account_id: 12017,
      account_name: 'plus-shayu-plus',
      model: 'gpt-5.6-luna',
      current_source: 'local',
      current_price: { input_price_per_token: 0.2e-6, output_price_per_token: 1.2e-6 },
      inferred_price: {
        input_price_per_token: 1e-6,
        output_price_per_token: 6e-6,
        sample_count: 2,
        input_sample_count: 2,
        output_sample_count: 2,
        observed_at: '2026-08-15T03:00:00Z'
      }
    }
    get.mockResolvedValueOnce({ data: { items: [item] } })
    post.mockResolvedValueOnce({ data: item })

    await expect(listUpstreamBillingPriceDiscrepancies()).resolves.toEqual([item])
    await expect(confirmUpstreamBillingPrice(12017, 'gpt-5.6-luna')).resolves.toEqual(item)

    expect(get).toHaveBeenCalledWith('/admin/accounts/upstream-billing-price-discrepancies')
    expect(post).toHaveBeenCalledWith(
      '/admin/accounts/12017/upstream-billing-price-confirm',
      { model: 'gpt-5.6-luna' }
    )
  })
})
