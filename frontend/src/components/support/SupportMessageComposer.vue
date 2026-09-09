<template>
  <form class="border-t border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900 sm:p-4" @submit.prevent="submit">
    <div class="mb-2 flex flex-wrap gap-1.5">
      <button v-for="emoji in emojis" :key="emoji" type="button" :disabled="sending" class="rounded-lg px-2 py-1 text-lg hover:bg-gray-100 dark:hover:bg-dark-800" :aria-label="t('supportChat.insertEmoji')" @click="emit('update:draft', draft + emoji)">{{ emoji }}</button>
    </div>
    <div class="overflow-hidden rounded-xl border border-gray-200 bg-gray-50 focus-within:border-primary-500 focus-within:ring-2 focus-within:ring-primary-500/20 dark:border-dark-700 dark:bg-dark-800">
      <div v-if="file" class="flex items-start gap-3 px-3 pt-3" data-testid="attachment-preview">
        <img v-if="previewUrl" :src="previewUrl" :alt="file.name" class="max-h-36 max-w-[min(100%,16rem)] rounded-lg object-contain" />
        <Icon v-else name="document" size="lg" class="shrink-0 text-gray-400" />
        <div class="min-w-0 flex-1 text-xs text-gray-500"><p class="truncate">{{ file.name }}</p><p class="mt-1">{{ formatBytes(file.size) }}</p></div>
        <button type="button" :disabled="sending" :aria-label="t('supportChat.removeAttachment')" class="rounded px-2 text-gray-500 hover:text-red-500" @click="emit('update:file', null)">×</button>
      </div>
      <textarea ref="textarea" :value="draft" :aria-label="t('supportChat.messageLabel')" :placeholder="t('supportChat.placeholder')" :disabled="sending" class="block min-h-20 w-full resize-none border-0 bg-transparent px-3 py-3 text-sm outline-none" @input="emit('update:draft', ($event.target as HTMLTextAreaElement).value)" @paste="pasteImage" @keydown="onKeydown" />
    </div>
    <div class="mt-2 flex items-center justify-between gap-3">
      <div class="flex min-w-0 items-center gap-2">
        <input ref="fileInput" type="file" class="hidden" accept="image/gif,image/jpeg,image/png,image/webp,text/plain,application/pdf,application/msword,application/vnd.openxmlformats-officedocument.wordprocessingml.document,.doc,.docx" @change="pickFile" />
        <button type="button" :disabled="sending" class="btn btn-secondary btn-sm" :aria-label="t('supportChat.attachFile')" @click="fileInput?.click()"><Icon name="upload" size="sm" /></button>
        <span class="hidden text-xs text-gray-400 sm:inline">{{ t('supportChat.composerHint') }}</span>
      </div>
      <div class="flex items-center gap-3">
        <span class="text-xs" :class="characterCount > 10000 ? 'text-red-500' : 'text-gray-400'">{{ characterCount }}/10000</span>
        <button class="btn btn-primary" type="submit" :disabled="!canSend">{{ sending ? t('common.submitting') : t('supportChat.send') }}</button>
      </div>
    </div>
    <p v-if="error" role="alert" class="mt-2 text-xs text-red-500">{{ error }}</p>
  </form>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'

const props = defineProps<{ draft: string; file: File | null; sending: boolean; error: string }>()
const emit = defineEmits<{
  'update:draft': [value: string]
  'update:file': [value: File | null]
  'update:error': [value: string]
  send: []
}>()
const { t } = useI18n()
const fileInput = ref<HTMLInputElement | null>(null)
const textarea = ref<HTMLTextAreaElement | null>(null)
const previewUrl = ref('')
const emojis = ['👍', '✅', '🎉', '🙏', '😊', '🤝', '💡', '📌', '⏳', '❤️']
const imageTypes = new Set(['image/gif', 'image/jpeg', 'image/png', 'image/webp'])
const characterCount = computed(() => Array.from(props.draft).length)
const canSend = computed(() => !props.sending && characterCount.value <= 10000 && !!(props.draft.trim() || props.file))
const formatBytes = (size: number) => size < 1024 * 1024 ? `${Math.ceil(size / 1024)} KB` : `${(size / 1024 / 1024).toFixed(1)} MB`

function releasePreview() {
  if (previewUrl.value) URL.revokeObjectURL(previewUrl.value)
  previewUrl.value = ''
}
watch(() => props.file, file => {
  releasePreview()
  if (file && imageTypes.has(file.type)) previewUrl.value = URL.createObjectURL(file)
}, { immediate: true })
onBeforeUnmount(releasePreview)

function selectFile(file: File) {
  if (props.sending) return
  if (file.size > 4 * 1024 * 1024) { emit('update:error', t('supportChat.fileTooLarge')); return }
  if (file.type.startsWith('image/') && !imageTypes.has(file.type)) { emit('update:error', t('supportChat.unsupportedImage')); return }
  emit('update:file', file)
  emit('update:error', '')
  textarea.value?.focus()
}
function pickFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (file) selectFile(file)
}
function pasteImage(event: ClipboardEvent) {
  const clipboard = event.clipboardData
  const item = Array.from(clipboard?.items ?? []).find(item => item.kind === 'file' && item.type.startsWith('image/'))
  const file = item?.getAsFile() ?? Array.from(clipboard?.files ?? []).find(file => file.type.startsWith('image/'))
  if (!file) return
  // Consume the image paste so HTML/base64 clipboard fallbacks cannot become message text.
  event.preventDefault()
  selectFile(file)
}
function submit() { if (canSend.value) emit('send') }
function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey || event.altKey || event.ctrlKey || event.metaKey || event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  submit()
}
</script>
