# Laporan Phase 06 · PWA dan Offline

**Keputusan Final Audit · PARTIAL / NO-GO untuk review merge Phase 06.** Fondasi PWA, kasir online dan pemulihan bukti checkout yang sudah commit dapat diuji. Penjualan offline yang terotorisasi, sinkronisasi transaksi offline, rekonsiliasi server, PIN offline dan perpindahan outlet belum memenuhi gate. Status ini mempertahankan scope P0 PRD.

## Baseline dan cara menjalankan

PR #15 terverifikasi merged ke `main` pada `d2cdf40eb64a6a105f920d9d4487ad2ff71c7296`. Branch `feat/phase-06-offline` dibuat dari SHA tersebut. Backend tetap Go 1.27.4 dan schema disposable 049. Migrasi tambahan tidak diperlukan untuk kontrak ACK. Compose development tetap dibatasi schema 028. PRD, konfigurasi deployment existing dan data persisten tidak diubah. [PR #15](https://github.com/PTKPP/hi-ve/pull/15)

Dari `apps/hi-ve-app`, jalankan `npm ci`, `npm run check`, `npm test`, dan `npm run build`. Keluaran `dist/` dapat disajikan sebagai aset statis. `wrangler.example.toml` menunjuk ke `dist/` tanpa Worker script. Untuk pengujian backend lintas origin, build dengan `HIVE_APP_API_ORIGIN` berisi origin API eksplisit. Origin App harus terdaftar pada konfigurasi CORS backend. App dan API perlu host HTTPS yang tetap same-site untuk cookie operator `Secure; HttpOnly; SameSite=Strict`. Default same-origin dipakai oleh fixture. Browser regression lokal memakai `npx --no-install playwright install chromium` dan `npm run test:browser`. Owner menyetujui pairing dari sesi terpisah melalui API existing, lalu perangkat memasukkan ID tenant dan outlet. Tidak ada kredensial Owner pada kasir.

## Increment dan traceability

| Increment | Implementasi dan hasil |
|---|---|
| 06.1 · CAP-50/51/54 | **PASS build dan browser uji.** Shell responsif, manifest dan ikon installability, service worker dengan precache shell berdasarkan hash, penggantian cache lama, dan IndexedDB v1. Aset API tidak dipersistenkan dalam Cache Storage. Instalasi perangkat target **NOT RUN**. |
| 06.2 · CAP-35/37/39/47/48/50 → FR-AUTH-02/04, FR-POS-01–03, FR-HV-DEV-001/OPR-001/RCP-001 | **PASS pada kontrak backend dan browser fixture untuk slice online.** Pairing P-256, login PIN online, outlet dari konteks server, pencarian produk/SKU/barcode, harga outlet, keranjang dan diskon per item, checkout, pencatatan konfirmasi pembayaran manual, histori keyset dan struk. Pemilihan batch dapat membaca saldo tersedia bila grant Inventory mengizinkan. Entitlement, permission dan batas outlet tetap diperiksa backend. |
| 06.3 · CAP-47/50 → FR-OFF-01, UAT-05 | **PARTIAL.** Antrean tunai persisten dengan UUID tetap, snapshot harga/versi/item/waktu/operator/outlet, serta status pending. Draf hanya dari keranjang yang sudah dimuat pada sesi berjalan dan selalu diberi label draf belum diotorisasi. Penyimpanan gagal memblokir konfirmasi. Nontunai dan mutasi administratif Inventory tidak tersedia offline. Otorisasi penjualan tunai offline dan pemulihan akses antrean tanpa koneksi **BLOCKED**. |
| 06.4 · CAP-41/47/50 → FR-OFF-02/03, UAT-06/07 | **PARTIAL.** Lookup ACK dilakukan saat reconnect atau manual, dengan prioritas untuk bukti yang belum atau paling lama diperiksa. ACK hanya untuk checkout tunai online yang sudah commit, dengan verifikasi device, aktor, tenant, outlet, UUID, hak terbaru dan fingerprint payload. Transisi `synced` hanya setelah ACK persisten cocok. Shortage, quote stale dan outcome yang tidak bisa dibuktikan masuk tinjauan lokal. Tidak ada repost otomatis, endpoint ingest offline, atau daftar rekonsiliasi Owner untuk transaksi offline. |
| 06.5 · CAP-38/39/41/57 → FR-HV-PIN-001, FR-HV-DEV-002, FR-HV-AUD-001 | **BLOCKED untuk otorisasi offline.** Online recovery memeriksa ulang perangkat, outlet dan sesi. Logout mengunci lokal, mempersistenkan target logout dengan revision dan meneruskan penutupan sesi asli ke backend ketika koneksi tersedia. Store login event terpisah dari Sales. Belum ada verifier offline, pemrosesan event login offline atau ACK Audit, outlet switching, atau mekanisme recovery yang menganggap data kosong aman. |
| 06.6 · CAP-50/52/53 → FR-HV-RCP-001, UAT-HV-03–08 | **PARTIAL.** Status koneksi browser, API, WebSocket dan antrean ditampilkan terpisah. Indikator WebSocket menunjukkan belum terhubung sampai transport Realtime Phase 07 tersedia. Status pending/synced/failed/needs_review dan snapshot browser printing teruji. Pencetakan fisik dan UAT lingkungan target **NOT RUN**. |

## Kontrak ACK dan bukti transaksi

`POST /api/v1/operator/tenants/{tenant_id}/devices/{device_id}/locations/{location_id}/sales/{id}/ack` memakai payload `CheckoutInput` asli, cookie operator, `Device-Proof` `sales.write` dan `Idempotency-Key`. Potongan konfirmasi hanya berisi ID, outlet, operator, status `cash_received` dan waktu posting. Payload berbeda ditolak dengan 409. Dokumen yang belum terbukti atau di luar scope tidak mengembalikan ACK. Bukti dapat dibaca ulang tanpa menulis order atau ledger. Status pembayaran manual hanya mencatat attestation operator.

Snapshot lokal menyimpan data jual minimum. PIN, verifier backend, cookie, HPP, margin dan field biaya tidak disalin. Private key P-256 non-extractable milik Devices tetap di store identitas tersendiri. Antrean Sales dipisahkan dari store login events. Antrean tidak dipangkas otomatis. Penyimpanan dan scope lokal bukan bukti otorisasi. Sumber `offline_draft` tidak dipakai oleh lookup ACK.

## Bukti pengujian

| Quality gate | Hasil aktual |
|---|---|
| Go 1.27.4 dan boundary | **PASS lokal.** `gofmt`, `go vet ./...`, `go test -race -count=1 ./...`, `go build ./...` dan `bash scripts/check-architecture.sh`. |
| PostgreSQL disposable | **PASS lokal.** `bash scripts/test-migrations.sh` mencakup fresh/retry/upgrade melalui 049, pengujian migrasi dan regresi OIDC, sesi, authorization, Inventory, Sales, concurrency dan rollback. `TestOperatorPINPostgres` juga mencakup proyeksi SKU dan ACK berulang, konflik payload, scope, dan penolakan ketika akses berubah. Rehearsal M5 dan preflight Compose 028 lulus. |
| Keamanan dependency dan build | **PASS lokal.** Govulncheck dibangun dengan Go 1.27.4 dan tidak menemukan kerentanan pada paket yang dipindai. `npm audit --audit-level=high` melaporkan 0 kerentanan. `npm run check`, dua tes Node dan static build lulus. |
| Browser | **PASS lokal, 15 Chromium fixture.** Meliputi ECDSA/Web Crypto, pairing dan kasir mobile, kehilangan respons sesudah commit simulasi, restart, ACK tanpa repost, draf offline, API gagal saat jaringan tersambung, logout offline, kuota, eviction terdeteksi, harga usang, shortage, revoke/expiry/suspend, konfirmasi manual, pemanggilan print dan cache tanpa API. Suite ini memakai fixture HTTP, sedangkan suite Go menjalankan handler dan PostgreSQL sungguhan. |
| Workflow dan UAT target | Bukti CI per revision dicatat di bawah. Browser target, OS printer, CORS/cookie pada host target dan UAT operator perangkat nyata **NOT RUN**. |

## Final Audit · exit gates

| Gate | Status dan batas bukti |
|---|---|
| Shell statis, versioned storage, API keluar dari service-worker cache | **PASS** untuk source dan browser fixture. Instalasi browser target masih **NOT RUN**. |
| Kasir online terikat device/tenant/outlet/operator dan permission/entitlement | **PASS** untuk scope yang diuji. Go dan browser memakai mekanisme proof dan jalur checkout existing. |
| Sales–Inventory atomik, idempotent dan tidak melakukan posting ganda | **PASS** untuk checkout online. ACK tidak dapat memicu posting. Tidak ada jalur posting offline. |
| UAT-05 · transaksi tunai offline sah dan pending bertahan setelah restart | **PARTIAL / gate belum lulus.** Durabilitas draf terbukti selama IndexedDB tersedia. Otorisasi dan akses ulang offline **BLOCKED**. |
| UAT-06 · ingest offline, deduplikasi dan pengakuan server | **PARTIAL / gate belum lulus.** Lookup pada checkout online yang sudah commit terbukti. Ingest transaksi yang terjadi offline **BLOCKED**. |
| UAT-07 · shortage offline, bukti dipertahankan dan tersedia untuk rekonsiliasi | **PARTIAL / gate belum lulus.** Bukti lokal dan alasan shortage pada percobaan online bertahan. Exception persisten dan reader Owner belum ada. |
| UAT-HV-04/05/06/08 · switch, PIN, audit offline, expiry dan revoke | **BLOCKED / gate belum lulus.** Fail-closed saat akses online ditolak. Perilaku offline yang memerlukan keputusan belum diaktifkan. |
| Browser target, struk fisik, UAT penuh | **NOT RUN / gate belum lulus.** Pengujian Chromium dan pemanggilan print tidak memvalidasi target perangkat. |

## Keputusan Product Owner dan risiko residual

| Keputusan | Opsi dan rekomendasi |
|---|---|
| **OPEN-001** · lease, reset/revoke, KDF verifier terpisah, lockout tahan bypass, browser target dan audit login | Opsi mempertahankan online-only atau pilot offline dengan lease bertanda tangan server yang terikat tenant, outlet, operator, perangkat dan revision. Rekomendasi mempertahankan mode saat ini sampai Security menyetujui TTL, parameter KDF berdasarkan perangkat terlemah, penanganan jam mundur, dan counter lintas tab/restart. Jika pilot disetujui, definisikan expiry, scope lease serta ingest event deduplicated oleh Audit dengan ACK persisten sebelum cleanup. Kandidat 72 jam PRD belum ditetapkan. |
| **OPEN-007** · outstanding saat tenant suspended atau device revoked | Opsi menolak posting dan simpan bukti untuk review, atau memberi recovery terotorisasi untuk dokumen yang terbukti dibuat sebelum cut-off. Rekomendasi karantina dan resolusi oleh aktor yang disetujui dengan alasan serta audit. Tetapkan siapa yang dapat meninjau, standar bukti, dan perlakuan finansial ketika pembeli telah membayar. |
| **OPEN-008** · expiry/grace, quote historis dan penanganan shortage | Opsi tanpa grace atau grace terbatas yang diotorisasi untuk transaksi dengan bukti valid. Rekomendasi menahan posting otomatis ketika hak atau quote tidak bisa dibuktikan, lalu menyimpan exception untuk keputusan review. Tetapkan kebijakan snapshot harga pada saat disconnected, waktu kejadian versus waktu terima, otoritas untuk menyelesaikan shortage, dan hasil ketika stok tetap kurang. |
| **OPEN-013** · outlet switching, perangkat hilang dan storage eviction | Opsi menolak switch hingga bukti terjamin, atau recovery manual dengan bukti, approval dan audit. Rekomendasi revision/epoch otoritatif dengan fence checkout dan bukti server yang dapat dikaitkan ke antrean, dilanjutkan prosedur review untuk outstanding atau data hilang. Klaim `pending_count=0` tidak cukup. Pemulihan tidak boleh memilih outlet baru sambil menghilangkan transaksi lama. |

Eviction seluruh origin dapat menghapus IndexedDB dan marker sekaligus. Permintaan storage persisten bergantung pada browser, dan antrean yang terus dipertahankan dapat memenuhi kuota. Pemulihan checkout yang sudah commit juga membutuhkan scope operator/perangkat yang masih sah. Pembayaran manual yang koneksinya terputus menggunakan UUID yang sama selama tab hidup dan perlu dicek di histori server setelah restart. OPEN-015 masih mencakup browser target dan ingress aktual. Distribusi Realtime mengikuti Phase 07. OPEN-002/014 tetap di luar scope.

## Bukti GitHub Actions

CI push dan pull request dijalankan dengan job Backend dan PWA pada HEAD Draft PR #16. [Draft PR #16](https://github.com/PTKPP/hi-ve/pull/16) · [Checks pada HEAD PR #16](https://github.com/PTKPP/hi-ve/pull/16/checks) · [GitHub Actions Phase 06](https://github.com/PTKPP/hi-ve/actions?query=branch%3Afeat%2Fphase-06-offline)

**Iterasi berikutnya** setelah keputusan Product Owner adalah lease/verifier dan Audit event, dilanjutkan penerimaan transaksi offline dengan exception persisten, recovery/switch, dan UAT perangkat target.
