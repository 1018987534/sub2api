import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import LotteryView from '../LotteryView.vue'

const { getAnnouncement, getCurrent, getRounds, showError, showSuccess } = vi.hoisted(() => ({
  getAnnouncement: vi.fn(),
  getCurrent: vi.fn(),
  getRounds: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/lottery', () => ({
  default: { getAnnouncement, getCurrent, getRounds },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const stubs = {
  AppLayout: { template: '<div><slot /></div>' },
  Icon: true,
  LotterySliderCaptcha: true,
  'router-link': true,
}

function winner(id: number, roundNo = 2) {
  return {
    id,
    round_id: roundNo,
    round_no: roundNo,
    email: `winner-${id}@example.com`,
    prize_amount: 5,
    awarded_at: '2026-08-29T13:00:00Z',
    participated_at: '2026-08-29T12:00:00Z',
  }
}

const round = {
  id: 3,
  round_no: 3,
  status: 'open',
  draw_mode: 'auto',
  participant_count: 4,
  participant_threshold: 50,
  prize_count: 2,
  prize_amount: 5,
  require_recharge: false,
  min_recharge_amount: 0,
  min_account_age_days: 0,
  started_at: '2026-08-29T13:00:00Z',
}

const recentWinners = [
  ...Array.from({ length: 4 }, (_, index) => winner(index + 1, 2)),
  ...Array.from({ length: 6 }, (_, index) => winner(index + 5, 1)),
]

describe('LotteryView slider verification', () => {
  const viewPath = resolve(dirname(fileURLToPath(import.meta.url)), '../LotteryView.vue')
  const viewSource = readFileSync(viewPath, 'utf8')
  const sliderPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../components/lottery/LotterySliderCaptcha.vue')
  const sliderSource = readFileSync(sliderPath, 'utf8')

  it('requires a fresh self-hosted slider proof before joining', () => {
    expect(viewSource).toContain('<LotterySliderCaptcha')
    expect(sliderSource).toContain('await lotteryAPI.getCaptcha()')
    expect(sliderSource).toContain('captcha_id: challenge.value.id')
    expect(sliderSource).toContain('captcha_x: point.x')
    expect(sliderSource).toContain('captcha_y: point.y')
    expect(viewSource).toContain("window.dispatchEvent(new Event('lottery-availability-changed'))")
  })

  it('does not depend on Tencent or Aliyun captcha settings', () => {
    expect(viewSource).not.toContain('CaptchaChallenge')
    expect(viewSource).not.toContain('tencentCaptchaEnabled')
    expect(viewSource).not.toContain('aliyunCaptchaEnabled')
    expect(sliderSource).toContain("from 'go-captcha-vue'")
  })
})

describe('LotteryView snapshot', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getAnnouncement.mockResolvedValue({ enabled: true, current_round: round, recent_winners: recentWinners })
    getCurrent.mockResolvedValue({
      enabled: true,
      current_round: round,
      joined: false,
      eligibility: { eligible: true, total_recharge: 0 },
      recent_winners: recentWinners,
      my_recent_winners: [],
    })
    getRounds.mockResolvedValue({ items: [], total: 0, pages: 0 })
  })

  it('loads the public endpoint and frames only the previous round without changing row spacing', async () => {
    const wrapper = mount(LotteryView, { props: { snapshot: true }, global: { stubs } })
    await flushPromises()

    expect(getAnnouncement).toHaveBeenCalledOnce()
    expect(getCurrent).not.toHaveBeenCalled()
    expect(wrapper.attributes('data-lottery-snapshot-ready')).toBe('true')
    const sections = wrapper.findAll('section')
    expect(sections).toHaveLength(4)
    expect(sections[0].classes()).toContain('lg:h-[540px]')
    expect(sections[1].classes()).toContain('lg:h-[540px]')
    expect(wrapper.find('[data-lottery-capture-region="true"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('winner-1@example.com')
    expect(wrapper.text()).toContain('winner-10@example.com')
    expect(wrapper.findAll('[data-lottery-previous-round="true"]')).toHaveLength(1)
    expect(wrapper.find('[data-lottery-previous-round="true"]').classes()).toEqual([
      'lottery-winner-group',
      'lottery-previous-round-frame',
    ])
    expect(wrapper.find('[aria-label="common.nextPage"]').exists()).toBe(false)
  })

  it('loads the next winner batch when the list scrolls near the bottom', async () => {
    getAnnouncement.mockResolvedValue({
      enabled: true,
      current_round: round,
      recent_winners: Array.from({ length: 15 }, (_, index) => winner(index + 1, index < 4 ? 2 : 1)),
    })
    const wrapper = mount(LotteryView, { props: { snapshot: true }, global: { stubs } })
    await flushPromises()

    expect(wrapper.text()).toContain('winner-10@example.com')
    expect(wrapper.text()).not.toContain('winner-11@example.com')
    const scrollPane = wrapper.find('[data-lottery-winners-scroll="true"]')
    Object.defineProperties(scrollPane.element, {
      scrollTop: { configurable: true, value: 690 },
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1000 },
    })
    await scrollPane.trigger('scroll')

    expect(wrapper.text()).toContain('winner-11@example.com')
    expect(wrapper.text()).toContain('winner-15@example.com')
  })

  it('never draws the previous-round frame on the actual lottery page', async () => {
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()

    expect(getCurrent).toHaveBeenCalledOnce()
    expect(getRounds).toHaveBeenCalledOnce()
    expect(getAnnouncement).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('winner-1@example.com')
    expect(wrapper.findAll('[data-lottery-previous-round="true"]')).toHaveLength(0)
  })
})
