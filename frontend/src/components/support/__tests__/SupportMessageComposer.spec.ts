import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SupportMessageComposer from '../SupportMessageComposer.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const createObjectURL = vi.fn(() => 'blob:preview')
const revokeObjectURL = vi.fn()
beforeEach(() => { vi.clearAllMocks(); Object.assign(URL, { createObjectURL, revokeObjectURL }) })
function composer() {
  const wrapper = mount(SupportMessageComposer, {
    props: { draft: '', file: null, sending: false, error: '',
      'onUpdate:draft': value => { void wrapper.setProps({ draft: value }) },
      'onUpdate:file': value => { void wrapper.setProps({ file: value }) },
      'onUpdate:error': value => { void wrapper.setProps({ error: value }) },
    }, global: { stubs: { Icon: true } },
  })
  return wrapper
}
const png = (name = 'pasted.png', size = 12) => new File([new Uint8Array(size)], name, { type: 'image/png' })
function paste(file: File) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { items: [{ kind: 'file', type: file.type, getAsFile: () => file }], files: [file] } })
  return event
}

describe('SupportMessageComposer', () => {
  it('previews a pasted image in the composer, keeps the caption and sends on Enter', async () => {
    const wrapper = composer()
    await wrapper.find('textarea').setValue('截图说明')
    const image = png(); const event = paste(image)
    wrapper.find('textarea').element.dispatchEvent(event)
    await wrapper.vm.$nextTick()
    expect(event.defaultPrevented).toBe(true)
    expect(wrapper.find('[data-testid="attachment-preview"] img').attributes('src')).toBe('blob:preview')
    expect(wrapper.props('file')).toBe(image)
    expect(wrapper.props('draft')).toBe('截图说明')
    await wrapper.find('textarea').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('send')).toHaveLength(1)
    wrapper.unmount()
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:preview')
  })
  it('sends an image with no text and releases its preview after success or removal', async () => {
    const wrapper = composer()
    await wrapper.setProps({ file: png() })
    await wrapper.find('textarea').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('send')).toHaveLength(1)
    await wrapper.find('[aria-label="supportChat.removeAttachment"]').trigger('click')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(revokeObjectURL).toHaveBeenCalledOnce()
    expect(wrapper.find('[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
  it('preserves normal text paste, Shift+Enter and IME candidate selection', async () => {
    const wrapper = composer(); await wrapper.setProps({ draft: '你好' })
    const textPaste = new Event('paste', { bubbles: true, cancelable: true })
    wrapper.find('textarea').element.dispatchEvent(textPaste)
    expect(textPaste.defaultPrevented).toBe(false)
    for (const extra of [{ shiftKey: true }, { isComposing: true }, { keyCode: 229 }]) await wrapper.find('textarea').trigger('keydown', { key: 'Enter', ...extra })
    expect(wrapper.emitted('send')).toBeUndefined()
    wrapper.unmount()
  })
  it('retains the pending image and text on failure and blocks duplicate sends while busy', async () => {
    const wrapper = composer(); const image = png()
    await wrapper.setProps({ draft: '说明', file: image, sending: true })
    await wrapper.find('textarea').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('send')).toBeUndefined()
    await wrapper.setProps({ sending: false, error: 'retry later' })
    expect(wrapper.props('file')).toBe(image)
    expect(wrapper.props('draft')).toBe('说明')
    await wrapper.find('textarea').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('send')).toHaveLength(1)
    wrapper.unmount()
  })
  it('rejects oversized pasted images without replacing the existing attachment', async () => {
    const wrapper = composer(); const previous = png('first.png')
    await wrapper.setProps({ file: previous })
    const event = paste(png('large.png', 4 * 1024 * 1024 + 1))
    wrapper.find('textarea').element.dispatchEvent(event); await wrapper.vm.$nextTick()
    expect(wrapper.props('file')).toBe(previous)
    expect(wrapper.text()).toContain('supportChat.fileTooLarge')
    expect(event.defaultPrevented).toBe(true)
    wrapper.unmount()
  })
  it('releases replaced previews and counts Unicode characters consistently', async () => {
    const wrapper = composer()
    await wrapper.setProps({ file: png('first.png') })
    await wrapper.setProps({ file: png('second.png'), draft: '😀'.repeat(10000) })
    expect(revokeObjectURL).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('10000/10000')
    expect(wrapper.find('[type="submit"]').attributes('disabled')).toBeUndefined()
    await wrapper.setProps({ draft: '你'.repeat(10001) })
    expect(wrapper.find('[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})
