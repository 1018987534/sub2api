<template>
 <article class="monitor-card card rounded-3xl p-5 sm:p-6">
  <header class="flex items-start gap-3">
   <span class="rounded-2xl bg-emerald-500/10 p-3 text-emerald-600 dark:text-emerald-300"><PlatformIcon :platform="row.platform as GroupPlatform" size="lg" /></span>
   <div class="min-w-0 flex-1"><h3 class="truncate text-base font-bold text-gray-900 dark:text-gray-100" :title="row.group_name">{{ row.group_name || '分组 #' + row.group_id }}</h3>
    <div class="mt-1 flex flex-wrap gap-1.5 text-xs"><span class="rounded-md bg-emerald-500/10 px-1.5 py-0.5 text-emerald-600 dark:text-emerald-300">{{ platformName(row.platform) }}</span><span v-if="row.current_multiplier != null" class="rounded-md bg-gray-100 px-1.5 py-0.5 dark:bg-dark-600">用户倍率 {{ row.current_multiplier.toFixed(2) }}x</span></div>
   </div>
   <span class="state-pill" :class="row.health.overall">{{ healthLabels[row.health.overall] }}</span>
  </header>
  <dl class="mt-6 grid grid-cols-3 gap-2">
   <div class="metric"><dt>缓存率</dt><dd>{{ percent(row.metrics.cache_rate, known) }}</dd></div>
   <div class="metric"><dt>可用率</dt><dd :class="{ 'text-emerald-500': row.health.overall === 'healthy' }">{{ percent(1 - row.metrics.error_rate, known) }}</dd></div>
   <div class="metric"><dt>首 TOKEN</dt><dd>{{ latency(row.metrics) }}</dd></div>
  </dl>
  <div class="mt-5 border-t border-gray-200 pt-4 dark:border-dark-600">
   <div class="mb-2 flex justify-between text-xs text-gray-500 dark:text-gray-400"><span>近 {{ buckets.length }} 个时间桶</span><span>{{ countdown }}S 后刷新</span></div>
   <div v-if="buckets.length" class="flex h-7 items-end gap-1" aria-label="被动用量历史">
    <button v-for="bucket in buckets" :key="bucket.bucket_start" type="button" class="history-bar" :class="bucket.health.overall" :style="{ height: barHeight(bucket) + '%' }" :title="bucketTitle(bucket)" :aria-label="bucketTitle(bucket)" @click="selectedBucket = bucket" />
   </div><p v-else class="text-xs text-gray-400">暂无用量记录</p>
   <div class="mt-2 flex justify-between text-[10px] tracking-widest text-gray-400"><span>PAST</span><span>NOW</span></div>
   <div v-if="selectedBucket" class="mt-2 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900" role="status"><button class="float-right px-1" aria-label="关闭时间桶详情" @click="selectedBucket = null">×</button>{{ bucketTitle(selectedBucket) }}</div>
  </div>
  <section v-if="records !== undefined" class="mt-5 border-t border-gray-200 pt-4 dark:border-dark-600" aria-label="降智状态">
   <div class="flex items-center justify-between gap-2 text-sm font-medium"><h4>降智状态</h4><span class="flex items-center gap-1.5"><i class="h-2 w-2 rounded-full" :class="last ? last.status : 'unknown'" />{{ last ? statusLabels[last.status] : '暂无检测' }}</span></div>
   <p class="my-2 text-[11px] leading-relaxed text-gray-500 dark:text-gray-400">{{ INTELLIGENCE_CAPTION }}</p>
   <div v-if="recent.length" class="flex h-6 gap-[2px]" aria-label="近 60 分钟真实检测记录">
    <button v-for="record in recent" :key="record.id" type="button" class="probe-bar" :class="record.status" :title="recordTitle(record)" :aria-label="recordTitle(record)" @click="selectedRecord = record" />
   </div><p v-else class="rounded-lg bg-gray-50 p-2 text-xs text-gray-400 dark:bg-dark-900">近 60 分钟没有已完成检测</p>
   <div class="mt-2 flex justify-between text-[10px] text-gray-500 dark:text-gray-400"><span>正常绿 · 其他黄 · 错误红</span><time v-if="last">{{ new Date(last.checked_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}</time></div>
   <p v-if="selectedRecord && recent.some(r => r.id === selectedRecord?.id)" class="mt-2 text-xs" role="status">{{ recordTitle(selectedRecord) }}<button class="ml-2" aria-label="关闭检测详情" @click="selectedRecord = null">×</button></p>
  </section>
 </article>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'
import type { MonitorMatrixRow, MonitorMatrixBucket } from '@/api/channelMonitorV2'
import type { IntelligenceRecord } from '@/api/intelligence'
import { INTELLIGENCE_CAPTION } from './constants'
import { healthLabels, statusLabels, recentRecords, percent, latency, platformName } from './presentation'
const props = defineProps<{ row: MonitorMatrixRow; records?: IntelligenceRecord[]; now: number; countdown: number }>()
const recent = computed(() => recentRecords(props.records || [], props.now))
const last = computed(() => recent.value.at(-1))
const known = computed(() => props.row.health.overall !== 'unknown')
const buckets = computed(() => [...props.row.buckets].sort((a, b) => a.bucket_start.localeCompare(b.bucket_start)))
const selectedBucket = ref<MonitorMatrixBucket | null>(null)
const selectedRecord = ref<IntelligenceRecord | null>(null)
const barHeight = (bucket: MonitorMatrixBucket) => bucket.health.overall === 'unknown' ? 15 : Math.max(20, Math.min(100, (1 - bucket.metrics.error_rate) * 100))
const bucketTitle = (b: MonitorMatrixBucket) => new Date(b.bucket_start).toLocaleString() + ' · ' + healthLabels[b.health.overall] + ' · 可用率 ' + percent(1 - b.metrics.error_rate, b.health.overall !== 'unknown') + ' · 缓存率 ' + percent(b.metrics.cache_rate, b.health.overall !== 'unknown') + ' · 首 Token ' + latency(b.metrics)
const recordTitle = (r: IntelligenceRecord) => new Date(r.checked_at).toLocaleString() + ' · ' + statusLabels[r.status] + ' · ' + (r.duration_ms / 1000).toFixed(1) + 's'
</script>
<style scoped>
.monitor-card{border:1px solid rgb(148 163 184 / .18);box-shadow:0 8px 30px rgb(15 23 42 / .03)}
.metric{border:1px solid rgb(148 163 184 / .16);border-radius:18px;padding:14px 10px;background:rgb(148 163 184 / .04)}
dt{font-size:11px;letter-spacing:.04em;color:#94a3b8}dd{margin-top:10px;font-size:20px;font-weight:700;font-variant-numeric:tabular-nums;font-family:ui-monospace,monospace}
.state-pill{border-radius:999px;padding:4px 10px;font-size:11px;white-space:nowrap}.state-pill.healthy{color:#059669;background:#10b98118}.state-pill.warning{color:#ca8a04;background:#eab30818}.state-pill.critical{color:#ef4444;background:#ef444418}.state-pill.unknown{color:#94a3b8;background:#94a3b818}
.history-bar,.probe-bar{flex:1;min-width:2px;border-radius:3px}.history-bar:focus-visible,.probe-bar:focus-visible{outline:2px solid #3b82f6;outline-offset:2px}.history-bar:hover,.probe-bar:hover{filter:brightness(1.15)}.history-bar.healthy,.normal{background:#2dcca0}.history-bar.warning,.degraded{background:#fbbf24}.history-bar.critical,.error{background:#f87171}.history-bar.unknown,.unknown:not(.state-pill){background:#94a3b8}
</style>
