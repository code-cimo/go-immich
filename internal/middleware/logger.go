// Package middleware groups the cross-cutting HTTP concerns: structured
// access logging, CORS pre-flight handling and JWT authentication.
package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"go-immich/pkg/logger"
)

// RequestLog returns a Gin middleware that records method, path, status and
// latency for every request except noisy/cheap ones (/health, /api/health).
//
// The line format is intentionally terse (no timestamps are added here; the
// async writer sits behind stdout redirection which already orders lines):
//
//	<METHOD> <path> <status> <latency>
func RequestLog(l *logger.AsyncLogger) gin.HandlerFunc {
	// Pre-compute skip set once at registration, not per request.
	skip := map[string]struct{}{"/health": {}, "/api/health": {}}
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if _, ok := skip[path]; ok {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		latency := time.Since(start)

		status := c.Writer.Status()
		// Only surface failures verbosely; successes stay one compact line.
		line := fmt.Sprintf("%s %s %d %s", c.Request.Method, path, status, roundDur(latency))
		if len(c.Errors) > 0 {
			line += " err=" + c.Errors.String()
		}
		l.Log(line)
	}
}

// roundDur keeps log volume down by dropping sub-millisecond precision for
// slow requests while preserving it for fast ones.
func roundDur(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return d.Round(time.Microsecond).String()
	case d < time.Second:
		return d.Round(100 * time.Microsecond).String()
	default:
		return d.Round(time.Millisecond).String()
	}
}
