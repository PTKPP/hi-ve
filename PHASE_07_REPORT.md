# Laporan Phase 07 · Reports, Realtime dan Integrasi Frontend

**Checkpoint historis sebelum persetujuan lanjutan · PARTIAL / NO-GO.** PR #16 terverifikasi merged pada `cec0ce0703b2ed6ffb78e12d6d7b3f0e4a7b0c70`, titik awal `feat/phase-07-integration`. Scope yang teruji didokumentasikan di bawah. Backend tetap Go, PostgreSQL tetap otoritatif, dan rute Offline pada binary standar tetap tertutup.

Checkpoint backend `bc72695` dan frontend `3685d02`.

## Hasil per increment

| Increment | Cakupan dan bukti |
|---|---|
| 07.1 · FR-DASH-01/AC-01–05, FR-DASH-02/AC-01–05, UAT-10 | Owner melihat gabungan, outlet dan metode pada cohort tanggal pesanan. Hari ini/kemarin/rentang memakai zona waktu browser dan batas akhir eksklusif. Detail menelusuri agregasi ke penjualan dan item. UAT otomatis membuktikan 500.000 + 300.000 + 200.000 = 1.000.000, transfer dan pending tidak menambah omzet. Tes juga membuktikan diskon, retur dan waktu pembaruan. |
| 07.2 · FR-RPT-01/02, CAP-55–58 | Saldo lokasi/SKU/batch dan bucket, ledger mutasi dengan filter, input/output repacking via ledger dan lineage, serta kuantitas transfer/transit/discrepancy. Reports memakai kontrak baca Sales/Inventory. Daftar dan ekspor CSV menggunakan keyset sampai 50 baris per halaman. CSV berizin dengan proteksi formula untuk teks. Kalkulasi HPP, margin dan valuasi tetap OPEN-002/014. |
| 07.3 · FR-NOTIF-01/AC-01–02/04–06 | Outbox PostgreSQL dan cursor tenant, WebSocket dengan otorisasi ulang, fallback polling, notifikasi di aplikasi serta preferensi Owner. Suite menguji event baru terlihat setelah commit, rollback tidak terlihat, origin salah ditolak dan reconnect melanjutkan cursor. Background Web Push **belum tersedia** untuk AC-03. |
| 07.4 · CAP-50, UAT-HV-10 | Dashboard/laporan/ekspor, navigasi berdasarkan entitlement, detail, indikator WebSocket, preferensi dan pembaruan data di Hi-Ve App. Chromium menguji render, drill-down, efek event duplikat dan polling. Feed tidak menyentuh offline queue atau status ACK. Regresi Phase 06 tetap berjalan. |
| 07.5 · FR-HV-PLT-001/002, FR-HV-SET-001, FR-HV-ADM-001 | Superadmin statis mengintegrasikan sesi Google platform, provisioning dan aktivasi existing, kirim ulang undangan Owner, baca direktori, entitlement dan audit provisioning dan entitlement tersaring. Tenant cookie tidak membuka API admin, platform cookie tidak membuka Reports tenant. Aktivasi awal tersedia. Mutasi suspend/reactivate/close, Owner recovery/transfer dan key Global Settings konkret belum tersedia. Landing statis minimal dari repository yang belum memiliki Landing. |
| 07.6 · Final Audit | Gate otomatis dan batas bukti dicatat di bawah. Phase 07 belum ENGINEERING COMPLETED. UAT fisik, ingress target dan printer tetap di Phase 08. |

## Definisi angka dan kontrol

- Kohort memakai waktu order server pada interval `[from,to)`, maks. 93 hari. Status pembayaran dan retur mengikuti snapshot baca PostgreSQL, sehingga angka periode asal dapat berubah setelah pembayaran atau pembalikan berikutnya. Bruto adalah nilai sebelum diskon pada penjualan tunai atau pembayaran manual terkonfirmasi. Neto adalah bruto dikurangi diskon, retur terposting dan void terposting. Pembayaran tercatat dan refund manual tercatat ditampilkan terpisah. Pending manual ditampilkan terpisah, pending browser/offline tidak ikut dihitung. Server tidak mengklaim verifikasi provider.
- Owner Google tenant aktif dan entitlement canonical diperiksa pada setiap pembacaan, detail dan ekspor. Query waktu/stok berindeks dan selalu berparameter tenant. Sales/Inventory tetap pemilik tabelnya. Filter lokasi berlaku pada unit data yang diminta. Belum ada grant laporan untuk staf. DTO/API/CSV tidak memuat HPP, margin atau valuasi.
- Realtime mengevaluasi transactional outbox dan memilih trigger deferred agar penulisan Sales lama yang kompatibel tetap menerbitkan intent atomik. Counter tenant dialokasikan pada commit untuk urutan replay. Transport tidak menulis efek bisnis. Pesan hanya memuat sequence, outlet, ID penjualan dan jenis perubahan. Pending server dapat memicu penyegaran laporan tanpa notifikasi penjualan diterima. ACK jual offline tetap terpisah.
- Report routes berada di `GET /api/v1/app/tenants/{tenant_id}/reports/{dashboard|sales|inventory|movements|transfers}`. Detail sale menerima ID outlet eksplisit. `format=csv` mengekspor halaman aktif dan memberi cursor halaman berikutnya. Realtime memakai `/realtime/events`, `/realtime/socket` dan `/realtime/preferences`.
- Superadmin read tambahan memakai `platform.tenants.read` dan `platform.audit.read` sebagai grant platform eksplisit. Audit tenant hanya menampilkan metadata provisioning dan entitlement, tanpa referensi verifikasi atau data transaksi tenant. Tidak ada grant otomatis.
- Build statis menghasilkan `apps/hi-ve-app/dist`, `apps/superadmin/dist` dan `apps/landing/dist`. Jalankan `npm run build` dari Hi-Ve App dan `node apps/scripts/build-static.mjs` dari root. Origin build `HIVE_APP_API_ORIGIN` dan `HIVE_SUPERADMIN_API_ORIGIN` menerima origin publik HTTPS atau HTTP loopback. Landing ditujukan ke Cloudflare Pages, App dan Superadmin ke Workers Static Assets. Semua business API tetap di Go.

## Bukti dan exit gates

| Gate | Status |
|---|---|
| UAT-10, isolasi tenant/outlet, entitlement, status pembayaran | **PASS automated** untuk skenario yang dijalankan. |
| PostgreSQL disposable 001–052, 050→052, retry/rollback dan preflight 028 | **PASS lokal**. Migrasi 051 menambah indeks dan 052 menambah outbox/preferensi serta trigger. |
| Go, race, vet, build, guard, Govulncheck dan rehearsal M5 | **PASS lokal**. |
| Frontend dan regresi | **PASS lokal**, 25 tes Chromium dan dua tes Node. Termasuk tiga integrasi baru dan regresi Phase 06. Ketiga build static-only lulus. |
| GitHub Actions | Checkpoint `447d285` lulus job `verify` dan `pwa` pada push dan pull request. Hasil revisi terbaru tersedia pada checks PR. [CI push checkpoint](https://github.com/PTKPP/hi-ve/actions/runs/45134793913) · [CI PR checkpoint](https://github.com/PTKPP/hi-ve/actions/runs/45134837053) · [Checks Draft PR #17](https://github.com/PTKPP/hi-ve/pull/17/checks) |
| Superadmin P0 dan background Web Push | **PARTIAL**. Lihat keputusan dan bukti yang masih diperlukan. |
| Pilot, OIDC/SMTP/ingress aktual, perangkat fisik | **NOT RUN**, OPEN-015 dan exit Phase 08. |

## Keputusan dan risiko residual

- **S3-G** membutuhkan persetujuan key global non-secret bertipe, consumer dan permission sebelum write API/UI. Allowlist sebelumnya kosong. Mutasi lifecycle setelah aktivasi dan recovery/transfer Primary Owner memerlukan cakupan Admin API, tindakan atas sesi saat restore, dan acceptance yang disetujui. Direktori dan aktivasi awal sudah independen. OPEN-004 tetap berlaku untuk recovery/transfer.
- **Background Web Push** membutuhkan kontrak subscription per browser, consent, kredensial dan pilihan delivery/ingress. Versi ini memberi notifikasi dalam aplikasi dan preferensi. Pengiriman background serta validasi kanal target belum tersedia.
- Indeks aditif perlu pengukuran waktu pembangunan pada volume target. Outbox menambah satu titik serialisasi singkat per tenant saat commit dan terus tumbuh sampai kebijakan retensi disepakati. Ukur latensi realtime, koneksi per tenant dan laju pertumbuhan outbox sebelum pilot.
- Laporan keuangan memakai cohort tanggal order dan status sekarang. Rekonsiliasi bank atau snapshot tutup buku berada di luar metrik ini. Export merupakan halaman berizin sehingga snapshot beberapa halaman dapat berbeda bila ada perubahan concurrent.
- OPEN-002/014 tidak ditutup. Tidak dilakukan perubahan PRD, migrasi persisten, deployment, atau aktivasi Offline.

## Engineering review internal setelah persetujuan lanjutan

Ketiga keputusan diterapkan pada migrasi aditif 053–054. PR #17 tetap Draft untuk Final Audit terpisah menggunakan Codex High. Bagian Final Audit sebelumnya adalah checkpoint yang mendahului persetujuan ini. Belum ada keputusan engineering completion atau pilot pada checkpoint ini.

| Keputusan | Implementasi dan batas |
|---|---|
| S3-G · FR-HV-SET-001 | `notifications.web_push_enabled` adalah boolean bertipe dengan default false. Pembacaan memakai `platform.settings.read` dan penulisan memakai `platform.settings.update`. Revision optimistis, key replay dan audit atomik. VAPID hanya melalui `HIVE_WEB_PUSH_VAPID_PUBLIC_KEY`, `HIVE_WEB_PUSH_VAPID_PRIVATE_KEY` dan `HIVE_WEB_PUSH_VAPID_SUBJECT` dari environment. |
| Lifecycle/Owner · FR-HV-PLT-001/002 | Suspend, reactivate dan close memakai permission terpisah dan revision tenant. Closed terminal. Transfer mensyaratkan case ID unik per tenant, alasan, referensi bukti manual, Owner asal dan principal Google dengan Google issuer yang sudah terdaftar. Tepat satu Owner aktif dipertahankan secara atomik, sesi dan challenge lama dicabut, konteks operator ditutup dan epoch tenant dinaikkan. Histori, audit serta antrean pending dipertahankan. Tidak ada flow enrollment baru untuk calon yang belum memiliki principal Google tepercaya. |
| Web Push · FR-NOTIF-01/AC-03 | Browser opt-in setelah API mengiklankan public key yang valid. Subscription terkait sesi tenant Google dan dua revision, yaitu setting dan entitlement. Logout/rotasi menghapus subscription melalui FK. Revoke/expiry menghentikan delivery dan grant baru tidak mengaktifkan subscription lama. Worker menggunakan outbox setelah commit, validasi ulang izin dan payload generik tanpa angka penjualan. Endpoint HTTPS provider dibatasi dan redirect ditolak. |
| Zona waktu laporan · FR-DASH-01/AC-02 | UI mengirim tanggal kalender dan parameter zona waktu IANA yang divalidasi server. Default UTC. SQL tetap memakai batas absolut eksklusif; rentang 25 jam saat DST dan zona invalid tercakup tes. Kontrak lama `from/to` RFC3339 tetap didukung. |

### Bukti dan risiko

- Test disposable mencakup default setting, pemisahan permission, race revision, penolakan endpoint di luar allowlist, delivery at-most-once, revoke, logout, transfer replay, rollback audit, suspend/reactivate/close dan histori tetap utuh.
- Go, vet, race, build, Govulncheck, architecture guard, preflight Compose 028 dan PostgreSQL disposable 052→054 lulus lokal. Dua tes Node dan 26 tes Chromium lulus. Suite Chromium menambah simulasi opt-in serta revoke per browser. Aktivasi memakai fixture browser; provider dan perangkat fisik **NOT RUN**. In-app, polling dan rollout Offline tidak bergantung pada Web Push.
- Cursor push diklaim permanen sebelum HTTP untuk mencegah duplikasi lintas replica. Worker yang berhenti setelah claim atau respons provider ambigu dapat kehilangan sinyal push. Feed dalam aplikasi tetap dapat diputar ulang. Delivery memakai TTL nol untuk menghindari antrian provider setelah revoke. Notifikasi yang telah muncul di perangkat tidak dapat ditarik kembali.
- Transfer memerlukan principal Google sah yang sudah terdaftar. Calon tanpa principal ditolak sampai ada enrollment Google yang telah diverifikasi. Fakta kepemilikan organisasi diteliti di luar aplikasi oleh Superadmin. Aplikasi menyimpan referensi dan tindakan yang diaudit.
- OPEN-002/014 tetap terbuka. OPEN-015, kompatibilitas provider/browser fisik, dan validasi kapasitas delivery masuk Phase 08. Semua migration run hanya pada PostgreSQL disposable. Offline pada binary standar tetap tertutup. [Checks HEAD Draft PR #17](https://github.com/PTKPP/hi-ve/pull/17/checks)

[Keputusan S3-G dan Web Push · Issue #9](https://github.com/PTKPP/hi-ve/issues/9#issuecomment-7095527544) · [Keputusan lifecycle · Issue #2](https://github.com/PTKPP/hi-ve/issues/2#issuecomment-7095528162)
