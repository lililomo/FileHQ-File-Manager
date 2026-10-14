import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { configure, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import type { PreviewResponse, RenameRule } from './api'
import { BatchRenameDialog } from './BatchRenameDialog'
import { DEFAULT_FORM, toRule } from './batchRule'

configure({ asyncUtilTimeout: 8000 })
beforeAll(() => i18n.changeLanguage('id'))

const preview = vi.fn<(paths: string[], rule: RenameRule) => Promise<PreviewResponse>>()
vi.mock('./api', async (orig) => ({
  ...(await orig<typeof import('./api')>()),
  batchRenamePreview: (paths: string[], rule: RenameRule) => preview(paths, rule),
}))

const PATHS = ['/d/foto1.jpg', '/d/foto2.jpg']

function renderDialog(onApply = vi.fn(async () => {})) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onClose = vi.fn()
  render(
    <QueryClientProvider client={qc}>
      <BatchRenameDialog paths={PATHS} onClose={onClose} onApply={onApply} />
    </QueryClientProvider>,
  )
  return { onApply, onClose }
}

beforeEach(() => {
  preview.mockReset()
  preview.mockImplementation(async (paths, rule) => {
    // a fake server: "foto" → rule.replace; identical results collide
    const items = paths.map((p) => {
      const name = p.split('/').pop()!
      const to = rule.find ? name.replace(rule.find, rule.replace ?? '') : name
      return { from: p, to, status: 'ok' as const }
    })
    const seen = new Map<string, number>()
    for (const it of items) seen.set(it.to, (seen.get(it.to) ?? 0) + 1)
    for (const it of items) {
      if ((seen.get(it.to) ?? 0) > 1)
        Object.assign(it, { status: 'collision', reason: 'duplicate_result' })
    }
    return { items, clean: items.every((it) => it.status === 'ok') }
  })
})

describe('toRule', () => {
  it('maps the form to the API rule', () => {
    expect(toRule(DEFAULT_FORM)).toEqual({})
    expect(
      toRule({
        ...DEFAULT_FORM,
        find: 'a',
        replace: 'b',
        regex: true,
        caseInsensitive: true,
        caseMode: 'upper',
        numbering: true,
        pattern: '{name}-{n}',
        start: '5',
        step: '2',
        pad: '3',
        extMode: 'include',
      }),
    ).toEqual({
      find: 'a',
      replace: 'b',
      regex: true,
      case_insensitive: true,
      case: 'upper',
      numbering: { pattern: '{name}-{n}', start: 5, step: 2, pad: 3 },
      ext_mode: 'include',
    })
  })
})

describe('<BatchRenameDialog>', () => {
  it('previews live, disables Terapkan on collisions, applies when clean', async () => {
    const user = userEvent.setup()
    const { onApply, onClose } = renderDialog()
    await screen.findByTestId('batch-rename-dialog')
    const apply = screen.getByTestId('br-apply')
    // nothing changed yet → disabled
    await waitFor(() => expect(preview).toHaveBeenCalled())
    expect(apply).toBeDisabled()

    // "foto1" and "foto2" → both "x" would collide (regex-free replace of the digit)
    await user.type(screen.getByTestId('br-find'), '1')
    await user.type(screen.getByTestId('br-replace'), '2')
    await waitFor(() =>
      expect(screen.getByTestId('br-row-0')).toHaveAttribute('data-status', 'collision'),
    )
    expect(screen.getByTestId('br-to-0')).toHaveTextContent('foto2.jpg')
    expect(screen.getByTestId('br-apply')).toBeDisabled()

    // "foto" → "img" is clean
    await user.clear(screen.getByTestId('br-find'))
    await user.type(screen.getByTestId('br-find'), 'foto')
    await user.clear(screen.getByTestId('br-replace'))
    await user.type(screen.getByTestId('br-replace'), 'img')
    await waitFor(() => expect(screen.getByTestId('br-to-1')).toHaveTextContent('img2.jpg'))
    await waitFor(() => expect(screen.getByTestId('br-apply')).toBeEnabled())
    await user.click(screen.getByTestId('br-apply'))
    await waitFor(() =>
      expect(onApply).toHaveBeenCalledWith(PATHS, { find: 'foto', replace: 'img' }),
    )
    expect(onClose).toHaveBeenCalled()
  })
})
