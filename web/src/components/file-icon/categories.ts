/** Categories match `Item.category` from the listing API (SPEC §9.1 icon colours). */
export const FILE_CATEGORIES = [
  'folder',
  'image',
  'video',
  'audio',
  'archive',
  'code',
  'pdf',
  'doc',
  'sheet',
  'text',
  'other',
] as const
export type FileCategory = (typeof FILE_CATEGORIES)[number]

export function isFileCategory(v: unknown): v is FileCategory {
  return typeof v === 'string' && (FILE_CATEGORIES as readonly string[]).includes(v)
}
