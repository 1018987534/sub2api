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

      <div class="grid min-h-[680px] gap-5 lg:grid-cols-[340px_minmax(0,1fr)] xl:grid-cols-[380px_minmax(0,1fr)]">
        <section class="h-fit rounded-lg border border-gray-200 bg-white p-4 shadow-sm dark:border-dark-700 dark:bg-dark-800 sm:p-5" data-test="studio-controls">
          <div class="grid grid-cols-2 rounded-lg bg-gray-100 p-1 dark:bg-dark-900" role="tablist" :aria-label="t('imageStudio.mode')">
            <button
              v-for="option in modeOptions"
              :key="option.value"
              type="button"
              role="tab"
              class="flex h-9 items-center justify-center gap-2 rounded-md text-sm font-medium transition-colors"
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
              <div class="flex h-10 items-center justify-between rounded-lg border border-gray-200 bg-gray-50 px-3 dark:border-dark-600 dark:bg-dark-900">
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
                    <span class="mt-1 block text-xs text-gray-500">PNG / JPEG / WebP · 20 MB</span>
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
                  <span class="min-w-0 flex-1 truncate">{{ maskImage?.name || t('imageStudio.optionalMask') }}</span>
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

            <div class="grid grid-cols-2 gap-3">
              <div>
                <label class="input-label" for="image-studio-size">{{ t('imageStudio.size') }}</label>
                <select id="image-studio-size" v-model="form.size" class="input">
                  <option v-for="option in sizeOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
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

            <div class="grid grid-cols-3 gap-2">
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
          <div class="mb-3 flex items-center justify-between gap-3">
            <div>
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('imageStudio.results') }}</h3>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('imageStudio.historyCount', { count: jobs.length }) }}</p>
            </div>
            <button v-if="hasFinishedJobs" type="button" class="btn btn-secondary btn-sm" :disabled="clearingHistory" @click="clearCompletedJobs">
              <Icon :name="clearingHistory ? 'refresh' : 'trash'" size="sm" :class="clearingHistory ? 'animate-spin' : ''" />
              <span class="ml-1.5">{{ t('imageStudio.clearCompleted') }}</span>
            </button>
          </div>

          <div v-if="loadingKeys || loadingHistory" class="flex min-h-[520px] items-center justify-center rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
            <Icon name="refresh" size="lg" class="animate-spin text-emerald-600" />
          </div>

          <div v-else-if="imageKeys.length === 0" class="flex min-h-[520px] flex-col items-center justify-center rounded-lg border border-dashed border-gray-300 bg-white px-6 text-center dark:border-dark-600 dark:bg-dark-800">
            <Icon name="key" size="xl" class="text-gray-400" />
            <h3 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">{{ t('imageStudio.noImageKey') }}</h3>
            <router-link to="/keys" class="btn btn-primary mt-5">{{ t('imageStudio.manageKeys') }}</router-link>
          </div>

          <div v-else-if="jobs.length === 0" class="flex min-h-[520px] flex-col items-center justify-center rounded-lg border border-dashed border-gray-300 bg-white dark:border-dark-600 dark:bg-dark-800">
            <Icon name="sparkles" size="xl" class="text-gray-400" />
            <p class="mt-4 text-sm font-medium text-gray-600 dark:text-gray-300">{{ t('imageStudio.noResults') }}</p>
          </div>

          <div v-else class="grid gap-4 2xl:grid-cols-2" data-test="job-list">
            <article v-for="job in jobs" :key="job.localID" class="overflow-hidden rounded-lg border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800">
              <div class="flex items-start justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
                <div class="min-w-0">
                  <div class="flex items-center gap-2">
                    <span class="h-2 w-2 rounded-full" :class="jobStatusDot(job.status)" />
                    <span class="text-sm font-medium text-gray-900 dark:text-white">{{ jobStatusLabel(job.status) }}</span>
                    <span class="rounded-md bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-500 dark:bg-dark-700 dark:text-gray-300">{{ job.mode === 'edit' ? t('imageStudio.editMode') : t('imageStudio.generateMode') }}</span>
                  </div>
                  <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400" :title="job.prompt">{{ job.prompt }}</p>
                </div>
                <div class="flex flex-shrink-0 items-center gap-1">
                  <button v-if="job.canReuse" type="button" class="btn-ghost btn-icon" :title="t('imageStudio.reuse')" @click="reuseJob(job)">
                    <Icon name="edit" size="sm" />
                  </button>
                  <button v-if="job.status !== 'processing'" type="button" class="btn-ghost btn-icon text-red-600 dark:text-red-400" :title="t('common.delete')" @click="removeJob(job)">
                    <Icon name="trash" size="sm" />
                  </button>
                </div>
              </div>

              <div v-if="job.status === 'processing'" class="grid min-h-64 place-items-center bg-gray-50 p-6 dark:bg-dark-900/50">
                <div class="text-center">
                  <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-lg bg-emerald-100 dark:bg-emerald-900/30">
                    <Icon name="sparkles" size="lg" class="animate-pulse text-emerald-600 dark:text-emerald-400" />
                  </div>
                  <p class="mt-3 text-sm text-gray-600 dark:text-gray-300">{{ t('imageStudio.processing') }}</p>
                </div>
              </div>

              <div v-else-if="job.status === 'failed'" class="grid min-h-64 place-items-center bg-red-50/50 p-6 dark:bg-red-950/10">
                <div class="max-w-md text-center">
                  <Icon name="exclamationCircle" size="lg" class="mx-auto text-red-500" />
                  <p class="mt-3 text-sm font-medium text-red-700 dark:text-red-300">{{ job.error || t('imageStudio.failed') }}</p>
                </div>
              </div>

              <div v-else class="grid gap-px bg-gray-200 dark:bg-dark-700" :class="job.outputs.length > 1 ? 'grid-cols-2' : 'grid-cols-1'">
                <div v-for="(output, outputIndex) in job.outputs" :key="`${job.localID}-${outputIndex}`" class="group relative min-h-64 bg-gray-50 dark:bg-dark-900">
                  <button type="button" class="block h-full w-full" @click="openPreview(output, job)">
                    <img :src="displayImageURL(output.url)" :alt="job.prompt" class="h-full min-h-64 w-full object-contain" loading="lazy" />
                  </button>
                  <div class="absolute bottom-3 right-3 flex gap-2 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
                    <button type="button" class="flex h-9 w-9 items-center justify-center rounded-lg bg-white/95 text-gray-700 shadow-md hover:text-emerald-600 dark:bg-dark-800/95 dark:text-gray-200" :title="t('imageStudio.preview')" @click="openPreview(output, job)">
                      <Icon name="eye" size="sm" />
                    </button>
                    <button type="button" class="flex h-9 w-9 items-center justify-center rounded-lg bg-white/95 text-gray-700 shadow-md hover:text-emerald-600 dark:bg-dark-800/95 dark:text-gray-200" :title="t('imageStudio.download')" @click="downloadOutput(output, job, outputIndex)">
                      <Icon name="download" size="sm" />
                    </button>
                  </div>
                </div>
              </div>

              <div class="flex items-center justify-between px-4 py-2 text-xs text-gray-500 dark:text-gray-400">
                <span>{{ formatJobTime(job.createdAt) }}</span>
                <span>{{ job.size }} · {{ job.quality }} · {{ job.outputFormat.toUpperCase() }}</span>
              </div>
            </article>
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
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
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

const MAX_UPLOAD_BYTES = 20 * 1024 * 1024
const POLL_INTERVAL_MS = 3000

const { t } = useI18n()
const appStore = useAppStore()

const form = reactive({
  mode: 'generate' as ImageStudioMode,
  apiKeyId: 0,
  prompt: '',
  size: '1024x1024',
  count: 1,
  quality: 'auto',
  background: 'auto',
  outputFormat: 'png',
  inputFidelity: 'high',
})

const imageKeys = ref<ApiKey[]>([])
const loadingKeys = ref(false)
const loadingHistory = ref(false)
const clearingHistory = ref(false)
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
let historyRequestController: AbortController | null = null
const jobControllers = new Map<string, AbortController>()

const selectedKey = computed(() => imageKeys.value.find((key) => key.id === form.apiKeyId) || null)
const canSubmit = computed(() => Boolean(
  !submitting.value &&
  selectedKey.value &&
  modelSupported.value === true &&
  form.prompt.trim() &&
  (form.mode === 'generate' || sourceImage.value),
))
const hasFinishedJobs = computed(() => jobs.value.some((job) => job.status !== 'processing'))

const modeOptions = computed(() => [
  { value: 'generate' as const, label: t('imageStudio.generateMode') },
  { value: 'edit' as const, label: t('imageStudio.editMode') },
])
const sizeOptions = computed(() => [
  { value: '1024x1024', label: t('imageStudio.square') },
  { value: '1536x1024', label: t('imageStudio.landscape') },
  { value: '1024x1536', label: t('imageStudio.portrait') },
])
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

async function loadKeys() {
  loadingKeys.value = true
  try {
    const keys: ApiKey[] = []
    let page = 1
    let pages = 1
    do {
      const response = await keysAPI.list(page, 100, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
      keys.push(...response.items)
      pages = Math.max(1, response.pages || 1)
      page++
    } while (page <= pages)

    imageKeys.value = keys.filter(isImageStudioKey)
    if (!imageKeys.value.some((key) => key.id === form.apiKeyId)) {
      form.apiKeyId = imageKeys.value[0]?.id || 0
    } else {
      await Promise.all([validateSelectedKey(), loadHistory()])
    }
  } catch (error) {
    appStore.showError(errorMessage(error, t('imageStudio.loadKeysFailed')))
  } finally {
    loadingKeys.value = false
  }
}

async function loadHistory() {
  historyRequestController?.abort()
  for (const controller of jobControllers.values()) controller.abort()
  jobControllers.clear()

  const key = selectedKey.value
  if (!key) {
    jobs.value = []
    loadingHistory.value = false
    return
  }

  const controller = new AbortController()
  historyRequestController = controller
  loadingHistory.value = true
  try {
    const tasks = await imageStudioAPI.listTasks(key.key, 50, controller.signal)
    if (controller.signal.aborted) return
    jobs.value = tasks.map((task) => historyTaskToJob(task, key.id))
    for (const job of jobs.value) {
      if (job.status !== 'processing' || !job.taskID) continue
      const jobController = new AbortController()
      jobControllers.set(job.localID, jobController)
      void monitorTask(job, key, jobController)
    }
  } catch (error) {
    if (!controller.signal.aborted) {
      jobs.value = []
      appStore.showError(errorMessage(error, t('imageStudio.loadHistoryFailed')))
    }
  } finally {
    if (historyRequestController === controller) {
      loadingHistory.value = false
      historyRequestController = null
    }
  }
}

function historyTaskToJob(task: ImageStudioTask, keyID: number): StudioJob {
  const outputFormat = task.output_format || inferTaskOutputFormat(task) || 'png'
  const status: StudioJobStatus = task.status === 'completed' || task.status === 'failed' ? task.status : 'processing'
  return {
    localID: `history_${task.task_id || task.id}`,
    taskID: task.task_id || task.id,
    keyID,
    mode: task.mode === 'edit' ? 'edit' : 'generate',
    prompt: task.prompt?.trim() || t('imageStudio.apiTask'),
    size: task.size || '1024x1024',
    quality: task.quality || 'auto',
    outputFormat,
    status,
    outputs: status === 'completed' ? extractImageStudioOutputs(task.result, outputFormat) : [],
    error: task.error?.message || '',
    createdAt: task.created_at ? task.created_at * 1000 : Date.now(),
    canReuse: Boolean(task.prompt?.trim()),
  }
}

function inferTaskOutputFormat(task: ImageStudioTask): string {
  const url = task.image_url || task.result?.data?.find((item) => item.url)?.url || ''
  const match = url.match(/\.([a-z0-9]+)(?:\?|$)/i)
  const format = match?.[1]?.toLowerCase()
  return format === 'jpg' ? 'jpeg' : format || ''
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

function buildRequestBody(): ImageStudioGenerationPayload | FormData {
  if (form.mode === 'generate') {
    return {
      model: IMAGE_STUDIO_MODEL,
      prompt: form.prompt.trim(),
      size: form.size,
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
  body.append('size', form.size)
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

  const job = reactive<StudioJob>({
    localID: `studio_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
    keyID: key.id,
    mode: form.mode,
    prompt: form.prompt.trim(),
    size: form.size,
    quality: form.quality,
    outputFormat: form.outputFormat,
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
      task = await imageStudioAPI.submitTask(key.key, form.mode, buildRequestBody(), controller.signal)
    } catch (error) {
      if (!isAsyncImageUnavailable(error)) throw error
      const response = await imageStudioAPI.generateSync(key.key, form.mode, buildRequestBody(), controller.signal)
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
    void monitorTask(job, key, controller)
  } catch (error) {
    if (!controller.signal.aborted) failJob(job, errorMessage(error, t('imageStudio.failed')))
    jobControllers.delete(job.localID)
  } finally {
    submitting.value = false
  }
}

async function monitorTask(job: StudioJob, key: ApiKey, controller: AbortController) {
  try {
    while (!controller.signal.aborted && job.status === 'processing') {
      await sleep(POLL_INTERVAL_MS, controller.signal)
      if (!job.taskID) throw new Error(t('imageStudio.taskMissing'))
      const task = await imageStudioAPI.getTask(key.key, job.taskID, controller.signal)
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
  form.size = job.size
  form.quality = job.quality
  form.outputFormat = job.outputFormat
  form.mode = 'generate'
  if (imageKeys.value.some((key) => key.id === job.keyID)) form.apiKeyId = job.keyID
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

async function removeJob(job: StudioJob) {
  if (job.status === 'processing') return
  if (job.taskID) {
    const key = imageKeys.value.find((item) => item.id === job.keyID)
    if (!key) return
    try {
      await imageStudioAPI.deleteTask(key.key, job.taskID)
    } catch (error) {
      appStore.showError(errorMessage(error, t('imageStudio.deleteHistoryFailed')))
      return
    }
  }
  jobs.value = jobs.value.filter((item) => item.localID !== job.localID)
  if (preview.value?.job.localID === job.localID) closePreview()
}

async function clearCompletedJobs() {
  clearingHistory.value = true
  try {
    await Promise.all(jobs.value.filter((job) => job.status !== 'processing').map((job) => removeJob(job)))
  } finally {
    clearingHistory.value = false
  }
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

watch(() => form.apiKeyId, () => {
  void validateSelectedKey()
  void loadHistory()
})

watch(() => form.outputFormat, (format) => {
  if (format !== 'png' && form.background === 'transparent') form.background = 'auto'
})

watch(() => form.background, (background) => {
  if (background === 'transparent' && form.outputFormat === 'jpeg') form.outputFormat = 'png'
})

onMounted(() => {
  document.addEventListener('keydown', handleEscape)
  void loadKeys()
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleEscape)
  modelRequestController?.abort()
  historyRequestController?.abort()
  for (const controller of jobControllers.values()) controller.abort()
  jobControllers.clear()
  if (sourcePreviewURL.value) URL.revokeObjectURL(sourcePreviewURL.value)
})
</script>
