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
- Versi library tidak ditulis; selalu "versi stable terbaru saat implementasi, verifikasi di registry". Versi minimum hanya: Go ≥ 1.25 pada patch terbaru — minimal **1.25.12** (CVE-2026-32282 `Root.Chmod` mengikuti symlink, diperbaiki 1.25.9; CVE-2026-39822 open mengikuti symlink pada komponen terakhir + trailing slash, diperbaiki 1.25.12; 1.26.x terbaru juga boleh) — butuh `os.Root.Rename/RemoveAll/MkdirAll/Symlink` (12 method baru 1.25, diverifikasi di go.dev/doc/go1.25), Node ≥ 22 (Node 24 terpasang di server), Ubuntu 22.04/24.04.

Fakta server yang sudah diverifikasi pada 14 Oktober 2026 (boleh dipakai tanpa tanda verifikasi): Ubuntu 24.04.5, kernel 6.8, **1 vCPU, 1,9 GB RAM + swap 2,8 GB; disk sistem 19 GB (sisa ±6 GB) untuk OS/aaPanel/`/www/wwwroot`; `/www/storage` = disk terpisah `/dev/vdb1` ext4 492 GB**; Go 1.27.2 dipasang 14 Okt 2026 di `/usr/local/go` (apt hanya 1.22), pnpm 12.11.2 via corepack; Node 24.21, npm 11.19, corepack 0.36; ffmpeg/ffprobe/vipsthumbnail/pdftoppm/7zz/rg/unrar **semua belum ada** (koreksi Step 1: `rg` yang terdeteksi sebelumnya ternyata fungsi shell dari lingkungan Claude Code, bukan `/usr/bin/rg`; tersedia di apt: `ffmpeg`, `libvips-tools`, `poppler-utils`, `ripgrep`, `7zip` → binary `7zz`, `unrar` multiverse); aaPanel **8.0.7** di `/www/server/panel` (port <PANEL_PORT>), Nginx aaPanel 1.30.5 dengan Lua; tipe proyek **Reverse Proxy** ada dan terbukti bekerja (site `proxy.kppbwi.app`); site `file.kppbwi.app` belum ada; `/www/storage/FileManager` ada, kosong, owner `www:www`; user Linux `filemanager` belum ada; `/proc` tanpa `hidepid`; UFW aktif, 8080 tertutup.

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
| Build | `pnpm build` (Vite + Monaco) dan `go build` bisa butuh > 1,5 GB RAM. `install.sh` memeriksa swap; jika < 2 GB, mencetak instruksi membuat swapfile 2 GB (tidak membuatnya otomatis). Rekomendasi utama: build di CI, unggah binary (ADR-22). |
| Disk | `/www/storage` adalah disk terpisah 492 GB, jadi `DEFAULT_USER_QUOTA=10GB` realistis; disk sistem 19 GB hanya menampung `APP_DIR`, cache build, dan log. Admin dashboard menampilkan kapasitas nyata (`statfs`); `install.sh` memperingatkan jika `DEFAULT_USER_QUOTA` > 50 % ruang bebas. Semua operasi yang menulis mengecek `statfs` dan gagal dengan `QUOTA_EXCEEDED` (details `reason:"disk"`) bila ruang bebas < 512 MB. |
| SQLite | `PRAGMA journal_mode=WAL; synchronous=NORMAL; busy_timeout=5000; cache_size=-16000` (16 MB). Satu koneksi penulis (`db.SetMaxOpenConns(1)` untuk pool tulis) + pool baca terpisah. |
| Transcoding | Di luar scope v1 (CPU). |

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
│   │   ├── tasks/                # engine, registry tipe task, scheduler, cleanup jobs
│   │   ├── audit/                # audit_logs + activity_logs + retensi
│   │   ├── notify/               # notifications
│   │   ├── trash/                # recycle bin
│   │   ├── versions/             # file_versions pre-image
│   │   ├── quota/                # quota_usage inkremental + rekalkulasi
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
| `APP_SECRET` | hex | — (wajib) | tepat 64 karakter hex (32 byte) | tidak | Kunci induk; turunan via HKDF-SHA256 dengan info `session`, `csrf`, `signed-url`, `share-cookie`, `totp-enc`. Rotasi = semua sesi/link signed tidak valid (didokumentasikan). |
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
| `FFMPEG_PATH` | path | `/usr/bin/ffmpeg` | file executable; jika tidak ada → fitur video thumb/cover/srt nonaktif dengan `DEPENDENCY_MISSING` | tidak | Status tampil di admin dashboard. |
| `FFPROBE_PATH` | path | `/usr/bin/ffprobe` | idem | tidak | |
| `VIPS_PATH` | path | `/usr/bin/vipsthumbnail` | idem; tanpa vips → thumbnail gambar memakai decoder Go (jpg/png/gif/webp) sebagai fallback lambat | tidak | |
| `PDFTOPPM_PATH` | path | `/usr/bin/pdftoppm` | idem; tanpa → thumbnail PDF nonaktif | tidak | |
| `SEVENZIP_PATH` | path | `/usr/bin/7zz` | idem; tanpa → 7z & zip AES tulis via 7zz nonaktif (zip AES tetap via library Go) | tidak | Ubuntu 24.04 paket `7zip` → `/usr/bin/7zz`. |
| `RAR_PATH` | path | kosong | kosong = compress rar nonaktif | tidak | Binary berlisensi, opsional. |
| `UNRAR_PATH` | path | kosong | kosong = extract rar via mholt/archives (README-nya menyatakan membaca RAR ber-password — diverifikasi 14 Okt 2026; rar5 dicek dengan fixture Step 14a); isi hanya sebagai fallback | tidak | |
| `RG_PATH` | path | `/usr/bin/rg` | tanpa → fallback worker Go | tidak | Belum terpasang di server (dipasang `install.sh` via paket `ripgrep`). |
| `LOG_LEVEL` | enum | `info` | `debug` \| `info` \| `warn` \| `error` | tidak | JSON slog ke stdout (journald). |
| `METRICS_ADDR` | host:port | kosong | kosong = nonaktif; harus loopback | tidak | Prometheus `/metrics` di listener terpisah. |
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
| `APP_NAME` | string | `FileManager` | ≤ 40 karakter | ya | Judul & logo teks. Logo gambar: upload admin → `DATA_DIR/branding/logo.webp`. |
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
| totp_secret_enc | BLOB NULL | AES-256-GCM dengan kunci HKDF `totp-enc` (fase akhir) |
| totp_enabled | INTEGER NOT NULL DEFAULT 0 | |
| created_by | TEXT NULL | user id admin |
| created_at, updated_at | TEXT NOT NULL | |
| deleted_at | TEXT NULL | soft-delete setelah hapus/arsip; username tetap unik (tidak bisa dipakai ulang sampai baris dihapus permanen) |

Index: `users_username_uq (username)`, `users_status_idx (status)`.

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

**settings** — `key TEXT PK, value_json TEXT NOT NULL, updated_at TEXT NOT NULL, updated_by TEXT NULL`. Key = nama env lowercase (§3.3) plus key non-env: `remote_download.allowlist` (array), `remote_download.denylist` (array), `branding.logo_path`, `share.allow_custom_slug`.

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
| exif_json | TEXT NULL | subset: make, model, lens, datetime, exposure, iso, fnumber, focal, orientation, gps (hanya jika pref `exif.show_gps`) |
| tags_json | TEXT NULL | audio: title, artist, album, year, track |
| has_cover | INTEGER NOT NULL DEFAULT 0 | |
| thumb_state | TEXT CHECK IN ('pending','ready','failed','unsupported') | |
| thumb_error | TEXT NULL | |
| computed_at | TEXT NOT NULL | |

Index `(user_id, mime)`. Baris dihapus saat file dihapus/dipindah (prefix update saat move).

**upload_sessions** — `id TEXT PK (tus upload id), user_id TEXT FK, share_id TEXT NULL FK shares, dest_dir TEXT, filename TEXT, size INTEGER, conflict_policy TEXT CHECK IN ('overwrite','skip','rename'), state TEXT CHECK IN ('active','finalized','aborted','expired'), finalized_path TEXT NULL, created_at, updated_at, expires_at TEXT`. Index `(user_id, state)`, `(expires_at)`. Reservasi kuota = Σ size baris `active` per user.

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
func (j *Jail) FS() fs.FS                               // os.Root.FS() untuk ripgrep fallback & pencarian nama
func (j *Jail) Statfs() (free, total uint64, err error) // unix.Statfs pada root

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

`internal/files/lock.go`, satu instance per proses, keyed per user:

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

Semua tabel ber-path (`favorites`, `pins`, `file_notes`, `file_tags`, `file_versions`, `shares`, `folder_view_prefs`, `media_metadata`, `trash_items.original_path` tidak diubah, `activity_logs` tidak diubah) diperbarui dalam **satu transaksi SQL** setelah operasi filesystem berhasil, oleh `meta.RewritePaths(ctx, tx, userID, oldPath, newPath, isDir)`:

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

- Base path `/api/v1`. Request/response JSON UTF-8 (`Content-Type: application/json`), kecuali endpoint streaming (raw/download/zip/thumb/SSE/NDJSON) dan tus.
- Autentikasi: cookie sesi (`fm_session`). Endpoint ditandai **S** = butuh sesi valid; **S+P** = sesi valid dan tidak sedang `must_change_password`; **A** = admin; **Sig** = signed URL (tanpa cookie); **Pub** = publik (dengan rate limit); **Shr** = publik + cookie share jika ber-password.
- Semua non-GET (termasuk tus `POST/PATCH/DELETE`) wajib header `X-CSRF-Token` (§8.1). GET tidak pernah mengubah state.
- `path` di query/body = path virtual. Parameter opsional `share=<share_id>` mengubah scope ke item yang dibagikan ke user (path relatif terhadap item; untuk share file tunggal `path` harus `/`). Admin dengan `ADMIN_CAN_BROWSE_USERS=true` boleh `as_user=<username>`.
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
| `VALIDATION_FAILED` | 400 | Body/query tidak valid; `details.fields: {name: reason}`. |
| `NOT_FOUND` | 404 | Path/ID tidak ada. |
| `METHOD_NOT_ALLOWED` | 405 | Method tidak didukung pada route yang ada (router chi). Ditambahkan Step 1. |
| `ALREADY_EXISTS` | 409 | Nama tujuan sudah ada dan policy tidak diberikan. `details.existing:{size,mtime,isDir}`. |
| `CONFLICT` | 409 | `If-Match` tidak cocok (editor), state task tidak memungkinkan aksi. |
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
| `SSRF_BLOCKED` | 422 | URL remote resolve ke IP terlarang atau skema bukan http(s); `details.host,ip`. |
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
| `POST /auth/login` | Pub | `{username, password, totp?}` | `200 {user, mustChangePassword, csrfToken}` + `Set-Cookie fm_session` | `UNAUTHENTICATED` (pesan generik "username atau password salah"), `ACCOUNT_LOCKED`, `ACCOUNT_DISABLED`, `RATE_LIMITED` |
| `POST /auth/logout` | S | — | `204` + cookie dihapus | |
| `GET /auth/csrf` | S | — | `200 {csrfToken}` | |
| `GET /auth/me` | S | — | `200 {user:{id,username,displayName,role,mustChangePassword,quota:{limit,used,trash,versions},homeProvisioned,language,prefs}, config:{chunkSize,maxUploadSize,editorMaxFileSize,remoteDownloadEnabled,publicSharesEnabled,shareAllowNoExpiry,shareRequirePassword,features:{ffmpeg,vips,sevenzip,rar,unrar,rg,pdftoppm}}}` | |
| `POST /auth/change-password` | S | `{currentPassword, newPassword}` | `204`; semua sesi lain dicabut | `VALIDATION_FAILED` (`details.fields.newPassword: too_short|common|same_as_current|same_as_initial`) |

**Profil (`/api/v1/me`)**

| Method & path | Auth | Request | Response |
| --- | --- | --- | --- |
| `GET /me/preferences` · `PUT /me/preferences` | S+P | `{prefs}` (merge patch, key terdaftar §8.18) | `200 {prefs}` |
| `GET /me/sessions` | S+P | — | `200 {items:[{id (prefix 8 hex), current, ip, deviceLabel, createdAt, lastSeenAt}]}` |
| `DELETE /me/sessions/{id}` · `POST /me/sessions/revoke-others` | S+P | — | `204` |
| `GET /me/folder-views?path=` · `PUT /me/folder-views` · `DELETE /me/folder-views?path=` | S+P | `{path, viewMode, sort…, columns}` | `200` |
| `GET /me/editor-session` · `PUT /me/editor-session` | S+P | `{tabs, activePath}` | `200` |
| `GET /me/activity?cursor&limit&action&from&to` | S+P | — | `200 {items:[{at, action, path, path2, details}]}` |
| `GET /me/recent?limit=100` | S+P | — | `200 {items:[Info + location]}` (hanya yang masih ada) |
| `GET /me/storage-analysis` | S+P | — | `202 {task}` pertama kali / `200 {tree (treemap depth 3), largest:[50], computedAt}` jika cache < 1 jam |
| `POST /me/totp/setup` · `POST /me/totp/enable` · `POST /me/totp/disable` | S+P | fase akhir (Step 23) | |

**Files (`/api/v1/files`)** — semua **S+P**, menerima `share=` dan `as_user=` sesuai §6.1.

| Method & path | Request | Response | Error utama |
| --- | --- | --- | --- |
| `GET /files/list?path&sort&dir&sort2&dir2&cursor&limit&foldersFirst&filter&hidden` | `sort ∈ name|size|type|ext|mtime|btime|atime|mode|note|dimensions|duration` | `200 {items:[Item], nextCursor, total, dir:{path, etag, mtime, itemCount}, parentPath}` | `NOT_FOUND`, `PATH_INVALID` |
| `GET /files/stat?path` | — | `200 {Item + stat:{mode,modeOctal,atime,mtime,ctime,btime,nlink}, counts? (folder: dihitung terpisah), checksums? (cache), versionsCount, shares:[…], note, tag, favorite, pinned}` | |
| `POST /files/mkdir` | `{path}` (path lengkap folder baru), `parents?:bool` | `201 {item}` | `ALREADY_EXISTS`, `NAME_INVALID` |
| `POST /files/create` | `{path, template?: "txt|md|html|css|js|ts|json|php|py|sh|yaml", content?}` | `201 {item}` | idem |
| `POST /files/rename` | `{path, newName}` | `200 {item, undo:{op:"rename", path:newPath, newName:oldName}}` | `ALREADY_EXISTS`, `OPERATION_IN_PROGRESS` |
| `POST /files/batch-rename/preview` | `{paths:[…], rule:{find?, replace?, regex?:bool, caseSensitive?:bool, numbering?:{template:"{n}", start:1, pad:3}, case?: "upper|lower|title|none", extension?: "keep|lower|upper"}}` | `200 {items:[{path, newName, conflict?:bool, invalid?:string}]}` | |
| `POST /files/batch-rename` | sama + `confirm:true` | `200 {results, undo:{op:"batch-rename", pairs}}` | |
| `POST /files/copy` | `{items:[path], dest, policy:{default:"ask|overwrite|skip|rename", perItem?:{path:policy}}}` | `200 {results}` atau `202 {task}` | `ALREADY_EXISTS` (policy ask), `QUOTA_EXCEEDED`, `VALIDATION_FAILED` (folder ke dalam dirinya) |
| `POST /files/move` | idem | idem + `undo:{op:"move", items, dest:oldDir}` | |
| `POST /files/duplicate` | `{path}` → `nama (1).ext` | `201 {item}` | |
| `POST /files/symlink` | `{path (link), target (relatif)}` | `201 {item}` | `SYMLINK_ESCAPE` |
| `POST /files/chmod` | `{paths, mode:"0644", recursive?:bool, dirMode?:"0755"}` | `200` / `202 {task}` jika rekursif | |
| `POST /files/delete` | `{paths}` → Recycle Bin | `200 {results, batchId, undo:{op:"restore", batchId}}` / `202` | |
| `POST /files/delete-permanent` | `{paths, confirm:"HAPUS" jika > 50}` | `202 {task}` | `VALIDATION_FAILED` |
| `POST /files/undo` | `{undo}` (objek dari response sebelumnya, ditandatangani HMAC, umur 60 s) | `200` | `CONFLICT` jika state berubah |
| `GET /files/content?path&encoding?&tail?` | — | `200 {content, encoding, detectedEncoding, bom, eol:"lf|crlf|mixed", language, size, etag, truncated?:bool, totalLines?}`; file > `EDITOR_MAX_FILE_SIZE` tanpa `tail` → `FILE_TOO_LARGE`; `tail=N` → N baris terakhir read-only | `BINARY_FILE` |
| `PUT /files/content` | header `If-Match`; `{path, content, encoding, eol, force?:bool}` | `200 {etag, size, version:{no,id}|null}` | `CONFLICT` (`details.serverEtag, serverMtime`), `FILE_TOO_LARGE`, `QUOTA_EXCEEDED` |
| `POST /files/sign` | `{path, purpose:"download|media|thumb|inline", share?, size? (thumb)}` | `200 {url, expiresAt}` | |
| `GET /files/raw?path&purpose&exp&sig&scope` | **Sig** | stream (Range) — kebijakan inline/attachment §8.9 | `SIGNATURE_*` |
| `GET /files/download?path&exp&sig&scope` | **Sig** | stream `attachment` selalu | |
| `POST /files/zip` | `{paths, name?, method:"store|deflate"}` | `200 {url, expiresAt}` | |
| `GET /files/zip?token` | **Sig** | `application/zip` streaming, `Accept-Ranges: none` | |
| `GET /files/thumb?path&size=128|256|512&exp&sig&scope` | **Sig** | `image/webp` + `ETag` + `Cache-Control: private, max-age=86400`; `202` kosong + `Retry-After: 2` jika belum siap (client retry maks 3×); `404` jika unsupported | |
| `POST /files/dir-size` | `{path}` | `202 {task}` (hasil di `task.result: {files, dirs, bytes}` dan di-cache 10 menit untuk kolom Ukuran) | |
| `POST /files/checksum` | `{path, algos:["md5","sha1","sha256"], verify?:{algo, expected}}` | `202 {task}`; result `{md5, sha1, sha256, verified?}`; file ≤ 100 MB dihitung sinkron `200` | |
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
| `GET /trash?cursor&limit&kind&q&sort` | | `200 {items:[{id, name, originalPath, isDir, size, itemCount, kind, deletedAt, expiresAt}], total, totalBytes}` |
| `POST /trash/restore` | `{ids, dest?: path, policy?}` | `200 {results}` / `202` |
| `POST /trash/purge` | `{ids}` | `202 {task}` |
| `POST /trash/empty` | `{confirm:"HAPUS"}` | `202 {task}` |
| `GET /trash/{id}/download` | | signed URL ke isi item (untuk melihat sebelum restore) |

**Meta (`/api/v1/meta`)** — S+P

| Method & path | Request | Response |
| --- | --- | --- |
| `GET /meta/favorites` · `PUT /meta/favorites` · `DELETE /meta/favorites` | `{paths}` | `200 {items:[Item+broken]}` / `204` |
| `GET /meta/pins?dir` · `PUT /meta/pins` · `DELETE /meta/pins` | `{paths}` | |
| `PUT /meta/notes` · `DELETE /meta/notes` | `{path, note}` | |
| `PUT /meta/tags` · `DELETE /meta/tags` | `{paths, color}` | |
| `GET /meta/tagged?color&cursor` | | `200 {items}` |

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
| `POST /upload/precheck` | S+P (atau **Shr** untuk file request) | `{dest, share?, files:[{name, size, relPath? (upload folder)}]}` → `{ok, conflicts, rejected, quota:{available}}` |
| `POST /upload/tus/` · `HEAD|PATCH|DELETE /upload/tus/{id}` · `OPTIONS` | S+P / Shr | Protokol tus 1.0.0 core + creation + termination + expiration + checksum (sha1, opsional). `Upload-Metadata` base64: `dest`, `name`, `relPath`, `policy`, `share`, `lastModified`. |
| `POST /upload/clipboard` | S+P | multipart ≤ 25 MB untuk paste gambar dari clipboard (PNG) → `201 {item}`; nama `Pasted <yyyy-MM-dd HH.mm.ss>.png` |

**Remote download (`/api/v1/remote-downloads`)** — S+P: `POST {urls:[…], dest, filenames?:{url:name}, policy}` → `202 {tasks:[…]}`; `POST /remote-downloads/probe {url}` → `200 {finalUrl, size?, filename?, acceptsRanges, contentType}` (mengikuti aturan SSRF yang sama, HEAD lalu GET Range 0-0).

**Archives (`/api/v1/archives`)** — S+P

| Method & path | Request | Response |
| --- | --- | --- |
| `POST /archives/compress` | `{paths, dest (file arsip), format:"zip|7z|tar|tar.gz|tar.bz2|tar.xz|tar.zst|gz|bz2|xz|zst|rar", level:"store|fast|normal|max", password?, encryptNames?:bool (7z), splitSize?, deleteSource?:bool}` | `202 {task}` |
| `POST /archives/extract` | `{archive, dest:"here|new|<path>", password?, encoding:"auto|utf-8|gbk|cp437|shift_jis|big5", policy:"overwrite|skip|rename", entries?:[names] (extract sebagian)}` | `202 {task}` |
| `GET /archives/entries?path&inner=&password?&encoding?&cursor` | | `200 {items:[{name, path (dalam arsip), isDir, size, compressedSize, mtime, encrypted}], format, total, supportsModify}` |
| `GET /archives/entry-content?path&entry&password?` | | teks ≤ 1 MB atau gambar ≤ 20 MB untuk preview; lainnya `UNSUPPORTED_TYPE` |
| `GET /archives/entry-download?path&entry&exp&sig` | Sig | stream satu entri |
| `POST /archives/add` | `{archive, paths:[…], into?:"dir/dalam/arsip"}` (zip tanpa password saja) | `202 {task}` |
| `POST /archives/remove-entries` | `{archive, entries}` (zip saja) | `202 {task}` |

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
| `GET /shared-with-me?cursor` | | `200 {groups:[{owner:{username, displayName}, items:[{shareId, name, isDir, permission, note, expiresAt, broken, favorite, pinned}]}]}` |
| `DELETE /shared-with-me/{shareId}` | penerima menyembunyikan/keluar dari share | |

**Publik (`/api/v1/public/shares/{token}`)** — **Pub/Shr**; `{token}` = token atau slug.

| Method & path | Response |
| --- | --- |
| `GET …` | `200 {name, isDir, size, owner? (jika !hideOwner), note, allowDownload, allowUpload, expiresAt, passwordRequired, unlocked}`; `410 SHARE_*` |
| `POST …/unlock {password}` | `204` + `Set-Cookie fm_share_<id>` (`HttpOnly; Secure; SameSite=Lax; Path=/s/<token>; Max-Age=3600`; juga diterima di `/api/v1/public/shares/<token>` lewat `Path` kedua) |
| `GET …/list?path&sort&dir&cursor` | listing read-only (folder) |
| `GET …/stat?path` · `GET …/raw?path&…` · `GET …/download?path` · `POST …/zip {paths}` → `GET …/zip?token` · `GET …/thumb?path&size` | seperti files, tanpa signed URL (otorisasi = token + cookie); rate limit §10.5 |
| `POST …/upload/precheck` · `…/upload/tus/` | hanya jika `allowUpload`; dest = folder share; dihitung ke kuota pemilik; nama konflik → selalu `rename` |
| `GET …/content?path` | preview teks read-only ≤ 1 MB |

Halaman `GET /s/<token>[/sub/path]` (bukan API) dilayani SPA (route `/s/:token/*`) dengan header `X-Robots-Tag: noindex, nofollow`, `Referrer-Policy: no-referrer`, dan meta `robots noindex`.

**Admin (`/api/v1/admin`)** — **A** (dan S+P)

| Method & path | Request | Response |
| --- | --- | --- |
| `GET /admin/dashboard` | | `{users:{active,disabled}, storage:{usedBytes,quotaSumBytes,disk:{total,free}}, topUsers:[{username,used,quota}], tasks:{running,queued}, publicShares, failedLogins24h, version:{version,commit,buildDate,go}, deps:{ffmpeg:{ok,path,version?},…}, warnings:[…]}` |
| `GET /admin/users?cursor&q&role&status&sort` | | `{items:[{id, username, displayName, role, status, quota:{limit,used}, lastLoginAt, homeProvisioned, homeWarning, createdAt, mustChangePassword, totpEnabled}]}` |
| `POST /admin/users` | `{username, displayName, role, quotaBytes?, temporaryPassword?}` | `201 {user, temporaryPassword}` (hanya sekali) |
| `PATCH /admin/users/{id}` | `{displayName?, role?, quotaBytes?, status?}` | `200 {user}`; `VALIDATION_FAILED last_admin` jika melanggar aturan admin terakhir |
| `POST /admin/users/{id}/reset-password` | | `200 {temporaryPassword}`; `must_change_password=1`; semua sesi dicabut |
| `POST /admin/users/{id}/revoke-sessions` | | `204` |
| `DELETE /admin/users/{id}` | `{confirmUsername, mode:"purge|archive"}` | `202 {task}` (`archive` → `.system/deleted-users/<username>-<yyyyMMdd-HHmmss>`) |
| `GET /admin/settings` · `PUT /admin/settings` | `{key: value}` subset yang boleh | `{items:[{key, value, source:"default|env|override", type, validation}]}` |
| `POST /admin/settings/reset` | `{keys}` | kembali ke env |
| `POST /admin/branding/logo` | multipart png/svg(dirasterisasi)/webp ≤ 1 MB | |
| `GET /admin/audit?cursor&actor&action&from&to&q` · `GET /admin/audit/export.csv?…` | | |
| `GET /admin/tasks?state&user&cursor` · `POST /admin/tasks/{id}/cancel` | | |
| `POST /admin/maintenance/{job}` | `job ∈ recalc-quota (body `{userId?}`), clear-thumbs, clean-uploads, purge-trash, integrity-check, backup-db, prune-logs` | `202 {task}` |
| `GET /admin/maintenance/backups` | | `{items:[{name, size, createdAt}]}` |
| `GET /admin/shares?cursor&owner&mode` · `POST /admin/shares/{id}/revoke` | | admin melihat metadata share (path dan nama item, bukan isi) |

**Sistem**

| Path | Auth | Response |
| --- | --- | --- |
| `GET /healthz` | Pub via Nginx (rate limit 60/menit per IP, log level debug) | `200 {"status":"ok"}` tanpa detail lain; dipakai uptime monitor & smoke test deploy |
| `GET /readyz` | Loopback saja (tidak di-proxy Nginx) | `200 {db:ok, storage:ok, migrations:ok}` atau `503` dengan komponen yang gagal |
| `GET /metrics` | listener `METRICS_ADDR` | Prometheus text (request count/duration per route, tasks by state, sse clients, thumb queue, quota bytes per user) |

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

- Endpoint `/api/v1/upload/tus/` memakai `tusd` `UnroutedHandler` (`BasePath` = `/api/v1/upload/tus/`, `RespectForwardedHeaders: true`, `MaxSize` = `MAX_UPLOAD_SIZE`, `DisableDownload: true`, `DisableTermination: false`), store `filestore` di `DATA_DIR/uploads`.
- Middleware sebelum handler: sesi/CSRF (atau share cookie untuk file request). `PreUploadCreateCallback` (tusd v2: `func(HookEvent) (HTTPResponse, FileInfoChanges, error)`, terverifikasi di pkg.go.dev 14 Okt 2026): parse metadata, `fsjail.ValidateName`, `BLOCKED_EXTENSIONS`, reservasi kuota (`upload_sessions` active + size ≤ sisa), simpan baris `upload_sessions` dengan `expires_at = now+24h`, kembalikan `FileInfoChanges` untuk menyetel `ID` menjadi UUIDv7 kita.
- `PreFinishResponseCallback` (`func(HookEvent) (HTTPResponse, error)`): verifikasi `info.Size == info.Offset` dan ukuran file di disk; tentukan nama akhir berdasarkan `policy` (`rename` → `nama (1).ext` dst), `MoveIn` atomik ke `dest` (buat subfolder `relPath` untuk upload folder dengan `MkdirAll`), set mtime dari `lastModified` jika ada, update `quota_usage`, `activity_logs`, invalidasi listing, antre thumbnail, SSE `file.changed`.
- Header yang wajib diteruskan Nginx: `Upload-Offset`, `Upload-Length`, `Upload-Metadata`, `Tus-Resumable`, `Location` (absolut berbasis `APP_BASE_URL`), `X-Forwarded-Proto` (agar `Location` https).
- Client (`tus-js-client`): `chunkSize` = `UPLOAD_CHUNK_SIZE`, `parallelUploads: 1` per file (paralelisme antar file = 3), `retryDelays: [0, 1000, 3000, 5000, 10000]`, `storeFingerprintForResuming: true`, `removeFingerprintOnSuccess: true`, `headers: {X-CSRF-Token}`, `onShouldRetry` → tidak retry pada 4xx selain 423/429.
- Nginx: `client_max_body_size 64m` (> 16 MB chunk + header), `proxy_request_buffering off`, timeout 3600 s (§12.4).
- Upload terbengkalai: job `clean-uploads` tiap jam menghapus `upload_sessions` `active` dengan `expires_at < now` beserta file `.info`/`.bin` di `DATA_DIR/uploads`, dan file yatim di folder itu yang tidak punya baris (umur > 24 h).

### 6.6 Share publik

- Resolusi `{token}`: jika cocok regex slug dan ada `shares.slug` → pakai; selain itu hash SHA-256 → `token_hash`. Lookup konstan-waktu tidak diperlukan (hash acak), tetapi respons untuk "tidak ada" dan "expired" dibedakan hanya setelah lookup berhasil; token yang tidak ada → `NOT_FOUND` generik dengan rate limit per IP.
- Urutan cek: ada? → `disabled_at`/owner disabled → `broken_at` → `expires_at` → `max_downloads` (saat download) → password (cookie valid? jika tidak → `SHARE_PASSWORD_REQUIRED`).
- Cookie share: nilai `base64url(share_id|exp|HMAC(hkdf("share-cookie"), share_id|exp|password_hash_prefix8))` sehingga mengganti password otomatis membatalkan cookie lama.
- Rate limit §10.5: unlock 10/10 menit per (link, IP) dan 30/10 menit per IP; listing/raw/download 120/menit per IP per link.
- Setiap `view` (halaman meta pertama kali per cookie sesi anonim `fm_pv` 24 jam) menaikkan `view_count`; setiap `download`/`zip` menaikkan `download_count` (zip dihitung 1). Log ke `share_access_logs` dengan IP dipotong.
- Halaman publik dilayani SPA; API publik tidak pernah mengembalikan path di atas item yang dibagikan (`path` selalu relatif terhadap item).

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
| `compress` | archives | `{paths, dest, format, level, encryptNames, splitSize, deleteSource}` — password hanya di memori | bytes (sumber) | `{archive, size, entries}` |
| `extract` | archives | `{archive, dest, encoding, policy, entries?}` — password di memori | bytes (uncompressed) | `{dest, entries, bytes, skipped[]}` |
| `archive_modify` | add/remove entries | `{archive, add?, remove?}` | items | |
| `remote_download` | remote-downloads | `{url, dest, filename, policy}` | bytes | `{path, size, finalUrl}` |
| `checksum` | files/checksum > 100 MB | `{path, algos, verify}` | bytes | `{md5, sha1, sha256, verified}` |
| `dir_size` | files/dir-size, storage-analysis | `{path}` | items | `{files, dirs, bytes, tree?, largest?}` |
| `replace_preview` / `replace_apply` | search | `{query…}` / `{previewId, files}` | items (file) | §8.12 |
| `batch_rename` | > 200 item | | items | |
| `thumbnail_batch` | internal (folder dibuka dalam view thumbnail) | `{dir, paths}` | items | tidak tampil di panel user (flag `hidden`) |
| `recalc_quota`, `clear_thumbs`, `clean_uploads`, `purge_trash`, `integrity_check`, `backup_db`, `prune_logs` | admin / jadwal | `{userId?}` | items | laporan |
| `delete_user` | admin | `{userId, mode}` | items | |
| `duplicate_finder` | fase akhir | `{path}` | items | `{groups:[{size, hash, paths}]}` |

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

- `tasks.Engine` memegang `registry map[type]Runner` di mana `Runner` = `func(ctx context.Context, t *Task, rt *Runtime) (result any, err error)`; `Runtime` menyediakan `Jail`, `Progress(done, total int64)`, `Log(level, msg)`, `Secret(key) string` (dari memori), `Lock`, `DB`.
- `Enqueue` menulis baris `tasks` (state `queued`) dalam transaksi bersama validasi; scheduler goroutine tunggal memilih task `queued` tertua yang user-nya punya slot, memanggil `TryAcquire` lock (jika gagal → tetap `queued`, dicoba lagi saat ada perubahan), lalu menjalankan di goroutine dengan `context.WithCancel`.
- Progress ditulis ke DB maks 1×/detik dan ke SSE maks 2×/detik; ETA = (total − done) / EMA kecepatan 10 detik.
- Cancel: `cancel_requested=1` + `cancel()`; runner wajib memeriksa `ctx.Err()` paling lambat tiap 1 MiB atau 100 item; cleanup (hapus partial di `tmp`, hapus file tujuan yang baru dibuat sebagian, tidak pernah menghapus yang sudah ada sebelum task) lalu state `cancelled`.
- Shutdown: `SIGTERM` → server berhenti menerima koneksi, menunggu maks 20 detik (`TimeoutStopSec=30` di systemd), task yang masih `running` dibatalkan ctx-nya dan ditandai `interrupted`; saat start, semua baris `running`/`queued` yang bukan milik proses hidup → `interrupted` (queued tetap `queued`).
- Retry: membuat task **baru** dengan payload yang sama (`result.retryOf`), kecuali yang butuh password (compress/extract ber-password) → UI meminta password ulang.
- Log: `rt.Log` menulis ke `task_logs`; level `warn` untuk item yang dilewati (zip-slip, symlink, ekstensi diblokir).
- Pembersihan: task final > 30 hari dihapus bersama log; `tmp/<task_id>` dihapus saat task final; saat start, `tmp/*` yang tidak punya task `running` dihapus.
- Job terjadwal (cron internal, `robfig/cron` atau ticker sederhana, zona `TZ_DEFAULT`): `purge_trash` 03:00 harian, `recalc_quota` 03:30 harian (semua user, dibatasi 1 jam, `nice`), `backup_db` 02:30 harian (keep 7), `clean_uploads` tiap jam, `prune_logs` 04:00 harian, `integrity_check` tiap 6 jam, sesi kedaluwarsa tiap jam.

### 7.4 Panel task (frontend)

Ikon "Tasks (n)" di top bar menampilkan popover daftar task aktif + 20 terakhir: judul, progress bar, kecepatan/ETA, tombol cancel/retry/log/hapus. Panel transfer upload (§8.8) terpisah di pojok kanan bawah tetapi memakai store Zustand yang sama (`useTasksStore`). Semua teks dari i18n; progress diumumkan ke live region saat selesai/gagal.

---

## §8 Spesifikasi per fitur

### 8.1 Auth & akun

**Seed admin.** Saat start, jika `SELECT count(*) FROM users` = 0: buat user `ADMIN_USERNAME` dengan `ADMIN_INITIAL_PASSWORD` (argon2id), role `admin`, `must_change_password=1`, `quota_bytes=DEFAULT_USER_QUOTA`, `created_by=NULL`; audit `system.seed_admin`. Simpan `settings.initial_password_hash` = SHA-256(password awal) untuk cek "password baru tidak boleh sama dengan awal" (dihapus setelah password diganti). Jika tabel tidak kosong, env tidak disentuh; jika `ADMIN_INITIAL_PASSWORD` masih terisi dan admin sudah `password_changed_at` → `WARN`.

**Login.** `POST /auth/login`:

1. Rate limit per IP (20/menit) → `RATE_LIMITED`.
2. Normalisasi username lowercase; ambil user; jika tidak ada → tetap jalankan argon2 pada hash dummy (konstan waktu) → `UNAUTHENTICATED`.
3. `locked_until > now` → `ACCOUNT_LOCKED` (retryAfter). `status=disabled` → `ACCOUNT_DISABLED`.
4. Verifikasi argon2id; gagal → `failed_login_count++`, `login_attempts` (success=0); lockout bertahap: gagal ke-5 → 1 menit, ke-10 → 5 menit, ke-15 → 15 menit, ke-20+ → 60 menit (`locked_until`); audit `auth.login_failed`.
5. Jika `totp_enabled` → butuh `totp` (6 digit, window ±1, anti-replay kode terakhir) → `VALIDATION_FAILED totp_required|totp_invalid`.
6. Sukses: reset counter; **provisioning folder** (§8.2); buat sesi (token 32 byte → cookie; DB simpan hash; `csrf_secret`); `last_login_at`; audit `auth.login`; response user + `csrfToken`.
7. Jika `must_change_password` → client diarahkan ke `/change-password`; server menolak semua endpoint S+P dengan `PASSWORD_CHANGE_REQUIRED`.

**Ganti password.** Validasi: panjang ≥ `PASSWORD_MIN_LENGTH`, ≤ 128, bukan di daftar password umum (100k entri, embed, dibandingkan lowercase), tidak sama dengan password sekarang maupun hash awal, tidak mengandung username. Sukses → hash baru, `must_change_password=0`, `password_changed_at`, cabut semua sesi lain, audit `auth.password_changed`.

**Sesi.** Middleware: baca cookie → hash → baris sesi; cek `revoked_at`, `absolute_expires_at`, `idle_expires_at`; perbarui `last_seen_at`/`idle_expires_at` jika > 5 menit sejak terakhir; muat user (cache 5 detik); `status=disabled` → cabut + `ACCOUNT_DISABLED`. Cookie: `fm_session=<token>; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=<absolute>`. Logout menghapus baris. Perubahan password/role/status oleh admin mencabut semua sesi user itu dan mengirim SSE `session.revoked`.

**CSRF.** Token = base64url(HMAC-SHA256(csrf_secret, session_id)); diberikan saat login dan `GET /auth/csrf`; client menyimpannya di memori (bukan cookie) dan mengirim `X-CSRF-Token` di setiap non-GET termasuk tus dan multipart. Server memverifikasi konstan-waktu; selain itu menolak jika `Sec-Fetch-Site` ∈ {`cross-site`} atau (`Origin` ada dan ≠ `APP_BASE_URL`). Endpoint publik unlock share memakai token CSRF anonim (double-submit: cookie `fm_pcsrf` + header) agar password share tidak bisa dipaksa dari situs lain.

**Role & authz.** Setiap handler memanggil `authz.Require(ctx, Policy{Admin bool, OwnerOf path, ShareEditor …})`; akses ke resource ber-ID (task, share, version, trash item, upload session) selalu `WHERE id = ? AND user_id = ?`. Test IDOR (§10.6) mencakup semua grup endpoint.

**Admin browse user** (`ADMIN_CAN_BROWSE_USERS=true`): `as_user` membuka `Jail` user target read-write; UI admin menampilkan banner merah "Anda sedang melihat folder <user>"; setiap request → `audit_logs action=admin.browse_user details{path, method}`.

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
- Enforcement di: precheck & create tus (reservasi), finalisasi upload (ukuran nyata), copy/duplicate (hitung ukuran sumber dulu; folder → walk cepat dengan batas 20 detik, jika melebihi → tolak dengan `details.reason:"unknown_size"` → user bisa memaksa sebagai task yang berhenti saat kuota habis), extract (pre-scan total uncompressed ≤ sisa), remote download (`Content-Length` jika ada; tanpa itu → berhenti saat melewati sisa), editor save (delta ukuran), restore dari trash (tidak dicek: file sudah dihitung), file request upload (kuota pemilik).
- Inkremental: setiap mutasi memanggil `quota.Apply(ctx, userID, deltaUsed, deltaTrash, deltaVersions)` dalam transaksi yang sama dengan metadata; `dirty=1` jika operasi sebagian gagal.
- Rekalkulasi: walk `Lstat` seluruh root user (symlink = ukuran link) + ukuran folder `trash/<uid>` + `versions/<uid>`; jadwal 03:30, aksi admin, dan otomatis jika `dirty=1` saat idle. Perbedaan > 1 % dicatat `WARN` + audit `quota.drift`.
- Indikator: sidebar "Kuota 62 %" (warna warning ≥ 80 %, danger ≥ 95 %), tooltip rincian; SSE `quota.updated` setelah task besar.
- Disk penuh: semua writer juga memeriksa `Statfs` free ≥ 512 MB; jika tidak → `QUOTA_EXCEEDED reason:disk`; dashboard warning.

### 8.3 Listing & sort

**Algoritma `listing.List`:**

1. `jail.Stat(dir)`; bukan dir → `VALIDATION_FAILED not_a_directory` (UI membuka viewer).
2. Cache key = `hash(userID|scope|path|sortSpec|foldersFirst|hidden)`; entri `{dirMtime, builtAt, items []Item}`; valid jika `dirMtime` sama dan `builtAt` < 15 detik lalu, dan tidak di-invalidate. Cache LRU global 64 MB (≈ 100 folder besar).
3. Build: `ReadDir` → `Lstat` tiap entri (10k entri ≈ 50–150 ms di SSD); ambil metadata batch dari DB (`WHERE user_id=? AND path IN (…)` dipecah per 500) untuk pins/favorites/tags/notes/shares/media_metadata; `mime` untuk file ditentukan dari ekstensi (cepat) dan diperbaiki oleh `media_metadata.mime` jika ada; `typeLabel` dari tabel ekstensi→label i18n.
4. Sort stabil (`sort.SliceStable`) dengan komparator berlapis: pinned desc → (foldersFirst: isDir desc) → field utama → field sekunder → nama (collator) → path.
5. Potong halaman: tanpa cursor → `items[0:limit]`; dengan cursor → cari posisi item pertama yang `> cursor` (binary search pada slice tersortir memakai komparator yang sama; cursor menyimpan `{v:1, f:sortField, d:dir, k:<nilai field item terakhir>, n:<nama>, p:<pinned>, i:<isDir>}`), sehingga cursor stabil walau folder berubah (item yang disisipkan sebelum posisi tidak menyebabkan duplikasi/lompatan item setelahnya).

**Natural sort.** Komparator natural kustom `listing.NaturalLess(a, b, coll)`: nama dipecah menjadi run digit dan non-digit; run digit dibandingkan secara numerik (nol di depan diabaikan, lalu panjang, lalu leksikal), run non-digit dibandingkan dengan collator `collate.New(tag, collate.IgnoreCase)` dengan `tag` = `language.Indonesian` atau `language.English` sesuai preferensi; hasil seri → byte-wise. **Jangan** memakai `collate.Numeric`: issue golang/go#67997 (`"0.0"` vs `"1.0"` salah urutan) ditutup "not planned" pada Sep 2026 walau ada perubahan `internal/colltab: fix zero digit` (Mei 2026) — status perbaikan tidak pasti (diverifikasi 14 Okt 2026), sehingga komparator sendiri + unit test wajib (`file2 < file10`, `0.0 < 1.0`, `a-21 < a-123`, `01` vs `1` deterministik, Unicode/NFC). Kunci non-digit (`collator.KeyFromString`) dihitung sekali per item saat build. Contoh urutan: `file1, file2, file10, File11, gambar, Zeta` (case-insensitive; `é` setelah `e`). Tie-break byte-wise nama.

**Field sort** (server): `name`, `size` (folder: itemCount jika diketahui, else 0; folder yang sudah dihitung memakai bytes), `type` (typeLabel lalu nama), `ext`, `mtime`, `btime` (NULL paling akhir), `atime`, `mode` (octal numerik), `note` (kosong paling akhir), `dimensions` (width×height area; NULL akhir), `duration` (NULL akhir). Arah `asc|desc`; `desc` membalik hanya field utama, bukan tie-break nama.

**Filter cepat & hidden.** `filter=` menyaring substring nama case-insensitive (setelah fold Unicode) pada seluruh folder di server (bukan hanya halaman). `hidden=false` menyembunyikan dotfile. `types=` (`image,video,audio,document,archive,code,folder`) untuk pemilih tipe.

**Response Item** juga membawa `itemCount` untuk folder hanya jika murah: dihitung lazy di background untuk folder dengan ≤ 10.000 entri (ReadDir tanpa stat) dan di-cache 60 detik; jika tidak tersedia → `null` dan UI menampilkan "—".

**Target:** folder 10.000 item: API p95 < 500 ms (dingin) / < 50 ms (cache); payload halaman 500 item ≈ 120 KB JSON gzip 20 KB (Nginx gzip on untuk `application/json`).

### 8.4 Views & pipeline thumbnail

**Mode view** (preferensi `view.mode` + `folder_view_prefs`): `details`, `list`, `tiles`, `large`, `xlarge` sesuai tabel prompt bagian 5. Komponen `FileGrid` memakai TanStack Virtual: Details = baris 36 px virtual (header 32 px sticky); List = kolom mengalir atas→bawah lalu kiri→kanan: tinggi kontainer menentukan baris per kolom, virtualisasi per kolom horizontal; Tiles = grid 220×64 px; Large = 128 px cell + nama 2 baris (`text-overflow` tengah via JS: `abc…xyz.ext`); Extra large = 256 px.

**Thumbnail (server):**

| Sumber | Tool | Perintah (argv) |
| --- | --- | --- |
| jpg, png, webp, gif (frame 1), bmp, tiff, avif, heic (jika libvips mendukung, `vips --list` saat start) | `vipsthumbnail` | `vipsthumbnail -- <in> -s <N>x<N> -o <out>.webp[Q=80,strip] --rotate` (EXIF orientation; `--rotate` nama flag perlu verifikasi per versi libvips) |
| svg | `vipsthumbnail` (librsvg) | sama; hasil raster, SVG sumber tidak pernah inline |
| video | `ffmpeg` | `ffprobe` durasi → `ffmpeg -nostdin -ss <10%> -i <in> -frames:v 1 -vf scale=<N>:-2 -f webp -quality 80 -y <out>` (fallback `-ss 0` jika gagal) |
| pdf | `pdftoppm` | `pdftoppm -f 1 -l 1 -scale-to <N> -png -- <in> <outprefix>` → vips konversi ke webp |
| audio (cover) | `ffmpeg` | `ffmpeg -nostdin -i <in> -an -vcodec copy -f image2 -y <tmp>` → vips resize |
| fallback gambar tanpa vips | Go `image` (jpg/png/gif/webp decode) + `x/image/draw` | lambat, hanya jika vips absen |

Semua CLI: `exec.CommandContext` dengan timeout 30 s (video 60 s), `nice -n 10`, `ionice -c3` (jika binary `ionice` ada), `Env` minimal (`PATH`, `HOME=/nonexistent`, `TMPDIR=DATA_DIR/tmp`), stdin `/dev/null`, stdout/stderr dibatasi 64 KB, file input dibuka lewat `Jail.Open` lalu diteruskan sebagai `/proc/self/fd/N`? — **tidak**; path absolut sumber dibentuk oleh `fsjail` khusus untuk CLI (`jail.HostPath(p)` hanya dapat dipanggil oleh package `thumbs/archive/search/media`, diverifikasi `Lstat` tanpa symlink pada setiap segmen tepat sebelum exec untuk memperkecil TOCTOU; race residual diterima karena CLI hanya membaca dan berjalan sebagai user `filemanager`).

- Ukuran 128/256/512; cache file `DATA_DIR/thumbs/<uid>/<h[0:2]>/<h>-<size>.webp`, `h = sha256(path|size|mtime|fileSize)`; `ETag = h`; `Cache-Control: private, max-age=86400`; `If-None-Match` → 304.
- Dihapus saat file dihapus/dipindah/diubah (prefix by path lewat tabel `media_metadata` → `thumb_state`); job `clear_thumbs` admin; LRU jika folder thumbs > 2 GB (hapus atime tertua).
- Antrian prioritas: request `GET /files/thumb` yang belum ada → masuk heap dengan prioritas (waktu request terbaru lebih tinggi; viewport = request yang datang); worker pool `THUMB_WORKERS`; dedup per key; response `202` + `Retry-After: 2` sementara; `thumbnail_batch` (saat folder dibuka di view thumbnail) menambahkan sisa item dengan prioritas rendah.
- Client: `IntersectionObserver` (rootMargin 200 px), maks 4 request thumb paralel, placeholder ikon tipe, `<img decoding="async" loading="lazy">`, retry 202 maks 3× dengan backoff 2/4/8 detik lalu fallback ikon.
- `media_metadata` diisi oleh worker yang sama (ffprobe/`image.DecodeConfig`), dipakai kolom Dimensi/Durasi dan sort.

### 8.5 Operasi file

**Rename** (`files/rename`): validasi nama; `TryAcquire(write, old, new)`; `Rename`; `meta.RewritePaths`; `activity_logs rename`; response membawa `undo` (objek `{op, args, exp, sig}` HMAC 60 detik).

**Copy / Move** (`files/copy|move`):

1. Validasi: semua `items` ada; `dest` folder ada; untuk tiap item, `dest` bukan item itu sendiri maupun turunannya (`VALIDATION_FAILED cannot_move_into_self`); nama tujuan = basename item.
2. Resolusi konflik per item: `Lstat(dest/name)` ada → `policy.perItem[path]` atau `policy.default`: `overwrite` (file menimpa file; folder menimpa folder = **merge** dengan overwrite per file, bukan hapus folder tujuan), `skip`, `rename` (`nama (1).ext`, `nama (2).ext`… — untuk folder `nama (1)`), `ask` → `ALREADY_EXISTS` dengan `details.conflicts:[{path, existing:{size,mtime,isDir}, incoming:{…}}]` agar UI menampilkan dialog bandingkan.
3. Estimasi: jumlah file & byte (walk cepat, batas 5 s); ≤ 200 item & ≤ 256 MB → sinkron (maks 5 s, jika lewat → otomatis diubah menjadi task dengan response `202`); selain itu → task `copy|move`.
4. Kuota (copy): cek sebelum mulai; task berhenti `failed QUOTA_EXCEEDED` jika habis di tengah (file parsial dihapus).
5. Move dalam satu filesystem = `Rename` per item (atomik, instan); referensi metadata di-rewrite. Copy tidak menyalin metadata (catatan/tag) kecuali preferensi `fileops.copy_metadata=true`.
6. Lock: write pada tiap item sumber (move) / read (copy) dan write pada `dest/<name>`.

**Duplicate**: copy ke folder yang sama dengan nama `nama (1).ext` → `nama (2).ext`…; folder sama.

**Symlink**: `files/symlink` (§5.4). UI: dialog "Buat symlink" dengan picker target (hanya dalam root).

**Chmod**: mode input `"0644"`/`"644"`/checkbox rwx ×3; bit lain dibuang; rekursif → task dengan `fileMode` dan `dirMode` terpisah (default: dir mendapat bit x jika r ada). Pemilik/grup tidak bisa diubah (non-goal).

**Batch rename** (`files/batch-rename`):

- Urutan transformasi per item: `find/replace` (plain atau regex Go RE2, flag case; `replace` mendukung `$1`) → `case` (`upper|lower|title`) pada nama tanpa ekstensi → `extension` (`keep|lower|upper`) → `numbering`: token `{n}` (mulai `start`, increment 1, urutan = urutan seleksi), `{n:3}` pad 3 (`001`), `{name}` nama asli tanpa ekstensi, `{ext}` ekstensi tanpa titik, `{date:yyyy-MM-dd}` mtime.
- Preview mengembalikan `newName` per item dengan `conflict` (bentrok dengan item lain di folder atau sesama hasil) atau `invalid` (`NAME_INVALID` reason). Apply hanya jika tidak ada conflict/invalid, kecuali client mengirim `skipInvalid:true`.
- Eksekusi memakai **dua fase** untuk menghindari tabrakan berantai (`a→b`, `b→c`): rename semua ke nama sementara `.<uuid>.fmtmp` lalu ke nama akhir; gagal di tengah → rollback ke nama asli. > 200 item → task.

**Delete ke Recycle Bin** (`files/delete`): per item: buat baris `trash_items` (`kind` dari mime, `size_bytes`/`item_count` via walk dengan batas 20 s — jika melebihi, `size_bytes` diisi dari task `dir_size` setelahnya), `MoveOut` ke `trash/<uid>/<id>`, `shares` → `disabled item_trashed`, `quota.Apply(-size, +size)`, `activity delete`. Satu request = satu `batch_id`. Dalam share Editor: item masuk Recycle Bin **pemilik** (penerima tidak punya trash untuk item itu) dengan `activity` di kedua sisi.

**Hapus permanen** (`Shift+Delete`, `files/delete-permanent`): konfirmasi UI; > 50 item wajib `confirm:"HAPUS"`; task `delete_permanent`: `RemoveAll`, hapus semua referensi + versi, `quota.Apply(-size)`, audit `file.delete_permanent` (jumlah & path pertama).

**Undo** (`files/undo`): body = objek `undo` dari response sebelumnya (`{op, args, exp, sig}`), diverifikasi HMAC `signed-url` + umur ≤ 60 s + user sama. Operasi balik: `rename` → rename kembali (gagal `CONFLICT` jika nama lama sudah terisi); `move` → move kembali ke folder asal; `restore` (dari delete) → `trash/restore {batchId}`; `batch-rename` → pasangan dibalik. Toast "Urungkan" 10 detik di UI; setelah 10 detik tombol hilang walau token masih berlaku 60 s (untuk latency).

**Dialog "Pindah ke… / Salin ke…"**: folder picker tree lazy (`files/list` dengan `types=folder`), tombol folder baru, menampilkan Favorit dan Recent folder; destinasi default = folder saat ini; validasi "tidak bisa ke dalam dirinya sendiri" di client dan server.

**Drag & drop**: `dnd-kit` atau HTML5 DnD native (pilih native untuk dukungan drop dari OS; verifikasi kompatibilitas dengan virtualisasi). Drop ke baris folder / breadcrumb / sidebar (Favorit, folder di tree) = move; tahan `Ctrl` saat drop = copy; indikator visual badge "Pindah/Salin". Drag dari OS = upload (`DataTransferItem.webkitGetAsEntry` untuk folder). Auto-scroll saat drag mendekati tepi.

**Clipboard aplikasi**: store Zustand `{mode:"copy"|"cut", items:[{path, share?}], from}` persist di `sessionStorage`, berlaku lintas tab folder dan tab browser (via `BroadcastChannel`); Cut menampilkan item sumber semi-transparan; Paste → `copy|move` dengan `dest` = folder aktif.

**Rename inline**: F2/klik lambat; input dengan nama tanpa ekstensi terseleksi; Esc batal; Enter/blur simpan; validasi real-time (nama invalid, duplikat).

### 8.6 Recycle Bin & undo

- Lokasi fisik `DATA_DIR/trash/<user_id>/<trash_item_id>` (nama asli disimpan di DB sehingga dua item bernama sama tidak bentrok).
- Halaman `/trash`: tabel (Nama, Lokasi asal, Ukuran, Dihapus, Kedaluwarsa), filter `kind` (file/folder/gambar/dokumen/video/audio/arsip), pencarian nama, sort, seleksi batch; aksi Restore, Restore ke…, Hapus permanen, Kosongkan; status bar total ukuran; banner "Item dihapus otomatis setelah N hari".
- Restore: tujuan default `original_path`; jika folder induk tidak ada → `MkdirAll`; jika nama sudah terisi → dialog konflik (overwrite/rename/skip); `MoveIn`; referensi metadata tetap (tidak diubah saat delete); share yang `disabled_reason=item_trashed` → dialog "aktifkan kembali share?" (client memanggil `PATCH /shares/{id} {disabled:false}`); `quota.Apply(+size, -size)`; `activity restore`.
- Auto-purge: job 03:00 menghapus `expires_at < now` (RemoveAll + referensi + kuota) dalam batch 100, dicatat `activity purge_auto`.
- Preview sebelum restore: `GET /trash/{id}/download` (file) atau listing read-only isi folder (`GET /trash/{id}/list` — fase akhir, opsional).
- Retensi 0 = tidak auto-purge; admin bisa memaksa purge semua user lewat maintenance.

### 8.7 Favorit, pin, catatan, tag, Recent, Properties, Analisis penyimpanan

- **Favorit**: file/folder; sidebar "Favorit" menampilkan daftar (folder bisa diklik masuk; file membuka viewer); DnD ke sidebar menambah favorit; badge bintang di kolom Nama; referensi rusak → badge + "Hapus dari favorit".
- **Pin**: per folder induk; item pinned selalu di atas (setelah sort), badge pin; maks 50 pin per folder.
- **Catatan** (remark/"PS" aaPanel): kolom "Catatan" editable inline (klik dua kali sel), ≤ 1000 karakter, Markdown tidak dirender (plain text, escape).
- **Tag warna**: 7 warna; badge titik warna sebelum nama; filter cepat "Tag: merah" di toolbar; halaman pencarian mendukung `tag=`; batch set/hapus.
- **Recent**: derivasi `activity_logs` (open/edit/upload/create/restore) 30 hari, dedup per path, maks 100, hanya yang masih ada; kolom Lokasi; aksi "Buka lokasi file" (navigasi ke folder + seleksi item).
- **Properties** (dialog/panel): Nama (editable), Tipe (label + MIME), Lokasi (path virtual, tombol copy), Ukuran (file: byte & manusiawi; folder: tombol "Hitung" → task `dir_size` → files/dirs/bytes), Diubah/Dibuat/Diakses (absolut + relatif), Permission (rwx grid + octal, tombol Ubah), Symlink target, Checksum (MD5/SHA-1/SHA-256: file ≤ 100 MB sinkron, lebih besar task; tombol salin; input "Verifikasi hash" → cocok/tidak), Riwayat versi (jumlah, buka panel versi), Share (daftar + buka dialog), Catatan, Tag, Favorit, Pin. Multi-seleksi → ringkasan jumlah & total ukuran (task jika folder).
- **Copy path**: menyalin path virtual (`/Proyek/a.txt`); menu "Salin link share" hanya jika ada share publik aktif.
- **Analisis penyimpanan** (`/storage`): task `dir_size` pada `/` dengan agregasi per folder (depth ≤ 3 untuk treemap, node < 0,5 % digabung "Lainnya"), 50 file terbesar (heap), ringkasan per tipe (gambar/video/audio/dokumen/arsip/lainnya), ukuran Recycle Bin dan versi; cache 1 jam; treemap SVG (d3-hierarchy, pakai skill dataviz saat implementasi), klik node → navigasi; tombol "Hitung ulang".

### 8.8 Upload

**Sumber**: tombol "Upload ▾" (File / Folder), drag-drop ke area listing (overlay "Lepaskan untuk mengunggah ke <folder>") atau tepat ke baris folder (upload ke folder itu), paste (`Ctrl+V` saat clipboard OS berisi gambar → `upload/clipboard`; berisi file → tus), context menu "Upload ke sini".

**Alur client** (`features/upload`):

1. Kumpulkan `File`/entri folder (webkitGetAsEntry rekursif, batas 10.000 file per drop → selebihnya ditolak dengan pesan).
2. `upload/precheck` per 200 file; tampilkan dialog konflik gabungan (Timpa / Lewati / Ganti nama, "Terapkan ke semua", bandingkan ukuran & tanggal) dan daftar yang ditolak (ukuran > maks, ekstensi, kuota tidak cukup → tawarkan lanjut sebagian).
3. Antrian: `concurrency 3`; tiap file `tus.Upload` dengan metadata `dest`, `name`, `relPath`, `policy`, `lastModified`; progress agregat (byte); kecepatan rata-rata jendela 5 detik; ETA.
4. Panel transfer (pojok kanan bawah, bisa diciutkan, tetap ada saat pindah route; `Zustand` persist daftar ke `sessionStorage` untuk resume setelah reload): per file nama, progress, kecepatan, status (`queued|uploading|paused|error|done|skipped`), tombol pause/resume/cancel per file dan semua, "Bersihkan selesai", "Buka folder".
5. Setelah tiap file selesai: invalidasi listing `dest`; toast ringkasan di akhir batch.
6. Error: 4xx (selain 423/429) → status `error` dengan kode; 5xx/jaringan → retry backoff; `QUOTA_EXCEEDED` menghentikan antrian sisa.
7. Reload halaman: panel menampilkan transfer sebelumnya dalam status `paused` dengan tombol "Lanjutkan" (butuh user memilih file lagi karena `File` handle hilang — kecuali File System Access API tersedia; fingerprint tus memastikan lanjut dari offset).

**Server**: §6.5. Finalisasi memverifikasi ukuran, mode file `0640`, mtime dari `lastModified` (opsional pref), nama akhir sesuai policy, dedup nama `rename` dilakukan atomik dengan `O_EXCL`-style loop (coba `Rename` ke nama; jika `EEXIST` → nomor berikutnya; maks 1000).

### 8.9 Download, zip streaming, signed URL

**Signed URL** (`download.Signer`): `key = HKDF(APP_SECRET, "signed-url")`; string yang ditandatangani = `v1|<scope>|<path>|<purpose>|<exp>|<extra>`; `scope` = `u:<user_id>` atau `s:<share_id>`; `extra` = `size` untuk thumb, `token` untuk zip; `sig = base64url(HMAC-SHA256)[:32]`. Verifikasi konstan-waktu; `exp` unix detik. TTL: `purpose=download|thumb|inline` → `SIGNED_URL_TTL` (5 m); `media` → `MEDIA_URL_TTL` (2 h). Signed URL **tidak** memerlukan cookie (dipakai download manager, `<video>`, tab baru). Pencabutan sesi tidak membatalkan signed URL yang masih hidup (diterima; TTL pendek).

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

**Zip streaming** (`POST /files/zip` → `GET /files/zip?token`): token = signed URL dengan `extra` = id entri cache 5 menit berisi daftar path. Handler: `archive/zip.Writer` langsung ke `w` (dibungkus `bufio` 1 MiB), `Content-Type: application/zip`, `Content-Disposition` nama `<folder>.zip` atau `<app>-<yyyyMMdd-HHmm>.zip`, `Transfer-Encoding: chunked` (tanpa `Content-Length`), metode `Store` default (CPU 1 vCPU) / `Deflate` level 1 jika diminta, `zip64` otomatis, nama entri UTF-8 flag, walk tanpa mengikuti symlink dir, symlink file dilewati (dicatat), item yang hilang di tengah dilewati. Read lock pada sumber. Client putus → `ctx.Done` menghentikan walk.

**Download folder di aaPanel** = compress lalu download; di sini zip streaming menggantikan tanpa file sementara; menu "Compress" tetap ada untuk membuat file arsip.

**Nama file UTF-8**: `filename="<ASCII fallback: non-ASCII → _>"; filename*=UTF-8''<url-encode>`.

### 8.10 Remote download & SSRF guard

**Input** (dialog "Remote download"): textarea URL satu per baris (maks 20), folder tujuan (default folder aktif), nama file per URL (opsional; default dari `Content-Disposition filename*`/`filename`, lalu segmen path terakhir URL, lalu `download-<n>`), policy konflik, tombol "Periksa" (`probe`).

**Task `remote_download`** (`internal/download/remote.go`):

1. Parse URL: skema wajib `http`/`https`; tanpa userinfo (`user:pass@` ditolak `VALIDATION_FAILED`); host bukan IP literal terlarang dan bukan `localhost`; port hanya 80/443/8080/8443 kecuali allowlist admin mengizinkan (default: semua port > 1023 ditolak? → **keputusan**: port 80, 443, dan 1024–65535 diizinkan; 1–1023 selain 80/443 ditolak).
2. Allow/denylist domain (settings): denylist menang; suffix match (`*.example.com`). Jika allowlist tidak kosong → hanya yang cocok.
3. **Resolve DNS** (`net.DefaultResolver.LookupNetIP`) → tolak jika **ada satu pun** alamat yang termasuk: `0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`, `192.0.0.0/24`, `192.0.2.0/24`, `192.168.0.0/16`, `198.18.0.0/15`, `198.51.100.0/24`, `203.0.113.0/24`, `224.0.0.0/4`, `240.0.0.0/4`, `255.255.255.255/32`, `::/128`, `::1/128`, `::ffff:0:0/96` (IPv4-mapped → periksa IPv4-nya), `64:ff9b::/96`, `fc00::/7`, `fe80::/10`, `ff00::/8`, `2001:db8::/32`. Alamat yang lolos dipilih satu (prefer IPv4).
4. **Pin IP**: `http.Transport` dengan `DialContext` kustom yang menyambung ke `ip:port` yang sudah divalidasi (bukan hostname), `TLSClientConfig.ServerName = host`; `Control` di `net.Dialer` memeriksa ulang alamat tujuan (double-check) — mencegah DNS rebinding karena resolusi hanya sekali oleh kita.
5. **Redirect**: `CheckRedirect` menjalankan langkah 1–4 pada URL baru; maks 5; redirect ke skema non-http(s) ditolak; header `Authorization` tidak diteruskan.
6. Request: `GET` dengan `User-Agent: FileManager/<version> (+https://file.kppbwi.app)`, `Accept-Encoding: identity`, timeout koneksi 15 s, TLS handshake 15 s, header 30 s, idle baca 60 s (`ReadIdleTimeout` via `net.Conn` deadline per read), total maks 24 jam. `Range: bytes=<offset>-` untuk resume jika server mengirim `Accept-Ranges: bytes` + `ETag`/`Last-Modified` cocok.
7. Batas ukuran = min(`REMOTE_DOWNLOAD_MAX_SIZE`, `MAX_UPLOAD_SIZE`, sisa kuota, disk − 512 MB): jika `Content-Length` > batas → gagal sebelum mulai; tanpa `Content-Length` → `io.LimitReader` + gagal saat melewati batas.
8. Tulis ke `DATA_DIR/tmp/<task_id>/part` (sehingga bisa resume dalam task yang sama setelah putus, retry 3× backoff 5/15/45 s), setelah selesai verifikasi ukuran, deteksi MIME (ekstensi diblokir → gagal), `MoveIn` ke tujuan dengan policy, kuota, aktivitas, thumbnail.
9. Progress bytes + kecepatan + ETA; cancel menghapus part.
10. Audit tidak diperlukan (aktivitas user), tetapi `SSRF_BLOCKED` dicatat `WARN` dengan host dan IP (bukan URL penuh jika mengandung query).

Rate limit: 10 task remote/jam/user; admin bisa menonaktifkan (`REMOTE_DOWNLOAD_ENABLED=false` → menu disembunyikan dan endpoint `FORBIDDEN`).

### 8.11 Arsip

**Matriks format** (sesuai prompt bagian 9):

| Format | Extract | Compress | Password | Engine extract | Engine compress |
| --- | --- | --- | --- | --- | --- |
| zip | ya | ya | extract ZipCrypto & AES; compress AES-256 | mholt/archives (tanpa password); `internal/archive/zipaes` untuk AES & ZipCrypto (ZipCrypto decrypt saja, implementasi kecil) | `archive/zip` (Go) + `zipaes` untuk AES-256 |
| 7z | ya | ya | ya (AES-256, `-mhe=on` enkripsi nama) | mholt/archives (README: membaca 7z ber-password — diverifikasi 14 Okt 2026) untuk listing & extract tanpa argv; `7zz x` hanya fallback untuk arsip yang gagal dibaca library (mis. varian `-mhe`), password via stdin jika didukung | `7zz a` |
| rar v4/v5 | ya | hanya jika `RAR_PATH` | extract ya | mholt/archives (README: membaca RAR ber-password — diverifikasi 14 Okt 2026; rar5 dicek fixture Step 14a) → fallback `unrar x` jika `UNRAR_PATH` diisi (password ke argv → risiko ADR-08) | `rar a` |
| tar | ya | ya | — | Go (mholt/archives) | Go |
| tar.gz / tgz | ya | ya | — | Go (`klauspost/pgzip` jika dibawa archives) | Go |
| tar.bz2 / tbz2 | ya | ya | — | Go | Go (compress/bzip2 hanya decode → encode via `dsnet/compress/bzip2`, verifikasi; jika tidak ada → compress tar.bz2 via `7zz`) |
| tar.xz / txz | ya | ya | — | Go (`ulikunitz/xz`) | Go |
| tar.zst | ya | ya | — | Go (`klauspost/compress/zstd`) | Go |
| gz, bz2, xz, zst (file tunggal) | ya | ya | — | Go | Go |
| war, jar | ya (zip) | opsional (zip dengan ekstensi) | — | Go | Go |
| iso | opsional read-only | tidak | — | `7zz` | — |

Deteksi format dari magic bytes (`archives.Identify`) lalu ekstensi; ekstensi ganda (`tar.gz`) dikenali.

**Compress** (task): opsi dari §6.3; output default `<nama item>.<ext>` (banyak item: `<folder induk>.<ext>` atau `Arsip-<tanggal>`); konflik nama output → `rename`. Level: zip `store|fast(1)|normal(6)|max(9)`; 7z `-mx0|1|5|9`; tar.* level codec setara. Split volume (7z `-v<size>`, zip via 7zz `-v`) opsional. `deleteSource` → setelah verifikasi arsip bisa dibuka (listing) → delete ke Recycle Bin (bukan permanen). Progress = byte sumber yang dibaca.

**Extract** (task):

1. Buka arsip (read lock), pre-scan entri: hitung `totalUncompressed`, `entries`; format yang tidak punya ukuran uncompressed di header (gz stream) → batas diterapkan saat streaming.
2. Tolak `ARCHIVE_UNSAFE` jika entri: nama mengandung `..` segmen, diawali `/` atau `\`, drive letter `C:`, mengandung `\0`; symlink/hardlink yang resolusinya keluar folder tujuan (symlink dalam tujuan diperbolehkan tetapi dibuat **setelah** semua file reguler agar tidak bisa dipakai sebagai lompatan); device/FIFO/socket dilewati (warn).
3. Tolak `ARCHIVE_LIMIT` jika `entries > ARCHIVE_MAX_ENTRIES`, `totalUncompressed > min(ARCHIVE_MAX_EXTRACT_SIZE, sisa kuota, disk−512MB)`, atau rasio `totalUncompressed / archiveSize > 200` dengan `totalUncompressed > 1 GB` (nilai ekstrem untuk bomb; file teks sangat kompresibel sah < 1 GB tetap lolos). Saat streaming, hentikan jika byte tertulis melewati batas (bom yang berbohong di header).
4. Extract ke `DATA_DIR/tmp/<task_id>/root/` memakai `*os.Root` pada folder itu (zip-slip mustahil lewat kernel), mode file `0640`, dir `0750`, setuid/setgid/sticky dibuang, mtime dipertahankan, nama entri dinormalisasi NFC + `ValidateName` per segmen (nama invalid → entri dilewati + warn; ekstensi diblokir → dilewati).
5. Encoding nama (zip tanpa flag UTF-8): `auto` → deteksi chardet pada gabungan nama (kandidat GBK, Shift_JIS, Big5, CP437, Windows-1252) dengan ambang keyakinan; pilihan manual memaksa. Tar: UTF-8 (fallback Latin-1 jika invalid).
6. Password: zip ZipCrypto/AES → library Go (`Secret("password")` dari memori); 7z → `7zz x -y -o<tmp> -- <archive>` dengan password: mode stdin (`-p` tanpa nilai, tulis password + `\n` ke stdin; **perlu verifikasi** apakah `7zz` membaca password dari pipe non-tty), fallback `-p<password>` di argv (risiko §10, keputusan ADR-08); rar → `unrar x -p<password>` argv (risiko sama) atau library.
7. Password salah: zip → `ARCHIVE_PASSWORD_WRONG` saat verifikasi check byte/HMAC entri pertama; 7z → exit code 2 + stderr "Wrong password" (parsing teks, verifikasi string per versi).
8. Pindah atomik: tujuan `here|new|<path>`; `new` = folder bernama arsip tanpa ekstensi (`rename` jika ada); jika tujuan belum ada → `Rename(tmp/root → dest)` satu kali; jika tujuan ada (extract "di sini") → per entri tingkat atas: `Rename` ke tujuan dengan policy (`overwrite`: file menimpa, folder merge rekursif per file; `skip`; `rename`).
9. Kuota: pre-scan + `quota.Apply` di akhir dengan byte nyata; thumbnail diantrekan; `file.changed`.
10. Arsip bersarang tidak diekstrak otomatis.

**Archive browser** (`GET /archives/entries`): membaca daftar entri tanpa extract (mholt/archives `Extract` dengan callback tanpa membaca body, atau `7zz l -slt` untuk 7z/iso), paging 1000; tree dibangun di client; preview teks (≤ 1 MB) dan gambar (≤ 20 MB, dibaca ke `tmp` lalu dilayani sekali) via `entry-content`; download entri streaming; extract sebagian = extract dengan `entries` (mendukung prefix folder). `supportsModify=true` hanya zip tanpa password: `add` = tulis ulang arsip ke `tmp` (copy raw entri lama `zip.Writer.CreateRaw`/`Copy`, tambah file baru), `Rename` menggantikan; `remove-entries` serupa; arsip > 2 GB → ditolak modifikasi (`UNSUPPORTED_TYPE reason:too_large_to_modify`).

**CLI**: `exec.CommandContext(ctx, SEVENZIP_PATH, "a", "-t7z", "-mx5", "-mhe=on", "-bd", "-y", "--", out, in...)`, `Dir` = `tmp/<task>`, env minimal, `nice -n 10`, `ionice -c3`, timeout = 6 jam, output dibatasi; progress 7zz via `-bsp1` parsing persen (verifikasi format) atau fallback progress berbasis ukuran output.

**Test fixture** (`backend/testdata/archives/`, dibuat oleh script Go `make fixtures`): `slip.zip` (`../../evil.txt`), `abs.zip` (`/etc/evil`), `symlink-escape.tar` (symlink `x → ../../etc`, lalu `x/evil`), `hardlink-escape.tar`, `bomb-small.zip` (10 MB nol → ratio tinggi, 1 file 10 MB; test memaksa `ARCHIVE_MAX_EXTRACT_SIZE=1MB`), `many-entries.zip` (10.001 entri kosong, test `ARCHIVE_MAX_ENTRIES=10000`), `nested.zip`, `gbk-names.zip`, `aes.zip` (password `secret`), `zipcrypto.zip`, `test.7z` (dibuat saat test jika `7zz` ada; jika tidak, test di-skip), `windows-names.zip` (`CON.txt`, `a .txt`).

### 8.12 Search & replace

**Pencarian nama** (`GET /search/names`): walk `Jail.WalkDir` dari `path` (default `/`), `recursive` default true, cocokkan substring case-insensitive pada nama (`strings.Contains` setelah `cases.Fold`), opsional wildcard `*`/`?` (diubah ke regex) dan `regex:true`; filter `types` (kelas MIME dari ekstensi), `ext`, `minSize/maxSize`, `from/to` (mtime), `tag` (join DB); batas 5.000 hasil atau 20 detik (`truncated:true`); hasil diurutkan (nama natural) dan dipaging dari cache hasil 60 detik (cursor); kolom Lokasi; UI: input di address bar "Cari di folder ini" (Ctrl+F) + halaman `/search` global dengan panel filter & riwayat 50 kueri. Rate limit 30/menit/user.

**Pencarian isi** (`POST /search/content`): engine `rg` jika `RG_PATH` ada:

```text
rg --json --no-ignore --hidden --no-follow --no-messages -n --column \
   --max-filesize <maxSize|50M> --max-count <perFile 200> \
   [-i|-s] [-w] [-F (plain) | -e (regex)] \
   [-g '*.ext' ...] [--max-depth 1 jika !recursive] \
   -- <pattern> .
```

dijalankan dengan `Dir` = host path folder (via `HostPath`), `nice`, timeout 30 s, output `--json` diparse streaming (`type: match` → `{file, line, col, text (dipotong 500 char), before/after dari `-C 1`}`), berhenti setelah `maxMatches` (2.000) lalu kill proses; filter `minSize/from/to` diterapkan server setelah match (rg tidak punya) via `Lstat` cache. Fallback Go: worker pool 2 goroutine, `regexp` (RE2; pola plain di-`QuoteMeta`), baca file ≤ maxSize dengan `bufio.Scanner` buffer 1 MB, deteksi biner (NUL di 8 KB pertama → lewati), hasil identik formatnya. Response NDJSON streaming; client merender incremental dengan virtual list; klik hasil → buka editor di baris itu (`/editor?path=&line=`). Rate limit 10/menit/user; satu pencarian isi aktif per user (yang baru membatalkan yang lama).

**Replace in files**: `replace/preview` (task): jalankan pencarian yang sama, untuk tiap file cocok hitung hasil replace di memori (regex `ReplaceAllString` dengan `$1`, plain `strings.ReplaceAll`), simpan hunk (baris sebelum/sesudah, maks 50 hunk per file ditampilkan; sisanya dihitung) ke `tmp/<task>/preview.json`; UI menampilkan diff per file dengan checkbox file. `replace` (task): untuk tiap file terpilih: lock write, baca ulang, verifikasi masih cocok dengan preview (hash isi sama; jika beda → file dilewati `warn`), buat versi `replace_in_files` (pre-image), tulis atomik (tmp + rename, pertahankan mode & encoding & EOL), aktivitas `replace`; result `{files, replacements, versionIds}` + log; rollback = restore versi per file (tombol "Batalkan replace" di hasil task memanggil `versions/{id}/restore` berurutan).

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
| `application/pdf` | PdfViewer (pdf.js) | `raw?purpose=media` via fetch + Range (`disableAutoFetch`, `rangeChunkSize 512 KB`) |
| `audio/*` (mp3, m4a/aac, ogg, opus, wav, flac) | AudioPlayer + mini-player | `raw?purpose=media` (Range) + `files/stat` → `media.tags`, cover `thumb?size=512` |
| `video/mp4, webm, ogg, quicktime, x-matroska, x-msvideo` | VideoPlayer (Video.js 10) | `raw?purpose=media`; `canPlayType` + `media.codec_*` dari ffprobe → jika tidak bisa diputar: pesan "Codec <x> tidak didukung browser ini" + download |
| `text/*`, `application/json`, `application/x-yaml`, `application/xml` (tampil sebagai teks), kode (dari ekstensi) | TextViewer (read-only, highlight ringan via `shiki`/`highlight.js` lazy — pilih satu saat implementasi; file ≤ 2 MB) + tombol "Edit" | `files/content` |
| `text/markdown` | MarkdownViewer (marked + DOMPurify, tab "Sumber") | `files/content` |
| `text/csv`, `text/tab-separated-values` | TableViewer (parse `papaparse` lazy, maks 100.000 baris, virtual) | `files/content` (≤ 20 MB) |
| `application/json` | JsonViewer (tree collapsible, ≤ 5 MB; lebih besar → TextViewer) | `files/content` |
| `text/html`, `application/xhtml+xml` | TextViewer sumber (preview live ditunda, ADR-06) | `files/content` |
| arsip (§8.11) | ArchiveBrowser | `archives/entries` |
| `application/vnd.openxmlformats-*`, `application/msword`, dll | InfoCard (ikon, nama, ukuran, tipe, download) | — |
| lainnya | InfoCard | — |

**Kerangka overlay** (`ViewerShell`): layar penuh (`role="dialog" aria-modal`), header: nama file, indeks "3 / 24", tombol prev/next (←/→, hanya file sekelas di folder yang sama berdasarkan listing yang sedang dimuat), download, share, info (panel kanan), tombol "Edit" (teks), close (Esc). Deep link `?view=<path>` pada route folder; tombol browser Back menutup overlay. Quick Look (Space) = overlay yang sama dengan ukuran 80 % dan tanpa navigasi folder; Space lagi/Esc menutup.

**ImageViewer**: `canvas`/`img` dengan transform; zoom scroll (ctrl+scroll di trackpad) 10 %–800 %, pinch (Pointer Events), tombol +/−, 1:1, fit, fill; pan drag; rotate 90° kiri/kanan, flip H/V (transformasi tampilan saja, tidak menulis file); fullscreen (Fullscreen API); slideshow (interval 2/5/10 s, loop, tombol play/pause); filmstrip thumbnail bawah (thumb 128, scroll ke aktif); panel info: dimensi, ukuran, MIME, EXIF ringkas (kamera, lensa, tanggal, exposure, ISO, f, focal), GPS disembunyikan kecuali pref `exif.show_gps` (tombol "Tampilkan lokasi" per sesi); gambar resized 2048 dimuat dulu, asli saat zoom > 100 % atau tombol 1:1; preload gambar berikutnya (resized); animasi GIF diputar dari asli.

**PdfViewer**: `pdfjs-dist` dengan worker self-hosted (`pdf.worker.min.mjs` via `?url`), render halaman virtual (hanya ±2 halaman di sekitar viewport), toolbar: sidebar thumbnail halaman & outline, zoom (fit width, fit page, 50–400 %), input halaman "3 / 120", cari teks (`PDFFindController`, highlight, next/prev), rotasi, print (`window.print` pada canvas render? → pdf.js `PDFPrintService` sederhana: render semua halaman ke canvas di iframe print, batasi 200 halaman), download; PDF ber-password → dialog `PasswordResponses.NEED_PASSWORD`; teks layer untuk seleksi; link internal; anotasi tampil read-only.

**AudioPlayer**: `HTMLAudioElement` tunggal global (di `app/` agar bertahan lintas route), UI custom: cover, judul/artis/album (dari `media.tags`, fallback nama file), progress seek, waktu, play/pause, prev/next, shuffle, repeat off/all/one, volume & mute (persist pref), kecepatan 0.5–2×; playlist otomatis = semua audio di folder saat diputar (urutan listing), panel playlist (DnD reorder, hapus, "Tambah dari seleksi"); mini-player sticky bawah (tinggi 56 px) saat overlay ditutup, tombol "buka penuh"; Media Session API (`metadata`, handler play/pause/prev/next/seekto); format tidak didukung (`canPlayType` kosong) → pesan + download.

**VideoPlayer** (Video.js 10 via `@videojs/react`; fallback media-chrome per ADR-07 — Vidstack/Plyr deprecated per 14 Okt 2026): sumber `raw?purpose=media` (Range → seek instan), kontrol: play/pause, seek, volume, kecepatan 0.25–2×, fullscreen, PiP, caption; subtitle sidecar `<nama>.vtt`/`<nama>.srt`/`<nama>.<lang>.srt` dicari di folder yang sama (server mengembalikan `subtitles:[{lang, url}]` di `files/stat` untuk video; srt dikonversi ke vtt oleh server saat diminta `raw?purpose=media&as=vtt`, cache); posisi terakhir disimpan di `localStorage` per `userId:path` (dan ke pref `video.positions` 100 terakhir) → resume dengan tombol "Lanjutkan dari 12:34"; playlist video folder (panel kanan, auto-next opsional); shortcut Space, ←/→ 5 s, J/K/L (−10 s / pause / +10 s), F, M, ↑/↓ volume, 0–9 lompat persen; `onError` → cek `SIGNATURE_EXPIRED` → minta signed URL baru, set `currentTime` kembali, lanjut; codec tidak didukung (HEVC di Chrome Linux, AVI/MKV tergantung) → kartu pesan + download; transcoding tidak ada.

**TextViewer/MarkdownViewer/TableViewer/JsonViewer**: read-only; tombol "Edit" membuka editor; Markdown: `marked` (gfm, breaks off) → DOMPurify (`USE_PROFILES: {html: true}`, `FORBID_TAGS: ['style','script','iframe','object','embed','form','input']`, `ALLOWED_URI_REGEXP` hanya `https?:`, `mailto:`, relatif → gambar relatif diubah ke signed URL `raw?purpose=inline` jika path ada; link relatif ke file lain membuka viewer); CSV: delimiter auto (`,;\t|`), header baris pertama (toggle), kolom bisa di-resize, sort client; JSON: tree expand/collapse, pencarian key, copy path.

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

**"Share saya"** (`/shares`): tabel: Item (ikon + nama + path), Mode (link/user), Penerima (chip), Permission, Proteksi (ikon password/expiry/limit), Kedaluwarsa, View/Download, Akses terakhir, Status (aktif/nonaktif/kedaluwarsa/rusak); aksi: Salin link, QR, Edit, Nonaktifkan/Aktifkan, Regenerate link, Hapus; detail → log akses ringkas (waktu, IP terpotong, aksi, path). Badge di kolom Nama & kolom "Status share".

**Admin**: `/admin/shares` melihat semua share publik (pemilik, nama item, dibuat, expiry, hitungan) tanpa isi; "Cabut" → `disabled_reason=admin_revoked` + audit `share.admin_revoked` + notifikasi pemilik.

**Siklus hidup**: item dihapus → `disabled item_trashed`; restore → tawarkan aktifkan; rename/move → path di-rewrite (link tetap berlaku); user dinonaktifkan → semua share pemilik `disabled owner_disabled` (otomatis aktif lagi saat user diaktifkan hanya jika reason itu); hapus user → share dihapus.

### 8.16 Notifikasi & aktivitas

**Notifikasi** (`notifications`): tipe `task_done` (judul task + link ke folder), `task_failed`, `share_received` ("budi membagikan folder Laporan ke Anda" → link `/shared-with-me`), `share_upload` ("Seseorang mengunggah 3 file ke share 'Drop' Anda"), `share_revoked_by_admin`, `system` (admin broadcast, fase akhir). Bell di top bar dengan badge unread (SSE `notification`), popover 20 terakhir, "Tandai semua dibaca", halaman penuh tidak diperlukan. Task tersembunyi (`thumbnail_batch`) tidak membuat notifikasi; task yang selesai saat user melihat panel task tetap membuat notifikasi tetapi langsung `read` jika tab aktif (client memanggil `read`).

**Aktivitas user** (`activity_logs`, halaman `/activity`): aksi `open`, `edit`, `create`, `upload`, `download` (hanya file tunggal via signed URL yang dibuat user), `rename`, `move`, `copy`, `delete`, `restore`, `purge`, `purge_auto`, `compress`, `extract`, `remote`, `replace`, `share_create`, `share_update`, `share_delete`, `chmod`, `batch_rename`; kolom waktu, aksi, item (link jika masih ada), tujuan, detail; filter aksi/tanggal; pencarian path; tombol "Hapus riwayat saya" (hanya aktivitas, bukan audit).

**Audit (admin)** `action`: `system.seed_admin`, `system.start`, `auth.login`, `auth.login_failed`, `auth.logout`, `auth.password_changed`, `auth.totp_enabled|disabled`, `user.create`, `user.update`, `user.reset_password`, `user.disable|enable`, `user.delete|archive`, `user.home_provisioned|home_reused`, `user.revoke_sessions`, `settings.update|reset`, `share.create|update|delete` (metadata saja), `share.public_access` (unlock_ok/download dari link publik; IP terpotong), `share.admin_revoked`, `file.delete_permanent`, `trash.empty`, `admin.browse_user`, `admin.task_cancel`, `maintenance.<job>`, `quota.drift`. `details_json` tidak pernah berisi password/token. Halaman `/admin/audit`: filter actor/action/tanggal/teks, export CSV (`admin/audit/export.csv`, streaming, maks 100k baris), retensi `AUDIT_RETENTION_DAYS`.

### 8.17 Admin

**Dashboard** (`/admin`): kartu ringkas (bukan hero): user aktif/nonaktif; storage: Σ `used` semua user vs Σ kuota vs disk `statfs(STORAGE_ROOT)` (bar bertumpuk), peringatan jika Σ kuota > disk; top 5 user; task berjalan/antre; share publik aktif; login gagal 24 jam (dari `login_attempts`); versi aplikasi (`version`, `commit`, `buildDate`, Go runtime); status dependency: tabel tool → path → terdeteksi (versi dari `--version` saat start, di-cache) → fitur yang terdampak; warning konfigurasi (ADMIN_INITIAL_PASSWORD masih ada, cross-FS, swap rendah (`/proc/meminfo` SwapTotal < 1 GB), disk < 10 %).

**Users** (`/admin/users`): tabel sesuai prompt; aksi baris: Edit (nama, role, kuota, status), Reset password (tampilkan sekali, tombol salin), Paksa logout, Nonaktifkan/Aktifkan, Hapus (dialog ketik username, pilih `purge` atau `archive`). Buat user: username (validasi live + cek unik), nama, role, kuota (default dari setting), password sementara: generate (16 karakter acak, tampil sekali) atau manual (validasi kebijakan). Aturan admin terakhir: server menolak `PATCH` role→user / status→disabled / `DELETE` jika target adalah satu-satunya admin aktif (`VALIDATION_FAILED last_admin`), termasuk diri sendiri.

**Hapus user** (task `delete_user`): cabut sesi; `purge` → `RemoveAll` root user + trash/versions/thumbs user + semua baris DB (cascade) + `users.deleted_at` (baris dipertahankan 30 hari untuk audit lalu dihapus); `archive` → `Rename` root user ke `DATA_DIR/deleted-users/<username>-<yyyyMMdd-HHmmss>` (cross-FS → copy), trash/versions user ikut dipindah ke dalam folder itu (`__trash`, `__versions`), baris DB dihapus; audit.

**Settings** (`/admin/settings`): form per kelompok (Upload, Storage, Recycle Bin & versi, Remote download, Sharing, Keamanan, Tampilan): tiap field menampilkan nilai efektif, sumber, tombol reset; validasi server sama dengan §3; perubahan → `settings.update` audit; nilai berlaku ≤ 5 detik (cache runtime). Daftar key yang bisa diubah = kolom "Override admin = ya" di §3.3 + `remote_download.allowlist/denylist`, `share.allow_custom_slug`, `branding.logo`.

**Tasks** (`/admin/tasks`): semua task semua user (kolom user), filter state, cancel (audit).

**Maintenance** (`/admin/maintenance`): tombol per job (§7.1) dengan konfirmasi; `recalc-quota` (semua/satu user), `clear-thumbs`, `clean-uploads`, `purge-trash` (semua user, hanya yang kedaluwarsa; opsi "paksa semua" dengan ketik `HAPUS`), `integrity-check` → laporan: folder di `STORAGE_ROOT` tanpa akun (tawarkan arsipkan), akun tanpa folder (akan dibuat saat login), referensi rusak per tabel, file yatim di `trash/versions/uploads/thumbs`, `backup-db` (`VACUUM INTO 'DATA_DIR/backups/app-<ts>.db'`, keep 7 + manual tidak dihapus otomatis jika diberi label), daftar backup + download (signed), `prune-logs`.

### 8.18 Profil & preferensi

**Profil** (`/settings`): ganti password (form; sesi lain dicabut), sesi aktif (tabel: perangkat, IP, dibuat, terakhir aktif, badge "sesi ini", tombol logout per sesi, "Logout semua sesi lain"), 2FA TOTP (fase akhir: setup QR `otpauth://`, verifikasi kode, recovery codes 10 × sekali pakai, nonaktifkan dengan password).

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
| `video.positions` | `{path: seconds}` 100 terakhir | |
| `notifications.task_toast` | bool | true |
| `shortcuts.overrides` | `{action: keys}` (fase akhir) | |
| `tabs.restore` | bool (pulihkan tab folder saat login) | true |
| `tabs.open` | `[{path}]` ≤ 10 | |

`PUT /me/preferences` adalah merge patch (JSON Merge Patch RFC 7386) dengan validasi key terdaftar; key tak dikenal ditolak `VALIDATION_FAILED`.

> Catatan: kolom `shares.token_enc` dan `shares.hide_listing` (§4.2) ada karena keputusan di §8.15; keduanya masuk migrasi awal.

---

## §9 UI spec

### 9.1 Design token

Didefinisikan sebagai CSS variables di `web/src/styles/tokens.css` (`:root` light, `[data-theme="dark"]` dark; `data-theme` diset sebelum render oleh script inline kecil di `index.html` yang membaca `localStorage.theme`/`prefers-color-scheme` → tanpa flash; script ini memakai nonce CSP yang dibuat server saat menyajikan `index.html`) dan dipetakan ke Tailwind lewat `theme.extend.colors` dengan `rgb(var(--x) / <alpha-value>)`.

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
| `--success` | #16A34A | #22C55E | |
| `--warning` | #D97706 | #F59E0B | |
| `--danger` | #DC2626 | #F87171 | |
| `--focus` | = accent | = accent | ring 2 px offset 1 px |

Aksen alternatif (pref `ui.accent`, mengganti `--accent`, `--accent-subtle`, `--accent-fg`): emerald #059669/#34D399, violet #7C3AED/#A78BFA, amber #D97706/#FBBF24 (fg gelap), rose #E11D48/#FB7185, slate #475569/#94A3B8. Script `web/scripts/check-contrast.ts` menghitung rasio WCAG untuk pasangan (text/bg, text-muted/bg, text/surface, accent-fg/accent, text/accent-subtle, danger/bg) di kedua tema dan semua aksen; gagal jika < 4.5:1 untuk teks normal, < 3:1 untuk UI non-teks; dijalankan di CI (`pnpm check:contrast`).

Warna ikon tipe (`--icon-folder` amber #F59E0B, `--icon-image` violet #8B5CF6, `--icon-video` rose #F43F5E, `--icon-audio` pink #EC4899, `--icon-archive` orange #F97316, `--icon-code` sky #0EA5E9, `--icon-pdf` red #EF4444, `--icon-doc` blue #3B82F6, `--icon-sheet` green #22C55E, `--icon-text` slate #64748B, `--icon-other` slate-400). Ikon tipe: set SVG internal (`components/FileIcon`, 24 px grid, glyph lucide (ISC) di atas bentuk file bulat 4 px), ukuran 16/24/48/96/192 via `currentColor`.

**Tipografi**: `Inter` (self-host, `font-feature-settings: "tnum"` di kelas `.tabular`), `JetBrains Mono` untuk path/permission/kode. Skala: 12 (meta/status bar), 13 (tabel/menu), 14 (UI), 16 (judul dialog), 20 (judul halaman); line-height 1.4; weight 400/500/600.

**Spasi & bentuk**: grid 4 px; radius 4 (badge), 6 (input/tombol), 8 (dialog/popover); border 1 px `--border`; shadow hanya popover/menu/dialog (`0 4px 16px rgb(0 0 0 / .12)` light, `.4` dark). Tinggi: top bar 48, address bar 40, toolbar 40, header tabel 32, baris 36 (compact 32), status bar 28, sidebar 232 px (ikon 48 px), panel preview 320 px.

**Motion**: 120–180 ms `cubic-bezier(.2,.8,.2,1)`; `@media (prefers-reduced-motion: reduce)` → 0 ms; tidak ada animasi pada listing besar selain fade skeleton.

### 9.2 Inventaris komponen

`components/ui` (shadcn, dikustom ke token): Button (variant `primary|secondary|ghost|danger`, size `sm|md|icon`), IconButton (32 px, tooltip wajib = label + shortcut), Input, Textarea, Select, Checkbox, Switch, RadioGroup, Tabs, Tooltip, DropdownMenu, ContextMenu, Dialog, AlertDialog, Sheet (mobile), Popover, Command (cmdk), Toast (sonner, posisi bottom-left agar tidak menutupi panel transfer kanan-bawah), Progress, Badge, Skeleton, Separator, ScrollArea, Kbd, Breadcrumb (dengan dropdown sibling & collapse "…"), Table (header sticky).

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
| `/storage` | StorageAnalysisPage | S+P | |
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

Aksi yang tidak berlaku **disembunyikan**; item destruktif (Delete, Hapus permanen) selalu terakhir dengan warna `--danger`. Menu dapat dinavigasi keyboard (Radix), `Shift+F10`/tombol Menu membuka context menu pada item fokus.

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
| `Esc` | Batalkan seleksi / tutup overlay, dialog, menu | |
| `?` | Daftar shortcut | di luar input |
| `↑ ↓ ← → Home End PageUp PageDown` | Navigasi grid (roving tabindex) | `Shift` = rentang |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Tab folder berikut/sebelum | tidak dapat dicegat di beberapa browser → alternatif `Alt+1…9` pilih tab, `Alt+T` tab baru, `Alt+W` tutup tab |
| Editor: `Ctrl+S`, `Ctrl+Shift+S`, `Alt+W`, `Ctrl+G`, `F1`, `Ctrl+P`, `Ctrl+Shift+F`, `Shift+Alt+F` | save, save all, tutup tab, go to line, palette, quick open, find in files, format | |
| Viewer: `←/→`, `+/-`, `0` (fit), `1` (1:1), `R` (rotate), `F` (fullscreen), `Esc` | | |
| Video: `Space`, `←/→` 5 s, `J/K/L`, `F`, `M`, `↑/↓`, `0–9` | | |

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
- Format: `Intl.DateTimeFormat` (id-ID: `12 Okt 2026, 14.05`), relatif via `Intl.RelativeTimeFormat` (≤ 7 hari) + tooltip absolut; byte via helper `formatBytes(locale)` (id: `1,2 MB`, en: `1.2 MB`, basis 1024 dengan label KB/MB/GB); angka `Intl.NumberFormat`.
- Backend mengirim `message` sesuai `Accept-Language` memakai katalog kecil (`internal/httpapi/i18n`), tetapi client selalu mengutamakan terjemahannya sendiri per `code`.
- Semua `aria-label`, judul dialog, placeholder, tooltip berasal dari katalog.

### 9.9 Responsif & aksesibilitas

| Lebar | Perilaku |
| --- | --- |
| ≥ 1280 | layout penuh; panel preview inline |
| 768–1279 | sidebar ikon 48 px (tooltip, expand saat hover/klik), panel preview menjadi Sheet kanan, toolbar menampilkan 6 aksi utama + "⋯" |
| < 768 | sidebar Drawer (tombol ☰), address bar hanya breadcrumb terakhir + up, toolbar hanya [+] [Upload] [⋯], Details hanya Nama + Ukuran/Diubah (dua baris), view default List/Large, long-press 500 ms = context menu (Sheet bawah), mode seleksi (tap checkbox) dengan bottom bar aksi, viewer swipe prev/next, editor tetap bisa dibuka (read-only disarankan) |

Aksesibilitas (WCAG 2.2 AA): `FileGrid` = `role="grid"` dengan `row`/`gridcell`, roving tabindex (satu tabstop), `aria-selected`, `aria-sort` pada header, `aria-rowcount`; semua `IconButton` punya `aria-label`; fokus ring selalu terlihat (`:focus-visible` 2 px accent); dialog memakai Radix (focus trap, `aria-modal`); `aria-live="polite"` region global untuk hasil operasi ("3 file dipindahkan", "Upload selesai"), `assertive` untuk error; kontras diverifikasi script; target sentuh ≥ 24×24 (mobile 44); tidak ada informasi hanya lewat warna (tag punya nama di tooltip, status share punya ikon + teks); `prefers-reduced-motion`; audit `axe` di Playwright untuk 8 layar utama (Step 21).

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

CSP untuk `index.html` (nonce per request untuk script inline tema):

```text
Content-Security-Policy:
  default-src 'self';
  script-src 'self' 'nonce-<nonce>';
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
  manifest-src 'self';
  upgrade-insecure-requests
```

Catatan: `style-src 'unsafe-inline'` diperlukan karena Monaco, Radix (`style` attribute untuk posisi popover), dan Tailwind arbitrary animations menyuntikkan inline style; risiko CSS injection diterima (tidak memungkinkan eksekusi script). `worker-src blob:` karena Monaco membuat worker via blob URL ketika `MonacoEnvironment.getWorker` mengembalikan worker dari bundel (`?worker` Vite menghasilkan file terpisah → `'self'` cukup; `blob:` dipertahankan untuk pdf.js fake worker fallback; verifikasi saat implementasi dan hapus jika tidak perlu). Jika `CONTENT_BASE_URL` diisi: tambah origin itu ke `img-src`, `media-src`, `connect-src`.

Respons `raw/download/thumb/zip`: `Content-Security-Policy: sandbox; default-src 'none'` + header §8.9. Respons API JSON: `Content-Security-Policy: default-src 'none'`.

### 10.4 Kebijakan password dan sesi

- Password user: min `PASSWORD_MIN_LENGTH` (12), maks 128, bukan di daftar 100k umum, tidak mengandung username, tidak sama dengan sekarang/awal; tidak ada aturan komposisi (sesuai NIST); meter kekuatan di UI (zxcvbn-like ringan, opsional).
- Hash argon2id m=65536 KiB (64 MiB), t=3, p=1, salt 16 byte, output 32 byte; parameter disimpan di string PHC sehingga bisa dinaikkan (rehash saat login jika parameter lama < target). Di server 1,9 GB, 64 MiB per verifikasi dengan rate limit login 20/menit/IP aman.
- Password share: argon2id m=19456 KiB, t=2, p=1.
- Sesi: idle 2 h, absolut 168 h, maks 20 sesi aktif per user (yang tertua dicabut), cookie host-only, tanpa "remember me" terpisah (absolut 7 hari sudah cukup).
- Password sementara admin-generated: 16 karakter dari `A-Za-z0-9` + 2 simbol, ditampilkan sekali, `must_change_password=1`.
- TOTP (fase akhir): secret 20 byte, SHA-1 6 digit 30 s, window ±1, recovery codes 10 × 10 karakter (hash), rate limit 5/menit.

### 10.5 Rate limit

| Key | Batas | Respons |
| --- | --- | --- |
| Login per IP | 20 / menit, burst 5 | 429 + `Retry-After` |
| Login per username | lockout 5 gagal/15 menit → 1/5/15/60 menit | 423 |
| Ganti password / reset | 5 / 15 menit / sesi | 429 |
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

`internal/download/remote_test.go`: `httptest.Server` lokal (loopback) sebagai target → harus `SSRF_BLOCKED` (karena 127.0.0.1) **kecuali** test menyuntikkan `allowLoopbackForTest=true` untuk menguji logika download; resolver di-mock (`Resolver` interface) untuk: hostname → `10.0.0.5`; hostname → `[2001:db8::1, 93.184.216.34]` (satu privat → tolak); rebinding (`[93.184.216.34]` lalu `[127.0.0.1]`) → dial tetap ke IP pertama (verifikasi lewat `DialContext` yang dicatat); redirect chain 6 → gagal; redirect ke `file://` → gagal; `Content-Length` > batas → gagal sebelum baca; tanpa `Content-Length` melewati batas → gagal + part dihapus; resume Range setelah putus (server mengirim 206).

### 11.4 Test IDOR dan permission

`testenv` membuat A, B, admin; untuk setiap resource bertipe (file path, task, version, trash item, share, recipient, upload session, notification, folder_view_prefs) dibuat oleh A, uji B: GET/PATCH/DELETE → 403/404 (nilai yang konsisten: `NOT_FOUND` untuk id acak, `FORBIDDEN` jika id valid tapi bukan milik — pilih **`NOT_FOUND` untuk keduanya** agar tidak membocorkan keberadaan). Share mode users: B viewer → tulis 403; B editor → tulis OK tetapi `path=/../x` → 400; admin tanpa flag → 403 pada `as_user`.

### 11.5 Performance 10.000 file

`genfolder` membuat `/Besar` dengan 10.000 file (nama bervariasi: angka, unicode, huruf besar/kecil) + 500 folder; benchmark `listing.List` dingin & cache; integration test mengukur p95 dari 50 request `files/list` < 500 ms (di CI toleransi 1 s); Playwright: buka folder, ukur `performance.measure('first-rows')` < 1 s, scroll ke bawah tanpa frame drop > 50 ms (trace), sort kolom < 300 ms (cache).

### 11.6 Coverage dan gerbang CI

`ci.yml`: job `backend` (lint, sqlc diff, test -race, cover-check, govulncheck), `web` (lint, typecheck, test, check:contrast, build, bundle budget `size-limit`: entry JS < 300 KB gzip), `openapi` (lint + route-coverage test), `e2e` (build binary, Playwright Chromium, artefak trace saat gagal), `release` (tag → build tar.gz). Ambang coverage: `fsjail` ≥ 90 %, `auth`, `share`, `archive`, `download` (signer/remote) ≥ 80 %, total backend ≥ 70 %. Test yang butuh CLI eksternal memakai `t.Skip` jika binary tidak ada dan CI memasang `7zip ffmpeg libvips-tools poppler-utils ripgrep` via apt (ubuntu-latest).

---

## §12 Deployment

Target: Ubuntu 24.04 + aaPanel 8.0.7 di server ini; binary + systemd; Nginx aaPanel sebagai reverse proxy untuk `https://file.kppbwi.app`. Langkah lengkap klik-per-klik ditulis ulang di `docs/DEPLOY-AAPANEL.md` (Step 22) berdasarkan bagian ini.

### 12.1 `deploy/install.sh` (idempoten, dijalankan `sudo bash deploy/install.sh` dari APP_DIR)

```text
 0. set -euo pipefail; APP_DIR=/www/wwwroot/file.kppbwi.app; STORAGE=/www/storage/FileManager; SVC_USER=filemanager
 1. Cek root; cek /etc/os-release Ubuntu 22.04|24.04 (lain → tanya lanjut y/N).
 2. Cek swap: SwapTotal < 2 GB → cetak peringatan + perintah membuat swapfile 2 GB (fallocate, mkswap, swapon, fstab); tidak otomatis.
 3. apt-get update; apt-get install -y ffmpeg libvips-tools poppler-utils ripgrep 7zip
    - verifikasi binary: /usr/bin/7zz (paket 7zip; pada Ubuntu 22.04 paket mungkin bernama 7zip juga, verifikasi; fallback p7zip-full → /usr/bin/7z dan set SEVENZIP_PATH)
    - unrar: hanya jika --with-unrar (multiverse, non-free): add-apt-repository multiverse; apt-get install unrar → UNRAR_PATH=/usr/bin/unrar
    - ionice ada di util-linux (sudah ada)
 4. Go: jika `go version` tidak ada atau < 1.25.12 (CVE `os.Root`) → unduh tarball resmi go.dev untuk linux-amd64 (versi stable terbaru; checksum diverifikasi dari halaman unduhan — tulis URL dan sha256 di script saat implementasi) ke /usr/local/go; tambah /usr/local/go/bin ke /etc/profile.d/go.sh. Dilewati jika --no-build (binary dari CI).
 5. Node: cek node ≥ 22 (server: 24); corepack enable; corepack prepare pnpm@<stable> --activate. Dilewati jika --no-build.
 6. useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin filemanager (jika belum ada).
 7. mkdir -p $STORAGE; chown filemanager:filemanager $STORAGE; chmod 0750 $STORAGE
    - jika owner sebelumnya www:www (kondisi server sekarang) → chown -R hanya jika folder kosong; jika berisi, tanya.
    - mkdir -p $STORAGE/.system/{db,trash,versions,uploads,thumbs,tmp,logs,backups,deleted-users}; chown -R filemanager; chmod 0750
 8. $APP_DIR: chown -R filemanager:filemanager $APP_DIR kecuali .git (tetap pemilik yang menjalankan git); mkdir -p $APP_DIR/public/.well-known; chmod 0755 $APP_DIR/public
    - hapus file default aaPanel jika ada: index.html, 404.html, .htaccess di $APP_DIR dan $APP_DIR/public; jika ada .user.ini → chattr -i lalu rm
 9. .env: jika belum ada → cp .env.example .env; isi APP_SECRET=$(openssl rand -hex 32) jika kosong; minta ADMIN_INITIAL_PASSWORD jika CHANGE_ME (baca dari stdin tanpa echo, bukan argumen); chown filemanager; chmod 0600
10. Build (jika tidak --no-build): su filemanager -c "make build" (butuh RAM; lihat langkah 2) atau --binary <path> untuk memasang binary hasil CI ke bin/filemanager (chmod 0755)
11. bin/filemanager check-deps (mencetak tabel tool) dan bin/filemanager migrate up (sebagai filemanager)
12. cp deploy/filemanager.service /etc/systemd/system/; systemctl daemon-reload; systemctl enable --now filemanager; tunggu /readyz di 127.0.0.1:8080 (maks 30 s)
13. Cetak checklist aaPanel (12.3) + perintah smoke test; ingatkan menghapus ADMIN_INITIAL_PASSWORD dari .env setelah login pertama (atau biarkan: hanya warning)
```

Flag: `--no-build`, `--binary <path>`, `--with-unrar`, `--skip-apt`, `--yes`.

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
ExecReload=/bin/kill -HUP $MAINPID
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
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
RestrictRealtime=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=false
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
SystemCallArchitectures=native
CapabilityBoundingSet=
AmbientCapabilities=
# Opsional — aktifkan HANYA jika REMOTE_DOWNLOAD_ENABLED=false (remote download butuh koneksi keluar):
#IPAddressDeny=any
#IPAddressAllow=localhost

[Install]
WantedBy=multi-user.target
```

Catatan: `MemoryDenyWriteExecute=false` karena ffmpeg/libvips dapat memakai JIT/`mprotect` (verifikasi; jika aman, set true). `ProtectSystem=strict` membuat `/www/wwwroot/file.kppbwi.app` read-only bagi proses (benar: aplikasi tidak menulis ke APP_DIR). `.env` dibaca systemd sebagai root → alternatif lebih ketat `chown root:root .env` tetap bekerja (dicatat sebagai opsi). `TMPDIR` diarahkan ke `.system/tmp` agar CLI tidak menulis ke `/tmp` privat yang kecil.

### 12.3 Langkah aaPanel klik-per-klik

Jalur utama — **tipe proyek Reverse Proxy** (terverifikasi ada di aaPanel 8.0.7 server ini; nama menu persis perlu verifikasi saat Step 22):

1. Panel aaPanel → **Website** → tab/kategori **Reverse Proxy** (atau "Proxy project") → **Add** (perlu verifikasi nama tombol).
2. Domain: `file.kppbwi.app`. Target/`proxy_pass`: `http://127.0.0.1:8080`. Send domain/`proxy_host`: `$http_host`. Path: `/`. Remark: `FileManager`. Simpan.
3. Setelah dibuat, aaPanel menulis `/www/server/panel/vhost/nginx/file.kppbwi.app.conf` dengan `root /www/wwwroot/file.kppbwi.app` (root diisi otomatis; verifikasi). Karena semua `location /` di-proxy, Nginx tidak menyajikan file apa pun dari root kecuali `.well-known` (via include Lua aaPanel). Jika UI memungkinkan mengatur site directory → set ke `/www/wwwroot/file.kppbwi.app/public`.
4. Buka site → **Config** (editor konfigurasi Nginx site; nama menu perlu verifikasi) → tempel `deploy/nginx-aapanel.conf`: **Bagian A** di dalam blok `location /` proxy milik aaPanel (menggantikan/menambah direktif `proxy_*`), **Bagian B** sebagai blok `location` tambahan di level `server {}` tepat **sebelum** blok proxy itu (lihat 12.4); simpan — aaPanel menjalankan `nginx -t` dan reload. Verifikasi dengan `nginx -T | grep -A3 'location /'` bahwa direktif bertahan.
5. Site → **SSL** → **Let's Encrypt** → pilih domain → verifikasi file (HTTP-01; `.well-known/acme-challenge` dilayani dari document root oleh aaPanel) → Apply; aktifkan **Force HTTPS**.
6. Verifikasi: `curl -I https://file.kppbwi.app/healthz` → 200 (`{"status":"ok"}`, HSTS ada); `curl -I https://file.kppbwi.app/readyz` → 404 (sengaja tidak di-proxy); `curl -I https://file.kppbwi.app/` → 200 dengan header aplikasi; `curl https://file.kppbwi.app/.env` dan `/bin/filemanager` → 404; `curl http://file.kppbwi.app/.well-known/acme-challenge/test` (file uji di `public/.well-known/acme-challenge/`) → 200 dari Nginx, bukan dari aplikasi.
7. Firewall: jangan membuka 8080 (UFW/aaPanel Security). Sudah tertutup saat ini.

Jalur alternatif (persis seperti teks prompt): **Website → PHP project → Add site**: domain `file.kppbwi.app`, site directory `/www/wwwroot/file.kppbwi.app`, PHP version **Static**, tanpa DB/FTP → Site → **Site directory** → Running directory `/public` → Site → **Reverse proxy** → Add: name `app`, target `http://127.0.0.1:8080`, send domain `$host`, proxy directory `/` → Config → tempel snippet → SSL → Force HTTPS. Hapus `index.html`, `404.html`, `.htaccess`, `.user.ini` (`chattr -i` dulu) yang dibuat aaPanel.

Larangan: jangan mengedit `/www/server/panel/vhost/nginx/file.kppbwi.app.conf` langsung (ditulis ulang aaPanel saat mengubah SSL/proxy). Snippet kustom hanya lewat editor Config di UI (yang juga menulis file itu, tetapi aaPanel menjaga blok yang ditandai komentar; verifikasi bahwa blok kustom bertahan setelah mengubah SSL — jika tidak, letakkan di `extension/<domain>/filemanager.conf`? folder itu dikelola aaPanel (berisi `site_total.conf`), perlu verifikasi apakah file tambahan bertahan; dokumentasikan hasil verifikasi di `DEPLOY-AAPANEL.md`).

Otomasi opsional (fase akhir): `deploy/aapanel/site.py` meniru pola `proxy.kppbwi.app` (`mod.project.proxy.comMod`) untuk `status|create|ssl|force-https`; tetap dianggap "perlu verifikasi" per versi panel.

### 12.4 `deploy/nginx-aapanel.conf`

```nginx
# ===== FileManager — tempel di konfigurasi site file.kppbwi.app (aaPanel → Website → site → Config) =====
# Bagian A: direktif di dalam blok  location / { ... proxy_pass http://127.0.0.1:8080; ... }  milik aaPanel.
# Tambahkan/ganti baris berikut di dalam blok itu (aaPanel sudah menulis proxy_pass, proxy_http_version 1.1,
# dan header Upgrade/Connection untuk websocket):
    proxy_set_header Host              $host;
    proxy_set_header X-Real-IP         $remote_addr;
    proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-Host  $host;
    proxy_http_version 1.1;
    proxy_set_header Connection        "";
    # upload tus: badan request dialirkan langsung, tidak ditampung di disk Nginx
    proxy_request_buffering off;
    client_max_body_size      64m;          # >= UPLOAD_CHUNK_SIZE (16 MB) + overhead; multipart clipboard 25 MB
    client_body_timeout       300s;
    # SSE & download besar: tanpa buffering, timeout panjang
    proxy_buffering           off;
    proxy_cache               off;
    proxy_read_timeout        3600s;
    proxy_send_timeout        3600s;
    proxy_connect_timeout     10s;
    proxy_max_temp_file_size  0;
    # aplikasi mengirim header keamanan sendiri; jangan tambahkan add_header di sini (add_header di level
    # location menimpa add_header level server milik aaPanel, mis. HSTS).

# Bagian B: blok location tambahan, letakkan SEBELUM blok  location /  proxy (urutan prefix tidak penting,
# tetapi regex location harus berada di dalam server {} dan tidak di dalam location lain).
    # /healthz boleh publik (hanya {"status":"ok"}, dipakai uptime monitor & smoke test);
    # readyz/metrics hanya untuk loopback (systemd/update.sh); tidak diekspos ke internet
    location = /readyz  { return 404; }
    location = /metrics { return 404; }
    # defense-in-depth: walau semua di-proxy, pastikan source/binary/env tidak pernah disajikan statis
    location ~ ^/(\.env|\.git|backend|web|deploy|docs|bin|Makefile|PRD\.md|SPEC\.md|PROMPT\.md|PROGRESS\.md|promp-master\.md) { return 404; }
    # dotfile selain .well-known
    location ~ /\.(?!well-known/) { return 404; }
# ===== akhir snippet =====
```

Catatan: aaPanel sudah menambahkan `location ~ \.well-known` / include Lua untuk ACME sebelum blok proxy; snippet tidak boleh mengganggunya. Gzip: aktifkan di aaPanel (site → Config/GZIP) untuk `application/json text/css application/javascript` — opsional, aplikasi juga bisa gzip sendiri (middleware) → **keputusan**: aplikasi mengompresi JSON & aset statis sendiri (`chi/middleware.Compress` level 5, kecuali respons streaming/raw), Nginx gzip dibiarkan default aaPanel.

### 12.5 `deploy/update.sh` dan rollback

```text
 1. Prasyarat: git pull (atau --binary/--release <tar.gz>), sebagai filemanager; cek /readyz saat ini OK.
 2. Backup: bin/filemanager backup-db → .system/backups/app-<ts>-preupdate.db; salin bin/filemanager → bin/filemanager.prev
 3. Build (make build) atau ekstrak release ke bin/filemanager.new; bin/filemanager.new version
 4. Migrasi: bin/filemanager.new migrate status; migrate up (goose; migrasi harus backward-compatible satu versi: tambah kolom nullable, jangan drop di rilis yang sama)
 5. mv bin/filemanager.new bin/filemanager; systemctl restart filemanager
 6. Tunggu /readyz (maks 30 s) + smoke (login API dengan akun smoke opsional) → sukses: hapus .prev lama > 3 versi; cetak versi
 7. Gagal: mv bin/filemanager.prev bin/filemanager; migrate down ke versi sebelum (jika migrasi tidak kompatibel, pulihkan DB dari app-<ts>-preupdate.db saat service berhenti); systemctl restart; cetak ROLLBACK + log journalctl 50 baris; exit 1
```

Release dari CI: `tar.gz` berisi `bin/filemanager`, `deploy/`, `docs/`; `update.sh --release <file>` memverifikasi `sha256sum`.

### 12.6 Backup dan restore

- **Storage** (`/www/storage/FileManager`): backup lewat aaPanel (Cron → Backup directory) atau rsync/restic ke lokasi lain, harian; **kecualikan** `.system/tmp`, `.system/uploads`, `.system/thumbs`. Catatan: aaPanel menjalankan backup sebagai root; folder 0750 milik `filemanager` tetap terbaca root.
- **Database**: job `backup_db` 02:30 harian → `VACUUM INTO '.system/backups/app-<yyyyMMdd-HHmm>.db'`, keep 7 (+ label manual tidak dihapus); ikut dalam backup storage karena berada di `.system`.
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

### ADR-01 Backend Go ≥ 1.25 + `net/http` + chi, CGO_ENABLED=0
**Konteks.** Butuh streaming file besar, HTTP Range, concurrency task, binary tunggal, memori kecil, dan akses filesystem yang tahan traversal/symlink. Server hanya 1,9 GB RAM; apt menyediakan Go 1.22. **Keputusan.** Go ≥ 1.25 pada patch terbaru (minimal 1.25.12: CVE-2026-32282 dan CVE-2026-39822 pada `os.Root`; `os.Root` dengan `Rename/RemoveAll/MkdirAll/Symlink`), dipasang dari tarball go.dev; router `chi` (ringan, middleware idiomatik, `chi.Walk` untuk audit route); module `kppbwi.app/filemanager`; `CGO_ENABLED=0`. **Konsekuensi.** Binary statis mudah di-deploy/rollback; tidak ada cgo → SQLite pure-Go dan libvips via CLI (ADR-02, ADR-09); `install.sh` mengelola Go sendiri.

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
**Konteks.** Brief meminta memilih Vidstack atau Plyr. Verifikasi registry npm pada 14 Okt 2026: `@vidstack/react` (latest 1.15.7) membawa notice *deprecated* — "security fixes only until January 2028"; `plyr` (latest 3.8.5) juga *deprecated* — "receives security updates only. Use Video.js 10 for new projects". `video.js` latest 10.1.0; roadmap resminya menyebut v10 "stable and recommended for new projects", punya paket React resmi `@videojs/react` (dokumen instalasi resmi) dan panduan migrasi dari Vidstack. Alternatif lain: `media-chrome` (Mux, MIT, web components, latest 4.x) — hanya kontrol UI di atas `<video>` native. **Keputusan.** Video.js 10 via `@videojs/react` (lisensi paket 10.x: verifikasi, diharapkan Apache-2.0) di balik komponen pembungkus `VideoPlayer` milik aplikasi; jika saat Step 17b `@videojs/react` ternyata belum stabil (dist-tag, isu kritis terbuka) → media-chrome dengan pembungkus yang sama. Memakai library yang sudah deprecated hanya demi mematuhi brief dinilai keputusan buruk (tanpa perbaikan bug sampai 2028); deviasi ini dicatat di PRD A25. **Konsekuensi.** Lazy-load chunk player; playlist, resume posisi, penyegaran signed URL, dan deteksi codec diimplementasikan di pembungkus, bukan bergantung library; subtitle VTT, PiP, keyboard memakai fitur engine; verifikasi ulang status paket wajib di Step 17b.

### ADR-08 Password arsip tidak lewat argv bila mungkin
**Konteks.** `7zz -p<password>`, `unrar -p<password>`, dan `rar -p<password>` menampakkan password di `ps`/`/proc/<pid>/cmdline` bagi user Linux lain di server yang sama (`/proc` di-mount tanpa `hidepid`). Verifikasi 14 Okt 2026: README `mholt/archives` menyatakan dapat **membaca** 7z dan RAR ber-password (tanpa proses eksternal) dan tidak menyebut zip terenkripsi; `yeka/zip` (fork `archive/zip`, MIT) mendukung ZipCrypto + AES-128/192/256 baca/tulis tetapi tanpa rilis bertag — maintenance perlu verifikasi. Opsi untuk kasus yang tersisa (membuat 7z ber-password): (1) `hidepid=2` pada `/proc` — menyembunyikan proses user lain, tetapi mengubah sistem di luar aplikasi dan dapat mengganggu pemantauan proses aaPanel (perlu verifikasi); (2) password via stdin untuk `7zz` (`-p` tanpa nilai; dukungan pipe non-tty perlu verifikasi lewat spike di Step 14a); (3) argv dengan risiko tercatat. **Keputusan.** (a) Zip ber-password: baca & tulis lewat library Go `internal/archive/zipaes` (kandidat `yeka/zip`; jika tidak layak, implementasi WinZip AES (PBKDF2-HMAC-SHA1, AES-CTR, HMAC-SHA1) + ZipCrypto decrypt dengan test vector terhadap `7zz`). (b) Membaca/extract/listing 7z dan rar ber-password: `mholt/archives` — password tidak pernah keluar proses; `unrar`/`7zz x` hanya fallback bila library gagal pada arsip tertentu (risiko argv berlaku). (c) Membuat 7z ber-password: `7zz a` — spike stdin dulu; jika tidak didukung → argv, risiko dicatat di `SECURITY.md`, dialog compress menampilkan catatan singkat bahwa password 7z dapat terlihat proses lain di server, dan `hidepid=2` direkomendasikan sebagai hardening opsional di `DEPLOY-AAPANEL.md`; pemilik dapat memilih menunda fitur ini (PRD Q6). Keputusan pemilik (14 Okt 2026): tidak ada lisensi `rar`, `unrar` tidak dipasang, dan risiko argv untuk pembuatan 7z ber-password diterima bila stdin tidak didukung. **Konsekuensi.** Permukaan argv menyusut ke satu kasus (membuat 7z ber-password); semua CLI lewat `internal/exec` (argv, `--`, timeout, nice/ionice, stdin opsional, tanpa shell); `hidepid=2` tidak dipasang otomatis.

### ADR-09 Thumbnail via CLI (vipsthumbnail, ffmpeg, pdftoppm), bukan govips
**Konteks.** govips butuh cgo + libvips dev headers; server kecil. **Keputusan.** CLI dengan argv, timeout, `nice`, worker pool `max(1, CPU−1)`, cache WebP, antrian prioritas viewport. **Konsekuensi.** Overhead spawn per thumbnail (~20–50 ms) diterima; tanpa cgo; dependency opsional (fitur degradasi anggun dengan `DEPENDENCY_MISSING`); di 1 vCPU thumbnail folder besar lambat → prioritas viewport & placeholder.

### ADR-10 Media metadata via ffprobe + decoder Go + EXIF library
**Konteks.** Kolom dimensi/durasi, info viewer, tag audio. **Keputusan.** `ffprobe -print_format json -show_format -show_streams` untuk audio/video; `image.DecodeConfig` (+ `x/image/webp`, `vipsheader` untuk tiff/heic/avif) untuk gambar; EXIF via library Go (kandidat `dsoprea/go-exif`, verifikasi); cache `media_metadata` keyed size+mtime. **Konsekuensi.** Satu tool untuk audio/video (ffprobe) mengurangi dependency; GPS disembunyikan default.

### ADR-11 Sort server-side + natural collation + cursor opaque
**Konteks.** Folder 10k item, paging, sort benar lintas halaman. **Keputusan.** Listing penuh di server (ReadDir+Lstat), sort dengan komparator natural kustom (run digit numerik) + `x/text/collate` (`IgnoreCase`, locale user) — bukan `collate.Numeric` karena bug golang/go#67997 (§8.3), cache 15 s per (user,path,sort) dengan invalidasi mutasi, cursor berisi kunci sort item terakhir (stabil). **Konsekuensi.** Memori cache dibatasi 64 MB LRU; perubahan eksternal terlihat ≤ 15 s; client tidak perlu sort sendiri.

### ADR-12 Search: walk nama + ripgrep isi + fallback Go
**Konteks.** Pencarian nama & isi dengan regex, cepat, aman dari ReDoS. **Keputusan.** Nama: walk `os.Root.FS()` dengan batas 5.000/20 s; isi: `rg --json` (Rust regex, tanpa backtracking) dengan timeout, `--no-follow`, `--no-ignore --hidden`; fallback worker Go `regexp` (RE2). Replace = task dengan preview + versi pre-image. **Konsekuensi.** Tanpa indeks (FTS) di v1 → pencarian global berulang memakan I/O; kandidat peningkatan: indeks nama SQLite FTS5 (post-v1).

### ADR-13 Sesi server-side + CSRF token
**Konteks.** Sesi harus dapat dicabut (admin, ganti password); cookie `SameSite=Lax` saja tidak cukup untuk semua browser lama. **Keputusan.** Sesi di DB (hash token), cookie `HttpOnly Secure SameSite=Lax`, idle/absolut timeout; CSRF = HMAC(csrf_secret, session_id) di header + cek `Sec-Fetch-Site`/`Origin`. **Konsekuensi.** Satu query DB per request (cache user 5 s); SPA harus mengambil token setelah login; tus/multipart ikut membawa header.

### ADR-14 argon2id + daftar password umum + lockout bertahap
**Konteks.** Tanpa 2FA wajib; server kecil. **Keputusan.** argon2id 64 MiB/t=3/p=1 (login), 19 MiB/t=2/p=1 (share); min 12 karakter; 100k daftar umum; lockout 1/5/15/60 menit; rate limit IP. **Konsekuensi.** Verifikasi ±100–200 ms di 1 vCPU; aman dengan rate limit; parameter bisa dinaikkan via rehash saat login.

### ADR-15 Signed URL HMAC dengan TTL berbeda per tujuan
**Konteks.** `<video>`, `<img>`, download manager, dan tab baru tidak selalu membawa cookie/CSRF; Range request berulang sepanjang playback. **Keputusan.** HMAC-SHA256 (kunci HKDF) atas scope|path|purpose|exp; TTL 5 menit untuk download/thumb, 2 jam untuk media; player menyegarkan URL saat 403. **Konsekuensi.** Link download yang bocor hanya hidup 5 menit; pencabutan sesi tidak membatalkan URL yang sedang hidup (diterima); pindah ke origin konten terpisah tidak mengubah mekanisme.

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

### ADR-21 Frontend: React + TS + Vite + TanStack + Zustand + Tailwind/shadcn
**Konteks.** UI padat, tabel virtual 10k baris, komponen accessible yang bisa dikustom, i18n. **Keputusan.** Stack sesuai prompt; lucide-react (ISC) untuk ikon; sonner; cmdk; react-hook-form + zod; i18next; `marked` + DOMPurify; pdfjs-dist; font self-host; pnpm via corepack; tanpa CDN/Google Fonts (CSP `'self'`). **Konsekuensi.** Bundle awal < 300 KB gzip dengan code-splitting per route dan lazy Monaco/pdf.js/Video.js; `shadcn/ui` di-vendor ke repo (bukan dependency) sehingga bisa diubah ke token.

### ADR-22 Build & CI: embed frontend, build di CI, GitHub Actions (asumsi)
**Konteks.** RAM server 1,9 GB (+ swap 2,8 GB); belum ada remote git (keputusan pemilik 14 Okt 2026: repo lokal, build di server). **Keputusan.** `make build` menghasilkan satu binary dengan `embed.FS`; CI (GitHub Actions diasumsikan; workflow ditulis agar mudah dipindah ke GitLab CI/Gitea Actions) menjalankan lint/test/vuln/e2e dan merilis `tar.gz`; `install.sh --binary` dan `update.sh --release` memasang hasil CI; build di server tetap bisa dengan swap ≥ 2 GB. **Konsekuensi.** Rollback = ganti binary; pipeline butuh apt tool untuk test CLI; jika hosting bukan GitHub, Step 1 menyesuaikan file workflow.

### ADR-23 Kuota inkremental + rekalkulasi terjadwal; kapasitas disk nyata di dashboard
**Konteks.** Walk seluruh storage mahal; drift bisa terjadi (SSH, crash). **Keputusan.** `quota_usage` diperbarui dalam transaksi tiap mutasi; rekalkulasi malam hari + saat `dirty` + aksi admin; semua writer memeriksa `statfs` free ≥ 512 MB; dashboard menampilkan Σ kuota vs disk (server sekarang: sisa ±6 GB, `DEFAULT_USER_QUOTA=10GB` tidak realistis untuk banyak user → peringatan). **Konsekuensi.** Kuota akurat dalam ±1 % di antara rekalkulasi; `install.sh` memperingatkan kuota default terhadap ruang bebas.
