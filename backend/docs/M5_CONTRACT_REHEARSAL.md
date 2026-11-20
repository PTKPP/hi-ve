# Rehearsal kompatibilitas M5 Contract

**Cakupan.** Migrasi `029_m5_contract.sql` telah disetujui untuk promosi ke rantai utama. Pernyataan SQL tetap sama dengan kandidat yang lulus rehearsal. Migrasi hanya melepas `tenant_memberships_principal_id_fkey`. Promosi source tidak memberi izin menjalankannya pada target persisten. Migrator Compose development hanya menyediakan 001–028 ke Tern dan binary API tetap mendukung schema 028. Onboarding dan login PIN-only menunggu persetujuan DV3.

## Menjalankan bukti

Dari `backend/` pada Linux dengan Docker lokal dan Go sesuai `go.mod`:

```sh
go install github.com/jackc/tern/v2@v2.4.3
PATH="$(go env GOPATH)/bin:$PATH" bash scripts/rehearse-m5-contract.sh
```

Script membuat PostgreSQL baru menggunakan image yang dipin, `tmpfs`, dan port loopback acak. Koneksi tersusun dari container disposable yang baru dibuat. Script tidak menerima DSN atau Compose project dan mengabaikan override endpoint Docker. Tern menjalankan 001–028 lebih dulu. Harness menjalankan file 029 yang dipromosikan dari salinan direktori migrasi sementara. 030–032 adalah langkah pemulihan khusus fixture yang dibuat di direktori sementara. Tidak ada perubahan pada ledger migrasi repository. Otomasi CI menjalankan perintah yang sama. [Script rehearsal](../scripts/rehearse-m5-contract.sh) · [Migrasi 029](../migrations/029_m5_contract.sql)

| Binary Go native | Tujuan |
|---|---|
| `19f643882a0e66493fd11052572766acf80b3e13` | Writer legacy sebelum pengaman Owner. SQL membership tidak mengisi kolom Google khusus, sehingga menguji bridge 024. Versi ini hanya dipakai sebagai fixture kompatibilitas. |
| `3e421e11c18f5207d4c68d2345731a349518df54` | Baseline rollback yang dipilih. Sudah mencakup pengaman Owner, pembatasan Google App ke Owner, dan perbaikan proof bootstrap DV2. |
| HEAD checkout | Candidate dibangun dari `cmd/api` pada checkout yang menjalankan rehearsal. Script mencatat commit dan SHA-256 setiap executable. Tidak ada perubahan runtime Go dalam increment ini. |

Binary berkomunikasi melalui API HTTP asli dan memakai role fixture tanpa ownership, DDL, atau hak update version table. Provisioning/login menggunakan issuer OIDC lokal bertanda tangan yang dijangkau melalui proxy loopback khusus. SMTP delivery digantikan oleh pengisian digest undangan fixture setelah API membuat undangan. Provider aktual, SMTP, dan role pada target production berada di luar bukti ini. Pengujian Owner login membutuhkan privilege UPDATE kolom pada `hive.active_tenants` untuk row locking. Role fixture menguji privilege pada jalur yang dipanggil dan tidak menetapkan role production. [Role fixture](../scripts/fixtures/m5/runtime-role.sql)

## Acceptance dan hasil yang diharapkan

- **028 sebelum kandidat dan 029 sesudahnya.** Kedua writer melakukan create, claim, dan aktivasi. Aktivasi paralel lintas binary menghasilkan 200/409 dan satu audit sukses. Receipt bisa diputar ulang lewat binary lain. OIDC dapat dimulai pada satu binary dan dituntaskan pada binary lain. Login Admin/Owner, isolasi tenant, histori Owner, logout, perubahan lifecycle, pencabutan sesi fixture, dan denial OAuth staf terus berlaku.
- **Invarian schema dan data.** Katalog sebelum dan sesudah kandidat berbeda hanya pada FK Google-only membership. Uji menolak principal tanpa anchor, Owner tanpa Google, penghapusan link Google Owner, dan Owner aktif kedua. UUID anggota, link Owner, sesi, event ID dan nilai histori tetap sama.
- **Lock dan kegagalan.** Transaksi penahan `ROW SHARE` menimbulkan lock timeout pada DDL aktual. Injeksi SQL gagal sesudah DDL membuktikan constraint dan ledger tetap pada 028. Setelah blocker dilepas, kandidat dapat diulang hingga 029. Menjalankan kandidat lagi di versi 029 tidak mengulang DDL.
- **Pemulihan.** Setelah seluruh binary dihentikan dan nol orphan terverifikasi, migrasi forward memulihkan FK. Uji berikutnya menerapkan kembali kandidat pada fixture dan menambahkan membership neutral-only via SQL. Upaya memulihkan FK gagal dan Tern membatalkan perubahannya. Candidate dan rollback dijalankan ulang, dengan sesi Owner dan histori tetap terbaca. Binary rollback juga dapat mengubah lifecycle staf neutral-only tanpa mengubah UUID atau membuat Google link.

## Batas dan prosedur pemulihan untuk keputusan berikutnya

1. Sebelum menjalankan migrasi yang telah dipromosikan pada target persisten, peroleh persetujuan khusus target dan verifikasi hanya FK yang ditargetkan. Periksa constraint tervalidasi dan index Owner, rekonsiliasi membership/Google, serta uji runtime grants dan SLA lock pada data representatif. `ACCESS EXCLUSIVE` dapat menunggu transaksi yang sudah membuka tabel membership dan membuat akses berikutnya mengantre. Rehearsal memakai `lock_timeout` 5 detik dan `statement_timeout` 15 detik pada fixture. Tetapkan batas job, window perubahan, pemantauan lock, dan cara menghentikan writer pada target yang disetujui.
2. Jika kandidat timeout atau gagal sebelum commit, hentikan rollout. Baca `tern status` dan periksa FK serta version table. Jalankan ulang hanya setelah blocker selesai. Tern menerapkan transaksi per file.
3. Bila kandidat sudah commit dan hitungan membership tanpa Google adalah nol, hentikan writer terkait dan ulangi rekonsiliasi sebelum pemulihan. Gunakan migrasi forward untuk menambah FK semula dengan `NOT VALID`, lalu validasi dalam transaksi. Pastikan FK tervalidasi sebelum melanjutkan traffic. Mekanisme yang dapat dijalankan ini diuji sebagai langkah 030 pada fixture.
4. Bila data neutral-only sudah ada, hentikan pembukaan profil baru yang terkait. Pertahankan UUID dan histori. Gunakan binary rollback yang sudah terbukti kompatibel atau lakukan roll-forward. Rehearsal memverifikasi baseline rollback untuk fitur yang ada saat ini. Kompatibilitas terhadap PIN bagi operator yang kelak sudah terdaftar memerlukan pengujian dan persetujuan DV3 tersendiri.

Promosi source sudah disetujui. Eksekusi pada target persisten, enablement DV3/PIN-only, merge, dan deployment mengikuti persetujuan masing-masing. [Issue #6](https://github.com/PTKPP/hi-ve/issues/6)
