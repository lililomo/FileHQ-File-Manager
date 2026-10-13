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

**Step 1 selesai (14 Oktober 2026).** Monorepo, config loader, logging, `/healthz` `/readyz`, webassets embed, scaffold web, Makefile, CI, openapi/SECURITY kerangka. `make lint`, `make test`, `make build` hijau di server. Step berikutnya: **Step 2** (DB, migrasi goose, sqlc).

Step yang sedang berjalan: — · Langkah terakhir yang selesai di dalamnya: — · Perintah terakhir yang gagal (jika ada): —

Toolchain server (dipasang pra-Step 1 / Step 1): Go 1.27.2 di `/usr/local/go/bin` (**PATH shell non-login tidak memuatnya** → `export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin`, Makefile sudah menambahkannya), pnpm 12.11.2 via corepack, golangci-lint 2.14.0 (binary prebuilt di `/usr/local/bin`), govulncheck di `~/go/bin`. Swap 2,8 GB sudah ada.

## Checklist step

### Milestone M1 — Fondasi
- [x] Step 1 — Scaffolding monorepo, Makefile, lint, CI, config loader, logging, `/healthz` `/readyz`
  - Selesai: 2026-10-14 · Commit: lihat `git log --oneline -2` (`feat(scaffold): …` + `docs(progress): …`) · Catatan: `backend/` (module `kppbwi.app/filemanager`, chi v5): `internal/config` (52 env SPEC §3, parser ukuran/durasi/bool/list, dotenv tanpa expand, validasi fail-fast exit 2, subfolder DATA_DIR 0750, deteksi cross-FS via device id, `check-deps`), `internal/httpapi` (TrustedPeer → RequestID → RealIP → AccessLog → Recoverer → SecurityHeaders → Compress; `/api/v1` dengan Timeout 60 s + MaxBytes 1 MB; envelope error + 36 kode; `/healthz` `/readyz`), `internal/webassets` (embed `all:dist`, SPA fallback, cache immutable untuk `/assets/`, dotfile 404), `cmd/filemanager` (`serve|version|check-deps|migrate`, `--env-file`, umask 0027, graceful shutdown). `web/`: Vite 8 + React 19 + TS 6.0.3 (lihat deviasi) + ESLint 10 + Prettier + Vitest 5. Coverage: config 82,9 %, httpapi 93,4 %. Ukuran perubahan ≈ 2.400 baris non-generated (Go ≈ 1.900 termasuk ≈ 700 baris test, web ≈ 150, YAML/Makefile ≈ 380) — melebihi target ±1.500 karena 13 tugas Step 1 memang luas; tidak dipecah. Verifikasi nyata: `make lint` 0 issue; `make test` lulus (Go + Vitest); `make build` → `bin/filemanager` statically linked 8,4 MB; curl `/healthz` 200, `/readyz` 200, `/tidak-ada` 200 SPA `no-store`, `/api/v1/x` 404 `NOT_FOUND` + requestId, `/.env` 404, `/assets/*` immutable; tanpa `APP_SECRET` exit 2; `LISTEN_ADDR=0.0.0.0` exit 2 (pesan loopback); `check-deps` tabel semua tool TIDAK ADA/NONAKTIF; `govulncheck` bersih; `ci.yml` valid secara struktur (actionlint tidak dipasang). **Untuk Step 2:** tabel `users/sessions/settings/user_preferences/audit_logs` + goose embedded + sqlc (`modernc.org/sqlite`, cek versi di registry); `readyz` menerima `ReadyChecker` tambahan (`httpapi.Options.ReadyChecks`) → tambahkan cek DB; subcommand `migrate` masih placeholder (exit 1); `config.Load` belum membuka DB (SPEC §3.4 butir 3/8/9 dikerjakan Step 2–3); `internal/db` sudah dikecualikan dari `forbidigo`; jalankan Go dengan `GOMAXPROCS=1` di server.
- [ ] Step 2 — DB: migrasi goose, sqlc, tabel inti
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 3 — Auth: seed admin, login/logout, sesi, CSRF, rate limit + lockout, wajib ganti password, provisioning folder
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 4 — `fsjail` + test keamanan path dan symlink
  - Selesai: — · Commit: — · Catatan: —
- [ ] Step 5 — API listing (sort server, cursor, metadata), mkdir, create file, stat
  - Selesai: — · Commit: — · Catatan: —
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

## Catatan lintas step

(Temuan review minor yang ditunda, keputusan sementara, hasil pengukuran yang perlu diingat step berikutnya. Kosong sampai Step 1 dimulai.)

| Tanggal | Asal | Catatan | Status |
| --- | --- | --- | --- |
| 2026-10-14 | Step 1 | `.env` di server memakai komentar di belakang nilai (`APP_SECRET=   # …`, `ADMIN_INITIAL_PASSWORD=…   # sekali pakai`). Parser `--env-file` aplikasi menghapusnya, tetapi **systemd `EnvironmentFile=` tidak** → nilai akan ikut komentar. `install.sh` (Step 22) wajib menulis `.env` bersih tanpa komentar inline; sampai saat itu jangan memakai `.env` ini langsung di systemd. | Terbuka → Step 22 |
| 2026-10-14 | Step 1 | Smoke test di server butuh `ALLOW_ROOT=true` (binary dijalankan sebagai root). Setelah Step 22, service berjalan sebagai `filemanager` tanpa flag itu. | Info |
| 2026-10-14 | Step 1 | CI: `actionlint`/`redocly` tidak dipasang di server (hemat resource); `make openapi-lint` memakai pemeriksaan struktur YAML; lint penuh openapi berjalan di job CI (`npx @redocly/cli`). Hosting git belum ada → workflow belum pernah dieksekusi. | Info |
| 2026-10-14 | Pra-Step 1 | Keputusan pemilik: kuota default 10 GB (storage disk terpisah 492 GB), Reverse Proxy aaPanel, origin konten sama, repo lokal + build di server, tanpa rar/unrar, 7z password via spike stdin, `ADMIN_CAN_BROWSE_USERS=true`. Toolchain: Go 1.27.2, pnpm 12.11.2. | Diterapkan di PRD §11.3, SPEC, `.env` |
