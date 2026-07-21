<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.reengagement.title')"
    width="normal"
    @close="emit('close')"
  >
    <form id="user-reengagement-email-form" class="space-y-5" @submit.prevent="handleSubmit">
      <div>
        <span class="input-label">{{ t('admin.users.reengagement.sendScope') }}</span>
        <div class="grid grid-cols-2 overflow-hidden rounded-md border border-gray-300 dark:border-dark-600">
          <button
            type="button"
            class="min-h-10 px-3 text-sm font-medium transition-colors"
            :class="mode === 'selected'
              ? 'bg-primary-600 text-white'
              : 'bg-white text-gray-700 hover:bg-gray-50 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'"
            :disabled="selectedIds.length === 0"
            data-test="mode-selected"
            @click="mode = 'selected'"
          >
            {{ t('admin.users.reengagement.selectedScope', { count: selectedIds.length }) }}
          </button>
          <button
            type="button"
            class="min-h-10 border-l border-gray-300 px-3 text-sm font-medium transition-colors dark:border-dark-600"
            :class="mode === 'all'
              ? 'bg-primary-600 text-white'
              : 'bg-white text-gray-700 hover:bg-gray-50 dark:bg-dark-800 dark:text-gray-300 dark:hover:bg-dark-700'"
            :disabled="filteredCount === 0"
            data-test="mode-all"
            @click="mode = 'all'"
          >
            {{ t('admin.users.reengagement.allScope', { count: filteredCount }) }}
          </button>
        </div>
      </div>

      <div>
        <label for="reengagement-activity" class="input-label">
          {{ t('admin.users.reengagement.activityLabel') }}
        </label>
        <select
          id="reengagement-activity"
          v-model="activity"
          class="input"
          data-test="activity"
        >
          <option value="7">{{ t('admin.users.reengagement.inactiveDays', { days: 7 }) }}</option>
          <option value="14">{{ t('admin.users.reengagement.inactiveDays', { days: 14 }) }}</option>
          <option value="30">{{ t('admin.users.reengagement.inactiveDays', { days: 30 }) }}</option>
          <option value="90">{{ t('admin.users.reengagement.inactiveDays', { days: 90 }) }}</option>
          <option value="never">{{ t('admin.users.reengagement.neverUsed') }}</option>
        </select>
      </div>

      <dl class="divide-y divide-gray-200 border-y border-gray-200 text-sm dark:divide-dark-700 dark:border-dark-700">
        <div class="flex items-center justify-between gap-4 py-3">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.users.reengagement.recipients') }}</dt>
          <dd class="font-medium text-gray-900 dark:text-white">{{ recipientCount }}</dd>
        </div>
        <div class="flex items-center justify-between gap-4 py-3">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.users.reengagement.template') }}</dt>
          <dd class="text-right font-medium text-gray-900 dark:text-white">
            {{ t('admin.users.reengagement.templateName') }}
          </dd>
        </div>
        <div class="flex items-center justify-between gap-4 py-3">
          <dt class="text-gray-500 dark:text-gray-400">{{ t('admin.users.reengagement.rate') }}</dt>
          <dd class="font-semibold text-emerald-700 dark:text-emerald-400">0.06</dd>
        </div>
      </dl>

      <p v-if="mode === 'all'" class="text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.users.reengagement.backgroundHint') }}
      </p>
      <p v-if="selectionTooLarge" class="text-sm text-red-600 dark:text-red-400">
        {{ t('admin.users.reengagement.selectionLimit', { max: MAX_BATCH_USER_IDS }) }}
      </p>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="emit('close')">
          {{ t('common.cancel') }}
        </button>
        <button
          type="submit"
          form="user-reengagement-email-form"
          class="btn btn-primary"
          :disabled="!canSubmit"
          data-test="submit"
        >
          {{ submitting
            ? t('admin.users.reengagement.sending')
            : mode === 'all'
              ? t('admin.users.reengagement.queueAll')
              : t('admin.users.reengagement.send') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type {
  SendUserReengagementEmailRequest,
  SendUserReengagementEmailResponse,
  UserReengagementAudienceFilters
} from '@/api/admin/users'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'

type SendMode = 'selected' | 'all'

const props = defineProps<{
  show: boolean
  selectedIds: number[]
  filteredTotal?: number
  initialMode?: SendMode
  initialActivity?: string
  audienceFilters?: UserReengagementAudienceFilters
}>()

const emit = defineEmits<{
  close: []
  success: [result: SendUserReengagementEmailResponse]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const mode = ref<SendMode>('selected')
const activity = ref('14')
const submitting = ref(false)
const MAX_BATCH_USER_IDS = 500
const allowedActivities = new Set(['7', '14', '30', '90', 'never'])

const filteredCount = computed(() => props.filteredTotal ?? 0)
const recipientCount = computed(() => mode.value === 'all' ? filteredCount.value : props.selectedIds.length)
const selectionTooLarge = computed(() =>
  mode.value === 'selected' && props.selectedIds.length > MAX_BATCH_USER_IDS
)
const canSubmit = computed(() =>
  recipientCount.value > 0 && !selectionTooLarge.value && !submitting.value
)

watch(
  () => props.show,
  (show) => {
    if (!show) return
    const requestedMode = props.initialMode ?? 'selected'
    mode.value = requestedMode === 'all' && filteredCount.value > 0
      ? 'all'
      : props.selectedIds.length > 0
        ? 'selected'
        : 'all'
    activity.value = props.initialActivity && allowedActivities.has(props.initialActivity)
      ? props.initialActivity
      : '14'
    submitting.value = false
  }
)

const handleSubmit = async () => {
  if (!canSubmit.value) return

  const request: SendUserReengagementEmailRequest = {}
  if (mode.value === 'all') {
    Object.assign(request, props.audienceFilters ?? {})
    request.send_all = true
  } else {
    request.user_ids = [...props.selectedIds]
    if (props.audienceFilters?.has_recharged !== undefined) {
      request.has_recharged = props.audienceFilters.has_recharged
    }
  }
  if (activity.value === 'never') {
    request.never_used = true
  } else {
    request.inactive_days = Number(activity.value)
  }

  const confirmed = window.confirm(
    t(
      mode.value === 'all'
        ? 'admin.users.reengagement.confirmAll'
        : 'admin.users.reengagement.confirm',
      { count: recipientCount.value }
    )
  )
  if (!confirmed) return

  submitting.value = true
  try {
    const result = await adminAPI.users.sendReengagementEmail(request)
    if (result.queued) {
      appStore.showSuccess(t('admin.users.reengagement.queuedSuccess', { count: result.matched }))
    } else {
      appStore.showSuccess(
        t('admin.users.reengagement.success', {
          sent: result.sent,
          skipped: result.skipped
        })
      )
    }
    if (result.failed > 0) {
      appStore.showError(t('admin.users.reengagement.partialFailure', { count: result.failed }))
    }
    emit('success', result)
    emit('close')
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.message
      || error.response?.data?.detail
      || t('admin.users.reengagement.failed')
    )
  } finally {
    submitting.value = false
  }
}
</script>
