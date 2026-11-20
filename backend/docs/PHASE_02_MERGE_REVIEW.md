# Audit kesiapan merge Phase 02

Baseline Draft PR #3 `6f856fb491f60e7bcb9eb2d7ab17c54292588753` di `feat/phase-02-platform`. GitHub melaporkan `MERGEABLE` dan `CLEAN` pada saat audit, serta CI push dan pull request sukses pada SHA yang sama. Pemeriksaan ini mencakup risiko kode, kontrak dan kompatibilitas schema. [CI push](https://github.com/PTKPP/hi-ve/actions/runs/44660534038) · [CI pull request](https://github.com/PTKPP/hi-ve/actions/runs/44660540648)

## Keputusan merge yang direkomendasikan

**PR #3 tetap Draft sebagai Phase 02 lengkap.** Exit gate di tracker masih memerlukan sesi operator nyata dengan credential yang disetujui, cakupan outlet switch, Settings/Audit yang masih terbuka, serta acceptance hak per field dan consumer yang relevan. Green CI membuktikan verifikasi yang ada. DV2 dengan fixture tidak membuktikan login operator atau alur browser end-to-end. Pemeriksaan target production memiliki gate sendiri.

**Jika baseline engineering perlu masuk lebih dini**, rekomendasikan satu PR fondasi atas subset yang disetujui, dengan PR lanjutan untuk konteks operator dan fitur yang masih terbuka. Tetapkan ulang acceptance dan daftar commit sebelum memisahkan. Mulai dari Group 001–021 yang mempertahankan urutan schema dari main. Evaluasi S3-T dan pengaman M5 sebagai calon slice tambahan setelah pemetaan dependency. Kedua slice itu sekarang memakai nomor 024–027 di belakang fondasi DV2 022–023. Jangan cherry-pick versi yang melompati urutan. Pertahankan nomor migrasi yang sudah digunakan pada database persisten, atau rekonsiliasi dengan explicit migration plan yang direview sebelum memilih baseline lain. Masing-masing subset perlu CI independen, jalur upgrade dari main dan keputusan merge sendiri. Saat ini belum ada pemisahan branch atau persetujuan merge.

## Exit gate dan acceptance

| Area | Bukti HEAD dan kekurangan |
|---|---|
| Provisioning, Google Owner/Admin, RBAC dan lifecycle | PostgreSQL disposable dan OIDC fixture membuktikan linking atomik, sesi terpisah, expiry/rotasi/revoke/logout, staff Google denial, serta isolasi tenant dan konkurensi grant. Akses lokasi exact diperiksa Identity. Penyelesaian field-level authorization bernilai bisnis menunggu policy dan consumer Catalog/Inventory/Reports yang relevan. OPEN-004 juga tetap berlaku untuk recovery Owner. |
| Entitlement dan Locations | Entitlement online memiliki revision dan window enforcement teruji dengan consumer fixture. Locations Foundation memberi outlet yang sah untuk pairing. Consumer bisnis dan izin sumber/tujuan transfer tetap mengikuti fasenya. |
| Pairing, konteks operator dan Audit | Pairing/revoke, proof dan bounded cleanup teruji. Constraint satu konteks, stale proof, scoped logout dan grant live teruji dengan fixture sesi. Penerbitan sesi dan profile discovery menunggu DV3; switch outlet dan pending evidence menunggu OPEN-013. S2 hanya mencakup histori Owner Google 168 jam. Event operator, critical audit reader dan kebijakan retensi masih terbuka. |
| Settings dan neutral Identity | S3-T teruji dengan revision, idempotensi dan audit. S3-G/S3-A belum final. Pengaman Google khusus Owner sudah tervalidasi di disposable. Pelepasan FK Google-only tetap menunggu review dan keputusan Contract. |

## Review migrasi 001–027

| Versi | Kompatibilitas dan risiko |
|---|---|
| 001–007 | Fondasi schema, provisioning, sesi, RBAC dan audit aditif. Periksa hak pembuatan objek di schema dan version table untuk migrator terpisah. |
| 008–015 | Entitlement, lifecycle, registry identitas dengan bridge writer, outlet dan audit logout aditif. 012 mem-backfill UUID dan memvalidasi FK. Uji kompatibilitas writer terbatas lulus lokal. Di target, ukur backfill, scan dan durasi lock sesuai volume data. |
| 016–019 | Pairing, audit dan histori online aditif. 018 menandai sesi lama sebagai `google`, mem-backfill event ID alur singkat dan membuat unique constraint. Deploy schema sebelum binary baru. Saat cutover, hentikan writer lama yang masih dapat membuat sesi Google staf agar aturan Owner-only konsisten. |
| 020–023 | Scoped grant, konteks perangkat dan audit menambah tabel/indeks. 021 mengganti allowlist audit dengan superset yang divalidasi. 022 melonggarkan CHECK bucket rate limit dan menambah constraint. Tidak ada penghapusan row atau tabel. Binary lama dapat membaca data dasar, tetapi versi sebelum integrasi lifecycle tidak membersihkan assignment/konteks operasional. Rollback lintas versi setelah data fitur baru ada perlu diuji per kemampuan. |
| 024–027 | Writer bridge dan kolom khusus Owner mendahului backfill/validasi 025. FK Google-only tetap utuh. Settings dan audit aditif. Simulasi rollback M5 memakai tabel fixture dan pola SQL writer lama/baru. Ukur lock dan WAL 025 pada data representatif sebelum target bersama. |

Tern menyimpan versi, mengambil advisory lock dan menerapkan transaksi per file. Bukti disposable mencakup fresh install, retry versi terakhir, dua migrator bersaing, upgrade fixture, serta rollback DDL dan versi pada file yang gagal. SQL migration memakai ledger Tern untuk idempotensi. Eksekusi ulang manual DDL atau downgrade lintas versi bukan prosedur pemulihan. Pemeriksaan source tidak menjamin kompatibilitas semua kombinasi binary/schema atau durasi lock pada volume production.

## Risiko teknis yang perlu keputusan

- **DV2 dan akses challenge.** `POST /challenges` dapat menerbitkan slot untuk ID device aktif tanpa proof awal. Batas outstanding per device terisi sebelum validasi tanda tangan. Klien dalam batas kuota peer yang mengetahui ID dapat menghambat device mengambil nonce untuk sementara. Review strategi pemesanan challenge dengan bukti perangkat atau bucket yang tidak membuat kuota device habis akibat caller belum terbukti. Tambahkan acceptance bahwa traffic tanpa otorisasi tidak menghentikan device sah. Temuan ini menahan gate DV2 operasional, termasuk penggabungan subset yang mengekspos endpoint tersebut sebelum keputusannya selesai. [Implementasi DV2](../internal/devices/operational.go) · [Issue #8](https://github.com/PTKPP/hi-ve/issues/8)
- **Ordering dan scope.** Protected write perlu mempertahankan lock membership, role, device dan resource yang relevan sampai commit. Pengujian saat ini mencakup lock terarah, replay dan rollback pada jalur yang ada. Consumer bisnis baru harus menegakkan scope resource sebenarnya dan entitlement Platform. Jangan menggunakan assignment device sebagai grant Identity.
- **Credential dan session.** Google token/PIN tidak disimpan pada receipt fitur yang diaudit. Bukti perangkat memakai key P-256 dan nonce. Runtime membutuhkan HMAC/pairing key unik, rotasi dengan overlap, cookie/Origin yang sesuai serta endpoint Google/SMTP yang sah. Browser target perlu membuktikan IndexedDB/Web Crypto setelah reload, eviction dan multi-tab.

## Gate deployment, terpisah dari merge

Verifikasi Proxmox VM, private network/firewall, TLS `verify-full`, role runtime dan migrator, backup/PITR/restore, readiness, kapasitas dan durasi migrasi, rollback image/schema, trusted ingress dan integrasi Google/SMTP/browser aktual tetap terbuka. Hal-hal ini menghalangi target production dan bukan dasar tunggal untuk menahan penggabungan fondasi yang telah direview dan tidak dideploy. Seluruh verifikasi database dalam audit ini dilakukan lokal. [Desain dan prosedur](DEPLOYMENT.md)
