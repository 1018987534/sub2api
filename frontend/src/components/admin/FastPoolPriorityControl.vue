<template>
  <div class="border-t border-gray-100 px-4 py-3 text-xs dark:border-dark-700" data-testid="fast-pool-priority-control">
    <form v-if="savedSettings" class="flex flex-wrap items-center gap-2" @submit.prevent="save">
      <label for="fast-pool-priority-mode" class="font-medium text-gray-700 dark:text-gray-200">
        {{ t('admin.dashboard.fastPoolPriorityLabel') }}
      </label>
      <select
        id="fast-pool-priority-mode"
        v-model="mode"
        class="input w-auto py-1.5 text-sm"
        :disabled="saving"
        aria-describedby="fast-pool-priority-hint"
        data-testid="fast-pool-priority-mode"
      >
        <option value="intelligence">{{ t('admin.dashboard.fastPoolCacheRatePriority') }}</option>
        <option value="latency">{{ t('admin.dashboard.fastPoolLatencyPriority') }}</option>
        <option value="rate">{{ t('admin.dashboard.fastPoolRatePriority') }}</option>
      </select>
      <label for="fast-pool-cache-rate-threshold" class="font-medium text-gray-700 dark:text-gray-200">
        {{ t('admin.dashboard.fastPoolCacheRateThresholdLabel') }}
      </label>
      <input
        id="fast-pool-cache-rate-threshold"
        v-model.number="thresholdPercent"
        type="number"
        min="0"
        max="100"
        step="any"
        class="input w-28 py-1.5 text-sm"
        :placeholder="t('admin.dashboard.fastPoolCacheRateThresholdPlaceholder')"
        :disabled="saving"
        :aria-invalid="!thresholdValid"
        aria-describedby="fast-pool-cache-rate-threshold-hint"
        data-testid="fast-pool-cache-rate-threshold"
      />
      <span>%</span>
      <button type="submit" class="btn btn-secondary py-1.5 text-xs" :disabled="!canSave" data-testid="save-fast-pool-priority">
        {{ t(saving ? 'common.saving' : 'common.save') }}
      </button>
      <span id="fast-pool-priority-hint" class="text-gray-500 dark:text-gray-400" aria-live="polite">
        {{ t(mode === 'rate' ? 'admin.dashboard.fastPoolRatePriorityHint' : mode === 'latency' ? 'admin.dashboard.fastPoolLatencyPriorityHint' : 'admin.dashboard.fastPoolCacheRatePriorityHint') }}
      </span>
      <span id="fast-pool-cache-rate-threshold-hint" class="w-full text-gray-500 dark:text-gray-400">
        {{ t('admin.dashboard.fastPoolCacheRateThresholdHint') }}
      </span>
      <span v-if="!thresholdValid" class="text-red-600 dark:text-red-400" role="alert">
        {{ t('admin.dashboard.fastPoolCacheRateThresholdInvalid') }}
      </span>
    </form>
    <div v-else-if="loadError" class="flex items-center gap-2 text-red-600 dark:text-red-400" role="alert">
      {{ t('admin.dashboard.fastPoolPriorityLoadFailed') }}
      <button type="button" class="btn btn-secondary py-1 text-xs" @click="load">{{ t('admin.dashboard.circuitBreakerRetry') }}</button>
    </div>
    <span v-else class="text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { FastPoolPriorityMode, FastPoolPrioritySettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores/app'

const emit = defineEmits<{ change: [settings: FastPoolPrioritySettings] }>()
const { t } = useI18n()
const appStore = useAppStore()
const mode = ref<FastPoolPriorityMode>('intelligence')
const thresholdPercent = ref<number | string>(80)
const savedSettings = ref<FastPoolPrioritySettings | null>(null)
const saving = ref(false)
const loadError = ref(false)
const threshold = computed(() => thresholdPercent.value === '' ? null : Number(thresholdPercent.value))
const thresholdValid = computed(() => threshold.value === null || (Number.isFinite(threshold.value) && threshold.value >= 0 && threshold.value <= 100))
const canSave = computed(() => savedSettings.value !== null && thresholdValid.value && !saving.value && (
  mode.value !== savedSettings.value.mode || threshold.value !== savedSettings.value.cache_rate_threshold_percent
))

const applySettings = (settings: FastPoolPrioritySettings) => {
  const saved = { ...settings, cache_rate_threshold_percent: settings.cache_rate_threshold_percent === undefined ? 80 : settings.cache_rate_threshold_percent }
  mode.value = saved.mode
  thresholdPercent.value = saved.cache_rate_threshold_percent ?? ''
  savedSettings.value = saved
  emit('change', saved)
}

const load = async () => {
  loadError.value = false
  try {
    const settings = await adminAPI.settings.getFastPoolPrioritySettings()
    applySettings(settings)
  } catch {
    loadError.value = true
  }
}

const save = async () => {
  if (!canSave.value) return
  saving.value = true
  try {
    const settings = await adminAPI.settings.updateFastPoolPrioritySettings(mode.value, threshold.value)
    applySettings(settings)
    appStore.showSuccess(t('admin.dashboard.fastPoolPrioritySaved'))
  } catch {
    appStore.showError(t('admin.dashboard.fastPoolPrioritySaveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
