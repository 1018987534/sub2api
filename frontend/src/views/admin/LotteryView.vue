<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div><h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.lottery.title') }}</h1><p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('admin.lottery.description') }}</p></div>
        <div class="flex flex-wrap gap-2"><button class="btn btn-secondary" :disabled="loading || roundBusy" :title="t('common.refresh')" @click="load"><Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" /><span>{{ t('common.refresh') }}</span></button><button class="btn btn-primary" :disabled="loading || saving || starting || !form.enabled || !!currentActiveRound" @click="startRound"><Icon name="plus" size="sm" /><span>{{ t('admin.lottery.startRound') }}</span></button></div>
      </div>

      <div v-if="loading" class="card flex justify-center py-16"><div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div></div>
      <template v-else>
        <form class="space-y-6" @submit.prevent="save">
          <section class="card p-5 sm:p-6">
            <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"><div><h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.lottery.enabled') }}</h2><p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('admin.lottery.enabledHint') }}</p></div><label class="inline-flex cursor-pointer items-center gap-3"><input v-model="form.enabled" type="checkbox" class="h-5 w-5 rounded border-gray-300 text-primary-600 focus:ring-primary-500" /><span class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ form.enabled ? t('common.enabled') : t('common.disabled') }}</span></label></div>
          </section>

          <section class="card p-5 sm:p-6"><h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.lottery.rules') }}</h2><div class="mt-5 grid gap-5 md:grid-cols-2 lg:grid-cols-4">
            <label class="space-y-1.5"><span class="label">{{ t('admin.lottery.participantThreshold') }}</span><input v-model.number="form.participant_threshold" type="number" min="2" max="100000" class="input" /></label>
            <label class="space-y-1.5"><span class="label">{{ t('admin.lottery.prizeCount') }}</span><input v-model.number="form.prize_count" type="number" min="1" max="10000" class="input" /></label>
            <label class="space-y-1.5"><span class="label">{{ t('admin.lottery.prizeAmount') }}</span><div class="relative"><input v-model.number="form.prize_amount" type="number" min="0.01" step="0.01" class="input pr-16" /><span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-gray-400">USD</span></div></label>
            <label class="space-y-1.5"><span class="label">{{ t('admin.lottery.drawMode') }}</span><select v-model="form.draw_mode" class="input"><option value="auto">{{ t('admin.lottery.automatic') }}</option><option value="manual">{{ t('admin.lottery.manual') }}</option></select></label>
            <label class="space-y-1.5 md:col-span-2"><span class="label">{{ t('admin.lottery.nextRoundMode') }}</span><select v-model="form.next_round_mode" class="input"><option value="manual">{{ t('admin.lottery.manualNext') }}</option><option value="auto">{{ t('admin.lottery.autoNext') }}</option></select></label>
          </div></section>

          <section class="card p-5 sm:p-6"><h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.lottery.eligibility') }}</h2><div class="mt-5 grid gap-5 md:grid-cols-3"><label class="flex items-start gap-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700"><input v-model="form.require_recharge" type="checkbox" class="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" /><span><span class="block text-sm font-medium text-gray-800 dark:text-gray-200">{{ t('admin.lottery.requireRecharge') }}</span><span class="mt-1 block text-xs text-gray-500 dark:text-dark-400">{{ t('lottery.rechargeRequired') }}</span></span></label><label class="space-y-1.5"><span class="label">{{ t('admin.lottery.minRecharge') }}</span><div class="relative"><input v-model.number="form.min_recharge_amount" type="number" min="0" step="0.01" class="input pr-16" /><span class="pointer-events-none absolute inset-y-0 right-3 flex items-center text-xs text-gray-400">USD</span></div><span class="hint">{{ t('admin.lottery.minRechargeHint') }}</span></label><label class="space-y-1.5"><span class="label">{{ t('admin.lottery.minAccountAge') }}</span><input v-model.number="form.min_account_age_days" type="number" min="0" max="36500" class="input" /><span class="hint">{{ t('admin.lottery.minRechargeHint') }}</span></label><label class="space-y-1.5 md:col-span-2"><span class="label">{{ t('admin.lottery.recentRecharge') }}</span><input v-model.number="form.recent_recharge_days" type="number" min="0" max="36500" class="input" /><span class="hint">{{ t('admin.lottery.recentRechargeHint') }}</span></label></div></section>
          <div class="flex justify-end"><button class="btn btn-primary w-full sm:w-auto" type="submit" :disabled="saving"><Icon v-if="saving" name="refresh" size="sm" class="animate-spin" /><Icon v-else name="checkCircle" size="sm" /><span>{{ saving ? t('common.saving') : t('admin.lottery.save') }}</span></button></div>
        </form>

        <section v-if="currentActiveRound" class="flex flex-col gap-4 border-b border-gray-200 py-4 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
          <div class="flex items-center gap-3"><h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.lottery.currentRound') }} #{{ currentActiveRound.round_no }}</h2><span class="badge badge-warning">{{ statusLabel(currentActiveRound.status) }}</span></div>
          <div class="flex flex-wrap items-end gap-3">
            <label class="space-y-1.5">
              <span class="label">{{ t('admin.lottery.prizeCount') }}</span>
              <input v-model.number="prizeCountDrafts[currentActiveRound.id]" type="number" min="1" :max="Math.min(currentActiveRound.participant_threshold, 10000)" class="input w-28" :aria-label="t('admin.lottery.currentPrizeCount')" :disabled="roundBusy" />
            </label>
            <button type="button" class="btn btn-secondary" :disabled="roundBusy || prizeCountDrafts[currentActiveRound.id] === currentActiveRound.prize_count" @click="updatePrizeCount(currentActiveRound)">
              <Icon :name="updatingPrizeId ? 'refresh' : 'checkCircle'" size="sm" :class="updatingPrizeId ? 'animate-spin' : ''" />
              <span>{{ t('admin.lottery.updatePrizeCount') }}</span>
            </button>
            <button v-if="currentActiveRound.status === 'open'" type="button" class="btn btn-secondary" :disabled="roundBusy" @click="requestStatusChange('paused')"><Icon name="clock" size="sm" /><span>{{ t('admin.lottery.pauseRound') }}</span></button>
            <button v-else type="button" class="btn btn-secondary" :disabled="roundBusy || !form.enabled" @click="updateStatus('open')"><Icon name="play" size="sm" /><span>{{ t('admin.lottery.resumeRound') }}</span></button>
            <button type="button" class="btn btn-danger" :disabled="roundBusy" @click="requestStatusChange('cancelled')"><Icon name="ban" size="sm" /><span>{{ t('admin.lottery.cancelRound') }}</span></button>
          </div>
        </section>

        <section class="overflow-hidden border-t border-gray-200 dark:border-dark-700">
          <div class="px-1 py-4"><h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.lottery.rounds') }}</h2><p class="mt-1 text-xs text-gray-500 dark:text-dark-400">{{ currentActiveRound ? `#${currentActiveRound.round_no}` : t('admin.lottery.noOpenRound') }}</p></div>
          <div class="overflow-x-auto">
            <table class="w-full min-w-[920px] text-left text-sm">
              <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-900 dark:text-dark-400"><tr><th v-for="column in ['round', 'progress', 'real', 'manualProgress', 'prizes', 'status', 'actions']" :key="column" class="px-5 py-3 font-medium">{{ t(`admin.lottery.columns.${column}`) }}</th></tr></thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                <tr v-for="round in rounds" :key="round.id">
                  <td class="px-5 py-3 font-medium text-gray-900 dark:text-white">#{{ round.round_no }}</td>
                  <td class="px-5 py-3 text-gray-600 dark:text-dark-300"><div v-if="round.status === 'open'" class="flex w-44 items-center gap-2"><input v-model.number="progressDrafts[round.id]" type="number" :min="round.real_participant_count || 0" :max="round.participant_threshold" :disabled="roundBusy" class="input h-9 w-24" :aria-label="t('admin.lottery.progressInput')" /><span class="shrink-0 text-xs">/ {{ round.participant_threshold }}</span></div><span v-else>{{ round.participant_count }} / {{ round.participant_threshold }}</span></td>
                  <td class="px-5 py-3 text-gray-600 dark:text-dark-300"><button type="button" class="inline-flex items-center gap-1.5 text-primary-600 hover:text-primary-700 hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/30 dark:text-primary-400" :aria-label="t('admin.lottery.viewParticipants', { count: round.real_participant_count || 0 })" @click="openParticipants(round)"><Icon name="eye" size="sm" /><span>{{ round.real_participant_count || 0 }}</span></button></td>
                  <td class="px-5 py-3 text-gray-600 dark:text-dark-300">{{ round.manual_progress_count || 0 }}</td>
                  <td class="px-5 py-3 text-gray-600 dark:text-dark-300">{{ round.prize_count }}</td>
                  <td class="px-5 py-3"><span class="badge" :class="round.status === 'open' || round.status === 'paused' ? 'badge-warning' : round.status === 'drawn' ? 'badge-success' : 'badge-gray'">{{ statusLabel(round.status) }}</span></td>
                  <td class="px-5 py-3"><div v-if="round.status === 'open'" class="flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" :disabled="roundBusy" @click="updateProgress(round)"><Icon v-if="updatingId === round.id" name="refresh" size="sm" class="animate-spin" /><Icon v-else name="checkCircle" size="sm" /><span>{{ t('admin.lottery.updateProgress') }}</span></button><button class="btn btn-secondary btn-sm" :disabled="roundBusy" @click="draw(round)"><Icon v-if="drawingId === round.id" name="refresh" size="sm" class="animate-spin" /><Icon v-else name="trophy" size="sm" /><span>{{ t('admin.lottery.draw') }}</span></button></div><span v-else class="text-xs text-gray-400">{{ round.drawn_at ? date(round.drawn_at) : '-' }}</span></td>
                </tr>
                <tr v-if="rounds.length === 0"><td colspan="7" class="px-5 py-10 text-center text-sm text-gray-500 dark:text-dark-400">{{ t('common.noData') }}</td></tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </div>
    <LotteryParticipantsDialog
      :show="selectedRound !== null"
      :round="selectedRound"
      @close="selectedRound = null"
    />
    <ConfirmDialog :show="pendingStatus !== null" :title="t(pendingStatus === 'paused' ? 'admin.lottery.pauseRound' : 'admin.lottery.cancelRound')" :message="t(pendingStatus === 'paused' ? 'admin.lottery.pauseRoundConfirm' : 'admin.lottery.cancelRoundConfirm')" :danger="pendingStatus === 'cancelled'" @confirm="confirmStatusChange" @cancel="pendingStatus = null" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LotteryParticipantsDialog from '@/components/lottery/LotteryParticipantsDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import lotteryAPI, { type LotteryConfig, type LotteryRound } from '@/api/lottery'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const starting = ref(false)
const drawingId = ref<number | null>(null)
const updatingId = ref<number | null>(null)
const updatingPrizeId = ref<number | null>(null)
const updatingStatus = ref(false)
const pendingStatus = ref<'paused' | 'cancelled' | null>(null)
const selectedRound = ref<LotteryRound | null>(null)
const rounds = ref<LotteryRound[]>([])
const progressDrafts = reactive<Record<number, number>>({})
const prizeCountDrafts = reactive<Record<number, number>>({})
const form = reactive<LotteryConfig>({ enabled: false, participant_threshold: 50, prize_count: 1, prize_amount: 5, draw_mode: 'auto', next_round_mode: 'manual', require_recharge: true, min_recharge_amount: 0, min_account_age_days: 0, recent_recharge_days: 0 })
const currentActiveRound = computed(() => rounds.value.find((round) => round.status === 'open' || round.status === 'paused'))
const roundBusy = computed(() => drawingId.value !== null || updatingId.value !== null || updatingPrizeId.value !== null || updatingStatus.value)

function date(value: string): string { return new Date(value).toLocaleString() }
function statusLabel(status: string): string { return t(`lottery.statuses.${status}`, status) }
function openParticipants(round: LotteryRound): void { selectedRound.value = round }

async function load(): Promise<void> {
  loading.value = true
  try {
    const [config, page] = await Promise.all([lotteryAPI.getAdminConfig(), lotteryAPI.getAdminRounds(1, 50)])
    Object.assign(form, config)
    rounds.value = page.items
    for (const round of rounds.value) {
      progressDrafts[round.id] = round.participant_count
      prizeCountDrafts[round.id] = round.prize_count
    }
  } catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.lottery.loadFailed'))) }
  finally { loading.value = false }
}
async function save(): Promise<void> {
  saving.value = true
  try { const updated = await lotteryAPI.updateAdminConfig({ ...form }); Object.assign(form, updated); await appStore.fetchPublicSettings(true); appStore.showSuccess(t('admin.lottery.saved')) }
  catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.lottery.saveFailed'))) }
  finally { saving.value = false }
}
async function startRound(): Promise<void> {
  if (starting.value) return
  starting.value = true
  try { const round = await lotteryAPI.startRound(); progressDrafts[round.id] = round.participant_count; prizeCountDrafts[round.id] = round.prize_count; rounds.value = [round, ...rounds.value.filter((item) => item.id !== round.id)]; appStore.showSuccess(t('admin.lottery.roundStarted')) }
  catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.lottery.loadFailed'))) }
  finally { starting.value = false }
}
async function updateProgress(round: LotteryRound): Promise<void> {
  if (roundBusy.value) return
  updatingId.value = round.id
  try {
    const updated = await lotteryAPI.updateRoundProgress(round.id, Number(progressDrafts[round.id]))
    const index = rounds.value.findIndex((item) => item.id === round.id)
    if (index >= 0) rounds.value[index] = updated
    progressDrafts[round.id] = updated.participant_count
    appStore.showSuccess(t('admin.lottery.progressUpdated'))
  } catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.lottery.progressUpdateFailed'))) }
  finally { updatingId.value = null }
}
async function updatePrizeCount(round: LotteryRound): Promise<void> {
  if (roundBusy.value) return
  const prizeCount = Number(prizeCountDrafts[round.id])
  if (!Number.isInteger(prizeCount) || prizeCount < 1 || prizeCount > Math.min(round.participant_threshold, 10000)) {
    appStore.showError(t('admin.lottery.prizeCountInvalid', { max: Math.min(round.participant_threshold, 10000) }))
    return
  }
  updatingPrizeId.value = round.id
  try {
    const updated = await lotteryAPI.updateRoundPrizeCount(round.id, prizeCount)
    const index = rounds.value.findIndex((item) => item.id === round.id)
    if (index >= 0) rounds.value[index] = updated
    prizeCountDrafts[round.id] = updated.prize_count
    window.dispatchEvent(new Event('lottery-availability-changed'))
    appStore.showSuccess(t('admin.lottery.prizeCountUpdated'))
  } catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.lottery.prizeCountUpdateFailed'))) }
  finally { updatingPrizeId.value = null }
}
async function draw(round: LotteryRound): Promise<void> {
  if (roundBusy.value) return
  drawingId.value = round.id
  try { const result = await lotteryAPI.drawRound(round.id); const index = rounds.value.findIndex((item) => item.id === round.id); if (index >= 0) rounds.value[index] = result.round; if (result.next_round) { prizeCountDrafts[result.next_round.id] = result.next_round.prize_count; progressDrafts[result.next_round.id] = result.next_round.participant_count; rounds.value.unshift(result.next_round) }; appStore.showSuccess(t('admin.lottery.drawn')) }
  catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.lottery.loadFailed'))) }
  finally { drawingId.value = null }
}
function requestStatusChange(status: 'paused' | 'cancelled'): void { pendingStatus.value = status }
async function confirmStatusChange(): Promise<void> {
  const status = pendingStatus.value
  pendingStatus.value = null
  if (status) await updateStatus(status)
}
async function updateStatus(status: 'open' | 'paused' | 'cancelled'): Promise<void> {
  const round = currentActiveRound.value
  if (!round || roundBusy.value) return
  updatingStatus.value = true
  try {
    const updated = await lotteryAPI.updateRoundStatus(round.id, status)
    const index = rounds.value.findIndex(item => item.id === round.id)
    if (index >= 0) rounds.value[index] = updated
    progressDrafts[round.id] = updated.participant_count
    prizeCountDrafts[round.id] = updated.prize_count
    window.dispatchEvent(new Event('lottery-availability-changed'))
    appStore.showSuccess(t(status === 'open' ? 'admin.lottery.roundResumed' : status === 'paused' ? 'admin.lottery.roundPaused' : 'admin.lottery.roundCancelled'))
  } catch (error) { appStore.showError(extractApiErrorMessage(error, t('admin.lottery.roundStatusUpdateFailed'))) }
  finally { updatingStatus.value = false }
}
onMounted(load)
</script>

<style scoped>
.label { @apply block text-sm font-medium text-gray-700 dark:text-gray-200; }
.hint { @apply block text-xs text-gray-500 dark:text-dark-400; }
</style>
