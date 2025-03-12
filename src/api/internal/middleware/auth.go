package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/lenaxia/llmsafespace/api/internal/interfaces"
)

// AuthMiddleware returns a middleware that handles authentication
func AuthMiddleware(authService interfaces.AuthService) gin.HandlerFunc {
	return authService.AuthMiddleware()
}
