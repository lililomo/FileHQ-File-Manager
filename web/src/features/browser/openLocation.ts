import { useNavigate } from '@tanstack/react-router'
import type { Item } from './api'
import { pathToSplat } from './path'

export function locationOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i <= 0 ? '/' : path.slice(0, i)
}

/** Navigates to the item's folder and selects it (`?select=`), SPEC §8.7
 *  "Buka lokasi file". With `openFolderItself` a folder opens directly. */
export function useOpenLocation() {
  const navigate = useNavigate()
  return (item: Pick<Item, 'path' | 'isDir'>, openFolderItself = false) => {
    if (openFolderItself && item.isDir) {
      void navigate({ to: '/files/$', params: { _splat: pathToSplat(item.path) } })
      return
    }
    const dir = locationOf(item.path)
    void navigate({
      to: '/files/$',
      params: { _splat: pathToSplat(dir) },
      search: { select: item.path.slice(dir === '/' ? 1 : dir.length + 1) },
    })
  }
}
