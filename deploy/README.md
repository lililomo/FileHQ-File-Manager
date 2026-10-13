# deploy/

Berkas deployment dibuat di **Step 22** (lihat `PROMPT.md`):

- `install.sh` — pemasangan idempoten di Ubuntu 22.04/24.04 (dependency apt, Go, user `filemanager`, storage, `.env`, systemd).
- `update.sh` — update + migrasi + restart dengan rollback otomatis bila `/readyz` gagal.
- `filemanager.service` — unit systemd dengan hardening (SPEC §12.2).
- `nginx-aapanel.conf` — snippet yang ditempel lewat UI aaPanel (SPEC §12.4).
- `Dockerfile`, `docker-compose.yml` — opsional.

Sampai Step 22, folder ini sengaja kosong selain README ini.
