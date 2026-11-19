# Hi-Ve · Execution tracker Phase 00–08

Checkpoint 19 November 2026, Asia/Jakarta. Commit baseline `853d6eff084adc8e42b7198a188e134f38da8220` di `main` dan `origin/main`. Foundation dikerjakan pada branch `feat/phase-01-foundation`. Commit foundation yang diuji adalah `56e5c30bf7fb36ed7a12f3fb328e9cf257ebc2ec`. [PR #1 · Ready for Review](https://github.com/PTKPP/hi-ve/pull/1)
Penomoran fase mengikuti urutan engineering dari audit sesuai arahan pengguna. PRD v04 menetapkan keputusan produk. Exit berlaku per fase, sedangkan kelulusan pilot menggunakan PRD §08.4. Checkpoint disimpan di working tree.

## Checkpoint aktif

**Phase 00 PASS · Phase 01 COMPLETED · Phase 02 BLOCKED pada entry · Phase 03–08 menunggu.** Seluruh gate Phase 01 lulus lokal dan di GitHub Actions. Review awal Phase 02 selesai dan persetujuan kontrak provisioning dicatat sebagai blocker. [Run Backend](https://github.com/PTKPP/hi-ve/actions/runs/44514611278) · [Issue #2](https://github.com/PTKPP/hi-ve/issues/2)

## Audit dan exit Phase 00

| Gate | Bukti pada baseline | Hasil |
|---|---|---|
| Inventaris source dan instruksi | `git ls-tree -r --name-only HEAD` berisi README, LICENSE, PRD, Functional Spec, instruksi backend dan Architecture. `git status --short --branch` bersih. Belum ada tracker, roadmap fase, source, manifest, kontrak API, migrasi, test, CI atau konfigurasi deploy. | PASS |
| Otoritas dan integritas | [PRD v04](PRD.md) §08.5/09, [Functional Spec v01](FUNCTIONAL_SPEC.md) §1/5/6/8/9, [AGENTS](backend/AGENTS.md), [Architecture Draft 02](backend/docs/ARCHITECTURE.md) §3–13 dibaca. Semua tautan file lokal pada empat dokumen dapat di-resolve. SHA-256 PRD dari `sha256sum PRD.md` cocok dengan Functional Spec §10, `3994d7592a1fdb5b3dcbf98e6f5b72991fe803fac76ee89cc04fde27af2b1ed9`. | PASS |
| Cakupan struktural | Pembacaan tabel PRD §07.7/09.1 dan Functional Spec §3/4/6 dengan Python memverifikasi kesamaan set 40 submodul, 12 domain dan CAP-01–58. Ada 36 FR baseline, 21 turunan, 257 ID AC unik, setiap FR memiliki AC dan rujukan dari registry. Terdapat 10 skenario UAT baseline dan 11 UAT-HV. | PASS untuk struktur. Review makna per AC tetap berjalan di OPEN-016. |
| Kelayakan increment pertama | `go version` memberi Go 1.26.5 linux/amd64. CAP-02/23 dan PRD §07.7 menetapkan lengkap ID registry. Increment bersifat read-only dalam proses Go, bebas database, kredensial, kontrak HTTP dan parameter bisnis OPEN. | PASS |
| Kontrol eksekusi | Dependensi, gate, keputusan, pemilik keputusan dan langkah berikut di bawah. Tiap fase perlu bukti exit pendahulunya dan entry pada barisnya. Saat diblokir, catat ID, dampak dan reviewer. | PASS |

## Urutan eksekusi

Gate di kolom exit adalah bukti yang harus tersedia pada akhir fase. Semua gate perilaku untuk fase yang belum dimulai berstatus **NOT RUN**. Tautan FR, AC dan UAT lengkap berada di Functional Spec §6–7.

| Phase / status | Lingkup dan dependency / entry | Exit yang diperiksa | Next action |
|---|---|---|---|
| 00 · PASS | Audit baseline, baca dokumen wajib, identifikasi supersession, desain urutan dan verifikasi prasyarat engineering | Lima gate audit di atas lulus | Entry 01 telah diverifikasi |
| 01 · COMPLETED | Fondasi Go, registry canonical, migrasi PostgreSQL, integration environment disposable, dan batas paket. Entry 00 PASS, toolchain tersedia, ID lengkap dan stabil (CAP-01–05/18/23/47) | Build dan regresi registry lulus, batas dependensi teruji, migrasi lulus pada database baru dan upgrade dari baseline termasuk retry, concurrency dan rollback. Workflow aktual lulus pada commit foundation | PR #1 siap direview. Merge menunggu otorisasi pengguna |
| 02 · BLOCKED | Platform, Identity, Devices, Settings, Audit online. Entry 01 PASS dan review FR/AC serta kontrak untuk slice provisioning (OPEN-011/016). OPEN per fitur tetap berlaku | PostgreSQL disposable, kompatibilitas migrasi, provisioning/linking Google, isolasi Admin/App dan tenant, hak per field, revoke, pairing, konteks outlet/operator, histori login 7 hari, audit. Buktikan lewat API dan DB termasuk kasus negatif | Review proposal Issue #2. Setelah merge Phase 01 diotorisasi dan kontrak serta AC slice disetujui, buat `feat/phase-02-platform` dari `main` terbaru |
| 03 · MENUNGGU 02 | Catalog, Locations, harga per outlet dan konteks stok. Entry 02 PASS dan kontrak SKU/unit/harga per tenant direview | Unik per tenant, SKU nonaktif tetap ada dalam histori, harga historis stabil, kg↔g dan L↔mL tepat, guard lokasi dan biaya diuji | Turunkan kasus FR-PRD-01–04 dan FR-LOC-01–02 ke fixture dua tenant |
| 04 · MENUNGGU 03 | Inventory online. Entry 03 PASS dan kontrak posting/approval yang dipakai disepakati | Ledger, saldo dan batch konsisten pada retry, rollback, posting bersamaan, transit/parsial, receiving, repacking, adjustment dan opname. Uji efek Inventory dari UAT-01–04/09 pada DB disposable | Rancang kontrak Inventory dan fixture stok terakhir. Review OPEN-003 dan detail packaging/costing yang terdampak |
| 05 · MENUNGGU 04 | Sales online, pembayaran manual, histori, retur/void, struk. Entry 04 PASS dan kontrak atomik Sales–Inventory tersedia | Checkout + posting stok dalam satu transaksi PostgreSQL, tidak ada double-sell, rollback utuh, retur/void terlacak, snapshot struk dan field biaya sesuai hak | Telusuri FR-POS/RET dan FR-HV-RCP-001, tes dua checkout berebut stok terakhir |
| 06 · MENUNGGU 05 | Hi-Ve App PWA, penjualan tunai offline dan antrean audit login. Entry 05 PASS dan kebijakan offline terkait direview | UAT-05–07, retry/restart, shortage, revoke/suspend/expiry, storage loss, hapus antrean hanya setelah ACK persistensi. Operasi inventori sensitif hanya online | Selesaikan OPEN-001/007/008/013 untuk skenario terdampak lalu uji di browser/perangkat |
| 07 · MENUNGGU 06 | Reports, Realtime dan integrasi tiga frontend. Entry 06 PASS, kontrak proyeksi dan payload berizin tersedia | Omzet UAT-10 benar, pending terpisah, event setelah commit, deduplikasi/reconnect, akses HPP diuji di API/report/export/cache/log/event. Build dan routing frontend static-only diverifikasi | Tetapkan dataset laporan dan observasi rollback/disconnect. Tahan hasil margin/valuasi sampai OPEN-002 selesai |
| 08 · MENUNGGU 07 | Acceptance pilot, reliability dan kesiapan rilis. Entry 07 PASS, target uji, perangkat pilot dan lingkungan tersedia | Semua AC P0 final dan skenario kritis lulus, UAT-01–10 serta UAT-HV terkait, restore terbukti, NFR-01–14/PM-01–06 diukur, temuan kritis ditutup. Rekam keputusan go/no-go | Jalankan matriks Functional Spec §9 dan PRD §08. Verifikasi konfigurasi Cloudflare/VPS aktual dan otorisasi target sebelum rilis |

## Keputusan dan blocker

| Keputusan / bukti | Status dan dampak |
|---|---|
| Satu backend Go, 12 domain, 40 submodul; registry, entitlement tenant, permission pengguna dan feature flag berbeda. Tiga kandidat CAP-06–08 tetap P1. | APPROVED, PRD §05/07.1/07.7. Untuk 01, Platform memelihara daftar ID bersama. Domain masing-masing memiliki operasi dan datanya. |
| PostgreSQL otoritatif dengan satu owner per dataset; Sales dan Inventory menulis secara atomik melalui kontrak domain. Migrasi berversi diuji pada database disposable. | Prinsip APPROVED dan guardrail engineering, Architecture §5/6/9.2. Tern v2.4.3 menjalankan migrasi SQL secara terpisah dari aplikasi. Engineering memiliki namespace teknis `hive` dan metadata versi Tern. Skema tabel bisnis dan driver aplikasi dinilai bersama slice pertama yang memerlukannya. [Database foundation](backend/docs/DATABASE.md) |
| Modul Go tunggal di backend, standard library untuk registry, paket ditambahkan ketika ada perilaku teruji. | Pilihan engineering 01. Registry memakai standard library. Penyimpanan privat domain memakai nested `internal/` saat dibutuhkan. Tern adalah dependensi tooling dan tidak ditautkan ke modul Go aplikasi. |
| OPEN-011/016 · kontrak dan review cakupan | Product Owner + Engineering/QA. Sebelum slice provisioning, validasi aktor platform, izin eksplisit, tenant sasaran, satu Primary Owner, kaitan identitas dan audit. Sepakati operasi request/result/error, skema milik domain, transaksi saat gagal/retry serta AC/test yang berlaku. |
| Temuan traceability · CAP-16 → FR-HV-IDN-001 | Crosswalk Functional Spec §6.2 mengaitkan verifikasi kontak WhatsApp ke FR login Google. AC FR itu mencakup linking/login/logout. Product Owner + Security perlu meninjau kaitan dan skenario verifikasi kontak di OPEN-009/016. Tidak menghambat registry. |
| OPEN-004/006/009 · identity dan audit | Product Owner + Security/Ops menetapkan bukti recovery/break-glass, retensi audit kritis, kebutuhan/provider verifikasi kontak. UAT-HV-11 BLOCKED oleh OPEN-004. Histori login operasional tujuh hari sudah disetujui. |
| OPEN-001/007/008/013 · offline dan entitlement | Product Owner bersama Security, Platform dan Operations menetapkan TTL/KDF offline, revoke/suspend/grace, serta pemulihan perangkat/data pending. 72 jam masih kandidat. Pertahankan bukti pending dan uji kebijakan sesudah diputuskan. |
| OPEN-002/003/014 · persediaan dan finansial | Product Owner bersama Inventory/Finance/Operations menetapkan metode HPP, approval posting dan perlakuan bahan penunjang. Field biaya Confidential dan pembatasan payload berlaku sejak awal. |
| OPEN-010/012 · komersial dan pengukuran | Product Owner + Commercial menangani paket/harga/kuota secara terpisah dari registry. Product Owner + QA/Ops menilai target numerik dengan data pilot. |
| OPEN-005 CLOSED, OPEN-015 masih OPEN | Landing di Pages, App dan Superadmin di Workers Static Assets. Engineering + Security/Ops masih perlu memvalidasi hostname, cookies, CSRF, OIDC redirects dan Tunnel pada target. |

## Bukti Phase 01

Gate lokal dijalankan pada Linux amd64, Go 1.26.5, dari `backend/`. Source baru berupa satu paket internal dan hanya mengimpor `slices` pada runtime. Cakupan validasi ini adalah identitas capability di registry. Validasi integrasi mencakup fondasi PostgreSQL. Entitlement tenant, aksi pengguna, kontrak HTTP dan penyimpanan data bisnis berada pada fase berikutnya.

| Gate | Pemeriksaan aktual | Hasil |
|---|---|---|
| Kontrak registry | `go test -v -count=1 ./internal/platform` menjalankan tiga [tes registry](backend/internal/platform/capabilities_test.go). Set ID sama persis dengan PRD §07.7, lookup mengenali seluruh 40 ID, menolak kandidat P1/alias/ID asing dan salinan daftar aman diubah pemanggil. | PASS · 3/3 |
| Regresi negatif | Di salinan sementara, ganti `sales.cashier` dengan `sales.cash_sessions` lalu jalankan `go test -count=1 ./...`. Jumlah ID tetap 40. Tes kesesuaian PRD dan penolakan kandidat P1 keduanya gagal sesuai ekspektasi. | PASS |
| Toolchain | `gofmt -l .` bersih, `go vet ./...`, `go test -race -count=1 ./...` dan `go build ./...` sukses. `go list` mengonfirmasi satu paket produksi. | PASS lokal |
| Boundary | `bash scripts/check-architecture.sh` memeriksa dependensi produksi Platform yang sekarang hanya standard library. Pada salinan sementara, import lintas-domain dari registry ditolak oleh guard, dan akses paket privat domain lain ditolak oleh Go. Build Go memeriksa siklus. | PASS |
| PostgreSQL | `bash scripts/test-migrations.sh` memakai Docker lokal, PostgreSQL 18.7 yang dipin oleh digest dan Tern v2.4.3. Versi 001 terpasang pada DB kosong dan retry tidak mengubah versi. Dua proses migrasi menerapkan upgrade aditif dari 001 pada fixture, mempertahankan data lama, dan mencapai satu versi akhir. Migrasi yang sengaja gagal membatalkan DDL dan mempertahankan versi. | PASS lokal |
| Workflow | `actionlint` v1.7.7 lulus. Semua langkah verifikasi dijalankan lokal. Run push `44514611278` mengembalikan `completed/success` pada SHA `56e5c30bf7fb36ed7a12f3fb328e9cf257ebc2ec`, termasuk langkah Test PostgreSQL migrations. | PASS lint, lokal dan GitHub Actions |
| Exit | Semua gate lokal lulus, commit foundation tersedia di feature branch, dan run pada SHA tersebut sukses. | PASS |

### Review PR #1 terhadap main

- `feat/phase-01-foundation` pada `852225d24ad689429002e5da698922cf89dd8219` direview terhadap `main` pada `853d6eff084adc8e42b7198a188e134f38da8220`. Registry dan tes sesuai CAP P0, guard mencakup paket produksi yang tersedia, migrasi 001 bersifat aditif dan tes PostgreSQL menggunakan target disposable. Workflow membatasi akses token ke baca dan mem-pin Actions serta image PostgreSQL. Review tidak menemukan regresi yang memerlukan perubahan implementasi.
- GitHub menampilkan dua check `Backend / verify` sukses pada SHA yang direview. `gh pr checks --required` dan API aturan branch tidak menampilkan required status check tambahan. PR #1 diubah menjadi Ready for Review. Merge menunggu otorisasi. [CI PR](https://github.com/PTKPP/hi-ve/actions/runs/44514921500) · [CI push](https://github.com/PTKPP/hi-ve/actions/runs/44514910959)

## Review entry Phase 02

| Area | Baseline dan hasil review |
|---|---|
| Aturan approved | CAP-14/15/22/24/28/29/33/40 menetapkan provisioning oleh Superadmin, sesi Platform dan tenant terpisah, tepat satu Primary Owner aktif per tenant, login Google langsung dan lifecycle tenant. Platform memiliki workflow tenancy, Identity memiliki linking dan sesi, Audit memiliki rekaman audit. |
| OPEN-011 · kontrak | Tetapkan izin aksi dan bentuk request/result/error provisioning. Review state awal tenant dan Owner, bukti untuk mengaitkan Owner ke Google issuer/subject, penanganan identitas duplikat, serta atomicity dan retry tenant, Owner dan audit. Keputusan ini menentukan API dan constraint skema. |
| OPEN-016 · acceptance | Review FR-HV-PLT-001/AC-01–02, FR-AUTH-01/AC-04–05, dan FR-HV-IDN-001/AC-01–03. UAT-HV-01 mencakup penolakan login dan isolasi konteks. Usulan tambahan menguji provisioning sukses, aktor tanpa izin, duplikasi/retry dan rollback tanpa tenant/Owner setengah jadi. |
| Dampak dan rekomendasi | Prioritaskan slice tenant dan Primary Owner. Implementasi endpoint dan migrasi tabel bisnis menunggu keputusan slice tersebut. Tangani OPEN-004 untuk recovery, OPEN-007/008 untuk transaksi offline, dan OPEN-015 untuk OIDC di lingkungan target pada lingkup masing-masing. [Issue #2](https://github.com/PTKPP/hi-ve/issues/2) |

**Decision matrix siap disetujui.** Issue #2 menjadi rujukan untuk D1 (permission dan sesi), D2 (pending dan kebijakan duplikasi), D3 (bukti OIDC, verifikasi independen dan masa berlaku), D4 (kontrak, constraint, idempotensi, transaksi dan audit), serta D5 (acceptance). OPEN-011/016 tetap OPEN. Setelah persetujuan eksplisit atas slice dan merge PR #1 yang diotorisasi, buat `feat/phase-02-platform` dari `main` terbaru. [Decision matrix Issue #2](https://github.com/PTKPP/hi-ve/issues/2#issuecomment-7008116637)

## Riwayat checkpoint

| ID | Hasil dan kelanjutan |
|---|---|
| 00.1 | Baseline diinventarisasi. Validasi dokumen dan kesesuaian registry PASS. Seluruh test perilaku produk, migrasi, UAT, NFR dan deployment masih NOT VERIFIED. |
| 00.2 | Exit 00 PASS. Entry 01 PASS untuk registry dan fondasi Go. |
| 01.1 | Modul Go dan registry P0 diimplementasikan. Perbandingan dengan PRD dan tes negatif PASS. |
| 01.2 | Gate lokal dan uji mutasi pada salinan disposable PASS. Workflow tersimpan. Untuk melanjutkan, verifikasi hasil CI pada SHA yang dikirim, lalu periksa entry provisioning. UAT dan kesiapan pilot masih NOT VERIFIED. |
| 01.3 | Audit ulang exit gate menambahkan PostgreSQL disposable, baseline 001 dan guard architecture. Go, actionlint, pengujian boundary negatif, fresh apply, retry, upgrade konkuren, dan rollback DDL PASS. GitHub Actions menunggu SHA dan run aktual yang memuat foundation. |
| 01.4 | Foundation dipush ke `feat/phase-01-foundation` pada `56e5c30bf7fb36ed7a12f3fb328e9cf257ebc2ec`. Run `44514611278` PASS. Draft PR #1 terbuka. Phase 01 COMPLETED. |
| 02.entry | Review kontrak dan AC awal selesai. Issue #2 melacak keputusan yang memblokir implementasi provisioning. |
| 01.5 | Review PR #1 dan CI pada SHA `852225d24ad689429002e5da698922cf89dd8219` PASS. PR Ready for Review. |
| 02.proposal | Proposal kontrak dan AC provisioning dipublikasikan pada Issue #2. Entry masih BLOCKED oleh approval slice dan merge Phase 01. |
| 02.decision | Review provisioning terhadap PRD, Functional Spec, Architecture dan AGENTS selesai. Decision matrix D1–D5 dipublikasikan di komentar Issue #2 yang sama. Hasil review Ready for Review pada PR #1 tetap berlaku. Implementasi dan DDL menunggu persetujuan slice serta merge yang diotorisasi. |
