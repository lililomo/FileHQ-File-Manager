# deploy/

Deployment ke Ubuntu + aaPanel (Step 22, SPEC §12). Panduan lengkap: [`docs/DEPLOY-AAPANEL.md`](../docs/DEPLOY-AAPANEL.md).

| File | Isi |
| --- | --- |
| `install.sh` | pemasangan idempoten (`--dry-run`, `--binary`, `--with-unrar`, …): apt, Go/Node bila build, user `filemanager`, storage 0750, `.env` 0600 bersih, unit systemd, tunggu `/readyz` |
| `update.sh` | update (`--binary` / `--release` / `--ref` / build) dengan `backup-db --label pre-update`, migrasi, restart, rollback otomatis |
| `lib.sh` | helper bersama kedua script (dry-run, `.env`, `wait_ready`) |
| `filemanager.service` | unit systemd dengan hardening (SPEC §12.2) |
| `nginx-aapanel.conf` | PART A (custom configuration URL proxy `/`), PART B (server block), PART C opsional |
| `aapanel/site.py` | otomasi opsional aaPanel (create/configure/ssl/force-https; perlu verifikasi per versi panel) |
| `Dockerfile`, `Dockerfile.dockerignore`, `docker-compose.yml` | image opsional (dibangun di CI) |
| `test/` | `install_test.sh`, `update_rollback_test.sh`, `nginx_snippet_test.sh` + shim `systemctl`/`journalctl` — dijalankan `make test-deploy`, hanya di folder scratch `.deploytest/` |
