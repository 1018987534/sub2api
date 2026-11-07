<template>
  <AppLayout>
    <div class="feedback-page mx-auto max-w-5xl space-y-5">
      <header class="feedback-hero flex items-center justify-between gap-4 px-5 py-3 sm:px-7">
        <div class="min-w-0">
          <p class="feedback-ink text-xs font-medium tracking-wider">{{ t('feedback.eyebrow') }}</p>
          <h1 class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{{ t('feedback.title') }}</h1>
          <p class="mt-2 max-w-xl text-sm leading-6 text-gray-600 dark:text-dark-300">{{ t('feedback.description') }}</p>
        </div>
        <FeedbackMailboxIllustration class="h-16 w-16 shrink-0 sm:h-24 sm:w-24" />
      </header>

      <div v-if="submitted" role="status" class="flex items-center gap-3 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200">
        <svg class="h-5 w-5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m5 12 4 4L19 6" stroke-linecap="round" stroke-linejoin="round" /></svg>
        {{ t('feedback.success') }}
      </div>

      <div class="grid items-start gap-5 lg:grid-cols-3">
        <form class="feedback-letter lg:col-span-2" @submit.prevent="submit">
          <div class="space-y-4 p-5">
            <div class="flex items-center justify-between gap-4">
              <div>
                <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('feedback.formTitle') }}</h2>
                <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('feedback.recipient') }}</p>
              </div>
              <span class="feedback-postmark shrink-0">{{ t('feedback.stamp') }}</span>
            </div>
            <div class="grid gap-4 sm:grid-cols-[160px_1fr]">
              <div>
                <label for="feedback-category" class="mb-2 block text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('feedback.category') }}</label>
                <select id="feedback-category" v-model="category" class="input feedback-input w-full" :disabled="sending">
                  <option v-for="option in categories" :key="option.value" :value="option.value">{{ option.label }}</option>
                </select>
              </div>
              <div>
                <label for="feedback-subject" class="mb-2 block text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('feedback.subject') }}</label>
                <input id="feedback-subject" v-model="subject" type="text" class="input feedback-input w-full" required maxlength="100" :placeholder="t('feedback.subjectPlaceholder')" :disabled="sending" />
              </div>
            </div>
            <div>
              <label for="feedback-details" class="mb-2 block text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('feedback.details') }}</label>
              <textarea id="feedback-details" v-model="details" class="input feedback-input feedback-writing w-full resize-y" rows="5" required maxlength="5000" aria-describedby="feedback-privacy" :placeholder="t('feedback.detailsPlaceholder')" :disabled="sending" />
              <div class="mt-2 flex flex-wrap justify-between gap-2 text-xs text-gray-500 dark:text-dark-400">
                <p id="feedback-privacy">{{ t('feedback.privacyHint') }}</p>
                <span>{{ t('feedback.characterCount', { count: details.length, max: 5000 }) }}</span>
              </div>
            </div>
            <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
            <div class="feedback-divider flex flex-wrap items-center justify-between gap-3 border-t border-dashed pt-4">
              <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('feedback.deliveryHint') }}</p>
              <button type="submit" class="btn feedback-submit w-full gap-2 sm:w-auto" :disabled="sending">
                <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="3" y="6" width="18" height="14" rx="2" /><path d="m3 7 9 7 9-7M12 2v7m-3-3 3 3 3-3" stroke-linecap="round" stroke-linejoin="round" /></svg>
                {{ sending ? t('feedback.submitting') : t('feedback.submit') }}
              </button>
            </div>
          </div>
        </form>

        <aside class="feedback-note space-y-5 p-5">
          <section>
            <p class="feedback-ink mb-3 flex items-center gap-2 text-xs font-medium">
              <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2" /><path d="m3 6 9 7 9-7" stroke-linecap="round" stroke-linejoin="round" /></svg>
              {{ t('feedback.oneWay') }}
            </p>
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('feedback.viewOnlyTitle') }}</h2>
            <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ t('feedback.viewOnlyDescription') }}</p>
          </section>
          <section class="feedback-divider border-t border-dashed pt-5">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('feedback.tipsTitle') }}</h2>
            <ul class="mt-3 space-y-3 text-sm leading-6 text-gray-500 dark:text-dark-400">
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
import FeedbackMailboxIllustration from '@/components/feedback/FeedbackMailboxIllustration.vue'
import feedbackAPI from '@/api/feedback'
import '@/styles/feedback.css'

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
