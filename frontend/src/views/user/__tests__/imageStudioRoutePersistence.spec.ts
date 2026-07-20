import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const frontendRoot = resolve(__dirname, '../../../..')

describe('image studio route persistence', () => {
  it('keeps only ImageStudioView alive across route changes', () => {
    const appSource = readFileSync(resolve(frontendRoot, 'src/App.vue'), 'utf8')
    const studioSource = readFileSync(resolve(frontendRoot, 'src/views/user/ImageStudioView.vue'), 'utf8')

    expect(appSource).toContain('<RouterView v-slot="{ Component }">')
    expect(appSource).toContain('<KeepAlive include="ImageStudioView">')
    expect(appSource).toContain('<component :is="Component" />')
    expect(studioSource).toContain("defineOptions({ name: 'ImageStudioView' })")
  })
})
