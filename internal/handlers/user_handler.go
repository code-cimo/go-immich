package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"go-immich/internal/middleware"
	"go-immich/internal/models"
	"go-immich/internal/repository"
	"go-immich/internal/services"
)

// UserHandler covers /api/user-me and /api/user endpoints.
type UserHandler struct {
	users *repository.UserRepo
	auth  *services.AuthService
}

// NewUserHandler wires dependencies.
func NewUserHandler(users *repository.UserRepo, auth *services.AuthService) *UserHandler {
	return &UserHandler{users: users, auth: auth}
}

// toResponse maps the persistence row onto the wire DTO. avatarColor keeps a
// stable value because the client uses it as fallback background when no
// profile picture exists.
func toResponse(u *models.User) models.UserResponseDto {
	return models.UserResponseDto{
		ID:               u.ID,
		Email:            u.Email,
		Name:             u.Name,
		AvatarColor:      "BLUE",
		HasProfileImage:  u.AvatarPath != "",
		ProfileImagePath: "",
		IsAdmin:          u.IsAdmin,
		IsActive:         u.IsActive,
		QuotaSizeInBytes: u.QuotaSizeInBytes,
		CreatedAt:        u.CreatedAt.UTC(),
		UpdatedAt:        u.UpdatedAt.UTC(),
		DeleteAfter:      nil,
	}
}

// Me handles GET /api/user/me (Bearer token required).
func (h *UserHandler) Me(c *gin.Context) {
	u, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	c.JSON(http.StatusOK, toResponse(u))
}

// UpdateMe handles PATCH /api/user-me (display name change).
func (h *UserHandler) UpdateMe(c *gin.Context) {
	u, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	var body models.UserInfoUpdateDto
	if err := c.ShouldBindJSON(&body); err != nil {
		replyError(c, http.StatusBadRequest, "Invalid user update payload")
		return
	}
	if body.Username != nil && *body.Username != "" {
		if err := h.users.UpdateName(c.Request.Context(), u.ID, *body.Username); err != nil {
			replyError(c, http.StatusInternalServerError, "Could not update profile")
			return
		}
	}
	fresh, err := h.users.ByID(c.Request.Context(), u.ID)
	if err != nil || fresh == nil {
		replyError(c, http.StatusInternalServerError, "Could not reload profile")
		return
	}
	c.JSON(http.StatusOK, toResponse(fresh))
}

// ChangePassword handles PUT /api/user-me/change-password.
func (h *UserHandler) ChangePassword(c *gin.Context) {
	u, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	var body models.ChangePasswordDTO
	if err := c.ShouldBindJSON(&body); err != nil {
		replyError(c, http.StatusBadRequest, "Invalid password payload")
		return
	}
	if err := h.auth.ChangePassword(c.Request.Context(), u.ID, body.Password, body.NewPassword); err != nil {
		if errors.Is(err, services.ErrInvalidCredentials) {
			replyError(c, http.StatusBadRequest, "Password does not match")
			return
		}
		replyError(c, http.StatusInternalServerError, "Could not change password")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// List handles GET /api/user (admin-only in the official API; we keep the same
// rule but tolerate non-admin callers returning only themselves is NOT done -
// clients expect the full list only for admins).
func (h *UserHandler) List(c *gin.Context) {
	u, ok := middleware.CurrentUser(c)
	if !ok {
		replyError(c, http.StatusUnauthorized, "Missing session")
		return
	}
	if !u.IsAdmin {
		replyError(c, http.StatusForbidden, "You do not own access to this resource")
		return
	}
	// Single-user deployments: return just the caller wrapped as a list.
	c.JSON(http.StatusOK, []models.UserResponseDto{toResponse(u)})
}
