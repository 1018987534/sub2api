import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LotteryView from '../LotteryView.vue'

const api = vi.hoisted(() => ({
  getAdminConfig: vi.fn(), getAdminRounds: vi.fn(), updateRoundPrizeCount: vi.fn(),
  updateRoundStatus: vi.fn(), showError: vi.fn(), showSuccess: vi.fn(),
}))
vi.mock('@/api/lottery', () => ({ default: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => api }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

const round = { id: 21, round_no: 21, status: 'open', participant_threshold: 50, participant_count: 12, real_participant_count: 12, prize_count: 2, prize_amount: 5, winner_count: 0 }
const config = { enabled: true, participant_threshold: 50, prize_count: 2, prize_amount: 5 }
const stubs = {
  AppLayout: { template: '<div><slot /></div>' }, Icon: true, LotteryParticipantsDialog: true,
  ConfirmDialog: { props: ['show'], template: '<button v-if="show" data-testid="confirm" @click="$emit(\'confirm\')">confirm</button>' }
}
function button(wrapper: ReturnType<typeof mount>, text: string) {
  const found = wrapper.findAll('button').find(item => item.text() === text)
  expect(found).toBeDefined()
  return found!
}

const viewPath = resolve(dirname(fileURLToPath(import.meta.url)), '../LotteryView.vue')
const viewSource = readFileSync(viewPath, 'utf8')
const dialogPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../components/lottery/LotteryParticipantsDialog.vue')
const dialogSource = readFileSync(dialogPath, 'utf8')

describe('Admin LotteryView progress control', () => {
  it('updates an open round with an absolute participant count', () => {
    expect(viewSource).toContain('lotteryAPI.updateRoundProgress(round.id')
    expect(viewSource).toContain(':min="round.real_participant_count || 0"')
    expect(viewSource).toContain(':max="round.participant_threshold"')
  })

  it('contains no actor pacing controls', () => {
    expect(viewSource).not.toContain('actor_percentage')
    expect(viewSource).not.toContain('actor_join_min_seconds')
    expect(viewSource).not.toContain('actor_join_max_seconds')
  })

  it('labels balance amounts as USD instead of coins', () => {
    expect(viewSource.match(/>USD<\/span>/g)).toHaveLength(2)
    expect(viewSource).not.toContain('coins')
  })

  it('opens a paginated list of exact real participants', () => {
    expect(viewSource).toContain('@click="openParticipants(round)"')
    expect(viewSource).toContain('<LotteryParticipantsDialog')
    expect(dialogSource).toContain('lotteryAPI.getAdminParticipants')
    expect(dialogSource).toContain("key: 'user_id'")
    expect(dialogSource).toContain("key: 'email'")
    expect(dialogSource).toContain("key: 'client_ip'")
    expect(dialogSource).toContain("key: 'joined_at'")
  })
})

describe('Admin LotteryView active round actions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.getAdminConfig.mockResolvedValue(config)
    api.getAdminRounds.mockResolvedValue({ items: [{ ...round }] })
    api.updateRoundPrizeCount.mockResolvedValue({ ...round, prize_count: 6 })
    api.updateRoundStatus.mockImplementation(async (_id, status) => ({ ...round, status }))
  })

  it('changes this round only, renders the result, and reports API failures', async () => {
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    await wrapper.get('input[aria-label="admin.lottery.currentPrizeCount"]').setValue('6')
    await button(wrapper, 'admin.lottery.updatePrizeCount').trigger('click')
    await flushPromises()
    expect(api.updateRoundPrizeCount).toHaveBeenCalledWith(21, 6)
    expect(wrapper.findAll('tbody td')[4].text()).toBe('6')
    expect(api.showSuccess).toHaveBeenCalledWith('admin.lottery.prizeCountUpdated')
    api.updateRoundPrizeCount.mockRejectedValueOnce(new Error('fixture failure'))
    await wrapper.get('input[aria-label="admin.lottery.currentPrizeCount"]').setValue('7')
    await button(wrapper, 'admin.lottery.updatePrizeCount').trigger('click')
    await flushPromises()
    expect(api.showError).toHaveBeenCalled()
    expect(wrapper.findAll('tbody td')[4].text()).toBe('6')
  })

  it('rejects invalid counts before calling the API', async () => {
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    for (const value of ['0', '51', '1.5']) {
      await wrapper.get('input[aria-label="admin.lottery.currentPrizeCount"]').setValue(value)
      await button(wrapper, 'admin.lottery.updatePrizeCount').trigger('click')
    }
    expect(api.updateRoundPrizeCount).not.toHaveBeenCalled()
    expect(api.showError).toHaveBeenCalledTimes(3)
  })

  it('confirms pause, prevents draws and new rounds, resumes, and confirms void', async () => {
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    await button(wrapper, 'admin.lottery.pauseRound').trigger('click')
    expect(api.updateRoundStatus).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(api.updateRoundStatus).toHaveBeenLastCalledWith(21, 'paused')
    expect(wrapper.text()).toContain('lottery.statuses.paused')
    expect(wrapper.findAll('button').some(item => item.text() === 'admin.lottery.draw')).toBe(false)
    expect(button(wrapper, 'admin.lottery.startRound').attributes('disabled')).toBeDefined()
    await button(wrapper, 'admin.lottery.resumeRound').trigger('click')
    await flushPromises()
    expect(api.updateRoundStatus).toHaveBeenLastCalledWith(21, 'open')
    await button(wrapper, 'admin.lottery.cancelRound').trigger('click')
    await wrapper.get('[data-testid="confirm"]').trigger('click')
    await flushPromises()
    expect(api.updateRoundStatus).toHaveBeenLastCalledWith(21, 'cancelled')
    expect(wrapper.text()).toContain('lottery.statuses.cancelled')
    expect(wrapper.find('input[aria-label="admin.lottery.currentPrizeCount"]').exists()).toBe(false)
    expect(button(wrapper, 'admin.lottery.startRound').attributes('disabled')).toBeUndefined()
  })
})
