<template>
 <AppLayout>
  <div class="space-y-7 pb-12">
   <section class="card overflow-hidden !rounded-3xl">
    <header class="flex items-center justify-between gap-4 p-6"><div><h1 class="text-xl font-bold">渠道状态 V2 · 卡片</h1><p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ snapshot ? '更新至 ' + new Date(snapshot.coverage.data_through).toLocaleString() : '正在读取监控数据' }}</p></div><button class="btn btn-secondary" :disabled="loading || !enabled" aria-label="刷新渠道状态" @click="load">{{ loading ? '刷新中…' : '刷新' }}</button></header>
    <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-200 px-6 py-3 text-xs dark:border-dark-600"><div class="flex flex-wrap items-center gap-2"><button v-for="period in periods" :key="period" class="rounded-lg px-3 py-1.5 font-semibold" :class="range === period ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300' : 'text-gray-500'" :aria-pressed="range === period" @click="range = period">{{ period }}</button><span class="border-l border-gray-300 pl-3 text-gray-500 dark:border-dark-600">V2 被动用量 · 缓存率与可用率</span></div><span v-if="snapshot" class="text-gray-500 dark:text-gray-400">可用率 {{ percent(1 - snapshot.metrics.error_rate, snapshot.health.overall !== 'unknown') }} · 缓存率 {{ percent(snapshot.metrics.cache_rate, snapshot.health.overall !== 'unknown') }}</span></div>
   </section>
   <p v-if="!enabled" class="card p-6">请先在系统配置中启用渠道监控 V2。官方监控页面保持不变。</p>
   <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}；已保留上次成功数据，请刷新重试。</p>
   <p v-if="probeError" role="alert" class="rounded-xl bg-amber-50 p-4 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">降智检测状态暂时无法更新，未把失败伪装成正常。</p>
   <p v-if="snapshot && !snapshot.coverage.coverage_complete" class="text-xs text-amber-600">当前时间范围数据尚未聚合完整，指标仅反映已覆盖区间。</p>
   <section v-for="section in sections" :key="section.platform" class="space-y-4"><h2 class="flex items-center gap-3 text-base font-semibold"><span class="rounded-full bg-emerald-500/10 p-2 text-emerald-600"><PlatformIcon :platform="section.platform as GroupPlatform" size="md" /></span>{{ platformName(section.platform) }}<span class="rounded-full bg-gray-200 px-2 py-0.5 text-xs text-gray-500 dark:bg-dark-600">{{ section.rows.length }}</span></h2><div class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4"><GroupMonitorCard v-for="row in section.rows" :key="row.platform + ':' + row.group_id" :row="row" :records="probes[String(row.group_id)]" :now="now + serverOffset" :countdown="countdown" /></div></section>
   <div v-if="enabled && !loading && !error && !sections.length" class="card p-10 text-center text-gray-500">当前没有可展示的分组数据</div>
  </div>
 </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { GroupPlatform } from '@/types'
import { getMatrix, getSnapshot, type MonitorRange, type MonitorMatrixRow, type MonitorSnapshot } from '@/api/channelMonitorV2'
import { getIntelligenceStatus, type IntelligenceRecord } from '@/api/intelligence'
import { isChannelMonitorV2Mode } from '@/utils/featureFlags'
import GroupMonitorCard from './GroupMonitorCard.vue'
import { percent, platformGroups, platformName } from './presentation'
const periods: MonitorRange[] = ['90m', '24h', '7d', '30d']
const range = ref<MonitorRange>('90m')
const rows = ref<MonitorMatrixRow[]>([])
const snapshot = ref<MonitorSnapshot | null>(null)
const probes = ref<Record<string, IntelligenceRecord[]>>({})
const loading = ref(false), error = ref(''), probeError = ref(false)
const now = ref(Date.now()), serverOffset = ref(0), nextRefresh = ref(Date.now() + 60000)
const enabled = computed(() => isChannelMonitorV2Mode())
const countdown = computed(() => Math.max(0, Math.ceil((nextRefresh.value - now.value) / 1000)))
const sections = computed(() => platformGroups(rows.value))
let abort: AbortController | undefined, timer: ReturnType<typeof setInterval> | undefined
let generation = 0
async function load() {
 const current = ++generation
 abort?.abort(); abort = new AbortController()
 const signal = abort.signal
 if (!enabled.value) { loading.value = false; rows.value = []; probes.value = {}; snapshot.value = null; error.value = ''; probeError.value = false; return }
 loading.value = true; error.value = ''
 const filter = { range: range.value, platforms: [], groupIds: [], models: [] }
 try {
  const [matrix, snap] = await Promise.all([getMatrix(filter, 'platform_group', false, signal), getSnapshot(filter, false, signal)])
  if (current !== generation) return
  rows.value = matrix.items; snapshot.value = snap
  const ids = [...new Set(matrix.items.map(row => row.group_id).filter((id): id is number => !!id))]
  try {
   const results = await Promise.all(Array.from({ length: Math.ceil(ids.length / 100) }, (_, i) => getIntelligenceStatus(ids.slice(i * 100, (i + 1) * 100), signal)))
   if (current !== generation) return
   probes.value = Object.assign({}, ...results.map(result => result.groups))
   if (results.length) serverOffset.value = Date.parse(results[0].server_time) - Date.now()
   probeError.value = false
  } catch { if (!signal.aborted) probeError.value = true }
 } catch { if (!signal.aborted) error.value = '监控数据加载失败' }
 finally { if (current === generation) { loading.value = false; nextRefresh.value = Date.now() + (snapshot.value?.config.refresh_interval_seconds || 60) * 1000 } }
}
watch([range, enabled], () => { void load() })
onMounted(() => { void load(); timer = setInterval(() => { now.value = Date.now(); if (!loading.value && enabled.value && now.value >= nextRefresh.value) void load() }, 1000) })
onUnmounted(() => { generation++; abort?.abort(); if (timer) clearInterval(timer) })
</script>
