<template>
  <div class="mt-3 border-t border-gray-100 pt-3 text-xs dark:border-dark-700">
    <form v-if="savedSettings" class="flex flex-wrap items-center gap-2" data-testid="server-cost-form" @submit.prevent="save">
      <label for="dashboard-monthly-server-cost" class="font-medium text-gray-700 dark:text-gray-200">
        {{ t('admin.dashboard.monthlyServerCost') }}
      </label>
      <input
        id="dashboard-monthly-server-cost"
        v-model.number="monthlyCost"
        type="number"
        min="0"
        step="0.01"
        class="input w-36 py-1.5 text-sm"
        :disabled="saving"
        :aria-invalid="!validAmount"
        aria-describedby="dashboard-server-cost-hint"
        data-testid="monthly-server-cost"
      />
      <span>{{ t('admin.dashboard.serverCostUnit') }}</span>
      <button type="submit" class="btn btn-secondary py-1.5 text-xs" :disabled="!canSave" data-testid="save-server-cost">
        {{ t(saving ? 'common.saving' : 'common.save') }}
      </button>
      <span v-if="validAmount" id="dashboard-server-cost-hint" class="text-gray-500 dark:text-gray-400" aria-live="polite">
        {{ t('admin.dashboard.serverCostHint', { days: savedSettings.month_days, amount: dailyPreview }) }}
      </span>
      <span v-else class="text-red-600 dark:text-red-400" role="alert">
        {{ t('admin.dashboard.serverCostInvalid') }}
      </span>
    </form>
    <div v-else-if="loadError" class="flex items-center gap-2 text-red-600 dark:text-red-400" role="alert">
      {{ t('admin.dashboard.serverCostLoadFailed') }}
      <button type="button" class="btn btn-secondary py-1 text-xs" @click="load">{{ t('admin.dashboard.circuitBreakerRetry') }}</button>
    </div>
    <span v-else class="text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { DashboardProfitSettings } from '@/api/admin/settings'
import { useAppStore } from '@/stores/app'

const emit = defineEmits<{ saved: [settings: DashboardProfitSettings] }>()
const { t } = useI18n()
const appStore = useAppStore()
const monthlyCost = ref<number | string>(0)
const savedSettings = ref<DashboardProfitSettings | null>(null)
const saving = ref(false)
const loadError = ref(false)
const amount = computed(() => Number(monthlyCost.value))
const validAmount = computed(() => monthlyCost.value !== '' && Number.isFinite(amount.value) && amount.value >= 0)
const canSave = computed(() => savedSettings.value !== null && validAmount.value && !saving.value && amount.value !== savedSettings.value.monthly_server_cost_usd)
const dailyPreview = computed(() => new Intl.NumberFormat('en-US', {
  style: 'currency', currency: 'USD', minimumFractionDigits: 2, maximumFractionDigits: 2
}).format(amount.value / (savedSettings.value?.month_days || 1)))

const applySettings = (settings: DashboardProfitSettings) => {
  savedSettings.value = settings
  monthlyCost.value = settings.monthly_server_cost_usd
}

const load = async () => {
  loadError.value = false
  try {
    applySettings(await adminAPI.settings.getDashboardProfitSettings())
  } catch {
    loadError.value = true
  }
}

const save = async () => {
  if (!canSave.value) return
  saving.value = true
  try {
    const settings = await adminAPI.settings.updateDashboardProfitSettings(amount.value)
    applySettings(settings)
    emit('saved', settings)
    appStore.showSuccess(t('admin.dashboard.serverCostSaved'))
  } catch {
    appStore.showError(t('admin.dashboard.serverCostSaveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
