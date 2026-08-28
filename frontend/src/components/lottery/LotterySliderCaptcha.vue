<template>
  <BaseDialog
    :show="show"
    :title="t('lottery.captchaTitle')"
    width="normal"
    :close-on-escape="!submitting"
    @close="handleClose"
  >
    <div class="flex min-h-[320px] flex-col items-center justify-center">
      <div v-if="loading" class="flex flex-col items-center gap-3 py-12 text-sm text-gray-500 dark:text-dark-400">
        <Icon name="refresh" size="lg" class="animate-spin" />
        <span>{{ t('lottery.captchaLoading') }}</span>
      </div>

      <div v-else-if="loadError" class="flex flex-col items-center gap-4 py-12 text-center">
        <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary" @click="loadChallenge">
          <Icon name="refresh" size="sm" />
          <span>{{ t('lottery.captchaRetry') }}</span>
        </button>
      </div>

      <div v-else class="max-w-full overflow-x-auto pb-1">
        <Slide
          ref="slideRef"
          :config="captchaConfig"
          :data="captchaData"
          :events="captchaEvents"
        />
      </div>

      <div v-if="submitting" class="mt-3 flex items-center gap-2 text-sm text-gray-500 dark:text-dark-400">
        <Icon name="refresh" size="sm" class="animate-spin" />
        <span>{{ t('lottery.captchaSubmitting') }}</span>
      </div>
      <p v-else-if="verifyError" class="mt-3 text-center text-sm text-red-600 dark:text-red-400">
        {{ verifyError }}
      </p>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Slide } from 'go-captcha-vue'
import 'go-captcha-vue/dist/style.css'
import lotteryAPI, { type LotteryCaptchaChallenge, type LotteryJoinResult } from '@/api/lottery'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

interface Props {
  show: boolean
}

interface Emits {
  (event: 'close'): void
  (event: 'joined', result: LotteryJoinResult): void
  (event: 'error', error: unknown): void
}

interface SlidePoint {
  x: number
  y: number
}

interface SlideMethods {
  reset: () => void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()

const slideRef = ref<SlideMethods | null>(null)
const challenge = ref<LotteryCaptchaChallenge | null>(null)
const loading = ref(false)
const submitting = ref(false)
const loadError = ref('')
const verifyError = ref('')

const captchaConfig = computed(() => ({
  width: 300,
  height: 220,
  horizontalPadding: 12,
  verticalPadding: 12,
  showTheme: true,
  title: t('lottery.captchaInstruction'),
  scope: true
}))

const captchaData = computed(() => ({
  thumbX: challenge.value?.thumb_x ?? 0,
  thumbY: challenge.value?.thumb_y ?? 0,
  thumbWidth: challenge.value?.thumb_width ?? 0,
  thumbHeight: challenge.value?.thumb_height ?? 0,
  image: challenge.value?.image ?? '',
  thumb: challenge.value?.thumb ?? ''
}))

const captchaEvents = computed(() => ({
  refresh: loadChallenge,
  close: handleClose,
  confirm: confirm
}))

async function loadChallenge(): Promise<void> {
  if (loading.value || submitting.value) return
  loading.value = true
  loadError.value = ''
  verifyError.value = ''
  challenge.value = null
  try {
    challenge.value = await lotteryAPI.getCaptcha()
  } catch (error) {
    loadError.value = extractApiErrorMessage(error, t('lottery.captchaLoadFailed'))
    emit('error', error)
  } finally {
    loading.value = false
  }
}

async function confirm(point: SlidePoint): Promise<void> {
  if (!challenge.value || submitting.value) return
  submitting.value = true
  verifyError.value = ''
  try {
    const result = await lotteryAPI.join({
      captcha_id: challenge.value.id,
      captcha_x: point.x,
      captcha_y: point.y
    })
    emit('joined', result)
  } catch (error) {
    verifyError.value = extractApiErrorMessage(error, t('lottery.captchaFailed'))
    emit('error', error)
    slideRef.value?.reset()
    challenge.value = null
  } finally {
    submitting.value = false
  }
  if (!challenge.value && props.show) await loadChallenge()
}

function handleClose(): void {
  if (!submitting.value) emit('close')
}

watch(
  () => props.show,
  (show) => {
    if (show) void loadChallenge()
    else {
      challenge.value = null
      loadError.value = ''
      verifyError.value = ''
      slideRef.value?.reset()
    }
  }
)
</script>
