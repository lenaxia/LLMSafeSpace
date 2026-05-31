// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminGuard returns a middleware that restricts access to admin users.
// Non-admin requests receive 404 (not 403) to avoid revealing route existence.
func AdminGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, exists := c.Get("userRole")
		if !exists || role != "admin" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}
