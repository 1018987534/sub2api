<template>
  <BaseDialog
    :show="show"
    :title="t('admin.lottery.participantsTitle', { round: round?.round_no ?? '-' })"
    width="extra-wide"
    @close="emit('close')"
  >
    <div class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700">
      <DataTable :columns="columns" :data="participants" :loading="loading" row-key="id">
        <template #cell-username="{ value }">
          <span>{{ value || '-' }}</span>
        </template>
        <template #cell-client_ip="{ value }">
          <span class="font-mono text-xs">{{ value || '-' }}</span>
        </template>
        <template #cell-joined_at="{ value }">
          <span>{{ date(value) }}</span>
        </template>
        <template #empty>
          <div class="py-4 text-sm text-gray-500 dark:text-dark-400">
            {{ t('admin.lottery.noParticipants') }}
          </div>
        </template>
      </DataTable>
      <Pagination
        v-if="total > 0"
        :page="page"
        :total="total"
        :page-size="pageSize"
        @update:page="changePage"
        @update:page-size="changePageSize"
      />
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import lotteryAPI, { type LotteryParticipant, type LotteryRound } from '@/api/lottery'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import type { Column } from '@/components/common/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

interface Props {
  show: boolean
  round: LotteryRound | null
}

interface Emits {
  (event: 'close'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()
const appStore = useAppStore()

const participants = ref<LotteryParticipant[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

const columns = computed<Column[]>(() => [
  { key: 'user_id', label: t('admin.lottery.participantColumns.userId') },
  { key: 'username', label: t('admin.lottery.participantColumns.username') },
  { key: 'email', label: t('admin.lottery.participantColumns.email') },
  { key: 'client_ip', label: t('admin.lottery.participantColumns.ip') },
  { key: 'joined_at', label: t('admin.lottery.participantColumns.joinedAt') }
])

function date(value: string): string {
  return value ? new Date(value).toLocaleString() : '-'
}

async function load(): Promise<void> {
  if (!props.show || !props.round) return
  loading.value = true
  try {
    const result = await lotteryAPI.getAdminParticipants(props.round.id, page.value, pageSize.value)
    participants.value = result.items
    total.value = result.total
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.lottery.participantsLoadFailed')))
  } finally {
    loading.value = false
  }
}

function changePage(value: number): void {
  page.value = value
  void load()
}

function changePageSize(value: number): void {
  pageSize.value = value
  page.value = 1
  void load()
}

watch(
  () => [props.show, props.round?.id] as const,
  ([show]) => {
    if (!show) return
    page.value = 1
    participants.value = []
    total.value = 0
    void load()
  }
)
</script>
