<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import QQBindingForm from '@/components/user/QQBindingForm.vue'
import QQActivityNotice from '@/components/user/QQActivityNotice.vue'
import { qqActivityAPI, type QQActivitySummary } from '@/api/qqActivity'

const summary = ref<QQActivitySummary | null>(null)
const busy = ref(false)
const error = ref('')
let requestId = 0
const statusLabel = computed(() => ({ preparing: '准备中', active: '进行中', paused: '奖励已关闭', ended: '已结束' }[summary.value?.config.status || 'preparing']))
async function refresh() {
  const currentRequest = ++requestId
  busy.value = true; error.value = ''
  try {
    const result = await qqActivityAPI.current()
    if (currentRequest === requestId) summary.value = result
  } catch { if (currentRequest === requestId) error.value = '活动暂时不可用，请稍后重试。' }
  finally { if (currentRequest === requestId) busy.value = false }
}
function bindingChanged() { void refresh() }
onMounted(() => {
  void refresh()
  window.addEventListener('qq-binding-changed', bindingChanged)
})
onBeforeUnmount(() => { requestId++; window.removeEventListener('qq-binding-changed', bindingChanged) })
</script>

<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-6">
      <div class="flex items-start justify-between gap-4"><div><h1 class="text-2xl font-semibold">QQ 群活动</h1><p class="mt-2 text-sm text-gray-500">关联官网账号，查看体验额度与邀请奖励。</p></div><button class="btn btn-secondary" :disabled="busy" @click="refresh">刷新</button></div>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <template v-if="summary">
        <QQActivityNotice :config="summary.config" />
        <section class="card space-y-3 p-6"><div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">我的绑定</h2><span class="rounded-full bg-gray-100 px-3 py-1 text-xs dark:bg-dark-700">{{ statusLabel }}</span></div><p>绑定状态：{{ summary.bound ? '已绑定' : '尚未绑定' }}</p><p>官网账号：{{ summary.account }}</p><p v-if="summary.qq">QQ：{{ summary.qq }}</p><p class="text-sm" role="status">{{ summary.welcome_text }}</p><QQBindingForm v-if="!summary.bound" :intro="summary.config.binding_display?.binding_intro" :note="summary.config.binding_display?.binding_note" @verified="refresh" /><p v-else class="text-sm text-gray-500">已有绑定继续有效。可从右上角 QQ 绑定入口自行取消绑定；历史和领奖记录会保留。</p></section>
        <section class="grid gap-4 sm:grid-cols-3"><div class="card p-5"><p class="text-sm text-gray-500">邀请奖励已到账</p><p class="mt-2 text-2xl font-semibold">${{ summary.invitation_paid.toFixed(2) }}</p></div><div class="card p-5"><p class="text-sm text-gray-500">邀请奖励待发放</p><p class="mt-2 text-2xl font-semibold">${{ summary.invitation_pending.toFixed(2) }}</p></div><div class="card p-5"><p class="text-sm text-gray-500">已确认待绑定领取</p><p class="mt-2 text-2xl font-semibold">${{ summary.pending_inviter_binding.toFixed(2) }}</p></div></section>
        <section class="card space-y-3 p-6"><h2 class="text-lg font-semibold">活动说明</h2><p class="text-sm">绑定不限 QQ 等级。符合领取条件且本人 QQ 达到 16 级（一个太阳），可获 $2 体验额度，每人限领一次。</p><p class="text-sm">真实受邀成员入群、完成绑定并达到 16 级，且 $2 新人体验额度实际到账后，邀请人才会自动获得 $2 邀请余额。邀请人本人不限等级。</p><p class="text-sm">体验额度计入使用余额；邀请奖励计入邀请余额，转入条件请查看现有邀请页面。</p><router-link to="/affiliate" class="inline-block text-sm text-blue-600">查看邀请余额 →</router-link><p class="text-xs text-gray-500">群内可发送：绑定、绑定状态、邀请奖励、活动帮助。领取资格以系统核验结果为准。</p></section>
        <section class="card overflow-hidden p-6"><h2 class="mb-4 text-lg font-semibold">活动奖励记录</h2><p v-if="!summary.rewards.length" class="text-sm text-gray-500">暂无奖励记录。</p><div v-else class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b"><th class="py-3">类型</th><th>金额</th><th>状态</th><th>到账时间</th></tr></thead><tbody><tr v-for="reward in summary.rewards" :key="reward.id" class="border-b border-gray-100 dark:border-dark-700"><td class="py-3">{{ reward.kind === 'newcomer' ? '体验额度' : '邀请余额' }}</td><td>${{ reward.amount.toFixed(2) }}</td><td>{{ reward.status === 'paid' ? '已到账' : '待发放' }}</td><td>{{ reward.paid_at ? new Date(reward.paid_at).toLocaleString() : '—' }}</td></tr></tbody></table></div></section>
      </template>
    </div>
  </AppLayout>
</template>
