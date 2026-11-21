import { describe, expect, it } from 'vitest'
import type { MonitorMatrixRow, MonitorMetric, MonitorRange } from '@/api/channelMonitorV2'
import { createMonitorDisplay, monitorTimelines } from '../displayData'

const end = Date.parse('2026-11-21T10:05:00Z')
const emptyMetrics: MonitorMetric = {
  has_requests: false, has_cache_tokens: false,
  success_requests: 0, error_requests: 0, request_count: 0, token_count: 0, rpm: 0, tpm: 0,
  error_rate: 0, cache_rate: 0, cache_rate_numerator: 0, cache_rate_denominator: 0,
  ttft: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
  duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null }
}
const emptyRow: MonitorMatrixRow = {
  platform: 'anthropic', group_id: 12, group_name: 'KIRO特价', metrics: emptyMetrics,
  health: { overall: 'unknown', error_rate: 'unknown', ttft: 'unknown', minimum_sample: 5 }, buckets: []
}
const display = (row = emptyRow, range: MonitorRange = '90m', time = end) =>
  createMonitorDisplay(row, { ...monitorTimelines[range], end: time })

describe('simulated empty monitor intervals', () => {
  it.each<MonitorRange>(['90m', '24h', '7d', '30d'])('fills the entire %s range with excellent, varied metrics', range => {
    const result = display(emptyRow, range)
    const { length, bucketSeconds } = monitorTimelines[range]
    expect(result.simulated).toBe(true)
    expect(result.health.overall).toBe('healthy')
    expect(result.buckets).toHaveLength(length)
    for (const bucket of result.buckets) {
      expect(bucket.simulated).toBe(true)
      expect(bucket.health.overall).toBe('healthy')
      expect(bucket.metrics.error_rate).toBeGreaterThan(0)
      expect(bucket.metrics.error_rate).toBeLessThanOrEqual(0.001)
      expect(bucket.metrics.cache_rate).toBeGreaterThanOrEqual(0.83)
      expect(bucket.metrics.cache_rate).toBeLessThan(0.98)
      expect(bucket.metrics.ttft.p50_ms).toBeGreaterThanOrEqual(450)
      expect(bucket.metrics.ttft.p50_ms).toBeLessThanOrEqual(1500)
      expect(Date.parse(bucket.bucket_start)).toBeLessThan(end)
    }
    expect(new Set(result.buckets.map(bucket => bucket.metrics.ttft.p50_ms)).size).toBeGreaterThan(5)
    const times = result.buckets.map(bucket => Date.parse(bucket.bucket_start))
    expect(times.every((time, index) => !index || time - times[index - 1] === bucketSeconds * 1000)).toBe(true)
    expect(result.metrics.error_rate).toBeLessThanOrEqual(0.001)
    expect(result.metrics.cache_rate).toBeGreaterThanOrEqual(0.83)
  })

  it('keeps values stable across countdown ticks, viewers and overlapping refreshes', () => {
    const first = display()
    expect(display()).toEqual(first)
    const sameInterval = display(emptyRow, '90m', end - 1000)
    expect(sameInterval).toEqual(first)
    const next = display(emptyRow, '90m', end + 300000)
    expect(next.buckets.slice(0, -1)).toEqual(first.buckets.slice(1))
    expect(next.buckets.at(-1)).not.toEqual(first.buckets.at(-1))
    expect(display({ ...emptyRow, group_id: 13 }).metrics).not.toEqual(first.metrics)
    expect(display({ ...emptyRow, platform: 'gemini' }).metrics).not.toEqual(first.metrics)
    const availabilities = Array.from({ length: 8 }, (_, group_id) => (100 * (1 - display({ ...emptyRow, group_id }).metrics.error_rate)).toFixed(2))
    expect(new Set(availabilities).size).toBeGreaterThan(2)
  })

  it('fills explicit no-request buckets without mutating the response or fabricating input history', () => {
    const bucket = { bucket_start: new Date(end - 300000).toISOString(), metrics: emptyMetrics, health: emptyRow.health }
    const row = { ...emptyRow, buckets: [bucket] }
    const original = JSON.stringify(row)
    expect(display(row).buckets.at(-1)?.simulated).toBe(true)
    expect(JSON.stringify(row)).toBe(original)
    expect(row.metrics.has_requests).toBe(false)
    expect(row.buckets).toHaveLength(1)
  })

  it('preserves redacted real failures, zero cache hits and missing latency while filling gaps', () => {
    const metrics = { ...emptyMetrics, has_requests: true, has_cache_tokens: true, error_rate: 1, cache_rate: 0 }
    const health = { ...emptyRow.health, overall: 'critical' as const, error_rate: 'critical' as const }
    const start = end - 900000
    const real = { bucket_start: new Date(start).toISOString(), metrics, health }
    const result = display({ ...emptyRow, metrics, health, buckets: [real] })
    expect(result.simulated).toBe(false)
    expect(result.metrics).toBe(metrics)
    expect(result.health).toBe(health)
    expect(result.buckets.filter(bucket => bucket.simulated)).toHaveLength(17)
    expect(result.buckets.at(-3)).toEqual({ ...real, simulated: false })
    expect(result.buckets.at(-2)?.simulated).toBe(true)
    expect(result.buckets.at(-1)?.simulated).toBe(true)
    expect(result.buckets.find(bucket => !bucket.simulated)?.health.overall).toBe('critical')
  })

  it('replaces only the simulated interval when actual traffic arrives', () => {
    const first = display()
    const synthetic = first.buckets.at(-2)!
    const metrics = { ...emptyMetrics, has_requests: true, has_cache_tokens: false, error_rate: 0.1 }
    const measured = { bucket_start: synthetic.bucket_start, metrics, health: emptyRow.health }
    const next = display({ ...emptyRow, metrics, buckets: [measured] })
    expect(next.simulated).toBe(false)
    expect(next.buckets.at(-2)).toEqual({ ...measured, simulated: false })
    expect(next.buckets.filter(bucket => bucket.simulated)).toEqual(first.buckets.filter(bucket => bucket.bucket_start !== synthetic.bucket_start))
  })

  it('retains a complete measured timeline without a simulation marker', () => {
    const metrics = { ...emptyMetrics, has_requests: true }
    const buckets = Array.from({ length: 18 }, (_, index) => ({
      bucket_start: new Date(end - (18 - index) * 300000).toISOString(), metrics, health: emptyRow.health
    }))
    const result = display({ ...emptyRow, metrics, buckets })
    expect(result.simulated).toBe(false)
    expect(result.buckets.every(bucket => !bucket.simulated)).toBe(true)
    expect(result.buckets.map(({ simulated: _simulated, ...bucket }) => bucket)).toEqual(buckets)
  })

  it('does not reuse measured samples outside the selected range or with invalid timestamps', () => {
    const metrics = { ...emptyMetrics, has_requests: true }
    const buckets = [new Date(end - 86400000).toISOString(), new Date(end).toISOString(), 'invalid'].map(bucket_start => ({
      bucket_start, metrics, health: emptyRow.health
    }))
    expect(display({ ...emptyRow, buckets }).buckets.filter(bucket => bucket.simulated)).toHaveLength(18)
  })
})
