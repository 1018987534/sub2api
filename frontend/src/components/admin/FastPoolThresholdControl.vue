<template>
  <div class="border-t border-gray-100 px-4 py-3 text-xs dark:border-dark-700" data-testid="fast-pool-threshold-control">
    <form v-if="saved" class="flex flex-wrap items-center gap-2" @submit.prevent="save">
      <label for="fast-pool-enter-seconds" class="font-medium text-gray-700 dark:text-gray-200">
        {{ t('admin.dashboard.fastPoolEnterThreshold') }} ≤
      </label>
      <input
        id="fast-pool-enter-seconds"
        v-model.number="enterSeconds"
        type="number"
        min="1"
        max="86400"
        step="1"
        class="input w-24 py-1.5 text-sm"
        :disabled="saving"
        :aria-label="t('admin.dashboard.fastPoolEnterThreshold')"
        aria-describedby="fast-pool-threshold-hint"
      />
      <span>{{ t('admin.dashboard.fastPoolThresholdSeconds') }}</span>
      <label for="fast-pool-exit-seconds" class="font-medium text-gray-700 dark:text-gray-200">
        {{ t('admin.dashboard.fastPoolExitThreshold') }} &gt;
      </label>
      <input
        id="fast-pool-exit-seconds"
        v-model.number="exitSeconds"
        type="number"
        :min="Number(enterSeconds) || 1"
        max="86400"
        step="1"
        class="input w-24 py-1.5 text-sm"
        :disabled="saving"
        :aria-label="t('admin.dashboard.fastPoolExitThreshold')"
        aria-describedby="fast-pool-threshold-hint"
      />
      <span>{{ t('admin.dashboard.fastPoolThresholdSeconds') }}</span>
      <button type="submit" class="btn btn-secondary py-1.5 text-xs" :disabled="!canSave">
        {{ t(saving ? 'common.saving' : 'common.save') }}
      </button>
      <span id="fast-pool-threshold-hint" class="text-gray-500 dark:text-gray-400">
        {{ t('admin.dashboard.fastPoolThresholdHint') }}
      </span>
      <span v-if="!valid" role="alert" class="text-red-600 dark:text-red-400">
        {{ t('admin.dashboard.fastPoolThresholdInvalid') }}
      </span>
    </form>
    <div v-else-if="loadError" class="flex items-center gap-2 text-red-600 dark:text-red-400" role="alert">
      {{ t('admin.dashboard.fastPoolThresholdLoadFailed') }}
      <button type="button" class="btn btn-secondary py-1 text-xs" @click="load">{{ t('admin.dashboard.circuitBreakerRetry') }}</button>
    </div>
    <span v-else class="text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { FastPoolThresholdSettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
const appStore = useAppStore()
const enterSeconds = ref<number | string>('')
const exitSeconds = ref<number | string>('')
const saved = ref<FastPoolThresholdSettings | null>(null)
const saving = ref(false)
const loadError = ref(false)
const valid = computed(() => typeof enterSeconds.value === 'number' && typeof exitSeconds.value === 'number'
  && Number.isInteger(enterSeconds.value) && Number.isInteger(exitSeconds.value)
  && enterSeconds.value >= 1 && exitSeconds.value >= enterSeconds.value && exitSeconds.value <= 86400)
const canSave = computed(() => saved.value !== null && valid.value && !saving.value
  && (enterSeconds.value !== saved.value.enter_seconds || exitSeconds.value !== saved.value.exit_seconds))

const apply = (settings: FastPoolThresholdSettings) => {
  saved.value = settings
  enterSeconds.value = settings.enter_seconds
  exitSeconds.value = settings.exit_seconds
}

const load = async () => {
  loadError.value = false
  try {
    apply(await adminAPI.settings.getFastPoolThresholdSettings())
  } catch {
    loadError.value = true
  }
}

const save = async () => {
  if (!canSave.value) return
  saving.value = true
  try {
    apply(await adminAPI.settings.updateFastPoolThresholdSettings({
      enter_seconds: Number(enterSeconds.value), exit_seconds: Number(exitSeconds.value)
    }))
    appStore.showSuccess(t('admin.dashboard.fastPoolThresholdSaved'))
  } catch {
    appStore.showError(t('admin.dashboard.fastPoolThresholdSaveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
