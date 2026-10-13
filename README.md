# FileManager — file.kppbwi.app

Web file manager multi-user self-hosted (Go + React) untuk satu server Ubuntu yang dikelola aaPanel.
Setiap user punya root folder sendiri di `/www/storage/FileManager/<username>`; fitur setara file manager
aaPanel ditambah editor kode, viewer media, arsip, remote download, dan sharing.

| Dokumen | Isi |
| --- | --- |
| [`PRD.md`](PRD.md) | Apa & kenapa: tujuan, persona, epic/user story, risiko, asumsi |
| [`SPEC.md`](SPEC.md) | Bagaimana: arsitektur, konfigurasi, model data, API, keamanan, deployment, ADR |
| [`PROMPT.md`](PROMPT.md) | Urutan step implementasi (satu step per sesi AI) |
| [`PROGRESS.md`](PROGRESS.md) | Checklist & jurnal step |
| `docs/DEPLOY-AAPANEL.md` | Langkah deploy klik-per-klik di aaPanel (Step 22) |
| [`docs/SECURITY.md`](docs/SECURITY.md) | Kontrol keamanan yang sudah ada |
| [`docs/openapi.yaml`](docs/openapi.yaml) | Kontrak API |

## Struktur

```text
backend/   Go module kppbwi.app/filemanager (cmd/filemanager, internal/*)
web/       React + TypeScript + Vite (pnpm)
deploy/    install.sh, update.sh, systemd unit, snippet Nginx aaPanel (Step 22)
docs/      SPEC pendukung, openapi.yaml, SECURITY.md
public/    document root site aaPanel: hanya .well-known/
bin/       hasil build: bin/filemanager (frontend ter-embed)
```

## Prasyarat

- **Go ≥ 1.25 patch terbaru** (minimal 1.25.12; di server ini terpasang 1.27.2 di `/usr/local/go`, dipasang dari
  tarball resmi go.dev karena apt hanya menyediakan 1.22 — lihat `/etc/profile.d/go.sh`).
- **Node ≥ 22** + pnpm via corepack (`corepack enable && corepack prepare pnpm@latest --activate`).
- `golangci-lint` (binary prebuilt dari rilis GitHub ke `/usr/local/bin`), `govulncheck` (`go install golang.org/x/vuln/cmd/govulncheck@latest`).
- `sqlc` (binary prebuilt dari rilis GitHub `sqlc-dev/sqlc` ke `/usr/local/bin`; dipakai `make sqlc` untuk meregenerasi `backend/internal/db/sqlc` — hasilnya di-commit dan CI memeriksa dengan `git diff --exit-code`).
- Tool runtime opsional (ffmpeg, libvips-tools, poppler-utils, ripgrep, 7zip) dipasang oleh `deploy/install.sh`; `bin/filemanager check-deps` menampilkan statusnya.

Server produksi hanya 1 vCPU / 1,9 GB RAM: `Makefile` memakai `GOMAXPROCS=1`; jalankan build dan test secara serial.

## Perintah

```bash
make deps        # pnpm install + go mod download
make dev         # backend (APP_ENV=development, data di ./.devdata) + Vite dev server (proxy /api → 8080)
make lint        # golangci-lint, eslint, tsc, prettier
make sqlc        # regenerasi query type-safe (setelah mengubah migrations/ atau internal/db/queries/)
make migrate ARGS=status|up|down   # goose migrations memakai .env (DATA_DIR/db/app.db)
make test        # go test -race -cover + vitest
make build       # pnpm build → embed → bin/filemanager
make check-deps  # tabel tool eksternal (memakai .env)
make ci          # lint + test + build + openapi-lint (sama dengan CI)
```

Menjalankan binary secara lokal:

```bash
APP_SECRET=$(openssl rand -hex 32) ALLOW_ROOT=true ./bin/filemanager serve --env-file .env
curl -i http://127.0.0.1:8080/healthz
```

`--env-file` hanya untuk pengembangan; di produksi systemd memakai `EnvironmentFile=.env`
(catatan: systemd tidak mendukung komentar di belakang nilai pada baris yang sama — `install.sh` menulis `.env` bersih).
`ALLOW_ROOT=true` hanya untuk uji lokal; service produksi berjalan sebagai user `filemanager`.

## Konfigurasi

Salin `.env.example` ke `.env` (chmod 600) dan isi `APP_SECRET` (`openssl rand -hex 32`) serta password admin awal.
Daftar lengkap variabel, default, dan validasi: `SPEC.md §3`.

## CI

Hosting git belum ditentukan (repo lokal di server). `.github/workflows/ci.yml` ditulis untuk GitHub Actions dan
mudah dipindah ke GitLab CI/Gitea Actions; sampai remote tersedia, `make ci` menjalankan pemeriksaan yang sama secara lokal.

## Frontend (web/)

- `pnpm -C web dev` — Vite dev server on :5173, proxying `/api` to the backend on :8080.
- `pnpm -C web lint` — eslint + WCAG contrast check (`scripts/check-contrast.mjs`) + i18n key check.
- `pnpm -C web test` — Vitest; `pnpm -C web e2e` — Playwright against `bin/filemanager` (run `make build` first; browsers via `pnpm -C web e2e:install`).
- `pnpm -C web gen:api` — regenerate `src/lib/api.gen.ts` from `docs/openapi.yaml`.
- `/dev/design` shows every token and component (dev builds only, or `VITE_DESIGN_PAGE=1 pnpm build`).
