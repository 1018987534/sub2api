<template>
  <section
    class="mb-5 overflow-hidden rounded-2xl border border-gray-200/80 bg-white/70 shadow-card backdrop-blur-xl dark:border-dark-700/70 dark:bg-dark-800/60"
    data-testid="monitor-group-metrics"
  >
    <header
      class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700"
    >
      <div class="min-w-0">
        <h2 class="text-sm font-bold text-gray-900 dark:text-white">
          {{ t('channelStatus.groupMetrics.title') }}
        </h2>
        <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
          {{ t('channelStatus.groupMetrics.description') }}
        </p>
      </div>
      <span
        class="shrink-0 rounded-full bg-gray-100 px-2.5 py-1 text-xs font-semibold text-gray-600 dark:bg-dark-700 dark:text-gray-300"
      >
        {{ t('channelStatus.groupMetrics.count', { n: rows.length }) }}
      </span>
    </header>

    <div v-if="loading" class="grid grid-cols-2 gap-3 p-4 sm:grid-cols-3 lg:grid-cols-4">
      <div v-for="n in 4" :key="n" class="h-20 animate-pulse rounded-xl bg-gray-100 dark:bg-dark-900/40" />
    </div>
    <div v-else-if="rows.length === 0" class="px-5 py-8 text-center text-sm text-gray-400">
      {{ t('channelStatus.groupMetrics.empty') }}
    </div>
    <div v-else>
      <div class="grid gap-3 p-4 md:hidden">
        <article
          v-for="row in rows"
          :key="`mobile-${row.platform}:${row.group_id}`"
          class="rounded-xl border border-gray-100 bg-gray-50/70 p-3 dark:border-dark-700 dark:bg-dark-900/30"
          data-testid="monitor-group-metric-card"
        >
          <div class="flex min-w-0 items-center gap-2">
            <span class="h-2 w-2 shrink-0 rounded-full bg-sky-500" aria-hidden="true" />
            <div class="min-w-0">
              <strong class="block truncate font-semibold text-gray-900 dark:text-white" :title="row.group_name">
                {{ row.group_name || `#${row.group_id}` }}
              </strong>
              <span class="text-xs text-gray-400">{{ providerLabel(row.platform) }} · #{{ row.group_id }}</span>
            </div>
          </div>
          <dl class="mt-3 grid grid-cols-2 gap-3 border-t border-gray-200/70 pt-3 dark:border-dark-700">
            <div>
              <dt class="text-[11px] text-gray-500 dark:text-gray-400">
                {{ t('channelStatus.groupMetrics.firstToken') }}
              </dt>
              <dd class="mt-1 font-mono text-sm font-semibold tabular-nums text-sky-600 dark:text-sky-400">
                {{ formatFirstToken(row.first_token_p50_ms) }}
                <span v-if="row.first_token_sample_count > 0" class="ml-1 text-[11px] font-normal text-gray-400"
                  >({{ row.first_token_sample_count }})</span
                >
              </dd>
            </div>
            <div>
              <dt class="text-[11px] text-gray-500 dark:text-gray-400">
                {{ t('channelStatus.groupMetrics.cacheRate') }}
              </dt>
              <dd class="mt-1 font-mono text-sm font-semibold tabular-nums text-cyan-600 dark:text-cyan-400">
                {{ formatCacheRate(row.cache_rate) }}
              </dd>
            </div>
          </dl>
        </article>
      </div>
      <div class="hidden overflow-x-auto md:block">
        <table class="w-full min-w-[560px] text-left text-sm">
          <thead class="bg-gray-50/70 text-xs text-gray-500 dark:bg-dark-900/30 dark:text-gray-400">
            <tr>
              <th class="px-5 py-2.5 font-medium">
                {{ t('channelStatus.groupMetrics.group') }}
              </th>
              <th class="w-40 px-5 py-2.5 font-medium">
                {{ t('channelStatus.groupMetrics.firstToken') }}
              </th>
              <th class="w-40 px-5 py-2.5 font-medium">
                {{ t('channelStatus.groupMetrics.cacheRate') }}
              </th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="row in rows" :key="`${row.platform}:${row.group_id}`" data-testid="monitor-group-metric-row">
              <td class="px-5 py-3">
                <div class="flex min-w-0 items-center gap-2">
                  <span class="h-2 w-2 shrink-0 rounded-full bg-sky-500" aria-hidden="true" />
                  <div class="min-w-0">
                    <strong class="block truncate font-semibold text-gray-900 dark:text-white" :title="row.group_name">
                      {{ row.group_name || `#${row.group_id}` }}
                    </strong>
                    <span class="text-xs text-gray-400">{{ providerLabel(row.platform) }} · #{{ row.group_id }}</span>
                  </div>
                </div>
              </td>
              <td class="px-5 py-3 font-mono font-semibold tabular-nums text-sky-600 dark:text-sky-400">
                {{ formatFirstToken(row.first_token_p50_ms) }}
                <span v-if="row.first_token_sample_count > 0" class="ml-1 text-[11px] font-normal text-gray-400"
                  >({{ row.first_token_sample_count }})</span
                >
              </td>
              <td class="px-5 py-3 font-mono font-semibold tabular-nums text-cyan-600 dark:text-cyan-400">
                {{ formatCacheRate(row.cache_rate) }}
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
import type { UserMonitorGroupMetric } from '@/api/channelMonitor'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'

defineProps<{
  rows: UserMonitorGroupMetric[]
  loading: boolean
}>()

const { t } = useI18n()
const { providerLabel } = useChannelMonitorFormat()

function formatFirstToken(value: number | null): string {
  if (value == null || !Number.isFinite(value)) return '-'
  return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${Math.round(value)}ms`
}

function formatCacheRate(value: number | null): string {
  if (value == null || !Number.isFinite(value)) return '-'
  return `${(value * 100).toFixed(1)}%`
}
</script>
