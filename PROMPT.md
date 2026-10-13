# PROMPT.md — Urutan Pengerjaan File Manager (file.kppbwi.app)

| | |
| --- | --- |
| Versi | 1.0 — 14 Oktober 2026 |
| Status | Belum ada step yang dijalankan. Menunggu persetujuan pemilik atas PRD.md dan SPEC.md. |
| Acuan | `PRD.md` (apa & kenapa) · `SPEC.md` (bagaimana) · `PROGRESS.md` (checklist & jurnal) · `promp-master.md` (brief asli) |

## Cara memakai

1. Tinjau dulu `PRD.md` (khususnya §11 Asumsi & pertanyaan terbuka) dan `SPEC.md` §13 ADR. Bila ada keputusan yang ingin diubah, sampaikan **sebelum** Step 1.
2. Jalankan step **satu per satu, berurutan**, masing-masing di **sesi AI yang baru**. Cukup ketik di Claude Code: `Jalankan Step 7a dari PROMPT.md`. AI wajib menyalin Preamble di bawah ke awal kerjanya.
3. Setiap step berakhir dengan laporan singkat (apa yang dibuat, hasil test, catatan untuk step berikutnya) lalu **berhenti**. Tinjau hasilnya sebelum lanjut.
4. Bila sesi terputus di tengah step: buka sesi baru, ketik `Lanjutkan step yang sedang berjalan`. AI membaca `PROGRESS.md` (bagian "Status saat ini") dan `git status`, lalu meneruskan dari kondisi nyata, bukan mengulang dari nol.
5. Step bertanda **R-x** adalah review independen: jalankan di sesi baru **tanpa** konteks implementasi, idealnya oleh model/akun berbeda. Temuan blocker/major wajib ditutup sebelum step berikutnya.
6. Step 22 (deployment) dan sebagian Step 3 (seed admin) mengubah state server. AI wajib meminta konfirmasi pemilik sebelum langkah yang menyentuh aaPanel, systemd, user Linux, atau firewall.

## Preamble (wajib disertakan di awal setiap step)

```text
Kamu melanjutkan proyek File Manager (file.kppbwi.app). Sebelum mulai:
1. Baca PRD.md, SPEC.md, PROGRESS.md, dan bagian SPEC yang dirujuk step ini.
2. Kerjakan HANYA Step <N>. Jangan mengerjakan step lain atau refactor di luar scope.
3. Jika SPEC salah atau kurang, perbarui SPEC.md dulu dan catat alasannya di PROGRESS.md.
4. Selesai = semua acceptance criteria terpenuhi, test lulus, lint dan typecheck bersih.
5. Centang step di PROGRESS.md, tulis ringkasan + catatan untuk step berikutnya, lalu commit.
```

## Aturan umum (berlaku untuk semua step)

1. **Scope ketat.** Satu step = satu sesi = perubahan ≤ ±1.500 baris kode (di luar file yang di-generate seperti `sqlc`, lockfile, fixture). Jika ternyata lebih besar, pecah menjadi sub-step (mis. 12a → 12a-1, 12a-2), catat di PROGRESS.md, dan tetap selesaikan unit yang bisa dites.
2. **Tidak merusak step sebelumnya.** Semua test lama harus tetap lulus. Perubahan pada API publik step lama hanya boleh jika SPEC diperbarui dulu dan alasannya ditulis di PROGRESS.md bagian "Deviasi dari SPEC".
3. **SPEC adalah kontrak.** Nama package, tabel, endpoint, kode error, route, dan env mengikuti SPEC §2–§6 dan §9. Jika SPEC kurang detail, tambahkan ke SPEC (bukan mengarang di kode saja).
4. **Versi library.** Jangan menulis versi dari ingatan. Gunakan versi stable terbaru saat implementasi dan verifikasi di registry (`go list -m -versions`, `pnpm view <pkg> version`). Versi minimum yang beralasan teknis: Go ≥ 1.25 pada patch terbaru — minimal 1.25.12 (CVE-2026-32282 dan CVE-2026-39822 pada `os.Root`; `go.mod` tetap `go 1.25`), Node ≥ 22.
5. **Keamanan sejak awal.** Semua akses file lewat `internal/fsjail`; handler dilarang memanggil `os.Open`, `os.Remove`, dan sejenisnya (ditegakkan lint sejak Step 4). Semua CLI dipanggil dengan argument array (tanpa shell), path binary absolut dari config, `--` sebelum nama file, timeout, `nice`/`ionice`. Tidak ada `sh -c`.
6. **Rahasia.** `APP_SECRET`, password, token share, isi `.env` tidak boleh masuk log, test fixture, commit, atau argumen perintah yang tercatat. `.env` ada di `.gitignore`.
7. **Bahasa.** Dokumen & string UI: Bahasa Indonesia (id-ID) dengan terjemahan en. Kode, identifier, komentar, commit: Inggris. Commit mengikuti Conventional Commits (`feat(scope): …`, `fix(scope): …`, `test(scope): …`, `docs: …`).
8. **Verifikasi nyata.** Acceptance criteria dicek dengan perintah yang benar-benar dijalankan; hasilnya dilaporkan apa adanya. Test yang gagal tidak boleh di-skip untuk "lulus".
9. **Klaim aaPanel.** Hanya yang ada di `promp-master.md` §4 dan SPEC §12 (terverifikasi di server) yang boleh ditulis sebagai fakta; selebihnya tandai "perlu verifikasi".
10. **Jurnal.** Setiap step selesai: centang di `PROGRESS.md`, isi tanggal (`date`), hash commit, ringkasan, dan "catatan untuk step berikutnya".

## Peta step

| Step | Judul | Milestone | Hasil yang bisa diverifikasi | Perkiraan ukuran |
| --- | --- | --- | --- | --- |
| 1 | Scaffolding monorepo, Makefile, lint, CI, config loader, logging, `/healthz` `/readyz`, header dasar | M1 | `make build` menghasilkan satu binary; CI hijau pada repo kosong | ~1.000 baris |
| 2 | DB: migrasi goose, sqlc, tabel inti | M1 | Migrasi naik/turun; test repository lulus | ~800 baris |
| 3 | Auth: seed admin, login/logout, sesi, CSRF, rate limit + lockout, wajib ganti password, provisioning folder | M1 | Alur login teruji lewat API; folder user dibuat saat login pertama | ~1.300 baris |
| 4 | `fsjail` + test keamanan path/symlink | M1 | Coverage package ≥ 90 % | ~1.000 baris |
| 5 | API listing (sort server, cursor), mkdir, create, stat | M1 | Test API lulus; folder 10.000 item p95 < 500 ms | ~900 baris |
| R-A | Review independen Step 1–5 | M1 | `docs/reviews/review-A.md`, blocker ditutup | — |
| 6 | Frontend shell: Vite/React, router, token, light/dark, i18n, `/dev/design`, login & ganti password | M1 | Login lewat UI; E2E pertama lulus | ~1.400 baris |
| 7a | File browser inti: layout, sidebar, address bar, riwayat, Details view tervirtualisasi, status bar | M1 | Navigasi folder di UI, 10.000 baris mulus | ~1.300 baris |
| 7b | Kolom, sort, seleksi, keyboard, filter cepat, type-ahead, tab folder, panel preview | M1 | Sort per kolom & seleksi keyboard di UI | ~1.300 baris |
| 8 | 4 view lain + pipeline thumbnail + media_metadata | M1 | 5 view jalan dengan thumbnail | ~1.400 baris |
| 9a | Operasi file backend: rename, copy, move, duplicate, symlink, chmod, batch rename, delete → trash, konflik, lock, undo | M2 | Test integrasi operasi lulus | ~1.400 baris |
| 9b | Operasi file frontend: context menu, clipboard, dialog, DnD, rename inline, undo toast | M2 | E2E operasi file lulus | ~1.400 baris |
| 10a | Recycle Bin, kuota, hitung ukuran folder, checksum | M2 | E2E trash + test kuota lulus | ~1.200 baris |
| 10b | Favorit, pin, catatan, tag, Recent, Properties, Analisis penyimpanan | M2 | Metadata ikut saat rename/move | ~1.200 baris |
| R-B | Review independen Step 6–10b | M2 | `docs/reviews/review-B.md` | — |
| 11 | Task engine + SSE + panel task + notifikasi | M2 | Task bisa dibatalkan; progress tampil realtime | ~1.300 baris |
| 12a | Upload tus + precheck + panel transfer | M2 | Upload 1 GB bisa di-pause/resume & lanjut setelah reload | ~1.300 baris |
| 12b | Download Range, zip streaming, signed URL | M2 | Resume download; zip folder tanpa file sementara | ~900 baris |
| 13 | Remote download + SSRF guard | M2 | Test SSRF lulus | ~900 baris |
| 14a | Arsip: compress/extract semua format, limit keamanan, fixture berbahaya | M3 | Test zip-slip/bomb lulus | ~1.500 baris |
| 14b | Archive browser, password, encoding nama | M3 | Buka arsip seperti folder; extract sebagian | ~1.100 baris |
| R-C | Review independen Step 11–14b | M3 | `docs/reviews/review-C.md` | — |
| 15 | Search nama, isi, replace in files | M3 | E2E search; replace bisa di-rollback | ~1.300 baris |
| 16a | Editor Monaco inti | M3 | E2E edit + simpan + reload | ~1.400 baris |
| 16b | Editor lanjutan: konflik, versi + diff, find in files, Markdown | M3 | E2E konflik & restore versi | ~1.200 baris |
| 17a | Viewer gambar + PDF | M3 | E2E buka gambar & PDF | ~1.200 baris |
| 17b | Audio + mini-player, video, teks/Markdown/CSV/JSON, Quick Look | M3 | E2E buka tiap tipe | ~1.400 baris |
| R-D | Review independen Step 15–17b | M3 | `docs/reviews/review-D.md` | — |
| 18a | Sharing: link publik + halaman publik | M4 | Test akses tamu; IDOR share | ~1.400 baris |
| 18b | Share ke user, "Dibagikan ke saya", "Share saya" | M4 | Test akses lintas user | ~1.300 baris |
| 19a | Admin: dashboard, users, settings | M4 | E2E admin buat user | ~1.400 baris |
| 19b | Admin: audit, tasks, maintenance; profil & sesi | M4 | Export CSV; backup SQLite | ~1.200 baris |
| R-E | Review independen Step 18a–19b | M4 | `docs/reviews/review-E.md` | — |
| 20 | Hardening: CSP final, OWASP, IDOR, rate limit, govulncheck | M5 | Checklist keamanan lulus | ~900 baris |
| 21 | Performa & aksesibilitas | M5 | Target SPEC §1.6 & PRD §8 tercapai; axe 0 serius | ~900 baris |
| 22 | Deployment: install.sh, update.sh, systemd, Nginx aaPanel, smoke test | M5 | `https://file.kppbwi.app/healthz` 200 dari server kosong | ~1.000 baris |
| R-F | Review independen Step 20–22 + checklist rilis | M5 | `docs/reviews/review-F.md`; tag `v1.0.0` | — |
| 23 | Opsional: 23a 2FA, 23b dual-pane, 23c duplicate finder, 23d transcoding, 23e origin konten terpisah | M6 | Sesuai prioritas PRD §9 | per sub-step |

Milestone: M1 fondasi · M2 operasi file + transfer · M3 arsip + editor + viewer · M4 sharing + admin · M5 hardening + deploy · M6 opsional. **MVP v1.0 = M1–M5.**

---

# Milestone M1 — Fondasi

## Step 1 — Scaffolding monorepo, Makefile, lint, CI, config loader, logging, `/healthz` `/readyz`

> Sertakan Preamble di atas.

**Tujuan:** Repo bisa di-build menjadi satu binary (`bin/filemanager`) dengan frontend ter-embed, konfigurasi dari env tervalidasi, log JSON, endpoint kesehatan hidup, dan CI hijau — tanpa fitur bisnis apa pun.

**Prasyarat:** Tidak ada. Pastikan Go ≥ 1.25.12 (patch terbaru seri 1.25 atau 1.26.x; dua CVE `os.Root` di patch lebih lama) dan Node ≥ 22 + pnpm (via `corepack enable`) tersedia di mesin kerja; di server ini Go belum terpasang (apt hanya 1.22) → pasang dari tarball go.dev ke `/usr/local/go` dan catat caranya di `README.md`.

**Baca dulu:** SPEC §2 (struktur repo), §3 (konfigurasi), §1.6 (batas resource), §10.3 (header dasar), §11 (strategi testing), §13 ADR-01, ADR-02, ADR-21, ADR-22; PRD epic E14.

**Tugas:**
1. Buat struktur monorepo persis SPEC §2: `backend/` (module `kppbwi.app/filemanager`, `go.mod` dengan `go 1.25`), `web/`, `deploy/` (kosong + `README.md`), `docs/`, `Makefile`, `.editorconfig`, `.gitignore` (sudah ada — lengkapi: `bin/`, `web/dist/`, `node_modules/`, `.env`, `*.db*`, coverage).
2. `backend/cmd/filemanager/main.go`: subcommand `serve` (default), `version`, `check-deps` (mendeteksi binary ffmpeg/ffprobe/vipsthumbnail/pdftoppm/7zz/rg/unrar/rar dari env path dan mencetak tabel ada/tidak + versi), `migrate` (placeholder, diisi Step 2). Flag `--env-file` hanya untuk dev (produksi memakai `EnvironmentFile` systemd).
3. `internal/config`: struct `Config` dengan SEMUA variabel di SPEC §3 (termasuk opsional `METRICS_ADDR`, `CONTENT_BASE_URL`, `VIPS_PATH`, `PDFTOPPM_PATH`, `FFPROBE_PATH`, `THUMB_WORKERS`, `TASK_MAX_PER_USER`, `LISTING_PAGE_SIZE`, `SIGNED_URL_TTL`, `MEDIA_URL_TTL`, `AUDIT_RETENTION_DAYS`, `ACTIVITY_RETENTION_DAYS`, `TZ_DEFAULT`). Parser ukuran (`10GB`, `16MB`, `5MB`), durasi (`2h`, `168h`), bool, addr. Validasi fail-fast yang mengumpulkan semua error lalu keluar dengan kode 2 dan pesan per variabel. Aturan: `APP_SECRET` wajib 64 hex; `LISTEN_ADDR` harus loopback di `APP_ENV=production` (kalau tidak → error); `STORAGE_ROOT` dan `DATA_DIR` harus path absolut, ada, dan `DATA_DIR` berada di filesystem yang sama dengan `STORAGE_ROOT` (bandingkan device id via `syscall.Stat_t.Dev`; beda → **warning**, set `cfg.CrossFSTrash=true`); `UPLOAD_CHUNK_SIZE` ≤ `MAX_UPLOAD_SIZE`; `TRUSTED_PROXIES` daftar CIDR valid; `THUMB_WORKERS` default `max(1, NumCPU-1)`.
4. `internal/config` juga membuat sub-folder `DATA_DIR/{db,trash,versions,uploads,thumbs,tmp,logs,backups}` mode 0750 saat start (idempoten) dan menolak start jika tidak bisa menulis.
5. Logging: `log/slog` JSON ke stdout, level dari `LOG_LEVEL`; middleware request ID (`X-Request-ID` dari proxy hanya dipercaya jika remote ada di `TRUSTED_PROXIES`, selain itu generate), access log satu baris per request (method, path tanpa query, status, durasi, bytes, user_id jika ada, request_id). Fungsi `Redact()` untuk field sensitif; test memastikan nilai `APP_SECRET` dan `ADMIN_INITIAL_PASSWORD` tidak pernah muncul di output log.
6. `internal/httpapi`: router chi, middleware `RealIP` (menghormati `X-Forwarded-For` hanya dari trusted proxy), `Recoverer` (500 dengan envelope error SPEC §6.1, tanpa stack ke client), `SecurityHeaders` (`X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, `X-Frame-Options: DENY`, `Permissions-Policy` minimal, HSTS hanya jika `X-Forwarded-Proto=https`, CSP awal `default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'` — final di Step 20), `Timeout` default 60 s kecuali route streaming (ditandai Step 11/12), `MaxBytes` 1 MB untuk JSON body. Envelope error `{"error":{"code","message","details"}}` + helper `WriteError(w, code, status, msg, details)` dan tabel kode error SPEC §6.2 sebagai konstanta.
7. Endpoint: `GET /healthz` → `200 {"status":"ok","version":"…"}`; `GET /readyz` → cek `DATA_DIR` writable, `STORAGE_ROOT` readable (DB ditambah Step 2) → `200` atau `503 {"status":"degraded","checks":{…}}`.
8. `internal/webassets`: `embed.FS` atas `backend/internal/webassets/dist/` (hasil copy `web/dist`), handler SPA fallback (`index.html` untuk route non-API non-file), cache `immutable` untuk aset ber-hash, `no-store` untuk `index.html`. Sertakan `dist/.gitkeep` + `index.html` placeholder agar `go build` sukses tanpa frontend.
9. `web/`: scaffold Vite + React + TypeScript (strict), pnpm, ESLint + Prettier, Vitest + Testing Library, script `dev`, `build`, `lint`, `typecheck`, `test`. Halaman placeholder "FileManager — scaffold" (UI nyata di Step 6). Vite dev proxy `/api`, `/healthz` → `127.0.0.1:8080`.
10. `Makefile`: `deps`, `build` (pnpm build → copy ke webassets/dist → `go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags --always)"` dengan `CGO_ENABLED=0`), `test` (go test -race ./... + pnpm test), `lint` (golangci-lint + pnpm lint + pnpm typecheck), `dev` (backend `go run` + `vite`), `check-deps`, `clean`. `golangci.yml` dengan `govet`, `staticcheck`, `errcheck`, `gosec`, `forbidigo` (pola `os.Open|os.Create|os.Remove…` dikecualikan untuk `internal/fsjail`, `internal/config`, `internal/db`, `cmd/`; aktif penuh Step 4).
11. `.github/workflows/ci.yml`: job backend (setup Go dari `go.mod`, `go build`, `go test -race -cover`, golangci-lint, `govulncheck ./...`), job web (pnpm install --frozen-lockfile, lint, typecheck, test, build), job openapi (lint `docs/openapi.yaml` dengan `redocly lint` atau `spectral`), job `make build` menghasilkan artefak binary. Catat di `README.md` bahwa hosting git belum ditentukan (asumsi GitHub Actions).
12. `docs/openapi.yaml` kerangka: `info`, `servers`, `components.schemas.Error` (envelope), `components.securitySchemes.cookieAuth`, path `/healthz`, `/readyz`. `docs/SECURITY.md` kerangka (threat model ringkas merujuk SPEC §10, kebijakan pelaporan, daftar kontrol yang akan diisi tiap step).
13. `README.md` ringkas: tujuan, struktur, cara dev (`make dev`), cara build, rujukan ke PRD/SPEC/PROMPT/PROGRESS/DEPLOY.

**File yang dibuat/diubah:** `Makefile`, `.editorconfig`, `.gitignore`, `README.md`, `backend/go.mod`, `backend/cmd/filemanager/main.go`, `backend/internal/config/*.go`, `backend/internal/httpapi/{router,middleware,errors,health}.go`, `backend/internal/webassets/*`, `backend/.golangci.yml`, `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/src/main.tsx`, `web/eslint.config.js`, `.github/workflows/ci.yml`, `docs/openapi.yaml`, `docs/SECURITY.md`, `deploy/README.md`.

**Acceptance criteria:**
- [ ] `make build` → `bin/filemanager` ada, `file bin/filemanager` menunjukkan statically linked; `./bin/filemanager version` mencetak versi.
- [ ] `APP_SECRET=$(openssl rand -hex 32) ./bin/filemanager serve --env-file .env` lalu `curl -i http://127.0.0.1:8080/healthz` → `200` JSON `{"status":"ok",…}`; `curl -i /readyz` → `200`.
- [ ] Menjalankan tanpa `APP_SECRET` → exit code 2, stderr menyebut `APP_SECRET`; `LISTEN_ADDR=0.0.0.0:8080 APP_ENV=production` → exit 2 dengan pesan loopback.
- [ ] `curl -i /tidak-ada` → `200` index.html SPA; `curl -i /api/v1/x` → `404` JSON envelope `NOT_FOUND`.
- [ ] Header respons memuat `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Content-Security-Policy`.
- [ ] `./bin/filemanager check-deps` mencetak tabel: di server ini `ffmpeg`/`ffprobe`/`7zz`/`vipsthumbnail`/`pdftoppm`/`rg` TIDAK ADA, `rar`/`unrar` NONAKTIF (sesuai kondisi nyata; catatan Step 1: `rg` yang semula dikira ada ternyata fungsi shell, bukan binary).
- [ ] `make lint` dan `make test` bersih; `pnpm build` sukses; CI workflow valid (`actionlint` atau dry-run).

**Test wajib:**
- `internal/config/config_test.go`: parse size/duration, missing required, invalid CIDR, loopback rule, chunk > max, DATA_DIR cross-FS warning (pakai `t.TempDir()` + mock device id).
- `internal/httpapi/middleware_test.go`: request ID dari trusted vs untrusted proxy, security headers, envelope 404/405/500, MaxBytes.
- `internal/httpapi/health_test.go`: `/healthz`, `/readyz` degraded saat DATA_DIR tidak writable.
- `internal/config/redact_test.go`: log tidak memuat secret.
- `web/src/App.test.tsx`: render placeholder.

**Verifikasi:** `make lint && make test && make build && ./bin/filemanager version && ./bin/filemanager check-deps`.

**Di luar scope step ini:** DB, auth, UI nyata, CSP final, deploy script.

**Commit:** `feat(scaffold): monorepo skeleton, config loader, logging, health endpoints, CI`

---

## Step 2 — DB: migrasi goose, sqlc, tabel inti

> Sertakan Preamble di atas.

**Tujuan:** SQLite (WAL) terbuka dengan aman, migrasi bisa naik/turun, query type-safe via sqlc, repository untuk `users`, `sessions`, `settings`, `user_preferences`, `audit_logs`, `login_attempts` siap dipakai Step 3.

**Prasyarat:** Step 1.

**Baca dulu:** SPEC §4 (ERD, definisi tabel, konvensi id/waktu), §3 (`DATA_DIR`), §13 ADR-02; PRD epic E1, E12.

**Tugas:**
1. `internal/db/open.go`: `Open(path)` memakai `modernc.org/sqlite` dengan pragma `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`, `temp_store=MEMORY`; pool: 1 koneksi tulis + N baca (`SetMaxOpenConns`), helper `WithTx`.
2. Migrasi `goose` di `backend/migrations/` (SQL, embedded): `0001_users_sessions.sql`, `0002_settings_preferences.sql`, `0003_audit_login_attempts.sql` sesuai kolom & index di SPEC §4.2 untuk `users`, `sessions`, `settings`, `user_preferences`, `folder_view_prefs`, `audit_logs`, `login_attempts`. Setiap migrasi punya `Down`.
3. `backend/sqlc.yaml` + `backend/internal/db/queries/*.sql`; generate ke `internal/db/sqlc/` (di-commit). Query minimal: users (create, get by id/username, update password/flags/quota/status/last_login/home_provisioned_at, list dengan filter + paging, count), sessions (create, get by hash, touch last_seen, revoke by id/user, delete expired), settings (get all, upsert), preferences (get/upsert per key), audit (insert, list dengan filter + cursor, delete older than), login_attempts (insert, count window, delete older).
4. `internal/db/repo.go`: interface `Repository` per domain (`UserRepo`, `SessionRepo`, `SettingsRepo`, `PrefRepo`, `AuditRepo`) agar bisa ganti ke PostgreSQL; implementasi sqlite memanggil sqlc.
5. Konvensi SPEC §4.3: id = UUIDv7 (string 36 karakter lowercase, terurut waktu, `github.com/google/uuid`), waktu = TEXT ISO-8601 UTC dengan milidetik (`2026-10-14T03:04:05.123Z`, lebar tetap agar urutan leksikal = kronologis), path = string path virtual NFC. (Dikoreksi saat Step 2: versi sebelumnya menyebut ULID + Unix millis yang bertentangan dengan SPEC.)
6. Subcommand `filemanager migrate up|down|status` dan auto-migrate saat `serve` start (dapat dimatikan dengan `--no-migrate`).
7. `internal/db/backup.go`: `Backup(ctx, dst)` memakai `VACUUM INTO` ke file sementara lalu rename; dipakai Step 19b.
8. `/readyz` menambah cek `SELECT 1` dan status migrasi (pending → 503).
9. Update `docs/openapi.yaml` tidak perlu; update SPEC §4 jika ada kolom yang berubah saat implementasi (catat alasan di PROGRESS).

**File yang dibuat/diubah:** `backend/migrations/000{1,2,3}_*.sql`, `backend/sqlc.yaml`, `backend/internal/db/{open,migrate,repo,backup}.go`, `backend/internal/db/queries/*.sql`, `backend/internal/db/sqlc/*` (generated), `backend/cmd/filemanager/main.go`, `backend/internal/httpapi/health.go`, `Makefile` (`make sqlc`, `make migrate`).

**Acceptance criteria:**
- [ ] `./bin/filemanager migrate status` menampilkan 3 migrasi applied; `migrate down` lalu `up` kembali tanpa error pada DB kosong.
- [ ] `sqlite3 $DATA_DIR/db/app.db "PRAGMA journal_mode"` → `wal`; file `app.db-wal` muncul saat ada tulisan.
- [ ] `go test ./internal/db/...` lulus termasuk test paralel baca saat tulis (tidak ada `SQLITE_BUSY` dalam 5 s).
- [ ] `make sqlc` tidak mengubah file yang di-commit (generated up-to-date; dicek di CI dengan `git diff --exit-code`).
- [ ] `Backup` menghasilkan DB yang bisa dibuka dan berisi baris yang sama.

**Test wajib:**
- `internal/db/migrate_test.go`: up → down → up pada temp DB; status.
- `internal/db/repo_test.go`: CRUD users (unique username, case), sessions (expired cleanup), settings upsert, preferences, audit cursor paging, login_attempts window.
- `internal/db/backup_test.go`.

**Verifikasi:** `make sqlc && git diff --exit-code && make test && make lint`.

**Di luar scope step ini:** Tabel fitur (shares, tasks, dsb. — ditambahkan di step pemiliknya), endpoint auth.

**Commit:** `feat(db): sqlite with goose migrations, sqlc queries, core repositories`

---

## Step 3 — Auth: seed admin, login/logout, sesi, CSRF, rate limit + lockout, wajib ganti password, provisioning folder

> Sertakan Preamble di atas.

**Tujuan:** Alur autentikasi lengkap lewat API: admin pertama dari `.env`, login dengan sesi server-side, CSRF, rate limit + lockout bertahap, paksa ganti password, dan folder user dibuat idempoten saat login pertama.

**Prasyarat:** Step 2.

**Baca dulu:** SPEC §8.1 (auth & akun), §8.2 (provisioning & kuota — bagian provisioning saja), §6.3 grup `auth` dan `me`, §6.2 kode error, §10.4 (password & sesi), §10.5 (angka rate limit), §13 ADR-13, ADR-14; PRD epic E1, E2.

**Tugas:**
1. `internal/auth/password.go`: argon2id (m=64 MiB, t=3, p=1, salt 16 B, hash 32 B; format PHC string), `Verify` constant-time, rehash otomatis jika parameter berubah. Kebijakan: min 12 karakter, maks 128, tidak sama dengan username, tidak ada di daftar 100k password umum (file embed `internal/auth/common_passwords.txt` dengan lisensi permisif — catat sumber & lisensi di file `NOTICE`).
2. Seed admin: saat `serve` start dan `COUNT(users)=0` → buat `ADMIN_USERNAME` dengan `ADMIN_INITIAL_PASSWORD`, role `admin`, `must_change_password=1`; tulis audit `admin.seeded`. Jika users sudah ada dan env masih berisi `ADMIN_INITIAL_PASSWORD` → log `warn` "hapus ADMIN_INITIAL_PASSWORD dari .env" (sekali saat start). Env tidak pernah dipakai ulang.
3. Sesi (`internal/auth/session.go`): id 32 byte acak → cookie `fm_session` (`HttpOnly; Secure; SameSite=Lax; Path=/`; `Secure` dilepas hanya jika `APP_ENV=development`), DB menyimpan SHA-256(id), `csrf_secret` 32 B, `ip`, `user_agent`, `created_at`, `last_seen_at`, `expires_at` = min(idle `SESSION_IDLE_TIMEOUT`, absolut `SESSION_ABSOLUTE_TIMEOUT`); sliding refresh maks sekali/5 menit; revoke per sesi & semua sesi user; job pembersih sesi kedaluwarsa tiap 10 menit.
4. CSRF: `GET /api/v1/auth/csrf` → token = base64(HMAC-SHA256(csrf_secret, session_id)); middleware `RequireCSRF` untuk semua metode selain GET/HEAD/OPTIONS: header `X-CSRF-Token` valid **dan** (`Sec-Fetch-Site` ∈ {same-origin, none} atau `Origin` == `APP_BASE_URL`), gagal → `403 CSRF_INVALID`.
5. Endpoint: `POST /api/v1/auth/login` `{username,password}` → `200 {user, must_change_password, csrf_token}` + cookie; `POST /auth/logout`; `GET /auth/me` → user + preferensi ringkas + `home_provisioned`; `POST /auth/change-password` `{current_password,new_password}` → validasi kebijakan, tidak boleh sama dengan password sekarang, revoke semua sesi lain, set `must_change_password=0`.
6. Middleware: `RequireAuth` (sesi valid, user `active`; nonaktif → `403 ACCOUNT_DISABLED` + hapus cookie), `RequirePasswordChanged` (jika `must_change_password` → `403 PASSWORD_CHANGE_REQUIRED` untuk semua endpoint kecuali `auth/me`, `auth/logout`, `auth/change-password`, `auth/csrf`), `RequireAdmin` (`403 FORBIDDEN`).
7. Rate limit & lockout (`internal/auth/ratelimit.go`): per IP 20 login/menit (token bucket, `429 RATE_LIMITED` + `Retry-After`); per username: 5 gagal dalam 15 menit → `locked_until` bertahap 1 → 5 → 15 → 60 menit (`423 ACCOUNT_LOCKED`, pesan tanpa membocorkan apakah username ada: respons login gagal selalu `401 UNAUTHENTICATED` dengan pesan sama; lockout hanya terlihat setelah username benar-benar ada dan dikunci — catat pertimbangan enumerasi di SPEC §10.1). Catat di `login_attempts`.
8. Provisioning (`internal/auth/provision.go` memanggil fsjail minimal atau `os` — **pengecualian lint** sampai Step 4 lalu di-refactor): saat login sukses pertama, `MkdirAll(STORAGE_ROOT/<username>, 0750)` idempoten; jika folder sudah ada sebelum provisioning → tetap pakai, tulis audit `user.home_preexisting` + notifikasi admin (tabel notifikasi baru di Step 11 → sementara audit saja), set `home_provisioned_at`.
9. Audit: `login.success`, `login.failed` (username yang dicoba, IP), `login.locked`, `password.changed`, `session.revoked`.
10. `docs/openapi.yaml`: tambah semua endpoint auth dengan skema request/response dan kode error.

**File yang dibuat/diubah:** `backend/internal/auth/{password,session,csrf,ratelimit,provision,service}.go`, `backend/internal/auth/common_passwords.txt`, `backend/internal/httpapi/{auth,middleware_auth}.go`, `backend/cmd/filemanager/main.go` (seed), `backend/internal/audit/audit.go`, `docs/openapi.yaml`, `NOTICE`.

**Acceptance criteria:**
- [ ] Start pertama dengan DB kosong → log `admin.seeded`; `curl -c c.txt -X POST /api/v1/auth/login -d '{"username":"admin","password":"<dari .env>"}'` → `200`, `must_change_password:true`, `Set-Cookie: fm_session=…; HttpOnly; Secure; SameSite=Lax`.
- [ ] Dengan cookie itu `GET /api/v1/auth/me` → `200`; endpoint lain (mis. `GET /api/v1/files/list` placeholder atau `/api/v1/me/preferences`) → `403 PASSWORD_CHANGE_REQUIRED`.
- [ ] `POST /auth/change-password` tanpa `X-CSRF-Token` → `403 CSRF_INVALID`; dengan token dan password baru = password awal → `400 VALIDATION_FAILED`; dengan password kuat → `200`, dan `must_change_password:false` di `/auth/me`.
- [ ] Setelah login sukses, folder `$STORAGE_ROOT/admin` ada dengan mode `0750`; login kedua tidak mengubah mtime folder.
- [ ] 5 login gagal → percobaan ke-6 `423 ACCOUNT_LOCKED` dengan `Retry-After`; 21 request/menit dari satu IP → `429`.
- [ ] Restart server dengan `ADMIN_INITIAL_PASSWORD` masih di env → password **tidak** di-reset, log warning muncul.

**Test wajib:**
- `internal/auth/password_test.go`: hash/verify, kebijakan, daftar umum.
- `internal/auth/session_test.go`: idle & absolute timeout dengan clock palsu, sliding refresh, revoke.
- `internal/httpapi/auth_test.go` (httptest + temp DB + temp STORAGE_ROOT): seed, login OK/gagal, must-change gating, CSRF, lockout bertahap, rate limit IP, provisioning idempoten & folder pre-existing, logout menghapus sesi.

**Verifikasi:** `make test && make lint && go test ./internal/auth/... -cover` (target ≥ 85 %).

**Di luar scope step ini:** UI login (Step 6), manajemen user oleh admin (19a), 2FA (23a), kuota (10a).

**Commit:** `feat(auth): admin seeding, sessions, csrf, lockout, forced password change, home provisioning`

---

## Step 4 — `fsjail` + test keamanan path dan symlink

> Sertakan Preamble di atas.

**Tujuan:** Satu package tempat SEMUA akses filesystem user terjadi, berbasis `os.Root`, tahan path traversal, symlink escape, dan TOCTOU, dengan lock manager per path; lint melarang akses `os.*` langsung di luar package ini.

**Prasyarat:** Step 3.

**Baca dulu:** SPEC §5 (seluruhnya: 5.1–5.7), §6.2 kode error `PATH_INVALID`, `NAME_INVALID`, `SYMLINK_ESCAPE`, `OPERATION_IN_PROGRESS`, §13 ADR-19; PRD epic E2, E4.

**Tugas:**
1. `internal/fsjail/path.go`: tipe `VPath` (path virtual, `/` = root user). `Normalize(raw) (VPath, error)`: tolak byte NUL dan karakter kontrol (< 0x20, 0x7F), konversi ke NFC, `path.Clean`, tolak hasil yang keluar dari root (`..` setelah clean), tolak segmen `.` kosong, batas nama ≤ 255 byte dan path ≤ 4096 byte, tolak nama `.system` di level mana pun? → hanya di root (sesuai SPEC §5.2). `ValidateName(name)` untuk create/rename: tolak `/`, NUL, kontrol, `.`/`..`, trailing spasi/titik → `warning` (bukan error) dan nama Windows-reserved (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`) → `warning`.
2. `internal/fsjail/jail.go`: `Open(absRoot string) (*Jail, error)` memakai `os.OpenRoot`; metode: `Stat`, `Lstat`, `ReadDir` (mengembalikan `fs.DirEntry` + info lstat), `Open`, `OpenFile`, `Create`, `Mkdir`, `MkdirAll`, `Rename`, `Remove`, `RemoveAll`, `Symlink(target, link)` (target wajib relatif, resolve di dalam root; selain itu `SYMLINK_ESCAPE`), `Readlink`, `Chmod` (hanya bit rwx, bukan setuid/setgid/sticky), `Chtimes`, `Sub(vpath) (*Jail, error)` via `Root.OpenRoot` untuk subtree share, `WalkDir` via `Root.FS()`, `AbsForCLI(vpath) (string, error)` — path absolut untuk argumen CLI (ffmpeg, 7zz) yang **hanya** boleh dipakai setelah `Lstat` memastikan bukan symlink dan dilakukan dalam lock.
3. Kebijakan symlink SPEC §5.4: symlink yang resolve di luar root dilaporkan `Broken=true, Reason="escape"` oleh `Lstat`/`ReadDir`, tidak pernah diikuti; semua pembukaan file memakai `os.Root` sehingga race symlink ditolak oleh kernel/`openat2`.
4. `internal/fsjail/lock.go`: `LockManager` per user: `Acquire(ctx, mode{read,write}, paths ...VPath) (release func(), err)`; konflik jika ada lock aktif pada path yang sama, ancestor, atau descendant; akuisisi dalam urutan leksikal; `TryAcquire` → `ErrLocked` → dipetakan `409 OPERATION_IN_PROGRESS`; timeout tunggu 2 s untuk operasi sinkron.
5. `internal/fsjail/fsutil.go`: `SameDevice(a, b)`, `CopyFile` (dengan `io.Copy` + `fsync` opsional + mode 0640), `CopyTree` dengan callback progres dan `ctx` cancel, `MoveOrCopy` (rename → fallback copy+delete lintas device).
6. Error mapping: `ErrInvalidPath`, `ErrInvalidName`, `ErrEscape`, `ErrLocked`, `ErrNotFound`, `ErrExists`, `ErrNotEmpty` → helper `ToAPIError()` dipakai httpapi.
7. Lint: aktifkan `forbidigo` penuh untuk `os.(Open|OpenFile|Create|Remove|RemoveAll|Rename|Mkdir|MkdirAll|ReadFile|WriteFile|Stat|Lstat|Symlink|Readlink|Chmod|Chtimes|ReadDir|DirFS)` dan `filepath.Walk(Dir)?` di semua package kecuali `internal/fsjail`, `internal/config`, `internal/db`, `internal/thumbs` (cache di DATA_DIR, bukan storage), `internal/upload` (filestore tus di DATA_DIR), `cmd/`. Refactor `internal/auth/provision.go` memakai fsjail atas `STORAGE_ROOT`.
8. Benchmark `BenchmarkReadDir10k`.

**File yang dibuat/diubah:** `backend/internal/fsjail/{path,jail,lock,fsutil,errors}.go` + `*_test.go`, `backend/internal/fsjail/testdata/`, `backend/.golangci.yml`, `backend/internal/auth/provision.go`, `docs/SECURITY.md` (bagian path & symlink).

**Acceptance criteria:**
- [ ] `go test ./internal/fsjail/ -cover` → coverage ≥ 90 %, semua kasus tabel lulus.
- [ ] `go test -race ./internal/fsjail/ -run TOCTOU -count=20` lulus (tidak pernah membuka file di luar root).
- [ ] `golangci-lint run` gagal jika ditambahkan `os.Open` percobaan di `internal/httpapi` (buktikan dengan test negatif lalu hapus).
- [ ] Symlink `link -> ../../etc/passwd` tampil `broken:true`, `Open` → `ErrEscape`.

**Test wajib:** (`jail_test.go`, `path_test.go`, `lock_test.go`)
- `../`, `/abs`, `a/../../b`, `./`, `a//b`, NUL, kontrol, NFD→NFC (`é` dua bentuk harus setara), nama 256 byte, path > 4096, `.system` di root.
- Symlink file & dir ke luar root, rantai symlink, symlink relatif valid di dalam root, hardlink (diizinkan); symlink pada komponen terakhir + trailing slash (`Open("link/")`, CVE-2026-39822) → `ErrEscape`; `Chmod` pada symlink tidak mengikuti target (CVE-2026-32282); test gagal keras jika `runtime.Version()` < go1.25.12.
- TOCTOU: goroutine mengganti folder dengan symlink keluar root selama loop `Open` → tidak ada pembacaan di luar root.
- Lock: ancestor/descendant konflik, urutan akuisisi dua goroutine (tidak deadlock), timeout.
- `MoveOrCopy` lintas device disimulasikan dengan `SameDevice` palsu.

**Verifikasi:** `go test -race -cover ./internal/fsjail/ && make lint && make test`.

**Di luar scope step ini:** Operasi tingkat tinggi (copy/move API), listing.

**Commit:** `feat(fsjail): os.Root-based jail with path validation, symlink policy, lock manager, and security tests`

---

## Step 5 — API listing (sort server, cursor, metadata), mkdir, create file, stat

> Sertakan Preamble di atas.

**Tujuan:** Endpoint listing yang cepat dan benar untuk folder besar (sort natural di server, cursor, cache), plus stat/mkdir/create; dasar untuk semua UI browsing.

**Prasyarat:** Step 4.

**Baca dulu:** SPEC §8.3 (listing & sort, algoritma natural sort, field server-side, cache, cursor), §6.1 (paging cursor), §6.3 grup `files` (list/stat/mkdir/create), §13 ADR-11; PRD epic E3.

**Tugas:**
1. `internal/listing/entry.go`: DTO `Entry` sesuai SPEC §8.3: `name`, `path`, `type` (`dir|file|symlink`), `size`, `mtime`, `ctime`, `btime` (via `statx`, `null` jika tidak didukung), `atime`, `mode` (`"rwxr-xr-x"` + oktal), `mime` (dari ekstensi untuk listing; `null` jika tidak diketahui), `type_label` (i18n key, mis. `type.image.jpeg`), `ext`, `broken` (symlink), `item_count` (folder, hanya jika murah: opsi `?counts=1` dengan batas 2.000 folder/permintaan), serta slot `note`, `tags`, `favorite`, `pinned`, `share_status`, `dimension`, `duration` (diisi step 8/10b/18b; sekarang `null`).
2. `internal/listing/sort.go`: comparator per field dari SPEC §8.3 (`name` natural via komparator kustom `NaturalLess` — run digit dibandingkan numerik, run non-digit via `x/text/collate` `IgnoreCase` locale `id`/`en` dari preferensi user; **bukan** `collate.Numeric` karena bug golang/go#67997 —, `size`, `type`, `ext`, `mtime`, `btime`, `atime`, `mode`, `note`, `dimension`, `duration`); nilai tidak diketahui selalu terakhir; tie-break nama lalu path; `folders_first` default true; item pinned paling atas (hook 10b).
3. `internal/listing/cache.go`: cache per (user_id, vpath, sort, dir, folders_first) berisi slice terurut; TTL 15 s; validasi `mtime` folder pada tiap hit; `Invalidate(user, path)` dipanggil semua mutasi (fungsi diekspos untuk step berikutnya); batas memori 20 MB per proses (LRU).
4. Cursor opaque: base64url(JSON `{f,d,k,n}`) = field, dir, nilai sort terakhir, nama terakhir; `limit` default `LISTING_PAGE_SIZE` (500), maks 1000; respons `{items, next_cursor, total, path, parent, folder: {…stat}}`.
5. Endpoint: `GET /api/v1/files/list?path=&sort=&dir=&cursor=&limit=&folders_first=&counts=`; `GET /api/v1/files/stat?path=` (satu item + `warnings`); `POST /api/v1/files/mkdir {path, name, parents?}`; `POST /api/v1/files/create {path, name, template?}` dengan template `txt|md|html|css|js|ts|json|php|py|sh|yaml` dari `internal/files/templates/*` (konten kecil, tanpa secret); respons `201` + entry; konflik → `409 ALREADY_EXISTS`.
6. Semua endpoint di atas memakai `RequireAuth + RequirePasswordChanged`; `path` divalidasi `fsjail.Normalize`; jail dibuka per request dari `STORAGE_ROOT/<username>` (helper `userJail(ctx)` dengan cache handle per user, ditutup saat idle 5 menit).
7. `internal/files/types.go`: tabel ekstensi → MIME + label manusiawi (gambar, video, audio, arsip, kode, dokumen, spreadsheet, PDF, teks) dipakai listing dan ikon di UI.
8. Fixture generator `internal/listing/testdata/gen.go` membuat 10.000 entri (nama campuran `file1…file10000`, Unicode, folder) di temp dir untuk test & benchmark.
9. `docs/openapi.yaml`: tambah endpoint di atas + skema `Entry`, `ListResponse`.

**File yang dibuat/diubah:** `backend/internal/listing/{entry,sort,cache,cursor,service}.go` + test, `backend/internal/files/{types,templates.go,templates/*}`, `backend/internal/httpapi/files.go`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Folder berisi `file1, file2, file10, File3, é.txt` → `sort=name` menghasilkan `file1, file2, File3, file10, é.txt` (natural, case-insensitive, locale-aware).
- [ ] Unit test `NaturalLess`: `0.0` < `1.0`, `a-21` < `a-123`, `file2` < `file10`, `01` vs `1` deterministik (tie-break byte), nama NFD/NFC setara; tidak ada pemakaian `collate.Numeric` di kode (`grep` di CI).
- [ ] `limit=2` → `next_cursor` ada; request kedua dengan cursor melanjutkan tanpa duplikat/lompat meski ada file baru ditambahkan di antaranya.
- [ ] `path=../x` atau `%00` → `400 PATH_INVALID`; `path=/.system` → `404 NOT_FOUND`.
- [ ] `go test -bench ListDir10k` → p95 < 500 ms pada 10.000 entri (dicatat di PROGRESS).
- [ ] `POST mkdir` nama `con` → `201` dengan `warnings:["windows_reserved"]`; nama `a/b` → `400 NAME_INVALID`.

**Test wajib:**
- `internal/listing/sort_test.go`: tiap field, unknown-last, folders_first, stabilitas.
- `internal/listing/cursor_test.go`: encode/decode, cursor rusak → `400 VALIDATION_FAILED`.
- `internal/listing/cache_test.go`: TTL, invalidasi, mtime change.
- `internal/httpapi/files_list_test.go`: integrasi list/stat/mkdir/create dengan temp root, path traversal, `.system` tersembunyi, user B tidak melihat root user A.
- Benchmark 10k.

**Verifikasi:** `make test && make lint && go test -bench=. -run=^$ ./internal/listing/`.

**Di luar scope step ini:** Rename/copy/move/delete (9a), thumbnail (8), metadata (10b).

**Commit:** `feat(listing): server-side natural sort, cursor paging, stat, mkdir, create`

---

## Step R-A — Review independen Step 1–5

> Sertakan Preamble di atas (reviewer tidak mengimplementasi; hanya menulis temuan dan — pada fase perbaikan — commit perbaikan terpisah).

**Tujuan:** Reviewer di sesi baru tanpa konteks implementasi memeriksa fondasi (config, DB, auth, fsjail, listing) terhadap PRD/SPEC, mencari bug, celah keamanan, dan penyimpangan desain sebelum UI dibangun di atasnya.

**Prasyarat:** Step 1–5 tercentang di PROGRESS.md.

**Baca dulu:** PRD epic E1, E2, E3; SPEC §3, §4, §5, §6, §8.1–8.3, §10; PROGRESS.md catatan Step 1–5.

**Tugas:**
1. Jalankan `make lint && make test && go test -race -cover ./...`; catat coverage `internal/fsjail`, `internal/auth`.
2. Baca kode (bukan hanya diff) `internal/config`, `internal/auth`, `internal/fsjail`, `internal/listing`, `internal/httpapi` dan bandingkan dengan SPEC §3, §5, §8.1, §8.3.
3. Checklist keamanan R-A: (a) adakah `os.*` langsung di luar pengecualian lint; (b) `Normalize` menolak NUL, NFD, `..`, path absolut, `.system`; (c) symlink escape & TOCTOU — coba fixture baru di luar yang sudah ada; (d) cookie flag, idle/absolute, CSRF `Sec-Fetch-Site`; (e) lockout tidak bisa dilewati dengan variasi huruf username/spasi; (f) seed admin tidak bisa dipicu ulang; (g) `must_change_password` menutup semua endpoint non-whitelist; (h) tidak ada secret di log (jalankan dengan `LOG_LEVEL=debug` dan grep); (i) listing tidak membocorkan path absolut server di respons/error; (j) user B tidak bisa list root user A (coba `path=../A`, `%2e%2e`, overlong UTF-8).
4. Checklist kualitas: error envelope konsisten, kode error sesuai SPEC §6.2, openapi.yaml sinkron dengan handler (bandingkan daftar route chi dengan paths openapi), test menguji perilaku bukan implementasi, tidak ada TODO yang menyembunyikan AC.
5. Tulis `docs/reviews/review-A.md`: ringkasan, tabel temuan (ID, prioritas blocker/major/minor, lokasi `file:line`, deskripsi, bukti/reproduksi, saran), daftar yang diverifikasi OK.
6. Fase perbaikan (boleh sesi yang sama atau sesi implementasi baru): tutup semua blocker & major, tambah test regresi per temuan, commit `fix(review-A): …`; update status temuan di review-A.md; minor boleh ditunda dengan dicatat di PROGRESS "Catatan lintas step".

**File yang dibuat/diubah:** `docs/reviews/review-A.md`, perbaikan kode + test regresi.

**Acceptance criteria:**
- [ ] `docs/reviews/review-A.md` ada dengan ≥ 10 butir yang diperiksa dan status tiap temuan.
- [ ] Tidak ada temuan blocker/major berstatus terbuka.
- [ ] Semua test lulus setelah perbaikan; coverage fsjail tetap ≥ 90 %.

**Test wajib:** Test regresi untuk setiap temuan blocker/major.

**Verifikasi:** `make lint && make test && go test -race ./...`.

**Di luar scope step ini:** Fitur baru, refactor gaya.

**Commit:** `docs(review): independent review A` lalu `fix(review-A): …`

---

## Step 6 — Frontend shell: Vite/React, router, design token, light/dark, i18n, `/dev/design`, login & ganti password

> Sertakan Preamble di atas.

**Tujuan:** Fondasi UI yang dipakai semua layar: token warna/tipografi, komponen shadcn/ui yang dikustom, tema tanpa flash, i18n id/en, routing, klien API, serta halaman login dan ganti password yang terhubung ke backend.

**Prasyarat:** Step 3 (auth API), Step 1 (scaffold web). Boleh dikerjakan paralel dengan Step 4–5 jika tim terpisah, tetapi urutan default setelah R-A.

**Baca dulu:** SPEC §9.1 (token), §9.2 (inventaris komponen), §9.4 (route), §9.7 (state), §9.8 (konvensi i18n), §9.9 (responsif & a11y), §13 ADR-21; PRD epic E13, E1.

**Tugas:**
1. Tailwind: theme memakai CSS variables dari SPEC §9.1 (`--bg`, `--surface`, `--surface-2`, `--border`, `--text`, `--text-muted`, `--accent`, `--accent-subtle`, `--success`, `--warning`, `--danger`) untuk light/dark + 6 warna aksen (biru default, emerald, violet, amber, rose, slate) via `data-accent`; skala font 12/13/14/16/20, weight 400/500/600, radius 4/6/8, spasi grid 4 px, motion 120–180 ms + `prefers-reduced-motion`. Font Inter & JetBrains Mono self-host di `web/public/fonts` (lisensi OFL disertakan).
2. Tema tanpa flash: skrip eksternal kecil `theme-init.js` (bukan inline, agar CSP tanpa `unsafe-inline` untuk script) membaca `localStorage.theme` (`light|dark|system`) dan men-set `data-theme` sebelum render; toggle di top bar; preferensi juga disimpan server (Step 7b).
3. shadcn/ui (Radix) komponen: button (varian + ikon 32 px dengan tooltip + shortcut), input, label, checkbox, select, dialog, sheet, dropdown-menu, context-menu, tooltip, popover, tabs, badge, progress, skeleton, separator, scroll-area, command (cmdk), toast (sonner, posisi tidak menutupi panel transfer: kiri bawah), table primitives, breadcrumb primitives, empty-state. Semua dikustom ke token; tidak memakai warna default shadcn.
4. Ikon: lucide-react untuk UI; set ikon tipe file sendiri di `web/src/components/file-icon/` (SVG, warna per tipe sesuai SPEC §9.1: folder amber, gambar violet, video rose, audio pink, arsip oranye, kode sky, PDF merah, dokumen biru, spreadsheet hijau, teks slate).
5. i18n: i18next + react-i18next, namespace per fitur (`common`, `auth`, `files`, `editor`, `viewer`, `share`, `admin`, …) di `web/src/i18n/{id,en}/*.json`; `id` default; format tanggal/angka via `Intl` sesuai locale (id-ID: "12 Okt 2026", "1,2 MB"); util `formatBytes`, `formatRelativeTime` dengan tooltip absolut; lint script yang gagal jika ada key hilang di salah satu bahasa.
6. TanStack Router: semua route SPEC §9.4 sebagai placeholder (`/login`, `/change-password`, `/files/*`, `/recent`, `/favorites`, `/shared-with-me`, `/shares`, `/trash`, `/activity`, `/storage`, `/search`, `/editor`, `/settings`, `/admin/*`, `/s/:token/*`, `/dev/design`), guard: belum login → `/login?next=`; `must_change_password` → `/change-password`.
7. Klien API `web/src/lib/api.ts`: `fetch` wrapper dengan `credentials: same-origin`, header `X-CSRF-Token` (diambil dari `/auth/csrf` sekali dan di-refresh saat `403 CSRF_INVALID`), parse envelope error ke `ApiError {code, message, details, status}`, 401 → redirect login, tipe respons dari skema (generate tipe dari `docs/openapi.yaml` dengan `openapi-typescript` di script `pnpm gen:api`). TanStack Query client dengan default `staleTime` 10 s, retry 1 (tidak retry 4xx).
8. Zustand store: `session` (user, csrf), `ui` (tema, aksen, density, sidebar collapsed).
9. Halaman login: form react-hook-form + zod, pesan error generik (tidak membedakan username/password salah), tampilan lockout `Retry-After`, caps-lock hint, fokus awal; halaman ganti password: aturan (≥ 12, tidak sama lama, indikator kekuatan sederhana), setelah sukses → `/files`.
10. `/dev/design`: menampilkan semua token (swatch + nilai + rasio kontras), tipografi, tiap komponen dalam light & dark berdampingan, ikon tipe file; hanya di-build jika `import.meta.env.DEV` atau flag `VITE_DESIGN_PAGE=1` (tidak masuk bundle produksi).
11. `web/scripts/check-contrast.mjs`: menghitung rasio WCAG untuk pasangan token (text/bg, text-muted/bg, accent/bg, text/surface-2, white/accent) di light & dark; gagal jika < 4.5:1 (teks) / 3:1 (UI); dijalankan di CI.
12. Playwright: config (`web/e2e/`), fixture backend (jalankan binary dengan temp `DATA_DIR`/`STORAGE_ROOT` dan admin seed), test `auth.spec.ts`: login admin → dipaksa ganti password → masuk `/files` → logout.
13. Bundle budget: script `web/scripts/bundle-size.mjs` mengukur gzip JS awal (< 300 KB) — dilaporkan, gagal keras mulai Step 21.

**File yang dibuat/diubah:** `web/tailwind.config.ts`, `web/src/styles/{tokens.css,globals.css}`, `web/public/theme-init.js`, `web/public/fonts/*`, `web/src/components/ui/*`, `web/src/components/file-icon/*`, `web/src/i18n/*`, `web/src/routes/*`, `web/src/lib/{api,format,query}.ts`, `web/src/stores/*`, `web/src/features/auth/*`, `web/src/routes/dev/design.tsx`, `web/scripts/{check-contrast,bundle-size,i18n-check}.mjs`, `web/e2e/*`, `web/playwright.config.ts`, `.github/workflows/ci.yml` (job e2e).

**Acceptance criteria:**
- [ ] `pnpm dev` + backend: buka `/login`, login admin → diarahkan `/change-password` → setelah ganti → `/files` (placeholder) dengan nama user di top bar.
- [ ] Toggle tema light/dark/system tanpa flash saat reload (verifikasi: `document.documentElement.dataset.theme` sudah benar sebelum first paint; tidak ada `unsafe-inline` script).
- [ ] `/dev/design` menampilkan semua token dan komponen; `node web/scripts/check-contrast.mjs` lulus (semua pasangan ≥ 4.5:1 atau 3:1 sesuai jenis).
- [ ] Ganti bahasa ke `en` mengubah semua string halaman login; `pnpm i18n:check` lulus.
- [ ] `pnpm e2e` `auth.spec.ts` lulus; `pnpm build` sukses; ukuran JS awal gzip tercatat di PROGRESS.

**Test wajib:** Vitest: `format.test.ts` (bytes/tanggal id & en), `api.test.ts` (envelope error, CSRF refresh), `LoginForm.test.tsx` (validasi, pesan generik); Playwright `auth.spec.ts`.

**Verifikasi:** `pnpm lint && pnpm typecheck && pnpm test && pnpm build && pnpm e2e`.

**Di luar scope step ini:** File browser (7a), preferensi server (7b).

**Commit:** `feat(web): design tokens, ui kit, i18n, router, api client, login and change-password pages`

---

## Step 7a — File browser inti: layout, sidebar, address bar, riwayat, Details view tervirtualisasi, status bar

> Sertakan Preamble di atas.

**Tujuan:** Layar utama file manager dengan navigasi lengkap dan tabel Details yang mulus pada 10.000 item.

**Prasyarat:** Step 5, Step 6.

**Baca dulu:** SPEC §9.3 (layout layar utama & wireframe), §8.3 (listing API), §8.4 (hanya bagian Details), §9.7 (empty/loading/error), §9.9; PRD epic E3.

**Tugas:**
1. Layout `web/src/features/browser/BrowserLayout.tsx` persis dimensi SPEC §9.3: top bar 48 px (logo, pencarian global Ctrl+K placeholder, Tasks(n) placeholder, tema, menu user), sidebar (File Saya, Recent, Favorit, Dibagikan ke saya, Share saya, Recycle Bin, Aktivitas, Analisis penyimpanan, Admin jika role admin; indikator kuota placeholder "—"), address bar 40 px, toolbar 40 px (tombol placeholder yang aktif di 9b), area konten, status bar 28 px.
2. Address bar: breadcrumb dengan dropdown folder sibling per segmen (lazy dari `/files/list` folder induk), collapse saat overflow (segmen tengah → `…`), klik area kosong → mode ketik path (`Alt+D`), Enter → navigasi, Esc batal; tombol back/forward/up/refresh; riwayat per tab (stack) dengan `Alt+←/→`, `Backspace`/`Alt+↑`, `F5` refresh (preventDefault), sinkron URL `/files/*`.
3. Details view: TanStack Table + TanStack Virtual, baris 36 px, header 32 px, kolom default Nama (ikon + nama), Ukuran (folder: jumlah item jika ada, else `—`), Tipe (label i18n), Diubah (relatif + tooltip absolut, `tabular-nums`), Catatan; infinite scroll via `next_cursor` (prefetch saat 80 % scroll); double-click/Enter folder → masuk; file → placeholder "viewer belum tersedia" (toast) sampai Step 17.
4. State per tab di Zustand (`tabs` store: id, path, history, scroll offset) — satu tab dulu; multi-tab di 7b.
5. Query key `['list', path, sort, dir, foldersFirst]`, cache hangat → navigasi kembali < 150 ms (data dari cache + revalidate).
6. Empty state (ikon kecil + satu kalimat + tombol "Upload" placeholder), skeleton saat loading pertama, error state dengan kode error dan tombol coba lagi; 403 `PASSWORD_CHANGE_REQUIRED` → redirect.
7. Status bar: `N item`, `M dipilih (ukuran)` (seleksi di 7b → sekarang 0), kuota placeholder.
8. Aksesibilitas dasar: tabel `role="grid"`, baris `role="row"`, fokus ring, `aria-label` pada tombol ikon, live region `aria-live="polite"` untuk pengumuman navigasi ("Membuka folder X, 124 item").
9. Responsif: ≥ 1280 penuh; 768–1279 sidebar ikon; < 768 sidebar drawer (sheet), kolom Details hanya Nama + Diubah.
10. E2E `browser-nav.spec.ts`: buat 3 folder bersarang via API, navigasi klik, breadcrumb, back/forward/up, ketik path, URL sinkron, 10.000 file (fixture via API `create` batch atau langsung di filesystem test) scroll ke bawah tanpa jank (ukur `performance.now` render pertama < 1 s).

**File yang dibuat/diubah:** `web/src/features/browser/{BrowserLayout,Sidebar,TopBar,AddressBar,Breadcrumb,Toolbar,StatusBar,DetailsView,useListing,columns}.tsx`, `web/src/stores/tabs.ts`, `web/src/routes/files.tsx`, `web/src/i18n/*/files.json`, `web/e2e/browser-nav.spec.ts`.

**Acceptance criteria:**
- [ ] Navigasi folder lewat klik, breadcrumb, dropdown sibling, ketik path, back/forward/up, F5 — semuanya memperbarui URL dan isi.
- [ ] Folder 10.000 item: render pertama < 1 detik, scroll 60 fps (tidak ada long task > 100 ms di Performance panel; dicatat).
- [ ] Kembali ke folder yang baru dikunjungi tampil instan dari cache lalu revalidate.
- [ ] Lebar 375 px: sidebar menjadi drawer, kolom hanya Nama + Diubah, tidak ada scroll horizontal.
- [ ] Keyboard: Tab mencapai semua kontrol; fokus ring terlihat.

**Test wajib:** Vitest `Breadcrumb.test.tsx` (collapse, sibling), `AddressBar.test.tsx` (ketik path, validasi), `useListing.test.ts` (cursor merge); Playwright `browser-nav.spec.ts`.

**Verifikasi:** `pnpm lint && pnpm typecheck && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Kolom tambahan & sort UI (7b), seleksi, context menu, view lain.

**Commit:** `feat(browser): main layout, navigation, breadcrumb, virtualized details view`

---

## Step 7b — Kolom, sort per kolom, seleksi, keyboard, filter cepat, type-ahead, tab folder, panel preview

> Sertakan Preamble di atas.

**Tujuan:** Details view setara aaPanel dan lebih: kolom bisa dipilih/resize/reorder, sort per kolom (+ sekunder), model seleksi lengkap, navigasi keyboard, tab folder, panel preview, semuanya tersimpan per user di server.

**Prasyarat:** Step 7a.

**Baca dulu:** SPEC §8.3 (sort), §8.4 (kolom), §8.18 (preferensi: kunci `view`, `columns`, `sort`, `folder_view_prefs`), §6.3 grup `me`, §9.5 (seleksi & interaksi), §9.6 (shortcut final), §9.2 (command palette); PRD epic E3, E13.

**Tugas:**
1. Backend: `GET/PUT /api/v1/me/preferences` (JSON map kunci → nilai, validasi skema per kunci, maks 64 KB), `GET/PUT/DELETE /api/v1/me/folder-prefs?path=` (`folder_view_prefs`: view, sort, dir, columns; maks 500 entri per user, LRU). Tabel sudah ada sejak Step 2.
2. Menu "Kolom" (toolbar + klik kanan header): semua kolom SPEC §8.4 — Nama (wajib), Ukuran, Tipe, Diubah, Catatan, Dibuat, Diakses, Ekstensi, Permission (`rwxr-xr-x` + `755` mono), Jumlah item, Dimensi, Durasi, Status share, Lokasi (hanya hasil pencarian/Recent). Resize (drag), reorder (drag header), reset default. Disimpan ke preferences (debounce 500 ms).
3. Sort: klik header asc → desc → default; Shift+klik = sort sekunder (indikator angka kecil); dropdown "Urutkan berdasarkan" (dipakai view grid di Step 8); opsi "Folder di atas"; perubahan sort memanggil API ulang (server-side).
4. Seleksi: klik, Ctrl/Cmd+klik, Shift+klik rentang (anchor), Ctrl+A, Ctrl+I invert, Esc bersihkan, kolom checkbox (header = semua/sebagian/tidak), store `selection` (Set path) per tab; status bar menampilkan jumlah & total ukuran terpilih.
5. Keyboard (ARIA grid roving tabindex): ↑/↓, Home/End, PageUp/PageDown, Shift+panah memperluas, Enter buka, Space Quick Look (placeholder sampai 17b), Alt+Enter Properties (placeholder sampai 10a), type-ahead (ketik huruf → lompat ke item yang cocok, reset 1 s), `?` membuka dialog daftar shortcut.
6. Filter cepat: kotak "Filter…" di address bar menyaring item yang sudah dimuat di klien (nama, case-insensitive); jika hasil listing belum lengkap (ada `next_cursor`), tampilkan tombol "Cari di server" → Step 15.
7. Tab folder: bar tab di bawah address bar (maks 10; `Buka di tab baru` dari context menu di 9b; menu tab: tutup, tutup lainnya, tutup di kanan), drag reorder, tab + riwayat tersimpan di preferences dan dipulihkan saat reload; Ctrl+Tab berpindah tab (jika dapat dicegat; fallback Alt+1…9).
8. Panel kanan Preview/Details (toggle `Alt+P`, lebar 320 px, di < 1280 menjadi overlay): untuk 1 item → ikon besar/thumbnail (placeholder sampai 8), nama, tipe, ukuran, tanggal, permission, path; untuk banyak → ringkasan jumlah & ukuran; tombol aksi cepat (placeholder sampai 9b).
9. Command palette `Ctrl+K` (cmdk): bagian "Lompat ke" (folder dari riwayat & favorit), "Aksi" (registry aksi — sekarang: refresh, ganti view, tema, buka tab), "Cari file" (placeholder Step 15). Registry aksi `web/src/lib/actions.ts` dipakai juga toolbar & context menu di 9b (satu sumber: id, label i18n, ikon, shortcut, `isApplicable(selection)`, `run`).
10. Shortcut manager `web/src/lib/shortcuts.ts`: daftar SPEC §9.6; tidak aktif saat fokus di input/editor; daftar terlarang (Ctrl+N/T/W/L/D, Ctrl+Shift+N/C) tidak dipakai.
11. E2E `details-view.spec.ts`: ubah kolom & sort → reload → tersimpan; seleksi shift/ctrl; keyboard navigasi; tab folder dipulihkan.

**File yang dibuat/diubah:** `backend/internal/httpapi/me.go`, `backend/internal/db/queries/prefs.sql`, `web/src/features/browser/{ColumnMenu,SortMenu,SelectionModel,useGridKeyboard,QuickFilter,TabBar,PreviewPanel,ShortcutHelp}.tsx`, `web/src/lib/{actions,shortcuts}.ts`, `web/src/features/command-palette/*`, `web/src/stores/{selection,prefs}.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Pilihan kolom, lebar, urutan, sort (primer + sekunder) bertahan setelah reload dan login ulang (tersimpan di server, bukan hanya localStorage).
- [ ] Shift+klik memilih rentang; Ctrl+A memilih semua termasuk yang belum di-render (virtual) — status bar menampilkan jumlah total.
- [ ] Type-ahead "fo" melompat ke item pertama yang diawali "fo".
- [ ] Buka 10 tab → percobaan tab ke-11 ditolak dengan toast; tab dipulihkan setelah reload.
- [ ] `?` menampilkan semua shortcut sesuai SPEC §9.6; tidak ada shortcut dari daftar terlarang.
- [ ] Axe pada `/files`: 0 pelanggaran serius.

**Test wajib:** Vitest `SelectionModel.test.ts` (rentang, invert, anchor), `shortcuts.test.ts` (konflik, konteks input), `sortCycle.test.ts`; Playwright `details-view.spec.ts`.

**Verifikasi:** `make test && pnpm lint && pnpm typecheck && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Context menu & operasi (9b), view grid (8).

**Commit:** `feat(browser): column management, sorting, selection model, keyboard navigation, folder tabs, preview panel`

---

## Step 8 — View List, Tiles, Large icons, Extra large icons + pipeline thumbnail + media_metadata

> Sertakan Preamble di atas.

**Tujuan:** Lima mode view lengkap dengan thumbnail yang dibuat di server (worker terbatas, cache, ETag) dan cache metadata media untuk kolom Dimensi/Durasi.

**Prasyarat:** Step 7b.

**Baca dulu:** SPEC §8.4 (views & pipeline thumbnail), §4.2 tabel `media_metadata`, §6.3 `files/thumb`, §8.9 (signed URL — bagian `purpose=thumb`), §13 ADR-09, ADR-10, ADR-15; PRD epic E3, E8.

**Tugas:**
1. Migrasi `0004_media_metadata.sql` (SPEC §4.2: user_id, path, size, mtime, kind, width, height, duration_ms, codec, has_thumb, exif_json, tags_json, updated_at; unique (user_id, path)).
2. `internal/exec`: helper `Run(ctx, bin, args...)` — tanpa shell, `--` sebelum path, `nice -n 10` + `ionice -c3` jika tersedia, timeout, batas output, `DEPENDENCY_MISSING` jika binary tidak ada. Dipakai semua step berikutnya.
3. `internal/thumbs`: worker pool `THUMB_WORKERS` (server ini = 1), antrian prioritas (permintaan terbaru/visible lebih dulu; dedup per key), generator per jenis: gambar → `vipsthumbnail` (jpg/png/webp/gif frame 1/bmp/tiff/avif/heic jika didukung — deteksi via `vips --list`? → cukup coba & tandai `unsupported` di cache), video → `ffmpeg -ss <10 % durasi> -frames:v 1`, PDF → `pdftoppm -f 1 -l 1 -png`, audio → cover dari `ffmpeg -an -c:v copy`, SVG → rasterisasi `vipsthumbnail` (tidak pernah inline). Output WebP 128/256/512 di `DATA_DIR/thumbs/<user_id>/<sha256(path|size|mtime)>-<size>.webp`; hasil gagal dicatat (`has_thumb=0`) agar tidak diulang sebelum mtime berubah.
4. `GET /api/v1/files/thumb?path=&size=128|256|512&sig=&exp=` (signed, `purpose=thumb`, TTL `MEDIA_URL_TTL`): `200 image/webp` + `ETag` + `Cache-Control: private, max-age=3600` (+ `304`), `202 Retry-After: 2` jika sedang diantrekan, `404` jika tidak didukung (klien memakai ikon). Batas 20 request thumb paralel per user.
5. `internal/media`: `Probe(path)` memakai `ffprobe -print_format json -show_format -show_streams` (durasi, dimensi, codec, tag judul/artis/album) dan `image.DecodeConfig` untuk dimensi gambar cepat; hasil ke `media_metadata`; dipicu saat thumbnail dibuat dan saat listing meminta kolom Dimensi/Durasi (lazy, batch 50 per request listing, sisanya diantrekan).
6. Listing: isi `dimension` & `duration` dari cache; sort `dimension`/`duration` (unknown last). Invalidasi thumbnail & metadata saat mutasi (hook `OnChange(user, path)` dipanggil 9a/12a/14a).
7. Frontend views: List (ikon 16 px + nama, aliran multi-kolom atas→bawah lalu kiri→kanan, tinggi kolom = viewport, virtualisasi per kolom), Tiles (grid 48 px ikon/thumbnail + nama + 2 baris meta), Large icons (96–128 px, nama maks 2 baris ellipsis tengah), Extra large icons (192–256 px). Pemilih view di toolbar + `Ctrl+1…5`; view disimpan per user + "ingat view per folder".
8. Thumbnail klien: `IntersectionObserver`, antrian maks 6 request paralel, placeholder ikon tipe, retry pada `202` dengan backoff, `<img loading="lazy" decoding="async">`, URL thumb ditandatangani batch via `POST /api/v1/files/sign` (array path, purpose thumb) → mengurangi request.
9. Marquee selection (drag di area kosong) di view grid; Shift/Ctrl bekerja sama seperti Details; keyboard grid 2D (←/→/↑/↓ sesuai kolom).
10. Dropdown "Urutkan" di view non-Details dengan field & arah sama.
11. `check-deps` + `/readyz` menandai dependency media hilang sebagai `degraded:false, warnings:[…]` (bukan gagal); UI menampilkan ikon jika thumbnail tidak tersedia.

**File yang dibuat/diubah:** `backend/migrations/0004_media_metadata.sql`, `backend/internal/exec/*`, `backend/internal/thumbs/*`, `backend/internal/media/*`, `backend/internal/httpapi/{thumbs,sign}.go`, `backend/internal/httpapi/sign/*` (HMAC signed URL, dipakai lagi 12b), `web/src/features/browser/views/{ListView,TilesView,LargeIconsView,XLIconsView,useThumb,Marquee}.tsx`, `web/src/features/browser/ViewSwitcher.tsx`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Folder berisi jpg, png, gif, webp, svg, mp4, mp3 (dengan cover), pdf: di Large icons semua menampilkan thumbnail (SVG sebagai raster), yang gagal menampilkan ikon tipe.
- [ ] Request thumbnail kedua → `304` dengan `If-None-Match`; mengganti file (mtime berubah) → thumbnail baru.
- [ ] `THUMB_WORKERS=1`: 200 thumbnail diproses serial tanpa RSS backend melebihi +150 MB (ukur `ps`/pprof; catat).
- [ ] Kolom Dimensi/Durasi terisi untuk media; sort Durasi menaruh file tanpa durasi di akhir.
- [ ] Tanpa `ffmpeg` terpasang: video/audio tampil ikon, tidak ada error 500, `/readyz` tetap 200 dengan warning.
- [ ] Marquee memilih item yang beririsan; Ctrl+1…5 mengganti view; "ingat view per folder" bekerja.

**Test wajib:** Go: `thumbs/queue_test.go` (dedup, prioritas, worker limit), `thumbs/keys_test.go`, `media/probe_test.go` (parse JSON ffprobe fixture), `httpapi/thumb_test.go` (sig, ETag/304, 202, 404, batas paralel); Vitest `useThumb.test.ts`; Playwright `views.spec.ts` (5 view, marquee).

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Viewer media (17), EXIF panel (17a).

**Commit:** `feat(views): list/tiles/icon views, server-side thumbnail pipeline, media metadata cache`

---

# Milestone M2 — Operasi file & transfer

## Step 9a — Operasi file backend: rename, copy, move, duplicate, symlink, chmod, batch rename, delete → Recycle Bin, konflik, lock, undo

> Sertakan Preamble di atas.

**Tujuan:** Semua mutasi file inti tersedia lewat API, aman (fsjail + lock), konsisten (referensi metadata ikut berpindah dalam satu transaksi), dan bisa di-undo.

**Prasyarat:** Step 8 (hook invalidasi), Step 4.

**Baca dulu:** SPEC §8.5 (operasi file: aturan, konflik, validasi, batch rename, undo), §8.6 (Recycle Bin — hanya mekanisme pindah ke trash), §5.5 (lock), §5.6 (lintas FS), §5.7 (update referensi metadata), §6.3 grup `files` (rename/copy/move/duplicate/symlink/chmod/delete/batch-rename/undo), §13 ADR-17, ADR-19; PRD epic E4, E11.

**Tugas:**
1. Migrasi `0005_trash_meta.sql`: `trash_items`, `activity_logs`, dan tabel referensi metadata yang dipengaruhi rename/move: `favorites`, `pins`, `file_notes`, `file_tags`, `file_versions` (kolom sesuai SPEC §4.2; isi datanya oleh 10b/16b) — agar `UpdateReferences` bisa diuji sekarang.
2. `internal/files/ops.go`: `Rename`, `Copy` (file & folder rekursif; progres via callback; `ctx` cancel; hasil parsial dibersihkan saat cancel), `Move` (rename atomik jika same device; else copy+delete), `Duplicate` (nama `nama (1).ext`, `(2)` dst.), `Symlink` (target relatif, resolve di dalam root), `Chmod` (mode rwx owner/group/public + rekursif; setuid/setgid/sticky selalu dibuang), `Delete` → trash: `rename` ke `DATA_DIR/trash/<user_id>/<trash_item_id>` + baris `trash_items` (original_path, name, is_dir, size, deleted_at, expires_at = +`TRASH_RETENTION_DAYS`); cross-FS (`cfg.CrossFSTrash`) → copy+delete dengan warning log.
3. Kebijakan konflik param `on_conflict=overwrite|skip|rename` (default `fail` → `409 ALREADY_EXISTS` + `details:{existing:{size,mtime}, incoming:{size,mtime}}` agar UI menampilkan perbandingan); `overwrite` folder → merge rekursif dengan kebijakan yang sama per file.
4. Validasi: tidak bisa memindah/menyalin folder ke dalam dirinya sendiri atau turunannya (`400 VALIDATION_FAILED`), nama via `fsjail.ValidateName` (warning Windows-reserved diteruskan di respons), tidak bisa menyentuh `.system`.
5. Batas sinkron: copy/move ≤ 200 MB dan ≤ 2.000 entri dijalankan sinkron; lebih besar → `202` dengan `task_id` (task engine Step 11; sampai itu kembalikan `413 PAYLOAD_TOO_LARGE` dengan pesan "butuh task engine" dan catat di PROGRESS).
6. Batch rename: `POST /api/v1/files/batch-rename/preview {paths, rule}` rule = `{find, replace, regex?, case_insensitive?, numbering:{pattern:"{n}", start, step, pad}, case:"upper|lower|title|none", ext_mode:"keep|include"}` → daftar `{from, to, status: ok|collision|invalid}`; `POST /api/v1/files/batch-rename` menerapkan hanya jika preview tanpa collision (server menghitung ulang, tidak percaya klien); regex memakai RE2 (`regexp`), timeout.
7. `UpdateReferences(tx, user, oldPath, newPath)` memperbarui prefix path di `favorites`, `pins`, `file_notes`, `file_tags`, `file_versions`, `shares` (tabel shares dibuat 18a → gunakan interface `ReferenceUpdater` yang didaftarkan per package agar 18a cukup mendaftar), `trash_items.original_path` tidak diubah. Satu transaksi dengan operasi fs: urutan = lock → fs op → tx update → commit; jika tx gagal → coba rollback fs (rename balik) dan log error.
8. Undo: setiap operasi mengembalikan `undo_token` (acak, disimpan in-memory per user, umur 60 s, satu kali pakai) + `undo:{kind, …}`; `POST /api/v1/files/undo {token}` menjalankan inverse (rename ↔ rename, move ↔ move balik, delete → restore dari trash by id, duplicate/copy → delete ke trash). Operasi yang tidak bisa di-undo (chmod, overwrite) mengembalikan `undo:null`.
9. `activity_logs`: satu baris per operasi (action, path, target, size, details JSON) untuk halaman Aktivitas (10b) dan Recent.
10. Semua endpoint memanggil `listing.Invalidate` dan `thumbs.OnChange`.
11. `docs/openapi.yaml` diperbarui.

**File yang dibuat/diubah:** `backend/migrations/0005_trash_meta.sql`, `backend/internal/files/{ops,copy,conflict,batchrename,refs,undo,activity}.go` + test, `backend/internal/trash/store.go` (fungsi pindah ke trash; UI/restore di 10a), `backend/internal/httpapi/files_ops.go`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] `POST /files/rename {path:"/a.txt", new_name:"b.txt"}` → `200` + `undo_token`; `POST /files/undo` → file kembali `a.txt`.
- [ ] `POST /files/move {paths:["/dir"], to:"/dir/sub"}` → `400 VALIDATION_FAILED`.
- [ ] `POST /files/copy` dengan tujuan sudah ada tanpa `on_conflict` → `409 ALREADY_EXISTS` berisi `existing`/`incoming`; dengan `on_conflict=rename` → `b (1).txt`.
- [ ] `POST /files/delete {paths:[...]}` → item hilang dari listing, muncul di `trash_items`, file fisik ada di `DATA_DIR/trash/<uid>/<id>`.
- [ ] Rename folder `/proyek` → `/proyek2` memperbarui `favorites`/`file_notes` yang path-nya diawali `/proyek/` dalam satu transaksi (cek DB).
- [ ] Dua request move pada path yang sama secara bersamaan: satu `200`, satu `409 OPERATION_IN_PROGRESS`.
- [ ] Batch rename preview mendeteksi collision; apply dengan collision → `409`.

**Test wajib:** `files/ops_test.go` (setiap operasi + konflik + lintas FS tersimulasi), `files/batchrename_test.go` (regex, numbering, case, collision), `files/refs_test.go`, `files/undo_test.go` (kedaluwarsa, sekali pakai), `httpapi/files_ops_test.go` (integrasi + lock + IDOR user A/B).

**Verifikasi:** `make test && make lint && go test -race ./internal/files/...`.

**Di luar scope step ini:** UI (9b), restore/purge trash UI & auto-purge (10a), task besar (11).

**Commit:** `feat(files): core file operations with conflict policy, metadata reference updates, path locks, and undo`

---

## Step 9b — Operasi file frontend: context menu, toolbar, clipboard, dialog pindah/salin, konflik, drag & drop, rename inline, batch rename, undo toast

> Sertakan Preamble di atas.

**Tujuan:** Semua operasi Step 9a bisa dilakukan lewat toolbar, klik kanan, dan keyboard, dengan dialog konflik dan undo 10 detik.

**Prasyarat:** Step 9a.

**Baca dulu:** SPEC §9.5 (matriks context menu — aksi yang tidak berlaku disembunyikan), §9.6 (shortcut), §8.5, §9.2 (dialog, folder picker, toast); PRD epic E4.

**Tugas:**
1. Registry aksi (`web/src/lib/actions.ts`) diisi semua aksi matriks SPEC §9.5 untuk konteks 1 file / 1 folder / banyak item / area kosong; aksi yang belum ada backend-nya (Share, Compress, Checksum, Riwayat versi, dll.) didaftarkan dengan `available:false` sehingga **tidak tampil** sampai step pemiliknya mengaktifkan.
2. Context menu (Radix) pada baris/item/area kosong, toolbar kontekstual (aksi utama + menu "More ⋯"), semua dengan ikon, label i18n, shortcut tampil.
3. Clipboard aplikasi (Zustand `clipboard`: mode copy|cut, paths, sourceTab) — lintas folder & tab; Ctrl+C/X/V; item yang di-cut ditampilkan pudar; paste ke folder aktif atau folder yang dipilih.
4. Dialog "Pindah ke…"/"Salin ke…": folder picker tree lazy (hanya folder), path ketik, tombol "Folder baru di sini".
5. Dialog konflik: pilihan Timpa / Lewati / Ganti nama otomatis, tampilan perbandingan ukuran & tanggal keduanya, checkbox "Terapkan ke semua"; untuk banyak konflik dialog diproses berurutan atau sekaligus (apply-to-all).
6. Drag & drop: baris/item → folder (highlight drop target), → breadcrumb segmen, → sidebar (File Saya/Favorit); tahan Ctrl = copy (kursor berubah); drag dari OS → event diteruskan ke hook upload (12a; sekarang toast "upload belum tersedia").
7. Rename inline (F2 / klik lambat pada nama): input di tempat, seleksi nama tanpa ekstensi, Enter simpan, Esc batal, validasi nama (error inline + warning Windows-reserved).
8. Dialog batch rename: rule builder (find/replace, regex toggle, case, numbering `{n}` start/step/pad, ext mode), tabel preview live dari API, status collision, tombol terapkan nonaktif jika ada collision.
9. Dialog "Baru": file kosong, folder, dari template (daftar dari API), symlink (target picker di dalam root). Dialog Permission: matriks rwx owner/group/public + oktal + "terapkan ke subfolder".
10. Delete: Delete → trash tanpa konfirmasi (sesuai preferensi "konfirmasi delete"), Shift+Delete → dialog hapus permanen; > 50 item harus mengetik `HAPUS`.
11. Undo toast (sonner, 10 s, tombol "Urungkan", Ctrl+Z saat toast tampil); hasil operasi diumumkan di live region.
12. Optimistic update daftar untuk rename/delete/move, rollback saat error.
13. E2E `file-ops.spec.ts`: buat, rename, copy, paste konflik rename, move via drag, delete + undo, batch rename.

**File yang dibuat/diubah:** `web/src/features/browser/{ContextMenu,ToolbarActions,InlineRename}.tsx`, `web/src/features/file-ops/{useFileOps,MoveCopyDialog,FolderPicker,ConflictDialog,BatchRenameDialog,NewItemDialog,PermissionDialog,DeleteDialog,UndoToast,dnd.ts}.tsx`, `web/src/stores/clipboard.ts`, `web/src/lib/actions.ts`, `web/e2e/file-ops.spec.ts`.

**Acceptance criteria:**
- [ ] Klik kanan 1 file / 1 folder / banyak / area kosong menampilkan tepat aksi sesuai SPEC §9.5 (aksi tidak relevan tidak terlihat, bukan disabled).
- [ ] Ctrl+C di tab 1, Ctrl+V di tab 2 → file tersalin; cut menampilkan item pudar sampai paste.
- [ ] Paste ke nama yang sudah ada memunculkan dialog konflik dengan ukuran/tanggal keduanya; "Terapkan ke semua" bekerja untuk 3 konflik.
- [ ] Drag 2 file ke folder → move; tahan Ctrl → copy.
- [ ] Delete → toast "Urungkan" 10 s → klik → file kembali.
- [ ] Shift+Delete 51 item → wajib ketik `HAPUS`.
- [ ] Semua dialog bisa dioperasikan dengan keyboard; fokus kembali ke item setelah dialog ditutup.

**Test wajib:** Vitest `ContextMenu.test.tsx` (matriks konteks), `ConflictDialog.test.tsx`, `BatchRenameDialog.test.tsx`, `clipboard.test.ts`; Playwright `file-ops.spec.ts`.

**Verifikasi:** `pnpm lint && pnpm typecheck && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Upload via drag dari OS (12a), compress (14), share (18).

**Commit:** `feat(web): context menu, clipboard, move/copy dialogs, conflict handling, drag and drop, inline and batch rename, undo`

---

## Step 10a — Recycle Bin, kuota, hitung ukuran folder, checksum

> Sertakan Preamble di atas.

**Tujuan:** Recycle Bin per user lengkap (restore/purge/empty/auto-purge), kuota ditegakkan dan ditampilkan, ukuran folder dan checksum bisa dihitung, dialog Properties dasar.

**Prasyarat:** Step 9b.

**Baca dulu:** SPEC §8.6 (Recycle Bin & undo), §8.2 (kuota: akuntansi inkremental, rekalkulasi, enforcement), §8.7 (properties, checksum, hitung ukuran — bagian dasar), §6.3 grup `trash`, `me/quota`, `files/dir-size`, `files/checksum`, §13 ADR-17, ADR-23; PRD epic E11, E2.

**Tugas:**
1. Migrasi `0006_quota.sql`: `quota_usage` (user_id PK, used_bytes, trash_bytes, versions_bytes, calculated_at) sesuai SPEC §4.2.
2. `internal/trash`: `List(user, filter{type: file|dir|image|document, q, cursor})`, `Restore(ids, to?)` (default lokasi asal; jika folder asal hilang → buat ulang; konflik → kebijakan `on_conflict`; jika item punya share nonaktif → respons `details.reactivate_shares:[…]` untuk ditawarkan UI — share baru ada di 18a, sekarang selalu kosong), `Purge(ids)`, `Empty()`, auto-purge harian (`TRASH_RETENTION_DAYS`) via scheduler sederhana `internal/scheduler` (cron-like in-process, dipakai lagi 19b), hasil purge memotong `trash_bytes`.
3. `internal/quota`: `Usage(user)`, `Reserve(ctx, user, bytes)` → `QUOTA_EXCEEDED` (`details:{used,quota,needed}`), `Commit/Release`, hook `Add(user, delta, bucket)` dipanggil dari ops (copy, duplicate, delete→trash memindahkan dari `used` ke `trash`, purge mengurangi `trash`), `Recalculate(user)` walk `fsjail` + trash + versions (job 03:00 semua user + endpoint admin di 19b); `GET /api/v1/me/quota` → `{quota, used, trash, versions, free, percent}`.
4. Enforcement: `Copy`/`Duplicate` dan (nanti) upload/extract/remote download memanggil `Reserve` sebelum menulis; `MAX_UPLOAD_SIZE` dicek terpisah.
5. `POST /api/v1/files/dir-size {path}` → sinkron walk dengan batas 60 s dan `partial:true` jika terpotong (dikonversi ke task di Step 11); hasil `{size, files, dirs, partial}` disimpan cache 10 menit per path.
6. `POST /api/v1/files/checksum {path, algos:["md5","sha1","sha256"]}` sinkron untuk file ≤ 100 MB; lebih besar → `413 FILE_TOO_LARGE` sampai Step 11 (task); `POST /api/v1/files/checksum/verify {path, algo, expected}` → `{match:true|false}`.
7. Endpoint trash: `GET /api/v1/trash?type=&q=&cursor=`, `POST /api/v1/trash/restore {ids, to?, on_conflict?}`, `POST /api/v1/trash/purge {ids}`, `POST /api/v1/trash/empty`; `DELETE` permanen dari browser (Shift+Delete di 9b) sekarang memanggil `POST /files/delete-permanent {paths}` (bypass trash, audit `file.delete_permanent`).
8. Frontend: halaman `/trash` (Details view reuse: kolom Nama, Lokasi asal, Dihapus, Ukuran; filter tipe; cari; aksi Restore, Restore ke…, Hapus permanen, Kosongkan; batch; context menu khusus Recycle Bin), indikator kuota di sidebar (bar + "6,2 GB dari 10 GB", bagian trash/versi di tooltip) dan status bar, peringatan ≥ 90 %.
9. Dialog Properties (Alt+Enter) dasar: nama, tipe, lokasi, ukuran (folder: tombol "Hitung" → ukuran, jumlah file & folder), permission (editor dari 9b), waktu dibuat/diubah/diakses, checksum (hitung MD5/SHA-1/SHA-256 + kolom "verifikasi" dengan input hash), tab Riwayat versi placeholder (16b). Multi-item: ringkasan.
10. Aksi "Hitung ukuran" di context menu folder; kolom Ukuran menampilkan hasil setelah dihitung (cache).
11. E2E `trash-quota.spec.ts`: hapus → tampil di trash → restore ke lokasi asal & ke folder lain → purge → kosongkan; kuota user 1 MB → copy file 2 MB ditolak dengan pesan kuota.

**File yang dibuat/diubah:** `backend/migrations/0006_quota.sql`, `backend/internal/trash/*`, `backend/internal/quota/*`, `backend/internal/scheduler/*`, `backend/internal/files/{dirsize,checksum}.go`, `backend/internal/httpapi/{trash,quota,properties}.go`, `web/src/features/trash/*`, `web/src/features/properties/PropertiesDialog.tsx`, `web/src/features/browser/QuotaIndicator.tsx`, `web/e2e/trash-quota.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Restore item yang folder asalnya sudah dihapus → folder dibuat ulang; restore ke nama yang sudah ada → dialog konflik.
- [ ] Item di trash lebih tua dari `TRASH_RETENTION_DAYS` dipurge oleh job (uji dengan clock palsu / env `=0` dan jalankan job manual).
- [ ] `GET /me/quota` konsisten dengan `Recalculate` setelah 20 operasi acak (selisih 0 byte).
- [ ] Kuota 1 MB: copy 2 MB → `413 QUOTA_EXCEEDED` dan tidak ada file parsial tersisa.
- [ ] Checksum file 50 MB benar (dibandingkan `sha256sum`); file 200 MB → `FILE_TOO_LARGE` (sampai Step 11).
- [ ] Trash user A tidak bisa di-restore/purge oleh user B lewat id (`404`).

**Test wajib:** `trash/*_test.go` (restore asal/lain/konflik, purge, empty, auto-purge), `quota/*_test.go` (reserve/commit/release, rekalkulasi vs inkremental), `files/checksum_test.go`, `httpapi/trash_test.go` (IDOR); Playwright `trash-quota.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Favorit/pin/catatan/tag (10b), task checksum besar (11).

**Commit:** `feat(trash,quota): recycle bin with restore/purge/auto-purge, quota accounting and enforcement, folder size and checksum`

---

## Step 10b — Favorit, pin, catatan, tag warna, Recent, Properties lengkap, Analisis penyimpanan

> Sertakan Preamble di atas.

**Tujuan:** Fitur organisasi setara aaPanel (favorit, pin, remark) plus tag warna, Recent, Aktivitas, dan halaman Analisis penyimpanan (treemap + 50 file terbesar).

**Prasyarat:** Step 10a.

**Baca dulu:** SPEC §8.7 (favorit/pin/catatan/tag/recent/properties/analisis), §8.16 (aktivitas — bagian halaman Aktivitas user), §6.3 grup `meta`, `me/recent`, `me/activity`, `me/storage-analysis`, §9.3 (layout halaman Favorit/Recent/Aktivitas/Analisis); PRD epic E3, E4, E15.

**Tugas:**
1. `internal/meta`: CRUD `favorites`, `pins`, `file_notes` (maks 500 karakter), `file_tags` (warna enum: red, orange, yellow, green, blue, purple, gray; satu tag per item); endpoint `PUT/DELETE /api/v1/meta/favorite`, `/pin`, `/note`, `/tag` (body `{path, …}`; batch `{paths:[…]}`), `GET /api/v1/meta/favorites`; semua referensi path ikut `UpdateReferences` (sudah dari 9a) dan dihapus saat item dipurge; item yang path-nya tidak ada lagi (perubahan via SSH) ditandai `broken:true` saat dibaca, tidak crash.
2. Listing enrichment: field `favorite`, `pinned`, `note`, `tag` per entri (join batch per halaman); pinned selalu di atas (setelah folder-first); filter `?tag=`.
3. Recent: `GET /api/v1/me/recent?limit=200` dari `activity_logs` (open/edit/upload/create, dedup per path, 30 hari) + kolom Lokasi; `GET /api/v1/me/activity?cursor=&action=&from=&to=` untuk halaman Aktivitas; retensi `ACTIVITY_RETENTION_DAYS` (job harian).
4. Analisis penyimpanan: `POST /api/v1/me/storage-analysis` → walk (sinkron ≤ 60 s, else dikonversi task di Step 11) menghasilkan pohon ukuran kedalaman ≤ 3 + 50 file terbesar + ringkasan per tipe (gambar/video/audio/arsip/dokumen/lainnya); cache 10 menit; respons `{generated_at, tree, top_files, by_type, trash, versions}`.
5. Frontend: sidebar Favorit (daftar item favorit dengan Lokasi), Recent (kolom Lokasi + aksi "Buka lokasi file" → navigasi + pilih item), halaman Aktivitas (tabel waktu/aksi/path/detail, filter), badge di kolom Nama (favorit ★, pin, tag warna titik, share badge placeholder 18b), kolom Catatan editable inline (klik → input, Enter simpan), context menu: Favorit/Hapus favorit, Pin/Lepas pin, Catatan…, Tag warna (submenu warna), Copy path (path virtual), Buka di tab baru, Buka di jendela baru (`window.open` route), Buka lokasi file (dari Search/Recent).
6. Filter tag di toolbar (chip warna; gabungan dengan filter cepat).
7. Halaman `/storage`: treemap squarified (SVG sendiri, tanpa library chart besar; hover tooltip; klik masuk folder), tabel 50 terbesar (aksi: buka lokasi, hapus), ringkasan per tipe, tombol "Hitung ulang".
8. Properties lengkap: + favorit/pin/tag/catatan editable, status share placeholder, "Lokasi" clickable.
9. E2E `organize.spec.ts`: favorit → tampil di Favorit → rename file → favorit tetap valid; pin di atas; catatan tersimpan; tag filter; Recent setelah buka file; Analisis menampilkan treemap.

**File yang dibuat/diubah:** `backend/internal/meta/*`, `backend/internal/files/activity.go` (query), `backend/internal/files/analysis.go`, `backend/internal/httpapi/{meta,me_activity,analysis}.go`, `web/src/features/{favorites,recent,activity,storage-analysis}/*`, `web/src/features/browser/{Badges,NoteCell,TagFilter}.tsx`, `web/src/features/properties/PropertiesDialog.tsx`, `web/e2e/organize.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Favorit/pin/catatan/tag bertahan setelah rename & move item maupun folder induknya (cek lewat UI dan DB).
- [ ] Menghapus file via SSH lalu membuka Favorit → item ditandai "rusak", tidak ada error 500.
- [ ] Pinned item selalu paling atas di semua sort; filter tag merah hanya menampilkan item merah.
- [ ] Recent menampilkan file yang baru dibuka dengan Lokasi; "Buka lokasi file" membuka folder dan menyorot item.
- [ ] `/storage` menampilkan treemap yang jumlah totalnya = `used` kuota (± ukuran blok) dan 50 file terbesar terurut.
- [ ] Catatan 501 karakter ditolak `400 VALIDATION_FAILED`.

**Test wajib:** `meta/*_test.go` (CRUD, broken detection, refs ikut rename), `files/analysis_test.go` (treemap agregasi, top 50), `httpapi/meta_test.go` (IDOR); Vitest `Treemap.test.tsx` (squarify), `NoteCell.test.tsx`; Playwright `organize.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Notifikasi (11), share badge (18b).

**Commit:** `feat(meta): favorites, pins, notes, color tags, recent, activity, storage analysis`

---

## Step R-B — Review independen Step 6–10b

> Sertakan Preamble di atas (mode reviewer).

**Tujuan:** Reviewer tanpa konteks implementasi memeriksa UI inti dan operasi file terhadap PRD/SPEC: kebenaran operasi, konsistensi metadata, kuota, aksesibilitas, dan celah keamanan di lapisan API yang baru.

**Prasyarat:** Step 6–10b tercentang.

**Baca dulu:** PRD epic E3, E4, E11, E13; SPEC §8.2–8.7, §8.18, §9; PROGRESS.md catatan Step 6–10b; `docs/reviews/review-A.md` (temuan minor yang ditunda).

**Tugas:**
1. Jalankan `make lint && make test && pnpm lint && pnpm typecheck && pnpm test && pnpm e2e`; catat hasil & durasi E2E.
2. Checklist R-B: (a) IDOR pada semua endpoint baru (`files/*`, `trash/*`, `meta/*`, `me/*`) — user B dengan id/path milik A; (b) `UpdateReferences` atomik — simulasikan kegagalan tx dan pastikan fs di-rollback atau error tercatat; (c) undo token tidak bisa dipakai user lain / dua kali; (d) kuota: cari jalur yang menulis tanpa `Reserve` (copy folder besar, duplicate, restore dari trash); (e) lock: operasi bersamaan rename vs copy pada ancestor; (f) batch rename regex: ReDoS tidak mungkin (RE2) dan tidak bisa menghasilkan nama dengan `/`; (g) thumbnail: tidak ada path absolut bocor di URL/log, signed URL tidak bisa dipakai lintas user; (h) UI: aksi tersembunyi vs disabled sesuai SPEC §9.5; shortcut terlarang tidak dipakai; (i) a11y: axe di `/files`, `/trash`, `/storage`; keyboard-only alur rename & delete; (j) i18n: tidak ada string hardcode (grep JSX untuk teks Indonesia/Inggris di luar `t()`); (k) bundle: ukuran JS awal tercatat & < 300 KB gzip.
3. Baca kode untuk penyimpangan desain: komponen yang tidak memakai token, warna hardcode, dialog tanpa fokus trap, tabel tanpa virtualisasi.
4. Tulis `docs/reviews/review-B.md` dengan format sama seperti review-A; fase perbaikan `fix(review-B): …` + test regresi.

**File yang dibuat/diubah:** `docs/reviews/review-B.md`, perbaikan + test.

**Acceptance criteria:**
- [ ] `docs/reviews/review-B.md` ada; semua blocker/major ditutup; minor dicatat di PROGRESS.
- [ ] Semua suite (Go, Vitest, Playwright) lulus setelah perbaikan.
- [ ] Axe: 0 pelanggaran serius/kritis pada `/files`, `/trash`, `/storage`.

**Test wajib:** Regresi per temuan.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Fitur baru.

**Commit:** `docs(review): independent review B` lalu `fix(review-B): …`

---

## Step 11 — Task engine + SSE + panel task + notifikasi in-app

> Sertakan Preamble di atas.

**Tujuan:** Semua proses berat berjalan sebagai task background yang persisten, bisa dibatalkan, dengan progress realtime via SSE dan notifikasi in-app.

**Prasyarat:** Step 10b.

**Baca dulu:** SPEC §7 (task engine: tipe, state machine, persistensi, konkurensi, cancel, event), §6.4 (SSE), §8.16 (notifikasi), §6.3 grup `tasks`, `notifications`, `events`, §13 ADR-04, ADR-18; PRD epic E15.

**Tugas:**
1. Migrasi `0007_tasks_notifications.sql`: `tasks` (id, user_id, type, state, title, payload_json, result_json, error_code, error_message, progress_done, progress_total, progress_unit `bytes|items`, eta_ms, cancel_requested, created_at, started_at, finished_at, interrupted), `task_logs` (task_id, ts, level, message), `notifications` (id, user_id, type, title, body, link, read_at, created_at) sesuai SPEC §4.2.
2. `internal/tasks`: `Engine` dengan registry `type → Runner` (`Run(ctx, task, progress Reporter) (result, error)`), antrian per user (FIFO) + semaphore `TASK_MAX_PER_USER` (3) + global `max(2, 2×CPU)`; state machine `queued → running → done | failed | cancelled | interrupted`; progress dibatasi 500 ms per update ke DB + SSE; ETA dari laju rata-rata 10 s terakhir; cancel = `context.Cancel` + runner wajib membersihkan hasil parsial; graceful shutdown: tunggu ≤ 10 s lalu tandai `interrupted`; saat start, task `running` lama → `interrupted`; `Retry(id)` membuat task baru dengan payload sama; pembersihan task selesai > 7 hari.
3. Konversi ke task: copy/move besar (9a, hapus `413` sementara), dir-size (10a), checksum > 100 MB (10a), storage-analysis (10b). Tipe task lain didaftarkan step berikutnya (`compress`, `extract`, `remote_download`, `replace_in_files`, `maintenance.*`).
4. Endpoint: `GET /api/v1/tasks?state=&cursor=`, `GET /api/v1/tasks/{id}` (+ `logs`), `POST /api/v1/tasks/{id}/cancel`, `POST /api/v1/tasks/{id}/retry`; semua dibatasi ke user pemilik (`404` untuk milik user lain).
5. `internal/sse`: broker per user, `GET /api/v1/events` (`text/event-stream`, header `Cache-Control: no-cache`, `X-Accel-Buffering: no`, heartbeat komentar tiap 25 s, `id:` monoton, buffer replay 100 event untuk `Last-Event-ID`, maks 5 koneksi per user, timeout route dimatikan untuk endpoint ini). Event: `task.queued|progress|done|failed|cancelled`, `notification.new`, `quota.updated`, `fs.changed {paths}` (dipancarkan mutasi dari task agar UI me-refresh folder yang terbuka).
6. `internal/notify`: buat notifikasi saat task selesai/gagal (kecuali task < 3 s), dan API `GET /api/v1/notifications?unread=1`, `POST /api/v1/notifications/read {ids|all}`.
7. Frontend: klien SSE (`EventSource` dengan reconnect eksponensial, `Last-Event-ID`), store `tasks`; top bar "Tasks(n)" membuka panel task (daftar: judul, user (admin saja, 19b), progress bar, ETA, kecepatan, status, tombol cancel/retry, expand log); toast ringkas saat selesai; lonceng notifikasi (daftar, tandai dibaca, tautan ke item); `fs.changed` → invalidasi query listing yang relevan.
8. Panel task tetap tampil saat berpindah route; tidak menutupi toast & panel transfer (layout pojok kanan bawah: transfer di atas task; sonner di kiri bawah).
9. E2E `tasks.spec.ts`: copy folder 5.000 file kecil → task tampil dengan progress → cancel → folder tujuan bersih; retry → selesai; notifikasi muncul.

**File yang dibuat/diubah:** `backend/migrations/0007_tasks_notifications.sql`, `backend/internal/tasks/*`, `backend/internal/sse/*`, `backend/internal/notify/*`, `backend/internal/httpapi/{tasks,events,notifications}.go`, `backend/internal/files/{copy,dirsize,checksum,analysis}.go` (runner), `web/src/lib/sse.ts`, `web/src/stores/tasks.ts`, `web/src/features/tasks/{TaskPanel,TaskRow}.tsx`, `web/src/features/notifications/*`, `web/e2e/tasks.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] `curl -N -H "Cookie: …" /api/v1/events` menerima `: heartbeat` tiap ≤ 25 s dan event `task.progress` saat copy besar berjalan.
- [ ] 4 task dibuat oleh satu user → maksimal 3 `running`, ke-4 `queued`.
- [ ] Cancel saat copy berjalan → state `cancelled`, tidak ada file parsial di tujuan, kuota dikembalikan.
- [ ] `kill -TERM` server saat task berjalan → setelah start ulang task berstatus `interrupted` dan bisa `retry`.
- [ ] Reconnect SSE dengan `Last-Event-ID` menerima event yang terlewat (buffer).
- [ ] Task user A tidak terlihat/terbatalkan oleh user B.

**Test wajib:** `tasks/engine_test.go` (state machine, semaphore, cancel cleanup, interrupted, retry, throttle progress), `sse/broker_test.go` (replay, maks koneksi, heartbeat), `httpapi/tasks_test.go` (IDOR), `notify/*_test.go`; Vitest `sse.test.ts`; Playwright `tasks.spec.ts`.

**Verifikasi:** `make test && make lint && go test -race ./internal/tasks/... && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Jenis task fitur (arsip, remote download, replace).

**Commit:** `feat(tasks): persistent task engine with cancel/retry, SSE events, task panel, in-app notifications`

---

## Step 12a — Upload tus + precheck + panel transfer + upload folder + paste clipboard

> Sertakan Preamble di atas.

**Tujuan:** Upload resumable ber-chunk yang tahan putus koneksi dan reload, dengan precheck kuota/konflik, upload folder berstruktur, paste gambar, dan panel transfer persisten.

**Prasyarat:** Step 11.

**Baca dulu:** SPEC §8.8 (upload: tus, finalisasi, precheck, panel transfer, pembersihan), §6.5 (tus), §4.2 `upload_sessions`, §1.3 (alur upload), §12.4 (`client_max_body_size`, `proxy_request_buffering off`), §13 ADR-03; PRD epic E5.

**Tugas:**
1. Migrasi `0008_upload_sessions.sql`: `upload_sessions` (tus_id, user_id, dest_path, filename, relative_path, size, on_conflict, share_id nullable (18a), created_at, expires_at, state).
2. `internal/upload`: `tusd` `UnroutedHandler` di `/api/v1/upload/tus/` dengan `filestore` di `DATA_DIR/uploads`, `RespectForwardedHeaders: true` (Location absolut via `APP_BASE_URL`), `MaxSize = MAX_UPLOAD_SIZE`, chunk ≤ `UPLOAD_CHUNK_SIZE` (verifikasi `Upload-Length`/`PATCH` ukuran), `PreUploadCreateCallback`: sesi + CSRF (header `X-CSRF-Token` wajib pada POST/PATCH/DELETE tus), metadata wajib `filename`, `path`, opsional `relativePath`, `onConflict`; validasi nama (`ValidateName`, tolak ekstensi diblokir dari settings), kuota `Reserve(size)`, simpan `upload_sessions`; `PreFinishResponseCallback`: verifikasi ukuran file = `Upload-Length`, buat subfolder `relativePath` (MkdirAll dalam jail), pindah atomik (`rename` same-FS; `DATA_DIR` dan `STORAGE_ROOT` same-FS by design, fallback copy) ke tujuan dengan `on_conflict` (overwrite/skip/rename), `quota.Commit`, `thumbs.OnChange`, `activity`, `fs.changed`; `OnTerminate` → `Release` kuota.
3. Pembersihan: job tiap jam menghapus upload terbengkalai > 24 jam (`.info` + bin) dan `Release` kuota.
4. `POST /api/v1/upload/precheck {path, files:[{name, size, relativePath}]}` → per file: `conflict` (existing size/mtime), `blocked_ext`, `too_large`; plus `quota_ok`, `needed`, `free`.
5. Frontend `web/src/features/upload/`: `tus-js-client` (chunk = `UPLOAD_CHUNK_SIZE` dari `/auth/me` config publik, 3 upload paralel, `retryDelays` backoff, `storeFingerprintForResuming` → resume setelah reload dengan prompt "Lanjutkan 2 upload?"), antrian global (Zustand `transfers`), pause/resume/cancel per file & semua, kecepatan rata-rata (window 5 s), ETA, total.
6. Sumber: tombol Upload file / Upload folder (`webkitdirectory`), drag & drop ke area konten (overlay "Lepaskan untuk upload ke /path") dan langsung ke baris folder, `DataTransferItem.webkitGetAsEntry` untuk folder dari drag (struktur utuh), paste (`Ctrl+V` saat clipboard berisi gambar → `image-YYYYMMDD-HHMMSS.png`).
7. Precheck sebelum mulai: dialog konflik (Timpa/Lewati/Ganti nama + terapkan ke semua, seperti aaPanel), pesan kuota tidak cukup (tampilkan sisa), ekstensi diblokir.
8. Panel transfer pojok kanan bawah (bisa diciutkan, tetap saat pindah folder, badge jumlah aktif): daftar per file (nama, tujuan, progress, kecepatan, ETA, status, aksi), ringkasan total; selesai → toast + `fs.changed` refresh folder.
9. Vite dev proxy meneruskan header tus; `README` menambah catatan Nginx (`client_max_body_size` ≥ chunk, `proxy_request_buffering off`) — final di Step 22.
10. E2E `upload.spec.ts`: upload 1 file 50 MB (generate di test) → pause → resume → selesai → checksum sama; putus jaringan (route abort) → retry otomatis; reload halaman di tengah → resume; upload folder 3 level; konflik rename; kuota tidak cukup ditolak sebelum mulai. Test manual 1 GB dicatat di PROGRESS (ukur RSS backend tidak tumbuh).

**File yang dibuat/diubah:** `backend/migrations/0008_upload_sessions.sql`, `backend/internal/upload/*`, `backend/internal/httpapi/upload.go`, `web/src/features/upload/{useUploader,TransferPanel,DropZone,PrecheckDialog,paste.ts,folder-entries.ts}.tsx`, `web/src/stores/transfers.ts`, `web/e2e/upload.spec.ts`, `docs/openapi.yaml` (tus didokumentasikan sebagai eksternal + precheck).

**Acceptance criteria:**
- [ ] `curl -X POST /api/v1/upload/tus/` tanpa sesi → `401`; tanpa CSRF → `403 CSRF_INVALID`.
- [ ] Upload 1 GB lewat UI: pause/resume, lanjut setelah reload; file akhir checksum identik; RSS backend stabil (≤ +100 MB).
- [ ] Mematikan server di tengah upload lalu start → resume melanjutkan dari offset terakhir (`HEAD` tus mengembalikan `Upload-Offset` benar).
- [ ] Upload folder `a/b/c/file.txt` menghasilkan struktur yang sama di tujuan.
- [ ] Paste gambar dari clipboard membuat `image-….png` di folder aktif.
- [ ] Upload terbengkalai (file `.info` 25 jam) dihapus oleh job dan kuota dilepas.
- [ ] Ekstensi diblokir (mis. `.exe` jika admin set) ditolak di precheck dan di `PreUploadCreate`.

**Test wajib:** `upload/hooks_test.go` (auth/csrf/metadata/kuota/konflik/finalisasi atomik/cleanup), integrasi tus protokol dengan klien Go (`POST`→`PATCH`×n→finish), `upload/precheck_test.go`; Vitest `useUploader.test.ts` (antrian, paralel 3), `folder-entries.test.ts`; Playwright `upload.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Download (12b), upload lewat link publik (18a).

**Commit:** `feat(upload): resumable tus uploads with precheck, folder upload, clipboard paste, and transfer panel`

---

## Step 12b — Download Range, zip streaming multi-item, signed URL, batas paralel

> Sertakan Preamble di atas.

**Tujuan:** Download file tunggal yang bisa di-resume, zip streaming on-the-fly untuk banyak item/folder tanpa file sementara, dan signed URL untuk media/download manager.

**Prasyarat:** Step 12a (hook `webassets`/sign dari Step 8 sudah ada).

**Baca dulu:** SPEC §8.9 (download, zip streaming, signed URL), §1.4 (alur download Range), §6.3 `files/download`, `files/raw`, `files/zip`, `files/sign`, §10.3 (header untuk konten user), §13 ADR-06, ADR-15; PRD epic E5, E8.

**Tugas:**
1. `internal/httpapi/sign` (dari Step 8) dilengkapi: `Sign(scope{user_id|share_id}, path, purpose download|media|thumb|zip, ttl)`; `Verify` constant-time; kunci HKDF(APP_SECRET, "signed-url"); `POST /api/v1/files/sign {items:[{path, purpose}]}` → URL ber-`exp`&`sig` (TTL download `SIGNED_URL_TTL` 5 m, media `MEDIA_URL_TTL` 2 h). Endpoint bertanda tangan **tidak** memerlukan cookie (dapat dipakai download manager) tetapi tetap memeriksa user aktif.
2. `GET /api/v1/files/download?path=&exp=&sig=`: `http.ServeContent` atas `*os.File` dari jail (Range, `Accept-Ranges`, `ETag` = hash(inode,size,mtime), `Last-Modified`, `If-Range`), `Content-Disposition: attachment; filename="ascii"; filename*=UTF-8''…`, `Content-Type` dari magic bytes (`mimetype`) tetapi untuk tipe berbahaya (html, svg, xml, xhtml, js) selalu `application/octet-stream`, `X-Content-Type-Options: nosniff`, `Cache-Control: private, no-store`, `X-Accel-Buffering: no`, tanpa timeout route; `Content-Security-Policy: sandbox; default-src 'none'`.
3. `GET /api/v1/files/raw?path=&exp=&sig=&variant=` (inline untuk viewer): kebijakan ADR-06 — hanya `image/*` raster (jpeg, png, gif, webp, avif, bmp, ico), `video/*`, `audio/*`, `application/pdf` yang inline (`Content-Disposition: inline`); lainnya dipaksa `attachment` + octet-stream; `variant=preview` (gambar 2048 px dari pipeline thumbs) dan `variant=thumb`.
4. `POST /api/v1/files/zip {paths:[…], name?}` → `{url}` (signed, purpose zip, payload daftar path disimpan sementara 10 menit dengan id di `DATA_DIR/tmp/zip-<id>.json`); `GET /api/v1/files/zip?id=&exp=&sig=` → `application/zip` streaming (`archive/zip` writer langsung ke `http.ResponseWriter`, metode Store default/Deflate opsional, zip64, UTF-8 flag, folder rekursif, symlink dilewati, file yang gagal dibaca dicatat sebagai `_ERRORS.txt` di akhir), `Content-Disposition` nama folder atau `download-<ts>.zip`, tanpa `Content-Length`, tanpa file sementara.
5. Batas: 3 download/zip paralel per user (semaphore; lebih → `429 RATE_LIMITED` dengan `Retry-After: 5`); throughput tidak dibatasi.
6. Frontend: aksi Download (1 file → `<a download href=signedURL>`; folder/multi → `POST zip` lalu buka URL; progres tak diketahui → toast "Menyiapkan zip…" lalu browser menangani), "Download sebagai zip" di context menu folder, download dari Properties dan panel preview; util `getMediaUrl(path)` dengan cache sampai `exp - 60 s` dan refresh otomatis pada `403 SIGNATURE_EXPIRED` (dipakai viewer 17).
7. E2E `download.spec.ts`: download file → isi sama; Range request `curl -r 0-99` → `206`; zip 2 file + 1 folder → `unzip -t` OK; HTML file di-download sebagai attachment octet-stream; signed URL kedaluwarsa → `403 SIGNATURE_EXPIRED`; signed URL user A dipakai setelah A dinonaktifkan (simulasi flag) → `403`.

**File yang dibuat/diubah:** `backend/internal/httpapi/sign/*`, `backend/internal/download/{serve,zipstream,policy}.go`, `backend/internal/httpapi/download.go`, `web/src/features/download/*`, `web/src/lib/media-url.ts`, `web/e2e/download.spec.ts`, `docs/openapi.yaml`, `docs/SECURITY.md` (kebijakan konten inline).

**Acceptance criteria:**
- [ ] `curl -I "<signed download url>"` → `Accept-Ranges: bytes`, `ETag`, `Content-Disposition: attachment; filename*=UTF-8''…`; `curl -r 100-199` → `206` 100 byte.
- [ ] `file.html` via `/raw` → `Content-Type: application/octet-stream` + `attachment`; `foto.jpg` via `/raw` → `image/jpeg` inline + CSP sandbox.
- [ ] Zip folder 1 GB (file dummy) → tidak ada file di `DATA_DIR/tmp` selain json kecil, RSS backend stabil, `unzip -t` OK.
- [ ] Download ke-4 paralel → `429`; selesai salah satu → berhasil.
- [ ] Mengubah 1 karakter `sig` → `403 SIGNATURE_INVALID`.

**Test wajib:** `sign/sign_test.go` (verify, exp, scope, purpose mismatch), `download/policy_test.go` (tabel MIME → inline/attachment), `download/zipstream_test.go` (zip64 > 4 GB disimulasikan dengan `io.Reader` sintetis, UTF-8, error entry), `httpapi/download_test.go` (Range, ETag, semaphore, IDOR); Playwright `download.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm e2e`.

**Di luar scope step ini:** Viewer (17), link publik (18a).

**Commit:** `feat(download): range downloads, streaming zip, signed URLs with inline content policy`

---

## Step 13 — Remote download + SSRF guard + allow/denylist

> Sertakan Preamble di atas.

**Tujuan:** Mengunduh dari URL http/https ke folder user sebagai task, dengan proteksi SSRF yang diuji dan batas ukuran/kuota.

**Prasyarat:** Step 12b.

**Baca dulu:** SPEC §8.10 (remote download & detail SSRF guard), §7 (task), §6.3 `remote-downloads`, §10.1 (STRIDE: SSRF), §10.5 (rate limit remote download); PRD epic E5.

**Tugas:**
1. `internal/netguard`: `SafeDialer` — resolve semua A/AAAA, tolak jika ADA satu pun alamat dalam daftar: `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `100.64.0.0/10`, `0.0.0.0/8`, `224.0.0.0/4`, `240.0.0.0/4`, `::1/128`, `::/128`, `fc00::/7`, `fe80::/10`, `ff00::/8`, `::ffff:0:0/96` (IPv4-mapped diperiksa sebagai IPv4); pin IP terpilih saat `Dial` (`Control`/`DialContext` custom) agar tidak ada resolusi ulang (DNS rebinding); hanya port 80/443 (+ daftar tambahan dari settings); skema hanya `http`/`https`; `CheckRedirect` memvalidasi ulang setiap hop (maks 5), tolak `file:`, `ftp:`, dan userinfo di URL.
2. `internal/remotedl` runner task `remote_download`: `POST /api/v1/remote-downloads {urls:[…], path, filename?, on_conflict}` → satu task per URL (maks 10 URL/permintaan; rate limit 10 task/jam/user), opsi `REMOTE_DOWNLOAD_ENABLED` + settings admin (allowlist/denylist domain, ukuran maks); nama file dari `filename` → `Content-Disposition` (sanitasi `ValidateName`, tanpa path) → segmen URL terakhir → `download-<ts>`; batas `min(REMOTE_DOWNLOAD_MAX_SIZE, sisa kuota)` dicek dari `Content-Length` (jika ada) dan saat streaming (`LimitReader` + abort); tulis ke `DATA_DIR/tmp/<task>.part` lalu rename ke tujuan dengan kebijakan konflik; progress bytes/total (atau unknown), kecepatan, ETA; `Retry` melanjutkan dengan `Range` jika server mendukung (`Accept-Ranges` & `ETag` sama), else mulai ulang; timeout connect 10 s, idle 60 s, total 24 h; `User-Agent: FileManager/<version> (+https://file.kppbwi.app)`; `http.Transport` tanpa proxy env (`ProxyFromEnvironment` dimatikan), `DisableCompression` untuk progress akurat.
3. Audit `remote_download.created` (URL host saja), log tanpa query string.
4. Frontend: dialog Remote download (textarea URL per baris, folder tujuan, nama file (untuk 1 URL), konflik), validasi URL klien, task tampil di panel (kecepatan, ETA, cancel, retry); aksi di toolbar "Remote" & context menu area kosong.
5. Admin hook: settings `remote_download.enabled|max_size|allowlist|denylist` dibaca dari `settings` (UI di 19a).
6. Test SSRF dengan resolver palsu + `httptest` server: hostname → 127.0.0.1, → 169.254.169.254, → `::1`, → IPv4-mapped, → publik lalu redirect ke privat, → DNS rebinding (resolusi pertama publik, kedua privat: harus tetap memakai IP pertama), skema `file://`, port 22, 6 redirect, `Content-Length` > batas, body > batas tanpa Content-Length, cancel membersihkan `.part`.

**File yang dibuat/diubah:** `backend/internal/netguard/*`, `backend/internal/remotedl/*`, `backend/internal/httpapi/remotedl.go`, `web/src/features/remote-download/*`, `docs/openapi.yaml`, `docs/SECURITY.md` (SSRF).

**Acceptance criteria:**
- [ ] `POST /remote-downloads {urls:["http://127.0.0.1:8080/healthz"]}` → task `failed` dengan `SSRF_BLOCKED` (atau `400 SSRF_BLOCKED` jika ditolak saat validasi awal).
- [ ] URL publik yang redirect ke `http://169.254.169.254/` → gagal `SSRF_BLOCKED` pada hop redirect.
- [ ] Unduh file 100 MB dari `httptest` → progress & kecepatan tampil, file akhir checksum sama; cancel → tidak ada `.part` tersisa.
- [ ] `REMOTE_DOWNLOAD_ENABLED=false` → `403 FORBIDDEN`; denylist domain → `403` dengan pesan jelas.
- [ ] 11 task dalam 1 jam → `429 RATE_LIMITED`.
- [ ] Log tidak memuat query string URL.

**Test wajib:** `netguard/*_test.go` (semua kasus di tugas 6), `remotedl/runner_test.go` (nama file, batas, resume Range, konflik), `httpapi/remotedl_test.go`; Playwright `remote-download.spec.ts` (dengan server lokal yang diizinkan lewat allowlist test).

**Verifikasi:** `make test && make lint && go test -race ./internal/netguard/... ./internal/remotedl/...`.

**Di luar scope step ini:** UI settings admin (19a).

**Commit:** `feat(remote-download): URL download tasks with SSRF guard, limits, and resume`

---

# Milestone M3 — Arsip, search, editor, viewer

## Step 14a — Arsip: compress & extract semua format, limit keamanan, fixture berbahaya

> Sertakan Preamble di atas.

**Tujuan:** Compress dan extract sebagai task untuk seluruh matriks format (zip, 7z, rar-read, tar, tar.gz/bz2/xz/zst, gz/bz2/xz/zst tunggal, war/jar, iso-read), dengan proteksi zip-slip dan zip bomb yang diuji fixture.

**Prasyarat:** Step 13.

**Baca dulu:** SPEC §8.11 (arsip: matriks format, engine, limit, keamanan, CLI), §7 (task), §6.3 grup `archives`, §10.1 (STRIDE arsip), §13 ADR-08; PRD epic E6.

**Tugas:**
1. `internal/archive/format.go`: deteksi format dari magic bytes (`mimetype`) + ekstensi ganda (`.tar.gz`, `.tgz`, `.tar.zst`, …); tabel kemampuan `{format: canRead, canWrite, passwordRead, passwordWrite, engine}` persis SPEC §8.11; `GET /api/v1/archives/capabilities` (bergantung dependency terdeteksi: tanpa `7zz` → 7z & iso off, tanpa `rar` → rar write off).
2. Engine: `mholt/archives` untuk baca zip/tar.*/rar/7z dan tulis zip (tanpa password) & tar.*; kompresi tunggal gz/bz2/xz/zst via library Go (`klauspost/compress`, `ulikunitz/xz` atau yang dibawa mholt/archives — verifikasi di registry); zip AES-256 baca/tulis via library Go sesuai ADR-08 (verifikasi kandidat; jika tidak layak → implementasi `internal/archive/zipaes` WinZip AES dengan test vector yang dibuat `7zz` di CI); 7z: baca/listing/extract (termasuk ber-password — README mholt/archives, verifikasi dengan fixture) via mholt/archives, `7zz x` hanya fallback; tulis 7z (termasuk password & enkripsi nama file `-mhe=on`) via `7zz` (`internal/exec`); rar baca (termasuk ber-password) via mholt/archives, fallback `unrar` jika terpasang; rar tulis via `rar` hanya jika `RAR_PATH` diset; iso baca via `7zz`. Password 7zz: spike **wajib** di awal step — uji apakah `7zz ... -p` (tanpa nilai) membaca password dari stdin pipe; hasil dicatat di SPEC §8.11 & SECURITY.md; jika tidak, pakai argv + peringatan dokumentasi (ADR-08).
3. Task `compress`: `POST /api/v1/archives/compress {paths, format, name?, dest_path?, level: store|fast|normal|max, password?, encrypt_names?, delete_source?:false, split_volume_mb?}` → tulis ke `DATA_DIR/tmp/<task>/` lalu rename atomik ke tujuan (konflik → kebijakan); progress per byte; cancel membersihkan; `delete_source` memindah ke trash (bukan hapus permanen) setelah sukses; `Reserve` kuota estimasi (ukuran sumber) lalu `Commit` ukuran akhir.
4. Task `extract`: `POST /api/v1/archives/extract {path, dest_path?: default folder baru bernama arsip, password?, name_encoding: auto|utf8|gbk|cp437|shift_jis, on_conflict, entries?:[…] (sebagian)}`; decoding nama via `x/text/encoding` + deteksi `chardet` untuk `auto` (zip tanpa flag UTF-8); hasil ke `DATA_DIR/tmp/<task>/` lalu pindah atomik per-entri-top-level ke tujuan; mode file 0640 / folder 0750, bit setuid/setgid/sticky dibuang; mtime dipertahankan.
5. Keamanan ekstraksi (`internal/archive/safe.go`): normalisasi nama entry (`Normalize` fsjail), tolak absolut, `..`, drive letter, NUL; symlink entry hanya jika target relatif dan resolve di dalam folder tujuan (selain itu dilewati + log), hardlink diperlakukan sama; device/FIFO/socket dilewati; batas: total ukuran dekompresi > `min(ARCHIVE_MAX_EXTRACT_SIZE, sisa kuota)` → `ARCHIVE_LIMIT`, entri > `ARCHIVE_MAX_ENTRIES` → `ARCHIVE_LIMIT`, rasio dekompresi > 200:1 setelah 10 MB pertama → `ARCHIVE_UNSAFE`, kedalaman path > 64 → tolak; arsip bersarang tidak diekstrak otomatis; ukuran diukur saat streaming (`LimitReader`), bukan dari header saja.
6. CLI (`7zz`, `unrar`, `rar`) via `internal/exec`: argv, `--`, cwd = tmp task, `-o` eksplisit, timeout 1 h, `nice`/`ionice`, output stderr dibatasi 64 KB untuk log task, password tidak masuk log task.
7. Fixture `internal/archive/testdata/`: `zipslip.zip` (entry `../../evil.txt`), `abs.zip` (`/etc/evil`), `symlink-escape.tar` (symlink → `/etc/passwd` lalu file via link), `hardlink-escape.tar`, `bomb-small.zip` (beberapa file nol 50 MB → rasio ekstrem, dibuat saat test), `nested.zip`, `many-entries.zip` (dibuat saat test, > `ARCHIVE_MAX_ENTRIES` yang diset kecil di test), `gbk-names.zip`, `encrypted-aes.zip`, `encrypted.7z` (dibuat oleh `7zz` jika ada, else skip), `sample.tar.{gz,bz2,xz,zst}`, `sample.rar` (hanya jika ada di repo dengan lisensi jelas; else skip test rar).
8. Frontend: dialog Compress (format sesuai `capabilities`, nama output default, lokasi, level, password + konfirmasi, enkripsi nama (7z), split volume, "hapus asli setelah sukses"), dialog Extract (tujuan: folder ini / folder baru bernama arsip (default) / pilih folder; password; encoding nama; konflik), aksi di context menu (Compress; Extract jika arsip), progress di panel task; `delete_source` berlabel jelas.
9. E2E `archive.spec.ts`: compress 2 file + folder ke zip & tar.gz → extract ke folder baru → isi sama; zip ber-password salah → `ARCHIVE_PASSWORD_WRONG` → prompt ulang; zip-slip fixture → ditolak dengan pesan.

**File yang dibuat/diubah:** `backend/internal/archive/{format,engine_*,compress,extract,safe,encoding,zipaes?}.go` + test + `testdata/`, `backend/internal/httpapi/archives.go`, `web/src/features/archive/{CompressDialog,ExtractDialog}.tsx`, `web/e2e/archive.spec.ts`, `docs/openapi.yaml`, `docs/SECURITY.md` (arsip), SPEC §8.11 (hasil spike 7zz stdin).

**Acceptance criteria:**
- [ ] Semua fixture berbahaya ditolak/dilewati tanpa menulis apa pun di luar folder tujuan (test memverifikasi dengan walk `t.TempDir()`).
- [ ] `ARCHIVE_MAX_ENTRIES=100` + arsip 101 entri → task `failed ARCHIVE_LIMIT`, tujuan bersih.
- [ ] Bomb: arsip 1 MB yang mengembang > batas → dihentikan ≤ batas + 1 chunk, tmp dibersihkan.
- [ ] Round-trip untuk setiap format di matriks yang `canWrite`: compress → extract → `diff -r` identik; format `canRead` saja diuji extract dari fixture.
- [ ] Zip AES-256 yang dibuat aplikasi bisa dibuka `7zz t -p<pw>`; zip AES dari 7zz bisa diekstrak aplikasi.
- [ ] `ps` selama extract 7z ber-password: tidak menampilkan password **atau** (jika stdin tidak didukung) hal ini terdokumentasi di SECURITY.md dengan rekomendasi `hidepid=2` dan peringatan di UI admin "Status dependency".
- [ ] Nama file GBK di zip lama terbaca benar dengan `auto` dan `gbk`.

**Test wajib:** `archive/safe_test.go` (semua fixture), `archive/format_test.go`, `archive/roundtrip_test.go` (matriks), `archive/encoding_test.go`, `archive/limits_test.go`, `httpapi/archives_test.go` (IDOR, validasi); Playwright `archive.spec.ts`.

**Verifikasi:** `make test && make lint && go test -race ./internal/archive/... && pnpm e2e`.

**Di luar scope step ini:** Archive browser & edit entry (14b).

**Commit:** `feat(archive): compress/extract tasks for all formats with zip-slip and zip-bomb protection`

---

## Step 14b — Archive browser, extract sebagian, tambah/hapus entry zip, password & encoding nama

> Sertakan Preamble di atas.

**Tujuan:** Membuka arsip seperti folder (read-only), preview isi, extract item terpilih, dan edit minimal (tambah/hapus entry) untuk zip.

**Prasyarat:** Step 14a.

**Baca dulu:** SPEC §8.11 (archive browser), §8.14 (pemetaan MIME → viewer untuk preview dalam arsip), §6.3 `archives/entries`, `archives/entry-content`, `archives/extract-entries`, `archives/add`, `archives/remove-entry`; PRD epic E6.

**Tugas:**
1. `GET /api/v1/archives/entries?path=&inner=&password=&name_encoding=&cursor=` → daftar entri satu level (nama, ukuran asli & terkompresi, mtime, is_dir, encrypted, method) dari indeks arsip (cache indeks per (path, mtime) 10 menit, maks `ARCHIVE_MAX_ENTRIES`); password lewat header `X-Archive-Password` (bukan query) dan tidak masuk log; arsip ber-password tanpa password → `401 ARCHIVE_PASSWORD_REQUIRED`.
2. `GET /api/v1/archives/entry-content?path=&inner=&exp=&sig=` (signed, purpose `media`, password disimpan singkat di server dengan id sesi arsip 10 menit) → streaming satu entri ≤ 50 MB dengan kebijakan inline yang sama seperti `/raw` (teks, gambar, PDF, audio); lainnya attachment.
3. `POST /api/v1/archives/extract-entries {path, entries:[…], dest_path, password?, on_conflict}` → task extract sebagian (reuse 14a dengan filter).
4. Zip edit (hanya zip tanpa enkripsi): `POST /api/v1/archives/add {path, inner_dir, sources:[paths]}` dan `POST /api/v1/archives/remove-entry {path, entries}` → task yang menulis zip baru di tmp (salin entri raw via `zip.Writer.CopyRaw`-setara/`CreateRaw`, tanpa dekompresi ulang) lalu rename atomik menggantikan asli (versi pre-image tidak dibuat; arsip asli tetap jika gagal); format lain → `400 UNSUPPORTED_TYPE` dengan pesan alasan.
5. Frontend: Archive browser overlay/route (`?archive=<path>`): breadcrumb di dalam arsip, tabel entri (kolom Nama, Ukuran, Terkompresi, Rasio, Diubah), double-click folder → masuk, file → preview (teks/gambar/PDF/audio via viewer Step 17 — sampai itu: teks & gambar sederhana), seleksi + "Extract yang dipilih ke…", "Extract semua", "Tambah file…" (folder picker + picker file dalam root), "Hapus entry" (konfirmasi), prompt password (tersimpan selama overlay terbuka), pemilih encoding nama dengan preview ulang.
6. Konteks menu: "Lihat isi arsip" untuk file arsip; "Buka" pada arsip membuka browser ini.
7. E2E `archive-browser.spec.ts`: buka zip → navigasi folder dalam arsip → preview txt → extract 2 entri → tambah file → hapus entry → verifikasi dengan `unzip -l`.

**File yang dibuat/diubah:** `backend/internal/archive/{index,entrystream,zipedit}.go` + test, `backend/internal/httpapi/archives_browser.go`, `web/src/features/archive/{ArchiveBrowser,EntryTable,PasswordPrompt,EncodingSelect}.tsx`, `web/e2e/archive-browser.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Zip 50.000 entri: `entries` halaman pertama < 1 s (indeks cache), navigasi antar folder dalam arsip tanpa membaca ulang seluruh file.
- [ ] Preview gambar/teks dalam arsip tanpa ekstraksi penuh (tidak ada file di `DATA_DIR/tmp` setelah preview).
- [ ] Extract 2 entri dari tar.gz menghasilkan hanya 2 entri tersebut.
- [ ] Tambah file ke zip 1 GB tidak mendekompresi ulang entri lama (durasi ≈ waktu salin + entri baru); kegagalan di tengah → arsip asli utuh.
- [ ] Arsip ber-password: password tidak muncul di URL, log, maupun task log.

**Test wajib:** `archive/index_test.go`, `archive/zipedit_test.go` (add/remove/raw copy/kegagalan atomik), `httpapi/archives_browser_test.go`; Playwright `archive-browser.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm e2e`.

**Di luar scope step ini:** Edit entry untuk 7z/tar (ditolak dengan alasan).

**Commit:** `feat(archive): archive browser with partial extract, in-archive preview, and zip entry add/remove`

---

## Step R-C — Review independen Step 11–14b

> Sertakan Preamble di atas (mode reviewer).

**Tujuan:** Reviewer memeriksa task engine, SSE, upload tus, download/zip, remote download, dan arsip — area dengan risiko keamanan tertinggi (SSRF, zip-slip/bomb, finalisasi upload, signed URL).

**Prasyarat:** Step 11–14b tercentang.

**Baca dulu:** PRD epic E5, E6, E15; SPEC §7, §8.8–8.11, §10; `docs/SECURITY.md`; PROGRESS catatan Step 11–14b; review-B minor tertunda.

**Tugas:**
1. Jalankan seluruh suite + `go test -race` + fuzz singkat (`go test -fuzz=FuzzEntryName -fuzztime=60s ./internal/archive/`, `-fuzz=FuzzNormalize ./internal/fsjail/`).
2. Checklist R-C: (a) tus: finalisasi atomik, ukuran diverifikasi, metadata `path` tidak bisa keluar root, upload ke share (belum ada) ditolak, kuota dilepas pada terminate/cleanup; (b) signed URL: scope user, purpose, exp, constant-time verify, tidak bisa dipakai setelah user nonaktif, tidak ada path absolut; (c) kebijakan inline `/raw` & `/entry-content`: coba html, svg, xml, xhtml, js, mhtml, pdf dengan JS → hanya raster/audio/video/pdf inline, CSP sandbox ada; (d) zip streaming: symlink dilewati, folder `.system` tidak pernah masuk, user B tidak bisa minta path A; (e) SSRF: tambah kasus baru (IPv6 zone id, hostname dengan trailing dot, `0x7f000001`, `127.1`, `[::ffff:127.0.0.1]`, redirect 307 ke skema lain, `Location` relatif); (f) arsip: fixture tambahan reviewer (zip dengan nama `..\\..\\evil`, tar dengan `./../`, entry 0 byte dengan ukuran header palsu, symlink lalu file dengan nama sama); password tidak di log/argv (cek `ps` & log); CLI tanpa shell; (g) task engine: cancel membersihkan di semua runner; `interrupted` saat SIGTERM; `TASK_MAX_PER_USER` tidak bisa dilewati lewat retry; SSE tidak membocorkan event user lain; (h) resource: RSS saat upload 1 GB + zip 1 GB + extract 1 GB bersamaan di 1 vCPU (catat angka).
3. Tulis `docs/reviews/review-C.md`; perbaikan `fix(review-C): …` + test regresi.

**File yang dibuat/diubah:** `docs/reviews/review-C.md`, perbaikan + fixture + test.

**Acceptance criteria:**
- [ ] `docs/reviews/review-C.md` ada; tidak ada blocker/major terbuka.
- [ ] Semua suite lulus; fuzz 60 s tanpa crash.
- [ ] Angka RSS beban gabungan tercatat di PROGRESS.

**Test wajib:** Regresi per temuan; fixture arsip baru masuk `testdata/`.

**Verifikasi:** `make test && make lint && go test -race ./... && pnpm e2e`.

**Di luar scope step ini:** Fitur baru.

**Commit:** `docs(review): independent review C` lalu `fix(review-C): …`

---

## Step 15 — Search: nama, isi file, replace in files dengan preview + rollback, pencarian global, riwayat

> Sertakan Preamble di atas.

**Tujuan:** Pencarian nama dengan filter, pencarian isi file (ripgrep dengan fallback Go), replace di banyak file dengan preview diff dan rollback via riwayat versi.

**Prasyarat:** Step 14b (task engine, versi pre-image tersedia sebagai package minimal — lihat tugas 4).

**Baca dulu:** SPEC §8.12 (search & replace: nama, isi, rg vs fallback, batas, replace), §8.13 (versi — fungsi `Snapshot` dipakai replace), §6.3 grup `search`, §10.5 (rate limit search), §13 ADR-12; PRD epic E9.

**Tugas:**
1. `internal/search/name.go`: walk `jail.WalkDir` dari folder awal (opsi `recursive`), cocokkan nama (substring case-insensitive dengan Unicode fold; opsi `glob`, `regex` RE2), filter `type` (file/dir/gambar/video/audio/dokumen/arsip/kode), `ext`, `size_min/max`, `mtime_from/to`, `tag`; batas 5.000 hasil / 20 s (`partial:true`), cursor berbasis offset deterministik; `GET /api/v1/search/names?path=&q=&recursive=&…`; hasil = `Entry` + `location`.
2. `internal/search/content.go`: ripgrep `rg --json --no-ignore --hidden --no-follow --max-filesize <EDITOR_MAX_FILE_SIZE> --max-count 100 --max-columns 500 -e <pattern>` + `-i`/`-w`/`-F`/`-g '*.ext'`, dijalankan via `internal/exec` dengan cwd = path absolut folder user (dari `AbsForCLI` setelah lock read), timeout 30 s, output diparse streaming; fallback worker pool Go (`regexp`, `bufio.Scanner` dengan batas baris, skip biner via heuristik NUL) jika `RG_PATH` kosong/tidak ada; `POST /api/v1/search/content {path, q, regex, case_sensitive, whole_word, recursive, ext:[…], size_max, mtime_from}` → respons NDJSON streaming `{file, line, col, text, before, after}` + ringkasan akhir `{files, matches, partial}`; batas 2.000 kecocokan; rate limit 10/menit/user.
3. Replace: `POST /api/v1/search/replace/preview {…query, replacement}` → per file: hunks diff (unified, maks 20 file pertama penuh, sisanya hitungan) + id preview (10 menit); `POST /api/v1/search/replace {preview_id}` → task `replace_in_files`: untuk tiap file lock write, `versions.Snapshot` (pre-image, reason `replace`), tulis atomik (tmp + rename, pertahankan mode & EOL & encoding via Step 16a decoder — sebelum 16a: hanya UTF-8/ASCII, file lain dilewati dan dicatat), log per file; rollback = restore versi (UI di 16b) + log replace tersimpan di task result.
4. `internal/versions` minimal: `Snapshot(user, path, reason)` menyalin isi ke `DATA_DIR/versions/<user_id>/<version_id>` + baris `file_versions`, limit `FILE_VERSION_LIMIT` (hapus tertua), lewati jika sha256 sama dengan versi terakhir, hitung ke kuota `versions_bytes`.
5. `search_history` (migrasi `0009_search_history.sql`): 50 query terakhir per user; `GET/DELETE /api/v1/search/history`.
6. Frontend: halaman `/search` (dari Ctrl+K "Cari file", kotak global di top bar, "Cari di folder ini" dari context menu): tab Nama & Isi; filter (tipe, ekstensi, ukuran, tanggal, tag, folder, subfolder); hasil nama = Details view dengan kolom Lokasi + aksi "Buka lokasi file"; hasil isi dikelompokkan per file, baris dengan highlight, klik → buka editor di baris (Step 16a; sebelumnya buka viewer teks); tombol "Replace…" → panel replacement + preview diff (komponen diff ringan, bukan Monaco) → konfirmasi → task; riwayat pencarian (dropdown); `Ctrl+F` = filter cepat folder (7b), `Ctrl+Shift+F` = cari isi.
7. E2E `search.spec.ts`: 200 file dengan isi → cari nama dengan filter ext; cari isi regex whole-word → hasil per baris; replace preview → apply → isi berubah → restore versi (via API) → kembali.

**File yang dibuat/diubah:** `backend/migrations/0009_search_history.sql`, `backend/internal/search/{name,content,rg,fallback,replace}.go` + test, `backend/internal/versions/{store,snapshot}.go`, `backend/internal/httpapi/search.go`, `web/src/features/search/{SearchPage,NameResults,ContentResults,ReplacePanel,DiffPreview,Filters}.tsx`, `web/e2e/search.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Cari nama `lap` di 10.000 file → hasil < 2 s, `partial:false`; filter `ext=pdf` benar.
- [ ] Cari isi regex `\bTODO\b` case-insensitive → setiap kecocokan menampilkan file, baris, kolom; file > `EDITOR_MAX_FILE_SIZE` dilewati.
- [ ] Dengan `RG_PATH=` kosong hasil sama dengan ripgrep untuk fixture yang sama (test membandingkan).
- [ ] Replace di 50 file → 50 versi pre-image dibuat; restore satu versi mengembalikan isi; task log mencatat per file.
- [ ] Pola regex yang jahat (`(a+)+$`) tidak membuat server hang (RE2 / rg).
- [ ] User tidak bisa mencari di luar root (`path=../`) → `400`.

**Test wajib:** `search/name_test.go`, `search/content_test.go` (parse rg json, fallback equivalence), `search/replace_test.go` (preview, atomik, versi), `versions/snapshot_test.go` (limit, dedup, kuota), `httpapi/search_test.go` (rate limit, IDOR); Playwright `search.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm e2e`.

**Di luar scope step ini:** UI riwayat versi (16b), indeks pencarian persisten.

**Commit:** `feat(search): name and content search with ripgrep, replace in files with preview and version rollback`

---

## Step 16a — Editor Monaco inti: workspace, tab, explorer, save/save all, pengaturan, encoding, EOL, bahasa, draft lokal

> Sertakan Preamble di atas.

**Tujuan:** Code editor ala aaPanel IDE yang bisa membuka, mengedit, dan menyimpan file dengan deteksi encoding/EOL/bahasa, multi-tab, explorer, dan draft lokal.

**Prasyarat:** Step 15.

**Baca dulu:** SPEC §8.13 (editor: endpoint content, encoding, EOL, bahasa, tab, draft, pengaturan, file besar), §9.3 (wireframe editor), §9.6 (shortcut editor), §10.3 (CSP worker), §13 ADR-05; PRD epic E7.

**Tugas:**
1. Backend `GET /api/v1/files/content?path=&encoding=&tail_lines=`: baca ≤ `EDITOR_MAX_FILE_SIZE`; deteksi biner (NUL dalam 8 KB pertama / rasio non-teks) → `415 BINARY_FILE` (`details.download_url`); deteksi encoding: BOM (UTF-8/16LE/16BE) → `chardet` (UTF-8, GBK, Big5, Shift_JIS, ISO-8859-1, Windows-1252) dengan `encoding` override; dekode ke UTF-8 (`x/text/encoding`); deteksi EOL (`LF|CRLF|mixed`); respons `{content, encoding, bom, eol, size, mtime, etag, language_hint, truncated}`; file > batas → mode tail (`tail_lines` default 1.000, `truncated:true`, `offset`) read-only; `GET …&more=` untuk memuat lebih banyak.
2. `PUT /api/v1/files/content {path, content, encoding, bom, eol}` + header `If-Match: <etag>` → encode sesuai encoding (gagal → `400 VALIDATION_FAILED` karakter tak terwakili, dengan posisi), konversi EOL eksplisit, tulis atomik (tmp + rename, mode dipertahankan), `versions.Snapshot` pre-image sebelum menimpa (reason `save`), lock write; etag beda → `409 CONFLICT` (`details:{server_etag, server_mtime}`) kecuali `force=true`; respons `{etag, mtime, size, version_id}`; file baru (belum ada) dibuat jika `create=true`.
3. `editor_sessions` (migrasi `0010_editor_sessions.sql`): tab terbuka per user (path, order, cursor, scroll, active) — `GET/PUT /api/v1/me/editor-session`.
4. Frontend `/editor?path=` (workspace penuh) dan mode modal besar (dari browser: "Buka dengan → Editor", bisa maximize): layout SPEC §9.3 — toolbar (Save, Save all, Find, Replace, Go to line, Format, Font size ±, Theme, Settings), explorer sidebar (tree folder lazy, filter nama, context menu buat/rename/hapus file-folder yang memanggil API 9a dan mensinkronkan query file manager), tab bar (● belum disimpan, close, close others, close right, close all, reorder drag, maks 20, Alt+W, middle-click), dialog Simpan/Jangan simpan/Batal saat tutup tab kotor, status bar (Ln/Col, encoding (klik → Reopen with encoding / Save with encoding), EOL (klik → ubah LF/CRLF), Spaces/Tabs + ukuran, bahasa (klik → ganti), ukuran file, lokasi, Riwayat (n) placeholder 16b, Read-only).
5. Monaco: `monaco-editor` via dynamic import (chunk terpisah), workers via Vite `?worker` (`editor`, `json`, `css`, `html`, `ts`) — tanpa CDN, CSP `worker-src 'self' blob:`; tema light/dark mengikuti aplikasi + 2 pilihan tambahan; opsi dari preferensi (font JetBrains Mono default/Consolas/Menlo/Ubuntu Mono, ukuran, tab size, insertSpaces, wordWrap, minimap, renderWhitespace, lineNumbers) tersimpan server (`me/preferences` kunci `editor.*`); multi-cursor, folding, bracket matching bawaan; find/replace bawaan (Ctrl+F/H di editor); `Ctrl+P` quick open (daftar file dari explorer) dengan preventDefault.
6. Bahasa: pemetaan ekstensi/shebang/nama file (`Dockerfile`, `Makefile`, `.env`, `nginx.conf`, `*.conf` di folder nginx, `.htaccess`, `*.blade.php`) → id bahasa Monaco; grammar Monarch kustom `nginx`, `apache` (`.htaccess`/httpd.conf), `ini/env`, `blade` (fallback php+html), `verilog`, `vbscript`, `r` jika tidak bawaan (verifikasi daftar bahasa Monaco saat implementasi); semua mode daftar SPEC §8.13 terpetakan.
7. Draft lokal: autosave isi tab kotor ke IndexedDB tiap 2 s (key user+path+etag); saat membuka file yang punya draft → banner "Pulihkan draft?" (lihat diff sederhana / pulihkan / buang); draft dihapus setelah simpan sukses. Auto-save ke server opsional (default off, interval 30 s).
8. Simpan: `Ctrl+S` (hanya tab aktif), `Ctrl+Shift+S` semua; indikator menyimpan; error konflik → placeholder dialog (lengkap di 16b: untuk sekarang pilihan Timpa / Muat ulang).
9. Integrasi: dari file manager "Buka" pada file teks/kode → editor (modal) ; hasil search "isi" → `/editor?path=&line=&col=`; file biner → pesan + tombol Download; file > batas → tail mode read-only + "muat lebih banyak".
10. E2E `editor.spec.ts`: buka 3 file di tab, edit, Ctrl+S, reload → tab dipulihkan & isi tersimpan; ganti EOL ke CRLF → simpan → byte `\r\n`; file GBK → terbaca benar → Save with encoding UTF-8; draft dipulihkan setelah reload tanpa simpan.

**File yang dibuat/diubah:** `backend/migrations/0010_editor_sessions.sql`, `backend/internal/files/{content,encoding,textdetect}.go` + test, `backend/internal/httpapi/{content,editor_session}.go`, `web/src/features/editor/{EditorWorkspace,EditorModal,MonacoHost,TabBar,Explorer,Toolbar,StatusBar,SettingsDialog,EncodingMenu,languages/*,monarch/{nginx,apache,…}.ts,drafts.ts,useEditorSession.ts}.tsx`, `web/vite.config.ts` (chunk Monaco), `web/e2e/editor.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Bundle: Monaco berada di chunk terpisah yang hanya dimuat saat editor dibuka (cek network); JS awal tetap < 300 KB gzip.
- [ ] File CRLF dibuka dan disimpan tanpa konversi diam-diam (byte identik jika tidak diubah); ubah ke LF hanya lewat status bar.
- [ ] File Shift_JIS dan UTF-16LE BOM terbaca benar; simpan kembali dengan encoding asal menghasilkan byte identik untuk isi yang tidak diubah.
- [ ] File 6 MB (> `EDITOR_MAX_FILE_SIZE` 5 MB) dibuka tail 1.000 baris read-only; "muat lebih banyak" bekerja; file biner → `BINARY_FILE` + Download.
- [ ] `PUT` dengan `If-Match` lama → `409 CONFLICT`; tiap simpan membuat satu versi pre-image (cek `file_versions`), simpan tanpa perubahan tidak membuat versi.
- [ ] 21 tab → ditolak dengan toast; tab & posisi kursor dipulihkan setelah reload.
- [ ] `nginx.conf` mendapat highlight (Monarch) dan `.htaccess` juga.

**Test wajib:** `files/encoding_test.go` (deteksi & round-trip tiap encoding, BOM, EOL), `files/textdetect_test.go`, `httpapi/content_test.go` (etag/conflict/force/versi/tail/binary/IDOR); Vitest `languages.test.ts`, `drafts.test.ts`, `TabBar.test.tsx`; Playwright `editor.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm test && pnpm build && pnpm e2e`.

**Di luar scope step ini:** Diff/riwayat versi UI, find in files panel, Markdown preview (16b).

**Commit:** `feat(editor): Monaco workspace with tabs, explorer, encoding/EOL handling, language detection, local drafts`

---

## Step 16b — Editor lanjutan: konflik + diff, riwayat versi + restore, find in files, go to line, format, Markdown preview, tail mode

> Sertakan Preamble di atas.

**Tujuan:** Melengkapi editor dengan deteksi konflik berbasis diff, riwayat versi (daftar, diff side-by-side, restore), find in files, format document, dan preview Markdown.

**Prasyarat:** Step 16a.

**Baca dulu:** SPEC §8.13 (versi, konflik, find in files, format, Markdown), §8.12 (API content search), §6.3 `files/versions`, `versions/{id}`; PRD epic E7, E11.

**Tugas:**
1. Backend: `GET /api/v1/files/versions?path=` (daftar: id, created_at, size, reason `save|replace|restore|upload_overwrite`, sha256), `GET /api/v1/versions/{id}/content` (teks, dengan encoding yang sama seperti `content`), `POST /api/v1/versions/{id}/restore` (snapshot isi sekarang sebagai versi `restore` lalu tulis versi lama; lock; etag baru), `DELETE /api/v1/versions/{id}`; versi ikut `UpdateReferences` pada rename/move (sudah via tabel), dihapus saat file dipurge dari trash; kuota `versions_bytes`.
2. Hook: overwrite oleh upload (12a `on_conflict=overwrite`) dan replace (15) juga membuat versi pre-image untuk file teks ≤ `EDITOR_MAX_FILE_SIZE` (reason sesuai).
3. Frontend konflik: pada `409 CONFLICT` tampilkan dialog dengan Monaco DiffEditor (kiri: versi server terbaru, kanan: milikmu), tombol "Timpa" (`force=true`), "Muat ulang" (buang perubahan lokal, draft disimpan dulu), "Gabung manual" (buka diff editable).
4. Riwayat versi: panel/dialog dari status bar "Riwayat (n)" dan dari Properties → tab Riwayat versi: daftar (waktu relatif + absolut, ukuran, alasan), "Buka read-only", "Bandingkan dengan sekarang" (DiffEditor side-by-side, toggle inline), "Restore" (konfirmasi), "Hapus versi".
5. Find in files: panel di explorer (ikon kaca pembesar): query + regex/case/whole word + glob → memakai `POST /search/content` dibatasi folder explorer; hasil tree file → baris; klik → buka tab di baris & kolom (highlight); "Replace in files" dari panel ini memakai preview Step 15 (buka dialog preview).
6. Go to line (`Ctrl+G`) dialog Monaco bawaan diaktifkan; command palette editor `F1` (Monaco) + aksi aplikasi (Save all, Close others, Toggle preview, Reopen with encoding) didaftarkan sebagai Monaco actions.
7. Format document: Monaco built-in (JSON, CSS/SCSS/LESS, HTML, JS/TS); Prettier (lazy-load chunk) opsional untuk Markdown/YAML/PHP plugin jika tersedia — toggle di settings; shortcut `Shift+Alt+F`.
8. Markdown: tombol "Preview" (side-by-side, scroll sync kasar) memakai `marked` + DOMPurify (profil ketat: tanpa script/iframe/event handler/`javascript:`; gambar hanya dari origin sendiri lewat signed URL relatif path markdown jika file ada di folder yang sama, selain itu placeholder).
9. Tail mode UI lengkap: indikator read-only + alasan, tombol "muat lebih banyak" (append ke atas), "Download untuk mengedit".
10. Editor untuk folder yang dibagikan (18b akan mengaktifkan): komponen menerima `scope={share}` sejak sekarang (prop), tanpa UI khusus.
11. E2E `editor-advanced.spec.ts`: dua sesi (dua context) mengedit file sama → konflik → diff → timpa; riwayat 3 versi → diff → restore → isi kembali; find in files → klik → kursor di baris; Markdown preview tidak menjalankan `<script>` (cek DOM).

**File yang dibuat/diubah:** `backend/internal/versions/{api,restore}.go`, `backend/internal/httpapi/versions.go`, `backend/internal/upload/hooks.go` (versi saat overwrite), `web/src/features/editor/{ConflictDialog,VersionHistory,FindInFiles,MarkdownPreview,TailBanner,commands.ts}.tsx`, `web/src/features/properties/VersionsTab.tsx`, `web/e2e/editor-advanced.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Simpan 60 kali file berbeda isi → tepat `FILE_VERSION_LIMIT` (50) versi tersisa, tertua dihapus; kuota `versions_bytes` sesuai jumlah byte di `DATA_DIR/versions/<uid>`.
- [ ] Restore versi membuat versi baru `restore` dari isi sekarang; etag berubah; tab terbuka memuat ulang isi.
- [ ] Konflik: setelah "Timpa", server berisi versi milikmu dan versi pre-image server tersimpan.
- [ ] Find in files di folder dengan 1.000 file < 3 s; klik hasil menempatkan kursor tepat (baris & kolom).
- [ ] Markdown dengan `<script>`, `<img onerror>`, `javascript:` link → tidak ada eksekusi; HTML hasil hanya tag yang diizinkan.
- [ ] Format JSON yang tidak valid → pesan error, isi tidak berubah.

**Test wajib:** `versions/api_test.go` (limit, restore chain, delete, kuota, IDOR), `httpapi/versions_test.go`; Vitest `MarkdownPreview.test.tsx` (sanitasi), `VersionHistory.test.tsx`; Playwright `editor-advanced.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Viewer non-editor (17).

**Commit:** `feat(editor): conflict diff, version history with restore, find in files, formatting, markdown preview`

---

## Step 17a — Viewer gambar (zoom/pan/rotate/slideshow/EXIF) + PDF (pdf.js, Range, password)

> Sertakan Preamble di atas.

**Tujuan:** Kerangka viewer overlay yang dipakai semua tipe, plus image viewer dan PDF viewer lengkap.

**Prasyarat:** Step 16b (signed media URL util dari 12b, thumbs/preview variant dari 8/12b).

**Baca dulu:** SPEC §8.14 (viewer: kerangka overlay, pemetaan MIME → viewer, image, PDF), §8.9 (`/raw` variant), §8.4 (EXIF di `media_metadata`), §9.6 (shortcut viewer), §10.3 (CSP worker pdf.js); PRD epic E8.

**Tugas:**
1. Kerangka `web/src/features/viewer/ViewerOverlay.tsx`: overlay layar penuh (route query `?view=<path>` → deep link; back menutup), header (nama, index/total, tombol Download, Share (placeholder 18a), Info, Edit (teks), Close Esc), navigasi prev/next ke file **sejenis** (kategori viewer sama) di folder yang sama (urutan = sort aktif; memuat halaman berikut jika perlu), `←/→`, `Space` (Quick Look di 17b), fokus trap, `aria-modal`, pemetaan MIME → viewer dari SPEC §8.14 (registry `viewers.ts`), fallback "kartu info + download".
2. Image viewer: sumber `getMediaUrl(path, variant=preview)` (≤ 2048 px dari server) lalu original saat zoom ≥ 100 % atau tombol 1:1; format jpg/jpeg/png/gif (animasi tetap via original)/webp/avif/bmp/ico langsung; svg/tiff/heic → selalu `variant=preview` raster (server; "perlu verifikasi dukungan heic libvips" → fallback kartu info); zoom (scroll/pinch/tombol/1:1/fit/fill), pan (drag), rotate 90° kiri/kanan, flip H/V, fullscreen API, slideshow (interval 3/5/10 s, tombol play/pause), filmstrip thumbnail bawah (thumb 128 dari pipeline), preload gambar berikutnya; shortcut `+/-/0/F/R/Shift+R`.
3. Backend: `GET /api/v1/files/media-info?path=` → metadata dari `media_metadata` + EXIF (kamera, lensa, ISO, aperture, shutter, tanggal asli, orientasi; GPS hanya jika `include_gps=1`); EXIF diekstrak via library Go (ADR-10, verifikasi) saat probe dan disimpan `exif_json` (GPS disimpan tetapi dikirim hanya atas permintaan); orientasi EXIF diterapkan oleh `vipsthumbnail` (`--rotate`) untuk preview.
4. Panel Info (sidebar kanan di overlay): dimensi, ukuran, tipe, tanggal, EXIF, toggle "Tampilkan lokasi GPS" (default off), tombol "Buka lokasi file".
5. PDF viewer: `pdfjs-dist` lazy chunk, worker self-hosted (`?url`), sumber via signed URL dengan `Range` (pdf.js `rangeChunkSize` 64 KB, `disableAutoFetch` untuk file besar), render halaman tervirtualisasi (hanya ±2 halaman di sekitar viewport), sidebar thumbnail halaman (lazy), outline/bookmark, zoom (fit width/fit page/persen, Ctrl+scroll), cari teks dengan highlight & next/prev, lompat ke halaman (input), rotasi, print (render ke canvas → `window.print` di iframe tersembunyi; "perlu verifikasi" kualitas), download, password prompt (`PasswordResponses`), layer teks untuk seleksi/copy, annotation layer tampilan saja (tanpa JS PDF: `enableScripting:false`).
6. Integrasi browser: double-click/Enter pada gambar/PDF → overlay; panel preview (7b) menampilkan thumbnail 256 + tombol "Buka"; Quick Look (Space) placeholder → overlay.
7. E2E `viewer-image-pdf.spec.ts`: buka jpg → zoom/rotate → next → png → slideshow; info EXIF tampil, GPS tersembunyi sampai toggle; buka PDF 20 halaman → sidebar → cari teks → halaman berubah; PDF ber-password → prompt → terbuka; URL `?view=` langsung membuka overlay.

**File yang dibuat/diubah:** `web/src/features/viewer/{ViewerOverlay,viewers.ts,useSiblingNav,InfoPanel,image/{ImageViewer,Filmstrip,useZoomPan},pdf/{PdfViewer,PdfSidebar,PdfToolbar,PdfSearch}}.tsx`, `backend/internal/media/exif.go`, `backend/internal/httpapi/media_info.go`, `web/vite.config.ts` (chunk pdf.js), `web/e2e/viewer-image-pdf.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Gambar 24 MP tampil < 1 s dari variant preview; zoom 100 % memuat original (cek network); gambar berikutnya di-preload.
- [ ] SVG dengan `<script>` ditampilkan sebagai raster; tidak ada request `image/svg+xml` inline ke origin aplikasi.
- [ ] GPS tidak ada di respons `media-info` tanpa `include_gps=1`.
- [ ] PDF 100 MB mulai tampil halaman 1 sebelum unduhan selesai (request Range terlihat di network); memori tab stabil saat scroll 500 halaman (virtualisasi).
- [ ] pdf.js worker dimuat dari origin sendiri; CSP tidak dilanggar (console bersih).
- [ ] Overlay dapat ditutup Esc, fokus kembali ke item; axe pada overlay 0 serius.

**Test wajib:** Go `media/exif_test.go` (fixture JPEG dengan EXIF & GPS), `httpapi/media_info_test.go`; Vitest `useZoomPan.test.ts`, `viewers.test.ts` (pemetaan MIME), `useSiblingNav.test.ts`; Playwright `viewer-image-pdf.spec.ts`.

**Verifikasi:** `make test && pnpm test && pnpm build && pnpm e2e`.

**Di luar scope step ini:** Audio/video/teks (17b), share dari viewer (18a).

**Commit:** `feat(viewer): overlay framework, image viewer with EXIF, pdf.js viewer with range streaming`

---

## Step 17b — Music player + mini-player, video player, viewer teks/Markdown/CSV/JSON, Quick Look

> Sertakan Preamble di atas.

**Tujuan:** Pemutar audio dengan playlist dan mini-player persisten, pemutar video dengan subtitle/PiP/resume, viewer teks/Markdown/CSV/JSON/HTML-source/Office-info, dan Quick Look.

**Prasyarat:** Step 17a.

**Baca dulu:** SPEC §8.14 (audio, video, viewer lain, Quick Look), §8.9 (media URL TTL & refresh), §9.6, §13 ADR-07 (Video.js 10, fallback media-chrome; Vidstack/Plyr deprecated); PRD epic E8.

**Tugas:**
1. Backend: `GET /api/v1/files/subtitle?path=&exp=&sig=` → cari sidecar `<nama>.vtt`/`<nama>.srt` (srt → vtt dikonversi di server, sanitasi: hanya teks + tag cue dasar), `text/vtt`; `media-info` untuk audio mengembalikan tag (judul, artis, album, track, tahun) dari `ffprobe` + cover via `variant=thumb`; posisi tonton `PUT/GET /api/v1/me/media-position {path, seconds}` (maks 500 entri, LRU).
2. Music player (`HTMLAudioElement` + UI custom): format mp3/m4a-aac/ogg/opus/wav/flac dengan `canPlayType` → pesan jelas bila tidak didukung + download; kontrol play/pause/prev/next/seek/volume/mute/shuffle/repeat off-all-one/kecepatan 0.5–2×; playlist otomatis dari folder (urutan sort aktif), drag reorder, "Tambah ke playlist" dari seleksi (context menu), kosongkan; metadata + cover; Media Session API (judul/artis/cover/aksi); **mini-player persisten** di bawah layar (di atas status bar; tetap memutar saat pindah folder/route; expand ke overlay penuh); store `player`.
3. Video player: Video.js 10 via `@videojs/react` (lazy chunk; Vidstack dan Plyr **tidak** dipakai karena keduanya deprecated di npm per 14 Okt 2026; verifikasi ulang status `@videojs/react` dan lisensinya di registry — jika bermasalah, media-chrome sesuai ADR-07, catat di PROGRESS & SPEC §13); sumber signed URL (TTL 2 h) dengan Range; deteksi codec: `canPlayType` + codec dari `media-info` (H.264/AAC, VP8/VP9/AV1/Opus; HEVC/MKV/AVI → pesan "tidak didukung browser ini" + download); kecepatan 0.25–2×, fullscreen, PiP, subtitle sidecar (pilih track), lanjut dari posisi terakhir (prompt kecil "Lanjutkan dari 12:34?"), playlist video folder (next otomatis), poster dari thumbnail; shortcut Space, ←/→ 5 s, J/K/L, F, M; refresh URL otomatis pada 403 lalu seek ke posisi sebelumnya.
4. Viewer teks/kode read-only: highlighter ringan (lazy per bahasa; bukan Monaco) + nomor baris + tombol "Edit" (buka editor) + wrap toggle; Markdown dirender (`marked` + DOMPurify, sama seperti 16b); CSV/TSV → tabel tervirtualisasi (parser streaming, delimiter auto, 100k baris, header toggle); JSON → tree (collapse/expand, cari kunci, ukuran ≤ 5 MB); HTML → tampilkan source (preview live dinonaktifkan sampai 23e, tampilkan catatan); Office (docx/xlsx/pptx) → kartu info + download; arsip → Archive browser (14b); tipe lain → kartu info.
5. Quick Look (`Space`): modal ringan di atas browser (bukan overlay penuh) memakai viewer yang sama dalam mode compact (gambar, PDF halaman 1–3, teks 200 baris pertama, audio mini, video muted preview, folder → ringkasan isi); `Space` lagi/Esc menutup; panah berpindah item.
6. Panel preview (7b) memakai komponen compact yang sama (gambar/teks/metadata).
7. E2E `viewer-media.spec.ts`: putar mp3 → pindah folder → masih berputar (mini-player) → playlist next; video mp4 H.264 → seek → PiP tersedia → subtitle `.srt` tampil → tutup → buka lagi → prompt lanjutkan; mkv HEVC → pesan tidak didukung + download; CSV 10k baris scroll; JSON tree; Quick Look txt.

**File yang dibuat/diubah:** `backend/internal/httpapi/{subtitle,media_position}.go`, `backend/internal/media/srt2vtt.go` + test, `web/src/features/viewer/{audio/{AudioPlayer,MiniPlayer,Playlist},video/{VideoPlayer,codec.ts},text/{CodeViewer,MarkdownViewer,CsvViewer,JsonTree,HtmlSource},InfoCard,QuickLook}.tsx`, `web/src/stores/player.ts`, `web/e2e/viewer-media.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Audio terus berputar saat navigasi folder & buka dialog; Media Session menampilkan judul/artis/cover (cek `navigator.mediaSession.metadata`).
- [ ] Video: seek instan (request `206`), subtitle srt tampil sebagai VTT, posisi dilanjutkan, URL yang kedaluwarsa (simulasi TTL 10 s di test) di-refresh tanpa reload halaman.
- [ ] File dengan codec tak didukung → pesan jelas + tombol download, bukan layar hitam.
- [ ] CSV 100k baris dibuka tanpa freeze (virtualisasi); JSON 5 MB → tree; Markdown tersanitasi.
- [ ] Quick Look membuka dalam < 300 ms untuk txt/gambar yang sudah di-cache.
- [ ] Chunk Video.js hanya dimuat saat video dibuka.

**Test wajib:** Go `media/srt2vtt_test.go` (format, sanitasi), `httpapi/subtitle_test.go` (sidecar lookup, IDOR); Vitest `codec.test.ts`, `CsvViewer.test.tsx`, `JsonTree.test.tsx`, `player.test.ts` (repeat/shuffle); Playwright `viewer-media.spec.ts`.

**Verifikasi:** `make test && pnpm test && pnpm build && pnpm e2e`.

**Di luar scope step ini:** Transcoding (23d), preview HTML live (23e), preview Office.

**Commit:** `feat(viewer): audio player with persistent mini-player, video player with subtitles and resume, text/csv/json viewers, quick look`

---

## Step R-D — Review independen Step 15–17b

> Sertakan Preamble di atas (mode reviewer).

**Tujuan:** Reviewer memeriksa search/replace, editor, versi, dan viewer — fokus kebenaran data (encoding/EOL/versi), XSS lewat konten user (Markdown, SVG, subtitle, nama file), dan bundle/lazy-load.

**Prasyarat:** Step 15–17b tercentang.

**Baca dulu:** PRD epic E7, E8, E9, E11; SPEC §8.12–8.14, §10.3; PROGRESS catatan Step 15–17b; review-C minor tertunda.

**Tugas:**
1. Jalankan seluruh suite; `pnpm build` dan catat ukuran chunk (awal, Monaco, pdf.js, video player).
2. Checklist R-D: (a) XSS: nama file dengan `<img src=x onerror=…>` di semua tempat (tabel, breadcrumb, tab editor, viewer header, toast, notifikasi, task panel, panel transfer); Markdown & subtitle & JSON tree dengan payload jahat; SVG tidak pernah inline; HTML selalu source; (b) encoding/EOL: round-trip byte-identik untuk 8 encoding; file campuran EOL; BOM dipertahankan; (c) versi: limit, dedup, restore chain, kuota, IDOR; tidak ada kebocoran versi lintas user; (d) replace: atomik, versi pre-image, file biner dilewati, file yang berubah saat preview → apply mendeteksi (etag) dan melewati; (e) ripgrep: argumen pengguna tidak bisa menjadi flag (`-e` selalu dipakai, `--` ada), cwd = root user, `--no-follow`; fallback setara; (f) editor: `If-Match` wajib (tanpa header → tolak), draft tidak bocor antar user di browser yang sama (key per user), tab dipulihkan hanya untuk file yang masih ada; (g) viewer: signed URL purpose `media` tidak bisa dipakai untuk download/zip, EXIF GPS default tersembunyi, subtitle sanitasi; (h) CSP: console bersih di editor/pdf/video; worker dari origin sendiri; (i) a11y: overlay fokus trap, shortcut tidak bentrok dengan input pencarian pdf; (j) performa: Monaco/pdf.js/video lazy; JS awal < 300 KB gzip.
3. Tulis `docs/reviews/review-D.md`; perbaikan `fix(review-D): …` + regresi.

**File yang dibuat/diubah:** `docs/reviews/review-D.md`, perbaikan + test.

**Acceptance criteria:**
- [ ] `docs/reviews/review-D.md` ada; tidak ada blocker/major terbuka.
- [ ] Semua suite lulus; ukuran chunk tercatat; JS awal < 300 KB gzip.

**Test wajib:** Regresi per temuan, termasuk fixture XSS nama file di Playwright.

**Verifikasi:** `make test && make lint && pnpm test && pnpm build && pnpm e2e`.

**Di luar scope step ini:** Fitur baru.

**Commit:** `docs(review): independent review D` lalu `fix(review-D): …`

---

# Milestone M4 — Sharing & admin

## Step 18a — Sharing: link publik (token, password, expiry, limit download, file request) + halaman publik `/s/<token>`

> Sertakan Preamble di atas.

**Tujuan:** Berbagi file/folder lewat link publik yang aman (token ter-hash, password argon2id, masa berlaku, batas download, nonaktif sementara) dengan halaman publik minimal untuk tamu, termasuk file request (upload lewat link).

**Prasyarat:** Step 17b.

**Baca dulu:** SPEC §8.15 (sharing: token, password, cookie share, halaman publik, file request, konsistensi metadata, log akses), §4.2 `shares`, `share_access_logs`, §6.6 (share publik), §6.3 grup `shares`, §10.3 (header halaman publik), §10.5 (rate limit password share), §13 ADR-16; PRD epic E10.

**Tugas:**
1. Migrasi `0011_shares.sql`: `shares`, `share_recipients` (dipakai 18b), `share_access_logs` sesuai SPEC §4.2 (termasuk `token_hash` unique, `slug` unique nullable, `password_hash`, `expires_at`, `max_downloads`, `download_count`, `view_count`, `allow_download`, `allow_upload`, `allow_reshare`, `hide_owner`, `disabled_at`, `broken_at`, `last_access_at`, `note`).
2. `internal/share`: `Create` (token 32 B acak → base64url; simpan SHA-256; slug kustom opsional `^[a-z0-9][a-z0-9-]{2,63}$` + daftar terlarang; password argon2id m=19 MiB t=2 p=1; expiry: 1/7/30 hari/custom/tanpa batas — admin bisa melarang tanpa batas & mewajibkan password via settings), `Update`, `Disable/Enable`, `Delete`, `List(owner)`, pendaftaran `ReferenceUpdater` (rename/move memperbarui `shares.path`; hapus → `disabled_at` + flag; restore dari trash → respons `reactivate_shares` di 10a kini terisi + endpoint `POST /shares/{id}/enable`), `broken_at` saat path tidak ada (dicek saat akses & job harian).
3. Endpoint pemilik: `POST /api/v1/shares`, `GET /api/v1/shares?cursor=`, `GET /api/v1/shares/{id}`, `PATCH /api/v1/shares/{id}`, `DELETE`, `POST /shares/{id}/disable|enable`, `GET /shares/{id}/access-logs?cursor=` (waktu, IP dipotong, aksi, user_agent ringkas), `GET /api/v1/files/share-status?paths=[…]` untuk badge.
4. Endpoint publik (tanpa sesi; rate limit per IP 60/menit): `GET /api/v1/public/shares/{token}` → meta (nama, tipe, ukuran, pemilik jika tidak disembunyikan, `requires_password`, `allow_download`, `allow_upload`, `expires_at`) atau `410 SHARE_EXPIRED` / `403 SHARE_DISABLED` / `404`; `POST …/unlock {password}` (rate limit 10/10 menit per IP+link, delay progresif) → cookie `fm_share_<id>` = HMAC(APP_SECRET-derived, share_id|exp) `HttpOnly; Secure; SameSite=Lax; Path=/s/<token>` dan `Path=/api/v1/public/shares/<token>` (dua cookie), umur 1 jam; `GET …/list?path=&sort=&cursor=` (hanya di dalam subtree; `path` relatif ke item; parent di atas item tidak pernah dikembalikan), `GET …/raw`, `GET …/download` (meningkatkan `download_count`; > `max_downloads` → `403 SHARE_LIMIT_REACHED`), `GET …/zip`, `GET …/thumb`, `POST …/upload/tus/` (jika `allow_upload`; `upload_sessions.share_id`; kuota pemilik; nama disanitasi; konflik → rename otomatis; tidak boleh overwrite), log akses setiap aksi.
5. Akses publik memakai `fsjail.Sub` di root pemilik pada path item; `Content-Disposition` & kebijakan inline sama dengan 12b; pemilik nonaktif → semua share `403`.
6. Halaman publik `/s/:token/*` (route SPA, tanpa sidebar/top bar aplikasi, `<meta name="robots" content="noindex">` + header `X-Robots-Tag: noindex`, `Referrer-Policy: no-referrer`): kartu file (ikon/thumbnail, nama, ukuran, pemilik opsional, tanggal), tombol Preview (viewer Step 17 dalam mode publik dengan sumber `public/.../raw`) & Download; folder → browser read-only (Details/Large icons, breadcrumb di dalam share, sort, download zip, upload jika file request dengan panel transfer); form password; status kedaluwarsa/nonaktif; catatan "Mode preview saja hanya menyembunyikan tombol download dan tidak mencegah penyimpanan" ditampilkan untuk pemilik saat membuat share.
7. Frontend pemilik: dialog Share (mode Link publik; opsi: password, masa berlaku, batas download, izinkan download, izinkan upload (folder), sembunyikan pemilik, slug kustom, catatan), hasil: link + "Salin link" + QR (library klien kecil, lazy), tombol Nonaktifkan/Hapus; aksi context menu Share & "Salin link share" (jika sudah ada); badge share di kolom Nama & kolom Status share; aksi di viewer header.
8. Audit: `share.created|updated|disabled|deleted`, `share.public_access` (ringkas), `share.password_failed`.
9. E2E `share-public.spec.ts`: buat link folder ber-password 1 hari → buka di context tanpa login → password salah ×3 → benar → browse → download file → zip → `max_downloads` tercapai → `SHARE_LIMIT_REACHED`; link file preview-only → tidak ada tombol download; nonaktifkan → `403`; rename folder → link tetap bekerja; hapus item → link nonaktif.

**File yang dibuat/diubah:** `backend/migrations/0011_shares.sql`, `backend/internal/share/*` + test, `backend/internal/httpapi/{shares,public_share}.go`, `backend/internal/upload/hooks.go` (share upload), `web/src/features/share/{ShareDialog,ShareLinkResult,QrCode,ShareBadge}.tsx`, `web/src/features/public/{PublicPage,PublicBrowser,PasswordForm}.tsx`, `web/src/routes/s.tsx`, `web/e2e/share-public.spec.ts`, `docs/openapi.yaml`, `docs/SECURITY.md` (share).

**Acceptance criteria:**
- [ ] DB tidak menyimpan token mentah (`token_hash` saja); link `/s/<token>` 43 karakter base64url.
- [ ] 11 percobaan password salah dalam 10 menit → `429` dengan `Retry-After`; cookie share tidak berlaku untuk link lain.
- [ ] `GET public/.../list?path=../` → `400`; path di atas item tidak pernah tampil di breadcrumb/respons.
- [ ] Upload lewat file request dihitung ke kuota pemilik dan tidak bisa menimpa file yang ada.
- [ ] Share user A tidak bisa dibaca/diubah user B lewat id (`404`); admin dapat `GET /api/v1/admin/shares` (19b) — belum ada sekarang.
- [ ] Halaman publik mengirim `X-Robots-Tag: noindex` dan `Referrer-Policy: no-referrer`; axe 0 serius.

**Test wajib:** `share/*_test.go` (token/hash/slug/password/expiry/limit/disable/broken/refs), `httpapi/public_share_test.go` (semua kode error, rate limit, cookie scope, subtree escape, upload rules), `httpapi/shares_test.go` (IDOR); Playwright `share-public.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Share ke user (18b), admin melihat semua share (19b).

**Commit:** `feat(share): public share links with password, expiry, download limits, file requests, and public page`

---

## Step 18b — Share ke user, "Dibagikan ke saya" (viewer/editor via fsjail sub-root), "Share saya", QR, log akses, badge

> Sertakan Preamble di atas.

**Tujuan:** Berbagi ke user terdaftar dengan permission Viewer/Editor, halaman penerima, halaman manajemen share pemilik, dan notifikasi.

**Prasyarat:** Step 18a.

**Baca dulu:** SPEC §8.15 (share ke user, autocomplete privasi, akses penerima lewat `?share=`), §5.3 (`Sub`), §6.3 grup `shares` (recipients), `shared-with-me`, `users/suggest`, §8.16 (notifikasi share); PRD epic E10, E15.

**Tugas:**
1. Backend: `shares.mode='users'` + `share_recipients` (share_id, recipient_user_id, permission `viewer|editor`, note, expires_at nullable, created_at); `POST /api/v1/shares` mode users `{path, recipients:[{username, permission}], note?, expires_at?, allow_reshare:false}`; `PATCH` menambah/menghapus penerima/permission; `GET /api/v1/users/suggest?q=` (≥ 2 karakter, maks 10, hanya `username` + `display_name`, user aktif, rate limit 30/menit, tidak termasuk diri sendiri); notifikasi `share.received` ke penerima (SSE + lonceng); pencabutan → notifikasi `share.revoked`.
2. `GET /api/v1/shared-with-me` → dikelompokkan per pemilik (username, display_name) → item (share_id, nama, tipe, permission, note, expires_at, broken); tidak ada info kuota/item lain pemilik.
3. Scope share pada endpoint files: semua endpoint `files/*`, `thumb`, `download`, `raw`, `zip`, `content`, `versions`, `archives/*`, `search/*`, `upload/tus` menerima `?share=<share_id>` (atau metadata tus `share`): middleware `ResolveScope` memverifikasi penerima aktif & belum kedaluwarsa, membuka `jail := ownerJail.Sub(share.path)` (file tunggal → jail pada folder induk dengan pembatasan nama), menetapkan `scope.Perm`; operasi tulis (mkdir/create/rename/copy/move/delete/content PUT/upload/extract/compress/batch-rename) hanya untuk `editor`; delete di share → trash **pemilik** (dicatat `deleted_by`); kuota = pemilik; `UpdateReferences` tetap milik pemilik; `.system` dan path di atas item tidak tercapai (uji escape).
4. Penerima dapat favorit/pin item share (`favorites.share_id` nullable + path relatif), tidak bisa reshare kecuali `allow_reshare`; Recent/Aktivitas penerima mencatat aksi di share.
5. Frontend: dialog Share tab "Ke user" (input username autocomplete, chip penerima, permission per chip, catatan, kedaluwarsa), halaman `/shared-with-me` (grup per pemilik, kartu/daftar item, buka → browser dengan `scope` share: toolbar sesuai permission, breadcrumb mulai dari item), `/shared-with-me/:shareId/*` route, editor/viewer/upload bekerja dengan `scope`; halaman `/shares` ("Share saya"): tabel semua share (item, mode, penerima, permission, kedaluwarsa, view/download count, akses terakhir, status) + aksi salin link, QR, edit, nonaktifkan, hapus, lihat log akses (dialog); badge share (publik/user) di listing & Properties "Status share"; notifikasi lonceng "X membagikan Y kepada Anda".
6. E2E `share-users.spec.ts`: A bagikan folder ke B (viewer) → B lihat di Dibagikan ke saya → buka → download OK → upload ditolak; A ubah ke editor → B upload & edit file di editor → A lihat perubahan; B coba `?share=` ke path `../` → 400; C (bukan penerima) → 404; A cabut → B tidak melihat lagi + notifikasi.

**File yang dibuat/diubah:** `backend/internal/share/recipients.go`, `backend/internal/httpapi/{scope,shared_with_me,users_suggest}.go`, perubahan handler files/* untuk scope, `backend/internal/notify/*`, `web/src/features/share/{ShareToUsersTab,UserPicker,MySharesPage,AccessLogDialog}.tsx`, `web/src/features/shared-with-me/*`, `web/src/lib/scope.ts` (query param propagation), `web/e2e/share-users.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] Matriks akses lulus (test tabel): pemilik / penerima viewer / penerima editor / user lain / anonim × {list, download, upload, rename, delete, content PUT, versions, archives} → hasil persis SPEC §8.15.
- [ ] `users/suggest?q=a` → `400`; `q=bu` → maks 10 hasil hanya username+display_name; 31 request/menit → `429`.
- [ ] Penerima editor menghapus file → masuk trash pemilik dengan `deleted_by`; kuota pemilik berkurang/berpindah.
- [ ] Rename folder yang dibagikan oleh pemilik → penerima tetap bisa membuka (path diperbarui).
- [ ] Penerima tidak pernah menerima path absolut pemilik atau path di atas item (grep respons).
- [ ] Notifikasi realtime muncul di sesi B saat A membagikan.

**Test wajib:** `share/recipients_test.go`, `httpapi/scope_test.go` (matriks akses + escape + kedaluwarsa + user nonaktif), `httpapi/users_suggest_test.go`; Playwright `share-users.spec.ts` (dua context browser).

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Admin melihat/mencabut share (19b).

**Commit:** `feat(share): share to users with viewer/editor permissions, shared-with-me, my shares management`

---

## Step 19a — Admin: dashboard, users (CRUD, reset password, nonaktif, hapus/arsip), settings override

> Sertakan Preamble di atas.

**Tujuan:** Panel admin untuk mengelola user, kuota, dan setting yang menimpa default `.env`, dengan proteksi server-side.

**Prasyarat:** Step 18b.

**Baca dulu:** SPEC §8.17 (admin: dashboard, users, settings, aksi user, aturan admin terakhir), §3 (kolom "override admin"), §6.3 grup `admin`, §4.2 `settings`, §10.4; PRD epic E12, E2.

**Tugas:**
1. `internal/admin`: dashboard aggregator `GET /api/v1/admin/dashboard` → user aktif/nonaktif, storage terpakai (sum `quota_usage`) vs kapasitas disk (`unix.Statfs(STORAGE_ROOT)`: total/free), top 10 user per pemakaian, task berjalan (semua user), share publik aktif, login gagal 24 jam, versi aplikasi + Go, status dependency (`check-deps` runtime: ffmpeg, ffprobe, vipsthumbnail, pdftoppm, 7zz, rg, unrar, rar — ada/tidak + versi), peringatan (DATA_DIR cross-FS, `ADMIN_INITIAL_PASSWORD` masih di env, swap tidak ada, password 7z via argv jika berlaku, disk < 10 %).
2. Users: `GET /api/v1/admin/users?q=&role=&status=&cursor=` (kolom SPEC: username, display_name, role, status, quota + used, last_login_at, home_provisioned_at, created_at), `POST /admin/users {username, display_name, role, quota, temp_password?}` (validasi username regex + daftar terlarang SPEC §5.2; password sementara digenerate 16 karakter jika kosong; respons menampilkan password **sekali**; `must_change_password=1`; folder dibuat saat login pertama — tidak di sini), `PATCH /admin/users/{id}` (display_name, role, quota, status), `POST /admin/users/{id}/reset-password` (password baru sekali tampil, revoke semua sesi), `POST /admin/users/{id}/logout-all`, `POST /admin/users/{id}/disable|enable` (disable → revoke sesi, share publik miliknya `403` sampai enable), `DELETE /admin/users/{id} {confirm:"<username>", mode:"purge"|"archive"}` (archive → `rename` folder ke `DATA_DIR/deleted-users/<username>-<ts>`; purge → task `maintenance.purge_user` menghapus folder + trash + versions + thumbs + baris terkait; share dicabut; sesi dicabut), aturan: admin terakhir tidak bisa dihapus/dinonaktifkan/diturunkan (`409 VALIDATION_FAILED` dengan pesan), admin tidak bisa menghapus dirinya sendiri; jika folder home sudah ada saat user dibuat ulang → peringatan di respons.
3. Settings: `GET /api/v1/admin/settings` (nilai efektif + sumber `env|db` + default), `PUT /api/v1/admin/settings {key: value}` dengan validasi per kunci (tipe, rentang, daftar) untuk kunci SPEC §3 kolom override: batas upload, chunk size, ekstensi diblokir, kuota default, retensi trash, limit versi, remote download (enabled/max/allowlist/denylist), aturan share (allow_public, allow_no_expiry, require_password), kebijakan password (min length), session timeout, nama & logo aplikasi (`POST /admin/branding/logo` PNG ≤ 512 KB → `DATA_DIR/branding/logo.png`, disajikan `/api/v1/branding/logo`), bahasa default; perubahan berlaku live via `config.Snapshot()` yang dibaca handler (atomic pointer), audit `settings.changed {key, old, new}` (tanpa nilai rahasia).
4. `ADMIN_CAN_BROWSE_USERS`: jika `true`, endpoint files menerima `?as_user=<username>` untuk admin → jail user itu, **setiap** request dicatat audit `admin.browse_user` (path); UI: pilih user di halaman Users → "Buka folder" (hanya jika flag aktif; jika tidak, tombol tidak tampil).
5. Frontend `/admin` (hanya role admin; route guard + server 403): layout sub-nav (Dashboard, Users, Settings, Audit log, Tasks, Maintenance — dua terakhir di 19b), Dashboard kartu padat + tabel top user + status dependency (ikon ✓/✗ + saran instalasi) + peringatan; Users tabel (filter, bar kuota, aksi di menu baris: edit, reset password (dialog tampil sekali + salin), paksa logout, nonaktif/aktif, hapus (dialog ketik username + pilihan purge/arsip), buka folder jika diizinkan), dialog Buat user (password sementara tampil sekali); Settings form berkelompok (react-hook-form + zod dari skema kunci) dengan indikator "dari env"/"ditimpa", tombol reset ke default.
6. Sidebar menu "Admin" hanya untuk admin; semua endpoint `/api/v1/admin/*` memakai `RequireAdmin`.
7. E2E `admin-users.spec.ts`: admin buat user budi (password sementara) → logout → budi login → dipaksa ganti password → folder budi ada → admin set kuota 1 MB → budi upload 2 MB ditolak → admin nonaktifkan budi → sesi budi mati → aktifkan → admin coba turunkan dirinya (admin terakhir) → ditolak → ubah setting `share.require_password` → dialog share budi mewajibkan password.

**File yang dibuat/diubah:** `backend/internal/admin/{dashboard,users,settings,branding}.go` + test, `backend/internal/config/snapshot.go`, `backend/internal/httpapi/admin_*.go`, `backend/internal/httpapi/scope.go` (`as_user`), `web/src/features/admin/{AdminLayout,Dashboard,UsersPage,UserDialogs,SettingsPage,settingsSchema.ts}.tsx`, `web/e2e/admin-users.spec.ts`, `docs/openapi.yaml`.

**Acceptance criteria:**
- [ ] User non-admin `GET /api/v1/admin/users` → `403 FORBIDDEN` (bukan hanya menu tersembunyi).
- [ ] Buat user dengan username `www` / `.x` / `Admin2` (huruf besar) → `400 VALIDATION_FAILED`; `budi_01` → `201` + `temp_password` hanya di respons ini.
- [ ] Admin terakhir: `PATCH role=user` / `disable` / `DELETE` → `409`.
- [ ] Hapus user mode arsip → folder berpindah ke `DATA_DIR/deleted-users/budi-<ts>`, share budi `403`, sesi dicabut; mode purge → task selesai, tidak ada sisa di storage/trash/versions/thumbs.
- [ ] `PUT settings {upload.max_size:"50GB"}` berlaku tanpa restart (precheck upload menerima batas baru); nilai tidak valid → `400` dengan `details.key`.
- [ ] `ADMIN_CAN_BROWSE_USERS=false`: `?as_user=` → `403`; `true` → berhasil + baris audit per request.
- [ ] Dashboard menampilkan kapasitas disk nyata (`df` cocok ± 1 %) dan status dependency sesuai `check-deps`.

**Test wajib:** `admin/users_test.go` (validasi, admin terakhir, delete modes, side effects), `admin/settings_test.go` (validasi per kunci, snapshot live), `httpapi/admin_test.go` (RequireAdmin, as_user audit); Playwright `admin-users.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Audit log UI, tasks semua user, maintenance, profil (19b).

**Commit:** `feat(admin): dashboard, user management, live settings overrides, branding`

---

## Step 19b — Admin: audit log + export CSV, tasks semua user, maintenance (rekalkulasi, cache, purge, integritas, backup SQLite); profil & sesi user

> Sertakan Preamble di atas.

**Tujuan:** Melengkapi admin (audit, tasks global, maintenance terjadwal & manual, backup) dan halaman profil user (password, sesi aktif, preferensi).

**Prasyarat:** Step 19a.

**Baca dulu:** SPEC §8.17 (audit log, tasks, maintenance), §8.18 (profil & preferensi), §8.16 (retensi aktivitas), §6.3 grup `admin/audit`, `admin/tasks`, `admin/maintenance`, `admin/shares`, `me/sessions`, §4.2 `audit_logs`, §12.6 (backup); PRD epic E12, E13.

**Tugas:**
1. Audit: `GET /api/v1/admin/audit?user=&action=&from=&to=&cursor=` (index sesuai §4.2), `GET /api/v1/admin/audit/export.csv?…` streaming CSV (RFC 4180, BOM UTF-8 untuk Excel, maks 1 juta baris, `Content-Disposition`), retensi `AUDIT_RETENTION_DAYS` (job harian) — pastikan semua aksi SPEC §8.17 tercatat: login sukses/gagal, perubahan user & setting, share dibuat/dicabut, akses link publik (ringkas per sesi share, bukan per byte), hapus permanen, akses admin ke folder user.
2. Tasks global: `GET /api/v1/admin/tasks?state=&user=&cursor=`, `POST /api/v1/admin/tasks/{id}/cancel`; panel task admin menampilkan kolom user.
3. Shares global: `GET /api/v1/admin/shares?mode=public&status=` (tanpa isi file: hanya nama item, pemilik, status, kedaluwarsa, hitungan), `POST /api/v1/admin/shares/{id}/disable` (audit).
4. Maintenance (`internal/admin/maintenance.go`, tiap aksi = task `maintenance.<nama>` + jadwal): `recalc_quota` (semua user; 03:00), `clear_thumb_cache` (semua / per user; juga pangkas cache > 2 GB mingguan LRU), `cleanup_uploads` (terbengkalai > 24 h; tiap jam — sudah ada, kini terlihat), `purge_trash` (kedaluwarsa; harian), `integrity_check` (folder di `STORAGE_ROOT` tanpa akun, akun tanpa folder (belum login), share/favorit/versi dengan path rusak → laporan JSON di task result), `backup_sqlite` (`VACUUM INTO DATA_DIR/backups/app-<YYYYMMDD-HHMMSS>.db`, simpan 7 terakhir; harian 02:30; juga sebelum `update.sh`), `cleanup_logs` (audit/activity/share_access retensi). Endpoint `POST /api/v1/admin/maintenance/{nama}`, `GET /api/v1/admin/maintenance` (jadwal, terakhir jalan, hasil).
5. Scheduler (`internal/scheduler` dari 10a) dilengkapi: definisi jadwal terpusat, tidak menjalankan ganda, jitter, log, status untuk UI; zona waktu jadwal = `TZ_DEFAULT`.
6. Profil `/settings` (semua user): ganti password (reuse 3), sesi aktif `GET /api/v1/me/sessions` (perangkat dari UA ringkas, IP, dibuat, terakhir aktif, "sesi ini") + `DELETE /me/sessions/{id}` + "Keluar dari semua sesi lain"; preferensi: tema (light/dark/system), warna aksen, bahasa, view default, density (compact/normal), folder di atas, konfirmasi delete, zona waktu tampilan (default browser) — disimpan `me/preferences`; placeholder 2FA (23a).
7. Frontend admin: halaman Audit (tabel padat, filter, detail JSON di drawer, export CSV), Tasks (reuse panel task dengan kolom user + cancel), Shares publik (tabel + nonaktifkan), Maintenance (kartu per aksi: deskripsi, jadwal, terakhir jalan, tombol jalankan, hasil terakhir; integrity report tabel).
8. E2E `admin-ops.spec.ts`: audit menampilkan login & perubahan setting; export CSV diunduh & kolom benar; maintenance `backup_sqlite` menghasilkan file; `integrity_check` mendeteksi folder tanpa akun (buat via fs di test); profil: dua sesi → logout sesi lain → sesi itu 401.

**File yang dibuat/diubah:** `backend/internal/admin/{audit,maintenance,shares}.go`, `backend/internal/scheduler/*`, `backend/internal/httpapi/{admin_audit,admin_tasks,admin_shares,admin_maintenance,me_sessions}.go`, `web/src/features/admin/{AuditPage,TasksPage,SharesPage,MaintenancePage}.tsx`, `web/src/features/settings/{ProfilePage,SessionsList,PreferencesForm}.tsx`, `web/e2e/admin-ops.spec.ts`, `docs/openapi.yaml`, `docs/SECURITY.md` (audit coverage).

**Acceptance criteria:**
- [ ] Setiap aksi di daftar SPEC §8.17 menghasilkan baris audit (test tabel memicu tiap aksi dan memeriksa `action`).
- [ ] Export 100.000 baris CSV streaming tanpa RSS melonjak; dibuka Excel/LibreOffice dengan kolom benar.
- [ ] `backup_sqlite` → file valid (`sqlite3 … "PRAGMA integrity_check"` = ok), hanya 7 terbaru tersimpan.
- [ ] `recalc_quota` menyamakan `quota_usage` dengan walk nyata; `integrity_check` melaporkan folder yatim & referensi rusak.
- [ ] Jadwal tidak berjalan ganda saat dua instance test dijalankan dalam satu proses; status jadwal tampil di UI.
- [ ] Logout sesi lain → sesi tersebut `401` pada request berikutnya; sesi saat ini tetap.

**Test wajib:** `admin/audit_test.go` (filter, cursor, export CSV format, retensi), `admin/maintenance_test.go` (tiap aksi), `scheduler/*_test.go`, `httpapi/me_sessions_test.go`; Playwright `admin-ops.spec.ts`.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** 2FA (23a), metrik Prometheus (20 jika sempat, opsional).

**Commit:** `feat(admin): audit log with CSV export, global tasks and shares, maintenance jobs, sqlite backup; user profile and sessions`

---

## Step R-E — Review independen Step 18a–19b

> Sertakan Preamble di atas (mode reviewer).

**Tujuan:** Reviewer memeriksa sharing dan admin — area dengan risiko otorisasi tertinggi (IDOR share, escape subtree, eskalasi admin, kebocoran data pemilik ke penerima/tamu).

**Prasyarat:** Step 18a–19b tercentang.

**Baca dulu:** PRD epic E10, E12, E13; SPEC §8.15–8.18, §10; `docs/SECURITY.md`; PROGRESS catatan Step 18a–19b; review-D minor tertunda.

**Tugas:**
1. Jalankan seluruh suite.
2. Checklist R-E: (a) IDOR: share, recipients, access-logs, admin users/settings/tasks/shares, me/sessions — dengan id milik user lain & id acak; (b) escape subtree share: `..`, symlink di dalam folder yang dibagikan menunjuk ke luar item (harus `broken`/ditolak), file tunggal share → tidak bisa list folder induk, `?share=` + `path` absolut, share kedaluwarsa/nonaktif/pemilik nonaktif/penerima nonaktif; (c) permission editor: tidak bisa chmod/symlink keluar/compress ke luar/extract ke luar subtree; delete masuk trash pemilik; (d) publik: cookie share scope path, token di URL tidak masuk `Referer` (no-referrer), `noindex`, rate limit password, `max_downloads` tidak bisa dilewati via Range/zip, upload file request tidak bisa overwrite/escape; (e) admin: `RequireAdmin` di setiap route (bandingkan daftar route chi), admin terakhir, `as_user` audit, settings validasi (nilai ekstrem, injeksi di ekstensi diblokir), password sementara tidak di log/audit, logo upload hanya PNG (magic bytes) & tidak bisa SVG; (f) audit lengkap & tidak memuat rahasia; export CSV injeksi formula (`=`, `+`, `-`, `@` di awal sel di-escape); (g) sesi: logout-all mencabut termasuk cookie share? (tidak perlu) — pastikan reset password admin mencabut sesi target; (h) UI: halaman publik tidak memuat chunk aplikasi yang tidak perlu (ukuran), tidak membocorkan username pemilik jika disembunyikan (cek seluruh respons API).
3. Tulis `docs/reviews/review-E.md`; perbaikan `fix(review-E): …` + regresi.

**File yang dibuat/diubah:** `docs/reviews/review-E.md`, perbaikan + test.

**Acceptance criteria:**
- [ ] `docs/reviews/review-E.md` ada; tidak ada blocker/major terbuka.
- [ ] Test matriks akses (18b) diperluas dengan kasus reviewer dan lulus.
- [ ] CSV export aman dari injeksi formula (test).

**Test wajib:** Regresi per temuan.

**Verifikasi:** `make test && make lint && pnpm test && pnpm e2e`.

**Di luar scope step ini:** Fitur baru.

**Commit:** `docs(review): independent review E` lalu `fix(review-E): …`

---

# Milestone M5 — Hardening, performa, deployment

## Step 20 — Hardening: CSP final, review OWASP, test IDOR, rate limit semua endpoint, govulncheck + pnpm audit, SECURITY.md

> Sertakan Preamble di atas.

**Tujuan:** Menutup checklist keamanan SPEC §10 secara menyeluruh: header & CSP final, rate limit dengan angka konkret di semua endpoint publik dan sensitif, suite IDOR lintas resource, pemindaian dependensi di CI, dan dokumentasi keamanan lengkap.

**Prasyarat:** Step R-E selesai.

**Baca dulu:** SPEC §10 (seluruhnya: STRIDE, kontrol, header & CSP final, password & sesi, rate limit, daftar test keamanan), §6.2, §11 (fixture keamanan); `docs/SECURITY.md`; PRD §8 (NFR) dan epic E1, E10, E12.

**Tugas:**
1. CSP final (SPEC §10.3) di middleware: `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'` (dicatat alasannya: Monaco/Radix inline style; jika bisa dihilangkan dengan nonce → lakukan dan perbarui SPEC); `worker-src 'self' blob:`; `img-src 'self' data: blob:`; `media-src 'self' blob:`; `font-src 'self'`; `connect-src 'self'`; `object-src 'none'`; `frame-ancestors 'none'`; `base-uri 'self'`; `form-action 'self'`; `frame-src 'none'` (kecuali halaman cetak PDF jika dipakai → `'self'`); report-only dulu 1 hari di staging lalu enforce; halaman publik `/s/*` dengan CSP yang sama + `Referrer-Policy: no-referrer`; `Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=(), usb=()`; HSTS `max-age=31536000; includeSubDomains` (preload tidak); `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin` pada API; pastikan tidak ada `Access-Control-Allow-Origin` (CORS mati).
2. Rate limit terpusat (`internal/httpapi/ratelimit.go`, token bucket in-memory per kunci, header `RateLimit-*`/`Retry-After`) dengan angka SPEC §10.5: login (IP 20/menit; username lockout), `auth/csrf` 60/menit, password share 10/10 menit per IP+link, publik `public/shares/*` 60/menit/IP, `users/suggest` 30/menit, content search 10/menit, remote download 10/jam, `files/sign` 120/menit, thumb 20 paralel, download 3 paralel, API umum 600/menit/user (perlindungan kasar), SSE 5 koneksi/user. Semua angka bisa dioverride env `RATE_LIMIT_*` (dokumentasikan di SPEC §3 jika ditambah).
3. Suite IDOR terpusat `backend/internal/httpapi/idor_test.go`: dua user + admin + anonim; iterasi otomatis atas daftar resource (files path, trash id, version id, task id, share id, recipient, notification id, session id, upload tus id, preferences, folder-prefs, media-position, editor-session, admin/*) dengan id milik user lain → harus `404`/`403`, tidak pernah `200`; daftar route chi dibandingkan dengan daftar yang dicakup test (route baru tanpa cakupan → test gagal).
4. Review OWASP ASVS (level 2, subset relevan) sebagai checklist di `docs/SECURITY.md`: tiap item → status (ada/tidak berlaku) + rujukan test/kode; threat model STRIDE SPEC §10.1 disalin & disinkronkan; daftar kontrol keamanan arsip/SSRF/path/XSS/CSRF/sesi/password/share/admin; instruksi pelaporan kerentanan; hasil spike 7zz; rekomendasi `hidepid=2` dan dampaknya.
5. Fuzz test dikomit dengan corpus: `FuzzNormalize` (fsjail), `FuzzEntryName` (archive), `FuzzParseRange`?, `FuzzCursor` (listing), `FuzzBatchRenameRule`; CI menjalankan `-fuzztime=30s` tiap paket (job terpisah, non-blocking jika terlalu lama → catat).
6. Rahasia & log: test memastikan output log (semua level) tidak memuat `APP_SECRET`, password (login/share/arsip), token share mentah, isi `.env`, cookie; error 500 ke klien tanpa pesan internal; `X-Request-ID` ada di setiap respons.
7. Dependensi: CI gagal jika `govulncheck` menemukan kerentanan yang dicapai kode, `pnpm audit --audit-level=high` gagal; `go.mod`/lockfile dipin; `make audit` lokal; dokumentasikan proses update bulanan.
8. Upload: pastikan file yang diunggah tidak pernah dieksekusi: storage di luar web root, Nginx tidak menyajikan `/www/storage` (dicek di Step 22), binary tidak `exec` file user; ekstensi diblokir default `[]` tetapi SPEC menyarankan daftar untuk admin; `X-Content-Type-Options` pada semua respons.
9. `/metrics` (opsional, jika `METRICS_ADDR` diset): listener terpisah hanya loopback, metrik dasar (request total/durasi, task aktif, thumb queue, SSE koneksi) — tanpa label berisi path user.
10. Perbaiki semua temuan; perbarui `docs/openapi.yaml` dengan header rate limit & kode `429`.
11. E2E `security.spec.ts`: CSP tidak dilanggar di semua halaman utama (console error 0), `Set-Cookie` flag benar, `/s/*` tidak mengirim Referer, login rate limit tampil ramah di UI.

**File yang dibuat/diubah:** `backend/internal/httpapi/{security_headers,ratelimit,idor_test,secrets_test}.go`, `backend/internal/**/fuzz_test.go` + `testdata/fuzz/*`, `backend/internal/metrics/*` (opsional), `docs/SECURITY.md`, `docs/openapi.yaml`, `.github/workflows/ci.yml` (audit/fuzz jobs), `Makefile` (`audit`), `web/e2e/security.spec.ts`, SPEC §10 (sinkron).

**Acceptance criteria:**
- [ ] `curl -I https://…/` (atau dev) memuat seluruh header SPEC §10.3; tidak ada `Access-Control-Allow-Origin`; CSP tanpa `unsafe-inline` untuk script dan tanpa `unsafe-eval`.
- [ ] Editor, PDF, video, upload, SSE berfungsi dengan CSP enforce (console tanpa pelanggaran CSP).
- [ ] `go test ./internal/httpapi/ -run IDOR` lulus dan mencakup 100 % route non-publik (daftar dibandingkan otomatis).
- [ ] Setiap rate limit di SPEC §10.5 punya test yang memicu `429` dengan `Retry-After`.
- [ ] `govulncheck ./...` dan `pnpm audit --audit-level=high` bersih; CI gagal saat kerentanan disuntik (uji sekali dengan dependensi lama lalu kembalikan).
- [ ] Fuzz 30 s per target tanpa crash; corpus dikomit.
- [ ] `docs/SECURITY.md` lengkap (ASVS checklist, STRIDE, kontrol, pelaporan, hasil spike 7zz, rekomendasi `hidepid=2`).

**Test wajib:** `idor_test.go`, `ratelimit_test.go`, `security_headers_test.go`, `secrets_test.go`, fuzz targets, Playwright `security.spec.ts`.

**Verifikasi:** `make audit && make test && make lint && go test -race ./... && pnpm e2e`.

**Di luar scope step ini:** Performa (21), deploy (22).

**Commit:** `feat(security): final CSP and headers, centralized rate limits, IDOR suite, fuzzing, dependency audits`

---

## Step 21 — Performa & aksesibilitas: folder 10.000 file, bundle budget, audit axe, responsif mobile

> Sertakan Preamble di atas.

**Tujuan:** Membuktikan target non-fungsional SPEC §1.6 / PRD §8 pada spesifikasi server nyata (1 vCPU, 1,9 GB RAM) dan memenuhi WCAG 2.2 AA serta responsif di tiga breakpoint.

**Prasyarat:** Step 20.

**Baca dulu:** SPEC §1.6 (batas resource & target), §8.3 (cache listing), §8.4 (worker thumbnail), §9.9 (responsif & a11y), §11 (performance test); PRD §2 metrik sukses, §8 NFR; epic E3, E13.

**Tugas:**
1. Fixture performa: `backend/internal/perf/gen.go` (10.000 file campuran + 500 folder + 200 gambar kecil) dan skrip `make perf` yang menjalankan: `k6` (jika ada) atau benchmark Go `httptest` untuk `files/list` (p50/p95/p99 pada 10.000 entri, cache dingin & hangat), navigasi berulang, 20 thumbnail paralel, upload 1 GB (pprof heap: RSS tidak tumbuh > +100 MB), zip streaming 1 GB, extract 1 GB — semua dicatat ke `docs/PERF.md` dengan angka di mesin dev **dan** di server (dengan izin pemilik, di luar jam sibuk).
2. Optimasi berdasarkan hasil: `Lstat` paralel terbatas (4 goroutine) di listing jika membantu di 1 vCPU, cache listing & `statx` batch, `ReadDir` tanpa alokasi berlebih, JSON encoding streaming untuk halaman besar, kompresi gzip/br respons API > 1 KB (Nginx atau middleware), `Cache-Control` untuk aset immutable, HTTP keep-alive, prefetch halaman berikutnya di UI hanya saat scroll mendekat.
3. Frontend: render pertama folder 10.000 < 1 s (Playwright trace `performance.measure`), navigasi hangat < 150 ms, long task < 50 ms saat scroll (virtualizer overscan 10, memo baris, ikon SVG sprite), `React.lazy` untuk route jarang (admin, storage, settings), `requestIdleCallback` untuk enrich kolom metadata.
4. Bundle budget diberlakukan di CI: JS awal < 300 KB gzip (gagal keras), chunk Monaco/pdf.js/video/QR/Prettier/highlighter lazy, `pnpm build --report` disimpan sebagai artefak; hapus dependensi yang tidak dipakai; tree-shake lucide (import per ikon).
5. Aksesibilitas: axe (Playwright `@axe-core/playwright`) di `/login`, `/files` (Details & Large icons), `/trash`, `/search`, `/editor`, viewer overlay, `/shares`, `/settings`, `/admin/users`, `/s/<token>` → 0 serious/critical; perbaiki: `aria-label` semua tombol ikon, `aria-sort` header, `aria-selected`, `aria-rowcount`, live region operasi, fokus trap dialog/overlay, fokus kembali, skip link, kontras (script 6), `prefers-reduced-motion`, keyboard-only E2E (login → navigasi → rename → delete → undo → upload via dialog) tanpa mouse; label form & error terkait `aria-describedby`.
6. Responsif: Playwright viewport 375×812 (mobile), 820×1180 (tablet), 1440×900 (desktop): < 768 sidebar drawer, toolbar diringkas ke "⋯", Details hanya Nama + Ukuran/Diubah, long-press (500 ms) = context menu, aksi seleksi di bottom bar, viewer & editor dapat dipakai (editor: peringatan keterbatasan mobile); 768–1279 sidebar ikon + panel preview overlay; tidak ada scroll horizontal; target sentuh ≥ 44 px untuk mobile.
7. Dark mode audit: tidak ada warna hardcode (lint rule/grep `#[0-9a-f]{6}` di `web/src` kecuali `tokens.css`), kontras di dark lulus, thumbnail/ikon terlihat di kedua tema.
8. Thumbnail di 1 vCPU: antrian memprioritaskan item terlihat; ukur throughput; batasi `nice`; pastikan UI tidak menunggu thumbnail.
9. Dokumentasi `docs/PERF.md` (metodologi, angka, perbandingan target) dan `docs/A11Y.md` (checklist WCAG 2.2 AA yang dipenuhi, pengecualian).

**File yang dibuat/diubah:** `backend/internal/perf/*`, `backend/internal/listing/*` (optimasi), `backend/internal/httpapi/compress.go`, `Makefile` (`perf`), `web/src/**` (optimasi, a11y, responsif), `web/scripts/bundle-size.mjs` (enforce), `web/e2e/{a11y,responsive,keyboard-only,perf}.spec.ts`, `docs/PERF.md`, `docs/A11Y.md`, `.github/workflows/ci.yml`.

**Acceptance criteria:**
- [ ] `make perf` di server: `files/list` 10.000 entri p95 < 500 ms (cache dingin) dan < 100 ms (hangat); angka tercatat di `docs/PERF.md`.
- [ ] Playwright perf: render pertama folder 10.000 < 1 s; navigasi hangat < 150 ms; scroll tanpa long task > 100 ms.
- [ ] Upload 1 GB + zip 1 GB: RSS backend ≤ baseline + 100 MB; idle RSS < 100 MB.
- [ ] CI gagal jika JS awal ≥ 300 KB gzip; laporan chunk tersimpan.
- [ ] Axe 0 serious/critical di semua halaman daftar; keyboard-only E2E lulus.
- [ ] Tiga viewport lulus tanpa scroll horizontal; long-press membuka context menu di mobile.
- [ ] Grep warna hardcode di `web/src` (di luar `tokens.css`, `monarch`, chart treemap palet) = 0.

**Test wajib:** Go benchmark/perf harness, Playwright `a11y.spec.ts`, `responsive.spec.ts`, `keyboard-only.spec.ts`, `perf.spec.ts`.

**Verifikasi:** `make perf && pnpm build && pnpm e2e && make test`.

**Di luar scope step ini:** Transcoding, indeks pencarian persisten, PWA.

**Commit:** `perf(a11y): meet listing/bundle/memory targets, WCAG 2.2 AA fixes, responsive layouts`

---

## Step 22 — Deployment: install.sh, update.sh, systemd, `nginx-aapanel.conf`, `docs/DEPLOY-AAPANEL.md`, smoke test di Ubuntu + aaPanel

> Sertakan Preamble di atas.

**Tujuan:** Aplikasi bisa dipasang dari server kosong ke `https://file.kppbwi.app` di belakang Nginx aaPanel, berjalan sebagai user `filemanager` di systemd, dengan update & rollback otomatis dan dokumentasi klik-per-klik.

**Prasyarat:** Step 21. **Peringatan:** step ini mengubah state server (user Linux, paket apt, systemd, site aaPanel, SSL, chown storage). AI wajib menampilkan rencana dan **meminta konfirmasi pemilik sebelum setiap langkah yang mengubah aaPanel, systemd, firewall, atau kepemilikan folder**; jangan menjalankan `install.sh` di server tanpa persetujuan eksplisit di sesi itu.

**Baca dulu:** SPEC §12 (seluruhnya: install.sh, systemd, aaPanel klik-per-klik, nginx-aapanel.conf, update/rollback, backup/restore, troubleshooting), §3, §1.6, §13 ADR-20, ADR-22; PRD epic E14; `promp-master.md` §3 "Lokasi aplikasi, domain & aaPanel".

**Tugas:**
1. `deploy/install.sh` (bash `set -euo pipefail`, idempoten, bisa diulang): cek root; deteksi Ubuntu 22.04/24.04 (lain → berhenti); cek RAM & swap → jika RAM < 3 GB dan swap < 2 GB: tawarkan membuat swapfile 2 GB (`/swapfile`, `fstab`) — default tanya; apt install `ffmpeg libvips-tools poppler-utils ripgrep 7zip` (binary `7zz`; verifikasi nama paket per versi Ubuntu; 24.04: `7zip` 23.01) dan opsional `unrar` (multiverse, non-free → tanya `--with-unrar`); Go: jika `go version` < 1.25.12 (CVE `os.Root`) → unduh tarball resmi go.dev (verifikasi SHA-256 dari `https://go.dev/dl/?mode=json`) ke `/usr/local/go` (opsi `--skip-build` jika binary dibawa dari CI); Node ≥ 22 + `corepack enable` (hanya jika build di server); buat user sistem `filemanager` (`--system --home /www/wwwroot/file.kppbwi.app --shell /usr/sbin/nologin`); `/www/storage/FileManager` → `chown filemanager:filemanager`, `chmod 0750` (peringatan jika sebelumnya `www:www` seperti di server ini); `.system` subfolder dibuat aplikasi; `APP_DIR` owner `filemanager`, `public/` dibuat (kosong + `.well-known/` 0755); `.env`: jika belum ada → salin dari `.env.example`, isi `APP_SECRET=$(openssl rand -hex 32)`, `ADMIN_INITIAL_PASSWORD` acak 24 karakter (tampilkan sekali di terminal, tidak di log), `chmod 600`, owner `filemanager`; build (`make build`) atau pasang binary dari argumen; pasang `deploy/filemanager.service` → `/etc/systemd/system/`, `daemon-reload`, `enable --now`; tunggu `/readyz` 200 (maks 30 s); jalankan `bin/filemanager check-deps`; **tidak** menyentuh UFW/firewall aaPanel (port 8080 hanya loopback); cetak checklist langkah aaPanel (tugas 4) di akhir.
2. `deploy/filemanager.service`: `[Unit] After=network-online.target`, `[Service] Type=simple`, `User=filemanager`, `Group=filemanager`, `WorkingDirectory=/www/wwwroot/file.kppbwi.app`, `EnvironmentFile=/www/wwwroot/file.kppbwi.app/.env`, `ExecStart=/www/wwwroot/file.kppbwi.app/bin/filemanager serve`, `Restart=on-failure`, `RestartSec=3`, `KillSignal=SIGTERM`, `TimeoutStopSec=30`, `LimitNOFILE=65536`, `UMask=0027`, hardening: `NoNewPrivileges=true`, `ProtectSystem=strict`, `ReadWritePaths=/www/storage/FileManager`, `ProtectHome=true`, `PrivateTmp=true`, `PrivateDevices=true`, `ProtectKernelTunables=true`, `ProtectKernelModules=true`, `ProtectControlGroups=true`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`, `RestrictNamespaces=true`, `LockPersonality=true`, `RestrictRealtime=true`, `SystemCallFilter=@system-service`, `SystemCallArchitectures=native`, `CapabilityBoundingSet=` (kosong), `AmbientCapabilities=` (kosong); verifikasi `ffmpeg`/`7zz` masih bisa dieksekusi di bawah filter (`systemd-analyze security filemanager` dicatat; `MemoryDenyWriteExecute` tidak dipakai jika merusak ffmpeg — perlu verifikasi).
3. `deploy/nginx-aapanel.conf`: snippet untuk ditempel di UI aaPanel (editor config site) di dalam blok `server {}` **sebelum** `location ^~ /` proxy: `client_max_body_size 64m;` (≥ chunk 16 MB; naikkan jika admin mengubah chunk), `proxy_request_buffering off;`, `proxy_buffering off;`, `proxy_read_timeout 3600s; proxy_send_timeout 3600s;`, `proxy_http_version 1.1;`, header `Host $host`, `X-Real-IP $remote_addr`, `X-Forwarded-For $proxy_add_x_forwarded_for`, `X-Forwarded-Proto $scheme`, `X-Request-ID $request_id`, `location ~ /\.(?!well-known) { deny all; }`, `location ~* ^/(bin|backend|web|deploy|docs|\.env|Makefile) { deny all; }` (defense-in-depth bila root pernah disajikan), `location ^~ /.well-known/acme-challenge/ { root /www/wwwroot/file.kppbwi.app/public; }` jika aaPanel tidak sudah menanganinya (di server ini aaPanel punya include Lua `well-known/<domain>.conf` — jangan duplikasi, verifikasi urutan location); `gzip` untuk `application/json` (jika aaPanel belum); catatan tiap baris. Dua varian: (A) tipe proyek **Reverse Proxy** aaPanel 8.0.7 (terverifikasi ada di server), (B) site PHP "Static" + fitur Reverse Proxy (teks prompt) — masing-masing dengan letak penempelan "perlu verifikasi" nama menu persis.
4. `docs/DEPLOY-AAPANEL.md` klik-per-klik: prasyarat (DNS A record `file.kppbwi.app` → IP server; port 80/443 terbuka — sudah); jalankan `install.sh`; aaPanel: Website → (varian A) Reverse Proxy → Add: domain `file.kppbwi.app`, target `http://127.0.0.1:8080`, send `$http_host`, site directory `/www/wwwroot/file.kppbwi.app` (jika bisa diatur; jika tidak, catat) — atau (varian B) Add site PHP Static tanpa DB/FTP, running directory `/public`, lalu Reverse Proxy → target; hapus file default aaPanel (`index.html`, `404.html`, `.htaccess`; `.user.ini` → `chattr -i` dulu) dari site directory; SSL → Let's Encrypt (verifikasi file/HTTP) → Force HTTPS; tempel snippet `nginx-aapanel.conf` di Config File site → simpan → aaPanel reload Nginx; verifikasi: `curl -I https://file.kppbwi.app/healthz` 200, `curl -I https://file.kppbwi.app/.env` 404/403, `curl https://file.kppbwi.app/.well-known/acme-challenge/test` disajikan dari `public/`, login admin, ganti password, upload 100 MB, SSE (task) berjalan tanpa putus 2 menit, download Range; catatan: jangan edit vhost manual (aaPanel menimpa), jangan pakai user `www`, port 8080 tidak dibuka di firewall, `ADMIN_INITIAL_PASSWORD` dihapus dari `.env` setelah ganti password; backup: `/www/storage/FileManager` (boleh cron/backup aaPanel) + `.system/backups/*.db`; restore; troubleshooting (502 → service mati/`readyz`; upload gagal 413 → `client_max_body_size`; SSE putus → `proxy_buffering`; SSL gagal → `.well-known` tidak disajikan; `Permission denied` → owner storage; OOM saat build → swap/CI).
5. `deploy/update.sh`: `git fetch` + checkout tag/branch (atau ambil binary rilis), `backup_sqlite` via `bin/filemanager backup` (subcommand baru memanggil `db.Backup`), build (atau pasang binary), `migrate up`, restart, tunggu `/readyz` ≤ 30 s; gagal → rollback: binary sebelumnya (`bin/filemanager.prev`), `migrate down` ke versi sebelumnya (jika migrasi baru reversible; jika tidak, restore DB dari backup) dan restart; log ke `DATA_DIR/logs/update-<ts>.log`.
6. `deploy/Dockerfile` multi-stage (node → go → distroless/debian-slim dengan ffmpeg, libvips-tools, poppler-utils, ripgrep, 7zip; user non-root; volume `/data`, `/storage`) + `deploy/docker-compose.yml` opsional — dibangun di CI tetapi bukan jalur deploy utama.
7. Opsional otomasi aaPanel: `deploy/aapanel-site.py` meniru pola `deploy/aapanel/site.py` proyek sibling (API internal `mod/project/proxy/comMod.py`) untuk membuat site Reverse Proxy, SSL, dan Force HTTPS — tandai "perlu verifikasi pada versi aaPanel lain"; langkah utama tetap UI.
8. Smoke test nyata di server (setelah konfirmasi pemilik): jalankan `install.sh` → langkah aaPanel → verifikasi semua butir tugas 4 → catat hasil (output `check-deps`, `systemd-analyze security`, `curl` header) di `docs/DEPLOY-AAPANEL.md` bagian "Hasil smoke test 14 Okt 2026+" dan PROGRESS.
9. `README.md` bagian deploy merujuk dokumen ini; `Makefile` target `install` (memanggil `deploy/install.sh`), `update`.

**File yang dibuat/diubah:** `deploy/install.sh`, `deploy/update.sh`, `deploy/filemanager.service`, `deploy/nginx-aapanel.conf`, `deploy/Dockerfile`, `deploy/docker-compose.yml`, `deploy/aapanel-site.py` (opsional), `deploy/README.md`, `docs/DEPLOY-AAPANEL.md`, `backend/cmd/filemanager/main.go` (`backup` subcommand), `Makefile`, `README.md`, `.github/workflows/ci.yml` (job docker build + shellcheck).

**Acceptance criteria:**
- [ ] `shellcheck deploy/*.sh` bersih; `install.sh` dijalankan dua kali berturut-turut tanpa error (idempoten) di VM/container Ubuntu 24.04 bersih (CI job atau lokal).
- [ ] Di server: `systemctl status filemanager` active, proses berjalan sebagai `filemanager`, `ss -ltnp` hanya `127.0.0.1:8080`, `ufw status` tidak menyebut 8080.
- [ ] `curl -I https://file.kppbwi.app/healthz` → 200 dengan HSTS; `https://file.kppbwi.app/.env`, `/bin/filemanager`, `/backend/go.mod` → 403/404; `/.well-known/acme-challenge/<file>` disajikan dari `public/`.
- [ ] Upload 100 MB lewat domain sukses (tidak ada 413); SSE task berjalan ≥ 2 menit tanpa putus; download Range 206; SSL Let's Encrypt terpasang & Force HTTPS aktif; perpanjangan dry-run (jika aaPanel menyediakan) sukses atau dicatat "perlu verifikasi".
- [ ] `update.sh` dengan binary yang sengaja gagal `/readyz` → rollback otomatis ke versi sebelumnya dalam ≤ 60 s.
- [ ] `/www/storage/FileManager` owner `filemanager:filemanager` 0750; `.env` 600; `systemd-analyze security filemanager` dicatat (target ≤ 4.0 "OK"/"MEDIUM", perlu verifikasi).
- [ ] `docs/DEPLOY-AAPANEL.md` memuat langkah klik-per-klik, snippet, verifikasi, backup/restore, troubleshooting, dan hasil smoke test.

**Test wajib:** `shellcheck`; CI job "install-smoke" (container Ubuntu 24.04: `install.sh --skip-aapanel --no-swap`, `/healthz` 200); `update.sh` rollback test (script dengan binary palsu); Go test untuk subcommand `backup`.

**Verifikasi:** `shellcheck deploy/*.sh && make build && sudo deploy/install.sh --dry-run` lalu (dengan konfirmasi) `sudo deploy/install.sh`; `curl -I https://file.kppbwi.app/healthz`.

**Di luar scope step ini:** Origin konten terpisah (23e), monitoring eksternal.

**Commit:** `feat(deploy): install/update scripts, hardened systemd unit, aaPanel nginx snippet, deployment guide`

---

## Step R-F — Review independen Step 20–22 + verifikasi checklist rilis v1.0

> Sertakan Preamble di atas (mode reviewer).

**Tujuan:** Reviewer akhir memverifikasi hardening, performa, dan deployment, lalu menjalankan "Checklist rilis v1.0" di akhir dokumen ini secara menyeluruh sebelum tag `v1.0.0`.

**Prasyarat:** Step 20–22 tercentang; aplikasi berjalan di `https://file.kppbwi.app`.

**Baca dulu:** PRD §2 (metrik sukses), §8, §9, §10; SPEC §1.6, §10, §11, §12; `docs/SECURITY.md`, `docs/PERF.md`, `docs/A11Y.md`, `docs/DEPLOY-AAPANEL.md`; semua `docs/reviews/*`; PROGRESS.

**Tugas:**
1. Jalankan seluruh suite di CI dan lokal; jalankan `make perf` pembanding; `make audit`.
2. Checklist R-F: (a) header di produksi (`curl -I https://file.kppbwi.app`) sama dengan SPEC §10.3; (b) `nmap`/`ss` port; (c) file sensitif tidak disajikan lewat domain; (d) `systemd-analyze security`; (e) proses & file owner; (f) `.env` tanpa `ADMIN_INITIAL_PASSWORD` setelah ganti password; (g) backup harian ada & restore diuji ke DATA_DIR sementara; (h) log JSON tanpa rahasia (sampel 1 hari); (i) rate limit di produksi lewat Nginx (X-Forwarded-For dipercaya hanya dari 127.0.0.1); (j) semua temuan minor review A–E yang ditunda: tutup atau catat alasan; (k) PRD §5 matriks paritas: setiap baris "Wajib" diverifikasi manual di produksi (tabel hasil di review-F); (l) PRD §2 metrik sukses diukur.
3. Jalankan "Checklist rilis v1.0" (bagian akhir dokumen ini) dan centang satu per satu dengan bukti.
4. Tulis `docs/reviews/review-F.md` + tabel paritas terverifikasi; perbaikan `fix(review-F): …`; setelah bersih: update `CHANGELOG.md`, tag `v1.0.0` (dengan persetujuan pemilik), PROGRESS status "v1.0.0 dirilis".

**File yang dibuat/diubah:** `docs/reviews/review-F.md`, `CHANGELOG.md`, perbaikan.

**Acceptance criteria:**
- [ ] Semua butir Checklist rilis v1.0 tercentang dengan bukti (perintah/screenshot path).
- [ ] Tidak ada blocker/major terbuka di review A–F.
- [ ] Tag `v1.0.0` dibuat setelah persetujuan pemilik.

**Test wajib:** Regresi per temuan.

**Verifikasi:** `make audit && make test && make lint && pnpm e2e && make perf`.

**Di luar scope step ini:** Fitur opsional (23).

**Commit:** `docs(review): independent review F and v1.0 release checklist` lalu `chore(release): v1.0.0`

---

# Milestone M6 — Opsional fase akhir

Kerjakan sesuai prioritas di PRD §9; masing-masing sub-step adalah sesi terpisah dan mengikuti aturan umum. Tidak ada yang memblokir v1.0.

## Step 23a — TOTP 2FA

> Sertakan Preamble di atas.

**Tujuan:** Verifikasi dua langkah TOTP opsional per user, dengan kode pemulihan.
**Prasyarat:** v1.0.0.
**Baca dulu:** SPEC §8.1 (2FA), §8.18, §10.4; PRD epic E1, E13.
**Tugas:** (1) tabel kolom `totp_secret_enc` (dienkripsi AES-GCM dengan kunci turunan `APP_SECRET`), `totp_enabled_at`, tabel `recovery_codes` (hash); (2) `POST /me/2fa/setup` → secret + QR (otpauth URI), `POST /me/2fa/enable {code}`, `POST /me/2fa/disable {password, code}`, `POST /auth/login` mengembalikan `requires_2fa` + token sementara 5 menit → `POST /auth/2fa {code|recovery_code}`; rate limit 5/menit; (3) admin dapat mereset 2FA user (audit); (4) UI di profil (QR, verifikasi, kode pemulihan tampil sekali) dan login; (5) audit `2fa.enabled|disabled|used_recovery`.
**File yang dibuat/diubah:** migrasi, `internal/auth/totp.go`, httpapi, `web/src/features/settings/TwoFactor.tsx`, login flow, openapi.
**Acceptance criteria:** - [ ] Login dengan 2FA aktif memerlukan kode; kode salah ×5 → `429`; kode pemulihan sekali pakai; secret tidak tersimpan plaintext (cek DB); admin reset berfungsi; E2E `2fa.spec.ts`.
**Test wajib:** `auth/totp_test.go` (RFC 6238 vector, window ±1), httpapi, Playwright.
**Verifikasi:** `make test && pnpm e2e`.
**Di luar scope step ini:** WebAuthn.
**Commit:** `feat(auth): optional TOTP two-factor authentication with recovery codes`

## Step 23b — Dual-pane

> Sertakan Preamble di atas.

**Tujuan:** Dua panel folder berdampingan untuk drag & drop antar folder dan perbandingan.
**Prasyarat:** v1.0.0.
**Baca dulu:** SPEC §9.3 (varian dual-pane), §8.5; PRD epic E3, E4.
**Tugas:** (1) toggle dual-pane (`F6`? → verifikasi tidak bentrok; default tombol di View), panel kiri/kanan masing-masing punya path, tab, sort, seleksi aktif (satu panel fokus); (2) aksi copy/move ke panel lain (`F5`/`F6` ala commander dinilai; jika bentrok dengan refresh F5, pakai tombol "→ Salin ke panel lain"); (3) DnD antar panel; (4) layout responsif (≥ 1280 saja; di bawah itu nonaktif); (5) preferensi tersimpan.
**File yang dibuat/diubah:** `web/src/features/browser/DualPane*.tsx`, store tabs (per panel), i18n.
**Acceptance criteria:** - [ ] Drag dari panel kiri ke kanan memindah/menyalin dengan dialog konflik; shortcut tidak melanggar daftar terlarang; E2E `dual-pane.spec.ts`.
**Test wajib:** Vitest store panel; Playwright.
**Verifikasi:** `pnpm test && pnpm e2e`.
**Di luar scope step ini:** Sinkronisasi folder.
**Commit:** `feat(browser): dual-pane mode`

## Step 23c — Duplicate finder

> Sertakan Preamble di atas.

**Tujuan:** Menemukan file duplikat (ukuran → hash parsial → hash penuh) sebagai task dan membantu membersihkannya.
**Prasyarat:** v1.0.0.
**Baca dulu:** SPEC §8.7 (analisis penyimpanan), §7; PRD epic E4, E2.
**Tugas:** (1) task `find_duplicates {path}`: kelompokkan per ukuran (> 0), hash 64 KB pertama+terakhir, lalu SHA-256 penuh untuk kandidat; batas waktu 1 jam, cancel; hasil `{groups:[{size, hash, files:[…]}], reclaimable}` disimpan 24 jam; (2) UI di `/storage` tab "Duplikat": grup, pilih yang dipertahankan (default: terlama/path terpendek), hapus sisanya ke trash (batch), "ganti dengan symlink" opsional; (3) peringatan untuk file di folder share.
**File yang dibuat/diubah:** `internal/files/duplicates.go`, httpapi, `web/src/features/storage-analysis/Duplicates.tsx`.
**Acceptance criteria:** - [ ] 1.000 file dengan 100 duplikat ditemukan 100 % benar tanpa false positive (test); hapus ke trash & undo-able; E2E.
**Test wajib:** Go `duplicates_test.go`; Playwright.
**Verifikasi:** `make test && pnpm e2e`.
**Di luar scope step ini:** Deduplikasi lintas user.
**Commit:** `feat(storage): duplicate finder`

## Step 23d — Transcoding HLS on-the-fly (opsional, dibatasi CPU)

> Sertakan Preamble di atas.

**Tujuan:** Memutar video dengan codec yang tidak didukung browser (HEVC/MKV/AVI) lewat transcoding ffmpeg ke HLS, dengan batas CPU ketat (server 1 vCPU → default nonaktif).
**Prasyarat:** v1.0.0; ffmpeg terpasang.
**Baca dulu:** SPEC §8.14 (video, catatan transcoding), §7, §1.6; PRD §3 Non-goals (ditunda) dan epic E8.
**Tugas:** (1) setting admin `transcode.enabled` (default off), `max_concurrent` (default 1), `max_resolution` (720p), `nice 19`; (2) task `transcode_hls {path}` → ffmpeg argv ke `DATA_DIR/tmp/hls/<id>/` (segmen 4 s, playlist), hapus 1 jam setelah selesai/idle; (3) endpoint signed `GET /files/hls/<id>/playlist.m3u8` & segmen; (4) player: jika codec tidak didukung dan transcode aktif → tombol "Putar dengan konversi (lambat)" → progress → putar HLS (hls.js lazy atau native Safari); (5) batas: durasi ≤ 3 jam, antrian per user 1.
**File yang dibuat/diubah:** `internal/media/transcode.go`, httpapi, `web/src/features/viewer/video/Hls*.tsx`.
**Acceptance criteria:** - [ ] File HEVC diputar lewat HLS saat aktif; CPU dibatasi (`nice`), hanya 1 proses bersamaan; file sementara dibersihkan; nonaktif → tombol tidak tampil.
**Test wajib:** Go (argv, cleanup, limit) dengan ffmpeg stub; Playwright manual dicatat.
**Verifikasi:** `make test`.
**Di luar scope step ini:** Transcoding permanen / pre-encode.
**Commit:** `feat(media): optional on-the-fly HLS transcoding with CPU limits`

## Step 23e — Origin konten terpisah + preview HTML

> Sertakan Preamble di atas.

**Tujuan:** Menyajikan konten mentah user dari origin terpisah (`usercontent.file.kppbwi.app`) sesuai alternatif ADR-06 dan mengaktifkan preview HTML di iframe sandbox.
**Prasyarat:** v1.0.0; DNS + site aaPanel + SSL untuk domain konten (pemilik).
**Baca dulu:** SPEC §13 ADR-06, §8.9, §8.14 (HTML), §10.3, §12; PRD epic E8, E14.
**Tugas:** (1) `CONTENT_BASE_URL` diaktifkan: `files/sign` mengembalikan URL absolut ke origin konten; server menerima request `/raw|/download|/zip|/thumb|/public/.../raw` di origin itu (deteksi via `Host`), tanpa cookie sesi (signed URL saja), CSP `sandbox`, `Cross-Origin-Resource-Policy: cross-origin` hanya untuk origin aplikasi; origin aplikasi menolak `/raw` inline (redirect ke origin konten); (2) aaPanel: site Reverse Proxy kedua ke `127.0.0.1:8080` (dokumentasi klik-per-klik tambahan di `DEPLOY-AAPANEL.md`); (3) preview HTML: viewer menampilkan `<iframe sandbox="" src=<signed raw url di origin konten>>` (tanpa `allow-scripts`, tanpa `allow-same-origin`), tombol "Tampilkan source"; (4) `connect-src`/`img-src`/`media-src` CSP aplikasi menambahkan origin konten; (5) test: konten HTML dengan script tidak bisa mengakses origin aplikasi (E2E memeriksa `document.cookie` kosong & `postMessage` tidak sampai).
**File yang dibuat/diubah:** `internal/httpapi/{content_origin,sign}.go`, middleware CSP, `web/src/lib/media-url.ts`, `web/src/features/viewer/text/HtmlPreview.tsx`, `docs/DEPLOY-AAPANEL.md`, SPEC ADR-06 (status).
**Acceptance criteria:** - [ ] Semua media/download dimuat dari origin konten; cookie sesi tidak dikirim ke origin konten; HTML preview berjalan di iframe sandbox tanpa akses ke origin aplikasi; semua E2E lama tetap lulus.
**Test wajib:** Go (host routing, sign absolut), Playwright `content-origin.spec.ts`.
**Verifikasi:** `make test && pnpm e2e`.
**Di luar scope step ini:** CDN.
**Commit:** `feat(security): separate user-content origin and sandboxed HTML preview`

---

# Definition of Done global

Sebuah step dianggap selesai hanya jika semua ini benar:

1. Semua **Acceptance criteria** step tercentang dengan bukti nyata (perintah dijalankan, output dicatat ringkas di PROGRESS).
2. **Test wajib** step ada, lulus, dan menguji perilaku (bukan implementasi); test lama tetap lulus (`make test`, `pnpm test`, `pnpm e2e` untuk step yang menyentuh UI).
3. `make lint` (golangci-lint termasuk forbidigo `os.*`), `pnpm lint`, `pnpm typecheck` bersih; `make build` sukses dengan `CGO_ENABLED=0`.
4. `docs/openapi.yaml` sinkron untuk endpoint yang ditambah/diubah; SPEC diperbarui jika ada deviasi dan dicatat di PROGRESS "Deviasi dari SPEC".
5. Tidak ada rahasia di kode, fixture, log, atau commit; tidak ada versi library yang dihardcode dari ingatan tanpa verifikasi registry.
6. Semua string UI lewat i18n (`id` + `en`), komponen memakai token desain, tidak ada warna hardcode.
7. Setiap endpoint baru: AuthZ (sesi + `must_change_password` + kepemilikan/scope), validasi input, kode error SPEC §6.2, dicakup test IDOR (sejak Step 20: otomatis).
8. Setiap operasi file lewat `internal/fsjail`; setiap CLI lewat `internal/exec` (argv, timeout, nice).
9. PROGRESS.md dicentang + tanggal (`date`) + hash commit + ringkasan + "catatan untuk step berikutnya".
10. Commit mengikuti Conventional Commits; satu step boleh lebih dari satu commit, tetapi commit terakhir step menyebut `Step <N>` di body.

# Checklist rilis v1.0 (dijalankan di Step R-F)

Dipetakan ke `promp-master.md` §18 dan PRD:

- [ ] `PRD.md`, `SPEC.md`, `PROMPT.md`, `PROGRESS.md`, `.env.example` ada di root; `.env` di `.gitignore` dan tidak pernah ter-commit (`git log --all -- .env` kosong).
- [ ] Setiap baris "Wajib" di matriks paritas PRD §5 terverifikasi manual di produksi (tabel di `docs/reviews/review-F.md`) dan dipetakan ke epic + step.
- [ ] 5 mode view (Details, List, Tiles, Large icons, Extra large icons), pilihan kolom, sort per kolom (+ sekunder) berfungsi dan tersimpan per user.
- [ ] Code editor (Monaco: tab, explorer, encoding, EOL, versi + diff, konflik), image viewer, PDF viewer, music player (+ mini-player), video player (+ subtitle, PiP, resume) berfungsi di produksi.
- [ ] Arsip: zip (termasuk AES), 7z, rar (read), tar, tar.gz, tar.bz2, tar.xz, tar.zst — compress/extract sesuai matriks; fixture zip-slip/bomb ditolak; archive browser bekerja.
- [ ] Upload tus resumable (pause/resume/reload) dan remote download dengan SSRF guard teruji di produksi.
- [ ] Share link publik (password, expiry, limit, file request) dan share ke user (viewer/editor) berfungsi; tamu tidak melihat path di atas item; IDOR suite lulus.
- [ ] Storage `/www/storage/FileManager/<username>` owner `filemanager` 0750; folder dibuat saat login pertama; isolasi via `fsjail` (user B tidak bisa mengakses A dengan cara apa pun yang diuji).
- [ ] Admin dari `.env` dengan wajib ganti password; `ADMIN_INITIAL_PASSWORD` sudah dihapus dari `.env`; hanya admin yang membuat user; admin terakhir terlindungi.
- [ ] Domain `file.kppbwi.app` lewat aaPanel (site tercatat di panel), SSL Let's Encrypt + Force HTTPS, `.well-known` disajikan; aplikasi di `/www/wwwroot/file.kppbwi.app` terpisah dari storage; Nginx tidak menyajikan source/binary/`.env`/`/www/storage`.
- [ ] Dark/light/system mode tanpa flash; token desain terdefinisi; kontras WCAG AA lulus; axe 0 serius; responsif 3 breakpoint.
- [ ] Header keamanan & CSP final aktif di produksi; rate limit aktif; `govulncheck` & `pnpm audit` bersih; `docs/SECURITY.md` lengkap.
- [ ] Target performa SPEC §1.6 tercatat di `docs/PERF.md` (listing 10.000 p95 < 500 ms; render < 1 s; upload 20 GB atau setidaknya 1 GB teruji dengan RAM stabil; idle < 100 MB; JS awal < 300 KB gzip).
- [ ] Backup harian SQLite berjalan dan restore teruji; dokumentasi backup storage ada.
- [ ] Tidak ada versi library yang diklaim tanpa verifikasi; klaim aaPanel di luar prompt bertanda "perlu verifikasi"; semua asumsi PRD §11 masih berlaku atau diperbarui.
- [ ] Semua review A–F tanpa blocker/major terbuka; `CHANGELOG.md` v1.0.0; tag `v1.0.0` dibuat dengan persetujuan pemilik.
