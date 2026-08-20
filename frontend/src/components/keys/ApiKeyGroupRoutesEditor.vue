<template>
  <div class="space-y-4" data-testid="api-key-group-routes-editor">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <h4 class="text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('keys.groupRoutesLabel') }}
        </h4>
        <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">
          {{ t('keys.groupRoutesHint') }}
        </p>
      </div>
      <button
        type="button"
        class="btn btn-secondary shrink-0 px-2.5 py-1.5 text-xs"
        :disabled="!canAddGroupRoute"
        :title="t('keys.addBackupGroup')"
        data-testid="add-backup-group"
        @click="addGroupRoute"
      >
        <Icon name="plus" size="sm" class="mr-1" />
        {{ t('keys.addBackupGroup') }}
      </button>
    </div>

    <div class="space-y-3">
      <div
        v-for="(route, index) in displayedRoutes"
        :key="`${index}-${route.group_id ?? 'empty'}`"
        class="rounded-lg border border-gray-200 bg-gray-50/70 p-3 dark:border-dark-600 dark:bg-dark-700/50"
        :data-testid="`group-route-${index}`"
      >
        <div class="mb-2 flex items-center justify-between gap-3">
          <span class="text-xs font-semibold text-gray-600 dark:text-gray-300">
            {{ index === 0 ? t('keys.primaryGroup') : t('keys.backupGroup', { index }) }}
          </span>
          <div v-if="index > 0" class="flex items-center gap-0.5">
            <button
              type="button"
              class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 disabled:opacity-30 dark:hover:bg-dark-600 dark:hover:text-gray-200"
              :disabled="index === 1"
              :title="t('keys.moveBackupUp')"
              @click="moveGroupRoute(index, -1)"
            >
              <Icon name="arrowUp" size="xs" />
            </button>
            <button
              type="button"
              class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 disabled:opacity-30 dark:hover:bg-dark-600 dark:hover:text-gray-200"
              :disabled="index === displayedRoutes.length - 1"
              :title="t('keys.moveBackupDown')"
              @click="moveGroupRoute(index, 1)"
            >
              <Icon name="arrowDown" size="xs" />
            </button>
            <button
              type="button"
              class="rounded p-1 text-gray-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
              :title="t('keys.removeBackupGroup')"
              @click="removeGroupRoute(index)"
            >
              <Icon name="trash" size="xs" />
            </button>
          </div>
        </div>

        <div class="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_9.5rem]">
          <div class="min-w-0">
            <label class="mb-1 block text-[11px] font-medium text-gray-500 dark:text-gray-400">
              {{ t('keys.groupLabel') }}
            </label>
            <Select
              :model-value="route.group_id || null"
              :options="routeOptionsFor(index)"
              :placeholder="t('keys.selectGroup')"
              :searchable="true"
              :search-placeholder="t('keys.searchGroup')"
              :data-testid="`group-route-select-${index}`"
              @update:model-value="updateRouteGroup(index, $event)"
            >
              <template #selected="{ option }">
                <GroupBadge
                  v-if="option"
                  :name="(option as unknown as GroupOption).label"
                  :platform="(option as unknown as GroupOption).platform"
                  :subscription-type="(option as unknown as GroupOption).subscriptionType"
                  :rate-multiplier="(option as unknown as GroupOption).rate"
                  :user-rate-multiplier="(option as unknown as GroupOption).userRate"
                  :peak-rate-enabled="(option as unknown as GroupOption).peakRateEnabled"
                  :peak-start="(option as unknown as GroupOption).peakStart"
                  :peak-end="(option as unknown as GroupOption).peakEnd"
                  :peak-rate-multiplier="(option as unknown as GroupOption).peakRateMultiplier"
                />
                <span v-else class="text-gray-400">{{ t('keys.selectGroup') }}</span>
              </template>
              <template #option="{ option, selected }">
                <GroupOptionItem
                  :name="(option as unknown as GroupOption).label"
                  :platform="(option as unknown as GroupOption).platform"
                  :subscription-type="(option as unknown as GroupOption).subscriptionType"
                  :rate-multiplier="(option as unknown as GroupOption).rate"
                  :user-rate-multiplier="(option as unknown as GroupOption).userRate"
                  :peak-rate-enabled="(option as unknown as GroupOption).peakRateEnabled"
                  :peak-start="(option as unknown as GroupOption).peakStart"
                  :peak-end="(option as unknown as GroupOption).peakEnd"
                  :peak-rate-multiplier="(option as unknown as GroupOption).peakRateMultiplier"
                  :description="(option as unknown as GroupOption).description"
                  :selected="selected"
                />
              </template>
            </Select>
          </div>

          <div>
            <label
              :for="`group-route-cap-${index}`"
              class="mb-1 block text-[11px] font-medium text-gray-500 dark:text-gray-400"
            >
              {{ t('keys.maxAcceptedMultiplier') }}
            </label>
            <div class="relative">
              <input
                :id="`group-route-cap-${index}`"
                :value="route.max_rate_multiplier ?? ''"
                type="number"
                min="0.000001"
                step="0.01"
                class="input h-[42px] pr-7 text-sm"
                :placeholder="t('keys.unlimitedMultiplier')"
                :aria-label="t('keys.maxAcceptedMultiplier')"
                :data-testid="`group-route-cap-${index}`"
                @input="updateRouteCap(index, $event)"
              />
              <span class="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-gray-400">x</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="flex gap-2 rounded-lg bg-blue-50 px-3 py-2.5 text-xs leading-5 text-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
      <Icon name="infoCircle" size="sm" class="mt-0.5 shrink-0" />
      <p>{{ t('keys.maxMultiplierHint') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import type { ApiKeyGroupRoute, Group, GroupPlatform, SubscriptionType } from '@/types'

interface GroupOption extends Record<string, unknown> {
  value: number
  label: string
  description: string | null
  rate: number
  userRate: number | null
  peakRateEnabled: boolean
  peakStart: string
  peakEnd: string
  peakRateMultiplier: number
  subscriptionType: SubscriptionType
  platform: GroupPlatform
}

interface Props {
  groupId: number | null
  routes: ApiKeyGroupRoute[]
  groups: Group[]
  userGroupRates: Record<number, number>
}

const props = defineProps<Props>()
const emit = defineEmits<{
  (event: 'update:groupId', value: number | null): void
  (event: 'update:routes', value: ApiKeyGroupRoute[]): void
}>()
const { t } = useI18n()

const groupOptions = computed<GroupOption[]>(() =>
  props.groups.map((group) => ({
    value: group.id,
    label: group.name,
    description: group.description,
    rate: group.rate_multiplier,
    userRate: props.userGroupRates[group.id] ?? null,
    peakRateEnabled: group.peak_rate_enabled,
    peakStart: group.peak_start,
    peakEnd: group.peak_end,
    peakRateMultiplier: group.peak_rate_multiplier,
    subscriptionType: group.subscription_type,
    platform: group.platform
  }))
)

const displayedRoutes = computed<ApiKeyGroupRoute[]>(() => {
  if (props.routes.length > 0) return props.routes
  return [{ group_id: props.groupId ?? 0, max_rate_multiplier: null }]
})

const routeOptionsFor = (index: number) => {
  const primaryGroupId = displayedRoutes.value[0]?.group_id || props.groupId
  const primaryGroup = props.groups.find((group) => group.id === primaryGroupId)
  const selected = new Set(displayedRoutes.value.map((route) => route.group_id).filter(Boolean))
  return groupOptions.value.filter((option) => {
    if (index === 0) return true
    if (option.value === primaryGroupId) return false
    if (selected.has(option.value) && option.value !== displayedRoutes.value[index]?.group_id) return false
    return !primaryGroup || option.platform === primaryGroup.platform
  })
}

const canAddGroupRoute = computed(() => {
  const primaryGroupId = displayedRoutes.value[0]?.group_id || props.groupId
  return Boolean(primaryGroupId && displayedRoutes.value.length < 10 && routeOptionsFor(displayedRoutes.value.length).length > 0)
})

const emitRoutes = (routes: ApiKeyGroupRoute[]) => {
  emit('update:routes', routes.map((route) => ({ ...route })))
}

const updateRouteGroup = (index: number, value: string | number | boolean | null) => {
  const groupId = typeof value === 'number' ? value : Number(value)
  if (!Number.isInteger(groupId) || groupId <= 0) return

  const routes = displayedRoutes.value.map((route) => ({ ...route }))
  routes[index] = { ...routes[index], group_id: groupId }
  if (index === 0) {
    const primaryPlatform = props.groups.find((group) => group.id === groupId)?.platform
    const compatibleRoutes = routes.slice(1).filter((route) => {
      const group = props.groups.find((item) => item.id === route.group_id)
      return route.group_id !== groupId && (!primaryPlatform || group?.platform === primaryPlatform)
    })
    emit('update:groupId', groupId)
    emitRoutes([routes[0], ...compatibleRoutes])
    return
  }
  emitRoutes(routes)
}

const updateRouteCap = (index: number, event: Event) => {
  const rawValue = (event.target as HTMLInputElement).value
  const routes = displayedRoutes.value.map((route) => ({ ...route }))
  routes[index] = {
    ...routes[index],
    max_rate_multiplier: rawValue === '' ? null : Number(rawValue)
  }
  emitRoutes(routes)
}

const addGroupRoute = () => {
  const option = routeOptionsFor(displayedRoutes.value.length)[0]
  if (!option) return
  emitRoutes([
    ...displayedRoutes.value,
    { group_id: option.value, max_rate_multiplier: null }
  ])
}

const removeGroupRoute = (index: number) => {
  if (index <= 0) return
  emitRoutes(displayedRoutes.value.filter((_, routeIndex) => routeIndex !== index))
}

const moveGroupRoute = (index: number, delta: -1 | 1) => {
  const target = index + delta
  if (index <= 0 || target <= 0 || target >= displayedRoutes.value.length) return
  const routes = displayedRoutes.value.map((route) => ({ ...route }))
  const [route] = routes.splice(index, 1)
  routes.splice(target, 0, route)
  emitRoutes(routes)
}
</script>
