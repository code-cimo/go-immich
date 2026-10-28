# go-immich — Immich-Compatible Backend in Pure Go

**Target produksi:** ODROID-C2 (ARM64, 2 GB RAM, DietPi OS) · **Dev/testing:** Linux x86_64 / WSL / Docker
**Kompatibilitas:** Aplikasi mobile Immich resmi (iOS & Android, API v1.100.x) — tanpa modifikasi client.

Dokumen ini adalah panduan lengkap untuk **operator**: instalasi step-by-step, penggunaan TUI installer, operasi harian (service, log, backup), migrasi dari Immich lama, troubleshooting, dan prosedur pengiriman file debug.

## Quick Start (TL;DR)

```bash
uname -m                                            # x86_64 → amd64 | aarch64 → arm64
tar xzf immich-go-release-linux-<arch>.tar.gz && cd immich-go-*
sudo ./immich-go-installer                          # wizard TUI: isi form → Review → eksekusi 8 langkah
curl -s http://127.0.0.1:2283/api/server-info/version   # harus membalas JSON versi
# App Immich (iOS/Android) → URL server: http://<IP-server>:2283/api → login admin dari installer
# Ada masalah? → sudo ./immich-go-installer --export-only → kirim SATU file di /data/logs/ ke maintainer
```

---

## Daftar Isi

1. [Ringkasan Sistem](#1-ringkasan-sistem)
2. [Dependensi & Prasyarat](#2-dependensi--prasyarat)
3. [Cara Mendapatkan Installer](#3-cara-mendapatkan-installer)
4. [Instalasi Step-by-Step (TUI)](#4-instalasi-step-by-step-tui)
5. [Instalasi Unattended (Tanpa TTY / SSH / Scripted)](#5-instalasi-unattended-tanpa-tty--ssh--scripted)
6. [Panduan Halaman TUI & Field Kustomisasi](#6-panduan-halaman-tui--field-kustomisasi)
7. [Verifikasi Pasca-Instalasi (Handshake iOS)](#7-verifikasi-pasca-instalasi-handshake-ios)
8. [Penggunaan API — Endpoint yang Didukung](#8-penggunaan-api--endpoint-yang-didukung)
9. [Operasi Harian](#9-operasi-harian)
10. [Konfigurasi Lanjutan (Environment Variables)](#10-konfigurasi-lanjutan-environment-variables)
11. [Legacy Migration Tool (Immich Lama → go-immich)](#11-legacy-migration-tool-immich-lama--go-immich)
12. [Debugging & Debug Log Export](#12-debugging--debug-log-export)
13. [Troubleshooting — Langkah Mengatasi Masalah](#13-troubleshooting--langkah-mengatasi-masalah)
14. [Backup & Restore](#14-backup--restore)
15. [Struktur Proyek, Dependensi Go & Development](#15-struktur-proyek-dependensi-go--development)
16. [FAQ Operator](#16-faq-operator)
17. [Catatan Keamanan](#17-catatan-keamanan)

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

## 2. Dependensi & Prasyarat

### 2.1 Dependensi RUNTIME di perangkat target — NOL

Binari dibangun dengan `CGO_ENABLED=0` (Go murni, statis). **Tidak ada dependensi library yang harus di-install** di ODROID-C2 maupun server amd64 target: tidak butuh libc dinamis, tidak butuh SQLite driver sistem, tidak butuh Node.js/PostgreSQL/Redis.

```bash
# verifikasi hasil build: harus tertulis "statically linked"
file bin/immich-go-linux-arm64
ldd  bin/immich-go-linux-arm64   # → "not a dynamic executable"
```

### 2.2 Prasyarat sistem target

| Kebutuhan | Wajib? | Keterangan |
|---|---|---|
| Linux amd64 atau arm64 (ARMv8) | ✅ | Diuji pada ODROID-C2 (Amlogic S905) & Ubuntu x86_64. ARMv7 / 32-bit **tidak** didukung. Cek: `uname -m` (`x86_64` / `aarch64`) |
| RAM ≥ 1 GB (disarankan 2 GB) | ✅ | Server dibatasi soft-limit default 500 MB (`IMMICH_MEMORY_LIMIT_MB`) |
| Disk cukup untuk library foto | ✅ | Eksternal HDD/SSD via USB lebih disarankan daripada SD card (I/O thumbnail & migrasi) |
| root / `sudo` | ✅ saat install | Installer menulis `/opt`, `/data`, unit systemd, dan sysctl tuning |
| systemd | ⚠️ opsional | Tanpa systemd → jalankan installer dengan `--no-service`, lalu start manual (§5.3). Container tanpa systemd otomatis men-skip step systemd |
| Port TCP bebas | ✅ | Default `2283`; bisa diubah di TUI/env |
| `jq` (opsional) | ❌ | Hanya untuk contoh perintah curl di §7 |

### 2.3 Dependensi BUILD (hanya bila ingin meng-compile sendiri)

| Dependensi | Versi | Fungsi |
|---|---|---|
| Go toolchain | ≥ 1.21 (repo ini: `go 1.25`) | Kompilasi; **tidak perlu** berada di perangkat target |
| Docker + buildx | 0.10+ | Build image multi-arch (`make docker-buildx`) |
| GNU `make`, `tar` | apa pun | Target Makefile & packaging arsip rilis |
| `air` (opsional) | latest | Hot-reload `make run-local` |

Pustaka Go pihak ketiga (semuanya pure-Go, dikelola `go mod`, unduh otomatis saat build):

| Modul | Dipakai oleh | Fungsi |
|---|---|---|
| `github.com/gin-gonic/gin` | server | Router HTTP |
| `modernc.org/sqlite` | server, migrate | Driver SQLite pure-Go (tanpa CGO) |
| `github.com/golang-jwt/jwt/v5` + `golang.org/x/crypto` (bcrypt) | server/auth | JWT & hash password kompatibel Immich |
| `github.com/rwcarlsen/goexif` | upload handler | Ekstraksi EXIF/GPS/tanggal ambil |
| `github.com/disintegration/imaging` + `golang.org/x/image` | worker kompresi | Resize JPEG (thumbnail & kompresi) |
| `github.com/jackc/pgx/v5` | immich-migrate | Pembaca PostgreSQL legacy |
| `github.com/google/uuid` | semua | ID asset/user kompatibel Immich |
| `github.com/hashicorp/golang-lru/v2` | file handler | Cache LRU thumbnail (evict saat mendekati batas RAM) |
| `github.com/charmbracelet/bubbletea` + `lipgloss` | installer | TUI wizard |

### 2.4 Dependensi sumber data lama (untuk migrasi saja)

PostgreSQL server lama Immich harus dapat diakses dari mesin yang menjalankan `immich-migrate` (DSN + folder storage lama ter-mount). Tidak perlu install PostgreSQL baru — hanya koneksi baca.

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

Instalasi ulang aman dijalankan kapan pun (idempotent): binary di-upgrade in-place, data & DB lama tidak disentuh, kredensial baru ditolak kecuali DB kosong (lihat catatan seeding admin di §11).

---

## 5. Instalasi Unattended (Tanpa TTY / SSH / Scripted)

### 5.1 Via flag CLI

Bila TUI tidak muncul (mis. `ssh host ./immich-go-installer` tanpa `-t`) atau memang ingin scripted install, gunakan mode unattended. Prioritas nilai: **flag CLI > environment variable > default arsitektur**:

```bash
sudo ./immich-go-installer --unattended \
  --install-dir /opt/immich-go \
  --data-dir /data \
  --port 2283 \
  --admin-email admin@rumah.local \
  --admin-password 'Ganti-Pass-Ini!' \
  [--no-service]        # skip systemd (jalankan manual nanti)
```

### 5.2 Via environment variable (Ansible / cloud-init)

```text
sudo ./immich-go-installer --unattended   # semua jawaban dari IMMICH_* env di bawah
```

```bash
sudo IMMICH_DATA_DIR=/mnt/hdd/immich \
     IMMICH_INSTALL_DIR=/opt/immich-go \
     IMMICH_PORT=2283 \
     IMMICH_ADMIN_EMAIL=admin@rumah.local \
     IMMICH_ADMIN_PASSWORD='Ganti-Pass-Ini!' \
     ./immich-go-installer --unattended
```

Semua variabel yang dikenali installer: `IMMICH_DATA_DIR`, `IMMICH_INSTALL_DIR`, `IMMICH_PORT`, `IMMICH_ADMIN_EMAIL`, `IMMICH_ADMIN_PASSWORD`, `IMMICH_JWT_SECRET`.

### 5.3 Menjalankan tanpa systemd (`--no-service`)

Bila host tidak punya systemd (container) step systemd otomatis di-*skip*; jalankan manual:

```bash
set -a; source /opt/immich-go/immich-go.env; set +a
nohup /opt/immich-go/immich-go >> /data/logs/server.out 2>&1 &
```

### 5.4 Verifikasi hasil & lokasi log debug

Cetak path log debug:

```bash
ls -t /data/logs/immich-go-install-*.log | head -1
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

Semua nilai ini berakhir sebagai variabel `IMMICH_*` di `/opt/immich-go/immich-go.env` dan dapat diubah pasca-instalasi (§10).

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

Bila app menolak koneksi → lihat §13, hampir selalu karena salah URL (kurang `/api`) atau port bentrok.

---

## 8. Penggunaan API — Endpoint yang Didukung

Semua route terdaftar di `internal/handlers/router.go`. Path lama (`/asset`, singular) dan baru (`/assets`, plural Immich modern) **keduanya dilayani**, jadi versi app mana pun bisa terhubung.

### 8.1 Publik (tanpa token — dipakai app saat discovery & login)

| Method | Path | Fungsi |
|---|---|---|
| GET | `/api/health` dan `/health` | Liveness probe (dipakai juga installer saat smoke-test) |
| GET | `/api/server-info/config` | Konfigurasi kapabilitas server; semua fitur ML/OAuth/map/sharing = `false` |
| GET | `/api/server-info/version` | `{major:1, minor:100, patch:x}` — gerbang kompatibilitas app |
| GET | `/api/server-info` / `/api/server-info/stats` | Statistik ringkas |
| POST | `/api/auth/login` | Body `{email,password}` → `{accessToken, refreshToken, userId, userEmail, name, isAdmin}` |
| DELETE | `/api/auth/logout` | Invalidate sesi (no-op token blacklist; client membuang token) |
| GET | `/api/auth/.info` | Metadata OIDC kosong (app memanggil ini saat init) |
| GET | `/api/admin/onboarding-check` | `{needsOnboarding:false}` agar app langsung ke layar normal |

### 8.2 Terautentikasi (header `Authorization: Bearer <token>` atau `x-api-key: <token>`)

| Method | Path | Fungsi |
|---|---|---|
| GET/PATCH | `/api/user-me` | Profil & preferensi user aktif |
| PUT | `/api/user-me/change-password` | Ganti password (bcrypt re-hash) |
| GET | `/api/user/me` | Alias legacy beberapa build app |
| GET | `/api/user` | Daftar user (admin) |
| POST | `/api/assets` (+ alias `/api/asset/upload`) | Upload multipart; field `file` wajib, `sourceType` opsional. Simpan ke `<data>/photos/<user>/YYYY/MM/DD/…`, ekstrak EXIF/GPS, dedup SHA-256. Duplikat → HTTP **409** |
| POST | `/api/assets/check` (+ `/api/asset/check`) | Cek duplikat sebelum upload (client backup) |
| GET | `/api/asset` & `/api/assets` | Galeri: query `take`, `skip`, `isFavorite`, `isArchived`, `isTrashed`, `type` (IMAGE/VIDEO), sort tanggal |
| GET | `/api/asset/:id` / `/api/assets/:id` | Detail satu asset (DTO penuh + `exifInfo`) |
| DELETE | `/api/asset/:id` / `/api/assets/:id` | Hapus asset (soft-delete/trash) |
| GET | `/api/asset( s)/:id/thumbnail` | JPEG thumbnail, `Cache-Control: public, max-age=31536000, immutable` |
| GET | `/api/asset( s)/:id/original` | Stream file asli, mendukung header `Range` (seeking video) |
| POST | `/api/album` | Buat album |
| GET | `/api/album` / `/api/albums` | Daftar album + jumlah asset |
| GET | `/api/album/:id` | Detail album beserta anggotanya |
| PUT | `/api/album/:id/assets` | Tambah asset ke album `{assets:[id,…], entries:[…]}` |
| DELETE | `/api/album/:id/assets` | Buang asset dari album |

### 8.3 Perilaku yang dijamin ke client mobile

- **Format error** mengikuti NestJS/Immich: `{"statusCode":401,"error":"Unauthorized","message":"Invalid credentials"}` — parser Flutter tidak crash.
- **Waktu** selalu ISO-8601 UTC dengan milidetik (`2026-10-28T03:00:00.000Z`).
- **ID** berupa UUID v4 string, sama seperti skema lama.
- Response `server-info/config` berisi seluruh kunci boolean yang diminta app (meski fiturnya mati) — inilah yang membuat app menerima server tiruan ini.

---

## 9. Operasi Harian

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

## 10. Konfigurasi Lanjutan (Environment Variables)

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

## 11. Legacy Migration Tool (Immich Lama → go-immich)

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

## 12. Debugging & Debug Log Export

### 12.1 Artefak debug utama — SATU file untuk dikirim ke maintainer

Setiap run installer (sukses maupun gagal) menghasilkan **tepat satu file** diagnostik:

```
<data-dir>/logs/immich-go-install-YYYYMMDD-HHMMSS.log
```

Isinya: fakta host (CPU/RAM/disk/kernel/arch), resolved configuration (**password & JWT di-mask**), trace timestamped tiap langkah instalasi (`STEP n/8 START|OK|SKIP|FAIL`), tail `journalctl` saat kegagalan service, dan status akhir SUCCESS/FAILURE.

Membuatnya **tanpa instalasi ulang** (hanya diagnosis, nol perubahan sistem):

```bash
sudo ./immich-go-installer --export-only
# atau tentukan path sendiri:
sudo ./immich-go-installer --export-only --log-file /tmp/diag.log
```

Mengirimnya:

```bash
LOG=$(ls -t /data/logs/immich-go-install-*.log | head -1)
echo "$LOG"                                  # catat path-nya
gzip -k "$LOG"
scp "$LOG.gz" operator@support:/incoming/    # atau lampirkan ke tiket/email
```

File ini aman dibagikan — kredensial sudah di-mask. Sertakan juga: versi installer, `uname -m`, dan langkah yang dilakukan sebelum error.

### 12.2 Log runtime server

Log HTTP ditulis **asinkron via channel** (tidak memblokir request; hanya path, method, status, latency — `/health` di-skip):

```bash
journalctl -u immich-go -f                       # systemd: semua stdout/stderr logger async
tail -F /data/logs/server.out                    # mode manual (§5.3)
grep ' 5[0-9][0-9] ' /data/logs/server.out       # filter request berstatus 5xx
```

Kata kunci yang muncul di log startup: `config:` (env invalid/default), `db:` (WAL/pool/migrasi), `worker:` (kompresi/thumbnail per asset), `http:` (graceful shutdown), `auth:` (login gagal per email).

### 12.3 Inspeksi database SQLite langsung

```bash
sqlite3 /data/immich.db '.tables'
sqlite3 /data/immich.db "SELECT id,email,"isAdmin" FROM users;"
sqlite3 /data/immich.db "SELECT count(*), resized FROM assets GROUP BY resized;"   # antrian worker
sqlite3 /data/immich.db "PRAGMA journal_mode; PRAGMA integrity_check;"              # WAL + korupsi
sqlite3 /data/immich.db ".dump" > /tmp/immich-dump.sql                              # audit/export
```

Tabel inti: `users`, `assets`, `albums`, `album_assets`. Kolom penting asset: `originalPath`, `thumbPath`, `encodedPath`, `fileSize`, `checksum` (SHA-256 dedup), `resized`, `exifImageDate`, `lat/lon`, `duration`.

### 12.4 Profil resource saat insiden

```bash
# RSS real-time (target < IMMICH_MEMORY_LIMIT_MB)
watch -n2 'ps -o pid,rss,vsz,pcpu,comm -p $(pgrep -f "/opt/immich-go/immich-go")'

# CPU worker kompresi (harusnya ≤ IMMICH_WORKER_MAX_CPU%)
top -b -n1 -H -p $(pgrep -f immich-go) | head -20

# I/O per proses — migrasi/copy mode sering bottleneck di sini
iotop -oP 2>/dev/null || pidstat -d 2

# tekanan memori & swap (swappiness sudah dituning ke 10 oleh installer arm64)
free -h; vmstat 1 5; dmesg | grep -i "out of memory\|oom" | tail
```

### 12.5 Debug API dengan curl

```bash
BASE=http://127.0.0.1:2283
TOKEN=$(curl -s -X POST $BASE/api/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"admin@rumah.local","password":"..."}' | jq -r .accessToken)

# lihat header cache thumbnail & status mentah
curl -sI -H "Authorization: Bearer $TOKEN" $BASE/api/assets/<id>/thumbnail | head

# Range request video (seek) — harus balas 206 Partial Content
curl -s -o /dev/null -w "%{http_code} %{size_download}\n" \
  -H "Authorization: Bearer $TOKEN" -H "Range: bytes=0-1023" \
  $BASE/api/assets/<id>/original

# respons error harus berbentuk NestJS-style (kompatibel parser Flutter)
curl -s -X POST $BASE/api/auth/login -H 'Content-Type: application/json' -d '{"email":"x","password":"y"}'
```

### 12.6 Build khusus debugging (race detector & pprof)

```bash
go build -race -o bin/immich-go-race cmd/server      # requires CGO — HANYA di laptop x86, jangan di ODROID
GODEBUG=gctrace=1 /opt/immich-go/immich-go           # trace GC (deteksi kebocoran memori)
GOTRACEBACK=all kill -QUIT <pid>                     # dump goroutine lengkap → journalctl
```

### 12.7 Mendiagnosis kegagalan migrasi

```bash
cat .import_state                                    # checkpoint: ID terakhir sukses
grep -c "missing file" migrate.log                   # jumlah file fisik hilang (warning, tidak fatal)
/opt/immich-go/immich-migrate --force-restart ...    # ulangi dari nol (abaikan checkpoint)
```

---

## 13. Troubleshooting — Langkah Mengatasi Masalah

### 13.1 Alur triase 5 langkah (ikuti berurutan)

```text
1. APAPUN masalahnya → systemctl status immich-go && journalctl -u immich-go -n 50
2. Service mati/loop?  → jalankan manual: set -a; source /opt/immich-go/immich-go.env; set +a;
                         /opt/immich-go/immich-go   (pesan error pertama muncul di layar)
3. API hidup tapi app menolak? → curl handshake §7.1; bandingkan dgn §13.2 baris "app menolak"
4. Lambat/OOM?         → profil resource §12.4, lalu turunkan limit (§10)
5. Tetap buntu?        → ./immich-go-installer --export-only, kirim SATU file log tsb (§12.1)
```

### 13.2 Tabel gejala → penyebab → solusi

| Gejala | Penyebab umum | Solusi |
|---|---|---|
| Installer: `port 2283 busy` | Service lain / instance lama | `ss -ltnp \| grep 2283`; hentikan pemakai, atau ubah port di form |
| Installer gagal tulis `/opt` | Dijalankan tanpa sudo | `sudo ./immich-go-installer` (menulis `/opt`, systemd, sysctl) |
| TUI tidak muncul lewat SSH | Tidak ada TTY | `ssh -t host sudo ./immich-go-installer` atau pakai `--unattended` |
| Step systemd berstatus `SKIP` | Host tanpa systemd (container) | Normal — jalankan manual per §5.3, atau deploy via docker-compose |
| Service `activating/auto-restart` | Env salah / DB rusak / port bentrok | `journalctl -u immich-go -n 50`; cek `immich-go.env`; kirim debug log |
| Service `Killed` lalu restart | OOM killer (RAM habis) | Turunkan `IMMICH_MEMORY_LIMIT_MB`→384, `IMMICH_DB_MAX_CONNS`→3; cek `dmesg \| grep oom` |
| App Immich "server not compatible" | URL salah | Pastikan berakhiran `/api`: `http://IP:2283/api`; cek `server-info/version` balas major=1 minor=100 |
| App connect tapi login gagal terus | Seed admin tidak aktif (users table sudah terisi hasil migrasi) | Login akun migrasi, atau reset password via `immich-migrate --reset-password` (§11) |
| Login 401 pada instalasi fresh | Password < 8 karakter diubah manual di env tanpa re-seed | Hapus DB hanya bila data boleh hilang, install ulang; atau PUT `/api/user-me/change-password` dari sesi lain |
| Semua perangkat logout bersamaan | `IMMICH_JWT_SECRET` berubah | By design. Jangan set ulang secret kecuali sengaja; simpan nilai lama di backup |
| Upload foto/video gagal / timeout | `WriteTimeout` terlalu kecil utk file besar | Naikkan `IMMICH_WRITE_TIMEOUT=1800s` → restart; cek sisa disk `df -h /data` |
| Upload balas `409 Conflict` | File duplikat (SHA-256 sama) — by design dedup | Bukan error; app akan menandai "already backed up" |
| Thumbnail lama muncul | Worker throttled by design | Normal di SBC; `sqlite3 /data/immich.db "select count(*) from assets where resized=0"`; naikkan `IMMICH_WORKER_MAX_CPU` sementara |
| Galeri kosong setelah migrasi | Path symlink putus (storage lama dipindah/unmount) | `find /data/photos -xtype l \| head`; re-mount storage lama atau jalankan ulang migrasi `--mode copy` |
| RAM tinggi saat migrasi | Batch terlalu besar | Turunkan `--batch 200`; tool memanggil `runtime.GC()` tiap batch |
| Migrasi lambat | Mode `copy` di SD card | Pakai `--mode symlink`; batasi `--batch`; jalankan malam hari |
| Ctrl+C saat migrasi, ragu lanjut? | Checkpoint | Aman — jalankan perintah yang sama, tool resume dari `.import_state` (§12.7) |
| Setelah reboot service mati | Unit tidak enabled | `sudo systemctl enable immich-go && sudo systemctl reboot --reboot`… lalu cek status |
| DB `database disk image is malformed` | Power-loss saat WAL checkpoint | Stop service → `sqlite3 immich.db "PRAGMA integrity_check"`; pulihkan dari backup §14; mencegah: pasang UPS/`sync` tuning |
| Clock melompat / thumbnail tanggal salah | ODROID tanpa RTC, waktu belum sinkron NTP | `timedatectl set-ntp true`; migrasi asset memakai tanggal EXIF, bukan waktu upload |
| Hotspot WiFi HP tidak bisa akses server | Isolasi client / IP berubah | Satu router LAN; cek `arp -a`; pertimbangkan IP statis via DietPi (`dietpi-config`) |

**Eskalasi:** kumpulkan debug log (§12.1) + output `journalctl -u immich-go -n 100` + `uname -a`, lalu kirim ke maintainer.

## 14. Backup & Restore

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

**Backup harian otomatis (cron):**

```bash
sudo crontab -e
# 02:30 setiap hari, simpan 7 salinan terakhir:
30 2 * * * systemctl stop immich-go && tar czf /backup/immich-go-$(date +\%F).tar.gz -C /data . ; systemctl start immich-go; find /backup -name 'immich-go-*.tar.gz' -mtime +7 -delete
```

**Verifikasi hasil restore** (lakukan setelah pindah hardware): `systemctl status immich-go` → handshake §7.1 → buka app, pastikan galeri & thumbnail muncul.

Mode `symlink` hasil migrasi: backup wajib mencakup storage lama (symlink menunjuk ke sana) — atau konversi ke `copy` sebelum media lama dilepas.

---

## 15. Struktur Proyek, Dependensi Go & Development

### 15.1 Pohon direktori

```text
go-immich/
├── cmd/
│   ├── server/      # immich-go            (API + worker, graceful shutdown)
│   ├── migrate/     # immich-migrate       (CLI legacy import, checkpoint)
│   └── installer/   # immich-go-installer  (TUI Bubble Tea + engine 8-step)
├── config/          # baca env IMMICH_*, crypto util
├── internal/
│   ├── app/         # orkestrator: DI manual, wiring router, lifecycle
│   ├── database/    # SQLite WAL, connection pool, auto-create tabel
│   ├── handlers/    # auth, server-info, assets, upload, album, files, router
│   ├── middleware/  # JWT auth, CORS, async logger
│   ├── models/      # DTO kompatibel OpenAPI Immich
│   ├── repository/  # layer query users/assets/albums
│   ├── services/    # auth (bcrypt/JWT), server-info
│   ├── worker/      # kompresi/thumbnail (throttled CPU)
│   ├── legacy/      # pembaca PostgreSQL lama (pgx)
│   └── installer/   # config, engine install, sysinfo, debuglog, systemd, tui
├── pkg/             # imgutil, storage linker, checkpoint, async logger
├── Makefile · Dockerfile · docker-compose.dev.yml · setup-os.sh
└── README.md        # dokumen ini
```

### 15.2 Manajemen dependensi Go

Semua dependensi didefinisikan di `go.mod` dan **terkunci** di `go.sum` — build selalu reproducible. Yang perlu operator/dev ketahui:

```bash
go mod download          # unduh semua modul ke cache lokal (perlu internet HANYA saat build)
go mod verify            # pastikan checksum modul cocok dgn go.sum (integritas supply-chain)
go list -m all           # daftar penuh modul langsung + transitive
go mod graph             # siapa menarik siapa (menelusuri modul tak dikenal)
go get -u ./... && go mod tidy   # upgrade bebas; JANGAN dilakukan tanpa regression test
```

Catatan penting:
- **Tidak ada satu pun modul yang butuh CGO.** Bila suatu hari `go build` menuntut gcc, berarti ada perubahan yang salah — kembalikan ke driver `modernc.org/sqlite`.
- Vendor opsional untuk build offline: `go mod vendor` lalu build dengan `-mod=vendor` (berguna di jaringan terbatas ODROID).
- Update keamanan dependensi dipantau lewat `govulncheck ./...` (opsional, butuh `golang.org/x/vuln`).

### 15.3 Build dari sumber (di laptop amd64 maupun arm64)

```bash
git clone <repo> && cd go-immich
make deps                # go mod download + verify
make test                # unit test (jalan di arsitektur apa pun)
make build-all           # bin/immich-go-linux-{amd64,arm64} + migrate + installer
ls -lh bin/              # 6 artefak statis, masing-masing ±10–15 MB
```

Build manual tanpa Makefile (mis. di CI minimal):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w -X main.version=1.100.0" \
  -o bin/immich-go-linux-arm64 cmd/server
```

### 15.4 Development loop di x86_64

```bash
make run-local                     # hot-reload via air; data di ./dev-data, port 2283
# atau docker:
docker compose -f docker-compose.dev.yml up --build   # + postgres-legacy utk uji migrasi
curl -s localhost:2283/api/server-info/version | jq .
```

### 15.5 Deploy manual x86 → ODROID-C2

```bash
make build-arm64
scp bin/immich-go-linux-arm64 root@odroid:/opt/immich-go/immich-go
ssh root@odroid "systemctl restart immich-go && systemctl status immich-go --no-pager -n 5"
```

Atau full-release dengan installer TUI:

```bash
tar czf immich-go-release-linux-arm64.tar.gz -C bin \
    immich-go-linux-arm64 immich-migrate-linux-arm64 immich-go-installer-linux-arm64
scp immich-go-release-linux-arm64.tar.gz root@odroid:/tmp/
ssh root@odroid 'cd /tmp && tar xzf immich-go-release-linux-arm64.tar.gz && ./immich-go-installer'
```

### 15.6 Rilis Docker multi-arch

```bash
make docker-buildx                    # build linux/amd64 + linux/arm64 sekaligus
docker run --rm -p 2283:2283 -v $PWD/dev-data:/data go-immich:local
```

## 16. FAQ Operator

**Q: Apakah data lama Immich saya hilang?** Tidak. `immich-migrate` hanya membaca PostgreSQL lama (read-only) dan me-link file; storage lama tidak diubah.

**Q: Bolehkah dua ODROID menjalankan satu DB yang sama?** Tidak — SQLite bersifat single-host. Satu server per instalasi.

**Q: Bagaimana menambah user kedua?** Versi MVP ini meng-auto-seed satu admin. User tambahan dapat dibuat via migrasi dari DB lama, atau insert baris bcrypt di tabel `users` (`sqlite3 /data/immich.db`) lalu restart service.

**Q: App meminta "admin API key"?** Kirim header `x-api-key: <token JWT>` — server menerimanya setara Bearer token.

**Q: Bisa diakses dari internet?** Jangan expose port 2283 langsung. Taruh di belakang reverse-proxy TLS (Caddy/nginx/Traefik) atau VPN (WireGuard/Tailscale).

**Q: Seberapa sering backup?** Harian via cron (§14), minimal mingguan disimpan off-device.

---

## 17. Catatan Keamanan

- `/opt/immich-go/immich-go.env` berisi secret (JWT + password seed) dengan permission **0600** — jangan pernah di-commit/di-share mentah. Yang boleh dikirim ke support hanyalah **debug log** (§12.1) yang sudah di-mask otomatis.
- Mengganti `IMMICH_JWT_SECRET` memaksa logout semua perangkat (token lama jadi tidak valid). Simpan secret aktif dalam brankas/backup Anda.
- Server berbicara HTTP polos di LAN. Untuk akses lintas jaringan/internet: tutup di reverse-proxy TLS (Caddy/nginx/Traefik) **atau** VPN (WireGuard/Tailscale) — jangan port-forward 2283 tanpa TLS.
- Ganti password admin bawaan segera setelah instalasi (app → change password, atau `PUT /api/user-me/change-password`).
- Unit systemd sudah memakai hardening: `NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=full`, dengan `ReadWritePaths` terbatas ke direktori data/install.
- Backup rutin = proteksi utama terhadap ransomware/kegagalan daya di SBC; simpan minimal satu salinan di luar perangkat (§14).
- Update dependensi Go dilakukan terpusat oleh maintainer (`go.sum` terkunci); operator cukup memasang rilis resmi, tidak perlu `go get` di perangkat.

---

*Versi dokumen: 2026-10-28 · selaras go-immich v1.100.0. Bila ragu, jalankan `./immich-go-installer --help` dan kirim debug log.*
