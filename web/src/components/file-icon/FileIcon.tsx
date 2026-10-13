import {
  AlignLeft,
  Archive,
  Code,
  File,
  FileText,
  FileType,
  Film,
  Image,
  Music,
  Table2,
  type LucideIcon,
} from 'lucide-react'
import { cn } from '@/lib/utils'

import { isFileCategory, type FileCategory } from './categories'

const GLYPH: Record<Exclude<FileCategory, 'folder'>, LucideIcon> = {
  image: Image,
  video: Film,
  audio: Music,
  archive: Archive,
  code: Code,
  pdf: FileType,
  doc: FileText,
  sheet: Table2,
  text: AlignLeft,
  other: File,
}

const COLOR: Record<FileCategory, string> = {
  folder: 'text-icon-folder',
  image: 'text-icon-image',
  video: 'text-icon-video',
  audio: 'text-icon-audio',
  archive: 'text-icon-archive',
  code: 'text-icon-code',
  pdf: 'text-icon-pdf',
  doc: 'text-icon-doc',
  sheet: 'text-icon-sheet',
  text: 'text-icon-text',
  other: 'text-icon-other',
}

export interface FileIconProps {
  category: FileCategory | string
  /** 16 | 24 | 48 | 96 | 192 (SPEC §9.1) */
  size?: number
  className?: string
  title?: string
}

/** Rounded file sheet in the category colour with a lucide glyph; folders use the folder shape. */
export function FileIcon({ category, size = 16, className, title }: FileIconProps) {
  const cat: FileCategory = isFileCategory(category) ? category : 'other'
  const common = {
    width: size,
    height: size,
    className: cn('shrink-0', COLOR[cat], className),
    'aria-hidden': title ? undefined : true,
    role: title ? 'img' : undefined,
  } as const
  if (cat === 'folder') {
    return (
      <svg viewBox="0 0 24 24" {...common}>
        {title ? <title>{title}</title> : null}
        <path
          d="M3 7.5A2.5 2.5 0 0 1 5.5 5h4.2a2 2 0 0 1 1.5.7L12.6 7h5.9A2.5 2.5 0 0 1 21 9.5V17a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17z"
          fill="currentColor"
          fillOpacity="0.22"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinejoin="round"
        />
      </svg>
    )
  }
  const Glyph = GLYPH[cat]
  const glyphSize = size >= 48 ? 11 : 10
  const offset = (24 - glyphSize) / 2
  return (
    <svg viewBox="0 0 24 24" {...common}>
      {title ? <title>{title}</title> : null}
      <path
        d="M6 2.75h7.2a1.5 1.5 0 0 1 1.06.44l4.55 4.55c.28.28.44.66.44 1.06V19.5A1.75 1.75 0 0 1 17.5 21.25h-11A1.75 1.75 0 0 1 4.75 19.5v-15A1.75 1.75 0 0 1 6.5 2.75z"
        fill="currentColor"
        fillOpacity="0.14"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
      <path
        d="M13.5 3v5h5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
      <Glyph x={offset} y={offset + 2.5} width={glyphSize} height={glyphSize} strokeWidth={2.2} />
    </svg>
  )
}
