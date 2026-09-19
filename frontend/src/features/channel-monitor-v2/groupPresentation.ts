import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import type { Group } from '@/types'

export interface MonitorGroupPresentation {
  orderByID: Map<number, number>
  multiplierByID: Map<number, number>
}

export function buildMonitorGroupPresentation(
  groups: Group[],
  userRates: Record<number, number>,
): MonitorGroupPresentation {
  return {
    orderByID: new Map(groups.map((group, index) => [group.id, index])),
    multiplierByID: new Map(
      groups.map((group) => [group.id, userRates[group.id] ?? group.rate_multiplier]),
    ),
  }
}

export function decorateAndSortMonitorRows(
  rows: MonitorMatrixRow[],
  presentation: MonitorGroupPresentation,
): MonitorMatrixRow[] {
  return rows
    .map((row, originalIndex) => ({
      row: row.group_id != null
        ? { ...row, current_multiplier: presentation.multiplierByID.get(Number(row.group_id)) }
        : row,
      originalIndex,
    }))
    .sort((left, right) => {
      const leftID = Number(left.row.group_id || 0)
      const rightID = Number(right.row.group_id || 0)
      const leftOrder = presentation.orderByID.get(leftID) ?? Number.MAX_SAFE_INTEGER
      const rightOrder = presentation.orderByID.get(rightID) ?? Number.MAX_SAFE_INTEGER
      if (leftOrder !== rightOrder) return leftOrder - rightOrder
      return left.originalIndex - right.originalIndex
    })
    .map(({ row }) => row)
}
