import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { TooltipProvider } from '@/components/ui/tooltip'
import type { Item } from '@/features/browser/api'
import { __mediaUrlInternals } from '@/lib/media-url'
import { setPublicScope } from '@/lib/public-scope'
import { useSessionStore, type ClientConfig } from '@/stores/session'
import { HtmlPreview } from './HtmlPreview'
import { htmlPreviewAvailable } from './htmlPreview'

const item = {
  name: 'halaman.html',
  path: '/halaman.html',
  isDir: false,
  size: 40,
  mime: 'text/html',
  category: 'code',
  ext: 'html',
  etag: 'e1',
  mtime: '2026-10-29T00:00:00Z',
} as unknown as Item

const config = (contentBaseUrl: string): ClientConfig =>
  ({
    appName: 'FM',
    chunkSize: 1,
    maxUploadSize: 1,
    editorMaxFileSize: 1,
    remoteDownloadEnabled: false,
    publicSharesEnabled: true,
    shareAllowNoExpiry: true,
    shareRequirePassword: false,
    passwordMinLength: 12,
    defaultLanguage: 'id',
    adminCanBrowseUsers: false,
    features: {},
    contentBaseUrl,
  }) as ClientConfig

function wrap(ui: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return (
    <QueryClientProvider client={qc}>
      <TooltipProvider>{ui}</TooltipProvider>
    </QueryClientProvider>
  )
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

describe('HtmlPreview (Step 23e)', () => {
  beforeEach(() => {
    __mediaUrlInternals.reset()
    setPublicScope(null)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    setPublicScope(null)
  })

  it('htmlPreviewAvailable follows config.contentBaseUrl', () => {
    expect(htmlPreviewAvailable('')).toBe(false)
    expect(htmlPreviewAvailable(undefined)).toBe(false)
    expect(htmlPreviewAvailable('https://usercontent.example.test')).toBe(true)
  })

  it('without a content origin only the source view exists (v1 behaviour)', async () => {
    useSessionStore.setState({
      status: 'authenticated',
      user: null,
      config: config(''),
      csrfToken: 't',
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          json(200, {
            content: '<p>halo</p>',
            encoding: 'utf-8',
            eol: 'lf',
            etag: 'e1',
            size: 11,
            lossy: false,
          }),
        ),
      ),
    )
    render(wrap(<HtmlPreview item={item} />))
    expect(screen.queryByRole('tablist')).toBeNull()
    expect(screen.queryByTestId('html-preview-frame')).toBeNull()
    await waitFor(() =>
      expect(screen.getByRole('note')).toHaveTextContent('Showing the HTML source'),
    )
  })

  it('with a content origin: sandboxed iframe (no allow-scripts, no allow-same-origin) + source tab', async () => {
    useSessionStore.setState({
      status: 'authenticated',
      user: null,
      config: config('https://usercontent.example.test'),
      csrfToken: 't',
    })
    const abs =
      'https://usercontent.example.test/api/v1/files/raw?path=%2Fhalaman.html&purpose=inline&scope=u%3A1&sig=s'
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.includes('/files/sign'))
        return Promise.resolve(
          json(200, {
            url: abs,
            expiresAt: new Date(Date.now() + 600_000).toISOString(),
            purpose: 'inline',
          }),
        )
      if (url === abs) {
        expect(init?.credentials).toBe('omit')
        return Promise.resolve(new Response('<', { status: 206 }))
      }
      return Promise.resolve(
        json(200, {
          content: '<p>halo</p>',
          encoding: 'utf-8',
          eol: 'lf',
          etag: 'e1',
          size: 11,
          lossy: false,
        }),
      )
    })
    vi.stubGlobal('fetch', fetchMock)
    render(wrap(<HtmlPreview item={item} />))
    const tabs = screen.getAllByRole('tab')
    expect(tabs.map((t) => t.textContent)).toEqual(['Preview', 'Show source'])
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true')
    const frame = await screen.findByTestId('html-preview-frame')
    expect(frame).toHaveAttribute('sandbox', '')
    expect(frame.getAttribute('sandbox')).not.toContain('allow-')
    expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(frame).toHaveAttribute('src', abs)
    expect(frame).toHaveAttribute('title', 'Preview of halaman.html (sandbox)')
    expect(screen.getByTestId('html-sandbox-note')).toHaveTextContent(
      'The preview runs in a sandbox',
    )
    await userEvent.click(screen.getByTestId('html-tab-source'))
    expect(screen.queryByTestId('html-preview-frame')).toBeNull()
    await waitFor(() => expect(screen.getByTestId('code-viewer')).toBeInTheDocument())
  })

  it('guests: the public share meta decides, not the session config', () => {
    useSessionStore.setState({ status: 'anonymous', user: null, config: null, csrfToken: null })
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise<Response>(() => {})),
    )
    setPublicScope({ token: 'tok', contentBaseUrl: 'https://usercontent.example.test' })
    render(wrap(<HtmlPreview item={item} />))
    expect(screen.getAllByRole('tab')).toHaveLength(2)
  })
})
