# Deploy FileManager di Ubuntu + aaPanel (`https://file.kppbwi.app`)

Panduan klik-per-klik Step 22 (SPEC §12, ADR-20/22, PRD E14). Aplikasi berjalan sebagai **satu binary** di
`/www/wwwroot/file.kppbwi.app/bin/filemanager`, dijalankan **systemd** sebagai user `filemanager`, hanya
mendengarkan `127.0.0.1:8080`; **Nginx aaPanel** menjadi satu-satunya pintu publik (HTTPS) dan meneruskan
semua request ke aplikasi. Storage user ada di `/www/storage/FileManager` (milik `filemanager`, 0750).

Penanda:

- **Terverifikasi** = dicek langsung di server ini (aaPanel 8.0.7, Ubuntu 24.04.5) atau dibaca dari source
  aaPanel yang terpasang (`/www/server/panel/mod/project/proxy/comMod.py`), tanggal 25 Okt 2026.
- **Perlu verifikasi** = nama menu/tombol UI atau perilaku yang belum dilihat langsung; cocokkan saat
  mengerjakan, lalu perbarui dokumen ini.

## Isi

1. [Ringkasan perubahan di server](#1-ringkasan-perubahan-di-server)
2. [Prasyarat](#2-prasyarat)
3. [Langkah 1 — `deploy/install.sh`](#3-langkah-1--deployinstallsh)
4. [Langkah 2 — site di aaPanel](#4-langkah-2--site-di-aapanel)
5. [Langkah 3 — SSL Let's Encrypt + Force HTTPS](#5-langkah-3--ssl-lets-encrypt--force-https)
6. [Langkah 4 — verifikasi](#6-langkah-4--verifikasi)
7. [Catatan penting](#7-catatan-penting)
8. [Update & rollback](#8-update--rollback)
9. [Backup & restore](#9-backup--restore)
10. [Troubleshooting](#10-troubleshooting)
11. [Membatalkan instalasi](#11-membatalkan-instalasi)
12. [Hasil smoke test](#12-hasil-smoke-test)
13. [Origin konten terpisah (opsional, Step 23e)](#13-origin-konten-terpisah-opsional-step-23e)

## 1. Ringkasan perubahan di server

| Bagian | Sebelum (25 Okt 2026, terverifikasi) | Sesudah `install.sh` + aaPanel |
| --- | --- | --- |
| User Linux | `filemanager` belum ada | user sistem `filemanager` (home `/nonexistent`, shell `/usr/sbin/nologin`) |
| `/www/storage/FileManager` | `www:www` 0755, isi hanya `.system` (`root:root`) | `filemanager:filemanager` 0750, `.system` + subfolder 0750 milik `filemanager` |
| `/www/wwwroot/file.kppbwi.app` | repo milik `root` | **pemilik tidak diubah** (lihat 7.1); `.env` → `filemanager` 0600; `bin/filemanager` 0755; `public/.well-known/` 0755 |
| `.env` | ada, `APP_SECRET` kosong | `APP_SECRET` 64 hex diisi, komentar di belakang nilai dibuang, salinan lama `.env.bak-<ts>` (0600) |
| systemd | unit `filemanager` belum ada | `/etc/systemd/system/filemanager.service` (enable + start) |
| Port | 8080 bebas; UFW mengizinkan 20, 80, 443 | `127.0.0.1:8080` (loopback); **UFW/firewall aaPanel tidak disentuh** |
| aaPanel | belum ada site `file.kppbwi.app` (domain saat ini dijawab site default Nginx) | site tipe **Reverse Proxy** → `http://127.0.0.1:8080`, SSL Let's Encrypt, Force HTTPS |
| DNS | `file.kppbwi.app` **proxied Cloudflare** (28 Okt 2026: `dig` menjawab `104.21.74.172`/`172.67.160.85`; IP server `<SERVER_IP>` menjadi origin — sebelumnya record A langsung ke server) | tetap; PART B membaca `CF-Connecting-IP` hanya dari rentang Cloudflare (lihat 7.11) |

`install.sh` **tidak pernah** menyentuh UFW, firewall aaPanel, Nginx, atau konfigurasi aaPanel.

## 2. Prasyarat

- DNS: `file.kppbwi.app` menunjuk ke server — langsung (record A `<SERVER_IP>`) **atau** lewat proxy Cloudflare
  (kondisi sejak Okt 2026, terverifikasi 28 Okt 2026: HTTP-01 Let's Encrypt, upload, SSE dan Range tetap berjalan
  lewat Cloudflare; lihat 7.11). Di Cloudflare pakai mode SSL **Full (strict)** setelah sertifikat origin terpasang;
  "Always Use HTTPS" tidak diperlukan (Force HTTPS aaPanel sudah mengalihkan).
- Port 80 dan 443 terbuka di UFW (sudah, terverifikasi `ufw status`). Port 8080 **tidak** dibuka.
- Ubuntu 22.04/24.04 (server: 24.04.5). RAM: build di server butuh ± 1,5 GB+; server kini 5,9 GB + swap 2,8 GB
  (cukup). Server yang lebih kecil: build di CI lalu `--binary`, atau swap 2 GB (install.sh menawarkan, default tidak).
- Akses root (`sudo`) dan login aaPanel.
- Checkout repo di `/www/wwwroot/file.kppbwi.app` (sudah).

## 3. Langkah 1 — `deploy/install.sh`

Selalu lihat rencananya dulu (hanya membaca, tidak mengubah apa pun):

```bash
cd /www/wwwroot/file.kppbwi.app
sudo bash deploy/install.sh --dry-run          # atau: make install ARGS="--dry-run"
```

Lalu jalankan sungguhan (bisa diulang kapan saja — idempoten; run kedua tidak mengubah apa pun dan tidak me-restart):

```bash
sudo bash deploy/install.sh                    # build di server (make build sebagai pemilik repo)
# atau, dengan binary hasil CI / build sebelumnya:
make build && sudo bash deploy/install.sh --binary bin/filemanager
```

| Flag | Arti |
| --- | --- |
| `--dry-run` | cetak rencana; tidak ada perubahan |
| `--yes` | jawab pertanyaan dengan default aman (swapfile dan `chown -R` isi storage = **tidak**) |
| `--binary PATH` | pasang binary jadi (CI); Go/Node tidak diperiksa |
| `--skip-build` (`--no-build`) | pakai `bin/filemanager` yang sudah ada |
| `--rebuild` | build ulang walau binary sudah dari commit HEAD |
| `--with-unrar` | pasang `unrar` (multiverse, non-free) dan isi `UNRAR_PATH` |
| `--skip-apt` | lewati apt |
| `--skip-aapanel` | jangan hapus file default aaPanel dan jangan cetak checklist aaPanel (CI) |
| `--no-swap` | jangan menawarkan swapfile |

Urutan kerja (SPEC §12.1): cek root + OS → RAM/swap → apt (`ffmpeg libvips-tools poppler-utils ripgrep 7zip curl
ca-certificates openssl`; hanya yang belum terpasang) → Go (≥ versi `go` di `backend/go.mod`; bila kurang, unduh rilis
stable terbaru dari indeks resmi `https://go.dev/dl/?mode=json` dan cocokkan SHA-256) → Node ≥ 22 + corepack →
user `filemanager` → `.env` → storage → `public/.well-known` → binary → `check-deps` + `migrate up` sebagai
`filemanager` → unit systemd → tunggu `/readyz` (≤ 30 s) → checklist aaPanel.

`.env`:

- Belum ada → disalin dari `.env.example` **tanpa komentar di belakang nilai** (systemd `EnvironmentFile=` membaca
  `nilai   # komentar` apa adanya), `APP_SECRET=$(openssl rand -hex 32)`, `ADMIN_INITIAL_PASSWORD` ditanyakan tanpa
  echo (kosong = acak 24 karakter). Password acak ditampilkan **sekali di terminal (`/dev/tty`)**, tidak pernah ke
  stdout/log; tanpa terminal (CI) lihat `.env`.
- Sudah ada → hanya dilengkapi (`APP_SECRET` kosong/`CHANGE_ME`, password admin `CHANGE_ME`) dan dibersihkan dari
  komentar inline; salinan lama `.env.bak-<tanggal>` (0600). Nilai lain tidak diubah.
- Selalu `filemanager` 0600. Peringatan bila `ALLOW_ROOT=true` atau `LISTEN_ADDR` bukan loopback.

Storage: bila `/www/storage/FileManager` masih `www:www` (kondisi server ini) install.sh memperingatkan lalu mengganti
ke `filemanager:filemanager` 0750. Bila sudah ada folder user di dalamnya, `chown -R` **ditanyakan** (default tidak).
`.system` selalu di-`chown -R` (berisi DB/cache aplikasi; di server ini masih milik `root` dari smoke test Step 1).

Unit systemd `deploy/filemanager.service`: `User=filemanager`, `EnvironmentFile=…/.env`, `GOMEMLIMIT=512MiB`,
`TMPDIR=…/.system/tmp`, `Restart=on-failure`, `LimitNOFILE=65536`, `UMask=0027`, hardening `ProtectSystem=strict`
(APP_DIR read-only bagi proses) + `ReadWritePaths=/www/storage/FileManager`, `ProtectHome`, `PrivateTmp`,
`PrivateDevices`, `ProtectKernel*`, `ProtectProc=invisible`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX`,
`SystemCallFilter=@system-service ~@privileged` (+ `SystemCallErrorNumber=EPERM`), `CapabilityBoundingSet=` kosong.
`@resources` sengaja **tidak** diblokir: `nice`/`ionice` (`internal/exec`) memerlukan `setpriority`/`ioprio_set`.
`MemoryDenyWriteExecute` tetap `false` (libvips/orc dapat membuat kode saat runtime; perlu verifikasi).

## 4. Langkah 2 — site di aaPanel

### 4.A Jalur utama: proyek **Reverse Proxy** (aaPanel 8.0.7)

Fakta dari source aaPanel yang terpasang (terverifikasi):

- Tipe proyek Reverse Proxy ada dan dipakai `proxy.kppbwi.app` di server ini.
- Konfigurasi Nginx site **dibangun ulang dari JSON** `/www/server/proxy_project/sites/<domain>/<domain>.json`
  setiap kali pengaturan diubah. Edit manual pada `/www/server/panel/vhost/nginx/file.kppbwi.app.conf` akan hilang.
  Hanya dua kolom kustom yang ikut disimpan: **custom configuration per URL proxy** (dirender di dalam
  `location ^~ / { … }`) dan **server block** site (dirender di dalam `server { }` antara `#SERVER-BLOCK START/END`).
  Timeout proxy adalah kolom tersendiri per URL proxy.
- Site directory **selalu** `/www/wwwroot/<domain>` = `/www/wwwroot/file.kppbwi.app` (tidak bisa diatur saat membuat;
  running directory `/public` tidak tersedia di tipe ini). Karena semua path di-proxy (`location ^~ /`), Nginx tidak
  menyajikan file dari folder itu kecuali `/.well-known/`.
- aaPanel membuat `/www/wwwroot/file.kppbwi.app/proxy_cache_dir` (milik `www`) dan menambah `proxy_cache_path` di
  level http. `nginx.conf` aaPanel juga memuat `proxy.conf` dengan `proxy_cache cache_one;` untuk **semua** site →
  PART A mematikannya untuk site ini (`proxy_cache off`) agar salinan file user tidak pernah tersimpan di
  `/www/server/nginx/proxy_cache_dir`.
- **Bahaya:** menghapus proyek dengan opsi hapus direktori (`remove_path=1`) menjalankan
  `rm -rf /www/wwwroot/file.kppbwi.app` — itu repo, binary, dan `.env`. Jangan pernah mencentang opsi itu.

Klik-per-klik (nama menu/tombol **perlu verifikasi**):

1. aaPanel → **Website** → tab **Reverse Proxy** (atau "Proxy Project") → **Add**.
2. Isi: Domain `file.kppbwi.app`; Target URL `http://127.0.0.1:8080`; Send domain (proxy host) `$http_host`;
   Remark `FileManager`. Simpan. (Jangan aktifkan cache.)
3. Buka site `file.kppbwi.app` → bagian **Proxy** / daftar URL proxy → baris `/` → **Edit**:
   - Timeout: **connect 10**, **send 3600**, **read 3600** (detik). Default aaPanel 60/600/600 memutus SSE dan
     unduhan panjang. Biarkan **WebSocket** aktif (default).
   - **Custom configuration** (konfigurasi kustom URL ini): tempel isi **PART A** dari `deploy/nginx-aapanel.conf`
     (baris di antara `# --- BEGIN PART A` dan `# --- END PART A ---`). Jangan menambahkan `proxy_read_timeout`
     dsb. di sini — aaPanel sudah menulisnya dari kolom timeout dan Nginx menolak direktif ganda.
   - **Cache** URL ini: **off**.
4. Site → **Config file** / **Configuration** → bagian **server block** (bukan "http block") → tempel **PART B**.
   Simpan: aaPanel menulis ulang vhost, menjalankan `nginx -t`, lalu reload.
5. Hapus file default aaPanel di site directory bila ada (`index.html`, `404.html`, `.htaccess`, `.user.ini` — yang
   terakhir `chattr -i` dulu). Menjalankan ulang `sudo bash deploy/install.sh` melakukannya otomatis (file yang
   di-track git tidak disentuh).
6. Cek hasil di server (hanya membaca):

   ```bash
   nginx -T 2>/dev/null | sed -n '/server_name file.kppbwi.app/,/^}/p' | grep -nE 'proxy_pass|proxy_request_buffering|proxy_buffering|proxy_cache|client_max_body_size|proxy_read_timeout|location = /readyz|access_log'
   ```

   Harus terlihat `proxy_pass http://127.0.0.1:8080`, `proxy_request_buffering off`, `proxy_buffering off`,
   `proxy_cache off`, `client_max_body_size 64m`, `proxy_read_timeout 3600s`, `location = /readyz`.

Alternatif otomatis (**terverifikasi 28 Okt 2026 di aaPanel 8.0.7** — keempat perintah inilah yang dipakai saat smoke
test; **perlu verifikasi** pada versi panel lain): `deploy/aapanel/site.py` memanggil API internal panel yang sama
dengan UI (pola `proxy.kppbwi.app/deploy/aapanel/site.py`):

```bash
P=/www/server/panel/pyenv/bin/python3
sudo $P deploy/aapanel/site.py status
sudo $P deploy/aapanel/site.py create        # Reverse Proxy → 127.0.0.1:8080, $http_host
sudo $P deploy/aapanel/site.py configure     # timeout 10/3600/3600 + PART A + PART B
sudo $P deploy/aapanel/site.py ssl           # Let's Encrypt (HTTP-01)
sudo $P deploy/aapanel/site.py force-https
```

### 4.B Alternatif: site PHP "Static" + Reverse proxy (teks brief; perlu verifikasi)

1. **Website** → **PHP project** → **Add site**: domain `file.kppbwi.app`, site directory
   `/www/wwwroot/file.kppbwi.app`, PHP **Static**, tanpa database & FTP.
2. Site → **Site directory** → **Running directory** `/public` → simpan (document root = `public/`, jadi
   `.well-known` disajikan dari `public/.well-known`).
3. Site → **Reverse proxy** → **Add**: nama `app`, target `http://127.0.0.1:8080`, send domain `$http_host`,
   proxy directory `/`. Di editor konfigurasi proxy itu tempel **PART A** (jika direktif timeout belum ada di file
   proxy, tambahkan `proxy_connect_timeout 10s; proxy_send_timeout 3600s; proxy_read_timeout 3600s;`).
4. Site → **Config** → tempel **PART B** di dalam `server { }` (di mana saja; aturan `if` level server dievaluasi
   sebelum pemilihan location).
5. Hapus `index.html`, `404.html`, `.htaccess`, `.user.ini` (`chattr -i` dulu) yang dibuat aaPanel.
6. Setelah mengubah SSL/proxy di kemudian hari, ulangi pengecekan `nginx -T` di 4.A langkah 6 — apakah aaPanel
   mempertahankan baris kustom pada tipe site ini **perlu verifikasi**.

### 4.C Kenapa PART B memakai `if` level server, bukan `location ~`

Blok proxy aaPanel adalah `location ^~ /`. Prefix `^~` yang cocok menghentikan Nginx sebelum mengevaluasi
location regex mana pun — termasuk `location ~ ^/(\.user.ini|\.htaccess|\.git|\.env|…)` bawaan aaPanel, yang jadi
tidak pernah berlaku untuk site proxy (dibuktikan `deploy/test/nginx_snippet_test.sh`: tanpa PART B, `/.env`,
`/bin/filemanager`, `/readyz` diteruskan ke aplikasi). `if ($uri ~ …) { return 404; }` di level server dijalankan
pada fase rewrite server, sebelum location dipilih, dan `$uri` sudah ter-decode dan dinormalisasi (`/%2e%65nv`,
`/assets/../.env` ikut tertolak). `/readyz` dan `/metrics` memakai `location =` (exact match selalu menang).

## 5. Langkah 3 — SSL Let's Encrypt + Force HTTPS

1. Site → **SSL** → **Let's Encrypt** → centang `file.kppbwi.app` → verifikasi **file** (HTTP-01) → **Apply**.
2. Setelah terbit: aktifkan **Force HTTPS** (aaPanel menambah rewrite `#HTTP_TO_HTTPS` dan header
   `Strict-Transport-Security`).
3. `.well-known`: vhost aaPanel meng-include `/www/server/panel/vhost/nginx/well-known/<domain>.conf` (Lua,
   terverifikasi) sebelum blok proxy: file `/.well-known/<x>` dibaca dari **document root** site
   (varian A: `/www/wwwroot/file.kppbwi.app/.well-known/`, varian B: `public/.well-known/`), fallback
   `/www/wwwroot/java_node_ssl/.well-known/`; ditambah `location /.well-known { root … }`. Snippet tidak
   menduplikasi location ini dan aturan dotfile PART B mengecualikan `.well-known`.
4. Perpanjangan otomatis dijalankan cron aaPanel; uji perpanjangan (renew/dry-run) dari UI bila tersedia —
   **perlu verifikasi** tombolnya.

## 6. Langkah 4 — verifikasi

Di server:

```bash
systemctl status filemanager --no-pager            # active (running)
ps -o user=,pid=,rss=,cmd= -C filemanager          # user filemanager
ss -ltnp | grep 8080                               # hanya 127.0.0.1:8080
ufw status | grep 8080 || echo "8080 tidak ada di UFW"
curl -s http://127.0.0.1:8080/readyz               # {"status":"ok",...}
sudo -u filemanager /www/wwwroot/file.kppbwi.app/bin/filemanager check-deps --env-file /www/wwwroot/file.kppbwi.app/.env
systemd-analyze security filemanager | tail -1     # target <= 4.0 (OK/MEDIUM)
stat -c '%U:%G %a %n' /www/storage/FileManager /www/wwwroot/file.kppbwi.app/.env
```

Dari luar (laptop):

```bash
H() { curl -s -D - -o /dev/null "$@"; }               # GET + header (HEAD dijawab 405 oleh /healthz)
H https://file.kppbwi.app/healthz | grep -iE '^HTTP|strict-transport'   # 200 + strict-transport-security
H http://file.kppbwi.app/healthz  | grep -iE '^HTTP|^location'          # 301 → https
for p in /.env /bin/filemanager /backend/go.mod /readyz /metrics; do
  printf '%-18s %s\n' "$p" "$(curl -s -o /dev/null -w '%{http_code}' https://file.kppbwi.app$p)"; done   # semua 404
# ACME: file uji di document root (varian A)
sudo install -d -m 0755 /www/wwwroot/file.kppbwi.app/.well-known/acme-challenge
echo fm-acme-ok | sudo tee /www/wwwroot/file.kppbwi.app/.well-known/acme-challenge/fm-test >/dev/null
curl -s http://file.kppbwi.app/.well-known/acme-challenge/fm-test   # fm-acme-ok (dari Nginx, bukan aplikasi)
sudo rm /www/wwwroot/file.kppbwi.app/.well-known/acme-challenge/fm-test
# Range pada aset statis — langsung ke origin (lewat Cloudflare aset yang di-cache dijawab 200 penuh, lihat 7.11)
A=$(curl -s https://file.kppbwi.app/ | grep -o '/assets/[^"]*\.js' | head -1)
curl -s -o /dev/null -w '%{http_code}\n' -r 0-99 --resolve file.kppbwi.app:443:<SERVER_IP> "https://file.kppbwi.app$A"   # 206
```

Di browser:

1. Login `admin` dengan `ADMIN_INITIAL_PASSWORD` → wajib ganti password → login ulang.
2. Hapus `ADMIN_INITIAL_PASSWORD` dari `.env` lalu `sudo systemctl restart filemanager` (aplikasi memberi WARN di log
   selama nilai itu masih ada).
3. Upload file 100 MB (tidak boleh 413 — chunk 16 MB < `client_max_body_size 64m`).
4. Jalankan task panjang (mis. compress folder besar) dan biarkan panel task terbuka ≥ 2 menit: progress mengalir
   tanpa putus (SSE, `proxy_buffering off` + timeout 3600 s).
5. Download file besar lalu jeda/lanjut di download manager (Range 206); putar video dan geser posisi.
6. Admin → Dashboard: status dependency (ffmpeg, vips, pdftoppm, 7z, rg) **ada**; thumbnail gambar/video/PDF
   muncul — membuktikan CLI berjalan di bawah filter syscall unit.

## 7. Catatan penting

1. **Pemilik APP_DIR tidak diubah ke `filemanager`** (deviasi dari brief, lihat SPEC §12.1): git menolak work tree
   milik user lain (`detected dubious ownership`), dan service tidak boleh bisa menulis ulang binary-nya sendiri.
   Service hanya perlu membaca `.env` (0600 miliknya) dan menjalankan `bin/filemanager`; `ProtectSystem=strict`
   membuat APP_DIR read-only bagi proses. Build dijalankan sebagai pemilik repo.
2. **Jangan memakai user `www`** untuk service atau storage: situs PHP lain yang berjalan sebagai `www` bisa
   membaca file user.
3. **Jangan edit vhost manual** (`/www/server/panel/vhost/nginx/file.kppbwi.app.conf`): aaPanel membangunnya ulang
   dari JSON. Semua kustomisasi lewat PART A/B di UI.
4. **Port 8080 tidak dibuka** di UFW maupun firewall aaPanel (Security). Aplikasi hanya loopback.
5. **Jangan hapus proyek Reverse Proxy dengan opsi hapus direktori** (`rm -rf /www/wwwroot/file.kppbwi.app`).
6. Log aaPanel: token link publik tidak dicatat (PART B `access_log off`). Statistik situs aaPanel
   (`site_total`) memuat header `Cookie`; untuk mematikannya bagi site ini aktifkan baris `access_log` opsional di
   PART A (perlu verifikasi efek pada halaman statistik).
7. HSTS dikirim dua kali (aaPanel + aplikasi); browser memakai yang pertama — dapat diterima.
8. Opsional `hidepid=2` untuk `/proc` (SECURITY.md §5, perlu verifikasi dengan monitor aaPanel); unit sudah
   memakai `ProtectProc=invisible`.
9. Gzip: aplikasi sudah mengompresi JSON/HTML/CSS/JS ≥ 1 KB (Step 21); gzip global aaPanel boleh dibiarkan.
   Brotli tidak tersedia (modul tidak ada di build Nginx aaPanel, terverifikasi `nginx -V`).
10. `METRICS_ADDR=127.0.0.1:9090` boleh diisi di `.env` untuk Prometheus lokal; tidak pernah di-proxy.
11. **Cloudflare di depan origin** (DNS proxied sejak Okt 2026):
    - Alamat pengunjung: Cloudflare mengirim `CF-Connecting-IP`; PART B memuat `set_real_ip_from` 22 rentang resmi
      Cloudflare + `real_ip_header CF-Connecting-IP`, sehingga `$remote_addr`, `X-Real-IP`, `X-Forwarded-For`, log
      Nginx dan limiter per-IP aplikasi melihat IP pengunjung. Hit langsung ke IP origin dengan header palsu
      diabaikan (peer bukan rentang Cloudflare). Daftar rentang: `www.cloudflare.com/ips-v4` dan `/ips-v6` — bila
      berubah, perbarui PART B (test snippet memperingatkan).
    - SSL: sertifikat Let's Encrypt origin diterbitkan lewat HTTP-01 **melalui** Cloudflare (terverifikasi 28 Okt
      2026); di Cloudflare pilih mode **Full (strict)**. Perpanjangan otomatis aaPanel memakai jalur yang sama.
    - Batas Cloudflare (paket gratis): body per request ≤ 100 MB (chunk tus 16 MB aman), origin harus mulai
      menjawab ≤ 100 s (SSE mengirim `: ping` tiap 25 s; task berjalan asinkron). Range pada **aset statis** yang
      di-cache Cloudflare dijawab `200` penuh (objek cache ter-gzip; bukan masalah fungsi); Range pada file user
      (`Cache-Control: private`, tidak di-cache) diteruskan ke origin → `206`.
    - Bila DNS dikembalikan ke "DNS only", tidak ada yang perlu diubah.

## 8. Update & rollback

```bash
sudo bash deploy/update.sh --dry-run                                   # rencana saja
sudo bash deploy/update.sh                                              # build checkout saat ini
sudo bash deploy/update.sh --ref v1.0.1                                 # git fetch (jika ada remote) + checkout + build
sudo bash deploy/update.sh --binary /path/filemanager                   # binary dari CI
sudo bash deploy/update.sh --release filemanager-linux-amd64.tar.gz     # tarball CI + .sha256 di sebelahnya
```

Alur: `backup-db --label pre-update` (versi yang sedang jalan, `VACUUM INTO`) → `bin/filemanager.prev` →
`systemctl stop` → `migrate up` dengan binary baru → binary baru dipasang → `start` → `/readyz` ≤ 30 s
(`--ready-timeout`). Gagal → **rollback otomatis**: migrasi baru di-`down` dengan binary baru sampai versi skema
lama (jika `down` gagal: DB dipulihkan dari backup pre-update; file lama disimpan `app.db.failed-<ts>`), binary
`.prev` dikembalikan, start, exit 1; binary gagal disimpan `bin/filemanager.failed`. Log:
`/www/storage/FileManager/.system/logs/update-<ts>.log`. Backup berlabel tidak dirotasi — hapus manual sesekali.
Rollback manual ke versi lebih lama: `sudo bash deploy/update.sh --binary <binary lama>` atau `--ref <tag lama>`.

## 9. Backup & restore

- **Storage** `/www/storage/FileManager`: backup harian lewat aaPanel (**Cron** → **Backup directory**, perlu
  verifikasi nama menu) atau rsync/restic ke lokasi lain. Kecualikan `.system/tmp`, `.system/uploads`,
  `.system/thumbs`. aaPanel berjalan sebagai root, jadi folder 0750 milik `filemanager` tetap terbaca.
- **Database**: job `backup_db` 02:30 (`VACUUM INTO .system/backups/app-<yyyyMMdd-HHmmss>.db`, simpan 7) +
  `filemanager backup-db [--label L]` manual; ikut backup storage.
- **`.env`** disimpan terpisah dan terenkripsi (berisi `APP_SECRET`; tanpa nilai yang sama sesi/link
  bertanda tangan tidak terbaca).
- **Restore**:

  ```bash
  sudo systemctl stop filemanager
  # pulihkan /www/storage/FileManager (atau subset user) dari backup
  cd /www/storage/FileManager/.system/db
  sudo mv app.db app.db.old; sudo rm -f app.db-wal app.db-shm
  sudo install -m 0640 -o filemanager -g filemanager ../backups/app-<ts>.db app.db
  sudo chown -R filemanager:filemanager /www/storage/FileManager
  sudo systemctl start filemanager
  ```

  Lalu di Admin → Maintenance jalankan `recalc_quota` dan `integrity_check`.

## 10. Troubleshooting

| Gejala | Penyebab umum | Tindakan |
| --- | --- | --- |
| 502 Bad Gateway | service mati / crash saat start / `LISTEN_ADDR` beda | `systemctl status filemanager`, `journalctl -u filemanager -n 100`, `curl 127.0.0.1:8080/readyz`, `check-deps` |
| Service tidak start: `configuration error(s)` | `.env` tidak valid (mis. `APP_SECRET` bukan 64 hex, komentar inline) | jalankan ulang `sudo bash deploy/install.sh` (membersihkan + melengkapi `.env`) |
| `refusing to run as root` | binary dijalankan manual sebagai root | jalankan lewat systemd / `sudo -u filemanager` |
| 413 saat upload | PART A belum ditempel/tertimpa (`client_max_body_size` http aaPanel 50m) | `nginx -T \| grep client_max_body_size` |
| Upload lambat, disk Nginx penuh | `proxy_request_buffering on` | PART A |
| SSE putus/progress baru muncul di akhir | `proxy_buffering on` atau timeout 600 s default | PART A + timeout 3600 s di kolom proxy |
| Download besar putus | `proxy_read_timeout`/`send_timeout` | timeout 3600 s |
| `nginx: [emerg] "proxy_read_timeout" directive is duplicate` | timeout ditulis di custom configuration | hapus dari PART A; pakai kolom timeout |
| `/.env` atau `/bin/filemanager` 200 | PART B tidak terpasang | tempel PART B di server block |
| SSL gagal / perpanjangan gagal | `.well-known` tidak terbaca Nginx (`www`) | `ls -ld` document root `.well-known` 0755; DNS A benar; port 80 terbuka |
| `Permission denied` di log | owner storage salah (mis. file dari root) | `sudo chown -R filemanager:filemanager /www/storage/FileManager` |
| Thumbnail/arsip gagal hanya di service | filter syscall/sandbox | `journalctl -u filemanager`; uji `systemd-run -p User=filemanager -p SystemCallFilter=@system-service …`; laporkan sebelum melonggarkan unit |
| OOM saat `make build` | RAM kecil | swap 2 GB atau `--binary` dari CI |
| `update.sh`: `another update is running` | update lain berjalan / lock tertinggal di proses lama | tunggu; cek `pgrep -af update.sh` |
| `update.sh`: `ROLLBACK FAILED` | binary lama juga tidak siap (mis. `.env` rusak) | baca log update + `journalctl`; perbaiki `.env`; `systemctl start filemanager` |

## 11. Membatalkan instalasi

```bash
sudo systemctl disable --now filemanager
sudo rm /etc/systemd/system/filemanager.service && sudo systemctl daemon-reload
sudo cp -a /www/wwwroot/file.kppbwi.app/.env.bak-<ts> /www/wwwroot/file.kppbwi.app/.env   # bila perlu
sudo chown www:www /www/storage/FileManager && sudo chmod 0755 /www/storage/FileManager       # kondisi semula
sudo chown -R root:root /www/storage/FileManager/.system                                      # kondisi semula
sudo userdel filemanager
```

aaPanel: hapus site Reverse Proxy **tanpa** opsi hapus direktori; hapus sertifikat dari menu SSL bila perlu.

## 12. Hasil smoke test

### 12.1 Di server — 28 Okt 2026 (smoke test nyata, Step 22-3, disetujui pemilik)

Urutan: pra-cek → `install.sh` 2× → verifikasi lokal → `site.py create/configure` → cek ACME → `site.py ssl/force-https`
→ curl publik → uji browser (Playwright Chromium lewat Cloudflare) → hapus `ADMIN_INITIAL_PASSWORD` → `update.sh` nyata +
rollback nyata. Semua perintah dijalankan dari server ini; "lewat Cloudflare" = ke `https://file.kppbwi.app` lewat
DNS publik, "langsung ke origin" = `--resolve file.kppbwi.app:443:<SERVER_IP>`.

| Pemeriksaan | Hasil |
| --- | --- |
| Pra-cek (hanya membaca) | git bersih; `install.sh --dry-run` exit 0; storage `www:www 755`, `.system root:root`; 8080 bebas; `.env` disalin ke berkas root-only; **DNS ternyata proxied Cloudflare** (bukan A langsung) → PART B ditambah real IP (7.11) sebelum site dibuat |
| `install.sh --yes` run 1 (14:35 WIB) | user `filemanager` (uid 993, nologin) dibuat; `.env` dilengkapi `APP_SECRET` (salinan `.env.bak-20261028-143551` 0600); storage → `filemanager:filemanager 0750`, `.system` chown -R; binary HEAD `7597a08` dipakai tanpa build ulang; `check-deps` sebagai `filemanager`: ffmpeg/ffprobe/vipsthumbnail/pdftoppm/7z/rg **ADA**; `migrate up` 15 migrasi; unit dipasang + start; `/readyz` 200 ≤ 2 s |
| `install.sh --yes` run 2 | `.env up to date`, `unit up to date`, `already running, nothing changed` — `MainPID` dan `ExecMainStartTimestamp` tidak berubah |
| Verifikasi lokal | `active (running)` sebagai `filemanager`, RSS 25 MB idle; `ss`: hanya `127.0.0.1:8080`; 8080 tidak di UFW; `systemd-analyze security filemanager` = **1.4 OK** (sama dengan offline); `/www/storage/FileManager` 750, `.env` 600 milik `filemanager`; journal tanpa WARN/ERROR |
| aaPanel `site.py create` + `configure` | site id 2 `project_type=proxy`; timeout 10/3600/3600 "Set successfully"; PART A dan PART B tersimpan; `nginx -t` OK; vhost berisi `proxy_pass http://127.0.0.1:8080`, `proxy_request_buffering off`, `proxy_buffering off`, `proxy_cache off`, `client_max_body_size 64m`, `proxy_read_timeout 3600s`, `location = /readyz`, `set_real_ip_from` + `real_ip_header CF-Connecting-IP`; aaPanel membuat `proxy_cache_dir` (www, di-gitignore), tidak ada `index.html`/`.htaccess`/`.user.ini` |
| ACME sebelum SSL | `/.well-known/acme-challenge/fm-test` dijawab Nginx dari document root — langsung ke origin **dan** lewat Cloudflare |
| `site.py ssl` + `force-https` | Let's Encrypt `CN=file.kppbwi.app` (28 Okt 2026 → 26 Jan 2027) lewat HTTP-01 **melalui Cloudflare**, "Certificate saved"; Force HTTPS "Setup successfully"; `openssl s_client` ke origin: `Verify return code: 0 (ok)` |
| curl publik lewat Cloudflare | `GET /healthz` 200 + `strict-transport-security` (dua header: aplikasi `max-age=31536000; includeSubDomains` dan aaPanel `max-age=31536000`); `http://…/healthz` → 301 `https://`; `/.env` `/bin/filemanager` `/backend/go.mod` `/readyz` `/metrics` `/PROGRESS.md` `/.git/config` → 404; `HEAD /healthz` → 405 (aplikasi hanya GET — perintah di bagian 6 disesuaikan) |
| curl langsung ke origin | 301 http→https; Range `/assets/*.js` `0-99` → **206** (lewat Cloudflare 200 penuh: objek cache ter-gzip, 7.11) |
| Browser (Chromium headless, locale id-ID, lewat Cloudflare) | `/login` 200 `server: cloudflare`; login `admin` dengan password awal → dipaksa ke `/change-password` → ganti → `/files`, menu user "admin"; logout + login ulang dengan password baru OK; `/auth/me` 200; log aplikasi mencatat **IP pengunjung asli** (bukan IP Cloudflare) |
| Upload 100 MB (tus, chunk 16 MB) | `done` dalam 21 s (≈ 4,8 MB/s dari server sendiri lewat Cloudflare), tanpa 413; sha256 via `files/checksum` identik dengan berkas sumber; jpg/mp4/pdf ikut `done` |
| Thumbnail di bawah unit systemd (filter syscall) | jpg (vips), mp4 (ffmpeg), pdf (pdftoppm) → 200 `image/webp` pada percobaan pertama |
| Range file user lewat Cloudflare | unduhan 100 MB `bytes=52428800-52428899` → **206**, `content-range` benar, isi identik, `cf-cache-status: DYNAMIC`; media mp4 `bytes=1000-1999` → 206 `video/mp4`, `accept-ranges: bytes` |
| SSE + task panjang | `/api/v1/events` (fetch Node, cookie sesi) terbuka **547 s** tanpa putus, 252 chunk, gap maksimum 17,3 s, 21 `: ping`; event: `task.created` 2, `task.progress` 222, `task.done` 2, `file.changed` 2, `quota.updated` 2, `notification` 2; task compress folder 2,5 GB acak: zip `max` 91 s, 7z `max` 456 s — keduanya `done`, `task-progress` di panel UI berubah tiap sampel 5 s |
| Admin → Dashboard | 200, status dependency tampil (ffmpeg, vips, pdftoppm, 7z, rg) |
| Console browser | 8 error: 6 dari skrip **Cloudflare Insights beacon** (`static.cloudflareinsights.com/beacon.min.js` + inline) yang disuntikkan Cloudflare ke HTML dan **diblokir CSP** aplikasi (tidak memengaruhi fungsi; matikan "Web Analytics"/RUM untuk host ini di dashboard Cloudflare — nama menu perlu verifikasi); 2 × `401` = `/auth/me` sebelum login (normal) |
| Setelah uji | `ADMIN_INITIAL_PASSWORD` dihapus dari `.env` (tetap `filemanager` 0600), `systemctl restart` → ready 3 s tanpa WARN/ERROR; 7 item uji (7,97 GB) dihapus permanen lewat API |
| `update.sh --binary` (binary sama) | backup `app-20261028-145807-pre-update.db`; `schema 15 → 15`; **UPDATE OK in 2 s**; downtime `/readyz` ≈ 0,8 s (polling 250 ms) |
| `update.sh` dengan binary yang `serve`-nya tidak pernah siap | tunggu 30 s → `ROLLBACK` → **ROLLBACK OK in 32 s**, downtime ≈ 30,8 s; service kembali `7597a08`; binary gagal disimpan `bin/filemanager.failed` (dihapus setelah uji), `bin/filemanager.prev` tetap |
| Belum diuji | perpanjangan SSL otomatis (cron aaPanel; jatuh tempo Jan 2027, **perlu verifikasi** saat tiba — jalur HTTP-01 lewat Cloudflare sudah terbukti); CI `deploy-scripts`/`install-smoke`/`docker` belum pernah berjalan (tidak ada remote git); `hidepid=2` (7.8) tidak diterapkan |

### 12.2 Tanpa mengubah server (25 Okt 2026)

| Pemeriksaan | Hasil |
| --- | --- |
| `shellcheck -x deploy/*.sh deploy/test/*.sh deploy/test/shim/*` (0.9.0) | bersih |
| `sudo bash deploy/install.sh --dry-run` di server | exit 0; snapshot sebelum/sesudah identik (storage, `.env`, `/etc/systemd/system`, `/etc/passwd`, `/etc/fstab`, swap, git) |
| `deploy/test/install_test.sh` (folder scratch + shim systemctl) | dry-run tidak membuat apa pun; run 1 & 2 exit 0, run 2 tanpa perubahan/restart; `.env` berkomentar dibersihkan + backup; Debian ditolak |
| `deploy/test/update_rollback_test.sh` | binary baik → UPDATE OK; binary yang tidak pernah siap → rollback **32 s** (timeout 30 s); migrasi baru → `migrate down`; `down` gagal → DB dari backup pre-update |
| `deploy/test/nginx_snippet_test.sh` (Nginx aaPanel 1.30.6, prefix scratch, template server block Reverse Proxy) | `nginx -t` OK; 21 path sensitif → 404 dari Nginx; ACME dari document root; `/s/<token>` tidak di access log; 60 MiB lolos, 70 MiB → 413; Range 206; `proxy_cache` global tidak terpakai |
| `systemd-analyze verify deploy/filemanager.service` | tanpa pesan (exit 0) |
| `systemd-analyze security --offline=yes deploy/filemanager.service` | **1.4 OK** (unit belum dipasang; nilai final dicatat setelah install) |

## 13. Origin konten terpisah (opsional, Step 23e)

**Keputusan pemilik.** Bagian ini mengaktifkan alternatif (a) SPEC ADR-06: file user (gambar, video, PDF, unduhan,
zip, thumbnail, playlist HLS, konten share publik) disajikan dari host **kedua** `usercontent.file.kppbwi.app` yang
**tidak punya cookie sama sekali**, dan viewer HTML mendapat pratinjau langsung di iframe `sandbox`. **Tidak ada yang
berubah di produksi sampai `CONTENT_BASE_URL` diisi di `.env`** — kode 23e sudah ada di binary, tetapi dengan variabel
kosong perilaku dan header identik dengan v1.0. Boleh tidak diaktifkan sama sekali; boleh dibatalkan kapan saja (13.6).

Prasyarat: Step 22 sudah jalan (site `file.kppbwi.app` aktif), akses DNS Cloudflare, login aaPanel, `sudo`.
Binary yang berjalan harus memuat Step 23e (`git log --oneline -1` ≥ commit 23e; bila service masih binary lama,
`sudo bash deploy/update.sh --yes` dulu — langkah 13.4 melakukannya juga).

### 13.1 DNS (Cloudflare)

1. Cloudflare → zona `kppbwi.app` → **DNS** → **Records** → **Add record** (nama menu **perlu verifikasi**).
2. Type **A**, Name `usercontent.file` (hasil `usercontent.file.kppbwi.app`), IPv4 address `<SERVER_IP>` (IP origin
   yang sama dengan `file.kppbwi.app`), **Proxy status: Proxied** (awan oranye) — sama seperti record `file`. Bila
   server punya AAAA untuk `file`, tambahkan AAAA yang sama; bila tidak, cukup A.
3. **SSL/TLS** → mode **Full (strict)** sudah berlaku untuk seluruh zona (dipilih di Step 22 — cek sekali lagi); tidak
   ada pengaturan per-host yang perlu diubah. Jangan aktifkan "Always Use HTTPS" khusus untuk host ini (Force HTTPS
   aaPanel yang mengalihkan).
4. Cek dari server (DNS publik; jawaban = IP Cloudflare, bukan IP origin, seperti `file.kppbwi.app`):

   ```bash
   dig +short usercontent.file.kppbwi.app A
   ```

### 13.2 Site Reverse Proxy kedua di aaPanel

Jalur otomatis (API internal panel yang sama dengan Step 22; **terverifikasi** untuk site utama 28 Okt 2026, opsi
`--domain/--snippet` baru di 23e — **perlu verifikasi** pada jalannya pertama):

```bash
cd /www/wwwroot/file.kppbwi.app
P=/www/server/panel/pyenv/bin/python3
sudo $P deploy/aapanel/site.py --domain usercontent.file.kppbwi.app --snippet deploy/nginx-usercontent.conf --remark "FileManager content" status
sudo $P deploy/aapanel/site.py --domain usercontent.file.kppbwi.app --snippet deploy/nginx-usercontent.conf --remark "FileManager content" create
sudo $P deploy/aapanel/site.py --domain usercontent.file.kppbwi.app --snippet deploy/nginx-usercontent.conf --remark "FileManager content" configure
```

Jalur UI (nama menu/tombol **perlu verifikasi**, sama dengan 4.A):

1. aaPanel → **Website** → tab **Reverse Proxy** → **Add**: Domain `usercontent.file.kppbwi.app`; Target URL
   `http://127.0.0.1:8080` (**binary yang sama**, bukan port lain); Send domain `$http_host`; Remark
   `FileManager content`. Simpan. Jangan aktifkan cache.
2. Site `usercontent.file.kppbwi.app` → **Proxy** → baris `/` → **Edit**: timeout **10 / 3600 / 3600**; **Custom
   configuration** ← **PART A** dari `deploy/nginx-usercontent.conf`; Cache **off**; WebSocket boleh tetap aktif.
3. Site → **Config file** → **server block** ← **PART B** dari `deploy/nginx-usercontent.conf` (allowlist route konten
   + metode `GET|HEAD|OPTIONS` + real-IP Cloudflare). Simpan → aaPanel `nginx -t` + reload.
4. Site directory baru `/www/wwwroot/usercontent.file.kppbwi.app` dibuat aaPanel (berisi `index.html`/`404.html`
   bawaan — tidak pernah disajikan karena semua path di-proxy atau 404; boleh dihapus). **Jangan** menghapus proyek
   dengan opsi hapus direktori.
5. Cek (hanya membaca):

   ```bash
   nginx -T 2>/dev/null | sed -n '/server_name usercontent.file.kppbwi.app/,/^}/p' | grep -nE 'proxy_pass|proxy_cache|access_log|request_method|public/shares|real_ip_header'
   ```

   Harus terlihat `proxy_pass http://127.0.0.1:8080`, `proxy_cache off`, `access_log off`, baris `if ($uri !~ …public/shares…)`,
   `if ($request_method !~ …)`, `real_ip_header CF-Connecting-IP`.

### 13.3 SSL Let's Encrypt + Force HTTPS

```bash
sudo $P deploy/aapanel/site.py --domain usercontent.file.kppbwi.app ssl           # HTTP-01 lewat Cloudflare, seperti site utama
sudo $P deploy/aapanel/site.py --domain usercontent.file.kppbwi.app force-https
```

atau UI: site → **SSL** → **Let's Encrypt** → centang domain → verifikasi file → **Apply** → **Force HTTPS**
(perlu verifikasi nama tombol). ACME tetap bekerja walau PART B mem-404-kan hampir semuanya: `/.well-known/`
dikecualikan dari aturan `if` dan dilayani dari document root oleh include well-known aaPanel (diuji
`deploy/test/nginx_snippet_test.sh`). Sebelum variabel diisi, host ini menjawab semua path (termasuk `/`) dengan
`404` — itu normal.

### 13.4 Aktifkan di aplikasi

1. Edit `.env` sebagai root (file 0600 milik `filemanager`; jangan pernah menampilkan isinya di terminal/log):

   ```bash
   sudo sed -i 's|^#\? *CONTENT_BASE_URL=.*$|CONTENT_BASE_URL=https://usercontent.file.kppbwi.app|' /www/wwwroot/file.kppbwi.app/.env
   sudo grep -c '^CONTENT_BASE_URL=https://usercontent.file.kppbwi.app$' /www/wwwroot/file.kppbwi.app/.env   # 1
   ```

   (bila barisnya belum ada sama sekali, tambahkan `CONTENT_BASE_URL=https://usercontent.file.kppbwi.app` tanpa
   komentar di belakang nilai). Nilai harus origin saja, `https`, host berbeda dari `APP_BASE_URL` — selain itu
   service menolak start (`configuration error(s)`), dan `update.sh` akan rollback otomatis.
2. Restart lewat alur update (backup DB + rollback otomatis; tanpa migrasi):

   ```bash
   sudo bash deploy/update.sh --yes
   ```

### 13.5 Verifikasi

Dari laptop (lewat Cloudflare; `H()` seperti di bagian 6):

```bash
H() { curl -s -D - -o /dev/null "$@"; }
# host konten: bukan aplikasi — 404 JSON untuk SPA/API/share page, tanpa cookie, CORS hanya untuk origin aplikasi
for p in / /login /s/x /api/v1/auth/me /healthz /api/v1/files/list; do
  printf '%-22s %s\n' "$p" "$(curl -s -o /dev/null -w '%{http_code}' https://usercontent.file.kppbwi.app$p)"; done   # semua 404
H https://usercontent.file.kppbwi.app/api/v1/auth/me | grep -iE '^HTTP|set-cookie|content-type'   # 404, application/json, TANPA set-cookie
# route konten tanpa tanda tangan: 403 dari aplikasi (bukan 404 Nginx) → proxy jalan
H 'https://usercontent.file.kppbwi.app/api/v1/files/raw?path=%2Fa&purpose=media' | grep -iE '^HTTP|access-control|cross-origin-resource'
#   HTTP/2 403 · access-control-allow-origin: https://file.kppbwi.app · cross-origin-resource-policy: cross-origin
curl -s -D - -o /dev/null -X OPTIONS -H 'Origin: https://file.kppbwi.app' -H 'Access-Control-Request-Method: GET' \
  -H 'Access-Control-Request-Headers: range' 'https://usercontent.file.kppbwi.app/api/v1/files/raw?path=%2Fa' | grep -iE '^HTTP|access-control'
#   204 · allow-origin https://file.kppbwi.app · allow-methods GET, HEAD · allow-headers Range · max-age 600
# origin aplikasi: route konten dialihkan ke origin konten
H 'https://file.kppbwi.app/api/v1/files/raw?path=%2Fa&purpose=media' | grep -iE '^HTTP|^location'
#   HTTP/2 302 · location: https://usercontent.file.kppbwi.app/api/v1/files/raw?path=%2Fa&purpose=media
H https://file.kppbwi.app/healthz | grep -iE '^HTTP'                              # 200 (aplikasi tetap di origin lama)
# CSP aplikasi memuat origin konten
H https://file.kppbwi.app/login | grep -i content-security-policy | grep -oE '(img|media|connect|frame)-src[^;]*'
```

Di browser (login seperti biasa di `https://file.kppbwi.app`):

1. DevTools → Network: buka gambar/video/PDF di viewer → request `files/raw?…sig=` menuju
   `usercontent.file.kppbwi.app`, status 200/206, **tanpa** header `Cookie` pada request, `access-control-allow-origin:
   https://file.kppbwi.app` pada respons; thumbnail di listing juga dari host konten.
2. Buka file `.html` → tab **Pratinjau** merender dokumen di iframe (skrip di dalamnya tidak jalan; catatan
   "Pratinjau berjalan di sandbox…"), tab **Tampilkan source** menampilkan markup. Tanpa variabel, hanya sumber.
3. Unduh file → unduhan tetap berjalan (URL absolut ke host konten, `Content-Disposition: attachment`).
4. Link share publik berpassword → buka password → gambar/thumbnail tamu dimuat dari host konten (`…/public/shares/<token>/raw?…sig=`).
5. Bila `transcode.enabled` aktif: "Putar dengan konversi" → playlist + segmen `files/hls/…` dari host konten.
6. Konsol tanpa pelanggaran CSP (origin konten ada di `img-src/media-src/connect-src/frame-src`).

Di server: `journalctl -u filemanager -n 50` tanpa WARN/ERROR; `curl -s http://127.0.0.1:8080/readyz` 200.

### 13.6 Rollback

Kosongkan variabel dan jalankan update lagi — aplikasi kembali ke perilaku v1 seketika (URL bertanda tangan relatif,
host aplikasi menyajikan konten sendiri, viewer HTML sumber saja):

```bash
sudo sed -i 's|^CONTENT_BASE_URL=.*$|CONTENT_BASE_URL=|' /www/wwwroot/file.kppbwi.app/.env
sudo bash deploy/update.sh --yes
```

Site aaPanel kedua boleh dibiarkan (semua path 404) atau dihapus **tanpa** opsi hapus direktori; record DNS boleh
tetap. URL bertanda tangan yang masih hidup (≤ 2 jam) dan menunjuk ke host konten akan gagal setelah rollback —
pemutar/viewer meminta tanda tangan baru sendiri (`SIGNATURE_*` → sign ulang), tab yang terbuka cukup dimuat ulang.

### 13.7 Troubleshooting

| Gejala | Penyebab umum | Tindakan |
| --- | --- | --- |
| Service tidak start setelah 13.4 (`update.sh` rollback) | `CONTENT_BASE_URL` berisi path/`http`/host sama dengan `APP_BASE_URL` | perbaiki nilai (origin saja, `https`, host `usercontent…`), ulangi `update.sh` |
| Gambar/video tidak tampil, konsol "blocked by CORS" | PART A/B belum terpasang di site kedua atau `proxy_pass` ke port lain | `nginx -T` seperti 13.2 langkah 5; harus `127.0.0.1:8080` |
| `https://usercontent…/api/v1/files/raw?…` → 404 halaman Nginx | aturan `if` PART B salah ketik / route di luar allowlist | tempel ulang PART B dari repo; path yang sah ada di komentar PART B |
| Semua path host konten 404 termasuk raw | normal bila `CONTENT_BASE_URL` masih kosong (aplikasi belum tahu host itu) — tetapi raw tetap harus 403, bukan 404, setelah 13.4 | cek `.env` + restart |
| Unduhan/pratinjau meminta tanda tangan berulang | jam server/klien, atau rollback sebagian (variabel kosong tetapi tab lama) | muat ulang tab; `timedatectl` |
| Sertifikat kedua gagal | DNS belum propagasi / record tidak proxied sama seperti `file` | `dig`, ulangi `site.py … ssl` |

