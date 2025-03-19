package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"regexp"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// RequestIDMiddleware adds a unique request ID to each request
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if request ID is already set
		requestID := c.GetHeader("X-Request-ID")
		
		// Validate existing request ID or generate a new one
		if requestID == "" || !uuidRegex.MatchString(requestID) {
			// Generate a new UUID v4
			id, err := uuid.NewV4()
			if err != nil {
				// Fallback to less secure but reliable method if UUID generation fails
				id, _ = uuid.FromString(uuid.Must(uuid.NewV4()).String())
			}
			requestID = id.String()
		}
		
		// Set request ID in context
		c.Set("request_id", requestID)
		
		// Set request ID in response header
		c.Writer.Header().Set("X-Request-ID", requestID)
		
		c.Next()
	}
}
