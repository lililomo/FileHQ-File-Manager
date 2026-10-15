import { lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { PageSpinner } from '@/components/PageSpinner'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'

const EditorWorkspace = lazy(() =>
  import('./EditorWorkspace').then((m) => ({ default: m.EditorWorkspace })),
)

/** Editor as a large modal over the file manager (SPEC §9.3): 90 % of the
 *  viewport, maximize → the `/editor` route with the same file. */
export function EditorModal({ path, onClose }: { path: string | null; onClose: () => void }) {
  const { t } = useTranslation('editor')
  const navigate = useNavigate()
  return (
    <Dialog open={!!path} onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        className="flex h-[90vh] max-w-[92vw] flex-col gap-0 overflow-hidden p-0 sm:max-w-[92vw]"
        showCloseButton={false}
        data-testid="editor-modal"
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        <DialogTitle className="sr-only">{t('title')}</DialogTitle>
        <Suspense fallback={<PageSpinner />}>
          {path ? (
            <EditorWorkspace
              mode="modal"
              initialPath={path}
              onClose={onClose}
              onMaximize={() => {
                onClose()
                void navigate({ to: '/editor', search: { path } })
              }}
            />
          ) : null}
        </Suspense>
      </DialogContent>
    </Dialog>
  )
}
