package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register jpeg decoder for DecodeConfig
	_ "image/png"  // register png decoder
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rwcarlsen/goexif/exif"

	"go-immich/config"
	"go-immich/internal/middleware"
	"go-immich/internal/models"
	"go-immich/internal/repository"
)

// streamBufferSize is deliberately tiny (32KB). On a 2GB board with concurrent
// uploads, large per-part buffers multiply RAM usage; io.Copy at 32KB keeps
// each in-flight upload under a page-cache-friendly footprint.
const streamBufferSize = 32 * 1024

// maxMemoryFileBytes bounds how much of one multipart part gin may buffer in
// RAM before spilling to an OS temp file - the key knob against OOM spikes.
const maxMemoryFileBytes = 8 << 20 // 8 MiB

// UploadHandler implements POST /api/assets (multipart) and the duplicate
// check endpoint the mobile app calls before uploading.
type UploadHandler struct {
	cfg   *config.Config
	assets *repository.AssetRepo
}

// NewUploadHandler wires dependencies.
func NewUploadHandler(cfg *config.Config, assets *repository.AssetRepo) *UploadHandler {
	return &UploadHandler{cfg: cfg, assets: assets}
}

// assetMeta accumulates everything extracted from one upload part.
type assetMeta struct {
	dateTimeOrig time.Time
	lat, lon     float64
	make, model  string
	lens         string
	desc         string
	width, height int
}

// Create handles POST /api/assets.
//
// Immich mobile sends multipart/form-data with parts named "assetData" (the
// binary) and "metadata" (JSON with deviceAssetId/createdAt). Older clients
// use arbitrary part names, so we accept any file part.
func (h *UploadHandler) Create(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}

	// Bound in-RAM buffering for this request (defence against OOM).
	if err := c.Request.ParseMultipartForm(maxMemoryFileBytes); err != nil {
		replyError(c, http.StatusBadRequest, "Invalid multipart payload: "+err.Error())
		return
	}
	defer func() { _ = c.Request.MultipartForm.RemoveAll() }()

	fileHeader, err := pickFilePart(c)
	if err != nil {
		replyError(c, http.StatusBadRequest, err.Error())
		return
	}

	// client checksum (base64 sha256) if provided via query param
	clientChecksum := strings.ToLower(c.Query("checksum"))

	deviceAssetID := c.Query("deviceAssetId")
	if v := c.PostForm("deviceAssetId"); v != "" {
		deviceAssetID = v
	}

	assetID := uuid.NewString()
	now := time.Now().UTC()

	// ---- storage path: /data/photos/[user]/[YYYY]/[MM]/[DD]/[name] ----------
	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	storedName := sanitizeName(strings.TrimSuffix(fileHeader.Filename, ext)) + ext
	dir := filepath.Join(h.cfg.PhotosDir, user.ID,
		fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", now.Month()), fmt.Sprintf("%02d", now.Day()))
	finalPath := filepath.Join(dir, assetID+storedName) // id prefix guarantees uniqueness on disk

	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Printf("upload: mkdir %s: %v", dir, err)
		replyError(c, http.StatusInternalServerError, "Storage unavailable")
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		replyError(c, http.StatusBadRequest, "Could not read upload part")
		return
	}
	defer func() { _ = src.Close() }()

	dst, err := os.Create(finalPath)
	if err != nil {
		log.Printf("upload: create %s: %v", finalPath, err)
		replyError(c, http.StatusInternalServerError, "Could not store file")
		return
	}

	// hash while streaming: zero extra passes over the file.
	hasher := sha256.New()
	buf := make([]byte, streamBufferSize) // explicit small buffer per spec
	written, err := writeWithBuffer(dst, io.TeeReader(src, hasher), buf)
	closeErr := dst.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(finalPath)
		if err == nil {
			err = closeErr
		}
		log.Printf("upload: stream write: %v", err)
		replyError(c, http.StatusInternalServerError, "Could not store file")
		return
	}

	shaHex := hex.EncodeToString(hasher.Sum(nil))

	// ---- de-duplication (SHA256 per owner) ----------------------------------
	if existingID, dup, err := h.assets.ExistsByChecksum(c, user.ID, shaHex); err == nil && dup {
		_ = os.Remove(finalPath) // discard the duplicate bytes immediately
		c.JSON(http.StatusConflict, APIError{
			Error:      "Conflict",
			Message:    fmt.Sprintf("Upload conflict. Duplicate file exists as asset %s", existingID),
			StatusCode: http.StatusConflict,
		})
		return
	} else if err != nil {
		log.Printf("upload: dedupe check failed (continuing): %v", err)
	}

	mimeType := detectType(fileHeader.Header.Get("Content-Type"), storedName)
	meta := extractMeta(mimeType, finalPath, written)

	localDate := meta.dateTimeOrig
	if localDate.IsZero() {
		localDate = now
	}

	asset := &models.Asset{
		ID:               assetID,
		DeviceAssetID:    firstNonEmpty(deviceAssetID, clientChecksum, assetID),
		OwnerID:          user.ID,
		OriginalPath:     finalPath,
		Type:             typeFromMime(mimeType),
		MimeType:         mimeType,
		FileName:         fileHeader.Filename,
		FileCreatedAt:    now,
		FileModifiedAt:   now,
		ChecksumSHA256:   shaHex,
		IsVisible:        true,
		FileSize:         written,
		Width:            meta.width,
		Height:           meta.height,
		DateTimeOrig:     meta.dateTimeOrig,
		LocalDate:        localDate,
		Lat:              meta.lat,
		Lon:              meta.lon,
		Make:             meta.make,
		Model:            meta.model,
		LensModel:        meta.lens,
		ImageDescription: meta.desc,
		ProjectionType:   "EQUIRECTANGULAR",
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := h.assets.Create(c.Request.Context(), asset); err != nil {
		if errors.Is(err, repository.ErrDuplicateChecksum) {
			_ = os.Remove(finalPath)
			c.JSON(http.StatusConflict, APIError{
				Error: "Conflict", Message: "Upload conflict", StatusCode: http.StatusConflict,
			})
			return
		}
		_ = os.Remove(finalPath)
		log.Printf("upload: db insert: %v", err)
		replyError(c, http.StatusInternalServerError, "Could not register asset")
		return
	}

	c.JSON(http.StatusOK, asset.ToResponse())
}

// CheckDuplicates answers POST /api/assets/check (body: base64 checksum array).
// The mobile app uses it to skip re-uploading known files; we must answer with
// the subset that already exists (raw hex checksums are returned by the app's
// own conversion, official server echoes base64 - we echo input verbatim).
func (h *UploadHandler) CheckDuplicates(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	var checksums []string
	if err := c.ShouldBindJSON(&checksums); err != nil {
		replyError(c, http.StatusBadRequest, "Invalid checksum list")
		return
	}
	duplicates := make([]string, 0)
	for _, b64 := range checksums {
		raw, err := decodeMaybeBase64(b64)
		if err != nil {
			continue
		}
		hexSum := hex.EncodeToString(raw)
		if _, dup, err := h.assets.ExistsByChecksum(c.Request.Context(), user.ID, hexSum); err == nil && dup {
			duplicates = append(duplicates, b64)
		}
	}
	c.JSON(http.StatusOK, duplicates)
}

// ---------- helpers ----------

// pickFilePart finds the binary part: prefer "assetData" (current mobile),
// then "data"/"file", finally the first non-metadata part.
func pickFilePart(c *gin.Context) (*multipart.FileHeader, error) {
	form := c.Request.MultipartForm
	order := []string{"assetData", "data", "file"}
	for _, name := range order {
		if headers, ok := form.File[name]; ok && len(headers) > 0 {
			return headers[0], nil
		}
	}
	for name, headers := range form.File {
		if name == "metadata" {
			continue
		}
		if len(headers) > 0 {
			return headers[0], nil
		}
	}
	return nil, errors.New("no file part found in upload")
}

func writeWithBuffer(dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	// io.CopyBuffer keeps our 32KB window instead of allocating its own 32KB.
	return io.CopyBuffer(dst, src, buf)
}

func detectType(declared, filename string) string {
	if declared != "" && declared != "application/octet-stream" {
		return declared
	}
	ext := filepath.Ext(filename)
	if t := mime.TypeByExtension(ext); t != "" {
		// mime.TypeByExtension returns "image/jpeg; charset=utf-8" style values
		if i := strings.IndexByte(t, ';'); i > 0 {
			t = t[:i]
		}
		return strings.TrimSpace(t)
	}
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".heic":
		return "image/heic"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".mp4", ".mov":
		return "video/mp4"
	case ".3gp":
		return "video/3gpp"
	case ".mkv":
		return "video/x-matroska"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	default:
		return "application/octet-stream"
	}
}

func typeFromMime(m string) models.AssetType {
	switch {
	case strings.HasPrefix(m, "image/"):
		return models.AssetTypeImage
	case strings.HasPrefix(m, "video/"):
		return models.AssetTypeVideo
	case strings.HasPrefix(m, "audio/"):
		return models.AssetTypeAudio
	default:
		return models.AssetTypeImage
	}
}

// extractMeta pulls EXIF date/GPS/camera plus pixel dimensions for images.
// Failures are non-fatal: an asset without metadata is still a valid asset.
func extractMeta(mimeType, path string, size int64) assetMeta {
	meta := assetMeta{}
	if !strings.HasPrefix(mimeType, "image/") {
		return meta
	}
	f, err := os.Open(path)
	if err != nil {
		return meta
	}
	defer func() { _ = f.Close() }()

	// goexif only needs the header region; cap the scan to 8MB to bound memory.
	x, err := exif.Decode(io.LimitReader(f, 8<<20))
	if err == nil {
		if dt, err := x.DateTime(); err == nil {
			meta.dateTimeOrig = dt.UTC()
		}
		if lat, long, err := x.LatLong(); err == nil {
			meta.lat, meta.lon = lat, long
		}
		meta.make = exifTag(x, exif.Make)
		meta.model = exifTag(x, exif.Model)
		meta.lens = exifTag(x, exif.LensModel)
		meta.desc = exifTag(x, exif.ImageDescription)
	}

	if w, hgt, err := readPixelDims(path); err == nil {
		meta.width, meta.height = w, hgt
	}
	_ = size
	return meta
}

func exifTag(x *exif.Exif, name exif.FieldName) string {
	v, err := x.Get(name)
	if err != nil {
		return ""
	}
	s, err := v.StringVal()
	if err != nil {
		return strings.TrimSpace(fmt.Sprintf("%s", v))
	}
	return strings.TrimSpace(s)
}

func readPixelDims(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := decodeConfigSafe(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func sanitizeName(name string) string {
	name = filepath.Base(name)
	replace := map[string]string{"/": "_", "\\": "_", ":": "_", "*": "_", "?": "_", "\"": "_", "<": "_", ">": "_", "|": "_"}
	for k, v := range replace {
		name = strings.ReplaceAll(name, k, v)
	}
	if name == "" || name == "." || name == ".." {
		return "unnamed"
	}
	if len(name) > 128 {
		name = name[len(name)-128:]
	}
	return name
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
