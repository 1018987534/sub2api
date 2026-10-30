<template>
  <div class="border-t border-gray-100 px-4 py-3 text-xs dark:border-dark-700" data-testid="fast-pool-priority-control">
    <form v-if="savedMode" class="flex flex-wrap items-center gap-2" @submit.prevent="save">
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
        <option value="intelligence">{{ t('admin.dashboard.fastPoolIntelligencePriority') }}</option>
        <option value="rate">{{ t('admin.dashboard.fastPoolRatePriority') }}</option>
      </select>
      <button type="submit" class="btn btn-secondary py-1.5 text-xs" :disabled="!canSave" data-testid="save-fast-pool-priority">
        {{ t(saving ? 'common.saving' : 'common.save') }}
      </button>
      <span id="fast-pool-priority-hint" class="text-gray-500 dark:text-gray-400" aria-live="polite">
        {{ t(mode === 'rate' ? 'admin.dashboard.fastPoolRatePriorityHint' : 'admin.dashboard.fastPoolIntelligencePriorityHint') }}
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
import type { FastPoolPriorityMode } from '@/api/admin/settings'
import { useAppStore } from '@/stores/app'

const emit = defineEmits<{ change: [mode: FastPoolPriorityMode] }>()
const { t } = useI18n()
const appStore = useAppStore()
const mode = ref<FastPoolPriorityMode>('intelligence')
const savedMode = ref<FastPoolPriorityMode | null>(null)
const saving = ref(false)
const loadError = ref(false)
const canSave = computed(() => savedMode.value !== null && mode.value !== savedMode.value && !saving.value)

const load = async () => {
  loadError.value = false
  try {
    const settings = await adminAPI.settings.getFastPoolPrioritySettings()
    mode.value = savedMode.value = settings.mode
    emit('change', settings.mode)
  } catch {
    loadError.value = true
  }
}

const save = async () => {
  if (!canSave.value) return
  saving.value = true
  try {
    const settings = await adminAPI.settings.updateFastPoolPrioritySettings(mode.value)
    mode.value = savedMode.value = settings.mode
    emit('change', settings.mode)
    appStore.showSuccess(t('admin.dashboard.fastPoolPrioritySaved'))
  } catch {
    appStore.showError(t('admin.dashboard.fastPoolPrioritySaveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
