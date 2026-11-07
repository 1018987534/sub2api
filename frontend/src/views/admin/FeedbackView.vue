<template>
  <AppLayout>
    <div class="feedback-page mx-auto max-w-5xl space-y-5">
      <header class="feedback-hero flex flex-wrap items-center justify-between gap-4 px-5 py-5 sm:px-7">
        <div class="flex items-center gap-4">
          <FeedbackMailboxIllustration class="h-16 w-16 shrink-0 sm:h-24 sm:w-24" />
          <div>
            <p class="feedback-ink text-xs font-medium">{{ t('feedback.readOnly') }}</p>
            <h1 class="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{{ t('feedback.title') }}</h1>
            <p class="mt-2 max-w-lg text-sm leading-6 text-gray-500 dark:text-dark-300">{{ t('feedback.adminDescription') }}</p>
          </div>
        </div>
        <div class="flex items-center gap-3">
          <span class="feedback-ink text-xs">{{ t('feedback.receivedCount', { count: total }) }}</span>
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="load(page)">{{ t('common.refresh') }}</button>
        </div>
      </header>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="loading && !items.length" role="status" class="feedback-note p-8 text-center text-gray-500">{{ t('common.loading') }}</p>
      <section v-else-if="!items.length && !error" class="feedback-note flex flex-col items-center px-6 py-12 text-center">
        <FeedbackMailboxIllustration class="mb-5 h-28 w-28" />
        <h2 class="font-semibold text-gray-800 dark:text-dark-100">{{ t('feedback.empty') }}</h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ t('feedback.emptyHint') }}</p>
      </section>
      <article v-for="item in items" :key="item.id" class="feedback-letter">
        <div class="space-y-4 p-5 sm:p-6">
          <header class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <div class="mb-2 flex flex-wrap items-center gap-3 text-xs">
                <span class="rounded-md bg-primary-50 px-2 py-1 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ categoryLabel(item.category) }}</span>
                <span class="text-gray-400">{{ t('feedback.letterNumber', { id: item.id }) }}</span>
              </div>
              <h2 class="break-words text-base font-semibold text-gray-900 dark:text-white">{{ item.subject }}</h2>
            </div>
            <span class="feedback-postmark shrink-0">{{ t('feedback.readOnly') }}</span>
          </header>
          <p class="feedback-divider border-t border-dashed pt-4 whitespace-pre-wrap break-words text-sm leading-7 text-gray-700 dark:text-dark-200">{{ item.content }}</p>
          <footer class="feedback-divider flex flex-wrap gap-x-5 gap-y-2 border-t border-dashed pt-3 text-xs text-gray-500 dark:text-dark-400">
            <span class="break-all">{{ t('feedback.submittedBy') }}: {{ item.user_username || item.user_email || `#${item.user_id}` }}<span v-if="item.user_username && item.user_email"> · {{ item.user_email }}</span> · #{{ item.user_id }}</span>
            <time :datetime="item.created_at">{{ t('feedback.submittedAt') }}: {{ formatTime(item.created_at) }}</time>
          </footer>
        </div>
      </article>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" :show-page-size-selector="false" @update:page="load" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import FeedbackMailboxIllustration from '@/components/feedback/FeedbackMailboxIllustration.vue'
import Pagination from '@/components/common/Pagination.vue'
import feedbackAPI, { type FeedbackSubmission } from '@/api/feedback'
import '@/styles/feedback.css'

const { t, locale } = useI18n()
const items = ref<FeedbackSubmission[]>([])
const page = ref(1)
const pageSize = 20
const total = ref(0)
const loading = ref(false)
const error = ref('')
const categoryLabel = (category: FeedbackSubmission['category']) => ({
  problem: t('feedback.categories.problem'),
  suggestion: t('feedback.categories.suggestion'),
  other: t('feedback.categories.other'),
}[category])
const formatTime = (value: string) => new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))

async function load(targetPage = page.value) {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const result = await feedbackAPI.adminList(targetPage, pageSize)
    items.value = result.items
    total.value = result.total
    page.value = result.page
  } catch {
    error.value = t('feedback.loadFailed')
  } finally {
    loading.value = false
  }
}
onMounted(() => { void load() })
</script>
