# Menjalankan dan merilis backend Hi-Ve

Development dan production memakai binary Go dari source yang sama, PostgreSQL 18, kontrak API yang sama, dan rantai migrasi `backend/migrations/`. Development memakai systemd dan PostgreSQL di Docker Compose. Target production memakai container backend di VM aplikasi Proxmox dan PostgreSQL native di VM terpisah. Prosedur production di bawah adalah desain untuk review.

## Development lokal

Prasyaratnya Go sesuai `backend/go.mod`, Docker Compose dengan daemon lokal, sesi systemd user, OpenSSL, dan Tern v2.4.3. Perintah di bawah dijalankan dari root repo kecuali dinyatakan lain. Port tetap development ialah `127.0.0.1:55432` untuk PostgreSQL dan `127.0.0.1:8080` untuk HTTP. Jalankan perintah Docker dari host development.

1. Salin `backend/deploy/dev/postgres.env.example` ke `backend/deploy/dev/postgres.env` sebagai file mode 0600. Isi `POSTGRES_PASSWORD` dengan keluaran unik `openssl rand -hex 32`. Simpan file di luar Git. PostgreSQL menggunakan password ini ketika named volume diinisialisasi pertama kali. Perubahan file langsung tidak mengganti password pada database lama.
2. Pasang Tern dan jalankan PostgreSQL serta migrasi secara eksplisit.

   ```sh
   go install github.com/jackc/tern/v2@v2.4.3
   env -u DOCKER_HOST -u DOCKER_CONTEXT docker --context default compose \
     -f backend/deploy/dev/compose.yaml up -d --wait
   PATH="$(go env GOPATH)/bin:$PATH" backend/deploy/dev/migrate.sh migrate
   PATH="$(go env GOPATH)/bin:$PATH" backend/deploy/dev/migrate.sh status
   ```

   Wrapper hanya menerima `status` atau migrasi forward hingga 028. Preflight menyediakan salinan 001–028 saja ke Tern dan menolak prefix yang tidak lengkap. Promosi migrasi 029 pada source memerlukan persetujuan target tersendiri sebelum eksekusi persisten. Gunakan wrapper untuk Compose development. Ia mengabaikan DSN/PG* dari shell, memakai daemon Docker melalui socket lokal, memeriksa binding Compose, serta memakai database `hive_dev` dan password dari container development. Tes integrasi memakai project/container dan database disposable tersendiri dengan port acak. User `hive_dev` adalah bootstrap role untuk lingkungan lokal.

3. Salin `backend.env.example` ke `~/.config/hi-ve/backend.env` dengan mode 0600. Setelah file dibuat, isi DSN `postgres://hive_dev:<password-lokal>@127.0.0.1:55432/hive_dev?sslmode=disable` menggunakan password Compose. Isi HMAC dan pairing code key dengan nilai hex acak yang berbeda. Isi integrasi Google dan SMTP khusus development melalui kanal konfigurasi yang sah. Origin App/Admin berbeda dan callback perlu HTTPS. Backend menolak startup tanpa konfigurasi lengkap dan melakukan Google discovery. Origin `.test` hanya contoh untuk disesuaikan dengan proxy HTTPS development dan redirect yang terdaftar.

   ```sh
   install -d -m 0700 "$HOME/.config/hi-ve"
   test -e "$HOME/.config/hi-ve/backend.env" || install -m 0600 backend/deploy/dev/backend.env.example "$HOME/.config/hi-ve/backend.env"
   # Isi backend.env sebelum melanjutkan.
   install -d "$HOME/.local/bin" "$HOME/.config/systemd/user"
   (cd backend && go build -trimpath -o "$HOME/.local/bin/hive-api" ./cmd/api)
   install -m 0644 backend/deploy/dev/hive-api.service "$HOME/.config/systemd/user/"
   systemctl --user daemon-reload
   systemctl --user start hive-api.service
   systemctl --user status hive-api.service
   ```

   File konfigurasi yang sudah ada dipertahankan. Gunakan `journalctl --user -u hive-api.service` untuk status startup, lalu verifikasi respons pada origin development sesuai alur API yang diizinkan. Unit tidak menjalankan migrasi.

Untuk shutdown, hentikan `systemctl --user stop hive-api.service` lalu gunakan `docker --context default compose -f backend/deploy/dev/compose.yaml down` dari shell dengan `DOCKER_HOST` dan `DOCKER_CONTEXT` tidak diset. Named volume tetap ada. Gunakan pemeriksaan berikut untuk membaca versi schema dan membuat backup development.

```sh
unset DOCKER_HOST DOCKER_CONTEXT
docker --context default compose -f backend/deploy/dev/compose.yaml exec -T db \
  psql -X -U hive_dev -d hive_dev -c 'SELECT version FROM public.hive_schema_version'
umask 077
backup_dir="${XDG_STATE_HOME:-$HOME/.local/state}/hi-ve"
install -d -m 0700 "$backup_dir"
docker --context default compose -f backend/deploy/dev/compose.yaml exec -T db \
  pg_dump -U hive_dev -Fc hive_dev > "$backup_dir/hive-dev.dump"
```

Restore untuk verifikasi ke database lokal baru yang kosong. Pilih nama unik jika `hive_dev_restore` sudah ada.

```sh
docker --context default compose -f backend/deploy/dev/compose.yaml exec -T db \
  createdb -U hive_dev hive_dev_restore
docker --context default compose -f backend/deploy/dev/compose.yaml exec -T db \
  pg_restore --exit-on-error -U hive_dev -d hive_dev_restore < "$backup_dir/hive-dev.dump"
docker --context default compose -f backend/deploy/dev/compose.yaml exec -T db \
  psql -X -U hive_dev -d hive_dev_restore -c 'SELECT version FROM public.hive_schema_version'
```

Jauhkan dump dan env lokal dari Git. Migrasi automated memakai `backend/scripts/test-migrations.sh`, dengan database `hive_test` disposable dan marker tes. Verifikasi koneksi runtime dengan startup lokal dan koneksi SQL melalui loopback. Buktikan alur OIDC/SMTP menggunakan konfigurasi development khusus ketika tersedia.

## DV3 PIN-only online

Backend membuka rute DV3 hanya saat schema minimal 035 dan pepper online dikonfigurasi. Pengujian schema 029 ke atas dilakukan pada PostgreSQL disposable. Compose development tetap berhenti di 028. Simpan konfigurasi fitur pada penyimpanan secret yang terpisah dari PostgreSQL.

- `HIVE_PIN_PEPPER_VERSION` adalah integer positif. `HIVE_PIN_PEPPER` berisi 32 byte acak dalam format hex.
- `HIVE_PIN_PREVIOUS_PEPPER_VERSION` dan `HIVE_PIN_PREVIOUS_PEPPER` opsional sebagai pasangan untuk rotasi terencana. Set/reset memakai versi aktif. Pertahankan versi sebelumnya hanya selama credential aktif masih mereferensikannya dan key masih aman. Untuk key yang bocor atau hilang, koordinasikan penghapusan versi di semua replica dan reset Owner. Pembacaan konteks menolak credential versi yang sudah tidak tersedia.
- Konfigurasi seluruh variabel PIN kosong menutup rute DV3. Lifecycle dan cleanup histori tetap berjalan pada schema kompatibel. Untuk rollback binary, tutup akses DV3 dan akhiri konteks operator yang pernah diterbitkan sebelum memakai binary sebelumnya. Pertahankan schema aditif, UUID netral dan histori. Gunakan baseline rollback yang kompatibel dengan M5.
- Eksekusi pada target persisten mengikuti persetujuan terpisah. Verifikasi role database, key distribution, sumber direct peer, kapasitas Argon2id, Origin/cookie, dan browser target pada gate OPEN-015.

## Desain production di Proxmox

| Komponen | Desain |
|---|---|
| VM aplikasi | Image multi-stage yang dipin dan dibangun CI dari Go di `go.mod`. Builder menjalankan `CGO_ENABLED=0 go build -trimpath ./cmd/api` dari `backend/`. Runtime image minimal menyediakan CA roots untuk Google/SMTP, binary native, dan user non-root. Entrypoint hanya binary API. Pilih image hasil review dan deploy berdasarkan digest. |
| Compose backend | Satu service `api` dengan image digest, restart policy, read-only root filesystem, `no-new-privileges`, dan resource limits berdasarkan uji beban. Gunakan env file di luar repo dengan izin minimum dan mount CA PostgreSQL read-only. Jika Tunnel berjalan di host VM yang sama, publish port API hanya ke loopback. Gunakan `HIVE_LISTEN=0.0.0.0:8080` di network container. Jika Tunnel dipisah, review jaringan dan firewall sebelum membuka akses. |
| VM PostgreSQL | PostgreSQL 18 native pada private subnet Proxmox, dengan disk/data dan WAL sesuai kapasitas yang diukur. Firewall Proxmox/guest mengizinkan port 5432 hanya dari VM aplikasi dan jalur migrator yang disetujui. Pakai `hostssl` dan autentikasi SCRAM di `pg_hba.conf`. DSN memakai hostname yang cocok dengan SAN sertifikat dan `sslmode=verify-full` plus CA privat lewat `sslrootcert`. |
| Role database | Role migrasi mengelola schema, tabel, constraint, dan version table. Role aplikasi mendapat `CONNECT`, schema `USAGE`, baca version table, dan DML yang dibutuhkan API/cleanup. Uji juga privilege trigger, `ON CONFLICT` dan row locking dengan dua role nyata pada database disposable. Role app tidak memperoleh DDL atau kredensial migrator. Kredensial backup/restore memakai role terpisah sesuai prosedur. |
| Health dan pengamatan | Liveness menguji proses/listener dan shutdown signal. Readiness yang diusulkan memeriksa DB melalui role aplikasi, schema yang dibutuhkan binary, dan timeout pendek tanpa detail internal. Source saat ini memiliki startup probe DB/schema dan Google discovery. Endpoint readiness/liveness serta integrasi probe Compose/Tunnel masih memerlukan implementasi/verifikasi sebelum rilis. Pantau pool, PostgreSQL lock wait, latency, failure audit, dan backlog cleanup. |
| Backup dan kapasitas | Siapkan backup PostgreSQL terenkripsi di luar VM bersama WAL/PITR serta retensi/RPO/RTO yang disetujui. Uji pemulihan pada VM/DB terisolasi dengan role, sertifikat, dan data representatif. Simpan bukti versi schema, constraint penting, data, waktu restore dan hasil smoke API. Snapshot VM mendukung prosedur restore setelah konsistensi PostgreSQL diverifikasi. |

Contoh DSN tanpa secret untuk provisioning melalui pengelola konfigurasi, `postgres://hive_app@pg.internal.example:5432/hive?sslmode=verify-full&sslrootcert=/run/hive/pg-ca.pem`. Untuk smoke koneksi production setelah otorisasi, gunakan `psql` dengan DSN yang sama lewat credential provider, lalu periksa `SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()` dan versi schema. Uji negatif hostname/CA salah dan firewall dari sumber di luar allowlist pada target uji.

### Urutan rilis dan pemulihan

1. Review SHA, digest image, versi awal/akhir, kompatibilitas pembaca/penulis lama, runtime grants, kapasitas dan lock migrations. Tentukan backup point dan uji restore sebelum jadwal perubahan. Pada migrasi dengan backfill/validasi besar, ukur durasi, WAL, dan dampak ke writer. Pilih strategi rilis/maintenance sesuai hasil.
2. Gunakan satu job migrasi terkontrol dengan role migrasi, Tern versi dipin, DSN TLS dan timeout eksplisit dari jalur yang disetujui. Jalankan `tern status`, migrasi forward, lalu verifikasi `public.hive_schema_version` dan constraint. Job ini berada di luar siklus startup setiap replica.
3. Mulai image backend yang kompatibel dengan schema tersebut. Periksa readiness, koneksi role aplikasi, OIDC/SMTP, Origin/cookies dan alur API non-destruktif yang diotorisasi. Rollout berhenti bila verifikasi gagal. Rotasi key pairing menjaga key lama dan baru selama window challenge.
4. Jika migrasi gagal, hentikan rollout. Tern mencatat versi per migrasi sehingga migrasi yang sudah commit tetap ada dan langkah yang gagal perlu diperbaiki atau dilanjutkan secara terkontrol. Jika aplikasi gagal setelah migrasi, rollback image hanya ke versi yang terbukti kompatibel. Jika schema perlu koreksi, utamakan migrasi forward. Restore/PITR memerlukan keputusan downtime dan kehilangan data sesuai RPO/RTO serta uji pada target terisolasi.

Bukti lokal mencakup disposable PostgreSQL dan konfigurasi development. Kesiapan VM Proxmox, jaringan/TLS, restore/PITR, OIDC/SMTP aktual, perangkat browser dan trusted proxy tetap membutuhkan verifikasi target di bawah OPEN-015 dan gate rilis.
