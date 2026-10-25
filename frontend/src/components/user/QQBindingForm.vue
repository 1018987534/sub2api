<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { qqActivityAPI, type QQBindingStatus, type QQEmailChallenge } from '@/api/qqActivity'

const emit = defineEmits<{ verified: [status: QQBindingStatus] }>()
const email = ref('')
const code = ref('')
const challenge = ref<QQEmailChallenge | null>(null)
const busy = ref(false)
const error = ref('')
const remaining = ref(0)
let timer: ReturnType<typeof setInterval> | undefined
let disposed = false
const validEmail = computed(() => /^[1-9][0-9]{4,14}(@qq\.com)?$/i.test(email.value.trim()))
const validCode = computed(() => /^[0-9]{6}$/.test(code.value.trim()))
watch(email, () => { challenge.value = null; code.value = ''; error.value = '' })
function message(err: unknown, fallback: string) {
  return typeof err === 'object' && err !== null && 'message' in err && typeof err.message === 'string' ? err.message : fallback
}
async function sendCode() {
  if (busy.value || remaining.value || !validEmail.value) return
  busy.value = true; error.value = ''
  try {
    challenge.value = await qqActivityAPI.sendBindingCode(email.value.trim())
    if (disposed) return
    code.value = ''
    const until = Date.now() + challenge.value.resend_after * 1000
    clearInterval(timer)
    remaining.value = challenge.value.resend_after
    timer = setInterval(() => {
      remaining.value = Math.max(0, Math.ceil((until - Date.now()) / 1000))
      if (!remaining.value) clearInterval(timer)
    }, 1000)
  } catch (err) { error.value = message(err, '验证码发送失败，请稍后重试。') }
  finally { busy.value = false }
}
async function verify() {
  if (busy.value || !challenge.value || !validCode.value) return
  busy.value = true; error.value = ''
  try {
    const status = await qqActivityAPI.verifyBinding(email.value.trim(), challenge.value.challenge, code.value.trim())
    if (disposed) return
    if (!status.bound) throw new Error('尚未完成绑定，请稍后重试。')
    code.value = ''; challenge.value = null
    window.dispatchEvent(new Event('qq-binding-changed'))
    emit('verified', status)
  } catch (err) { error.value = message(err, '验证失败，请核对验证码后重试。') }
  finally { busy.value = false }
}
onBeforeUnmount(() => { disposed = true; clearInterval(timer) })
</script>

<template>
  <form class="space-y-4" @submit.prevent="verify">
    <p class="text-sm text-gray-500 dark:text-dark-400">注册 QQ 邮箱能匹配到群成员时会自动绑定，已有绑定继续有效。使用其他邮箱注册的，请在此验证本人 QQ 邮箱。</p>
    <label class="block space-y-2 text-sm">
      <span class="font-medium">QQ 邮箱</span>
      <input v-model="email" name="qq-email" class="input w-full" type="text" inputmode="email" autocomplete="off" maxlength="80" placeholder="输入 QQ 号或完整数字 QQ 邮箱" :disabled="busy" required>
    </label>
    <div class="flex flex-wrap gap-2">
      <button class="btn btn-secondary" type="button" :disabled="busy || remaining > 0 || !validEmail" @click="sendCode">{{ remaining ? `${remaining} 秒后重发` : '发送验证码' }}</button>
    </div>
    <p v-if="challenge" role="status" class="text-sm text-gray-600 dark:text-dark-300">验证码已发送至 {{ challenge.qq_email }}，10 分钟内有效。请在此填写，不要发到 QQ 群。</p>
    <label class="block space-y-2 text-sm">
      <span class="font-medium">邮箱验证码</span>
      <input v-model="code" name="qq-code" class="input w-full" inputmode="numeric" autocomplete="one-time-code" maxlength="6" pattern="[0-9]{6}" placeholder="输入邮件中的 6 位验证码" :disabled="busy || !challenge" required>
    </label>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <button class="btn btn-primary" type="submit" :disabled="busy || !challenge || !validCode">{{ busy ? '处理中…' : '验证并绑定' }}</button>
    <p class="text-xs text-gray-500">每个 QQ 只能关联一个官网账号。注册邮箱保持不变，修改个人资料中的用户名不再用于绑定。</p>
  </form>
</template>
