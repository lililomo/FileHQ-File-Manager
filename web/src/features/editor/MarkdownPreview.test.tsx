import { describe, expect, it } from 'vitest'
import { renderMarkdown, resolveRelative } from './markdown'

describe('renderMarkdown (DOMPurify strict profile)', () => {
  it('drops scripts, event handlers, javascript: links and embeds', () => {
    const html = renderMarkdown(
      [
        '# Title',
        '',
        '<script>alert(1)</script>',
        '',
        '<img src="x" onerror="alert(1)">',
        '',
        '[click](javascript:alert(1))',
        '',
        '<iframe src="https://evil.test"></iframe>',
        '',
        '<a href="https://ok.test" onclick="alert(1)">ok</a>',
        '',
        '<style>body{display:none}</style>',
        '',
        '<form action="/x"><input name="a"></form>',
        '',
        'plain **bold** text',
      ].join('\n'),
    )
    expect(html).toContain('<h1>Title</h1>')
    expect(html).toContain('<strong>bold</strong>')
    expect(html).not.toMatch(/<script/i)
    expect(html).not.toMatch(/onerror/i)
    expect(html).toContain('>click</a>')
    expect(html).not.toMatch(/href="javascript/i)
    expect(html).not.toMatch(/javascript:/i)
    expect(html).not.toMatch(/<iframe/i)
    expect(html).not.toMatch(/onclick/i)
    expect(html).not.toMatch(/<style/i)
    expect(html).not.toMatch(/<form|<input/i)
    expect(html).toContain('href="https://ok.test"')
  })

  it('keeps gfm tables and code, and relative links/images', () => {
    const html = renderMarkdown(
      '| a | b |\n| - | - |\n| 1 | 2 |\n\n```js\nlet x\n```\n\n![pic](img/a.png) [doc](./b.md)',
    )
    expect(html).toContain('<table>')
    expect(html).toContain('<code class="language-js">')
    expect(html).toContain('src="img/a.png"')
    expect(html).toContain('href="./b.md"')
  })
})

describe('resolveRelative', () => {
  it('resolves against the document folder and refuses escapes/absolute schemes', () => {
    expect(resolveRelative('/docs/readme.md', 'img/a.png')).toBe('/docs/img/a.png')
    expect(resolveRelative('/docs/readme.md', '../x.md')).toBe('/x.md')
    expect(resolveRelative('/readme.md', '../../x.md')).toBeNull()
    expect(resolveRelative('/docs/readme.md', 'https://e.test/a.png')).toBeNull()
    expect(resolveRelative('/docs/readme.md', '//e.test/a.png')).toBeNull()
    expect(resolveRelative('/docs/readme.md', '#anchor')).toBeNull()
    expect(resolveRelative('/docs/readme.md', '/abs/b.md?x=1#h')).toBe('/abs/b.md')
  })
})
