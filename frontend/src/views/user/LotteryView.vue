<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <div class="flex items-center gap-3">
            <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
              <Icon name="gift" size="lg" />
            </div>
            <div>
              <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('lottery.title') }}</h1>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('lottery.description') }}</p>
            </div>
          </div>
        </div>
        <button class="btn btn-secondary self-start sm:self-auto" :disabled="loading" :title="t('lottery.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          <span>{{ t('lottery.refresh') }}</span>
        </button>
      </div>
      <CaptchaChallenge
        ref="captchaRef"
        :turnstile-enabled="false"
        turnstile-site-key=""
        :tencent-enabled="tencentCaptchaEnabled"
        :tencent-app-id="publicSettings?.tencent_captcha_app_id || ''"
        :tencent-region="publicSettings?.tencent_captcha_region || 'cn'"
        :aliyun-enabled="aliyunCaptchaEnabled"
        :aliyun-scene-id="publicSettings?.aliyun_captcha_scene_id || ''"
        :aliyun-prefix="publicSettings?.aliyun_captcha_prefix || ''"
        :aliyun-region="publicSettings?.aliyun_captcha_region || 'cn'"
        @error="handleCaptchaError"
      />

      <div v-if="loading" class="card flex justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
      </div>

      <template v-else>
        <div v-if="!current?.enabled || !current?.current_round" class="card border-dashed p-12 text-center">
          <Icon name="gift" size="xl" class="mx-auto text-gray-300 dark:text-dark-600" />
          <h2 class="mt-4 text-lg font-semibold text-gray-900 dark:text-white">{{ t('lottery.noRound') }}</h2>
        </div>

        <template v-else>
          <section class="overflow-hidden rounded-2xl border border-amber-200 bg-gradient-to-br from-amber-50 via-white to-orange-50 shadow-sm dark:border-amber-900/50 dark:from-amber-950/40 dark:via-dark-900 dark:to-orange-950/30">
            <div class="flex flex-col gap-5 p-5 sm:p-7 md:flex-row md:items-center md:justify-between">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2 text-sm font-medium text-amber-800 dark:text-amber-200">
                  <span class="rounded-full bg-amber-200/70 px-2.5 py-1 dark:bg-amber-900/60">#{{ current.current_round.round_no }}</span>
                  <span>{{ t('lottery.progress') }}</span>
                </div>
                <div class="mt-3 flex flex-wrap items-baseline gap-x-3 gap-y-1">
                  <span class="text-4xl font-bold tracking-tight text-gray-900 dark:text-white">{{ current.current_round.participant_count }}</span>
                  <span class="text-lg text-gray-500 dark:text-dark-400">/ {{ current.current_round.participant_threshold }}</span>
                  <span class="text-sm text-gray-500 dark:text-dark-400">{{ t('lottery.round') }} #{{ current.current_round.round_no }}</span>
                </div>
                <div class="mt-4 h-3 overflow-hidden rounded-full bg-amber-100 dark:bg-amber-900/50">
                  <div class="h-full rounded-full bg-amber-500 transition-all duration-500" :style="{ width: `${progress}%` }"></div>
                </div>
                <p class="mt-2 text-xs text-amber-800/80 dark:text-amber-200/80">{{ t(current.current_round.draw_mode === 'manual' ? 'lottery.manualWaiting' : 'lottery.waiting') }}</p>
              </div>
              <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 md:w-[360px]">
                <div class="rounded-xl border border-amber-200/80 bg-white/70 p-3 dark:border-amber-900/50 dark:bg-dark-900/50">
                  <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('lottery.prize') }}</p>
                  <p class="mt-1 text-xl font-semibold text-amber-700 dark:text-amber-300">{{ money(current.current_round.prize_amount) }}</p>
                </div>
                <div class="rounded-xl border border-amber-200/80 bg-white/70 p-3 dark:border-amber-900/50 dark:bg-dark-900/50">
                  <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('lottery.prizeCount') }}</p>
                  <p class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">{{ current.current_round.prize_count }}</p>
                </div>
                <div class="col-span-2 rounded-xl border border-amber-200/80 bg-white/70 p-3 dark:border-amber-900/50 dark:bg-dark-900/50 sm:col-span-1">
                  <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('lottery.eligibility') }}</p>
                  <p class="mt-1 text-sm font-medium" :class="current.eligibility.eligible || current.joined ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-600 dark:text-dark-300'">
                    {{ current.joined ? t('lottery.joined') : current.eligibility.eligible ? t('lottery.join') : t('lottery.notEligible') }}
                  </p>
                </div>
              </div>
            </div>
            <div class="flex flex-col gap-3 border-t border-amber-200/80 bg-white/50 p-4 dark:border-amber-900/50 dark:bg-dark-950/20 sm:flex-row sm:items-center sm:justify-between">
              <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-600 dark:text-dark-300">
                <span v-if="current.current_round.require_recharge">{{ t('lottery.rechargeRequired') }}</span>
                <span v-if="current.current_round.min_recharge_amount > 0">{{ t('lottery.minRecharge', { amount: money(current.current_round.min_recharge_amount) }) }}</span>
                <span v-if="current.current_round.min_account_age_days > 0">{{ t('lottery.accountAge', { days: current.current_round.min_account_age_days }) }}</span>
                <span v-if="!current.current_round.require_recharge && current.current_round.min_recharge_amount <= 0 && current.current_round.min_account_age_days <= 0">{{ t('lottery.join') }}</span>
              </div>
              <button class="btn btn-primary w-full sm:w-auto" :disabled="current.joined || !current.eligibility.eligible || joining" @click="join">
                <Icon v-if="joining" name="refresh" size="sm" class="animate-spin" />
                <Icon v-else name="gift" size="sm" />
                <span>{{ current.joined ? t('lottery.joined') : joining ? t('lottery.joining') : t('lottery.join') }}</span>
              </button>
            </div>
          </section>

          <div class="grid gap-6 lg:grid-cols-2">
            <section class="card p-5 sm:p-6">
              <div class="flex items-center justify-between">
                <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('lottery.recentWinners') }}</h2>
                <Icon name="trophy" size="md" class="text-amber-500" />
              </div>
              <div v-if="current.recent_winners.length === 0" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('common.noData') }}</div>
              <div v-else class="mt-4 divide-y divide-gray-100 dark:divide-dark-800">
                <div v-for="winner in current.recent_winners" :key="winner.id" class="flex items-center justify-between gap-3 py-3 first:pt-0 last:pb-0">
                  <div class="min-w-0">
                    <p class="truncate text-sm font-medium text-gray-800 dark:text-gray-200">{{ winner.email }}</p>
                    <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('lottery.round') }} #{{ winner.round_no }} · {{ date(winner.awarded_at) }}</p>
                  </div>
                  <span class="shrink-0 text-sm font-semibold text-emerald-600 dark:text-emerald-400">+{{ money(winner.prize_amount) }}</span>
                </div>
              </div>
            </section>

            <section class="card p-5 sm:p-6">
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('lottery.myWinners') }}</h2>
              <div v-if="current.my_recent_winners.length === 0" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('common.noData') }}</div>
              <div v-else class="mt-4 divide-y divide-gray-100 dark:divide-dark-800">
                <div v-for="winner in current.my_recent_winners" :key="winner.id" class="flex items-center justify-between gap-3 py-3 first:pt-0 last:pb-0">
                  <div>
                    <p class="text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('lottery.round') }} #{{ winner.round_no }}</p>
                    <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ date(winner.awarded_at) }}</p>
                  </div>
                  <div class="text-right"><p class="text-sm font-semibold text-emerald-600 dark:text-emerald-400">+{{ money(winner.prize_amount) }}</p><p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('lottery.awarded') }}</p></div>
                </div>
              </div>
            </section>
          </div>
        </template>

        <section class="card overflow-hidden">
          <div class="flex items-center justify-between border-b border-gray-100 px-5 py-4 dark:border-dark-800 sm:px-6">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('lottery.history') }}</h2>
          </div>
          <div class="overflow-x-auto">
            <table class="w-full min-w-[560px] text-left text-sm">
              <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-900 dark:text-dark-400"><tr><th class="px-5 py-3 font-medium">{{ t('lottery.columns.round') }}</th><th class="px-5 py-3 font-medium">{{ t('lottery.columns.participants') }}</th><th class="px-5 py-3 font-medium">{{ t('lottery.columns.winners') }}</th><th class="px-5 py-3 font-medium">{{ t('lottery.columns.status') }}</th></tr></thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800"><tr v-for="round in rounds" :key="round.id"><td class="px-5 py-3 font-medium text-gray-900 dark:text-white">#{{ round.round_no }}</td><td class="px-5 py-3 text-gray-600 dark:text-dark-300">{{ round.participant_count }} / {{ round.participant_threshold }}</td><td class="px-5 py-3 text-gray-600 dark:text-dark-300">{{ round.winner_count }} / {{ round.prize_count }}</td><td class="px-5 py-3"><span class="badge" :class="round.status === 'open' ? 'badge-warning' : round.status === 'drawn' ? 'badge-success' : 'badge-gray'">{{ statusLabel(round.status) }}</span></td></tr><tr v-if="rounds.length === 0"><td colspan="4" class="px-5 py-10 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('common.noData') }}</td></tr></tbody>
            </table>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import CaptchaChallenge from '@/components/CaptchaChallenge.vue'
import lotteryAPI, { type LotteryCurrent, type LotteryRound } from '@/api/lottery'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const joining = ref(false)
const captchaRef = ref<InstanceType<typeof CaptchaChallenge> | null>(null)
const current = ref<LotteryCurrent | null>(null)
const rounds = ref<LotteryRound[]>([])
const publicSettings = computed(() => appStore.cachedPublicSettings)
const tencentCaptchaEnabled = computed(() => publicSettings.value?.tencent_captcha_enabled === true && Boolean(publicSettings.value.tencent_captcha_app_id))
const aliyunCaptchaEnabled = computed(() => publicSettings.value?.aliyun_captcha_enabled === true && Boolean(publicSettings.value.aliyun_captcha_scene_id) && Boolean(publicSettings.value.aliyun_captcha_prefix))
const sliderCaptchaConfigured = computed(() => tencentCaptchaEnabled.value || aliyunCaptchaEnabled.value)
const progress = computed(() => {
  const round = current.value?.current_round
  return round ? Math.min(100, Math.round((round.participant_count / Math.max(1, round.participant_threshold)) * 100)) : 0
})

function money(value: number): string { return `$${Number(value || 0).toFixed(2)}` }
function date(value: string): string { return new Date(value).toLocaleString() }
function statusLabel(status: string): string { return t(`lottery.statuses.${status}`, status) }

async function load(): Promise<void> {
  loading.value = true
  try {
    const [currentResult, roundsResult] = await Promise.all([lotteryAPI.getCurrent(), lotteryAPI.getRounds(1, 8)])
    current.value = currentResult
    rounds.value = roundsResult.items
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('lottery.loadFailed')))
  } finally { loading.value = false }
}

async function join(): Promise<void> {
  if (!sliderCaptchaConfigured.value) {
    appStore.showError(t('lottery.captchaUnavailable'))
    return
  }
  joining.value = true
  try {
    const proof = await captchaRef.value?.verifyAction()
    if (!proof) return
    await lotteryAPI.join(tencentCaptchaEnabled.value
      ? { tencent_captcha_ticket: proof.token, tencent_captcha_randstr: proof.randstr }
      : { turnstile_token: proof.token })
    appStore.showSuccess(t('lottery.joinSuccess'))
    await load()
    window.dispatchEvent(new Event('lottery-availability-changed'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('lottery.joinFailed')))
  } finally { joining.value = false }
}

function handleCaptchaError(): void {
  appStore.showError(t('lottery.captchaFailed'))
}

onMounted(async () => {
  await Promise.all([appStore.fetchPublicSettings(), load()])
})
</script>
