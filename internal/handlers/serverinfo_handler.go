package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-immich/internal/services"
)

// ServerInfoHandler exposes the /api/server-info endpoints. The official
// server registers these WITHOUT authentication, and mobile clients call them
// before login - so we must too.
type ServerInfoHandler struct {
	svc *services.ServerInfoService
}

// NewServerInfoHandler wires the service.
func NewServerInfoHandler(svc *services.ServerInfoService) *ServerInfoHandler {
	return &ServerInfoHandler{svc: svc}
}

// Config handles GET /api/server-info/config.
func (h *ServerInfoHandler) Config(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.Config())
}

// Version handles GET /api/server-info/version.
func (h *ServerInfoHandler) Version(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.Version())
}

// Stats handles GET /api/server-info/stats. Clients read images/videos/onDisk;
// zero values are valid for a fresh install.
func (h *ServerInfoHandler) Stats(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"images":            0,
		"videos":            0,
		"usage":             0,
		"onThisServerImages": 0,
		"onThisServerVideos": 0,
		"onThisServerUsage":  0,
	})
}
