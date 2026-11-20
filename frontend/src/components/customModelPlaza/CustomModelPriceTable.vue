<template>
  <section :aria-label="t('customModelPlaza.familyPrices', { family })" class="space-y-4">
    <h2 class="px-1 text-xl font-semibold text-gray-900 dark:text-white">{{ family }}</h2>
    <div class="overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-card dark:border-dark-700 dark:bg-dark-800/60">
      <div class="px-4 py-4 sm:px-5">
        <h3 class="text-base font-semibold text-gray-900 dark:text-gray-100">{{ t('customModelPlaza.standard') }}</h3>
        <p class="mt-1.5 text-sm text-gray-500 dark:text-dark-400">{{ t('customModelPlaza.standardNote') }}</p>
      </div>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[680px] table-fixed border-collapse text-left text-sm">
          <colgroup><col class="w-[32%]" /></colgroup>
          <colgroup class="bg-primary-50/60 dark:bg-primary-500/5"><col class="w-[18%]" /><col class="w-[15%]" /><col /></colgroup>
          <thead class="border-y border-gray-200 text-xs text-gray-600 dark:border-dark-700 dark:text-dark-300">
            <tr>
              <th rowspan="2" scope="col" class="border-r border-gray-200 px-4 sm:px-5 dark:border-dark-700">{{ t('customModelPlaza.model') }}</th>
              <th colspan="3" scope="colgroup" class="border-b border-primary-200 px-3 py-2 text-center font-semibold text-primary-700 dark:border-primary-500/20 dark:text-primary-300">
                {{ t('customModelPlaza.unitPrice') }}
                <span class="ml-2 whitespace-nowrap font-normal">{{ t('customModelPlaza.unit') }}</span>
              </th>
            </tr>
            <tr>
              <th scope="col" class="px-3 py-2">{{ t('customModelPlaza.fields.input') }}</th>
              <th scope="col" class="px-3 py-2">{{ t('customModelPlaza.fields.output') }}</th>
              <th scope="col" class="px-3 py-2">{{ t('customModelPlaza.cache') }}</th>
            </tr>
          </thead>
          <tbody v-for="model in models" :key="model.id" class="border-b border-gray-100 last:border-0 dark:border-dark-700/60">
            <tr v-for="(tier, index) in model.tiers" :key="tier.max_tokens" class="align-middle">
              <th v-if="index === 0" :rowspan="model.tiers.length" scope="rowgroup" class="break-words border-r border-gray-200 px-4 py-3 font-medium text-gray-900 sm:px-5 dark:border-dark-700/60 dark:text-gray-100">
                {{ model.id }}
              </th>
              <td :class="priceRowClass(index, model.tiers.length)">
                <span v-if="tierLabel(tier)" class="mr-2 whitespace-nowrap text-xs text-gray-500 dark:text-dark-400" :title="t('customModelPlaza.contextNote')">{{ tierLabel(tier) }}</span>
                <strong class="whitespace-nowrap">{{ displayPrice(tier.input) }}</strong>
              </td>
              <td :class="priceRowClass(index, model.tiers.length)"><strong class="whitespace-nowrap">{{ displayPrice(tier.output) }}</strong></td>
              <td :class="priceRowClass(index, model.tiers.length)">
                <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                  <span class="whitespace-nowrap"><span class="cache-label">{{ t('customModelPlaza.write') }}</span> {{ displayPrice(tier.cache_write) }}</span>
                  <span v-if="tier.cache_write_1h != null" class="whitespace-nowrap">(<span class="cache-label">1h</span> {{ displayPrice(tier.cache_write_1h) }})</span>
                  <span v-if="tier.cache_write_12h != null" class="whitespace-nowrap">(<span class="cache-label">12h</span> {{ displayPrice(tier.cache_write_12h) }})</span>
                  <span class="whitespace-nowrap"><span class="cache-label">{{ t('customModelPlaza.read') }}</span> {{ displayPrice(tier.cache_read) }}</span>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CustomPlazaModel } from '@/api/customModelPlaza'
import { displayPrice, tierLabel } from './format'

defineProps<{ family: string; models: CustomPlazaModel[] }>()
const { t } = useI18n()

function priceRowClass(index: number, count: number) {
  return ['px-3 font-mono text-xs tabular-nums text-gray-800 dark:text-gray-100',
    index === 0 ? 'pt-3' : 'pt-0.5', index + 1 === count ? 'pb-3' : 'pb-0.5']
}
</script>

<style scoped>
.cache-label { @apply font-sans text-gray-500 dark:text-dark-400; }
</style>
