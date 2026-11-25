<template>
  <AppLayout>
    <div class="intelligence-page mx-auto max-w-[1500px] space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div><h1 class="text-2xl font-semibold text-gray-900 dark:text-white">降智检测</h1><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">仅检测名称包含“不降智”且已启用的分组；其他分组不展示，并自动关闭检测。</p></div>

      </header>
      <div class="flex gap-5 border-b border-gray-200 pb-3 dark:border-dark-700" role="tablist" aria-label="检测类型">
        <button type="button" role="tab" :aria-selected="view === 'groups'" :class="view === 'groups' ? 'font-semibold text-emerald-600' : 'text-gray-500'" @click="view = 'groups'">分组检测</button>
        <button type="button" role="tab" :aria-selected="view === 'recovery'" :class="view === 'recovery' ? 'font-semibold text-emerald-600' : 'text-gray-500'" @click="view = 'recovery'">账号恢复检测</button>
      </div>
      <IntelligenceRecoveryPanel v-if="view === 'recovery'" />
      <div v-show="view === 'groups'" class="public-preview">
        <span class="preview-label">公共监控文案</span>
        <p class="preview-caption" data-testid="public-caption">{{ publicCaption }}</p>
        <p class="preview-note">渠道状态按模型展示检测结果。每行最多显示最近 60 条实际检测记录。</p>
      </div>
      <div v-if="error" class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/20 dark:text-red-300">{{ error }}</div>
      <p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
      <div v-show="view === 'groups'" class="grid items-start gap-5 lg:grid-cols-[240px_minmax(0,1fr)]">
        <aside class="group-sidebar panel">
          <div class="mb-3 flex items-center justify-between"><h2 class="font-medium text-gray-900 dark:text-white">分组</h2><span class="text-xs text-gray-400">{{ groups.length }}</span></div>
          <input v-model="search" class="control mb-3" aria-label="搜索分组" placeholder="搜索分组">
          <div class="group-list space-y-1 overflow-auto">
            <button v-for="group in filteredGroups" :key="group.id" class="group-option" :aria-pressed="selected?.id === group.id" :class="selected?.id === group.id ? 'bg-indigo-50 text-indigo-700 dark:bg-indigo-950/40 dark:text-indigo-300' : 'text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-dark-800'" @click="selectGroup(group)"><span class="min-w-0 truncate">{{ group.name }}</span><span class="ml-2 text-[11px] text-gray-400">#{{ group.id }}</span></button>
            <p v-if="!filteredGroups.length" class="px-3 py-5 text-center text-xs text-gray-400">暂无分组</p>
          </div>
        </aside>
        <section v-if="selected && draft" class="min-w-0 space-y-5">
          <div class="panel config-panel">
            <div class="config-heading flex flex-wrap items-center justify-between gap-3"><div><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ selected.name }}</h2><p class="mt-1 text-xs text-gray-500">{{ selected.platform }} · #{{ selected.id }}</p></div><button type="button" class="save-button" :disabled="saving || running || deleting || !dirty || keySelectionBlocked || !!modelError" @click="save">{{ saving ? '保存中…' : dirty ? '保存配置' : '已保存' }}</button></div>
            <section class="form-section space-y-3" aria-label="检测模型">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <h3 class="text-sm font-semibold">检测模型 <span class="ml-1 text-xs font-normal text-gray-500">{{ groupConfigs.length }} / {{ maxModels }}</span></h3>
                <button type="button" class="btn-secondary" :disabled="busy || adding || !primaryConfig?.version || groupConfigs.length >= maxModels" @click="startModel">添加检测模型</button>
              </div>
              <div class="flex flex-wrap gap-2" role="group" aria-label="切换检测模型">
                <button v-for="model in modelOptions" :key="model.probe_id ?? 0" type="button" class="model-option" :aria-pressed="!adding && (draft.probe_id ?? 0) === (model.probe_id ?? 0)" :disabled="busy" @click="selectModel(model)">
                  <span class="break-all">{{ model.model }}</span><span v-if="!model.probe_id" class="shrink-0 text-[10px] opacity-70">主检测</span>
                </button>
                <span v-if="adding" class="model-option model-option-active">新增模型</span>
              </div>
              <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500">
                <span>{{ adding || draft.probe_id ? '该模型的结果用于渠道状态展示。' : '主检测用于渠道状态展示和账号自动保护。' }}</span>
                <button v-if="adding" type="button" class="btn-secondary" :disabled="busy" @click="primaryConfig && selectModel(primaryConfig)">取消添加</button>
                <button v-else-if="draft.probe_id" type="button" class="text-xs font-medium text-red-600 dark:text-red-400" :disabled="busy" @click="removeModel">{{ deleting ? '移除中…' : '移除模型' }}</button>
              </div>
            </section>
            <section class="form-section" aria-labelledby="schedule-heading">
              <h3 id="schedule-heading" class="section-heading"><span>01</span>检测配置</h3>
              <label class="auto-detection"><input v-model="draft.enabled" type="checkbox"><span><b>启用自动检测</b><span class="block text-xs text-gray-500 dark:text-gray-400">仅在控制面、V2 监控和被动聚合均允许时运行</span></span></label>
              <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
                <label class="field"><span>检测间隔（分钟）</span><input v-model.number="draft.interval_minutes" type="number" min="1" max="1440"></label>
                <label class="field"><span>单次超时（秒）</span><input v-model.number="draft.timeout_seconds" type="number" min="5" max="900"></label>
                <label class="field"><span>模型</span><input v-model="draft.model" maxlength="128" :aria-invalid="!!modelError"><span v-if="modelError" role="alert" class="!text-red-600">{{ modelError }}</span></label>
                <label class="field"><span>协议</span><select v-model="draft.protocol"><option value="responses">OpenAI Responses</option><option value="chat_completions">OpenAI Chat Completions</option><option value="messages">Anthropic Messages</option></select></label>
                <label class="field"><span>reasoning effort <em>（Messages 协议忽略）</em></span><select v-model="draft.reasoning_effort" :disabled="draft.protocol === 'messages'"><option v-for="v in efforts" :key="v" :value="v">{{ v }}</option></select></label>
              </div>
            </section>
            <section class="form-section" aria-labelledby="key-heading">
              <h3 id="key-heading" class="section-heading"><span>02</span>调用凭证</h3>
              <div class="space-y-2">
                <label class="field">
                  <span>管理员 API Key</span>
                  <select v-model.number="draft.api_key_id" data-testid="intelligence-key-picker" :disabled="keysLoading || saving || running">
                    <option :value="0">请选择当前管理员的 API Key</option>
                    <option v-if="draft.api_key_id && !keyOptions.some(key => key.id === draft!.api_key_id)" :value="draft.api_key_id" disabled>已保存 Key #{{ draft.api_key_id }}（未在当前列表中，请刷新或搜索）</option>
                    <option v-for="key in keyOptions" :key="key.id" :value="key.id" :disabled="!key.available">{{ keyLabel(key) }}</option>
                  </select>
                </label>
                <div class="flex flex-wrap gap-2">
                  <input v-model="keySearch" aria-label="搜索管理员 API Key" maxlength="100" placeholder="按名称搜索" class="control min-w-0 flex-1" @keydown.enter.prevent="loadKeys()">
                  <button type="button" class="btn-secondary" :disabled="keysLoading" @click="loadKeys()">刷新 Key</button>
                  <button v-if="keysHasMore" type="button" class="btn-secondary" :disabled="keysLoading" @click="loadKeys(true)">更多</button>
                </div>
                <p v-if="keysLoading" class="text-xs text-gray-500">正在读取 Key…</p>
                <p v-else-if="keysError" role="alert" class="text-xs text-red-600">{{ keysError }}，请刷新重试。</p>
                <p v-else-if="!keyOptions.length" class="text-xs text-amber-600">未找到绑定本分组的 API Key；请先在「API 密钥」创建，再回来刷新。</p>
                <p class="text-xs text-gray-500">仅列出当前登录管理员名下、绑定本分组的站内 API Key；停用、过期或额度耗尽的 Key 不可选。不显示或复制密钥明文，不是系统管理接口的 Admin API Key。</p>
              </div>
            </section>
            <section class="form-section" aria-labelledby="answer-heading">
              <h3 id="answer-heading" class="section-heading"><span>03</span>提示词与答案</h3>
              <div class="grid items-start gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
                <label class="field prompt-field"><span>提示词（最多 32KB）</span><textarea v-model="draft.prompt" maxlength="32768" rows="9"></textarea></label>
                <div class="space-y-4">
                  <label class="field"><span>答案匹配</span><select v-model="draft.match_mode"><option value="contains_any">包含任一关键词（NFKC）</option><option value="exact">完全匹配（NFKC）</option></select></label>
                  <label class="field"><span>期望答案（每行一个；默认：21）</span><textarea v-model="expectedText" rows="5"></textarea></label>
                </div>
              </div>
            </section>
            <p class="config-footnote">内置糖果题仅接受最终数字 21；旧版“手感、21”关键词配置也按此规则判断。自定义题目按所选匹配方式判断。检测会实际消耗所绑定 Key 的额度。系统不重试请求；失败只写入 error 状态。保存采用版本号 CAS，避免覆盖其他管理员的修改。</p>
          </div>
          <div v-if="!adding" class="panel history-panel">
            <div class="mb-4 flex flex-wrap items-center justify-between gap-3"><div><h2 class="font-semibold text-gray-900 dark:text-white">最近 7 天记录</h2><p class="text-xs text-gray-500">{{ draft.model }} · 点击记录查看检测回答</p></div><div class="flex gap-2"><button class="btn-secondary" :disabled="busy || dirty || !draft.version || !draft.api_key_id || keySelectionBlocked" @click="run">{{ running ? '已排队…' : '立即检测' }}</button><button class="btn-secondary" :disabled="historyLoading" @click="loadHistory()">刷新</button></div></div>
            <div v-if="!history.length" class="rounded-lg bg-gray-50 px-4 py-8 text-center text-sm text-gray-400 dark:bg-dark-800">暂无已检测记录</div>
            <div v-else class="space-y-2"><button v-for="record in history" :key="record.id" class="history-row" @click="detail = record"><span><b class="status-badge" :class="statusClass(record.status)">{{ statusLabel(record.status) }}</b><span class="ml-3 text-xs text-gray-500">{{ formatTime(record.checked_at) }}</span><span class="ml-3 text-xs">{{ record.account_id ? '账号 #' + record.account_id : '账号未归属' }}</span></span><span class="text-xs text-gray-500">{{ (record.duration_ms / 1000).toFixed(1) }}s <span class="ml-2 text-gray-400">#{{ record.id }}</span></span></button></div>
            <button v-if="hasMore" class="btn-secondary mt-3" :disabled="historyLoading" @click="loadMore">加载更早记录</button>
          </div>
        </section>
        <div v-else class="rounded-2xl border border-dashed border-gray-300 p-12 text-center text-sm text-gray-400 dark:border-dark-700 lg:col-start-2">请选择一个分组</div>
      </div>
    </div>
    <BaseDialog :show="!!detail" title="检测详情" width="wide" @close="detail = null">
      <div v-if="detail" class="space-y-4 text-sm">
        <p>{{ statusLabel(detail.status) }} · {{ formatTime(detail.checked_at) }} · {{ detail.duration_ms }}ms · #{{ detail.id }}</p>
        <p>{{ detail.account_id ? '被测账号 #' + detail.account_id : '账号未归属' }}</p>
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
import IntelligenceRecoveryPanel from './IntelligenceRecoveryPanel.vue'
import { addIntelligenceModel, deleteIntelligenceModel, getIntelligenceConfigs, getIntelligenceHistory, runIntelligenceCheck, saveIntelligenceConfig, type IntelligenceConfig, type IntelligenceRecord, getIntelligenceKeyOptions, type IntelligenceKeyOption } from '@/api/intelligence'
import { getAll } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { formatIntelligenceCaption } from '@/features/channel-monitor-v2-cards/constants'

const groups = ref<AdminGroup[]>([])
const view = ref<'groups' | 'recovery'>('groups')
const selected = ref<AdminGroup | null>(null)
const configs = ref<IntelligenceConfig[]>([])
const defaults = ref<IntelligenceConfig | null>(null)
const draft = ref<IntelligenceConfig | null>(null)
const adding = ref(false)
const deleting = ref(false)
const maxModels = ref(8)
const expectedText = ref('')
const publicCaption = computed(() => formatIntelligenceCaption(draft.value ?? undefined))
const history = ref<IntelligenceRecord[]>([])
const hasMore = ref(false)
const search = ref('')
const error = ref('')
const notice = ref('')
const saving = ref(false)
const running = ref(false)
const busy = computed(() => saving.value || running.value || deleting.value)
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
const groupConfigs = computed(() => configs.value.filter(c => c.group_id === selected.value?.id))
const primaryConfig = computed(() => groupConfigs.value.find(c => !c.probe_id))
const modelOptions = computed(() => groupConfigs.value.length ? groupConfigs.value : draft.value ? [draft.value] : [])
const input = computed(() => draft.value ? { ...draft.value, expected: expectedText.value.split(/\r?\n/).map(v => v.trim()).filter(Boolean) } : null)
const dirty = computed(() => {
  if (!input.value) return false
  if (adding.value) return true
  const saved = groupConfigs.value.find(c => c.probe_id === input.value?.probe_id)
  return !saved || JSON.stringify(input.value) !== JSON.stringify(saved)
})
const modelError = computed(() => {
  const name = draft.value?.model.trim()
  if (!name) return '请输入模型名称'
  if (groupConfigs.value.some(c => (adding.value || c.probe_id !== draft.value?.probe_id) && c.model.trim() === name)) {
    return '同一分组不能重复配置相同模型'
  }
  return ''
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
    const [allGroups, cs] = await Promise.all([getAll(), getIntelligenceConfigs()])
    if (disposed) return
    const gs = allGroups.filter(group => group.status === 'active' && group.name.includes('不降智'))
    groups.value = gs; configs.value = cs.items || []; defaults.value = cs.defaults
    maxModels.value = cs.max_models_per_group ?? 8
    if (gs[0]) selectGroup(gs[0])
  } catch (e) { if (!disposed) error.value = message(e) }
}
function selectGroup(g: AdminGroup) {
  if (busy.value || !defaults.value) return
  if (selected.value?.id === g.id) return
  if (dirty.value && !window.confirm('当前配置未保存，确定切换分组吗？')) return
  selected.value = g
  editModel(primaryConfig.value || defaults.value)
  keyOptions.value = []; keySearch.value = ''; keysPage = 0; keysHasMore.value = false
  void loadKeys()
  void loadHistory()
}
function editModel(model: IntelligenceConfig, isNew = false) {
  if (!selected.value) return
  adding.value = isNew
  draft.value = clone(model, selected.value.id)
  expectedText.value = model.expected.join('\n')
  historyGeneration++
  history.value = []
  hasMore.value = false
  historyLoading.value = false
  detail.value = null
  notice.value = ''
  error.value = ''
}
function selectModel(model: IntelligenceConfig) {
  if (busy.value || (!adding.value && draft.value?.probe_id === model.probe_id)) return
  if (dirty.value && !window.confirm('当前配置未保存，确定切换模型吗？')) return
  editModel(model)
  void loadHistory()
}
function startModel() {
  const primary = primaryConfig.value
  if (busy.value || adding.value || !primary?.version || groupConfigs.value.length >= maxModels.value) return
  if (dirty.value && !window.confirm('当前配置未保存，确定切换模型吗？')) return
  editModel({ ...primary, model: '', enabled: false, version: 0 }, true)
}
async function save() {
  if (!input.value || busy.value || keySelectionBlocked.value || modelError.value) return
  saving.value = true; error.value = ''
  try {
    const saveConfig = adding.value ? addIntelligenceModel : saveIntelligenceConfig
    const saved = await saveConfig(clone(input.value, input.value.group_id))
    if (disposed) return
    configs.value = [...configs.value.filter(v => v.group_id !== saved.group_id || v.probe_id !== saved.probe_id), saved]
      .sort((a, b) => a.group_id - b.group_id || (a.probe_id ?? 0) - (b.probe_id ?? 0))
    const added = adding.value
    adding.value = false
    draft.value = clone(saved, saved.group_id)
    expectedText.value = saved.expected.join('\n')
    notice.value = '配置已保存；实际运行间隔为 ' + saved.interval_minutes + ' 分钟。'
    if (added) void loadHistory()
  } catch (e) { if (!disposed) error.value = message(e) }
  finally { saving.value = false }
}
async function removeModel() {
  const current = draft.value
  if (busy.value || !current?.probe_id || !window.confirm('确定移除此模型的检测配置？')) return
  deleting.value = true
  error.value = ''
  try {
    await deleteIntelligenceModel(current.group_id, current.probe_id, current.version)
    if (disposed) return
    configs.value = configs.value.filter(c => c.group_id !== current.group_id || c.probe_id !== current.probe_id)
    if (primaryConfig.value) editModel(primaryConfig.value)
    notice.value = '检测模型已移除。'
    void loadHistory()
  } catch (e) { if (!disposed) error.value = message(e) }
  finally { deleting.value = false }
}
async function run() {
  if (!selected.value || dirty.value || busy.value || !draft.value?.version || !draft.value.api_key_id || keySelectionBlocked.value) return
  running.value = true; error.value = ''
  try {
    const probeId = draft.value.probe_id
    await (probeId ? runIntelligenceCheck(selected.value.id, probeId) : runIntelligenceCheck(selected.value.id))
    if (disposed) return
    notice.value = '检测已提交，完成后显示真实结果；页面每 15 秒刷新首页记录。请勿重复提交。'
    await loadHistory()
  } catch (e) { if (!disposed) error.value = message(e) }
  finally { running.value = false }
}
async function loadHistory(before = 0) {
  if (!selected.value || adding.value) return
  const generation = ++historyGeneration
  const groupID = selected.value.id
  historyLoading.value = true
  if (!before) expanded = false
  try {
    const probeId = draft.value?.probe_id
    const result = await (probeId ? getIntelligenceHistory(groupID, before, probeId) : getIntelligenceHistory(groupID, before))
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
function statusClass(s: IntelligenceRecord['status']) { return s === 'normal' ? 'status-normal' : s === 'degraded' ? 'status-degraded' : 'status-error' }
function formatTime(s: string) { return new Date(s).toLocaleString() }
function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value || saving.value || deleting.value) { e.preventDefault(); e.returnValue = '' }
}
onBeforeRouteLeave(() => !(dirty.value || saving.value || deleting.value) || window.confirm('配置尚未保存，确定离开吗？'))
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
.intelligence-page { --ic-panel: #fff; --ic-control: #f8fafc; --ic-line: #e2e8f0; --ic-text: #1e293b; --ic-muted: #64748b; --ic-accent: #4f46e5; --ic-tint: #eef2ff; color: var(--ic-text); }
.dark .intelligence-page { --ic-panel: #141b2a; --ic-control: #0f1726; --ic-line: #2b3548; --ic-text: #e2e8f0; --ic-muted: #94a3b8; --ic-accent: #a5b4fc; --ic-tint: #202940; color-scheme: dark; }
.panel { min-width: 0; border: 1px solid var(--ic-line); border-radius: 1rem; background: var(--ic-panel); box-shadow: 0 2px 6px #00000004; }
.public-preview { display: grid; gap: .4rem; border: 1px solid var(--ic-line); border-left: 3px solid var(--ic-accent); border-radius: .75rem; padding: 1rem 1.25rem; background: var(--ic-panel); }
.preview-label { color: var(--ic-muted); font-size: .75rem; }
.preview-caption { color: var(--ic-accent); font-size: .875rem; font-weight: 600; overflow-wrap: anywhere; }
.preview-note, .config-footnote { color: var(--ic-muted); font-size: .75rem; line-height: 1.7; }
.group-sidebar { padding: 1rem; }
.group-list { max-height: 620px; }
.group-option { display: flex; width: 100%; align-items: center; justify-content: space-between; border: 1px solid transparent; border-radius: .625rem; padding: .75rem; text-align: left; font-size: .875rem; transition: background .15s; }
.group-option[aria-pressed="true"] { border-color: var(--ic-line); background: var(--ic-tint); color: var(--ic-accent); font-weight: 600; }
.config-heading { padding: 1.25rem 1.5rem; border-bottom: 1px solid var(--ic-line); }
.model-option { display: inline-flex; align-items: center; gap: .5rem; min-width: 0; max-width: 100%; border: 1px solid var(--ic-line); border-radius: .625rem; padding: .45rem .65rem; font-size: .75rem; text-align: left; }
.model-option[aria-pressed="true"], .model-option-active { background: var(--ic-tint); color: var(--ic-accent); font-weight: 600; }
.save-button { min-height: 2.5rem; border-radius: .625rem; background: #4f46e5; padding: .625rem 1.25rem; font-size: .875rem; font-weight: 600; color: #fff; }
.save-button:hover:not(:disabled) { background: #4338ca; }
.form-section { padding: 1.25rem 1.5rem; border-bottom: 1px solid var(--ic-line); }
.section-heading { display: flex; gap: .625rem; align-items: center; margin-bottom: 1rem; font-size: .875rem; font-weight: 600; }
.section-heading > span { display: inline-flex; align-items: center; justify-content: center; width: 1.5rem; height: 1.5rem; border-radius: .4rem; background: var(--ic-tint); color: var(--ic-accent); font-size: .65rem; font-variant-numeric: tabular-nums; }
.auto-detection { display: flex; align-items: center; gap: .75rem; margin-bottom: 1.25rem; border-radius: .625rem; background: var(--ic-control); padding: .875rem 1rem; font-size: .875rem; }
.auto-detection input { width: 1rem; height: 1rem; accent-color: #6366f1; }
.auto-detection b { display: block; margin-bottom: .2rem; font-weight: 500; }
.field { min-width: 0; display: flex; flex-direction: column; gap: .5rem; font-size: .875rem; }
.field > span { font-size: .75rem; font-weight: 500; color: var(--ic-muted); }
.field em { font-style: normal; font-weight: 400; }
.control, .field input, .field select, .field textarea { min-width: 0; width: 100%; border: 1px solid var(--ic-line); border-radius: .625rem; background: var(--ic-control); padding: .65rem .8rem; color: var(--ic-text); outline: none; font-size: .875rem; line-height: 1.5; transition: border-color .15s, box-shadow .15s; }
.control { width: auto; }
.group-sidebar > .control { width: 100%; }
.control:focus, .field input:focus, .field select:focus, .field textarea:focus { border-color: #818cf8; box-shadow: 0 0 0 3px #818cf820; }
.field textarea { resize: vertical; line-height: 1.8; }
.prompt-field textarea { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.config-footnote { padding: 1rem 1.5rem; }
.history-panel { padding: 1.25rem 1.5rem; }
.history-row { display: flex; flex-wrap: wrap; width: 100%; align-items: center; justify-content: space-between; gap: .5rem; border: 1px solid var(--ic-line); border-radius: .625rem; padding: .75rem; text-align: left; }
.history-row:hover { background: var(--ic-control); }
.status-badge { display: inline-block; border-radius: .375rem; padding: .2rem .5rem; font-size: .75rem; font-weight: 500; }
.status-normal { color: #047857; background: #10b98115; }
.status-degraded { color: #b45309; background: #f59e0b15; }
.status-error { color: #dc2626; background: #ef444415; }
.dark .status-normal { color: #6ee7b7; }
.dark .status-degraded { color: #fcd34d; }
.dark .status-error { color: #fca5a5; }
.btn-secondary { white-space: nowrap; border: 1px solid var(--ic-line); border-radius: .625rem; background: var(--ic-panel); padding: .5rem .875rem; font-size: .75rem; color: var(--ic-text); }
.btn-secondary:hover:not(:disabled) { background: var(--ic-control); border-color: var(--ic-muted); }
button:focus-visible, .auto-detection input:focus-visible { outline: 2px solid #818cf8; outline-offset: 3px; }
button:disabled, .field select:disabled { cursor: not-allowed; opacity: .45; }
@media (min-width: 1024px) { .group-sidebar { position: sticky; top: 1.5rem; } }
@media (max-width: 639px) { .group-list { max-height: 180px; } .config-heading, .form-section, .history-panel { padding: 1rem; } .config-footnote { padding: .875rem 1rem; } }
</style>
