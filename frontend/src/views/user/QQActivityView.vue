<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import { qqActivityAPI, type QQActivitySummary, type QQClaim } from '@/api/qqActivity'

const route = useRoute()
const summary = ref<QQActivitySummary | null>(null)
const claim = ref<QQClaim | null>(null)
const busy = ref(false)
const error = ref('')
const token = computed(() => typeof route.query.claim === 'string' ? route.query.claim : '')
const statusLabel = computed(() => ({ preparing: '准备中', active: '进行中', paused: '奖励已关闭', ended: '已结束' }[summary.value?.config.status || 'preparing']))
async function refresh() {
  busy.value = true; error.value = ''
  try {
    summary.value = await qqActivityAPI.current()
    if (token.value) claim.value = await qqActivityAPI.claim(token.value)
  } catch { error.value = token.value ? '确认信息无效或已过期，或当前登录的账号与候选账号不一致。请回到活动群重新发送「绑定」。' : '活动暂时不可用，请稍后重试。' }
  finally { busy.value = false }
}
async function issueCode() {
  busy.value = true; error.value = ''
  try { claim.value = await qqActivityAPI.claim(token.value, true) }
  catch { error.value = '确认信息无效或已过期，请回到活动群重新发送「绑定」。' }
  finally { busy.value = false }
}
onMounted(refresh)
</script>

<template>
  <AppLayout>
    <div class="mx-auto max-w-4xl space-y-6">
      <div class="flex items-start justify-between gap-4"><div><h1 class="text-2xl font-semibold">QQ 群活动</h1><p class="mt-2 text-sm text-gray-500">关联官网账号，查看体验额度与邀请奖励。</p></div><button class="btn btn-secondary" :disabled="busy" @click="refresh">刷新</button></div>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <section v-if="claim" class="card space-y-4 p-6" aria-label="确认账号归属">
        <h2 class="text-lg font-semibold">确认关联的官网账号</h2>
        <p class="text-sm">官网账号：<strong>{{ claim.account }}</strong><br>QQ 邮箱提示：{{ claim.qq_email }}</p>
        <p class="text-sm text-gray-500">请确认这是你本人使用的账号。生成确认码后，用发起绑定的同一个 QQ，在原活动群发送完整指令。确认码只用于本次绑定，请勿交给他人。</p>
        <p v-if="claim.consumed" role="status">本次绑定已经完成，请刷新查看结果。</p>
        <template v-else><button class="btn btn-primary" :disabled="busy" @click="issueCode">确认是我的账号，生成绑定码</button><div v-if="claim.code" class="rounded-xl bg-blue-50 p-5 dark:bg-blue-950/30"><p class="text-sm">在原活动群发送：</p><code class="mt-2 block text-2xl font-bold tracking-widest">绑定 {{ claim.code }}</code><p class="mt-3 text-xs text-gray-500">有效期至 {{ new Date(claim.expires_at).toLocaleString() }}。重新生成会使旧码失效。</p></div></template>
      </section>
      <template v-if="summary">
        <section class="card space-y-3 p-6"><div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">我的绑定</h2><span class="rounded-full bg-gray-100 px-3 py-1 text-xs dark:bg-dark-700">{{ statusLabel }}</span></div><p>绑定状态：{{ summary.bound ? '已绑定' : '尚未绑定' }}</p><p>官网账号：{{ summary.account }}</p><p v-if="summary.qq">QQ：{{ summary.qq }}</p><p class="text-sm" role="status">{{ summary.welcome_text }}</p><p v-if="!summary.bound" class="text-sm text-gray-500">请在活动群发送「绑定」。若使用其他邮箱注册，请将官网「个人资料」中的用户名填写为自己的完整数字 QQ 邮箱，再回群重试。请自行输入完整地址，不要填写星号。</p><router-link v-if="!summary.bound" to="/profile" class="inline-block text-sm text-blue-600">打开个人资料 →</router-link></section>
        <section class="grid gap-4 sm:grid-cols-3"><div class="card p-5"><p class="text-sm text-gray-500">邀请奖励已到账</p><p class="mt-2 text-2xl font-semibold">${{ summary.invitation_paid.toFixed(2) }}</p></div><div class="card p-5"><p class="text-sm text-gray-500">邀请奖励待发放</p><p class="mt-2 text-2xl font-semibold">${{ summary.invitation_pending.toFixed(2) }}</p></div><div class="card p-5"><p class="text-sm text-gray-500">已确认待绑定领取</p><p class="mt-2 text-2xl font-semibold">${{ summary.pending_inviter_binding.toFixed(2) }}</p></div></section>
        <section class="card space-y-3 p-6"><h2 class="text-lg font-semibold">活动说明</h2><p class="text-sm">绑定不限 QQ 等级。符合领取条件且本人 QQ 达到 16 级（一个太阳），可获 $2 体验额度，每人限领一次。</p><p class="text-sm">真实受邀成员入群、完成绑定并达到 16 级后，邀请人可获 $2 邀请余额。邀请人本人不限等级。</p><p class="text-sm">体验额度计入使用余额；邀请奖励计入邀请余额，转入条件请查看现有邀请页面。</p><router-link to="/affiliate" class="inline-block text-sm text-blue-600">查看邀请余额 →</router-link><p class="text-xs text-gray-500">群内可发送：绑定、绑定状态、邀请奖励、活动帮助。领取资格以系统核验结果为准。</p></section>
        <section class="card overflow-hidden p-6"><h2 class="mb-4 text-lg font-semibold">活动奖励记录</h2><p v-if="!summary.rewards.length" class="text-sm text-gray-500">暂无奖励记录。</p><div v-else class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b"><th class="py-3">类型</th><th>金额</th><th>状态</th><th>到账时间</th></tr></thead><tbody><tr v-for="reward in summary.rewards" :key="reward.id" class="border-b border-gray-100 dark:border-dark-700"><td class="py-3">{{ reward.kind === 'newcomer' ? '体验额度' : '邀请余额' }}</td><td>${{ reward.amount.toFixed(2) }}</td><td>{{ reward.status === 'paid' ? '已到账' : '待发放' }}</td><td>{{ reward.paid_at ? new Date(reward.paid_at).toLocaleString() : '—' }}</td></tr></tbody></table></div></section>
      </template>
    </div>
  </AppLayout>
</template>
