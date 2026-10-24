import { describe, expect, it } from 'vitest'
import { detectClientArchitecture, detectClientPlatform, OFFICIAL_DOWNLOADS, parseClientManifest, resolveClientDownload } from '../clientDownloads'

describe('beginner desktop downloads', () => {
  it.each([
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)', 'MacIntel', 0, 'macos'],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'Win32', 0, 'windows'],
    ['Mozilla/5.0 (Windows NT 10.0; ARM64)', 'Win32', 0, 'windows'],
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 18_0)', 'iPhone', 5, 'other'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15)', 'MacIntel', 5, 'other'],
    ['Mozilla/5.0 (Linux; Android 15)', 'Linux armv8l', 5, 'other'],
    ['Mozilla/5.0 (X11; Linux x86_64)', 'Linux x86_64', 0, 'other'],
  ])('detects %s without sending phone users to a desktop installer', (ua, platform, touch, expected) => {
    expect(detectClientPlatform(ua, platform, touch)).toBe(expected)
  })

  it('uses architecture hints and leaves an x64 default when hints are absent', () => {
    expect(detectClientArchitecture('arm')).toBe('arm64')
    expect(detectClientArchitecture('', 'Windows NT 10.0; ARM64')).toBe('arm64')
    expect(detectClientArchitecture('')).toBe('x64')
  })

  const file = { url: '/downloads/clients/releases/20261024T000000Z-0123456789/CC-Switch-v4.0.19-macOS.dmg', name: 'CC-Switch-v4.0.19-macOS.dmg', bytes: 100, sha256: 'a'.repeat(64), source: 'https://github.com/farion1231/cc-switch/releases/download/v4.0.19/CC-Switch-v4.0.19-macOS.dmg' }
  const payload = { schema: 1, updated_at: '2026-10-24T00:00:00Z', ccswitch_version: 'v4.0.19', files: { 'ccswitch-macos': file } }

  it('only uses a real file in the published manifest, otherwise keeping the official fallback', () => {
    const manifest = parseClientManifest(payload)
    expect(resolveClientDownload('ccswitch', 'macos', 'x64', manifest)).toMatchObject({ url: '#download', id: 'ccswitch-macos', mirrored: true })
    expect(resolveClientDownload('ccswitch', 'windows', 'arm64', manifest)).toMatchObject({ url: OFFICIAL_DOWNLOADS.ccswitch, mirrored: false })
    expect(resolveClientDownload('chatgpt', 'windows', 'arm64', manifest)).toMatchObject({ url: OFFICIAL_DOWNLOADS.chatgptWindows, mirrored: false })
    expect(resolveClientDownload('ccswitch', 'other', 'x64', manifest).label).toBe('查看官方下载选项')
  })

  it.each(['https://evil.test/install.exe', '//evil.test/install.exe', '/downloads/clients/releases/../../secret', '/downloads/clients/.mirror.lock'])('rejects manifest link %s', url => {
    const manifest = parseClientManifest({ ...payload, files: { 'ccswitch-macos': { ...file, url } } })
    expect(manifest?.files['ccswitch-macos']).toBeUndefined()
  })

  it('rejects malformed JSON shape and avoids treating Vite HTML fallback as a manifest', () => {
    expect(parseClientManifest('<!doctype html>')).toBeNull()
    expect(parseClientManifest({ ...payload, schema: 2 })).toBeNull()
    expect(parseClientManifest({ ...payload, updated_at: 'yesterday' })).toBeNull()
    expect(parseClientManifest({ ...payload, files: { 'ccswitch-macos': { ...file, sha256: '' } } })?.files).toEqual({})
  })
})
