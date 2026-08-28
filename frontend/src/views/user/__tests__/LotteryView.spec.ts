import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const viewPath = resolve(dirname(fileURLToPath(import.meta.url)), '../LotteryView.vue')
const viewSource = readFileSync(viewPath, 'utf8')
const sliderPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../components/lottery/LotterySliderCaptcha.vue')
const sliderSource = readFileSync(sliderPath, 'utf8')

describe('LotteryView slider verification', () => {
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

  it('formats lottery rewards in the account USD currency', () => {
    expect(viewSource).toContain('return `$${Number(value || 0).toFixed(2)}`')
    expect(viewSource).not.toContain('coins')
  })
})
