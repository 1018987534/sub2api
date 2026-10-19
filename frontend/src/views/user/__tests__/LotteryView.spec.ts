import { existsSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import LotteryView from '../LotteryView.vue'

const { getAnnouncement, getCurrent, getRounds, markRead, auth, getPublicSettings, join, verifyAction, resetCaptcha, showError, showSuccess } = vi.hoisted(() => ({
  getAnnouncement: vi.fn(),
  getCurrent: vi.fn(),
  getRounds: vi.fn(),
  markRead: vi.fn(),
  auth: { user: { id: 7 } },
  getPublicSettings: vi.fn(),
  join: vi.fn(),
  verifyAction: vi.fn(),
  resetCaptcha: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/lottery', () => ({
  default: { getAnnouncement, getCurrent, getRounds, markRead, join },
}))

vi.mock('@/api/auth', () => ({ getPublicSettings }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }) }
})

const CaptchaStub = defineComponent({
  name: 'CaptchaChallenge',
  props: ['turnstileEnabled', 'turnstileSiteKey', 'tencentEnabled', 'tencentAppId', 'tencentRegion', 'aliyunEnabled', 'aliyunSceneId', 'aliyunPrefix', 'aliyunRegion'],
  emits: ['verify', 'expire', 'error'],
  setup(_, { expose }) {
    expose({ verifyAction, reset: resetCaptcha })
    return () => h('div', { 'data-testid': 'login-captcha' })
  },
})

const stubs = {
  AppLayout: { template: '<div><slot /></div>' },
  Icon: true,
  CaptchaChallenge: CaptchaStub,
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

beforeEach(() => {
  vi.clearAllMocks()
  auth.user = { id: 7 }
  markRead.mockReset().mockResolvedValue(undefined)
  getPublicSettings.mockReset().mockResolvedValue({ turnstile_enabled: false, turnstile_site_key: '' })
  verifyAction.mockReset().mockResolvedValue({ token: 'fixture-ticket', randstr: 'fixture-randstr' })
  join.mockReset().mockResolvedValue({ round_id: 3, round_no: 3, participant_count: 5 })
  getCurrent.mockResolvedValue({ enabled: true, current_round: round, joined: false, eligibility: { eligible: true, total_recharge: 0 }, recent_winners: recentWinners, my_recent_winners: [] })
  getRounds.mockResolvedValue({ items: [], total: 0, pages: 0 })
})

describe('LotteryView login captcha', () => {
  function mountView() { return mount(LotteryView, { global: { stubs } }) }

  it('uses the login Tencent configuration and proof', async () => {
    getPublicSettings.mockResolvedValue({ turnstile_enabled: false, tencent_captcha_enabled: true, tencent_captcha_app_id: 'fixture-app', tencent_captcha_region: 'intl' })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.getComponent(CaptchaStub).props()).toMatchObject({ tencentEnabled: true, tencentAppId: 'fixture-app', tencentRegion: 'intl' })
    const event = vi.spyOn(window, 'dispatchEvent')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(verifyAction).toHaveBeenCalledOnce()
    expect(join).toHaveBeenCalledWith({ tencent_captcha_ticket: 'fixture-ticket', tencent_captcha_randstr: 'fixture-randstr' })
    expect(showSuccess).toHaveBeenCalledWith('lottery.joinSuccess')
    expect(resetCaptcha).toHaveBeenCalledOnce()
    expect(event.mock.calls.some(([value]) => value.type === 'lottery-availability-changed')).toBe(true)
    event.mockRestore()
    wrapper.unmount()
  })

  it('uses the login Aliyun scene and passes its token in turnstile_token', async () => {
    getPublicSettings.mockResolvedValue({ turnstile_enabled: false, aliyun_captcha_enabled: true, aliyun_captcha_scene_id: 'fixture-scene', aliyun_captcha_prefix: 'fixture-prefix', aliyun_captcha_region: 'sgp' })
    verifyAction.mockResolvedValue({ token: 'fixture-aliyun-param', randstr: '' })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.getComponent(CaptchaStub).props()).toMatchObject({ aliyunEnabled: true, aliyunSceneId: 'fixture-scene', aliyunPrefix: 'fixture-prefix', aliyunRegion: 'sgp' })
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(join).toHaveBeenCalledWith({ turnstile_token: 'fixture-aliyun-param' })
    expect(resetCaptcha).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('requires the login Turnstile proof and resets it after submitting', async () => {
    getPublicSettings.mockResolvedValue({ turnstile_enabled: true, turnstile_site_key: 'fixture-site-key' })
    const wrapper = mountView()
    await flushPromises()
    const button = wrapper.get('button.btn-primary')
    const captcha = wrapper.getComponent(CaptchaStub)
    expect(captcha.props()).toMatchObject({ turnstileEnabled: true, turnstileSiteKey: 'fixture-site-key' })
    expect(button.attributes('disabled')).toBeDefined()
    await button.trigger('click')
    expect(join).not.toHaveBeenCalled()
    captcha.vm.$emit('verify', 'fixture-turnstile-token')
    await wrapper.vm.$nextTick()
    await button.trigger('click')
    await flushPromises()
    expect(join).toHaveBeenCalledWith({ turnstile_token: 'fixture-turnstile-token' })
    expect(verifyAction).not.toHaveBeenCalled()
    expect(resetCaptcha).toHaveBeenCalledOnce()
    expect(button.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it.each(['expire', 'error'])('invalidates the proof on %s without submitting', async event => {
    getPublicSettings.mockResolvedValue({ turnstile_enabled: true, turnstile_site_key: 'fixture-site-key' })
    const wrapper = mountView()
    await flushPromises()
    const captcha = wrapper.getComponent(CaptchaStub)
    captcha.vm.$emit('verify', 'fixture-token')
    await wrapper.vm.$nextTick()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeUndefined()
    captcha.vm.$emit(event)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    expect(showError).toHaveBeenCalledWith(event === 'expire' ? 'auth.turnstileExpired' : 'auth.turnstileFailed')
    expect(join).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not submit when login verification is cancelled', async () => {
    getPublicSettings.mockResolvedValue({ turnstile_enabled: false, tencent_captcha_enabled: true, tencent_captcha_app_id: 'fixture-app' })
    verifyAction.mockResolvedValue(null)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(join).not.toHaveBeenCalled()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('obtains a fresh proof after a failed API request', async () => {
    getPublicSettings.mockResolvedValue({ turnstile_enabled: false, tencent_captcha_enabled: true, tencent_captcha_app_id: 'fixture-app' })
    join.mockRejectedValueOnce(new Error('fixture failure')).mockResolvedValueOnce({ round_id: 3 })
    verifyAction.mockResolvedValueOnce({ token: 'fixture-ticket-1', randstr: 'rand-1' }).mockResolvedValueOnce({ token: 'fixture-ticket-2', randstr: 'rand-2' })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(resetCaptcha).toHaveBeenCalledOnce()
    expect(showError).toHaveBeenCalled()
    expect(showSuccess).not.toHaveBeenCalled()
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(verifyAction).toHaveBeenCalledTimes(2)
    expect(join).toHaveBeenNthCalledWith(2, { tencent_captcha_ticket: 'fixture-ticket-2', tencent_captcha_randstr: 'rand-2' })
    wrapper.unmount()
  })

  it('prevents duplicate entry while verification is pending', async () => {
    getPublicSettings.mockResolvedValue({ turnstile_enabled: false, tencent_captcha_enabled: true, tencent_captcha_app_id: 'fixture-app' })
    let finish!: (proof: { token: string; randstr: string }) => void
    verifyAction.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountView()
    await flushPromises()
    const button = wrapper.get('button.btn-primary')
    await button.trigger('click')
    await button.trigger('click')
    expect(verifyAction).toHaveBeenCalledOnce()
    expect(join).not.toHaveBeenCalled()
    finish({ token: 'fixture-ticket', randstr: 'fixture-randstr' })
    await flushPromises()
    expect(join).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('does not permit entry if public settings fail to load', async () => {
    getPublicSettings.mockRejectedValue(new Error('fixture unavailable'))
    const wrapper = mountView()
    await flushPromises()
    expect(join).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalled()
    expect(wrapper.find('button.btn-primary').exists()).toBe(false)
    wrapper.unmount()
  })

  it('matches login when disabled and never loads verification for snapshots', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(join).toHaveBeenCalledWith({})
    expect(wrapper.findComponent(CaptchaStub).exists()).toBe(false)
    wrapper.unmount()
    getPublicSettings.mockClear()
    getAnnouncement.mockResolvedValue({ enabled: true, current_round: round, recent_winners: [] })
    const snapshot = mount(LotteryView, { props: { snapshot: true }, global: { stubs } })
    await flushPromises()
    expect(getPublicSettings).not.toHaveBeenCalled()
    expect(snapshot.findComponent(CaptchaStub).exists()).toBe(false)
    snapshot.unmount()
  })

  it('removes the standalone slider component', () => {
    const slider = resolve(dirname(fileURLToPath(import.meta.url)), '../../../components/lottery/LotterySliderCaptcha.vue')
    expect(existsSync(slider)).toBe(false)
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

  it.each(['paused', 'cancelled'])('shows %s state and disables participation', async status => {
    getCurrent.mockResolvedValue({ enabled: true, current_round: { ...round, status }, joined: false,
      eligibility: { eligible: true }, recent_winners: [], my_recent_winners: [] })
    getRounds.mockResolvedValue({ items: [{ ...round, status }] })
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.text()).toContain(`lottery.statuses.${status}`)
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
  })

  it('loads the public endpoint and frames only the previous round without changing row spacing', async () => {
    const wrapper = mount(LotteryView, { props: { snapshot: true }, global: { stubs } })
    await flushPromises()

    expect(getAnnouncement).toHaveBeenCalledOnce()
    expect(getCurrent).not.toHaveBeenCalled()
    expect(wrapper.attributes('data-lottery-snapshot-ready')).toBe('true')
    const sections = wrapper.findAll('section')
    expect(sections).toHaveLength(4)
    expect(sections[0].classes()).toContain('lg:h-[26rem]')
    expect(sections[1].classes()).toContain('lg:h-[26rem]')
    expect(wrapper.find('[data-lottery-capture-region="true"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('winner-1@example.com')
    expect(wrapper.text()).toContain('winner-10@example.com')
    expect(wrapper.findAll('[data-lottery-previous-round="true"]')).toHaveLength(1)
    expect(wrapper.find('[data-lottery-previous-round="true"]').classes()).toEqual(expect.arrayContaining([
      'lottery-winner-group',
      'lottery-previous-round-frame',
    ]))
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

describe('LotteryView participation and displayed counts', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getRounds.mockResolvedValue({ items: [{ ...round, winner_count: 0 }], total: 1, pages: 1 })
  })

  it('disables participation after a draw even for an eligible user who has not joined', async () => {
    getCurrent.mockResolvedValue({ enabled: true, current_round: { ...round, status: 'drawn' }, joined: false, eligibility: { eligible: true }, recent_winners: [], my_recent_winners: [] })
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.text()).toContain('lottery.waitNextRound')
    expect(wrapper.find('button.btn-primary').attributes('disabled')).toBeDefined()
  })

  it('shows the configured reward and actual counts in history', async () => {
    getCurrent.mockResolvedValue({ enabled: true, current_round: { ...round, prize_amount: 7.5, prize_count: 4 }, joined: false, eligibility: { eligible: true }, recent_winners: [], my_recent_winners: [] })
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.text()).toContain('$7.50')
    expect(wrapper.find('button.btn-primary').attributes('disabled')).toBeUndefined()
    expect(wrapper.findAll('tbody td').map(cell => cell.text())).toEqual(['#3', '4', '0', 'lottery.statuses.open'])
  })

  it('keeps the completed round and both winner sections visible while waiting for the next round', async () => {
    getCurrent.mockResolvedValue({ enabled: true, current_round: { ...round, status: 'drawn', participant_count: 50 }, joined: true, eligibility: { eligible: false }, recent_winners: [winner(21, 3)], my_recent_winners: [winner(22, 3)] })
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    expect(wrapper.text()).toContain('#3')
    expect(wrapper.text()).toContain('$5.00')
    expect(wrapper.text()).toContain('50 / 50')
    expect(wrapper.text()).toContain('lottery.waitNextRound')
    expect(wrapper.text()).toContain('winner-21@example.com')
    expect(wrapper.find('[data-testid="my-winners-section"]').text()).toContain('lottery.rewardCredited')
    expect(wrapper.find('button.btn-primary').text()).toContain('lottery.alreadyJoined')
    expect(wrapper.find('button.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain('lottery.noRound')
  })
})

describe('LotteryView marks reminders read on viewing', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    auth.user = { id: 7 }
    markRead.mockResolvedValue(undefined)
    getRounds.mockResolvedValue({ items: [], total: 0, pages: 0 })
    getCurrent.mockResolvedValue({ enabled: true, current_round: round, joined: false, eligibility: { eligible: true }, recent_winners: [], my_recent_winners: [], current_round_unread: true, unread_winner_id: 0 })
  })

  it('acknowledges the round without participating and notifies the sidebar', async () => {
    const listener = vi.fn()
    window.addEventListener('lottery-availability-changed', listener)
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    expect(markRead).toHaveBeenCalledOnce()
    expect(markRead).toHaveBeenCalledWith(round.id, 0)
    expect(listener).toHaveBeenCalledOnce()
    expect(wrapper.find('button.btn-primary').attributes('disabled')).toBeUndefined()
    window.removeEventListener('lottery-availability-changed', listener)
    wrapper.unmount()
  })

  it('acknowledges only the latest prize returned by the viewed response', async () => {
    getCurrent.mockResolvedValue({ enabled: true, current_round: round, joined: true, eligibility: { eligible: true }, recent_winners: [], my_recent_winners: [winner(9)], current_round_unread: false, unread_winner_id: 9 })
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    expect(markRead).toHaveBeenCalledOnce()
    expect(markRead).toHaveBeenCalledWith(round.id, 9)
    expect(wrapper.find('[data-testid="my-winners-section"]').text()).toContain('lottery.rewardCredited')
    wrapper.unmount()
  })

  it('does not write read state for the public snapshot', async () => {
    getAnnouncement.mockResolvedValue({ enabled: true, current_round: round, recent_winners: [] })
    const wrapper = mount(LotteryView, { props: { snapshot: true }, global: { stubs } })
    await flushPromises()
    expect(markRead).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps reminders unread when saving the acknowledgement fails', async () => {
    markRead.mockRejectedValue(new Error('offline'))
    const listener = vi.fn()
    window.addEventListener('lottery-availability-changed', listener)
    const wrapper = mount(LotteryView, { global: { stubs } })
    await flushPromises()
    expect(showError).toHaveBeenCalledOnce()
    expect(listener).not.toHaveBeenCalled()
    window.removeEventListener('lottery-availability-changed', listener)
    wrapper.unmount()
  })

  it('ignores a previous account response and a page closed before loading', async () => {
    let resolve!: (value: object) => void
    getCurrent.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = mount(LotteryView, { global: { stubs } })
    auth.user = { id: 571 }
    resolve({ enabled: true, current_round: round, current_round_unread: true, unread_winner_id: 9 })
    await flushPromises()
    expect(markRead).not.toHaveBeenCalled()
    wrapper.unmount()
    getCurrent.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const closed = mount(LotteryView, { global: { stubs } })
    closed.unmount()
    resolve({ enabled: true, current_round: round, current_round_unread: true, unread_winner_id: 9 })
    await flushPromises()
    expect(markRead).not.toHaveBeenCalled()
  })
})
