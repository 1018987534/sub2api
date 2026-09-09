<template>
  <AppLayout>
    <section class="mx-auto flex h-[calc(100vh-8rem)] max-w-5xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900">
      <header class="flex items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
        <div><h1 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('supportChat.title') }}</h1><p class="text-sm text-gray-500 dark:text-dark-400">{{ t('supportChat.description') }}</p></div>
        <div class="flex items-center gap-2 text-xs"><span class="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2.5 py-1 font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-200"><span class="h-2 w-2 rounded-full bg-emerald-500" />{{ t('supportChat.connected') }}</span><button class="btn btn-secondary btn-sm" :disabled="loading" @click="load">{{ t('common.refresh') }}</button></div>
      </header>
      <div ref="messagePane" class="min-h-0 flex-1 overflow-y-auto bg-gray-50 p-5 dark:bg-dark-950/60">
        <div v-if="loading && !messages.length" class="flex h-full items-center justify-center text-sm text-gray-500">{{ t('common.loading') }}</div>
        <div v-else-if="!messages.length" class="flex h-full flex-col items-center justify-center text-center text-gray-500 dark:text-dark-400"><div class="mb-3 rounded-2xl bg-primary-50 p-4 text-primary-600 dark:bg-primary-900/20 dark:text-primary-300"><Icon name="chat" size="lg" /></div><p class="font-medium text-gray-700 dark:text-dark-200">{{ t('supportChat.emptyTitle') }}</p><p class="mt-1 text-sm">{{ t('supportChat.emptyDescription') }}</p></div>
        <div v-else class="space-y-4"><article v-for="message in messages" :key="message.id" class="flex" :class="message.sender_type === 'user' ? 'justify-end' : 'justify-start'"><div class="max-w-[82%] rounded-2xl px-4 py-3 text-sm shadow-sm" :class="message.sender_type === 'user' ? 'bg-primary-600 text-white' : 'border border-gray-200 bg-white text-gray-800 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-100'"><p v-if="message.content" class="whitespace-pre-wrap break-words">{{ message.content }}</p><button v-if="message.attachment" type="button" class="mt-2 flex items-center gap-2 underline" @click="openAttachment(message.attachment)"><Icon name="document" size="sm" />{{ message.attachment.filename }} <span class="text-xs opacity-70">({{ formatBytes(message.attachment.size_bytes) }})</span></button><time class="mt-1 block text-[11px] opacity-70">{{ formatTime(message.created_at) }}</time></div></article></div>
      </div>
      <SupportMessageComposer v-model:draft="draft" v-model:file="file" v-model:error="error" :sending="sending" @send="send" />
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import AppLayout from '@/components/layout/AppLayout.vue'
import SupportMessageComposer from '@/components/support/SupportMessageComposer.vue'
import supportChatAPI, { type SupportMessage } from '@/api/supportChat'

const { t, locale } = useI18n(); const messages = ref<SupportMessage[]>([]); const draft = ref(''); const file = ref<File | null>(null); const error = ref(''); const loading = ref(false); const sending = ref(false); const messagePane = ref<HTMLElement | null>(null); let timer: ReturnType<typeof setInterval> | undefined
const formatTime = (value: string) => new Intl.DateTimeFormat(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value))
const formatBytes = (value: number) => value < 1024 * 1024 ? `${Math.ceil(value / 1024)} KB` : `${(value / 1024 / 1024).toFixed(1)} MB`
async function openAttachment(attachment: NonNullable<SupportMessage['attachment']>) { const blob = await supportChatAPI.fetchAttachment(attachment.id); const url = URL.createObjectURL(blob); window.open(url, '_blank', 'noopener'); setTimeout(() => URL.revokeObjectURL(url), 60000) }
async function scrollBottom() { await nextTick(); if (messagePane.value) messagePane.value.scrollTop = messagePane.value.scrollHeight }
async function load() { loading.value = true; try { const page = await supportChatAPI.messages(); messages.value = page.items; await supportChatAPI.read(); await scrollBottom() } finally { loading.value = false } }
async function send() { const content = draft.value.trim(); if ((!content && !file.value) || sending.value) return; sending.value = true; error.value = ''; try { const message = await supportChatAPI.send(content, file.value || undefined); messages.value = [...messages.value, message]; draft.value = ''; file.value = null; await scrollBottom() } catch (err: any) { error.value = err?.message || t('supportChat.sendFailed') } finally { sending.value = false } }
onMounted(async () => { await load(); timer = setInterval(() => { if (document.visibilityState === 'visible' && !sending.value) void load() }, 15000) })
onBeforeUnmount(() => { if (timer) clearInterval(timer) })
</script>
