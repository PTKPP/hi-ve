# Inventory Phase 04

Inventory memiliki ledger dan saldo per tenant, node persediaan, SKU, batch opsional, serta bucket `available`, `non_saleable`, `in_transit`, dan `discrepancy`. Posting mencatat dokumen dan aktor. PostgreSQL menolak perubahan, penghapusan dan truncate terhadap histori. Idempotensi memakai tenant, jenis posting dan ID dokumen/operasi serta fingerprint input dan aktor. Akses diperiksa sebelum replay. Kunci saldo diambil dalam urutan lokasi, SKU, batch, bucket. Kondisi update menolak pengurangan yang membuat saldo negatif. Seluruh nilai dikonversi ke satuan Catalog dengan numeric desimal.

## Receiving dan repacking online

Pada schema 044 ke atas, Primary Owner memakai sesi tenant aktif dan grant Platform `inventory.receiving` atau `inventory.repacking` sesuai operasi. Capability ini menggunakan mekanisme grant, expiry dan revoke yang sudah ada.

- `PUT /api/v1/app/tenants/{tenant_id}/locations/{location_id}/inventory/receiving/{id}` menyimpan draft. Isi `item` berisi `sku_id`, `batch_id` untuk SKU tracked, `quantity` sebagai string dan `unit`. Isi lainnya `lot`, `origin`, `received_on` dengan format YYYY-MM-DD, dan `non_saleable`.
- `PUT /api/v1/app/tenants/{tenant_id}/locations/{location_id}/inventory/repacking/{id}` menyimpan `source` dan `output` dengan bentuk pengukuran yang sama, `lot` keluaran dan `variance_note` opsional. Keluaran menggunakan SKU yang dikonfigurasi untuk tracking batch. Input dan output diukur terpisah, termasuk jika dimensinya berbeda.
- `POST` ke path dokumen dengan akhiran `/post` memposting draft. UUID dibuat caller. Retry UUID dengan payload draft identik mengembalikan hasil yang sama. Untuk memperbaiki draft gunakan dokumen baru. Draft tidak memengaruhi stok. Mutasi dokumen yang sudah diposting mengikuti proses koreksi.

## Transfer, stock count dan kontrak consumer

Go contract transfer membutuhkan izin posting eksplisit pada sumber dan tujuan untuk dispatch. Penerima harus berizin pada tujuan yang tersimpan. Penerimaan dibatasi sisa dokumen dan memakai UUID operasi terpisah. Transfer normal mendebit stok tersedia dan memasukkannya ke transit. Transfer cepat memindahkan saldo tersedia secara atomik. Selisih tercatat dengan alasan dan masuk bucket discrepancy. Selisih tetap membutuhkan penyelesaian walaupun sisa transit sudah nol.

Draft adjustment menyimpan perubahan yang diminta dan alasan. Stock count menangkap revisi dan saldo awal serta menyimpan pengukuran fisik terpisah. Selisih terhadap snapshot dan indikasi perubahan stok dapat dibaca melalui kontrak Go. Tahap persetujuan, posting koreksi dan penyelesaian discrepancy menunggu OPEN-003.

`inventory.Service` mengharuskan callback otorisasi di dalam transaksi. Caller memeriksa hak aksi, scope lokasi, dan entitlement dengan policy yang berlaku. API Owner tersedia untuk receiving/repacking. Binding operator dan operasi lain memerlukan kontrak otorisasi operasional sebelum route dipublikasikan. `Stock` dan `BatchHistory` memberikan proyeksi terbatas lokasi melalui kontrak Go. Histori menyertakan aktor, sumber dokumen, perubahan, waktu dan bucket.

Sales memanggil `PostSale` dalam transaksi checkout setelah membentuk konteks tenant, aktor dan outlet dari sesi yang terotorisasi. Gunakan ID sale yang sama untuk retry. Inventory hanya mendebit `available`, memvalidasi outlet aktif, SKU aktif, batch dan pengukuran, serta mengaudit mutasi melalui ledger. Sales harus membatalkan transaksi checkout bila Inventory menghasilkan error. Checkout HTTP, snapshot harga/diskon, retur, void, dan penerbitan peristiwa setelah commit berada di jalur integrasi Sales.

OPEN-002 mencakup costing, OPEN-003 mencakup approval dan OPEN-014 mencakup perlakuan bahan kemasan. Compose development menggunakan schema paling tinggi 028. Migrasi 043–046 diuji dengan PostgreSQL disposable.
