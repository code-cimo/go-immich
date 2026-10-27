// Package worker implements the low-priority background pipeline that turns
// freshly uploaded photos into thumbnails and (optionally) recompressed
// originals. It is engineered around two ODROID-C2 realities:
//
//  1. CPU budget: the Amlogic S905 must stay responsive for the foreground
//     user, so processing sleeps proportionally to the time it just burned
//     (dynamic duty cycle targeting ~cfg.WorkerMaxCPUPct of one core).
//  2. RAM budget: at most cfg.ResizeConcurrency images are decoded at once;
//     when runtime.MemStats.Sys approaches cfg.MemoryLimitBytes the worker
//     idles an extra cycle instead of risking the OOM killer.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"go-immich/config"
	"go-immich/internal/models"
	"go-immich/internal/repository"
	"go-immich/pkg/imgutil"
)

// CompressWorker polls SQLite for un-processed IMAGE assets.
type CompressWorker struct {
	cfg    *config.Config
	assets *repository.AssetRepo
	sem    chan struct{} // resize concurrency limiter (<=2 on the C2)
}

// NewCompressWorker builds the worker with its semaphore pre-sized.
func NewCompressWorker(cfg *config.Config, assets *repository.AssetRepo) *CompressWorker {
	return &CompressWorker{
		cfg:    cfg,
		assets: assets,
		sem:    make(chan struct{}, cfg.ResizeConcurrency),
	}
}

// Run blocks until ctx is cancelled. Returns only on shutdown.
func (w *CompressWorker) Run(ctx context.Context) error {
	log.Printf("worker: compression pipeline online (interval=%s cpuBudget=%d%% parallelism=%d)",
		w.cfg.WorkerInterval, w.cfg.WorkerMaxCPUPct, w.cfg.ResizeConcurrency)

	ticker := time.NewTicker(w.cfg.WorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("worker: shutting down")
			return nil
		case <-ticker.C:
			if w.overMemory() {
				log.Printf("worker: memory above soft limit - skipping cycle")
				continue
			}
			w.processBatch(ctx)
			// Adaptive back-off: stretch the wait when we just spent real CPU.
			if extra := w.dutyCycleSleep(); extra > 0 {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(extra):
				}
			}
		}
	}
}

// processBatch grabs a small page of pending images and fans them out through
// the semaphore-limited pool, waiting for all of them before returning so at
// most `concurrency` decodes are ever in flight.
func (w *CompressWorker) processBatch(ctx context.Context) {
	pending, err := w.assets.PendingImages(ctx, w.cfg.ResizeConcurrency*2)
	if err != nil {
		log.Printf("worker: queue query failed: %v", err)
		return
	}
	if len(pending) == 0 {
		lastCycle = 0 // nothing burned -> no extra sleep
		return
	}

	var wg sync.WaitGroup
	start := time.Now()
	for _, a := range pending {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case w.sem <- struct{}{}:
		}
		wg.Add(1)
		go func(asset *models.Asset) {
			defer func() { <-w.sem; wg.Done() }()
			if err := w.handleAsset(ctx, asset); err != nil {
				// Missing files or undecodable formats must not kill the loop.
				log.Printf("worker: asset %s skipped: %v", asset.ID, err)
			}
		}(a)
	}
	wg.Wait()
	lastCycle = time.Since(start)
	runtime.GC() // hand pages back to the OS between batches (eMMC-friendly)
}

var lastCycle time.Duration

// handleAsset performs the full pipeline for one image:
// decode -> thumbnail -> optional recompress/resize original -> DB update.
func (w *CompressWorker) handleAsset(ctx context.Context, a *models.Asset) error {
	if _, err := os.Stat(a.OriginalPath); err != nil {
		return fmt.Errorf("original missing: %w", err)
	}

	img, w0, h0, err := imgutil.Decode(a.OriginalPath)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if w0 > 0 && h0 > 0 && (w0 != a.Width || h0 != a.Height) {
		if err := w.assets.UpdateDimensions(ctx, a.ID, w0, h0); err != nil {
			log.Printf("worker: dims update %s: %v", a.ID, err)
		}
	}

	// ---- thumbnail ---------------------------------------------------------
	thumbPath := w.thumbPathFor(a)
	if err := imgutil.WriteJPEG(thumbPath, imgutil.Thumbnail(img, w.cfg.ThumbnailSize), 80); err != nil {
		return fmt.Errorf("thumbnail: %w", err)
	}

	// ---- recompression of the original (only JPEGs wider than the cap) -----
	resized := false
	newSize := a.FileSize
	ext := filepath.Ext(a.FileName)
	if (ext == ".jpg" || ext == ".jpeg") && w0 > w.cfg.ResizeMaxWidth {
		fitted, changed := imgutil.FitWidth(img, w.cfg.ResizeMaxWidth)
		if changed {
			tmpPath := a.OriginalPath + ".recompressed"
			if err := imgutil.WriteJPEG(tmpPath, fitted, w.cfg.ResizeQuality); err != nil {
				_ = os.Remove(tmpPath)
				return fmt.Errorf("recompress: %w", err)
			}
			fi, err := os.Stat(tmpPath)
			if err != nil {
				_ = os.Remove(tmpPath)
				return fmt.Errorf("stat recompressed: %w", err)
			}
			// Only adopt the recompressed file if it is actually smaller -
			// otherwise keep the original bytes (never inflate storage).
			if fi.Size() < a.FileSize {
				if err := os.Rename(tmpPath, a.OriginalPath); err != nil {
					_ = os.Remove(tmpPath)
					return fmt.Errorf("replace original: %w", err)
				}
				resized = true
				newSize = fi.Size()
			} else {
				_ = os.Remove(tmpPath)
			}
		}
	}

	return w.assets.MarkThumbDone(ctx, a.ID, thumbPath, resized, newSize)
}

// thumbPathFor mirrors the requested layout:
// /data/thumbnails/[user]/[YYYY]/[MM]/[DD]/[assetid]_thumb.jpg
func (w *CompressWorker) thumbPathFor(a *models.Asset) string {
	day := a.CreatedAt
	if !a.LocalDate.IsZero() {
		day = a.LocalDate
	}
	return filepath.Join(w.cfg.ThumbnailsDir, a.OwnerID,
		fmt.Sprintf("%04d", day.Year()), fmt.Sprintf("%02d", int(day.Month())),
		fmt.Sprintf("%02d", day.Day()), a.ID+"_thumb.jpg")
}

// overMemory reports whether the process already sits above the soft ceiling.
func (w *CompressWorker) overMemory() bool {
	if w.cfg.MemoryLimitBytes == 0 {
		return false
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Alloc+ms.Sys/8 > w.cfg.MemoryLimitBytes
}

// dutyCycleSleep converts the previous batch wall-time into an idle period so
// the average CPU share stays near WorkerMaxCPUPct percent of one core.
// Formula: busy/(budget) - busy  =>  busy*(100/budget - 1).
func (w *CompressWorker) dutyCycleSleep() time.Duration {
	budget := w.cfg.WorkerMaxCPUPct
	if budget <= 0 || budget >= 100 || lastCycle <= 0 {
		return 0
	}
	sleep := time.Duration(float64(lastCycle) * (100.0/float64(budget) - 1.0))
	const cap = 5 * time.Minute
	if sleep > cap {
		sleep = cap
	}
	return sleep
}

// ErrNoWork signals an intentional empty pass (kept for future tests).
var ErrNoWork = errors.New("worker: nothing to do")
