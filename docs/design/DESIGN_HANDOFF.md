# Hi-Ve Design Handoff

Gunakan [Design Baseline V1](BASELINE.md) dan indeks pada halaman 17 (frame `10229`). Halaman 01 memuat token, 02 library komponen, 03 fondasi App, 06–11 journey produk, dan 12–16 adaptasi serta matriks responsive. Navigasikan dari baseline mobile dan flow start pada indeks. Halaman 99 adalah lab authoring.

## Design system

- Gunakan koleksi `Hi-Ve tokens · provisional` dengan mode Light dan Dark. Petakan variabel semantik `color/bg/*`, `color/text/*`, `color/brand/*`, `color/border/*`, `color/status/*` dan `color/control/*` ke token aplikasi. Gunakan `color/control/outline` untuk field interaktif. Pertahankan pembedaan status informatif, aksi, dan aksi destruktif. Empat stroke pada set komponen dicatat sebagai pengecualian binding di audit UI-07B/UI-07C. Verifikasi pemetaannya ketika mengimplementasikan komponen.
- Gunakan skala spacing 4/8/12/16/24/32, radius 8/16, dan target sentuh utama minimum 48 px. Ikuti style `Hi-Ve/*` untuk hierarki tipografi serta halaman 01–02 untuk ikon dan elevation. Pertahankan brand provisional.
- Gunakan main components, instances, properties, variants, dan auto layout yang didokumentasikan pada halaman 02 serta 06/07/08/16. Petakan property seperti `Label` ke prop aplikasi dan sinkronkan dengan label visual. Gunakan state matrix untuk loading, empty, error, disabled dan permission denied, serta state domain yang dicantumkan pada journey. Verifikasi fokus dan nama aksesibel sebelum handoff implementasi komponen interaktif.

### Angka tabular (R01)

Untuk nominal, kuantitas, waktu, dan kolom angka yang memakai digit tabular, petakan `features: {tnum: true}` ke CSS berikut. Pastikan font yang dipakai menyediakan fitur OpenType `tnum` dan uji tampilan digit di browser.

```css
font-variant-numeric: tabular-nums;
```

586 layer target mempertahankan binding text style sekaligus override `tnum`. Uji UI-07C menunjukkan penerapan ulang text style dengan `style.apply` menghapus override dan `inspect.code` tidak mengekspor properti CSS tersebut. Pulihkan override setelah penerapan style pada layer angka dan sertakan mapping CSS secara eksplisit. Bukti lokal ada di `UI-07C-Review/evidence/tnum_targets.json` dan bagian R01 pada `UI-07C-Review/README.md`.

## Responsive dan batas aplikasi

- Mobile menjadi baseline, dengan contoh 320, 360, 390, dan 430 px. Gunakan pola portrait dan landscape tablet, termasuk produk dan keranjang berdampingan pada POS. Gunakan sidebar, master-detail, tabel/filter, dan dashboard pada desktop sesuai workspace. Periksa delapan rail landscape yang direkonsiliasi di R04. Gunakan state matrix dan screen representatif untuk adaptasi lintas ukuran. Uji reflow, data panjang, safe area dan keyboard virtual di browser target.
- Pertahankan surface Landing, Hi-Ve App, dan Superadmin beserta batas sesi dan aksesnya. Arahkan operator dengan capability penjualan saja langsung ke POS setelah autentikasi dan konteks yang diwajibkan. Terapkan gating menurut entitlement, permission tindakan dan field, location grants serta konteks perangkat. Otorisasi dan scope data tetap ditegakkan server.
- Gunakan progressive disclosure untuk detail koneksi. Bedakan koneksi, pembayaran, dan sinkronisasi. Konfirmasi simpan lokal sesudah persistensi. Pertahankan identitas transaksi saat retry, dan keluarkan pending offline serta pembayaran yang belum terkonfirmasi dari omzet terkonfirmasi. Offline terbatas pada transaksi tunai yang diizinkan kontrak dan rollout. Posting Inventory tetap online.
- Pertahankan fixture lintas layar dan hubungan dokumen sumber saat mengimplementasikan journey. ID contoh seperti `UTM-00127` adalah label desain untuk UUID. Overlay review berlabel catatan prototipe menjelaskan batas skenario. Implementasikan aksi nyata berdasarkan kontrak yang berlaku.

## Traceability

Pertahankan suffix CAP/FR pada nama layar dan metadata journey. Telusuri keputusan dari [PRD](../../PRD.md), periksa AC, UAT dan supersession di [Functional Spec](../../FUNCTIONAL_SPEC.md), lalu cocokkan kontrak domain dan [arsitektur](../../backend/docs/ARCHITECTURE.md). Catat mismatch atau keputusan tertunda di [Post-Design Backlog](POST_DESIGN_BACKLOG.md). Tautan CAP/FR menunjukkan asal requirement dan membantu perencanaan verifikasi.
