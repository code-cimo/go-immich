# go-immich — Immich-Compatible Backend in Pure Go

**Target produksi:** ODROID-C2 (ARM64, 2 GB RAM, DietPi OS) · **Dev/testing:** Linux x86_64 / WSL / Docker
**Kompatibilitas:** Aplikasi mobile Immich resmi (iOS & Android, API v1.100.x) — tanpa modifikasi client.

Dokumen ini adalah panduan lengkap untuk **operator**: instalasi step-by-step, penggunaan TUI installer, operasi harian (service, log, backup), migrasi dari Immich lama, troubleshooting, dan prosedur pengiriman file debug.

---

## Daftar Isi

1. [Ringkasan Sistem](#1-ringkasan-sistem)
2. [Prasyarat](#2-prasyarat)
3. [Cara Mendapatkan Installer](#3-cara-mendapatkan-installer)
4. [Instalasi Step-by-Step (TUI)](#4-instalasi-step-by-step-tui)
5. [Instalasi Unattended (Tanpa TTY / SSH / Scripted)](#5-instalasi-unattended-tanpa-tty--ssh--scripted)
6. [Panduan Halaman TUI & Field Kustomisasi](#6-panduan-halaman-tui--field-kustomisasi)
7. [Verifikasi Pasca-Instalasi (Handshake iOS)](#7-verifikasi-pasca-instalasi-handshake-ios)
8. [Operasi Harian](#8-operasi-harian)
9. [Konfigurasi Lanjutan (Environment Variables)](#9-konfigurasi-lanjutan-environment-variables)
10. [Legacy Migration Tool (Immich Lama → go-immich)](#10-legacy-migration-tool-immich-lama--go-immich)
11. [Debug Log Export — Kirim Satu File ke Maintainer](#11-debug-log-export--kirim-satu-file-ke-maintainer)
12. [Troubleshooting](#12-troubleshooting)
13. [Backup & Restore](#13-backup--restore)
14. [Struktur Proyek & Development](#14-struktur-proyek--development)
15. [Catatan Keamanan](#15-catatan-keamanan)

---

## 1. Ringkasan Sistem

Satu binary monolitik (Go murni, **tanpa CGO**, database SQLite) yang meniru API server Immich resmi:

| Komponen | Binary | Fungsi |
|---|---|---|
| Server HTTP + Worker kompresi | `immich-go` | API Immich-compatible di port **2283** (auth JWT, upload, gallery, thumbnail, album, server-info) |
| CLI migrasi legacy | `immich-migrate` | Import DB PostgreSQL Immich lama + relink file (mode `symlink`/`copy`, checkpoint resume) |
| **TUI Installer** | `immich-go-installer` | Wizard instalasi multi-arch (amd64/arm64) + export satu file log debug |

Yang **tidak** disertakan (sesuai desain, semua di-report `false` ke client): ML/AI face recognition, CLIP search, OAuth, sharing publik, partner sharing, memories, map view, LivePhoto processing.

Layout hasil instalasi (default):

```
/opt/immich-go/
├── immich-go              # server binary
├── immich-migrate         # CLI migrasi (opsional, bila tersedia)
└── immich-go.env          # kredensial & tuning (permission 0600!)

/data/                     # IMMICH_DATA_DIR
├── immich.db              # SQLite (WAL mode)
├── photos/<user_id>/YYYY/MM/DD/...
├── thumbnails/...
├── encoded/...
└── logs/
    └── immich-go-install-<timestamp>.log   # ← FILE DEBUG YANG DIKIRIM KE KAMI
```

Systemd unit terpasang sebagai `immich-go.service` (`Restart=always`, `Nice=19`, `IOSchedulingClass=idle`).

---

## 2. Prasyarat

**Perangkat target:**
- Linux amd64 atau arm64 (diuji pada ARMv8/ODROID-C2; ARMv7 **tidak** didukung).
- RAM ≥ 1 GB (disarankan 2 GB), penyimpanan sesuai ukuran library foto.
- root atau akses `sudo` (installer perlu menulis `/opt`, `/data`, systemd, sysctl).
- systemd tersedia (atau gunakan `--no-service` untuk jalan manual).

**Aplikasi mobile:** Immich official app (iOS/Android). Pada layar koneksi server isi:
`http://<IP-server>:2283/api`

---

## 3. Cara Mendapatkan Installer

### Opsi A — Unduh binari rilis (direkomendasikan)
Ambil arsip sesuai arsitektur target (`uname -m` → `x86_64` = amd64, `aarch64` = arm64):

```bash
uname -m                                   # cek arsitektur
# contoh: salin folder release ke server
scp immich-go-release-linux-arm64.tar.gz root@<IP-odroid>:/tmp/
ssh root@<IP-odroid>
cd /tmp && tar xzf immich-go-release-linux-arm64.tar.gz && cd immich-go-*
ls   # harus berisi: immich-go-installer, immich-go, immich-migrate
```

> Installer **wajib** berada satu folder dengan binary `immich-go` (dan `immich-migrate` opsional) — ia menyalinnya secara atomik ke `/opt/immich-go`.

### Opsi B — Build sendiri (cross-compile dari laptop dev)

```bash
git clone <repo> && cd go-immich
make build-all        # menghasilkan bin/immich-go-linux-amd64, -arm64, dst.
# atau khusus arm64:
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -ldflags="-s -w -X main.version=1.100.0" -o bin/immich-go-installer cmd/installer
```

---

## 4. Instalasi Step-by-Step (TUI)

Jalankan sebagai root di terminal fisik/SSH dengan TTY:

```bash
cd /path/to/release-folder
sudo ./immich-go-installer
```

Wizard 5 halaman:

1. **Welcome** — menampilkan arsitektur terdeteksi + profil default (arm64 = preset hemat ODROID-C2; amd64 = preset performa). Tekan `Enter`/`y` lanjut.
2. **Setup Form** — semua field bisa dikustom (lihat §6). Navigasi: `↑/↓` antar field, `←/→` atau `Tab` pindah grup, ketik nilai, `Space` toggle boolean, `Ctrl+S`/`Enter` lanjut.
3. **Review** — ringkasan konfigurasi akhir. `e` kembali edit, `y` eksekusi, `n/q` batal.
4. **Progress Live** — 8 langkah berjalan dengan status per langkah:
   `Preflight checks → Create directory layout → Copy server binaries → Write env file → Initialise SQLite database → Apply OS tuning → Install systemd unit → Start & verify service`.
   Langkah gagal = proses berhenti, tapi **debug log tetap ter-flush** dan path-nya ditampilkan di layar.
5. **Done** — ringkasan: URL server, kredensial admin, lokasi DB, dan **lokasi file log debug**. Simpan path log ini.

Instalasi ulang aman dijalankan kapan pun (idempotent): binary di-upgrade in-place, data & DB lama tidak disentuh, kredensial baru ditolak kecuali DB kosong (lihat §10 catatan seeding admin).

---

## 5. Instalasi Unattended (Tanpa TTY / SSH / Scripted)

Bila TUI tidak muncul (mis. `ssh host ./immich-go-installer` tanpa `-t`) atau memang ingin scripted install, gunakan mode unattended. Flag > environment variable > default arsitektur:

```bash
sudo ./immich-go-installer --unattended \
  --install-dir /opt/immich-go \
  --data-dir /data \
  --port 2283 \
  --admin-email admin@rumah.local \
  --admin-password 'Ganti-Pass-Ini!' \
  [--no-service]        # skip systemd (jalankan manual nanti)
```

Via environment (untuk provisioning tool seperti Ansible/cloud-init):

```bash
sudo IMMICH_DATA_DIR=/mnt/hdd/immich \
     IMMICH_INSTALL_DIR=/opt/immich-go \
     IMMICH_PORT=2283 \
     IMMICH_ADMIN_EMAIL=admin@rumah.local \
     IMMICH_ADMIN_PASSWORD='Ganti-Pass-Ini!' \
     ./immich-go-installer --unattended
```

Setelah selesai, cetak path log debug:

```bash
ls -t /data/logs/immich-go-install-*.log | head -1
```

Menjalankan tanpa systemd (`--no-service`):

```bash
set -a; source /opt/immich-go/immich-go.env; set +a
/opt/immich-go/immich-go
```

---

## 6. Panduan Halaman TUI & Field Kustomisasi

| Field | Default (arm64 / amd64) | Keterangan |
|---|---|---|
| Install directory | `/opt/immich-go` | Lokasi binary + `immich-go.env` |
| Data directory | `/data` | Root storage: DB, photos, thumbnails, encoded, logs |
| HTTP port | `2283` | Port API Immich. Divalidasi real-time (bentrok port → error di form) |
| Admin e-mail | `admin@example.com` | Login Immich app. Wajib format email valid |
| Admin password | (min. 8 karakter) | Disimpan bcrypt di DB; ditulis juga ke env (0600) |
| Admin display name | `Administrator` | Nama tampilan di app |
| JWT secret | otomatis (hex acak) | Kosongkan = generator membuat secret baru saat instalasi |
| DB max connections | `5` | Connection pool SQLite (WAL). Jangan naikkan di SBC |
| Worker interval | `15s` (arm64) | Periode polling antrian kompresi/thumbnail |
| Worker max CPU % | `20` (arm64) | Throttling background worker agar HP/user tidak freeze |
| Resize concurrency | `2` (arm64) | Goroutine image-processing paralel (max 2 di SBC) |
| Memory limit MB | `500` (arm64) | Batas soft RSS; LRU thumbnail cache dievict mendekati batas |
| Thumbnail size px | `256` | Ukuran thumbnail gallery |
| Install systemd service | `yes` (butuh root) | Pasang & enable `immich-go.service` |
| Start immediately | `yes` | `systemctl start` + smoke-test endpoint setelah install |
| Apply OS tuning | `yes` (arm64) / `no` (amd64) | sysctl: `vm.swappiness=10`, `vfs_cache_pressure=50`, I/O scheduler `mq-deadline` (hanya diterapkan jika arch = arm64/SBC) |

Semua nilai ini berakhir sebagai variabel `IMMICH_*` di `/opt/immich-go/immich-go.env` dan dapat diubah pasca-instalasi (§9).

---

## 7. Verifikasi Pasca-Instalasi (Handshake iOS)

### 7.1 Cek cepat via curl (dari server atau laptop satu jaringan)

```bash
BASE=http://<IP-server>:2283

# 1) Server info — client Immich menolak server bila ini gagal/tidak match
curl -s $BASE/api/server-info/config | jq .
curl -s $BASE/api/server-info/version | jq .      # expect major=1 minor=100

# 2) Health
curl -s $BASE/api/health

# 3) Login → token
TOKEN=$(curl -s -X POST $BASE/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@rumah.local","password":"Ganti-Pass-Ini!"}' | jq -r .accessToken)
echo "$TOKEN"

# 4) Profil user
curl -s $BASE/api/user-me -H "Authorization: Bearer $TOKEN" | jq .

# 5) Upload uji
curl -s -X POST $BASE/api/assets \
  -H "Authorization: Bearer $TOKEN" \
  -H "x-api-key: $TOKEN" \
  -F "file=@test.jpg" \
  -F "sourceType=mobile-upload" | jq .
```

### 7.2 Dari aplikasi Immich (iOS/Android)
1. Buka app → pilih "Add self-hosted server" / server URL: `http://<IP-server>:2283/api`
2. App harus menampilkan **"Connected to Immich v1.100.x"** tanpa warning.
3. Login dengan email/password admin dari installer.
4. Aktifkan Backup — foto masuk ke Gallery, thumbnail muncul dalam beberapa detik (worker kompresi berjalan latar belakang).

Bila app menolak koneksi → lihat §12, hampir selalu karena salah URL (kurang `/api`) atau port bentrok.

---

## 8. Operasi Harian

```bash
systemctl status immich-go          # kondisi service
journalctl -u immich-go -f          # log runtime realtime
journalctl -u immich-go --since "1 hour ago"

sudo systemctl restart immich-go    # setelah ubah env file
sudo systemctl stop immich-go       # stop rapi (graceful shutdown ≤15 dtk)

# resource check di ODROID-C2
ps -o rss= -p $(pgrep -f '/opt/immich-go/immich-go') | awk '{printf "RSS: %.0f MB\n",$1/1024}'
```

**Mengubah konfigurasi:** edit `/opt/immich-go/immich-go.env` → `systemctl restart immich-go`. Alternatif: jalankan ulang installer (upgrade in-place).

**Upgrade versi:** ganti folder release baru → `sudo ./immich-go-installer` (atau `--unattended`). Data tidak tersentuh.

**Uninstall:**
```bash
sudo systemctl disable --now immich-go
sudo rm /etc/systemd/system/immich-go.service && sudo systemctl daemon-reload
sudo rm -rf /opt/immich-go
sudo rm -rf /data           # ⚠️ HATI-HATI: menghapus seluruh foto & DB
```

---

## 9. Konfigurasi Lanjutan (Environment Variables)

Seluruh variabel dibaca server saat start (default di kurung). Tambahkan manual ke `immich-go.env`:

| Variabel | Default | Fungsi |
|---|---|---|
| `IMMICH_PORT` | `2283` | Bind port HTTP |
| `IMMICH_DB_PATH` | `<data>/immich.db` | Lokasi SQLite |
| `IMMICH_PHOTOS_DIR` / `IMMICH_THUMBNAILS_DIR` / `IMMICH_ENCODED_DIR` | `<data>/…` | Hierarki storage |
| `IMMICH_JWT_SECRET` | (auto-generate saat install) | Kunci tanda tangan JWT. Diganti = semua sesi logout |
| `IMMICH_TOKEN_VALIDITY` | `720h` (30 hari) | Masa berlaku token |
| `IMMICH_READ_TIMEOUT` / `IMMICH_WRITE_TIMEOUT` | `300s` / `600s` | Timeout HTTP (write besar untuk upload video) |
| `IMMICH_SHUTDOWN_TIMEOUT` | `15s` | Budget graceful shutdown |
| `IMMICH_DB_MAX_CONNS` | `5` | Pool SQLite |
| `IMMICH_WORKER_INTERVAL` | `15s` | Interval tick background worker |
| `IMMICH_WORKER_MAX_CPU` | `20` | Target throttle CPU worker (%) |
| `IMMICH_RESIZE_CONCURRENCY` | `2` | Worker pool resize |
| `IMMICH_RESIZE_MAX_WIDTH` | `2048` | Foto lebih lebar dari ini di-resize |
| `IMMICH_RESIZE_QUALITY` | `80` | Kualitas JPEG hasil kompresi |
| `IMMICH_THUMBNAIL_SIZE` | `256` | Panjang sisi thumbnail (px) |
| `IMMICH_MEMORY_LIMIT_MB` | `500` | Batas memori; memicu evict cache LRU |
| `IMMICH_ADMIN_EMAIL/PASSWORD/NAME` | — | Seed admin **hanya saat users table kosong** |

---

## 10. Legacy Migration Tool (Immich Lama → go-immich)

Tool: `immich-migrate` (ikut terpasang di `/opt/immich-go/`). Membaca PostgreSQL Immich lama, memetakan users/assets/exif/albums ke SQLite baru, lalu me-relink file fisik.

```bash
# Mode SYMLINK (HEMAT disk & waktu — REKOMENDASI utama untuk ODROID-C2)
sudo /opt/immich-go/immich-migrate \
  --legacy-dsn "postgres://immich:pass@192.168.1.50:5432/immich?sslmode=disable" \
  --library-root /mnt/old-immich/upload/library \
  --mode symlink

# Mode COPY (reorganisasi file fisik ke /data/photos/..., buffer 32KB)
sudo /opt/immich-go/immich-migrate \
  --legacy-dsn "postgres://..." --library-root /mnt/old --mode copy \
  --batch 500
```

Flag penting:

| Flag | Default | Keterangan |
|---|---|---|
| `--legacy-dsn` | `$IMMICH_LEGACY_DSN` | Koneksi PostgreSQL lama |
| `--library-root` | `/data/legacy-upload` | Akar storage lama (struktur hash `ab/cd/…`) |
| `--mode` | `symlink` | `symlink` (zero-copy) atau `copy` |
| `--batch` | `500` | Rows per transaksi (GC dipicu tiap batch) |
| `--reset-password <pw>` | – | Reset semua password lama ke satu password bcrypt baru |
| `--include-trashed` | false | Ikut impor asset di trash |
| `--force-restart` | false | Abaikan checkpoint, mulai dari nol |
| `--state-file` | `.import_state` | File checkpoint resume |

**Resilience:** proses bisa di-`Ctrl+C`/restart kapan pun — tool melanjutkan dari asset ID terakhir di `.import_state`. File fisik hilang hanya di-log sebagai *warning* dan dilewati; migrasi tidak abort. Setelah selesai: `systemctl restart immich-go`, lalu biarkan worker membangun thumbnail untuk asset yang belum punya preview.

> Catatan seed admin: bila Anda mengimpor user dari DB lama, tabel `users` tidak lagi kosong sehingga kredensial admin installer **tidak** akan dibuat ulang. Login memakai akun hasil migrasi (atau pakai `--reset-password`).

---

## 11. Debug Log Export — Kirim Satu File ke Maintainer

Setiap run installer (sukses maupun gagal) menghasilkan **tepat satu file** artefak diagnostik:

```
<data-dir>/logs/immich-go-install-YYYYMMDD-HHMMSS.log
```

Isinya: fakta host (CPU/RAM/disk/kernel/arch), resolved configuration (**password & JWT di-mask**), trace timestamped tiap langkah instalasi, tail `journalctl` saat kegagalan service, dan status akhir SUCCESS/FAILURE.

**Kapan diperlukan:** instalasi gagal, service tidak mau start, app menolak koneksi, atau sebelum membuka tiket support.

**Cara membuatnya tanpa instalasi ulang** (hanya diagnosis):

```bash
sudo ./immich-go-installer --export-only
# atau tentukan path sendiri:
sudo ./immich-go-installer --export-only --log-file /tmp/diag.log
```

**Cara mengirim:**

```bash
LOG=$(ls -t /data/logs/immich-go-install-*.log | head -1)
echo "$LOG"                                  # catat path-nya
gzip -k "$LOG"                               # opsional, perkecil ukuran
scp "$LOG.gz" operator@support:/incoming/    # atau lampirkan ke tiket/email
```

File ini aman dibagikan — kredensial sudah di-mask. Sertakan juga saat melapor: versi installer, arsitektur (`uname -m`), dan langkah yang dilakukan sebelum error.

---

## 12. Troubleshooting

| Gejala | Penyebab umum | Solusi |
|---|---|---|
| Installer: `port 2283 busy` | Service lain / instance lama | `ss -ltnp \| grep 2283`; hentikan pemakai, atau ubah port di form |
| Installer gagal tulis `/opt` | Dijalankan tanpa sudo | `sudo ./immich-go-installer` (menulis `/opt`, systemd, sysctl) |
| TUI tidak muncul lewat SSH | Tidak ada TTY | `ssh -t host sudo ./immich-go-installer` atau pakai `--unattended` |
| Service `activating/auto-restart` | Env salah / DB rusak / port bentrok | `journalctl -u immich-go -n 50`; cek `immich-go.env`; kirim debug log |
| App Immich "server not compatible" | URL salah | Pastikan berakhiran `/api` : `http://IP:2283/api` |
| Login 401 padahal benar | Password seed tidak aktif (users table sudah terisi) | Instalasi fresh, atau reset via migrasi `--reset-password` |
| Upload video gagal/timeout | `WriteTimeout` terlalu kecil | Naikkan `IMMICH_WRITE_TIMEOUT` (mis. `1800s`) → restart |
| Thumbnail lama muncul | Worker throttled by design | Normal di SBC; cek `journalctl -u immich-go \| grep worker`; naikkan `IMMICH_WORKER_MAX_CPU` sementara |
| RAM tinggi / OOM | Limit terlalu longgar | Turunkan `IMMICH_MEMORY_LIMIT_MB` & `IMMICH_DB_MAX_CONNS`, restart |
| Migrasi lambat | Mode `copy` di SD card | Pakai `--mode symlink`; batasi `--batch`; jalankan malam hari |
| Setelah reboot service mati | Unit tidak enabled | `sudo systemctl enable immich-go` |

**Eskalasi:** kumpulkan debug log (§11) + output `journalctl -u immich-go -n 100` + `uname -a`, lalu kirim ke maintainer.

---

## 13. Backup & Restore

Server memakai SQLite WAL — jangan salin `immich.db` sembarangan saat service hidup.

```bash
# BACKUP (aman):
sudo systemctl stop immich-go
sudo tar czf /backup/immich-go-$(date +%F).tar.gz -C /data .
sudo systemctl start immich-go

# atau snapshot WAL-safe dengan sqlite3 bila tersedia:
sqlite3 /data/immich.db ".backup '/backup/immich-$(date +%F).db'"

# RESTORE:
sudo systemctl stop immich-go
sudo tar xzf /backup/immich-go-2026-10-28.tar.gz -C /data
sudo systemctl start immich-go
```

Mode `symlink` hasil migrasi: backup wajib mencakup storage lama (symlink menunjuk ke sana) — atau konversi ke `copy` sebelum media lama dilepas.

---

## 14. Struktur Proyek & Development

```
go-immich/
├── cmd/
│   ├── server/      # immich-go        (API + worker, graceful shutdown)
│   ├── migrate/     # immich-migrate   (CLI legacy import)
│   └── installer/   # immich-go-installer (TUI Bubble Tea)
├── internal/
│   ├── app/         # orkestrator: DI manual, wiring router, lifecycle
│   ├── config/      # baca env IMMICH_*
│   ├── database/    # SQLite WAL, auto-migration tabel
│   ├── handlers/    # auth, server-info, assets, upload, album, router
│   ├── middleware/  # JWT auth, CORS, async logger
│   ├── models/      # DTO kompatibel OpenAPI Immich
│   ├── repository/  # layer query
│   ├── services/    # business logic
│   ├── worker/      # kompresi/thumbnail (throttled)
│   ├── legacy/      # pembaca PostgreSQL lama (pgx)
│   └── installer/   # engine 8-step, TUI, sysinfo, debuglog, systemd
├── pkg/             # imgutil, storage linker, checkpoint, async logger
├── Makefile         # build-amd64/arm64/docker, run-local
├── Dockerfile       # multi-stage, TARGETARCH (buildx)
└── docker-compose.dev.yml
```

Perintah dev utama:

```bash
make run-local       # jalan langsung di laptop x86_64
make build-all       # bin/immich-go-linux-{amd64,arm64} + installer + migrate
make docker-buildx   # image multi-arch via buildx
```

Deploy manual x86 → ODROID:

```bash
make build-arm64
scp bin/immich-go-linux-arm64 root@odroid:/opt/immich-go/immich-go
ssh root@odroid "systemctl restart immich-go"
```

---

## 15. Catatan Keamanan

- `/opt/immich-go/immich-go.env` berisi secret (JWT + password seed) dengan permission **0600** — jangan pernah di-commit/di-share mentah. Yang boleh dikirim ke support hanyalah **debug log** (§11) yang sudah di-mask.
- Mengganti `IMMICH_JWT_SECRET` memaksa logout semua perangkat.
- Server berbicara HTTP polos; untuk akses internet, tutup di reverse-proxy TLS (Caddy/nginx/Traefik) — jangan expose port 2283 langsung.
- Backup rutin = proteksi utama terhadap ransomware/power-loss di SBC.

---

*Versi dokumen: 2026-10-28 · selaras go-immich v1.100.0. Bila ragu, jalankan `./immich-go-installer --help` dan kirim debug log.*
