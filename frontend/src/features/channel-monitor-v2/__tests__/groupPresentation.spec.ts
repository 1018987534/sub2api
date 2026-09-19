import { describe, expect, it } from 'vitest'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import type { Group } from '@/types'
import {
  buildMonitorGroupPresentation,
  decorateAndSortMonitorRows,
} from '../groupPresentation'

const groups = [
  { id: 20, name: 'PLUS', rate_multiplier: 0.2 },
  { id: 10, name: 'PRO', rate_multiplier: 1 },
] as Group[]

const rows = [
  { platform: 'openai', group_id: 10, group_name: 'PRO' },
  { platform: 'openai', group_id: 99, group_name: 'Legacy' },
  { platform: 'openai', group_id: 20, group_name: 'PLUS' },
] as MonitorMatrixRow[]

describe('monitor group presentation', () => {
  it('uses the API-key group order and the current user multiplier', () => {
    const presentation = buildMonitorGroupPresentation(groups, { 20: 0.08 })
    const result = decorateAndSortMonitorRows(rows, presentation)

    expect(result.map((row) => row.group_id)).toEqual([20, 10, 99])
    expect(result.map((row) => row.current_multiplier)).toEqual([0.08, 1, undefined])
  })
})
