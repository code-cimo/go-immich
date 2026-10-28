// immich-migrate: Phase 7 legacy import CLI.
//
// Reads users/assets/exif/albums from an official Immich PostgreSQL database
// (via pure-Go pgx) and rewrites them into this server's SQLite schema, while
// re-linking the physical files either as zero-copy symlinks (recommended on
// ODROID-C2) or throttled 32KB-buffered copies.
//
// Design constraints for a 2GB ARM64 board:
//   - batch processing (default 500 rows/transaction) + runtime.GC() per batch
//   - checkpoint file (.import_state): restart resumes where it stopped
//   - missing files log a warning and are skipped - never aborts the run
//   - optional --reset-password for argon2id hashes we cannot verify in Go
//
// Usage:
//
//	immich-migrate \
//	  --legacy-dsn "postgres://immich:immich@localhost:5432/immich?sslmode=disable" \
//	  --library-root /mnt/old-immich-upload \
//	  --db /data/immich.db \
//	  --photos-dir /data/photos \
//	  --mode symlink \
//	  [--batch 500] [--reset-password changeme] [--include-trashed] [--force-restart]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"go-immich/internal/database"
	"go-immich/internal/legacy"
	"go-immich/internal/models"
	"go-immich/internal/repository"
	"go-immich/internal/services"
	"go-immich/pkg/checkpoint"
	"go-immich/pkg/storage"
)

const (
	batchUsers  = "users"
	batchAssets = "assets"
	batchAlbums = "albums"
)

type options struct {
	legacyDSN      string
	libraryRoot    string
	dbPath         string
	photosDir      string
	mode           string
	batchSize      int
	resetPassword  string
	includeTrashed bool
	forceRestart   bool
	stateFile      string
}

func main() {
	opts := parseFlags()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, opts); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("migrate: interrupted - progress saved, rerun to resume")
			os.Exit(130)
		}
		log.Fatalf("migrate: %v", err)
	}
}

func parseFlags() *options {
	o := &options{}
	flag.StringVar(&o.legacyDSN, "legacy-dsn", envOr("IMMICH_LEGACY_DSN", ""),
		"PostgreSQL DSN of the old Immich server (required)")
	flag.StringVar(&o.libraryRoot, "library-root", envOr("IMMICH_LEGACY_LIBRARY", "/data/legacy-upload"),
		"root directory of the old upload/library storage")
	flag.StringVar(&o.dbPath, "db", envOr("IMMICH_DB_PATH", "/data/immich.db"),
		"target SQLite database file")
	flag.StringVar(&o.photosDir, "photos-dir", envOr("IMMICH_PHOTOS_DIR", "/data/photos"),
		"destination photos root for the new layout")
	flag.StringVar(&o.mode, "mode", envOr("IMMICH_MIGRATE_MODE", "symlink"),
		"file linking strategy: symlink (zero-copy, recommended) or copy")
	flag.IntVar(&o.batchSize, "batch", 500, "rows per transaction batch")
	flag.StringVar(&o.resetPassword, "reset-password", "",
		"if set, overwrite every imported password with this value (for argon2-only sources)")
	flag.BoolVar(&o.includeTrashed, "include-trashed", false,
		"also import trashed assets (they land in the new trash)")
	flag.BoolVar(&o.forceRestart, "force-restart", false,
		"ignore the checkpoint file and re-import everything (idempotent upserts)")
	flag.StringVar(&o.stateFile, "state-file", ".import_state",
		"checkpoint file path (resume position after crash/restart)")
	flag.Parse()

	if o.legacyDSN == "" {
		log.Fatal("migrate: --legacy-dsn is required (or set IMMICH_LEGACY_DSN)")
	}
	if o.batchSize < 1 || o.batchSize > 5000 {
		log.Fatalf("migrate: --batch must be within 1..5000 (got %d)", o.batchSize)
	}
	return o
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, o *options) error {
	start := time.Now()

	mode, err := storage.ParseMode(o.mode)
	if err != nil {
		return err
	}

	cp, err := checkpoint.Open(o.stateFile)
	if err != nil {
		return err
	}
	if o.forceRestart {
		log.Printf("migrate: --force-restart set - clearing checkpoint %s", o.stateFile)
		if err := cp.Reset(); err != nil {
			return err
		}
	}

	// ---- legacy source -------------------------------------------------------
	pg, err := legacy.Connect(ctx, o.legacyDSN)
	if err != nil {
		return fmt.Errorf("connect legacy postgres: %w", err)
	}
	defer pg.Close()

	// ---- target store ----------------------------------------------------------
	db, err := database.Open(ctx, o.dbPath, 2) // CLI needs 1 writer + headroom
	if err != nil {
		return fmt.Errorf("open target sqlite: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := database.InitSchema(ctx, db); err != nil {
		return fmt.Errorf("target schema: %w", err)
	}
	if err := database.EnsureStorageDirs(o.photosDir, filepath.Join(filepath.Dir(o.photosDir), "thumbnails"),
		filepath.Join(filepath.Dir(o.photosDir), "encoded")); err != nil {
		return fmt.Errorf("storage dirs: %w", err)
	}

	users := repository.NewUserRepo(db)
	assets := repository.NewAssetRepo(db)
	albums := repository.NewAlbumRepo(db)
	linker := storage.NewLinker(mode, o.libraryRoot, o.photosDir)

	stats := &counters{}

	// ---- phase 1: users ---------------------------------------------------------
	log.Printf("migrate[users]: resuming after id %q", cp.LastID(batchUsers))
	if err := importUsers(ctx, pg, users, cp, o, stats); err != nil {
		return fmt.Errorf("import users: %w", err)
	}

	// ---- phase 2: assets (+ file relinking) --------------------------------------
	log.Printf("migrate[assets]: resuming after id %q", cp.LastID(batchAssets))
	if err := importAssets(ctx, pg, assets, cp, o, linker, stats); err != nil {
		return fmt.Errorf("import assets: %w", err)
	}

	// ---- phase 3: albums + membership ----------------------------------------------
	log.Printf("migrate[albums]: resuming after id %q", cp.LastID(batchAlbums))
	if err := importAlbums(ctx, pg, albums, cp, o, stats); err != nil {
		return fmt.Errorf("import albums: %w", err)
	}

	log.Printf("migrate: DONE in %s | users=%d assets=%d (linked=%d missing=%d failed=%d) albums=%d memberships=%d",
		time.Since(start).Round(time.Second), stats.users, stats.assets,
		stats.linked, stats.missing, stats.failed, stats.albums, stats.memberships)
	log.Printf("migrate: thumbnails will be rebuilt automatically by the server worker")
	return nil
}

type counters struct {
	users       int
	assets      int
	linked      int
	missing     int
	failed      int
	albums      int
	memberships int
}

// ---------- users ----------

func importUsers(ctx context.Context, pg *legacy.Client, repo *repository.UserRepo,
	cp *checkpoint.Store, o *options, st *counters) error {

	last := cp.LastID(batchUsers)
	total := int(cp.Count(batchUsers))
	st.users = total

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := pg.FetchUsers(ctx, last, o.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, lu := range rows {
			hash := lu.PasswordHash
			switch lu.PasswordKind() {
			case "bcrypt":
				// $2a$/$2b$/$2y$ all verify under golang.org/x/crypto/bcrypt.
			case "argon2id":
				if o.resetPassword == "" {
					log.Printf("migrate[user %s]: argon2id hash not verifiable in pure Go;"+
						" keeping hash but login will fail unless --reset-password is used", lu.ID)
				}
				fallthrough
			default:
				if o.resetPassword != "" {
					h, err := services.HashPassword(o.resetPassword)
					if err != nil {
						return err
					}
					hash = h
				}
			}
			u := &models.User{
				ID:           lu.ID,
				Email:        lu.Email,
				PasswordHash: hash,
				Name:         lu.Name,
				IsAdmin:      lu.IsAdmin,
				IsActive:     lu.IsActive,
				CreatedAt:    lu.CreatedAt,
			}
			if err := upsertUser(ctx, repo, u); err != nil {
				return fmt.Errorf("user %s: %w", lu.ID, err)
			}
			last = lu.ID
			total++
		}
		if err := cp.Save(batchUsers, last, int64(total)); err != nil {
			return err
		}
		log.Printf("migrate[users]: %d imported (checkpoint id=%s)", total, short(last))
		runtime.GC()
	}
}

func upsertUser(ctx context.Context, repo *repository.UserRepo, u *models.User) error {
	existing, err := repo.ByID(ctx, u.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return repo.Create(ctx, u)
	}
	// resume-safe: refresh mutable fields, keep nothing stale
	return repo.UpdatePassword(ctx, u.ID, u.PasswordHash)
}

// ---------- assets ----------

func importAssets(ctx context.Context, pg *legacy.Client, repo *repository.AssetRepo,
	cp *checkpoint.Store, o *options, linker *storage.Linker, st *counters) error {

	last := cp.LastID(batchAssets)
	total := int(cp.Count(batchAssets))
	st.assets = total

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := pg.FetchAssets(ctx, last, o.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for i := range rows {
			la := &rows[i]
			if la.IsTrashed && !o.includeTrashed {
				last = la.ID
				continue
			}
			date := la.Exif.DateTimeOriginal
			if date.IsZero() || date.Year() < 1980 {
				date = la.FileCreatedAt
			}
			dst := linker.TargetPath(la.OwnerID, la.ID, la.FileName, date)

			src, err := linker.Resolve(la.OriginalPath)
			switch {
			case errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file"):
				st.missing++
				log.Printf("migrate WARN [asset %s]: source file missing (%s) - importing DB row only",
					la.ID, la.OriginalPath)
				dst = "" // record the metadata; file endpoints will 404 cleanly
			case err != nil:
				st.failed++
				log.Printf("migrate WARN [asset %s]: resolve failed: %v - skipping", la.ID, err)
				last = la.ID
				continue
			default:
				if err := linker.Link(src, dst); err != nil {
					st.failed++
					log.Printf("migrate WARN [asset %s]: link failed: %v - skipping", la.ID, err)
					last = la.ID
					continue
				}
				st.linked++
			}

			asset := la.ToModel(dst)
			if asset.FileSize == 0 && dst != "" {
				if fi, err := statMaybeLink(dst); err == nil {
					asset.FileSize = fi.Size()
				}
			}
			if err := upsertAsset(ctx, repo, asset); err != nil {
				return fmt.Errorf("asset %s: %w", la.ID, err)
			}
			last = la.ID
			total++
			st.assets = total
		}
		if err := cp.Save(batchAssets, last, int64(total)); err != nil {
			return err
		}
		log.Printf("migrate[assets]: %d imported | linked=%d missing=%d failed=%d (checkpoint id=%s)",
			total, st.linked, st.missing, st.failed, short(last))
		runtime.GC() // release decoded buffers between batches (RAM ceiling)
	}
}

func upsertAsset(ctx context.Context, repo *repository.AssetRepo, a *models.Asset) error {
	existing, err := repo.ByID(ctx, a.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return repo.Create(ctx, a)
	}
	// Update path if the previous attempt had no file but now we found one.
	if existing.OriginalPath == "" && a.OriginalPath != "" {
		return repo.UpdatePath(ctx, a.ID, a.OriginalPath, a.FileSize)
	}
	return nil
}

func statMaybeLink(p string) (os.FileInfo, error) {
	fi, err := os.Stat(p) // follows symlinks - reports the real payload size
	return fi, err
}

// ---------- albums ----------

func importAlbums(ctx context.Context, pg *legacy.Client, repo *repository.AlbumRepo,
	cp *checkpoint.Store, o *options, st *counters) error {

	last := cp.LastID(batchAlbums)
	total := int(cp.Count(batchAlbums))
	st.albums = total

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := pg.FetchAlbums(ctx, last, o.batchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, la := range rows {
			al := &models.Album{
				ID:          la.ID,
				OwnerID:     la.OwnerID,
				AlbumName:   la.Name,
				Description: la.Description,
				CreatedAt:   la.CreatedAt,
				UpdatedAt:   la.UpdatedAt,
			}
			existing, err := repo.ByID(ctx, al.ID)
			if err != nil {
				return err
			}
			if existing == nil {
				if err := repo.Create(ctx, al); err != nil {
					return fmt.Errorf("album %s: %w", al.ID, err)
				}
			}
			st.albums++

			assetIDs, err := pg.AlbumAssets(ctx, la.ID)
			if err != nil {
				log.Printf("migrate WARN [album %s]: asset list unreadable: %v", la.ID, err)
				last = la.ID
				continue
			}
			added, failed, err := repo.AddAssets(ctx, al.ID, assetIDs)
			if err != nil {
				return fmt.Errorf("album assets %s: %w", al.ID, err)
			}
			st.memberships += len(added)
			if len(failed) > 0 {
				log.Printf("migrate WARN [album %s]: %d/%d assets not yet imported (run again later)",
					la.ID, len(failed), len(assetIDs))
			}
			last = la.ID
			total++
		}
		if err := cp.Save(batchAlbums, last, int64(total)); err != nil {
			return err
		}
		log.Printf("migrate[albums]: %d imported, %d memberships (checkpoint id=%s)",
			total, st.memberships, short(last))
		runtime.GC()
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
