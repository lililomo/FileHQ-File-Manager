import { describe, expect, it } from 'vitest'
import { highlightLines, splitLines } from './highlight'

// Review D (m-09): CRLF / CR files must split into the same lines whether or
// not highlighting is on, and highlight.js output stays sanitised.
describe('highlight review D', () => {
  it('normalises CRLF and CR before highlighting', async () => {
    const crlf = await highlightLines('const a = 1\r\nconst b = "<x>"\r\n', 'javascript')
    const cr = await highlightLines('const a = 1\rconst b = 2', 'javascript')
    expect(crlf?.length).toBe(3) // two lines + the trailing empty one
    expect(cr?.length).toBe(2)
    expect(crlf?.join('\n')).not.toContain('\r')
    expect(crlf?.[1]).toContain('&lt;x&gt;')
    expect(crlf?.[1]).not.toMatch(/<(?!\/?span)/)
  })

  it('splitLines reopens spans across line breaks', () => {
    expect(splitLines('<span class="a">x\ny</span>z')).toEqual([
      '<span class="a">x</span>',
      '<span class="a">y</span>z',
    ])
  })
})
