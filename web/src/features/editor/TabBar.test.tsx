import { fireEvent, render, screen } from '@testing-library/react'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import { newTab } from '@/stores/editor'
import { TabBar } from './TabBar'

beforeAll(() => i18n.changeLanguage('id'))

function setup(active = '/a.txt') {
  const props = {
    tabs: [newTab('/a.txt', { dirty: true }), newTab('/b/c.md'), newTab('/d.js')],
    activePath: active,
    onActivate: vi.fn(),
    onClose: vi.fn(),
    onCloseOthers: vi.fn(),
    onCloseRight: vi.fn(),
    onCloseAll: vi.fn(),
    onReorder: vi.fn(),
    onCopyPath: vi.fn(),
    onReveal: vi.fn(),
  }
  render(<TabBar {...props} />)
  return props
}

describe('TabBar', () => {
  it('renders names, the unsaved dot and the active tab', () => {
    setup()
    const tabs = screen.getAllByRole('tab')
    expect(tabs).toHaveLength(3)
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true')
    expect(tabs[0]).toHaveTextContent('a.txt')
    expect(screen.getAllByTestId('tab-dirty')).toHaveLength(1)
  })

  it('activates on click, closes on ✕, middle click and Delete', () => {
    const p = setup()
    const tabs = screen.getAllByRole('tab')
    fireEvent.click(tabs[1]!)
    expect(p.onActivate).toHaveBeenCalledWith('/b/c.md')
    fireEvent.click(screen.getAllByTestId('tab-close')[2]!)
    expect(p.onClose).toHaveBeenCalledWith('/d.js')
    fireEvent(tabs[1]!, new MouseEvent('auxclick', { button: 1, bubbles: true }))
    expect(p.onClose).toHaveBeenCalledWith('/b/c.md')
    fireEvent.keyDown(tabs[0]!, { key: 'Delete' })
    expect(p.onClose).toHaveBeenCalledWith('/a.txt')
  })

  it('moves focus with the arrow keys and reorders by drag and drop', () => {
    const p = setup()
    const tabs = screen.getAllByRole('tab')
    fireEvent.keyDown(tabs[0]!, { key: 'ArrowRight' })
    expect(p.onActivate).toHaveBeenCalledWith('/b/c.md')
    fireEvent.keyDown(tabs[0]!, { key: 'End' })
    expect(p.onActivate).toHaveBeenCalledWith('/d.js')
    const dt = { effectAllowed: '', setData: vi.fn(), getData: vi.fn() }
    fireEvent.dragStart(tabs[2]!, { dataTransfer: dt })
    fireEvent.dragOver(tabs[0]!, { dataTransfer: dt })
    fireEvent.drop(tabs[0]!, { dataTransfer: dt })
    expect(p.onReorder).toHaveBeenCalledWith(2, 0)
  })
})
