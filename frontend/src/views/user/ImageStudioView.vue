<template>
  <AppLayout>
    <div class="mx-auto flex w-full max-w-[1600px] flex-col gap-5">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <Icon name="sparkles" size="lg" class="text-emerald-600 dark:text-emerald-400" />
            <h2 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('imageStudio.title') }}</h2>
          </div>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('imageStudio.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary self-start sm:self-auto" :disabled="loadingKeys" @click="loadKeys">
          <Icon name="refresh" size="sm" :class="loadingKeys ? 'animate-spin' : ''" />
          <span class="ml-2">{{ t('common.refresh') }}</span>
        </button>
      </div>

      <div class="flex items-start gap-3 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-900 dark:border-emerald-900/60 dark:bg-emerald-950/30 dark:text-emerald-100" data-test="privacy-notice">
        <Icon name="lock" size="md" class="mt-0.5 flex-shrink-0 text-emerald-600 dark:text-emerald-300" />
        <div>
          <p class="font-semibold">{{ t('imageStudio.privacyTitle') }}</p>
          <p class="mt-1 leading-5 text-emerald-800/80 dark:text-emerald-100/80">{{ t('imageStudio.privacyDescription') }}</p>
        </div>
      </div>

      <div class="grid min-h-[680px] gap-5 lg:grid-cols-[340px_minmax(0,1fr)] xl:grid-cols-[380px_minmax(0,1fr)]">
        <section class="h-fit rounded-lg border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800 sm:p-5" data-test="studio-controls">
          <div class="grid grid-cols-2 rounded-lg bg-gray-100 p-1 dark:bg-dark-900" role="tablist" :aria-label="t('imageStudio.mode')">
            <button
              v-for="option in modeOptions"
              :key="option.value"
              type="button"
              role="tab"
              class="flex h-9 items-center justify-center gap-1.5 whitespace-nowrap rounded-md px-1 text-xs font-medium transition-colors sm:gap-2 sm:text-sm"
              :class="form.mode === option.value
                ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white'
                : 'text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'"
              :aria-selected="form.mode === option.value"
              @click="setMode(option.value)"
            >
              <Icon :name="option.value === 'generate' ? 'sparkles' : 'edit'" size="sm" />
              {{ option.label }}
            </button>
          </div>

          <div class="mt-5 space-y-4">
            <div>
              <label class="input-label" for="image-studio-key">{{ t('imageStudio.apiKey') }}</label>
              <select id="image-studio-key" v-model.number="form.apiKeyId" class="input" :disabled="loadingKeys" data-test="api-key-select">
                <option :value="0">{{ loadingKeys ? t('imageStudio.loadingKeys') : t('imageStudio.selectKey') }}</option>
                <option v-for="key in imageKeys" :key="key.id" :value="key.id">
                  {{ key.name }} · {{ key.group?.name }}
                </option>
              </select>
              <div v-if="selectedKey" class="mt-2 flex items-center gap-2 text-xs">
                <span
                  class="h-2 w-2 rounded-full"
                  :class="modelSupported === true ? 'bg-emerald-500' : modelSupported === false ? 'bg-red-500' : 'bg-amber-500'"
                />
                <span :class="modelSupported === false ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400'">
                  {{ modelStatusText }}
                </span>
              </div>
            </div>

            <div>
              <label class="input-label">{{ t('imageStudio.model') }}</label>
              <div class="flex min-h-10 flex-col items-start justify-center gap-1 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 dark:border-dark-600 dark:bg-dark-900 sm:h-10 sm:flex-row sm:items-center sm:py-0">
                <span class="text-sm font-medium text-gray-800 dark:text-gray-100">GPT Image 2</span>
                <span class="rounded-md bg-emerald-100 px-2 py-1 font-mono text-[11px] text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
                  {{ IMAGE_STUDIO_MODEL }}
                </span>
              </div>
            </div>

            <div v-if="form.mode === 'edit'" class="space-y-3">
              <div>
                <label class="input-label">{{ t('imageStudio.sourceImage') }}</label>
                <label
                  class="flex min-h-24 cursor-pointer items-center gap-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 p-3 transition-colors hover:border-emerald-400 dark:border-dark-600 dark:bg-dark-900 dark:hover:border-emerald-600"
                >
                  <img v-if="sourcePreviewURL" :src="sourcePreviewURL" alt="" class="h-16 w-16 rounded-md object-cover" />
                  <Icon v-else name="upload" size="lg" class="text-gray-400" />
                  <span class="min-w-0 text-sm text-gray-600 dark:text-gray-300">
                    <span class="block truncate font-medium text-gray-800 dark:text-gray-100">{{ sourceImage?.name || t('imageStudio.chooseImage') }}</span>
                    <span class="mt-1 block text-xs text-gray-500">PNG / JPEG / WebP · 10 MB</span>
                  </span>
                  <input type="file" accept="image/png,image/jpeg,image/webp" class="hidden" data-test="source-image-input" @change="selectSourceImage" />
                </label>
              </div>

              <div>
                <div class="mb-1 flex items-center justify-between">
                  <label class="input-label mb-0">{{ t('imageStudio.maskImage') }}</label>
                  <button v-if="maskImage" type="button" class="text-xs text-red-600 hover:text-red-700 dark:text-red-400" @click="clearMaskImage">
                    {{ t('imageStudio.removeMask') }}
                  </button>
                </div>
                <label class="flex h-10 cursor-pointer items-center gap-2 rounded-lg border border-gray-200 px-3 text-sm text-gray-600 hover:border-emerald-400 dark:border-dark-600 dark:text-gray-300 dark:hover:border-emerald-600">
                  <Icon name="upload" size="sm" />
                  <span class="min-w-0 flex-1 truncate">{{ maskImage?.name || t('imageStudio.optionalMask') }} · 10 MB</span>
                  <input type="file" accept="image/png,image/jpeg,image/webp" class="hidden" @change="selectMaskImage" />
                </label>
              </div>
            </div>

            <div>
              <label class="input-label" for="image-studio-prompt">{{ t('imageStudio.prompt') }}</label>
              <textarea
                id="image-studio-prompt"
                v-model="form.prompt"
                class="input min-h-[132px] resize-y py-3 leading-6"
                :placeholder="t('imageStudio.promptPlaceholder')"
                maxlength="32000"
                data-test="prompt-input"
                @keydown.meta.enter.prevent="submitGeneration"
                @keydown.ctrl.enter.prevent="submitGeneration"
              />
              <div class="mt-1 text-right text-xs text-gray-400">{{ form.prompt.length }} / 32000</div>
            </div>

            <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
              <div>
                <label class="input-label" for="image-studio-aspect-ratio">{{ t('imageStudio.aspectRatio') }}</label>
                <select id="image-studio-aspect-ratio" v-model="form.orientation" class="input px-2 text-sm" data-test="aspect-ratio-select">
                  <option v-for="option in aspectRatioOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
                </select>
              </div>
              <div>
                <label class="input-label" for="image-studio-resolution">{{ t('imageStudio.resolution') }}</label>
                <select id="image-studio-resolution" v-model="form.resolution" class="input px-2 text-sm" data-test="resolution-select">
                  <option v-for="option in resolutionOptions" :key="option" :value="option">{{ option }}</option>
                </select>
              </div>
              <div>
                <label class="input-label">{{ t('imageStudio.count') }}</label>
                <div class="flex h-10 overflow-hidden rounded-lg border border-gray-300 dark:border-dark-600">
                  <button type="button" class="w-10 text-lg text-gray-600 hover:bg-gray-100 disabled:opacity-40 dark:text-gray-300 dark:hover:bg-dark-700" :disabled="form.count <= 1" :title="t('imageStudio.decrease')" @click="form.count--">-</button>
                  <div class="flex min-w-0 flex-1 items-center justify-center border-x border-gray-300 text-sm font-medium text-gray-900 dark:border-dark-600 dark:text-white">{{ form.count }}</div>
                  <button type="button" class="w-10 text-lg text-gray-600 hover:bg-gray-100 disabled:opacity-40 dark:text-gray-300 dark:hover:bg-dark-700" :disabled="form.count >= 4" :title="t('imageStudio.increase')" @click="form.count++">+</button>
                </div>
              </div>
            </div>

            <div class="grid grid-cols-1 gap-3 sm:grid-cols-3 sm:gap-2">
              <div>
                <label class="input-label" for="image-studio-quality">{{ t('imageStudio.quality') }}</label>
                <select id="image-studio-quality" v-model="form.quality" class="input px-2 text-sm">
                  <option v-for="option in qualityOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
                </select>
              </div>
              <div>
                <label class="input-label" for="image-studio-background">{{ t('imageStudio.background') }}</label>
                <select id="image-studio-background" v-model="form.background" class="input px-2 text-sm">
                  <option v-for="option in backgroundOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
                </select>
              </div>
              <div>
                <label class="input-label" for="image-studio-format">{{ t('imageStudio.format') }}</label>
                <select id="image-studio-format" v-model="form.outputFormat" class="input px-2 text-sm">
                  <option value="png">PNG</option>
                  <option value="jpeg">JPEG</option>
                  <option value="webp">WebP</option>
                </select>
              </div>
            </div>

            <div v-if="form.mode === 'edit'">
              <label class="input-label" for="image-studio-fidelity">{{ t('imageStudio.inputFidelity') }}</label>
              <select id="image-studio-fidelity" v-model="form.inputFidelity" class="input">
                <option value="high">{{ t('imageStudio.fidelityHigh') }}</option>
                <option value="low">{{ t('imageStudio.fidelityLow') }}</option>
              </select>
            </div>

            <button
              type="button"
              class="btn btn-primary flex h-11 w-full items-center justify-center"
              :disabled="!canSubmit"
              data-test="generate-button"
              @click="submitGeneration"
            >
              <Icon :name="submitting ? 'refresh' : 'sparkles'" size="sm" :class="submitting ? 'animate-spin' : ''" />
              <span class="ml-2">{{ submitting ? t('imageStudio.submitting') : t('imageStudio.generate') }}</span>
            </button>
          </div>
        </section>

        <section class="min-w-0" aria-live="polite">
          <div
            class="flex h-full min-h-[680px] w-full flex-col overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800"
            data-test="session-results-panel"
          >
            <div class="flex min-h-16 items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700 sm:px-5">
              <div>
                <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('imageStudio.results') }}</h3>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400" data-test="session-summary">
                  {{ t('imageStudio.sessionCount', { tasks: sessionTaskCount, images: sessionImageCount }) }}
                </p>
              </div>
            </div>

            <div v-if="loadingKeys" class="flex min-h-[614px] flex-1 items-center justify-center">
              <Icon name="refresh" size="lg" class="animate-spin text-emerald-600" />
            </div>

            <div v-else-if="imageKeys.length === 0" class="flex min-h-[614px] flex-1 flex-col items-center justify-center px-6 text-center">
              <Icon name="key" size="xl" class="text-gray-400" />
              <h3 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">{{ t('imageStudio.noImageKey') }}</h3>
              <router-link to="/keys" class="btn btn-primary mt-5">{{ t('imageStudio.manageKeys') }}</router-link>
            </div>

            <div v-else-if="jobs.length === 0" class="flex min-h-[614px] flex-1 flex-col items-center justify-center">
              <Icon name="sparkles" size="xl" class="text-gray-400" />
              <p class="mt-4 text-sm font-medium text-gray-600 dark:text-gray-300">{{ t('imageStudio.noResults') }}</p>
            </div>

            <div v-else class="flex flex-1 flex-col" data-test="job-list">
              <div
                class="grid flex-1 content-start grid-cols-[repeat(auto-fill,minmax(min(150px,100%),1fr))] gap-3 bg-gray-50 p-3 dark:bg-dark-900/50 sm:p-4"
                data-test="session-grid"
              >
                <article
                  v-for="item in sessionItems"
                  :key="item.id"
                  class="group min-w-0 overflow-hidden rounded-md border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800"
                  data-test="session-card"
                >
                  <div class="relative aspect-square overflow-hidden bg-gray-100 dark:bg-dark-900">
                    <button v-if="item.output" type="button" class="block h-full w-full" @click="openPreview(item.output, item.job, item.outputIndex)">
                      <img :src="displayImageURL(item.output.url)" :alt="item.job.prompt" class="h-full w-full object-contain" loading="lazy" />
                    </button>
                    <div v-else-if="item.job.status === 'processing'" class="grid h-full place-items-center p-4">
                      <div class="text-center">
                        <Icon name="sparkles" size="lg" class="mx-auto animate-pulse text-emerald-600 dark:text-emerald-400" />
                        <p class="mt-2 text-xs text-gray-600 dark:text-gray-300">{{ t('imageStudio.processing') }}</p>
                      </div>
                    </div>
                    <div v-else class="grid h-full place-items-center bg-red-50/50 p-4 dark:bg-red-950/10">
                      <div class="text-center">
                        <Icon name="exclamationCircle" size="lg" class="mx-auto text-red-500" />
                        <p class="mt-2 line-clamp-3 text-xs font-medium text-red-700 dark:text-red-300">{{ item.job.error || t('imageStudio.failed') }}</p>
                      </div>
                    </div>

                    <span class="absolute left-2 top-2 flex items-center gap-1 rounded-md bg-white/95 px-1.5 py-1 text-[11px] font-medium text-gray-700 shadow-sm dark:bg-dark-800/95 dark:text-gray-200">
                      <span class="h-1.5 w-1.5 rounded-full" :class="jobStatusDot(item.job.status)" />
                      {{ jobStatusLabel(item.job.status) }}
                    </span>

                    <span
                      v-if="item.output && item.job.requestedCount > 1"
                      class="absolute right-2 top-2 rounded-md bg-gray-900/80 px-1.5 py-1 text-[11px] font-semibold text-white shadow-sm"
                      data-test="output-position"
                    >
                      {{ item.outputIndex + 1 }}/{{ item.job.requestedCount }}
                    </span>

                    <div v-if="item.output" class="absolute bottom-2 right-2 flex gap-1.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
                      <button type="button" class="flex h-8 w-8 items-center justify-center rounded-md bg-white/95 text-gray-700 shadow-md hover:text-emerald-600 dark:bg-dark-800/95 dark:text-gray-200" :title="t('imageStudio.preview')" @click="openPreview(item.output, item.job, item.outputIndex)">
                        <Icon name="eye" size="sm" />
                      </button>
                      <button type="button" class="flex h-8 w-8 items-center justify-center rounded-md bg-white/95 text-gray-700 shadow-md hover:text-emerald-600 dark:bg-dark-800/95 dark:text-gray-200" :title="t('imageStudio.download')" @click="downloadOutput(item.output, item.job, item.outputIndex)">
                        <Icon name="download" size="sm" />
                      </button>
                    </div>
                  </div>

                  <div class="p-2.5">
                    <p class="truncate text-xs font-medium text-gray-800 dark:text-gray-100" :title="item.job.prompt">{{ item.job.prompt }}</p>
                    <div class="mt-1.5 flex items-center justify-between gap-2 text-[11px] text-gray-500 dark:text-gray-400">
                      <span class="min-w-0 truncate">{{ jobSizeLabel(item.job.size) }} · {{ item.job.outputFormat.toUpperCase() }}</span>
                      <span class="flex-shrink-0">{{ formatJobTime(item.job.createdAt) }}</span>
                    </div>
                    <div class="mt-1.5 flex items-center justify-between border-t border-gray-100 pt-1.5 dark:border-dark-700">
                      <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-500 dark:bg-dark-700 dark:text-gray-300">{{ item.job.mode === 'edit' ? t('imageStudio.editMode') : t('imageStudio.generateMode') }}</span>
                      <div class="flex items-center gap-0.5">
                        <button v-if="item.job.canReuse" type="button" class="btn-ghost btn-icon h-7 w-7" :title="t('imageStudio.reuse')" @click="reuseJob(item.job)">
                          <Icon name="edit" size="xs" />
                        </button>
                      </div>
                    </div>
                  </div>
                </article>
              </div>

            </div>
          </div>
        </section>
      </div>
    </div>

    <Teleport to="body">
      <div v-if="preview" class="fixed inset-0 z-[10000] flex items-center justify-center bg-black/80 p-4" role="dialog" aria-modal="true" @click.self="closePreview">
        <div class="relative flex max-h-full max-w-[min(1200px,96vw)] flex-col overflow-hidden rounded-lg bg-white shadow-2xl dark:bg-dark-900">
          <div class="flex items-center justify-between gap-4 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
            <p class="min-w-0 truncate text-sm font-medium text-gray-800 dark:text-gray-100">{{ preview.prompt }}</p>
            <div class="flex flex-shrink-0 gap-1">
              <button type="button" class="btn-ghost btn-icon" :title="t('imageStudio.download')" @click="downloadOutput(preview.output, preview.job, preview.index)">
                <Icon name="download" size="sm" />
              </button>
              <button type="button" class="btn-ghost btn-icon" :title="t('common.close')" @click="closePreview">
                <Icon name="x" size="sm" />
              </button>
            </div>
          </div>
          <div class="min-h-0 overflow-auto bg-gray-100 p-3 dark:bg-black">
            <img :src="displayImageURL(preview.output.url)" :alt="preview.prompt" class="max-h-[82vh] max-w-full object-contain" />
          </div>
        </div>
      </div>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { keysAPI } from '@/api'
import imageStudioAPI, {
  IMAGE_STUDIO_MODEL,
  extractImageStudioOutputs,
  isAsyncImageUnavailable,
  type ImageStudioGenerationPayload,
  type ImageStudioMode,
  type ImageStudioOutput,
  type ImageStudioTask,
} from '@/api/imageStudio'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { sanitizeUrl } from '@/utils/url'
import type { ApiKey } from '@/types'

defineOptions({ name: 'ImageStudioView' })

type StudioJobStatus = 'processing' | 'completed' | 'failed'

interface StudioJob {
  localID: string
  taskID?: string
  keyID: number
  mode: ImageStudioMode
  prompt: string
  size: string
  quality: string
  outputFormat: string
  requestedCount: number
  status: StudioJobStatus
  outputs: ImageStudioOutput[]
  error: string
  createdAt: number
  canReuse: boolean
}

interface PreviewState {
  output: ImageStudioOutput
  job: StudioJob
  index: number
  prompt: string
}

interface SessionImageItem {
  id: string
  job: StudioJob
  output?: ImageStudioOutput
  outputIndex: number
}

type ImageStudioOrientation = 'square' | 'landscape' | 'portrait'
type ImageStudioResolution = '1K' | '2K' | '4K'

interface ImageStudioPreset {
  orientation: ImageStudioOrientation
  resolution: ImageStudioResolution
}

const MAX_UPLOAD_BYTES = 10 * 1024 * 1024
const POLL_INTERVAL_MS = 3000
const DEFAULT_IMAGE_STUDIO_PRESET: ImageStudioPreset = { orientation: 'square', resolution: '2K' }
const IMAGE_STUDIO_SIZE_PRESETS: Record<ImageStudioResolution, Record<ImageStudioOrientation, string>> = {
  '1K': { square: '1024x1024', landscape: '1024x688', portrait: '688x1024' },
  '2K': { square: '2048x2048', landscape: '2048x1360', portrait: '1360x2048' },
  '4K': { square: '2880x2880', landscape: '3840x2160', portrait: '2160x3840' },
}

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

function createDefaultForm() {
  return {
    mode: 'generate' as ImageStudioMode,
    apiKeyId: 0,
    prompt: '',
    orientation: DEFAULT_IMAGE_STUDIO_PRESET.orientation,
    resolution: DEFAULT_IMAGE_STUDIO_PRESET.resolution,
    count: 1,
    quality: 'auto',
    background: 'auto',
    outputFormat: 'png',
    inputFidelity: 'high',
  }
}

const form = reactive(createDefaultForm())

const imageKeys = ref<ApiKey[]>([])
const loadingKeys = ref(false)
const submitting = ref(false)
const modelLoading = ref(false)
const modelSupported = ref<boolean | null>(null)
const modelError = ref('')
const sourceImage = ref<File | null>(null)
const maskImage = ref<File | null>(null)
const sourcePreviewURL = ref('')
const jobs = ref<StudioJob[]>([])
const preview = ref<PreviewState | null>(null)

let modelRequestController: AbortController | null = null
const jobControllers = new Map<string, AbortController>()
let sessionRevision = 0
let loadingOwnerUserID = 0
let loadedOwnerUserID = 0

const selectedKey = computed(() => imageKeys.value.find((key) => key.id === form.apiKeyId) || null)
const canSubmit = computed(() => Boolean(
  !submitting.value &&
  selectedKey.value &&
  modelSupported.value === true &&
  form.prompt.trim() &&
  (form.mode === 'generate' || sourceImage.value),
))
const sessionItems = computed<SessionImageItem[]>(() => jobs.value.flatMap((job) => {
  if (job.status === 'completed' && job.outputs.length > 0) {
    return job.outputs.map((output, outputIndex) => ({
      id: `${job.localID}_${outputIndex}`,
      job,
      output,
      outputIndex,
    }))
  }
  return [{ id: job.localID, job, outputIndex: 0 }]
}))
const sessionTaskCount = computed(() => jobs.value.length)
const sessionImageCount = computed(() => jobs.value.reduce((count, job) => count + job.outputs.length, 0))

const modeOptions = computed(() => [
  { value: 'generate' as const, label: t('imageStudio.generateMode') },
  { value: 'edit' as const, label: t('imageStudio.editMode') },
])
const aspectRatioOptions = computed(() => [
  { value: 'square' as const, label: t('imageStudio.square') },
  { value: 'landscape' as const, label: t('imageStudio.landscape') },
  { value: 'portrait' as const, label: t('imageStudio.portrait') },
])
const resolutionOptions: ImageStudioResolution[] = ['1K', '2K', '4K']
const qualityOptions = computed(() => [
  { value: 'auto', label: t('imageStudio.auto') },
  { value: 'low', label: t('imageStudio.low') },
  { value: 'medium', label: t('imageStudio.medium') },
  { value: 'high', label: t('imageStudio.high') },
])
const backgroundOptions = computed(() => [
  { value: 'auto', label: t('imageStudio.auto') },
  { value: 'opaque', label: t('imageStudio.opaque') },
  { value: 'transparent', label: t('imageStudio.transparent') },
])

const modelStatusText = computed(() => {
  if (modelLoading.value) return t('imageStudio.checkingModel')
  if (modelSupported.value === true) return t('imageStudio.modelReady')
  if (modelSupported.value === false) return modelError.value || t('imageStudio.modelUnavailable')
  return t('imageStudio.selectKeyFirst')
})

function isImageStudioKey(key: ApiKey): boolean {
  return key.status === 'active' &&
    key.group?.status === 'active' &&
    key.group.platform === 'openai' &&
    key.group.allow_image_generation === true
}

function currentUserID(): number {
  return authStore.user?.id || 0
}

function resolveImageStudioSize(preset: ImageStudioPreset): string {
  return IMAGE_STUDIO_SIZE_PRESETS[preset.resolution][preset.orientation]
}

function inferImageStudioPreset(size: string): ImageStudioPreset {
  const normalized = size.trim().toLowerCase()
  for (const resolution of resolutionOptions) {
    for (const orientation of Object.keys(IMAGE_STUDIO_SIZE_PRESETS[resolution]) as ImageStudioOrientation[]) {
      if (IMAGE_STUDIO_SIZE_PRESETS[resolution][orientation].toLowerCase() === normalized) {
        return { resolution, orientation }
      }
    }
  }

  const match = normalized.match(/^(\d+)x(\d+)$/)
  if (!match) return { ...DEFAULT_IMAGE_STUDIO_PRESET }
  const width = Number(match[1])
  const height = Number(match[2])
  const maxEdge = Math.max(width, height)
  const resolution: ImageStudioResolution = maxEdge <= 1024 ? '1K' : maxEdge <= 2048 ? '2K' : '4K'
  const ratio = width / height
  const orientation: ImageStudioOrientation = Math.abs(ratio - 1) < 0.1 ? 'square' : ratio > 1 ? 'landscape' : 'portrait'
  return { resolution, orientation }
}

function jobSizeLabel(size: string): string {
  return `${inferImageStudioPreset(size).resolution} · ${size}`
}

function isCurrentSession(userID: number, revision: number): boolean {
  return userID > 0 && currentUserID() === userID && sessionRevision === revision
}

function abortImageStudioRequests() {
  modelRequestController?.abort()
  modelRequestController = null
  for (const controller of jobControllers.values()) controller.abort()
  jobControllers.clear()
}

function resetImageStudioSession() {
  sessionRevision++
  abortImageStudioRequests()

  if (sourcePreviewURL.value) URL.revokeObjectURL(sourcePreviewURL.value)
  sourcePreviewURL.value = ''
  sourceImage.value = null
  maskImage.value = null
  preview.value = null
  imageKeys.value = []
  jobs.value = []
  Object.assign(form, createDefaultForm())

  loadingKeys.value = false
  submitting.value = false
  modelLoading.value = false
  modelSupported.value = null
  modelError.value = ''
  loadingOwnerUserID = 0
  loadedOwnerUserID = 0
}

function synchronizeImageStudioOwner() {
  const userID = currentUserID()
  if (userID <= 0) {
    resetImageStudioSession()
    return
  }
  if (loadedOwnerUserID === userID || loadingOwnerUserID === userID) return

  resetImageStudioSession()
  void loadKeysForUser(userID, sessionRevision)
}

function loadKeys() {
  const userID = currentUserID()
  if (userID <= 0) {
    resetImageStudioSession()
    return Promise.resolve()
  }
  return loadKeysForUser(userID, sessionRevision)
}

async function loadKeysForUser(userID: number, revision: number) {
  loadingOwnerUserID = userID
  loadingKeys.value = true
  try {
    const keys: ApiKey[] = []
    let page = 1
    let pages = 1
    do {
      const response = await keysAPI.list(page, 100, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
      if (!isCurrentSession(userID, revision)) return
      keys.push(...response.items)
      pages = Math.max(1, response.pages || 1)
      page++
    } while (page <= pages)

    if (!isCurrentSession(userID, revision)) return
    imageKeys.value = keys.filter(isImageStudioKey)
    loadedOwnerUserID = userID
    if (!imageKeys.value.some((key) => key.id === form.apiKeyId)) {
      form.apiKeyId = imageKeys.value[0]?.id || 0
    } else {
      await validateSelectedKey()
    }
  } catch (error) {
    if (isCurrentSession(userID, revision)) {
      appStore.showError(errorMessage(error, t('imageStudio.loadKeysFailed')))
    }
  } finally {
    if (isCurrentSession(userID, revision)) {
      loadingKeys.value = false
      if (loadingOwnerUserID === userID) loadingOwnerUserID = 0
    }
  }
}

function clearSessionOutputs() {
  for (const controller of jobControllers.values()) controller.abort()
  jobControllers.clear()
  jobs.value = []
  closePreview()
}

async function validateSelectedKey() {
  modelRequestController?.abort()
  modelSupported.value = null
  modelError.value = ''
  if (!selectedKey.value) return

  const controller = new AbortController()
  modelRequestController = controller
  modelLoading.value = true
  try {
    const models = await imageStudioAPI.listModels(selectedKey.value.key, controller.signal)
    if (controller.signal.aborted) return
    modelSupported.value = models.includes(IMAGE_STUDIO_MODEL)
    if (!modelSupported.value) modelError.value = t('imageStudio.keyDoesNotSupportModel')
  } catch (error) {
    if (controller.signal.aborted) return
    modelSupported.value = false
    modelError.value = errorMessage(error, t('imageStudio.modelCheckFailed'))
  } finally {
    if (modelRequestController === controller) {
      modelLoading.value = false
      modelRequestController = null
    }
  }
}

function setMode(mode: ImageStudioMode) {
  form.mode = mode
}

function validateUpload(file: File | undefined): file is File {
  if (!file) return false
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) {
    appStore.showError(t('imageStudio.invalidImageType'))
    return false
  }
  if (file.size > MAX_UPLOAD_BYTES) {
    appStore.showError(t('imageStudio.imageTooLarge'))
    return false
  }
  return true
}

function selectSourceImage(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!validateUpload(file)) return
  if (sourcePreviewURL.value) URL.revokeObjectURL(sourcePreviewURL.value)
  sourceImage.value = file
  sourcePreviewURL.value = URL.createObjectURL(file)
}

function selectMaskImage(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!validateUpload(file)) return
  maskImage.value = file
}

function clearMaskImage() {
  maskImage.value = null
}

function buildRequestBody(size: string): ImageStudioGenerationPayload | FormData {
  if (form.mode === 'generate') {
    return {
      model: IMAGE_STUDIO_MODEL,
      prompt: form.prompt.trim(),
      size,
      quality: form.quality,
      background: form.background,
      output_format: form.outputFormat,
      n: form.count,
      response_format: 'b64_json',
    }
  }

  const body = new FormData()
  body.append('model', IMAGE_STUDIO_MODEL)
  body.append('prompt', form.prompt.trim())
  body.append('size', size)
  body.append('quality', form.quality)
  body.append('background', form.background)
  body.append('output_format', form.outputFormat)
  body.append('input_fidelity', form.inputFidelity)
  body.append('n', String(form.count))
  body.append('response_format', 'b64_json')
  if (sourceImage.value) body.append('image', sourceImage.value, sourceImage.value.name)
  if (maskImage.value) body.append('mask', maskImage.value, maskImage.value.name)
  return body
}

async function submitGeneration() {
  const key = selectedKey.value
  if (!key || !canSubmit.value) return
  const ownerUserID = currentUserID()
  const revision = sessionRevision
  if (!isCurrentSession(ownerUserID, revision)) return
  const size = resolveImageStudioSize({ orientation: form.orientation, resolution: form.resolution })
  const requestBody = buildRequestBody(size)

  const job = reactive<StudioJob>({
    localID: `studio_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
    keyID: key.id,
    mode: form.mode,
    prompt: form.prompt.trim(),
    size,
    quality: form.quality,
    outputFormat: form.outputFormat,
    requestedCount: form.count,
    status: 'processing',
    outputs: [],
    error: '',
    createdAt: Date.now(),
    canReuse: true,
  })
  jobs.value.unshift(job)
  submitting.value = true

  const controller = new AbortController()
  jobControllers.set(job.localID, controller)
  try {
    let task: ImageStudioTask
    try {
      task = await imageStudioAPI.submitTask(key.key, form.mode, requestBody, controller.signal)
      if (!isCurrentSession(ownerUserID, revision)) return
    } catch (error) {
      if (!isAsyncImageUnavailable(error)) throw error
      const response = await imageStudioAPI.generateSync(key.key, form.mode, requestBody, controller.signal)
      if (!isCurrentSession(ownerUserID, revision)) return
      completeJob(job, extractImageStudioOutputs(response, form.outputFormat))
      jobControllers.delete(job.localID)
      return
    }

    job.taskID = task.task_id || task.id
    if (task.status === 'completed') {
      completeJob(job, extractImageStudioOutputs(task.result, form.outputFormat))
      jobControllers.delete(job.localID)
      return
    }
    if (task.status === 'failed') {
      failJob(job, task.error?.message || t('imageStudio.failed'))
      jobControllers.delete(job.localID)
      return
    }
    void monitorTask(job, key, controller, ownerUserID, revision)
  } catch (error) {
    if (!controller.signal.aborted) failJob(job, errorMessage(error, t('imageStudio.failed')))
    jobControllers.delete(job.localID)
  } finally {
    submitting.value = false
  }
}

async function monitorTask(job: StudioJob, key: ApiKey, controller: AbortController, ownerUserID = currentUserID(), revision = sessionRevision) {
  try {
    while (!controller.signal.aborted && isCurrentSession(ownerUserID, revision) && job.status === 'processing') {
      await sleep(POLL_INTERVAL_MS, controller.signal)
      if (!job.taskID) throw new Error(t('imageStudio.taskMissing'))
      const task = await imageStudioAPI.getTask(key.key, job.taskID, controller.signal)
      if (!isCurrentSession(ownerUserID, revision)) return
      if (task.status === 'completed') {
        completeJob(job, extractImageStudioOutputs(task.result, job.outputFormat))
        return
      }
      if (task.status === 'failed') {
        failJob(job, task.error?.message || t('imageStudio.failed'))
        return
      }
    }
  } catch (error) {
    if (!controller.signal.aborted) failJob(job, errorMessage(error, t('imageStudio.failed')))
  } finally {
    jobControllers.delete(job.localID)
  }
}

function completeJob(job: StudioJob, outputs: ImageStudioOutput[]) {
  if (!outputs.length) {
    failJob(job, t('imageStudio.emptyResult'))
    return
  }
  job.outputs = outputs
  job.status = 'completed'
  appStore.showSuccess(t('imageStudio.completed', { count: outputs.length }))
}

function failJob(job: StudioJob, message: string) {
  job.status = 'failed'
  job.error = message
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const onAbort = () => {
      window.clearTimeout(timer)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    const timer = window.setTimeout(() => {
      signal.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    signal.addEventListener('abort', onAbort, { once: true })
  })
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message.trim()) return error.message
  if (typeof error === 'object' && error && 'message' in error) return String((error as { message?: unknown }).message || fallback)
  return fallback
}

function displayImageURL(url: string): string {
  return sanitizeUrl(url, { allowRelative: true, allowDataUrl: true })
}

function openPreview(output: ImageStudioOutput, job: StudioJob, index = job.outputs.indexOf(output)) {
  preview.value = { output, job, index: Math.max(0, index), prompt: output.revisedPrompt || job.prompt }
}

function closePreview() {
  preview.value = null
}

function downloadOutput(output: ImageStudioOutput, job: StudioJob, index: number) {
  const url = displayImageURL(output.url)
  if (!url) return
  const link = document.createElement('a')
  link.href = url
  link.download = `image-${new Date(job.createdAt).toISOString().replace(/[:.]/g, '-')}-${index + 1}.${job.outputFormat}`
  link.target = '_blank'
  link.rel = 'noopener noreferrer'
  document.body.appendChild(link)
  link.click()
  link.remove()
}

function reuseJob(job: StudioJob) {
  form.prompt = job.prompt
  const preset = inferImageStudioPreset(job.size)
  form.orientation = preset.orientation
  form.resolution = preset.resolution
  form.quality = job.quality
  form.outputFormat = job.outputFormat
  form.mode = 'generate'
  if (imageKeys.value.some((key) => key.id === job.keyID)) form.apiKeyId = job.keyID
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

function jobStatusLabel(status: StudioJobStatus): string {
  return t(`imageStudio.status.${status}`)
}

function jobStatusDot(status: StudioJobStatus): string {
  if (status === 'completed') return 'bg-emerald-500'
  if (status === 'failed') return 'bg-red-500'
  return 'bg-amber-500 animate-pulse'
}

function formatJobTime(timestamp: number): string {
  return new Intl.DateTimeFormat(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(timestamp)
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === 'Escape') closePreview()
}

watch(
  () => authStore.user?.id || 0,
  () => synchronizeImageStudioOwner(),
  { flush: 'sync' },
)

watch(() => form.apiKeyId, () => {
  void validateSelectedKey()
  clearSessionOutputs()
})

watch(() => form.outputFormat, (format) => {
  if (format !== 'png' && form.background === 'transparent') form.background = 'auto'
})

watch(() => form.background, (background) => {
  if (background === 'transparent' && form.outputFormat === 'jpeg') form.outputFormat = 'png'
})

onMounted(() => {
  document.addEventListener('keydown', handleEscape)
  synchronizeImageStudioOwner()
})

onActivated(synchronizeImageStudioOwner)

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleEscape)
  abortImageStudioRequests()
  if (sourcePreviewURL.value) URL.revokeObjectURL(sourcePreviewURL.value)
})
</script>
