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

const FOLDER_PATH =
  'M3 7.5A2.5 2.5 0 0 1 5.5 5h4.2a2 2 0 0 1 1.5.7L12.6 7h5.9A2.5 2.5 0 0 1 21 9.5V17a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 17z'
const SHEET_PATH =
  'M6 2.75h7.2a1.5 1.5 0 0 1 1.06.44l4.55 4.55c.28.28.44.66.44 1.06V19.5A1.75 1.75 0 0 1 17.5 21.25h-11A1.75 1.75 0 0 1 4.75 19.5v-15A1.75 1.75 0 0 1 6.5 2.75z'

const symbolId = (cat: FileCategory, large: boolean) =>
  `fm-icon-${cat}${large && cat !== 'folder' ? '-lg' : ''}`

/** The file-type shapes as one hidden SVG sprite, mounted once at the root:
 *  every FileIcon is then a single `<use>` instead of a lucide component tree
 *  (thousands of icons are rendered while scrolling a 10 000-entry folder;
 *  SPEC §9.1 icons, Step 21 "ikon SVG sprite"). */
export function FileIconSprite() {
  const glyphs = Object.entries(GLYPH) as [Exclude<FileCategory, 'folder'>, LucideIcon][]
  return (
    <svg aria-hidden="true" focusable="false" width="0" height="0" className="absolute">
      <symbol id={symbolId('folder', false)} viewBox="0 0 24 24">
        <path
          d={FOLDER_PATH}
          fill="currentColor"
          fillOpacity="0.22"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinejoin="round"
        />
      </symbol>
      {glyphs.flatMap(([cat, Glyph]) =>
        [false, true].map((large) => {
          const glyphSize = large ? 11 : 10
          const offset = (24 - glyphSize) / 2
          return (
            <symbol key={symbolId(cat, large)} id={symbolId(cat, large)} viewBox="0 0 24 24">
              <path
                d={SHEET_PATH}
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
              <Glyph
                x={offset}
                y={offset + 2.5}
                width={glyphSize}
                height={glyphSize}
                strokeWidth={2.2}
              />
            </symbol>
          )
        }),
      )}
    </svg>
  )
}

/** Rounded file sheet in the category colour with a lucide glyph; folders use the folder shape. */
export function FileIcon({ category, size = 16, className, title }: FileIconProps) {
  const cat: FileCategory = isFileCategory(category) ? category : 'other'
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      className={cn('shrink-0', COLOR[cat], className)}
      aria-hidden={title ? undefined : true}
      role={title ? 'img' : undefined}
      data-category={cat}
    >
      {title ? <title>{title}</title> : null}
      <use href={`#${symbolId(cat, size >= 48)}`} />
    </svg>
  )
}
