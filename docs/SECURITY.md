# SECURITY.md — File Manager (file.kppbwi.app)

Status: kerangka (Step 1). Diisi bertahap tiap step; versi final di Step 20 (hardening) dan R-F.
Sumber kebenaran untuk kontrol keamanan adalah `SPEC.md §10`; dokumen ini merangkum apa yang **sudah
diimplementasikan dan diuji** di kode.

## 1. Melaporkan masalah keamanan

Laporkan ke admin aplikasi (pemilik KPP BWI) secara privat, bukan lewat issue publik. Sertakan langkah
reproduksi dan dampak. Jangan menguji terhadap data user lain tanpa izin.

## 2. Threat model (ringkas)

Rujukan lengkap: `SPEC.md §10.1` (STRIDE). Aset utama: file user di `/www/storage/FileManager/<username>`,
kredensial & sesi, `APP_SECRET`, link share. Batas kepercayaan: browser ↔ Nginx aaPanel ↔ aplikasi Go ↔
filesystem/CLI. Penyerang yang diperhitungkan: user terdaftar yang jahat (lintas user), tamu dengan link
publik, pihak luar lewat remote download (SSRF), isi arsip berbahaya (zip-slip/bomb), konten user
(XSS), user Linux lain di server yang sama (`ps`, `/proc`).

## 3. Kontrol yang sudah ada per step

| Step | Kontrol | Lokasi | Test |
| --- | --- | --- | --- |
| 1 | Konfigurasi fail-fast: `APP_SECRET` wajib 64 hex, `LISTEN_ADDR` loopback di production, tolak root di production (`ALLOW_ROOT` hanya uji lokal), umask 0027 | `internal/config` | `config_test.go` |
| 1 | Rahasia tidak pernah masuk log (redaksi berdasarkan nama key + `LogValuer`) | `internal/config/redact.go` | `redact_test.go` |
| 1 | `X-Forwarded-For` dan `X-Request-ID` hanya dipercaya dari `TRUSTED_PROXIES` | `internal/httpapi/middleware.go` | `middleware_test.go` |
| 1 | Header dasar: `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`, `Permissions-Policy`, COOP, `X-Robots-Tag`, CSP awal; HSTS hanya saat HTTPS terverifikasi | `internal/httpapi/middleware.go` | `middleware_test.go` |
| 1 | Panic tidak membocorkan detail ke client (500 `INTERNAL` + log) | `Recoverer` | `middleware_test.go` |
| 1 | Body JSON ≤ 1 MB (`PAYLOAD_TOO_LARGE`), timeout handler 60 s | `MaxBytes`, `middleware.Timeout` | `middleware_test.go` |
| 1 | Aset statis: dotfile tidak pernah disajikan; `index.html` `no-store` | `internal/webassets` | — (E2E Step 6) |
| 1 | Lint `forbidigo`: `os.*` filesystem dan `exec.Command` dilarang di luar package yang diizinkan | `backend/.golangci.yml` | CI |
| 3 | Password argon2id, kebijakan password + daftar 100k umum, sesi server-side (`fm_session` HttpOnly/Secure/Lax, idle 2 h, absolut 168 h), CSRF HMAC + `Sec-Fetch-Site`/`Origin`, lockout bertahap + rate limit IP, seed admin sekali, `must_change_password` | `internal/auth`, `internal/httpapi` | `auth/*_test.go`, `httpapi/auth_test.go` |
| 4 | **Path & symlink**: semua akses file user lewat `internal/fsjail` di atas `*os.Root` (openat2 `RESOLVE_BENEATH`): path virtual dinormalisasi (`Clean`: NFC, tolak `..`/`.`/NUL/kontrol, batas 255/4096/64, `.system` di root), `checkVPath` di setiap method publik, symlink keluar root → `SYMLINK_ESCAPE` (dibuat: hanya target relatif yang resolve di dalam root; lama: ditampilkan `SymlinkBroken` reason `escape`, tidak pernah diikuti), `Create` lewat `openat(O_NOFOLLOW)` satu komponen pada handle folder dari root (tulisan tidak pernah lewat symlink), `Chmod` lewat `fchmodat2(AT_SYMLINK_NOFOLLOW)` + bit setuid/setgid/sticky dibuang (kelas CVE-2026-32282), `RemoveAll`/`WalkDir`/`DirSize` tidak mengikuti symlink, `AbsForCLI` menolak komponen symlink, sub-root share (`SubRoot`) mengurung penerima, `MoveOut/MoveIn` via `renameat2(RENAME_NOREPLACE)` antar handle. Toolchain dicek ≥ go1.25.12 (CVE-2026-39822 trailing slash) oleh test. Lint: `os.OpenRoot` dan `filepath.Walk/EvalSymlinks` juga dilarang di luar fsjail/config/db/thumbs/upload/cmd (test negatif dibuktikan di Step 4) | `internal/fsjail` | `jail_test.go` (TOCTOU `-race -count=20`, trailing slash, chmod symlink, loop, hardlink), `fsutil_test.go`, `lock_test.go`, `coverage_test.go`; coverage ≥ 90 % |
| 4 | Lock per path sadar-ancestor per user (`LockManager`): operasi bentrok dijawab `409 OPERATION_IN_PROGRESS` dengan pemilik lock | `internal/fsjail/lock.go` | `lock_test.go` |
| 8/12b | **Signed URL** (`internal/download.Signer`): HMAC-SHA256 dengan kunci HKDF(`APP_SECRET`, "signed-url") atas `v1|scope|path|purpose|exp|extra` (tiap field diawali panjangnya), verifikasi constant-time, purpose terikat ke endpoint (URL download tidak bisa dipakai di `/raw` dan sebaliknya), TTL `download|inline|zip` 5 menit / `media|thumb` 2 jam. **Signed URL adalah bearer token**: tidak memerlukan cookie, siapa pun yang memegangnya dapat memakainya sampai `exp` (dipakai download manager, `<video>`, tab baru); pencabutan sesi tidak membatalkan URL yang masih hidup, tetapi akun nonaktif/dihapus ditolak `403` dan `exp` tidak dapat diperpanjang tanpa kunci. Log akses hanya memuat path + status, bukan query string. | `internal/download/signer.go`, `httpapi/download.go` | `signer_test.go`, `signer_extra_test.go`, `httpapi/download_test.go` (tamper sig/path/scope → 403, kedaluwarsa, akun nonaktif, IDOR user B) |
| 12b | **Kebijakan konten inline (ADR-06)**: MIME dari magic bytes (`mimetype`) + ekstensi; HTML/XHTML/SVG/XML/JS/CSS/SWF (dan `*+xml`) selalu `attachment` + `application/octet-stream` apa pun `purpose`; hanya gambar raster, audio, video yang `inline`; PDF/teks `attachment` (viewer mengambil lewat `fetch`). Semua respons konten: `nosniff`, `Content-Security-Policy: sandbox; default-src 'none'`, `Referrer-Policy: no-referrer`, `Cross-Origin-Resource-Policy: same-origin`, `Cache-Control: private, max-age=0, must-revalidate`; `Content-Disposition` dengan `filename*` (fallback ASCII, tanpa `"`/kontrol). Folder dan file khusus ditolak. | `internal/download/policy.go`, `serve.go` | `policy_test.go` (tabel MIME/ekstensi), `download_test.go` (`file.html` & `logo.svg` → octet-stream attachment, `notes.txt.jpg` tidak di-inline) |
| 12b | **Zip streaming** tanpa file sementara: payload daftar path hanya id acak di URL, file JSON ≤ 4 KB di `DATA_DIR/tmp` dengan pemilik, disapu 10 menit; symlink tidak pernah diikuti/diarsipkan (dicatat di `_ERRORS.txt`), path host tidak bocor (`Jail.Redact`), read lock pada sumber, walk berhenti saat klien putus, batas 3 download/zip paralel per user (`429`). | `internal/download/zipstream.go`, `zipstore.go`, `limiter.go` | `zipstream_test.go` (symlink/escape dilewati, zip64 > 4 GiB sintetis, klien putus), `httpapi/download_test.go` (payload user lain → 404, token palsu → 403, semaphore) |

Kontrol yang **belum** ada (dijadwalkan): rate limit endpoint lain (Step 20), SSRF guard (Step 13), batas arsip (Step 14a), CSP final dengan nonce (Step 20), IDOR test suite (Step 20).

## 4. Catatan risiko yang diterima (keputusan pemilik)

- Password untuk **membuat** arsip 7z dapat lewat argv `7zz` jika stdin tidak didukung (ADR-08); hasil spike
  dicatat di Step 14a.
- `ADMIN_CAN_BROWSE_USERS=true` di server ini: setiap akses admin ke folder user tercatat di audit log.

## 5. Dependency & toolchain

- Go: `go 1.26.0` di `go.mod` (dipaksa dependensi inti), toolchain patch terbaru (server: 1.27.2); seri 1.25 lama punya CVE-2026-32282 dan CVE-2026-39822 pada `os.Root` — test `TestRuntimeVersion` gagal keras di bawah 1.25.12.
- `govulncheck` dan `pnpm audit` berjalan di CI (`make vuln`).
