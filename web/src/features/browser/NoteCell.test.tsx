import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Item } from './api'
import { NoteCell } from './NoteCell'

const item: Item = {
  name: 'a.txt',
  path: '/a.txt',
  type: 'file',
  isDir: false,
  isSymlink: false,
  size: 1,
  itemCount: null,
  mime: 'text/plain',
  typeLabel: 'type.text',
  category: 'text',
  ext: 'txt',
  mtime: '2026-10-14T00:00:00.000Z',
  ctime: '2026-10-14T00:00:00.000Z',
  btime: null,
  atime: '2026-10-14T00:00:00.000Z',
  mode: 'rw-r--r--',
  modeOctal: '644',
  note: 'lama',
  tag: null,
  favorite: false,
  pinned: false,
  shared: 'none',
  media: null,
  thumb: false,
  etag: '"1"',
}

describe('NoteCell', () => {
  it('shows the note read-only without a save handler', () => {
    render(<NoteCell item={item} />)
    const cell = screen.getByTestId('note-cell')
    expect(cell.textContent).toBe('lama')
    expect(cell.dataset.editable).toBeUndefined()
    fireEvent.doubleClick(cell)
    expect(screen.queryByTestId('note-input')).toBeNull()
  })

  it('double-click edits, Enter saves the trimmed note, Esc cancels', async () => {
    const onSave = vi.fn().mockResolvedValue(true)
    render(<NoteCell item={item} onSave={onSave} />)
    fireEvent.doubleClick(screen.getByTestId('note-cell'))
    const input = screen.getByTestId<HTMLInputElement>('note-input')
    expect(input.value).toBe('lama')
    fireEvent.change(input, { target: { value: '  baru  ' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onSave).toHaveBeenCalledWith('/a.txt', 'baru')
    await Promise.resolve()
    // reopen and cancel
    fireEvent.doubleClick(screen.getByTestId('note-cell'))
    fireEvent.change(screen.getByTestId('note-input'), { target: { value: 'x' } })
    fireEvent.keyDown(screen.getByTestId('note-input'), { key: 'Escape' })
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('note-input')).toBeNull()
  })

  it('refuses to save above 500 characters', () => {
    const onSave = vi.fn().mockResolvedValue(true)
    render(<NoteCell item={item} onSave={onSave} />)
    fireEvent.doubleClick(screen.getByTestId('note-cell'))
    const input = screen.getByTestId<HTMLInputElement>('note-input')
    fireEvent.change(input, { target: { value: 'x'.repeat(501) } })
    expect(input.getAttribute('aria-invalid')).toBe('true')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onSave).not.toHaveBeenCalled()
    fireEvent.blur(input)
    expect(onSave).not.toHaveBeenCalled()
  })

  it('does not call save when the note is unchanged', () => {
    const onSave = vi.fn().mockResolvedValue(true)
    render(<NoteCell item={item} onSave={onSave} />)
    fireEvent.doubleClick(screen.getByTestId('note-cell'))
    fireEvent.keyDown(screen.getByTestId('note-input'), { key: 'Enter' })
    expect(onSave).not.toHaveBeenCalled()
  })
})
