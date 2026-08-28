import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

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
