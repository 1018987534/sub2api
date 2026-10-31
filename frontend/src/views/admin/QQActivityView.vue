<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import QQActivityNotice from '@/components/user/QQActivityNotice.vue'
import { qqActivityAPI, type QQActivityConfig, type QQAdminReport } from '@/api/qqActivity'

const report = ref<QQAdminReport | null>(null)
const busy = ref(false)
const feedback = ref('')
const failed = ref(false)
const filter = ref('')
const settings = reactive({ newcomer_enabled: true, invitation_enabled: true, auto_process_join: true, group_messages_enabled: true })
const bindingDisplay = reactive({ group_number: '', activity_title: '', activity_text: '', binding_intro: '', binding_note: '' })
const previewConfig = computed(() => report.value ? { ...report.value.config, binding_display: bindingDisplay } : undefined)
const review = reactive({ invitee: '', inviter: '', group_id: '', evidence: '' })
const statusLabel = computed(() => ({ preparing: '准备中', active: '已开启', paused: '已关闭', ended: '已结束' }[report.value?.config.status || 'preparing']))
const members = computed(() => (report.value?.members || []).filter(m => !filter.value || m.qq.includes(filter.value) || m.account.includes(filter.value) || m.match_state.includes(filter.value)))
const matchLabel = (s: string) => ({ unmatched: '待匹配', ambiguous: '匹配冲突', account_unavailable: '账号待处理', conflict: '绑定冲突', matched: '已匹配', verified: '待自动绑定', confirmation_required: '待自动绑定', bound: '已绑定', unbound: '已解绑', needs_verification: '待邮箱验证' }[s] || s)
const invitationLabel = (s: string) => ({ awaiting_evidence: '待核验邀请', awaiting_level: '待受邀人资格核验', awaiting_newcomer: '待新人奖励到账', awaiting_inviter: '待邀请人绑定', pending: '待发放', paid: '已到账', ineligible: '待处理' }[s] || s)
async function load() {
  report.value = await qqActivityAPI.admin()
  for (const key of Object.keys(settings) as (keyof typeof settings)[]) settings[key] = report.value.config[key]
  Object.assign(bindingDisplay, report.value.config.binding_display)
}
async function refresh() { busy.value = true; feedback.value = ''; try { await load() } catch { failed.value = true; feedback.value = '读取活动配置失败，请稍后重试。' } finally { busy.value = false } }
async function act(data: Record<string, unknown>, success: string) {
  busy.value = true; feedback.value = ''; failed.value = false
  try { await qqActivityAPI.control(data); await load(); feedback.value = success }
  catch { failed.value = true; feedback.value = data.action === 'binding_display' ? '文案保存未完成，请检查字数限制后重试。' : '操作未完成，请核对活动状态、绑定记录和邀请证据后重试。' }
  finally { busy.value = false }
}
function toggle() {
  if (!report.value) return
  const active = report.value.config.status === 'active'
  void act({ action: 'status', status: active ? 'paused' : 'active' }, active ? '活动已关闭，绑定和查询保留，奖励暂停入账。' : '活动已开启，已确认的待发奖励会继续结算。')
}
function exportMembers() {
  if (!report.value) return
  const blob = new Blob([JSON.stringify(report.value, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob); const a = document.createElement('a'); a.href = url; a.download = 'qq-activity-redacted.json'; a.click(); URL.revokeObjectURL(url)
}
const configured = computed(() => !!report.value?.config.started_at && report.value.config.status !== 'ended')
const startedAt = (c: QQActivityConfig) => c.started_at ? new Date(c.started_at).toLocaleString() : '尚未开启'
onMounted(refresh)
</script>

<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-4"><div><h1 class="text-2xl font-semibold">QQ 活动管理</h1><p class="mt-2 text-sm text-gray-500">管理群成员绑定、活动开关和奖励结算。</p></div><div class="flex gap-3"><button class="btn btn-secondary" :disabled="busy" @click="refresh">刷新</button><button class="btn btn-secondary" :disabled="!report" @click="exportMembers">导出脱敏记录</button></div></div>
      <p v-if="feedback" :role="failed ? 'alert' : 'status'" class="rounded-xl p-4 text-sm" :class="failed ? 'bg-red-50 text-red-700 dark:bg-red-950/30 dark:text-red-300' : 'bg-green-50 text-green-700 dark:bg-green-950/30 dark:text-green-300'">{{ feedback }}</p>
      <template v-if="report">
        <section class="card space-y-4 p-6"><div class="flex flex-wrap items-center justify-between gap-4"><div><h2 class="text-lg font-semibold">活动开关 <span class="ml-2 rounded-full bg-gray-100 px-3 py-1 text-xs dark:bg-dark-700">{{ statusLabel }}</span></h2><p class="mt-2 text-sm text-gray-500">关闭时保留绑定、查询和已确认奖励，暂停奖励入账。重新开启后继续结算。</p></div><button class="btn" :class="report.config.status === 'active' ? 'btn-secondary' : 'btn-primary'" :disabled="busy || !configured" @click="toggle">{{ report.config.status === 'active' ? '关闭活动' : '开启活动' }}</button></div><p class="text-xs text-gray-500">首次开启：{{ startedAt(report.config) }}。首次初始化由服务端核对完整群名单和网站账号基线后完成。</p><p v-if="report.config.status === 'preparing'" class="text-sm text-amber-700">初始化尚未完成，暂不能开启奖励。</p><p v-if="report.config.status === 'ended'" class="text-sm text-gray-500">本期已结束，已成立的待发奖励仍可结算。</p></section>
        <section class="card space-y-5 p-6"><h2 class="text-lg font-semibold">功能配置</h2><div class="grid gap-5 md:grid-cols-2"><label class="flex items-start gap-3"><input v-model="settings.newcomer_enabled" type="checkbox" class="mt-1" :disabled="busy"><span><span class="font-medium">新人 $2 体验额度</span><span class="mt-1 block text-sm text-gray-500">本人资格满足且 QQ 达到 16 级后发放；关闭时保留已确认的待发记录。</span></span></label><label class="flex items-start gap-3"><input v-model="settings.invitation_enabled" type="checkbox" class="mt-1" :disabled="busy"><span><span class="font-medium">邀请 $2 余额</span><span class="mt-1 block text-sm text-gray-500">受邀人入群、绑定且达到 16 级，新人体验额度实际到账后才自动发放；邀请人本人不限等级。</span></span></label><label class="flex items-start gap-3"><input v-model="settings.auto_process_join" type="checkbox" class="mt-1" :disabled="busy"><span><span class="font-medium">自动匹配并绑定群成员</span><span class="mt-1 block text-sm text-gray-500">自动匹配注册 QQ 邮箱与群成员，保留历史绑定。其他邮箱需在官网验证 QQ 邮箱；主动解绑后不会自动绑回。关闭后仍登记入群及邀请证据。</span></span></label><label class="flex items-start gap-3"><input v-model="settings.group_messages_enabled" type="checkbox" class="mt-1" :disabled="busy"><span><span class="font-medium">普通群内活动提示</span><span class="mt-1 block text-sm text-gray-500">关闭后不发送普通活动提示；实际到账的新人和邀请奖励仍逐笔通知，失败自动重试。</span></span></label></div><div class="flex flex-wrap gap-3"><button class="btn btn-primary" :disabled="busy" @click="act({ action: 'settings', settings: { ...settings } }, '配置已保存。')">保存配置</button><button class="btn btn-secondary" :disabled="busy" @click="act({ action: 'retry' }, '已重新处理符合条件的待发奖励。')">重试待发奖励</button></div><p class="text-xs text-gray-500">奖励金额固定 $2，受邀人和新人本人等级门槛固定 16 级。邮箱和邮箱形式用户名在活动输出中均已脱敏。</p></section>
        <section class="card space-y-4 p-6">
          <div><h2 class="text-lg font-semibold">QQ 绑定弹窗文案</h2><p class="mt-2 text-sm text-gray-500">保存后用户下次打开弹窗即可看到最新内容。支持纯文本和换行；活动状态随上方活动开关显示。</p></div>
          <form class="space-y-4" @submit.prevent="act({ action: 'binding_display', binding_display: { ...bindingDisplay } }, '绑定弹窗文案已保存。')">
            <label class="block space-y-2 text-sm"><span class="font-medium">展示群号</span><input v-model="bindingDisplay.group_number" class="input w-full" aria-label="展示群号" maxlength="200" :disabled="busy" placeholder="留空使用已登记的活动群号"><span class="block text-xs text-gray-500">可填写多个群号及说明，仅用于页面展示。</span></label>
            <label class="block space-y-2 text-sm"><span class="font-medium">活动标题</span><input v-model="bindingDisplay.activity_title" class="input w-full" aria-label="活动标题" maxlength="80" :disabled="busy"></label>
            <label class="block space-y-2 text-sm"><span class="font-medium">当前活动说明</span><textarea v-model="bindingDisplay.activity_text" class="input w-full" aria-label="当前活动说明" rows="5" maxlength="4000" :disabled="busy"></textarea></label>
            <label class="block space-y-2 text-sm"><span class="font-medium">绑定前说明</span><textarea v-model="bindingDisplay.binding_intro" class="input w-full" aria-label="绑定前说明" rows="3" maxlength="1000" :disabled="busy"></textarea></label>
            <label class="block space-y-2 text-sm"><span class="font-medium">绑定底部提示</span><textarea v-model="bindingDisplay.binding_note" class="input w-full" aria-label="绑定底部提示" rows="3" maxlength="1000" :disabled="busy"></textarea></label>
            <p class="text-xs text-gray-500">说明留空可隐藏对应文案。奖励金额与领取资格由实际活动规则核验。</p>
            <button class="btn btn-primary" :disabled="busy">保存弹窗文案</button>
          </form>
          <div class="space-y-2"><h3 class="text-sm font-medium">群号与活动预览</h3><QQActivityNotice :config="previewConfig" /></div>
        </section>
        <section class="grid grid-cols-2 gap-4 xl:grid-cols-4"><div class="card p-5"><p class="text-sm text-gray-500">登记群成员 / 已绑定</p><p class="mt-2 text-xl font-semibold">{{ report.members_count }} / {{ report.bindings_count }}</p></div><div class="card p-5"><p class="text-sm text-gray-500">网站初始化账号</p><p class="mt-2 text-xl font-semibold">{{ report.baseline_users }}</p></div><div class="card p-5"><p class="text-sm text-gray-500">体验额度 / 邀请余额已发</p><p class="mt-2 text-xl font-semibold">${{ report.newcomer_paid.toFixed(2) }} / ${{ report.invitation_paid.toFixed(2) }}</p></div><div class="card p-5"><p class="text-sm text-gray-500">已确认待发 / 待通知</p><p class="mt-2 text-xl font-semibold">${{ report.pending_amount.toFixed(2) }} / {{ report.unsent_messages }}</p></div></section>
        <section class="card p-6"><div class="mb-4 flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">群成员与绑定记录</h2><input v-model="filter" class="input max-w-xs" placeholder="搜索 QQ 或脱敏账号" aria-label="搜索成员"></div><div class="overflow-x-auto"><table class="w-full whitespace-nowrap text-left text-sm"><thead><tr class="border-b"><th class="py-3 pr-5">QQ / 群号</th><th class="pr-5">官网账号</th><th class="pr-5">匹配状态</th><th class="pr-5">等级</th><th>成员状态</th></tr></thead><tbody><tr v-for="member in members" :key="`${member.group_id}:${member.qq}`" class="border-b border-gray-100 dark:border-dark-700"><td class="py-3 pr-5">{{ member.qq }}<span class="block text-xs text-gray-400">{{ member.group_id }}</span></td><td class="pr-5">{{ member.account }}</td><td class="pr-5">{{ member.bound ? '已绑定' : matchLabel(member.match_state) }}</td><td class="pr-5">{{ member.level ?? '待核验' }}</td><td>{{ member.robot ? '机器人' : member.in_group ? '在群' : '已离群' }}</td></tr></tbody></table></div></section>
        <section class="card space-y-4 p-6"><h2 class="text-lg font-semibold">邀请核验</h2><p class="text-sm text-gray-500">只有可核验的真实邀请才能确认。确认不会豁免受邀人的入群、绑定、16 级和新人奖励实际到账条件；每位受邀人只结算一次。</p><form class="grid gap-3 md:grid-cols-3" @submit.prevent="act({ action: 'verify_invitation', ...review }, '邀请证据已登记，符合条件的奖励已进入结算。')"><input v-model="review.invitee" required class="input" placeholder="受邀人 QQ" aria-label="受邀人 QQ"><input v-model="review.inviter" required class="input" placeholder="邀请人 QQ" aria-label="邀请人 QQ"><input v-model="review.group_id" required class="input" placeholder="活动群号" aria-label="活动群号"><textarea v-model="review.evidence" required minlength="8" maxlength="2000" class="input md:col-span-3" placeholder="可核验的邀请证据、记录编号与核验说明" aria-label="邀请证据"></textarea><button class="btn btn-primary w-fit" :disabled="busy || !configured">登记核验结果</button></form><div v-if="report.invitations.length" class="overflow-x-auto"><table class="w-full whitespace-nowrap text-left text-sm"><thead><tr class="border-b"><th class="py-3 pr-5">受邀人</th><th class="pr-5">邀请人</th><th class="pr-5">群号</th><th>状态</th></tr></thead><tbody><tr v-for="invite in report.invitations" :key="invite.invitee" class="border-b border-gray-100 dark:border-dark-700"><td class="py-3 pr-5">{{ invite.invitee }}</td><td class="pr-5">{{ invite.inviter || '待核验' }}</td><td class="pr-5">{{ invite.group_id }}</td><td>{{ invitationLabel(invite.status) }}</td></tr></tbody></table></div></section>
      </template>
    </div>
  </AppLayout>
</template>
