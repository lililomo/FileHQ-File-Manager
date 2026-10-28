# Changelog — FileManager (file.kppbwi.app)

Format mengikuti [Keep a Changelog](https://keepachangelog.com/id/1.1.0/); versi mengikuti SemVer. Nomor step merujuk ke `PROMPT.md`; hash = commit di branch `main`.

## [Unreleased]

### Added (Step 23c — Duplicate finder)
- Tab "Duplikat" di `/storage` (deep link `?tab=duplicates`): pilih folder, "Cari duplikat" berjalan sebagai task `duplicate_finder` (ukuran → SHA-256 64 KiB awal+akhir → SHA-256 penuh; symlink, berkas kosong, nama staging dilewati, hard link dihitung sekali; batas 1 jam, bisa dibatalkan), hasil disimpan 24 jam.
- Per grup: pilih salinan yang dipertahankan (default terlama/path terpendek), hapus sisanya ke Recycle Bin (bisa diurungkan) atau "Ganti dengan symlink" (server memeriksa ulang hash; undo memulihkan file asli); peringatan untuk berkas di item yang dibagikan.
- Endpoint `GET|POST /me/duplicates`, `POST /me/duplicates/symlink` (pemilik saja, di openapi dan suite IDOR).

### Fixed (Step 23c)
- Tombol Hapus di tabel "50 file terbesar" `/storage` tidak lagi menggantung saat konfirmasi hapus aktif (dialog konfirmasi kini dipasang di halaman itu).
- E2E: `loginAsAdmin` hanya beralih ke password awal bila alert berisi "Username atau password salah" (flake `responsive.spec`).

### Added (Step 23b — Dual-pane)
- Mode dual-pane di file browser (≥ 1280 px): dua panel berdampingan; panel kiri tetap mengikuti URL, panel kanan punya tab (≤ 10), riwayat, urutan, filter dan seleksi sendiri; satu panel fokus menerima toolbar, shortcut, palette dan panel preview. Toggle lewat View ▾ → "Dual-pane", palette, atau `F6`; di bawah 1280 px otomatis kembali ke satu panel.
- Aksi "Salin ke panel lain" / "Pindahkan ke panel lain" (menu konteks, toolbar ⋯, palette) memakai copy/move biasa: dialog konflik, task untuk batch besar, toast "Urungkan".
- Drag & drop antar panel: area listing (termasuk folder kosong) dan address bar panel menerima drop ke folder aktifnya, dengan badge "Pindahkan/Salin ke …" (`Ctrl` = salin); drop ke folder yang sama tidak melakukan apa-apa.
- Preferensi baru `dualpane.enabled` dan `dualpane.right` (divalidasi server); `Tab` berpindah antar grid, setiap panel adalah region berlabel.

### Added (Step 23a — TOTP 2FA)
- Verifikasi dua langkah TOTP opsional per user (RFC 6238, SHA-1, 6 digit, 30 s, window ±1, anti-replay `totp_last_step`): `POST /me/totp/setup|enable|disable|recovery-codes`, langkah kedua inline pada `POST /auth/login` (`totp`/`recoveryCode`; `VALIDATION_FAILED totp_required|totp_invalid`), 10 kode pemulihan sekali pakai (HMAC-SHA256 kunci HKDF `totp-recovery`), secret at rest AES-256-GCM (kunci HKDF `totp-enc`, AAD id user), limiter `RATE_LIMIT_TOTP` 5/menit per user, audit `auth.totp_enabled|disabled|recovery_used|recovery_regenerated`, `user.reset_totp`.
- UI: kartu 2FA di Profil (QR `otpauth://` + secret manual, verifikasi kode, kode pemulihan dengan salin/unduh dan kotak centang wajib, nonaktifkan dengan password + kode, buat kode pemulihan baru); langkah kode pada halaman login (tautan kode pemulihan, peringatan kode terakhir); admin: badge 2FA + aksi "Reset 2FA" (sesi user dicabut).
- Migrasi `0016_totp` (`users.totp_enabled_at`, `users.totp_last_step`, tabel `totp_recovery_codes`), env opsional `RATE_LIMIT_TOTP`, `GET /auth/me` + login mengembalikan `user.totpEnabled`/`totpEnabledAt`, daftar admin sudah memuat `totpEnabled`.

### Fixed
- Login mencatat sukses (`last_login_at`, reset counter) hanya setelah provisioning folder home berhasil (review A m-08).
- Batas 20 sesi per user mencabut sesi yang paling lama **dibuat** (`created_at`, SPEC §10.4), bukan yang paling lama tidak dipakai (review A m-09).

## [1.0.0] — 2026-10-28

Rilis MVP (PRD §9: M1–M5). Tag `v1.0.0` dibuat setelah persetujuan pemilik (lihat `docs/reviews/review-F.md`).

### Fondasi (M1 — Step 1–8)
- Scaffolding monorepo Go (`backend/`) + React/Vite (`web/`), config fail-fast, log JSON `slog`, `/healthz` `/readyz`, CI GitHub Actions (`e357924`).
- SQLite WAL via `modernc.org/sqlite` + goose + sqlc, repositori inti (`09c4e76`).
- Auth: seed admin dari `.env`, sesi server-side (cookie `fm_session` HttpOnly/Secure/Lax), CSRF HMAC + Fetch Metadata, argon2id + daftar 100k password umum, lockout bertahap + limit IP, wajib ganti password, provisioning folder home saat login pertama (`1341a58`).
- `internal/fsjail` di atas `os.Root` (openat2 `RESOLVE_BENEATH`): validasi path/nama, kebijakan symlink, lock manager per path, test keamanan TOCTOU (`c7d2b53`).
- Listing: natural sort di server, cursor opaque bertanda tangan, cache LRU 15 s, `stat`/`mkdir`/`create` (`6d72ef0`).
- Frontend: design token (terang/gelap/sistem tanpa flash), UI kit, i18n id/en, router, API client, halaman login & ganti password (`d2c6105`); layout browser, breadcrumb/address bar, Details view tervirtualisasi (`121351a`); kolom, sort (+sekunder), seleksi, keyboard, tab folder, panel preview (`b4d6522`); List/Tiles/Large/Extra large + pipeline thumbnail (vips/ffmpeg/pdftoppm) + cache metadata media (`438158d`).
- Review A (`9fdcf06`, perbaikan `3fa168d`): header cache/CORP API, ekstensi diblokir, aturan nama path penuh.

### Operasi file & transfer (M2 — Step 9a–13)
- Operasi file (rename/move/copy/delete/duplicate/symlink/chmod/batch rename/undo 10 s) dengan dialog konflik, pembaruan referensi metadata, lock path (`e22d8e3`, `8f4a636`).
- Recycle Bin per user (restore/purge/auto-purge), kuota inkremental + rekalkulasi, ukuran folder (`a7f8129`); favorit, pin, catatan, tag warna, Recent, Aktivitas, analisis penyimpanan (`840a485`).
- Review B (`8bfc851`, perbaikan `33c7555`): kuota pada `files/create`, ekstensi diblokir di batch rename, perbaikan axe.
- Task engine persisten (cancel/retry), SSE realtime, panel task, notifikasi in-app (`df76840`).
- Upload tus resumable (pause/resume/reload, folder, paste clipboard, precheck kuota), panel transfer (`88218c0`).
- Download HTTP Range, zip streaming tanpa file sementara, signed URL HMAC dengan kebijakan konten inline (ADR-06) (`c9bb687`).
- Remote download sebagai task dengan SSRF guard (DNS pin, redirect per hop, allow/denylist) (`5b801f4`).

### Arsip, search, editor, viewer (M3 — Step 14a–17b)
- Compress/extract zip (termasuk AES), 7z, rar (baca), tar/gz/bz2/xz/zst dengan proteksi zip-slip/bomb, password 7-Zip lewat stdin (`e9da365`); archive browser: lihat isi tanpa extract, extract sebagian, preview entri, tambah/hapus entri zip (`3b84792`).
- Review C (`fa6e24f`, perbaikan `2833029`): finalisasi upload saat retry, penanganan password 7z, redaksi path host 7-Zip.
- Search nama + isi (ripgrep, RE2), replace in files dengan preview dan rollback versi (`07fa750`, `c35c34a`).
- Editor Monaco multi-tab: encoding/EOL/deteksi bahasa, simpan atomik + versi pre-image, konflik ETag + diff, riwayat versi, find in files, format, preview Markdown, draft lokal (`c717cf6`, `1108e4d`, `3e0ca49`, `5f6252e`).
- Viewer: gambar (EXIF, preview shrink-only, slideshow), PDF (pdf.js, Range, outline, cari, password, cetak), audio + mini-player persisten, video (Video.js 10, subtitle, PiP, resume), teks/Markdown/CSV/JSON, Quick Look (`9712223` … `c2a6436`, `079f208`, `a8b14d6`).
- Review D (`524a741`, perbaikan `b59e342`, `acad6b7`): deteksi password 7z deterministik, pembersihan sesi editor, hardening viewer.

### Sharing & admin (M4 — Step 18a–19b)
- Link publik: token 256-bit (DB hash + AES-GCM), slug, password, kedaluwarsa, batas unduh, file request, halaman tamu `/s/<token>`, QR (`442bfba`, `c4c92d8`).
- Share ke user (viewer/editor) lewat `fsjail.SubRoot`, "Dibagikan ke saya", "Share saya" + log akses (`9572dbf`, `585416a`).
- Admin: dashboard, manajemen user (admin terakhir dilindungi), settings live, branding, browse-as-user teraudit (`9854982`, `7181f30`); audit log + export CSV, task & share global, maintenance (backup SQLite `VACUUM INTO`, integrity check, purge), profil + sesi + preferensi (`7795985`, `5be2669`).
- Review E (`5b3a664`, perbaikan `34c47e0`): batas unduh vs Range, redaksi token di access log, kebocoran path pemilik di jalur upload share.

### Hardening, performa, deployment (M5 — Step 20–22, R-F)
- CSP final tanpa `unsafe-inline`/`unsafe-eval` script, header keamanan lengkap, rate limit terpusat dengan header `RateLimit-*`, `/metrics` loopback (`dc70ca5`); suite IDOR atas semua route, test rahasia-di-log, korpus fuzz, audit dependensi di CI (`e6ff855`); `docs/SECURITY.md` + ASVS L2 (`ab376e2`).
- Performa: listing 10.500 entri dingin p95 132–223 ms, hangat ≤ 22 ms, idle RSS ≈ 27 MB, gzip middleware, harness `make perf` (`7867920`); render 10k < 300 ms, long task ≤ 100 ms, anggaran bundle < 300 KB gzip (JS awal 220,7 KB) (`8a02e65`); WCAG 2.2 AA (axe 0 serius di 11 layar), responsif 375/820/1440 (`399892a`, `4f0b617`, `bc611da`).
- Deployment: `deploy/install.sh` idempoten, unit systemd hardened (`systemd-analyze security` 1.4), `deploy/update.sh` dengan backup + rollback otomatis (`8b485dd`); snippet Nginx aaPanel (Reverse Proxy), Docker opsional, CI deploy, `docs/DEPLOY-AAPANEL.md` (`3d2511b`, `5032d9c`); real IP Cloudflare + smoke test nyata di server (`b940f98`).
- Review F (`docs/reviews/review-F.md`): limiter thumbnail dikunci pada scope terverifikasi (M-02), aturan admin terakhir di dalam transaksi (M-03), `pnpm audit` bersih lewat override dompurify 3.4.17 + jsdom 30.1.3 (M-01), audit `admin.browse_user` memuat path body, nama staging tidak bocor ke listing saat file tersembunyi ditampilkan; checklist rilis v1.0 diverifikasi di produksi.

### Catatan rilis
- Lingkungan target: Ubuntu 24.04 + aaPanel 8.0.7 (Reverse Proxy), Go 1.27.3 (`go.mod` `go 1.26.0`), Node ≥ 22, DNS proxied Cloudflare.
- Fitur opsional (M6, Step 23a–23e: TOTP 2FA, dual-pane, duplicate finder, HLS, origin konten terpisah) belum termasuk.
