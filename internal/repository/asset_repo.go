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

// AssetRepo owns all assets-table SQL.
type AssetRepo struct{ db *sql.DB }

// NewAssetRepo builds the repository around an opened pool.
func NewAssetRepo(db *sql.DB) *AssetRepo { return &AssetRepo{db: db} }

const assetColumns = `id, device_asset_id, owner_id, original_path, thumb_path,
	encoded_path, type, mime_type, file_name, file_created_at, file_modified_at,
	checksum_sha256, is_visible, is_favorite, is_archived, is_trashed, is_resized,
	thumb_generated, file_size, width, height, DateTimeOriginal, local_date,
	city, state, country, lat, lon, make, model, lens_model, exif_image_desc,
	projection_type, fps, timezone, rating, created_at, updated_at, deleted_at`

func scanAsset(row interface{ Scan(...any) error }) (*models.Asset, error) {
	var (
		a        models.Asset
		vis, fav, arch, trash, resized, thumb int64
		del      sql.NullString
		typ, mime, fname string
		fc, fm, dtorig, ldate, created, updated string
	)
	err := row.Scan(&a.ID, &a.DeviceAssetID, &a.OwnerID, &a.OriginalPath,
		&a.ThumbPath, &a.EncodedPath, &typ, &mime, &fname, &fc, &fm,
		&a.ChecksumSHA256, &vis, &fav, &arch, &trash, &resized, &thumb,
		&a.FileSize, &a.Width, &a.Height, &dtorig, &ldate,
		&a.City, &a.State, &a.Country, &a.Lat, &a.Lon,
		&a.Make, &a.Model, &a.LensModel, &a.ImageDescription,
		&a.ProjectionType, &a.FPS, &a.Timezone, &a.Rating,
		&created, &updated, &del)
	if err != nil {
		return nil, err
	}
	a.Type = models.AssetType(typ)
	a.MimeType = mime
	a.FileName = fname
	a.FileCreatedAt = parseTS(fc)
	a.FileModifiedAt = parseTS(fm)
	a.DateTimeOrig = parseTS(dtorig)
	a.LocalDate = parseTS(ldate)
	a.CreatedAt = parseTS(created)
	a.UpdatedAt = parseTS(updated)
	a.IsVisible = vis != 0
	a.IsFavorite = fav != 0
	a.IsArchived = arch != 0
	a.IsTrashed = trash != 0
	a.IsResized = resized != 0
	a.ThumbGenerated = thumb != 0
	if del.Valid {
		t := parseTS(del.String)
		a.DeletedAt = &t
	}
	return &a, nil
}

// ErrDuplicateChecksum signals a re-upload of identical content by the same
// user; the handler maps it to HTTP 400/409 per Immich semantics.
var ErrDuplicateChecksum = errors.New("asset: duplicate checksum")

// Create inserts one asset inside an explicit transaction (single-writer rule).
func (r *AssetRepo) Create(ctx context.Context, a *models.Asset) error {
	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("repository: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	res, err := tx.ExecContext(ctx,
		`INSERT INTO assets (`+assetColumnsInsert+`) VALUES (`+strings.Repeat("?, ", 37)+"?)",
		a.ID, a.DeviceAssetID, a.OwnerID, a.OriginalPath, a.ThumbPath,
		a.EncodedPath, string(a.Type), a.MimeType, a.FileName,
		formatTS(a.FileCreatedAt), formatTS(a.FileModifiedAt), a.ChecksumSHA256,
		b2i(a.IsVisible), b2i(a.IsFavorite), b2i(a.IsArchived), b2i(a.IsTrashed),
		b2i(a.IsResized), b2i(a.ThumbGenerated), a.FileSize, a.Width, a.Height,
		formatTS(a.DateTimeOrig), formatTS(a.LocalDate),
		a.City, a.State, a.Country, a.Lat, a.Lon,
		a.Make, a.Model, a.LensModel, a.ImageDescription,
		a.ProjectionType, a.FPS, a.Timezone, a.Rating,
		formatTS(a.CreatedAt), formatTS(a.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicateChecksum
		}
		return fmt.Errorf("repository: insert asset: %w", err)
	}
	_ = res
	return tx.Commit()
}

const assetColumnsInsert = `id, device_asset_id, owner_id, original_path, thumb_path,
	encoded_path, type, mime_type, file_name, file_created_at, file_modified_at,
	checksum_sha256, is_visible, is_favorite, is_archived, is_trashed, is_resized,
	thumb_generated, file_size, width, height, DateTimeOriginal, local_date,
	city, state, country, lat, lon, make, model, lens_model, exif_image_desc,
	projection_type, fps, timezone, rating, created_at, updated_at`

// ByID fetches one asset (nil when absent).
func (r *AssetRepo) ByID(ctx context.Context, id string) (*models.Asset, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+assetColumns+" FROM assets WHERE id = ?", id)
	a, err := scanAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// ExistsByChecksum powers SHA256 de-duplication before writing a new file.
func (r *AssetRepo) ExistsByChecksum(ctx context.Context, ownerID, checksum string) (string, bool, error) {
	var id string
	err := r.db.QueryRowContext(ctx,
		"SELECT id FROM assets WHERE owner_id = ? AND checksum_sha256 = ? LIMIT 1",
		ownerID, checksum).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("repository: checksum lookup: %w", err)
	}
	return id, true, nil
}

// ListFilter captures the query parameters of GET /api/asset (timeline feed).
type ListFilter struct {
	OwnerID     string
	Type        string // "IMAGE" | "VIDEO" | "" (all)
	IsFavorite  *bool
	IsArchived  *bool
	IsTrashed   *bool
	Take        int
	Skip        int
}

// List returns a page of assets ordered newest-first, matching the default
// timeline sort (DateTimeOriginal DESC).
func (r *AssetRepo) List(ctx context.Context, f ListFilter) ([]models.AssetResponseDto, error) {
	q := strings.Builder{}
	q.WriteString("SELECT " + assetColumns + " FROM assets WHERE 1=1")
	args := make([]any, 0, 8)

	add := func(cond string, v ...any) {
		q.WriteString(" AND ")
		q.WriteString(cond)
		args = append(args, v...)
	}

	if f.OwnerID != "" {
		add("owner_id = ?", f.OwnerID)
	}
	if f.Type != "" {
		add("type = ?", f.Type)
	}
	trash := false
	if f.IsTrashed != nil {
		trash = *f.IsTrashed
	}
	add("is_trashed = ?", b2i(trash))
	if trash {
		// Trash view shows everything that was trashed, archived or not.
	} else if f.IsArchived != nil {
		add("is_archived = ?", b2i(*f.IsArchived))
	} else {
		add("is_archived = 0") // Immich default: archive hidden from timeline
	}
	if f.IsFavorite != nil {
		add("is_favorite = ?", b2i(*f.IsFavorite))
	}

	q.WriteString(" ORDER BY DateTimeOriginal DESC, created_at DESC LIMIT ? OFFSET ?")
	take := f.Take
	if take <= 0 || take > 1000 {
		take = 500 // Immich mobile pulls pages of up to ~1k
	}
	args = append(args, take, f.Skip)

	rows, err := r.db.QueryContext(ctx, q.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("repository: list assets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]models.AssetResponseDto, 0, take)
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, fmt.Errorf("repository: scan asset: %w", err)
		}
		out = append(out, a.ToResponse())
	}
	return out, rows.Err()
}

// PendingImages lists IMAGE assets whose thumbnail has not been generated yet
// (the background worker queue). Ordered oldest-first for fairness.
func (r *AssetRepo) PendingImages(ctx context.Context, limit int) ([]*models.Asset, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+assetColumns+` FROM assets
		  WHERE thumb_generated = 0 AND is_trashed = 0 AND type = 'IMAGE'
		  ORDER BY created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("repository: pending images: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*models.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkThumbDone flips the thumbnail flag and optionally records that the
// original was recompressed in place (is_resized) plus its new size.
func (r *AssetRepo) MarkThumbDone(ctx context.Context, id, thumbPath string, resized bool, newSize int64) error {
	var err error
	if resized {
		_, err = r.db.ExecContext(ctx,
			`UPDATE assets SET thumb_generated = 1, thumb_path = ?, is_resized = 1,
				file_size = ?, updated_at = ? WHERE id = ?`,
			thumbPath, newSize, formatTS(time.Now().UTC()), id)
	} else {
		_, err = r.db.ExecContext(ctx,
			`UPDATE assets SET thumb_generated = 1, thumb_path = ?, updated_at = ? WHERE id = ?`,
			thumbPath, formatTS(time.Now().UTC()), id)
	}
	if err != nil {
		return fmt.Errorf("repository: mark thumb done: %w", err)
	}
	return nil
}

// UpdateDimensions stores width/height discovered during processing.
func (r *AssetRepo) UpdateDimensions(ctx context.Context, id string, w, h int) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE assets SET width = ?, height = ? WHERE id = ?", w, h, id)
	return err
}

// UpdatePath re-points an asset to its physical file after a resumed migration
// run located a source file that was missing on the first pass.
func (r *AssetRepo) UpdatePath(ctx context.Context, id, path string, size int64) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE assets SET original_path = ?, file_size = ? WHERE id = ?", path, size, id)
	return err
}

// CountAll feeds the /server-info statistics block.
func (r *AssetRepo) CountAll(ctx context.Context) (images int64, videos int64, onDisk int64, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT
			SUM(CASE WHEN type='IMAGE' THEN 1 ELSE 0 END),
			SUM(CASE WHEN type='VIDEO' THEN 1 ELSE 0 END),
			COALESCE(SUM(file_size), 0)
		 FROM assets WHERE is_trashed = 0`).Scan(&images, &videos, &onDisk)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

// Delete marks an asset trashed (soft delete like the official server).
func (r *AssetRepo) Trash(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`UPDATE assets SET is_trashed = 1, deleted_at = ?, updated_at = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	now := formatTS(time.Now().UTC())
	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, now, now, id); err != nil {
			return fmt.Errorf("repository: trash %s: %w", id, err)
		}
	}
	return tx.Commit()
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed")
}
