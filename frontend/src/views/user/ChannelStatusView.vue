<template>
  <ChannelStatusV1View v-if="isV1" />
  <ChannelStatusCardsView v-else-if="isV2" />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { isChannelMonitorV1Mode, isChannelMonitorV2Mode } from '@/utils/featureFlags'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import ChannelStatusCardsView from '@/features/channel-monitor-v2-cards/ChannelStatusCardsView.vue'

const isV1 = computed(() => isChannelMonitorV1Mode())
const isV2 = computed(() => isChannelMonitorV2Mode())
</script>
