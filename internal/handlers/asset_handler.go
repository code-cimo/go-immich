package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"go-immich/internal/middleware"
	"go-immich/internal/models"
	"go-immich/internal/repository"
)

// AssetHandler serves the gallery/timeline read endpoints.
type AssetHandler struct {
	assets *repository.AssetRepo
}

// NewAssetHandler wires dependencies.
func NewAssetHandler(assets *repository.AssetRepo) *AssetHandler {
	return &AssetHandler{assets: assets}
}

// List handles GET /api/asset?take=&skip=&isFavorite=&isArchived=&isTrashed=&type=
// The official endpoint is POST /assets/browse etc., but mobile timeline calls
// GET /api/asset with these exact query keys; we honour all of them.
func (h *AssetHandler) List(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}

	f := repository.ListFilter{OwnerID: user.ID}
	if v := c.Query("take"); v != "" {
		f.Take, _ = strconv.Atoi(v)
	}
	if v := c.Query("skip"); v != "" {
		f.Skip, _ = strconv.Atoi(v)
	}
	if v := c.Query("isFavorite"); v != "" {
		b := parseBool(v)
		f.IsFavorite = &b
	}
	if v := c.Query("isArchived"); v != "" {
		b := parseBool(v)
		f.IsArchived = &b
	}
	if v := c.Query("isTrashed"); v != "" {
		b := parseBool(v)
		f.IsTrashed = &b
	}
	if v := c.Query("type"); v != "" {
		f.Type = normalizeType(v)
	}

	items, err := h.assets.List(c.Request.Context(), f)
	if err != nil {
		replyError(c, http.StatusInternalServerError, "Could not load assets")
		return
	}
	c.JSON(http.StatusOK, items)
}

// Get handles GET /api/asset/:id - single asset detail.
func (h *AssetHandler) Get(c *gin.Context) {
	if _, ok := middleware.CurrentUser(c); !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	a, err := h.assets.ByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		replyError(c, http.StatusInternalServerError, "Could not load asset")
		return
	}
	if a == nil || a.IsTrashed {
		c.JSON(http.StatusNotFound, APIError{
			Error: "Not Found", Message: "Asset not found", StatusCode: http.StatusNotFound,
		})
		return
	}
	c.JSON(http.StatusOK, a.ToResponse())
}

// Delete handles DELETE /api/asset/:id (soft trash like the official server).
func (h *AssetHandler) Delete(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	a, err := h.assets.ByID(c.Request.Context(), c.Param("id"))
	if err != nil || a == nil {
		replyError(c, http.StatusNotFound, "Asset not found")
		return
	}
	if a.OwnerID != user.ID && !user.IsAdmin {
		replyError(c, http.StatusForbidden, "You do not own access to this resource")
		return
	}
	if err := h.assets.Trash(c.Request.Context(), []string{a.ID}); err != nil {
		replyError(c, http.StatusInternalServerError, "Could not trash asset")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleting"})
}

func parseBool(s string) bool {
	switch s {
	case "1", "true", "TRUE", "yes", "on":
		return true
	default:
		return false
	}
}

func normalizeType(t string) string {
	switch models.AssetType(t) {
	case models.AssetTypeImage:
		return string(models.AssetTypeImage)
	case models.AssetTypeVideo:
		return string(models.AssetTypeVideo)
	case models.AssetTypeAudio:
		return string(models.AssetTypeAudio)
	default:
		return ""
	}
}
