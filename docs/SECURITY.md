# SECURITY.md — File Manager (file.kppbwi.app)

Status: **final Step 20 (hardening), 22 Oktober 2026**; diverifikasi ulang di R-F. Sumber kebenaran untuk kontrol
keamanan adalah `SPEC.md §10`; dokumen ini merangkum apa yang **sudah diimplementasikan dan diuji** di kode, hasil
review OWASP ASVS, batasan yang diketahui, dan cara melapor.

## 1. Melaporkan masalah keamanan

Laporkan ke admin aplikasi (pemilik KPP BWI) secara privat, bukan lewat issue publik. Sertakan langkah
reproduksi, dampak, dan versi (`GET /api/v1/version`). Jangan menguji terhadap data user lain tanpa izin.
Balasan awal ≤ 3 hari kerja; perbaikan untuk temuan major/blocker dirilis lewat `deploy/update.sh` dengan
backup otomatis (`filemanager backup-db --label pre-update`).

## 2. Threat model (STRIDE, sinkron dengan SPEC §10.1)

Aset: file user di `/www/storage/FileManager/<username>`, kredensial & sesi, `APP_SECRET` (turunan HKDF untuk
signed URL, cursor, cookie share, token share), link share, DB SQLite (`.system/db`, backup). Batas kepercayaan:
browser ↔ Nginx aaPanel ↔ aplikasi Go (loopback `127.0.0.1:8080`) ↔ filesystem/CLI. Penyerang: user terdaftar
jahat (lintas user, penerima share), tamu dengan link publik, pihak luar lewat remote download (SSRF), isi arsip
berbahaya (zip-slip/bomb), konten user (XSS), user Linux lain di server (`ps`, `/proc`), supply chain.

| STRIDE | Ancaman utama | Kontrol | Verifikasi |
| --- | --- | --- | --- |
| Spoofing | tebak password, curi/pakai sesi, tebak token share | argon2id (64 MiB) + daftar 100k; lockout bertahap + limit IP (bertahan restart lewat `login_attempts`); cookie `HttpOnly Secure SameSite=Lax` host-only; token sesi/share 256-bit, DB hanya hash; HSTS; `ACCOUNT_DISABLED` hanya setelah password benar (anti enumerasi) | `auth/*_test.go`, `httpapi/auth_test.go`, `ratelimit_test.go`, `share_test.go` |
| Tampering | traversal/symlink/TOCTOU/zip-slip, CSRF, cursor/undo dimanipulasi, IDOR metadata | `fsjail` di atas `os.Root` untuk **semua** akses (forbidigo), ekstraksi di `tmp` + validasi entri, CSRF HMAC + `Sec-Fetch-Site`/`Origin` (host-only), cursor/undo HMAC, `WHERE user_id=?` + sub-root share | `fsjail/*_test.go` ≥ 90 %, `archive/safe_test.go`, `idor_test.go`, `scope_test.go` |
| Repudiation | admin/pengguna menyangkal | audit append-only (hanya retensi), `X-Request-ID` di setiap respons & log, IP /24 | `admin/audit_test.go`, `secrets_test.go` |
| Information disclosure | path absolut, stack trace, enumerasi user, rahasia di log/argv, GPS EXIF, token di log, DB backup | `fsjail.Error` tanpa path host, 500 generik, login generik + argon2 dummy, suggest ≥ 2 char, redaksi log + `LogPath`, GPS opt-in, password CLI via stdin, `ADMIN_CAN_BROWSE_USERS` + audit, backup hanya admin + limiter + audit | `secrets_test.go`, `review_e_test.go`, `media_info_test.go`, `archive/password_test.go` |
| XSS/konten user | HTML/SVG/Markdown/subtitle/nama file | konten aktif selalu `attachment`+`octet-stream`+`sandbox`; CSP final tanpa `unsafe-inline` script; DOMPurify profil ketat; nama file sebagai text node; `nosniff` | `download/policy_test.go`, E2E `review-d.spec.ts`, `security.spec.ts` |
| DoS | zip bomb, folder raksasa, regex, upload tak terbatas, SSE, thumbnail CPU, body besar | limit entri/ukuran/rasio; paging + cache listing; RE2 + timeout + 1 pencarian aktif; `MAX_UPLOAD_SIZE` + kuota; maks 5 SSE; worker pool `nice`; `MaxBytes` 1 MB; **rate limit terpusat §10.5** | `archive/limits_test.go`, `search/*_test.go`, `ratelimit_test.go`, `events_test` |
| Elevation | user → admin, viewer → editor, admin ke folder user, shell via nama file, setuid dari arsip, eksekusi upload | `RequireAdmin` (daftar route ditegakkan test), permission dicek per request di server, `as_user` + audit, argv tanpa shell + `--`, mode bit dibuang, storage di luar web root, unit systemd `User=filemanager` + `NoNewPrivileges`/`ProtectSystem=strict`/`CapabilityBoundingSet=` kosong (`deploy/filemanager.service`) | `idor_test.go`, `scope_test.go`, `admin_test.go`, `archive/safe_test.go` |
| SSRF | remote download ke jaringan internal | resolve + denylist + pin IP + redirect per hop | `netguard_test.go` |
| Supply chain | dependensi rentan | `govulncheck` (gagal pada kerentanan yang dicapai kode), `pnpm audit --audit-level=high`, lockfile, toolchain dipin (`toolchain go1.27.3`), tanpa CDN | CI job `audit`, `make audit` |

## 3. Kontrol yang ada per step

| Step | Kontrol | Lokasi | Test |
| --- | --- | --- | --- |
| 1 | Konfigurasi fail-fast: `APP_SECRET` wajib 64 hex, `LISTEN_ADDR` loopback di production, tolak root di production (`ALLOW_ROOT` hanya uji lokal), umask 0027 | `internal/config` | `config_test.go` |
| 1 | Rahasia tidak pernah masuk log (redaksi berdasarkan nama key + `LogValuer`) | `internal/config/redact.go` | `redact_test.go` |
| 1 | `X-Forwarded-For` dan `X-Request-ID` hanya dipercaya dari `TRUSTED_PROXIES` | `internal/httpapi/middleware.go` | `middleware_test.go` |
| 1 | Panic tidak membocorkan detail ke client (500 `INTERNAL` + log) | `Recoverer` | `middleware_test.go`, `secrets_test.go` |
| 1 | Body JSON ≤ 1 MB (`PAYLOAD_TOO_LARGE`), timeout handler 60 s | `MaxBytes`, `middleware.Timeout` | `middleware_test.go` |
| 1 | Aset statis: dotfile tidak pernah disajikan; `index.html` `no-store` | `internal/webassets` | E2E |
| 1 | Lint `forbidigo`: `os.*` filesystem dan `exec.Command` dilarang di luar package yang diizinkan | `backend/.golangci.yml` | CI |
| 3 | Password argon2id, kebijakan password + daftar 100k umum, sesi server-side (`fm_session` HttpOnly/Secure/Lax, idle 2 h, absolut 168 h), CSRF HMAC + `Sec-Fetch-Site`/`Origin`, lockout bertahap + rate limit IP, seed admin sekali, `must_change_password` | `internal/auth`, `internal/httpapi` | `auth/*_test.go`, `httpapi/auth_test.go` |
| 4 | **Path & symlink**: semua akses file user lewat `internal/fsjail` di atas `*os.Root` (openat2 `RESOLVE_BENEATH`): `Clean` (NFC, tolak `..`/`.`/NUL/kontrol, batas 255/4096/64, `.system` di root), symlink keluar root → `SYMLINK_ESCAPE`, `Create` `O_NOFOLLOW`, `Chmod` `fchmodat2(AT_SYMLINK_NOFOLLOW)` + setuid/setgid/sticky dibuang, `RemoveAll`/`WalkDir`/`DirSize` tidak mengikuti symlink, `AbsForCLI` menolak komponen symlink, `SubRoot` mengurung penerima share, `MoveOut/MoveIn` via `renameat2(RENAME_NOREPLACE)`; toolchain ≥ go1.25.12 dicek test | `internal/fsjail` | `jail_test.go` (TOCTOU `-race -count=20`), `coverage_test.go` ≥ 90 %, `FuzzNormalize` |
| 4 | Lock per path sadar-ancestor per user → `409 OPERATION_IN_PROGRESS` | `internal/fsjail/lock.go` | `lock_test.go` |
| 8/12b | **Signed URL**: HMAC-SHA256 HKDF(`APP_SECRET`,"signed-url") atas `v1\|scope\|path\|purpose\|exp\|extra`, purpose terikat endpoint, TTL `download\|inline\|zip` 5 menit / `media\|thumb` 2 jam; bearer (tanpa cookie); akun nonaktif → `403 account_inactive` di semua route bertanda tangan (Step 20 menyamakan thumb yang semula 404); log hanya path tanpa query | `internal/download/signer.go`, `httpapi/download.go` | `signer_test.go`, `download_test.go`, `thumb_test.go` |
| 12b | **Kebijakan konten inline (ADR-06)**: MIME dari magic bytes; HTML/XHTML/SVG/XML/JS/CSS/SWF selalu `attachment` + `octet-stream`; semua respons konten `nosniff`, `CSP: sandbox; default-src 'none'`, `Referrer-Policy: no-referrer`, CORP `same-origin`, `filename*` tersanitasi | `internal/download/policy.go`, `serve.go` | `policy_test.go`, `download_test.go` |
| 12b | **Zip streaming** tanpa file sementara, payload id acak, symlink dilewati, path host tidak bocor, 3 download paralel/user | `internal/download/zipstream.go` | `zipstream_test.go`, `download_test.go` |
| 13 | **SSRF guard**: skema/userinfo/port/host, semua jawaban DNS vs 22 prefix terlarang, pin IP di `DialContext` + `Control` (tanpa jendela rebinding), redirect ≤ 5 hop divalidasi, tanpa proxy env, timeout, allow/denylist admin, batas ukuran, nama tersanitasi, log tanpa query | `internal/netguard`, `internal/remotedl` | `netguard_test.go`, `remotedl/runner_test.go`, `httpapi/remotedl_test.go` |
| 14a | **Arsip**: deteksi magic bytes; entri dinormalisasi + divalidasi per segmen (`ARCHIVE_UNSAFE` untuk absolut/`..`/drive/NUL; device/FIFO/hardlink/symlink keluar dilewati); ekstraksi ke `DATA_DIR/tmp/ar-<task>/root` di bawah `fsjail` dengan mode bit dibuang; batas entri/ukuran/rasio diukur saat streaming; **password 7-Zip hanya lewat stdin**; CLI via `internal/exec` (argv, `--`, cwd tmp, timeout, nice) | `internal/archive` | `safe_test.go`, `limits_test.go`, `password_test.go` (`/proc/*/cmdline`), `FuzzEntryName` |
| 14b | **Archive browser**: indeks tanpa ekstraksi, password hanya via header → sesi memori 16 byte terikat user+path (TTL 10 menit), `entry-content` memakai kebijakan inline ADR-06, `POST /archives/sign` dibatasi 120/menit/sesi (Step 20) | `internal/archive/{index,session,browse}.go`, `httpapi/archives_browser.go` | `archives_browser_test.go`, `ratelimit_test.go` |
| 15 | **Search & replace**: pola RE2 dulu (ReDoS mustahil), ripgrep argv (`-e`, `--`, `--no-follow`, cwd user), `.system`/staging tidak dicari, limit 30/10 per menit + 1 aktif, replace atomik dengan sha256 + pre-image versi | `internal/search`, `internal/versions` | `search/*_test.go`, `review_d_test.go` |
| R-D | 7z ber-password deterministik (CRC/`ReadError.Encrypted`), profil Markdown tanpa media/`usemap`, error boundary viewer, restore versi memeriksa kuota, sesi tab editor tidak memulihkan file hilang | `archive/entry.go`, `features/editor/markdown.ts` | `review_d_test.go`, E2E `review-d.spec.ts` |
| 18a | **Link publik**: token 256-bit (DB hash + AES-GCM), slug + daftar terlarang, password argon2id m=19 MiB, cookie unlock HMAC ber-`Path`, urutan cek disabled → pemilik nonaktif → kunci → broken → expired → password, tamu dikurung `SubRoot` read-only, `download_count` sekali per unduhan dan tidak bisa dilewati lewat `Range` setelah batas (R-E), preview-only → 403, file request = principal pemilik tanpa overwrite, `share_access_logs` IP /24, `/s/*` + API publik `no-referrer`, token diredaksi di access log aplikasi | `internal/share`, `httpapi/{shares,public_share,public_share_upload}.go` | `share_test.go`, `public_share_test.go`, `review_e_test.go`, E2E `share-public.spec.ts` |
| 18b | **Share ke user**: penerima bekerja di `fsjail.SubRoot` pemilik (viewer read-only di level jail), id bukan milik penerima → 404, viewer menulis → 403, `chmod/symlink/delete-permanent/undo/trash/shares` menolak scope, state tetap milik pemilik (`Jail.Abs/RelOf`), signed URL `r:<share>:<user>` diperiksa ulang tiap pemakaian, autocomplete hanya username+displayName ≥ 2 char 30/menit | `internal/share/recipients.go`, `httpapi/scope.go` | `scope_test.go` (matriks 5 aktor × 17 operasi) |
| 19a | **Admin**: `/api/v1/admin/*` di balik `OriginCheck` + sesi + CSRF + `RequirePasswordChanged` + `RequireAdmin` (403 server-side; daftar route ditegakkan test), username regex + daftar terlarang, password sementara `crypto/rand` hanya di respons `no-store`, admin terakhir/diri sendiri dilindungi (409), disable/reset/delete mencabut sesi dalam transaksi, hapus user lewat `fsjail.ArchiveHome/RemoveHome` + `db.PurgeUserData` (daftar tabel tetap), settings hanya kunci terdaftar + validasi + snapshot atomik, logo hanya PNG magic bytes ≤ 512 KB, `?as_user=` butuh flag + audit per request | `internal/admin`, `httpapi/{admin_users,admin_settings,scope}.go` | `admin/*_test.go`, `httpapi/admin_test.go`, E2E `users-admin.spec.ts` |
| 19b | **Audit & operasi admin**: cakupan audit §8.16 (login/gagal/lock, user.*, settings.*, share.*, delete permanen, admin.browse_user, maintenance.*, backup_download) tanpa rahasia (`IsSensitiveKey`), export CSV BOM + RFC 4180 + escape formula (`= + - @ TAB CR` → `'`) streaming ≤ 1 juta baris, share admin hanya metadata, maintenance job tetap dengan `409 already_running`, backup `VACUUM INTO` tmp+rename 0600 di `DATA_DIR/backups`, unduh backup hanya nama regex + audit; `me/sessions` hanya sesi sendiri (IDOR 404), sesi saat ini 409 | `internal/admin/{audit,maintenance,shares}.go`, `httpapi/admin_*.go`, `me_sessions.go` | `admin/{audit,maintenance}_test.go`, `admin_ops_test.go`, `me_sessions_test.go` |
| R-E | `max_downloads` vs `Range`, token link di access log (`LogPath`), path pemilik lewat jalur upload tamu/penerima, `symlinkTarget` di scope share, link publik untuk symlink ditolak, rename share file tunggal ditolak, karakter kontrol di list settings, kunci audit `generated`; fuzz path/token/slug/cookie | `httpapi/{public_share,middleware,public_share_upload}.go`, `upload/*`, `share/service.go` | `review_e_test.go`, E2E `review-e.spec.ts` |
| **20** | **Header & CSP final** (§10.3): `script-src 'self' 'wasm-unsafe-eval'` (tanpa nonce — tidak ada script inline; tanpa `unsafe-inline`/`unsafe-eval`), `style-src 'self' 'unsafe-inline'` (lihat §5), `img/media-src 'self' data: blob:`, `worker-src 'self' blob:`, `object-src 'none'`, `frame-src 'none'`, `frame-ancestors 'none'`, `base-uri 'self'`, `form-action 'self'`, `upgrade-insecure-requests` hanya pada respons HTTPS; API `default-src 'none'`; `Permissions-Policy`, COOP/CORP `same-origin`, `X-Robots-Tag`, HSTS 1 tahun saat HTTPS; tidak ada `Access-Control-Allow-Origin` (CORS mati); `/s/*` `Referrer-Policy: no-referrer` | `httpapi/middleware.go` | `middleware_test.go`, E2E `security.spec.ts` (konsol tanpa pelanggaran di browser/editor/viewer/admin/settings/publik; pdf/video/Monaco di `review-d.spec.ts`) |
| 20 | **Rate limit terpusat** (§10.5): `config.RateLimits` dari env `RATE_LIMIT_*` (19 kunci, §3.3) — login IP 20/1m (jendela geser, **bertahan restart** lewat `login_attempts`), ganti password 5/15m, share unlock 10/10m per link+IP & 30/10m per IP, publik 120/1m, token tak dikenal 30/1m, API umum 600/1m burst 120 per sesi (tus/thumb/raw/download/zip/entry-content/subtitle/publik/events dikecualikan — punya limit sendiri), `files/list` 240/1m, thumb 600/1m per scope, pencarian 30 & 10/1m, remote download 10/1h, suggest 30/1m, `archives/sign` & `files/sign` 120/1m, `auth/csrf` 60/1m, tus create 60/1m, `healthz` 60/1m per IP, unduh backup 5/10m; semua 429 memuat `Retry-After`, middleware terpusat menambah `RateLimit-Limit/Remaining/Reset/Policy` | `httpapi/ratelimit.go`, `config/ratelimit.go`, `auth/ratelimit.go`, `share/access.go` | `ratelimit_test.go` (tiap limit → 429 + `Retry-After`, isolasi per sesi/IP/scope, override env, `off`), `auth_test.go`, `public_share_test.go`, `search_test.go`, `remotedl_test.go` |
| 20 | **Suite IDOR terpusat**: `chi.Walk` atas semua route → setiap route diklasifikasi (public/signed/self/resource/admin); route baru tanpa klasifikasi menggagalkan test; user A dengan id nyata milik B (task, versi, share publik & user, notifikasi, sesi, upload tus, user) → 403/404/410, anonim → 401, admin → 403 untuk user biasa, signed tanpa tanda tangan → 403, token publik acak → non-2xx | `httpapi/idor_test.go` | `TestIDORSuite` (semua route `chi.Walk`) |
| 20 | **Rahasia & log**: alur login/share/signed URL/arsip di `LOG_LEVEL=debug` → tidak ada `APP_SECRET`, password, token mentah, `sig=`, cookie di log; `X-Request-ID` pada 404/401/400/403/halaman publik; panic → `INTERNAL` generik; **payload task `remote_download` diredaksi** (userinfo + query dibuang) di respons API; `ACCOUNT_DISABLED` hanya setelah password benar; `OriginCheck` membandingkan `scheme://host` (base URL ber-path tidak menolak semua non-GET) | `httpapi/secrets_test.go`, `httpapi/tasks.go`, `auth/service.go`, `httpapi/middleware_auth.go` | `secrets_test.go`, `auth_test.go`, `review_a_test.go` |
| 20 | **Fuzz** dengan korpus dikomit (`testdata/fuzz/*`): `FuzzNormalize` (fsjail), `FuzzEntryName` (archive), `FuzzCursor` (listing), `FuzzBatchRenameRule` (files), ditambah `FuzzParseTIFF/ExtractExif/SanitizeVTT` (media), `FuzzReviewESlug/Cookie` (share), `FuzzReviewEPublicPath/Token` (httpapi); `make fuzz` 30 s/target; CI job `fuzz` non-blocking | `internal/*/fuzz_test.go` | CI |
| 20 | **Dependensi**: `make audit` = `govulncheck ./...` (gagal pada kerentanan yang dicapai kode) + `pnpm audit --audit-level=high`; CI job `audit` memblokir; `toolchain go1.27.3` di `go.mod` (GO-2026-6809 `crypto/x509` di 1.27.2 — dicapai lewat `io.Copy`→TLS); `bodgit/sevenzip` v1.6.5; `/metrics` opsional hanya di `METRICS_ADDR` loopback tanpa label berisi path/user | `Makefile`, `.github/workflows/ci.yml`, `internal/metrics` | CI, `metrics_test.go` |
| R-F | Limiter thumbnail dikunci pada scope **terverifikasi** (tanda tangan salah/tanpa tanda tangan → IP pemanggil; sebelumnya siapa pun yang tahu id user bisa menghabiskan jatah thumbnail-nya); aturan admin terakhir diperiksa di dalam transaksi tulis (dua admin yang saling menurunkan bersamaan tidak lagi bisa meninggalkan nol admin); audit `admin.browse_user` memuat `paths` dari body JSON untuk tulis di bawah `?as_user=`; nama staging `.fm-*` tidak lagi bocor ke listing saat "tampilkan file tersembunyi" aktif; `pnpm audit` 0 advisori (override dompurify 3.4.17, jsdom 30.1.3); probe route `self` dengan id milik user lain (trash restore/purge, notifications read) | `httpapi/{ratelimit,scope,router}.go`, `admin/users.go`, `listing/service.go`, `web/pnpm-workspace.yaml` | `httpapi/review_f_test.go`, `admin/review_f_test.go`, `listing/review_f_test.go`, `ratelimit_test.go` |

## 4. Checklist OWASP ASVS 4.0 (Level 2, subset relevan)

| ASVS | Persyaratan | Status | Bukti |
| --- | --- | --- | --- |
| V2.1 | Password ≥ 12, ≤ 128, tanpa komposisi, cek daftar bocor | ada | `auth/password_test.go` |
| V2.2 | Anti-automation login (lockout + limit IP), pesan generik | ada | `auth_test.go`, `ratelimit_test.go` |
| V2.4 | Hash argon2id parameter kuat, rehash saat login | ada | `auth/password.go` |
| V2.8 | Password sementara sekali pakai, wajib ganti | ada | `admin/users_test.go` |
| V3.2–3.4 | Sesi server-side, id 256-bit, cookie HttpOnly/Secure/Lax, idle & absolut, revoke saat ganti password/role/disable | ada | `auth/session_test.go`, `me_sessions_test.go` |
| V3.5 | Token API (signed URL) terikat purpose/scope/TTL | ada | `signer_test.go` |
| V4.1 | Otorisasi di server untuk setiap resource; prinsip least privilege; IDOR | ada | `idor_test.go`, `scope_test.go` |
| V4.2 | Anti-CSRF (token + Fetch Metadata + Origin) | ada | `auth_test.go` CSRF |
| V4.3 | Fungsi admin terpisah (`RequireAdmin`), admin terakhir dilindungi, `as_user` diaudit | ada | `admin_test.go` |
| V5.1 | Validasi input (JSON `DisallowUnknownFields`, tipe query, path `fsjail.Clean`) | ada | `errors.go`, `fsjail` |
| V5.2 | Sanitasi Markdown/VTT/nama file; tidak ada `eval`/shell | ada | `markdown.ts`, `srt2vtt.go`, `internal/exec` |
| V5.3 | Output encoding (JSON escape HTML, `nosniff`, `Content-Type` eksplisit), CSV escape formula | ada | `errors.go`, `admin/audit.go` |
| V5.5 | Deserialisasi aman (JSON std, tanpa gob/XML user) | ada | — |
| V6.2 | Kriptografi: HKDF per tujuan, HMAC-SHA256, AES-GCM, `crypto/rand` | ada | `download/signer.go`, `share/token.go` |
| V7.1 | Log tanpa kredensial/token; request-id | ada | `secrets_test.go` |
| V7.3 | Log injection: JSON terstruktur (slog) | ada | `config/logger.go` |
| V7.4 | Error handling generik (500 `INTERNAL`) | ada | `middleware_test.go` |
| V8.1 | Data sensitif tidak di cache (`no-store` API, `private` konten) | ada | `middleware.go`, `serve.go` |
| V8.3 | Rahasia hanya di env; `.env` 0600 milik `filemanager`, tanpa komentar inline, password admin acak hanya ke tty | ada | `config`, `deploy/install.sh`, `deploy/test/install_test.sh` |
| V9.1 | TLS di proxy; HSTS; `upgrade-insecure-requests` | ada (aaPanel; smoke test di server menunggu persetujuan pemilik) | `middleware.go`, `docs/DEPLOY-AAPANEL.md` |
| V10.3 | Supply chain: audit otomatis, lockfile, toolchain dipin | ada | CI `audit` |
| V11.1 | Rate limit logika bisnis (unggah, share, remote download, task) | ada | `ratelimit_test.go`, `remotedl_test.go` |
| V12.1–12.3 | Upload: ukuran, ekstensi diblokir, nama tersanitasi, tidak dieksekusi (di luar web root), traversal | ada | `upload_test.go`, `archive/safe_test.go` |
| V12.5 | Konten aktif tidak disajikan inline | ada | `policy_test.go` |
| V12.6 | SSRF | ada | `netguard_test.go` |
| V13.1 | Header keamanan API, CORS mati | ada | `security.spec.ts` |
| V13.2 | Metode HTTP dibatasi (`405`), body dibatasi | ada | `router.go` |
| V14.2 | Dependensi tanpa kerentanan dikenal | ada | `make audit` |
| V14.3 | Tanpa debug di production (`APP_ENV`), `/readyz` & `/metrics` tidak di-proxy; source/binary/dotfile 404 di Nginx | ada | `config`, `deploy/nginx-aapanel.conf`, `deploy/test/nginx_snippet_test.sh` |
| V14.4 | `Content-Type` + charset, `X-Content-Type-Options`, `Referrer-Policy`, CSP, `Permissions-Policy` | ada | `middleware_test.go` |
| V14.5 | Validasi `Origin`/`Host` (OriginCheck host-only) | ada | `review_a_test.go` |

Tidak berlaku: V1 (arsitektur formal), V2.7 (out-of-band), V2.9 (kriptografi hardware), V3.6 (federasi), V6.4 (manajemen kunci eksternal), V9.2 (mTLS). TOTP (V2.8.1 multi-faktor) menyusul Step 23a.

## 5. Risiko yang diterima & batasan yang diketahui

- **`style-src 'unsafe-inline'`** tetap dibutuhkan: Radix memposisikan popover/menu lewat atribut `style` yang dihitung saat runtime (nonce tidak berlaku untuk atribut; `'unsafe-hashes'` tidak praktis untuk nilai dinamis) dan Monaco menyuntikkan elemen `<style>`; pengamatan Step 9b/16a: tanpa direktif ini konsol melaporkan pelanggaran CSP pada atribut `style`. Risiko CSS injection tidak memungkinkan eksekusi script karena `script-src` ketat.
- **`script-src 'wasm-unsafe-eval'`**: pdf.js 6.x mendekode JPX/JBIG2/ICC lewat WASM dari origin sendiri; direktif hanya mengizinkan kompilasi WebAssembly, bukan `eval` JS.
- **Signed URL adalah bearer**: pencabutan sesi tidak membatalkan URL yang masih hidup (TTL 5 menit untuk download/zip, 2 jam media/thumb); akun nonaktif/dihapus dan pencabutan share tetap ditolak saat pemakaian.
- **Unduh backup DB** (`GET /admin/maintenance/backups/{name}`): sesi admin + limiter 5/10 menit + audit + `no-store`; **tanpa konfirmasi ulang password** sampai sudo mode (password + TOTP) di Step 23a. Backup memuat hash argon2id, `token_enc` AES-GCM (hanya terbuka dengan `APP_SECRET`), hash sesi.
- **Limiter in-memory** hilang saat restart, kecuali login yang membaca `login_attempts`; `deploy/nginx-aapanel.conf` PART C memberi contoh `limit_req` opsional di Nginx (tidak dipasang default).
- **Preview-only share** hanya menyembunyikan tombol unduh; tidak mencegah penyimpanan konten yang sudah dirender.
- Arsip **rar** hanya dibaca (tanpa lisensi `rar`) dan belum pernah diuji dengan arsip nyata; arsip 7z ber-password yang rusak dilaporkan `ARCHIVE_PASSWORD_WRONG`.
- `ADMIN_CAN_BROWSE_USERS=true` di server ini: setiap akses admin ke folder user tercatat (`admin.browse_user`); UI read-only, API tulis dijurnal.
- `/api/v1/version` publik (versi + nama aplikasi + logo) untuk halaman login — tidak memuat informasi sensitif.
- **Log Nginx aaPanel**: token link publik (`/s/<token>`, `/api/v1/public/…`) tidak dicatat (`access_log off`, PART B), tetapi URL lain tercatat lengkap termasuk query (`sig=` signed URL, bearer ≤ 2 jam). Statistik situs aaPanel (`log_format site_total`, dikirim ke `/tmp/site_total.sock`) juga memuat header `Cookie` (sesi) — opsi `access_log` di PART A mematikannya untuk situs ini (perlu verifikasi efek pada halaman statistik).
- **HSTS dua kali** di belakang aaPanel (header aaPanel `max-age=31536000` + header aplikasi dengan `includeSubDomains`); browser memakai yang pertama (RFC 6797 §8.1) — dapat diterima.
- `media_positions` tidak ditulis ulang saat rename/move (LRU 500); `refUpdater.Delete` menghapus blob versi di dalam transaksi (bila transaksi gagal setelahnya, baris tanpa blob → `integrity_check` melaporkan).

### Spike 7-Zip (Step 14a)

7-Zip 23.01 (`/usr/lib/7zip/7z`) membaca password dari stdin pipe untuk `a`, `x`, `t`, `l` → tidak ada password di argv; `TestSevenZipPasswordNeverInArgv -count=5` memindai `/proc/*/cmdline` selama operasi. ADR-08 risiko argv tidak berlaku.

### Rekomendasi `hidepid=2`

Memasang `/proc` dengan `hidepid=2` menyembunyikan proses user lain (`ps`, `/proc/<pid>/cmdline`, `environ`) dari
user Linux non-root — mengurangi dampak bila ada rahasia di argv proses lain (bukan dari aplikasi ini). Dampak:
tool monitoring aaPanel yang berjalan sebagai user non-root tidak lagi melihat proses `filemanager`; systemd dan
root tidak terpengaruh. Perintah (opsional, dicatat di `docs/DEPLOY-AAPANEL.md` Step 22):
`mount -o remount,hidepid=2 /proc` + baris `proc /proc proc defaults,hidepid=2 0 0` di `/etc/fstab`. **Perlu
verifikasi** pada aaPanel 8.0.7 bahwa panel (berjalan sebagai root) tetap menampilkan status proses. Tanpa itu pun
unit systemd memakai `ProtectProc=invisible`: proses `filemanager` sendiri tidak melihat proses user lain.

## 6. Dependency & toolchain

- Go: `go 1.26.0` + `toolchain go1.27.3` di `go.mod` (GO-2026-6809 `crypto/x509` diperbaiki di 1.27.3; seri 1.25 lama: CVE-2026-32282, CVE-2026-39822 pada `os.Root` — `TestRuntimeVersion` gagal di bawah 1.25.12).
- `make audit`: `govulncheck ./...` + `pnpm audit --audit-level=high` — keduanya memblokir CI (job `audit`). `pnpm audit` (28 Okt 2026, R-F): **0 advisori** setelah `monaco-editor` 0.57.0 (yang memin `dompurify` 3.4.15 persis — GHSA-cg2g-xh64-v4q7 critical, GHSA-mrv2-3pqr-j5jr high, 3 low) dipaksa memakai dompurify 3.4.17 lewat `overrides` di `web/pnpm-workspace.yaml`, dan `jsdom` 30.1.3 (undici 8.12.3; 4 high + 4 moderate hanya di dependensi dev). Periksa ulang override saat monaco-editor dinaikkan.
- Proses update bulanan: `go list -m -u all`, `pnpm outdated`, baca changelog, naikkan di branch, `make audit && make test && pnpm e2e`, catat di PROGRESS; kerentanan yang dicapai kode diperbaiki ≤ 7 hari.
- Hosting git belum ada → workflow CI belum pernah dieksekusi di GitHub; target yang sama dapat dijalankan lokal: `make lint test build openapi-lint sqlc-check audit`.
