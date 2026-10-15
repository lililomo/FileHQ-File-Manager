export const TYPE_OPTIONS = [
  'folder',
  'image',
  'video',
  'audio',
  'archive',
  'code',
  'document',
  'spreadsheet',
  'presentation',
  'pdf',
  'text',
  'font',
  'other',
] as const

export interface FilterState {
  recursive: boolean
  type: string
  ext: string
  minKB: string
  maxKB: string
  from: string
  to: string
  tag: string
  regex: boolean
  glob: boolean
  caseSensitive: boolean
  wholeWord: boolean
}

export const DEFAULT_FILTERS: FilterState = {
  recursive: true,
  type: '',
  ext: '',
  minKB: '',
  maxKB: '',
  from: '',
  to: '',
  tag: '',
  regex: false,
  glob: false,
  caseSensitive: false,
  wholeWord: false,
}

export function isDefaultFilters(f: FilterState): boolean {
  return JSON.stringify(f) === JSON.stringify(DEFAULT_FILTERS)
}
