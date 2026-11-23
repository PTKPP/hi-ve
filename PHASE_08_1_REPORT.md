# Phase 08.1A · Staging Infrastructure Foundation

**Implementasi tersedia untuk Independent Final Audit.** Baseline PR #17 merged pada `746ced40911e9564b501cd85567ec25c8bbaf56f`. Branch `feat/phase-08-1a-staging` menyiapkan artefak dan kontrol staging bagi monolit Go dan PostgreSQL 18 native pada VM terpisah. Bukti lokal memakai fixture disposable. Kesiapan staging dan pilot mengikuti verifikasi target.

| Gate | Status dan bukti |
|---|---|
| Image dan provenance | **PASS.** Dua build lokal dari `91eb0b3` menghasilkan manifest `linux/amd64` identik, `sha256:4d9309eda44a59bf5b351610a2cc6a7a50def41d28bfb45711354085579c1c80`. Index OCI berbeda karena metadata attestation. Smoke melewati Docker load dan probe non-root di image `scratch`. CI membangun dari Git archive dan menyimpan OCI tar, BuildKit provenance dan `release.json`. Registry serta digest rilis target mengikuti verifikasi Ops. |
| Compose dan runtime | **PASS lokal.** Satu service API loopback-only, resource limits, log rotation, healthcheck, root filesystem read-only dan kapabilitas dilepas. Tes meliputi readiness sukses/gagal, fail-closed saat versi schema tidak memenuhi minimum, DB tidak tersedia, shutdown menunggu HTTP dan socket, serta frame 1001 dan sambung ulang dengan cursor. |
| PostgreSQL TLS dan roles | **PASS disposable.** PostgreSQL 18 menjalankan migrasi sebagai `hive_migrator`. Go terhubung sebagai `hive_app` melalui TLS terverifikasi, menolak CA/nama host salah dan membatasi concurrency pool. HBA menolak plaintext. Smoke role menolak DDL, penulisan ulang audit dan akses Offline/purge. Trigger lokasi, epoch 055, dan outbox bekerja pada privilege runtime. |
| Migrasi dan recovery | **PASS disposable.** Fresh 001–055, 054→055, lock timeout, retry konkuren, rollback DDL, pairing epoch dan M5 rehearsal. Base backup fisik via role replication dilanjutkan WAL dan restore point pada port/socket terpisah, lalu diverifikasi bahwa hanya baris sebelum restore point yang pulih. Rehearsal memakai arsip lokal disposable. |
| Regresi dan security | **PASS lokal.** Format, Go vet/race/build, architecture guard, Govulncheck tanpa temuan, verifikasi Compose development maksimal 028, build statis tiga frontend, audit npm, 2 tes Node dan 28 tes Chromium. Binary standar mempertahankan rollout Offline tertutup. |
| Cloudflare dan provider | **PASS template/build · NOT VERIFIED target.** Pages Landing serta Workers Static Assets App/Superadmin. `cloudflared 2026.11.2` memvalidasi template lokal dan mencocokkan path callback API ke loopback serta `/readyz` ke catch-all 404. Hostname, cookie, provider dan perilaku cache/WSS target masih membutuhkan uji. |
| CI | `verify`, `pwa` dan `staging-image` lulus pada event push dan pull request untuk implementasi `91eb0b3`. Artefak push menggunakan SHA branch. Artefak pull request menggunakan SHA integrasi sintetis GitHub. [Push](https://github.com/PTKPP/hi-ve/actions/runs/45207770277) · [Pull request](https://github.com/PTKPP/hi-ve/actions/runs/45207830912) · [Checks terkini PR #18](https://github.com/PTKPP/hi-ve/pull/18/checks) |

## Fokus audit independen

- Verifikasi traceability commit dan digest OCI serta reproducibility manifest platform. BuildKit attestation bukan kebijakan signing untuk registry target.
- Review privilege `hive_app` atas use case lengkap. Rehearsal role terbatas mencakup alur representatif. Suite bisnis penuh berjalan dengan role fixture yang bisa menyiapkan data.
- Periksa staging schema 055, prioritas shutdown dan timeout saat socket atau DB sedang aktif. Rollback hanya memakai image yang lulus verifikasi authorization epoch 055.
- Review kontrak allowlist DB, off-VM archive dan recovery. Product Owner/Ops perlu menetapkan RPO/RTO, retensi, jendela perubahan dan pemilik alarm. Ops perlu memasang penyimpanan arsip, CA, hostname, OAuth/SMTP dan Tunnel target.
- Kuota peer Tunnel masih dapat dipakai bersama beberapa perangkat. Uji pada target untuk menetapkan apakah trusted ingress perlu pengembangan tambahan. Web Push masih at-most-once. UAT perangkat, provider, printer, p95 dan kesiapan pilot tetap NOT VERIFIED.

[Runbook deployment, rollback, recovery dan checklist bukti](backend/docs/STAGING.md)
