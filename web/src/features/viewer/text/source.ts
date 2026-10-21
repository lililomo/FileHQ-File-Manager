/** Loading text for the read-only viewers (Step 17b). Small files come from
 *  `GET /files/content` (encoding detection, binary check — same as the
 *  editor); big ones from the signed raw URL with a byte Range so only the
 *  head is transferred. */
import { getContent } from '@/features/editor/api'
import { detectLanguage } from '@/features/editor/languages'
import { ApiError } from '@/lib/api'
import { fetchSigned } from '@/lib/media-url'

/** Full text up to this size (SPEC §8.14 TextViewer ≤ 2 MB). */
export const TEXT_FULL_MAX = 2 * 1024 * 1024
/** Head shown for bigger files. */
export const TEXT_HEAD_BYTES = 256 * 1024

export interface TextSource {
  text: string
  language: string
  /** only the first `headBytes` bytes are shown */
  partial: boolean
  encoding: string
  lossy: boolean
}

export class BinaryFileError extends Error {}

/** First `bytes` bytes of a file decoded as UTF-8 (a cut multi-byte
 *  character at the end is dropped). */
export async function fetchHead(
  path: string,
  bytes: number,
  signal?: AbortSignal,
): Promise<string> {
  const res = await fetchSigned(path, 'media', {
    headers: { Range: `bytes=0-${bytes - 1}` },
    ...(signal ? { signal } : {}),
  })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const buf = new Uint8Array(await res.arrayBuffer())
  const slice = buf.length > bytes ? buf.subarray(0, bytes) : buf
  if (slice.subarray(0, 8000).includes(0)) throw new BinaryFileError('binary')
  let text = new TextDecoder('utf-8', { fatal: false }).decode(slice)
  if (res.status === 206 && text.endsWith('\uFFFD')) text = text.slice(0, -1)
  return text
}

export async function loadText(
  item: { path: string; name: string; size: number },
  signal?: AbortSignal,
): Promise<TextSource> {
  if (item.size > TEXT_FULL_MAX) {
    const text = await fetchHead(item.path, TEXT_HEAD_BYTES, signal)
    return {
      text,
      language: detectLanguage(item.name, text.slice(0, 200).split('\n')[0] ?? ''),
      partial: true,
      encoding: 'utf-8',
      lossy: text.includes('\uFFFD'),
    }
  }
  try {
    const c = await getContent(item.path, {}, signal)
    return {
      text: c.content,
      language: c.language,
      partial: false,
      encoding: c.encoding,
      lossy: c.lossy,
    }
  } catch (e) {
    if (e instanceof ApiError && e.code === 'BINARY_FILE') throw new BinaryFileError('binary')
    throw e
  }
}
