# PROGRESS.md — Checklist & Jurnal File Manager (file.kppbwi.app)

| | |
| --- | --- |
| Acuan | `PROMPT.md` (urutan step) · `PRD.md` · `SPEC.md` |
| Dibuat | 14 Oktober 2026 |

## Cara memakai

1. File ini **hanya** diperbarui oleh AI di akhir setiap step (atau di tengah step bila sesi berisiko terputus). Jangan mencentang step yang acceptance criteria-nya belum dibuktikan.
2. Setelah step selesai: ubah `- [ ]` menjadi `- [x]`, isi `Selesai` dengan tanggal dari perintah `date` (bukan perkiraan), `Commit` dengan hash pendek commit terakhir step, dan `Catatan` dengan ringkasan 1–3 kalimat + hal yang perlu diketahui step berikutnya.
3. Jika step dipecah (mis. 12a-1, 12a-2) tambahkan sub-baris di bawah step induk.
4. Deviasi dari SPEC dicatat di tabel "Deviasi dari SPEC" **dan** SPEC.md diperbarui di commit yang sama.
5. Bagian "Status saat ini" ditulis ulang setiap kali ada perubahan, cukup jelas agar sesi baru yang tidak tahu apa-apa bisa melanjutkan.

## Status saat ini

Dokumen perencanaan disetujui pemilik 14 Oktober 2026 (jawaban Q1–Q7 di PRD §11.3); pemilik meminta semua step dijalankan berurutan.

**Step 1 selesai (14 Oktober 2026).** Monorepo, config loader, logging, `/healthz` `/readyz`, webassets embed, scaffold web, Makefile, CI, openapi/SECURITY kerangka.

**Step 2 selesai (14 Oktober 2026, commit `09c4e76`).** SQLite WAL (`modernc.org/sqlite`) dengan writer pool tunggal + reader pool, goose migrations 0001–0003 (users, sessions, settings, user_preferences, folder_view_prefs, audit_logs, login_attempts) embedded, sqlc (generated code di-commit, `make sqlc-check` + CI), `Repository` interface per domain (`db.NewStore`), backup `VACUUM INTO`, subcommand `migrate up|down|status`, `serve` auto-migrate + `/readyz` cek DB/migrasi. `make lint`, `make test`, `make build` hijau. Step berikutnya: **Step 3**.

**Step 3 selesai (14 Oktober 2026, commit `1341a58`).** `internal/auth` (argon2id PHC + rehash, kebijakan password + daftar 100k SecLists embed gzip, sesi server-side `fm_session` idle/absolut/sliding + maks 20/user + cleanup, CSRF HMAC + cek Origin/Sec-Fetch-Site, limiter login 20/60 s per IP (jendela geser) + lockout tiap kelipatan 5 kegagalan 1/5/15/60 menit, seed admin sekali + warning password tersisa, provisioning home via `os.Root` 0750), `internal/audit` (recorder, detail sensitif dibuang), `httpapi` (`RequireAuth`/`RequirePasswordChanged`/`RequireAdmin`/`OriginCheck`/`RequireCSRF`, `/auth/login|logout|csrf|me|change-password`, `/me/preferences` GET/PUT), `serve` memanggil `Seed` + job maintenance. Coverage auth 88,0 %, httpapi 87,4 %. Semua AC Step 3 diverifikasi lewat `curl` terhadap binary nyata (termasuk restart dengan `ADMIN_INITIAL_PASSWORD` tersisa → warning, password tidak di-reset). Step berikutnya: **Step 4** (`fsjail`).

**Step 4 selesai (14 Oktober 2026, commit `c7d2b53`).** `internal/fsjail` (VPath Clean/Join/ValidateName, Jail di atas `*os.Root`: statx Lstat/Stat/ReadDir dengan klasifikasi symlink, Create via openat O_NOFOLLOW, Chmod fchmodat2 nofollow, Symlink hanya relatif dalam root, WalkDir/DirSize tanpa mengikuti symlink, SubRoot untuk share, AbsForCLI, CopyFile/CopyTree/MoveTree + EXDEV fallback, SysDir + MoveOut/MoveIn via renameat2, LockManager sadar-ancestor, ProvisionHome), `httpapi.WriteFSError`, probe readiness dipindah ke `config`, lint forbidigo diperluas (`os.OpenRoot`, `filepath.Walk/EvalSymlinks`). Coverage fsjail 91 %, TOCTOU `-race -count=20` lulus, lint negatif terbukti. Keputusan `go.mod`: tetap `go 1.26.0` (dipaksa dependensi) → dokumen menjadi "Go ≥ 1.26". Step berikutnya: **Step 5** (API listing).

**Step 5 selesai (14 Oktober 2026, commit `6d72ef0`).** `internal/listing` (natural sort kustom tanpa `collate.Numeric`, SortSpec 11 field + sekunder, cursor HMAC stabil, cache LRU 15 s + invalidasi, counts, MetaProvider hook, fixture 10k + benchmark), `internal/files` (tabel tipe + template), `fsjail.CountEntries`, endpoint `GET /files/list|stat`, `POST /files/mkdir|create` dengan cache jail per user dan lock path. p95 10k dingin 477 ms (tanpa race), cached 0,43 ms. `make lint`/`make test`/`make build` hijau; AC diverifikasi lewat `curl` ke binary nyata. Step berikutnya: **R-A** (review independen Step 1–5).

Step yang sedang berjalan: — · Langkah terakhir yang selesai di dalamnya: — · Perintah terakhir yang gagal (jika ada): —

Toolchain server (dipasang pra-Step 1 / Step 1): Go 1.27.2 di `/usr/local/go/bin` (**PATH shell non-login tidak memuatnya** → `export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin`, Makefile sudah menambahkannya), pnpm 12.11.2 via corepack, golangci-lint 2.14.0 (binary prebuilt di `/usr/local/bin`), govulncheck di `~/go/bin`. Swap 2,8 GB sudah ada.

## Checklist step

### Milestone M1 — Fondasi
- [x] Step 1 — Scaffolding monorepo, Makefile, lint, CI, config loader, logging, `/healthz` `/readyz`
  - Selesai: 2026-10-14 · Commit: e357924 (feat) + docs(progress) berikutnya · Catatan: `backend/` (module `kppbwi.app/filemanager`, chi v5): `internal/config` (52 env SPEC §3, parser ukuran/durasi/bool/list, dotenv tanpa expand, validasi fail-fast exit 2, subfolder DATA_DIR 0750, deteksi cross-FS via device id, `check-deps`), `internal/httpapi` (TrustedPeer → RequestID → RealIP → AccessLog → Recoverer → SecurityHeaders → Compress; `/api/v1` dengan Timeout 60 s + MaxBytes 1 MB; envelope error + 36 kode; `/healthz` `/readyz`), `internal/webassets` (embed `all:dist`, SPA fallback, cache immutable untuk `/assets/`, dotfile 404), `cmd/filemanager` (`serve|version|check-deps|migrate`, `--env-file`, umask 0027, graceful shutdown). `web/`: Vite 8 + React 19 + TS 6.0.3 (lihat deviasi) + ESLint 10 + Prettier + Vitest 5. Coverage: config 82,9 %, httpapi 93,4 %. Ukuran perubahan ≈ 2.400 baris non-generated (Go ≈ 1.900 termasuk ≈ 700 baris test, web ≈ 150, YAML/Makefile ≈ 380) — melebihi target ±1.500 karena 13 tugas Step 1 memang luas; tidak dipecah. Verifikasi nyata: `make lint` 0 issue; `make test` lulus (Go + Vitest); `make build` → `bin/filemanager` statically linked 8,4 MB; curl `/healthz` 200, `/readyz` 200, `/tidak-ada` 200 SPA `no-store`, `/api/v1/x` 404 `NOT_FOUND` + requestId, `/.env` 404, `/assets/*` immutable; tanpa `APP_SECRET` exit 2; `LISTEN_ADDR=0.0.0.0` exit 2 (pesan loopback); `check-deps` tabel semua tool TIDAK ADA/NONAKTIF; `govulncheck` bersih; `ci.yml` valid secara struktur (actionlint tidak dipasang). **Untuk Step 2:** tabel `users/sessions/settings/user_preferences/audit_logs` + goose embedded + sqlc (`modernc.org/sqlite`, cek versi di registry); `readyz` menerima `ReadyChecker` tambahan (`httpapi.Options.ReadyChecks`) → tambahkan cek DB; subcommand `migrate` masih placeholder (exit 1); `config.Load` belum membuka DB (SPEC §3.4 butir 3/8/9 dikerjakan Step 2–3); `internal/db` sudah dikecualikan dari `forbidigo`; jalankan Go dengan `GOMAXPROCS=1` di server.
- [x] Step 2 — DB: migrasi goose, sqlc, tabel inti
  - Selesai: 2026-10-14 · Commit: 09c4e76 (feat) + docs(progress) berikutnya · Catatan: `internal/db`: `Open(path)` → DSN `_pragma=busy_timeout(5000)/journal_mode(WAL)/synchronous(NORMAL)/foreign_keys(ON)/temp_store(MEMORY)/cache_size(-16000)`, writer pool `SetMaxOpenConns(1)` + `_txlock=immediate`, reader pool max(2, min(8, 2×CPU)); `WithTx` (rollback on error/panic); `NewID()` UUIDv7, `Now()/FormatTime/ParseTime` ISO-8601 ms UTC (`TimeLayout`), `Ptr()`; `MigrateUp/Down/Status/HasPendingMigrations/Ready` via goose Provider (`migrations.FS` embed); `Backup(ctx, dst)` VACUUM INTO → temp → rename, menolak timpa, mode 0600; `Store` (`db.NewStore`) mengimplementasikan `Repository` (User/Session/Settings/Pref/Audit/LoginAttempt + `WithTx` bersarang), `IsNotFound`, `IsUniqueViolation`, `ErrPrefsTooLarge`; `GetPrefs/SetPref/SetPrefs` dokumen JSON ≤ 64 KB; `UpsertFolderViewPref` memangkas ke 500 baris. `main.go`: `migrate [up|down|status]` memakai nilai env mentah (`config.ReadValues`, tanpa validasi penuh) dan umask 0027 (DB 0640); `serve` membuka `DATA_DIR/db/app.db`, auto-migrate (`--no-migrate` → `/readyz` 503 `pending migrations`). Tool: sqlc 1.31.1 prebuilt di `/usr/local/bin` (rilis GitHub tidak menyediakan file checksum → tidak diverifikasi, dicatat). Verifikasi nyata: `go test ./internal/db/ -race` lulus (coverage 78,0 %; termasuk test 5 s baca paralel saat tulis tanpa SQLITE_BUSY, up→down→up, backup), `golangci-lint` 0 issue, `make sqlc-check` bersih, `make test` + `make build` hijau; AC dijalankan pada DATA_DIR scratch (bukan `/www/storage` agar tidak membuat DB milik root sebelum Step 22): `migrate status` 3 pending → `up` applied [1 2 3] → `status` 3 applied → `sqlite3 … "PRAGMA journal_mode"` = `wal` → `down`×3 lalu `down` "nothing to roll back" → `up` kembali; `serve` → `/readyz` 200 (db ok, migrations ok) dan `app.db-wal` muncul; `serve --no-migrate` pada DB kosong → 503 degraded. Ukuran: +2.091 baris non-generated (≈ 900 test, 130 SQL migrasi, 200 query, go.sum) — melebihi ±1.500, tidak dipecah. **Untuk Step 3:** pakai `db.NewStore(database)` (atau `Repository`) — `CreateUser`/`GetUserByUsername`/`RecordLoginFailure`/`RecordLoginSuccess`/`SetHomeProvisioned`/sesi/`InsertLoginAttempt`/`InsertAudit` sudah ada; seed admin (SPEC §3.4 butir 8/9) belum: lakukan di `serve` setelah migrasi (butuh `cfg.AdminUsername`/`AdminInitialPassword`, argon2id); `sessions.id` = SHA-256 hex token cookie, `csrf_secret` 32 byte; hindari mencampur `sqlc.arg` dan `?` polos dalam satu query (SPEC §4.3); `httpapi.Options` belum punya field DB/Store — tambahkan saat auth middleware dibuat.
- [x] Step 3 — Auth: seed admin, login/logout, sesi, CSRF, rate limit + lockout, wajib ganti password, provisioning folder
  - Selesai: 2026-10-14 · Commit: 1341a58 (feat) + docs(progress) berikutnya · Catatan: `auth.Service` (`NewService(cfg, repo, audit, logger, now)`; `Seed`, `Login`, `Authenticate`, `Logout`, `ChangePassword`, `UserByID` cache 5 s + `InvalidateUser`, `RunMaintenance`), `auth.Sessions` (`Create/Lookup/Revoke/RevokeAllExcept/Cleanup`, `Cookie`/`ClearCookie`), `auth.ProvisionHome(storageRoot, user)` memakai `os.OpenRoot` + `Root.Lstat/Mkdir` (**Step 4: pindahkan ke fsjail**; `os.OpenRoot` saat ini tidak dilarang forbidigo — tambahkan ke pola larangan dengan pengecualian fsjail), `audit.Recorder.Record/RecordWith` + konstanta aksi SPEC §8.16, `httpapi.UserFrom/SessionFrom`. Login unknown-user menjalankan argon2 dummy (±0,4 s produksi). Password baru dicek kebijakan sebelum verifikasi password lama; limiter ganti password hanya menghitung percobaan terverifikasi. Kegagalan ke-5 dijawab 401, kunci terlihat pada percobaan ke-6 (423 + Retry-After). Tabel `notifications` belum ada → home reused hanya audit + log WARN. Test memakai `auth.DefaultParams` ringan (TestMain) agar `-race` cepat; parameter 64 MiB diverifikasi di `TestVerifyReportsRehashWhenParamsDiffer` dan dipakai di uji curl. Ukuran perubahan ≈ +3.550 baris non-generated (≈ 1.350 test, ≈ 230 openapi, sisanya kode) — melebihi ±1.500, tidak dipecah (satu alur keamanan utuh).
- [x] Step 4 — `fsjail` + test keamanan path dan symlink
  - Selesai: 2026-10-14 · Commit: c7d2b53 (feat) + docs(progress) berikutnya · Catatan: `internal/fsjail` = satu-satunya pemakai `os.Root`. API: `Clean/Normalize(raw) (VPath, error)` (NFC, tolak `..`/`.`/NUL/kontrol, 255/4096/64, `.system` di root; reason di `details.reason`), `Join`, `ValidateName` (warning `windows_incompatible`), `VPath.Rel/Dir/Base/HasPrefix/Segments`; `Open(abs)`, `OpenUser(storageRoot, username, userID)` (`ErrHomeMissing`/`ErrHomeNotDir`), `(*Jail).SubRoot(dir, readOnly)` untuk share, `Lstat/Stat/ReadDir` (statx: `Info{Name, Path, IsDir, IsSymlink, SymlinkTarget, SymlinkBroken, SymlinkReason escape|missing|loop|error, Size, Mode, ModTime, AccessTime, ChangeTime, BirthTime*, Nlink, Ino, Dev}`; ReadDir **tidak mengurutkan**), `Open`, `OpenFile`, `Create(p, excl)` (openat O_NOFOLLOW), `Mkdir/MkdirAll` 0750, `Rename`, `Remove` (`ErrNotEmpty`), `RemoveAll`, `Symlink(target, link)` (target relatif & resolve di dalam root), `Readlink`, `Chmod` (fchmodat2 nofollow, no-op pada symlink, setuid dibuang), `ChmodRecursive`, `Chtimes`, `WalkDir(ctx, p, fn)` (fn menerima VPath string; symlink dir tidak diikuti), `DirSize`, `FS()`, `Statfs()`, `AbsForCLI/HostPath` (menolak komponen symlink), `CopyFile/CopyTree/MoveTree(ctx, src, dst, progress)` (copy_file_range, EXDEV → `.fm-partial-<id>`), `OpenSys(dataDir, sub)` → `*SysDir` (`Create/Open/Lstat/MkdirAll/Remove/RemoveAll/Rename/ReadDirNames/HostPath/FS`), `MoveOut(ctx, src, sys, name)` / `MoveIn(ctx, sys, name, dst)` (renameat2 NOREPLACE, EXDEV fallback), `NewLockManager()` → `TryAcquire(userID, mode, owner, paths...)` / `Acquire(ctx, …)` (`SyncLockTimeout` 2 s, `TaskLockTimeout` 30 s; `ErrLocked` + `By`), `ProvisionHome(storageRoot, username, provisionedBefore)`, error `*fsjail.Error{Code, Op, Path, Reason, By}` + sentinel (`ErrNotFound`, `ErrExists`, `ErrEscape`, `ErrInvalidPath`, `ErrInvalidName`, `ErrNotEmpty`, `ErrLocked`, `ErrReadOnly`, `ErrIsDir`, `ErrNotDir`, …), `APIError(err)` → `httpapi.WriteFSError(w, r, err)`. Setiap method publik memanggil `checkVPath` (VPath tidak bersih → `PATH_INVALID`). Verifikasi nyata: `go test ./internal/fsjail/ -cover` 91,4–91,7 %; `go test -race -run TOCTOU -count=20` lulus (10,5 s); `-race` seluruh package lulus; `BenchmarkReadDir10k` ≈ 64 ms/op (10.000 entri, statx per entri); lint negatif: `os.Open` di `internal/httpapi` → `forbidigo` gagal (dibuktikan, file probe dihapus); `make lint`/`make test`/`make build` hijau. **Untuk Step 5 (listing):** gunakan `OpenUser` per request (handle murah) atau cache jail per user; `ReadDir(VPath)` → `[]Info` sudah berisi lstat + klasifikasi symlink; natural sort & cursor di `internal/listing`; `Info.Mode&fs.ModeSymlink` + `SymlinkBroken` untuk UI; `Statfs()` untuk kuota disk; `httpapi.WriteFSError` untuk semua error jail; mkdir/create via `Jail.Mkdir`/`Jail.Create(p, true)`; validasi nama via `fsjail.Join(dir, name)` (warning dikembalikan ke client sebagai `warnings`). Ukuran: +4.242 baris (≈ 1.900 test) — melebihi ±1.500, tidak dipecah (package keamanan utuh + test wajib SPEC).
- [x] Step 5 — API listing (sort server, cursor, metadata), mkdir, create file, stat
  - Selesai: 2026-10-14 · Commit: 6d72ef0 (feat) + docs(progress) berikutnya · Catatan: `internal/listing` (`Item` DTO camelCase SPEC §6.3 + `type`/`category`/`ctime`; `NaturalKey/CompareNatural/NaturalLess` — run digit numerik + `x/text/collate` IgnoreCase locale `id`/`en`, tanpa `collate.Numeric`, CI `make check-sort-policy`; `SortSpec` 11 field + sekunder, unknown-last, desc hanya field utama; cursor `Signer` HMAC-8 byte berisi `{v,s,k,k2,n,p,i}` → posisi via binary search, stabil saat insert; `Cache` LRU 64 MB TTL 15 s validasi mtime + `Invalidate(user,scope,dir)`/`InvalidateUser`; `Service.List/Stat/Invalidate`, `counts=1` ≤ 2.000 folder/permintaan cache 60 s; `MetaProvider` hook (nil sampai 10b); `fixture.Generate` 10k). `internal/files` (`Lookup(name)` → ext/MIME/kategori/label `type.<kategori>.<key>`/thumb; template embed 11 jenis). `fsjail.CountEntries`. `httpapi`: `GET /files/list|stat`, `POST /files/mkdir|create` (`{path}` penuh atau `{path,name}`, `parents`, `template|content`), group `OriginCheck+RequireAuth+RequireCSRF+RequirePasswordChanged`, cache jail per user (`jails.go`, refcount, idle 5 menit), lock `Write` pada target, `/.system` → 404, `share=`/`as_user=` → 400 `not_implemented`. **Angka:** `BenchmarkListDir10k` 219 ms/op dingin (68 MB alloc), cached 0,43 ms; `TestList10kLatency` tanpa race: min 199 ms, median 276 ms, **p95 477 ms** (< 500 ms, sempit di 1 vCPU; ReadDir+statx ≈ 64 ms, sisanya kunci kolasi + sort + GC); coverage listing 94,3 %, files 76,2 %, fsjail 91,5 %, httpapi 84,1 %. AC `curl` terhadap binary nyata: urutan `é.txt,file1,file2,File3,file10`; `limit=2` + cursor lanjut tanpa duplikat setelah mkdir/create; `../x` → 400 `parent_segment`, `%00` → 400 `control_char`, `/.system` → 404, `/file1` → 400 `not_a_directory`; mkdir `con` → 201 `windows_incompatible`, `a/b` → 400 `NAME_INVALID slash`; create template md → 201 9 byte `text/markdown`, duplikat → 409 `existing`; tanpa CSRF 403; tanpa sesi 401; must_change → 403. **Untuk Step 6/7:** bentuk respons list `{items:[Item], nextCursor, total, path, parentPath, dir:{path,etag,mtime,itemCount}}`; `Item` punya `typeLabel` (kunci i18n, mis. `type.image.jpeg`, `type.folder`), `category` untuk ikon, `mode`/`modeOctal`, `etag`; stat = Item + `stat{…,nlink}` + `warnings` + `versionsCount` + `shares`; error `VALIDATION_FAILED details.fields.{sort,dir,limit,types,cursor}`; `foldersFirst`/`hidden` default true, `counts` default false; bahasa kolasi dari pref `ui.language`. Ukuran: +3.413 baris (≈ 1.150 test, 200 openapi) — melebihi ±1.500, tidak dipecah.
- [ ] Step R-A — Review independen Step 1–5
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 6 — Frontend shell: Vite/React, router, design token, light/dark, i18n, `/dev/design`, login & ganti password
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 7a — File browser inti: layout, sidebar, address bar, riwayat, Details view tervirtualisasi, status bar
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 7b — Kolom, sort per kolom, seleksi, keyboard, filter cepat, type-ahead, tab folder, panel preview
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 8 — View List, Tiles, Large icons, Extra large icons + pipeline thumbnail + media_metadata
  - Selesai: — · Commit: — · Catatan: —

### Milestone M2 — Operasi file & transfer
- [ ] Step 9a — Operasi file backend: rename, copy, move, duplicate, symlink, chmod, batch rename, delete → Recycle Bin, konflik, lock, undo
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 9b — Operasi file frontend: context menu, toolbar, clipboard, dialog pindah/salin, konflik, drag & drop, rename inline, batch rename, undo toast
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 10a — Recycle Bin, kuota, hitung ukuran folder, checksum
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 10b — Favorit, pin, catatan, tag warna, Recent, Properties lengkap, Analisis penyimpanan
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step R-B — Review independen Step 6–10b
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 11 — Task engine + SSE + panel task + notifikasi in-app
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 12a — Upload tus + precheck + panel transfer + upload folder + paste clipboard
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 12b — Download Range, zip streaming multi-item, signed URL, batas paralel
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 13 — Remote download + SSRF guard + allow/denylist
  - Selesai: — · Commit: — · Catatan: —

### Milestone M3 — Arsip, search, editor, viewer
- [ ] Step 14a — Arsip: compress & extract semua format, limit keamanan, fixture berbahaya
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 14b — Archive browser, extract sebagian, tambah/hapus entry zip, password & encoding nama
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step R-C — Review independen Step 11–14b
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 15 — Search: nama, isi file, replace in files dengan preview + rollback, pencarian global, riwayat
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 16a — Editor Monaco inti: workspace, tab, explorer, save/save all, pengaturan, encoding, EOL, bahasa, draft lokal
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 16b — Editor lanjutan: konflik + diff, riwayat versi + restore, find in files, go to line, format, Markdown preview, tail mode
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 17a — Viewer gambar (zoom/pan/rotate/slideshow/EXIF) + PDF (pdf.js, Range, password)
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 17b — Music player + mini-player, video player, viewer teks/Markdown/CSV/JSON, Quick Look
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step R-D — Review independen Step 15–17b
  - Selesai: — · Commit: — · Catatan: —

### Milestone M4 — Sharing & admin
- [ ] Step 18a — Sharing: link publik (token, password, expiry, limit download, file request) + halaman publik `/s/<token>`
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 18b — Share ke user, "Dibagikan ke saya" (viewer/editor via fsjail sub-root), "Share saya", QR, log akses, badge
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 19a — Admin: dashboard, users (CRUD, reset password, nonaktif, hapus/arsip), settings override
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 19b — Admin: audit log + export CSV, tasks semua user, maintenance; profil & sesi user
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step R-E — Review independen Step 18a–19b
  - Selesai: — · Commit: — · Catatan: —

### Milestone M5 — Hardening, performa, deployment
- [ ] Step 20 — Hardening: CSP final, review OWASP, test IDOR, rate limit semua endpoint, govulncheck + pnpm audit, SECURITY.md
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 21 — Performa & aksesibilitas: folder 10.000 file, bundle budget, audit axe, responsif mobile
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 22 — Deployment: install.sh, update.sh, systemd, `nginx-aapanel.conf`, `docs/DEPLOY-AAPANEL.md`, smoke test di Ubuntu + aaPanel
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step R-F — Review independen Step 20–22 + verifikasi checklist rilis v1.0
  - Selesai: — · Commit: — · Catatan: —

### Milestone M6 — Opsional fase akhir
- [ ] Step 23a — TOTP 2FA
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 23b — Dual-pane
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 23c — Duplicate finder
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 23d — Transcoding HLS on-the-fly (opsional, dibatasi CPU)
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 23e — Origin konten terpisah + preview HTML
  - Selesai: — · Commit: — · Catatan: —

## Deviasi dari SPEC

| Tanggal | Step | Bagian SPEC | Sebelum | Sesudah | Alasan |
| --- | --- | --- | --- | --- | --- |
| 2026-10-14 | 1 | §3.3, §3.4 butir 6 | Tidak ada cara menjalankan di production sebagai root | Env `ALLOW_ROOT=true` (default false, hanya uji lokal) ditambahkan ke tabel §3.3 | AC Step 1 menjalankan binary di server sebagai root sebelum user `filemanager` ada; tanpa flag, aturan "tolak root di production" membuat smoke test mustahil. SPEC sudah diperbarui. |
| 2026-10-14 | 1 | §6.2 | Tidak ada kode untuk 405 | `METHOD_NOT_ALLOWED` (405) ditambahkan | Router chi membedakan 404/405; envelope harus konsisten. SPEC sudah diperbarui. |
| 2026-10-14 | 1 | PROMPT Step 1 tugas 8 | `dist/index.html` placeholder di-commit | Hanya `dist/.gitkeep` di-commit; placeholder HTML menjadi konstanta di `webassets/dist.go` | `make build` menimpa `dist/index.html`; jika di-commit, working tree selalu kotor setelah build. `go build` tetap sukses tanpa frontend (AC terpenuhi). |
| 2026-10-14 | 1 | ADR-21 (versi library) | "versi stable terbaru" | TypeScript dipatok `6.0.3` (bukan latest 7.0.2) | `typescript-eslint` 8.71 menolak TS 7 (`peerDependencies.typescript >=4.8.4 <6.1.0`). Naikkan saat typescript-eslint mendukung TS 7. |
| 2026-10-14 | 1 | §1 fakta server, §3.3 `RG_PATH` | "`rg` ada" | `rg` TIDAK terpasang | `rg` yang terdeteksi saat perencanaan adalah fungsi shell lingkungan Claude Code (wrapper), bukan `/usr/bin/rg`. `check-deps` benar; `install.sh` (Step 22) memasang `ripgrep`. SPEC/PRD/PROMPT sudah dikoreksi. |
| 2026-10-14 | 2 | PROMPT Step 2 tugas 5 (bukan SPEC) | id = ULID 26 karakter, waktu = Unix millis | id = UUIDv7 36 karakter, waktu = TEXT ISO-8601 ms UTC | PROMPT bertentangan dengan SPEC §4.3; SPEC adalah kontrak → PROMPT.md dikoreksi. |
| 2026-10-14 | 2 | §4.3 (sqlc) | — | Tambahan konvensi: `emit_pointers_for_null_types`, skema sqlc dibaca dari `migrations/`, larangan mencampur placeholder bernama dan `?` polos, `Repository.WithTx` | Ditemukan saat implementasi (sqlc menomori `sqlc.arg` sebagai `?N` sehingga `?` polos bergeser → "missing argument with index 4"). SPEC diperbarui (penambahan, bukan perubahan perilaku). |
| 2026-10-14 | 3 | §10.5 (login per IP) | 20/menit burst 5 (token bucket) | 20 percobaan per jendela geser 60 detik per IP | Burst 5 membuat percobaan ke-6 dari satu IP terkena 429 sebelum lockout 423 (AC Step 3). Token bucket kapasitas 20 pun gagal memenuhi AC "21 request/menit → 429" di server nyata karena argon2 ±0,4 s/percobaan memberi waktu isi ulang. SPEC diperbarui. |
| 2026-10-14 | 3 | §10.5 (ganti password) | 5 / 15 menit / sesi (semua percobaan) | Hanya percobaan yang sampai ke verifikasi password sekarang yang dihitung; pelanggaran kebijakan password baru tidak | Kebijakan dicek dulu (tanpa rahasia) supaya user yang salah memilih password baru tidak terkunci 15 menit; jatah tetap melindungi password lama dari tebakan. SPEC diperbarui. |
| 2026-10-14 | 3 | §8.1, §8.16 | Aksi audit lockout tidak ada | `auth.account_locked` ditambahkan; kegagalan pemicu kunci tetap dijawab 401 generik | Kejelasan audit; respons generik mencegah enumerasi. SPEC diperbarui. |
| 2026-10-14 | 3 | PROMPT Step 3 (bukan SPEC) | AC ganti password `200`; audit `login.success` dll.; `common_passwords.txt` | `204` sesuai SPEC §6.3; nama aksi mengikuti SPEC §8.16; file embed `common_passwords.txt.gz` (sorted, 264 KB) | SPEC adalah kontrak; gzip memperkecil binary 0,5 MB. PROMPT.md dikoreksi. |
| 2026-10-14 | 4 | §2, §3, ADR-01 (Go ≥ 1.25) | `go 1.25` | `go 1.26.0`; dokumen "Go ≥ 1.26 patch terbaru" | `modernc.org/sqlite`, `goose`, `golang.org/x/crypto`, `golang.org/x/sys` mendeklarasikan `go 1.26` → MVS memaksa 1.26; toolchain server 1.27.2. SPEC/PROMPT/PRD diperbarui. |
| 2026-10-14 | 4 | §5.5 lokasi lock manager | `internal/files/lock.go` | `internal/fsjail/lock.go` | PROMPT Step 4 menempatkannya di fsjail; paket keamanan utuh dan `ErrLocked` bagian dari error jail. SPEC diperbarui. |
| 2026-10-14 | 4 | §5.3 (`Create`, `Chmod`, pertahanan tambahan) | Tidak dirinci | `Create` lewat `openat(dirfd, name, O_NOFOLLOW)` pada handle dari `os.Root` (os.Root mengikuti symlink terakhir sendiri sehingga `O_NOFOLLOW` yang diteruskan ke `Root.OpenFile` diabaikan — ditemukan lewat test); `Chmod` lewat `fchmodat2(AT_SYMLINK_NOFOLLOW)`; `checkVPath` di setiap method publik; `ReadDir` tidak mengurutkan | Diperlukan untuk memenuhi kebijakan §5.4 secara bebas race. SPEC diperbarui (penambahan). |
| 2026-10-14 | 4 | §8.2 / Step 3 tugas 8 | `auth.ProvisionHome` memakai `os.OpenRoot` langsung | `fsjail.ProvisionHome(storageRoot, username, provisionedBefore)`; `auth.ProvisionHome` hanya membungkus | Sesuai rencana Step 4; `os.OpenRoot` kini dilarang lint di luar fsjail/config/db/thumbs/upload/cmd. |
| 2026-10-14 | 5 | PROMPT Step 5 AC & tugas (bukan SPEC) | AC urutan `file1, file2, File3, file10, é.txt`; warning `windows_reserved`; respons `next_cursor/parent/folder`; fixture di `testdata/` | Urutan `é.txt, file1, …` (kolasi: `é` setelah `e`, sebelum `f` — contoh SPEC §8.3); warning `windows_incompatible` (SPEC §5.2); respons camelCase `nextCursor/parentPath/dir` (SPEC §6.3); fixture di `internal/listing/fixture` | SPEC adalah kontrak; `testdata` diabaikan go tool sehingga tidak bisa di-import. PROMPT.md dikoreksi. |
| 2026-10-14 | 5 | §6.3 `files/mkdir`, `files/create` | Body hanya `{path}` penuh | Juga menerima `{path: induk, name}`; respons `{item, warnings}`; `content` ≤ 512 KB menang atas template | PROMPT memakai bentuk `{path,name}`; `warnings` §5.2 harus sampai ke UI (AC `con`). SPEC diperbarui (penambahan). |
| 2026-10-14 | 5 | §5.3 | — | `Jail.CountEntries(p, max)` (getdents saja) | Dibutuhkan `itemCount` tanpa statx per entri. SPEC diperbarui. |
| 2026-10-14 | 5 | §8.3 | Cache 20 MB (PROMPT) vs 64 MB (SPEC) | 64 MB default, bisa diatur `listing.Options.CacheBytes` | SPEC dipakai; catatan implementasi (cache jail, cursor, `.system` → 404, `share=` ditolak) ditambahkan ke §8.3. |

## Catatan lintas step

(Temuan review minor yang ditunda, keputusan sementara, hasil pengukuran yang perlu diingat step berikutnya. Kosong sampai Step 1 dimulai.)

| Tanggal | Asal | Catatan | Status |
| --- | --- | --- | --- |
| 2026-10-14 | Step 1 | `.env` di server memakai komentar di belakang nilai (`APP_SECRET=   # …`, `ADMIN_INITIAL_PASSWORD=…   # sekali pakai`). Parser `--env-file` aplikasi menghapusnya, tetapi **systemd `EnvironmentFile=` tidak** → nilai akan ikut komentar. `install.sh` (Step 22) wajib menulis `.env` bersih tanpa komentar inline; sampai saat itu jangan memakai `.env` ini langsung di systemd. | Terbuka → Step 22 |
| 2026-10-14 | Step 1 | Smoke test di server butuh `ALLOW_ROOT=true` (binary dijalankan sebagai root). Setelah Step 22, service berjalan sebagai `filemanager` tanpa flag itu. | Info |
| 2026-10-14 | Step 1 | CI: `actionlint`/`redocly` tidak dipasang di server (hemat resource); `make openapi-lint` memakai pemeriksaan struktur YAML; lint penuh openapi berjalan di job CI (`npx @redocly/cli`). Hosting git belum ada → workflow belum pernah dieksekusi. | Info |
| 2026-10-14 | Step 2 | `sqlc` 1.31.1 dipasang dari rilis GitHub tanpa file checksum (rilis tidak menyediakannya); `goose` v3.29.0, `modernc.org/sqlite` v1.60.1, `google/uuid` v1.6.0 = stable terbaru saat Step 2 (cek `go list -m -versions`). Kompilasi pertama modernc ±1 menit di server. | Info |
| 2026-10-14 | Pra-Step 1 | Keputusan pemilik: kuota default 10 GB (storage disk terpisah 492 GB), Reverse Proxy aaPanel, origin konten sama, repo lokal + build di server, tanpa rar/unrar, 7z password via spike stdin, `ADMIN_CAN_BROWSE_USERS=true`. Toolchain: Go 1.27.2, pnpm 12.11.2. | Diterapkan di PRD §11.3, SPEC, `.env` |
| 2026-10-14 | Step 3 | `backend/go.mod` berisi `go 1.26.0` (ditulis `go mod tidy` di step sebelumnya), SPEC §2/Step 1 menyebut `go 1.25`. Tidak diubah di Step 3; tetapkan di Step 4 atau R-A (1.26.0 aman: toolchain server 1.27.2). | Terbuka → R-A |
| 2026-10-14 | Step 3 | `govulncheck`: Vulnerability #1: GO-2026-5932;   Module: golang.org/x/crypto;     Found in: golang.org/x/crypto@v0.58.0;     Fixed in: N/A — modul yang dibutuhkan, tidak dipanggil kode kita (informasional). | Info → Step 20 |
| 2026-10-14 | Step 4 | `go test -race` untuk fsjail ≈ 2 s, TOCTOU ×20 ≈ 10 s di 1 vCPU; jalankan serial (`GOMAXPROCS=1`). `errcheck` dikecualikan untuk `_test.go` (bersama forbidigo/gosec/noctx). `golang.org/x/text` v0.43.0 dan `x/sys` v0.49.0 = latest registry saat Step 4. | Info |
| 2026-10-14 | Step 3 | Uji manual dengan cookie `Secure` lewat HTTP loopback: curl tidak mengirim cookie Secure ke `http://` dari cookie jar → kirim lewat header `Cookie:` manual (atau `APP_ENV=development`). | Info |
| 2026-10-14 | Step 3 | Provisioning memakai `os.OpenRoot` langsung di `internal/auth/provision.go` (sesuai Step 3 tugas 8); Step 4 wajib mengganti dengan `fsjail` dan menambahkan `os.OpenRoot` ke pola forbidigo. | Terbuka → Step 4 |
| 2026-10-14 | Step 5 | Cache handle jail per user (`httpapi/jails.go`) menutup handle idle > 5 menit hanya jika refcount 0; task background (Step 11+) harus membuka jail sendiri, jangan meminjam dari cache. Listing dingin 10k ≈ 200–480 ms di 1 vCPU; separuh waktu di statx per entri — jika Step 21 butuh lebih cepat, kandidat: kunci kolasi di-cache per nama, `GOGC` lebih longgar saat build listing. `share=`/`as_user=` saat ini ditolak 400 `not_implemented` (18b/19a mengganti). | Info |
