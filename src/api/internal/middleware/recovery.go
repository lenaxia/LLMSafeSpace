package middleware

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/errors"
	"github.com/lenaxia/llmsafespace/api/internal/logger"
)

// RecoveryMiddleware returns a middleware that recovers from panics
func RecoveryMiddleware(log *logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// Check for a broken connection, as it is not really a
				// condition that warrants a panic stack trace.
				var brokenPipe bool
				if ne, ok := err.(*net.OpError); ok {
					if se, ok := ne.Err.(*os.SyscallError); ok {
						if strings.Contains(strings.ToLower(se.Error()), "broken pipe") ||
							strings.Contains(strings.ToLower(se.Error()), "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				// Get stack trace
				stack := string(debug.Stack())
				
				// Log the error
				httpRequest := fmt.Sprintf("%s %s", c.Request.Method, c.Request.URL.Path)
				if brokenPipe {
					log.Error("Broken pipe", fmt.Errorf("%v", err),
						"request", httpRequest,
						"client_ip", c.ClientIP(),
						"request_id", c.GetString("request_id"),
					)
				} else {
					log.Error("Recovery from panic", fmt.Errorf("%v", err),
						"request", httpRequest,
						"client_ip", c.ClientIP(),
						"request_id", c.GetString("request_id"),
						"stack", stack,
					)
				}

				// If the connection is dead, we can't write a status to it.
				if brokenPipe {
					c.Abort()
					return
				}

				// Create API error
				apiErr := errors.NewInternalError("Internal server error", fmt.Errorf("%v", err))
				
				// Send error response
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": gin.H{
						"code":    apiErr.Code,
						"message": apiErr.Message,
					},
				})
				c.Abort()
			}
		}()
		
		c.Next()
	}
}
