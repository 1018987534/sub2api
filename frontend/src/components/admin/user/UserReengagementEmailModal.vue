<template>
  <BaseDialog
    :show="show"
    :title="t('admin.users.reengagement.title')"
    width="normal"
    @close="emit('close')"
  >
    <form id="user-reengagement-email-form" class="space-y-5" @submit.prevent="handleSubmit">
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
          <dd class="font-medium text-gray-900 dark:text-white">{{ selectedIds.length }}</dd>
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
          {{ submitting ? t('admin.users.reengagement.sending') : t('admin.users.reengagement.send') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { SendUserReengagementEmailRequest, SendUserReengagementEmailResponse } from '@/api/admin/users'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = defineProps<{
  show: boolean
  selectedIds: number[]
  initialActivity?: string
}>()

const emit = defineEmits<{
  close: []
  success: [result: SendUserReengagementEmailResponse]
}>()

const { t } = useI18n()
const appStore = useAppStore()
const activity = ref('14')
const submitting = ref(false)
const MAX_BATCH_USER_IDS = 500
const allowedActivities = new Set(['7', '14', '30', '90', 'never'])

const selectionTooLarge = computed(() => props.selectedIds.length > MAX_BATCH_USER_IDS)
const canSubmit = computed(() =>
  props.selectedIds.length > 0 && !selectionTooLarge.value && !submitting.value
)

watch(
  () => props.show,
  (show) => {
    if (!show) return
    activity.value = props.initialActivity && allowedActivities.has(props.initialActivity)
      ? props.initialActivity
      : '14'
    submitting.value = false
  }
)

const handleSubmit = async () => {
  if (!canSubmit.value) return
  const request: SendUserReengagementEmailRequest = {
    user_ids: [...props.selectedIds]
  }
  if (activity.value === 'never') {
    request.never_used = true
  } else {
    request.inactive_days = Number(activity.value)
  }

  const confirmed = window.confirm(
    t('admin.users.reengagement.confirm', { count: props.selectedIds.length })
  )
  if (!confirmed) return

  submitting.value = true
  try {
    const result = await adminAPI.users.sendReengagementEmail(request)
    appStore.showSuccess(
      t('admin.users.reengagement.success', {
        sent: result.sent,
        skipped: result.skipped
      })
    )
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
