// Package storage implements the file-linking half of the legacy migration:
// mapping an old Immich library path (hash folders or storage template) onto
// the new flat hierarchy /data/photos/<user>/<YYYY>/<MM>/<DD>/<file>.
//
// Two strategies are supported and both are I/O-throttled because the target
// board (ODROID-C2) reads/writes from an SD card or eMMC that saturates fast:
//
//   - symlink: zero-copy, instant, recommended (files stay on the old volume)
//   - copy:    32KB buffered stream + periodic short sleeps to yield the bus
package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StreamBufferSize matches the upload handler: small windows keep RSS flat.
const StreamBufferSize = 32 * 1024

// Mode selects the linking strategy.
type Mode string

const (
	ModeSymlink Mode = "symlink"
	ModeCopy    Mode = "copy"
)

// ParseMode validates a CLI flag value.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "symlink", "s":
		return ModeSymlink, nil
	case "copy", "c":
		return ModeCopy, nil
	default:
		return "", fmt.Errorf("unknown mode %q (want symlink|copy)", s)
	}
}

// Linker resolves legacy paths and materialises them in the new layout.
type Linker struct {
	Mode         Mode
	LibraryRoot  string      // root of the old upload/library directory
	PhotosDir    string      // new /data/photos root
	PauseEvery   int         // sleep once per N copied bytes chunks (0 disables)
	PauseFor     time.Duration
	chunksSince  int
}

// NewLinker builds a Linker with sane throttle defaults for an SBC.
func NewLinker(mode Mode, libraryRoot, photosDir string) *Linker {
	return &Linker{
		Mode:        mode,
		LibraryRoot: libraryRoot,
		PhotosDir:   photosDir,
		PauseEvery:  64, // ~2MB of copies then rest
		PauseFor:    50 * time.Millisecond,
	}
}

// Resolve maps a legacy originalPath (as stored in the old DB) to a real file
// on disk. Immich historically used several layouts; we try the common ones:
//
//	<root>/library/<originalPath>          (storage template, v1.7x+)
//	<root>/<originalPath>                  (path already absolute-ish)
//	<root>/upload/library/<originalPath>   (docker default mount)
//
// An absolute originalPath is honoured verbatim. Returns os.ErrNotExist when
// no candidate exists - callers log a warning and continue (resilient import).
func (l *Linker) Resolve(originalPath string) (string, error) {
	p := strings.TrimPrefix(filepath.ToSlash(originalPath), "./")
	var candidates []string
	if filepath.IsAbs(p) {
		candidates = append(candidates, p)
	} else {
		candidates = append(candidates,
			filepath.Join(l.LibraryRoot, "library", p),
			filepath.Join(l.LibraryRoot, p),
			filepath.Join(l.LibraryRoot, "upload", "library", p),
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("%w: %s (tried under %s)", os.ErrNotExist, originalPath, l.LibraryRoot)
}

// TargetPath computes the destination for one asset in the new layout:
// <photosDir>/<ownerID>/<YYYY>/<MM>/<DD>/<assetID>-<sanitised filename>.
func (l *Linker) TargetPath(ownerID, assetID, fileName string, date time.Time) string {
	y, m, d := date.UTC().Date()
	name := sanitize(strings.TrimSuffix(fileName, filepath.Ext(fileName))) + filepath.Ext(fileName)
	if name == "" {
		name = assetID
	}
	return filepath.Join(l.PhotosDir, ownerID,
		fmt.Sprintf("%04d", y), fmt.Sprintf("%02d", int(m)), fmt.Sprintf("%02d", d),
		assetID+"-"+name)
}

// Link creates the destination entry for one resolved source file according to
// the configured mode. It is idempotent: an existing correct destination is
// treated as success (checkpoint re-runs must not fail).
func (l *Linker) Link(srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
		return fmt.Errorf("storage: mkdir: %w", err)
	}
	if _, err := os.Lstat(dstPath); err == nil {
		return nil // already linked/copied from a previous run
	}
	switch l.Mode {
	case ModeSymlink:
		abs, err := filepath.Abs(srcPath)
		if err != nil {
			return err
		}
		return os.Symlink(abs, dstPath)
	case ModeCopy:
		return l.copyFile(srcPath, dstPath)
	default:
		return errors.New("storage: linker mode unset")
	}
}

// copyFile streams src -> dst with our fixed 32KB window, inserting tiny
// pauses so the SD-card bus stays available for foreground traffic.
func (l *Linker) copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("storage: open src: %w", err)
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("storage: create dst: %w", err)
	}

	buf := make([]byte, StreamBufferSize)
	written, err := io.CopyBuffer(out, in, buf)
	if err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: copy %s (%d bytes): %w", dst, written, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: fsync: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: close: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("storage: rename: %w", err)
	}

	l.throttle(written)
	return nil
}

// throttle rests after PauseEvery full-buffer chunks of copied data.
func (l *Linker) throttle(bytesCopied int64) {
	if l.PauseEvery <= 0 || l.PauseFor <= 0 {
		return
	}
	l.chunksSince += int(bytesCopied/int64(StreamBufferSize)) + 1
	if l.chunksSince >= l.PauseEvery {
		l.chunksSince = 0
		time.Sleep(l.PauseFor)
	}
}

func sanitize(name string) string {
	name = filepath.Base(name)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	name = replacer.Replace(name)
	if len(name) > 128 {
		name = name[len(name)-128:]
	}
	if name == "" {
		return "unnamed"
	}
	return name
}
