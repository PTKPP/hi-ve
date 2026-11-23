# Laporan Phase 06 · PWA dan Offline

**ENGINEERING COMPLETED.** Product Owner memindahkan UAT perangkat Android/iOS, benchmark PBKDF2 perangkat target dan cetak printer fisik ke exit gate Phase 08. Ketiganya **NOT RUN**. Status kesiapan pilot belum dinyatakan lulus. Phase 07 dapat dimulai dari main terbaru setelah merge PR #16 oleh Product Owner. [PR #16](https://github.com/PTKPP/hi-ve/pull/16)

## Hasil per increment

| Increment dan requirement | Hasil implementasi |
|---|---|
| 06.1 · CAP-50/51/54 | Shell responsif dan instalabel, build static-only untuk Workers Static Assets, service worker precache shell tanpa API, dan IndexedDB upgrade v1→v2 mempertahankan antrean. |
| 06.2 · FR-POS-01–03, FR-HV-DEV-001/OPR-001/RCP-001 | Pairing P-256, login operator, SKU/barcode, harga outlet, diskon, checkout online, histori dan struk memakai backend existing. Diskon offline memerlukan izin pada lease. Cache harga terbatas pada harga jual yang pernah dimuat untuk outlet dan operator tersebut. |
| 06.3 · FR-OFF-01, UAT-05 | Checkout tunai offline menyimpan UUID, sequence, snapshot quote, tenant/perangkat/operator/outlet, waktu kejadian dan envelope bertanda tangan dalam satu transaksi IndexedDB. Status awal pending. Gagal simpan membatalkan konfirmasi. Nontunai dan mutasi administratif Inventory tetap online. |
| 06.4 · FR-OFF-02/03, UAT-06/07 | Replay identik mengembalikan ACK persisten. Posting memakai Sales dan Inventory dalam transaksi yang sama. Revoke, suspend, entitlement, lease, permission, quote dan shortage menjadi exception. Owner dapat memposting dengan hak server saat ini dan quote historis yang terbukti, atau mencatat refund manual dengan nominal, referensi dan alasan. |
| 06.5 · FR-HV-PIN-001/DEV-002/AUD-001 | Lease, PBKDF2 600.000, lockout lintas tab, Audit ingest dengan ACK persisten, dan revision outlet diterapkan. Switch Owner hanya online setelah jurnal disegel, exception selesai dan penutupan Owner tercatat. Kehilangan bukti memerlukan revoke perangkat dan penutupan beralasan. |
| 06.6 · CAP-50/52/53, UAT-HV-03–08 | UI membedakan jaringan, API, WebSocket, pending, synced, needs_review, gagal dan refund manual. Realtime WebSocket mengikuti Phase 07. Chromium fixture menguji panggilan browser print. UAT fisik **NOT RUN**. |

## Keputusan Product Owner

| Keputusan | Baseline APPROVED dan perilaku |
|---|---|
| OPEN-001 | Lease Ed25519 berlaku maksimal 24 jam dan berhenti lebih awal pada batas entitlement. Binding mencakup tenant, device, operator, outlet, revisi konteks/PIN/device, scope diskon, epoch Platform dan revision RBAC/entitlement. Web Crypto PBKDF2-HMAC-SHA256 memakai 600.000 iterasi dan salt acak. Verifier terpisah dilindungi CryptoKey HMAC non-extractable. Lima kegagalan mengunci 15 menit. Percobaan dicatat sebelum KDF. Login sukses tersedia setelah Audit queue commit. |
| OPEN-007 | Perangkat terbukti lewat signature ES256 dapat menyampaikan bukti setelah revoke/suspend. PostgreSQL menyimpan bukti dalam karantina `needs_review`. Tenant epoch mencegah lease lama auto-post setelah restore. Review memakai sesi Google Primary Owner aktif. |
| OPEN-008 | Posting otomatis tanpa grace memeriksa state dan revision saat menerima. Posting manual memeriksa hak dan entitlement sekarang serta quote historis dari Catalog. Waktu kejadian perangkat dan waktu terima server disimpan terpisah. Refund manual merekam tindakan yang sudah dilakukan melalui satu keputusan final per bukti dan dengan batas nilai bersih snapshot dan dana tunai yang dicatat. |
| OPEN-013 | Server mengetahui jurnal sejak lease diterbitkan. Segel bertanda tangan mencocokkan jumlah bukti berurutan yang telah diterima. Owner tetap harus menutupnya, termasuk jurnal dengan segel nol. Switch memeriksa assignment, revision dan exception. Recovery kehilangan bukti memerlukan revoke serta alasan yang diaudit. Bukti terlambat tetap dapat diterima sebagai exception. |

[Persetujuan di Issue #8](https://github.com/PTKPP/hi-ve/issues/8#issuecomment-7093429577) · [Audit login di Issue #9](https://github.com/PTKPP/hi-ve/issues/9#issuecomment-7093429934)

## Kontrak dan operasi

- Operator memakai `/api/v1/operator/tenants/{tenant}/devices/{device}/offline`. `POST /lease` memakai cookie operator, proof one-time dan state online. `PUT /leases/{lease}/sales/{id}`, `/logins/{id}` dan `/seal` memverifikasi signature perangkat atas scope, ID dan hash body. Cookie live tidak diperlukan untuk pengiriman bukti yang sudah ditandatangani. Checkout yang telah commit online tetap memakai endpoint ACK existing.
- Primary Owner memakai `/api/v1/app/tenants/{tenant}/offline`. `GET /review` membaca exception dan jurnal tenant. `PUT /sales/{id}/resolution` dan `PUT /leases/{lease}/close` merekam alasan keputusan. `GET/PUT /devices/{device}/outlet` membaca assignment dan mengubah outlet dengan revision. Switch mengakhiri context lama. Perangkat membaca outlet baru sebelum login operator.
- Offline menyimpan jurnal bukti dan mengoordinasikan Sales. Platform memegang lifecycle/entitlement, Identity memegang PIN dan grants, Devices memegang assignment/context, Audit memegang histori login. Order dan stok tetap dimiliki Sales/Inventory. ACK diberikan sesudah commit. Shortage dan konflik bisnis membatalkan posting melalui savepoint sambil mempertahankan exception.
- Migrasi aditif 050 menambah jurnal tanpa backfill penjualan. Backend pada schema lama tetap menawarkan jalur online existing. Compose development maksimal schema 028. Aset dibangun dari `apps/hi-ve-app`. Build untuk pengujian origin nyata memakai `HIVE_APP_API_ORIGIN` berupa origin API HTTPS yang didaftarkan bersama origin App dan cookie live same-site.

## Bukti verifikasi dan exit gate

Checkpoint implementasi backend `cf32781` dan PWA `05b01fe`.

| Gate | Hasil |
|---|---|
| Go, security dan module boundaries | **PASS lokal** untuk format, vet, race, build, architecture guard, actionlint dan Govulncheck Go 1.27.4. |
| Database disposable | **PASS lokal** untuk fresh/retry, 049→050, rollback migrasi, preflight Compose 028, rehearsal M5 dan regresi Phase 01–05. Tes disposable mencakup signature, isolasi, retry konkuren, stok atomik, revoke/suspend dan restore, lease expiry, shortage, rollback Audit dan Sales–Inventory, review/refund, segel/penutupan dan revision switch. |
| Browser dan build | **PASS lokal** untuk static build, dua tes Node, npm audit dan 22 tes Chromium fixture. Tes mencakup Web Crypto, lockout lintas tab, rollback jam, expiry, restart fail-closed, quota/eviction, upload bukti revoked dan ACK login. Chromium memakai fixture HTTP. Go disposable menjalankan handler, posting dan PostgreSQL sungguhan. |
| UAT-05–07 · pending, replay dan shortage | **PASS automated** untuk jalur yang diuji. UAT perangkat fisik **NOT RUN**. |
| UAT-HV-04–08 · outlet, PIN, Audit dan revoke | **PASS automated** untuk kontrak yang diuji. Kinerja KDF target, installability fisik, OIDC/ingress target dan pencetakan fisik **NOT RUN**. |
| GitHub Actions | Bukti Backend dan PWA tersedia pada checks HEAD PR #16. [Checks](https://github.com/PTKPP/hi-ve/pull/16/checks) |

## Risiko residual

- Browser tidak memiliki jam tepercaya yang terus dapat diverifikasi setelah cold restart. Sesi offline baru menunggu re-anchor waktu online. Bukti yang sudah disimpan tetap dapat dikirim. Perangkat terputus baru mengetahui revoke saat menghubungi server.
- Eviction seluruh origin atau perangkat hilang sebelum upload dapat menghapus transaksi yang belum pernah diterima server. Recovery mencatat kehilangan dan mencabut perangkat. Jurnal terdaftar, segel dan keputusan Owner melindungi perpindahan konteks. Draf lama dan checkout ambigu yang masih tersimpan perlu ditinjau sebelum segel.
- Kontrol PIN lokal mengandalkan isolasi browser dan integritas origin serta OS perangkat. Revalidasi server membatasi dampak bukti yang tidak memenuhi hak posting.
- PBKDF2 600.000 dijalankan pada Chromium fixture. Latensi, storage persistence, dukungan Web Crypto/Web Locks dan pencetakan perangkat kasir target perlu diuji. Realtime mengikuti Phase 07, ingress target mengikuti OPEN-015, dan OPEN-002/014 tetap di luar scope.


## Pengaman rollout dan exit Phase 08

Entrypoint Go standar tidak merakit service Offline. Rute lease, intake, review, dan switch tidak terdaftar pada pilot/production, termasuk jika binari berjalan di schema 050. PWA menyembunyikan enrollment saat API state tidak mengiklankan kemampuan offline. Architecture guard juga menolak import Offline langsung dari entrypoint standar. Fixture disposable merakit service secara eksplisit untuk pengujian otomatis. Pengaman ini tidak menggunakan toggle environment, entitlement, atau permission sebagai jalur aktivasi. Integrasi rollout Phase 08 memerlukan perubahan wiring yang direview dan bukti UAT pada target yang diotorisasi. Pemeriksaan ini memverifikasi source dan test composition.

| Exit gate Phase 08 | Bukti yang masih diperlukan |
|---|---|
| UAT Android/iOS | Install/update PWA, Web Crypto dan Web Locks, pairing dan sesi live, ketersediaan storage, quota dan eviction, restart dingin dan rollback jam. Uji pending, reconnect/ACK, revoke/suspend, shortage, rekonsiliasi Owner dan outlet switching pada perangkat target. |
| Benchmark PBKDF2 | Ukur 600.000 iterasi pada perangkat kasir target, termasuk perangkat terlemah, latensi unlock/lockout dan dampak pada UI. |
| Printer fisik | Verifikasi cetak struk online dan pending, status yang terlihat, ukuran kertas serta alur OS/browser/printer pada perangkat target. |
| Ingress, risiko residual dan rollout | Verifikasi OPEN-015, cookie/CORS/OIDC target, pembatasan expiry/revoke dan risiko kehilangan bukti sebelum upload. Uji rencana recovery dan pastikan konfigurasi aktif sesuai persetujuan pilot. |
