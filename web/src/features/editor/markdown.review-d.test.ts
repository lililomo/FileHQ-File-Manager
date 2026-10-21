import { describe, expect, it } from 'vitest'
import { renderMarkdown } from './markdown'

// Review D: hostile Markdown/HTML payloads against the DOMPurify profile of
// SPEC §8.13/§8.14. Nothing executable, no foreign resources, no inline
// SVG/MathML/media.
const payloads: Record<string, string> = {
  svgOnload: '<svg onload="window.__xss=1"><circle r="1"/></svg>',
  mathHref: '<math><mi xlink:href="javascript:window.__xss=1">x</mi></math>',
  imgOnerror: '<img src="x" onerror="window.__xss=2">',
  jsLink: '[x](javascript:window.__xss=3)',
  jsLinkSpaced: '[x](  javascript:window.__xss=3)',
  jsLinkEntity: '<a href="&#106;avascript:window.__xss=3">x</a>',
  jsLinkTab: '<a href="java\tscript:window.__xss=3">x</a>',
  dataLink: '[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)',
  vbscript: '<a href="vbscript:msgbox(1)">x</a>',
  iframe: '<iframe src="https://evil.example"></iframe>',
  detailsToggle: '<details open ontoggle="window.__xss=4"><summary>x</summary></details>',
  form: '<form action="https://evil.example"><input name="a"><button>go</button></form>',
  styleTag: '<style>body{background:url(https://evil.example/x)}</style>',
  styleAttr: '<p style="background:url(https://evil.example/x)">p</p>',
  base: '<base href="https://evil.example/">',
  meta: '<meta http-equiv="refresh" content="0;url=https://evil.example">',
  object: '<object data="https://evil.example/x.swf"></object><embed src="x">',
  clobber: '<a id="location" href="#">x</a><img name="body"><form id="document"></form>',
  media:
    '<video src="https://evil.example/v.mp4" autoplay></video><audio src="//evil/x"></audio><source src="x"><track src="y">',
  srcset: '<img srcset="https://evil.example/x 1x" src="a.png">',
  imageMap:
    '<img src="a.png" usemap="#m"><map name="m"><area href="javascript:1" shape="rect" coords="0,0,1,1"></map>',
  mdImageJs: '![x](javascript:alert(1))',
  tableHtml: '| a | b |\n|---|---|\n| <script>x</script> | <b onmouseover=1>b</b> |',
  linkTitle: '[x](https://e.com "on"mouseover="1")',
  codeBlock: '```html\n<script>alert(1)</script>\n```',
}

describe('renderMarkdown review D', () => {
  for (const [name, src] of Object.entries(payloads)) {
    it(name, () => {
      const html = renderMarkdown(src)
      expect(html).not.toMatch(/\son[a-z]+\s*=/i)
      expect(html).not.toMatch(
        /<(script|svg|math|iframe|form|input|button|style|base|meta|object|embed|video|audio|source|track|map|area)\b/i,
      )
      expect(html).not.toMatch(/javascript:|vbscript:|data:/i)
      expect(html).not.toMatch(/srcset|style=|usemap/i)
    })
  }
  it('keeps safe content', () => {
    const html = renderMarkdown('# T\n\n**b** [l](https://e.com) ![i](a.png)\n\n```js\nx<y\n```')
    expect(html).toContain('<h1>T</h1>')
    expect(html).toContain('<strong>b</strong>')
    expect(html).toContain('<a href="https://e.com">l</a>')
    expect(html).toContain('<img src="a.png" alt="i">')
    expect(html).toContain('x&lt;y')
  })
})
