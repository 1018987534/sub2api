<template>
  <BaseDialog :show="show" :title="t('admin.channelMonitor.order.title')" width="normal" @close="emit('close')">
    <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.channelMonitor.order.description') }}
    </p>

    <div v-if="loading" class="flex min-h-40 items-center justify-center text-sm text-gray-500">
      {{ t('common.loading') }}
    </div>
    <div v-else-if="ordered.length === 0" class="flex min-h-40 items-center justify-center text-sm text-gray-500">
      {{ t('admin.channelMonitor.noMonitorsYet') }}
    </div>
    <ol v-else class="max-h-[52vh] divide-y divide-gray-100 overflow-y-auto border-y border-gray-100 dark:divide-dark-700 dark:border-dark-700">
      <li v-for="(monitor, index) in ordered" :key="monitor.id" class="flex min-h-14 items-center gap-3 py-2.5">
        <span class="w-7 flex-shrink-0 text-center font-mono text-xs text-gray-400">{{ index + 1 }}</span>
        <div class="min-w-0 flex-1">
          <p class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ monitor.name }}</p>
          <p class="truncate text-xs text-gray-500 dark:text-gray-400">
            {{ providerLabel(monitor.provider) }}<template v-if="monitor.group_name"> · {{ monitor.group_name }}</template>
          </p>
        </div>
        <div class="flex h-9 flex-shrink-0 items-center gap-1">
          <button
            type="button"
            class="grid h-8 w-8 place-items-center rounded-md text-gray-500 hover:bg-gray-100 hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-30 dark:hover:bg-dark-700 dark:hover:text-white"
            :disabled="index === 0 || submitting"
            :title="t('admin.channelMonitor.order.moveUp')"
            :aria-label="t('admin.channelMonitor.order.moveUp')"
            data-testid="monitor-order-up"
            @click="move(index, -1)"
          >
            <Icon name="arrowUp" size="sm" />
          </button>
          <button
            type="button"
            class="grid h-8 w-8 place-items-center rounded-md text-gray-500 hover:bg-gray-100 hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-30 dark:hover:bg-dark-700 dark:hover:text-white"
            :disabled="index === ordered.length - 1 || submitting"
            :title="t('admin.channelMonitor.order.moveDown')"
            :aria-label="t('admin.channelMonitor.order.moveDown')"
            data-testid="monitor-order-down"
            @click="move(index, 1)"
          >
            <Icon name="arrowDown" size="sm" />
          </button>
        </div>
      </li>
    </ol>

    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="submitting" @click="emit('close')">
        {{ t('common.cancel') }}
      </button>
      <button type="button" class="btn btn-primary" :disabled="loading || submitting || ordered.length === 0" @click="save">
        {{ submitting ? t('common.saving') : t('common.save') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ChannelMonitor } from '@/api/admin/channelMonitor'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'

const props = defineProps<{
  show: boolean
  items: ChannelMonitor[]
  loading: boolean
  submitting: boolean
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'save', orderedIds: number[]): void
}>()

const { t } = useI18n()
const { providerLabel } = useChannelMonitorFormat()
const ordered = ref<ChannelMonitor[]>([])

watch(
  [() => props.show, () => props.items],
  ([show]) => {
    if (show) ordered.value = [...props.items]
  },
  { immediate: true },
)

function move(index: number, direction: -1 | 1) {
  const target = index + direction
  if (target < 0 || target >= ordered.value.length) return
  const next = [...ordered.value]
  const current = next[index]
  next[index] = next[target]
  next[target] = current
  ordered.value = next
}

function save() {
  emit('save', ordered.value.map((monitor) => monitor.id))
}
</script>
