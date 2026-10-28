import { create } from 'zustand'

export type Role = 'admin' | 'user'

export interface Quota {
  limit: number
  used: number
  trash: number
  versions: number
}

/** Shape of `user` in `POST /auth/login` and `GET /auth/me` (SPEC §6.3). */
export interface User {
  id: string
  username: string
  displayName: string
  role: Role
  status: 'active' | 'disabled'
  mustChangePassword: boolean
  homeProvisioned: boolean
  quotaBytes: number
  createdAt: string
  lastLoginAt?: string
  homeWarning?: string
  /** TOTP 2FA (Step 23a). */
  totpEnabled?: boolean
  totpEnabledAt?: string
  quota?: Quota
  language?: string
  prefs?: Record<string, unknown>
}

/** `config` in `GET /auth/me`. */
export interface ClientConfig {
  appName: string
  chunkSize: number
  maxUploadSize: number
  editorMaxFileSize: number
  remoteDownloadEnabled: boolean
  publicSharesEnabled: boolean
  shareAllowNoExpiry: boolean
  shareRequirePassword: boolean
  passwordMinLength: number
  defaultLanguage: string
  adminCanBrowseUsers: boolean
  /** Step 19a: custom PNG logo uploaded by the admin ("" when none) */
  logoUrl?: string
  shareDefaultExpiryDays?: number
  /** Step 23d: admin setting transcode.enabled with ffmpeg/ffprobe present */
  transcodeEnabled?: boolean
  /** Step 23e: origin that serves signed content ("" = same origin); when set
   *  the HTML viewer offers the sandboxed preview */
  contentBaseUrl?: string
  features: Record<string, boolean>
}

export type SessionStatus = 'unknown' | 'anonymous' | 'authenticated'

export interface SessionState {
  status: SessionStatus
  user: User | null
  config: ClientConfig | null
  csrfToken: string | null
  setAuthenticated: (user: User, config: ClientConfig | null, csrfToken?: string | null) => void
  setAnonymous: () => void
  setCsrfToken: (token: string) => void
  updateUser: (patch: Partial<User>) => void
}

export const useSessionStore = create<SessionState>((set) => ({
  status: 'unknown',
  user: null,
  config: null,
  csrfToken: null,
  setAuthenticated: (user, config, csrfToken) =>
    set((s) => ({
      status: 'authenticated',
      user,
      config: config ?? s.config,
      csrfToken: csrfToken ?? s.csrfToken,
    })),
  setAnonymous: () => set({ status: 'anonymous', user: null, csrfToken: null }),
  setCsrfToken: (token) => set({ csrfToken: token }),
  updateUser: (patch) => set((s) => (s.user ? { user: { ...s.user, ...patch } } : {})),
}))
