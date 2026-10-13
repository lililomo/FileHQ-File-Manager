# Prompt Master — Aplikasi File Manager

Oct 14, 2026 · @KPP BWI

Dokumen ini adalah brief lengkap untuk AI coding agent. Tugas pertama agent: menghasilkan `PRD.md`, `SPEC.md`, dan `PROMPT.md` — belum menulis kode aplikasi.

## 0. Peran, misi & aturan kerja

Kamu bertindak sebagai satu tim senior: product manager, software architect, staff engineer (Go + React/TypeScript), security engineer, dan product designer. Misimu: merancang **web file manager multi-user** yang minimal sekuat file manager aaPanel, dengan UI modern, elegan, ringkas, dan profesional.

**Tugas di sesi ini — kerjakan berurutan:**

1. Baca seluruh prompt ini sampai habis sebelum menulis apa pun.
2. Buat `PRD.md`, `SPEC.md`, dan `PROMPT.md` di root repo. Isi wajibnya ada di bagian 15, 16, dan 17.
3. Buat juga `PROGRESS.md` (checklist semua step di `PROMPT.md`, semua belum dicentang), file .env lokal berisi nilai dari bagian 3 (masuk .gitignore), dan `.env.example`.
4. **BERHENTI.** Jangan menulis kode aplikasi. Tampilkan ringkasan singkat: keputusan utama, asumsi, risiko, dan pertanyaan terbuka untuk saya review.

**Aturan kerja:**

- Jangan mengarang. Jika kamu tidak yakin sebuah fitur ada di aaPanel, tulis "perlu verifikasi" — jangan diklaim.
- Jangan hardcode versi library dari ingatan. Tulis "versi stable terbaru saat implementasi, verifikasi di registry". Sebut versi minimum hanya jika ada alasan teknis (contoh: Go ≥ 1.25 karena method `os.Root` seperti `Rename`, `RemoveAll`, `MkdirAll`, `Symlink` baru ada di 1.25).
- Jika prompt ini ambigu, pakai default yang tertulis di sini, catat sebagai asumsi di PRD, lalu lanjutkan. Berhenti untuk bertanya hanya jika keputusannya sulit diubah belakangan.
- Jika kamu menilai ada keputusan di prompt ini yang buruk (keamanan, performa, maintainability), katakan terus terang di dokumen, beri alternatif dan trade-off-nya. Jangan diam-diam mengubah.
- Bahasa dokumen: Bahasa Indonesia, istilah teknis tetap bahasa Inggris. Kode, nama variabel, komentar kode, dan commit message: bahasa Inggris.

## 1. Ringkasan produk & prinsip

Produk ini adalah file manager web self-hosted untuk satu server Linux. Setiap user punya folder sendiri di `/www/storage/FileManager/<username>` dan tidak bisa melihat file user lain. Fitur intinya setara file manager aaPanel, ditambah code editor ala aaPanel IDE, viewer media, arsip lengkap, remote download, dan sharing.

Aplikasi berjalan di server Ubuntu yang dikelola aaPanel, dengan domain `file.kppbwi.app`. Kode aplikasi ada di `/www/wwwroot/file.kppbwi.app`, terpisah dari storage file user di `/www/storage/FileManager`.

| Persona | Kebutuhan utama |
| --- | --- |
| Admin | Membuat dan mengelola user, kuota, setting sistem, audit log. Admin juga memakai file manager untuk filenya sendiri. |
| User | Mengelola file miliknya, mengedit kode, membuka media, membagikan file. |
| Penerima share (user terdaftar) | Membuka file/folder yang dibagikan kepadanya lewat menu "Dibagikan ke saya". |
| Tamu (tanpa akun) | Membuka link publik: preview dan download. |

**Prinsip produk:**

1. Powerful tapi tidak ramai: semua fitur ada, tapi muncul saat relevan (context menu, toolbar kontekstual, menu "More").
2. Padat seperti aaPanel: tampilan file berbasis tabel dan ikon yang compact. Tidak ada hero section, kartu besar, atau elemen dekoratif yang memakan ruang.
3. Keyboard-first dan mouse-friendly: setiap aksi bisa lewat shortcut, klik kanan, dan drag & drop.
4. Aman by default: isolasi user ditegakkan di level filesystem dan API, bukan hanya disembunyikan di UI.
5. Cepat di folder besar: 10.000+ item tetap mulus (virtualisasi di frontend, sort dan paging di server).
6. Tidak ada kehilangan data tanpa konfirmasi: delete masuk Recycle Bin, simpan file membuat riwayat versi, bentrok nama memunculkan dialog konflik.

## 2. Stack teknologi

Gunakan stack di bawah sebagai default. Kamu boleh mengusulkan alternatif di SPEC hanya dengan alasan teknis yang kuat dan trade-off yang jelas.

| Lapisan | Pilihan | Alasan |
| --- | --- | --- |
| Backend | Go ≥ 1.25, `net/http` + router `chi` | Streaming file besar, HTTP Range, concurrency untuk task background, binary tunggal, memori kecil. `os.Root` untuk akses file yang tahan path traversal dan symlink escape. |
| Database | SQLite (WAL) + migrasi `goose` + query type-safe `sqlc` | Aplikasi single-server tanpa service DB terpisah. Repository layer dirancang agar bisa pindah ke PostgreSQL. |
| Upload | Protokol tus (resumable): `tusd` sebagai library di backend, `tus-js-client` di frontend | Upload ber-chunk, pause/resume, lanjut otomatis setelah koneksi putus. |
| Realtime | Server-Sent Events (SSE) | Progress task (compress, extract, remote download, copy besar) dan notifikasi. Satu arah sudah cukup, lebih sederhana dari WebSocket. |
| Arsip | `github.com/mholt/archives` + CLI `7zz` (7-Zip) + opsional `rar`/`unrar` | mholt/archives membaca zip, tar.*, rar, 7z dan menulis zip & tar.* (rar dan 7z read-only). Tulis 7z dan zip ber-password AES-256 lewat `7zz`. Membuat .rar butuh binary `rar` berlisensi: opsional, default nonaktif. |
| Thumbnail & metadata media | libvips (`vipsthumbnail` atau govips), `ffmpeg`/`ffprobe`, `pdftoppm` (poppler) | Thumbnail gambar cepat dan hemat memori, frame video, halaman pertama PDF, durasi dan dimensi. |
| Deteksi tipe & encoding | `gabriel-vasile/mimetype`, detektor charset (mis. `saintfish/chardet`) + `golang.org/x/text/encoding` | MIME dari isi file, bukan ekstensi. Editor butuh deteksi dan konversi encoding. |
| Pencarian isi file | `ripgrep` (`rg --json`) jika ada, fallback worker pool Go | Cepat, mendukung regex dan filter ekstensi. |
| Frontend | React + TypeScript + Vite, TanStack Router, TanStack Query, Zustand | Ekosistem matang, type-safe, build cepat. |
| UI kit | Tailwind CSS + shadcn/ui (Radix), lucide-react, sonner, cmdk | Komponen accessible yang bisa dikustom penuh, tidak terlihat seperti template. |
| Tabel & virtualisasi | TanStack Table + TanStack Virtual | Kolom dinamis, resize, reorder, sort, 10.000+ baris. |
| Code editor | Monaco Editor (lazy-load) | Mesin VS Code: multi-cursor, find/replace regex, diff editor untuk riwayat versi. |
| PDF viewer | pdf.js (`pdfjs-dist`) + toolbar custom | Standar de facto untuk render PDF di browser. |
| Video player | Vidstack atau Plyr (pilih satu, beri alasan) | Kontrol modern, subtitle, PiP, shortcut keyboard. |
| Audio player | `HTMLAudioElement` + UI custom | Ringan; mendukung playlist dan mini-player persisten. |
| Form & validasi | react-hook-form + zod | Skema validasi konsisten di semua dialog. |
| i18n | i18next — Bahasa Indonesia (default) dan English |  |
| Testing | `go test` + `httptest`, Vitest + Testing Library, Playwright (E2E) |  |
| Deploy | Single binary (frontend di-embed via `embed`), systemd, Dockerfile multi-stage, docker-compose (opsional), snippet Nginx untuk aaPanel | Jalan di belakang Nginx aaPanel (reverse proxy) untuk domain file.kppbwi.app; detail di bagian 3 dan 13. |

Monaco dan pdf.js berukuran besar, jadi wajib lazy-load. Di SPEC, bandingkan Monaco dengan CodeMirror 6 secara singkat lalu tetapkan satu pilihan.

## 3. Storage, isolasi user, admin & `.env`

Semua file user disimpan di `/www/storage/FileManager/<username>`, dan isolasi antar-user wajib ditegakkan di lapisan filesystem backend, bukan hanya di UI.

```text
/www/storage/FileManager/          ← STORAGE_ROOT: hanya folder user + .system
├── admin/                         ← root milik user "admin"
├── budi/                          ← root milik user "budi"
└── .system/                       ← DATA_DIR, tidak pernah terlihat oleh user
    ├── db/app.db                  ← SQLite
    ├── trash/<user_id>/           ← Recycle Bin per user
    ├── versions/<user_id>/        ← riwayat versi file dari editor
    ├── uploads/                   ← chunk upload tus sementara
    ├── thumbs/                    ← cache thumbnail
    ├── tmp/                       ← kerja sementara (zip stream, extract)
    └── logs/
```

**Aturan storage:**

- `.system` harus berada di filesystem yang sama dengan folder user agar pindah ke Recycle Bin bersifat atomik (`rename`). Jika `DATA_DIR` diarahkan ke filesystem lain, backend fallback ke copy + delete dan menulis warning saat start.
- Username: regex `^[a-z0-9][a-z0-9_-]{2,31}$`, tidak bisa diubah setelah dibuat, ada daftar nama terlarang (tentukan di SPEC). Karena tidak boleh diawali titik, tidak bisa bentrok dengan `.system`.
- Folder user dibuat **saat user pertama kali sukses login**, bukan saat akun dibuat. Operasinya idempoten: `MkdirAll` mode `0750`, simpan `home_provisioned_at`. Jika folder sudah ada (mis. username dibuat ulang), jangan ditimpa; tampilkan peringatan ke admin.
- Semua path dari client adalah path virtual relatif terhadap root user (`/` = root user). Server tidak pernah mengirim path absolut server ke client.
- Semua operasi file lewat satu package (mis. `internal/fsjail`) yang membuka root user dengan `os.OpenRoot` dan bekerja hanya lewat `*os.Root`. Handler dilarang memanggil `os.Open`, `os.Remove`, dan sejenisnya secara langsung.
- Unit test wajib untuk `fsjail`: `../`, path absolut, null byte, normalisasi Unicode, symlink ke luar root, symlink race (TOCTOU), nama dan path yang terlalu panjang.
- Proses berjalan sebagai user Linux khusus (mis. `filemanager`), bukan root. User aplikasi bukan user Linux.
- Kuota per user (default dari env, bisa diubah admin), ditegakkan saat upload, copy, extract, dan remote download. Pemakaian dihitung inkremental dan direkalkulasi berkala.

**Akun admin & autentikasi:**

- Saat start pertama dan tabel user masih kosong: buat user `ADMIN_USERNAME` dengan password `ADMIN_INITIAL_PASSWORD` dari `.env`, role `admin`, flag `must_change_password=true`.
- Login pertama admin langsung diarahkan ke halaman ganti password. Semua API lain menolak dengan `403 PASSWORD_CHANGE_REQUIRED` sampai password diganti. Password baru tidak boleh sama dengan password awal.
- Setelah admin dibuat, nilai env tidak pernah dipakai lagi (restart tidak me-reset password). Jika env masih berisi password awal setelah diganti, tulis warning di log agar dihapus.
- Tidak ada registrasi publik. Hanya admin yang membuat user; user baru juga mendapat password sementara + `must_change_password=true`.
- Hash password argon2id (parameter ditetapkan di SPEC). Minimal 12 karakter dan dicek terhadap daftar password umum.
- Session server-side di DB (bisa dicabut) via cookie `HttpOnly`, `Secure`, `SameSite=Lax`; idle timeout + absolute timeout; CSRF token untuk semua request yang mengubah state.
- Rate limit + lockout bertahap untuk login. TOTP 2FA opsional di fase akhir.
- Role: `admin` dan `user`. Secara default admin **tidak bisa** melihat isi folder user lain. Flag `ADMIN_CAN_BROWSE_USERS=false`; jika diaktifkan, setiap akses dicatat di audit log.

**`.env`** (file asli masuk `.gitignore` dengan permission `600`; `.env.example` berisi placeholder `CHANGE_ME`):

```env
APP_ENV=production
APP_BASE_URL=https://file.kppbwi.app
LISTEN_ADDR=127.0.0.1:8080
TRUSTED_PROXIES=127.0.0.1
STORAGE_ROOT=/www/storage/FileManager
DATA_DIR=/www/storage/FileManager/.system
APP_SECRET=                        # diisi installer: openssl rand -hex 32
ADMIN_USERNAME=admin
ADMIN_INITIAL_PASSWORD=IB+35t3U5+I_p5ZJcHLodfcr   # sekali pakai, wajib diganti saat login pertama
ADMIN_CAN_BROWSE_USERS=false
DEFAULT_USER_QUOTA=10GB
MAX_UPLOAD_SIZE=20GB
UPLOAD_CHUNK_SIZE=16MB
TRASH_RETENTION_DAYS=30
FILE_VERSION_LIMIT=50
EDITOR_MAX_FILE_SIZE=5MB
REMOTE_DOWNLOAD_ENABLED=true
REMOTE_DOWNLOAD_MAX_SIZE=10GB
ARCHIVE_MAX_EXTRACT_SIZE=50GB
ARCHIVE_MAX_ENTRIES=200000
SHARE_DEFAULT_EXPIRY_DAYS=7
SESSION_IDLE_TIMEOUT=2h
SESSION_ABSOLUTE_TIMEOUT=168h
FFMPEG_PATH=/usr/bin/ffmpeg
SEVENZIP_PATH=/usr/bin/7zz
RAR_PATH=
UNRAR_PATH=
RG_PATH=/usr/bin/rg
LOG_LEVEL=info
```

**Lokasi aplikasi, domain & aaPanel:**

Server memakai Ubuntu + aaPanel. Domain `file.kppbwi.app` ditambahkan lewat menu **Website** di aaPanel, bukan dengan mengedit Nginx secara manual. Kode aplikasi berada di `/www/wwwroot/file.kppbwi.app`, terpisah dari storage.

```text
/www/wwwroot/file.kppbwi.app/      ← APP_DIR: repo + hasil build, milik user filemanager
├── bin/filemanager                ← binary tunggal (frontend di-embed)
├── .env                           ← permission 600, tidak pernah bisa diakses lewat web
├── public/                        ← running directory site di aaPanel; kosong kecuali .well-known
├── deploy/                        ← unit systemd, snippet Nginx, install.sh, update.sh
└── backend/, web/, docs/ ...      ← source code

/www/storage/FileManager/          ← STORAGE_ROOT, terpisah dari APP_DIR
```

- Di aaPanel: Website → Add site, domain `file.kppbwi.app`, site directory `/www/wwwroot/file.kppbwi.app`, PHP "Static" (tanpa PHP), tanpa database dan FTP. Set running directory ke `/public` agar Nginx tidak pernah menyajikan source code, binary, atau `.env`.
- Semua request diteruskan lewat fitur Reverse Proxy site di aaPanel ke `http://127.0.0.1:8080`. Jika versi aaPanel yang terpasang punya tipe proyek Go atau "Other project", boleh dipakai sebagai alternatif — perlu verifikasi di server.
- SSL Let's Encrypt diterbitkan dari menu SSL site di aaPanel, dengan Force HTTPS aktif. Path `/.well-known/acme-challenge/` harus tetap disajikan dari document root (tidak ikut di-proxy) agar perpanjangan sertifikat tidak gagal.
- Hapus file default yang dibuat aaPanel di site directory jika ada (mis. `index.html`, `404.html`, `.htaccess`). Jika ada `.user.ini` yang terkunci `chattr +i`, buka kuncinya dulu lalu hapus.
- Config Nginx wajib untuk site ini: `proxy_request_buffering off` dan `client_max_body_size` ≥ ukuran chunk tus untuk upload; `proxy_buffering off` + timeout panjang untuk SSE dan download besar; header `Host`, `X-Forwarded-For`, `X-Forwarded-Proto`; deny semua dotfile. Tulis snippet-nya di `deploy/nginx-aapanel.conf` beserta letak menempelnya di UI aaPanel.
- Proses dijalankan systemd: `filemanager.service` dengan `User=filemanager`, `EnvironmentFile=/www/wwwroot/file.kppbwi.app/.env`, dan hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `ReadWritePaths=/www/storage/FileManager`). Jangan memakai user `www` milik aaPanel: situs PHP lain yang berjalan sebagai `www` bisa ikut membaca storage.
- `LISTEN_ADDR` hanya `127.0.0.1`; port aplikasi tidak dibuka di firewall aaPanel maupun UFW.
- Jangan mengedit file config yang dikelola aaPanel di luar UI-nya karena bisa ditimpa aaPanel. Tulis semua langkah sebagai checklist klik-per-klik di `docs/DEPLOY-AAPANEL.md`.

## 4. Paritas fitur aaPanel (batas minimal)

Semua baris bertanda "Wajib" adalah batas minimal; aplikasi ini harus setara atau lebih baik. Matriks disusun dari source code aaPanel ([aaPanel/aaPanel](https://github.com/aaPanel/aaPanel): `class/files.py`, bundle `view-files.js` dan `feature-FileEditor.js`, dicek 14 Oktober 2026). Perilaku UI bisa berbeda antar versi; jika kamu menambah klaim lain tentang aaPanel, tandai "perlu verifikasi".

| Area | Yang ada di aaPanel | Keputusan untuk aplikasi ini |
| --- | --- | --- |
| Navigasi | Breadcrumb + address bar yang bisa diketik, riwayat direktori, up/refresh, daftar disk, tab folder (close others, close right, batas jumlah tab) | Wajib: breadcrumb + address bar, back/forward/up/refresh, riwayat folder, tab folder (maks. 10). Daftar disk diganti indikator kuota user. |
| Daftar file | Kolom nama, permission/owner, ukuran, waktu modifikasi, remark, aksi; sort nama/ukuran/mtime/permission/owner; paging per halaman; list view & icon view | Wajib, lalu diperluas jadi 5 mode view dan kolom yang bisa dipilih (bagian 5). |
| Cari nama | Cari di folder aktif atau termasuk subfolder (dibatasi 3.000 hasil) | Wajib, ditambah filter tipe, ukuran, dan tanggal. |
| Cari isi file | Keyword atau regex, case-sensitive, whole word, subfolder, filter ekstensi, ukuran, waktu modifikasi; hasil per baris yang bisa dibuka di editor | Wajib. |
| Replace di banyak file | Replace hasil pencarian + log replace | Wajib, dengan preview diff sebelum eksekusi dan rollback lewat riwayat versi. |
| Buat | File baru, folder baru, soft link, buat salinan | Wajib. Soft link hanya boleh menunjuk target di dalam root user. |
| Upload | Drag & drop, upload ber-chunk dengan resume, dialog konflik (overwrite/skip), kecepatan dan total waktu | Wajib (protokol tus). |
| Remote download | URL → folder tujuan + nama file, berjalan sebagai task | Wajib, dengan proteksi SSRF (bagian 13). |
| Download | File tunggal; compress lalu download | Wajib, plus zip streaming tanpa file sementara untuk multi-select dan folder. |
| Copy/Cut/Paste | Batch, Ctrl+C/X/V, dialog konflik paste (overwrite/rename), rename, delete satu atau banyak | Wajib. |
| Recycle Bin | Aktif/nonaktif, filter tipe (file/folder/gambar/dokumen), cari, restore ke lokasi asal atau folder lain, hapus permanen, kosongkan, operasi batch | Wajib per user, plus auto-purge setelah `TRASH_RETENTION_DAYS`. |
| Compress | tar.gz, zip, rar | Wajib, plus format tambahan (bagian 9). |
| Decompress | zip, rar, gz, tgz, bz2, war; password, encoding nama file, folder tujuan, peringatan overwrite | Wajib, plus 7z, tar.xz, tar.zst, dll. |
| Preview arsip | Lihat isi, extract sebagian, tambah file ke arsip, hapus entry | Wajib (tambah/hapus entry minimal untuk zip). |
| Permission | chmod rwx owner/group/public, apply ke subfolder, owner (chown), backup/restore permission, fix permission | Wajib: chmod (mode bits) + rekursif. chown dan backup/restore permission dikecualikan (bagian 14). |
| Properties | Nama, tipe, lokasi, ukuran, user/group, uid/gid, permission, atime/mtime/ctime, info inode, MD5/SHA-1 (file ≤ 100 MB), riwayat versi + restore | Wajib, tanpa uid/gid/inode. Checksum MD5/SHA-1/SHA-256 dihitung on-demand sebagai task, plus verifikasi hash. |
| Ukuran folder | Hitung ukuran, jumlah file dan folder, analisis disk | Wajib: hitung on-demand + halaman "Analisis penyimpanan" (treemap) per user. |
| Organisasi | Favorit file/folder, pin to top, remark per file, copy path, buka di jendela baru, buka lokasi file | Wajib semuanya. |
| Share | Link share dengan kode ekstraksi (password), masa berlaku, catatan; daftar share; batalkan share | Wajib, plus share ke user tertentu (bagian 10). |
| Media | Preview gambar + thumbnail, video player dengan playlist folder; pdf.js ada di bundle | Wajib, diperluas (bagian 8). |
| Editor | Editor online multi-tab (detail di bagian 7) | Wajib. |
| Task | Antrian task: progress, estimasi sisa waktu, log, cancel | Wajib (progress via SSE). |
| Riwayat operasi | Log operasi file, cari, hapus | Wajib: "Aktivitas" per user + audit log untuk admin. |
| Terminal | Terminal di folder aktif | Dikecualikan (bagian 14). |
| Fitur panel server | Tamper protection, file sync (rsync), transfer cloud storage, malware scan, composer/git, konversi format | Dikecualikan: fitur panel server, bukan file manager. |

## 5. Tampilan file: mode view, kolom & sort

Default view adalah Details karena paling mirip aaPanel. Pilihan view disimpan per user di server, dengan opsi "ingat view per folder".

| Mode | Isi | Ikon/thumbnail | Kegunaan |
| --- | --- | --- | --- |
| Details | Tabel dengan kolom pilihan; sortable, resizable, reorderable | 16 px | Default, kerja sehari-hari |
| List | Ikon kecil + nama, mengalir multi-kolom (atas ke bawah, lalu kiri ke kanan) seperti Windows Explorer | 16 px | Melihat banyak nama sekaligus |
| Tiles | Grid kartu kecil: ikon/thumbnail + nama + 2 baris metadata (tipe, ukuran/tanggal) | 48 px | Ringkas tapi informatif |
| Large icons | Grid thumbnail + nama (maks. 2 baris, ellipsis di tengah) | 96–128 px | Melihat gambar |
| Extra large icons | Grid thumbnail besar + nama | 192–256 px | Melihat gambar/video lebih detail |

**Thumbnail:**

- Tampil di Tiles, Large icons, dan Extra large icons. Lazy-load lewat IntersectionObserver, jumlah request paralel dibatasi, ikon tipe file sebagai placeholder, cache HTTP (ETag + `Cache-Control: private`).
- Dibuat di server untuk: gambar (jpg, png, webp, gif frame pertama, bmp, tiff, avif, heic jika libvips mendukung), video (frame di sekitar 10% durasi), PDF (halaman 1), audio (cover art embedded), SVG (dirasterisasi di server, tidak pernah di-inline ke halaman).
- Ukuran 128/256/512 px, format WebP, cache key = hash(path, ukuran, mtime). Thumbnail dihapus saat file dihapus atau berubah.

**Kolom** (dipilih lewat menu "Kolom" di toolbar dan klik kanan pada header):

| Kolom | Default tampil | Catatan |
| --- | --- | --- |
| Nama | Ya, selalu | Ikon tipe, badge share/favorit/pin, rename inline |
| Ukuran | Ya | Folder menampilkan jumlah item; ukuran folder hanya setelah dihitung |
| Tipe | Ya | Label manusiawi dari MIME, mis. "Gambar JPEG" |
| Diubah | Ya | Format relatif + tooltip tanggal absolut, zona waktu user |
| Catatan | Ya | Setara kolom remark ("PS") di aaPanel |
| Dibuat | Tidak | Birth time dari `statx`; "—" jika filesystem tidak mendukung |
| Diakses | Tidak | atime |
| Ekstensi | Tidak |  |
| Permission | Tidak | `rwxr-xr-x` + `755` |
| Jumlah item | Tidak | Khusus folder |
| Dimensi | Tidak | Gambar/video, dari cache metadata |
| Durasi | Tidak | Audio/video, dari cache metadata |
| Status share | Tidak | Publik, ke user, atau tidak dibagikan |
| Lokasi | Hanya di hasil pencarian dan Recent | Path relatif |

**Sort:**

- Klik header = asc → desc → default. Shift+klik = sort sekunder. Ada indikator arah di header.
- Di view selain Details: dropdown "Urutkan berdasarkan" dengan field dan arah yang sama.
- Sort dikerjakan di server agar benar di folder besar yang di-paging. Nama memakai natural sort, case-insensitive, locale-aware (`file2` sebelum `file10`). Field: nama, ukuran, tipe, ekstensi, diubah, dibuat, diakses, permission, catatan, dimensi, durasi. Dimensi dan durasi memakai cache metadata; nilai yang belum diketahui ditaruh paling akhir.
- Opsi "Folder di atas" (default aktif). Item yang di-pin selalu paling atas, seperti aaPanel.
- Lebar, urutan, visibilitas kolom, dan sort disimpan per user.

**Loading data:**

- API listing mengembalikan halaman yang sudah tersortir (default 500 item, cursor-based); frontend memakai virtualisasi + infinite scroll. Target: folder 10.000 item tampil dalam < 1 detik.
- Filter cepat di folder aktif (ketik untuk menyaring) dan type-ahead (ketik huruf untuk lompat ke item).

**Seleksi & interaksi:**

- Klik, Ctrl/Cmd+klik, Shift+klik (rentang), Ctrl+A, invert selection, marquee selection di view grid, checkbox di Details.
- Double-click atau Enter = buka (folder dimasuki; file dibuka di viewer/editor sesuai tipe). Space = Quick Look (preview cepat di modal).
- Drag & drop item ke folder, breadcrumb, atau sidebar = move (tahan Ctrl = copy). Drag file dari OS = upload.
- Panel kanan Preview/Details (toggle): preview, metadata, aksi cepat.
- Status bar bawah: jumlah item, jumlah terpilih + total ukurannya, kuota terpakai.
- Setiap view punya empty state, loading skeleton, dan error state.

## 6. Operasi file & fitur tambahan

Setiap aksi harus bisa dijalankan lewat toolbar, klik kanan, dan keyboard. Aksi yang tidak berlaku untuk seleksi saat ini disembunyikan, bukan sekadar dinonaktifkan.

**Matriks context menu:**

| Konteks | Aksi |
| --- | --- |
| 1 file | Buka, Buka dengan (Editor/Viewer/Download), Quick Look, Download, Share, Salin link share, Favorit, Pin, Catatan, Tag warna, Copy, Cut, Duplikat, Rename, Pindah ke…, Salin ke…, Compress, Extract (jika arsip), Lihat isi arsip, Permission, Buat symlink, Checksum, Riwayat versi, Copy path, Buka lokasi file (dari Search/Recent), Properties, Delete |
| 1 folder | Buka, Buka di tab baru, Download sebagai zip, Share, Favorit, Pin, Catatan, Tag warna, Copy, Cut, Duplikat, Rename, Pindah ke…, Salin ke…, Compress, Hitung ukuran, Upload ke sini, Cari di folder ini, Permission, Copy path, Properties, Delete |
| Banyak item | Download zip, Copy, Cut, Pindah ke…, Salin ke…, Compress, Batch rename, Permission, Favorit, Tag warna, Delete |
| Area kosong | Baru (file/folder/dari template/symlink), Upload file, Upload folder, Remote download, Paste, Refresh, Pilih semua, View, Urutkan, Kolom, Properties folder ini |
| Recycle Bin | Restore, Restore ke…, Hapus permanen |

**Aturan operasi inti:**

- Baru: file kosong, folder, file dari template (txt, md, html, css, js, ts, json, php, py, sh, yaml), symlink.
- Copy/Cut/Paste memakai clipboard aplikasi yang berlaku lintas folder dan tab, plus dialog "Pindah ke…/Salin ke…" dengan folder picker.
- Dialog konflik: Timpa, Lewati, Ganti nama otomatis (`nama (1).ext`), bandingkan ukuran dan tanggal, checkbox "Terapkan ke semua".
- Copy/move besar berjalan sebagai task background yang bisa dibatalkan. Move di filesystem yang sama memakai `rename` atomik.
- Validasi: tidak bisa paste folder ke dalam dirinya sendiri; nama berisi `/`, null byte, atau karakter kontrol ditolak; nama yang bermasalah di Windows (`CON`, `aux`, trailing dot/space) diberi warning.
- Rename inline (F2). Batch rename: find/replace, regex, penomoran `{n}`, ubah huruf besar/kecil, preview sebelum eksekusi.
- Delete memindahkan ke Recycle Bin. Shift+Delete = hapus permanen dengan konfirmasi; untuk lebih dari 50 item, user harus mengetik `HAPUS`.
- Undo untuk operasi terakhir (rename, move, delete ke Recycle Bin) lewat toast "Urungkan" selama 10 detik.

**Fitur tambahan di luar aaPanel:**

- Sidebar: File Saya, Recent, Favorit, Dibagikan ke saya, Share saya, Recycle Bin, Aktivitas, Analisis penyimpanan, Admin (khusus admin).
- Tab folder ala browser. Dual-pane (dua panel berdampingan untuk drag antar folder) sebagai fitur fase akhir.
- Command palette (Ctrl/Cmd+K): lompat ke folder, jalankan aksi, cari file.
- Pencarian global di seluruh root user dengan filter tipe, ukuran, tanggal, ekstensi; riwayat pencarian.
- Tag warna (merah, oranye, kuning, hijau, biru, ungu, abu) yang bisa difilter.
- Upload folder dengan struktur utuh; paste gambar dari clipboard OS menjadi file PNG.
- Preview read-only untuk teks, kode (syntax highlight), Markdown, CSV (tabel), JSON (tree) tanpa membuka editor.
- Analisis penyimpanan: treemap + 50 file terbesar per user.
- Notifikasi in-app: task selesai atau gagal, ada file yang dibagikan ke kamu.
- Duplicate finder (ukuran → hash) sebagai fitur fase akhir.

**Shortcut keyboard** (usulan; finalkan di SPEC dan hindari kombinasi yang tidak bisa dicegat browser seperti Ctrl+N, Ctrl+Shift+N, Ctrl+T, Ctrl+W):

| Shortcut | Aksi |
| --- | --- |
| Ctrl/Cmd+C, X, V | Copy, Cut, Paste |
| Delete / Shift+Delete | Ke Recycle Bin / hapus permanen |
| F2 | Rename |
| Ctrl+A / Ctrl+I | Pilih semua / invert seleksi |
| Enter / Space | Buka / Quick Look |
| Backspace atau Alt+↑ | Ke folder induk |
| Alt+← / Alt+→ | Back / Forward |
| Ctrl+F | Cari di folder aktif |
| Ctrl+K | Command palette |
| Alt+N / Alt+Shift+N | Folder baru / file baru |
| Ctrl+U | Upload |
| Ctrl+1 … Ctrl+5 | Details, List, Tiles, Large icons, Extra large icons |
| Ctrl+Z | Undo |
| ? | Daftar shortcut |

## 7. Code editor (gaya aaPanel IDE)

Editor dibuka sebagai workspace penuh (route `/editor`) atau modal besar yang bisa di-maximize, seperti aaPanel. Batas minimalnya adalah fitur yang terlihat di bundle editor aaPanel: toolbar (save, save all, search, replace, jump to line, font size, theme), tab (close, close all, close others), explorer sidebar dengan pencarian, footer (line/col, encoding, tab size, bahasa, lokasi file, jumlah riwayat, mode read-only), dan dialog encoding, line ending, tab size, theme, serta riwayat file.

```text
┌ Toolbar: Save · Save all · Find · Replace · Go to line · Format · Font size · Theme · Settings ┐
├ Explorer (tree + filter) │ Tabs:  app.js ● │ index.php │ .env                          │
│                          │ Monaco editor (minimap opsional)                            │
├──────────────────────────┴──────────────────────────────────────────────────────────────┤
└ Status: Ln 12, Col 4 · UTF-8 · LF · Spaces: 4 · JavaScript · 12 KB · Riwayat (7) · Read-only  ┘
```

**Fitur wajib:**

- Multi-tab: indikator belum disimpan (●), close, close others, close to the right, close all, dialog "Simpan / Jangan simpan / Batal", reorder tab, maks. 20 tab, tab dipulihkan setelah reload.
- Explorer sidebar: tree folder (lazy-load), filter nama, buat/rename/hapus file dan folder, sinkron dengan file manager.
- Simpan: Ctrl/Cmd+S, Save all Ctrl/Cmd+Shift+S, tutup tab Alt+W (sama seperti aaPanel). Auto-save opsional (default off). Draft lokal otomatis yang bisa dipulihkan setelah browser crash, setara fitur draft aaPanel.
- Deteksi konflik: simpan membawa `ETag`/mtime versi yang dibuka. Jika file sudah berubah di server, tampilkan dialog: lihat diff, timpa (force save), atau muat ulang.
- Riwayat versi: setiap simpan membuat versi (maks. `FILE_VERSION_LIMIT`; tidak dibuat jika isinya sama). Daftar versi dengan waktu dan ukuran, buka read-only, diff side-by-side (Monaco diff editor) terhadap versi sekarang, restore.
- Encoding: deteksi otomatis (UTF-8, UTF-8 BOM, UTF-16 LE/BE, GBK, Big5, Shift\_JIS, ISO-8859-1, Windows-1252), tampil di status bar, plus "Reopen with encoding" dan "Save with encoding".
- Line ending LF/CRLF: tampilkan, bisa diubah, dan aslinya dipertahankan. Jangan otomatis mengonversi CRLF ke LF seperti aaPanel; jadikan pilihan eksplisit.
- Bahasa: otomatis dari ekstensi, shebang, atau nama file (`Dockerfile`, `Makefile`, `.env`, `nginx.conf`, `.htaccess`), bisa diganti manual. Minimal semua mode di config editor aaPanel: Apache conf, Batch, C/C++, C#, CSS, Dockerfile, Go, HTML/Vue, Java, JavaScript/JSX, JSON, JSP, LESS, Lua, Makefile, Markdown, MySQL/SQL/pgSQL, Nginx, INI, Perl, PHP (+ Blade), PowerShell, Python, R, Ruby, Rust, SASS/SCSS, Shell, Swift, TypeScript, VBScript, Verilog, XML, YAML. Bahasa yang tidak tersedia bawaan di Monaco (mis. Nginx, Apache conf) dibuat dengan grammar Monarch sederhana.
- Find/replace dalam file (regex, case, whole word), Find in files (memakai content search backend; klik hasil membuka file di baris itu), Go to line (Ctrl+G), command palette editor (F1), multi-cursor, folding, bracket matching.
- Format document untuk bahasa yang didukung Monaco (JSON, CSS, HTML, JS/TS). Prettier opsional dan di-lazy-load.
- Pengaturan per user: theme (mengikuti light/dark aplikasi + beberapa pilihan), font (JetBrains Mono default, Consolas, Menlo, Ubuntu Mono), ukuran font, tab size, spaces/tabs, word wrap, minimap, render whitespace, line numbers.
- Markdown: preview side-by-side.
- File besar: di atas `EDITOR_MAX_FILE_SIZE` dibuka read-only dalam mode tail (N baris terakhir, seperti aaPanel untuk file > 3 MB) dengan tombol "muat lebih banyak". File biner ditolak dengan pesan jelas dan tawaran download.
- Editor juga dipakai untuk file di folder yang dibagikan dengan permission Editor.
- Tidak ada terminal terintegrasi (alasan di bagian 14).

## 8. Viewer & player media

Semua viewer terbuka di overlay layar penuh dengan prev/next ke file sejenis di folder yang sama, tombol download, share, info, dan close (Esc). URL viewer bisa di-bookmark (deep link).

**Image viewer:**

- Format: jpg, jpeg, png, gif (animasi), webp, avif, bmp, ico, svg, serta tiff dan heic lewat konversi server (jika libvips mendukung). SVG ditampilkan sebagai `<img>` atau hasil raster, tidak pernah di-inline ke DOM.
- Zoom (scroll, pinch, tombol, 1:1, fit, fill), pan, rotate 90°, flip, fullscreen, slideshow dengan interval, filmstrip thumbnail di bawah.
- Panel info: dimensi, ukuran, EXIF (kamera, lensa, tanggal); koordinat GPS disembunyikan secara default.
- Tampilkan versi resized (mis. 2048 px) untuk kecepatan; original dimuat saat zoom 100%. Preload gambar berikutnya.

**PDF viewer (pdf.js):**

- Render halaman dengan virtualisasi, sidebar thumbnail halaman, outline/bookmark, zoom (fit width, fit page, persen), cari teks dengan highlight, lompat ke halaman, rotasi, print, download.
- Streaming lewat HTTP Range agar PDF besar mulai tampil sebelum selesai diunduh. PDF ber-password memunculkan input password.

**Music player:**

- Format: mp3, m4a/aac, ogg, opus, wav, flac, sesuai dukungan browser; tampilkan pesan jelas jika tidak didukung.
- Play/pause, prev/next, seek, volume, mute, shuffle, repeat (off/all/one), kecepatan putar.
- Playlist otomatis dari folder; bisa disusun ulang dan ditambah dari seleksi.
- Metadata judul, artis, album, dan cover dari tag ID3/Vorbis (dibaca backend). Integrasi Media Session API untuk kontrol dari OS.
- Mini-player persisten di bawah layar yang tetap memutar saat user berpindah folder.

**Video player:**

- Format: mp4 (H.264/AAC), webm, ogv, mov jika codec-nya didukung. MKV, HEVC, dan AVI bergantung pada browser: deteksi dengan `canPlayType` + `ffprobe`, lalu tampilkan pesan jelas + tombol download jika tidak bisa diputar.
- Transcoding on-the-fly **tidak** termasuk scope awal (opsional fase akhir via ffmpeg/HLS, dengan batas CPU).
- Streaming lewat HTTP Range (seek instan), kecepatan 0.25–2×, fullscreen, Picture-in-Picture, subtitle sidecar `.vtt`/`.srt` bernama sama (srt dikonversi ke vtt di server), lanjut dari posisi terakhir, playlist video di folder (seperti aaPanel).
- Shortcut: Space, ←/→ (5 detik), J/K/L, F (fullscreen), M (mute).

**Viewer lain:**

- Teks dan kode: read-only dengan syntax highlight + tombol "Edit".
- Markdown dirender dengan HTML yang disanitasi. CSV/TSV sebagai tabel. JSON sebagai tree.
- HTML: default tampilkan source. Preview opsional hanya di iframe `sandbox` tanpa script, dari origin konten terpisah.
- Office (docx/xlsx/pptx): di luar scope awal; tampilkan info + download.
- Arsip: daftar isi (bagian 9). Tipe lain: kartu info file + download.

Semua konten user disajikan dengan `X-Content-Type-Options: nosniff`. Tipe berbahaya (html, svg, xml, js) dikirim sebagai `Content-Disposition: attachment` kecuali lewat jalur viewer yang aman.

## 9. Arsip, upload, download & remote download

Semua proses berat (compress, extract, remote download, zip besar) berjalan sebagai task background dengan progress, estimasi sisa waktu, log, dan tombol cancel yang membersihkan hasil parsial.

**Matriks format arsip:**

| Format | Extract | Compress | Password | Engine |
| --- | --- | --- | --- | --- |
| zip | Ya | Ya | Extract ZipCrypto & AES; compress AES-256 | mholt/archives + `7zz` |
| 7z | Ya | Ya | Ya (AES-256, opsi enkripsi nama file) | `7zz` |
| rar (v4/v5) | Ya | Hanya jika binary `rar` berlisensi terpasang | Extract ya | mholt/archives atau `unrar`; `rar` |
| tar | Ya | Ya | — | Go |
| tar.gz / tgz | Ya | Ya | — | Go |
| tar.bz2 / tbz2 | Ya | Ya | — | Go |
| tar.xz / txz | Ya | Ya | — | Go |
| tar.zst | Ya | Ya | — | Go |
| gz, bz2, xz, zst (file tunggal) | Ya | Ya | — | Go |
| war, jar | Ya (diperlakukan sebagai zip) | Opsional | — | Go |
| iso | Opsional, read-only | Tidak | — | `7zz` |

**Compress:** format, nama output (default: nama item + ekstensi), lokasi, level (store/fast/normal/max), password (zip AES atau 7z), split volume (opsional), opsi "hapus asli setelah sukses" (default off).

**Extract:** tujuan (folder ini / folder baru bernama arsip sebagai default / pilih folder), password, encoding nama file untuk zip lama (auto, UTF-8, GBK, CP437, Shift\_JIS — aaPanel punya opsi encoding serupa), penanganan konflik (timpa/lewati/rename), preview isi sebelum extract.

**Archive browser:** buka arsip seperti folder (tree read-only, ukuran asli dan terkompresi), preview teks/gambar di dalam arsip tanpa extract penuh, extract item terpilih, tambah file dan hapus entry (minimal untuk zip; format lain tampilkan alasan jika tidak didukung).

**Keamanan arsip (wajib + unit test):**

- Zip-slip: entry dengan `..`, path absolut, atau symlink/hardlink yang mengarah keluar folder tujuan ditolak. Device file dan FIFO dilewati.
- Zip bomb: hentikan jika total ukuran hasil > min(`ARCHIVE_MAX_EXTRACT_SIZE`, sisa kuota), jumlah entry > `ARCHIVE_MAX_ENTRIES`, atau rasio kompresi ekstrem. Arsip bersarang tidak diekstrak otomatis.
- Extract ke `DATA_DIR/tmp` dulu, lalu dipindah atomik ke tujuan. Mode file 0640 dan folder 0750; bit setuid/setgid dari arsip diabaikan.
- CLI dipanggil dengan argument array (tanpa shell), `--` sebelum nama file, timeout, `nice`/`ionice`.
- Risiko: password di argumen CLI (`7zz -p...`) terlihat di `ps` oleh user Linux lain. Evaluasi di SPEC: mount `/proc` dengan `hidepid=2`, atau gunakan library Go untuk zip AES dan batasi `7zz` hanya untuk 7z.

**Upload:**

- Protokol tus dengan chunk `UPLOAD_CHUNK_SIZE`, 3 file paralel, pause/resume/cancel per file dan semua, retry dengan backoff, resume setelah reload halaman.
- Sumber: tombol file/folder, drag & drop ke area mana pun atau langsung ke folder tertentu, paste dari clipboard.
- Pre-check sebelum upload: kuota, ukuran maksimum, ekstensi yang diblokir admin, konflik nama (Timpa/Lewati/Rename + terapkan ke semua, seperti aaPanel).
- Panel transfer di pojok kanan bawah (bisa diciutkan): progress per file, kecepatan rata-rata, sisa waktu, status. Tetap tampil saat pindah folder.
- Finalisasi di server: chunk di `DATA_DIR/uploads`; setelah lengkap, ukuran diverifikasi lalu file dipindah atomik ke tujuan dan thumbnail diantrekan. Upload terbengkalai dibersihkan setelah 24 jam.

**Download:**

- File tunggal: streaming dengan HTTP Range (bisa di-resume), ETag, Last-Modified, nama UTF-8 via `filename*`.
- Banyak item atau folder: zip streaming on-the-fly tanpa file sementara. Jumlah download paralel per user dibatasi.
- Signed URL berumur pendek (mis. 5 menit) untuk `<video>`, `<img>`, dan download manager.

**Remote download:**

- Input: satu atau banyak URL (satu per baris), folder tujuan, nama file (default dari `Content-Disposition` atau URL), opsi timpa/rename.
- Hanya http/https. Berjalan sebagai task: progress, kecepatan, sisa waktu, cancel, retry; resume dengan Range jika server sumber mendukung.
- Proteksi SSRF wajib: resolve DNS lalu tolak IP private, loopback, link-local, CGNAT, dan metadata (127.0.0.0/8, 10/8, 172.16/12, 192.168/16, 169.254/16, 100.64/10, 0.0.0.0, ::1, fc00::/7, fe80::/10). Cek ulang di setiap redirect (maks. 5) dan pin IP saat connect untuk mencegah DNS rebinding.
- Batas ukuran = min(`REMOTE_DOWNLOAD_MAX_SIZE`, sisa kuota), timeout koneksi dan idle, User-Agent yang jelas. Admin bisa menonaktifkan fitur ini atau mengatur allowlist/denylist domain.

## 10. Sharing

File dan folder bisa dibagikan dengan dua mode: link publik untuk siapa pun yang tahu link, atau share ke user terdaftar tertentu dengan memasukkan username.

| Aspek | Link publik | Share ke user tertentu |
| --- | --- | --- |
| Siapa yang bisa akses | Siapa pun yang punya link, tanpa login | Hanya user terdaftar yang dipilih berdasarkan username |
| Cara penerima membuka | `https://file.kppbwi.app/s/<token>` | Menu "Dibagikan ke saya" + notifikasi in-app |
| Permission | Lihat/preview + download (download bisa dimatikan); khusus folder ada opsi "boleh upload" (file request) | Viewer (lihat, preview, download) atau Editor (upload, buat, rename, edit, hapus di dalam item yang dibagikan) |
| Proteksi | Password opsional, masa berlaku, batas jumlah download, bisa dinonaktifkan sementara | Masa berlaku opsional, bisa dicabut kapan saja |
| Kuota | Upload lewat link dihitung ke kuota pemilik | Sama, dihitung ke kuota pemilik |

**Aturan link publik:**

- Token acak ≥ 128 bit, URL-safe. Yang disimpan di DB hanya hash-nya (SHA-256). Slug kustom opsional (unik dan divalidasi).
- Password share di-hash argon2id. Percobaan salah di-rate-limit per IP dan per link. Setelah benar, akses memakai cookie bertanda tangan yang hanya berlaku untuk link itu.
- Masa berlaku default dari `SHARE_DEFAULT_EXPIRY_DAYS`; pilihan 1 hari, 7 hari, 30 hari, custom, atau tanpa batas (admin bisa melarang "tanpa batas").
- Halaman publik minimal dan rapi: nama item, ukuran, pemilik (bisa disembunyikan), tombol preview dan download. Untuk folder: browser read-only (Details/Large icons), breadcrumb di dalam folder share, download zip. Path di atas item yang dibagikan tidak pernah terlihat. Halaman publik memakai `noindex`.
- Mode "preview saja" hanya menyembunyikan tombol download dan tidak mencegah orang yang berniat menyimpan file. Tulis ini dengan jelas di UI.

**Aturan share ke user:**

- Input username dengan autocomplete yang menjaga privasi: saran hanya muncul setelah ≥ 2 karakter, tanpa menampilkan data lain. Bisa banyak user sekaligus, permission per user, catatan opsional.
- Penerima melihat item di "Dibagikan ke saya" (dikelompokkan per pemilik), bisa favorit dan pin, tetapi tidak bisa reshare kecuali pemilik mengizinkan (default tidak). Penerima tidak melihat kuota atau item lain milik pemilik.
- Akses penerima tetap lewat lapisan `fsjail` pada root pemilik, dibatasi ke subtree item yang dibagikan.

**Konsistensi metadata:** share, favorit, pin, catatan, tag, dan riwayat versi mengacu ke path relatif. Saat rename/move lewat aplikasi (termasuk rename folder induk), semua referensi ikut diperbarui dalam satu transaksi. Perubahan dari luar aplikasi (mis. via SSH) membuat referensi tidak valid; tandai sebagai "rusak" di UI, jangan crash. Item yang dihapus menonaktifkan share-nya; restore dari Recycle Bin menawarkan untuk mengaktifkannya kembali.

**Manajemen "Share saya":** tabel semua share (item, mode, penerima, permission, kedaluwarsa, jumlah view dan download, akses terakhir, status) dengan aksi salin link, QR code, edit, nonaktifkan, hapus. Item yang dibagikan diberi badge di daftar file. Pemilik melihat log akses ringkas (waktu, IP dipotong, aksi). Admin bisa melihat dan mencabut semua share publik tanpa melihat isi file.

Notifikasi email di luar scope (tidak ada SMTP); notifikasi hanya in-app.

## 11. Admin panel & manajemen user

Admin panel hanya muncul untuk role `admin` dan setiap endpoint-nya diproteksi di server, bukan hanya disembunyikan di UI.

| Halaman | Isi |
| --- | --- |
| Dashboard | Jumlah user aktif/nonaktif; storage terpakai vs kapasitas disk (`statfs` pada `STORAGE_ROOT`); top user per pemakaian; task berjalan; share publik aktif; login gagal 24 jam terakhir; versi aplikasi; status dependency (ffmpeg, 7zz, libvips, rg, rar/unrar: terdeteksi atau tidak) |
| Users | Tabel: username, nama tampilan, role, status, kuota + bar pemakaian, login terakhir, folder sudah dibuat atau belum, tanggal dibuat |
| Settings | Override nilai default `.env` yang disimpan di DB (detail di bawah) |
| Audit log | Login sukses/gagal, perubahan user dan setting, share dibuat/dicabut, akses link publik, hapus permanen, akses admin ke folder user. Filter user/aksi/tanggal, export CSV, retensi default 180 hari |
| Tasks | Semua task semua user (nama, user, progress); admin bisa cancel |
| Maintenance | Rekalkulasi kuota, bersihkan cache thumbnail, bersihkan upload terbengkalai, purge Recycle Bin kedaluwarsa (juga terjadwal otomatis), cek integritas (folder tanpa akun, akun tanpa folder), backup SQLite (online backup API) ke `.system/backups` |

**Aksi pada user:**

- Buat user: username, nama tampilan, role, kuota, password sementara (generate otomatis atau manual) + wajib ganti saat login pertama. Folder baru dibuat saat login pertama (bagian 3).
- Edit nama, role, kuota, status. Reset password (password baru tampil sekali). Paksa logout semua sesi.
- Nonaktifkan/aktifkan akun. User nonaktif tidak bisa login dan link publik miliknya ikut nonaktif.
- Hapus user dengan konfirmasi ketik username, dua pilihan: hapus data permanen, atau arsipkan folder ke `.system/deleted-users/<username>-<timestamp>`.
- Admin terakhir tidak bisa menghapus, menonaktifkan, atau menurunkan role dirinya sendiri.

**Settings yang bisa diubah admin:** batas ukuran upload, chunk size, daftar ekstensi diblokir, kuota default, retensi Recycle Bin, limit versi file, remote download (on/off, ukuran maks., allowlist/denylist domain), aturan share (boleh link publik, boleh tanpa batas waktu, wajib password), kebijakan password, session timeout, nama dan logo aplikasi, bahasa default.

**Profil (semua user):** ganti password; sesi aktif (perangkat, IP, terakhir aktif, logout per sesi); preferensi (tema light/dark/system, warna aksen, bahasa, view default, density, folder di atas, konfirmasi delete); 2FA TOTP di fase akhir.

## 12. Desain UI/UX

Arah visual: alat kerja yang presisi — tenang, padat, rapi — dengan kepadatan informasi setara aaPanel, rasa modern seperti dashboard Linear/Vercel dan Finder. Ini bukan landing page: tidak ada hero, gradient besar, ilustrasi dekoratif, kartu besar, glassmorphism, atau shadow tebal.

```text
┌───────────────────────────────────────────────────────────────────────────┐
│ [logo] FileManager   [ Cari file…        Ctrl+K ]     Tasks(2)  Tema  budi ▾│  top bar 48px
├───────────┬───────────────────────────────────────────────────────────┤
│ File Saya │ ← → ↑ ⟳  / Proyek / website / assets ▾      [Filter…]      │  address bar 40px
│ Recent    │ [+ Baru▾] [Upload▾] [Remote] | Copy Cut Paste Del ⋯ | View▾ Kolom│  toolbar 40px
│ Favorit   ├───────────────────────────────────────────────────────────────┤
│ Dibagikan │ [ ] Nama ▲          Ukuran  Tipe         Diubah       Catatan  │  header 32px
│ Share     │ [ ] [dir] css           —    Folder       2 jam lalu          │  baris 36px
│ Trash     │ [ ] [img] logo.png  24 KB    Gambar PNG   kemarin      logo v2│
│ Aktivitas │ [ ] [htm] index.html 3 KB    HTML         12 Okt 2026         │
│ Kuota 62% │                                                         │
├───────────┴───────────────────────────────────────────────────────────┤
│ 124 item · 3 dipilih (1,2 MB)                          6,2 GB dari 10 GB │  status bar 28px
└─────────────────────────────────────────────────────────────────────────┘
```

**Token warna** (CSS variables; definisikan sebagai theme Tailwind, verifikasi kontras WCAG AA dengan script):

| Token | Light | Dark |
| --- | --- | --- |
| `--bg` | #FFFFFF | #0E1015 |
| `--surface` (sidebar, toolbar) | #F7F8FA | #14171E |
| `--surface-2` (hover, header tabel) | #F0F2F5 | #1B1F28 |
| `--border` | #E4E7EC | #262B36 |
| `--text` | #0F172A | #E6E8EE |
| `--text-muted` | #5B6474 | #9AA3B2 |
| `--accent` | #2563EB | #4C8DFF |
| `--accent-subtle` (baris terpilih) | #EAF1FE | #1A2A4A |
| `--success` | #16A34A | #22C55E |
| `--warning` | #D97706 | #F59E0B |
| `--danger` | #DC2626 | #F87171 |

- Warna aksen bisa dipilih user: biru (default), emerald, violet, amber, rose, slate.
- Warna ikon tipe file konsisten: folder amber, gambar violet, video rose, audio pink, arsip oranye, kode sky, PDF merah, dokumen biru, spreadsheet hijau, teks slate. Pakai set ikon berlisensi MIT, atau buat sendiri.
- Dark mode bukan hitam pekat dan bukan sekadar invert. Mode: light, dark, system (default system), toggle di top bar, tanpa flash saat load.

**Tipografi & ritme:**

- Inter untuk UI dengan `tabular-nums` di kolom ukuran dan tanggal; JetBrains Mono untuk path, permission, dan kode.
- Skala: 12 px (meta, status bar), 13 px (tabel, menu), 14 px (UI umum), 16 px (judul dialog), 20 px (judul halaman). Weight hanya 400/500/600.
- Grid spasi 4 px. Radius: 4 px badge, 6 px input/tombol, 8 px dialog. Border 1 px. Shadow hanya untuk popover, menu, dan dialog.
- Motion 120–180 ms ease-out; hormati `prefers-reduced-motion`.

**Komponen:** tombol ikon 32 px dengan tooltip + shortcut; grup toolbar dengan separator; dropdown dan context menu (Radix); dialog; sheet untuk mobile; toast (sonner) yang tidak menutupi panel transfer; breadcrumb dengan dropdown folder sibling dan collapse saat overflow; progress linear; badge; skeleton; empty state berisi ikon kecil, satu kalimat, dan tombol aksi.

**Responsif:** ≥ 1280 px tampilan penuh. 768–1279 px: sidebar menjadi ikon, panel preview menjadi overlay. < 768 px: sidebar menjadi drawer, toolbar diringkas ke menu "⋯", Details hanya Nama + Ukuran/Diubah, long-press = context menu, aksi seleksi di bottom bar.

**Aksesibilitas & i18n:** WCAG 2.2 AA; tabel file memakai pola ARIA grid dengan roving tabindex; focus ring selalu terlihat; tombol ikon punya `aria-label`; hasil operasi diumumkan lewat live region. Semua string di file locale; format tanggal dan angka mengikuti locale (id-ID: "12 Okt 2026", "1,2 MB").

**Style guide:** sebelum membangun layar, buat route `/dev/design` yang menampilkan semua token dan komponen dalam light dan dark mode. Setiap layar baru harus memakai komponen dari sana.

## 13. Keamanan, non-fungsional & deployment

Keamanan adalah syarat rilis, bukan fase polish: setiap poin di bawah harus punya test atau langkah verifikasi di SPEC.

**Keamanan:**

- AuthZ di setiap endpoint: session valid, tidak sedang wajib ganti password, dan resource milik user atau dibuka oleh share yang sah. Test IDOR: user A tidak bisa mengakses file, share, task, atau versi milik user B lewat ID maupun path apa pun.
- Path traversal dan symlink: semua lewat `fsjail` (bagian 3). Symlink buatan user harus relatif dan resolve di dalam root. Symlink lama yang mengarah keluar root ditampilkan sebagai rusak dan tidak bisa diikuti.
- XSS dari konten user: nama file selalu di-escape; Markdown disanitasi (DOMPurify); SVG/HTML tidak pernah disajikan inline dari origin aplikasi. Bandingkan di SPEC: origin konten terpisah (mis. `usercontent.file.kppbwi.app`, yang juga harus ditambahkan sebagai domain di aaPanel) versus `Content-Disposition: attachment` + CSP `sandbox` di origin yang sama. Tetapkan satu pilihan.
- Header: CSP ketat (`default-src 'self'`, tanpa `unsafe-inline` untuk script, worker Monaco/pdf.js diatur eksplisit), HSTS, `X-Content-Type-Options: nosniff`, `Referrer-Policy` (`no-referrer` di halaman share), `Permissions-Policy`, `frame-ancestors 'none'`.
- CSRF token + `SameSite`; CORS dimatikan.
- Rate limit: login, password share, remote download, content search, semua endpoint publik.
- SSRF, zip-slip, dan zip bomb sesuai bagian 9. File upload tidak pernah dieksekusi: storage berada di luar web root dan Nginx tidak pernah menyajikan `/www/storage` secara langsung.
- Tidak ada `sh -c`. Semua CLI dipanggil lewat argument array dengan path biner absolut dari config.
- `APP_SECRET`, password, dan token share tidak pernah masuk log.
- `govulncheck` dan `npm audit` (atau setara) berjalan di CI. Threat model singkat (STRIDE) ditulis di SPEC.

**Target non-fungsional:**

| Area | Target |
| --- | --- |
| Listing | Folder 10.000 item: API p95 < 500 ms, render pertama < 1 detik |
| Navigasi | Pindah folder dengan cache hangat < 150 ms |
| Upload | File 20 GB berhasil; RAM backend tidak tumbuh sebanding ukuran file |
| Download & stream | HTTP Range; zip streaming tanpa menampung file di memori |
| Thumbnail | Worker pool terbatas (jumlah CPU − 1); item yang terlihat di layar diproses lebih dulu |
| Bundle frontend | JS awal < 300 KB gzip; Monaco, pdf.js, video player di-lazy-load |
| Resource | Idle < 100 MB RAM; nyaman di VPS 2 vCPU / 2 GB |
| Konkurensi | Operasi pada path yang sama dikunci per path; maks. 3 task paralel per user |
| Ketahanan | Graceful shutdown: task berjalan ditandai interrupted dan bisa diulang; upload tus otomatis lanjut |
| Observability | Log JSON (`slog`) dengan request ID, `/healthz` dan `/readyz`, metrik Prometheus opsional di `127.0.0.1` |
| Kualitas | Coverage backend inti (`fsjail`, auth, share, arsip) ≥ 80%; E2E Playwright untuk alur utama |

**Deployment di Ubuntu + aaPanel (`file.kppbwi.app`):**

- Target utama: binary + systemd di `/www/wwwroot/file.kppbwi.app` sesuai bagian 3. Docker hanya opsional; dengan aaPanel, binary + systemd lebih sederhana dan tidak bentrok dengan Nginx aaPanel.
- `deploy/install.sh` (idempoten): cek versi Ubuntu (22.04/24.04); install dependency via apt (ffmpeg, libvips-tools, poppler-utils, ripgrep, 7-Zip, unrar dari multiverse bila diizinkan — nama paket dan nama binary 7-Zip `7zz` vs `7z` perlu diverifikasi per versi Ubuntu); buat user `filemanager`; buat `/www/storage/FileManager` (owner `filemanager`, mode 0750); generate `APP_SECRET`; pasang `.env` (600) dan unit systemd; enable + start; lalu cetak checklist langkah di aaPanel (Add site, running directory `/public`, reverse proxy, SSL, snippet Nginx).
- `deploy/update.sh`: build atau ambil rilis baru, backup DB, migrasi, restart, rollback otomatis jika `/readyz` gagal.
- `make build` menghasilkan satu binary dengan frontend ter-embed. Build bisa di server (Go + Node terpasang) atau di CI lalu diunggah.
- Dokumentasikan backup `/www/storage/FileManager` (boleh memakai cron/backup aaPanel) dan backup online SQLite.

## 14. Di luar scope

Beberapa fitur aaPanel sengaja tidak dibawa karena bertentangan dengan isolasi multi-user. Tulis tabel ini di PRD sebagai Non-goals, lengkap dengan alasannya.

| Fitur | Keputusan | Alasan |
| --- | --- | --- |
| Terminal web | Dikecualikan | Shell membuka akses ke seluruh server dan menembus isolasi antar-user. Jika kelak dibutuhkan, harus lewat container/namespace terisolasi sebagai proyek terpisah. |
| chown / owner Linux | Dikecualikan | User aplikasi bukan user Linux; semua file milik `filemanager`. Mengganti owner tidak bermakna dan butuh root. |
| Backup/restore permission, fix permission | Dikecualikan | Relevan untuk web root situs, bukan storage pribadi. |
| Tamper protection, rsync, cloud storage, malware scan, composer/git, konversi format | Dikecualikan | Fitur panel server aaPanel, bukan fitur file manager. |
| Menjelajah path di luar `/www/storage/FileManager` | Dikecualikan | aaPanel bisa menjelajah seluruh server; aplikasi ini sengaja dikunci ke root masing-masing user. |
| Transcoding video, preview Office, email/SMTP | Ditunda (fase akhir, opsional) | Biaya CPU dan kompleksitas tinggi; tidak memblokir nilai inti. |
| WebDAV / sinkronisasi klien desktop | Ditunda | Kandidat fitur berikutnya setelah v1 stabil. |
| Registrasi publik, SSO/LDAP | Ditunda | Di v1 hanya admin yang membuat user. |
| Multi-server, object storage (S3) | Ditunda | v1 single-server dengan filesystem lokal. |

## 15. Isi wajib `PRD.md`

PRD menjawab "apa dan kenapa", bukan "bagaimana". Gunakan struktur berikut:

1. Ringkasan dan latar belakang, termasuk kenapa tidak cukup memakai file manager aaPanel langsung (isolasi multi-user, sharing antar-user, UI).
2. Tujuan dan metrik sukses yang terukur (mis. waktu buka folder, tugas inti selesai tanpa bantuan, nol temuan kritis di security review).
3. Non-goals (bagian 14).
4. Persona dan skenario utama (bagian 1).
5. Matriks paritas aaPanel (bagian 4) dengan kolom: fitur aaPanel, status (Wajib / Diperluas / Dikecualikan), referensi epic.
6. Epic dan user story dengan acceptance criteria format Given/When/Then dan prioritas MoSCoW. Epic minimal: Auth & Akun; Provisioning folder & Kuota; Browsing & Views; Operasi file; Upload, Download & Remote download; Arsip; Editor; Viewer & Player; Search; Sharing; Recycle Bin & Versi; Admin; Preferensi; Deployment aaPanel.
7. User flow utama (teks atau mermaid): admin login pertama → ganti password → buat user → user login pertama (folder dibuat) → upload → share ke user lain → penerima membuka.
8. Ringkasan kebutuhan non-fungsional (bagian 13), dengan rujukan ke SPEC.
9. Rilis dan milestone, tandai mana yang MVP: M1 fondasi (auth, `fsjail`, listing, views), M2 operasi file + upload/download, M3 arsip + editor + viewer, M4 sharing + admin, M5 hardening + deploy aaPanel.
10. Risiko dan mitigasi, minimal: keamanan path, zip bomb, SSRF, performa folder besar, codec video, lisensi RAR, password di argumen CLI, aaPanel menimpa config Nginx, disk penuh.
11. Asumsi dan pertanyaan terbuka: setiap default dari prompt ini yang kamu pakai.
12. Glosarium.

## 16. Isi wajib `SPEC.md`

SPEC menjawab "bagaimana", cukup detail sehingga engineer lain bisa mengimplementasikan tanpa menebak.

1. **Arsitektur:** diagram komponen (mermaid) — browser, Nginx aaPanel, aplikasi Go (HTTP API, auth, `fsjail`, task engine, thumbnail worker, archive engine, search, share), SQLite, storage, CLI tools. Alur request utama: listing, upload tus, download Range, extract sebagai task + SSE.
2. **Struktur repo (monorepo):**

   ```text
   backend/   cmd/filemanager, internal/{config,auth,fsjail,files,upload,download,archive,
              thumbs,media,search,share,admin,tasks,audit,db}, migrations/, sqlc/
   web/       src/{app,routes,features/*,components/ui,lib,i18n,styles}
   deploy/    install.sh, update.sh, filemanager.service, nginx-aapanel.conf,
              Dockerfile, docker-compose.yml
   docs/      DEPLOY-AAPANEL.md, SECURITY.md, openapi.yaml
   Makefile, .env.example, PRD.md, SPEC.md, PROMPT.md, PROGRESS.md
   ```
3. **Konfigurasi:** tabel semua variabel env (nama, tipe, default, validasi, bisa di-override admin atau tidak) yang konsisten dengan `.env.example`.
4. **Model data:** ERD (mermaid) + definisi tabel, kolom, dan index. Minimal: users, sessions, user\_preferences, settings, shares, share\_recipients, share\_access\_logs, favorites, pins, file\_notes, file\_tags, file\_versions, trash\_items, tasks, task\_logs, notifications, audit\_logs, media\_metadata (cache dimensi/durasi/thumbnail), upload sessions (state tusd), quota\_usage.
5. **Path & `fsjail`:** model path virtual, normalisasi, aturan nama, API package, kebijakan symlink, locking per path, copy/move lintas filesystem, update referensi metadata saat rename/move.
6. **API:** semua endpoint REST (method, path, auth, request, response, kode error) dalam `docs/openapi.yaml` + ringkasan di SPEC. Format error seragam `{"error":{"code","message","details"}}`. Paging berbasis cursor. Termasuk endpoint SSE, tus, dan share publik.
7. **Task engine:** tipe task, state machine (queued → running → done / failed / cancelled / interrupted), persistensi, batas konkurensi, cancel, event progress via SSE, pembersihan.
8. **Spesifikasi per fitur:** listing & sort (algoritma natural sort, field yang disortir di server), views & pipeline thumbnail, upload, download & zip streaming, remote download (detail SSRF guard), arsip (matriks format, engine, limit), editor (encoding, versi, konflik), viewer (pemetaan MIME → viewer), search (nama dan isi; ripgrep vs fallback), sharing (token, password, cookie share, halaman publik), Recycle Bin, kuota, admin.
9. **UI spec:** design token lengkap (bagian 12), inventaris komponen, layout tiap layar (wireframe teks), daftar route, matriks context menu, tabel shortcut final, state empty/loading/error, konvensi key i18n.
10. **Keamanan:** threat model STRIDE, kontrol untuk setiap ancaman, header dan CSP final, kebijakan password dan session, rate limit dengan angka konkret, daftar test keamanan.
11. **Strategi testing:** unit; integration (`httptest` + temp dir); E2E Playwright untuk alur di PRD; fixture arsip berbahaya (zip-slip, bomb kecil); test SSRF; test IDOR; performance test folder 10.000 file; target coverage.
12. **Deployment:** langkah aaPanel klik-per-klik, isi `nginx-aapanel.conf`, unit systemd lengkap, install/update/rollback, backup/restore, troubleshooting (502, upload gagal karena body size, SSE putus karena buffering, perpanjangan SSL gagal).
13. **ADR singkat** untuk setiap keputusan besar (Go, SQLite, tus, SSE, Monaco vs CodeMirror, origin konten, video player, penanganan password arsip): konteks → keputusan → konsekuensi.

## 17. Format & urutan `PROMPT.md`

`PROMPT.md` berisi prompt step-by-step yang akan dijalankan AI satu per satu, berurutan, masing-masing di sesi terpisah. Setiap step harus selesai dalam satu sesi kerja (target perubahan ≤ ±1.500 baris kode), menghasilkan sesuatu yang bisa dijalankan dan dites, dan tidak merusak step sebelumnya.

**Preamble** di awal `PROMPT.md`, wajib disertakan di awal setiap step:

```text
Kamu melanjutkan proyek File Manager (file.kppbwi.app). Sebelum mulai:
1. Baca PRD.md, SPEC.md, PROGRESS.md, dan bagian SPEC yang dirujuk step ini.
2. Kerjakan HANYA Step <N>. Jangan mengerjakan step lain atau refactor di luar scope.
3. Jika SPEC salah atau kurang, perbarui SPEC.md dulu dan catat alasannya di PROGRESS.md.
4. Selesai = semua acceptance criteria terpenuhi, test lulus, lint dan typecheck bersih.
5. Centang step di PROGRESS.md, tulis ringkasan + catatan untuk step berikutnya, lalu commit.
```

**Format setiap step:**

```markdown
## Step N — <judul>
**Tujuan:** …
**Prasyarat:** Step …
**Baca dulu:** SPEC §…, PRD epic …
**Tugas:** daftar bernomor yang konkret
**File yang dibuat/diubah:** …
**Acceptance criteria:** checklist yang bisa diverifikasi (perintah + hasil yang diharapkan)
**Test wajib:** unit / integration / E2E yang ditulis di step ini
**Verifikasi:** perintah yang dijalankan (mis. make test, make lint, pnpm test, pnpm e2e)
**Di luar scope step ini:** …
**Commit:** feat(scope): …
```

**Urutan step yang disarankan** (boleh dipecah lebih kecil, jangan digabung):

| # | Step | Hasil yang bisa diverifikasi |
| --- | --- | --- |
| 1 | Scaffolding monorepo, Makefile, lint/format, CI, config loader + validasi `.env`, logging, `/healthz` | Build dan test kosong lulus di CI |
| 2 | DB, migrasi, sqlc; model user, session, settings | Migrasi naik/turun, test repository |
| 3 | Auth: seed admin dari env, login/logout, session, CSRF, rate limit, wajib ganti password, buat folder user saat login pertama | Alur login teruji lewat API |
| 4 | `fsjail` + test keamanan path dan symlink | Coverage package ≥ 90% |
| 5 | API listing (sort server-side, paging cursor, metadata), buat folder, buat file | Test API lulus |
| 6 | Frontend shell: Vite/React, routing, design token, light/dark, i18n, `/dev/design`, halaman login & ganti password | Login lewat UI |
| 7 | File browser: layout, sidebar, address bar, tab folder, Details view + pilihan kolom + sort + seleksi + keyboard | Navigasi dan sort di UI |
| 8 | View List, Tiles, Large icons, Extra large icons + pipeline thumbnail (worker + cache) | 5 view jalan dengan thumbnail |
| 9 | Operasi file: rename, batch rename, copy/cut/paste, dialog pindah/salin, konflik, drag & drop, delete ke Recycle Bin, undo | E2E operasi file |
| 10 | Recycle Bin, favorit, pin, catatan, tag, recent, properties, checksum, hitung ukuran folder, kuota | E2E + test kuota |
| 11 | Task engine + SSE + panel task | Task bisa di-cancel, progress tampil |
| 12 | Upload tus + panel transfer; download Range, zip streaming, signed URL | Upload 1 GB bisa di-resume |
| 13 | Remote download + SSRF guard | Test SSRF lulus |
| 14 | Arsip: compress, extract, archive browser, limit keamanan, fixture berbahaya | Test zip-slip/bomb lulus |
| 15 | Cari nama, cari isi file, replace in files dengan preview | E2E search |
| 16 | Editor Monaco: tab, explorer, save/save all, konflik, encoding, EOL, versi + diff, pengaturan | E2E edit + konflik |
| 17 | Viewer: gambar, PDF, audio + mini-player, video, teks/Markdown/CSV/JSON | E2E buka tiap tipe |
| 18 | Sharing: link publik + halaman publik, share ke user, "Dibagikan ke saya", "Share saya" | Test akses lintas user |
| 19 | Admin panel (dashboard, users, settings, audit log, tasks, maintenance) + profil & sesi | E2E admin |
| 20 | Hardening: header/CSP, review OWASP, test IDOR, rate limit, govulncheck, perbaikan temuan | Checklist keamanan lulus |
| 21 | Performa & aksesibilitas: folder 10.000 file, bundle budget, audit axe, responsif mobile | Target bagian 13 tercapai |
| 22 | Deployment: install.sh, update.sh, systemd, `nginx-aapanel.conf`, `DEPLOY-AAPANEL.md`, smoke test di Ubuntu + aaPanel untuk `file.kppbwi.app` | Bisa di-deploy dari server kosong |
| 23 | Opsional fase akhir: 2FA, dual-pane, duplicate finder, transcoding | Sesuai prioritas PRD |

Sisipkan step "Review independen" setiap 4–5 step: reviewer (sesi AI baru tanpa konteks implementasi) membaca diff terhadap PRD/SPEC, mencari bug, celah keamanan, dan penyimpangan desain, lalu menulis temuan yang wajib diselesaikan sebelum lanjut.

## 18. Aturan output & checklist sebelum berhenti

Sebelum berhenti, periksa dan pastikan semua poin ini terpenuhi:

- [ ] `PRD.md`, `SPEC.md`, `PROMPT.md`, `PROGRESS.md`, dan `.env.example` ada di root repo; `.env` (berisi password awal admin) masuk `.gitignore`.
- [ ] Setiap baris "Wajib" di matriks paritas (bagian 4) dipetakan ke epic di PRD dan ke minimal satu step di `PROMPT.md`.
- [ ] 5 mode view, pilihan kolom, dan sort per kolom tercakup di SPEC dan di step.
- [ ] Code editor, image viewer, PDF viewer, music player, dan video player tercakup.
- [ ] Arsip zip, rar, 7z, tar, tar.gz, tar.bz2, tar.xz, tar.zst tercakup sesuai matriks format.
- [ ] Upload resumable dan remote download dengan SSRF guard tercakup.
- [ ] Share link publik dan share ke user tertentu tercakup.
- [ ] Storage `/www/storage/FileManager/<username>`, folder dibuat saat login pertama, isolasi lewat `fsjail`.
- [ ] Admin dari `.env` dengan wajib ganti password; hanya admin yang membuat user.
- [ ] Domain `file.kppbwi.app` lewat aaPanel; aplikasi di `/www/wwwroot/file.kppbwi.app`, terpisah dari storage.
- [ ] Dark/light mode dan design token terdefinisi.
- [ ] Tidak ada versi library yang diklaim tanpa catatan verifikasi; klaim tentang aaPanel di luar prompt ini ditandai "perlu verifikasi".
- [ ] Semua asumsi tercatat di PRD.

Lalu tampilkan di chat (bukan di file): ringkasan keputusan utama maks. 10 baris, daftar asumsi, risiko teratas, dan maks. 7 pertanyaan terbuka. **Jangan mulai Step 1 sebelum saya menyetujui.**
