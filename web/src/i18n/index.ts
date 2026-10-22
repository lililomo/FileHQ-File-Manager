import i18next from 'i18next'
import { initReactI18next } from 'react-i18next'
import { readStored, STORAGE_KEYS } from '@/lib/theme'
import idCommon from './id/common.json'
import idNav from './id/nav.json'
import idAuth from './id/auth.json'
import idErrors from './id/errors.json'
import idFiles from './id/files.json'
import idFileops from './id/fileops.json'
import idTrash from './id/trash.json'
import idMeta from './id/meta.json'
import idTasks from './id/tasks.json'
import idUpload from './id/upload.json'
import idRemote from './id/remote.json'
import idArchive from './id/archive.json'
import idSearch from './id/search.json'
import idEditor from './id/editor.json'
import idViewer from './id/viewer.json'
import idShare from './id/share.json'
import idAdmin from './id/admin.json'
import enCommon from './en/common.json'
import enNav from './en/nav.json'
import enAuth from './en/auth.json'
import enErrors from './en/errors.json'
import enFiles from './en/files.json'
import enFileops from './en/fileops.json'
import enTrash from './en/trash.json'
import enMeta from './en/meta.json'
import enTasks from './en/tasks.json'
import enUpload from './en/upload.json'
import enRemote from './en/remote.json'
import enArchive from './en/archive.json'
import enSearch from './en/search.json'
import enEditor from './en/editor.json'
import enViewer from './en/viewer.json'
import enShare from './en/share.json'
import enAdmin from './en/admin.json'

export const LANGUAGES = ['id', 'en'] as const
export type Language = (typeof LANGUAGES)[number]
export const DEFAULT_LANGUAGE: Language = 'id'

export const resources = {
  id: {
    common: idCommon,
    nav: idNav,
    auth: idAuth,
    errors: idErrors,
    files: idFiles,
    fileops: idFileops,
    trash: idTrash,
    meta: idMeta,
    tasks: idTasks,
    upload: idUpload,
    remote: idRemote,
    archive: idArchive,
    search: idSearch,
    editor: idEditor,
    viewer: idViewer,
    share: idShare,
    admin: idAdmin,
  },
  en: {
    common: enCommon,
    nav: enNav,
    auth: enAuth,
    errors: enErrors,
    files: enFiles,
    fileops: enFileops,
    trash: enTrash,
    meta: enMeta,
    tasks: enTasks,
    upload: enUpload,
    remote: enRemote,
    archive: enArchive,
    search: enSearch,
    editor: enEditor,
    viewer: enViewer,
    share: enShare,
    admin: enAdmin,
  },
} as const

export function isLanguage(v: unknown): v is Language {
  return v === 'id' || v === 'en'
}

/** Stored choice → browser language → default (SPEC §9.8). */
export function detectLanguage(): Language {
  const stored = readStored('lang')
  if (isLanguage(stored)) return stored
  const nav =
    typeof navigator !== 'undefined' ? (navigator.languages?.[0] ?? navigator.language ?? '') : ''
  return nav.toLowerCase().startsWith('en') ? 'en' : DEFAULT_LANGUAGE
}

export function currentLanguage(): Language {
  const l = i18next.resolvedLanguage ?? i18next.language
  return isLanguage(l) ? l : DEFAULT_LANGUAGE
}

export async function setLanguage(lang: Language, persist = true): Promise<void> {
  if (persist) {
    try {
      localStorage.setItem(STORAGE_KEYS.lang, lang)
    } catch {
      /* ignore */
    }
  }
  await i18next.changeLanguage(lang)
  if (typeof document !== 'undefined') document.documentElement.lang = lang
}

if (!i18next.isInitialized) {
  void i18next.use(initReactI18next).init({
    resources,
    lng: detectLanguage(),
    fallbackLng: DEFAULT_LANGUAGE,
    supportedLngs: [...LANGUAGES],
    defaultNS: 'common',
    ns: ['common', 'nav', 'auth', 'errors', 'files', 'fileops', 'trash'],
    interpolation: { escapeValue: false },
    initAsync: false,
    returnNull: false,
  })
  if (typeof document !== 'undefined') document.documentElement.lang = currentLanguage()
}

export default i18next
