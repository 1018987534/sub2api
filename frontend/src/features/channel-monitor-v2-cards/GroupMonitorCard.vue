<template>
 <article class="monitor-card glass-card flex min-h-[286px] flex-col rounded-[24px] p-5 text-left">
  <header class="flex items-start gap-3">
   <span class="platform-icon grid h-9 w-9 shrink-0 place-items-center rounded-xl text-gray-900 ring-1 ring-black/5 dark:text-gray-100 dark:ring-white/10" :class="platformIconClass(row.platform)"><PlatformIcon :platform="row.platform as GroupPlatform" size="lg" /></span>
   <div class="min-w-0 flex-1"><h3 class="truncate text-base font-semibold text-gray-900 dark:text-gray-100" :title="row.group_name">{{ row.group_name || '分组 #' + row.group_id }}</h3>
    <div class="mt-1 flex min-w-0 flex-wrap items-center gap-1.5 text-[10px] font-medium"><span class="platform-badge rounded-md px-1.5 py-0.5" :class="platformBadgeClass(row.platform)">{{ platformName(row.platform) }}</span><span class="rounded-md bg-primary-50 px-1.5 py-0.5 font-mono text-primary-700 dark:bg-dark-700 dark:text-gray-300">用户倍率 {{ groupMultiplier(row.current_multiplier) }}</span></div>
   </div>
   <span class="state-pill" :class="row.health.overall">{{ healthLabels[row.health.overall] }}</span>
  </header>
  <dl class="mt-5 grid grid-cols-3 gap-2">
   <div class="metric"><dt>缓存率</dt><dd>{{ percent(row.metrics.cache_rate, known) }}</dd></div>
   <div class="metric"><dt>可用率</dt><dd :class="{ 'text-emerald-500': row.health.overall === 'healthy' }">{{ percent(1 - row.metrics.error_rate, known) }}</dd></div>
   <div class="metric"><dt>首 TOKEN</dt><dd>{{ latency(row.metrics) }}</dd></div>
  </dl>
  <div class="passive-section mt-auto border-t border-white/70 pt-3 dark:border-dark-700/60">
   <div class="mb-2 flex justify-between text-[10px] font-semibold uppercase tracking-widest text-gray-400"><span>近 {{ buckets.length }} 次记录</span><span>{{ countdown }}S 后刷新</span></div>
   <div class="timeline-track passive-track" :style="{ gridTemplateColumns: `repeat(${timelineLength}, minmax(0, 1fr))` }" aria-label="被动用量历史">
    <div v-for="(bucket, index) in passiveSlots" :key="bucket?.bucket_start || 'empty-' + index" class="history-slot" @mouseenter="bucket && showBucket(bucket, index, $event)" @mouseleave="hideBucket">
     <span v-if="bucket" class="history-hitbox" role="img" tabindex="0" :aria-label="bucketTitle(bucket)" :aria-describedby="activeIndex === index ? tooltipId : undefined" @focus="showBucket(bucket, index, $event)" @blur="hideBucket" @keydown.esc="hideBucket">
      <span class="history-visual" :style="barStyle(index)" aria-hidden="true"><span class="history-bar" :class="passiveColor(bucket)" :style="{ height: barHeight(bucket) + '%' }" /></span>
     </span>
    </div>
   </div><span v-if="!buckets.length" class="sr-only">暂无用量记录</span>
   <div class="mt-1 flex justify-between text-[9px] uppercase tracking-widest text-gray-400"><span>PAST</span><span>NOW</span></div>

  </div>
  <section v-if="row.platform === 'openai' || records !== undefined" class="intelligence-section mt-4 border-t border-gray-200/70 pt-3 dark:border-dark-700/60" aria-label="降智状态">
   <div class="flex items-center justify-between gap-2 text-xs font-medium"><h4 class="font-semibold">降智状态</h4><span class="flex items-center gap-1.5"><i class="h-2 w-2 rounded-full" :class="last ? last.status : 'unknown'" />{{ last ? statusLabels[last.status] : '暂无检测' }}</span></div>
   <p class="intelligence-caption mt-1 text-[10px] leading-[15px] text-gray-500 dark:text-gray-400">{{ intelligenceCaption }}</p>
   <div class="timeline-track probe-track mt-2" aria-label="最近 60 次真实检测记录">
    <div v-for="(slot, index) in probeSlots" :key="index" class="probe-slot">
     <span v-for="record in slot" :key="record.id" role="img" tabindex="0" class="probe-bar" :class="record.status" :title="recordTitle(record)" :aria-label="recordTitle(record)" />
    </div>
   </div><span v-if="!recent.length" class="sr-only">暂无已完成检测</span>
   <div class="mt-1.5 flex justify-between text-[10px] text-gray-500 dark:text-gray-400"><span>{{ INTELLIGENCE_LEGEND }}</span><time v-if="last">{{ new Date(last.checked_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}</time></div>

  </section>
  <Teleport to="body">
   <Transition name="timeline-tooltip">
    <div v-if="tooltip" :id="tooltipId" class="timeline-tooltip" role="tooltip" :style="{ left: tooltip.left + 'px', top: tooltip.top + 'px', '--tooltip-x': tooltip.x }">{{ tooltip.text }}</div>
   </Transition>
  </Teleport>
 </article>
</template>
<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted }  from 'vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'
import type { MonitorMatrixRow, MonitorMatrixBucket } from '@/api/channelMonitorV2'
import type { IntelligenceRecord, IntelligenceMetadata } from '@/api/intelligence'
import { INTELLIGENCE_CAPTION, INTELLIGENCE_LEGEND } from './constants'
import { healthLabels, statusLabels, appendHistory, groupMultiplier, percent, latency, platformName, platformIconClass, platformBadgeClass, passiveColor, barHeight } from './presentation'
const props = withDefaults(defineProps<{ row: MonitorMatrixRow; records?: IntelligenceRecord[]; probeMetadata?: IntelligenceMetadata; now: number; countdown: number; timelineLength?: number }>(), { timelineLength: 18 })
const intelligenceCaption = computed(() => `${props.probeMetadata?.model || '—'} · ${props.probeMetadata?.reasoning_effort || '—'} · ${INTELLIGENCE_CAPTION}`)
const recent = ref<IntelligenceRecord[]>([])
watch(() => props.records, records => {
 recent.value = appendHistory(recent.value, (records || []).filter(record => Date.parse(record.checked_at) <= props.now), record => record.id, record => Date.parse(record.checked_at), 60)
}, { immediate: true, deep: true })
const last = computed(() => recent.value.at(-1))
const known = computed(() => props.row.health.overall !== 'unknown')
const buckets = computed(() => [...props.row.buckets].sort((a, b) => a.bucket_start.localeCompare(b.bucket_start)))
const activeIndex = ref<number | null>(null)
const tooltipId = `monitor-history-${props.row.platform}-${props.row.group_id}`
const tooltip = ref<{ text: string; left: number; top: number; x: string } | null>(null)
function showBucket(bucket: MonitorMatrixBucket, index: number, event: Event) {
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
// Empty cells are presentation placeholders, never successful samples.
const passiveSlots = computed(() => {
 const visible = buckets.value.slice(-props.timelineLength)
 return [...Array<null>(Math.max(0, props.timelineLength - visible.length)).fill(null), ...visible]
})
// Newest stays at the right edge; only a new record shifts existing marks left.
const probeSlots = computed(() => [...Array.from({ length: 60 - recent.value.length }, () => [] as IntelligenceRecord[]), ...recent.value.map(record => [record])])
const bucketTitle = (b: MonitorMatrixBucket) => new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(b.bucket_start)) + ' · 可用率 ' + percent(1 - b.metrics.error_rate, b.health.overall !== 'unknown') + ' · 缓存率 ' + percent(b.metrics.cache_rate, b.health.overall !== 'unknown') + ' · 首 Token ' + latency(b.metrics)
const recordTitle = (r: IntelligenceRecord) => new Date(r.checked_at).toLocaleString() + ' · ' + statusLabels[r.status] + ' · ' + (r.duration_ms / 1000).toFixed(1) + 's'
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
.passive-track{gap:4px}.probe-track{grid-template-columns:repeat(60,minmax(0,1fr));gap:1px}
.history-slot,.probe-slot{display:flex;align-items:flex-end;min-width:0;height:100%}.probe-slot{gap:1px}
.history-bar,.probe-bar{display:block;min-width:0;width:100%;border-radius:3px}.history-bar{min-height:3px}.probe-bar{flex:1;height:100%;border-radius:2px}
.history-hitbox:focus-visible,.probe-bar:focus-visible{outline:2px solid #3b82f6;outline-offset:2px}
.normal{background:#10b981}.degraded{background:#fbbf24}.error{background:#ef4444}.unknown:not(.state-pill){background:#9ca3af}
.normal:is(.dark *){background:#34d399}.error:is(.dark *){background:#f87171}
.history-hitbox,.history-visual{display:flex;align-items:flex-end;width:100%;height:100%;min-width:0}
.history-hitbox{cursor:crosshair;outline-offset:2px;border-radius:4px}.history-visual{transform-origin:center bottom;pointer-events:none;transition:transform .24s cubic-bezier(.22,1,.36,1),opacity .22s ease}
.history-hitbox[aria-describedby] .history-bar{filter:saturate(1.12) brightness(1.05);box-shadow:0 4px 10px #0f766e3d}
.probe-bar{cursor:default}
.timeline-tooltip{position:fixed;z-index:9999;width:max-content;max-width:min(280px,calc(100vw - 32px));transform:translate(var(--tooltip-x),-100%);border:1px solid rgb(255 255 255 / .84);border-radius:9px;background:#0f172aeb;padding:6px 9px;color:#f8fafc;font-size:10px;font-weight:600;line-height:1.35;box-shadow:0 10px 24px #0f172a33;pointer-events:none}
.timeline-tooltip:after{position:absolute;left:50%;bottom:-4px;width:7px;height:7px;transform:translateX(-50%) rotate(45deg);border-right:1px solid rgb(255 255 255 / .84);border-bottom:1px solid rgb(255 255 255 / .84);background:#0f172aeb;content:''}
.timeline-tooltip-enter-active,.timeline-tooltip-leave-active{transition:opacity .1s ease,transform .12s cubic-bezier(.22,1,.36,1)}
.timeline-tooltip-enter-from,.timeline-tooltip-leave-to{opacity:0;transform:translate(var(--tooltip-x),calc(-100% + 3px)) scale(.96)}
@media(prefers-reduced-motion:reduce){.history-visual,.timeline-tooltip-enter-active,.timeline-tooltip-leave-active{transition:none}}
</style>
