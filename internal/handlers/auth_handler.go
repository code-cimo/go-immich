package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"go-immich/internal/models"
	"go-immich/internal/services"
)

// AuthHandler exposes POST /api/auth/login and its companions.
type AuthHandler struct {
	svc *services.AuthService
}

// NewAuthHandler wires the auth service.
func NewAuthHandler(svc *services.AuthService) *AuthHandler { return &AuthHandler{svc: svc} }

// Login handles POST /api/auth/login.
//
// Response shape (official):
//
//	{"accessToken": "...", "userId": "...", "userName": "...", "userEmail": "...", "isAdmin": true}
func (h *AuthHandler) Login(c *gin.Context) {
	var body models.LoginCredentialDTO
	if err := c.ShouldBindJSON(&body); err != nil {
		replyError(c, http.StatusBadRequest, "Invalid login credentials payload")
		return
	}

	user, token, err := h.svc.Login(c.Request.Context(), body.Email, body.Password)
	switch {
	case errors.Is(err, services.ErrInvalidCredentials):
		// Official server returns 401 with a generic message.
		c.JSON(http.StatusUnauthorized, APIError{
			Error: "Unauthorized", Message: "Login failed! Check authentication credentials",
			StatusCode: http.StatusUnauthorized,
		})
		return
	case err != nil:
		log.Printf("auth: login error: %v", err)
		replyError(c, http.StatusInternalServerError, "Login failed")
		return
	}

	c.JSON(http.StatusOK, models.LoginResponseDTO{
		AccessToken:      token,
		UserID:           user.ID,
		UserName:         user.Name,
		UserEmail:        user.Email,
		IsAdmin:          user.IsAdmin,
		ProfileImagePath: "",
	})
}

// Logout is stateless on our side (JWTs live client-side) but the endpoint
// must exist so the app can clear its session without hitting a 404.
func (h *AuthHandler) Logout(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// Info answers GET /api/auth/.info used by web clients to probe sessions.
func (h *AuthHandler) Info(c *gin.Context) {
	c.JSON(http.StatusOK, models.SessionInfoDto{HasAuthenticatedSession: false})
}

// AdminOnboardingCheck answers GET /api/admin/onboarding-check. We always look
// "fully onboarded" so the mobile app skips its setup wizard.
func (h *AuthHandler) AdminOnboardingCheck(c *gin.Context) {
	c.JSON(http.StatusOK, models.AdminOnboardingCheckDto{
		ShouldChangeAdminPassword: false,
		ShouldSetupAdmin:          false,
	})
}
