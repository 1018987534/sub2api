<template>
  <section
    v-if="loading || items.length > 0"
    data-testid="upstream-price-discrepancies"
    class="border border-amber-300 bg-amber-50 dark:border-amber-800 dark:bg-amber-950/20"
  >
    <header class="flex items-start justify-between gap-3 border-b border-amber-200 px-4 py-3 dark:border-amber-900">
      <div class="flex min-w-0 items-start gap-2.5">
        <Icon name="exclamationTriangle" size="md" class="mt-0.5 flex-none text-amber-700 dark:text-amber-400" />
        <div class="min-w-0">
          <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('admin.channels.upstreamPrice.title') }}
          </h2>
          <p class="mt-0.5 text-xs text-gray-600 dark:text-gray-400">
            {{ t('admin.channels.upstreamPrice.description') }}
          </p>
        </div>
      </div>
      <button
        type="button"
        class="btn btn-secondary p-2"
        :disabled="loading"
        :title="t('common.refresh')"
        :aria-label="t('common.refresh')"
        @click="$emit('refresh')"
      >
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
      </button>
    </header>

    <div v-if="loading" class="px-4 py-4 text-sm text-gray-500 dark:text-gray-400">
      {{ t('common.loading') }}
    </div>
    <div v-else class="overflow-x-auto">
      <table class="min-w-[920px] w-full text-left text-xs">
        <thead class="bg-amber-100/70 text-gray-600 dark:bg-amber-950/30 dark:text-gray-300">
          <tr>
            <th class="px-4 py-2 font-medium">{{ t('admin.channels.upstreamPrice.account') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('admin.channels.upstreamPrice.model') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('admin.channels.upstreamPrice.current') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('admin.channels.upstreamPrice.inferred') }}</th>
            <th class="px-4 py-2 font-medium">{{ t('admin.channels.upstreamPrice.evidence') }}</th>
            <th class="px-4 py-2 text-right font-medium">{{ t('common.actions') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-amber-200 bg-white/70 dark:divide-amber-900 dark:bg-dark-800/60">
          <tr v-for="item in items" :key="itemKey(item)" data-testid="upstream-price-discrepancy-row">
            <td class="px-4 py-3 align-top">
              <div class="font-medium text-gray-900 dark:text-white">{{ item.account_name }}</div>
              <div class="mt-0.5 text-gray-500">#{{ item.account_id }}</div>
            </td>
            <td class="px-4 py-3 align-top font-mono text-gray-900 dark:text-gray-100">
              {{ item.model }}
            </td>
            <td class="px-4 py-3 align-top">
              <div class="mb-1 text-gray-500">{{ sourceLabel(item.current_source) }}</div>
              <div v-for="line in priceLines(item.current_price, item.inferred_price)" :key="line.key" class="leading-5 text-gray-700 dark:text-gray-300">
                {{ line.label }} <span class="font-mono">{{ line.value }}</span>
              </div>
            </td>
            <td class="px-4 py-3 align-top">
              <div v-for="line in priceLines(item.inferred_price, item.inferred_price)" :key="line.key" class="leading-5 font-medium text-amber-800 dark:text-amber-300">
                {{ line.label }} <span class="font-mono">{{ line.value }}</span>
              </div>
            </td>
            <td class="px-4 py-3 align-top text-gray-600 dark:text-gray-400">
              <div>{{ t('admin.channels.upstreamPrice.samples', { count: item.inferred_price.sample_count }) }}</div>
              <div class="mt-1">{{ formatObservedAt(item.inferred_price.observed_at) }}</div>
            </td>
            <td class="px-4 py-3 text-right align-top">
              <button
                type="button"
                class="btn btn-primary inline-flex items-center gap-1.5 whitespace-nowrap"
                :disabled="syncingKey === itemKey(item)"
                data-testid="confirm-upstream-price"
                @click="$emit('confirm', item)"
              >
                <Icon name="sync" size="sm" :class="syncingKey === itemKey(item) ? 'animate-spin' : ''" />
                {{ t('admin.channels.upstreamPrice.confirm') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type {
  UpstreamBillingInferredModelPrice,
  UpstreamBillingModelPrice,
  UpstreamBillingPriceDiscrepancy
} from '@/api/admin/accounts'
import Icon from '@/components/icons/Icon.vue'

defineProps<{
  items: UpstreamBillingPriceDiscrepancy[]
  loading: boolean
  syncingKey: string | null
}>()

defineEmits<{
  refresh: []
  confirm: [item: UpstreamBillingPriceDiscrepancy]
}>()

const { t } = useI18n()

function itemKey(item: UpstreamBillingPriceDiscrepancy): string {
  return `${item.account_id}:${item.model}`
}

function formatPerMillion(value: number | undefined): string {
  const perMillion = Number(value || 0) * 1_000_000
  return `$${new Intl.NumberFormat(undefined, { maximumFractionDigits: 6 }).format(perMillion)}/M`
}

function priceLines(
  price: UpstreamBillingModelPrice,
  evidence: UpstreamBillingInferredModelPrice
): Array<{ key: string; label: string; value: string }> {
  const lines: Array<{ key: string; label: string; value: string }> = []
  if ((evidence.input_sample_count || 0) > 0) {
    lines.push({ key: 'input', label: t('admin.channels.upstreamPrice.input'), value: formatPerMillion(price.input_price_per_token) })
  }
  if ((evidence.output_sample_count || 0) > 0) {
    lines.push({ key: 'output', label: t('admin.channels.upstreamPrice.output'), value: formatPerMillion(price.output_price_per_token) })
  }
  if ((evidence.cache_creation_sample_count || 0) > 0) {
    lines.push({ key: 'cache-write', label: t('admin.channels.upstreamPrice.cacheWrite'), value: formatPerMillion(price.cache_creation_price_per_token) })
  }
  if ((evidence.cache_read_sample_count || 0) > 0) {
    lines.push({ key: 'cache-read', label: t('admin.channels.upstreamPrice.cacheRead'), value: formatPerMillion(price.cache_read_price_per_token) })
  }
  return lines
}

function sourceLabel(source: UpstreamBillingPriceDiscrepancy['current_source']): string {
  return t(`admin.channels.upstreamPrice.source.${source}`)
}

function formatObservedAt(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString()
}
</script>
