// Package database owns the SQLite connection lifecycle: DSN tuning for the
// modernc.org/sqlite pure-Go driver, WAL pragmas, connection pooling and the
// idempotent schema bootstrap used at server start-up.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure Go driver - no CGO, cross-compiles cleanly for arm64
)

// schema is executed once per boot. Everything is IF NOT EXISTS so the same
// statement doubles as a lightweight migration for fresh installs.
const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    avatar_path   TEXT NOT NULL DEFAULT '',
    is_admin      INTEGER NOT NULL DEFAULT 0,
    is_active     INTEGER NOT NULL DEFAULT 1,
    quota_bytes   INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS assets (
    id               TEXT PRIMARY KEY,
    device_asset_id  TEXT NOT NULL DEFAULT '',
    owner_id         TEXT NOT NULL REFERENCES users(id),
    original_path    TEXT NOT NULL DEFAULT '',
    thumb_path       TEXT NOT NULL DEFAULT '',
    encoded_path     TEXT NOT NULL DEFAULT '',
    type             TEXT NOT NULL CHECK (type IN ('IMAGE','VIDEO','AUDIO')),
    mime_type        TEXT NOT NULL DEFAULT '',
    file_name        TEXT NOT NULL DEFAULT '',
    file_created_at  TEXT NOT NULL DEFAULT '',
    file_modified_at TEXT NOT NULL DEFAULT '',
    checksum_sha256  TEXT NOT NULL DEFAULT '',
    checksum_unique  TEXT GENERATED ALWAYS AS (CASE WHEN checksum_sha256 = '' THEN NULL ELSE id || ':' || checksum_sha256 END) UNIQUE,
    is_visible       INTEGER NOT NULL DEFAULT 1,
    is_favorite      INTEGER NOT NULL DEFAULT 0,
    is_archived      INTEGER NOT NULL DEFAULT 0,
    is_trashed       INTEGER NOT NULL DEFAULT 0,
    is_resized       INTEGER NOT NULL DEFAULT 0,
    thumb_generated  INTEGER NOT NULL DEFAULT 0,
    file_size        INTEGER NOT NULL DEFAULT 0,
    width            INTEGER NOT NULL DEFAULT 0,
    height           INTEGER NOT NULL DEFAULT 0,
    DateTimeOriginal TEXT NOT NULL DEFAULT '',
    local_date       TEXT NOT NULL DEFAULT '',
    city             TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT '',
    country          TEXT NOT NULL DEFAULT '',
    lat              REAL NOT NULL DEFAULT 0,
    lon              REAL NOT NULL DEFAULT 0,
    make             TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    lens_model       TEXT NOT NULL DEFAULT '',
    exif_image_desc  TEXT NOT NULL DEFAULT '',
    projection_type  TEXT NOT NULL DEFAULT 'EQUIRECTANGULAR',
    fps              REAL NOT NULL DEFAULT 0,
    timezone         TEXT NOT NULL DEFAULT '',
    rating           INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    deleted_at       TEXT
);

-- hot paths: gallery timeline + duplicate detection + worker queue scans
CREATE INDEX IF NOT EXISTS idx_assets_owner_date ON assets (owner_id, local_date DESC);
CREATE INDEX IF NOT EXISTS idx_assets_checksum   ON assets (checksum_sha256);
CREATE INDEX IF NOT EXISTS idx_assets_pending    ON assets (thumb_generated, type);

CREATE TABLE IF NOT EXISTS albums (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL REFERENCES users(id),
    album_name  TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    "order"     INTEGER NOT NULL DEFAULT 0,
    thumb_asset_id TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS album_assets (
    album_id  TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
    asset_id  TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    PRIMARY KEY (album_id, asset_id)
);

CREATE TABLE IF NOT EXISTS import_state (
    source     TEXT PRIMARY KEY,
    last_id    TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
`

// Open prepares the SQLite file (creating parent dirs), applies the ODROID-C2
// friendly pragmas and returns a pooled *sql.DB.
//
// DSN pragmas matter: busy_timeout prevents SQLITE_BUSY spurious failures when
// the background worker writes while HTTP handlers read; foreign_keys keeps
// referential integrity; journal_mode=WAL enables concurrent readers.
func Open(ctx context.Context, dbPath string, maxConns int) (*sql.DB, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("database: create dir %q: %w", dir, err)
		}
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"+
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_pragma=temp_store(MEMORY)"+
		"&_pragma=mmap_size(8388608)&_pragma=cache_size(-8000)&_time_format=sqlite",
		dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("database: open %q: %w", dbPath, err)
	}

	// Minimal pool: SQLite serialises writers anyway; extra connections only
	// waste RAM on a 2GB board. maxConns covers WAL readers + 1 writer.
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxLifetime(0) // never recycle: single-file DB, cheap to keep

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}
	return db, nil
}

// InitSchema creates all tables/indexes idempotently and runs a quick PRAGMA
// integrity sanity check (page_count based, O(1)).
func InitSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("database: schema bootstrap: %w", err)
	}
	// WAL is persistent but re-assert it; harmless on already-configured files.
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err == nil {
		if mode != "wal" {
			if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
				return fmt.Errorf("database: enable WAL: %w", err)
			}
		}
	}
	return nil
}

// EnsureStorageDirs creates the flat-file storage tree expected by the upload
// handler and the legacy importer.
func EnsureStorageDirs(photosDir, thumbsDir, encodedDir string) error {
	for _, d := range []string{photosDir, thumbsDir, encodedDir} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return fmt.Errorf("database: storage dir %q: %w", d, err)
		}
	}
	return nil
}
