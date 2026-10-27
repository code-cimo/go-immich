package handlers

import (
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"go-immich/config"
	"go-immich/internal/middleware"
	"go-immich/internal/repository"
)

// FileHandler streams thumbnails and originals off disk. It performs no
// transcoding; everything it serves was produced at upload time or by the
// background worker.
type FileHandler struct {
	cfg    *config.Config
	assets *repository.AssetRepo
}

// NewFileHandler wires dependencies.
func NewFileHandler(cfg *config.Config, assets *repository.AssetRepo) *FileHandler {
	return &FileHandler{cfg: cfg, assets: assets}
}

// Thumbnail handles GET /api/asset/:id/thumbnail.
// Cache-Control is 1 year because thumbnail bytes are immutable per asset id.
func (h *FileHandler) Thumbnail(c *gin.Context) {
	a := h.resolveAsset(c)
	if a == nil {
		return
	}
	path := a.ThumbPath
	if path == "" {
		// Worker has not caught up yet: fall back to the original so the
		// gallery never shows broken images (clients tolerate the size hit).
		path = a.OriginalPath
	}
	h.serveFile(c, path, "image/jpeg", true /*immutable*/, 0)
}

// Original handles GET /api/asset/:id/original.
// Gin's http.ServeContent gives us Range support for free - required for
// video seeking on the mobile player.
func (h *FileHandler) Original(c *gin.Context) {
	a := h.resolveAsset(c)
	if a == nil {
		return
	}
	mimeType := a.MimeType
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = guessMime(a.FileName)
	}
	h.serveFile(c, a.OriginalPath, mimeType, false, a.FileSize)
}

// resolveAsset loads the asset or writes the error response itself.
func (h *FileHandler) resolveAsset(c *gin.Context) *repository.ResolvedAsset {
	if _, ok := middleware.CurrentUser(c); !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return nil
	}
	a, err := h.assets.ByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		replyError(c, http.StatusInternalServerError, "Could not load asset")
		return nil
	}
	if a == nil {
		c.JSON(http.StatusNotFound, APIError{
			Error: "Not Found", Message: "Asset not found", StatusCode: http.StatusNotFound,
		})
		return nil
	}
	return &repository.ResolvedAsset{Asset: a}
}

// serveFile opens p and pipes it with an explicit 32KB buffer via
// http.ServeContent (which internally uses io.Copy against our ReadSeeker).
func (h *FileHandler) serveFile(c *gin.Context, p string, contentType string, immutable bool, declaredSize int64) {
	if p == "" {
		replyError(c, http.StatusNotFound, "File missing")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			replyError(c, http.StatusNotFound, "File missing")
			return
		}
		log.Printf("file: open %s: %v", p, err)
		replyError(c, http.StatusInternalServerError, "Could not read file")
		return
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		replyError(c, http.StatusInternalServerError, "Could not stat file")
		return
	}

	// Immich sets long-lived caching on media responses.
	extra := ""
	if immutable {
		extra = "public, max-age=31536000, immutable"
		c.Header("Cache-Control", extra)
	} else {
		c.Header("Cache-Control", "public, max-age=86400")
	}
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	if declaredSize > 0 {
		c.Header("Accept-Ranges", "bytes")
		c.Header("Content-Length", strconv.FormatInt(declaredSize, 10))
	}

	http.ServeContent(c.Writer, c.Request, filepath.Base(p), fi.ModTime(), f)
}

func guessMime(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".heic":
		return "image/heic"
	case ".mp4", ".mov", ".m4v":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}
