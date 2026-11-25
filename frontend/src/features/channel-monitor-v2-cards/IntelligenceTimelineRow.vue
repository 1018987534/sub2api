<template>
 <div class="intelligence-model-row min-w-0" :data-probe-id="probeId" :aria-label="metadata?.model">
  <div class="flex items-start justify-between gap-2 text-xs font-medium">
   <p class="intelligence-caption min-w-0 break-words text-[10px] leading-[15px] text-gray-500 dark:text-gray-400">{{ caption }}</p>
   <span class="flex shrink-0 items-center gap-1.5 whitespace-nowrap"><i class="probe-dot h-2 w-2 rounded-full" :class="last?.status ?? 'unknown'" />{{ last ? statusLabels[last.status] : '暂无检测' }}</span>
  </div>
  <div class="probe-track mt-2 grid h-5 w-full" :aria-label="(metadata?.model ? metadata.model + ' · ' : '') + '最近 60 次真实检测记录'">
   <div v-for="(record, index) in slots" :key="index" class="probe-slot">
    <button v-if="record" :key="record.id" type="button" class="probe-bar" :class="record.status" :data-record-id="record.id" :title="recordTitle(record)" :aria-label="recordTitle(record)" @click="$emit('open', record)" />
   </div>
  </div>
  <span v-if="!recent.length" class="sr-only">暂无已完成检测</span>
  <div class="mt-1.5 flex justify-between text-[10px] text-gray-500 dark:text-gray-400">
   <span>{{ INTELLIGENCE_LEGEND }}</span>
   <time v-if="last" :datetime="last.checked_at">{{ new Date(last.checked_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }}</time>
  </div>
 </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { IntelligenceMetadata, IntelligenceRecord } from '@/api/intelligence'
import { formatIntelligenceCaption, INTELLIGENCE_LEGEND } from './constants'
import { appendHistory, statusLabels } from './presentation'

const props = defineProps<{ records?: IntelligenceRecord[]; metadata?: IntelligenceMetadata; now: number; probeId: number }>()
defineEmits<{ open: [record: IntelligenceRecord] }>()
const caption = computed(() => formatIntelligenceCaption(props.metadata))
const recent = ref<IntelligenceRecord[]>([])
watch(() => props.records, records => {
 const cutoff = props.now - 7 * 24 * 60 * 60 * 1000
 const valid = (record: IntelligenceRecord) => {
  const time = Date.parse(record.checked_at)
  return time >= cutoff && time <= props.now
 }
 recent.value = appendHistory(recent.value.filter(valid), (records || []).filter(valid), record => record.id, record => Date.parse(record.checked_at), 60)
}, { immediate: true })
const last = computed(() => recent.value.at(-1))
const slots = computed(() => [...Array<undefined>(60 - recent.value.length).fill(undefined), ...recent.value])
function recordTitle(record: IntelligenceRecord) {
 const model = props.metadata?.model
 return (model ? model + ' · ' : '') + new Date(record.checked_at).toLocaleString() + ' · ' + statusLabels[record.status] + ' · ' + (record.duration_ms / 1000).toFixed(1) + 's · 查看回答'
}
</script>

<style scoped>
.probe-track{grid-template-columns:repeat(60,minmax(0,1fr));gap:1px}
.probe-slot{min-width:0;height:100%}
.probe-bar{display:block;width:100%;height:100%;border:0;border-radius:2px;padding:0;cursor:pointer;transition:filter .15s}
.probe-bar:hover{filter:brightness(1.15)}
.probe-bar:focus-visible{outline:2px solid #3b82f6;outline-offset:2px}
.normal{background:#10b981}.degraded{background:#fbbf24}.error{background:#ef4444}.unknown{background:#9ca3af}
.normal:is(.dark *){background:#34d399}.error:is(.dark *){background:#f87171}
</style>
