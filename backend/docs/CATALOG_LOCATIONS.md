# Catalog dan Locations Phase 03

## Master dan satuan

Pada App tenant, gunakan prefix `/api/v1/app/tenants/{tenant_id}/catalog`. Primary Owner memakai sesi Google tenant yang aktif. `PUT /products/{id}` menerima `name`, `category`, dan `revision`. `PUT /skus/{id}` menerima `product_id`, `code`, `variant`, `barcode` opsional, `unit`, `active`, `batch_tracked`, dan `revision`. Client membuat UUID untuk identitas master. Revisi nol membuat record, revisi saat ini menggantinya. Setiap PUT membutuhkan `Idempotency-Key`. Retry yang sah mengembalikan revisi hasil semula. Key yang dipakai untuk input lain, revisi usang, atau benturan barcode menghasilkan 409.

Kategori disimpan sebagai label produk. Satu induk dapat mempunyai beberapa SKU. Kode dan barcode unik per tenant, termasuk pada SKU nonaktif. Barcode adalah teks, sehingga nol awal tetap utuh. Unit, induk dan konfigurasi tracking SKU tetap stabil untuk referensi Inventory dan Sales. Gunakan `GET /products`, `GET /products/{id}/skus`, atau `GET /skus/{id}`. Dua daftar pertama mengembalikan hingga 50 record, diurutkan menurut UUID. Untuk halaman berikutnya kirim ID terakhir pada `after_id`. Master nonaktif masih dapat dibaca oleh pengelola.

Konversi menerima bilangan desimal. `hive.catalog_quantity` mendukung `pcs`, `g`, `kg`, `mL`, `L`. `catalog.QuantityForSKU` menghasilkan teks desimal pada unit SKU aktif. Konversi g/kg dan mL/L memakai faktor 1000 dalam dua arah. Pasangan lintas dimensi dan hasil di luar presisi PostgreSQL ditolak.

## Izin dan harga jual

Perubahan master memakai entitlement online `catalog.products`. Staf memerlukan sesi operator perangkat yang masih aktif dan grant tenant-wide `catalog.products.manage`. Owner memberikan grant melalui RBAC. Untuk staf, jalur PUT produk/SKU memakai prefix `/api/v1/operator/tenants/{tenant_id}/devices/{device_id}/catalog`. Minta challenge `catalog.products.write` dengan konteks, revisi dan `command_digest`. Hitung SHA-256 dalam hex lowercase atas method, escaped path, Idempotency-Key, dan byte body yang persis dikirim, dengan satu LF di antara komponen. Helper browser `catalogCommandDigest` menghasilkan digest tersebut. Setelah proof terpakai, retry memakai challenge baru dan receipt key yang sama. Penerbitan dan pemakaian proof, grant, serta receipt tetap memiliki pemeriksaan masing-masing.

`PUT /skus/{id}/price` pada App menetapkan harga dasar tenant. `PUT /skus/{id}/outlets/{outlet}/price` menetapkan override outlet aktif. Kirim `amount` sebagai string desimal nonnegatif dan `revision` untuk lingkup harga tersebut. Setiap perubahan menambah versi dengan ID, aktor dan waktu server. `GET` pada kedua path dengan akhiran `/history` menampilkan hingga 50 versi terbaru. Gunakan `before_revision` untuk halaman lebih lama. Owner dapat membaca histori SKU nonaktif. Perubahan versi lama, penghapusan dan truncate ditolak oleh database.

Kasir membaca `GET /api/v1/operator/tenants/{tenant_id}/devices/{device_id}/catalog/skus/{id}/price` dengan cookie operator dan proof `context.read` sekali pakai. Outlet berasal dari konteks Devices. Identity memeriksa grant lokasi `catalog.pricing.read`. Hasil hanya memuat `price_id`, `amount`, `revision`, dan `updated_at`. Catalog memilih override outlet, lalu harga dasar jika override tidak tersedia.

Platform memberikan `catalog.pricing` melalui endpoint entitlement yang sama dengan `catalog.products`. Kedua capability mempunyai grant, revocation, dan expiry tersendiri. Owner aktif mengubah harga dan membaca histori. Grant `catalog.products.manage` mengizinkan pengelolaan master dan SKU. Kasir memerlukan grant lokasi `catalog.pricing.read` serta konteks outlet aktif. Pemisahan capability tidak mengubah kebijakan monetisasi. HPP, approval perubahan sensitif dan costing kemasan mengikuti OPEN-002/003/014.

## Lokasi dan kontrak consumer

Locations menerbitkan satu ID lokasi inventori utama untuk setiap lokasi master, termasuk lokasi lama saat upgrade. Trigger yang sama melayani writer dari versi sebelumnya. Pada prefix App tenant gunakan `GET /locations/{id}/inventory-location` untuk membaca mapping, status dan revisi. `PUT /locations/{id}/status` memakai sesi Owner, idempotency key serta body `{status,revision}`. Nonaktivasi mengakhiri konteks operator dalam transaksi yang sama. Reaktivasi mempertahankan ID stok dan memerlukan login operator baru.

Inventory dan Sales memasok transaksi serta konteks tenant/lokasi yang sudah diotorisasi. `catalog.SKUForOperation` mengunci SKU aktif, `catalog.QuantityForSKU` mengonversi pengukuran, `locations.PrimaryInventory` me-resolve node stok dan `catalog.Service.SellingPrice` mengembalikan versi harga jual yang berlaku. Gunakan `outletOnly=true` untuk kasir. Guard memegang lock sampai commit agar archive SKU/lokasi dan harga baru terurut terhadap operasi. Inventory memiliki saldo, batch dan transfer. Sales menyimpan snapshot harga/diskon pada transaksi. Offline dan Reports memiliki sinkronisasi serta proyeksi masing-masing.

## Verifikasi dan upgrade

Migrasi 037–042 diterapkan berurutan pada PostgreSQL disposable dari schema 036. Untuk rollout kelak, beri runtime akses SELECT/INSERT/UPDATE sesuai operasi master, SELECT/INSERT untuk versi harga dan histori lifecycle, SELECT untuk mapping, serta UPDATE status/revisi lokasi. Role migrasi memiliki trigger untuk pembuatan mapping. Ukur waktu lock dan backfill lokasi pada volume representatif sebelum rollout. Compose development tetap memakai prefix 001–028.
