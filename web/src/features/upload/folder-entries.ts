/** Collects the files of a file input or a drop (SPEC §8.8 step 1): folder
 *  structure comes from `webkitRelativePath` (input[webkitdirectory]) or from
 *  `DataTransferItem.webkitGetAsEntry()` walked recursively. At most
 *  MAX_ENTRIES files per batch; the rest is reported as truncated. */

export const MAX_ENTRIES = 10_000

export interface UploadEntry {
  file: File
  /** Folder path below the destination ("" for a plain file, "a/b" for folder uploads). */
  relativePath: string
}

export interface CollectResult {
  entries: UploadEntry[]
  truncated: boolean
}

/** "a/b/c/file.txt" → "a/b/c"; "file.txt" → "". */
export function relativeDir(webkitRelativePath: string): string {
  const p = webkitRelativePath.replace(/\\/g, '/').replace(/^\/+|\/+$/g, '')
  const i = p.lastIndexOf('/')
  return i < 0 ? '' : p.slice(0, i)
}

/** Files picked through an input (plain or `webkitdirectory`). */
export function entriesFromFileList(files: Iterable<File> | ArrayLike<File>): CollectResult {
  const list = Array.from(files as Iterable<File>)
  const entries: UploadEntry[] = []
  for (const file of list) {
    if (entries.length >= MAX_ENTRIES) return { entries, truncated: true }
    const rel = typeof file.webkitRelativePath === 'string' ? file.webkitRelativePath : ''
    entries.push({ file, relativePath: relativeDir(rel) })
  }
  return { entries, truncated: false }
}

// --- drag & drop (File System Entry API, Chromium/Firefox/Safari) ---

interface FSEntry {
  isFile: boolean
  isDirectory: boolean
  name: string
}
interface FSFileEntry extends FSEntry {
  file(ok: (f: File) => void, err?: (e: unknown) => void): void
}
interface FSDirectoryEntry extends FSEntry {
  createReader(): { readEntries(ok: (es: FSEntry[]) => void, err?: (e: unknown) => void): void }
}

function readAllEntries(dir: FSDirectoryEntry): Promise<FSEntry[]> {
  const reader = dir.createReader()
  const out: FSEntry[] = []
  return new Promise((resolve, reject) => {
    const step = () =>
      reader.readEntries((batch) => {
        if (batch.length === 0) resolve(out)
        else {
          out.push(...batch)
          step() // readEntries returns at most ~100 entries per call
        }
      }, reject)
    step()
  })
}

function fileOf(entry: FSFileEntry): Promise<File> {
  return new Promise((resolve, reject) => entry.file(resolve, reject))
}

async function walk(entry: FSEntry, dir: string, out: UploadEntry[]): Promise<boolean> {
  if (out.length >= MAX_ENTRIES) return true
  if (entry.isFile) {
    try {
      const file = await fileOf(entry as FSFileEntry)
      out.push({ file, relativePath: dir })
    } catch {
      /* unreadable entry (permissions): skipped */
    }
    return false
  }
  if (entry.isDirectory) {
    const children = await readAllEntries(entry as FSDirectoryEntry)
    const sub = dir ? `${dir}/${entry.name}` : entry.name
    for (const child of children) {
      if (await walk(child, sub, out)) return true
    }
  }
  return false
}

/** Files (and folders, structure intact) dropped from the OS. */
export async function entriesFromDataTransfer(dt: DataTransfer): Promise<CollectResult> {
  const items = Array.from(dt.items ?? [])
  const withEntries = items
    .filter((it) => it.kind === 'file')
    .map((it) => (typeof it.webkitGetAsEntry === 'function' ? it.webkitGetAsEntry() : null))
  if (withEntries.length > 0 && withEntries.every((e) => e !== null)) {
    const out: UploadEntry[] = []
    let truncated = false
    for (const e of withEntries) {
      if (await walk(e as unknown as FSEntry, '', out)) {
        truncated = true
        break
      }
    }
    return { entries: out, truncated }
  }
  // no entry API: plain files only
  return entriesFromFileList(dt.files)
}

/** Name for an image pasted from the clipboard (SPEC §8.8: image-YYYYMMDD-HHMMSS). */
export function pastedImageName(file: File, now = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
  const extFromType = file.type.startsWith('image/') ? file.type.slice('image/'.length) : ''
  const ext = (extFromType === 'jpeg' ? 'jpg' : extFromType) || 'png'
  return `image-${stamp}.${ext.replace(/[^a-z0-9]/gi, '')}`
}

/** Files from a paste event: images get a timestamped name because the
 *  clipboard only knows them as "image.png". */
export function entriesFromClipboard(dt: DataTransfer, now = new Date()): UploadEntry[] {
  const out: UploadEntry[] = []
  for (const file of Array.from(dt.files ?? [])) {
    if (
      file.type.startsWith('image/') &&
      /^(image|clipboard|screenshot)?\.?[a-z]*$/i.test(file.name)
    ) {
      out.push({
        file: new File([file], pastedImageName(file, now), { type: file.type }),
        relativePath: '',
      })
    } else {
      out.push({ file, relativePath: '' })
    }
  }
  return out
}
