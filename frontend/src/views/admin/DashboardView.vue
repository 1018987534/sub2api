<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>

      <template v-else-if="stats">
        <!-- Row 1: Core Stats -->
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <!-- Total API Keys -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-blue-100 p-2 dark:bg-blue-900/30">
                <Icon name="key" size="md" class="text-blue-600 dark:text-blue-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.apiKeys') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.total_api_keys }}
                </p>
                <p class="text-xs text-green-600 dark:text-green-400">
                  {{ stats.active_api_keys }} {{ t('common.active') }}
                </p>
              </div>
            </div>
          </div>

          <!-- Service Accounts -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-purple-100 p-2 dark:bg-purple-900/30">
                <Icon name="server" size="md" class="text-purple-600 dark:text-purple-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.accounts') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.total_accounts }}
                </p>
                <p class="text-xs">
                  <span class="text-green-600 dark:text-green-400"
                    >{{ stats.normal_accounts }} {{ t('common.active') }}</span
                  >
                  <span v-if="stats.error_accounts > 0" class="ml-1 text-red-500"
                    >{{ stats.error_accounts }} {{ t('common.error') }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Today Requests -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-green-100 p-2 dark:bg-green-900/30">
                <Icon name="chart" size="md" class="text-green-600 dark:text-green-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.todayRequests') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.today_requests }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('common.total') }}: {{ formatNumber(stats.total_requests) }}
                </p>
              </div>
            </div>
          </div>

          <!-- New Users Today -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-emerald-100 p-2 dark:bg-emerald-900/30">
                <Icon name="userPlus" size="md" class="text-emerald-600 dark:text-emerald-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.users') }}
                </p>
                <p class="text-xl font-bold text-emerald-600 dark:text-emerald-400">
                  +{{ stats.today_new_users }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('common.total') }}: {{ formatNumber(stats.total_users) }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <!-- Row 2: Token Stats -->
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <!-- Today Tokens -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-amber-100 p-2 dark:bg-amber-900/30">
                <Icon name="cube" size="md" class="text-amber-600 dark:text-amber-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.todayTokens') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatTokens(stats.today_tokens) }}
                </p>
                <p class="text-xs">
                  <span
                    class="text-green-600 dark:text-green-400"
                    :title="t('admin.dashboard.actual')"
                    >${{ formatCost(stats.today_actual_cost) }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="text-orange-500 dark:text-orange-400"
                    :title="t('admin.dashboard.accountCost')"
                    >${{ formatCost(stats.today_account_cost) }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="text-gray-400 dark:text-gray-500"
                    :title="t('admin.dashboard.standard')"
                    >${{ formatCost(stats.today_cost) }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Total Tokens -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-indigo-100 p-2 dark:bg-indigo-900/30">
                <Icon name="database" size="md" class="text-indigo-600 dark:text-indigo-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.totalTokens') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatTokens(stats.total_tokens) }}
                </p>
                <p class="text-xs">
                  <span
                    class="text-green-600 dark:text-green-400"
                    :title="t('admin.dashboard.actual')"
                    >${{ formatCost(stats.total_actual_cost) }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="text-orange-500 dark:text-orange-400"
                    :title="t('admin.dashboard.accountCost')"
                    >${{ formatCost(stats.total_account_cost) }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="text-gray-400 dark:text-gray-500"
                    :title="t('admin.dashboard.standard')"
                    >${{ formatCost(stats.total_cost) }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Performance (RPM/TPM) -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-violet-100 p-2 dark:bg-violet-900/30">
                <Icon name="bolt" size="md" class="text-violet-600 dark:text-violet-400" :stroke-width="2" />
              </div>
              <div class="flex-1">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.performance') }}
                </p>
                <div class="flex items-baseline gap-2">
                  <p class="text-xl font-bold text-gray-900 dark:text-white">
                    {{ formatTokens(stats.rpm) }}
                  </p>
                  <span class="text-xs text-gray-500 dark:text-gray-400">RPM</span>
                </div>
                <div class="flex items-baseline gap-2">
                  <p class="text-sm font-semibold text-violet-600 dark:text-violet-400">
                    {{ formatTokens(stats.tpm) }}
                  </p>
                  <span class="text-xs text-gray-500 dark:text-gray-400">TPM</span>
                </div>
              </div>
            </div>
          </div>

          <!-- Avg Response Time -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-rose-100 p-2 dark:bg-rose-900/30">
                <Icon name="clock" size="md" class="text-rose-600 dark:text-rose-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.avgResponse') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatDuration(stats.average_duration_ms) }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ stats.active_users }} {{ t('admin.dashboard.activeUsers') }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <section class="border-y border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800/40" data-testid="first-token-latency-panel">
          <div class="flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.dashboard.firstTokenLatencyTitle') }}
              </h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.dashboard.firstTokenLatencyDescription') }}
              </p>
            </div>
            <div class="flex w-full items-center gap-2 sm:w-auto">
              <div class="min-w-0 flex-1 sm:w-52 sm:flex-none" data-testid="first-token-group-filter">
                <Select
                  v-model="firstTokenGroupFilter"
                  :options="firstTokenGroupOptions"
                  :aria-label="t('admin.dashboard.firstTokenGroupFilter')"
                  :disabled="firstTokenLoading"
                />
              </div>
              <button
                type="button"
                class="btn btn-secondary inline-flex shrink-0 items-center gap-2"
                :disabled="firstTokenLoading"
                :title="t('common.refresh')"
                data-testid="refresh-first-token-latencies"
                @click="loadFirstTokenLatencies"
              >
                <Icon name="refresh" size="sm" />
                {{ t('common.refresh') }}
              </button>
            </div>
          </div>
          <div v-if="firstTokenLoading" class="flex min-h-32 items-center justify-center border-t border-gray-100 dark:border-dark-700">
            <LoadingSpinner size="md" />
          </div>
          <div v-else-if="firstTokenError" class="border-t border-gray-100 px-4 py-8 text-center text-sm text-red-600 dark:border-dark-700 dark:text-red-400">
            {{ t('admin.dashboard.firstTokenLatencyFailed') }}
          </div>
          <div v-else-if="firstTokenMetrics.length === 0" class="border-t border-gray-100 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
            {{ t('admin.dashboard.firstTokenLatencyEmpty') }}
          </div>
          <div v-else class="border-t border-gray-100 dark:border-dark-700">
            <section v-for="group in visibleFirstTokenGroups" :key="group.id" data-testid="first-token-group-section">
              <div class="flex items-center justify-between border-b border-gray-100 bg-gray-50 px-4 py-2.5 dark:border-dark-700 dark:bg-dark-800">
                <div class="min-w-0">
                  <h3 class="truncate text-sm font-semibold text-gray-900 dark:text-white" :title="group.name">{{ group.name }}</h3>
                  <span v-if="group.id > 0" class="text-xs text-gray-400">#{{ group.id }}</span>
                </div>
                <span class="shrink-0 text-xs tabular-nums text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.firstTokenPoolCounts', { total: group.metrics.length, fast: group.fastCount, slow: group.slowCount }) }}
                </span>
              </div>
              <div class="divide-y divide-gray-100 md:hidden dark:divide-dark-700">
                <div v-for="metric in group.metrics" :key="`mobile:${group.id}:${metric.account_id}`" class="px-4 py-3" data-testid="first-token-mobile-row">
                  <div class="flex items-start justify-between gap-3">
                    <div class="min-w-0">
                      <div class="truncate font-medium text-gray-900 dark:text-white" :title="metric.account_name">{{ metric.account_name }}</div>
                      <div class="text-xs text-gray-400">#{{ metric.account_id }}</div>
                    </div>
                    <span class="inline-flex shrink-0 items-center gap-1.5 text-sm font-medium" :class="metric.is_fast_pool ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
                      <span class="h-2 w-2 rounded-full" :class="metric.is_fast_pool ? 'bg-emerald-500' : 'bg-amber-500'" aria-hidden="true"></span>
                      {{ t(metric.is_fast_pool ? 'admin.dashboard.firstTokenFastPool' : 'admin.dashboard.firstTokenSlowPool') }}
                    </span>
                  </div>
                  <dl class="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
                    <div>
                      <dt class="text-gray-400">{{ t('admin.dashboard.firstTokenPrediction') }}</dt>
                      <dd class="mt-0.5 font-mono text-sm font-semibold" :class="metric.is_fast_pool ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
                        {{ metric.has_prediction ? formatDuration(metric.predicted_ms) : t('admin.dashboard.firstTokenPendingSample') }}
                      </dd>
                    </div>
                    <div>
                      <dt class="text-gray-400">{{ t('admin.dashboard.firstTokenSchedulingRate') }}</dt>
                      <dd class="mt-0.5 font-mono text-sm text-gray-700 dark:text-gray-300">
                        {{ metric.scheduling_rate_multiplier == null ? '-' : `${formatMultiplier(metric.scheduling_rate_multiplier)}x` }}
                      </dd>
                    </div>
                    <div>
                      <dt class="text-gray-400">{{ t('admin.dashboard.firstTokenSamples') }}</dt>
                      <dd class="mt-0.5 text-gray-700 dark:text-gray-300">{{ metric.sample_count }}</dd>
                    </div>
                    <div>
                      <dt class="text-gray-400">{{ t('admin.dashboard.firstTokenProbeInterval') }}</dt>
                      <dd class="mt-0.5 text-gray-700 dark:text-gray-300">{{ formatProbeInterval(metric.probe_interval_seconds) }}</dd>
                    </div>
                  </dl>
                </div>
              </div>
              <div class="hidden overflow-x-auto md:block">
                <table class="min-w-[860px] w-full table-fixed text-left text-sm">
                  <thead class="text-xs text-gray-500 dark:text-gray-400">
                    <tr>
                      <th class="w-[24%] px-4 py-2.5 font-medium">{{ t('admin.dashboard.firstTokenAccount') }}</th>
                      <th class="w-[15%] px-4 py-2.5 font-medium">{{ t('admin.dashboard.firstTokenPrediction') }}</th>
                      <th class="w-[12%] px-4 py-2.5 font-medium">{{ t('admin.dashboard.firstTokenPool') }}</th>
                      <th class="w-[13%] px-4 py-2.5 font-medium">{{ t('admin.dashboard.firstTokenSchedulingRate') }}</th>
                      <th class="w-[10%] px-4 py-2.5 font-medium">{{ t('admin.dashboard.firstTokenSamples') }}</th>
                      <th class="w-[14%] px-4 py-2.5 font-medium">{{ t('admin.dashboard.firstTokenUpdated') }}</th>
                      <th class="w-[12%] px-4 py-2.5 font-medium">{{ t('admin.dashboard.firstTokenProbeInterval') }}</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                    <tr v-for="metric in group.metrics" :key="`${group.id}:${metric.account_id}`" data-testid="first-token-latency-row">
                      <td class="px-4 py-3">
                        <div class="truncate font-medium text-gray-900 dark:text-white" :title="metric.account_name">{{ metric.account_name }}</div>
                        <div class="text-xs text-gray-400">#{{ metric.account_id }}</div>
                      </td>
                      <td data-testid="first-token-prediction" class="px-4 py-3 font-mono font-semibold" :class="metric.is_fast_pool ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
                        {{ metric.has_prediction ? formatDuration(metric.predicted_ms) : t('admin.dashboard.firstTokenPendingSample') }}
                      </td>
                      <td class="px-4 py-3" data-testid="first-token-pool">
                        <span class="inline-flex items-center gap-1.5 font-medium" :class="metric.is_fast_pool ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
                          <span class="h-2 w-2 shrink-0 rounded-full" :class="metric.is_fast_pool ? 'bg-emerald-500' : 'bg-amber-500'" aria-hidden="true"></span>
                          {{ t(metric.is_fast_pool ? 'admin.dashboard.firstTokenFastPool' : 'admin.dashboard.firstTokenSlowPool') }}
                        </span>
                      </td>
                      <td class="px-4 py-3 font-mono text-gray-700 dark:text-gray-300" data-testid="first-token-scheduling-rate">
                        {{ metric.scheduling_rate_multiplier == null ? '-' : `${formatMultiplier(metric.scheduling_rate_multiplier)}x` }}
                      </td>
                      <td class="px-4 py-3 text-gray-700 dark:text-gray-300">{{ metric.sample_count }}</td>
                      <td class="px-4 py-3 text-xs text-gray-500 dark:text-gray-400">{{ metric.has_prediction ? formatFirstTokenUpdatedAt(metric.updated_at) : '-' }}</td>
                      <td class="px-4 py-3 text-gray-700 dark:text-gray-300">{{ formatProbeInterval(metric.probe_interval_seconds) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>
          </div>
        </section>

        <!-- Quick Actions -->
        <div class="card p-4">
          <div class="mb-3 flex items-center justify-between">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.dashboard.quickActions') }}
            </h2>
          </div>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <button
              v-if="canUseBatchImage"
              type="button"
              class="group flex items-center gap-3 rounded-lg bg-gray-50 p-3 text-left transition-colors hover:bg-sky-50 dark:bg-dark-800/50 dark:hover:bg-sky-900/20"
              @click="router.push('/batch-image')"
            >
              <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-sky-100 text-sky-600 dark:bg-sky-900/30 dark:text-sky-400">
                <Icon name="sparkles" size="md" :stroke-width="2" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="block text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.dashboard.batchImage') }}
                </span>
                <span class="block text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.batchImageDesc') }}
                </span>
              </span>
              <Icon name="chevronRight" size="sm" class="text-gray-400 group-hover:text-sky-500" />
            </button>
            <button
              type="button"
              class="group flex items-center gap-3 rounded-lg bg-gray-50 p-3 text-left transition-colors hover:bg-emerald-50 dark:bg-dark-800/50 dark:hover:bg-emerald-900/20"
              @click="router.push('/admin/groups')"
            >
              <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-emerald-100 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400">
                <Icon name="grid" size="md" :stroke-width="2" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="block text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.dashboard.groupPricing') }}
                </span>
                <span class="block text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.groupPricingDesc') }}
                </span>
              </span>
              <Icon name="chevronRight" size="sm" class="text-gray-400 group-hover:text-emerald-500" />
            </button>
          </div>
        </div>

        <!-- Charts Section -->
        <div class="space-y-6">
          <!-- Date Range Filter -->
          <div class="card p-4">
            <div class="flex flex-wrap items-center gap-4">
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t('admin.dashboard.timeRange') }}:</span
                >
                <DateRangePicker
                  v-model:start-date="startDate"
                  v-model:end-date="endDate"
                  @change="onDateRangeChange"
                />
              </div>
              <button @click="loadDashboardStats" :disabled="chartsLoading" class="btn btn-secondary">
                {{ t('common.refresh') }}
              </button>
              <div class="ml-auto flex items-center gap-2">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t('admin.dashboard.granularity') }}:</span
                >
                <div class="w-28">
                  <Select
                    v-model="granularity"
                    :options="granularityOptions"
                    @change="loadChartData"
                  />
                </div>
              </div>
            </div>
          </div>

          <!-- Charts Grid -->
          <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <ModelDistributionChart
              :model-stats="modelStats"
              :enable-ranking-view="true"
              :ranking-items="rankingItems"
              :ranking-total-actual-cost="rankingTotalActualCost"
              :ranking-total-requests="rankingTotalRequests"
              :ranking-total-tokens="rankingTotalTokens"
              :loading="chartsLoading"
              :ranking-loading="rankingLoading"
              :ranking-error="rankingError"
              :start-date="startDate"
              :end-date="endDate"
              @ranking-click="goToUserUsage"
            />
            <TokenUsageTrend :trend-data="trendData" :loading="chartsLoading" />
          </div>

          <!-- User Usage Trend (Full Width) -->
          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.dashboard.recentUsage') }} (Top 12)
            </h3>
            <div class="h-64">
              <div v-if="userTrendLoading" class="flex h-full items-center justify-center">
                <LoadingSpinner size="md" />
              </div>
              <Line v-else-if="userTrendChartData" :data="userTrendChartData" :options="lineOptions" />
              <div
                v-else
                class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.dashboard.noDataAvailable') }}
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
import { adminAPI } from '@/api/admin'
import type {
  DashboardStats,
  TrendDataPoint,
  ModelStat,
  UserUsageTrendPoint,
  UserSpendingRankingItem,
  AccountFirstTokenLatencyMetric
} from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { formatMultiplier } from '@/utils/formatters'

import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'

// Register Chart.js components
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
)

const appStore = useAppStore()
const router = useRouter()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()
const stats = ref<DashboardStats | null>(null)
const loading = ref(false)
const chartsLoading = ref(false)
const userTrendLoading = ref(false)
const rankingLoading = ref(false)
const rankingError = ref(false)
const firstTokenLoading = ref(false)
const firstTokenError = ref(false)
const firstTokenMetrics = ref<AccountFirstTokenLatencyMetric[]>([])
const firstTokenGroupFilter = ref<string | number>('all')

interface FirstTokenGroupSection {
  id: number
  name: string
  metrics: AccountFirstTokenLatencyMetric[]
  fastCount: number
  slowCount: number
}

const firstTokenGroupSections = computed<FirstTokenGroupSection[]>(() => {
  const sections = new Map<number, FirstTokenGroupSection>()
  for (const metric of firstTokenMetrics.value) {
    const groups = metric.groups?.length
      ? metric.groups
      : [{ group_id: 0, group_name: t('admin.dashboard.firstTokenUngrouped') }]
    for (const group of groups) {
      const name = group.group_name || `#${group.group_id}`
      const section = sections.get(group.group_id) ?? { id: group.group_id, name, metrics: [], fastCount: 0, slowCount: 0 }
      section.metrics.push(metric)
      if (metric.is_fast_pool) section.fastCount += 1
      else section.slowCount += 1
      sections.set(group.group_id, section)
    }
  }
  const sorted = Array.from(sections.values()).sort((left, right) => {
    if (left.id === 0) return 1
    if (right.id === 0) return -1
    return left.name.localeCompare(right.name, undefined, { numeric: true })
  })
  for (const section of sorted) {
    section.metrics.sort((left, right) => {
      if (left.is_fast_pool !== right.is_fast_pool) return left.is_fast_pool ? -1 : 1
      if (left.is_fast_pool && right.is_fast_pool) {
        const leftRate = left.scheduling_rate_multiplier ?? Number.POSITIVE_INFINITY
        const rightRate = right.scheduling_rate_multiplier ?? Number.POSITIVE_INFINITY
        if (leftRate !== rightRate) return leftRate - rightRate
      }
      if (left.has_prediction !== right.has_prediction) return left.has_prediction ? -1 : 1
      if (left.predicted_ms !== right.predicted_ms) return left.predicted_ms - right.predicted_ms
      return left.account_id - right.account_id
    })
  }
  return sorted
})

const firstTokenGroupOptions = computed(() => [
  { value: 'all', label: t('admin.dashboard.firstTokenAllGroups') },
  ...firstTokenGroupSections.value.map(group => ({
    value: group.id,
    label: `${group.name} (${group.metrics.length})`
  }))
])

const visibleFirstTokenGroups = computed(() => {
  if (firstTokenGroupFilter.value === 'all') return firstTokenGroupSections.value
  return firstTokenGroupSections.value.filter(group => group.id === Number(firstTokenGroupFilter.value))
})

// Chart data
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const userTrend = ref<UserUsageTrendPoint[]>([])
const rankingItems = ref<UserSpendingRankingItem[]>([])
const rankingTotalActualCost = ref(0)
const rankingTotalRequests = ref(0)
const rankingTotalTokens = ref(0)
let chartLoadSeq = 0
let usersTrendLoadSeq = 0
let rankingLoadSeq = 0
const rankingLimit = 12

// Helper function to format date in local timezone
const formatLocalDate = (date: Date): string => {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

const getTodayRangeDates = (): { start: string; end: string } => {
  const today = formatLocalDate(new Date())
  return { start: today, end: today }
}

// Date range
const granularity = ref<'day' | 'hour'>('hour')
const defaultRange = getTodayRangeDates()
const startDate = ref(defaultRange.start)
const endDate = ref(defaultRange.end)

// Granularity options for Select component
const granularityOptions = computed(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') }
])

// Dark mode detection
const isDarkMode = computed(() => {
  return document.documentElement.classList.contains('dark')
})

// Chart colors
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#e5e7eb' : '#374151',
  grid: isDarkMode.value ? '#374151' : '#e5e7eb'
}))

// Line chart options (for user trend chart)
const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    intersect: false,
    mode: 'index' as const
  },
  plugins: {
    legend: {
      position: 'top' as const,
      labels: {
        color: chartColors.value.text,
        usePointStyle: true,
        pointStyle: 'circle',
        padding: 15,
        font: {
          size: 11
        }
      }
    },
    tooltip: {
      itemSort: (a: any, b: any) => {
        const aValue = typeof a?.raw === 'number' ? a.raw : Number(a?.parsed?.y ?? 0)
        const bValue = typeof b?.raw === 'number' ? b.raw : Number(b?.parsed?.y ?? 0)
        return bValue - aValue
      },
      callbacks: {
        label: (context: any) => {
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
        }
      }
    }
  },
  scales: {
    x: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        }
      }
    },
    y: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        },
        callback: (value: string | number) => formatTokens(Number(value))
      }
    }
  }
}))

// User trend chart data
const userTrendChartData = computed(() => {
  if (!userTrend.value?.length) return null

  const getDisplayName = (point: UserUsageTrendPoint): string => {
    const username = point.username?.trim()
    if (username) {
      return username
    }

    const email = point.email?.trim()
    if (email) {
      return email
    }

    return t('admin.redeem.userPrefix', { id: point.user_id })
  }

  // Group by user_id to avoid merging different users with the same display name
  const userGroups = new Map<number, { name: string; data: Map<string, number> }>()
  const allDates = new Set<string>()

  userTrend.value.forEach((point) => {
    allDates.add(point.date)
    const key = point.user_id
    if (!userGroups.has(key)) {
      userGroups.set(key, { name: getDisplayName(point), data: new Map() })
    }
    userGroups.get(key)!.data.set(point.date, point.tokens)
  })

  const sortedDates = Array.from(allDates).sort()
  const colors = [
    '#3b82f6',
    '#10b981',
    '#f59e0b',
    '#ef4444',
    '#8b5cf6',
    '#ec4899',
    '#14b8a6',
    '#f97316',
    '#6366f1',
    '#84cc16',
    '#06b6d4',
    '#a855f7'
  ]

  const datasets = Array.from(userGroups.values()).map((group, idx) => ({
    label: group.name,
    data: sortedDates.map((date) => group.data.get(date) || 0),
    borderColor: colors[idx % colors.length],
    backgroundColor: `${colors[idx % colors.length]}20`,
    fill: false,
    tension: 0.3
  }))

  return {
    labels: sortedDates,
    datasets
  }
})

// Format helpers
const formatTokens = (value: number | undefined): string => {
  if (value === undefined || value === null) return '0'
  if (value >= 1_000_000_000) {
    return `${(value / 1_000_000_000).toFixed(2)}B`
  } else if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(2)}M`
  } else if (value >= 1_000) {
    return `${(value / 1_000).toFixed(2)}K`
  }
  return value.toLocaleString()
}

const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? numberValue : 0
}

const formatNumber = (value: number | null | undefined): string => {
  return toFiniteNumber(value).toLocaleString()
}

const formatCost = (value: number | null | undefined): string => {
  const safeValue = toFiniteNumber(value)
  if (safeValue >= 1000) {
    return (safeValue / 1000).toFixed(2) + 'K'
  } else if (safeValue >= 1) {
    return safeValue.toFixed(2)
  } else if (safeValue >= 0.01) {
    return safeValue.toFixed(3)
  }
  return safeValue.toFixed(4)
}

const formatDuration = (ms: number): string => {
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
}

const formatFirstTokenUpdatedAt = (value: string): string => {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  }).format(date)
}

const formatProbeInterval = (seconds: number): string => {
  if (seconds >= 3600) return `${Math.round(seconds / 3600)}h`
  if (seconds >= 60) return `${Math.round(seconds / 60)}m`
  return `${Math.max(0, Math.round(seconds))}s`
}

const goToUserUsage = (item: UserSpendingRankingItem) => {
  void router.push({
    path: '/admin/usage',
    query: {
      user_id: String(item.user_id),
      start_date: startDate.value,
      end_date: endDate.value
    }
  })
}

// Date range change handler
const onDateRangeChange = (range: {
  startDate: string
  endDate: string
  preset: string | null
}) => {
  // Auto-select granularity based on date range
  const start = new Date(range.startDate)
  const end = new Date(range.endDate)
  const daysDiff = Math.ceil((end.getTime() - start.getTime()) / (1000 * 60 * 60 * 24))

  // If range is 1 day, use hourly granularity
  if (daysDiff <= 1) {
    granularity.value = 'hour'
  } else {
    granularity.value = 'day'
  }

  loadChartData()
}

// Load data
const loadDashboardSnapshot = async (includeStats: boolean) => {
  const currentSeq = ++chartLoadSeq
  if (includeStats && !stats.value) {
    loading.value = true
  }
  chartsLoading.value = true
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      include_stats: includeStats,
      include_trend: true,
      include_model_stats: true,
      include_group_stats: false,
      include_users_trend: false
    })
    if (currentSeq !== chartLoadSeq) return
    if (includeStats && response.stats) {
      stats.value = response.stats
    }
    trendData.value = response.trend || []
    modelStats.value = response.models || []
  } catch (error) {
    if (currentSeq !== chartLoadSeq) return
    appStore.showError(t('admin.dashboard.failedToLoad'))
    console.error('Error loading dashboard snapshot:', error)
  } finally {
    if (currentSeq === chartLoadSeq) {
      loading.value = false
      chartsLoading.value = false
    }
  }
}

const loadUsersTrend = async () => {
  const currentSeq = ++usersTrendLoadSeq
  userTrendLoading.value = true
  try {
    const response = await adminAPI.dashboard.getUserUsageTrend({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      limit: 12
    })
    if (currentSeq !== usersTrendLoadSeq) return
    userTrend.value = response.trend || []
  } catch (error) {
    if (currentSeq !== usersTrendLoadSeq) return
    console.error('Error loading users trend:', error)
    userTrend.value = []
  } finally {
    if (currentSeq === usersTrendLoadSeq) {
      userTrendLoading.value = false
    }
  }
}

const loadUserSpendingRanking = async () => {
  const currentSeq = ++rankingLoadSeq
  rankingLoading.value = true
  rankingError.value = false
  try {
    const response = await adminAPI.dashboard.getUserSpendingRanking({
      start_date: startDate.value,
      end_date: endDate.value,
      limit: rankingLimit
    })
    if (currentSeq !== rankingLoadSeq) return
    rankingItems.value = response.ranking || []
    rankingTotalActualCost.value = response.total_actual_cost || 0
    rankingTotalRequests.value = response.total_requests || 0
    rankingTotalTokens.value = response.total_tokens || 0
  } catch (error) {
    if (currentSeq !== rankingLoadSeq) return
    console.error('Error loading user spending ranking:', error)
    rankingItems.value = []
    rankingTotalActualCost.value = 0
    rankingTotalRequests.value = 0
    rankingTotalTokens.value = 0
    rankingError.value = true
  } finally {
    if (currentSeq === rankingLoadSeq) {
      rankingLoading.value = false
    }
  }
}

const loadFirstTokenLatencies = async () => {
  firstTokenLoading.value = true
  firstTokenError.value = false
  try {
    const response = await adminAPI.accounts.getFirstTokenLatencies()
    firstTokenMetrics.value = response.items || []
    if (firstTokenGroupFilter.value !== 'all' && !firstTokenGroupSections.value.some(group => group.id === Number(firstTokenGroupFilter.value))) {
      firstTokenGroupFilter.value = 'all'
    }
  } catch (error) {
    console.error('Error loading account first-token latencies:', error)
    firstTokenMetrics.value = []
    firstTokenError.value = true
  } finally {
    firstTokenLoading.value = false
  }
}

const loadDashboardStats = async () => {
  await Promise.all([
    loadDashboardSnapshot(true),
    loadUsersTrend(),
    loadUserSpendingRanking()
  ])
}

const loadChartData = async () => {
  await Promise.all([
    loadDashboardSnapshot(false),
    loadUsersTrend(),
    loadUserSpendingRanking()
  ])
}

onMounted(() => {
  void refreshBatchImageAccess()
  void Promise.all([loadDashboardStats(), loadFirstTokenLatencies()])
})
</script>

<style scoped>
</style>
