<template>
 <article class="monitor-card glass-card flex min-h-[286px] flex-col rounded-[24px] p-5 text-left">
  <header class="flex items-start gap-3">
   <span class="platform-icon grid h-9 w-9 shrink-0 place-items-center rounded-xl text-gray-900 ring-1 ring-black/5 dark:text-gray-100 dark:ring-white/10" :class="platformIconClass(row.platform)"><PlatformIcon :platform="row.platform as GroupPlatform" size="lg" /></span>
   <div class="min-w-0 flex-1"><h3 class="truncate text-base font-semibold text-gray-900 dark:text-gray-100" :title="row.group_name">{{ row.group_name || '分组 #' + row.group_id }}</h3>
    <div class="mt-1 flex min-w-0 flex-wrap items-center gap-1.5 text-[10px] font-medium"><span class="platform-badge rounded-md px-1.5 py-0.5" :class="platformBadgeClass(row.platform)">{{ platformName(row.platform) }}</span><span class="rounded-md bg-primary-50 px-1.5 py-0.5 font-mono text-primary-700 dark:bg-dark-700 dark:text-gray-300">用户倍率 {{ groupMultiplier(row.current_multiplier) }}</span></div>
   </div>
   <span class="state-pill" :class="display.health.overall">{{ healthLabels[display.health.overall] }}</span>
  </header>
  <dl class="mt-5 grid grid-cols-3 gap-2">
   <div class="metric"><dt>缓存率</dt><dd>{{ cachePercent(display.metrics) }}</dd></div>
   <div class="metric"><dt>可用率</dt><dd :class="{ 'text-emerald-500': display.health.overall === 'healthy' }">{{ availabilityPercent(display.metrics, display.simulated ? 2 : 1) }}</dd></div>
   <div class="metric"><dt>首 TOKEN</dt><dd>{{ latency(display.metrics) }}</dd></div>
  </dl>
  <div class="passive-section mt-auto border-t border-white/70 pt-3 dark:border-dark-700/60">
   <div class="mb-2 flex justify-between text-[10px] font-semibold uppercase tracking-widest text-gray-400"><span>近 {{ display.buckets.length }} 个时间区间</span><span>{{ countdown }}S 后刷新</span></div>
   <div class="timeline-track passive-track" :style="{ gridTemplateColumns: `repeat(${timelineLength}, minmax(0, 1fr))` }" aria-label="用量历史">
    <div v-for="(bucket, index) in display.buckets" :key="bucket.bucket_start" class="history-slot" @mouseenter="showBucket(bucket, index, $event)" @mouseleave="hideBucket">
     <span class="history-hitbox" role="img" tabindex="0" :aria-label="bucketTitle(bucket)" :aria-describedby="activeIndex === index ? tooltipId : undefined" @focus="showBucket(bucket, index, $event)" @blur="hideBucket" @keydown.esc="hideBucket">
      <span class="history-visual" :style="barStyle(index)" aria-hidden="true"><span class="history-bar" :class="passiveColor(bucket)" :style="{ height: barHeight(bucket) + '%' }" /></span>
     </span>
    </div>
   </div>
   <div class="mt-1 flex justify-between text-[9px] uppercase tracking-widest text-gray-400"><span>PAST</span><span>NOW</span></div>

  </div>
  <section v-if="row.platform === 'openai' || records !== undefined || models?.length" class="intelligence-section mt-4 border-t border-gray-200/70 pt-3 dark:border-dark-700/60" aria-label="降智状态">
   <h4 class="mb-2 text-xs font-semibold">降智状态</h4>
   <div class="divide-y divide-gray-200/70 dark:divide-dark-700/60 [&>*+*]:mt-2 [&>*+*]:pt-2">
    <IntelligenceTimelineRow :key="'primary:' + (probeMetadata?.model ?? '')" :probe-id="0" :records="records" :metadata="probeMetadata" :now="now" @open="openRecord" />
    <IntelligenceTimelineRow v-for="model in models" :key="model.probe_id + ':' + model.model" :probe-id="model.probe_id" :records="model.records" :metadata="model" :now="now" @open="openRecord" />
   </div>
  </section>
  <Teleport to="body">
   <Transition name="timeline-tooltip">
    <div v-if="tooltip" :id="tooltipId" class="timeline-tooltip" role="tooltip" :style="{ left: tooltip.left + 'px', top: tooltip.top + 'px', '--tooltip-x': tooltip.x }">{{ tooltip.text }}</div>
   </Transition>
  </Teleport>
  <BaseDialog :show="selectedRecord !== null" title="降智检测回答" width="wide" :close-on-click-outside="true" @close="closeRecord">
   <p v-if="detailLoading" role="status" class="py-8 text-center text-sm text-gray-500">正在读取回答…</p>
   <div v-else-if="detailError" role="alert" class="space-y-3 py-6 text-center text-sm">
    <p class="text-red-600 dark:text-red-400">回答加载失败，请重试。</p>
    <button class="btn btn-secondary" @click="selectedRecord && openRecord(selectedRecord)">重试</button>
   </div>
   <div v-else-if="recordDetail" class="space-y-4 text-sm">
    <dl class="grid grid-cols-2 gap-3">
     <div><dt>检测结果</dt><dd class="!text-sm !font-medium">{{ statusLabels[recordDetail.status] }}</dd></div>
     <div><dt>模型</dt><dd class="break-words !text-sm !font-medium">{{ recordDetail.model || '—' }} · {{ recordDetail.reasoning_effort || '—' }}</dd></div>
    </dl>
    <section aria-label="完整回答">
     <h4 class="mb-2 font-semibold">完整回答</h4>
     <pre v-if="recordDetail.answer" class="max-h-[55vh] overflow-auto whitespace-pre-wrap break-words rounded-xl bg-gray-50 p-4 font-sans text-sm leading-relaxed text-gray-800 dark:bg-dark-900 dark:text-gray-200">{{ recordDetail.answer }}</pre>
     <p v-else class="rounded-xl bg-gray-50 p-4 text-gray-500 dark:bg-dark-900">本次检测未返回可展示的回答。</p>
    </section>
   </div>
  </BaseDialog>
 </article>
</template>
<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { GroupPlatform } from '@/types'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import { createMonitorDisplay, type DisplayBucket } from './displayData'
import { getIntelligenceRecordDetail, type IntelligenceRecord, type IntelligenceMetadata, type IntelligenceModelTimeline, type IntelligenceRecordDetail } from '@/api/intelligence'
import IntelligenceTimelineRow from './IntelligenceTimelineRow.vue'
import { healthLabels, statusLabels, groupMultiplier, availabilityPercent, cachePercent, latency, platformName, platformIconClass, platformBadgeClass, passiveColor, barHeight } from './presentation'
const props = withDefaults(defineProps<{ row: MonitorMatrixRow; records?: IntelligenceRecord[]; probeMetadata?: IntelligenceMetadata; models?: IntelligenceModelTimeline[]; now: number; countdown: number; timelineLength?: number; bucketSeconds?: number; windowEnd?: string }>(), { timelineLength: 18, bucketSeconds: 300 })
const selectedRecord = ref<IntelligenceRecord | null>(null)
const recordDetail = ref<IntelligenceRecordDetail | null>(null)
const detailLoading = ref(false), detailError = ref(false)
let detailAbort: AbortController | undefined
async function openRecord(record: IntelligenceRecord) {
 detailAbort?.abort()
 detailAbort = new AbortController()
 const signal = detailAbort.signal
 selectedRecord.value = record
 recordDetail.value = null
 detailLoading.value = true
 detailError.value = false
 try {
  const detail = await getIntelligenceRecordDetail(record.group_id, record.id, signal)
  if (!signal.aborted) recordDetail.value = detail
 } catch {
  if (!signal.aborted) detailError.value = true
 } finally {
  if (!signal.aborted) detailLoading.value = false
 }
}
function closeRecord() {
 detailAbort?.abort()
 selectedRecord.value = null
 recordDetail.value = null
 detailLoading.value = false
 detailError.value = false
}
onUnmounted(() => detailAbort?.abort())
const display = computed(() => {
 const requestedEnd = Date.parse(props.windowEnd ?? '')
 const step = props.bucketSeconds * 1000
 const end = Number.isFinite(requestedEnd) ? requestedEnd : (Math.floor(props.now / step) + 1) * step
 return createMonitorDisplay(props.row, { length: props.timelineLength, bucketSeconds: props.bucketSeconds, end })
})
const activeIndex = ref<number | null>(null)
const tooltipId = `monitor-history-${props.row.platform}-${props.row.group_id}`
const tooltip = ref<{ text: string; left: number; top: number; x: string } | null>(null)
function showBucket(bucket: DisplayBucket, index: number, event: Event) {
 const element = event.currentTarget
 if (!(element instanceof HTMLElement)) return
 const rect = element.getBoundingClientRect()
 const center = rect.left + rect.width / 2
 const width = Math.min(280, window.innerWidth - 32)
 const leftEdge = center - width / 2 < 16
 const rightEdge = center + width / 2 > window.innerWidth - 16
 activeIndex.value = index
 tooltip.value = { text: bucketTitle(bucket), left: leftEdge ? 16 : rightEdge ? window.innerWidth - 16 : center, top: rect.top - 8, x: leftEdge ? '0%' : rightEdge ? '-100%' : '-50%' }
}
function hideBucket() { activeIndex.value = null; tooltip.value = null }
function barStyle(index: number) {
 const distance = activeIndex.value === null ? null : Math.abs(index - activeIndex.value)
 const influence = distance === null ? 0 : Math.exp(-distance / 2.8)
 return { transform: `translateY(${distance === 0 ? -1 : 0}px) scaleY(${distance === 0 ? 1.1 : 1 - .06 * influence})`, opacity: distance === null || distance === 0 ? 1 : .8 + .2 * (1 - influence) }
}
onMounted(() => { window.addEventListener('scroll', hideBucket, true); window.addEventListener('resize', hideBucket) })
onUnmounted(() => { window.removeEventListener('scroll', hideBucket, true); window.removeEventListener('resize', hideBucket) })
const bucketTitle = (b: DisplayBucket) => new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(b.bucket_start)) + ' · 可用率 ' + availabilityPercent(b.metrics, b.simulated ? 2 : 1) + ' · 缓存率 ' + cachePercent(b.metrics) + ' · 首 Token ' + latency(b.metrics) + (b.health.overall === 'unknown' ? ' · 样本不足' : '')
</script>
<style scoped>
.monitor-card{min-width:0;align-self:start}
.metric{border:1px solid rgb(226 232 240 / .8);border-radius:16px;padding:12px;background:rgb(248 250 252 / .85);min-width:0}
.metric:is(.dark *){border-color:rgb(51 65 85 / .5);background:rgb(15 23 42 / .4)}
dt{font-size:10px;line-height:15px;font-weight:600;letter-spacing:.05em;color:#9ca3af}dd{margin-top:6px;font-size:18px;line-height:28px;font-weight:700;font-variant-numeric:tabular-nums;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}
.passive-section{margin-top:16px}
.monitor-card:not(:has(.intelligence-section)) .passive-section{margin-top:auto}
.state-pill{border-radius:999px;padding:4px 10px;font-size:12px;line-height:16px;font-weight:600;white-space:nowrap;flex-shrink:0}.state-pill.healthy{color:#047857;background:#d1fae5}.state-pill.warning{color:#b45309;background:#fef3c7}.state-pill.critical{color:#b91c1c;background:#fee2e2}.state-pill.unknown{color:#4b5563;background:#f3f4f6}
.state-pill.healthy:is(.dark *){color:#6ee7b7;background:#10b98126}.state-pill.warning:is(.dark *){color:#fcd34d;background:#f59e0b26}.state-pill.critical:is(.dark *){color:#fca5a5;background:#ef444426}.state-pill.unknown:is(.dark *){color:#d1d5db;background:#334155}
.timeline-track{display:grid;position:relative;height:20px;width:100%;isolation:isolate}
.passive-track{gap:4px}
.history-slot{display:flex;align-items:flex-end;min-width:0;height:100%}
.history-bar{display:block;min-width:0;width:100%;border-radius:3px;min-height:3px}
.history-hitbox:focus-visible{outline:2px solid #3b82f6;outline-offset:2px}
.history-hitbox,.history-visual{display:flex;align-items:flex-end;width:100%;height:100%;min-width:0}
.history-hitbox{cursor:crosshair;outline-offset:2px;border-radius:4px}.history-visual{transform-origin:center bottom;pointer-events:none;transition:transform .24s cubic-bezier(.22,1,.36,1),opacity .22s ease}
.history-hitbox[aria-describedby] .history-bar{filter:saturate(1.12) brightness(1.05);box-shadow:0 4px 10px #0f766e3d}
.timeline-tooltip{position:fixed;z-index:9999;width:max-content;max-width:min(280px,calc(100vw - 32px));transform:translate(var(--tooltip-x),-100%);border:1px solid rgb(255 255 255 / .84);border-radius:9px;background:#0f172aeb;padding:6px 9px;color:#f8fafc;font-size:10px;font-weight:600;line-height:1.35;box-shadow:0 10px 24px #0f172a33;pointer-events:none}
.timeline-tooltip:after{position:absolute;left:50%;bottom:-4px;width:7px;height:7px;transform:translateX(-50%) rotate(45deg);border-right:1px solid rgb(255 255 255 / .84);border-bottom:1px solid rgb(255 255 255 / .84);background:#0f172aeb;content:''}
.timeline-tooltip-enter-active,.timeline-tooltip-leave-active{transition:opacity .1s ease,transform .12s cubic-bezier(.22,1,.36,1)}
.timeline-tooltip-enter-from,.timeline-tooltip-leave-to{opacity:0;transform:translate(var(--tooltip-x),calc(-100% + 3px)) scale(.96)}
@media(prefers-reduced-motion:reduce){.history-visual,.timeline-tooltip-enter-active,.timeline-tooltip-leave-active{transition:none}}
</style>
