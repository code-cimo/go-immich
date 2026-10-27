// Package legacy reads the schema of an official Immich server (PostgreSQL)
// and maps its rows onto this project's SQLite models. It is shared by the
// migrate CLI so all SQL/dialect quirks live in exactly one place.
package legacy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"go-immich/internal/models"
)

// Client wraps a pgx pool pointed at the old Immich database.
type Client struct {
	pool *pgxpool.Pool
}

// Connect opens the legacy Postgres connection and verifies the schema is
// recognisable as Immich (users + assets + albums tables must exist).
func Connect(ctx context.Context, dsn string) (*Client, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("legacy: parse dsn: %w", err)
	}
	// The legacy server may run on the same tiny board; keep the pool small.
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("legacy: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("legacy: ping: %w", err)
	}
	c := &Client{pool: pool}
	if err := c.verifySchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return c, nil
}

// Close releases the pool.
func (c *Client) Close() { c.pool.Close() }

func (c *Client) verifySchema(ctx context.Context) error {
	var found int
	err := c.pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT table_name) FROM information_schema.tables
		  WHERE table_name IN ('users','assets','albums','album_users')`).Scan(&found)
	if err != nil {
		return fmt.Errorf("legacy: schema probe: %w", err)
	}
	if found < 4 {
		return fmt.Errorf("legacy: expected Immich tables (got %d/4) - wrong database?", found)
	}
	return nil
}

// ---------- users ----------

// User row mapped from the legacy "users" table.
type User struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string // bcrypt ("$2a$..") or argon2id ("$argon2id$..")
	IsAdmin      bool
	IsActive     bool
	CreatedAt    time.Time
}

// FetchUsers pages through the legacy user table ordered by id so checkpointing
// can resume deterministically.
func (c *Client) FetchUsers(ctx context.Context, afterID string, limit int) ([]User, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT id::text,
		        lower(coalesce(nullif(email,''), 'user-'||substr(id::text,1,8)||'@legacy.local')),
		        coalesce(nullif(name,''), 'User'),
		        password,
		        admin,
		        deleted_at IS NULL,
		        created_at
		   FROM users
		  WHERE id::text > $1
		  ORDER BY id::text
		  LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("legacy: fetch users: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var (
			u       User
			created pgtype.Timestamptz
		)
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash,
			&u.IsAdmin, &u.IsActive, &created); err != nil {
			return nil, fmt.Errorf("legacy: scan user: %w", err)
		}
		u.CreatedAt = created.Time
		out = append(out, u)
	}
	return out, rows.Err()
}

// PasswordKind classifies the stored hash so the CLI can decide whether to
// carry it over (bcrypt) or force a reset (argon2id without extra deps).
func (u User) PasswordKind() string {
	switch {
	case strings.HasPrefix(u.PasswordHash, "$2"):
		return "bcrypt"
	case strings.HasPrefix(u.PasswordHash, "$argon2"):
		return "argon2id"
	default:
		return "unknown"
	}
}

// ---------- assets ----------

// Asset carries everything needed from "assets" + LEFT JOIN "exif".
type Asset struct {
	ID            string
	OwnerID       string
	DeviceAssetID string
	OriginalPath  string // storage-template path relative to --library-root
	Type          string // IMAGE / VIDEO / AUDIO
	MimeType      string
	FileName      string
	FileCreatedAt time.Time
	FileModifiedAt time.Time
	ChecksumHex   string
	IsFavorite    bool
	IsArchived    bool
	IsTrashed     bool
	Visibility    string
	Exif          Exif
}

// Exif mirrors the subset of the legacy exif table we persist.
type Exif struct {
	DateTimeOriginal time.Time
	City             string
	State            string
	Country          string
	Lat              float64
	Lon              float64
	Make             string
	Model            string
	LensModel        string
	Description      string
	ProjectionType   string
	ImageWidth       int32
	ImageHeight      int32
	FileSizeByte     int64
	FPS              float64
	TimeZone         string
	Rating           int32
}

const assetSQL = `
SELECT a.id::text,
       a.owner_id::text,
       a."deviceAssetId",
       a."originalPath",
       upper(a.type),
       a."fileCreatedAt",
       a."fileModifiedAt",
       encode(a.checksum, 'hex'),
       a."isFavorite",
       a."isArchived",
       a."isTrashed",
       coalesce(a."visibility", 'visible'),
       coalesce(e."dateTimeOriginal", 'epoch'::timestamptz),
       coalesce(e.city, ''), coalesce(e.state, ''), coalesce(e.country, ''),
       coalesce(e."latitude", 0), coalesce(e."longitude", 0),
       coalesce(lower(e.make), ''), coalesce(e.model, ''),
       coalesce(e."lensModel", ''), coalesce(e."imageDescription", ''),
       coalesce(e."projectionType", 'EQUIRECTANGULAR'),
       coalesce(e."exifImageWidth", 0), coalesce(e."exifImageHeight", 0),
       coalesce(e."fileSizeInByte", 0),
       coalesce((e."updateInfo"->>'fps')::float8, 0),
       coalesce(e."timeZone", ''),
       coalesce(e.rating, 0)
  FROM assets a
  LEFT JOIN exif e ON e.asset_id = a.id
 WHERE a.id::text > $1 AND a."deletedAt" IS NULL
 ORDER BY a.id::text
 LIMIT $2`

// FetchAssets pages assets+exif ordered by id (uuid text ordering is stable,
// which is what the checkpoint relies on).
func (c *Client) FetchAssets(ctx context.Context, afterID string, limit int) ([]Asset, error) {
	rows, err := c.pool.Query(ctx, assetSQL, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("legacy: fetch assets: %w", err)
	}
	defer rows.Close()

	var out []Asset
	for rows.Next() {
		var (
			a         Asset
			fc, fm, dt pgtype.Timestamptz
		)
		if err := rows.Scan(&a.ID, &a.OwnerID, &a.DeviceAssetID, &a.OriginalPath,
			&a.Type, &fc, &fm, &a.ChecksumHex, &a.IsFavorite,
			&a.IsArchived, &a.IsTrashed, &a.Visibility,
			&dt, &a.Exif.City, &a.Exif.State, &a.Exif.Country,
			&a.Exif.Lat, &a.Exif.Lon, &a.Exif.Make, &a.Exif.Model,
			&a.Exif.LensModel, &a.Exif.Description, &a.Exif.ProjectionType,
			&a.Exif.ImageWidth, &a.Exif.ImageHeight, &a.Exif.FileSizeByte,
			&a.Exif.FPS, &a.Exif.TimeZone, &a.Exif.Rating); err != nil {
			return nil, fmt.Errorf("legacy: scan asset: %w", err)
		}
		a.FileCreatedAt = fc.Time
		a.FileModifiedAt = fm.Time
		a.Exif.DateTimeOriginal = dt.Time
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- albums ----------

// Album maps the legacy albums + album_users ownership join.
type Album struct {
	ID          string
	OwnerID     string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// FetchAlbums pages albums ordered by id.
func (c *Client) FetchAlbums(ctx context.Context, afterID string, limit int) ([]Album, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT au.album_id::text, au.user_id::text, al."albumName",
		        coalesce(al.description,''), al."createdAt", al."updatedAt"
		   FROM album_users au
		   JOIN albums al ON al.id = au.album_id
		  WHERE au.album_id::text > $1 AND al."deletedAt" IS NULL
		  ORDER BY au.album_id::text
		  LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("legacy: fetch albums: %w", err)
	}
	defer rows.Close()

	var out []Album
	for rows.Next() {
		var (
			al               Album
			created, updated pgtype.Timestamptz
		)
		if err := rows.Scan(&al.ID, &al.OwnerID, &al.Name, &al.Description,
			&created, &updated); err != nil {
			return nil, fmt.Errorf("legacy: scan album: %w", err)
		}
		al.CreatedAt = created.Time
		al.UpdatedAt = updated.Time
		out = append(out, al)
	}
	return out, rows.Err()
}

// AlbumAssets returns the asset ids belonging to one album, trying the newer
// "assetIds" jsonb array first and falling back to the legacy join table.
func (c *Client) AlbumAssets(ctx context.Context, albumID string) ([]string, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT jsonb_array_elements_text("assetIds")
		   FROM albums WHERE id = $1::uuid AND "assetIds" IS NOT NULL`, albumID)
	if err == nil {
		defer rows.Close()
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
	// older schema fallback: album_assets join table
	rows2, err2 := c.pool.Query(ctx,
		`SELECT asset_id::text FROM album_assets WHERE album_id = $1::uuid ORDER BY "order"`, albumID)
	if err2 != nil {
		return nil, fmt.Errorf("legacy: album assets: %w", err2)
	}
	defer rows2.Close()
	var ids []string
	for rows2.Next() {
		var id string
		if err := rows2.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows2.Err()
}

// ---------- mapping helpers ----------

// ToModel converts a legacy asset into our SQLite model given the final on-disk
// path chosen by the file linker (symlink target or copied destination).
func (a *Asset) ToModel(mappedPath string) *models.Asset {
	localDate := a.Exif.DateTimeOriginal
	if localDate.IsZero() || localDate.Year() < 1980 {
		localDate = a.FileCreatedAt
	}
	return &models.Asset{
		ID:               a.ID,
		DeviceAssetID:    a.DeviceAssetID,
		OwnerID:          a.OwnerID,
		OriginalPath:     mappedPath,
		Type:             models.AssetType(strings.ToUpper(a.Type)),
		MimeType:         guessMime(a.FileName, a.Type),
		FileName:         a.FileName,
		FileCreatedAt:    a.FileCreatedAt,
		FileModifiedAt:   a.FileModifiedAt,
		ChecksumSHA256:   a.ChecksumHex,
		IsVisible:        a.Visibility == "visible" && !a.IsArchived,
		IsFavorite:       a.IsFavorite,
		IsArchived:       a.IsArchived,
		IsTrashed:        a.IsTrashed,
		ThumbGenerated:   false, // worker rebuilds thumbnails post-migration
		FileSize:         a.Exif.FileSizeByte,
		Width:            int(a.Exif.ImageWidth),
		Height:           int(a.Exif.ImageHeight),
		DateTimeOrig:     a.Exif.DateTimeOriginal,
		LocalDate:        localDate,
		City:             a.Exif.City,
		State:            a.Exif.State,
		Country:          a.Exif.Country,
		Lat:              a.Exif.Lat,
		Lon:              a.Exif.Lon,
		Make:             a.Exif.Make,
		Model:            a.Exif.Model,
		LensModel:        a.Exif.LensModel,
		ImageDescription: a.Exif.Description,
		ProjectionType:   a.Exif.ProjectionType,
		FPS:              a.Exif.FPS,
		Timezone:         a.Exif.TimeZone,
		Rating:           int(a.Exif.Rating),
	}
}

func guessMime(name, assetType string) string {
	lower := strings.ToLower(name)
	for _, pair := range [][2]string{
		{".jpg", "image/jpeg"}, {".jpeg", "image/jpeg"}, {".png", "image/png"},
		{".webp", "image/webp"}, {".heic", "image/heic"}, {".gif", "image/gif"},
		{".tif", "image/tiff"}, {".tiff", "image/tiff"},
		{".mp4", "video/mp4"}, {".mov", "video/quicktime"}, {".mkv", "video/x-matroska"},
		{".avi", "video/x-msvideo"}, {".3gp", "video/3gpp"},
		{".mp3", "audio/mpeg"}, {".m4a", "audio/mp4"}, {".wav", "audio/wav"},
	} {
		if strings.HasSuffix(lower, pair[0]) {
			return pair[1]
		}
	}
	switch strings.ToUpper(assetType) {
	case "VIDEO":
		return "video/mp4"
	case "AUDIO":
		return "audio/mp4"
	default:
		return "image/jpeg"
	}
}
