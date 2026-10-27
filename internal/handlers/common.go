// Package handlers implements the HTTP layer. Each handler is a thin adapter:
// parse/validate input -> call a service or repository -> serialise an
// Immich-compatible DTO (or a NestJS-shaped error).
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-immich/config"
)

// APIError mirrors the body of NestJS HttpExceptions; the mobile client's
// Dio interceptor inspects statusCode/message so we must keep both keys.
type APIError struct {
	Error      string `json:"error"`
	Message    string `json:"message"`
	StatusCode int    `json:"statusCode"`
}

func replyError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, APIError{
		Error:      http.StatusText(status),
		Message:    message,
		StatusCode: status,
	})
}

// HealthHandler serves liveness probes and version advertisement.
type HealthHandler struct{ version string }

// NewHealthHandler wires the build-time version string.
func NewHealthHandler() *HealthHandler { return &HealthHandler{version: config.ServerVersion} }

// Check answers GET /api/health (and /health for container probes).
// Body matches the official {"status":"ok"} plus extra informational keys
// which clients ignore.
func (h *HealthHandler) Check(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":       "ok",
		"httpStatus":   "OK",
		"information":  gin.H{"version": h.version},
		"details":      gin.H{},
	})
}
