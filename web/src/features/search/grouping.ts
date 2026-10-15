import type { ContentMatch } from './api'

export interface FileGroup {
  file: string
  matches: ContentMatch[]
}

export function groupMatches(matches: ContentMatch[]): FileGroup[] {
  const map = new Map<string, FileGroup>()
  for (const m of matches) {
    let g = map.get(m.file)
    if (!g) {
      g = { file: m.file, matches: [] }
      map.set(m.file, g)
    }
    g.matches.push(m)
  }
  return [...map.values()]
}
