<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-6">
      <header>
        <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('feedback.title') }}</h1>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ t('feedback.description') }}</p>
      </header>

      <div v-if="submitted" role="status" class="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200">
        {{ t('feedback.success') }}
      </div>

      <div class="grid gap-6 lg:grid-cols-3">
        <form class="card space-y-5 p-5 sm:p-6 lg:col-span-2" @submit.prevent="submit">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('feedback.formTitle') }}</h2>
          <div>
            <label for="feedback-category" class="mb-2 block text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('feedback.category') }}</label>
            <select id="feedback-category" v-model="category" class="input w-full" :disabled="sending">
              <option v-for="option in categories" :key="option.value" :value="option.value">{{ option.label }}</option>
            </select>
          </div>
          <div>
            <label for="feedback-subject" class="mb-2 block text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('feedback.subject') }}</label>
            <input id="feedback-subject" v-model="subject" type="text" class="input w-full" required maxlength="100" :placeholder="t('feedback.subjectPlaceholder')" :disabled="sending" />
          </div>
          <div>
            <label for="feedback-details" class="mb-2 block text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('feedback.details') }}</label>
            <textarea id="feedback-details" v-model="details" class="input min-h-48 w-full resize-y" rows="8" required maxlength="5000" aria-describedby="feedback-privacy" :placeholder="t('feedback.detailsPlaceholder')" :disabled="sending" />
            <div class="mt-2 flex flex-wrap justify-between gap-2 text-xs text-gray-500 dark:text-dark-400">
              <p id="feedback-privacy">{{ t('feedback.privacyHint') }}</p>
              <span>{{ t('feedback.characterCount', { count: details.length, max: 5000 }) }}</span>
            </div>
          </div>
          <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
          <button type="submit" class="btn btn-primary w-full sm:w-auto" :disabled="sending">
            {{ sending ? t('feedback.submitting') : t('feedback.submit') }}
          </button>
        </form>

        <aside class="space-y-5">
          <section class="card p-5">
            <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('feedback.viewOnlyTitle') }}</h2>
            <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('feedback.viewOnlyDescription') }}</p>
          </section>
          <section class="rounded-xl bg-gray-50 p-5 dark:bg-dark-800/60">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('feedback.tipsTitle') }}</h2>
            <ul class="mt-3 list-disc space-y-3 pl-4 text-sm leading-6 text-gray-500 dark:text-dark-400">
              <li>{{ t('feedback.tipsSteps') }}</li>
              <li>{{ t('feedback.tipsSuggestion') }}</li>
            </ul>
          </section>
        </aside>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import feedbackAPI from '@/api/feedback'

const { t } = useI18n()
const category = ref<'problem' | 'suggestion' | 'other'>('problem')
const subject = ref('')
const details = ref('')
const sending = ref(false)
const submitted = ref(false)
const error = ref('')
let pendingSubmission: { fingerprint: string; key: string } | undefined
const categories = computed(() => [
  { value: 'problem', label: t('feedback.categories.problem') },
  { value: 'suggestion', label: t('feedback.categories.suggestion') },
  { value: 'other', label: t('feedback.categories.other') },
])

async function submit() {
  if (sending.value) return
  error.value = ''
  submitted.value = false
  const title = subject.value.trim()
  const content = details.value.trim()
  if (!title || !content) {
    error.value = t('feedback.required')
    return
  }
  if (title.length > 100 || content.length > 5000) {
    error.value = t('feedback.tooLong')
    return
  }
  const input = { category: category.value, subject: title, content }
  const fingerprint = JSON.stringify(input)
  if (pendingSubmission?.fingerprint !== fingerprint) {
    pendingSubmission = { fingerprint, key: crypto.randomUUID() }
  }
  sending.value = true
  try {
    await feedbackAPI.create(input, pendingSubmission.key)
    subject.value = ''
    details.value = ''
    submitted.value = true
    pendingSubmission = undefined
  } catch (err: unknown) {
    const message = (err as { message?: string } | null)?.message
    error.value = message || t('feedback.sendFailed')
  } finally {
    sending.value = false
  }
}
</script>
