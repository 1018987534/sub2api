import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const viewPath = resolve(dirname(fileURLToPath(import.meta.url)), '../LotteryView.vue')
const viewSource = readFileSync(viewPath, 'utf8')

describe('LotteryView slider verification', () => {
  it('requires a fresh action captcha proof before joining', () => {
    expect(viewSource).toContain('captchaRef.value?.verifyAction()')
    expect(viewSource).toContain('tencent_captcha_ticket: proof.token')
    expect(viewSource).toContain('turnstile_token: proof.token')
    expect(viewSource).toContain("window.dispatchEvent(new Event('lottery-availability-changed'))")
  })

  it('does not submit when no slider provider is configured', () => {
    expect(viewSource).toContain('if (!sliderCaptchaConfigured.value)')
    expect(viewSource).toContain("t('lottery.captchaUnavailable')")
  })

  it('formats lottery rewards in the account USD currency', () => {
    expect(viewSource).toContain('return `$${Number(value || 0).toFixed(2)}`')
    expect(viewSource).not.toContain('coins')
  })
})
