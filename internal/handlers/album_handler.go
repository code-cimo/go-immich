package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go-immich/internal/middleware"
	"go-immich/internal/models"
	"go-immich/internal/repository"
)

// AlbumHandler implements /api/album CRUD + asset membership management.
type AlbumHandler struct {
	albums *repository.AlbumRepo
	assets *repository.AssetRepo
}

// NewAlbumHandler wires dependencies.
func NewAlbumHandler(albums *repository.AlbumRepo, assets *repository.AssetRepo) *AlbumHandler {
	return &AlbumHandler{albums: albums, assets: assets}
}

// createAlbumDTO is the POST body accepted by the official endpoint.
type createAlbumDTO struct {
	AlbumName   string   `json:"albumName" binding:"required"`
	Description string   `json:"description"`
	AssetIDs    []string `json:"assetIds"`
	Order       int32    `json:"order"`
}

type albumAssetsDTO struct {
	AssetIDs []string `json:"assetIds" binding:"required"`
}

// albumResponse mirrors AlbumResponseDto of the OpenAPI schema.
type albumResponse struct {
	ID                  string                    `json:"id"`
	OwnerID             string                    `json:"ownerId"`
	Name                string                    `json:"albumName"`
	Description         string                    `json:"description"`
	CreatedAt           time.Time                 `json:"createdAt"`
	UpdatedAt           time.Time                 `json:"updatedAt"`
	AlbumThumbnailAsset map[string]any            `json:"albumThumbnailAsset"`
	AssetCount          int64                     `json:"assetCount"`
	Assets              []models.AssetResponseDto `json:"assets"`
	AlbumUsers          []map[string]any          `json:"albumUsers"`
	Shared              bool                      `json:"shared"`
	HasSharedLink       bool                      `json:"hasSharedLink"`
	Order               int32                     `json:"order"`
	OriginalAssets      []map[string]any          `json:"originalAssets"`
	StackCount          int                       `json:"stackCount"`
	IsActivityEnabled   bool                      `json:"isActivityEnabled"`
	LastModifiedAssetTimestamp *string             `json:"lastModifiedAssetTimestamp"`
}

// Create handles POST /api/album.
func (h *AlbumHandler) Create(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	var body createAlbumDTO
	if err := c.ShouldBindJSON(&body); err != nil {
		replyError(c, http.StatusBadRequest, "Invalid album payload")
		return
	}
	a := &models.Album{
		ID:          uuid.NewString(),
		OwnerID:     user.ID,
		AlbumName:   body.AlbumName,
		Description: body.Description,
		Order:       body.Order,
	}
	if err := h.albums.Create(c.Request.Context(), a); err != nil {
		replyError(c, http.StatusInternalServerError, "Could not create album")
		return
	}
	if len(body.AssetIDs) > 0 {
		added, _, _ := h.albums.AddAssets(c.Request.Context(), a.ID, body.AssetIDs)
		_ = added
	}
	c.JSON(http.StatusOK, h.render(c, a.ID, false))
}

// List handles GET /api/album (all albums of the caller, with counts).
func (h *AlbumHandler) List(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	rows, err := h.albums.ListByOwner(c.Request.Context(), user.ID)
	if err != nil {
		replyError(c, http.StatusInternalServerError, "Could not list albums")
		return
	}
	out := make([]albumResponse, 0, len(rows))
	for _, wc := range rows {
		out = append(out, albumResponse{
			ID:          wc.Album.ID,
			OwnerID:     wc.Album.OwnerID,
			Name:        wc.Album.AlbumName,
			Description: wc.Album.Description,
			CreatedAt:   wc.Album.CreatedAt.UTC(),
			UpdatedAt:   wc.Album.UpdatedAt.UTC(),
			AssetCount:  wc.AssetCount,
			Assets:      []models.AssetResponseDto{},
			AlbumUsers:  []map[string]any{},
			Order:       wc.Album.Order,
		})
	}
	c.JSON(http.StatusOK, out)
}

// Get handles GET /api/album/:id including its assets.
func (h *AlbumHandler) Get(c *gin.Context) {
	if _, ok := middleware.CurrentUser(c); !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	body := h.render(c, c.Param("id"), true)
	if body == nil {
		return // render already answered 404
	}
	c.JSON(http.StatusOK, body)
}

// AddAssets handles PUT /api/album/:id/assets.
// Response matches AssetIdsDto: {"added":[...],"failed":[...]}.
func (h *AlbumHandler) AddAssets(c *gin.Context) {
	if _, ok := middleware.CurrentUser(c); !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	var body albumAssetsDTO
	if err := c.ShouldBindJSON(&body); err != nil {
		replyError(c, http.StatusBadRequest, "Invalid asset id list")
		return
	}
	added, failed, err := h.albums.AddAssets(c.Request.Context(), c.Param("id"), body.AssetIDs)
	if err != nil {
		replyError(c, http.StatusInternalServerError, "Could not update album")
		return
	}
	c.JSON(http.StatusOK, gin.H{"added": added, "failed": failed})
}

// RemoveAssets handles DELETE /api/album/:id/assets.
func (h *AlbumHandler) RemoveAssets(c *gin.Context) {
	if _, ok := middleware.CurrentUser(c); !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	var body albumAssetsDTO
	if err := c.ShouldBindJSON(&body); err != nil {
		replyError(c, http.StatusBadRequest, "Invalid asset id list")
		return
	}
	removed, failed, err := h.albums.RemoveAssets(c.Request.Context(), c.Param("id"), body.AssetIDs)
	if err != nil {
		replyError(c, http.StatusInternalServerError, "Could not update album")
		return
	}
	c.JSON(http.StatusOK, gin.H{"removed": removed, "failed": failed})
}

// Delete handles DELETE /api/album/:id.
func (h *AlbumHandler) Delete(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	a, err := h.albums.ByID(c.Request.Context(), c.Param("id"))
	if err != nil || a == nil {
		replyError(c, http.StatusNotFound, "Album not found")
		return
	}
	if a.OwnerID != user.ID && !user.IsAdmin {
		replyError(c, http.StatusForbidden, "You do not own access to this resource")
		return
	}
	if err := h.albums.Delete(c.Request.Context(), a.ID); err != nil {
		replyError(c, http.StatusInternalServerError, "Could not delete album")
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

// render loads an album (+optionally its assets) and writes 404 on miss.
func (h *AlbumHandler) render(c *gin.Context, id string, withAssets bool) *albumResponse {
	ctx := c.Request.Context()
	a, err := h.albums.ByID(ctx, id)
	if err != nil {
		replyError(c, http.StatusInternalServerError, "Could not load album")
		return nil
	}
	if a == nil {
		c.JSON(http.StatusNotFound, APIError{
			Error: "Not Found", Message: "Album not found", StatusCode: http.StatusNotFound,
		})
		return nil
	}
	resp := &albumResponse{
		ID:          a.ID,
		OwnerID:     a.OwnerID,
		Name:        a.AlbumName,
		Description: a.Description,
		CreatedAt:   a.CreatedAt.UTC(),
		UpdatedAt:   a.UpdatedAt.UTC(),
		AlbumUsers:  []map[string]any{},
		Order:       a.Order,
	}
	ids, err := h.albums.AssetIDs(ctx, a.ID)
	if err != nil {
		replyError(c, http.StatusInternalServerError, "Could not load album assets")
		return nil
	}
	resp.AssetCount = int64(len(ids))
	resp.Assets = []models.AssetResponseDto{}
	if withAssets {
		for _, aid := range ids {
			if asset, err := h.assets.ByID(ctx, aid); err == nil && asset != nil {
				resp.Assets = append(resp.Assets, asset.ToResponse())
			}
		}
	}
	return resp
}
