import { configure, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import type { Conflict } from './api'
import { ConflictDialog } from './ConflictDialog'

configure({ asyncUtilTimeout: 8000 })
beforeAll(() => i18n.changeLanguage('id'))

const stat = (size: number, isDir = false) => ({ size, mtime: '2026-10-14T01:00:00Z', isDir })
const conflicts: Conflict[] = [
  { path: '/dst/x1.txt', source: '/src/x1.txt', existing: stat(100), incoming: stat(200) },
  { path: '/dst/x2.txt', source: '/src/x2.txt', existing: stat(10), incoming: stat(20) },
  { path: '/dst/x3.txt', source: '/src/x3.txt', existing: stat(1), incoming: stat(2) },
]

describe('<ConflictDialog>', () => {
  it('shows both sides and walks through the conflicts one by one', async () => {
    const user = userEvent.setup()
    const onResolve = vi.fn()
    render(<ConflictDialog op="copy" conflicts={conflicts} onResolve={onResolve} />)
    expect(await screen.findByTestId('conflict-dialog')).toBeInTheDocument()
    expect(screen.getByText('"x1.txt" sudah ada')).toBeInTheDocument()
    expect(screen.getByText('100 B')).toBeInTheDocument()
    expect(screen.getByText('200 B')).toBeInTheDocument()
    expect(screen.getByTestId('conflict-counter')).toHaveTextContent('1 dari 3')
    await user.click(screen.getByTestId('conflict-skip'))
    expect(screen.getByTestId('conflict-counter')).toHaveTextContent('2 dari 3')
    await user.click(screen.getByTestId('conflict-overwrite'))
    await user.click(screen.getByTestId('conflict-rename'))
    expect(onResolve).toHaveBeenCalledWith({
      '/src/x1.txt': 'skip',
      '/src/x2.txt': 'overwrite',
      '/src/x3.txt': 'rename',
    })
  })

  it('"Terapkan ke semua" answers the remaining conflicts at once', async () => {
    const user = userEvent.setup()
    const onResolve = vi.fn()
    render(<ConflictDialog op="move" conflicts={conflicts} onResolve={onResolve} />)
    await screen.findByTestId('conflict-dialog')
    await user.click(screen.getByTestId('conflict-apply-all'))
    await user.click(screen.getByTestId('conflict-rename'))
    expect(onResolve).toHaveBeenCalledTimes(1)
    expect(onResolve).toHaveBeenCalledWith({
      '/src/x1.txt': 'rename',
      '/src/x2.txt': 'rename',
      '/src/x3.txt': 'rename',
    })
  })

  it('disables overwrite for file/folder mismatches and resolves null on cancel', async () => {
    const user = userEvent.setup()
    const onResolve = vi.fn()
    render(
      <ConflictDialog
        op="copy"
        conflicts={[
          { path: '/dst/a', source: '/src/a', existing: stat(0, true), incoming: stat(5) },
        ]}
        onResolve={onResolve}
      />,
    )
    await screen.findByTestId('conflict-dialog')
    expect(screen.getByTestId('conflict-overwrite')).toBeDisabled()
    expect(screen.queryByTestId('conflict-apply-all')).not.toBeInTheDocument()
    await user.click(screen.getByTestId('conflict-cancel'))
    expect(onResolve).toHaveBeenCalledWith(null)
  })
})
