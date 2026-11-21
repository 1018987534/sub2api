<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="relative w-full sm:max-w-xs">
            <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input v-model="search" type="search" class="input pl-9" :aria-label="tr('search')" :placeholder="tr('search')" />
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <RouterLink to="/custom-model-plaza" class="btn btn-secondary gap-1.5">
              <Icon name="eye" size="sm" /> {{ tr('preview') }}
            </RouterLink>
            <button type="button" class="btn btn-secondary" :aria-label="t('common.refresh')" :title="t('common.refresh')" :disabled="loading || saving" @click="load">
              <Icon name="refresh" size="sm" />
            </button>
            <button type="button" class="btn btn-primary gap-1.5" :disabled="loading || loadFailed" @click="openEditor()">
              <Icon name="plus" size="sm" /> {{ tr('create') }}
            </button>
          </div>
        </div>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">{{ tr('adminNote') }}</p>
        <p v-if="notice" role="status" class="mt-2 text-sm text-primary-700 dark:text-primary-300">{{ notice }}</p>
      </template>

      <template #table>
        <div v-if="loadFailed" class="p-4">
          <p role="alert" class="mb-3 text-red-600 dark:text-red-400">{{ tr('loadFailed') }}</p>
          <button type="button" class="btn btn-secondary" @click="load">{{ tr('retry') }}</button>
        </div>
        <DataTable v-else :columns="columns" :data="pageRows" :loading="loading">
          <template #cell-id="{ value }">
            <span class="break-all font-medium text-gray-900 dark:text-white">{{ value }}</span>
          </template>
          <template #cell-family="{ value }">{{ plazaFamilyName(value) }}</template>
          <template #cell-hidden="{ row }">
            <span :class="row.hidden ? 'text-gray-500 dark:text-dark-300' : 'text-emerald-700 dark:text-emerald-400'">
              {{ tr(row.hidden ? 'hidden' : 'visible') }}
            </span>
          </template>
          <template #cell-overrides="{ row }">{{ tr(row.overrides.length ? 'customized' : 'defaults') }}</template>
          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button type="button" class="p-2 text-gray-500 hover:text-primary-600 dark:text-dark-300 dark:hover:text-primary-400" :title="tr('edit')" :aria-label="`${tr('edit')}: ${row.id}`" @click="openEditor(row)">
                <Icon name="edit" size="sm" />
              </button>
              <button type="button" class="p-2 text-gray-500 hover:text-red-600 dark:text-dark-300 dark:hover:text-red-400" :title="tr('delete')" :aria-label="`${tr('delete')}: ${row.id}`" @click="deleting = row">
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </template>
          <template #empty><p class="p-5 text-center text-sm text-gray-500">{{ tr(search.trim() ? 'noResults' : 'empty') }}</p></template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination v-if="!loadFailed && filtered.length" v-model:page="page" :total="filtered.length" :page-size="pageSize" @update:page-size="changePageSize" />
      </template>
    </TablePageLayout>

    <BaseDialog :show="editorOpen" :title="tr(editing ? 'edit' : 'create')" width="wide" @close="closeEditor">
      <form id="plaza-model-form" class="space-y-5" @submit.prevent="save">
        <fieldset :disabled="saving" class="space-y-5">
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="text-sm font-medium">
              {{ tr('modelID') }}
              <input v-model.trim="draft.id" class="input mt-1" required maxlength="200" :readonly="editing" @change="refreshDefaults" />
            </label>
            <label class="text-sm font-medium">
              {{ tr('family') }}
              <input v-model.trim="draft.family" list="plaza-families" class="input mt-1" required maxlength="40" @change="refreshDefaults" />
              <datalist id="plaza-families"><option v-for="family in familyOptions" :key="family" :value="family" /></datalist>
            </label>
          </div>
          <label class="flex items-center gap-2 text-sm font-medium">
            <input v-model="draft.visible" type="checkbox" class="rounded text-primary-600" />
            {{ tr('showOnPlaza') }}
          </label>
          <div class="space-y-3 border-t border-gray-200 pt-3 dark:border-dark-700">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <label class="flex items-center gap-2 text-sm font-medium">
                <select :value="manual ? 'manual' : 'official'" class="input" :aria-label="tr('defaultTiers')" @change="setTierMode(($event.target as HTMLSelectElement).value === 'manual')">
                  <option value="official">{{ tr('defaultTiers') }}</option>
                  <option value="manual">{{ tr('manualTiers') }}</option>
                </select>
              </label>
              <button v-if="manual" type="button" class="btn btn-secondary gap-1.5" :disabled="limits.length >= 7" @click="limits.push('')">
                <Icon name="plus" size="sm" /> {{ tr('addLimit') }}
              </button>
            </div>
            <div v-if="manual" class="grid gap-3 sm:grid-cols-2">
              <div v-for="(_, index) in limits" :key="index" class="flex items-end gap-1.5">
                <label class="flex-1 text-sm font-medium">
                  {{ t('customModelPlaza.limit', { n: index + 1 }) }}
                  <input v-model="limits[index]" type="number" class="input mt-1" required min="1" max="1000000000" step="1" />
                </label>
                <button type="button" class="btn btn-secondary px-3" :aria-label="t('customModelPlaza.removeLimit', { n: index + 1 })" :title="t('customModelPlaza.removeLimit', { n: index + 1 })" @click="limits.splice(index, 1)">
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </div>
          </div>

          <p class="text-sm text-gray-500 dark:text-dark-300">{{ tr('priceNote') }}</p>
          <p v-if="previewing" role="status" class="text-sm text-gray-500">{{ tr('loading') }}</p>
          <p v-else-if="defaultsFailed" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ tr('defaultsFailed') }}</p>
          <fieldset v-for="tier in effectiveTiers" :key="tierKey(tier)" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
            <legend class="px-1 text-sm font-semibold">{{ tierLabel(tier) || tr('standard') }} · {{ tr('unit') }}</legend>
            <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              <label v-for="field in visibleFields" :key="field" class="text-sm font-medium">
                {{ tr(`fields.${field}`) }}
                <input :value="prices[tierKey(tier)]?.[field] ?? ''" type="number" class="input mt-1" min="0" max="1000000" step="any" :placeholder="displayPrice(tier[field])" @input="setRate(tier, field, $event)" />
              </label>
            </div>
          </fieldset>
          <p v-if="isGPT" class="text-sm text-gray-500 dark:text-dark-300">{{ tr('gptCacheWrite') }}</p>
          <p v-if="editorError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ editorError }}</p>
        </fieldset>
      </form>
      <template #footer>
        <div class="flex flex-wrap justify-between gap-2">
          <button type="button" class="btn btn-secondary gap-1.5" :disabled="saving || !Object.keys(prices).length" @click="prices = {}">
            <Icon name="refresh" size="sm" /> {{ tr('reset') }}
          </button>
          <div class="flex gap-2">
            <button type="button" class="btn btn-secondary" :disabled="saving" @click="closeEditor">{{ t('common.cancel') }}</button>
            <button type="submit" form="plaza-model-form" class="btn btn-primary gap-1.5" :disabled="saving || previewing">
              <Icon name="check" size="sm" /> {{ t('common.save') }}
            </button>
          </div>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog :show="!!deleting" :title="tr('delete')" :message="t('customModelPlaza.deleteConfirm', { id: deleting?.id })" :confirm-text="t('common.delete')" danger @cancel="cancelDelete" @confirm="deleteModel">
      <p v-if="deleteFailed" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ tr('deleteFailed') }}</p>
    </ConfirmDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { displayPrice, tierLabel } from '@/components/customModelPlaza/format'
import {
  deleteCustomModelPlazaModel, getCustomModelPlazaAdmin, getCustomModelPlazaDefaults,
  plazaFamilies, plazaFamilyName, plazaRateKeys, saveCustomModelPlazaConfig,
  type CustomPlazaAdminModel, type CustomPlazaTier, type PlazaRateKey,
} from '@/api/customModelPlaza'

const { t } = useI18n()
const tr = (key: string) => t(`customModelPlaza.${key}`)
const models = ref<CustomPlazaAdminModel[]>([])
const loading = ref(true)
const loadFailed = ref(false)
const search = ref('')
const page = ref(1)
const pageSize = ref(20)
const notice = ref('')
const columns = computed(() => [
  { key: 'id', label: tr('modelID') }, { key: 'family', label: tr('family') },
  { key: 'hidden', label: tr('visibility') }, { key: 'overrides', label: tr('pricing') },
  { key: 'actions', label: t('common.actions') },
])
const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return models.value.filter(row => row.id.toLowerCase().includes(q) || plazaFamilyName(row.family).toLowerCase().includes(q))
})
const pageRows = computed(() => filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
watch(search, () => { page.value = 1 })
watch(() => filtered.value.length, count => { page.value = Math.min(page.value, Math.max(1, Math.ceil(count / pageSize.value))) })
function changePageSize(size: number) { pageSize.value = size; page.value = 1 }

const editorOpen = ref(false)
const editing = ref(false)
const saving = ref(false)
const editorError = ref('')
const deleting = ref<CustomPlazaAdminModel>()
const deleteFailed = ref(false)
const draft = ref({ id: '', family: '', visible: true })
const familyOptions = computed(() => [...new Set([...plazaFamilies.map(f => f.name), ...models.value.map(m => plazaFamilyName(m.family))])])
const manual = ref(false)
const limits = ref<(number | '')[]>([])
const officialTiers = ref<CustomPlazaTier[]>([])
const previewing = ref(false)
const defaultsFailed = ref(false)
let quoteRequest = 0
let quoteIdentity = ''
const prices = ref<Record<string, Partial<Record<PlazaRateKey, number>>>>({})
const isGPT = computed(() => draft.value.family.toLowerCase() === 'gpt' || /^(gpt|codex)-/i.test(draft.value.id.split('/').pop() ?? ''))
const visibleFields = computed(() => isGPT.value ? plazaRateKeys.slice(0, 3) : plazaRateKeys)
const resolvedLimits = computed(() => {
  const values: number[] = []
  for (const limit of limits.value) {
    if (typeof limit !== 'number' || !Number.isInteger(limit) || limit <= (values[values.length - 1] ?? 0) || limit > 1e9) return null
    values.push(limit)
  }
  return values
})
const effectiveTiers = computed<CustomPlazaTier[]>(() => {
  const official = officialTiers.value.length ? officialTiers.value : [{ min_tokens: 0, max_tokens: 0 }]
  if (!manual.value) return official
  if (!resolvedLimits.value) return []
  return [...resolvedLimits.value, 0].map((max_tokens, i) => {
    const min_tokens = resolvedLimits.value![i - 1] ?? 0
    const defaults = [...official].reverse().find(tier => tier.min_tokens <= min_tokens)
    return { ...defaults, min_tokens, max_tokens }
  })
})

function tierKey(tier: CustomPlazaTier) { return `${tier.min_tokens}:${tier.max_tokens}` }
function setRate(tier: CustomPlazaTier, field: PlazaRateKey, event: Event) {
  const input = event.target as HTMLInputElement
  const key = tierKey(tier)
  const row = { ...prices.value[key] }
  if (input.value === '') delete row[field]
  else if (Number.isFinite(input.valueAsNumber)) row[field] = input.valueAsNumber
  if (Object.keys(row).length) prices.value[key] = row
  else delete prices.value[key]
}

function setTierMode(value: boolean) {
  if (value && !manual.value) limits.value = officialTiers.value.map(tier => tier.max_tokens).filter(max => max > 0)
  manual.value = value
}

async function load() {
  loading.value = true
  loadFailed.value = false
  notice.value = ''
  try { models.value = await getCustomModelPlazaAdmin() }
  catch { loadFailed.value = true }
  finally { loading.value = false }
}

function identity() { return JSON.stringify([draft.value.id, draft.value.family]) }
function openEditor(model?: CustomPlazaAdminModel) {
  quoteRequest++
  editing.value = !!model
  draft.value = { id: model?.id ?? '', family: model?.family ?? '', visible: !model?.hidden }
  editorError.value = ''
  defaultsFailed.value = previewing.value = false
  officialTiers.value = model?.official_tiers ?? []
  quoteIdentity = model ? identity() : ''
  manual.value = Array.isArray(model?.context_limits)
  limits.value = [...model?.context_limits ?? []]
  prices.value = Object.fromEntries((model?.overrides ?? []).map(tier => [
    tierKey(tier), Object.fromEntries(plazaRateKeys.flatMap(field => typeof tier[field] === 'number' ? [[field, tier[field]]] : [])),
  ]))
  editorOpen.value = true
}

function closeEditor() { if (!saving.value) { quoteRequest++; editorOpen.value = false } }

async function refreshDefaults() {
  const current = identity()
  if (!draft.value.id || !draft.value.family || current === quoteIdentity) return
  const request = ++quoteRequest
  previewing.value = true
  defaultsFailed.value = false
  try {
    const updated = await getCustomModelPlazaDefaults(draft.value.id, draft.value.family)
    if (request !== quoteRequest || current !== identity()) return
    officialTiers.value = updated.official_tiers
    quoteIdentity = current
  } catch {
    if (request === quoteRequest && current === identity()) defaultsFailed.value = true
  } finally {
    if (request === quoteRequest) previewing.value = false
  }
}

async function save() {
  if (saving.value || previewing.value || !draft.value.id || !draft.value.family) return
  editorError.value = ''
  if (manual.value && !resolvedLimits.value) { editorError.value = tr('invalidLimits'); return }
  if (!manual.value && identity() !== quoteIdentity) {
    await refreshDefaults()
    if (!editorOpen.value || identity() !== quoteIdentity) return
  }
  const tiers = effectiveTiers.value.map(({ min_tokens, max_tokens }): CustomPlazaTier => {
    const tier = { min_tokens, max_tokens } as CustomPlazaTier
    const prior = prices.value[tierKey(tier)]
    for (const field of visibleFields.value) if (prior?.[field] !== undefined) tier[field] = prior[field]
    return tier
  })
  saving.value = true
  try {
    const updated = await saveCustomModelPlazaConfig({
      id: draft.value.id, family: draft.value.family, hidden: !draft.value.visible,
      context_limits: manual.value ? resolvedLimits.value : null, tiers,
    }, !editing.value)
    const index = models.value.findIndex(row => row.id === updated.id)
    if (index >= 0) models.value.splice(index, 1, updated)
    else { models.value.unshift(updated); search.value = ''; page.value = 1 }
    editorOpen.value = false
    notice.value = tr('saved')
  } catch (error) {
    const reason = (error as { reason?: string }).reason
    editorError.value = tr(reason === 'CUSTOM_PLAZA_DUPLICATE_MODEL' ? 'duplicate'
      : reason === 'CUSTOM_PLAZA_INVALID_ID' ? 'invalidID'
      : reason === 'CUSTOM_PLAZA_CONFLICT' ? 'conflict' : 'saveFailed')
  } finally { saving.value = false }
}

function cancelDelete() { if (!saving.value) { deleting.value = undefined; deleteFailed.value = false } }
async function deleteModel() {
  const row = deleting.value
  if (!row || saving.value) return
  saving.value = true
  deleteFailed.value = false
  try {
    await deleteCustomModelPlazaModel(row.id)
    models.value = models.value.filter(model => model.id !== row.id)
    deleting.value = undefined
    notice.value = tr('deleted')
  } catch { deleteFailed.value = true }
  finally { saving.value = false }
}

onMounted(load)
</script>
