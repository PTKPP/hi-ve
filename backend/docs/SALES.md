# Sales Phase 05

Sales memiliki transaksi, snapshot item, konfirmasi pembayaran, dokumen pembalik, dan pencatatan refund. Inventory memiliki saldo serta ledger stok. Cash checkout, konfirmasi nontunai manual, approval retur dan void masing-masing commit dalam satu transaksi PostgreSQL dengan Inventory. Sales mengunci UUID transaksi di dalam tenant sebelum mengakses dokumen. Catalog mengunci SKU dalam urutan ID untuk snapshot. Inventory mengunci saldo menurut lokasi, SKU, batch dan bucket.

## Checkout online

Operator memakai prefix `/api/v1/operator/tenants/{tenant_id}/devices/{device_id}/locations/{location_id}/sales`. Backend memvalidasi sesi operator, perangkat, dan outlet aktif. Mutasi memerlukan challenge `sales.write` dan proof yang mengikat method, escaped path, `Idempotency-Key` dan byte body persis. Gunakan helper digest yang sudah tersedia. Retry bisnis memakai UUID dokumen yang sama serta challenge baru. Read memakai proof `context.read`.

- `GET /items/search?q=...` mencari nama, kode SKU atau barcode. Harga dan ID versi quote berasal dari API Catalog pricing yang sudah ada.
- `PUT /{id}` menerima `items` berisi `sku_id`, `batch_id` bila tracked, `unit`, `quantity`, `price_id`, dan `discount` per baris. Semua nilai desimal dikirim sebagai string. Gunakan `method=cash` dengan `tendered`, atau `method=manual_non_cash` tanpa `tendered`. Backend memvalidasi quote aktif dan menghitung nilai bersih serta kembalian. Diskon ditetapkan per item. Nominal yang tidak dapat dinyatakan tepat dalam minor unit IDR ditolak sebelum posting. [ISO 4217 List One](https://www.six-group.com/dam/download/financial-information/data-center/iso-currrency/lists/list-one.xml)
- `POST /{id}/confirm-manual` menerima `reference`. Status `pending_manual_confirmation` belum mendebit stok. `manually_confirmed` berarti operator mencatat pembayaran, dengan stok terposting dalam transaksi yang sama. Inventori tetap dapat menolak jika barang sudah habis. `cash_received` berasal dari checkout tunai.
- `GET /` mengambil histori outlet. Filter `sale_id`, `operator_id`, `total`, `from` inklusif, dan `to` eksklusif. Pakai timestamp RFC3339 dan pasangan `before` serta `before_id` dari baris terakhir untuk halaman berikutnya. Limit 50. Detail `GET /{id}` menyertakan dokumen retur/void. `GET /{id}/receipt` mengembalikan snapshot item, versi harga, diskon, label outlet dan footer. Primary Owner memakai prefix `/api/v1/app/tenants/{tenant_id}/locations/{location_id}/sales` untuk histori, struk, dan workflow pembalik yang terotorisasi.

Checkout memerlukan `sales.checkout.post`, entitlement `sales.transactions` dan `sales.payments`, serta entitlement quote Catalog. Diskon memerlukan `sales.transactions.discount`. Pencarian, histori dan struk memiliki capability dan aksi tersendiri. Grant staf berasal dari Identity per outlet. Platform mengelola entitlement canonical tanpa perubahan paket komersial.

## Retur, void dan refund

Pada kedua prefix gunakan `PUT /{sale_id}/returns/{reversal_id}` atau `PUT /{sale_id}/voids/{reversal_id}` untuk request. Isi `reason`. Retur menyertakan `items` yang masing-masing mempunyai `line`, `quantity` dalam satuan snapshot item, dan `saleable` eksplisit. Void membalik seluruh item. Untuk keputusan approval gunakan `POST` ke path request dengan akhiran `/approve` dan body `{"reason":"..."}`. Grant `sales.returns.create/approve` dan `sales.voids.create/approve` dipisah. Staf tidak dapat menyetujui request sendiri. Primary Owner dapat menyetujui sendiri dengan alasan. Void setelah retur terposting ditolak dan retur setelah void juga ditolak. Dokumen pending pembayaran tidak dapat dibalik sebagai transaksi terposting.

Rumus kumulatif per item memakai `floor(kuantitas kumulatif × nilai bersih asli dalam minor unit ÷ kuantitas asli)`. Kurangi total refund item yang sudah diposting untuk mendapat refund baru. Retur terakhir menerima sisa pembulatan. Snapshot asli tetap immutable. Inventory menautkan pembalik ke debit asli, turut memvalidasi batas kumulatif, dan memperbarui bucket tersedia atau non-saleable. Seluruh pembalik tercatat dengan tenant, approver, requester, alasan, perangkat jika relevan, waktu dan dokumen asal.

Approval menghasilkan `pending_manual_refund` untuk refund positif dan `not_required` untuk nol. `POST /{sale_id}/refunds/{reversal_id}/confirm` menerima `reference` dengan izin `sales.payments.refund_confirm`, lalu menyimpan `manually_recorded_completed` dengan metode asli. Operator mencatat bahwa dana sudah dikembalikan secara manual. Integrasi provider dan penyelesaian otomatis berada di luar scope.

## Batas fase

Migrasi aditif 048–049 diterapkan pada PostgreSQL disposable. Compose development tetap maksimum 028. OPEN-003 Sales berlaku sesuai [Issue #14](https://github.com/PTKPP/hi-ve/issues/14). OPEN-002/014 tetap terbuka. Offline dan UI PWA mengikuti Phase 06. Proyeksi laporan dan distribusi event setelah commit mengikuti phase integrasi berikutnya.
