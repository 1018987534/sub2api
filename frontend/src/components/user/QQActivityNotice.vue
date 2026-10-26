<script setup lang="ts">
import { computed } from 'vue'
import type { QQActivityConfig } from '@/api/qqActivity'

const props = defineProps<{ config?: QQActivityConfig }>()
const display = computed(() => props.config?.binding_display)
const statusLabel = computed(() => ({ preparing: '准备中', active: '进行中', paused: '奖励已暂停', ended: '已结束' }[props.config?.status || 'preparing']))
</script>

<template>
  <section v-if="display && (display.group_number || display.activity_title || display.activity_text)" aria-label="群号与活动" class="space-y-3 rounded-xl border border-primary-100 bg-primary-50/60 p-4 dark:border-primary-900/60 dark:bg-primary-950/20">
    <p v-if="display.group_number" class="whitespace-pre-line break-words text-sm text-gray-700 dark:text-dark-200">QQ 群号：<span class="select-all font-semibold text-primary-700 dark:text-primary-300">{{ display.group_number }}</span></p>
    <div v-if="display.activity_title || display.activity_text" class="space-y-2">
      <div class="flex flex-wrap items-center gap-2">
        <h4 class="break-words text-sm font-semibold text-gray-900 dark:text-dark-100">{{ display.activity_title || '当前活动' }}</h4>
        <span class="shrink-0 rounded-full bg-white px-2 py-0.5 text-xs text-gray-600 dark:bg-dark-800 dark:text-dark-300">{{ statusLabel }}</span>
      </div>
      <p v-if="display.activity_text" class="whitespace-pre-line break-words text-sm leading-relaxed text-gray-600 dark:text-dark-300">{{ display.activity_text }}</p>
    </div>
  </section>
</template>
