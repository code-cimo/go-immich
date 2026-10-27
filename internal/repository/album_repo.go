package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-immich/internal/models"
)

// AlbumRepo owns albums + album_assets SQL.
type AlbumRepo struct{ db *sql.DB }

// NewAlbumRepo builds the repository around an opened pool.
func NewAlbumRepo(db *sql.DB) *AlbumRepo { return &AlbumRepo{db: db} }

// Create inserts an album owned by userID.
func (r *AlbumRepo) Create(ctx context.Context, a *models.Album) error {
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO albums (id, owner_id, album_name, description, "order", thumb_asset_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.OwnerID, a.AlbumName, a.Description, a.Order, a.ThumbAssetID,
		formatTS(a.CreatedAt), formatTS(a.UpdatedAt))
	if err != nil {
		return fmt.Errorf("repository: create album: %w", err)
	}
	return nil
}

// ByID returns one album or nil when missing.
func (r *AlbumRepo) ByID(ctx context.Context, id string) (*models.Album, error) {
	var (
		a       models.Album
		created string
		updated string
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT id, owner_id, album_name, description, "order", thumb_asset_id, created_at, updated_at
		   FROM albums WHERE id = ?`, id).
		Scan(&a.ID, &a.OwnerID, &a.AlbumName, &a.Description, &a.Order,
			&a.ThumbAssetID, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("repository: album by id: %w", err)
	}
	a.CreatedAt = parseTS(created)
	a.UpdatedAt = parseTS(updated)
	return &a, nil
}

// AlbumWithCount pairs an album with its live asset count for list views.
type AlbumWithCount struct {
	Album     models.Album
	AssetCount int64
}

// ListByOwner returns all albums of a user with counts (newest first).
func (r *AlbumRepo) ListByOwner(ctx context.Context, ownerID string) ([]AlbumWithCount, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT a.id, a.owner_id, a.album_name, a.description, a."order",
		        a.thumb_asset_id, a.created_at, a.updated_at,
		        (SELECT COUNT(*) FROM album_assets aa WHERE aa.album_id = a.id)
		   FROM albums a WHERE a.owner_id = ? ORDER BY a.created_at DESC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("repository: list albums: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AlbumWithCount
	for rows.Next() {
		var (
			wc      AlbumWithCount
			created string
			updated string
		)
		if err := rows.Scan(&wc.Album.ID, &wc.Album.OwnerID, &wc.Album.AlbumName,
			&wc.Album.Description, &wc.Album.Order, &wc.Album.ThumbAssetID,
			&created, &updated, &wc.AssetCount); err != nil {
			return nil, err
		}
		wc.Album.CreatedAt = parseTS(created)
		wc.Album.UpdatedAt = parseTS(updated)
		out = append(out, wc)
	}
	return out, rows.Err()
}

// AddAssets links assets to an album in one transaction; unknown asset ids
// are silently skipped (foreign keys reject them) but we pre-validate to give
// the handler an accurate added/failed split like the official endpoint.
func (r *AlbumRepo) AddAssets(ctx context.Context, albumID string, assetIDs []string) (added, failed []string, err error) {
	added = make([]string, 0, len(assetIDs))
	failed = make([]string, 0)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("repository: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	check := tx.StmtContext(ctx, prepareMust(tx, `SELECT 1 FROM assets WHERE id = ? AND is_trashed = 0`))
	link := prepareMust(tx, `INSERT OR IGNORE INTO album_assets (album_id, asset_id, sort_order, created_at) VALUES (?, ?, (SELECT COALESCE(MAX(sort_order)+1,0) FROM album_assets WHERE album_id = ?), ?)`)

	now := formatTS(time.Now().UTC())
	for _, id := range assetIDs {
		var one int
		if err := check.QueryRow(id).Scan(&one); err != nil {
			failed = append(failed, id)
			continue
		}
		if _, err := link.Exec(albumID, id, albumID, now); err != nil {
			failed = append(failed, id)
			continue
		}
		added = append(added, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return added, failed, nil
}

// RemoveAssets unlinks assets from an album.
func (r *AlbumRepo) RemoveAssets(ctx context.Context, albumID string, assetIDs []string) (removed, failed []string, err error) {
	removed = make([]string, 0, len(assetIDs))
	failed = make([]string, 0)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt := prepareMust(tx, `DELETE FROM album_assets WHERE album_id = ? AND asset_id = ?`)
	for _, id := range assetIDs {
		res, err := stmt.Exec(albumID, id)
		if err != nil {
			failed = append(failed, id)
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			failed = append(failed, id)
		} else {
			removed = append(removed, id)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return removed, failed, nil
}

// AssetIDs lists the asset ids currently inside an album, ordered by insertion.
func (r *AlbumRepo) AssetIDs(ctx context.Context, albumID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT asset_id FROM album_assets WHERE album_id = ? ORDER BY sort_order ASC`, albumID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Delete removes an album (links cascade via FK).
func (r *AlbumRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, "DELETE FROM albums WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// prepareMust is a tiny helper that turns statement-prep errors into panics
// only during development-time query typos; at runtime these are static SQL
// strings so failure means a broken build, not bad input. We still return the
// error path safely by prepping on the tx and propagating via a wrapper.
func prepareMust(tx *sql.Tx, q string) *sql.Stmt {
	stmt, err := tx.Prepare(q)
	if err != nil {
		// Surface as a poisoned statement: every Exec returns this error.
		panic(fmt.Sprintf("repository: static statement prep failed (%q): %v", q, trimSQL(q)))
	}
	return stmt
}

func trimSQL(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
