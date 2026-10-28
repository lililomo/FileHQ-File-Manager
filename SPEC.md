# SPEC.md — Spesifikasi Teknis File Manager (file.kppbwi.app)

| | |
| --- | --- |
| Versi | 0.1 — 14 Oktober 2026 |
| Status | **Draft**, menunggu review pemilik sebelum Step 1 dijalankan |
| Acuan | `PRD.md` (apa & kenapa) · `PROMPT.md` (urutan step) · `PROGRESS.md` (status) · `promp-master.md` (brief asal) |
| Pembaca | Engineer yang mengerjakan tiap step `PROMPT.md`; reviewer independen |

Dokumen ini menjawab "bagaimana". Setiap angka batas, nama tabel, kode error, route, dan signature di sini adalah **kontrak**: jika implementasi harus menyimpang, perbarui SPEC dulu dan catat alasannya di `PROGRESS.md`. Bahasa dokumen Indonesia dengan istilah teknis Inggris; kode dan identifier Inggris.

Konvensi penandaan:

- **perlu verifikasi** = klaim tentang perangkat lunak pihak ketiga (aaPanel, library) yang belum dicek di server atau registry; wajib dicek saat step terkait.
- Versi library tidak ditulis; selalu "versi stable terbaru saat implementasi, verifikasi di registry". Versi minimum hanya: **Go ≥ 1.26 pada patch terbaru** (`go.mod`: `go 1.26.0` — diwajibkan oleh dependensi `modernc.org/sqlite`, `goose`, `golang.org/x/crypto`, `golang.org/x/sys` yang semuanya mendeklarasikan `go 1.26`; seri 1.25 punya CVE `os.Root`: CVE-2026-32282 `Root.Chmod` mengikuti symlink, diperbaiki 1.25.9; CVE-2026-39822 open mengikuti symlink pada komponen terakhir + trailing slash, diperbaiki 1.25.12 — patch 1.26.x terbaru memuat keduanya; server memakai 1.27.2) — butuh `os.Root.Rename/RemoveAll/MkdirAll/Symlink` (12 method baru 1.25, diverifikasi di go.dev/doc/go1.25), Node ≥ 22 (Node 24 terpasang di server), Ubuntu 22.04/24.04.

Fakta server yang sudah diverifikasi pada 14 Oktober 2026 (boleh dipakai tanpa tanda verifikasi): Ubuntu 24.04.5, kernel 6.8, **1 vCPU, 1,9 GB RAM + swap 2,8 GB; disk sistem 19 GB (sisa ±6 GB) untuk OS/aaPanel/`/www/wwwroot`; `/www/storage` = disk terpisah `/dev/vdb1` ext4 492 GB**; Go 1.27.2 dipasang 14 Okt 2026 di `/usr/local/go` (apt hanya 1.22), pnpm 12.11.2 via corepack; Node 24.21, npm 11.19, corepack 0.36; ffmpeg/ffprobe/vipsthumbnail/pdftoppm/7zz/rg/unrar **semua belum ada** (koreksi Step 1: `rg` yang terdeteksi sebelumnya ternyata fungsi shell dari lingkungan Claude Code, bukan `/usr/bin/rg`; tersedia di apt: `ffmpeg`, `libvips-tools`, `poppler-utils`, `ripgrep`, `7zip` → binary asli `/usr/lib/7zip/7z`, wrapper sh `/usr/bin/7z|7za|7zr`; **tidak ada** `7zz`, `unrar` multiverse); aaPanel **8.0.7** di `/www/server/panel` (port <PANEL_PORT>), Nginx aaPanel 1.30.5 dengan Lua; tipe proyek **Reverse Proxy** ada dan terbukti bekerja (site `proxy.kppbwi.app`); site `file.kppbwi.app` belum ada; `/www/storage/FileManager` ada, kosong, owner `www:www`; user Linux `filemanager` belum ada; `/proc` tanpa `hidepid`; UFW aktif, 8080 tertutup.

---

## §1 Arsitektur

### 1.1 Komponen

```mermaid
flowchart LR
  subgraph Client["Browser — SPA React/TS"]
    UI["File browser · Editor (Monaco) · Viewer (pdf.js, Video.js 10)"]
    TUSC["tus-js-client"]
    ESRC["EventSource (SSE)"]
  end

  subgraph Edge["Nginx aaPanel · 443 HTTP/2+3 · site file.kppbwi.app"]
    NG["Reverse proxy → 127.0.0.1:8080<br/>proxy_request_buffering off · proxy_buffering off<br/>/.well-known dari document root"]
  end

  subgraph App["filemanager — binary Go tunggal · user Linux filemanager · 127.0.0.1:8080"]
    API["httpapi<br/>chi router · middleware: request-id, headers, session, CSRF, rate limit, authz"]
    AUTH["auth<br/>argon2id · sesi DB · lockout · must_change_password"]
    JAIL["fsjail<br/>os.Root per user · SubRoot untuk share"]
    LIST["listing<br/>ReadDir+Lstat · natural sort · cache 15 s · cursor"]
    FILES["files<br/>rename/copy/move/delete/symlink/chmod/batch rename/undo"]
    UPL["upload<br/>tusd handler + hooks · precheck · finalisasi"]
    DL["download<br/>Range · zip streaming · signed URL"]
    TASK["tasks<br/>engine · semaphore per user · cancel · interrupted"]
    THUMB["thumbs<br/>worker pool = max(1, CPU-1) · cache WebP"]
    ARCH["archive<br/>mholt/archives · zipaes · 7zz"]
    SEARCH["search<br/>walk nama · rg --json · replace"]
    SHARE["share<br/>token hash · cookie HMAC · recipients"]
    SSE["sse<br/>broker per user · heartbeat 25 s"]
    QUOTA["quota · trash · versions · meta · notify · audit"]
    DBL["db<br/>sqlc repository · goose migrations"]
    WEB["webassets<br/>embed.FS web/dist"]
  end

  SQLITE[("SQLite WAL<br/>/www/storage/FileManager/.system/db/app.db")]
  STORE[("STORAGE_ROOT<br/>/www/storage/FileManager/&lt;username&gt;")]
  SYS[(".system/<br/>trash · versions · uploads · thumbs · tmp · logs · backups")]
  CLI["CLI (argv, tanpa shell)<br/>7zz · ffmpeg/ffprobe · vipsthumbnail · pdftoppm · rg · unrar (opsional)"]

  UI -- "HTTPS JSON /api/v1" --> NG
  TUSC -- "POST/PATCH /api/v1/upload/tus/" --> NG
  ESRC -- "GET /api/v1/events" --> NG
  NG --> API
  API --> AUTH
  API --> JAIL
  API --> LIST
  API --> FILES
  API --> UPL
  API --> DL
  API --> TASK
  API --> SHARE
  API --> SEARCH
  API --> QUOTA
  API --> WEB
  API --> DBL
  TASK --> ARCH
  TASK --> THUMB
  TASK --> SEARCH
  TASK --> FILES
  TASK --> SSE
  SSE --> ESRC
  JAIL --> STORE
  FILES --> JAIL
  LIST --> JAIL
  UPL --> SYS
  THUMB --> SYS
  ARCH --> SYS
  QUOTA --> SYS
  DBL --> SQLITE
  ARCH --> CLI
  THUMB --> CLI
  SEARCH --> CLI
```

Prinsip yang mengikat semua komponen:

1. **Satu pintu ke filesystem.** Hanya package `fsjail` yang boleh memanggil `os.OpenRoot` dan metode `*os.Root`. Handler, task, dan worker menerima `*fsjail.Jail`, bukan path absolut. Linter kustom (`make lint` → `go vet` + aturan `forbidigo` untuk `os.Open|os.Create|os.Remove|os.Rename|os.Mkdir|os.ReadDir|os.Stat|ioutil.*` di luar `internal/fsjail`, `internal/config`, dan `internal/db`) menegakkannya.
2. **Server tidak pernah mengirim path absolut.** Semua path di API adalah path virtual relatif terhadap root user (`/` = root user) atau relatif terhadap item yang dibagikan (`?share=<id>`).
3. **Semua proses berat adalah task** (§7) dengan progress via SSE; handler HTTP tidak boleh memblokir lebih dari ±5 detik kecuali endpoint streaming (download, zip, SSE, content search NDJSON).
4. **Tanpa shell.** CLI dipanggil `exec.CommandContext(ctx, absPath, args...)` dengan `--` sebelum nama file, `nice -n 10` + `ionice -c3` (jika tersedia), timeout, dan `Stdin` eksplisit.
5. **Rahasia tidak masuk log.** `APP_SECRET`, password, token share, cookie, dan signed URL disaring oleh `slog.Handler` pembungkus (`internal/httpapi/logredact.go`).

### 1.2 Alur listing

```mermaid
sequenceDiagram
  autonumber
  participant B as Browser
  participant N as Nginx aaPanel
  participant A as httpapi
  participant L as listing
  participant J as fsjail
  participant D as db
  B->>N: GET /api/v1/files/list?path=/Proyek&sort=name&dir=asc&limit=500
  N->>A: proxy (Host, X-Forwarded-For/Proto)
  A->>A: middleware: session → user, PASSWORD_CHANGE_REQUIRED?, rate limit
  A->>L: List(user, "/Proyek", sortSpec, cursor=nil, limit=500)
  L->>J: Stat("/Proyek") → mtime, isDir
  alt cache hit (user,path,sortSpec) & mtime sama & umur < 15 s
    L-->>L: pakai slice tersortir dari cache
  else
    L->>J: ReadDir + Lstat semua entri (batch)
    L->>D: ambil metadata batch: pins, favorites, tags, notes, share flags, media_metadata (IN path...)
    L->>L: natural sort (komparator natural kustom + collator IgnoreCase, §8.3), folder di atas, pin paling atas
    L->>L: simpan cache (TTL 15 s, key hash(user,path,sortSpec))
  end
  L-->>A: halaman 500 item + nextCursor (opaque)
  A-->>B: 200 {items:[...], nextCursor, total, dirMtime}
  B->>B: TanStack Virtual render; infinite scroll → GET ... &cursor=<nextCursor>
```

Mutasi lewat aplikasi (rename, upload selesai, delete, dst.) memanggil `listing.Invalidate(user, dirPath)` sehingga cache tidak pernah menampilkan state basi dari aksi user sendiri. Perubahan dari luar aplikasi (SSH) terdeteksi lewat perbandingan mtime folder paling lambat 15 detik.

### 1.3 Alur upload tus

```mermaid
sequenceDiagram
  autonumber
  participant B as Browser (tus-js-client)
  participant N as Nginx
  participant U as upload (tusd handler)
  participant Q as quota
  participant J as fsjail
  participant D as db
  B->>U: POST /api/v1/upload/precheck {destDir, files:[{name,size}]}
  U->>Q: cek kuota (sisa ≥ Σ size), MAX_UPLOAD_SIZE, ekstensi diblokir
  U->>J: cek konflik nama per file (Lstat)
  U-->>B: 200 {ok:[...], conflicts:[{name, existing:{size,mtime}}], rejected:[{name, code}]}
  B->>B: dialog konflik → policy per file (overwrite|skip|rename)
  B->>N: POST /api/v1/upload/tus/ (Upload-Length, Upload-Metadata: dest, name, policy, X-CSRF-Token)
  N->>U: proxy_request_buffering off
  U->>U: PreUploadCreateCallback: session valid, CSRF, kuota reservasi, NAME_INVALID?, simpan upload_sessions
  U-->>B: 201 Location: /api/v1/upload/tus/<id>
  loop tiap chunk UPLOAD_CHUNK_SIZE (16 MB), 3 file paralel
    B->>N: PATCH /api/v1/upload/tus/<id> (Upload-Offset, application/offset+octet-stream)
    N->>U: stream ke .system/uploads/<id>
    U-->>B: 204 Upload-Offset baru
  end
  U->>U: PreFinishResponseCallback: ukuran file == Upload-Length? (tidak → 400 + hapus)
  U->>J: Rename(.system/uploads/<id> → <dest>/<name>) atomik (fallback copy+delete lintas FS)
  U->>D: upload_sessions.state=finalized, quota_usage += size, activity_logs, media_metadata invalidasi
  U->>U: listing.Invalidate(destDir); antrekan thumbnail; SSE event file.changed
  U-->>B: 204 (selesai)
```

Jika koneksi putus, `tus-js-client` menyimpan URL upload di `localStorage` (fingerprint = nama+ukuran+mtime+destDir) dan melanjutkan dengan `HEAD` → `PATCH` dari offset terakhir, termasuk setelah reload halaman. Upload yang tidak disentuh 24 jam dihapus oleh job pembersih (§7).

### 1.4 Alur download dengan HTTP Range dan signed URL

```mermaid
sequenceDiagram
  autonumber
  participant B as Browser / download manager / <video>
  participant A as httpapi
  participant S as signer
  participant J as fsjail
  B->>A: POST /api/v1/files/sign {path, purpose:"download"|"media"|"thumb", share?}
  A->>S: HMAC-SHA256(key_hkdf, scope|path|exp|purpose)
  A-->>B: 200 {url:"/api/v1/files/raw?path=…&exp=…&purpose=…&sig=…", expiresAt}
  B->>A: GET url (Range: bytes=1048576-) tanpa cookie pun boleh
  A->>S: verifikasi sig (constant-time) & exp
  A->>J: Open(path) → *os.File (O_RDONLY|O_NOFOLLOW di hop terakhir)
  A->>A: tentukan MIME dari magic bytes (mimetype); kebijakan inline vs attachment (§8.9)
  A-->>B: 206 Partial Content · Content-Range · ETag "size-mtime" · Last-Modified · Accept-Ranges · X-Accel-Buffering: no · CSP sandbox · nosniff
  Note over A,B: http.ServeContent menangani Range, If-Range, If-None-Match
```

Download multi-item: `POST /api/v1/files/zip` mengembalikan signed URL ke `GET /api/v1/files/zip?token=…`; server menulis `archive/zip` langsung ke `ResponseWriter` (metode Store default, Deflate opsional) tanpa file sementara; tidak mendukung Range (`Accept-Ranges: none`).

### 1.5 Alur extract sebagai task dengan SSE

```mermaid
sequenceDiagram
  autonumber
  participant B as Browser
  participant A as httpapi
  participant T as tasks
  participant X as archive worker
  participant J as fsjail
  participant E as sse
  B->>A: POST /api/v1/archives/extract {archive:"/a.zip", dest:"/a", password?, encoding:"auto", conflict:"rename"}
  A->>A: validasi path, lock dest (OPERATION_IN_PROGRESS jika bentrok), cek slot task user (< 3)
  A->>T: Enqueue(type=extract, payload)
  T-->>A: task {id, state:queued}
  A-->>B: 202 {task}
  T->>X: run(ctx) saat slot tersedia
  X->>X: pre-scan entri: hitung total, tolak zip-slip/absolut/symlink keluar, cek ARCHIVE_MAX_ENTRIES & rasio
  X->>J: extract ke .system/tmp/<task_id>/ (mode 0640/0750, tanpa setuid)
  loop tiap N entri / 500 ms
    X->>T: Progress(done, total)
    T->>E: publish task.progress {id, done, total, eta}
    E-->>B: event: task.progress
  end
  X->>J: Rename(tmp → dest) atomik (atau per entri jika dest sudah ada & conflict=merge)
  X->>T: Done(result {entries, bytes})
  T->>E: publish task.done + notification
  E-->>B: event: task.done
  B->>B: invalidasi query listing dest, toast "Extract selesai"
  Note over B,A: Cancel: POST /api/v1/tasks/{id}/cancel → ctx dibatalkan → tmp dihapus → state cancelled
```

### 1.6 Batas resource server nyata dan konsekuensinya

Server produksi saat ini: **1 vCPU, 1,9 GB RAM (±490–700 MB available saat dicek) + swap 2,8 GB; disk sistem 19 GB dengan sisa ±6 GB; storage di disk terpisah 492 GB**, dipakai bersama aaPanel, Nginx, dan proyek `proxy.kppbwi.app`. Target non-fungsional di PRD/prompt ditulis untuk 2 vCPU / 2 GB. Konsekuensi yang ditetapkan:

| Aspek | Keputusan |
| --- | --- |
| Worker thumbnail / media | `THUMB_WORKERS` default `max(1, NumCPU−1)` → **1** di server ini. Semua ffmpeg/vips berjalan serial; item yang terlihat di layar diproses dulu (antrian prioritas, §8.4). |
| Task paralel | Per user 3 (`TASK_MAX_PER_USER`), global `max(2, 2×NumCPU)` → **2** di server ini. Compress/extract memakai `nice -n 10`. |
| Memori proses | Target idle < 100 MB tetap. `GOMEMLIMIT` di unit systemd = 512 MiB (`Environment=GOMEMLIMIT=512MiB`) agar GC agresif sebelum OOM. Streaming (upload, download, zip, extract) memakai buffer 1 MiB, tidak pernah menampung file di memori. |
| Build | `pnpm build` (Vite + Monaco) dan `go build` bisa butuh > 1,5 GB RAM. `install.sh` memeriksa RAM/swap; jika RAM < 3 GB dan swap < 2 GB, mencetak instruksi swapfile 2 GB dan hanya menawarkannya secara interaktif (default tidak). Rekomendasi utama: build di CI, unggah binary (ADR-22). |
| Disk | `/www/storage` adalah disk terpisah 492 GB, jadi `DEFAULT_USER_QUOTA=10GB` realistis; disk sistem 19 GB hanya menampung `APP_DIR`, cache build, dan log. Admin dashboard menampilkan kapasitas nyata (`statfs`); `install.sh` memperingatkan jika `DEFAULT_USER_QUOTA` > 50 % ruang bebas. Semua operasi yang menulis mengecek `statfs` dan gagal dengan `QUOTA_EXCEEDED` (details `reason:"disk"`) bila ruang bebas < 512 MB. |
| SQLite | `PRAGMA journal_mode=WAL; synchronous=NORMAL; busy_timeout=5000; cache_size=-16000` (16 MB). Satu koneksi penulis (`db.SetMaxOpenConns(1)` untuk pool tulis) + pool baca terpisah. |
| Transcoding | **Opsional sejak Step 23d** (M6; sebelumnya di luar scope v1 karena CPU): HLS on-the-fly via ffmpeg hanya untuk video yang tidak bisa diputar browser, **default nonaktif** (`transcode.enabled`), `nice 19` + `ionice` idle, maks `transcode.max_concurrent` (default 1) proses global + 1 per user, durasi ≤ 3 jam, resolusi ≤ 720p default, cache `DATA_DIR/tmp/hls` ≤ 10 GiB dihapus 1 jam setelah selesai/idle (§8.14 "Transcoding HLS"). |

**Pembaruan Step 21 (24 Okt 2026).** Server di-upgrade 15 Okt 2026 menjadi **4 vCPU, 5,9 GB RAM** (disk `/` 58 GB, storage 492 GB tetap). Default turunan `NumCPU` di server ini kini `THUMB_WORKERS=3` dan `TASK_MAX_GLOBAL=8`. Target non-fungsional tidak diubah dan tetap dibuktikan untuk 1 vCPU: `make perf` (§11.5) menjalankan binary sekali di semua CPU dan sekali dipin ke CPU 0 (`taskset -c 0`, `GOMAXPROCS=1`, sehingga `THUMB_WORKERS=1`, `TASK_MAX_GLOBAL=2`). Memori: argon2id (64 MiB per hash akun) dikembalikan ke OS 2 detik setelah hash terakhir (`debug.FreeOSMemory`, debounced, `auth.scheduleKDFRelease`) — tanpa itu proses idle setelah satu login memegang 156–219 MB RSS sampai scavenger Go menyusul; kini 26–27 MB. Angka lengkap di `docs/PERF.md`.

---

## §2 Struktur repo

Monorepo satu repo git di `/www/wwwroot/file.kppbwi.app` (APP_DIR). Go module tunggal di `backend/`, package Node tunggal di `web/`.

```text
/www/wwwroot/file.kppbwi.app/
├── .env                          # rahasia runtime, 600, di .gitignore
├── .env.example                  # placeholder CHANGE_ME
├── .gitignore
├── .editorconfig
├── .github/workflows/ci.yml      # lint, test, build, govulncheck, pnpm audit, openapi lint (hosting git: asumsi GitHub)
├── Makefile
├── PRD.md · SPEC.md · PROMPT.md · PROGRESS.md · README.md
├── promp-master.md               # brief asal (tidak dipakai runtime)
├── bin/                          # hasil build: bin/filemanager (di .gitignore)
├── public/                       # document root site aaPanel: hanya .well-known/ (kosong di git, .gitkeep)
├── backend/
│   ├── go.mod · go.sum           # module kppbwi.app/filemanager
│   ├── sqlc.yaml
│   ├── cmd/filemanager/main.go   # flag: serve (default), migrate, admin reset-password, check-deps, version
│   ├── migrations/               # goose: 00001_init.sql …
│   ├── internal/
│   │   ├── config/               # load .env/env, parse size/duration, validasi start
│   │   ├── auth/                 # password, sesi, csrf, lockout, totp (fase akhir)
│   │   ├── fsjail/               # satu-satunya pemakai os.Root
│   │   ├── listing/              # ReadDir, sort, cache, cursor
│   │   ├── files/                # operasi file + konflik + undo + batch rename + symlink + chmod
│   │   ├── upload/               # tusd integrasi, precheck, finalisasi, pembersih
│   │   ├── download/             # Range, zip streaming, signer
│   │   ├── archive/              # engine mholt/archives, zipaes, sevenzip (7zz), limit, browser
│   │   ├── exec/                 # runner CLI: argv tanpa shell, `--`, timeout, nice/ionice, stdin opsional, batas output
│   │   ├── thumbs/               # worker pool, cache, vips/ffmpeg/pdftoppm
│   │   ├── media/                # ffprobe, exif, media_metadata cache, srt→vtt
│   │   ├── search/               # nama (walk), isi (rg/fallback), replace
│   │   ├── share/                # token, slug, password, cookie, recipients, public handlers
│   │   ├── admin/                # dashboard, users, settings, maintenance
│   │   ├── tasks/                # engine, registry tipe task, cleanup jobs
│   │   ├── scheduler/            # cron in-process (job harian jam TZ_DEFAULT / interval), RunNow untuk admin (Step 10a)
│   │   ├── audit/                # audit_logs + activity_logs + retensi
│   │   ├── notify/               # notifications
│   │   ├── trash/                # recycle bin
│   │   ├── versions/             # file_versions pre-image
│   │   ├── quota/                # quota_usage inkremental, reservasi, enforcement, rekalkulasi (Step 10a)
│   │   ├── meta/                 # favorites, pins, notes, tags, folder_view_prefs, referensi path
│   │   ├── sse/                  # broker
│   │   ├── httpapi/              # router chi, middleware, handler per grup, error mapping, logredact
│   │   ├── db/                   # sqlc output (queries.sql.go, models.go), repository interfaces, tx helper
│   │   └── webassets/            # embed.FS dari web/dist (dist.go + //go:embed all:dist)
│   └── testdata/                 # fixture arsip berbahaya, file encoding, gambar kecil
├── web/
│   ├── package.json · pnpm-lock.yaml · vite.config.ts · tsconfig.json · tailwind.config.ts · postcss.config.js
│   ├── index.html
│   ├── public/fonts/             # Inter, JetBrains Mono (OFL) · public/icons/
│   ├── scripts/check-contrast.ts # verifikasi WCAG AA token
│   ├── playwright.config.ts · e2e/
│   └── src/
│       ├── app/                  # providers, router, theme, query client, error boundary
│       ├── routes/               # TanStack Router file routes (sesuai §9.4)
│       ├── features/
│       │   ├── auth/ · browser/ · views/ · fileops/ · upload/ · download/ · tasks/ · trash/ · meta/
│       │   ├── archive/ · search/ · editor/ · viewer/ · share/ · admin/ · profile/ · notifications/ · storage/
│       ├── components/ui/        # shadcn/ui yang dikustom (button, dialog, dropdown, context-menu, …)
│       ├── components/           # komponen lintas fitur (Breadcrumb, FileIcon, DataGrid, EmptyState…)
│       ├── lib/                  # api client (fetch + CSRF + error), format (bytes/date), shortcuts, sse client
│       ├── i18n/{id,en}/*.json   # per fitur
│       └── styles/               # tokens.css, globals.css
├── deploy/
│   ├── install.sh · update.sh · filemanager.service · nginx-aapanel.conf
│   ├── Dockerfile · docker-compose.yml   # opsional
│   └── aapanel/                  # (opsional, fase akhir) otomasi site via API internal panel, pola dari proxy.kppbwi.app
└── docs/
    ├── DEPLOY-AAPANEL.md · SECURITY.md · openapi.yaml · ARCHITECTURE.md (ringkas) · RUNBOOK.md
```

**Target Makefile** (semua dijalankan dari root repo):

| Target | Isi |
| --- | --- |
| `make deps` | `corepack enable && pnpm -C web install --frozen-lockfile`; `go -C backend mod download`; cek tool: `sqlc`, `goose`, `golangci-lint`, `govulncheck` (install via `go install` ke `$(go env GOPATH)/bin`, versi "latest stable, verifikasi"). |
| `make dev` | Jalankan `go run ./cmd/filemanager serve` dengan `.env` dev (`APP_ENV=development`, `STORAGE_ROOT=./.devdata/storage`) dan `pnpm -C web dev` (Vite proxy `/api` dan `/s` ke 8080). |
| `make build` | `pnpm -C web build` → `rm -rf backend/internal/webassets/dist && cp -r web/dist backend/internal/webassets/dist` → `CGO_ENABLED=0 go -C backend build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags --always) -X main.commit=$(git rev-parse --short HEAD) -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o ../bin/filemanager ./cmd/filemanager`. |
| `make test` | `go -C backend test ./... -race -cover` + `pnpm -C web test` (Vitest). |
| `make test-cover` | Coverage profile backend + ambang: `fsjail` ≥ 90 %, `auth`/`share`/`archive` ≥ 80 % (script `backend/scripts/cover-check.sh`). |
| `make lint` | `golangci-lint run` (govet, staticcheck, errcheck, gosec, forbidigo aturan §1.1), `pnpm -C web lint` (eslint + tsc --noEmit), `pnpm -C web format:check` (prettier). |
| `make sqlc` | `sqlc generate` di `backend/`. CI gagal jika output berbeda dari yang di-commit. |
| `make migrate` | `go run ./cmd/filemanager migrate up` (goose embedded). `make migrate-down` menurunkan satu versi. |
| `make e2e` | `pnpm -C web e2e` (Playwright terhadap binary build dengan storage temp). |
| `make openapi-lint` | Validasi `docs/openapi.yaml` (mis. `redocly lint` atau `spectral`, verifikasi tool). |
| `make vuln` | `govulncheck ./...` + `pnpm -C web audit --audit-level=high`. |
| `make release` | `make lint test build` lalu `tar czf dist/filemanager-<version>-linux-amd64.tar.gz bin/filemanager deploy/ docs/DEPLOY-AAPANEL.md`. |

**Alur build/embed:** `web/dist` di-copy ke `backend/internal/webassets/dist` (di `.gitignore`) lalu di-embed dengan `//go:embed all:dist`. Server menyajikan SPA dengan `index.html` fallback untuk route non-API, `Cache-Control: public, max-age=31536000, immutable` untuk aset ber-hash, dan `no-store` untuk `index.html`. Chunk Monaco, pdf.js, Video.js, dan workers berada di `dist/assets/` sebagai file terpisah (dynamic import), tidak pernah dari CDN.

---

## §3 Konfigurasi

### 3.1 Sumber dan precedence

1. **Default** di kode (`internal/config/defaults.go`).
2. **Environment** — dari `EnvironmentFile=` systemd (`.env`) atau env proses. Di `APP_ENV=development`, `config.Load` juga membaca `.env` di cwd (library dotenv kecil tanpa expand shell; verifikasi pilihan library).
3. **Override admin** di tabel `settings` (hanya variabel yang ditandai "Override admin = ya"). Nilai dibaca lewat `config.Runtime()` yang menggabungkan env + settings dan di-cache 5 detik; perubahan dari admin panel berlaku tanpa restart.

Nilai yang ditampilkan di admin panel menunjukkan sumbernya (default / env / override) dan tombol "Kembalikan ke nilai env".

### 3.2 Format nilai

- **Ukuran:** integer + suffix `B`, `KB`, `MB`, `GB`, `TB` (basis 1024, case-insensitive, spasi opsional: `10GB`, `10 GB`, `16MB`). Tanpa suffix = byte. Parser: `config.ParseSize`.
- **Durasi:** format `time.ParseDuration` (`2h`, `30m`, `168h`) ditambah suffix `d` (hari): `30d` = 720h. Parser: `config.ParseDuration`.
- **Boolean:** `true/false/1/0/yes/no` (case-insensitive).
- **Daftar:** dipisah koma, trim spasi (`TRUSTED_PROXIES=127.0.0.1,::1`).

### 3.3 Tabel variabel

| Variabel | Tipe | Default | Validasi | Override admin | Catatan |
| --- | --- | --- | --- | --- | --- |
| `APP_ENV` | enum | `production` | `production` \| `development` \| `test` | tidak | Dev: log teks berwarna, cookie `Secure` dilonggarkan hanya jika `APP_BASE_URL` http://localhost. |
| `APP_BASE_URL` | URL | — (wajib) | `https://` di production; tanpa trailing slash | tidak | Dipakai untuk link share, cookie domain (tidak diset → host-only), `Location` tus. |
| `LISTEN_ADDR` | host:port | `127.0.0.1:8080` | host harus loopback di production (warning jika bukan) | tidak | |
| `TRUSTED_PROXIES` | daftar CIDR/IP | `127.0.0.1` | parse `netip.ParsePrefix`/`ParseAddr` | tidak | `X-Forwarded-For` hanya dipercaya dari sini; IP klien = hop terakhir yang tidak trusted. |
| `STORAGE_ROOT` | path absolut | `/www/storage/FileManager` | ada, dir, writable oleh proses, bukan di bawah APP_DIR | tidak | Hanya berisi folder user + `.system`. |
| `DATA_DIR` | path absolut | `${STORAGE_ROOT}/.system` | ada atau bisa dibuat (0750); subfolder `db, trash, versions, uploads, thumbs, tmp, logs, backups, deleted-users` dibuat saat start | tidak | Jika fsid (`unix.Statfs`) berbeda dari `STORAGE_ROOT` → log `WARN cross-filesystem data dir: trash/upload finalize will copy+delete`. |
| `APP_SECRET` | hex | — (wajib) | tepat 64 karakter hex (32 byte) | tidak | Kunci induk; turunan via HKDF-SHA256 dengan info `session`, `csrf`, `signed-url`, `share-cookie`, `totp-enc`, `totp-recovery` (Step 23a). Rotasi = semua sesi/link signed tidak valid (didokumentasikan). |
| `ADMIN_USERNAME` | string | `admin` | regex username §5.2 | tidak | Dipakai hanya saat tabel `users` kosong. |
| `ADMIN_INITIAL_PASSWORD` | string | — | ≥ 12 karakter saat seed | tidak | Dipakai sekali. Setelah seed, jika masih terisi saat start → `WARN ADMIN_INITIAL_PASSWORD still set in env; remove it`. Tidak pernah di-log nilainya. |
| `ADMIN_CAN_BROWSE_USERS` | bool | `false` | | tidak (sengaja env-only) | Jika true: endpoint `files/*` menerima `?as_user=<username>` untuk admin; setiap request dicatat `audit_logs action=admin.browse_user`. |
| `DEFAULT_USER_QUOTA` | ukuran | `10GB` | ≥ 1 MB; warning jika > 50 % ruang bebas disk | ya | Untuk user baru; user lama tidak berubah. |
| `MAX_UPLOAD_SIZE` | ukuran | `20GB` | ≥ `UPLOAD_CHUNK_SIZE` | ya | Per file. Berlaku juga untuk remote download dan file request. |
| `UPLOAD_CHUNK_SIZE` | ukuran | `16MB` | 1 MB–256 MB | ya | Dikirim ke client via `GET /api/v1/me/config`. Nginx `client_max_body_size` harus ≥ nilai ini (+ overhead) → snippet pakai 64m. |
| `TRASH_RETENTION_DAYS` | int | `30` | 1–3650; `0` = tidak auto-purge | ya | |
| `FILE_VERSION_LIMIT` | int | `50` | 0–500; `0` = versi nonaktif | ya | Per file. |
| `EDITOR_MAX_FILE_SIZE` | ukuran | `5MB` | 64 KB–64 MB | ya | Di atasnya: tail mode read-only. |
| `REMOTE_DOWNLOAD_ENABLED` | bool | `true` | | ya | |
| `REMOTE_DOWNLOAD_MAX_SIZE` | ukuran | `10GB` | ≤ `MAX_UPLOAD_SIZE` | ya | |
| `ARCHIVE_MAX_EXTRACT_SIZE` | ukuran | `50GB` | | ya | Batas total hasil extract; efektif = min(ini, sisa kuota, ruang disk − 512 MB). |
| `ARCHIVE_MAX_ENTRIES` | int | `200000` | 1–10.000.000 | ya | |
| `SHARE_DEFAULT_EXPIRY_DAYS` | int | `7` | 0 = tanpa batas (jika diizinkan) | ya | |
| `SESSION_IDLE_TIMEOUT` | durasi | `2h` | 5m–720h | ya | |
| `SESSION_ABSOLUTE_TIMEOUT` | durasi | `168h` | ≥ idle | ya | |
| `FFMPEG_PATH` | path | `/usr/bin/ffmpeg` | file executable; jika tidak ada → fitur video thumb/cover/srt dan konversi HLS (Step 23d; juga butuh `FFPROBE_PATH`) nonaktif dengan `DEPENDENCY_MISSING` | tidak | Status tampil di admin dashboard. |
| `FFPROBE_PATH` | path | `/usr/bin/ffprobe` | idem | tidak | |
| `VIPS_PATH` | path | `/usr/bin/vipsthumbnail` | idem; tanpa vips → thumbnail gambar memakai decoder Go (jpg/png/gif/webp) sebagai fallback lambat | tidak | |
| `PDFTOPPM_PATH` | path | `/usr/bin/pdftoppm` | idem; tanpa → thumbnail PDF nonaktif | tidak | |
| `SEVENZIP_PATH` | path | `/usr/lib/7zip/7z` | idem; tanpa → 7z & zip AES tulis via 7zz nonaktif (zip AES tetap via library Go) | tidak | Ubuntu 24.04 paket `7zip` 23.01 memasang binary asli di `/usr/lib/7zip/7z` (diverifikasi 14 Okt 2026 setelah `apt install 7zip`); `/usr/bin/7z`, `7za`, `7zr` hanya wrapper `sh` → pakai path binary asli agar tidak lewat shell. Nama `7zz` tidak ada di paket ini. |
| `RAR_PATH` | path | kosong | kosong = compress rar nonaktif | tidak | Binary berlisensi, opsional. |
| `UNRAR_PATH` | path | kosong | kosong = extract rar via mholt/archives (README-nya menyatakan membaca RAR ber-password — diverifikasi 14 Okt 2026; rar5 dicek dengan fixture Step 14a); isi hanya sebagai fallback | tidak | |
| `RG_PATH` | path | `/usr/bin/rg` | tanpa → fallback worker Go | tidak | Belum terpasang di server (dipasang `install.sh` via paket `ripgrep`). |
| `LOG_LEVEL` | enum | `info` | `debug` \| `info` \| `warn` \| `error` | tidak | JSON slog ke stdout (journald). |
| `METRICS_ADDR` | host:port | kosong | kosong = nonaktif; harus loopback | tidak | Prometheus `/metrics` di listener terpisah. |
| `RATE_LIMIT_LOGIN_IP` | `N/jendela[:burst]` \| `off` | `20/1m` | jendela 1s–24h, burst 1..N | tidak | §10.5 login per IP (jendela geser). Semua `RATE_LIMIT_*` dibaca sekali saat start (Step 20). |
| `RATE_LIMIT_PASSWORD_CHANGE` | idem | `5/15m` | | tidak | Ganti password terverifikasi per sesi. |
| `RATE_LIMIT_SHARE_UNLOCK_LINK` | idem | `10/10m` | | tidak | Password share per (link, IP). |
| `RATE_LIMIT_SHARE_UNLOCK_IP` | idem | `30/10m` | | tidak | Password share per IP. |
| `RATE_LIMIT_SHARE_PUBLIC` | idem | `120/1m:60` | | tidak | meta/list/raw/download/zip/thumb publik per (link, IP); burst diabaikan oleh limiter share (kapasitas = N). |
| `RATE_LIMIT_SHARE_NOT_FOUND` | idem | `30/1m` | | tidak | Token share tidak dikenal per IP (anti-enumerasi). |
| `RATE_LIMIT_API` | idem | `600/1m:120` | | tidak | Semua `/api/v1` per sesi (cookie di-hash; tanpa sesi → IP). Dikecualikan: tus, thumb/raw/download/zip/entry-content/subtitle, playlist/segmen `files/hls/*` (Step 23d; `POST /files/hls` tetap dihitung), `public/shares/*`, `events`. |
| `RATE_LIMIT_FILES_LIST` | idem | `240/1m` | | tidak | `GET /files/list` per sesi. |
| `RATE_LIMIT_THUMB` | idem | `600/1m` | | tidak | `GET /files/thumb` per scope tanda tangan yang **terverifikasi** (`u:`/`r:`); tanpa tanda tangan, tanda tangan salah atau kedaluwarsa → kunci IP pemanggil, sehingga scope orang lain tidak bisa dihabiskan hanya dengan menyebut id-nya (R-F M-02). |
| `RATE_LIMIT_NAME_SEARCH` | idem | `30/1m` | | tidak | Pencarian nama per user. |
| `RATE_LIMIT_CONTENT_SEARCH` | idem | `10/1m` | | tidak | Pencarian isi per user (+ 1 aktif). Jendela dipakai bersama pencarian nama. |
| `RATE_LIMIT_REMOTE_DOWNLOAD` | idem | `10/1h` | | tidak | Task remote download per user. |
| `RATE_LIMIT_USER_SUGGEST` | idem | `30/1m` | | tidak | `GET /users/suggest` per user. |
| `RATE_LIMIT_ARCHIVE_SIGN` | idem | `120/1m` | | tidak | `POST /archives/sign` per sesi (review C m-05). |
| `RATE_LIMIT_FILES_SIGN` | idem | `120/1m` | | tidak | `POST /files/sign` per sesi. |
| `RATE_LIMIT_CSRF` | idem | `60/1m` | | tidak | `GET /auth/csrf` per sesi. |
| `RATE_LIMIT_TUS_CREATE` | idem | `60/1m` | | tidak | `POST /upload/tus` per sesi. |
| `RATE_LIMIT_HEALTHZ` | idem | `60/1m` | | tidak | `GET /healthz` per IP. |
| `RATE_LIMIT_BACKUP_DOWNLOAD` | idem | `5/10m` | | tidak | Unduh backup SQLite per sesi admin. |
| `RATE_LIMIT_TOTP` | idem | `5/1m` | | tidak | Verifikasi kode TOTP/kode pemulihan **per user** (login langkah 5, `me/totp/enable`, `me/totp/disable`, `me/totp/recovery-codes`); jendela geser di dalam service (Step 23a). |
| `CONTENT_BASE_URL` | URL | kosong | kosong = origin sama (ADR-06) | tidak | Jika diisi: `raw`, `thumb`, `download`, `zip`, dan halaman share raw dilayani dari origin ini (butuh site aaPanel kedua). |
| `THUMB_WORKERS` | int | `max(1, NumCPU−1)` | 1–16 | tidak | |
| `TASK_MAX_PER_USER` | int | `3` | 1–10 | ya | |
| `TASK_MAX_GLOBAL` | int | `max(2, 2×NumCPU)` | 1–64 | tidak | |
| `LISTING_PAGE_SIZE` | int | `500` | 50–1000 | tidak | Client boleh minta `limit` ≤ nilai ini. |
| `SIGNED_URL_TTL` | durasi | `5m` | 1m–1h | tidak | Download & thumb. |
| `MEDIA_URL_TTL` | durasi | `2h` | 5m–24h | tidak | Playback `<video>/<audio>/<img>` viewer (ADR-15). |
| `AUDIT_RETENTION_DAYS` | int | `180` | 30–3650 | ya | |
| `ACTIVITY_RETENTION_DAYS` | int | `90` | 7–3650 | ya | Juga untuk `share_access_logs`. |
| `TZ_DEFAULT` | IANA tz | `Asia/Jakarta` | `time.LoadLocation` | ya | Dipakai server untuk jadwal job dan email-less laporan; tampilan tanggal di UI memakai zona browser. |
| `BLOCKED_EXTENSIONS` | daftar | kosong | ekstensi tanpa titik, lowercase | ya | Upload/rename/remote download ditolak `UNSUPPORTED_TYPE`. Default kosong karena storage tidak pernah dieksekusi. |
| `PUBLIC_SHARES_ENABLED` | bool | `true` | | ya | |
| `SHARE_ALLOW_NO_EXPIRY` | bool | `true` | | ya | |
| `SHARE_REQUIRE_PASSWORD` | bool | `false` | | ya | |
| `PASSWORD_MIN_LENGTH` | int | `12` | 8–128 | ya | |
| `APP_NAME` | string | `FileManager` | ≤ 40 karakter | ya | Judul & logo teks. Logo gambar: upload admin (PNG ≤ 512 KB, magic bytes diverifikasi) → `DATA_DIR/branding/logo.png`, disajikan `GET /api/v1/branding/logo` (publik, untuk halaman login); setting `branding.logo` menandai keberadaannya (Step 19a). |
| `DEFAULT_LANGUAGE` | enum | `id` | `id` \| `en` | ya | |
| `ALLOW_ROOT` | bool | `false` | hanya untuk uji lokal/CI; **jangan** di production | tidak | Tanpa ini proses yang berjalan sebagai uid 0 di `APP_ENV=production` ditolak saat start (§3.4 butir 6). Ditambahkan Step 1 agar smoke test di server (sebagai root, sebelum user `filemanager` ada) tetap bisa dijalankan. |

Nilai-nilai "Override admin = ya" disimpan di `settings` dengan key sama persis dengan nama env dalam lowercase (`default_user_quota`), value JSON bertipe sesuai (string ukuran/durasi tetap string agar mudah ditampilkan).

### 3.4 Validasi saat start (fail-fast)

`config.Load()` mengembalikan daftar error terkumpul (bukan hanya yang pertama) dan proses keluar dengan kode 2 jika ada:

1. `APP_SECRET` 64 hex; `APP_BASE_URL` valid; `LISTEN_ADDR` parse.
2. `STORAGE_ROOT` ada, direktori, dapat ditulis (test `MkdirAll` + `os.CreateTemp` di `DATA_DIR/tmp`), bukan prefix dari APP_DIR dan sebaliknya.
3. `DATA_DIR` dapat dibuat; subfolder dibuat `0750`; file DB dibuka, `PRAGMA` diterapkan, migrasi `goose up` dijalankan.
4. `STORAGE_ROOT` dan `DATA_DIR` satu filesystem (`unix.Statfs` → bandingkan `Fsid`); jika beda → warning, flag `crossFS=true` dipakai oleh trash/upload.
5. Setiap `*_PATH` yang terisi: ada & executable; yang hilang → fitur dinonaktifkan + warning + status di dashboard (`check-deps` subcommand mencetak tabel ini).
6. Proses tidak boleh berjalan sebagai uid 0 di production (`WARN running as root` → di production = error).
7. Relasi: `UPLOAD_CHUNK_SIZE ≤ MAX_UPLOAD_SIZE`, `SESSION_ABSOLUTE_TIMEOUT ≥ SESSION_IDLE_TIMEOUT`, `REMOTE_DOWNLOAD_MAX_SIZE ≤ MAX_UPLOAD_SIZE`.
8. Seed admin jika tabel `users` kosong (§8.1).
9. Jika `ADMIN_INITIAL_PASSWORD` terisi dan admin sudah pernah mengganti password → warning (bukan error).
10. Umask proses dipaksa `0027`.

---

## §4 Model data

### 4.1 ERD

```mermaid
erDiagram
  users ||--o{ sessions : "memiliki"
  users ||--o| user_preferences : "memiliki"
  users ||--o| editor_sessions : "memiliki"
  users ||--o| quota_usage : "memiliki"
  users ||--o{ folder_view_prefs : "memiliki"
  users ||--o{ shares : "memiliki (owner)"
  shares ||--o{ share_recipients : "ditujukan ke"
  users ||--o{ share_recipients : "menerima"
  shares ||--o{ share_access_logs : "dicatat"
  users ||--o{ favorites : ""
  users ||--o{ pins : ""
  users ||--o{ file_notes : ""
  users ||--o{ file_tags : ""
  users ||--o{ file_versions : ""
  users ||--o{ trash_items : ""
  users ||--o{ tasks : ""
  tasks ||--o{ task_logs : ""
  users ||--o{ notifications : ""
  users ||--o{ audit_logs : "actor (nullable)"
  users ||--o{ activity_logs : ""
  users ||--o{ media_metadata : ""
  users ||--o{ upload_sessions : ""
  shares ||--o{ upload_sessions : "file request (nullable)"
  users ||--o{ login_attempts : "by username (tanpa FK)"
  users ||--o{ search_history : ""
  settings {
    text key PK
    text value_json
    text updated_at
    text updated_by
  }
```

### 4.2 Definisi tabel

Tipe SQLite: `TEXT`, `INTEGER`, `REAL`, `BLOB`. Semua tabel `STRICT` (SQLite ≥ 3.37, dibawa modernc.org/sqlite) dan `WITHOUT ROWID` hanya untuk tabel komposit kecil (favorites, pins, file_notes, file_tags, share_recipients, folder_view_prefs). Foreign key `ON DELETE CASCADE` kecuali disebut lain; `PRAGMA foreign_keys=ON` di setiap koneksi.

**users**

| Kolom | Tipe | Constraint / catatan |
| --- | --- | --- |
| id | TEXT PK | UUIDv7 |
| username | TEXT UNIQUE NOT NULL | regex §5.2, lowercase, immutable |
| display_name | TEXT NOT NULL | ≤ 80 karakter |
| role | TEXT NOT NULL CHECK (role IN ('admin','user')) | |
| status | TEXT NOT NULL CHECK (status IN ('active','disabled')) DEFAULT 'active' | |
| password_hash | TEXT NOT NULL | format PHC `$argon2id$v=19$m=65536,t=3,p=1$<salt>$<hash>` |
| must_change_password | INTEGER NOT NULL DEFAULT 0 | 0/1 |
| password_changed_at | TEXT NULL | |
| quota_bytes | INTEGER NOT NULL | |
| home_provisioned_at | TEXT NULL | diisi saat login pertama sukses |
| home_warning | TEXT NULL | mis. `existing_dir_reused` untuk ditampilkan ke admin |
| last_login_at | TEXT NULL | |
| failed_login_count | INTEGER NOT NULL DEFAULT 0 | direset saat sukses |
| locked_until | TEXT NULL | lockout bertahap |
| totp_secret_enc | BLOB NULL | AES-256-GCM (nonce 12 byte acak ∥ ciphertext, AAD = `users.id`) dengan kunci HKDF-SHA256(`APP_SECRET`, info `totp-enc`); terisi sejak `setup` (pending) dan dihapus saat disable/reset (Step 23a) |
| totp_enabled | INTEGER NOT NULL DEFAULT 0 | 0 = tidak aktif atau setup belum diverifikasi |
| totp_enabled_at | TEXT NULL | saat `enable` berhasil (migrasi 0016) |
| totp_last_step | INTEGER NULL | langkah waktu (unix/30) kode TOTP terakhir yang diterima; anti-replay: kode dengan langkah ≤ nilai ini ditolak (migrasi 0016) |
| created_by | TEXT NULL | user id admin |
| created_at, updated_at | TEXT NOT NULL | |
| deleted_at | TEXT NULL | soft-delete setelah hapus/arsip; username tetap unik (tidak bisa dipakai ulang sampai baris dihapus permanen) |

Index: `users_username_uq (username)`, `users_status_idx (status)`.

**totp_recovery_codes** (Step 23a, migrasi 0016)

| Kolom | Tipe | Catatan |
| --- | --- | --- |
| id | INTEGER PK | |
| user_id | TEXT NOT NULL FK users ON DELETE CASCADE | |
| code_hash | BLOB NOT NULL | HMAC-SHA256(kunci HKDF `totp-recovery`, kode ternormalisasi: huruf kecil tanpa `-`/spasi); 32 byte |
| used_at | TEXT NULL | kode sekali pakai; baris dipertahankan sampai disable/reset/regenerasi |
| created_at | TEXT NOT NULL | |

Index: `idx_totp_recovery_codes_user (user_id)`. Kode plaintext tidak pernah disimpan; 10 baris per user dibuat saat `enable`/regenerasi, dihapus seluruhnya saat disable/reset admin.

**sessions**

| Kolom | Tipe | Catatan |
| --- | --- | --- |
| id | TEXT PK | SHA-256 hex dari token cookie (token 32 byte base64url hanya ada di cookie) |
| user_id | TEXT NOT NULL FK users | |
| csrf_secret | BLOB NOT NULL | 32 byte acak; token CSRF = base64url(HMAC-SHA256(csrf_secret, session_id)) |
| created_at | TEXT NOT NULL | |
| last_seen_at | TEXT NOT NULL | diperbarui maks sekali / 5 menit |
| idle_expires_at | TEXT NOT NULL | last_seen + SESSION_IDLE_TIMEOUT |
| absolute_expires_at | TEXT NOT NULL | created + SESSION_ABSOLUTE_TIMEOUT |
| ip | TEXT NOT NULL | IP klien saat login |
| user_agent | TEXT NOT NULL | dipotong 256 karakter |
| device_label | TEXT NULL | hasil parse UA ringkas ("Chrome · Windows") untuk halaman sesi |
| revoked_at | TEXT NULL | |

Index: `sessions_user_idx (user_id)`, `sessions_idle_idx (idle_expires_at)`. Job pembersih menghapus baris kedaluwarsa > 7 hari.

**login_attempts** — `id INTEGER PK AUTOINCREMENT, username TEXT NOT NULL, ip TEXT NOT NULL, success INTEGER NOT NULL, at TEXT NOT NULL`. Index `(username, at)`, `(ip, at)`. Retensi 30 hari. Dipakai untuk dashboard "login gagal 24 jam" dan rate limit IP (cadangan dari limiter in-memory).

**user_preferences** — `user_id TEXT PK FK, prefs_json TEXT NOT NULL DEFAULT '{}', updated_at TEXT NOT NULL`. Key JSON terdaftar di §8.18. Ukuran maks 64 KB.

**folder_view_prefs** — `user_id TEXT, path TEXT, view_mode TEXT CHECK IN ('details','list','tiles','large','xlarge'), sort_field TEXT, sort_dir TEXT CHECK IN ('asc','desc'), sort2_field TEXT NULL, sort2_dir TEXT NULL, columns_json TEXT NULL, updated_at TEXT, PRIMARY KEY (user_id, path)`. Maks 500 baris per user (saat insert ke-501, hapus yang `updated_at` tertua). Hanya diisi jika preferensi "ingat view per folder" aktif.

**editor_sessions** — `user_id TEXT PK FK, tabs_json TEXT NOT NULL, active_path TEXT NULL, updated_at TEXT NOT NULL`. `tabs_json` = array `{path, scrollTop, cursor:{line,col}, language?, encoding?}` maks 20 entri.

**media_positions** (Step 17b, migrasi `0011`) — `user_id TEXT FK ON DELETE CASCADE, path TEXT, seconds REAL NOT NULL, duration REAL NULL, updated_at TEXT NOT NULL`, PK `(user_id, path)`, indeks `(user_id, updated_at)`. Posisi tonton terakhir per video (resume); maks 500 baris per user — setiap `PUT` memangkas baris yang paling lama tidak diperbarui (LRU). `seconds = 0` menghapus baris (selesai ditonton/mulai dari awal). Path tidak ditulis ulang saat rename/move (baris lama tersapu LRU).

**settings** — `key TEXT PK, value_json TEXT NOT NULL, updated_at TEXT NOT NULL, updated_by TEXT NULL`. Key = nama env lowercase (§3.3; mis. `max_upload_size`, `remote_download_enabled`) plus key non-env: `remote_download.allowlist` (array), `remote_download.denylist` (array), `branding.logo` (nama file logo, ditulis oleh endpoint upload — read-only lewat PUT), `share.allow_custom_slug`, dan konversi HLS (Step 23d, tanpa env, kelompok `media`): `transcode.enabled` (bool, default `false`), `transcode.max_concurrent` (int 1–4, default 1), `transcode.max_resolution` (`480p|720p|1080p`, default `720p`), `transcode.max_duration_minutes` (int 1–180, default 180), `transcode.nice` (int 0–19, default 19). Nilai disimpan sebagai JSON ternormalisasi: ukuran/durasi string (`"50GB"`, `"2h"`), int number, bool, daftar array (Step 19a). Override **menggantikan** nilai env (§3.1) — bukan AND/min seperti implementasi awal Step 13/18a.

**shares**

| Kolom | Tipe | Catatan |
| --- | --- | --- |
| id | TEXT PK | UUIDv7 |
| owner_user_id | TEXT NOT NULL FK users | |
| path | TEXT NOT NULL | path virtual di root pemilik |
| is_dir | INTEGER NOT NULL | |
| mode | TEXT NOT NULL CHECK IN ('public','users') | |
| token_hash | TEXT NULL UNIQUE | SHA-256 hex dari token (hanya mode public) |
| slug | TEXT NULL UNIQUE | `^[a-z0-9][a-z0-9-]{2,63}$`, lowercase, tidak boleh sama dengan token prefix |
| password_hash | TEXT NULL | argon2id m=19456,t=2,p=1 |
| expires_at | TEXT NULL | NULL = tanpa batas |
| max_downloads | INTEGER NULL | |
| download_count | INTEGER NOT NULL DEFAULT 0 | file atau zip |
| view_count | INTEGER NOT NULL DEFAULT 0 | |
| allow_download | INTEGER NOT NULL DEFAULT 1 | |
| allow_upload | INTEGER NOT NULL DEFAULT 0 | file request, hanya folder |
| allow_reshare | INTEGER NOT NULL DEFAULT 0 | mode users |
| hide_owner | INTEGER NOT NULL DEFAULT 0 | |
| note | TEXT NULL | ≤ 500 karakter, tampil di halaman publik |
| token_enc | BLOB NULL | token asli terenkripsi AES-256-GCM (kunci HKDF `share-token`) agar pemilik bisa "Salin link" kapan pun; lookup tetap via `token_hash` |
| hide_listing | INTEGER NOT NULL DEFAULT 0 | file request mode drop-box: pengunjung tidak melihat daftar isi |
| disabled_at | TEXT NULL | nonaktif sementara (manual, user nonaktif, item dihapus) |
| disabled_reason | TEXT NULL | `manual`, `owner_disabled`, `item_trashed`, `admin_revoked` |
| broken_at | TEXT NULL | referensi path tidak ditemukan saat dicek |
| last_access_at | TEXT NULL | |
| created_at, updated_at | TEXT NOT NULL | |

Index: `shares_owner_idx (owner_user_id, path)`, `shares_token_uq`, `shares_slug_uq`, `shares_expires_idx (expires_at)`.

**share_recipients** — `share_id TEXT FK shares, recipient_user_id TEXT FK users, permission TEXT CHECK IN ('viewer','editor'), note TEXT NULL, created_at TEXT, PRIMARY KEY (share_id, recipient_user_id)`. Index `(recipient_user_id)`.

**share_access_logs** — `id INTEGER PK AUTOINCREMENT, share_id TEXT FK, at TEXT, ip_trunc TEXT (IPv4 /24 → "103.10.5.0", IPv6 /48), action TEXT CHECK IN ('view','list','download','zip','unlock_ok','unlock_fail','upload','denied'), user_id TEXT NULL, user_agent TEXT (≤ 200), path TEXT NULL`. Index `(share_id, at)`. Retensi `ACTIVITY_RETENTION_DAYS`.

**favorites** / **pins** — `user_id TEXT, path TEXT, created_at TEXT, PRIMARY KEY (user_id, path)`. Pin berlaku per folder induk (pin menaikkan item ke atas di folder induknya).

**file_notes** — `user_id, path, note TEXT NOT NULL (≤ 1000 karakter), updated_at, PRIMARY KEY (user_id, path)`.

**file_tags** — `user_id, path, color TEXT NOT NULL CHECK IN ('red','orange','yellow','green','blue','purple','gray'), updated_at, PRIMARY KEY (user_id, path)`. Satu warna per item (seperti Finder tag tunggal; multi-tag di luar scope).

**file_versions**

| Kolom | Tipe | Catatan |
| --- | --- | --- |
| id | TEXT PK | UUIDv7 |
| user_id | TEXT FK | |
| path | TEXT NOT NULL | path virtual file |
| version_no | INTEGER NOT NULL | naik per path, mulai 1 |
| size | INTEGER NOT NULL | |
| sha256 | TEXT NOT NULL | untuk dedup "isi sama" |
| blob_rel | TEXT NOT NULL | relatif terhadap `DATA_DIR/versions/<user_id>/`, nama = `<id>` |
| reason | TEXT CHECK IN ('save','force_save','restore','replace_in_files','external_overwrite') | |
| created_at | TEXT NOT NULL | |

Index `(user_id, path, version_no DESC)`, unique `(user_id, path, version_no)`. Pemangkasan: saat jumlah > `FILE_VERSION_LIMIT`, hapus `version_no` terkecil beserta blob.

**trash_items**

| Kolom | Tipe | Catatan |
| --- | --- | --- |
| id | TEXT PK | UUIDv7; juga nama folder/file di `DATA_DIR/trash/<user_id>/<id>` |
| user_id | TEXT FK | |
| original_path | TEXT NOT NULL | path virtual asal (termasuk nama) |
| name | TEXT NOT NULL | |
| is_dir | INTEGER NOT NULL | |
| size_bytes | INTEGER NOT NULL | dihitung saat dihapus (walk) |
| item_count | INTEGER NOT NULL | folder: jumlah file+folder di dalamnya |
| kind | TEXT CHECK IN ('file','folder','image','document','video','audio','archive','other') | untuk filter |
| deleted_at | TEXT NOT NULL | |
| expires_at | TEXT NULL | deleted + TRASH_RETENTION_DAYS; NULL jika retensi 0 |
| batch_id | TEXT NULL | satu aksi delete banyak item → satu batch (untuk undo) |
| restored_at, purged_at | TEXT NULL | baris dipertahankan 7 hari setelah purge untuk aktivitas, lalu dihapus |
| deleted_by | TEXT NULL | (Step 18b, migrasi 0013) user id penerima share ber-izin editor yang menghapus; NULL = pemilik sendiri |

Index `(user_id, deleted_at DESC)`, `(expires_at)`.

**tasks**

| Kolom | Tipe | Catatan |
| --- | --- | --- |
| id | TEXT PK | UUIDv7 |
| user_id | TEXT FK | |
| type | TEXT NOT NULL | §7.1 |
| state | TEXT NOT NULL CHECK IN ('queued','running','done','failed','cancelled','interrupted') | |
| title | TEXT NOT NULL | teks untuk panel ("Extract a.zip → /a") |
| payload_json | TEXT NOT NULL | input, tanpa password (password disimpan hanya di memori; task interrupted yang butuh password diulang dengan input ulang) |
| result_json | TEXT NULL | |
| error_code, error_message | TEXT NULL | |
| progress_done, progress_total | INTEGER NOT NULL DEFAULT 0 | |
| progress_unit | TEXT CHECK IN ('bytes','items') | |
| eta_seconds | INTEGER NULL | |
| speed_bps | INTEGER NULL | |
| cancel_requested | INTEGER NOT NULL DEFAULT 0 | |
| locks_json | TEXT NOT NULL | path yang dikunci (untuk pemulihan setelah restart) |
| created_at, started_at, finished_at, updated_at | TEXT | |

Index `(user_id, created_at DESC)`, `(state)`. Retensi: done/failed/cancelled > 30 hari dihapus.

**task_logs** — `id INTEGER PK AUTOINCREMENT, task_id TEXT FK, at TEXT, level TEXT CHECK IN ('info','warn','error'), message TEXT (≤ 1000)`. Index `(task_id, id)`. Maks 1000 baris per task (baris lebih lama digabung menjadi satu baris "… N baris dilewati").

**notifications** — `id TEXT PK, user_id TEXT FK, type TEXT ('task_done','task_failed','share_received','share_upload','system'), title TEXT, body TEXT NULL, link TEXT NULL (route frontend), read_at TEXT NULL, created_at TEXT`. Index `(user_id, read_at, created_at DESC)`. Maks 200 per user (tertua dihapus).

**audit_logs** — `id INTEGER PK AUTOINCREMENT, at TEXT, actor_user_id TEXT NULL (FK ON DELETE SET NULL), actor_username TEXT NULL, action TEXT, target_type TEXT NULL, target_id TEXT NULL, ip TEXT, user_agent TEXT (≤ 256), details_json TEXT NULL`. Index `(at)`, `(actor_user_id, at)`, `(action, at)`. Retensi `AUDIT_RETENTION_DAYS`. Daftar `action` di §8.16.

**activity_logs** — `id INTEGER PK AUTOINCREMENT, user_id TEXT FK, at TEXT, action TEXT, path TEXT NULL, path2 TEXT NULL (tujuan rename/move), details_json TEXT NULL, batch_id TEXT NULL`. Index `(user_id, at DESC)`, `(user_id, path)`. Retensi `ACTIVITY_RETENTION_DAYS`. "Recent" = 100 aksi terakhir bertipe `open|edit|upload|create|restore` yang path-nya masih ada.

**media_metadata**

| Kolom | Tipe | Catatan |
| --- | --- | --- |
| user_id, path | TEXT, PRIMARY KEY (user_id, path) | |
| size, mtime_unix | INTEGER NOT NULL | kunci validitas; beda → baris dihitung ulang |
| mime | TEXT NOT NULL | dari magic bytes |
| width, height | INTEGER NULL | gambar/video |
| duration_ms | INTEGER NULL | audio/video |
| codec_video, codec_audio | TEXT NULL | |
| bitrate | INTEGER NULL | |
| exif_json | TEXT NULL | subset (Step 17a, `media.Exif`): make, model, lens, software, dateTimeOriginal, dateTime, exposureTime, fNumber, iso, focalLength, focalLength35, orientation, flash, width, height, gps{lat,lon,alt}; **gps disimpan tetapi hanya dikirim** oleh `GET /files/media-info?include_gps=1` (pref `exif.show_gps` = default toggle UI); `{}` = file tanpa EXIF (agar tidak dibaca ulang); NULL = baris lama sebelum 17a (diisi malas saat `media-info`) |
| tags_json | TEXT NULL | audio: title, artist, album, year, track |
| has_cover | INTEGER NOT NULL DEFAULT 0 | |
| thumb_state | TEXT CHECK IN ('pending','ready','failed','unsupported') | |
| thumb_error | TEXT NULL | |
| computed_at | TEXT NOT NULL | |

Index `(user_id, mime)`. Baris dihapus saat file dihapus/dipindah (prefix update saat move).

**upload_sessions** — `id TEXT PK (tus upload id, UUIDv7), user_id TEXT FK, share_id TEXT NULL (FK shares ditambahkan 18a), dest_dir TEXT, filename TEXT, relative_path TEXT NOT NULL DEFAULT '' (folder di bawah dest_dir untuk upload folder, "a/b/c"), size INTEGER, conflict_policy TEXT CHECK IN ('overwrite','skip','rename'), state TEXT CHECK IN ('active','finalized','aborted','expired'), finalized_path TEXT NULL (NULL pada finalized = dilewati karena policy skip), last_modified INTEGER NULL (epoch ms dari klien → mtime), created_at, updated_at, expires_at TEXT`. Index `(user_id, state)`, `(expires_at)`. Reservasi kuota = Σ size baris `active` per user (sumber reservasi durable `quota.AddReservedSource`, ikut dihitung `Usage.Reserved` dan `Reserve`; bertahan setelah restart). Baris non-aktif dipangkas setelah 7 hari (Step 12a).

**quota_usage** — `user_id TEXT PK FK, used_bytes INTEGER, trash_bytes INTEGER, versions_bytes INTEGER, file_count INTEGER, dir_count INTEGER, calculated_at TEXT, dirty INTEGER DEFAULT 0`. `used_bytes` = file di root user (tanpa trash/versi); kuota efektif terpakai = used + trash + versions.

**search_history** — `id INTEGER PK AUTOINCREMENT, user_id TEXT FK, kind TEXT ('name','content'), query TEXT, scope_path TEXT, filters_json TEXT NULL, at TEXT`. Maks 50 per user.

### 4.3 Konvensi

- **ID:** UUIDv7 (string 36 karakter, lowercase) untuk semua entitas yang diekspos ke API; `INTEGER AUTOINCREMENT` hanya untuk log append-only. Library: `github.com/google/uuid` (`uuid.NewV7`, verifikasi ketersediaan di versi stable).
- **Waktu:** `TEXT` ISO-8601 UTC dengan milidetik (`2026-10-14T03:04:05.123Z`); API mengembalikan string yang sama; UI memformat ke zona browser.
- **Path virtual:** selalu diawali `/`, tanpa trailing slash (root user = `/`), NFC, pemisah `/`. Kolom `path` menyimpan path lengkap termasuk nama. Query prefix memakai `path = ? OR path LIKE ? || '/%'` dengan `ESCAPE '\'` setelah meng-escape `%`, `_`, `\` pada prefix.
- **Boolean:** `INTEGER 0/1`.
- **Migrasi:** goose SQL, satu file per perubahan, selalu punya `-- +goose Down`. Nama `NNNNN_<deskripsi>.sql`.
- **sqlc:** query di `backend/internal/db/queries/*.sql`, `emit_json_tags: true`, `emit_interface: true` untuk mock di test, `emit_pointers_for_null_types: true` (kolom nullable → `*string`/`*int64`, bukan `sql.Null*`). Skema sqlc dibaca langsung dari `backend/migrations/` (sqlc mengabaikan bagian `-- +goose Down`). **Jangan mencampur placeholder bernama (`sqlc.arg`/`sqlc.narg`) dan `?` polos dalam satu query**: sqlc menomori placeholder bernama (`?3`) sementara `?` polos dinomori SQLite setelah angka tertinggi, sehingga argumen bergeser ("missing argument with index N"). Parameter `sqlc.narg` di dalam pola `(sqlc.narg('x') IS NULL OR col = sqlc.narg('x'))` dihasilkan bertipe `interface{}`; repository membungkusnya dengan tipe Go yang jelas (`UserFilter`, `AuditFilter`).
- **Transaksi:** helper `(*db.DB).WithTx(ctx, func(q *sqlc.Queries) error)` dan `Repository.WithTx(ctx, func(tx Repository) error)` (nested reuse transaksi luar; writer pool satu koneksi dengan `_txlock=immediate`, reader pool terpisah); operasi filesystem yang berpasangan dengan DB mengikuti urutan **FS dulu, lalu DB** untuk create/move (agar DB tidak merujuk file yang gagal), dan **DB dulu (soft) lalu FS** untuk delete-to-trash (baris `trash_items` dibuat, rename, jika rename gagal baris dihapus).

---

## §5 Path & `fsjail`

### 5.1 Model path virtual dan normalisasi

Setiap request membawa path virtual (`path`, `dest`, `src`, dst.). Normalisasi (`fsjail.Clean(raw string) (VPath, error)`) berjalan **sebelum** sentuhan apa pun ke filesystem dan deterministik:

1. Tolak jika mengandung byte `\0` atau karakter kontrol U+0000–U+001F, U+007F → `PATH_INVALID`.
2. Konversi ke NFC (`golang.org/x/text/unicode/norm`). Tidak ada case-folding (filesystem ext4 case-sensitive; dua nama berbeda case adalah dua item berbeda, UI tetap menampilkan apa adanya).
3. Ganti `\` dengan `/`? **Tidak** — backslash adalah karakter sah di Linux; ditolak di aturan nama (§5.2) agar tidak membingungkan user Windows.
4. Harus diawali `/`; jika tidak, tambahkan `/` (client boleh mengirim relatif). `path.Clean` lalu tolak jika hasil mengandung segmen `..` sebelum Clean (`strings.Split` → segmen `..` atau `.` → `PATH_INVALID`; segmen kosong ganda `//` dirapikan).
5. Panjang: setiap segmen ≤ 255 byte UTF-8, total ≤ 4096 byte, kedalaman ≤ 64 → `PATH_INVALID` (`details.reason`: `segment_too_long|path_too_long|too_deep`).
6. Hasil: `VPath` string `/a/b` (root = `/`). Konversi ke path relatif untuk `os.Root`: `strings.TrimPrefix(v, "/")` → `""` berarti root itu sendiri (`"."`).

Path server absolut = `STORAGE_ROOT/<username>` + rel, tetapi **tidak pernah dibentuk sebagai string** oleh pemanggil; semua akses lewat `*os.Root` yang dibuka pada folder user sehingga kernel (`openat2`/`RESOLVE_BENEATH` di Linux ≥ 5.6, kernel server 6.8) menolak keluar root termasuk lewat symlink dan race.

### 5.2 Aturan nama

**Username** (`auth.ValidateUsername`): `^[a-z0-9][a-z0-9_-]{2,31}$`, tidak dapat diubah, dan bukan salah satu dari daftar terlarang: `root`, `www`, `www-data`, `filemanager`, `system`, `api`, `s`, `share`, `shares`, `static`, `assets`, `public`, `login`, `logout`, `null`, `undefined`, `anonymous`, `guest`, `support`, `trash`, `tmp`, `me`, `all`, `everyone`, `nobody`, `daemon`, `healthz`, `readyz`, `metrics`, `dev`, `editor`, `settings`, `search`. `admin` hanya untuk akun pertama dari env (`ADMIN_USERNAME`); admin kedua harus memakai nama lain. Karena tidak boleh diawali titik, tidak mungkin bentrok dengan `.system`.

**Nama file/folder** (`fsjail.ValidateName(name) (warnings []string, err error)`):

| Aturan | Hasil |
| --- | --- |
| Kosong, `.`, `..` | `NAME_INVALID` |
| Mengandung `/`, `\0`, kontrol | `NAME_INVALID` |
| Mengandung `\` | `NAME_INVALID` (`reason: backslash`) agar tidak ambigu lintas OS |
| > 255 byte UTF-8 | `NAME_INVALID` |
| Diawali `.` | diizinkan (dotfile), UI menampilkan dengan opsi "tampilkan file tersembunyi" (default tampil, seperti aaPanel: perlu verifikasi) |
| Nama Windows reserved (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`, case-insensitive, dengan/ tanpa ekstensi), diakhiri `.` atau spasi, mengandung `< > : " | ? *` | **warning** `windows_incompatible` (operasi tetap jalan; UI menampilkan peringatan sekali, checkbox "jangan tampilkan lagi") |
| Ekstensi ada di `BLOCKED_EXTENSIONS` | `UNSUPPORTED_TYPE` untuk upload/rename/create/remote download/extract (entri dilewati + log) |

Di root user, nama `.system` tidak mungkin muncul karena `.system` ada di `STORAGE_ROOT`, bukan di dalam root user. Namun `fsjail` tetap menolak membuat entri bernama `.system` di level `/` (defense-in-depth, `NAME_INVALID`).

### 5.3 API package `fsjail`

```go
package fsjail

// Jail membungkus satu *os.Root milik satu user (atau sub-root share).
type Jail struct { /* root *os.Root; base VPath; userID string; readOnly bool */ }

// OpenUser membuka STORAGE_ROOT/<username> dengan os.OpenRoot. Folder harus sudah ada
// (provisioning dilakukan auth saat login pertama, §8.2). Mengembalikan ErrHomeMissing jika belum ada.
func OpenUser(storageRoot, username, userID string) (*Jail, error)

// SubRoot membuka sub-jail pada folder di dalam jail (untuk share folder). readOnly=true untuk viewer.
// Untuk share file tunggal, gunakan OpenSharedFile.
func (j *Jail) SubRoot(dir VPath, readOnly bool) (*Jail, error)
func (j *Jail) Close() error

type VPath string
func Clean(raw string) (VPath, error)
func Join(dir VPath, name string) (VPath, error)        // memvalidasi name
func ValidateName(name string) (warnings []string, err error)
func (v VPath) Dir() VPath
func (v VPath) Base() string
func (v VPath) IsRoot() bool
func (v VPath) HasPrefix(dir VPath) bool                // sadar segmen: /ab bukan prefix /a

type Info struct {
    Name string; Path VPath; IsDir bool; IsSymlink bool; SymlinkTarget string; SymlinkBroken bool
    Size int64; Mode fs.FileMode; ModTime, AccessTime, ChangeTime time.Time; BirthTime *time.Time // statx
    Nlink uint64
}

func (j *Jail) Stat(p VPath) (Info, error)              // mengikuti symlink (dalam root)
func (j *Jail) Lstat(p VPath) (Info, error)
func (j *Jail) ReadDir(p VPath) ([]Info, error)         // Lstat tiap entri; untuk 10k entri pakai batch statx
func (j *Jail) Open(p VPath) (*os.File, error)          // O_RDONLY, hop terakhir O_NOFOLLOW ditangani os.Root
func (j *Jail) OpenFile(p VPath, flag int, perm fs.FileMode) (*os.File, error)
func (j *Jail) Create(p VPath, excl bool) (*os.File, error)   // excl → O_EXCL (ALREADY_EXISTS)
func (j *Jail) Mkdir(p VPath) error                     // 0750
func (j *Jail) MkdirAll(p VPath) error
func (j *Jail) Rename(old, new VPath) error             // atomik dalam root
func (j *Jail) Remove(p VPath) error
func (j *Jail) RemoveAll(p VPath) error
func (j *Jail) Symlink(target string, link VPath) error // target relatif, resolve dalam root; lihat 5.4
func (j *Jail) Readlink(p VPath) (string, error)
func (j *Jail) Chmod(p VPath, mode fs.FileMode) error   // hanya bit rwx (0777); setuid/setgid/sticky selalu dibuang
func (j *Jail) ChmodRecursive(ctx context.Context, p VPath, fileMode, dirMode fs.FileMode, progress func(n int)) error
func (j *Jail) WalkDir(ctx context.Context, p VPath, fn fs.WalkDirFunc) error // tidak mengikuti symlink dir
func (j *Jail) DirSize(ctx context.Context, p VPath, progress func(files, dirs int, bytes int64)) (DirStats, error)
func (j *Jail) CountEntries(p VPath, max int) (n int, truncated bool, err error) // getdents saja, untuk itemCount (§8.3)
func (j *Jail) FS() fs.FS                               // os.Root.FS() untuk ripgrep fallback & pencarian nama
func (j *Jail) Statfs() (free, total uint64, err error) // unix.Statfs pada root

// Pertahanan tambahan (implementasi Step 4):
//  - Setiap method publik memanggil checkVPath(p): VPath yang dibangun manual
//    dan tidak bersih ("/a/..", "a", "/a/") ditolak PATH_INVALID sebelum menyentuh os.Root.
//  - Create membuka komponen terakhir dengan openat(dirfd, name, O_NOFOLLOW|O_EXCL?)
//    pada handle folder yang diperoleh dari os.Root: os.Root sendiri mengikuti symlink
//    terakhir, jadi O_NOFOLLOW harus diterapkan oleh satu openat satu komponen → tulisan
//    tidak pernah melewati symlink, bebas race.
//  - Chmod memakai fchmodat2(AT_SYMLINK_NOFOLLOW) (kernel ≥ 6.6); pada symlink menjadi no-op
//    (kelas CVE-2026-32282); fallback lstat+chmod bila ENOSYS.
//  - Lstat/ReadDir memakai statx(AT_SYMLINK_NOFOLLOW) relatif ke handle folder (btime, ctime);
//    ReadDir tidak mengurutkan (listing yang mengurutkan). Symlink diklasifikasi:
//    SymlinkReason ∈ escape | missing | loop | error.
//  - MoveOut/MoveIn memakai renameat2(RENAME_NOREPLACE) antara dua handle folder dari dua
//    os.Root (tanpa path absolut), fallback copy+remove saat EXDEV.
//  - ProvisionHome(storageRoot, username, provisionedBefore) hidup di fsjail; auth memanggilnya.
//  - SubRoot("/") berbagi handle induk (ditandai shared): Close pada sub-jail itu hanya melepas
//    pointer-nya, handle induk tetap hidup (review A, M-05). Setiap method publik memanggil
//    j.check(p): jail yang sudah Close mengembalikan INTERNAL reason "closed" (ErrClosed), bukan panic.

// Operasi komposit (dipakai files/tasks); semua memakai Rename atomik bila bisa.
func (j *Jail) CopyFile(ctx context.Context, src, dst VPath, progress ProgressFn) error   // copy_file_range → fallback io.Copy 1 MiB
func (j *Jail) CopyTree(ctx context.Context, src, dst VPath, progress ProgressFn) error
func (j *Jail) MoveTree(ctx context.Context, src, dst VPath, progress ProgressFn) error   // Rename; EXDEV → CopyTree + RemoveAll
func (j *Jail) MoveOut(ctx context.Context, src VPath, sysDir *SysDir, name string) error // ke .system (trash)
func (j *Jail) MoveIn(ctx context.Context, sysDir *SysDir, name string, dst VPath) error  // dari .system (restore, upload finalize, extract)

// SysDir adalah *os.Root pada subfolder DATA_DIR (trash/<uid>, uploads, tmp/<task>, versions/<uid>).
type SysDir struct{ /* root *os.Root */ }
func OpenSys(dataDir, sub string) (*SysDir, error)
```

Kontrak: semua error dibungkus `fsjail.Error{Code, Path, Err}` dengan `Code` ∈ `NOT_FOUND`, `ALREADY_EXISTS`, `PATH_INVALID`, `NAME_INVALID`, `SYMLINK_ESCAPE`, `FORBIDDEN` (EACCES), `INTERNAL`; `httpapi` memetakannya ke kode error §6.2 tanpa membocorkan path absolut (`Path` selalu VPath).

**Unit test wajib** (`fsjail/jail_test.go`, coverage ≥ 90 %): `../` di awal/tengah/akhir; path absolut `/etc/passwd` diperlakukan sebagai virtual (`/etc/passwd` di dalam root, bukan sistem); `\0`; nama 256 byte; path 4097 byte; kedalaman 65; NFD → NFC (nama `é` dua representasi merujuk entri yang sama setelah normalisasi); symlink absolut ke `/etc` → `SYMLINK_ESCAPE` saat dibuat dan `SymlinkBroken=true` saat di-Lstat jika sudah ada (dibuat lewat `os.Symlink` langsung di test); symlink relatif `../../x` keluar root → ditolak; symlink relatif ke dalam root → boleh dan diikuti; **TOCTOU**: goroutine mengganti folder dengan symlink ke luar root saat operasi `CopyTree` berjalan → operasi gagal dengan `SYMLINK_ESCAPE`, tidak pernah menyentuh target luar (verifikasi dengan folder luar yang berisi file kanari); `Rename` ke luar root ditolak; `RemoveAll` pada symlink dir hanya menghapus link; `Chmod` membuang setuid; `MoveTree` lintas filesystem (simulasi `EXDEV` lewat interface `renamer` yang bisa di-mock); **symlink pada komponen terakhir dengan trailing slash** (`Open("link/")` di mana `link → ../../outside`, CVE-2026-39822) → `SYMLINK_ESCAPE`; `Chmod` pada symlink tidak mengikuti target (CVE-2026-32282); test gagal keras jika `runtime.Version()` < go1.25.12.

### 5.4 Kebijakan symlink

| Situasi | Perilaku |
| --- | --- |
| User membuat symlink lewat API | Target harus **relatif** (ditolak jika diawali `/`), dihitung relatif terhadap folder link, dan hasil resolusinya (`path.Clean(dir+"/"+target)`) harus berada di dalam root (`HasPrefix("/")` setelah normalisasi, tidak ada `..` sisa). Target tidak wajib ada (dangling diperbolehkan, ditampilkan rusak). |
| Listing | `Lstat`; entri symlink ditampilkan dengan ikon link + target. Jika `Stat` gagal atau resolusi keluar root → `SymlinkBroken=true`, UI menampilkan "rusak", aksi buka/masuk dinonaktifkan. |
| Membuka / mengunduh symlink file | Diikuti hanya jika resolusi di dalam root (`os.Root` menjamin; `ELOOP`/`ENOTDIR` dipetakan `SYMLINK_ESCAPE`). |
| Walk (search, dir size, copy, zip, kuota) | Tidak mengikuti symlink direktori; symlink file dihitung sebagai link (ukuran link), bukan isi target, untuk mencegah penggandaan dan loop. |
| Copy/move symlink | Link disalin sebagai link (target string dipertahankan); jika setelah dipindah target keluar root → tetap disalin tetapi ditandai rusak. |
| Arsip | Entri symlink di arsip: dilewati (dicatat di log task) kecuali resolusinya di dalam folder tujuan; hardlink entri tar dilewati. |
| Share | Sub-root dibuka dengan `OpenRoot(subpath)`; symlink di dalam share yang menunjuk ke luar sub-root tetapi di dalam root pemilik → **rusak** dari sudut pandang penerima (tidak diikuti). |

### 5.5 Lock manager per path

`internal/fsjail/lock.go` (dipindah dari `internal/files` di Step 4 agar paket keamanan utuh), satu instance per proses, keyed per user:

```go
type LockManager struct{ mu sync.Mutex; held map[string][]heldLock; cond *sync.Cond }
type heldLock struct{ path VPath; write bool; owner string /* task id / request id */ }

// Acquire memblokir sampai semua paths bisa dikunci atau ctx selesai. Paths diurutkan
// (sort.Strings) sebelum akuisisi agar urutan global konsisten → bebas deadlock.
func (m *LockManager) Acquire(ctx context.Context, userID string, write bool, paths ...VPath) (release func(), err error)
// TryAcquire gagal segera dengan ErrLocked (→ OPERATION_IN_PROGRESS, details.by = judul task).
func (m *LockManager) TryAcquire(userID string, write bool, paths ...VPath) (release func(), err error)
```

Aturan konflik (semua dalam satu user): dua lock bentrok jika salah satunya `write` **dan** path-nya sama atau salah satu adalah ancestor yang lain (`HasPrefix` sadar segmen). Read lock (listing tidak memakai lock; read lock dipakai zip streaming & compress sumber) kompatibel satu sama lain. Request HTTP sinkron memakai `TryAcquire` (gagal cepat); task memakai `Acquire` dengan timeout 30 detik lalu `failed` `OPERATION_IN_PROGRESS`. Lock milik task yang `interrupted` dibuang saat start berikutnya (lock in-memory). Tujuan lock ditulis ke `tasks.locks_json` hanya untuk ditampilkan.

### 5.6 Copy/move lintas filesystem

`MoveTree` mencoba `Rename`; jika `errors.Is(err, syscall.EXDEV)` → `CopyTree` ke nama sementara `dst + ".fm-partial-<id>"` di folder tujuan, verifikasi jumlah/byte, `Rename` ke `dst`, lalu `RemoveAll(src)`. Jika gagal di tengah, partial dihapus dan sumber tetap utuh. Hal yang sama berlaku untuk `MoveOut/MoveIn` saat `crossFS=true` (trash, finalisasi upload, extract tmp → dest). Copy file memakai `unix.CopyFileRange` loop (reflink/zero-copy di ext4 tidak tersedia, tetapi tetap hemat syscall) dengan fallback `io.CopyBuffer` 1 MiB; mode file hasil = mode sumber ∧ 0777 (tanpa setuid), mtime dipertahankan (`os.Chtimes` lewat `Root.Chtimes` jika tersedia di Go 1.25, verifikasi; jika tidak, dilewati).

### 5.7 Update referensi metadata saat rename/move

Semua tabel ber-path (`favorites`, `pins`, `file_notes`, `file_tags`, `file_versions`, `shares`; `trash_items.original_path` dan `activity_logs` tidak diubah) diperbarui dalam **satu transaksi SQL** setelah operasi filesystem berhasil, oleh `files.UpdateReferences(ctx, tx, userID, oldPath, newPath)` (Step 9a; nama paket `meta` di draf awal). Implementasi: registry `files.RegisterReferenceUpdater(name, ReferenceUpdater{Rewrite, Delete})` — lima tabel bawaan didaftarkan oleh `files` lewat `db.RefRepo.RewriteRefs/DeleteRefs`; `shares` mendaftar sendiri di Step 18a (sekaligus me-NULL-kan `broken_at`). Pengecualian yang disengaja: **`media_metadata` tidak di-rewrite** melainkan dihapus lalu di-probe ulang lewat hook `OnChange` (kunci cache thumbnail `sha256(path|size|mtime)` memuat path, sehingga rewrite baris tanpa memindah berkas cache akan menunjuk thumbnail yang tidak ada; probe ulang murah, thumbnail dirender lagi saat terlihat); `folder_view_prefs` juga tidak di-rewrite (preferensi tampilan folder yang dipindah kembali ke default). Prefix di SQL memakai `substr(path,1,len(old||'/'))=old||'/'` (bukan `LIKE … ESCAPE`, lihat §4.3):

```sql
-- untuk tiap tabel T dengan kolom path:
UPDATE T SET path = :new || substr(path, length(:old) + 1)
 WHERE user_id = :uid AND (path = :old OR (:isDir AND path LIKE :oldEsc || '/%' ESCAPE '\'));
```

Jika tujuan sudah punya baris (mis. memindah `a` ke lokasi yang punya catatan lama karena overwrite), baris lama di tujuan dihapus dulu (`DELETE … WHERE path = :new OR path LIKE :new/%`). Untuk `shares`, selain path diubah, `broken_at` di-NULL-kan. Operasi delete-to-trash **tidak** menghapus referensi: baris dibiarkan agar restore memulihkan favorit/catatan/tag; share diset `disabled_at`, `disabled_reason='item_trashed'`. Hapus permanen (dari trash atau Shift+Delete) menghapus semua referensi dan blob versi.

**Referensi rusak** (perubahan via SSH/luar aplikasi): job integritas per 6 jam dan setiap kali item dibuka memverifikasi `Lstat`; jika tidak ada → `shares.broken_at` diisi (share menolak akses `SHARE_DISABLED` details `broken`), favorit/pin/catatan/tag ditampilkan di daftar masing-masing dengan badge "rusak" + aksi hapus; `media_metadata` dihapus. Tidak ada operasi yang boleh panic karena referensi hilang.

---

## §6 API

### 6.1 Konvensi

Body JSON di-decode dengan `DisallowUnknownFields`: field yang tidak dikenal → `400 VALIDATION_FAILED` (klien tidak boleh mengirim key tambahan; dicatat review A m-11, didokumentasikan Step 20).

- Base path `/api/v1`. Request/response JSON UTF-8 (`Content-Type: application/json`), kecuali endpoint streaming (raw/download/zip/thumb/SSE/NDJSON) dan tus.
- Autentikasi: cookie sesi (`fm_session`). Endpoint ditandai **S** = butuh sesi valid; **S+P** = sesi valid dan tidak sedang `must_change_password`; **A** = admin; **Sig** = signed URL (tanpa cookie); **Pub** = publik (dengan rate limit); **Shr** = publik + cookie share jika ber-password.
- Semua non-GET (termasuk tus `POST/PATCH/DELETE`) wajib header `X-CSRF-Token` (§8.1). GET tidak pernah mengubah state.
- `path` di query/body = path virtual. Parameter opsional `share=<share_id>` mengubah scope ke item yang dibagikan ke user (path relatif terhadap item; untuk share file tunggal `path` harus `/` — nama berkas juga diterima — dan respons memakai `/<nama>`). (Step 18b) Scope diterima oleh `files/list|stat|mkdir|create|rename|copy|move|duplicate|delete`, `files/content` GET/PUT, `files/sign`, `files/zip`, `files/media-info`, `versions` (list/content/restore) dan `upload/precheck`; request berjalan di `fsjail.SubRoot` pemilik pada folder yang dibagikan (read-only untuk viewer), id share yang tidak dikenal atau bukan milik penerima → `404`, viewer yang menulis → `403 FORBIDDEN reason=share_permission`. `chmod`, `symlink`, `batch-rename`, `delete-permanent`, `undo`, `trash/*`, `archives/*`, `search/*`, `meta/*`, `me/*`, `shares` dan `files/hls` (Step 23d) menolak scope (`400 not_implemented`/owner-only); kompres/ekstrak/pencarian di dalam share menyusul (task memerlukan scope, catatan 18b). Admin dengan `ADMIN_CAN_BROWSE_USERS=true` boleh `as_user=<username>`.
- Paging: `limit` (≤ `LISTING_PAGE_SIZE`) + `cursor` opaque (base64url JSON, ditandatangani HMAC 8 byte agar tidak dimanipulasi, tidak bersifat rahasia). Response `{"items":[…],"nextCursor":"…"|null,"total":N}`.
- Operasi batch menerima array dan mengembalikan `{"results":[{"path","ok","code"?,"message"?}], "taskId"?}`; jika estimasi > 200 item atau > 256 MB → berjalan sebagai task dan response `202 {task}`.
- Idempotency: `POST` yang membuat task menerima header `Idempotency-Key` (UUID) opsional; key sama dalam 10 menit mengembalikan task yang sama.
- ETag file: `"<size>-<mtimeUnixNano>"` (weak untuk listing: `W/"<dirMtime>-<count>"`). Editor save mengirim `If-Match`.
- Nama file di header: `Content-Disposition: attachment; filename="<ascii-fallback>"; filename*=UTF-8''<percent-encoded>`.
- Request ID: header `X-Request-ID` (dibuat server jika tidak ada), ikut di log dan body error (`details.requestId`).
- Tanggal ISO-8601 UTC. Ukuran selalu byte integer.

### 6.2 Format error dan kode

```json
{ "error": { "code": "QUOTA_EXCEEDED", "message": "Kuota tidak cukup", "details": { "needed": 1048576, "available": 0, "requestId": "…" } } }
```

`message` dalam bahasa UI user (header `Accept-Language` atau preferensi); client memakai `code` untuk logika dan boleh mengganti pesan dengan terjemahannya sendiri.

| Kode | HTTP | Kapan |
| --- | --- | --- |
| `UNAUTHENTICATED` | 401 | Tidak ada/invalid/expired sesi. |
| `FORBIDDEN` | 403 | Role tidak cukup; akses item bukan milik user; share tanpa permission editor. |
| `PASSWORD_CHANGE_REQUIRED` | 403 | Semua endpoint selain auth/me/change-password saat `must_change_password`. |
| `CSRF_INVALID` | 403 | Header hilang/salah, atau `Sec-Fetch-Site` = `cross-site`. |
| `ACCOUNT_LOCKED` | 423 | Lockout login; `details.retryAfter` detik. |
| `ACCOUNT_DISABLED` | 403 | Status disabled (sesi dicabut). |
| `VALIDATION_FAILED` | 400 | Body/query tidak valid; `details.fields: {name: reason}`. Pengecualian Step 23d: `POST /files/hls` menjawab **422** dengan `details.reason=too_long|unknown_duration` (+ `maxMinutes`, `durationMs`) — inputnya valid, videonya yang tidak memenuhi batas. |
| `NOT_FOUND` | 404 | Path/ID tidak ada. |
| `METHOD_NOT_ALLOWED` | 405 | Method tidak didukung pada route yang ada (router chi). Ditambahkan Step 1. |
| `ALREADY_EXISTS` | 409 | Nama tujuan sudah ada dan policy tidak diberikan. `details.existing:{size,mtime,isDir}`. |
| `CONFLICT` | 409 | `If-Match` tidak cocok (editor), state task tidak memungkinkan aksi; `POST /files/hls`: `reason=already_running` (`details.taskId`, konversi lain milik user masih queued/running) atau `reason=disk_budget` (cache `DATA_DIR/tmp/hls` ≥ 10 GiB; sementara — hilang setelah sweep, karena itu bukan `QUOTA_EXCEEDED`). |
| `OPERATION_IN_PROGRESS` | 409 | Path dikunci task lain; `details.taskId,title`. |
| `QUOTA_EXCEEDED` | 413 | Kuota user atau disk; `details.reason: quota|disk`. |
| `PAYLOAD_TOO_LARGE` | 413 | Body JSON > 1 MB; file > `MAX_UPLOAD_SIZE`. |
| `FILE_TOO_LARGE` | 413 | Editor: > `EDITOR_MAX_FILE_SIZE` tanpa `tail=true`; checksum > batas. |
| `RATE_LIMITED` | 429 | `Retry-After` header. |
| `UNSUPPORTED_TYPE` | 415 | Ekstensi diblokir; format arsip tidak didukung; viewer tidak mendukung. |
| `BINARY_FILE` | 415 | Editor menolak file biner. |
| `PATH_INVALID` / `NAME_INVALID` | 400 | §5.1 / §5.2; `details.reason`. |
| `SYMLINK_ESCAPE` | 400 | Symlink keluar root / rusak diikuti. |
| `TASK_LIMIT` | 429 | > `TASK_MAX_PER_USER` task aktif. |
| `ARCHIVE_UNSAFE` | 422 | Zip-slip/absolut/symlink keluar; `details.entry`. |
| `ARCHIVE_LIMIT` | 422 | Entri/ukuran/rasio melampaui batas. |
| `ARCHIVE_PASSWORD_REQUIRED` / `ARCHIVE_PASSWORD_WRONG` | 422 | |
| `SSRF_BLOCKED` | 422 | URL remote resolve ke IP terlarang, skema bukan http(s), userinfo, host/port terlarang, atau redirect ke alamat terlarang; `details.reason, host, ip?, hop?, index?` (Step 13). |
| `REMOTE_UPSTREAM` | 502 | Server sumber remote download menjawab 4xx/5xx; `details.status` (Step 13). |
| `REMOTE_NETWORK` | 502 | Koneksi ke server sumber gagal/putus setelah retry; part dipertahankan untuk Retry (Step 13). |
| `SHARE_EXPIRED` / `SHARE_DISABLED` / `SHARE_LIMIT_REACHED` | 410 | Halaman publik menampilkan pesan sesuai. |
| `SHARE_PASSWORD_REQUIRED` / `SHARE_PASSWORD_WRONG` | 401 | Hanya endpoint publik. |
| `SIGNATURE_INVALID` / `SIGNATURE_EXPIRED` | 403 | Signed URL. Client media meminta URL baru lalu melanjutkan. |
| `DEPENDENCY_MISSING` | 501 | Fitur butuh CLI yang tidak terpasang; `details.tool`. |
| `INTERNAL` | 500 | Tidak pernah membawa pesan error Go mentah ke client; detail hanya di log dengan requestId. |

### 6.3 Endpoint per grup

Kolom "Auth" memakai kode §6.1. Request/response ditulis ringkas; skema lengkap di `docs/openapi.yaml` (§6.7).

**Auth & sesi (`/api/v1/auth`)**

| Method & path | Auth | Request | Response | Error utama |
| --- | --- | --- | --- | --- |
| `POST /auth/login` | Pub | `{username, password, totp?, recoveryCode?}` | `200 {user, mustChangePassword, csrfToken, recoveryCodesRemaining?}` + `Set-Cookie fm_session` (`recoveryCodesRemaining` hanya bila login memakai kode pemulihan) | `UNAUTHENTICATED` (pesan generik "username atau password salah"), `ACCOUNT_LOCKED`, `ACCOUNT_DISABLED`, `RATE_LIMITED` (per IP, atau per user pada langkah TOTP), `VALIDATION_FAILED` (`details.fields.totp: totp_required` — password benar tetapi 2FA aktif dan `totp`/`recoveryCode` tidak dikirim; `totp_invalid` — kode salah/kedaluwarsa/sudah dipakai; Step 23a) |
| `POST /auth/logout` | S | — | `204` + cookie dihapus | |
| `GET /auth/csrf` | S | — | `200 {csrfToken}` | |
| `GET /auth/me` | S | — | `200 {user:{id,username,displayName,role,mustChangePassword,quota:{limit,used,trash,versions},homeProvisioned,totpEnabled,totpEnabledAt?,language,prefs}, config:{chunkSize,maxUploadSize,editorMaxFileSize,remoteDownloadEnabled,publicSharesEnabled,shareAllowNoExpiry,shareRequirePassword,transcodeEnabled (Step 23d: `transcode.enabled` dan ffmpeg+ffprobe ada),features:{ffmpeg,vips,sevenzip,rar,unrar,rg,pdftoppm}}}` | |
| `POST /auth/change-password` | S | `{currentPassword, newPassword}` | `204`; semua sesi lain dicabut | `VALIDATION_FAILED` (`details.fields.newPassword: too_short|common|same_as_current|same_as_initial`) |

**Profil (`/api/v1/me`)**

| Method & path | Auth | Request | Response |
| --- | --- | --- | --- |
| `GET /me/preferences` · `PUT /me/preferences` | S+P | `{prefs}` (merge patch, key terdaftar §8.18; key tak dikenal → `400 VALIDATION_FAILED details.fields.<key>=unknown_key`, bentuk salah → `invalid_value`) | `200 {prefs}` |
| `GET /me/folder-prefs?path=` · `PUT /me/folder-prefs` · `DELETE /me/folder-prefs?path=` | S+P (CSRF non-GET) | PUT `{path, view?, sort?, dir?, sort2?, dir2?, columns?}`; hanya dipakai bila `view.remember_per_folder` | `200 {pref}` (`pref: null` bila belum ada) · `204`; maks 500 baris/user (LRU `updated_at`) |
| `GET /me/sessions` | S+P | — | `200 {items:[{id (prefix 8 hex), current, ip, deviceLabel, createdAt, lastSeenAt}]}` |
| `DELETE /me/sessions/{id}` · `POST /me/sessions/revoke-others` | S+P | — | `204`; id milik user lain/tidak dikenal → `404`; sesi saat ini → `409 CONFLICT reason=current_session` (pakai `auth/logout`); audit `user.revoke_sessions {scope: one|others}` (Step 19b) |
| `GET /me/folder-views?path=` · `PUT /me/folder-views` · `DELETE /me/folder-views?path=` | S+P | `{path, viewMode, sort…, columns}` | `200` |
| `GET /me/editor-session` · `PUT /me/editor-session` | S+P | `{tabs:[{path, scrollTop, cursor:{line,col}, language?, encoding?, pinned?}] (maks 20, path divalidasi), activePath|null}`; PUT mengganti seluruh dokumen (≤ 32 KB) | `200`, `VALIDATION_FAILED` (`tabs=max_20|invalid_path`), `PAYLOAD_TOO_LARGE` |
| `GET /me/activity?cursor&limit&action&from&to&q` · `DELETE /me/activity` | S+P | — | `200 {items:[{id, at, action, path, path2, details, batchId, exists}], nextCursor, retentionDays}` (cursor = id terakhir; `from`/`to` = `YYYY-MM-DD` atau RFC 3339, `to` inklusif harian; `q` substring path) · `200 {deleted}` ("Hapus riwayat saya") |
| `GET /me/media-position?path` · `PUT /me/media-position` | S+P (CSRF non-GET) | PUT `{path, seconds (0–10.000.000; 0 = lupakan), duration?}` (Step 17b) | `200 {path, seconds, duration?, updatedAt|null}` — tanpa baris → `seconds:0, updatedAt:null`; maks 500 baris/user (LRU `updated_at`) |
| `GET /me/recent?limit=100` · `POST /me/recent` | S+P | `{path}` | `200 {items:[Item + location + action + at], total, windowDays}` (hanya yang masih ada) · `204` (catat `open`) |
| `GET /me/storage-analysis` · `POST /me/storage-analysis` | S+P | — | `200 {generatedAt, computedAt, partial, total, files, dirs, tree, topFiles:[50], byType, trash, versions, quota, cached, durationMs}` (GET: cache 10 menit atau hitung sinkron ≤ 60 s; POST: hitung ulang). Walk > 60 s → `202 {…laporan parsial, task}` dengan task `dir_size` (`payload.analysis=true`) yang menghitung penuh (Step 11); `task.done` membuat klien memuat ulang halaman |
| `GET /me/duplicates` · `POST /me/duplicates` · `POST /me/duplicates/symlink` | S+P (CSRF non-GET; tanpa `?share=`) | POST `{path?}` (default `/`, harus folder) · symlink `{keep, paths[]}` atau `{groups:[{keep, paths[]}]}` (≤ 200 path total) | GET `200 {taskId, path, groups, reclaimable, scanned, candidates, generatedAt, truncated, totalGroups}` — hasil task `duplicate_finder` `done` terbaru dengan `finished_at` ≤ 24 jam, salinan yang hilang/berubah dibuang saat dibaca; tidak ada → `404 reason=no_result` · POST `202 {task}`; scan user ini masih queued/running → `409 CONFLICT reason=already_running details.taskId`; bukan folder → `400 not_a_directory` · symlink `200 {results, batchId, undoToken, undo}` (seperti `files/delete`); hash/ukuran/inode tidak cocok → `400 VALIDATION_FAILED reason=hash_mismatch|size_mismatch|same_file|not_a_file|contains_keep|duplicate_path`; Σ ukuran yang di-hash ulang > 2 GiB → `413 FILE_TOO_LARGE reason=too_large` (Step 23c, §8.7) |
| `POST /me/totp/setup` | S+P | — | `200 {secret (base32 tanpa padding, 32 karakter), otpauthUri}`; menyimpan secret **pending** terenkripsi (`totp_enabled=0`, setup baru menimpa pending lama); 2FA sudah aktif → `409 CONFLICT reason=totp_enabled`. `otpauthUri = otpauth://totp/<issuer>:<username>?secret=…&issuer=<issuer>&algorithm=SHA1&digits=6&period=30` (issuer = `APP_NAME`/branding, fallback `FileManager`); QR dirender client (Step 23a) |
| `POST /me/totp/enable` | S+P | `{code}` | `200 {recoveryCodes:[10 × "xxxxx-xxxxx"], enabledAt}` (kode pemulihan tampil **sekali**); kode diverifikasi terhadap secret pending (window ±1) → `totp_enabled=1`, `totp_enabled_at`, `totp_last_step`; tanpa setup → `409 reason=totp_not_setup`; sudah aktif → `409 reason=totp_enabled`; kode salah → `400 VALIDATION_FAILED details.fields.code=totp_invalid`; limiter `RATE_LIMIT_TOTP` per user → `429`; audit `auth.totp_enabled` |
| `POST /me/totp/disable` | S+P | `{password, code}` (`code` = TOTP atau kode pemulihan) | `204`; menghapus secret, kode pemulihan, `totp_last_step`; belum aktif → `409 reason=totp_disabled`; password salah → `400 details.fields.password=incorrect`; kode salah → `400 details.fields.code=totp_invalid`; setiap percobaan dihitung limiter TOTP per user → `429`; audit `auth.totp_disabled` |
| `POST /me/totp/recovery-codes` | S+P | `{password}` | `200 {recoveryCodes}` — membuat 10 kode baru dan membatalkan semua kode lama; belum aktif → `409 reason=totp_disabled`; password salah → `400 details.fields.password=incorrect` (dihitung limiter TOTP); audit `auth.totp_recovery_regenerated` |
| `GET /me/quota` | S+P | `200 {quota, limit (alias), used, trash, versions, reserved, free, percent, fileCount, dirCount, dirty, calculatedAt}` — angka inkremental `quota_usage` (+ reservasi operasi yang sedang berjalan); `dirty:true` sebelum rekalkulasi pertama atau setelah operasi sebagian gagal (Step 10a). Objek yang sama ada di `GET /auth/me` `user.quota` | |

**Files (`/api/v1/files`)** — semua **S+P**, menerima `share=` dan `as_user=` sesuai §6.1.

| Method & path | Request | Response | Error utama |
| --- | --- | --- | --- |
| `GET /files/list?path&sort&dir&sort2&dir2&cursor&limit&foldersFirst&filter&hidden` | `sort ∈ name|size|type|ext|mtime|btime|atime|mode|note|dimensions|duration` | `200 {items:[Item], nextCursor, total, dir:{path, etag, mtime, itemCount}, parentPath}` | `NOT_FOUND`, `PATH_INVALID` |
| `GET /files/stat?path` | — | `200 {Item + stat:{mode,modeOctal,atime,mtime,ctime,btime,nlink}, counts? (folder: dihitung terpisah), checksums? (cache 10 menit, Step 10a), dirSize? (cache `files/dir-size`), versionsCount, shares:[…], note, tag, favorite, pinned}` | |
| `GET /files/media-info?path&include_gps?` | — | `200 {path, name, size, mtime, category, mime, kind:"image|svg|video|audio|pdf|other", width?, height?, durationMs?, codecVideo?, codecAudio?, bitrate?, hasCover, tags?, thumbState?, exif:{make, model, lens, software, dateTimeOriginal, dateTime, exposureTime, fNumber, iso, focalLength, focalLength35, orientation, flash, width, height, hasGps}|null, gps?:{lat, lon, alt?}}` — baris `media_metadata` (diprobe bila belum ada) + subset EXIF gambar raster (Step 17a); `gps` hanya dikirim dengan `include_gps=1` (`exif.hasGps` memberi tahu ada/tidak); symlink ke file diikuti seperti `/files/raw`; tipe non-media → `kind:"other"` tanpa probe; video → `subtitles:[{path, name, lang?, label, format:"vtt|srt", url}]` (sidecar di folder yang sama, `url` = signed `/files/subtitle`; dihilangkan bila tidak ada; Step 17b) | `VALIDATION_FAILED not_a_file`, `NOT_FOUND`, `PATH_INVALID` |
| `GET /files/subtitle?path&purpose=media&scope&exp&sig` | **Sig** | `.srt`/`.vtt` milik user penanda tangan (≤ 2 MiB) → `text/vtt; charset=utf-8` tersanitasi (Step 17b; lihat §8.14 VideoPlayer); header konten §8.9 + `Cache-Control: private, max-age=300` | `SIGNATURE_*`, `400 not_a_subtitle`, `404`, `413 FILE_TOO_LARGE` |
| `POST /files/mkdir` | `{path}` (path lengkap folder baru) **atau** `{path: folder induk, name}`, `parents?:bool` | `201 {item, warnings:[]}` (`warnings` dari §5.2, mis. `windows_incompatible`) | `ALREADY_EXISTS` (`details.existing`), `NAME_INVALID`, `OPERATION_IN_PROGRESS` |
| `POST /files/create` | `{path}` atau `{path, name}`, `template?: "txt|md|html|css|js|ts|json|php|py|sh|yaml"`, `content?` (≤ 512 KB; menang atas template) | `201 {item, warnings:[]}` | idem, `PAYLOAD_TOO_LARGE`, `UNSUPPORTED_TYPE` (`details.ext`, ekstensi di `BLOCKED_EXTENSIONS`) |
| `GET /files/templates` | — | `200 {templates:["css","html",…]}` (nama = ekstensi; daftar statis dari `files.TemplateNames()`, dipakai dialog "Baru → Dari template", Step 9b) | |
| `POST /files/rename` | `{path, newName}` (alias `new_name`) | `200 {item, results:[ItemResult], undoToken, undo:{kind:"rename", path:newPath, newName:oldName}, warnings}` | `ALREADY_EXISTS` (`details.existing/incoming`), `OPERATION_IN_PROGRESS`, `UNSUPPORTED_TYPE` |
| `POST /files/batch-rename/preview` | `{paths:[… ≤ 200], rule:{find?, replace?, regex?:bool, case_insensitive?:bool, numbering?:{pattern:"foto-{n}", start:1, step:1, pad:3}, case?: "upper|lower|title|none", ext_mode?: "keep|include"}}` | `200 {items:[{from, to, status:"ok|collision|invalid", reason?}], clean:bool}` — `to` = path tujuan lengkap untuk baris valid (nama saja bila `invalid`); klien menampilkan `basename(to)` (Step 9b) | `VALIDATION_FAILED` (rule), `PAYLOAD_TOO_LARGE` (> 200) |
| `POST /files/batch-rename` | sama (server menghitung ulang preview) | `200 {results, items, batchId, undoToken, undo:{kind:"batch_rename", items:[{from,to}]}}` | `CONFLICT` (`details.reason=preview_not_clean`, `details.items`), `ALREADY_EXISTS` (tujuan muncul setelah preview) |
| `POST /files/copy` | `{paths:[…] (alias items), to (alias dest), onConflict?: "fail|overwrite|skip|rename" (alias on_conflict; `ask` = fail), perItem?:{path:policy}}` | `200 {results:[{path,to,status:"ok|skipped|overwritten|renamed|merged",isDir}], undoToken?, undo:{kind:"delete_created", created}|null}` atau `202 {task, results:[], undo:null}` bila > 2.000 entri / > 200 MB atau (move) lintas filesystem — operasi berjalan sebagai task `copy|move` (Step 11; `413 task_required` hanya jika engine tidak dipasang) | `ALREADY_EXISTS` (policy fail: `details.existing/incoming/conflicts[]`), `QUOTA_EXCEEDED` (10a), `VALIDATION_FAILED` (`cannot_move_into_self`, `not_a_directory`, `type_mismatch`) |
| `POST /files/move` | idem | idem + `undo:{kind:"move", items:[{from:newPath, to:oldPath}]}` (hanya item `ok|renamed`) | |
| `POST /files/duplicate` | `{path}` → `nama (1).ext` | `201 {item, results, undoToken, undo:{kind:"delete_created"}}` | `PAYLOAD_TOO_LARGE` (task_required) |
| `POST /files/symlink` | `{path (link), target (relatif)}` | `201 {item, results, undoToken, undo:{kind:"delete_created"}}` (undo menghapus link) | `SYMLINK_ESCAPE`, `VALIDATION_FAILED target_absolute`, `ALREADY_EXISTS` |
| `POST /files/chmod` | `{paths, mode:"0644", recursive?:bool, dirMode?:"0755"}` | `200 {results, undo:null}`; rekursif sinkron ≤ 2.000 entri, lebih → `413 task_required` (task di Step 11); symlink dilewati (`status:"skipped"`) | |
| `POST /files/delete` | `{paths}` → Recycle Bin | `200 {results:[{path,status,isDir,trashId}], batchId, undoToken, undo:{kind:"restore", batchId, trash:[{id,path}]}}` / `202` (Step 11) | `OPERATION_IN_PROGRESS` |
| `POST /files/delete-permanent` | `{paths, confirm:"HAPUS" jika > 50}` | `200 {results, batchId, deleted, bytesFreed, undo:null}` sinkron (Step 10a; `202 {task}` untuk batch besar menyusul Step 11). `RemoveAll` + semua referensi/versi, kuota −size, audit `file.delete_permanent` | `VALIDATION_FAILED` (`fields.confirm=required`, `details.word`) |
| `POST /files/undo` | `{token}` (alias `undoToken`/`undo_token`; token acak sekali pakai dari response sebelumnya, umur 60 s, in-memory per user) | `200 {results, undo:null}` | `NOT_FOUND` (`undo_expired`: token tidak dikenal/kedaluwarsa/milik user lain), `CONFLICT` (`undo_conflict`) jika state berubah |
| `GET /files/content?path&encoding?&tail?` | — | `200 {path, content, encoding, detectedEncoding, bom, lossy, eol:"lf|crlf|mixed", language, size, mtime, etag, mode, lines, truncated, readOnly, offset?, tailLines?}` (header `ETag`); file > `EDITOR_MAX_FILE_SIZE` tanpa `tail` → `FILE_TOO_LARGE`; `tail=N` (atau `tail=true` = 1.000; alias `tail_lines`) → N baris terakhir read-only (`truncated:true`, `offset` = byte awal baris pertama, `tailLines`); "muat lebih banyak" = `tail` lebih besar. Read lock. | `BINARY_FILE`, `VALIDATION_FAILED` (`not_a_file`, `encoding` tak dikenal), `OPERATION_IN_PROGRESS` |
| `PUT /files/content` | header `If-Match` (`*`/`force` melewati cek); `{path, content, encoding?:"utf-8", bom?:false, eol?:"keep|lf|crlf", force?:bool, create?:bool}`; body dikecualikan dari batas JSON 1 MiB (batas = 4 × `EDITOR_MAX_FILE_SIZE` + 64 KB, `content` ≤ `EDITOR_MAX_FILE_SIZE`) | `200 {etag, mtime, size, version:{no,id}|null, noop, created}` (header `ETag`); byte identik → `noop:true` tanpa tulis/versi | `CONFLICT` (`etag_mismatch`: `details.serverEtag, serverMtime, serverSize`), `VALIDATION_FAILED` (`fields.content=unencodable` + `line/col/char`, `eol`, `encoding`, `not_a_file`), `NOT_FOUND` (tanpa `create`), `UNSUPPORTED_TYPE` (ekstensi diblokir saat `create`), `FILE_TOO_LARGE`, `QUOTA_EXCEEDED`, `OPERATION_IN_PROGRESS` |
| `POST /files/sign` | `{path, purpose:"download|media|thumb|inline", share?, size? (thumb)}`, batch `{paths:[…≤500], purpose, size}`, atau `{items:[{path, purpose}…≤500]}` (purpose per item; `zip` lewat `POST /files/zip`) | `200 {url, expiresAt, purpose}` / batch `{urls:[{path,url,purpose,expiresAt}], expiresAt (paling awal), invalid:[…]}`; TTL `download|inline` = `SIGNED_URL_TTL`, `media|thumb` = `MEDIA_URL_TTL` | `VALIDATION_FAILED` (purpose/size/kombinasi path), `share` → `not_implemented` sampai 18a |
| `GET /files/raw?path&purpose=media|inline&exp&sig&scope&variant?` | **Sig** | stream (Range) — kebijakan inline/attachment §8.9; `variant=thumb` → thumbnail 512 px (202 saat dirender), `variant=preview` → WebP 2048 px gambar raster/SVG (dirender saat diminta; jenis lain jatuh ke 512 px); tidak dihitung ke batas paralel | `SIGNATURE_*`, `403 FORBIDDEN reason:account_inactive`, `400` folder/variant |
| `GET /files/download?path&purpose=download&exp&sig&scope` | **Sig** | stream `attachment` selalu; `HEAD`, Range, ETag, `If-Range`/`If-None-Match`; dihitung ke batas 3 paralel | `SIGNATURE_*`, `403 FORBIDDEN` akun nonaktif, `429 RATE_LIMITED reason:concurrent_downloads` + `Retry-After: 5` |
| `POST /files/zip` | `{paths:[…≤2000], name?, method:"store|deflate"}` (root bukan entri) | `200 {url, expiresAt, name, count}` — payload di `DATA_DIR/tmp/zip-<id>.json` 10 menit | `VALIDATION_FAILED` |
| `GET /files/zip?token&purpose=zip&exp&sig&scope` | **Sig** | `application/zip` streaming, `Accept-Ranges: none`, tanpa `Content-Length`; read lock sumber | `404 reason:zip_expired` (payload hilang/kedaluwarsa/milik user lain), `409 OPERATION_IN_PROGRESS`, `429` |
| `POST /files/hls` | `{path}` (file video milik user; tanpa `?share=`/`?as_user=` → `400`) (Step 23d) | `202 {task, playlistUrl, expiresAt, ready}` task `transcode_hls` baru · `200` (sama) bila konversi file yang sama (path + ukuran + mtime) sedang queued/running atau sudah selesai dan foldernya masih ada — dipakai juga untuk tanda tangan ulang; `playlistUrl` = `/api/v1/files/hls/<task>/playlist.m3u8?scope&exp&sig` (purpose `hls`, TTL `MEDIA_URL_TTL`); `ready` = ≥ 2 segmen ada atau selesai | `403 FORBIDDEN reason=transcode_disabled`, `501 DEPENDENCY_MISSING` (ffmpeg/ffprobe), `400 not_a_file`, `415 UNSUPPORTED_TYPE reason=not_video`, `422 VALIDATION_FAILED reason=too_long|unknown_duration`, `409 CONFLICT reason=already_running|disk_budget`, `413 QUOTA_EXCEEDED reason=disk`, `429 TASK_LIMIT` |
| `GET /files/hls/{id}/playlist.m3u8?scope&exp&sig` | **Sig** | playlist `application/vnd.apple.mpegurl` dari `DATA_DIR/tmp/hls/<id>/`, ditulis ulang: setiap URI segmen diberi query tanda tangan yang sama, `#EXT-X-START:TIME-OFFSET=0` disisipkan (mulai dari awal, bukan live edge); `Cache-Control: private, no-store` selama ffmpeg berjalan, `private, max-age=3600` setelah selesai; header konten §8.9 (`nosniff`, CSP sandbox); `202` kosong + `Retry-After: 2` sebelum playlist pertama ditulis | `SIGNATURE_INVALID|EXPIRED` (403), `403 account_inactive`, `403 reason=transcode_disabled`, `404` id tidak dikenal / milik user lain / task bukan `transcode_hls` / dibatalkan / sudah dibersihkan |
| `GET /files/hls/{id}/{segment}?scope&exp&sig` | **Sig** | `segment` hanya `seg_NNNNN.ts` (regex ketat; selain itu `404`, tidak ada listing folder) → `video/mp2t`, Range (`206`), `ETag`, `Cache-Control: private, max-age=3600`, header §8.9 | idem |
| `GET /files/thumb?path&size=128|256|512&exp&sig&scope` | **Sig** | `image/webp` + `ETag` + `Cache-Control: private, max-age=86400`; `202` kosong + `Retry-After: 2` jika belum siap (client retry maks 3×); `404` jika unsupported | |
| `POST /files/dir-size` | `{path}` | `200 {path, size, bytes, files, dirs, partial, calculatedAt, cached}` sinkron dengan budget walk 60 s (`partial:true` bila terpotong; walk yang terpotong dijawab `202 {task, …angka parsial}` dan task `dir_size` menyelesaikannya; `file.changed` dipancarkan saat selesai, Step 11). Hasil di-cache 10 menit per path, dibatalkan oleh mutasi di bawahnya, dipakai kolom Ukuran dan `files/stat.dirSize` | `VALIDATION_FAILED not_a_directory` |
| `POST /files/checksum` | `{path, algos?:["md5","sha1","sha256"], verify?:{algo, expected}}` | `200 {path, md5?, sha1?, sha256?, verified?, actual?}` untuk file ≤ 100 MB (digest di-cache 10 menit per size+mtime dan muncul sebagai `files/stat.checksums`); lebih besar → `202 {task, path, size}` (task `checksum`, Step 11; digest yang sudah di-cache tetap dijawab `200`) | `VALIDATION_FAILED not_a_file|bad_algo` |
| `POST /files/checksum/verify` | `{path, algo, expected}` (hex, tidak peka huruf besar/kecil) | `200 {path, algo, match, actual}` | idem |
| `GET /files/archive-entries` → lihat Archives | | | |

`Item` = `{name, path, isDir, isSymlink, symlinkTarget?, broken?, size, itemCount? (folder, jika < 10k atau sudah dihitung), mime, typeLabel, ext, mtime, btime?, atime, mode:"rwxr-xr-x", modeOctal:"755", note?, tag?, favorite, pinned, shared:"none|public|users|both", media?:{width,height,durationMs}, thumb:bool, etag}`.

**Versions (`/api/v1/versions`)** — S+P

| Method & path | Response |
| --- | --- |
| `GET /versions?path` | `200 {items:[{id, no, size, sha256, reason, createdAt}], limit}` |
| `GET /versions/{id}/content?encoding?` | `200 {content…}` (read-only, format sama dengan `files/content`) |
| `GET /versions/{id}/download` | signed redirect → stream |
| `POST /versions/{id}/restore` | `{force?}` → `200 {etag, version}` (membuat versi baru dari isi sekarang, reason `restore`) |
| `DELETE /versions/{id}` · `DELETE /versions?path` | `204` |

**Trash (`/api/v1/trash`)** — S+P

| Method & path | Request | Response |
| --- | --- | --- |
| `GET /trash?cursor&limit&kind&q&sort&dir` | `kind ∈ file|folder(dir)|image|document|video|audio|archive|other`, `q` substring nama, `sort ∈ deleted(default, desc)|name|size|expires|path`, `limit ≤ 1000` (default 500) | `200 {items:[{id, name, originalPath, isDir, size, itemCount, kind, deletedAt, expiresAt, batchId?}], total, totalBytes, nextCursor, retentionDays}` — baris aktif user dimuat sekali (maks 20.000) lalu difilter/di-sort (natural sort) dan dipaging di memori; cursor = offset opak (Step 10a) |
| `POST /trash/restore` | `{ids, to?: folder tujuan (alias dest; kosong = lokasi asal), onConflict?: fail|overwrite|skip|rename (alias on_conflict/policy), perItem?:{<trashId>: policy}}` | `200 {results:[{id, path (asal), to, status: ok|renamed|overwritten|skipped, isDir, size, overwrittenId?}], reactivateShares:[] (18a), undo:null}`; semua konflik dikumpulkan dulu → `409 ALREADY_EXISTS details.conflicts[]` (`source` = trash id) sebelum ada yang dipindah; `overwrite` memindahkan item yang ada ke Recycle Bin lebih dulu (`overwrittenId`); id asing/user lain → `404 NOT_FOUND` |
| `POST /trash/purge` | `{ids}` | `200 {results:[{id, path, size, isDir, status: ok|gone}], deleted, bytesFreed, undo:null}` sinkron (Step 11: `202` untuk batch besar); id asing → `gone`, bukan error; audit `file.delete_permanent` |
| `POST /trash/empty` | `{confirm:"HAPUS"}` (wajib) | `200 {results:[], deleted, bytesFreed, undo:null}`; audit `trash.empty` |
| `GET /trash/{id}/download` | | signed URL ke isi item (untuk melihat sebelum restore) |

**Meta (`/api/v1/meta`)** — S+P

| Method & path | Request | Response |
| --- | --- | --- |
| `GET /meta/favorites` · `PUT /meta/favorites` · `DELETE /meta/favorites` | `{paths}` (atau alias `{path}`; maks 2000; root ditolak) | `200 {items:[Item+broken], total}` (folder dulu, natural) · `200 {paths, count}` · `204` (referensi rusak boleh dihapus tanpa cek jail) |
| `GET /meta/pins?dir` · `PUT /meta/pins` · `DELETE /meta/pins` | `{paths}` | `200 {items, dir, max:50}` · `200 {paths, count}` (> 50 per folder → `400 VALIDATION_FAILED reason=pin_limit`) · `204` |
| `PUT /meta/notes` · `DELETE /meta/notes` | `{path, note}` (≤ 500 karakter; kosong = hapus) · `{paths}` | `200 {path, note, max}` (> 500 → `400 VALIDATION_FAILED reason=note_too_long`) · `204` |
| `PUT /meta/tags` · `DELETE /meta/tags` | `{paths, color}` | `200 {paths, count, color}` · `204` |
| `GET /meta/tagged?color` | | `200 {items:[Item+broken], total, color}` |
| `GET /files/list?tag=<color>` | | filter server-side item ber-tag (Step 10b) |

**Tasks & events**

| Method & path | Auth | Response |
| --- | --- | --- |
| `GET /tasks?state&cursor&limit` | S+P | `200 {items:[Task]}`; `Task = {id,type,state,title,progress:{done,total,unit,percent},etaSeconds,speedBps,error?,result?,createdAt,startedAt,finishedAt,cancelable}` |
| `GET /tasks/{id}` · `GET /tasks/{id}/logs?after` | S+P | |
| `POST /tasks/{id}/cancel` · `POST /tasks/{id}/retry` | S+P | `200 {task}`; retry hanya untuk `failed|interrupted|cancelled` yang `payload` tidak butuh rahasia |
| `DELETE /tasks/{id}` | S+P | hapus dari daftar (state final saja) |
| `GET /events?lastEventId` | S+P | `text/event-stream` (§6.4) |
| `GET /notifications?cursor&unread` · `POST /notifications/read` `{ids|all}` · `DELETE /notifications/{id}` | S+P | |

**Upload**

| Method & path | Auth | Catatan |
| --- | --- | --- |
| `POST /upload/precheck` | S+P (atau **Shr** untuk file request, 18a) | `{path, files:[{name, size, relativePath? (folder di bawah path, "a/b/c")}]}` (≤ 1000 file/panggilan; klien memecah per 200) → `{items:[{name, relativePath?, path, status:"ok|conflict|blocked_ext|too_large|name_invalid", reason?, existing?:{size, mtime, isDir}}], quotaOk, quotaReason?:"quota|disk", needed, free, limit, maxUploadSize, chunkSize}`. `free` sudah dikurangi reservasi upload aktif; tidak ada yang direservasi oleh precheck. Folder tujuan tidak ada → `404 NOT_FOUND`. (Step 12a: bentuk camelCase per item menggantikan `{ok, conflicts, rejected}`.) |
| `POST /upload/tus/` · `HEAD|PATCH|DELETE /upload/tus/{id}` · `OPTIONS` | S+P / Shr | Protokol tus 1.0.0 core + creation + termination + expiration (checksum tidak diaktifkan). `Upload-Metadata` base64 (kunci kanonis Step 12a): `filename`, `path`, `relativePath`, `onConflict` (`overwrite|skip|rename`, default `rename`), `lastModified` (epoch ms); alias `name`, `dest`, `relPath`, `policy` diterima; `share` (18a). `Upload-Defer-Length` dan concatenation ditolak. `HEAD/PATCH/DELETE` pada id milik user lain, atau sesi yang sudah final/kedaluwarsa → `404 NOT_FOUND`. PATCH > `UPLOAD_CHUNK_SIZE` → `413 PAYLOAD_TOO_LARGE reason chunk_too_large`. Respons PATCH terakhir membawa `Upload-Result: created|renamed|overwritten|skipped` dan `Upload-Path` (path virtual ter-percent-encode). |
| `POST /upload/clipboard` | — | **Tidak diimplementasikan (Step 12a):** paste gambar dari clipboard memakai jalur tus yang sama dengan nama `image-YYYYMMDD-HHMMSS.<ext>` (PROMPT Step 12a), sehingga tidak perlu endpoint multipart terpisah. Baris ini dipertahankan sebagai catatan; dihapus bila 18a tidak membutuhkannya. |

**Remote download (`/api/v1/remote-downloads`)** — S+P (+ CSRF): `POST {urls:[…] (1..10), path (alias dest), filename? (satu URL), filenames?:{url:name}, onConflict (alias policy, on_conflict; default fail)}` → `202 {tasks:[…]}` (satu task `remote_download` per URL; `422 SSRF_BLOCKED`, `403 FORBIDDEN reason:disabled|denylist|allowlist`, `404` folder tujuan, `415 UNSUPPORTED_TYPE` nama berekstensi diblokir, `429 RATE_LIMITED` + `Retry-After`; `details.index` menunjuk URL yang ditolak); `POST /remote-downloads/probe {url}` → `200 {finalUrl, size|null, filename?, acceptsRanges, contentType?}` (mengikuti aturan SSRF yang sama, HEAD lalu GET Range 0-0; `502 REMOTE_UPSTREAM|REMOTE_NETWORK`).

**Archives (`/api/v1/archives`)** — S+P

| Method & path | Request | Response |
| --- | --- | --- |
| `POST /archives/compress` | `{paths (1..1000), format:"zip|7z|tar|tar.gz|tar.bz2|tar.xz|tar.zst|gz|bz2|xz|zst|jar|war" (+alias tgz/tbz2/txz/tzst), name? (default `<item>.<ext>` / `<folder induk>.<ext>` / `Arsip-<tanggal>.<ext>`; ekstensi ditambahkan bila hilang), destPath? (alias dest_path/dest; default folder sumber pertama), level:"store|fast|normal|max", password? (zip AES-256, 7z), encryptNames?:bool (7z + password; alias encrypt_names), splitVolumeMb?:int (7z; alias split_volume_mb), deleteSource?:bool (alias delete_source → Recycle Bin)}` | `202 {task}` (`400` validasi, `404` sumber/tujuan, `415` format tak bisa ditulis / ekstensi diblokir, `501 DEPENDENCY_MISSING` 7z tanpa binary) |
| `POST /archives/extract` | `{path (alias archive), dest:"here|new|<path>" (alias dest_path; default new), password?, encoding:"auto|utf-8|gbk|cp437|shift_jis|big5|windows-1252" (alias name_encoding), onConflict:"fail|overwrite|skip|rename" (alias on_conflict/policy; default fail), entries?:[nama atau prefix folder] (extract sebagian, ≤ 1000)}` | `202 {task}` (`415 UNSUPPORTED_TYPE reason:unknown_format` bila magic bytes tidak dikenali) |
| `GET /archives/entries?path&inner=&encoding=auto&cursor&limit≤1000` + header `X-Archive-Password` (password di query → `400`) | | `200 {archive, inner, format, items:[{path (dalam arsip), name, isDir, isSymlink?, size, compressedSize (−1 bila tak tercatat), mtime, encrypted, method?, implicit?, itemCount}], nextCursor?, total (folder ini), totalEntries, size, encrypted, encoding?, supportsModify, modifyReason?:"format|encrypted|too_large_to_modify", session? (id sesi arsip saat password diterima), unsafe?}`; indeks di-cache per (user, path, size, mtime, encoding) 10 menit; arsip terenkripsi tetap terdaftar tanpa password (entri ditandai `encrypted`), password salah → `422 ARCHIVE_PASSWORD_WRONG`; `404` folder dalam arsip tidak ada; `415` bukan arsip; `501` iso tanpa 7-Zip |
| `POST /archives/sign` | `{path (alias archive), inner (alias entry), session?}` | `200 {url, expiresAt, purpose:"entry", path, inner}` — URL `entry-content` bertanda tangan (purpose `entry`, TTL `MEDIA_URL_TTL`); `session` ditempel sebagai `&sess=` (bearer terikat user + arsip, tidak ikut ditandatangani); sesi tak dikenal/kedaluwarsa/user lain → `404 reason:session_expired` |
| `GET|HEAD /archives/entry-content?path&inner&purpose=entry&scope&exp&sig[&sess]` | Sig | streaming satu entri langsung dari arsip (tanpa file di `tmp`), ≤ 50 MB (`413 FILE_TOO_LARGE`), kebijakan inline `/files/raw` (ADR-06) dengan MIME dari magic bytes, `Cache-Control: private, max-age=300`, tanpa Range; entri terenkripsi tanpa `sess` → `401 ARCHIVE_PASSWORD_REQUIRED`; iso → `415 reason:cli_only`; dihitung pada batas download paralel (`429`). Menggantikan `entry-download` (unduh = URL yang sama lewat `<a download>`) |
| `POST /archives/extract-entries` | `{path (alias archive), entries:[path dalam arsip; folder mencakup isinya] (1..1000), dest:"here|new|<path>" (alias destPath/dest_path; default new), password?, encoding? (alias name_encoding), onConflict? (alias on_conflict/policy)}` | `202 {task}` — task `extract` dengan filter `entries`; nama yang tidak ada di indeks → `404 reason:entry_not_found details.entries` tanpa task; `401 ARCHIVE_PASSWORD_REQUIRED` |
| `POST /archives/add` | `{path (alias archive), sources:[file/folder di root user] (1..1000; alias paths), innerDir? (alias inner_dir/into; "" = root)}` (zip tanpa enkripsi ≤ 2 GiB) | `202 {task}` — task `zip_edit` mode `add`; `415 UNSUPPORTED_TYPE reason:format|encrypted|too_large_to_modify|blocked_extension`, `400 reason:into_itself`, `404` sumber |
| `POST /archives/remove-entries` (alias `/archives/remove-entry`, menerima `entry` tunggal) | `{path (alias archive), entries:[…] (1..1000; folder menghapus isinya)}` (zip saja) | `202 {task}` — task `zip_edit` mode `remove`; entri tidak ada → `404` |

**Search (`/api/v1/search`)** — S+P

| Method & path | Request | Response |
| --- | --- | --- |
| `GET /search/names?q&path&recursive&types&minSize&maxSize&from&to&ext&tag&cursor&limit` | | `200 {items:[Item+location], truncated, tookMs}` (maks 5.000, 20 s) |
| `POST /search/content` | `{q, path, regex, caseSensitive, wholeWord, recursive, ext:[…], minSize, maxSize, from, to, maxMatches:2000}` | `200 application/x-ndjson` baris `{file, line, col, text, before, after}` lalu baris akhir `{done:true, files, matches, truncated, engine:"rg|go"}`; dibatalkan jika client putus |
| `POST /search/replace/preview` | `{… sama + replace}` | `202 {task}`; result `{files:[{path, hunks:[{line, before, after}]}], totalReplacements}` (preview disimpan 10 menit) |
| `POST /search/replace` | `{previewId, files?:[path] (subset)}` | `202 {task}`; setiap file dibuat versi `replace_in_files` sebelum ditulis; result `{files, replacements, versionIds}` |
| `GET /search/history` · `DELETE /search/history` | | |

**Shares (`/api/v1/shares`)** — S+P

| Method & path | Request | Response |
| --- | --- | --- |
| `POST /shares` | `{path, mode:"public|users", slug?, password?, expiresAt?|expiresInDays?, maxDownloads?, allowDownload, allowUpload, hideOwner, note, recipients?:[{username, permission, note?}], allowReshare?}` | `201 {share, url? (hanya sekali membawa token asli)}` |
| `GET /shares?cursor&mode&status` | | `200 {items:[{share, stats:{views, downloads, lastAccessAt}, recipients}]}` |
| `GET /shares/{id}` · `PATCH /shares/{id}` · `DELETE /shares/{id}` | PATCH field sama; `password:null` menghapus; `disabled:bool` | |
| `POST /shares/{id}/regenerate-token` | | `200 {url}` (token lama tidak berlaku) |
| `GET /shares/{id}/access-logs?cursor` | | `200 {items:[{at, ipTrunc, action, path?, userId?}]}` |
| `GET /shares/{id}/qr` | | `image/png` QR dari URL (perlu library QR Go, verifikasi) |
| `GET /shares/by-path?path` | | share aktif untuk item (badge) |
| `GET /shares/user-suggest?q` | ≥ 2 karakter, maks 8 hasil `{username, displayName}` hanya user aktif, tidak termasuk diri sendiri | |
| `GET /shared-with-me` | | `200 {groups:[{owner:{username, displayName}, items:[{shareId, name, isDir, permission, note, expiresAt, allowReshare, receivedAt, status, reason?}]}]}` — tanpa cursor (jumlah share per penerima kecil); `status` active/disabled/expired/broken + `reason`; favorit/pin item share ditunda (lihat Deviasi 18b) |
| `GET /shared-with-me/{shareId}` | | header untuk browser penerima: `{shareId, name, isDir, permission, note, expiresAt, allowReshare, allowDownload, owner:{username, displayName}}` — tanpa path pemilik |
| `DELETE /shared-with-me/{shareId}` | penerima keluar dari share | `204` |
| `POST /upload/share/{shareId}/precheck` · `POST /upload/share/{shareId}/tus[/]` · `HEAD/PATCH/DELETE …/tus/{id}` | editor share: unggah sebagai pemilik ke folder share (`path` metadata relatif ke share, policy klien dihormati, kuota pemilik, pemilik mendapat notifikasi `share_upload` bernama pengunggah) | seperti tus biasa; id upload share lain → `404` |

**Publik (`/api/v1/public/shares/{token}`)** — **Pub/Shr**; `{token}` = token atau slug.

| Method & path | Response |
| --- | --- |
| `GET …` | `200 {name, isDir, size, owner? (jika !hideOwner), note, allowDownload, allowUpload, expiresAt, passwordRequired, unlocked}`; `410 SHARE_*` |
| `POST …/unlock {password}` | `204` + `Set-Cookie fm_share_<id>` (`HttpOnly; Secure; SameSite=Lax; Path=/s/<token>; Max-Age=3600`; juga diterima di `/api/v1/public/shares/<token>` lewat `Path` kedua) |
| `GET …/list?path&sort&dir&cursor` | listing read-only (folder) |
| `GET …/stat?path` · `GET …/raw?path&…` · `GET …/download?path` · `POST …/zip {paths}` → `GET …/zip?token` · `GET …/thumb?path&size` | seperti files, tanpa signed URL (otorisasi = token + cookie); rate limit §10.5 |
| `POST …/upload/precheck` · `…/upload/tus/` | hanya jika `allowUpload`; dest = folder share; dihitung ke kuota pemilik; nama konflik → selalu `rename` |
| `GET …/content?path` | preview teks read-only ≤ 1 MB |
| `GET …/media-info?path` · `GET …/subtitle?path` | (Step 18a) metadata viewer untuk tamu **tanpa GPS** dan subtitle sidecar sebagai WebVTT dengan URL publik (`subtitles[].url` di `media-info`) — agar viewer 17a/17b bekerja di halaman publik tanpa signed URL |

Halaman `GET /s/<token>[/sub/path]` (bukan API) dilayani SPA (route `/s/:token/*`) dengan header `X-Robots-Tag: noindex, nofollow`, `Referrer-Policy: no-referrer`, dan meta `robots noindex`.

**Admin (`/api/v1/admin`)** — **A** (dan S+P)

| Method & path | Request | Response |
| --- | --- | --- |
| `GET /admin/dashboard` | | `{users:{active,disabled}, storage:{usedBytes,quotaSumBytes,disk:{total,free}}, topUsers:[{username,used,quota}], tasks:{running,queued}, publicShares, failedLogins24h, version:{version,commit,buildDate,go}, deps:{ffmpeg:{ok,path,version?},…}, warnings:[…]}` |
| `GET /admin/users?cursor&q&role&status&limit` | | `{items:[{id, username, displayName, role, status, quota:{limit,used,trash,versions,dirty}, lastLoginAt, homeProvisioned, homeProvisionedAt, homeWarning, createdAt, updatedAt, mustChangePassword, totpEnabled, lockedUntil, createdBy}], nextCursor, total}` — urut username, cursor keyset (Step 19a) |
| `GET /admin/users/{id}` | | `{user}` (Step 19a) |
| `POST /admin/users` | `{username, displayName, role, quotaBytes?, temporaryPassword?}` | `201 {user, temporaryPassword, warnings:[home_exists?]}` (password hanya sekali; username diterima apa adanya — huruf besar ditolak `400`, bukan dilipat) |
| `PATCH /admin/users/{id}` | `{displayName?, role?, quotaBytes?, status?}` | `200 {user}`; `VALIDATION_FAILED last_admin` jika melanggar aturan admin terakhir |
| `POST /admin/users/{id}/reset-password` | | `200 {temporaryPassword}`; `must_change_password=1`; semua sesi dicabut |
| `POST /admin/users/{id}/revoke-sessions` | | `204` |
| `POST /admin/users/{id}/reset-totp` | | `204` — menghapus secret TOTP, kode pemulihan, `totp_last_step`, lalu mencabut semua sesi user itu (user login berikutnya tanpa kode); audit `user.reset_totp` (Step 23a) |
| `POST /admin/users/{id}/disable` · `POST /admin/users/{id}/enable` | | `200 {user}` — pintasan `PATCH status`; disable mencabut sesi dan mematikan link publik pemilik (`410 owner_disabled`); menonaktifkan diri sendiri `409 reason self` (Step 19a) |
| `DELETE /admin/users/{id}` | `{confirmUsername, mode:"purge|archive"}` | `202 {task}` task `delete_user` milik admin pemanggil (tampil di panel task-nya); akun soft-delete + sesi dicabut seketika; `archive` → `.system/deleted-users/<username>-<yyyyMMdd-HHmmss>` (+ `__trash`, `__versions`), baris DB dihapus; `purge` → folder/trash/versions/thumbs + baris per tabel dihapus, baris `users` disimpan 30 hari (job `purge_deleted_users` 04:15) |
| `GET /admin/settings` · `PUT /admin/settings` | `{key: value}` subset yang boleh | `{items:[{key, group, type, value, default, envValue?, source:"default|env|override", readOnly?, validation:{min,max,maxLen,options}, updatedAt?, updatedBy?}]}`; PUT memvalidasi per kunci + relasi §3.4 butir 7, satu kunci salah → seluruh batch `400 VALIDATION_FAILED` dengan `details.key`/`reason`; berlaku seketika lewat snapshot atomik `config.Runtime()` |
| `POST /admin/settings/reset` | `{keys}` | kembali ke env |
| `POST /admin/branding/logo` · `DELETE /admin/branding/logo` | multipart field `logo`: PNG (magic bytes) ≤ 512 KB | `200 {logoUrl}` / `204`; `GET /api/v1/branding/logo` (publik) menyajikan `image/png` atau 404 |
| `GET /admin/audit?cursor&actor&action&from&to&q&limit` | | `{items:[{id, at, actorUserId, actorUsername, action, targetType, targetId, ip, userAgent, details}], nextCursor?}` urut `(at, id)` turun, cursor `<at>|<id>`; `actor` = username; `action` persis atau prefiks (`auth.` / `auth.*`); `from`/`to` RFC 3339 atau `YYYY-MM-DD` (zona `TZ_DEFAULT`, `to` tanggal = inklusif); `q` substring (actor/target/IP/details, tanpa beda huruf); limit ≤ 500 (Step 19b) |
| `GET /admin/audit/export.csv?…` (filter sama) | | `200 text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="audit-<ts>.csv"`; BOM UTF-8, RFC 4180 (CRLF), kolom `id,at,actor,actor_id,action,target_type,target_id,ip,user_agent,details`; sel diawali `= + - @ TAB CR` diberi prefiks `'` (injeksi formula); streaming per 1.000 baris, maks **1.000.000** baris; filter salah → `400` sebelum streaming |
| `GET /admin/tasks?state&user&cursor&limit` · `POST /admin/tasks/{id}/cancel` | | `{items:[task + userId?, username?], nextCursor?}` semua user + task sistem (`user=<username>`, `user=system` = hanya task sistem; task `hidden` tidak tampil); cancel → `200 {task}`, task final `409 CONFLICT reason=final`, audit `admin.task_cancel` (Step 19b) |
| `GET /admin/maintenance` | | `{timeZone, jobs:[{name, slug?, manual, schedule?:{name, schedule ("daily 03:30"|"weekly Sun 05:00"|"every 1h0m0s"), timeZone, nextRun, lastRun?, lastDurationMs?, lastError?, running}, lastTask?}], backups:[…]}` — 7 job maintenance (manual) + job terjadwal lain (read-only) (Step 19b) |
| `POST /admin/maintenance/{job}` | `job ∈ recalc-quota, clear-thumbs, clean-uploads, purge-trash, integrity-check, backup-db, prune-logs` (nama tipe task dengan `_` juga diterima); body opsional `{userId?}` (recalc-quota, clear-thumbs), `{force:true, confirm:"HAPUS"}` (purge-trash semua item), `{prune:true}` (clear-thumbs LRU), `{label}` (backup-db, `[a-z0-9-]{1,32}`, tidak dirotasi) | `202 {task}` task sistem (`user_id` NULL) bertipe §7.1, hasil JSON di `task.result`; job yang sama masih queued/running → `409 CONFLICT reason=already_running details.taskId`; job tak dikenal `404`; audit `maintenance.<job>` |
| `GET /admin/maintenance/backups` · `GET /admin/maintenance/backups/{name}` | | `{items:[{name, size, createdAt, label?}]}`; unduh dengan sesi admin (bukan signed URL), nama harus cocok `app-<yyyyMMdd-HHmmss>[-label].db` (lain → 404), audit `maintenance.backup_download` |
| `GET /admin/shares?cursor&owner&mode&status` · `POST /admin/shares/{id}/revoke` | | `{items:[{id, ownerId, ownerUsername, path, name, isDir, mode, hasPassword, expiresAt, maxDownloads, downloadCount, viewCount, recipientCount, allowUpload, status, disabledReason, lastAccessAt, createdAt}], nextCursor}` — metadata saja: tanpa token/link/slug/isi; `owner` = username. Revoke → `204` (idempoten), `disabled_reason=admin_revoked` (pemilik tidak bisa mengaktifkan lagi, 403), penerima mendapat `share_revoked`, pemilik `share_revoked_by_admin`, audit `share.admin_revoked`; id tak dikenal `404` (Step 19b) |

**Sistem**

| Path | Auth | Response |
| --- | --- | --- |
| `GET /healthz` | Pub via Nginx (rate limit 60/menit per IP, log level debug) | `200 {"status":"ok"}` tanpa detail lain; dipakai uptime monitor & smoke test deploy |
| `GET /readyz` | Loopback saja (tidak di-proxy Nginx) | `200 {db:ok, storage:ok, migrations:ok}` atau `503` dengan komponen yang gagal |
| `GET /metrics` | listener `METRICS_ADDR` | Prometheus text (request count/duration per route, tasks by state, sse clients, thumb queue, quota bytes per user; sejak Step 21 juga `go_heap_objects_bytes`, `go_heap_released_bytes`, `go_memory_total_bytes` dari `runtime/metrics` — dipakai `make perf` sebagai pengganti pprof, yang tidak pernah diekspos) |

### 6.4 SSE

`GET /api/v1/events` → `Content-Type: text/event-stream; charset=utf-8`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`, `Connection: keep-alive`. Server mengirim `retry: 3000` lalu event. Setiap event punya `id: <unix_ms>-<seq>`; client mengirim `Last-Event-ID` saat reconnect; broker menyimpan ring buffer 200 event per user (± 60 detik) untuk replay, jika id tidak ditemukan → event `resync` agar client me-refetch tasks dan notifications. Heartbeat komentar `: ping` tiap 25 detik. Maks 5 koneksi SSE per user (tab tambahan memakai `BroadcastChannel` untuk berbagi, fallback: koneksi ke-6 menutup yang tertua).

| event | data |
| --- | --- |
| `task.created` / `task.progress` / `task.done` / `task.failed` / `task.cancelled` | `Task` (progress di-throttle maks 2×/detik per task) |
| `notification` | `Notification` |
| `file.changed` | `{dir, reason:"upload|extract|move|delete|restore|remote|replace|editor", paths?:[…]}` → client invalidasi query listing `dir` (hanya dari aksi task/background; aksi sinkron user sendiri tidak perlu) |
| `quota.updated` | `{used, trash, versions, limit}` |
| `session.revoked` | `{}` → client redirect ke login |
| `resync` | `{}` |

### 6.5 tus

- Endpoint `/api/v1/upload/tus/` memakai `tusd` v2 `UnroutedHandler` (`BasePath` = `APP_BASE_URL + /api/v1/upload/tus/` sehingga `Location` absolut dan deterministik; `RespectForwardedHeaders: true`, `MaxSize` = `MAX_UPLOAD_SIZE`, `DisableDownload: true`, `DisableTermination: false`, `DisableConcatenation: true`, CORS tusd dimatikan karena origin sama, `memorylocker` untuk kunci per upload), store `filestore` di `DATA_DIR/uploads` (mode file 0640, folder 0750). Router chi memangkas prefix sebelum memanggil tusd (`http.StripPrefix`); route tus dikecualikan dari batas body JSON 1 MB dan timeout API 60 s (`MaxBytes(…, prefix)`, `TimeoutExcept(…, prefix)`), PATCH dibatasi `UPLOAD_CHUNK_SIZE` oleh middleware sendiri (`413`). Hook tusd menerima user lewat `upload.Principal` di context (diisi httpapi setelah RequireAuth/CSRF); logger tusd (masih `golang.org/x/exp/slog` di v2.10) dijembatani ke `log/slog` dan dibatasi level Warn. Saat dev, proxy Vite menulis ulang `Location` ke origin dev server.
- Middleware sebelum handler: sesi/CSRF (atau share cookie untuk file request). `PreUploadCreateCallback` (tusd v2: `func(HookEvent) (HTTPResponse, FileInfoChanges, error)`, terverifikasi di pkg.go.dev 14 Okt 2026): parse metadata, `fsjail.ValidateName`, `BLOCKED_EXTENSIONS`, reservasi kuota (`upload_sessions` active + size ≤ sisa), simpan baris `upload_sessions` dengan `expires_at = now+24h`, kembalikan `FileInfoChanges` untuk menyetel `ID` menjadi UUIDv7 kita.
- `PreFinishResponseCallback` (`func(HookEvent) (HTTPResponse, error)`): verifikasi `info.Size == info.Offset == upload_sessions.size` dan ukuran file di disk (tidak cocok → upload dibuang, `400 VALIDATION_FAILED size_mismatch`); folder tujuan harus masih ada (`404`, upload dibuang); lock `Write` pada path target (`SyncLockTimeout`; gagal → `423 OPERATION_IN_PROGRESS` agar klien tus mencoba lagi); `MkdirAll` untuk `relativePath`; nama akhir sesuai policy (`rename` → `files.UniqueName` "nama (1).ext"; `skip` → bytes dibuang, baris `finalized` dengan `finalized_path NULL`, kuota tidak berubah; `overwrite` → `MoveIn` ke nama sementara `.fm-upload-<id>` di folder tujuan lalu `Rename` menimpa secara atomik, delta kuota = size − ukuran lama; menimpa folder → `400 VALIDATION_FAILED type_mismatch`, upload dibuang); race `ErrExists` saat `MoveIn` → fallback nama unik; mtime dari `lastModified` bila masuk akal (≥ 1980, ≤ now+24 h); satu transaksi: `SetUploadSessionState(finalized)` + `quota.Apply` (gagal → log + `MarkDirty`); lalu `activity_logs` `upload`, hook thumbnail, invalidasi listing folder tujuan dan folder yang dibuat, SSE `file.changed {dir, reason:"upload", paths}` (+ event untuk `dest_dir` bila berbeda). Respons membawa `Upload-Result`/`Upload-Path`. `DELETE` (`PreUploadTerminateCallback`) menandai `aborted` lalu tusd menghapus berkas.
- Header yang wajib diteruskan Nginx: `Upload-Offset`, `Upload-Length`, `Upload-Metadata`, `Tus-Resumable`, `Location` (absolut berbasis `APP_BASE_URL`), `X-Forwarded-Proto` (agar `Location` https).
- Client (`tus-js-client` 4.x, dimuat lazy): `chunkSize` = `UPLOAD_CHUNK_SIZE` (dari `/auth/me config.chunkSize`, diperbarui dari `precheck.chunkSize`), `parallelUploads: 1` per file (paralelisme antar file = 3, slot diambil sinkron), `retryDelays: [0, 1000, 3000, 5000, 10000]`, `storeFingerprintForResuming: true`, `removeFingerprintOnSuccess: true`, `headers: {X-CSRF-Token}` + `onBeforeRequest` menyegarkan token, `onShouldRetry` → tidak retry pada 4xx selain 423/429. Upload dengan policy `skip` yang sudah diketahui konflik di precheck tidak dikirim sama sekali (status `skipped` di panel). `QUOTA_EXCEEDED` pada satu file menandai sisa antrian `error`. Hasil akhir (`Upload-Result`/`Upload-Path`) dibaca dari `onSuccess.lastResponse`.
- Nginx: `client_max_body_size 64m` (> 16 MB chunk + header), `proxy_request_buffering off`, timeout 3600 s (§12.4).
- Upload terbengkalai: job `clean_uploads` tiap jam (`upload.Service.CleanExpired`) menandai `expired` baris `active` dengan `expires_at < now` (TTL 24 h sejak dibuat) dan menghapus berkas `<id>` + `<id>.info` di `DATA_DIR/uploads`, menghapus berkas yatim di folder itu yang tidak punya baris aktif dan berumur > 24 h, serta memangkas baris non-aktif > 7 hari. Reservasi kuota hilang bersama state `active`.

### 6.6 Share publik

- Resolusi `{token}`: jika cocok regex slug dan ada `shares.slug` → pakai; selain itu hash SHA-256 → `token_hash`. Lookup konstan-waktu tidak diperlukan (hash acak), tetapi respons untuk "tidak ada" dan "expired" dibedakan hanya setelah lookup berhasil; token yang tidak ada → `NOT_FOUND` generik dengan rate limit per IP.
- Urutan cek: ada? → `disabled_at`/owner disabled → `broken_at` → `expires_at` → `max_downloads` (saat download) → password (cookie valid? jika tidak → `SHARE_PASSWORD_REQUIRED`).
- Cookie share: nilai `base64url(share_id|exp|HMAC(hkdf("share-cookie"), share_id|exp|password_hash_prefix8))` sehingga mengganti password otomatis membatalkan cookie lama.
- Rate limit §10.5: unlock 10/10 menit per (link, IP) dan 30/10 menit per IP; listing/raw/download 120/menit per IP per link.
- Setiap `view` (halaman meta pertama kali per cookie sesi anonim `fm_pv` 24 jam) menaikkan `view_count`; setiap `download`/`zip` menaikkan `download_count` (zip dihitung 1). Log ke `share_access_logs` dengan IP dipotong.
- Halaman publik dilayani SPA; API publik tidak pernah mengembalikan path di atas item yang dibagikan (`path` selalu relatif terhadap item).
- (Step 18a) Kode akses: `SHARE_EXPIRED`/`SHARE_DISABLED`/`SHARE_LIMIT_REACHED` selalu **410** (§6.2; PROMPT 18a menulis 403 untuk dua yang terakhir — SPEC yang berlaku). `SHARE_DISABLED` membawa `details.reason`: `manual`, `owner_disabled`, `item_trashed`, `admin_revoked`, `broken` (item tidak ada: `broken_at` diset saat akses dan dicek ulang harian oleh job `check_shares` 02:30; item yang kembali membersihkan flag saat akses berikutnya), `locked` (50 password salah/jam → link dikunci 1 jam, pemilik diberi notifikasi `system`). Link preview-only (`allowDownload=false`): `download`/`zip` → `403 FORBIDDEN reason=preview_only`, `raw` tetap bisa. `download_count` naik sekali per unduhan (request `Range` lanjutan tidak dihitung); zip dihitung 1.
- (Step 18a) Meta sebelum unlock hanya memuat `isDir`, `passwordRequired`, `unlocked`, flag kebijakan, dan `expiresAt` — nama/ukuran/catatan/pemilik baru dikirim setelah password benar. Cookie `fm_pv` (24 jam, `Path=/api/v1/public/shares`) membedakan pengunjung untuk `view_count`.
- (Step 18a) Tamu dibatasi `Jail.SubRoot(path, readOnly)` untuk folder: listing memakai scope `s:<id>` tanpa anotasi metadata pemilik (favorit/pin/catatan/tag tidak pernah ikut) dan selalu `hidden=false`; file tunggal dialamatkan sebagai `path=/`. Thumbnail memakai pipeline pemilik (kunci path absolut) tetapi entri symlink ditolak agar link tidak bisa menunjuk keluar folder yang dibagikan. Upload file request: principal = pemilik + `share_id`, tujuan dipaksa ke folder share (metadata `path` klien diabaikan, `relativePath` dipakai), policy selalu `rename`, `upload_sessions.share_id` diisi, header `Location` tus ditulis ulang ke endpoint publik; setelah mendarat: log `upload` + notifikasi `share_upload` ke pemilik.
- (Step 18a) Halaman `/s/*` dan semua respons `/api/v1/public/shares/*` mengirim `Referrer-Policy: no-referrer` (X-Robots-Tag `noindex, nofollow` + meta robots berlaku global). QR dibuat di klien (`qrcode`, lazy) — endpoint `GET /shares/{id}/qr` **tidak** dibuat (tanpa library QR Go tambahan). Badge `Item.shared` diisi `share.Service.Annotate` (MetaProvider ke-2 setelah metadata item) untuk setiap share pada path itu, termasuk yang nonaktif/kedaluwarsa, sampai share dihapus. Migrasi memakai nomor `0012` (`0011` sudah dipakai `media_positions` 17b).

### 6.7 `docs/openapi.yaml`

Dibuat kerangkanya di Step 1 (info, servers, components: Error, Item, Task, securitySchemes cookie) dan **wajib diperluas di setiap step yang menambah endpoint** (acceptance criteria tiap step mencantumkannya). CI menjalankan `make openapi-lint`. Handler mendapat validasi request dari skema zod di frontend dan validator Go per handler (bukan generator dari OpenAPI) agar tidak mengunci toolchain; tes kontrak sederhana membandingkan daftar route chi (`chi.Walk`) dengan path di `openapi.yaml` dan gagal jika ada route API yang tidak terdokumentasi.

---

## §7 Task engine

### 7.1 Tipe task

| type | Pemicu | Payload (tanpa rahasia) | Unit progress | Hasil |
| --- | --- | --- | --- | --- |
| `copy` | `files/copy` besar | `{items, dest, policy}` | bytes | `{copied, skipped, renamed, errors[]}` |
| `move` | `files/move` lintas FS atau besar | idem | bytes | idem |
| `delete_to_trash` | `files/delete` > 200 item | `{paths}` | items | `{batchId, trashed}` |
| `delete_permanent` | `files/delete-permanent`, `trash/purge`, `trash/empty` | `{paths|trashIds}` | items | `{deleted, bytesFreed}` |
| `restore` | `trash/restore` banyak | `{ids, dest?}` | items | |
| `chmod` | `files/chmod recursive` | `{paths, mode, dirMode}` | items | |
| `compress` | archives/compress | `{paths, format, name, dest, level, hasPassword, encryptNames, deleteSource, splitMb}` — password hanya di memori engine | bytes (sumber dibaca) | `{path, paths? (volume), size, files, dirs, skipped[], status, deletedSources}` |
| `extract` | archives/extract | `{archive, dest:"here|new|<path>", encoding, policy, entries?, hasPassword}` — password di memori | bytes (ditulis; total dari indeks bila ada) | `{dest, format, files, dirs, links, bytes, skipped[{name,reason}], warnings, encoding, renamed, overwritten, skippedDest}` |
| `zip_edit` | archives/add, archives/remove-entries (Step 14b) | `{archive, mode:"add|remove", sources?, into?, entries?}` | bytes (ukuran arsip + byte baru) | `{path, size, entries, added, removed, replaced, skipped[]}` — zip ditulis ulang di `tmp` (entri lama disalin raw), diverifikasi, lalu `Rename` atomik; gagal/batal → arsip asli utuh |
| `remote_download` | remote-downloads | `{url, dest, filename, policy}` | bytes | `{path, size, finalUrl}` |
| `checksum` | files/checksum > 100 MB | `{path, algos, verify}` | bytes | `{md5, sha1, sha256, verified}` |
| `dir_size` | files/dir-size, storage-analysis | `{path}` | items | `{files, dirs, bytes, tree?, largest?}` |
| `replace_preview` / `replace_apply` | search | `{query…}` / `{previewId, files}` | items (file) | §8.12 |
| `batch_rename` | > 200 item | | items | |
| `thumbnail_batch` | internal (folder dibuka dalam view thumbnail) | `{dir, paths}` | items | tidak tampil di panel user (flag `hidden`) |
| `recalc_quota`, `clear_thumbs`, `clean_uploads`, `purge_trash`, `integrity_check`, `backup_db`, `prune_logs` | admin / jadwal | `{userId?}` | items | laporan |
| `delete_user` | admin | `{userId, mode}` | items | |
| `duplicate_finder` | `POST /me/duplicates` (Step 23c) | `{path}` (default `/`) | items (file yang di-hash) | `{path, groups:[{size, hash, reclaimable, paths:[{path, name, size, mtime, shared}]}], reclaimable, scanned, candidates, generatedAt, truncated, totalGroups}` — superset dari bentuk awal `{groups:[{size, hash, paths}]}`; batas waktu 1 jam → `failed` `error.code=TIMEOUT` (tanpa hasil parsial) |
| `transcode_hls` | `POST /files/hls` (Step 23d) | `{path, size, mtimeNs, durationMs}` (fakta sumber saat enqueue) | items = **detik media** yang sudah dikonversi (`out_time_us/1e6` dari `ffmpeg -progress pipe:1`; total = durasi; unit `items` karena CHECK `progress_unit IN ('bytes','items')` — tanpa migrasi) | `{dir:"<task id>", playlist:"/files/hls/<id>/playlist.m3u8", duration, width, height, segments}`; global maks `transcode.max_concurrent` berjalan (`Spec.MaxConcurrent`, sisanya tetap `queued` tanpa memakai slot), per user 1 queued/running (HTTP); runner mengecek ulang setting/ukuran+mtime/probe/disk saat mulai; timeout min(3 jam, durasi × 4 + 60 s) → `TIMEOUT`; gagal/batal/dimatikan admin → folder dihapus |

Task bertipe sistem (`user_id` = NULL → ditampilkan hanya di admin) berjalan di slot global, tidak mengambil slot user.

### 7.2 State machine

```mermaid
stateDiagram-v2
  [*] --> queued : Enqueue (validasi + lock reserved)
  queued --> running : slot tersedia (per user < TASK_MAX_PER_USER, global < TASK_MAX_GLOBAL)
  queued --> cancelled : cancel sebelum mulai
  running --> done : selesai tanpa error
  running --> failed : error (hasil parsial dibersihkan)
  running --> cancelled : cancel → ctx.Done → cleanup
  running --> interrupted : proses berhenti (SIGTERM/crash) — ditandai saat start berikutnya
  interrupted --> queued : retry (jika payload lengkap)
  failed --> queued : retry
  cancelled --> queued : retry
  done --> [*]
  failed --> [*]
  cancelled --> [*]
  interrupted --> [*]
```

### 7.3 Persistensi dan eksekusi

- `tasks.Engine` memegang `registry map[type]Spec{Unit, Hidden, NeedsSecret, Link, MaxConcurrent, Run}` (`MaxConcurrent func() int`, Step 23d: batas task berjalan per tipe lintas user, dibaca live; task di atasnya tetap di antrean dan dispatcher melanjutkan ke task lain) dengan `Runner` = `func(ctx context.Context, rt *Runtime) (result any, err error)`; `rt.Task` adalah snapshot (id, user, type, payload) dan `Runtime` menyediakan `Jail(ctx)`, `Progress(done, total)`/`Add(delta)`/`SetTotal`, `Log(level, format, …)`, `Secret(key)` (hanya memori, diserahkan lewat `EnqueueRequest.Secrets`), `Emit(event, data)` (mis. `file.changed`), `Cancelled()`. Runner tipe file (`internal/files/tasks.go`) memakai `files.WithTask(ctx, &TaskContext{Reporter: rt})` sehingga `Service.Copy/Move/DirSize/Checksum/Analyze` yang sama berjalan tanpa batas sinkron, tanpa undo, dan melaporkan progres.
- `Enqueue` memvalidasi tipe, membatasi queued+running per user (50 → `TASK_LIMIT`), menulis baris `tasks` (`queued`, `locks_json` informatif) lalu membangunkan scheduler goroutine tunggal yang memilih task `queued` tertua yang user-nya punya slot (`TASK_MAX_PER_USER`, global `TASK_MAX_GLOBAL`; task sistem `user_id NULL` hanya memakai slot global) dan menjalankannya dengan `context.WithCancel`. Lock path **tidak** dipegang engine: runner mengambilnya sendiri dan dalam mode task menunggu (`LockManager.Acquire`) sampai operasi sinkron pemegangnya selesai — efeknya sama dengan "tetap queued" tanpa dua pemegang lock.
- Progress ditulis ke DB maks 1×/detik dan ke SSE maks 2×/detik (`task.progress`); kecepatan = Δdone / Δt atas sampel 10 detik terakhir, ETA = (total − done) / kecepatan (dari laju pecahan sejak Step 23d: task yang < 1 unit/detik — konversi lebih lambat dari waktu nyata, hash berkas besar — tetap mendapat ETA; `speedBps` tetap bilangan bulat); keduanya `null` saat tidak diketahui. Hasil akhir (`result_json`, `error_code/message`) ditulis sekali di state final; `quota.updated` dipancarkan sebelum event final agar klien memegang angka kuota terbaru.
- Cancel: `cancel_requested=1` + `cancel()`; runner wajib memeriksa `ctx.Err()` paling lambat tiap 1 MiB atau 100 item; cleanup (hapus partial di `tmp`, hapus file tujuan yang baru dibuat sebagian, tidak pernah menghapus yang sudah ada sebelum task) lalu state `cancelled`.
- Shutdown: `SIGTERM` → `Engine.Shutdown(10 s)` membatalkan ctx semua runner dan menunggu maks 10 detik (sisa ditandai `interrupted` di DB, `error_code=INTERRUPTED`), broker SSE menutup semua stream, baru `http.Server.Shutdown` (≤ 30 s, `TimeoutStopSec=30` di systemd); saat start, semua baris `running` milik proses sebelumnya → `interrupted`, baris `queued` dimuat kembali dan dijalankan.
- Retry: membuat task **baru** dengan payload yang sama (`result.retryOf`), kecuali yang butuh password (compress/extract ber-password) → UI meminta password ulang.
- Log: `rt.Log` menulis ke `task_logs`; level `warn` untuk item yang dilewati (zip-slip, symlink, ekstensi diblokir).
- Pembersihan: job `cleanup_tasks` 04:30 harian menghapus task final > 30 hari bersama log (FK cascade); `DELETE /tasks/{id}` menghapus satu task final dari daftar; `tmp/<task_id>` dihapus saat task final; saat start, `tmp/*` yang tidak punya task `running` dihapus (dipakai arsip/remote download, Step 13–14).
- Mode pengembangan: `DEV_TASK_ITEM_DELAY_MS` (hanya `APP_ENV=development`, ≤ 1000) menunda tiap berkas/chunk yang disalin agar demo/E2E bisa membatalkan task di tengah jalan; `DEV_REMOTE_DOWNLOAD_ALLOW_LOOPBACK=true` (Step 13, §8.10) mengizinkan remote download ke loopback untuk E2E; `DEV_HLS_IDLE_TTL` (Step 23d, durasi ≤ 1 jam, mis. `2m`) memperpendek jendela idle folder HLS untuk cek manual sweeper; produksi mengabaikan ketiganya.
- Job terjadwal: `internal/scheduler` (Step 10a; tanpa dependensi, poll 30 s, job harian `At:"15:04"` di zona `TZ_DEFAULT` atau `Every` interval, satu job tidak pernah tumpang tindih dengan dirinya, `Timeout` per run, `RunNow(name)`): `purge_trash` 03:00 harian, `recalc_quota` 03:30 harian (semua user, maks 10 menit/user, 1 jam total), `backup_db` 02:30 harian (keep 7, 19b), `clean_uploads` tiap jam (12a), `prune_logs` 04:00 harian (`activity_logs` dan `share_access_logs` > `ACTIVITY_RETENTION_DAYS`, `audit_logs` > `AUDIT_RETENTION_DAYS`), `integrity_check` tiap 6 jam, `clear_thumbs` mingguan `Sun 05:00` (pangkas cache thumbnail > 2 GB secara LRU — atime/mtime — sampai ≤ 90 %), `check_shares` 02:45 (dipindah dari 02:30 agar tidak bersamaan dengan backup), `purge_deleted_users` 04:15, `cleanup_tasks` 04:30; sesi kedaluwarsa tiap jam (sudah ada di `auth.RunMaintenance`). **Step 19b:** definisi 7 job maintenance terpusat di `internal/admin/maintenance.go` (`jobs`), tiap run (terjadwal maupun manual) = task sistem bertipe §7.1 (progress, cancel, `result_json`); run terjadwal meng-enqueue lalu menunggu task. Tidak berjalan ganda: `scheduler.Registry` proses-wide (satu nama job tidak pernah berjalan bersamaan dan satu slot terjadwal hanya diklaim satu instance `Scheduler` dalam proses) + penolakan `409 already_running` bila task tipe sama masih queued/running (slot terjadwal yang bertemu run admin dilewati). Jitter acak 0–2 menit per run terjadwal; format jadwal mingguan `"Sun 05:00"`; `Status()` untuk UI; log `scheduled job started|done|failed`.

### 7.4 Panel task (frontend)

Tombol "Tasks (n)" di top bar (n = queued+running) membuka panel tetap di pojok kanan bawah (`features/tasks/TaskPanel`, tetap terpasang lintas route karena hidup di `AppShell`; panel transfer upload §8.8 ditempatkan di atasnya, toast sonner di kiri bawah) berisi task aktif + 20 terakhir: judul lokal dari `type`+`payload` (`taskLabel`, fallback `title` server), badge state, progress bar (indeterminate bila total 0), byte/item · kecepatan · ETA, tombol cancel/retry/hapus, log yang bisa dibuka (`GET /tasks/{id}/logs`). Store Zustand `useTasksStore` (task, notifikasi, status koneksi SSE) diisi `useTaskEvents` (klien SSE `lib/sse.ts`: reconnect eksponensial 1→30 s dengan `?lastEventId=`, `resync` → refetch `/tasks` + `/notifications`, `file.changed` → invalidasi `['list', dir]`, `quota.updated` → store sesi, `session.revoked` → login). Lonceng notifikasi (`features/notifications/NotificationsBell`): badge unread, popover 20 terakhir, tandai dibaca satu/semua, hapus, tautan `link`. Semua teks dari i18n (namespace `tasks`); task yang dijalankan di latar belakang, selesai, gagal, atau dibatalkan diumumkan ke live region dan toast.

---

## §8 Spesifikasi per fitur

### 8.1 Auth & akun

**Seed admin.** Saat start, jika `SELECT count(*) FROM users` = 0: buat user `ADMIN_USERNAME` dengan `ADMIN_INITIAL_PASSWORD` (argon2id), role `admin`, `must_change_password=1`, `quota_bytes=DEFAULT_USER_QUOTA`, `created_by=NULL`; audit `system.seed_admin`. Simpan `settings.initial_password_hash` = SHA-256(password awal) untuk cek "password baru tidak boleh sama dengan awal" (dihapus setelah password diganti). Jika tabel tidak kosong, env tidak disentuh; jika `ADMIN_INITIAL_PASSWORD` masih terisi dan admin sudah `password_changed_at` → `WARN`.

**Login.** `POST /auth/login`:

1. Rate limit per IP (20/menit) → `RATE_LIMITED`.
2. Normalisasi username lowercase; ambil user; jika tidak ada → tetap jalankan argon2 pada hash dummy (konstan waktu) → `UNAUTHENTICATED`.
3. `locked_until > now` → `ACCOUNT_LOCKED` (retryAfter). `status=disabled` → `ACCOUNT_DISABLED`.
4. Verifikasi argon2id; gagal → `failed_login_count++`, `login_attempts` (success=0); lockout bertahap: gagal ke-5 → 1 menit, ke-10 → 5 menit, ke-15 → 15 menit, ke-20+ → 60 menit (`locked_until`), dengan syarat ≥ 5 kegagalan dalam 15 menit terakhir (`login_attempts`); kegagalan yang memicu kunci tetap dijawab `UNAUTHENTICATED` generik, kunci terlihat (`ACCOUNT_LOCKED`) pada percobaan berikutnya; `failed_login_count` di-reset saat login sukses; audit `auth.login_failed` + `auth.account_locked`.
5. Jika `totp_enabled` (Step 23a, alur **inline** — tanpa token sementara): bila `totp` maupun `recoveryCode` tidak dikirim → `400 VALIDATION_FAILED details.fields.totp=totp_required` (tanpa sesi, tanpa menambah counter lockout, tanpa audit — klien menampilkan langkah kedua lalu mengirim ulang username+password+kode). Bila dikirim: limiter `RATE_LIMIT_TOTP` (5/menit per user, jendela geser) → `429 RATE_LIMITED` + `Retry-After`; `totp` = 6 digit, SHA-1, periode 30 s, window ±1 langkah, langkah yang diterima harus > `totp_last_step` (anti-replay, diperbarui atomik `UPDATE … WHERE totp_last_step IS NULL OR totp_last_step < ?`); `recoveryCode` = 10 karakter (`-`/spasi/huruf besar diabaikan) dicocokkan konstan-waktu terhadap semua baris `totp_recovery_codes` yang belum dipakai, lalu `used_at` diisi (sekali pakai; audit `auth.totp_recovery_used {remaining}`; respons memuat `recoveryCodesRemaining` sehingga UI memperingatkan bila 0). Kode salah/replay/sudah dipakai → `400 VALIDATION_FAILED details.fields.totp=totp_invalid` + `login_attempts` (success=0) + audit `auth.login_failed reason=totp_invalid`, **tanpa** `failed_login_count++` (password sudah benar; lockout bertahap tidak dipicu oleh kode TOTP, cukup limiter per user).
6. Sukses: **provisioning folder** (§8.2) dulu, baru reset counter + `last_login_at` (bila provisioning gagal, counter dan `last_login_at` tidak berubah — R-A m-08, Step 23a); buat sesi (token 32 byte → cookie; DB simpan hash; `csrf_secret`); `last_login_at`; audit `auth.login`; response user + `csrfToken`.
7. Jika `must_change_password` → client diarahkan ke `/change-password`; server menolak semua endpoint S+P dengan `PASSWORD_CHANGE_REQUIRED`.

**Ganti password.** Urutan: (1) kebijakan password baru — panjang ≥ `PASSWORD_MIN_LENGTH`, ≤ 128, UTF-8 valid, bukan di daftar password umum (SecLists `Pwdb_top-100000`, 95.667 entri unik lowercase, embed gzip, lihat `NOTICE`), tidak mengandung username, tidak sama dengan password awal (`settings.initial_password_hash`) maupun dengan `currentPassword` yang dikirim; (2) rate limit 5/15 menit per sesi; (3) verifikasi password sekarang (`VALIDATION_FAILED details.fields.currentPassword: incorrect`). Kebijakan dicek sebelum verifikasi agar kesalahan kebijakan tidak menghabiskan jatah percobaan. Sukses → hash baru, `must_change_password=0`, `password_changed_at`, hapus `initial_password_hash` (untuk admin dari env), cabut semua sesi lain, audit `auth.password_changed`; respons `204`.

**Sesi.** Middleware: baca cookie → hash → baris sesi; cek `revoked_at`, `absolute_expires_at`, `idle_expires_at`; perbarui `last_seen_at`/`idle_expires_at` jika > 5 menit sejak terakhir; muat user (cache 5 detik); `status=disabled` → cabut + `ACCOUNT_DISABLED`. Cookie: `fm_session=<token>; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=<absolute>`. Logout menghapus baris. Perubahan password/role/status oleh admin mencabut semua sesi user itu dan mengirim SSE `session.revoked`.

**CSRF.** Token = base64url(HMAC-SHA256(csrf_secret, session_id)); diberikan saat login dan `GET /auth/csrf`; client menyimpannya di memori (bukan cookie) dan mengirim `X-CSRF-Token` di setiap non-GET termasuk tus dan multipart. Server memverifikasi konstan-waktu; selain itu menolak jika `Sec-Fetch-Site` ∈ {`cross-site`} atau (`Origin` ada dan ≠ `APP_BASE_URL`). Endpoint publik unlock share memakai token CSRF anonim (double-submit: cookie `fm_pcsrf` + header) agar password share tidak bisa dipaksa dari situs lain.

**Role & authz.** Setiap handler memanggil `authz.Require(ctx, Policy{Admin bool, OwnerOf path, ShareEditor …})`; akses ke resource ber-ID (task, share, version, trash item, upload session) selalu `WHERE id = ? AND user_id = ?`. Test IDOR (§10.6) mencakup semua grup endpoint.

**Admin browse user** (`ADMIN_CAN_BROWSE_USERS=true`): `as_user` membuka `Jail` user target read-write; UI admin menampilkan banner merah "Anda sedang melihat folder <user>"; setiap request yang **diizinkan** → `audit_logs action=admin.browse_user details{username, path (query), method, endpoint, paths (nilai field path/paths/src/dst/to/from/items… dari body JSON ≤ 1 MB — R-F, review E m-10)}` (penolakan 403/404 tidak diaudit); `as_user` dan `share` tidak boleh digabung (400); endpoint milik pribadi (`trash/*`, `archives/*`, aktivitas) menolak scope.

### 8.2 Provisioning folder & kuota

**Provisioning** (`auth.ProvisionHome`, dipanggil saat login sukses, idempoten):

```text
dir := STORAGE_ROOT/<username>
if home_provisioned_at != NULL && dir ada → selesai
if dir tidak ada → MkdirAll(dir, 0750); chown tidak perlu (proses = filemanager); home_provisioned_at = now; audit user.home_provisioned
if dir sudah ada (username dibuat ulang / sisa lama) → JANGAN ubah isi; home_provisioned_at = now; users.home_warning = "existing_dir_reused"; audit user.home_reused; notifikasi admin
if dir ada tapi bukan direktori (file/symlink) → login gagal INTERNAL + log ERROR; admin diberi notifikasi
```

Pembuatan terjadi lewat `os.Root` pada `STORAGE_ROOT` (`OpenRoot(STORAGE_ROOT).Mkdir(username)`), bukan path gabungan string.

**Kuota.**

- Limit: `users.quota_bytes`; terpakai efektif = `used_bytes + trash_bytes + versions_bytes` (+ reservasi upload aktif saat precheck).
- Enforcement di: `files/create` dengan `content`/template (reservasi sebesar isi, lalu `Apply` — review B, M-01), precheck & create tus (reservasi), finalisasi upload (ukuran nyata), copy/duplicate (hitung ukuran sumber dulu; folder → walk cepat dengan batas 20 detik, jika melebihi → tolak dengan `details.reason:"unknown_size"` → user bisa memaksa sebagai task yang berhenti saat kuota habis), extract (pre-scan total uncompressed ≤ sisa), remote download (`Content-Length` jika ada; tanpa itu → berhenti saat melewati sisa), editor save (delta ukuran), restore dari trash (tidak dicek: file sudah dihitung), file request upload (kuota pemilik).
- Inkremental (`internal/quota`, Step 10a): setiap mutasi memanggil `quota.Apply(ctx, repo, userID, deltaUsed, deltaTrash, deltaVersions)` lewat repository transaksi yang sama bila ada (`ApplyQuotaDelta` upsert, counter tidak pernah < 0); delete→trash memindahkan `used → trash`, restore sebaliknya, purge/empty mengurangi `trash`, delete-permanent mengurangi `used`; move dengan `overwrite`/merge mengurangi `used` sebesar byte tujuan yang tertimpa (delta ukuran sumber+tujuan sebelum/sesudah; review B, m-01); copy/duplicate memakai delta nyata (ukuran tujuan sesudah − sebelum, walk ≤ 2.000 entri). `Reserve(ctx, jail, userID, bytes)` menahan byte di memori (`reserved`) sampai `Commit(actual)`/`Release`; `used + trash + versions + reserved + bytes > quota_bytes` → `QUOTA_EXCEEDED reason:quota details{used, quota, needed, free}`; `Statfs` free − bytes < 512 MB → `reason:disk`. `dirty=1` jika walk ukuran melewati budget 20 s atau bookkeeping gagal setelah FS berubah.
- Rekalkulasi: walk `Lstat` seluruh root user (symlink = ukuran link) + ukuran folder `trash/<uid>` + `versions/<uid>`; `SetQuotaUsage` absolut + `calculated_at`, `dirty=0`. Jadwal 03:30 (`internal/scheduler`, semua user yang sudah diprovisi, maks 10 menit per user, 1 jam total), aksi admin (19b). Perbedaan > 1 % terhadap angka inkremental yang tidak `dirty` dicatat `WARN` + audit `quota.drift`; rekalkulasi pertama user baru tidak dihitung drift.
- Indikator: sidebar "Kuota 62 %" (warna warning ≥ 80 %, danger ≥ 95 %, baris "Kuota hampir penuh" ≥ 90 %), tooltip rincian terpakai/Recycle Bin/versi/tersisa; status bar "x dari y". Klien mem-poll `GET /me/quota` tiap 60 s dan menginvalidasinya setelah operasi file (`useQuotaSync`); SSE `quota.updated` setelah task besar (Step 11).
- Disk penuh: semua writer juga memeriksa `Statfs` free ≥ 512 MB; jika tidak → `QUOTA_EXCEEDED reason:disk`; dashboard warning.

### 8.3 Listing & sort

**Algoritma `listing.List`:**

1. `jail.Stat(dir)`; bukan dir → `VALIDATION_FAILED not_a_directory` (UI membuka viewer).
2. Cache key = `hash(userID|scope|path|sortSpec|foldersFirst|hidden)`; entri `{dirMtime, builtAt, items []Item}`; valid jika `dirMtime` sama dan `builtAt` < 15 detik lalu, dan tidak di-invalidate. Cache LRU global 64 MB (≈ 100 folder besar).
3. Build: `ReadDir` → `Lstat` tiap entri (10k entri ≈ 50–150 ms di SSD); ambil metadata dari DB — favorites/pins/notes/tags per folder dengan satu query per tabel (`substr(path,1,len(prefix))=prefix AND instr(substr(path,from),'/')=0`, `meta.Service.Annotate`, Step 10b; tidak bergantung jumlah entri), media_metadata batch `path IN (…)` per 500 (`listing.MultiMeta` menjalankan keduanya sebelum sort sehingga `pinned` ikut tersortir dan ter-cache; tulis metadata memanggil `listing.Invalidate` folder induk); `mime` untuk file ditentukan dari ekstensi (cepat) dan diperbaiki oleh `media_metadata.mime` jika ada; `typeLabel` dari tabel ekstensi→label i18n.
4. Sort stabil (`sort.SliceStable`) dengan komparator berlapis: pinned desc → (foldersFirst: isDir desc) → field utama → field sekunder → nama (collator) → path.
5. Potong halaman: tanpa cursor → `items[0:limit]`; dengan cursor → cari posisi item pertama yang `> cursor` (binary search pada slice tersortir memakai komparator yang sama; cursor menyimpan `{v:1, f:sortField, d:dir, k:<nilai field item terakhir>, n:<nama>, p:<pinned>, i:<isDir>}`), sehingga cursor stabil walau folder berubah (item yang disisipkan sebelum posisi tidak menyebabkan duplikasi/lompatan item setelahnya).

**Natural sort.** Komparator natural kustom `listing.NaturalLess(a, b, coll)`: nama dipecah menjadi run digit dan non-digit; run digit dibandingkan secara numerik (nol di depan diabaikan, lalu panjang, lalu leksikal), run non-digit dibandingkan dengan collator `collate.New(tag, collate.IgnoreCase)` dengan `tag` = `language.Indonesian` atau `language.English` sesuai preferensi; hasil seri → byte-wise. **Jangan** memakai `collate.Numeric`: issue golang/go#67997 (`"0.0"` vs `"1.0"` salah urutan) ditutup "not planned" pada Sep 2026 walau ada perubahan `internal/colltab: fix zero digit` (Mei 2026) — status perbaikan tidak pasti (diverifikasi 14 Okt 2026), sehingga komparator sendiri + unit test wajib (`file2 < file10`, `0.0 < 1.0`, `a-21 < a-123`, `01` vs `1` deterministik, Unicode/NFC). Kunci non-digit (`collator.KeyFromString`) dihitung sekali per item saat build. Contoh urutan: `file1, file2, file10, File11, gambar, Zeta` (case-insensitive; `é` setelah `e`). Tie-break byte-wise nama.

**Field sort** (server): `name`, `size` (folder: itemCount jika diketahui, else 0; folder yang sudah dihitung memakai bytes), `type` (typeLabel lalu nama), `ext`, `mtime`, `btime` (NULL paling akhir), `atime`, `mode` (octal numerik), `note` (kosong paling akhir), `dimensions` (width×height area; NULL akhir), `duration` (NULL akhir). Arah `asc|desc`; `desc` membalik hanya field utama, bukan tie-break nama.

**Filter cepat & hidden.** `filter=` menyaring substring nama case-insensitive (setelah fold Unicode) pada seluruh folder di server (bukan hanya halaman). `hidden=false` menyembunyikan dotfile. `types=` (`image,video,audio,document,archive,code,folder`) untuk pemilih tipe.

**Response Item** juga membawa `itemCount` untuk folder hanya jika murah: dihitung lazy di background untuk folder dengan ≤ 10.000 entri (ReadDir tanpa stat) dan di-cache 60 detik; jika tidak tersedia → `null` dan UI menampilkan "—".

**Catatan implementasi (Step 5).** Handler membuka jail per user lewat cache handle (`httpapi/jails.go`: satu `*os.Root` per user, refcount saat dipakai, ditutup setelah idle 5 menit; operasi panjang/task membuka jail sendiri). Cursor = base64url(JSON `{v:1, s:<SortSpec>, k, k2, n, p, i}` ‖ HMAC-SHA256 8 byte dari kunci turunan `APP_SECRET`); cursor untuk spec sort lain/dimanipulasi → `VALIDATION_FAILED fields.cursor`. `path=/.system` dijawab `404 NOT_FOUND` (nama reserved tidak pernah ada di dalam root user). `counts=1` mengisi `itemCount` hanya untuk folder di halaman (≤ 2.000/permintaan, cache 60 s, folder > 10.000 entri → `null`). Kolasi: `é` berkolasi tepat setelah `e` (sebelum `f`), jadi `é.txt` mendahului `file1`. Parameter `share=`/`as_user=` ditolak `VALIDATION_FAILED not_implemented` sampai Step 18b/19a. `limit` > `LISTING_PAGE_SIZE` (atau > 1000) → `VALIDATION_FAILED fields.limit` (review A, m-01). Bentuk `{path}` penuh pada `mkdir`/`create` memvalidasi nama segmen terakhir dengan `ValidateName` (semua segmen bila `parents:true`) → `NAME_INVALID details.reason` (review A, M-03); `create` menolak ekstensi di `BLOCKED_EXTENSIONS` dengan `UNSUPPORTED_TYPE details.ext` sebelum menyentuh jail (review A, M-02). Respons API membawa `Cache-Control: no-store`, `Cross-Origin-Resource-Policy: same-origin`, dan CSP `default-src 'none'` (§10.3; review A, M-01); `docs/openapi.yaml` dijaga sinkron dua arah oleh `TestReviewAOpenAPIContract` (§6.7).

**Catatan implementasi (Step 21).** `fsjail.Jail.ReadDir` membaca nama dengan getdents (`Readdirnames`, tanpa `lstat` tambahan dari `os.File.ReadDir`) lalu `statx` per entri — paralel oleh ≤ 4 goroutine (blok 256 entri) bila `GOMAXPROCS > 1`, berurutan di 1 CPU. `listing` menyimpan item mentah di cache dan memformat string tampilan (`mtime/ctime/atime/btime`, `mode`, `modeOctal`, `etag`) hanya untuk item halaman yang dikirim (`Item.fill` pada salinan); kunci kolasi tiap run teks di-memo per build (`keyer`, ≤ 8.192 run) dan nama ASCII dilipat tanpa `cases.Fold`/NFC. Hasil: build dingin 10.000 entri 219 → 66 ms (4 CPU) / 191 → 69 ms (1 CPU) in-process, alokasi 68 → 11 MB; detail `docs/PERF.md`. Respons dikirim `encoding/json.Encoder` langsung ke writer; kompresi oleh middleware `httpapi.Compress` (gzip level 6 untuk `application/json`, `text/html`, `text/css`, `application/javascript`, `text/javascript`, `image/svg+xml` ≥ 1 KB; tidak menyentuh HEAD, `Range`/206, respons ber-`Content-Encoding`, unduhan/media/SSE; `ETag` kuat menjadi lemah saat dikompresi; `Vary: Accept-Encoding`). Brotli tidak dibuat di aplikasi (tanpa dependensi baru) — opsional di Nginx bila modulnya ada (perlu verifikasi).

**Target:** folder 10.000 item: API p95 < 500 ms (dingin) / < 50 ms (cache); payload halaman 500 item ≈ 240 KB JSON (diukur Step 21; perkiraan awal 120 KB); gzip 5 KB pada fixture sintetis (nama berurutan), perkiraan 15–25 KB untuk nama nyata — gzip oleh aplikasi (lihat catatan di atas).

### 8.4 Views & pipeline thumbnail

**Mode view** (preferensi `view.mode` + `folder_view_prefs`): `details`, `list`, `tiles`, `large`, `xlarge` sesuai tabel prompt bagian 5. Komponen `FileGrid` memakai TanStack Virtual (TanStack **Table** tidak dipakai sejak Step 7b: row model-nya membangun ulang 10.000 objek `Row` di setiap halaman cursor dan menjadi long task terbesar di E2E 10k; kolom didefinisikan sendiri di `features/browser/columns.tsx` — `{id, header, cell, meta}` — dan state kolom/sort hidup di preferensi server): Details = baris 36 px virtual (header 32 px sticky); List = kolom mengalir atas→bawah lalu kiri→kanan: tinggi kontainer menentukan baris per kolom, virtualisasi per kolom horizontal; Tiles = grid 220×64 px; Large = 128 px cell + nama 2 baris (`text-overflow` tengah via JS: `abc…xyz.ext`); Extra large = 256 px.

**Thumbnail (server):**

| Sumber | Tool | Perintah (argv) |
| --- | --- | --- |
| jpg, png, webp, gif (frame 1), bmp, tiff, avif, heic (libvips 8.15 di server: libheif, librsvg, libwebp, poppler aktif — diverifikasi `vips --vips-config`) | `vipsthumbnail` | `vipsthumbnail -s <N>x<N> -o <out>.webp[Q=80,strip] <in>` — auto-rotate EXIF adalah default (flag `--no-rotate` untuk mematikan); **`--` tidak didukung** vipsthumbnail (diperlakukan sebagai nama file), aman karena path input selalu absolut dari `fsjail` dan tidak pernah diawali `-` |
| svg | `vipsthumbnail` (librsvg) | sama; hasil raster, SVG sumber tidak pernah inline |
| video | `ffmpeg` | durasi dari `media_metadata` → `ffmpeg -nostdin -v error -ss <10 % durasi> -i <in> -frames:v 1 -vf scale='min(512,iw)':'min(512,ih)':force_original_aspect_ratio=decrease -f image2 -c:v png -y <tmp>.png` (fallback `-ss 0`), lalu `vipsthumbnail` menghasilkan WebP tiap ukuran dari PNG itu |
| pdf | `pdftoppm` | `pdftoppm -singlefile -f 1 -l 1 -scale-to 512 -png -- <in> <prefix>` → `<prefix>.png` → vips (`-singlefile` menghindari penomoran halaman yang bervariasi) |
| audio (cover) | `ffmpeg` | `ffmpeg -nostdin -v error -i <in> -an -c:v copy -f image2 -frames:v 1 -y <tmp>.jpg` → vips; audio dianggap *thumbable* secara tipe (`Item.thumb=true`) sampai probe menyatakan tidak ada cover → `thumb_state=unsupported` (404 → ikon) |
| fallback gambar tanpa vips | Go `image` (jpg/png/gif/webp decode) + `x/image/draw` | lambat, hanya jika vips absen |

Semua CLI: `exec.CommandContext` dengan timeout 30 s (video 60 s), `nice -n 10`, `ionice -c3` (jika binary `ionice` ada), `Env` minimal (`PATH`, `HOME=/nonexistent`, `TMPDIR=DATA_DIR/tmp`), stdin `/dev/null`, stdout/stderr dibatasi 64 KB, file input dibuka lewat `Jail.Open` lalu diteruskan sebagai `/proc/self/fd/N`? — **tidak**; path absolut sumber dibentuk oleh `fsjail` khusus untuk CLI (`jail.HostPath(p)` hanya dapat dipanggil oleh package `thumbs/archive/search/media`, diverifikasi `Lstat` tanpa symlink pada setiap segmen tepat sebelum exec untuk memperkecil TOCTOU; race residual diterima karena CLI hanya membaca dan berjalan sebagai user `filemanager`).

- Ukuran 128/256/512; cache file `DATA_DIR/thumbs/<uid>/<h[0:2]>/<h>-<size>.webp`, `h = sha256(path|fileSize|mtime_ns)`; `ETag = "<h[:32]>-<size>"`; `Cache-Control: private, max-age=86400`; `If-None-Match` → 304. Satu job merender **ketiga ukuran sekaligus** ke nama sementara lalu mempublikasikannya bersama (rename setelah semua selesai) sehingga permintaan ukuran mana pun tidak pernah melihat job setengah jadi. Respons raw memakai `Content-Security-Policy: sandbox; default-src 'none'` (§10.3). Pesan error tool (stderr vipsthumbnail/ffmpeg memuat path absolut input) disimpan ke `media_metadata.thumb_error` dan log hanya setelah `Jail.Redact` mengganti `STORAGE_ROOT/<user>` dengan `<root>` (review B, m-02). Implementasi: `internal/thumbs` (queue heap: permintaan viewport terbaru dulu, pekerjaan latar `Low` terakhir, dedup per (user, path), job yang sedang berjalan tidak di-queue ulang), `internal/media` (probe + cache `media_metadata`), `internal/exec` (runner CLI: `Runner.Run(ctx, Spec{Bin, Args, Stdin, Timeout, MaxOutput})` → argv `nice -n 10 ionice -c3 <bin> …`, env minimal `PATH/HOME/LC_ALL/TMPDIR`, stdin `/dev/null` kecuali diberikan, process group dibunuh saat timeout, `ErrMissing` → `DEPENDENCY_MISSING`), signer di `internal/download/signer.go` (dipakai lagi 12b/18a).
- Dihapus saat file dihapus/dipindah/diubah (prefix by path lewat tabel `media_metadata` → `thumb_state`); job `clear_thumbs` admin; LRU jika folder thumbs > 2 GB (hapus atime tertua).
- Antrian prioritas: request `GET /files/thumb` yang belum ada → masuk heap dengan prioritas (waktu request terbaru lebih tinggi; viewport = request yang datang); worker pool `THUMB_WORKERS`; dedup per key; response `202` + `Retry-After: 2` sementara; `thumbnail_batch` (saat folder dibuka di view thumbnail) menambahkan sisa item dengan prioritas rendah.
- Client (Step 21): ikon tipe file adalah sprite SVG (`FileIconSprite` dipasang sekali di root route, setiap `FileIcon` = satu `<use href="#fm-icon-<kategori>[-lg]">`); baris Details = `DetailsRow` ber-`React.memo` (handler stabil lewat ref, overscan 10, checkbox baris elemen ringan `role="checkbox"` dengan tabIndex −1 — fokus keyboard tetap di grid); offset scroll per folder disimpan ke store tab setelah scroll berhenti 150 ms (bukan per event); kolom Dimensi/Durasi: listing tanpa `media` dulu, varian `media=1` diminta saat `requestIdleCallback` dan menggantikan baris di tempat.
- Client: `IntersectionObserver` (rootMargin 200 px), maks 4 pemuatan paralel (slot LIFO: sel yang terakhir terlihat dulu; permintaan dibatalkan saat sel keluar viewport sebelum gilirannya), placeholder ikon tipe, `<img decoding="async" loading="lazy">`. URL ditandatangani per batch (`POST /files/sign {paths:[…≤500], purpose:"thumb", size}` — satu panggilan per ±50 ms permintaan, cache per (path, etag, size) sampai 60 s sebelum `expiresAt`). Pemuatan memakai `fetch` agar status terbaca: 202 → retry maks 3× dengan backoff 2/4/8 detik; 404 (unsupported/failed) → ikon tanpa retry; 200 → body dibaca penuh sehingga `<img src>` berikutnya memakai HTTP cache. TTL signed URL thumb = `MEDIA_URL_TTL` (2 jam) agar grid yang dibuka lama tidak terus menandatangani ulang (deviasi dari §8.9 awal yang menulis `SIGNED_URL_TTL`).
- `media_metadata` diisi oleh worker yang sama (ffprobe/`image.DecodeConfig`; MIME dari magic bytes `gabriel-vasile/mimetype`; EXIF ditunda ke Step 17a), dipakai kolom Dimensi/Durasi dan sort. Listing `GET /files/list?media=1` (dikirim client saat kolom Dimensi/Durasi tampil) memprobe sinkron ≤ 50 file per halaman (`media.MaxSyncProbes`), sisanya diantrekan prioritas rendah (`ProbeOnly`); baris yang tidak valid lagi (size/mtime berbeda) dihitung ulang; `Item.thumb` menjadi `false` setelah pipeline menandai `failed`/`unsupported`.

### 8.5 Operasi file

**Rename** (`files/rename`): validasi nama; `TryAcquire(write, old, new)`; `Rename`; `files.UpdateReferences` (§5.7) + `activity_logs rename` dalam satu transaksi (gagal → rename dibatalkan, error dicatat); response membawa `undoToken` + `undo:{kind:"rename", path, newName}` (Step 9a: token acak in-memory per user, umur 60 detik, sekali pakai — lihat "Undo").

**Copy / Move** (`files/copy|move`):

1. Validasi: semua `items` ada; `dest` folder ada; untuk tiap item, `dest` bukan item itu sendiri maupun turunannya (`VALIDATION_FAILED cannot_move_into_self`); nama tujuan = basename item.
2. Resolusi konflik per item (semua item dipindai dulu; pada policy `fail` tidak ada yang dimutasi): `Lstat(dest/name)` ada → `perItem[path]` atau `onConflict`: `overwrite` (file menimpa file; folder bertemu folder = **merge** per file, bukan hapus folder tujuan; file vs folder → `VALIDATION_FAILED type_mismatch`), `skip` (file dilewati; folder bertemu folder tetap merge dengan skip per file), `rename` (`nama (1).ext`, `nama (2).ext`… — untuk folder `nama (1)`, tanpa merge), `fail` (default; alias `ask`) → `ALREADY_EXISTS` dengan `details.existing/incoming` (konflik pertama) dan `details.conflicts:[{path, source, existing:{size,mtime,isDir}, incoming:{…}}]` agar UI menampilkan dialog bandingkan. Status per item: `ok|skipped|overwritten|renamed|merged`.
3. Estimasi: jumlah entri & byte (walk cepat, batas 5 s; `internal/files.Service.estimate`); ≤ 2.000 entri & ≤ 200 MB → sinkron (PROMPT Step 9a); selain itu → task `copy|move` (`202`). Handler menjawab `202 {task}` dan runner menjalankan `Copy/Move` yang sama dalam mode task (`files.WithTask`: tanpa batas sinkron, lock ditunggu bukan gagal cepat, tanpa undo, progres byte ke `Reporter`). Move dalam satu filesystem adalah rename (tanpa batas); batas berlaku untuk copy, merge folder, move lintas filesystem (deteksi `Info.Dev`) dan chmod rekursif.
4. Kuota (copy): cek sebelum mulai; task berhenti `failed QUOTA_EXCEEDED` jika habis di tengah (file parsial dihapus).
5. Move dalam satu filesystem = `Rename` per item (atomik, instan); referensi metadata di-rewrite. Copy tidak menyalin metadata (catatan/tag) kecuali preferensi `fileops.copy_metadata=true`.
6. Lock: write pada tiap item sumber (move) / read (copy) dan write pada `dest/<name>`.

**Duplicate**: copy ke folder yang sama dengan nama `nama (1).ext` → `nama (2).ext`…; folder sama.

**Symlink**: `files/symlink` (§5.4). UI: dialog "Buat symlink" dengan picker target (hanya dalam root).

**Chmod**: mode input `"0644"`/`"644"`/checkbox rwx ×3; bit lain dibuang; rekursif → task dengan `fileMode` dan `dirMode` terpisah (default: dir mendapat bit x jika r ada). Pemilik/grup tidak bisa diubah (non-goal).

**Batch rename** (`files/batch-rename`):

- Urutan transformasi per item (`ext_mode=keep` default: hanya stem; `include`: seluruh nama): `find/replace` (plain = literal, `$` tidak diekspansi; `regex=true` = Go RE2 dengan `$1`; `case_insensitive` = `(?i)`; pola ≤ 256 karakter) → `case` (`upper|lower|title|none`) → `numbering`: `pattern` wajib memuat `{n}` (mulai `start` default 1, `step` default 1, `pad` 0–12, urutan = urutan `paths`), `{name}` stem saat itu, `{ext}` ekstensi tanpa titik; ekstensi majemuk (`.tar.gz`) dikenali oleh `files.SplitExt`. (`{date:…}` dan `extension: lower|upper` ditunda ke 9b jika UI membutuhkannya.)
- Preview mengembalikan `{from, to, status:"ok|collision|invalid", reason}` per item: `collision` bila hasil sama dengan hasil item lain (`duplicate_result`; kedua item ditandai) atau dengan entri folder yang tidak ikut di-rename (`exists`); `invalid` dengan reason `fsjail.ValidateName` (`empty|slash|…`), `not_found`, atau `blocked_extension` bila nama file hasil berakhiran ekstensi di `BLOCKED_EXTENSIONS` (§5.2; folder tidak terkena; review B, M-02); item yang namanya tidak berubah `ok` dan dilewati saat apply. Apply menghitung ulang preview di server dan menolak (`409 CONFLICT preview_not_clean`, `details.items`) bila ada collision/invalid.
- Eksekusi memakai **dua fase** untuk menghindari tabrakan berantai (`a→b`, `b→a`): semua ke nama sementara `.<batch8>-<i>.fmtmp` di folder masing-masing lalu ke nama akhir; gagal di tengah → rollback ke nama asli; state dicek ulang di bawah lock (tujuan yang muncul setelah preview → `ALREADY_EXISTS`). Referensi metadata di-rewrite dua fase pula dalam satu transaksi. > 200 item → `PAYLOAD_TOO_LARGE` (task di Step 11).

**Delete ke Recycle Bin** (`files/delete`): UI: tombol `Delete` meminta konfirmasi ringan (AlertDialog + "Jangan tanya lagi") selama preferensi `fileops.confirm_delete=true` (default), lalu toast "Urungkan" 10 s. Server, per item: buat baris `trash_items` (`kind` dari mime, `size_bytes`/`item_count` via walk dengan batas 20 s — jika melebihi, `size_bytes` diisi dari task `dir_size` setelahnya), `MoveOut` ke `trash/<uid>/<id>`, `shares` → `disabled item_trashed`, `quota.Apply(-size, +size)`, `activity delete`. Satu request = satu `batch_id`. Dalam share Editor: item masuk Recycle Bin **pemilik** (penerima tidak punya trash untuk item itu) dengan `activity` di kedua sisi.

**Hapus permanen** (`Shift+Delete`, `files/delete-permanent`): konfirmasi UI; > 50 item wajib `confirm:"HAPUS"`; task `delete_permanent`: `RemoveAll`, hapus semua referensi + versi, `quota.Apply(-size)`, audit `file.delete_permanent` (jumlah & path pertama). Dialognya (`DeletePermanentDialog`, Step 9b) sudah ada; sampai endpoint-nya tiba di Step 10a, konfirmasi memanggil `files/delete` (ke Recycle Bin) dan toast menjelaskan hal itu.

**Undo** (`files/undo`): body = `{token}`; token acak 24 byte dari response sebelumnya, disimpan **in-memory** per user (`files.UndoStore`, umur 60 s, sekali pakai; proses restart = token hilang — hanya menonaktifkan "Urungkan" semenit terakhir). Response operasi juga membawa `undo:{kind, …}` tanpa rahasia untuk label toast. Operasi balik (tidak menghasilkan token baru, `activity undo`): `rename` → rename kembali; `move` → tiap item dipindah persis ke path asal (`moveExact`, termasuk yang mendapat nama `(n)`); `restore` (dari delete) → `trash.Restore` per item; `delete_created` (copy/duplicate/symlink) → item baru dipindah ke Recycle Bin; `batch_rename` → pasangan dibalik lewat dua fase yang sama. Gagal karena state berubah (nama terisi, item hilang) → `409 CONFLICT details.reason=undo_conflict`; token asing/kedaluwarsa → `404 NOT_FOUND undo_expired`. Operasi yang tidak bisa di-undo (chmod, overwrite, merge, skip) → `undo:null`. Toast "Urungkan" 10 detik di UI; setelah 10 detik tombol hilang walau token masih berlaku 60 s (untuk latency).

**Dialog "Pindah ke… / Salin ke…"**: folder picker tree lazy (`files/list` dengan `types=folder`, `role=tree` + `aria-activedescendant`, panah/Enter), path ketik (Enter langsung menjalankan), tombol "Folder baru di sini"; bagian Favorit dan Recent folder ditambahkan di Step 10b; destinasi default = folder saat ini; validasi "tidak bisa ke dalam dirinya sendiri" (dan "sudah di folder ini" untuk move) di client dan server. Fokus kembali ke grid saat dialog ditutup (semua dialog operasi file).

**Drag & drop**: HTML5 DnD native (dipilih di Step 9b: satu-satunya API yang menerima file dari OS; kompatibel dengan baris tervirtualisasi karena `draggable` dipasang per baris yang dirender). Payload = MIME `application/x-fm-paths` `{paths, from}` + salinan in-memory (Chromium tidak membuka `getData` saat `dragover`); drag image = badge "N item". Drop ke baris folder / breadcrumb / sidebar (Favorit, folder di tree) = move; tahan `Ctrl` saat drop = copy; indikator visual badge "Pindah/Salin". Drag dari OS = upload (`DataTransferItem.webkitGetAsEntry` untuk folder). Auto-scroll saat drag mendekati tepi.

**Clipboard aplikasi**: store Zustand `{mode:"copy"|"cut", paths, from, sourceTab}` (`stores/clipboard.ts`) persist di `sessionStorage`, berlaku lintas tab folder dan tab browser (via `BroadcastChannel` `fm-clipboard`); Cut menampilkan item sumber semi-transparan (`data-cut`); Paste → `copy|move` dengan `dest` = folder yang dipilih (tepat satu folder) atau folder aktif; clipboard `cut` dikosongkan setelah paste berhasil.

**Rename inline**: F2/klik lambat; input dengan nama tanpa ekstensi terseleksi; Esc batal; Enter/blur simpan; validasi real-time (nama invalid, duplikat).

### 8.6 Recycle Bin & undo

- Lokasi fisik `DATA_DIR/trash/<user_id>/<trash_item_id>` (nama asli disimpan di DB sehingga dua item bernama sama tidak bentrok).
- Halaman `/trash`: tabel (Nama, Lokasi asal, Ukuran, Dihapus, Kedaluwarsa), filter `kind` (file/folder/gambar/dokumen/video/audio/arsip), pencarian nama, sort, seleksi batch; aksi Restore, Restore ke…, Hapus permanen, Kosongkan; status bar total ukuran; banner "Item dihapus otomatis setelah N hari".
- Restore (`files.RestoreFromTrash` → `trash.Restore`): tujuan default `original_path`, atau `to/<name>` untuk "Pulihkan ke…"; jika folder induk tidak ada → `MkdirAll`; semua konflik dikumpulkan dulu (tidak ada yang dipindah) → dialog konflik (overwrite/rename/skip, per item lewat `perItem[trashId]`); `overwrite` memindahkan item yang menempati tujuan ke Recycle Bin lebih dulu (`overwrittenId`, bisa dipulihkan lagi — tidak ada yang hilang tanpa purge eksplisit); `MoveIn` di bawah write lock tujuan; referensi metadata tetap (tidak diubah saat delete); share yang `disabled_reason=item_trashed` → dialog "aktifkan kembali share?" (client memanggil `PATCH /shares/{id} {disabled:false}`; `reactivateShares` kosong sampai 18a); `quota.Apply(+size, -size)`; `activity restore`; hook `OnChange` + invalidasi listing tujuan.
- Purge/Empty: `RemoveAll` payload, lalu satu transaksi: `purged_at`, hapus referensi `original_path` **kecuali** path itu kini ditempati item baru (metadatanya milik item baru), `quota.Apply(0, −size)`; `activity purge`/`trash_empty`; audit `file.delete_permanent`/`trash.empty`. Baris purged dipertahankan 7 hari lalu di-prune oleh job.
- Auto-purge: job `purge_trash` 03:00 (`internal/scheduler`, zona `TZ_DEFAULT`) menghapus `expires_at < now` dalam batch 100 untuk semua user (jail dibuka per user untuk cek referensi), lalu prune baris purged > 7 hari; `TRASH_RETENTION_DAYS=0` → `expires_at` NULL dan job tidak melakukan apa-apa. Admin bisa menjalankan job manual lewat `Scheduler.RunNow` (19b).
- Ukuran: `size_bytes` dihitung saat delete (walk dengan budget 20 s; bila terpotong angka parsial disimpan dan kuota ditandai `dirty`).
- Preview sebelum restore: `GET /trash/{id}/download` (file) atau listing read-only isi folder (`GET /trash/{id}/list` — fase akhir, opsional).
- Retensi 0 = tidak auto-purge; admin bisa memaksa purge semua user lewat maintenance.

### 8.7 Favorit, pin, catatan, tag, Recent, Properties, Analisis penyimpanan

- **Favorit**: file/folder; sidebar "Favorit" menampilkan daftar (folder bisa diklik masuk; file membuka viewer); DnD ke sidebar menambah favorit; badge bintang di kolom Nama; referensi rusak → badge + "Hapus dari favorit".
- **Pin**: per folder induk; item pinned selalu di atas (setelah sort), badge pin; maks 50 pin per folder.
- **Catatan** (remark/"PS" aaPanel): kolom "Catatan" editable inline (klik dua kali sel; Enter simpan, Esc batal, counter karakter), **≤ 500 karakter** (PROMPT Step 10b; draf awal 1000), UTF-8 valid, Markdown tidak dirender (plain text, escape). Juga lewat dialog "Catatan…" (menu konteks) dan Properties/Quick Look.
- **Tag warna**: 7 warna; badge titik warna sebelum nama; filter cepat "Tag: merah" di toolbar; halaman pencarian mendukung `tag=`; batch set/hapus.
- **Recent**: derivasi `activity_logs` (open/edit/upload/create/restore) 30 hari, dedup per path, default 100 (maks 200 via `?limit`), hanya yang masih ada; kolom Lokasi; aksi "Buka lokasi file" (navigasi ke folder + seleksi item lewat `?select=<nama>` di route `/files/*`). `open` dicatat klien lewat `POST /me/recent {path}` saat viewer/Quick Look/editor membuka berkas; `create` dicatat handler mkdir/create. Favorit/pin/catatan/tag yang path-nya hilang (perubahan via SSH) dikembalikan sebagai `broken:true`/`brokenReason:"missing"`, tidak pernah 500.
- **Properties** (dialog `features/properties/PropertiesDialog`, Alt+Enter; tab Umum / Checksum (file) / Riwayat versi): Nama, Tipe (label + MIME), Lokasi (path virtual), Ukuran (file: byte & manusiawi; folder: tombol "Hitung" → `POST /files/dir-size` sinkron ≤ 60 s, hasil `files/dirs/bytes` juga mengisi kolom Ukuran; Step 11 mengubah walk panjang menjadi task), Diubah/Dibuat/Diakses/Perubahan status, Permission (rwx + octal, tombol Ubah… membuka dialog Permission), Symlink target, Checksum (MD5/SHA-1/SHA-256 per algoritma atau semua: file ≤ 100 MB sinkron, lebih besar pesan "butuh task engine" (Step 11); tombol salin; "Verifikasi hash" → cocok/tidak + hash sebenarnya), Riwayat versi (placeholder sampai 16b), Share/Catatan/Tag/Favorit/Pin (10b/18b). Multi-seleksi → ringkasan jumlah file/folder & total ukuran file (folder tidak dihitung; "Hitung ukuran" per folder). Aksi menu "Hitung ukuran" (folder) dan "Checksum…" (file) aktif sejak Step 10a.
- **Copy path**: menyalin path virtual (`/Proyek/a.txt`); menu "Salin link share" hanya jika ada share publik aktif.
- **Analisis penyimpanan** (`/storage`): walk sinkron `files.Analyze` pada `/` (budget 60 s, `partial:true` bila terpotong; walk terpotong dilanjutkan task `dir_size` dengan `payload.analysis=true`, Step 11) dengan agregasi per folder (depth ≤ 3 untuk treemap; anak < 0,5 % total digabung node `kind:"other"`; berkas langsung tiap folder menjadi leaf `kind:"files"`), 50 file terbesar (min-heap), ringkasan per kelas (image/video/audio/document/archive/other — dokumen mencakup document/spreadsheet/presentation/pdf/text/code), ukuran Recycle Bin dan versi dari `quota_usage`; **akuntansi sama dengan kuota** (berkas reguler + ukuran link symlink) sehingga `total` = `quota.used`; cache 10 menit per user (dihapus oleh mutasi file apa pun), `POST` memaksa hitung ulang ("Hitung ulang"); treemap SVG squarified ditulis sendiri (`features/storage-analysis/squarify.ts`, tanpa d3), dua tingkat tampil sekaligus, hover tooltip, klik folder → drill-down lalu buka folder; tabel 50 terbesar dengan "Buka lokasi file" dan Hapus.
- **Duplikat** (Step 23c; `/storage` kini dua tab Radix — "Ringkasan" = analisis di atas, "Duplikat" — dengan deep link `?tab=duplicates`): task `duplicate_finder {path}` (`internal/files/duplicates.go`, hanya scope pemilik, tanpa dedupe lintas user) — (1) walk subtree lewat `fsjail.WalkDir`: hanya berkas reguler > 0 byte; symlink, berkas kosong, nama staging (`.fm-upload-*`, `.fm-remote-*`, `.fm-extract-*`, `.fm-zipedit-*`, `*.fm-partial-*`, `.*.fm-tmp-*`; folder staging dilewati utuh) dan entri yang gagal `Lstat` dilewati; (2) kelompok per ukuran; anggota grup ≥ 2 di-`Lstat` dan **hard link** (`dev`+`ino` sama) dihitung sekali (path terurut pertama mewakili inode, tidak dihitung sebagai bisa dihemat); (3) digest parsial SHA-256 atas 64 KiB pertama + 64 KiB terakhir — berkas ≤ 128 KiB langsung di-hash penuh; (4) SHA-256 penuh untuk grup parsial ≥ 2; berkas yang berubah ukuran/tipe sejak walk dilewati (`warn`). Progress: `SetTotal` = jumlah kandidat setelah walk, `+1` per berkas selesai di-hash, log per fase; cancel lewat engine (ctx dicek tiap baca 32 KiB/berkas); batas waktu **1 jam** (`context.WithTimeout` di runner) → task `failed` `error.code=TIMEOUT` + log fase & angka ("N file dipindai, x dari y di-hash"), tidak ada hasil parsial. Hasil: grup diurut `reclaimable` turun, path di dalam grup diurut (mtime naik, panjang path naik, path) sehingga `paths[0]` = kandidat "Pertahankan" default (terlama/path terpendek); `reclaimable = Σ size × (n − 1)`; maks 5.000 grup disimpan (`truncated`, `totalGroups`); `shared:true` bila berkas atau folder induknya punya baris `shares` (status apa pun) — satu query per task (`ListSharePathsRelated`: path ancestor scope + prefix). Hasil hanya disimpan di `tasks.result_json` (tanpa tabel/migrasi baru), disajikan `GET /me/duplicates` selama 24 jam. UI (`features/storage-analysis/Duplicates.tsx`): folder scope (default `/`, dialog `FolderPicker`), "Cari duplikat" (nonaktif saat berjalan; progress dari store task/SSE + "Batalkan"), header (jumlah grup, bisa dihemat, file dipindai, waktu, "Hasil disimpan 24 jam"), daftar grup (tervirtualisasi `@tanstack/react-virtual` bila > 50 grup) dengan radio "Pertahankan" (default `paths[0]`) + checkbox "Hapus" (default semua kecuali yang dipertahankan; mengganti yang dipertahankan membalik checkbox grup itu), badge "Dibagikan", "Buka lokasi file" (`/files/<dir>?select=<nama>`); footer "Hapus N item ke Recycle Bin (X)" (`POST /files/delete` lewat `useFileOps`, toast Urungkan), "Ganti dengan symlink", "Pilih semua"/"Batal pilih". Dialog konfirmasi mencantumkan berkas `shared` yang ikut terpilih (share-nya dapat nonaktif `item_trashed`). **Ganti dengan symlink** (`POST /me/duplicates/symlink`): server meng-hash ulang `keep` dan tiap salinan (tidak pernah percaya klien; ukuran/inode/hash beda → 400 tanpa perubahan), memindah semua salinan ke Recycle Bin sebagai satu batch (kuota, `TrashHook`), lalu menulis symlink **relatif** ke `keep` di tiap path; undo (`POST /files/undo`, kind `restore` + `symlinks`) menghapus symlink yang ditulisnya (hanya bila masih symlink dengan target yang sama) lalu memulihkan aslinya. Setelah aksi apa pun UI memperbarui hasil secara lokal (path yang dihapus dibuang, tanpa scan ulang) dan menginvalidasi analisis penyimpanan + kuota; undo menginvalidasi `['duplicates']` sehingga salinan yang pulih tampil lagi.

### 8.8 Upload

**Sumber**: tombol "Upload ▾" (File / Folder), drag-drop ke area listing (overlay "Lepaskan untuk mengunggah ke <folder>") atau tepat ke baris folder (upload ke folder itu), paste (`Ctrl+V` saat clipboard OS berisi gambar → `upload/clipboard`; berisi file → tus), context menu "Upload ke sini".

**Alur client** (`features/upload`):

1. Kumpulkan `File`/entri folder (webkitGetAsEntry rekursif, batas 10.000 file per drop → selebihnya ditolak dengan pesan).
2. `upload/precheck` per 200 file; tampilkan dialog konflik gabungan (Timpa / Lewati / Ganti nama, "Terapkan ke semua", bandingkan ukuran & tanggal) dan daftar yang ditolak (ukuran > maks, ekstensi, kuota tidak cukup → tawarkan lanjut sebagian).
3. Antrian: `concurrency 3`; tiap file `tus.Upload` dengan metadata `dest`, `name`, `relPath`, `policy`, `lastModified`; progress agregat (byte); kecepatan rata-rata jendela 5 detik; ETA.
4. Panel transfer (pojok kanan bawah, bisa diciutkan, tetap ada saat pindah route; `Zustand` persist daftar ke `sessionStorage` untuk resume setelah reload): per file nama, progress, kecepatan, status (`queued|uploading|paused|error|done|skipped`), tombol pause/resume/cancel per file dan semua, "Bersihkan selesai", "Buka folder".
5. Setelah tiap file selesai: invalidasi listing `dest`; toast ringkasan di akhir batch.
6. Error: 4xx (selain 423/429) → status `error` dengan kode; 5xx/jaringan → retry backoff; `QUOTA_EXCEEDED` menghentikan antrian sisa.
7. Reload halaman: panel menampilkan transfer sebelumnya dalam status `paused` + `detached` dengan tombol "Pilih file…" dan toast "N upload belum selesai" (butuh user memilih file lagi karena `File` handle hilang); file yang dipilih dicocokkan dengan transfer berdasarkan nama + ukuran (+ folder relatif), lalu fingerprint tus (`findPreviousUploads`) melanjutkan dari offset server (`HEAD` → `PATCH`, tanpa `POST` baru).
8. Paste (`Ctrl+V` saat clipboard OS berisi file/gambar; listener `paste` dokumen di luar input): gambar dinamai `image-YYYYMMDD-HHMMSS.<ext>` lalu masuk antrian tus biasa (Step 12a: menggantikan endpoint `upload/clipboard`). Drop OS ke baris folder mengunggah ke folder itu; ke area kosong/grid → folder terbuka (overlay "Lepaskan untuk upload ke <folder>", `pointer-events: none`, target drop tetap baris/grid).

**Server**: §6.5. Finalisasi memverifikasi ukuran, mode file `0640`, mtime dari `lastModified` (klien mengirimnya hanya bila pref `upload.preserve_mtime` aktif — Step 12a: klien selalu mengirim; pref dibaca di 18b/19 bila diperlukan), nama akhir sesuai policy; `rename` memakai `files.UniqueName` lalu `MoveIn` dengan `RENAME_NOREPLACE` — `EEXIST` karena balapan → nomor berikutnya.

### 8.9 Download, zip streaming, signed URL

**Signed URL** (`download.Signer`): `key = HKDF(APP_SECRET, "signed-url")`; string yang ditandatangani = `v1|<scope>|<path>|<purpose>|<exp>|<extra>` (setiap field diawali panjangnya, `len|value|`, agar `|` di dalam path tidak ambigu); `scope` = `u:<user_id>` atau `s:<share_id>`; `extra` = `size` untuk thumb, `token` untuk zip; `sig = base64url(HMAC-SHA256)[:32]`. Verifikasi konstan-waktu; `exp` unix detik. TTL: `purpose=download|inline` → `SIGNED_URL_TTL` (5 m); `thumb|media` → `MEDIA_URL_TTL` (2 h; thumb mengikuti media sejak Step 8). Purpose `hls` (Step 23d): `path` = id task `transcode_hls`, `scope` = `u:<pemilik>`, satu tanda tangan untuk seluruh folder (playlist + semua segmen; query `scope&exp&sig` saja, purpose dan id dari route), TTL `MEDIA_URL_TTL`, hanya diterbitkan `POST /files/hls` (tidak diterima `POST /files/sign`); tanda tangan `media` tidak berlaku untuk folder HLS dan sebaliknya (purpose bagian dari HMAC). Signed URL **tidak** memerlukan cookie (dipakai download manager, `<video>`, tab baru). Pencabutan sesi tidak membatalkan signed URL yang masih hidup (diterima; TTL pendek).

**Kebijakan inline vs attachment** (`GET /files/raw`): MIME dideteksi dari magic bytes (`mimetype.DetectReader` 3 KB pertama) dan dicocokkan dengan ekstensi:

| Kelas | Contoh | Perlakuan |
| --- | --- | --- |
| Raster image | jpeg, png, gif, webp, avif, bmp | `inline`, `Content-Type` sesuai deteksi |
| Audio/video | mp4, webm, ogg, mp3, flac, wav, m4a, mov | `inline`, Range |
| PDF | application/pdf | `attachment` (pdf.js memuat via `fetch` + Range; browser tidak merender native) |
| Teks (viewer) | text/plain, json, csv, md | `attachment`; viewer mengambil via `files/content`/`fetch` dan merender sendiri |
| Berbahaya | html, xhtml, svg, xml, js, css?, swf, dll | `attachment` + `Content-Type: application/octet-stream` selalu, apa pun `purpose` |
| Lainnya | | `attachment` |

Header semua respons raw/download: `X-Content-Type-Options: nosniff`, `Content-Security-Policy: sandbox; default-src 'none'`, `Cache-Control: private, max-age=0, must-revalidate` (thumb: `max-age=86400`), `Referrer-Policy: no-referrer`, `Cross-Origin-Resource-Policy: same-origin` (atau `same-site` jika `CONTENT_BASE_URL` di-set), `X-Accel-Buffering: no`, `Accept-Ranges: bytes`, `ETag`, `Last-Modified`, `Content-Disposition` dengan `filename*`.

**File tunggal**: `http.ServeContent(w, r, name, modtime, file)` → Range, If-Range, HEAD, 304. Throughput tidak dibatasi; jumlah download aktif per user maks 3 (`downloads.semaphore`, HTTP 429 `RATE_LIMITED details.reason:"concurrent_downloads"` untuk yang ke-4); viewer media inline tidak dihitung.

**Variant** (`GET /files/raw?variant=`; Step 12b): `thumb` menyajikan thumbnail 512 px dari pipeline §8.4 (202 + `Retry-After` saat job berjalan); `preview` menyajikan WebP ≤ 2048 px (`thumbs.SizePreview`, cache `<h>-2048.webp` di folder yang sama, dirender sinkron oleh `vipsthumbnail -s 2048x2048>` — sufiks `>` = hanya memperkecil, gambar lebih kecil dienkode ulang pada ukurannya sendiri (Step 17a; sebelumnya diperbesar), orientasi EXIF diterapkan otomatis — hanya untuk gambar raster/SVG; video/PDF/audio jatuh ke thumbnail 512 px). Invalidasi thumbnail (§8.4) juga membuang preview.

**Signed URL = bearer token** (SECURITY.md): siapa pun yang memegang URL dapat memakainya sampai `exp` tanpa cookie; karena itu TTL download hanya 5 menit, URL tidak pernah dicatat utuh di log (hanya path + status), dan akun yang dinonaktifkan/dihapus ditolak `403` meski URL masih hidup. Pemakaian oleh download manager pihak ketiga memang diizinkan.

**Zip streaming** (`POST /files/zip` → `GET /files/zip?token`): token = signed URL dengan `extra` = id (16 byte acak, hex) payload `DATA_DIR/tmp/zip-<id>.json` (`{userId, paths, name, method, createdAt}`, disapu setelah 10 menit — sedikit lebih lama dari TTL URL 5 menit) berisi daftar path; payload hanya dapat dibuka oleh user pemiliknya. Handler: `archive/zip.Writer` langsung ke `w` (dibungkus `bufio` 1 MiB), `Content-Type: application/zip`, `Content-Disposition` nama `<folder>.zip` atau `<app>-<yyyyMMdd-HHmm>.zip`, `Transfer-Encoding: chunked` (tanpa `Content-Length`), metode `Store` default (CPU 1 vCPU) / `Deflate` level 1 jika diminta, `zip64` otomatis, nama entri UTF-8 flag, walk tanpa mengikuti symlink dir, symlink file dilewati (dicatat), item yang hilang di tengah dilewati. Read lock pada sumber. Client putus → `ctx.Done` menghentikan walk.

**Download folder di aaPanel** = compress lalu download; di sini zip streaming menggantikan tanpa file sementara; menu "Compress" tetap ada untuk membuat file arsip.

**Nama file UTF-8**: `filename="<ASCII fallback: non-ASCII → _>"; filename*=UTF-8''<url-encode>`.

### 8.10 Remote download & SSRF guard

**Input** (dialog "Remote download", `features/remote-download/RemoteDownloadDialog`): textarea URL satu per baris (maks **10** per permintaan — selaras dengan rate limit 10 task/jam; Step 13), folder tujuan (default folder aktif, `FolderPicker` + path ketik), nama file (hanya saat satu URL; default dari `Content-Disposition filename*`/`filename`, lalu segmen path terakhir URL, lalu `download-<unix>`), policy konflik (`rename` default di UI; server default `fail`), tombol "Periksa" (`probe`). Validasi klien (`parseUrlLines`: skema http/https, tanpa userinfo, ≤ 10) hanya menghemat round-trip; server tetap memutuskan. Tombol toolbar "Remote" dan entri menu area kosong disembunyikan bila `config.remoteDownloadEnabled=false` (`/auth/me`).

**Paket:** `internal/netguard` (SSRF guard, dipakai ulang oleh fitur keluar lain) + `internal/remotedl` (runner task, validasi, rate limit, settings, probe). Task `remote_download` payload `{url, dest, filename?, policy}` (SPEC §7.1), hasil `{path, size, finalUrl (tanpa query string), status: created|renamed|overwritten|skipped, resumedFrom?}`.

1. **Parse URL** (`netguard.Guard.ValidateURL`, dijalankan saat `POST` dan lagi di awal task): skema wajib `http`/`https` (`file:`, `ftp:` → `SSRF_BLOCKED reason:scheme`); userinfo ditolak (`reason:userinfo`); host kosong/`localhost`/`*.localhost` ditolak (`reason:host`); port hanya 80, 443, dan 1024–65535 (`reason:port`); IP literal diperiksa langsung terhadap daftar langkah 3 (`reason:private_ip`, `details.ip`). Pelanggaran kebijakan jaringan → `422 SSRF_BLOCKED`; pelanggaran daftar admin → `403 FORBIDDEN` (`reason:denylist|allowlist`); fitur mati → `403 FORBIDDEN reason:disabled`.
2. **Allow/denylist domain** (settings `remote_download.allowlist|denylist`, array JSON atau string dipisah koma/baris): denylist menang; suffix match (`example.com` = dirinya + subdomain, `*.example.com` = subdomain saja). Jika allowlist tidak kosong → hanya yang cocok. Settings dibaca dengan cache 5 detik (`remote_download.enabled` hanya bisa mematikan, `remote_download.max_size` hanya bisa menurunkan nilai env).
3. **Resolve DNS** (`Guard.Resolve` via `Resolver.LookupNetIP`, `net.DefaultResolver` di produksi) → tolak jika **ada satu pun** alamat yang termasuk: `0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`, `192.0.0.0/24`, `192.0.2.0/24`, `192.168.0.0/16`, `198.18.0.0/15`, `198.51.100.0/24`, `203.0.113.0/24`, `224.0.0.0/4`, `240.0.0.0/4`, `255.255.255.255/32`, `::/128`, `::1/128`, `::ffff:0:0/96` (IPv4-mapped → periksa IPv4-nya), `64:ff9b::/96`, `fc00::/7`, `fe80::/10`, `ff00::/8`, `2001:db8::/32` (+ klasifikasi `netip` sebagai jaring pengaman). Alamat yang lolos dipilih satu (prefer IPv4). Resolusi terjadi **di dalam task** (bukan saat `POST`) agar request cepat; kegagalan → task `failed SSRF_BLOCKED`.
4. **Pin IP** (`Guard.DialContext`): resolusi + validasi dilakukan di dalam dial, lalu `net.Dialer` menerima `ip:port` literal (bukan hostname — tidak ada resolusi kedua, jadi DNS rebinding tidak punya jendela); `Dialer.Control` memeriksa ulang alamat socket (double-check); TLS/SNI tetap memakai hostname dari URL (`http.Transport` tanpa `DialTLSContext`). Transport: `Proxy: nil` (env proxy diabaikan), `DisableCompression`, `TLSHandshakeTimeout` 15 s, `ResponseHeaderTimeout` 30 s, koneksi dibungkus `idleConn` yang memasang deadline baca 60 s per `Read`.
5. **Redirect**: `CheckRedirect` menjalankan langkah 1–2 pada URL baru (langkah 3–4 terjadi saat dial hop itu); maks 5 (`reason:too_many_redirects`); skema non-http(s) ditolak; header `Authorization`/`Cookie` tidak diteruskan. `BlockedError.Hop` mencatat hop yang ditolak.
6. Request: `GET` dengan `User-Agent: FileManager/<version> (+<APP_BASE_URL>)`, `Accept-Encoding: identity`, timeout koneksi 15 s, total maks 24 jam (`context.WithTimeout`). `Range: bytes=<offset>-` + `If-Range` (ETag, atau Last-Modified) untuk resume jika meta part mencatat `Accept-Ranges: bytes`/206 sebelumnya; `200` saat resume → mulai ulang dari nol; `206` dengan `Content-Range` yang tidak cocok → mulai ulang.
7. **Batas ukuran** (dua kelas kode): `size` = min(`remote_download.max_size`, `REMOTE_DOWNLOAD_MAX_SIZE`, `MAX_UPLOAD_SIZE`) → `FILE_TOO_LARGE`; `budget` = min(sisa kuota − reservasi aktif, disk bebas − 512 MB) → `QUOTA_EXCEEDED`. `Content-Length` > batas → gagal sebelum membaca body; tanpa `Content-Length` → `io.LimitReader` + gagal saat melewati batas. Kuota direservasi sebesar `Content-Length` saat respons diterima (`quota.Reserve`), atau sebesar ukuran akhir sebelum `MoveIn` bila panjang tidak diketahui; `Commit(actual − replaced)` setelah file di tempat; `Release` saat gagal/batal.
8. **Part file**: `DATA_DIR/tmp/rd-<task_id>/{part, meta.json}` (`fsjail.SysDir`, `OpenAppend` untuk resume). Putus di tengah → retry **di dalam task** 3× (backoff 5/15/45 s) dari offset part. Gagal setelah itu → task `failed REMOTE_NETWORK` dan part **dipertahankan**; "Retry" (task baru, `retryOf`) membaca `rd-<retryOf>/root` → bekerja di folder root yang sama dan melanjutkan dengan `Range`. Setelah selesai: verifikasi ukuran = `Content-Length`, nama (ekstensi diblokir → `UNSUPPORTED_TYPE`), lock tulis target (`LockManager`, menunggu dalam mode task), policy konflik (`fail` → `ALREADY_EXISTS`; `skip`; `rename` → `UniqueName`; `overwrite` → `MoveIn` ke nama sementara lalu `Rename` atomik), kuota, aktivitas `remote_download`, `OnChange` (thumbnail), invalidasi listing, event `file.changed`, lalu kedua folder kerja dihapus. Blocked/4xx/terlalu besar/kuota/cancel → part dihapus. Job `clean_uploads` tiap jam juga menghapus `rd-*` yang lebih tua dari 48 jam (yatim setelah crash atau retry yang tidak pernah datang).
9. Progress bytes (`rt.Progress` tiap 256 KiB) + kecepatan + ETA dari engine; cancel menghapus part dan melepas reservasi.
10. Audit `remote_download.created` (`details.hosts[]`, `count`, `policy` — host saja, bukan URL); `SSRF_BLOCKED` dicatat `WARN` dengan `reason`, `host`, `ip`, `hop`; log, log task, dan `finalUrl` tidak pernah memuat query string (`netguard.Describe`). Kode error tambahan: `REMOTE_UPSTREAM` (server sumber 4xx/5xx, `details.status`), `REMOTE_NETWORK` (koneksi putus setelah retry).

**Probe** `POST /remote-downloads/probe {url}`: aturan yang sama; `HEAD`, fallback `GET Range: bytes=0-0` bila `HEAD` 405/501/5xx; hasil `{finalUrl, size|null, filename?, acceptsRanges, contentType?}`.

Rate limit: 10 task remote/jam/user (jendela geser, satu batch diterima seluruhnya atau ditolak `429 RATE_LIMITED` + `Retry-After` + `details.retryAfter`); admin bisa menonaktifkan (`REMOTE_DOWNLOAD_ENABLED=false` atau settings → menu/tombol disembunyikan dan endpoint `403 FORBIDDEN`). Mode pengembangan: `DEV_REMOTE_DOWNLOAD_ALLOW_LOOPBACK=true` (hanya `APP_ENV=development`, dipakai E2E dengan server lokal) mengecualikan `127.0.0.0/8` dan `::1` dari langkah 3; produksi mengabaikannya.

### 8.11 Arsip

**Matriks format** (sesuai prompt bagian 9):

| Format | Extract | Compress | Password | Engine extract | Engine compress |
| --- | --- | --- | --- | --- | --- |
| zip | ya | ya | extract ZipCrypto & AES-128/192/256; compress AES-256 (WinZip AE-2) | `archive/zip` (stdlib) + `internal/archive/zipaes` (implementasi sendiri: PBKDF2-SHA1, AES-CTR little-endian, HMAC-SHA1; ZipCrypto decrypt) — diverifikasi dua arah dengan 7-Zip 23.01 (`zipaes_test.go`); `yeka/zip` tidak dipakai (tanpa rilis bertag) | `archive/zip` (Go) + `zipaes` untuk AES-256 |
| 7z | ya | ya | ya (AES-256, `-mhe=on` enkripsi nama) | `bodgit/sevenzip` (library di balik mholt/archives, dipakai langsung agar header & password tersedia; `-mhe=on` terbaca) — reader baru per lintasan karena folder solid adalah satu stream maju; fallback `7z x` (CLI) untuk multi-volume `.7z.001` dan arsip yang ditolak library, password via **stdin** (spike 14a) | `7z a -t7z -mx<N> [-mhe=on] [-v<n>m] -p` + password di stdin |
| rar v4/v5 | ya | **tidak** (keputusan pemilik 14 Okt 2026: tanpa lisensi `rar`, `unrar` tidak dipasang) | extract ya | `nwaples/rardecode/v2` (library di balik mholt/archives); fixture rar tidak ada di repo (tidak ada tool berlisensi untuk membuatnya) → jalur rar hanya diuji unit untuk pemetaan error | — (`canWrite:false`, `reason:rar_licence`) |
| tar | ya | ya | — | Go (mholt/archives) | Go |
| tar.gz / tgz | ya | ya | — | Go (`klauspost/pgzip` jika dibawa archives) | Go |
| tar.bz2 / tbz2 | ya | ya | — | Go | Go (compress/bzip2 hanya decode → encode via `dsnet/compress/bzip2`, verifikasi; jika tidak ada → compress tar.bz2 via `7zz`) |
| tar.xz / txz | ya | ya | — | Go (`ulikunitz/xz`) | Go |
| tar.zst | ya | ya | — | Go (`klauspost/compress/zstd`) | Go |
| gz, bz2, xz, zst (file tunggal) | ya | ya | — | Go | Go |
| war, jar | ya (zip) | opsional (zip dengan ekstensi) | — | Go | Go |
| iso | read-only | tidak | — | `7z l -slt` (pra-pindai + batas) lalu `7z x` ke tmp, hasil disanitasi (link/khusus dibuang) | — |

Deteksi format dari magic bytes (`archive.Detect`: zip/7z/rar/gzip/bzip2/xz/zstd, `ustar` di offset 257, `CD001` di 0x8001, checksum header tar v7) lalu ekstensi hanya untuk memutuskan `tar.<c>` vs file tunggal (tanpa ekstensi: 512 byte hasil dekompresi diperiksa untuk header tar); ekstensi tidak pernah mengalahkan magic (`fake.zip` berisi teks → `UNSUPPORTED_TYPE`). Ekstensi ganda (`tar.gz`, `tgz`, …) dikenali.

**Implementasi Step 14a** (14 Okt 2026): package `internal/archive` (`format.go` deteksi + tabel kemampuan, `entry.go` `Source` per engine, `safe.go` normalisasi nama + batas, `encoding.go` decoder nama, `extract.go`, `compress.go`, `cli.go` 7-Zip, `service.go` validasi/enqueue/`CleanOrphans`) + `internal/archive/zipaes`. `GET /archives/capabilities` mengembalikan matriks yang disesuaikan dengan tool terpasang (`reason: dependency_missing|rar_licence`) dan `passwordStdin:true`. Workspace `DATA_DIR/tmp/ar-<task>` (`root/` extract, `out/` compress) dibungkus `fsjail.Jail` sehingga `Create` lewat `O_NOFOLLOW` dan `Symlink` menolak target yang keluar root; dibersihkan di akhir task dan oleh job `clean_uploads` tiap jam (> 24 jam). Dependensi baru (library di balik `mholt/archives`, dipakai langsung karena header/opsi password dibutuhkan; `mholt/archives` sendiri tidak diimpor): `bodgit/sevenzip` v1.6.1 (BSD-3), `nwaples/rardecode/v2` v2.2.0 (BSD-2), `klauspost/compress` v1.20.1 + `klauspost/pgzip` v1.2.6 (BSD-3/MIT), `ulikunitz/xz` v0.5.15 (BSD-3), `dsnet/compress` (BSD-3) untuk bzip2 write.

**Compress** (task): opsi dari §6.3; output default `<nama item>.<ext>` (banyak item: `<folder induk>.<ext>` atau `Arsip-<tanggal>`); konflik nama output → `rename`. Level: zip `store|fast(1)|normal(6)|max(9)`; 7z `-mx0|1|5|9`; tar.* level codec setara. Split volume (7z `-v<size>`, zip via 7zz `-v`) opsional. `deleteSource` → setelah verifikasi arsip bisa dibuka (listing) → delete ke Recycle Bin (bukan permanen). Progress = byte sumber yang dibaca.

**Extract** (task):

1. Buka arsip (read lock), pre-scan entri: hitung `totalUncompressed`, `entries`; format yang tidak punya ukuran uncompressed di header (gz stream) → batas diterapkan saat streaming.
2. Tolak `ARCHIVE_UNSAFE` jika entri: nama mengandung `..` segmen, diawali `/` atau `\`, drive letter `C:`, mengandung `\0`; symlink/hardlink yang resolusinya keluar folder tujuan (symlink dalam tujuan diperbolehkan tetapi dibuat **setelah** semua file reguler agar tidak bisa dipakai sebagai lompatan); device/FIFO/socket dilewati (warn).
3. Tolak `ARCHIVE_LIMIT` jika `entries > ARCHIVE_MAX_ENTRIES`, `totalUncompressed > min(ARCHIVE_MAX_EXTRACT_SIZE, sisa kuota, disk−512MB)`, atau rasio `totalUncompressed / archiveSize > 200` dengan `totalUncompressed > 1 GB` (nilai ekstrem untuk bomb; file teks sangat kompresibel sah < 1 GB tetap lolos). Saat streaming, hentikan jika byte tertulis melewati batas (bom yang berbohong di header).
4. Extract ke `DATA_DIR/tmp/<task_id>/root/` memakai `*os.Root` pada folder itu (zip-slip mustahil lewat kernel), mode file `0640`, dir `0750`, setuid/setgid/sticky dibuang, mtime dipertahankan, nama entri dinormalisasi NFC + `ValidateName` per segmen (nama invalid → entri dilewati + warn; ekstensi diblokir → dilewati).
5. Encoding nama (zip tanpa flag UTF-8): `auto` → deteksi chardet pada gabungan nama (kandidat GBK, Shift_JIS, Big5, CP437, Windows-1252) dengan ambang keyakinan; pilihan manual memaksa. Tar: UTF-8 (fallback Latin-1 jika invalid).
6. Password: zip ZipCrypto/AES → `zipaes` (password dari `rt.Secret("password")`, hanya di memori engine; payload menyimpan `hasPassword`); 7z → library (`bodgit/sevenzip`), fallback CLI `7z x -y -bd -bsp1 -bso0 -o<tmp> -- <archive>` **tanpa** `-p`: 7-Zip 23.01 mencetak `Enter password (will not be echoed):` dan membaca satu baris dari stdin meskipun stdin pipe (spike 14a, terverifikasi; stdin `/dev/null` → exit 255 = `ARCHIVE_PASSWORD_REQUIRED`, exit 2 `Wrong password`/`Can't open as archive` = `ARCHIVE_PASSWORD_WRONG`); membuat 7z: `7z a … -p` (switch tanpa nilai) + password di stdin. Password tidak pernah masuk argv (`TestSevenZipPasswordNeverInArgv` memindai `/proc/*/cmdline` selama task). rar → library dengan opsi `Password`. Retry task ber-password tanpa secret → gagal cepat `ARCHIVE_PASSWORD_REQUIRED` (UI meminta password lagi).
7. Password salah: zip → `ARCHIVE_PASSWORD_WRONG` saat verifikasi check byte/HMAC entri pertama; 7z → exit code 2 + stderr "Wrong password" (parsing teks, verifikasi string per versi).
8. Pindah atomik: tujuan `here|new|<path>`; `new` = folder bernama arsip tanpa ekstensi (`rename` jika ada); jika tujuan belum ada → `Rename(tmp/root → dest)` satu kali; jika tujuan ada (extract "di sini") → per entri tingkat atas: `Rename` ke tujuan dengan policy (`overwrite`: file menimpa, folder merge rekursif per file; `skip`; `rename`).
9. Kuota: pre-scan + `quota.Apply` di akhir dengan byte nyata; thumbnail diantrekan; `file.changed`.
10. Arsip bersarang tidak diekstrak otomatis.

**Archive browser** (Step 14b, 14 Okt 2026): `GET /archives/entries` membaca daftar entri tanpa extract (`Source.Index` untuk zip/7z/rar, satu lintasan `Walk` tanpa membaca body untuk tar.*/stream, `7z l -slt` untuk iso dan 7z yang ditolak library) menjadi pohon (`internal/archive/index.go`: folder implisit disintesis, folder dulu lalu natural sort, `Children`/`Members`/`Lookup`; nama yang bukan path valid dihitung `unsafe` dan tidak didaftarkan), di-cache per (user, path, size, mtime_ns, encoding) selama 10 menit (maks 16 arsip) sehingga navigasi antar folder tidak membaca ulang file; paging 1000 dengan cursor offset. Password lewat header `X-Archive-Password` (query ditolak `400`), diverifikasi pada entri terenkripsi pertama, lalu disimpan di memori sebagai **sesi arsip** (`session.go`: id acak 16 byte, terikat user + path, TTL 10 menit sliding, maks 32/user, dihapus saat arsip ditulis ulang) yang dikembalikan sebagai `session` dan ditempel `POST /archives/sign` ke URL `entry-content` (`&sess=`) — password tidak pernah ada di URL, log, maupun payload task (`hasPassword`). `GET /archives/entry-content` (signed, purpose `entry`, TTL `MEDIA_URL_TTL`) men-stream satu entri ≤ 50 MB langsung dari arsip lewat `io.Pipe` (`browse.go` `OpenEntry`: akses acak zip/7z/rar, walk maju untuk tar.*; 3 KB pertama di-peek untuk MIME dan agar password salah/data korup menjadi kode status, bukan body terpotong) — tidak ada file di `DATA_DIR/tmp`; kebijakan inline ADR-06 sama dengan `/files/raw`; unduh entri memakai URL yang sama lewat `<a download>` (endpoint `entry-download` terpisah tidak dibuat). Extract sebagian = `POST /archives/extract-entries` → task `extract` dengan `entries` (folder = prefix), nama dicek ke indeks dulu. `supportsModify=true` hanya zip tanpa enkripsi ≤ 2 GiB (`modifyReason` lainnya): `POST /archives/add` dan `/archives/remove-entries` → task `zip_edit` (`zipedit.go`): zip baru ditulis di `DATA_DIR/tmp/ar-<task>/out`, entri yang dipertahankan disalin **raw** (`zip.File.OpenRaw` → `zip.Writer.CreateRaw`, extra zip64 lama dibuang, tanpa dekompresi ulang), file baru deflate (nama sama = ganti), hasil diverifikasi `zip.NewReader`, dipindah ke folder arsip (`.fm-zipedit-<task>-<nama>`) lalu `Rename` menggantikan asli — gagal/batal di tengah → asli utuh; kuota direservasi untuk byte baru dan di-commit dengan delta ukuran nyata; indeks + sesi arsip diinvalidasi, `file.changed`. Frontend: overlay `?archive=<path>` (`features/archive/ArchiveBrowser.tsx`: breadcrumb dalam arsip, `EntryTable` tervirtualisasi `role=grid` kolom Nama/Ukuran/Terkompresi/Rasio/Diubah dengan sort klien, seleksi Shift/Ctrl/Space/Ctrl+A, Enter/Backspace/Delete, `EntryPreview` teks ≤ 1 MB & gambar ≤ 20 MB via URL bertanda tangan, dialog Extract semua/yang dipilih, Tambah file (FolderPicker mode `all`), Hapus entry (konfirmasi), `PasswordPrompt` sekali per overlay, pemilih encoding nama yang memuat ulang indeks, context menu konteks `archive`); "Buka" dan "Lihat isi arsip" pada file arsip membuka overlay ini.

**CLI**: `exec.CommandContext(ctx, SEVENZIP_PATH, "a", "-t7z", "-mx5", "-mhe=on", "-bd", "-y", "--", out, in...)`, `Dir` = `tmp/<task>`, env minimal, `nice -n 10`, `ionice -c3`, timeout = 6 jam, output dibatasi; progress 7zz via `-bsp1` parsing persen (verifikasi format) atau fallback progress berbasis ukuran output.

**Test fixture** (dibuat saat test oleh `internal/archive/fixtures_test.go` — tidak ada biner buram di repo; `encrypted.7z`/AES zip dari 7-Zip dibuat saat test dan di-skip tanpa binary): `slip.zip` (`../../evil.txt`), `abs.zip` (`/etc/evil`), `symlink-escape.tar` (symlink `x → ../../etc`, lalu `x/evil`), `hardlink-escape.tar`, `bomb-small.zip` (10 MB nol → ratio tinggi, 1 file 10 MB; test memaksa `ARCHIVE_MAX_EXTRACT_SIZE=1MB`), `many-entries.zip` (10.001 entri kosong, test `ARCHIVE_MAX_ENTRIES=10000`), `nested.zip`, `gbk-names.zip`, `aes.zip` (password `secret`), `zipcrypto.zip`, `test.7z` (dibuat saat test jika `7zz` ada; jika tidak, test di-skip), `windows-names.zip` (`CON.txt`, `a .txt`).

### 8.12 Search & replace

**Pencarian nama** (`GET /search/names`): walk `Jail.WalkDir` dari `path` (default `/`), `recursive` default true, cocokkan substring case-insensitive pada nama (`strings.Contains` setelah `cases.Fold`), opsional wildcard `*`/`?` (diubah ke regex) dan `regex:true`; filter `types` (kelas MIME dari ekstensi), `ext`, `minSize/maxSize`, `from/to` (mtime), `tag` (join DB); batas 5.000 hasil atau 20 detik (`truncated:true`); hasil diurutkan (nama natural) dan dipaging dari cache hasil 60 detik (cursor); kolom Lokasi; UI: input di address bar "Cari di folder ini" (Ctrl+F) + halaman `/search` global dengan panel filter & riwayat 50 kueri (satu baris per (kind, query, scope); pencarian ulang memperbarui waktunya). Rate limit 30/menit/user.

**Pencarian isi** (`POST /search/content`): engine `rg` jika `RG_PATH` ada:

```text
rg --json --no-ignore --hidden --no-follow --no-messages -n --column \
   --max-filesize <maxSize|50M> --max-count <perFile 200> \
   [-i|-s] [-w] [-F (plain) | -e (regex)] \
   [-g '*.ext' ...] [--max-depth 1 jika !recursive] \
   -- <pattern> .
```

dijalankan dengan `Dir` = host path folder (via `HostPath`), `nice`, timeout 30 s, output `--json` diparse streaming (`type: match` → `{file, line, col, text (dipotong 500 char), before/after dari `-C 1`}`), berhenti setelah `maxMatches` (2.000) lalu kill proses; filter `minSize/from/to` diterapkan server setelah match (rg tidak punya) via `Lstat` cache. Fallback Go: worker pool 2 goroutine, `regexp` (RE2; pola plain di-`QuoteMeta`), baca file ≤ maxSize dengan `bufio.Scanner` buffer 1 MB, deteksi biner (NUL di 8 KB pertama → lewati), hasil identik formatnya. Response NDJSON streaming; client merender incremental dengan virtual list; klik hasil → buka editor di baris itu (`/editor?path=&line=`). Rate limit 10/menit/user; satu pencarian isi aktif per user (yang baru membatalkan yang lama).

**Replace in files**: `replace/preview` (task): jalankan pencarian yang sama, untuk tiap file cocok hitung hasil replace di memori (regex `ReplaceAllString` dengan `$1`, plain `strings.ReplaceAll`), simpan hunk (baris sebelum/sesudah, maks 50 hunk per file ditampilkan; sisanya dihitung) ke `tmp/sr-<task>/preview.json` (pratinjau terikat user, kedaluwarsa 10 menit; hasil task memuat 20 file pertama lengkap, sisanya hitungan); UI menampilkan diff per file dengan checkbox file. `replace` (task): untuk tiap file terpilih: lock write, baca ulang, verifikasi masih cocok dengan preview (hash isi sama; jika beda → file dilewati `warn`), buat versi `replace_in_files` (pre-image), tulis atomik (tmp + rename, pertahankan mode & encoding & EOL), aktivitas `replace`; result `{files, replacements, versionIds}` + log; rollback = restore versi per file (tombol "Batalkan replace" di hasil task memanggil `versions/{id}/restore` berurutan).

### 8.13 Editor & versi

**Workspace** `/editor?path=<file>&line=&col=` (route penuh) atau modal besar dari file manager (tombol maximize membuka route). Layout sesuai wireframe prompt bagian 7: toolbar, explorer kiri (lebar 240 px, bisa disembunyikan), tab bar, Monaco, status bar.

**Membuka file** (`GET /files/content`):

1. `Lstat`; dir → `VALIDATION_FAILED`; ukuran > `EDITOR_MAX_FILE_SIZE` → `FILE_TOO_LARGE` kecuali `tail=N` (default 1.000 baris; server membaca dari akhir file dengan `Seek` mundur blok 64 KB sampai N baris, maks 2 MB) → `truncated:true`, editor read-only "tail mode" dengan tombol "Muat 1.000 baris lagi" (tail N+1000) dan "Unduh".
2. Deteksi biner: NUL byte dalam 8 KB pertama atau rasio non-teks > 30 % (mengikuti heuristik git) → `BINARY_FILE` (UI: pesan + tombol download; gambar → viewer).
3. Encoding: BOM (UTF-8/UTF-16 LE/BE) → pasti; tanpa BOM → valid UTF-8? → `utf-8`; selain itu `chardet` (kandidat GBK, Big5, Shift_JIS, EUC-KR, Windows-1252, ISO-8859-1, KOI8-R) dengan keyakinan ≥ 0,5, fallback `windows-1252`. Konversi ke UTF-8 via `x/text/encoding`; karakter tak terpetakan → U+FFFD + flag `lossy:true` (editor memperingatkan "simpan dengan encoding lain dapat menghilangkan karakter").
4. EOL: hitung `\r\n` vs `\n`; `crlf` jika ≥ 1 CRLF dan tanpa LF tunggal; `mixed` jika campur (status bar menunjukkan; simpan mempertahankan pilihan user, default = dominan).
5. Bahasa: ekstensi → id Monaco (tabel §9.8 i18n bukan; tabel di `web/src/features/editor/languages.ts`), nama file khusus (`Dockerfile`, `Makefile`, `.env*`, `nginx.conf`/`*.conf` di folder `nginx`, `.htaccess`/`httpd.conf` → apache, `composer.json` → json, `*.blade.php` → php+html), shebang baris pertama (`#!/bin/bash` → shell, `python`, `node`, `php`), fallback `plaintext`. Monarch grammar kustom untuk `nginx`, `apache`, `ini` (bawaan), `batch`, `verilog`, `vbscript`, `jsp` (html+java), `makefile` (bawaan? verifikasi), `blade`.
6. Response membawa `etag` (dan `mtime`).

**Menyimpan** (`PUT /files/content`, `If-Match`):

1. Lock write; `Lstat`; `etag` server ≠ `If-Match` dan `!force` → `CONFLICT` dengan `serverEtag`, `serverMtime` → UI dialog: "Lihat diff" (ambil isi server → Monaco diff editor), "Timpa" (`force:true`), "Muat ulang".
2. Encode isi ke `encoding` yang diminta (BOM dipertahankan sesuai pilihan), EOL sesuai `eol` (`lf|crlf|keep` → `keep` = tidak mengubah baris yang ada; Monaco diset `eol` agar baris baru konsisten).
3. Versi: jika `FILE_VERSION_LIMIT` > 0 dan file ada dan `sha256(isi lama)` ≠ `sha256(isi baru)` → salin isi lama ke `versions/<uid>/<version_id>` (`CopyFile` atau `Rename` file lama lalu tulis baru → pilih **rename lama → tulis baru**: atomik, tanpa membaca ulang), baris `file_versions` (`reason: save|force_save`), pangkas > limit, `quota.Apply(versions +size)`.
4. Tulis atomik: `tmp` di folder yang sama (`.<name>.fm-tmp-<id>`) → `fsync` → `Rename` → mode & mtime baru; kuota delta.
5. Response `etag` baru + versi; aktivitas `edit`.
6. Isi sama persis → `200` tanpa menulis dan tanpa versi (`noop:true`).

**Riwayat versi**: panel kanan di editor dan di Properties: daftar (no, waktu, ukuran, reason), "Buka" (tab read-only), "Bandingkan dengan sekarang" (Monaco diff side-by-side, bisa inline), "Pulihkan" (konfirmasi; membuat versi `restore` dari isi sekarang), "Unduh", "Hapus". Versi untuk file yang dipindah ikut (prefix rewrite); file dihapus permanen → versi dihapus.

**Catatan implementasi 16a (2026-10-15):** Monaco menormalkan akhir baris model ke EOL dominan saat file dibuka, sehingga file `mixed` tampil "Campuran" di status bar hanya sampai simpan pertama (hasil simpan = EOL dominan, atau pilihan user lewat status bar); byte file `lf`/`crlf` yang tidak diubah tetap identik (`PUT` dengan `eol:"keep"` + `noop`). File di atas `EDITOR_MAX_FILE_SIZE` dibuka otomatis dalam tail mode (klien mencoba `tail=1000` setelah `FILE_TOO_LARGE`). Bahasa Monarch kustom yang disertakan: `nginx`, `apache`, `makefile`; `ini` (termasuk `.env`), `bat`, `systemverilog`, `vb`, `r`, `dockerfile`, `shell` memakai grammar bawaan Monaco 0.57; `*.blade.php` → `php`, `*.jsp` → `html` (tanpa grammar khusus). Worker Monaco (`editor`, `json`, `css`, `html`, `ts`) dibundel Vite (`?worker`, origin sama) — CSP `worker-src 'self' blob:` sudah diaktifkan di `InitialCSP` bersama `style-src 'unsafe-inline'` (diperlukan `<style>` Monaco), sisanya sesuai daftar §10.3 tanpa nonce.

**Catatan implementasi 16b (2026-10-15):** `GET /versions/{id}/download` dilayani **langsung** sebagai attachment terikat sesi (owner-only, Range/ETag, kebijakan §8.9) — bukan "signed redirect" — karena route sudah milik pemilik dan tidak perlu dibagikan; `GET /files/versions?path` = alias `GET /versions?path`; `DELETE /versions?path` menghapus semua versi satu file. Versi untuk file yang dihapus permanen/dipurge dari Recycle Bin: `versions.Store` mendaftarkan `ReferenceUpdater` `file_versions` sendiri sehingga blob ikut dihapus dan `versions_bytes` dikembalikan dalam transaksi operasi (rename/move hanya menulis ulang path; blob dikunci oleh id). Upload `onConflict=overwrite` mengambil pre-image `external_overwrite` hanya untuk file teks ≤ `EDITOR_MAX_FILE_SIZE` (header `Upload-Version` membawa id-nya); replace in files sudah memakai `replace_in_files` sejak Step 15. Dialog konflik memuat Monaco DiffEditor (server kiri, milikmu kanan; toggle inline) dengan "Timpa", "Muat ulang", dan "Gabung manual" (sisi kanan dapat diedit → "Terapkan & simpan" = `force:true`). Riwayat versi: dialog dari status bar "Riwayat (n)" dan tab Properties → Riwayat versi (buka hanya-baca sebagai tab `path#v<id>`, bandingkan, pulihkan dengan konfirmasi, unduh, hapus satu/semua); tab versi tidak ikut `editor_sessions`. Find in files = panel di explorer (`Ctrl+Shift+F`, seed = seleksi) memakai `POST /search/content` dibatasi folder (default folder tab aktif), klik hasil membuka tab dengan kecocokan terseleksi (kursor di awal kecocokan), "Replace in files…" membuka panel pratinjau Step 15. Format document: JSON tidak valid → pesan posisi, isi tidak diubah; Markdown/YAML lewat Prettier standalone (chunk terpisah, pref `editor.format_with_prettier`, plugin PHP tidak dibawa); lainnya pemformat bawaan Monaco (json/css/scss/less/html/js/ts), selain itu "tidak ada pemformat". `Shift+Alt+F` di dalam Monaco dicegat agar lewat jalur ini. Markdown preview memakai `marked` (gfm) + DOMPurify profil ketat (`FORBID_TAGS` style/script/iframe/object/embed/form/input/button/textarea/select/link/meta/base/svg/math + audio/video/source/track/map/area (review D), `FORBID_ATTR` style/srcset/formaction/xlink:href/usemap, URI hanya `http(s)`/`mailto`/relatif): gambar relatif → signed URL `media` bila file ada, gambar eksternal/gagal → placeholder; link relatif membuka file di editor, link eksternal `target=_blank rel=noopener`; sinkron scroll persentase. Aksi aplikasi di palet `F1` Monaco: Save all, Close others, Markdown preview, Reopen with encoding (dialog pilih encoding), Riwayat versi, Cari di file, Format. Tail mode: banner memuat alasan (batas `EDITOR_MAX_FILE_SIZE`), "Muat 1.000 baris lagi" (memuat ulang `tail` lebih besar), "Unduh untuk mengedit". `EditorWorkspace` menerima prop `scope` (share, Step 18b) tanpa efek. Listing menyembunyikan nama staging `.fm-tmp-*`/`.fm-upload-*`/`.fm-zipedit-*` juga saat `hidden=true`.

**Tab & sesi**: maks 20 tab; indikator ● unsaved; menu tab: Close, Close others, Close to the right, Close all, Copy path, Reveal in explorer; reorder DnD; `editor_sessions` disimpan tiap 2 detik setelah perubahan (debounce) dan dipulihkan saat buka `/editor` (isi file dimuat ulang dari server; perubahan belum disimpan dipulihkan dari draft lokal).

**Draft lokal**: IndexedDB (`idb-keyval`), key `draft:<userId>:<path>`, isi + etag dasar + waktu, disimpan tiap 1 detik idle setelah perubahan; saat membuka file dengan draft: jika `draft.etag == server.etag` → tawarkan "Pulihkan draft"; jika beda → tawarkan "Pulihkan draft (file di server sudah berubah, lihat diff)"; draft dihapus saat simpan sukses atau user memilih buang; retensi 7 hari.

**Explorer**: tree lazy (`files/list types=folder,file`), filter nama (client, pada node yang sudah dimuat + tombol "cari di server"), context menu buat file/folder, rename, hapus (ke Recycle Bin), refresh; sinkron dengan clipboard/aksi file manager via query invalidation.

**Fitur Monaco yang diaktifkan**: multi-cursor, find/replace (regex/case/whole word) bawaan, folding, bracket matching, minimap (pref), word wrap (pref), render whitespace (pref), `Ctrl+G` go to line (aksi kustom karena Monaco default `Ctrl+G` ada, tetap dipakai), `F1` command palette, `Ctrl+P` quick open (dialog cari file di root), `Ctrl+Shift+F` find in files (panel hasil pencarian isi, klik → buka di baris), format document (`Shift+Alt+F`) untuk json/css/html/js/ts via Monaco; Prettier (lazy, pref `editor.format_with_prettier`) untuk md/yaml/scss/php?(tidak; plugin php tidak dibawa) — verifikasi plugin yang dibawa; auto-save (pref, interval 2 s setelah idle, hanya jika tidak ada konflik).

**Pengaturan** (pref `editor.*`): `theme` (`system|light|dark|monokai|solarized`? → minimal `system`, `light`, `dark`, `high-contrast`), `font_family` (JetBrains Mono default, Consolas, Menlo, Ubuntu Mono, monospace), `font_size` 10–24, `tab_size` 2/4/8, `insert_spaces`, `word_wrap`, `minimap`, `render_whitespace`, `line_numbers`, `auto_save`, `format_on_save` (off), `bracket_pair_colorization`.

**Markdown**: tombol "Preview" → split kanan (`marked` + DOMPurify, tanpa raw HTML, gambar relatif dipetakan ke signed URL), sinkron scroll sederhana (persentase).

**Share Editor**: file di "Dibagikan ke saya" dengan permission `editor` dibuka dengan `share=<id>`; versi disimpan atas nama **pemilik** (`file_versions.user_id = owner`), aktivitas dicatat untuk keduanya (`details.actor`).

**Shortcut editor**: `Ctrl+S` save, `Ctrl+Shift+S` save all, `Alt+W` tutup tab (`Ctrl+W` tidak bisa), `Ctrl+Tab` tab berikut (dapat dicegat di dalam elemen fokus? → tidak di semua browser; pakai `Alt+→/←` juga), `Ctrl+G`, `F1`, `Ctrl+P`, `Ctrl+Shift+F`, `Esc` tutup panel.

**Yang tidak ada**: terminal, menjalankan kode, LSP server-side (completion hanya bawaan Monaco per bahasa).

### 8.14 Viewer & player

**Pemetaan MIME → viewer** (`web/src/features/viewer/registry.ts`; server menyediakan `mime` + `typeLabel`):

| Kelas / MIME | Viewer | Sumber data |
| --- | --- | --- |
| `image/jpeg, png, gif, webp, avif, bmp, x-icon` | ImageViewer | `raw?purpose=media` (asli) dan `raw?purpose=media&variant=2048` (resized server via vips, cache seperti thumb) |
| `image/svg+xml` | ImageViewer (raster) | `thumb?size=2048`-style raster (`variant=raster`), **bukan** SVG mentah |
| `image/tiff, image/heic, image/heif` | ImageViewer via konversi | `variant=2048` (jika vips mendukung; jika tidak → kartu info + download) |
| `application/pdf` | PdfViewer (pdf.js) | `raw?purpose=media` via fetch + Range (`disableAutoFetch`, `rangeChunkSize` 64 KB = bawaan pdf.js; Step 17a: 512 KB diturunkan karena halaman pertama PDF besar butuh beberapa potongan kecil, bukan satu potongan besar) |
| `audio/*` (mp3, m4a/aac, ogg, opus, wav, flac) | AudioPlayer + mini-player | `raw?purpose=media` (Range) + `files/media-info` → `tags`, cover `raw?variant=thumb` (512 px dari pipeline thumbnail) |
| `video/mp4, webm, ogg, quicktime, x-matroska, x-msvideo` | VideoPlayer (Video.js 10) | `raw?purpose=media`; `canPlayType` + `codecVideo/codecAudio` dari `files/media-info` (ffprobe) → jika tidak bisa diputar: pesan "Codec <x> tidak didukung browser ini" / "Format <x> …" + download, dan tombol "Putar dengan konversi (lambat)" bila `config.transcodeEnabled` (Step 23d, HLS `files/hls`) |
| `text/*`, `application/json`, `application/x-yaml`, `application/xml` (tampil sebagai teks), kode (dari ekstensi) | TextViewer (read-only, highlight ringan `highlight.js` 11.12.0 — core + satu chunk lazy per bahasa, diputuskan Step 17b; ≤ 2 MB utuh, lebih besar → 256 KB pertama lewat Range) + tombol "Edit" | `files/content` (≤ 2 MB) / `raw` + `Range` |
| `text/markdown` | MarkdownViewer (marked + DOMPurify, tab "Sumber") | `files/content` |
| `text/csv`, `text/tab-separated-values` | TableViewer (parser streaming milik sendiri `text/csv.ts` — Step 17b, bukan papaparse; maks 100.000 baris, virtual) | `raw?purpose=media` + `Range: bytes=0-(20 MB−1)`, di-parse selama mengalir |
| `application/json` | JsonViewer (tree collapsible, ≤ 5 MB; lebih besar → TextViewer) | `files/content` |
| `text/html`, `application/xhtml+xml` | TextViewer sumber (preview live ditunda, ADR-06) | `files/content` |
| arsip (§8.11) | ArchiveBrowser | `archives/entries` |
| `application/vnd.openxmlformats-*`, `application/msword`, dll | InfoCard (ikon, nama, ukuran, tipe, download) | — |
| lainnya | InfoCard | — |

**Kerangka overlay** (`ViewerShell`): layar penuh (`role="dialog" aria-modal`), header: nama file, indeks "3 / 24", tombol prev/next (←/→, hanya file sekelas di folder yang sama berdasarkan listing yang sedang dimuat), download, share, info (panel kanan), tombol "Edit" (teks), close (Esc). Deep link `?view=<path>` pada route folder; tombol browser Back menutup overlay. Quick Look (Space) = overlay yang sama dengan ukuran 80 % dan tanpa navigasi folder; Space lagi/Esc menutup.

**ImageViewer**: `canvas`/`img` dengan transform; zoom scroll (ctrl+scroll di trackpad) 10 %–800 %, pinch (Pointer Events), tombol +/−, 1:1, fit, fill; pan drag; rotate 90° kiri/kanan, flip H/V (transformasi tampilan saja, tidak menulis file); fullscreen (Fullscreen API); slideshow (interval 2/5/10 s, loop, tombol play/pause); filmstrip thumbnail bawah (thumb 128, scroll ke aktif); panel info (`GET /files/media-info`): dimensi, ukuran, MIME, EXIF ringkas (kamera, lensa, tanggal, exposure, ISO, f, focal), GPS disembunyikan kecuali pref `exif.show_gps` (tombol "Tampilkan lokasi" per sesi → permintaan ulang dengan `include_gps=1`; koordinat tidak pernah ada di respons default); gambar resized 2048 dimuat dulu, asli saat zoom > 100 % atau tombol 1:1; preload gambar berikutnya (resized); animasi GIF diputar dari asli.

**PdfViewer** (Step 17a, pdf.js 6.4.299): `pdfjs-dist` dengan worker self-hosted (`pdf.worker.min.mjs` via `?url`, ES module dari `/assets/`), komponen `PDFViewer`/`PDFLinkService`/`PDFFindController`/`EventBus` dari `pdfjs-dist/web/pdf_viewer.mjs` (satu chunk lazy ±52 KB gz + CSS 42 KB gz; `pdf_viewer.mjs` membaca `globalThis.pdfjsLib` saat evaluasi, jadi library dimuat dulu lalu diekspos), aset runtime (CMaps, 14 font standar, WASM OpenJPEG/JBIG2/qcms, ikon anotasi) disalin `scripts/pdfjs-assets.mjs` ke `public/pdfjs/` (di-gitignore, 4,3 MB) dan diembed bersama dist; `PDFViewer` memvirtualisasi halaman (merender yang terlihat + buffer, cache 10 halaman → E2E 500 halaman ≤ 12 canvas setelah scroll ke akhir), toolbar: sidebar thumbnail halaman (dirender sendiri lewat `page.render` + `IntersectionObserver`, cache per dokumen; `PDFThumbnailViewer` tidak ada di bundel komponen) & outline (`getOutline` → `linkService.goToDestination`), zoom (otomatis, lebar halaman, seluruh halaman, 50–400 %, +/−), input halaman "3 / 120", cari teks (`find` event → `PDFFindController`, highlight semua, next/prev, hitungan `updatefindmatchescount`; `Ctrl+F` di scope viewer; Esc di kotak cari hanya menutup kotak cari), rotasi, print (render ≤ 200 halaman ke JPEG blob di `div.fm-pdf-print-root` + `@media print` → `window.print()`; **tanpa iframe** karena `frame-src 'none'`), download; sumber signed URL `purpose=media` + Range (`rangeChunkSize` 64 KB, `disableAutoFetch`; E2E: PDF 100 MB → halaman 1 tampil setelah < 8 MB ditransfer lewat respons 206), 403 kedaluwarsa → tanda tangan ulang sekali; PDF ber-password → form `onPassword` (`NEED_PASSWORD`/`INCORRECT_PASSWORD`); layer teks untuk seleksi (`textLayerMode` ENABLE); anotasi `AnnotationMode.ENABLE` (tampil + link internal/eksternal `noopener`, tanpa form/editor: `annotationEditorMode` DISABLE); `enableXfa:false`; tanpa l10n Fluent (shim `nullL10n`).

**AudioPlayer**: `HTMLAudioElement` tunggal global (di `app/` agar bertahan lintas route), UI custom: cover, judul/artis/album (dari `media.tags`, fallback nama file), progress seek, waktu, play/pause, prev/next, shuffle, repeat off/all/one, volume & mute (persist pref), kecepatan 0.5–2×; playlist otomatis = semua audio di folder saat diputar (urutan listing), panel playlist (DnD reorder, hapus, "Tambah dari seleksi"); mini-player sticky bawah (tinggi 56 px) saat overlay ditutup, tombol "buka penuh"; Media Session API (`metadata`, handler play/pause/prev/next/seekto); format tidak didukung (`canPlayType` kosong) → pesan + download.

**VideoPlayer** (Video.js 10 via `@videojs/react`; fallback media-chrome per ADR-07 — Vidstack/Plyr deprecated per 14 Okt 2026): sumber `raw?purpose=media` (Range → seek instan), kontrol: play/pause, seek, volume, kecepatan 0.25–2×, fullscreen, PiP, caption; subtitle sidecar `<nama>.vtt`/`<nama>.srt`/`<nama>.<lang>.srt` dicari di folder yang sama (server mengembalikan `subtitles:[{path, name, lang?, label, format, url}]` di `files/media-info` untuk video; srt dikonversi ke vtt dan vtt disanitasi oleh `GET /files/subtitle` — Step 17b, menggantikan rencana `files/stat` + `raw?as=vtt`); posisi terakhir disimpan di server `PUT /me/media-position` (tabel `media_positions`, 500/user LRU — Step 17b, menggantikan `localStorage` + pref `video.positions`) → resume dengan prompt "Lanjutkan dari 12:34?"; playlist video folder (panel kanan, auto-next opsional); shortcut Space, ←/→ 5 s, J/K/L (−10 s / pause / +10 s), F, M, ↑/↓ volume, 0–9 lompat persen; `onError` → cek `SIGNATURE_EXPIRED` → minta signed URL baru, set `currentTime` kembali, lanjut; codec tidak didukung (HEVC di Chrome Linux, AVI/MKV tergantung) → kartu pesan + download; sejak Step 23d + tombol konversi HLS bila admin mengaktifkannya (paragraf "Transcoding HLS" di bawah).

**Catatan implementasi 17b (2026-10-21).** *Audio*: store `stores/player.ts` (queue, urutan main, shuffle = permutasi acak dengan lagu aktif di depan, repeat off/all/one — "one" mengulang lagu saat selesai, "Berikutnya" manual tetap maju; "Sebelumnya" > 3 s mengulang lagu), elemen `HTMLAudioElement` tunggal di `features/viewer/audio/engine.ts` (di luar React; dimulai oleh `MiniPlayer` di `AppShell`), "Buka" file audio → playlist = file audio folder (urutan listing yang terlihat) → dialog pemutar penuh; menutup dialog → mini-player 56 px di bawah layar (dipasang di `AppShell`; di halaman browser di-portal ke slot tepat di atas status bar lewat `miniPlayerSlot.ts`, di route lain baris terakhir; panel transfer/task bergeser ke atasnya); logout, sesi berakhir (401) atau user lain masuk di tab yang sama mengosongkan pemutar (`engine.ts` mengikuti `useSessionStore`); Quick Look audio menjeda pemutar global; aksi konteks `addToPlaylist` ("Tambah ke playlist", file/multi, hanya bila seleksi berisi audio). Media Session: `metadata` (judul/artis/album/cover `variant=thumb`) + handler play/pause/previoustrack/nexttrack/seekto/seekbackward/seekforward + `setPositionState`. *Video*: `@videojs/react` 10.1.0 (`VideoPlayer` + `Video` + `Container`/`Controls`/`TimeSlider` + store `usePlayer`) dengan kontrol milik aplikasi — skin bawaan tidak dipakai karena pintasan & daftar kecepatan tetapnya (0.2…2, issue v10 #1404) tidak sesuai §9.6/0.25–2×; subtitle sidecar: `files/media-info.subtitles` (server mencari `<nama>.vtt|srt` dan `<nama>.<label>.vtt|srt`, `.vtt` menang atas `.srt` berlabel sama) → `<track kind="subtitles">` dengan URL `GET /files/subtitle` (signed `media`, TTL `MEDIA_URL_TTL`); server mengonversi SRT → WebVTT dan menyusun ulang VTT: hanya timing, setting cue yang valid (`vertical|line|position|size|align`), tag `<b> <i> <u>`; tag lain/`STYLE`/`REGION`/`NOTE` dibuang, `< > &` lepas di-escape; encoding dideteksi seperti editor; track pertama `default`. Posisi tonton lewat `GET/PUT /me/media-position` (disimpan tiap 5 s saat main, saat pause, saat menutup; ≥ 97 % / sisa < 5 s = selesai → dihapus), prompt "Lanjutkan dari m:ss?" bila tersimpan ≥ 5 s. Error elemen → `fetch` Range 0-0 ke URL yang sama: `SIGNATURE_EXPIRED` → tanda tangan ulang (`force`), `currentTime` dipulihkan setelah `loadedmetadata`, lanjut main; error lain/decode → kartu "tidak dapat diputar" + download. Codec: `video/codec.ts` (`videoVerdict`: kontainer dari ekstensi — QuickTime diuji sebagai MP4 bila browser hanya mengenal MP4 — lalu `canPlayType` dengan codec RFC 6381 dari nama ffprobe; codec audio yang tak terputar juga ditolak). *Teks*: `CodeViewer` (highlight.js, DOMPurify `span[class]`, baris dipecah dengan span ditutup/dibuka ulang, gutter nomor baris, wrap, tervirtualisasi; > 512 K karakter tanpa highlight), `MarkdownViewer` (tab Tampilan/Sumber; `renderMarkdown` 16b; link relatif membuka viewer), `CsvViewer` (delimiter otomatis `, ; TAB |`, header toggle, sort klik header — collator numerik, resize kolom, 100.000 baris, 20 MB), `JsonViewer` (≤ 5 MB, pohon JSON Pointer, kedalaman 2 terbuka, cari kunci + berikut/sebelumnya, salin path JSONPath, buka/tutup semua; JSON tidak valid / > 5 MB → TextViewer dengan catatan), HTML → TextViewer sumber + catatan (preview langsung 23e), Office → kartu info + download, arsip di overlay (deep link) → kartu dengan "Lihat isi arsip". "Buka" kini: folder → navigasi, arsip → archive browser, jenis viewer (gambar/PDF/audio/video/teks/HTML/Markdown/CSV/JSON) → overlay, sisanya teks yang dikenali editor → editor, lainnya → kartu; tombol "Edit" di header overlay membuka editor. *Quick Look* (`features/viewer/QuickLook.tsx`, Space): dialog 80 % ringan (bukan overlay penuh, tanpa `?view=`): gambar = `ImageViewer` tanpa slideshow, PDF = halaman 1–3 (`PdfPages`, chunk pdf.js), teks/HTML/Markdown/JSON/CSV = 200 baris pertama (`TextHead`, Range 64 KB, tanpa highlight), audio = `<audio controls>`, video = `<video muted autoplay>` dengan poster, folder = ringkasan (jumlah item, fakta, favorit/pin/tag/catatan), lainnya = kartu info + kontrol meta; `←/→` berpindah item listing (seleksi grid mengikuti), Space/Esc menutup; membuka mencatat `open` (Recent). Panel preview 7b menampilkan 40 baris pertama untuk jenis teks (komponen `TextHead` yang sama). Pintasan video di scope `video` di atas `viewer`; registry kini mendahulukan scope terdalam (sebelumnya urutan registrasi).

**Transcoding HLS (Step 23d, opsional, dibatasi CPU).** Untuk video yang tidak bisa diputar browser (verdict `codec.ts` `playable:false`, atau elemen gagal mendekode), bila admin mengaktifkan `transcode.enabled` (default **mati**; `GET /auth/me` `config.transcodeEnabled` = setting ∧ ffmpeg + ffprobe ada) dan file milik user sendiri (bukan halaman publik, "Dibagikan ke saya", atau admin `as_user`), kartu "tidak didukung" menampilkan tombol **"Putar dengan konversi (lambat)"** di samping "Unduh". Klik → `POST /files/hls {path}` → task `transcode_hls` (§7.1) → kartu progres (persen, "m:ss dari m:ss", ETA engine, "Batalkan" = `POST /tasks/{id}/cancel`, "Unduh") → klien memeriksa playlist bertanda tangan tiap 2 s dan memasang pemutar begitu **≥ 2 segmen** ada (8 s video) atau playlist sudah `#EXT-X-ENDLIST` (sinyal kesiapan = isi playlist; `ready` di respons POST hanya pintasan untuk konversi yang sudah ada). Pemutar = `PlayerBody` yang sama (kontrol, pintasan, kecepatan, PiP, subtitle sidecar, auto-next, simpan posisi) dengan sumber HLS (`HlsPlayer.tsx` + `useHlsPlayback.ts`): Safari/iOS (WebKit, `navigator.vendor` Apple) memakai HLS native `<video src>`; browser lain memakai **hls.js** build `light` yang dimuat lazy (chunk sendiri ±110 KB gzip, hanya setelah tombol diklik) — juga di Chromium ≥ 141 yang menjawab `canPlayType('application/vnd.apple.mpegurl') = "maybe"` (satu jalur untuk EXT-X-START, reload playlist EVENT, dan tanda tangan ulang); tanpa MSE → native bila ada, selain itu kartu gagal. Tanda tangan: setiap permintaan hls.js membawa `scope/exp/sig` terbaru (`xhrSetup`); `403` → cek playlist → `SIGNATURE_EXPIRED` → `POST /files/hls` lagi (mengembalikan task yang sama + URL baru) → hls.js dibuat ulang di posisi yang sama (native: `src` diganti + `currentTime` dipulihkan). Batasan yang diketahui: selama konversi berjalan durasi/seek terbatas pada bagian yang sudah dikonversi (playlist `EVENT` bertambah; `EXT-X-START:TIME-OFFSET=0` membuat pemutar mulai dari awal, bukan dari live edge), prompt "Lanjutkan dari m:ss?" hanya ditawarkan bila konversi sudah selesai, kualitas = H.264 CRF 23 ≤ `transcode.max_resolution` dengan audio AAC stereo 128 kbit/s (track audio pertama saja, subtitle tertanam tidak dibakar), menutup viewer **tidak** membatalkan konversi (tetap di panel Tasks). **Server:** `internal/media/transcode.go` — saat enqueue: setting aktif, file reguler, probe ffprobe (`Prober`) harus berisi stream video dengan durasi diketahui ≤ `transcode.max_duration_minutes` (≤ 180), cache `DATA_DIR/tmp/hls` < 10 GiB dan ruang bebas ≥ 512 MiB, maks 1 task `transcode_hls` queued/running per user (file yang sama → task itu dikembalikan); runner mengulang cek (setting, ukuran+mtime sumber, probe, disk) lalu menjalankan ffmpeg lewat `internal/exec` (argv tetap, tanpa shell; path input `j.AbsForCLI`, keluaran di `fsjail.OpenSys(DATA_DIR, "tmp/hls/<task id>")`; `nice -n <transcode.nice>` + `ionice -c 3`; timeout min(3 jam, durasi × 4 + 60 s)):

```text
ffmpeg -nostdin -hide_banner -loglevel error -y -progress pipe:1 -nostats -i <in>
  -map 0:v:0 -map 0:a:0? -vf scale=w=-2:h='trunc(min(ih,<H>)/2)*2'
  -c:v libx264 -preset veryfast -crf 23 -profile:v main -pix_fmt yuv420p -force_key_frames expr:gte(t,n_forced*4)
  -c:a aac -b:a 128k -ac 2 -sn -dn -threads <max(1, NumCPU/2)>
  -f hls -hls_time 4 -hls_playlist_type event -hls_flags temp_file
  -hls_segment_filename <dir>/seg_%05d.ts <dir>/playlist.m3u8
```

(`-force_key_frames` membuat segmen benar-benar 4 s — tanpa itu GOP 250 libx264 memberi segmen 8 s; `trunc(…/2)*2` menjaga tinggi genap untuk yuv420p; `temp_file` menulis `*.tmp` lalu rename sehingga playlist/segmen yang terlihat selalu utuh). Progress = detik media dari `out_time_us` (§7.1); runner memeriksa `transcode.enabled` maks 1×/detik dan menghentikan ffmpeg bila admin mematikannya (`failed` `FORBIDDEN transcode_disabled`); batal/gagal/timeout/shutdown → proses (grup) dibunuh dan folder dihapus; stderr ffmpeg masuk log task dengan path host diredaksi. Konkurensi global `transcode.max_concurrent` (default 1) ditegakkan engine (`Spec.MaxConcurrent`): task di atasnya tetap `queued` tanpa memakai slot `TASK_MAX_GLOBAL`/per user. **Pembersihan:** sweeper di paket media (saat start + tiap min(5 menit, TTL/2); juga dipanggil job `clean_uploads`) menghapus folder: nama yang bukan id task, task yang tidak ada/terhapus/bukan `done` (gagal, batal, `interrupted` oleh restart), semua folder selesai saat `transcode.enabled` mati, dan folder selesai yang **1 jam** setelah selesai **dan** 1 jam setelah akses playlist/segmen terakhir (akses dicatat di memori; setelah restart mtime folder menjadi akses terakhir); jumlah dicatat di log `hls cleanup`. Tidak ada yang ditulis ke storage user. Di luar scope: transcoding permanen/pre-encode, konversi audio saja, burn-in subtitle, HLS untuk share (owner saja).

**TextViewer/MarkdownViewer/TableViewer/JsonViewer**: read-only; tombol "Edit" membuka editor; Markdown: `marked` (gfm, breaks off) → DOMPurify (`USE_PROFILES: {html: true}`, `FORBID_TAGS` profil 16b + `audio/video/source/track/map/area` (review D: tidak ada media atau image map inline dari dokumen), `ALLOWED_URI_REGEXP` hanya `https?:`, `mailto:`, relatif → gambar relatif diubah ke signed URL `raw?purpose=inline` jika path ada; link relatif ke file lain membuka viewer); CSV: delimiter auto (`,;\t|`), header baris pertama (toggle), kolom bisa di-resize, sort client; JSON: tree expand/collapse, pencarian key, copy path.

**Keamanan viewer**: tidak ada `dangerouslySetInnerHTML` selain hasil DOMPurify; nama file selalu text node; `object-src 'none'`; semua sumber media same-origin (CSP `media-src 'self'`).

### 8.15 Sharing

**Membuat share** (dialog dari context menu/toolbar/panel detail): tab "Link publik" dan "Ke user".

*Link publik* (`mode=public`, hanya jika `PUBLIC_SHARES_ENABLED`):

- Token: `crypto/rand` 32 byte → base64url tanpa padding (43 karakter); DB `token_hash = sha256(token)`; URL `APP_BASE_URL/s/<token>` ditampilkan sekali di response `create`/`regenerate-token` dan juga tersimpan di client untuk tombol "Salin link" — **server tidak bisa menampilkan token lagi**; "Share saya" menampilkan tombol "Salin link" hanya jika client masih menyimpannya (`localStorage` per share id, terenkripsi? tidak perlu: link sendiri tidak rahasia bagi pemiliknya) → **keputusan**: untuk kenyamanan, simpan token terenkripsi AES-GCM (kunci HKDF `share-token`) di kolom `shares.token_enc` agar pemilik bisa menyalin link kapan pun; `token_hash` tetap dipakai untuk lookup. (kolom `token_enc` di §4.2.)
- Slug kustom (`settings share.allow_custom_slug`, default true): `^[a-z0-9][a-z0-9-]{2,63}$`, unik global, tidak boleh 43 karakter persis (hindari tabrakan dengan token), URL `/s/<slug>`.
- Password: argon2id (m=19456, t=2, p=1); `SHARE_REQUIRE_PASSWORD` memaksa.
- Expiry: default `SHARE_DEFAULT_EXPIRY_DAYS`; pilihan 1/7/30 hari, custom tanggal, tanpa batas (jika `SHARE_ALLOW_NO_EXPIRY`); share kedaluwarsa dibiarkan di daftar dengan status "kedaluwarsa" (tombol perpanjang).
- `maxDownloads`; `allowDownload=false` = "preview saja" dengan teks jelas di dialog: "Hanya menyembunyikan tombol unduh; orang yang berniat tetap bisa menyimpan file"; `allowUpload` (folder; "file request": pengunjung bisa mengunggah, tidak bisa melihat daftar jika `allowDownload=false`? → **keputusan**: file request selalu bisa melihat daftar kecuali opsi "sembunyikan daftar" (kolom `hide_listing` di §4.2) untuk mode drop-box).
- `hideOwner`; `note` tampil di halaman publik; QR (`GET /shares/{id}/qr`).
- Response `create` → dialog menampilkan link + tombol salin + QR + ringkasan proteksi.

*Ke user* (`mode=users`): input username dengan autocomplete (`user-suggest`, ≥ 2 karakter, maks 8, hanya `username` + `displayName`, tanpa email/status/kuota), chip per penerima, permission per penerima (Viewer/Editor), catatan, expiry opsional, `allowReshare` (default false). Penerima mendapat notifikasi `share_received` + SSE. Pemilik bisa menambah/menghapus penerima lewat `PATCH`.

**Halaman publik `/s/<token>`** (SPA, tanpa sidebar, header minimal `APP_NAME`): state: password (form + rate limit pesan), expired/disabled/limit (halaman 410 ramah), file (kartu: ikon/thumb, nama, ukuran, tipe, pemilik (jika tidak disembunyikan), catatan, tombol Preview (viewer overlay yang sama, data dari endpoint publik) dan Download), folder (browser read-only: Details & Large icons, breadcrumb relatif ke item share, sort, Quick Look, download file, "Download semua (zip)", upload jika `allowUpload` dengan panel transfer yang sama memakai endpoint publik tus). `X-Robots-Tag: noindex`, `Referrer-Policy: no-referrer`, tanpa link ke aplikasi utama selain logo. Nama item di atas item share tidak pernah dikirim.

**"Dibagikan ke saya"** (`/shared-with-me`): dikelompokkan per pemilik; item folder dibuka di `/shared-with-me/:shareId/*` dengan browser penuh dalam scope share (`?share=`): Viewer = lihat/preview/download/zip/Quick Look/favorit/pin; Editor = + upload, buat, rename, edit (editor), move/copy **di dalam** share, delete (ke trash pemilik), compress/extract di dalam share; tidak ada: share ulang (kecuali `allowReshare` → hanya mode `users`), melihat kuota pemilik, melihat path di atas item, chmod. Penerima bisa "keluar" dari share. Share rusak/expired tampil abu dengan alasan.

(Step 18b) Implementasi penerima: jail = `SubRoot` pemilik pada folder share (file tunggal → sub-root folder induk dengan satu-satunya path `/<nama>`), sehingga `..`, symlink keluar folder dan path pemilik tidak pernah terjangkau; state penyimpanan tetap milik pemilik dan dikunci path absolut pemilik lewat `fsjail.Jail.Abs/RelOf` (trash `original_path` + `deleted_by`, versi, aktivitas pemilik, lock, invalidasi listing, thumbnail/media cache). Penerima mendapat entri aktivitas sendiri dengan path relatif share + `shareId`; **undo tidak ditawarkan** di dalam share. Signed URL penerima memakai scope `r:<share_id>:<user_id>` (TTL sama) dan diperiksa ulang terhadap share pada setiap pemakaian; pencabutan membuat URL mati. Log akses share mencatat `user_id` penerima untuk `list`/`download`/`zip`/`upload`. Transfer besar (> batas sinkron) di dalam share ditolak `413 task_required` sampai task membawa scope.

**"Share saya"** (`/shares`): tabel: Item (ikon + nama + path), Mode (link/user), Penerima (chip), Permission, Proteksi (ikon password/expiry/limit), Kedaluwarsa, View/Download, Akses terakhir, Status (aktif/nonaktif/kedaluwarsa/rusak); aksi: Salin link, QR, Edit, Nonaktifkan/Aktifkan, Regenerate link, Hapus; detail → log akses ringkas (waktu, IP terpotong, aksi, path). Badge di kolom Nama & kolom "Status share".

**Admin**: `/admin/shares` melihat semua share publik (pemilik, nama item, dibuat, expiry, hitungan) tanpa isi; "Cabut" → `disabled_reason=admin_revoked` + audit `share.admin_revoked` + notifikasi pemilik.

**Siklus hidup**: item dihapus → `disabled item_trashed`; restore → tawarkan aktifkan; rename/move → path di-rewrite (link tetap berlaku); user dinonaktifkan → semua share pemilik `disabled owner_disabled` (otomatis aktif lagi saat user diaktifkan hanya jika reason itu); hapus user → share dihapus.

### 8.16 Notifikasi & aktivitas

**Notifikasi** (`notifications`): tipe `task_done` (judul task + link ke folder), `task_failed`, `share_received` ("budi membagikan folder Laporan ke Anda" → link `/shared-with-me`), `share_upload` ("Seseorang mengunggah 3 file ke share 'Drop' Anda"; dari penerima: "budi mengunggah …"), `share_revoked` (penerima dicabut/share dihapus, Step 18b), `share_revoked_by_admin`, `system` (admin broadcast, fase akhir). Bell di top bar dengan badge unread (SSE `notification`), popover 20 terakhir, "Tandai semua dibaca", halaman penuh tidak diperlukan. Task tersembunyi (`thumbnail_batch`) tidak membuat notifikasi; task yang selesai saat user melihat panel task tetap membuat notifikasi tetapi langsung `read` jika tab aktif (client memanggil `read`).

**Aktivitas user** (`activity_logs`, halaman `/activity`): aksi `open`, `edit`, `create`, `upload`, `download` (hanya file tunggal via signed URL yang dibuat user), `rename`, `move`, `copy`, `delete`, `restore`, `purge`, `purge_auto`, `compress`, `extract`, `remote`, `replace`, `share_create`, `share_update`, `share_delete`, `chmod`, `batch_rename`; kolom waktu, aksi, item (link jika masih ada), tujuan, detail; filter aksi/tanggal; pencarian path; tombol "Hapus riwayat saya" (hanya aktivitas, bukan audit).

**Audit (admin)** `action`: `system.seed_admin`, `system.start`, `auth.login`, `auth.login_failed`, `auth.account_locked`, `auth.logout`, `auth.password_changed`, `auth.totp_enabled|disabled`, `auth.totp_recovery_used` (`{remaining}`), `auth.totp_recovery_regenerated`, `user.create`, `user.update`, `user.reset_password`, `user.reset_totp` (Step 23a), `user.disable|enable`, `user.delete|archive`, `user.home_provisioned|home_reused`, `user.revoke_sessions`, `settings.update|reset`, `share.create|update|delete` (metadata saja), `share.public_access` (unlock_ok/download dari link publik; IP terpotong), `share.admin_revoked`, `file.delete_permanent`, `trash.empty`, `admin.browse_user`, `admin.task_cancel`, `maintenance.<job>` (run manual oleh admin; run terjadwal tidak diaudit) + `maintenance.backup_download`, `quota.drift`. `details_json` tidak pernah berisi password/token. Halaman `/admin/audit`: filter actor/action/tanggal/teks, export CSV (`admin/audit/export.csv`, streaming, maks 1.000.000 baris — Step 19b, sebelumnya 100k), retensi `AUDIT_RETENTION_DAYS`.

### 8.17 Admin

**Dashboard** (`/admin`): kartu ringkas (bukan hero): user aktif/nonaktif; storage: Σ `used` semua user vs Σ kuota vs disk `statfs(STORAGE_ROOT)` (bar bertumpuk), peringatan jika Σ kuota > disk; top 10 user; task berjalan/antre; share publik aktif; login gagal 24 jam (dari `login_attempts`); versi aplikasi (`version`, `commit`, `buildDate`, Go runtime); status dependency: tabel tool → path → terdeteksi (versi dari `--version` saat start, di-cache) → fitur yang terdampak; warning konfigurasi (ADMIN_INITIAL_PASSWORD masih ada, cross-FS, swap rendah (`/proc/meminfo` SwapTotal < 1 GB), disk < 10 %).

**Users** (`/admin/users`): tabel sesuai prompt; aksi baris: Edit (nama, role, kuota, status), Reset password (tampilkan sekali, tombol salin), Paksa logout, Reset 2FA (hanya tampil bila `totpEnabled`; badge "2FA" di kolom status; konfirmasi; sesi user dicabut — Step 23a), Nonaktifkan/Aktifkan, Hapus (dialog ketik username, pilih `purge` atau `archive`). Buat user: username (validasi live + cek unik), nama, role, kuota (default dari setting), password sementara: generate (16 karakter acak, tampil sekali) atau manual (validasi kebijakan). Aturan admin terakhir: server menolak `PATCH` role→user / status→disabled / `DELETE` jika target adalah satu-satunya admin aktif (`VALIDATION_FAILED last_admin`), termasuk diri sendiri; hitungan admin aktif diperiksa ulang **di dalam transaksi tulis** (`BEGIN IMMEDIATE`) sehingga dua admin yang saling menurunkan pada saat yang sama tidak bisa meninggalkan nol admin (R-F M-03).

**Hapus user** (task `delete_user`, dimiliki admin pemanggil agar terlihat di panel task-nya; Step 19a): akun soft-delete (`deleted_at`, status disabled) + sesi dicabut seketika di request, lalu task: `purge` → `fsjail.RemoveHome` root user + `trash/<uid>`, `versions/<uid>`, `thumbs/<uid>` + baris per tabel (`db.PurgeUserData`, daftar tabel tetap) + `users.deleted_at` (baris dipertahankan 30 hari untuk audit lalu dihapus job `purge_deleted_users`; username belum bisa dipakai ulang); `archive` → `fsjail.ArchiveHome` (renameat, cross-FS → copy+delete) ke `DATA_DIR/deleted-users/<username>-<yyyyMMdd-HHmmss>`, trash/versions user ikut dipindah ke dalam folder itu (`__trash`, `__versions`), thumbs dihapus, baris DB dihapus (cascade; username bebas); audit `user.delete|archive` saat request.

**Settings** (`/admin/settings`): form per kelompok (Upload, Storage, Recycle Bin & versi, Remote download, Sharing, Keamanan, Tasks, Media & konversi video (Step 23d: `transcode.*`, tiap key dengan teks bantuan), Tampilan): tiap field menampilkan nilai efektif, sumber, tombol reset; validasi server sama dengan §3; perubahan → `settings.update` audit `{key, old, new}` (tidak ada kunci rahasia), reset → `settings.reset`; nilai berlaku seketika untuk handler (snapshot `config.Runtime()`) dan ≤ 5 detik untuk cache `share.Settings`/`remotedl.Settings` (di-invalidate langsung oleh admin API). Catatan: `tz_default` baru dipakai scheduler setelah restart. Daftar key yang bisa diubah = kolom "Override admin = ya" di §3.3 + `remote_download.allowlist/denylist`, `share.allow_custom_slug`, `branding.logo`, `transcode.enabled|max_concurrent|max_resolution|max_duration_minutes|nice` (§4.2).

**Tasks** (`/admin/tasks`): semua task semua user (kolom user) + task sistem (maintenance), filter state/user, cancel (audit `admin.task_cancel`); baris memakai komponen panel task (`TaskRow` dengan kolom pemilik, tanpa retry/hapus). **Shares** (`/admin/shares`, Step 19b): tabel metadata semua share (default mode publik; filter mode/status/pemilik) + nonaktifkan (`admin_revoked`).

**Maintenance** (`/admin/maintenance`): tombol per job (§7.1) dengan konfirmasi; `recalc-quota` (semua/satu user), `clear-thumbs`, `clean-uploads`, `purge-trash` (semua user, hanya yang kedaluwarsa; opsi "paksa semua" dengan ketik `HAPUS`), `integrity-check` → laporan: folder di `STORAGE_ROOT` tanpa akun (tawarkan arsipkan), akun tanpa folder (akan dibuat saat login), referensi rusak per tabel, file yatim di `trash/versions/uploads/thumbs`, `backup-db` (`VACUUM INTO 'DATA_DIR/backups/app-<ts>.db'`, keep 7 + manual tidak dihapus otomatis jika diberi label), daftar backup + download (sesi admin, diaudit `maintenance.backup_download`; Step 19b), `prune-logs`. **Step 19b:** halaman berisi kartu per job (deskripsi, jadwal di `TZ_DEFAULT`, berikutnya/terakhir, tombol jalankan dengan konfirmasi, hasil JSON terakhir; integrity check sebagai tabel), daftar backup dan job terjadwal lain (read-only); tiap run = task sistem (§7.1) yang juga tampil di `/admin/tasks`.

### 8.18 Profil & preferensi

**Profil** (`/settings`): ganti password (form; sesi lain dicabut), sesi aktif (tabel: perangkat, IP, dibuat, terakhir aktif, badge "sesi ini", tombol logout per sesi, "Logout semua sesi lain"), 2FA TOTP (Step 23a, komponen `features/settings/TwoFactor.tsx`): kartu status Aktif/Nonaktif (+ sejak kapan); "Aktifkan" → dialog: QR `otpauth://` (dirender client dari `qrcode`, lazy) + secret manual yang bisa disalin + input 6 digit (`inputmode=numeric`, `autocomplete=one-time-code`) → sukses menampilkan 10 kode pemulihan **sekali** dengan Salin/Unduh `.txt` dan kotak centang wajib "Saya sudah menyimpan kode ini" sebelum dialog bisa ditutup; "Nonaktifkan" → dialog password + kode (TOTP atau kode pemulihan); "Buat kode pemulihan baru" → dialog password → kode baru (kode lama batal). Halaman login: setelah `totp_required` tampil langkah kedua (input kode, autofocus, tautan "Gunakan kode pemulihan" beralih ke input teks, tautan kembali); `totp_invalid` → pesan inline; `429` → pesan limit dengan detik; login dengan kode pemulihan terakhir memberi peringatan bahwa tidak ada kode tersisa.

**Preferensi** (`user_preferences.prefs_json`, semua opsional, default di client dan server):

| Key | Nilai | Default |
| --- | --- | --- |
| `ui.theme` | `light|dark|system` | `system` |
| `ui.accent` | `blue|emerald|violet|amber|rose|slate` | `blue` |
| `ui.language` | `id|en` | `DEFAULT_LANGUAGE` |
| `ui.density` | `compact|comfortable` | `compact` (baris 32 px vs 36 px) |
| `ui.sidebar_collapsed`, `ui.preview_panel` | bool | false / false |
| `ui.timezone` | IANA atau `auto` | `auto` |
| `view.mode` | `details|list|tiles|large|xlarge` | `details` |
| `view.remember_per_folder` | bool | true |
| `view.folders_first` | bool | true |
| `view.show_hidden` | bool | true |
| `view.columns` | `{visible:[…], order:[…], widths:{…}}` | Nama, Ukuran, Tipe, Diubah, Catatan |
| `view.sort` | `{field, dir, field2?, dir2?}` | name asc |
| `fileops.confirm_delete` | bool | true |
| `fileops.copy_metadata` | bool | false |
| `upload.preserve_mtime` | bool | true |
| `editor.*` | §8.13 | |
| `viewer.slideshow_interval` | 2/5/10 | 5 |
| `exif.show_gps` | bool | false |
| `audio.volume`, `audio.repeat`, `audio.shuffle` | | 0.8, off, false |
| `video.positions` | `{path: seconds}` 100 terakhir — **tidak dipakai sejak Step 17b** (posisi di tabel `media_positions` lewat `/me/media-position`); key tetap diterima validator agar dokumen lama tidak ditolak | |
| `notifications.task_toast` | bool | true |
| `shortcuts.overrides` | `{action: keys}` (fase akhir) | |
| `tabs.restore` | bool (pulihkan tab folder saat login) | true |
| `tabs.open` | `[{path}]` ≤ 10 | |
| `dualpane.enabled` | bool (mode dual-pane, Step 23b; berlaku hanya ≥ 1280 px) | false |
| `dualpane.right` | `{tabs:[{path}] 1–10, active?: indeks 0…len−1}` — tab panel kanan; field lain/bentuk lain → `invalid_value` | |

`PUT /me/preferences` adalah merge patch (JSON Merge Patch RFC 7386) dengan validasi key terdaftar (`httpapi/prefs.go`: enum/bool/angka/objek per key; `editor.*` bebas ≤ 4 KB); key tak dikenal ditolak `VALIDATION_FAILED details.fields.<key>=unknown_key`, nilai salah bentuk `invalid_value`. Client (`stores/prefs.ts`) menerapkan perubahan lokal seketika dan mengirim patch ter-debounce 500 ms (pending dikirim `keepalive` saat `pagehide`). `view.columns` = `{visible, order, widths}` (id kolom §8.4, `name` wajib); `view.sort` = `{field, dir, field2?, dir2?}`; `tabs.open` dipulihkan saat login: URL yang dibuka dicari di daftar tab, bila tidak ada → tab pertama melanjutkan ke URL itu (path lamanya tetap di riwayat Back); path duplikat diizinkan.

> Catatan: kolom `shares.token_enc` dan `shares.hide_listing` (§4.2) ada karena keputusan di §8.15; keduanya masuk migrasi awal.

---

## §9 UI spec

### 9.1 Design token

Didefinisikan sebagai CSS variables di `web/src/styles/tokens.css` (`:root` light, `[data-theme="dark"]` dark, `[data-accent=…]` aksen; nama internal `--font-family-sans/mono`, `--elevation-popover`). `data-theme` diset sebelum first paint oleh **script eksternal** `web/public/theme-init.js` (classic script di `<head>`, origin sama) yang membaca `localStorage.theme|accent|density` dan `prefers-color-scheme` → tanpa flash dan **tanpa `unsafe-inline` maupun nonce** (deviasi dari rencana awal "script inline ber-nonce"; nonce tetap tersedia di §10.3 bila kelak dibutuhkan). Pemetaan ke Tailwind memakai Tailwind v4 CSS-first: `@theme inline { --color-bg: var(--bg); … }` di `globals.css` (tidak ada `tailwind.config.ts`); nama shadcn (`background`, `foreground`, `primary`, `muted`, …) dipetakan ke token yang sama sehingga komponen yang di-vendor tidak memakai palet default shadcn, dan `accent` versi shadcn (latar hover) diganti `surface-2` di file komponen.

| Token | Light | Dark | Tailwind |
| --- | --- | --- | --- |
| `--bg` | #FFFFFF | #0E1015 | `bg-bg` |
| `--surface` | #F7F8FA | #14171E | `bg-surface` |
| `--surface-2` | #F0F2F5 | #1B1F28 | `bg-surface-2` |
| `--border` | #E4E7EC | #262B36 | `border-border` |
| `--text` | #0F172A | #E6E8EE | `text-text` |
| `--text-muted` | #5B6474 | #9AA3B2 | `text-muted` |
| `--accent` | #2563EB | #4C8DFF | `bg-accent text-accent` |
| `--accent-fg` | #FFFFFF | #0E1015 | teks di atas accent |
| `--accent-subtle` | #EAF1FE | #1A2A4A | baris terpilih, badge |
| `--success` | #15803D (Step 21; semula #16A34A, 3,3:1 sebagai teks) | #22C55E | `text-success` (teks ≥ 4,5:1 di `--bg`/`--surface`) |
| `--warning` | #B45309 (Step 21; semula #D97706) | #F59E0B | `text-warning` |
| `--danger` | #DC2626 | #F87171 | |
| `--danger-fg` | #FFFFFF | #FFFFFF | `text-destructive-foreground` (tombol/badge destruktif; Step 21) |
| `--tag-red/orange/yellow/green/blue/purple/gray` | #F43F5E/#F97316/#FACC15/#10B981/#0EA5E9/#8B5CF6/#A1A1AA | #FB7185/#FB923C/#FDE047/#34D399/#38BDF8/#A78BFA/#A1A1AA | `bg-tag-*` titik tag (Step 21, review B m-07) |
| `--star` / `--star-fill` | #F59E0B / #FBBF24 | #FBBF24 / #FCD34D | `text-star fill-star-fill` bintang favorit |
| `--focus` | = accent | = accent | ring 2 px offset 1 px |

Aksen alternatif (pref `ui.accent`, mengganti `--accent`, `--accent-subtle`, `--accent-fg`): light memakai teks putih di atas aksen, dark memakai teks gelap (#0E1015) di atas aksen yang lebih terang. Nilai final setelah verifikasi kontras (≥ 4.5:1 teks di atas aksen): emerald #047857/#34D399, violet #7C3AED/#A78BFA, amber #B45309/#FBBF24, rose #BE123C/#FB7185, slate #475569/#94A3B8 (nilai light emerald/amber/rose diturunkan dari usulan awal #059669/#D97706/#E11D48 yang hanya 3,2–3,8:1 dengan teks putih); `--accent-subtle` per aksen ada di `tokens.css`. Script `web/scripts/check-contrast.mjs` menghitung rasio WCAG untuk 14 pasangan (text/bg, text-muted/bg, text/surface, text/surface-2, accent-fg/accent, text/accent-subtle, danger/bg, success/bg, success/surface, warning/bg, warning/surface = 4.5:1 — success/warning dinaikkan dari 3:1 di Step 21 karena dipakai sebagai teks; accent/bg, accent/surface, focus/bg, danger/surface = 3:1) di 2 tema × 6 aksen = 192 pemeriksaan; dijalankan oleh `pnpm lint` dan CI; `/dev/design` menampilkan rasio yang sama di browser.

Warna ikon tipe (`--icon-folder` amber #F59E0B, `--icon-image` violet #8B5CF6, `--icon-video` rose #F43F5E, `--icon-audio` pink #EC4899, `--icon-archive` orange #F97316, `--icon-code` sky #0EA5E9, `--icon-pdf` red #EF4444, `--icon-doc` blue #3B82F6, `--icon-sheet` green #22C55E, `--icon-text` slate #64748B, `--icon-other` slate-400). Ikon tipe: set SVG internal (`components/FileIcon`, 24 px grid, glyph lucide (ISC) di atas bentuk file bulat 4 px), ukuran 16/24/48/96/192 via `currentColor`.

**Tipografi**: `Inter` (self-host, `font-feature-settings: "tnum"` di kelas `.tabular`), `JetBrains Mono` untuk path/permission/kode. Skala: 12 (meta/status bar), 13 (tabel/menu), 14 (UI), 16 (judul dialog), 20 (judul halaman); line-height 1.4; weight 400/500/600.

**Spasi & bentuk**: grid 4 px; radius 4 (badge), 6 (input/tombol), 8 (dialog/popover); border 1 px `--border`; shadow hanya popover/menu/dialog (`0 4px 16px rgb(0 0 0 / .12)` light, `.4` dark). Tinggi: top bar 48, address bar 40, toolbar 40, header tabel 32, baris 36 (compact 32), status bar 28, sidebar 232 px (ikon 48 px), panel preview 320 px.

**Motion**: 120–180 ms `cubic-bezier(.2,.8,.2,1)`; `@media (prefers-reduced-motion: reduce)` → 0 ms; tidak ada animasi pada listing besar selain fade skeleton.

### 9.2 Inventaris komponen

`components/ui` (shadcn/ui di-vendor ke repo lewat CLI, dikustom ke token): Button (variant shadcn `default|secondary|outline|ghost|destructive|link` + alias `primary|danger`; size `sm` 28 px, `md`/default 32 px, `lg` 36 px, `icon` 32 px, `icon-sm` 28 px; focus ring 2 px accent offset 1 px), IconButton (32 px, `label` wajib = aria-label + tooltip, `shortcut` opsional sebagai `Kbd`), Input, Textarea, Select, Checkbox, Switch, RadioGroup, Tabs, Tooltip, DropdownMenu, ContextMenu, Dialog, AlertDialog, Sheet (mobile), Popover, Command (cmdk), Toast (sonner, posisi bottom-left agar tidak menutupi panel transfer kanan-bawah, tema dari store UI, maks 3), Progress, Badge, Skeleton, Separator, ScrollArea, Kbd/KbdGroup, Breadcrumb (dropdown sibling & collapse "…" ditambahkan Step 7a), Table (header sticky di Step 7a), Empty.

`components/`: `AppShell`, `TopBar`, `Sidebar` (+ `QuotaIndicator`), `AddressBar` (back/forward/up/refresh, breadcrumb ↔ input path editable (klik area kosong/`Alt+D`), filter cepat), `Toolbar` (grup + overflow "⋯"), `FolderTabs`, `FileGrid` (Details/List/Tiles/Large/XLarge; ARIA grid), `ColumnPicker`, `SortMenu`, `SelectionBar` (mobile), `StatusBar`, `PreviewPanel`, `FileIcon`, `Thumb`, `EmptyState`, `ErrorState`, `TaskPopover`, `TransferPanel`, `NotificationBell`, `UserMenu`, `ThemeToggle`, `ConflictDialog`, `FolderPicker`, `BatchRenameDialog`, `PropertiesDialog`, `ShareDialog`, `PermissionDialog`, `ChecksumPanel`, `VersionsPanel`, `CompressDialog`, `ExtractDialog`, `RemoteDownloadDialog`, `SearchPanel`, `ShortcutHelp`, `CommandPalette`, `MiniPlayer`, `ViewerShell` + viewer per tipe, `Treemap`.

### 9.3 Layout layar (wireframe teks)

**File browser** (`/files/*`, ≥ 1280 px) — sesuai prompt bagian 12:

```text
┌ TopBar 48 ──────────────────────────────────────────────────────────────────────────────┐
│ [logo] FileManager   [ 🔍 Cari file…  Ctrl+K ]              Tasks(2)  🔔(1)  ☾  budi ▾  │
├ Sidebar 232 ┬ AddressBar 40 ───────────────────────────────────────────────────────────┤
│ File Saya   │ ← → ↑ ⟳ │ 📁 Proyek › website › assets ▾ │          [Filter… ]  [Alt+P] │
│ Recent      ├ Tabs 32 ─────────────────────────────────────────────────────────────────┤
│ Favorit     │ [assets ×] [Dokumen ×] [+]                                                │
│ Dibagikan   ├ Toolbar 40 ──────────────────────────────────────────────────────────────┤
│ Share saya  │ [+ Baru ▾] [⬆ Upload ▾] [🌐 Remote] │ Copy Cut Paste Rename Del ⋯ │ View▾ Kolom▾ Urut▾ │
│ Recycle Bin ├ Header 32 ───────────────────────────────────────────────────────────────┤
│ Aktivitas   │ [ ] Nama ▲            Ukuran   Tipe          Diubah        Catatan        │
│ Analisis    │ [ ] 📁 css              —      Folder        2 jam lalu                   │
│ Admin       │ [x] 🖼 logo.png       24 KB    Gambar PNG    kemarin       logo v2        │
│             │ [ ] 📄 index.html      3 KB    HTML          12 Okt 2026                  │
│ Kuota 62%   │ …                                                      ║ PreviewPanel 320 │
│ ▓▓▓▓▓▓░░░░  │                                                        ║ (toggle)         │
├─────────────┴ StatusBar 28 ─────────────────────────────────────────────────────────────┤
│ 124 item · 3 dipilih (1,2 MB)                                   6,2 GB dari 10 GB ▓▓▓░ │
└──────────────────────────────────────────────────────────────────────────────────────────┘
                                                        ┌ TransferPanel (kanan bawah) ┐
                                                        │ ⬆ 3 file · 45 % · 12 MB/s  ▾ │
```

**File browser — varian dual-pane** (Step 23b; hanya desktop ≥ 1280 px, `(min-width: 1280px)`):

```text
┌ TopBar 48 ──────────────────────────────────────────────────────────────────────────────┐
├ Sidebar 232 ┬ Toolbar 40 (bersama; bertindak pada panel yang fokus) ─────────────────────┤
│ File Saya   │ [+ Baru ▾] [⬆ Upload ▾] [🌐] │ Copy Cut Paste Rename Del ⋯ │ View▾ Urut▾ Kolom▾ │
│ …           ├ region "Panel kiri" (fokus: outline 2 px --focus) ┬ region "Panel kanan" ──┤
│             │ ← → ↑ ⟳ │ 📁 Proyek › web     [Filter…]        │ ← → ↑ ⟳ │ 📁 Arsip  [Filter…] │ PreviewPanel 320
│             │ [web ×] [+]                                    │ [Arsip ×] [Foto ×] [+]      │ (milik panel
│             │ [ ] Nama ▲          Ukuran   Diubah            │ [ ] Nama ▲    Ukuran  Diubah│  yang fokus)
│             │ …                                              │ …   ┌ Pindahkan ke Arsip ┐  │
│             │ 124 item · 3 dipilih                    kuota  │ 8 item                kuota │
│             ├ MiniPlayer (jika aktif) ─────────────────────────────────────────────────┤
```

- **Toggle:** View ▾ → kotak centang "Dual-pane" (dengan pintasan `F6`, §9.6) dan aksi palette "Dual-pane"; tersimpan di `dualpane.enabled` (§8.18). Di bawah 1280 px (tablet/ponsel) toggle disembunyikan dan UI diam-diam kembali ke satu panel; preferensinya tetap tersimpan dan aktif lagi saat layar cukup lebar.
- **Panel kiri** = perilaku single-pane apa adanya: path dari URL `/files/*` (+ `?select=`, `?view=`, `?archive=`), riwayat per tab dan `tabs.open` (`stores/tabs.ts`). "Buka lokasi file" (`openLocation.ts`) dan deep link selalu menuju panel kiri.
- **Panel kanan** tidak pernah tercermin di URL. State-nya di `web/src/stores/panes.ts`: `useRightTabsStore` (model tab yang sama dengan panel kiri lewat `createTabsStore('rtab')`: ≤ 10 tab, riwayat Back/Forward per tab, offset scroll per folder), seleksi per tab di `stores/selection.ts` (id `rtab-*`, tidak bertabrakan dengan `tab-*`), override urutan sesi `rightSort` (mengubah urutan di panel kanan **tidak** menulis `view.sort`/`folder_view_prefs`); filter cepat/tag, rename inline, type-ahead, Quick Look, viewer dan browser arsip adalah state lokal komponen panel. Tab + tab aktif panel kanan disimpan di `dualpane.right`. Mode view dan kolom tetap preferensi biasa (`view.mode`/`view.columns`/`folder_view_prefs`, §8.4) untuk kedua panel.
- **Satu panel fokus** (`usePanesStore.focused`): pointerdown atau fokus keyboard yang masuk ke panel memindahkan fokus. Toolbar (dirender panel fokus ke slot bersama), shortcut scope `browser` (`Ctrl+A`, `Delete`, `F2`, `F5`, `Alt+D`, `Ctrl+C/X/V`, `Alt+P`, tab `Alt+T/W/1…9`, …), konteks aksi palette, drop handler sidebar/breadcrumb, paste-upload dan panel preview mengikuti panel fokus; status bar ada per panel. Dialog operasi file tetap satu (`FileOpsDialogs` milik panel kiri; `useFileOps` tanpa state per panel); `focusGrid()` mengembalikan fokus ke grid panel yang fokus.
- **Aksi antar panel** (hanya saat dual-pane aktif): "Salin ke panel lain" dan "Pindahkan ke panel lain" di menu konteks (1 file, 1 folder, banyak item), toolbar ⋯ dan palette → `POST /files/copy|move` dengan `dest` = folder aktif panel lain melalui `useFileOps.transfer` (dialog konflik §8.5, task untuk batch besar, toast "Urungkan" — persis seperti Paste). Tampil `disabled` bila seleksi kosong, tujuan = folder sumber, atau tujuan berada di dalam item terpilih.
- **Drag & drop:** mekanisme §8.5 (drop ke baris folder/breadcrumb/sidebar; tanpa modifier = pindah, `Ctrl`/`⌘` = salin). Tambahan: area listing panel (termasuk area kosong/empty state) dan address bar panel menerima drop ke folder aktif panel itu; selama item dari folder lain melayang di atas area listing tampil badge "Pindahkan ke <folder>"/"Salin ke <folder>" (mengikuti `Ctrl`). Drop ke folder yang sama = no-op (tanpa request, tanpa dialog konflik). Pemilihan target = helper murni `features/browser/paneDrop.ts`.
- **Tata letak panel:** address bar ringkas (filter cepat lebih sempit tanpa petunjuk `Ctrl+F`, filter tag ikon saja), tab, listing, status bar per panel; kolom Nama di Details minimal 180 px — panel sempit menggulir horizontal di dalam grid, bukan memotong nama.
- **Aksesibilitas:** setiap panel `role="region"` dengan `aria-label` "Panel kiri"/"Panel kanan"; panel fokus diberi outline 2 px `--focus`; `Tab` pada grid memindahkan fokus ke grid panel lain (gaya Commander; model `aria-activedescendant` di dalam grid tidak berubah), `Shift+Tab` mengikuti urutan DOM biasa sehingga address bar & tab panel tetap terjangkau; toggle dan perpindahan fokus panel diumumkan lewat live region (`announce`). Ponsel/tablet tidak berubah.

**Editor** (`/editor`): sesuai wireframe prompt bagian 7 (toolbar · explorer 240 · tabs · Monaco · status bar). Mode modal dari file manager: dialog 90 % viewport dengan tombol maximize → route.

**Viewer overlay**: header 48 (nama, "3/24", aksi), area konten penuh, filmstrip 88 px (gambar), panel info 320 kanan (toggle). Quick Look: dialog 80 %.

**Dialog Share**: tabs [Link publik | Ke user]; link: switch proteksi (password, kedaluwarsa, limit download, boleh unduh, boleh upload, sembunyikan pemilik), catatan, slug, tombol "Buat link" → hasil: input readonly link + Salin + QR; user: input username (chip), permission per chip, catatan, kedaluwarsa, "Bagikan".

**Halaman publik** (`/s/:token`): header tipis (logo/nama app), kartu tengah (file) atau browser read-only (folder) lebar maks 1100 px, footer "Dibagikan lewat FileManager". Password: kartu kecil di tengah.

**Admin Users**: judul + tombol "Buat user"; tabel (Username, Nama, Role, Status, Kuota bar, Login terakhir, Folder, Dibuat, ⋯); drawer kanan untuk edit/buat.

**Login** (`/login`): kartu 360 px di tengah `--surface`, logo/nama app, username, password (toggle lihat), tombol Masuk, pesan error inline, tanpa link register. `/change-password`: kartu serupa dengan aturan password live.

### 9.4 Route

| Route | Komponen | Auth | Catatan |
| --- | --- | --- | --- |
| `/login` | LoginPage | publik | redirect ke `/files` jika sudah login |
| `/change-password` | ChangePasswordPage | S | satu-satunya route saat `mustChangePassword` |
| `/` | redirect `/files` | | |
| `/files/*` | BrowserPage (scope own) | S+P | `*` = path virtual; query `?view=<path>`, `?select=<name>` |
| `/recent`, `/favorites` | ListPage variant | S+P | |
| `/shared-with-me` | SharedWithMePage | S+P | |
| `/shared-with-me/:shareId/*` | BrowserPage (scope share) | S+P | |
| `/shares` | MySharesPage | S+P | |
| `/trash` | TrashPage | S+P | |
| `/activity` | ActivityPage | S+P | |
| `/storage` | StorageAnalysisPage | S+P | `?tab=duplicates` membuka tab Duplikat (Step 23c) |
| `/search` | SearchPage | S+P | query `?q&path&…` |
| `/editor` | EditorWorkspace | S+P | `?path&line&col&share` |
| `/settings` | SettingsPage (tabs: Profil, Preferensi, Sesi, Keamanan) | S+P | |
| `/admin`, `/admin/users`, `/admin/settings`, `/admin/audit`, `/admin/tasks`, `/admin/maintenance`, `/admin/shares` | Admin* | A | sidebar admin |
| `/s/:token/*` | PublicSharePage | publik | layout terpisah |
| `/dev/design` | DesignSystemPage | hanya `APP_ENV != production` atau admin | semua token & komponen, toggle tema |
| `*` | NotFoundPage | | |

Router: TanStack Router (file-based), code-splitting per route; loader memakai TanStack Query (`staleTime` listing 10 s, `gcTime` 5 menit); scroll restoration per tab folder.

### 9.5 Matriks context menu

Sama dengan prompt bagian 6 (dipakai apa adanya) dengan aturan tampil:

| Konteks | Aksi (urutan menu, separator `|`) |
| --- | --- |
| 1 file | Buka · Buka dengan ▸ (Editor/Viewer/Download) · Quick Look \| Download · Share · Salin link share (jika ada) \| Favorit · Pin · Catatan · Tag warna ▸ \| Copy · Cut · Duplikat · Rename · Pindah ke… · Salin ke… \| Compress… · Extract ▸ (jika arsip: di sini / ke folder baru / …) · Lihat isi arsip \| Permission… · Buat symlink… · Checksum… · Riwayat versi \| Copy path · Buka lokasi file (Search/Recent) · Properties \| Delete |
| 1 folder | Buka · Buka di tab baru · Download sebagai zip \| Share · Favorit · Pin · Catatan · Tag warna ▸ \| Copy · Cut · Duplikat · Rename · Pindah ke… · Salin ke… \| Compress… · Hitung ukuran · Upload ke sini · Cari di folder ini \| Permission… · Copy path · Properties \| Delete |
| Banyak item | Download zip \| Copy · Cut · Pindah ke… · Salin ke… \| Compress… · Batch rename… · Permission… \| Favorit · Tag warna ▸ \| Delete |
| Area kosong | Baru ▸ (File · Folder · Dari template ▸ · Symlink) · Upload file · Upload folder · Remote download \| Paste (jika clipboard) · Refresh · Pilih semua \| View ▸ · Urutkan ▸ · Kolom ▸ \| Properties folder ini |
| Recycle Bin item | Restore · Restore ke… \| Hapus permanen |
| Dibagikan ke saya (viewer) | Buka · Quick Look · Download · Favorit · Pin · Copy path · Properties · Keluar dari share |
| Dibagikan ke saya (editor) | seperti "1 file/1 folder" tanpa Share/Symlink/Permission/Riwayat versi (versi tetap bisa dilihat di editor) |

Aksi yang tidak berlaku **disembunyikan** di context menu dan palette; lima tombol inti toolbar (Copy, Cut, Paste, Rename, Hapus) tetap tampil dan hanya `disabled` agar posisinya stabil (keputusan Step 9b, dikonfirmasi review B). Item destruktif (Delete, Hapus permanen) selalu terakhir dengan warna `--danger`. Menu dapat dinavigasi keyboard (Radix), `Shift+F10`/tombol Menu membuka context menu pada seleksi saat ini. Implementasi (Step 9b): matriks ini adalah `MENU_MATRIX` di `lib/actions.ts`; aksi yang backend-nya belum ada terdaftar `available:false` dan tidak tampil di menu, toolbar, maupun palette sampai step pemiliknya mengaktifkannya. Klik kanan pada item yang belum dipilih memilihnya dulu; klik kanan di dalam seleksi mempertahankan seleksi.

### 9.6 Shortcut final

| Shortcut | Aksi | Catatan |
| --- | --- | --- |
| `Ctrl/Cmd+C` · `X` · `V` | Copy · Cut · Paste | tidak dicegat saat fokus di input |
| `Delete` / `Shift+Delete` | Ke Recycle Bin / hapus permanen | |
| `F2` | Rename inline | |
| `Ctrl+A` / `Ctrl+I` | Pilih semua / invert | |
| `Enter` / `Space` | Buka / Quick Look | |
| `Backspace` / `Alt+↑` | Folder induk | |
| `Alt+←` / `Alt+→` | Back / Forward | |
| `Ctrl+F` | Cari di folder aktif (fokus filter + mode cari) | |
| `Ctrl+Shift+F` | Cari isi file | |
| `Ctrl+K` | Command palette | |
| `Alt+N` / `Alt+Shift+N` | Folder baru / file baru | `Ctrl+N` tidak bisa dicegat |
| `Ctrl+U` | Upload file | |
| `Ctrl+1 … Ctrl+5` | Details · List · Tiles · Large · Extra large | |
| `Ctrl+Z` | Undo (operasi terakhir, ≤ 10 detik) | |
| `Alt+Enter` | Properties | |
| `Alt+P` | Toggle panel preview | |
| `Alt+D` | Fokus address bar (edit path) | `Ctrl+L` tidak bisa |
| `F5` | Refresh listing | `preventDefault`; `Ctrl+F5` dibiarkan ke browser |
| `F6` | Toggle dual-pane (Step 23b, hanya ≥ 1280 px) | `preventDefault`. Diverifikasi di Chromium (Playwright 1.64): `keydown` F6 sampai ke halaman dan fokus tidak berpindah; F6 bukan accelerator *reserved* Chrome (halaman boleh mencegatnya). Perilaku jendela Chrome/Firefox/Safari bertampilan penuh (siklus fokus ke address bar browser) **perlu verifikasi**; alternatif selalu ada: View ▾ → Dual-pane. Copy/move ala Commander (`F5`/`F6`) **tidak** dipakai karena `F5` = refresh; diganti aksi "Salin/Pindahkan ke panel lain" |
| `Tab` (dual-pane) | Pindah ke grid panel lain | hanya saat fokus di grid; `Shift+Tab` = urutan normal |
| `Esc` | Batalkan seleksi / tutup overlay, dialog, menu | |
| `?` | Daftar shortcut | di luar input |
| `↑ ↓ ← → Home End PageUp PageDown` | Navigasi grid (roving tabindex) | `Shift` = rentang |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Tab folder berikut/sebelum | tidak dapat dicegat di beberapa browser → alternatif `Alt+1…9` pilih tab, `Alt+T` tab baru, `Alt+W` tutup tab |
| Editor: `Ctrl+S`, `Ctrl+Shift+S`, `Alt+W`, `Ctrl+G`, `F1`, `Ctrl+P`, `Ctrl+Shift+F`, `Shift+Alt+F` | save, save all, tutup tab, go to line, palette, quick open, find in files, format | |
| Viewer: `←/→`, `+/-` (alias `=`), `0` (fit / seluruh halaman PDF), `1` (1:1 / 100 %), `R` (rotate), `Shift+R` (rotate kiri), `F` (fullscreen), `I` (panel info), `Space` (slideshow; di Quick Look = tutup), `Ctrl+F` (cari teks PDF), `Esc` | scope `viewer` bersifat modal seperti `dialog`: pintasan `browser` (Delete, Backspace, …) tidak sampai ke grid di belakang overlay (Step 17a) | |
| Video: `Space`/`K` (play/pause), `←/→` 5 s, `J/L` 10 s, `F`, `M`, `↑/↓` volume, `0–9` lompat 0–90 % | scope `video` didorong di atas `viewer` oleh VideoPlayer; scope terdalam menang (Step 17b) | |

Dilarang dipakai: `Ctrl+N`, `Ctrl+Shift+N`, `Ctrl+T`, `Ctrl+W`, `Ctrl+L`, `Ctrl+D`, `Ctrl+Shift+C`, `Ctrl+Shift+I`, `Ctrl+Q`. Implementasi: `lib/shortcuts.ts` dengan scope (`global`, `browser`, `editor`, `viewer`, `dialog`), registry tunggal yang juga menghasilkan tooltip dan halaman "?"; shortcut dinonaktifkan saat fokus di `input/textarea/[contenteditable]` kecuali `Esc` dan `Enter` pada rename.

### 9.7 State empty / loading / error

| Layar | Empty | Loading | Error |
| --- | --- | --- | --- |
| Listing folder | ikon folder kecil, "Folder ini kosong", tombol [Upload] [Folder baru] (drop zone aktif) | skeleton 12 baris / 24 kartu (≤ 300 ms tidak ditampilkan agar tidak berkedip) | kartu "Tidak bisa memuat folder" + kode + [Coba lagi] [Ke File Saya]; `NOT_FOUND` → "Folder tidak ditemukan" |
| Hasil filter/search | "Tidak ada yang cocok dengan '…'", [Hapus filter] | spinner inline + hasil incremental | |
| Recent / Favorit / Dibagikan / Share saya / Trash | satu kalimat + ikon + aksi relevan ("Belum ada favorit — klik ★ pada file") | skeleton tabel | kartu error |
| Tasks popover | "Tidak ada task" | — | — |
| Viewer | — | spinner tengah + nama file | "Tidak bisa membuka" + alasan (codec, signature) + [Unduh] |
| Editor | tab kosong: "Buka file dari explorer" | skeleton editor | banner merah di atas tab: konflik/baca gagal + aksi |
| Admin dashboard | — | skeleton kartu | kartu per bagian (dashboard tetap tampil sebagian) |
| Halaman publik | — | skeleton kartu | halaman 410/404 ramah dengan ilustrasi ikon kecil |
| Offline/SSE putus | — | — | banner kuning "Koneksi terputus, mencoba lagi…" (SSE reconnect) |

Semua toast: sukses 3 detik, error sampai ditutup (atau 8 detik), undo 10 detik dengan progress ring; maksimal 3 toast bertumpuk.

### 9.8 Konvensi i18n

- `i18next` + `react-i18next`, bahasa `id` (default) dan `en`, deteksi: pref user → `Accept-Language` → `DEFAULT_LANGUAGE`.
- File: `web/src/i18n/{id,en}/{common,auth,browser,fileops,upload,tasks,trash,meta,archive,search,editor,viewer,share,admin,profile,errors}.json`; key `area.component.key` dalam camelCase (`browser.toolbar.newFolder`), kode error di `errors.<CODE>`; plural dengan suffix i18next (`_one`, `_other`), interpolasi `{{count}}`; tidak ada string UI hardcoded (eslint rule `i18next/no-literal-string` untuk JSX).
- Format: `Intl.DateTimeFormat` (id-ID: `12 Okt 2026, 14.05`), relatif via `Intl.RelativeTimeFormat` (≤ 7 hari) + tooltip absolut; byte via helper `formatBytes(locale)` (id: `1,2 MB`, en: `1.2 MB`, basis 1024 dengan label KB/MB/GB); angka `Intl.NumberFormat`. Locale Intl: `id` → `id-ID`, `en` → `en-GB` (urutan hari-bulan-tahun dan jam 24 jam konsisten dengan id; `1.2 MB`). Helper: `formatBytes`, `formatDate` (`{time}` → "12 Okt 2026, 14.05"), `formatRelativeTime` (≤ 7 hari relatif, `numeric: 'auto'` → "kemarin"), `formatDateTimeFull` untuk tooltip.
- Backend mengirim `message` sesuai `Accept-Language` memakai katalog kecil (`internal/httpapi/i18n`), tetapi client selalu mengutamakan terjemahannya sendiri per `code`.
- Semua `aria-label`, judul dialog, placeholder, tooltip berasal dari katalog.

### 9.9 Responsif & aksesibilitas

| Lebar | Perilaku |
| --- | --- |
| ≥ 1280 | layout penuh; panel preview inline; mode dual-pane opsional (§9.3, Step 23b) |
| 768–1279 | (dual-pane nonaktif, kembali ke satu panel) sidebar ikon 48 px (tooltip, expand saat hover/klik), panel preview menjadi Sheet kanan, toolbar menampilkan 6 aksi utama + "⋯" |
| < 768 | sidebar Drawer (tombol ☰), address bar hanya breadcrumb terakhir + up, toolbar hanya [+] [Upload] [⋯], Details hanya Nama + Ukuran/Diubah (dua baris), view default List/Large, long-press 500 ms = context menu (Sheet bawah), mode seleksi (tap checkbox) dengan bottom bar aksi, viewer swipe prev/next, editor tetap bisa dibuka (read-only disarankan) |

Aksesibilitas (WCAG 2.2 AA): `FileGrid` = `role="grid"` dengan `row`/`gridcell`; model keyboard: satu tabstop pada kontainer grid dengan `aria-activedescendant` menunjuk baris aktif (bukan roving tabindex per baris, karena baris tervirtualisasi/di-recycle sehingga fokus per elemen bisa hilang — keputusan Step 7a); panah ↑/↓/Home/End/PageUp/PageDown + Enter; `aria-selected`, `aria-sort` pada header, `aria-rowcount`; semua `IconButton` punya `aria-label`; fokus ring selalu terlihat (`:focus-visible` 2 px accent); dialog memakai Radix (focus trap, `aria-modal`); `aria-live="polite"` region global untuk hasil operasi ("3 file dipindahkan", "Upload selesai"), `assertive` untuk error; kontras diverifikasi script; target sentuh ≥ 24×24 (mobile 44); tidak ada informasi hanya lewat warna (tag punya nama di tooltip, status share punya ikon + teks); `prefers-reduced-motion`; audit `axe` di Playwright untuk 8 layar utama (Step 21).

**Implementasi Step 21** (detail & bukti: `docs/A11Y.md`). axe (`@axe-core/playwright` 4.14.0, tag WCAG 2.0/2.1/2.2 A+AA + best-practice) di 11 layar × tema terang/gelap → 0 serious/critical (`web/e2e/a11y.spec.ts`); alur keyboard-only login → navigasi → rename → hapus → undo → upload tanpa event pointer (`keyboard-only.spec.ts`); tiga viewport tanpa scroll horizontal dokumen (`responsive.spec.ts`). Keputusan: (1) **< 768**: kolom Details = Nama + `summary` "Ukuran / Diubah" (dua baris, sort `mtime`; kolom khusus ponsel, bukan pilihan pengguna); long-press 500 ms dibuat aplikasi (`lib/longPress.ts`: event `contextmenu` di titik jari + `touchend` dibatalkan) karena long-press Radix tetap 700 ms; **context menu tampil di titik sentuh (menu Radix), bukan Sheet bawah**; ketuk baris = pilih, lalu **bottom bar seleksi** (`role=toolbar`: jumlah, Copy/Cut/Rename/Delete, ⋯, batalkan); semua `Button` ukuran default/sm/lg/icon ≥ 44 px (`max-md:min-h-11 min-w-11`), `xs` tetap 24 px; nama aplikasi di top bar `sr-only`; editor: explorer tertutup per sesi, catatan keterbatasan, toolbar menggulir di dalam. (2) **768–1279**: sidebar 48 px ikon (state `sidebarTabletExpanded` tidak dipersistenkan, tombol top bar melebarkan); panel preview overlay. (3) Nama aksesibel memuat teks yang terlihat (WCAG 2.5.3: menu pengguna = nama + `sr-only`; chip status bar editor `aria-label` = nilai tampil + " — " + fungsi); target minimum 24 px (menu sibling breadcrumb); tab `/search` menunjuk satu `tabpanel` hasil; `document.title` per route (`lib/routeTitle.ts`, "<folder> · File Saya · <app>"); `h1` sr-only di browser & editor; field dengan pesan error `aria-describedby` → pesan. (4) Warna: `--success`/`--warning` terang digelapkan (#15803d/#b45309) agar lulus 4,5:1 sebagai teks; token baru `--tag-*`, `--star`, `--star-fill`, `--danger-fg` (§9.1); tidak ada hex di `web/src` di luar `tokens.css`.

---

## §10 Keamanan

### 10.1 Threat model (STRIDE)

| Ancaman | Komponen | Kontrol | Test / verifikasi |
| --- | --- | --- | --- |
| **S**poofing: tebak password, curi cookie, pakai sesi orang lain | auth, sesi | argon2id; lockout bertahap; rate limit IP; cookie `HttpOnly Secure SameSite=Lax`; token 256-bit, DB simpan hash; HSTS; sesi dicabut saat ganti password/role | `auth_test.go` (lockout, timing), E2E login; cek header cookie |
| Spoofing share: tebak token/slug, brute-force password share | share | token 256-bit acak; password argon2id; rate limit per link+IP; cookie HMAC terikat password | `share_test.go` (brute-force → 429), test entropi token |
| **T**ampering: path traversal, symlink escape, TOCTOU, zip-slip | fsjail, archive | `os.Root` (openat2 beneath) untuk semua akses; extract di `tmp` dengan `os.Root`; validasi entri; symlink hanya relatif dalam root | `fsjail/jail_test.go` ≥ 90 %, fixture arsip berbahaya |
| Tampering: CSRF, cursor/undo dimanipulasi | httpapi | token CSRF per sesi + `Sec-Fetch-Site`; cursor & undo ditandatangani HMAC | integration test request tanpa/dengan token salah |
| Tampering: metadata referensi orang lain (favorit/share milik user B) | semua | setiap query `WHERE user_id = ?`; test IDOR | `idor_test.go` |
| **R**epudiation: admin menyangkal aksi, pengguna menyangkal hapus | audit, activity | audit log append-only (admin tidak bisa hapus baris; hanya retensi), request-id, IP | review log; test audit terisi untuk 12 aksi |
| **I**nformation disclosure: path absolut, stack trace, enumerasi user, password di `ps`, GPS EXIF, token di log, listing folder user lain | httpapi, archive, media, log | `fsjail.Error` tanpa path absolut; `INTERNAL` generik; login pesan generik + argon2 dummy; user-suggest ≥ 2 char & hanya username; `logredact`; GPS default disembunyikan; `ADMIN_CAN_BROWSE_USERS=false` + audit; ADR-08 untuk password CLI | grep log test untuk pola rahasia; test respons 500; test suggest |
| Disclosure via XSS/konten user | raw, viewer | HTML/SVG/XML/JS selalu attachment + octet-stream; CSP ketat + `sandbox` pada raw; DOMPurify; nama file sebagai text node; `nosniff` | test header raw per tipe; E2E file `<img onerror>` di nama & isi |
| **D**oS: zip bomb, folder raksasa, regex berat, upload tak terbatas, SSE koneksi banyak, thumbnail CPU | archive, listing, search, upload, sse, thumbs | limit entri/ukuran/rasio; listing paging & cache & batas waktu; RE2/rg + timeout + 1 pencarian aktif; `MAX_UPLOAD_SIZE` + kuota + pembersihan; maks 5 SSE/user; worker pool + prioritas + `nice` | fixture bomb; perf test 10k; test timeout search; test limit SSE |
| DoS: body JSON besar, slowloris, header besar | httpapi, nginx | `http.MaxBytesReader` 1 MB (kecuali tus/multipart), `ReadHeaderTimeout 10 s`, `IdleTimeout 120 s`; Nginx timeout | test body 2 MB → 413 |
| **E**levation: user → admin, viewer share → editor, admin ke folder user, perintah shell via nama file, setuid dari arsip, eksekusi file upload | authz, share, archive, deploy | `authz.Require`; permission dicek server per request; `as_user` hanya dengan flag + audit; argv tanpa shell + `--`; mode bit dibuang; storage di luar web root, Nginx hanya proxy; systemd `NoNewPrivileges`, user non-root | IDOR/permission test; test nama file `-rf`/`;` ke CLI; test mode hasil extract; verifikasi Nginx tidak menyajikan `/www/storage` |
| SSRF via remote download | download/remote | resolve + CIDR denylist + pin IP + cek redirect | `ssrf_test.go` dengan server lokal & DNS stub |
| Supply chain | CI | `govulncheck`, `pnpm audit`, lockfile, `go.sum`, tanpa CDN | CI gagal pada high |

### 10.2 Daftar kontrol

1. AuthN/AuthZ di setiap handler (middleware + `authz.Require`), tidak ada endpoint "tersembunyi" tanpa auth kecuali yang didaftar Pub/Sig/Shr.
2. `fsjail` wajib; `forbidigo` menegakkan.
3. Validasi input: body JSON lewat struct + validator; query lewat parser tipe; path lewat `fsjail.Clean`; ukuran body ≤ 1 MB.
4. Output encoding: JSON encoder standar (escape HTML `SetEscapeHTML(true)`), header `Content-Type` eksplisit, `nosniff`.
5. Rahasia: `APP_SECRET` hanya di env; turunan HKDF per tujuan; log redaksi; password CLI sesuai ADR-08.
6. Rate limiting (§10.5) in-memory token bucket per key (`golang.org/x/time/rate`) + fallback DB untuk login (bertahan restart).
7. Header keamanan (§10.3) di semua respons.
8. Dependensi CLI dipanggil dengan path absolut dari config, argv, env minimal, timeout, `nice`.
9. File upload tidak dieksekusi: storage di luar web root; tidak ada `location` Nginx yang menunjuk `/www/storage`; binary hanya membaca/menulis data.
10. Backup DB terenkripsi? Tidak (di dalam `DATA_DIR` 0750 milik `filemanager`); dokumentasi menyarankan backup off-site terenkripsi.
11. `govulncheck`, `pnpm audit`, `gosec` di CI; dependabot/renovate (jika hosting mendukung).
12. SECURITY.md: cara melapor, model ancaman ringkas, batasan yang diketahui (password 7z di argv jika stdin tidak didukung, signed URL tidak dicabut saat logout, preview-only share tidak mencegah simpan).

### 10.3 Header dan CSP final

Semua respons HTML/API:

```text
Strict-Transport-Security: max-age=31536000; includeSubDomains   (aaPanel juga menambah HSTS di site; duplikat tidak masalah)
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: strict-origin-when-cross-origin   (halaman /s/*: no-referrer)
Permissions-Policy: camera=(), microphone=(), geolocation=(), payment=(), usb=(), fullscreen=(self), picture-in-picture=(self), autoplay=(self)
Cross-Origin-Opener-Policy: same-origin
Cross-Origin-Resource-Policy: same-origin
Cache-Control: no-store   (API & index.html; aset hash: immutable)
X-Robots-Tag: noindex, nofollow   (seluruh aplikasi; tidak ada halaman publik yang perlu diindeks)
```

CSP untuk `index.html` (final Step 20; tidak ada script inline — tema diterapkan oleh `theme-init.js` eksternal — sehingga `script-src` tanpa nonce dan tanpa `unsafe-inline`; nonce tidak dipasang karena tidak ada yang memakainya. `upgrade-insecure-requests` hanya ditambahkan saat request diketahui HTTPS (TLS atau `X-Forwarded-Proto: https` dari proxy terpercaya); di luar itu tidak, agar E2E/dev lewat HTTP tidak memaksa upgrade):

```text
Content-Security-Policy:
  default-src 'self';
  script-src 'self' 'wasm-unsafe-eval';
  style-src 'self' 'unsafe-inline';
  img-src 'self' data: blob:;
  media-src 'self' blob:;
  font-src 'self';
  connect-src 'self';
  worker-src 'self' blob:;
  child-src 'self' blob:;
  object-src 'none';
  frame-src 'none';
  frame-ancestors 'none';
  base-uri 'self';
  form-action 'self';
  manifest-src 'self'
  (+ upgrade-insecure-requests hanya pada respons HTTPS)
```

Catatan: `script-src 'wasm-unsafe-eval'` (Step 17a) hanya mengizinkan kompilasi WebAssembly — pdf.js 6.x mendekode JPX (OpenJPEG), JBIG2 dan profil ICC (qcms) lewat modul WASM yang dimuat dari `/pdfjs/wasm/` (origin sendiri; pdf.js 6 tidak lagi punya jalur `new Function`, jadi `unsafe-eval` tetap tidak diperlukan); tanpa direktif ini gambar JPX/JBIG2 di PDF gagal dirender. `style-src 'unsafe-inline'` diperlukan karena Monaco, Radix (`style` attribute untuk posisi popover), dan Tailwind arbitrary animations menyuntikkan inline style; risiko CSS injection diterima (tidak memungkinkan eksekusi script). `worker-src blob:` karena Monaco membuat worker via blob URL ketika `MonacoEnvironment.getWorker` mengembalikan worker dari bundel (`?worker` Vite menghasilkan file terpisah → `'self'` cukup; `blob:` dipertahankan untuk pdf.js fake worker fallback; verifikasi saat implementasi dan hapus jika tidak perlu). Jika `CONTENT_BASE_URL` diisi: tambah origin itu ke `img-src`, `media-src`, `connect-src`.

Respons `raw/download/thumb/zip` dan playlist/segmen `files/hls/*` (Step 23d): `Content-Security-Policy: sandbox; default-src 'none'` + header §8.9. HLS tidak mengubah CSP aplikasi: hls.js mengambil playlist/segmen dengan XHR ke origin sendiri (`connect-src 'self'`) dan memutar lewat MSE (`blob:` sudah ada di `media-src`); worker transmux hls.js dibuat dari URL `blob:` (sudah diizinkan `worker-src 'self' blob:`). Diverifikasi di Chromium (E2E `hls.spec.ts`) tanpa pelanggaran CSP. Respons API JSON: `Content-Security-Policy: default-src 'none'`.

### 10.4 Kebijakan password dan sesi

- Password user: min `PASSWORD_MIN_LENGTH` (12), maks 128, bukan di daftar 100k umum, tidak mengandung username, tidak sama dengan sekarang/awal; tidak ada aturan komposisi (sesuai NIST); meter kekuatan di UI (zxcvbn-like ringan, opsional).
- Hash argon2id m=65536 KiB (64 MiB), t=3, p=1, salt 16 byte, output 32 byte; parameter disimpan di string PHC sehingga bisa dinaikkan (rehash saat login jika parameter lama < target). Di server 1,9 GB, 64 MiB per verifikasi dengan rate limit login 20/menit/IP aman.
- Password share: argon2id m=19456 KiB, t=2, p=1.
- Sesi: idle 2 h, absolut 168 h, maks 20 sesi aktif per user (yang tertua menurut `created_at` dicabut — bukan yang paling lama tidak dipakai; R-A m-09, Step 23a), cookie host-only, tanpa "remember me" terpisah (absolut 7 hari sudah cukup).
- Password sementara admin-generated: 16 karakter dari `A-Za-z0-9` + 2 simbol, ditampilkan sekali, `must_change_password=1`.
- TOTP (Step 23a, RFC 6238/4226): secret 20 byte `crypto/rand` (base32 untuk user), HMAC-SHA1, 6 digit, periode 30 s, window ±1 langkah, anti-replay lewat `users.totp_last_step` (langkah ≤ yang terakhir diterima ditolak); verifikasi konstan-waktu; implementasi pustaka standar (`crypto/hmac`, `crypto/sha1`, `encoding/base32`) di `internal/auth/totp.go`, diuji dengan vektor RFC 6238 Appendix B (SHA-1). Secret at rest: AES-256-GCM, kunci HKDF-SHA256(`APP_SECRET`, `totp-enc`), nonce 12 byte acak di depan ciphertext, AAD = id user (ciphertext tidak bisa dipindahkan antar akun). Kode pemulihan: 10 × 10 karakter dari alfabet `abcdefghjkmnpqrstuvwxyz23456789` (31 simbol ≈ 49,5 bit entropi), ditampilkan sekali sebagai `xxxxx-xxxxx`, disimpan sebagai HMAC-SHA256 dengan kunci HKDF(`APP_SECRET`, `totp-recovery`) — hash cepat berkunci cukup karena entropi kode ≈ 50 bit (bukan password pilihan manusia), tebakan online dibatasi limiter 5/menit per user, dan hash tanpa `APP_SECRET` tidak bisa diverifikasi offline dari backup DB; sekali pakai (`used_at`), pencocokan konstan-waktu atas semua kode yang belum dipakai. Rate limit `RATE_LIMIT_TOTP` 5/menit per user untuk login langkah TOTP, enable, disable, dan regenerasi kode pemulihan (password salah pada disable/regenerasi juga dihitung limiter ini). Secret, kode TOTP, dan kode pemulihan tidak pernah masuk log maupun `details_json` audit. Admin dapat mereset 2FA user (`user.reset_totp`, sesi dicabut). Sudo mode (konfirmasi ulang password/TOTP untuk aksi sensitif) **belum** diimplementasi — backlog (lihat PROGRESS).

### 10.5 Rate limit

| Key | Batas | Respons |
| --- | --- | --- |
| Login per IP | 20 percobaan per jendela geser 60 detik (bukan token bucket: argon2 ±0,4 s/percobaan membuat bucket terisi ulang sebelum 21 request selesai, sehingga AC "21 request/menit → 429" tidak terpenuhi; bukan burst 5 agar 5 kegagalan + percobaan ke-6 mencapai lockout 423 sebelum 429) | 429 + `Retry-After` |
| Login per username | lockout 5 gagal/15 menit → 1/5/15/60 menit | 423 |
| TOTP / kode pemulihan per user (login langkah 5, `me/totp/enable|disable|recovery-codes`) | 5 / menit (jendela geser, in-memory; `RATE_LIMIT_TOTP`) | 429 + `Retry-After` |
| Ganti password / reset | 5 percobaan **terverifikasi** (yang sampai ke pengecekan password sekarang) / 15 menit / sesi; pelanggaran kebijakan password baru tidak dihitung | 429 |
| Share unlock per (link, IP) | 10 / 10 menit | 429; setelah 50 gagal/jam per link → link `disabled` sementara 1 jam + notifikasi pemilik |
| Share unlock per IP | 30 / 10 menit | 429 |
| Endpoint publik lain per IP (meta/list/raw/download/zip/thumb) | 120 / menit, burst 60 | 429 |
| Token share tidak ditemukan per IP | 30 / menit | 429 (anti-enumerasi) |
| API umum per sesi | 600 / menit, burst 120 | 429 |
| `files/list` per sesi | 240 / menit | 429 |
| Thumb per sesi | 600 / menit | 429 |
| Content search per user | 10 / menit; 1 aktif | 429 |
| Name search per user | 30 / menit | 429 |
| Remote download task per user | 10 / jam | 429 |
| Task baru per user | `TASK_MAX_PER_USER` aktif | `TASK_LIMIT` |
| Download aktif per user | 3 | 429 `concurrent_downloads` |
| SSE per user | 5 koneksi | koneksi tertua ditutup |
| tus create per user | 60 / menit | 429 |
| Admin user-suggest per sesi | 60 / menit | 429 |
| `/healthz` | 60 / menit per IP (publik via Nginx, body hanya status) | 429 |
| `/readyz` `/metrics` | tidak dibatasi; tidak di-proxy Nginx (`return 404` di snippet), hanya loopback untuk systemd/update.sh | |

Implementasi (Step 20): angka di atas dibaca dari `config.RateLimits` (env `RATE_LIMIT_*`, §3.3). Limit per route (`healthz`, `files/list`, `files/sign`, `archives/sign`, `auth/csrf`, `upload/tus` create, `files/thumb`, unduh backup, dan API umum per sesi) ditegakkan oleh middleware token bucket `httpapi/ratelimit.go` yang menambahkan header `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset`, `RateLimit-Policy` pada setiap respons dan `Retry-After` + `429 RATE_LIMITED {retryAfter, limit}` saat ditolak. Limit yang hidup di dalam service (login, langkah TOTP per user, ganti password, share unlock/publik/not-found, suggest, search, remote download, download/thumb paralel, SSE) tetap ditegakkan di sana dengan angka dari konfigurasi yang sama. Limiter API umum mengecualikan route streaming/bertanda tangan (tus, thumb/raw/download/zip/entry-content/subtitle, `public/shares/*`, `events`) agar grid thumbnail dan unggahan berpotongan tidak tercekik; semuanya in-memory (hilang saat restart, kecuali login yang juga membaca `login_attempts`). Kunci per scope pada `files/thumb` hanya dipakai setelah tanda tangan URL diverifikasi (satu HMAC, tanpa DB); permintaan tanpa/dengan tanda tangan salah dihitung ke IP pemanggil (R-F M-02).


### 10.6 Daftar test keamanan (wajib lulus sebelum rilis, Step 20)

1. `fsjail`: semua kasus §5.3 (traversal, absolut, NUL, panjang, NFC, symlink keluar, TOCTOU, rename keluar).
2. Arsip: `slip.zip`, `abs.zip`, `symlink-escape.tar`, `hardlink-escape.tar` → `ARCHIVE_UNSAFE`, tidak ada file tercipta di luar `tmp`.
3. Arsip: `bomb-small.zip` dengan `ARCHIVE_MAX_EXTRACT_SIZE=1MB` → `ARCHIVE_LIMIT`, `tmp` dibersihkan; `many-entries.zip` → `ARCHIVE_LIMIT`.
4. Arsip: entri dengan mode setuid → hasil tanpa setuid; entri device/FIFO dilewati.
5. SSRF: URL ke `127.0.0.1`, `10.0.0.1`, `169.254.169.254`, `[::1]`, `0.0.0.0`, hostname yang resolve ke privat (stub resolver), redirect 302 ke privat, `http://user:pass@host`, skema `file://`/`ftp://` → `SSRF_BLOCKED`/`VALIDATION_FAILED`; DNS rebinding (resolver mengembalikan publik lalu privat) → koneksi tetap ke IP pertama (pin).
6. IDOR: user B mengakses `files/*` path milik A lewat `share` id palsu, `tasks/{idA}`, `versions/{idA}`, `trash/{idA}`, `shares/{idA}`, `upload/tus/{idA}`, `notifications/{idA}` → 403/404; admin tanpa flag `as_user` → 403.
7. CSRF: non-GET tanpa header → 403; header dari sesi lain → 403; `Origin` beda → 403; `Sec-Fetch-Site: cross-site` → 403.
8. Sesi: cookie expired idle/absolut → 401; sesi dicabut → 401; ganti password mencabut sesi lain; user disabled → 403 segera.
9. `PASSWORD_CHANGE_REQUIRED` memblokir semua S+P (uji 10 endpoint acak) tetapi tidak `auth/me`, `auth/change-password`, `auth/logout`.
10. Login: pesan identik untuk user tidak ada vs password salah; waktu respons tidak berbeda > 20 % (uji 50 sampel); lockout 423 setelah 5 gagal; counter reset setelah sukses.
11. Password umum (`password12345`) ditolak; sama dengan awal ditolak.
12. Header: semua respons HTML/API memuat header §10.3; raw HTML/SVG → `attachment` + `octet-stream` + `sandbox`; raster → inline; `nosniff` ada di semua.
13. XSS: nama file `<img src=x onerror=alert(1)>.png`, catatan `<script>`, Markdown dengan `<script>`/`javascript:` link, SVG dengan script → tidak ada eksekusi (Playwright memantau `dialog` event dan CSP violation).
14. Rate limit: login 21× dalam 1 menit → 429; share unlock 11× → 429; `Retry-After` ada.
15. Signed URL: tanpa `sig` → 403; `sig` dimodifikasi → 403; `exp` lewat → 403 `SIGNATURE_EXPIRED`; `path` diganti → 403; scope user lain → 403.
16. Share: token kedaluwarsa → 410; disabled → 410; password salah → 401 + log; cookie share dari share lain → 401; `max_downloads` tercapai → 410; path `..` di listing publik → 400; path di atas item → 404.
17. Share editor: penerima viewer `PUT content` → 403; editor bisa; penerima tidak bisa `shares` create pada item share (tanpa `allowReshare`).
18. Upload: `Upload-Length` > `MAX_UPLOAD_SIZE` → 413; metadata nama `../x` → 400; ekstensi diblokir → 415; kuota penuh → 413; finalisasi dengan ukuran tidak cocok → gagal & file tidak muncul.
19. Nama file ke CLI: `-rf`, `--version`, `; rm -rf`, spasi, unicode → CLI menerima sebagai nama (argv + `--`), tidak ada efek samping (uji dengan compress & thumbnail).
20. Log: jalankan alur login/share/signed URL lalu grep log untuk password, token, `sig=` → tidak ditemukan.
21. Admin terakhir tidak bisa dihapus/diturunkan/dinonaktifkan.
22. `govulncheck` dan `pnpm audit --audit-level=high` bersih (atau temuan didokumentasikan dengan mitigasi di SECURITY.md).
23. Nginx: `curl https://file.kppbwi.app/.env`, `/backend/go.mod`, `/bin/filemanager`, `/.git/config` → 404/403 (uji di Step 22).
24. Proses: `ps -o user,cmd -p $(pidof filemanager)` = `filemanager`, bukan root; `systemd-analyze security filemanager.service` skor ≤ 4 (verifikasi angka yang bisa dicapai).

---

## §11 Strategi testing

### 11.1 Lapisan

| Lapisan | Alat | Cakupan | Perintah |
| --- | --- | --- | --- |
| Unit backend | `go test`, table-driven, `testing/fstest`, temp dir (`t.TempDir()`) | fsjail, config parse, natural sort, cursor, signer, SSRF guard, archive limits, batch rename, encoding, EOL, zipaes, lock manager, quota math | `make test` |
| Integration backend | `httptest.Server` + SQLite file temp + storage temp; helper `testenv.New(t)` membuat admin + user A + user B + sesi | semua handler per grup: auth, files, upload (tus client Go kecil), download Range, archives (dengan/tanpa 7zz → skip), search (rg jika ada, fallback), shares publik, admin, tasks + SSE (client `bufio` membaca event), IDOR, CSRF, rate limit (clock yang bisa di-mock) | `make test` |
| Unit frontend | Vitest + Testing Library + MSW (mock API) | komponen kritis: FileGrid seleksi/keyboard, ConflictDialog, BatchRenameDialog preview, shortcuts registry, formatBytes/date, api client error mapping, upload queue state | `pnpm -C web test` |
| E2E | Playwright (Chromium + Firefox; WebKit opsional) terhadap binary nyata (`make build` + storage temp + `.env` test), fixture user dibuat via API admin | alur PRD: admin login pertama → ganti password → buat user → user login (folder dibuat) → upload (1 MB & 100 MB resume: matikan jaringan via `context.setOffline`) → rename/move/copy/delete/undo → trash restore → compress/extract → editor save/konflik/versi → viewer tiap tipe (fixture gambar/pdf/mp3/mp4 kecil) → share publik (password) → share ke user → penerima buka/edit → admin panel → a11y axe 8 layar → mobile viewport 390 px alur dasar | `make e2e` |
| Performance | script `backend/scripts/genfolder.go` (10.000 file + 500 folder, ukuran acak kecil) + benchmark `go test -bench` listing; Playwright mengukur TTFB `files/list` dan waktu render pertama (performance.mark) | p95 < 500 ms API, < 1 s render, scroll 60 fps (trace) | `make perf` |
| Keamanan | §10.6 sebagai test otomatis di `backend/internal/httpapi/security_test.go` + E2E XSS + `govulncheck` | | `make vuln`, `make test` |
| Deploy smoke | `deploy/smoke.sh`: curl `/readyz` via Nginx, login API, upload 1 file 32 MB (melewati chunk), download Range, SSE 5 detik, cek header, cek 404 untuk file sensitif | dijalankan setelah install/update | `make smoke BASE_URL=…` |

### 11.2 Fixture arsip berbahaya

Dibuat oleh `backend/testdata/archives/gen.go` (`go generate`), bukan di-commit sebagai biner buram (kecuali `aes.zip`, `test.7z` kecil yang dibuat dengan tool eksternal dan dicatat perintahnya):

- `slip.zip`: `zip.Writer` dengan nama `../../evil.txt` dan `sub/../../evil2.txt`.
- `abs.zip`: nama `/etc/evil`, `C:\evil.txt`.
- `symlink-escape.tar`: `tar.Writer` header `Typeflag=TypeSymlink`, `Name=x`, `Linkname=../../outside`, lalu file `x/evil`.
- `hardlink-escape.tar`: `TypeLink` ke `../../etc/passwd`.
- `bomb-small.zip`: satu file 10 MB berisi nol (ratio ≈ 1000:1), nested `bomb-small.zip` di dalam `nested.zip`.
- `many-entries.zip`: 10.001 entri kosong.
- `setuid.tar`: file mode `04755`.
- `gbk-names.zip`: nama `测试.txt` di-encode GBK tanpa flag UTF-8.
- `windows-names.zip`: `CON.txt`, `trailing. `, `a|b.txt`.
- Semua test memverifikasi tidak ada file tercipta di luar folder tujuan dengan `filepath.WalkDir` pada `t.TempDir()` induk + canary file di `../outside`.

### 11.3 Test SSRF

`internal/netguard/netguard_test.go` + `internal/remotedl/runner_test.go` + `httpapi/remotedl_test.go` (Step 13): `httptest.Server` lokal (loopback) sebagai target → harus `SSRF_BLOCKED` (karena 127.0.0.1) **kecuali** test menyuntikkan `netguard.Policy{AllowLoopback: true}` (HTTP: `TEST_REMOTE_ALLOW_LOOPBACK=1`) untuk menguji logika download; resolver di-mock (`Resolver` interface) untuk: hostname → `10.0.0.5`; hostname → `[2001:db8::1, 93.184.216.34]` (satu privat → tolak); rebinding (`[93.184.216.34]` lalu `[127.0.0.1]`) → dial tetap ke IP pertama (verifikasi lewat `DialContext` yang dicatat); redirect chain 6 → gagal; redirect ke `file://` → gagal; `Content-Length` > batas → gagal sebelum baca; tanpa `Content-Length` melewati batas → gagal + part dihapus; resume Range setelah putus (server mengirim 206).

### 11.4 Test IDOR dan permission

`testenv` membuat A, B, admin; untuk setiap resource bertipe (file path, task, version, trash item, share, recipient, upload session, notification, folder_view_prefs) dibuat oleh A, uji B: GET/PATCH/DELETE → 403/404 (nilai yang konsisten: `NOT_FOUND` untuk id acak, `FORBIDDEN` jika id valid tapi bukan milik — pilih **`NOT_FOUND` untuk keduanya** agar tidak membocorkan keberadaan). Share mode users: B viewer → tulis 403; B editor → tulis OK tetapi `path=/../x` → 400; admin tanpa flag → 403 pada `as_user`.

### 11.5 Performance 10.000 file

`genfolder` membuat `/Besar` dengan 10.000 file (nama bervariasi: angka, unicode, huruf besar/kecil) + 500 folder; benchmark `listing.List` dingin & cache; integration test mengukur p95 dari 50 request `files/list` < 500 ms (di CI toleransi 1 s); Playwright: buka folder, ukur `performance.measure('first-rows')` < 1 s, scroll ke bawah tanpa frame drop > 50 ms (trace), sort kolom < 300 ms (cache).

**Catatan pengukuran Step 7a (server 1 vCPU, Chromium + backend + runner berbagi CPU).** Backend sendiri (curl, tanpa browser): list 10.000 entri dingin 160–580 ms, cached 5 ms. Lewat Playwright di mesin yang sama: request list 1,8–2,3 s, render pertama (dblclick → baris pertama) 2,3–3,4 s, back-navigation cache hangat 300–600 ms, long task maksimum ≈ 800 ms saat halaman berikutnya (500 item) ditambahkan (hotspot: TanStack Table membangun ulang row model untuk seluruh item yang dimuat per halaman — kandidat optimasi Step 21: render baris langsung dari `items`/`createWorkerRowModel`). Target PRD/SPEC (< 1 s, < 150 ms, long task ≤ 100 ms) tetap menjadi gerbang **di Step 21 pada perangkat klien nyata**; di E2E, ambang ketat hanya aktif dengan `E2E_PERF_STRICT=1`, selain itu angka dicatat di log dan hanya batas longgar (6 s / 1,5 s / 800 ms) yang diuji agar suite tidak flaky di server ini. **Step 21:** gerbang ketat kini aktif tanpa flag di `web/e2e/perf.spec.ts` (render pertama 10.500 entri < 1 s, navigasi hangat back/forward p95 < 150 ms, long task maks ≤ 100 ms saat scroll seluruh folder, sort sampai baris pertama < 300 ms), diukur di dalam halaman (`performance.mark/measure` + `PerformanceObserver longtask`) dengan tracing Playwright dimatikan untuk spec itu (snapshot DOM tracing terukur sebagai long task aplikasi); `E2E_PERF_STRICT` dan uji 10k di `browser-nav.spec.ts` dihapus. Hasil 4 vCPU (browser + backend + runner satu mesin): 205–273 ms / p95 86–100 ms / long task maks 62–85 ms / sort 171–234 ms (`docs/PERF.md` §6).
**Implementasi Step 21.** Fixture `backend/internal/perf/gen.go` (`perf.Generate`: `/Besar` 10.000 file campuran — angka beragam lebar, huruf besar/kecil, Unicode NFC, Jepang, spasi, dotfile, multi-titik, 16 ekstensi, 0–32 KB — + 500 folder berisi 0–3 file, `/Gambar` 200 PNG 64–320 px; deterministik per seed; `perf.Stream` data acak tanpa memori untuk upload, `perf.WriteStoreZip` zip Store 1 GiB untuk extract) menggantikan `backend/scripts/genfolder.go`. `make perf` = `make bench` (in-process, `GOMAXPROCS` semua CPU dan 1) + harness `backend/internal/perf/perf_test.go` `TestPerf` (k6 tidak terpasang → klien Go `net/http` dengan keep-alive, cookie, CSRF, gzip transparan) terhadap **binary nyata** di `.perfdata/` (bukan `STORAGE_ROOT`/`.env` produksi; dihapus setelah selesai), dua kali: semua CPU dan dipin CPU 0 (klien di CPU lain). Skenario: `files/list` 10.500 entri dingin (mtime folder diubah sebelum tiap permintaan = cache §8.3 tidak valid; page cache OS tetap hangat — `drop_caches` tidak dipakai di server produksi) 50× sort nama + 20× sort mtime, hangat 50×, seluruh 21 halaman lewat cursor, navigasi berulang 6 folder 120×, 200 thumbnail 256 px (20 paralel, polling 202 tiap 200 ms) sambil mengukur `files/list` bersamaan, upload tus 1 GiB (16 MiB per PATCH), zip streaming 1 GiB, extract zip Store 1 GiB (task); RSS dari `/proc/<pid>/status` (sampling 50 ms) + gauge Go dari `/metrics`. Gerbang (gagal bila meleset, `FM_PERF_ASSERT=0` hanya melapor): list dingin p95 < 500 ms, hangat p95 < 100 ms, idle RSS < 100 MB, puncak RSS upload/zip/extract ≤ baseline + 100 MB. Batas rate limit API/list/sign/thumb/csrf/tus dimatikan khusus harness (mengukur latensi, bukan anggaran §10.5). Hasil JSON di `.perfdata/results-<n>cpu.json`, ringkasan di `docs/PERF.md`.

### 11.6 Coverage dan gerbang CI

`ci.yml`: job `backend` (lint, sqlc diff, test -race, cover-check, govulncheck), `web` (lint, typecheck, test, check:contrast, `pnpm build --report` dengan bundle budget `web/scripts/bundle-size.mjs` sejak Step 21 — gagal bila JS awal (entry + `modulepreload` di `index.html`) ≥ 300 KB gzip, laporan semua chunk `web/bundle-report/` sebagai artefak, plus uji-diri gerbang dengan `BUNDLE_BUDGET_KB=100`; `size-limit` tidak dipakai), `openapi` (lint + route-coverage test), `e2e` (build binary, Playwright Chromium, artefak trace saat gagal), `perf` (Step 21: `make perf`, informatif/`continue-on-error` karena runner bersama berisik, artefak `.perfdata/*.json`), `release` (tag → build tar.gz). Ambang coverage: `fsjail` ≥ 90 %, `auth`, `share`, `archive`, `download` (signer/remote) ≥ 80 %, total backend ≥ 70 %. Test yang butuh CLI eksternal memakai `t.Skip` jika binary tidak ada dan CI memasang `7zip ffmpeg libvips-tools poppler-utils ripgrep` via apt (ubuntu-latest).

---

## §12 Deployment

Target: Ubuntu 24.04 + aaPanel 8.0.7 di server ini; binary + systemd; Nginx aaPanel sebagai reverse proxy untuk `https://file.kppbwi.app`. Langkah lengkap klik-per-klik ditulis ulang di `docs/DEPLOY-AAPANEL.md` (Step 22) berdasarkan bagian ini.

### 12.1 `deploy/install.sh` (idempoten, dijalankan `sudo bash deploy/install.sh` dari APP_DIR)

```text
 0. set -euo pipefail; APP_DIR = repo tempat script berada; STORAGE=/www/storage/FileManager; SVC_USER=filemanager; cd APP_DIR
 1. Cek root (dry-run boleh tanpa root); /etc/os-release Ubuntu 22.04|24.04, lain → berhenti.
 2. RAM < 3 GB dan swap < 2 GB dan build di server → peringatan + perintah swapfile 2 GB; menawarkan membuat
    /swapfile (fstab) hanya interaktif, default TIDAK (--yes tidak membuatnya; --no-swap tidak menawarkan).
 3. apt: hanya paket yang belum terpasang (dpkg-query) → apt-get update + install
    ffmpeg libvips-tools poppler-utils ripgrep 7zip curl ca-certificates openssl
    - 7zip: binary /usr/lib/7zip/7z (24.04, diverifikasi); tanpa paket 7zip (22.04, perlu verifikasi) → p7zip-full;
      binary terdeteksi pertama dari /usr/lib/7zip/7z, /usr/bin/7zz, /usr/lib/p7zip/7z → SEVENZIP_PATH di .env baru bila beda
    - unrar: hanya --with-unrar (add-apt-repository multiverse bila perlu) → UNRAR_PATH=/usr/bin/unrar bila kosong
 4. Go (hanya bila build): versi minimum = direktif `go` di backend/go.mod; kurang/tidak ada → rilis stable terbaru
    linux-<arch> dari https://go.dev/dl/?mode=json (versi + sha256 dibaca saat runtime, tidak ditulis di script),
    sha256sum -c, ganti /usr/local/go, /etc/profile.d/go.sh.
 5. Node (hanya bila build): ≥ 22, kalau tidak → berhenti dengan saran --binary; corepack enable bila pnpm tidak ada.
 6. useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin filemanager.
 7. .env (sebelum storage, agar DATA_DIR dari .env dipakai):
    - belum ada → .env.example tanpa komentar inline (systemd EnvironmentFile= tidak membuangnya); STORAGE_ROOT/DATA_DIR,
      LISTEN_ADDR, APP_BASE_URL ditulis bila di-override (FM_STORAGE, FM_LISTEN_ADDR, FM_BASE_URL)
    - ada → komentar inline dibuang; nilai lain tidak diubah; salinan .env.bak-<ts> (0600) bila ada perubahan
    - APP_SECRET kosong/CHANGE_ME → openssl rand -hex 32; nilai lain yang bukan 64 hex → berhenti
    - ADMIN_INITIAL_PASSWORD kosong/CHANGE_ME → ditanya tanpa echo dari /dev/tty (≥ 12, tanpa spasi/kutip/\/$/#/`),
      kosong/--yes/non-tty → acak 24 alnum; password acak hanya dicetak ke /dev/tty (tidak ke stdout/log)
    - owner filemanager, 0600; WARN bila ALLOW_ROOT=true atau LISTEN_ADDR bukan loopback
 8. Storage: mkdir -p; owner lama www:www → WARN; isi selain .system/lost+found → chown -R ditanya (default tidak);
    chown filemanager:filemanager + chmod 0750; .system/{db,trash,versions,uploads,thumbs,tmp,logs,backups,deleted-users}
    dibuat 0750; chown -R .system bila ada file milik user lain. DATA_DIR di luar STORAGE → WARN (ReadWritePaths).
 9. APP_DIR: pemilik TIDAK diubah (git menolak work tree milik user lain — "dubious ownership" — dan service tidak
    boleh bisa menulis binary-nya sendiri; ProtectSystem=strict membuatnya read-only bagi proses). mkdir public/.well-known
    dan bin/ 0755. Kecuali --skip-aapanel: hapus index.html, 404.html, .htaccess, .user.ini (chattr -i dulu) di APP_DIR
    dan public/ yang tidak di-track git.
10. Binary: --binary PATH (cmp; install -m 0755) | --skip-build (pakai yang ada) | make build sebagai pemilik repo
    (pemilik .git), dilewati bila bin/filemanager sudah dari commit HEAD dan work tree bersih (--rebuild memaksa).
11. bin/filemanager check-deps + migrate up sebagai filemanager (runuser).
12. Unit dirender dari deploy/filemanager.service (path APP_DIR/STORAGE/user diganti hanya bila di-override), tulis bila
    berbeda + daemon-reload; systemctl enable; start bila belum aktif, restart bila unit/binary/.env berubah, selain itu
    tidak disentuh; tunggu /readyz di LISTEN_ADDR ≤ 30 s (gagal → journalctl 50 baris, exit 1).
13. Ringkasan + checklist aaPanel (12.3) + perintah smoke test; pengingat menghapus ADMIN_INITIAL_PASSWORD.
Tidak pernah menyentuh UFW/firewall aaPanel, Nginx, atau konfigurasi aaPanel.
```

Flag: `--dry-run` (hanya mencetak rencana; semua langkah yang mengubah sistem lewat satu helper `act`/`plan`),
`--yes`, `--binary <path>`, `--skip-build` (= `--no-build`), `--rebuild`, `--with-unrar`, `--skip-apt`,
`--skip-aapanel`, `--no-swap`. Override untuk test/CI: `FM_APP_DIR`, `FM_STORAGE`, `FM_SYSTEMD_DIR`, `FM_SVC_USER`,
`FM_SKIP_APT=1`, `FM_SKIP_USER=1`, `FM_ALLOW_NONROOT=1`, `FM_LISTEN_ADDR`, `FM_BASE_URL`, `FM_READY_TIMEOUT`,
`FM_OS_RELEASE`. Test: `deploy/test/install_test.sh` (folder scratch + shim `systemctl`), CI `install-smoke`
(systemd nyata di VM runner, dua kali).

### 12.2 Unit systemd (`deploy/filemanager.service`)

```ini
[Unit]
Description=FileManager (file.kppbwi.app)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
User=filemanager
Group=filemanager
WorkingDirectory=/www/wwwroot/file.kppbwi.app
EnvironmentFile=/www/wwwroot/file.kppbwi.app/.env
Environment=GOMEMLIMIT=512MiB
Environment=TMPDIR=/www/storage/FileManager/.system/tmp
ExecStart=/www/wwwroot/file.kppbwi.app/bin/filemanager serve
Restart=on-failure
RestartSec=3
TimeoutStopSec=30
KillSignal=SIGTERM
LimitNOFILE=65536
UMask=0027

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/www/storage/FileManager
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectKernelLogs=true
ProtectControlGroups=true
ProtectClock=true
ProtectHostname=true
ProtectProc=invisible
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
RestrictRealtime=true
RestrictSUIDSGID=true
LockPersonality=true
RemoveIPC=true
MemoryDenyWriteExecute=false
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged
SystemCallErrorNumber=EPERM
CapabilityBoundingSet=
AmbientCapabilities=
# Opsional — aktifkan HANYA jika REMOTE_DOWNLOAD_ENABLED=false (remote download butuh koneksi keluar):
#IPAddressDeny=any
#IPAddressAllow=localhost

[Install]
WantedBy=multi-user.target
```

Catatan (Step 22): `~@resources` **tidak** dipakai — grup itu memuat `setpriority`, `ioprio_set`, `setrlimit`
(`systemd-analyze syscall-filter @resources`), sehingga `nice`/`ionice` yang membungkus setiap CLI (`internal/exec`) akan
dibunuh SIGSYS; `SystemCallErrorNumber=EPERM` membuat syscall terfilter gagal dengan EPERM alih-alih membunuh proses.
`ProtectProc=invisible` = hidepid per-service. Skor `systemd-analyze security --offline=yes` = 1.4 OK (nilai final
setelah unit terpasang dicatat di `docs/DEPLOY-AAPANEL.md` §12). `MemoryDenyWriteExecute=false` karena ffmpeg/libvips dapat memakai JIT/`mprotect` (verifikasi; jika aman, set true). `ProtectSystem=strict` membuat `/www/wwwroot/file.kppbwi.app` read-only bagi proses (benar: aplikasi tidak menulis ke APP_DIR). `.env` dibaca systemd sebagai root → alternatif lebih ketat `chown root:root .env` tetap bekerja (dicatat sebagai opsi). `TMPDIR` diarahkan ke `.system/tmp` agar CLI tidak menulis ke `/tmp` privat yang kecil.

### 12.3 Langkah aaPanel klik-per-klik

**Diverifikasi Step 22 (25 Okt 2026) dari source aaPanel 8.0.7 di server (`mod/project/proxy/comMod.py`, hanya dibaca):**
vhost proyek Reverse Proxy dibangun ulang dari `/www/server/proxy_project/sites/<domain>/<domain>.json` setiap
pengaturan berubah; kustomisasi yang bertahan hanya kolom `custom_conf` per URL proxy (dirender di dalam
`location ^~ <path> { … }`, setelah `proxy_pass`, header bawaan, timeout, dan blok websocket) dan `server_block`
site (di dalam `server { }` antara `#SERVER-BLOCK START/END`, setelah blok proxy); timeout = kolom per URL
(`proxy_connect/send/read_timeout`, default 60/600/600). `site_path` selalu `/www/wwwroot/<domain>` (running directory
tidak tersedia); aaPanel membuat `<site_path>/proxy_cache_dir` milik `www` + `proxy_cache_path` level http; hapus
proyek dengan `remove_path=1` menjalankan `rm -rf /www/wwwroot/<domain>` (= repo + `.env`). `nginx.conf` meng-include
`proxy.conf` dengan `proxy_cache cache_one;` dan `client_max_body_size 50m` level http untuk semua site. Include Lua
`well-known/<domain>.conf` menyajikan `/.well-known/*` dari `document_root` (varian A = site directory, bukan
`public/`), fallback `/www/wwwroot/java_node_ssl`. Log statistik `site_total` memuat `$http_cookie`.

Jalur utama — **tipe proyek Reverse Proxy** (terverifikasi ada di aaPanel 8.0.7 server ini; nama menu persis perlu verifikasi saat Step 22):

1. Panel aaPanel → **Website** → tab/kategori **Reverse Proxy** (atau "Proxy project") → **Add** (perlu verifikasi nama tombol).
2. Domain: `file.kppbwi.app`. Target/`proxy_pass`: `http://127.0.0.1:8080`. Send domain/`proxy_host`: `$http_host`. Path: `/`. Remark: `FileManager`. Simpan.
3. Setelah dibuat, aaPanel menulis `/www/server/panel/vhost/nginx/file.kppbwi.app.conf` dengan `root /www/wwwroot/file.kppbwi.app` (root diisi otomatis; verifikasi). Karena semua `location /` di-proxy, Nginx tidak menyajikan file apa pun dari root kecuali `.well-known` (via include Lua aaPanel). Jika UI memungkinkan mengatur site directory → set ke `/www/wwwroot/file.kppbwi.app/public`.
4. URL proxy `/` → timeout connect 10 / send 3600 / read 3600 s; **custom configuration** ← PART A dari `deploy/nginx-aapanel.conf`; cache off. Site → **Config** → **server block** ← PART B (lihat 12.4); simpan — aaPanel menjalankan `nginx -t` dan reload. Verifikasi dengan `nginx -T` (perintah di `docs/DEPLOY-AAPANEL.md` 4.A). Nama menu perlu verifikasi; `deploy/aapanel/site.py configure` memanggil `set_url_proxy`, `set_url_custom_conf`, `save_config(conf_type=server_block)`.
5. Site → **SSL** → **Let's Encrypt** → pilih domain → verifikasi file (HTTP-01; `.well-known/acme-challenge` dilayani dari document root oleh aaPanel) → Apply; aktifkan **Force HTTPS**.
6. Verifikasi: `curl -I https://file.kppbwi.app/healthz` → 200 (`{"status":"ok"}`, HSTS ada); `curl -I https://file.kppbwi.app/readyz` → 404 (sengaja tidak di-proxy); `curl -I https://file.kppbwi.app/` → 200 dengan header aplikasi; `curl https://file.kppbwi.app/.env` dan `/bin/filemanager` → 404; `curl http://file.kppbwi.app/.well-known/acme-challenge/test` (file uji di `.well-known/acme-challenge/` document root: varian A `/www/wwwroot/file.kppbwi.app/`, varian B `public/`) → 200 dari Nginx, bukan dari aplikasi.
7. Firewall: jangan membuka 8080 (UFW/aaPanel Security). Sudah tertutup saat ini.

Jalur alternatif (persis seperti teks prompt): **Website → PHP project → Add site**: domain `file.kppbwi.app`, site directory `/www/wwwroot/file.kppbwi.app`, PHP version **Static**, tanpa DB/FTP → Site → **Site directory** → Running directory `/public` → Site → **Reverse proxy** → Add: name `app`, target `http://127.0.0.1:8080`, send domain `$host`, proxy directory `/` → Config → tempel snippet → SSL → Force HTTPS. Hapus `index.html`, `404.html`, `.htaccess`, `.user.ini` (`chattr -i` dulu) yang dibuat aaPanel.

Larangan: jangan mengedit `/www/server/panel/vhost/nginx/file.kppbwi.app.conf` langsung (ditulis ulang aaPanel saat mengubah SSL/proxy). Snippet kustom hanya lewat editor Config di UI (yang juga menulis file itu, tetapi aaPanel menjaga blok yang ditandai komentar; verifikasi bahwa blok kustom bertahan setelah mengubah SSL — jika tidak, letakkan di `extension/<domain>/filemanager.conf`? folder itu dikelola aaPanel (berisi `site_total.conf`), perlu verifikasi apakah file tambahan bertahan; dokumentasikan hasil verifikasi di `DEPLOY-AAPANEL.md`).

Otomasi opsional: `deploy/aapanel/site.py` meniru pola `proxy.kppbwi.app` (`mod.project.proxy.comMod`) untuk `status|create|configure|ssl|force-https`; tetap dianggap "perlu verifikasi" per versi panel (belum dijalankan; menunggu persetujuan pemilik).

### 12.4 `deploy/nginx-aapanel.conf`

File itu adalah kontrak; ringkasan (Step 22 menggantikan draf sebelumnya):

- **PART A** — custom configuration URL proxy `/` (di dalam `location ^~ /` aaPanel): `X-Forwarded-Proto $scheme`,
  `X-Forwarded-Host $host`, `X-Request-ID $request_id` (aaPanel sudah mengirim `Host $http_host`, `X-Real-IP`,
  `X-Forwarded-For`, websocket), `proxy_request_buffering off`, `client_max_body_size 64m`, `client_body_timeout 300s`,
  `proxy_buffering off`, `proxy_max_temp_file_size 0`, `proxy_cache off` (menimpa `proxy_cache cache_one` global
  aaPanel), tanpa `add_header`, baris `access_log` opsional (mematikan log statistik `site_total` yang memuat Cookie).
  Timeout **tidak** di PART A (direktif ganda ditolak `nginx -t`) — diisi di kolom timeout URL proxy.
- **PART B** — server block: `set_real_ip_from` untuk 22 rentang resmi Cloudflare (`www.cloudflare.com/ips-v4|ips-v6`,
  disalin 28 Okt 2026) + `real_ip_header CF-Connecting-IP` — domain `file.kppbwi.app` **proxied Cloudflare** sejak Okt
  2026, sehingga tanpa ini aplikasi melihat semua pengunjung sebagai IP Cloudflare dan limiter per-IP (§10.5) menghitung
  semua orang bersama; header hanya dipercaya dari rentang itu (hit langsung ke IP origin tidak bisa memalsukannya),
  tidak berpengaruh bila DNS dikembalikan ke "DNS only"; `nginx_snippet_test.sh` memastikan header dari peer non-Cloudflare
  diabaikan dan memperingatkan bila daftar rentang berbeda dari yang dipublikasikan Cloudflare.
  Lalu `location = /readyz` dan `= /metrics` → 404; `if ($uri ~* "^/(bin|backend|web|deploy|docs|migrations|proxy_cache_dir)(/|$)")`,
  `if ($uri ~* "^/(Makefile|NOTICE|README\.md|PRD\.md|SPEC\.md|PROMPT\.md|PROGRESS\.md|promp-master\.md|go\.mod|go\.sum)$")`
  dan `if ($uri ~ "/\.(?!well-known(/|$))")` → `return 404`; `location ^~ /s/` dan `^~ /api/v1/public/` dengan
  `access_log off` + pengaturan proxy lengkap (token link publik tidak tercatat, review E m-14).
- **PART C** (opsional, http block) — contoh `limit_req_zone` untuk login.

Kenapa bukan `location ~` seperti draf: blok proxy aaPanel `location ^~ /` menghentikan evaluasi location regex,
sehingga `location ~ /\.(?!well-known)` dan `location ~* ^/(bin|…)` (juga regex larangan bawaan aaPanel) tidak
pernah berlaku; `if` level server berjalan sebelum pemilihan location. Dibuktikan `deploy/test/nginx_snippet_test.sh`
(Nginx aaPanel 1.30.6 dengan template server block Reverse Proxy, prefix scratch): dengan snippet 21 path sensitif
→ 404 dari Nginx, ACME dari document root, 60 MiB lolos / 70 MiB → 413, Range 206, token `/s/` tidak di log; tanpa
PART B path yang sama diteruskan ke aplikasi. Gzip: aplikasi mengompresi sendiri (`httpapi.Compress`, Step 21);
gzip global aaPanel dibiarkan; brotli tidak ada di build Nginx aaPanel.

### 12.5 `deploy/update.sh` dan rollback

```text
 1. Seluruh script di dalam main() (bash mem-parse semuanya sebelum git checkout bisa menimpa file); root; flock
    bin/.update.lock (fd tidak diwariskan ke service); log tee ke DATA_DIR/logs/update-<ts>.log; cd APP_DIR.
    /readyz saat ini tidak 200 → tanya (default berhenti).
 2. Binary baru → bin/filemanager.new: --binary PATH | --release <tar.gz> (sha256 WAJIB: --sha256 atau <file>.sha256;
    hanya anggota bin/filemanager diekstrak, --no-same-owner) | --ref REF (work tree bersih; git fetch bila ada remote,
    checkout, ff-only ke upstream) lalu build | default: make build BIN=bin/filemanager.new sebagai pemilik repo.
    `<new> version` harus jalan.
 3. bin/filemanager backup-db --label pre-update (versi yang berjalan, sebagai filemanager) → path dari stdout
    "backup written: <nama>"; salin bin/filemanager → bin/filemanager.prev (hanya satu versi sebelumnya disimpan).
 4. V_BEFORE = versi skema tertinggi yang applied menurut `<new> migrate status`; systemctl stop;
    `<new> migrate up` (gagal → rollback); mv .new → bin/filemanager; systemctl start; /readyz ≤ 30 s
    (--ready-timeout; berhenti lebih awal bila unit "failed") → "UPDATE OK".
 5. Gagal: stop; selama versi skema > V_BEFORE → `<new> migrate down` (binary baru yang memuat SQL down); bila down
    gagal atau status tak terbaca → pulihkan DB dari backup pre-update (app.db/-wal/-shm lama → *.failed-<ts>);
    binary gagal → bin/filemanager.failed; .prev → bin/filemanager; start; /readyz → "ROLLBACK OK in Ns", exit 1;
    tetap tidak siap → journalctl 50 baris, "ROLLBACK FAILED", exit 2.
```

Release dari CI (job `build`): `filemanager-linux-amd64.tar.gz` berisi `bin/filemanager`, `deploy/`,
`docs/DEPLOY-AAPANEL.md` + `.sha256`. Test: `deploy/test/update_rollback_test.sh` (binary palsu yang tidak pernah
siap → rollback 32 s dengan timeout 30 s; migrasi palsu di-`down`; `down` gagal → restore backup; tarball dengan
checksum salah ditolak sebelum perubahan apa pun). `--dry-run` hanya mencetak rencana.

### 12.6 Backup dan restore

- **Storage** (`/www/storage/FileManager`): backup lewat aaPanel (Cron → Backup directory) atau rsync/restic ke lokasi lain, harian; **kecualikan** `.system/tmp`, `.system/uploads`, `.system/thumbs`. Catatan: aaPanel menjalankan backup sebagai root; folder 0750 milik `filemanager` tetap terbaca root.
- **Database**: job `backup_db` 02:30 harian → `VACUUM INTO '.system/backups/app-<yyyyMMdd-HHmmss>[-label].db'` (waktu `TZ_DEFAULT`), keep 7 yang tanpa label (+ label manual tidak dihapus); CLI `filemanager backup-db [--label L] [--env-file F]` untuk `update.sh` (Step 19b); ikut dalam backup storage karena berada di `.system`.
- **Restore**: hentikan service; pulihkan `/www/storage/FileManager` (atau subset user); salin backup DB ke `.system/db/app.db` (hapus `-wal`/`-shm` lama); `chown -R filemanager`; start; jalankan `recalc-quota` + `integrity-check` dari admin.
- **`.env`** dibackup terpisah (berisi rahasia) ke tempat terenkripsi; tanpa `APP_SECRET` yang sama, sesi/link signed/`token_enc` share tidak bisa dibaca (share tetap berfungsi lewat `token_hash`, hanya tombol "Salin link" pemilik hilang).

### 12.7 Troubleshooting

| Gejala | Penyebab umum | Tindakan |
| --- | --- | --- |
| 502 Bad Gateway | service mati / `LISTEN_ADDR` beda / crash saat start (config invalid) | `systemctl status filemanager`, `journalctl -u filemanager -n 100`; `bin/filemanager check-deps`; cek `.env` |
| 413 saat upload | `client_max_body_size` Nginx < chunk | snippet 12.4 (64m) belum ditempel atau tertimpa aaPanel; cek `nginx -T \| grep client_max_body_size` |
| Upload lambat / RAM Nginx naik | `proxy_request_buffering on` | pastikan `off` di blok proxy |
| SSE putus / progress tidak muncul / baru muncul saat selesai | `proxy_buffering on` atau timeout 60 s default | snippet 12.4; aplikasi juga mengirim `X-Accel-Buffering: no`; cek `proxy_read_timeout` |
| Download besar terputus di 60 s | `proxy_read_timeout`/`send_timeout` default | snippet |
| Perpanjangan SSL gagal | `.well-known` di-proxy ke aplikasi (aplikasi 404) | pastikan include Lua aaPanel ada sebelum blok proxy dan folder `public/.well-known` dapat dibaca `www` (0755) |
| Upload resume gagal setelah reload | fingerprint/`localStorage` dihapus atau `upload_sessions` expired (24 h) | ulang upload; jika sering, naikkan retensi di `clean_uploads` (konstanta) |
| 403 `SIGNATURE_EXPIRED` di video | TTL media habis saat jeda lama | player meminta URL baru otomatis; jika tetap, cek jam server (NTP) |
| Thumbnail tidak muncul | vips/ffmpeg tidak terpasang atau `THUMB_WORKERS` antre panjang | dashboard → status dependency; `journalctl` `thumbs` error; `clear-thumbs` |
| `cross-filesystem data dir` warning | `DATA_DIR` di disk lain | pindahkan `.system` ke `STORAGE_ROOT` atau terima copy+delete |
| Login admin pertama gagal | `ADMIN_INITIAL_PASSWORD` di `.env` berbeda dari yang dipakai seed (sudah diubah setelah seed) | `bin/filemanager admin reset-password <username>` (membaca password baru dari stdin, set must_change) |
| `QUOTA_EXCEEDED reason:disk` | disk < 512 MB bebas | bersihkan; dashboard menampilkan; pertimbangkan volume terpisah untuk `/www/storage` |
| OOM saat `make build` | RAM 1,9 GB | tambah swap 2 GB atau build di CI (`--binary`) |

---

## §13 ADR

Format: Konteks → Keputusan → Konsekuensi. Nomor sesuai OUTLINE bersama; dirujuk dari PRD/PROMPT.

### ADR-01 Backend Go ≥ 1.26 + `net/http` + chi, CGO_ENABLED=0
**Konteks.** Butuh streaming file besar, HTTP Range, concurrency task, binary tunggal, memori kecil, dan akses filesystem yang tahan traversal/symlink. Server hanya 1,9 GB RAM; apt menyediakan Go 1.22. **Keputusan.** Go ≥ 1.26 pada patch terbaru (`go.mod` `go 1.26.0`, dipaksa oleh dependensi inti yang mendeklarasikan 1.26 — diputuskan Step 4; 1.26.x juga memuat perbaikan CVE-2026-32282 dan CVE-2026-39822 pada `os.Root`; `os.Root` dengan `Rename/RemoveAll/MkdirAll/Symlink` ada sejak 1.25), dipasang dari tarball go.dev; router `chi` (ringan, middleware idiomatik, `chi.Walk` untuk audit route); module `kppbwi.app/filemanager`; `CGO_ENABLED=0`. **Konsekuensi.** Binary statis mudah di-deploy/rollback; tidak ada cgo → SQLite pure-Go dan libvips via CLI (ADR-02, ADR-09); `install.sh` mengelola Go sendiri.

### ADR-02 SQLite WAL via modernc.org/sqlite + goose + sqlc
**Konteks.** Single-server, tanpa service DB terpisah; kebutuhan query type-safe dan migrasi. **Keputusan.** `modernc.org/sqlite` (pure Go) dengan WAL, `busy_timeout`, satu writer; `goose` (SQL migrations embedded), `sqlc` (`emit_interface` untuk mock); repository interface per domain. Backup `VACUUM INTO`. **Konsekuensi.** Sedikit lebih lambat dari cgo driver (diterima pada skala ini); PostgreSQL nanti = implementasi repository + migrasi dialek (sqlc mendukung); tidak ada `LISTEN/NOTIFY` → SSE in-process cukup (single node).

### ADR-03 Upload tus (tusd library + tus-js-client)
**Konteks.** File hingga 20 GB, jaringan tidak stabil, resume setelah reload. **Keputusan.** `tusd` v2 sebagai library (`UnroutedHandler` + `filestore`) di `/api/v1/upload/tus/`, hook pre-create (auth/kuota/nama) dan pre-finish (verifikasi + rename atomik); client `tus-js-client` dengan fingerprint. **Konsekuensi.** Nginx wajib `proxy_request_buffering off` dan `client_max_body_size ≥ chunk`; folder `uploads` butuh pembersihan; mendapat pause/resume/retry gratis; protokol standar (bisa dipakai klien lain nanti).

### ADR-04 SSE untuk realtime
**Konteks.** Progress task, notifikasi, perubahan folder — satu arah server→client. **Keputusan.** SSE `/api/v1/events` dengan `Last-Event-ID` + ring buffer replay, heartbeat 25 s, `X-Accel-Buffering: no`. **Konsekuensi.** Lebih sederhana dari WebSocket (tanpa upgrade, kompatibel proxy), tetapi butuh `proxy_buffering off`; batas koneksi per browser (HTTP/2 di aaPanel mengatasi limit 6 koneksi HTTP/1.1); aksi client→server tetap REST.

### ADR-05 Editor: Monaco (bukan CodeMirror 6)
**Konteks.** Parity dengan aaPanel IDE (multi-tab, find/replace regex, go to line, 30+ bahasa, tema), riwayat versi dengan diff side-by-side, multi-cursor, format document. **Perbandingan.** CodeMirror 6: ~300 KB, modular, sangat baik di mobile, aksesibilitas bagus; tetapi diff view (`@codemirror/merge`), find/replace regex, multi-cursor, format, dan grammar bahasa harus dirakit dari paket terpisah; beberapa bahasa (nginx, apache, blade) tetap perlu grammar kustom. Monaco: ~2–3 MB (lazy), mesin VS Code: diff editor, multi-cursor, find/replace, folding, command palette, format JSON/CSS/HTML/TS bawaan, Monarch untuk bahasa kustom; kelemahan: bundle besar, dukungan mobile/touch lemah, perlu konfigurasi worker (CSP). **Keputusan.** Monaco, dimuat lazy hanya di route editor/diff, worker self-hosted, tanpa CDN. **Konsekuensi.** Budget bundle awal tetap < 300 KB karena editor terpisah; editing di ponsel terbatas (viewer teks tetap read-only nyaman); versi `monaco-editor` dipilih saat implementasi (verifikasi di registry).

### ADR-06 Origin konten: origin sama + attachment + CSP sandbox (bukan origin terpisah di v1)
**Konteks.** Konten user (HTML/SVG/JS) yang disajikan dari origin aplikasi bisa menjadi XSS dengan akses cookie sesi. Dua opsi: (a) origin terpisah `usercontent.file.kppbwi.app` (praktik Google/GitHub): isolasi total, tetapi butuh DNS, site aaPanel kedua, sertifikat kedua, signed URL lintas origin, CORS untuk pdf.js/fetch; (b) origin sama dengan aturan ketat: tipe berbahaya selalu `attachment` + `application/octet-stream`, `nosniff`, CSP `sandbox` pada raw, hanya raster/audio/video inline, viewer merender teks/Markdown/CSV/JSON sendiri dengan sanitasi, preview HTML live ditiadakan. **Keputusan.** (b) untuk v1, dengan `CONTENT_BASE_URL` disiapkan sehingga (a) bisa diaktifkan tanpa perubahan kode selain konfigurasi (semua URL konten dibangun lewat satu helper; signed URL tidak bergantung cookie). **Konsekuensi.** Deployment satu site; tidak ada preview HTML live di v1 (ditulis sebagai non-goal sementara); residual risk: bug di kebijakan MIME → mitigasi test §10.6 butir 12–13 dan deteksi dari magic bytes bukan ekstensi.

### ADR-07 Video player: Video.js 10 (`@videojs/react`), fallback media-chrome — bukan Vidstack/Plyr
**Konteks.** Brief meminta memilih Vidstack atau Plyr. Verifikasi registry npm pada 14 Okt 2026: `@vidstack/react` (latest 1.15.7) membawa notice *deprecated* — "security fixes only until January 2028"; `plyr` (latest 3.8.5) juga *deprecated* — "receives security updates only. Use Video.js 10 for new projects". `video.js` latest 10.1.0; roadmap resminya menyebut v10 "stable and recommended for new projects", punya paket React resmi `@videojs/react` (dokumen instalasi resmi) dan panduan migrasi dari Vidstack. Alternatif lain: `media-chrome` (Mux, MIT, web components, latest 4.x) — hanya kontrol UI di atas `<video>` native. **Keputusan.** Video.js 10 via `@videojs/react` (lisensi paket 10.x: verifikasi, diharapkan Apache-2.0) di balik komponen pembungkus `VideoPlayer` milik aplikasi; jika saat Step 17b `@videojs/react` ternyata belum stabil (dist-tag, isu kritis terbuka) → media-chrome dengan pembungkus yang sama. Memakai library yang sudah deprecated hanya demi mematuhi brief dinilai keputusan buruk (tanpa perbaikan bug sampai 2028); deviasi ini dicatat di PRD A25. **Konsekuensi.** Lazy-load chunk player; playlist, resume posisi, penyegaran signed URL, dan deteksi codec diimplementasikan di pembungkus, bukan bergantung library; subtitle VTT, PiP, keyboard memakai fitur engine; verifikasi ulang status paket wajib di Step 17b. **Verifikasi Step 17b (21 Okt 2026, `pnpm view`):** `@videojs/react` latest 10.1.0 (rilis 13 Okt 2026; 10.0.0 1 Okt 2026), lisensi Apache-2.0, tanpa notice deprecated, peer dep `react ^18 || ^19` (adapter streaming opsional); `video.js` latest 10.1.0; `@vidstack/react` 1.15.7 dan `plyr` 3.8.5 masih deprecated → Video.js 10 dipakai (media-chrome tidak diperlukan). Dipakai sebagai store + komponen primitif (`VideoPlayer`, `Video`, `Container`, `Controls`, `TimeSlider`) dengan kontrol milik aplikasi; skin bawaan tidak diimpor. Chunk lazy `VideoPlayer-*.js` ±29 KB gzip, hanya dimuat saat video dibuka (E2E). **Catatan Step 23d (29 Okt 2026):** pemutaran hasil konversi HLS memakai `<Video>` + kontrol yang sama dengan sumber HLS: native di Safari/iOS, selain itu **hls.js** (`hls.js` ^1.7.3, Apache-2.0, versi registry 28 Okt 2026, rilis 11 Sep 2026; build `hls.js/light` tanpa subtitle/alt-audio/EME, chunk lazy ±110 KB gzip). Adapter Video.js 10 `@videojs/hlsjs-video`/`native-hls-video` (peer dependency `@videojs/react`) tidak dipakai: itu menambah paket lain, sedangkan kita perlu mengendalikan `xhrSetup` (tanda tangan folder terbaru di setiap permintaan) dan pemuatan lazy sendiri.

### ADR-08 Password arsip tidak lewat argv bila mungkin
**Konteks.** `7zz -p<password>`, `unrar -p<password>`, dan `rar -p<password>` menampakkan password di `ps`/`/proc/<pid>/cmdline` bagi user Linux lain di server yang sama (`/proc` di-mount tanpa `hidepid`). Verifikasi 14 Okt 2026: README `mholt/archives` menyatakan dapat **membaca** 7z dan RAR ber-password (tanpa proses eksternal) dan tidak menyebut zip terenkripsi; `yeka/zip` (fork `archive/zip`, MIT) mendukung ZipCrypto + AES-128/192/256 baca/tulis tetapi tanpa rilis bertag — maintenance perlu verifikasi. Opsi untuk kasus yang tersisa (membuat 7z ber-password): (1) `hidepid=2` pada `/proc` — menyembunyikan proses user lain, tetapi mengubah sistem di luar aplikasi dan dapat mengganggu pemantauan proses aaPanel (perlu verifikasi); (2) password via stdin untuk `7zz` (`-p` tanpa nilai; dukungan pipe non-tty perlu verifikasi lewat spike di Step 14a); (3) argv dengan risiko tercatat. **Keputusan.** (a) Zip ber-password: baca & tulis lewat library Go `internal/archive/zipaes` (kandidat `yeka/zip`; jika tidak layak, implementasi WinZip AES (PBKDF2-HMAC-SHA1, AES-CTR, HMAC-SHA1) + ZipCrypto decrypt dengan test vector terhadap `7zz`). (b) Membaca/extract/listing 7z dan rar ber-password: `mholt/archives` — password tidak pernah keluar proses; `unrar`/`7zz x` hanya fallback bila library gagal pada arsip tertentu (risiko argv berlaku). (c) Membuat 7z ber-password: `7zz a` — spike stdin dulu; jika tidak didukung → argv, risiko dicatat di `SECURITY.md`, dialog compress menampilkan catatan singkat bahwa password 7z dapat terlihat proses lain di server, dan `hidepid=2` direkomendasikan sebagai hardening opsional di `DEPLOY-AAPANEL.md`; pemilik dapat memilih menunda fitur ini (PRD Q6). Keputusan pemilik (14 Okt 2026): tidak ada lisensi `rar`, `unrar` tidak dipasang, dan risiko argv untuk pembuatan 7z ber-password diterima bila stdin tidak didukung. **Konsekuensi.** Permukaan argv menyusut ke satu kasus (membuat 7z ber-password); semua CLI lewat `internal/exec` (argv, `--`, timeout, nice/ionice, stdin opsional, tanpa shell); `hidepid=2` tidak dipasang otomatis. **Hasil spike Step 14a (14 Okt 2026, 7-Zip 23.01 `/usr/lib/7zip/7z`):** `printf 'pw\n' | 7z x -y -o<dir> -- enc.7z` → `Enter password (will not be echoed):` + `Everything is Ok` (exit 0); password salah → exit 2 `Cannot open encrypted archive. Wrong password?`; stdin `/dev/null` → exit 255; `printf 'pw\n' | 7z a -t7z -mhe=on -p -- out.7z in` membuat arsip terenkripsi yang hanya terbuka dengan `pw`. Jadi opsi (2) berlaku: tidak ada kasus argv tersisa, `hidepid=2` tidak diperlukan untuk fitur ini.

### ADR-09 Thumbnail via CLI (vipsthumbnail, ffmpeg, pdftoppm), bukan govips
**Konteks.** govips butuh cgo + libvips dev headers; server kecil. **Keputusan.** CLI dengan argv, timeout, `nice`, worker pool `max(1, CPU−1)`, cache WebP, antrian prioritas viewport. **Konsekuensi.** Overhead spawn per thumbnail (~20–50 ms) diterima; tanpa cgo; dependency opsional (fitur degradasi anggun dengan `DEPENDENCY_MISSING`); di 1 vCPU thumbnail folder besar lambat → prioritas viewport & placeholder.

### ADR-10 Media metadata via ffprobe + decoder Go + parser EXIF sendiri
**Konteks.** Kolom dimensi/durasi, info viewer, tag audio. **Keputusan.** `ffprobe -print_format json -show_format -show_streams` untuk audio/video; `image.DecodeConfig` (+ `x/image/webp`, `vipsheader` untuk tiff/heic/avif) untuk gambar; cache `media_metadata` keyed size+mtime. **EXIF (diputuskan Step 17a, 21 Okt 2026):** parser kecil milik sendiri `internal/media/exif.go` (±600 baris, ~15 tag IFD0/Exif/GPS, pembacaan kontainer JPEG APP1 / PNG `eXIf` / WebP `EXIF` / TIFF / HEIF-AVIF `iinf`+`iloc` dengan batas baca, uji mutasi 3.000 varian tanpa panic), **bukan** library: `dsoprea/go-exif/v3` v3.0.1 rilis terakhir Okt 2022 dan menarik `go-logging`/`go-utility`; `rwcarlsen/goexif` tanpa rilis sejak 2019; `evanoberholster/imagemeta` v1.1.0 (Sep 2026) terawat tetapi menarik msgp/fwd/pkg-errors/nfnt-resize/gosax/cpuid untuk kebutuhan yang jauh lebih kecil dari subset kita (diverifikasi `go list -m` 2026-10-21). Input EXIF adalah data tak tepercaya dari upload, sehingga parser dengan bounds check eksplisit dan tanpa rekursi lebih mudah diaudit daripada dependensi besar. **Konsekuensi.** Satu tool untuk audio/video (ffprobe) mengurangi dependency; tidak ada dependensi EXIF pihak ketiga; tag di luar subset (MakerNote, XMP, IPTC) tidak dibaca; GPS disimpan tetapi disembunyikan default.

### ADR-11 Sort server-side + natural collation + cursor opaque
**Konteks.** Folder 10k item, paging, sort benar lintas halaman. **Keputusan.** Listing penuh di server (ReadDir+Lstat), sort dengan komparator natural kustom (run digit numerik) + `x/text/collate` (`IgnoreCase`, locale user) — bukan `collate.Numeric` karena bug golang/go#67997 (§8.3), cache 15 s per (user,path,sort) dengan invalidasi mutasi, cursor berisi kunci sort item terakhir (stabil). **Konsekuensi.** Memori cache dibatasi 64 MB LRU; perubahan eksternal terlihat ≤ 15 s; client tidak perlu sort sendiri.

### ADR-12 Search: walk nama + ripgrep isi + fallback Go
**Konteks.** Pencarian nama & isi dengan regex, cepat, aman dari ReDoS. **Keputusan.** Nama: walk `os.Root.FS()` dengan batas 5.000/20 s; isi: `rg --json` (Rust regex, tanpa backtracking) dengan timeout, `--no-follow`, `--no-ignore --hidden`; fallback worker Go `regexp` (RE2). Replace = task dengan preview + versi pre-image. **Konsekuensi.** Tanpa indeks (FTS) di v1 → pencarian global berulang memakan I/O; kandidat peningkatan: indeks nama SQLite FTS5 (post-v1).

### ADR-13 Sesi server-side + CSRF token
**Konteks.** Sesi harus dapat dicabut (admin, ganti password); cookie `SameSite=Lax` saja tidak cukup untuk semua browser lama. **Keputusan.** Sesi di DB (hash token), cookie `HttpOnly Secure SameSite=Lax`, idle/absolut timeout; CSRF = HMAC(csrf_secret, session_id) di header + cek `Sec-Fetch-Site`/`Origin`. **Konsekuensi.** Satu query DB per request (cache user 5 s); SPA harus mengambil token setelah login; tus/multipart ikut membawa header.

### ADR-14 argon2id + daftar password umum + lockout bertahap
**Konteks.** Tanpa 2FA wajib; server kecil. **Keputusan.** argon2id 64 MiB/t=3/p=1 (login), 19 MiB/t=2/p=1 (share); min 12 karakter; 100k daftar umum; lockout 1/5/15/60 menit; rate limit IP. **Konsekuensi.** Verifikasi ±100–200 ms di 1 vCPU; aman dengan rate limit; parameter bisa dinaikkan via rehash saat login.

### ADR-15 Signed URL HMAC dengan TTL berbeda per tujuan
**Konteks.** `<video>`, `<img>`, download manager, dan tab baru tidak selalu membawa cookie/CSRF; Range request berulang sepanjang playback. **Keputusan.** HMAC-SHA256 (kunci HKDF) atas scope|path|purpose|exp; TTL 5 menit untuk download/thumb, 2 jam untuk media; player menyegarkan URL saat 403. **Konsekuensi.** Link download yang bocor hanya hidup 5 menit; pencabutan sesi tidak membatalkan URL yang sedang hidup (diterima); pindah ke origin konten terpisah tidak mengubah mekanisme. Step 23d menambah purpose `hls` (satu tanda tangan per folder konversi, path = id task, TTL media; diterbitkan hanya oleh `POST /files/hls`).

### ADR-16 Share: token hash + slug + cookie HMAC; share ke user via SubRoot
**Konteks.** Link publik harus tidak bisa ditebak dan tidak bocor lewat DB dump; penerima terdaftar harus terkurung di subtree. **Keputusan.** Token 256-bit, DB simpan SHA-256 (+ `token_enc`: token terenkripsi AES-256-GCM dengan kunci HKDF dari `APP_SECRET` agar tombol "Salin link" di "Share saya" bekerja — **deviasi dari brief** yang meminta "hanya hash": brief sekaligus meminta aksi "Salin link" yang mustahil tanpa token; alternatif tanpa deviasi = link ditampilkan sekali + `regenerate-token`, dapat dipilih pemilik lewat PRD A27), slug opsional, password argon2id, cookie HMAC terikat `password_hash`; share ke user → `Jail.SubRoot` pada root pemilik, permission viewer/editor dicek per request, kuota pemilik. **Konsekuensi.** DB dump tidak membocorkan link aktif (kecuali `APP_SECRET` ikut bocor); penerima tidak pernah melihat path di atas item; semua operasi penerima adalah operasi pemilik secara filesystem (aktivitas dicatat ganda).

### ADR-17 Recycle Bin via rename ke `.system/trash`; versi = pre-image; keduanya dihitung ke kuota
**Konteks.** Delete harus cepat dan dapat dibatalkan; versi editor harus bisa diff/restore; kuota harus jujur terhadap disk. **Keputusan.** Trash = `rename` atomik ke `trash/<uid>/<id>` + baris DB (fallback copy lintas FS); versi = snapshot isi **sebelum** ditimpa (`rename` file lama → blob versi, tulis baru), dilewati jika hash sama, maks `FILE_VERSION_LIMIT`; `used + trash + versions` dibandingkan dengan kuota, ditampilkan terpisah. **Konsekuensi.** Mengosongkan Recycle Bin/versi membebaskan kuota; restore memulihkan metadata (tidak dihapus saat trash); perubahan via SSH tidak membuat versi (tercatat sebagai `external_overwrite` hanya jika editor mendeteksi konflik).

### ADR-18 Task engine in-process dengan persistensi SQLite
**Konteks.** Operasi berat harus asinkron, bisa dibatalkan, bertahan restart (minimal ditandai), dan dibatasi per user di server 1 vCPU. **Keputusan.** Goroutine scheduler tunggal, tabel `tasks/task_logs`, semaphore per user (3) & global (2 di server ini), `context` cancel, state `interrupted` saat shutdown, retry membuat task baru. **Konsekuensi.** Tidak ada antrian eksternal (Redis) — cukup single node; task tidak dilanjutkan otomatis setelah restart (user menekan retry); task yang butuh password meminta ulang.

### ADR-19 Lock path in-process sadar-ancestor
**Konteks.** Dua operasi pada subtree yang sama (extract ke folder yang sedang dipindah) dapat merusak data. **Keputusan.** Lock manager per user, kunci path dengan semantik ancestor/descendant, akuisisi terurut, `TryAcquire` untuk request sinkron (gagal cepat `OPERATION_IN_PROGRESS`), `Acquire` dengan timeout untuk task. **Konsekuensi.** Single-process saja (cukup); listing tidak dikunci (bisa melihat state transisi, diterima); perubahan via SSH tidak terkunci (di luar kendali).

### ADR-20 Deploy: binary + systemd hardening; aaPanel Reverse Proxy project; Docker opsional
**Konteks.** Server dikelola aaPanel dengan Nginx-nya; prompt meminta domain lewat UI aaPanel, user khusus non-`www`, port hanya loopback. Verifikasi: aaPanel 8.0.7 di server ini memiliki tipe proyek Reverse Proxy yang bekerja. **Keputusan.** Unit systemd `User=filemanager`, `ProtectSystem=strict`, `ReadWritePaths` storage, `NoNewPrivileges`, dll.; site aaPanel tipe Reverse Proxy → `127.0.0.1:8080` (alternatif PHP Static + fitur reverse proxy); snippet Nginx lewat editor Config UI; Docker hanya sebagai opsi dev/alternatif. **Konsekuensi.** Tidak bentrok dengan Nginx aaPanel; konfigurasi kustom harus diverifikasi bertahan setelah aaPanel menulis ulang vhost (didokumentasikan); storage 0750 milik `filemanager` tidak terbaca situs PHP (`www`).

### ADR-21 Frontend: React + TS + Vite + TanStack (Router/Query/Virtual) + Zustand + Tailwind/shadcn
**Konteks.** UI padat, tabel virtual 10k baris, komponen accessible yang bisa dikustom, i18n. **Keputusan.** Stack sesuai prompt, kecuali TanStack Table yang dihapus di Step 7b (lihat §8.4: row model 10k dibangun ulang per halaman; kolom didefinisikan sendiri); lucide-react (ISC) untuk ikon; sonner; cmdk; react-hook-form + zod; i18next; `marked` + DOMPurify; pdfjs-dist; font self-host; pnpm via corepack; tanpa CDN/Google Fonts (CSP `'self'`). **Konsekuensi.** Bundle awal < 300 KB gzip dengan code-splitting per route dan lazy Monaco/pdf.js/Video.js; `shadcn/ui` di-vendor ke repo (bukan dependency) sehingga bisa diubah ke token.

### ADR-22 Build & CI: embed frontend, build di CI, GitHub Actions (asumsi)
**Konteks.** RAM server 1,9 GB (+ swap 2,8 GB); belum ada remote git (keputusan pemilik 14 Okt 2026: repo lokal, build di server). **Keputusan.** `make build` menghasilkan satu binary dengan `embed.FS`; CI (GitHub Actions diasumsikan; workflow ditulis agar mudah dipindah ke GitLab CI/Gitea Actions) menjalankan lint/test/vuln/e2e dan merilis `tar.gz`; `install.sh --binary` dan `update.sh --release` memasang hasil CI; build di server tetap bisa dengan swap ≥ 2 GB. **Konsekuensi.** Rollback = ganti binary; pipeline butuh apt tool untuk test CLI; jika hosting bukan GitHub, Step 1 menyesuaikan file workflow.

### ADR-23 Kuota inkremental + rekalkulasi terjadwal; kapasitas disk nyata di dashboard
**Konteks.** Walk seluruh storage mahal; drift bisa terjadi (SSH, crash). **Keputusan.** `quota_usage` diperbarui dalam transaksi tiap mutasi; rekalkulasi malam hari + saat `dirty` + aksi admin; semua writer memeriksa `statfs` free ≥ 512 MB; dashboard menampilkan Σ kuota vs disk (server sekarang: sisa ±6 GB, `DEFAULT_USER_QUOTA=10GB` tidak realistis untuk banyak user → peringatan). **Konsekuensi.** Kuota akurat dalam ±1 % di antara rekalkulasi; `install.sh` memperingatkan kuota default terhadap ruang bebas.
