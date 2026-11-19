# PRD — Hi-Ve Multi-Product SaaS Platform (v04, optimized)

> **File kanonis di repository:** `PRD.md` · **Baseline produk:** v04 (18 November 2026) · **Perapian editorial:** 19 November 2026. Versi ini bukan versi aplikasi.  
> **Status:** **Approved Product Baseline** untuk CAP-01–CAP-58 sesuai batas keputusan; rincian `OPEN` dan review AC turunan tetap pending. Dokumen **bukan izin implementasi kode atau bukti pengujian**.  
> **Dokumen pendamping aktif:** [`FUNCTIONAL_SPEC.md`](FUNCTIONAL_SPEC.md) (57 FR, AC dan rencana UAT; **Review Required**), [`backend/AGENTS.md`](backend/AGENTS.md) (aturan coding agent), [`backend/docs/ARCHITECTURE.md`](backend/docs/ARCHITECTURE.md) (batas desain Go).  
> **Riwayat:** `PRDv1.0.md` (22 Oktober 2026) dan snapshot `PRDv01.md`–`PRDv04.md` adalah **arsip/provenance**, tidak wajib ada di repo aktif. FR/AC yang relevan telah dikonsolidasikan ke `FUNCTIONAL_SPEC.md`. Pengoptimalan ini **tidak mengubah CAP, prioritas P0, atau keputusan bisnis**.

## Konvensi status dan otoritas

- **APPROVED** = keputusan produk disetujui sesuai batasnya; **APPROVED prinsip/tingkat requirement** tidak berarti setiap parameter, API, atau AC turunan disetujui. **APPROVED kandidat P1** bukan P0.
- **OPEN / DETAIL DRAFT / Review Required** = belum final. Agent tidak boleh menebak kebijakan atau mengklaim detail tersebut approved. **P0** = wajib bagi pilot; **P1/P2** = tahapan berikutnya.
- **Otoritas kebutuhan:** keputusan terbaru yang disetujui di `PRD.md` + matriks supersession (§08.5) → FR/AC yang berlaku pada `FUNCTIONAL_SPEC.md` → arsip historis hanya sebagai bukti asal. Konflik atau kebutuhan kritis OPEN harus ditandai **BLOCKED**, bukan direkonsiliasi diam-diam.
- **Pembagian dokumen:** PRD = *what/why, scope, decision*; Functional Spec = *observable behavior/AC/UAT*; AGENTS = aturan kerja; ARCHITECTURE = *how/boundaries*. Baca bagian yang relevan, cek source/test aktual; jangan mengarang endpoint, hasil uji, atau keputusan.
- Perencanaan memakai **Product → Module → Submodule → Feature**. Setiap data/aturan bisnis memiliki domain owner. `OPEN-016` tetap terbuka untuk audit kelengkapan/traceability FR/AC, **bukan** karena Functional Spec belum dibuat.

---

## 01. Product Identity — APPROVED

| Atribut | Keputusan |
|---|---|
| Brand / Platform | **Hi-Ve** |
| Jenis | **Multi-Product SaaS Platform** |
| Kategori | **Business Operations & Commerce** |
| Deskripsi | **Hi-Ve adalah ekosistem SaaS multi-produk yang dirancang untuk membantu bisnis mengelola operasional secara terintegrasi, modular, dan scalable.** |
| Penamaan produk | Pola **Hi-Ve [Product]**; awal: **Hi-Ve POS**, **Hi-Ve Retail**, **Hi-Ve Warehouse**. Produk lain dapat ditambahkan. |
| Sasaran umum | Platform operasional bisnis dengan produk yang independen secara komersial, tetapi dapat berbagi data dan kemampuan secara terkendali. |

**Batas interpretasi:** Identitas produk tidak mewajibkan setiap produk memiliki backend, database, atau deployment terpisah.

## 02. Vision & Goals — APPROVED

**Visi:** Membangun ekosistem SaaS multi-produk yang terintegrasi, fleksibel, andal, dan dapat mengikuti pertumbuhan bisnis dari skala kecil ke skala lebih besar.

**Strategi pasar:** **Indonesia-first, Global-ready**. Operasi awal berorientasi Indonesia; rancangan tidak boleh mengunci sistem secara permanen pada satu jenis bisnis atau satu pasar.

**Tujuan utama:**

1. Menawarkan beberapa produk bisnis yang dapat digunakan sendiri atau dikombinasikan.
2. Menerapkan monetisasi modular, terutama pada tingkat **submodul**.
3. Mengintegrasikan data bisnis dalam tenant tanpa pemilik data yang tumpang tindih.
4. Memprioritaskan performa, keandalan, keamanan, serta integritas transaksi.
5. Menjaga UX operasional cepat dan sederhana untuk pengguna lapangan.
6. Memungkinkan perluasan industri, produk, dan kemampuan tanpa menulis ulang inti sistem.

## 03. Target Users & Problems — APPROVED

**Strategi segmen:** **Retail-first, Multi-Industry Ready**.

**Pilot operasional:** Toko madu di Indonesia, **3 outlet, 1 gudang, sekitar 10 pekerja**. Angka pilot tidak boleh menjadi batas yang di-hardcode pada aplikasi.

**Persona dan kewenangan:**

- **Platform Superadmin:** Pengelola platform Hi-Ve, **bukan** Owner tenant; fungsi spesifik dirinci pada Bagian 07.
- **Primary Owner:** Tepat **satu Primary Owner aktif sebagai penanggung jawab utama per tenant** dalam model produk saat ini; dapat membuat pengguna, mengatur custom roles, permissions dan cakupan lokasi di tenant-nya. Alur recovery/transfer Owner melalui verifikasi manual Superadmin telah disetujui; prosedur verifikasi dan bukti spesifik masih OPEN.
- **Tenant users:** Kepala outlet, kasir, staf gudang/inventori, administrator bisnis, dan persona lainnya sesuai kebutuhan. Nama role merupakan contoh/preset, **bukan** daftar role permanen yang dibatasi sistem.

**Masalah yang ditangani:** Sistem operasional terpisah, data stok dan transaksi yang tidak konsisten, kesulitan mengawasi banyak outlet, pembatasan akses yang kurang rinci, ekspansi bisnis yang kompleks, serta UX yang lambat/sulit dipelajari.

**Arah UX:** Minimal langkah, respons cepat, mudah dipahami pekerja pilot, dievaluasi melalui UAT lapangan. Owner membutuhkan dashboard dan notifikasi real-time saat aplikasi aktif, beserta indikator status koneksi yang jelas.

**Model akses:** Tenant isolation; entitlement produk/submodul; permission tingkat aksi yang relevan (mis. `read`, `create`, `update`, `approve`, `void`) dan location/data scope. Backend mengotorisasi dengan **deny by default / least privilege**.

## 04. Product Ecosystem — APPROVED

### 04.1 Produk

- **Hi-Ve POS:** Pengalaman kasir/penjualan, menggunakan shared catalog, lokasi, inventori, dan transaksi yang relevan.
- **Hi-Ve Retail:** Operasional retail/outlet; capability Sales/Cashier dapat disertakan melalui bundling tanpa implementasi kasir duplikat (CAP-27). Paket komersial detail masih OPEN.
- **Hi-Ve Warehouse:** Operasional gudang dan inventori; independen dari langganan POS untuk operasi stok. Pemetaan paket/harga detail masih OPEN.
- Produk selanjutnya dapat menggunakan kemampuan bersama tanpa membuat ulang domain bisnis yang sama.

**Independent Products:** Produk dapat digunakan/berlangganan secara independen. Ini adalah independensi **fungsional/komersial**, bukan janji microservices, backend terpisah, maupun database per produk.

### 04.2 Shared Platform & Shared Business Data

- **Shared Platform Capabilities:** Tenancy, identitas/akses, entitlement, keamanan dan audit.
- **Shared Business Capabilities:** Catalog, Inventory, Locations, dan kemampuan relevan lain digunakan lintas produk melalui kontrak yang jelas.
- **Shared Business Data per Tenant:** Satu sumber kebenaran untuk data yang sama, dengan **domain owner** yang otoritatif dan isolasi antartenant.
- **Product-Specific Presentation:** Produk boleh memiliki tampilan serta alur kerja berbeda, **tanpa menduplikasi logika bisnis inti**.
- Produk yang berdiri sendiri tetap harus memiliki kemampuan fundamental untuk operasinya; contoh: POS tetap dapat memproses pengurangan stok penjualan tanpa mewajibkan pembelian Warehouse.

### 04.3 Monetisasi

- **Submodule** adalah unit utama entitlement/monetisasi SaaS; feature-level add-on diperbolehkan bila diperlukan. **Flexible Product Capability Bundling (CAP-27):** POS, Retail, Warehouse dapat mengemas capability lintas produk sesuai entitlement, dengan tetap satu implementasi canonical.
- Tidak semua submodul merupakan add-on berbayar: ada kemampuan **core/fundamental**, **included**, dan **optional/billable**. Komposisi paket/harga final masih OPEN.
- Capability sama yang diaktifkan untuk tenant tidak wajib dibeli kembali hanya karena digunakan melalui produk lain; **akses produk dan permission pengguna tetap berlaku**.

## 05. Product Principles — APPROVED

1. **Performance First** sebagai prioritas optimisasi. **Security & Data Integrity** merupakan quality gate yang wajib dan tidak boleh dikorbankan untuk performa.
2. **Correctness & Reliability**, termasuk concurrency, atomicity, idempotency, audit, retry aman, dan penanganan kegagalan.
3. **Fast, intuitive UX**, diuji dengan pekerja pilot pada perangkat nyata.
4. **Go Modular Monolith dengan Screaming Architecture**: boundary mengikuti kemampuan/domain bisnis, dependensi eksplisit, tidak ada circular dependency ilegal, business logic tidak duplikatif.
5. **Kontrak antarmodul** untuk akses data dan operasi milik domain lain; modul tidak bebas menulis data domain lain.
6. **Architecture validation dan regression tests dalam GitHub Actions CI/CD**; risiko perubahan lokal diuji dengan unit/integration/contract/regression tests sesuai kebutuhan.
7. **PWA offline-first untuk operasi inti saja**, dengan IndexedDB untuk data/antrean persisten lokal dan service worker untuk app shell/cache; bukan janji semua operasional bisa offline.
8. **PostgreSQL = authoritative system of record**. **Redis** adalah infrastruktur pendukung bila digunakan (mis. cache/rate limit/event fanout), **bukan** pengganti penyimpanan transaksi/stok kritis. Kegagalan Redis atau WebSocket tidak boleh otomatis menggagalkan transaksi inti saat backend dan PostgreSQL sehat.
9. **Realtime setelah commit**; event/notification tidak boleh menyebabkan transaksi yang sama tercatat ganda. Status pending lokal tidak disamakan dengan transaksi yang telah dikonfirmasi backend.
10. **Global-ready dan extensible** tanpa memaksakan semua fitur internasional menjadi P0.
11. **Sensitive Business Data Protection (CAP-55–58):** Lindungi HPP/harga modal, margin, valuasi persediaan, harga pemasok, informasi finansial dan data strategis lain sesuai klasifikasi. **Backend wajib memeriksa hak akses per field dan membatasi payload**, bukan hanya menyembunyikan elemen UI. Perlindungan dasar ini wajib pada semua paket, bukan add-on keamanan berbayar.

## 06. Scope & Priorities — APPROVED, dengan rincian administrasi masih OPEN

### 06.1 Aturan prioritas

- **Seluruh kebutuhan operasional yang diperlukan pilot merupakan P0**; pengembangan dapat bertahap sesuai dependensi, tanpa mengurangi scope yang telah ditetapkan.
- **P1** adalah penyempurnaan lanjutan; **P2** adalah ekspansi; perubahan P0 hanya melalui keputusan eksplisit.
- Detail baseline FR/AC dari arsip `PRDv1.0.md` **sudah tersedia dalam `FUNCTIONAL_SPEC.md` aktif**. Ketentuan offline-first core terbaru menajamkan strategi lama *online-first/offline fallback*; **hanya operasi inti yang diputuskan boleh offline**. Agent tidak perlu mencari arsip historis dalam repository aktif.
- **Fondasi minimum Platform Superadmin** untuk provisioning tenant/Primary Owner dan pengelolaan entitlement disetujui untuk V1 (CAP-14–15, 21–23, 28–32, 40–42), **tanpa full billing SaaS**. Kebijakan minimum dijabarkan di Bagian 07; detail teknis, contoh layar, serta sebagian exception tetap OPEN.

### 06.2 Lingkup P0 pilot (domain operasional)

| Area | Kebutuhan wajib |
|---|---|
| Tenant / Identity / Access | Tenant, Primary Owner, Google OIDC langsung untuk Owner/Superadmin, user & custom role, granular permissions, cakupan lokasi, operator/PIN offline aman (PIN 6 digit **ditetapkan Owner pada V1**), aktivasi perangkat oleh Owner, multi-outlet dengan satu outlet aktif, penugasan profil per perangkat/lokasi, **histori login 7 hari di PostgreSQL; IndexedDB hanya antrean offline**, entitlement dasar. |
| Catalog | Master produk/SKU, kategori, barcode, varian/satuan, harga per outlet, histori/snapshot harga, tracking batch; **konversi standar dalam dimensi yang sama (kg↔g, L↔mL); tanpa konversi otomatis massa↔volume**. |
| Locations | Gudang dan outlet sebagai lokasi stok terpisah; hanya outlet menjadi lokasi kasir, aturan akses lokasi/rute. |
| Inventory | Saldo, stock ledger, penerimaan, repacking madu curah, transfer cepat/normal/transit/parsial/selisih, retur & koreksi, adjustment, stock opname & approval, traceability batch. |
| Sales | Pencarian produk, keranjang, checkout, pembayaran tunai dan nontunai manual **online**, histori transaksi, struk, retur dan void dengan bukti pembalik. |
| Reports | Owner dashboard gabungan & per outlet, laporan penjualan, stok dan mutasi, dasar perhitungan uang/omzet yang jelas. |
| Offline PWA | Cache katalog/harga sesuai versi, offline **penjualan tunai** dengan antrean IndexedDB, retry idempotent, status pending/synced/failed, shortage exception dan rekonsiliasi. |
| Realtime & Notification | Pembaruan owner atas transaksi server yang terkonfirmasi, indikator koneksi internet/backend/WebSocket, notifikasi tanpa duplikasi. Dukungan browser push serta kanal teknisnya perlu validasi perangkat/izin pengguna, sesuai baseline `FR-NOTIF-01`. |
| Audit / Settings / Devices | Audit aktivitas sensitif, pengaturan tenant/struk dasar, aktivasi & pencabutan perangkat, pengaturan operator per lokasi, pergantian operator aman, serta antrean audit login offline yang dikirim ke PostgreSQL dan dihapus lokal hanya setelah server mengakui penerimaan. |
| Quality | Performance, security, data integrity, perlindungan data bisnis sensitif berdasarkan permission, observability, backup/restore, CI & UAT P0. |

**Aturan data sensitif P0 (CAP-55–58):** HPP/harga modal, margin, harga pembelian, dan valuasi stok adalah **Confidential**. Secara default hanya Primary Owner atau pengguna dengan permission eksplisit dan cakupan data yang sesuai yang boleh menerima field tersebut. Kasir tanpa permission biaya **tidak menerima HPP/margin dalam respons API, cache offline, laporan, ekspor, atau event realtime**. Proteksi data ini **bukan paket premium**. Analitik profitabilitas lanjutan boleh menjadi kandidat komersial terpisah tanpa mengurangi proteksi dasar.

**Aturan bisnis pilot yang harus dipertahankan:**

- Gudang dan setiap outlet memiliki saldo stok masing-masing. Gudang tidak otomatis menjadi lokasi kasir.
- Ledger menyimpan referensi dokumen; catatan yang telah diposting tidak diedit/dihapus langsung.
- Transfer normal memiliki saldo transit dan rekonsiliasi penerimaan parsial; transfer cepat atomik dan tetap mempunyai histori.
- Repacking boleh dilakukan di gudang **dan** outlet, menjaga hubungan batch sumber-hasil, jumlah terukur dan selisih; satu sumber batch dan satu SKU hasil per pekerjaan V1 sesuai baseline.
- **Konversi P0 bersifat standar dan dalam dimensi yang sama**: 1 kg = 1.000 g, 1 L = 1.000 mL; `pcs` tidak lintas dimensi. Tidak ada konversi otomatis kg↔mL pada V1. Repacking tetap mencatat konsumsi bahan aktual dan jumlah hasil aktual tanpa mengasumsikan densitas madu.
- Retur, void, dan koreksi memakai transaksi/dokumen pembalik yang terlacak; transaksi sukses tidak dihapus.
- Offline cash sale yang tersimpan tidak hilang diam-diam saat konflik stok terjadi: bukti transaksi dipertahankan dan shortage dicatat untuk rekonsiliasi.
- Transfer internal/repacking **bukan omzet**. Dashboard hanya menghitung sesuai basis transaksi terkonfirmasi dan memperhitungkan retur/void serta status pembayaran.

### 06.3 Di luar scope P0/V1 sebagaimana baseline

- Akuntansi double-entry lengkap dan laporan keuangan formal.
- Purchase order supplier lengkap.
- Gateway QRIS/pembayaran otomatis (nontunai manual online tetap ada).
- Manufaktur penuh, blending multi-batch dan perencanaan produksi kompleks.
- Payroll/absensi, loyalty/membership dan e-commerce.
- Offline receiving, repacking, transfer, dan stock opname.
- SaaS automated billing dan full subscription administration (fondasi entitlement tetap ada).
- Jaminan integrasi printer Bluetooth native untuk semua perangkat; aplikasi native/Flutter juga bukan bagian V1.

### 06.4 Platform dan deployment — REVISED (CAP-51, CAP-54)

- **Tiga frontend statis:** Landing Page → **Cloudflare Pages**; Hi-Ve App PWA (mobile/tablet-first, desktop supported) dan Superadmin App → **Workers Static Assets**. Superadmin tidak perlu PWA/offline V1.
- **Satu backend Go Screaming Modular Monolith** pada VPS/VM melalui HTTPS/WSS dan Cloudflare Tunnel; PostgreSQL adalah sumber data otoritatif, Redis hanya pendukung. Login, stok, transaksi, entitlement dan API berada di backend, bukan Cloudflare edge.
- **V1 tidak memakai Pages Functions, Worker script untuk jalur utama, SSR dinamis, proxy API edge, atau edge auth middleware.** CI harus memeriksa output static-only, SPA/PWA, dan konfigurasi routing sesungguhnya.
- **Batas Cloudflare Free yang diperiksa 18 November 2026:** 10 ms CPU dan 100.000 request/hari berlaku pada **Worker script**, bukan static assets tanpa eksekusi script. Pages juga melayani static assets tanpa memotong kuota Workers. Kuota build, jumlah/ukuran file, dan kebijakan harga **wajib diverifikasi ulang saat deployment**.
- **Risiko deployment:** CORS/cookies/CSRF/OIDC lintas origin, disconnect WebSocket, eviction IndexedDB, dan downtime API Go tetap harus ditangani. Hosting frontend statis tidak menjamin backend selalu tersedia.

**Referensi teknis:** [Workers Static Assets](https://developers.cloudflare.com/workers/static-assets/billing-and-limitations/), [Workers Limits](https://developers.cloudflare.com/workers/platform/limits/), [Pages Limits](https://developers.cloudflare.com/pages/platform/limits/).

## 07. Core Capabilities — APPROVED pada tingkat requirement; rincian teknis OPEN

### 07.1 Capability Registry dan monetisasi — APPROVED (CAP-01–05)

- **CAP-01:** Shared Business Capabilities dengan Product-Specific Presentation; satu logika bisnis otoritatif per domain, tampilan produk dapat berbeda.
- **CAP-02:** Canonical Capability ID berbasis domain seperti `inventory.stock_count`, bukan `retail.inventory.stock_count` dan `warehouse.inventory.stock_count` untuk kemampuan sama.
- **CAP-03:** Cross-Product Mapping: satu submodul dapat digunakan beberapa produk tanpa menduplikasi business logic.
- **CAP-04:** Entitlement capability berlaku **sekali pada tingkat tenant**; penggunaan lintas produk tidak membutuhkan pembelian hak capability sama dua kali, tetapi hak akses produk tetap diperiksa.
- **CAP-05:** **Feature Registry**, **entitlement tenant**, **permission user**, dan **feature flag rollout** merupakan kontrol berbeda. UI hide/show hanya untuk UX; otorisasi wajib di backend.
- ID stabil saat label/menu berubah; tidak perlu membuat satu package Go untuk setiap fitur kecil. Pemilik domain, dependensi, pemetaan produk, dan lifecycle registry perlu dirinci dalam Feature Mapping.
- Kategori komersial dapat meliputi **Core/Included/Billable/Optional**, tetapi harga, paket, dan entitlement feature-level final masih **OPEN**. Infrastruktur seperti Audit/Offline/Realtime tidak otomatis menjadi add-on berbayar.

### 07.2 Tiga produk independen dan Flexible Bundling — APPROVED (CAP-27)

| Produk | Fokus operasional | Sifat capability |
|---|---|---|
| **Hi-Ve POS** | Cashier, checkout, pembayaran manual, sales transaction, receipts, returns/voids. | Memiliki kemampuan minimum Catalog, Locations dan Inventory Core untuk beroperasi tanpa Warehouse. |
| **Hi-Ve Retail** | Pengelolaan toko/outlet, katalog, harga, stok outlet dan pelaporan retail. | Capability Sales/Cashier **dapat disertakan melalui entitlement**, memakai domain Sales yang sama dengan POS. Tidak diwajibkan selalu ada pada semua paket. |
| **Hi-Ve Warehouse** | Receiving, repacking, transfer, stock count, adjustment, traceability, laporan inventori. | Tidak wajib berlangganan POS; menggunakan shared Inventory/Locations/Catalog dengan kontrak domain. |

**CAP-27:** Produk merupakan penawaran independen; kemampuan dapat dibundel secara fleksibel tanpa menduplikasi implementasi backend. POS/Retail/Warehouse **bukan** tiga frontend atau tiga backend terpisah. **Matriks included/optional per paket, harga, dan produk mana wajib memiliki submodul tertentu belum final.**

### 07.3 Frontend, backend dan API — APPROVED (CAP-17–20)

| Frontend | Tanggung jawab | Ketentuan |
|---|---|---|
| **Landing Page** | Informasi publik, produk, materi pemasaran, akses aplikasi. | **Cloudflare Pages (static assets)**; tidak menggunakan Pages Functions untuk operasi inti; tidak perlu API bisnis operasional. Kedalaman konten pemasaran dapat berkembang. |
| **Hi-Ve App** | Login Owner/tenant; pengalaman POS, Retail, Warehouse sesuai entitlement dan izin pengguna. | **Workers Static Assets**, PWA responsif; prioritas **mobile, tablet**, lalu desktop/PC tetap didukung. Offline-first hanya untuk operasi inti yang disepakati. |
| **Superadmin App** | Administrasi platform, tenant, Owner, entitlement dan konfigurasi global. | **Workers Static Assets**, frontend terpisah; tidak perlu PWA/offline V1. |

**Satu backend**: Go Modular Monolith dengan Screaming Architecture; PostgreSQL adalah authoritative system of record; Redis merupakan pendukung, bukan pemilik transaksi atau hak akses. API produksi ditujukan untuk **App API** dan **Admin API** dengan otorisasi berbeda. Contoh URL `/api/v1/app/*` dan `/api/v1/admin/*` **belum final**. Browser tetap untrusted client: CORS/Origin tidak dapat menggantikan autentikasi dan otorisasi backend.

### 07.4 Platform Administration — APPROVED (CAP-14–15, 21–23, 28–32)

**Konteks:** Platform Admin adalah principal/otoritas **di luar tenant**, bukan Owner atau user tenant.

- **CAP-14–15:** Superadmin terpisah dari Primary Owner, mengelola tenant, akun Primary Owner dan akses produk/submodul tenant; administrasi tidak memberi akses otomatis ke semua transaksi tenant.
- **CAP-21:** Arsitektur mendukung **beberapa administrator platform dengan role/permission berbeda**, namun pilot dapat beroperasi dengan **satu Superadmin utama**. Role tambahan Support/Billing hanya contoh, tidak otomatis wajib dibuat di V1.
- **CAP-22:** Provisioning tenant dan Primary Owner pada V1 melalui Superadmin; **self-service registration/billing tidak diwajibkan**. Aktivasi Owner mengikuti alur identity yang aman.
- **CAP-23:** `platform.administration` adalah submodul internal Platform, bukan add-on tenant. Koordinasi workflow admin dilakukan di sini; business rules tenancy/entitlement tetap milik domain terkait.
- **CAP-28:** Tenant lifecycle memiliki state konseptual `pending`, `active`, `suspended`, `closed`. Status tenant berbeda dari status produk/subscription. Suspend membatasi operasi baru tanpa menghapus bukti transaksi lokal/riwayat; aturan rekonsiliasi saat perangkat offline masih OPEN.
- **CAP-29:** Satu **Primary Owner aktif per tenant**, dengan mekanisme activation, lock, recovery, dan ownership transfer yang aman/teraudit. Lock account bukan transfer Owner. Prosedur verifikasi transfer masih OPEN.
- **CAP-30:** Entitlement lifecycle berbasis tenant dengan status, masa berlaku/effective date, sumber pemberian hak dan histori perubahan. Grant/revoke ditegakkan backend; dokumen bisnis historis tidak dihapus karena entitlement dicabut. Kebijakan operasi tertunda saat revoke OPEN.
- **CAP-31:** Pisahkan **Global Platform Settings** (provider dan kebijakan teknis/global) dari **Tenant Settings** (identitas toko, struk, preferensi operasional). Secret dan token provider tidak boleh dikelola sebagai teks polos di UI.
- **CAP-32 (REVISED) dan CAP-34:** Hak Superadmin menggunakan platform permission, allowlist/registrasi admin, audit dan least privilege. **V1 cukup Google Sign-In tanpa MFA tambahan yang diwajibkan oleh Hi-Ve.** Ini pengecualian eksplisit terhadap rekomendasi MFA sebelumnya; jangan menulis MFA wajib. Keamanan Google sendiri tidak dikendalikan Hi-Ve.

**Proteksi lintas domain (CAP-55–58):** `identity.permissions` memiliki kebijakan otorisasi untuk akses field/data sensitif; domain sumber (`catalog`, `inventory`, `sales`) tetap pemilik data bisnisnya; `reports` menghitung/menampilkan hanya data yang boleh diakses peminta; `audit.activity` mencatat tindakan sensitif tanpa merekam nilai kredensial. Tidak ada domain baru atau submodul ke-41.

**Ownership:** `platform.administration` mengoordinasikan; `platform.tenancy` owns tenant lifecycle; `platform.entitlements` owns grant/revoke; `identity.*` owns identity/auth; `audit.activity` owns audit; `settings.tenant` owns tenant preferences. Platform audit dan settings global tidak mengizinkan penulisan bebas ke transaksi tenant.

### 07.5 Identity, Google OIDC dan verifikasi kontak — APPROVED (CAP-16, 24–26, 33–34, 40)

- **CAP-24:** Konteks autentikasi, sesi, dan otorisasi **Platform Admin** terpisah dari **Tenant User**. Sesi admin tidak bisa dipakai sebagai sesi tenant dan sebaliknya.
- **CAP-33:** **Google OAuth 2.0 / OpenID Connect (OIDC) langsung tanpa Auth0** untuk login online **Superadmin dan Primary Owner**. Login Google yang berhasil **tidak otomatis membuat atau mengizinkan akun Hi-Ve**; backend mencocokkan akun yang terdaftar dan status tenant/admin.
- **CAP-34:** Untuk Superadmin **tidak ada MFA tambahan yang diwajibkan oleh Hi-Ve di V1**. Kontrol lain tetap berlaku: autentikasi Google, identity linking yang sah, sesi aman, pencabutan akses, rate limit, audit, platform permissions.
- **CAP-25 dan CAP-16:** Verifikasi nomor WhatsApp Owner adalah **verifikasi kontak terpisah dari Google login dan account recovery**. Arsitektur pengiriman memakai provider adapter yang dapat diganti; GoWA/Meta Cloud API belum dipilih. Kewajiban verifikasi nomor pada aktivasi V1, metode OTP, masa berlaku dan recovery masih **OPEN**.
- **CAP-26:** Visibilitas produk/menu tenant berdasarkan product access, entitlement, permission user, cakupan lokasi/data, status sesi dan tenant. **Backend menegakkan semua akses**; feature flag tidak menggantikan entitlement atau RBAC.
- **Rekomendasi teknis untuk validasi implementasi (belum keputusan parameter final):** Authorization Code Flow + PKCE, client/callback terpisah App/Admin, validasi issuer/audience/expiry dan pasangan `issuer + sub`, `state` dan `nonce` bila relevan; backend-managed session dengan cookie aman dan perlindungan CSRF. Alamat email saja bukan identitas eksternal stabil.
- **Keputusan V1 staf:** staf menggunakan **profil + PIN hanya pada perangkat yang teraktivasi**. Login staf dari perangkat pribadi tanpa aktivasi ditunda sebagai kemungkinan pengembangan berikutnya. Staf tidak wajib memiliki Google/email/nomor HP pribadi. Usulan email+password atau nomor HP+password untuk Owner **tidak disetujui** sebagai pengganti Google OIDC; jangan mengimplementasikan credential store password Owner dari eksplorasi.

### 07.6 Identity/Device/Operator Lifecycle — APPROVED (CAP-35–39 + Topik 01–05)

- **Pairing online oleh Owner:** perangkat belum aktif meminta kode sekali pakai; Owner yang login Google pada perangkat pribadinya menyetujui, menetapkan tenant (satu), beberapa outlet yang diizinkan dan operator per outlet. Kredensial Google Owner **tidak disalin ke perangkat kasir**.
- **Satu outlet aktif per sesi:** perangkat multi-outlet hanya dapat berganti outlet **online** setelah otorisasi dan pemeriksaan transaksi pending; offline memakai outlet terakhir yang sah. Penugasan operator tidak menambah permission. Tiap transaksi menyimpan operator sebenarnya, device dan outlet.
- **Login operator:** Owner menentukan **PIN 6 digit** untuk staf V1; operator memilih profil dan PIN pada perangkat teraktivasi, dan sesi sebelumnya dikunci saat pergantian. Owner yang memakai PIN sebagai operator tidak mendapat hak administratif Google. Login staf di perangkat pribadi tanpa aktivasi bukan P0.
- **Proteksi PIN:** PIN plaintext tidak disimpan; backend memakai hash adaptif, perangkat boleh menggunakan verifier offline **terpisah** dengan izin lokal terbatas. Kebijakan awal **5 kegagalan → lockout 15 menit**; keterbatasan terhadap pencurian storage harus diakui. **TTL 72 jam hanya usulan uji**, belum final (`OPEN-001`). Reset PIN/revoke perlu audit dan pembaruan izin saat perangkat kembali online.
- **Batas offline:** hanya operasi **penjualan tunai inti** saat device/operator sebelumnya diotorisasi; receiving, repacking, transfer, adjustment, opname, dan administrasi tetap online. Revoke backend tidak bisa menjamin penghentian seketika jika perangkat masih offline.
- **Histori login:** setiap sukses/gagal/lockout mendapat event ID unik, tanpa nilai PIN; saat online tulis ke backend, saat offline antre **IndexedDB sementara**; kirim idempotent dan hapus lokal **hanya setelah ACK persistensi**. **PostgreSQL menyimpan histori login operasional selama 7 hari**; audit keamanan kritis memiliki retensi terpisah (`OPEN-006`). Kuota browser penuh/eviction tidak boleh disamarkan sebagai sukses sync.
- **Device lifecycle/recovery:** Owner dapat melihat, suspend, revoke, dan mengganti perangkat melalui registrasi baru. PWA tidak mengikat identitas hardware permanen; data pending tidak boleh dihapus diam-diam saat reset/hilang. Owner recovery/relink/transfer memakai verifikasi manual Superadmin (detail `OPEN-004`), bukan OTP WhatsApp saja.

**FR/AC sumber:** `FUNCTIONAL_SPEC.md` §3–4 (`FR-AUTH-*`, `FR-HV-DEV-*`, `FR-HV-PIN-001`, `FR-HV-OPR-001`, `FR-HV-AUD-001`).

### 07.7 Registry Module → Submodule — APPROVED (40 submodul konseptual)

**Perhitungan yang disepakati:** registry sebelumnya **12 modul / 39 submodul** (validated draft); tambahan **`platform.administration` melalui CAP-23** menjadikan struktur konseptual **12 modul / 40 submodul**. Penambahan submodul telah disetujui; klasifikasi produk, per-feature permissions, API contracts, dan paket tetap perlu validasi. Jangan menambahkan tiga kandidat P1 ke angka 40.

| Modul | Canonical Submodule IDs | Jumlah |
|---|---|---:|
| Platform | `platform.tenancy`, `platform.entitlements`, `platform.administration` | **3** |
| Identity | `identity.authentication`, `identity.users`, `identity.permissions`, `identity.offline_access` | 4 |
| Devices | `devices.management`, `devices.operators` | 2 |
| Settings | `settings.tenant` | 1 |
| Audit | `audit.activity` | 1 |
| Catalog | `catalog.products`, `catalog.units`, `catalog.pricing` | 3 |
| Locations | `locations.management` | 1 |
| Inventory | `inventory.balance`, `inventory.ledger`, `inventory.receiving`, `inventory.repacking`, `inventory.transfer`, `inventory.adjustment`, `inventory.stock_count`, `inventory.traceability` | 8 |
| Sales | `sales.cashier`, `sales.transactions`, `sales.payments`, `sales.history`, `sales.returns`, `sales.voids`, `sales.receipts` | 7 |
| Reports | `reports.dashboard`, `reports.sales`, `reports.inventory`, `reports.stock_movements` | 4 |
| Offline | `offline.storage`, `offline.sync`, `offline.reconciliation` | 3 |
| Realtime | `realtime.events`, `realtime.notifications`, `realtime.connection_status` | 3 |
| **TOTAL** | **12 modules** | **40** |

**Catatan:** Tidak ada submodul `locations.access` terpisah. Otorisasi lokasi dikelola `identity.permissions`; batch operasional dikelola `inventory.traceability`. `platform.administration` **internal**, bukan entitlement komersial; tabel **bukan** janji tiap submodul adalah service/package maupun produk yang dijual terpisah.

### 07.8 Pemilik data dan kontrak antarmodul — APPROVED (prinsip)

| Domain | Tanggung jawab inti |
|---|---|
| Platform | Tenancy, entitlement, workflow Superadmin. Entitlement bukan sistem billing penuh. |
| Identity | Google OIDC, sesi, user, RBAC granular, PIN operator, authorization offline; platform-vs-tenant context dipisah. |
| Devices | Device pairing, activation/revocation, multi-outlet association, assignment profil, active context. |
| Catalog | Definisi produk, SKU, kategori/barcode/varian, satuan, harga, kebutuhan batch tracking. |
| Locations | Data master outlet, gudang dan mapping lokasi; policy akses lokasi tetap di Identity. |
| Settings | Preferensi tenant/struk/notifikasi; pengaturan global tetap dimiliki Platform. |
| Inventory | Saldo stok, ledger, traceability batch, receiving, transfer, repacking, stock count dan adjustment. |
| Sales | Cashier, transaction posting, payment status, history, returns, voids, receipts. Perubahan stok melalui kontrak Inventory. |
| Reports | Baca data otoritatif/read model dari domain terkait, tidak memposting transaksi sendiri. |
| Offline | Queue lokal, retry, sync idempotent, rekonsiliasi; kewenangan server tetap di Sales/Inventory. |
| Realtime | Event sesudah commit, monitoring koneksi dan notifikasi tanpa duplikasi, bukan pemilik data transaksi. |
| Audit | Log keamanan/transaksi penting dan event login yang dikirim dari perangkat. |

### 07.9 Kandidat P1 dari Qasir — APPROVED SEBAGAI KANDIDAT (CAP-06–08)

| ID | Capability | Penempatan kandidat | Prioritas | Batas |
|---|---|---|---|---|
| CAP-06 | Cash Sessions / Rekap Kas | `sales.cash_sessions` | **P1** | Tidak wajib bagi sale tunai P0; buka/tutup kas lebih rinci kemudian. |
| CAP-07 | Customer Management | `customers.directory` | **P1** | Kasir P0 tetap bisa menjual kepada pelanggan umum. |
| CAP-08 | Supplier Management | `partners.suppliers` | **P1** | Receiving P0 tetap mencatat asal barang tanpa master supplier lengkap. |

Ketiganya **tidak termasuk** dalam 40 submodul konseptual P0. Kandidat Qasir lain seperti saved transaction, discount management, modifiers, bundles, loyalty, self-order, absensi, ecommerce dan resep lanjutan **belum mengubah P0**.

### 07.10 Product Capability Mapping — APPROVED (CAP-43–46, Topik 09)

**Prinsip:** POS, Retail, dan Warehouse adalah penawaran yang dapat beroperasi secara independen dengan minimum capability; shared business domains tetap satu implementasi. Produk bukan submodul baru, bukan backend baru, dan bukan paket harga final.

| Area kemampuan | Hi-Ve POS | Hi-Ve Retail | Hi-Ve Warehouse |
|---|---|---|---|
| Identity/Tenancy/Devices/Audit | Fundamental | Fundamental | Fundamental |
| Catalog/SKU | Dasar | Utama | Dasar |
| Pricing | Dasar penjualan | Utama | Tidak wajib kecuali di-entitle |
| Locations | Dasar outlet kasir | Utama outlet | Dasar gudang/lokasi |
| Stock balance/ledger | Dasar pengurangan stok | Dasar operasional | Utama |
| Receiving/Adjustment | Dasar terbatas menurut operasi | Opsional/utama sesuai capability | Utama |
| Repacking | Opsional | Opsional | Utama |
| Transfer/Stock opname | Opsional | Utama | Utama |
| Traceability | Berlaku pada SKU terkait | Berlaku pada SKU terkait | Utama |
| Cashier/Transactions/Payments/Returns/Void/Receipts | Utama | Dapat di-bundle melalui entitlement | Tidak wajib |
| Sales/Inventory Reports | Relevan ke penjualan/stok kasir | Utama operasional outlet | Utama stok/mutasi |
| Offline cash sale | Saat Sales aktif | Jika Sales aktif | Tidak wajib |

**CAP-43:** Setiap produk harus memiliki minimum operational capabilities yang cukup agar tidak harus membeli produk lain untuk fungsi dasarnya. **CAP-44:** POS berfokus Sales, Retail berfokus store/outlet operations, Warehouse berfokus inventory operations. **CAP-45:** shared capability lintas produk menggunakan canonical ID dan entitlement satu kali per tenant. **CAP-46:** Retail **dapat** menyertakan Cashier sesuai entitlement, **tidak wajib** berisi seluruh POS.

**Batas komersial yang masih OPEN:** product bundles final, tier/plan, batas jumlah outlet/user/device, klasifikasi `included`/`billable`, harga dan detail feature-level add-on. Jangan menjadikan pembagian paket contoh sebagai kewajiban P0 baru.

### 07.11 Feature Mapping 10A–10D — APPROVED PADA TINGKAT REQUIREMENT (CAP-47)

Ke-40 submodul pada §07.7 telah dipetakan menjadi kemampuan P0 tanpa penambahan domain/package baru. **Perilaku yang telah ditentukan, hubungan ke 57 FR/257 AC, CAP, serta rencana UAT berada di [`FUNCTIONAL_SPEC.md`](FUNCTIONAL_SPEC.md) §3–7**. Coverage pada Functional Spec **tidak membuktikan** setiap aksi fitur/API/permission sudah final (`OPEN-011`).

| Kelompok | Domain | Fokus kemampuan | Jumlah |
|---|---|---|---:|
| 10A | Platform, Identity, Devices, Audit | Provisioning, akses, pairing/PIN, audit | 10 |
| 10B | Catalog, Locations, Inventory | Produk, satuan, lokasi, stok, repacking, transfer | 12 |
| 10C | Sales, Reports | Kasir, pembayaran, retur/void, laporan | 11 |
| 10D | Settings, Offline, Realtime | Preferensi, antrean/sync, notifikasi/koneksi | 7 |

**Batas approval:** Feature mapping disetujui pada tingkat requirement, **bukan** canonical feature/action ID final, payload API, skema, UI detail, aturan costing, atau test yang telah lulus. Kandidat P1 Qasir tetap terpisah. Perlindungan field Confidential wajib diterapkan lintas semua kelompok sesuai §07.12A; detail guard dan test negatif di Functional Spec.

### 07.12 Granular Permissions, approvals & scope — APPROVED (CAP-48 / Topik 11)

- **Custom role** yang bisa diatur Owner, preset Owner/Kepala Outlet/Kasir/Gudang sebagai titik awal (bukan hardcode semua tenant). Backend **deny by default**, terapkan tenant isolation dan prinsip least privilege.
- Permission aksi dapat meliputi `read/create/update/deactivate/manage_permissions` pada Identity; `activate/assign_operators/revoke` Devices; `create/change_price` Catalog; `create/post/approve/adjust` Inventory; `create/return/void/approve` Sales; `read/export` Reports. Ini **action examples**, bukan izin otomatis pada semua role.
- Hak efektif mengharuskan **tenant aktif + user aktif + session sah + entitlement + permission + outlet/location scope + device assignment** bila relevan. Khusus platform admin, konteks dan permission terpisah. Feature flags tidak memberikan hak.
- Operasi berisiko seperti adjustment, stock count posting, retur/void, transfer tertentu, dan perubahan harga sensitif membutuhkan kebijakan approval/otorisasi yang terukur. Preset siapa yang menyetujui per aksi adalah **OPEN**; bukan klaim semua approval punya workflow baru.
- Setiap akses ditolak juga pada API langsung bila tidak berizin, terlepas dari penyembunyian menu.

### 07.12A Sensitive Business Data Protection — APPROVED (CAP-55–58)

| Klasifikasi | Contoh | Kebijakan |
|---|---|---|
| **Public** | Informasi katalog/harga jual publik | Sesuai konteks publikasi |
| **Operational** | Transaksi dan stok operasional | Sesuai tenant, permission, lokasi, entitlement |
| **Confidential** | **HPP, harga modal/beli, margin, valuasi, strategi harga** | Default Primary Owner; staf hanya dengan **izin eksplisit dan cakupan yang sah** |
| **Restricted** | PIN/verifier, token, secret, recovery material | Tidak dikirim atau ditampilkan sebagai nilai mentah |

- **CAP-55–56:** klasifikasi dan **field-level authorization backend** pada baca, ubah, agregasi, dan ekspor; menyembunyikan kolom di UI tidak cukup. Akses menu/katalog tidak memberi `cost.read`. Platform Superadmin tidak otomatis berhak melihat HPP tenant.
- **CAP-57–58:** audit akses/ekspor sensitif yang berisiko dan perubahan biaya/permission tanpa mencatat rahasia; minimisasi payload berlaku pada API, search, reports, export, realtime, cache/IndexedDB dan logging. **Kasir tanpa izin tidak boleh menerima HPP/margin**, termasuk dalam respons/asset offline; perlindungan dasar ini **P0 untuk semua paket**, bukan add-on.
- **Penyimpanan V1:** HPP numerik presisi di PostgreSQL dengan kontrol akses server/database dan backup, **tanpa enkripsi kolom/aplikasi khusus untuk HPP**. **Risiko residual:** pemegang akses DB tinggi atau pencuri dump tak terlindungi masih berpotensi membaca HPP; field guard API tidak melindungi dump. Metode HPP/costing tetap `OPEN-002`; tidak otomatis mewajibkan engine margin lanjutan P0.

**Detail perilaku dan uji negatif:** `FUNCTIONAL_SPEC.md` (`FR-HV-ACL-002`, `FR-HV-AUD-002`, `FR-HV-DATA-001`); retensi critical audit `OPEN-006`.

### 07.13 Operational Rules & Exceptions — APPROVED (CAP-49 / Topik 12)

- **Units:** P0 hanya konversi standar dalam dimensi sama, **kg↔g, L↔mL**, dan `pcs`; tanpa konversi otomatis kg↔mL. Semua jumlah/uang menggunakan presisi yang aman.
- **Inventory:** ledger dan saldo berdasarkan posting atomik/idempotent; receiving draft bukan stok; transfer normal/cepat mengelola transit/selisih; opname butuh otorisasi sebelum koreksi; repacking menggunakan satu batch sumber, satu SKU hasil dan kuantitas aktual di gudang/outlet.
- **Sales:** penjualan tunai offline berstatus pending sebelum ACK, tidak dihitung sebagai konfirmasi server; retur/void/reversal mempertahankan histori; transfer/repacking bukan omzet. Pembayaran nontunai manual hanya online V1.
- **Exceptions:** jangan hapus bukti transaksi pending ketika shortage, suspend, revoke atau entitlement expired; penanganan posting saat konflik masih `OPEN-007/008`. Batas approval `OPEN-003` dan costing `OPEN-002/014` tidak boleh diputuskan agent.
- **Audit dan data sensitif:** histori login PostgreSQL 7 hari dan antrean IndexedDB sementara; field Confidential hanya untuk principal berizin (§07.12A), tanpa enkripsi khusus HPP V1.

**Rincian FR/AC dan skenario:** `FUNCTIONAL_SPEC.md` §3–7; pengecualian P0 yang tidak termasuk ada di §06.3.

### 07.14 UX & user flows — APPROVED (CAP-50 / Topik 13)

- **Landing:** halaman publik statis untuk informasi bisnis/produk; tidak menjalankan admin/tenant business logic.
- **Hi-Ve App:** Owner Google login pada perangkat pribadinya untuk mengelola tenant; perangkat bersama belum teraktivasi menampilkan pairing; setelah pairing tampil outlet aktif dan daftar operator sesuai assignment; operator masuk PIN; workspace menyesuaikan permission dan entitlements (POS/Retail/Warehouse).
- **Superadmin App:** login Google terpisah, dashboard/provisioning tenant, Owner lifecycle/recovery, entitlement, settings global dan audit platform; tidak perlu PWA/offline V1.
- **Prioritas desain:** mobile/tablet-first, desktop tetap didukung, navigasi singkat, tombol dan target sentuh nyaman, pencarian kasir cepat, operator/outlet selalu terlihat jelas saat transaksi.
- **Indikator koneksi dibedakan:** koneksi jaringan, backend reachability, status WebSocket, transaksi `pending/synced/failed/needs_review`, waktu sync terakhir. Sukses menyimpan lokal **tidak boleh** dipresentasikan sebagai sukses backend.
- **Batas frontend:** SPA/PWA statis di Cloudflare, API hanya lewat backend Go; CDN tidak mengautentikasi atau mengotorisasi operasi bisnis secara otoritatif.

### 07.15 Platform & SaaS lifecycle — APPROVED (CAP-40–42 / Topik 06–08)

- **CAP-40:** Tenant melalui Superadmin dengan status `pending/active/suspended/closed`; tepat satu Primary Owner aktif; provisioning/activation/transfer/recovery/reaktivasi teraudit. Recovery akun Google Hi-Ve dilakukan melalui verifikasi manual Superadmin pada V1, bukan OTP WhatsApp saja.
- **CAP-41:** Entitlement per tenant mencakup produk/submodul/feature khusus bila perlu, effective/expiry, status dan audit. Revocation/expiry langsung membatasi **operasi baru** menurut waktu efektif di server; histori tidak dihapus. Grace period opsional **OPEN**, billing otomatis **bukan P0**. Operasi offline yang terjadi saat perubahan hak dikirim untuk rekonsiliasi berdasar bukti status otorisasi, bukan otomatis di-post.
- **CAP-42:** Global Platform Settings (kebijakan platform/provider/security dan feature flags) terpisah dari Tenant Settings (profil/struk/notifikasi/timezone/lokasi default). Secrets harus dikelola sebagai rahasia, bukan nilai polos yang dapat dilihat bebas. Superadmin tidak otomatis mendapat akses baca/tulis bebas transaksi tenant.

## 08. Success Criteria — APPROVED sebagai standar target P0; angka wajib diuji (CAP-52–53)

### 08.1 Target NFR awal (bukan klaim benchmark)

Baseline historis menetapkan **14 NFR**. Target di bawah adalah **target penerimaan pilot untuk diuji**, bukan bukti sistem mencapai target. Sebelum penguncian deployment, perangkat uji, kondisi jaringan, dataset, dan metode pengukuran p95/latency harus dijelaskan.

| ID | Kategori | Target/quality gate awal |
|---|---|---|
| NFR-01 | Respons keranjang | p95 **≤ 250 ms** pada perangkat uji |
| NFR-02 | Selesai transaksi online | p95 **≤ 3 detik** pada jaringan uji stabil |
| NFR-03 | Simpan transaksi tunai offline | p95 **≤ 1 detik** di storage lokal perangkat uji |
| NFR-04 | Dashboard realtime | Transaksi terkonfirmasi tampil **≤ 5 detik** jika koneksi stabil |
| NFR-05 | Kapasitas pilot | ≥ 10 user terdaftar, 3 outlet, 1 gudang, dengan transaksi simultan |
| NFR-06 | Histori/pagination | Page/cursor efisien, contoh default 50 baris |
| NFR-07 | Integritas bisnis | Tidak ada posting duplikat, stok parsial atau double-consume pada concurrency test |
| NFR-08 | Keamanan API | AuthN/AuthZ, tenant isolation, backend enforcement, deny by default, serta uji negatif atas field Confidential dan kebocoran lintas tenant/role |
| NFR-09 | Backup | Backup terjadwal, pemantauan kegagalan dan **uji restore** sebelum produksi |
| NFR-10 | Observability | Error, latensi, antrean sync, kegagalan background tasks, audit event delivery |
| NFR-11 | UX | Operator baru mampu menyelesaikan alur kasir setelah orientasi singkat; diuji lapangan |
| NFR-12 | Kompatibilitas | Android Chrome dan iOS/iPadOS Safari/Home Screen sesuai device pilot |
| NFR-13 | Waktu | Timestamp konsisten; laporan sesuai timezone outlet/tenant yang dipilih |
| NFR-14 | Audit & histori | RBAC, pencarian berindeks; perubahan/ekspor sensitif terlacak; critical audit tidak hilang karena kuota paket |

**Tambahan kebijakan audit terbaru (di atas baseline):** login operational history di PostgreSQL **7 hari**, IndexedDB antrean offline saja; retensi critical audit server terpisah dan masih perlu diputuskan. **72 jam offline authorization adalah usulan uji, bukan NFR final.**

### 08.1A Product Success Metrics — APPROVED pada kategori/metode ukur, target numerik perlu bukti

Evaluasi produk mengikuti praktik aplikasi **yang telah beroperasi di produksi** sebagai pembanding kualitatif, namun **bukan menyalin angka kompetitor yang tidak diverifikasi**. Target final harus dikalibrasi dari pilot, observasi operator, telemetry yang sesuai privasi, kapasitas perangkat dan pengujian terukur. Semua angka NFR pada 08.1 tetap **target awal**, bukan capaian.

| KPI | Definisi & metode pengukuran | Gate awal / status |
|---|---|---|
| PM-01 — Keberhasilan checkout | Rasio checkout yang selesai sah tanpa kesalahan aplikasi terhadap percobaan valid; pisahkan online/offline, error jaringan dan pembatalan pengguna | Metode **APPROVED**; ambang numerik **PENDING** baseline pilot |
| PM-02 — Waktu alur kasir | Durasi dari mulai checkout hingga konfirmasi lokal/server yang relevan; laporkan median dan p95 per tipe perangkat/jaringan | Metode **APPROVED**; tidak disamakan dengan NFR-02 tanpa konteks |
| PM-03 — Akurasi dan rekonsiliasi inventori | Selisih stok sistem/fisik yang terukur serta jumlah exception unresolved menurut lokasi/SKU/periode | Metode **APPROVED**; ambang berdasarkan data operasional |
| PM-04 — Keberhasilan sinkronisasi | Persentase transaksi offline valid yang mencapai ACK server, waktu hingga ACK, jumlah retry dan konflik unresolved | Metode **APPROVED**; **tidak boleh menganggap pending sebagai synced** |
| PM-05 — Kemandirian operator | Persentase operator uji yang menyelesaikan alur kasir/receiving sesuai izin tanpa bantuan setelah orientasi singkat | Metode **APPROVED**; sampel/metode uji harus dicatat |
| PM-06 — Kesiapan multi-outlet & reliability | Seluruh lokasi pilot menuntaskan skenario P0; pantau insiden kritis kehilangan/duplikasi transaksi, isolasi tenant dan stok | **Zero known critical integrity defects** sebagai gate kualitas; durasi observasi pilot perlu disepakati |

**Interpretasi:** Kategori KPI, cara ukur, dan pendekatan berbasis pembuktian disetujui; **hasil, baseline produksi dan target angka yang belum diukur tidak diklaim APPROVED maupun PASSED**. Implementasi observability/analytics tidak otomatis menambah fitur berbayar atau modul baru. Rincian data pengukuran dan quality gates akan dicantumkan di `TESTING.md`/rencana UAT.

### 08.2 Quality gates arsitektur, keamanan, dan deployment

- **Frontend** harus menghasilkan static assets: **Landing = Cloudflare Pages; App dan Superadmin = Workers Static Assets**. Uji routing/SPAs bila digunakan, asset caching dan PWA/service worker pada Hi-Ve App, responsif dan kondisi browser storage penuh. **Tidak ada Worker script/Pages Functions di request path utama V1**; CI memverifikasi dengan konfigurasi deploy yang nyata, bukan hanya asumsi.
- **CPU Free Worker 10 ms / 100.000 invocations/hari** berlaku pada Worker scripts, bukan JavaScript/IndexedDB/service worker di browser, tidak juga pada API Go VPS. Jika suatu saat edge script ditambahkan, review arsitektur, usage dan pricing wajib dilakukan.
- **Backend** unit/integration tests Go untuk sales/inventory/identity/tenant, concurrency/idempotency, shared-domain boundaries, API Admin vs App, scope tenant/location dan data-leak negative tests. **Negative field authorization tests** wajib membuktikan HPP, margin, harga beli, dan valuasi tidak muncul pada body API, ekspor, cache offline, log, atau event realtime untuk role tanpa izin.
- **CI architecture**: module boundary, dependency cycle, contract/regression tests, build fail-closed bila ada pelanggaran, tanpa mengganti versi/deployment configuration secara tak terkait.
- **Offline**: koneksi terputus, timeout/retry, restart normal browser, duplicate submission, oversold/shortage, stale local authorization, revoke offline, storage eviction/kuota, reconnection audit flush dan acknowledgement.
- **Realtime**: event hanya sesudah commit, reconnect, no duplicate notification, fallback refresh/polling berizin jika WebSocket gagal.
- **Reliability/Ops**: backup dan restore berhasil diuji, penyimpanan observability sesuai retensi, pemulihan VPS/API, indikator backend unreachable, secrets aman.

### 08.3 Baseline UAT-01 sampai UAT-10 (P0)

| ID | Skenario end-to-end yang wajib diuji |
|---|---|
| UAT-01 | Receiving dan repacking dengan saldo serta relasi batch benar |
| UAT-02 | Transfer cepat tanpa menambah omzet |
| UAT-03 | Transfer parsial dengan selisih yang tetap terlacak |
| UAT-04 | Repacking di outlet tanpa mengubah saldo lokasi lain |
| UAT-05 | Penjualan tunai offline tersimpan sebagai pending |
| UAT-06 | Retry sinkronisasi idempotent: tanpa posting ganda |
| UAT-07 | Konflik stok offline masuk rekonsiliasi, bukti tidak hilang |
| UAT-08 | Pelanggaran permission/lokasi ditolak UI dan API |
| UAT-09 | Retur/opname menulis pembalik/adjustment sah |
| UAT-10 | Omzet konsolidasi outlet akurat, transfer bukan penjualan |

**Pengujian tambahan CAP baru:** OIDC/isolasi admin, pairing, operator multi-outlet, PIN offline, histori login 7 hari, suspend/expiry, realtime, static hosting, dan kebocoran field sensitif wajib divalidasi. **Rencana rinci UAT-HV-01–11 berada di [`FUNCTIONAL_SPEC.md`](FUNCTIONAL_SPEC.md) §7; UAT-HV-11 recovery masih BLOCKED oleh `OPEN-004`.** Ini bukan klaim test PASS.

### 08.4 Definition of Done untuk pilot

P0 **siap pilot** jika semua acceptance criteria FR P0 dipenuhi; 10 UAT baseline dan skenario tambahan kritis telah diuji/lulus; tidak ada bug kritis kehilangan/duplikasi transaksi, perubahan stok parsial, atau kebocoran tenant; uji perangkat lapangan, offline, backup/restore, keamanan dan deployment static-only berhasil; serta metrik NFR dilaporkan jujur terhadap target. **Semua hasil pengujian masih PENDING — tidak ada bukti PASS dari dokumen ini.**

### 08.5 Requirement Traceability & Supersession — aturan wajib untuk spesifikasi implementasi

**Status:** Seluruh 36 FR+AC baseline beserta tambahan 21 FR turunan **sudah dikonsolidasikan** dalam [`FUNCTIONAL_SPEC.md`](FUNCTIONAL_SPEC.md). Matriks berikut tetap menentukan supersession: hanya klausul yang bertentangan yang diganti; AC lama yang tidak bertentangan tetap relevan. AC turunan menunggu review sesuai status masing-masing.

| Sumber lama / FR | Ketentuan versi 04 yang mengungguli | Perlakuan untuk agent |
|---|---|---|
| `FR-PRD-02` — konversi kg→mL menggunakan faktor khusus | **CAP-49:** P0 hanya konversi standar **kg↔g, L↔mL**, `pcs` tetap hitungan; **tanpa konversi massa↔volume otomatis** | **Superseded sebagian** pada AC lintas dimensi; AC presisi angka tetap berlaku |
| Strategi *online-first/offline fallback* pada header `PRDv1.0.md` | Offline-first hanya untuk **operasi kasir tunai inti**; receiving, repacking, transfer, adjustment dan opname tetap online | **Revised** sesuai Scope V1; tidak memperluas operasi offline |
| `FR-AUTH-02` — aktivasi perangkat masih umum | CAP-35–39: pairing online disetujui Owner, profil operator per device+outlet, multi-outlet dengan satu outlet aktif | **Extended**; AC baseline yang tidak konflik tetap berlaku |
| `FR-AUTH-04` — usulan 72 jam otorisasi offline | Verifier PIN lokal terbatas; **72 jam hanya kandidat uji, belum TTL final** | **Pending parameter**, tidak hardcode 72 jam sebagai persyaratan final |
| `FR-AUD-01` dan keputusan lama tentang retensi login lokal | Histori login operasional **PostgreSQL 7 hari**; IndexedDB hanya queue offline, dihapus **setelah ACK**; critical audit punya retensi berbeda | **Revised**; jangan simpan histori login tujuh hari secara sengaja di browser |
| FR/AC auth yang belum membedakan sesi platform | CAP-24, 33–34: Admin vs Tenant sessions terpisah; Google OIDC langsung; **tanpa MFA tambahan wajib Superadmin V1** | **Extended**; jangan menambah Auth0 atau MFA wajib |
| CAP-51/54 versi 03: pilihan frontend host masih OPEN | **v04:** Landing **Cloudflare Pages**, Hi-Ve App & Superadmin **Workers Static Assets** | **Resolved**; tidak ada Pages Functions/Worker script pada jalur inti |
| Data biaya/HPP sebelum CAP-55–58 | Field-level authorization, minimal response, audit/ekspor/cache guard; **tanpa enkripsi kolom/aplikasi khusus HPP V1** | **Extended**; tidak boleh membocorkan HPP atau menambah encryption engine |

**Traceability:** `FUNCTIONAL_SPEC.md` telah menyediakan peta awal `CAP/keputusan → canonical capability → FR-ID → AC-ID → Test-ID/UAT-ID`; **akurasi cakupan dan status Review Required tetap harus divalidasi**. Bukti uji dan penyelesaian kebijakan OPEN terkait masih pending; status **implementation-ready tidak otomatis tercapai**.

## 09. Decisions & References — REGISTER CAP-01–CAP-58

### 09.1 Decision Register

> Seluruh CAP-01 sampai CAP-58 **APPROVED dalam batas keputusan yang tercantum**. Approval keputusan produk tidak mengesahkan detail teknis yang masih OPEN dan **bukan instruksi implementasi**. CAP-06–08 hanya kandidat P1. **CAP-51/54: alokasi hosting per frontend dikunci pada revisi v04 (Landing=Pages; App/Superadmin=Workers Static Assets)**; tidak menambah CAP baru dan bukan izin memakai Worker script.

| ID | Keputusan | Status / batas |
|---|---|---|
| CAP-01 | Shared Business Capabilities & Product-Specific Presentation | APPROVED |
| CAP-02 | Canonical Capability ID berorientasi domain | APPROVED |
| CAP-03 | Cross-Product Mapping tanpa business logic duplikat | APPROVED |
| CAP-04 | Entitlement submodul/capability sekali per tenant | APPROVED |
| CAP-05 | Pisah Registry, Entitlement, Permission, Feature Flag | APPROVED |
| CAP-06 | `sales.cash_sessions` | APPROVED **kandidat P1** |
| CAP-07 | `customers.directory` | APPROVED **kandidat P1** |
| CAP-08 | `partners.suppliers` | APPROVED **kandidat P1** |
| CAP-09 | Pemilik transaksi penjualan `sales.transactions` | APPROVED |
| CAP-10 | Batch operasional milik `inventory.traceability` | APPROVED |
| CAP-11 | Otorisasi lokasi di `identity.permissions`, bukan `locations.access` terpisah | APPROVED |
| CAP-12 | Returns dan Voids dipisahkan | APPROVED |
| CAP-13 | `devices.management` untuk aktivasi/pengelolaan perangkat | APPROVED |
| CAP-14 | Platform Superadmin ≠ Tenant Primary Owner | APPROVED |
| CAP-15 | Superadmin mengelola tenant, Owner dan entitlement | APPROVED |
| CAP-16 | Verifikasi kontak WhatsApp Owner provider-agnostic | APPROVED **prinsip**, provider OPEN |
| CAP-17 | 3 frontend: Landing, App, Superadmin | APPROVED |
| CAP-18 | 1 backend Go Modular Monolith | APPROVED |
| CAP-19 | Hi-Ve App PWA mobile/tablet-first, desktop supported | APPROVED |
| CAP-20 | API App vs Admin dipisah secara kontrak dan otorisasi | APPROVED |
| CAP-21 | Multi-admin ready, satu Superadmin utama pilot | APPROVED |
| CAP-22 | Provision tenant dan Owner via Superadmin V1 | APPROVED |
| CAP-23 | `platform.administration` submodul internal Platform | APPROVED; +1 menjadi **40** konseptual |
| CAP-24 | Sesi/otorisasi Platform Admin dan Tenant User terpisah | APPROVED |
| CAP-25 | Owner verification & recovery terpisah dari login; provider-agnostic | APPROVED **prinsip** |
| CAP-26 | Product visibility & authorization berdasarkan entitlement dan permission | APPROVED |
| CAP-27 | Flexible Product Capability Bundling; Retail dapat memakai Sales/Cashier dari domain sama | APPROVED; harga/paket OPEN |
| CAP-28 | Tenant lifecycle: pending, active, suspended, closed | APPROVED **prinsip** |
| CAP-29 | Primary Owner lifecycle: aktivasi, lock, recovery, transfer yang aman | APPROVED **prinsip** |
| CAP-30 | Entitlement lifecycle: status, periode, audit, otorisasi backend | APPROVED **prinsip** |
| CAP-31 | Global Platform Settings ≠ Tenant Settings | APPROVED |
| CAP-32 | Privileged Superadmin access: platform permission, audit, least privilege | **APPROVED REVISED:** tanpa MFA tambahan wajib V1 |
| CAP-33 | Google OAuth 2.0 / OIDC **langsung tanpa Auth0** untuk Superadmin & Primary Owner | APPROVED |
| CAP-34 | MFA tambahan Superadmin **ditunda**, cukup Google Sign-In V1 | APPROVED |
| CAP-35 | Owner-managed device pairing: aktivasi online dengan kode sekali pakai | APPROVED |
| CAP-36 | Perangkat bersama dengan beberapa operator dan PIN masing-masing | APPROVED |
| CAP-37 | Device identity dan operator identity terpisah, audit operator sebenarnya | APPROVED |
| CAP-38 | PIN offline via verifier lokal yang diperkuat + histori login PostgreSQL 7 hari; IndexedDB queue sementara saat offline | APPROVED; parameter KDF/offline expiry OPEN |
| CAP-39 | Owner assignment profil per device+outlet, multi-outlet, **satu outlet aktif** per sesi | APPROVED; exception pending sync OPEN |
| CAP-40 | Tenant lifecycle, Primary Owner provisioning/recovery/transfer via Superadmin, manual verification pada V1 | APPROVED; detail verifikasi OPEN |
| CAP-41 | Entitlement lifecycle (effective/expiry/revoke/history) & proteksi histori bisnis | APPROVED; grace/exception offline OPEN |
| CAP-42 | Pemisahan Global Platform Settings dan Tenant Settings; rahasia provider dilindungi | APPROVED |
| CAP-43 | Minimum Operational Capabilities setiap produk agar dapat berjalan independen | APPROVED |
| CAP-44 | POS=Sales, Retail=Store/Outlet Operations, Warehouse=Inventory Operations | APPROVED |
| CAP-45 | Shared capabilities lintas produk melalui canonical entitlement tanpa duplikasi | APPROVED |
| CAP-46 | Retail dapat menyertakan Cashier via entitlement, tidak wajib semua POS | APPROVED |
| CAP-47 | Topik 10A–10D: feature mapping tingkat kemampuan P0 bagi 40 submodul | APPROVED tingkat requirement; detail ID/API OPEN |
| CAP-48 | Granular permission, custom role, location scope, backend guards & approval policy | APPROVED prinsip; preset approval final OPEN |
| CAP-49 | Operational Rules/Exceptions; konversi standar dalam satu dimensi, tanpa kg↔mL otomatis V1 | APPROVED; exception tertentu OPEN |
| CAP-50 | User flow dan UX: tiga frontend, mobile/tablet PWA, outlet/operator context dan status sync | APPROVED tingkat requirement |
| CAP-51 | Frontend statis via Cloudflare; **Landing menggunakan Pages**, **Hi-Ve App dan Superadmin menggunakan Workers Static Assets**; tanpa Pages Functions/Worker script untuk jalur inti | **APPROVED REVISED (v04)** |
| CAP-52 | NFR dan Quality Gates baseline sebagai target uji, bukan klaim benchmark; validasi Cloudflare Free | APPROVED sebagai target awal; angka final/tuning OPEN |
| CAP-53 | Acceptance Criteria, 10 UAT baseline, uji tambahan dan DoD pilot | APPROVED tingkat requirement; test execution PENDING |
| CAP-54 | Cloudflare Pages dan Workers Static Assets sama-sama layak untuk aset statis; **pilihan per frontend sudah dikunci di v04**; request aset statis tanpa Worker script tidak memakai kuota Worker script Free; backend Go terpisah | **APPROVED REVISED (v04)** |
| CAP-55 | **Sensitive Business Data Classification**: Public/Operational/Confidential/Restricted; HPP, margin, harga beli dan valuasi bersifat Confidential | **APPROVED** |
| CAP-56 | **Field-Level Authorization & Data Minimization**: backend menegakkan permission per field/data scope; tidak sekadar hide UI | **APPROVED** |
| CAP-57 | **Sensitive Data Access & Change Audit**: audit perubahan, permission sensitif dan akses/ekspor berisiko | **APPROVED**; critical audit retention OPEN |
| CAP-58 | **Sensitive Data Protection Across Channels**: API, laporan, ekspor, realtime, cache/offline, audit/logging | **APPROVED**; tidak menambah modul/submodul |

### 09.2 Decision Tracker (OPEN tidak boleh diasumsikan)

`OPEN-*` adalah **ID pelacakan pertanyaan**, bukan CAP baru. Satu keputusan baru menjadi **CLOSED** ketika rumusannya disetujui/dibuktikan, bukan karena agent membuat default sendiri. Kolom *owner* menunjukkan **peran pengambil keputusan**, bukan penugasan kepada individu tertentu.

| ID | Isu / keputusan yang diperlukan | Pemilik keputusan | Wajib selesai sebelum | Status |
|---|---|---|---|---|
| OPEN-001 | TTL akses offline; 72 jam masih usulan uji, parameter KDF dan lockout tahan serangan lokal | Product Owner + Security/Engineering | Implementasi auth offline | **OPEN — High** |
| OPEN-002 | Metode costing/HPP: harga beli vs unit cost, FIFO/average, snapshot historis, stock valuation | Product Owner + Inventory/Finance | Implementasi costing/margin | **OPEN — High** |
| OPEN-003 | Preset approval untuk adjustment, opname, retur/void, transfer, perubahan harga sensitif | Product Owner + Operations | Implementasi otorisasi posting | **OPEN — High** |
| OPEN-004 | Bukti verifikasi recovery/transfer Owner dan break-glass Superadmin | Product Owner + Security | Implementasi recovery | **OPEN — High** |
| OPEN-005 | Hosting frontend: Landing Pages; App/Superadmin Workers Static Assets | Product Owner | Finalisasi v04 | **CLOSED — APPROVED** |
| OPEN-006 | Retensi critical security audit, purge dan clock-skew, kasus data audit offline hilang sebelum upload | Product Owner + Security/Ops | Implementasi audit retention | **OPEN — High** |
| OPEN-007 | Rekonsiliasi transaksi offline ketika tenant suspended atau device revoked | Product Owner + Operations/Engineering | Implementasi sync/reconciliation | **OPEN — High** |
| OPEN-008 | Entitlement expiry/grace dan tindakan pada operasi tertunda saat revoke | Product Owner + Platform | Implementasi entitlement enforcement | **OPEN — High** |
| OPEN-009 | Kewajiban verifikasi WhatsApp Owner, provider, OTP/rate-limit dan biaya | Product Owner + Security | Implementasi verifikasi WhatsApp | **OPEN — Medium** |
| OPEN-010 | Product bundles, tier, harga, quota tenant/outlet/device/user | Product Owner + Commercial | Penawaran komersial/paket | **OPEN — Medium; tidak blokir pilot** |
| OPEN-011 | ID fitur final, permission action, kontrak API/skema, actor/exception dan test per FR | Product Owner + Engineering | Implementasi tiap fitur terkait | **OPEN — High; FUNCTIONAL_SPEC tersedia, rincian kontrak belum final** |
| OPEN-012 | Target numerik product KPIs dan final NFR berdasarkan perangkat, jaringan, data, dan hasil uji nyata | Product Owner + QA/Operations | Go/no-go pilot/produksi | **OPEN — Medium** |
| OPEN-013 | Kehilangan perangkat, penuh/eviction browser storage dan pemulihan transaksi pending | Product Owner + Operations/Security | Pilot offline | **OPEN — High** |
| OPEN-014 | Botol/tutup/label sebagai SKU persediaan dan rincian costing bahan penunjang | Product Owner + Inventory | Implementasi valuasi/packaging detail | **OPEN — Medium** |
| OPEN-015 | Domain/subdomain, cookies, CSRF, OIDC redirects, konfigurasi Pages dan Workers Static Assets, secrets dan contingency VPS | Engineering + Security/Operations | Deployment produksi | **OPEN — High; provider sudah CLOSED** |
| OPEN-016 | **Review kelengkapan FR/AC hasil konsolidasi, traceability, dan penutupan mismatch/parameter terbuka per requirement**; 36 FR+AC baseline dan 21 FR turunan sudah tersedia | Product Owner + Engineering/QA | Penugasan implementasi pada fitur terkait | **OPEN — Critical (review/verifikasi, bukan ketiadaan file)** |

**Catatan:** login staf lewat perangkat pribadi tetap **di luar V1**, bukan tugas agent untuk mengimplementasikan secara spekulatif. **Enkripsi khusus HPP V1 ditolak** dan tidak menjadi isu OPEN. Billing otomatis tetap P1+.

### 09.3 Referensi aktif dan arsip

- **[`FUNCTIONAL_SPEC.md`](FUNCTIONAL_SPEC.md)** — **tersedia, Review Required**; otoritas detail 57 FR/AC, Supersession dan UAT. Review kelengkapan masih `OPEN-016`.
- **[`backend/AGENTS.md`](backend/AGENTS.md)** dan **[`backend/docs/ARCHITECTURE.md`](backend/docs/ARCHITECTURE.md)** — aturan agent dan batas desain Go; bukan dokumen yang membenarkan klaim source sudah diimplementasikan.
- **Arsip historis opsional:** `PRDv1.0.md` (36 FR P0/AC, 14 NFR, 10 UAT), snapshot `PRDv01.md`–`PRDv04.md`, dan snapshot PRD November 2026; **tidak wajib hadir di repo**. Keputusan aktif mengungguli isi bertentangan sesuai §08.5.
- **Referensi keamanan:** [OWASP API3 Broken Object Property Level Authorization](https://owasp.org/API-Security/editions/2023/en/0xa3-broken-object-property-level-authorization/), [Authorization](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html), [Logging](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html).
- **Referensi hosting:** [Cloudflare Workers Static Assets](https://developers.cloudflare.com/workers/static-assets/billing-and-limitations/), [Workers Limits](https://developers.cloudflare.com/workers/platform/limits/), [Pages Limits](https://developers.cloudflare.com/pages/platform/limits/). Detail kuota diverifikasi saat deployment. Riset Qasir adalah referensi kandidat P1, bukan spesifikasi implementasi.

### 09.3A Keputusan tata kelola dokumen — APPROVED (tidak menambah nomor CAP)

| ID | Keputusan dan batas | Status |
|---|---|---|
| DOC-01 | `PRD.md` menjadi **satu nama kanonis** saat ditempatkan di proyek; `PRDv04.md` adalah nama revisi pada distribusi percakapan; arsip tidak menjadi acuan aktif | **APPROVED** |
| DOC-02 | Seluruh FR P0 beserta acceptance criteria, termasuk delta CAP terbaru, diturunkan ke [`FUNCTIONAL_SPEC.md`](FUNCTIONAL_SPEC.md) yang ditautkan ke PRD | **APPROVED; artefak tersedia, review kelengkapan pending (`OPEN-016`)** |
| DOC-03 | Versi 04 mengungguli isi baseline yang bertentangan sebagaimana **Supersession Matrix** 08.5 | **APPROVED** |
| DOC-04 | Product success metrics berbasis pengukuran operasional/pilot dan pembanding aplikasi produksi yang diverifikasi, tanpa mengarang benchmark | **APPROVED kategori/metode; baseline angka PENDING** |
| DOC-05 | Pilihan deployment **Landing=Cloudflare Pages; App/Superadmin=Workers Static Assets** dan Go backend VPS | **APPROVED; OPEN-005 CLOSED** |
| DOC-06 | *Business Domain Glossary* menjadi referensi istilah bisnis; metodologi costing/HPP yang tidak diputuskan tidak boleh diasumsikan | **APPROVED definisi; metode costing OPEN** |

### 09.3B Business Domain Glossary — APPROVED untuk definisi, detail perhitungan tertentu OPEN

| Istilah | Pengertian yang digunakan Hi-Ve | Status/implikasi |
|---|---|---|
| **Harga beli / harga pemasok** | Harga yang dikutip/ditagihkan pemasok untuk barang sebelum/menurut diskon atau biaya lain yang berlaku | **Bukan otomatis HPP**; data Confidential jika tercatat |
| **Harga modal / unit cost** | Biaya per unit yang menjadi dasar valuasi persediaan/biaya penjualan setelah aturan komponen biaya ditetapkan | **Konsep APPROVED; komponen/metode masih OPEN-002** |
| **HPP / Harga Pokok Penjualan (COGS)** | Biaya persediaan yang dialokasikan pada **unit yang terjual**, bukan hanya harga beli terakhir | **Konsep APPROVED; FIFO vs weighted average/penetapan biaya OPEN-002** |
| **Margin / laba kotor** | Selisih pendapatan penjualan yang relevan dengan HPP terkait; persen margin memerlukan basis yang dinyatakan secara eksplisit | **Confidential; rumus rinci diskon/retur & pembulatan belum final** |
| **Nilai persediaan / stock valuation** | Nilai biaya atas stok yang masih tercatat, berbeda dari omzet dan nilai harga jual | **Confidential; costing OPEN-002** |
| **Omzet bruto / neto** | Metrik nilai penjualan; neto mempertimbangkan diskon/retur sesuai definisi yang sudah dicatat dalam `FR-DASH-02` | **Tidak otomatis sama dengan uang diterima; status pembayaran harus jelas** |
| **SKU** | Unit identitas barang yang dapat dikelola/dijual/dimutasi; satu produk dapat mempunyai banyak SKU | **APPROVED** |
| **Batch / lot** | Kelompok stok dengan identitas asal yang dapat ditelusuri; repacking menghubungkan batch sumber dan output | **APPROVED; dimiliki `inventory.traceability`** |
| **Stock ledger** | Catatan mutasi stok berdasarkan transaksi/dokumen sah dan referensi sumber; bukan saldo yang boleh diedit bebas | **APPROVED; dimiliki Inventory** |
| **Repacking** | Pengemasan ulang yang mengonsumsi kuantitas bahan sumber aktual dan menghasilkan kuantitas SKU hasil aktual | **APPROVED; konversi lintas dimensi otomatis bukan P0** |
| **Stock in transit** | Stok transfer yang belum sah diterima dan belum tersedia untuk dijual di lokasi tujuan/pengirim | **APPROVED** |
| **Pending sync** | Data transaksi lokal yang belum memperoleh acknowledgement penerimaan server | **Bukan transaksi final tersinkron** |
| **Entitlement** | Hak tenant atas produk/capability yang disediakan | **Berbeda dengan user permission dan feature flag** |
| **Permission** | Hak principal melakukan aksi/akses data pada scope tertentu | **Tidak otomatis diberikan oleh entitlement saja** |
| **Device identity / Operator identity** | Registrasi instalasi/perangkat operasional vs orang yang menjalankan tindakan/ transaksi | **Dipisah untuk kontrol akses dan audit** |

**Larangan interpretasi:** Menyebut HPP sebagai *harga beli terakhir*, otomatis menghitung margin, atau membuat FIFO/average tanpa keputusan OPEN-002 **tidak diperbolehkan**. Klasifikasi data Confidential pada CAP-55–58 tetap berlaku walaupun metode costing belum final.

### 09.4 Riwayat revisi

| Dokumen | Tanggal | Cakupan |
|---|---|---|
| `PRDv1.0.md` | 22 Oktober 2026 | Baseline historis functional requirements/AC pilot |
| `PRD.md` | 16 November 2026 | Snapshot awal PRD 9 bagian sampai CAP-20 |
| `PRDv01.md` | 18 November 2026 | CAP-01–39; 12 modul/40 submodul; identitas, device dan offline |
| `PRDv02.md` | 18 November 2026 | CAP-01–54, Topik 01–15, histori login server 7 hari, konversi umum, static Cloudflare hosting netral, NFR/UAT/DoD |
| `PRDv03.md` | 18 November 2026 | CAP-01–58, Sensitive Business Data Protection, field-level authorization, audit dan pembatasan payload/cache; tanpa enkripsi khusus HPP |
| **`PRDv04.md` / kanonis `PRD.md`** | **18 November 2026** | **CAP-01–58 tetap**, dokumentasi untuk agent, supersession, KPI produk, tracker OPEN (OPEN-005 CLOSED), **Landing Pages / App+Superadmin Workers Static Assets**, dan glossary bisnis |
| `PRD.md` (editorial alignment) | 19 November 2026 | Status Functional Spec dan AGENTS terkini, provenance baseline tidak wajib ada di repo, OPEN-016 dipersempit ke review/traceability; **tanpa CAP atau scope baru** |

## Catatan penggunaan dokumen

`PRD.md` ini adalah sumber **keputusan produk dan batas scope**; 36 FR baseline, 21 FR tambahan, AC, pemilik submodul, traceability lengkap dan rencana test berada di [`FUNCTIONAL_SPEC.md`](FUNCTIONAL_SPEC.md). Tidak ada perubahan keputusan CAP, jumlah 12 domain/40 submodul, atau syarat pilot akibat peringkasan ini. **Semua pengujian dan implementasi tetap belum diklaim lulus/selesai; OPEN tidak dianggap telah tertutup.**
