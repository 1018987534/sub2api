<template>
  <AppLayout class="lottery-page" :data-lottery-snapshot-ready="snapshot && !loading && !!current ? 'true' : undefined">
    <div class="mx-auto max-w-5xl space-y-6">
      <div data-lottery-capture-region="true" class="grid items-start gap-4 lg:grid-cols-[1.5fr_1fr]">
        <section class="card overflow-hidden lg:h-[26rem]">
          <div class="border-b border-gray-100 px-6 py-5 dark:border-dark-700">
            <div class="flex items-start justify-between gap-4">
              <div>
                <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('lottery.title') }}</h1>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('lottery.description') }}</p>
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <button type="button" class="btn btn-secondary h-10 px-3" :disabled="loading || refreshing" :title="t('lottery.refresh')" @click="load">
                  <svg class="h-4 w-4" :class="{ 'animate-spin': refreshing }" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.8"><path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h5M20 20v-5h-5" /><path stroke-linecap="round" stroke-linejoin="round" d="M5.6 9A7 7 0 0117.7 6.3L20 9M18.4 15A7 7 0 016.3 17.7L4 15" /></svg>
                  <span class="ml-2 hidden sm:inline">{{ t('lottery.refresh') }}</span>
                </button>
                <div class="flex h-11 w-11 items-center justify-center rounded-lg bg-amber-100 dark:bg-amber-900/30"><Icon name="gift" size="md" class="text-amber-600 dark:text-amber-300" /></div>
              </div>
            </div>
          </div>
          <div v-if="loading" class="flex justify-center py-16"><div class="h-7 w-7 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" /></div>
          <div v-else-if="!current?.enabled || !current.current_round" class="p-6"><div class="rounded-lg border border-gray-200 bg-gray-50 px-4 py-8 text-center dark:border-dark-600 dark:bg-dark-800"><Icon name="gift" size="xl" class="mx-auto text-gray-400" /><p class="mt-3 text-sm text-gray-500 dark:text-gray-400">{{ t('lottery.noRound') }}</p></div></div>
          <div v-else class="space-y-6 p-6">
            <div class="grid gap-3 sm:grid-cols-3">
              <div class="rounded-lg bg-emerald-50 p-4 ring-1 ring-inset ring-emerald-200/60 dark:bg-emerald-500/10 dark:ring-emerald-400/20"><p class="text-xs text-emerald-700 dark:text-emerald-300">{{ t('lottery.prize') }}</p><p class="mt-1 text-2xl font-semibold tabular-nums text-emerald-700 dark:text-emerald-300">{{ money(current.current_round.prize_amount) }}</p></div>
              <div class="rounded-lg bg-amber-50 p-4 ring-1 ring-inset ring-amber-200/60 dark:bg-amber-500/10 dark:ring-amber-400/20"><p class="text-xs text-amber-700 dark:text-amber-300">{{ t('lottery.prizeCount') }}</p><p class="mt-1 text-2xl font-semibold tabular-nums text-amber-700 dark:text-amber-300">{{ current.current_round.prize_count }}</p></div>
              <div class="rounded-lg bg-indigo-50 p-4 ring-1 ring-inset ring-indigo-200/60 dark:bg-indigo-500/10 dark:ring-indigo-400/20"><p class="text-xs text-indigo-700 dark:text-indigo-300">{{ t('lottery.round') }}</p><p class="mt-1 text-2xl font-semibold tabular-nums text-indigo-700 dark:text-indigo-300">#{{ current.current_round.round_no }}</p></div>
            </div>
            <div>
              <div class="mb-2 flex items-center justify-between text-sm"><span class="font-medium text-gray-700 dark:text-gray-200">{{ t('lottery.progress') }}</span><span class="font-semibold tabular-nums text-sky-700 dark:text-sky-300">{{ current.current_round.participant_count }} / {{ current.current_round.participant_threshold }}</span></div>
              <div class="h-3 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"><div class="h-full rounded-full bg-primary-500 transition-all" :style="{ width: `${progress}%` }" /></div>
            </div>
            <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div><p class="text-sm font-medium" :class="current.current_round.status === 'drawn' ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-900 dark:text-white'">{{ participationStatus }}</p><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ participationRule }}</p></div>
              <button v-if="!snapshot" type="button" class="btn btn-primary min-w-32" :disabled="!canJoin || showCaptcha" @click="showCaptcha = true"><Icon name="sparkles" size="sm" class="mr-2" />{{ current.joined ? t('lottery.alreadyJoined') : t('lottery.joinNow') }}</button>
              <router-link v-else to="/login" class="btn btn-primary min-w-32"><Icon name="sparkles" size="sm" class="mr-2" />{{ t('lottery.joinNow') }}</router-link>
            </div>
          </div>
        </section>
        <section class="card overflow-hidden lg:flex lg:h-[26rem] lg:flex-col">
          <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700 lg:shrink-0"><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('lottery.recentWinners') }}</h2></div>
          <div data-lottery-winners-scroll="true" class="p-6 lg:min-h-0 lg:overflow-y-auto lg:px-4 lg:py-3" @scroll.passive="onRecentWinnersScroll">
            <div v-if="!visibleRecentWinners.length" class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('common.noData') }}</div>
            <div v-else class="space-y-3 lg:space-y-1">
              <div v-for="(group, groupIndex) in visibleRecentWinnerGroups" :key="groupIndex" class="lottery-winner-group space-y-3 lg:space-y-1" :class="{ 'lottery-previous-round-frame': group.highlighted }" :data-lottery-previous-round="group.highlighted ? 'true' : undefined">
                <div v-for="winner in group.winners" :key="winner.id" class="lottery-winner-row flex items-center justify-between rounded-lg bg-gray-50 p-3 dark:bg-dark-800 lg:h-9 lg:px-3 lg:py-0">
                  <div class="min-w-0 lg:flex lg:items-center lg:gap-2"><p class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ winner.email }}</p><p class="lottery-round-badge lg:shrink-0 lg:whitespace-nowrap">{{ t('lottery.roundLabel', { round: winner.round_no }) }}</p></div>
                  <p class="ml-3 shrink-0 text-sm font-semibold text-emerald-600 dark:text-emerald-400">+{{ money(winner.prize_amount) }}</p>
                </div>
              </div>
            </div>
          </div>
        </section>
      </div>
      <section class="card overflow-hidden" data-testid="my-winners-section">
        <div class="flex items-center gap-2 border-b border-gray-100 px-6 py-4 dark:border-dark-700"><Icon name="trophy" size="sm" class="text-amber-600 dark:text-amber-300" /><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('lottery.myWinners') }}</h2></div>
        <div v-if="!current?.my_recent_winners.length" class="px-6 py-8 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('common.noData') }}</div>
        <ul v-else class="divide-y divide-gray-100 dark:divide-dark-700">
          <li v-for="winner in current.my_recent_winners" :key="winner.id" class="flex flex-col gap-3 px-6 py-4 sm:flex-row sm:items-center sm:justify-between">
            <div class="min-w-0"><p class="lottery-round-badge">{{ t('lottery.roundLabel', { round: winner.round_no }) }}</p><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('lottery.awardedAt', { time: date(winner.awarded_at) }) }}</p></div>
            <div class="flex shrink-0 items-center justify-between gap-4 sm:block sm:text-right"><p class="text-sm font-semibold text-emerald-600 dark:text-emerald-400">+{{ money(winner.prize_amount) }}</p><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ t('lottery.rewardCredited') }}</p></div>
          </li>
        </ul>
      </section>
      <section class="card overflow-hidden">
        <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700"><h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('lottery.history') }}</h2></div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-100 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-800"><tr><th v-for="column in ['round', 'participants', 'winners', 'status']" :key="column" class="px-6 py-3 text-left text-xs font-medium uppercase text-gray-500">{{ t(`lottery.columns.${column}`) }}</th></tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="round in rounds" :key="round.id">
                <td class="px-6 py-3 text-sm"><span class="lottery-round-badge">#{{ round.round_no }}</span></td>
                <td class="px-6 py-3 text-sm font-medium tabular-nums text-sky-700 dark:text-sky-300">{{ round.participant_count }}</td>
                <td class="px-6 py-3 text-sm font-medium tabular-nums" :class="round.winner_count > 0 ? 'text-amber-700 dark:text-amber-300' : 'text-gray-500 dark:text-gray-400'">{{ round.winner_count }}</td>
                <td class="px-6 py-3 text-sm"><span class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium" :class="statusBadgeClass(round.status)"><span aria-hidden="true" class="h-1.5 w-1.5 rounded-full bg-current" />{{ statusLabel(round.status) }}</span></td>
              </tr>
              <tr v-if="!rounds.length"><td colspan="4" class="px-6 py-8 text-center text-sm text-gray-500">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
    <LotterySliderCaptcha v-if="!snapshot" :show="showCaptcha" @close="showCaptcha = false" @joined="handleJoined" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LotterySliderCaptcha from '@/components/lottery/LotterySliderCaptcha.vue'
import lotteryAPI, { type LotteryCurrent, type LotteryRound, type LotteryWinner } from '@/api/lottery'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ snapshot?: boolean }>()
const { t, locale } = useI18n()
const appStore = useAppStore()
const snapshot = computed(() => props.snapshot === true)
const loading = ref(true)
const refreshing = ref(false)
const showCaptcha = ref(false)
const current = ref<LotteryCurrent | null>(null)
const rounds = ref<LotteryRound[]>([])
const recentWinnersVisibleCount = ref(10)
const recentWinnersBatchSize = 10
const canJoin = computed(() => current.value?.enabled && current.value.current_round?.status === 'open' && !current.value.joined && current.value.eligibility.eligible)
const participationStatus = computed(() => {
  const round = current.value?.current_round
  if (!round) return ''
  if (round.status === 'drawn') return t('lottery.waitNextRound')
  if (round.status === 'cancelled') return t('lottery.statuses.cancelled')
  if (current.value?.joined) return t('lottery.joined')
  return t(round.draw_mode === 'manual' ? 'lottery.manualWaiting' : 'lottery.notJoined')
})
const participationRule = computed(() => {
  const round = current.value?.current_round
  if (!round) return ''
  const rules = []
  if (round.min_recharge_amount > 0) rules.push(t('lottery.minRecharge', { amount: money(round.min_recharge_amount) }))
  else if (round.require_recharge) rules.push(t('lottery.rechargeRequired'))
  if (round.min_account_age_days > 0) rules.push(t('lottery.accountAge', { days: round.min_account_age_days }))
  return rules.length ? t('lottery.participationRule', { rules: rules.join(locale.value.startsWith('zh') ? '，' : ', ') }) : t('lottery.openRule')
})
const progress = computed(() => {
  const round = current.value?.current_round
  return round ? Math.min(100, Math.round((round.participant_count / Math.max(1, round.participant_threshold)) * 100)) : 0
})
const visibleRecentWinners = computed(() => {
  const winners = current.value?.recent_winners ?? []
  return winners.slice(0, recentWinnersVisibleCount.value)
})
const previousRoundNo = computed<number | null>(() => {
  const currentRoundNo = Number(current.value?.current_round?.round_no)
  if (!Number.isSafeInteger(currentRoundNo)) return null
  const previousRoundNos = (current.value?.recent_winners ?? [])
    .map((winner) => Number(winner.round_no))
    .filter((roundNo) => Number.isSafeInteger(roundNo) && roundNo < currentRoundNo)
  return previousRoundNos.length > 0 ? Math.max(...previousRoundNos) : null
})
const visibleRecentWinnerGroups = computed<Array<{ highlighted: boolean; winners: LotteryWinner[] }>>(() => {
  const groups: Array<{ highlighted: boolean; winners: LotteryWinner[] }> = []
  for (const winner of visibleRecentWinners.value) {
    const highlighted = snapshot.value && previousRoundNo.value !== null && Number(winner.round_no) === previousRoundNo.value
    const last = groups[groups.length - 1]
    if (last && last.highlighted === highlighted) {
      last.winners.push(winner)
    } else {
      groups.push({ highlighted, winners: [winner] })
    }
  }
  return groups
})

function money(value: number): string { return `$${Number(value || 0).toFixed(2)}` }
function date(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))
}
function statusLabel(status: string): string { return t(`lottery.statuses.${status}`, status) }
function statusBadgeClass(status: string): string {
  if (status === 'open') return 'bg-sky-50 text-sky-700 dark:bg-sky-400/10 dark:text-sky-300'
  if (status === 'drawn') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
}

function onRecentWinnersScroll(event: Event): void {
  const target = event.currentTarget as HTMLElement
  const nearBottom = target.scrollTop + target.clientHeight >= target.scrollHeight - 32
  if (!nearBottom) return
  const total = current.value?.recent_winners.length ?? 0
  recentWinnersVisibleCount.value = Math.min(total, recentWinnersVisibleCount.value + recentWinnersBatchSize)
}

watch(() => current.value?.recent_winners.length ?? 0, () => {
  recentWinnersVisibleCount.value = recentWinnersBatchSize
})

async function load(): Promise<void> {
  if (refreshing.value) return
  refreshing.value = true
  try {
    if (snapshot.value) {
      const announcement = await lotteryAPI.getAnnouncement()
      current.value = {
        enabled: announcement.enabled,
        current_round: announcement.current_round,
        joined: false,
        eligibility: { eligible: true, total_recharge: 0 },
        recent_winners: announcement.recent_winners,
        my_recent_winners: []
      }
      rounds.value = []
    } else {
      const [currentResult, roundsResult] = await Promise.all([lotteryAPI.getCurrent(), lotteryAPI.getRounds(1, 8)])
      current.value = currentResult
      rounds.value = roundsResult.items
    }
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('lottery.loadFailed')))
  } finally { loading.value = false; refreshing.value = false }
}

async function handleJoined(): Promise<void> {
  showCaptcha.value = false
  appStore.showSuccess(t('lottery.joinSuccess'))
  await load()
  window.dispatchEvent(new Event('lottery-availability-changed'))
}

onMounted(load)
</script>

<style scoped>
.lottery-round-badge {
  @apply inline-flex rounded-md bg-indigo-50 px-2 py-0.5 text-xs font-semibold tabular-nums text-indigo-700 dark:bg-indigo-400/10 dark:text-indigo-300;
}
.lottery-winner-group { position: relative; }
.lottery-previous-round-frame::after {
  position: absolute;
  inset: 0;
  z-index: 1;
  border: 2px solid #ef4444;
  border-radius: 8px;
  content: '';
  pointer-events: none;
}
</style>
