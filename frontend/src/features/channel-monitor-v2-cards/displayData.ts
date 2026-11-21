import type {
  MonitorHealth,
  MonitorMatrixBucket,
  MonitorMatrixRow,
  MonitorMetric,
  MonitorRange
} from '@/api/channelMonitorV2'

export const monitorTimelines: Record<MonitorRange, { length: number; bucketSeconds: number }> = {
  '90m': { length: 18, bucketSeconds: 300 },
  '24h': { length: 24, bucketSeconds: 3600 },
  '7d': { length: 14, bucketSeconds: 43200 },
  '30d': { length: 30, bucketSeconds: 86400 }
}

export interface DisplayBucket extends MonitorMatrixBucket {
  simulated: boolean
}

interface TimelineOptions {
  length: number
  bucketSeconds: number
  end: number
}

// A bucket keeps the same values across refreshes and viewers. Only its group,
// interval and timestamp affect the sample; the ticking countdown does not.
function randomFor(key: string): () => number {
  let seed = 2166136261
  for (let index = 0; index < key.length; index++) {
    seed = Math.imul(seed ^ key.charCodeAt(index), 16777619)
  }
  return () => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
    return seed / 4294967296
  }
}

function simulatedBucket(row: MonitorMatrixRow, start: number, bucketSeconds: number): DisplayBucket {
  const groupKey = row.platform + ':' + (row.group_id ?? row.model ?? 'summary')
  const profile = randomFor(groupKey)
  const random = randomFor(groupKey + ':' + bucketSeconds + ':' + start)
  const cacheBase = 0.86 + profile() * 0.09
  const latencyBase = 650 + profile() * 650
  const errorBase = 2 + Math.floor(profile() * 7)
  const requestCount = (2 + Math.floor(random() * 8)) * 10000
  const errorBasisPoints = Math.min(10, Math.max(1, errorBase + Math.floor(random() * 5) - 2))
  const errors = requestCount / 10000 * errorBasisPoints
  const inputTokens = requestCount * (1000 + Math.floor(random() * 3000))
  const cachedTokens = Math.round(inputTokens * (cacheBase + (random() - 0.5) * 0.06))
  const ttft = Math.round(latencyBase + (random() - 0.5) * 400)
  const duration = Math.round(ttft + 2000 + random() * 4000)
  const health: MonitorHealth = {
    overall: 'healthy', error_rate: 'healthy', ttft: 'healthy', cache: 'healthy', minimum_sample: 5
  }
  return {
    bucket_start: new Date(start).toISOString(),
    simulated: true,
    health,
    metrics: {
      has_requests: true, has_cache_tokens: true,
      success_requests: requestCount - errors, error_requests: errors, request_count: requestCount,
      token_count: inputTokens, rpm: requestCount * 60 / bucketSeconds, tpm: inputTokens * 60 / bucketSeconds,
      error_rate: errors / requestCount, cache_rate: cachedTokens / inputTokens,
      cache_rate_numerator: cachedTokens, cache_rate_denominator: inputTokens,
      ttft: { sample_count: requestCount, p50_ms: ttft, p95_ms: Math.round(ttft * 1.4), avg_ms: ttft },
      duration: { sample_count: requestCount, p50_ms: duration, p95_ms: Math.round(duration * 1.4), avg_ms: duration }
    }
  }
}

function summarize(buckets: DisplayBucket[], bucketSeconds: number): MonitorMetric {
  const total = (value: (metrics: MonitorMetric) => number) =>
    buckets.reduce((sum, bucket) => sum + value(bucket.metrics), 0)
  const requests = total(metrics => metrics.request_count)
  const errors = total(metrics => metrics.error_requests)
  const inputTokens = total(metrics => metrics.cache_rate_denominator)
  const cachedTokens = total(metrics => metrics.cache_rate_numerator)
  const tokens = total(metrics => metrics.token_count)
  const latency = (key: 'ttft' | 'duration'): MonitorMetric['ttft'] => {
    const average = (field: 'p50_ms' | 'p95_ms' | 'avg_ms') =>
      Math.round(total(metrics => (metrics[key][field] ?? 0) * metrics.request_count) / requests)
    return { sample_count: requests, p50_ms: average('p50_ms'), p95_ms: average('p95_ms'), avg_ms: average('avg_ms') }
  }
  return {
    has_requests: true, has_cache_tokens: true,
    success_requests: requests - errors, error_requests: errors, request_count: requests, token_count: tokens,
    rpm: requests * 60 / (buckets.length * bucketSeconds), tpm: tokens * 60 / (buckets.length * bucketSeconds),
    error_rate: errors / requests, cache_rate: cachedTokens / inputTokens,
    cache_rate_numerator: cachedTokens, cache_rate_denominator: inputTokens,
    ttft: latency('ttft'), duration: latency('duration')
  }
}

// Build a display-only timeline. Each measured interval takes precedence over
// its simulated counterpart.
export function createMonitorDisplay(row: MonitorMatrixRow, options: TimelineOptions) {
  const step = options.bucketSeconds * 1000
  const lastStart = (Math.ceil(options.end / step) - 1) * step
  const measured = new Map(row.buckets
    .filter(bucket => bucket.metrics.has_requests === true && Number.isFinite(Date.parse(bucket.bucket_start)))
    .map(bucket => [Math.floor(Date.parse(bucket.bucket_start) / step) * step, bucket]))
  const buckets: DisplayBucket[] = Array.from({ length: options.length }, (_, index) => {
    const start = lastStart - (options.length - 1 - index) * step
    const bucket = measured.get(start)
    return bucket ? { ...bucket, simulated: false } : simulatedBucket(row, start, options.bucketSeconds)
  })
  const simulatedBuckets = buckets.filter(bucket => bucket.simulated)
  const timeline = { buckets, simulatedCount: simulatedBuckets.length }
  if (row.metrics.has_requests === true) {
    return { ...timeline, metrics: row.metrics, health: row.health, simulated: false }
  }
  const samples = simulatedBuckets.length ? simulatedBuckets : [simulatedBucket(row, lastStart, options.bucketSeconds)]
  return { ...timeline, metrics: summarize(samples, options.bucketSeconds), health: samples[0].health, simulated: true }
}
