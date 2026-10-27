package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS returns a permissive middleware matching the official Immich server:
// mobile apps, the web UI and CLI tools talk to the API from arbitrary origins
// (file://, device IPs, localhost during development), so "*" is required.
//
// NOTE: credentials-mode stays OFF because "*" + Allow-Credentials is invalid
// per the Fetch spec; Immich authenticates via Authorization headers, not
// cookies, for all API traffic we serve.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, Accept-encoding, Range")
		h.Set("Access-Control-Max-Age", "86400") // cache pre-flight for a day

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
