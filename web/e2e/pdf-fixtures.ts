/** PDF fixtures generated at test time (nothing binary is committed): a
 *  multi-page text document with a correct xref table and optional outline,
 *  an optional multi-megabyte junk stream placed before the pages (so page 1
 *  is only reachable through Range requests), and the PDF 1.4 standard
 *  security handler (R2, RC4 40-bit) for the password scenario. */
import { createHash, randomBytes } from 'node:crypto'

const PAD = Buffer.from([
  0x28, 0xbf, 0x4e, 0x5e, 0x4e, 0x75, 0x8a, 0x41, 0x64, 0x00, 0x4e, 0x56, 0xff, 0xfa, 0x01, 0x08,
  0x2e, 0x2e, 0x00, 0xb6, 0xd0, 0x68, 0x3e, 0x80, 0x2f, 0x0c, 0xa9, 0xfe, 0x64, 0x53, 0x69, 0x7a,
])

function md5(...parts: Buffer[]): Buffer {
  const h = createHash('md5')
  for (const p of parts) h.update(p)
  return h.digest()
}

function rc4(key: Buffer, data: Buffer): Buffer {
  const s = new Uint8Array(256)
  for (let i = 0; i < 256; i++) s[i] = i
  for (let i = 0, j = 0; i < 256; i++) {
    j = (j + s[i]! + key[i % key.length]!) & 0xff
    ;[s[i], s[j]] = [s[j]!, s[i]!]
  }
  const out = Buffer.alloc(data.length)
  for (let k = 0, i = 0, j = 0; k < data.length; k++) {
    i = (i + 1) & 0xff
    j = (j + s[i]!) & 0xff
    ;[s[i], s[j]] = [s[j]!, s[i]!]
    out[k] = data[k]! ^ s[(s[i]! + s[j]!) & 0xff]!
  }
  return out
}

function padPassword(pw: string): Buffer {
  return Buffer.concat([Buffer.from(pw, 'latin1'), PAD]).subarray(0, 32)
}

export interface PdfOptions {
  pages: number
  /** text drawn on page n (Helvetica 24 pt) */
  text?: (n: number) => string
  /** bytes of an unused stream object written before the page objects */
  junkBytes?: number
  /** outline entries: title → page number */
  outline?: { title: string; page: number }[]
  /** user password: encrypts strings and streams (R2, RC4 40-bit) */
  password?: string
}

/** Builds a PDF with a valid cross-reference table. */
export function makePdf(opts: PdfOptions): Buffer {
  const text = opts.text ?? ((n) => `Halaman ${n}`)
  const objects: (Buffer | null)[] = [null] // 1-based
  const add = (body: Buffer | string): number => {
    objects.push(Buffer.isBuffer(body) ? body : Buffer.from(body, 'latin1'))
    return objects.length - 1
  }
  const reserve = (): number => {
    objects.push(null)
    return objects.length - 1
  }
  const set = (n: number, body: Buffer | string) => {
    objects[n] = Buffer.isBuffer(body) ? body : Buffer.from(body, 'latin1')
  }

  const id = randomBytes(16)
  let key: Buffer | null = null
  let encryptRef = 0
  if (opts.password !== undefined) {
    const userPad = padPassword(opts.password)
    const ownerKey = md5(userPad).subarray(0, 5) // owner password = user password
    const O = rc4(ownerKey, userPad)
    const P = -1
    const pBytes = Buffer.alloc(4)
    pBytes.writeInt32LE(P)
    key = md5(userPad, O, pBytes, id).subarray(0, 5)
    const U = rc4(key, PAD)
    encryptRef = add(
      `<< /Filter /Standard /V 1 /R 2 /Length 40 /P ${P} /O <${O.toString('hex')}> /U <${U.toString('hex')}> >>`,
    )
  }
  const objKey = (num: number): Buffer => {
    const extra = Buffer.from([num & 0xff, (num >> 8) & 0xff, (num >> 16) & 0xff, 0, 0])
    return md5(key!, extra).subarray(0, 10)
  }
  const stream = (num: number, data: Buffer, dict = ''): Buffer => {
    const body = key ? rc4(objKey(num), data) : data
    return Buffer.concat([
      Buffer.from(`<< /Length ${body.length} ${dict}>>\nstream\n`, 'latin1'),
      body,
      Buffer.from('\nendstream', 'latin1'),
    ])
  }
  const pdfString = (num: number, s: string): string => {
    const raw = Buffer.from(s, 'latin1')
    const body = key ? rc4(objKey(num), raw) : raw
    return `<${body.toString('hex')}>`
  }

  const catalog = reserve()
  const pagesObj = reserve()
  const font = add(
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>',
  )
  if (opts.junkBytes) {
    // random bytes compress badly and sit before every page object
    const junk = reserve()
    set(
      junk,
      stream(
        junk,
        randomBytes(opts.junkBytes),
        '/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 ',
      ),
    )
  }
  const pageRefs: number[] = []
  for (let n = 1; n <= opts.pages; n++) {
    const page = reserve()
    const content = reserve()
    const escaped = text(n).replace(/[\\()]/g, (c) => `\\${c}`)
    set(
      content,
      stream(content, Buffer.from(`BT /F1 24 Tf 60 700 Td (${escaped}) Tj ET`, 'latin1')),
    )
    set(
      page,
      `<< /Type /Page /Parent ${pagesObj} 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 ${font} 0 R >> >> /Contents ${content} 0 R >>`,
    )
    pageRefs.push(page)
  }
  set(
    pagesObj,
    `<< /Type /Pages /Kids [${pageRefs.map((r) => `${r} 0 R`).join(' ')}] /Count ${pageRefs.length} >>`,
  )
  let outlinesRef = 0
  if (opts.outline && opts.outline.length > 0) {
    outlinesRef = reserve()
    const items = opts.outline.map(() => reserve())
    items.forEach((ref, i) => {
      const o = opts.outline![i]!
      const prev = i > 0 ? ` /Prev ${items[i - 1]} 0 R` : ''
      const next = i < items.length - 1 ? ` /Next ${items[i + 1]} 0 R` : ''
      set(
        ref,
        `<< /Title ${pdfString(ref, o.title)} /Parent ${outlinesRef} 0 R${prev}${next} /Dest [${pageRefs[o.page - 1]} 0 R /XYZ 0 842 0] >>`,
      )
    })
    set(
      outlinesRef,
      `<< /Type /Outlines /First ${items[0]} 0 R /Last ${items[items.length - 1]} 0 R /Count ${items.length} >>`,
    )
  }
  set(
    catalog,
    `<< /Type /Catalog /Pages ${pagesObj} 0 R${outlinesRef ? ` /Outlines ${outlinesRef} 0 R /PageMode /UseOutlines` : ''} >>`,
  )

  const parts: Buffer[] = [Buffer.from('%PDF-1.4\n%\xe2\xe3\xcf\xd3\n', 'latin1')]
  const offsets: number[] = [0]
  let pos = parts[0]!.length
  for (let n = 1; n < objects.length; n++) {
    offsets[n] = pos
    const chunk = Buffer.concat([
      Buffer.from(`${n} 0 obj\n`, 'latin1'),
      objects[n]!,
      Buffer.from('\nendobj\n', 'latin1'),
    ])
    parts.push(chunk)
    pos += chunk.length
  }
  const xrefPos = pos
  let xref = `xref\n0 ${objects.length}\n0000000000 65535 f \n`
  for (let n = 1; n < objects.length; n++)
    xref += `${String(offsets[n]).padStart(10, '0')} 00000 n \n`
  const idHex = id.toString('hex')
  xref += `trailer\n<< /Size ${objects.length} /Root ${catalog} 0 R /ID [<${idHex}> <${idHex}>]${encryptRef ? ` /Encrypt ${encryptRef} 0 R` : ''} >>\nstartxref\n${xrefPos}\n%%EOF\n`
  parts.push(Buffer.from(xref, 'latin1'))
  return Buffer.concat(parts)
}
