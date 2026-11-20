# Inventory Phase 04

Inventory memiliki ledger dan saldo per tenant, node persediaan, SKU, batch opsional, serta bucket `available`, `non_saleable`, `in_transit`, dan `discrepancy`. Posting mencatat dokumen dan aktor. PostgreSQL menolak perubahan, penghapusan dan truncate terhadap histori. Idempotensi memakai tenant, jenis posting dan ID dokumen/operasi serta fingerprint input dan aktor. Akses diperiksa sebelum replay. Kunci saldo diambil dalam urutan lokasi, SKU, batch, bucket. Kondisi update menolak pengurangan yang membuat saldo negatif. Seluruh nilai dikonversi ke satuan Catalog dengan numeric desimal.

## Receiving dan repacking online

Pada schema 044 ke atas, Primary Owner memakai sesi tenant aktif dan grant Platform `inventory.receiving` atau `inventory.repacking` sesuai operasi. Capability ini menggunakan mekanisme grant, expiry dan revoke yang sudah ada.

- `PUT /api/v1/app/tenants/{tenant_id}/locations/{location_id}/inventory/receiving/{id}` menyimpan draft. Isi `item` berisi `sku_id`, `batch_id` untuk SKU tracked, `quantity` sebagai string dan `unit`. Isi lainnya `lot`, `origin`, `received_on` dengan format YYYY-MM-DD, dan `non_saleable`.
- `PUT /api/v1/app/tenants/{tenant_id}/locations/{location_id}/inventory/repacking/{id}` menyimpan `source` dan `output` dengan bentuk pengukuran yang sama, `lot` keluaran dan `variance_note` opsional. Keluaran menggunakan SKU yang dikonfigurasi untuk tracking batch. Input dan output diukur terpisah, termasuk jika dimensinya berbeda.
- `POST` ke path dokumen dengan akhiran `/post` memposting draft. UUID dibuat caller. Retry UUID dengan payload draft identik mengembalikan hasil yang sama. Untuk memperbaiki draft gunakan dokumen baru. Draft tidak memengaruhi stok. Mutasi dokumen yang sudah diposting mengikuti proses koreksi.

## Transfer, stock count dan kontrak consumer

Go contract transfer membutuhkan izin posting eksplisit pada sumber dan tujuan untuk dispatch. Penerima harus berizin pada tujuan yang tersimpan. Penerimaan dibatasi sisa dokumen dan memakai UUID operasi terpisah. Transfer normal mendebit stok tersedia dan memasukkannya ke transit. Transfer cepat memindahkan saldo tersedia secara atomik. Selisih tercatat dengan alasan dan masuk bucket discrepancy. Selisih tetap membutuhkan penyelesaian walaupun sisa transit sudah nol.

Draft adjustment menyimpan perubahan yang diminta dan alasan. Stock count menangkap revisi dan saldo awal serta menyimpan pengukuran fisik terpisah. Selisih terhadap snapshot dan indikasi perubahan stok dapat dibaca melalui kontrak Go. Primary Owner aktif atau staf dengan grant approval di lokasi terkait dapat menyetujui adjustment dan koreksi opname. Approval, audit, dan posting commit bersama. Staf tidak dapat menyetujui dokumennya sendiri. Self-approval Owner dan penyelesaian discrepancy mewajibkan alasan. Tolak koreksi tanpa mengubah saldo melalui endpoint reject. Jika snapshot opname usang, cocokkan ulang stok melalui snapshot dan pengukuran baru. Audit menyimpan ID pengukuran yang disetujui.

`inventory.Service` mengharuskan callback otorisasi di dalam transaksi. Caller memeriksa hak aksi, scope lokasi, dan entitlement dengan policy yang berlaku. API Owner tersedia untuk receiving/repacking. Pada schema 047, API Owner dan operator menyediakan operasi transfer, adjustment, count, dan discrepancy. Operator menggunakan sesi aktif dan `Device-Proof` dengan challenge `inventory.write` untuk mutasi. Digest mengikat method, escaped path, `Idempotency-Key`, serta body menggunakan helper yang sama dengan Catalog. Lokasi yang disebut request tetap harus memiliki grant aksi di Identity. Klaim konteks perangkat tidak memberi grant ke lokasi lain. Read memakai proof `context.read`. Platform mengendalikan entitlement per submodule. `Stock` dan `BatchHistory` memberikan proyeksi terbatas lokasi melalui kontrak Go. Histori menyertakan aktor, sumber dokumen, perubahan, waktu dan bucket.

Gunakan prefix `/api/v1/app/tenants/{tenant_id}/locations/{location_id}/inventory` untuk Owner. Operator memakai prefix `/api/v1/operator/tenants/{tenant_id}/devices/{device_id}/locations/{location_id}/inventory`.

| Route relatif | Isi dan hasil |
|---|---|
| PUT `/transfers/{id}` · POST `/transfers/{id}/dispatch` | Draft berisi `destination_id`, `item`, `quick`. Dispatch cepat memerlukan `inventory.transfer.quick` di kedua lokasi. |
| PUT `/transfers/{id}/receipts/{operation_id}` | Lokasi path adalah tujuan. Kirim `source_id`, `quantity`, `unit`, dan `discrepancy_reason` bila mencatat selisih. |
| PUT `/adjustments/{id}` · POST `/adjustments/{id}/approve` atau `/reject` | Draft berisi `item`, `decrease`, `reason`. Request keputusan berisi `reason`. |
| PUT `/counts/{id}` dan `/counts/{id}/observations/{operation_id}` | Snapshot menerima `items` dengan `sku_id` dan `batch_id`. Pengukuran menerima bentuk item lengkap dan mengembalikan saldo snapshot, fisik, selisih, serta flag perubahan. |
| POST `/counts/{id}/approve` atau `/reject` | Setujui dengan satu ID pengukuran per baris snapshot pada `observation_ids` dan `reason`. Approval menolak snapshot usang dengan `STALE_SNAPSHOT`. |
| POST `/discrepancies/{id}/resolve` | ID berasal dari receipt discrepancy. Kirim `reason` dan `found` eksplisit. Barang ditemukan dipindahkan ke stok tersedia. Barang yang dinyatakan hilang dikeluarkan dari bucket discrepancy. |
| GET `/balances/{id}` atau `/batches/{id}/movements` | Gunakan ID SKU atau batch. Hasil terbatas pada lokasi yang diotorisasi. |


Sales memanggil `PostSale` dalam transaksi checkout setelah membentuk konteks tenant, aktor dan outlet dari sesi yang terotorisasi. Gunakan ID sale yang sama untuk retry. Inventory hanya mendebit `available`, memvalidasi outlet aktif, SKU aktif, batch dan pengukuran, serta mengaudit mutasi melalui ledger. Sales harus membatalkan transaksi checkout bila Inventory menghasilkan error. Checkout HTTP, snapshot harga/diskon, retur, void, dan penerbitan peristiwa setelah commit berada di jalur integrasi Sales.

OPEN-002 mencakup costing dan OPEN-014 mencakup bahan kemasan. Keputusan approval Inventory dicatat pada [Issue #12](https://github.com/PTKPP/hi-ve/issues/12). Compose development menggunakan schema paling tinggi 028. Migrasi 043–047 diuji dengan PostgreSQL disposable.
