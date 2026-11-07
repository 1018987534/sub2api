<template>
  <AppLayout>
    <div class="mx-auto max-w-5xl space-y-5">
      <header class="flex items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('feedback.title') }}</h1>
          <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ t('feedback.adminDescription') }}</p>
        </div>
        <button type="button" class="btn btn-secondary" :disabled="loading" @click="load(page)">{{ t('common.refresh') }}</button>
      </header>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
      <p v-if="loading && !items.length" role="status" class="card p-8 text-center text-gray-500">{{ t('common.loading') }}</p>
      <p v-else-if="!items.length && !error" class="card p-8 text-center text-gray-500">{{ t('feedback.empty') }}</p>
      <article v-for="item in items" :key="item.id" class="card space-y-3 p-5 sm:p-6">
        <header class="flex flex-wrap items-center gap-3">
          <span class="rounded-full bg-primary-50 px-3 py-1 text-xs text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ categoryLabel(item.category) }}</span>
          <h2 class="break-words font-semibold text-gray-900 dark:text-white">{{ item.subject }}</h2>
          <span class="ml-auto text-xs text-gray-400">#{{ item.id }}</span>
        </header>
        <p class="whitespace-pre-wrap break-words text-sm leading-7 text-gray-700 dark:text-dark-200">{{ item.content }}</p>
        <footer class="flex flex-wrap gap-x-5 gap-y-1 border-t border-gray-100 pt-3 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400">
          <span>{{ t('feedback.submittedBy') }}: {{ item.user_username || item.user_email || `#${item.user_id}` }}<span v-if="item.user_username && item.user_email"> · {{ item.user_email }}</span> · #{{ item.user_id }}</span>
          <time :datetime="item.created_at">{{ t('feedback.submittedAt') }}: {{ formatTime(item.created_at) }}</time>
        </footer>
      </article>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" :show-page-size-selector="false" @update:page="load" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import feedbackAPI, { type FeedbackSubmission } from '@/api/feedback'

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
