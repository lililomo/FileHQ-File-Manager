# Review C — Review independen Step 11–14b (task engine, SSE, upload tus, download/zip, remote download, arsip)

| | |
| --- | --- |
| Tanggal | 15 Oktober 2026 |
| Basis kode | commit `951f799` (branch `main`, Step 14b selesai) |
| Lingkup | `backend/internal/{tasks,sse,notify,upload,download,netguard,remotedl,archive,exec,httpapi}` (endpoint `tasks/*`, `events`, `upload/*`, `files/raw|download|zip|sign`, `remote-downloads/*`, `archives/*`), fixture `testdata/`, `docs/SECURITY.md`, `docs/openapi.yaml` |
| Acuan | PRD E5, E6, E15 · SPEC §7, §8.8–8.11, §10 · ADR-06/08/15 · PROMPT Step R-C · review-B minor tertunda |
| Reviewer | Sesi baru tanpa konteks implementasi; fase perbaikan dilanjutkan oleh sesi berikutnya setelah sesi review terputus |

## 1. Ringkasan

Lapisan transfer dan arsip **tahan terhadap sebagian besar probe** yang dicoba: metadata tus tidak bisa keluar root, signed URL terikat user + purpose + exp dan ditolak setelah akun dinonaktifkan, kebijakan inline menolak seluruh matriks konten aktif (HTML/SVG/XML/XHTML/JS/CSS/PDF-dengan-JS/JPG tersamar) dengan `CSP sandbox` + `nosniff`, zip streaming menolak root/`.system`/token user lain, SSRF guard menolak semua ejaan alamat lokal yang dicoba (zone id IPv6, trailing dot, `0x7f000001`, `127.1`, `[::ffff:127.0.0.1]`, redirect 307 lintas skema, `data:`, `Location` relatif/scheme-relative, port privileged, dial non-TCP), ekstraksi menahan `..\..\evil`, `./../`, header ukuran palsu (0 B vs 3 MiB; 100 MiB vs kosong), symlink-lalu-file sama nama, password 7-Zip tidak pernah masuk argv, dan SSE tidak pernah menyilangkan user — semuanya **lolos** (daftar di §4).

Ditemukan **0 blocker, 4 major, 6 minor**. Major: (M-01) upload tus yang **gagal difinalisasi pada PATCH terakhir** (mis. `423` karena target terkunci operasi lain) tidak pernah bisa difinalisasi lagi — tusd menjawab HEAD/PATCH upload yang sudah lengkap **tanpa memanggil hook pre-finish**, sehingga file tidak pernah mendarat, sesi tetap `active`, dan reservasi kuota tertahan sampai cleanup 24 jam; (M-02) arsip 7z ber-password: `Encrypted()` diprobe **dengan** password sehingga selalu `false` → browser arsip tidak pernah mendapat `session` (preview mustahil), password salah pada header terenkripsi (`-mhe=on`) dilaporkan `ARCHIVE_CORRUPT`, dan **indeks cache** (key tanpa password) melayani daftar nama arsip `-mhe` kepada request tanpa password; (M-03) stderr 7-Zip yang memuat **path host** (`STORAGE_ROOT/<user>/…`) diteruskan apa adanya ke `details.stderr` error `ARCHIVE_CORRUPT` (respons API dan error task) — catatan 14a "path host tidak muncul karena cwd" tidak berlaku untuk `7z l` yang menerima path absolut; (M-04) task engine: `Cancel` yang tiba **di antara dispatch dan start runner** melihat `r.cancel == nil` sehingga context runner tidak pernah dibatalkan — task berjalan sampai selesai dengan `cancelRequested=true`.

Baseline verifikasi (sebelum perbaikan): `make lint` hijau; `go test -race ./...` hijau untuk kode produksi tetapi **race detector** menandai buffer log harness test (`bytes.Buffer` ditulis goroutine engine saat test membaca) di tiga paket; fuzz `FuzzEntryName`/`FuzzNormalize` 60 s tanpa crash; `pnpm e2e` hijau kecuali gerbang longgar 10k yang sudah dikenal. Setelah perbaikan: `make lint` hijau (golangci-lint 0 issue, eslint/tsc/prettier/kontras/i18n bersih), `go test -race ./...` hijau 23 paket, Vitest 127/127, `pnpm e2e` 71/71 (termasuk gerbang 10k yang sebelumnya longgar).

Semua major (M-01…M-04) dan empat minor **ditutup di fase perbaikan** (§5); dua minor ditunda ke Step 20 (tabel "Catatan lintas step" PROGRESS). Probe reviewer dipertahankan sebagai test regresi: `backend/internal/{upload,tasks,sse,archive,httpapi,netguard,fsjail}/review_c_test.go` (17 test + 2 target fuzz).

## 2. Metode

1. Membaca PRD/SPEC/PROGRESS yang dirujuk (termasuk Deviasi dan Catatan lintas step Step 11–14b, review-B minor tertunda), lalu **seluruh kode** paket di lingkup: `tasks/{engine,runtime,repo}.go`, `sse/broker.go`, `upload/{service,hooks,cleanup,precheck}.go`, `download/{signer,policy,serve,zipstream,zipstore}.go`, `netguard/*.go`, `remotedl/{service,runner}.go`, `archive/{format,entry,safe,extract,compress,cli,index,session,browse,zipedit}.go`, `exec/exec.go`, `httpapi/{tasks,events,upload,download,remotedl,archives,archives_browser,sign}.go`.
2. Menjalankan `make lint && make test`, `go test -race ./...`, `go test -fuzz=FuzzEntryName -fuzztime=60s ./internal/archive/`, `go test -fuzz=FuzzNormalize -fuzztime=60s ./internal/fsjail/`, lalu `pnpm e2e` terhadap binary hasil `make build`.
3. Checklist R-C (a)–(h) dengan **probe baru yang ditulis sebagai test**: lock asing saat PATCH terakhir, metadata `..`/`share`/`lastModified` negatif, DELETE upload aktif vs reservasi; matriks 11 jenis konten pada `/files/raw` (purpose media/inline), `/files/download`, dan `/archives/entry-content`; tukar scope/purpose/`inner`/`sess` pada URL bertanda tangan, akun dinonaktifkan; payload zip root/`.system`/token user lain; 20 ejaan URL SSRF + 6 pola redirect; fixture arsip berbahaya (backslash traversal, `./../`, header bohong, symlink lalu file, iso palsu); retry vs slot per user/`TASK_LIMIT`, `Cancel` sedini mungkin; broker SSE dua user dengan `Last-Event-ID` silang.
4. Pengukuran beban gabungan (checklist h): upload tus 1 GiB (chunk 16 MB) + compress 1 GiB → zip + extract zip 1 GiB (Store) **bersamaan** terhadap binary nyata, sampling `VmRSS`/`VmHWM` tiap 1 s (§6).
5. Grep terarah: `-p` pada argv 7-Zip, `slog` yang memuat `AbsForCLI`/`HostPath`, `sh -c`, `os.Open*` di luar `fsjail` (lint forbidigo), path host dalam pesan error (`strings.Contains(…, root)` di test).

## 3. Temuan

Prioritas: **blocker** = harus ditutup sebelum step berikutnya dan berdampak keamanan/data; **major** = wajib ditutup sebelum step berikutnya; **minor** = boleh ditunda dengan dicatat di PROGRESS "Catatan lintas step".

### 3.1 Blocker

Tidak ada.

### 3.2 Major

| ID | Lokasi | Deskripsi | Bukti / reproduksi | Saran | Status |
| --- | --- | --- | --- | --- | --- |
| M-01 | `backend/internal/upload/service.go` (`preFinish`), `httpapi/upload.go` (route HEAD/PATCH) | Hook `PreFinishResponseCallback` tusd hanya dipanggil pada PATCH yang menulis byte terakhir. Jika hook gagal dengan error yang bisa diulang (`423 OPERATION_IN_PROGRESS` karena target dikunci rename/copy, error sementara DB/disk), byte terakhir **sudah tersimpan** di store; HEAD/PATCH berikutnya (yang dilakukan tus-js-client saat retry) dijawab tusd dari store (`Upload-Offset == Upload-Length`) **tanpa hook** → klien melapor sukses, file tidak pernah ada di folder tujuan, sesi `upload_sessions` tetap `active`, reservasi kuota tertahan sampai cleanup 24 jam, part 1 file penuh tertinggal di `DATA_DIR/uploads`. Checklist (a) "finalisasi atomik". | `TestReviewCFinalizeAfterLockConflict` pada baseline: PATCH terakhir di bawah lock asing → `423` (benar); setelah lock dilepas, HEAD → `200 Upload-Offset=3145728` tanpa `Upload-Result`, PATCH kosong di offset akhir → `204` tanpa `Upload-Result`; file tidak ada, sesi `active`. | Bungkus HEAD/PATCH: bila sesi milik pemanggil masih `active` dan `info.Offset == info.Size == row.Size`, jalankan pre-finish (serialisasi per id, re-check di bawah lock) dan jawab seperti tusd (`200`/`204` + `Upload-Offset` + header `Upload-Result`/`Upload-Path`); error hook dipetakan ke respons tusd yang sama. | **Diperbaiki** — `upload.Handler` (override `HeadFile`/`PatchFile` di `tushandler.go`) + `Service.FinishStored`; `httpapi` memakai handler ini. Regresi `TestReviewCFinalizeAfterLockConflict`. |
| M-02 | `backend/internal/archive/entry.go` (`sevenZipSource`), `browse.go` (`indexFor`), `index.go` | Tiga cacat terkait pada 7z ber-password: (1) `Encrypted()` membuka entri pertama **dengan password yang diberikan** → sukses → `false`; `Entries` lalu tidak membuat `session`, sehingga `POST /archives/sign` + `entry-content` (yang hanya menerima `sess`) tidak pernah bisa dipakai untuk 7z terenkripsi (browser arsip tidak bisa preview); (2) password salah pada header terenkripsi (`-mhe=on`) gagal di `NewReaderWithPassword` dengan `sevenzip: unexpected id` → dipetakan `ARCHIVE_CORRUPT` ("not a valid 7z file"), tanpa password juga `ARCHIVE_CORRUPT` bukan `ARCHIVE_PASSWORD_REQUIRED`; (3) key cache indeks = (user, path, size, mtime, encoding) tanpa password → setelah satu listing sah, request **tanpa password** mendapat daftar nama arsip `-mhe` dari cache selama 10 menit. Checklist (f). | `TestReviewCSevenZipPasswordSession`: `plain-hdr.7z`/`enc-hdr.7z` dengan `pw-7` → `encrypted=false session=""`; `enc-hdr.7z` + `nope` → `ARCHIVE_CORRUPT`; `enc-hdr.7z` tanpa password setelah listing sah → `200` dengan `a.txt`. | Probe `Encrypted` **tanpa** password sekali saat open (header terenkripsi gagal di open; body terenkripsi gagal di read pertama) dan simpan; klasifikasi error open: `unexpected id` tanpa password → `PASSWORD_REQUIRED`, dengan password → `PASSWORD_WRONG` (konsisten dengan aturan CLI "can't open as archive"); tandai `Index.HeaderEncrypted` dan tolak cache hit tanpa password. | **Diperbaiki** — `detectEncrypted` + `hdrEnc` (7z) dan `HeaderEncrypted()` (rar `-hp`), `isEncryptedHeaderErr`, `Index.HeaderEncrypted` + penolakan di `indexFor`; jalur CLI menandai `HeaderEncrypted` konservatif bila listing memakai password. Regresi `TestReviewCSevenZipPasswordSession`. |
| M-03 | `backend/internal/archive/cli.go` (`run7z`) | Error `ARCHIVE_CORRUPT` CLI menyertakan `details.stderr` (≤ 400 karakter) **tanpa menyamarkan path host**: `7z l -slt -- /www/storage/FileManager/<user>/x.iso` menulis `ERROR: /www/storage/…/x.iso : Cannot open the file as archive`. Path host bocor ke respons `GET /archives/entries` (iso rusak), error task extract, dan log task. SPEC §10 "path host tidak pernah keluar dari server". Checklist (f). | `TestReviewCCorruptCLIErrorHidesHostPath` (service) dan `TestReviewCSevenZipErrorHidesHostPath` (HTTP): iso palsu 32 KiB → `details.stderr` memuat `STORAGE_ROOT`. | Samarkan setiap argumen absolut (arsip, sumber, folder output, cwd) menjadi `…/<basename>` dan sisa path absolut lain lewat regex sebelum masuk `details`/log. | **Diperbaiki** — `scrubHostPaths(stderr, args, dir)` di `run7z`. Regresi dua test di atas. |
| M-04 | `backend/internal/tasks/engine.go` (`dispatch`/`execute`/`Cancel`) | `dispatch` memasukkan `run` ke `e.running` lalu memulai goroutine `execute`, yang **baru** membuat `context.WithCancel` dan mengisi `r.cancel`. `Cancel` yang tiba pada jendela itu menemukan `r.cancel == nil` → hanya menandai `cancel_requested` di DB; context runner tidak pernah dibatalkan → task (copy 5.000 file, extract, remote download) berjalan sampai selesai sementara UI menampilkan "membatalkan…". Checklist (g). | `TestReviewCCancelBeforeRunnerStartsStillCancels` (loop `Cancel` sedini mungkin) gagal sporadis pada baseline (task `done` setelah 3 s, bukan `cancelled`). | Buat context + cancel di `dispatch` **di bawah `e.mu`** sebelum run dipublikasikan; `execute` menerima context. | **Diperbaiki** — `dispatch` membuat `ctx, cancel`, `execute(r, ctx)`, `Cancel` memanggil `r.cancel()` tanpa cek nil. Regresi test di atas. |

### 3.3 Minor

| ID | Lokasi | Deskripsi | Saran | Status |
| --- | --- | --- | --- | --- |
| m-01 | `upload/service.go` (`parseMetadata`) | Key metadata `share` (file request, Step 18a) **diabaikan diam-diam**: byte mendarat di folder uploader sendiri, bukan ditolak. Checklist (a) "upload ke share (belum ada) ditolak". | Tolak `400 VALIDATION_FAILED fields.share=not_implemented` sampai 18a mengganti dengan jalur share. | **Diperbaiki** — regresi `TestReviewCMetadataCannotLeaveRootOrTargetShares`. |
| m-02 | `download/policy.go` | `.mhtml`/`.mht`/`.eml` (arsip web berisi bagian HTML + script) dilayani `text/plain` attachment — sudah attachment, tetapi bukan `application/octet-stream` seperti anggota lain keluarga konten aktif; `multipart/related`/`message/rfc822` tidak ada di `dangerousMIME`. | Tambahkan ke `dangerousExt`/`dangerousMIME`. | **Diperbaiki** — matriks `TestReviewCInlinePolicyMatrix` mencakup `saved.mhtml`/`saved.mht`. |
| m-03 | Harness test `archive`, `httpapi`, `remotedl` | `bytes.Buffer` log ditulis goroutine engine/runner sementara test membaca `logs.String()` → laporan race detector (test, bukan kode produksi), membuat `go test -race` tidak deterministik. | Buffer ber-mutex. | **Diperbaiki** — `syncBuffer` di tiga harness. |
| m-04 | `archive/browse.go` (`indexCLI`) | Jalur CLI (`7z l`, dipakai iso dan 7z yang ditolak library) tidak bisa membedakan header plain vs terenkripsi. | Tandai `HeaderEncrypted` bila listing memakai password (konservatif: listing ulang tanpa password diminta memasukkan password lagi). | **Diperbaiki** (bagian M-02). |
| m-05 | `httpapi/archives_browser.go`, `archive/session.go` | `sess` pada `entry-content` adalah bearer 128-bit **tanpa tanda tangan** di query (terikat user + path, TTL 10 menit sliding, maks 32/user); `POST /archives/sign` belum punya rate limit khusus; `Cache-Control: private, max-age=300` pada entry-content. Sudah dicatat 14b. Risiko rendah (butuh sesi login + signed URL yang juga terikat user). | Rate limit `archives/sign` + evaluasi `no-store` saat hardening. | **Ditunda → Step 20** (Catatan lintas step). |
| m-06 | `archive/zipedit.go` | Staging `.fm-zipedit-<task>-<nama>` tampil sesaat di folder arsip sebelum `Rename` (sudah dicatat 14b); listing cache 15 s bisa memuatnya. | Prefiks `.fm-` disembunyikan di listing atau staging di `DATA_DIR/tmp` dengan fallback EXDEV. | **Ditunda → Step 20/21** (Catatan lintas step). |

## 4. Checklist R-C — hasil verifikasi

| Butir | Hasil | Bukti |
| --- | --- | --- |
| (a) tus: finalisasi atomik | **M-01** → diperbaiki | `TestReviewCFinalizeAfterLockConflict` |
| (a) ukuran diverifikasi | OK | `preFinish` membandingkan `info.Size` vs `row.Size` vs byte di store; `Upload-Defer-Length` ditolak (`upload_test.go`) |
| (a) metadata `path` tidak bisa keluar root | OK | `TestReviewCMetadataCannotLeaveRootOrTargetShares`: `/in/../../../etc` → `PATH_INVALID`, `/.system` → `PATH_INVALID`, `relativePath ../../up`/`a/..\b` → `NAME_INVALID`, `filename ../x.txt` → `NAME_INVALID`; tidak ada file/reservasi tersisa |
| (a) upload ke share ditolak | m-01 → diperbaiki | idem (`share` → `VALIDATION_FAILED`) |
| (a) kuota dilepas pada terminate/cleanup | OK | `TestReviewCTerminateReleasesQuota` (reservasi 5 MiB hilang, sesi `aborted`, file hilang, upload berikutnya diterima); cleanup 24 jam di `cleanup.go` memanggil jalur yang sama |
| (b) signed URL: scope user, purpose, exp, constant-time, nonaktif, tanpa path absolut | OK | `TestReviewCArchiveEntryContentGates`: tukar scope → 403, tukar purpose → 403, URL `entry` di `/files/raw` → 403, `inner` traversal ditolak saat sign, `sess` asing → 404, akun dinonaktifkan → ditolak, tidak ada `STORAGE_ROOT` di URL/log; `download/signer.go` memakai `hmac.Equal` |
| (c) kebijakan inline `/raw` & `/entry-content` | OK (+ m-02) | `TestReviewCInlinePolicyMatrix`: html, svg, xml (XXE), xhtml, js, mhtml, mht, css, jpg tersamar → `application/octet-stream` attachment; pdf-dengan-JS → `application/pdf` attachment; png → `image/png` inline; semua dengan `CSP: sandbox; default-src 'none'`, `nosniff`, `Referrer-Policy: no-referrer` — pada `/files/raw` (media & inline), `/files/download`, `/archives/entry-content` |
| (d) zip streaming | OK | `TestReviewCZipRequestRefusals`: payload `/` dan `/.system` → 400, token user lain → 404, `files/sign` purpose zip ditolak; symlink dilewati → `_ERRORS.txt` (`zipstream_test.go` 12b) |
| (e) SSRF ejaan baru | OK | `TestReviewCSSRFSpellings`/`TestReviewCRedirectSchemeAndRelativeLocation`/`TestReviewCDialRefusesNonTCP`: `[fe80::1%eth0]`, `localhost.`, `0x7f000001`, `127.1`, `0177.0.0.1`, `[::ffff:127.0.0.1]`, `[::1]`, 307/308 ke `file:`/`ftp:`/`data:`, `//10.0.0.1/`, `Location` relatif (host sama, divalidasi ulang → lolos), redirect ke port privileged, dial `udp` — semua ditolak dengan alasan yang benar; resolver nyata juga menolak |
| (f) fixture arsip reviewer | OK | `TestReviewCHostileNamesAndLyingHeaders` (`..\..\evil` → `ARCHIVE_UNSAFE`, `./../` → unsafe, header 0 B/stream 3 MiB → `ARCHIVE_LIMIT max_extract_size` tanpa sisa, header 100 MiB/stream kosong → gagal bersih tanpa reservasi, tidak ada file nyasar di `DATA_DIR/tmp`), `TestReviewCSymlinkThenFileSameName` (tar + zip; canary di luar home utuh) |
| (f) password tidak di log/argv; CLI tanpa shell | OK (+ **M-03** path host) | `cli.go`: `-p` tanpa nilai + stdin; `TestSevenZipPasswordNeverInArgv` (14a) membaca `/proc/<pid>/cmdline`; `exec.Runner` argv array, tidak ada `sh -c` (grep); password task hanya di memori engine (`e.secrets`, dihapus saat dispatch) |
| (g) cancel membersihkan di semua runner | OK (+ **M-04** race) | `TestExtractCancelCleansUp`, `TestZipEditCancelKeepsOriginal`, `TestCancelRemovesPartAndReleasesQuota` (remotedl), copy/move via `files` |
| (g) `interrupted` saat SIGTERM | OK | `TestEngineInterruptedAndRecovery` |
| (g) `TASK_MAX_PER_USER` tidak bisa dilewati lewat retry | OK | `TestReviewCRetryCannotBypassPerUserSlots` (retry = baris baru yang antre; `TASK_LIMIT` menghitung retry; user lain tetap dapat slot; get/retry/delete lintas user → 404) |
| (g) SSE tidak membocorkan event user lain | OK | `TestReviewCBrokerNeverCrossesUsers` (ring per user, `Last-Event-ID` silang → `resync` tanpa replay, event sistem tanpa user dibuang) |
| (h) RSS beban gabungan | Tercatat | §6 |
| Fuzz 60 s | OK | `FuzzEntryName` 127.538 eksekusi / 125 corpus; `FuzzNormalize` 674.944 eksekusi / 100 corpus — tanpa crash |

## 5. Perbaikan

Commit `fix(review-C): …` memuat:

- `internal/upload/tushandler.go` (baru) + `service.go` (`FinishStored`, `Tus()` mengembalikan `*Handler`, penolakan `share`), `httpapi/upload.go` (route HEAD/PATCH memakai handler).
- `internal/archive/entry.go` (`detectEncrypted`, `hdrEnc`, `HeaderEncrypted` 7z/rar, `isEncryptedHeaderErr`), `index.go` (`Index.HeaderEncrypted`), `browse.go` (penolakan cache hit tanpa password, `indexCLI`), `cli.go` (`scrubHostPaths`).
- `internal/tasks/engine.go` (context dibuat di `dispatch`).
- `internal/download/policy.go` (`mhtml`/`mht`/`eml`, `multipart/related`, `message/rfc822`, `application/x-mimearchive`).
- Harness test `syncBuffer` di `archive`, `httpapi`, `remotedl`.
- Test regresi: `internal/{upload,tasks,sse,archive,httpapi,netguard,fsjail}/review_c_test.go` (17 test, `FuzzEntryName`, `FuzzNormalize`).

Tidak ada perubahan API publik atau SPEC: respons HEAD/PATCH hasil finalisasi tertunda identik dengan respons tusd normal (`Upload-Result`/`Upload-Path` sudah didokumentasikan di openapi untuk PATCH terakhir); `ARCHIVE_PASSWORD_REQUIRED`/`WRONG` kini dipakai konsisten untuk 7z `-mhe` (sebelumnya `ARCHIVE_CORRUPT`, dicatat sebagai catatan 14a yang kini tidak berlaku lagi).

## 6. Pengukuran beban gabungan (checklist h)

Binary nyata (`make build`), `APP_ENV=development`, `DATA_DIR` dan `STORAGE_ROOT` pada disk `/` (ext4), tiga beban dimulai bersamaan: upload tus 1 GiB (64 PATCH × 16 MB, data acak), compress 1 GiB data acak → zip (deflate normal), extract zip Store 1 GiB. Sampling `VmRSS`/`VmHWM` proses `filemanager` tiap 1 s.

| Ukuran | Nilai |
| --- | --- |
| Host | 4 vCPU, 5,9 GB RAM (`GOMAXPROCS=4`), `TASK_MAX_PER_USER=3`, `TASK_MAX_GLOBAL=4` |
| RSS idle (setelah start + login) | 168 MB |
| RSS puncak (sampling 1 s) | 176 MB |
| `VmHWM` proses (puncak sejak start) | **216 MB** |
| RSS anak (7z dll.) | 0 MB (zip = engine Go, tanpa CLI) |
| Durasi compress 1 GiB → zip (deflate normal, data acak) | 4,1 s |
| Durasi upload 1 GiB (64 PATCH × 16 MB) | 4,7 s |
| Durasi extract zip Store 1 GiB | 7,1 s |
| Total wall (ketiganya bersamaan) | 8,3 s |

Ketiga beban selesai `done`, hasil terverifikasi (zip 1.073.823.909 B, file upload 1.073.741.824 B, folder hasil extract). Puncak memori proses **≈ 216 MB** untuk tiga aliran 1 GiB serentak — jauh di bawah anggaran 1,9 GB server lama sekalipun; tidak ada buffering file utuh di memori (tusd streaming ke disk, zip writer `bufio` 1 MiB, extract streaming per entri).

Catatan: server telah di-upgrade pada 15 Oktober 2026 (4 vCPU, 5,9 GB RAM, disk `/` 58 GB) — angka SPEC/PROGRESS sebelum tanggal itu diukur pada 1 vCPU/1,9 GB. `TASK_MAX_GLOBAL` default `max(2, 2×NumCPU)` kini 8.

## 7. Catatan untuk step berikutnya

- **Step 15–17 (viewer/editor):** `entry-content` untuk 7z terenkripsi kini memberi `session`; viewer di dalam arsip tinggal memakai `signArchiveEntry(path, inner, session)`.
- **Step 18a (file request):** ganti penolakan `share` di `parseMetadata` dengan jalur share (`fsjail.SubRoot`), pertahankan `TestReviewCMetadataCannotLeaveRootOrTargetShares` dengan ekspektasi baru.
- **Step 20:** m-05 (rate limit `archives/sign`, `sess` bearer), m-06 (staging zipedit), evaluasi `details.stderr` 7-Zip — pertimbangkan hanya di log server, tidak di respons.
- **Step 21:** angka §6 sebagai baseline memori; `UPLOAD_CHUNK_SIZE` 16 MB membuat RSS upload datar (tusd streaming), beban memori didominasi deflate + buffer zip 1 MiB per task.
