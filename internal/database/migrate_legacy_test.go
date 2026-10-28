package database

import (
	"context"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

const legacyAssetsDDL = `CREATE TABLE assets (
    id TEXT PRIMARY KEY, device_asset_id TEXT NOT NULL DEFAULT '', owner_id TEXT NOT NULL,
    original_path TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, file_name TEXT NOT NULL DEFAULT '',
    is_resized INTEGER NOT NULL DEFAULT 0, thumb_generated INTEGER NOT NULL DEFAULT 0,
    file_size INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
INSERT INTO assets (id, owner_id, type, created_at, updated_at) VALUES ('a1','u1','IMAGE','now','now');`

func TestMigrateLegacyChecksumColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(context.Background(), dbPath, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, legacyAssetsDDL); err != nil {
		t.Fatal(err)
	}

	// Pre-condition: legacy table lacks the column.
	if _, err := db.ExecContext(ctx, "SELECT checksum_sha256 FROM assets"); err == nil {
		t.Fatal("expected missing-column error before migration")
	}

	// The fix: InitSchema must upgrade in place without error.
	if err := InitSchema(ctx, db); err != nil {
		t.Fatalf("InitSchema on legacy DB failed: %v", err)
	}

	var got string
	if err := db.QueryRowContext(ctx, "SELECT checksum_sha256 FROM assets WHERE id='a1'").Scan(&got); err != nil {
		t.Fatalf("post-migration select failed: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty default, got %q", got)
	}

	// Idempotency: second run must also pass.
	if err := InitSchema(ctx, db); err != nil {
		t.Fatalf("second InitSchema failed: %v", err)
	}

	// Fresh DB path still works too.
	db2Path := filepath.Join(t.TempDir(), "fresh.db")
	db2, err := Open(ctx, db2Path, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := InitSchema(ctx, db2); err != nil {
		t.Fatalf("InitSchema on fresh DB failed: %v", err)
	}
	// generated column + dedup constraint exist on fresh install: seed the
	// owner first (FK is enforced), then two identical-checksum rows for the
	// same owner must be rejected by checksum_unique.
	if _, err := db2.ExecContext(ctx, "INSERT INTO users (id,email,password_hash,name,created_at,updated_at) VALUES ('u1','a@b.c','h','A','now','now')"); err != nil {
		t.Fatalf("seed user failed: %v", err)
	}
	if _, err := db2.ExecContext(ctx, "INSERT INTO assets (id,owner_id,type,checksum_sha256,checksum_owner,created_at,updated_at) VALUES ('x','u1','IMAGE','abc','u1:abc','now','now')"); err != nil {
		t.Fatalf("insert with checksum failed: %v", err)
	}
	if _, err := db2.ExecContext(ctx, "INSERT INTO assets (id,owner_id,type,checksum_sha256,checksum_owner,created_at,updated_at) VALUES ('y','u1','IMAGE','abc','u1:abc','now','now')"); err == nil {
		t.Fatal("expected UNIQUE rejection for duplicate checksum")
	}
	// Legacy rows migrated in have empty checksums; the generated column must
	// map them to NULL so multiple empty-checksum rows coexist.
	if _, err := db2.ExecContext(ctx, "INSERT INTO assets (id,owner_id,type,created_at,updated_at) VALUES ('z','u1','IMAGE','now','now')"); err != nil {
		t.Fatalf("second empty-checksum row should be allowed: %v", err)
	}
}
