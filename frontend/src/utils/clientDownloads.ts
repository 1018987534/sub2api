export type ClientPlatform = 'macos' | 'windows' | 'other'
export type ClientArchitecture = 'x64' | 'arm64'
export type DownloadId = 'ccswitch-macos' | 'ccswitch-windows-x64' | 'ccswitch-windows-arm64' | 'chatgpt-macos' | 'chatgpt-windows-x64' | 'chatgpt-windows-arm64'

export interface ClientManifest {
  schema: 1
  updated_at: string
  ccswitch_version: string
  files: Partial<Record<DownloadId, { url: string; name: string; bytes: number; sha256: string; source: string }>>
}

export const OFFICIAL_DOWNLOADS = {
  ccswitch: 'https://github.com/farion1231/cc-switch/releases/latest',
  chatgpt: 'https://chatgpt.com/download/',
  chatgptMac: 'https://persistent.oaistatic.com/codex-app-prod/Codex.dmg',
  chatgptWindows: 'https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi',
  chatgptWindowsX64: 'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix',
  chatgptWindowsArm: 'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-arm64.msix',
} as const

export function detectClientPlatform(userAgent: string, platform = '', touchPoints = 0): ClientPlatform {
  if (/Android|iPhone|iPad|iPod/i.test(userAgent) || (/Mac/i.test(platform) && touchPoints > 1)) return 'other'
  if (/Windows|Win32|Win64/i.test(`${userAgent} ${platform}`)) return 'windows'
  if (/Macintosh|MacIntel|Mac OS X|macOS/i.test(`${userAgent} ${platform}`)) return 'macos'
  return 'other'
}

export function detectClientArchitecture(architecture: string, userAgent = ''): ClientArchitecture {
  return /arm|aarch64/i.test(`${architecture} ${userAgent}`) ? 'arm64' : 'x64'
}

// Only files published by our atomic mirror are accepted as local download links.
export function parseClientManifest(value: unknown): ClientManifest | null {
  if (!value || typeof value !== 'object') return null
  const data = value as Partial<ClientManifest>
  if (data.schema !== 1 || typeof data.updated_at !== 'string' || !Number.isFinite(Date.parse(data.updated_at)) ||
      typeof data.ccswitch_version !== 'string' || !/^v\d+\.\d+\.\d+[\w.-]*$/.test(data.ccswitch_version) ||
      !data.files || typeof data.files !== 'object') return null
  const ids: DownloadId[] = ['ccswitch-macos', 'ccswitch-windows-x64', 'ccswitch-windows-arm64', 'chatgpt-macos', 'chatgpt-windows-x64', 'chatgpt-windows-arm64']
  const files: ClientManifest['files'] = {}
  for (const id of ids) {
    const file = data.files[id]
    if (!file || typeof file !== 'object' || typeof file.url !== 'string') continue
    if (!/^\/downloads\/clients\/releases\/[a-zA-Z0-9._-]+\/[a-zA-Z0-9._-]+$/.test(file.url) || file.url.includes('..') ||
        typeof file.name !== 'string' || !Number.isSafeInteger(file.bytes) || file.bytes <= 0 ||
        typeof file.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(file.sha256) || typeof file.source !== 'string') continue
    files[id] = { ...file }
  }
  return { schema: 1, updated_at: data.updated_at, ccswitch_version: data.ccswitch_version, files }
}

export function resolveClientDownload(client: 'ccswitch' | 'chatgpt', platform: ClientPlatform, architecture: ClientArchitecture, manifest: ClientManifest | null) {
  if (platform === 'other') return { url: OFFICIAL_DOWNLOADS[client], mirrored: false, label: '查看官方下载选项' }
  const id = `${client}-${platform === 'macos' ? 'macos' : `windows-${architecture}`}` as DownloadId
  const file = manifest?.files[id]
  if (file) return { url: '#download', id, mirrored: true, label: platform === 'macos' ? '下载 Mac 安装包' : `下载 Windows ${architecture === 'arm64' ? 'ARM' : ''} 安装包` }
  if (client === 'chatgpt') return {
    url: platform === 'macos' ? OFFICIAL_DOWNLOADS.chatgptMac : OFFICIAL_DOWNLOADS.chatgptWindows,
    mirrored: false,
    label: platform === 'macos' ? '下载 Mac 安装包' : '打开微软官方安装器',
  }
  return { url: OFFICIAL_DOWNLOADS.ccswitch, mirrored: false, label: '前往官方选择安装包' }
}
