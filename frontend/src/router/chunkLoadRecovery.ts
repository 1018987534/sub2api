const CHUNK_RELOAD_MARKER = '__sub2api_chunk_reload'
const CHUNK_RELOAD_STORAGE_KEY = 'sub2api_chunk_reload_attempt'
const CHUNK_RELOAD_COOLDOWN_MS = 10_000

type StorageLike = Pick<Storage, 'getItem' | 'setItem'>

interface ChunkReloadAttempt {
  target: string
  timestamp: number
}

interface RouterErrorRecoveryOptions {
  endNavigation: () => void
  reload: (url: string) => void
  storage?: StorageLike
  now?: () => number
  origin?: string
}

export type RouterErrorRecoveryResult = 'ended' | 'reloading' | 'throttled'

const getErrorText = (error: unknown): string => {
  if (error instanceof Error) {
    return `${error.name} ${error.message}`
  }
  if (typeof error === 'object' && error !== null) {
    const value = error as { name?: unknown; message?: unknown }
    return `${String(value.name ?? '')} ${String(value.message ?? '')}`
  }
  return String(error ?? '')
}

export const isChunkLoadError = (error: unknown): boolean => {
  const text = getErrorText(error)
  return [
    /ChunkLoadError/i,
    /Failed to fetch dynamically imported module/i,
    /error loading dynamically imported module/i,
    /Loading (?:CSS )?chunk .+ failed/i,
    /Importing a module script failed/i,
    /Failed to load module script/i,
    /Unable to preload CSS/i,
    /module script.+MIME type/i,
    /MIME type.+(?:JavaScript|module)/i,
  ].some((pattern) => pattern.test(text))
}

const normalizeTarget = (targetFullPath: string, origin: string): string => {
  const url = new URL(targetFullPath, origin)
  url.searchParams.delete(CHUNK_RELOAD_MARKER)
  return `${url.pathname}${url.search}${url.hash}`
}

const buildReloadUrl = (targetFullPath: string, timestamp: number, origin: string): string => {
  const url = new URL(targetFullPath, origin)
  url.searchParams.set(CHUNK_RELOAD_MARKER, timestamp.toString())
  return `${url.pathname}${url.search}${url.hash}`
}

const readReloadAttempt = (storage: StorageLike | undefined): ChunkReloadAttempt | null => {
  if (!storage) return null
  try {
    const raw = storage.getItem(CHUNK_RELOAD_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<ChunkReloadAttempt>
    if (typeof parsed.target !== 'string' || typeof parsed.timestamp !== 'number') return null
    return { target: parsed.target, timestamp: parsed.timestamp }
  } catch {
    return null
  }
}

const writeReloadAttempt = (storage: StorageLike | undefined, attempt: ChunkReloadAttempt): void => {
  if (!storage) return
  try {
    storage.setItem(CHUNK_RELOAD_STORAGE_KEY, JSON.stringify(attempt))
  } catch {
    // A blocked sessionStorage must not prevent recovery.
  }
}

export const recoverFromRouterError = (
  error: unknown,
  targetFullPath: string,
  options: RouterErrorRecoveryOptions
): RouterErrorRecoveryResult => {
  options.endNavigation()

  if (!isChunkLoadError(error)) return 'ended'

  const timestamp = (options.now ?? Date.now)()
  const origin = options.origin ?? window.location.origin
  const target = normalizeTarget(targetFullPath, origin)
  const previousAttempt = readReloadAttempt(options.storage)

  if (
    previousAttempt?.target === target &&
    timestamp - previousAttempt.timestamp < CHUNK_RELOAD_COOLDOWN_MS
  ) {
    return 'throttled'
  }

  writeReloadAttempt(options.storage, { target, timestamp })
  options.reload(buildReloadUrl(target, timestamp, origin))
  return 'reloading'
}

export const stripChunkReloadMarker = (currentUrl: string): string | null => {
  const url = new URL(currentUrl)
  if (!url.searchParams.has(CHUNK_RELOAD_MARKER)) return null
  url.searchParams.delete(CHUNK_RELOAD_MARKER)
  return `${url.pathname}${url.search}${url.hash}`
}
