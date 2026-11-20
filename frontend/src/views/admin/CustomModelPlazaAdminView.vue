<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('customModelPlaza.adminNote') }}</p>
        <RouterLink to="/custom-model-plaza" class="btn btn-secondary shrink-0 gap-1.5">
          <Icon name="eye" size="sm" /> {{ t('customModelPlaza.preview') }}
        </RouterLink>
      </div>
      <p v-if="loading" role="status">{{ t('customModelPlaza.loading') }}</p>
      <div v-else-if="loadFailed">
        <p role="alert" class="mb-3 text-red-600 dark:text-red-400">{{ t('customModelPlaza.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary" @click="load">{{ t('customModelPlaza.retry') }}</button>
      </div>
      <p v-else-if="!active">{{ t('customModelPlaza.empty') }}</p>
      <form v-else class="space-y-5" @submit.prevent="save(false)">
        <div class="max-w-lg">
          <label for="plaza-price-model" class="input-label">{{ t('customModelPlaza.model') }}</label>
          <select id="plaza-price-model" v-model="selected" class="input" :disabled="saving">
            <optgroup v-for="family in plazaFamilies" :key="family.id" :label="family.name">
              <option v-for="model in models.filter(m => m.family === family.id)" :key="model.id" :value="model.id">
                {{ model.id }}{{ model.overrides.length ? ' · ' + t('customModelPlaza.customized') : '' }}
              </option>
            </optgroup>
          </select>
        </div>
        <fieldset v-for="(tier, index) in active.default_tiers" :key="tier.max_tokens" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700" :disabled="saving">
          <legend class="px-1 text-sm font-semibold text-gray-900 dark:text-white">
            {{ tierLabel(tier) || t('customModelPlaza.standard') }} · {{ t('customModelPlaza.unit') }}
          </legend>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <label v-for="field in visibleFields" :key="field" class="block text-sm font-medium text-gray-700 dark:text-gray-200">
              {{ t(`customModelPlaza.fields.${field}`) }}
              <input
                v-model="drafts[index][field]"
                class="input mt-1"
                type="number"
                min="0"
                max="1000000"
                step="any"
                :placeholder="displayPrice(tier[field])"
              />
            </label>
          </div>
          <p v-if="active.family === 'gpt'" class="mt-3 text-sm text-gray-500 dark:text-dark-400">{{ t('customModelPlaza.gptCacheWrite') }}</p>
        </fieldset>
        <div class="flex flex-wrap items-center gap-3">
          <button type="submit" class="btn btn-primary gap-1.5" :disabled="saving || !dirty">
            <Icon name="check" size="sm" /> {{ t('customModelPlaza.save') }}
          </button>
          <button type="button" class="btn btn-secondary gap-1.5" :disabled="saving || !active.overrides.length" @click="save(true)">
            <Icon name="refresh" size="sm" /> {{ t('customModelPlaza.reset') }}
          </button>
          <p v-if="notice" role="status" class="text-sm text-primary-700 dark:text-primary-300">{{ notice }}</p>
          <p v-if="saveFailed" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t('customModelPlaza.saveFailed') }}</p>
        </div>
      </form>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { displayPrice, tierLabel } from '@/components/customModelPlaza/format'
import {
  getCustomModelPlazaAdmin, plazaFamilies, plazaRateKeys, saveCustomModelPlazaPrices,
  type CustomPlazaAdminModel, type CustomPlazaTier, type PlazaRateKey,
} from '@/api/customModelPlaza'

const { t } = useI18n()
type Draft = Partial<Record<PlazaRateKey, number | ''>>
const models = ref<CustomPlazaAdminModel[]>([])
const selected = ref('')
const drafts = ref<Draft[]>([])
const loading = ref(true)
const saving = ref(false)
const loadFailed = ref(false)
const saveFailed = ref(false)
const notice = ref('')
const active = computed(() => models.value.find(model => model.id === selected.value))
const visibleFields = computed(() => active.value?.family === 'gpt' ? plazaRateKeys.slice(0, 3) : plazaRateKeys)

function modelDraft(model?: CustomPlazaAdminModel): Draft[] {
  return model?.default_tiers.map(tier => {
    const prior = model.overrides.find(row => row.min_tokens === tier.min_tokens && row.max_tokens === tier.max_tokens)
    return Object.fromEntries(plazaRateKeys.map(field => [field, prior?.[field] ?? ''])) as Draft
  }) ?? []
}

const dirty = computed(() => JSON.stringify(drafts.value) !== JSON.stringify(modelDraft(active.value)))
watch(active, model => {
  drafts.value = modelDraft(model)
  saveFailed.value = false
  notice.value = ''
}, { flush: 'sync' })

async function load() {
  loading.value = true
  loadFailed.value = false
  try {
    models.value = await getCustomModelPlazaAdmin()
    selected.value = models.value[0]?.id ?? ''
  } catch {
    loadFailed.value = true
  } finally {
    loading.value = false
  }
}

async function save(reset: boolean) {
  const model = active.value
  if (!model || saving.value) return
  const tiers = reset ? [] : model.default_tiers.map(({ min_tokens, max_tokens }, index): CustomPlazaTier => {
    const values: CustomPlazaTier = { min_tokens, max_tokens }
    for (const field of visibleFields.value) {
      const value = drafts.value[index][field]
      if (typeof value === 'number') values[field] = value
    }
    return values
  })
  saving.value = true
  saveFailed.value = false
  notice.value = ''
  try {
    const updated = await saveCustomModelPlazaPrices(model.id, tiers)
    models.value = models.value.map(current => current.id === model.id ? updated : current)
    notice.value = t(reset ? 'customModelPlaza.resetDone' : 'customModelPlaza.saved')
  } catch {
    saveFailed.value = true
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
