/** Monaco models per open path (outside React state: large strings). */
import type * as Monaco from 'monaco-editor'

const models = new Map<string, Monaco.editor.ITextModel>()
const viewStates = new Map<string, Monaco.editor.ICodeEditorViewState>()
/** Model version id at the last load/save → dirty = current ≠ saved. */
const savedVersions = new Map<string, number>()

export function getModel(path: string): Monaco.editor.ITextModel | undefined {
  return models.get(path)
}

export function setModel(path: string, model: Monaco.editor.ITextModel): void {
  models.set(path, model)
  savedVersions.set(path, model.getAlternativeVersionId())
}

export function markSaved(path: string): void {
  const m = models.get(path)
  if (m) savedVersions.set(path, m.getAlternativeVersionId())
}

export function isDirty(path: string): boolean {
  const m = models.get(path)
  return !!m && savedVersions.get(path) !== m.getAlternativeVersionId()
}

export function disposeModel(path: string): void {
  models.get(path)?.dispose()
  models.delete(path)
  savedVersions.delete(path)
  viewStates.delete(path)
}

export function saveViewState(
  path: string,
  state: Monaco.editor.ICodeEditorViewState | null,
): void {
  if (state) viewStates.set(path, state)
}

export function takeViewState(path: string): Monaco.editor.ICodeEditorViewState | undefined {
  return viewStates.get(path)
}

export function allModelPaths(): string[] {
  return [...models.keys()]
}
