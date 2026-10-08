import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IntelligenceRecoveryPanel from '../IntelligenceRecoveryPanel.vue'
import { getIntelligenceRecoveryHistory } from '@/api/intelligence'
vi.mock('@/api/intelligence', () => ({ getIntelligenceRecoveryHistory: vi.fn() }))
const result = {
 items: [{ id: 9, account_id: 12459, account_name: 'paused-account', group_id: 5, group_name: '013不降智', model: 'probe-model', reasoning_effort: 'low', protocol: 'responses', retry_step: 2, started_at: '2026-10-08T01:00:00Z', finished_at: '2026-10-08T01:00:01Z', duration_ms: 1000, status: 'normal' as const, outcome: 'recovered' as const }],
 queue: [{ account_id: 12464, account_name: 'queued-account', retry_step: 2, interval_minutes: 8, next_run_at: '2026-10-08T01:08:00Z', running: false, last_checked_at: '2026-10-08T01:00:00Z', last_status: 'degraded' as const }],
 has_more: false, server_time: '2026-10-08T01:00:01Z'
}
beforeEach(() => { vi.resetAllMocks(); vi.mocked(getIntelligenceRecoveryHistory).mockResolvedValue(result) })
describe('account recovery admin history', () => {
 it('shows account identity, result, recovery action, source and next scheduled test', async () => {
  const wrapper = mount(IntelligenceRecoveryPanel); await flushPromises()
  for (const text of ['paused-account', '#12459', '已恢复待采集', '013不降智', 'probe-model', 'queued-account', '等待重试', '8 分钟']) expect(wrapper.text()).toContain(text)
  expect(wrapper.findAll('table')).toHaveLength(2); wrapper.unmount()
 })
 it('pages history and explicitly refreshes the first page', async () => {
  vi.mocked(getIntelligenceRecoveryHistory).mockResolvedValueOnce({ ...result, has_more: true })
  const wrapper = mount(IntelligenceRecoveryPanel); await flushPromises()
  vi.mocked(getIntelligenceRecoveryHistory).mockResolvedValueOnce({ ...result, items: [{ ...result.items[0], id: 8 }] })
  await wrapper.findAll('button').find(b => b.text() === '加载更早记录')!.trigger('click'); await flushPromises()
  expect(getIntelligenceRecoveryHistory).toHaveBeenLastCalledWith(9, expect.any(AbortSignal))
  expect(wrapper.findAll('table')[1].findAll('tbody tr')).toHaveLength(2)
  await wrapper.findAll('button').find(b => b.text() === '刷新')!.trigger('click'); await flushPromises()
  expect(wrapper.findAll('table')[1].findAll('tbody tr')).toHaveLength(1); wrapper.unmount()
 })
 it('shows errors and aborts in-flight reads on unmount', async () => {
  vi.mocked(getIntelligenceRecoveryHistory).mockRejectedValueOnce(new Error('fixture read failed'))
  const wrapper = mount(IntelligenceRecoveryPanel); await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toContain('fixture read failed')
  const signal = vi.mocked(getIntelligenceRecoveryHistory).mock.calls[0][1]!
  wrapper.unmount(); expect(signal.aborted).toBe(true)
 })
})
