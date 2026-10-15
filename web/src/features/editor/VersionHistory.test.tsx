import { fireEvent, render, screen } from '@testing-library/react'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { VersionList } from './VersionHistory'
import type { FileVersion } from './versionsApi'

beforeAll(() => i18n.changeLanguage('id'))

const versions: FileVersion[] = [
  {
    id: 'v3',
    path: '/a.txt',
    no: 3,
    size: 30,
    sha256: 'c',
    reason: 'restore',
    createdAt: new Date().toISOString(),
  },
  {
    id: 'v2',
    path: '/a.txt',
    no: 2,
    size: 20,
    sha256: 'b',
    reason: 'force_save',
    createdAt: '2026-10-14T01:00:00Z',
  },
  {
    id: 'v1',
    path: '/a.txt',
    no: 1,
    size: 10,
    sha256: 'a',
    reason: 'save',
    createdAt: '2026-10-13T01:00:00Z',
  },
]

describe('VersionList', () => {
  it('renders rows newest first with reason labels and fires the actions', () => {
    const onRestore = vi.fn()
    const onDelete = vi.fn()
    const onCompare = vi.fn()
    const onOpen = vi.fn()
    render(
      <TooltipProvider>
        <VersionList
          path="/a.txt"
          data={{ items: versions, limit: 50, enabled: true }}
          onRestore={onRestore}
          onDelete={onDelete}
          onCompare={onCompare}
          onOpen={onOpen}
        />
      </TooltipProvider>,
    )
    const rows = screen.getAllByTestId('version-row')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveAttribute('data-version-no', '3')
    expect(rows[0]).toHaveTextContent('Pulihkan')
    expect(rows[1]).toHaveTextContent('Timpa')
    expect(rows[2]).toHaveTextContent('Simpan')
    expect(screen.getByText(/Maksimal 50 versi/)).toBeInTheDocument()
    fireEvent.click(screen.getAllByTestId('version-restore')[2]!)
    expect(onRestore).toHaveBeenCalledWith(versions[2])
    fireEvent.click(screen.getAllByTestId('version-delete')[0]!)
    expect(onDelete).toHaveBeenCalledWith(versions[0])
    fireEvent.click(screen.getAllByTestId('version-compare')[1]!)
    expect(onCompare).toHaveBeenCalledWith(versions[1])
    fireEvent.click(screen.getAllByTestId('version-open')[0]!)
    expect(onOpen).toHaveBeenCalledWith(versions[0])
  })

  it('shows the empty and disabled states', () => {
    const { rerender } = render(
      <VersionList
        path="/a.txt"
        data={{ items: [], limit: 50, enabled: true }}
        onRestore={vi.fn()}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByTestId('versions-empty')).toBeInTheDocument()
    rerender(
      <VersionList
        path="/a.txt"
        data={{ items: [], limit: 0, enabled: false }}
        onRestore={vi.fn()}
        onDelete={vi.fn()}
      />,
    )
    expect(screen.getByTestId('versions-disabled')).toBeInTheDocument()
  })
})
