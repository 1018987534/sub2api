<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { detectClientArchitecture, detectClientPlatform, OFFICIAL_DOWNLOADS, parseClientManifest, resolveClientDownload, type ClientArchitecture, type ClientManifest, type ClientPlatform } from '@/utils/clientDownloads'

withDefaults(defineProps<{ siteName?: string; inheritTheme?: boolean }>(), { siteName: '小红豆', inheritTheme: false })
const platform = ref<ClientPlatform>('other')
const architecture = ref<ClientArchitecture>('x64')
const detected = ref<ClientPlatform>('other')
const manifest = ref<ClientManifest | null>(null)
const imagePreview = ref<{ src: string; alt: string } | null>(null)
const abort = new AbortController()
const ccDownload = computed(() => resolveClientDownload('ccswitch', platform.value, architecture.value, manifest.value))
const chatDownload = computed(() => resolveClientDownload('chatgpt', platform.value, architecture.value, manifest.value))
const faqs = [
  ['点了导入，没有反应？', '先打开一次 CC Switch，再回本站点击「导入到 CC Switch」，并允许浏览器打开应用。没有导入按钮时，使用密钥旁的「使用密钥」查看配置说明。'],
  ['提示密钥无效、余额不足或模型不存在？', '先检查账户余额、密钥是否启用，以及分组支持的模型。更换密钥后重新导入；切换供应商后完全退出并重开客户端。'],
  ['Windows 的 .msix 打不开？', '改用上面的「微软商店安装」。MSIX 由 Windows 应用安装程序处理；有依赖或系统版本提示时，按微软说明处理。ARM 电脑请选择 ARM 安装包。'],
  ['在本站充值，会变成 ChatGPT Plus 吗？', '本站额度用于中转 API，和 ChatGPT 账号的 Plus / Pro 订阅分别管理。请进入客户端的 Codex，并使用刚导入的供应商配置。'],
]
function closePreview(event: KeyboardEvent) { if (event.key === 'Escape') imagePreview.value = null }
onMounted(async () => {
  detected.value = detectClientPlatform(navigator.userAgent, navigator.platform, navigator.maxTouchPoints)
  platform.value = detected.value
  architecture.value = detected.value === 'windows' ? detectClientArchitecture('', navigator.userAgent) : 'x64'
  const hints = navigator as Navigator & { userAgentData?: { getHighEntropyValues: (hints: string[]) => Promise<{ architecture?: string }> } }
  if (detected.value === 'windows') void hints.userAgentData?.getHighEntropyValues(['architecture']).then(data => { architecture.value = detectClientArchitecture(data.architecture || '', navigator.userAgent) }).catch(() => {})
  document.addEventListener('keydown', closePreview)
  try {
    const response = await fetch('/downloads/clients/manifest.json', { signal: abort.signal, cache: 'no-cache' })
    if (response.ok) manifest.value = parseClientManifest(await response.json())
  } catch { /* Before mirror deployment, official downloads remain available. */ }
})
onBeforeUnmount(() => { abort.abort(); document.removeEventListener('keydown', closePreview) })
</script>

<template>
  <main class="beginner-guide" :class="{ 'guide-theme-inherit': inheritTheme }">
    <header class="guide-header"><h1>小白教程</h1><p>下载软件 → 一键导入 → 开始使用</p></header>

    <section id="download" class="guide-step">
      <h2>第一步：下载并安装软件</h2>
      <div class="guide-os"><span>{{ detected === 'other' ? '选择你的电脑系统：' : `已识别：${detected === 'macos' ? 'Mac' : 'Windows'}` }}</span><div role="group" aria-label="选择电脑系统"><button :aria-pressed="platform === 'macos'" @click="platform = 'macos'">Mac</button><button :aria-pressed="platform === 'windows'" @click="platform = 'windows'">Windows</button></div><select v-if="platform === 'windows'" v-model="architecture" aria-label="Windows 处理器"><option value="x64">Intel / AMD</option><option value="arm64">ARM</option></select></div>
      <div class="guide-downloads">
        <div class="guide-download"><div><strong>CC Switch</strong><span>管理中转配置，先安装并打开一次。</span></div><a :href="ccDownload.url" class="guide-button" target="_blank" rel="noopener noreferrer" data-testid="ccswitch-download">{{ ccDownload.label }} ↓</a><small>{{ ccDownload.mirrored ? `${manifest?.ccswitch_version} · 本站下载` : '官方发布页：Mac 选 .dmg，Windows 选 .msi' }}</small></div>
        <div class="guide-download"><div><strong>ChatGPT 桌面客户端</strong><span>新版包含 Codex，后面用它连接中转站。</span></div><a :href="chatDownload.url" class="guide-button" target="_blank" rel="noopener noreferrer" data-testid="chatgpt-download">{{ chatDownload.label }} ↓</a><small>{{ chatDownload.mirrored ? `本站下载 · ${platform === 'windows' ? '完整 MSIX 安装包' : 'DMG 安装包'}` : '官方渠道下载' }}<a v-if="platform === 'windows'" :href="OFFICIAL_DOWNLOADS.chatgptWindows" target="_blank" rel="noopener noreferrer">微软商店安装 ↗</a></small></div>
      </div>
      <p v-if="platform === 'macos'" class="guide-note"><strong>Mac：</strong>打开 .dmg，把图标拖进「应用程序」。新版 ChatGPT Mac 客户端适用于 Apple 芯片；Intel 用户可用 <a href="https://learn.chatgpt.com/docs/codex/cli" target="_blank" rel="noopener noreferrer">Codex CLI</a>，CC Switch 的 Mac 安装包通用。</p>
      <p v-else-if="platform === 'windows'" class="guide-note"><strong>Windows：</strong>CC Switch 双击 .msi 安装；ChatGPT 可用微软商店，或双击完整 .msix 安装包。ARM 电脑在上方选 ARM。</p>
      <p v-else class="guide-note">请在电脑上选择 Mac 或 Windows 下载。手机可以先看这篇教程。</p>
      <figure><button class="guide-picture" @click="imagePreview = { src: '/tutorial/beginner/install.svg', alt: 'Mac 拖到应用程序，Windows 按安装向导安装' }"><img src="/tutorial/beginner/install.svg" alt="Mac 拖到应用程序，Windows 按安装向导安装" loading="lazy"></button><figcaption>按你的电脑系统安装。图片可点击放大。</figcaption></figure>
    </section>

    <section id="import" class="guide-step">
      <h2>第二步：在本站一键导入 CC Switch</h2>
      <ol><li>登录{{ siteName }}，确认账户有可用额度。</li><li>打开 <a href="/keys">API 密钥</a>，点「创建密钥」，选择支持 <strong>OpenAI / Codex</strong> 的分组。已有密钥可直接用。</li><li>点这一行右侧的 <strong>「导入到 CC Switch」</strong>，允许浏览器打开应用，再确认导入。</li></ol>
      <figure><button class="guide-picture" @click="imagePreview = { src: '/tutorial/beginner/import.svg', alt: '在 API 密钥页面，点击创建密钥和导入到 CC Switch' }"><img src="/tutorial/beginner/import.svg" alt="在 API 密钥页面，点击创建密钥和导入到 CC Switch" loading="lazy"></button><figcaption>先选对密钥，再点右边的导入按钮。密钥已隐藏。</figcaption></figure>
      <p class="guide-note">Claude、Gemini 分组要配对应工具；这篇以 Codex 为例。不确定选哪个分组，先看 <a href="/available-channels">可用渠道</a>。</p>
    </section>

    <section id="start" class="guide-step">
      <h2>第三步：启用配置，开始使用</h2>
      <ol><li>打开 CC Switch → <strong>Codex</strong>，将 <strong>「{{ siteName }}」</strong>设为当前使用的供应商。</li><li>完全退出并重开 ChatGPT / Codex。Mac 用 <kbd>⌘ Q</kbd> 退出；Windows 检查托盘里是否仍在运行。</li><li>进入客户端的 <strong>Codex</strong>，按提示完成首次设置，新建对话，选择分组支持的模型，发一句「你好」测试。</li></ol>
      <figure><button class="guide-picture" @click="imagePreview = { src: '/tutorial/beginner/start.svg', alt: 'CC Switch 中启用小红豆，再到客户端的 Codex 新建对话' }"><img src="/tutorial/beginner/start.svg" alt="CC Switch 中启用小红豆，再到客户端的 Codex 新建对话" loading="lazy"></button><figcaption>选中本站配置 → 重开客户端 → 进入 Codex 使用。</figcaption></figure>
      <p class="guide-note"><strong>确认是否成功：</strong>收到回复后，回本站 <a href="/usage">使用记录</a>，核对刚才的时间、模型和费用。普通 ChatGPT 聊天区的账号与额度单独管理。</p>
    </section>

    <section class="guide-help"><h2>遇到问题</h2><details v-for="[question, answer] in faqs" :key="question"><summary>{{ question }}</summary><p>{{ answer }}</p></details><p>仍有问题，联系 <a href="/support">在线客服</a>，附电脑系统、报错文字和打码截图。不要发送完整密钥、密码或验证码。</p></section>
    <footer>图片为操作示意，界面以实际版本为准。资料核对：2026-10-24。<a :href="OFFICIAL_DOWNLOADS.ccswitch" target="_blank" rel="noopener noreferrer">CC Switch 官方</a> · <a :href="OFFICIAL_DOWNLOADS.chatgpt" target="_blank" rel="noopener noreferrer">ChatGPT 官方</a></footer>
    <div v-if="imagePreview" class="guide-image-modal" role="dialog" aria-modal="true" :aria-label="imagePreview.alt" @click.self="imagePreview = null"><button aria-label="关闭大图" @click="imagePreview = null">关闭 ×</button><img :src="imagePreview.src" :alt="imagePreview.alt"></div>
  </main>
</template>

<style scoped>
.beginner-guide{--g-text:#25323d;--g-muted:#6b7682;--g-border:#e3e7eb;--g-card:#fff;--g-note:#f6f8fa;--g-accent:#3464d5;max-width:960px;margin:0 auto;color:var(--g-text);font-family:-apple-system,BlinkMacSystemFont,"PingFang SC","Microsoft YaHei",sans-serif;font-size:14px;line-height:1.8}.guide-header{padding:0 0 17px;border-bottom:1px solid var(--g-border)}h1{font-size:27px;font-weight:650;line-height:1.5;margin:0 0 3px}.guide-header p{margin:0;font-size:13px;color:var(--g-muted)}.guide-step{padding:25px 0;border-bottom:1px solid var(--g-border);scroll-margin-top:20px}h2{font-size:19px;line-height:1.6;font-weight:650;margin:0 0 16px}p{margin:12px 0}a{color:var(--g-accent);text-decoration:none}a:hover{text-decoration:underline}.guide-os{display:flex;align-items:center;gap:12px;flex-wrap:wrap;font-size:12px;color:var(--g-muted);margin-bottom:15px}.guide-os>div{display:flex;border:1px solid var(--g-border);border-radius:6px;overflow:hidden}.guide-os button{padding:4px 18px;background:var(--g-card);border:0;color:var(--g-muted);cursor:pointer;font-size:12px}.guide-os button+button{border-left:1px solid var(--g-border)}.guide-os button[aria-pressed=true]{color:var(--g-accent);background:#edf3ff;font-weight:600}.guide-os select{background:var(--g-card);border:1px solid var(--g-border);border-radius:5px;padding:5px;color:var(--g-text);font-size:12px}.guide-downloads{display:grid;grid-template-columns:1fr 1fr;gap:15px}.guide-download{border:1px solid var(--g-border);border-radius:8px;padding:17px 20px;background:var(--g-card)}.guide-download strong{display:block;font-size:15px}.guide-download span{display:block;font-size:12px;color:var(--g-muted);margin:3px 0 12px}.guide-button{display:inline-flex;align-items:center;justify-content:center;background:var(--g-accent);border-radius:6px;padding:8px 15px;color:#fff;font-size:12px;font-weight:550;min-height:37px}.guide-button:hover{text-decoration:none;filter:brightness(1.08)}.guide-download small{display:block;font-size:10px;color:var(--g-muted);margin-top:7px}.guide-download small a{margin-left:10px}.guide-note{font-size:12px;color:var(--g-muted);line-height:1.85;background:var(--g-note);padding:10px 13px;border-radius:6px;margin:15px 0 0}.guide-note strong{color:var(--g-text)}figure{margin:17px 0 0}.guide-picture{display:block;width:100%;padding:0;border:1px solid var(--g-border);border-radius:7px;overflow:hidden;background:var(--g-note);cursor:zoom-in}.guide-picture img{display:block;width:100%;height:auto}figcaption{font-size:11px;color:var(--g-muted);margin-top:6px}ol{padding-left:23px;margin:0;color:var(--g-text)}li{padding-left:2px;margin:7px 0}li::marker{color:var(--g-muted)}kbd{font-size:12px;border:1px solid var(--g-border);border-radius:4px;padding:1px 5px;background:var(--g-note)}.guide-help{padding:23px 0}.guide-help h2{font-size:17px;margin-bottom:8px}details{padding:10px 0;border-bottom:1px solid var(--g-border);font-size:12px}summary{cursor:pointer}details p{color:var(--g-muted);margin:8px 0 0}.guide-help>p{font-size:12px;color:var(--g-muted);margin:15px 0 0}footer{border-top:1px solid var(--g-border);padding:15px 0 20px;color:var(--g-muted);font-size:10px;line-height:1.9}.guide-image-modal{position:fixed;inset:0;background:#172535e8;z-index:200;display:flex;align-items:center;justify-content:center;padding:60px 28px}.guide-image-modal>img{max-width:1100px;width:100%;max-height:85vh;object-fit:contain;border-radius:8px}.guide-image-modal>button{position:absolute;right:24px;top:18px;background:#ffffff20;border:1px solid #ffffff50;border-radius:6px;padding:7px 14px;color:#fff;cursor:pointer}button:focus-visible,a:focus-visible,summary:focus-visible,select:focus-visible{outline:3px solid #86a9f3;outline-offset:3px}.dark .beginner-guide.guide-theme-inherit{--g-text:#dde5ef;--g-muted:#a3afbd;--g-border:#334254;--g-card:#1e2c3d;--g-note:#253446;--g-accent:#87aaff}.dark .beginner-guide.guide-theme-inherit .guide-button{background:#426fe2;color:white}.dark .beginner-guide.guide-theme-inherit .guide-os button[aria-pressed=true]{background:#2a3e62}@media(max-width:640px){h1{font-size:24px}h2{font-size:18px}.guide-step{padding:21px 0}.guide-downloads{grid-template-columns:1fr;gap:10px}.guide-download{padding:14px 16px}.beginner-guide{font-size:13px}.guide-note{font-size:11px}.guide-image-modal{padding:55px 8px}figcaption{font-size:10px}}
</style>
