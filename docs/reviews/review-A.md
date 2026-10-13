# Review A — Review independen Step 1–5 (config, DB, auth, fsjail, listing)

| | |
| --- | --- |
| Tanggal | 14 Oktober 2026 |
| Basis kode | commit `5cee171` (branch `main`) |
| Lingkup | `backend/internal/{config,db,audit,auth,fsjail,listing,files,httpapi}`, `backend/cmd/filemanager`, `docs/openapi.yaml` |
| Acuan | PRD E1–E3, §11 · SPEC §3, §4, §5, §6, §8.1–8.3, §10 · PROMPT Step R-A |
| Reviewer | Sesi baru tanpa konteks implementasi |

## 1. Ringkasan

Fondasi (config → DB → auth → `fsjail` → listing) secara umum **sesuai desain dan tahan terhadap serangan yang dicoba**: semua probe traversal/encoding, symlink escape (termasuk fixture baru: parent yang ditukar symlink saat `Create`/`MkdirAll` berjalan, loop `a→b→a`, rantai yang keluar root, link dari dalam share ke file privat pemilik), lockout dengan variasi huruf/spasi/NBSP, CSRF (`Origin: null`, subdomain, token sesi lain), timer sesi, seed ulang, dan kebocoran rahasia ke log — semuanya **lolos**.

Ditemukan **0 blocker, 5 major, 11 minor**. Major yang ditemukan bukan celah eksploitasi langsung, melainkan kontrol yang diwajibkan SPEC tetapi belum ditegakkan (header cache/CORP pada respons API, `BLOCKED_EXTENSIONS` pada `files/create`, aturan nama pada bentuk `{path}` penuh), satu test kontrak yang diwajibkan SPEC §6.7 tetapi belum ada, dan satu bug laten di `fsjail` (`SubRoot("/").Close()` menutup handle induk — bertentangan dengan dokumentasi fungsinya sendiri; belum ada pemanggil, tetapi Step 18b akan memakainya).

Baseline verifikasi (sebelum perbaikan): `make lint` 0 issue; `make test` hijau (Go + Vitest); `go test -race -cover ./...` hijau. Coverage: **`internal/fsjail` 91,5 %**, **`internal/auth` 88,6 %**, httpapi 84,1 %, listing 94,1 %, config 80,6 %, db 78,0 %, files 76,2 %.

Semua temuan major **ditutup di fase perbaikan** (lihat kolom Status dan §5). Test probe yang ditulis saat review dipertahankan sebagai test regresi: `backend/internal/httpapi/review_a_test.go`, `backend/internal/fsjail/review_a_test.go`, `backend/internal/auth/review_a_test.go`.

## 2. Metode

1. Membaca PRD/SPEC/PROGRESS yang dirujuk, lalu **seluruh kode** paket di lingkup (bukan diff).
2. Menjalankan `make lint && make test && go test -race -cover ./...`.
3. Menjalankan checklist keamanan R-A dengan **fixture/serangan baru** yang ditulis sebagai test (bukan hanya membaca test lama): lihat daftar di §4 dan file `review_a_test.go`.
4. Membandingkan daftar route chi (`chi.Walk`) dengan `docs/openapi.yaml`; memeriksa envelope/kode error terhadap SPEC §6.2; mencari TODO/`t.Skip` yang menyembunyikan AC.

## 3. Temuan

Prioritas: **blocker** = harus ditutup sebelum step berikutnya dan berdampak keamanan/data; **major** = wajib ditutup sebelum step berikutnya; **minor** = boleh ditunda dengan dicatat di PROGRESS "Catatan lintas step".

### 3.1 Blocker

Tidak ada.

### 3.2 Major

| ID | Lokasi | Deskripsi | Bukti / reproduksi | Saran | Status |
| --- | --- | --- | --- | --- | --- |
| M-01 | `backend/internal/httpapi/middleware.go:262-278` (`SecurityHeaders`) | Respons API JSON (termasuk `GET /files/list` yang berisi daftar file privat dan `Set-Cookie` login) **tidak membawa `Cache-Control: no-store`** dan `Cross-Origin-Resource-Policy: same-origin`; `Permissions-Policy` lebih pendek dari SPEC §10.3; CSP API `default-src 'none'` (§10.3) belum ada. Hanya `index.html` (webassets) yang `no-store`. Cache browser/proxy boleh menyimpan respons privat (CWE-525). | `TestReviewAAPIResponseHeaders`: `GET /api/v1/auth/me` → `Cache-Control=""`. `grep -rn Cache-Control backend/internal` hanya mengenai `webassets/dist.go`. | Set `Cache-Control: no-store` untuk `/api/*`, `/healthz`, `/readyz`; `Cross-Origin-Resource-Policy: same-origin` di semua respons; `Permissions-Policy` lengkap; CSP `default-src 'none'` untuk `/api/*`. Aset hash tetap `immutable` (webassets menimpa). | **Diperbaiki** — `httpapi/middleware.go` (`APICSP`, `PermissionsPolicy`, `isAPIPath`); regresi `TestReviewAAPIResponseHeaders`, `TestSecurityHeadersAndHSTS` |
| M-02 | `backend/internal/httpapi/files.go:354-416` (`handleFilesCreate`); `config.go:443-450` hanya mem-parse | `BLOCKED_EXTENSIONS` di-parse oleh config tetapi **tidak ditegakkan** oleh `POST /files/create`. SPEC §3.3 dan §5.2: ekstensi terblokir → `415 UNSUPPORTED_TYPE` untuk upload/rename/**create**/remote download/extract. | `TestReviewABlockedExtensionOnCreate` dengan `BLOCKED_EXTENSIONS=exe,php,sh`: `POST /files/create {path:"/x.exe"}` → **201** (file tercipta). | Cek ekstensi terakhir (lowercase, tanpa titik) terhadap daftar sebelum menyentuh jail → `UNSUPPORTED_TYPE details.ext`; helper dipakai ulang oleh rename/upload/extract di step berikutnya. `mkdir` tidak terkena (folder bukan file). | **Diperbaiki** — `Server.blockedExtension` dipanggil sebelum jail di `handleFilesCreate`; regresi `TestReviewABlockedExtensionOnCreate` |
| M-03 | `backend/internal/httpapi/files.go:257-265` (`resolveTarget`, cabang `{path}` penuh) dan `files.go:192-201` (`nameWarnings` membuang error) | Bentuk `{path:"/dir/nama"}` hanya melewati `fsjail.Clean` (aturan **path**), bukan `ValidateName` (aturan **nama** §5.2). Backslash sah di segmen path tetapi dilarang di nama → `POST /files/mkdir {path:"/a\\b"}` membuat folder `a\b`; dengan `parents:true` segmen perantara juga tidak divalidasi. Bentuk `{path, name}` sudah benar. | `TestReviewAPathAttacksAndIsolation`: `{path:"/a\\b"}` → **201** `{"name":"a\\b"}` (harus `400 NAME_INVALID reason backslash`). | Validasi segmen terakhir dengan `ValidateName` di cabang path penuh; dengan `parents:true` validasi semua segmen. Kembalikan `NAME_INVALID` + `details.reason`. | **Diperbaiki** — `validateTargetNames(target, allSegments)` di `resolveTarget`; regresi `TestReviewAPathAttacksAndIsolation` (`/a\\b`, `/ok/a\\b`) |
| M-04 | `backend/internal/httpapi` (tidak ada test), `docs/openapi.yaml` | SPEC §6.7 mewajibkan "tes kontrak sederhana membandingkan daftar route chi (`chi.Walk`) dengan path di `openapi.yaml` dan gagal jika ada route API yang tidak terdokumentasi". Test itu **belum ada**; sinkronisasi saat ini manual (kebetulan sinkron: 13 operasi). | `grep -rn "chi.Walk\|openapi" --include='*_test.go' backend/` → kosong. | Tambahkan `TestReviewAOpenAPIContract` (dua arah: route tanpa dokumentasi dan dokumentasi tanpa route) tanpa menambah dependensi YAML (pemindaian blok `paths:`). | **Diperbaiki** — `TestReviewAOpenAPIContract` (13 operasi, dua arah) |
| M-05 | `backend/internal/fsjail/jail.go:88-90` (`SubRoot` berbagi `j.root` untuk `dir="/"`), `jail.go:117-127` (`Close`) | Komentar `Close` menyatakan "A sub-root that shares the parent's handle (SubRoot("/")) does not close it", tetapi kode memanggil `j.root.Close()` tanpa syarat → **menutup handle jail induk** (cache jail per user di `httpapi/jails.go`). Belum ada pemanggil (share Step 18b), tetapi `defer sub.Close()` yang wajar akan mematikan semua request user itu sampai handle di-evict. | `TestReviewASubRootCloseKeepsParentHandle`: `sub,_ := j.SubRoot("/", true); sub.Close(); j.Lstat("/")` → `fsjail lstat /: INTERNAL` (file already closed). | Tandai sub-jail berbagi handle (`shared bool`); `Close` pada sub-jail berbagi hanya melepaskan pointer-nya sendiri. | **Diperbaiki** — field `shared`; tambahan: `j.check(p)` di semua method mengembalikan `ErrClosed` (bukan panic) setelah `Close`, `FS()` jail tertutup = FS kosong; regresi `TestReviewASubRootCloseKeepsParentHandle` |

### 3.3 Minor

| ID | Lokasi | Deskripsi | Bukti / reproduksi | Saran | Status |
| --- | --- | --- | --- | --- | --- |
| m-01 | `backend/internal/httpapi/files.go:137-144`; `backend/internal/listing/service.go:169-175` | `limit` divalidasi terhadap `listing.MaxPageSize` (1000), bukan `LISTING_PAGE_SIZE` konfigurasi. SPEC §3.3/§6.1: "Client boleh minta `limit` ≤ nilai ini". Dengan `LISTING_PAGE_SIZE=50`, `limit=51` tetap diterima. | `TestReviewAListLimitBoundedByPageSize`: `limit=51` → 200. | Validasi `limit ≤ cfg.ListingPageSize` → `VALIDATION_FAILED fields.limit`. | **Diperbaiki** — `handleFilesList`; regresi `TestReviewAListLimitBoundedByPageSize`; SPEC §8.3 diperjelas |
| m-02 | `backend/internal/auth/session.go:94`; `backend/internal/audit/audit.go:390` | `user_agent` dipotong 512 byte; SPEC §4.2 (sessions, audit_logs) menyebut 256. | Baca kode; `TestSessionCreateTruncatesUserAgent` mengunci 512. | Ubah ke 256 dan perbarui test. | **Diperbaiki** — `auth.MaxUserAgentLen = 256`, audit 256; `TestSessionCreateTruncatesUserAgent` |
| m-03 | `backend/internal/auth/service.go:196-203` | `ACCOUNT_LOCKED` dan `ACCOUNT_DISABLED` dijawab **sebelum** verifikasi password (sesuai urutan SPEC §8.1 langkah 3 dan PRD E1-US7), sehingga siapa pun dapat mengetahui bahwa sebuah username ada dan nonaktif/terkunci (oracle enumerasi). | Probe: login `budi` (disabled) dengan password apa pun → 403 `ACCOUNT_DISABLED`. | Spec-conformant; pertimbangkan di Step 20: jawab `ACCOUNT_DISABLED` hanya setelah password benar (lockout tetap sebelum verifikasi untuk melindungi CPU). Catat di SECURITY.md sebagai trade-off. | Ditunda → Step 20 |
| m-04 | `backend/internal/httpapi/auth.go:194-196` | Username > 64 karakter dilaporkan sebagai `fields.password: too_long`. | Baca kode. | Petakan ke field yang benar. | **Diperbaiki** — `fields.username: too_long` |
| m-05 | `backend/internal/httpapi/middleware_auth.go:97-114` (`OriginCheck`) | `Origin` dibandingkan dengan seluruh `APP_BASE_URL`. Jika `APP_BASE_URL` berisi path (`https://x/fm`), semua request non-GET ditolak; config tidak melarang path. | Baca kode. | Bandingkan `scheme://host[:port]` saja, atau tolak path di `config.Load`. Deployment saat ini memakai root → tidak berdampak. | Ditunda → Step 20/22 |
| m-06 | `backend/internal/auth/service.go:175-177` | Limiter login per IP hanya in-memory; `login_attempts` (`CountLoginAttemptsByIPSince`) tidak dikonsultasikan sebagai fallback "bertahan restart" (SPEC §10.2 butir 6). | `CountLoginAttemptsByIPSince` tidak punya pemanggil non-test. | Di Step 20: hitung DB jika limiter in-memory kosong untuk IP itu (mis. setelah restart). | Ditunda → Step 20 |
| m-07 | `backend/internal/httpapi/router.go:84` | `/readyz` ada di listener utama dan mengembalikan `err.Error()` komponen (bisa memuat path host). SPEC §6.3: loopback saja, tidak di-proxy Nginx. | Baca kode. | Pastikan snippet Nginx Step 22 `return 404` untuk `/readyz` dan `/metrics` (sudah direncanakan §12.4). | Ditunda → Step 22 |
| m-08 | `backend/internal/auth/service.go:244-252` | `RecordLoginSuccess` (reset counter, `last_login_at`) dijalankan **sebelum** provisioning home; jika provisioning gagal login gagal tetapi counter sudah direset dan `last_login_at` terisi. | Baca kode. | Pindahkan `RecordLoginSuccess` setelah provisioning dan pembuatan sesi (atau dalam satu transaksi). | Ditunda |
| m-09 | `backend/internal/auth/session.go:73-83`; `db/queries/sessions.sql:22-26` | Batas 20 sesi: yang dicabut adalah yang paling lama **tidak dipakai** (`last_seen_at`), SPEC §10.4 menyebut "yang tertua" (`created_at`). Kosmetik. | Baca kode. | Samakan dengan SPEC atau perbarui SPEC (LRU lebih masuk akal). | Ditunda |
| m-10 | `backend/internal/httpapi/jails.go:467-478` | Jika entri cache ada dengan `username` berbeda dan `inUse > 0`, handle lama tidak pernah ditutup (bocor). Tidak terjangkau sekarang (username immutable). | Baca kode. | Tutup saat refcount 0 lewat sweep, atau tolak kasus ini. | Ditunda |
| m-11 | `backend/internal/httpapi/errors.go:491-504` (`ReadJSON`) | `DisallowUnknownFields` → body dengan field tak dikenal → `400 VALIDATION_FAILED`. Tidak disebut SPEC; frontend (Step 6+) harus tidak mengirim key tambahan. | Baca kode. | Dokumentasikan di SPEC §6.1 atau longgarkan. | Info → Step 6 |

### 3.4 Catatan (bukan temuan)

- `go.mod` `go 1.26.0` (catatan lintas step "Terbuka → R-A"): sudah diselesaikan Step 4 (SPEC/PRD/PROMPT "Go ≥ 1.26"). Ditutup.
- `t.Skip` hanya tiga dan beralasan (toolchain non-rilis, `-short`, root mengabaikan izin direktori). Tidak ada TODO/FIXME di kode non-generated.
- `/api/v1/version` publik (tanpa sesi) mengembalikan versi + `APP_NAME`; terdokumentasi di openapi. Informasi versi ke publik adalah pilihan desain; pertimbangkan di Step 20.

## 4. Diverifikasi OK

Setiap butir diverifikasi dengan test/probe yang dijalankan (nama test di `review_a_test.go`) atau pemeriksaan kode yang disebut.

1. **`os.*` langsung** hanya di paket yang dikecualikan lint (`fsjail`, `config`, `db`, `cmd`); grep manual di `httpapi/listing/files/auth/audit/webassets` kosong; `forbidigo` aktif (`.golangci.yml`).
2. **`Clean`/`Normalize`** menolak NUL, kontrol (U+0000–U+001F, U+007F, termasuk ESC), overlong UTF-8 (`%c0%ae%c0%ae`), `..`, `.`, `.system` di root (→ 404 di API), segmen > 255 B, path > 4096 B, kedalaman > 64; path "absolut" (`/etc/passwd`) diperlakukan virtual; NFD → NFC pada path **dan** nama (`café` → `café` di disk). (`TestReviewAPathAttacksAndIsolation`, `TestReviewACleanEdgeCases`)
3. **Symlink escape** pada parent yang ditukar symlink ke luar root untuk 13 operasi mutasi (`Create`, `Create` truncate, `OpenFile`, `Mkdir`, `MkdirAll`, `Rename`, `Symlink`, `Chmod`, `Chtimes`, `Remove`, `CopyFile`, `CopyTree`, `MoveTree`) → `SYMLINK_ESCAPE`, kanari luar utuh (isi & mode), error tidak memuat path host. Loop `a→b→a` → `ErrEscape` + klasifikasi `loop`; rantai relatif yang keluar → `escape`. (`TestReviewAWritesThroughEscapingParent`, `TestReviewASymlinkLoopAndChain`)
4. **TOCTOU** baru: goroutine menukar `/dir` ↔ symlink ke luar saat `Create`/`MkdirAll` berulang (651 create sukses, 4 ditolak) → tidak ada entri tercipta di luar root. Fixture lama `Open`/`CopyTree` tetap lulus dengan `-race`. (`TestReviewATOCTOUCreate`)
5. **Sub-root share**: link di dalam share yang menunjuk file privat pemilik (di dalam root, di luar share) → `ErrEscape` bagi penerima, `Lstat` menandai `escape`; traversal `..` ditolak; share read-only menolak `Create`. (`TestReviewASubRootCannotReachParent`)
6. **Cookie & sesi**: `fm_session` `HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=604800`; idle 2 jam bergeser (aktif di 1h59 → hidup di 3h58), kedaluwarsa idle → 401 + cookie dihapus; absolut 168 jam tidak bisa diperpanjang dengan aktivitas. (`TestReviewASessionTimers`)
7. **CSRF**: token sesi lain, token sampah, `Origin: null`, `Origin` beda skema, subdomain dengan `Sec-Fetch-Site: same-site`, `Sec-Fetch-Site: cross-site` → `403 CSRF_INVALID` dan tidak ada efek samping; cookie setelah logout → `401` sebelum cek CSRF. (`TestReviewACSRFVariants`)
8. **Lockout** tidak bisa dilewati dengan variasi `ADMIN`, ` admin`, `admin `, `Admin\t`, ` aDmIn` (satu counter), kunci berlaku dari IP lain, percobaan ke-6 → `423` + `Retry-After`; username tak dikenal tidak memengaruhi counter akun nyata. (`TestReviewALockoutUsernameVariants`, `TestReviewALockoutUsernameNormalisation`)
9. **Seed admin** tidak bisa dipicu ulang: env sama, `ADMIN_INITIAL_PASSWORD` berbeda, `ADMIN_USERNAME` berbeda, admin soft-deleted → jumlah user tetap 1, hash tidak berubah, `must_change_password` tidak kembali 1, warning "still set" tercatat, password tidak muncul di log. (`TestReviewASeedCannotBeRetriggered`, `TestReviewASeedNotRetriggeredByDeletedUsers`)
10. **`must_change_password`** menutup semua route S+P yang ter-mount (enumerasi `chi.Walk`, test gagal jika route baru tidak tercakup) dan membiarkan `auth/me`, `auth/csrf`, `auth/change-password`, `auth/logout`. (`TestReviewAMustChangePasswordGate`)
11. **Isolasi user A vs B** pada list/stat/mkdir/create: `../A`, `%2e%2e`, overlong, NUL, `.system`, nama `../x`, `..\x`, `a/b`, `.system` → 400/404 sesuai §6.1/§6.2; tidak ada entri tercipta di home lawan; **cache listing** dengan nama folder sama di dua home tidak bocor; cursor milik A dipakai B hanya mengembalikan item B. (`TestReviewAPathAttacksAndIsolation`, `TestReviewACursorTamper`)
12. **Cursor** dipalsukan (byte dibalik, dipotong, JSON tanpa MAC, `%20`) atau dipakai dengan sort lain → `400 VALIDATION_FAILED fields.cursor`. (`TestReviewACursorTamper`)
13. **Envelope error** `{error:{code,message,details.requestId}}` konsisten pada 400/401/403/404/405/413/423/429; tabel `statusByCode` cocok dengan SPEC §6.2 (36 kode); `fsjail.Error.Path` selalu VPath dan tidak ada respons probe yang memuat `STORAGE_ROOT`.
14. **Log debug** tidak memuat password awal/baru/salah, nilai cookie, token CSRF, `APP_SECRET`, maupun query string (`cursor=bogus`). (`TestReviewANoSecretsInDebugLog`)
15. **`docs/openapi.yaml`** sinkron dua arah dengan 13 operasi yang di-mount (`TestReviewAOpenAPIContract`).
16. **argon2id** m=64 MiB t=3 p=1 (PHC), `VerifyDummy` untuk user tak dikenal, parser PHC membatasi panjang salt/key; CSRF = HMAC(csrf_secret, session_id) dibandingkan konstan-waktu; token sesi 32 byte acak, DB hanya menyimpan SHA-256.
17. **Lint/test**: `golangci-lint` 0 issue (gosec, forbidigo, errcheck, …); `check-sort-policy` (tanpa `collate.Numeric`) lulus; tidak ada `t.Skip` yang menyembunyikan AC.

## 5. Hasil fase perbaikan

Commit `fix(review-A)` (hash di PROGRESS.md Step R-A) menutup **semua major (M-01…M-05)** dan tiga minor murah (m-01, m-02, m-04); minor lain ditunda dan dicatat di PROGRESS "Catatan lintas step" (m-03, m-05, m-06 → Step 20; m-07 → Step 22; m-08, m-09, m-10 → saat menyentuh kode terkait; m-11 → Step 6).

Perubahan kode (± 150 baris non-test):

- `backend/internal/httpapi/middleware.go` — `Cache-Control: no-store` + CSP `default-src 'none'` untuk `/api/*`, `/healthz`, `/readyz`; `Cross-Origin-Resource-Policy: same-origin` dan `Permissions-Policy` lengkap di semua respons (M-01).
- `backend/internal/httpapi/files.go` — `validateTargetNames` untuk bentuk `{path}` penuh (M-03), `blockedExtension` sebelum jail pada `create` (M-02), `limit ≤ LISTING_PAGE_SIZE` (m-01).
- `backend/internal/httpapi/auth.go` — pemetaan field `too_long` (m-04).
- `backend/internal/fsjail/jail.go`, `errors.go` — `shared` pada `SubRoot("/")`, `j.check` + `ErrClosed`, `FS()` aman setelah `Close` (M-05); `stat_linux.go`, `fsutil.go`, `count.go` memakai `j.check`.
- `backend/internal/auth/session.go`, `backend/internal/audit/audit.go` — `user_agent` ≤ 256 (m-02).
- `SPEC.md` §5.3, §6.3, §8.3 — kontrak diperjelas (lihat PROGRESS "Deviasi dari SPEC").

Test regresi/probe baru (± 900 baris): `backend/internal/httpapi/review_a_test.go` (11 test, termasuk kontrak openapi M-04), `backend/internal/fsjail/review_a_test.go` (6 test), `backend/internal/auth/review_a_test.go` (2 test). Semua lulus dengan `-race`.

Verifikasi akhir: lihat §6 dan PROGRESS.md (hasil `make lint && make test && go test -race -cover ./...`).

## 6. Coverage

Diukur dengan `go test -race -cover -count=1 ./...` (GOMAXPROCS=1, 1 vCPU). Gerbang R-A: `fsjail` ≥ 90 % terpenuhi.

| Paket | Sebelum | Sesudah perbaikan |
| --- | --- | --- |
| `internal/fsjail` | 91,5 % | **91,8 %** (tanpa race 91,9 %) |
| `internal/auth` | 88,6 % | 88,6 % |
| `internal/httpapi` | 84,1 % | 85,5 % |
| `internal/listing` | 94,1 % | 94,1 % |
| `internal/config` / `db` / `files` | 80,6 % / 78,0 % / 76,2 % | tidak berubah |

Hasil akhir: `make lint` 0 issue (golangci-lint + sort-policy + eslint/tsc/prettier), `make test` lulus (Go + Vitest), `go test -race -cover -count=1 ./...` lulus.
