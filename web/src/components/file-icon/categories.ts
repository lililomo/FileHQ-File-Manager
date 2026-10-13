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

/** Map the listing API category (SPEC §8.3: document/spreadsheet/presentation/font…) to an icon category. */
export function toIconCategory(apiCategory: string | undefined | null): FileCategory {
  switch (apiCategory) {
    case 'document':
    case 'presentation':
      return 'doc'
    case 'spreadsheet':
      return 'sheet'
    case 'font':
      return 'other'
    default:
      return isFileCategory(apiCategory) ? apiCategory : 'other'
  }
}
