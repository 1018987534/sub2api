<template>
  <div
    v-if="models.length"
    class="overflow-x-auto rounded-lg border border-gray-100 dark:border-dark-700/60"
    data-testid="monitor-official-pricing"
  >
    <table class="w-full min-w-[620px] text-left text-sm tabular-nums">
      <thead class="border-b border-gray-200 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-900/40">
        <tr class="text-[11px] font-semibold uppercase text-gray-500 dark:text-gray-400">
          <th class="px-3 py-2.5">{{ t('channelStatus.detailColumns.model') }}</th>
          <th class="px-3 py-2.5">{{ t('channelStatus.models.input') }}</th>
          <th class="px-3 py-2.5">{{ t('channelStatus.models.output') }}</th>
          <th class="px-3 py-2.5">{{ t('channelStatus.models.cacheWrite') }}</th>
          <th class="px-3 py-2.5">{{ t('channelStatus.models.cacheRead') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="model in models"
          :key="`${model.platform}:${model.name}`"
          class="border-b border-gray-100 last:border-b-0 dark:border-dark-800"
        >
          <td class="px-3 py-2.5 font-medium text-gray-900 dark:text-gray-100">
            <div>{{ model.name }}</div>
            <div class="mt-0.5 text-[10px] font-normal text-gray-400 dark:text-gray-500">
              {{ model.platform }}
            </div>
          </td>
          <td class="px-3 py-2.5 font-mono text-xs text-gray-600 dark:text-gray-300">
            {{ official(model.official_pricing?.input_price) }}
          </td>
          <td class="px-3 py-2.5 font-mono text-xs text-gray-600 dark:text-gray-300">
            {{ official(model.official_pricing?.output_price) }}
          </td>
          <td class="px-3 py-2.5 font-mono text-xs text-gray-600 dark:text-gray-300">
            <span v-if="model.official_pricing?.cache_write_price != null">
              {{ official(model.official_pricing.cache_write_price) }}
              <span v-if="model.official_pricing.cache_write_1h_price != null" class="text-gray-400">
                / {{ official(model.official_pricing.cache_write_1h_price) }} (1h)
              </span>
            </span>
            <span v-else>-</span>
          </td>
          <td class="px-3 py-2.5 font-mono text-xs text-gray-600 dark:text-gray-300">
            {{ official(model.official_pricing?.cache_read_price) }}
          </td>
        </tr>
      </tbody>
    </table>
  </div>
  <p v-else class="rounded-xl border border-dashed border-gray-200 px-4 py-6 text-center text-sm text-gray-400 dark:border-dark-700 dark:text-dark-500">
    {{ t('channelStatus.models.noPricing') }}
  </p>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { UserMonitorModelPricing } from '@/api/channelMonitor'
import { formatScaled } from '@/utils/pricing'

defineProps<{
  models: UserMonitorModelPricing[]
}>()

const { t } = useI18n()

function official(value: number | null | undefined): string {
  return formatScaled(value ?? null, 1_000_000, 2)
}
</script>
