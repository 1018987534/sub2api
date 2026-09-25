<template>
  <AppLayout>
    <div class="mx-auto max-w-[1500px] space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div><h1 class="text-2xl font-semibold text-gray-900 dark:text-white">降智检测</h1><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">按分组配置真实低成本探针；不会伪造检测记录，也不会把检测答案展示给普通用户。</p></div>
        <div class="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-xs leading-5 text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200">公共监控固定显示：<b>gpt-6-astra · low · 每分钟检测 · 近 60 分钟 · 仅显示已检测记录</b><br>实际间隔、模型和提示词以本页保存的分组配置为准；“正常/降智”是启发式结果，不代表数学真值或模型 IQ。</div>
      </header>
      <div v-if="error" class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/20 dark:text-red-300">{{ error }}</div>
      <p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
      <div class="grid gap-5 lg:grid-cols-[260px_minmax(0,1fr)]">
        <aside class="rounded-2xl border border-gray-200 bg-white p-3 shadow-sm dark:border-dark-700 dark:bg-dark-900">
          <div class="mb-3 flex items-center justify-between"><h2 class="font-medium text-gray-900 dark:text-white">分组</h2><span class="text-xs text-gray-400">{{ groups.length }}</span></div>
          <input v-model="search" class="mb-3 w-full rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm outline-none focus:border-indigo-400 dark:border-dark-700 dark:bg-dark-800" placeholder="搜索分组">
          <div class="max-h-[650px] space-y-1 overflow-auto">
            <button v-for="group in filteredGroups" :key="group.id" class="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm transition" :class="selected?.id === group.id ? 'bg-indigo-50 text-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-300' : 'text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-dark-800'" @click="selectGroup(group)"><span class="min-w-0 truncate">{{ group.name }}</span><span class="ml-2 text-[11px] text-gray-400">#{{ group.id }}</span></button>
            <p v-if="!filteredGroups.length" class="px-3 py-5 text-center text-xs text-gray-400">暂无分组</p>
          </div>
        </aside>
        <section v-if="selected && draft" class="space-y-5">
          <div class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900">
            <div class="mb-5 flex flex-wrap items-start justify-between gap-3"><div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ selected.name }}</h2><p class="mt-1 text-xs text-gray-500">{{ selected.platform }} · #{{ selected.id }} · {{ selected.status === 'active' ? 'active' : 'inactive' }}</p></div><button class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white disabled:cursor-not-allowed disabled:opacity-50" :disabled="saving || running || !dirty || keySelectionBlocked" @click="save">{{ saving ? '保存中…' : dirty ? '保存配置' : '已保存' }}</button></div>
            <div class="grid gap-4 md:grid-cols-2">
              <label class="flex items-center gap-3 rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700"><input v-model="draft.enabled" type="checkbox" class="h-4 w-4 rounded text-indigo-600"><span><b class="text-gray-800 dark:text-gray-200">启用自动检测</b><span class="block text-xs text-gray-500">仅在控制面、V2 监控和被动聚合均允许时运行</span></span></label>
              <label class="field"><span>检测间隔（分钟）</span><input v-model.number="draft.interval_minutes" type="number" min="1" max="1440"></label>
              <label class="field"><span>单次超时（秒）</span><input v-model.number="draft.timeout_seconds" type="number" min="5" max="900"></label>
              <label class="field"><span>模型</span><input v-model="draft.model" maxlength="128"></label>
              <label class="field"><span>协议</span><select v-model="draft.protocol"><option value="responses">OpenAI Responses</option><option value="chat_completions">OpenAI Chat Completions</option><option value="messages">Anthropic Messages</option></select></label>
              <label class="field"><span>reasoning effort <em>（Messages 协议忽略）</em></span><select v-model="draft.reasoning_effort" :disabled="draft.protocol === 'messages'"><option v-for="v in efforts" :key="v" :value="v">{{ v }}</option></select></label>
              <div class="space-y-2">
                <label class="field">
                  <span>管理员 API Key</span>
                  <select v-model.number="draft.api_key_id" data-testid="intelligence-key-picker" :disabled="keysLoading || saving || running">
                    <option :value="0">请选择当前管理员的 API Key</option>
                    <option v-if="draft.api_key_id && !keyOptions.some(key => key.id === draft!.api_key_id)" :value="draft.api_key_id" disabled>已保存 Key #{{ draft.api_key_id }}（未在当前列表中，请刷新或搜索）</option>
                    <option v-for="key in keyOptions" :key="key.id" :value="key.id" :disabled="!key.available">{{ keyLabel(key) }}</option>
                  </select>
                </label>
                <div class="flex gap-2">
                  <input v-model="keySearch" aria-label="搜索管理员 API Key" maxlength="100" placeholder="按名称搜索" class="min-w-0 flex-1 rounded-lg border border-gray-200 bg-transparent px-2 py-1 text-xs dark:border-dark-700" @keydown.enter.prevent="loadKeys()">
                  <button type="button" class="btn-secondary" :disabled="keysLoading" @click="loadKeys()">刷新 Key</button>
                  <button v-if="keysHasMore" type="button" class="btn-secondary" :disabled="keysLoading" @click="loadKeys(true)">更多</button>
                </div>
                <p v-if="keysLoading" class="text-xs text-gray-500">正在读取 Key…</p>
                <p v-else-if="keysError" role="alert" class="text-xs text-red-600">{{ keysError }}，请刷新重试。</p>
                <p v-else-if="!keyOptions.length" class="text-xs text-amber-600">未找到绑定本分组的 API Key；请先在「API 密钥」创建，再回来刷新。</p>
                <p class="text-xs text-gray-500">仅列出当前登录管理员名下、绑定本分组的站内 API Key；停用、过期或额度耗尽的 Key 不可选。不显示或复制密钥明文，不是系统管理接口的 Admin API Key。</p>
              </div>
              <label class="field"><span>答案匹配</span><select v-model="draft.match_mode"><option value="contains_any">包含任一关键词（NFKC）</option><option value="exact">完全匹配（NFKC）</option></select></label>
            </div>
            <label class="field mt-4"><span>提示词（最多 32KB）</span><textarea v-model="draft.prompt" maxlength="32768" rows="5"></textarea></label>
            <label class="field mt-4"><span>期望答案（每行一个；默认：手感、21）</span><textarea v-model="expectedText" rows="3"></textarea></label>
            <p class="mt-3 text-xs text-gray-500">检测会实际消耗所绑定 Key 的额度。系统不重试请求；失败只写入 error 状态。保存采用版本号 CAS，避免覆盖其他管理员的修改。</p>
          </div>
          <div class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-900">
            <div class="mb-4 flex flex-wrap items-center justify-between gap-3"><div><h2 class="font-semibold text-gray-900 dark:text-white">最近 7 天记录</h2><p class="text-xs text-gray-500">答案仅管理员点击详情时可见。</p></div><div class="flex gap-2"><button class="btn-secondary" :disabled="running || saving || dirty || !draft.version || !draft.api_key_id || keySelectionBlocked" @click="run">{{ running ? '已排队…' : '立即检测' }}</button><button class="btn-secondary" :disabled="historyLoading" @click="loadHistory()">刷新</button></div></div>
            <div v-if="!history.length" class="rounded-lg bg-gray-50 px-4 py-8 text-center text-sm text-gray-400 dark:bg-dark-800">暂无已检测记录</div>
            <div v-else class="space-y-2"><button v-for="record in history" :key="record.id" class="flex w-full items-center justify-between rounded-lg border border-gray-100 px-3 py-3 text-left hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800" @click="detail = record"><span><b :class="statusClass(record.status)">{{ statusLabel(record.status) }}</b><span class="ml-3 text-xs text-gray-500">{{ formatTime(record.checked_at) }}</span></span><span class="text-xs text-gray-500">{{ (record.duration_ms / 1000).toFixed(1) }}s <span class="ml-2 text-gray-400">#{{ record.id }}</span></span></button></div>
            <button v-if="hasMore" class="mt-3 text-xs text-indigo-600 hover:underline" :disabled="historyLoading" @click="loadMore">加载更早记录</button>
          </div>
        </section>
        <div v-else class="rounded-2xl border border-dashed border-gray-300 p-12 text-center text-sm text-gray-400 dark:border-dark-700 lg:col-start-2">请选择一个分组</div>
      </div>
    </div>
    <BaseDialog :show="!!detail" title="检测详情" width="wide" @close="detail = null">
      <div v-if="detail" class="space-y-4 text-sm">
        <p>{{ statusLabel(detail.status) }} · {{ formatTime(detail.checked_at) }} · {{ detail.duration_ms }}ms · #{{ detail.id }}</p>
        <section><h3>模型答案（原文）</h3><pre class="mt-2 whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 dark:bg-dark-800">{{ detail.answer || detail.error || '无' }}</pre></section>
        <section v-if="detail.config"><h3>实际配置快照</h3><p class="my-2 text-gray-500">{{ detail.config.model }} · {{ detail.config.reasoning_effort }} · {{ detail.config.protocol }} · 每 {{ detail.config.interval_minutes }} 分钟 · 超时 {{ detail.config.timeout_seconds }} 秒</p><p>规则：{{ detail.config.match_mode }} / {{ detail.config.expected.join('、') }}</p><pre class="mt-2 whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 dark:bg-dark-800">{{ detail.config.prompt }}</pre></section>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { getIntelligenceConfigs, getIntelligenceHistory, runIntelligenceCheck, saveIntelligenceConfig, type IntelligenceConfig, type IntelligenceRecord, getIntelligenceKeyOptions, type IntelligenceKeyOption } from '@/api/intelligence'
import { getAllIncludingInactive } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'

const groups = ref<AdminGroup[]>([])
const selected = ref<AdminGroup | null>(null)
const configs = ref<IntelligenceConfig[]>([])
const defaults = ref<IntelligenceConfig | null>(null)
const draft = ref<IntelligenceConfig | null>(null)
const expectedText = ref('')
const history = ref<IntelligenceRecord[]>([])
const hasMore = ref(false)
const search = ref('')
const error = ref('')
const notice = ref('')
const saving = ref(false)
const running = ref(false)
const historyLoading = ref(false)
const detail = ref<IntelligenceRecord | null>(null)
const keyOptions = ref<IntelligenceKeyOption[]>([])
const keySearch = ref('')
const keysLoading = ref(false)
const keysError = ref('')
const keysHasMore = ref(false)
let keysPage = 0
let keysAppliedSearch = ''
let keysGeneration = 0
let keysAbort: AbortController | undefined
const keySelectionBlocked = computed(() => {
  const id = draft.value?.api_key_id
  if (!id) return !!draft.value?.enabled
  return keysLoading.value || !!keysError.value || keyOptions.value.some(key => key.id === id && !key.available)
})
const efforts = ['none', 'low', 'medium', 'high', 'xhigh'] as const
let disposed = false
let historyGeneration = 0
let expanded = false
let timer: ReturnType<typeof setInterval> | undefined
const filteredGroups = computed(() => groups.value.filter(g => !search.value.trim() || g.name.toLowerCase().includes(search.value.trim().toLowerCase()) || String(g.id).includes(search.value.trim())))
const input = computed(() => draft.value ? { ...draft.value, expected: expectedText.value.split(/\r?\n/).map(v => v.trim()).filter(Boolean) } : null)
const dirty = computed(() => {
  if (!input.value) return false
  const saved = configs.value.find(c => c.group_id === input.value!.group_id)
  return !saved || JSON.stringify(input.value) !== JSON.stringify(saved)
})
function keyLabel(key: IntelligenceKeyOption) {
  const quota = key.quota_remaining < 0 ? '不限额' : '剩余 $' + key.quota_remaining.toFixed(2)
  const expiry = key.expires_at ? '到期 ' + new Date(key.expires_at).toLocaleDateString() : '不过期'
  return key.name + ' · #' + key.id + ' · ' + (key.available ? quota + ' · ' + expiry : key.unavailable_reason || '不可用')
}
async function loadKeys(more = false) {
  if (!selected.value) return
  const groupID = selected.value.id
  const generation = ++keysGeneration
  keysAbort?.abort()
  keysAbort = new AbortController()
  const page = more ? keysPage + 1 : 1
  const search = more ? keysAppliedSearch : keySearch.value.trim()
  keysLoading.value = true; keysError.value = ''
  if (!more) { keyOptions.value = []; keysHasMore.value = false }
  try {
    const result = await getIntelligenceKeyOptions(groupID, page, search, keysAbort.signal)
    if (disposed || generation !== keysGeneration) return
    const items = (result.items || []).filter(key => key.group_id === groupID)
    const merged = more ? [...keyOptions.value, ...items] : items
    keyOptions.value = [...new Map(merged.map(key => [key.id, key])).values()]
    keysPage = result.page; keysHasMore.value = result.has_more; keysAppliedSearch = search
  } catch (e) {
    if (!disposed && generation === keysGeneration) keysError.value = message(e)
  } finally {
    if (generation === keysGeneration) keysLoading.value = false
  }
}
function clone(c: IntelligenceConfig, id: number): IntelligenceConfig {
  return { ...c, group_id: id, expected: [...c.expected] }
}
function message(e: unknown) {
  const x = e as { response?: { data?: { message?: string } }; message?: string }
  return x.response?.data?.message || x.message || '请求失败'
}
async function reload() {
  error.value = ''
  try {
    const [gs, cs] = await Promise.all([getAllIncludingInactive(), getIntelligenceConfigs()])
    if (disposed) return
    groups.value = gs; configs.value = cs.items || []; defaults.value = cs.defaults
    if (gs[0]) selectGroup(gs[0])
  } catch (e) { if (!disposed) error.value = message(e) }
}
function selectGroup(g: AdminGroup) {
  if (saving.value || running.value || !defaults.value) return
  if (selected.value?.id === g.id) return
  if (dirty.value && !window.confirm('当前配置未保存，确定切换分组吗？')) return
  selected.value = g
  draft.value = clone(configs.value.find(v => v.group_id === g.id) || defaults.value, g.id)
  expectedText.value = draft.value.expected.join('\n')
  history.value = []; hasMore.value = false; detail.value = null; notice.value = ''; error.value = ''
  keyOptions.value = []; keySearch.value = ''; keysPage = 0; keysHasMore.value = false
  void loadKeys()
  void loadHistory()
}
async function save() {
  if (!input.value || saving.value || keySelectionBlocked.value) return
  saving.value = true; error.value = ''
  try {
    const saved = await saveIntelligenceConfig(clone(input.value, input.value.group_id))
    if (disposed) return
    configs.value = [...configs.value.filter(v => v.group_id !== saved.group_id), saved]
    draft.value = clone(saved, saved.group_id)
    expectedText.value = saved.expected.join('\n')
    notice.value = '配置已保存；实际运行间隔为 ' + saved.interval_minutes + ' 分钟。'
  } catch (e) { if (!disposed) error.value = message(e) }
  finally { saving.value = false }
}
async function run() {
  if (!selected.value || dirty.value || running.value || !draft.value?.version || keySelectionBlocked.value) return
  running.value = true; error.value = ''
  try {
    await runIntelligenceCheck(selected.value.id)
    if (disposed) return
    notice.value = '检测已提交，完成后显示真实结果；页面每 15 秒刷新首页记录。请勿重复提交。'
    await loadHistory()
  } catch (e) { if (!disposed) error.value = message(e) }
  finally { running.value = false }
}
async function loadHistory(before = 0) {
  if (!selected.value) return
  const generation = ++historyGeneration
  const groupID = selected.value.id
  historyLoading.value = true
  if (!before) expanded = false
  try {
    const result = await getIntelligenceHistory(groupID, before)
    if (disposed || generation !== historyGeneration) return
    history.value = before ? [...history.value, ...(result.items || [])] : result.items || []
    hasMore.value = result.has_more
    expanded = !!before
  } catch (e) { if (!disposed && generation === historyGeneration) error.value = message(e) }
  finally { if (generation === historyGeneration) historyLoading.value = false }
}
function loadMore() {
  const last = history.value.at(-1)
  if (last && !historyLoading.value) void loadHistory(last.id)
}
function statusLabel(s: IntelligenceRecord['status']) { return s === 'normal' ? '正常' : s === 'degraded' ? '答案异常' : '错误' }
function statusClass(s: IntelligenceRecord['status']) { return s === 'normal' ? 'text-emerald-600' : s === 'degraded' ? 'text-amber-600' : 'text-red-600' }
function formatTime(s: string) { return new Date(s).toLocaleString() }
function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value || saving.value) { e.preventDefault(); e.returnValue = '' }
}
onBeforeRouteLeave(() => !(dirty.value || saving.value) || window.confirm('配置尚未保存，确定离开吗？'))
onMounted(() => {
  void reload()
  window.addEventListener('beforeunload', beforeUnload)
  timer = setInterval(() => { if (!historyLoading.value && !expanded) void loadHistory() }, 15000)
})
onUnmounted(() => {
  disposed = true; historyGeneration++; keysGeneration++; keysAbort?.abort()
  if (timer) clearInterval(timer)
  window.removeEventListener('beforeunload', beforeUnload)
})
</script>

<style scoped>
.field { display:flex; flex-direction:column; gap:.4rem; font-size:.875rem; color:rgb(75 85 99); }
.field > span { font-size:.75rem; color:rgb(107 114 128); }
.field em { font-style:normal; color:rgb(156 163 175); }
.field input,.field select,.field textarea { width:100%; border:1px solid rgb(229 231 235); border-radius:.5rem; background:rgb(249 250 251); padding:.55rem .7rem; color:rgb(31 41 55); outline:none; }
.field textarea { resize:vertical; }
.btn-secondary { border:1px solid rgb(229 231 235); border-radius:.5rem; padding:.5rem .75rem; font-size:.75rem; color:rgb(75 85 99); }
.btn-secondary:disabled { cursor:not-allowed; opacity:.45; }
:global(.dark) .field input,:global(.dark) .field select,:global(.dark) .field textarea { border-color:rgb(55 65 81); background:rgb(31 41 55); color:rgb(229 231 235); }
</style>
