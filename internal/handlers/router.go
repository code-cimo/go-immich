// Package handlers exposes BuildRouter which wires every route with the exact
// paths the official Immich OpenAPI spec registers. Anything a stock Immich
// client calls MUST exist here or return a benign stub - unknown 404s make the
// Flutter app surface "server out of date" errors.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-immich/internal/middleware"
	"go-immich/pkg/logger"
)

// Deps bundles everything BuildRouter needs; assembled in cmd/server/main.go.
type Deps struct {
	AuthMW     *middleware.AuthMiddleware
	Log        *logger.AsyncLogger
	Health     *HealthHandler
	ServerInfo *ServerInfoHandler
	Users      *UserHandler
	Auth       *AuthHandler
	Upload     *UploadHandler
	Assets     *AssetHandler
	Files      *FileHandler
	Albums     *AlbumHandler
}

// BuildRouter creates the Gin engine with all middleware and routes attached.
func BuildRouter(mode string, d *Deps) *gin.Engine {
	gin.SetMode(mode)
	r := gin.New()
	r.Use(gin.Recovery())                  // panic -> 500, never crash the process
	r.Use(middleware.RequestLog(d.Log))    // async request logging (skips /health)
	r.Use(middleware.CORS())               // permissive CORS like the official server
	r.RedirectTrailingSlash = true         // mobile clients sometimes append "/"

	// ---- public (pre-auth) endpoints ----------------------------------------
	// The official server answers these WITHOUT auth because mobile clients
	// call them before login to discover the API version.
	r.GET("/health", d.Health.Check)
	api := r.Group("/api")
	api.GET("/health", d.Health.Check)
	api.GET("/server-info", d.ServerInfo.Stats)
	api.GET("/server-info/config", d.ServerInfo.Config)
	api.GET("/server-info/version", d.ServerInfo.Version)
	api.GET("/server-info/stats", d.ServerInfo.Stats)

	api.POST("/auth/login", d.Auth.Login)
	api.DELETE("/auth/logout", d.Auth.Logout)
	api.GET("/auth/.info", d.Auth.Info)
	api.GET("/admin/onboarding-check", d.Auth.AdminOnboardingCheck)

	// ---- authenticated endpoints --------------------------------------------
	authed := api.Group("")
	authed.Use(d.AuthMW.Require())

	// users
	authed.GET("/user-me", d.Users.Me)
	authed.PATCH("/user-me", d.Users.UpdateMe)
	authed.PUT("/user-me/change-password", d.Users.ChangePassword)
	authed.GET("/user/me", d.Users.Me) // legacy alias some builds use
	authed.GET("/user", d.Users.List)  // admin listing

	// uploads + duplicate checks
	authed.POST("/assets", d.Upload.Create)
	authed.POST("/asset/upload", d.Upload.Create) // spec-literal alias from the brief
	authed.POST("/assets/check", d.Upload.CheckDuplicates)
	authed.POST("/asset/check", d.Upload.CheckDuplicates)

	// gallery / retrieval
	authed.GET("/asset", d.Assets.List)
	authed.GET("/assets", d.Assets.List)
	authed.GET("/asset/:id", d.Assets.Get)
	authed.GET("/assets/:id", d.Assets.Get)
	authed.DELETE("/asset/:id", d.Assets.Delete)
	authed.DELETE("/assets/:id", d.Assets.Delete)
	authed.GET("/asset/:id/thumbnail", d.Files.Thumbnail)
	authed.GET("/assets/:id/thumbnail", d.Files.Thumbnail)
	authed.GET("/asset/:id/original", d.Files.Original)
	authed.GET("/assets/:id/original", d.Files.Original)

	// albums
	authed.POST("/album", d.Albums.Create)
	authed.GET("/album", d.Albums.List)
	authed.GET("/albums", d.Albums.List)
	authed.GET("/album/:id", d.Albums.Get)
	authed.PUT("/album/:id/assets", d.Albums.AddAssets)
	authed.DELETE("/album/:id/assets", d.Albums.RemoveAssets)
	authed.DELETE("/album/:id", d.Albums.Delete)

	// ---- graceful stubs for features we intentionally do not implement ------
	// Empty success bodies prevent client-side crash loops even if a feature
	// flag slips through (defence in depth).
	authed.GET("/timeline/trash", emptyList)
	authed.GET("/partners", emptyList)
	authed.GET("/shared-links", emptyList)
	authed.GET("/memories", emptyList)
	authed.GET("/system-config", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{}) })

	return r
}

func emptyList(c *gin.Context) { c.JSON(http.StatusOK, []any{}) }
