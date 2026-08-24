import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const wordTypes = [
  'application/msword',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  '.doc',
  '.docx',
]

describe('support chat attachment types', () => {
  it.each(['user/SupportChatView.vue', 'admin/SupportChatView.vue'])(
    'offers Word documents in %s',
    (view) => {
      const source = readFileSync(resolve(process.cwd(), 'src/views', view), 'utf8')
      for (const type of wordTypes) expect(source).toContain(type)
    },
  )
})
