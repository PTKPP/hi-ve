# Hi-Ve Post-Design Backlog

Semua item berikut ditahan untuk Post-Design Review. Referensi node mengarah ke [Hi-Ve.figcraft](../../Hi-Ve.figcraft). Path `UI-*-Review` mengarah ke bukti lokal yang dipertahankan. ID audit dan ID produk tetap berlaku. Item tanpa ID sumber ditelusuri melalui fase dan node.

## Design defects dan batas cakupan

| ID | Temuan yang dipertahankan | Bukti |
|---|---|---|
| D-01 | `Navigation / Item` belum memiliki slot ikon. | UI-07A, node `10163` |
| D-02 | Fixture pembayaran lengkap tersedia untuk total Rp220.000. Overlay menjelaskan batas skenario pada keranjang antara. Fixture tambahan tetap opsi review. | UI-07A/07C, node `10163`, overlay `10407` |
| D-03, D-04 | State penghapusan item lewat stepper pada keranjang multi-SKU dan cakupan stepper Kaliandra di 09/10 masih terbatas. Overlay menjelaskan batas simulasi. | UI-07A/07F, node `10163`, keranjang 09/12, overlay `10413` |
| D-05 | Field pencarian masih berupa frame lokal. Review ekstraksi komponen beserta state. | UI-07A, node `10163` |
| D-06 | Main components modul tersebar pada halaman 06/07/08/16. Review konsolidasi ke library. | UI-07A, node `10163` |
| D-08, UI7B-R06 (R06) | Sejumlah nama layer teks sudah tidak cocok dengan isi. Sebanyak 164 instance masih memakai nama generik `Component 1–4` pada catatan terakhir. | UI-07A/07C, node `10163` dan `10467`, `UI-07B-Review/README.md` |
| D-10 | Cakupan Dark masih representatif. Review semua state operasional, termasuk data panjang. | UI-06/07A, node `9987` dan `10163` |
| UI7B-R07 (R07) | Glyph keypad dan stepper memakai `Heading/20`. Review style glyph yang sesuai. | `UI-07B-Review/README.md`, node `10467` |
| UI7B-R08 (R08) | `Icon / Scan` (`10023`) belum dipakai pada aksi pindai (`641`, `666`). | `UI-07B-Review/README.md`, node `10467` |
| UI7B-R09 (R09) | Line-height paragraf panjang masih rapat pada sampel audit. | `UI-07B-Review/evidence/diff3_out.json`, node `10467` |
| UI7B-R11 (R11) | Lengkapi spesifikasi fokus untuk nav, field, stepper dan kartu produk, serta nama aksesibel pada ikon interaktif. | Halaman 02, `UI-07B-Review/README.md`, node `10467` |

## Requirement gaps

| ID atau asal | Temuan yang dipertahankan | Bukti |
|---|---|---|
| UI-07B, CAP-50 | Anotasi gating capability untuk nav Antrean pada POS online. Verifikasi routing dan akses pada runtime sesuai capability dan rollout. | `UI-07B-Review/README.md`, [Phase 06](../../PHASE_06_REPORT.md), node `9987` |
| OPEN-016 | Audit kelengkapan CAP/FR/AC, UAT dan mismatch yang tersisa. UI-02 dan UI-05 mencatat pemetaan Sales Management, approval/refund dan admin tenant saat implementasi. | [PRD §09.2](../../PRD.md), [Functional Spec](../../FUNCTIONAL_SPEC.md), node `7897` |

## Backend contract gaps

| Asal | Gap atau keterbatasan kontrak yang dipertahankan | Bukti |
|---|---|---|
| UI-05, Sales | Aksi reject final dan jejak audit/state yang sesuai belum ditemukan pada review. Penolakan otorisasi approval membiarkan pengajuan terbuka. | Node `7897`, `9987` |
| UI-03–06, Offline | `GET /offline/review` menyajikan jurnal terbuka dan exception yang belum selesai. Konfirmasi kontrak baca keputusan historis, bukti dan closure. Wiring Offline pada entrypoint standar tetap berada di bawah gate rollout Phase 06–08. | Node `3399`, `5388`, `7897`, `9987`, [Phase 06](../../PHASE_06_REPORT.md) |
| UI-04–06, Inventory | Konfirmasi cakupan detail sumber/lineage per tipe dokumen, rute tersimpan, dan granularitas shortage versus konflik yang sama-sama dapat dipetakan ke `STATE_CONFLICT`. Susut menggunakan `variance_note`. Harga modal pada FR penerimaan belum tercakup pada payload yang ditinjau. Perlakuan biaya dan bahan kemasan mengikuti OPEN-002/014. | Node `5388`, `7897`, `9987` |
| UI-05–06, Tenant | Konfirmasi proyeksi baca staf/perangkat, cakupan audit, dan pengaturan tenant di luar footer struk. | Node `7897`, `9987` |
| UI-05–06, Reports | Verifikasi field dan scope ekspor. CSV mengikuti halaman yang dirender, dengan potensi perubahan snapshot lintas halaman. | Node `7897`, `9987` |
| UI-05, Web Push V1 | Pertahankan at-most-once dengan feed cadangan. Uji dukungan browser/provider target. | Node `7897`, `UI-07C-Review/README.md` |

## Keputusan OPEN

| ID | Keputusan yang dipertahankan |
|---|---|
| D-09 | Set nominal cepat Bayar tunai. Fixture Rp250.000 tidak menetapkan opsi produk. |
| OPEN-002, OPEN-014 | HPP, margin, valuasi, dan perlakuan SKU bahan kemasan. |
| OPEN-003 | Preset approval yang tersisa, termasuk perubahan harga sensitif. Ikuti alur Sales/Inventory yang sudah dikontrakkan. |
| OPEN-004 | Verifikasi manual recovery/transfer Primary Owner dan prosedur break-glass. |
| OPEN-006 | Retensi dan purge audit keamanan kritis. |
| OPEN-009, OPEN-010 | Verifikasi kontak WhatsApp, provider/OTP dan biaya, serta paket, harga dan kuota. |
| OPEN-011, OPEN-012, OPEN-015 | Detail kontrak dan permission per aksi, KPI/NFR target, serta kesiapan ingress, OIDC, SMTP dan operasi produksi yang masih perlu bukti. |
| Brand (UI-01–07F) | Persetujuan logo final dan palet. Keduanya provisional. |

Rujukan keputusan adalah [PRD §09.2](../../PRD.md) dan node `7897`, `9987`, `10163`. Untuk OPEN-001/007/008/013 gunakan batas Phase 06 yang sudah ditetapkan, termasuk lease maksimal 24 jam dan berhenti pada batas entitlement, karantina bukti serta penutupan jurnal. Pertahankan gate UAT untuk kasus nyata terkait perangkat, expiry, reconnect, dan storage. OPEN-005 tercatat CLOSED pada PRD.

## Keterbatasan alat dan bukti yang masih dibutuhkan

| Asal | Batas dan verifikasi lanjutan |
|---|---|
| UI-01.1, UI-06–07F | Pemeriksaan visual mengambil sampel frame. Verifikasi reflow browser, portrait/landscape fisik, safe area, keyboard virtual, data panjang, dan Dark per state. Uji keyboard/tab order, focus trap dialog, pembaca layar, pengumuman status, dan target sentuh di perangkat. Bukti desain di node `9987`, `10091` dan audit final. |
| UI-02–07F | Reachability, trigger, timer, dan tujuan interaksi diperiksa secara struktural. Preview MCP memiliki keterbatasan render/playback dan audit final tidak menjalankan klik-through. Uji lintas surface/role, transaksi API, scanner/barcode, printer/struk dan input nyata. Rujuk node `5388`, `7897`, `9987`. |
| UI-03, Phase 06–08 | UAT Android/iOS, jam, KDF, IndexedDB persistence/eviction, replay/reconnect, jaringan target, ACK dan integrasi ledger/audit masih membutuhkan bukti rollout yang relevan. Rujuk node `3399` dan [Phase 06](../../PHASE_06_REPORT.md). |
| R01 | Generator FigCraft tidak mengekspor `tnum`. Penerapan ulang style dapat menghapus override. Gunakan [mapping CSS handoff](DESIGN_HANDOFF.md#angka-tabular-r01). |
| R10 | Identifikasi field memakai heuristik dan daftar eksplisit. Sembilan multiline tambahan masuk UI-07E. Verifikasi field dengan hierarki lain dan tampilan browser sebelum menyatakan cakupan aksesibilitas menyeluruh. Rujuk `UI-07C-Review/evidence/r10_input_field_ids.json` dan `UI-07E-Review/README.md`. |
| R12 | Backup UI-06 setara secara isi menurut review, dengan 937.223 B akibat simpan pasca smoke test. File UI-06 asli tercatat 937.224 B, sehingga identitas byte asli tidak dapat dibuktikan dari backup ini. Rujuk `UI-07A-Review/README.md` dan `UI-07C-Review/README.md`. |

D-07 (style inheritance) dan R01–R05, R10, R12 tercatat selesai dalam lingkup audit desain atau dokumentasi. Batas alat dan cakupan yang disebut di atas tetap berlaku.
