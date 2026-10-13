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

Dokumen perencanaan (PRD/SPEC/PROMPT) dibuat 14 Oktober 2026 dan **disetujui pemilik pada hari yang sama** (jawaban Q1–Q7 di PRD §11.3). Pemilik meminta semua step dijalankan berurutan. Prasyarat toolchain sudah dipasang di server: Go 1.27.2 (`/usr/local/go/bin`, `/etc/profile.d/go.sh`), pnpm 12.11.2 (corepack); swap 2,8 GB sudah ada. Belum ada step yang dikerjakan.

Step yang sedang berjalan: — · Langkah terakhir yang selesai di dalamnya: — · Perintah terakhir yang gagal (jika ada): —

## Checklist step

### Milestone M1 — Fondasi
- [ ] Step 1 — Scaffolding monorepo, Makefile, lint, CI, config loader, logging, `/healthz` `/readyz`
  - Selesai: — · Commit: — · Catatan: —
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
| — | — | — | — | — | — |

## Catatan lintas step

(Temuan review minor yang ditunda, keputusan sementara, hasil pengukuran yang perlu diingat step berikutnya. Kosong sampai Step 1 dimulai.)

| Tanggal | Asal | Catatan | Status |
| --- | --- | --- | --- |
| 2026-10-14 | Pra-Step 1 | Keputusan pemilik: kuota default 10 GB (storage disk terpisah 492 GB), Reverse Proxy aaPanel, origin konten sama, repo lokal + build di server, tanpa rar/unrar, 7z password via spike stdin, `ADMIN_CAN_BROWSE_USERS=true`. Toolchain: Go 1.27.2, pnpm 12.11.2. | Diterapkan di PRD §11.3, SPEC, `.env` |
