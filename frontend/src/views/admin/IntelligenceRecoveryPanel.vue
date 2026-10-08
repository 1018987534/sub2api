<template>
  <section class="recovery-panel" aria-labelledby="recovery-heading">
    <header class="recovery-heading">
      <div><h2 id="recovery-heading">账号恢复检测</h2><span class="muted">{{ serverTime ? '更新于 ' + formatTime(serverTime) : '' }}</span></div>
      <button type="button" class="btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="load()"><Icon name="refresh" size="sm" />刷新</button>
    </header>
    <p v-if="error" role="alert" class="recovery-error">{{ error }}</p>
    <h3>当前调度 <span class="muted">{{ queue.length }}</span></h3>
    <div class="recovery-table-scroll">
      <table aria-label="恢复检测调度"><thead><tr><th>被测账号</th><th>状态</th><th>最近检测</th><th>下一次检测</th><th>重试间隔</th></tr></thead>
        <tbody><tr v-for="entry in queue" :key="entry.account_id">
          <td class="account-cell">{{ entry.account_name }} <span class="muted">#{{ entry.account_id }}</span></td>
          <td>{{ entry.running ? '检测中' : entry.last_status ? '等待重试' : '等待首次检测' }}</td>
          <td>{{ entry.last_status ? statusLabel(entry.last_status) : '未检测' }}<small v-if="entry.last_checked_at">{{ formatTime(entry.last_checked_at) }}</small></td>
          <td>{{ entry.running ? '检测中' : formatTime(entry.next_run_at) }}</td><td>{{ entry.interval_minutes }} 分钟</td>
        </tr><tr v-if="!queue.length"><td colspan="5" class="empty-state">{{ loading ? '读取中' : '暂无待恢复账号' }}</td></tr></tbody>
      </table>
    </div>
    <h3>最近 7 天恢复记录</h3>
    <div class="recovery-table-scroll">
      <table aria-label="账号恢复检测记录"><thead><tr><th>被测账号</th><th>检测时间</th><th>结果</th><th>处理状态</th><th>配置来源 / 模型</th><th>耗时</th></tr></thead>
        <tbody><tr v-for="record in records" :key="record.id">
          <td class="account-cell">{{ record.account_name }} <span class="muted">#{{ record.account_id }}</span></td>
          <td>{{ formatTime(record.started_at) }}<small v-if="record.finished_at">完成 {{ formatTime(record.finished_at) }}</small></td>
          <td><span class="status-badge" :class="'status-' + record.status">{{ statusLabel(record.status) }}</span></td>
          <td>{{ outcomeLabel(record.outcome) }}<small>第 {{ record.retry_step + 1 }} 档</small></td>
          <td class="model-cell">{{ record.group_name || '分组' }} #{{ record.group_id }}<small>{{ record.model }} · {{ record.reasoning_effort }} · {{ record.protocol }}</small></td>
          <td>{{ record.status === 'running' ? '-' : (record.duration_ms / 1000).toFixed(1) + 's' }}</td>
        </tr><tr v-if="!records.length"><td colspan="6" class="empty-state">{{ loading ? '读取中' : '暂无已保存的恢复记录' }}</td></tr></tbody>
      </table>
    </div>
    <button v-if="hasMore" type="button" class="btn-secondary" :disabled="loading" @click="load(records.at(-1)?.id)">加载更早记录</button>
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { getIntelligenceRecoveryHistory, type IntelligenceRecoveryRecord, type IntelligenceRecoveryQueueEntry } from '@/api/intelligence'
const records = ref<IntelligenceRecoveryRecord[]>([])
const queue = ref<IntelligenceRecoveryQueueEntry[]>([])
const loading = ref(false), error = ref(''), hasMore = ref(false), serverTime = ref('')
let disposed = false, expanded = false, generation = 0
let abort: AbortController | undefined, timer: ReturnType<typeof setInterval> | undefined
async function load(before = 0) {
  const current = ++generation
  abort?.abort(); abort = new AbortController()
  loading.value = true; error.value = ''
  try {
    const data = await getIntelligenceRecoveryHistory(before, abort.signal)
    if (disposed || current !== generation) return
    records.value = before ? [...records.value, ...data.items] : data.items
    queue.value = data.queue; hasMore.value = data.has_more; serverTime.value = data.server_time; expanded = !!before
  } catch (e) {
    if (!disposed && current === generation) error.value = (e as Error).message || '无法读取恢复检测记录'
  } finally { if (current === generation) loading.value = false }
}
function formatTime(value: string) { return new Date(value).toLocaleString() }
function statusLabel(status: string) { return ({ running: '检测中', normal: '正常', degraded: '答案异常', error: '错误' } as Record<string, string>)[status] || status }
function outcomeLabel(outcome: string) { return ({ running: '执行中', recovered: '已恢复待采集', retry: '继续重试', superseded: '隔离状态已变更', interrupted: '检测中断', recovery_failed: '恢复操作失败' } as Record<string, string>)[outcome] || outcome }
onMounted(() => {
  void load()
  timer = setInterval(() => { if (!loading.value && !expanded) void load() }, 15000)
})
onUnmounted(() => { disposed = true; generation++; abort?.abort(); if (timer) clearInterval(timer) })
</script>

<style scoped>
.recovery-panel { min-width: 0; }
.recovery-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 8px 0 16px; }
h2 { font-size: 18px; font-weight: 600; }
h3 { font-size: 14px; font-weight: 600; margin: 20px 0 10px; }
.muted, small { color: var(--ic-muted); font-size: 12px; }
small { display: block; margin-top: 4px; white-space: normal; overflow-wrap: anywhere; }
.recovery-table-scroll { overflow-x: auto; margin-bottom: 16px; }
table { width: 100%; border-collapse: collapse; text-align: left; font-size: 13px; }
th { color: var(--ic-muted); font-size: 12px; font-weight: 500; }
th, td { padding: 12px 10px; border-bottom: 1px solid var(--ic-line); vertical-align: top; }
td { white-space: nowrap; }
.account-cell, .model-cell { white-space: normal; min-width: 150px; max-width: 260px; overflow-wrap: anywhere; }
.empty-state { text-align: center; color: var(--ic-muted); padding: 32px; }
.recovery-error { color: #dc2626; font-size: 13px; }
.status-badge { display: inline-block; padding: 3px 7px; border-radius: 4px; font-size: 12px; }
.status-normal { color: #059669; background: #10b98115; }
.status-degraded { color: #d97706; background: #f59e0b15; }
.status-error { color: #ef4444; background: #ef444415; }
.status-running { color: #0284c7; background: #0ea5e915; }
.btn-secondary { border: 1px solid var(--ic-line); border-radius: 6px; padding: 8px 14px; background: var(--ic-panel); font-size: 13px; }
.btn-secondary:disabled { opacity: .5; }
</style>
