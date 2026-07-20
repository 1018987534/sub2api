import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { ApiKey, Group } from '@/types'
import { IMAGE_STUDIO_MODEL } from '@/api/imageStudio'
import ImageStudioView from '../ImageStudioView.vue'

const mocks = vi.hoisted(() => ({
  listKeys: vi.fn(),
  listModels: vi.fn(),
  submitTask: vi.fn(),
  getTask: vi.fn(),
  listTasks: vi.fn(),
  deleteTask: vi.fn(),
  generateSync: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api', () => ({
  keysAPI: { list: mocks.listKeys },
}))

vi.mock('@/api/imageStudio', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/imageStudio')>()
  return {
    ...actual,
    default: {
      listModels: mocks.listModels,
      submitTask: mocks.submitTask,
      getTask: mocks.getTask,
      listTasks: mocks.listTasks,
      deleteTask: mocks.deleteTask,
      generateSync: mocks.generateSync,
    },
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: mocks.showError,
    showSuccess: mocks.showSuccess,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => params?.count === undefined ? key : `${key}:${params.count}`,
    }),
  }
})

function makeGroup(overrides: Partial<Group> = {}): Group {
  return {
    id: 12,
    name: 'Image Group',
    description: null,
    platform: 'openai',
    rate_multiplier: 1,
    is_exclusive: false,
    status: 'active',
    subscription_type: 'standard',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    allow_image_generation: true,
    allow_batch_image_generation: false,
    image_rate_independent: true,
    image_rate_multiplier: 1,
    batch_image_discount_multiplier: 0.5,
    batch_image_hold_multiplier: 0.6,
    image_price_1k: 0.1,
    image_price_2k: 0.2,
    image_price_4k: 0.3,
    video_rate_independent: false,
    video_rate_multiplier: 1,
    video_price_480p: null,
    video_price_720p: null,
    video_price_1080p: null,
    web_search_price_per_call: null,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    require_oauth_only: false,
    require_privacy_set: false,
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
    ...overrides,
  }
}

function makeKey(id: number, group: Group): ApiKey {
  return {
    id,
    user_id: 1,
    key: `sk-key-${id}`,
    name: `key-${id}`,
    group_id: group.id,
    group,
    status: 'active',
    ip_whitelist: [],
    ip_blacklist: [],
    last_used_at: null,
    last_used_ip: null,
    quota: 0,
    quota_used: 0,
    expires_at: null,
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
    current_concurrency: 0,
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    window_5h_start: null,
    window_1d_start: null,
    window_7d_start: null,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null,
  }
}

const globalOptions = {
  stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    Icon: { template: '<i />' },
    RouterLink: { template: '<a><slot /></a>' },
    Teleport: true,
  },
}

describe('ImageStudioView', () => {
  beforeEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()

    const imageKey = makeKey(1, makeGroup())
    const textKey = makeKey(2, makeGroup({ id: 22, name: 'Text Group', allow_image_generation: false }))
    const geminiKey = makeKey(3, makeGroup({ id: 33, name: 'Gemini Group', platform: 'gemini' }))
    mocks.listKeys.mockResolvedValue({ items: [textKey, geminiKey, imageKey], total: 3, page: 1, page_size: 100, pages: 1 })
    mocks.listModels.mockResolvedValue([IMAGE_STUDIO_MODEL])
    mocks.listTasks.mockResolvedValue([])
    mocks.deleteTask.mockResolvedValue(undefined)
    mocks.submitTask.mockResolvedValue({ id: 'imgtask_1', task_id: 'imgtask_1', status: 'processing' })
    mocks.getTask.mockResolvedValue({
      id: 'imgtask_1',
      status: 'completed',
      result: { data: [{ b64_json: 'aW1hZ2U=', output_format: 'png' }] },
    })
  })

  it('only lists active OpenAI keys whose group allows image generation', async () => {
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()

    const options = wrapper.find('[data-test="api-key-select"]').findAll('option').map((option) => option.text())
    expect(options).toContain('key-1 · Image Group')
    expect(options.join(' ')).not.toContain('Text Group')
    expect(options.join(' ')).not.toContain('Gemini Group')
    expect(mocks.listModels).toHaveBeenCalledWith('sk-key-1', expect.any(AbortSignal))

    wrapper.unmount()
  })

  it('submits the fixed image model and renders the polled result', async () => {
    vi.useFakeTimers()
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()

    await wrapper.find('[data-test="prompt-input"]').setValue('A lighthouse in a storm')
    await wrapper.find('[data-test="generate-button"]').trigger('click')
    await flushPromises()

    expect(mocks.submitTask).toHaveBeenCalledWith(
      'sk-key-1',
      'generate',
      expect.objectContaining({ model: IMAGE_STUDIO_MODEL, prompt: 'A lighthouse in a storm' }),
      expect.any(AbortSignal),
    )

    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    await wrapper.vm.$nextTick()

    expect(mocks.getTask).toHaveBeenCalledWith('sk-key-1', 'imgtask_1', expect.any(AbortSignal))
    expect(mocks.showSuccess).toHaveBeenCalled()
    const imageSources = wrapper.findAll('img').map((image) => image.attributes('src'))
    expect(imageSources).toContain('data:image/png;base64,aW1hZ2U=')

    wrapper.unmount()
    vi.useRealTimers()
  })

  it('loads completed tasks from the selected key history', async () => {
    mocks.listTasks.mockResolvedValue([{
      id: 'imgtask_history',
      task_id: 'imgtask_history',
      status: 'completed',
      mode: 'generate',
      prompt: 'A saved lighthouse',
      size: '1536x1024',
      quality: 'high',
      output_format: 'png',
      created_at: 1_721_430_000,
      result: { data: [{ url: 'https://cdn.example.com/history.png' }] },
    }])

    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()

    expect(mocks.listTasks).toHaveBeenCalledWith('sk-key-1', 50, expect.any(AbortSignal))
    expect(wrapper.text()).toContain('A saved lighthouse')
    expect(wrapper.text()).toContain('1536x1024')
    expect(wrapper.findAll('img').map((image) => image.attributes('src'))).toContain('https://cdn.example.com/history.png')

    wrapper.unmount()
  })
})
