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
| 1 | Lint `forbidigo`: `os.*` filesystem dan `exec.Command` dilarang di luar package yang diizinkan (aktif penuh Step 4) | `backend/.golangci.yml` | CI |

Kontrol yang **belum** ada (dijadwalkan): autentikasi & sesi (Step 3), `fsjail` (Step 4), CSRF (Step 3),
rate limit (Step 3/20), signed URL (Step 12b), SSRF guard (Step 13), batas arsip (Step 14a), CSP final
dengan nonce (Step 20), IDOR test suite (Step 20).

## 4. Catatan risiko yang diterima (keputusan pemilik)

- Password untuk **membuat** arsip 7z dapat lewat argv `7zz` jika stdin tidak didukung (ADR-08); hasil spike
  dicatat di Step 14a.
- `ADMIN_CAN_BROWSE_USERS=true` di server ini: setiap akses admin ke folder user tercatat di audit log.

## 5. Dependency & toolchain

- Go: patch terbaru seri yang dipakai (minimal 1.25.12 karena CVE-2026-32282 dan CVE-2026-39822 pada `os.Root`).
- `govulncheck` dan `pnpm audit` berjalan di CI (`make vuln`).
