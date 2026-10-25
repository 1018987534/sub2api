<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import QQBindingForm from '@/components/user/QQBindingForm.vue'
import { qqActivityAPI, type QQBindingStatus } from '@/api/qqActivity'

const status = ref<QQBindingStatus | null>(null)
const error = ref('')
const loading = ref(false)
const show = ref(false)
const confirmUnbind = ref(false)
const unbinding = ref(false)
const notice = ref('')
const label = computed(() => status.value?.bound ? 'QQ 已绑定' : loading.value ? 'QQ 查询中' : error.value ? 'QQ 状态重试' : 'QQ 未绑定')
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false
let revision = 0
async function refresh() {
  if (loading.value) return
  const requestedRevision = revision
  loading.value = true
  try {
    const result = await qqActivityAPI.binding()
    if (!disposed && requestedRevision === revision) { status.value = result; error.value = '' }
  } catch { if (!disposed && requestedRevision === revision) error.value = '绑定状态暂时不可用，请重试。' }
  finally { if (!disposed) loading.value = false }
}
async function open() { show.value = true; confirmUnbind.value = false; notice.value = ''; await refresh() }
function verified(result: QQBindingStatus) { revision++; status.value = result; error.value = ''; notice.value = 'QQ 绑定成功。' }
async function unbind() {
  if (unbinding.value) return
  revision++
  unbinding.value = true; error.value = ''
  try {
    status.value = await qqActivityAPI.unbind()
    confirmUnbind.value = false
    notice.value = '已取消绑定。历史和领奖记录已保留，重新绑定需验证 QQ 邮箱。'
    window.dispatchEvent(new Event('qq-binding-changed'))
  } catch { error.value = '取消绑定未完成，请稍后重试；若绑定受限，请联系管理员。' }
  finally { unbinding.value = false }
}
function refreshVisible() { if (document.visibilityState !== 'hidden') void refresh() }
onMounted(() => {
  void refresh()
  window.addEventListener('focus', refreshVisible)
  window.addEventListener('qq-binding-changed', refreshVisible)
  timer = setInterval(() => { if (!status.value?.bound) refreshVisible() }, 30000)
})
onBeforeUnmount(() => {
  disposed = true; clearInterval(timer)
  window.removeEventListener('focus', refreshVisible)
  window.removeEventListener('qq-binding-changed', refreshVisible)
})
</script>

<template>
  <button
    type="button"
    data-testid="qq-binding-entry"
    :aria-label="label"
    :title="status?.bound ? `QQ 已绑定：${status.qq_email}` : '查看绑定状态或验证 QQ 邮箱'"
    class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg px-2 py-1.5 text-xs font-medium transition-colors hover:bg-gray-100 dark:hover:bg-dark-800 sm:px-2.5 sm:text-sm"
    :class="status?.bound ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'"
    @click="open"
  >
    <Icon :name="status?.bound ? 'check' : 'link'" size="sm" />
    <span class="hidden sm:inline">{{ label }}</span>
  </button>
  <BaseDialog :show="show" title="QQ 绑定" width="narrow" @close="show = false">
    <p v-if="notice" role="status" class="mb-4 text-sm text-emerald-600">{{ notice }}</p>
    <div v-if="status?.bound" class="space-y-3">
      <p class="font-medium text-emerald-600">已绑定 QQ：{{ status.qq_email }}</p>
      <p class="text-sm text-gray-500">已有绑定继续有效，无需重复验证。</p>
      <div v-if="confirmUnbind" class="space-y-3 rounded-lg bg-amber-50 p-3 dark:bg-amber-950/20">
        <p class="text-sm">确认取消当前 QQ 绑定？解绑后停止自动匹配，历史和领奖记录保留，重新绑定不会重复发放新人奖励。</p>
        <button class="btn btn-danger" :disabled="unbinding" @click="unbind">{{ unbinding ? '处理中…' : '确认取消绑定' }}</button>
        <button class="btn btn-secondary ml-2" :disabled="unbinding" @click="confirmUnbind = false">保留绑定</button>
      </div>
      <button v-else class="btn btn-secondary" @click="confirmUnbind = true">取消绑定</button>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    </div>
    <p v-else-if="loading && !status" role="status">正在查询绑定状态…</p>
    <div v-else-if="error" class="space-y-3"><p role="alert">{{ error }}</p><button class="btn btn-secondary" @click="refresh">重试</button></div>
    <QQBindingForm v-else-if="status" @verified="verified" />
  </BaseDialog>
</template>
