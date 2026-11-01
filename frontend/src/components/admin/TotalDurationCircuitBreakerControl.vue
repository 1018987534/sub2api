<template>
  <div class="border-t border-gray-100 px-4 py-3 text-xs dark:border-dark-700" data-testid="total-duration-circuit-control">
    <form v-if="settings" class="flex flex-wrap items-center gap-2" @submit.prevent="save">
      <label for="total-duration-circuit-window" class="font-medium text-gray-700 dark:text-gray-200">
        {{ t('admin.dashboard.circuitBreakerWindowLabel') }}
      </label>
      <input
        id="total-duration-circuit-window"
        v-model.number="windowMinutes"
        type="number"
        min="1"
        max="1440"
        step="1"
        required
        class="input w-20 py-1.5 text-sm"
        :disabled="saving"
        :aria-label="t('admin.dashboard.circuitBreakerWindowLabel')"
        :aria-invalid="!validWindowMinutes"
        data-testid="total-duration-circuit-window"
      />
      <span>{{ t('admin.dashboard.circuitBreakerMinutes') }}</span>
      <span>{{ t('admin.dashboard.circuitBreakerRuleJoin') }}</span>
      <label for="total-duration-circuit-threshold" class="sr-only">
        {{ t('admin.dashboard.circuitBreakerThresholdLabel') }}
      </label>
      <input
        id="total-duration-circuit-threshold"
        v-model.number="thresholdMinutes"
        type="number"
        min="1"
        max="1440"
        step="1"
        required
        class="input w-20 py-1.5 text-sm"
        :disabled="saving"
        :aria-label="t('admin.dashboard.circuitBreakerThresholdLabel')"
        :aria-invalid="!validThresholdMinutes"
        data-testid="total-duration-circuit-threshold"
      />
      <span>{{ t('admin.dashboard.circuitBreakerMinutes') }}</span>
      <span>{{ t('admin.dashboard.circuitBreakerRuleCountJoin') }}</span>
      <label for="total-duration-circuit-limit" class="sr-only">
        {{ t('admin.dashboard.circuitBreakerLimit') }}
      </label>
      <input
        id="total-duration-circuit-limit"
        v-model.number="maxSlowRequests"
        type="number"
        min="0"
        max="9999"
        step="1"
        required
        class="input w-20 py-1.5 text-sm"
        :disabled="saving"
        :aria-label="t('admin.dashboard.circuitBreakerLimit')"
        :aria-invalid="!validLimit"
        aria-describedby="total-duration-circuit-hint"
        data-testid="total-duration-circuit-limit"
      />
      <span class="text-gray-600 dark:text-gray-300">{{ t('admin.dashboard.circuitBreakerSuffix') }}</span>
      <button type="submit" class="btn btn-secondary py-1.5 text-xs" :disabled="!canSave" data-testid="save-total-duration-circuit">
        {{ t(saving ? 'common.saving' : 'common.save') }}
      </button>
      <span id="total-duration-circuit-hint" class="w-full text-gray-500 dark:text-gray-400" aria-live="polite">
        {{ validLimit && validThresholdMinutes && validWindowMinutes
          ? t('admin.dashboard.circuitBreakerHint', { count: Number(maxSlowRequests) + 1 })
          : t('admin.dashboard.circuitBreakerInvalid') }}
      </span>
    </form>
    <div v-else-if="loadError" class="flex items-center gap-2 text-red-600 dark:text-red-400" role="alert">
      {{ t('admin.dashboard.circuitBreakerLoadFailed') }}
      <button type="button" class="btn btn-secondary py-1 text-xs" @click="load">{{ t('admin.dashboard.circuitBreakerRetry') }}</button>
    </div>
    <span v-else class="text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { TotalDurationCircuitBreakerSettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const settings = ref<TotalDurationCircuitBreakerSettings | null>(null)
const maxSlowRequests = ref<number | string>('')
const thresholdMinutes = ref<number | string>('')
const windowMinutes = ref<number | string>('')
const saving = ref(false)
const loadError = ref(false)

const isIntegerInRange = (value: number | string, min: number, max: number): boolean =>
  typeof value === 'number' && Number.isInteger(value) && value >= min && value <= max

const validLimit = computed(() => isIntegerInRange(maxSlowRequests.value, 0, 9999))
const validThresholdMinutes = computed(() => isIntegerInRange(thresholdMinutes.value, 1, 1440))
const validWindowMinutes = computed(() => isIntegerInRange(windowMinutes.value, 1, 1440))
const canSave = computed(() => {
  if (settings.value === null || !validLimit.value || !validThresholdMinutes.value || !validWindowMinutes.value || saving.value) return false
  return Number(maxSlowRequests.value) !== settings.value.max_slow_requests
    || Number(thresholdMinutes.value) * 60 !== settings.value.threshold_seconds
    || Number(windowMinutes.value) * 60 !== settings.value.window_seconds
})

const load = async () => {
  loadError.value = false
  try {
    settings.value = await adminAPI.settings.getTotalDurationCircuitBreakerSettings()
    maxSlowRequests.value = settings.value.max_slow_requests
    thresholdMinutes.value = settings.value.threshold_seconds / 60
    windowMinutes.value = settings.value.window_seconds / 60
  } catch {
    loadError.value = true
  }
}

const save = async () => {
  if (!canSave.value) return
  saving.value = true
  try {
    settings.value = await adminAPI.settings.updateTotalDurationCircuitBreakerSettings({
      max_slow_requests: Number(maxSlowRequests.value),
      threshold_seconds: Number(thresholdMinutes.value) * 60,
      window_seconds: Number(windowMinutes.value) * 60,
    })
    maxSlowRequests.value = settings.value.max_slow_requests
    thresholdMinutes.value = settings.value.threshold_seconds / 60
    windowMinutes.value = settings.value.window_seconds / 60
    appStore.showSuccess(t('admin.dashboard.circuitBreakerSaved'))
  } catch {
    appStore.showError(t('admin.dashboard.circuitBreakerSaveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
