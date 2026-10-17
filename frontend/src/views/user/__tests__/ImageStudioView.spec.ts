import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { ApiKey, Group, User } from '@/types'
import { IMAGE_STUDIO_MODEL } from '@/api/imageStudio'
import { useAuthStore } from '@/stores/auth'
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

vi.mock('@/stores/auth', async () => {
  const { reactive } = await vi.importActual<typeof import('vue')>('vue')
  const authStore = reactive<{ user: User | null }>({ user: { id: 1 } as User })
  return { useAuthStore: () => authStore }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (params?.count !== undefined) return `${key}:${params.count}`
        if (params?.tasks !== undefined && params?.images !== undefined) return `${key}:${params.tasks}:${params.images}`
        return key
      },
    }),
  }
})

function makeGroup(overrides: Partial<Group> = {}): Group {
  return {
    id: 12,
    name: '生图 Image Group',
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

function makeKey(id: number, group: Group, userID = 1): ApiKey {
  return {
    id,
    user_id: userID,
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
    localStorage.clear()
    useAuthStore().user = { id: 1 } as User

    const imageKey = makeKey(1, makeGroup())
    const textKey = makeKey(2, makeGroup({ id: 22, name: 'Text Group', allow_image_generation: false }))
    const mislabeledKey = makeKey(4, makeGroup({ id: 44, name: 'General Group', allow_image_generation: true }))
    const geminiKey = makeKey(3, makeGroup({ id: 33, name: 'Gemini Group', platform: 'gemini' }))
    mocks.listKeys.mockResolvedValue({ items: [textKey, geminiKey, imageKey, mislabeledKey], total: 4, page: 1, page_size: 100, pages: 1 })
    mocks.listModels.mockResolvedValue([IMAGE_STUDIO_MODEL, 'gpt-image-2.5', 'gpt-5.4'])
    mocks.listTasks.mockResolvedValue([])
    mocks.deleteTask.mockResolvedValue(undefined)
    mocks.submitTask.mockResolvedValue({ id: 'imgtask_1', task_id: 'imgtask_1', status: 'processing' })
    mocks.getTask.mockResolvedValue({
      id: 'imgtask_1',
      status: 'completed',
      result: { data: [{ b64_json: 'aW1hZ2U=', output_format: 'png' }] },
    })
  })

  it('only lists image-enabled OpenAI keys from groups whose name contains 生图', async () => {
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()

    const options = wrapper.find('[data-test="api-key-select"]').findAll('option').map((option) => option.text())
    expect(options).toContain('key-1 · 生图 Image Group')
    expect(options.join(' ')).not.toContain('Text Group')
    expect(options.join(' ')).not.toContain('Gemini Group')
    expect(options.join(' ')).not.toContain('General Group')
    expect(mocks.listModels).toHaveBeenCalledWith('sk-key-1', expect.any(AbortSignal))
    expect(wrapper.find('[data-test="image-model-select"]').findAll('option').map((option) => option.text())).toEqual([
      'gpt-image-2', 'gpt-image-2.5',
    ])

    wrapper.unmount()
  })

  it('submits the selected image model and renders the polled result', async () => {
    vi.useFakeTimers()
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()

    await wrapper.find('[data-test="image-model-select"]').setValue('gpt-image-2.5')
    await wrapper.find('[data-test="prompt-input"]').setValue('A lighthouse in a storm')
    await wrapper.find('[data-test="generate-button"]').trigger('click')
    await flushPromises()

    expect(mocks.submitTask).toHaveBeenCalledWith(
      'sk-key-1',
      'generate',
      expect.objectContaining({ model: 'gpt-image-2.5', prompt: 'A lighthouse in a storm', size: '2048x2048' }),
      expect.any(AbortSignal),
    )
    expect(wrapper.get('[data-test="resolution-select"]').element).toHaveProperty('value', '2K')

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

  it('submits the selected image model when editing an image', async () => {
    const originalCreateObjectURL = URL.createObjectURL
    const originalRevokeObjectURL = URL.revokeObjectURL
    const createObjectURL = vi.fn().mockReturnValue('blob:studio-source')
    const revokeObjectURL = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL })
    mocks.submitTask.mockResolvedValue({
      id: 'imgtask_edit',
      task_id: 'imgtask_edit',
      status: 'completed',
      result: { data: [{ b64_json: 'aW1hZ2U=', output_format: 'png' }] },
    })
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()
    await wrapper.find('[data-test="image-model-select"]').setValue('gpt-image-2.5')
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    await wrapper.find('[data-test="prompt-input"]').setValue('Edit the source')

    const sourceInput = wrapper.get('[data-test="source-image-input"]')
    Object.defineProperty(sourceInput.element, 'files', {
      value: [new File(['source'], 'source.png', { type: 'image/png' })],
    })
    await sourceInput.trigger('change')
    await wrapper.find('[data-test="generate-button"]').trigger('click')
    await flushPromises()

    const [key, mode, payload] = mocks.submitTask.mock.calls[0] as [string, string, FormData]
    expect([key, mode]).toEqual(['sk-key-1', 'edit'])
    expect(payload.get('model')).toBe('gpt-image-2.5')
    expect(payload.get('image')).toBeInstanceOf(File)

    wrapper.unmount()
    if (originalCreateObjectURL) {
      Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: originalCreateObjectURL })
    } else {
      Reflect.deleteProperty(URL, 'createObjectURL')
    }
    if (originalRevokeObjectURL) {
      Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: originalRevokeObjectURL })
    } else {
      Reflect.deleteProperty(URL, 'revokeObjectURL')
    }
  })

  it('maps every resolution and orientation to an upstream-valid pixel size', async () => {
    mocks.submitTask.mockResolvedValue({
      id: 'imgtask_done',
      task_id: 'imgtask_done',
      status: 'completed',
      result: { data: [{ b64_json: 'aW1hZ2U=', output_format: 'png' }] },
    })
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()

    const presets = [
      ['1K', 'square', '1024x1024'],
      ['1K', 'landscape', '1024x688'],
      ['1K', 'portrait', '688x1024'],
      ['2K', 'square', '2048x2048'],
      ['2K', 'landscape', '2048x1360'],
      ['2K', 'portrait', '1360x2048'],
      ['4K', 'square', '2880x2880'],
      ['4K', 'landscape', '3840x2160'],
      ['4K', 'portrait', '2160x3840'],
    ] as const

    for (const [resolution, orientation, size] of presets) {
      await wrapper.get('[data-test="resolution-select"]').setValue(resolution)
      await wrapper.get('[data-test="aspect-ratio-select"]').setValue(orientation)
      await wrapper.find('[data-test="prompt-input"]').setValue(`Generate ${resolution} ${orientation}`)
      await wrapper.find('[data-test="generate-button"]').trigger('click')
      await flushPromises()

      expect(mocks.submitTask).toHaveBeenLastCalledWith(
        'sk-key-1',
        'generate',
        expect.objectContaining({ size }),
        expect.any(AbortSignal),
      )
    }
    expect(wrapper.text()).toContain('4K · 3840x2160')

    wrapper.unmount()
  })

  it('does not load server history and advertises one-time browser storage', async () => {
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()

    expect(mocks.listTasks).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="privacy-notice"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="session-results-panel"]').text()).toContain('imageStudio.sessionCount:0:0')

    wrapper.unmount()
  })

  it('renders generated outputs only in the current session', async () => {
    mocks.submitTask.mockResolvedValue({
      id: 'imgtask_session',
      task_id: 'imgtask_session',
      status: 'completed',
      result: {
        data: [
          { b64_json: 'one', output_format: 'png' },
          { b64_json: 'two', output_format: 'png' },
        ],
      },
    })
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()

    await wrapper.find('[data-test="prompt-input"]').setValue('Two concepts')
    await wrapper.find('[data-test="generate-button"]').trigger('click')
    await flushPromises()

    expect(wrapper.findAll('[data-test="session-card"]')).toHaveLength(2)
    expect(wrapper.get('[data-test="session-summary"]').text()).toBe('imageStudio.sessionCount:1:2')
    expect(wrapper.find('[data-test="session-results-panel"]').text()).not.toContain('24')
    expect(mocks.listTasks).not.toHaveBeenCalled()

    wrapper.unmount()
  })

  it('clears current-session images before loading a different user session', async () => {
    const authStore = useAuthStore()
    const oldKey = makeKey(1, makeGroup(), 1)
    const newKey = makeKey(8, makeGroup({ id: 18, name: 'New 生图 Group' }), 8)
    type KeyListResponse = { items: ApiKey[]; total: number; page: number; page_size: number; pages: number }
    let resolveNewKeys!: (value: KeyListResponse) => void
    const newKeys = new Promise<KeyListResponse>((resolve) => { resolveNewKeys = resolve })

    mocks.listKeys.mockImplementation(() => {
      if (authStore.user?.id === 8) return newKeys
      return Promise.resolve({ items: [oldKey], total: 1, page: 1, page_size: 100, pages: 1 })
    })
    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-select"]').text()).toContain('key-1')

    await wrapper.find('[data-test="prompt-input"]').setValue('Old owner private image')
    await wrapper.find('[data-test="generate-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Old owner private image')

    authStore.user = { id: 8 } as User
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).not.toContain('Old owner private image')
    expect(wrapper.find('[data-test="api-key-select"]').text()).not.toContain('key-1')
    expect(wrapper.findAll('[data-test="session-card"]')).toHaveLength(0)

    resolveNewKeys({ items: [newKey], total: 1, page: 1, page_size: 100, pages: 1 })
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-select"]').text()).toContain('key-8 · New 生图 Group')
    expect(mocks.listTasks).not.toHaveBeenCalled()

    wrapper.unmount()
  })

  it('restores the complete form per user when returning to the studio', async () => {
    localStorage.clear()
    const authStore = useAuthStore()
    const userOneKey = makeKey(1, makeGroup(), 1)
    const userTwoKey = makeKey(8, makeGroup({ id: 18, name: '用户二生图' }), 8)
    mocks.listKeys.mockImplementation(() => {
      const userID = authStore.user?.id
      const key = userID === 8 ? userTwoKey : userOneKey
      return Promise.resolve({ items: [key], total: 1, page: 1, page_size: 100, pages: 1 })
    })
    mocks.listModels.mockImplementation((key: string) => Promise.resolve(
      key === 'sk-key-8' ? [IMAGE_STUDIO_MODEL] : [IMAGE_STUDIO_MODEL, 'gpt-image-2.5'],
    ))

    const wrapper = mount(ImageStudioView, { global: globalOptions })
    await flushPromises()
    await wrapper.find('[data-test="image-model-select"]').setValue('gpt-image-2.5')
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    await wrapper.find('[data-test="prompt-input"]').setValue('Remember my image prompt')
    await wrapper.find('[data-test="aspect-ratio-select"]').setValue('portrait')
    await wrapper.find('[data-test="resolution-select"]').setValue('1K')
    await wrapper.find('[data-test="count-increase"]').trigger('click')
    await wrapper.find('[data-test="quality-select"]').setValue('high')
    await wrapper.find('[data-test="background-select"]').setValue('opaque')
    await wrapper.find('[data-test="format-select"]').setValue('webp')
    await wrapper.find('[data-test="fidelity-select"]').setValue('low')
    await flushPromises()

    authStore.user = { id: 8 } as User
    await flushPromises()
    expect(wrapper.find('[data-test="prompt-input"]').element).toHaveProperty('value', '')
    expect(wrapper.find('[data-test="image-model-select"]').element).toHaveProperty('value', IMAGE_STUDIO_MODEL)
    expect(wrapper.find('[data-test="resolution-select"]').element).toHaveProperty('value', '2K')

    authStore.user = { id: 1 } as User
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-select"]').element).toHaveProperty('value', '1')
    expect(wrapper.find('[data-test="image-model-select"]').element).toHaveProperty('value', 'gpt-image-2.5')
    expect(wrapper.findAll('[role="tab"]')[1].attributes('aria-selected')).toBe('true')
    expect(wrapper.find('[data-test="prompt-input"]').element).toHaveProperty('value', 'Remember my image prompt')
    expect(wrapper.find('[data-test="aspect-ratio-select"]').element).toHaveProperty('value', 'portrait')
    expect(wrapper.find('[data-test="resolution-select"]').element).toHaveProperty('value', '1K')
    expect(wrapper.get('[data-test="count-increase"]').element).toHaveProperty('disabled', false)
    expect(wrapper.find('[data-test="quality-select"]').element).toHaveProperty('value', 'high')
    expect(wrapper.find('[data-test="background-select"]').element).toHaveProperty('value', 'opaque')
    expect(wrapper.find('[data-test="format-select"]').element).toHaveProperty('value', 'webp')
    expect(wrapper.find('[data-test="fidelity-select"]').element).toHaveProperty('value', 'low')
    expect(wrapper.get('[data-test="count-value"]').text()).toBe('2')
    expect(localStorage.getItem('image-studio-settings:user:1')).toContain('Remember my image prompt')
    expect(JSON.parse(localStorage.getItem('image-studio-settings:user:8') || '{}')).toMatchObject({ prompt: '' })

    wrapper.unmount()
  })
})
