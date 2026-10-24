# PERF.md — Performa File Manager (Step 21)

| | |
| --- | --- |
| Acuan | SPEC §1.6, §8.3, §8.4, §11.5 · PRD §2 (G3, G4, G5, G6), §8 · PROMPT Step 21 |
| Diukur | 24 Oktober 2026, ±22.00–23.00 WIB, commit kerja Step 21 di atas `26c1913` |
| Perintah | `make perf` (backend) · `pnpm -C web e2e perf.spec.ts` (frontend) · `pnpm -C web build` (bundle) |

## 1. Ringkasan terhadap target

| Target (sumber) | Batas | Semua CPU (4) | CPU 0 saja (target 1 vCPU) | Status |
| --- | --- | --- | --- | --- |
| `files/list` 10.000+ entri, cache dingin, p95 (PRD G3, SPEC §8.3) | < 500 ms | **140 ms** (p50 91, p99 155) | **187 ms** (p50 106, p99 216) | ✅ |
| `files/list` cache hangat, p95 (PROMPT 21; SPEC §8.3 < 50 ms) | < 100 ms | **10 ms** | **11 ms** | ✅ |
| Navigasi berulang folder yang sudah dibuka, p95 (API) | — | 7,5 ms | 12,7 ms | ✅ |
| Idle RSS (PRD G6, SPEC §1.6) | < 100 MB | **30,5 MB** setelah login · 77 MB setelah semua beban | **26,2 MB** · 74 MB | ✅ |
| Upload 1 GiB: puncak RSS (PRD G5, PROMPT 21) | ≤ baseline + 100 MB | +0,4 MB | +0,1 MB | ✅ |
| Zip streaming 1 GiB: puncak RSS | ≤ baseline + 100 MB | +0,4 MB | +1,2 MB | ✅ |
| Extract 1 GiB: puncak RSS | ≤ baseline + 100 MB | +1,1 MB | +1,2 MB | ✅ |
| Render pertama folder 10.500 entri di UI (PRD G3) | < 1 s | **205–273 ms** (Chromium headless, server yang sama) | — (browser = perangkat klien) | ✅ |
| Navigasi hangat di UI, p95 (PRD G4) | < 150 ms | **86–100 ms** | — | ✅ |
| Long task saat scroll 10.500 baris (PROMPT 21 AC) | ≤ 100 ms | **maks 62–85 ms** (5–12 task > 50 ms dalam ±21 s scroll agresif) | — | ✅ (sasaran < 50 ms: sebagian, §6) |
| Sort 10.000 entri dari server sampai baris pertama (SPEC §11.5) | < 300 ms | 171–234 ms | — | ✅ |
| JS awal (PRD G6) | < 300 KB gzip | **220,2 KB** (20 chunk) | — | ✅ (gagal keras di CI bila ≥ 300 KB) |

Sebelum optimasi Step 21 (binary `26c1913`, harness yang sama): list dingin p95 **287 ms** (4 CPU) / **400 ms** (CPU 0) dan idle RSS setelah login **156 MB / 219 MB** — target idle < 100 MB **gagal** sebelum Step 21.

## 2. Lingkungan

- **Mesin dev = server produksi** (satu mesin, Ubuntu 24.04, kernel 6.8, ext4 di `/dev/vda1`): `nproc` = **4** (Intel Broadwell 2,0 GHz, KVM), **5,9 GB RAM** (`free -m`: 5.878 MB), swap 2,8 GB. PROMPT/SPEC §1.6 menulis 1 vCPU / 1,9 GB — itu kondisi saat perencanaan; server di-upgrade 15 Oktober 2026. Target asli disusun untuk 1 vCPU, karena itu setiap angka backend diukur dua kali: semua CPU, dan binary dipin ke **CPU 0** (`taskset -c 0`, `GOMAXPROCS=1` → `THUMB_WORKERS=1`, `TASK_MAX_GLOBAL=2`) dengan generator beban di CPU 1–3 (seperti klien jarak jauh).
- Layanan `filemanager` belum dideploy (systemd inactive), jadi pengukuran tidak mengganggu pengguna; aaPanel, Nginx, MySQL dan proyek lain tetap berjalan (load average ±0,2 sebelum mulai). Pengukuran **tidak** menyentuh `/www/storage/FileManager`, `.env`, systemd, Nginx, maupun aaPanel: harness menjalankan `bin/filemanager serve` sendiri pada `.perfdata/run/` (di `.gitignore`, dihapus setelah selesai) di `127.0.0.1:<port acak>`.
- Go 1.27 (toolchain `go.mod`), binary `CGO_ENABLED=0`, `GOMEMLIMIT=512MiB` seperti unit systemd (SPEC §1.6), `APP_ENV=development` (HTTP polos di loopback — satu-satunya beda dengan produksi adalah cookie tanpa `Secure`).
- k6 tidak terpasang (`command -v k6` kosong) → klien Go `net/http` (keep-alive, cookie jar, CSRF + `Origin`, gzip transparan seperti browser), diizinkan PROMPT 21.

## 3. Metodologi (`make perf`)

1. `make bench` dua kali (`GOMAXPROCS` = semua CPU dan 1): benchmark in-process `listing.List` 10.000 entri dingin/cache (`backend/internal/listing/service_test.go`).
2. `TestPerf` (`backend/internal/perf/perf_test.go`, perlu `FM_PERF_BIN`/`FM_PERF_DIR`, dilewati di `make test`):
   - start binary nyata, login admin + ganti password awal (alur SPEC §8.2), fixture `perf.Generate` (`backend/internal/perf/gen.go`): `/Besar` = 10.000 file (nama angka beragam lebar, huruf besar/kecil, Unicode NFC, Jepang, spasi, dotfile, multi-titik; 16 ekstensi; 0–32 KB) + 500 folder (0–3 file), `/Gambar` = 200 PNG 64–320 px. Listing `/Besar` = **10.500 entri** (sedikit lebih berat dari target 10.000).
   - **list dingin**: sebelum setiap permintaan, harness membuat + menghapus file di `/Besar` dari luar proses → `mtime` folder berubah → entri cache SPEC §8.3 tidak valid (inilah "dingin" dalam praktik: folder baru disentuh atau TTL 15 s lewat). Page cache kernel tetap hangat; `drop_caches` sengaja tidak dipakai di server produksi. 50× sort nama + 20× sort `mtime desc`, halaman pertama 500 item. Permintaan pertama setelah start dilaporkan terpisah.
   - **list hangat**: 50× permintaan yang sama (cache hit).
   - **infinite scroll**: 21 halaman lewat `nextCursor`.
   - **navigasi**: 6 folder yang sudah dibuka, 120 permintaan bergiliran.
   - **thumbnail**: `files/sign` 200 path (`thumb`, 256 px), 20 permintaan paralel (= satu viewport grid, batas paralel per user SPEC §10.5), polling `202` tiap 200 ms (UI memakai `Retry-After: 2`), lalu 180 sisanya dengan lebar 20; bersamaan, `files/list` diminta tiap 100 ms untuk membuktikan UI tidak menunggu thumbnail.
   - **upload 1 GiB** tus (64 PATCH × 16 MiB, data acak `perf.Stream` tanpa file sumber), **zip streaming 1 GiB** (`files/zip` method store, body dibuang), **extract 1 GiB** (zip Store 8 anggota ditulis `perf.WriteStoreZip`, task `archives/extract` ditunggu sampai `done`).
   - Memori: `VmRSS`/`RssAnon`/`RssFile`/`VmHWM` dari `/proc/<pid>/status` (sampling 50 ms selama beban), gauge Go `go_heap_objects_bytes`/`go_heap_released_bytes`/`go_memory_total_bytes` dari `/metrics` (`METRICS_ADDR` loopback) — pengganti `pprof`, yang tidak pernah diekspos.
   - Gerbang: test gagal bila list dingin p95 ≥ 500 ms, hangat p95 ≥ 100 ms, idle RSS ≥ 100 MB, atau puncak RSS beban > baseline + 100 MB (`FM_PERF_ASSERT=0` hanya melapor).
   - Rate limit API/list/sign/thumb/csrf/tus dimatikan **hanya** di harness (mengukur latensi, bukan anggaran SPEC §10.5; 50 list/detik bukan pola klien).
3. Hasil mentah: `.perfdata/results-<n>cpu.json` (tidak di-commit).

## 4. Hasil backend

### 4.1 Listing (`GET /files/list?path=/Besar`, 10.500 entri, halaman 500 item = 242 KB JSON, **5 KB gzip** di kabel)

| Skenario | Semua CPU (4) | CPU 0 |
| --- | --- | --- |
| Permintaan pertama setelah start | 139 ms | 170 ms |
| Dingin, sort nama (n=50): p50 / p95 / p99 / max | 91 / **140** / 155 / 155 ms | 106 / **187** / 216 / 216 ms |
| Dingin, sort `mtime desc` (n=20): p50 / p95 | 86 / 107 ms | 123 / 149 ms |
| Hangat (n=50): p50 / p95 / p99 | 6,0 / **10,1** / 23,2 ms | 5,9 / **11,0** / 23,8 ms |
| 21 halaman via cursor (hangat): total · p95 per halaman | 234 ms · 19 ms | 336 ms · 25 ms |
| Navigasi 6 folder (n=120): p50 / p95 / p99 | 0,9 / 7,5 / 9,2 ms | 2,2 / 12,7 / 31,1 ms |
| `files/list` selama 200 thumbnail dirender: p50 / p95 / max | 5,5 / 10,8 / 98 ms | 5,5 / 9,8 / 120 ms |

Gzip 5 KB untuk 242 KB adalah rasio fixture sintetis (nama berurutan, kunci JSON berulang); data nyata diperkirakan 15–25 KB (SPEC §8.3).

In-process (`make bench`, tanpa HTTP/JSON):

| Benchmark | Sebelum (26c1913) | Sesudah | |
| --- | --- | --- | --- |
| `BenchmarkListDir10k` 4 CPU | 219 ms · 68,3 MB · 249.612 alokasi | **62–66 ms · 11,1 MB · 84.850 alokasi** | −70 % |
| `BenchmarkListDir10k` 1 CPU | 191 ms | **69–100 ms** | −48…64 % |
| `BenchmarkListDir10kCached` | 0,29 ms | 1,7 ms | +1,4 ms (format string kini per halaman, lihat §5) |

### 4.2 Transfer besar dan memori

| | Semua CPU (4) | CPU 0 |
| --- | --- | --- |
| RSS idle setelah start (sebelum login) | 26,0 MB | 25,3 MB |
| RSS idle setelah login + ganti password + fixture (≥ 4 s) | **30,5 MB** (anon 11,8) | **26,2 MB** (anon 7,5) |
| RSS setelah listing / setelah thumbnail (cache listing ±45 MB heap hidup) | 81 / 87 MB | 78 / 74 MB |
| Upload 1 GiB tus: durasi · puncak RSS vs baseline | 4,2 s (241 MB/s) · **+0,4 MB** | 3,8 s (273 MB/s) · **+0,1 MB** |
| Zip streaming 1 GiB: durasi · puncak RSS | 1,0 s (1.050 MB/s) · **+0,4 MB** | 0,9 s (1.104 MB/s) · **+1,2 MB** |
| Extract 1 GiB (task): durasi · puncak RSS | 1,8 s (585 MB/s) · **+1,1 MB** | 2,0 s (520 MB/s) · **+1,2 MB** |
| RSS idle 10 s setelah semua beban | 77,4 MB (anon 57,4) | 74,2 MB (anon 54,4) |
| `VmHWM` (puncak seumur proses) | 219 MB | 217 MB |

`VmHWM` ±217 MB **bukan** dari listing/transfer: `GODEBUG=gctrace=1` menunjukkan heap 195 MB tepat saat login + ganti password (tiga hash argon2id 64 MiB berurutan sebelum GC berikutnya: verifikasi login, verifikasi password lama, hash password baru — parameter SPEC §10.4). Memori itu dikembalikan ke OS 2 s kemudian (§5). Pengukuran R-C (15 Okt, binary lama, tiga beban 1 GiB bersamaan) mencatat RSS idle 168 MB / `VmHWM` 216 MB; idle kini 26–31 MB.

### 4.3 Thumbnail (SPEC §8.4)

| | Semua CPU (4, `THUMB_WORKERS=3`) | CPU 0 (`THUMB_WORKERS=1`) |
| --- | --- | --- |
| 20 thumbnail pertama (satu viewport, 20 paralel) | 2,2 s | 5,3 s |
| 200 thumbnail 256 px (3 ukuran per job) | 21,6 s = **9,2/s** | 56,3 s = **3,6/s** |
| Listing selama render (p95) | 10,8 ms | 9,8 ms |

Antrian thumbnail sudah memprioritaskan permintaan terbaru (heap LIFO, item terlihat = permintaan yang datang; klien membatalkan permintaan sel yang keluar viewport), CLI berjalan `nice -n 10 ionice -c3`, dan respons `202 + Retry-After` tidak pernah memblokir: angka "listing selama render" membuktikan UI tidak menunggu thumbnail. Pada 1 vCPU satu viewport penuh gambar baru butuh ±5 s; thumbnail tersimpan di cache disk (`ETag`, `max-age=86400`) sehingga kunjungan berikutnya instan.

## 5. Optimasi Step 21 (berdasarkan profil)

Profil CPU `BenchmarkListDir10k` (`go test -cpuprofile`) sebelum optimasi: 38 % syscall (dua stat per entri), 26 % `NaturalKey` (collator per run teks), 25 % alokasi + GC assist (68 MB per build).

| Perubahan | File | Dampak terukur |
| --- | --- | --- |
| `ReadDir` memakai getdents (`Readdirnames`) — `os.File.ReadDir` melakukan `lstat` sendiri lalu fsjail `statx` lagi | `fsjail/stat_linux.go` | separuh syscall hilang |
| `statx` paralel terbatas: ≤ 4 goroutine, blok 256 entri, hanya bila `GOMAXPROCS > 1` (di 1 CPU tetap berurutan — paralel hanya menambah penjadwalan) | `fsjail/stat_linux.go` | `ReadDir` 10k: 48 ms (1) → 24 ms (2) → 16 ms (4 goroutine) |
| String tampilan (`mtime/ctime/atime/btime`, `mode`, `modeOctal`, `etag`) diformat hanya untuk 500 item halaman yang dikirim (`Item.fill` pada salinan), bukan 10.000 item tiap build | `listing/entry.go` | −60.000 alokasi per build; cache hit +1,4 ms |
| Kunci kolasi per run teks di-memo per build (`keyer`, ≤ 8.192 run; nama satu folder berbagi `file`, `.txt`, `IMG_`) | `listing/sort.go` | `NaturalKey` 78 ms → ±5 ms per build |
| Nama ASCII dilipat tanpa `cases.Fold`/NFC (filter cepat) | `listing/service.go` | −10.000 alokasi |
| Memori argon2id dikembalikan ke OS 2 s setelah hash terakhir (`debug.FreeOSMemory`, debounced) | `auth/password.go` | idle setelah login 156–219 MB → 26–31 MB |
| Middleware gzip sendiri: hanya ≥ 1 KB, tipe JSON/HTML/CSS/JS/SVG, tidak menyentuh HEAD/`Range`/206/`Content-Encoding`/unduhan/SSE, `ETag` kuat → lemah, `Vary`, jalur `ReadFrom` (sendfile) tetap untuk respons tak terkompresi | `httpapi/compress.go` | halaman 242 KB → 5 KB; respons < 1 KB tidak lagi dikompresi |
| Gauge memori Go di `/metrics` | `metrics/metrics.go` | observabilitas tanpa pprof |

Sudah ada sebelum Step 21 (diverifikasi, tidak diubah): cache listing LRU 64 MB + invalidasi (SPEC §8.3), `json.Encoder` langsung ke writer (tanpa buffer ganda), `Cache-Control: public, max-age=31536000, immutable` untuk `/assets/*` berhash (`webassets`), HTTP keep-alive (`IdleTimeout` 120 s), antrian thumbnail prioritas + `nice`. Tidak dibuat: brotli di aplikasi (tanpa dependensi baru; opsional di Nginx bila modul tersedia — perlu verifikasi di aaPanel) dan prakompresi aset (aset immutable diunduh sekali per rilis; gzip level 6 per permintaan ±10 ms CPU untuk JS awal).

## 6. Frontend (`web/e2e/perf.spec.ts`)

Playwright Chromium headless di mesin yang sama dengan backend (4 vCPU dibagi browser, binary, runner), binary `make build` (bundle produksi). Fixture: `perf10k/` = 10.000 file (12 ekstensi, nama `file<i>`/`Laporan <i> (final)`) + 500 folder. Waktu diambil **di dalam halaman**: `performance.mark` saat `dblclick`/`click` (listener capture) dan `performance.measure` pada frame setelah baris target ada di DOM (`MutationObserver` → `requestAnimationFrame` → `setTimeout 0`), sehingga round trip Playwright tidak terhitung. Long task dari `PerformanceObserver({type:'longtask'})`. **Tracing Playwright dimatikan** untuk spec ini: snapshot DOM tracing (`trace: retain-on-failure` global) terukur sebagai long task aplikasi (terlihat di profil CDP).

| Skenario (gerbang) | Sebelum 21-2 (UI `7867920`) | Sesudah 21-2 (4 run) |
| --- | --- | --- |
| Render pertama 10.500 entri, cache server dingin (< 1 s) | 270 ms | 205 / 273 / 232 / 219 ms |
| Navigasi hangat back/forward, p95 dari 10 (< 150 ms) | 134 ms | 94 / 96 / 86 / 100 ms |
| Scroll seluruh 10.500 baris (2 tinggi viewport per frame + infinite scroll 20 halaman): long task maks (≤ 100 ms) | **124 ms — gagal**, 356 long task | 85 / 71 / 72 / 62 ms; 78 / 11 / 12 / 5 long task |
| Sort nama turun sampai baris pertama (< 300 ms) | — | 234 / 195 / 183 / 171 ms |

Profil CDP (`Profiler.start` selama scroll, build tanpa minify) menunjukkan penyebab, diperbaiki berurutan:

1. **Offset scroll ditulis ke store tab pada setiap event scroll** → seluruh `BrowserLayout` (toolbar, address bar, tab bar, status bar) dirender ulang tiap frame. Kini offset disimpan setelah scroll berhenti 150 ms dan di-flush saat folder berganti (`BrowserLayout.rememberScroll`; back/forward tetap memulihkan posisi). Ini perbaikan terbesar (356 → 5–12 long task).
2. **Baris Details tidak di-memo**: kini `DetailsRow` (`React.memo`) dengan handler stabil lewat ref, `colStyle` di-memo, overscan 12 → 10.
3. **Hook per sel**: `NoteCell` read-only tanpa `useTranslation` (editor terpisah), `ItemBadges` keluar sebelum hook bila tidak ada badge, label tipe dan tanggal relatif/absolut di-memo per set kolom (`CellMemo`, tanggal relatif dihitung ulang tiap 30 s).
4. **Checkbox baris** memakai elemen ringan ber-`role="checkbox"` (gaya sama dengan `ui/Checkbox`; tabIndex −1, fokus tetap di grid) alih-alih primitive Radix per baris.
5. **Ikon file sebagai sprite SVG** (`FileIconSprite` sekali di root, setiap ikon = satu `<use>`) alih-alih pohon komponen lucide per sel.
6. Kolom Dimensi/Durasi (`media=1` memprobe ≤ 50 file sinkron di server): listing biasa dulu, varian ber-media diminta saat browser idle (`requestIdleCallback`, fallback 200 ms) dan menggantikan baris di tempat (`useListing`, `placeholderData`).

Sudah ada sebelumnya dan dipertahankan: route jarang (`admin/*`, `storage`, `settings`, editor, viewer, share) sudah lazy lewat `autoCodeSplitting` TanStack Router; halaman berikut diambil saat 80 % baris yang dimuat sudah dirender (bukan lebih awal). Dicoba lalu dibatalkan: `structuralSharing: false` pada query listing (tidak mengubah angka, kehilangan identitas item saat refetch).

Sisa yang diketahui: dengan scroll "lempar" 2 viewport per frame, setiap frame membuat ±60 baris baru sekaligus; 4–10 frame per lintasan penuh tetap 50–85 ms (di atas sasaran 50 ms, di bawah gerbang 100 ms). View Grid/List/Tiles belum di-memo per sel (perbaikan store scroll berlaku juga untuk semuanya).

## 7. Bundle (`web/scripts/bundle-size.mjs`)

`pnpm build` (dan `pnpm build --report` di CI) mengukur JS yang dimuat `dist/index.html` di awal (entry module + `modulepreload`) dan **gagal (exit 1) bila ≥ 300 KB gzip**; laporan semua chunk ditulis ke `web/bundle-report/bundle-report.{json,md}` (di luar `dist`, jadi tidak ikut ter-embed/disajikan binary — sebelumnya `dist/bundle-size.json` ikut terlayani publik) dan diunggah CI sebagai artefak `bundle-report`. CI juga menguji gerbangnya sendiri (`BUNDLE_BUDGET_KB=100` harus gagal).

| | Ukuran gzip |
| --- | --- |
| JS awal (20 chunk) | **220,2 KB** (Step 20: 218,4 KB; +1,8 KB = sprite ikon file + `DetailsRow`) |
| CSS awal | 15,2 KB |
| JS lazy (289 chunk) | 4.445,5 KB — terbesar: worker TypeScript Monaco 1.450, `editor.api` 681, worker pdf.js 367, `vs` 314, pdf.js 125, Markdown/Prettier 89 + 26, viewer pdf.js 50, Video.js 28 |

Monaco, pdf.js, Video.js, QR (`qrcode`), Prettier, highlight.js dan Markdown hanya dimuat oleh chunk route/fitur masing-masing (diverifikasi di laporan: tidak ada yang berlabel `initial`). Lucide sudah tree-shake dengan named import (117 file mengimpor 259 nama; ikon yang tidak dipakai, mis. `AlarmClock`, tidak ada di `dist`), jadi import per path ikon tidak diperlukan. Tidak ada dependensi yang tidak dipakai (setiap paket `dependencies` diimpor dari `src/`; `@testing-library/dom` = peer `@testing-library/react`).

## 8. Pengukuran ulang

```bash
pnpm -C web e2e perf.spec.ts    # frontend (butuh `make build`)
make perf                       # ±5 menit, butuh ±3,5 GB ruang sementara di .perfdata/
FM_PERF_ASSERT=0 make perf      # hanya melapor (tanpa gagal)
FM_PERF_ONLY=listing …          # (env pada go test) hanya skenario listing
```

Untuk mengukur di server lain/setelah deploy: jalankan `make perf` dari checkout di server itu (harness tetap memakai binary dan direktori sendiri; tidak membaca `.env`).

## 9. Batasan yang diketahui

- Klien dan server satu mesin (loopback): latensi jaringan nyata (TLS lewat Nginx, RTT) tidak termasuk; angka API adalah biaya server.
- "Dingin" = cache aplikasi tidak valid dengan page cache kernel hangat. Folder yang belum pernah disentuh sejak boot (cache inode/dentry dingin di disk virtual) bisa lebih lambat; tidak diukur karena `drop_caches` memengaruhi semua layanan di server produksi.
- Fixture memakai file kosong/kecil; ekstensi media palsu memicu probe latar `ProbeOnly` (SPEC §8.4) sehingga run pertama juga menanggung ffprobe/vips nice — termasuk dalam angka.
