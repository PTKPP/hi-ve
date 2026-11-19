# FUNCTIONAL_SPEC — Hi-Ve Multi-Product SaaS

> **Versi dokumen:** FS v01 · **Tanggal:** 18 November 2026 (Asia/Jakarta)  
> **Status:** **Derived specification / Review Required**. Requirements pada level produk yang berasal dari `PRD.md` telah disetujui; **AC turunan baru dan parameter teknis belum direview individual**. Dokumen ini tidak berarti implementasi sudah dilakukan atau pengujian lulus.  
> **Sumber otoritatif aktif:** [`PRD.md`](PRD.md) (produk/keputusan) dan `FUNCTIONAL_SPEC.md` ini (FR/AC). `PRDv1.0.md` (22 Oktober 2026) adalah **provenance historis, tidak wajib ada di repository**; 36 FR+AC-nya sudah dikonsolidasikan di sini.  
> **Ruang lingkup:** Semua **36 FR baseline P0** berikut AC yang berlaku, ditambah **21 FR turunan P0** berdasarkan CAP-01–CAP-58. Tidak memasukkan kandidat P1 sebagai P0.  
> **Riwayat pembaruan:** FS v01 (18 November 2026); editorial alignment 19 November 2026: referensi aktif, status dokumen, dan traceability UAT. **Tidak ada penambahan/perubahan aturan bisnis, FR, atau AC.**

## 1. Panduan membaca dan otoritas keputusan

1. Untuk *what/why*, product boundaries, scope, dan keputusan bisnis, `PRD.md` adalah otoritas utama. Persyaratan di sini menjabarkan *observable behavior* dan hasil verifikasinya.
2. FR baseline (`FR-AUTH-*` dst.) mempertahankan ID asli dan teks dasar; AC yang ditimpa keputusan terbaru diganti eksplisit serta ditandai **Revised**. AC tambahan diberi nomor lanjutan.
3. Semua **AC baseline yang tidak bertentangan** dipertahankan. AC baru diturunkan dari CAP/Topik yang disetujui pada tingkat requirement, tetapi pengesahan detailnya tetap **Review Required**.
4. Tanda **OPEN-###** menyatakan kebijakan yang belum diputuskan. Agent tidak boleh memilih TTL, metode costing, approval authority, fitur billing, rate limit kriptografi, atau kontrak endpoint sendiri.
5. UAT/TEST-ID adalah **rencana verifikasi**, bukan bukti PASS. Semua perilaku P0 harus berstatus teruji dan lolos sebelum dinyatakan *Implementation Ready*; kategori NFR terpisah di PRD Bagian 08.
6. Sebelum implementasi, validasi keselarasan dengan source terkini; desain API/tabel/permission code yang belum final bukan janji dari dokumen ini. Dokumen historis `PRDv1.0.md` **tidak perlu tersedia di folder proyek aktif** agar agent bisa membaca FR/AC yang telah dikonsolidasikan. Rujukan nama/ID historis berikut hanya jejak asal, bukan tautan file wajib.

### 1.1 Status requirement dan penilaian AC

| Status | Makna |
|---|---|
| **Approved baseline** | FR/AC asli `PRDv1.0.md` yang konsisten dengan keputusan terbaru |
| **Revised by PRD v04** | AC baseline berubah karena keputusan explicit pada `PRD.md` |
| **Derived P0 — Review Required** | Requirement baru menurunkan CAP yang Approved pada tingkat konsep; redaksi AC/parameter belum di-approve satu per satu |
| **Blocked Parameter** | Implementasi/acceptance tidak boleh dianggap lengkap sebelum OPEN-ID terkait selesai |
| **P1 Candidate** | Di luar scope P0; tidak masuk ke FR P0 tanpa keputusan baru |

### 1.2 Bentuk identitas dan skenario

- FR asli menggunakan ID stabil, misalnya `FR-REP-02`; AC lokal diberi ID global `FR-REP-02/AC-01`.
- FR tambahan menggunakan `FR-HV-<AREA>-NNN`, misalnya `FR-HV-DEV-001`, agar tidak menimpa identitas baseline.
- Rencana test minimum diberi ID `TEST/<FR-ID>/<AC-ID>` untuk setiap AC. ID ini **planned**, belum ada implementasi test.
- Kaitan end-to-end memakai `UAT-01`–`UAT-10` dari baseline serta rencana `UAT-HV-01`–`UAT-HV-11` pada Bagian 7; `UAT-HV-11` khusus recovery/transfer Owner **BLOCKED oleh `OPEN-004`**.

## 2. Aturan lintas domain yang wajib berlaku

| Prinsip | Aturan yang tidak boleh dilanggar | Sumber |
|---|---|---|
| Tenant isolation | Akses lintas tenant ditolak pada backend; identitas platform dan tenant terpisah | CAP-14, CAP-24, CAP-26 |
| Entitlement / RBAC | Entitlement capability ≠ izin operator; akses memerlukan session, permission, scope lokasi, device bila relevan | CAP-04, CAP-05, CAP-48 |
| Domain ownership | Satu owner untuk ledger, batch, dan transaksi; report tidak memposting pergerakan sendiri | CAP-01–03, CAP-09–11 |
| Conversion | Konversi standar 1 kg=1.000 g dan 1 L=1.000 mL; tidak ada kg↔mL otomatis pada V1 | CAP-49 |
| Offline-first terbatas | Penjualan tunai inti boleh offline dengan izin lokal; inventory posting lainnya online | CAP-35–39, CAP-49 |
| Device + operator + outlet | Satu tenant per device, banyak outlet diizinkan, satu outlet aktif per sesi, operator punya PIN sendiri | CAP-35–39 |
| Audit login | Server PostgreSQL 7 hari; IndexedDB hanya queue sementara yang dihapus setelah ACK | CAP-38 |
| Sensitive fields | HPP/harga beli/margin/valuasi tidak dibocorkan lewat API, report, export, websocket, error atau offline cache bagi aktor tanpa izin | CAP-55–58 |
| No special HPP encryption | Tidak ada column-level/application-level HPP encryption V1; kontrol DB, backup, secret tetap wajib | PRD 07.12A |
| Cloudflare static frontend | Landing Pages, App/Superadmin Workers Static Assets; Go API tetap di VPS; tak ada Worker script/Pages Functions jalur bisnis utama | CAP-51/54 (NFR/Deployment) |

## 3. Functional Requirements Baseline P0 (36 FR)

> **Provenance:** requirement dan AC pada bagian ini berasal dari arsip historis `PRDv1.0.md` yang **tidak wajib tersedia di repository**. Teks asli yang masih berlaku sudah tertanam di bagian ini; detail override diberi penanda. Supersession lengkap di Bagian 5. Semua label **`historical baseline: FR-* (PRDv1.0.md)`** di bawah adalah jejak asal, bukan path yang harus dapat dibuka agent.

### FR-AUTH-01 — Identitas tenant

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `platform.tenancy`, `identity.authentication`  
**Actor:** Superadmin/Owner/staf sesuai konteks · **Authorization scope:** tenancy + session; online activation  
**Reference:** `historical baseline: FR-AUTH-01 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-08; UAT-HV-01  

**Requirement:** Owner mempunyai akun utama yang menghubungkan pengguna dan outlet dalam satu tenant. Aktivasi tenant dan perangkat dilakukan online.

**Acceptance criteria:**
- **FR-AUTH-01/AC-01** — Login akun owner yang valid membuka tenant miliknya.
- **FR-AUTH-01/AC-02** — Pengguna dari tenant lain tidak dapat membaca maupun mengubah data melalui API.
- **FR-AUTH-01/AC-03** — Tidak ada akses operasional offline pertama kali sebelum perangkat diaktivasi.
- **FR-AUTH-01/AC-04** — Owner/Superadmin login online melalui **Google OIDC langsung**; pengguna yang tidak diprovision dalam konteks Hi-Ve yang sesuai tidak memperoleh sesi operasional. **[Added from PRD v04 — Review Required]**
- **FR-AUTH-01/AC-05** — Sesi Platform Admin dan Tenant tidak saling dapat digunakan; status tenant/user dan tenant scoping tetap ditegakkan backend. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-AUTH-01/AC-*` (belum dijalankan); UAT-08; UAT-HV-01.

### FR-AUTH-02 — Aktivasi perangkat dan operator

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `devices.management`, `devices.operators`  
**Actor:** Owner/pengguna berizin; operator · **Authorization scope:** devices.management + devices.operators; device/outlet  
**Reference:** `historical baseline: FR-AUTH-02 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-05; UAT-HV-03  

**Requirement:** Owner dapat mendaftarkan perangkat kasir serta operator yang boleh menggunakannya. Pergantian operator dilakukan melalui pemilihan profil dan PIN individual.

**Acceptance criteria:**
- **FR-AUTH-02/AC-01** — Perangkat baru memerlukan autentikasi online dari pengguna berizin sebelum digunakan.
- **FR-AUTH-02/AC-02** — Operator A dan B dapat bergantian pada perangkat yang sama tanpa berbagi PIN.
- **FR-AUTH-02/AC-03** — Setiap transaksi menyimpan operator sebenarnya, bukan sekadar pemilik perangkat.
- **FR-AUTH-02/AC-04** — Saat pergantian operator, hak akses operator sebelumnya tidak terbawa.
- **FR-AUTH-02/AC-05** — Pairing code sekali pakai dikeluarkan ketika device online, disetujui Owner berizin pada perangkat Owner sendiri, dan ditolak jika kedaluwarsa, sudah dipakai, atau bukan tenant yang sama. **[Added from PRD v04 — Review Required]**
- **FR-AUTH-02/AC-06** — Device dapat diassign beberapa outlet tetapi hanya satu outlet aktif; profil operator yang terlihat mengikuti assignment device+outlet dan permission pengguna. **[Added from PRD v04 — Review Required]**
- **FR-AUTH-02/AC-07** — Sesi Google Owner tidak ditransfer atau disimpan pada device bersama. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-AUTH-02/AC-*` (belum dijalankan); UAT-05; UAT-HV-03.

### FR-AUTH-03 — Pengelolaan pengguna dan peran

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `identity.users`, `identity.permissions`  
**Actor:** Owner/pengelola berizin · **Authorization scope:** identity.users + identity.permissions; tenant/location  
**Reference:** `historical baseline: FR-AUTH-03 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-08; UAT-HV-04  

**Requirement:** Owner dapat membuat, menonaktifkan, mereset kredensial, dan memberikan akses lokasi kepada karyawan. Role default: Owner, Kepala Outlet, Kasir, dan Petugas Gudang.

**Acceptance criteria:**
- **FR-AUTH-03/AC-01** — Owner dapat memberikan akses satu atau beberapa outlet kepada seorang pengguna.
- **FR-AUTH-03/AC-02** — Kasir tanpa izin laporan global tidak dapat membuka laporan global melalui UI maupun API.
- **FR-AUTH-03/AC-03** — Pengguna yang dinonaktifkan kehilangan akses online sejak perubahan berlaku di server.
- **FR-AUTH-03/AC-04** — Semua perubahan role, permission, dan status akun masuk audit log.
- **FR-AUTH-03/AC-05** — Owner menentukan PIN enam digit awal setiap user pada V1; PIN tidak ditampilkan kembali dan reset dicatat audit. **[Added from PRD v04 — Review Required]**
- **FR-AUTH-03/AC-06** — Akses user yang dinonaktifkan atau ditarik haknya ditolak untuk operasi online baru; bila perangkat masih offline, bukti transaksi tertunda dipertahankan untuk rekonsiliasi saat reconnect. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-AUTH-03/AC-*` (belum dijalankan); UAT-08; UAT-HV-04.

### FR-AUTH-04 — Sesi offline aman

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `identity.offline_access`, `devices.management`  
**Actor:** Operator aktif pada device teraktivasi · **Authorization scope:** identity.offline_access; device/outlet + TTL  
**Reference:** `historical baseline: FR-AUTH-04 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-05; UAT-HV-05  

**Requirement:** Perangkat teraktivasi menyimpan informasi operator dan izin terbatas yang dibutuhkan untuk transaksi offline.

**Acceptance criteria:**
- **FR-AUTH-04/AC-01** — Operator yang telah diaktivasi dapat membuka kasir offline menggunakan PIN jika masa otorisasi lokal masih berlaku.
- **FR-AUTH-04/AC-02** — Kesalahan PIN berulang memicu pembatasan percobaan.
- **FR-AUTH-04/AC-03** — Operator yang belum pernah diaktivasi pada perangkat tidak dapat mengakses offline.
- **FR-AUTH-04/AC-04** — Saat koneksi kembali, otorisasi disegarkan sebelum operasi online sensitif diizinkan.
- **FR-AUTH-04/AC-05** — Transaksi yang telanjur tercatat secara sah saat offline tetap dipertahankan untuk penanganan terkontrol bila otorisasi ternyata dicabut selama perangkat terputus.
- **FR-AUTH-04/AC-06** — Verifier offline **terpisah** dari verifier backend; PIN plaintext tidak disimpan pada cache/log/database. **[Added from PRD v04 — Review Required]**
- **FR-AUTH-04/AC-07** — Lima kegagalan PIN berturut-turut menyebabkan lockout 15 menit menurut kebijakan operasional awal; batas keamanan terhadap perangkat yang dikuasai penyerang tidak dijamin. **[Added from PRD v04 — Review Required]**
- **FR-AUTH-04/AC-08** — Batas waktu otorisasi offline harus menggunakan parameter kebijakan yang telah disetujui; **72 jam bukan nilai final** (OPEN-001), sehingga pengujian expiry final berstatus blocked hingga keputusan. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-AUTH-04/AC-*` (belum dijalankan); UAT-05; UAT-HV-05.

**Blocked:** `OPEN-001` menetapkan TTL/kebijakan verifier offline. Kandidat 72 jam **bukan nilai wajib**.

### FR-PRD-01 — Pengelolaan katalog

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `catalog.products`  
**Actor:** Owner/staf katalog berizin · **Authorization scope:** catalog.products; tenant  
**Reference:** `historical baseline: FR-PRD-01 (PRDv1.0.md)` · **UAT:** UAT-01  

**Requirement:** Owner atau pengguna berizin dapat membuat produk induk, SKU, barcode, kategori, satuan jual, harga, dan status aktif/nonaktif.

**Acceptance criteria:**
- **FR-PRD-01/AC-01** — Satu produk induk dapat mempunyai beberapa SKU, misalnya Madu Randu Curah, Randu 250 ml dan Randu 500 ml.
- **FR-PRD-01/AC-02** — Barcode tidak boleh duplikat dalam satu tenant bila digunakan sebagai identitas unik SKU.
- **FR-PRD-01/AC-03** — SKU nonaktif tidak dapat ditambahkan ke transaksi baru tetapi tetap tersedia dalam histori.
- **FR-PRD-01/AC-04** — Harga pada transaksi lama tidak berubah ketika harga master diubah.

**Planned verification:** `TEST/FR-PRD-01/AC-*` (belum dijalankan); UAT-01.

### FR-PRD-02 — Satuan dan konversi

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `catalog.units`  
**Actor:** Pengelola katalog/repacking berizin · **Authorization scope:** catalog.units; tenant/SKU  
**Reference:** `historical baseline: FR-PRD-02 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-01; UAT-04  

**Requirement:** Mendukung gram, kg, ml, liter, dan pcs sesuai definisi SKU.

**Acceptance criteria:**
- **FR-PRD-02/AC-01** — Konversi dalam dimensi massa menghasilkan nilai tepat, misalnya **1 kg = 1.000 g**, termasuk arah sebaliknya. **[Revised v04]**
- **FR-PRD-02/AC-02** — Konversi dalam dimensi volume menghasilkan nilai tepat, misalnya **1 liter = 1.000 mL**, termasuk arah sebaliknya. **[Revised v04]**
- **FR-PRD-02/AC-03** — Sistem **tidak menghitung otomatis** konversi lintas dimensi massa↔volume (kg↔mL, g↔L); pada repacking, kuantitas bahan yang digunakan dan hasil aktual dicatat sebagai pengukuran terpisah. **[Revised v04]**
- **FR-PRD-02/AC-04** — Kuantitas dan pengurangan stok menggunakan representasi numerik presisi, bukan floating-point yang menimbulkan selisih pembulatan. **[Revised v04]**

**Planned verification:** `TEST/FR-PRD-02/AC-*` (belum dijalankan); UAT-01; UAT-04.

**Supersession:** AC-02/03 dari baseline tentang faktor massa→volume dan penjualan curah berbasis volume otomatis **tidak dipakai** sebagai P0. Operator tetap mencatat bahan aktual dan hasil aktual pada repacking; belum ada engine konversi densitas.

### FR-PRD-03 — Harga per outlet dan histori harga

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `catalog.pricing`  
**Actor:** Pengelola pricing/kasir berizin · **Authorization scope:** catalog.pricing; tenant/outlet  
**Reference:** `historical baseline: FR-PRD-03 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-05; UAT-HV-09  

**Requirement:** Harga dasar dapat ditetapkan pada tingkat tenant dan dapat dioverride untuk outlet tertentu.

**Acceptance criteria:**
- **FR-PRD-03/AC-01** — SKU yang sama dapat memiliki harga berbeda di Outlet 1 dan Outlet 2.
- **FR-PRD-03/AC-02** — Kasir hanya melihat harga yang berlaku pada outletnya.
- **FR-PRD-03/AC-03** — Transaksi menyimpan snapshot harga dan diskon.
- **FR-PRD-03/AC-04** — Katalog offline menggunakan harga terakhir yang tersinkron, dengan versi dan waktu pembaruannya tercatat.
- **FR-PRD-03/AC-05** — Perangkat tanpa hak biaya tidak menerima HPP, margin, harga beli atau valuasi dalam payload harga offline dan respons kasir. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-PRD-03/AC-*` (belum dijalankan); UAT-05; UAT-HV-09.

### FR-PRD-04 — Batch dan ketertelusuran

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `catalog.products`, `inventory.traceability`  
**Actor:** Operator inventori berizin · **Authorization scope:** inventory.traceability; tenant/location/batch  
**Reference:** `historical baseline: FR-PRD-04 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-01; UAT-04  

**Requirement:** Produk madu curah serta hasil repacking dapat dilacak menggunakan batch.

**Acceptance criteria:**
- **FR-PRD-04/AC-01** — Batch masuk menyimpan identitas unik, produk, tanggal penerimaan, dan informasi asal.
- **FR-PRD-04/AC-02** — Batch hasil repacking mempunyai referensi ke batch bahan sumber.
- **FR-PRD-04/AC-03** — Operator dapat melihat histori lokasi, transfer, repacking, dan penjualan terkait batch.
- **FR-PRD-04/AC-04** — Batch nonlayak jual tidak dapat digunakan dalam operasi inventori normal.
- **FR-PRD-04/AC-05** — Pemetaan lot operasional dan lineage batch milik `inventory.traceability`; konfigurasi SKU tetap di `catalog.products`. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-PRD-04/AC-*` (belum dijalankan); UAT-01; UAT-04.

### FR-LOC-01 — Lokasi gudang dan outlet

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `locations.management`  
**Actor:** Owner/staf lokasi berizin · **Authorization scope:** locations.management; tenant  
**Reference:** `historical baseline: FR-LOC-01 (PRDv1.0.md)` · **UAT:** UAT-01; UAT-04  

**Requirement:** Sistem mendukung lokasi inventori independen dari outlet penjualan.

**Acceptance criteria:**
- **FR-LOC-01/AC-01** — Owner membuat Gudang Pusat dan tiga outlet dengan saldo terpisah.
- **FR-LOC-01/AC-02** — Gudang tidak tampil sebagai pilihan lokasi kasir.
- **FR-LOC-01/AC-03** — Satu outlet memiliki lokasi inventori utama yang digunakan kasir.
- **FR-LOC-01/AC-04** — Lokasi yang sudah mempunyai histori tidak dapat dihapus secara permanen.

**Planned verification:** `TEST/FR-LOC-01/AC-*` (belum dijalankan); UAT-01; UAT-04.

### FR-LOC-02 — Pengaturan akses dan rute

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `locations.management`, `identity.permissions`  
**Actor:** Staf operasional berizin · **Authorization scope:** identity.permissions; lokasi  
**Reference:** `historical baseline: FR-LOC-02 (PRDv1.0.md)` · **UAT:** UAT-08  

**Requirement:** Hak melakukan penerimaan, pengemasan, pengiriman, atau koreksi dibatasi per lokasi.

**Acceptance criteria:**
- **FR-LOC-02/AC-01** — Pengguna Outlet 2 tanpa izin Gudang Pusat tidak dapat membuat pergerakan yang tidak diizinkan dari gudang.
- **FR-LOC-02/AC-02** — Pengguna berizin dapat memindahkan stok Gudang → Outlet 1/2/3.
- **FR-LOC-02/AC-03** — Transfer antar-outlet hanya tersedia untuk operator dengan permission yang sesuai.

**Planned verification:** `TEST/FR-LOC-02/AC-*` (belum dijalankan); UAT-08.

### FR-STK-01 — Stock ledger

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.ledger`  
**Actor:** Sales/Inventory system posting berizin · **Authorization scope:** inventory.ledger; tenant/location  
**Reference:** `historical baseline: FR-STK-01 (PRDv1.0.md)` · **UAT:** UAT-01; UAT-02; UAT-06  

**Requirement:** Setiap perubahan stok membuat pergerakan dengan referensi dokumen sumber.

**Acceptance criteria:**
- **FR-STK-01/AC-01** — Penjualan 2 botol menghasilkan pengurangan 2 botol pada lokasi penjualan.
- **FR-STK-01/AC-02** — Histori mencatat waktu, operator, SKU, batch, lokasi, kuantitas, tipe aktivitas, dan referensi dokumen.
- **FR-STK-01/AC-03** — Pergerakan yang diposting tidak bisa diubah langsung.
- **FR-STK-01/AC-04** — Perhitungan ulang ledger cocok dengan saldo tersimpan setelah seluruh transaksi terkonfirmasi diproses.

**Planned verification:** `TEST/FR-STK-01/AC-*` (belum dijalankan); UAT-01; UAT-02; UAT-06.

### FR-STK-02 — Saldo tersedia

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.balance`  
**Actor:** Operator inventori/kasir · **Authorization scope:** inventory.balance; location/SKU/batch  
**Reference:** `historical baseline: FR-STK-02 (PRDv1.0.md)` · **UAT:** UAT-02; UAT-07  

**Requirement:** Sistem menyediakan saldo stok untuk penjualan, stok tidak layak jual, stok transit, dan selisih yang membutuhkan rekonsiliasi.

**Acceptance criteria:**
- **FR-STK-02/AC-01** — Produk di gudang tidak otomatis ditambahkan ke saldo kasir Outlet 1.
- **FR-STK-02/AC-02** — Produk dalam perjalanan tidak dapat dijual oleh outlet pengirim maupun penerima.
- **FR-STK-02/AC-03** — Sistem menolak pengurangan stok online biasa yang melebihi stok tersedia.
- **FR-STK-02/AC-04** — Penjualan offline yang menimbulkan kekurangan tidak dihapus; kekurangan ditampilkan sebagai exception untuk ditangani.

**Planned verification:** `TEST/FR-STK-02/AC-*` (belum dijalankan); UAT-02; UAT-07.

### FR-STK-03 — Konsistensi dan konkurensi

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.ledger`, `inventory.balance`  
**Actor:** Backend posting services · **Authorization scope:** inventory.ledger; tenant/document/operation ID  
**Reference:** `historical baseline: FR-STK-03 (PRDv1.0.md)` · **UAT:** UAT-06; UAT-07  

**Requirement:** Semua operasi stok online memakai transaksi database dengan perlindungan konkurensi dan idempotency.

**Acceptance criteria:**
- **FR-STK-03/AC-01** — Dua operasi yang bersamaan tidak dapat mengonsumsi unit stok terakhir secara ganda ketika online.
- **FR-STK-03/AC-02** — Kegagalan parsial tidak menghasilkan perubahan saldo sebelah saja.
- **FR-STK-03/AC-03** — Permintaan yang dikirim ulang dengan operation ID sama tidak membuat pergerakan tambahan.
- **FR-STK-03/AC-04** — Stok yang tersimpan selalu dapat ditelusuri kembali ke dokumen sumber.

**Planned verification:** `TEST/FR-STK-03/AC-*` (belum dijalankan); UAT-06; UAT-07.

### FR-RCV-01 — Penerimaan barang

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.receiving`  
**Actor:** Petugas gudang/outlet berizin · **Authorization scope:** inventory.receiving; tenant/location  
**Reference:** `historical baseline: FR-RCV-01 (PRDv1.0.md)` · **UAT:** UAT-01  

**Requirement:** Pengguna berizin dapat mencatat stok masuk dari pemasok atau sebagai saldo awal.

**Acceptance criteria:**
- **FR-RCV-01/AC-01** — Form mencatat lokasi, SKU, jumlah, satuan, batch, tanggal, asal barang, dan harga modal dasar bila tersedia.
- **FR-RCV-01/AC-02** — Stok belum bertambah ketika dokumen masih draft.
- **FR-RCV-01/AC-03** — Ketika dokumen diposting, saldo dan ledger bertambah satu kali.
- **FR-RCV-01/AC-04** — Jumlah nol/negatif dan data batch wajib yang tidak lengkap ditolak.
- **FR-RCV-01/AC-05** — Kesalahan dokumen yang sudah diposting diselesaikan melalui dokumen koreksi, bukan pengubahan langsung.

**Planned verification:** `TEST/FR-RCV-01/AC-*` (belum dijalankan); UAT-01.

### FR-REP-01 — Membuat pengemasan

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.repacking`  
**Actor:** Petugas gudang/outlet berizin · **Authorization scope:** inventory.repacking; tenant/location  
**Reference:** `historical baseline: FR-REP-01 (PRDv1.0.md)` · **UAT:** UAT-01; UAT-04  

**Requirement:** Petugas dengan izin dapat mengonversi stok madu curah menjadi SKU kemasan pada lokasi yang sama.

**Acceptance criteria:**
- **FR-REP-01/AC-01** — Pengemasan tersedia di Gudang Pusat dan semua outlet.
- **FR-REP-01/AC-02** — Operator memilih satu batch sumber, satu SKU hasil, jumlah hasil, dan kuantitas bahan aktual.
- **FR-REP-01/AC-03** — Pengemasan tidak dapat diposting jika bahan kurang atau tidak layak jual.
- **FR-REP-01/AC-04** — Draft tidak mengubah saldo.
- **FR-REP-01/AC-05** — Posting mengurangi bahan dan menambah produk jadi secara atomik.

**Planned verification:** `TEST/FR-REP-01/AC-*` (belum dijalankan); UAT-01; UAT-04.

### FR-REP-02 — Pengukuran, susut, dan traceability

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.repacking`, `inventory.traceability`  
**Actor:** Petugas repacking berizin · **Authorization scope:** inventory.traceability; tenant/location/batch  
**Reference:** `historical baseline: FR-REP-02 (PRDv1.0.md)` · **UAT:** UAT-01; UAT-04  

**Requirement:** Jumlah hasil kemasan tidak ditentukan hanya dari perkiraan volume tanpa pengukuran fisik.

**Acceptance criteria:**
- **FR-REP-02/AC-01** — Operator memasukkan berat bahan terpakai dan jumlah unit kemasan hasil.
- **FR-REP-02/AC-02** — Sistem menyimpan kuantitas aktual, faktor konversi yang digunakan bila relevan, dan susut/selisih yang dicatat.
- **FR-REP-02/AC-03** — Hasil pengemasan mendapat batch keluaran dengan relasi ke batch sumber.
- **FR-REP-02/AC-04** — Riwayat menunjukkan siapa melakukan pengemasan, di mana, dan kapan.
- **FR-REP-02/AC-05** — Ketika posting gagal, saldo bahan dan hasil keduanya tidak berubah.

**Planned verification:** `TEST/FR-REP-02/AC-*` (belum dijalankan); UAT-01; UAT-04.

### FR-TRF-01 — Transfer normal

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.transfer`  
**Actor:** Pengirim/penerima berizin · **Authorization scope:** inventory.transfer; source/destination  
**Reference:** `historical baseline: FR-TRF-01 (PRDv1.0.md)` · **UAT:** UAT-03  

**Requirement:** Mendukung alur Draft → Dikirim → Dalam Transit → Diterima/Selisih → Selesai.

**Acceptance criteria:**
- **FR-TRF-01/AC-01** — Transfer mempunyai nomor dokumen, lokasi asal/tujuan, SKU, batch, kuantitas, operator dan waktu.
- **FR-TRF-01/AC-02** — Draft tidak mengurangi saldo asal.
- **FR-TRF-01/AC-03** — Ketika dikirim, stok tersedia asal berkurang dan saldo transit bertambah.
- **FR-TRF-01/AC-04** — Stok tujuan hanya bertambah sejumlah yang telah diterima.
- **FR-TRF-01/AC-05** — Transfer internal tidak mengubah omzet.
- **FR-TRF-01/AC-06** — Posting pengiriman atau penerimaan berulang dengan ID sama tidak menduplikasi mutasi.

**Planned verification:** `TEST/FR-TRF-01/AC-*` (belum dijalankan); UAT-03.

### FR-TRF-02 — Transfer cepat

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.transfer`  
**Actor:** Operator transfer cepat berizin · **Authorization scope:** inventory.transfer; source/destination  
**Reference:** `historical baseline: FR-TRF-02 (PRDv1.0.md)` · **UAT:** UAT-02  

**Requirement:** Untuk perpindahan fisik langsung, khususnya Gudang Pusat → Outlet 1, operator berizin dapat menyelesaikan perpindahan dalam satu konfirmasi.

**Acceptance criteria:**
- **FR-TRF-02/AC-01** — Operator memilih SKU, jumlah, sumber dan tujuan.
- **FR-TRF-02/AC-02** — Konfirmasi menghasilkan pengurangan sumber dan pertambahan tujuan dalam satu transaksi atomik.
- **FR-TRF-02/AC-03** — Histori tetap memuat dokumen transfer lengkap dan identitas operator.
- **FR-TRF-02/AC-04** — Operator tanpa izin transfer cepat tidak dapat menggunakan API operasi tersebut.

**Planned verification:** `TEST/FR-TRF-02/AC-*` (belum dijalankan); UAT-02.

### FR-TRF-03 — Penerimaan sebagian dan selisih

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.transfer`  
**Actor:** Petugas penerimaan/approver berizin · **Authorization scope:** inventory.transfer; destination  
**Reference:** `historical baseline: FR-TRF-03 (PRDv1.0.md)` · **UAT:** UAT-03  

**Requirement:** Outlet penerima dapat mencatat jumlah aktual yang berbeda dari jumlah dikirim.

**Acceptance criteria:**
- **FR-TRF-03/AC-01** — Jika 20 botol dikirim dan 18 diterima, stok tujuan bertambah 18 botol.
- **FR-TRF-03/AC-02** — Sisa 2 botol tetap teridentifikasi sebagai saldo transit yang belum terselesaikan atau discrepancy sesuai status kasus.
- **FR-TRF-03/AC-03** — Penyelesaian selisih memerlukan alasan dan pengguna berizin.
- **FR-TRF-03/AC-04** — Sistem mencegah penerimaan melebihi jumlah yang belum diterima tanpa proses koreksi khusus.
- **FR-TRF-03/AC-05** — Status akhir tidak dapat menjadi Selesai jika masih terdapat selisih yang belum diputuskan.

**Planned verification:** `TEST/FR-TRF-03/AC-*` (belum dijalankan); UAT-03.

### FR-RET-01 — Retur penjualan

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `sales.returns`  
**Actor:** Operator retur berizin · **Authorization scope:** sales.returns; tenant/outlet + transaksi asal  
**Reference:** `historical baseline: FR-RET-01 (PRDv1.0.md)` · **UAT:** UAT-09  

**Requirement:** Pengguna berizin dapat membuat retur atas transaksi penjualan yang telah tersinkron.

**Acceptance criteria:**
- **FR-RET-01/AC-01** — Retur merujuk nomor transaksi penjualan asal.
- **FR-RET-01/AC-02** — Jumlah retur kumulatif tidak dapat melebihi jumlah pembelian yang belum diretur.
- **FR-RET-01/AC-03** — Produk layak jual menambah stok jual kembali; produk rusak masuk stok tidak layak jual.
- **FR-RET-01/AC-04** — Nilai retur mengurangi penjualan neto dan mencatat pengembalian dana sesuai metode yang diproses.
- **FR-RET-01/AC-05** — Retur tidak menghapus transaksi asal.

**Planned verification:** `TEST/FR-RET-01/AC-*` (belum dijalankan); UAT-09.

### FR-RET-02 — Void dan koreksi

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `sales.voids`  
**Actor:** Supervisor/Owner berizin · **Authorization scope:** sales.voids; tenant/outlet + transaksi asal  
**Reference:** `historical baseline: FR-RET-02 (PRDv1.0.md)` · **UAT:** UAT-09  

**Requirement:** Transaksi selesai tidak diedit secara langsung; pembatalan memakai dokumen pembalik.

**Acceptance criteria:**
- **FR-RET-02/AC-01** — Void transaksi selesai membutuhkan permission supervisor/owner dan alasan.
- **FR-RET-02/AC-02** — Void menghasilkan pembalikan finansial dan inventori yang tepat satu kali.
- **FR-RET-02/AC-03** — Riwayat menyimpan hubungan transaksi asli, pembalik, operator dan waktu.
- **FR-RET-02/AC-04** — Transaksi offline yang masih pending tidak boleh diretur melalui mekanisme retur server sebelum sinkronisasi.

**Planned verification:** `TEST/FR-RET-02/AC-*` (belum dijalankan); UAT-09.

### FR-RET-03 — Adjustment stok

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.adjustment`  
**Actor:** Petugas/approver inventori berizin · **Authorization scope:** inventory.adjustment; tenant/location  
**Reference:** `historical baseline: FR-RET-03 (PRDv1.0.md)` · **UAT:** UAT-09  

**Requirement:** Pengguna berizin dapat menyesuaikan stok berdasarkan penyebab yang jelas.

**Acceptance criteria:**
- **FR-RET-03/AC-01** — Adjustment mempunyai lokasi, SKU, batch, jumlah perubahan, alasan dan operator.
- **FR-RET-03/AC-02** — Perubahan yang ditolak tidak memengaruhi saldo.
- **FR-RET-03/AC-03** — Penyesuaian negatif tidak boleh melewati batas validasi yang ditetapkan tanpa prosedur penyelesaian khusus.
- **FR-RET-03/AC-04** — Histori adjustment dapat difilter oleh outlet, produk, tanggal dan alasan.

**Planned verification:** `TEST/FR-RET-03/AC-*` (belum dijalankan); UAT-09.

**Blocked:** `OPEN-003` untuk preset approver/approval policy final; jangan memberi akses approval otomatis hanya dari nama role.

### FR-COUNT-01 — Pembuatan sesi opname

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.stock_count`  
**Actor:** Petugas stock opname berizin · **Authorization scope:** inventory.stock_count; tenant/location  
**Reference:** `historical baseline: FR-COUNT-01 (PRDv1.0.md)` · **UAT:** UAT-09  

**Requirement:** Petugas dapat membuat sesi penghitungan untuk lokasi dan daftar produk tertentu.

**Acceptance criteria:**
- **FR-COUNT-01/AC-01** — Sesi menyimpan snapshot saldo sistem dan waktu mulai.
- **FR-COUNT-01/AC-02** — Petugas memasukkan jumlah fisik tanpa langsung mengganti stok.
- **FR-COUNT-01/AC-03** — Sistem menampilkan selisih fisik terhadap snapshot.
- **FR-COUNT-01/AC-04** — Pengguna tanpa izin persetujuan tidak dapat memposting koreksi.

**Planned verification:** `TEST/FR-COUNT-01/AC-*` (belum dijalankan); UAT-09.

### FR-COUNT-02 — Persetujuan dan penerapan

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `inventory.stock_count`, `inventory.adjustment`  
**Actor:** Approver opname berizin · **Authorization scope:** inventory.stock_count; tenant/location  
**Reference:** `historical baseline: FR-COUNT-02 (PRDv1.0.md)` · **UAT:** UAT-09  

**Requirement:** Selisih opname dapat ditinjau dan disetujui sebelum menjadi adjustment.

**Acceptance criteria:**
- **FR-COUNT-02/AC-01** — Pengguna berizin menyetujui atau menolak hasil opname.
- **FR-COUNT-02/AC-02** — Jika stok berubah setelah snapshot dan sebelum persetujuan, sistem mendeteksi perubahan tersebut dan meminta rekonsiliasi/penghitungan ulang atas SKU terdampak.
- **FR-COUNT-02/AC-03** — Posting menghasilkan adjustment yang bisa ditelusuri ke sesi opname.
- **FR-COUNT-02/AC-04** — Sesi selesai tidak dapat diposting dua kali.

**Planned verification:** `TEST/FR-COUNT-02/AC-*` (belum dijalankan); UAT-09.

**Blocked:** `OPEN-003` untuk preset approver/approval policy final; jangan memberi akses approval otomatis hanya dari nama role.

### FR-AUD-01 — Audit trail

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `audit.activity`  
**Actor:** Sistem; Owner/role audit berizin · **Authorization scope:** audit.activity; tenant/platform context  
**Reference:** `historical baseline: FR-AUD-01 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-08; UAT-HV-06  

**Requirement:** Sistem merekam seluruh aktivitas bisnis sensitif dan peristiwa keamanan yang relevan.

**Acceptance criteria:**
- **FR-AUD-01/AC-01** — Login, kegagalan login, perubahan akses, perubahan harga, posting stok, retur, void, transfer, dan repacking mempunyai event audit.
- **FR-AUD-01/AC-02** — Event memuat tenant, operator, perangkat jika relevan, objek, tindakan, status, dan timestamp.
- **FR-AUD-01/AC-03** — Event penting tidak dapat diedit atau dihapus pengguna biasa.
- **FR-AUD-01/AC-04** — Password, PIN, token dan kredensial tidak disimpan sebagai nilai mentah dalam audit.
- **FR-AUD-01/AC-05** — Owner dapat mencari aktivitas berdasarkan operator, tanggal, lokasi dan jenis aksi.
- **FR-AUD-01/AC-06** — Histori login operasional disimpan pada **PostgreSQL selama 7 hari**; event offline memakai queue IndexedDB sementara, dan dihapus dari queue hanya sesudah ACK persisten. **[Added from PRD v04 — Review Required]**
- **FR-AUD-01/AC-07** — Perubahan/ekspor data Confidential dicatat tanpa mencatat nilai secret; retensi critical security audit berbeda dan masih OPEN-006. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-AUD-01/AC-*` (belum dijalankan); UAT-08; UAT-HV-06.

### FR-RPT-01 — Stok per lokasi dan global

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `reports.inventory`  
**Actor:** Owner/role laporan inventori · **Authorization scope:** reports.inventory; tenant/location  
**Reference:** `historical baseline: FR-RPT-01 (PRDv1.0.md)` · **UAT:** UAT-10  

**Requirement:** Owner melihat saldo stok tiap lokasi, seluruh outlet, gudang dan stok transit.

**Acceptance criteria:**
- **FR-RPT-01/AC-01** — Filter lokasi hanya menampilkan saldo lokasi yang dipilih.
- **FR-RPT-01/AC-02** — Laporan global tidak menghitung stok transit sebagai tambahan ganda terhadap total kepemilikan barang.
- **FR-RPT-01/AC-03** — Detail stok dapat ditelusuri hingga SKU dan batch.
- **FR-RPT-01/AC-04** — Laporan menandai data bermasalah seperti saldo negatif hasil rekonsiliasi atau selisih transfer.

**Planned verification:** `TEST/FR-RPT-01/AC-*` (belum dijalankan); UAT-10.

### FR-RPT-02 — Mutasi dan pengemasan

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `reports.stock_movements`  
**Actor:** Owner/role laporan mutasi · **Authorization scope:** reports.stock_movements; tenant/location  
**Reference:** `historical baseline: FR-RPT-02 (PRDv1.0.md)` · **UAT:** UAT-01; UAT-03  

**Requirement:** Owner dapat melihat asal, tujuan dan penyebab perubahan stok.

**Acceptance criteria:**
- **FR-RPT-02/AC-01** — Riwayat dapat difilter berdasarkan tanggal, lokasi, SKU, operator dan jenis operasi.
- **FR-RPT-02/AC-02** — Pengemasan menampilkan total bahan terpakai serta jumlah produk jadi.
- **FR-RPT-02/AC-03** — Transfer menampilkan jumlah dikirim, diterima, transit dan selisih.
- **FR-RPT-02/AC-04** — Seluruh angka laporan sesuai dokumen transaksi yang telah diposting.

**Planned verification:** `TEST/FR-RPT-02/AC-*` (belum dijalankan); UAT-01; UAT-03.

### FR-POS-01 — Antarmuka transaksi mobile

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `sales.cashier`  
**Actor:** Operator kasir · **Authorization scope:** sales.cashier; device/outlet + permission  
**Reference:** `historical baseline: FR-POS-01 (PRDv1.0.md)` · **UAT:** UAT-05  

**Requirement:** Kasir mempunyai tampilan sederhana untuk menemukan produk, mengatur keranjang dan melakukan pembayaran.

**Acceptance criteria:**
- **FR-POS-01/AC-01** — Kasir bisa mencari produk menggunakan nama, SKU atau barcode yang tersedia.
- **FR-POS-01/AC-02** — Tombol dan area interaksi nyaman untuk layar HP/tablet.
- **FR-POS-01/AC-03** — Keranjang menampilkan harga, jumlah, diskon, subtotal dan total.
- **FR-POS-01/AC-04** — Perubahan jumlah langsung menghitung ulang total tanpa memuat ulang halaman.
- **FR-POS-01/AC-05** — Outlet aktif dan operator terlihat jelas selama transaksi.

**Planned verification:** `TEST/FR-POS-01/AC-*` (belum dijalankan); UAT-05.

### FR-POS-02 — Penyelesaian penjualan

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `sales.transactions`, `sales.payments`  
**Actor:** Operator kasir · **Authorization scope:** sales.transactions/payments; tenant/outlet  
**Reference:** `historical baseline: FR-POS-02 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-05; UAT-06  

**Requirement:** Kasir dapat menyelesaikan transaksi tunai dan mencatat pembayaran nontunai manual ketika online.

**Acceptance criteria:**
- **FR-POS-02/AC-01** — Setiap transaksi memiliki ID unik, waktu kejadian, outlet, operator, detail item, nominal dan metode pembayaran.
- **FR-POS-02/AC-02** — Perhitungan total dan kembalian benar.
- **FR-POS-02/AC-03** — Transaksi yang berhasil diproses server menghasilkan stok keluar satu kali.
- **FR-POS-02/AC-04** — Kasir tidak dapat mengubah transaksi yang sudah selesai.
- **FR-POS-02/AC-05** — Jika pembayaran manual masih menunggu konfirmasi, statusnya tidak ditampilkan seolah-olah pembayaran telah terverifikasi oleh penyedia pembayaran.
- **FR-POS-02/AC-06** — Struk/presentasi penjualan offline harus membedakan status tersimpan lokal (`pending`) dari transaksi sudah diterima backend (`synced`). **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-POS-02/AC-*` (belum dijalankan); UAT-05; UAT-06.

### FR-POS-03 — Histori transaksi

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `sales.history`  
**Actor:** Operator/Owner berizin · **Authorization scope:** sales.history; tenant/outlet  
**Reference:** `historical baseline: FR-POS-03 (PRDv1.0.md)` · **UAT:** UAT-08  

**Requirement:** Operator melihat riwayat sesuai hak akses dan dapat mencari transaksi tertentu.

**Acceptance criteria:**
- **FR-POS-03/AC-01** — Kasir tidak dapat melihat transaksi outlet yang tidak diizinkan.
- **FR-POS-03/AC-02** — Owner dapat mencari transaksi berdasarkan nomor, waktu, outlet, operator dan nominal.
- **FR-POS-03/AC-03** — Daftar menggunakan pagination berbasis cursor atau metode efisien setara, tanpa mengambil seluruh histori.
- **FR-POS-03/AC-04** — Detail menampilkan status pembayaran, status sinkronisasi bila relevan, retur dan pembatalan.

**Planned verification:** `TEST/FR-POS-03/AC-*` (belum dijalankan); UAT-08.

### FR-OFF-01 — Penyimpanan transaksi lokal

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `offline.storage`, `identity.offline_access`  
**Actor:** Operator kasir offline berizin · **Authorization scope:** offline.storage; device/outlet  
**Reference:** `historical baseline: FR-OFF-01 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-05  

**Requirement:** Ketika server tidak dapat diakses, kasir yang telah diaktivasi dapat menyelesaikan pencatatan penjualan tunai di perangkat.

**Acceptance criteria:**
- **FR-OFF-01/AC-01** — Transaksi disimpan secara lokal sebelum aplikasi menyatakan pencatatan lokal berhasil.
- **FR-OFF-01/AC-02** — Setiap transaksi offline memiliki ID unik yang stabil ketika dikirim ulang.
- **FR-OFF-01/AC-03** — Transaksi pending tetap tampil setelah aplikasi dibuka kembali, selama penyimpanan perangkat masih tersedia.
- **FR-OFF-01/AC-04** — Aplikasi menampilkan jumlah transaksi pending dan waktu sinkronisasi terakhir.
- **FR-OFF-01/AC-05** — Jika penyimpanan lokal gagal atau tidak tersedia, aplikasi tidak menampilkan transaksi seolah-olah sudah berhasil disimpan.
- **FR-OFF-01/AC-06** — Offline-first berlaku untuk **kasir tunai inti saja**; receiving, repacking, transfer, adjustment, dan opname tidak diperbolehkan offline. **[Added from PRD v04 — Review Required]**
- **FR-OFF-01/AC-07** — Queue transaksi penjualan dan queue histori login adalah alur berbeda; pembersihan queue audit tidak boleh menghapus transaksi pending. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-OFF-01/AC-*` (belum dijalankan); UAT-05.

### FR-OFF-02 — Sinkronisasi dan idempotency

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `offline.sync`  
**Actor:** Sync client/backend · **Authorization scope:** offline.sync; device/tenant/operation ID  
**Reference:** `historical baseline: FR-OFF-02 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-06  

**Requirement:** Saat koneksi dan aplikasi aktif kembali, transaksi pending dikirim ke server secara aman.

**Acceptance criteria:**
- **FR-OFF-02/AC-01** — Transaksi dikirim ulang secara otomatis dengan mekanisme retry dan dapat dipicu manual.
- **FR-OFF-02/AC-02** — Kegagalan jaringan tidak menghapus antrean lokal.
- **FR-OFF-02/AC-03** — Pengiriman transaksi identik sebanyak dua kali menghasilkan satu transaksi server dan satu set mutasi stok.
- **FR-OFF-02/AC-04** — Server mengembalikan hasil pengakuan yang memungkinkan perangkat menandai transaksi sebagai tersinkron.
- **FR-OFF-02/AC-05** — Jika transaksi membutuhkan penanganan khusus, status dan alasannya ditampilkan tanpa kehilangan detail transaksi.
- **FR-OFF-02/AC-06** — Rekonsiliasi transaksi yang dibuat offline ketika tenant suspended/device revoked/entitlement expired tidak menghapus bukti lokal dan **tidak otomatis memposting** tanpa kebijakan yang belum diputuskan (OPEN-007/008). **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-OFF-02/AC-*` (belum dijalankan); UAT-06.

### FR-OFF-03 — Rekonsiliasi stok

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `offline.reconciliation`, `inventory.balance`  
**Actor:** Owner/rekonsiliasi berizin · **Authorization scope:** offline.reconciliation; tenant/outlet  
**Reference:** `historical baseline: FR-OFF-03 (PRDv1.0.md)` · **UAT:** UAT-07  

**Requirement:** Sistem memproses penjualan offline tanpa mengabaikan konflik stok.

**Acceptance criteria:**
- **FR-OFF-03/AC-01** — Penjualan offline mempertahankan harga, operator, outlet dan waktu transaksi asli.
- **FR-OFF-03/AC-02** — Jika stok server tidak mencukupi ketika transaksi masuk, sistem tetap menyimpan bukti penjualan dan mencatat shortage/exception untuk rekonsiliasi.
- **FR-OFF-03/AC-03** — Transaksi tidak menggandakan pengurangan stok pada proses retry.
- **FR-OFF-03/AC-04** — Owner dapat melihat transaksi bermasalah yang memerlukan tindak lanjut.
- **FR-OFF-03/AC-05** — Sistem membedakan waktu transaksi terjadi di perangkat dan waktu transaksi diterima server.

**Planned verification:** `TEST/FR-OFF-03/AC-*` (belum dijalankan); UAT-07.

### FR-DASH-01 — Dashboard penjualan

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `reports.dashboard`  
**Actor:** Owner/role laporan · **Authorization scope:** reports.dashboard; tenant/location  
**Reference:** `historical baseline: FR-DASH-01 (PRDv1.0.md)` · **UAT:** UAT-10  

**Requirement:** Owner melihat performa semua outlet dalam satu dashboard.

**Acceptance criteria:**
- **FR-DASH-01/AC-01** — Owner melihat omzet per outlet dan omzet gabungan.
- **FR-DASH-01/AC-02** — Filter Hari Ini, Kemarin dan Rentang Tanggal tersedia.
- **FR-DASH-01/AC-03** — Dashboard dapat menelusuri total menjadi daftar transaksi dan detailnya.
- **FR-DASH-01/AC-04** — Penjualan yang belum diterima server tidak dihitung ke angka server.
- **FR-DASH-01/AC-05** — Dashboard menampilkan waktu pembaruan terakhir.

**Planned verification:** `TEST/FR-DASH-01/AC-*` (belum dijalankan); UAT-10.

### FR-DASH-02 — Definisi metrik

**Priority:** P0 · **Status:** Approved baseline  
**Canonical owner:** `reports.sales`  
**Actor:** Owner/role laporan · **Authorization scope:** reports.sales; tenant/location  
**Reference:** `historical baseline: FR-DASH-02 (PRDv1.0.md)` · **UAT:** UAT-10  

**Requirement:** Laporan membedakan nilai transaksi dari uang yang benar-benar diterima.

**Acceptance criteria:**
- **FR-DASH-02/AC-01** — Penjualan bruto dihitung sebelum diskon/retur berdasarkan definisi yang ditetapkan.
- **FR-DASH-02/AC-02** — Penjualan neto memperhitungkan diskon serta retur/pembalik yang relevan.
- **FR-DASH-02/AC-03** — Total pembayaran yang diterima dapat dikelompokkan berdasarkan metode pembayaran.
- **FR-DASH-02/AC-04** — Status pembayaran tertunda tidak dicampur tanpa penanda ke pembayaran yang sudah terkonfirmasi.
- **FR-DASH-02/AC-05** — Total gabungan sama dengan jumlah komponen outlet untuk periode dan dasar pengakuan yang sama.

**Planned verification:** `TEST/FR-DASH-02/AC-*` (belum dijalankan); UAT-10.

### FR-NOTIF-01 — Pembaruan penjualan owner

**Priority:** P0 · **Status:** Approved baseline + Revised/Extended (AC delta Review Required)  
**Canonical owner:** `realtime.events`, `realtime.notifications`  
**Actor:** Owner/role penerima event · **Authorization scope:** realtime.events/notifications; tenant/recipient  
**Reference:** `historical baseline: FR-NOTIF-01 (PRDv1.0.md)`; `PRD.md` supersession/CAP decisions · **UAT:** UAT-10; UAT-HV-10  

**Requirement:** Penjualan yang masuk server dapat memperbarui dashboard dan menghasilkan notifikasi owner.

**Acceptance criteria:**
- **FR-NOTIF-01/AC-01** — Ketika penjualan online selesai, event diterbitkan setelah transaksi database berhasil commit.
- **FR-NOTIF-01/AC-02** — Owner yang sedang membuka aplikasi mendapatkan pembaruan dashboard tanpa refresh manual ketika koneksi realtime tersedia.
- **FR-NOTIF-01/AC-03** — Owner dapat mengaktifkan atau mematikan push notification penjualan.
- **FR-NOTIF-01/AC-04** — Penjualan offline baru menghasilkan pembaruan ketika transaksi telah diterima server.
- **FR-NOTIF-01/AC-05** — Notifikasi berulang tidak muncul akibat retry pengiriman yang identik.
- **FR-NOTIF-01/AC-06** — Realtime memakai tenant/user/location/field scope; event dan notifikasi untuk user tanpa izin biaya tidak mengandung HPP, margin atau valuasi. **[Added from PRD v04 — Review Required]**

**Planned verification:** `TEST/FR-NOTIF-01/AC-*` (belum dijalankan); UAT-10; UAT-HV-10.

## 4. FR tambahan hasil konsolidasi CAP-14–58 (Derived P0)

> **Status semua FR di bagian ini:** **Derived P0 — Review Required**. Sumber keputusan tingkat requirement sudah Approved, tetapi ini adalah perincian AC yang dibuat dari sumber; bukan klaim pengguna menyetujui satu per satu. FR dengan OPEN-ID hanya dapat diimplementasikan sampai batas perilaku yang telah diputuskan.

### FR-HV-PLT-001 — Provisioning dan status tenant

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-007`  
**Canonical owner:** `platform.tenancy`  
**Actor:** Superadmin platform berizin · **Authorization scope:** platform scope + tenant lifecycle  
**Decision source:** CAP-14/15/22/28/40 · **UAT:** UAT-HV-01 (provisioning/session), UAT-HV-08 (suspend)  

**Requirement:** Superadmin dapat memprovision tenant, menetapkan tepat satu Primary Owner aktif, mengubah status `pending/active/suspended/closed`, dan mengaudit perubahannya.

**Acceptance criteria (derived):**
- **FR-HV-PLT-001/AC-01** — Provisioning tenant hanya dapat dilakukan dalam sesi admin platform yang sah dan permission eksplisit.
- **FR-HV-PLT-001/AC-02** — Tenant baru mempunyai identitas unik; Primary Owner ditautkan pada tenant yang tepat tanpa membuat sesi tenant dari sesi admin.
- **FR-HV-PLT-001/AC-03** — Status `suspended` menolak operasi bisnis online baru yang tidak diizinkan; histori dan transaksi pending tidak dihapus.
- **FR-HV-PLT-001/AC-04** — Perubahan status dan alasan dicatat dalam audit platform; pengaktifan kembali membutuhkan aktor berizin.

**Planned verification:** `TEST/FR-HV-PLT-001/AC-*`, UAT-HV-01 (provisioning/session) dan UAT-HV-08 (suspend); hasil belum tersedia.

### FR-HV-PLT-002 — Lifecycle dan recovery Primary Owner

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-004`  
**Canonical owner:** `platform.tenancy`, `platform.administration`  
**Actor:** Superadmin berizin · **Authorization scope:** platform + tenant target  
**Decision source:** CAP-29/40 · **UAT:** UAT-HV-11 (**BLOCKED — OPEN-004**, bukan UAT-HV-01)  

**Requirement:** Hi-Ve menyediakan proses administratif manual yang terlacak untuk pemulihan akses dan transfer Primary Owner.

**Acceptance criteria (derived):**
- **FR-HV-PLT-002/AC-01** — Transfer tidak dapat menghasilkan lebih dari satu Primary Owner aktif dalam tenant yang sama.
- **FR-HV-PLT-002/AC-02** — Akun Google/nomor WhatsApp yang hanya dapat diakses seseorang tidak dianggap bukti cukup untuk transfer kepemilikan.
- **FR-HV-PLT-002/AC-03** — Permintaan recovery/transfer tidak otomatis terlaksana tanpa keputusan verifikasi manual yang berwenang dan audit.
- **FR-HV-PLT-002/AC-04** — Implementasi final bukti/verifikasi dan prosedur break-glass diblokir hingga OPEN-004 diselesaikan.

**Planned verification:** `TEST/FR-HV-PLT-002/AC-*` dan UAT-HV-11 (**BLOCKED — OPEN-004**); hasil belum tersedia.

### FR-HV-ENT-001 — Entitlement per tenant dan masa berlaku

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-008`, `OPEN-010`  
**Canonical owner:** `platform.entitlements`  
**Actor:** Superadmin berizin; guard backend · **Authorization scope:** tenant + capability canonical ID  
**Decision source:** CAP-04/05/27/30/41/45 · **UAT:** UAT-HV-08  

**Requirement:** Backend mengelola entitlement capability tenant dengan effective/expiry, status, dan histori, tanpa menduplikasi hak atas canonical submodule yang sama.

**Acceptance criteria (derived):**
- **FR-HV-ENT-001/AC-01** — Pemberian/revoke entitlement memerlukan permission platform dan menghasilkan event audit.
- **FR-HV-ENT-001/AC-02** — Operasi baru di server ditolak bila capability belum efektif, kedaluwarsa atau dicabut, termasuk request API langsung.
- **FR-HV-ENT-001/AC-03** — Perubahan entitlement tidak menghapus transaksi, audit, atau data bisnis historis.
- **FR-HV-ENT-001/AC-04** — Grace period dan kebijakan operasi offline yang tertunda mengikuti OPEN-008, tidak dibuat default diam-diam.

**Planned verification:** `TEST/FR-HV-ENT-001/AC-*` dan UAT-HV-08; hasil belum tersedia.

### FR-HV-ADM-001 — Platform Superadmin terpisah

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required  
**Canonical owner:** `platform.administration`  
**Actor:** Superadmin terdaftar · **Authorization scope:** platform scope only  
**Decision source:** CAP-14/15/21/23/24/32/34 · **UAT:** UAT-HV-01  

**Requirement:** Superadmin App menyediakan workflow administratif minimum tenant, Owner dan entitlement pada konteks platform yang terpisah.

**Acceptance criteria (derived):**
- **FR-HV-ADM-001/AC-01** — Login Google yang tidak terdaftar sebagai admin tidak dapat memakai Admin API.
- **FR-HV-ADM-001/AC-02** — Sesi tenant tidak dapat mengakses Admin API; sesi admin tidak otomatis dapat menggunakan App API tenant.
- **FR-HV-ADM-001/AC-03** — Superadmin tidak memperoleh akses otomatis ke transaksi/HPP tenant hanya karena memiliki role platform.
- **FR-HV-ADM-001/AC-04** — Tidak diwajibkan MFA tambahan oleh Hi-Ve pada V1; kontrol login, izin, audit dan revocation tetap diterapkan.

**Planned verification:** `TEST/FR-HV-ADM-001/AC-*` dan UAT-HV-01; hasil belum tersedia.

### FR-HV-IDN-001 — Google OIDC untuk Owner dan Superadmin

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-015`  
**Canonical owner:** `identity.authentication`  
**Actor:** Primary Owner / Superadmin · **Authorization scope:** sesi platform/tenant terpisah  
**Decision source:** CAP-24/25/33/34 · **UAT:** UAT-HV-01  

**Requirement:** Login online Superadmin dan Primary Owner menggunakan Google OAuth 2.0/OpenID Connect secara langsung tanpa Auth0.

**Acceptance criteria (derived):**
- **FR-HV-IDN-001/AC-01** — Identitas eksternal ditautkan menggunakan pasangan issuer dan subject; email saja tidak cukup untuk linking.
- **FR-HV-IDN-001/AC-02** — Token Google valid dari akun yang belum diprovision tidak menghasilkan izin Hi-Ve.
- **FR-HV-IDN-001/AC-03** — Backend memverifikasi bukti OIDC yang diperlukan dan status akun/tenant sebelum menerbitkan sesi Hi-Ve.
- **FR-HV-IDN-001/AC-04** — Logout/revocation membatasi sesi sesuai konteks; token Google Owner tidak dibagikan kepada device kasir bersama.

**Planned verification:** `TEST/FR-HV-IDN-001/AC-*` dan UAT-HV-01; hasil belum tersedia.

### FR-HV-IDN-002 — Batas hak staf & Owner operator

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required  
**Canonical owner:** `identity.authentication`, `devices.operators`  
**Actor:** Owner dan staf pada device aktif · **Authorization scope:** device+outlet+operator  
**Decision source:** CAP-33/35/36/37/40 · **UAT:** UAT-HV-04  

**Requirement:** Staf V1 login dengan profil dan PIN pada perangkat teraktivasi; fungsi administratif sensitif Owner tetap membutuhkan sesi Google online yang sesuai.

**Acceptance criteria (derived):**
- **FR-HV-IDN-002/AC-01** — Pengguna staf tanpa perangkat teraktivasi tidak mendapat metode login langsung pada perangkat pribadi V1.
- **FR-HV-IDN-002/AC-02** — Owner yang muncul sebagai operator kasir tidak otomatis memperoleh fungsi admin sensitif hanya lewat PIN.
- **FR-HV-IDN-002/AC-03** — Pergantian operator mengakhiri otorisasi operator sebelumnya sebelum sesi baru diterapkan.

**Planned verification:** `TEST/FR-HV-IDN-002/AC-*` dan UAT-HV-04; hasil belum tersedia.

### FR-HV-USR-001 — Owner menetapkan PIN awal staf

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-001`  
**Canonical owner:** `identity.users`  
**Actor:** Owner atau pengguna dengan izin user/PIN · **Authorization scope:** tenant + target user  
**Decision source:** CAP-35/36/38/40 · **UAT:** UAT-HV-05  

**Requirement:** Owner mengelola PIN awal enam digit untuk pengguna V1 dan dapat meresetnya secara terotorisasi.

**Acceptance criteria (derived):**
- **FR-HV-USR-001/AC-01** — PIN asli tidak dapat dibaca kembali dari UI maupun API setelah ditetapkan.
- **FR-HV-USR-001/AC-02** — PIN yang tidak terdiri atas enam digit ditolak; keputusan panjang PIN V1 tetap enam digit.
- **FR-HV-USR-001/AC-03** — Reset dicatat dalam audit dan perubahan diverifikasi saat perangkat kembali online.
- **FR-HV-USR-001/AC-04** — PIN pilihan staf sendiri tidak wajib pada V1 dan tidak muncul sebagai jalur aktivasi default.

**Planned verification:** `TEST/FR-HV-USR-001/AC-*` dan UAT-HV-05; hasil belum tersedia.

### FR-HV-ACL-001 — Enforcement granular permission

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-003`, `OPEN-011`  
**Canonical owner:** `identity.permissions`  
**Actor:** Owner, role khusus, service backend · **Authorization scope:** tenant + entitlement + user + location/device  
**Decision source:** CAP-05/11/26/48 · **UAT:** UAT-08; UAT-HV-04  

**Requirement:** Semua operasi sensitif menegakkan custom role, action permission, location scope, entitlement dan status resource di backend dengan deny-by-default.

**Acceptance criteria (derived):**
- **FR-HV-ACL-001/AC-01** — Menyembunyikan menu tidak dianggap cukup; request langsung ke API tanpa izin ditolak.
- **FR-HV-ACL-001/AC-02** — Penetapan operator pada device tidak pernah menambah permission atau location scope user.
- **FR-HV-ACL-001/AC-03** — Perubahan role/izin dicatat audit dan berlaku untuk operasi online baru.
- **FR-HV-ACL-001/AC-04** — Preset final untuk approval berisiko tidak dikarang sampai OPEN-003 diputuskan.

**Planned verification:** `TEST/FR-HV-ACL-001/AC-*` dan UAT-08; UAT-HV-04; hasil belum tersedia.

### FR-HV-ACL-002 — Akses field Confidential

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-002`, `OPEN-011`  
**Canonical owner:** `identity.permissions`  
**Actor:** Owner/role yang secara eksplisit diberi hak biaya · **Authorization scope:** tenant + location + field permission  
**Decision source:** CAP-55/56/58 · **UAT:** UAT-HV-09  

**Requirement:** Backend membatasi HPP, harga beli, margin, valuasi dan data strategis berdasarkan permission field dan scope yang sah.

**Acceptance criteria (derived):**
- **FR-HV-ACL-002/AC-01** — Owner secara default boleh mengakses data Confidential dalam tenant-nya; staf tanpa izin eksplisit tidak menerima field tersebut.
- **FR-HV-ACL-002/AC-02** — Respons daftar/detail, filter, agregasi, export, serta error response tidak membocorkan Confidential kepada aktor tanpa izin.
- **FR-HV-ACL-002/AC-03** — Superadmin tidak otomatis bisa membaca HPP tenant.
- **FR-HV-ACL-002/AC-04** — Margin sendiri termasuk Confidential dan dapat memungkinkan inferensi HPP bila harga jual diketahui; pemberian `margin.read` harus mempertimbangkan sensitivitas biaya dan memerlukan izin eksplisit.

**Planned verification:** `TEST/FR-HV-ACL-002/AC-*` dan UAT-HV-09; hasil belum tersedia.

### FR-HV-PIN-001 — Verifikasi PIN online dan offline

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-001`  
**Canonical owner:** `identity.offline_access`  
**Actor:** Operator device teraktivasi · **Authorization scope:** device + operator + outlet + bounded rights  
**Decision source:** CAP-35/36/38 · **UAT:** UAT-HV-05  

**Requirement:** Perangkat teraktivasi memverifikasi PIN online melalui backend dan mendukung login offline terbatas melalui verifier lokal terpisah.

**Acceptance criteria (derived):**
- **FR-HV-PIN-001/AC-01** — PIN plaintext tidak disimpan dalam PostgreSQL, IndexedDB, logs atau audit; verifier backend menggunakan hash adaptif.
- **FR-HV-PIN-001/AC-02** — Verifier offline tidak menyebarkan hash backend apa adanya dan hanya berlaku untuk operator yang sudah disetujui sebelumnya.
- **FR-HV-PIN-001/AC-03** — Lima kegagalan PIN berturut-turut menyebabkan lockout awal 15 menit; pengujian harus mencakup pembatasan online dan offline.
- **FR-HV-PIN-001/AC-04** — Setelah batas otorisasi offline yang disetujui habis, sistem menolak login offline baru; TTL final dan desain KDF masih OPEN-001.

**Planned verification:** `TEST/FR-HV-PIN-001/AC-*` dan UAT-HV-05; hasil belum tersedia.

### FR-HV-DEV-001 — Pairing dan revoke perangkat

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-001`, `OPEN-013`  
**Canonical owner:** `devices.management`  
**Actor:** Owner atau aktor dengan permission · **Authorization scope:** tenant + instalasi device  
**Decision source:** CAP-13/35/37 · **UAT:** UAT-HV-03  

**Requirement:** Perangkat baru hanya dapat teraktivasi online menggunakan pairing code sekali pakai yang disetujui Owner dari sesi terpisah.

**Acceptance criteria (derived):**
- **FR-HV-DEV-001/AC-01** — Pairing code berlaku terbatas, hanya satu kali, dan salah tenant/expired/used ditolak.
- **FR-HV-DEV-001/AC-02** — Proses activation tidak mengirim credential Google Owner ke perangkat bersama.
- **FR-HV-DEV-001/AC-03** — Revoke/suspend dari backend menghentikan operasi online baru pada perangkat terkait.
- **FR-HV-DEV-001/AC-04** — Saat offline, revoke baru efektif setelah kondisi otorisasi lokal memungkinkan; aplikasi tidak mengklaim pencabutan seketika.

**Planned verification:** `TEST/FR-HV-DEV-001/AC-*` dan UAT-HV-03; hasil belum tersedia.

### FR-HV-DEV-002 — Perangkat multi-outlet dan perpindahan konteks

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-013`  
**Canonical owner:** `devices.management`  
**Actor:** Owner/role penugas device · **Authorization scope:** tenant + list outlet permitted + active outlet  
**Decision source:** CAP-39/50 · **UAT:** UAT-HV-04  

**Requirement:** Satu device terdaftar pada satu tenant, dapat diassign beberapa outlet, dan tepat satu outlet aktif per sesi operasional.

**Acceptance criteria (derived):**
- **FR-HV-DEV-002/AC-01** — Perpindahan outlet membutuhkan koneksi server dan validasi access/assignment terbaru.
- **FR-HV-DEV-002/AC-02** — Transaksi dan audit melekat pada outlet aktif saat kejadian; tidak diwarisi dari operator yang berbeda.
- **FR-HV-DEV-002/AC-03** — Perubahan outlet tidak dapat membuang transaksi pending; bila ada pending, pergantian ditunda atau mengikuti prosedur terkontrol yang telah disetujui.

**Planned verification:** `TEST/FR-HV-DEV-002/AC-*` dan UAT-HV-04; hasil belum tersedia.

### FR-HV-OPR-001 — Operator assignment per device-outlet

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required  
**Canonical owner:** `devices.operators`  
**Actor:** Owner/pengguna dengan izin assign · **Authorization scope:** device + outlet + user location scope  
**Decision source:** CAP-36/37/39 · **UAT:** UAT-HV-04  

**Requirement:** Owner memilih hanya akun user tenant yang diperlukan untuk masing-masing perangkat dan outlet tanpa membuat akun baru.

**Acceptance criteria (derived):**
- **FR-HV-OPR-001/AC-01** — Profil tidak aktif, bukan anggota tenant, tidak mempunyai akses outlet atau tidak ditugaskan tidak dapat login ke device/outlet tersebut.
- **FR-HV-OPR-001/AC-02** — Pergantian operator mengunci sesi lama dan mengubah konteks operator tanpa membawa hak sebelumnya.
- **FR-HV-OPR-001/AC-03** — Transaksi dan histori audit memuat operator sebenarnya, bukan hanya pemilik device.

**Planned verification:** `TEST/FR-HV-OPR-001/AC-*` dan UAT-HV-04; hasil belum tersedia.

### FR-HV-AUD-001 — Histori login PostgreSQL 7 hari

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-006`  
**Canonical owner:** `audit.activity`  
**Actor:** Backend dan client audit queue; Owner/role audit · **Authorization scope:** tenant + device + operator + outlet  
**Decision source:** CAP-38/57 · **UAT:** UAT-HV-06  

**Requirement:** Histori setiap percobaan login sukses/gagal/lockout disimpan secara otoritatif di PostgreSQL selama 7 hari, dengan IndexedDB hanya sebagai antrean sementara offline.

**Acceptance criteria (derived):**
- **FR-HV-AUD-001/AC-01** — Saat online, event login masuk server; ketika offline, event ID unik tersimpan dalam queue lokal sementara.
- **FR-HV-AUD-001/AC-02** — Pengiriman ulang event yang sama tidak menggandakan histori di backend.
- **FR-HV-AUD-001/AC-03** — Event queue lokal dihapus **hanya setelah ACK penyimpanan persisten**; kegagalan sync/kuota tidak disembunyikan.
- **FR-HV-AUD-001/AC-04** — Owner/role berizin dapat memfilter histori menurut user/device/outlet/waktu/hasil; tidak ada PIN yang tercatat.
- **FR-HV-AUD-001/AC-05** — Retensi histori operasional PostgreSQL 7 hari; critical security audit tidak dihapus hanya karena jangka ini berakhir.

**Planned verification:** `TEST/FR-HV-AUD-001/AC-*` dan UAT-HV-06; hasil belum tersedia.

### FR-HV-AUD-002 — Audit akses data sensitif

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-006`  
**Canonical owner:** `audit.activity`  
**Actor:** Sistem + pembaca audit berizin · **Authorization scope:** tenant + actor + record/field category  
**Decision source:** CAP-55/56/57/58 · **UAT:** UAT-HV-09  

**Requirement:** Perubahan harga modal/HPP, permission biaya, dan ekspor sensitif tercatat dalam audit terproteksi.

**Acceptance criteria (derived):**
- **FR-HV-AUD-002/AC-01** — Audit menyimpan aktor, waktu, objek, tenant, lingkup, tindakan, hasil dan device bila relevan.
- **FR-HV-AUD-002/AC-02** — Audit tidak menyimpan PIN/token maupun salinan masif nilai Confidential dalam log umum.
- **FR-HV-AUD-002/AC-03** — Aktor tanpa izin audit tidak dapat mencari atau memodifikasi event audit kritis.
- **FR-HV-AUD-002/AC-04** — Retensi audit kritis mengikuti kebijakan terpisah yang masih OPEN-006.

**Planned verification:** `TEST/FR-HV-AUD-002/AC-*` dan UAT-HV-09; hasil belum tersedia.

### FR-HV-SET-001 — Pengaturan tenant dan global

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-009`  
**Canonical owner:** `settings.tenant`, `platform.administration`  
**Actor:** Owner/role setting tenant; admin platform · **Authorization scope:** tenant scope / global platform  
**Decision source:** CAP-31/42 · **UAT:** UAT-HV-02  

**Requirement:** Pengaturan identitas tenant, struk, notifikasi, timezone dan preferensi operasional dipisahkan dari konfigurasi platform global.

**Acceptance criteria (derived):**
- **FR-HV-SET-001/AC-01** — Owner berizin dapat membaca/mengubah pengaturan hanya milik tenant sendiri.
- **FR-HV-SET-001/AC-02** — Admin platform berizin mengelola konfigurasi global tanpa otomatis memperoleh akses ke data transaksi tenant.
- **FR-HV-SET-001/AC-03** — Credential provider/secret tidak ditampilkan kembali sebagai teks asli pada UI settings.

**Planned verification:** `TEST/FR-HV-SET-001/AC-*` dan UAT-HV-02; hasil belum tersedia.

### FR-HV-RCP-001 — Tampilan struk dan keluaran yang didukung perangkat

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required  
**Canonical owner:** `sales.receipts`  
**Actor:** Kasir/Owner berizin · **Authorization scope:** tenant + outlet + transaction  
**Decision source:** CAP-47/50 · **UAT:** UAT-HV-07  

**Requirement:** Hi-Ve menyediakan tampilan struk penjualan dengan identitas transaksi, outlet, item, jumlah, harga, total, pembayaran dan status sync.

**Acceptance criteria (derived):**
- **FR-HV-RCP-001/AC-01** — Struk transaksi offline menandakan belum diterima backend; tidak mengklaim final server.
- **FR-HV-RCP-001/AC-02** — Nilai struk konsisten dengan snapshot transaksi yang dibuat, termasuk diskon dan metode pembayaran.
- **FR-HV-RCP-001/AC-03** — Print menggunakan dukungan browser/perangkat yang tersedia; tidak menjanjikan dukungan native seluruh printer Bluetooth pada V1.
- **FR-HV-RCP-001/AC-04** — Struk kasir tidak memuat HPP, margin atau biaya jika operator tidak berizin.

**Planned verification:** `TEST/FR-HV-RCP-001/AC-*` dan UAT-HV-07; hasil belum tersedia.

### FR-HV-RTS-001 — Indikator koneksi dan status sinkronisasi

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required  
**Canonical owner:** `realtime.connection_status`  
**Actor:** Operator/Owner · **Authorization scope:** device + current session + pending queue  
**Decision source:** CAP-47/50 · **UAT:** UAT-HV-10  

**Requirement:** Hi-Ve App membedakan koneksi jaringan, reachability backend, status realtime, pending sync, sync failure dan waktu ACK terakhir.

**Acceptance criteria (derived):**
- **FR-HV-RTS-001/AC-01** — Tidak ada label "synced" hanya karena browser online tanpa acknowledgement server.
- **FR-HV-RTS-001/AC-02** — Gangguan WebSocket tidak otomatis disajikan sebagai backend/API offline jika API masih dapat diakses.
- **FR-HV-RTS-001/AC-03** — Pending dan needs-review tetap terlihat sesuai permission setelah aplikasi dibuka kembali selama datanya tersedia.

**Planned verification:** `TEST/FR-HV-RTS-001/AC-*` dan UAT-HV-10; hasil belum tersedia.

### FR-HV-DATA-001 — Data minimization semua kanal

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-002`  
**Canonical owner:** `identity.permissions`, `reports.sales`, `offline.storage`, `realtime.events`  
**Actor:** Semua caller API dan penyaji data · **Authorization scope:** tenant + permission + field + outlet  
**Decision source:** CAP-55/56/58 · **UAT:** UAT-HV-09  

**Requirement:** HPP/margin/harga beli/valuasi terlindungi pada API, report, export, websocket, log, error dan offline cache, bukan hanya melalui penyembunyian UI.

**Acceptance criteria (derived):**
- **FR-HV-DATA-001/AC-01** — Kasir tanpa izin biaya menerima data SKU/harga jual yang sah tetapi tidak menerima field Confidential atau agregasi yang mengungkapnya.
- **FR-HV-DATA-001/AC-02** — Data biaya tidak dipersist dalam IndexedDB kasir yang tidak memerlukan akses biaya.
- **FR-HV-DATA-001/AC-03** — Ekspor memerlukan otorisasi data sensitif dan tetap mematuhi tenant/location scope.
- **FR-HV-DATA-001/AC-04** — Tidak ditambahkan enkripsi khusus kolom HPP atau aplikasi pada V1; keamanan DB, disk, backup dan secrets tetap harus ditangani.

**Planned verification:** `TEST/FR-HV-DATA-001/AC-*` dan UAT-HV-09; hasil belum tersedia.

### FR-HV-PROD-001 — Minimum capability dan cross-product reuse

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required; **Blocked parameter:** `OPEN-010`  
**Canonical owner:** `platform.entitlements`  
**Actor:** Platform authorization + tenant roles · **Authorization scope:** tenant + canonical capability  
**Decision source:** CAP-01/02/03/04/05/27/43/44/45/46 · **UAT:** UAT-HV-08  

**Requirement:** Setiap produk memiliki minimum operational capabilities dan capability bersama tidak diduplikasi hanya karena berbeda produk.

**Acceptance criteria (derived):**
- **FR-HV-PROD-001/AC-01** — POS dapat menyiapkan SKU, harga dan stok minimum untuk berjualan tanpa harus membeli Warehouse.
- **FR-HV-PROD-001/AC-02** — Warehouse dapat memproses inventory core tanpa mengaktifkan capability kasir POS.
- **FR-HV-PROD-001/AC-03** — Retail dapat mengaktifkan Cashier via entitlement canonical yang sama, tidak memerlukan transaksi engine kedua.
- **FR-HV-PROD-001/AC-04** — Paket harga dan alokasi submodul komersial final mengikuti OPEN-010, bukan dibuat default tersembunyi.

**Planned verification:** `TEST/FR-HV-PROD-001/AC-*` dan UAT-HV-08; hasil belum tersedia.

### FR-HV-RT-001 — Penerbitan event setelah commit

**Priority:** P0 (tingkat requirement) · **Status:** Derived P0 — Review Required  
**Canonical owner:** `realtime.events`, `realtime.notifications`  
**Actor:** Backend event publisher · **Authorization scope:** tenant + recipient entitlement/location  
**Decision source:** CAP-47/50 · **UAT:** UAT-HV-10  

**Requirement:** Event bisnis dan notifikasi Owner dipublikasikan setelah commit transaksi backend dan hanya kepada penerima berizin.

**Acceptance criteria (derived):**
- **FR-HV-RT-001/AC-01** — Event tidak diterbitkan sebagai transaksi sah bila commit gagal.
- **FR-HV-RT-001/AC-02** — Pengiriman ulang bisnis yang sama tidak menghasilkan notifikasi ganda yang menyesatkan.
- **FR-HV-RT-001/AC-03** — Reconnection dan pembaruan ulang status tidak memunculkan angka dashboard dari transaksi lokal yang belum di-ACK.

**Planned verification:** `TEST/FR-HV-RT-001/AC-*` dan UAT-HV-10; hasil belum tersedia.

## 5. Supersession Matrix & batasan tidak boleh diasumsikan

| Sumber / istilah lama | Rule yang berlaku dari PRD v04 | Perlakuan di functional spec |
|---|---|---|
| `FR-PRD-02/AC-02–03` kg↔mL berbasis faktor | Konversi otomatis hanya dimensi sama; repacking mencatat input/output aktual | AC-02/03 **Revised**, faktor densitas bukan P0 |
| Header `PRDv1.0.md` online-first | Offline-first hanya inti kasir tunai; operasi inventori posting online | `FR-OFF-01` diperjelas |
| `FR-AUTH-02` device activation umum | Pairing online Owner, multi-outlet, per-device+outlet assignment | AC tambahan + `FR-HV-DEV-*` |
| `FR-AUTH-04` 72 jam kandidat | TTL belum disetujui | `OPEN-001`, tidak hardcode 72 jam |
| Retensi login lokal 7 hari (diskusi lama) | PostgreSQL 7 hari, IndexedDB hanya queue offline hingga ACK | `FR-HV-AUD-001` |
| Autentikasi umum admin/Owner | Google OIDC langsung, sesi platform vs tenant terpisah, no Auth0; tak ada MFA tambahan Hi-Ve wajib V1 | `FR-HV-IDN-001`, `FR-HV-ADM-001` |
| Host frontend belum final | Landing Pages; App/Superadmin Workers Static Assets; bisnis API di Go VPS | NFR/deployment gate, bukan FR baru |
| HPP sebagai atribut produk umum | Confidential, field auth + response minimization, tanpa enkripsi kolom/app khusus | `FR-HV-ACL-002`, `FR-HV-DATA-001` |

## 6. Traceability Registry, CAP, FR dan test

### 6.1 Coverage 40 canonical submodules

| Submodule | FR yang memverifikasi tanggung jawab P0 |
|---|---|
| `platform.tenancy` | `FR-AUTH-01`, `FR-HV-PLT-001`, `FR-HV-PLT-002` |
| `platform.entitlements` | `FR-HV-ENT-001`, `FR-HV-PROD-001` |
| `platform.administration` | `FR-HV-PLT-002`, `FR-HV-ADM-001`, `FR-HV-SET-001` |
| `identity.authentication` | `FR-AUTH-01`, `FR-HV-IDN-001`, `FR-HV-IDN-002` |
| `identity.users` | `FR-AUTH-03`, `FR-HV-USR-001` |
| `identity.permissions` | `FR-AUTH-03`, `FR-LOC-02`, `FR-HV-ACL-001`, `FR-HV-ACL-002`, `FR-HV-DATA-001` |
| `identity.offline_access` | `FR-AUTH-04`, `FR-OFF-01`, `FR-HV-PIN-001` |
| `devices.management` | `FR-AUTH-02`, `FR-AUTH-04`, `FR-HV-DEV-001`, `FR-HV-DEV-002` |
| `devices.operators` | `FR-AUTH-02`, `FR-HV-IDN-002`, `FR-HV-OPR-001` |
| `settings.tenant` | `FR-HV-SET-001` |
| `audit.activity` | `FR-AUD-01`, `FR-HV-AUD-001`, `FR-HV-AUD-002` |
| `catalog.products` | `FR-PRD-01`, `FR-PRD-04` |
| `catalog.units` | `FR-PRD-02` |
| `catalog.pricing` | `FR-PRD-03` |
| `locations.management` | `FR-LOC-01`, `FR-LOC-02` |
| `inventory.balance` | `FR-STK-02`, `FR-STK-03`, `FR-OFF-03` |
| `inventory.ledger` | `FR-STK-01`, `FR-STK-03` |
| `inventory.receiving` | `FR-RCV-01` |
| `inventory.repacking` | `FR-REP-01`, `FR-REP-02` |
| `inventory.transfer` | `FR-TRF-01`, `FR-TRF-02`, `FR-TRF-03` |
| `inventory.adjustment` | `FR-RET-03`, `FR-COUNT-02` |
| `inventory.stock_count` | `FR-COUNT-01`, `FR-COUNT-02` |
| `inventory.traceability` | `FR-PRD-04`, `FR-REP-02` |
| `sales.cashier` | `FR-POS-01` |
| `sales.transactions` | `FR-POS-02` |
| `sales.payments` | `FR-POS-02` |
| `sales.history` | `FR-POS-03` |
| `sales.returns` | `FR-RET-01` |
| `sales.voids` | `FR-RET-02` |
| `sales.receipts` | `FR-HV-RCP-001` |
| `reports.dashboard` | `FR-DASH-01` |
| `reports.sales` | `FR-DASH-02`, `FR-HV-DATA-001` |
| `reports.inventory` | `FR-RPT-01` |
| `reports.stock_movements` | `FR-RPT-02` |
| `offline.storage` | `FR-OFF-01`, `FR-HV-DATA-001` |
| `offline.sync` | `FR-OFF-02` |
| `offline.reconciliation` | `FR-OFF-03` |
| `realtime.events` | `FR-NOTIF-01`, `FR-HV-DATA-001`, `FR-HV-RT-001` |
| `realtime.notifications` | `FR-NOTIF-01`, `FR-HV-RT-001` |
| `realtime.connection_status` | `FR-HV-RTS-001` |

> **Makna coverage:** minimal ada requirement pemilik untuk setiap submodul. Ini **bukan** bukti semua aksi fitur, API contract, atau permission IDs final. Nama final Feature Registry/permision codes masih OPEN-011.

### 6.2 Crosswalk keputusan CAP-01–CAP-58

| CAP | FR yang terkait atau pengecualian | Catatan |
|---|---|---|
| CAP-01 | `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-02 | `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-03 | `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-04 | `FR-HV-ENT-001`, `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-05 | `FR-HV-ENT-001`, `FR-HV-ACL-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-06 | — | Kandidat P1; sengaja tidak diturunkan ke FR P0 |
| CAP-07 | — | Kandidat P1; sengaja tidak diturunkan ke FR P0 |
| CAP-08 | — | Kandidat P1; sengaja tidak diturunkan ke FR P0 |
| CAP-09 | `FR-POS-02` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-10 | `FR-PRD-04` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-11 | `FR-LOC-02`, `FR-HV-ACL-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-12 | `FR-RET-01`, `FR-RET-02` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-13 | `FR-HV-DEV-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-14 | `FR-HV-ADM-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-15 | `FR-HV-ADM-001`, `FR-HV-PLT-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-16 | `FR-HV-IDN-001` | Prinsip adapter disetujui; provider/kewajiban verifikasi OPEN-009 |
| CAP-17 | `FR-HV-ADM-001`, `FR-HV-RCP-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-18 | — | Arsitektur/hosting atau NFR/UAT; diwajibkan dalam PRD dan quality gates, bukan FR bisnis tersendiri |
| CAP-19 | `FR-POS-01`, `FR-OFF-01` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-20 | `FR-HV-ADM-001`, `FR-HV-IDN-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-21 | `FR-HV-ADM-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-22 | `FR-HV-PLT-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-23 | `FR-HV-ADM-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-24 | `FR-HV-IDN-001`, `FR-HV-ADM-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-25 | `FR-HV-PLT-002` | Detail verifikasi/prosedur OPEN-004 |
| CAP-26 | `FR-HV-ACL-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-27 | `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-28 | `FR-HV-PLT-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-29 | `FR-HV-PLT-002` | Detail verifikasi/prosedur OPEN-004 |
| CAP-30 | `FR-HV-ENT-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-31 | `FR-HV-SET-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-32 | `FR-HV-ADM-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-33 | `FR-HV-IDN-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-34 | `FR-HV-ADM-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-35 | `FR-HV-DEV-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-36 | `FR-HV-OPR-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-37 | `FR-HV-OPR-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-38 | `FR-HV-PIN-001`, `FR-HV-AUD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-39 | `FR-HV-DEV-002`, `FR-HV-OPR-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-40 | `FR-HV-PLT-001`, `FR-HV-PLT-002` | Detail verifikasi/prosedur OPEN-004 |
| CAP-41 | `FR-HV-ENT-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-42 | `FR-HV-SET-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-43 | `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-44 | `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-45 | `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-46 | `FR-HV-PROD-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-47 | `FR-HV-RT-001`, `FR-HV-RCP-001`, `FR-HV-RTS-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-48 | `FR-HV-ACL-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-49 | `FR-PRD-02`, `FR-REP-02`, `FR-OFF-01` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-50 | `FR-HV-RTS-001`, `FR-POS-01` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-51 | — | Arsitektur/hosting atau NFR/UAT; diwajibkan dalam PRD dan quality gates, bukan FR bisnis tersendiri |
| CAP-52 | — | Arsitektur/hosting atau NFR/UAT; diwajibkan dalam PRD dan quality gates, bukan FR bisnis tersendiri |
| CAP-53 | — | Arsitektur/hosting atau NFR/UAT; diwajibkan dalam PRD dan quality gates, bukan FR bisnis tersendiri |
| CAP-54 | — | Arsitektur/hosting atau NFR/UAT; diwajibkan dalam PRD dan quality gates, bukan FR bisnis tersendiri |
| CAP-55 | `FR-HV-ACL-002`, `FR-HV-DATA-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-56 | `FR-HV-ACL-002`, `FR-HV-DATA-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-57 | `FR-HV-AUD-002` | Traceability requirement, tidak menyatakan semua detail implementasi final |
| CAP-58 | `FR-HV-DATA-001` | Traceability requirement, tidak menyatakan semua detail implementasi final |

### 6.3 Indeks UAT baseline dan kaitan ke FR

#### UAT-01 — Penerimaan dan pengemasan

**Given** Gudang menerima 30 kg Madu Randu dari batch RD-001.  
**When** Petugas mengemas 7,1 kg menjadi 10 botol Randu 500 ml.  
**Then** Sisa curah menjadi 22,9 kg, stok botol bertambah 10, dan riwayat produksi menghubungkan kedua SKU serta batch-nya.

**Linked FR (coverage awal):** `FR-PRD-01`, `FR-PRD-02`, `FR-PRD-04`, `FR-LOC-01`, `FR-STK-01`, `FR-RCV-01`, `FR-REP-01`, `FR-REP-02`, `FR-RPT-02`.

#### UAT-02 — Transfer cepat

**Given** Gudang mempunyai 10 botol siap jual.  
**When** Petugas memindahkan 4 botol ke Outlet 1 melalui transfer cepat.  
**Then** Gudang berkurang 4, Outlet 1 bertambah 4, dan omzet tetap tidak berubah.

**Linked FR (coverage awal):** `FR-STK-01`, `FR-STK-02`, `FR-TRF-02`.

#### UAT-03 — Transfer normal dengan selisih

**Given** Gudang mengirim 20 botol ke Outlet 2.  
**When** Outlet 2 menerima 18 botol.  
**Then** Outlet 2 bertambah 18 dan 2 botol tetap terlacak untuk penyelesaian selisih.

**Linked FR (coverage awal):** `FR-TRF-01`, `FR-TRF-03`, `FR-RPT-02`.

#### UAT-04 — Pengemasan di outlet

**Given** Outlet 3 menerima stok madu curah.  
**When** Operator berizin melakukan repacking.  
**Then** Stok curah Outlet 3 berkurang, stok kemasannya bertambah, dan stok Gudang maupun Outlet 1/2 tidak berubah.

**Linked FR (coverage awal):** `FR-PRD-02`, `FR-PRD-04`, `FR-LOC-01`, `FR-REP-01`, `FR-REP-02`.

#### UAT-05 — Penjualan offline

**Given** Perangkat kasir Outlet 2 telah diaktivasi, memiliki data lokal, dan internet terputus.  
**When** Kasir menjual 2 botol secara tunai.  
**Then** Transaksi disimpan dengan status pending, tersedia dalam riwayat lokal, dan tidak hilang saat navigasi/restart normal aplikasi selama penyimpanan lokal tetap tersedia.

**Linked FR (coverage awal):** `FR-AUTH-02`, `FR-AUTH-04`, `FR-PRD-03`, `FR-POS-01`, `FR-POS-02`, `FR-OFF-01`.

#### UAT-06 — Retry sinkronisasi

**Given** Transaksi offline telah disimpan.  
**When** Perangkat online dan mengirim transaksi yang sama dua kali.  
**Then** Server hanya membuat satu transaksi, satu pengurangan stok, dan satu event bisnis yang terkonfirmasi.

**Linked FR (coverage awal):** `FR-STK-01`, `FR-STK-03`, `FR-POS-02`, `FR-OFF-02`.

#### UAT-07 — Konflik stok offline

**Given** Kasir offline mencatat penjualan yang melebihi stok server terbaru.  
**When** Transaksi disinkronkan.  
**Then** Bukti penjualan tetap ada, tidak terjadi posting ganda, dan shortage masuk daftar rekonsiliasi.

**Linked FR (coverage awal):** `FR-STK-02`, `FR-STK-03`, `FR-OFF-03`.

#### UAT-08 — Pembatasan akses

**Given** Kasir Outlet 2 tidak memiliki izin laporan global.  
**When** Kasir membuka laporan melalui navigasi maupun request API langsung.  
**Then** Akses ditolak, dan upaya sensitif yang tidak diizinkan dapat tercatat di audit.

**Linked FR (coverage awal):** `FR-AUTH-01`, `FR-AUTH-03`, `FR-LOC-02`, `FR-AUD-01`, `FR-POS-03`.

#### UAT-09 — Retur dan stock opname

**Given** Sebuah transaksi telah selesai dan stok berubah karena penjualan.  
**When** Supervisor melakukan retur atau menyetujui penyesuaian stok.  
**Then** Dibuat pergerakan pembalik/adjustment yang benar, dengan alasan dan referensi asal tanpa menghapus histori.

**Linked FR (coverage awal):** `FR-RET-01`, `FR-RET-02`, `FR-RET-03`, `FR-COUNT-01`, `FR-COUNT-02`.

#### UAT-10 — Dashboard konsolidasi

**Given** Outlet 1 mempunyai penjualan neto Rp500.000, Outlet 2 Rp300.000, dan Outlet 3 Rp200.000 pada periode yang sama.  
**When** Owner membuka laporan global.  
**Then** Total penjualan neto Rp1.000.000 dan dapat ditelusuri menjadi tiga komponen outlet. Transfer internal tidak ikut dihitung.

**Linked FR (coverage awal):** `FR-RPT-01`, `FR-DASH-01`, `FR-DASH-02`, `FR-NOTIF-01`.


## 7. Rencana uji tambahan keputusan baru (UAT-HV, belum dieksekusi)

### UAT-HV-01 — OIDC dan isolasi platform

Given akun Google valid tetapi tidak diprovision, atau sesi tenant mencoba Admin API; When login/API admin dilakukan; Then akses ditolak dan tidak ada tenant session yang diterbitkan lintas konteks.

**Linked derived FR:** `FR-HV-PLT-001` (provisioning/session), `FR-HV-ADM-001`, `FR-HV-IDN-001`. **Tidak mencakup recovery/transfer `FR-HV-PLT-002`**; lihat UAT-HV-11. **Status:** Proposed verification, not executed.

### UAT-HV-02 — Settings sesuai konteks

Given Owner tenant dan platform admin; When masing-masing membuka/mengubah settings; Then hanya settings pada scope yang sah yang terlihat/terubah dan secret tidak ditampilkan sebagai plaintext.

**Linked derived FR:** `FR-HV-SET-001`. **Status:** Proposed verification, not executed.

### UAT-HV-03 — Pairing perangkat

Given perangkat baru dan kode pairing; When kode digunakan kembali/kedaluwarsa atau disetujui Owner tenant lain; Then aktivasi ditolak tanpa token Owner tersalin ke perangkat.

**Linked derived FR:** `FR-HV-DEV-001`. **Status:** Proposed verification, not executed.

### UAT-HV-04 — Multi-outlet dan operator

Given perangkat diizinkan Outlet 1/2, hanya beberapa operator di Outlet 2; When outlet dipilih dan operator login; Then hanya profil aktif/berizin tampil dan transaksi terikat pada outlet/operator benar.

**Linked derived FR:** `FR-HV-IDN-002`, `FR-HV-ACL-001`, `FR-HV-DEV-002`, `FR-HV-OPR-001`. **Status:** Proposed verification, not executed.

### UAT-HV-05 — PIN offline dan batas akses

Given device teraktivasi dan operator terdaftar; When PIN salah lima kali atau otorisasi lokal kedaluwarsa; Then lockout/deny sesuai policy dan operasi inventori sensitif tetap online.

**Linked derived FR:** `FR-HV-USR-001`, `FR-HV-PIN-001`. **Status:** Proposed verification, not executed.

### UAT-HV-06 — Audit login 7 hari

Given event login online dan offline; When offline event dikirim retry dua kali; Then PostgreSQL menyimpan sekali, queue lokal dihapus sesudah ACK, filter Owner berfungsi, dan histori operasional mengikuti 7 hari.

**Linked derived FR:** `FR-HV-AUD-001`. **Status:** Proposed verification, not executed.

### UAT-HV-07 — Struk transaksi

Given penjualan online maupun offline; When struk ditampilkan; Then item/total/outlet/operator/status sync sesuai snapshot tanpa mengungkap HPP tidak berizin.

**Linked derived FR:** `FR-HV-RCP-001`. **Status:** Proposed verification, not executed.

### UAT-HV-08 — Tenant lifecycle & entitlement

Given tenant suspended atau capability expired; When pengguna mencoba operasi online baru dan ada transaksi offline pending; Then operasi baru ditolak tetapi bukti pending dipertahankan untuk rekonsiliasi.

**Linked derived FR:** `FR-HV-PLT-001` (tenant suspended), `FR-HV-ENT-001`, `FR-HV-PROD-001`. **Status:** Proposed verification, not executed; perlakuan posting/rekonsiliasi pending masih BLOCKED oleh OPEN-007/008.

### UAT-HV-09 — Sensitive data leakage negatif

Given kasir tanpa cost permission dan Owner dengan hak biaya; When API SKU, report, export, WebSocket, cache dan error diakses; Then kasir tidak menerima Confidential, Owner terotorisasi sesuai scope, tindakan sensitif tercatat audit.

**Linked derived FR:** `FR-HV-ACL-002`, `FR-HV-AUD-002`, `FR-HV-DATA-001`. **Status:** Proposed verification, not executed.

### UAT-HV-10 — Realtime/connection status

Given API reachable tetapi WebSocket terputus dan transaksi pending lokal; When app dibuka/reconnect; Then status dibedakan, pending tidak dihitung sebagai synced, dan event server ditampilkan hanya setelah commit.

**Linked derived FR:** `FR-HV-RTS-001`, `FR-HV-RT-001`. **Status:** Proposed verification, not executed.

### UAT-HV-11 — Recovery/transfer Primary Owner (rencana terblokir)

Given permohonan pemulihan akses atau transfer Primary Owner suatu tenant; When Superadmin berizin menilai bukti dan memproses keputusan administratif; Then tidak boleh ada lebih dari satu Primary Owner aktif, kepemilikan tidak berpindah hanya karena penguasaan Google/WhatsApp, dan seluruh hasil dicatat di audit.

**Linked derived FR:** `FR-HV-PLT-002`. **Status:** **BLOCKED — OPEN-004** (kriteria verifikasi identitas, prosedur recovery dan break-glass belum final); **bukan** klaim implementasi atau test PASS.

## 8. Daftar keputusan yang masih OPEN dan blocked verification

| OPEN ID | Kebijakan yang belum final | Status |
|---|---|---|
| `OPEN-001` | TTL akses offline; 72 jam masih usulan uji, parameter KDF dan lockout tahan serangan lokal | **OPEN — High** |
| `OPEN-002` | Metode costing/HPP: harga beli vs unit cost, FIFO/average, snapshot historis, stock valuation | **OPEN — High** |
| `OPEN-003` | Preset approval untuk adjustment, opname, retur/void, transfer, perubahan harga sensitif | **OPEN — High** |
| `OPEN-004` | Bukti verifikasi recovery/transfer Owner dan break-glass Superadmin | **OPEN — High** |
| `OPEN-005` | Hosting frontend: Landing Pages; App/Superadmin Workers Static Assets | **CLOSED — APPROVED** |
| `OPEN-006` | Retensi critical security audit, purge dan clock-skew, kasus data audit offline hilang sebelum upload | **OPEN — High** |
| `OPEN-007` | Rekonsiliasi transaksi offline ketika tenant suspended atau device revoked | **OPEN — High** |
| `OPEN-008` | Entitlement expiry/grace dan tindakan pada operasi tertunda saat revoke | **OPEN — High** |
| `OPEN-009` | Kewajiban verifikasi WhatsApp Owner, provider, OTP/rate-limit dan biaya | **OPEN — Medium** |
| `OPEN-010` | Product bundles, tier, harga, quota tenant/outlet/device/user | **OPEN — Medium; tidak blokir pilot** |
| `OPEN-011` | ID fitur final, permission action, kontrak API/skema, actor/exception dan test per FR | **OPEN — High; FUNCTIONAL_SPEC tersedia, kontrak dan ID aksi belum final** |
| `OPEN-012` | Target numerik product KPIs dan final NFR berdasarkan perangkat, jaringan, data, dan hasil uji nyata | **OPEN — Medium** |
| `OPEN-013` | Kehilangan perangkat, penuh/eviction browser storage dan pemulihan transaksi pending | **OPEN — High** |
| `OPEN-014` | Botol/tutup/label sebagai SKU persediaan dan rincian costing bahan penunjang | **OPEN — Medium** |
| `OPEN-015` | Domain/subdomain, cookies, CSRF, OIDC redirects, konfigurasi Pages dan Workers Static Assets, secrets dan contingency VPS | **OPEN — High; provider sudah CLOSED** |
| `OPEN-016` | **Review kelengkapan FR/AC yang sudah dikonsolidasikan serta validasi traceability**; 36 FR+AC baseline dan 21 FR turunan sudah tersedia | **OPEN — Critical (review/verifikasi, bukan ketiadaan file)** |

> **OPEN-016:** Ketersediaan dokumen dan pemetaan FR/AC telah ditangani; **review cakupan FR/AC turunan, keterlacakan UAT termasuk UAT-HV-11, parameter OPEN, dan bukti pengujian belum selesai**. Tidak boleh menyatakan Temuan 1/4 Closed seluruhnya.

## 9. Quality gates, test management, dan Definition of Done

- **NFR:** Referensi tunggal: `PRD.md` §08.1–08.2 (NFR-01–NFR-14) dan metrik PM-01–PM-06. Tidak diduplikasi menjadi FR fiktif.
- **Security:** Uji negatif langsung terhadap API Admin vs App, tenant cross-access, field Confidential pada JSON/export/WebSocket/IndexedDB, exception/log, serta PIN dan device revocation.
- **Inventory:** Uji AC posting atomik/idempotent, ledger balance, transit/discrepancy, batch lineage, stock opname concurrency.
- **Offline:** Uji queue transaction & audit terpisah, timeout, retry, evicted storage, kasir tunai, deny sensitive online-only, stale auth; kasus OPEN tidak diakali dengan asumsi.
- **Deployment:** Landing Cloudflare Pages; App/Superadmin Workers Static Assets; tidak ada Worker script/Pages Functions pada jalur inti; HTTPS API/WSS menuju Go VPS.
- **Semua test ID pada spesifikasi ini adalah daftar rencana.** Bukti hasil dan perintah pengujian valid dari repo nanti dicatat dalam `TESTING.md`/CI setelah implementasi terotorisasi.
- **Gate pilot:** Semua AC P0 yang sudah final terpenuhi, UAT baseline + tambahan kritis lulus, backup/restore diuji, tidak ada bug kehilangan/duplikasi transaksi dan isolasi tenant, target NFR/PM terukur dan dilaporkan secara jujur.

## 10. Riwayat dan integritas sumber

| Dokumen | Peran |
|---|---|
| [`PRD.md`](PRD.md) (v04) | **Dokumen aktif**: sumber keputusan produk tertinggi, supersession dan OPEN tracker |
| `PRDv1.0.md` (22 Oktober 2026; arsip historis) | **Provenance saja, tidak perlu berada di repository aktif**: asal 36 FR+AC dan 10 UAT yang sudah dikonsolidasikan di file ini |
| `FUNCTIONAL_SPEC.md` (ini) | Dokumen aktif: FR/AC, pemetaan CAP dan rencana uji (detail turunan masih Review Required) |
| [`backend/AGENTS.md`](backend/AGENTS.md) | **Sudah tersedia**: aturan agent untuk backend Go, scope dan verifikasi |
| [`backend/docs/ARCHITECTURE.md`](backend/docs/ARCHITECTURE.md) | **Sudah tersedia**: batas desain Go Screaming Modular Monolith |

**Integritas sumber:** SHA-256 `PRD.md` aktif: `3994d7592a1fdb5b3dcbf98e6f5b72991fe803fac76ee89cc04fde27af2b1ed9`; arsip historis `PRDv1.0.md` saat konsolidasi: `eed8c2be0920daadf20ecb56f583e3b75d164e554edcbf8ce5470ed952a225b8` (**tidak diperlukan oleh agent saat runtime**). Hash hanya berlaku untuk isi file pada versi ini; hitung ulang setelah revisi.

**Tidak ada approval untuk membuat/memodifikasi source aplikasi melalui dokumen ini.**
