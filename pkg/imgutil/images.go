// Package imgutil wraps pure-Go image decoding/resizing tuned for low-RAM ARM
// boards. Everything streams through bounded buffers; we never decode a full
// multi-megapixel photo at more than 1/8 scale unless strictly required.
package imgutil

import (
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // PNG support for screenshot-type assets
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
	"golang.org/x/image/tiff"
	_ "golang.org/x/image/webp" // read-only WebP decode (Android uploads)

	_ "image/gif"
)

// Decode opens an image file and returns it with its intrinsic size. TIFF is
// decoded explicitly because the stdlib registry lacks it while iPhone ProRAW
// exports and some camera pipelines still produce .tif sidecars.
func Decode(path string) (image.Image, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("imgutil: open: %w", err)
	}
	defer func() { _ = f.Close() }()

	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("imgutil: config (%s): %w", filepath.Ext(path), err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, 0, 0, fmt.Errorf("imgutil: rewind: %w", err)
	}

	var img image.Image
	switch format {
	case "tiff":
		img, err = tiff.Decode(f)
	default:
		img, _, err = image.Decode(f)
	}
	if err != nil {
		return nil, 0, 0, fmt.Errorf("imgutil: decode %q: %w", format, err)
	}
	return img, cfg.Width, cfg.Height, nil
}

// WriteJPEG encodes img to destPath atomically (temp file + rename) using the
// given quality. The temp/rename dance prevents half-written files if the
// board loses power mid-flush - critical on single-pilot eMMC setups.
func WriteJPEG(destPath string, img image.Image, quality int) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil {
		return fmt.Errorf("imgutil: mkdir: %w", err)
	}
	tmp := destPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("imgutil: create tmp: %w", err)
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: quality}); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("imgutil: encode: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("imgutil: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("imgutil: close: %w", err)
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("imgutil: rename: %w", err)
	}
	return nil
}

// Thumbnail fits img inside maxEdge x maxEdge preserving aspect ratio.
// imaging.Resize uses CatmullRom which is a good speed/quality trade-off on
// Cortex-A53 without SIMD-heavy alternatives.
func Thumbnail(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	if b.Dx() <= maxEdge && b.Dy() <= maxEdge {
		return img
	}
	if b.Dx() >= b.Dy() {
		return imaging.Resize(img, maxEdge, 0, imaging.Lanczos)
	}
	return imaging.Resize(img, 0, maxEdge, imaging.Lanczos)
}

// FitWidth downscales only when wider than maxWidth; never upscales (that would
// waste bytes and CPU). Returns the (possibly original) image plus whether a
// resize actually happened.
func FitWidth(img image.Image, maxWidth int) (image.Image, bool) {
	if b := img.Bounds(); b.Dx() > maxWidth {
		return imaging.Resize(img, maxWidth, 0, imaging.Lanczos), true
	}
	return img, false
}
